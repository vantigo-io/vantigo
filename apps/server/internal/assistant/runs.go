package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// The per-run caps (the Assistant design S14): a run makes at most this many
// tool calls and model turns without a new message from the person. Over a
// cap the model is asked for a final answer without tools.
const (
	maxToolCalls = 12
	maxTurns     = 4

	// runCeiling is how long a run may live, whatever happens to the client.
	runCeiling = 5 * time.Minute
	// grantTTL is the assistant grant's life: a run's ceiling is shorter, so a
	// run never outlives its grant.
	grantTTL = 15 * time.Minute
	// answerTokens bounds one model answer.
	answerTokens = 2048

	maxMessageChars = 8000
)

// Why a run's context ended, as the cause of its cancellation.
var (
	errRunStopped       = errors.New("assistant: the person stopped the run")
	errConversationGone = errors.New("assistant: the conversation was deleted")
)

// runRegistry knows the runs this process is executing, so a stop or a delete
// reaches the goroutine at once. (A stop on another replica reaches it through
// the run's stop_requested, which the run reads between steps.)
type runRegistry struct {
	mu   sync.Mutex
	runs map[uuid.UUID]runHandle
}

type runHandle struct {
	conversation uuid.UUID
	cancel       context.CancelCauseFunc
}

func (r *runRegistry) add(run, conversation uuid.UUID, cancel context.CancelCauseFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[uuid.UUID]runHandle{}
	}
	r.runs[run] = runHandle{conversation, cancel}
}

func (r *runRegistry) remove(run uuid.UUID) {
	r.mu.Lock()
	delete(r.runs, run)
	r.mu.Unlock()
}

func (r *runRegistry) cancelRun(run uuid.UUID) {
	r.mu.Lock()
	h, ok := r.runs[run]
	r.mu.Unlock()
	if ok {
		h.cancel(errRunStopped)
	}
}

func (r *runRegistry) cancelConversation(conversation uuid.UUID) {
	r.mu.Lock()
	var cancels []context.CancelCauseFunc
	for _, h := range r.runs {
		if h.conversation == conversation {
			cancels = append(cancels, h.cancel)
		}
	}
	r.mu.Unlock()
	for _, c := range cancels {
		c(errConversationGone)
	}
}

// textPart, toolCallPart and so on build the parts a message is stored as.
func textPart(text string) gen.AssistantPart {
	return gen.AssistantPart{Type: gen.AssistantPartType("text"), Text: &text}
}

func marshalParts(parts []gen.AssistantPart) []byte {
	raw, err := json.Marshal(parts)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

// SendAssistantMessage (POST /api/v1/assistant/conversations/{id}/messages)
//
// It persists the person's message and starts the run, then answers the stream
// of the run's events. The run is detached from the request: it runs on a
// context of its own (the person, the grant, a five-minute ceiling), so a
// client that disconnects stops hearing and not the run.
func (s *server) SendAssistantMessage(ctx context.Context, req gen.SendAssistantMessageRequestObject) (gen.SendAssistantMessageResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.SendAssistantMessageResponseObject](r, err)
	}
	if s.deps.AI == nil || s.deps.AssistantGrants == nil || s.deps.Operations == nil {
		return refuse(503, codeAIUnavailable, "The assistant's model is not available"), nil
	}
	if req.Body == nil {
		return invalid("A message is required", nil), nil
	}
	text := strings.TrimSpace(req.Body.Text)
	if text == "" || utf8.RuneCountInString(text) > maxMessageChars {
		return invalid("The message is not valid", map[string][]string{"text": {fmt.Sprintf("text is from 1 to %d characters", maxMessageChars)}}), nil
	}
	chip := req.Body.Context
	if chip != nil && (len(chip.App) > 40 || len(chip.Route) > 300 || (chip.Label != nil && utf8.RuneCountInString(*chip.Label) > 200)) {
		return invalid("The context is not valid", map[string][]string{"context": {"context is too long"}}), nil
	}

	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	conv, err := q.GetConversation(ctx, store.GetConversationParams{ID: req.Id, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: read the conversation: %w", err)
	}

	runID, userMessageID, now := uuid.New(), uuid.New(), s.deps.Clock()

	// The run's context is made, and registered, before the run exists in the
	// database: a delete or a stop that arrives the moment the row does must find
	// it to cancel.
	runCtx, cancel := context.WithCancelCause(context.Background())
	runCtx, stopTimer := context.WithTimeoutCause(runCtx, runCeiling, errors.New("assistant: the run reached its ceiling"))
	s.runs.add(runID, conv.ID, func(cause error) { cancel(cause) })
	started := false
	defer func() {
		if !started { // a refusal or a failure before the goroutine took over
			stopTimer()
			cancel(nil)
			s.runs.remove(runID)
		}
	}()
	var chipJSON []byte
	if chip != nil {
		chipJSON, _ = json.Marshal(chip)
	}
	var userMessage store.AssistantMessage
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		if _, err := tq.InsertRun(ctx, store.InsertRunParams{ID: runID, ConversationID: conv.ID, UserID: user, Now: now}); err != nil {
			return err
		}
		var err error
		userMessage, err = tq.InsertMessage(ctx, store.InsertMessageParams{
			ID: userMessageID, ConversationID: conv.ID, RunID: &runID, Role: "user",
			Content: marshalParts([]gen.AssistantPart{textPart(text)}), Context: chipJSON, SearchText: text, Now: now,
		})
		if err != nil {
			return err
		}
		if err := tq.TouchConversation(ctx, store.TouchConversationParams{ID: conv.ID, Now: now}); err != nil {
			return err
		}
		if err := tq.SetTitleIfEmpty(ctx, store.SetTitleIfEmptyParams{ID: conv.ID, Title: truncateTitle(text)}); err != nil {
			return err
		}
		return tq.SetLastConversation(ctx, store.SetLastConversationParams{UserID: user, ConversationID: &conv.ID, Now: now})
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return refuse(409, codeRunInProgress, "The assistant is still answering the last message"), nil
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // the conversation was deleted meanwhile
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: start the run: %w", err)
	}

	// The grant, from the person's own request: it carries their session to the
	// run, and ends with the run.
	grantID, err := s.deps.AssistantGrants.Issue(ctx, conv.ID, runID, grantTTL)
	if err != nil {
		s.finishRun(context.WithoutCancel(ctx), runID, "failed", "grant_failed")
		return nil, fmt.Errorf("assistant: issue the run's grant: %w", err)
	}

	lang, name := "en", ""
	if entry, err := s.deps.Users.User(ctx, user); err == nil && entry != nil {
		name = entry.DisplayName
		if entry.PreferredLanguage == "nb" {
			lang = "nb"
		}
	}

	runCtx = contracts.WithAssistantGrant(contracts.AsUser(runCtx, user), grantID)

	queue := newEventQueue()
	queue.emit(eventRun, map[string]any{"runId": runID})
	queue.emit(eventMessage, messageView(userMessage))

	r := &runner{
		s: s, ctx: runCtx, runID: runID, convID: conv.ID, userID: user, grantID: grantID, queue: queue,
		lang: lang, name: name, chip: chip,
		tools: &toolbox{s: s, ctx: runCtx, userID: user, lang: lang, params: map[string]map[string]string{}},
	}
	started = true
	go func() {
		defer stopTimer()
		defer cancel(nil)
		defer s.runs.remove(runID)
		r.execute()
	}()
	return streamResponse{ctx: ctx, queue: queue}, nil
}

// finishRun ends the run's row, once.
func (s *server) finishRun(ctx context.Context, runID uuid.UUID, state, code string) {
	var c *string
	if code != "" {
		c = &code
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := store.New(s.deps.Pool).FinishRun(fctx, store.FinishRunParams{ID: runID, State: state, ErrorCode: c, Now: s.deps.Clock()})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.deps.Logger.Error("assistant: finish the run", "run", runID, "error", err)
	}
}

// runner executes one run: the model loop of S5 and S14.
type runner struct {
	s       *server
	ctx     context.Context // the person, the grant, the ceiling
	runID   uuid.UUID
	convID  uuid.UUID
	userID  uuid.UUID
	grantID uuid.UUID
	queue   *eventQueue
	lang    string
	name    string
	chip    *gen.AssistantMessageContext
	tools   *toolbox

	toolCalls, turns int
}

// persist is the context database writes use: the run's own, minus its
// cancellation, so a stop or a ceiling does not lose what was already said.
func (r *runner) persist() context.Context { return context.WithoutCancel(r.ctx) }

func (r *runner) execute() {
	state, code := "done", ""
	defer func() {
		// Whatever happened, the grant ends with the run and the run row says how.
		revoke, cancel := context.WithTimeout(r.persist(), 10*time.Second)
		defer cancel()
		_ = r.s.deps.AssistantGrants.Revoke(revoke, r.grantID)
		if errors.Is(context.Cause(r.ctx), errConversationGone) {
			r.queue.emit(eventDone, map[string]string{"state": "stopped"})
			r.queue.close()
			return
		}
		r.s.finishRun(r.persist(), r.runID, state, code)
		if state == "failed" {
			r.queue.emit(eventError, map[string]string{"code": code})
		} else {
			r.queue.emit(eventDone, map[string]string{"state": state})
		}
		r.queue.close()
	}()

	rows, err := store.New(r.s.deps.Pool).ListMessages(r.persist(), store.ListMessagesParams{ConversationID: r.convID, UserID: r.userID})
	if err != nil {
		state, code = "failed", "internal"
		r.s.deps.Logger.Error("assistant: read the conversation for a run", "run", r.runID, "error", err)
		return
	}
	history := modelMessages(rows)

	system := systemPrompt(promptInput{
		now: r.s.deps.Clock(), name: r.name, language: r.lang, apps: r.s.deps.Config.Modules, context: r.chip,
	})
	r.s.deps.Logger.Debug("assistant: a run starts", "run", r.runID, "prompt", promptVersion, "language", r.lang)
	budget := r.s.deps.Config.AIContextTokens
	if budget <= 0 {
		budget = 16000
	}

	for {
		if cause := r.stopped(); cause != "" {
			state, code = cause, ""
			if cause == "failed" {
				code = "timeout"
			}
			return
		}
		r.turns++
		final := r.turns >= maxTurns || r.toolCalls >= maxToolCalls
		tools := toolDefs
		if final {
			tools = nil
		}
		resp, err := r.s.deps.AI.Chat(r.ctx, aiTaskChat, ai.ChatRequest{
			System: system, Messages: fitContext(system, tools, history, budget), Tools: tools, MaxTokens: answerTokens,
		}, func(text string) { r.queue.emit(eventDelta, map[string]string{"text": text}) })
		if err != nil {
			if cause := r.stopped(); cause != "" {
				state = cause
				if cause == "failed" {
					code = "timeout"
				}
				return
			}
			state, code = "failed", "ai_failed"
			r.s.deps.Logger.Warn("assistant: the model call failed", "run", r.runID, "error_type", fmt.Sprintf("%T", err))
			return
		}
		_ = store.New(r.s.deps.Pool).CountRunProgress(r.persist(), store.CountRunProgressParams{ID: r.runID, ToolCalls: int32(r.toolCalls), ModelTurns: int32(r.turns)})

		calls := resp.Message.ToolCalls
		if final {
			calls = nil // no tools were offered; whatever a stubborn model asks is ignored
		}
		assistantMsg, err := r.saveAssistant(resp, calls)
		if err != nil {
			state, code = "failed", "internal"
			r.s.deps.Logger.Error("assistant: save the answer", "run", r.runID, "error", err)
			return
		}
		history = append(history, ai.Message{Role: ai.RoleAssistant, Text: resp.Message.Text, ToolCalls: calls})
		if len(calls) == 0 {
			return
		}

		var links []gen.AssistantPart
		for _, call := range calls {
			result, link := r.runTool(call)
			history = append(history, ai.Message{Role: ai.RoleTool, ToolCallID: call.ID, Text: result})
			if link != nil {
				links = append(links, *link)
			}
		}
		if len(links) > 0 {
			r.addLinks(assistantMsg, links)
		}
	}
}

// stopped reports why the run must end now, as the state it ends in, or "".
func (r *runner) stopped() string {
	if cause := context.Cause(r.ctx); cause != nil {
		switch {
		case errors.Is(cause, errRunStopped), errors.Is(cause, errConversationGone):
			return "stopped"
		default:
			return "failed"
		}
	}
	if stop, err := store.New(r.s.deps.Pool).GetStopRequested(r.persist(), r.runID); err == nil && stop {
		return "stopped"
	}
	return ""
}

// saveAssistant stores the model's answer: its text and the tool calls it
// asked for, with the model and the tokens the provider reported.
func (r *runner) saveAssistant(resp ai.ChatResponse, calls []ai.ToolCall) (store.AssistantMessage, error) {
	var parts []gen.AssistantPart
	if resp.Message.Text != "" {
		parts = append(parts, textPart(resp.Message.Text))
	}
	for _, c := range calls {
		var args map[string]any
		_ = json.Unmarshal(c.Arguments, &args)
		callID, name, label := c.ID, c.Name, toolLabel(r.lang, c.Name, "")
		parts = append(parts, gen.AssistantPart{Type: gen.AssistantPartType("tool_call"), CallId: &callID, Name: &name, Label: &label, Arguments: &args})
	}
	var model *string
	if resp.Model != "" {
		model = &resp.Model
	}
	msg, err := store.New(r.s.deps.Pool).InsertMessage(r.persist(), store.InsertMessageParams{
		ID: uuid.New(), ConversationID: r.convID, RunID: &r.runID, Role: "assistant", Content: marshalParts(parts),
		SearchText: resp.Message.Text, Model: model, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens, Now: r.s.deps.Clock(),
	})
	if err != nil {
		return msg, err
	}
	_ = store.New(r.s.deps.Pool).TouchConversation(r.persist(), store.TouchConversationParams{ID: r.convID, Now: r.s.deps.Clock()})
	r.queue.emit(eventMessage, messageView(msg))
	return msg, nil
}

// addLinks appends the navigate tool's links to the assistant message that
// asked for them, and announces the message again (same id) so a client
// replaces it.
func (r *runner) addLinks(msg store.AssistantMessage, links []gen.AssistantPart) {
	var parts []gen.AssistantPart
	_ = json.Unmarshal(msg.Content, &parts)
	parts = append(parts, links...)
	content := marshalParts(parts)
	if err := store.New(r.s.deps.Pool).UpdateMessageContent(r.persist(), store.UpdateMessageContentParams{ID: msg.ID, Content: content}); err != nil {
		r.s.deps.Logger.Error("assistant: save a link", "run", r.runID, "error", err)
		return
	}
	msg.Content = content
	r.queue.emit(eventMessage, messageView(msg))
}

// runTool executes one tool call: it announces it, runs it, writes the action
// and the tool message, and answers the content the model sees and the link a
// navigate produced.
func (r *runner) runTool(call ai.ToolCall) (string, *gen.AssistantPart) {
	r.toolCalls++
	label := toolLabel(r.lang, call.Name, "")
	r.queue.emit(eventTool, map[string]any{"callId": call.ID, "name": call.Name, "label": label, "state": "started"})

	var out outcome
	start := time.Now()
	if r.toolCalls > maxToolCalls {
		out = refused("tool_limit", "the limit of tool calls for this answer is reached; answer with what you have")
	} else {
		out = r.tools.run(call)
	}
	elapsed := time.Since(start)
	if out.detail != "" {
		label = toolLabel(r.lang, call.Name, out.detail)
	}
	if out.operation == "" {
		out.operation = "tool:" + call.Name
	}
	if out.mode == "" {
		out.mode = "tool"
	}

	q := store.New(r.s.deps.Pool)
	if err := q.InsertAction(r.persist(), store.InsertActionParams{
		ID: uuid.New(), RunID: r.runID, OperationID: out.operation, Mode: out.mode, HttpStatus: out.httpStatus,
		DurationMs: int32(elapsed.Milliseconds()), Now: r.s.deps.Clock(),
	}); err != nil {
		r.s.deps.Logger.Error("assistant: log an action", "run", r.runID, "error", err)
	}

	callID, name, status, content := call.ID, call.Name, gen.AssistantPartStatus(out.status), out.content
	part := gen.AssistantPart{Type: gen.AssistantPartType("tool_result"), CallId: &callID, Name: &name, Status: &status, Content: &content}
	msg, err := q.InsertMessage(r.persist(), store.InsertMessageParams{
		ID: uuid.New(), ConversationID: r.convID, RunID: &r.runID, Role: "tool", Content: marshalParts([]gen.AssistantPart{part}), Now: r.s.deps.Clock(),
	})
	if err != nil {
		r.s.deps.Logger.Error("assistant: save a tool result", "run", r.runID, "error", err)
	} else {
		r.queue.emit(eventMessage, messageView(msg))
	}
	r.queue.emit(eventTool, map[string]any{"callId": call.ID, "name": call.Name, "label": label, "state": "done"})

	var link *gen.AssistantPart
	if out.link != nil {
		route, linkLabel := out.link.route, out.link.label
		link = &gen.AssistantPart{Type: gen.AssistantPartType("link"), Route: &route, Label: &linkLabel}
	}
	return out.content, link
}

// modelMessages rebuilds the model's transcript from the stored messages: the
// person's words, the model's text and tool calls, and each tool's result.
// Link parts are for the interface only.
func modelMessages(rows []store.AssistantMessage) []ai.Message {
	var out []ai.Message
	for _, row := range rows {
		var parts []gen.AssistantPart
		if json.Unmarshal(row.Content, &parts) != nil {
			continue
		}
		switch row.Role {
		case "user":
			out = append(out, ai.Message{Role: ai.RoleUser, Text: joinText(parts)})
		case "assistant":
			m := ai.Message{Role: ai.RoleAssistant, Text: joinText(parts)}
			for _, p := range parts {
				if string(p.Type) == "tool_call" && p.CallId != nil && p.Name != nil {
					args, _ := json.Marshal(p.Arguments)
					if p.Arguments == nil {
						args = []byte("{}")
					}
					m.ToolCalls = append(m.ToolCalls, ai.ToolCall{ID: *p.CallId, Name: *p.Name, Arguments: args})
				}
			}
			out = append(out, m)
		case "tool":
			for _, p := range parts {
				if string(p.Type) == "tool_result" && p.CallId != nil {
					content := ""
					if p.Content != nil {
						content = *p.Content
					}
					out = append(out, ai.Message{Role: ai.RoleTool, ToolCallID: *p.CallId, Text: content})
				}
			}
		}
	}
	return out
}

func joinText(parts []gen.AssistantPart) string {
	var texts []string
	for _, p := range parts {
		if string(p.Type) == "text" && p.Text != nil {
			texts = append(texts, *p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// StopAssistantRun (POST /api/v1/assistant/runs/{id}/stop)
func (s *server) StopAssistantRun(ctx context.Context, req gen.StopAssistantRunRequestObject) (gen.StopAssistantRunResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.StopAssistantRunResponseObject](r, err)
	}
	run, err := store.New(s.deps.Pool).RequestStop(ctx, store.RequestStopParams{ID: req.Id, UserID: callerID(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: stop the run: %w", err)
	}
	s.runs.cancelRun(run.ID)
	return gen.StopAssistantRun200JSONResponse(runView(run)), nil
}
