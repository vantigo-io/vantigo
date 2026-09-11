package identity_test

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The fake provider's fixed values: .NET's test tenant, client and object
// ids (TS/Integration/IdentityApiFactory.cs, its OIDC settings and its
// DeterministicOidcBackchannelHandler), a client secret, a Google client,
// and the identity the provider signs in by default.
const (
	fakeTenantID        = "00000000-0000-0000-0000-000000000000"
	fakeOtherTenantID   = "33333333-3333-3333-3333-333333333333"
	fakeEntraClientID   = "11111111-1111-1111-1111-111111111111"
	fakeObjectID        = "22222222-2222-2222-2222-222222222222"
	fakeClientSecret    = "fake-oidc-client-secret-7f3a9c"
	fakeGoogleClientID  = "vantigo-test.apps.googleusercontent.com"
	fakeEntraAuthority  = "https://login.microsoftonline.com/" + fakeTenantID + "/v2.0"
	fakeGoogleAuthority = "https://accounts.google.com"
	fakeSubject         = "workforce-subject-1"
	fakeEmail           = "workforce.user@example.test"
	fakeGoogleEmail     = "person@example.com"
	fakeDisplayName     = "Workforce User"
	fakeSigningKeyID    = "fake-oidc-rsa"
	jwtBearer           = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
)

// The OIDC operations' paths, without the base path.
const (
	oidcChallengePath = "/api/v1/identity/oidc/challenge"
	oidcCallbackPath  = "/api/v1/identity/oidc/callback"
	oidcCompletePath  = "/api/v1/identity/oidc/complete"
	oidcCookiePath    = "/api/v1/identity/oidc"
)

// oidcErrorLocation is where a failed OIDC step sends the browser.
func oidcErrorLocation(code string) string { return "/sign-in?error=" + code }

// fakeSigningKey is the one RSA key every fake provider signs with,
// generated once, since generating it is the fake's only slow step.
var fakeSigningKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// foreignSigningKey is an RSA key the provider never published, for
// forged id_tokens.
var foreignSigningKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// fakeOIDC is a workforce OIDC provider for one test, as .NET's
// DeterministicOidcBackchannelHandler was (TS/Integration/IdentityApiFactory.cs:501):
// discovery, a JWKS with one RSA key, an authorization endpoint that signs
// the user straight in, redirecting back with a code and the state it was
// given, and a token endpoint that checks the redemption (the code, once;
// the redirect URI; the client; PKCE; the client secret or assertion),
// records its form, and mints an RS256 id_token.
//
// It answers under the configured authority's real host: the installation
// reaches it through httpClient, which sends every request to the fake's
// listener whatever its URL says, so the issuer stays the exact
// https://login.microsoftonline.com/<tenant>/v2.0 config demands. A test
// follows the browser's hop to the authorization endpoint with authorize.
//
// The id_token's claims are the provider's defaults (iss, aud, sub, iat,
// exp, nonce, name; tid, oid, azp and a verified email for Entra; a
// verified email and hd for Google), with every claim a test set over them.
// iat is when the user authorized, on the harness clock, and exp is
// tokenLifetime later.
type fakeOIDC struct {
	provider  string // "entra" | "google"
	authority string
	clientID  string
	secret    string // "" for workload identity
	srv       *httptest.Server

	mu              sync.Mutex
	now             func() time.Time
	discoveryIssuer string
	discoveryDown   bool
	discoveryGate   chan struct{} // when set, discovery answers once it is closed
	discoveryCount  int
	forge           func(header map[string]string, payload []byte) string
	claims          map[string]any
	tokenLifetime   time.Duration
	tokenError      string
	authorizeError  string
	grants          map[string]fakeGrant
	authorizations  []url.Values
	redemptions     []url.Values
	issued          []string // every code, access token and id_token handed out
}

// fakeGrant is one authorization: the authorize request, and when it was.
type fakeGrant struct {
	query url.Values
	at    time.Time
}

// newFakeOIDC is an Entra provider with a client secret.
func newFakeOIDC(t testing.TB) *fakeOIDC {
	return startFakeOIDC(t, "entra", fakeEntraAuthority, fakeEntraClientID)
}

// newFakeGoogleOIDC is a Google provider with a client secret, whose users
// are in the hosted domain example.com.
func newFakeGoogleOIDC(t testing.TB) *fakeOIDC {
	return startFakeOIDC(t, "google", fakeGoogleAuthority, fakeGoogleClientID)
}

func startFakeOIDC(t testing.TB, provider, authority, clientID string) *fakeOIDC {
	t.Helper()
	f := &fakeOIDC{
		provider:        provider,
		authority:       authority,
		clientID:        clientID,
		secret:          fakeClientSecret,
		now:             time.Now,
		discoveryIssuer: authority,
		claims:          map[string]any{},
		tokenLifetime:   time.Hour,
		grants:          map[string]fakeGrant{},
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

// options configure a harness for this provider: the OIDC_* settings and
// the route to the fake. A test adds to them (a base path, a workload
// identity token file) as it needs.
func (f *fakeOIDC) options() []harnessOption {
	opts := []harnessOption{
		withFakeOIDC(f),
		withEnv("OIDC_PROVIDER", f.provider),
		withEnv("OIDC_AUTHORITY", f.authority),
		withEnv("OIDC_CLIENT_ID", f.clientID),
	}
	if f.secret != "" {
		opts = append(opts, withEnv("OIDC_CLIENT_SECRET", f.secret))
	}
	if f.provider == "google" {
		opts = append(opts, withEnv("OIDC_ALLOWED_DOMAINS", "example.com"))
	}
	return opts
}

// withFakeOIDC routes the installation's OIDC traffic to f, and puts f on
// the harness clock.
func withFakeOIDC(f *fakeOIDC) harnessOption {
	return func(s *harnessSetup) { s.oidc = f }
}

// attach is withFakeOIDC's half inside newHarness, once the Access exists.
func (f *fakeOIDC) attach(a *identity.Access, now func() time.Time) {
	identity.SetOIDCHTTPClient(a, f.httpClient())
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// set makes claim v in every id_token minted from now on; unset leaves the
// claim out.
func (f *fakeOIDC) set(claim string, v any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims[claim] = v
}

func (f *fakeOIDC) unset(claim string) { f.set(claim, nil) }

// update changes the fake's behaviour under its lock.
func (f *fakeOIDC) update(change func(f *fakeOIDC)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// httpClient is the installation's client for the provider: every request
// goes to the fake's listener, with its Host kept.
func (f *fakeOIDC) httpClient() *http.Client {
	return &http.Client{Transport: fakeOIDCTransport{f}}
}

type fakeOIDCTransport struct{ f *fakeOIDC }

func (rt fakeOIDCTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	routed := r.Clone(r.Context())
	routed.URL.Scheme = "http"
	routed.URL.Host = rt.f.srv.Listener.Addr().String()
	routed.Host = r.URL.Host
	return rt.f.srv.Client().Transport.RoundTrip(routed)
}

// authorize is the browser's visit to the authorization endpoint the
// challenge redirected to: the fake signs the user in and answers with its
// redirect back, whose location it returns.
func (f *fakeOIDC) authorize(t testing.TB, location *url.URL) string {
	t.Helper()
	c := &http.Client{
		Transport:     fakeOIDCTransport{f},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := c.Get(location.String())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorize %s: status %d", location, res.StatusCode)
	}
	return res.Header.Get("Location")
}

// discoveries is how many discovery requests the fake has received, the
// ones still held at discoveryGate included.
func (f *fakeOIDC) discoveries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discoveryCount
}

// redemptionForms is the form of every token request, in order.
func (f *fakeOIDC) redemptionForms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.redemptions...)
}

// secretValues is every value the installation must never log: the client
// secret, and each code, token, state, nonce, PKCE verifier and client
// assertion the flow carried.
func (f *fakeOIDC) secretValues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := append([]string{}, f.issued...)
	if f.secret != "" {
		values = append(values, f.secret)
	}
	for _, q := range f.authorizations {
		values = append(values, q.Get("state"), q.Get("nonce"), q.Get("code_challenge"))
	}
	for _, form := range f.redemptions {
		values = append(values, form.Get("code_verifier"), form.Get("client_assertion"))
	}
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (f *fakeOIDC) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authority, err := url.Parse(f.authority)
	if err != nil || r.Host != authority.Host {
		http.NotFound(w, r)
		return
	}
	base := authority.Path
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base+"/.well-known/openid-configuration":
		f.discovery(w)
	case r.Method == http.MethodGet && r.URL.Path == base+"/discovery/v2.0/keys":
		f.keys(w)
	case r.Method == http.MethodGet && r.URL.Path == base+"/oauth2/v2.0/authorize":
		f.authorizeEndpoint(w, r)
	case r.Method == http.MethodPost && r.URL.Path == base+"/oauth2/v2.0/token":
		f.tokenEndpoint(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOIDC) discovery(w http.ResponseWriter) {
	f.mu.Lock()
	f.discoveryCount++
	down, issuer, gate := f.discoveryDown, f.discoveryIssuer, f.discoveryGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                f.authority + "/oauth2/v2.0/authorize",
		"token_endpoint":                        f.authority + "/oauth2/v2.0/token",
		"jwks_uri":                              f.authority + "/discovery/v2.0/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "private_key_jwt"},
	})
}

func (f *fakeOIDC) keys(w http.ResponseWriter) {
	pub := fakeSigningKey().PublicKey
	writeFakeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": fakeSigningKeyID,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (f *fakeOIDC) authorizeEndpoint(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizations = append(f.authorizations, q)
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || q.Get("response_type") != "code" || q.Get("client_id") != f.clientID ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" {
		http.Error(w, "invalid authorization request", http.StatusBadRequest)
		return
	}
	v := back.Query()
	v.Set("state", q.Get("state"))
	if f.authorizeError != "" {
		v.Set("error", f.authorizeError)
	} else {
		code := "fake-code-" + rand.Text()
		f.grants[code] = fakeGrant{query: q, at: f.now()}
		f.issued = append(f.issued, code)
		v.Set("code", code)
	}
	back.RawQuery = v.Encode()
	w.Header().Set("Location", back.String())
	w.WriteHeader(http.StatusFound)
}

func (f *fakeOIDC) tokenEndpoint(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	form := r.PostForm
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redemptions = append(f.redemptions, form)
	if f.tokenError != "" {
		writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": f.tokenError})
		return
	}
	code := form.Get("code")
	grant, ok := f.grants[code]
	delete(f.grants, code) // a code is redeemed at most once
	challenge := sha256.Sum256([]byte(form.Get("code_verifier")))
	if !ok || form.Get("grant_type") != "authorization_code" ||
		form.Get("redirect_uri") != grant.query.Get("redirect_uri") ||
		form.Get("client_id") != f.clientID || form.Get("code_verifier") == "" ||
		b64(challenge[:]) != grant.query.Get("code_challenge") {
		writeFakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	authenticated := form.Get("client_assertion_type") == jwtBearer && form.Get("client_assertion") != "" && !form.Has("client_secret")
	if f.secret != "" {
		authenticated = form.Get("client_secret") == f.secret && !form.Has("client_assertion")
	}
	if !authenticated {
		writeFakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	access := "fake-access-" + rand.Text()
	idToken := f.mint(grant)
	f.issued = append(f.issued, access, idToken)
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
	})
}

// mint signs the id_token for grant, or hands its header and payload to
// forge when a test set one, for a token the provider never signed. f.mu
// is held.
func (f *fakeOIDC) mint(grant fakeGrant) string {
	claims := map[string]any{
		"iss":   f.authority,
		"aud":   f.clientID,
		"sub":   fakeSubject,
		"iat":   grant.at.Unix(),
		"exp":   grant.at.Add(f.tokenLifetime).Unix(),
		"nonce": grant.query.Get("nonce"),
		"name":  fakeDisplayName,
	}
	switch f.provider {
	case "entra":
		claims["tid"], claims["oid"], claims["azp"] = fakeTenantID, fakeObjectID, f.clientID
		claims["email"], claims["email_verified"] = fakeEmail, true
	case "google":
		claims["email"], claims["email_verified"], claims["hd"] = fakeGoogleEmail, true, "example.com"
	}
	for k, v := range f.claims {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": fakeSigningKeyID}
	payload, _ := json.Marshal(claims)
	if f.forge != nil {
		return f.forge(header, payload)
	}
	return signRS256(fakeSigningKey(), header, payload)
}

// signingInput is a JWS's header and payload, base64url, as it is signed.
func signingInput(header map[string]string, payload []byte) string {
	h, _ := json.Marshal(header)
	return b64(h) + "." + b64(payload)
}

// signRS256 is a compact JWS over header and payload, signed with key.
func signRS256(key *rsa.PrivateKey, header map[string]string, payload []byte) string {
	signed := signingInput(header, payload)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return signed + "." + b64(signature)
}

// The forged id_tokens fakeOIDC.forge can mint, each one the provider's
// JWKS must not verify.

// forgeAlgNone is an unsigned token: alg "none", no signature.
func forgeAlgNone(header map[string]string, payload []byte) string {
	header["alg"] = "none"
	return signingInput(header, payload) + "."
}

// forgeHS256WithThePublicKey is the key-confusion forgery: HS256 keyed with
// the provider's public RSA key, as PEM, which a verifier that trusts the
// token's alg would check against that same public key.
func forgeHS256WithThePublicKey(header map[string]string, payload []byte) string {
	der, err := x509.MarshalPKIXPublicKey(&fakeSigningKey().PublicKey)
	if err != nil {
		panic(err)
	}
	header["alg"] = "HS256"
	signed := signingInput(header, payload)
	mac := hmac.New(sha256.New, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	_, _ = mac.Write([]byte(signed))
	return signed + "." + b64(mac.Sum(nil))
}

// forgeWithAForeignKey signs with a key the provider never published, under
// the provider's own kid.
func forgeWithAForeignKey(header map[string]string, payload []byte) string {
	return signRS256(foreignSigningKey(), header, payload)
}

// forgeWithAnUnknownKid signs with the provider's real key but names a kid
// its JWKS does not have.
func forgeWithAnUnknownKid(header map[string]string, payload []byte) string {
	header["kid"] = "unknown-kid"
	return signRS256(fakeSigningKey(), header, payload)
}

func writeFakeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// oidcChallenge starts a sign-in on c, which must redirect to the
// provider's authorization endpoint, and returns that URL.
func (h *harness) oidcChallenge(t testing.TB, c *client, opts ...reqOpt) *url.URL {
	t.Helper()
	r := c.do(http.MethodGet, h.cfg.BasePath+oidcChallengePath, nil, opts...)
	if r.status != http.StatusFound {
		t.Fatalf("challenge: status %d body %s", r.status, r.body)
	}
	u, err := url.Parse(r.header("Location"))
	if err != nil || u.Host == "" {
		t.Fatalf("challenge: Location %q is not the provider's", r.header("Location"))
	}
	return u
}

// oidcCallbackLocation starts a sign-in on c and signs in at f, and
// returns the provider's redirect back to the callback.
func (h *harness) oidcCallbackLocation(t testing.TB, c *client, f *fakeOIDC, opts ...reqOpt) string {
	t.Helper()
	return f.authorize(t, h.oidcChallenge(t, c, opts...))
}

// oidcCallback starts a sign-in on c, signs in at f, and returns the
// callback's answer.
func (h *harness) oidcCallback(t testing.TB, c *client, f *fakeOIDC, opts ...reqOpt) *resp {
	t.Helper()
	return c.do(http.MethodGet, h.pathOf(t, h.oidcCallbackLocation(t, c, f, opts...)), nil, opts...)
}

// oidcSignIn runs the whole flow on c against f, asserting that callback
// redirects to complete, and returns complete's answer.
func (h *harness) oidcSignIn(t testing.TB, c *client, f *fakeOIDC, opts ...reqOpt) *resp {
	t.Helper()
	callback := h.oidcCallback(t, c, f, opts...)
	if want := h.cfg.BasePath + oidcCompletePath; callback.status != http.StatusFound || callback.header("Location") != want {
		t.Fatalf("callback: status %d Location %q, want 302 %s", callback.status, callback.header("Location"), want)
	}
	return c.do(http.MethodGet, callback.header("Location"), nil, opts...)
}

// pathOf is location's path and query, for a location on the
// installation.
func (h *harness) pathOf(t testing.TB, location string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(location, h.url)
	if !ok || !strings.HasPrefix(rest, "/") {
		t.Fatalf("location %q is not on the installation %s", location, h.url)
	}
	return rest
}

// cookieAt is the value of the client's cookie name as a request to path
// would send it, or "" without one.
func (c *client) cookieAt(path, name string) string {
	u := *c.h.base
	u.Path = path
	for _, ck := range c.http.Jar.Cookies(&u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// setCookieAt puts a cookie in the client's jar as the installation would
// have set it at path.
func (c *client) setCookieAt(path, name, value string) {
	u := *c.h.base
	u.Path = path
	c.http.Jar.SetCookies(&u, []*http.Cookie{{Name: name, Value: value, Path: path}})
}
