package identity_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

const selfRevokePath = "/api/v1/identity/account/sessions/revoke"

func revokePathFor(userID uuid.UUID) string {
	return "/api/v1/identity/system/users/" + userID.String() + "/sessions/revoke"
}

// authSession is AuthSessionResponse as a test reads it.
type authSession struct {
	User                  authUser `json:"user"`
	TwoFactorEnabled      bool     `json:"twoFactorEnabled"`
	MfaEnrollmentRequired bool     `json:"mfaEnrollmentRequired"`
	MfaAuthenticated      bool     `json:"mfaAuthenticated"`
	IsSystemAdmin         bool     `json:"isSystemAdmin"`
}

func getSession(t *testing.T, c *client) (authSession, *resp) {
	t.Helper()
	r := c.do(http.MethodGet, sessionPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /session: status %d body %s", r.status, r.body)
	}
	var s authSession
	r.json(&s)
	return s, r
}

// TestSession_DescribesTheCallerAndTheirSession proves /session's body:
// the user with every role in orderRoles order, TOTP state, the Owner
// enrolment hint, whether this session verified a second factor, and the
// SystemAdmin flag.
func TestSession_DescribesTheCallerAndTheirSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	billing := h.insertRole(t, "Billing")
	const email = "admin@example.test"
	id := h.seedUser(t, email, userPassword, billing, identity.RoleUserID, identity.RoleOwnerID, identity.RoleSystemAdminID)

	s, _ := getSession(t, h.login(t, email, userPassword))
	if s.User.ID != id || s.User.DisplayName != email || s.User.Email == nil || *s.User.Email != email ||
		!slices.Equal(s.User.Roles, []string{"SystemAdmin", "Owner", "User", "Billing"}) {
		t.Errorf("user = %+v", s.User)
	}
	if s.TwoFactorEnabled || !s.MfaEnrollmentRequired || s.MfaAuthenticated || !s.IsSystemAdmin {
		t.Errorf("session = %+v, want no TOTP, enrolment required, no MFA, SystemAdmin", s)
	}

	// With TOTP enrolled and a session that verified it: the hint goes and
	// mfaAuthenticated comes.
	h.exec(t, `UPDATE identity.users SET totp_enabled = true WHERE id = $1`, id)
	if s, _ := getSession(t, h.signIn(t, id, true)); !s.TwoFactorEnabled || s.MfaEnrollmentRequired || !s.MfaAuthenticated {
		t.Errorf("MFA session = %+v", s)
	}
}

// TestSession_SyntheticSSOEmailIsNull proves an OIDC account's synthetic
// @sso.invalid address is never shown.
func TestSession_SyntheticSSOEmailIsNull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "oidc-"+strings.Repeat("a1", 32)+"@sso.invalid", identity.RoleUserID)

	s, r := getSession(t, h.signIn(t, id, false))
	if s.User.Email != nil || !strings.Contains(string(r.body), `"email":null`) {
		t.Errorf("body %s, want email null", r.body)
	}
}

// Ported from IdentitySessionRevocationIntegrationTests.SelfRevocationEndsEverySessionForTheAccount.
func TestSessionRevocation_SelfRevocationEndsEverySessionForTheAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	first, second := h.login(t, email, userPassword), h.login(t, email, userPassword)
	admitted(t, second)
	firstToken := first.cookie(identity.SessionCookieName)

	r := first.do(http.MethodPost, selfRevokePath, nil)
	var body struct {
		UserID  uuid.UUID `json:"userId"`
		Revoked bool      `json:"revoked"`
	}
	r.json(&body)
	if r.status != http.StatusOK || body.UserID != id || !body.Revoked {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	if cleared := r.setCookie(identity.SessionCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("Set-Cookie = %+v, want the caller's cookie cleared", cleared)
	}
	rejected(t, second)
	rejected(t, first)
	// Not only the cookie: the caller's own session row is revoked too.
	if r := h.client(t).do(http.MethodGet, sessionPath, nil, header("Cookie", identity.SessionCookieName+"="+firstToken)); r.status != http.StatusUnauthorized {
		t.Errorf("the caller's revoked cookie replayed: status %d, want 401", r.status)
	}

	events := h.auditEvents(t, "user.sessions-revoked")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != id || events[0].TargetUser == nil || *events[0].TargetUser != id {
		t.Errorf("audit events = %+v, want one by the user on themselves", events)
	}
}

// Ported from IdentitySessionRevocationIntegrationTests.SelfRevocationRequiresAuthentication.
func TestSessionRevocation_SelfRevocationRequiresAuthentication(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if r := h.client(t).do(http.MethodPost, selfRevokePath, nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("status %d code %q, want 401 unauthenticated", r.status, r.code())
	}
}

// Ported from IdentitySessionRevocationIntegrationTests.SystemAdminRevokesAnotherAccountsSessions.
// The .NET factory's Owner is its configured SystemAdmin; here every
// bootstrap Owner is one.
func TestSessionRevocation_SystemAdminRevokesAnotherAccountsSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "victim@example.test"
	victimID := h.seedUser(t, email, userPassword, identity.RoleUserID)
	victim := h.login(t, email, userPassword)
	administrator, adminID := h.bootstrapOwner(t)

	r := administrator.do(http.MethodPost, revokePathFor(victimID), nil)
	var body struct {
		UserID  uuid.UUID `json:"userId"`
		Revoked bool      `json:"revoked"`
	}
	r.json(&body)
	if r.status != http.StatusOK || body.UserID != victimID || !body.Revoked {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	if c := r.setCookie(identity.SessionCookieName); c != nil {
		t.Errorf("Set-Cookie = %+v, want the administrator's cookie untouched", c)
	}
	rejected(t, victim)
	// Revoking someone else's sessions must not disturb the administrator's own.
	admitted(t, administrator)

	events := h.auditEvents(t, "user.sessions-revoked")
	if len(events) != 1 {
		t.Fatalf("%d audit events, want 1", len(events))
	}
	e := events[0]
	if e.Actor == nil || *e.Actor != adminID || e.TargetUser == nil || *e.TargetUser != victimID || e.TargetRole != nil || e.MFA ||
		e.Details != `{"action":"user.sessions-revoked"}` || e.Before != `{"sessionsRevoked":false}` ||
		e.After != `{"sessionsRevoked":true,"revokedAt":"2026-09-11T12:00:00Z"}` {
		t.Errorf("audit event = %+v", e)
	}
}

// Ported from IdentitySessionRevocationIntegrationTests.RevokedAccountCanSignInAgain.
func TestSessionRevocation_RevokedAccountCanSignInAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	administrator, _ := h.bootstrapOwner(t)
	if r := administrator.do(http.MethodPost, revokePathFor(id), nil); r.status != http.StatusOK {
		t.Fatalf("revoke: status %d", r.status)
	}

	admitted(t, h.login(t, email, userPassword))
}

// Ported from IdentitySessionRevocationIntegrationTests.NonAdministratorCannotRevokeAnotherAccountsSessions.
// An Owner who is not a SystemAdmin (seeded directly, since every bootstrap
// Owner is one) is refused too.
func TestSessionRevocation_NonAdministratorCannotRevokeAnotherAccountsSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.seedUser(t, "target@example.test", userPassword, identity.RoleUserID)
	h.seedUser(t, "caller@example.test", userPassword, identity.RoleUserID)
	targetClient := h.login(t, "target@example.test", userPassword)
	h.seedUser(t, ownerEmail, ownerPassword, identity.RoleOwnerID)
	owner := h.login(t, ownerEmail, ownerPassword)

	for name, c := range map[string]*client{"User": h.login(t, "caller@example.test", userPassword), "Owner": owner} {
		if r := c.do(http.MethodPost, revokePathFor(target), nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("%s: status %d code %q, want 403 forbidden", name, r.status, r.code())
		}
	}
	admitted(t, targetClient)
}

// Ported from IdentitySessionRevocationIntegrationTests.RevokingAnUnknownAccountReturnsNotFound.
func TestSessionRevocation_RevokingAnUnknownAccountReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	administrator, _ := h.bootstrapOwner(t)

	r := administrator.do(http.MethodPost, revokePathFor(uuid.New()), nil)
	if r.status != http.StatusNotFound || r.code() != "user_not_found" || !strings.Contains(string(r.body), "The account does not exist.") {
		t.Errorf("status %d body %s, want 404 user_not_found", r.status, r.body)
	}
	if n := len(h.auditEvents(t, "user.sessions-revoked")); n != 0 {
		t.Errorf("%d audit events for an unknown account, want none", n)
	}
}

// Ported from IdentitySessionRevocationIntegrationTests.RotatingTheStampEndsTheOtherSessionsButNotTheCallers.
// .NET triggered this with a profile edit. Go's own profile edit
// deliberately revokes nothing (spec *Sessions*), so the test drives the
// caller-sparing revocation every self-change uses (revokeOtherSessions)
// over two real signed-in sessions; each self-change proves its own trigger
// where it is implemented.
func TestSessionRevocation_RotatingTheStampEndsTheOtherSessionsButNotTheCallers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	caller, other := h.login(t, email, userPassword), h.login(t, email, userPassword)
	admitted(t, other)

	keep := h.sessionID(t, caller.cookie(identity.SessionCookieName))
	if err := identity.RevokeOtherSessions(h.access, context.Background(), store.New(h.pool), id, keep); err != nil {
		t.Fatal(err)
	}
	rejected(t, other)
	admitted(t, caller)
}

// TestSessionRevocation_SystemAdminRevokingThemselvesIsSignedOut proves the
// one case where the SystemAdmin revocation spares nobody: naming their own
// account ends their session too and clears their cookie, as .NET signed
// them out (EA/SessionEndpoints.cs:91-94).
func TestSessionRevocation_SystemAdminRevokingThemselvesIsSignedOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	administrator, adminID := h.bootstrapOwner(t)
	token := administrator.cookie(identity.SessionCookieName)

	r := administrator.do(http.MethodPost, revokePathFor(adminID), nil)
	if r.status != http.StatusOK {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	if cleared := r.setCookie(identity.SessionCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("Set-Cookie = %+v, want the cookie cleared", cleared)
	}
	if r := h.client(t).do(http.MethodGet, sessionPath, nil, header("Cookie", identity.SessionCookieName+"="+token)); r.status != http.StatusUnauthorized {
		t.Errorf("the administrator's cookie replayed: status %d, want 401", r.status)
	}
}

// TestSessionRevocation_BothRevocationsSpendResetLinks: .NET's RevokeAsync,
// behind both revocation endpoints (EA/SessionEndpoints.cs:47, :84), rotated
// the security stamp (UpdateSecurityStampAsync, :109), which also killed
// every reset link the target held. Go deletes the target's reset tokens in
// the revocation's transaction, for the self-revocation and a SystemAdmin's
// revocation of another account alike.
func TestSessionRevocation_BothRevocationsSpendResetLinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := h.bootstrapOwner(t)

	for _, via := range []string{"self", "system"} {
		email := via + "-revocation@example.test"
		id := h.createUser(t, admin, email, identity.RoleUser)
		c := h.login(t, email, userPassword)
		requestRecovery(t, h, email)
		token := mailedLink(t, h, email).Query().Get("token")

		var r *resp
		if via == "self" {
			r = c.do(http.MethodPost, "/api/v1/identity/account/sessions/revoke", nil)
		} else {
			r = admin.do(http.MethodPost, revokePathFor(id), nil)
		}
		if r.status != http.StatusOK {
			t.Fatalf("%s revocation: status %d body %s", via, r.status, r.body)
		}
		if r := reset(h, t, email, token, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
			t.Errorf("%s revocation: the reset link afterwards: status %d code %q, want 400 invalid_reset_token", via, r.status, r.code())
		}
		if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 0 {
			t.Errorf("%s revocation: %d reset tokens left, want 0", via, n)
		}
		rejected(t, c)
	}
	admitted(t, admin)
}
