package agents

// start_conversation (virtual employees design V29): an agent writes first, to a
// contact of the run's customer, on one of its role's own channels. It goes
// through the same pipeline as a reply: the verdict (V17), the hard rules (V18),
// the mode (the role's start mode, V19), the disclosure (V11) and, in
// Communications, the marking. The model names the channel and the address, and
// the code checks both: the channel must be one the role is bound to, and the
// address a contact of the run's customer (S1), whoever asked for it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// storedStart is the item of a conversation the agent would start.
type storedStart struct {
	Text       string    `json:"text"`
	AgentText  string    `json:"agentText,omitempty"`
	Disclosure string    `json:"disclosure"`
	ChannelID  uuid.UUID `json:"channelId"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
}

const maxStartSubject = 200

func startResult(status string, v map[string]any) agentloop.Result {
	ok := "ok"
	if status != "ok" {
		ok = "refused"
	}
	return agentloop.Result{Content: agentloop.Wrap(v), Status: ok, Mode: "tool", Operation: "tool:" + toolStartConversation}
}

func (rs *runState) startTool() agentloop.Tool {
	return agentloop.Tool{
		Name: toolStartConversation,
		Description: "Write first to a contact of this customer: start a new conversation by e-mail. channelId must be one of your channels (listed in the data you were given), " +
			"and to must be the e-mail address of one of the customer's contacts (read them with getCustomersByIdContacts first). It is checked against the rules before anything is sent, and a person may review it first. " +
			"You may start one conversation in a run. Give the sources you read and your honest score.",
		Schema: schema([]string{"channelId", "to", "subject", "text", "sources", "score"}, map[string]any{
			"channelId": text("One of your channels."),
			"to":        text("A contact's e-mail address."),
			"subject":   text("The subject line."),
			"text":      text("The message, in the customer's language. No line about being an AI: the system adds it."),
			"sources":   sourcesProp,
			"score":     scoreProp,
		}),
		Call: func(ctx context.Context, raw json.RawMessage) (agentloop.Result, error) {
			return rs.start(ctx, raw), nil
		},
	}
}

// ensureContacts reads the run's customer's contacts once.
func (rs *runState) ensureContacts(ctx context.Context) bool {
	if rs.contacts != nil {
		return true
	}
	if rs.customerID == nil {
		return false
	}
	res, err := rs.call(ctx, operations.CallRequest{OperationID: opContacts, PathParams: map[string]any{"id": *rs.customerID}})
	if err != nil || res.Status != 200 {
		return false
	}
	set := parseContacts(res.Body)
	rs.contacts = &set
	return true
}

// roleHasChannel says the role is bound to the channel.
func (rs *runState) roleHasChannel(ctx context.Context, channelID uuid.UUID) bool {
	return s2roleHasChannel(ctx, rs.s, rs.role.ID, channelID)
}

func s2roleHasChannel(ctx context.Context, s *server, roleID, channelID uuid.UUID) bool {
	rows, err := store.New(s.deps.Pool).ListChannelsOfRoles(ctx, []uuid.UUID{roleID})
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.ChannelID == channelID {
			return true
		}
	}
	return false
}

func (rs *runState) start(ctx context.Context, raw json.RawMessage) agentloop.Result {
	var a struct {
		ChannelID string   `json:"channelId"`
		To        string   `json:"to"`
		Subject   string   `json:"subject"`
		Text      string   `json:"text"`
		Sources   []string `json:"sources"`
		Score     *int     `json:"score"`
	}
	if err := agentloop.ToolArgs(raw, &a); err != nil || a.Score == nil || a.Sources == nil {
		return agentloop.Refused("invalid_arguments", "channelId, to, subject, text, sources and score are required")
	}
	rs.mu.Lock()
	replied := rs.replied
	rs.mu.Unlock()
	if replied {
		return agentloop.Refused("already_started", "you have started one conversation in this run")
	}
	body, subject := strings.TrimSpace(a.Text), strings.TrimSpace(a.Subject)
	if body == "" || subject == "" {
		return agentloop.Refused("invalid_arguments", "subject and text are required")
	}
	if utf8.RuneCountInString(subject) > maxStartSubject {
		return agentloop.Refused("invalid_arguments", fmt.Sprintf("the subject is at most %d characters", maxStartSubject))
	}
	channelID, err := uuid.Parse(strings.TrimSpace(a.ChannelID))
	if err != nil {
		return agentloop.Refused("invalid_arguments", "channelId must be one of your channels")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(a.To))
	if err != nil || addr.Address != strings.TrimSpace(a.To) {
		return agentloop.Refused("invalid_arguments", "to must be one e-mail address")
	}
	to := strings.ToLower(addr.Address)

	// The channel is the role's own, and the address a contact of the run's
	// customer: both are checked here, in code, whatever the model was told.
	if !rs.roleHasChannel(ctx, channelID) {
		return agentloop.Refused("channel_not_yours", "that is not one of your channels")
	}
	if rs.customerID == nil {
		return agentloop.Refused("no_customer", "this work has no customer, so you cannot write to anyone")
	}
	if !rs.ensureContacts(ctx) || !rs.contacts.includes(to, nil) {
		return agentloop.Refused("not_a_contact", "that address is not an e-mail address of one of the customer's contacts")
	}
	q := store.New(rs.s.deps.Pool)
	if isChannel, err := q.IsChannelAddress(ctx, to); err == nil && isChannel {
		return agentloop.Refused("not_a_contact", "that address is a channel of this installation")
	}

	now := rs.s.deps.Clock()
	// S7: a customer is written to first at most max-replies-per-day times a day by a role.
	started, _ := q.CountStartsSince(ctx, store.CountStartsSinceParams{RoleID: rs.role.ID, CustomerID: rs.customerID, Since: now.Add(-24 * time.Hour)})
	if int(started) >= int(rs.role.MaxRepliesPerDay) {
		return agentloop.Refused("already_followed_up", "this customer has already been written to the most times a day the company allows; end your work")
	}

	lang := disclosureLanguage(rs.agent.Languages, body)
	line := disclosureLine(rs.agent.DisclosureWording, lang, rs.agent.Name, rs.company)
	full := withDisclosure(line, body)

	log := rs.actionLog(ctx)
	valid, _ := validSources(a.Sources, log)
	score := scoreOf(*a.Score, valid)
	chk := rs.s.contentCheck(ctx, rs.agent.ID, rs.role.Instructions, full)
	in := ruleInput{
		Mode: rs.role.StartMode, Threshold: int(rs.role.Threshold), MaxRepliesPerDay: int(rs.role.MaxRepliesPerDay),
		Score: score, CarriesCustomerData: runHasScopedRead(log), Check: chk,
		Facts: replyFacts{Recipient: to, HasCustomer: true, RecipientIsContact: true},
	}
	dec := decideReply(in)
	rs.recordVerdict(ctx, in, dec, score, *a.Score, valid)

	st := storedStart{Text: body, Disclosure: line, ChannelID: channelID, To: to, Subject: subject}
	item, _ := json.Marshal(st)
	rs.conv.ChannelID = channelID // the item belongs to the channel it would be sent on
	if dec.Send {
		return rs.sendStart(ctx, st, full, item, score, valid, *a.Score, dec.Held)
	}
	return rs.queueStart(ctx, item, score, valid, *a.Score, dec.Held)
}

// startKey makes a run's start idempotent per role, subject, address and day: a
// run that is retried the same day starts the conversation once.
func (rs *runState) startKey(to string) string {
	day := rs.run.StartedAt.UTC().Format(time.DateOnly)
	return fmt.Sprintf("agents-start-%s-%s-%s-%s-%s", rs.role.ID, rs.run.SubjectType, rs.run.SubjectID, day, to)
}

// startRequest is the call that starts the conversation through Communications as
// the agent: from the channel, to the one address, linked to the customer.
func startRequest(st storedStart, full string, customerID *int32, key string) operations.CallRequest {
	payload := map[string]any{
		"channelId": st.ChannelID, "subject": st.Subject, "textBody": full,
		"to": []map[string]string{{"email": st.To}}, "customerId": customerID,
	}
	body, _ := json.Marshal(payload)
	return operations.CallRequest{OperationID: opStart, Body: body, Headers: map[string]string{"Idempotency-Key": key}}
}

// queueStart leaves the conversation for a person to approve.
func (rs *runState) queueStart(ctx context.Context, item json.RawMessage, score int, valid []string, modelScore int, held []string) agentloop.Result {
	id, err := rs.insertItem(ctx, "start", item, score, valid, modelScore, held, "waiting", rs.s.deps.Clock().Add(24*time.Hour))
	if err != nil {
		return agentloop.Result{Content: agentloop.Wrap(map[string]string{"error": "call_failed", "message": "the conversation could not be queued"}), Status: "error", Mode: "tool", Operation: "tool:" + toolStartConversation}
	}
	rs.mu.Lock()
	rs.replied = true
	rs.items++
	rs.mu.Unlock()
	rs.reviewNote(ctx, id, "a conversation to start")
	return startResult("ok", map[string]any{"status": "waiting_for_review", "rules": held, "message": "the conversation is waiting for a person to review it; it has not been started"})
}

// sendStart starts the conversation now.
func (rs *runState) sendStart(ctx context.Context, st storedStart, full string, item json.RawMessage, score int, valid []string, modelScore int, held []string) agentloop.Result {
	if !rs.stillMine(ctx) {
		return agentloop.Refused("run_stopped", "this run was stopped; nothing was sent")
	}
	res, err := rs.call(ctx, startRequest(st, full, rs.customerID, rs.startKey(st.To)))
	if err != nil {
		var oe *operations.Error
		if asOpError(err, &oe) {
			rs.s.warn("agents: the start was refused before it was sent", "run", rs.run.ID, "err", err)
			return rs.queueStart(ctx, item, score, valid, modelScore, []string{"SEND"})
		}
		// A timeout may have queued the message: a person is told instead of a second try.
		rs.s.warn("agents: the outcome of a start is unknown", "run", rs.run.ID, "err", err)
		rs.mu.Lock()
		rs.handOff = &handOffRequest{code: handUnknownSend, summary: "The conversation to " + st.To + " may or may not have been started: the call did not answer. Check the customer's conversations before writing again."}
		rs.mu.Unlock()
		return startResult("refused", map[string]any{"status": "unknown", "message": "it is not known whether the conversation was started; a colleague is told"})
	}
	code := errorCodeOf(res.Body)
	switch {
	case res.Status >= 200 && res.Status < 300:
		now := rs.s.deps.Clock()
		rs.mu.Lock()
		rs.replied, rs.sent, rs.sentAt = true, true, now
		rs.mu.Unlock()
		_ = store.New(rs.s.deps.Pool).AddUsage(context.WithoutCancel(ctx), store.AddUsageParams{AgentID: rs.agent.ID, Day: rs.s.day(now), Sent: 1})
		return startResult("ok", map[string]any{"status": "started", "message": "the conversation was started"})
	case res.Status >= 500:
		rs.mu.Lock()
		rs.handOff = &handOffRequest{code: handUnknownSend, summary: "The conversation to " + st.To + " may or may not have been started: the server failed. Check the customer's conversations before writing again."}
		rs.mu.Unlock()
		return startResult("refused", map[string]any{"status": "unknown", "message": "it is not known whether the conversation was started; a colleague is told"})
	case res.Status == 400 || res.Status == 422:
		return agentloop.Refused(code, "the channel refused the message ("+code+"); change it and try again")
	}
	rs.s.warn("agents: the start was refused", "run", rs.run.ID, "status", res.Status, "code", code)
	return rs.queueStart(ctx, item, score, valid, modelScore, []string{"SEND"})
}
