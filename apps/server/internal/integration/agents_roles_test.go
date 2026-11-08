package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The roles of delivery C end to end (virtual employees design V29, V30): a quote
// sent in the real Orders wakes the sales follow-up, which starts a conversation
// through the real Communications under the real gate; the timesheet reminders
// and the project digest write to a colleague through the host's notifications.
// The model is scripted; everything it reads and writes is real.

var rolesRecorder = mergedRecorder("agents", modCustomers, modInvoices, modProducts, modOrders, "communications", "knowledge")

// salesRig is a front desk rig with Orders composed, and a second role, the sales
// follow-up, on a channel of its own.
type salesRig struct {
	*runRig
	salesRole    uuid.UUID
	salesChannel uuid.UUID
	salesAddress string
	salesMailbox string
	customer     int32
	contact      string
	seller       *modtest.Client
}

func newSalesRig(t *testing.T, startMode string, turns ...modtest.ChatTurn) *salesRig {
	t.Helper()
	r := newRunRigWith(t, "review", []modtest.Option{
		modtest.WithRecorder(rolesRecorder),
		modtest.WithModule(moduleNamed(t, modOrders)),
	}, turns...)
	s := &salesRig{runRig: r, contact: "kari@example.test"}
	s.customer = r.customer(t, "Acme AS", s.contact)

	okJSON(t, r.owner, http.MethodPost, "/api/v1/agents/agents/"+r.agentID.String()+"/roles", map[string]any{"template": "sales_followup", "name": "Follow-up", "instructions": ""}, nil)
	var ag struct {
		Roles []struct {
			ID       string `json:"id"`
			Template string `json:"template"`
			Version  int    `json:"version"`
		} `json:"roles"`
	}
	okJSON(t, r.owner, http.MethodGet, "/api/v1/agents/agents/"+r.agentID.String(), nil, &ag)
	var version int
	for _, role := range ag.Roles {
		if role.Template == "sales_followup" {
			s.salesRole, version = uuid.MustParse(role.ID), role.Version
		}
	}

	s.salesAddress = fmt.Sprintf("sales-%s@example.test", uuid.NewString()[:8])
	mailbox := contracts.ChannelMailboxInfo{Mailbox: uuid.NewString(), Address: s.salesAddress, DisplayName: "Salg", Shared: true}
	s.salesMailbox = mailbox.Mailbox
	r.fake.SetMailbox(s.salesAddress, mailbox)
	var ch struct {
		ID string `json:"id"`
	}
	okJSON(t, r.staff, http.MethodPost, "/api/v1/communications/channels", map[string]any{"type": "email", "provider": "entra", "address": s.salesAddress}, &ch)
	s.salesChannel = uuid.MustParse(ch.ID)
	okJSON(t, r.owner, http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/roles/%s/channels", r.agentID, s.salesRole), map[string]any{"channelIds": []string{ch.ID}}, nil)
	body := map[string]any{
		"name": "Follow-up", "instructions": "Follow up the quote with its contact.", "replyMode": "review", "threshold": 85, "startMode": startMode,
		"capsToolCalls": 30, "capsTurns": 8, "maxRepliesPerDay": 5, "handOffUserId": nil, "waitDays": 7,
	}
	if resp := r.owner.Do(http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/roles/%s", r.agentID, s.salesRole), body, modtest.Header("If-Match", fmt.Sprint(version))); resp.Status != http.StatusOK {
		t.Fatalf("set the sales role: %d %s", resp.Status, resp.Body)
	}

	// The levels the second role needs, and the Identity role that holds them.
	a := &agentsInstall{h: r.h, owner: r.owner}
	var keys []string
	for key, level := range a.needs(t, r.agentID) {
		a.level(t, r.agentID, key, level)
		keys = append(keys, key)
	}
	a.roles(t, r.userID, keys...)

	s.seller = r.h.SignIn(t, append([]string{"customers:view", "invoices:access", "invoices:manage", "orders:quotes-manage"}, orderPermissions...)...)
	return s
}

// quote makes a quote for the customer with one line and marks it sent, which
// publishes orders.quote.sent. It answers the quote's id.
func (s *salesRig) quote(t *testing.T, customer int32) int64 {
	t.Helper()
	standard := taxCategory(t, s.seller, "Standard "+uuid.NewString()[:4], 0.25, 1)
	variant := pricedVariant(t, s.seller, standard, "Hylle", "HYL-"+uuid.NewString()[:6], 1249.50)
	var q struct {
		Id        int64     `json:"id"`
		Revision  int32     `json:"revision"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	okJSON(t, s.seller, http.MethodPost, "/api/v1/orders/quotes", map[string]any{
		"clientRequestId": uuid.NewString(), "customerId": customer,
		"lines": []any{map[string]any{"kind": "product", "variantId": variant, "quantity": 1}},
	}, &q)
	okJSON(t, s.seller, http.MethodPost, fmt.Sprintf("/api/v1/orders/quotes/%d/mark-sent", q.Id), map[string]any{
		"expectedUpdatedAt": q.UpdatedAt, "revision": q.Revision,
	}, nil)
	return q.Id
}

// deliver drains the outbox to the agents' handlers.
func (s *salesRig) deliver(t *testing.T) {
	t.Helper()
	d := events.NewDispatcher(s.h.Pool(), agents.Module().EventHandlers(s.h.Deps()), s.h.Deps().Logger, s.h.Now)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatalf("deliver the events: %v", err)
	}
}

// awaitSubjectRun lets the wakeup come due and waits for the run it makes to end.
func (s *runRig) awaitSubjectRun(t *testing.T, subjectType string) string {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if err := agents.WakeupsCycle(s.h.Deps()); err != nil {
			t.Fatalf("wakeups: %v", err)
		}
		var state string
		err := s.h.Pool().QueryRow(context.Background(),
			`SELECT state FROM agents.runs WHERE subject_type = $1 ORDER BY started_at DESC LIMIT 1`, subjectType).Scan(&state)
		if err == nil && state != "running" && state != "queued" {
			return state
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("the run about a %s never ended; log:\n%s", subjectType, s.h.Logs())
	return ""
}

func startCall(channel uuid.UUID, to, text string, sources []string, score int) modtest.ChatToolCall {
	return toolCall("start_conversation", map[string]any{
		"channelId": channel.String(), "to": to, "subject": "Oppfølging av tilbud", "text": text, "sources": sources, "score": score,
	})
}

// startItems are the review items of kind start.
func (s *salesRig) startItems(t *testing.T) []reviewItem {
	t.Helper()
	rows, err := s.h.Pool().Query(context.Background(), `SELECT state, kind, score, model_score, held_by, item FROM agents.review_items WHERE kind = 'start' ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []reviewItem
	for rows.Next() {
		var it reviewItem
		var item []byte
		if err := rows.Scan(&it.State, &it.Kind, &it.Score, &it.ModelScore, &it.HeldBy, &item); err != nil {
			t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

// TestSalesFollowup_AQuoteSentWakesTheRoleAfterItsWaitAndAutoStartsTheConversation:
// quote sent, a week on, the role reads the quote and the contacts, and starts one
// conversation to the contact on its own channel, marked as the agent's, with the
// disclosure on top.
func TestSalesFollowup_AQuoteSentWakesTheRoleAfterItsWaitAndAutoStartsTheConversation(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "auto")
	quote := s.quote(t, s.customer)
	s.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getQuote", map[string]any{"id": quote})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getCustomersByIdContacts", map[string]any{"id": s.customer})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, s.contact, "Hei Kari! Har du noen spørsmål om tilbudet?",
			[]string{fmt.Sprintf("getQuote %d", quote)}, 90)}},
		modtest.ChatTurn{Text: "Ferdig."},
	)
	s.deliver(t)
	if n := s.h.Count(t, `SELECT count(*) FROM agents.wakeups WHERE role_id = $1 AND subject_type = 'quote'`, s.salesRole); n != 1 {
		t.Fatalf("%d wakeups after the quote was sent, want one", n)
	}
	if got := s.h.Count(t, `SELECT count(*) FROM agents.runs WHERE subject_type = 'quote'`); got != 0 {
		t.Fatalf("a run started the moment the quote was sent")
	}

	s.h.Advance(7*24*time.Hour + time.Minute)
	if state := s.awaitSubjectRun(t, "quote"); state != "done" {
		t.Fatalf("run state = %s, want done; log:\n%s", state, s.h.Logs())
	}
	sent := s.sent(t)
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d, want the one follow-up", len(sent))
	}
	mime := string(sent[0].MIME)
	if !strings.Contains(mime, s.contact) || !strings.Contains(mime, "X-Vantigo-Agent: "+s.agentID.String()+"; ai-generated") {
		t.Errorf("the follow-up is not to the contact, or not marked as the agent's:\n%s", mime)
	}
	if body := decodedBody(t, sent[0].MIME); !strings.Contains(body, "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).\n\nHei Kari!") {
		t.Errorf("the disclosure is not at the top:\n%s", body)
	}
	if got := modtest.One[*uuid.UUID](t, s.h, `SELECT agent_id FROM communications.conversation_messages WHERE direction = 'outbound' AND subject = 'Oppfølging av tilbud'`); got == nil || *got != s.agentID {
		t.Errorf("the message's agent = %v, want the agent", got)
	}
	if got := modtest.One[*int32](t, s.h, `SELECT customer_id FROM communications.conversations WHERE subject = 'Oppfølging av tilbud'`); got == nil || *got != s.customer {
		t.Errorf("the conversation's customer = %v, want %d", got, s.customer)
	}
	if n := s.h.Count(t, `SELECT count(*) FROM agents.handed_off`); n != 0 {
		t.Errorf("%d hand-off markers for a run about a quote", n)
	}
}

// reviewer is someone who may see and decide what the sales follow-up leaves.
func (s *salesRig) reviewer(t *testing.T) *modtest.Client {
	t.Helper()
	return s.h.SignIn(t, append([]string{
		"agents:view", "agents:review", "communications:conversations-view", "communications:conversations-reply",
		"customers:view", "customers:contacts-view", "customers:associations-view", "knowledge:articles",
	}, orderPermissions...)...)
}

// TestSalesFollowup_StartConversationIsHeldToTheContactsAndTheRolesOwnChannels is
// S1 and the channel rule for a conversation the agent starts: an address that is
// no contact of the run's customer, and a channel that is not the role's, are
// refused whatever the model asks; in Review the right one waits, and a reviewer's
// approval is what sends it.
func TestSalesFollowup_StartConversationIsHeldToTheContactsAndTheRolesOwnChannels(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "review")
	quote := s.quote(t, s.customer)
	read := fmt.Sprintf("getQuote %d", quote)
	text := "Hei Kari! Har du noen spørsmål om tilbudet?"
	s.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getQuote", map[string]any{"id": quote})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getCustomersByIdContacts", map[string]any{"id": s.customer})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, "attacker@evil.test", text, []string{read}, 90)}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.channel, s.contact, text, []string{read}, 90)}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, s.contact, text, []string{read}, 90)}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, s.contact, "En til.", []string{read}, 90)}},
		modtest.ChatTurn{Text: "Ferdig."},
	)
	s.deliver(t)
	s.h.Advance(7*24*time.Hour + time.Minute)
	if state := s.awaitSubjectRun(t, "quote"); state != "waiting_review" {
		t.Fatalf("run state = %s, want waiting_review; log:\n%s", state, s.h.Logs())
	}
	if len(s.sent(t)) != 0 {
		t.Fatal("a conversation was started without a reviewer")
	}
	items := s.startItems(t)
	if len(items) != 1 || items[0].State != "waiting" || items[0].Score != 90 {
		t.Fatalf("start items = %+v, want the one valid start waiting (the second was refused: one a run)", items)
	}
	var results []string
	rows, err := s.h.Pool().Query(context.Background(), `SELECT t.text FROM agents.run_messages t JOIN agents.runs r ON r.id = t.run_id WHERE r.subject_type = 'quote' AND t.role = 'tool' ORDER BY t.id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var txt string
		_ = rows.Scan(&txt)
		results = append(results, txt)
	}
	rows.Close()
	all := strings.Join(results, "\n")
	for _, code := range []string{"not_a_contact", "channel_not_yours", "already_started"} {
		if !strings.Contains(all, code) {
			t.Errorf("the tool results do not hold %q:\n%s", code, all)
		}
	}

	// The reviewer approves: the conversation starts, from the role's channel.
	var itemID uuid.UUID
	var version int
	if err := s.h.Pool().QueryRow(context.Background(), `SELECT id, version FROM agents.review_items WHERE kind = 'start'`).Scan(&itemID, &version); err != nil {
		t.Fatal(err)
	}
	rev := s.reviewer(t)
	resp := rev.Do(http.MethodPost, "/api/v1/agents/review/"+itemID.String()+"/approve", nil, modtest.Header("If-Match", fmt.Sprint(version)))
	if resp.Status != http.StatusOK {
		t.Fatalf("approve: %d %s", resp.Status, resp.Body)
	}
	sent := s.sent(t)
	if len(sent) != 1 || sent[0].Mailbox != s.salesMailbox || !strings.Contains(string(sent[0].MIME), s.contact) {
		t.Fatalf("sent = %d messages %+v, want the one start from the sales channel to the contact", len(sent), sent)
	}
	if body := decodedBody(t, sent[0].MIME); !strings.Contains(body, "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).\n\nHei Kari!") {
		t.Errorf("the approved start lacks the disclosure at the top:\n%s", body)
	}
	// A second approval is one send (the key is the item's).
	if resp := rev.Do(http.MethodPost, "/api/v1/agents/review/"+itemID.String()+"/approve", nil, modtest.Header("If-Match", fmt.Sprint(version))); resp.Status >= 500 {
		t.Errorf("a second approval = %d %s", resp.Status, resp.Body)
	}
	if n := s.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE direction = 'outbound' AND subject = 'Oppfølging av tilbud'`); n != 1 {
		t.Errorf("%d messages in the conversation started, want one", n)
	}
}

// TestSalesFollowup_AutoAboveSendsFromTheThresholdAndHoldsBelow is the mode for
// "start a conversation": the same threshold and score as replies.
func TestSalesFollowup_AutoAboveSendsFromTheThresholdAndHoldsBelow(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "auto_above")
	quote := s.quote(t, s.customer)
	read := fmt.Sprintf("getQuote %d", quote)
	s.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getQuote", map[string]any{"id": quote})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getCustomersByIdContacts", map[string]any{"id": s.customer})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, s.contact, "Hei Kari!", []string{read}, 60)}},
		modtest.ChatTurn{Text: "Ferdig."},
	)
	s.deliver(t)
	s.h.Advance(7*24*time.Hour + time.Minute)
	if state := s.awaitSubjectRun(t, "quote"); state != "waiting_review" {
		t.Fatalf("run state = %s, want waiting_review for a score under the threshold; log:\n%s", state, s.h.Logs())
	}
	if len(s.sent(t)) != 0 || len(s.startItems(t)) != 1 {
		t.Errorf("a start scored 60 under a threshold of 85: sent %d, items %d", len(s.sent(t)), len(s.startItems(t)))
	}
}

// TestSalesFollowup_ARunWithNoSourceHasNoScore: a start built on nothing the run
// read scores 0 and is held in Review and Auto above X %.
func TestSalesFollowup_ARunWithNoSourceHasNoScoreAndIsHeld(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "auto_above")
	quote := s.quote(t, s.customer)
	s.script.setTurns(
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{readOp("getCustomersByIdContacts", map[string]any{"id": s.customer})}},
		modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{startCall(s.salesChannel, s.contact, "Hei Kari!", []string{fmt.Sprintf("getQuote %d", quote)}, 99)}},
		modtest.ChatTurn{Text: "Ferdig."},
	)
	s.deliver(t)
	s.h.Advance(7*24*time.Hour + time.Minute)
	if state := s.awaitSubjectRun(t, "quote"); state != "waiting_review" {
		t.Fatalf("run state = %s, want waiting_review; log:\n%s", state, s.h.Logs())
	}
	items := s.startItems(t)
	if len(items) != 1 || items[0].Score != 0 || items[0].ModelScore != 99 {
		t.Errorf("items = %+v, want a start scored 0 (the model said 99, but read no quote)", items)
	}
}

// TestSalesFollowup_AQuoteAcceptedBeforeTheWaitStartsNothing: accepting the quote
// takes the wakeup away.
func TestSalesFollowup_AQuoteAcceptedBeforeTheWaitIsNeverFollowedUp(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "auto")
	quote := s.quote(t, s.customer)
	s.deliver(t)
	if err := publishOnOutbox(t, s.h, contracts.QuoteAccepted{QuoteID: quote, Number: 1, Revision: 1, CustomerID: s.customer, AcceptedIntoKind: "order", AcceptedIntoID: 1, AcceptedAt: s.h.Now()}); err != nil {
		t.Fatal(err)
	}
	s.deliver(t)
	s.h.Advance(8 * 24 * time.Hour)
	if err := agents.WakeupsCycle(s.h.Deps()); err != nil {
		t.Fatal(err)
	}
	if n := s.h.Count(t, `SELECT count(*) FROM agents.run_queue WHERE subject_type = 'quote'`); n != 0 {
		t.Errorf("%d run requests for a quote that was accepted", n)
	}
}

// publishOnOutbox puts an event on the outbox the way a module's transaction does.
func publishOnOutbox(t *testing.T, h *modtest.Harness, ev contracts.Event) error {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	if err := events.Publish(ctx, tx, "test", ev, h.Now()); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// TestQuoteScope_AnAgentReadsOnlyItsRunsCustomersQuotes is the scope mark of the
// two quote reads the sales follow-up has: getQuote and listQuotes carry
// x-vantigo-customer-scope, and a run with no customer reads none.
func TestQuoteScope_AnAgentReadsOnlyItsRunsCustomersQuotes(t *testing.T) {
	t.Parallel()
	s := newSalesRig(t, "review")
	other := s.runRig.customer(t, "Elv Sør AS", "per@example.test")
	own, theirs := s.quote(t, s.customer), s.quote(t, other)

	ops := s.h.Operations(t)
	ops.SetGate(contracts.GrantAgent, func(_ context.Context, info operations.Info) operations.Mode { return info.Mode })
	agent, _ := s.h.AgentContext(t, &s.customer, "orders:access")

	if res, err := ops.Call(agent, operations.CallRequest{OperationID: "getQuote", PathParams: map[string]any{"id": own}}); err != nil || res.Status != http.StatusOK {
		t.Errorf("getQuote of the run's customer: %v %d %s", err, res.Status, res.Body)
	}
	res, err := ops.Call(agent, operations.CallRequest{OperationID: "getQuote", PathParams: map[string]any{"id": theirs}})
	if !errorsIsScope(err) || len(res.Body) != 0 {
		t.Errorf("getQuote of another customer = %+v, %v, want customer_scope and no body", res, err)
	}
	if _, err := ops.Call(agent, operations.CallRequest{OperationID: "listQuotes", Query: map[string]any{"customerId": other}}); !errorsIsScope(err) {
		t.Errorf("listQuotes for another customer = %v, want customer_scope", err)
	}
	none, _ := s.h.AgentContext(t, nil, "orders:access")
	if _, err := ops.Call(none, operations.CallRequest{OperationID: "getQuote", PathParams: map[string]any{"id": own}}); !errorsIsScope(err) {
		t.Errorf("getQuote in a run with no customer = %v, want customer_scope", err)
	}
}

func errorsIsScope(err error) bool { return errors.Is(err, operations.ErrCustomerScope) }

// colleagueRig is the front desk rig with a timesheet reminder role, a project
// digest role, and a colleague who can be mailed.
type colleagueRig struct {
	*runRig
	reminders uuid.UUID
	digest    uuid.UUID
	colleague uuid.UUID
	email     string
}

func newColleagueRig(t *testing.T, turns ...modtest.ChatTurn) *colleagueRig {
	t.Helper()
	r := newRunRig(t, "review", turns...)
	c := &colleagueRig{runRig: r}
	_, c.colleague = r.h.SignInUser(t)
	c.email = modtest.One[string](t, r.h, `SELECT email FROM identity.users WHERE id = $1`, c.colleague)
	for _, tpl := range []string{"timesheet_reminders", "project_digest"} {
		okJSON(t, r.owner, http.MethodPost, "/api/v1/agents/agents/"+r.agentID.String()+"/roles", map[string]any{"template": tpl, "name": tpl, "instructions": ""}, nil)
	}
	var ag struct {
		Roles []struct {
			ID       string `json:"id"`
			Template string `json:"template"`
			Version  int    `json:"version"`
		} `json:"roles"`
	}
	okJSON(t, r.owner, http.MethodGet, "/api/v1/agents/agents/"+r.agentID.String(), nil, &ag)
	for _, role := range ag.Roles {
		switch role.Template {
		case "timesheet_reminders":
			c.reminders = uuid.MustParse(role.ID)
		case "project_digest":
			c.digest = uuid.MustParse(role.ID)
			resp := r.owner.Do(http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/roles/%s", r.agentID, role.ID), map[string]any{
				"name": "Digest", "instructions": "Write the digest.", "replyMode": "review", "threshold": 85, "capsToolCalls": 30, "capsTurns": 8,
				"maxRepliesPerDay": 5, "handOffUserId": nil, "recipientUserId": c.colleague.String(),
				"schedule": map[string]any{"kind": "weekly", "day": 1, "time": "08:00"},
			}, modtest.Header("If-Match", fmt.Sprint(role.Version)))
			if resp.Status != http.StatusOK {
				t.Fatalf("set the digest role: %d %s", resp.Status, resp.Body)
			}
		}
	}
	return c
}

func (c *colleagueRig) mails(t *testing.T) []mail.Message {
	t.Helper()
	fake, ok := c.h.Deps().Mail.(*mail.Fake)
	if !ok {
		t.Fatal("the installation's mail is not the fake")
	}
	return fake.Messages()
}

func notifyTurn(title, text string) modtest.ChatTurn {
	return modtest.ChatTurn{ToolCalls: []modtest.ChatToolCall{toolCall("notify", map[string]any{"title": title, "text": text})}}
}

// TestTimesheetReminders_AnUnsubmittedWeekMakesOneNoticeToThatPersonAlone: the
// event names the person, and the model names no recipient and reads nothing.
func TestTimesheetReminders_AnUnsubmittedWeekMakesOneNoticeToThatPersonAlone(t *testing.T) {
	t.Parallel()
	c := newColleagueRig(t, notifyTurn("Husk timelisten", "Hei! Uke 45 mangler timer. Kan du føre dem og levere uken?"), modtest.ChatTurn{Text: "Ferdig."})
	if err := publishOnOutbox(t, c.h, contracts.WeekUnsubmitted{UserID: c.colleague, WeekStart: "2026-11-02", Hours: "12.50"}); err != nil {
		t.Fatal(err)
	}
	d := events.NewDispatcher(c.h.Pool(), agents.Module().EventHandlers(c.h.Deps()), c.h.Deps().Logger, c.h.Now)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state := c.awaitSubjectRun(t, "person_week"); state != "done" {
		t.Fatalf("run state = %s, want done; log:\n%s", state, c.h.Logs())
	}
	mails := c.mails(t)
	if len(mails) != 1 || mails[0].To != c.email || mails[0].Subject != "Husk timelisten" ||
		!strings.Contains(mails[0].TextBody, "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).") || !strings.Contains(mails[0].TextBody, "Uke 45") {
		t.Fatalf("mails = %+v, want one to the colleague with the disclosure on top", mails)
	}
	// The seed told the model the week and the hours, and not who.
	seeds := c.script.startedWith()
	if len(seeds) != 1 || !strings.Contains(seeds[0], "2026-11-02") || !strings.Contains(seeds[0], "12.50") || strings.Contains(seeds[0], c.colleague.String()) {
		t.Errorf("seeds = %v, want the week and the hours and not the person's id", seeds)
	}
	if n := c.h.Count(t, `SELECT count(*) FROM agents.handed_off`); n != 0 {
		t.Errorf("%d hand-off markers for a run about a week", n)
	}
}

// A notice that claims the agent is a person is stopped by C1 and nothing is mailed.
func TestTimesheetReminders_ANoticeThatClaimsToBeAPersonIsNotSent(t *testing.T) {
	t.Parallel()
	c := newColleagueRig(t, notifyTurn("Husk timelisten", "Hei, det er meg, din kollega Hanna."), modtest.ChatTurn{Text: "Ferdig."})
	c.script.check = `{"claimsHuman": true, "promises": []}`
	if err := publishOnOutbox(t, c.h, contracts.WeekUnsubmitted{UserID: c.colleague, WeekStart: "2026-11-02", Hours: "0.00"}); err != nil {
		t.Fatal(err)
	}
	d := events.NewDispatcher(c.h.Pool(), agents.Module().EventHandlers(c.h.Deps()), c.h.Deps().Logger, c.h.Now)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.awaitSubjectRun(t, "person_week")
	if mails := c.mails(t); len(mails) != 0 {
		t.Errorf("mails = %+v, want none for a notice that claims to be a person", mails)
	}
}

// TestProjectDigest_TheScheduleWakesTheRoleAndTheNoticeGoesToTheChosenPerson.
func TestProjectDigest_TheScheduleWakesTheRoleAndTheNoticeGoesToTheChosenPerson(t *testing.T) {
	t.Parallel()
	c := newColleagueRig(t, notifyTurn("Ukens prosjekter", "Tre prosjekter er aktive."), modtest.ChatTurn{Text: "Ferdig."})
	// The next Monday 08:00 in Oslo.
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no tz database")
	}
	now := c.h.Now().In(loc)
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7
	}
	monday := time.Date(now.Year(), now.Month(), now.Day()+(8-wd), 8, 0, 0, 0, loc)
	c.h.Advance(monday.Sub(c.h.Now()) + time.Minute)
	if state := c.awaitSubjectRun(t, "schedule"); state != "done" {
		t.Fatalf("run state = %s, want done; log:\n%s", state, c.h.Logs())
	}
	mails := c.mails(t)
	if len(mails) != 1 || mails[0].To != c.email || mails[0].Subject != "Ukens prosjekter" {
		t.Fatalf("mails = %+v, want the digest to the chosen person", mails)
	}
	if n := c.h.Count(t, `SELECT count(*) FROM agents.runs WHERE subject_type = 'schedule'`); n != 1 {
		t.Errorf("%d runs for one occurrence, want one", n)
	}
}

// toolResults are the results the run on conv got back from its tools.
func (r *runRig) toolResults(t *testing.T, conv uuid.UUID) []string {
	t.Helper()
	rows, err := r.h.Pool().Query(context.Background(),
		`SELECT m.text FROM agents.run_messages m JOIN agents.runs r ON r.id = m.run_id WHERE r.conversation_id = $1 AND m.role = 'tool' ORDER BY m.id`, conv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// setChangeMode sets the front desk role's change mode as the Owner.
func (r *runRig) setChangeMode(t *testing.T, mode string) {
	t.Helper()
	var ag struct {
		Roles []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"roles"`
	}
	okJSON(t, r.owner, http.MethodGet, "/api/v1/agents/agents/"+r.agentID.String(), nil, &ag)
	resp := r.owner.Do(http.MethodPut, fmt.Sprintf("/api/v1/agents/agents/%s/roles/%s", r.agentID, r.roleID), map[string]any{
		"name": "Front desk", "instructions": "Answer.", "replyMode": "review", "threshold": 85, "changeMode": mode,
		"capsToolCalls": 30, "capsTurns": 8, "maxRepliesPerDay": 5, "handOffUserId": nil,
	}, modtest.Header("If-Match", fmt.Sprint(ag.Roles[0].Version)))
	if resp.Status != http.StatusOK {
		t.Fatalf("set the change mode: %d %s", resp.Status, resp.Body)
	}
}

// TestFollowUp_IsAboutTheRunsOwnConversationAndAtMostThirtyDaysAhead is V29's
// limits: not in the past, not past 30 days, and one wakeup a subject.
func TestFollowUp_IsAboutTheRunsOwnConversationAndAtMostThirtyDaysAhead(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	now := r.h.Now()
	call := func(at time.Time, reason string) modtest.ChatToolCall {
		return toolCall("follow_up", map[string]any{"at": at.Format(time.RFC3339), "reason": reason})
	}
	r.script.turns = []modtest.ChatTurn{
		{ToolCalls: []modtest.ChatToolCall{
			call(now.Add(-time.Hour), "yesterday"),
			call(now.Add(31*24*time.Hour), "too far"),
			call(now.Add(5*24*time.Hour), "look at it on Friday"),
			call(now.Add(6*24*time.Hour), "or on Saturday"),
		}},
		{Text: "Ferdig."},
	}
	conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Spørsmål", "Hei, kan dere ringe meg neste uke?", "")
	r.await(t, conv)

	results := r.toolResults(t, conv)
	if len(results) != 4 || !strings.Contains(results[0], "out_of_range") || !strings.Contains(results[1], "out_of_range") ||
		!strings.Contains(results[2], "scheduled") || !strings.Contains(results[3], "scheduled") {
		t.Fatalf("tool results = %v, want two refused and two scheduled", results)
	}
	rows, err := r.h.Pool().Query(context.Background(), `SELECT subject_type, subject_id, at, reason FROM agents.wakeups WHERE role_id = $1`, r.roleID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var typ, id, reason string
		var at time.Time
		if err := rows.Scan(&typ, &id, &at, &reason); err != nil {
			t.Fatal(err)
		}
		n++
		if typ != "conversation" || id != conv.String() || !at.Equal(now.Add(6*24*time.Hour).Truncate(time.Second)) || reason != "or on Saturday" {
			t.Errorf("wakeup = %s %s %s %q, want the conversation's, moved to the later time", typ, id, at, reason)
		}
	}
	if n != 1 {
		t.Errorf("%d wakeups, want one for the subject: a new follow-up replaces the pending one", n)
	}
}

// TestChangeMode_OnlyAnOperationTheTemplateMarksSafeFollowsTheMode: the status
// change of the front desk is marked safe, so in Auto it is made at once; in
// Review it waits for a person as before.
func TestChangeMode_OnlyAnOperationTheTemplateMarksSafeFollowsTheMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"review", "auto"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := newRunRig(t, "review")
			customer := r.customer(t, "Acme AS", "kari@example.test")
			r.setChangeMode(t, mode)
			r.script.turns = []modtest.ChatTurn{
				{ToolCalls: []modtest.ChatToolCall{readOp("getCustomer", map[string]any{"id": customer})}},
				{ToolCalls: []modtest.ChatToolCall{toolCall("call_operation", map[string]any{
					"id": "patchCommunicationsConversationsById", "body": map[string]any{"status": "closed"}, "explain": "Close it.",
					"sources": []string{fmt.Sprintf("getCustomer %d", customer)}, "score": 90,
				})}},
				{Text: "Lukket."},
			}
			conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Takk", "Takk for hjelpen, saken er løst.", "")
			r.link(t, conv, customer)
			r.await(t, conv)

			status := modtest.One[string](t, r.h, `SELECT status FROM communications.conversations WHERE id = $1`, conv)
			items := r.items(t, conv)
			switch mode {
			case "auto":
				if status != "closed" || len(items) != 0 {
					t.Errorf("auto: status %q, %d items, want the status change made and nothing waiting", status, len(items))
				}
			default:
				if status == "closed" || len(items) != 1 || items[0].Kind != "change" || items[0].State != "waiting" {
					t.Errorf("review: status %q, items %+v, want the change waiting and the status as it was", status, items)
				}
			}
		})
	}
}
