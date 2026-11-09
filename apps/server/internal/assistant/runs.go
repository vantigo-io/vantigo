package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
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
	// A conversation with a virtual employee goes on only while the person may see
	// agents and the agent is active (V31); the agent's side of the double check is
	// read now and used for the whole run.
	var panel *panelAgent
	if conv.AgentID != nil {
		if !s.mayTalkToAgents(ctx) {
			r := unavailableAgent()
			return r, nil
		}
		var err error
		if panel, err = s.loadPanelAgent(ctx, *conv.AgentID); errors.Is(err, contracts.ErrColleagueUnavailable) {
			r := unavailableAgent()
			return r, nil
		} else if err != nil {
			return nil, fmt.Errorf("assistant: read the agent: %w", err)
		}
	}
	// The Owner's daily token budget (S14): a person who has used the day's is
	// told so before anything is stored.
	if over, err := s.overBudget(ctx, user); err != nil {
		return nil, err
	} else if over {
		return refuse(http.StatusTooManyRequests, codeBudgetExhausted, "You have used the assistant's token budget for today"), nil
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

	s.countRun(ctx, user)
	lang, name := s.person(ctx, user)
	runCtx = withGrant(runCtx, user, grantID)

	queue := newEventQueue()
	queue.emit(eventRun, map[string]any{"runId": runID})
	queue.emit(eventMessage, messageView(userMessage))

	r := &runner{
		s: s, ctx: runCtx, runID: runID, convID: conv.ID, userID: user, grantID: grantID, queue: queue,
		lang: lang, name: name, agent: panel,
		tools: s.newToolbox(runCtx, user, lang, panel),
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

// person is the language and the name the model is told the person has.
func (s *server) person(ctx context.Context, user uuid.UUID) (lang, name string) {
	lang = "en"
	if entry, err := s.deps.Users.User(ctx, user); err == nil && entry != nil {
		name = entry.DisplayName
		if entry.PreferredLanguage == "nb" {
			lang = "nb"
		}
	}
	return lang, name
}

// withGrant is the context a run's tools and calls carry: the person and the
// assistant grant that stands on their session.
func withGrant(ctx context.Context, user, grant uuid.UUID) context.Context {
	return contracts.WithAssistantGrant(contracts.AsUser(ctx, user), grant)
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

// runner executes one run: the model loop of S5 and S14. A run that proposed a
// change and was decided is the same run, resumed: resume carries the person's
// decision, which is applied before the model is asked again.
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
	tools   *toolbox
	resume  *decision
	// agent is the virtual employee the conversation is with (V31), nil for the
	// Assistant's own.
	agent *panelAgent

	turns int
	// final has the run answer once without tools: the resumed run after a bulk
	// job ends tells the person how it went.
	final bool
	// proposed is the action this segment of the run created: the run pauses
	// for it (S12).
	proposed *store.AssistantPendingAction
}

// panelPerson is the agent the system prompt speaks as, nil for the Assistant's
// own conversations and for a run whose agent is gone (its first run was refused
// already, and a later one only closes a card).
func (r *runner) panelPerson() *contracts.ColleagueAgent {
	if r.agent == nil || r.agent.agent.ID == uuid.Nil {
		return nil
	}
	return &r.agent.agent
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
		if state == stateAwaiting {
			// The run waits for the person: the change is shown on a card, and the
			// grant above ended with this segment of the run.
			if _, err := store.New(r.s.deps.Pool).PauseRun(r.persist(), r.runID); err != nil {
				r.s.deps.Logger.Error("assistant: pause the run", "run", r.runID, "error", err)
				state, code = "failed", "internal"
			}
		}
		if state != stateAwaiting {
			r.s.finishRun(r.persist(), r.runID, state, code)
		}
		if state == "failed" {
			r.queue.emit(eventError, map[string]string{"code": code})
		} else {
			r.queue.emit(eventDone, map[string]string{"state": state})
		}
		r.queue.close()
	}()

	// A resumed run first carries out what the person decided.
	if r.resume != nil {
		r.decide(*r.resume)
		if !r.resume.confirm {
			// A cancelled change is answered in one short line, without tools.
			r.turns = maxTurns - 1
		}
	}

	if r.final {
		r.turns = maxTurns - 1
	}

	q := store.New(r.s.deps.Pool)
	rows, err := q.ListMessages(r.persist(), store.ListMessagesParams{ConversationID: r.convID, UserID: r.userID})
	if err != nil {
		state, code = "failed", "internal"
		r.s.deps.Logger.Error("assistant: read the conversation for a run", "run", r.runID, "error", err)
		return
	}
	actions, err := q.ListPendingActions(r.persist(), store.ListPendingActionsParams{ConversationID: r.convID, UserID: r.userID})
	if err != nil {
		state, code = "failed", "internal"
		r.s.deps.Logger.Error("assistant: read the pending actions for a run", "run", r.runID, "error", err)
		return
	}
	loc := r.s.businessLocation(r.persist())
	provider := r.s.deps.AI.Provider()
	preserving := provider == ai.ProviderAnthropic

	// The system prompt is frozen at the conversation's first run and sent
	// unchanged ever after: a Claude model's thinking blocks are bound to it.
	cstate, err := q.FreezeConversationPrompt(r.persist(), store.FreezeConversationPromptParams{
		ConversationID: r.convID, PromptVersion: promptVersion,
		SystemPrompt: systemPrompt(promptInput{name: r.name, language: r.lang, apps: r.s.deps.Config.Modules, agent: r.panelPerson()}),
	})
	if err != nil {
		state, code = "failed", "internal"
		r.s.deps.Logger.Error("assistant: freeze the system prompt", "run", r.runID, "error", err)
		return
	}
	system := cstate.SystemPrompt
	r.s.deps.Logger.Debug("assistant: a run starts", "run", r.runID, "prompt", cstate.PromptVersion, "language", r.lang)
	budget := r.s.deps.Config.AIContextTokens
	if budget <= 0 {
		budget = 16000
	}

	if preserving {
		cstate = r.compact(q, cstate, rows, actions, system, loc, budget)
	}
	history := modelMessages(afterSummary(rows, cstate), actions, loc)
	if cstate.Summary != nil {
		history = append([]ai.Message{{Role: ai.RoleUser, Text: agentloop.SummaryMessage(*cstate.Summary)}}, history...)
	}

	out, err := r.loop(budget).Run(r.ctx, agentloop.Input{System: system, History: history, Turns: r.turns})
	state, code = out.State, out.Code
	if state == agentloop.StatePaused {
		state = stateAwaiting
	}
	switch {
	case err == nil:
	case code == agentloop.CodeAIFailed || code == agentloop.CodeAIRefused:
		r.s.deps.Logger.Warn("assistant: the model call failed", "run", r.runID, "error_type", fmt.Sprintf("%T", errors.Unwrap(err)))
	default:
		r.s.deps.Logger.Error("assistant: the run failed", "run", r.runID, "error", err)
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
		SearchText: resp.Message.Text, Model: model, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
		Provider: agentloop.ProviderBlocks(resp, calls), CacheReadTokens: resp.CacheReadTokens, CacheWriteTokens: resp.CacheWriteTokens,
		Now: r.s.deps.Clock(),
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

// modelMessages rebuilds the model's transcript from the stored messages: the
// person's words, the model's text and tool calls, and each tool's result.
// Link parts are for the interface only.
//
// A tool result that stands for a change the person never decided — the run was
// stopped, or the card expired — reads as the change not made, whatever it said
// while it waited.
func modelMessages(rows []store.AssistantMessage, actions []store.AssistantPendingAction, loc *time.Location) []ai.Message {
	undone := map[uuid.UUID]string{}
	for _, a := range actions {
		if a.ToolMessageID == nil {
			continue
		}
		switch {
		case a.Status == "cancelled" || a.Status == "expired":
			undone[*a.ToolMessageID] = a.Status
		case a.Status == "failed" && strings.Contains(string(a.Outcome), `"interrupted"`):
			undone[*a.ToolMessageID] = "interrupted"
		}
	}
	var out []ai.Message
	for _, row := range rows {
		var parts []gen.AssistantPart
		if json.Unmarshal(row.Content, &parts) != nil {
			continue
		}
		switch row.Role {
		case "user":
			out = append(out, ai.Message{Role: ai.RoleUser, Text: turnHeader(row.CreatedAt, loc, rowContext(row)) + joinText(parts)})
		case "assistant":
			m := ai.Message{Role: ai.RoleAssistant, Text: joinText(parts), Provider: row.Provider}
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
					if why, ok := undone[row.ID]; ok {
						content = agentloop.Wrap(map[string]any{"changed": false, "status": why, "note": "the change was not made"})
					}
					out = append(out, ai.Message{Role: ai.RoleTool, ToolCallID: *p.CallId, Text: content})
				}
			}
		}
	}
	return out
}

// rowContext is the screen chip a message of the person's was sent with.
func rowContext(row store.AssistantMessage) *gen.AssistantMessageContext {
	if len(row.Context) == 0 {
		return nil
	}
	var c gen.AssistantMessageContext
	if json.Unmarshal(row.Context, &c) != nil {
		return nil
	}
	return &c
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
	if run.State == stateAwaiting {
		// A run that waits for the person has no goroutine to stop: stopping it
		// cancels the change it proposed and ends it. A run that waits for a bulk
		// job stops the job: a plan not started is cancelled, a job waiting for
		// Continue ends, a running one stops between chunks, and the run goes on
		// when the job ends.
		q := store.New(s.deps.Pool)
		if job, err := q.GetBulkJobByRun(ctx, store.GetBulkJobByRunParams{RunID: run.ID, UserID: callerID(ctx)}); err == nil && job.State != jobDone && job.State != jobStopped && job.State != jobFailed && job.State != jobExpired {
			if err := s.stopBulkJob(ctx, job); err != nil {
				return nil, err
			}
			if cur, err := q.GetRun(ctx, store.GetRunParams{ID: run.ID, UserID: callerID(ctx)}); err == nil {
				run = cur
			}
			return gen.StopAssistantRun200JSONResponse(runView(run)), nil
		}
		if err := q.CancelRunPendingActions(ctx, store.CancelRunPendingActionsParams{RunID: run.ID, Now: s.deps.Clock()}); err != nil {
			return nil, fmt.Errorf("assistant: cancel the waiting change: %w", err)
		}
		if ended, err := q.FinishRun(ctx, store.FinishRunParams{ID: run.ID, State: "stopped", Now: s.deps.Clock()}); err == nil {
			run = ended
		}
		return gen.StopAssistantRun200JSONResponse(runView(run)), nil
	}
	s.runs.cancelRun(run.ID)
	return gen.StopAssistantRun200JSONResponse(runView(run)), nil
}
