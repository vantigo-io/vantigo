package identity_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// OAuth connections, delivery C (OAuth connections design C8, C11, C18, A3,
// A23): the clients an Owner registers by hand, the client hosts that may
// connect, the revalidation of a client's document at a refresh, and the
// revocation endpoint.

const (
	oauthClientsPath  = "/api/v1/identity/oauth-clients"
	oauthSettingsPath = "/api/v1/identity/oauth/settings"
	oauthRevokePath   = "/api/oauth/revoke"
	chatGPTID         = "https://chatgpt.com/oauth/client.json"
	chatGPTReturn     = "https://chatgpt.com/connector_platform_oauth_redirect"
)

var registeredIDPattern = regexp.MustCompile(`^vtc_[0-9A-Za-z]{24}$`)

type registeredClient struct {
	ID              uuid.UUID  `json:"id"`
	ClientID        string     `json:"clientId"`
	Name            string     `json:"name"`
	RedirectURIs    []string   `json:"redirectUris"`
	CreatedBy       *uuid.UUID `json:"createdBy"`
	CreatedByName   *string    `json:"createdByName"`
	Version         string     `json:"version"`
	LiveConnections int        `json:"liveConnections"`
}

func registerClient(t *testing.T, c *client, name string, uris ...string) registeredClient {
	t.Helper()
	r := c.do(http.MethodPost, oauthClientsPath, map[string]any{"name": name, "redirectUris": uris})
	if r.status != http.StatusCreated {
		t.Fatalf("register: %d %s", r.status, r.body)
	}
	var out registeredClient
	r.json(&out)
	return out
}

func registeredClients(t *testing.T, c *client) []registeredClient {
	t.Helper()
	r := c.do(http.MethodGet, oauthClientsPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	var out struct {
		Clients []registeredClient `json:"clients"`
	}
	r.json(&out)
	return out.Clients
}

// tokenPost posts form to the token endpoint.
func tokenPost(h *harness, t *testing.T, form url.Values) *resp {
	t.Helper()
	return h.client(t).do(http.MethodPost, oauthTokenPath, nil, rawBody("application/x-www-form-urlencoded", []byte(form.Encode())),
		skipContract("the token endpoint is RFC 6749's, in no contract"))
}

// refreshOnce renews a refresh token, and answers the response and the new
// refresh token.
func refreshOnce(h *harness, t *testing.T, refresh, clientID string) (*resp, string) {
	t.Helper()
	r := tokenPost(h, t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	var out struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.Unmarshal(r.body, &out)
	return r, out.RefreshToken
}

// revokeAt posts form to the revocation endpoint.
func revokeAt(h *harness, t *testing.T, form url.Values, opts ...reqOpt) *resp {
	t.Helper()
	return h.client(t).do(http.MethodPost, oauthRevokePath, nil, append([]reqOpt{
		rawBody("application/x-www-form-urlencoded", []byte(form.Encode())), skipContract("the revocation endpoint is RFC 7009's, in no contract"),
	}, opts...)...)
}

func revokeReasonOf(t *testing.T, h *harness, connection uuid.UUID) string {
	t.Helper()
	var reason string
	if err := h.pool.QueryRow(t.Context(), `SELECT coalesce(revoke_reason, '') FROM identity.oauth_connections WHERE id = $1`, connection).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	return reason
}

// connectAs runs the flow for clientID and redirect to the tokens and the
// connection.
func connectAs(t *testing.T, h *harness, owner *client, integration uuid.UUID, clientID, redirect string) (access, refresh string, connection uuid.UUID) {
	t.Helper()
	h.advance(time.Second)
	code, _ := approve(t, owner, authorizationQuery(clientID, redirect), integration, "read")
	access, refresh = exchange(t, h, code, clientID, redirect)
	if err := h.pool.QueryRow(t.Context(), `SELECT id FROM identity.oauth_connections WHERE client_id = $1 ORDER BY created_at DESC LIMIT 1`, clientID).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	return access, refresh, connection
}

// Registering a client (C8): Vantigo mints its vtc_ client_id; the name and
// the redirect URIs are held to the rules a document's are, every refusal
// naming its field and never echoing what was sent; an edit is under
// If-Match; each change is audited.
func TestOAuthClients_RegisterListAndEdit(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, _ := oauthHarness(t)

	c := registerClient(t, owner, "  Cursor\u202e on\tmy laptop ", "http://127.0.0.1/callback", "https://vscode.dev/redirect", "http://127.0.0.1/callback")
	if !registeredIDPattern.MatchString(c.ClientID) || c.Name != "Cursor on my laptop" || strings.Join(c.RedirectURIs, " ") != "http://127.0.0.1/callback https://vscode.dev/redirect" ||
		c.CreatedBy == nil || *c.CreatedBy != ownerID || c.CreatedByName == nil || c.LiveConnections != 0 {
		t.Errorf("registered %+v", c)
	}
	if events := h.auditEvents(t, "oauth_client.created"); len(events) != 1 || !strings.Contains(events[0].After, c.ClientID) || events[0].Actor == nil || *events[0].Actor != ownerID {
		t.Errorf("oauth_client.created %+v", events)
	}
	h.advance(time.Second)
	if other := registerClient(t, owner, "VS Code", "http://127.0.0.1/"); other.ClientID == c.ClientID {
		t.Error("two clients share a client_id")
	}
	if got := registeredClients(t, owner); len(got) != 2 || got[0].ID != c.ID {
		t.Errorf("list %+v", got)
	}

	const secret = "javascript-attacker-value"
	for name, body := range map[string]map[string]any{
		"no name":            {"name": " \u200b ", "redirectUris": []string{"https://app.example.com/cb"}},
		"a long name":        {"name": strings.Repeat("n", 81), "redirectUris": []string{"https://app.example.com/cb"}},
		"no redirect":        {"name": "App", "redirectUris": []string{}},
		"a custom scheme":    {"name": "App", "redirectUris": []string{secret + "://cb"}},
		"http off loopback":  {"name": "App", "redirectUris": []string{"http://" + secret + ".example.com/cb"}},
		"a fragment":         {"name": "App", "redirectUris": []string{"https://app.example.com/cb#" + secret}},
		"too many redirects": {"name": "App", "redirectUris": manyRedirects(21)},
	} {
		r := owner.do(http.MethodPost, oauthClientsPath, body)
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" || strings.Contains(string(r.body), secret) {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
		field := "redirectUris"
		if strings.Contains(name, "name") {
			field = "name"
		}
		if !strings.Contains(string(r.body), `"`+field+`":`) {
			t.Errorf("%s names no %s: %s", name, field, r.body)
		}
	}

	path := oauthClientsPath + "/" + c.ID.String()
	edit := map[string]any{"name": "Cursor", "redirectUris": []string{"http://localhost/cb"}}
	if r := owner.do(http.MethodPatch, path, edit, header("If-Match", "not-a-version")); r.status != http.StatusBadRequest {
		t.Errorf("a bad If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPatch, path, edit, header("If-Match", uuid.NewString())); r.status != http.StatusPreconditionFailed || r.code() != "version_mismatch" {
		t.Errorf("a stale If-Match: %d %s", r.status, r.body)
	}
	r := owner.do(http.MethodPatch, path, edit, header("If-Match", `"`+c.Version+`"`))
	var edited registeredClient
	r.json(&edited)
	if r.status != http.StatusOK || edited.Name != "Cursor" || edited.Version == c.Version || edited.ClientID != c.ClientID || strings.Join(edited.RedirectURIs, " ") != "http://localhost/cb" {
		t.Errorf("edit: %d %s", r.status, r.body)
	}
	if events := h.auditEvents(t, "oauth_client.updated"); len(events) != 1 || !strings.Contains(events[0].Before, "vscode.dev") || strings.Contains(events[0].After, "vscode.dev") {
		t.Errorf("oauth_client.updated %+v", events)
	}
	if r := owner.do(http.MethodPatch, oauthClientsPath+"/"+uuid.NewString(), edit, header("If-Match", c.Version)); r.status != http.StatusNotFound {
		t.Errorf("an unknown client: %d %s", r.status, r.body)
	}

	// Only an Owner, and an Owner's session meeting the MFA rule.
	user := h.signIn(t, h.createUser(t, owner, "user@example.com", "User"), true)
	for _, r := range []*resp{
		user.do(http.MethodGet, oauthClientsPath, nil),
		user.do(http.MethodPost, oauthClientsPath, map[string]any{"name": "App", "redirectUris": []string{"https://app.example.com/cb"}}),
		user.do(http.MethodGet, oauthSettingsPath, nil),
		user.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": []string{}}),
	} {
		if r.status != http.StatusForbidden {
			t.Errorf("a User: %d %s", r.status, r.body)
		}
	}
}

func manyRedirects(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("https://app.example.com/cb/%d", i)
	}
	return out
}

// At most 50 registered clients, held under concurrency: two registrations
// racing for the last place register one.
func TestOAuthClients_TheLimit(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	for i := range 49 {
		h.exec(t, `INSERT INTO identity.oauth_clients (id, client_id, name, redirect_uris, created_at, updated_at, version)
			VALUES ($1, $2, 'Seeded', ARRAY['https://app.example.com/cb'], now(), now(), $1)`, uuid.New(), fmt.Sprintf("vtc_%024d", i))
	}
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = owner.do(http.MethodPost, oauthClientsPath, map[string]any{"name": "App", "redirectUris": []string{"https://app.example.com/cb"}}).status
		}()
	}
	wg.Wait()
	oneEach := (statuses[0] == http.StatusCreated && statuses[1] == http.StatusConflict) || (statuses[1] == http.StatusCreated && statuses[0] == http.StatusConflict)
	if !oneEach {
		t.Errorf("two registrations for the last place: %v", statuses)
	}
	if r := owner.do(http.MethodPost, oauthClientsPath, map[string]any{"name": "App", "redirectUris": []string{"https://app.example.com/cb"}}); r.status != http.StatusConflict || r.code() != "oauth_client_limit" {
		t.Errorf("the 51st: %d %s", r.status, r.body)
	}
}

// A registered client signs in (C8, C13, A6): the consent page says who
// registered it and where its code goes, any loopback port matches a
// registered loopback URI without one, an error is redirected without a
// click, the token endpoint takes none, and Connected apps shows its kind.
// Archiving it revokes its connections (client_archived) and refuses its
// next sign-in.
func TestOAuthClients_SignInAndArchive(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	c := registerClient(t, owner, "Cursor", "http://127.0.0.1/callback")
	redirect := "http://127.0.0.1:61234/callback"

	r := owner.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": authorizationQuery(c.ClientID, redirect)})
	var p struct {
		Outcome  string `json:"outcome"`
		FirstUse *bool  `json:"firstUse"`
		Warning  *bool  `json:"nameWarning"`
		Client   struct {
			Kind         string  `json:"kind"`
			Name         string  `json:"name"`
			ClientIDHost string  `json:"clientIdHost"`
			RedirectHost string  `json:"redirectHost"`
			Loopback     bool    `json:"loopback"`
			RegisteredBy *string `json:"registeredBy"`
		} `json:"client"`
	}
	r.json(&p)
	if p.Outcome != "consent" || p.Client.Kind != "registered" || p.Client.Name != "Cursor" || p.Client.RedirectHost != "127.0.0.1" ||
		p.Client.ClientIDHost != "127.0.0.1" || !p.Client.Loopback || p.Client.RegisteredBy == nil || *p.Client.RegisteredBy == "" ||
		p.FirstUse == nil || *p.FirstUse || p.Warning == nil || *p.Warning {
		t.Errorf("preview %s", r.body)
	}
	// A redirect URI it did not register is refused, never redirected to.
	if p := previewOf(t, owner, authorizationQuery(c.ClientID, "http://localhost:61234/callback")); p.Outcome != "client_refused" || p.RedirectTo != nil {
		t.Errorf("another loopback host: %+v", p)
	}
	// An error is redirected at once: the redirect URIs are an Owner's.
	if p := previewOf(t, owner, authorizationQuery(c.ClientID, redirect, "code_challenge", "")); p.Outcome != "redirect" || p.Automatic == nil || !*p.Automatic {
		t.Errorf("no PKCE: %+v", p)
	}

	access, refresh, connection := connectAs(t, h, owner, agent, c.ClientID, redirect)
	if got := connectionsOf(t, owner, agent); len(got) != 1 || got[0].ClientHost != "127.0.0.1" || got[0].ClientName != "Cursor" {
		t.Errorf("connections %+v", got)
	}
	if kind := listedKind(t, owner, agent); kind != "registered" {
		t.Errorf("the connection's kind %q", kind)
	}
	if r, next := refreshOnce(h, t, refresh, c.ClientID); r.status != http.StatusOK || next == "" {
		t.Errorf("refresh: %d %s", r.status, r.body)
	} else {
		refresh = next
	}
	if got := registeredClients(t, owner); len(got) != 1 || got[0].LiveConnections != 1 {
		t.Errorf("list %+v", got)
	}

	path := oauthClientsPath + "/" + c.ID.String() + "/archive"
	if r := owner.do(http.MethodPost, path, nil, header("If-Match", uuid.NewString())); r.status != http.StatusPreconditionFailed {
		t.Errorf("a stale archive: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, path, nil, header("If-Match", c.Version)); r.status != http.StatusNoContent {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, connection); reason != "client_archived" {
		t.Errorf("the connection's reason %q", reason)
	}
	if events := h.auditEvents(t, "oauth_client.archived"); len(events) != 1 {
		t.Errorf("oauth_client.archived %+v", events)
	}
	if events := h.auditEvents(t, "connection.revoked"); len(events) != 1 || !strings.Contains(events[0].After, `"revokeReason":"client_archived"`) {
		t.Errorf("connection.revoked %+v", events)
	}
	var refusal *contracts.APIKeyRefusal
	if _, err := h.access.Check(keyRequest(http.MethodGet, access), anonymousRead); !errors.As(err, &refusal) || refusal.Reason != "revoked" {
		t.Errorf("an archived client's token: %v", err)
	}
	if r, _ := refreshOnce(h, t, refresh, c.ClientID); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("an archived client's refresh: %d %s", r.status, r.body)
	}
	if p := previewOf(t, owner, authorizationQuery(c.ClientID, redirect)); p.Outcome != "client_refused" {
		t.Errorf("an archived client's sign-in: %+v", p)
	}
	if r := owner.do(http.MethodPost, path, nil, header("If-Match", c.Version)); r.status != http.StatusNotFound {
		t.Errorf("archive again: %d %s", r.status, r.body)
	}
	if got := registeredClients(t, owner); len(got) != 0 {
		t.Errorf("an archived client lists %+v", got)
	}
}

func listedKind(t *testing.T, c *client, integration uuid.UUID) string {
	t.Helper()
	r := c.do(http.MethodGet, integrationsPath+"/"+integration.String()+"/connections", nil)
	var out struct {
		Connections []struct {
			ClientKind string `json:"clientKind"`
		} `json:"connections"`
	}
	r.json(&out)
	if len(out.Connections) == 0 {
		return ""
	}
	return out.Connections[0].ClientKind
}

// A registered client's client_id is no token, and no token is a client_id:
// each presented where the other belongs is refused as unknown.
func TestOAuthClients_KindConfusion(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	c := registerClient(t, owner, "Cursor", "http://127.0.0.1/callback")
	access, refresh, _ := connectAs(t, h, owner, agent, c.ClientID, "http://127.0.0.1/callback")

	// vtc_ as a bearer is no caller.
	if r := h.client(t).do(http.MethodGet, integrationsPath, nil, header("Authorization", "Bearer "+c.ClientID)); r.status != http.StatusUnauthorized {
		t.Errorf("a client_id as a bearer: %d %s", r.status, r.body)
	}
	// vtc_ as a refresh token or a code.
	if r := tokenPost(h, t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.ClientID}, "client_id": {c.ClientID}}); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("a client_id as a refresh token: %d %s", r.status, r.body)
	}
	if r := tokenPost(h, t, url.Values{"grant_type": {"authorization_code"}, "code": {c.ClientID}, "client_id": {c.ClientID},
		"redirect_uri": {"http://127.0.0.1/callback"}, "code_verifier": {oauthVerifier}}); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("a client_id as a code: %d %s", r.status, r.body)
	}
	// A token as a client_id, and a malformed vtc_ client_id.
	for _, id := range []string{access, refresh, "vtc_short", strings.Replace(c.ClientID, "vtc_", "vtc-", 1)} {
		if p := previewOf(t, owner, authorizationQuery(id, "http://127.0.0.1/callback")); p.Outcome != "client_refused" || p.RedirectTo != nil {
			t.Errorf("%.8s… as a client_id: %+v", id, p)
		}
	}
	// The refresh token is refused where an access token belongs (A10).
	if _, err := h.access.Check(keyRequest(http.MethodGet, "vta_"+refresh[4:]), anonymousRead); !errors.Is(err, contracts.ErrInvalidToken) {
		t.Errorf("a refresh token as an access token: %v", err)
	}
	// The token endpoint refuses a client that authenticates, registered or not.
	if r := h.client(t).do(http.MethodPost, oauthTokenPath, nil, rawBody("application/x-www-form-urlencoded",
		[]byte(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {c.ClientID}, "client_secret": {"x"}}.Encode())),
		skipContract("RFC 6749")); r.status != http.StatusUnauthorized || !strings.Contains(string(r.body), `"invalid_client"`) {
		t.Errorf("a secret: %d %s", r.status, r.body)
	}
}

// The client hosts that may connect (C11): empty admits any; otherwise a
// client_id's host must equal an entry, held at the preview (before any
// fetch), the approval and every refresh, where a refused host is
// invalid_grant with nothing ended and the connection listed inactive until
// the host is listed again. Registered clients are always admitted. A
// change is audited; the list already held writes nothing.
func TestOAuthSettings_TheClientHosts(t *testing.T) {
	t.Parallel()
	h, owner, ownerID, host := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	_, chatRefresh, chatConnection := connectAs(t, h, owner, agent, chatGPTID, chatGPTReturn)
	registered := registerClient(t, owner, "Cursor", "http://127.0.0.1/callback")

	if r := owner.do(http.MethodGet, oauthSettingsPath, nil); r.status != http.StatusOK || string(r.body) != `{"clientHosts":[]}`+"\n" {
		t.Errorf("the default: %d %q", r.status, r.body)
	}
	const secret = "attacker-chosen"
	for _, bad := range [][]string{{"*.claude.ai"}, {"203.0.113.9"}, {"localhost"}, {"xn--bcher-kva.example"}, {secret + " .example"}, {"claude.ai:443"}, manyHosts(51)} {
		r := owner.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": bad})
		if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"clientHosts":`) || strings.Contains(string(r.body), secret) {
			t.Errorf("%v: %d %s", bad[0], r.status, r.body)
		}
	}
	r := owner.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": []string{" Claude.AI ", "claude.ai"}})
	if r.status != http.StatusOK || string(r.body) != `{"clientHosts":["claude.ai"]}`+"\n" {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	_ = owner.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": []string{"claude.ai"}})
	if events := h.auditEvents(t, "oauth.client_hosts_changed"); len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID ||
		events[0].Before != `{"clientHosts":[]}` || events[0].After != `{"clientHosts":["claude.ai"]}` {
		t.Errorf("oauth.client_hosts_changed %+v", events)
	}

	// A host off the list is refused before it is fetched.
	fetched := host.Fetches(chatGPTID)
	h.exec(t, `DELETE FROM identity.oauth_client_documents`)
	r = owner.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": authorizationQuery(chatGPTID, chatGPTReturn)})
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"outcome":"client_refused"`) || strings.Contains(string(r.body), "chatgpt") {
		t.Errorf("a host off the list: %d %s", r.status, r.body)
	}
	if host.Fetches(chatGPTID) != fetched {
		t.Error("a host off the list was fetched")
	}
	if r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": authorizationQuery(chatGPTID, chatGPTReturn), "integrationId": agent, "access": "read", "decision": "approve"}); r.status != http.StatusBadRequest || r.code() != "client_refused" {
		t.Errorf("approving a host off the list: %d %s", r.status, r.body)
	}
	if p := previewOf(t, owner, authorizationQuery(hostedClaudeID, hostedClaudeReturn)); p.Outcome != "consent" {
		t.Errorf("a listed host: %+v", p)
	}
	if p := previewOf(t, owner, authorizationQuery(registered.ClientID, "http://127.0.0.1/callback")); p.Outcome != "consent" {
		t.Errorf("a registered client with a list: %+v", p)
	}

	// The connection that was made before is not ended, cannot renew, and lists as inactive.
	if r, _ := refreshOnce(h, t, chatRefresh, chatGPTID); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("a refresh off the list: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, chatConnection); reason != "" {
		t.Errorf("a connection off the list was ended: %q", reason)
	}
	listed := connectionsOf(t, owner, agent)
	if len(listed) != 1 || listed[0].Status != "inactive" || listed[0].InactiveReason == nil || *listed[0].InactiveReason != "client_not_allowed" {
		t.Errorf("listed %+v", listed)
	}
	// Listed again, the same refresh token renews: it was not spent.
	if r := owner.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": []string{"chatgpt.com", "claude.ai"}}); r.status != http.StatusOK {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	if r, _ := refreshOnce(h, t, chatRefresh, chatGPTID); r.status != http.StatusOK {
		t.Errorf("a refresh back on the list: %d %s", r.status, r.body)
	}
	if listed := connectionsOf(t, owner, agent); len(listed) != 1 || listed[0].Status != "active" {
		t.Errorf("listed %+v", listed)
	}
}

func manyHosts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("h%d.example.com", i)
	}
	return out
}

// Switched off (C24), every new session operation is 404.
func TestOAuthClients_SwitchedOff(t *testing.T) {
	t.Parallel()
	_, owner, _ := rbacHarness(t, withEnv("OAUTH_ENABLED", "0"))
	id := uuid.NewString()
	for name, r := range map[string]*resp{
		"list":     owner.do(http.MethodGet, oauthClientsPath, nil),
		"register": owner.do(http.MethodPost, oauthClientsPath, map[string]any{"name": "App", "redirectUris": []string{"https://app.example.com/cb"}}),
		"edit":     owner.do(http.MethodPatch, oauthClientsPath+"/"+id, map[string]any{"name": "App", "redirectUris": []string{"https://app.example.com/cb"}}, header("If-Match", id)),
		"archive":  owner.do(http.MethodPost, oauthClientsPath+"/"+id+"/archive", nil, header("If-Match", id)),
		"settings": owner.do(http.MethodGet, oauthSettingsPath, nil),
		"save":     owner.do(http.MethodPut, oauthSettingsPath, map[string]any{"clientHosts": []string{}}),
		"revoke":   owner.do(http.MethodPost, oauthRevokePath, nil, rawBody("application/x-www-form-urlencoded", []byte("token=x&client_id=y")), skipContract("RFC 7009")),
	} {
		if r.status != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
}

// Revalidation at a refresh (A3): while the cached document is fresh a
// refresh fetches nothing; once it has expired the refresh is answered and
// the document fetched again in the background. A document that no longer
// lists the redirect URI, or breaks a rule, revokes the connection
// (client_document_changed); one that cannot be read changes nothing; one
// that still holds is cached again.
func TestOAuth_RevalidationAtARefresh(t *testing.T) {
	t.Parallel()
	h, owner, _, host := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	good := mustRead(t, "claude.json")

	_, refresh, connection := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	fetched := host.Fetches(hostedClaudeID)
	var next string
	refreshed := func(what string) {
		t.Helper()
		r, n := refreshOnce(h, t, refresh, hostedClaudeID)
		if r.status != http.StatusOK {
			t.Fatalf("%s: refresh %d %s", what, r.status, r.body)
		}
		h.access.WaitOAuthRevalidations()
		refresh, next = n, n
	}
	refreshed("fresh")
	if host.Fetches(hostedClaudeID) != fetched {
		t.Error("a fresh document was fetched again")
	}

	// Expired and unreadable: transient, nothing changes.
	h.advance(11 * time.Minute)
	host.Unserve(hostedClaudeID)
	refreshed("unreadable")
	if host.Fetches(hostedClaudeID) != fetched+1 || revokeReasonOf(t, h, connection) != "" {
		t.Errorf("unreadable: fetches %d, reason %q", host.Fetches(hostedClaudeID), revokeReasonOf(t, h, connection))
	}

	// Expired and still good: cached again, so the next refresh fetches nothing.
	h.advance(11 * time.Second)
	host.Serve(hostedClaudeID, good, "max-age=600")
	refreshed("good")
	if host.Fetches(hostedClaudeID) != fetched+2 || revokeReasonOf(t, h, connection) != "" {
		t.Errorf("good: fetches %d", host.Fetches(hostedClaudeID))
	}
	refreshed("cached again")
	if host.Fetches(hostedClaudeID) != fetched+2 {
		t.Errorf("cached again: fetches %d", host.Fetches(hostedClaudeID))
	}

	// Expired, and the redirect URI is gone: the refresh is answered, then
	// the connection revoked.
	h.advance(11 * time.Minute)
	host.Serve(hostedClaudeID, []byte(strings.Replace(string(good), "/api/mcp/auth_callback", "/elsewhere", 1)), "")
	refreshed("the redirect gone")
	if reason := revokeReasonOf(t, h, connection); reason != "client_document_changed" {
		t.Errorf("the redirect gone: reason %q", reason)
	}
	if events := h.auditEvents(t, "connection.revoked"); len(events) != 1 || !strings.Contains(events[0].After, `"revokeReason":"client_document_changed"`) || events[0].Actor != nil {
		t.Errorf("connection.revoked %+v", events)
	}
	if r, _ := refreshOnce(h, t, next, hostedClaudeID); r.status != http.StatusBadRequest {
		t.Errorf("after the revocation: %d %s", r.status, r.body)
	}

	// A document that breaks a rule (it now asks for a secret) revokes too.
	host.Serve(hostedClaudeID, good, "max-age=600")
	h.advance(time.Minute)
	_, refresh, connection = connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	h.advance(11 * time.Minute)
	host.Serve(hostedClaudeID, []byte(strings.Replace(string(good), `"token_endpoint_auth_method": "none"`, `"token_endpoint_auth_method": "client_secret_basic"`, 1)), "")
	refreshed("a rule broken")
	if reason := revokeReasonOf(t, h, connection); reason != "client_document_changed" {
		t.Errorf("a rule broken: reason %q", reason)
	}
}

// The revocation endpoint (C18, A23, RFC 7009): either token, with its
// connection's client_id, ends the connection (revoked_by_client); a token
// unknown, malformed, of another kind or of another client's connection is
// 200 with nothing revoked; a malformed request is RFC 6749's error.
func TestOAuth_TheRevocationEndpoint(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	_, key := createKey(t, owner, agent, nil)
	access, refresh, connection := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)

	if r := h.client(t).do(http.MethodGet, oauthRevokePath, nil, skipContract("RFC 7009")); r.status != http.StatusMethodNotAllowed || r.header("Allow") != http.MethodPost {
		t.Errorf("GET: %d %s", r.status, r.body)
	}
	form := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}
	for name, c := range map[string]struct {
		r      *resp
		status int
		code   string
	}{
		"JSON":            {h.client(t).do(http.MethodPost, oauthRevokePath, map[string]string{"token": access, "client_id": hostedClaudeID}, skipContract("RFC 7009")), 400, "invalid_request"},
		"Basic":           {revokeAt(h, t, form("token", access, "client_id", hostedClaudeID), header("Authorization", "Basic eDp5")), 401, "invalid_client"},
		"a secret":        {revokeAt(h, t, form("token", access, "client_id", hostedClaudeID, "client_secret", "s")), 401, "invalid_client"},
		"no token":        {revokeAt(h, t, form("client_id", hostedClaudeID)), 400, "invalid_request"},
		"no client_id":    {revokeAt(h, t, form("token", access)), 400, "invalid_request"},
		"a repeated name": {revokeAt(h, t, form("token", access, "token", access, "client_id", hostedClaudeID)), 400, "invalid_request"},
	} {
		if c.r.status != c.status || !strings.Contains(string(c.r.body), `"error":"`+c.code+`"`) || c.r.header("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s", name, c.r.status, c.r.body)
		}
	}
	for name, f := range map[string]url.Values{
		"garbage":           form("token", "not-a-token", "client_id", hostedClaudeID),
		"a forged checksum": form("token", access[:len(access)-1]+"x", "client_id", hostedClaudeID),
		"an API key":        form("token", key, "client_id", hostedClaudeID),
		"a client_id":       form("token", "vtc_000000000000000000000000", "client_id", hostedClaudeID),
		"another client":    form("token", access, "client_id", chatGPTID),
		"with a hint":       form("token", refresh, "client_id", chatGPTID, "token_type_hint", "refresh_token"),
	} {
		r := revokeAt(h, t, f)
		if r.status != http.StatusOK || len(r.body) != 0 || r.header("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %q", name, r.status, r.body)
		}
	}
	if reason := revokeReasonOf(t, h, connection); reason != "" {
		t.Fatalf("revoked by a wrong request: %q", reason)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, key), anonymousRead); err != nil {
		t.Errorf("the API key after its revocation was asked: %v", err)
	}

	if r := revokeAt(h, t, form("token", access, "client_id", hostedClaudeID)); r.status != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, connection); reason != "revoked_by_client" {
		t.Errorf("the reason %q", reason)
	}
	if events := h.auditEvents(t, "connection.revoked"); len(events) != 1 || !strings.Contains(events[0].Details, "system:oauth") {
		t.Errorf("connection.revoked %+v", events)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, access), anonymousRead); !errors.Is(err, contracts.ErrInvalidToken) {
		t.Errorf("the access token: %v", err)
	}
	if r, _ := refreshOnce(h, t, refresh, hostedClaudeID); r.status != http.StatusBadRequest {
		t.Errorf("the refresh token: %d %s", r.status, r.body)
	}
	// Again: 200, nothing more.
	if r := revokeAt(h, t, form("token", access, "client_id", hostedClaudeID)); r.status != http.StatusOK || len(h.auditEvents(t, "connection.revoked")) != 1 {
		t.Errorf("again: %d", r.status)
	}

	// The refresh token ends its connection too.
	_, refresh, connection = connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	if r := revokeAt(h, t, form("token", refresh, "client_id", hostedClaudeID, "token_type_hint", "access_token")); r.status != http.StatusOK || revokeReasonOf(t, h, connection) != "revoked_by_client" {
		t.Errorf("by the refresh token: %d %q", r.status, revokeReasonOf(t, h, connection))
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "oauth", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
