package operations

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The agent principal: a gate per kind, a call under an agent grant, and the
// customer scope (virtual employees design V8, V9b) against a small contract of
// its own.
const scopeContract = `{
  "openapi": "3.0.3",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/api/v1/things": {
      "get": {
        "operationId": "listCustomerThings", "summary": "List the things of a customer", "x-vantigo-access": "permission:things:view",
        "x-vantigo-customer-scope": {"param": "customerId"},
        "parameters": [{"in": "query", "name": "customerId", "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/api/v1/customers/{id}": {
      "get": {
        "operationId": "getCustomerThing", "summary": "Get a customer", "x-vantigo-access": "permission:things:view",
        "x-vantigo-customer-scope": {"param": "id"},
        "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/api/v1/things/{id}": {
      "get": {
        "operationId": "getOwnedThing", "summary": "Get a thing that belongs to a customer", "x-vantigo-access": "permission:things:view",
        "x-vantigo-customer-scope": {"resource": "customer.customerId"},
        "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/api/v1/plain/{id}": {
      "get": {
        "operationId": "getPlain", "summary": "Get something with no customer", "x-vantigo-access": "permission:things:view",
        "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    },
    "/api/v1/broken": {
      "get": {
        "operationId": "getBroken", "summary": "A scope that names both", "x-vantigo-access": "permission:things:view",
        "x-vantigo-customer-scope": {"param": "a", "resource": "b"},
        "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`

func newScopeFixture(t *testing.T) *fixture {
	t.Helper()
	user := uuid.New()
	f := &fixture{
		d:       New(),
		access:  &fakeAccess{holds: map[uuid.UUID]map[string]bool{user: {"things:view": true}}},
		handler: &recordingHandler{},
		user:    user,
	}
	if err := f.d.Bind([]byte(scopeContract), thingsCatalog, f.access, f.handler, Options{}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return f
}

// agentCtx is the context of an agent's run for the customer (nil for none).
func (f *fixture) agentCtx(customer *int32) context.Context {
	ctx := contracts.AsUser(context.Background(), f.user)
	ctx = contracts.WithGrant(ctx, contracts.GrantRef{Kind: contracts.GrantAgent, ID: uuid.New()})
	return contracts.WithAgentRun(ctx, contracts.AgentRun{AgentID: uuid.New(), RunID: uuid.New(), CustomerID: customer})
}

func customer(id int32) *int32 { return &id }

func TestGate_OnePerKind(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAssistant, func(context.Context, Info) Mode { return ModeAuto })
	f.d.SetGate(contracts.GrantAgent, func(_ context.Context, info Info) Mode {
		if info.ID == "getPlain" {
			return ModeAuto
		}
		return ModeNever
	})
	assistant, _ := f.ctx()
	agent := f.agentCtx(customer(7))

	if mode, _ := f.d.Effective(assistant, "listCustomerThings"); mode != ModeAuto {
		t.Errorf("the assistant's mode = %q, want its own gate's auto", mode)
	}
	if mode, _ := f.d.Effective(agent, "listCustomerThings"); mode != ModeNever {
		t.Errorf("the agent's mode = %q, want its own gate's never", mode)
	}
	if mode, _ := f.d.Effective(context.Background(), "listCustomerThings"); mode != ModeAuto {
		t.Errorf("a context with no grant = %q, want the assistant's gate", mode)
	}
	listed, _ := f.d.List(agent, f.user)
	if got := ids(listed); !slices.Equal(got, []string{"getPlain"}) {
		t.Errorf("List for the agent = %v, want only what its gate allows", got)
	}
	found, _ := f.d.Search(agent, f.user, "list the things of a customer", 10)
	if slices.Contains(ids(found), "listCustomerThings") {
		t.Errorf("Search for the agent finds %v, want none the gate narrowed", ids(found))
	}
	if _, err := f.d.Call(agent, CallRequest{OperationID: "listCustomerThings"}); !errors.Is(err, ErrNever) {
		t.Errorf("Call for the agent = %v, want ErrNever from its gate", err)
	}
	f.d.SetGate(contracts.GrantAgent, nil)
	if mode, _ := f.d.Effective(agent, "getPlain"); mode != ModeAuto {
		t.Errorf("an agent without a gate = %q, want the default gate (a read that is not sensitive stays auto)", mode)
	}
}

func TestGate_NoGateForAKindFailsClosed(t *testing.T) {
	f := newFixture(t, "things:view", "things:sensitive")
	f.d.SetGate(contracts.GrantAssistant, func(context.Context, Info) Mode { return ModeAuto })
	agent := f.agentCtx(nil)
	if mode, _ := f.d.Effective(agent, "getSecret"); mode != ModeNever {
		t.Errorf("a sensitive operation for an agent with no gate = %q, want never", mode)
	}
	if mode, _ := f.d.Effective(f.ctxOnly(), "getSecret"); mode != ModeAuto {
		t.Errorf("the assistant's own gate = %q, want auto: installing one kind's gate leaves the other's alone", mode)
	}
}

func TestCall_AnAgentGrant(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAgent, func(_ context.Context, info Info) Mode { return info.Mode })
	ctx := f.agentCtx(customer(7))
	ref, _ := contracts.GrantFrom(ctx)

	if _, err := f.d.Call(ctx, CallRequest{OperationID: "getPlain", PathParams: map[string]any{"id": "1"}}); err != nil {
		t.Fatalf("Call = %v", err)
	}
	got, _ := f.handler.seen()
	if got.RemoteAddr != "agent-"+f.user.String() {
		t.Errorf("RemoteAddr = %q, want agent-<user>", got.RemoteAddr)
	}
	if grant, ok := f.handlerGrant(); !ok || grant != ref {
		t.Errorf("the request carried grant %+v, want %+v", grant, ref)
	}

	// An agent grant with no run is refused, and so is a kind nobody knows.
	bare := contracts.WithGrant(contracts.AsUser(context.Background(), f.user), ref)
	if _, err := f.d.Call(bare, CallRequest{OperationID: "getPlain", PathParams: map[string]any{"id": "1"}}); !errors.Is(err, ErrNoGrant) {
		t.Errorf("an agent grant without its run = %v, want ErrNoGrant", err)
	}
	odd := contracts.WithGrant(contracts.AsUser(context.Background(), f.user), contracts.GrantRef{Kind: "robot", ID: uuid.New()})
	if _, err := f.d.Call(odd, CallRequest{OperationID: "getPlain", PathParams: map[string]any{"id": "1"}}); !errors.Is(err, ErrNoGrant) {
		t.Errorf("a grant of another kind = %v, want ErrNoGrant", err)
	}
}

// handlerGrant is the grant the last request carried.
func (f *fixture) handlerGrant() (contracts.GrantRef, bool) {
	f.handler.mu.Lock()
	defer f.handler.mu.Unlock()
	return f.handler.lastRef, f.handler.hasRef
}

func TestCustomerScope_Param(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAgent, func(_ context.Context, info Info) Mode { return info.Mode })
	agent := f.agentCtx(customer(7))

	// The query parameter is filled in when the agent leaves it out.
	if _, err := f.d.Call(agent, CallRequest{OperationID: "listCustomerThings"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.handler.seen(); got.RawQuery != "customerId=7" {
		t.Errorf("query = %q, want the run's customer filled in", got.RawQuery)
	}
	// And so is the path parameter.
	if _, err := f.d.Call(agent, CallRequest{OperationID: "getCustomerThing"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.handler.seen(); got.Path != "/api/v1/customers/7" {
		t.Errorf("path = %q, want the run's customer filled in", got.Path)
	}
	// The same value is accepted, in any scalar form.
	for _, v := range []any{"7", float64(7), 7} {
		if _, err := f.d.Call(agent, CallRequest{OperationID: "getCustomerThing", PathParams: map[string]any{"id": v}}); err != nil {
			t.Errorf("Call with the run's customer as %T = %v, want it allowed", v, err)
		}
	}
	// Another customer is refused before the handler is reached.
	_, before := f.handler.seen()
	for _, req := range []CallRequest{
		{OperationID: "listCustomerThings", Query: map[string]any{"customerId": 8}},
		{OperationID: "listCustomerThings", Query: map[string]any{"customerId": []any{7, 8}}},
		{OperationID: "getCustomerThing", PathParams: map[string]any{"id": "8"}},
	} {
		if _, err := f.d.Call(agent, req); !errors.Is(err, ErrCustomerScope) {
			t.Errorf("Call(%v) = %v, want ErrCustomerScope", req, err)
		}
	}
	if _, after := f.handler.seen(); after != before {
		t.Error("the handler was reached by a call for another customer")
	}
	// The caller's maps are not changed.
	q := map[string]any{}
	if _, err := f.d.Call(agent, CallRequest{OperationID: "listCustomerThings", Query: q}); err != nil || len(q) != 0 {
		t.Errorf("the caller's query = %v, %v, want it untouched", q, err)
	}
	// Validate applies the scope too.
	if err := f.d.Validate(agent, CallRequest{OperationID: "getCustomerThing", PathParams: map[string]any{"id": "8"}}); !errors.Is(err, ErrCustomerScope) {
		t.Errorf("Validate for another customer = %v, want ErrCustomerScope", err)
	}
}

func TestCustomerScope_Resource(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAgent, func(_ context.Context, info Info) Mode { return info.Mode })
	agent := f.agentCtx(customer(7))
	answer := func(body string, status int) {
		f.handler.respond = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	call := func() (CallResult, error) {
		return f.d.Call(agent, CallRequest{OperationID: "getOwnedThing", PathParams: map[string]any{"id": "t1"}})
	}

	answer(`{"id":"t1","customer":{"customerId":7}}`, 200)
	if res, err := call(); err != nil || res.Status != 200 || len(res.Body) == 0 {
		t.Errorf("the run's customer's thing = %+v, %v, want it", res, err)
	}
	for name, body := range map[string]string{
		"another customer":   `{"customer":{"customerId":8}}`,
		"no customer":        `{"customer":{"customerId":null}}`,
		"the field missing":  `{"customer":{}}`,
		"the object missing": `{"id":"t1"}`,
		"a string":           `{"customer":{"customerId":"7"}}`,
		"not an object":      `[7]`,
	} {
		answer(body, 200)
		res, err := call()
		if !errors.Is(err, ErrCustomerScope) || len(res.Body) != 0 {
			t.Errorf("%s: %+v, %v, want ErrCustomerScope and no body", name, res, err)
		}
	}
	// An error answer carries no data and is the handler's own.
	answer(`{"error":"not_found"}`, 404)
	if res, err := call(); err != nil || res.Status != 404 {
		t.Errorf("a 404 = %+v, %v, want it passed on", res, err)
	}
	// An answer too large to check is refused.
	big := `{"customer":{"customerId":7},"pad":"` + strings.Repeat("x", resultMaxBytes) + `"}`
	answer(big, 200)
	if _, err := call(); !errors.Is(err, ErrCustomerScope) {
		t.Errorf("a truncated answer = %v, want ErrCustomerScope", err)
	}
}

func TestCustomerScope_ARunWithNoCustomerReadsNothingScoped(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAgent, func(_ context.Context, info Info) Mode { return info.Mode })
	none := f.agentCtx(nil)
	for _, id := range []string{"listCustomerThings", "getCustomerThing", "getOwnedThing", "getBroken"} {
		req := CallRequest{OperationID: id, PathParams: map[string]any{"id": "1"}}
		if id == "listCustomerThings" || id == "getBroken" {
			req.PathParams = nil
		}
		if _, err := f.d.Call(none, req); !errors.Is(err, ErrCustomerScope) {
			t.Errorf("Call(%s) with no customer = %v, want ErrCustomerScope", id, err)
		}
	}
	if _, err := f.d.Call(none, CallRequest{OperationID: "getPlain", PathParams: map[string]any{"id": "1"}}); err != nil {
		t.Errorf("an operation with no scope = %v, want it allowed: the allowlist is the gate's", err)
	}
	// A malformed scope refuses even for a run with a customer.
	if _, err := f.d.Call(f.agentCtx(customer(7)), CallRequest{OperationID: "getBroken"}); !errors.Is(err, ErrCustomerScope) {
		t.Errorf("a malformed scope = %v, want ErrCustomerScope", err)
	}
}

func TestCustomerScope_TheAssistantIsNotScoped(t *testing.T) {
	f := newScopeFixture(t)
	f.d.SetGate(contracts.GrantAssistant, func(_ context.Context, info Info) Mode { return info.Mode })
	ctx, _ := f.ctx()
	if _, err := f.d.Call(ctx, CallRequest{OperationID: "getCustomerThing", PathParams: map[string]any{"id": "99"}}); err != nil {
		t.Errorf("the assistant's call for any customer = %v, want it allowed: its reach is its person's", err)
	}
	if info, _ := f.d.Info("getOwnedThing"); info.CustomerScope == nil || info.CustomerScope.Resource != "customer.customerId" {
		t.Errorf("Info.CustomerScope = %+v, want the resource scope for a gate to read", info.CustomerScope)
	}
	if info, _ := f.d.Info("getPlain"); info.CustomerScope != nil {
		t.Errorf("an operation with no extension has scope %+v", info.CustomerScope)
	}
}
