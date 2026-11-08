package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The review queue end to end (virtual employees design V20): a run in Review
// leaves a reply, a reviewer who may see what the agent read and may send it
// approves, and the reply goes out through the real Communications as the agent,
// with the disclosure at the top and the agent header, and the action log and the
// grant name the reviewer.

// seeKeys are what the front desk reads: a reviewer needs them to see an item.
var seeKeys = []string{
	"communications:conversations-view", "customers:view", "customers:contacts-view", "customers:associations-view",
	"orders:access", "invoices:access", "products:products-view", "products:variants-view", "products:pricing-view",
	"products:categories-view", "products:tax-categories-view", "knowledge:articles",
}

func (r *runRig) reviewer(t *testing.T, extra ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	keys := append([]string{"agents:review", "communications:conversations-reply", "communications:conversations-manage"}, seeKeys...)
	return r.h.SignInUser(t, append(keys, extra...)...)
}

type queued struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	State   string `json:"state"`
	Kind    string `json:"kind"`
	Reply   *struct {
		Text       string `json:"text"`
		Disclosure string `json:"disclosure"`
	} `json:"reply"`
	DecidedByUserID *string  `json:"decidedByUserId"`
	EditRatio       *float64 `json:"editRatio"`
}

func (r *runRig) queue(t *testing.T, c *modtest.Client, state string) []queued {
	t.Helper()
	var list struct {
		Items []queued `json:"items"`
	}
	okJSON(t, c, http.MethodGet, "/api/v1/agents/review?state="+state, nil, &list)
	return list.Items
}

// awaitRuns waits until n runs of the conversation have ended.
func (r *runRig) awaitRuns(t *testing.T, conv uuid.UUID, n int) {
	t.Helper()
	r.h.Advance(30 * time.Second)
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if r.h.Count(t, `SELECT count(*) FROM agents.runs WHERE conversation_id = $1 AND state NOT IN ('queued', 'running')`, conv) >= n {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("fewer than %d runs of %s ended; log:\n%s", n, conv, r.h.Logs())
}

func decide(t *testing.T, c *modtest.Client, it queued, action string, body any) (int, queued) {
	t.Helper()
	resp := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/agents/review/%s/%s", it.ID, action), body, modtest.Header("If-Match", fmt.Sprint(it.Version)))
	var out queued
	if resp.Status == http.StatusOK {
		resp.JSON(&out)
	}
	return resp.Status, out
}

func TestAgentReview_AnApprovedReplyIsSentAsTheAgentWithTheDisclosureAndTheHeader(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	customer := r.customer(t, "Acme AS", "kari@example.test")
	r.script.turns = []modtest.ChatTurn{
		{ToolCalls: []modtest.ChatToolCall{readOp("getCustomer", map[string]any{"id": customer})}},
		{ToolCalls: []modtest.ChatToolCall{replyCall("Hei Kari! Kunden din er registrert hos oss.", []string{fmt.Sprintf("getCustomer %d", customer)}, 91)}},
		{Text: "Svar er sendt til gjennomgang."},
	}
	conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Spørsmål", "Hei, er jeg registrert kunde hos dere?", "")
	r.link(t, conv, customer)
	if state := r.await(t, conv); state != "waiting_review" {
		t.Fatalf("run state = %s, want waiting_review", state)
	}

	// A person with agents:review alone cannot see what the agent read.
	bare := r.h.SignIn(t, "agents:review")
	if resp := bare.Do(http.MethodGet, "/api/v1/agents/review", nil); resp.Status != http.StatusForbidden {
		t.Errorf("the queue with agents:review alone = %d, want 403", resp.Status)
	}

	reviewer, reviewerID := r.reviewer(t)
	items := r.queue(t, reviewer, "waiting")
	if len(items) != 1 || items[0].Kind != "reply" || items[0].Reply == nil {
		t.Fatalf("queue = %+v, want the one reply", items)
	}
	var count struct {
		Waiting int `json:"waiting"`
	}
	okJSON(t, reviewer, http.MethodGet, "/api/v1/agents/review/count", nil, &count)
	if count.Waiting != 1 {
		t.Errorf("the badge counts %d, want 1", count.Waiting)
	}
	if len(r.sent(t)) != 0 {
		t.Fatal("Review sent a message before it was approved")
	}

	status, done := decide(t, reviewer, items[0], "approve", nil)
	if status != http.StatusOK || done.State != "sent" || done.DecidedByUserID == nil || *done.DecidedByUserID != reviewerID.String() {
		t.Fatalf("approve = %d %+v, want sent by the reviewer", status, done)
	}

	sent := r.sent(t)
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d, want the agent's reply", len(sent))
	}
	mime := string(sent[0].MIME)
	if !strings.Contains(mime, "kari@example.test") {
		t.Errorf("the reply does not go to the contact:\n%s", mime)
	}
	if !strings.Contains(mime, "X-Vantigo-Agent: "+r.agentID.String()+"; ai-generated") {
		t.Errorf("the mail does not carry the agent header:\n%s", mime)
	}
	if body := decodedBody(t, sent[0].MIME); !strings.Contains(body, "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).\n\nHei Kari! Kunden din er registrert hos oss.") {
		t.Errorf("the disclosure is not at the top of the message:\n%s", body)
	}
	if got := modtest.One[*uuid.UUID](t, r.h, `SELECT agent_id FROM communications.conversation_messages WHERE conversation_id = $1 AND direction = 'outbound'`, conv); got == nil || *got != r.agentID {
		t.Errorf("the stored message's agent_id = %v, want the agent", got)
	}
	if got := modtest.One[uuid.UUID](t, r.h, `SELECT author_user_id FROM communications.conversation_messages WHERE conversation_id = $1 AND direction = 'outbound'`, conv); got != r.userID {
		t.Errorf("the message's author = %s, want the agent's user %s: the reviewer did not write it", got, r.userID)
	}
	// A reviewer-approved reply is the agent's own: its replied event says byAgent, so it is not a
	// person answering over an earlier Auto reply of the conversation (V21).
	if n := r.h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'communications.conversation.replied' AND aggregate_id = $1 AND payload->>'byAgent' = 'true'`, conv.String()); n != 1 {
		t.Errorf("replied events by the agent = %d, want 1", n)
	}
	if n := r.h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'communications.conversation.replied' AND aggregate_id = $1 AND payload->>'byAgent' <> 'true'`, conv.String()); n != 0 {
		t.Errorf("replied events by a person = %d, want none", n)
	}
	earlier := uuid.New()
	r.h.Exec(t, `INSERT INTO agents.runs (id, agent_id, role_id, conversation_id, reason, state, outcome, verdict, started_at, ended_at, sent_at)
	             VALUES ($1, $2, $3, $4, 'message', 'done', 'sent', '{"score":90}', $5, $5, $5)`, earlier, r.agentID, r.roleID, conv, r.h.Now())
	d := events.NewDispatcher(r.h.Pool(), agents.Module().EventHandlers(r.h.Deps()), r.h.Deps().Logger, r.h.Now)
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := r.h.Count(t, `SELECT count(*) FROM agents.runs WHERE id = $1 AND answered_over_at IS NOT NULL`, earlier); n != 0 {
		t.Error("an approved agent reply marked the earlier Auto reply as answered over")
	}
	// The audit: the action log names the reviewer, and so did the grant the send was made under.
	if n := r.h.Count(t, `SELECT count(*) FROM agents.actions a JOIN agents.runs x ON x.id = a.run_id
		WHERE x.conversation_id = $1 AND a.approved_by = $2 AND a.operation_id = 'postCommunicationsConversationsByIdReply'`, conv, reviewerID); n != 1 {
		t.Errorf("approved actions in the log = %d, want 1", n)
	}
	if n := r.h.Count(t, `SELECT count(*) FROM identity.agent_grants WHERE approved_by_user_id = $1 AND user_id = $2`, reviewerID, r.userID); n != 1 {
		t.Errorf("grants that carry the approval = %d, want 1", n)
	}
	if n := r.h.Count(t, `SELECT count(*) FROM identity.agent_grants WHERE approved_by_user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, reviewerID); n != 0 {
		t.Errorf("the approval's grant is still live: %d", n)
	}
	// A second approval sends nothing more.
	if status, _ := decide(t, reviewer, done, "approve", nil); status != http.StatusConflict {
		t.Errorf("approve again = %d, want 409", status)
	}
	if n := len(r.sent(t)); n != 1 {
		t.Errorf("messages after the second approval = %d, want 1", n)
	}
}

func TestAgentReview_EditAndSendKeepsTheDisclosureAndCountsTheEdit(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	customer := r.customer(t, "Acme AS", "kari@example.test")
	r.script.turns = []modtest.ChatTurn{
		{ToolCalls: []modtest.ChatToolCall{readOp("getCustomer", map[string]any{"id": customer})}},
		{ToolCalls: []modtest.ChatToolCall{replyCall("Hei Kari! Kunden din er registrert hos oss.", []string{fmt.Sprintf("getCustomer %d", customer)}, 91)}},
		{Text: "Ferdig."},
	}
	conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Spørsmål", "Hei, er jeg registrert kunde hos dere?", "")
	r.link(t, conv, customer)
	r.await(t, conv)
	reviewer, _ := r.reviewer(t)
	it := r.queue(t, reviewer, "waiting")[0]

	// A reviewer cannot take the disclosure out by writing a text that holds it too: it is added above.
	status, done := decide(t, reviewer, it, "send", map[string]any{"text": "Hei Kari! Ja, du er registrert hos oss."})
	if status != http.StatusOK || done.State != "edited_sent" || done.EditRatio == nil || *done.EditRatio <= 0 {
		t.Fatalf("edit and send = %d %+v", status, done)
	}
	sent := r.sent(t)
	if len(sent) != 1 {
		t.Fatalf("messages sent = %d", len(sent))
	}
	if body := decodedBody(t, sent[0].MIME); !strings.Contains(body, "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).\n\nHei Kari! Ja, du er registrert hos oss.") {
		t.Errorf("the edited message does not open with the disclosure:\n%s", body)
	}
	if !strings.Contains(string(sent[0].MIME), "X-Vantigo-Agent:") {
		t.Errorf("the edited mail lost the agent header")
	}
}

func TestAgentReview_ADiscardedReplyHandsTheConversationOffAndANewerReplySupersedes(t *testing.T) {
	t.Parallel()
	r := newRunRig(t, "review")
	r.script.turns = []modtest.ChatTurn{
		{ToolCalls: []modtest.ChatToolCall{replyCall("Første svar.", []string{"the mail"}, 80)}},
		{Text: "Ferdig."},
		{ToolCalls: []modtest.ChatToolCall{replyCall("Andre svar.", []string{"the mail"}, 80)}},
		{Text: "Ferdig."},
	}
	conv := r.mail(t, "Kari Nordmann <kari@example.test>", "", "Hei", "Hallo?", "")
	r.await(t, conv)
	reviewer, _ := r.reviewer(t)
	first := r.queue(t, reviewer, "waiting")
	if len(first) != 1 {
		t.Fatalf("queue = %+v, want one", first)
	}

	// A newer message gets a newer reply, and the first one no longer waits.
	r.mail(t, "Kari Nordmann <kari@example.test>", "", "Re: Hei", "Hallo? Er noen der?", r.lastMsgID)
	r.awaitRuns(t, conv, 2)
	waiting := r.queue(t, reviewer, "waiting")
	if len(waiting) != 1 || waiting[0].ID == first[0].ID || waiting[0].Reply == nil || waiting[0].Reply.Text != "Andre svar." {
		t.Fatalf("waiting = %+v, want only the newer reply", waiting)
	}
	if withdrawn := r.queue(t, reviewer, "withdrawn"); len(withdrawn) != 1 || withdrawn[0].ID != first[0].ID {
		t.Errorf("withdrawn = %+v, want the first reply", withdrawn)
	}

	// Discarding the newer one hands the conversation off to the people; the agent leaves it.
	if status, d := decide(t, reviewer, waiting[0], "discard", map[string]any{"reason": "wrong_tone", "text": "Too curt."}); status != http.StatusOK || d.State != "discarded" {
		t.Fatalf("discard = %d %+v", status, d)
	}
	if reason := modtest.One[string](t, r.h, `SELECT reason FROM agents.handed_off WHERE conversation_id = $1`, conv); reason != "discarded" {
		t.Errorf("hand-off = %q, want discarded", reason)
	}
	if len(r.sent(t)) != 0 {
		t.Error("a discard sent a message")
	}
	r.mail(t, "Kari Nordmann <kari@example.test>", "", "Re: Hei", "Hallo?!", r.lastMsgID)
	r.h.Advance(time.Minute)
	if n := r.h.Count(t, `SELECT count(*) FROM agents.run_queue WHERE conversation_id = $1 AND taken_at IS NULL`, conv); n != 0 {
		t.Errorf("a handed-off conversation woke the agent: %d requests", n)
	}
}
