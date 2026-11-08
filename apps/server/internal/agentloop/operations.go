package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The three tools that reach the contract's operations (the Assistant design
// S5). The model gets a handful, not hundreds: the contract's operations are
// reached by searching, describing and calling, which also keeps a small local
// model's context small.
const (
	ToolSearchOperations = "search_operations"
	ToolDescribe         = "describe_operation"
	ToolCall             = "call_operation"
)

// Operations is the dispatcher as the three tools use it;
// *operations.Dispatcher is it.
type Operations interface {
	Search(ctx context.Context, userID uuid.UUID, query string, limit int) ([]operations.Info, error)
	List(ctx context.Context, userID uuid.UUID) ([]operations.Info, error)
	Describe(id string) (json.RawMessage, error)
	Call(ctx context.Context, req operations.CallRequest) (operations.CallResult, error)
}

// Dispatch is the dispatcher-backed tools of one run, as one principal. It is
// for one run at a time and keeps what the run has read of the dispatcher.
type Dispatch struct {
	Ops  Operations
	User uuid.UUID
	// Confirm answers a call to an operation whose mode is confirm: a change.
	// It never runs on the model's say-so; what it becomes is the module's
	// (the Assistant shows the person a card, another module may route it to a
	// review). nil refuses such a call as unavailable.
	Confirm func(ctx context.Context, info operations.Info, req operations.CallRequest, explain string) Result

	callable map[string]operations.Info // the operations the principal may call, by id; read once per run
	params   map[string]map[string]string
}

func objectSchema(required []string, props map[string]any) json.RawMessage {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// OperationToolDefs are the three tools as the model is offered them, in the
// order they are offered.
var OperationToolDefs = []Tool{
	{
		Name:        ToolSearchOperations,
		Description: "Find the operations (API calls) that read the data the person asks about. Returns up to 10 with id, method, path, summary and mode. Only operations the person may call are found.",
		Schema:      objectSchema([]string{"query"}, map[string]any{"query": str("What the data is about, in English words such as \"overdue invoices\".")}),
	},
	{
		Name:        ToolDescribe,
		Description: "Describe one operation found by search_operations: its parameters, request body and response, compacted.",
		Schema:      objectSchema([]string{"id"}, map[string]any{"id": str("The operation id from search_operations.")}),
	},
	{
		Name:        ToolCall,
		Description: "Call an operation as the person. Pass path, query and header parameters together in params, by name, and a JSON body in body when the operation takes one. An operation that reads data runs at once; lists come back truncated with their counts and any cursor for the next page. An operation that changes something never runs on your say-so: it is shown to the person on a confirmation card and runs only if they confirm it. Propose one change at a time, with all of its fields, and stop.",
		Schema: objectSchema([]string{"id"}, map[string]any{
			"id":      str("The operation id from search_operations."),
			"params":  map[string]any{"type": "object", "description": "Parameters by name: path parameters, query parameters and header parameters.", "additionalProperties": true},
			"body":    map[string]any{"type": "object", "description": "The JSON request body, when the operation takes one.", "additionalProperties": true},
			"explain": str("For a change: one sentence, in the person's language, saying what it will do, for the card."),
		}),
	},
}

// Tools are the three tools, bound to this run.
func (d *Dispatch) Tools() []Tool {
	calls := map[string]func(context.Context, json.RawMessage) Result{
		ToolSearchOperations: d.searchOperations,
		ToolDescribe:         d.describe,
		ToolCall:             d.callOperation,
	}
	tools := make([]Tool, len(OperationToolDefs))
	for i, def := range OperationToolDefs {
		call := calls[def.Name]
		def.Call = func(ctx context.Context, args json.RawMessage) (Result, error) { return call(ctx, args), nil }
		tools[i] = def
	}
	return tools
}

func (d *Dispatch) searchOperations(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		Query string `json:"query"`
	}
	if err := ToolArgs(raw, &args); err != nil || strings.TrimSpace(args.Query) == "" {
		return Refused("invalid_arguments", "query is required")
	}
	found, err := d.Ops.Search(ctx, d.User, args.Query, 10)
	if err != nil {
		return Result{Content: Wrap(map[string]string{"error": "search_failed"}), Status: "error", Mode: "tool", Operation: "tool:" + ToolSearchOperations}
	}
	type item struct {
		ID      string `json:"id"`
		Method  string `json:"method"`
		Path    string `json:"path"`
		Summary string `json:"summary"`
		Mode    string `json:"mode"`
	}
	items := make([]item, 0, len(found))
	for _, f := range found {
		items = append(items, item{f.ID, f.Method, f.Path, f.Summary, string(f.Mode)})
	}
	return Result{Content: Wrap(map[string]any{"operations": items}), Status: "ok", Mode: "tool", Operation: "tool:" + ToolSearchOperations}
}

// MayUse loads the operations the principal may call once and reports whether
// id is one, with its effective mode.
func (d *Dispatch) MayUse(ctx context.Context, id string) (operations.Info, bool, error) {
	if d.callable == nil {
		list, err := d.Ops.List(ctx, d.User)
		if err != nil {
			return operations.Info{}, false, err
		}
		d.callable = make(map[string]operations.Info, len(list))
		for _, info := range list {
			d.callable[info.ID] = info
		}
	}
	info, ok := d.callable[id]
	return info, ok, nil
}

// Callable is the operations MayUse has loaded, by id; nil before it has
// loaded any.
func (d *Dispatch) Callable() map[string]operations.Info { return d.callable }

func (d *Dispatch) describe(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		ID string `json:"id"`
	}
	if err := ToolArgs(raw, &args); err != nil || args.ID == "" {
		return Refused("invalid_arguments", "id is required")
	}
	info, ok, err := d.MayUse(ctx, args.ID)
	if err != nil {
		return Result{Content: Wrap(map[string]string{"error": "describe_failed"}), Status: "error"}
	}
	if !ok {
		return Result{Content: Wrap(map[string]string{"error": "operation_unknown", "message": "there is no operation with that id that you may use"}), Status: "refused", Mode: "tool", Operation: "tool:" + ToolDescribe}
	}
	doc, err := d.Ops.Describe(args.ID)
	if err != nil {
		return Refused("operation_unknown", "there is no operation with that id")
	}
	// The mode the model is told is the effective one, narrowed by the gate.
	var m map[string]any
	if json.Unmarshal(doc, &m) == nil {
		m["mode"] = string(info.Mode)
		d.setParams(args.ID, paramLocations(m))
		return Result{Content: Wrap(m), Status: "ok", Mode: "tool", Operation: "tool:" + ToolDescribe}
	}
	return Result{Content: Wrap(json.RawMessage(doc)), Status: "ok", Mode: "tool", Operation: "tool:" + ToolDescribe}
}

func (d *Dispatch) setParams(id string, loc map[string]string) {
	if d.params == nil {
		d.params = map[string]map[string]string{}
	}
	d.params[id] = loc
}

// paramLocations maps each parameter name of a described operation to where it
// goes: path, query or header.
func paramLocations(doc map[string]any) map[string]string {
	out := map[string]string{}
	for key, where := range map[string]string{"pathParameters": "path", "queryParameters": "query", "headerParameters": "header"} {
		list, _ := doc[key].([]any)
		for _, p := range list {
			if m, ok := p.(map[string]any); ok {
				if name, ok := m["name"].(string); ok {
					out[name] = where
				}
			}
		}
	}
	return out
}

// Locations is where each parameter of an operation goes, read once.
func (d *Dispatch) Locations(id string) map[string]string {
	if loc, ok := d.params[id]; ok {
		return loc
	}
	var loc map[string]string
	if doc, err := d.Ops.Describe(id); err == nil {
		var m map[string]any
		if json.Unmarshal(doc, &m) == nil {
			loc = paramLocations(m)
		}
	}
	d.setParams(id, loc)
	return loc
}

func (d *Dispatch) callOperation(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		ID      string          `json:"id"`
		Params  map[string]any  `json:"params"`
		Body    json.RawMessage `json:"body"`
		Explain string          `json:"explain"`
	}
	if err := ToolArgs(raw, &args); err != nil || args.ID == "" {
		return Refused("invalid_arguments", "id is required")
	}
	info, ok, err := d.MayUse(ctx, args.ID)
	if err != nil {
		return Result{Content: Wrap(map[string]string{"error": "call_failed"}), Status: "error", Operation: args.ID}
	}
	out := Result{Operation: args.ID, Mode: string(operations.ModeNever), Detail: info.Summary}
	if !ok {
		o := Refused("operation_unavailable", "that operation does not exist or you may not use it; search again with search_operations")
		o.Operation, o.Mode = args.ID, out.Mode
		return o
	}
	out.Mode = string(info.Mode)

	locations := d.Locations(args.ID)
	req := operations.CallRequest{OperationID: args.ID, PathParams: map[string]any{}, Query: map[string]any{}, Headers: map[string]string{}, Body: args.Body}
	for name, value := range args.Params {
		switch locations[name] {
		case "path":
			req.PathParams[name] = value
		case "header":
			if s, ok := value.(string); ok {
				req.Headers[name] = s
			}
		default:
			req.Query[name] = value
		}
	}

	if info.Mode != operations.ModeAuto {
		// A change never runs here: the module's Confirm decides what it becomes.
		if info.Mode == operations.ModeConfirm && d.Confirm != nil {
			return d.Confirm(ctx, info, req, args.Explain)
		}
		o := Refused("operation_unavailable", "that operation does not exist or you may not use it; search again with search_operations")
		o.Operation, o.Mode, o.Detail = args.ID, out.Mode, info.Summary
		return o
	}

	res, err := d.Ops.Call(ctx, req)
	if err != nil {
		var oe *operations.Error
		if errors.As(err, &oe) {
			o := Refused(oe.Code, oe.Message)
			o.Operation, o.Mode, o.Detail = args.ID, out.Mode, info.Summary
			return o
		}
		return Result{Content: Wrap(map[string]string{"error": "call_failed", "message": "the call failed unexpectedly"}), Status: "error", Operation: args.ID, Mode: out.Mode, Detail: info.Summary}
	}
	status := int32(res.Status)
	body := map[string]any{"status": res.Status}
	if res.Truncated {
		body["truncated"] = true
		body["note"] = "the answer was too large to read; narrow the request with filters or a smaller page"
	} else if b := CompactBody(res.Body); b != nil {
		body["body"] = b
	}
	state := "ok"
	switch {
	case res.Status == 401 || res.Status == 403:
		state = "refused"
	case res.Status >= 400:
		state = "error"
	}
	return Result{Content: Wrap(body), Status: state, HTTPStatus: &status, Mode: out.Mode, Operation: args.ID, Detail: info.Summary}
}
