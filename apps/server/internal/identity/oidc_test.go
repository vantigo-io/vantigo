package identity_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// oidcSessionBody is AuthSessionResponse as the OIDC tests read it.
type oidcSessionBody struct {
	User *struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"displayName"`
		Email       *string   `json:"email"`
		Roles       []string  `json:"roles"`
	} `json:"user"`
	TwoFactorEnabled      bool `json:"twoFactorEnabled"`
	MfaEnrollmentRequired bool `json:"mfaEnrollmentRequired"`
	MfaAuthenticated      bool `json:"mfaAuthenticated"`
}

// oidcSession is c's session, which must be signed in.
func oidcSession(t *testing.T, c *client) oidcSessionBody {
	t.Helper()
	r := c.do(http.MethodGet, "/api/v1/identity/session", nil)
	if r.status != http.StatusOK {
		t.Fatalf("session: status %d body %s", r.status, r.body)
	}
	var s oidcSessionBody
	r.json(&s)
	if s.User == nil {
		t.Fatalf("session without a user: %s", r.body)
	}
	return s
}

// assertOIDCRedirect fails t unless r is a 302 to location.
func assertOIDCRedirect(t *testing.T, r *resp, location string) {
	t.Helper()
	if r.status != http.StatusFound || r.header("Location") != location {
		t.Fatalf("status %d Location %q, want 302 %s", r.status, r.header("Location"), location)
	}
}

// assertCleared fails t unless r tells the browser to drop cookie name at
// path.
func assertCleared(t *testing.T, r *resp, name, path string) {
	t.Helper()
	c := r.setCookie(name)
	if c == nil || c.Value != "" || c.MaxAge >= 0 || c.Path != path {
		t.Errorf("Set-Cookie %s = %+v, want it cleared at %s", name, c, path)
	}
}

// assertLaxCookie fails t unless r sets cookie name as the OIDC flow sets
// its cookies: HttpOnly, SameSite=Lax, at path, for maxAge seconds (and not
// Secure, in the development harness). It returns the cookie.
func assertLaxCookie(t *testing.T, r *resp, name, path string, maxAge int) *http.Cookie {
	t.Helper()
	c := r.setCookie(name)
	if c == nil || c.Value == "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != path || c.MaxAge != maxAge || c.Secure {
		t.Fatalf("Set-Cookie %s = %+v, want HttpOnly SameSite=Lax Path=%s Max-Age=%d", name, c, path, maxAge)
	}
	return c
}

// assertNoSecretsLogged fails t if any log record carries one of f's
// secret values, or one of extra.
func assertNoSecretsLogged(t *testing.T, h *harness, f *fakeOIDC, extra ...string) {
	t.Helper()
	logged := h.log.Bytes()
	for _, v := range append(f.secretValues(), extra...) {
		if bytes.Contains(logged, []byte(v)) {
			t.Errorf("the log carries a secret value of %d bytes", len(v))
		}
	}
}

// insertOIDCLink links userID to (issuer, subject) directly, as a sign-in
// would have.
func insertOIDCLink(t *testing.T, h *harness, issuer, subject string, userID uuid.UUID) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.oidc_links (issuer, subject, user_id, created_at) VALUES ($1, $2, $3, $4)`,
		issuer, subject, userID, h.now())
}

// Ported from DisabledIdentityOidcIntegrationTests.DisabledOidcChallenge_ReturnsNotFound,
// plus complete's 404 and the callback's failure redirect.
func TestOidc_DisabledChallengeReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)

	if r := c.do(http.MethodGet, oidcChallengePath, nil); r.status != http.StatusNotFound {
		t.Errorf("challenge: status %d, want 404", r.status)
	}
	if r := c.do(http.MethodGet, oidcCompletePath, nil); r.status != http.StatusNotFound {
		t.Errorf("complete: status %d, want 404", r.status)
	}
	// No challenge can have started, so a response is a remote failure, as
	// .NET's disabled scheme answered on its callback path.
	assertOIDCRedirect(t, c.do(http.MethodGet, oidcCallbackPath+"?code=c&state=s", nil), oidcErrorLocation("oidc_remote_failure"))
}

// Ported from IdentityOidcIntegrationTests.OidcCodePkceFlow_UsesRealMiddlewareAndJitProvisionsUser.
// Every hop is asserted: the authorization request (code, PKCE S256, nonce,
// state, the fixed callback, the scopes, response_mode=query, no secret),
// both cookies' attributes, the redemption's form, the session and the
// provisioned account.
func TestOidc_CodeAndPkceFlowProvisionsTheUserJustInTime(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	c := h.client(t)

	if r := c.do(http.MethodGet, "/api/v1/identity/providers", nil); r.status != http.StatusOK ||
		strings.TrimSpace(string(r.body)) != `{"oidc":{"displayName":"Workforce SSO"}}` {
		t.Errorf("providers: status %d body %s", r.status, r.body)
	}

	challenge := c.do(http.MethodGet, oidcChallengePath, nil)
	if challenge.status != http.StatusFound {
		t.Fatalf("challenge: status %d body %s", challenge.status, challenge.body)
	}
	authorize, err := url.Parse(challenge.header("Location"))
	if err != nil || authorize.Scheme != "https" || authorize.Host != "login.microsoftonline.com" ||
		authorize.Path != "/"+fakeTenantID+"/v2.0/oauth2/v2.0/authorize" {
		t.Fatalf("challenge: Location %q is not the provider's authorization endpoint", challenge.header("Location"))
	}
	q := authorize.Query()
	for k, want := range map[string]string{
		"response_type":         "code",
		"client_id":             fakeEntraClientID,
		"redirect_uri":          harnessOrigin + oidcCallbackPath,
		"scope":                 "openid profile email",
		"code_challenge_method": "S256",
		"response_mode":         "query",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("authorization %s = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if q.Get(k) == "" {
			t.Errorf("authorization %s is empty", k)
		}
	}
	if strings.Contains(authorize.RawQuery, "client_secret") || strings.Contains(authorize.RawQuery, fakeClientSecret) {
		t.Errorf("the authorization request carries the client secret: %s", authorize.RawQuery)
	}
	state := assertLaxCookie(t, challenge, identity.OIDCStateCookieName, oidcCookiePath, 600)
	if strings.Contains(state.Value, q.Get("state")) || strings.Contains(state.Value, q.Get("nonce")) {
		t.Error("the state cookie carries the state or nonce in the clear")
	}

	back := f.authorize(t, authorize)
	callback := c.do(http.MethodGet, h.pathOf(t, back), nil)
	assertOIDCRedirect(t, callback, oidcCompletePath)
	assertCleared(t, callback, identity.OIDCStateCookieName, oidcCookiePath)
	assertLaxCookie(t, callback, identity.OIDCExternalCookieName, oidcCookiePath, 300)

	forms := f.redemptionForms()
	if len(forms) != 1 {
		t.Fatalf("%d redemptions, want 1", len(forms))
	}
	backURL, _ := url.Parse(back)
	verifierHash := sha256.Sum256([]byte(forms[0].Get("code_verifier")))
	if form := forms[0]; form.Get("grant_type") != "authorization_code" || form.Get("code") != backURL.Query().Get("code") ||
		form.Get("redirect_uri") != harnessOrigin+oidcCallbackPath || form.Get("client_id") != fakeEntraClientID ||
		form.Get("client_secret") != fakeClientSecret || form.Has("client_assertion") || b64(verifierHash[:]) != q.Get("code_challenge") {
		t.Errorf("redemption form %v", form)
	}

	complete := c.do(http.MethodGet, oidcCompletePath, nil)
	assertOIDCRedirect(t, complete, "/")
	assertCleared(t, complete, identity.OIDCExternalCookieName, oidcCookiePath)
	if sc := complete.setCookie(identity.SessionCookieName); sc == nil || sc.Value == "" || !sc.HttpOnly ||
		sc.SameSite != http.SameSiteStrictMode || sc.MaxAge != 0 || sc.Path != "/" {
		t.Errorf("session cookie = %+v, want a non-persistent Strict session cookie", sc)
	}

	s := oidcSession(t, c)
	if s.User.Email == nil || *s.User.Email != fakeEmail || s.User.DisplayName != fakeDisplayName ||
		!slices.Equal(s.User.Roles, []string{"User"}) || s.MfaAuthenticated || s.TwoFactorEnabled {
		t.Errorf("session = %+v", s)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users
		WHERE email = $1 AND normalized_email = upper($1) AND email_confirmed AND password_hash IS NULL AND display_name = $2`,
		fakeEmail, fakeDisplayName); n != 1 {
		t.Errorf("%d provisioned users as expected, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE issuer = $1 AND subject = $2 AND user_id = $3 AND created_at = $4`,
		fakeEntraAuthority, fakeSubject, s.User.ID, h.now()); n != 1 {
		t.Errorf("%d links, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1`, s.User.ID); n != 1 ||
		h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, s.User.ID, identity.RoleUserID) != 1 {
		t.Errorf("roles: %d, want User only", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1 AND mfa_verified_at IS NULL AND NOT persistent`, s.User.ID); n != 1 {
		t.Errorf("%d non-MFA non-persistent sessions, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.operational_events WHERE kind = 'static-oidc.sign-in-succeeded' AND occurred_at = $1`, h.now()); n != 1 {
		t.Errorf("heartbeat rows = %d, want 1", n)
	}
	listed := false
	for _, u := range ownerUsers(t, owner) {
		if u.ID == s.User.ID {
			listed = true
			if !u.SsoEnabled {
				t.Error("ssoEnabled is false for the provisioned user")
			}
		}
	}
	if !listed {
		t.Error("the provisioned user is not listed")
	}
	assertNoSecretsLogged(t, h, f)
}

// Ported from IdentityOidcIntegrationTests.OidcCodeRedemptionFailure_IsRedirectedWithoutCreatingSessionOrUser.
func TestOidc_AFailedCodeRedemptionCreatesNothing(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.update(func(f *fakeOIDC) { f.tokenError = "invalid_grant" })
	h := newHarness(t, f.options()...)
	c := h.client(t)

	callback := h.oidcCallback(t, c, f)
	assertOIDCRedirect(t, callback, oidcErrorLocation("oidc_authentication_failed"))
	assertCleared(t, callback, identity.OIDCStateCookieName, oidcCookiePath)
	if callback.setCookie(identity.OIDCExternalCookieName) != nil || callback.setCookie(identity.SessionCookieName) != nil {
		t.Error("a failed redemption set an identity or session cookie")
	}
	if r := c.do(http.MethodGet, "/api/v1/identity/session", nil); r.status != http.StatusUnauthorized {
		t.Errorf("session: status %d, want 401", r.status)
	}
	assertOIDCRedirect(t, c.do(http.MethodGet, oidcCompletePath, nil), oidcErrorLocation("oidc_external_identity_missing"))
	for _, table := range []string{"users", "oidc_links", "sessions"} {
		if n := h.count(t, `SELECT count(*) FROM identity.`+table); n != 0 {
			t.Errorf("identity.%s has %d rows, want 0", table, n)
		}
	}
	assertNoSecretsLogged(t, h, f)
}

// Ported from IdentityOidcIntegrationTests.DisabledExistingOidcAccount_IsRejectedWithGenericAccountLockedError,
// plus a lockout, which is unavailable too until it ends. The system
// status reports the provider and the heartbeat of the real sign-in.
func TestOidc_AnUnavailableLinkedAccountIsAccountLocked(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	first := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, first, f), "/")
	id := oidcSession(t, first).User.ID

	r := owner.do(http.MethodGet, systemStatusPath, nil)
	var status ownerSystemStatusBody
	r.json(&status)
	if r.status != http.StatusOK || !status.StaticOidcEnabled || status.StaticOidcProvider == nil || *status.StaticOidcProvider != "entra" ||
		status.LastStaticOidcSignInAtUtc == nil || !status.LastStaticOidcSignInAtUtc.Equal(h.now()) {
		t.Errorf("system status = %s", r.body)
	}
	if strings.Contains(string(r.body), fakeClientSecret) {
		t.Errorf("the system status carries the client secret: %s", r.body)
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)
	rejected := h.oidcSignIn(t, h.client(t), f)
	assertOIDCRedirect(t, rejected, oidcErrorLocation("account_locked"))
	assertCleared(t, rejected, identity.OIDCExternalCookieName, oidcCookiePath)
	if rejected.setCookie(identity.SessionCookieName) != nil {
		t.Error("a disabled account got a session cookie")
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = false, lockout_end = $2 WHERE id = $1`, id, h.now().Add(15*time.Minute))
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("account_locked"))
	h.advance(15 * time.Minute)
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != 2 {
		t.Errorf("%d sessions, want 2: the first sign-in and the one after the lockout", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id <> $1 AND email = $2`, id, fakeEmail); n != 0 {
		t.Errorf("%d other accounts were provisioned", n)
	}
}

// A state that does not match the cookie's is a remote failure, and the
// cookie is spent by it: the genuine response then fails too, and the code
// is never redeemed. A response on a browser that never started the
// sign-in, and a provider's error, are remote failures as well.
func TestOidc_AStateMismatchIsARemoteFailureAndSpendsTheStateCookie(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	c := h.client(t)

	back, err := url.Parse(h.oidcCallbackLocation(t, c, f))
	if err != nil {
		t.Fatal(err)
	}
	forged := *back
	q := forged.Query()
	q.Set("state", "forged-"+q.Get("state"))
	forged.RawQuery = q.Encode()
	r := c.do(http.MethodGet, h.pathOf(t, forged.String()), nil)
	assertOIDCRedirect(t, r, oidcErrorLocation("oidc_remote_failure"))
	assertCleared(t, r, identity.OIDCStateCookieName, oidcCookiePath)
	if r.setCookie(identity.OIDCExternalCookieName) != nil {
		t.Error("a forged state set the external identity cookie")
	}
	assertOIDCRedirect(t, c.do(http.MethodGet, h.pathOf(t, back.String()), nil), oidcErrorLocation("oidc_remote_failure"))

	stranger := h.client(t)
	someoneElses := h.oidcCallbackLocation(t, h.client(t), f)
	assertOIDCRedirect(t, stranger.do(http.MethodGet, h.pathOf(t, someoneElses), nil), oidcErrorLocation("oidc_remote_failure"))

	f.update(func(f *fakeOIDC) { f.authorizeError = "access_denied" })
	assertOIDCRedirect(t, h.oidcCallback(t, h.client(t), f), oidcErrorLocation("oidc_remote_failure"))

	if n := len(f.redemptionForms()); n != 0 {
		t.Errorf("%d codes were redeemed, want 0", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users, want 0", n)
	}
}

// An id_token that fails validation is an authentication failure, after
// the redemption, and creates nothing: a nonce other than the one sealed in
// the state cookie, another audience, another issuer, an authorized party
// that is another client, a token without iat or exp; and every forged
// signature: alg "none", HS256 keyed with the provider's public key (key
// confusion), a foreign RSA key under the provider's kid, and a kid the
// provider's JWKS does not have.
func TestOidc_AnInvalidIdTokenIsAnAuthenticationFailure(t *testing.T) {
	t.Parallel()
	forged := func(forge func(map[string]string, []byte) string) func(f *fakeOIDC) {
		return func(f *fakeOIDC) { f.update(func(f *fakeOIDC) { f.forge = forge }) }
	}
	for name, setup := range map[string]func(f *fakeOIDC){
		"nonce mismatch":                         func(f *fakeOIDC) { f.set("nonce", "another-nonce") },
		"wrong audience":                         func(f *fakeOIDC) { f.set("aud", fakeOtherTenantID) },
		"wrong issuer":                           func(f *fakeOIDC) { f.set("iss", "https://login.microsoftonline.com/"+fakeOtherTenantID+"/v2.0") },
		"another authorized part":                func(f *fakeOIDC) { f.set("azp", fakeOtherTenantID) },
		"no issued-at":                           func(f *fakeOIDC) { f.unset("iat") },
		"no expiry":                              func(f *fakeOIDC) { f.unset("exp") },
		"alg none":                               forged(forgeAlgNone),
		"HS256 keyed with the RSA public key":    forged(forgeHS256WithThePublicKey),
		"a foreign key under the provider's kid": forged(forgeWithAForeignKey),
		"an unknown kid":                         forged(forgeWithAnUnknownKid),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFakeOIDC(t)
			setup(f)
			h := newHarness(t, f.options()...)
			r := h.oidcCallback(t, h.client(t), f)
			assertOIDCRedirect(t, r, oidcErrorLocation("oidc_authentication_failed"))
			assertCleared(t, r, identity.OIDCStateCookieName, oidcCookiePath)
			if r.setCookie(identity.OIDCExternalCookieName) != nil {
				t.Error("an invalid id_token set the external identity cookie")
			}
			if n := len(f.redemptionForms()); n != 1 {
				t.Errorf("%d redemptions, want 1", n)
			}
			for _, table := range []string{"users", "oidc_links", "sessions"} {
				if n := h.count(t, `SELECT count(*) FROM identity.`+table); n != 0 {
					t.Errorf("identity.%s has %d rows, want 0", table, n)
				}
			}
		})
	}
}

// A valid token that fails the Entra claim policy is a remote failure, as
// .NET's OnTokenValidated failed it: another tenant, no object id, or two
// audiences without the client as the authorized party.
func TestOidc_AnEntraClaimPolicyFailureIsARemoteFailure(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(f *fakeOIDC){
		"another tenant": func(f *fakeOIDC) { f.set("tid", fakeOtherTenantID) },
		"no object id":   func(f *fakeOIDC) { f.unset("oid") },
		"two audiences and no authorized party": func(f *fakeOIDC) {
			f.set("aud", []string{fakeEntraClientID, fakeOtherTenantID})
			f.unset("azp")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFakeOIDC(t)
			setup(f)
			h := newHarness(t, f.options()...)
			r := h.oidcCallback(t, h.client(t), f)
			assertOIDCRedirect(t, r, oidcErrorLocation("oidc_remote_failure"))
			if r.setCookie(identity.OIDCExternalCookieName) != nil {
				t.Error("a policy failure set the external identity cookie")
			}
		})
	}
}

// An id_token is judged on the harness clock with ASP.NET's five minutes of
// clock skew: a one-minute token is still accepted four minutes past its
// exp and refused six minutes past it, while the ten-minute state cookie is
// still good. The state cookie itself expires at ten. nbf keeps go-oidc's
// five minutes: four minutes ahead is accepted, six refused.
func TestOidc_AnExpiredIdTokenIsAnAuthenticationFailure(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.update(func(f *fakeOIDC) { f.tokenLifetime = time.Minute })
	h := newHarness(t, f.options()...)

	skewed := h.client(t)
	back := h.oidcCallbackLocation(t, skewed, f)
	h.advance(5 * time.Minute) // exp + 4 min
	assertOIDCRedirect(t, skewed.do(http.MethodGet, h.pathOf(t, back), nil), oidcCompletePath)

	late := h.client(t)
	back = h.oidcCallbackLocation(t, late, f)
	h.advance(7 * time.Minute) // exp + 6 min
	assertOIDCRedirect(t, late.do(http.MethodGet, h.pathOf(t, back), nil), oidcErrorLocation("oidc_authentication_failed"))

	stale := h.client(t)
	back = h.oidcCallbackLocation(t, stale, f)
	h.advance(10 * time.Minute)
	redemptions := len(f.redemptionForms())
	assertOIDCRedirect(t, stale.do(http.MethodGet, h.pathOf(t, back), nil), oidcErrorLocation("oidc_remote_failure"))
	if n := len(f.redemptionForms()); n != redemptions {
		t.Error("a stale state cookie's code was redeemed")
	}

	f.update(func(f *fakeOIDC) { f.tokenLifetime = time.Hour })
	f.set("nbf", h.now().Add(4*time.Minute).Unix())
	assertOIDCRedirect(t, h.oidcCallback(t, h.client(t), f), oidcCompletePath)
	f.set("nbf", h.now().Add(6*time.Minute).Unix())
	assertOIDCRedirect(t, h.oidcCallback(t, h.client(t), f), oidcErrorLocation("oidc_authentication_failed"))
}

// Discovery holds no lock across its fetch: two challenges while the
// provider's metadata hangs both reach the provider, neither queued behind
// the other, and both complete once it answers; the result is then cached.
func TestOidc_ConcurrentChallengesDoNotQueueBehindAHangingDiscovery(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	gate := make(chan struct{})
	f.update(func(f *fakeOIDC) { f.discoveryGate = gate })
	h := newHarness(t, f.options()...)
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release) // before the fake's own cleanup waits on its handlers

	clients := []*client{h.client(t), h.client(t)}
	results := make([]*resp, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.do(http.MethodGet, oidcChallengePath, nil)
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for f.discoveries() < len(clients) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	waiting := f.discoveries()
	release()
	wg.Wait()

	if waiting != len(clients) {
		t.Errorf("%d discovery requests reached the hanging provider, want %d: a challenge queued behind another", waiting, len(clients))
	}
	for _, r := range results {
		if r.status != http.StatusFound || !strings.HasPrefix(r.header("Location"), "https://login.microsoftonline.com/") {
			t.Errorf("challenge: status %d Location %q, want the provider", r.status, r.header("Location"))
		}
	}
	fetched := f.discoveries()
	h.oidcChallenge(t, h.client(t))
	if f.discoveries() != fetched {
		t.Error("the discovered metadata was not cached")
	}
}

// A server error in completion is a 500, as .NET's unhandled exception
// was, but it still consumes the external identity cookie.
func TestOidc_AServerErrorInCompletionStillClearsTheExternalCookie(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	c := h.client(t)
	assertOIDCRedirect(t, h.oidcCallback(t, c, f), oidcCompletePath)
	h.exec(t, `DROP TABLE identity.oidc_links`)

	// Off-contract by design: the contract documents no 500.
	r := c.do(http.MethodGet, oidcCompletePath, nil, skipContract("forced server error"))
	if r.status != http.StatusInternalServerError {
		t.Fatalf("complete: status %d, want 500", r.status)
	}
	assertCleared(t, r, identity.OIDCExternalCookieName, oidcCookiePath)
	if c.cookieAt(oidcCompletePath, identity.OIDCExternalCookieName) != "" {
		t.Error("the browser still holds the external identity after the 500")
	}
}

// A provider whose metadata cannot be fetched fails the challenge as a
// remote failure without the installation failing to start, and the
// failure is not cached. Metadata naming Entra's multi-tenant {tenantid}
// template rather than the configured tenant's issuer is refused.
func TestOidc_ADiscoveryFailureIsARemoteFailure(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.update(func(f *fakeOIDC) { f.discoveryDown = true })
	h := newHarness(t, f.options()...)
	c := h.client(t)
	r := c.do(http.MethodGet, oidcChallengePath, nil)
	assertOIDCRedirect(t, r, oidcErrorLocation("oidc_remote_failure"))
	if r.setCookie(identity.OIDCStateCookieName) != nil {
		t.Error("a failed challenge set the state cookie")
	}
	f.update(func(f *fakeOIDC) { f.discoveryDown = false })
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")

	templated := newFakeOIDC(t)
	templated.update(func(f *fakeOIDC) { f.discoveryIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0" })
	ht := newHarness(t, templated.options()...)
	assertOIDCRedirect(t, ht.client(t).do(http.MethodGet, oidcChallengePath, nil), oidcErrorLocation("oidc_remote_failure"))
}

// A replayed callback finds the state cookie spent. Replayed with the
// spent cookie captured and put back, the provider refuses the code it
// already redeemed. The first callback's identity completes exactly once.
func TestOidc_AReplayedCallbackIsRefused(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	c := h.client(t)

	authorize := h.oidcChallenge(t, c)
	captured := c.cookieAt(oidcCallbackPath, identity.OIDCStateCookieName)
	back := f.authorize(t, authorize)
	assertOIDCRedirect(t, c.do(http.MethodGet, h.pathOf(t, back), nil), oidcCompletePath)
	assertOIDCRedirect(t, c.do(http.MethodGet, h.pathOf(t, back), nil), oidcErrorLocation("oidc_remote_failure"))

	c.setCookieAt(oidcCookiePath, identity.OIDCStateCookieName, captured)
	assertOIDCRedirect(t, c.do(http.MethodGet, h.pathOf(t, back), nil), oidcErrorLocation("oidc_authentication_failed"))
	if n := len(f.redemptionForms()); n != 2 {
		t.Errorf("%d redemptions, want 2", n)
	}

	assertOIDCRedirect(t, c.do(http.MethodGet, oidcCompletePath, nil), "/")
	assertOIDCRedirect(t, c.do(http.MethodGet, oidcCompletePath, nil), oidcErrorLocation("oidc_external_identity_missing"))
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
}

// The external identity cookie lasts five minutes on the harness clock, is
// sealed (a tampered value is no identity) and bound to its purpose (a
// state cookie presented in its place is no identity either).
func TestOidc_TheExternalIdentityCookieIsSealedAndShortLived(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)

	expired := h.client(t)
	assertOIDCRedirect(t, h.oidcCallback(t, expired, f), oidcCompletePath)
	h.advance(5 * time.Minute)
	r := expired.do(http.MethodGet, oidcCompletePath, nil)
	assertOIDCRedirect(t, r, oidcErrorLocation("oidc_external_identity_missing"))
	assertCleared(t, r, identity.OIDCExternalCookieName, oidcCookiePath)

	tampered := h.client(t)
	assertOIDCRedirect(t, h.oidcCallback(t, tampered, f), oidcCompletePath)
	value := []byte(tampered.cookieAt(oidcCompletePath, identity.OIDCExternalCookieName))
	value[len(value)/2] ^= 0x01
	tampered.setCookieAt(oidcCookiePath, identity.OIDCExternalCookieName, string(value))
	assertOIDCRedirect(t, tampered.do(http.MethodGet, oidcCompletePath, nil), oidcErrorLocation("oidc_external_identity_missing"))

	crossed := h.client(t)
	h.oidcChallenge(t, crossed)
	crossed.setCookieAt(oidcCookiePath, identity.OIDCExternalCookieName, crossed.cookieAt(oidcCompletePath, identity.OIDCStateCookieName))
	assertOIDCRedirect(t, crossed.do(http.MethodGet, oidcCompletePath, nil), oidcErrorLocation("oidc_external_identity_missing"))

	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users, want 0", n)
	}
}

// An email that belongs to an account the identity is not linked to is
// never linked by: oidc_email_conflict, whatever the case, with no link
// made and the account untouched; account_locked when that account is
// unavailable.
func TestOidc_AnEmailTakenWithoutALinkIsAConflict(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.set("email", strings.ToUpper(fakeEmail))
	h := newHarness(t, f.options()...)
	local := h.seedUser(t, fakeEmail, userPassword, identity.RoleUserID)

	r := h.oidcSignIn(t, h.client(t), f)
	assertOIDCRedirect(t, r, oidcErrorLocation("oidc_email_conflict"))
	assertCleared(t, r, identity.OIDCExternalCookieName, oidcCookiePath)
	if r.setCookie(identity.SessionCookieName) != nil {
		t.Error("a conflict got a session cookie")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links`); n != 0 {
		t.Errorf("%d links, want 0", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want the local one only", n)
	}
	h.login(t, fakeEmail, userPassword)

	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, local)
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("account_locked"))
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links`); n != 0 {
		t.Errorf("%d links, want 0", n)
	}
}

// The link, never the email, finds the account: a second sign-in signs the
// same account in even after the provider's email changed, with no new
// user. Another subject is another identity, and the subject's case counts:
// one differing only in case, with the linked account's email, is a
// conflict, not that account.
func TestOidc_AnExistingLinkSignsInWithoutANewUser(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	first := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, first, f), "/")
	id := oidcSession(t, first).User.ID

	f.set("email", "renamed@example.test")
	second := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, second, f), "/")
	if s := oidcSession(t, second); s.User.ID != id || s.User.Email == nil || *s.User.Email != fakeEmail {
		t.Errorf("second sign-in = %+v, want the linked account %s unchanged", s.User, id)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != 2 {
		t.Errorf("%d sessions, want 2", n)
	}

	f.set("sub", "workforce-subject-2")
	f.set("email", "second@example.test")
	third := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, third, f), "/")
	if oidcSession(t, third).User.ID == id {
		t.Error("another subject signed the first account in")
	}

	f.set("sub", strings.ToUpper(fakeSubject))
	f.set("email", fakeEmail)
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), oidcErrorLocation("oidc_email_conflict"))
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links`); n != 2 {
		t.Errorf("%d links, want 2", n)
	}
}

// An account with TOTP is never signed in by OIDC alone: external claims
// are no local second factor. local_mfa_required, and no session.
func TestOidc_ALinkedAccountWithTotpNeedsItsLocalSecondFactor(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	const email = "totp.user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	h.enrollTOTP(t, h.login(t, email, userPassword), userPassword)
	insertOIDCLink(t, h, fakeEntraAuthority, fakeSubject, id)
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	r := h.oidcSignIn(t, h.client(t), f)
	assertOIDCRedirect(t, r, oidcErrorLocation("local_mfa_required"))
	assertCleared(t, r, identity.OIDCExternalCookieName, oidcCookiePath)
	if r.setCookie(identity.SessionCookieName) != nil {
		t.Error("a TOTP account got a session cookie")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("sessions went from %d to %d", sessions, n)
	}
}

// Two completions for one new subject racing: exactly one account and one
// link, and both browsers signed in to it, the loser through the link the
// winner committed. Run it with -count=10.
func TestOidc_ConcurrentCompletionsForOneNewSubjectProvisionOneUser(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	clients := []*client{h.client(t), h.client(t)}
	for _, c := range clients {
		assertOIDCRedirect(t, h.oidcCallback(t, c, f), oidcCompletePath)
	}

	results := make([]*resp, len(clients))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = c.do(http.MethodGet, oidcCompletePath, nil)
		}()
	}
	close(start)
	wg.Wait()

	for _, r := range results {
		assertOIDCRedirect(t, r, "/")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE normalized_email = upper($1)`, fakeEmail); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.oidc_links`); n != 1 {
		t.Errorf("%d links, want 1", n)
	}
	if a, b := oidcSession(t, clients[0]).User.ID, oidcSession(t, clients[1]).User.ID; a != b {
		t.Errorf("the two sessions are for %s and %s", a, b)
	}
}

// ssoEnabled counts only a link under the configured issuer, as .NET
// compared LoginProvider with the normalized authority: a link left by
// another tenant does not count, and with OIDC off no link does.
func TestOidc_SsoEnabledCountsOnlyTheConfiguredIssuersLinks(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	linked := oidcSession(t, c).User.ID
	elsewhere := h.insertUser(t, "elsewhere@example.test", identity.RoleUserID)
	insertOIDCLink(t, h, "https://login.microsoftonline.com/"+fakeOtherTenantID+"/v2.0", fakeSubject, elsewhere)

	sso := map[uuid.UUID]bool{}
	for _, u := range ownerUsers(t, owner) {
		sso[u.ID] = u.SsoEnabled
	}
	if !sso[linked] || sso[elsewhere] {
		t.Errorf("ssoEnabled: configured issuer %v, another tenant %v; want true, false", sso[linked], sso[elsewhere])
	}

	off := newHarness(t)
	offOwner, _ := off.bootstrapOwner(t)
	u := off.insertUser(t, "linked@example.test", identity.RoleUserID)
	insertOIDCLink(t, off, fakeEntraAuthority, fakeSubject, u)
	for _, listed := range ownerUsers(t, offOwner) {
		if listed.SsoEnabled {
			t.Errorf("user %s is ssoEnabled with OIDC off", listed.ID)
		}
	}
}

// /providers reports the configured provider's display name.
func TestOidc_ProvidersReportTheConfiguredProvidersDisplayName(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("OIDC_DISPLAY_NAME", "Contoso SSO"))...)
	r := h.client(t).do(http.MethodGet, "/api/v1/identity/providers", nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"oidc":{"displayName":"Contoso SSO"}}` {
		t.Errorf("status %d body %s", r.status, r.body)
	}
}

// The POST callback reads the form exactly as the GET reads the query, for
// a same-origin post. A provider's form_post is a cross-site POST, which
// the platform's CrossOriginProtection refuses before identity sees it:
// the limitation PostIdentityOidcCallback documents, and why challenge
// asks for response_mode=query.
func TestOidc_ThePostCallbackServesSameOriginFormPosts(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	c := h.client(t)
	back, err := url.Parse(h.oidcCallbackLocation(t, c, f))
	if err != nil {
		t.Fatal(err)
	}
	form := func(state string) []byte {
		return []byte(url.Values{"code": {back.Query().Get("code")}, "state": {state}}.Encode())
	}
	const formType = "application/x-www-form-urlencoded"

	// Off-contract by design: the platform's 403 is not in the contract.
	r := c.do(http.MethodPost, oidcCallbackPath, nil, rawBody(formType, form(back.Query().Get("state"))),
		origin("https://login.microsoftonline.com"), header("Sec-Fetch-Site", "cross-site"), skipContract("CSRF probe"))
	if r.status != http.StatusForbidden || len(f.redemptionForms()) != 0 {
		t.Fatalf("cross-site form_post: status %d with %d redemptions, want 403 and none", r.status, len(f.redemptionForms()))
	}

	r = c.do(http.MethodPost, oidcCallbackPath, nil, rawBody(formType, form(back.Query().Get("state"))),
		origin(harnessOrigin), header("Sec-Fetch-Site", "same-origin"))
	assertOIDCRedirect(t, r, oidcCompletePath)
	assertCleared(t, r, identity.OIDCStateCookieName, oidcCookiePath)
	assertLaxCookie(t, r, identity.OIDCExternalCookieName, oidcCookiePath, 300)
	assertOIDCRedirect(t, c.do(http.MethodGet, oidcCompletePath, nil), "/")

	forger := h.client(t)
	forged, err := url.Parse(h.oidcCallbackLocation(t, forger, f))
	if err != nil {
		t.Fatal(err)
	}
	r = forger.do(http.MethodPost, oidcCallbackPath, nil,
		rawBody(formType, []byte(url.Values{"code": {forged.Query().Get("code")}, "state": {"forged"}}.Encode())),
		origin(harnessOrigin), header("Sec-Fetch-Site", "same-origin"))
	assertOIDCRedirect(t, r, oidcErrorLocation("oidc_remote_failure"))
}

// Under a base path, the redirect URI, both cookies' paths and every
// redirect carry it.
func TestOidc_TheFlowLivesUnderTheBasePath(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("APP_BASE_PATH", "/vantigo"))...)
	// Off-contract by necessity: the contract's paths carry no base path, so
	// these exchanges cannot be matched to it. Every other OIDC test's are.
	skip := skipContract("the contract's paths carry no base path")
	const base = "/vantigo"
	c := h.client(t)

	challenge := c.do(http.MethodGet, base+oidcChallengePath, nil, skip)
	if challenge.status != http.StatusFound {
		t.Fatalf("challenge: status %d body %s", challenge.status, challenge.body)
	}
	authorize, err := url.Parse(challenge.header("Location"))
	if err != nil || authorize.Query().Get("redirect_uri") != harnessOrigin+base+oidcCallbackPath {
		t.Fatalf("challenge: Location %q, want the base path's callback as redirect_uri", challenge.header("Location"))
	}
	assertLaxCookie(t, challenge, identity.OIDCStateCookieName, base+oidcCookiePath, 600)

	callback := c.do(http.MethodGet, h.pathOf(t, f.authorize(t, authorize)), nil, skip)
	assertOIDCRedirect(t, callback, base+oidcCompletePath)
	assertCleared(t, callback, identity.OIDCStateCookieName, base+oidcCookiePath)
	assertLaxCookie(t, callback, identity.OIDCExternalCookieName, base+oidcCookiePath, 300)

	complete := c.do(http.MethodGet, base+oidcCompletePath, nil, skip)
	assertOIDCRedirect(t, complete, base+"/")
	assertCleared(t, complete, identity.OIDCExternalCookieName, base+oidcCookiePath)
	if sc := complete.setCookie(identity.SessionCookieName); sc == nil || sc.Path != base {
		t.Errorf("session cookie = %+v, want path %s", sc, base)
	}

	r := h.client(t).do(http.MethodGet, base+oidcCompletePath, nil, skip)
	assertOIDCRedirect(t, r, base+oidcErrorLocation("oidc_external_identity_missing"))
	assertCleared(t, r, identity.OIDCExternalCookieName, base+oidcCookiePath)
}

// Ported from StaticOidcSecurityTests.WorkloadAssertionIsReadFreshAndNeverSendsAClientSecret,
// end to end: each redemption carries the token file's content as it is at
// that moment, as a jwt-bearer client assertion, and no client secret. An
// unusable token file fails before anything is sent.
func TestOidc_TheWorkloadAssertionIsReadForEveryRedemptionAndNoSecretIsSent(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.update(func(f *fakeOIDC) { f.secret = "" })
	tokenFile := filepath.Join(t.TempDir(), "workload-token")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(tokenFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first-assertion\n")
	h := newHarness(t, append(f.options(), withEnv("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", tokenFile))...)

	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	write("second-assertion")
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
	forms := f.redemptionForms()
	if len(forms) != 2 {
		t.Fatalf("%d redemptions, want 2", len(forms))
	}
	for i, want := range []string{"first-assertion", "second-assertion"} {
		if form := forms[i]; form.Get("client_assertion") != want || form.Get("client_assertion_type") != jwtBearer || form.Has("client_secret") {
			t.Errorf("redemption %d form %v, want assertion %q and no secret", i, form, want)
		}
	}

	write(" \n")
	assertOIDCRedirect(t, h.oidcCallback(t, h.client(t), f), oidcErrorLocation("oidc_remote_failure"))
	if n := len(f.redemptionForms()); n != 2 {
		t.Errorf("%d redemptions, want 2: nothing is sent without an assertion", n)
	}
	assertNoSecretsLogged(t, h, f, "first-assertion", "second-assertion")
}

// Google: a verified email in an allowed hosted domain signs in; an
// unverified email, a hosted domain that is not the email's, one that is
// not allowed, or none, is a remote failure (the policy); and Google's
// scheme-less issuer, which go-oidc tolerates, is an authentication failure,
// as ASP.NET's exact issuer validation refused it.
func TestOidc_GoogleSignInRequiresAVerifiedAllowedHostedDomain(t *testing.T) {
	t.Parallel()
	t.Run("an allowed hosted domain", func(t *testing.T) {
		t.Parallel()
		f := newFakeGoogleOIDC(t)
		h := newHarness(t, f.options()...)
		c := h.client(t)
		assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
		if s := oidcSession(t, c); s.User.Email == nil || *s.User.Email != fakeGoogleEmail {
			t.Errorf("session = %+v", s.User)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.oidc_links WHERE issuer = $1 AND subject = $2`, fakeGoogleAuthority, fakeSubject); n != 1 {
			t.Errorf("%d links, want 1", n)
		}
	})
	for name, tc := range map[string]struct {
		setup func(f *fakeOIDC)
		code  string
	}{
		"an unverified email":             {func(f *fakeOIDC) { f.set("email_verified", false) }, "oidc_remote_failure"},
		"a hosted domain not the email's": {func(f *fakeOIDC) { f.set("hd", "other.example") }, "oidc_remote_failure"},
		"a hosted domain not allowed": {func(f *fakeOIDC) {
			f.set("email", "person@other.example")
			f.set("hd", "other.example")
		}, "oidc_remote_failure"},
		"no hosted domain":        {func(f *fakeOIDC) { f.unset("hd") }, "oidc_remote_failure"},
		"the scheme-less issuer":  {func(f *fakeOIDC) { f.set("iss", "accounts.google.com") }, "oidc_authentication_failed"},
		"a string email_verified": {func(f *fakeOIDC) { f.set("email_verified", "false") }, "oidc_remote_failure"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFakeGoogleOIDC(t)
			tc.setup(f)
			h := newHarness(t, f.options()...)
			r := h.oidcCallback(t, h.client(t), f)
			assertOIDCRedirect(t, r, oidcErrorLocation(tc.code))
			if r.setCookie(identity.OIDCExternalCookieName) != nil {
				t.Error("a refused identity set the external identity cookie")
			}
		})
	}
}

// An identity without an email gets the opaque reserved address (hex
// SHA-256 of issuer, NUL, subject), unconfirmed and shown as null, and a
// display name from the given and family names. It signs in again through
// its link.
func TestOidc_AnIdentityWithoutAnEmailGetsTheOpaqueAddress(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.unset("email")
	f.unset("email_verified")
	f.unset("name")
	f.set("given_name", "Ada")
	f.set("family_name", "Lovelace")
	h := newHarness(t, f.options()...)

	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	s := oidcSession(t, c)
	if s.User.Email != nil || s.User.DisplayName != "Ada Lovelace" {
		t.Errorf("session user = %+v, want a null email and Ada Lovelace", s.User)
	}
	sum := sha256.Sum256([]byte(fakeEntraAuthority + "\x00" + fakeSubject))
	opaque := "oidc-" + hex.EncodeToString(sum[:]) + "@sso.invalid"
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = $2 AND NOT email_confirmed`, s.User.ID, opaque); n != 1 {
		t.Errorf("the account does not have the unconfirmed opaque address %s", opaque)
	}

	again := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, again, f), "/")
	if oidcSession(t, again).User.ID != s.User.ID {
		t.Error("the second sign-in reached another account")
	}
}

// An Owner linked to an identity signs in through OIDC without MFA, as in
// .NET, whose completion refused no role: the session is not MFA-verified,
// and while OWNERS_REQUIRE_MFA holds, the Owner's privileged endpoints
// refuse it until a second factor is verified.
func TestOidc_AnOwnerSignsInWithoutMfaAndPrivilegedAccessStillRequiresIt(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("OWNERS_REQUIRE_MFA", "1"))...)
	_, ownerID := h.bootstrapOwner(t)
	insertOIDCLink(t, h, fakeEntraAuthority, fakeSubject, ownerID)

	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	s := oidcSession(t, c)
	if s.User.ID != ownerID || s.MfaAuthenticated || !s.MfaEnrollmentRequired {
		t.Errorf("session = %+v, want the Owner, without MFA, told to enrol", s)
	}
	if r := c.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusForbidden {
		t.Errorf("owner users: status %d, want 403", r.status)
	}
}
