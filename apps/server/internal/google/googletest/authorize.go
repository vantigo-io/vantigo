package googletest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// This file is the fake's authorization server for the person's own Google
// connection (Google Workspace design G26–G28, delivery D): what
// accounts.google.com, oauth2.googleapis.com and www.googleapis.com answer
// the organisation's Internal web client.
//
//   - GET accounts.google.com/.well-known/openid-configuration is Google's
//     discovery document, and GET www.googleapis.com/oauth2/v3/certs the
//     fake's one RSA signing key as a JWK set, made at the first need.
//   - IssueCode stands in for /o/oauth2/v2/auth, which a test never calls: it
//     answers the code Google's redirect would carry for what the
//     authorizeUrl asked and what the person consented to. The
//     authorization_code grant checks the web client and its secret, the
//     redirect URI, the PKCE verifier against the S256 challenge, a code's
//     single use and its minute of life on the fake's clock; it answers an
//     access token, an RS256 ID token carrying sub, email and hd, a refresh
//     token and scope as granted — granular consent may grant less than was
//     asked, in Google's spelling.
//   - The refresh_token grant answers a new access token and no new refresh
//     token, as Google does, with the grant's scope; RefuseRefresh queues a
//     refusal of one token, NarrowRefresh changes what a refresh answers,
//     RefreshCalls counts.
//   - POST oauth2.googleapis.com/revoke forgets a refresh token: 200, or 400
//     invalid_token for one it does not know.
//
// Every refusal is the token endpoint's { error, error_description }, the
// description Google's words a test proves are never repeated.

// AccountsHost is accounts.google.com, which serves the discovery document.
const AccountsHost = "accounts.google.com"

// Issuer is the ID token's issuer.
const Issuer = "https://accounts.google.com"

// codeLifetime is how long an issued code may be redeemed.
const codeLifetime = time.Minute

// signingKeyID is the kid of the fake's signing key.
const signingKeyID = "googletest-rs256"

// CodeGrant is what the authorization endpoint would have been asked and
// what the person consented to: the web client and its redirect URI, the
// PKCE challenge (S256), the scopes asked and the nonce, and the account
// signed in — its sub, address and hd (empty for a consumer account).
// Granted is the scope the token answer reports; nil grants what was asked.
type CodeGrant struct {
	ClientID    string
	RedirectURI string
	Challenge   string
	Scopes      []string
	Nonce       string
	Sub         string
	Email       string
	HD          string
	Granted     []string
}

// GrantFor is the grant the authorization URL authorize asks, for the
// account sub with email and hd, granting all it asked.
func GrantFor(authorize *url.URL, sub, email, hd string) CodeGrant {
	q := authorize.Query()
	return CodeGrant{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), Challenge: q.Get("code_challenge"),
		Scopes: strings.Fields(q.Get("scope")), Nonce: q.Get("nonce"),
		Sub: sub, Email: email, HD: hd,
	}
}

// issuedCode is one code: its grant, when it was issued and whether it was
// redeemed.
type issuedCode struct {
	grant  CodeGrant
	issued time.Time
	used   bool
}

// authState is the fake's authorization server's memory, under the fake's
// lock.
type authState struct {
	key          *rsa.PrivateKey
	codes        map[string]*issuedCode
	refreshes    map[string]CodeGrant
	refusals     map[string][]string
	revoked      []string
	refreshCalls int
	idTokenFor   func(claims map[string]any) bool
}

// AddWebClient makes the token endpoint know the web client id with secret.
func (g *FakeGoogle) AddWebClient(id, secret string) {
	g.Configure(func(g *FakeGoogle) {
		if g.WebClients == nil {
			g.WebClients = map[string]string{}
		}
		g.WebClients[id] = secret
	})
}

// IssueCode is the code the authorization endpoint would answer for grant;
// it is redeemable once, for codeLifetime on the fake's clock.
func (g *FakeGoogle) IssueCode(grant CodeGrant) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.auth.codes == nil {
		g.auth.codes = map[string]*issuedCode{}
	}
	code := "4/0A" + randomText(48)
	g.auth.codes[code] = &issuedCode{grant: grant, issued: g.now()}
	return code
}

// IssueRefresh is a refresh token for grant as a completed connect would
// have left it, without the authorization step: for a test that seeds a
// person's connection directly. Granted must be set.
func (g *FakeGoogle) IssueRefresh(grant CodeGrant) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.auth.refreshes == nil {
		g.auth.refreshes = map[string]CodeGrant{}
	}
	refresh := "1//0g" + randomText(72)
	g.auth.refreshes[refresh] = grant
	return refresh
}

// IDTokenFor sets how every later ID token is made: fn is handed the claims
// the fake would sign and may change them — another iss, aud, nonce or hd —
// and answers whether they are signed with the fake's published key (true)
// or a key nobody published (false). Nil restores the faithful token.
func (g *FakeGoogle) IDTokenFor(fn func(claims map[string]any) bool) {
	g.Configure(func(g *FakeGoogle) { g.auth.idTokenFor = fn })
}

// RefuseRefresh queues a refusal of token's next refresh with the OAuth
// error code. Each call queues one more.
func (g *FakeGoogle) RefuseRefresh(token, code string) {
	g.Configure(func(g *FakeGoogle) {
		if g.auth.refusals == nil {
			g.auth.refusals = map[string][]string{}
		}
		g.auth.refusals[token] = append(g.auth.refusals[token], code)
	})
}

// NarrowRefresh makes token's later refreshes answer granted as their scope,
// as a person removing a grant in their Google account would.
func (g *FakeGoogle) NarrowRefresh(token string, granted ...string) {
	g.Configure(func(g *FakeGoogle) {
		grant := g.auth.refreshes[token]
		grant.Granted = slices.Clone(granted)
		g.auth.refreshes[token] = grant
	})
}

// RefreshTokens are the refresh tokens issued for sub, oldest first.
func (g *FakeGoogle) RefreshTokens(sub string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for token, grant := range g.auth.refreshes {
		if grant.Sub == sub {
			out = append(out, token)
		}
	}
	slices.Sort(out)
	return out
}

// RefreshCalls is how many refresh_token grants the fake was asked for,
// refused ones included.
func (g *FakeGoogle) RefreshCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.auth.refreshCalls
}

// Revoked are the refresh tokens revoked, in order.
func (g *FakeGoogle) Revoked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.auth.revoked)
}

// signingKey is the fake's key, made at its first need. g.mu is held.
func (g *FakeGoogle) signingKey() *rsa.PrivateKey {
	if g.auth.key == nil {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		g.auth.key = k
	}
	return g.auth.key
}

// discovery is Google's OpenID configuration. g.mu is held.
func (g *FakeGoogle) discovery(req *http.Request) answer {
	if req.Method != http.MethodGet || req.URL.Path != "/.well-known/openid-configuration" {
		return answer{status: http.StatusNotFound, body: g.apiError(http.StatusNotFound, "notFound")}
	}
	return answer{status: http.StatusOK, body: map[string]any{
		"issuer":                                Issuer,
		"authorization_endpoint":                "https://accounts.google.com/o/oauth2/v2/auth",
		"token_endpoint":                        TokenURL,
		"userinfo_endpoint":                     "https://openidconnect.googleapis.com/v1/userinfo",
		"revocation_endpoint":                   "https://" + TokenHost + "/revoke",
		"jwks_uri":                              "https://" + APIsHost + "/oauth2/v3/certs",
		"response_types_supported":              []string{"code", "token", "id_token"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"plain", "S256"},
	}}
}

// certs is the fake's signing key as a JWK set. g.mu is held.
func (g *FakeGoogle) certs() answer {
	pub := g.signingKey().PublicKey
	return answer{status: http.StatusOK, body: map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": signingKeyID,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}}
}

// clientRefusal checks form's web client and secret: an unknown client is
// invalid_client, as Google answers it ("The OAuth client was not found"),
// and so is a wrong secret. ok when they hold.
func (g *FakeGoogle) clientRefusal(form url.Values) (answer, bool) {
	secret, known := g.WebClients[form.Get("client_id")]
	if !known || secret != form.Get("client_secret") {
		return g.oauthError(http.StatusUnauthorized, "invalid_client"), false
	}
	return answer{}, true
}

// authorizationCode is the authorization_code grant. g.mu is held.
func (g *FakeGoogle) authorizationCode(form url.Values) answer {
	if a, ok := g.clientRefusal(form); !ok {
		return a
	}
	c, ok := g.auth.codes[form.Get("code")]
	switch {
	case !ok, c.used, !g.now().Before(c.issued.Add(codeLifetime)),
		c.grant.ClientID != form.Get("client_id"),
		c.grant.RedirectURI != form.Get("redirect_uri"),
		s256(form.Get("code_verifier")) != c.grant.Challenge:
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	c.used = true
	grant := c.grant
	if grant.Granted == nil {
		grant.Granted = slices.Clone(grant.Scopes)
	}
	return g.personalAnswer(grant, true)
}

// refreshToken is the refresh_token grant. g.mu is held.
func (g *FakeGoogle) refreshToken(form url.Values) answer {
	g.auth.refreshCalls++
	if a, ok := g.clientRefusal(form); !ok {
		return a
	}
	token := form.Get("refresh_token")
	if queued := g.auth.refusals[token]; len(queued) > 0 {
		code := queued[0]
		g.auth.refusals[token] = queued[1:]
		status := http.StatusBadRequest
		if code == "temporarily_unavailable" {
			status = http.StatusServiceUnavailable
		}
		return g.oauthError(status, code)
	}
	grant, ok := g.auth.refreshes[token]
	if !ok || grant.ClientID != form.Get("client_id") {
		return g.oauthError(http.StatusBadRequest, "invalid_grant")
	}
	return g.personalAnswer(grant, false)
}

// revoke is the revocation endpoint. g.mu is held.
func (g *FakeGoogle) revoke(form url.Values) answer {
	token := form.Get("token")
	if _, ok := g.auth.refreshes[token]; !ok {
		return g.oauthError(http.StatusBadRequest, "invalid_token")
	}
	delete(g.auth.refreshes, token)
	g.auth.revoked = append(g.auth.revoked, token)
	return answer{status: http.StatusOK, body: map[string]any{}}
}

// personalAnswer is a personal grant's answer: an access token for the
// account, the granted scope, and for a code a refresh token and the ID
// token. g.mu is held.
func (g *FakeGoogle) personalAnswer(grant CodeGrant, code bool) answer {
	lifetime := g.TokenLifetime
	if lifetime == 0 {
		lifetime = 3599 * time.Second
	}
	now := g.now()
	access := "ya29.person-" + randomText(24)
	if g.tokens == nil {
		g.tokens = map[string]issued{}
	}
	g.tokens[access] = issued{subject: grant.Sub, scopes: slices.Clone(grant.Granted), until: now.Add(lifetime)}
	b := map[string]any{
		"access_token": access,
		"expires_in":   int(lifetime / time.Second),
		"scope":        strings.Join(grant.Granted, " "),
		"token_type":   "Bearer",
	}
	if code {
		refresh := "1//0g" + randomText(72)
		if g.auth.refreshes == nil {
			g.auth.refreshes = map[string]CodeGrant{}
		}
		g.auth.refreshes[refresh] = grant
		b["refresh_token"] = refresh
		b["id_token"] = g.idToken(grant, now)
	}
	return answer{status: http.StatusOK, body: b}
}

// idToken is grant's ID token, signed RS256 with the fake's key unless
// IDTokenFor says otherwise. g.mu is held.
func (g *FakeGoogle) idToken(grant CodeGrant, now time.Time) string {
	claims := map[string]any{
		"iss":            Issuer,
		"azp":            grant.ClientID,
		"aud":            grant.ClientID,
		"sub":            grant.Sub,
		"email":          grant.Email,
		"email_verified": true,
		"nonce":          grant.Nonce,
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
	}
	if grant.HD != "" {
		claims["hd"] = grant.HD
	}
	key := g.signingKey()
	if g.auth.idTokenFor != nil && !g.auth.idTokenFor(claims) {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		key = k
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": signingKeyID})
	payload, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// s256 is PKCE's S256 challenge of verifier.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomText is n random bytes, base64url.
func randomText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
