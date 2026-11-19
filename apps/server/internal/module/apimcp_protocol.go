package module

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// This file is the MCP endpoint's protocol (MCP server design M7, M28): the
// JSON-RPC envelope, which era a request speaks, the checks of its headers
// against its body, and the shape of each answer. It is hand-rolled, stateless
// and answers JSON only, pinned to the specification pages of 2026-07-28
// (modern) and 2025-11-25 (legacy) by apimcp_test.go.

// The protocol versions the endpoint speaks: the modern one, served per
// request from the version its _meta names, and the legacy ones, which a
// client reaches with initialize.
const mcpModernVersion = "2026-07-28"

var (
	mcpLegacyVersions    = []string{"2025-11-25", "2025-06-18"}
	mcpSupportedVersions = append([]string{mcpModernVersion}, mcpLegacyVersions...)
)

// The _meta keys of the modern protocol.
const (
	mcpMetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	mcpMetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	mcpMetaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// The HTTP headers a request mirrors its body into.
const (
	mcpHeaderProtocolVersion = "MCP-Protocol-Version"
	mcpHeaderMethod          = "Mcp-Method"
	mcpHeaderName            = "Mcp-Name"
)

// The JSON-RPC error codes the endpoint answers with: JSON-RPC's own, and the
// two the MCP specification defines that apply here.
const (
	rpcParseError         = -32700
	rpcInvalidRequest     = -32600
	rpcMethodNotFound     = -32601
	rpcInvalidParams      = -32602
	rpcInternalError      = -32603
	rpcHeaderMismatch     = -32020
	rpcUnsupportedVersion = -32022
)

// The methods the endpoint answers.
const (
	mcpMethodDiscover    = "server/discover"
	mcpMethodInitialize  = "initialize"
	mcpMethodInitialized = "notifications/initialized"
	mcpMethodPing        = "ping"
	mcpMethodToolsList   = "tools/list"
	mcpMethodToolsCall   = "tools/call"
)

// The caching hints (M28): the discovery result carries nothing key-specific
// and may be kept an hour by anyone; the tool list varies by key, so it is
// private and stale at once.
const (
	mcpDiscoverTTLMs = 3_600_000
	mcpToolsTTLMs    = 0
)

// mcpServerName names the server in serverInfo.
const mcpServerName = "vantigo"

// mcpEra is which protocol a request is served under.
type mcpEra int

const (
	mcpModern mcpEra = iota
	mcpLegacy
)

// rpcMessage is one JSON-RPC request or notification as it arrives.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// notification reports whether the message carries no id: no answer but 202.
func (m rpcMessage) notification() bool { return len(m.ID) == 0 }

// rpcParams is what the endpoint reads of a message's params.
type rpcParams struct {
	Meta            map[string]json.RawMessage `json:"_meta"`
	Name            *string                    `json:"name"`
	Arguments       json.RawMessage            `json:"arguments"`
	ProtocolVersion string                     `json:"protocolVersion"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rpcFailure is an error answer and the HTTP status it goes with.
type rpcFailure struct {
	status int
	err    rpcError
}

func failure(status, code int, message string) *rpcFailure {
	return &rpcFailure{status: status, err: rpcError{Code: code, Message: message}}
}

// headerMismatch is the 400 -32020 every disagreement between a header and the
// body, and every missing required header, is answered with.
func headerMismatch(format string, args ...any) *rpcFailure {
	return failure(http.StatusBadRequest, rpcHeaderMismatch, "Header mismatch: "+fmt.Sprintf(format, args...))
}

// unsupportedVersion is the 400 -32022 naming what the endpoint speaks.
func unsupportedVersion(requested string) *rpcFailure {
	f := failure(http.StatusBadRequest, rpcUnsupportedVersion, "Unsupported protocol version")
	f.err.Data = map[string]any{"supported": mcpSupportedVersions, "requested": requested}
	return f
}

// mcpRequest is a parsed, classified request.
type mcpRequest struct {
	msg     rpcMessage
	params  rpcParams
	era     mcpEra
	version string
}

// parseMCP reads body as one JSON-RPC message. A batch, which no revision the
// endpoint speaks allows, is an invalid request.
func parseMCP(body []byte) (rpcMessage, rpcParams, *rpcFailure) {
	var msg rpcMessage
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || !json.Valid(body) {
		return msg, rpcParams{}, failure(http.StatusBadRequest, rpcParseError, "Parse error: the body is not JSON.")
	}
	if !strings.HasPrefix(trimmed, "{") {
		return msg, rpcParams{}, failure(http.StatusBadRequest, rpcInvalidRequest, "Invalid Request: the body must be one JSON-RPC request or notification.")
	}
	if err := json.Unmarshal(body, &msg); err != nil || msg.JSONRPC != "2.0" || msg.Method == "" {
		return rpcMessage{}, rpcParams{}, failure(http.StatusBadRequest, rpcInvalidRequest, `Invalid Request: a JSON-RPC 2.0 message has "jsonrpc": "2.0" and a method.`)
	}
	if !msg.notification() {
		var id any
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			return rpcMessage{}, rpcParams{}, failure(http.StatusBadRequest, rpcInvalidRequest, "Invalid Request: the id is a string or a number.")
		}
		switch id.(type) {
		case string, float64:
		default:
			msg.ID = nil
			return rpcMessage{}, rpcParams{}, failure(http.StatusBadRequest, rpcInvalidRequest, "Invalid Request: the id is a string or a number.")
		}
	}
	var params rpcParams
	if len(msg.Params) > 0 && string(msg.Params) != "null" {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return msg, rpcParams{}, failure(http.StatusBadRequest, rpcInvalidParams, "Invalid params: params is an object.")
		}
	}
	return msg, params, nil
}

// classify decides which era r speaks and checks its headers against its body
// (M28):
//
//   - initialize is legacy, its version negotiated down to one the endpoint
//     speaks;
//   - a _meta protocol version is served as that version says, and the
//     MCP-Protocol-Version header must equal it; under the modern version
//     Mcp-Method must equal the method, Mcp-Name the tool's name (decoded from
//     its =?base64?…?= form), and _meta must carry the client's capabilities;
//   - with no _meta, a legacy MCP-Protocol-Version selects the legacy method
//     set, the modern one is a mismatch (the body does not say it), and any
//     other is an unsupported version;
//   - with neither, a request is invalid: no revision the endpoint speaks
//     sends one so.
func classify(r *http.Request, msg rpcMessage, params rpcParams) (mcpRequest, *rpcFailure) {
	req := mcpRequest{msg: msg, params: params}
	header := r.Header.Get(mcpHeaderProtocolVersion)
	metaVersion, hasMeta, ok := metaString(params.Meta, mcpMetaProtocolVersion)
	if !ok {
		return req, failure(http.StatusBadRequest, rpcInvalidParams, "Invalid params: "+mcpMetaProtocolVersion+" is a string.")
	}

	switch {
	case msg.Method == mcpMethodInitialize:
		req.era, req.version = mcpLegacy, negotiateLegacy(params.ProtocolVersion)
		return req, nil
	case hasMeta:
		if header == "" {
			return req, headerMismatch("the %s header is missing", mcpHeaderProtocolVersion)
		}
		if header != metaVersion {
			return req, headerMismatch("%s header value %q does not match body value %q", mcpHeaderProtocolVersion, header, metaVersion)
		}
		switch {
		case metaVersion == mcpModernVersion:
			if f := checkModernHeaders(r, msg, params); f != nil {
				return req, f
			}
			req.era, req.version = mcpModern, metaVersion
		case slices.Contains(mcpLegacyVersions, metaVersion):
			req.era, req.version = mcpLegacy, metaVersion
		default:
			return req, unsupportedVersion(metaVersion)
		}
		return req, nil
	case slices.Contains(mcpLegacyVersions, header):
		req.era, req.version = mcpLegacy, header
		return req, nil
	case header == mcpModernVersion:
		return req, headerMismatch("%s header value %q has no %s in the body's _meta", mcpHeaderProtocolVersion, header, mcpMetaProtocolVersion)
	case header != "":
		return req, unsupportedVersion(header)
	case msg.Method == mcpMethodDiscover:
		// A modern method with nothing to say which version it speaks.
		return req, unsupportedVersion("")
	default:
		return req, failure(http.StatusBadRequest, rpcInvalidRequest,
			"Invalid Request: send the "+mcpHeaderProtocolVersion+" header, or initialize first.")
	}
}

// checkModernHeaders is the modern request's header-body validation.
func checkModernHeaders(r *http.Request, msg rpcMessage, params rpcParams) *rpcFailure {
	if _, present := params.Meta[mcpMetaClientCapabilities]; !present {
		return failure(http.StatusBadRequest, rpcInvalidParams, "Invalid params: _meta must carry "+mcpMetaClientCapabilities+".")
	}
	if msg.notification() {
		// The revision defines no header rule for a notification.
		return nil
	}
	method := r.Header.Get(mcpHeaderMethod)
	if method == "" {
		return headerMismatch("the %s header is missing", mcpHeaderMethod)
	}
	if method != msg.Method {
		return headerMismatch("%s header value %q does not match body value %q", mcpHeaderMethod, method, msg.Method)
	}
	if msg.Method != mcpMethodToolsCall {
		return nil
	}
	raw, present := r.Header[http.CanonicalHeaderKey(mcpHeaderName)]
	if !present || len(raw) == 0 {
		return headerMismatch("the %s header is missing", mcpHeaderName)
	}
	name, ok := decodeHeaderValue(raw[0])
	if !ok {
		return headerMismatch("the %s header is not a valid value", mcpHeaderName)
	}
	body := ""
	if params.Name != nil {
		body = *params.Name
	}
	if name != body {
		return headerMismatch("%s header value %q does not match body value %q", mcpHeaderName, name, body)
	}
	return nil
}

// decodeHeaderValue is a mirrored header's value, decoded from the Base64
// sentinel form =?base64?…?= when it has it.
func decodeHeaderValue(v string) (string, bool) {
	const prefix, suffix = "=?base64?", "?="
	if !strings.HasPrefix(v, prefix) || !strings.HasSuffix(v, suffix) || len(v) < len(prefix)+len(suffix) {
		return v, true
	}
	decoded, err := base64.StdEncoding.DecodeString(v[len(prefix) : len(v)-len(suffix)])
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// metaString reads key of meta as a string: present reports whether it is
// there at all, ok whether it is a string when it is.
func metaString(meta map[string]json.RawMessage, key string) (value string, present, ok bool) {
	raw, present := meta[key]
	if !present {
		return "", false, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

// negotiateLegacy answers an initialize's version: the same when the endpoint
// speaks it, its newest legacy version otherwise.
func negotiateLegacy(requested string) string {
	if slices.Contains(mcpLegacyVersions, requested) {
		return requested
	}
	return mcpLegacyVersions[0]
}

// methodAllowed reports whether the era serves method: the modern revision
// has no initialize and no ping, the legacy ones no server/discover.
func methodAllowed(era mcpEra, method string) bool {
	switch method {
	case mcpMethodToolsList, mcpMethodToolsCall:
		return true
	case mcpMethodDiscover:
		return era == mcpModern
	case mcpMethodInitialize, mcpMethodPing:
		return era == mcpLegacy
	}
	return false
}

// rpcResponse is a JSON-RPC response: a result or an error.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// writeRPC writes one JSON-RPC answer with status.
func writeRPC(w http.ResponseWriter, status int, resp rpcResponse) {
	resp.JSONRPC = "2.0"
	body, err := json.Marshal(resp)
	if err != nil {
		// Every result is built of plain values; this cannot happen.
		status, body = http.StatusInternalServerError, []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeFailure writes f for the message id (nil when it could not be read).
func writeFailure(w http.ResponseWriter, id json.RawMessage, f *rpcFailure) {
	e := f.err
	writeRPC(w, f.status, rpcResponse{ID: id, Error: &e})
}

// result is a result object in the era's shape: a modern one carries
// resultType and the server's identity in its _meta.
func (req mcpRequest) result(fields map[string]any, version string) map[string]any {
	if req.era == mcpModern {
		fields["resultType"] = "complete"
		fields["_meta"] = map[string]any{mcpMetaServerInfo: serverInfo(version)}
	}
	return fields
}

// serverInfo is the server's Implementation object.
func serverInfo(version string) map[string]any {
	return map[string]any{"name": mcpServerName, "title": "Vantigo", "version": version}
}
