package brreg

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

// This file is the client's second Enhetsregisteret operation:
// where client.go's Search looks for candidates by name or organisation
// number, (*Client).Entity fetches one entity's full record — the
// registry's complete view of a single organisation, keyed by the
// organisation number a caller already has (from a pick, or from the
// customer's own legal identity, a supplier's).
//
// Entity has four defined outcomes, not two, because the registry itself
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
	// entityMediaType is the pinned Enhetsregisteret v2 representation
	// (the design's "What the registry looks like": v1 is gone, a request for
	// it now answers 406). Sent as the Accept header on every attempt and
	// checked, alongside plain application/json, against the response's own
	// Content-Type before any byte of the body is trusted as this shape.
	entityMediaType = "application/vnd.brreg.enhetsregisteret.enhet.v2+json"

	// entityMaxBodyBytes caps a single entity response at 1 MiB. The
	// registry's own records are a few kilobytes at most; a response far
	// past that is not a bigger company, it is something to refuse rather
	// than buffer in full.
	entityMaxBodyBytes = 1 << 20

	// entityRead is what this read is called in an error message
	// (validateContentType, readBody), the counterpart of
	// feedRead's own constant (feed.go): the two
	// reads' failures are told apart in a log by this word, so it is named in
	// both places rather than spelled as a literal in one of them.
	entityRead = "entity"
)

// Address is one of the registry's two address shapes on an entity
// (forretningsadresse, postadresse), kept close to the wire: Lines is the
// registry's own free-form "adresse" array (a street address is one or more
// lines, trimmed, with blank lines dropped), and PostalCode/Municipality
// are empty on a foreign address — the registry only assigns a Norwegian
// post code and municipality, so a foreign address carries its own postal
// district as part of City instead (e.g. "81-336 GDYNIA").
type Address struct {
	Lines        []string
	PostalCode   string
	City         string
	Municipality string
	CountryCode  string
}

// Entity is the subset of one Enhetsregisteret entity this
// caller keeps (Customers' customer_registry_records): the fields a
// customer or supplier record can actually use, not every field the registry returns
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
type Entity struct {
	OrganisationNumber, Name, OrganisationFormCode, OrganisationForm, IndustryCode, Industry string
	Employees                                                                                *int32
	VATRegistered, Bankrupt, UnderLiquidation, UnderForcedLiquidation                        bool
	DeletedOn, FoundedOn, BankruptOn, LiquidationOn                                          *time.Time
	Website, Email, Phone, Mobile, ParentOrganisationNumber                                  string
	BusinessAddress, PostalAddress                                                           *Address
}

// Outcome is entity's answer about which of the registry's three
// shapes the response was, once a request has actually reached and
// understood the registry (an outcome is only meaningful alongside a nil
// error; entity never returns a mix of a real outcome and a non-nil error).
type Outcome int

const (
	// Found is an ordinary live entity: the full record is
	// populated from the response.
	Found Outcome = iota
	// Deleted is a 200 response whose body is "SlettetEnhet" — see
	// this file's doc comment for why that is a body to inspect, not a
	// status to branch on. Only Name, OrganisationNumber and DeletedOn are
	// populated on the returned record; everything else the registry might
	// still echo (such as the organisation form) is deliberately left zero,
	// because a struck-off company's form is not a fact worth keeping.
	Deleted
	// Removed is a 410: gone from open data entirely. DeletedOn is
	// populated from the body's slettedato when the registry sends one.
	Removed
	// Unknown is a plain 404: no entity ever existed at this
	// organisation number, or it is a sub-entity's number (the design doc:
	// "A sub-entity's number on /enheter: 404"). It doubles as the sentinel
	// returned alongside a non-nil error (not the type's zero value — that is
	// Found) — callers must check the error first, since the
	// outcome carries no meaning there.
	Unknown
)

// entityPath is the request path for one entity
// (GET {base}/enhetsregisteret/api/enheter/{orgnr}), sharing searchPath with
// the search in client.go — the same collection, one more segment.
func entityPath(orgnr string) string {
	return searchPath + "/" + url.PathEscape(orgnr)
}

// addressWire is forretningsadresse/postadresse exactly as the
// registry sends it: adresse is an array of lines (never a single string),
// and postnummer/kommune are simply absent on a foreign address rather than
// sent empty.
type addressWire struct {
	Adresse    []string `json:"adresse"`
	Postnummer string   `json:"postnummer"`
	Poststed   string   `json:"poststed"`
	Kommune    string   `json:"kommune"`
	Landkode   string   `json:"landkode"`
}

// codeWire is the {kode, beskrivelse} shape the registry uses for both
// organisasjonsform and naeringskode1 — one wire type for both since the
// shape, and this file's handling of it, is identical.
type codeWire struct {
	Kode        string `json:"kode"`
	Beskrivelse string `json:"beskrivelse"`
}

// entityWire is the registry's own field names for GET
// .../enheter/{orgnr}, decoded once and then translated into
// Entity by entityFrom. respons_klasse is present
// only on a deleted entity's reduced body ("SlettetEnhet") — its absence
// (the zero value "") is exactly what marks an ordinary live entity.
type entityWire struct {
	ResponsKlasse                             string       `json:"respons_klasse"`
	OrganisationNumber                        string       `json:"organisasjonsnummer"`
	Name                                      string       `json:"navn"`
	OrganisationForm                          *codeWire    `json:"organisasjonsform"`
	IndustryCode1                             *codeWire    `json:"naeringskode1"`
	HarRegistrertAntallAnsatte                bool         `json:"harRegistrertAntallAnsatte"`
	AntallAnsatte                             *int32       `json:"antallAnsatte"`
	RegistrertIMvaregisteret                  bool         `json:"registrertIMvaregisteret"`
	Konkurs                                   bool         `json:"konkurs"`
	Konkursdato                               string       `json:"konkursdato"`
	UnderAvvikling                            bool         `json:"underAvvikling"`
	UnderAvviklingDato                        string       `json:"underAvviklingDato"`
	UnderTvangsavviklingEllerTvangsopplosning bool         `json:"underTvangsavviklingEllerTvangsopplosning"`
	Slettedato                                string       `json:"slettedato"`
	Stiftelsesdato                            string       `json:"stiftelsesdato"`
	Hjemmeside                                string       `json:"hjemmeside"`
	Epostadresse                              string       `json:"epostadresse"`
	Telefon                                   string       `json:"telefon"`
	Mobil                                     string       `json:"mobil"`
	OverordnetEnhet                           string       `json:"overordnetEnhet"`
	Forretningsadresse                        *addressWire `json:"forretningsadresse"`
	Postadresse                               *addressWire `json:"postadresse"`
}

// addressFrom translates one wire address into a *Address, or nil
// when w itself is nil (an absent forretningsadresse/postadresse must stay
// absent, never become an address with every field blank). Lines are
// trimmed and blank lines dropped; landkode is upper-cased when it is
// exactly two letters (ISO 3166-1 alpha-2) and treated as absent otherwise
// — the registry never sends anything else, but a malformed body here is
// still just a malformed body, not a crash.
func addressFrom(w *addressWire) *Address {
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
	return &Address{
		Lines:        lines,
		PostalCode:   strings.TrimSpace(w.Postnummer),
		City:         strings.TrimSpace(w.Poststed),
		Municipality: strings.TrimSpace(w.Kommune),
		CountryCode:  countryCode,
	}
}

// parseDate parses one of the registry's dates (stiftelsesdato,
// slettedato): "" is absent (nil, no error) — the registry never sends a
// malformed date, so anything present that fails to parse as 2006-01-02 is
// treated the same as any other malformed body: an error, not a silently
// dropped date.
func parseDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("brreg: malformed date %q: %w", s, err)
	}
	return &parsed, nil
}

// entityFrom translates a decoded, live (non-"SlettetEnhet")
// wire entity into the record a caller keeps: codes and free text
// trimmed, Employees left nil unless the registry has registered a
// headcount at all, addresses translated through addressFrom.
func entityFrom(wire entityWire) (Entity, error) {
	deletedOn, err := parseDate(wire.Slettedato)
	if err != nil {
		return Entity{}, err
	}
	foundedOn, err := parseDate(wire.Stiftelsesdato)
	if err != nil {
		return Entity{}, err
	}
	// Both status dates are as optional as the flags beside them are
	// unconditional: the registry sends konkurs/underAvvikling on every
	// entity and their dates only when it has them.
	bankruptOn, err := parseDate(wire.Konkursdato)
	if err != nil {
		return Entity{}, err
	}
	liquidationOn, err := parseDate(wire.UnderAvviklingDato)
	if err != nil {
		return Entity{}, err
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

	return Entity{
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
		BusinessAddress:          addressFrom(wire.Forretningsadresse),
		PostalAddress:            addressFrom(wire.Postadresse),
	}, nil
}

// parseFoundOrDeletedEntity decodes a 200 response body as either a live
// entity or a "SlettetEnhet" (this file's doc comment), the only two shapes
// a 2xx from this operation can carry.
func parseFoundOrDeletedEntity(body []byte) (Entity, Outcome, error) {
	var wire entityWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return Entity{}, Unknown, fmt.Errorf("brreg: entity body could not be decoded: %w", err)
	}
	if wire.ResponsKlasse == "SlettetEnhet" {
		deletedOn, err := parseDate(wire.Slettedato)
		if err != nil {
			return Entity{}, Unknown, err
		}
		return Entity{
			OrganisationNumber: strings.TrimSpace(wire.OrganisationNumber),
			Name:               strings.TrimSpace(wire.Name),
			DeletedOn:          deletedOn,
		}, Deleted, nil
	}
	rec, err := entityFrom(wire)
	if err != nil {
		return Entity{}, Unknown, err
	}
	return rec, Found, nil
}

// parseRemovedEntity decodes a 410 body ({"organisasjonsnummer":"…",
// "slettedato":"2026-09-21","_links":{…}}): DeletedOn from slettedato when
// present. An empty body is tolerated (removed, no date known) rather than
// treated as malformed, since the design doc does not promise 410 always
// carries one, unlike the 404 case it does pin as empty.
func parseRemovedEntity(body []byte) (Entity, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return Entity{}, nil
	}
	var wire struct {
		Slettedato string `json:"slettedato"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return Entity{}, fmt.Errorf("brreg: 410 body could not be decoded: %w", err)
	}
	deletedOn, err := parseDate(wire.Slettedato)
	if err != nil {
		return Entity{}, err
	}
	return Entity{DeletedOn: deletedOn}, nil
}

// errBody marks the two body failures that are terminal rather than
// retryable (fix round 2, minors), for every one of this client's reads:
// a response past its cap, and a body that could not be read to the end. A
// registry answering a megabyte of nonsense will answer the same megabyte on
// the next three attempts too, so retrying spends the budget a genuine outage
// needs on an answer that cannot improve — the same reasoning a 404 and a 410
// are never retried on.
var errBody = errors.New("brreg: response body could not be used")

// ErrUnreachable marks entity's one failure that is about the registry
// rather than the organisation asked about: every attempt spent on a
// transport error, a timeout or a retryable status. Customers' import reads
// it to stop asking for the rest of a run; a refused body, a
// content type or an answer about another organisation number is that one
// answer's failure and does not carry it.
var ErrUnreachable = errors.New("brreg: registry could not be reached")

// validateContentType requires contentType to parse
// (mime.ParseMediaType) to one of accepted, naming what it actually got when
// it does not — a 406's problem body, an HTML error page from a misbehaving
// proxy — rather than handing it to json.Unmarshal to fail on its own, less
// informative terms.
//
// accepted is an explicit list, deliberately not a "+json suffix" rule: this
// package's own tests pin application/problem+json as a REFUSAL for the entity
// read (TestEntity_WrongMediaTypeIsAnError), because a problem document is
// exactly the shape a failure arrives in and must never be decoded as a
// record. what names the read in the message (entityRead, feedRead).
func validateContentType(what, contentType string, accepted ...string) error {
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

// readBody reads resp's body up to max+1 bytes — one byte past the cap, so
// a body exactly at the limit is still accepted and one over it is refused
// rather than silently truncated into something that happens to parse (the same
// idiom internal/peppol/smp.go uses for its own response cap). Both failures
// wrap errBody, which is what marks them terminal rather than retryable.
func readBody(what string, resp *http.Response, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBody, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: brreg %s response exceeded %d bytes", errBody, what, max)
	}
	return data, nil
}

// Entity fetches the Enhetsregisteret record for orgnr, bounded overall by
// c.timeout and per attempt by AttemptTimeout, retrying a transport
// error or a retryable status (isRetryableStatus) with c.backoff's delay
// between attempts — the same policy c.lookup uses for the search. A 404 or
// 410 is never retried: both are definite answers about this organisation
// number, not something one more attempt could improve on, and neither is an
// unusable body (errBody). Once every attempt is exhausted, or a
// non-retryable status/content-type/decode failure ends the loop early, the
// returned error wraps ErrUnavailable, exactly as lookup's does; the
// outcome value is meaningless whenever error is non-nil.
func (c *Client) Entity(ctx context.Context, orgnr string) (Entity, Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	path := entityPath(orgnr)

	var lastErr error
	for attempt := 0; attempt <= retryAttempts; attempt++ {
		if attempt > 0 {
			if !waitBackoff(ctx, c.backoff(attempt)) {
				break
			}
		}
		status, contentType, body, err := c.entityAttempt(ctx, path)
		if err != nil {
			if errors.Is(err, errBody) {
				return Entity{}, Unknown, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			lastErr = err
			continue
		}

		if status == http.StatusNotFound {
			return Entity{}, Unknown, nil
		}
		if status == http.StatusGone {
			rec, perr := parseRemovedEntity(body)
			if perr != nil {
				return Entity{}, Unknown, fmt.Errorf("%w: %w", ErrUnavailable, perr)
			}
			return rec, Removed, nil
		}
		if isRetryableStatus(status) {
			lastErr = fmt.Errorf("brreg responded %d", status)
			continue
		}

		// Every remaining status — a successful 2xx and a terminal failure
		// alike (a 406, say) — must carry a body this client can trust before
		// it is decoded, so the content type is checked here, once, ahead of
		// both the success and failure branches below.
		if err := validateContentType(entityRead, contentType, entityMediaType, "application/json"); err != nil {
			return Entity{}, Unknown, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		if !isSuccessStatus(status) {
			return Entity{}, Unknown, fmt.Errorf("%w: brreg responded %d", ErrUnavailable, status)
		}
		rec, outcome, perr := parseFoundOrDeletedEntity(body)
		if perr != nil {
			return Entity{}, Unknown, fmt.Errorf("%w: %w", ErrUnavailable, perr)
		}
		// The one thing a response must agree with the request about (fix
		// round 2, minors): a body about some other organisation — a misrouted
		// proxy, a cache serving the wrong key — must never be stored as this
		// organisation's record, so it is terminal rather than retried, the same as
		// a body that will not decode. (The 410 above carries no record to
		// store, so nothing there needs checking.)
		if rec.OrganisationNumber != orgnr {
			return Entity{}, Unknown, fmt.Errorf("%w: brreg answered for organisation number %q, asked for %q",
				ErrUnavailable, rec.OrganisationNumber, orgnr)
		}
		return rec, outcome, nil
	}
	return Entity{}, Unknown, fmt.Errorf("%w: %w: %w", ErrUnavailable, ErrUnreachable, lastErr)
}

// entityAttempt performs one GET for path, bounded by AttemptTimeout,
// asking for entityMediaType; the capped body read is readBody's
// own rule (this file, above).
func (c *Client) entityAttempt(ctx context.Context, path string) (status int, contentType string, body []byte, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, AttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Accept", entityMediaType)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := readBody(entityRead, resp, entityMaxBodyBytes)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}
