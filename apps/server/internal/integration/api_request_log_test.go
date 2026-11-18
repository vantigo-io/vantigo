package integration_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// The rate limit of an integration and the request log of its keys, through a
// business module as an external application meets them (API access design
// K23, K24).

func keyReasons(t *testing.T, h *modtest.Harness, integration uuid.UUID) []string {
	t.Helper()
	h.FlushAPIRequests()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT coalesce(reason, '') || ' ' || status FROM identity.api_requests WHERE integration_id = $1 ORDER BY 1`, integration)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestAPIRequestLog_ReadOnlyAndForbiddenAreRecorded: a write by a read-only
// key and a call its roles do not allow are in the log with their reasons,
// beside the admitted read.
func TestAPIRequestLog_ReadOnlyAndForbiddenAreRecorded(t *testing.T) {
	h := modtest.New(t, modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers")))
	c, integration := h.SignInAPIKey(t, modtest.APIKey{ReadOnly: true}, "customers:view", "customers:create")
	if r := c.Do(http.MethodGet, "/api/v1/customers", nil); r.Status != http.StatusOK {
		t.Fatalf("a read: %d %s", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}); r.Status != http.StatusForbidden {
		t.Fatalf("a read-only key's write: %d %s", r.Status, r.Body)
	}
	plain, other := h.SignInAPIKey(t, modtest.APIKey{})
	if r := plain.Do(http.MethodGet, "/api/v1/customers", nil); r.Status != http.StatusForbidden {
		t.Fatalf("a key without the permission: %d %s", r.Status, r.Body)
	}
	if got, want := keyReasons(t, h, integration), []string{" 200", "read_only 403"}; !slices.Equal(got, want) {
		t.Errorf("logged %q, want %q", got, want)
	}
	if got, want := keyReasons(t, h, other), []string{"forbidden 403"}; !slices.Equal(got, want) {
		t.Errorf("logged %q, want %q", got, want)
	}
}

// TestAPIRateLimit_CountsTheIntegrationAcrossItsKeys: the 601st request in a
// minute, across two keys of one integration, is 429 with the headers, and
// another integration is counted on its own.
func TestAPIRateLimit_CountsTheIntegrationAcrossItsKeys(t *testing.T) {
	h := modtest.New(t, modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers")))
	first, integration := h.SignInAPIKey(t, modtest.APIKey{}, "customers:view")
	secondKey, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
	second := h.APIKeyClient(t, secondKey)

	for i := range 600 {
		c := first
		if i%2 == 1 {
			c = second
		}
		r := c.Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("600 reads"))
		if r.Status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, r.Status, r.Body)
		}
		if i == 0 && (r.Header("RateLimit-Limit") != "600" || r.Header("RateLimit-Remaining") != "599" || r.Header("RateLimit-Reset") == "") {
			t.Errorf("the first answer's headers: %q %q %q", r.Header("RateLimit-Limit"), r.Header("RateLimit-Remaining"), r.Header("RateLimit-Reset"))
		}
	}
	r := second.Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("past the limit"))
	if r.Status != http.StatusTooManyRequests || r.Code() != "rate_limited" || r.Header("Retry-After") == "" ||
		r.Header("RateLimit-Remaining") != "0" || r.Header("RateLimit-Limit") != "600" {
		t.Errorf("the 601st: %d %s, headers Retry-After %q Remaining %q", r.Status, r.Body, r.Header("Retry-After"), r.Header("RateLimit-Remaining"))
	}

	other, _ := h.SignInAPIKey(t, modtest.APIKey{}, "customers:view")
	if r := other.Do(http.MethodGet, "/api/v1/customers", nil); r.Status != http.StatusOK || r.Header("RateLimit-Remaining") != "599" {
		t.Errorf("another integration: %d, remaining %q", r.Status, r.Header("RateLimit-Remaining"))
	}

	reasons := keyReasons(t, h, integration)
	if len(reasons) != 601 || reasons[0] != " 200" || !slices.Contains(reasons, "rate_limited 429") {
		t.Errorf("%d rows logged, want the 600 admitted and the one rate_limited", len(reasons))
	}
}
