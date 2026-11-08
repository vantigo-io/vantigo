package agents

// The agent's tools (virtual employees design V15) and the loop's seams. The
// model gets the three tools that reach the contract's operations, limited to
// the role's allowlist by the gate, and four of the module's own: reply, note,
// tag and hand_off. Every operation that names a conversation is pinned to the
// run's own, and the one that changes a conversation's fields is pinned to its
// status; the module's tools are the only way to write a message, a note, a tag
// or an assignee, and all of them go through the run's conversation alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

type aiMessage = ai.Message

// The module's own tools.
const (
	toolReply   = "reply"
	toolNote    = "note"
	toolTag     = "tag"
	toolHandOff = "hand_off"
)

// reservedOperations are the writes the module's own tools make: the model asks
// for them through the tools, which run the pipeline, and never through
// call_operation, which would send a reply the rules never saw.
var reservedOperations = []string{opReply, opNote, opTagPut, opTagDelete}

func (rs *runState) loop() *agentloop.Loop {
	rs.dispatch = &agentloop.Dispatch{Ops: rs.s.deps.Operations, User: rs.agent.UserID, Confirm: rs.confirm}
	base := rs.dispatch.Tools()
	tools := []agentloop.Tool{base[0], base[1], rs.callOperationTool(base[2]), rs.replyTool(), rs.noteTool(), rs.tagTool(), rs.handOffTool()}
	return &agentloop.Loop{
		AI:    rs.s.deps.AI,
		Task:  aiTaskFor(rs.agent.ID),
		Tools: tools,
		Caps: agentloop.Caps{
			ToolCalls: int(rs.role.CapsToolCalls), Turns: int(rs.role.CapsTurns),
			ContextTokens: rs.s.deps.Config.AIContextTokens, AnswerTokens: answerTokens,
		},
		Store: runStore{rs},
	}
}

// aiTaskFor names the agent's model: agents.run for this agent (V7).
func aiTaskFor(agentID uuid.UUID) string { return ai.TaskFor(aiTaskRun, agentID.String()) }

func schema(required []string, props map[string]any) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	raw, _ := json.Marshal(s)
	return raw
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

var (
	sourcesProp = map[string]any{"type": "array", "items": map[string]any{"type": "string"},
		"description": "What you read in this run that supports the answer: an operation and its ids, such as \"getInvoicesById 2026-0931\", or a Knowledge article id. A source you did not read in this run, or the conversation itself, counts for nothing."}
	scoreProp = map[string]any{"type": "integer", "minimum": 0, "maximum": 100,
		"description": "How sure you are, 0 to 100, that the answer is correct and complete, judged from the sources."}
)

// callOperationTool wraps agentloop's call_operation with the pins, and asks for
// the evidence a change carries (V17).
func (rs *runState) callOperationTool(base agentloop.Tool) agentloop.Tool {
	def := base
	def.Schema = schema([]string{"id"}, map[string]any{
		"id":      text("The operation id from search_operations."),
		"params":  map[string]any{"type": "object", "description": "Parameters by name: path parameters, query parameters and header parameters.", "additionalProperties": true},
		"body":    map[string]any{"type": "object", "description": "The JSON request body, when the operation takes one.", "additionalProperties": true},
		"explain": text("For a change: one sentence saying what it will do, for the reviewer."),
		"sources": sourcesProp,
		"score":   scoreProp,
	})
	def.Description = "Call an operation as the agent. Pass path, query and header parameters together in params, by name, and a JSON body in body when the operation takes one. An operation that reads data runs at once. An operation that changes something never runs on your say-so: a person reviews it first; give sources and score, as for reply. Operations on this conversation are about this conversation only."
	def.Call = func(ctx context.Context, raw json.RawMessage) (agentloop.Result, error) {
		var a struct {
			ID      string          `json:"id"`
			Params  map[string]any  `json:"params"`
			Body    json.RawMessage `json:"body"`
			Explain string          `json:"explain"`
			Sources []string        `json:"sources"`
			Score   int             `json:"score"`
		}
		if err := agentloop.ToolArgs(raw, &a); err != nil || a.ID == "" {
			return agentloop.Refused("invalid_arguments", "id is required"), nil
		}
		if slices.Contains(reservedOperations, a.ID) {
			return agentloop.Refused("use_the_tool", "that operation is made through the reply, note and tag tools, not call_operation"), nil
		}
		if refusal := rs.pin(a.ID, a.Params, a.Body); refusal != nil {
			return *refusal, nil
		}
		if a.Params == nil {
			a.Params = map[string]any{}
		}
		if op, ok := rs.allowed(a.ID); ok && op.OwnConversation {
			a.Params["id"] = rs.run.ConversationID.String()
		}
		args := map[string]any{"id": a.ID, "params": a.Params, "explain": a.Explain}
		if len(a.Body) > 0 && string(a.Body) != "null" {
			args["body"] = a.Body
		}
		pinned, _ := json.Marshal(args)
		rs.mu.Lock()
		rs.pending = &changeArgs{Sources: a.Sources, Score: a.Score}
		rs.mu.Unlock()
		res, err := base.Call(ctx, pinned)
		rs.mu.Lock()
		rs.pending = nil
		rs.mu.Unlock()
		return res, err
	}
	return def
}

// allowed is the role's template's entry for an operation.
func (rs *runState) allowed(id string) (allowedOperation, bool) {
	for _, a := range rs.tpl.Allowlist {
		if a.ID == id {
			return a, true
		}
	}
	return allowedOperation{}, false
}

// pin refuses a call that reaches beyond the run's own conversation, or beyond
// the fields an agent may change on it: an operation that names a conversation
// is for this run's alone, and patchCommunicationsConversationsById takes the
// status and nothing else (the customer link and the assignee are not the
// model's to set; the assignee is hand_off's).
func (rs *runState) pin(id string, params map[string]any, body json.RawMessage) *agentloop.Result {
	if op, ok := rs.allowed(id); ok && op.OwnConversation {
		if given, present := params["id"]; present {
			if s, ok := given.(string); !ok || !strings.EqualFold(s, rs.run.ConversationID.String()) {
				r := agentloop.Refused("own_conversation_only", "this operation is for the conversation you are answering, and no other")
				r.Operation = id
				return &r
			}
		}
	}
	if id == opPatch {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil || len(fields) == 0 {
			r := agentloop.Refused("status_only", "send a body with the conversation's status")
			r.Operation = id
			return &r
		}
		for name := range fields {
			if name != "status" {
				r := agentloop.Refused("status_only", "only the conversation's status may be changed here; hand_off hands the conversation to a colleague")
				r.Operation = id
				return &r
			}
		}
	}
	return nil
}

// confirm is what a call to a change becomes in this delivery: always a review
// item, never a call (V19). The operation is checked against the contract first,
// so a reviewer is not asked to approve what cannot run.
func (rs *runState) confirm(ctx context.Context, info operations.Info, req operations.CallRequest, explain string) agentloop.Result {
	if err := rs.s.deps.Operations.Validate(rs.withGrant(ctx), req); err != nil {
		r := agentloop.Refused("invalid_request", err.Error())
		r.Operation, r.Mode = info.ID, string(operations.ModeConfirm)
		return r
	}
	rs.mu.Lock()
	ev := rs.pending
	rs.mu.Unlock()
	var sources []string
	var modelScore int
	if ev != nil {
		sources, modelScore = ev.Sources, ev.Score
	}
	valid, _ := validSources(sources, rs.actionLog(ctx))
	item, _ := json.Marshal(map[string]any{
		"operationId": info.ID, "summary": info.Summary, "explain": explain,
		"pathParams": req.PathParams, "query": req.Query, "body": req.Body,
	})
	id, err := rs.insertItem(ctx, "change", item, scoreOf(modelScore, valid), valid, modelScore, []string{ruleChange}, "waiting", rs.expiry(false))
	if err != nil {
		r := agentloop.Result{Content: agentloop.Wrap(map[string]string{"error": "call_failed", "message": "the change could not be queued"}), Status: "error"}
		r.Operation, r.Mode = info.ID, string(operations.ModeConfirm)
		return r
	}
	rs.mu.Lock()
	rs.items++
	rs.mu.Unlock()
	rs.reviewNote(ctx, id, "a change")
	return agentloop.Result{
		Content:   agentloop.Wrap(map[string]string{"status": "queued_for_review", "message": "this change is waiting for a person to review it; it has not been made"}),
		Status:    "ok",
		Mode:      string(operations.ModeConfirm),
		Operation: info.ID,
		Detail:    info.Summary,
	}
}

// expiry is when a review item lapses: a day, and for a reply on a channel with a
// window, 15 minutes before the window closes (V20).
func (rs *runState) expiry(reply bool) time.Time {
	at := rs.s.deps.Clock().Add(24 * time.Hour)
	if reply && rs.conv.Reply != nil && rs.conv.Reply.ClosesAt != nil {
		if closes := rs.conv.Reply.ClosesAt.Add(-15 * time.Minute); closes.Before(at) {
			at = closes
		}
	}
	return at
}

// insertItem stores a review item of the run.
func (rs *runState) insertItem(ctx context.Context, kind string, item json.RawMessage, score int, sources []string, modelScore int, heldBy []string, state string, expires time.Time) (uuid.UUID, error) {
	if sources == nil {
		sources = []string{}
	}
	src, _ := json.Marshal(sources)
	id := uuid.New()
	now := rs.s.deps.Clock()
	p := store.InsertReviewItemParams{
		ID: id, RunID: rs.run.ID, AgentID: rs.agent.ID, RoleID: rs.role.ID, ConversationID: rs.run.ConversationID, CustomerID: rs.customerID,
		Kind: kind, Item: item, Score: int16(score), Sources: src, ModelScore: int16(min(max(modelScore, 0), 100)),
		HeldBy: heldBy, State: state, ExpiresAt: expires, Now: now,
	}
	if state != "waiting" {
		p.DecidedAt = &now
	}
	if err := store.New(rs.s.deps.Pool).InsertReviewItem(context.WithoutCancel(ctx), p); err != nil {
		rs.s.warn("agents: store a review item", "run", rs.run.ID, "err", err)
		return uuid.Nil, err
	}
	if state == "waiting" {
		_ = store.New(rs.s.deps.Pool).AddUsage(context.WithoutCancel(ctx), store.AddUsageParams{AgentID: rs.agent.ID, Day: rs.s.day(now), Held: 1})
	}
	return id, nil
}

// reviewNote tells the people on the conversation that something waits for them
// (V20). It imports nothing of Communications: it is a note, with a link.
func (rs *runState) reviewNote(ctx context.Context, itemID uuid.UUID, what string) {
	body, _ := json.Marshal(map[string]string{"textBody": fmt.Sprintf("%s has %s waiting for review: /agents/review/%s", rs.agent.Name, what, itemID)})
	if res, err := rs.call(ctx, operations.CallRequest{OperationID: opNote, PathParams: rs.own(), Body: body}); err != nil || res.Status >= 300 {
		rs.s.warn("agents: the review note was not written", "run", rs.run.ID, "status", res.Status, "err", err)
	}
}

// noteTool writes an internal note on the run's conversation.
func (rs *runState) noteTool() agentloop.Tool {
	return agentloop.Tool{
		Name:        toolNote,
		Description: "Write an internal note on this conversation, for the colleagues. The customer does not see it.",
		Schema:      schema([]string{"text"}, map[string]any{"text": text("The note.")}),
		Call: func(ctx context.Context, raw json.RawMessage) (agentloop.Result, error) {
			var a struct {
				Text string `json:"text"`
			}
			if err := agentloop.ToolArgs(raw, &a); err != nil || strings.TrimSpace(a.Text) == "" {
				return agentloop.Refused("invalid_arguments", "text is required"), nil
			}
			body, _ := json.Marshal(map[string]string{"textBody": a.Text})
			return rs.ownWrite(ctx, toolNote, operations.CallRequest{OperationID: opNote, PathParams: rs.own(), Body: body}), nil
		},
	}
}

// tagTool tags the run's conversation.
func (rs *runState) tagTool() agentloop.Tool {
	return agentloop.Tool{
		Name:        toolTag,
		Description: "Tag this conversation with one of the company's tags (find them with search_operations and getCommunicationsTags).",
		Schema:      schema([]string{"tagId"}, map[string]any{"tagId": text("The tag's id.")}),
		Call: func(ctx context.Context, raw json.RawMessage) (agentloop.Result, error) {
			var a struct {
				TagID string `json:"tagId"`
			}
			if err := agentloop.ToolArgs(raw, &a); err != nil {
				return agentloop.Refused("invalid_arguments", err.Error()), nil
			}
			if _, err := uuid.Parse(a.TagID); err != nil {
				return agentloop.Refused("invalid_arguments", "tagId must be a tag id"), nil
			}
			params := rs.own()
			params["tagId"] = a.TagID
			return rs.ownWrite(ctx, toolTag, operations.CallRequest{OperationID: opTagPut, PathParams: params}), nil
		},
	}
}

// ownWrite makes one of the module's own writes on the run's conversation and
// answers what the model sees.
func (rs *runState) ownWrite(ctx context.Context, tool string, req operations.CallRequest) agentloop.Result {
	res, err := rs.call(ctx, req)
	out := agentloop.Result{Mode: "tool", Operation: "tool:" + tool}
	switch {
	case err != nil:
		var oe *operations.Error
		if asOpError(err, &oe) {
			return agentloop.Refused(oe.Code, oe.Message)
		}
		out.Content, out.Status = agentloop.Wrap(map[string]string{"error": "call_failed"}), "error"
	case res.Status >= 300:
		status := int32(res.Status)
		out.HTTPStatus = &status
		out.Content, out.Status = agentloop.Wrap(map[string]any{"error": "refused", "status": res.Status}), "error"
	default:
		status := int32(res.Status)
		out.HTTPStatus = &status
		out.Content, out.Status = agentloop.Wrap(map[string]string{"status": "done"}), "ok"
	}
	return out
}

// handOffTool hands the conversation to a colleague and ends the run.
func (rs *runState) handOffTool() agentloop.Tool {
	return agentloop.Tool{
		Name:        toolHandOff,
		Description: "Hand this conversation to a colleague, with a summary of what the customer wants and what you found. Use it when you do not know, may not decide, or the customer asks for a person. It ends your work on the conversation.",
		Schema: schema([]string{"summary", "reason"}, map[string]any{
			"summary": text("What the customer wants and what you found, for the colleague."),
			"reason":  text("Why you hand off, in a few words."),
		}),
		Call: func(ctx context.Context, raw json.RawMessage) (agentloop.Result, error) {
			var a struct {
				Summary string `json:"summary"`
				Reason  string `json:"reason"`
			}
			if err := agentloop.ToolArgs(raw, &a); err != nil || strings.TrimSpace(a.Summary) == "" {
				return agentloop.Refused("invalid_arguments", "summary is required"), nil
			}
			summary := strings.TrimSpace(a.Summary)
			if r := strings.TrimSpace(a.Reason); r != "" {
				summary += " (Reason: " + r + ")"
			}
			rs.mu.Lock()
			rs.handOff = &handOffRequest{code: handAgent, summary: summary}
			rs.mu.Unlock()
			rs.doHandOff(ctx, handAgent, summary)
			return agentloop.Result{Content: agentloop.Wrap(map[string]string{"status": "handed_off"}), Status: "ok", Mode: "tool", Operation: "tool:" + toolHandOff}, nil
		},
	}
}

// refRef is the reference an action log names a call by: the operation and the
// values of the parameters it named, in the order of their names.
func refOf(operation string, params map[string]any) string {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := []string{operation}
	for _, n := range names {
		switch v := params[n].(type) {
		case string:
			parts = append(parts, v)
		case float64, bool, int, int32, int64:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return strings.Join(parts, " ")
}

// runStore keeps a run's transcript and progress (agentloop.Store).
type runStore struct{ rs *runState }

func (st runStore) Stopped(ctx context.Context) (agentloop.Outcome, bool) {
	rs := st.rs
	rs.mu.Lock()
	handedOff := rs.handOff != nil
	rs.mu.Unlock()
	if handedOff {
		return agentloop.Outcome{State: agentloop.StateDone}, true
	}
	if ctx.Err() != nil {
		return agentloop.Outcome{State: agentloop.StateFailed, Code: "timeout"}, true
	}
	return agentloop.Outcome{}, false
}

func (st runStore) Budget(ctx context.Context) (agentloop.Outcome, bool) {
	if over, _ := st.rs.s.overBudget(ctx, st.rs.agent); over {
		return agentloop.Outcome{State: agentloop.StateFailed, Code: "budget"}, true
	}
	return agentloop.Outcome{}, false
}

func (st runStore) Usage(ctx context.Context, resp ai.ChatResponse, toolCalls, turns int) {
	rs := st.rs
	var in, out int64
	if resp.InputTokens != nil {
		in = int64(*resp.InputTokens)
	}
	if resp.OutputTokens != nil {
		out = int64(*resp.OutputTokens)
	}
	rs.mu.Lock()
	rs.inTokens += in
	rs.outTokens += out
	rs.toolCalls, rs.modelTurns = toolCalls, turns
	rs.mu.Unlock()
	rs.s.countTokens(ctx, rs.agent.ID, in, out)
}

func (st runStore) AppendAnswer(_ context.Context, resp ai.ChatResponse, calls []ai.ToolCall) (any, error) {
	rs := st.rs
	rs.mu.Lock()
	rs.lastText = resp.Message.Text
	rs.mu.Unlock()
	var raw json.RawMessage
	if len(calls) > 0 {
		raw, _ = json.Marshal(calls)
	}
	rs.transcript("assistant", resp.Message.Text, raw, "")
	return nil, nil
}

// AppendResult logs the call (V25: no body) and keeps its result in the
// transcript.
func (st runStore) AppendResult(ctx context.Context, call ai.ToolCall, res agentloop.Result, elapsed time.Duration) agentloop.Recorded {
	rs := st.rs
	operation, mode := res.Operation, res.Mode
	if operation == "" {
		operation = "tool:" + call.Name
	}
	if mode == "" {
		mode = "tool"
	}
	ref, scoped := operation, false
	if call.Name == agentloop.ToolCall {
		var a struct {
			ID     string         `json:"id"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(call.Arguments, &a) == nil {
			ref = refOf(a.ID, a.Params)
			if op, ok := rs.allowed(a.ID); ok {
				scoped = op.CustomerData
			}
		}
	}
	if call.Name == toolReply || call.Name == toolHandOff || call.Name == toolNote || call.Name == toolTag {
		ref = operation
	}
	p := store.InsertActionParams{
		RunID: rs.run.ID, AgentID: rs.agent.ID, CustomerID: rs.customerID, OperationID: operation, Ref: ref, Scoped: scoped,
		Mode: mode, Status: res.Status, HttpStatus: res.HTTPStatus, DurationMs: int32(elapsed.Milliseconds()), Now: rs.s.deps.Clock(),
	}
	if err := store.New(rs.s.deps.Pool).InsertAction(ctx, p); err != nil {
		rs.s.warn("agents: log an action", "run", rs.run.ID, "err", err)
	}
	rs.transcript("tool", res.Content, nil, call.ID)
	return agentloop.Recorded{Result: res}
}

func (st runStore) Attach(context.Context, any, []any) {}
