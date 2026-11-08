package modtest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Operations is the Assistant's dispatcher (internal/operations) bound to this
// harness's composed API, the way Compose binds the one it gives the assistant
// module: over the merged contract of identity and the modules under test, the
// composed permission catalog, identity's Access and the composed handler. It is
// built on first use and shared by the harness's tests.
//
// The harness composes no assistant module, so Compose gave no module a
// dispatcher; a test of the dispatcher itself builds its own here, and a test
// of the assistant module gets Deps.Operations from Compose.
func (h *Harness) Operations(t testing.TB) *operations.Dispatcher {
	t.Helper()
	h.opsMu.Lock()
	defer h.opsMu.Unlock()
	if h.ops != nil {
		return h.ops
	}

	// The merged contract, as any signed-in person fetches it.
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: h.session(t, h.seedUser(t))})
	rec := httptest.NewRecorder()
	h.api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("modtest: GET /api/openapi.json answered %d: %s", rec.Code, rec.Body)
	}
	contract, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}

	catalog := map[string]contracts.Permission{}
	for _, m := range h.mods {
		for _, p := range m.Permissions {
			catalog[p.Key] = p
		}
	}
	d := operations.New()
	if err := d.Bind(contract, catalog, h.deps.Access, h.api, operations.Options{Origin: h.deps.Config.AppOrigin, AgentGrants: h.agentGrants}); err != nil {
		t.Fatalf("modtest: bind the operations dispatcher: %v", err)
	}
	h.ops = d
	return d
}

// AssistantContext is the context an assistant's run has for the person the
// client is signed in as: contracts.AsUser for them and an assistant grant
// issued from the client's own session, as the assistant module issues one
// while it serves the person's message. The grant lives for ttl.
func (h *Harness) AssistantContext(t testing.TB, c *Client, ttl time.Duration) context.Context {
	t.Helper()
	token, ok := c.sessionCookie()
	if !ok {
		t.Fatal("modtest: AssistantContext: the client has no session; sign it in first")
	}
	userID := h.userOf(t, token)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	messageCtx := contracts.WithRequest(req.Context(), httptest.NewRecorder(), req)
	grantID, err := h.grants.Issue(messageCtx, uuid.New(), uuid.New(), ttl)
	if err != nil {
		t.Fatalf("modtest: issue an assistant grant: %v", err)
	}
	return contracts.WithAssistantGrant(contracts.AsUser(context.Background(), userID), grantID)
}

// AgentContext is the context an agent's run has: a new agent user holding
// permissions through a role of its own, and an agent grant issued for a run of
// it for customerID (nil for a run with no customer), put on the context as the
// agents module does (contracts.WithGrant). The dispatcher reads the run, and
// so the customer, from the grant itself. It answers the agent's user id too,
// for a test that names it. An agent may call nothing until the test installs
// its gate with Operations(t).SetGate(contracts.GrantAgent, ...): with no gate
// for the kind every operation is never.
func (h *Harness) AgentContext(t testing.TB, customerID *int32, permissions ...string) (context.Context, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID, err := h.agentAccounts.Create(ctx, "Modtest agent")
	if err != nil {
		t.Fatalf("modtest: create an agent: %v", err)
	}
	if len(permissions) > 0 {
		h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, h.seedRole(t, permissions))
	}
	return h.AgentGrantContext(t, userID, uuid.New(), customerID), userID
}

// AgentGrantContext is the context of a run of an agent that exists already: an
// agent grant for the agent's user, for the agents module's agentID and for
// customerID (nil for a run with no customer), put on the context as the agents
// module does. A test of the agents module uses it for the agent it made
// through the API.
func (h *Harness) AgentGrantContext(t testing.TB, userID, agentID uuid.UUID, customerID *int32) context.Context {
	t.Helper()
	ctx := context.Background()
	grantID, err := h.agentGrants.Issue(ctx, contracts.IssueAgentGrant{
		AgentUserID: userID, AgentID: agentID, RunID: uuid.New(), CustomerID: customerID,
	})
	if err != nil {
		t.Fatalf("modtest: issue an agent grant: %v", err)
	}
	return contracts.WithGrant(ctx, contracts.GrantRef{Kind: contracts.GrantAgent, ID: grantID})
}

// userOf is the user a session token belongs to.
func (h *Harness) userOf(t testing.TB, token string) uuid.UUID {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("modtest: session token: %v", err)
	}
	hash := sha256.Sum256(raw)
	return One[uuid.UUID](t, h, `SELECT user_id FROM identity.sessions WHERE token_hash = $1`, hash[:])
}
