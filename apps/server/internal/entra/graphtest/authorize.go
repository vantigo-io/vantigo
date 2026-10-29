package graphtest

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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is the fake's authorization server (Outlook connection design
// M3–M5, delivery A's plan reading 21): what login.microsoftonline.com
// answers a confidential client's delegated flow, beside the client
// credentials grant graph.go keeps.
//
//   - GET /{tid}/discovery/v2.0/keys is the fake's one RSA signing key as a
//     JWK set; the key is made at the first need, one per fake.
//   - IssueCode stands in for /authorize, which a test never calls: it
//     answers the code Microsoft's redirect would carry for what the
//     authorizeUrl asked. The authorization_code grant checks the secret,
//     the redirect URI, the PKCE verifier against the S256 challenge, a
//     code's single use (54005 on reuse) and its minute of life on the
//     fake's clock (70008); it answers an access token, an RS256 id_token,
//     a 1.5 KB refresh token and scope from what was granted, in Graph's
//     own style (bare Calendars.ReadWrite).
//   - The refresh_token grant answers a new refresh token each time and
//     leaves the old one valid, as Microsoft does (research §3);
//     RefuseRefresh queues a refusal of one token, RefreshCalls counts.
//   - A delegated access token carries scp, oid and tid; Graph answers GET
//     /me for it, and ChallengeNext makes that user's next Graph call 401
//     with a CAE claims challenge.
//
// Every refusal is the token endpoint's { error, error_description,
// error_codes }.

// codeLifetime is how long an issued code may be redeemed.
const codeLifetime = time.Minute

// refreshTokenBytes is the size of a refresh token, before its base64url:
// about 1.5 KB once encoded, a typical v2 refresh token's length.
const refreshTokenBytes = 1152

// signingKeyID is the kid of the fake's signing key.
const signingKeyID = "graphtest-rs256"

// CodeGrant is what /authorize would have been asked and what the person
// would have consented to: the client and its redirect URI, the PKCE
// challenge (S256), the scopes asked, the nonce, and the signed-in account
// — its object id, its tenant and its UPN. Granted is the scope the token
// answer reports; nil grants what was asked.
type CodeGrant struct {
	ClientID    string
	RedirectURI string
	Challenge   string
	Scopes      []string
	Nonce       string
	OID         string
	TID         string
	UPN         string
	Granted     []string
}

// issuedCode is one code: its grant, when it was issued and whether it was
// redeemed.
type issuedCode struct {
	grant  CodeGrant
	issued time.Time
	used   bool
}

// refreshRefusal is one queued refusal of a refresh token.
type refreshRefusal struct {
	error string
	codes []int
}

// delegatedToken is who a delegated access token is for.
type delegatedToken struct {
	oid string
	upn string
}

// authState is the fake's authorization server's memory, under the fake's
// lock.
type authState struct {
	key          *rsa.PrivateKey
	codes        map[string]*issuedCode
	refreshes    map[string]CodeGrant
	refusals     map[string][]refreshRefusal
	refreshCalls int
	delegated    map[string]delegatedToken
	challenges   map[string]int
	idTokenFor   func(claims map[string]any) bool
}

// IssueCode is the code /authorize would answer for grant; it is redeemable
// once, for codeLifetime on the fake's clock.
func (g *FakeGraph) IssueCode(grant CodeGrant) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.auth.codes == nil {
		g.auth.codes = map[string]*issuedCode{}
	}
	code := "0.A" + randomText(48)
	g.auth.codes[code] = &issuedCode{grant: grant, issued: g.now()}
	return code
}

// IDTokenFor sets how every later id_token is made: fn is handed the claims
// the fake would sign and may change them — another tid, nonce or aud — and
// answers whether they are signed with the fake's published key (true) or a
// key nobody published (false). Nil restores the faithful token.
func (g *FakeGraph) IDTokenFor(fn func(claims map[string]any) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.auth.idTokenFor = fn
}

// RefuseRefresh queues a refusal of token's next refresh: error and the
// AADSTS codes in error_codes. Each call queues one more.
func (g *FakeGraph) RefuseRefresh(token, errorCode string, codes ...int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.auth.refusals == nil {
		g.auth.refusals = map[string][]refreshRefusal{}
	}
	g.auth.refusals[token] = append(g.auth.refusals[token], refreshRefusal{error: errorCode, codes: codes})
}

// RefreshCalls is how many refresh_token grants the fake was asked for,
// refused ones included.
func (g *FakeGraph) RefreshCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.auth.refreshCalls
}

// ChallengeNext makes the next Graph call with a delegated token for oid
// answer 401 with a CAE claims challenge. Each call queues one more.
func (g *FakeGraph) ChallengeNext(oid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.auth.challenges == nil {
		g.auth.challenges = map[string]int{}
	}
	g.auth.challenges[strings.ToLower(oid)]++
}

// signingKey is the fake's key, made at its first need. g.mu is held.
func (g *FakeGraph) signingKey() *rsa.PrivateKey {
	if g.auth.key == nil {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		g.auth.key = k
	}
	return g.auth.key
}

var keysPath = regexp.MustCompile(`^/([^/]+)/discovery/v2\.0/keys$`)

// loginHost answers one request to login.microsoftonline.com: the key set
// or the token endpoint. g.mu is held.
func (g *FakeGraph) loginHost(req *http.Request, body []byte) answer {
	if m := keysPath.FindStringSubmatch(req.URL.Path); m != nil && req.Method == http.MethodGet {
		if g.TenantID != "" && !strings.EqualFold(m[1], g.TenantID) {
			return answer{status: http.StatusBadRequest, body: map[string]any{"error": "invalid_tenant", "error_description": "AADSTS90002: Tenant not found."}}
		}
		pub := g.signingKey().PublicKey
		return answer{status: http.StatusOK, body: map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": signingKeyID,
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}}}
	}
	return g.token(req, body)
}

// oauthRefusal is the token endpoint's refusal with its AADSTS codes.
func oauthRefusal(status int, code, description string, codes ...int) answer {
	b := map[string]any{"error": code, "error_description": description}
	if len(codes) > 0 {
		b["error_codes"] = codes
	}
	return answer{status: status, body: b}
}

// clientRefusal checks form's client and secret: an unknown client is
// unauthorized_client, a wrong secret invalid_client. ok when they hold.
func (g *FakeGraph) clientRefusal(form url.Values) (answer, bool) {
	secrets, known := g.Secrets[form.Get("client_id")]
	if !known {
		return oauthRefusal(http.StatusBadRequest, "unauthorized_client", "AADSTS700016: Application not found in the directory.", 700016), false
	}
	if !slices.Contains(secrets, form.Get("client_secret")) {
		return oauthRefusal(http.StatusUnauthorized, "invalid_client", "AADSTS7000215: Invalid client secret provided.", 7000215), false
	}
	return answer{}, true
}

// authorizationCode is the authorization_code grant. g.mu is held.
func (g *FakeGraph) authorizationCode(tenant string, form url.Values) answer {
	if a, ok := g.clientRefusal(form); !ok {
		return a
	}
	c, ok := g.auth.codes[form.Get("code")]
	switch {
	case !ok:
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS9002313: Invalid request. Request is malformed or invalid.", 9002313)
	case c.used:
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS54005: OAuth2 Authorization code was already redeemed.", 54005)
	case !g.now().Before(c.issued.Add(codeLifetime)):
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS70008: The provided authorization code or refresh token has expired.", 70008)
	case c.grant.ClientID != form.Get("client_id"):
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS70000: The code was issued to another client.", 70000)
	case c.grant.RedirectURI != form.Get("redirect_uri"):
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS50148: The redirect_uri does not match the one in the authorization request.", 50148)
	case s256(form.Get("code_verifier")) != c.grant.Challenge:
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS501481: The Code_Verifier does not match the code_challenge supplied in the authorization request.", 501481)
	}
	c.used = true
	grant := c.grant
	if grant.TID == "" {
		grant.TID = tenant
	}
	if grant.Granted == nil {
		grant.Granted = slices.Clone(grant.Scopes)
	}
	return g.delegatedAnswer(grant, true)
}

// refreshToken is the refresh_token grant. g.mu is held.
func (g *FakeGraph) refreshToken(form url.Values) answer {
	g.auth.refreshCalls++
	if a, ok := g.clientRefusal(form); !ok {
		return a
	}
	token := form.Get("refresh_token")
	if queued := g.auth.refusals[token]; len(queued) > 0 {
		r := queued[0]
		g.auth.refusals[token] = queued[1:]
		return oauthRefusal(http.StatusBadRequest, r.error, "The fake was told to refuse this refresh token.", r.codes...)
	}
	grant, ok := g.auth.refreshes[token]
	if !ok {
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS9002313: Invalid request. Request is malformed or invalid.", 9002313)
	}
	if grant.ClientID != form.Get("client_id") {
		return oauthRefusal(http.StatusBadRequest, "invalid_grant", "AADSTS70000: The refresh token was issued to another client.", 70000)
	}
	return g.delegatedAnswer(grant, false)
}

// delegatedAnswer is a delegated grant's answer: an access token for the
// account, a new refresh token, the granted scope and, for a code, the
// id_token. g.mu is held.
func (g *FakeGraph) delegatedAnswer(grant CodeGrant, idToken bool) answer {
	lifetime := g.TokenLifetime
	if lifetime == 0 {
		lifetime = time.Hour
	}
	now := g.now()
	access := unsignedJWT(map[string]any{
		"aud":   "https://graph.microsoft.com",
		"tid":   grant.TID,
		"oid":   grant.OID,
		"appid": grant.ClientID,
		"scp":   strings.Join(grant.Granted, " "),
		"upn":   grant.UPN,
		"iat":   now.Unix(),
		"exp":   now.Add(lifetime).Unix(),
	})
	if g.tokens == nil {
		g.tokens = map[string]time.Time{}
	}
	g.tokens[access] = now.Add(lifetime)
	if g.auth.delegated == nil {
		g.auth.delegated = map[string]delegatedToken{}
	}
	g.auth.delegated[access] = delegatedToken{oid: strings.ToLower(grant.OID), upn: grant.UPN}

	size := refreshTokenBytes
	if g.RefreshTokenBytes > 0 {
		size = g.RefreshTokenBytes
	}
	refresh := "1.A" + randomText(size)
	if g.auth.refreshes == nil {
		g.auth.refreshes = map[string]CodeGrant{}
	}
	g.auth.refreshes[refresh] = grant

	b := map[string]any{
		"token_type":     "Bearer",
		"scope":          strings.Join(grant.Granted, " "),
		"expires_in":     int(lifetime / time.Second),
		"ext_expires_in": int(lifetime / time.Second),
		"access_token":   access,
		"refresh_token":  refresh,
	}
	if idToken {
		b["id_token"] = g.idToken(grant, now)
	}
	return answer{status: http.StatusOK, body: b}
}

// idToken is grant's id_token, signed RS256 with the fake's key unless
// IDTokenFor says otherwise. g.mu is held.
func (g *FakeGraph) idToken(grant CodeGrant, now time.Time) string {
	claims := map[string]any{
		"ver":                "2.0",
		"iss":                "https://login.microsoftonline.com/" + grant.TID + "/v2.0",
		"aud":                grant.ClientID,
		"sub":                "sub-" + grant.OID,
		"tid":                grant.TID,
		"oid":                grant.OID,
		"nonce":              grant.Nonce,
		"preferred_username": grant.UPN,
		"iat":                now.Unix(),
		"nbf":                now.Unix(),
		"exp":                now.Add(time.Hour).Unix(),
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

// delegatedCall answers what Graph answers a delegated token before its
// route: a queued claims challenge, or GET /me. ok is false when the call
// goes on to the route. g.mu is held.
func (g *FakeGraph) delegatedCall(token, method, path string) (answer, bool) {
	who, ok := g.auth.delegated[token]
	if !ok {
		if path == "/me" {
			return graphError(http.StatusBadRequest, "BadRequest", "/me request is only valid with delegated authentication flow."), true
		}
		return answer{}, false
	}
	if g.auth.challenges[who.oid] > 0 {
		g.auth.challenges[who.oid]--
		a := graphError(http.StatusUnauthorized, "InvalidAuthenticationToken", "Continuous access evaluation resulted in challenge.")
		claims := base64.StdEncoding.EncodeToString([]byte(`{"access_token":{"nbf":{"essential":true,"value":"` + uuid.NewString() + `"}}}`))
		a.header = http.Header{"Www-Authenticate": []string{
			`Bearer realm="", authorization_uri="https://login.microsoftonline.com/common/oauth2/authorize", error="insufficient_claims", claims="` + claims + `"`,
		}}
		return a, true
	}
	if path == "/me" && method == http.MethodGet {
		return answer{status: http.StatusOK, body: map[string]any{"id": who.oid, "userPrincipalName": who.upn}}, true
	}
	return answer{}, false
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
