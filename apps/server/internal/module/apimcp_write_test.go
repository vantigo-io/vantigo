package module

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The write tool (MCP server design M8, M11, M25, M31, M35): listed and
// callable only in the effective mode read_write, calling only operations of
// kind write, through the same loopback as read.

func newWriteAccess() *mcpFakeAccess {
	a := newMCPAccess()
	a.mode = contracts.MCPAccessReadWrite
	a.permissions = []string{"customers:view", "customers:create", "customers:update", "customers:delete"}
	return a
}

// bodyOf reads a looped-back request's body.
func bodyOf(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// write loops the call back as the caller, as read does: its method, its
// escaped path, its JSON body, its Authorization and address, no principal or
// grant on its context; the envelope is only recorded, as the module's router
// counts and records the call itself (M12, M22, M26).
func TestMCP_WriteLoopsBackAsTheCaller(t *testing.T) {
	access := newWriteAccess()
	var bodies []string
	customers := &mcpCustomers{}
	customers.answer = func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, bodyOf(t, r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"name":"Acme AS"}`))
	}
	h := composeMCP(t, access, customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
		"operation": "postCustomers", "body": map[string]any{"name": "Acme AS"},
	}))
	if isError || text != `{"id":7,"name":"Acme AS"}` {
		t.Errorf("write: %v %s", isError, text)
	}
	r := customers.last(t)
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/customers" || bodies[0] != `{"name":"Acme AS"}` {
		t.Errorf("looped back %s %s %s", r.Method, r.URL.Path, bodies[0])
	}
	if r.Header.Get("Authorization") != "Bearer vtg_test" || r.Header.Get("Content-Type") != "application/json" || r.RemoteAddr != "203.0.113.9:4000" {
		t.Errorf("headers %v, address %q", r.Header, r.RemoteAddr)
	}
	if customers.leaked {
		t.Error("the loopback's context carried a principal or a grant")
	}
	if got := access.operations(); !slices.Equal(got, []string{mcpOperationID}) {
		t.Errorf("recorded %v: the envelope only", got)
	}

	// A path parameter of a write is one escaped segment too.
	toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
		"operation": "putCustomersById", "path": map[string]any{"id": "../identity/x"}, "body": map[string]any{"name": "x"},
	}))
	if got := customers.last(t); got.Method != http.MethodPut || got.URL.EscapedPath() != "/api/v1/customers/%2E%2E%2Fidentity%2Fx" {
		t.Errorf("escaped path %s %q", got.Method, got.URL.EscapedPath())
	}
}

// read refuses a write and write refuses a read, each naming the tool that
// calls it; neither reaches the module (M11).
func TestMCP_EachToolCallsItsOwnKind(t *testing.T) {
	customers := &mcpCustomers{}
	h := composeMCP(t, newWriteAccess(), customers, true)
	if text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}})); !isError || !strings.Contains(text, "write tool") {
		t.Errorf("read of a write: %v %s", isError, text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "getCustomers"})); !isError || !strings.Contains(text, "read tool") {
		t.Errorf("write of a read: %v %s", isError, text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "getIdentityIntegrations"})); !isError || !strings.Contains(text, "not available") {
		t.Errorf("write outside the catalogue: %v %s", isError, text)
	}
	// A DELETE is delete's alone, and delete calls nothing else (M36).
	remove := map[string]any{"operation": "deleteCustomersById", "path": map[string]any{"id": "7"}}
	if text, isError := toolText(t, callTool(t, h, mcpToolWrite, remove)); !isError || !strings.Contains(text, "delete tool") {
		t.Errorf("write of a DELETE: %v %s", isError, text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolDelete, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}})); !isError || !strings.Contains(text, "write tool") {
		t.Errorf("delete of a POST: %v %s", isError, text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolDelete, map[string]any{"operation": "getCustomers"})); !isError || !strings.Contains(text, "read tool") {
		t.Errorf("delete of a read: %v %s", isError, text)
	}
	if len(customers.requests) != 0 {
		t.Errorf("%d refused calls reached the module", len(customers.requests))
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolDelete, remove)); isError {
		t.Error("delete of deleteCustomersById refused")
	}
	if r := customers.last(t); r.Method != http.MethodDelete || r.URL.Path != "/api/v1/customers/7" {
		t.Errorf("looped back %s %s", r.Method, r.URL.Path)
	}
}

// The mode is judged at every request (§6): under read_write search and
// describe reach the writes; lowered to read after tools/list, write is the
// protocol error and the writes leave search and describe.
func TestMCP_TheModeIsJudgedAtTheCall(t *testing.T) {
	access := newWriteAccess()
	customers := &mcpCustomers{}
	h := composeMCP(t, access, customers, true)
	found := func() string {
		text, _ := toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "create customer", "limit": 25}))
		return text
	}
	if text := found(); !strings.Contains(text, `"operation":"postCustomers"`) || !strings.Contains(text, `"kind":"write"`) {
		t.Errorf("read_write search: %s", text)
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolDescribe, map[string]any{"operation": "postCustomers"})); isError {
		t.Error("read_write: describe postCustomers refused")
	}
	body, header := modern(mcpMethodToolsList, nil)
	if tools, _ := answerOf(t, postMCP(t, h, body, header)).Result["tools"].([]any); len(tools) != 5 {
		t.Fatalf("read_write listed %d tools", len(tools))
	}

	access.mode = contracts.MCPAccessRead
	wantError(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}), http.StatusOK, rpcInvalidParams)
	wantError(t, callTool(t, h, mcpToolDelete, map[string]any{"operation": "deleteCustomersById", "path": map[string]any{"id": "1"}}), http.StatusOK, rpcInvalidParams)
	if text := found(); strings.Contains(text, `"kind":"write"`) {
		t.Errorf("read search found a write: %s", text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolDescribe, map[string]any{"operation": "postCustomers"})); !isError {
		t.Errorf("read: describe postCustomers: %s", text)
	}
	if len(customers.requests) != 0 {
		t.Errorf("%d calls reached the module", len(customers.requests))
	}
}

// A refused write's status passes through: an error result carrying the REST
// answer's status and code, the permission's 403 or a stale version's 412
// alike (M13).
func TestMCP_ARefusedWritesStatusPassesThrough(t *testing.T) {
	customers := &mcpCustomers{}
	h := composeMCP(t, newWriteAccess(), customers, true)
	for status, code := range map[int]string{http.StatusForbidden: "forbidden", http.StatusPreconditionFailed: "version_mismatch"} {
		customers.answer = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"No."}}`))
		}
		text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}))
		var problem map[string]any
		_ = json.Unmarshal([]byte(text), &problem)
		if !isError || problem["status"] != float64(status) || problem["code"] != code {
			t.Errorf("a %d: %v %s", status, isError, text)
		}
	}
}

// composeInventory composes the inventory module, its permissions delegable,
// for the operations customers' contract lacks: a declared If-Match and a
// POST marked read outside mcpReadPosts (M35).
func composeInventory(t *testing.T, access contracts.Access, inventory http.Handler) http.Handler {
	t.Helper()
	var perms []contracts.Permission
	for _, key := range []string{
		"counts-manage", "locations-manage", "locations-view", "lots-manage", "receipts-manage", "stock-adjust", "stock-view", "transfers-manage",
	} {
		perms = append(perms, contracts.Permission{Key: "inventory:" + key, Display: key, Description: key, Category: "Inventory", Delegable: true})
	}
	none := func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil }
	handler, err := Compose(Deps{Access: access, Config: &config.Config{
		Modules: []string{"inventory"}, AppOrigin: "https://vantigo.example.com", MCPEnabled: true,
	}},
		Module{Name: "identity", Mount: none, Permissions: []contracts.Permission{{Key: "identity:manage", Display: "d", Description: "d", Category: "c"}}},
		Module{Name: "inventory", Mount: func(Deps) (http.Handler, error) { return inventory, nil }, Permissions: perms},
	)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return handler
}

func inventoryAccess(mode string) *mcpFakeAccess {
	a := newMCPAccess()
	a.mode = mode
	a.permissions = []string{"inventory:locations-view", "inventory:locations-manage"}
	return a
}

// The header parameters a write may set (M31): the declared If-Match reaches
// the module and its refusal comes back with its status; an Idempotency-Key
// the operation does not declare, Authorization and a line break are refused
// before anything is called.
func TestMCP_AWriteSendsOnlyTheDeclaredHeaders(t *testing.T) {
	inventory := &mcpCustomers{answer: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"title":"Conflict","status":409,"code":"revision_conflict","detail":"The site has moved on to revision 4."}`))
	}}
	h := composeInventory(t, inventoryAccess(contracts.MCPAccessReadWrite), inventory)
	site := map[string]any{"code": "OSL", "name": "Oslo", "kind": "warehouse", "status": "active"}
	text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
		"operation": "putInventorySite", "path": map[string]any{"id": "1"}, "headers": map[string]any{"if-match": `"3"`}, "body": site,
	}))
	if !isError || !strings.Contains(text, `"code":"revision_conflict"`) || !strings.Contains(text, `"status":409`) {
		t.Errorf("a stale If-Match: %v %s", isError, text)
	}
	if r := inventory.last(t); r.Method != http.MethodPut || r.Header.Get("If-Match") != `"3"` || r.URL.Path != "/api/v1/inventory/sites/1" {
		t.Errorf("looped back %s %s If-Match %q", r.Method, r.URL.Path, r.Header.Get("If-Match"))
	}
	calls := len(inventory.requests)
	for name, headers := range map[string]map[string]any{
		"an undeclared Idempotency-Key": {"If-Match": "3", "Idempotency-Key": "k1"},
		"Authorization":                 {"If-Match": "3", "Authorization": "Bearer vtg_other"},
		"Cookie":                        {"If-Match": "3", "Cookie": "s=1"},
		"a line break":                  {"If-Match": "3\r\nX-Evil: 1"},
	} {
		if text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
			"operation": "putInventorySite", "path": map[string]any{"id": "1"}, "headers": headers, "body": site,
		})); !isError {
			t.Errorf("%s: %s", name, text)
		}
	}
	if len(inventory.requests) != calls {
		t.Errorf("%d refused calls reached the module", len(inventory.requests)-calls)
	}
}

// buildCall sets an Idempotency-Key an operation declares, under its declared
// name whatever the case it is given in, and never a declared header outside
// the three (M31).
func TestMCP_TheHeaderAllowList(t *testing.T) {
	e := &mcpOperation{ID: "postThing", Method: http.MethodPost, Path: "/api/v1/things", hasBody: true, params: []*openapi3.Parameter{
		{Name: "Idempotency-Key", In: openapiInHeader},
		{Name: "X-Request-Source", In: openapiInHeader},
	}}
	_, headers, _, refusal := buildCall(e, mcpCallArguments{Operation: e.ID, Headers: map[string]string{"idempotency-key": "k1"}})
	if refusal != nil || headers.Get("Idempotency-Key") != "k1" || len(headers) != 1 {
		t.Errorf("Idempotency-Key: %v %v", refusal, headers)
	}
	if _, _, _, refusal := buildCall(e, mcpCallArguments{Operation: e.ID, Headers: map[string]string{"X-Request-Source": "mcp"}}); refusal == nil {
		t.Error("a declared header outside the three was set")
	}
}

// A POST marked x-vantigo-api: read outside mcpReadPosts is a write to MCP
// (M35): out of reach in mode read, refused by read in read_write, and called
// by write, its PDF answered by type and length.
func TestMCP_UnlistedReadPostsAreWrites(t *testing.T) {
	inventory := &mcpCustomers{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}}
	labels := map[string]any{"operation": "printInventoryLabels", "body": map[string]any{"codes": []string{"OSL"}}}

	read := composeInventory(t, inventoryAccess(contracts.MCPAccessRead), inventory)
	if text, isError := toolText(t, callTool(t, read, mcpToolRead, labels)); !isError || !strings.Contains(text, "not available") {
		t.Errorf("read mode: %v %s", isError, text)
	}
	if text, _ := toolText(t, callTool(t, read, mcpToolSearch, map[string]any{"query": "labels", "limit": 25})); strings.Contains(text, "printInventoryLabels") {
		t.Errorf("read mode found it: %s", text)
	}

	rw := composeInventory(t, inventoryAccess(contracts.MCPAccessReadWrite), inventory)
	if text, isError := toolText(t, callTool(t, rw, mcpToolRead, labels)); !isError || !strings.Contains(text, "write tool") {
		t.Errorf("read_write, read: %v %s", isError, text)
	}
	if len(inventory.requests) != 0 {
		t.Fatalf("%d refused calls reached the module", len(inventory.requests))
	}
	text, isError := toolText(t, callTool(t, rw, mcpToolWrite, labels))
	if isError || text != `{"contentType":"application/pdf","length":8,"status":200}` {
		t.Errorf("read_write, write: %v %s", isError, text)
	}
	if r := inventory.last(t); r.Method != http.MethodPost || r.URL.Path != "/api/v1/inventory/labels" {
		t.Errorf("looped back %s %s", r.Method, r.URL.Path)
	}
}
