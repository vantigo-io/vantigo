package entra_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestAccess_EveryOperation proves each operation is mounted under its
// access rule, with OWNERS_REQUIRE_MFA on: 401 without a session, 403 for a
// caller holding nothing; the Owner's operations (the registration, the
// secrets, the test, the directory's two reads) 403 for entra:manage alone and for an Owner whose
// session verified no second factor — the Owner policy's MFA requirement
// refuses as a 403, as identity's own Owner endpoints do — and reached by
// an Owner with MFA; entra:manage's operations reached by entra:manage.
func TestAccess_EveryOperation(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t, modtest.WithEnv("OWNERS_REQUIRE_MFA", "1"))
	future := h.Now().AddDate(0, 6, 0).Format(time.RFC3339)
	for _, op := range []struct {
		method, path string
		body         any
		opts         []modtest.RequestOption
		ownerOnly    bool
	}{
		{http.MethodGet, connectionPath, nil, nil, false},
		{http.MethodPut, connectionPath, map[string]any{"tenantId": tenantID, "clientId": clientID}, []modtest.RequestOption{ifMatch(1)}, true},
		{http.MethodPost, credentialsPath, map[string]any{"kind": "client_secret", "secret": secretValue, "expiresAt": future}, nil, true},
		{http.MethodDelete, credentialsPath + "/1001", nil, nil, true},
		{http.MethodPost, testPath, nil, nil, true},
		{http.MethodPut, settingsPath, map[string]any{"upnPattern": "g.family"}, []modtest.RequestOption{ifMatch(1)}, false},
		{http.MethodGet, skusPath, nil, nil, false},
		{http.MethodGet, eventsPath + "?limit=10", nil, nil, false},
		{http.MethodGet, accountsPath, nil, nil, false},
		{http.MethodGet, groupsPath, nil, nil, false},
		{http.MethodPost, accountsPath + "/" + uuid.NewString() + "/confirm", nil, nil, false},
		{http.MethodGet, directoryPath + "?q=kari", nil, nil, true},
		{http.MethodGet, matchesPath, nil, nil, true},
	} {
		name := op.method + " " + op.path
		if r := h.Client(t).Do(op.method, op.path, op.body, op.opts...); r.Status != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", name, r.Status)
		}
		if r := h.SignIn(t).Do(op.method, op.path, op.body, op.opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s holding nothing: %d, want 403", name, r.Status)
		}
		m, _ := manager(t, h)
		withoutMFA, _ := owner(t, h)
		r := m.Do(op.method, op.path, op.body, op.opts...)
		if !op.ownerOnly {
			if r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
				t.Errorf("%s with entra:manage: %d, want it reached", name, r.Status)
			}
			continue
		}
		if r.Status != http.StatusForbidden {
			t.Errorf("%s with entra:manage alone: %d, want 403 (the Owner's)", name, r.Status)
		}
		if r := withoutMFA.Do(op.method, op.path, op.body, op.opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s by an Owner without MFA: %d, want 403", name, r.Status)
		}
		if r := ownerWithMFA(t, h).Do(op.method, op.path, op.body, op.opts...); r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
			t.Errorf("%s by an Owner with MFA: %d, want it reached", name, r.Status)
		}
	}
}

// TestAccess_OutlookOperations proves the person's Outlook operations are
// under the session rule — 401 without a session, reached by a caller
// holding nothing — and the callback anonymous (Outlook connection design
// M4).
func TestAccess_OutlookOperations(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/entra/outlook/connect", map[string]any{"feature": "calendar"}},
		{http.MethodPost, "/api/v1/entra/outlook/complete", nil},
		{http.MethodGet, "/api/v1/entra/outlook/connection", nil},
		{http.MethodDelete, "/api/v1/entra/outlook/connection", nil},
	} {
		name := op.method + " " + op.path
		if r := h.Client(t).Do(op.method, op.path, op.body); r.Status != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", name, r.Status)
		}
		if r := h.SignIn(t).Do(op.method, op.path, op.body); r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
			t.Errorf("%s holding nothing: %d, want it reached", name, r.Status)
		}
	}
	if r := h.Client(t).Do(http.MethodGet, "/api/v1/entra/outlook/callback?state=x&code=y", nil); r.Status != http.StatusFound {
		t.Errorf("the callback without a session: %d, want 302", r.Status)
	}
}

// TestAccess_OutlookLimits proves the buckets: connect and complete share
// entra-outlook's 30 a minute, the callback has entra-outlook-callback's
// 60 of its own.
func TestAccess_OutlookLimits(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	c := h.SignIn(t)
	for i := range 30 {
		path := "/api/v1/entra/outlook/connect"
		var body any = map[string]any{"feature": "calendar"}
		if i%2 == 1 {
			path, body = "/api/v1/entra/outlook/complete", nil
		}
		if r := c.Do(http.MethodPost, path, body); r.Status == http.StatusTooManyRequests {
			t.Fatalf("request %d: 429 within the limit", i+1)
		}
	}
	if r := c.Do(http.MethodPost, "/api/v1/entra/outlook/complete", nil); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 31st connect or complete: %d, want 429", r.Status)
	}
	for i := range 60 {
		if r := c.Do(http.MethodGet, "/api/v1/entra/outlook/callback", nil); r.Status != http.StatusFound {
			t.Fatalf("callback %d: %d, want 302 in its own bucket", i+1, r.Status)
		}
	}
	if r := c.Do(http.MethodGet, "/api/v1/entra/outlook/callback", nil); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 61st callback: %d, want 429", r.Status)
	}
	if r := c.Do(http.MethodGet, "/api/v1/entra/outlook/connection", nil); r.Status != http.StatusOK {
		t.Errorf("the person's read after both limits: %d, want 200 (unlimited)", r.Status)
	}
}
