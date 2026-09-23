package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file is the module's second Enhetsregisteret operation:
// where brreg.go's lookup searches for candidates by name or organisation
// number, (*brregClient).entity fetches one entity's full record — the
// registry's complete view of a single organisation, keyed by the
// organisation number a caller already has (from a pick, or from the
// customer's own legal identity).
//
// entity has four defined outcomes, not two, because the registry itself
// does not answer "found or not": an organisation can be deleted (struck
// from the register but still on file) or removed (struck from *open data*
// entirely — the record itself is gone, only the fact that it once existed
// and when it left remains). Both are legitimate answers about a real
// company, not failures, so both are outcomes rather than errors.
//
// The deleted case is the registry's own quirk, verified against the live
// API: a deleted entity answers HTTP 200 — an ordinary success — with a
// reduced body carrying "respons_klasse": "SlettetEnhet". There is no
// status code for "deleted"; the only way to tell a deleted entity from a
// live one is to look inside a 200 body most callers would otherwise decode
// without a second glance. Getting this wrong (branching on status alone)
// would silently store a struck-off company as if it were still trading.
// "Removed" is the opposite shape: an actual non-2xx status, 410 Gone, with
// its own small body naming when the removal happened. "Unknown" is a plain
// 404 with an empty body — never retried, like a 410, because retrying a
// definite answer about *this* organisation number would only waste the
// budget a genuine outage needs.
const (
	// brregEntityMediaType is the pinned Enhetsregisteret v2 representation
	// (the design's "What the registry looks like": v1 is gone, a request for
	// it now answers 406). Sent as the Accept header on every attempt and
	// checked, alongside plain application/json, against the response's own
	// Content-Type before any byte of the body is trusted as this shape.
	brregEntityMediaType = "application/vnd.brreg.enhetsregisteret.enhet.v2+json"

	// brregEntityMaxBodyBytes caps a single entity response at 1 MiB. The
	// registry's own records are a few kilobytes at most; a response far
	// past that is not a bigger company, it is something to refuse rather
	// than buffer in full.
	brregEntityMaxBodyBytes = 1 << 20

	// brregEntityRead is what this read is called in an error message
	// (validateBrregContentType, readBrregBody), the counterpart of
	// brregFeedRead's own constant (brreg_feed.go, final fix wave M11): the two
	// reads' failures are told apart in a log by this word, so it is named in
	// both places rather than spelled as a literal in one of them.
	brregEntityRead = "entity"
)

// brregAddress is one of the registry's two address shapes on an entity
// (forretningsadresse, postadresse), kept close to the wire: Lines is the
// registry's own free-form "adresse" array (a street address is one or more
// lines, trimmed, with blank lines dropped), and PostalCode/Municipality
// are empty on a foreign address — the registry only assigns a Norwegian
// post code and municipality, so a foreign address carries its own postal
// district as part of City instead (e.g. "81-336 GDYNIA").
type brregAddress struct {
	Lines        []string
	PostalCode   string
	City         string
	Municipality string
	CountryCode  string
}

// brregEntityRecord is the subset of one Enhetsregisteret entity this
// module keeps (D1's customer_registry_records table, Task 2): the fields a
// customer record can actually use, not every field the registry returns
// (free-text purpose/activity, roles, sub-entities and the rest stay out —
// this file's design doc "Out of scope"). Employees is nil whenever the
// registry has not registered a headcount at all (harRegistrertAntallAnsatte
// false) — distinct from a registered headcount of zero, which the registry
// does not appear to send but this type would still represent correctly as
// a non-nil pointer to 0 rather than collapsing it into "unknown".
// BankruptOn and LiquidationOn are when each of those two flags became true
// (konkursdato, underAvviklingDato) — nil when the registry sends the flag
// without a date, which it does. The flag is the load-bearing fact; the date
// is what the dashboard's attention item is dated by, so that a bankruptcy
// from years ago does not read as having happened on the day it was last
// fetched (design D4, fix round 2).
type brregEntityRecord struct {
	OrganisationNumber, Name, OrganisationFormCode, OrganisationForm, IndustryCode, Industry string
	Employees                                                                                *int32
	VATRegistered, Bankrupt, UnderLiquidation, UnderForcedLiquidation                        bool
	DeletedOn, FoundedOn, BankruptOn, LiquidationOn                                          *time.Time
	Website, Email, Phone, Mobile, ParentOrganisationNumber                                  string
	BusinessAddress, PostalAddress                                                           *brregAddress
}

// brregEntityOutcome is entity's answer about which of the registry's three
// shapes the response was, once a request has actually reached and
// understood the registry (an outcome is only meaningful alongside a nil
// error; entity never returns a mix of a real outcome and a non-nil error).
type brregEntityOutcome int

const (
	// brregEntityFound is an ordinary live entity: the full record is
	// populated from the response.
	brregEntityFound brregEntityOutcome = iota
	// brregEntityDeleted is a 200 response whose body is "SlettetEnhet" — see
	// this file's doc comment for why that is a body to inspect, not a
	// status to branch on. Only Name, OrganisationNumber and DeletedOn are
	// populated on the returned record; everything else the registry might
	// still echo (such as the organisation form) is deliberately left zero,
	// because a struck-off company's form is not a fact this module stores.
	brregEntityDeleted
	// brregEntityRemoved is a 410: gone from open data entirely. DeletedOn is
	// populated from the body's slettedato when the registry sends one.
	brregEntityRemoved
	// brregEntityUnknown is a plain 404: no entity ever existed at this
	// organisation number, or it is a sub-entity's number (the design doc:
	// "A sub-entity's number on /enheter: 404"). It doubles as the sentinel
	// returned alongside a non-nil error (not the type's zero value — that is
	// brregEntityFound) — callers must check the error first, since the
	// outcome carries no meaning there.
	brregEntityUnknown
)

// brregEntityPath is the request path for one entity
// (GET {base}/enhetsregisteret/api/enheter/{orgnr}), sharing brregPath with
// the search in brreg.go — the same collection, one more segment.
func brregEntityPath(orgnr string) string {
	return brregPath + "/" + url.PathEscape(orgnr)
}

// brregAddressWire is forretningsadresse/postadresse exactly as the
// registry sends it: adresse is an array of lines (never a single string),
// and postnummer/kommune are simply absent on a foreign address rather than
// sent empty.
type brregAddressWire struct {
	Adresse    []string `json:"adresse"`
	Postnummer string   `json:"postnummer"`
	Poststed   string   `json:"poststed"`
	Kommune    string   `json:"kommune"`
	Landkode   string   `json:"landkode"`
}

// brregCodeWire is the {kode, beskrivelse} shape the registry uses for both
// organisasjonsform and naeringskode1 — one wire type for both since the
// shape, and this file's handling of it, is identical.
type brregCodeWire struct {
	Kode        string `json:"kode"`
	Beskrivelse string `json:"beskrivelse"`
}

// brregEntityWire is the registry's own field names for GET
// .../enheter/{orgnr}, decoded once and then translated into
// brregEntityRecord by brregEntityRecordFrom. respons_klasse is present
// only on a deleted entity's reduced body ("SlettetEnhet") — its absence
// (the zero value "") is exactly what marks an ordinary live entity.
type brregEntityWire struct {
	ResponsKlasse                             string            `json:"respons_klasse"`
	OrganisationNumber                        string            `json:"organisasjonsnummer"`
	Name                                      string            `json:"navn"`
	OrganisationForm                          *brregCodeWire    `json:"organisasjonsform"`
	IndustryCode1                             *brregCodeWire    `json:"naeringskode1"`
	HarRegistrertAntallAnsatte                bool              `json:"harRegistrertAntallAnsatte"`
	AntallAnsatte                             *int32            `json:"antallAnsatte"`
	RegistrertIMvaregisteret                  bool              `json:"registrertIMvaregisteret"`
	Konkurs                                   bool              `json:"konkurs"`
	Konkursdato                               string            `json:"konkursdato"`
	UnderAvvikling                            bool              `json:"underAvvikling"`
	UnderAvviklingDato                        string            `json:"underAvviklingDato"`
	UnderTvangsavviklingEllerTvangsopplosning bool              `json:"underTvangsavviklingEllerTvangsopplosning"`
	Slettedato                                string            `json:"slettedato"`
	Stiftelsesdato                            string            `json:"stiftelsesdato"`
	Hjemmeside                                string            `json:"hjemmeside"`
	Epostadresse                              string            `json:"epostadresse"`
	Telefon                                   string            `json:"telefon"`
	Mobil                                     string            `json:"mobil"`
	OverordnetEnhet                           string            `json:"overordnetEnhet"`
	Forretningsadresse                        *brregAddressWire `json:"forretningsadresse"`
	Postadresse                               *brregAddressWire `json:"postadresse"`
}

// brregAddressFrom translates one wire address into a *brregAddress, or nil
// when w itself is nil (an absent forretningsadresse/postadresse must stay
// absent, never become an address with every field blank). Lines are
// trimmed and blank lines dropped; landkode is upper-cased when it is
// exactly two letters (ISO 3166-1 alpha-2) and treated as absent otherwise
// — the registry never sends anything else, but a malformed body here is
// still just a malformed body, not a crash.
func brregAddressFrom(w *brregAddressWire) *brregAddress {
	if w == nil {
		return nil
	}
	lines := make([]string, 0, len(w.Adresse))
	for _, line := range w.Adresse {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	countryCode := strings.ToUpper(strings.TrimSpace(w.Landkode))
	if len(countryCode) != 2 {
		countryCode = ""
	}
	return &brregAddress{
		Lines:        lines,
		PostalCode:   strings.TrimSpace(w.Postnummer),
		City:         strings.TrimSpace(w.Poststed),
		Municipality: strings.TrimSpace(w.Kommune),
		CountryCode:  countryCode,
	}
}

// parseBrregDate parses one of the registry's dates (stiftelsesdato,
// slettedato): "" is absent (nil, no error) — the registry never sends a
// malformed date, so anything present that fails to parse as 2006-01-02 is
// treated the same as any other malformed body: an error, not a silently
// dropped date.
func parseBrregDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("brreg: malformed date %q: %w", s, err)
	}
	return &parsed, nil
}

// brregEntityRecordFrom translates a decoded, live (non-"SlettetEnhet")
// wire entity into the record this module keeps: codes and free text
// trimmed, Employees left nil unless the registry has registered a
// headcount at all, addresses translated through brregAddressFrom.
func brregEntityRecordFrom(wire brregEntityWire) (brregEntityRecord, error) {
	deletedOn, err := parseBrregDate(wire.Slettedato)
	if err != nil {
		return brregEntityRecord{}, err
	}
	foundedOn, err := parseBrregDate(wire.Stiftelsesdato)
	if err != nil {
		return brregEntityRecord{}, err
	}
	// Both status dates are as optional as the flags beside them are
	// unconditional: the registry sends konkurs/underAvvikling on every
	// entity and their dates only when it has them.
	bankruptOn, err := parseBrregDate(wire.Konkursdato)
	if err != nil {
		return brregEntityRecord{}, err
	}
	liquidationOn, err := parseBrregDate(wire.UnderAvviklingDato)
	if err != nil {
		return brregEntityRecord{}, err
	}

	var employees *int32
	if wire.HarRegistrertAntallAnsatte && wire.AntallAnsatte != nil {
		count := *wire.AntallAnsatte
		employees = &count
	}

	var formCode, form string
	if wire.OrganisationForm != nil {
		formCode = strings.TrimSpace(wire.OrganisationForm.Kode)
		form = strings.TrimSpace(wire.OrganisationForm.Beskrivelse)
	}
	var industryCode, industry string
	if wire.IndustryCode1 != nil {
		industryCode = strings.TrimSpace(wire.IndustryCode1.Kode)
		industry = strings.TrimSpace(wire.IndustryCode1.Beskrivelse)
	}

	return brregEntityRecord{
		OrganisationNumber:       strings.TrimSpace(wire.OrganisationNumber),
		Name:                     strings.TrimSpace(wire.Name),
		OrganisationFormCode:     formCode,
		OrganisationForm:         form,
		IndustryCode:             industryCode,
		Industry:                 industry,
		Employees:                employees,
		VATRegistered:            wire.RegistrertIMvaregisteret,
		Bankrupt:                 wire.Konkurs,
		UnderLiquidation:         wire.UnderAvvikling,
		UnderForcedLiquidation:   wire.UnderTvangsavviklingEllerTvangsopplosning,
		DeletedOn:                deletedOn,
		FoundedOn:                foundedOn,
		BankruptOn:               bankruptOn,
		LiquidationOn:            liquidationOn,
		Website:                  strings.TrimSpace(wire.Hjemmeside),
		Email:                    strings.TrimSpace(wire.Epostadresse),
		Phone:                    strings.TrimSpace(wire.Telefon),
		Mobile:                   strings.TrimSpace(wire.Mobil),
		ParentOrganisationNumber: strings.TrimSpace(wire.OverordnetEnhet),
		BusinessAddress:          brregAddressFrom(wire.Forretningsadresse),
		PostalAddress:            brregAddressFrom(wire.Postadresse),
	}, nil
}

// parseFoundOrDeletedEntity decodes a 200 response body as either a live
// entity or a "SlettetEnhet" (this file's doc comment), the only two shapes
// a 2xx from this operation can carry.
func parseFoundOrDeletedEntity(body []byte) (brregEntityRecord, brregEntityOutcome, error) {
	var wire brregEntityWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("brreg: entity body could not be decoded: %w", err)
	}
	if wire.ResponsKlasse == "SlettetEnhet" {
		deletedOn, err := parseBrregDate(wire.Slettedato)
		if err != nil {
			return brregEntityRecord{}, brregEntityUnknown, err
		}
		return brregEntityRecord{
			OrganisationNumber: strings.TrimSpace(wire.OrganisationNumber),
			Name:               strings.TrimSpace(wire.Name),
			DeletedOn:          deletedOn,
		}, brregEntityDeleted, nil
	}
	rec, err := brregEntityRecordFrom(wire)
	if err != nil {
		return brregEntityRecord{}, brregEntityUnknown, err
	}
	return rec, brregEntityFound, nil
}

// parseRemovedEntity decodes a 410 body ({"organisasjonsnummer":"…",
// "slettedato":"2026-09-21","_links":{…}}): DeletedOn from slettedato when
// present. An empty body is tolerated (removed, no date known) rather than
// treated as malformed, since the design doc does not promise 410 always
// carries one, unlike the 404 case it does pin as empty.
func parseRemovedEntity(body []byte) (brregEntityRecord, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return brregEntityRecord{}, nil
	}
	var wire struct {
		Slettedato string `json:"slettedato"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return brregEntityRecord{}, fmt.Errorf("brreg: 410 body could not be decoded: %w", err)
	}
	deletedOn, err := parseBrregDate(wire.Slettedato)
	if err != nil {
		return brregEntityRecord{}, err
	}
	return brregEntityRecord{DeletedOn: deletedOn}, nil
}

// errBrregBody marks the two body failures that are terminal rather than
// retryable (fix round 2, minors), for every one of this module's Brreg reads:
// a response past its cap, and a body that could not be read to the end. A
// registry answering a megabyte of nonsense will answer the same megabyte on
// the next three attempts too, so retrying spends the budget a genuine outage
// needs on an answer that cannot improve — the same reasoning a 404 and a 410
// are never retried on.
var errBrregBody = errors.New("brreg: response body could not be used")

// validateBrregContentType requires contentType to parse
// (mime.ParseMediaType) to one of accepted, naming what it actually got when
// it does not — a 406's problem body, an HTML error page from a misbehaving
// proxy — rather than handing it to json.Unmarshal to fail on its own, less
// informative terms.
//
// accepted is an explicit list, deliberately not a "+json suffix" rule: this
// module's own tests pin application/problem+json as a REFUSAL for the entity
// read (TestEntity_WrongMediaTypeIsAnError), because a problem document is
// exactly the shape a failure arrives in and must never be decoded as a
// record. what names the read in the message (brregEntityRead, brregFeedRead).
func validateBrregContentType(what, contentType string, accepted ...string) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil {
		for _, want := range accepted {
			if mediaType == want {
				return nil
			}
		}
	}
	return fmt.Errorf("brreg %s response had content type %q, want %s", what, contentType, strings.Join(accepted, " or "))
}

// readBrregBody reads resp's body up to max+1 bytes — one byte past the cap, so
// a body exactly at the limit is still accepted and one over it is refused
// rather than silently truncated into something that happens to parse (the same
// idiom internal/peppol/smp.go uses for its own response cap). Both failures
// wrap errBrregBody, which is what marks them terminal rather than retryable.
func readBrregBody(what string, resp *http.Response, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBrregBody, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: brreg %s response exceeded %d bytes", errBrregBody, what, max)
	}
	return data, nil
}

// entity fetches the Enhetsregisteret record for orgnr, bounded overall by
// c.timeout and per attempt by brregAttemptTimeout, retrying a transport
// error or a retryable status (isRetryableStatus) with c.backoff's delay
// between attempts — the same policy c.lookup uses for the search. A 404 or
// 410 is never retried: both are definite answers about this organisation
// number, not something one more attempt could improve on, and neither is an
// unusable body (errBrregBody). Once every attempt is exhausted, or a
// non-retryable status/content-type/decode failure ends the loop early, the
// returned error wraps errBrregUnavailable, exactly as lookup's does; the
// outcome value is meaningless whenever error is non-nil.
func (c *brregClient) entity(ctx context.Context, orgnr string) (brregEntityRecord, brregEntityOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	path := brregEntityPath(orgnr)

	var lastErr error
	for attempt := 0; attempt <= brregRetryAttempts; attempt++ {
		if attempt > 0 {
			if !waitBackoff(ctx, c.backoff(attempt)) {
				break
			}
		}
		status, contentType, body, err := c.entityAttempt(ctx, path)
		if err != nil {
			if errors.Is(err, errBrregBody) {
				return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: %w", errBrregUnavailable, err)
			}
			lastErr = err
			continue
		}

		if status == http.StatusNotFound {
			return brregEntityRecord{}, brregEntityUnknown, nil
		}
		if status == http.StatusGone {
			rec, perr := parseRemovedEntity(body)
			if perr != nil {
				return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: %w", errBrregUnavailable, perr)
			}
			return rec, brregEntityRemoved, nil
		}
		if isRetryableStatus(status) {
			lastErr = fmt.Errorf("brreg responded %d", status)
			continue
		}

		// Every remaining status — a successful 2xx and a terminal failure
		// alike (a 406, say) — must carry a body this module can trust before
		// it is decoded, so the content type is checked here, once, ahead of
		// both the success and failure branches below.
		if err := validateBrregContentType(brregEntityRead, contentType, brregEntityMediaType, "application/json"); err != nil {
			return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: %w", errBrregUnavailable, err)
		}
		if !isSuccessStatus(status) {
			return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: brreg responded %d", errBrregUnavailable, status)
		}
		rec, outcome, perr := parseFoundOrDeletedEntity(body)
		if perr != nil {
			return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: %w", errBrregUnavailable, perr)
		}
		// The one thing a response must agree with the request about (fix
		// round 2, minors): a body about some other organisation — a misrouted
		// proxy, a cache serving the wrong key — must never be stored as this
		// customer's record, so it is terminal rather than retried, the same as
		// a body that will not decode. (The 410 above carries no record to
		// store, so nothing there needs checking.)
		if rec.OrganisationNumber != orgnr {
			return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: brreg answered for organisation number %q, asked for %q",
				errBrregUnavailable, rec.OrganisationNumber, orgnr)
		}
		return rec, outcome, nil
	}
	return brregEntityRecord{}, brregEntityUnknown, fmt.Errorf("%w: %w", errBrregUnavailable, lastErr)
}

// entityAttempt performs one GET for path, bounded by brregAttemptTimeout,
// asking for brregEntityMediaType; the capped body read is readBrregBody's
// own rule (this file, above).
func (c *brregClient) entityAttempt(ctx context.Context, path string) (status int, contentType string, body []byte, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, brregAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Accept", brregEntityMediaType)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := readBrregBody(brregEntityRead, resp, brregEntityMaxBodyBytes)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}
