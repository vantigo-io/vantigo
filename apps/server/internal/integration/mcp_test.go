package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// The MCP endpoint end to end (MCP server design §7): identity's Check judging
// the envelope, the integration's MCP setting, and each read tool call looped
// back through the router's gate as the key — its networks, its narrowing,
// its integration's rate limit and the request log — with customers as the
// business module behind it.

const mcpPath = "/api/mcp"

// mcpOffContract is why no MCP exchange is validated: /api/mcp is not an
// operation of any contract (M12).
var mcpOffContract = modtest.SkipContract("the MCP endpoint is not an operation of the contract")

func mcpHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	opts = append([]modtest.Option{modtest.WithModule(customers.Module()), modtest.WithRecorder(contracttest.NewForModule("customers"))}, opts...)
	return modtest.New(t, opts...)
}

func setMCPAccess(t *testing.T, h *modtest.Harness, integration uuid.UUID, mode string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.integrations SET mcp_access = $2 WHERE user_id = $1`, integration, mode)
}

// mcpCall sends one modern request for method with params, as a conforming
// client does.
func mcpCall(c *modtest.Client, method string, params map[string]any) *modtest.Response {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
	opts := []modtest.RequestOption{
		mcpOffContract,
		modtest.Header("Accept", "application/json, text/event-stream"),
		modtest.Header("MCP-Protocol-Version", "2026-07-28"),
		modtest.Header("Mcp-Method", method),
	}
	if name, ok := params["name"].(string); ok {
		opts = append(opts, modtest.Header("Mcp-Name", name))
	}
	return c.Do(http.MethodPost, mcpPath, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, opts...)
}

// mcpTool is one tools/call's text and isError, the call answered 200.
func mcpTool(t *testing.T, c *modtest.Client, name string, args map[string]any) (*modtest.Response, string, bool) {
	t.Helper()
	r := mcpCall(c, "tools/call", map[string]any{"name": name, "arguments": args})
	if r.Status != http.StatusOK {
		t.Fatalf("tools/call %s: %d %s", name, r.Status, r.Body)
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	r.JSON(&out)
	if len(out.Result.Content) != 1 {
		t.Fatalf("tools/call %s: %s", name, r.Body)
	}
	return r, out.Result.Content[0].Text, out.Result.IsError
}

type mcpLogRow struct {
	keyID       uuid.UUID
	operationID string
	status      int
	reason      string
}

func mcpLog(t *testing.T, h *modtest.Harness, integration uuid.UUID) []mcpLogRow {
	t.Helper()
	h.FlushAPIRequests()
	rows, err := h.Pool().Query(context.Background(),
		`SELECT key_id, operation_id, status, coalesce(reason, '') FROM identity.api_requests WHERE integration_id = $1 ORDER BY at, operation_id`, integration)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []mcpLogRow
	for rows.Next() {
		var r mcpLogRow
		if err := rows.Scan(&r.keyID, &r.operationID, &r.status, &r.reason); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func remaining(t *testing.T, r *modtest.Response) int {
	t.Helper()
	n, err := strconv.Atoi(r.Header("RateLimit-Remaining"))
	if err != nil {
		t.Fatalf("RateLimit-Remaining %q: %s", r.Header("RateLimit-Remaining"), r.Body)
	}
	return n
}

// countedOnce asserts that after cost exactly one request of the limit since
// before — unless the minute's window turned between them, which leaves more
// remaining than before, and nothing to compare.
func countedOnce(t *testing.T, what string, before, after *modtest.Response) {
	t.Helper()
	b, a := remaining(t, before), remaining(t, after)
	if a > b {
		t.Logf("%s: the rate limit's window turned (%d → %d); not compared", what, b, a)
		return
	}
	if a != b-1 {
		t.Errorf("%s: remaining %d after %d, want %d: it counts once", what, a, b, b-1)
	}
}

// A read runs through the REST path as the key: the customers list comes
// back, the request log holds the real operation and the key beside the
// envelope, and the call counts once against the limit while tools/list
// counts once on its own (M12, M22).
func TestMCP_AReadRunsAsTheKey(t *testing.T) {
	h := mcpHarness(t)
	owner := h.SignIn(t, "customers:view", "customers:create")
	if r := owner.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Fjordkraft AS"}); r.Status != http.StatusCreated {
		t.Fatalf("create a customer: %d %s", r.Status, r.Body)
	}
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	key, keyID := h.APIKeyFor(t, integration, modtest.APIKey{Networks: []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15")}})
	c := h.APIKeyClient(t, key)
	setMCPAccess(t, h, integration, "read")

	r, text, isError := mcpTool(t, c, "read", map[string]any{"operation": "getCustomers", "query": map[string]any{"search": "Fjordkraft"}})
	if isError || !strings.Contains(text, "Fjordkraft AS") {
		t.Fatalf("read: %v %s", isError, text)
	}
	list := mcpCall(c, "tools/list", nil)
	if list.Status != http.StatusOK {
		t.Errorf("tools/list: %d %s", list.Status, list.Body)
	}
	countedOnce(t, "tools/list", r, list)
	again, _, _ := mcpTool(t, c, "read", map[string]any{"operation": "getCustomers"})
	countedOnce(t, "a second read", list, again)

	got := mcpLog(t, h, integration)
	var ops []string
	for _, row := range got {
		if row.keyID != keyID || row.status != http.StatusOK {
			t.Errorf("logged %+v", row)
		}
		ops = append(ops, row.operationID)
	}
	slices.Sort(ops)
	if want := []string{"getCustomers", "getCustomers", "postApiMcp", "postApiMcp", "postApiMcp"}; !slices.Equal(ops, want) {
		t.Errorf("logged %v, want %v", ops, want)
	}
}

// Who reaches the endpoint (M3, M6, M27): no key and a person's session are
// the endpoint's own 401, the session left signed in; a revoked key and one
// used from outside its networks are refused and recorded; an integration
// whose MCP is off is 403 mcp_not_enabled.
func TestMCP_WhoReachesIt(t *testing.T) {
	h := mcpHarness(t)
	person := h.SignIn(t, "customers:view")
	r := mcpCall(person, "tools/list", nil)
	if r.Status != http.StatusUnauthorized || r.Code() != "api_key_required" || r.Header("WWW-Authenticate") != `Bearer realm="vantigo"` || r.Header("Set-Cookie") != "" {
		t.Errorf("a session: %d %s, Set-Cookie %q", r.Status, r.Body, r.Header("Set-Cookie"))
	}
	if r := person.Do(http.MethodGet, "/api/v1/customers", nil); r.Status != http.StatusOK {
		t.Errorf("the person after /api/mcp: %d, want still signed in", r.Status)
	}
	if r := mcpCall(h.Client(t), "tools/list", nil); r.Status != http.StatusUnauthorized || r.Code() != "api_key_required" {
		t.Errorf("no key: %d %s", r.Status, r.Body)
	}

	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	outsideKey, outsideID := h.APIKeyFor(t, integration, modtest.APIKey{Networks: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}})
	if r := mcpCall(h.APIKeyClient(t, outsideKey), "tools/list", nil); r.Status != http.StatusUnauthorized || r.Code() != "unauthenticated" {
		t.Errorf("from outside the key's networks: %d %s", r.Status, r.Body)
	}
	revokedKey, revokedID := h.APIKeyFor(t, integration, modtest.APIKey{})
	h.Exec(t, `UPDATE identity.api_keys SET revoked_at = now() WHERE id = $1`, revokedID)
	if r := mcpCall(h.APIKeyClient(t, revokedKey), "tools/list", nil); r.Status != http.StatusUnauthorized {
		t.Errorf("a revoked key: %d %s", r.Status, r.Body)
	}
	got := mcpLog(t, h, integration)
	want := []mcpLogRow{
		{outsideID, "postApiMcp", http.StatusUnauthorized, "network"},
		{revokedID, "postApiMcp", http.StatusUnauthorized, "revoked"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("logged %+v, want %+v", got, want)
	}

	off := h.Integration(t, "Off", "customers:view")
	offKey, _ := h.APIKeyFor(t, off, modtest.APIKey{})
	if r := mcpCall(h.APIKeyClient(t, offKey), "tools/list", nil); r.Status != http.StatusForbidden || r.Code() != "mcp_not_enabled" {
		t.Errorf("MCP off: %d %s", r.Status, r.Body)
	}
	if got := mcpLog(t, h, off); len(got) != 1 || got[0].operationID != "postApiMcp" || got[0].status != http.StatusForbidden {
		t.Errorf("MCP off logged %+v", got)
	}
}

// mcpRPCError is the JSON-RPC error code of an answer, 0 when it has none.
func mcpRPCError(r *modtest.Response) int {
	var out struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	r.JSON(&out)
	if out.Error == nil {
		return 0
	}
	return out.Error.Code
}

// A write runs through the REST path as the key (M8, M12, M22): write is
// listed under read_write, the customer is created, the request log holds the
// real operation with the key beside the envelope, and the call counts once.
// Lowered to read, write is the protocol error and creates nothing.
func TestMCP_AWriteRunsAsTheKey(t *testing.T) {
	h := mcpHarness(t)
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")
	key, keyID := h.APIKeyFor(t, integration, modtest.APIKey{})
	c := h.APIKeyClient(t, key)

	list := mcpCall(c, "tools/list", nil)
	var tools struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	list.JSON(&tools)
	if n := len(tools.Result.Tools); n != 5 || tools.Result.Tools[3].Name != "write" || tools.Result.Tools[3].Annotations["destructiveHint"] != true ||
		tools.Result.Tools[4].Name != "delete" {
		t.Fatalf("tools %+v", tools.Result.Tools)
	}

	r, text, isError := mcpTool(t, c, "write", map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "Fjordkraft AS"}})
	if isError || !strings.Contains(text, `"customerNumber"`) {
		t.Fatalf("write: %v %s", isError, text)
	}
	countedOnce(t, "the write", list, r)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Fjordkraft AS'`); n != 1 {
		t.Errorf("%d customers created", n)
	}

	got := mcpLog(t, h, integration)
	var ops []string
	for _, row := range got {
		if row.keyID != keyID {
			t.Errorf("logged %+v", row)
		}
		if row.operationID == "postCustomers" && row.status != http.StatusCreated {
			t.Errorf("postCustomers logged %+v", row)
		}
		ops = append(ops, row.operationID)
	}
	slices.Sort(ops)
	if want := []string{"postApiMcp", "postApiMcp", "postCustomers"}; !slices.Equal(ops, want) {
		t.Errorf("logged %v, want %v", ops, want)
	}

	setMCPAccess(t, h, integration, "read")
	if r := mcpCall(c, "tools/call", map[string]any{"name": "write", "arguments": map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "Again AS"}}}); r.Status != http.StatusOK || mcpRPCError(r) != -32602 {
		t.Errorf("write after the mode went to read: %d %s", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 1 {
		t.Errorf("%d customers: a write went through under read", n)
	}
}

// What a key reaches (M3, M10, M25): under read_write a read-only key lists
// the three tools, reads, and its write is the protocol error; read refuses a
// write and names the write tool; a narrowed key neither finds nor reads, nor
// writes, what its narrowing leaves out.
func TestMCP_WhatAKeyReaches(t *testing.T) {
	h := mcpHarness(t)
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")
	readOnly, _ := h.APIKeyFor(t, integration, modtest.APIKey{ReadOnly: true})
	c := h.APIKeyClient(t, readOnly)
	r := mcpCall(c, "tools/list", nil)
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	r.JSON(&list)
	var names []string
	for _, tool := range list.Result.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, []string{"search_operations", "describe_operation", "read"}) {
		t.Errorf("tools %v", names)
	}
	if _, text, isError := mcpTool(t, c, "read", map[string]any{"operation": "getCustomers"}); isError {
		t.Errorf("a read-only key's read: %s", text)
	}
	if r := mcpCall(c, "tools/call", map[string]any{"name": "write", "arguments": map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}}); r.Status != http.StatusOK || mcpRPCError(r) != -32602 {
		t.Errorf("a read-only key's write: %d %s", r.Status, r.Body)
	}
	full, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
	if _, text, isError := mcpTool(t, h.APIKeyClient(t, full), "read", map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}); !isError || !strings.Contains(text, "write tool") {
		t.Errorf("a write through read: %v %s", isError, text)
	}
	viewOnly, _ := h.APIKeyFor(t, integration, modtest.APIKey{Permissions: []string{"customers:view"}})
	if _, text, isError := mcpTool(t, h.APIKeyClient(t, viewOnly), "write", map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}); !isError || !strings.Contains(text, "not available") {
		t.Errorf("a key narrowed to customers:view wrote: %v %s", isError, text)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers: a write went through", n)
	}

	narrow, _ := h.APIKeyFor(t, integration, modtest.APIKey{Permissions: []string{"customers:create"}})
	nc := h.APIKeyClient(t, narrow)
	if _, text, _ := mcpTool(t, nc, "search_operations", map[string]any{"query": "list customers"}); strings.Contains(text, `"getCustomers"`) {
		t.Errorf("a narrowed key found getCustomers: %s", text)
	}
	if _, text, isError := mcpTool(t, nc, "read", map[string]any{"operation": "getCustomers"}); !isError || !strings.Contains(text, "not available") {
		t.Errorf("a narrowed key read getCustomers: %s", text)
	}
}

// A looped-back call over the integration's limit is a result that says how
// long to wait; the envelope after it is the limit's own 429 (M9).
func TestMCP_TheLimitIsTheIntegrations(t *testing.T) {
	h := mcpHarness(t)
	integration := h.Integration(t, "Agent", "customers:view")
	setMCPAccess(t, h, integration, "read")
	h.Exec(t, `UPDATE identity.integrations SET rate_limit_per_minute = 60 WHERE user_id = $1`, integration)
	key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
	c := h.APIKeyClient(t, key)
	for i := range 60 {
		if r := mcpCall(c, "tools/list", nil); r.Status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i+1, r.Status, r.Body)
		}
	}
	_, text, isError := mcpTool(t, c, "read", map[string]any{"operation": "getCustomers"})
	var problem struct {
		Status     int    `json:"status"`
		Code       string `json:"code"`
		RetryAfter int    `json:"retryAfterSeconds"`
	}
	if err := json.Unmarshal([]byte(text), &problem); err != nil || !isError || problem.Status != http.StatusTooManyRequests || problem.Code != "rate_limited" || problem.RetryAfter <= 0 {
		t.Errorf("the 61st as a read: %v %s", isError, text)
	}
	if r := mcpCall(c, "tools/list", nil); r.Status != http.StatusTooManyRequests || r.Code() != "rate_limited" {
		t.Errorf("the 62nd as tools/list: %d %s", r.Status, r.Body)
	}
}

// MCP_ENABLED=0: the path answers the 404 of every unknown API path (M4).
func TestMCP_SwitchedOff(t *testing.T) {
	h := mcpHarness(t, modtest.WithEnv("MCP_ENABLED", "0"))
	c, integration := h.SignInAPIKey(t, modtest.APIKey{}, "customers:view")
	setMCPAccess(t, h, integration, "read")
	if r := mcpCall(c, "tools/list", nil); r.Status != http.StatusNotFound {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}
