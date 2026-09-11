package identity_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

const (
	maintenanceStatusPath = "/api/v1/identity/system/status"
	maintenanceUpdatePath = "/api/v1/identity/system/maintenance"
)

// maintenanceStatusBody is SystemMaintenanceStatus as a test reads it.
type maintenanceStatusBody struct {
	Maintenance bool    `json:"maintenance"`
	Message     *string `json:"message"`
}

// ownerSystemStatusBody is IdentitySystemStatusResponse as a test reads it.
type ownerSystemStatusBody struct {
	Total                             int        `json:"total"`
	Active                            int        `json:"active"`
	Disabled                          int        `json:"disabled"`
	PendingInvitations                int        `json:"pendingInvitations"`
	StaticOidcEnabled                 bool       `json:"staticOidcEnabled"`
	StaticOidcProvider                *string    `json:"staticOidcProvider"`
	StaticScimEnabled                 bool       `json:"staticScimEnabled"`
	LastStaticOidcSignInAtUtc         *time.Time `json:"lastStaticOidcSignInAtUtc"`
	LastAuthenticatedScimRequestAtUtc *time.Time `json:"lastAuthenticatedScimRequestAtUtc"`
}

// Ported from IdentityMaintenanceModeIntegrationTests.StatusIsAnonymousAndDefaultsToDisabled.
func TestMaintenance_StatusIsAnonymousAndDefaultsToDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.client(t).do(http.MethodGet, maintenanceStatusPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", maintenanceStatusPath, r.status, r.body)
	}
	var status maintenanceStatusBody
	r.json(&status)
	if status.Maintenance || status.Message != nil {
		t.Errorf("default status = %+v, want {false, nil}", status)
	}
}

// Ported from IdentityMaintenanceModeIntegrationTests.NonSystemAdminCannotUpdateMaintenanceMode.
// Extended: anonymous gets 401, and an Owner who is not also a SystemAdmin
// (SYSTEM_ADMIN_EMAIL unset, so bootstrapOwner grants only Owner) is refused
// too, since the contract's rule is policy:SystemAdmin, not Owner. Nothing
// any of the three requests sent changes the status.
func TestMaintenance_NonSystemAdminIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedUser(t, "user@example.test", userPassword, identity.RoleUserID)
	owner, _ := h.bootstrapOwner(t)
	body := map[string]any{"enabled": true, "message": "Denied"}

	if r := h.client(t).do(http.MethodPut, maintenanceUpdatePath, body); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("anonymous: status %d code %q, want 401 unauthenticated", r.status, r.code())
	}
	if r := h.login(t, "user@example.test", userPassword).do(http.MethodPut, maintenanceUpdatePath, body); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("standard user: status %d code %q, want 403 forbidden", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, maintenanceUpdatePath, body); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("Owner without SystemAdmin: status %d code %q, want 403 forbidden", r.status, r.code())
	}

	r := h.client(t).do(http.MethodGet, maintenanceStatusPath, nil)
	var status maintenanceStatusBody
	r.json(&status)
	if status.Maintenance || status.Message != nil {
		t.Errorf("status after three refusals = %+v, want unchanged {false, nil}", status)
	}
}

// Ported from IdentityMaintenanceModeIntegrationTests.SystemAdminCanEnableAndDisableMaintenanceMode.
// The .NET factory's Owner is also its configured SystemAdmin; here
// SYSTEM_ADMIN_EMAIL makes the bootstrap Owner one, as sessions_test.go's
// system-admin-revoke tests do.
func TestMaintenance_SystemAdminCanEnableAndDisable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SYSTEM_ADMIN_EMAIL", ownerEmail))
	administrator, _ := h.bootstrapOwner(t)

	r := administrator.do(http.MethodPut, maintenanceUpdatePath, map[string]any{"enabled": true, "message": "Scheduled maintenance"})
	var enabled maintenanceStatusBody
	r.json(&enabled)
	if r.status != http.StatusOK || !enabled.Maintenance || enabled.Message == nil || *enabled.Message != "Scheduled maintenance" {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	r = h.client(t).do(http.MethodGet, maintenanceStatusPath, nil)
	var status maintenanceStatusBody
	r.json(&status)
	if !status.Maintenance || status.Message == nil || *status.Message != "Scheduled maintenance" {
		t.Errorf("status after enable = %+v", status)
	}

	r = administrator.do(http.MethodPut, maintenanceUpdatePath, map[string]any{"enabled": false, "message": nil})
	var disabled maintenanceStatusBody
	r.json(&disabled)
	if r.status != http.StatusOK || disabled.Maintenance || disabled.Message != nil {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	r = h.client(t).do(http.MethodGet, maintenanceStatusPath, nil)
	r.json(&status)
	if status.Maintenance || status.Message != nil {
		t.Errorf("status after disable = %+v", status)
	}
}

// Ported from IdentityMaintenanceModeIntegrationTests.MessageOver500CharactersReturnsBadRequest.
// Extended: exactly 500 characters, counted in UTF-16 units as elsewhere in
// this package, is accepted; the refusal changes nothing.
func TestMaintenance_MessageLengthBoundary(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SYSTEM_ADMIN_EMAIL", ownerEmail))
	administrator, _ := h.bootstrapOwner(t)

	tooLong := strings.Repeat("x", 501)
	if r := administrator.do(http.MethodPut, maintenanceUpdatePath, map[string]any{"enabled": true, "message": tooLong}); r.status != http.StatusBadRequest || r.code() != "invalid_message" {
		t.Fatalf("501 characters: status %d code %q, want 400 invalid_message", r.status, r.code())
	}
	r := h.client(t).do(http.MethodGet, maintenanceStatusPath, nil)
	var status maintenanceStatusBody
	r.json(&status)
	if status.Maintenance {
		t.Errorf("the refusal changed the status: %+v", status)
	}

	atLimit := strings.Repeat("x", 500)
	r = administrator.do(http.MethodPut, maintenanceUpdatePath, map[string]any{"enabled": true, "message": atLimit})
	r.json(&status)
	if r.status != http.StatusOK || status.Message == nil || *status.Message != atLimit {
		t.Fatalf("500 characters: status %d body %s, want it accepted", r.status, r.body)
	}
}

// TestMaintenance_CacheServesStaleReadsForTwentySecondsThenRefreshesAndPutEvicts
// proves the 20 s in-process cache GetStatus reads through
// (EA/SystemMaintenanceEndpoints.cs:16-17,33-47): a direct database write is
// invisible until the cache is 20 s old, and a PUT evicts it at once instead
// of waiting out the TTL.
func TestMaintenance_CacheServesStaleReadsForTwentySecondsThenRefreshesAndPutEvicts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SYSTEM_ADMIN_EMAIL", ownerEmail))
	administrator, _ := h.bootstrapOwner(t)
	anonymous := h.client(t)

	// Prime the cache with maintenance off, at the harness clock's current now.
	if r := anonymous.do(http.MethodGet, maintenanceStatusPath, nil); r.status != http.StatusOK {
		t.Fatalf("prime: status %d", r.status)
	}

	// A write that bypasses the cache entirely, as a direct database change
	// (or another process) would.
	h.exec(t, `INSERT INTO identity.system_settings (id, maintenance_enabled, maintenance_message, updated_at)
	        VALUES (1, true, 'Direct write', $1)
	        ON CONFLICT (id) DO UPDATE SET maintenance_enabled = true, maintenance_message = 'Direct write', updated_at = $1`, h.now())

	h.advance(19 * time.Second)
	r := anonymous.do(http.MethodGet, maintenanceStatusPath, nil)
	var status maintenanceStatusBody
	r.json(&status)
	if status.Maintenance {
		t.Fatalf("stale read at 19s = %+v, want the primed {false, nil}", status)
	}

	h.advance(2 * time.Second) // 21s since the cache was filled: past the 20s TTL.
	r = anonymous.do(http.MethodGet, maintenanceStatusPath, nil)
	r.json(&status)
	if !status.Maintenance || status.Message == nil || *status.Message != "Direct write" {
		t.Errorf("fresh read at 21s = %+v, want the direct write", status)
	}

	if r := administrator.do(http.MethodPut, maintenanceUpdatePath, map[string]any{"enabled": false, "message": nil}); r.status != http.StatusOK {
		t.Fatalf("disable: status %d", r.status)
	}
	r = anonymous.do(http.MethodGet, maintenanceStatusPath, nil)
	r.json(&status)
	if status.Maintenance || status.Message != nil {
		t.Errorf("read right after PUT = %+v, want the eviction to show it at once", status)
	}
}

// Ported from IdentitySystemStatusIntegrationTests.SystemStatusRequiresOwnerAndRedactsSecretsWhileReturningCoherentCounts.
func TestSystemStatus_RequiresOwnerAndRedactsSecrets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if r := h.client(t).do(http.MethodGet, systemStatusPath, nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("anonymous: status %d code %q, want 401 unauthenticated", r.status, r.code())
	}
	h.seedUser(t, "user@example.test", userPassword, identity.RoleUserID)
	if r := h.login(t, "user@example.test", userPassword).do(http.MethodGet, systemStatusPath, nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("standard user: status %d code %q, want 403 forbidden", r.status, r.code())
	}

	owner, _ := h.bootstrapOwner(t)
	r := owner.do(http.MethodGet, systemStatusPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("Owner: status %d body %s", r.status, r.body)
	}
	var status ownerSystemStatusBody
	r.json(&status)
	if status.Total < 2 || status.Active < 0 || status.Active > status.Total-status.Disabled ||
		status.PendingInvitations < 0 || status.StaticOidcEnabled || status.StaticScimEnabled {
		t.Errorf("status = %+v", status)
	}
	if strings.Contains(string(r.body), bootstrapSecret) {
		t.Errorf("body leaked the bootstrap secret: %s", r.body)
	}
}

// Ported from StaticScimSystemStatusIntegrationTests.CountsExcludeUnavailableAccountsAndOnlyCountPendingInvitations.
// Extended: the deactivated Owner (the break-glass account) still counts as
// active, as ScimLifecycleService exempts Owner from SCIM deactivation and
// GetSessionByTokenHash applies the same exemption to a live session.
func TestSystemStatus_CountsExcludeUnavailableAccountsAndOnlyCountPendingInvitations(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SCIM_TOKEN", "identity-harness-scim-token"))
	owner, ownerID := h.bootstrapOwner(t)
	deactivate := func(id uuid.UUID) {
		h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		           VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), h.now())
	}
	h.seedUser(t, "available@example.test", userPassword, identity.RoleUserID)
	locked := h.seedUser(t, "locked@example.test", userPassword, identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, locked, h.now().Add(10*time.Minute))
	disabled := h.seedUser(t, "disabled@example.test", userPassword, identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	deactivate(h.seedUser(t, "scim-inactive@example.test", userPassword, identity.RoleUserID))
	deactivate(ownerID) // the break-glass Owner: still active despite upstream_active = false.

	insertInvitation := func(email string, revoked, accepted bool, createdAt, expiresAt time.Time) {
		var revokedAt, acceptedAt any
		if revoked {
			revokedAt = createdAt
		}
		if accepted {
			acceptedAt = createdAt
		}
		tokenHash := sha256.Sum256([]byte(email))
		h.exec(t, `INSERT INTO identity.invitations (id, email, normalized_email, role, token_hash, created_at, expires_at, revoked_at, accepted_at)
		        VALUES ($1, $2, upper($2), 'User', $3, $4, $5, $6, $7)`,
			uuid.New(), email, tokenHash[:], createdAt, expiresAt, revokedAt, acceptedAt)
	}
	insertInvitation("pending-status@example.test", false, false, h.now().Add(-time.Hour), h.now().Add(time.Hour))
	insertInvitation("revoked-status@example.test", true, false, h.now().Add(-time.Hour), h.now().Add(time.Hour))
	insertInvitation("accepted-status@example.test", false, true, h.now().Add(-time.Hour), h.now().Add(time.Hour))
	insertInvitation("expired-status@example.test", false, false, h.now().Add(-48*time.Hour), h.now().Add(-time.Hour))

	r := owner.do(http.MethodGet, systemStatusPath, nil)
	var status ownerSystemStatusBody
	r.json(&status)
	if r.status != http.StatusOK || status.Total != 5 || status.Active != 2 || status.Disabled != 1 || status.PendingInvitations != 1 {
		t.Fatalf("status = %+v, want {total:5 active:2 disabled:1 pendingInvitations:1}", status)
	}
	for _, email := range []string{"pending-status@example.test", "revoked-status@example.test", "expired-status@example.test"} {
		if strings.Contains(string(r.body), email) {
			t.Errorf("body leaked an invitation address %q: %s", email, r.body)
		}
	}
}

// Ported from StaticScimSystemStatusIntegrationTests.AuthenticatedScimIngressIsReportedBestEffortWithoutExposingToken.
// The heartbeat is written directly with the same query
// recordOperationalEvent uses, called twice to prove the one-row-per-kind
// upsert: only the later occurrence is reported.
// TestScim_TheHeartbeatRecordsAuthenticatedRequestsOnly asserts it end to
// end through the real SCIM endpoints.
func TestSystemStatus_ScimHeartbeatIsReportedWithoutExposingTheToken(t *testing.T) {
	t.Parallel()
	const token = "identity-harness-scim-token"
	h := newHarness(t, withEnv("SCIM_TOKEN", token))
	owner, _ := h.bootstrapOwner(t)
	q := store.New(h.pool)
	const kind = "scim.authenticated-request"
	first, second := h.now(), h.now().Add(5*time.Minute)
	if err := q.RecordOperationalEvent(context.Background(), store.RecordOperationalEventParams{Kind: kind, Now: first}); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if err := q.RecordOperationalEvent(context.Background(), store.RecordOperationalEventParams{Kind: kind, Now: second}); err != nil {
		t.Fatalf("record second: %v", err)
	}

	r := owner.do(http.MethodGet, systemStatusPath, nil)
	var status ownerSystemStatusBody
	r.json(&status)
	if r.status != http.StatusOK || !status.StaticScimEnabled || status.StaticOidcEnabled {
		t.Fatalf("status = %+v", status)
	}
	if status.LastAuthenticatedScimRequestAtUtc == nil || !status.LastAuthenticatedScimRequestAtUtc.Equal(second) {
		t.Errorf("lastAuthenticatedScimRequestAtUtc = %v, want the later occurrence %v", status.LastAuthenticatedScimRequestAtUtc, second)
	}
	if status.LastStaticOidcSignInAtUtc != nil {
		t.Errorf("lastStaticOidcSignInAtUtc = %v, want nil", status.LastStaticOidcSignInAtUtc)
	}
	if strings.Contains(string(r.body), token) {
		t.Errorf("body leaked the SCIM token: %s", r.body)
	}
}

// New: the OIDC half of the brief, symmetric to the SCIM heartbeat test
// above, for a Google configuration, with the heartbeat written directly.
// TestOidc_AnUnavailableLinkedAccountIsAccountLocked asserts it end to end,
// from a real sign-in.
func TestSystemStatus_ReportsTheConfiguredOidcProviderAndHeartbeatWithoutExposingSecrets(t *testing.T) {
	t.Parallel()
	const clientSecret = "google-client-secret-harness"
	h := newHarness(t,
		withEnv("OIDC_PROVIDER", "google"),
		withEnv("OIDC_AUTHORITY", "https://accounts.google.com"),
		withEnv("OIDC_CLIENT_ID", "12345-abc.apps.googleusercontent.com"),
		withEnv("OIDC_CLIENT_SECRET", clientSecret),
		withEnv("OIDC_ALLOWED_DOMAINS", "example.com"),
	)
	owner, _ := h.bootstrapOwner(t)
	q := store.New(h.pool)
	if err := q.RecordOperationalEvent(context.Background(), store.RecordOperationalEventParams{
		Kind: "static-oidc.sign-in-succeeded", Now: h.now(),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	r := owner.do(http.MethodGet, systemStatusPath, nil)
	var status ownerSystemStatusBody
	r.json(&status)
	if r.status != http.StatusOK || !status.StaticOidcEnabled || status.StaticOidcProvider == nil || *status.StaticOidcProvider != "google" || status.StaticScimEnabled {
		t.Fatalf("status = %+v", status)
	}
	if status.LastStaticOidcSignInAtUtc == nil || !status.LastStaticOidcSignInAtUtc.Equal(h.now()) {
		t.Errorf("lastStaticOidcSignInAtUtc = %v, want %v", status.LastStaticOidcSignInAtUtc, h.now())
	}
	if strings.Contains(string(r.body), clientSecret) {
		t.Errorf("body leaked the OIDC client secret: %s", r.body)
	}
}
