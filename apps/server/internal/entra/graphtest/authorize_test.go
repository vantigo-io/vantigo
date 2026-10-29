package graphtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

const (
	redirectURI = "https://vantigo.example/api/v1/entra/outlook/callback"
	verifier    = "the-pkce-verifier-of-at-least-forty-three-characters-0123"
	personOID   = "0b8c3d4e-5f60-4a71-8b92-a3b4c5d6e7f8"
	personUPN   = "kari@nordlys.no"
)

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// codeGrant is a person consenting to Calendars.ReadWrite for the client.
func codeGrant() graphtest.CodeGrant {
	return graphtest.CodeGrant{
		ClientID: clientID, RedirectURI: redirectURI, Challenge: challengeOf(verifier),
		Scopes: []string{"openid", "profile", "offline_access", "https://graph.microsoft.com/Calendars.ReadWrite"},
		Nonce:  "the-nonce", OID: personOID, TID: tenant, UPN: personUPN,
		Granted: []string{"Calendars.ReadWrite", "User.Read", "openid", "profile"},
	}
}

// tokenAnswer is the token endpoint's answer, success or refusal.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorCodes   []int  `json:"error_codes"`
}

func postToken(t *testing.T, g *graphtest.FakeGraph, form url.Values) (int, tokenAnswer) {
	t.Helper()
	resp, err := (&http.Client{Transport: g}).Post("https://login.microsoftonline.com/"+tenant+"/oauth2/v2.0/token",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var a tokenAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return resp.StatusCode, a
}

func redeem(t *testing.T, g *graphtest.FakeGraph, code string, change func(url.Values)) (int, tokenAnswer) {
	t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "client_secret": {secret},
		"code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}
	if change != nil {
		change(form)
	}
	return postToken(t, g, form)
}

// verifyIDToken verifies raw against the fake's published key set, as the
// module does.
func verifyIDToken(t *testing.T, g *graphtest.FakeGraph, now time.Time, raw string) (*oidc.IDToken, error) {
	t.Helper()
	ctx := oidc.ClientContext(context.Background(), &http.Client{Transport: g})
	keys := oidc.NewRemoteKeySet(ctx, "https://login.microsoftonline.com/"+tenant+"/discovery/v2.0/keys")
	v := oidc.NewVerifier("https://login.microsoftonline.com/"+tenant+"/v2.0", keys,
		&oidc.Config{ClientID: clientID, Now: func() time.Time { return now }})
	return v.Verify(ctx, raw)
}

func TestAuthorizationCode_RedeemedOnceForASignedIDToken(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	code := g.IssueCode(codeGrant())

	status, a := redeem(t, g, code, nil)
	if status != http.StatusOK || a.AccessToken == "" || a.RefreshToken == "" || a.IDToken == "" {
		t.Fatalf("redeem: %d %+v", status, a)
	}
	if a.Scope != "Calendars.ReadWrite User.Read openid profile" {
		t.Errorf("scope = %q, want the granted scopes in Graph's style", a.Scope)
	}
	if n := len(a.RefreshToken); n < 1400 || n > 1700 {
		t.Errorf("refresh token of %d bytes, want about 1.5 KB", n)
	}
	id, err := verifyIDToken(t, g, now, a.IDToken)
	if err != nil {
		t.Fatalf("the id_token does not verify: %v", err)
	}
	var claims struct {
		TID   string `json:"tid"`
		OID   string `json:"oid"`
		UPN   string `json:"preferred_username"`
		Nonce string `json:"nonce"`
	}
	if err := id.Claims(&claims); err != nil {
		t.Fatal(err)
	}
	if claims.TID != tenant || claims.OID != personOID || claims.UPN != personUPN || claims.Nonce != "the-nonce" {
		t.Errorf("claims = %+v", claims)
	}

	// Graph knows the person by the access token.
	c := &client{t: t, http: &http.Client{Transport: g}, token: a.AccessToken}
	if status, _, me := c.do(http.MethodGet, "/me", nil); status != http.StatusOK || me["id"] != personOID || me["userPrincipalName"] != personUPN {
		t.Errorf("GET /me: %d %v", status, me)
	}

	if status, a := redeem(t, g, code, nil); status != http.StatusBadRequest || a.Error != "invalid_grant" || len(a.ErrorCodes) == 0 || a.ErrorCodes[0] != 54005 {
		t.Errorf("a code redeemed twice: %d %+v, want invalid_grant 54005", status, a)
	}
}

func TestAuthorizationCode_Refusals(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		change func(url.Values)
		wait   time.Duration
		status int
		error  string
		code   int
	}{
		{"expired", nil, time.Minute, http.StatusBadRequest, "invalid_grant", 70008},
		{"wrong verifier", func(f url.Values) { f.Set("code_verifier", verifier+"x") }, 0, http.StatusBadRequest, "invalid_grant", 501481},
		{"wrong redirect URI", func(f url.Values) { f.Set("redirect_uri", "https://elsewhere.example/cb") }, 0, http.StatusBadRequest, "invalid_grant", 50148},
		{"wrong secret", func(f url.Values) { f.Set("client_secret", "nope") }, 0, http.StatusUnauthorized, "invalid_client", 7000215},
		{"unknown code", func(f url.Values) { f.Set("code", "0.Anothing") }, 0, http.StatusBadRequest, "invalid_grant", 9002313},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now
			g := tenantFake(&at)
			code := g.IssueCode(codeGrant())
			at = at.Add(tc.wait)
			status, a := redeem(t, g, code, tc.change)
			if status != tc.status || a.Error != tc.error || len(a.ErrorCodes) == 0 || a.ErrorCodes[0] != tc.code {
				t.Errorf("%d %+v, want %d %s %d", status, a, tc.status, tc.error, tc.code)
			}
		})
	}
}

func TestRefreshToken_NewEachTimeTheOldStillValid(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	_, first := redeem(t, g, g.IssueCode(codeGrant()), nil)
	refresh := func(token string) (int, tokenAnswer) {
		return postToken(t, g, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {clientID}, "client_secret": {secret},
			"refresh_token": {token}, "scope": {"https://graph.microsoft.com/Calendars.ReadWrite offline_access"},
		})
	}
	status, second := refresh(first.RefreshToken)
	if status != http.StatusOK || second.RefreshToken == "" || second.RefreshToken == first.RefreshToken || second.IDToken != "" {
		t.Fatalf("refresh: %d %+v", status, second)
	}
	if status, again := refresh(first.RefreshToken); status != http.StatusOK || again.AccessToken == "" {
		t.Errorf("the old refresh token again: %d %+v, want it still valid", status, again)
	}
	g.RefuseRefresh(second.RefreshToken, "invalid_grant", 50076)
	if status, a := refresh(second.RefreshToken); status != http.StatusBadRequest || a.Error != "invalid_grant" || len(a.ErrorCodes) != 1 || a.ErrorCodes[0] != 50076 {
		t.Errorf("a refused refresh: %d %+v", status, a)
	}
	if status, _ := refresh(second.RefreshToken); status != http.StatusOK {
		t.Errorf("after the queued refusal: %d, want 200", status)
	}
	if n := g.RefreshCalls(); n != 4 {
		t.Errorf("RefreshCalls = %d, want 4", n)
	}
}

func TestChallengeNext_OneCallAnswersAClaimsChallenge(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	_, a := redeem(t, g, g.IssueCode(codeGrant()), nil)
	c := &client{t: t, http: &http.Client{Transport: g}, token: a.AccessToken}
	g.ChallengeNext(personOID)
	status, header, _ := c.do(http.MethodGet, "/me", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("challenged: %d, want 401", status)
	}
	if h := header.Get("WWW-Authenticate"); !strings.Contains(h, `error="insufficient_claims"`) || !strings.Contains(h, `claims="`) {
		t.Errorf("WWW-Authenticate = %q", h)
	}
	if status, _, _ := c.do(http.MethodGet, "/me", nil); status != http.StatusOK {
		t.Errorf("the next call: %d, want 200", status)
	}
	app := newClient(t, g)
	if status, _, _ := app.do(http.MethodGet, "/me", nil); status != http.StatusBadRequest {
		t.Errorf("GET /me with an application token: %d, want 400", status)
	}
}

func TestIDTokenFor_Tampered(t *testing.T) {
	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	g := tenantFake(&now)
	g.IDTokenFor(func(claims map[string]any) bool { claims["nonce"] = "another"; return true })
	_, a := redeem(t, g, g.IssueCode(codeGrant()), nil)
	id, err := verifyIDToken(t, g, now, a.IDToken)
	if err != nil || id.Nonce != "another" {
		t.Errorf("a changed claim, still signed: %v %v", id, err)
	}
	g.IDTokenFor(func(map[string]any) bool { return false })
	_, a = redeem(t, g, g.IssueCode(codeGrant()), nil)
	if _, err := verifyIDToken(t, g, now, a.IDToken); err == nil {
		t.Error("a token signed with an unpublished key verified")
	}
}
