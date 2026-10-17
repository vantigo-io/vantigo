package identity_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// A pending user's only way in is accepting their invitation: an Owner
// cannot set their password, and should a credential reach them all the
// same (here by SQL), password, two-factor, passkey and OIDC sign-in refuse
// them as they refuse a disabled account.

// An Owner's password set on a pending user is 409 account_pending, and the
// user keeps no password.
func TestStaffManaged_AnOwnerCannotSetAPendingUsersPassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := pendingUser(t, h, "pending-password@example.test", "Pending", "E-20", identity.RoleUserID)

	r := owner.do(http.MethodPost, userPath(id, "/password"), map[string]string{"password": userPassword})
	if r.status != http.StatusConflict || r.code() != "account_pending" {
		t.Errorf("set a pending user's password: status %d body %s, want 409 account_pending", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND password_hash IS NULL`, id); n != 1 {
		t.Errorf("the pending user has a password")
	}
}

// A pending user with a password is refused at the password step as a
// disabled account is: 429 account_locked, whatever the password.
func TestLogin_APendingUserIsRefusedAsDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "pending-login@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	makePending(t, h, id, "E-21")

	r := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword))
	if r.status != http.StatusTooManyRequests || r.code() != "account_locked" {
		t.Errorf("a pending user's sign-in: status %d body %s, want 429 account_locked", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != 0 {
		t.Errorf("%d sessions, want none", n)
	}
}

// A user made pending between the password step and the second factor is
// refused at the second, as one disabled there is.
func TestLogin2fa_APendingUserIsRefusedAtTheSecondFactor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "pending-2fa@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)
	makePending(t, h, id, "E-22")

	if r := secondFactor(c, totp(secret, h.now()), false); r.status != http.StatusTooManyRequests || r.code() != "account_locked" {
		t.Errorf("a pending user's second factor: status %d body %s, want 429 account_locked", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("%d sessions, want %d", n, sessions)
	}
}

// A pending user with a passkey is no passkey sign-in candidate, and a
// ceremony begun before they became pending is refused at completion.
func TestPasskeyLogin_APendingUserIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "pending-passkey@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	makePending(t, h, id, "E-23")
	wantAuthError(t, "pending at completion", completePasskeyLogin(t, login, ceremony, a), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)

	beginPasskeyLogin(t, h.client(t), email)
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE user_id = $1 AND kind = 'login' AND consumed_at IS NULL`, id); n != 0 {
		t.Errorf("%d live ceremonies hold the pending user, want none", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("%d sessions, want %d", n, sessions)
	}
}

// A pending user linked to a federated identity is refused account_locked,
// as a disabled one is.
func TestOIDC_APendingLinkedUserIsRefused(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	first := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, first, f), "/")
	id := oidcSession(t, first).User.ID

	makePending(t, h, id, "E-24")
	rejected := h.oidcSignIn(t, h.client(t), f)
	assertOIDCRedirect(t, rejected, oidcErrorLocation("account_locked"))
	if rejected.setCookie(identity.SessionCookieName) != nil {
		t.Error("a pending account got a session cookie")
	}
}
