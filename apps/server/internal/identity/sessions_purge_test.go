package identity_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// TestSessions_ASignInPurgesTheUsersDeadSessions: every sign-in deletes, in
// its own transaction, the user's sessions that can never be valid again —
// revoked ones and ones past the standard absolute lifetime — and keeps
// every live one. The sessions die through the endpoints and the clock: a
// logout revokes one, and the clock carries another past the lifetime.
// Another user's dead session is not the sign-in's to purge.
func TestSessions_ASignInPurgesTheUsersDeadSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email, otherEmail = "purge@example.test", "bystander@example.test"
	userID := h.seedUser(t, email, userPassword, identity.RoleUserID)
	otherID := h.seedUser(t, otherEmail, userPassword, identity.RoleUserID)
	absolute := h.cfg.Sessions.Absolute
	idle := h.cfg.Sessions.Idle

	sessions := func(id any) int {
		return h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)
	}

	aged := h.login(t, email, userPassword) // past the absolute lifetime once the clock moves on
	bystander := h.login(t, otherEmail, userPassword)
	if r := bystander.do(http.MethodPost, logoutPath, nil); r.status != http.StatusOK {
		t.Fatalf("bystander logout: status %d", r.status)
	}

	h.advance(absolute - idle/2)
	live := h.login(t, email, userPassword)
	loggedOut := h.login(t, email, userPassword)
	if r := loggedOut.do(http.MethodPost, logoutPath, nil); r.status != http.StatusOK {
		t.Fatalf("logout: status %d", r.status)
	}
	if n := sessions(userID); n != 3 {
		t.Fatalf("before the purge: %d sessions, want the aged, live and logged-out three", n)
	}

	h.advance(idle/2 + time.Minute) // aged is now past the absolute lifetime; live is inside both bounds
	rejected(t, aged)
	latest := h.login(t, email, userPassword)

	if n := sessions(userID); n != 2 {
		t.Errorf("after the purge: %d sessions, want the live one and the new one", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND revoked_at IS NOT NULL`, userID); n != 0 {
		t.Errorf("%d revoked sessions survived the purge", n)
	}
	admitted(t, live)
	admitted(t, latest)
	if n := sessions(otherID); n != 1 {
		t.Errorf("the bystander's revoked session: %d rows, want 1 (untouched by another user's sign-in)", n)
	}
}

// TestSessions_ThePurgeKeepsASessionOnlyPastThePrivilegedLifetime: the
// purge measures age against the standard absolute lifetime, the looser
// bound, because whether the privileged bound applies is decided per
// request from the roles the user holds then. An Owner's session past the
// privileged lifetime but inside the standard one is rejected while they
// are an Owner, but stays: a demotion would make it valid again.
func TestSessions_ThePurgeKeepsASessionOnlyPastThePrivilegedLifetime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	if h.cfg.Sessions.PrivilegedAbsolute >= h.cfg.Sessions.Absolute {
		t.Fatalf("the harness's privileged absolute lifetime %v is not below the standard %v", h.cfg.Sessions.PrivilegedAbsolute, h.cfg.Sessions.Absolute)
	}

	h.advance(h.cfg.Sessions.PrivilegedAbsolute + time.Minute)
	h.login(t, ownerEmail, ownerPassword)

	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, ownerID); n != 2 {
		t.Errorf("%d Owner sessions, want the bootstrap session (past only the privileged lifetime) and the new one", n)
	}
}
