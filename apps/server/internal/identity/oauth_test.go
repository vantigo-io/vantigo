package identity_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// OAuth connections (OAuth connections design C13-C18, C25, A6, A17, A20)
// through identity's own operations: the consent page's preview and
// approval, the token endpoint's exchange, the connections list and Revoke,
// and what ends a connection by itself.

const (
	oauthPreviewPath   = "/api/v1/identity/oauth/authorizations/preview"
	oauthApprovePath   = "/api/v1/identity/oauth/authorizations"
	oauthTokenPath     = "/api/oauth/token"
	hostedClaudeID     = "https://claude.ai/oauth/mcp-oauth-client-metadata"
	hostedClaudeReturn = "https://claude.ai/api/mcp/auth_callback"
	oauthVerifier      = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

func oauthChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthHarness is an rbacHarness with OAuth on and the three live client
// documents served from a fake host (A1).
func oauthHarness(t *testing.T) (*harness, *client, uuid.UUID, *modtest.OAuthClientHost) {
	t.Helper()
	h, owner, ownerID := rbacHarness(t, withEnv("OAUTH_ENABLED", "1"))
	host := modtest.NewOAuthClientHost(t, "claude.ai", "chatgpt.com", "example.com")
	for file, id := range map[string]string{
		"claude.json":      hostedClaudeID,
		"claude-code.json": "https://claude.ai/oauth/claude-code-client-metadata",
		"chatgpt.json":     "https://chatgpt.com/oauth/client.json",
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "oauth", file))
		if err != nil {
			t.Fatal(err)
		}
		host.Serve(id, b, "max-age=600")
	}
	host.Attach(h.access)
	return h, owner, ownerID, host
}

// authorizationQuery is a conforming client's authorization request.
func authorizationQuery(clientID, redirect string, extra ...string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {oauthChallenge(oauthVerifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"af0ifjsldkj"},
		"scope":                 {"read write"},
		"resource":              {harnessOrigin + "/api/mcp"},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			q.Del(extra[i])
		} else {
			q.Set(extra[i], extra[i+1])
		}
	}
	return q.Encode()
}

type oauthPreview struct {
	Outcome    string  `json:"outcome"`
	Error      *string `json:"error"`
	RedirectTo *string `json:"redirectTo"`
	Automatic  *bool   `json:"automatic"`
	Client     *struct {
		Name         string `json:"name"`
		ClientIDHost string `json:"clientIdHost"`
		RedirectHost string `json:"redirectHost"`
		Loopback     bool   `json:"loopback"`
	} `json:"client"`
	Scopes       []string `json:"scopes"`
	FirstUse     *bool    `json:"firstUse"`
	Integrations []struct {
		ID        uuid.UUID `json:"id"`
		Eligible  bool      `json:"eligible"`
		MaxAccess *string   `json:"maxAccess"`
		Reason    *string   `json:"reason"`
	} `json:"integrations"`
}

func previewOf(t *testing.T, c *client, query string) oauthPreview {
	t.Helper()
	r := c.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": query})
	if r.status != http.StatusOK {
		t.Fatalf("preview: %d %s", r.status, r.body)
	}
	if got := r.header("Cache-Control"); got != "no-store" {
		t.Errorf("the preview's Cache-Control = %q", got)
	}
	var p oauthPreview
	r.json(&p)
	return p
}

// approve approves query for integration with access and answers the code
// the redirect carries.
func approve(t *testing.T, c *client, query string, integration uuid.UUID, access string) (code string, redirect *url.URL) {
	t.Helper()
	r := c.do(http.MethodPost, oauthApprovePath, map[string]any{"query": query, "integrationId": integration, "access": access, "decision": "approve"})
	if r.status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.status, r.body)
	}
	if got := r.header("Cache-Control"); got != "no-store" {
		t.Errorf("the approval's Cache-Control = %q", got)
	}
	var out struct {
		RedirectTo string `json:"redirectTo"`
	}
	r.json(&out)
	u, err := url.Parse(out.RedirectTo)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("code"), u
}

// exchange posts the code to the token endpoint and answers its tokens.
func exchange(t *testing.T, h *harness, code, clientID, redirect string) (access, refresh string) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {oauthVerifier}}
	r := h.client(t).do(http.MethodPost, oauthTokenPath, nil, rawBody("application/x-www-form-urlencoded", []byte(form.Encode())),
		skipContract("the token endpoint is RFC 6749's, in no contract"))
	if r.status != http.StatusOK {
		t.Fatalf("exchange: %d %s", r.status, r.body)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatal(err)
	}
	if out.TokenType != "Bearer" || out.ExpiresIn != 3600 || !strings.HasPrefix(out.AccessToken, "vta_") || !strings.HasPrefix(out.RefreshToken, "vtr_") {
		t.Errorf("tokens %+v", out)
	}
	return out.AccessToken, out.RefreshToken
}

// connect runs the flow to the tokens for integration with access.
func connect(t *testing.T, h *harness, owner *client, integration uuid.UUID, access string) (token string, connection uuid.UUID) {
	t.Helper()
	// The clock moves on, so this connection is the newest by created_at.
	h.advance(time.Second)
	code, _ := approve(t, owner, authorizationQuery(hostedClaudeID, hostedClaudeReturn), integration, access)
	token, _ = exchange(t, h, code, hostedClaudeID, hostedClaudeReturn)
	if err := h.pool.QueryRow(t.Context(), `SELECT id FROM identity.oauth_connections WHERE integration_id = $1 ORDER BY created_at DESC LIMIT 1`, integration).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	return token, connection
}

var anonymousRead = contracts.Rule{Kind: contracts.RuleAnonymous, API: contracts.APIRead}

func setIntegrationMCP(t *testing.T, h *harness, integration uuid.UUID, mode string) {
	t.Helper()
	h.exec(t, `UPDATE identity.integrations SET mcp_access = $2 WHERE user_id = $1`, integration, mode)
}

type listedConnection struct {
	ID             uuid.UUID `json:"id"`
	ClientName     string    `json:"clientName"`
	ClientHost     string    `json:"clientHost"`
	Scopes         []string  `json:"scopes"`
	Status         string    `json:"status"`
	InactiveReason *string   `json:"inactiveReason"`
}

func connectionsOf(t *testing.T, c *client, integration uuid.UUID) []listedConnection {
	t.Helper()
	r := c.do(http.MethodGet, integrationsPath+"/"+integration.String()+"/connections", nil)
	if r.status != http.StatusOK {
		t.Fatalf("connections: %d %s", r.status, r.body)
	}
	var out struct {
		Connections []listedConnection `json:"connections"`
	}
	r.json(&out)
	return out.Connections
}

// The whole flow through identity: the preview shows the client as it calls
// itself beside the host Vantigo fetched it from, the approval redirects
// with a code, state and iss, the exchange activates the connection (audited
// connection.authorized), the list shows it, and Revoke ends it (audited
// connection.revoked) so its token is refused at the next request.
func TestOAuth_ConsentApproveListAndRevoke(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, host := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read_write")
	off := createIntegration(t, owner, "No MCP").Integration.ID

	query := authorizationQuery(hostedClaudeID, hostedClaudeReturn)
	p := previewOf(t, owner, query)
	if p.Outcome != "consent" || p.Client == nil || p.Client.Name != "Claude" || p.Client.ClientIDHost != "claude.ai" ||
		p.Client.RedirectHost != "claude.ai" || p.Client.Loopback || p.FirstUse == nil || !*p.FirstUse ||
		strings.Join(p.Scopes, " ") != "read write" {
		t.Errorf("preview %+v %+v", p, p.Client)
	}
	eligible := map[uuid.UUID]bool{}
	for _, i := range p.Integrations {
		eligible[i.ID] = i.Eligible
		if i.ID == agent && (i.MaxAccess == nil || *i.MaxAccess != "read_write") {
			t.Errorf("the agent's max access %v", i.MaxAccess)
		}
		if i.ID == off && (i.Reason == nil || *i.Reason != "mcp_off") {
			t.Errorf("the MCP-off integration's reason %v", i.Reason)
		}
	}
	if !eligible[agent] || eligible[off] {
		t.Errorf("eligible %v", eligible)
	}
	// A second preview reads the cached document.
	_ = previewOf(t, owner, query)
	if n := host.Fetches(hostedClaudeID); n != 1 {
		t.Errorf("the document was fetched %d times, want 1 (cached)", n)
	}

	if r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": query, "integrationId": off, "access": "read", "decision": "approve"}); r.status != http.StatusConflict || r.code() != "integration_not_eligible" {
		t.Errorf("an MCP-off integration: %d %s", r.status, r.body)
	}
	code, redirect := approve(t, owner, query, agent, "read_write")
	if redirect.Host != "claude.ai" || redirect.Path != "/api/mcp/auth_callback" || redirect.Query().Get("state") != "af0ifjsldkj" ||
		redirect.Query().Get("iss") != harnessOrigin || code == "" {
		t.Errorf("redirect %s", redirect)
	}
	if got := connectionsOf(t, owner, agent); len(got) != 0 {
		t.Errorf("an unexchanged code lists %+v", got)
	}
	token, _ := exchange(t, h, code, hostedClaudeID, hostedClaudeReturn)
	got := connectionsOf(t, owner, agent)
	if len(got) != 1 || got[0].ClientName != "Claude" || got[0].ClientHost != "claude.ai" || got[0].Status != "active" || strings.Join(got[0].Scopes, " ") != "read write" {
		t.Fatalf("connections %+v", got)
	}
	if events := h.auditEvents(t, "connection.authorized"); len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID ||
		events[0].TargetUser == nil || *events[0].TargetUser != agent || strings.Contains(events[0].After, token) {
		t.Errorf("connection.authorized %+v", events)
	}

	pr, err := h.access.Check(keyRequest(http.MethodGet, token), anonymousRead)
	if err != nil || pr.UserID != agent || pr.Via == nil || pr.Via.ConnectionID != got[0].ID || pr.Via.KeyID != uuid.Nil {
		t.Fatalf("the token: %+v %v", pr, err)
	}
	m, err := h.access.MCP(t.Context(), pr, false)
	if err != nil || m.Effective() != contracts.MCPAccessReadWrite {
		t.Errorf("MCP %+v %v", m, err)
	}

	if r := owner.do(http.MethodDelete, integrationsPath+"/"+agent.String()+"/connections/"+got[0].ID.String(), nil); r.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, integrationsPath+"/"+agent.String()+"/connections/"+got[0].ID.String(), nil); r.status != http.StatusNoContent {
		t.Errorf("revoke again: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, integrationsPath+"/"+agent.String()+"/connections/"+uuid.NewString(), nil); r.status != http.StatusNotFound {
		t.Errorf("revoke an unknown one: %d %s", r.status, r.body)
	}
	if events := h.auditEvents(t, "connection.revoked"); len(events) != 1 || !strings.Contains(events[0].After, `"revokeReason":"revoked"`) {
		t.Errorf("connection.revoked %+v", events)
	}
	if got := connectionsOf(t, owner, agent); len(got) != 0 {
		t.Errorf("a revoked connection lists %+v", got)
	}
	var refusal *contracts.APIKeyRefusal
	if _, err := h.access.Check(keyRequest(http.MethodGet, token), anonymousRead); !errors.As(err, &refusal) || refusal.Reason != "revoked" || !errors.Is(err, contracts.ErrInvalidToken) {
		t.Errorf("a revoked connection's token: %v", err)
	}
}

// What the preview refuses (A6): a client that cannot be fetched, a
// redirect URI it does not register, and the client's own host are
// client_refused and never redirected; a request wrong once the client is
// known redirects, automatically when the redirect goes to the client's
// host or loopback. Only an Owner previews, and nothing answers while OAuth
// is off.
func TestOAuth_PreviewRefusals(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	for name, q := range map[string]string{
		"an unknown document":     authorizationQuery("https://example.com/missing.json", "https://example.com/cb"),
		"a foreign redirect":      authorizationQuery(hostedClaudeID, "https://evil.example.com/cb"),
		"the installation's host": authorizationQuery("https://identity.example.com/client.json", "https://identity.example.com/cb"),
		"an http client_id":       authorizationQuery("http://claude.ai/oauth/mcp-oauth-client-metadata", hostedClaudeReturn),
		"a repeated client_id":    authorizationQuery(hostedClaudeID, hostedClaudeReturn) + "&client_id=" + url.QueryEscape(hostedClaudeID),
		"localhost for 127.0.0.1": authorizationQuery("https://claude.ai/oauth/claude-code-client-metadata", "http://127.0.0.2:4000/callback"),
	} {
		if p := previewOf(t, owner, q); p.Outcome != "client_refused" || p.RedirectTo != nil {
			t.Errorf("%s: %+v", name, p)
		}
	}
	// Until a client of claude.ai has been connected here, no error is
	// redirected without a click (A26); afterwards those whose redirect goes
	// to the client's host or loopback are.
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	for _, connected := range []bool{false, true} {
		h.advance(time.Minute) // a fresh window of the consent limit
		if connected {
			connect(t, h, owner, agent, "read")
		}
		for name, c := range map[string]struct{ query, code string }{
			"response_type token":  {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "response_type", "token"), "unsupported_response_type"},
			"no PKCE":              {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "code_challenge", ""), "invalid_request"},
			"plain PKCE":           {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "code_challenge_method", "plain"), "invalid_request"},
			"an unknown scope":     {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "scope", "read admin"), "invalid_scope"},
			"another resource":     {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "resource", "https://other.example.com/api/mcp"), "invalid_target"},
			"a long state":         {authorizationQuery(hostedClaudeID, hostedClaudeReturn, "state", strings.Repeat("s", 513)), "invalid_request"},
			"a loopback redirect":  {authorizationQuery("https://claude.ai/oauth/claude-code-client-metadata", "http://localhost:53682/callback", "response_type", "token"), "unsupported_response_type"},
			"a repeated parameter": {authorizationQuery(hostedClaudeID, hostedClaudeReturn) + "&scope=read", "invalid_request"},
		} {
			p := previewOf(t, owner, c.query)
			if p.Outcome != "redirect" || p.Error == nil || *p.Error != c.code || p.Automatic == nil || *p.Automatic != connected || p.RedirectTo == nil {
				t.Errorf("%s (connected before %v): %+v", name, connected, p)
				continue
			}
			u, _ := url.Parse(*p.RedirectTo)
			if u.Query().Get("error") != c.code || u.Query().Get("iss") != harnessOrigin {
				t.Errorf("%s: redirect %s", name, u)
			}
			if name == "a long state" && u.Query().Has("state") {
				t.Errorf("a state over its bound was echoed")
			}
		}
	}
	// Previews and approvals together are limited per person (A2).
	h.advance(time.Minute)
	limited := 0
	for range 21 {
		if r := owner.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": authorizationQuery(hostedClaudeID, hostedClaudeReturn)}); r.status == http.StatusTooManyRequests {
			limited++
		}
	}
	if r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": authorizationQuery(hostedClaudeID, hostedClaudeReturn), "decision": "deny"}); r.status != http.StatusTooManyRequests || r.header("Retry-After") == "" {
		t.Errorf("an approval over the limit: %d %s", r.status, r.body)
	}
	if limited != 1 {
		t.Errorf("%d of 21 previews limited, want 1", limited)
	}
	h.advance(time.Minute)
	// Deny answers the client access_denied.
	r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": authorizationQuery(hostedClaudeID, hostedClaudeReturn), "decision": "deny"})
	var denied struct {
		RedirectTo string `json:"redirectTo"`
	}
	r.json(&denied)
	if u, _ := url.Parse(denied.RedirectTo); r.status != http.StatusOK || u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "af0ifjsldkj" {
		t.Errorf("deny: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": authorizationQuery(hostedClaudeID, "https://evil.example.com/cb"), "decision": "deny"}); r.status != http.StatusBadRequest || r.code() != "client_refused" {
		t.Errorf("deny to a foreign redirect: %d %s", r.status, r.body)
	}

	// A person who is not an Owner is refused before anything is fetched.
	userID := h.createUser(t, owner, "member@example.com", "User")
	member := h.signIn(t, userID, false)
	if r := member.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": authorizationQuery(hostedClaudeID, hostedClaudeReturn)}); r.status != http.StatusForbidden {
		t.Errorf("a non-Owner's preview: %d %s", r.status, r.body)
	}

	// Switched off, every operation is 404 and an access token is no caller.
	offH, offOwner, _ := rbacHarness(t)
	id := createIntegration(t, offOwner, "Agent").Integration.ID
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, oauthPreviewPath},
		{http.MethodPost, oauthApprovePath},
		{http.MethodGet, integrationsPath + "/" + id.String() + "/connections"},
		{http.MethodDelete, integrationsPath + "/" + id.String() + "/connections/" + uuid.NewString()},
	} {
		var body any
		if c.method == http.MethodPost {
			body = map[string]any{"query": "x", "decision": "deny"}
		}
		if r := offOwner.do(c.method, c.path, body); r.status != http.StatusNotFound {
			t.Errorf("off: %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	token, _ := identity.NewAPIKeyForTest()
	token = "vta_" + token[4:]
	if _, err := offH.access.Check(keyRequest(http.MethodGet, token), anonymousRead); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Errorf("off: a vta_ token: %v", err)
	}
	if r := offH.client(t).do(http.MethodPost, oauthTokenPath, nil, rawBody("application/x-www-form-urlencoded", []byte("grant_type=refresh_token")),
		skipContract("the token endpoint is in no contract")); r.status != http.StatusNotFound {
		t.Errorf("off: the token endpoint: %d %s", r.status, r.body)
	}
}

// What ends a connection by itself (A17, A20): its authorizer demoted from
// Owner, disabled or deleted, and its integration archived, each audited
// connection.revoked with its reason, and the token refused.
func TestOAuth_TheAuthorizerAndTheArchive(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")

	refused := func(what, token, reason string) {
		t.Helper()
		var refusal *contracts.APIKeyRefusal
		if _, err := h.access.Check(keyRequest(http.MethodGet, token), anonymousRead); !errors.As(err, &refusal) || refusal.Reason != reason {
			t.Errorf("%s: %v", what, err)
		}
	}
	revokedFor := func(connection uuid.UUID) string {
		t.Helper()
		var reason string
		if err := h.pool.QueryRow(t.Context(), `SELECT coalesce(revoke_reason, '') FROM identity.oauth_connections WHERE id = $1`, connection).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		return reason
	}

	for _, change := range []string{"demote", "disable", "delete"} {
		email := change + "@example.com"
		otherID := h.createUser(t, owner, email, "Owner")
		other := h.signIn(t, otherID, true)
		token, connection := connect(t, h, other, agent, "read")
		if _, err := h.access.Check(keyRequest(http.MethodGet, token), anonymousRead); err != nil {
			t.Fatalf("%s: before: %v", change, err)
		}
		switch change {
		case "demote":
			r := owner.do(http.MethodPut, "/api/v1/identity/owner/users/"+otherID.String(), map[string]any{"displayName": email, "email": email, "role": "User"})
			if r.status != http.StatusOK {
				t.Fatalf("demote: %d %s", r.status, r.body)
			}
		case "disable":
			if r := owner.do(http.MethodPost, "/api/v1/identity/owner/users/"+otherID.String()+"/disable", nil); r.status != http.StatusOK {
				t.Fatalf("disable: %d %s", r.status, r.body)
			}
		case "delete":
			if r := owner.do(http.MethodDelete, "/api/v1/identity/owner/users/"+otherID.String(), nil); r.status != http.StatusNoContent && r.status != http.StatusOK {
				t.Fatalf("delete: %d %s", r.status, r.body)
			}
		}
		if got := revokedFor(connection); got != "authorizer_removed" {
			t.Errorf("%s: the connection's reason %q", change, got)
		}
		refused(change, token, "revoked")
	}

	// A demotion that bypasses the managed role (a direct role removal, as a
	// group mapping would) leaves the row live but listed as inactive, and
	// the token refused.
	otherID := h.createUser(t, owner, "mapped@example.com", "Owner")
	token, _ := connect(t, h, h.signIn(t, otherID, true), agent, "read")
	h.exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, otherID, identity.RoleOwnerID)
	refused("an Owner role removed underneath", token, "token_invalid")
	if got := connectionsOf(t, owner, agent); len(got) != 1 || got[0].Status != "inactive" || got[0].InactiveReason == nil || *got[0].InactiveReason != "authorizer_removed" {
		t.Errorf("the inactive connection lists %+v", got)
	}

	// Archiving the integration ends its connections with its keys.
	token, connection := connect(t, h, owner, agent, "read")
	if r := owner.do(http.MethodPost, integrationsPath+"/"+agent.String()+"/archive", nil); r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	if got := revokedFor(connection); got != "integration_archived" {
		t.Errorf("archive: the connection's reason %q", got)
	}
	refused("archived", token, "revoked")
	if n := len(h.auditEvents(t, "connection.revoked")); n < 5 {
		t.Errorf("%d connection.revoked events, want one for each ending", n)
	}
}

// The worker's prune (C26, A21): it runs when OAUTH_ENABLED is the only
// switch on: codes an hour after they were consumed or expired, tokens a day
// past expiry, connections never activated after 10 minutes, connections 90
// days after they ended, and documents a week past expiry.
func TestOAuth_ThePrune(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t, withEnv("OAUTH_ENABLED", "1"), withEnv("IDENTITY_API_KEYS_ENABLED", "0"), withEnv("IDEMPOTENCY_ENABLED", "0"))
	agent := createIntegration(t, owner, "Agent").Integration.ID
	now := h.now()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	insert := func(created time.Time, activated, revoked *time.Time, expires time.Time) uuid.UUID {
		id := uuid.New()
		var reason *string
		if revoked != nil {
			r := "revoked"
			reason = &r
		}
		h.exec(t, `INSERT INTO identity.oauth_connections (id, integration_id, client_id, client_kind, client_name, client_host, redirect_uri, scopes,
		           created_at, activated_at, expires_at, revoked_at, revoke_reason)
		           VALUES ($1, $2, 'https://claude.ai/x', 'metadata', 'Claude', 'claude.ai', 'https://claude.ai/cb', '{read}', $3, $4, $5, $6, $7)`,
			id, agent, created, activated, expires, revoked, reason)
		return id
	}
	day := 24 * time.Hour
	live := insert(now.Add(-time.Hour), at(-time.Hour), nil, now.Add(day))
	fresh := insert(now.Add(-5*time.Minute), nil, nil, now.Add(day))
	stale := insert(now.Add(-11*time.Minute), nil, nil, now.Add(day))
	ended := insert(now.Add(-100*day), at(-100*day), at(-91*day), now.Add(day))
	expired := insert(now.Add(-300*day), at(-300*day), nil, now.Add(-91*day))
	recent := insert(now.Add(-30*day), at(-30*day), at(-10*day), now.Add(day))
	h.exec(t, `INSERT INTO identity.oauth_codes (code_hash, connection_id, code_challenge, created_at, expires_at, consumed_at) VALUES
	           ('\x01', $1, 'c', $2, $2, $2), ('\x02', $1, 'c', $2, $2, NULL), ('\x03', $1, 'c', $2, $3, $4)`,
		live, now.Add(-2*time.Hour), now.Add(time.Minute), now.Add(-time.Minute))
	h.exec(t, `INSERT INTO identity.oauth_tokens (token_hash, connection_id, kind, created_at, expires_at) VALUES
	           ('\x01', $1, 'access', $2, $3), ('\x02', $1, 'refresh', $2, $4)`,
		live, now.Add(-2*day), now.Add(-25*time.Hour), now.Add(-23*time.Hour))
	h.exec(t, `INSERT INTO identity.oauth_client_documents (client_id, document, fetched_at, expires_at) VALUES
	           ('https://a.example.com/x', '{}', $1, $2), ('https://b.example.com/x', '{}', $1, $3)`,
		now.Add(-10*day), now.Add(-8*day), now.Add(-6*day))

	w := apiKeysWorker(h)
	if w == nil {
		t.Fatal("the worker does not run with only OAUTH_ENABLED on")
	}
	runCycle(t, w)
	for id, want := range map[uuid.UUID]bool{live: true, fresh: true, stale: false, ended: false, expired: false, recent: true} {
		if got := h.count(t, `SELECT count(*) FROM identity.oauth_connections WHERE id = $1`, id) == 1; got != want {
			t.Errorf("connection %s kept %v, want %v", id, got, want)
		}
	}
	if got := h.count(t, `SELECT count(*) FROM identity.oauth_codes`); got != 1 {
		t.Errorf("%d codes kept, want the one consumed a minute ago", got)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.oauth_tokens`); got != 1 {
		t.Errorf("%d tokens kept, want the one 23 hours past expiry", got)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.oauth_client_documents`); got != 1 {
		t.Errorf("%d documents kept, want the one 6 days past expiry", got)
	}

	off := newHarness(t, withEnv("IDENTITY_API_KEYS_ENABLED", "0"), withEnv("IDEMPOTENCY_ENABLED", "0"))
	if apiKeysWorker(off) != nil {
		t.Error("the worker runs with all three switches off")
	}
}
