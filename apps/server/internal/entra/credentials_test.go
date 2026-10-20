package entra_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The client secrets (plan task 3, credentials.go): the Owner's step 2, the
// order they are tried in, the connection failing when none works, the
// revoke and its refusal, and the value never answered, logged or audited.

func TestCredentials_AddValidatesEveryField(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, _ := owner(t, h)
	future := h.Now().AddDate(0, 6, 0).Format(time.RFC3339)
	for _, c := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a kind that is not client_secret", map[string]any{"kind": "certificate", "secret": "s", "expiresAt": future}, "kind"},
		{"a label over 100 characters", map[string]any{"kind": "client_secret", "label": strings.Repeat("l", 101), "secret": "s", "expiresAt": future}, "label"},
		{"an empty secret", map[string]any{"kind": "client_secret", "secret": "  ", "expiresAt": future}, "secret"},
		{"a secret over 1000 characters", map[string]any{"kind": "client_secret", "secret": strings.Repeat("s", 1001), "expiresAt": future}, "secret"},
		{"an expiry in the past", map[string]any{"kind": "client_secret", "secret": "s", "expiresAt": h.Now().Add(-time.Minute).Format(time.RFC3339)}, "expiresAt"},
		{"an expiry over 24 months ahead", map[string]any{"kind": "client_secret", "secret": "s", "expiresAt": h.Now().AddDate(2, 0, 1).Format(time.RFC3339)}, "expiresAt"},
	} {
		r := o.Do(http.MethodPost, credentialsPath, c.body)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", c.name, r.Status, r.Body)
			continue
		}
		if p := problemOf(t, r); p.Errors == nil || len((*p.Errors)[c.field]) == 0 {
			t.Errorf("%s: %s, want a field error on %s", c.name, r.Body, c.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.credentials`); n != 0 {
		t.Errorf("%d secrets stored by refused requests", n)
	}

	// The boundaries pass: a label of 100, a secret of 1000, 24 months.
	long := strings.Repeat("x", 1000)
	cred := addSecret(t, o, long, strings.Repeat("l", 100), h.Now().AddDate(2, 0, 0))
	if cred.Kind != gen.EntraCredentialKindClientSecret || !cred.InUse || cred.Label == nil || len(*cred.Label) != 100 || cred.RevokedAt != nil {
		t.Errorf("the secret answered %+v", cred)
	}
}

func TestCredentials_AddSealsTheSecretAndAuditsTheLabelAndDates(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, ownerID := owner(t, h)
	expires := h.Now().AddDate(1, 0, 0).Truncate(time.Second)
	r := o.Do(http.MethodPost, credentialsPath, map[string]any{
		"kind": "client_secret", "label": "Vantigo 2026", "secret": secretValue, "expiresAt": expires.Format(time.RFC3339),
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), secretValue) || strings.Contains(string(r.Body), `"secret"`) {
		t.Errorf("the answer carries the secret: %s", r.Body)
	}
	var cred gen.EntraCredential
	r.JSON(&cred)
	if !cred.ExpiresAt.Equal(expires) || !cred.CreatedAt.Equal(h.Now()) || *cred.Label != "Vantigo 2026" {
		t.Errorf("the secret answered %+v", cred)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.credentials WHERE position(convert_to($1, 'UTF8') in sealed) > 0 OR created_by_user_id <> $2`, secretValue, ownerID); n != 0 {
		t.Error("the secret is stored in the clear, or not as the Owner's")
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM entra.events WHERE action = 'entra.credential.added' AND actor_user_id = $1`, ownerID)
	want := []string{fmt.Sprintf("credential:%d", cred.Id), "expires_at:" + expires.UTC().Format(time.RFC3339), "label:Vantigo 2026"}
	if !slices.Equal(codes, want) {
		t.Errorf("the event's codes = %v, want %v", codes, want)
	}
}

func TestCredentials_TheNextSecretIsTriedAndTheRefusedOneMarked(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o, _ := owner(t, h)
	register(t, o)
	good := addSecret(t, o, secretValue, "Good", h.Now().AddDate(1, 0, 0))
	h.Advance(time.Minute)
	bad := addSecret(t, o, "a-secret-the-tenant-never-issued", "Bad", h.Now().AddDate(1, 0, 0))
	if conn := getConnection(t, o); !inUse(conn, bad.Id) {
		t.Errorf("before any token the newest secret is not the one in use: %+v", conn.Credentials)
	}

	if result := testConnection(t, o); result.Status != gen.EntraConnectionTestStatusConnected {
		t.Fatalf("the test: %+v, want connected through the older secret", result)
	}
	conn := getConnection(t, o)
	for _, c := range conn.Credentials {
		switch c.Id {
		case bad.Id:
			if c.LastFailedAt == nil || c.LastUsedAt != nil || c.InUse {
				t.Errorf("the refused secret: %+v, want it marked failed and not in use", c)
			}
		case good.Id:
			if c.LastUsedAt == nil || c.LastFailedAt != nil || !c.InUse {
				t.Errorf("the working secret: %+v, want it marked used and in use", c)
			}
		}
	}

	// The refused secret is tried after the working one from then on.
	before := tokenRequests(g)
	if result := testConnection(t, o); result.Status != gen.EntraConnectionTestStatusConnected {
		t.Fatalf("the second test: %+v", result)
	}
	if n := tokenRequests(g) - before; n != 1 {
		t.Errorf("the second test asked for %d tokens, want the working secret's alone", n)
	}
}

func TestCredentials_NoSecretThatWorksFailsTheConnection(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, _ := owner(t, h)
	register(t, o)
	addSecret(t, o, "a-secret-the-tenant-never-issued", "Bad", h.Now().AddDate(1, 0, 0))

	result := testConnection(t, o)
	if result.Status != gen.EntraConnectionTestStatusFailing || result.Reason == nil || *result.Reason != "credential_invalid" {
		t.Errorf("the test: %+v, want failing/credential_invalid", result)
	}
	if conn := getConnection(t, o); conn.Status != gen.EntraConnectionStatusFailing || conn.StatusReason == nil || *conn.StatusReason != "credential_invalid" {
		t.Errorf("the connection: %s/%v, want failing/credential_invalid", conn.Status, conn.StatusReason)
	}
	if info, _ := provider(h).Provider(context.Background()); info.Status != contracts.ProviderFailing || info.Reason != "credential_invalid" {
		t.Errorf("Provider() = %+v, want failing/credential_invalid", info)
	}
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "credential_invalid" {
		t.Errorf("GET /skus: %d %s, want 503 credential_invalid", r.Status, r.Body)
	}
}

func TestCredentials_AnExpiredSecretFailsTheConnectionAsSecretExpired(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, _ := owner(t, h)
	register(t, o)
	addSecret(t, o, secretValue, "Short", h.Now().Add(48*time.Hour))
	testConnection(t, o)
	if info, _ := provider(h).Provider(context.Background()); info.Status != contracts.ProviderConnected || info.Reason != "secret_expiring" {
		t.Errorf("two days from the end: Provider() = %+v, want connected/secret_expiring", info)
	}

	h.Advance(72 * time.Hour)
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "credential_invalid" {
		t.Errorf("GET /skus with no valid secret: %d %s, want 503 credential_invalid", r.Status, r.Body)
	}
	status := modtest.One[string](t, h.Harness, `SELECT status || '/' || coalesce(status_reason, '') FROM entra.connection`)
	if status != "failing/credential_invalid" {
		t.Errorf("the row = %s, want failing/credential_invalid", status)
	}
	if info, _ := provider(h).Provider(context.Background()); info.Status != contracts.ProviderFailing || info.Reason != "secret_expired" {
		t.Errorf("Provider() = %+v, want failing/secret_expired", info)
	}
	if conn := getConnection(t, m); conn.StatusReason == nil || *conn.StatusReason != "secret_expired" || inUse(conn, conn.Credentials[0].Id) {
		t.Errorf("GET /connection: %s/%v %+v, want failing/secret_expired and nothing in use", conn.Status, conn.StatusReason, conn.Credentials)
	}
}

func TestCredentials_RevokeAndTheOnlyValidSecret(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, ownerID := owner(t, h)
	register(t, o)
	first := addSecret(t, o, secretValue, "First", h.Now().AddDate(1, 0, 0))
	h.Advance(time.Minute)
	second := addSecret(t, o, "the-second-secret", "Second", h.Now().AddDate(1, 0, 0))
	addAccount(t, h, "E-1", "kari.nordmann@nordlys.no")

	r := o.Do(http.MethodDelete, fmt.Sprintf("%s/%d", credentialsPath, second.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("revoke the second of two: status %d body %s", r.Status, r.Body)
	}
	var revoked gen.EntraCredential
	r.JSON(&revoked)
	if revoked.RevokedAt == nil || revoked.InUse || revoked.Id != second.Id {
		t.Errorf("the revoked secret: %+v", revoked)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.credentials`); n != 2 {
		t.Errorf("%d secrets after a revoke, want both kept", n)
	}

	r = o.Do(http.MethodDelete, fmt.Sprintf("%s/%d", credentialsPath, first.Id), nil)
	if r.Status != http.StatusConflict || codeOf(t, r) != "credential_in_use" {
		t.Errorf("revoke the only valid secret while accounts exist: %d %s, want 409 credential_in_use", r.Status, r.Body)
	}
	if conn := getConnection(t, o); !inUse(conn, first.Id) || conn.Credentials[1].RevokedAt != nil {
		t.Errorf("the refused revoke changed the secrets: %+v", conn.Credentials)
	}

	// A revoked secret answers as it is; an unknown one is a bare 404.
	again := o.Do(http.MethodDelete, fmt.Sprintf("%s/%d", credentialsPath, second.Id), nil)
	var same gen.EntraCredential
	again.JSON(&same)
	if again.Status != http.StatusOK || !same.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Errorf("revoke again: %d %s", again.Status, again.Body)
	}
	if r := o.Do(http.MethodDelete, credentialsPath+"/999999", nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("an unknown secret: %d %q, want a bare 404", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.credential.revoked' AND actor_user_id = $1`, ownerID); n != 1 {
		t.Errorf("%d revoke events, want one", n)
	}

	// Without accounts the last secret may go, and the connection is
	// unconfigured again.
	h.Exec(t, `DELETE FROM entra.accounts`)
	if r := o.Do(http.MethodDelete, fmt.Sprintf("%s/%d", credentialsPath, first.Id), nil); r.Status != http.StatusOK {
		t.Errorf("revoke the last secret with no accounts: %d %s", r.Status, r.Body)
	}
	if conn := getConnection(t, o); conn.Status != gen.EntraConnectionStatusUnconfigured {
		t.Errorf("with every secret revoked the connection is %s, want unconfigured", conn.Status)
	}
}

func TestCredentials_ARevokedSecretIsNeverUsedAgain(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o := connected(t, h)
	conn := getConnection(t, o)
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusOK {
		t.Fatalf("GET /skus: %d %s", r.Status, r.Body)
	}
	if r := o.Do(http.MethodDelete, fmt.Sprintf("%s/%d", credentialsPath, conn.Credentials[0].Id), nil); r.Status != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	before := len(g.Calls())
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "credential_invalid" {
		t.Errorf("GET /skus after the revoke: %d %s, want 503 credential_invalid", r.Status, r.Body)
	}
	if n := len(g.Calls()) - before; n != 0 {
		t.Errorf("%d calls to the tenant with the only secret revoked", n)
	}
}

// TestCredentials_TheSecretIsNeverAnswered walks every step with the secret
// and greps it out of every answer, the log, the events and the row's
// sealed column.
func TestCredentials_TheSecretIsNeverAnswered(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o, _ := owner(t, h)
	m, _ := manager(t, h)
	var bodies []string
	keep := func(r *modtest.Response) { bodies = append(bodies, string(r.Body)) }

	register(t, o)
	keep(o.Do(http.MethodPost, credentialsPath, map[string]any{
		"kind": "client_secret", "label": "Vantigo", "secret": secretValue, "expiresAt": h.Now().AddDate(1, 0, 0).Format(time.RFC3339),
	}))
	keep(o.Do(http.MethodPost, credentialsPath, map[string]any{
		"kind": "client_secret", "secret": "x" + secretValue, "expiresAt": h.Now().AddDate(3, 0, 0).Format(time.RFC3339),
	}))
	keep(o.Do(http.MethodPost, testPath, nil))
	g.FailNext(http.StatusForbidden)
	keep(o.Do(http.MethodPost, testPath, nil))
	keep(o.Do(http.MethodGet, connectionPath, nil))
	keep(m.Do(http.MethodGet, skusPath, nil))
	keep(m.Do(http.MethodGet, eventsPath, nil))
	keep(m.Do(http.MethodGet, accountsPath, nil))
	keep(o.Do(http.MethodDelete, credentialsPath+"/1001", nil))

	for _, b := range bodies {
		if strings.Contains(b, secretValue) {
			t.Errorf("an answer carries the secret: %s", b)
		}
	}
	if strings.Contains(h.Logs(), secretValue) {
		t.Error("the log carries the secret")
	}
	if !strings.Contains(h.Logs(), "Graph refused a call") {
		t.Error("the 403 was not logged at debug; the grep above proves nothing")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE array_to_string(codes, ' ') LIKE '%' || $1 || '%'`, secretValue); n != 0 {
		t.Error("an event carries the secret")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.credentials WHERE position(convert_to($1, 'UTF8') in sealed) > 0`, secretValue); n != 0 {
		t.Error("the secret is stored in the clear")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.connection WHERE row_to_json(connection)::text LIKE '%' || $1 || '%'`, secretValue); n != 0 {
		t.Error("the connection's row carries the secret")
	}
}

// inUse reports whether the connection's secret id is the one in use, and
// the only one.
func inUse(conn gen.EntraConnection, id int64) bool {
	found := false
	for _, c := range conn.Credentials {
		if c.InUse {
			if c.Id != id || found {
				return false
			}
			found = true
		}
	}
	return found
}

// tokenRequests counts the fake's token requests.
func tokenRequests(g *graphtest.FakeGraph) int {
	n := 0
	for _, c := range g.Calls() {
		if c.Host == graphtest.TokenHost {
			n++
		}
	}
	return n
}
