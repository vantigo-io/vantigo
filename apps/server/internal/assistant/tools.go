package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The tools of delivery A (the Assistant design S5). The model gets a handful,
// not hundreds: the contract's operations are reached by searching, describing
// and calling, which also keeps a small local model's context small.
const (
	toolSearchDocs       = "search_docs"
	toolSearchOperations = agentloop.ToolSearchOperations
	toolDescribe         = agentloop.ToolDescribe
	toolCall             = agentloop.ToolCall
	toolNavigate         = "navigate"
)

func objectSchema(required []string, props map[string]any) json.RawMessage {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}

var str = func(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// baseToolDefs are the tools of delivery A; toolDefs (tours.go) adds show_tour.
var baseToolDefs = func() []ai.Tool {
	defs := []ai.Tool{{
		Name:        toolSearchDocs,
		Description: "Search Vantigo's documentation for how to do something. Returns up to 4 sections with their page title, heading, the documentation path and an excerpt. Use it first for any how-to question.",
		Parameters:  objectSchema([]string{"query"}, map[string]any{"query": str("What to find, in a few words, in the person's language.")}),
	}}
	defs = append(defs, operationToolDefs()...)
	return append(defs, moduleToolDefs...)
}()

// operationToolDefs are agentloop's three tools, as the model is offered them.
func operationToolDefs() []ai.Tool {
	defs := make([]ai.Tool, len(agentloop.OperationToolDefs))
	for i, t := range agentloop.OperationToolDefs {
		defs[i] = ai.Tool{Name: t.Name, Description: t.Description, Parameters: t.Schema}
	}
	return defs
}

// moduleToolDefs are the tools this module adds, after agentloop's.
var moduleToolDefs = []ai.Tool{
	{
		Name: toolProposeBulk,
		Description: "Propose the same change to many items at once, such as every metering point in a price area or every customer with a tag: use it instead of changing items one by one whenever the person says all, every or each and more than a few items match. " +
			"It reads the selector (a list operation that reads data, with its filters) to its end and freezes the items it finds, at most 2000; it changes nothing. The person is shown a card with the count, a sample of 10 with each item's current and new value, and how many cannot be changed, and must press Start. Start runs only the first 5 items for real; the person then decides whether to go on, and the rest runs as a job they can stop. " +
			"Give the operation that changes one item (an operation you would otherwise propose with call_operation), the values that are the same for every item, and bind: which field of each listed item feeds which parameter of that operation, such as {\"id\": \"id\"} for a path parameter {id} or {\"revision\": \"revision\"} for a body field. Read the list with call_operation first and say how many items match before you propose. One bulk plan at a time; it ends your turn.",
		Parameters: objectSchema([]string{"selector", "operation", "bind"}, map[string]any{
			"selector": map[string]any{
				"type":        "object",
				"description": "The list that finds the items. It must be an operation that only reads.",
				"properties": map[string]any{
					"operationId": str("The list operation's id from search_operations."),
					"query":       map[string]any{"type": "object", "description": "The filters, by query parameter name. Do not give page, pageSize, cursor, limit or offset: the server pages to the end.", "additionalProperties": true},
					"pathParams":  map[string]any{"type": "object", "additionalProperties": true},
					"body":        map[string]any{"type": "object", "description": "The request body, for a POST that only reads.", "additionalProperties": true},
				},
				"required": []string{"operationId"},
			},
			"operation": map[string]any{
				"type":        "object",
				"description": "The change made to each item.",
				"properties": map[string]any{
					"operationId": str("The id of the operation that changes one item."),
					"pathParams":  map[string]any{"type": "object", "description": "Path parameters that are the same for every item.", "additionalProperties": true},
					"body":        map[string]any{"type": "object", "description": "The values to set, the same for every item; give every field the operation needs besides the bound ones.", "additionalProperties": true},
				},
				"required": []string{"operationId"},
			},
			"bind":    map[string]any{"type": "object", "description": "Which item field feeds which parameter: {\"<path parameter or body field of the operation>\": \"<field of each listed item>\"}.", "additionalProperties": map[string]any{"type": "string"}},
			"explain": str("One sentence, in the person's language, saying what the change does and to which items, for the card."),
		}),
	},
	{
		Name:        toolNavigate,
		Description: "Offer the person a button that opens a screen or a record in Vantigo. It does not move the screen by itself. Use a route from the list in your instructions.",
		Parameters: objectSchema([]string{"route", "label"}, map[string]any{
			"route": str("An in-app route such as /customers/123 or /invoices/overdue."),
			"label": str("The button's text, in the person's language, such as \"Open the customer\"."),
		}),
	},
}

// toolLabel is the short text the interface shows while a tool runs, in the
// person's language.
func toolLabel(lang, name, detail string) string {
	nb := lang == "nb"
	switch name {
	case toolSearchDocs:
		if nb {
			return "Søker i dokumentasjonen"
		}
		return "Searching the documentation"
	case toolSearchOperations:
		if nb {
			return "Finner det jeg kan lese"
		}
		return "Finding what I can read"
	case toolDescribe:
		if nb {
			return "Leser hvordan det fungerer"
		}
		return "Reading how it works"
	case toolCall:
		if detail != "" {
			if nb {
				return "Leser: " + detail
			}
			return "Reading: " + detail
		}
		if nb {
			return "Leser data"
		}
		return "Reading data"
	case toolNavigate:
		if nb {
			return "Lager en lenke"
		}
		return "Preparing a link"
	case toolShowTour:
		if nb {
			return "Forbereder en omvisning"
		}
		return "Preparing a tour"
	case toolProposeBulk:
		if nb {
			return "Forbereder en masseendring"
		}
		return "Preparing a bulk change"
	}
	return name
}

// outcome is what a tool answered: the content the model sees, how it went,
// and what the action log records of it.
type outcome struct {
	content    string // JSON, wrapped as data
	status     string // ok, refused or error
	httpStatus *int32 // the router's answer, for call_operation
	mode       string // the operation's mode, or "tool"
	operation  string // what the action log names
	link       *linkPart
	// tour is the steps of a guided tour the model offered: a "Show me" button.
	tour   []gen.AssistantTourStep
	detail string // the operation's summary, for the label
	// pending is a change the model proposed: the card to store. The run pauses.
	pending *pendingDraft
	// extra are calls the tool made besides its own, for the action log.
	extra []loggedCall
}

type linkPart struct{ route, label string }

func refused(code, message string) outcome {
	return outcome{content: agentloop.Wrap(map[string]string{"error": code, "message": message}), status: "refused"}
}

// result is the outcome as agentloop sees a tool's answer. The outcome itself
// travels with it, for the run's store to read what only this module knows: the
// link, the tour, the change proposed, the calls made besides.
func (o outcome) result() agentloop.Result {
	return agentloop.Result{Content: o.content, Status: o.status, HTTPStatus: o.httpStatus, Mode: o.mode, Operation: o.operation, Detail: o.detail, Attach: o}
}

// outcomeOf is the outcome behind a result: the module's own tools made theirs,
// and agentloop's tools answered a result with nothing more than its fields.
func outcomeOf(res agentloop.Result) outcome {
	if o, ok := res.Attach.(outcome); ok {
		return o
	}
	return outcome{content: res.Content, status: res.Status, httpStatus: res.HTTPStatus, mode: res.Mode, operation: res.Operation, detail: res.Detail}
}

// toolbox runs the tools for one run, as one person.
type toolbox struct {
	s        *server
	ctx      context.Context // carries the user and the assistant grant
	userID   uuid.UUID
	lang     string
	ops      *agentloop.Dispatch // search_operations, describe_operation and call_operation
	agent    *panelAgent         // the agent the conversation is with (V31), nil for the Assistant's own
	proposed bool                // a change was proposed in this run: one at a time
}

// newToolbox is the tools of one run of userID, who reads in lang. A run of a
// conversation with an agent is given the agent, which narrows the operations to
// those the agent may use too (V31).
func (s *server) newToolbox(ctx context.Context, userID uuid.UUID, lang string, agent *panelAgent) *toolbox {
	t := &toolbox{s: s, ctx: ctx, userID: userID, lang: lang, agent: agent}
	var ops agentloop.Operations = s.deps.Operations
	if agent != nil {
		ops = colleagueOps{inner: ops, agent: agent}
	}
	t.ops = &agentloop.Dispatch{Ops: ops, User: userID, Confirm: t.confirm}
	return t
}

// confirm is what a call to a change becomes: a card the person decides.
func (t *toolbox) confirm(_ context.Context, info operations.Info, req operations.CallRequest, explain string) agentloop.Result {
	return t.propose(info, req, explain).result()
}

// tools are what the model is offered, in the order of toolDefs: agentloop's
// three over the dispatcher, and this module's.
func (t *toolbox) tools() []agentloop.Tool {
	own := map[string]agentloop.Tool{}
	for _, tool := range t.ops.Tools() {
		own[tool.Name] = tool
	}
	tools := make([]agentloop.Tool, 0, len(toolDefs))
	for _, def := range toolDefs {
		if def.Name == toolProposeBulk && t.agent != nil {
			continue // a bulk change is the Assistant's own (V31)
		}
		if tool, ok := own[def.Name]; ok {
			tools = append(tools, tool)
			continue
		}
		name := def.Name
		tools = append(tools, agentloop.Tool{Name: name, Description: def.Description, Schema: def.Parameters,
			Call: func(_ context.Context, args json.RawMessage) (agentloop.Result, error) {
				return t.run(ai.ToolCall{Name: name, Arguments: args}).result(), nil
			}})
	}
	return tools
}

func (t *toolbox) run(call ai.ToolCall) outcome {
	switch call.Name {
	case toolSearchDocs:
		return t.searchDocs(call.Arguments)
	case toolNavigate:
		return t.navigate(call.Arguments)
	case toolShowTour:
		return t.showTour(call.Arguments)
	case toolProposeBulk:
		return t.proposeBulk(call.Arguments)
	}
	return refused("unknown_tool", fmt.Sprintf("there is no tool %q", call.Name))
}

func (t *toolbox) searchDocs(raw json.RawMessage) outcome {
	var args struct {
		Query string `json:"query"`
	}
	if err := agentloop.ToolArgs(raw, &args); err != nil || strings.TrimSpace(args.Query) == "" {
		return refused("invalid_arguments", "query is required")
	}
	results, err := searchDocs(args.Query, t.lang, 4)
	if err != nil {
		return outcome{content: agentloop.Wrap(map[string]string{"error": "docs_unavailable"}), status: "error"}
	}
	if results == nil {
		results = []DocResult{}
	}
	return outcome{content: agentloop.Wrap(map[string]any{"results": results}), status: "ok", mode: "tool", operation: "tool:" + toolSearchDocs}
}

func (t *toolbox) navigate(raw json.RawMessage) outcome {
	var args struct {
		Route string `json:"route"`
		Label string `json:"label"`
	}
	if err := agentloop.ToolArgs(raw, &args); err != nil {
		return refused("invalid_arguments", "route and label are required")
	}
	route := strings.TrimSpace(args.Route)
	label := strings.TrimSpace(args.Label)
	if label == "" || len([]rune(label)) > 100 {
		return refused("invalid_arguments", "label is required, at most 100 characters")
	}
	if !validRoute(route) {
		return refused("invalid_route", "that is not a route in Vantigo; use one from the list in your instructions")
	}
	return outcome{content: agentloop.Wrap(map[string]string{"shown": "a button \"" + label + "\" that opens " + route}), status: "ok", mode: "tool", operation: "tool:" + toolNavigate, link: &linkPart{route, label}}
}

// validRoute reports whether route is an in-app route: it begins with one of
// the host's known route prefixes at a segment boundary and is a plain path
// (with an optional query), never an address elsewhere. The prefixes are kept
// in appRoutes, beside the prompt that tells the model the shapes, rather than
// passed in by the host: the host's route set changes with a release of the
// whole binary, so one list in the server cannot drift from the page that
// carries it, and navigate stays checkable without a browser.
func validRoute(route string) bool {
	if route == "" || len(route) > 300 || route[0] != '/' || strings.HasPrefix(route, "//") || strings.ContainsAny(route, "\\\x00\r\n \t") {
		return false
	}
	if strings.Contains(route, "..") {
		return false
	}
	u, err := url.Parse(route)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return false
	}
	for _, r := range appRoutes {
		if u.Path == r.prefix || strings.HasPrefix(u.Path, r.prefix+"/") {
			return true
		}
	}
	return false
}
