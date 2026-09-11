package identity_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	recoveryRequestPath = "/api/v1/identity/password-recovery/request"
	recoveryResetPath   = "/api/v1/identity/password-recovery/reset"

	newUserPassword = "IntegrationNewPassword123"
)

// requestRecovery sends POST /password-recovery/request for email from a
// new client (the recovery limit is ten a quarter-hour per client).
func requestRecovery(t *testing.T, h *harness, email string) *resp {
	t.Helper()
	r := h.client(t).do(http.MethodPost, recoveryRequestPath, map[string]string{"email": email})
	if r.status != http.StatusOK {
		t.Fatalf("recovery request %s: status %d body %s", email, r.status, r.body)
	}
	return r
}

// reset sends POST /password-recovery/reset from a new client.
func reset(h *harness, t *testing.T, email, token, newPassword string) *resp {
	return h.client(t).do(http.MethodPost, recoveryResetPath, map[string]string{"email": email, "token": token, "newPassword": newPassword})
}

// Ported from IdentityPasswordAuthIntegrationTests.PasswordRecoveryReturnsIndistinguishableResponsesAndEmailsOnlyEligibleAccount.
func TestPasswordRecovery_RequestAnswerIsIndistinguishable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const (
		known        = "known@example.test"
		unknown      = "unknown-recovery@example.test"
		unconfirmed  = "unconfirmed@example.test"
		passwordless = "passwordless@example.test"
	)
	h.createUser(t, owner, known, identity.RoleUser) // an Owner-created account's email is confirmed
	h.seedUser(t, unconfirmed, userPassword, identity.RoleUserID)
	id := h.insertUser(t, passwordless, identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET email_confirmed = true WHERE id = $1`, id)

	var bodies []string
	for _, email := range []string{known, unknown, unconfirmed, passwordless} {
		bodies = append(bodies, string(requestRecovery(t, h, email).body))
	}
	blank := h.client(t).do(http.MethodPost, recoveryRequestPath, map[string]string{})
	bodies = append(bodies, string(blank.body))
	for i, b := range bodies {
		if b != bodies[0] {
			t.Errorf("answer %d = %s, want %s like the first", i, b, bodies[0])
		}
	}
	if strings.TrimSpace(bodies[0]) != `{"accepted":true}` {
		t.Errorf("answer = %s, want {\"accepted\":true}", bodies[0])
	}
	if got := h.mail.Messages(); len(got) != 1 || got[0].To != known {
		t.Errorf("mails = %+v, want exactly one, to the eligible account", got)
	}
}

// Ported from IdentityPasswordAuthIntegrationTests.PasswordRecoveryRoundTripChangesPasswordInvalidatesSessionAndRejectsReplay.
func TestPasswordRecovery_RoundTripEndsSessionsAndRejectsReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "recovering@example.test"
	userID := h.createUser(t, owner, email, identity.RoleUser)
	existing := h.login(t, email, userPassword)

	requestRecovery(t, h, email)
	msgs := h.mailTo(email)
	if len(msgs) != 1 {
		t.Fatalf("%d mails, want 1", len(msgs))
	}
	link := mailedLink(t, h, email)
	query := link.Query()
	wantBody := "Use this link to reset your password: " + h.url + "/password-reset?email=recovering%40example.test&token=" + query.Get("token")
	if msgs[0].Subject != "Reset your Vantigo password" || msgs[0].TextBody != wantBody {
		t.Errorf("mail = %q / %q, want %q / %q", msgs[0].Subject, msgs[0].TextBody, "Reset your Vantigo password", wantBody)
	}
	if query.Get("email") != email || query.Get("token") == "" {
		t.Fatalf("link %s, want the email and a token", link)
	}

	r := reset(h, t, query.Get("email"), query.Get("token"), newUserPassword)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"success":true}` {
		t.Fatalf("reset: status %d body %s", r.status, r.body)
	}
	rejected(t, existing)

	if r := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Errorf("old password: status %d code %q, want 401 invalid_credentials", r.status, r.code())
	}
	admitted(t, h.login(t, email, newUserPassword))

	// The token is spent: replaying it changes nothing.
	if r := reset(h, t, query.Get("email"), query.Get("token"), "AnotherIntegrationPassword123"); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("replay: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}
	rejected(t, existing)
	if r := h.client(t).do(http.MethodPost, loginPath, credentials(email, "AnotherIntegrationPassword123")); r.status != http.StatusUnauthorized {
		t.Errorf("the replayed password signs in: status %d", r.status)
	}
	h.login(t, email, newUserPassword)
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("%d reset tokens left, want 0", n)
	}
}

// TestPasswordRecovery_TokenExpiresAfter24Hours holds two tokens minted
// twelve hours apart: at the first one's 24 hours it is refused while the
// second still works.
func TestPasswordRecovery_TokenExpiresAfter24Hours(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "slow@example.test"
	h.createUser(t, owner, email, identity.RoleUser)

	requestRecovery(t, h, email)
	first := mailedLink(t, h, email).Query().Get("token")
	h.advance(12 * time.Hour)
	requestRecovery(t, h, email)
	second := mailedLink(t, h, email).Query().Get("token")
	h.advance(12 * time.Hour)

	if r := reset(h, t, email, first, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("24 hours on: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}
	if r := reset(h, t, email, second, newUserPassword); r.status != http.StatusOK {
		t.Errorf("12 hours on: status %d body %s, want 200", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens`); n != 0 {
		t.Errorf("%d reset tokens left, want 0: the expired one purged, the used one spent", n)
	}
}

// TestPasswordRecovery_ResetRefusesEverythingButTheAccountsOwnToken: any
// token failure is the same 400 invalid_reset_token, and a blank password
// is refused without spending the token.
func TestPasswordRecovery_ResetRefusesEverythingButTheAccountsOwnToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email, other = "holder@example.test", "other@example.test"
	h.createUser(t, owner, email, identity.RoleUser)
	h.createUser(t, owner, other, identity.RoleUser)
	requestRecovery(t, h, email)
	token := mailedLink(t, h, email).Query().Get("token")

	for _, c := range []struct{ email, token string }{
		{other, token},
		{"nobody@example.test", token},
		{email, "not-a-token"},
		{email, ""},
		{"", token},
	} {
		r := reset(h, t, c.email, c.token, newUserPassword)
		if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_reset_token" || e.Message != "The password reset token is invalid or expired." {
			t.Errorf("reset %q with %q: status %d error %+v, want 400 invalid_reset_token", c.email, c.token, r.status, e)
		}
	}
	r := reset(h, t, email, token, "   ")
	if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_request" ||
		!fieldsEqual(e.Fields, map[string][]string{"newPassword": {"A new password is required."}}) {
		t.Errorf("blank password: status %d error %+v, want 400 invalid_request on newPassword", r.status, e)
	}
	if r := reset(h, t, " HOLDER@example.test ", token, newUserPassword); r.status != http.StatusOK {
		t.Errorf("reset with the email as typed: status %d body %s, want 200", r.status, r.body)
	}
	h.login(t, email, newUserPassword)
}

// TestPasswordRecovery_ConcurrentResetsWithOneTokenSucceedOnce races two
// resets with one token: however they interleave, the token is spent once,
// so one reset succeeds and the other is 400 invalid_reset_token, and only
// the winner's password signs in. Run with -count to repeat the race.
func TestPasswordRecovery_ConcurrentResetsWithOneTokenSucceedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "raced-reset@example.test"
	h.createUser(t, owner, email, identity.RoleUser)
	requestRecovery(t, h, email)
	token := mailedLink(t, h, email).Query().Get("token")

	passwords := []string{"RacedPasswordOne123", "RacedPasswordTwo123"}
	clients := []*client{h.client(t), h.client(t)}
	resetWith := func(i int) func() *resp {
		return func() *resp {
			return clients[i].do(http.MethodPost, recoveryResetPath, map[string]string{"email": email, "token": token, "newPassword": passwords[i]})
		}
	}
	responses := race(resetWith(0), resetWith(1))

	winner := -1
	for i, r := range responses {
		switch {
		case r.status == http.StatusOK:
			if winner >= 0 {
				t.Fatalf("both resets succeeded with one token")
			}
			winner = i
		case r.status != http.StatusBadRequest || r.code() != "invalid_reset_token":
			t.Errorf("reset %d: status %d code %q, want 200 or 400 invalid_reset_token", i, r.status, r.code())
		}
	}
	if winner < 0 {
		t.Fatalf("neither reset succeeded")
	}
	h.login(t, email, passwords[winner])
	if r := h.client(t).do(http.MethodPost, loginPath, credentials(email, passwords[1-winner])); r.status != http.StatusUnauthorized {
		t.Errorf("the loser's password: status %d, want 401", r.status)
	}
}

// TestPasswordRecovery_AFailedSendIsSwallowedAndLogsNoAddress: the answer
// to a failed send is the usual one, and the warning names neither the
// address nor the mail error.
func TestPasswordRecovery_AFailedSendIsSwallowedAndLogsNoAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "bouncing@example.test"
	userID := h.createUser(t, owner, email, identity.RoleUser)

	h.mail.FailNext(errors.New("smtp: mailbox unavailable (" + mailFailureMarker + "), affected recipient(s): " + email))
	failed := requestRecovery(t, h, email)
	unknown := requestRecovery(t, h, "nobody@example.test")
	if string(failed.body) != string(unknown.body) {
		t.Errorf("a failed send answers %s, an unknown email %s; want them equal", failed.body, unknown.body)
	}
	if len(h.mail.Messages()) != 0 {
		t.Errorf("a mail was recorded for the failed send")
	}
	var warned bool
	for _, rec := range h.logRecords(t) {
		line := strings.Join(logValues(rec), " ")
		if strings.Contains(line, email) || strings.Contains(line, mailFailureMarker) || strings.Contains(line, "token=") {
			t.Errorf("a log record carries the address, the mail error or the link: %v", rec)
		}
		if rec["msg"] == "password reset email could not be sent" && rec["level"] == "WARN" && rec["user_id"] == userID.String() {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no WARN record for the failed send")
	}
}
