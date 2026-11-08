package assistant

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// The model loop of a run is agentloop's. This file is the Assistant's side of
// its seams: where a run's messages and actions are stored (runStore) and what
// the person's stream hears (runEmit).

// loop is the model loop of this run. The run's context already carries the
// five-minute ceiling, so the loop is given none of its own.
func (r *runner) loop(contextTokens int) *agentloop.Loop {
	return &agentloop.Loop{
		AI:    r.s.deps.AI,
		Task:  aiTaskChat,
		Tools: r.tools.tools(),
		Caps:  agentloop.Caps{ToolCalls: maxToolCalls, Turns: maxTurns, ContextTokens: contextTokens, AnswerTokens: answerTokens},
		Store: runStore{r},
		Emit:  runEmit{r},
	}
}

// runStore keeps a run's transcript in the Assistant's tables.
type runStore struct{ r *runner }

func (s runStore) Stopped(context.Context) (agentloop.Outcome, bool) {
	cause := s.r.stopped()
	if cause == "" {
		return agentloop.Outcome{}, false
	}
	out := agentloop.Outcome{State: cause}
	if cause == "failed" {
		out.Code = "timeout"
	}
	return out, true
}

func (s runStore) Budget(context.Context) (agentloop.Outcome, bool) {
	r := s.r
	if over, err := r.s.overBudget(r.persist(), r.userID); err == nil && over {
		return agentloop.Outcome{State: "failed", Code: codeBudgetExhausted}, true
	}
	return agentloop.Outcome{}, false
}

func (s runStore) Usage(_ context.Context, resp ai.ChatResponse, toolCalls, turns int) {
	r := s.r
	r.s.countTokens(r.persist(), r.userID, resp.InputTokens, resp.OutputTokens)
	_ = store.New(r.s.deps.Pool).CountRunProgress(r.persist(), store.CountRunProgressParams{ID: r.runID, ToolCalls: int32(toolCalls), ModelTurns: int32(turns)})
}

func (s runStore) AppendAnswer(_ context.Context, resp ai.ChatResponse, calls []ai.ToolCall) (any, error) {
	return s.r.saveAssistant(resp, calls)
}

// AppendResult writes the action and the tool message of one tool call: it
// takes the module's outcome out of the result, stores the change a tool
// proposed, and answers the content the model sees and the part a navigate
// produced (a link) or a proposed change (its card).
func (s runStore) AppendResult(_ context.Context, call ai.ToolCall, res agentloop.Result, elapsed time.Duration) agentloop.Recorded {
	r := s.r
	out := outcomeOf(res)
	if out.operation == "" {
		out.operation = "tool:" + call.Name
	}
	if out.mode == "" {
		out.mode = "tool"
	}

	// A proposed change is stored before its tool result is, with the result's
	// id, so the decision can replace the waiting result with the real one.
	toolMessageID := uuid.New()
	var proposed *store.AssistantPendingAction
	if out.pending != nil {
		row, err := r.createPending(out.pending, call.ID, toolMessageID)
		if err != nil {
			r.s.deps.Logger.Error("assistant: store a pending action", "run", r.runID, "error", err)
			out = outcome{content: agentloop.Wrap(map[string]string{"error": "call_failed", "message": "the change could not be prepared"}), status: "error", operation: out.operation, mode: out.mode, detail: out.detail}
		} else {
			proposed = &row
			r.proposed = &row
		}
	}

	q := store.New(r.s.deps.Pool)
	for _, extra := range out.extra {
		st := extra.status
		if err := q.InsertAction(r.persist(), store.InsertActionParams{
			ID: uuid.New(), RunID: r.runID, OperationID: extra.operation, Mode: extra.mode, HttpStatus: &st,
			DurationMs: int32(extra.elapsed.Milliseconds()), Now: r.s.deps.Clock(),
		}); err != nil {
			r.s.deps.Logger.Error("assistant: log an action", "run", r.runID, "error", err)
		}
	}
	if err := q.InsertAction(r.persist(), store.InsertActionParams{
		ID: uuid.New(), RunID: r.runID, OperationID: out.operation, Mode: out.mode, HttpStatus: out.httpStatus,
		DurationMs: int32(elapsed.Milliseconds()), Now: r.s.deps.Clock(),
	}); err != nil {
		r.s.deps.Logger.Error("assistant: log an action", "run", r.runID, "error", err)
	}

	callID, name, status, content := call.ID, call.Name, gen.AssistantPartStatus(out.status), out.content
	part := gen.AssistantPart{Type: gen.AssistantPartType("tool_result"), CallId: &callID, Name: &name, Status: &status, Content: &content}
	msg, err := q.InsertMessage(r.persist(), store.InsertMessageParams{
		ID: toolMessageID, ConversationID: r.convID, RunID: &r.runID, Role: "tool", Content: marshalParts([]gen.AssistantPart{part}), Now: r.s.deps.Clock(),
	})
	if err != nil {
		r.s.deps.Logger.Error("assistant: save a tool result", "run", r.runID, "error", err)
	} else {
		r.queue.emit(eventMessage, messageView(msg))
	}

	rec := agentloop.Recorded{Result: out.result(), Pause: proposed != nil}
	var link *gen.AssistantPart
	switch {
	case proposed != nil:
		id := proposed.ID
		link = &gen.AssistantPart{Type: gen.AssistantPartType("confirmation"), ActionId: &id}
		rec.Value = proposed
	case out.link != nil:
		route, linkLabel := out.link.route, out.link.label
		link = &gen.AssistantPart{Type: gen.AssistantPartType("link"), Route: &route, Label: &linkLabel}
	case len(out.tour) > 0:
		steps := out.tour
		link = &gen.AssistantPart{Type: gen.AssistantPartType("tour"), Steps: &steps}
	}
	if link != nil {
		rec.Note = *link
	}
	return rec
}

func (s runStore) Attach(_ context.Context, answer any, notes []any) {
	parts := make([]gen.AssistantPart, len(notes))
	for i, n := range notes {
		parts[i] = n.(gen.AssistantPart)
	}
	s.r.addLinks(answer.(store.AssistantMessage), parts)
}

// runEmit is the run's SSE stream.
type runEmit struct{ r *runner }

func (e runEmit) Delta(text string) {
	e.r.queue.emit(eventDelta, map[string]string{"text": text})
}

func (e runEmit) Tool(call ai.ToolCall, state string, rec *agentloop.Recorded) {
	detail := ""
	if rec != nil {
		detail = rec.Result.Detail
	}
	e.r.queue.emit(eventTool, map[string]any{"callId": call.ID, "name": call.Name, "label": toolLabel(e.r.lang, call.Name, detail), "state": state})
	if rec != nil {
		if proposed, ok := rec.Value.(*store.AssistantPendingAction); ok {
			e.r.queue.emit(eventConfirmation, e.r.s.cardOf(e.r.persist(), *proposed))
		}
	}
}
