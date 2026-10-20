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
// secrets, the test) 403 for entra:manage alone and for an Owner whose
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
		{http.MethodPost, accountsPath + "/" + uuid.NewString() + "/confirm", nil, nil, false},
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
