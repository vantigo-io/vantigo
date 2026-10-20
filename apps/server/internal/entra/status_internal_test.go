package entra

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// TestStatusOf pins the status and the reason Provider and GET /connection
// answer, by the row and the secrets' dates (plan task 3, status.go).
func TestStatusOf(t *testing.T) {
	now := time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)
	tenant, client := uuid.New(), uuid.New()
	str := func(s string) *string { return &s }
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	secret := func(expires time.Duration, revoked bool) store.EntraCredential {
		c := store.EntraCredential{ID: 1, ExpiresAt: now.Add(expires), CreatedAt: now}
		if revoked {
			c.RevokedAt = at(-time.Hour)
		}
		return c
	}
	registered := func(status string, reason *string, streak int32) store.EntraConnection {
		return store.EntraConnection{TenantID: &tenant, ClientID: &client, Status: status, StatusReason: reason, SensitiveDeniedStreak: streak}
	}
	const year = 365 * 24 * time.Hour
	for _, c := range []struct {
		name         string
		conn         store.EntraConnection
		creds        []store.EntraCredential
		status, want string
	}{
		{"no tenant", store.EntraConnection{ClientID: &client, Status: "connected"}, []store.EntraCredential{secret(year, false)}, "unconfigured", ""},
		{"no secret", registered("connected", nil, 0), nil, "unconfigured", ""},
		{"every secret revoked", registered("connected", nil, 0), []store.EntraCredential{secret(year, true)}, "unconfigured", ""},
		{"never tested", registered("unconfigured", nil, 0), []store.EntraCredential{secret(year, false)}, "unconfigured", ""},
		{"every secret expired", registered("connected", nil, 0), []store.EntraCredential{secret(-time.Hour, false), secret(year, true)}, "failing", "secret_expired"},
		{"connected", registered("connected", nil, 0), []store.EntraCredential{secret(year, false)}, "connected", ""},
		{"expiring within 30 days", registered("connected", nil, 0), []store.EntraCredential{secret(29*24*time.Hour, false)}, "connected", "secret_expiring"},
		{"a later secret ends later", registered("connected", nil, 0), []store.EntraCredential{secret(29*24*time.Hour, false), secret(year, false)}, "connected", ""},
		{"three sensitive refusals", registered("connected", nil, 3), []store.EntraCredential{secret(year, false)}, "connected", "role_missing"},
		{"two sensitive refusals", registered("connected", nil, 2), []store.EntraCredential{secret(year, false)}, "connected", ""},
		{"failing keeps its reason", registered("failing", str("credential_invalid"), 3), []store.EntraCredential{secret(29*24*time.Hour, false)}, "failing", "credential_invalid"},
		{"a reason outside the bound is dropped", registered("failing", str("Not A Code"), 0), []store.EntraCredential{secret(year, false)}, "failing", ""},
	} {
		status, reason := statusOf(c.conn, c.creds, now)
		if status != c.status || reason != c.want {
			t.Errorf("%s: %s/%s, want %s/%s", c.name, status, reason, c.status, c.want)
		}
		if reason != "" && !reasonPattern.MatchString(reason) {
			t.Errorf("%s: reason %q outside Staff's bound", c.name, reason)
		}
	}
	if contracts.ProviderConnected != "connected" || contracts.ProviderFailing != "failing" {
		t.Fatal("the contract's statuses moved")
	}
}

// TestUsableCredentials pins the order secrets are tried in: unrevoked and
// unexpired only, newest first, a secret refused since it last worked after
// every other.
func TestUsableCredentials(t *testing.T) {
	now := time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	rows := []store.EntraCredential{
		{ID: 1005, CreatedAt: now.Add(-1 * time.Hour), ExpiresAt: now.Add(time.Hour), LastFailedAt: at(-time.Minute)},
		{ID: 1004, CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour), RevokedAt: at(-time.Minute)},
		{ID: 1003, CreatedAt: now.Add(-3 * time.Hour), ExpiresAt: now.Add(-time.Minute)},
		{ID: 1002, CreatedAt: now.Add(-4 * time.Hour), ExpiresAt: now.Add(time.Hour), LastFailedAt: at(-time.Hour), LastUsedAt: at(-time.Minute)},
		{ID: 1001, CreatedAt: now.Add(-5 * time.Hour), ExpiresAt: now.Add(time.Hour)},
	}
	var got []int64
	for _, r := range usableCredentials(rows, now) {
		got = append(got, r.ID)
	}
	if len(got) != 3 || got[0] != 1002 || got[1] != 1001 || got[2] != 1005 {
		t.Errorf("order = %v, want [1002 1001 1005]", got)
	}
}
