package customers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the customer's registry record end to end (Brreg in full
// design D1, D2, D4): GET /customers/{id}/registry-record, POST
// /customers/{id}/registry-refresh, and the fetch a Brreg pick triggers on
// create and on a legal-identity PUT. No .NET ancestor — the registry record
// is new to this port. Every test drives modtest.WithTransport with a fake
// round tripper standing in for Enhetsregisteret, exactly as brreg_test.go's
// own fakeBrregTransport does for the search (this file reuses that type);
// no test here opens a socket.

// registryEntityResponse is a canned entity response under the pinned v2
// media type the client asks for (brreg_entity.go's brregEntityMediaType) —
// brreg_test.go's jsonResponse answers plain application/json, which the
// client also accepts, but the entity endpoint's own responses are the v2
// type and these fixtures should look like what production sees.
func registryEntityResponse(status int, body string) *http.Response {
	resp := jsonResponse(status, body)
	resp.Header.Set("Content-Type", "application/vnd.brreg.enhetsregisteret.enhet.v2+json")
	return resp
}

// equinorRegistryBody is the full record the fixtures fetch for 923609016:
// the always-present fields plus a form, an industry, a headcount, VAT, a
// founding date, a website and both addresses.
const equinorRegistryBody = `{
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA",
	"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
	"naeringskode1": {"kode": "06.100", "beskrivelse": "Utvinning av råolje"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 21272,
	"registrertIMvaregisteret": true,
	"stiftelsesdato": "1972-06-14",
	"hjemmeside": "www.equinor.com",
	"forretningsadresse": {"landkode": "NO", "postnummer": "4035", "poststed": "STAVANGER", "adresse": ["Forusbeen 50"], "kommune": "STAVANGER"},
	"postadresse": {"landkode": "NO", "postnummer": "4035", "poststed": "STAVANGER", "adresse": ["Postboks 8500"], "kommune": "STAVANGER"},
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// movedEquinorRegistryBody is the same entity a year later: two fields
// differ — the headcount and the business address — and nothing else does.
const movedEquinorRegistryBody = `{
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA",
	"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
	"naeringskode1": {"kode": "06.100", "beskrivelse": "Utvinning av råolje"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 21000,
	"registrertIMvaregisteret": true,
	"stiftelsesdato": "1972-06-14",
	"hjemmeside": "www.equinor.com",
	"forretningsadresse": {"landkode": "NO", "postnummer": "0155", "poststed": "OSLO", "adresse": ["Storgata 1"], "kommune": "OSLO"},
	"postadresse": {"landkode": "NO", "postnummer": "4035", "poststed": "STAVANGER", "adresse": ["Postboks 8500"], "kommune": "STAVANGER"},
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// deletedRegistryBody is the registry's reduced body for a struck-off
// entity: HTTP 200 with respons_klasse "SlettetEnhet" (brreg_entity.go's own
// doc comment on why that is a body to read, not a status to branch on).
const deletedRegistryBody = `{
	"respons_klasse": "SlettetEnhet",
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA",
	"slettedato": "2026-09-21"
}`

// removedRegistryBody is the 410 body: gone from open data entirely.
const removedRegistryBody = `{"organisasjonsnummer":"923609016","slettedato":"2026-09-21"}`

// registryTransport answers one canned response per request and remembers
// the paths it was asked for, so a test can assert both "the record came
// from this body" and "no request was made at all".
type registryTransport struct {
	mu      sync.Mutex
	paths   []string
	respond func(path string) (*http.Response, error)
}

func (f *registryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	respond := f.respond
	f.mu.Unlock()
	return respond(r.URL.Path)
}

func (f *registryTransport) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// registryBody is a transport answering every request with one 200 body.
func registryBody(body string) *registryTransport {
	return &registryTransport{respond: func(string) (*http.Response, error) {
		return registryEntityResponse(http.StatusOK, body), nil
	}}
}

// registryStatus is a transport answering every request with one status and
// body — a 404 (unknown), a 410 (removed) or a 500 (a real failure).
func registryStatus(status int, body string) *registryTransport {
	return &registryTransport{respond: func(string) (*http.Response, error) {
		return registryEntityResponse(status, body), nil
	}}
}

// registrySequence answers each request with the next body in bodies,
// repeating the last one once they run out: the shape a "fetch, then fetch
// again after something changed" test needs.
func registrySequence(bodies ...string) *registryTransport {
	var n int
	f := &registryTransport{}
	f.respond = func(string) (*http.Response, error) {
		i := n
		n++
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		return registryEntityResponse(http.StatusOK, bodies[i]), nil
	}
	return f
}

func newRegistryHarness(t *testing.T, transport http.RoundTripper) *modtest.Harness {
	t.Helper()
	return newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
}

type registryAddressJSON struct {
	Lines        []string `json:"lines"`
	PostalCode   *string  `json:"postalCode"`
	City         *string  `json:"city"`
	Municipality *string  `json:"municipality"`
	CountryCode  string   `json:"countryCode"`
}

type registryRecordJSON struct {
	OrganisationNumber       string               `json:"organisationNumber"`
	Name                     string               `json:"name"`
	OrganisationFormCode     *string              `json:"organisationFormCode"`
	OrganisationForm         *string              `json:"organisationForm"`
	IndustryCode             *string              `json:"industryCode"`
	Industry                 *string              `json:"industry"`
	Employees                *int32               `json:"employees"`
	VatRegistered            bool                 `json:"vatRegistered"`
	Bankrupt                 bool                 `json:"bankrupt"`
	UnderLiquidation         bool                 `json:"underLiquidation"`
	UnderForcedLiquidation   bool                 `json:"underForcedLiquidation"`
	DeletedOn                *string              `json:"deletedOn"`
	FoundedOn                *string              `json:"foundedOn"`
	Website                  *string              `json:"website"`
	Email                    *string              `json:"email"`
	Phone                    *string              `json:"phone"`
	Mobile                   *string              `json:"mobile"`
	ParentOrganisationNumber *string              `json:"parentOrganisationNumber"`
	BusinessAddress          *registryAddressJSON `json:"businessAddress"`
	PostalAddress            *registryAddressJSON `json:"postalAddress"`
	FetchedAt                time.Time            `json:"fetchedAt"`
}

type registryChangeJSON struct {
	Field string  `json:"field"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

type registryRefreshJSON struct {
	Status  string               `json:"status"`
	Record  *registryRecordJSON  `json:"record"`
	Changes []registryChangeJSON `json:"changes"`
}

// createBrregPick creates a customer whose legal identity is a Brreg pick —
// source "brreg", the one source the create hook fetches for.
func createBrregPick(t *testing.T, c *modtest.Client, name, orgNumber string) createdCustomerJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": name,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": orgNumber, "name": name, "source": "brreg",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create customer %q: status %d body %s, want 201", name, r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	return created
}

func getRegistryRecord(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/registry-record", id), nil)
}

func postRegistryRefresh(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/registry-refresh", id), nil)
}

// fetchRegistryRecord reads the stored record through the endpoint, failing
// the test on anything but 200.
func fetchRegistryRecord(t *testing.T, c *modtest.Client, id int32) registryRecordJSON {
	t.Helper()
	r := getRegistryRecord(t, c, id)
	if r.Status != http.StatusOK {
		t.Fatalf("get registry record: status %d body %s, want 200", r.Status, r.Body)
	}
	var rec registryRecordJSON
	r.JSON(&rec)
	return rec
}

// registryRowCount is how many registry rows a customer has — 0 or 1, since
// customer_id is the primary key. Asked directly of the database, because
// "no row" and "a row the caller may not see" are the same 204 through the
// endpoint.
func registryRowCount(t *testing.T, h *modtest.Harness, id int32) int {
	t.Helper()
	return modtest.One[int](t, h, `SELECT count(*)::int FROM customers.customer_registry_records WHERE customer_id = $1`, id)
}

// fetchRegistryEvents lists registry.change timeline entries for customerID,
// oldest first — GET .../timeline's own default order reversed, as
// fetchPeppolLookupEvents does for its own event type.
func fetchRegistryEvents(t *testing.T, c *modtest.Client, customerID int32) []timelineEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=registry.change", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get timeline: status %d body %s, want 200", r.Status, r.Body)
	}
	var feed timelineListJSON
	r.JSON(&feed)
	entries := append([]timelineEntryJSON(nil), feed.Data...)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

// TestCreateCustomer_WithBrregPick_FetchesAndStoresTheRecord pins design
// D2's first hook: a create whose identity came from the registry fetches
// the full record after the transaction commits, and every field of the
// answer is stored.
func TestCreateCustomer_WithBrregPick_FetchesAndStoresTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)

	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	if want := []string{"/enhetsregisteret/api/enheter/923609016"}; len(transport.requests()) != 1 || transport.requests()[0] != want[0] {
		t.Fatalf("requests = %v, want %v", transport.requests(), want)
	}

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.OrganisationNumber != "923609016" || rec.Name != "EQUINOR ASA" {
		t.Errorf("record = %+v, want the Equinor entity", rec)
	}
	if str(rec.OrganisationFormCode) != "ASA" || str(rec.OrganisationForm) != "Allmennaksjeselskap" {
		t.Errorf("form = %q/%q, want ASA/Allmennaksjeselskap", str(rec.OrganisationFormCode), str(rec.OrganisationForm))
	}
	if str(rec.IndustryCode) != "06.100" || str(rec.Industry) != "Utvinning av råolje" {
		t.Errorf("industry = %q/%q, want 06.100/Utvinning av råolje", str(rec.IndustryCode), str(rec.Industry))
	}
	if rec.Employees == nil || *rec.Employees != 21272 {
		t.Errorf("Employees = %v, want 21272", rec.Employees)
	}
	if !rec.VatRegistered || rec.Bankrupt || rec.UnderLiquidation || rec.UnderForcedLiquidation {
		t.Errorf("flags = %+v, want VAT only", rec)
	}
	if str(rec.FoundedOn) != "1972-06-14" || rec.DeletedOn != nil {
		t.Errorf("foundedOn/deletedOn = %v/%v, want 1972-06-14/nil", rec.FoundedOn, rec.DeletedOn)
	}
	if str(rec.Website) != "www.equinor.com" || rec.Email != nil || rec.Phone != nil || rec.Mobile != nil {
		t.Errorf("contact fields = %+v, want the website alone", rec)
	}
	if rec.ParentOrganisationNumber != nil {
		t.Errorf("ParentOrganisationNumber = %v, want nil", rec.ParentOrganisationNumber)
	}
	if rec.BusinessAddress == nil || strings.Join(rec.BusinessAddress.Lines, "|") != "Forusbeen 50" ||
		str(rec.BusinessAddress.PostalCode) != "4035" || str(rec.BusinessAddress.City) != "STAVANGER" ||
		str(rec.BusinessAddress.Municipality) != "STAVANGER" || rec.BusinessAddress.CountryCode != "NO" {
		t.Errorf("BusinessAddress = %+v, want the Forusbeen address", rec.BusinessAddress)
	}
	if rec.PostalAddress == nil || strings.Join(rec.PostalAddress.Lines, "|") != "Postboks 8500" {
		t.Errorf("PostalAddress = %+v, want the Postboks address", rec.PostalAddress)
	}
	if rec.FetchedAt.Before(modtest.Start) {
		t.Errorf("FetchedAt = %v, want at or after harness Start", rec.FetchedAt)
	}

	// The record lives beside the customer, never on it: the create's own
	// revision is untouched by the fetch that followed it.
	var customer customerJSON
	c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil).JSON(&customer)
	if customer.Revision != 1 {
		t.Errorf("Revision = %d, want 1 (a registry fetch never touches the customer row)", customer.Revision)
	}
	// The registry's name is the legal name, so the first fetch is silent.
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0 (the registry name is the legal name)", len(entries))
	}
}

// TestCreateCustomer_WithBrregPick_SurvivesARegistryFailure pins the other
// half of that hook (design D2): the customer is created whatever the
// registry does, no record is stored, and the failure is logged rather than
// returned.
func TestCreateCustomer_WithBrregPick_SurvivesARegistryFailure(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryStatus(http.StatusInternalServerError, `{"title":"boom"}`))
	c := authenticatedClient(t, h)

	created := createBrregPick(t, c, "Registry Down Co", "923609016")

	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (nothing is stored for a failed fetch)", n)
	}
	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "customers: registry record fetch failed") {
		t.Errorf("logs = %s, want the swallowed failure to be logged", logs)
	}
	if !strings.Contains(logs, `"errorKind":"registry_unavailable"`) {
		t.Errorf("logs = %s, want an errorKind naming the registry as unreachable", logs)
	}
	if strings.Contains(logs, "enhetsregisteret/api/enheter") {
		t.Errorf("logs = %s, must not carry the request URL", logs)
	}
}

// TestCreateCustomer_WithBrregPick_IsNotDelayedByASlowRegistry pins I1: the
// create's own hook is bounded by one attempt's worth of patience
// (registryHookTimeout), not by the whole BRREG_TIMEOUT budget the refresh
// endpoint spends, so a registry that has stopped answering costs the create a
// moment rather than a perceptible pause before its 201 — and the Refresh
// button is the retry for the record that is not there.
//
// The deadline is shortened through the package's own seam rather than waited
// out, which is also why this test does not run in parallel: the deadline
// belongs to the package, not to one harness.
func TestCreateCustomer_WithBrregPick_IsNotDelayedByASlowRegistry(t *testing.T) {
	restore := customers.SetRegistryHookTimeout(50 * time.Millisecond)
	t.Cleanup(restore)

	// A transport that answers nothing at all until the context it was handed
	// gives up: the only thing that ends this request is the hook's deadline.
	h := newRegistryHarness(t, contextBlockingTransport{})
	c := authenticatedClient(t, h)

	start := time.Now()
	created := createBrregPick(t, c, "Slow Registry Co", "923609016")
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("create took %v, want well under the 4s one attempt would allow (the hook's own deadline is what bounds it)", elapsed)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (nothing was fetched)", n)
	}
	logs := h.Logs()
	if !strings.Contains(logs, "customers: registry record fetch failed") {
		t.Errorf("logs = %s, want the dropped failure logged", logs)
	}
	if !strings.Contains(logs, `"errorKind":"timeout"`) {
		t.Errorf("logs = %s, want an errorKind naming the timeout", logs)
	}
}

// contextBlockingTransport answers no request: every round trip waits for the
// context it was given to be done and then reports why. It is how a registry
// that has stopped responding is stood in for without any test sleeping.
type contextBlockingTransport struct{}

func (contextBlockingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// TestCreateCustomer_WithManualIdentity_FetchesNothing pins the source rule:
// a Norwegian organisation number typed in by hand is not a Brreg pick, so
// the create makes no request at all. (The refresh endpoint enriches it when
// a person asks — see the refresh tests below.)
func TestCreateCustomer_WithManualIdentity_FetchesNothing(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)

	created := createCustomerWithIdentity(t, c, "Manual Identity Co", "no", "923609016")

	if reqs := transport.requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none (a manual identity is not a pick)", reqs)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0", n)
	}
}

// TestPutLegalIdentity_WithBrregSource_FetchesTheRecord pins design D2's
// second hook: pointing a customer at a different entity reads the new one
// straight away, so the record on file is never the wrong company's.
func TestPutLegalIdentity_WithBrregSource_FetchesTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Identity Later Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "EQUINOR ASA", "source": "brreg",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("put legal identity: status %d body %s, want 200", r.Status, r.Body)
	}

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.Name != "EQUINOR ASA" {
		t.Errorf("record name = %q, want EQUINOR ASA", rec.Name)
	}
	if len(transport.requests()) != 1 {
		t.Errorf("requests = %v, want exactly one", transport.requests())
	}
}

// TestPutLegalIdentity_WithManualSource_FetchesNothing is the same source
// rule on the PUT.
func TestPutLegalIdentity_WithManualSource_FetchesNothing(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Manual Identity Later Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "EQUINOR ASA", "source": "manual",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("put legal identity: status %d body %s, want 200", r.Status, r.Body)
	}
	if reqs := transport.requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none", reqs)
	}
}

// TestGetRegistryRecord_WithoutLegalIdentityView_Returns204 pins design D2's
// withholding rule: the record repeats the legal identity's organisation
// number, so a caller without customers:legal-identity-view gets the same
// 204 a customer with no record at all answers — never a 403, and never a
// hint that a record exists.
func TestGetRegistryRecord_WithoutLegalIdentityView_Returns204(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	owner := authenticatedClient(t, h)
	created := createBrregPick(t, owner, "EQUINOR ASA", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows = %d, want 1 before the withholding check", n)
	}

	limited := h.SignIn(t, "customers:view")
	r := getRegistryRecord(t, limited, created.Id)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty", r.Body)
	}
}

// TestGetRegistryRecord_WithoutARecord_Returns204 and
// TestGetRegistryRecord_UnknownCustomer_Returns404 pin the other two
// answers, the same three GetCustomersByIdLegalIdentity gives.
func TestGetRegistryRecord_WithoutARecord_Returns204(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "No Record Co")

	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("status %d body %s, want 204", r.Status, r.Body)
	}
}

func TestGetRegistryRecord_UnknownCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)

	if r := getRegistryRecord(t, c, 999999); r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestRegistryRefresh_FirstFetch_IsSilentWhenTheNameMatches pins the
// first-fetch rule (design D4): with no record on file the only comparison
// is the registry's name against the legal identity's, and an equal pair
// records nothing.
func TestRegistryRefresh_FirstFetch_IsSilentWhenTheNameMatches(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "found" {
		t.Errorf("status = %q, want found", got.Status)
	}
	if got.Record == nil || got.Record.Name != "EQUINOR ASA" {
		t.Errorf("record = %+v, want the stored record", got.Record)
	}
	if len(got.Changes) != 0 {
		t.Errorf("changes = %+v, want none", got.Changes)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}
}

// TestRegistryRefresh_FirstFetch_ReportsANameThatDiffersFromTheLegalName is
// the other first-fetch outcome: the customer's identity says one thing, the
// registry another, and the difference is the one thing worth recording.
func TestRegistryRefresh_FirstFetch_ReportsANameThatDiffersFromTheLegalName(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c, userID := authenticatedClientWithID(t, h)
	created := createCustomerWithIdentity(t, c, "Equinor", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	want := []registryChangeJSON{{Field: "name", From: ptr("Equinor"), To: ptr("EQUINOR ASA")}}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}

	entries := fetchRegistryEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Provenance != "generated" || e.Producer != "customers.brreg" {
		t.Errorf("provenance/producer = %q/%q, want generated/customers.brreg", e.Provenance, e.Producer)
	}
	if str(e.Summary) != "Registry record updated: name" {
		t.Errorf("summary = %q, want %q", str(e.Summary), "Registry record updated: name")
	}
	if e.ActorKind != "user" || str(e.ActorDisplay) != userDisplayName(t, h, userID) {
		t.Errorf("actor = %s/%v, want the user who clicked Refresh", e.ActorKind, e.ActorDisplay)
	}
	assertRegistryPayload(t, e, want)
}

// TestRegistryRefresh_SecondFetch_ReportsEveryChangedFieldInOneEvent pins
// design D4's "one event per refresh, listing what moved": a second refresh
// whose headcount and business address both changed records exactly one
// entry naming both, moves fetchedAt, and still leaves the customer row's
// revision alone.
func TestRegistryRefresh_SecondFetch_ReportsEveryChangedFieldInOneEvent(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registrySequence(equinorRegistryBody, movedEquinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	first := fetchRegistryRecord(t, c, created.Id)

	h.Advance(time.Hour)
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	want := []registryChangeJSON{
		{Field: "employees", From: ptr("21272"), To: ptr("21000")},
		{Field: "businessAddress", From: ptr("Forusbeen 50, 4035 STAVANGER, NO"), To: ptr("Storgata 1, 0155 OSLO, NO")},
	}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}

	entries := fetchRegistryEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want 1 (one event naming both fields)", len(entries))
	}
	if str(entries[0].Summary) != "Registry record updated: employees, businessAddress" {
		t.Errorf("summary = %q, want both fields named", str(entries[0].Summary))
	}
	assertRegistryPayload(t, entries[0], want)

	second := fetchRegistryRecord(t, c, created.Id)
	if !second.FetchedAt.After(first.FetchedAt) {
		t.Errorf("FetchedAt = %v, want after the first fetch's %v", second.FetchedAt, first.FetchedAt)
	}
	if second.Employees == nil || *second.Employees != 21000 {
		t.Errorf("Employees = %v, want 21000", second.Employees)
	}
	var customer customerJSON
	c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil).JSON(&customer)
	if customer.Revision != 1 {
		t.Errorf("Revision = %d, want 1 (two refreshes never touch the customer row)", customer.Revision)
	}
}

// TestRegistryRefresh_UnchangedRecord_RecordsNoSecondEvent pins the quiet
// re-check: the same body twice moves fetchedAt and writes no event.
func TestRegistryRefresh_UnchangedRecord_RecordsNoSecondEvent(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Equinor", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	h.Advance(time.Hour)
	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}

	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 1 {
		t.Errorf("timeline events = %d, want 1 (only the first fetch's name difference)", len(entries))
	}
}

// TestRegistryRefresh_DeletedEntity_StoresTheDeletionAndKeepsTheRest pins
// the SlettetEnhet outcome (design D2): status deleted, deletedOn stored,
// one event — and the fields the reduced body does not carry are the ones
// the record already had, not blanks.
func TestRegistryRefresh_DeletedEntity_StoresTheDeletionAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registrySequence(equinorRegistryBody, deletedRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}

	// Past the 60-second throttle window (registryRefreshMinInterval): inside
	// it a second click answers the stored record without asking the registry,
	// which is the very next test's subject rather than this one's.
	h.Advance(time.Hour)
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "deleted" {
		t.Errorf("status = %q, want deleted", got.Status)
	}
	want := []registryChangeJSON{{Field: "deletedOn", To: ptr("2026-09-21")}}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}

	rec := fetchRegistryRecord(t, c, created.Id)
	if str(rec.DeletedOn) != "2026-09-21" {
		t.Errorf("DeletedOn = %v, want 2026-09-21", rec.DeletedOn)
	}
	if str(rec.OrganisationForm) != "Allmennaksjeselskap" || rec.Employees == nil || *rec.Employees != 21272 {
		t.Errorf("record = %+v, want the pre-deletion fields kept", rec)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 1 || str(entries[0].Summary) != "Registry record updated: deletedOn" {
		t.Fatalf("entries = %+v, want one \"Registry record updated: deletedOn\"", entries)
	}
}

// TestRegistryRefresh_RemovedFromOpenData_DeletesTheRecord pins the 410
// outcome (design D2): the stored record goes, the endpoint answers removed
// with no record at all, and the timeline keeps the one fact left.
func TestRegistryRefresh_RemovedFromOpenData_DeletesTheRecord(t *testing.T) {
	t.Parallel()
	var removed bool
	transport := &registryTransport{respond: func(string) (*http.Response, error) {
		if removed {
			return registryEntityResponse(http.StatusGone, removedRegistryBody), nil
		}
		return registryEntityResponse(http.StatusOK, equinorRegistryBody), nil
	}}
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows = %d, want 1 before the removal", n)
	}

	removed = true
	h.Advance(time.Hour) // past the throttle window, so the click really asks
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "removed" {
		t.Errorf("status = %q, want removed", got.Status)
	}
	if got.Record != nil {
		t.Errorf("record = %+v, want none (the record is deleted)", got.Record)
	}
	want := []registryChangeJSON{{Field: "removedFromOpenData", To: ptr("2026-09-21")}}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}

	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (removed from open data)", n)
	}
	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
	entries := fetchRegistryEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want 1", len(entries))
	}
	if str(entries[0].Summary) != "Registry record removed from open data" {
		t.Errorf("summary = %q, want the removal's own sentence", str(entries[0].Summary))
	}
	assertRegistryPayload(t, entries[0], want)
}

// TestRegistryRefresh_RemovedTwice_RecordsOneEvent pins the removal's own
// idempotence (fix round 1): the click that actually removed the record
// records it; a second click, with nothing left on file, still answers
// removed but writes no duplicate entry about a disappearance already on the
// timeline.
func TestRegistryRefresh_RemovedTwice_RecordsOneEvent(t *testing.T) {
	t.Parallel()
	var removed bool
	transport := &registryTransport{respond: func(string) (*http.Response, error) {
		if removed {
			return registryEntityResponse(http.StatusGone, removedRegistryBody), nil
		}
		return registryEntityResponse(http.StatusOK, equinorRegistryBody), nil
	}}
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	removed = true
	h.Advance(time.Hour) // past the throttle window, so the click really asks
	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}

	// No advance needed for the third: the removal deleted the row, so there is
	// no fetchedAt left to throttle against.
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("third refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "removed" || got.Record != nil || len(got.Changes) != 0 {
		t.Errorf("refresh = %+v, want removed with no record and no changes", got)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 1 {
		t.Errorf("timeline events = %d, want 1 (only the click that removed the record)", len(entries))
	}
}

// TestRegistryRefresh_RemovedWithNothingOnFile_RecordsNothing is the same
// rule on a customer whose record was never fetched: the registry says the
// entity left open data, there is nothing to delete, and nothing happened
// that this customer's timeline should claim did.
func TestRegistryRefresh_RemovedWithNothingOnFile_RecordsNothing(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryStatus(http.StatusGone, removedRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Never Fetched Co", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "removed" || got.Record != nil || len(got.Changes) != 0 {
		t.Errorf("refresh = %+v, want removed with no record and no changes", got)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0", n)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}
}

// TestCreateCustomer_WithBrregPickOfADeletedEntity_ReportsTheDeletion pins
// the other half of the first-fetch comparison (fix round 1): picking a
// company that has already been struck from the register stores the deletion
// *and* says so on the timeline, rather than filing it silently.
func TestCreateCustomer_WithBrregPickOfADeletedEntity_ReportsTheDeletion(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(deletedRegistryBody))
	c := authenticatedClient(t, h)

	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	rec := fetchRegistryRecord(t, c, created.Id)
	if str(rec.DeletedOn) != "2026-09-21" {
		t.Errorf("DeletedOn = %v, want 2026-09-21", rec.DeletedOn)
	}
	entries := fetchRegistryEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want 1", len(entries))
	}
	if str(entries[0].Summary) != "Registry record updated: deletedOn" {
		t.Errorf("summary = %q, want the deletion named", str(entries[0].Summary))
	}
	assertRegistryPayload(t, entries[0], []registryChangeJSON{{Field: "deletedOn", To: ptr("2026-09-21")}})
}

// TestRegistryRefresh_RestoredEntity_ClearsTheDeletion is the reverse: an
// entity that is found alive again clears deletedOn and reports that it did,
// so a record wrongly marked deleted never sticks.
func TestRegistryRefresh_RestoredEntity_ClearsTheDeletion(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registrySequence(deletedRegistryBody, equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}

	h.Advance(time.Hour) // past the throttle window, so the click really asks
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "found" {
		t.Errorf("status = %q, want found", got.Status)
	}
	want := []registryChangeJSON{
		{Field: "organisationForm", To: ptr("Allmennaksjeselskap")},
		{Field: "industryCode", To: ptr("06.100")},
		{Field: "employees", To: ptr("21272")},
		{Field: "vatRegistered", From: ptr("false"), To: ptr("true")},
		{Field: "deletedOn", From: ptr("2026-09-21")},
		{Field: "website", To: ptr("www.equinor.com")},
		{Field: "businessAddress", To: ptr("Forusbeen 50, 4035 STAVANGER, NO")},
		{Field: "postalAddress", To: ptr("Postboks 8500, 4035 STAVANGER, NO")},
	}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}
	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.DeletedOn != nil {
		t.Errorf("DeletedOn = %v, want nil (the entity is alive again)", rec.DeletedOn)
	}
}

// TestPutLegalIdentity_Unchanged_FetchesNothing pins the no-op rule reaching
// the registry too: a resubmit of exactly the identity already stored writes
// nothing and, since it returns before the transaction ever opens, makes no
// network call either.
func TestPutLegalIdentity_Unchanged_FetchesNothing(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Resubmit Identity Co")
	identity := map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "EQUINOR ASA", "source": "brreg",
	}

	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), identity); r.Status != http.StatusOK {
		t.Fatalf("first put: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := len(transport.requests()); n != 1 {
		t.Fatalf("requests after the first put = %d, want 1", n)
	}

	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), identity); r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := len(transport.requests()); n != 1 {
		t.Errorf("requests = %d, want still 1 (an unchanged identity fetches nothing)", n)
	}
}

// TestRegistryRefresh_EmptyAddressLines_RoundTripAsAnEmptyArray pins the
// stored address shape: an address the registry sends with no street lines
// at all is still an address, and its lines come back as an empty array
// rather than null.
func TestRegistryRefresh_EmptyAddressLines_RoundTripAsAnEmptyArray(t *testing.T) {
	t.Parallel()
	body := `{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": false,
		"forretningsadresse": {"landkode": "NO", "adresse": []},
		"konkurs": false,
		"underAvvikling": false,
		"underTvangsavviklingEllerTvangsopplosning": false
	}`
	h := newRegistryHarness(t, registryBody(body))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.BusinessAddress == nil {
		t.Fatalf("BusinessAddress = nil, want an address with no lines")
	}
	if rec.BusinessAddress.Lines == nil {
		t.Errorf("Lines = null, want an empty array")
	}
	if len(rec.BusinessAddress.Lines) != 0 || rec.BusinessAddress.CountryCode != "NO" {
		t.Errorf("BusinessAddress = %+v, want no lines and country NO", rec.BusinessAddress)
	}
}

// TestRegistryRefresh_UnknownOrganisationNumber_StoresNothing pins the 404
// outcome: the registry does not know this number, which is worth telling
// the caller and nothing else — no record, no event.
func TestRegistryRefresh_UnknownOrganisationNumber_StoresNothing(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryStatus(http.StatusNotFound, ""))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Unknown Number Co", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "unknown" || got.Record != nil || len(got.Changes) != 0 {
		t.Errorf("refresh = %+v, want unknown with nothing else", got)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0", n)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}
}

// TestRegistryRefresh_WithoutARegistryIdentity_Returns409 pins design D2's
// conflict: there is nothing to look up for a customer with no Norwegian
// organisation number, whichever way it lacks one.
func TestRegistryRefresh_WithoutARegistryIdentity_Returns409(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, h *modtest.Harness, c *modtest.Client) int32
	}{
		{"no identity at all", func(t *testing.T, _ *modtest.Harness, c *modtest.Client) int32 {
			return createCustomer(t, c, "No Identity Co").Id
		}},
		{"a foreign identity", func(t *testing.T, _ *modtest.Harness, c *modtest.Client) int32 {
			return createCustomerWithIdentity(t, c, "Swedish Co", "se", "5560000000").Id
		}},
		{"a person", func(t *testing.T, _ *modtest.Harness, c *modtest.Client) int32 {
			r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
				"name": "Private Person", "type": "person",
				"identity": map[string]any{"country": "no", "type": "person", "id": "01019012345", "name": "Private Person", "source": "manual"},
			})
			if r.Status != http.StatusCreated {
				t.Fatalf("create person: status %d body %s", r.Status, r.Body)
			}
			var created createdCustomerJSON
			r.JSON(&created)
			return created.Id
		}},
		{"a legacy organisation number that is not one", func(t *testing.T, h *modtest.Harness, c *modtest.Client) int32 {
			// Written straight to the row: validateLegalIdentity refuses this
			// today, but rows from before it did exist (billing_values.go's
			// derivedPeppolID makes the same allowance), and a refresh must
			// answer the conflict rather than ask the registry about
			// "NO 923 609 016 MVA".
			id := createCustomer(t, c, "Legacy Identity Co").Id
			h.Exec(t, `UPDATE customers.customers SET legal_country = 'no', legal_type = 'business',
				legal_id = 'NO 923 609 016 MVA', legal_name = 'Legacy Identity Co', legal_source = 'manual' WHERE id = $1`, id)
			return id
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := registryBody(equinorRegistryBody)
			h := newRegistryHarness(t, transport)
			c := authenticatedClient(t, h)
			id := tc.setup(t, h, c)

			r := postRegistryRefresh(t, c, id)
			if r.Status != http.StatusConflict {
				t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
			}
			var problem conflictProblemJSON
			r.JSON(&problem)
			if str(problem.Code) != "no_registry_identity" {
				t.Errorf("code = %v, want no_registry_identity", problem.Code)
			}
			if reqs := transport.requests(); len(reqs) != 0 {
				t.Errorf("requests = %v, want none (the conflict is decided before any call)", reqs)
			}
		})
	}
}

// TestRegistryRefresh_UpstreamFailure_Returns502AndKeepsTheRecord pins
// design D2's 502 boundary, shaped like the Peppol lookup's own: a failed
// fetch stores nothing, records nothing, and leaves the record on file
// exactly as it was.
func TestRegistryRefresh_UpstreamFailure_Returns502AndKeepsTheRecord(t *testing.T) {
	t.Parallel()
	var fail bool
	transport := &registryTransport{respond: func(string) (*http.Response, error) {
		if fail {
			return registryEntityResponse(http.StatusInternalServerError, `{"title":"boom"}`), nil
		}
		return registryEntityResponse(http.StatusOK, equinorRegistryBody), nil
	}}
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchRegistryRecord(t, c, created.Id)

	fail = true
	h.Advance(time.Hour)
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("status %d body %s, want 502", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Registry unavailable" {
		t.Errorf("problem = %+v, want the registry-unavailable title", problem)
	}

	after := fetchRegistryRecord(t, c, created.Id)
	if !after.FetchedAt.Equal(before.FetchedAt) || after.Name != before.Name {
		t.Errorf("record = %+v, want unchanged from %+v (a 502 stores nothing)", after, before)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}
}

// TestRegistryRefresh_WithoutLegalIdentityManage_ReturnsForbidden pins the
// access rule (customers:legal-identity-manage+customers:legal-identity-view),
// enforced by the router from x-vantigo-access.
func TestRegistryRefresh_WithoutLegalIdentityManage_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	owner := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, owner, "EQUINOR ASA", "no", "923609016")

	viewer := h.SignIn(t, "customers:view", "customers:legal-identity-view")
	if r := postRegistryRefresh(t, viewer, created.Id); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestRegistryRefresh_WithoutLegalIdentityView_ReturnsForbidden is the other
// half of that rule (fix round 2, C1): the refresh hands back the whole
// record and every from/to, so it is gated on the very permission the GET
// withholds the record without — a caller who may write the identity but not
// read it must not be able to read the registry's copy of it through a
// Refresh. Follows PUT .../legal-identity's own manage+view precedent.
func TestRegistryRefresh_WithoutLegalIdentityView_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	owner := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, owner, "EQUINOR ASA", "no", "923609016")

	writer := h.SignIn(t, "customers:view", "customers:legal-identity-manage")
	if r := postRegistryRefresh(t, writer, created.Id); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
	if reqs := transport.requests(); len(reqs) != 0 {
		t.Errorf("requests = %v, want none (the router refuses before the handler runs)", reqs)
	}
}

// TestRegistryRefresh_UnknownCustomer_Returns404 pins the 404-first
// ordering: an unknown customer never reaches the identity check.
func TestRegistryRefresh_UnknownCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)

	if r := postRegistryRefresh(t, c, 999999); r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestRegistryRefresh_TruncatesOverlongText pins the storage rule: a navn
// longer than the column is truncated in UTF-16 units rather than failing
// the insert — the registry promises no length for its free text, and a
// refresh that 500s on a long company name would be this module's bug, not
// the registry's.
func TestRegistryRefresh_TruncatesOverlongText(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("Æ", 400)
	body := fmt.Sprintf(`{
		"organisasjonsnummer": "923609016",
		"navn": %q,
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": true,
		"konkurs": false,
		"underAvvikling": false,
		"underTvangsavviklingEllerTvangsopplosning": false
	}`, longName)
	h := newRegistryHarness(t, registryBody(body))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Long Name Co", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	rec := fetchRegistryRecord(t, c, created.Id)
	if n := len([]rune(rec.Name)); n != 255 {
		t.Errorf("name length = %d runes, want 255 (truncated to the column)", n)
	}
	if !strings.HasPrefix(longName, rec.Name) {
		t.Errorf("name = %q, want a prefix of what the registry sent", rec.Name)
	}
}

// TestRegistryRefresh_TruncatesSurrogatePairsByUTF16Units pins what
// "truncated to the column" actually counts: UTF-16 code units, the same
// unit truncateUTF16 uses everywhere else in this module (timeline.go). An
// emoji is two units, so 400 of them are cut at 255 units — 127 whole emoji
// and one half of a pair, which decodes to a replacement character rather
// than invalid UTF-8 the database would refuse.
func TestRegistryRefresh_TruncatesSurrogatePairsByUTF16Units(t *testing.T) {
	t.Parallel()
	emojiName := strings.Repeat("🙂", 400)
	body := fmt.Sprintf(`{
		"organisasjonsnummer": "923609016",
		"navn": %q,
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": false,
		"konkurs": false,
		"underAvvikling": false,
		"underTvangsavviklingEllerTvangsopplosning": false
	}`, emojiName)
	h := newRegistryHarness(t, registryBody(body))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Emoji Name Co", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	rec := fetchRegistryRecord(t, c, created.Id)
	if n := len(utf16.Encode([]rune(rec.Name))); n != 255 {
		t.Errorf("name length = %d UTF-16 units, want 255", n)
	}
	runes := []rune(rec.Name)
	if len(runes) != 128 {
		t.Fatalf("name = %d runes, want 128 (127 whole emoji plus the split pair)", len(runes))
	}
	if string(runes[:127]) != strings.Repeat("🙂", 127) {
		t.Errorf("name = %q, want the first 127 emoji intact", rec.Name)
	}
	if !utf8.ValidString(rec.Name) {
		t.Errorf("name = %q, want valid UTF-8", rec.Name)
	}
}

// TestRegistryRefresh_TruncatesAShortColumnToo pins the same rule on one of
// the short columns: telefon is varchar(30), and a registry body carrying
// more than that is stored truncated rather than answered with a 500 from a
// database-level "value too long".
func TestRegistryRefresh_TruncatesAShortColumnToo(t *testing.T) {
	t.Parallel()
	longPhone := strings.Repeat("9", 40)
	body := fmt.Sprintf(`{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
		"harRegistrertAntallAnsatte": false,
		"registrertIMvaregisteret": false,
		"telefon": %q,
		"konkurs": false,
		"underAvvikling": false,
		"underTvangsavviklingEllerTvangsopplosning": false
	}`, longPhone)
	h := newRegistryHarness(t, registryBody(body))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	rec := fetchRegistryRecord(t, c, created.Id)
	if str(rec.Phone) != strings.Repeat("9", 30) {
		t.Errorf("phone = %q, want 30 nines (truncated to the column)", str(rec.Phone))
	}
}

// brregSelfRegistryBody is a second, different company (Registerenheten i
// Brønnøysund, 974760673): what a customer's record must become once its
// identity is pointed at another entity, and what it must never be while the
// identity still names Equinor.
const brregSelfRegistryBody = `{
	"organisasjonsnummer": "974760673",
	"navn": "REGISTERENHETEN I BRØNNØYSUND",
	"organisasjonsform": {"kode": "ORGL", "beskrivelse": "Organisasjonsledd"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 487,
	"registrertIMvaregisteret": false,
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// bankruptEquinorRegistryBody is equinorRegistryBody with two flags flipped
// and the dates the registry sends beside them: the same entity, years after
// it went under, so a diff against the healthy body names the two flags and
// nothing else.
const bankruptEquinorRegistryBody = `{
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA",
	"organisasjonsform": {"kode": "ASA", "beskrivelse": "Allmennaksjeselskap"},
	"naeringskode1": {"kode": "06.100", "beskrivelse": "Utvinning av råolje"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 21272,
	"registrertIMvaregisteret": true,
	"stiftelsesdato": "1972-06-14",
	"hjemmeside": "www.equinor.com",
	"forretningsadresse": {"landkode": "NO", "postnummer": "4035", "poststed": "STAVANGER", "adresse": ["Forusbeen 50"], "kommune": "STAVANGER"},
	"postadresse": {"landkode": "NO", "postnummer": "4035", "poststed": "STAVANGER", "adresse": ["Postboks 8500"], "kommune": "STAVANGER"},
	"konkurs": true,
	"konkursdato": "2019-03-04",
	"underAvvikling": true,
	"underAvviklingDato": "2018-11-30",
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// undatedDeletedRegistryBody is a SlettetEnhet with no slettedato at all: the
// respons_klasse is the fact, and the date is missing.
const undatedDeletedRegistryBody = `{
	"respons_klasse": "SlettetEnhet",
	"organisasjonsnummer": "923609016",
	"navn": "EQUINOR ASA"
}`

// registryByOrgNumber answers each organisation number with its own body,
// keyed by the last path segment — what a test needs once one customer's
// identity moves from one company to another.
func registryByOrgNumber(bodies map[string]string) *registryTransport {
	return &registryTransport{respond: func(path string) (*http.Response, error) {
		orgnr := path[strings.LastIndex(path, "/")+1:]
		body, ok := bodies[orgnr]
		if !ok {
			return registryEntityResponse(http.StatusNotFound, ""), nil
		}
		return registryEntityResponse(http.StatusOK, body), nil
	}}
}

// putCustomerIdentity is PUT /customers/{id} carrying a Brreg-picked identity
// — the edit modal's own picker path, as opposed to the dedicated
// legal-identity endpoint.
func putCustomerIdentity(t *testing.T, c *modtest.Client, id int32, name, orgNumber, identityName, source string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", id), map[string]any{
		"name": name,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": orgNumber, "name": identityName, "source": source,
		},
	})
}

// TestUpdateCustomer_WithANewBrregPick_ReplacesTheRecord pins both halves of
// fix round 2's C2 on the edit modal's own path: a PUT /customers/{id} that
// points the customer at another company deletes the record fetched for the
// old one (in the same transaction as the write) and fetches the new one
// after the commit, exactly as PUT .../legal-identity already did.
func TestUpdateCustomer_WithANewBrregPick_ReplacesTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryByOrgNumber(map[string]string{
		"923609016": equinorRegistryBody,
		"974760673": brregSelfRegistryBody,
	})
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if rec := fetchRegistryRecord(t, c, created.Id); rec.Name != "EQUINOR ASA" {
		t.Fatalf("record name = %q, want EQUINOR ASA before the identity moves", rec.Name)
	}

	r := putCustomerIdentity(t, c, created.Id, "Renamed Customer", "974760673", "REGISTERENHETEN I BRØNNØYSUND", "brreg")
	if r.Status != http.StatusOK {
		t.Fatalf("put customer: status %d body %s, want 200", r.Status, r.Body)
	}

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.OrganisationNumber != "974760673" || rec.Name != "REGISTERENHETEN I BRØNNØYSUND" {
		t.Errorf("record = %+v, want the new company's", rec)
	}
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Errorf("registry rows = %d, want exactly 1 (the old company's row is gone, not kept beside)", n)
	}
	if reqs := transport.requests(); len(reqs) != 2 {
		t.Errorf("requests = %v, want two (the create's and the update's)", reqs)
	}
}

// TestUpdateCustomer_WithAManuallyTypedIdentity_DeletesTheRecord is the half
// of C2 no later fetch can cover for: the new identity is typed in by hand, so
// no hook fetches anything, and the only thing standing between the customer
// and another company's record on file is the delete inside the identity's own
// transaction.
func TestUpdateCustomer_WithAManuallyTypedIdentity_DeletesTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows = %d, want 1 before the identity moves", n)
	}

	r := putCustomerIdentity(t, c, created.Id, "Somebody Else AS", "974760673", "SOMEBODY ELSE AS", "manual")
	if r.Status != http.StatusOK {
		t.Fatalf("put customer: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (the old company's record is deleted, not left behind)", n)
	}
	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := len(transport.requests()); n != 1 {
		t.Errorf("requests = %d, want still 1 (a manually typed identity is not a pick)", n)
	}
}

// TestUpdateCustomer_WithoutTouchingTheIdentity_KeepsTheRecord is the other
// side of that rule: a rename is not an identity change, so the record stands
// and nothing is fetched.
func TestUpdateCustomer_WithoutTouchingTheIdentity_KeepsTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Equinor, our name for them"})
	if r.Status != http.StatusOK {
		t.Fatalf("put customer: status %d body %s, want 200", r.Status, r.Body)
	}

	if rec := fetchRegistryRecord(t, c, created.Id); rec.Name != "EQUINOR ASA" {
		t.Errorf("record = %+v, want the record kept", rec)
	}
	if n := len(transport.requests()); n != 1 {
		t.Errorf("requests = %d, want still 1 (a rename asks the registry nothing)", n)
	}
}

// TestUpdateCustomer_WithTheSameOrganisationNumber_KeepsTheRecord pins the
// rule's exact hinge: the organisation number, not the identity. Editing the
// identity's own name (or its source) leaves the record where it is, because
// it is still a record of the same company.
func TestUpdateCustomer_WithTheSameOrganisationNumber_KeepsTheRecord(t *testing.T) {
	t.Parallel()
	transport := registryBody(equinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	first := fetchRegistryRecord(t, c, created.Id)

	r := putCustomerIdentity(t, c, created.Id, "EQUINOR ASA", "923609016", "Equinor ASA (Statoil)", "brreg")
	if r.Status != http.StatusOK {
		t.Fatalf("put customer: status %d body %s, want 200", r.Status, r.Body)
	}

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.OrganisationNumber != "923609016" || !rec.FetchedAt.Equal(first.FetchedAt) {
		t.Errorf("record = %+v, want the same row untouched (%+v)", rec, first)
	}
}

// TestDeleteLegalIdentity_RemovesTheRecord pins C2 on the delete: with no
// identity there is no company the record could be about, so it goes — and
// the attention item it was raising goes with it, even though the record said
// the company was bankrupt.
func TestDeleteLegalIdentity_RemovesTheRecord(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(bankruptEquinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if items := getAttention(t, c); len(items) != 1 {
		t.Fatalf("items = %+v, want one bankruptcy item before the identity is removed", items)
	}

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("delete legal identity: status %d body %s, want 204", r.Status, r.Body)
	}

	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0", n)
	}
	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
	if items := getAttention(t, c); len(items) != 0 {
		t.Errorf("items = %+v, want none once the identity is gone", items)
	}
}

// TestPutCustomerType_ToPerson_RemovesTheRecord pins C2 on the type change: a
// private person has no organisation number, the business identity is removed
// in the same transaction, and the record fetched for it must not outlive it.
func TestPutCustomerType_ToPerson_RemovesTheRecord(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows = %d, want 1 before the type change", n)
	}

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "person"})
	if r.Status != http.StatusOK {
		t.Fatalf("put type: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (a person has no registry record)", n)
	}
	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
}

// TestGetRegistryRecord_ForAnotherCompanysRecord_Returns204 pins C2's
// read-side guard on its own, against a row written straight to the database:
// whatever put it there — a race, a write path added later — a record whose
// organisation number is not the customer's is never shown and never raises an
// attention item.
func TestGetRegistryRecord_ForAnotherCompanysRecord_Returns204(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")
	h.Exec(t, `
		INSERT INTO customers.customer_registry_records
			(customer_id, organisation_number, name, vat_registered, bankrupt, under_liquidation, under_forced_liquidation, fetched_at)
		VALUES ($1, '974760673', 'SOMEBODY ELSE AS', false, true, false, false, $2)`, created.Id, h.Now())

	if r := getRegistryRecord(t, c, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("get registry record: status %d body %s, want 204", r.Status, r.Body)
	}
	if items := getAttention(t, c); len(items) != 0 {
		t.Errorf("items = %+v, want none for another company's record", items)
	}
}

// TestRegistryRefresh_WithinTheThrottleWindow_AnswersTheStoredRecord pins I2:
// the endpoint is one outbound GET per click on an open API whose 429 is not
// retryable, so a second click within registryRefreshMinInterval answers what
// is already on file — no request, no changes, no event — and a click past the
// window asks again.
func TestRegistryRefresh_WithinTheThrottleWindow_AnswersTheStoredRecord(t *testing.T) {
	t.Parallel()
	transport := registrySequence(equinorRegistryBody, movedEquinorRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	first := fetchRegistryRecord(t, c, created.Id)

	h.Advance(59 * time.Second)
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "found" || len(got.Changes) != 0 {
		t.Errorf("refresh = %+v, want found with no changes", got)
	}
	if got.Record == nil || !got.Record.FetchedAt.Equal(first.FetchedAt) {
		t.Errorf("record = %+v, want the stored record with its own fetchedAt %v", got.Record, first.FetchedAt)
	}
	if n := len(transport.requests()); n != 1 {
		t.Fatalf("requests = %d, want still 1 (a click inside the window asks nothing)", n)
	}
	if entries := fetchRegistryEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}

	// One second later the window has passed, and the click reaches the
	// registry — which by now has moved the headcount and the address.
	h.Advance(2 * time.Second)
	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("third refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := len(transport.requests()); n != 2 {
		t.Errorf("requests = %d, want 2 (past the window the click really asks)", n)
	}
	if rec := fetchRegistryRecord(t, c, created.Id); rec.Employees == nil || *rec.Employees != 21000 {
		t.Errorf("Employees = %v, want 21000 (the fetch past the window stored the new record)", rec.Employees)
	}
}

// TestRegistryRefresh_ThrottledDeletedRecord_AnswersDeleted pins the status a
// throttled answer carries: it is the stored row's own, so a record with a
// deletion date answers "deleted" rather than "found" — the same thing a real
// fetch would have said a moment earlier.
func TestRegistryRefresh_ThrottledDeletedRecord_AnswersDeleted(t *testing.T) {
	t.Parallel()
	transport := registryBody(deletedRegistryBody)
	h := newRegistryHarness(t, transport)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "deleted" || got.Record == nil || str(got.Record.DeletedOn) != "2026-09-21" {
		t.Errorf("refresh = %+v, want deleted with the stored record", got)
	}
	if n := len(transport.requests()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

// TestRegistryRefresh_StoresTheStatusDates pins I3's storage half: the two
// dates the registry sends beside its flags reach the row, and the attention
// item is dated by them rather than by the fetch. Neither date is diffed or
// sent on the wire — the flags beside them are what a refresh reports.
func TestRegistryRefresh_StoresTheStatusDates(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registrySequence(equinorRegistryBody, bankruptEquinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	h.Advance(time.Hour) // past the throttle window
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	want := []registryChangeJSON{
		{Field: "bankrupt", From: ptr("false"), To: ptr("true")},
		{Field: "underLiquidation", From: ptr("false"), To: ptr("true")},
	}
	if !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v (the flags, never the dates)", got.Changes, want)
	}

	stored := modtest.One[string](t, h,
		`SELECT bankrupt_on::text || ' ' || liquidation_on::text FROM customers.customer_registry_records WHERE customer_id = $1`, created.Id)
	if stored != "2019-03-04 2018-11-30" {
		t.Errorf("bankrupt_on/liquidation_on = %q, want \"2019-03-04 2018-11-30\"", stored)
	}

	items := getAttention(t, c)
	if len(items) != 1 || items[0].Type != "registryBankrupt" {
		t.Fatalf("items = %+v, want one registryBankrupt item", items)
	}
	if want := "2019-03-04T00:00:00Z"; items[0].OccurredAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("occurredAt = %v, want %s (the day it happened, not the fetch)", items[0].OccurredAt, want)
	}
}

// TestRegistryRefresh_DeletedEntityWithNoDate_IsDatedTheFetch pins the
// undated SlettetEnhet (fix round 2, minors): the respons_klasse is the
// load-bearing fact, so the record is stored as deleted with the fetch date
// standing in — a deletion stored with no date at all would be reported as no
// change and raise no attention item, the one outcome that must not happen.
func TestRegistryRefresh_DeletedEntityWithNoDate_IsDatedTheFetch(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(undatedDeletedRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	today := h.Now().UTC().Format("2006-01-02")
	if got.Status != "deleted" || got.Record == nil || str(got.Record.DeletedOn) != today {
		t.Errorf("refresh = %+v, want deleted dated %s", got, today)
	}
	if want := []registryChangeJSON{{Field: "deletedOn", To: ptr(today)}}; !sameChanges(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}
	if items := getAttention(t, c); len(items) != 1 || items[0].Type != "registryDeleted" {
		t.Errorf("items = %+v, want one registryDeleted item", items)
	}
}

// TestRegistryRefresh_DeletedEntityWithNoDate_KeepsTheDateOnFile is the
// other half of the stand-in date: it stands in once. A later refresh that
// again carries no slettedato must keep the date already stored, not re-stamp
// its own fetch day — deletedOn is a diffed field and the attention item's
// occurredAt, so re-stamping would write a false registry.change every day
// and re-float a settled deletion to the top of the list on every click.
func TestRegistryRefresh_DeletedEntityWithNoDate_KeepsTheDateOnFile(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(undatedDeletedRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	if r := postRegistryRefresh(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	firstDay := h.Now().UTC().Format("2006-01-02")

	h.Advance(24 * time.Hour)
	c = authenticatedClient(t, h) // a day outlives the session; sign in again
	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("second refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var got registryRefreshJSON
	r.JSON(&got)
	if got.Status != "deleted" || got.Record == nil || str(got.Record.DeletedOn) != firstDay {
		t.Errorf("second refresh = %+v, want deleted still dated %s", got, firstDay)
	}
	if len(got.Changes) != 0 {
		t.Errorf("changes = %+v, want none (the stand-in date stands in once)", got.Changes)
	}
	items := getAttention(t, c)
	if len(items) != 1 || items[0].OccurredAt.UTC().Format("2006-01-02") != firstDay {
		t.Errorf("items = %+v, want one item still dated %s", items, firstDay)
	}
}

// TestRegistryRefresh_AnswerAboutAnotherOrganisation_Returns502 pins the
// organisation-number check where a user can see it (fix round 2, minors): a
// body about a different company never becomes this customer's record, and the
// refresh reports it as the registry being unusable rather than storing it.
func TestRegistryRefresh_AnswerAboutAnotherOrganisation_Returns502(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(brregSelfRegistryBody))
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "EQUINOR ASA", "no", "923609016")

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("status %d body %s, want 502", r.Status, r.Body)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0 (another company's record is never stored)", n)
	}
}

// assertRegistryPayload checks a registry.change entry's payload carries
// exactly the changes the response reported, with an empty side omitted
// rather than sent as null.
func assertRegistryPayload(t *testing.T, e timelineEntryJSON, want []registryChangeJSON) {
	t.Helper()
	var payload struct {
		Changes []map[string]any `json:"changes"`
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload.Changes) != len(want) {
		t.Fatalf("payload changes = %v, want %d", payload.Changes, len(want))
	}
	for i, w := range want {
		got := payload.Changes[i]
		if got["field"] != w.Field {
			t.Errorf("payload change %d field = %v, want %q", i, got["field"], w.Field)
		}
		if w.From == nil {
			if _, ok := got["from"]; ok {
				t.Errorf("payload change %d = %v, must omit an empty from", i, got)
			}
		} else if got["from"] != *w.From {
			t.Errorf("payload change %d from = %v, want %q", i, got["from"], *w.From)
		}
		if w.To == nil {
			if _, ok := got["to"]; ok {
				t.Errorf("payload change %d = %v, must omit an empty to", i, got)
			}
		} else if got["to"] != *w.To {
			t.Errorf("payload change %d to = %v, want %q", i, got["to"], *w.To)
		}
	}
}

// sameChanges compares a reported change list with what a test expects,
// including which of from/to were omitted.
func sameChanges(got, want []registryChangeJSON) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Field != want[i].Field || str(got[i].From) != str(want[i].From) || str(got[i].To) != str(want[i].To) {
			return false
		}
		if (got[i].From == nil) != (want[i].From == nil) || (got[i].To == nil) != (want[i].To == nil) {
			return false
		}
	}
	return true
}
