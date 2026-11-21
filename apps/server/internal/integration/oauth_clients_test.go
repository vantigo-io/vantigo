package integration_test

import (
	"net/http"
	"net/url"
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

// OAuth connections, delivery C, end to end (OAuth connections design C8,
// C11, C18, A3, A18, A23): a client registered by hand connects and works
// as a document's client does, the revocation endpoint ends a connection
// from the client's side, the request log filters by connection, and the
// revalidation of a document runs on a pool of one.

var oauthRevokeSkip = modtest.SkipContract("the revocation endpoint is RFC 7009's, in no contract")

type registered struct {
	ID       uuid.UUID `json:"id"`
	ClientID string    `json:"clientId"`
	Version  string    `json:"version"`
}

func registerOAuthClient(t *testing.T, owner *modtest.Client, name string, uris ...string) registered {
	t.Helper()
	r := owner.Do(http.MethodPost, "/api/v1/identity/oauth-clients", map[string]any{"name": name, "redirectUris": uris}, oauthSkip)
	if r.Status != http.StatusCreated {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	var out registered
	r.JSON(&out)
	return out
}

func revokeRequest(c *modtest.Client, form url.Values, opts ...modtest.RequestOption) *modtest.Response {
	return c.Do(http.MethodPost, "/api/oauth/revoke", nil, append([]modtest.RequestOption{
		oauthRevokeSkip, modtest.RawBody("application/x-www-form-urlencoded", []byte(form.Encode())),
	}, opts...)...)
}

func waitRevalidations(h *modtest.Harness) {
	h.Deps().Access.(*identity.Access).WaitOAuthRevalidations()
}

// The whole flow for a registered client (C8): an Owner registers it, the
// client signs in from any loopback port, calls /api/mcp and a REST
// operation, renews its token, and ends its connection itself through the
// revocation endpoint, cross-site and ahead of the cross-origin guard, after
// which its token is the 401 with the Bearer challenge.
func TestOAuth_ARegisteredClientsWholeFlow(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	person := h.SignIn(t, "customers:view", "customers:create")
	if r := person.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Fjordkraft AS"}); r.Status != http.StatusCreated {
		t.Fatalf("create a customer: %d %s", r.Status, r.Body)
	}
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	c := registerOAuthClient(t, owner, "VS Code", "http://127.0.0.1/", "https://vscode.dev/redirect")
	redirect := "http://127.0.0.1:33418/"

	tok := connectClient(t, h, owner, integration, "read", c.ClientID, redirect)
	if tok.Scope != "read" {
		t.Errorf("scope %q", tok.Scope)
	}
	api := h.APIKeyClient(t, tok.AccessToken)
	if _, text, isError := mcpTool(t, api, "read", map[string]any{"operation": "getCustomers", "query": map[string]any{"search": "Fjordkraft"}}); isError || !strings.Contains(text, "Fjordkraft AS") {
		t.Errorf("read: %v %s", isError, text)
	}
	if r := api.Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a connection's REST read")); r.Status != http.StatusOK {
		t.Errorf("REST: %d %s", r.Status, r.Body)
	}
	r, next := tokenRequest(h.Client(t), refreshForm(tok.RefreshToken, c.ClientID))
	if r.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	waitRevalidations(h)
	if n := h.Count(t, `SELECT count(*) FROM identity.oauth_client_documents`); n != 0 {
		t.Errorf("a registered client's refresh fetched %d documents", n)
	}

	connection := connectionOf(t, h, integration)
	rr := revokeRequest(h.Client(t), url.Values{"token": {next.AccessToken}, "client_id": {c.ClientID}},
		modtest.Header("Origin", "https://vscode.dev"), modtest.Header("Sec-Fetch-Site", "cross-site"))
	if rr.Status != http.StatusOK || len(rr.Body) != 0 {
		t.Fatalf("revoke: %d %s", rr.Status, rr.Body)
	}
	if reason := revokeReason(t, h, connection); reason != "revoked_by_client" {
		t.Errorf("the reason %q", reason)
	}
	after := mcpCall(h.APIKeyClient(t, next.AccessToken), "tools/list", nil)
	if after.Status != http.StatusUnauthorized || !strings.Contains(after.Header("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("after the revocation: %d %q", after.Status, after.Header("WWW-Authenticate"))
	}
}

// The request log's filter by connected app (A18): each connection's
// requests apart from the key's and from each other's, through the index
// the migration adds.
func TestOAuth_TheRequestLogFilter(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})

	first := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	firstID := connectionOf(t, h, integration)
	h.Advance(time.Second)
	second := connectClient(t, h, owner, integration, "read", chatGPTID, chatGPTReturn)
	secondID := connectionOf(t, h, integration)
	for c, n := range map[string]int{key: 1, first.AccessToken: 2, second.AccessToken: 3} {
		for range n {
			if r := h.APIKeyClient(t, c).Do(http.MethodGet, "/api/v1/customers", nil, modtest.SkipContract("a logged read")); r.Status != http.StatusOK {
				t.Fatalf("read: %d %s", r.Status, r.Body)
			}
		}
	}
	h.FlushAPIRequests()
	h.Exec(t, `UPDATE identity.api_requests SET at = $2 WHERE integration_id = $1`, integration, h.Now().Add(-time.Minute))

	base := "/api/v1/identity/integrations/" + integration.String() + "/requests"
	for name, c := range map[string]struct {
		query string
		want  int
	}{
		"all":                {"", 6},
		"the first":          {"?connection=" + firstID.String(), 2},
		"the second":         {"?connection=" + secondID.String(), 3},
		"an unknown":         {"?connection=" + uuid.NewString(), 0},
		"a key and a client": {"?connection=" + firstID.String() + "&key=" + uuid.NewString(), 0},
	} {
		r := owner.Do(http.MethodGet, base+c.query, nil, oauthSkip)
		var log struct {
			Latest []struct {
				ConnectionID *uuid.UUID `json:"connectionId"`
				ClientName   *string    `json:"clientName"`
			} `json:"latest"`
			Operations []struct {
				Requests int `json:"requests"`
			} `json:"operations"`
		}
		r.JSON(&log)
		if r.Status != http.StatusOK || len(log.Latest) != c.want {
			t.Errorf("%s: %d, %d rows, want %d", name, r.Status, len(log.Latest), c.want)
		}
		if c.query != "" && c.want > 0 && (len(log.Operations) != 1 || log.Operations[0].Requests != c.want) {
			t.Errorf("%s: operations %+v", name, log.Operations)
		}
	}
	if r := owner.Do(http.MethodGet, base+"?connection=not-a-uuid", nil, modtest.SkipContract("a malformed filter")); r.Status != http.StatusBadRequest {
		t.Errorf("a malformed connection: %d %s", r.Status, r.Body)
	}
	if !planUsesIndex(t, h, firstID) {
		t.Error("the filter does not use ix_api_requests_connection_at")
	}
}

// planUsesIndex reports whether Postgres, made to prefer an index, reads a
// connection's latest requests through the migration's index.
func planUsesIndex(t *testing.T, h *modtest.Harness, connection uuid.UUID) bool {
	t.Helper()
	ctx := t.Context()
	conn, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT * FROM identity.api_requests WHERE connection_id = $1 ORDER BY at DESC LIMIT 200`, connection)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	// A partition's copy of the index is named after its columns.
	if !strings.Contains(plan.String(), "connection_id_at") && !strings.Contains(plan.String(), "ix_api_requests_connection_at") {
		t.Log(plan.String())
		return false
	}
	return true
}

// The revocation endpoint's limit per address (A23), as the token
// endpoint's: 120 a minute, then 429 with Retry-After.
func TestOAuth_TheRevocationLimit(t *testing.T) {
	h := oauthHarness(t)
	c := h.Client(t)
	form := url.Values{"token": {"vta_unknown"}, "client_id": {claudeID}}
	for i := range 120 {
		if r := revokeRequest(c, form); r.Status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, r.Status, r.Body)
		}
	}
	r := revokeRequest(c, form)
	if r.Status != http.StatusTooManyRequests || r.Header("Retry-After") == "" || !strings.Contains(string(r.Body), `"error":"invalid_request"`) {
		t.Errorf("the 121st: %d %q %s", r.Status, r.Header("Retry-After"), r.Body)
	}
}

// A registered client archived while its code is being exchanged (C8): the
// two queue on the connection's row, and whichever wins, no live connection
// of the archived client is left.
func TestOAuth_AnArchiveRacingAnExchange(t *testing.T) {
	h := oauthHarness(t)
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	for round := range 5 {
		c := registerOAuthClient(t, owner, "Racer", "http://127.0.0.1/cb")
		code := approveCode(t, owner, authorizeQuery(c.ClientID, "http://127.0.0.1/cb", "read"), integration, "read")
		var wg sync.WaitGroup
		wg.Add(2)
		var exchanged, archived int
		go func() {
			defer wg.Done()
			r, _ := tokenRequest(h.Client(t), codeForm(code, c.ClientID, "http://127.0.0.1/cb"))
			exchanged = r.Status
		}()
		go func() {
			defer wg.Done()
			archived = owner.Do(http.MethodPost, "/api/v1/identity/oauth-clients/"+c.ID.String()+"/archive", nil, oauthSkip, modtest.Header("If-Match", c.Version)).Status
		}()
		wg.Wait()
		if archived != http.StatusNoContent || (exchanged != http.StatusOK && exchanged != http.StatusBadRequest) {
			t.Fatalf("round %d: exchange %d, archive %d", round, exchanged, archived)
		}
		if n := h.Count(t, `SELECT count(*) FROM identity.oauth_connections WHERE client_id = $1 AND revoked_at IS NULL`, c.ClientID); n != 0 {
			t.Errorf("round %d: %d live connections of an archived client", round, n)
		}
	}
}

// A pool of one (the lease-worker lessons): a refresh whose document has
// expired and is fetched again, its revocation when the document changed,
// the revocation endpoint, the allow-list's save and a registered client's
// archive each finish without holding one connection while waiting for a
// second, and the revalidation's fetch holds none.
func TestOAuth_DeliveryCOnAPoolOfOne(t *testing.T) {
	host := modtest.NewOAuthClientHost(t, "claude.ai")
	doc := []byte(`{"client_id":"` + claudeID + `","client_name":"Claude","redirect_uris":["` + claudeReturn + `"],"token_endpoint_auth_method":"none"}`)
	host.Serve(claudeID, doc, "max-age=300")
	h := modtest.New(t, modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers")),
		modtest.WithPoolMaxConns(1), modtest.WithEnv("OAUTH_ENABLED", "1"), modtest.WithOAuthClientHost(host))
	owner, _ := signInOwner(t, h)
	integration := h.Integration(t, "Agent")
	setMCPAccess(t, h, integration, "read")

	tok := connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	h.Advance(6 * time.Minute)
	host.Serve(claudeID, []byte(strings.Replace(string(doc), claudeReturn, "https://claude.ai/elsewhere", 1)), "")
	r, next := tokenRequest(h.Client(t), refreshForm(tok.RefreshToken, claudeID))
	if r.Status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	waitRevalidations(h)
	if reason := revokeReason(t, h, connectionOf(t, h, integration)); reason != "client_document_changed" {
		t.Errorf("the revalidation: %q", reason)
	}
	if r, _ := tokenRequest(h.Client(t), refreshForm(next.RefreshToken, claudeID)); r.Status != http.StatusBadRequest {
		t.Errorf("after the revalidation: %d %s", r.Status, r.Body)
	}

	host.Serve(claudeID, doc, "max-age=300")
	h.Advance(time.Minute)
	tok = connectClient(t, h, owner, integration, "read", claudeID, claudeReturn)
	if r := revokeRequest(h.Client(t), url.Values{"token": {tok.RefreshToken}, "client_id": {claudeID}}); r.Status != http.StatusOK {
		t.Errorf("revoke: %d %s", r.Status, r.Body)
	}
	if r := owner.Do(http.MethodPut, "/api/v1/identity/oauth/settings", map[string]any{"clientHosts": []string{"claude.ai"}}, oauthSkip); r.Status != http.StatusOK {
		t.Errorf("the allow-list: %d %s", r.Status, r.Body)
	}
	c := registerOAuthClient(t, owner, "Cursor", "http://127.0.0.1/cb")
	connectClient(t, h, owner, integration, "read", c.ClientID, "http://127.0.0.1/cb")
	if r := owner.Do(http.MethodPost, "/api/v1/identity/oauth-clients/"+c.ID.String()+"/archive", nil, oauthSkip, modtest.Header("If-Match", c.Version)); r.Status != http.StatusNoContent {
		t.Errorf("archive: %d %s", r.Status, r.Body)
	}
}
