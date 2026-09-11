package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	loginPath  = "/api/v1/identity/login"
	logoutPath = "/api/v1/identity/logout"

	userPassword = "IntegrationUserPassword123"
)

func credentials(email, password string) map[string]string {
	return map[string]string{"email": email, "password": password}
}

// authSuccess is AuthSuccessResponse as a test reads it.
type authSuccess struct {
	User                  *authUser `json:"user"`
	RequiresTwoFactor     bool      `json:"requiresTwoFactor"`
	TwoFactorEnabled      bool      `json:"twoFactorEnabled"`
	MfaEnrollmentRequired bool      `json:"mfaEnrollmentRequired"`
}

// Ported from IdentityPasswordAuthIntegrationTests.LocalPasswordLoginEstablishesSessionAndLogoutClearsIt.
// Go's logout also revokes the session row (spec *Sessions*, a deliberate
// divergence), so the old cookie is dead even if replayed.
func TestLogin_LocalPasswordLoginEstablishesSessionAndLogoutClearsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.client(t)

	r := c.do(http.MethodPost, loginPath, credentials(email, userPassword))
	if r.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", r.status, r.body)
	}
	var body authSuccess
	r.json(&body)
	if body.User == nil || body.User.ID != id || body.User.Email == nil || *body.User.Email != email ||
		!slices.Equal(body.User.Roles, []string{"User"}) || body.RequiresTwoFactor || body.TwoFactorEnabled || body.MfaEnrollmentRequired {
		t.Errorf("login body = %s", r.body)
	}
	if !strings.Contains(string(r.body), `"tenants":[]`) || !strings.Contains(string(r.body), `"activeTenantId":null`) {
		t.Errorf("login body %s: want the contract's leftover tenancy as an empty list and a null", r.body)
	}
	cookie := r.setCookie(identity.SessionCookieName)
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("session cookie = %+v, want a non-persistent HttpOnly SameSite=Strict cookie", cookie)
	}
	token := c.cookie(identity.SessionCookieName)
	admitted(t, c)

	r = c.do(http.MethodPost, logoutPath, nil)
	var logout struct {
		Success bool `json:"success"`
	}
	r.json(&logout)
	if r.status != http.StatusOK || !logout.Success {
		t.Errorf("logout: status %d body %s", r.status, r.body)
	}
	if cleared := r.setCookie(identity.SessionCookieName); cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("logout Set-Cookie = %+v, want the cookie cleared", cleared)
	}
	rejected(t, c)

	replay := h.client(t).do(http.MethodGet, sessionPath, nil, header("Cookie", identity.SessionCookieName+"="+token))
	if replay.status != http.StatusUnauthorized {
		t.Errorf("the logged-out cookie replayed: status %d, want 401", replay.status)
	}
}

// Ported from IdentityPasswordAuthIntegrationTests.UnknownEmailAndIncorrectPasswordReturnIndistinguishableResponsesAndNoSession.
func TestLogin_UnknownEmailAndIncorrectPasswordAreIndistinguishable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	unknownClient, incorrectClient := h.client(t), h.client(t)

	unknown := unknownClient.do(http.MethodPost, loginPath, credentials("unknown-9e3f@integration.test", userPassword))
	incorrect := incorrectClient.do(http.MethodPost, loginPath, credentials(email, "WrongPassword123"))

	if unknown.status != http.StatusUnauthorized || incorrect.status != http.StatusUnauthorized {
		t.Fatalf("statuses %d and %d, want 401 for both", unknown.status, incorrect.status)
	}
	if unknown.code() != "invalid_credentials" || string(unknown.body) != string(incorrect.body) {
		t.Errorf("bodies differ or wrong code: %s vs %s", unknown.body, incorrect.body)
	}
	if !strings.Contains(string(unknown.body), "Invalid email or password.") {
		t.Errorf("body %s, want .NET's message", unknown.body)
	}
	for _, r := range []*resp{unknown, incorrect} {
		if c := r.setCookie(identity.SessionCookieName); c != nil {
			t.Errorf("a failed login set %+v", c)
		}
	}
	rejected(t, unknownClient)
	rejected(t, incorrectClient)
}

// Ported from IdentityPasswordAuthIntegrationTests.UnsafePasswordEndpointsRejectMissingAntiforgeryHeader.
// Go has no antiforgery token: the platform's CrossOriginProtection refuses
// an unsafe cross-site browser request with a 403 before identity sees it,
// where .NET answered 400 csrf_validation_failed. The same request sent
// same-origin passes through to identity.
func TestLogin_CrossSiteUnsafeRequestsAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	// Off-contract by design: the platform's 403 problem is not in identity.yaml.
	crossSite := []reqOpt{origin("https://evil.example"), header("Sec-Fetch-Site", "cross-site"), skipContract("CSRF probe")}
	sameOrigin := []reqOpt{origin(h.url), header("Sec-Fetch-Site", "same-origin")}

	signedIn := h.login(t, email, userPassword)
	for _, probe := range []struct {
		c    *client
		path string
		body any
	}{
		{h.client(t), loginPath, credentials("csrf-dummy@integration.test", "csrf-dummy-password")},
		{h.client(t), "/api/v1/identity/password-recovery/request", map[string]string{"email": "csrf-dummy@integration.test"}},
		{h.client(t), "/api/v1/identity/password-recovery/reset", map[string]string{"email": "csrf-dummy@integration.test", "token": "csrf-dummy-token", "newPassword": "csrf-dummy-new-password"}},
		{signedIn, logoutPath, nil},
	} {
		r := probe.c.do(http.MethodPost, probe.path, probe.body, crossSite...)
		if r.status != http.StatusForbidden || r.header("Content-Type") != "application/problem+json" {
			t.Errorf("cross-site %s: status %d Content-Type %q, want the 403 problem", probe.path, r.status, r.header("Content-Type"))
		}
	}
	admitted(t, signedIn) // the refused logout never reached identity

	if r := h.client(t).do(http.MethodPost, loginPath, credentials("csrf-dummy@integration.test", "csrf-dummy-password"), sameOrigin...); r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" {
		t.Errorf("same-origin login: status %d code %q, want 401 invalid_credentials", r.status, r.code())
	}
	if r := signedIn.do(http.MethodPost, logoutPath, nil, sameOrigin...); r.status != http.StatusOK {
		t.Errorf("same-origin logout: status %d, want 200", r.status)
	}
	if r := h.client(t).do(http.MethodPost, "/api/v1/identity/password-recovery/request", map[string]string{"email": "csrf-dummy@integration.test"}, sameOrigin...); r.status != http.StatusOK {
		t.Errorf("same-origin recovery request: status %d, want 200", r.status)
	}
	if r := h.client(t).do(http.MethodPost, "/api/v1/identity/password-recovery/reset", map[string]string{"email": "csrf-dummy@integration.test", "token": "csrf-dummy-token", "newPassword": "csrf-dummy-new-password"}, sameOrigin...); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("same-origin recovery reset: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}
}

// Ported from LoginThrottlingIntegrationTests.Repeated_failures_for_one_account_are_throttled_without_affecting_others.
func TestLoginThrottling_RepeatedFailuresForOneAccountAreThrottledWithoutAffectingOthers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	const email = "stuffing-5a1c@integration.test"

	for attempt := range 10 {
		if r := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", attempt+1, r.status)
		}
	}
	throttled := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password"))
	if throttled.status != http.StatusTooManyRequests || throttled.code() != "rate_limited" ||
		!strings.Contains(string(throttled.body), "Too many authentication attempts. Please try again later.") {
		t.Errorf("11th attempt: status %d body %s, want 429 rate_limited", throttled.status, throttled.body)
	}
	if got := throttled.header("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want none: the throttle does not say when to retry", got)
	}

	// A different account from the same client is judged on its own
	// credentials, not the throttled account's failures.
	if r := c.do(http.MethodPost, loginPath, credentials("other-5a1c@integration.test", "wrong-password")); r.status != http.StatusUnauthorized {
		t.Errorf("another account: status %d, want 401", r.status)
	}
}

// throttleHits is the login throttle's count for email from the client
// address ip, 0 without a row. The key is the hex SHA-256 of the normalized
// email, then "|" and the address.
func throttleHits(t *testing.T, h *harness, email, ip string) int {
	t.Helper()
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(email))))
	return h.count(t, `SELECT coalesce(sum(hits), 0) FROM platform.rate_limit WHERE key = $1`,
		"login-attempts:"+hex.EncodeToString(sum[:])+"|"+ip)
}

// Ported from LoginThrottlingIntegrationTests.A_successful_login_clears_the_account_failure_window.
// It also reads the throttle row and the lockout counter directly: four
// failures (one short of lockout) are recorded on both, and the success
// clears both.
func TestLoginThrottling_ASuccessfulLoginClearsTheAccountFailureWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.client(t)

	for attempt := range 4 {
		if r := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", attempt+1, r.status)
		}
	}
	if n := throttleHits(t, h, email, c.ip); n != 4 {
		t.Errorf("throttle hits after 4 failures = %d", n)
	}
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, id); n != 4 {
		t.Errorf("failed_login_count after 4 failures = %d", n)
	}

	if r := c.do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusOK {
		t.Fatalf("success: status %d body %s", r.status, r.body)
	}
	if n := throttleHits(t, h, email, c.ip); n != 0 {
		t.Errorf("throttle hits after the success = %d, want the window cleared", n)
	}
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, id); n != 0 {
		t.Errorf("failed_login_count after the success = %d, want 0", n)
	}

	// The cleared window means more attempts are available again.
	if r := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusUnauthorized {
		t.Errorf("after the success: status %d, want 401", r.status)
	}
}

// TestLoginThrottling_IsPerClientAddress proves the harness's synthetic
// X-Forwarded-For becomes each client's address end to end: one client
// exhausts the throttle for an email, and another client, with its own
// address, is still judged on its credentials for that same email. The
// throttle rows are keyed by each client's exact synthetic address.
func TestLoginThrottling_IsPerClientAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a, b := h.client(t), h.client(t)
	const email = "shared-8b2d@integration.test"

	for range 10 {
		a.do(http.MethodPost, loginPath, credentials(email, "wrong-password"))
	}
	if r := a.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusTooManyRequests {
		t.Fatalf("client A's 11th attempt: status %d, want 429", r.status)
	}
	if r := b.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusUnauthorized {
		t.Errorf("client B, same email: status %d, want 401 (not throttled)", r.status)
	}
	if n := throttleHits(t, h, email, a.ip); n != 10 {
		t.Errorf("client A's throttle row (%s) has %d hits, want 10", a.ip, n)
	}
	if n := throttleHits(t, h, email, b.ip); n != 1 {
		t.Errorf("client B's throttle row (%s) has %d hits, want 1", b.ip, n)
	}
	// The limiter never holds the email in clear.
	if n := h.count(t, `SELECT count(*) FROM platform.rate_limit WHERE upper(key) LIKE '%SHARED-8B2D%'`); n != 0 {
		t.Errorf("%d limiter rows carry the email in clear", n)
	}
}

// TestLogin_OversizeEmailIsAnUnknownEmail is the regression for a 16 KB
// email, which once made the throttle's key too large for the limiter's
// index and answered 500. It is now just an unknown email: the same 401
// invalid_credentials body as any other, contract-validated (the contract
// puts no bound on the email), with its failure counted on a bounded key.
// The email is random hex: PostgreSQL compresses a long value before
// indexing it, so a repetitive one would fit the index and prove nothing.
func TestLogin_OversizeEmailIsAnUnknownEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	noise := make([]byte, 8*1024)
	_, _ = rand.Read(noise) // crypto/rand.Read never returns an error
	oversize := hex.EncodeToString(noise) + "@example.test"

	usual := c.do(http.MethodPost, loginPath, credentials("unknown-3c4d@integration.test", "wrong-password"))
	r := c.do(http.MethodPost, loginPath, credentials(oversize, "wrong-password"))
	if r.status != http.StatusUnauthorized || r.code() != "invalid_credentials" || string(r.body) != string(usual.body) {
		t.Errorf("oversize email: status %d body %.200s, want the unknown-email 401 %s", r.status, r.body, usual.body)
	}
	if n := throttleHits(t, h, oversize, c.ip); n != 1 {
		t.Errorf("the oversize email's throttle row has %d hits, want 1", n)
	}
}

// TestLogin_FiveFailuresLockTheAccountForFifteenMinutes proves ASP.NET
// Identity's lockout as .NET configured it: the fifth wrong password itself
// answers 429 account_locked, the right password is refused while the lock
// holds, and it is accepted again at exactly 15 minutes on the harness clock.
func TestLogin_FiveFailuresLockTheAccountForFifteenMinutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.client(t)

	for attempt := range 4 {
		if r := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password")); r.status != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d, want 401", attempt+1, r.status)
		}
	}
	locked := c.do(http.MethodPost, loginPath, credentials(email, "wrong-password"))
	if locked.status != http.StatusTooManyRequests || locked.code() != "account_locked" ||
		!strings.Contains(string(locked.body), "The account is temporarily locked. Please try again later.") {
		t.Fatalf("fifth failure: status %d body %s, want 429 account_locked", locked.status, locked.body)
	}
	var lockoutEnd time.Time
	var failures int
	if err := h.pool.QueryRow(context.Background(), `SELECT lockout_end, failed_login_count FROM identity.users WHERE id = $1`, id).Scan(&lockoutEnd, &failures); err != nil {
		t.Fatal(err)
	}
	if !lockoutEnd.Equal(start.Add(15*time.Minute)) || failures != 0 {
		t.Errorf("lockout_end %v failed_login_count %d, want %v and 0", lockoutEnd, failures, start.Add(15*time.Minute))
	}

	if r := c.do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusTooManyRequests || r.code() != "account_locked" {
		t.Errorf("right password while locked: status %d code %q, want 429 account_locked", r.status, r.code())
	}
	h.advance(15*time.Minute - time.Second)
	if r := c.do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusTooManyRequests {
		t.Errorf("one second before the lock ends: status %d, want 429", r.status)
	}
	h.advance(time.Second)
	if r := c.do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusOK {
		t.Errorf("when the lock ends: status %d body %s, want 200", r.status, r.body)
	}
}

// TestLogin_UnavailableAccountIsRefusedBeforeThePassword proves .NET's
// "effectively disabled" check comes before the password: a disabled
// account, or (with SCIM configured) a directory-deactivated non-Owner, gets
// 429 account_locked whatever the password, and no failure is counted. A
// directory-deactivated Owner still signs in.
func TestLogin_UnavailableAccountIsRefusedBeforeThePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SCIM_TOKEN", "identity-harness-scim-token"))
	deactivate := func(id uuid.UUID) {
		h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		           VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), start)
	}
	disabled := h.seedUser(t, "disabled@example.test", userPassword, identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	deactivate(h.seedUser(t, "deactivated@example.test", userPassword, identity.RoleUserID))
	deactivate(h.seedUser(t, "deactivated-owner@example.test", userPassword, identity.RoleOwnerID))
	c := h.client(t)

	for _, email := range []string{"disabled@example.test", "deactivated@example.test"} {
		for _, pw := range []string{userPassword, "wrong-password"} {
			r := c.do(http.MethodPost, loginPath, credentials(email, pw))
			if r.status != http.StatusTooManyRequests || r.code() != "account_locked" ||
				!strings.Contains(string(r.body), "The account is temporarily unavailable. Please try again later.") {
				t.Errorf("%s with %q: status %d body %s, want 429 account_locked", email, pw, r.status, r.body)
			}
		}
	}
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, disabled); n != 0 {
		t.Errorf("failed_login_count = %d, want no failure counted", n)
	}
	if r := c.do(http.MethodPost, loginPath, credentials("deactivated-owner@example.test", userPassword)); r.status != http.StatusOK {
		t.Errorf("directory-deactivated Owner: status %d, want 200", r.status)
	}
}

// TestLogin_InvalidRequestListsTheMissingFields proves the first check: a
// blank email or password is 400 invalid_request with .NET's messages.
func TestLogin_InvalidRequestListsTheMissingFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	for _, tc := range []struct {
		body any
		want []string
	}{
		{map[string]string{}, []string{"email", "password"}},
		{credentials(" ", "x"), []string{"email"}},
		{credentials("user@example.test", ""), []string{"password"}},
	} {
		r := c.do(http.MethodPost, loginPath, tc.body)
		var body struct {
			Error struct {
				Code    string              `json:"code"`
				Message string              `json:"message"`
				Fields  map[string][]string `json:"fields"`
			} `json:"error"`
		}
		r.json(&body)
		var keys []string
		for k := range body.Error.Fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if r.status != http.StatusBadRequest || body.Error.Code != "invalid_request" || body.Error.Message != "The login request is invalid." || !slices.Equal(keys, tc.want) {
			t.Errorf("%v: status %d body %s, want 400 invalid_request on %v", tc.body, r.status, r.body, tc.want)
		}
		if f := body.Error.Fields["email"]; f != nil && !slices.Equal(f, []string{"Email is required."}) {
			t.Errorf("email message %v", f)
		}
		if f := body.Error.Fields["password"]; f != nil && !slices.Equal(f, []string{"Password is required."}) {
			t.Errorf("password message %v", f)
		}
	}
}

// TestLogin_WithTotpEnrolledAnswersRequiresTwoFactorWithATicket proves the
// first half of two-factor sign-in: no session, the requiresTwoFactor body,
// a vantigo.2fa cookie carrying a ticket whose hash is a login_tickets row
// that expires in five minutes, and any session cookie the browser held
// cleared.
func TestLogin_WithTotpEnrolledAnswersRequiresTwoFactorWithATicket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "mfa@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleOwnerID)
	h.exec(t, `UPDATE identity.users SET totp_enabled = true WHERE id = $1`, id)
	c := h.signIn(t, id, false) // a browser that still holds an older session

	r := c.do(http.MethodPost, loginPath, credentials(email, userPassword))
	if r.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", r.status, r.body)
	}
	var body authSuccess
	r.json(&body)
	if body.User != nil || !body.RequiresTwoFactor || !body.TwoFactorEnabled || body.MfaEnrollmentRequired ||
		!strings.Contains(string(r.body), `"user":null`) || !strings.Contains(string(r.body), `"activeTenantId":null`) {
		t.Errorf("body = %s, want the requiresTwoFactor answer", r.body)
	}
	ticket := r.setCookie(identity.LoginTicketCookieName)
	if ticket == nil || ticket.Value == "" || !ticket.HttpOnly || ticket.SameSite != http.SameSiteStrictMode || ticket.MaxAge != 300 {
		t.Fatalf("ticket cookie = %+v", ticket)
	}
	if cleared := r.setCookie(identity.SessionCookieName); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("session Set-Cookie = %+v, want the older session cookie cleared", cleared)
	}

	raw, err := base64.RawURLEncoding.DecodeString(ticket.Value)
	if err != nil || len(raw) != 32 {
		t.Fatalf("ticket %q: %d bytes (%v), want a 32-byte token", ticket.Value, len(raw), err)
	}
	hash := sha256.Sum256(raw)
	var owner uuid.UUID
	var expires time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT user_id, expires_at FROM identity.login_tickets WHERE token_hash = $1`, hash[:]).Scan(&owner, &expires); err != nil {
		t.Fatalf("login ticket row: %v", err)
	}
	if owner != id || !expires.Equal(start.Add(5*time.Minute)) {
		t.Errorf("ticket row user %v expires %v, want %v and %v", owner, expires, id, start.Add(5*time.Minute))
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d sessions, want only the older one: a password alone starts none", n)
	}
}

// TestLogin_ResponseCarriesOrderedRolesAndTheOwnerEnrolmentHint proves the
// success body lists roles in .NET's order and tells an Owner without TOTP
// to enrol while owners are required to use MFA.
func TestLogin_ResponseCarriesOrderedRolesAndTheOwnerEnrolmentHint(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "owner-admin@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID, identity.RoleOwnerID, identity.RoleSystemAdminID)

	r := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword))
	var body authSuccess
	r.json(&body)
	if r.status != http.StatusOK || body.User == nil || !slices.Equal(body.User.Roles, []string{"SystemAdmin", "Owner", "User"}) || !body.MfaEnrollmentRequired {
		t.Errorf("status %d body %s", r.status, r.body)
	}
}

// TestProviders_OffersNoOIDCWhileItIsOff proves /providers answers the
// contract's shape with no OIDC provider while none is configured
// (TestOidc_ProvidersReportTheConfiguredProvidersDisplayName covers one).
func TestProviders_OffersNoOIDCWhileItIsOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := h.client(t).do(http.MethodGet, "/api/v1/identity/providers", nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"oidc":null}` {
		t.Errorf("status %d body %s, want {\"oidc\":null}", r.status, r.body)
	}
}
