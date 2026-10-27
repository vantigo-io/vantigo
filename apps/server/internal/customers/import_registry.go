package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
)

// This file is the import asking Brønnøysund (Enhetsregisteret) for what a
// row of a Norwegian business leaves out: a new customer's name, and the
// postal address the file gives in part or not at all. Three things about it
// are worth saying once:
//
//   - The lookup happens before a chunk's rows are planned, never inside a
//     row: resolve stays a pure function of the row, the customer on file and
//     the answers handed to it, and no network call is made while a row's
//     transaction is open (customers foundation design D1's rule, import.go's
//     own "nothing leaves the database").
//   - Only a row that would use the answer asks (registryWanted), once per
//     organisation number per check or run, so a file whose rows are complete
//     makes no request at all and a supplier's export that repeats a company
//     on every row makes one.
//   - A registry that cannot help never fails more than it explains: the row
//     is decided as it would be without the lookup, with one more error on
//     the legal id saying why nothing was filled in (refusal). The first
//     lookup that cannot reach the registry ends the asking for the rest of
//     the run, so an outage costs one timeout, not one per company.

// importRegistryConcurrency is how many lookups of one chunk are in flight
// at once: a few, so a chunk of new companies takes seconds rather than
// minutes, and never enough to look like a burst to a public register.
const importRegistryConcurrency = 4

// importRegistryAnswer is what Brønnøysund said about one organisation number,
// or why it said nothing: Err is a registry that could not be reached;
// otherwise Outcome is the entity's (brreg_entity.go), FetchedAt when it
// answered.
type importRegistryAnswer struct {
	OrgNr     string
	Outcome   brregEntityOutcome
	Record    brregEntityRecord
	FetchedAt time.Time
	Err       error
}

// answered reports whether the registry knows the organisation — a live
// entity, or a deleted one, whose name it still gives — so its values may be
// used and its record stored.
func (a *importRegistryAnswer) answered() bool {
	return a != nil && a.Err == nil && (a.Outcome == brregEntityFound || a.Outcome == brregEntityDeleted)
}

// address is the address a row's postal address is filled from: the
// entity's postal address, falling back to its business address; nil when
// the registry has neither, or did not answer.
func (a *importRegistryAnswer) address() *brregAddress {
	if !a.answered() {
		return nil
	}
	if a.Record.PostalAddress != nil {
		return a.Record.PostalAddress
	}
	return a.Record.BusinessAddress
}

// refusal is the legal id's error on a row the registry could not help: the
// registry not reached, its answer about this organisation unusable, or the
// organisation unknown to it (a 404, or removed from open data).
func (a *importRegistryAnswer) refusal() string {
	switch {
	case errors.Is(a.Err, errBrregUnreachable):
		return "Brønnøysund could not be reached; the name and postal address were not looked up"
	case a.Err != nil:
		return fmt.Sprintf("Brønnøysund's answer about organisation %s could not be read; the name and postal address were not looked up", a.OrgNr)
	}
	return fmt.Sprintf("Brønnøysund has no organisation %s", a.OrgNr)
}

// registryAddressParts is a registry address as an address form's parts —
// the customers app's registryAddressValues (registry-address.ts), on the
// server: the first line is line 1 and the rest are joined into line 2, the
// country lowercased, Norway when the registry names none.
func registryAddressParts(a *brregAddress) (line1, line2, postalCode, city, country string) {
	if len(a.Lines) > 0 {
		line1 = a.Lines[0]
		line2 = strings.Join(a.Lines[1:], ", ")
	}
	country = strings.ToLower(a.CountryCode)
	if country == "" {
		country = importDefaultCountry
	}
	return line1, line2, a.PostalCode, a.City, country
}

// registryOrganisationNumberOf is the organisation number a row could look
// up, read from the file alone: registryOrganisationNumber's rule — a
// Norwegian business with a valid organisation number — with the import's
// defaults (country NO, type the customer type's). A row whose identity the
// file does not give is never looked up; a row whose customer type the file
// leaves to the customer on file is looked up, and resolve, which knows the
// type, decides whether the answer is used.
func (l importLayout) registryOrganisationNumberOf(raw map[string]string) string {
	if !l.mapped("legalId") {
		return ""
	}
	key, ok := identityKeyOf(raw["legalCountry"], raw["legalId"])
	if !ok || key.Country != "no" || !validNorwegianOrgNumber(key.ID) {
		return ""
	}
	if v := strings.TrimSpace(raw["type"]); v != "" {
		if t, msg := importCustomerType(v); msg != "" || t != "business" {
			return ""
		}
	}
	if v := strings.ToLower(strings.TrimSpace(raw["legalType"])); v != "" && v != "business" {
		return ""
	}
	return key.ID
}

// importRowTarget is what the lookup knows, before it asks, about what a row
// will write: a new customer, or the customer it updates — that customer's
// type and primary postal address on file.
type importRowTarget struct {
	creates bool
	typ     string
	postal  *addressSnapshot
}

// registryWanted is the organisation number a row looks up, "" when it has
// none or would not use the answer. A row that will create looks up when it
// gives no name, or no postal address, or an incomplete one; a row that will
// update a business looks up only when its postal address — the file's cells
// laid over the address on file, as resolve lays them — is incomplete, since
// an update never takes the registry's name and never replaces an address.
func (l importLayout) registryWanted(raw map[string]string, t importRowTarget) string {
	orgnr := l.registryOrganisationNumberOf(raw)
	if orgnr == "" {
		return ""
	}
	if !t.creates {
		if t.typ != "business" || !l.has(csvGroupPostal) {
			return ""
		}
		parts := l.addressParts(raw, "postal", t.postal)
		if fileimport.AllBlank(parts[:]) {
			return ""
		}
		if _, errs := validateAddress(parts.request("postal")); errs != nil {
			return orgnr
		}
		return ""
	}
	if strings.TrimSpace(raw["name"]) == "" || !l.has(csvGroupPostal) {
		return orgnr
	}
	parts := l.addressParts(raw, "postal", nil)
	if fileimport.AllBlank(parts[:]) {
		return orgnr
	}
	if _, errs := validateAddress(parts.request("postal")); errs != nil {
		return orgnr
	}
	return ""
}

// importTargetOf is what a row will write, read on q — the worker's lease
// connection, outside any row's transaction — as importRow will find it:
// the customer it matches, or a new one when the mode creates for it. ok is
// false for a row that will write nothing the registry could help with: one
// refused for its match, or an update that maps no postal field. A row's own
// transaction decides again; a customer changed in between is only a lookup
// made or missed, never a different write.
func (s *server) importTargetOf(ctx context.Context, q *store.Queries, l importLayout, p importPlan) (importRowTarget, bool, error) {
	if l.creates() {
		return importRowTarget{creates: true}, true, nil
	}
	id, found, err := s.importFind(ctx, q, l, p)
	var refused *importRowRefusal
	if errors.As(err, &refused) {
		return importRowTarget{}, false, nil
	}
	if err != nil {
		return importRowTarget{}, false, err
	}
	if !found {
		return importRowTarget{creates: true}, importCreatesUnmatched(l, p), nil
	}
	if !l.has(csvGroupPostal) {
		return importRowTarget{}, false, nil
	}
	existing, err := q.GetCustomer(ctx, id)
	if err != nil {
		return importRowTarget{}, false, err
	}
	t := importRowTarget{typ: existing.Type}
	row, err := q.PrimaryCustomerAddressOfType(ctx, store.PrimaryCustomerAddressOfTypeParams{CustomerID: id, Type: "postal"})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return importRowTarget{}, false, err
	default:
		snapshot := addressSnapshotFromRow(row)
		t.postal = &snapshot
	}
	return t, true, nil
}

// importRegistry is one check's or run's answers, by organisation number,
// and whether the registry has been found unreachable.
type importRegistry struct {
	srv     *server
	answers map[string]*importRegistryAnswer
	down    error
}

func newImportRegistry(srv *server) *importRegistry {
	return &importRegistry{srv: srv, answers: map[string]*importRegistryAnswer{}}
}

// lookup answers, by row number, the rows of one chunk that want the
// registry (registryWanted, against importTargetOf read on q), asking about
// the organisation numbers this run has not asked about yet,
// importRegistryConcurrency at a time. Only a row with an organisation number
// it could look up has its target read, so a file without one reads nothing.
// Once a lookup finds the registry unreachable (errBrregUnreachable), every
// later answer is that error without asking; an answer refused for this
// organisation alone is that answer's. Only a cancelled context or a database
// error is the run's.
func (reg *importRegistry) lookup(ctx context.Context, q *store.Queries, l importLayout, recs []fileimport.Record) (map[int]*importRegistryAnswer, error) {
	byRow := map[int]string{}
	var wanted []string
	for _, rec := range recs {
		p, _ := l.matchKey(rec)
		if l.registryOrganisationNumberOf(p.Raw) == "" {
			continue
		}
		t, ok, err := reg.srv.importTargetOf(ctx, q, l, p)
		if err != nil {
			return nil, fmt.Errorf("customers: read the customer import row %d updates: %w", rec.Row, err)
		}
		if !ok {
			continue
		}
		orgnr := l.registryWanted(p.Raw, t)
		if orgnr == "" {
			continue
		}
		byRow[rec.Row] = orgnr
		if _, asked := reg.answers[orgnr]; !asked && !slices.Contains(wanted, orgnr) {
			wanted = append(wanted, orgnr)
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, importRegistryConcurrency)
	for _, orgnr := range wanted {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			mu.Lock()
			down := reg.down
			mu.Unlock()
			answer := &importRegistryAnswer{OrgNr: orgnr, Err: down}
			if down == nil {
				answer.Record, answer.Outcome, answer.Err = reg.srv.brreg.entity(ctx, orgnr)
				answer.FetchedAt = reg.srv.deps.Clock()
			}
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(answer.Err, errBrregUnreachable) && reg.down == nil && ctx.Err() == nil {
				reg.down = answer.Err
				reg.logger().WarnContext(ctx, "customers: Brønnøysund could not be reached; the import's rows are not looked up",
					"worker", importWorkerName, "errorKind", registryErrorKind(answer.Err))
			}
			reg.answers[orgnr] = answer
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	answers := make(map[int]*importRegistryAnswer, len(byRow))
	for row, orgnr := range byRow {
		answers[row] = reg.answers[orgnr]
	}
	return answers, nil
}

// logger is the worker's logger, slog's default when the deps carry none —
// the import worker's own rule.
func (reg *importRegistry) logger() *slog.Logger {
	if reg.srv.deps.Logger != nil {
		return reg.srv.deps.Logger
	}
	return slog.Default()
}

// storeImportRegistryRecord stores the record the lookup fetched for a row
// that created a customer, or changed one's identity, to the registry's,
// inside the row's own transaction — what the create and the PUT fetch
// after their commit (fetchAndStoreRegistryRecord), without the second
// fetch, so the Registry card is filled the moment the row is.
func storeImportRegistryRecord(ctx context.Context, txq *store.Queries, customerID int32, identity *legalIdentity, answer *importRegistryAnswer, act actor) error {
	if identity == nil || identity.Source != "brreg" || !answer.answered() {
		return nil
	}
	_, err := storeRegistryAnswer(ctx, txq, customerID, answer.OrgNr, identity.Name, answer.Record, answer.Outcome, answer.FetchedAt, act)
	return err
}
