package identity_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The rules delivery C's review added (OAuth connections design A28, A29):
// an approval that races a registered client's archive or edit, a
// revalidation that only a document speaking for the client can end, an
// edit that ends the connections of a redirect URI it removes, the
// revocation endpoint and old tokens, who the consent page names, and the
// ended connections the request log's filter offers.

// An approval racing an archive (C8): the approval holds the client against
// the archive's lock, so whichever wins, no live connection of an archived
// client is left.
func TestOAuthClients_AnApprovalRacingAnArchive(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	const redirect = "http://127.0.0.1/cb"
	for round := range 6 {
		c := registerClient(t, owner, "Racer", redirect)
		query := authorizationQuery(c.ClientID, redirect)
		if p := previewOf(t, owner, query); p.Outcome != "consent" {
			t.Fatalf("round %d: preview %+v", round, p)
		}
		var wg sync.WaitGroup
		var approved, archived int
		wg.Add(2)
		go func() {
			defer wg.Done()
			approved = owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": query, "integrationId": agent, "access": "read", "decision": "approve"}).status
		}()
		go func() {
			defer wg.Done()
			archived = owner.do(http.MethodPost, oauthClientsPath+"/"+c.ID.String()+"/archive", nil, header("If-Match", c.Version)).status
		}()
		wg.Wait()
		if archived != http.StatusNoContent || (approved != http.StatusOK && approved != http.StatusBadRequest) {
			t.Fatalf("round %d: approve %d, archive %d", round, approved, archived)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.oauth_connections WHERE client_id = $1 AND revoked_at IS NULL`, c.ClientID); n != 0 {
			t.Errorf("round %d: %d live connections of an archived client", round, n)
		}
	}
}

// An approval after an edit dropped the redirect URI it names is refused;
// and the token endpoint refuses a registered client's connection whose
// client was archived without its connections being revoked (defence in
// depth).
func TestOAuthClients_ApprovalAndTokensHoldTheClient(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	c := registerClient(t, owner, "Cursor", "http://127.0.0.1/cb", "https://app.example.com/cb")

	query := authorizationQuery(c.ClientID, "https://app.example.com/cb")
	if p := previewOf(t, owner, query); p.Outcome != "consent" {
		t.Fatalf("preview %+v", p)
	}
	if r := owner.do(http.MethodPatch, oauthClientsPath+"/"+c.ID.String(), map[string]any{"name": "Cursor", "redirectUris": []string{"http://127.0.0.1/cb"}},
		header("If-Match", c.Version)); r.status != http.StatusOK {
		t.Fatalf("edit: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, oauthApprovePath, map[string]any{"query": query, "integrationId": agent, "access": "read", "decision": "approve"}); r.status != http.StatusBadRequest || r.code() != "client_refused" {
		t.Errorf("an approval for a dropped redirect: %d %s", r.status, r.body)
	}

	// A code issued, then the client archived underneath (no revocation).
	h.advance(time.Second)
	code, _ := approve(t, owner, authorizationQuery(c.ClientID, "http://127.0.0.1/cb"), agent, "read")
	h.exec(t, `UPDATE identity.oauth_clients SET archived_at = now() WHERE client_id = $1`, c.ClientID)
	r := tokenPost(h, t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {c.ClientID},
		"redirect_uri": {"http://127.0.0.1/cb"}, "code_verifier": {oauthVerifier}})
	if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("an exchange for an archived client: %d %s", r.status, r.body)
	}

	// A connection made, then the client archived underneath.
	h.exec(t, `UPDATE identity.oauth_clients SET archived_at = NULL WHERE client_id = $1`, c.ClientID)
	_, refresh, connection := connectAs(t, h, owner, agent, c.ClientID, "http://127.0.0.1/cb")
	h.exec(t, `UPDATE identity.oauth_clients SET archived_at = now() WHERE client_id = $1`, c.ClientID)
	if r, _ := refreshOnce(h, t, refresh, c.ClientID); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), `"invalid_grant"`) {
		t.Errorf("a refresh for an archived client: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, connection); reason != "" {
		t.Errorf("the refused refresh revoked: %q", reason)
	}
}

// An edit that removes a redirect URI ends the connections that use it
// (A29), audited, and keeps the others; a loopback URI's port is ignored, as
// at sign-in.
func TestOAuthClients_AnEditEndsTheConnectionsOfARemovedURI(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	c := registerClient(t, owner, "VS Code", "http://127.0.0.1/", "https://vscode.dev/redirect")
	_, _, loopback := connectAs(t, h, owner, agent, c.ClientID, "http://127.0.0.1:33418/")
	_, _, web := connectAs(t, h, owner, agent, c.ClientID, "https://vscode.dev/redirect")

	// Keeping the loopback URI with another path is a removal; keeping it
	// under another spelling of the same URI is not.
	r := owner.do(http.MethodPatch, oauthClientsPath+"/"+c.ID.String(), map[string]any{"name": "VS Code", "redirectUris": []string{"https://vscode.dev/redirect", "http://127.0.0.1/other"}},
		header("If-Match", c.Version))
	if r.status != http.StatusOK {
		t.Fatalf("edit: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, loopback); reason != "client_changed" {
		t.Errorf("the loopback connection: %q", reason)
	}
	if reason := revokeReasonOf(t, h, web); reason != "" {
		t.Errorf("the web connection: %q", reason)
	}
	events := h.auditEvents(t, "connection.revoked")
	if len(events) != 1 || !strings.Contains(events[0].After, `"revokeReason":"client_changed"`) {
		t.Errorf("connection.revoked %+v", events)
	}
}

// Revalidation (A28): only a JSON object that speaks for the client ends its
// connection. A page of another type, a JSON error body, JSON that is not an
// object and an object with no client_id are transient: nothing revoked,
// nothing cached. A different client_id is terminal.
func TestOAuth_RevalidationOnlyTheClientsDocumentEndsIt(t *testing.T) {
	t.Parallel()
	h, owner, _, host := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	good := mustRead(t, "claude.json")
	_, refresh, connection := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)

	for _, c := range []struct {
		name, contentType string
		body              []byte
	}{
		{"an HTML page", "text/html; charset=utf-8", []byte("<html><body>Down for maintenance</body></html>")},
		{"the document as text/plain", "text/plain", good},
		{"a JSON error", "application/json", []byte(`{"error":"x"}`)},
		{"JSON that is not an object", "application/json", []byte(`["a", 1]`)},
		{"an empty client_id", "application/problem+json", []byte(`{"client_id":"","title":"oops"}`)},
	} {
		h.advance(11 * time.Minute)
		host.ServeAs(hostedClaudeID, c.body, c.contentType, "")
		h.exec(t, `DELETE FROM identity.oauth_client_documents`)
		r, next := refreshOnce(h, t, refresh, hostedClaudeID)
		if r.status != http.StatusOK {
			t.Fatalf("%s: refresh %d %s", c.name, r.status, r.body)
		}
		refresh = next
		h.access.WaitOAuthRevalidations()
		if reason := revokeReasonOf(t, h, connection); reason != "" {
			t.Fatalf("%s revoked the connection: %q", c.name, reason)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.oauth_client_documents`); n != 0 {
			t.Errorf("%s was cached", c.name)
		}
	}

	h.advance(11 * time.Minute)
	host.Serve(hostedClaudeID, []byte(strings.Replace(string(good), hostedClaudeID, "https://claude.ai/oauth/another", 1)), "")
	if r, _ := refreshOnce(h, t, refresh, hostedClaudeID); r.status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.status, r.body)
	}
	h.access.WaitOAuthRevalidations()
	if reason := revokeReasonOf(t, h, connection); reason != "client_document_changed" {
		t.Errorf("another client_id: %q", reason)
	}
}

// The revocation endpoint and old tokens: an expired access token revokes
// nothing; a spent refresh token still ends its connection (A25).
func TestOAuth_RevocationOfOldTokens(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	access, refresh, connection := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)

	h.advance(61 * time.Minute)
	if r := revokeAt(h, t, url.Values{"token": {access}, "client_id": {hostedClaudeID}}); r.status != http.StatusOK {
		t.Fatalf("an expired access token: %d %s", r.status, r.body)
	}
	if reason := revokeReasonOf(t, h, connection); reason != "" {
		t.Fatalf("an expired access token revoked: %q", reason)
	}
	r, _ := refreshOnce(h, t, refresh, hostedClaudeID)
	if r.status != http.StatusOK {
		t.Fatalf("refresh: %d %s", r.status, r.body)
	}
	// The refresh token just spent ends the connection.
	if r := revokeAt(h, t, url.Values{"token": {refresh}, "client_id": {hostedClaudeID}}); r.status != http.StatusOK || revokeReasonOf(t, h, connection) != "revoked_by_client" {
		t.Errorf("a spent refresh token: %d %q", r.status, revokeReasonOf(t, h, connection))
	}
}

// The consent page names who registered a client only while they are an
// active Owner.
func TestOAuthClients_RegisteredByOnlyAnActiveOwner(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	createIntegration(t, owner, "Agent")
	otherID := h.createUser(t, owner, "other@example.com", "Owner")
	other := h.signIn(t, otherID, true)
	c := registerClient(t, other, "In-house", "https://app.example.com/cb")
	registeredBy := func() *string {
		t.Helper()
		r := owner.do(http.MethodPost, oauthPreviewPath, map[string]any{"query": authorizationQuery(c.ClientID, "https://app.example.com/cb")})
		var p struct {
			Client struct {
				RegisteredBy *string `json:"registeredBy"`
			} `json:"client"`
		}
		if err := json.Unmarshal(r.body, &p); err != nil {
			t.Fatal(err)
		}
		return p.Client.RegisteredBy
	}
	if by := registeredBy(); by == nil || *by == "" {
		t.Fatalf("an active Owner is not named: %v", by)
	}
	if r := owner.do(http.MethodPut, "/api/v1/identity/owner/users/"+otherID.String(), map[string]any{"displayName": "other@example.com", "email": "other@example.com", "role": "User"}); r.status != http.StatusOK {
		t.Fatalf("demote: %d %s", r.status, r.body)
	}
	if by := registeredBy(); by != nil {
		t.Errorf("a demoted Owner is still named: %q", *by)
	}
}

// With include=ended the list also has the connections that ended in the
// last 30 days, with when and why, for the request log's filter; without it
// only the live ones.
func TestOAuth_TheListWithEndedConnections(t *testing.T) {
	t.Parallel()
	h, owner, _, _ := oauthHarness(t)
	agent := createIntegration(t, owner, "Agent").Integration.ID
	setIntegrationMCP(t, h, agent, "read")
	_, _, live := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	_, _, ended := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	_, _, old := connectAs(t, h, owner, agent, hostedClaudeID, hostedClaudeReturn)
	if r := owner.do(http.MethodDelete, integrationsPath+"/"+agent.String()+"/connections/"+ended.String(), nil); r.status != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.status)
	}
	h.exec(t, `UPDATE identity.oauth_connections SET revoked_at = $2, revoke_reason = 'revoked' WHERE id = $1`, old, h.now().Add(-40*24*time.Hour))

	if got := connectionsOf(t, owner, agent); len(got) != 1 || got[0].ID != live {
		t.Errorf("without ended: %+v", got)
	}
	r := owner.do(http.MethodGet, integrationsPath+"/"+agent.String()+"/connections?include=ended", nil)
	var out struct {
		Connections []struct {
			ID        uuid.UUID `json:"id"`
			Status    string    `json:"status"`
			EndReason *string   `json:"endReason"`
			EndedAt   *string   `json:"endedAt"`
		} `json:"connections"`
	}
	r.json(&out)
	byID := map[uuid.UUID]string{}
	for _, c := range out.Connections {
		reason := ""
		if c.EndReason != nil {
			reason = *c.EndReason
		}
		byID[c.ID] = c.Status + "/" + reason
		if c.Status == "ended" && c.EndedAt == nil {
			t.Errorf("%s has no endedAt", c.ID)
		}
	}
	if len(byID) != 2 || byID[live] != "active/" || byID[ended] != "ended/revoked" {
		t.Errorf("with ended: %v", byID)
	}
}
