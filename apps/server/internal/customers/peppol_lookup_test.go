package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is POST /customers/{id}/peppol-lookup (can-this-customer-
// receive-EHF design D3): asks the Peppol network whether a customer can
// receive an EHF invoice, remembers the answer on the billing profile
// (peppolLookup), records a timeline event when it changed, and feeds the
// two new billing-profile warnings (design D4). No .NET ancestor: Peppol
// lookup is new to this port. Every test drives modtest.WithPeppolLookup, a
// fake standing in for a *peppol.Client's own Lookup — no test here ever
// touches the network or real DNS (internal/peppol's own tests own that).

type peppolLookupJSON struct {
	Status               string    `json:"status"`
	CanReceiveInvoice    bool      `json:"canReceiveInvoice"`
	CanReceiveCreditNote bool      `json:"canReceiveCreditNote"`
	ParticipantId        *string   `json:"participantId"`
	SmpHost              *string   `json:"smpHost"`
	CheckedAt            time.Time `json:"checkedAt"`
}

func postPeppolLookup(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/peppol-lookup", id), nil)
}

// peppolLookupCalls is a concurrency-safe recorder of the participant
// identifiers a fake Deps.PeppolLookup was asked about, so a test that
// wants "zero calls" (no_identifier, disabled) or "exactly this participant"
// (explicit id wins over derived) has something to assert on.
type peppolLookupCalls struct {
	mu   sync.Mutex
	args []string
}

func (c *peppolLookupCalls) record(participant string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.args = append(c.args, participant)
}

func (c *peppolLookupCalls) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.args...)
}

// stubPeppolLookup is modtest.WithPeppolLookup's fake for a single canned
// outcome: every call is recorded on calls (nil to skip recording, for a
// test that doesn't care), then answers result, err.
func stubPeppolLookup(calls *peppolLookupCalls, result peppol.Result, err error) func(context.Context, string) (peppol.Result, error) {
	return func(_ context.Context, participant string) (peppol.Result, error) {
		if calls != nil {
			calls.record(participant)
		}
		return result, err
	}
}

// createNorwegianBusiness is duplicates_test.go's own createCustomerWithIdentity,
// fixed to country "no": the customer's legal identity then derives the
// Peppol participant "0192:"+orgNumber (billing_values.go's derivedPeppolID)
// — 923609016 and 974760673 are the module's two valid test organisation
// numbers (implementer context).
func createNorwegianBusiness(t *testing.T, c *modtest.Client, name, orgNumber string) createdCustomerJSON {
	t.Helper()
	return createCustomerWithIdentity(t, c, name, "no", orgNumber)
}

// TestPostCustomersByIdPeppolLookup_Registered_StoresAndReturnsBothCapabilities
// pins the plain success path: a registered participant with both
// capabilities is returned as-is, stored, withheld nothing (the caller
// holds legal-identity-view), and recorded on the timeline.
func TestPostCustomersByIdPeppolLookup_Registered_StoresAndReturnsBothCapabilities(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, SMPHost: "smp.example.test", CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c, userID := authenticatedClientWithID(t, h)
	created := createNorwegianBusiness(t, c, "Registered Both Co", "923609016")

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.Status != "registered" || !got.CanReceiveInvoice || !got.CanReceiveCreditNote {
		t.Errorf("lookup = %+v, want registered with both capabilities", got)
	}
	if got.ParticipantId == nil || *got.ParticipantId != "0192:923609016" {
		t.Errorf("ParticipantId = %v, want 0192:923609016 (caller holds legal-identity-view)", got.ParticipantId)
	}
	if got.SmpHost == nil || *got.SmpHost != "smp.example.test" {
		t.Errorf("SmpHost = %v, want smp.example.test", got.SmpHost)
	}
	if got.CheckedAt.Before(modtest.Start) {
		t.Errorf("CheckedAt = %v, want at or after harness Start", got.CheckedAt)
	}
	if want := []string{"0192:923609016"}; len(calls.all()) != 1 || calls.all()[0] != want[0] {
		t.Errorf("calls = %v, want %v", calls.all(), want)
	}

	// Remembered on the billing profile, revision untouched.
	profile := fetchBillingProfile(t, c, created.Id)
	if profile.Revision != 1 {
		t.Errorf("Revision = %d, want 1 (a lookup never bumps the customer row)", profile.Revision)
	}

	// Recorded on the timeline, attributed to the signed-in caller, no
	// participant id in the payload.
	entries := fetchPeppolLookupEvents(t, c, created.Id)
	if len(entries) != 1 {
		t.Fatalf("timeline events = %d, want 1", len(entries))
	}
	e := entries[0]
	if str(e.Summary) != "Peppol lookup: can receive EHF invoices" {
		t.Errorf("Summary = %q, want %q", str(e.Summary), "Peppol lookup: can receive EHF invoices")
	}
	if e.ActorKind != "user" || str(e.ActorDisplay) != userDisplayName(t, h, userID) {
		t.Errorf("actor = %s/%v, want user/%s", e.ActorKind, e.ActorDisplay, userDisplayName(t, h, userID))
	}
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := payload["participantId"]; ok {
		t.Errorf("payload = %v, must not carry participantId (timeline-view does not imply legal-identity-view)", payload)
	}
	if payload["status"] != "registered" || payload["canReceiveInvoice"] != true || payload["canReceiveCreditNote"] != true {
		t.Errorf("payload = %v, want status/canReceiveInvoice/canReceiveCreditNote to match", payload)
	}
}

// TestPostCustomersByIdPeppolLookup_CheckedAtCapturedAfterNetworkCall proves
// checkedAt is the clock read once the network call returns, not the clock
// read before it was made: the fake lookup advances the harness clock while
// it runs, and the returned checkedAt must reflect that — a network call
// that takes real time must not be timestamped as though it were
// instantaneous at the start.
func TestPostCustomersByIdPeppolLookup_CheckedAtCapturedAfterNetworkCall(t *testing.T) {
	t.Parallel()
	var h *modtest.Harness
	lookup := func(context.Context, string) (peppol.Result, error) {
		h.Advance(time.Hour)
		return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
	}
	h = newHarness(t, modtest.WithPeppolLookup(lookup))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Checked At After Network Co", "923609016")

	before := h.Now()
	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if !got.CheckedAt.After(before) {
		t.Errorf("CheckedAt = %v, want after %v — the lookup advanced the clock by an hour before returning", got.CheckedAt, before)
	}
}

// TestPostCustomersByIdPeppolLookup_RegisteredWithoutInvoiceCapability pins
// "registered, but not for invoices" — both the wire status/capabilities and
// its own distinct timeline summary.
func TestPostCustomersByIdPeppolLookup_RegisteredWithoutInvoiceCapability(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: false, CanReceiveCreditNote: false}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Registered No Invoice Co", "923609016")

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.Status != "registered" || got.CanReceiveInvoice || got.CanReceiveCreditNote {
		t.Errorf("lookup = %+v, want registered with neither capability", got)
	}

	entries := fetchPeppolLookupEvents(t, c, created.Id)
	if len(entries) != 1 || str(entries[0].Summary) != "Peppol lookup: registered, but not for invoices" {
		t.Fatalf("entries = %+v, want one \"Peppol lookup: registered, but not for invoices\"", entries)
	}
}

// TestPostCustomersByIdPeppolLookup_NotRegistered pins the third outcome.
func TestPostCustomersByIdPeppolLookup_NotRegistered(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil, peppol.Result{Registered: false}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Not Registered Co", "923609016")

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.Status != "not_registered" || got.CanReceiveInvoice || got.CanReceiveCreditNote {
		t.Errorf("lookup = %+v, want not_registered with both capabilities false", got)
	}

	entries := fetchPeppolLookupEvents(t, c, created.Id)
	if len(entries) != 1 || str(entries[0].Summary) != "Peppol lookup: not registered" {
		t.Fatalf("entries = %+v, want one \"Peppol lookup: not registered\"", entries)
	}
}

// TestPostCustomersByIdPeppolLookup_NoIdentifier_MakesNoNetworkCallAndStoresNothing
// pins design D3's third response outcome: no explicit peppolId, no
// derivable Norwegian business identity, so there is nothing to look up.
func TestPostCustomersByIdPeppolLookup_NoIdentifier_MakesNoNetworkCallAndStoresNothing(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "No Identifier Co")

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.Status != "no_identifier" || got.CanReceiveInvoice || got.CanReceiveCreditNote {
		t.Errorf("lookup = %+v, want no_identifier with both capabilities false", got)
	}
	if got.ParticipantId != nil || got.SmpHost != nil {
		t.Errorf("lookup = %+v, want no participantId or smpHost", got)
	}
	if len(calls.all()) != 0 {
		t.Errorf("calls = %v, want none (nothing to look up)", calls.all())
	}

	profile := fetchBillingProfile(t, c, created.Id)
	if profile.PeppolLookup != nil {
		t.Errorf("PeppolLookup = %+v, want nil (nothing stored)", profile.PeppolLookup)
	}
	if entries := fetchPeppolLookupEvents(t, c, created.Id); len(entries) != 0 {
		t.Errorf("timeline events = %d, want 0", len(entries))
	}
}

// TestPostCustomersByIdPeppolLookup_ExplicitPeppolIdWinsOverDerived pins
// design D3's participant resolution order: an explicit peppolId on the
// billing profile always wins over a derivable one, even for a customer
// whose legal identity would derive a different, valid participant.
func TestPostCustomersByIdPeppolLookup_ExplicitPeppolIdWinsOverDerived(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: false}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Explicit Wins Co", "923609016")
	if r := putBillingProfile(t, c, created.Id, map[string]any{"peppolId": "0192:974760673"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile: status %d body %s", r.Status, r.Body)
	}

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if want := []string{"0192:974760673"}; len(calls.all()) != 1 || calls.all()[0] != want[0] {
		t.Errorf("calls = %v, want %v (explicit peppolId, not the derived 0192:923609016)", calls.all(), want)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.ParticipantId == nil || *got.ParticipantId != "0192:974760673" {
		t.Errorf("ParticipantId = %v, want 0192:974760673 (an explicit id is never withheld)", got.ParticipantId)
	}
}

// TestPostCustomersByIdPeppolLookup_UpstreamFailure_Returns502KeepsOldRow
// pins design D3's 502 boundary: a failed lookup never stores anything and
// never records an event, and a previous good answer stands unchanged.
func TestPostCustomersByIdPeppolLookup_UpstreamFailure_Returns502KeepsOldRow(t *testing.T) {
	t.Parallel()
	var fail bool
	var calls peppolLookupCalls
	lookup := func(_ context.Context, participant string) (peppol.Result, error) {
		calls.record(participant)
		if fail {
			return peppol.Result{}, errors.New("peppol: simulated network failure")
		}
		return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true, SMPHost: "smp.example.test"}, nil
	}
	h := newHarness(t, modtest.WithPeppolLookup(lookup))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Keeps Old Row Co", "923609016")

	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first lookup: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchBillingProfile(t, c, created.Id)
	if before.PeppolLookup == nil || before.PeppolLookup.Status != "registered" {
		t.Fatalf("before = %+v, want a stored registered answer", before.PeppolLookup)
	}

	fail = true
	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("status %d body %s, want 502", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Peppol lookup unavailable" || problemTitle(problem.Detail) != "The Peppol network could not be reached" {
		t.Errorf("problem = %+v, want title/detail pinned by design D3", problem)
	}

	after := fetchBillingProfile(t, c, created.Id)
	if after.PeppolLookup == nil || !reflect.DeepEqual(*after.PeppolLookup, *before.PeppolLookup) {
		t.Errorf("after = %+v, want unchanged from before %+v (a 502 stores nothing)", after.PeppolLookup, before.PeppolLookup)
	}
	if entries := fetchPeppolLookupEvents(t, c, created.Id); len(entries) != 1 {
		t.Errorf("timeline events = %d, want 1 (only the first, successful lookup)", len(entries))
	}
}

// TestPostCustomersByIdPeppolLookup_Disabled_Returns503AndNeverCallsSeam
// pins design D3/D5's controller ruling: PEPPOL_LOOKUP_ENABLED=0 answers
// 503 without ever consulting Deps.PeppolLookup, even though a fake is set.
func TestPostCustomersByIdPeppolLookup_Disabled_Returns503AndNeverCallsSeam(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t,
		modtest.WithEnv("PEPPOL_LOOKUP_ENABLED", "0"),
		modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Disabled Co", "923609016")

	r := postPeppolLookup(t, c, created.Id)
	if r.Status != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s, want 503", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Peppol lookup disabled" || problemTitle(problem.Detail) != "Peppol lookup is disabled on this installation" {
		t.Errorf("problem = %+v, want title/detail pinned by design D3", problem)
	}
	if len(calls.all()) != 0 {
		t.Errorf("calls = %v, want none — the seam must not be consulted while disabled", calls.all())
	}
}

// TestPostCustomersByIdPeppolLookup_UnknownCustomer_Returns404 pins the
// 404-before-503/participant ordering: an unknown customer answers 404 even
// though PEPPOL_LOOKUP_ENABLED never comes into it.
func TestPostCustomersByIdPeppolLookup_UnknownCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := postPeppolLookup(t, c, 999999)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPostCustomersByIdPeppolLookup_WithoutBillingManagePermission_ReturnsForbidden
// pins the access rule (customers:billing-manage+customers:view), shaped
// like billing_profile_test.go's own PUT permission test.
func TestPostCustomersByIdPeppolLookup_WithoutBillingManagePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "No Billing Manage Lookup Co")

	viewer := h.SignIn(t, "customers:view")
	r := postPeppolLookup(t, viewer, created.Id)
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestPostCustomersByIdPeppolLookup_StaleAfterOrgNumberChanges pins design
// D3's staleness rule: once the legal identity's organisation number
// changes, the previous answer's participant no longer matches the one that
// would be looked up now, so it is dropped from the response and the
// warnings — as though never checked — even though the row itself is left
// alone (the handler that changed the identity is not this one).
func TestPostCustomersByIdPeppolLookup_StaleAfterOrgNumberChanges(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Stale Org Number Co", "923609016")

	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("lookup: status %d body %s, want 200", r.Status, r.Body)
	}
	if profile := fetchBillingProfile(t, c, created.Id); profile.PeppolLookup == nil {
		t.Fatalf("PeppolLookup = nil, want the fresh answer before the identity changes")
	}

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "no", "type": "business", "id": "974760673", "name": "Stale Org Number Co", "source": "manual",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("put legal identity: status %d body %s", r.Status, r.Body)
	}

	profile := fetchBillingProfile(t, c, created.Id)
	if profile.PeppolLookup != nil {
		t.Errorf("PeppolLookup = %+v, want nil (stale: the org number changed)", profile.PeppolLookup)
	}
}

// TestPostCustomersByIdPeppolLookup_StaleAfterPeppolIdChanges is the same
// staleness rule for an explicit peppolId.
func TestPostCustomersByIdPeppolLookup_StaleAfterPeppolIdChanges(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Stale Peppol Id Co")
	if r := putBillingProfile(t, c, created.Id, map[string]any{"peppolId": "0192:923609016"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile: status %d body %s", r.Status, r.Body)
	}
	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("lookup: status %d body %s, want 200", r.Status, r.Body)
	}
	if profile := fetchBillingProfile(t, c, created.Id); profile.PeppolLookup == nil {
		t.Fatalf("PeppolLookup = nil, want the fresh answer before peppolId changes")
	}

	if r := putBillingProfile(t, c, created.Id, map[string]any{"peppolId": "0192:974760673"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile (new peppolId): status %d body %s", r.Status, r.Body)
	}

	profile := fetchBillingProfile(t, c, created.Id)
	if profile.PeppolLookup != nil {
		t.Errorf("PeppolLookup = %+v, want nil (stale: peppolId changed)", profile.PeppolLookup)
	}
}

// TestPostCustomersByIdPeppolLookup_WithholdsDerivedParticipantWithoutLegalIdentityView
// and its sibling below pin server.go's withholding rule: participantId is
// omitted from a *derived* answer for a caller lacking
// customers:legal-identity-view, in both the POST response and the stored
// answer GET .../billing-profile later returns — but never for an explicit
// peppolId, and never smpHost.
func TestPostCustomersByIdPeppolLookup_WithholdsDerivedParticipantWithoutLegalIdentityView(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true, SMPHost: "smp.example.test"}, nil)))
	owner := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, owner, "Withheld Derived Co", "923609016")

	limited := h.SignIn(t, "customers:view", "customers:billing-manage")
	r := postPeppolLookup(t, limited, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.ParticipantId != nil {
		t.Errorf("ParticipantId = %v, want nil (derived, caller lacks legal-identity-view)", got.ParticipantId)
	}
	if got.SmpHost == nil || *got.SmpHost != "smp.example.test" {
		t.Errorf("SmpHost = %v, want smp.example.test (smpHost is never withheld)", got.SmpHost)
	}

	profile := fetchBillingProfile(t, limited, created.Id)
	if profile.PeppolLookup == nil || profile.PeppolLookup.ParticipantId != nil {
		t.Errorf("GET .../billing-profile PeppolLookup = %+v, want participantId nil for the same caller", profile.PeppolLookup)
	}

	// The very same stored answer, read by a caller who does hold
	// legal-identity-view, shows the derived participant id.
	full := fetchBillingProfile(t, owner, created.Id)
	if full.PeppolLookup == nil || full.PeppolLookup.ParticipantId == nil || *full.PeppolLookup.ParticipantId != "0192:923609016" {
		t.Errorf("owner's PeppolLookup = %+v, want participantId 0192:923609016", full.PeppolLookup)
	}
}

// TestPostCustomersByIdPeppolLookup_NeverWithholdsExplicitParticipant pins
// the other half of the withholding rule: an explicit peppolId is already
// visible in the profile itself, so it is never withheld from the lookup
// response either.
func TestPostCustomersByIdPeppolLookup_NeverWithholdsExplicitParticipant(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil, peppol.Result{Registered: false}, nil)))
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "Explicit Never Withheld Co")
	if r := putBillingProfile(t, owner, created.Id, map[string]any{"peppolId": "0192:923609016"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile: status %d body %s", r.Status, r.Body)
	}

	limited := h.SignIn(t, "customers:view", "customers:billing-manage")
	r := postPeppolLookup(t, limited, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got peppolLookupJSON
	r.JSON(&got)
	if got.ParticipantId == nil || *got.ParticipantId != "0192:923609016" {
		t.Errorf("ParticipantId = %v, want 0192:923609016 (explicit, never withheld)", got.ParticipantId)
	}
}

// TestBillingProfile_EhfRecipientNotRegisteredWarning and
// TestBillingProfile_EhfAvailableWarning pin design D4's two new warnings,
// appended after the existing four.
func TestBillingProfile_EhfRecipientNotRegisteredWarning(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil, peppol.Result{Registered: false}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Ehf Recipient Not Registered Co", "923609016")
	if r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "ehf"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile: status %d body %s", r.Status, r.Body)
	}
	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("lookup: status %d body %s, want 200", r.Status, r.Body)
	}

	profile := fetchBillingProfile(t, c, created.Id)
	if !slices.Contains(profile.Warnings, "ehf_recipient_not_registered") {
		t.Errorf("warnings = %v, want ehf_recipient_not_registered", profile.Warnings)
	}
	if slices.Contains(profile.Warnings, "ehf_available") {
		t.Errorf("warnings = %v, want no ehf_available", profile.Warnings)
	}
	// Order: the existing four (only no_invoice_address applies here), then
	// the two new ones in order.
	if last := profile.Warnings[len(profile.Warnings)-1]; last != "ehf_recipient_not_registered" {
		t.Errorf("last warning = %q, want ehf_recipient_not_registered (appended after the existing four)", last)
	}
}

func TestBillingProfile_EhfAvailableWarning(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Ehf Available Co", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("lookup: status %d body %s, want 200", r.Status, r.Body)
	}

	profile := fetchBillingProfile(t, c, created.Id)
	if !slices.Contains(profile.Warnings, "ehf_available") {
		t.Errorf("warnings = %v, want ehf_available (registered, can receive invoices, delivery unset)", profile.Warnings)
	}
	if slices.Contains(profile.Warnings, "ehf_recipient_not_registered") {
		t.Errorf("warnings = %v, want no ehf_recipient_not_registered", profile.Warnings)
	}

	// Switching to ehf delivery (the "Use EHF" action) turns the offer off:
	// canReceiveInvoice is true, so ehf_recipient_not_registered does not
	// fire either.
	if r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "ehf"}); r.Status != http.StatusOK {
		t.Fatalf("put billing profile: status %d body %s", r.Status, r.Body)
	}
	after := fetchBillingProfile(t, c, created.Id)
	if slices.Contains(after.Warnings, "ehf_available") || slices.Contains(after.Warnings, "ehf_recipient_not_registered") {
		t.Errorf("warnings = %v, want neither new warning once ehf is in use and the customer can receive it", after.Warnings)
	}
}

// TestPostCustomersByIdPeppolLookup_UnchangedAnswer_RecordsNoSecondEvent and
// its sibling below pin design D3's "only when the status or either
// capability changed" rule: a first lookup always counts as changed; an
// identical re-check writes the row again (so checkedAt still moves) but no
// second event; a lookup whose answer differs from the stored one does.
func TestPostCustomersByIdPeppolLookup_UnchangedAnswer_RecordsNoSecondEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Unchanged Answer Co", "923609016")

	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first lookup: status %d body %s, want 200", r.Status, r.Body)
	}
	h.Advance(time.Hour)
	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("second lookup: status %d body %s, want 200", r.Status, r.Body)
	}

	if entries := fetchPeppolLookupEvents(t, c, created.Id); len(entries) != 1 {
		t.Errorf("timeline events = %d, want 1 (the re-check is quiet)", len(entries))
	}
	// checkedAt still moved, even though nothing was recorded.
	profile := fetchBillingProfile(t, c, created.Id)
	if profile.PeppolLookup == nil || !profile.PeppolLookup.CheckedAt.After(modtest.Start) {
		t.Errorf("PeppolLookup.CheckedAt = %v, want it to have moved past harness Start", profile.PeppolLookup)
	}
}

func TestPostCustomersByIdPeppolLookup_ChangedAnswer_RecordsSecondEvent(t *testing.T) {
	t.Parallel()
	var registered bool
	lookup := func(_ context.Context, _ string) (peppol.Result, error) {
		if registered {
			return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
		}
		return peppol.Result{Registered: false}, nil
	}
	h := newHarness(t, modtest.WithPeppolLookup(lookup))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Changed Answer Co", "923609016")

	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("first lookup: status %d body %s, want 200", r.Status, r.Body)
	}
	registered = true
	if r := postPeppolLookup(t, c, created.Id); r.Status != http.StatusOK {
		t.Fatalf("second lookup: status %d body %s, want 200", r.Status, r.Body)
	}

	entries := fetchPeppolLookupEvents(t, c, created.Id)
	if len(entries) != 2 {
		t.Fatalf("timeline events = %d, want 2 (not_registered, then registered)", len(entries))
	}
	var payload map[string]any
	if err := json.Unmarshal(entries[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["previousStatus"] != nil {
		t.Errorf("first event previousStatus = %v, want nil (first lookup ever)", payload["previousStatus"])
	}
	if err := json.Unmarshal(entries[1].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["previousStatus"] != "not_registered" {
		t.Errorf("second event previousStatus = %v, want not_registered", payload["previousStatus"])
	}
}

// fetchPeppolLookupEvents lists customer.peppol_lookup timeline entries for
// customerID, oldest first — GET .../timeline's own default order — through
// c, so a withholding test can also prove the event feed carries nothing a
// narrower caller couldn't already see.
func fetchPeppolLookupEvents(t *testing.T, c *modtest.Client, customerID int32) []timelineEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=customer.peppol_lookup", customerID), nil)
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
