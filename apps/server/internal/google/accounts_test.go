package google_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The Accounts tab's operations (Google Workspace design G17, G20): the
// accounts and the groups from the module's rows, the account rules under
// If-Match, the licences, and who may call them.

func TestAccounts_TheListAnswersNoPassword(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	acc := l.prepare(t, joiner("E1042"))
	l.activate(t, acc.ExternalID, joiner("E1042"))
	m, _ := manager(t, l.h)
	r := m.Do(http.MethodGet, "/api/v1/google/accounts", nil)
	var accounts []gen.GoogleAccount
	r.JSON(&accounts)
	if r.Status != http.StatusOK || len(accounts) != 1 {
		t.Fatalf("GET /accounts: %d %s", r.Status, r.Body)
	}
	a := accounts[0]
	if a.UserId != acc.ExternalID || a.PrimaryEmail != "kari.nordmann@nordlys.no" || a.State != gen.Enabled ||
		!a.CreatedByUs || !a.PassWaiting || len(a.Licences) != 1 || a.Licences[0] != licenceStandard {
		t.Errorf("the account: %+v", a)
	}
	if pw := l.user(t, acc.ExternalID).Password; len(pw) == 0 || strings.Contains(string(r.Body), pw) {
		t.Error("the list answers the password")
	}
}

func TestSettings_TheAccountRulesUnderIfMatch(t *testing.T) {
	t.Parallel()
	l := newLifecycle(t)
	m, _ := manager(t, l.h)
	conn := getConnection(t, m)
	if conn.AddressPattern != "given.family" || conn.OrgUnitPath != "/" || conn.PassLifetimeDays != 7 || conn.PassDelivery != "hr" ||
		!conn.LeaverRemoveLicences || conn.LeaverRemoveGroups != "managed" || !conn.LeaverSignOut || conn.AddressDomain != nil {
		t.Fatalf("the defaults: %+v", conn)
	}
	body := map[string]any{
		"addressDomain": "Nordlys.example", "addressPattern": "g.family", "orgUnitPath": "/Ansatte/Nye", "passLifetimeDays": 14,
		"passDelivery": "both", "leaverRemoveLicences": false, "leaverRemoveGroups": "all", "leaverSignOut": false,
	}
	r := m.Do(http.MethodPut, "/api/v1/google/connection/settings", body, ifMatch(conn.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection/settings: %d %s", r.Status, r.Body)
	}
	var got gen.GoogleConnection
	r.JSON(&got)
	if got.AddressDomain == nil || *got.AddressDomain != "nordlys.example" || got.AddressPattern != "g.family" || got.OrgUnitPath != "/Ansatte/Nye" ||
		got.PassLifetimeDays != 14 || got.PassDelivery != "both" || got.LeaverRemoveLicences || got.LeaverRemoveGroups != "all" ||
		got.LeaverSignOut || got.Version != conn.Version+1 {
		t.Errorf("the rules set: %+v", got)
	}
	if ev := l.events(t, "google.settings.changed"); len(ev) != 1 || len(ev[0]) != 8 {
		t.Errorf("the event: %v", ev)
	}
	// The old version is refused; the same values again write nothing.
	if r := m.Do(http.MethodPut, "/api/v1/google/connection/settings", body, ifMatch(conn.Version)); r.Status != http.StatusPreconditionFailed || codeOf(t, r) != "version_mismatch" {
		t.Errorf("a stale If-Match: %d %s", r.Status, r.Body)
	}
	if r := m.Do(http.MethodPut, "/api/v1/google/connection/settings", body, ifMatch(got.Version)); r.Status != http.StatusOK {
		t.Errorf("the same values: %d", r.Status)
	} else if ev := l.events(t, "google.settings.changed"); len(ev) != 1 {
		t.Errorf("the same values wrote an event: %v", ev)
	}
	// Refusals, each a field error.
	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"a domain not verified":  {map[string]any{"addressDomain": "elsewhere.example"}, "addressDomain"},
		"not a domain":           {map[string]any{"addressDomain": "not a domain"}, "addressDomain"},
		"an organisational unit": {map[string]any{"orgUnitPath": "Ansatte"}, "orgUnitPath"},
		"a lifetime of 31 days":  {map[string]any{"passLifetimeDays": 31}, "passLifetimeDays"},
		"a pattern":              {map[string]any{"addressPattern": "family"}, "addressPattern"},
	} {
		r := m.Do(http.MethodPut, "/api/v1/google/connection/settings", tc.body, ifMatch(got.Version))
		p := problemOf(t, r)
		if r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)[tc.field]) == 0 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := m.Do(http.MethodPut, "/api/v1/google/connection/settings", body, modtest.Header("If-Match", "soon")); r.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is no version: %d", r.Status)
	}
}

func TestSkus_GoogleUnavailableIs503(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	m, _ := manager(t, h)
	// No delegation: no token for the licences.
	r := m.Do(http.MethodGet, "/api/v1/google/connection/skus", nil)
	if r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "credential_invalid" {
		t.Errorf("before delegation: %d %s", r.Status, r.Body)
	}
	h.g.Delegate(saClientID, everyScope...)
	testConnection(t, o)
	// The Licensing call fails, and so does its one retry. The failure is
	// put on the Licensing path, not on the next requests: the token cache
	// is the process's, and a parallel test whose clock is a day on forgets
	// this test's token, so a token fetch may come between the two.
	const licensing = "GET /apps/licensing/v1/product/Google-Apps/users"
	h.g.FailOn(licensing, http.StatusInternalServerError)
	before := len(h.g.Calls())
	r = m.Do(http.MethodGet, "/api/v1/google/connection/skus", nil)
	if r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "google_unreachable" {
		t.Errorf("Google failing: %d %s", r.Status, r.Body)
	}
	var failed int
	for _, c := range h.g.Calls()[before:] {
		if c.Host == googletest.LicensingHost && c.Status == http.StatusInternalServerError {
			failed++
		}
	}
	if failed != 2 {
		t.Errorf("the Licensing call was tried %d times, want twice", failed)
	}
}

func TestAccess_TheAccountsOperationsAreGoogleManages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	nobody := h.SignIn(t)
	for _, op := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/google/accounts"},
		{http.MethodPost, "/api/v1/google/accounts/1/confirm"},
		{http.MethodGet, "/api/v1/google/groups"},
		{http.MethodPost, "/api/v1/google/groups/abc/confirm"},
		{http.MethodGet, "/api/v1/google/connection/skus"},
		{http.MethodPut, "/api/v1/google/connection/settings"},
	} {
		var body any
		if op.method == http.MethodPut {
			body = map[string]any{}
		}
		if r := nobody.Do(op.method, op.path, body, ifMatch(1)); r.Status != http.StatusForbidden {
			t.Errorf("%s %s without google:manage: %d", op.method, op.path, r.Status)
		}
	}
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, "/api/v1/google/groups", nil); r.Status != http.StatusOK {
		t.Errorf("GET /groups with google:manage: %d", r.Status)
	}
}
