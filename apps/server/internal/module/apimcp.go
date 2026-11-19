package module

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The MCP endpoint, POST /api/mcp (MCP server design M5-M13, M21-M33): an AI
// client connects with an integration's API key and reaches, through its
// tools, exactly the operations that key may call over REST: the reads, and
// the writes too when the key's effective mode is read_write. The handler
// authenticates the key itself, refuses an integration whose MCP setting is
// off, and runs a tool call as a request of the key's own through the
// composed API handler, so every rule of the REST path applies to it. The one
// thing it adds is the setting: under read, nothing but a read is called.

// mcpPath is where the endpoint is registered.
const mcpPath = "/api/mcp"

// mcpOperationID names the envelope in the request log, as
// getApiOpenapiJson names the document: no contract declares the endpoint.
const mcpOperationID = "postApiMcp"

// mcpMaxBodyBytes caps a request's body (M5).
const mcpMaxBodyBytes = 1 << 20

// mcpRule is the endpoint's own check, the document's (K28): anonymous, so
// any key passes it to be judged here, and a read, so a read-only key does.
var mcpRule = documentRule

// mcpPolicy limits the endpoint per client address ahead of Check (M22), as
// the per-operation limits do: an unknown key is refused with nothing counted
// against any integration, so this is what bounds a flood of them.
var mcpPolicy = ratelimit.Policy{Name: "Mcp", Limit: 1200, Window: time.Minute}

// The words of the endpoint's own refusals.
const (
	mcpKeyRequiredMessage = "The MCP endpoint needs an integration's API key: Authorization: Bearer vtg_…"
	mcpNotEnabledMessage  = "MCP is not turned on for this integration. An Owner turns it on under Workspace admin › API access."
)

// mcpHandler serves the endpoint for one composition.
type mcpHandler struct {
	access    contracts.Access
	keys      keyGate
	limiter   *ratelimit.Limiter
	catalogue *mcpCatalogue
	inflight  *inFlight
	// api is the composed API handler every tool call loops back through,
	// bound once it exists (bind).
	api     http.Handler
	version string
}

func newMCPHandler(docs *apiDocuments, access contracts.Access, limiter *ratelimit.Limiter) *mcpHandler {
	return &mcpHandler{
		access:    access,
		keys:      docs.keys,
		limiter:   limiter,
		catalogue: newMCPCatalogue(docs),
		inflight:  &inFlight{limit: mcpInFlight, keys: map[uuid.UUID]int{}},
		version:   buildinfo.Version,
	}
}

// bind hands the handler the composed API it loops calls back through.
func (h *mcpHandler) bind(api http.Handler) { h.api = api }

// writeAuthError writes K19's body shape, {"error":{"code","message"}}.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// presentsKey reports whether r carries an Authorization: Bearer vtg_… header,
// the only credential the endpoint takes; Check judges it, and every other
// header beside it.
func presentsKey(r *http.Request) bool {
	for _, v := range r.Header.Values("Authorization") {
		scheme, token, ok := strings.Cut(strings.TrimSpace(v), " ")
		if ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(strings.TrimSpace(token), "vtg_") {
			return true
		}
	}
	return false
}

// keyRequired is the endpoint's 401 for a request with no key, or with a
// session (M27). It is written here, never by Access.Reject, which would
// clear a signed-in browser's session cookie.
func keyRequired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="vantigo"`)
	writeAuthError(w, http.StatusUnauthorized, "api_key_required", mcpKeyRequiredMessage)
}

func (h *mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		// No GET stream and no session to DELETE (M5).
		w.Header().Set("Allow", http.MethodPost)
		httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
		return
	}
	// What is no key at all, or a body over the cap by its own account, is
	// answered before anything is counted or looked up (M27, M5).
	if !presentsKey(r) {
		keyRequired(w)
		return
	}
	if r.ContentLength > mcpMaxBodyBytes {
		httpx.WriteProblem(w, r, http.StatusRequestEntityTooLarge, httpx.TooLargeDetail)
		return
	}
	if h.limiter != nil {
		d, err := h.limiter.Hit(r.Context(), mcpPolicy, httpx.ClientIP(r))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !d.Allowed {
			ratelimit.Reject(w, r, mcpPolicy, d)
			return
		}
	}

	p, err := h.access.Check(r, mcpRule)
	if err != nil {
		var refusal *contracts.APIKeyRefusal
		switch {
		case errors.As(err, &refusal):
			// A key identity found and refused: answered and recorded as on
			// every other path (K19, K23).
			h.keys.refuse(w, r, h.access, mcpRule, err, mcpOperationID, start)
		case errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden):
			keyRequired(w)
		default:
			httpx.WriteError(w, r, err)
		}
		return
	}
	if p.Via == nil || p.Via.Kind != contracts.ViaAPIKey {
		keyRequired(w)
		return
	}

	answer, call := h.plan(w, r, p)
	if call != nil {
		if !h.inflight.acquire(p.Via.KeyID) {
			// A key with mcpInFlight calls running is told to wait (S7); the
			// refusal is the envelope's, counted as one.
			h.keys.serve(w, r, p, mcpOperationID, start, func(w http.ResponseWriter) {
				t := toolError("This key has %d calls running already: wait for one to finish, and retry shortly.", mcpInFlight)
				writeRPC(w, http.StatusOK, rpcResponse{ID: call.req.msg.ID, Result: call.req.result(t.fields(), h.version)})
			})
			return
		}
		defer h.inflight.release(p.Via.KeyID)
		// A tool call that reaches the loopback is counted there, once, under
		// its real operation; the envelope is only recorded (M22).
		h.keys.recordOnly(w, r, p, mcpOperationID, start, func(w http.ResponseWriter) { h.run(w, r, call) })
		return
	}
	h.keys.serve(w, r, p, mcpOperationID, start, answer)
}

// mcpInFlight is how many tool calls one key may have looping back at once,
// per process (S7): enough for a client's parallel reads, few enough that one
// agent cannot hold the pool.
const mcpInFlight = 4

// inFlight counts the looped-back calls of each key.
type inFlight struct {
	mu    sync.Mutex
	limit int
	keys  map[uuid.UUID]int
}

// acquire takes one of key's places, and reports false when all are taken.
func (f *inFlight) acquire(key uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys[key] >= f.limit {
		return false
	}
	f.keys[key]++
	return true
}

func (f *inFlight) release(key uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys[key]--; f.keys[key] <= 0 {
		delete(f.keys, key)
	}
}

// mcpCall is a tools/call of read, write or delete that is ready to loop back.
type mcpCall struct {
	req     mcpRequest
	op      *mcpOperation
	target  string
	headers http.Header
	body    []byte
}

// plan reads and judges the request: it answers either how to answer it at
// once (answer) or the call to loop back (call). Nothing is written yet.
func (h *mcpHandler) plan(w http.ResponseWriter, r *http.Request, p contracts.Principal) (answer func(http.ResponseWriter), call *mcpCall) {
	fail := func(err error) (func(http.ResponseWriter), *mcpCall) {
		return func(w http.ResponseWriter) { httpx.WriteError(w, r, err) }, nil
	}
	mcp, ok := h.access.(contracts.MCPAccess)
	if !ok {
		return fail(errors.New("module: an API key was admitted by an Access that has no MCPAccess"))
	}

	// The body is read and judged first, so the key is read once, with its
	// scope only when a tool needs the catalogue (M22).
	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxBodyBytes))
	var (
		msg    rpcMessage
		params rpcParams
		req    mcpRequest
		f      *rpcFailure
	)
	if readErr == nil {
		if msg, params, f = parseMCP(body); f == nil {
			req, f = classify(r, msg, params)
		}
	}
	needScope := readErr == nil && f == nil && !msg.notification() && msg.Method == mcpMethodToolsCall &&
		params.Name != nil && (*params.Name == mcpToolSearch || *params.Name == mcpToolDescribe || *params.Name == mcpToolRead || *params.Name == mcpToolWrite ||
		*params.Name == mcpToolDelete)
	key, err := mcp.MCP(r.Context(), p, needScope)
	if err != nil {
		return fail(err)
	}
	mode := key.Effective()
	if mode == contracts.MCPAccessOff {
		return func(w http.ResponseWriter) {
			writeAuthError(w, http.StatusForbidden, "mcp_not_enabled", mcpNotEnabledMessage)
		}, nil
	}
	if readErr != nil {
		return fail(readErr)
	}
	if f != nil {
		return func(w http.ResponseWriter) { writeFailure(w, msg.ID, f) }, nil
	}
	if msg.notification() {
		// notifications/initialized and any other: accepted, never answered.
		return func(w http.ResponseWriter) { w.WriteHeader(http.StatusAccepted) }, nil
	}
	if !methodAllowed(req.era, msg.Method) {
		status := http.StatusNotFound
		if req.era == mcpLegacy {
			// A legacy client reads a 404 as its session gone.
			status = http.StatusOK
		}
		return func(w http.ResponseWriter) {
			writeFailure(w, msg.ID, failure(status, rpcMethodNotFound, "Method not found: "+msg.Method))
		}, nil
	}

	// writes is whether this request's mode reaches writes: judged afresh on
	// every request, so a mode lowered after tools/list holds at the next call.
	writes := mode == contracts.MCPAccessReadWrite
	result := func(fields map[string]any) (func(http.ResponseWriter), *mcpCall) {
		return func(w http.ResponseWriter) {
			writeRPC(w, http.StatusOK, rpcResponse{ID: msg.ID, Result: req.result(fields, h.version)})
		}, nil
	}
	switch msg.Method {
	case mcpMethodDiscover:
		return result(map[string]any{
			"supportedVersions": mcpSupportedVersions,
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      mcpInstructions,
			"ttlMs":             mcpDiscoverTTLMs,
			"cacheScope":        "public",
		})
	case mcpMethodInitialize:
		return result(map[string]any{
			"protocolVersion": req.version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      serverInfo(h.version),
			"instructions":    mcpInstructions,
		})
	case mcpMethodPing:
		return result(map[string]any{})
	case mcpMethodToolsList:
		fields := map[string]any{"tools": mcpTools(writes)}
		if req.era == mcpModern {
			fields["ttlMs"], fields["cacheScope"] = mcpToolsTTLMs, "private"
		}
		return result(fields)
	}

	// tools/call.
	if params.Name == nil {
		return func(w http.ResponseWriter) {
			writeFailure(w, msg.ID, failure(http.StatusOK, rpcInvalidParams, "Invalid params: a tools/call names its tool."))
		}, nil
	}
	name := *params.Name
	tool := func(t toolResult) (func(http.ResponseWriter), *mcpCall) { return result(t.fields()) }
	switch {
	case name == mcpToolSearch, name == mcpToolDescribe, name == mcpToolRead:
	case (name == mcpToolWrite || name == mcpToolDelete) && writes:
	default:
		// An unknown tool, and write or delete where they are not listed, is
		// a protocol error (M31).
		return func(w http.ResponseWriter) {
			writeFailure(w, msg.ID, failure(http.StatusOK, rpcInvalidParams, "Unknown tool: "+name))
		}, nil
	}
	index, err := h.catalogue.scoped(key.Permissions, key.ReadOnly)
	if err != nil {
		return fail(err)
	}
	switch name {
	case mcpToolSearch:
		return tool(searchTool(index, params.Arguments, writes))
	case mcpToolDescribe:
		return tool(describeTool(index, params.Arguments, writes))
	}

	var args mcpCallArguments
	if err := decodeArguments(params.Arguments, &args); err != nil {
		return tool(toolError("The arguments are not valid: %v.", err))
	}
	op, refusal := catalogueOperation(index, args.Operation, writes)
	if refusal != nil {
		return tool(*refusal)
	}
	if refusal := wrongTool(name, op); refusal != nil {
		return tool(*refusal)
	}
	target, headers, callBody, refusal := buildCall(op, args)
	if refusal != nil {
		return tool(*refusal)
	}
	return nil, &mcpCall{req: req, op: op, target: target, headers: headers, body: callBody}
}

// run loops call back through the composed API and answers its result,
// passing on the integration's RateLimit-* headers the loopback wrote.
func (h *mcpHandler) run(w http.ResponseWriter, r *http.Request, call *mcpCall) {
	var t toolResult
	if h.api == nil {
		t = toolError("The call could not be made.")
	} else {
		rec, panicked, ended := loopback(h.api, r, call.op, call.target, call.headers, call.body)
		write := call.op.Kind == mcpKindWrite
		// A call whose context ended — the deadline passed, or the client went
		// away — may still have answered: what it answered is the result,
		// unless it is a server error, which after an ended context says
		// nothing of whether a write was made (S1).
		answered := rec != nil && rec.wrote && rec.status < 500
		switch {
		case rec == nil:
			t = toolError("The call could not be made.")
		case panicked && write:
			t = toolError("%s failed unexpectedly, and the change may or may not have been made: read the record before "+
				"you retry. Quote the trace id %s when reporting it.", call.op.ID, httpx.TraceID(r))
		case panicked:
			t = toolError("%s failed unexpectedly. Quote the trace id %s when reporting it.", call.op.ID, httpx.TraceID(r))
		case ended && !answered && write:
			t = toolError("%s did not answer in time, and the change may or may not have been made: read the record "+
				"before you retry, and resend with the same Idempotency-Key where the operation takes one. Quote the "+
				"trace id %s when reporting it.", call.op.ID, httpx.TraceID(r))
		case ended && !answered:
			t = toolError("%s did not answer within %s. Quote the trace id %s when reporting it.", call.op.ID, mcpCallTimeout, httpx.TraceID(r))
		default:
			for _, name := range []string{"RateLimit-Limit", "RateLimit-Remaining", "RateLimit-Reset"} {
				if v := rec.header.Get(name); v != "" {
					w.Header().Set(name, v)
				}
			}
			t = callResult(call.op, rec)
		}
	}
	writeRPC(w, http.StatusOK, rpcResponse{ID: call.req.msg.ID, Result: call.req.result(t.fields(), h.version)})
}
