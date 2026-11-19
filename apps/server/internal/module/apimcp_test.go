package module

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/openapi/integrator"
)

// The MCP endpoint's conformance (MCP server design M7, M28): each message's
// shape as the specification pages of 2026-07-28 and 2025-11-25 give it —
// basic/transports/streamable-http, basic/versioning, basic/index,
// server/discover, server/tools, server/utilities/caching, and the legacy
// basic/lifecycle and basic/transports — and the handler's own rules around
// it, against a fake Access and a customers module that records what it is
// called with. The end-to-end rules (identity's Check, the router's gate, the
// request log, the limit) are internal/integration's mcp_test.go.

// mcpFakeAccess admits a key (or answers err) and reads mode as its MCP
// setting; it records what the request log would.
type mcpFakeAccess struct {
	fakeAccess
	err         error
	principal   *contracts.Principal
	mode        string
	readOnly    bool
	permissions []string

	mu       sync.Mutex
	recorded []contracts.APIRequest
	scopes   []bool // what each MCP call asked for
}

func (a *mcpFakeAccess) Check(r *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	a.checked = append(a.checked, rule)
	if a.err != nil {
		return contracts.Principal{}, a.err
	}
	if a.principal != nil {
		return *a.principal, nil
	}
	return contracts.Principal{UserID: uuid.New(), Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: uuid.New()}}, nil
}

func (a *mcpFakeAccess) MCP(_ context.Context, _ contracts.Principal, scope bool) (contracts.MCPKeyAccess, error) {
	a.mu.Lock()
	a.scopes = append(a.scopes, scope)
	a.mu.Unlock()
	m := contracts.MCPKeyAccess{Mode: a.mode, ReadOnly: a.readOnly}
	if scope {
		m.Permissions = a.permissions
	}
	return m, nil
}

func (a *mcpFakeAccess) Record(_ context.Context, req contracts.APIRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recorded = append(a.recorded, req)
}

func (a *mcpFakeAccess) operations() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.recorded {
		out = append(out, r.OperationID)
	}
	return out
}

func newMCPAccess() *mcpFakeAccess {
	return &mcpFakeAccess{mode: contracts.MCPAccessRead, permissions: []string{"customers:view", "customers:create"}}
}

// mcpCustomers is the customers module as a recorder: every request it is
// handed is kept, with whether its context carried a principal or a grant,
// and answered by answer.
type mcpCustomers struct {
	mu       sync.Mutex
	requests []*http.Request
	leaked   bool
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (c *mcpCustomers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.requests = append(c.requests, r)
	if _, ok := contracts.PrincipalFrom(r.Context()); ok {
		c.leaked = true
	}
	if _, ok := contracts.GrantFrom(r.Context()); ok {
		c.leaked = true
	}
	answer := c.answer
	c.mu.Unlock()
	if answer != nil {
		answer(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"items":[{"id":"1","name":"Acme AS"}]}`))
}

func (c *mcpCustomers) last(t *testing.T) *http.Request {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("the customers module was not called")
	}
	return c.requests[len(c.requests)-1]
}

func composeMCP(t *testing.T, access contracts.Access, customers http.Handler, enabled bool) http.Handler {
	t.Helper()
	mods := customersModules()
	mods[1].Mount = func(Deps) (http.Handler, error) { return customers, nil }
	handler, err := Compose(Deps{Access: access, Config: &config.Config{
		Modules: []string{"customers"}, AppOrigin: "https://vantigo.example.com", MCPEnabled: enabled,
	}}, mods...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return handler
}

// modernMeta is a modern request's _meta.
func modernMeta() map[string]any {
	return map[string]any{
		mcpMetaProtocolVersion:               mcpModernVersion,
		"io.modelcontextprotocol/clientInfo": map[string]any{"name": "test", "version": "1"},
		mcpMetaClientCapabilities:            map[string]any{},
	}
}

// modern is a modern request for method with params (a _meta added) and the
// headers a conforming client sends.
func modern(method string, params map[string]any) (map[string]any, http.Header) {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = modernMeta()
	h := http.Header{}
	h.Set(mcpHeaderProtocolVersion, mcpModernVersion)
	h.Set(mcpHeaderMethod, method)
	if name, ok := params["name"].(string); ok {
		h.Set(mcpHeaderName, name)
	}
	return map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, h
}

func postMCP(t *testing.T, h http.Handler, body any, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, mcpPath, bytes.NewReader(raw))
	req.RemoteAddr = "203.0.113.9:4000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer vtg_test")
	for k, values := range header {
		req.Header.Del(k)
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rpcAnswer is a JSON-RPC answer as the tests read it.
type rpcAnswer struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

func answerOf(t *testing.T, rec *httptest.ResponseRecorder) rpcAnswer {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q, want application/json: %d %s", ct, rec.Code, rec.Body)
	}
	var a rpcAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	if a.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q", a.JSONRPC)
	}
	return a
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status, code int) rpcAnswer {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
	a := answerOf(t, rec)
	if a.Error == nil || a.Error.Code != code {
		t.Fatalf("error %+v, want code %d: %s", a.Error, code, rec.Body)
	}
	return a
}

// toolText is a tools/call result's text and isError.
func toolText(t *testing.T, rec *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	a := answerOf(t, rec)
	if a.Error != nil {
		t.Fatalf("a protocol error: %+v", a.Error)
	}
	content, _ := a.Result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content %v", a.Result["content"])
	}
	item, _ := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Fatalf("content type %v", item["type"])
	}
	isError, ok := a.Result["isError"].(bool)
	if !ok {
		t.Fatalf("isError missing: %s", rec.Body)
	}
	text, _ := item["text"].(string)
	return text, isError
}

func callTool(t *testing.T, h http.Handler, name string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, header := modern(mcpMethodToolsCall, map[string]any{"name": name, "arguments": args})
	return postMCP(t, h, body, header)
}

// server/discover: resultType, supportedVersions, capabilities, instructions,
// serverInfo in _meta, and the public caching hints (server/discover,
// server/utilities/caching).
func TestMCP_Discover(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	body, header := modern(mcpMethodDiscover, nil)
	rec := postMCP(t, h, body, header)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	a := answerOf(t, rec)
	r := a.Result
	if string(a.ID) != "1" || r["resultType"] != "complete" || r["cacheScope"] != "public" || r["ttlMs"].(float64) <= 0 {
		t.Errorf("result %v", r)
	}
	versions, _ := r["supportedVersions"].([]any)
	if len(versions) == 0 || versions[0] != mcpModernVersion {
		t.Errorf("supportedVersions %v", versions)
	}
	if caps, _ := r["capabilities"].(map[string]any); caps["tools"] == nil {
		t.Errorf("capabilities %v", r["capabilities"])
	}
	if s, _ := r["instructions"].(string); !strings.Contains(s, "untrusted") {
		t.Errorf("instructions %q", s)
	}
	meta, _ := r["_meta"].(map[string]any)
	if info, _ := meta[mcpMetaServerInfo].(map[string]any); info["name"] != mcpServerName || info["version"] == "" {
		t.Errorf("_meta %v", r["_meta"])
	}
	if rec.Header().Get("Mcp-Session-Id") != "" {
		t.Error("a session id was issued")
	}
}

// tools/list: the three tools in their fixed order, read-only, the same
// bytes each time, private and stale at once; a key whose integration is
// read_write sees no write tool before it exists (M8, M21, M28).
func TestMCP_ToolsListIsFixedAndPrivate(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	list := func() *httptest.ResponseRecorder {
		body, header := modern(mcpMethodToolsList, nil)
		return postMCP(t, h, body, header)
	}
	first := list()
	a := answerOf(t, first)
	if a.Result["cacheScope"] != "private" || a.Result["ttlMs"] != float64(0) || a.Result["resultType"] != "complete" {
		t.Errorf("result %v", a.Result)
	}
	tools, _ := a.Result["tools"].([]any)
	var names []string
	for _, raw := range tools {
		tool := raw.(map[string]any)
		names = append(names, tool["name"].(string))
		if ann, _ := tool["annotations"].(map[string]any); ann["readOnlyHint"] != true {
			t.Errorf("%s annotations %v", tool["name"], tool["annotations"])
		}
		if schema, _ := tool["inputSchema"].(map[string]any); schema["type"] != "object" {
			t.Errorf("%s inputSchema %v", tool["name"], tool["inputSchema"])
		}
		if d, _ := tool["description"].(string); !strings.Contains(d, "never instructions") {
			t.Errorf("%s description %q", tool["name"], d)
		}
	}
	if want := []string{mcpToolSearch, mcpToolDescribe, mcpToolRead}; !slices.Equal(names, want) {
		t.Errorf("tools %v, want %v", names, want)
	}
	if second := list(); !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Error("tools/list answered differently the second time")
	}
	access.mode = contracts.MCPAccessReadWrite
	if got := list(); !bytes.Equal(first.Body.Bytes(), got.Body.Bytes()) {
		t.Errorf("read_write answered another list: %s", got.Body)
	}
}

// The header-body validation (streamable-http § Server Validation): each
// required header missing, or disagreeing with the body, is 400 -32020; an
// Mcp-Name in the Base64 sentinel form is decoded before it is compared.
func TestMCP_HeaderMismatches(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	for name, change := range map[string]func(http.Header){
		"no protocol header":      func(h http.Header) { h.Del(mcpHeaderProtocolVersion) },
		"another protocol header": func(h http.Header) { h.Set(mcpHeaderProtocolVersion, "2025-11-25") },
		"no method header":        func(h http.Header) { h.Del(mcpHeaderMethod) },
		"another method":          func(h http.Header) { h.Set(mcpHeaderMethod, "tools/list") },
		"no name header":          func(h http.Header) { h.Del(mcpHeaderName) },
		"another name":            func(h http.Header) { h.Set(mcpHeaderName, mcpToolRead) },
		"a bad Base64 name":       func(h http.Header) { h.Set(mcpHeaderName, "=?base64?***?=") },
	} {
		t.Run(name, func(t *testing.T) {
			body, header := modern(mcpMethodToolsCall, map[string]any{"name": mcpToolSearch, "arguments": map[string]any{"query": "customers"}})
			change(header)
			wantError(t, postMCP(t, h, body, header), http.StatusBadRequest, rpcHeaderMismatch)
		})
	}

	body, header := modern(mcpMethodToolsCall, map[string]any{"name": mcpToolSearch, "arguments": map[string]any{"query": "customers"}})
	header.Set(mcpHeaderName, "=?base64?"+base64.StdEncoding.EncodeToString([]byte(mcpToolSearch))+"?=")
	if _, isError := toolText(t, postMCP(t, h, body, header)); isError {
		t.Error("a Base64 Mcp-Name was refused")
	}

	// The modern version in the header with no _meta in the body.
	plain := map[string]any{"jsonrpc": "2.0", "id": 1, "method": mcpMethodToolsList}
	hdr := http.Header{mcpHeaderProtocolVersion: {mcpModernVersion}, mcpHeaderMethod: {mcpMethodToolsList}}
	wantError(t, postMCP(t, h, plain, hdr), http.StatusBadRequest, rpcHeaderMismatch)
}

// An unsupported version (basic/versioning): 400 -32022 with the versions the
// endpoint speaks and the one asked for, from _meta or from the header.
func TestMCP_UnsupportedVersions(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	body, header := modern(mcpMethodToolsList, nil)
	body["params"].(map[string]any)["_meta"].(map[string]any)[mcpMetaProtocolVersion] = "1900-01-01"
	header.Set(mcpHeaderProtocolVersion, "1900-01-01")
	a := wantError(t, postMCP(t, h, body, header), http.StatusBadRequest, rpcUnsupportedVersion)
	if a.Error.Data["requested"] != "1900-01-01" || len(a.Error.Data["supported"].([]any)) != len(mcpSupportedVersions) {
		t.Errorf("data %v", a.Error.Data)
	}

	plain := map[string]any{"jsonrpc": "2.0", "id": 2, "method": mcpMethodToolsList}
	a = wantError(t, postMCP(t, h, plain, http.Header{mcpHeaderProtocolVersion: {"2024-11-05"}}), http.StatusBadRequest, rpcUnsupportedVersion)
	if a.Error.Data["requested"] != "2024-11-05" || string(a.ID) != "2" {
		t.Errorf("answer %+v", a)
	}

	// With nothing naming a version, the missing header is a mismatch,
	// whatever the method.
	a = wantError(t, postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 3, "method": mcpMethodDiscover}, nil), http.StatusBadRequest, rpcHeaderMismatch)
	if !strings.Contains(a.Error.Message, "header is missing") || string(a.ID) != "3" {
		t.Errorf("answer %+v", a)
	}
	wantError(t, postMCP(t, h, plain, nil), http.StatusBadRequest, rpcHeaderMismatch)
}

// A modern request whose _meta lacks the client's capabilities is malformed:
// 400 -32602 (basic/index § _meta).
func TestMCP_ClientCapabilitiesAreRequired(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	body, header := modern(mcpMethodToolsList, nil)
	delete(body["params"].(map[string]any)["_meta"].(map[string]any), mcpMetaClientCapabilities)
	wantError(t, postMCP(t, h, body, header), http.StatusBadRequest, rpcInvalidParams)
}

// An unknown method is 404 -32601 for a modern request; a legacy client
// reads a 404 as its session gone, so it gets 200 -32601. initialize is never
// modern, and server/discover never legacy.
func TestMCP_UnknownMethods(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	body, header := modern("resources/list", nil)
	wantError(t, postMCP(t, h, body, header), http.StatusNotFound, rpcMethodNotFound)
	body, header = modern(mcpMethodPing, nil)
	wantError(t, postMCP(t, h, body, header), http.StatusNotFound, rpcMethodNotFound)

	legacy := http.Header{mcpHeaderProtocolVersion: {"2025-11-25"}}
	wantError(t, postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/list"}, legacy), http.StatusOK, rpcMethodNotFound)
	wantError(t, postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 1, "method": mcpMethodDiscover}, legacy), http.StatusOK, rpcMethodNotFound)
}

// The legacy flow (2025-11-25 basic/lifecycle, basic/transports): initialize
// negotiates a version the endpoint speaks and answers its capabilities,
// serverInfo and instructions with no session; notifications/initialized is
// 202 with no body; ping is {}; tools/list and tools/call answer in the
// legacy shape, with no resultType or caching hints. A session id that
// arrives is ignored.
func TestMCP_TheLegacyFlow(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	for requested, want := range map[string]string{"2025-11-25": "2025-11-25", "2025-06-18": "2025-06-18", "2025-03-26": "2025-11-25", "2099-01-01": "2025-11-25"} {
		rec := postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": "init", "method": mcpMethodInitialize, "params": map[string]any{
			"protocolVersion": requested, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "old", "version": "1"},
		}}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("initialize %s: %d %s", requested, rec.Code, rec.Body)
		}
		a := answerOf(t, rec)
		r := a.Result
		if string(a.ID) != `"init"` || r["protocolVersion"] != want || r["instructions"] == "" || r["resultType"] != nil {
			t.Errorf("initialize %s: %v", requested, r)
		}
		if caps, _ := r["capabilities"].(map[string]any); caps["tools"] == nil {
			t.Errorf("capabilities %v", r["capabilities"])
		}
		if info, _ := r["serverInfo"].(map[string]any); info["name"] != mcpServerName {
			t.Errorf("serverInfo %v", r["serverInfo"])
		}
		if rec.Header().Get("Mcp-Session-Id") != "" {
			t.Error("initialize issued a session id")
		}
	}

	legacy := http.Header{mcpHeaderProtocolVersion: {"2025-11-25"}, "Mcp-Session-Id": {"abc"}}
	rec := postMCP(t, h, map[string]any{"jsonrpc": "2.0", "method": mcpMethodInitialized}, legacy)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notifications/initialized: %d %q", rec.Code, rec.Body)
	}
	rec = postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 7, "method": mcpMethodPing}, legacy)
	if a := answerOf(t, rec); rec.Code != http.StatusOK || a.Result == nil || len(a.Result) != 0 || string(a.ID) != "7" {
		t.Errorf("ping: %d %s", rec.Code, rec.Body)
	}
	rec = postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 8, "method": mcpMethodToolsList}, legacy)
	if a := answerOf(t, rec); a.Result["tools"] == nil || a.Result["ttlMs"] != nil || a.Result["resultType"] != nil {
		t.Errorf("legacy tools/list: %s", rec.Body)
	}
	rec = postMCP(t, h, map[string]any{"jsonrpc": "2.0", "id": 9, "method": mcpMethodToolsCall,
		"params": map[string]any{"name": mcpToolSearch, "arguments": map[string]any{"query": "customers"}}}, legacy)
	if text, isError := toolText(t, rec); isError || !strings.Contains(text, "getCustomers") {
		t.Errorf("legacy tools/call: %s", rec.Body)
	}
	if a := answerOf(t, rec); a.Result["resultType"] != nil {
		t.Errorf("a legacy result carries resultType: %s", rec.Body)
	}
	// A legacy version in a request's _meta is served as that version.
	body, header := modern(mcpMethodToolsList, nil)
	body["params"].(map[string]any)["_meta"].(map[string]any)[mcpMetaProtocolVersion] = "2025-06-18"
	header.Set(mcpHeaderProtocolVersion, "2025-06-18")
	if a := answerOf(t, postMCP(t, h, body, header)); a.Result["tools"] == nil || a.Result["resultType"] != nil {
		t.Errorf("a legacy _meta version: %+v", a)
	}
}

// A notification is 202 with no body, whatever its method (streamable-http §
// Sending Messages).
func TestMCP_NotificationsAreAccepted(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	body, header := modern("notifications/cancelled", nil)
	delete(body, "id")
	rec := postMCP(t, h, body, header)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

// GET and DELETE are 405 with Allow: POST: no stream, no session (M5).
func TestMCP_OnlyPOST(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, mcpPath, nil))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
			t.Errorf("%s: %d Allow %q", method, rec.Code, rec.Header().Get("Allow"))
		}
	}
	if len(access.checked) != 0 {
		t.Error("a 405 was judged by Check")
	}
	// The subtree is not the endpoint.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, mcpPath+"/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/api/mcp/x: %d", rec.Code)
	}
}

// What is not one JSON-RPC message: not JSON (-32700), a batch or a message
// without jsonrpc or method (-32600), a null id (-32600).
func TestMCP_MalformedBodies(t *testing.T) {
	h := composeMCP(t, newMCPAccess(), &mcpCustomers{}, true)
	wantError(t, postMCP(t, h, "{nope", nil), http.StatusBadRequest, rpcParseError)
	wantError(t, postMCP(t, h, "", nil), http.StatusBadRequest, rpcParseError)
	wantError(t, postMCP(t, h, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, nil), http.StatusBadRequest, rpcInvalidRequest)
	if a := wantError(t, postMCP(t, h, `{"id":1,"method":"ping"}`, nil), http.StatusBadRequest, rpcInvalidRequest); string(a.ID) != "1" {
		t.Errorf("an invalid request with a valid id answered id %s", a.ID)
	}
	if a := wantError(t, postMCP(t, h, `{"jsonrpc":"2.0","id":"x"}`, nil), http.StatusBadRequest, rpcInvalidRequest); string(a.ID) != `"x"` {
		t.Errorf("a request without a method answered id %s", a.ID)
	}
	wantError(t, postMCP(t, h, `{"jsonrpc":"2.0","id":null,"method":"ping"}`, nil), http.StatusBadRequest, rpcInvalidRequest)
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", mcpMaxBodyBytes) + `"}}`
	if rec := postMCP(t, h, big, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over 1 MiB: %d", rec.Code)
	}
}

// An unknown tool, write among them while it does not exist, and a call that
// names none are protocol errors, -32602 (server/tools § Error Handling).
func TestMCP_UnknownTools(t *testing.T) {
	access := newMCPAccess()
	access.mode = contracts.MCPAccessReadWrite
	h := composeMCP(t, access, &mcpCustomers{}, true)
	for _, name := range []string{"write", "delete_everything"} {
		a := wantError(t, callTool(t, h, name, nil), http.StatusOK, rpcInvalidParams)
		if !strings.Contains(a.Error.Message, name) {
			t.Errorf("%s: %q", name, a.Error.Message)
		}
	}
	body, header := modern(mcpMethodToolsCall, nil)
	header.Set(mcpHeaderName, "")
	wantError(t, postMCP(t, h, body, header), http.StatusOK, rpcInvalidParams)
}

// Authentication is the handler's (M27): no key, or a person's session, is
// its own 401 with the bearer challenge, never Access.Reject, which would
// clear a session cookie; a key identity found and refused goes through
// Reject and is recorded; an integration whose MCP is off is 403
// mcp_not_enabled, recorded.
func TestMCP_Authentication(t *testing.T) {
	for name, access := range map[string]*mcpFakeAccess{
		"no key":      {err: contracts.ErrUnauthenticated},
		"a session":   {principal: &contracts.Principal{UserID: uuid.New(), SessionID: uuid.New()}},
		"a scim call": {principal: &contracts.Principal{}},
	} {
		t.Run(name, func(t *testing.T) {
			h := composeMCP(t, access, &mcpCustomers{}, true)
			body, header := modern(mcpMethodToolsList, nil)
			rec := postMCP(t, h, body, header)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != `Bearer realm="vantigo"` ||
				!strings.Contains(rec.Body.String(), `"code":"api_key_required"`) {
				t.Errorf("%d %v %s", rec.Code, rec.Header(), rec.Body)
			}
			if len(access.rejected) != 0 || len(access.recorded) != 0 {
				t.Errorf("rejected %v, recorded %v", access.rejected, access.recorded)
			}
		})
	}

	key, integration := uuid.New(), uuid.New()
	refused := &mcpFakeAccess{err: &contracts.APIKeyRefusal{KeyID: key, IntegrationID: integration, Reason: contracts.APIReasonNetwork, Err: contracts.ErrUnauthenticated}}
	h := composeMCP(t, refused, &mcpCustomers{}, true)
	body, header := modern(mcpMethodToolsList, nil)
	if rec := postMCP(t, h, body, header); rec.Code != http.StatusUnauthorized || len(refused.rejected) != 1 {
		t.Errorf("a refused key: %d, %d rejected", rec.Code, len(refused.rejected))
	}
	if got := refused.recorded; len(got) != 1 || got[0].OperationID != mcpOperationID || got[0].KeyID != key || got[0].Reason != contracts.APIReasonNetwork {
		t.Errorf("recorded %+v", got)
	}
	if rule := refused.checked[0]; rule.Kind != contracts.RuleAnonymous || rule.API != contracts.APIRead {
		t.Errorf("checked with %+v", rule)
	}

	off := newMCPAccess()
	off.mode = contracts.MCPAccessOff
	h = composeMCP(t, off, &mcpCustomers{}, true)
	rec := postMCP(t, h, body, header)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"mcp_not_enabled"`) {
		t.Errorf("off: %d %s", rec.Code, rec.Body)
	}
	if got := off.operations(); !slices.Equal(got, []string{mcpOperationID}) {
		t.Errorf("off recorded %v", got)
	}
}

// MCP_ENABLED off: the path is the catch-all's 404 (M4).
func TestMCP_SwitchedOffIsNotFound(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, false)
	body, header := modern(mcpMethodToolsList, nil)
	rec := postMCP(t, h, body, header)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" || len(access.checked) != 0 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// A composition with no Config serves no MCP endpoint: only MCP_ENABLED on
// does (fail-safe).
func TestMCP_NoConfigNoEndpoint(t *testing.T) {
	access := newMCPAccess()
	handler, _, err := composeFrom(Deps{Access: access}, embedded, customersModules()...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	body, header := modern(mcpMethodToolsList, nil)
	if rec := postMCP(t, handler, body, header); rec.Code != http.StatusNotFound || len(access.checked) != 0 {
		t.Errorf("no Config: %d %s", rec.Code, rec.Body)
	}
}

// A composition with no Access serves no MCP endpoint.
func TestMCP_NoAccessNoEndpoint(t *testing.T) {
	if _, err := IntegratorDocument(customersModules()...); err != nil {
		t.Fatalf("IntegratorDocument: %v", err)
	}
	mods := customersModules()
	handler, _, err := composeFrom(Deps{}, embedded, mods...)
	if err != nil {
		t.Fatalf("compose with no Access: %v", err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, mcpPath, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/mcp with no Access: %d", rec.Code)
	}
}

// search_operations: the dispatcher's scoring over the key's reads, at most
// 25, a write never among them; describe_operation within its cap, with
// $refs resolved; both refuse what the key cannot reach.
func TestMCP_SearchAndDescribe(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	text, isError := toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "list customers", "limit": 25}))
	var found struct {
		Operations []struct {
			Operation, Method, Kind string
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(text), &found); err != nil || isError {
		t.Fatalf("search: %v %s", err, text)
	}
	if len(found.Operations) == 0 || len(found.Operations) > 25 || found.Operations[0].Operation != "getCustomers" {
		t.Errorf("found %+v", found.Operations)
	}
	for _, op := range found.Operations {
		if op.Kind != mcpKindRead {
			t.Errorf("a write in the results: %+v", op)
		}
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "x", "limit": 26})); !isError {
		t.Error("a limit of 26 was taken")
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "the of"})); isError {
		t.Error("a query of stop words is an error, want no results")
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "x", "extra": 1})); !isError {
		t.Error("an unknown argument was taken")
	}

	text, isError = toolText(t, callTool(t, h, mcpToolDescribe, map[string]any{"operation": "getCustomers"}))
	if isError || len(text) > mcpDescribeMaxBytes || strings.Contains(text, "$ref") || !strings.Contains(text, `"queryParameters"`) || !strings.Contains(text, `"response"`) {
		t.Errorf("describe getCustomers (%d bytes): %s", len(text), text)
	}
	for _, id := range []string{"postCustomers", "getIdentityIntegrations", "nope", ""} {
		if text, isError := toolText(t, callTool(t, h, mcpToolDescribe, map[string]any{"operation": id})); !isError {
			t.Errorf("describe %q: %s", id, text)
		}
	}

	// A narrower key reaches less: without customers:view, nothing.
	access.permissions = []string{"customers:create"}
	text, _ = toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "customers"}))
	if text != `{"operations":[]}` {
		t.Errorf("a key without customers:view found %s", text)
	}
}

// read loops the call back through the composed API as the caller: its
// method, its escaped path, its query in a stable order, its Authorization,
// its address, Accept: application/json, and no principal or grant on its
// context (M12, M26).
func TestMCP_ReadLoopsBackAsTheCaller(t *testing.T) {
	access := newMCPAccess()
	customers := &mcpCustomers{}
	h := composeMCP(t, access, customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{
		"operation": "getCustomers",
		"query":     map[string]any{"search": "acme", "status": []string{"active", "inactive"}, "pageSize": "5"},
	}))
	if isError || text != `{"items":[{"id":"1","name":"Acme AS"}]}` {
		t.Errorf("read: %v %s", isError, text)
	}
	r := customers.last(t)
	if r.Method != http.MethodGet || r.URL.Path != "/api/v1/customers" || r.URL.RawQuery != "pageSize=5&search=acme&status=active&status=inactive" {
		t.Errorf("looped back %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	if r.Header.Get("Authorization") != "Bearer vtg_test" || r.Header.Get("Accept") != "application/json" || r.RemoteAddr != "203.0.113.9:4000" {
		t.Errorf("headers %v, address %q", r.Header, r.RemoteAddr)
	}
	if customers.leaked {
		t.Error("the loopback's context carried a principal or a grant")
	}
	if got := access.operations(); !slices.Equal(got, []string{mcpOperationID}) {
		t.Errorf("recorded %v: the envelope only, as the module's router records the call", got)
	}

	// A path parameter is one escaped segment: no "/" splits it and no ".."
	// climbs out.
	toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomer", "path": map[string]any{"id": "../identity/x"}}))
	if got := customers.last(t).URL.EscapedPath(); got != "/api/v1/customers/%2E%2E%2Fidentity%2Fx" {
		t.Errorf("escaped path %q", got)
	}
}

// read refuses, as results the model can correct, what it may not call: a
// write, an operation outside the key's catalogue, a parameter the operation
// does not declare, a header beyond the three, a line break in one.
func TestMCP_ReadRefuses(t *testing.T) {
	customers := &mcpCustomers{}
	h := composeMCP(t, newMCPAccess(), customers, true)
	for name, args := range map[string]map[string]any{
		"a write":                   {"operation": "postCustomers", "body": map[string]any{"name": "x"}},
		"not the key's":             {"operation": "getIdentityIntegrations"},
		"no operation":              {},
		"an undeclared query":       {"operation": "getCustomers", "query": map[string]any{"nope": "1"}},
		"an undeclared path":        {"operation": "getCustomers", "path": map[string]any{"id": "1"}},
		"a missing path parameter":  {"operation": "getCustomer"},
		"a list as a path":          {"operation": "getCustomer", "path": map[string]any{"id": []string{"1", "2"}}},
		"Authorization":             {"operation": "getCustomers", "headers": map[string]any{"Authorization": "Bearer vtg_other"}},
		"a body to a GET":           {"operation": "getCustomers", "body": map[string]any{"x": 1}},
		"a number as a query value": {"operation": "getCustomers", "query": map[string]any{"pageSize": 5}},
	} {
		if text, isError := toolText(t, callTool(t, h, mcpToolRead, args)); !isError {
			t.Errorf("%s: %s", name, text)
		}
	}
	if len(customers.requests) != 0 {
		t.Errorf("%d refused calls reached the module", len(customers.requests))
	}
}

// The result of a call (M13, M32): a refusal is an error with its status,
// code, title and detail and never the raw body; a 429 says how long to wait;
// a text over 100 KB once escaped is refused, never truncated; a panic is an
// error result, not a failed envelope.
func TestMCP_CallResults(t *testing.T) {
	customers := &mcpCustomers{}
	h := composeMCP(t, newMCPAccess(), customers, true)
	read := func() (map[string]any, string, bool) {
		text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"}))
		var v map[string]any
		_ = json.Unmarshal([]byte(text), &v)
		return v, text, isError
	}

	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"You do not have access to this.","fields":{"x":["stack trace"]}}}`))
	}
	if v, text, isError := read(); !isError || v["status"] != float64(403) || v["code"] != "forbidden" || v["detail"] != "You do not have access to this." || v["title"] != "Forbidden" || strings.Contains(text, "stack") {
		t.Errorf("a 403: %v %s", isError, text)
	}

	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"x","title":"Not Found","status":404,"detail":"No such customer.","traceId":"t"}`))
	}
	if v, text, isError := read(); !isError || v["status"] != float64(404) || v["detail"] != "No such customer." {
		t.Errorf("a problem: %v %s", isError, text)
	}

	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "17")
		w.Header().Set("RateLimit-Limit", "600")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"Too many requests."}}`))
	}
	if v, text, isError := read(); !isError || v["retryAfterSeconds"] != float64(17) || v["code"] != "rate_limited" {
		t.Errorf("a 429: %v %s", isError, text)
	}

	// 60 KB of quotes: under the cap as a body, twice it escaped.
	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["` + strings.Repeat(`\"`, 30_000) + `"]`))
	}
	if _, text, isError := read(); !isError || !strings.Contains(text, "larger than") {
		t.Errorf("an escaped text over the cap: %v %.200s", isError, text)
	}
	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"` + strings.Repeat("a", 200_000) + `"`))
	}
	if _, text, isError := read(); !isError || !strings.Contains(text, "larger than") {
		t.Errorf("a body over the cap: %v %.200s", isError, text)
	}

	customers.answer = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}
	if v, text, isError := read(); isError || v["contentType"] != "text/csv" || v["length"] != float64(8) {
		t.Errorf("a CSV: %v %s", isError, text)
	}

	customers.answer = func(http.ResponseWriter, *http.Request) { panic("boom") }
	if _, text, isError := read(); !isError || !strings.Contains(text, "failed unexpectedly") {
		t.Errorf("a panic: %v %s", isError, text)
	}
}

// The kind of an operation is the router's rule (M24): for every operation a
// key could call in every module's contract, read exactly when the
// integrator's document for a read-only key keeps it, which judges the
// x-vantigo-api mark on its own — except a POST marked read that is not in
// mcpReadPosts, which MCP takes for a write (M35). Every entry of
// mcpReadPosts must still be a POST marked read.
func TestMCP_KindIsTheRoutersRule(t *testing.T) {
	var names []string
	entries, err := fs.ReadDir(openapi.Files(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && name != "common" {
			names = append(names, name)
		}
	}
	full, err := embedded.combined(context.Background(), names)
	if err != nil {
		t.Fatal(err)
	}
	docs := newAPIDocuments(nil, nil, full, nil, "")
	contract, err := docs.contract()
	if err != nil {
		t.Fatal(err)
	}
	// Every permission the contracts name, delegable, so the documents keep
	// every operation a key could call.
	seen := map[string]bool{}
	var catalog []contracts.Permission
	for _, item := range contract.Paths.Map() {
		for _, op := range item.Operations() {
			access, _ := op.Extensions["x-vantigo-access"].(string)
			rule, _ := contracts.ParseRule(access)
			for _, key := range rule.Names {
				if !seen[key] && !strings.HasPrefix(key, "identity:") && !strings.HasPrefix(key, "agents:") {
					seen[key] = true
					catalog = append(catalog, contracts.Permission{Key: key, Delegable: true})
				}
			}
		}
	}
	var perms []string
	for key := range seen {
		perms = append(perms, key)
	}
	ops := func(filter *integrator.Filter) map[string]bool {
		doc, err := integrator.Build(contract, catalog, filter)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, item := range doc.Paths.Map() {
			for _, op := range item.Operations() {
				out[op.OperationID] = true
			}
		}
		return out
	}
	keyable, readable := ops(nil), ops(&integrator.Filter{Permissions: perms, ReadOnly: true})
	// The POSTs a read-only key may call: MCP reads only those it lists.
	readPosts := map[string]bool{}
	for _, item := range contract.Paths.Map() {
		if op := item.Post; op != nil && readable[op.OperationID] {
			readPosts[op.OperationID] = true
		}
	}
	for id := range mcpReadPosts {
		if !readPosts[id] {
			t.Errorf("mcpReadPosts lists %s, which is no POST marked read: drop it", id)
		}
	}
	if len(readPosts) == 0 {
		t.Fatal("no POST marked read: the test reads no contract")
	}
	kinds, err := operationKinds(contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyable) < 100 {
		t.Fatalf("only %d keyable operations: the test reads no contract", len(keyable))
	}
	for id := range keyable {
		want := mcpKindWrite
		if readable[id] && (!readPosts[id] || mcpReadPosts[id]) {
			// The router's rule, except a POST marked read that MCP has not
			// been told is safe, which is a write to MCP (M35).
			want = mcpKindRead
		}
		if kinds[id] != want {
			t.Errorf("%s: kind %q, want %q", id, kinds[id], want)
		}
	}
}

// The catalogues are kept per scope, bounded.
func TestMCP_TheCatalogueIsBounded(t *testing.T) {
	docs := newAPIDocuments(nil, nil, mustCustomersContract(t), map[string]contracts.Permission{
		"customers:view": {Key: "customers:view", Delegable: true},
	}, "")
	c := newMCPCatalogue(docs)
	for i := range mcpIndexes + 5 {
		if _, err := c.scoped([]string{"customers:view", "x:" + strings.Repeat("y", i)}, false); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := c.scoped([]string{"customers:view"}, true)
	again, _ := c.scoped([]string{"customers:view"}, true)
	if c.cached() != mcpIndexes || first != again {
		t.Errorf("%d cached, the same scope built twice: %v", c.cached(), first != again)
	}
	if len(first.ops) == 0 || first.byID["getCustomers"] == nil || first.byID["postCustomers"] != nil {
		t.Errorf("a read-only customers:view scope: %d operations", len(first.ops))
	}
}

func mustCustomersContract(t *testing.T) []byte {
	t.Helper()
	full, err := embedded.combined(context.Background(), []string{"customers"})
	if err != nil {
		t.Fatal(err)
	}
	return full
}

// decodeHeaderValue reads the Base64 sentinel and leaves anything else.
func TestMCP_DecodeHeaderValue(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain", "=?base64?SGVsbG8sIOS4lueVjA==?=": "Hello, 世界", "=?base64??=": "",
	} {
		if got, ok := decodeHeaderValue(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	if _, ok := decodeHeaderValue("=?base64?!!?="); ok {
		t.Error("bad Base64 decoded")
	}
}

// Without an API key in Authorization the endpoint answers 401 before it
// counts or looks anything up (S5).
func TestMCP_NoKeyIsRefusedFirst(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	for _, auth := range []string{"", "Basic dXNlcjpwdw==", "Bearer not-a-key"} {
		req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(`{}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "api_key_required") {
			t.Errorf("%q: %d %s", auth, rec.Code, rec.Body)
		}
	}
	if len(access.checked) != 0 {
		t.Errorf("Check ran %d times", len(access.checked))
	}
}

// A body over 1 MiB is 413: by its Content-Length before anything is read,
// and, sent without one, when reading passes the cap.
func TestMCP_TheBodyCap(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", mcpMaxBodyBytes) + `"}}`

	if rec := postMCP(t, h, big, nil); rec.Code != http.StatusRequestEntityTooLarge || len(access.checked) != 0 {
		t.Errorf("by Content-Length: %d, %d checked", rec.Code, len(access.checked))
	}
	req := httptest.NewRequest(http.MethodPost, mcpPath, io.NopCloser(strings.NewReader(big)))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer vtg_test")
	req.Header.Set(mcpHeaderProtocolVersion, "2025-11-25")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked: %d %s", rec.Code, rec.Body)
	}
}

// Only a tool that reads the catalogue asks identity for the key's scope
// (M22): tools/list and server/discover read the mode alone.
func TestMCP_TheScopeIsReadOnlyForTools(t *testing.T) {
	access := newMCPAccess()
	h := composeMCP(t, access, &mcpCustomers{}, true)
	for _, method := range []string{mcpMethodDiscover, mcpMethodToolsList} {
		body, header := modern(method, nil)
		postMCP(t, h, body, header)
	}
	toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "customers"}))
	if want := []bool{false, false, true}; !slices.Equal(access.scopes, want) {
		t.Errorf("scopes asked %v, want %v", access.scopes, want)
	}
}

// A call that does not answer within mcpCallTimeout is an error result naming
// the trace id; the operation's context ends with it.
func TestMCP_ACallTimesOut(t *testing.T) {
	old := mcpCallTimeout
	mcpCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { mcpCallTimeout = old })
	ended := make(chan struct{})
	customers := &mcpCustomers{answer: func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(ended)
	}}
	h := composeMCP(t, newMCPAccess(), customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"}))
	if !isError || !strings.Contains(text, "did not answer") || !strings.Contains(text, "trace id") {
		t.Errorf("%v %s", isError, text)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Error("the operation's context did not end")
	}
}

// A client that goes away cancels the call it was making, as on REST (S1).
func TestMCP_AGoneClientCancelsTheCall(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	customers := &mcpCustomers{answer: func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(ended)
		case <-time.After(10 * time.Second):
		}
	}}
	h := composeMCP(t, newMCPAccess(), customers, true)
	body, header := modern(mcpMethodToolsCall, map[string]any{"name": mcpToolRead, "arguments": map[string]any{"operation": "getCustomers"}})
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, mcpPath, bytes.NewReader(raw))
	req.Header = header
	req.Header.Set("Authorization", "Bearer vtg_test")
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-started
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the call's context was not cancelled with the client's")
	}
	<-done
}

// A burst of first requests for one scope builds its catalogue once.
func TestMCP_OneBuildPerScope(t *testing.T) {
	docs := newAPIDocuments(nil, nil, mustCustomersContract(t), map[string]contracts.Permission{
		"customers:view": {Key: "customers:view", Delegable: true},
	}, "")
	c := newMCPCatalogue(docs)
	var wg sync.WaitGroup
	got := make([]*mcpIndex, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = c.scoped([]string{"customers:view"}, false)
		}()
	}
	wg.Wait()
	for _, index := range got {
		if index == nil || index != got[0] {
			t.Fatal("the same scope answered different catalogues")
		}
	}
}
