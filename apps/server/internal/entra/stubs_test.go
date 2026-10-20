package entra_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The tests in this file are the skeleton's (plan task 2): each proves one
// operation is mounted under its access rule — 401 without a session, 403
// for a caller the rule does not admit — and reaches its handler, which
// answers 501 until task 3 builds it. Task 3 replaces this file with the
// operations' own tests.

// stubCase is one operation: how to call it, and who its rule admits.
type stubCase struct {
	method, path string
	body         any
	opts         []modtest.RequestOption
	// ownerOnly is an operation under policy:Owner+OwnerManagement, which
	// entra:manage alone does not reach.
	ownerOnly bool
}

// checkStub signs in the callers the case needs and checks the three
// answers. The 501 is off the contract — no operation declares it — so that
// one exchange skips validation; the 401 and the 403 are validated.
func checkStub(t *testing.T, c stubCase) {
	t.Helper()
	h := newHarness(t)

	anonymous := h.Client(t)
	if r := anonymous.Do(c.method, c.path, c.body, c.opts...); r.Status != http.StatusUnauthorized {
		t.Errorf("%s %s without a session: status %d body %s, want 401", c.method, c.path, r.Status, r.Body)
	}

	nobody := h.SignIn(t)
	if r := nobody.Do(c.method, c.path, c.body, c.opts...); r.Status != http.StatusForbidden {
		t.Errorf("%s %s without entra:manage: status %d body %s, want 403", c.method, c.path, r.Status, r.Body)
	}

	allowed, _ := manager(t, h)
	if c.ownerOnly {
		if r := allowed.Do(c.method, c.path, c.body, c.opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s %s with entra:manage alone: status %d body %s, want 403 (the Owner's)", c.method, c.path, r.Status, r.Body)
		}
		allowed, _ = owner(t, h)
	}
	stub := append(slices.Clone(c.opts), modtest.SkipContract("task 2's stub answers 501, which no operation declares"))
	if r := allowed.Do(c.method, c.path, c.body, stub...); r.Status != http.StatusNotImplemented {
		t.Errorf("%s %s by an admitted caller: status %d body %s, want the stub's 501", c.method, c.path, r.Status, r.Body)
	}
}

// ifMatch is the If-Match header carrying version.
func ifMatch(version int) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(version))
}

func TestStub_GetConnection(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodGet, path: connectionPath})
}

func TestStub_PutConnection(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{
		method: http.MethodPut, path: connectionPath, ownerOnly: true,
		body: map[string]any{"tenantId": "6d5f3f0e-1d2c-4b3a-9f8e-7a6b5c4d3e2f", "clientId": "0f1e2d3c-4b5a-4968-8776-655443322110"},
		opts: []modtest.RequestOption{ifMatch(1)},
	})
}

func TestStub_PostCredentials(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{
		method: http.MethodPost, path: credentialsPath, ownerOnly: true,
		body: map[string]any{"kind": "client_secret", "label": "First", "secret": "s3cret-value",
			"expiresAt": time.Now().Add(180 * 24 * time.Hour).UTC().Format(time.RFC3339)},
	})
}

func TestStub_DeleteCredential(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodDelete, path: credentialsPath + "/1001", ownerOnly: true})
}

func TestStub_PostTest(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodPost, path: testPath, ownerOnly: true})
}

func TestStub_PutSettings(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{
		method: http.MethodPut, path: settingsPath,
		body: map[string]any{"upnPattern": "g.family"},
		opts: []modtest.RequestOption{ifMatch(1)},
	})
}

func TestStub_GetSkus(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodGet, path: skusPath})
}

func TestStub_GetEvents(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodGet, path: eventsPath + "?limit=10"})
}

func TestStub_GetAccounts(t *testing.T) {
	t.Parallel()
	checkStub(t, stubCase{method: http.MethodGet, path: accountsPath})
}
