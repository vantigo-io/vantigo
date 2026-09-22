package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the customer's registry record (Brreg in full design D1, D2,
// D4): GET /customers/{id}/registry-record, POST
// /customers/{id}/registry-refresh, and the fetch-and-store both of those
// and the create/legal-identity-PUT hooks share.
//
// The record lives on customers.customer_registry_records — its own table,
// keyed by customer id, exactly as customer_peppol_lookups is and for the
// same reason (00021_customers_registry_records.sql): it is a fact about the
// world with a timestamp, not something the user owns, so writing it must
// never bump customers.revision and conflict somebody's open form. The
// customer's own name and legal identity stay what the user asserted; where
// the registry disagrees, the difference is *reported* (a registry.change
// timeline event, design D4) rather than written over them.
//
// What a refresh never does is decide anything on the record's behalf: it
// does not archive a bankrupt customer, does not rewrite the legal name, and
// does not touch customer_addresses (design D3 — an address on file may
// deliberately differ from the registry's, and a refresh must never silently
// move where mail goes). The dashboard's attention list and the Registry
// card ask a person instead.

// The four outcomes a refresh answers with (design D2), mirroring
// brreg_entity.go's brregEntityOutcome one level up as a wire string:
// "found" and "deleted" both store a record, "removed" deletes the one on
// file, "unknown" stores nothing at all.
const (
	registryStatusFound   = "found"
	registryStatusDeleted = "deleted"
	registryStatusRemoved = "removed"
	registryStatusUnknown = "unknown"
)

// registryRemovedField is the one change a "removed from open data" refresh
// reports (design D2): not a field of the record — the record is gone — but
// the fact itself, with the removal date as its "to" when the registry sent
// one.
const registryRemovedField = "removedFromOpenData"

// The text columns' widths, from the migration. Everything the registry
// sends is truncated to its column in UTF-16 units before it is stored
// (truncateUTF16, timeline.go): a navn longer than 255 characters is
// possible, and the free-text fields beside it have no length the registry
// promises at all. The four short ones (the two codes and the two
// organisation numbers) are fixed-width registry identifiers that cannot
// legitimately overflow — they are truncated all the same, so that no
// response body, however malformed, can turn a refresh into a database
// error the caller reads as a 500.
const (
	registryOrganisationNumberMax = 9
	registryNameMax               = 255
	registryFormCodeMax           = 10
	registryFormMax               = 100
	registryIndustryCodeMax       = 10
	registryIndustryMax           = 255
	registryWebsiteMax            = 2048
	registryEmailMax              = 255
	registryPhoneMax              = 30
)

// registryAddress is one of the two addresses the registry holds, as stored
// in the record's business_address/postal_address jsonb and as sent on the
// wire. The json tags are the stored shape: Lines is always an array (never
// null — an address with no street lines is still an address), and each
// empty string is simply left out, so a foreign address's row carries no
// postalCode key at all rather than an empty one.
type registryAddress struct {
	Lines        []string `json:"lines"`
	PostalCode   string   `json:"postalCode,omitempty"`
	City         string   `json:"city,omitempty"`
	Municipality string   `json:"municipality,omitempty"`
	CountryCode  string   `json:"countryCode,omitempty"`
}

// registryRecord is one stored registry record: brreg_entity.go's
// brregEntityRecord after truncation, plus the moment it was read. It is the
// one shape this file's storage, diff (registry_diff.go) and response all
// speak, so a value read back from the database and a value just fetched are
// compared as equals rather than each against its own representation.
//
// registry_updated_hint is deliberately not here: delivery B's update-feed
// worker owns that column, nothing in delivery A reads or writes it, and a
// field this file never fills would only invite a future reader to believe
// it means something.
//
// BankruptOn and LiquidationOn are stored and read back but never diffed
// (registry_diff.go) and never sent on the wire: they exist so a bankruptcy's
// attention item is dated the day the company went bankrupt rather than the
// day we last asked (design D4, fix round 2 I3), and the flag beside each of
// them is what a refresh reports — saying "bankruptOn changed" in the same
// breath as "bankrupt changed" would report one fact twice.
type registryRecord struct {
	OrganisationNumber, Name, OrganisationFormCode, OrganisationForm, IndustryCode, Industry string
	Employees                                                                                *int32
	VATRegistered, Bankrupt, UnderLiquidation, UnderForcedLiquidation                        bool
	DeletedOn, FoundedOn, BankruptOn, LiquidationOn                                          *time.Time
	Website, Email, Phone, Mobile, ParentOrganisationNumber                                  string
	BusinessAddress, PostalAddress                                                           *registryAddress
	FetchedAt                                                                                time.Time
}

// registryAddressFrom is brreg_entity.go's address translated into the
// stored shape — the same five fields, a different package-level type
// because this one carries the json tags the jsonb column is written with
// and the wire repeats.
func registryAddressFrom(a *brregAddress) *registryAddress {
	if a == nil {
		return nil
	}
	lines := a.Lines
	if lines == nil {
		lines = []string{}
	}
	return &registryAddress{
		Lines:        lines,
		PostalCode:   a.PostalCode,
		City:         a.City,
		Municipality: a.Municipality,
		CountryCode:  a.CountryCode,
	}
}

// registryRecordFrom is what the registry just said, ready to store: every
// text field truncated to its column, the addresses translated, and now as
// the moment it was read.
func registryRecordFrom(e brregEntityRecord, now time.Time) registryRecord {
	return registryRecord{
		OrganisationNumber:       truncateUTF16(e.OrganisationNumber, registryOrganisationNumberMax),
		Name:                     truncateUTF16(e.Name, registryNameMax),
		OrganisationFormCode:     truncateUTF16(e.OrganisationFormCode, registryFormCodeMax),
		OrganisationForm:         truncateUTF16(e.OrganisationForm, registryFormMax),
		IndustryCode:             truncateUTF16(e.IndustryCode, registryIndustryCodeMax),
		Industry:                 truncateUTF16(e.Industry, registryIndustryMax),
		Employees:                e.Employees,
		VATRegistered:            e.VATRegistered,
		Bankrupt:                 e.Bankrupt,
		UnderLiquidation:         e.UnderLiquidation,
		UnderForcedLiquidation:   e.UnderForcedLiquidation,
		DeletedOn:                e.DeletedOn,
		FoundedOn:                e.FoundedOn,
		BankruptOn:               e.BankruptOn,
		LiquidationOn:            e.LiquidationOn,
		Website:                  truncateUTF16(e.Website, registryWebsiteMax),
		Email:                    truncateUTF16(e.Email, registryEmailMax),
		Phone:                    truncateUTF16(e.Phone, registryPhoneMax),
		Mobile:                   truncateUTF16(e.Mobile, registryPhoneMax),
		ParentOrganisationNumber: truncateUTF16(e.ParentOrganisationNumber, registryOrganisationNumberMax),
		BusinessAddress:          registryAddressFrom(e.BusinessAddress),
		PostalAddress:            registryAddressFrom(e.PostalAddress),
		FetchedAt:                now,
	}
}

// deletedRegistryRecordFrom is registryRecordFrom for a struck-off entity
// (brregEntityDeleted): the registry's reduced body carries only the name,
// the organisation number and the deletion date, so everything else is
// carried over from the record on file rather than blanked. A company that
// goes under keeps the industry, headcount and address it had on the day it
// did — reporting all of those as "changed to nothing" in the same breath as
// its deletion would be noise, and losing them would throw away the last
// picture anyone has of it. With no record on file there is nothing to carry
// over, and the zero values (no employees, every flag false) stand.
//
// A SlettetEnhet body with no slettedato at all is dated the day of the fetch
// (fix round 2, minors): the respons_klasse is the load-bearing fact — this
// company is struck from the register — and a record carrying that fact with
// no date would be reported as no change whatsoever and raise no attention
// item, which is the one outcome that must not happen. The fetch date is the
// best anyone here knows — and it stands in once: a later undated body keeps
// the date already on file, because deletedOn is a diffed field and the
// attention item's occurredAt, and re-stamping it would write a false
// registry.change every day and re-float a settled deletion on every click.
func deletedRegistryRecordFrom(before *registryRecord, e brregEntityRecord, now time.Time) registryRecord {
	rec := registryRecord{}
	if before != nil {
		rec = *before
	}
	rec.OrganisationNumber = truncateUTF16(e.OrganisationNumber, registryOrganisationNumberMax)
	rec.Name = truncateUTF16(e.Name, registryNameMax)
	switch {
	case e.DeletedOn != nil:
		rec.DeletedOn = e.DeletedOn
	case rec.DeletedOn == nil:
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		rec.DeletedOn = &day
	}
	rec.FetchedAt = now
	return rec
}

// registryAddressColumn encodes an address for its jsonb column, nil (SQL
// NULL) when there is none — never a "null" literal inside the column, so
// "the registry holds no postal address" reads the same in SQL as it does in
// Go.
func registryAddressColumn(a *registryAddress) ([]byte, error) {
	if a == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("customers: encode registry address: %w", err)
	}
	return encoded, nil
}

// registryAddressFromColumn decodes one jsonb address column. A NULL or
// empty column is no address; anything else that will not decode is a real
// error — this module wrote the column itself, so a body it cannot read back
// is a bug, not a shrug.
func registryAddressFromColumn(raw []byte) (*registryAddress, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var a registryAddress
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("customers: decode registry address: %w", err)
	}
	if a.Lines == nil {
		a.Lines = []string{}
	}
	return &a, nil
}

// registryOptional is a text column's stored value: nil for the empty
// string, so a field the registry does not have is NULL in the row and
// absent from the wire, never "".
func registryOptional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// registryDateColumn is a *time.Time as its nullable date column.
func registryDateColumn(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

// registryDateFromColumn is the reverse: a nullable date column as a
// *time.Time.
func registryDateFromColumn(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

// registryRecordFromRow is a stored row as a registryRecord — the shape
// everything above the store speaks.
func registryRecordFromRow(row store.CustomersCustomerRegistryRecord) (registryRecord, error) {
	business, err := registryAddressFromColumn(row.BusinessAddress)
	if err != nil {
		return registryRecord{}, err
	}
	postal, err := registryAddressFromColumn(row.PostalAddress)
	if err != nil {
		return registryRecord{}, err
	}
	return registryRecord{
		OrganisationNumber:       row.OrganisationNumber,
		Name:                     row.Name,
		OrganisationFormCode:     deref(row.OrganisationFormCode),
		OrganisationForm:         deref(row.OrganisationForm),
		IndustryCode:             deref(row.IndustryCode),
		Industry:                 deref(row.Industry),
		Employees:                row.Employees,
		VATRegistered:            row.VatRegistered,
		Bankrupt:                 row.Bankrupt,
		UnderLiquidation:         row.UnderLiquidation,
		UnderForcedLiquidation:   row.UnderForcedLiquidation,
		DeletedOn:                registryDateFromColumn(row.DeletedOn),
		FoundedOn:                registryDateFromColumn(row.FoundedOn),
		BankruptOn:               registryDateFromColumn(row.BankruptOn),
		LiquidationOn:            registryDateFromColumn(row.LiquidationOn),
		Website:                  deref(row.Website),
		Email:                    deref(row.Email),
		Phone:                    deref(row.Phone),
		Mobile:                   deref(row.Mobile),
		ParentOrganisationNumber: deref(row.ParentOrganisationNumber),
		BusinessAddress:          business,
		PostalAddress:            postal,
		FetchedAt:                row.FetchedAt,
	}, nil
}

// registryUpsertParams is a registryRecord as the upsert's parameters.
func registryUpsertParams(customerID int32, rec registryRecord) (store.UpsertCustomerRegistryRecordParams, error) {
	business, err := registryAddressColumn(rec.BusinessAddress)
	if err != nil {
		return store.UpsertCustomerRegistryRecordParams{}, err
	}
	postal, err := registryAddressColumn(rec.PostalAddress)
	if err != nil {
		return store.UpsertCustomerRegistryRecordParams{}, err
	}
	return store.UpsertCustomerRegistryRecordParams{
		CustomerID:               customerID,
		OrganisationNumber:       rec.OrganisationNumber,
		Name:                     rec.Name,
		OrganisationFormCode:     registryOptional(rec.OrganisationFormCode),
		OrganisationForm:         registryOptional(rec.OrganisationForm),
		IndustryCode:             registryOptional(rec.IndustryCode),
		Industry:                 registryOptional(rec.Industry),
		Employees:                rec.Employees,
		VatRegistered:            rec.VATRegistered,
		Bankrupt:                 rec.Bankrupt,
		UnderLiquidation:         rec.UnderLiquidation,
		UnderForcedLiquidation:   rec.UnderForcedLiquidation,
		DeletedOn:                registryDateColumn(rec.DeletedOn),
		FoundedOn:                registryDateColumn(rec.FoundedOn),
		BankruptOn:               registryDateColumn(rec.BankruptOn),
		LiquidationOn:            registryDateColumn(rec.LiquidationOn),
		Website:                  registryOptional(rec.Website),
		Email:                    registryOptional(rec.Email),
		Phone:                    registryOptional(rec.Phone),
		Mobile:                   registryOptional(rec.Mobile),
		ParentOrganisationNumber: registryOptional(rec.ParentOrganisationNumber),
		BusinessAddress:          business,
		PostalAddress:            postal,
		FetchedAt:                rec.FetchedAt,
	}, nil
}

// registryAddressResponse is one address on the wire. An empty string is
// omitted the same way it is left out of the stored jsonb — except
// countryCode, which the contract makes required and which therefore ships
// as "" on the rare foreign address the registry sent no landkode for.
func registryAddressResponse(a *registryAddress) *gen.CustomerRegistryAddress {
	if a == nil {
		return nil
	}
	lines := a.Lines
	if lines == nil {
		lines = []string{}
	}
	return &gen.CustomerRegistryAddress{
		Lines:        lines,
		PostalCode:   registryOptional(a.PostalCode),
		City:         registryOptional(a.City),
		Municipality: registryOptional(a.Municipality),
		CountryCode:  a.CountryCode,
	}
}

// registryRecordResponse is CustomerRegistryRecord on the wire.
func registryRecordResponse(rec registryRecord) gen.CustomerRegistryRecord {
	return gen.CustomerRegistryRecord{
		OrganisationNumber:       rec.OrganisationNumber,
		Name:                     rec.Name,
		OrganisationFormCode:     registryOptional(rec.OrganisationFormCode),
		OrganisationForm:         registryOptional(rec.OrganisationForm),
		IndustryCode:             registryOptional(rec.IndustryCode),
		Industry:                 registryOptional(rec.Industry),
		Employees:                rec.Employees,
		VatRegistered:            rec.VATRegistered,
		Bankrupt:                 rec.Bankrupt,
		UnderLiquidation:         rec.UnderLiquidation,
		UnderForcedLiquidation:   rec.UnderForcedLiquidation,
		DeletedOn:                registryDateResponse(rec.DeletedOn),
		FoundedOn:                registryDateResponse(rec.FoundedOn),
		Website:                  registryOptional(rec.Website),
		Email:                    registryOptional(rec.Email),
		Phone:                    registryOptional(rec.Phone),
		Mobile:                   registryOptional(rec.Mobile),
		ParentOrganisationNumber: registryOptional(rec.ParentOrganisationNumber),
		BusinessAddress:          registryAddressResponse(rec.BusinessAddress),
		PostalAddress:            registryAddressResponse(rec.PostalAddress),
		FetchedAt:                rec.FetchedAt,
	}
}

// registryDateResponse is a registry date on the wire, absent when the
// registry has none — dateFromPgtype's counterpart for a *time.Time
// (customers.go).
func registryDateResponse(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	return &openapi_types.Date{Time: *t}
}

// registryChangeResponses is the diff on the wire: an empty from/to is
// omitted, so a field that was not set before carries only its new value.
func registryChangeResponses(changes []registryChange) []gen.CustomerRegistryChange {
	out := make([]gen.CustomerRegistryChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, gen.CustomerRegistryChange{
			Field: c.Field,
			From:  registryOptional(c.From),
			To:    registryOptional(c.To),
		})
	}
	return out
}

// registryOrganisationNumber is the organisation number a refresh would read
// the registry for, "" when the customer has none to read one for (design
// D2's 409): the customer's legal identity must be a Norwegian business
// whose id passes validNorwegianOrgNumber — the same three conditions
// derivedPeppolID applies (billing_values.go), and for the same reason, a
// legacy row can hold something like "NO 923 609 016 MVA" that no registry
// lookup could ever succeed for.
//
// The source is deliberately not one of them: design D2 says any source, so
// a manually entered, valid organisation number can be enriched from the
// registry too. The create/legal-identity hooks below add the source check
// themselves, because *that* is a Brreg pick, not an enrichment.
func registryOrganisationNumber(identity *legalIdentity, customerType string) string {
	if identity == nil || identity.Country != "no" || customerType != "business" {
		return ""
	}
	if !validNorwegianOrgNumber(identity.ID) {
		return ""
	}
	return identity.ID
}

// brregPickOrganisationNumber is registryOrganisationNumber plus the source
// check the two write hooks add (design D2): a fetch on create or on a
// legal-identity PUT happens for a Brreg *pick* — an identity the user chose
// out of the registry — not for every Norwegian organisation number someone
// types in. A manual identity is enriched when a person asks for it, through
// the refresh endpoint, and never behind their back on a save.
func brregPickOrganisationNumber(identity *legalIdentity, customerType string) string {
	if identity == nil || identity.Source != "brreg" {
		return ""
	}
	return registryOrganisationNumber(identity, customerType)
}

// registryRefreshResult is one completed fetch-and-store: which of the four
// outcomes it was, the stored record afterwards (nil for removed and
// unknown, which store none) and what differed from the record on file.
type registryRefreshResult struct {
	Status  string
	Record  *registryRecord
	Changes []registryChange
}

// registryErrorKind classifies a failed fetch into a small, fixed set of
// kinds for the warning logs below, rather than logging err.Error() itself —
// peppolErrorKind's reasoning (peppol_lookup.go) applies unchanged: the
// error can carry the organisation number and the URL it was built from, and
// neither belongs in a log line next to the customer id that caused it.
func registryErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errBrregUnavailable):
		return "registry_unavailable"
	default:
		return registryErrorKindDatabase
	}
}

// registryErrorKindDatabase is the one kind whose own error text is safe to
// log beside it (logRegistryFetchFailure): it is this module's own wrapped
// error, carrying neither an organisation number nor a URL.
const registryErrorKindDatabase = "database"

// registryRefreshUnavailableResponse is the 502 a refresh answers when the
// registry itself could not be reached (design D2: "502 when Brreg cannot be
// reached, as the lookup"), worded as GetCustomersLookupBrreg's own
// brregUnavailableResponse is — the two failures are the same failure.
func registryRefreshUnavailableResponse() gen.PostCustomersByIdRegistryRefresh502ApplicationProblemPlusJSONResponse {
	return gen.PostCustomersByIdRegistryRefresh502ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
		"Registry unavailable",
		"The Brønnøysundregisteret entity register could not be reached. Please try again later.",
		http.StatusBadGateway,
	))
}

// noRegistryIdentityConflict is the 409 a refresh answers for a customer
// there is nothing to refresh for (design D2): a person, a foreign identity,
// no identity at all, or an organisation number that is not one. It carries
// a code, unlike the revision conflict (customers.go's
// customerRevisionConflict), because a client can act on this one — the fix
// is to give the customer a Norwegian organisation number.
func noRegistryIdentityConflict() gen.CustomerConflictProblem {
	title := "No registry identity"
	detail := "This customer has no Norwegian organisation number to look up in the registry."
	code := "no_registry_identity"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// registryHookTimeout is how long a create or an identity PUT may wait on the
// registry (fix round 2, I1): one attempt's worth, not BRREG_TIMEOUT's fifteen
// seconds. The record is a bonus on those two requests — the write has already
// committed — so a registry that is slow must cost a moment, not the
// perceptible pause before a 201, and the Refresh button is the retry. The
// refresh endpoint keeps the full BRREG_TIMEOUT: someone is waiting for that
// answer on purpose.
//
// A var, not a const, only so a test can shorten it (export_test.go's
// SetRegistryHookTimeout); nothing at runtime writes it.
var registryHookTimeout = brregAttemptTimeout

// fetchAndStoreRegistryRecord is the create/legal-identity-PUT hook (design
// D2): one fetch, stored, with whatever it changed recorded on the timeline,
// for a customer whose identity was just picked from — or pointed at — the
// registry. It runs after the caller's own transaction has committed and
// returns the error rather than acting on it, because neither caller may
// fail its request for it: the customer exists, the record is simply absent,
// and the Registry card offers a Refresh.
//
// It runs on context.WithoutCancel with its own registryHookTimeout deadline
// rather than on the request's context (fix round 2, I1): the write is
// committed, so a client that hangs up must not abort the fetch that follows
// it, and the deadline that does bound it is one attempt's worth rather than
// the whole BRREG_TIMEOUT budget the refresh endpoint spends.
func (s *server) fetchAndStoreRegistryRecord(ctx context.Context, customerID int32, orgnr, legalName string, act actor) error {
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), registryHookTimeout)
	defer cancel()
	_, err := s.refreshRegistryRecord(hookCtx, customerID, orgnr, legalName, act)
	return err
}

// logRegistryFetchFailure is the one warning the two write hooks answer a
// failed fetch with (design D2: logged and dropped, never returned). The kind
// alone is logged, for peppolErrorKind's reason — the error can carry the
// organisation number and the URL it was built from, and neither belongs in a
// log line next to the customer id that caused it — except for a "database"
// kind, which is this module's own wrapped error and carries neither, and
// whose text is the only thing that would say what actually broke.
func (s *server) logRegistryFetchFailure(ctx context.Context, customerID int32, err error) {
	kind := registryErrorKind(err)
	if kind == registryErrorKindDatabase {
		s.deps.Logger.WarnContext(ctx, "customers: registry record fetch failed",
			"customerId", customerID, "errorKind", kind, "error", err.Error())
		return
	}
	s.deps.Logger.WarnContext(ctx, "customers: registry record fetch failed", "customerId", customerID, "errorKind", kind)
}

// invalidateRegistryRecord deletes a stored record that is no longer this
// customer's company's (fix round 2, C2), called from inside every
// transaction that writes the legal identity — PutCustomersById,
// PutCustomersByIdLegalIdentity, DeleteCustomersByIdLegalIdentity and
// PutCustomersByIdType — once that handler has established the identity
// actually changed, and after the write that holds the customer row's own
// lock.
//
// The rule is the organisation number, not the identity: pointing a customer
// at a different company leaves the old company's record on file, where a
// refresh would answer 409 forever (there is nothing to look up for a person,
// or the new number never matches the stored row) while the stale row keeps
// raising attention items about a company this customer is not. The same
// number reached a different way — a manual identity re-picked from Brreg, a
// name-only edit — keeps the record: it is still a record of this company.
//
// A locked read is fine here: the caller already holds the customer row.
// A missing row is not an error, and neither is a customer that never had one.
func invalidateRegistryRecord(ctx context.Context, q *store.Queries, customerID int32, after *legalIdentity, customerType string) error {
	row, err := q.GetCustomerRegistryRecordForUpdate(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.OrganisationNumber == registryOrganisationNumber(after, customerType) {
		return nil
	}
	return q.DeleteCustomerRegistryRecord(ctx, customerID)
}

// refreshRegistryRecord is one fetch-and-store, the whole of it: the network
// call first, outside any transaction (design D2's controller ruling —
// customers foundation design D1's rule that no out-of-process call happens
// under a lock), then one transaction that locks the customer, reads the
// record on file, writes what the registry said and records at most one
// registry.change event for what differed.
//
// The customer row is locked first (LockCustomer's FOR NO KEY UPDATE, the
// same lock every address write takes — queries/addresses.sql), because the
// record's own FOR UPDATE cannot serialize the case that needs it most: on a
// first fetch there is no record row to lock, so two refreshes racing each
// other would both see "nothing on file", both diff against the legal name
// and both write their own event. Locking the customer makes refreshes of
// one customer queue behind each other, so the second one diffs against what
// the first actually stored. It is never held across the network call — that
// has already returned by the time this transaction opens.
//
// A failed network call returns the error with nothing stored and nothing
// recorded: the record on file, however old, is a better answer than none.
// Each of the four outcomes is a real answer about a real company, never an
// error — see brreg_entity.go's own doc comment for why the registry has
// three shapes of "gone" and only one of them is a failure.
func (s *server) refreshRegistryRecord(ctx context.Context, customerID int32, orgnr, legalName string, act actor) (registryRefreshResult, error) {
	entity, outcome, err := s.brreg.entity(ctx, orgnr)
	if err != nil {
		return registryRefreshResult{}, err
	}
	// Read once the network call has returned, not before it was made: the
	// call takes real time, and fetchedAt says when the registry answered,
	// not when it was asked (peppol_lookup.go's checkedAt, same rule).
	now := s.deps.Clock()

	// The registry does not know this organisation number: nothing to store,
	// nothing to report, and deliberately not an error — the identity is
	// what needs a person's attention, and the 200 says so.
	if outcome == brregEntityUnknown {
		return registryRefreshResult{Status: registryStatusUnknown}, nil
	}

	var result registryRefreshResult
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, customerID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The customer went away between this request's own 404 check
				// and here. A customer is archived, never hard-deleted, so this
				// is all but unreachable — but a refresh must answer the same
				// 404 the address writes do rather than a 500.
				return errCustomerNotFound
			}
			return err
		}
		before, err := lockedRegistryRecord(ctx, txq, customerID)
		if err != nil {
			return err
		}

		if outcome == brregEntityRemoved {
			// Removed from open data: the copy goes (Brreg's own terms), and
			// the only thing kept is the fact that it left, and when. There is
			// no record to return and no before/after to diff — the one change
			// reported is the removal itself.
			//
			// With nothing on file there is nothing to remove and nothing to
			// report: the status still says "removed", so the caller learns
			// what the registry answered, but the timeline stays quiet. That is
			// what keeps a second click (or a first refresh of a customer whose
			// record was never fetched) from writing a duplicate entry about a
			// disappearance that had already happened.
			if before == nil {
				result = registryRefreshResult{Status: registryStatusRemoved}
				return nil
			}
			if err := txq.DeleteCustomerRegistryRecord(ctx, customerID); err != nil {
				return err
			}
			changes := []registryChange{{Field: registryRemovedField, To: registryDateDisplay(entity.DeletedOn)}}
			result = registryRefreshResult{Status: registryStatusRemoved, Changes: changes}
			return recordRegistryRemoved(ctx, txq, now, customerID, changes, act.Kind, act.Display, act.UserID)
		}

		after := registryRecordFrom(entity, now)
		status := registryStatusFound
		if outcome == brregEntityDeleted {
			after = deletedRegistryRecordFrom(before, entity, now)
			status = registryStatusDeleted
		}
		changes := diffRegistryRecords(before, legalName, after)

		params, err := registryUpsertParams(customerID, after)
		if err != nil {
			return err
		}
		if err := txq.UpsertCustomerRegistryRecord(ctx, params); err != nil {
			return err
		}
		stored := after
		result = registryRefreshResult{Status: status, Record: &stored, Changes: changes}
		if len(changes) == 0 {
			return nil
		}
		return recordRegistryChange(ctx, txq, now, customerID, changes, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return registryRefreshResult{}, fmt.Errorf("customers: store registry record: %w", err)
	}
	return result, nil
}

// lockedRegistryRecord reads the record on file inside the refresh's own
// transaction, holding it for the write that follows: nil when there is none
// (pgx.ErrNoRows — "never fetched" is not an error, it is the first-fetch
// case diffRegistryRecords compares against the legal name instead).
func lockedRegistryRecord(ctx context.Context, q *store.Queries, customerID int32) (*registryRecord, error) {
	row, err := q.GetCustomerRegistryRecordForUpdate(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec, err := registryRecordFromRow(row)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetCustomersByIdRegistryRecord Get what the registry says about this
// customer (GET /api/v1/customers/{id}/registry-record)
//
// 404 when the customer does not exist, 200 with the record when there is
// one, 204 otherwise — the same three answers GetCustomersByIdLegalIdentity
// gives (legal_identity.go), and for the same reason: "nothing to show" is
// not an error.
//
// A caller without customers:legal-identity-view gets the 204 too (design
// D2's controller ruling). The record repeats the legal identity's
// organisation number and is the registry's view of the very entity that
// permission gates, so it is withheld exactly as the customer response's
// identity block is — as a shape, never a 403. The two 204s are deliberately
// indistinguishable: a caller who may not see the record has no business
// learning whether one exists.
func (s *server) GetCustomersByIdRegistryRecord(ctx context.Context, req gen.GetCustomersByIdRegistryRecordRequestObject) (gen.GetCustomersByIdRegistryRecordResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdRegistryRecord404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if !s.hasPermission(ctx, legalIdentityView) {
		return gen.GetCustomersByIdRegistryRecord204Response{}, nil
	}

	row, err := q.GetCustomerRegistryRecord(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdRegistryRecord204Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer registry record: %w", err)
	}
	// A record is only this customer's while it is still the company its legal
	// identity names (fix round 2, C2): an identity change deletes a record it
	// no longer matches, and this guard is the belt to that braces — a row that
	// slipped through a race, or a write path added later, answers the same 204
	// "nothing to show" rather than another company's details.
	if row.OrganisationNumber != deref(existing.LegalID) {
		return gen.GetCustomersByIdRegistryRecord204Response{}, nil
	}
	rec, err := registryRecordFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.GetCustomersByIdRegistryRecord200JSONResponse(registryRecordResponse(rec)), nil
}

// PostCustomersByIdRegistryRefresh Re-read this customer's registry record
// (POST /api/v1/customers/{id}/registry-refresh)
//
// Ordering (design D2's controller ruling, plus fix round 2's throttle): 404
// → the identity check, 409 when there is no Norwegian organisation number to
// look up → the throttle, which answers the stored record for a click inside
// registryRefreshMinInterval of the last fetch and makes no call at all → the
// timeline actor, resolved before the network call because actorFor's
// directory lookup is itself an out-of-process call, and only once a write can
// actually happen → the fetch, outside any transaction → 502 on failure, with
// nothing stored → the transaction that stores what the registry said and
// records what differed.
//
// Unlike the create hook above, this one reports every failure it meets: a
// user who clicked Refresh is waiting for the answer.
func (s *server) PostCustomersByIdRegistryRefresh(ctx context.Context, req gen.PostCustomersByIdRegistryRefreshRequestObject) (gen.PostCustomersByIdRegistryRefreshResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersByIdRegistryRefresh404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	identity := identityFromRow(existing.LegalCountry, existing.LegalID, existing.LegalName, existing.LegalSource, existing.LegalType)
	orgnr := registryOrganisationNumber(identity, existing.Type)
	if orgnr == "" {
		return gen.PostCustomersByIdRegistryRefresh409ApplicationProblemPlusJSONResponse(noRegistryIdentityConflict()), nil
	}

	throttled, err := s.throttledRegistryRefresh(ctx, q, req.Id, orgnr)
	if err != nil {
		return nil, err
	}
	if throttled != nil {
		return *throttled, nil
	}

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	result, err := s.refreshRegistryRecord(ctx, req.Id, orgnr, identity.Name, act)
	if errors.Is(err, errBrregUnavailable) {
		s.deps.Logger.WarnContext(ctx, "customers: registry refresh failed", "customerId", req.Id, "errorKind", registryErrorKind(err))
		return registryRefreshUnavailableResponse(), nil
	}
	if errors.Is(err, errCustomerNotFound) {
		// The customer disappeared under the transaction's own lock (see
		// refreshRegistryRecord): the same 404 the read above would have given
		// a moment earlier.
		return gen.PostCustomersByIdRegistryRefresh404Response{}, nil
	}
	if err != nil {
		return nil, err
	}

	resp := gen.CustomerRegistryRefreshResponse{
		Status:  result.Status,
		Changes: registryChangeResponses(result.Changes),
	}
	if result.Record != nil {
		record := registryRecordResponse(*result.Record)
		resp.Record = &record
	}
	return gen.PostCustomersByIdRegistryRefresh200JSONResponse(resp), nil
}

// registryRefreshMinInterval is how long a stored record stands before a
// second click is allowed to spend another outbound request on it (fix round
// 2, I2). The endpoint is one GET per click on an open API whose 429 is not
// retryable and would surface as a 502, and a registry record does not change
// twice a minute — so a refresh inside the window answers what is already on
// file, unchanged, rather than asking again.
const registryRefreshMinInterval = 60 * time.Second

// throttledRegistryRefresh is that window: the stored record read unlocked
// (this path writes nothing), and a ready 200 when it is younger than
// registryRefreshMinInterval — the record as it stands, no changes, and the
// status the stored row itself implies. nil means "go ahead and fetch".
//
// Three cases deliberately fall through to a real fetch. No record at all:
// there is nothing to answer with, which is also why a removal (which deletes
// the row) is never throttled — the click after a 410 asks the registry again,
// as it should. A record whose organisation number is not the customer's any
// more: it is another company's and must not be handed back (C2's guard).
// And of course a record older than the window.
func (s *server) throttledRegistryRefresh(ctx context.Context, q *store.Queries, customerID int32, orgnr string) (*gen.PostCustomersByIdRegistryRefresh200JSONResponse, error) {
	row, err := q.GetCustomerRegistryRecord(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer registry record: %w", err)
	}
	if row.OrganisationNumber != orgnr {
		return nil, nil
	}
	if s.deps.Clock().Sub(row.FetchedAt) >= registryRefreshMinInterval {
		return nil, nil
	}

	rec, err := registryRecordFromRow(row)
	if err != nil {
		return nil, err
	}
	status := registryStatusFound
	if rec.DeletedOn != nil {
		status = registryStatusDeleted
	}
	record := registryRecordResponse(rec)
	resp := gen.PostCustomersByIdRegistryRefresh200JSONResponse(gen.CustomerRegistryRefreshResponse{
		Status:  status,
		Changes: registryChangeResponses(nil),
		Record:  &record,
	})
	return &resp, nil
}
