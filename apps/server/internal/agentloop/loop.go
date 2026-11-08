// Package agentloop is the model loop a module runs a conversation with: the
// turns, the tool calls, the caps, the final turn without tools, the Anthropic
// branch that keeps the history append-only, the compaction of what the model
// is sent, the wrapping of what a tool answers as data, and the three tools
// that reach the contract's operations through the dispatcher.
//
// It is a platform package (module boundaries, platform packages): it imports
// no business module and knows no table. What differs from one module to
// another is behind four seams the caller fills:
//
//   - Store, where the transcript and the run's progress are kept;
//   - Emit, which hears the deltas and the tool events;
//   - Tool, whose Call is how a tool runs;
//   - Dispatch.Confirm, which decides what a change to a record becomes.
//
// The Assistant (srv/assistant) is the first caller; its behaviour is exactly
// what it was when this loop lived in it.
package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/ai"
)

// AI is the part of the platform's AI client the loop uses; *ai.Client is it.
type AI interface {
	// Provider names the provider; Anthropic's history is kept append-only.
	Provider() string
	Chat(ctx context.Context, task string, req ai.ChatRequest, onDelta func(text string)) (ai.ChatResponse, error)
}

// How a run ended, as Outcome.State. A module that keeps a run's state maps
// these to its own words.
const (
	StateDone    = "done"
	StateStopped = "stopped"
	StateFailed  = "failed"
	// StatePaused is a run that waits for a decision on a change a tool
	// proposed: the turn ends, and the module resumes the run when it has one.
	StatePaused = "paused"
)

// The codes Outcome.Code carries for a failed model call.
const (
	CodeAIFailed  = "ai_failed"
	CodeAIRefused = "ai_refused"
)

// The states Emit.Tool hears a tool call in.
const (
	ToolStarted = "started"
	ToolDone    = "done"
)

// Caps bound a run (the Assistant design S14): at most ToolCalls tool calls and
// Turns model turns without a new message from the person. Over a cap the model
// is asked for a final answer without tools.
type Caps struct {
	ToolCalls int
	Turns     int
	// Ceiling is how long Run may take; zero for no limit of its own, for a
	// caller that already bounds the context it passes.
	Ceiling time.Duration
	// ContextTokens is the budget a request is fitted to for a provider whose
	// history may be shortened in place.
	ContextTokens int
	// AnswerTokens bounds one model answer.
	AnswerTokens int
}

// Result is what a tool answered: the content the model sees, how it went, and
// what a module's action log records of it.
type Result struct {
	Content    string // JSON, wrapped as data
	Status     string // ok, refused or error
	HTTPStatus *int32 // the router's answer, for call_operation
	Mode       string // the operation's mode, or "tool"
	Operation  string // what the action log names
	Detail     string // the operation's summary, for a label
	// Attach is whatever else the module's own tools carry back to its Store: a
	// link, a proposed change. The loop does not look at it.
	Attach any
}

// Tool is one tool the model is offered.
type Tool struct {
	Name        string
	Description string
	// Schema is the JSON Schema of the tool's arguments.
	Schema json.RawMessage
	// Call runs the tool. An error is a tool that failed unexpectedly; a tool
	// that refuses answers a Result.
	Call func(ctx context.Context, args json.RawMessage) (Result, error)
}

// Recorded is what the Store made of a tool's Result.
type Recorded struct {
	// Result is the answer the model sees, which the Store may have replaced.
	Result Result
	// Pause ends the turn after this round of tool calls: a change waits for a
	// decision.
	Pause bool
	// Note is what the Store attaches to the model's answer afterwards (a link,
	// a card); nil for nothing.
	Note any
	// Value is the Store's own, for Emit.Tool.
	Value any
}

// Store is where a run's transcript and progress are kept. A module implements
// it over its own tables. Except for Stopped, every method is given a context
// that outlives a stop, so what was already said is not lost.
type Store interface {
	// Stopped reports why the run must end now, as an Outcome.
	Stopped(ctx context.Context) (Outcome, bool)
	// Budget reports that the run may not take another model turn.
	Budget(ctx context.Context) (Outcome, bool)
	// Usage counts a model turn's tokens and the run's progress; toolCalls is
	// what the run had made before this turn's.
	Usage(ctx context.Context, resp ai.ChatResponse, toolCalls, turns int)
	// AppendAnswer stores the model's answer, the tool calls it asked for among
	// them, and answers a handle on it that Attach is given back.
	AppendAnswer(ctx context.Context, resp ai.ChatResponse, calls []ai.ToolCall) (any, error)
	// AppendResult stores a tool's result and acts on it (a proposed change is
	// stored, the call is logged).
	AppendResult(ctx context.Context, call ai.ToolCall, res Result, elapsed time.Duration) Recorded
	// Attach adds the notes the tools left to the answer.
	Attach(ctx context.Context, answer any, notes []any)
}

// Emit hears what a run says as it goes. A module without a stream passes nil.
type Emit interface {
	Delta(text string)
	// Tool hears a call start (rec nil) and finish.
	Tool(call ai.ToolCall, state string, rec *Recorded)
}

type silent struct{}

func (silent) Delta(string)                        {}
func (silent) Tool(ai.ToolCall, string, *Recorded) {}

// Loop runs a conversation's model turns.
type Loop struct {
	AI AI
	// Task is the AI task key the model is chosen by.
	Task  string
	Tools []Tool
	Caps  Caps
	Store Store
	Emit  Emit
}

// Input is what a run starts from.
type Input struct {
	// System is the system prompt, sent unchanged every turn.
	System string
	// History is the model's transcript so far.
	History []ai.Message
	// Turns are the model turns the run has already made; one short of the cap
	// has the run answer once, without tools.
	Turns int
}

// Outcome is how a run ended.
type Outcome struct {
	State string
	// Code says why a run failed.
	Code string
}

// Run takes model turns until the model answers without asking for a tool, a
// cap is reached, the Store says stop, or a tool proposes a change. A non-nil
// error accompanies an Outcome that failed for a reason the caller should log.
func (l *Loop) Run(ctx context.Context, in Input) (Outcome, error) {
	if l.Caps.Ceiling > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, l.Caps.Ceiling, errors.New("agentloop: the run reached its ceiling"))
		defer cancel()
	}
	persist := context.WithoutCancel(ctx)
	emit := l.Emit
	if emit == nil {
		emit = silent{}
	}
	preserving := l.AI.Provider() == ai.ProviderAnthropic
	defs := make([]ai.Tool, len(l.Tools))
	for i, t := range l.Tools {
		defs[i] = ai.Tool{Name: t.Name, Description: t.Description, Parameters: t.Schema}
	}
	history, turns, toolCalls := in.History, in.Turns, 0

	for {
		if out, stop := l.Store.Stopped(ctx); stop {
			return out, nil
		}
		// The Owner's daily token budget (S14), judged before every model turn.
		if out, over := l.Store.Budget(persist); over {
			return out, nil
		}
		turns++
		final := turns >= l.Caps.Turns || toolCalls >= l.Caps.ToolCalls
		req := ai.ChatRequest{System: in.System, Messages: history, Tools: defs, ToolsDisabled: final, MaxTokens: l.Caps.AnswerTokens}
		if final {
			// The last turn is answered without tools. A provider that does not
			// preserve thinking is simply sent none; Anthropic keeps the tool set
			// the conversation began with and is told not to use it.
			if !preserving {
				req.Tools = nil
			}
		}
		if !preserving {
			// S7: older tool results are shortened in place, for the providers
			// whose history carries no signed thinking.
			req.Messages = FitContext(in.System, req.Tools, history, l.Caps.ContextTokens)
		}
		resp, err := l.AI.Chat(ctx, l.Task, req, emit.Delta)
		if err != nil {
			if out, stop := l.Store.Stopped(ctx); stop {
				return out, nil
			}
			code := CodeAIFailed
			if _, refused := ai.RefusalCategory(err); refused {
				code = CodeAIRefused
			}
			return Outcome{State: StateFailed, Code: code}, fmt.Errorf("the model call failed: %w", err)
		}
		l.Store.Usage(persist, resp, toolCalls, turns)

		calls := resp.Message.ToolCalls
		if final {
			calls = nil // no tools were offered; whatever a stubborn model asks is ignored
		}
		answer, err := l.Store.AppendAnswer(persist, resp, calls)
		if err != nil {
			return Outcome{State: StateFailed, Code: "internal"}, fmt.Errorf("save the answer: %w", err)
		}
		history = append(history, ai.Message{Role: ai.RoleAssistant, Text: resp.Message.Text, ToolCalls: calls, Provider: ProviderBlocks(resp, calls)})
		if len(calls) == 0 {
			return Outcome{State: StateDone}, nil
		}

		var notes []any
		paused := false
		for _, call := range calls {
			toolCalls++
			emit.Tool(call, ToolStarted, nil)
			var res Result
			start := time.Now()
			if toolCalls > l.Caps.ToolCalls {
				res = Refused("tool_limit", "the limit of tool calls for this answer is reached; answer with what you have")
			} else {
				res = l.call(ctx, call)
			}
			rec := l.Store.AppendResult(persist, call, res, time.Since(start))
			emit.Tool(call, ToolDone, &rec)
			history = append(history, ai.Message{Role: ai.RoleTool, ToolCallID: call.ID, Text: rec.Result.Content})
			if rec.Note != nil {
				notes = append(notes, rec.Note)
			}
			paused = paused || rec.Pause
		}
		if len(notes) > 0 {
			l.Store.Attach(persist, answer, notes)
		}
		if paused {
			// A change waits for the person: the turn, the stream and the grant end.
			return Outcome{State: StatePaused}, nil
		}
	}
}

// call runs one tool call.
func (l *Loop) call(ctx context.Context, call ai.ToolCall) Result {
	for _, t := range l.Tools {
		if t.Name != call.Name {
			continue
		}
		res, err := t.Call(ctx, call.Arguments)
		if err != nil {
			return Result{Content: Wrap(map[string]string{"error": "call_failed", "message": "the tool failed unexpectedly"}), Status: "error"}
		}
		return res
	}
	return Refused("unknown_tool", fmt.Sprintf("there is no tool %q", call.Name))
}

// ProviderBlocks is the provider's own record of an answer, kept with the turn
// to be replayed unchanged. A turn whose tool calls were not all kept (a final
// turn that was offered none) has none: it would not match its results.
func ProviderBlocks(resp ai.ChatResponse, calls []ai.ToolCall) json.RawMessage {
	if len(calls) != len(resp.Message.ToolCalls) {
		return nil
	}
	return resp.Message.Provider
}
