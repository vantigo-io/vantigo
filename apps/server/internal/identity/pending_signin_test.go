package identity_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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

// An Owner's password set judges pending on the row it writes, not only on
// the read before: a user made pending by a transaction that holds their row
// while the set waits on it (shown parked by pg_blocking_pids) is refused 409
// account_pending once it commits, and keeps no password.
func TestStaffManaged_APasswordSetRacingAPendingMarkIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := h.insertUser(t, "racing-password@example.test", identity.RoleUserID)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	holder, err := pgx.Connect(ctx, h.cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect the row holder: %v", err)
	}
	defer func() { _ = holder.Close(context.Background()) }()
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'P-1', employee_number_key = 'P-1', pending_since = $2 WHERE id = $1`, id, h.now()); err != nil {
		t.Fatalf("mark the user pending: %v", err)
	}
	var holderPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}

	result := make(chan *resp, 1)
	go func() {
		result <- owner.do(http.MethodPost, userPath(id, "/password"), map[string]string{"password": userPassword})
	}()
	deadline := time.Now().Add(15 * time.Second)
	for h.count(t, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))`, holderPID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the password set never waited on the user's row")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the pending mark: %v", err)
	}

	if r := <-result; r.status != http.StatusConflict || r.code() != "account_pending" {
		t.Errorf("the racing password set: status %d body %s, want 409 account_pending", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND password_hash IS NULL`, id); n != 1 {
		t.Errorf("the user made pending has a password")
	}
}

// A session survives no pending mark: a user made pending by SQL after
// signing in is unauthenticated on their next request, as a disabled one is.
func TestSession_APendingUsersSessionEnds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "pending-session@example.test", identity.RoleUserID)
	token := h.session(t, id, false)
	if _, o := check(t, h, token, "session"); o != allowed {
		t.Fatalf("before the mark: %v, want allowed", o)
	}

	h.exec(t, `UPDATE identity.users SET pending_since = $2 WHERE id = $1`, id, h.now())
	if _, o := check(t, h, token, "session"); o != u401 {
		t.Errorf("after the mark: %v, want ErrUnauthenticated", o)
	}
}
