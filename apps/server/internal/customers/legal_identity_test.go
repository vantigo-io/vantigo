package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports the two Task 6 deferred tests: two of
// Integration/CustomersEndpointsTests.cs's 24 tests are named for
// PutCustomersById but actually exercise the dedicated legal-identity
// endpoints (customers_test.go's file doc comment) —
// UpdateCustomer_WithoutIdentity_RemovesExistingIdentity calls DELETE
// .../legal-identity, and
// UpdateCustomer_WithUnreadableIdentityPayload_ReturnsSanitizedProblemDetails
// calls PUT .../legal-identity. Both are ported below.
//
// Everything else here is not a port: with those two exceptions, no .NET
// test anywhere in customers inventory §7's portable set calls
// GET/PUT/DELETE .../legal-identity directly — CreateCustomer_WithLegalIdentity_PersistsIdentity
// and UpdateCustomer_WithIdentity_ReplacesIdentity also touch the GET
// endpoint, but only as an assertion helper, and Task 6 already replaced
// those assertions with a direct row fixture (legalRow/fetchLegalRow).
// Without the tests below, GetCustomersByIdLegalIdentity and
// DeleteCustomersByIdLegalIdentity would have no happy-path coverage at
// all, and PutCustomersByIdLegalIdentity none beyond its one malformed-body
// case — exactly the toothless-test pattern the last two tasks left behind.

// legalIdentityJSON is LegalIdentityResponse's five fields.
type legalIdentityJSON struct {
	Country string `json:"country"`
	Type    string `json:"type"`
	Id      string `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source"`
}

// fetchCustomerJSON reads GET /customers/{id} and decodes it, failing the
// test on anything but 200.
func fetchCustomerJSON(t *testing.T, c *modtest.Client, id int32) customerJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /customers/%d: status %d body %s", id, r.Status, r.Body)
	}
	var customer customerJSON
	r.JSON(&customer)
	return customer
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WithoutIdentity_RemovesExistingIdentity — named for
// PutCustomersById, but its body actually calls DELETE .../legal-identity
// (see the file doc comment above).
func TestUpdateCustomer_WithoutIdentity_RemovesExistingIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Removable",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "912345670", "name": "Removable AS", "source": "manual",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	r = c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}

	updated := fetchCustomerJSON(t, c, created.Id)
	if updated.Identity != nil {
		t.Errorf("Identity = %+v, want nil after DELETE .../legal-identity", updated.Identity)
	}

	r = c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Errorf("GET .../legal-identity status %d body %s, want 204", r.Status, r.Body)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WithUnreadableIdentityPayload_ReturnsSanitizedProblemDetails
// — named for PutCustomersById, but its body actually PUTs
// .../legal-identity with a payload the framework cannot bind (see the file
// doc comment above). .NET's LegalIdentityRequest requires country/type/id/
// name/source all at the top level; the body sent here supplies none of
// them (a top-level "name" and a nested "identity" object instead), which
// .NET's required-member deserialization rejects before the handler ever
// runs, and the centralized exception handler reports as Problem Details
// with a status and a traceId, never the raw BadHttpRequestException.
//
// oapi-codegen's generated types are not required-member-aware — a missing
// JSON field just decodes to its zero value — so that specific body would
// not reproduce a bind failure here; it would reach validateLegalIdentity
// as five blank fields and answer an ordinary 400 ValidationProblem, no
// different from any other "everything blank" case already covered below.
// The genuine Go analogue of "a payload the framework cannot bind" is a
// body encoding/json itself cannot parse, which module.DecodeError's
// generic writeDecodeError (errors.go) already exists to sanitize — a path
// this operation shares with every other one in the module, but had no
// test of its own before this. It answers through httpx.WriteProblem,
// which (unlike this module's own problem()/validationProblem()) does
// carry a traceId, so the .NET assertions port almost verbatim.
func TestUpdateCustomer_WithUnreadableIdentityPayload_ReturnsSanitizedProblemDetails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Identity Validation Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil,
		modtest.RawBody("application/json", []byte(`{"country": "no", "type": "business",`)), // truncated, unparsable JSON
		modtest.SkipContract("a body the JSON decoder cannot parse, deliberately off-contract"))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}

	// The centralized handler reports it as Problem Details; the decode
	// error's own text must never reach the caller.
	lower := strings.ToLower(string(r.Body))
	for _, leak := range []string{"unexpected end of json", "invalid character", "unmarshal", "syntax error"} {
		if strings.Contains(lower, leak) {
			t.Errorf("body %s leaks the decoder's own error text (%q)", r.Body, leak)
		}
	}

	var problem struct {
		Status  int    `json:"status"`
		Detail  string `json:"detail"`
		TraceID string `json:"traceId"`
	}
	r.JSON(&problem)
	if problem.Status != http.StatusBadRequest {
		t.Errorf("status field = %d, want 400", problem.Status)
	}
	if problem.Detail != "" {
		t.Errorf("detail = %q, want empty (never echoing what failed to parse)", problem.Detail)
	}
	if strings.TrimSpace(problem.TraceID) == "" {
		t.Error("traceId is empty or whitespace, want a value to quote when reporting this problem")
	}
}

// TestPutCustomersByIdLegalIdentity_WithDotNetOriginalPayload_ReturnsSanitizedValidationProblem
// sends .NET's actual UpdateCustomer_WithUnreadableIdentityPayload_ReturnsSanitizedProblemDetails
// payload (CustomersEndpointsTests.cs:308-319) verbatim — well-formed JSON
// missing every one of LegalIdentityRequest's required top-level members
// (country/type/id/name/source), not unparsable JSON. .NET's
// required-member deserialization rejects this before the handler runs;
// oapi-codegen's generated types are not required-member-aware, so it binds
// cleanly with every field defaulting to "" (the body's own top-level
// "name": "" happens to agree with that default) and reaches
// validateLegalIdentity as five blank fields — an ordinary sanitized 400
// ValidationProblem, not a crash and not a 500. This is the fix-round
// addition confirming the substitution above (a body encoding/json itself
// cannot parse) didn't leave .NET's actual payload unpinned.
func TestPutCustomersByIdLegalIdentity_WithDotNetOriginalPayload_ReturnsSanitizedValidationProblem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Identity Validation Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"name": "",
		"identity": map[string]any{
			"country": "", "type": "business", "id": " ", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}

	var problem struct {
		Title  string              `json:"title"`
		Status int                 `json:"status"`
		Errors map[string][]string `json:"errors"`
	}
	r.JSON(&problem)
	if problem.Status != http.StatusBadRequest {
		t.Errorf("status field = %d, want 400", problem.Status)
	}
	if problem.Title != "Invalid legal identity" {
		t.Errorf("title = %q, want %q", problem.Title, "Invalid legal identity")
	}
	for _, field := range []string{"country", "type", "id", "name", "source"} {
		if len(problem.Errors[field]) != 1 {
			t.Errorf("errors[%q] = %v, want exactly one message (every required field is missing from this payload)", field, problem.Errors[field])
		}
	}
}

// TestGetCustomersByIdLegalIdentity_ReturnsIdentityWhenPresent pins
// LegalIdentityEndpoints.Get's 200 path (LegalIdentityEndpoints.cs:13-29):
// not a port (see the file doc comment) — CreateCustomer_WithLegalIdentity_PersistsIdentity
// is the nearest .NET test, but it checks this endpoint's response as a
// side assertion, not as its own subject.
func TestGetCustomersByIdLegalIdentity_ReturnsIdentityWhenPresent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "NO", "type": "Business", "id": "923609016", "name": "Acme AS", "source": "brreg",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	r = c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var identity legalIdentityJSON
	r.JSON(&identity)
	want := legalIdentityJSON{Country: "no", Type: "business", Id: "923609016", Name: "Acme AS", Source: "brreg"}
	if identity != want {
		t.Errorf("identity = %+v, want %+v", identity, want)
	}
}

// TestGetCustomersByIdLegalIdentity_ReturnsNoContentWhenAbsent pins the 204
// branch: LegalIdentityEndpoints.cs:26-28's ternary answers NoContent, not
// an empty 200 body, when the customer has no identity.
func TestGetCustomersByIdLegalIdentity_ReturnsNoContentWhenAbsent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "No Identity Co")

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Errorf("status %d body %s, want 204", r.Status, r.Body)
	}
	if len(r.Body) != 0 {
		t.Errorf("body = %s, want empty on 204", r.Body)
	}
}

// TestGetCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound
// pins LegalIdentityEndpoints.cs:21-24's 404.
func TestGetCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/999999/legal-identity", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPutCustomersByIdLegalIdentity_ReplacesIdentityNormalizesAndRecordsEvent
// pins LegalIdentityEndpoints.Upsert's success path end to end: the 200
// response is normalized (lowercased country/type/id/source, name case
// kept, exactly validateLegalIdentity's rules), the row is persisted with
// those normalized values, and one customer.updated event is recorded
// because the identity changed from none to something.
func TestPutCustomersByIdLegalIdentity_ReplacesIdentityNormalizesAndRecordsEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Replaceable")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "NO", "type": "Business", "id": "923609016", "name": "Replaceable AS", "source": "BRREG",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var identity legalIdentityJSON
	r.JSON(&identity)
	want := legalIdentityJSON{Country: "no", Type: "business", Id: "923609016", Name: "Replaceable AS", Source: "brreg"}
	if identity != want {
		t.Errorf("identity = %+v, want %+v", identity, want)
	}

	fetched := fetchCustomerJSON(t, c, created.Id)
	if fetched.Identity == nil || fetched.Identity.Country != "no" || fetched.Identity.Type != "business" || fetched.Identity.Id != "923609016" {
		t.Errorf("Identity = %+v, want the normalized identity", fetched.Identity)
	}

	if got := countTimelineEvents(t, h, created.Id, "customer.updated"); got != 1 {
		t.Errorf("customer.updated events = %d, want 1", got)
	}
}

// TestPutCustomersByIdLegalIdentity_WithBlankFields_ReturnsExactValidationMessages
// asserts .NET's exact value-object message text for every field, byte for
// byte, keyed bare ("country", not "identity.country" — this operation's
// body *is* the identity, unlike PostCustomers/PutCustomersById's nested
// one), under the title "Invalid legal identity"
// (LegalIdentityEndpoints.cs:38-41).
func TestPutCustomersByIdLegalIdentity_WithBlankFields_ReturnsExactValidationMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Validation Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "", "type": "", "id": "", "name": "", "source": "",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}

	var problem struct {
		Title  string              `json:"title"`
		Errors map[string][]string `json:"errors"`
	}
	r.JSON(&problem)
	if problem.Title != "Invalid legal identity" {
		t.Errorf("title = %q, want %q", problem.Title, "Invalid legal identity")
	}
	want := map[string][]string{
		"country": {"A country code cannot be null or empty"},
		"type":    {"A legal type cannot be null or empty"},
		"id":      {"A legal id cannot be null or empty"},
		"name":    {"A legal name cannot be null or empty"},
		"source":  {"A legal source cannot be null or empty"},
	}
	for field, msgs := range want {
		if got := problem.Errors[field]; len(got) != 1 || got[0] != msgs[0] {
			t.Errorf("errors[%q] = %v, want %v", field, got, msgs)
		}
	}
}

// TestPutCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound
// pins LegalIdentityEndpoints.cs:43-47's 404, which runs only once the body
// has already validated (legal_identity.go validates the identity before it
// looks the customer up).
//
// There *is* a field-validation-versus-existence ordering question here, and
// it is observable: an invalid body aimed at a missing customer answers 400
// today and would answer 404 if the two steps were swapped. It is pinned by
// TestPutCustomersByIdLegalIdentity_InvalidBodyAgainstMissingCustomer_Returns400
// below. (An earlier version of this comment claimed there was no such
// question, because the identity is the whole body rather than a
// conditionally-present sub-object. That explains why this operation's error
// keys are bare — "country", not "identity.country" — not why the ordering
// would be unobservable.)
func TestPutCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999/legal-identity", map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "Ghost AS", "source": "manual",
	})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPutCustomersByIdLegalIdentity_InvalidBodyAgainstMissingCustomer_Returns400
// pins the ordering the comment above describes, the half the 404 test cannot
// see: validation runs first, so a blank-field identity aimed at a
// nonexistent customer answers 400 and not the 404 a swapped order would
// produce.
func TestPutCustomersByIdLegalIdentity_InvalidBodyAgainstMissingCustomer_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999/legal-identity", map[string]any{
		"country": "", "type": "business", "id": "923609016", "name": "Ghost AS", "source": "manual",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation must run before the existence check)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["country"]; !ok {
		t.Errorf("errors = %v, want a key \"country\"", problem.Errors)
	}
}

// TestPutCustomersByIdLegalIdentity_ResubmittingUnchangedIdentity_IsANoOp
// pins the before/after comparison LegalIdentityEndpoints.Upsert makes
// (:49-56, identityEqual/timeline_events.go): resubmitting the identity
// already on the row records no second event and never bumps updated_at —
// the same "semantically unchanged" suppression TimelineEndpointsTests.cs
// pins for UpdateCustomerEndpoint (customers inventory §2.4), applied here
// to the dedicated endpoint. The harness clock is advanced between the two
// PUTs so an accidental updated_at bump cannot hide behind an unmoved clock.
func TestPutCustomersByIdLegalIdentity_ResubmittingUnchangedIdentity_IsANoOp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Idempotent Co")

	identity := map[string]any{"country": "no", "type": "business", "id": "923609016", "name": "Idempotent AS", "source": "manual"}
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), identity)
	if r.Status != http.StatusOK {
		t.Fatalf("seed PUT: status %d body %s", r.Status, r.Body)
	}
	before := fetchCustomerJSON(t, c, created.Id)
	if got := countTimelineEvents(t, h, created.Id, "customer.updated"); got != 1 {
		t.Fatalf("customer.updated events = %d, want 1 after the seeding PUT", got)
	}

	h.Advance(time.Hour)

	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), identity)
	if r.Status != http.StatusOK {
		t.Fatalf("resubmit PUT: status %d body %s", r.Status, r.Body)
	}
	after := fetchCustomerJSON(t, c, created.Id)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged at %v (resubmitting the same identity must be a no-op)", after.UpdatedAt, before.UpdatedAt)
	}
	if got := countTimelineEvents(t, h, created.Id, "customer.updated"); got != 1 {
		t.Errorf("customer.updated events = %d, want still 1 (no event for an unchanged resubmission)", got)
	}
}

// TestDeleteCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound
// pins LegalIdentityEndpoints.cs:67-70's 404.
func TestDeleteCustomersByIdLegalIdentity_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodDelete, "/api/v1/customers/999999/legal-identity", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestDeleteCustomersByIdLegalIdentity_WhenAlreadyAbsent_IsIdempotent pins
// LegalIdentityEndpoints.cs:73-81's idempotence: a customer with no
// identity answers 204 with no write and no timeline event, mirroring
// DeleteCustomersById's archival idempotence (customers inventory §4).
func TestDeleteCustomersByIdLegalIdentity_WhenAlreadyAbsent_IsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Never Had One")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if got := countTimelineEvents(t, h, created.Id, "customer.updated"); got != 0 {
		t.Errorf("customer.updated events = %d, want 0 (a customer with no identity was already at rest)", got)
	}
}
