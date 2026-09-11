package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const login2faPath = "/api/v1/identity/login/2fa"

// Two-factor sign-in's answers, .NET's (EA/AuthEndpoints.cs:370-401).
const (
	lockedMessage      = "The account is temporarily locked. Please try again later."
	unavailableMessage = "The account is temporarily unavailable. Please try again later."
)

// enrolledUser creates a password user with roles and TOTP enrolled, and
// returns its id, the secret and the recovery codes. The enrolling session
// is left signed in (it verified a second factor).
func enrolledUser(t *testing.T, h *harness, email string, roles ...uuid.UUID) (uuid.UUID, string, []string) {
	t.Helper()
	id := h.seedUser(t, email, userPassword, roles...)
	secret, codes := h.enrollTOTP(t, h.login(t, email, userPassword), userPassword)
	return id, secret, codes
}

// secondFactor sends code to /login/2fa from c.
func secondFactor(c *client, code string, rememberMe bool) *resp {
	return c.do(http.MethodPost, login2faPath, map[string]any{"code": code, "rememberMe": rememberMe})
}

// ticketRows is how many login tickets userID has.
func ticketRows(t *testing.T, h *harness, userID uuid.UUID) int {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM identity.login_tickets WHERE user_id = $1`, userID)
}

func failedLogins(t *testing.T, h *harness, userID uuid.UUID) int {
	t.Helper()
	return h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, userID)
}

// TestLogin2fa_TotpCodeCompletesTheSignInWithAnMfaSession proves the second
// step end to end: the body, a non-persistent session cookie, the ticket
// cookie cleared and its row spent, a session row that verified a second
// factor at now, and the spent ticket refused when replayed.
func TestLogin2fa_TotpCodeCompletesTheSignInWithAnMfaSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "two-factor@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	ticket := c.cookie(identity.LoginTicketCookieName)

	r := secondFactor(c, totp(secret, h.now()), false)
	if r.status != http.StatusOK {
		t.Fatalf("login/2fa: status %d body %s", r.status, r.body)
	}
	var body authSuccess
	r.json(&body)
	if body.User == nil || body.User.ID != id || body.User.Email == nil || *body.User.Email != email ||
		!slices.Equal(body.User.Roles, []string{"User"}) || body.RequiresTwoFactor || !body.TwoFactorEnabled || body.MfaEnrollmentRequired {
		t.Errorf("body = %s", r.body)
	}
	if !strings.Contains(string(r.body), `"tenants":[]`) || !strings.Contains(string(r.body), `"activeTenantId":null`) {
		t.Errorf("body %s: want the contract's leftover tenancy as an empty list and a null", r.body)
	}
	cookie := r.setCookie(identity.SessionCookieName)
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("session cookie = %+v, want a non-persistent HttpOnly SameSite=Strict cookie", cookie)
	}
	if cleared := r.setCookie(identity.LoginTicketCookieName); cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("ticket Set-Cookie = %+v, want the ticket cookie cleared", cleared)
	}
	if n := ticketRows(t, h, id); n != 0 {
		t.Errorf("%d login tickets left, want the spent one gone", n)
	}
	if !sessionMFA(t, c) {
		t.Errorf("the new session does not count as MFA-verified")
	}
	var persistent bool
	var verifiedAt *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT persistent, mfa_verified_at FROM identity.sessions WHERE id = $1`,
		h.sessionID(t, c.cookie(identity.SessionCookieName))).Scan(&persistent, &verifiedAt); err != nil {
		t.Fatal(err)
	}
	if persistent || verifiedAt == nil || !verifiedAt.Equal(h.now()) {
		t.Errorf("session row persistent=%v mfa_verified_at=%v, want non-persistent and verified at %v", persistent, verifiedAt, h.now())
	}

	h.advance(totpStep)
	replay := h.client(t).do(http.MethodPost, login2faPath, map[string]any{"code": totp(secret, h.now())},
		header("Cookie", identity.LoginTicketCookieName+"="+ticket))
	if replay.status != http.StatusUnauthorized || replay.code() != "two_factor_session_expired" {
		t.Errorf("the spent ticket replayed: status %d code %q, want 401 two_factor_session_expired", replay.status, replay.code())
	}
	if cleared := replay.setCookie(identity.LoginTicketCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("the replay's ticket Set-Cookie = %+v, want it cleared", cleared)
	}
}

// TestLogin2fa_RememberMeMakesThePersistentCookie proves rememberMe: the
// cookie's Max-Age is the absolute lifetime, and the row is persistent.
func TestLogin2fa_RememberMeMakesThePersistentCookie(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "remember-me@example.test"
	_, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)

	r := secondFactor(c, totp(secret, h.now()), true)
	cookie := r.setCookie(identity.SessionCookieName)
	if r.status != http.StatusOK || cookie == nil || cookie.MaxAge != int(h.cfg.Sessions.Absolute.Seconds()) || cookie.MaxAge != 24*60*60 {
		t.Fatalf("status %d cookie %+v, want Max-Age %d", r.status, cookie, int(h.cfg.Sessions.Absolute.Seconds()))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE id = $1 AND persistent`, h.sessionID(t, cookie.Value)); n != 1 {
		t.Errorf("the session row is not persistent")
	}
}

// TestLogin2fa_BlankCodeIs400 proves a blank code is 400 invalid_request
// before the ticket is looked at, and leaves the ticket usable.
func TestLogin2fa_BlankCodeIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "blank-code@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)

	for _, code := range []any{nil, "", "   "} {
		r := c.do(http.MethodPost, login2faPath, map[string]any{"code": code})
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" || !strings.Contains(string(r.body), "A two-factor code is required.") {
			t.Errorf("code %v: status %d body %s, want 400 invalid_request", code, r.status, r.body)
		}
	}
	if n := failedLogins(t, h, id); n != 0 {
		t.Errorf("failed_login_count = %d, want 0: a blank code is not a failed attempt", n)
	}
	if r := secondFactor(c, totp(secret, h.now()), false); r.status != http.StatusOK {
		t.Errorf("the ticket afterwards: status %d, want 200", r.status)
	}
}

// TestLogin2fa_MissingUnknownAndExpiredTicketsAre401 proves the ticket
// check: no cookie, a cookie that is no ticket, and a ticket at its expiry
// all answer 401 two_factor_session_expired; a second before, it works.
func TestLogin2fa_MissingUnknownAndExpiredTicketsAre401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "expiry@example.test"
	_, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)

	if r := secondFactor(h.client(t), totp(secret, h.now()), false); r.status != http.StatusUnauthorized || r.code() != "two_factor_session_expired" ||
		!strings.Contains(string(r.body), "The two-factor sign-in session has expired.") || r.setCookie(identity.LoginTicketCookieName) != nil {
		t.Errorf("no ticket: status %d body %s, want 401 two_factor_session_expired and no Set-Cookie", r.status, r.body)
	}
	for _, garbage := range []string{"not-a-ticket", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		r := h.client(t).do(http.MethodPost, login2faPath, map[string]any{"code": totp(secret, h.now())}, header("Cookie", identity.LoginTicketCookieName+"="+garbage))
		if r.status != http.StatusUnauthorized || r.code() != "two_factor_session_expired" {
			t.Errorf("ticket %q: status %d code %q, want 401 two_factor_session_expired", garbage, r.status, r.code())
		}
	}

	early := h.startTwoFactor(t, email, userPassword)
	late := h.startTwoFactor(t, email, userPassword)
	h.advance(5*time.Minute - time.Second)
	if r := secondFactor(early, totp(secret, h.now()), false); r.status != http.StatusOK {
		t.Errorf("a second before expiry: status %d body %s, want 200", r.status, r.body)
	}
	h.advance(time.Second)
	if r := secondFactor(late, totp(secret, h.now().Add(totpStep)), false); r.status != http.StatusUnauthorized || r.code() != "two_factor_session_expired" {
		t.Errorf("at expiry: status %d code %q, want 401 two_factor_session_expired", r.status, r.code())
	}
}

// TestLogin2fa_AFailedCodeKeepsTheTicketAndCountsTowardLockout proves a
// failed code, TOTP-shaped or not, is 401 invalid_two_factor_code, counts
// one failure and leaves the ticket, which the right code then redeems,
// clearing the count: .NET's failure path kept the two-factor cookie.
func TestLogin2fa_AFailedCodeKeepsTheTicketAndCountsTowardLockout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "retry@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)

	for i, code := range []string{totp(secret, h.now().Add(-10*time.Minute)), "AAAAA-AAAAA"} {
		r := secondFactor(c, code, false)
		if r.status != http.StatusUnauthorized || r.code() != "invalid_two_factor_code" || !strings.Contains(string(r.body), "The two-factor code is invalid.") {
			t.Errorf("wrong code %q: status %d body %s, want 401 invalid_two_factor_code", code, r.status, r.body)
		}
		if r.setCookie(identity.LoginTicketCookieName) != nil {
			t.Errorf("wrong code %q touched the ticket cookie", code)
		}
		if n := failedLogins(t, h, id); n != i+1 {
			t.Errorf("after %d wrong codes failed_login_count = %d", i+1, n)
		}
	}
	if n := ticketRows(t, h, id); n != 1 {
		t.Errorf("%d login tickets after failed codes, want the one kept", n)
	}
	if r := secondFactor(c, totp(secret, h.now()), false); r.status != http.StatusOK {
		t.Fatalf("the right code on the same ticket: status %d body %s", r.status, r.body)
	}
	if n := failedLogins(t, h, id); n != 0 {
		t.Errorf("failed_login_count = %d after the success, want 0", n)
	}
}

// TestLogin2fa_FiveFailedCodesLockTheAccount proves the second step counts
// toward the password lockout: the fifth wrong code answers 429
// account_locked; the right code then meets a locked account, which
// discards the ticket; 15 minutes later a new sign-in works.
func TestLogin2fa_FiveFailedCodesLockTheAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "lockout@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	wrong := totp(secret, h.now().Add(-10*time.Minute))

	for i := 1; i <= 4; i++ {
		if r := secondFactor(c, wrong, false); r.status != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: status %d, want 401", i, r.status)
		}
	}
	r := secondFactor(c, wrong, false)
	if r.status != http.StatusTooManyRequests || r.code() != "account_locked" || !strings.Contains(string(r.body), lockedMessage) {
		t.Fatalf("the fifth wrong code: status %d body %s, want 429 account_locked", r.status, r.body)
	}
	var lockoutEnd time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT lockout_end FROM identity.users WHERE id = $1`, id).Scan(&lockoutEnd); err != nil {
		t.Fatal(err)
	}
	if !lockoutEnd.Equal(h.now().Add(15*time.Minute)) || failedLogins(t, h, id) != 0 {
		t.Errorf("lockout_end %v count %d, want %v and the count reset by the lock", lockoutEnd, failedLogins(t, h, id), h.now().Add(15*time.Minute))
	}

	r = secondFactor(c, totp(secret, h.now()), false)
	if r.status != http.StatusTooManyRequests || r.code() != "account_locked" || !strings.Contains(string(r.body), unavailableMessage) {
		t.Errorf("the right code while locked: status %d body %s, want 429 account_locked", r.status, r.body)
	}
	if cleared := r.setCookie(identity.LoginTicketCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("ticket Set-Cookie = %+v, want it cleared", cleared)
	}
	if n := ticketRows(t, h, id); n != 0 {
		t.Errorf("%d login tickets left, want the refused one discarded", n)
	}

	h.advance(15 * time.Minute)
	if r := secondFactor(h.startTwoFactor(t, email, userPassword), totp(secret, h.now()), false); r.status != http.StatusOK {
		t.Errorf("after the lockout: status %d body %s, want 200", r.status, r.body)
	}
}

// TestLogin2fa_ADisabledAccountIsRefusedAndSignedOut proves an account
// disabled after the password step gets 429 account_locked at the second,
// counts no failure, and loses the ticket and any session cookie, as .NET
// signed both schemes out. An account whose TOTP was turned off meanwhile
// finds the ticket expired.
func TestLogin2fa_ADisabledAccountIsRefusedAndSignedOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "disabled-2fa@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	c.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: h.session(t, id, false), Path: "/"}})
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)

	r := secondFactor(c, totp(secret, h.now()), false)
	if r.status != http.StatusTooManyRequests || r.code() != "account_locked" || !strings.Contains(string(r.body), unavailableMessage) {
		t.Errorf("disabled: status %d body %s, want 429 account_locked", r.status, r.body)
	}
	for _, name := range []string{identity.LoginTicketCookieName, identity.SessionCookieName} {
		if cleared := r.setCookie(name); cleared == nil || cleared.MaxAge >= 0 {
			t.Errorf("%s Set-Cookie = %+v, want it cleared", name, cleared)
		}
	}
	if ticketRows(t, h, id) != 0 || failedLogins(t, h, id) != 0 {
		t.Errorf("tickets %d failures %d, want the ticket discarded and no failure counted", ticketRows(t, h, id), failedLogins(t, h, id))
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, id)
	c = h.startTwoFactor(t, email, userPassword)
	h.exec(t, `UPDATE identity.users SET totp_enabled = false WHERE id = $1`, id)
	if r := secondFactor(c, totp(secret, h.now()), false); r.status != http.StatusUnauthorized || r.code() != "two_factor_session_expired" {
		t.Errorf("TOTP turned off meanwhile: status %d code %q, want 401 two_factor_session_expired", r.status, r.code())
	}
	if ticketRows(t, h, id) != 0 || failedLogins(t, h, id) != 0 {
		t.Errorf("tickets %d failures %d, want the ticket discarded and no failure counted", ticketRows(t, h, id), failedLogins(t, h, id))
	}
}

// TestLogin2fa_TotpReplayIsRejected proves a spent code signs in only once,
// and a code for an earlier step than the one spent fails inside the
// window; the next step's code works.
func TestLogin2fa_TotpReplayIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "replay@example.test"
	_, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	code := totp(secret, h.now())

	if r := secondFactor(h.startTwoFactor(t, email, userPassword), code, false); r.status != http.StatusOK {
		t.Fatalf("first use: status %d body %s", r.status, r.body)
	}
	c := h.startTwoFactor(t, email, userPassword)
	if r := secondFactor(c, code, false); r.status != http.StatusUnauthorized || r.code() != "invalid_two_factor_code" {
		t.Errorf("the code again: status %d code %q, want 401 invalid_two_factor_code", r.status, r.code())
	}
	h.advance(totpStep)
	if r := secondFactor(c, code, false); r.status != http.StatusUnauthorized || r.code() != "invalid_two_factor_code" {
		t.Errorf("the code a step later, still inside the window: status %d code %q, want 401", r.status, r.code())
	}
	if r := secondFactor(c, totp(secret, h.now()), false); r.status != http.StatusOK {
		t.Errorf("the next step's code: status %d body %s, want 200", r.status, r.body)
	}
}

// TestLogin2fa_ConcurrentUsesOfOneTotpCodeSucceedOnce races one code from
// two sign-ins, each with its own ticket, released together from behind the
// user row so both have verified nothing yet: the one that gets the row
// second finds the step spent (RecordTOTPStep's condition), and exactly one
// gets a session.
func TestLogin2fa_ConcurrentUsesOfOneTotpCodeSucceedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "raced-totp@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	code := totp(secret, h.now())
	a, b := h.startTwoFactor(t, email, userPassword), h.startTwoFactor(t, email, userPassword)
	before := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	responses := raceBehindUserRow(t, h, id, func() *resp { return secondFactor(a, code, false) }, func() *resp { return secondFactor(b, code, false) })
	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusUnauthorized}) {
		t.Fatalf("statuses %v (bodies %s / %s), want one 200 and one 401", statuses, responses[0].body, responses[1].body)
	}
	for _, r := range responses {
		if r.status == http.StatusUnauthorized && r.code() != "invalid_two_factor_code" {
			t.Errorf("the loser's code = %q, want invalid_two_factor_code", r.code())
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != before+1 {
		t.Errorf("%d new sessions, want 1", n-before)
	}
}

// TestLogin2fa_RecoveryCodeIsSingleUse proves a recovery code signs in once,
// in any case, with or without its dash, and then never again.
func TestLogin2fa_RecoveryCodeIsSingleUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "recovery-code@example.test"
	id, _, codes := enrolledUser(t, h, email, identity.RoleUserID)

	c := h.startTwoFactor(t, email, userPassword)
	if r := secondFactor(c, strings.ToLower(strings.ReplaceAll(codes[0], "-", "")), true); r.status != http.StatusOK {
		t.Fatalf("a recovery code, lower case without its dash: status %d body %s", r.status, r.body)
	}
	if !sessionMFA(t, c) {
		t.Errorf("a recovery-code session does not count as MFA-verified")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, id); n != 9 {
		t.Errorf("%d recovery codes left, want 9", n)
	}

	again := h.startTwoFactor(t, email, userPassword)
	if r := secondFactor(again, codes[0], false); r.status != http.StatusUnauthorized || r.code() != "invalid_two_factor_code" {
		t.Errorf("the spent recovery code: status %d code %q, want 401 invalid_two_factor_code", r.status, r.code())
	}
	// A space where the dash was is stripped, as .NET stripped spaces.
	if r := secondFactor(again, strings.Replace(codes[1], "-", " ", 1), false); r.status != http.StatusOK {
		t.Errorf("another recovery code: status %d body %s, want 200", r.status, r.body)
	}
}

// TestLogin2fa_ConcurrentUsesOfOneRecoveryCodeSucceedOnce races one recovery
// code from two sign-ins, released together from behind the user row:
// exactly one gets a session.
func TestLogin2fa_ConcurrentUsesOfOneRecoveryCodeSucceedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "raced-recovery@example.test"
	id, _, codes := enrolledUser(t, h, email, identity.RoleUserID)
	a, b := h.startTwoFactor(t, email, userPassword), h.startTwoFactor(t, email, userPassword)

	responses := raceBehindUserRow(t, h, id, func() *resp { return secondFactor(a, codes[0], false) }, func() *resp { return secondFactor(b, codes[0], false) })
	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusUnauthorized}) {
		t.Fatalf("statuses %v (bodies %s / %s), want one 200 and one 401", statuses, responses[0].body, responses[1].body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, id); n != 9 {
		t.Errorf("%d recovery codes left, want 9", n)
	}
}

// awaitLockWaiters waits until n backends on h's database are waiting on
// a lock, failing t if finished closes first or ten seconds pass.
func awaitLockWaiters(t *testing.T, h *harness, n int, finished <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		select {
		case <-finished:
			t.Fatalf("the requests answered without waiting on the gate's lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d requests ever waited on the gate's lock; backends: %s", n, backends(t, h))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lockUserRow starts a transaction that holds userID's users row, as an MFA
// change or a counted failure holds it, and rolls it back at cleanup unless
// the test commits it first.
func lockUserRow(t *testing.T, h *harness, userID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM identity.users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		t.Fatalf("gate: lock the user row: %v", err)
	}
	return gate
}

// raceBehindUserRow runs fns at once while a gate holds userID's users row,
// and releases the gate only once every one of them is waiting on a lock,
// so they truly overlap at the row every second step locks first.
func raceBehindUserRow(t *testing.T, h *harness, userID uuid.UUID, fns ...func() *resp) []*resp {
	t.Helper()
	gate := lockUserRow(t, h, userID)
	var out []*resp
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		out = race(fns...)
	}()
	awaitLockWaiters(t, h, len(fns), finished)
	if err := gate.Commit(context.Background()); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	<-finished
	return out
}

// TestLogin2fa_OneTicketRedeemsOnceUnderConcurrency races two valid second
// factors, a TOTP code and a recovery code, on one ticket. A gate holds the
// ticket row until both requests are waiting on a lock, so they truly
// overlap. Exactly one sign-in succeeds, the other finds the ticket spent,
// and the loser's factor is not used up.
func TestLogin2fa_OneTicketRedeemsOnceUnderConcurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "one-ticket@example.test"
	id, secret, codes := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	code := totp(secret, h.now())
	sessionsBefore := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)
	stepBefore := readTOTP(t, h, id).lastStep

	raw, err := base64.RawURLEncoding.DecodeString(c.cookie(identity.LoginTicketCookieName))
	if err != nil {
		t.Fatal(err)
	}
	ticketHash := sha256.Sum256(raw)
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM identity.login_tickets WHERE token_hash = $1 FOR UPDATE`, ticketHash[:]); err != nil {
		t.Fatalf("gate: lock the ticket: %v", err)
	}
	var responses []*resp
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		responses = race(func() *resp { return secondFactor(c, code, false) }, func() *resp { return secondFactor(c, codes[0], false) })
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	<-finished

	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusUnauthorized}) {
		t.Fatalf("statuses %v (bodies %s / %s), want one 200 and one 401", statuses, responses[0].body, responses[1].body)
	}
	for _, r := range responses {
		if r.status == http.StatusUnauthorized && r.code() != "two_factor_session_expired" {
			t.Errorf("the loser's code = %q, want two_factor_session_expired", r.code())
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessionsBefore+1 {
		t.Errorf("%d new sessions from one ticket, want 1", n-sessionsBefore)
	}
	recoveryLeft := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, id)
	stepSpent := *readTOTP(t, h, id).lastStep != *stepBefore
	if responses[0].status == http.StatusOK && (!stepSpent || recoveryLeft != 10) ||
		responses[1].status == http.StatusOK && (stepSpent || recoveryLeft != 9) {
		t.Errorf("step spent %v, %d recovery codes left: want only the winner's factor used up", stepSpent, recoveryLeft)
	}
}

// TestLogin2fa_ALockThatLandsWhileWaitingIsTheOneSeen forces the race the
// lockout guards: the account is locked while the second step waits. The
// step locks the user row before it reads the account, so it waits for the
// change and then sees the lockout: 429 account_locked, the ticket
// discarded, and no session, no step spent and no failure counted.
// (ResetLoginFailures' conditional stays as a second guard; under the row
// lock it can no longer lose.)
func TestLogin2fa_ALockThatLandsWhileWaitingIsTheOneSeen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "lock-race@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)
	stepBefore := readTOTP(t, h, id).lastStep
	sessionsBefore := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	ctx := context.Background()
	gate := lockUserRow(t, h, id)
	var r *resp
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r = secondFactor(c, totp(secret, h.now()), false)
	}()
	// The sign-in is waiting on the user row.
	awaitLockWaiters(t, h, 1, finished)
	if _, err := gate.Exec(ctx, `UPDATE identity.users SET lockout_end = $2, failed_login_count = 0 WHERE id = $1`, id, h.now().Add(15*time.Minute)); err != nil {
		t.Fatalf("gate: lock the account: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	<-finished

	if r.status != http.StatusTooManyRequests || r.code() != "account_locked" || !strings.Contains(string(r.body), unavailableMessage) {
		t.Fatalf("status %d body %s, want 429 account_locked", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessionsBefore {
		t.Errorf("%d new sessions, want none", n-sessionsBefore)
	}
	if n := ticketRows(t, h, id); n != 0 {
		t.Errorf("%d login tickets, want the refused one discarded", n)
	}
	if after := readTOTP(t, h, id).lastStep; after == nil || stepBefore == nil || *after != *stepBefore {
		t.Errorf("last spent step %v, want %v: the refused sign-in spent nothing", after, stepBefore)
	}
	if n := failedLogins(t, h, id); n != 0 {
		t.Errorf("failed_login_count = %d, want 0", n)
	}
}

// TestLogin2fa_TakesTheUserRowBeforeTheRecoveryCodes proves the lock order.
// An MFA change holds the user row and then locks the user's recovery
// codes, as disable and regeneration do (users first, then the codes). A
// recovery-code sign-in that started meanwhile waits for the user row
// without having touched the codes, so the change completes and the
// sign-in then succeeds. Spending the code before taking the row
// deadlocked here: PostgreSQL aborted one side with 40P01, a 500.
func TestLogin2fa_TakesTheUserRowBeforeTheRecoveryCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "lock-order@example.test"
	id, _, codes := enrolledUser(t, h, email, identity.RoleUserID)
	c := h.startTwoFactor(t, email, userPassword)

	ctx := context.Background()
	gate := lockUserRow(t, h, id)
	var r *resp
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r = secondFactor(c, codes[0], false)
	}()
	awaitLockWaiters(t, h, 1, finished)
	if _, err := gate.Exec(ctx, `SELECT 1 FROM identity.recovery_codes WHERE user_id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("the change could not lock the recovery codes behind the waiting sign-in: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	<-finished

	if r.status != http.StatusOK {
		t.Fatalf("the recovery-code sign-in after the change: status %d body %s, want 200", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, id); n != 9 {
		t.Errorf("%d recovery codes left, want 9", n)
	}
}

// TestLogout_AndSelfRevocationDiscardAPendingTwoFactorTicket proves the
// sign-outs end a pending two-factor sign-in, as .NET's SignOutAsync signed
// the TwoFactorUserId scheme out: the ticket cookie is cleared, its row is
// gone, and the ticket no longer redeems. A sign-out without a ticket sets
// no ticket cookie.
func TestLogout_AndSelfRevocationDiscardAPendingTwoFactorTicket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "sign-out-ticket@example.test"
	id, secret, _ := enrolledUser(t, h, email, identity.RoleUserID)

	for _, path := range []string{logoutPath, "/api/v1/identity/account/sessions/revoke"} {
		c := h.startTwoFactor(t, email, userPassword)
		ticket := c.cookie(identity.LoginTicketCookieName)
		c.http.Jar.SetCookies(h.base, []*http.Cookie{{Name: identity.SessionCookieName, Value: h.session(t, id, false), Path: "/"}})

		r := c.do(http.MethodPost, path, nil)
		if r.status != http.StatusOK {
			t.Fatalf("POST %s: status %d body %s", path, r.status, r.body)
		}
		if cleared := r.setCookie(identity.LoginTicketCookieName); cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
			t.Errorf("POST %s: ticket Set-Cookie = %+v, want it cleared", path, cleared)
		}
		if n := ticketRows(t, h, id); n != 0 {
			t.Errorf("POST %s: %d login tickets left, want 0", path, n)
		}
		replay := h.client(t).do(http.MethodPost, login2faPath, map[string]any{"code": totp(secret, h.now())},
			header("Cookie", identity.LoginTicketCookieName+"="+ticket))
		if replay.status != http.StatusUnauthorized || replay.code() != "two_factor_session_expired" {
			t.Errorf("POST %s: the discarded ticket: status %d code %q, want 401 two_factor_session_expired", path, replay.status, replay.code())
		}
	}

	r := h.signIn(t, id, false).do(http.MethodPost, logoutPath, nil)
	if r.status != http.StatusOK || r.setCookie(identity.LoginTicketCookieName) != nil {
		t.Errorf("a logout without a ticket: status %d ticket Set-Cookie %+v, want none", r.status, r.setCookie(identity.LoginTicketCookieName))
	}
}
