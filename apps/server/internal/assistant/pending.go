package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Changes with confirmation (the Assistant design S12). A `confirm` operation
// never runs on the model's say-so: call_operation turns it into a pending
// action (a card), the run pauses, and only the person's own POST to confirm
// runs it, through the dispatcher, as them.

const (
	// pendingTTL is how long a card waits for the person.
	pendingTTL = 24 * time.Hour

	codeActionDecided = "action_decided"
	codeActionExpired = "action_expired"
	codeNotAllowed    = "operation_unavailable"

	// The run states of a run that waits and of one whose card timed out.
	stateAwaiting = "awaiting_confirmation"
	stateExpired  = "expired"

	maxFields       = 20
	maxFieldChars   = 200
	maxExplainChars = 300
)

// storedRequest is what the model asked for, kept until the action is decided
// and then reduced by assistant.redact_request to the operation, the path
// parameters (ids) and the names of the fields.
type storedRequest struct {
	OperationID string            `json:"operationId"`
	PathParams  map[string]any    `json:"pathParams,omitempty"`
	Query       map[string]any    `json:"query,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	Fields      []string          `json:"fields"`
}

// previewDoc is the card, as stored and as the contract answers it.
type previewDoc struct {
	Summary string                      `json:"summary"`
	Explain string                      `json:"explain,omitempty"`
	Method  string                      `json:"method"`
	Target  *gen.AssistantPendingTarget `json:"target,omitempty"`
	Fields  []gen.AssistantPendingField `json:"fields"`
	// Bulk is the preview of a bulk plan: the sample and the excluded.
	Bulk *bulkPreview `json:"bulk,omitempty"`
}

// pendingDraft is a proposed change before it is stored.
type pendingDraft struct {
	request storedRequest
	preview previewDoc
	// bulk is the plan of a bulk change; nil for a single change.
	bulk *bulkDraft
}

// pendingView is a stored action as the contract answers it. An action
// nobody decided in time reads as expired before the worker has written it.
func pendingView(row store.AssistantPendingAction, now time.Time) gen.AssistantPendingAction {
	var doc previewDoc
	_ = json.Unmarshal(row.Preview, &doc)
	if doc.Fields == nil {
		doc.Fields = []gen.AssistantPendingField{}
	}
	status := row.Status
	if status == "pending" && !row.ExpiresAt.After(now) {
		status = "expired"
	}
	kind := gen.AssistantPendingActionKind(row.Kind)
	out := gen.AssistantPendingAction{
		Kind: &kind, Id: row.ID, RunId: row.RunID, ConversationId: row.ConversationID, OperationId: row.OperationID, Method: doc.Method,
		Status: gen.AssistantPendingActionStatus(status), Summary: doc.Summary, Fields: doc.Fields, Target: doc.Target,
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, DecidedAt: row.DecidedAt,
	}
	if doc.Explain != "" {
		out.Explain = &doc.Explain
	}
	if len(row.Outcome) > 0 {
		var o gen.AssistantPendingOutcome
		if json.Unmarshal(row.Outcome, &o) == nil {
			out.Outcome = &o
		}
	}
	return out
}

// loggedCall is a call a tool made besides its own, such as the read that
// gives a card its current values; the action log records it.
type loggedCall struct {
	operation string
	mode      string
	status    int32
	elapsed   time.Duration
}

// clip shortens s to at most n characters.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// display is a value as the card shows it.
func display(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return clip(x, maxFieldChars)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return clip(string(raw), maxFieldChars)
}

// decodeObject decodes raw as a JSON object with numbers kept as written.
func decodeObject(raw []byte) (map[string]any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// labelKeys are the properties a record's name is read from, best first.
var labelKeys = []string{"name", "displayName", "title", "subject", "label", "code", "number"}

func labelOf(obj map[string]any) string {
	for _, k := range labelKeys {
		if s, ok := obj[k].(string); ok && strings.TrimSpace(s) != "" {
			return clip(strings.TrimSpace(s), 120)
		}
	}
	return ""
}

// routeFor is the in-app route of the record a change is about, from the
// operation's path template and its path parameters: /api/v1/customers/{id}
// and everything below it is /customers/<id>, when that is a route of the app.
// It answers "" when there is none.
func routeFor(pathTemplate string, params map[string]any) string {
	segs := strings.Split(strings.Trim(strings.TrimPrefix(pathTemplate, "/api/v1"), "/"), "/")
	if len(segs) < 2 || strings.ContainsAny(segs[0], "{}") || !strings.HasPrefix(segs[1], "{") {
		return ""
	}
	name := strings.Trim(segs[1], "{}")
	raw, ok := params[name]
	if !ok {
		return ""
	}
	value, ok := scalarString(raw)
	if !ok || value == "" {
		return ""
	}
	route := "/" + segs[0] + "/" + url.PathEscape(value)
	if !validRoute(route) {
		return ""
	}
	return route
}

// routeOfCreated is the route of a record a POST to a collection created, from
// the id in its answer.
func routeOfCreated(pathTemplate string, body map[string]any) string {
	segs := strings.Split(strings.Trim(strings.TrimPrefix(pathTemplate, "/api/v1"), "/"), "/")
	if len(segs) != 1 || body == nil {
		return ""
	}
	value, ok := scalarString(body["id"])
	if !ok || value == "" {
		return ""
	}
	route := "/" + segs[0] + "/" + url.PathEscape(value)
	if !validRoute(route) {
		return ""
	}
	return route
}

func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	}
	return "", false
}

// before is what a read of the record said about it.
type before struct {
	object map[string]any
	etag   string
}

// readBefore reads the record a change is about, for the card's current values
// and the ETag the change is guarded with. It exists only when the operation's
// path has a GET at the same path template that is auto for the person — one
// they may call and the Owner's levels allow — and is read through the
// dispatcher as the person, so nothing is read that they could not read. It is
// best effort and never a write.
func (t *toolbox) readBefore(info operations.Info, req operations.CallRequest) (*before, []loggedCall) {
	var getter *operations.Info
	for _, candidate := range t.ops.Callable() {
		if candidate.Method == "GET" && candidate.Path == info.Path && candidate.Mode == operations.ModeAuto {
			c := candidate
			getter = &c
			break
		}
	}
	if getter == nil {
		return nil, nil
	}
	start := time.Now()
	res, err := t.s.deps.Operations.Call(t.ctx, operations.CallRequest{OperationID: getter.ID, PathParams: req.PathParams})
	if err != nil {
		return nil, nil
	}
	logged := []loggedCall{{operation: getter.ID, mode: string(operations.ModeAuto), status: int32(res.Status), elapsed: time.Since(start)}}
	if res.Status != 200 {
		return nil, logged
	}
	obj, ok := decodeObject(res.Body)
	if !ok {
		return nil, logged
	}
	b := &before{object: obj, etag: strings.TrimSpace(res.Header.Get("ETag"))}
	if b.etag == "" {
		// A module that versions its rows by a revision in the body hands the same
		// number out as the header (the planning module's rows).
		for _, k := range []string{"revision", "version"} {
			if tag, ok := revisionETag(obj[k]); ok {
				b.etag = tag
				break
			}
		}
	}
	return b, logged
}

// revisionETag is the entity tag for a revision read from a response body.
// Every module that versions its rows this way (planning, calendar) writes the
// revision as an integer, so only an integer is used, and the tag is built
// from the parsed number rather than the body's text: nothing the body holds
// reaches the quotes. Any other value gives no tag, and the change then goes
// without If-Match, as for a record that has no version.
func revisionETag(v any) (string, bool) {
	text, ok := scalarString(v)
	if !ok {
		return "", false
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return "", false
	}
	return `"` + strconv.FormatInt(n, 10) + `"`, true
}

// propose turns a change the person has yet to confirm into a pending draft.
// It checks the request fits the operation, builds the card (the fields, with
// their current values where a read of the same record can say) and writes
// nothing.
func (t *toolbox) propose(info operations.Info, req operations.CallRequest, explain string) outcome {
	out := outcome{operation: req.OperationID, mode: string(operations.ModeConfirm), detail: info.Summary}
	if t.proposed {
		o := refused("one_change_at_a_time", "one change at a time: wait for the person to decide the change already shown, then propose the next")
		o.operation, o.mode, o.detail = out.operation, out.mode, out.detail
		return o
	}
	if err := t.s.deps.Operations.Validate(t.ctx, req); err != nil {
		var oe *operations.Error
		if errors.As(err, &oe) {
			o := refused(oe.Code, oe.Message)
			o.operation, o.mode, o.detail = out.operation, out.mode, out.detail
			return o
		}
		return outcome{content: agentloop.Wrap(map[string]string{"error": "call_failed", "message": "the request could not be checked"}), status: "error", operation: out.operation, mode: out.mode, detail: out.detail}
	}

	body, _ := decodeObject(req.Body)
	var names []string
	for k := range body {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > maxFields {
		names = names[:maxFields]
	}

	prior, logged := t.readBefore(info, req)
	out.extra = logged

	doc := previewDoc{Summary: info.Summary, Explain: clip(strings.TrimSpace(explain), maxExplainChars), Method: info.Method, Fields: []gen.AssistantPendingField{}}
	var current map[string]any
	if prior != nil {
		current = recordFields(prior.object, names)
	}
	doc.Fields = cardFields(names, body, current)
	if body == nil && len(bytes.TrimSpace(req.Body)) > 0 && string(bytes.TrimSpace(req.Body)) != "null" {
		next := display(json.RawMessage(req.Body))
		doc.Fields = append(doc.Fields, gen.AssistantPendingField{Name: "body", Next: &next})
		names = append(names, "body")
	}

	target := &gen.AssistantPendingTarget{}
	if current != nil {
		if label := labelOf(current); label != "" {
			target.Label = &label
		}
	}
	if route := routeFor(info.Path, req.PathParams); route != "" {
		target.Route = &route
	}
	if target.Label != nil || target.Route != nil {
		doc.Target = target
	}

	// The change is guarded with the ETag the card was made at, when the
	// operation takes an If-Match and the model gave none.
	headers := req.Headers
	if prior != nil && prior.etag != "" {
		for name, where := range t.ops.Locations(req.OperationID) {
			if where == "header" && strings.EqualFold(name, "If-Match") {
				if headers == nil {
					headers = map[string]string{}
				}
				if _, given := headers[name]; !given {
					headers[name] = prior.etag
				}
			}
		}
	}

	t.proposed = true
	out.pending = &pendingDraft{
		request: storedRequest{
			OperationID: req.OperationID, PathParams: req.PathParams, Query: req.Query, Headers: headers,
			Body: req.Body, Fields: append([]string{}, names...),
		},
		preview: doc,
	}
	out.content = agentloop.Wrap(map[string]any{
		"status":  stateAwaiting,
		"summary": info.Summary,
		"note":    "Nothing has been changed. The change is shown to the person on a card and runs only if they confirm it. Say in one sentence what you proposed and that it waits for their confirmation, then stop. Do not call another operation that changes data, and never say the change is done.",
	})
	out.status = "ok"
	return out
}

// recordFields is the record a read answered: its fields are under "data" when
// the module wraps them, else the object itself.
func recordFields(object map[string]any, names []string) map[string]any {
	if inner, ok := object["data"].(map[string]any); ok && len(names) > 0 {
		if _, top := object[names[0]]; !top {
			return inner
		}
	}
	return object
}

// cardFields are the fields of a card: each name with the value the change
// gives it and, where a read of the same record said, the value it has now.
func cardFields(names []string, body, current map[string]any) []gen.AssistantPendingField {
	fields := make([]gen.AssistantPendingField, 0, len(names))
	for _, name := range names {
		next := display(body[name])
		field := gen.AssistantPendingField{Name: name, Next: &next}
		if current != nil {
			if v, ok := current[name]; ok {
				cur := display(v)
				field.Current = &cur
			}
		}
		fields = append(fields, field)
	}
	return fields
}

// createPending stores the draft and its card, tied to the tool result that
// stands for it.
func (r *runner) createPending(d *pendingDraft, callID string, toolMessage uuid.UUID) (store.AssistantPendingAction, error) {
	if d.bulk != nil {
		return r.createBulkPending(d, callID, toolMessage)
	}
	request, err := json.Marshal(d.request)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	preview, err := json.Marshal(d.preview)
	if err != nil {
		return store.AssistantPendingAction{}, err
	}
	now := r.s.deps.Clock()
	return store.New(r.s.deps.Pool).InsertPendingAction(r.persist(), store.InsertPendingActionParams{Kind: kindChange,
		ID: uuid.New(), RunID: r.runID, ConversationID: r.convID, UserID: r.userID, OperationID: d.request.OperationID,
		CallID: callID, ToolMessageID: &toolMessage, Request: request, Preview: preview, Now: now, ExpiresAt: now.Add(pendingTTL),
	})
}

// A decision on a pending action: what the person chose.
type decision struct {
	action  store.AssistantPendingAction
	confirm bool
}

// problemText is the sentence an operation's own refusal carries, if it has one.
func problemText(body json.RawMessage) string {
	m, ok := decodeObject(body)
	if !ok {
		return ""
	}
	for _, k := range []string{"detail", "title", "message"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return clip(strings.TrimSpace(s), 200)
		}
	}
	if inner, ok := m["error"].(map[string]any); ok {
		if s, ok := inner["message"].(string); ok {
			return clip(s, 200)
		}
	}
	return ""
}

// settled is how a decision ended, for the tool result the model reads, the
// stored action and the action log.
type settled struct {
	content    string // the tool result, wrapped as data
	state      string // ok, refused or error
	status     string // the pending action's final status
	outcome    gen.AssistantPendingOutcome
	httpStatus *int32
	target     *gen.AssistantPendingTarget
}

// perform runs a confirmed change through the dispatcher as the person and
// settles it. It never reports a change as made unless the router answered 2xx.
func (r *runner) perform(action store.AssistantPendingAction) settled {
	var req storedRequest
	if err := json.Unmarshal(action.Request, &req); err != nil {
		return failedSettlement("failed", "the stored request could not be read")
	}
	call := operations.CallRequest{OperationID: req.OperationID, PathParams: req.PathParams, Query: req.Query, Headers: req.Headers, Body: req.Body, Confirmed: true}
	res, err := r.tools.ops.Ops.Call(r.ctx, call)
	if err != nil {
		var oe *operations.Error
		if errors.As(err, &oe) {
			s := failedSettlement("refused", oe.Message)
			s.content = agentloop.Wrap(map[string]any{"changed": false, "error": oe.Code, "message": oe.Message})
			return s
		}
		return failedSettlement("failed", "the change failed unexpectedly")
	}
	status := int32(res.Status)
	s := settled{httpStatus: &status}
	s.outcome.Status = &status
	body := map[string]any{"status": res.Status}
	switch {
	case res.Status >= 200 && res.Status < 300:
		s.state, s.status = "ok", "confirmed"
		s.outcome.Ok = true
		body["changed"] = true
		if res.Truncated {
			body["truncated"] = true
		} else if b := agentloop.CompactBody(res.Body); b != nil {
			body["body"] = b
		}
		if created, ok := decodeObject(res.Body); ok {
			var info operations.Info
			if i, found := r.s.deps.Operations.Info(req.OperationID); found {
				info = i
			}
			target := &gen.AssistantPendingTarget{}
			if route := routeOfCreated(info.Path, created); route != "" {
				target.Route = &route
				if label := labelOf(created); label != "" {
					target.Label = &label
				}
				s.target = target
			}
		}
	case res.Status == 412 || (res.Status == 409 && isRevisionConflict(res.Body)):
		s.state, s.status = "refused", "failed"
		reason := "changed"
		s.outcome.Reason = &reason
		body["changed"] = false
		body["note"] = "the record changed since the person looked at the card, so nothing was written; tell them and offer to look at it again and propose the change again"
	default:
		s.state, s.status = "error", "failed"
		if res.Status == 401 || res.Status == 403 {
			s.state = "refused"
		}
		reason := "refused"
		if res.Status >= 500 {
			reason = "failed"
		}
		s.outcome.Reason = &reason
		if msg := problemText(res.Body); msg != "" {
			s.outcome.Message = &msg
		}
		body["changed"] = false
		if b := agentloop.CompactBody(res.Body); b != nil {
			body["body"] = b
		}
	}
	s.content = agentloop.Wrap(body)
	return s
}

// isRevisionConflict reports whether a 409 is the module saying the record
// moved on since it was read: its problem code names a revision or a version.
func isRevisionConflict(body json.RawMessage) bool {
	m, ok := decodeObject(body)
	if !ok {
		return false
	}
	code, _ := m["code"].(string)
	code = strings.ToLower(code)
	return strings.Contains(code, "revision") || strings.Contains(code, "version")
}

func failedSettlement(reason, message string) settled {
	s := settled{state: "error", status: "failed"}
	s.outcome.Reason = &reason
	s.outcome.Message = &message
	s.content = agentloop.Wrap(map[string]any{"changed": false, "error": reason, "message": message})
	return s
}

func cancelledSettlement() settled {
	reason := "cancelled"
	return settled{
		content: agentloop.Wrap(map[string]any{"changed": false, "cancelled": true, "note": "the person cancelled the change; nothing was written. Answer in one short line."}),
		state:   "ok", status: "cancelled", outcome: gen.AssistantPendingOutcome{Reason: &reason},
	}
}

// decide applies the person's decision at the head of a resumed run: it runs
// the change if they confirmed, records how it ended and drops the values of
// their request, replaces the waiting tool result with the real one, and tells
// the stream. The run then goes on with the model.
func (r *runner) decide(d decision) {
	var s settled
	started := time.Now()
	if d.confirm {
		s = r.perform(d.action)
	} else {
		s = cancelledSettlement()
	}
	elapsed := time.Since(started)

	outcomeJSON, _ := json.Marshal(s.outcome)
	var targetJSON []byte
	if s.target != nil {
		targetJSON, _ = json.Marshal(s.target)
	}
	q := store.New(r.s.deps.Pool)
	finished, err := q.FinishPendingAction(r.persist(), store.FinishPendingActionParams{
		ID: d.action.ID, Status: s.status, Outcome: outcomeJSON, Target: targetJSON, Now: r.s.deps.Clock(),
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.s.deps.Logger.Error("assistant: record a decision", "action", d.action.ID, "error", err)
	}
	if err == nil {
		r.queue.emit(eventConfirmation, r.s.cardOf(r.persist(), finished))
	}

	if d.confirm {
		var confirmedBy *uuid.UUID
		by := r.userID
		confirmedBy = &by
		if err := q.InsertAction(r.persist(), store.InsertActionParams{
			ID: uuid.New(), RunID: r.runID, OperationID: d.action.OperationID, Mode: string(operations.ModeConfirm), HttpStatus: s.httpStatus,
			DurationMs: int32(elapsed.Milliseconds()), Now: r.s.deps.Clock(), ConfirmedBy: confirmedBy,
		}); err != nil {
			r.s.deps.Logger.Error("assistant: log a confirmed change", "run", r.runID, "error", err)
		}
		r.s.countChange(r.persist(), r.userID, s.outcome.Ok)
	}

	// The waiting tool result becomes the real one, so the model's transcript
	// reads the change and its outcome where it asked for it.
	if d.action.ToolMessageID != nil {
		callID, name, status, content := d.action.CallID, toolCall, gen.AssistantPartStatus(s.state), s.content
		part := gen.AssistantPart{Type: gen.AssistantPartType("tool_result"), CallId: &callID, Name: &name, Status: &status, Content: &content}
		if err := q.UpdateMessageContent(r.persist(), store.UpdateMessageContentParams{ID: *d.action.ToolMessageID, Content: marshalParts([]gen.AssistantPart{part})}); err != nil {
			r.s.deps.Logger.Error("assistant: settle a tool result", "run", r.runID, "error", err)
		} else if msg, err := q.GetMessageByID(r.persist(), *d.action.ToolMessageID); err == nil {
			r.queue.emit(eventMessage, messageView(msg))
		}
	}
}

// ConfirmAssistantPendingAction (POST /api/v1/assistant/pending-actions/{id}/confirm)
func (s *server) ConfirmAssistantPendingAction(ctx context.Context, req gen.ConfirmAssistantPendingActionRequestObject) (gen.ConfirmAssistantPendingActionResponseObject, error) {
	stream, ref, err := s.decideAction(ctx, req.Id, true)
	if err != nil {
		return nil, err
	}
	if ref != nil {
		return ref, nil
	}
	return stream, nil
}

// CancelAssistantPendingAction (POST /api/v1/assistant/pending-actions/{id}/cancel)
func (s *server) CancelAssistantPendingAction(ctx context.Context, req gen.CancelAssistantPendingActionRequestObject) (gen.CancelAssistantPendingActionResponseObject, error) {
	stream, ref, err := s.decideAction(ctx, req.Id, false)
	if err != nil {
		return nil, err
	}
	if ref != nil {
		return ref, nil
	}
	return stream, nil
}

// decideAction is the person's decision on a card. It refuses before it
// streams: a card that is not theirs is not found; one already decided or
// expired is a conflict; the assistant being unavailable, or the Owner having
// narrowed the operation since, is a 403. Otherwise it claims the action once
// (so a double click, or two devices, decide it once), issues a grant from
// this request's own session and runs the rest on a detached runner: the
// change if they confirmed, then the model's answer, streamed.
func (s *server) decideAction(ctx context.Context, id uuid.UUID, confirm bool) (*streamResponse, *refusal, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return nil, r, err
	}
	if s.deps.AI == nil || s.deps.AssistantGrants == nil || s.deps.Operations == nil {
		r := refuse(503, codeAIUnavailable, "The assistant's model is not available")
		return nil, &r, nil
	}
	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	action, err := q.GetPendingAction(ctx, store.GetPendingActionParams{ID: id, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		r := notFound()
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: read the pending action: %w", err)
	}
	now := s.deps.Clock()
	switch {
	case action.Status == "expired" || (action.Status == "pending" && !action.ExpiresAt.After(now)):
		r := refuse(409, codeActionExpired, "This change waited too long and has expired")
		return nil, &r, nil
	case action.Status != "pending":
		r := refuse(409, codeActionDecided, "This change has already been decided")
		return nil, &r, nil
	}
	if action.Kind == kindBulk {
		return s.decideBulk(ctx, action, confirm)
	}
	conv, err := q.GetConversation(ctx, store.GetConversationParams{ID: action.ConversationID, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		r := notFound()
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: read the conversation: %w", err)
	}
	if confirm {
		// The Owner may have narrowed what the assistant may use while the card
		// waited; a change the gate now refuses is not made.
		if mode, ok := s.deps.Operations.Effective(ctx, action.OperationID); !ok || mode != operations.ModeConfirm {
			r := refuse(403, codeNotAllowed, "The assistant may no longer make this change")
			return nil, &r, nil
		}
		// A card of a conversation with an agent is the person's decision on a
		// change the agent may make too: the agent being paused, the person losing
		// agents:view or the agent's level for it being lowered since stops it (V31).
		if conv.AgentID != nil {
			if r := s.agentMayStillChange(ctx, *conv.AgentID, action.OperationID); r != nil {
				return nil, r, nil
			}
		}
	}

	// The grant is from this request's session; the claim follows, and a claim
	// lost to another decision gives it back.
	grantID, err := s.deps.AssistantGrants.Issue(ctx, conv.ID, action.RunID, grantTTL)
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: issue the run's grant: %w", err)
	}
	status := "cancelled"
	if confirm {
		status = "confirmed"
	}
	var claimed store.AssistantPendingAction
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tq := store.New(tx)
		var err error
		if claimed, err = tq.DecidePendingAction(ctx, store.DecidePendingActionParams{ID: id, UserID: user, Status: status, Now: now}); err != nil {
			return err
		}
		_, err = tq.ResumeRun(ctx, store.ResumeRunParams{ID: action.RunID, UserID: user, Now: now})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		revoke, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.deps.AssistantGrants.Revoke(revoke, grantID)
		r := refuse(409, codeActionDecided, "This change has already been decided")
		return nil, &r, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("assistant: claim the pending action: %w", err)
	}

	return s.resumeDecided(ctx, conv, claimed, grantID, confirm)
}

// resumeDecided carries on a run whose action the person has just decided, on a
// detached runner: the change if they confirmed, then the model's answer,
// streamed.
func (s *server) resumeDecided(ctx context.Context, conv store.GetConversationRow, claimed store.AssistantPendingAction, grantID uuid.UUID, confirm bool) (*streamResponse, *refusal, error) {
	user := callerID(ctx)
	lang, name := s.person(ctx, user)
	panel := s.panelAgentOf(ctx, conv.AgentID)
	runCtx, cancel := context.WithCancelCause(context.Background())
	runCtx, stopTimer := context.WithTimeoutCause(runCtx, runCeiling, errors.New("assistant: the run reached its ceiling"))
	s.runs.add(claimed.RunID, conv.ID, func(cause error) { cancel(cause) })
	runCtx = withGrant(runCtx, user, grantID)

	queue := newEventQueue()
	queue.emit(eventRun, map[string]any{"runId": claimed.RunID})
	queue.emit(eventConfirmation, s.cardOf(context.WithoutCancel(ctx), claimed))

	r := &runner{
		s: s, ctx: runCtx, runID: claimed.RunID, convID: conv.ID, userID: user, grantID: grantID, queue: queue, lang: lang, name: name,
		agent:  panel,
		tools:  s.newToolbox(runCtx, user, lang, panel),
		resume: &decision{action: claimed, confirm: confirm},
	}
	go func() {
		defer stopTimer()
		defer cancel(nil)
		defer s.runs.remove(claimed.RunID)
		r.execute()
	}()
	return &streamResponse{ctx: ctx, queue: queue}, nil, nil
}
