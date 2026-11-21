package integration_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// OAuth connections end to end (OAuth connections design §8): a client
// identified by its metadata document, served from a fake host, connects by
// an Owner's consent, exchanges its code, calls /api/mcp and a REST
// operation with its access token, renews it, and is refused every way the
// design says, each refusal with its status, body and WWW-Authenticate.

const (
	oauthOrigin     = "http://modules.example.com"
	oauthResourceMD = oauthOrigin + "/.well-known/oauth-protected-resource/api/mcp"
	claudeID        = "https://claude.ai/oauth/mcp-oauth-client-metadata"
	claudeReturn    = "https://claude.ai/api/mcp/auth_callback"
	chatGPTID       = "https://chatgpt.com/oauth/client.json"
	chatGPTReturn   = "https://chatgpt.com/connector_platform_oauth_redirect"
	claudeCodeID    = "https://claude.ai/oauth/claude-code-client-metadata"
	pkceVerifier    = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

var (
	oauthSkip      = modtest.SkipContract("the OAuth flow's identity operations are validated by identity's own tests")
	oauthTokenSkip = modtest.SkipContract("the token endpoint is RFC 6749's, in no contract")
)

// oauthHarness composes customers with OAuth on and the three live client
// documents (A1) served from a fake host.
func oauthHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	host := modtest.NewOAuthClientHost(t, "claude.ai", "chatgpt.com")
	for file, id := range map[string]string{"claude.json": claudeID, "chatgpt.json": chatGPTID, "claude-code.json": claudeCodeID} {
		b, err := os.ReadFile(filepath.Join("..", "identity", "testdata", "oauth", file))
		if err != nil {
			t.Fatal(err)
		}
		host.Serve(id, b, "")
	}
	opts = append([]modtest.Option{
		modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers")),
		modtest.WithEnv("OAUTH_ENABLED", "1"), modtest.WithOAuthClientHost(host),
	}, opts...)
	return modtest.New(t, opts...)
}

// signInOwner is a signed-in Owner whose session verified a second factor.
func signInOwner(t *testing.T, h *modtest.Harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, identity.RoleOwnerID)
	h.Exec(t, `UPDATE identity.sessions SET mfa_verified_at = created_at WHERE user_id = $1`, id)
	return c, id
}

func pkceChallenge() string {
	sum := sha256.Sum256([]byte(pkceVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeQuery(clientID, redirect, scope string) string {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {pkceChallenge()}, "code_challenge_method": {"S256"}, "state": {"s-1"},
		"scope": {scope}, "resource": {oauthOrigin + "/api/mcp"},
	}.Encode()
}

// approveCode previews and approves the request as owner and answers the code.
func approveCode(t *testing.T, owner *modtest.Client, query string, integration uuid.UUID, access string) string {
	t.Helper()
	if r := owner.Do(http.MethodPost, "/api/v1/identity/oauth/authorizations/preview", map[string]any{"query": query}, oauthSkip); r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"outcome":"consent"`) {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	r := owner.Do(http.MethodPost, "/api/v1/identity/oauth/authorizations",
		map[string]any{"query": query, "integrationId": integration, "access": access, "decision": "approve"}, oauthSkip)
	if r.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
	var out struct {
		RedirectTo string `json:"redirectTo"`
	}
	r.JSON(&out)
	u, err := url.Parse(out.RedirectTo)
	if err != nil || u.Query().Get("iss") != oauthOrigin || u.Query().Get("state") != "s-1" {
		t.Fatalf("redirect %q", out.RedirectTo)
	}
	return u.Query().Get("code")
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
}

// tokenRequest posts form to the token endpoint from c.
func tokenRequest(c *modtest.Client, form url.Values, opts ...modtest.RequestOption) (*modtest.Response, tokens) {
	r := c.Do(http.MethodPost, "/api/oauth/token", nil, append([]modtest.RequestOption{
		oauthTokenSkip, modtest.RawBody("application/x-www-form-urlencoded", []byte(form.Encode())),
	}, opts...)...)
	var out tokens
	_ = json.Unmarshal(r.Body, &out)
	return r, out
}

func codeForm(code, clientID, redirect string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {pkceVerifier}}
}

func refreshForm(refresh, clientID string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
}

// connectClient runs the flow for clientID and answers its tokens.
func connectClient(t *testing.T, h *modtest.Harness, owner *modtest.Client, integration uuid.UUID, access, clientID, redirect string) tokens {
	t.Helper()
	code := approveCode(t, owner, authorizeQuery(clientID, redirect, "read write offline_access"), integration, access)
	r, tok := tokenRequest(h.Client(t), codeForm(code, clientID, redirect))
	if r.Status != http.StatusOK || r.Header("Cache-Control") != "no-store" || tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 {
		t.Fatalf("exchange: %d %s", r.Status, r.Body)
	}
	return tok
}

func connectionOf(t *testing.T, h *modtest.Harness, integration uuid.UUID) uuid.UUID {
	t.Helper()
	return modtest.One[uuid.UUID](t, h, `SELECT id FROM identity.oauth_connections WHERE integration_id = $1 AND activated_at IS NOT NULL ORDER BY activated_at DESC LIMIT 1`, integration)
}

// The whole flow for each of the three live clients (A1): consent,
// exchange, /api/mcp's tools/list and a read tool call, and a REST read,
// each as the integration, with the request log naming the connection.
func TestOAuth_TheWholeFlow(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	person := h.SignIn(t, "customers:view", "customers:create")
	if r := person.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Fjordkraft AS"}); r.Status != http.StatusCreated {
		t.Fatalf("create a customer: %d %s", r.Status, r.Body)
	}
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")

	for _, client := range []struct{ id, redirect string }{
		{claudeID, claudeReturn}, {chatGPTID, chatGPTReturn}, {claudeCodeID, "http://localhost:53682/callback"},
	} {
		tok := connectClient(t, h, owner, integration, "read_write", client.id, client.redirect)
		if tok.Scope != "read write" {
			t.Errorf("%s: scope %q", client.id, tok.Scope)
		}
		c := h.APIKeyClient(t, tok.AccessToken)
		list := mcpCall(c, "tools/list", nil)
		if list.Status != http.StatusOK || !strings.Contains(string(list.Body), `"name":"write"`) || !strings.Contains(string(list.Body), `"securitySchemes":[{"scopes":["write"],"type":"oauth2"}]`) {
			t.Errorf("%s: tools/list %d %s", client.id, list.Status, list.Body)
		}
		if _, text, isError := mcpTool(t, c, "read", map[string]any{"operation": "getCustomers", "query": map[string]any{"search": "Fjordkraft"}}); isError || !strings.Contains(text, "Fjordkraft AS") {
			t.Errorf("%s: read %v %s", client.id, isError, text)
		}
		if r := c.Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a connection's REST read")); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "Fjordkraft") {
			t.Errorf("%s: REST %d %s", client.id, r.Status, r.Body)
		}
	}

	// The request log names the connection, not a key (A18).
	connection := connectionOf(t, h, integration)
	h.FlushAPIRequests()
	if n := h.Count(t, `SELECT count(*) FROM identity.api_requests WHERE integration_id = $1 AND key_id IS NOT NULL`, integration); n != 0 {
		t.Errorf("%d rows name a key", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM identity.api_requests WHERE connection_id = $1 AND operation_id = 'getCustomers' AND status = 200`, connection); n != 2 {
		t.Errorf("%d getCustomers rows for the last connection, want 2 (the tool call and the REST read)", n)
	}
	// The screen reads the harness clock's 30 days; the log stamps the wall
	// clock's.
	h.Exec(t, `UPDATE identity.api_requests SET at = $2 WHERE integration_id = $1`, integration, h.Now().Add(-time.Minute))
	r := owner.Do(http.MethodGet, "/api/v1/identity/integrations/"+integration.String()+"/requests", nil, oauthSkip)
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"clientName":"Claude Code"`) || !strings.Contains(string(r.Body), `"keyId":null`) {
		t.Errorf("the request log: %d %s", r.Status, r.Body)
	}
}

// Every refusal (C15, C21, C22, A9, A15, A16): the MCP challenge without a
// bearer, an unknown and a revoked token's 401 with its challenge, a
// read-only connection's write as insufficient_scope and the token
// endpoint's errors, none clearing the session cookie.
func TestOAuth_TheRefusals(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")

	// No bearer at all: the 401 starts a sign-in (C22).
	r := mcpCall(h.Client(t), "tools/list", nil)
	if want := `Bearer realm="vantigo", resource_metadata="` + oauthResourceMD + `", scope="read write"`; r.Status != http.StatusUnauthorized || r.Header("WWW-Authenticate") != want || r.Code() != "api_key_required" {
		t.Errorf("no bearer: %d %q %s", r.Status, r.Header("WWW-Authenticate"), r.Body)
	}
	// An unknown token, at MCP and REST.
	unknown, _, _ := identity.MintAPIKey()
	unknown = "vta_" + unknown[4:]
	invalid := `Bearer error="invalid_token", resource_metadata="` + oauthResourceMD + `"`
	for name, r := range map[string]*modtest.Response{
		"mcp":  mcpCall(h.APIKeyClient(t, unknown), "tools/list", nil),
		"rest": h.APIKeyClient(t, unknown).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")),
	} {
		if r.Status != http.StatusUnauthorized || r.Header("WWW-Authenticate") != invalid || r.Code() != "token_invalid" || r.Header("Set-Cookie") != "" {
			t.Errorf("an unknown token at %s: %d %q %s", name, r.Status, r.Header("WWW-Authenticate"), r.Body)
		}
	}

	// A read-only connection: reads, and a write refused for its scope alone.
	tok := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	c := h.APIKeyClient(t, tok.AccessToken)
	if tok.Scope != "read" {
		t.Errorf("scope %q", tok.Scope)
	}
	if r := c.Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a connection's read")); r.Status != http.StatusOK {
		t.Errorf("read: %d %s", r.Status, r.Body)
	}
	r = c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "x"}, modtest.SkipContract("a refusal"))
	if want := `Bearer error="insufficient_scope", scope="read write", resource_metadata="` + oauthResourceMD + `"`; r.Status != http.StatusForbidden || r.Code() != "insufficient_scope" || r.Header("WWW-Authenticate") != want {
		t.Errorf("a read-only write: %d %q %s", r.Status, r.Header("WWW-Authenticate"), r.Body)
	}
	if list := mcpCall(c, "tools/list", nil); list.Status != http.StatusOK || strings.Contains(string(list.Body), `"name":"write"`) {
		t.Errorf("a read-only connection lists write: %s", list.Body)
	}
	// A refusal the roles cause is the plain 403, with no challenge (A16).
	narrow := h.Integration(t, "Narrow", "customers:view")
	setMCPAccess(t, h, narrow, "read_write")
	wide := connectClient(t, h, owner, narrow, "read_write", claudeID, claudeReturn)
	r = h.APIKeyClient(t, wide.AccessToken).Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "x"}, modtest.SkipContract("a refusal"))
	if r.Status != http.StatusForbidden || r.Code() != "forbidden" || r.Header("WWW-Authenticate") != "" {
		t.Errorf("a roles refusal: %d %q %s", r.Status, r.Header("WWW-Authenticate"), r.Body)
	}
	// MCP off for the integration: 403 mcp_not_enabled, REST still works.
	setMCPAccess(t, h, narrow, "off")
	if r := mcpCall(h.APIKeyClient(t, wide.AccessToken), "tools/list", nil); r.Status != http.StatusForbidden || r.Code() != "mcp_not_enabled" {
		t.Errorf("MCP off: %d %s", r.Status, r.Body)
	}

	// A signed-in browser presenting a token keeps its session (C21).
	browser := h.SignIn(t, "customers:view")
	r = browser.Do(http.MethodGet, "/api/v1/customers", nil, modtest.Header("Authorization", "Bearer "+unknown), modtest.SkipContract("a refusal"))
	if r.Status != http.StatusUnauthorized || r.Header("Set-Cookie") != "" {
		t.Errorf("a browser's token: %d %q", r.Status, r.Header("Set-Cookie"))
	}
	if r := browser.Do(http.MethodGet, "/api/v1/customers", nil); r.Status != http.StatusOK {
		t.Errorf("the browser after: %d", r.Status)
	}

	// The token endpoint's errors are RFC 6749's.
	anon := h.Client(t)
	for name, c := range map[string]struct {
		form   url.Values
		opts   []modtest.RequestOption
		status int
		code   string
	}{
		"no grant":       {url.Values{}, nil, 400, "invalid_request"},
		"an unknown one": {url.Values{"grant_type": {"client_credentials"}}, nil, 400, "unsupported_grant_type"},
		"a secret":       {url.Values{"grant_type": {"authorization_code"}, "client_secret": {"x"}}, nil, 401, "invalid_client"},
		"an assertion":   {url.Values{"grant_type": {"authorization_code"}, "client_assertion": {"x"}}, nil, 401, "invalid_client"},
		"Basic":          {url.Values{"grant_type": {"authorization_code"}}, []modtest.RequestOption{modtest.Header("Authorization", "Basic eDp5")}, 401, "invalid_client"},
		"a bad code":     {codeForm("nope", claudeID, claudeReturn), nil, 400, "invalid_grant"},
		"a short verifier": {url.Values{"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {claudeID}, "redirect_uri": {claudeReturn}, "code_verifier": {"short"}},
			nil, 400, "invalid_request"},
		"another resource": {url.Values{"grant_type": {"refresh_token"}, "resource": {"https://elsewhere.example.com/api/mcp"}}, nil, 400, "invalid_target"},
		"a bad refresh":    {refreshForm("vtr_nope", claudeID), nil, 400, "invalid_grant"},
		"an Origin":        {url.Values{}, []modtest.RequestOption{modtest.Header("Origin", "https://claude.ai"), modtest.Header("Sec-Fetch-Site", "cross-site")}, 400, "invalid_request"},
	} {
		r, out := tokenRequest(anon, c.form, c.opts...)
		if r.Status != c.status || out.Error != c.code || r.Header("Cache-Control") != "no-store" || !strings.HasPrefix(r.Header("Content-Type"), "application/json") {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	r = anon.Do(http.MethodPost, "/api/oauth/token", nil, oauthTokenSkip, modtest.RawBody("application/x-www-form-urlencoded", []byte("grant_type=refresh_token&grant_type=refresh_token")))
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "repeated") {
		t.Errorf("a repeated parameter: %d %s", r.Status, r.Body)
	}
	r = anon.Do(http.MethodPost, "/api/oauth/token", map[string]any{"grant_type": "refresh_token"}, oauthTokenSkip)
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "invalid_request") {
		t.Errorf("a JSON body: %d %s", r.Status, r.Body)
	}
}

// A code is spent once (C15): presented again it revokes the connection,
// and the tokens of the first exchange stop.
func TestOAuth_ACodeReplayed(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	code := approveCode(t, owner, authorizeQuery(claudeID, claudeReturn, "read"), integration, "read")

	// A wrong verifier spends the code: it gets no second try.
	wrong := codeForm(code, claudeID, claudeReturn)
	wrong.Set("code_verifier", strings.Repeat("A", 43))
	if r, out := tokenRequest(h.Client(t), wrong); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("a wrong verifier: %d %s", r.Status, r.Body)
	}

	r, tok := tokenRequest(h.Client(t), codeForm(code, claudeID, claudeReturn))
	if r.Status != http.StatusBadRequest || tok.Error != "invalid_grant" {
		t.Fatalf("the code after a wrong verifier: %d %s", r.Status, r.Body)
	}

	code = approveCode(t, owner, authorizeQuery(claudeID, claudeReturn, "read"), integration, "read")
	r, tok = tokenRequest(h.Client(t), codeForm(code, claudeID, claudeReturn))
	if r.Status != http.StatusOK {
		t.Fatalf("exchange: %d %s", r.Status, r.Body)
	}
	if r := h.APIKeyClient(t, tok.AccessToken).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a read")); r.Status != http.StatusOK {
		t.Fatalf("the token: %d", r.Status)
	}
	if r, out := tokenRequest(h.Client(t), codeForm(code, claudeID, claudeReturn)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("the replay: %d %s", r.Status, r.Body)
	}
	connection := connectionOf(t, h, integration)
	if reason := modtest.One[string](t, h, `SELECT coalesce(revoke_reason, '') FROM identity.oauth_connections WHERE id = $1`, connection); reason != "code_replayed" {
		t.Errorf("the connection's reason %q", reason)
	}
	if r := h.APIKeyClient(t, tok.AccessToken).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")); r.Status != http.StatusUnauthorized || r.Code() != "token_invalid" {
		t.Errorf("the first exchange's token after the replay: %d %s", r.Status, r.Body)
	}
	if r, _ := tokenRequest(h.Client(t), refreshForm(tok.RefreshToken, claudeID)); r.Status != http.StatusBadRequest {
		t.Errorf("its refresh token: %d %s", r.Status, r.Body)
	}
}

// revokeReason is the connection's revoke_reason, "" while it is live.
func revokeReason(t *testing.T, h *modtest.Harness, connection uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT coalesce(revoke_reason, '') FROM identity.oauth_connections WHERE id = $1`, connection)
}

// unspentRefreshTokens is how many refresh tokens of the connection are live.
func unspentRefreshTokens(t *testing.T, h *modtest.Harness, connection uuid.UUID) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM identity.oauth_tokens WHERE connection_id = $1 AND kind = 'refresh' AND used_at IS NULL`, connection)
}

// Refresh (C17, A10, A11): each use issues a new pair; a spent token is
// honoured once more within 60 seconds, which supersedes the first pair's
// refresh token; after that it revokes the connection. An access token is
// never a refresh token, nor the other way round.
func TestOAuth_RefreshGraceAndReuse(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	first := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	connection := connectionOf(t, h, integration)
	anon := h.Client(t)

	// The same bytes under the other prefix are refused (A10).
	if r, out := tokenRequest(anon, refreshForm("vtr_"+first.AccessToken[4:], claudeID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("an access token as a refresh token: %d %s", r.Status, r.Body)
	}
	if r := h.APIKeyClient(t, "vta_"+first.RefreshToken[4:]).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")); r.Status != http.StatusUnauthorized {
		t.Errorf("a refresh token as an access token: %d %s", r.Status, r.Body)
	}
	// An unspent token sent with another client_id or a wider scope is
	// refused, and neither spends it nor ends the connection.
	if r, out := tokenRequest(anon, refreshForm(first.RefreshToken, chatGPTID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("another client_id: %d %s", r.Status, r.Body)
	}
	if r, out := tokenRequest(anon, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {claudeID}, "scope": {"read write"}}); r.Status != http.StatusBadRequest || out.Error != "invalid_scope" {
		t.Errorf("a wider scope: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "" || unspentRefreshTokens(t, h, connection) != 1 {
		t.Fatalf("after the refused refreshes: %q, %d unspent", reason, unspentRefreshTokens(t, h, connection))
	}

	r, second := tokenRequest(anon, refreshForm(first.RefreshToken, claudeID))
	if r.Status != http.StatusOK || second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	// The client lost the answer and retries within the grace: a new pair.
	h.Advance(30 * time.Second)
	r, retry := tokenRequest(anon, refreshForm(first.RefreshToken, claudeID))
	if r.Status != http.StatusOK || unspentRefreshTokens(t, h, connection) != 1 {
		t.Fatalf("the retry: %d %s", r.Status, r.Body)
	}
	// A third use within the grace is refused, and ends nothing.
	if r, out := tokenRequest(anon, refreshForm(first.RefreshToken, claudeID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("a third use within the grace: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "" {
		t.Fatalf("revoked within the grace: %q", reason)
	}
	r, third := tokenRequest(anon, refreshForm(retry.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("refresh the retry's: %d %s", r.Status, r.Body)
	}
	// The spent token presented after the grace revokes the connection,
	// whatever client_id and scope it is sent with (the presenter holds it).
	h.Advance(2 * time.Minute)
	reuse := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {retry.RefreshToken}, "client_id": {chatGPTID}, "scope": {"read write"}}
	if r, out := tokenRequest(anon, reuse); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("reuse after the grace: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "refresh_reused" {
		t.Errorf("the connection's reason %q", reason)
	}
	if r := h.APIKeyClient(t, third.AccessToken).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")); r.Status != http.StatusUnauthorized {
		t.Errorf("the newest token after reuse: %d", r.Status)
	}

	// An access token lives an hour.
	fresh := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	h.Advance(time.Hour + time.Second)
	if r := h.APIKeyClient(t, fresh.AccessToken).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")); r.Status != http.StatusUnauthorized || r.Code() != "token_invalid" {
		t.Errorf("an hour-old token: %d %s", r.Status, r.Body)
	}
	if r, _ := tokenRequest(anon, refreshForm(fresh.RefreshToken, claudeID)); r.Status != http.StatusOK {
		t.Errorf("its refresh token after the hour: %d %s", r.Status, r.Body)
	}
}

// A grace replay by a second party (A25): the client spends R0 for R1; a
// thief replays R0 within the grace and gets a pair, which supersedes R1;
// when the client then presents R1 the family has two holders, and the
// connection is revoked, the thief's tokens with it.
func TestOAuth_AGraceReplayBySomeoneElse(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	first := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	connection := connectionOf(t, h, integration)
	client, thief := h.Client(t), h.Client(t)

	r, own := tokenRequest(client, refreshForm(first.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("the client's refresh: %d %s", r.Status, r.Body)
	}
	h.Advance(10 * time.Second)
	r, stolen := tokenRequest(thief, refreshForm(first.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("the replay within the grace: %d %s", r.Status, r.Body)
	}
	if r, out := tokenRequest(client, refreshForm(own.RefreshToken, claudeID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("the superseded token: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "refresh_reused" {
		t.Errorf("the connection's reason %q, want refresh_reused", reason)
	}
	if r := h.APIKeyClient(t, stolen.AccessToken).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")); r.Status != http.StatusUnauthorized {
		t.Errorf("the thief's access token: %d", r.Status)
	}
	if r, _ := tokenRequest(thief, refreshForm(stolen.RefreshToken, claudeID)); r.Status != http.StatusBadRequest {
		t.Errorf("the thief's refresh token: %d %s", r.Status, r.Body)
	}
}

// A refresh issues nothing the next request would refuse: with the
// approver no longer an Owner, or the integration disabled, it is
// invalid_grant, and the token is left unspent and the connection live
// (a revocation is the change's own business).
func TestOAuth_ARefreshOfAConnectionThatCannotAct(t *testing.T) {
	h := oauthHarness(t)
	owner, ownerID := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	tok := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	connection := connectionOf(t, h, integration)
	anon := h.Client(t)

	h.Exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, ownerID, identity.RoleOwnerID)
	if r, out := tokenRequest(anon, refreshForm(tok.RefreshToken, claudeID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("the approver no longer an Owner: %d %s", r.Status, r.Body)
	}
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, ownerID, identity.RoleOwnerID)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, integration)
	if r, out := tokenRequest(anon, refreshForm(tok.RefreshToken, claudeID)); r.Status != http.StatusBadRequest || out.Error != "invalid_grant" {
		t.Errorf("the integration disabled: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "" || unspentRefreshTokens(t, h, connection) != 1 {
		t.Fatalf("after the refused refreshes: %q, %d unspent", reason, unspentRefreshTokens(t, h, connection))
	}
	h.Exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, integration)
	if r, _ := tokenRequest(anon, refreshForm(tok.RefreshToken, claudeID)); r.Status != http.StatusOK {
		t.Errorf("enabled again: %d %s", r.Status, r.Body)
	}
}

// Races (code review M2): two exchanges of one code at once, two refreshes
// of one token at once, and a refresh token with its child at once. Each
// request runs in a parallel subtest of its own, released together.
func TestOAuth_Races(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")

	// race sends each form at once and answers the statuses in order.
	race := func(name string, forms ...url.Values) []int {
		t.Helper()
		statuses := make([]int, len(forms))
		release := make(chan struct{})
		var ready sync.WaitGroup
		ready.Add(len(forms))
		t.Run(name, func(t *testing.T) {
			for i, form := range forms {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					t.Parallel()
					c := h.Client(t)
					ready.Done()
					<-release
					r, _ := tokenRequest(c, form)
					statuses[i] = r.Status
				})
			}
			go func() {
				ready.Wait()
				close(release)
			}()
		})
		return statuses
	}

	// One code, two exchanges: one wins, and the other revokes the
	// connection as a replay.
	code := approveCode(t, owner, authorizeQuery(claudeID, claudeReturn, "read"), integration, "read")
	got := race("one code twice", codeForm(code, claudeID, claudeReturn), codeForm(code, claudeID, claudeReturn))
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusOK, http.StatusBadRequest}) {
		t.Errorf("two exchanges: %v", got)
	}
	if reason := revokeReason(t, h, connectionOf(t, h, integration)); reason != "code_replayed" {
		t.Errorf("after two exchanges: %q, want code_replayed", reason)
	}

	// One refresh token, two refreshes: the second waits for the first and
	// is honoured as its grace retry; the connection never has two live
	// refresh tokens.
	h.Advance(time.Second)
	tok := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	connection := connectionOf(t, h, integration)
	got = race("one token twice", refreshForm(tok.RefreshToken, claudeID), refreshForm(tok.RefreshToken, claudeID))
	if !slices.Equal(got, []int{http.StatusOK, http.StatusOK}) {
		t.Errorf("two refreshes: %v", got)
	}
	if n, reason := unspentRefreshTokens(t, h, connection), revokeReason(t, h, connection); n != 1 || reason != "" {
		t.Errorf("after two refreshes: %d unspent, %q", n, reason)
	}

	// A refresh token and its child at once: whichever runs second either
	// is the parent's grace retry or finds itself superseded and revokes;
	// never two live refresh tokens.
	h.Advance(time.Second)
	tok = connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	connection = connectionOf(t, h, integration)
	r, child := tokenRequest(h.Client(t), refreshForm(tok.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	got = race("a token and its child", refreshForm(tok.RefreshToken, claudeID), refreshForm(child.RefreshToken, claudeID))
	reason := revokeReason(t, h, connection)
	switch {
	case reason == "refresh_reused" && slices.Contains(got, http.StatusBadRequest):
	case reason == "" && slices.Equal(got, []int{http.StatusOK, http.StatusOK}) && unspentRefreshTokens(t, h, connection) == 1:
	default:
		t.Errorf("a token and its child: %v, %q, %d unspent", got, reason, unspentRefreshTokens(t, h, connection))
	}
	if n := unspentRefreshTokens(t, h, connection); n > 1 {
		t.Errorf("%d live refresh tokens", n)
	}
}

// Discovery (C12, A13), with and without a base path, and the 404 every
// other well-known path answers instead of the SPA.
func TestOAuth_Discovery(t *testing.T) {
	for _, base := range []string{"", "/erp"} {
		var opts []modtest.Option
		if base != "" {
			opts = append(opts, modtest.WithEnv("APP_BASE_PATH", base))
		}
		h := oauthHarness(t, opts...)
		c := h.Client(t)
		issuer := oauthOrigin + base
		get := func(path string) map[string]any {
			t.Helper()
			r := c.Do(http.MethodGet, path, nil, modtest.SkipContract("discovery is in no contract"))
			if r.Status != http.StatusOK || r.Header("Cache-Control") != "public, max-age=300" || r.Header("Content-Type") != "application/json" {
				t.Fatalf("%s: %d %s", path, r.Status, r.Body)
			}
			var doc map[string]any
			r.JSON(&doc)
			return doc
		}
		for _, path := range []string{"/.well-known/oauth-protected-resource" + base + "/api/mcp", "/.well-known/oauth-protected-resource"} {
			doc := get(path)
			if doc["resource"] != issuer+"/api/mcp" || doc["authorization_servers"].([]any)[0] != issuer || doc["resource_name"] != "Vantigo" {
				t.Errorf("%s: %v", path, doc)
			}
		}
		paths := []string{"/.well-known/oauth-authorization-server" + base, base + "/.well-known/oauth-authorization-server"}
		for _, path := range paths {
			doc := get(path)
			if doc["issuer"] != issuer || doc["authorization_endpoint"] != issuer+"/oauth/authorize" || doc["token_endpoint"] != issuer+"/api/oauth/token" ||
				doc["client_id_metadata_document_supported"] != true || doc["authorization_response_iss_parameter_supported"] != true ||
				doc["revocation_endpoint"] != issuer+"/api/oauth/revoke" {
				t.Errorf("%s: %v", path, doc)
			}
			if methods, _ := doc["revocation_endpoint_auth_methods_supported"].([]any); len(methods) != 1 || methods[0] != "none" {
				t.Errorf("%s: %v", path, doc["revocation_endpoint_auth_methods_supported"])
			}
			if methods, _ := doc["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
				t.Errorf("%s: %v", path, doc["code_challenge_methods_supported"])
			}
		}
		for _, path := range []string{"/.well-known/openid-configuration", "/.well-known/security.txt", base + "/.well-known/oauth-protected-resource/elsewhere"} {
			r := c.Do(http.MethodGet, path, nil, modtest.SkipContract("discovery is in no contract"))
			if r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "<html") {
				t.Errorf("%s: %d %s", path, r.Status, r.Body)
			}
		}
	}
}

// Switched off (C24, A24): the discovery documents and the token endpoint
// are 404, a vta_ token is an unknown key's 401, and the MCP 401 is M6's.
func TestOAuth_SwitchedOff(t *testing.T) {
	h := mcpHarness(t, modtest.WithEnv("OAUTH_ENABLED", "0"))
	c := h.Client(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/api/mcp", "/.well-known/oauth-authorization-server"} {
		if r := c.Do(http.MethodGet, path, nil, modtest.SkipContract("discovery")); r.Status != http.StatusNotFound {
			t.Errorf("%s: %d", path, r.Status)
		}
	}
	if r, _ := tokenRequest(c, url.Values{"grant_type": {"refresh_token"}}); r.Status != http.StatusNotFound {
		t.Errorf("the token endpoint: %d %s", r.Status, r.Body)
	}
	if r := revokeRequest(c, url.Values{"token": {"vta_x"}, "client_id": {claudeID}}); r.Status != http.StatusNotFound {
		t.Errorf("the revocation endpoint: %d %s", r.Status, r.Body)
	}
	if r := mcpCall(c, "tools/list", nil); r.Status != http.StatusUnauthorized || r.Header("WWW-Authenticate") != `Bearer realm="vantigo"` {
		t.Errorf("MCP without a key: %d %q", r.Status, r.Header("WWW-Authenticate"))
	}
	token, _, _ := identity.MintAPIKey()
	token = "vta_" + token[4:]
	for name, r := range map[string]*modtest.Response{
		"mcp":  mcpCall(h.APIKeyClient(t, token), "tools/list", nil),
		"rest": h.APIKeyClient(t, token).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal")),
	} {
		if r.Status != http.StatusUnauthorized || r.Header("WWW-Authenticate") != `Bearer realm="vantigo"` {
			t.Errorf("a vta_ token at %s: %d %q %s", name, r.Status, r.Header("WWW-Authenticate"), r.Body)
		}
	}
}

// A pool of one connection (the lease-worker lessons): the flow, a tool
// call, a refresh, a code replay, a reuse after the grace, an Owner's
// demotion and an integration's archive each finish without holding one
// connection while waiting for a second. A request that did would wait out
// the client's timeout and fail here.
func TestOAuth_APoolOfOne(t *testing.T) {
	h := oauthHarness(t, modtest.WithPoolMaxConns(1))
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")

	tok := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	if _, _, isError := mcpTool(t, h.APIKeyClient(t, tok.AccessToken), "read", map[string]any{"operation": "getCustomers"}); isError {
		t.Error("the read failed on a pool of one")
	}
	r, next := tokenRequest(h.Client(t), refreshForm(tok.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	if r, _ := tokenRequest(h.Client(t), refreshForm(next.RefreshToken, claudeID)); r.Status != http.StatusOK {
		t.Fatalf("refresh again: %d %s", r.Status, r.Body)
	}
	h.Advance(2 * time.Minute)
	if r, _ := tokenRequest(h.Client(t), refreshForm(next.RefreshToken, claudeID)); r.Status != http.StatusBadRequest {
		t.Errorf("reuse after the grace: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connectionOf(t, h, integration)); reason != "refresh_reused" {
		t.Errorf("reuse: %q", reason)
	}

	h.Advance(time.Second)
	code := approveCode(t, owner, authorizeQuery(claudeID, claudeReturn, "read"), integration, "read")
	if r, _ := tokenRequest(h.Client(t), codeForm(code, claudeID, claudeReturn)); r.Status != http.StatusOK {
		t.Fatalf("exchange: %d %s", r.Status, r.Body)
	}
	if r, _ := tokenRequest(h.Client(t), codeForm(code, claudeID, claudeReturn)); r.Status != http.StatusBadRequest {
		t.Errorf("the replay: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, connectionOf(t, h, integration)); reason != "code_replayed" {
		t.Errorf("replay: %q", reason)
	}

	// Another Owner connects, and is demoted to User.
	other, otherID := signInOwner(t, h)
	h.Advance(time.Second)
	connectClient(t, h, other, integration, "read", claudeID, claudeReturn)
	demoted := connectionOf(t, h, integration)
	email := modtest.One[string](t, h, `SELECT email FROM identity.users WHERE id = $1`, otherID)
	if r := owner.Do(http.MethodPut, "/api/v1/identity/owner/users/"+otherID.String(),
		map[string]any{"displayName": email, "email": email, "role": "User"}, oauthSkip); r.Status != http.StatusOK {
		t.Fatalf("demote: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, demoted); reason != "authorizer_removed" {
		t.Errorf("demotion: %q", reason)
	}

	h.Advance(time.Second)
	connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	archived := connectionOf(t, h, integration)
	if r := owner.Do(http.MethodPost, "/api/v1/identity/integrations/"+integration.String()+"/archive", nil, oauthSkip); r.Status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.Status, r.Body)
	}
	if reason := revokeReason(t, h, archived); reason != "integration_archived" {
		t.Errorf("archive: %q", reason)
	}
}

// With MCP off the protected resource's metadata is not served, so no
// challenge names it (A13): a token's 401 is Bearer error="invalid_token"
// alone.
func TestOAuth_MCPOffNamesNoMetadata(t *testing.T) {
	h := oauthHarness(t, modtest.WithEnv("MCP_ENABLED", "0"))
	token, _, _ := identity.MintAPIKey()
	token = "vta_" + token[4:]
	r := h.APIKeyClient(t, token).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a refusal"))
	if r.Status != http.StatusUnauthorized || r.Header("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Errorf("a token with MCP off: %d %q", r.Status, r.Header("WWW-Authenticate"))
	}
	if r := h.Client(t).Do(http.MethodGet, "/.well-known/oauth-protected-resource/api/mcp", nil, modtest.SkipContract("discovery")); r.Status != http.StatusNotFound {
		t.Errorf("the protected resource's metadata with MCP off: %d", r.Status)
	}
}
