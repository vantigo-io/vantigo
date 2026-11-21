package identity

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// The OAuth client rules of OAuth connections design C6, C9, A1, A2, A4,
// A12, and the token encoding of C16, A10.

func TestOAuthClientIDURL(t *testing.T) {
	for _, c := range []struct {
		id string
		ok bool
	}{
		{"https://claude.ai/oauth/claude-code-client-metadata", true},
		{"https://chatgpt.com/oauth/client.json", true},
		{"https://example.com:443/client.json", true},
		{"https://example.com/client.json?v=1", true},
		{"http://example.com/client.json", false},
		{"https://example.com", false},
		{"https://example.com/", false},
		{"https://example.com:8443/client.json", false},
		{"https://user@example.com/client.json", false},
		{"https://user:pw@example.com/client.json", false},
		{"https://example.com/client.json#frag", false},
		{"https://example.com/a/../client.json", false},
		{"https://example.com/./client.json", false},
		{"https://example.com/a/%2e%2e/client.json", false},
		{"https://example.com/a/%2E/client.json", false},
		{"https://127.0.0.1/client.json", false},
		{"https://[::1]/client.json", false},
		{"https://localhost/client.json", false},
		{"https://xn--80ak6aa92e.com/client.json", false},
		{"https://exämple.com/client.json", false},
		{"https://vantigo.example.com/client.json", false},
		{"https://VANTIGO.example.com/client.json", false},
		{"https://example.com/" + strings.Repeat("a", 512), false},
		{"", false},
		{"https://example.com/ space", false},
	} {
		_, err := oauthClientIDURL(c.id, "vantigo.example.com")
		if (err == nil) != c.ok {
			t.Errorf("%q: err %v, want ok %v", c.id, err, c.ok)
		}
		var refused errOAuthClient
		if err != nil && !errors.As(err, &refused) {
			t.Errorf("%q: %T is not a client refusal", c.id, err)
		}
	}
}

func TestRedirectURIs(t *testing.T) {
	for _, c := range []struct {
		uri string
		ok  bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://chatgpt.com/connector_platform_oauth_redirect", true},
		{"http://localhost/callback", true},
		{"http://127.0.0.1:53682/callback", true},
		{"http://[::1]:8080/cb", true},
		{"http://example.com/callback", false},
		{"https://example.com/cb#frag", false},
		{"myapp://callback", false},
		{"https:///nohost", false},
		{"https://" + strings.Repeat("a", 1020) + ".com/", false},
	} {
		if got := validRedirectURI(c.uri); got != c.ok {
			t.Errorf("%q: %v, want %v", c.uri, got, c.ok)
		}
	}
	for _, c := range []struct {
		registered, requested string
		ok                    bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/auth_callback", true},
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/auth_callback/", false},
		{"https://claude.ai/api/mcp/auth_callback", "https://Claude.ai/api/mcp/auth_callback", false},
		{"https://claude.ai/cb", "https://claude.ai:443/cb", false},
		// A loopback URI's port is the client's to choose (A4).
		{"http://localhost/callback", "http://localhost:53682/callback", true},
		{"http://localhost/callback", "http://localhost/callback", true},
		{"http://localhost:3000/callback", "http://localhost:4000/callback", true},
		{"http://127.0.0.1/callback", "http://127.0.0.1:9/callback", true},
		// localhost and 127.0.0.1 are different hosts (C9).
		{"http://localhost/callback", "http://127.0.0.1:53682/callback", false},
		{"http://127.0.0.1/callback", "http://localhost/callback", false},
		{"http://localhost/callback", "http://localhost:1/other", false},
		{"http://localhost/callback?a=1", "http://localhost:1/callback?a=1", true},
		{"http://localhost/callback?a=1", "http://localhost:1/callback?a=2", false},
		{"http://localhost/callback", "https://localhost/callback", false},
	} {
		if got := redirectMatches(c.registered, c.requested); got != c.ok {
			t.Errorf("%q against %q: %v, want %v", c.requested, c.registered, got, c.ok)
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "oauth", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The three live documents A1 records are accepted, ChatGPT's private_key_jwt
// beside none included.
func TestAcceptOAuthDocument_TheLiveClients(t *testing.T) {
	for _, c := range []struct{ file, id, host, name, redirect string }{
		{"claude-code.json", "https://claude.ai/oauth/claude-code-client-metadata", "claude.ai", "Claude Code", "http://localhost/callback"},
		{"claude.json", "https://claude.ai/oauth/mcp-oauth-client-metadata", "claude.ai", "Claude", "https://claude.ai/api/mcp/auth_callback"},
		{"chatgpt.json", "https://chatgpt.com/oauth/client.json", "chatgpt.com", "ChatGPT", "https://chatgpt.com/connector_platform_oauth_redirect"},
	} {
		client, err := acceptOAuthDocument(c.id, c.host, readFixture(t, c.file))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if client.name != c.name || client.redirectURIs[0] != c.redirect {
			t.Errorf("%s: %+v", c.file, client)
		}
	}
}

func TestAcceptOAuthDocument_TheRules(t *testing.T) {
	const id = "https://example.com/client.json"
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"minimal", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"]}`, true},
		{"not an object", `["x"]`, false},
		{"not JSON", `{`, false},
		{"another client_id", `{"client_id":"https://example.com/other.json","client_name":"App","redirect_uris":["https://example.com/cb"]}`, false},
		{"no name", `{"client_id":"` + id + `","redirect_uris":["https://example.com/cb"]}`, false},
		{"a blank name", `{"client_id":"` + id + `","client_name":"  ","redirect_uris":["https://example.com/cb"]}`, false},
		{"no redirect", `{"client_id":"` + id + `","client_name":"App","redirect_uris":[]}`, false},
		{"an http redirect", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["http://example.com/cb"]}`, false},
		{"a secret", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"client_secret":"s"}`, false},
		{"private_key_jwt alone", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"token_endpoint_auth_method":"private_key_jwt"}`, false},
		{"client_secret_basic", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"token_endpoint_auth_method":"client_secret_basic"}`, false},
		{"a list without none", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":["private_key_jwt"]}`, false},
		{"no authorization_code", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"grant_types":["client_credentials"]}`, false},
		{"no code", `{"client_id":"` + id + `","client_name":"App","redirect_uris":["https://example.com/cb"],"response_types":["token"]}`, false},
	} {
		if _, err := acceptOAuthDocument(id, "example.com", []byte(c.body)); (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok %v", c.name, err, c.ok)
		}
	}
	tooMany := `{"client_id":"` + id + `","client_name":"App","redirect_uris":[` + strings.TrimSuffix(strings.Repeat(`"https://example.com/cb",`, 21), ",") + `]}`
	if _, err := acceptOAuthDocument(id, "example.com", []byte(tooMany)); err == nil {
		t.Error("21 redirect URIs were accepted")
	}
}

func TestOAuthDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"Claude":                      "Claude",
		"  Chat\tGPT \n":              "Chat GPT",
		"Evil\u202eppa.exe":           "Evilppa.exe",
		"Zero\u200bWidth":             "ZeroWidth",
		"Bell\x07":                    "Bell",
		strings.Repeat("é", 100):      strings.Repeat("é", 80),
		"Vantigo \u2066Support\u2069": "Vantigo Support",
	} {
		if got := oauthDisplayName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestDocumentMaxAge(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"":                          time.Hour,
		"no-cache":                  time.Hour,
		"max-age=60":                5 * time.Minute,
		"public, max-age=600":       10 * time.Minute,
		"max-age=999999":            24 * time.Hour,
		"max-age=abc":               time.Hour,
		`max-age="3600", immutable`: time.Hour,
	} {
		if got := documentMaxAge(header); got != want {
			t.Errorf("%q: %v, want %v", header, got, want)
		}
	}
}

func TestPKCEAndScopes(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !pkceMatches(verifier, challenge) || pkceMatches(verifier+"x", challenge) {
		t.Error("PKCE S256 does not compare")
	}
	if !codeChallengePattern.MatchString(challenge) || codeChallengePattern.MatchString(challenge+"A") {
		t.Error("the challenge pattern")
	}
	if !codeVerifierPattern.MatchString(verifier) || codeVerifierPattern.MatchString(verifier[:42]) || codeVerifierPattern.MatchString(verifier+"!") {
		t.Error("the verifier pattern")
	}
	for scope, want := range map[string][2]bool{
		"":                     {false, true},
		"offline_access":       {false, true},
		"read":                 {false, true},
		"read write":           {true, true},
		"write offline_access": {true, true},
		"read admin":           {false, false},
		"openid":               {false, false},
	} {
		w, ok := parseScopes(scope)
		if w != want[0] || ok != want[1] {
			t.Errorf("%q: write %v ok %v", scope, w, ok)
		}
	}
}

func TestResourceMatches(t *testing.T) {
	a := &Access{cfg: &config.Config{OAuthIssuer: "https://vantigo.example.com/erp", OAuthResource: "https://vantigo.example.com/erp/api/mcp"}}
	for resource, want := range map[string]bool{
		"https://vantigo.example.com/erp/api/mcp":      true,
		"https://vantigo.example.com/erp/api/mcp/":     true,
		"HTTPS://Vantigo.Example.com:443/erp/api/mcp":  true,
		"https://vantigo.example.com/erp":              true,
		"https://vantigo.example.com/erp/":             true,
		"https://vantigo.example.com/api/mcp":          false,
		"https://vantigo.example.com:8443/erp/api/mcp": false,
		"http://vantigo.example.com/erp/api/mcp":       false,
		"https://vantigo.example.com/erp/api/mcp?x=1":  false,
		"https://other.example.com/erp/api/mcp":        false,
		"":                                             false,
	} {
		if got := a.resourceMatches(resource); got != want {
			t.Errorf("%q: %v, want %v", resource, got, want)
		}
	}
}

// An access token and a refresh token share the key's encoding, and the
// prefix is no part of the hash (A10): only the kind in the lookup tells
// them apart.
func TestOAuthTokenEncoding(t *testing.T) {
	access, hash := newPrefixedToken(accessTokenPrefix)
	if len(access) != apiKeyLength || !strings.HasPrefix(access, "vta_") {
		t.Fatalf("access token %q", access)
	}
	if got, ok := parsePrefixedToken(accessTokenPrefix, access); !ok || string(got) != string(hash) {
		t.Error("the access token does not parse to its hash")
	}
	if _, ok := parsePrefixedToken(refreshTokenPrefix, access); ok {
		t.Error("an access token parsed as a refresh token")
	}
	swapped := refreshTokenPrefix + access[len(accessTokenPrefix):]
	if got, ok := parsePrefixedToken(refreshTokenPrefix, swapped); !ok || string(got) != string(hash) {
		t.Error("the same bytes under vtr_ should hash alike; the lookup's kind is what refuses them")
	}
	if _, ok := parsePrefixedToken(accessTokenPrefix, access[:len(access)-1]+"0"); ok && !strings.HasSuffix(access, "0") {
		t.Error("a bad checksum parsed")
	}
}

// The fetch (C6, A2) against a fake host: a good document, then every way a
// host can fail it.
func TestOAuthFetch(t *testing.T) {
	var handler http.HandlerFunc
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r) }))
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	addr := srv.Listener.Addr().String()
	fetcher := func(resolve netip.Addr) *oauthFetcher {
		a := &Access{}
		a.UseOAuthNetwork(fixedResolver{addrs: []netip.Addr{resolve}}, func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
		return a.oauthFetcherFor()
	}
	public := netip.MustParseAddr("93.184.215.14")
	target := "https://example.com/client.json"

	var gotUA, gotAccept string
	handler = func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.UserAgent(), r.Header.Get("Accept")
		w.Header().Set("Cache-Control", "max-age=120")
		_, _ = w.Write([]byte(`{"client_id":"x"}`))
	}
	body, age, err := fetcher(public).fetch(context.Background(), target)
	if err != nil || string(body) != `{"client_id":"x"}` || age != 5*time.Minute {
		t.Fatalf("a good document: %q %v %v", body, age, err)
	}
	if gotUA != "Vantigo" || gotAccept != "application/json" {
		t.Errorf("User-Agent %q, Accept %q", gotUA, gotAccept)
	}

	for name, h := range map[string]http.HandlerFunc{
		"a redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.com/elsewhere.json", http.StatusFound)
		},
		"a 404":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"oversized": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", 16<<10))) },
	} {
		handler = h
		if _, _, err := fetcher(public).fetch(context.Background(), target); err == nil {
			t.Errorf("%s was fetched", name)
		}
	}
	handler = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }
	for _, private := range []string{"10.0.0.1", "127.0.0.1", "169.254.169.254", "192.168.1.1"} {
		_, _, err := fetcher(netip.MustParseAddr(private)).fetch(context.Background(), target)
		var refused errOAuthClient
		if err == nil || !errors.As(err, &refused) || !strings.Contains(refused.reason, "address") {
			t.Errorf("%s: %v", private, err)
		}
	}
}
