package communications_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is what an agent needs of Communications (virtual employees design
// V11, V13, V15, V22): the capabilities of a conversation's channel, the marking
// of a message an agent sent, and the refusals of a reply made through an agent
// grant. The agent's calls here go through the operations dispatcher with an agent
// grant, as the agents module's runs do, under a gate that lets every operation
// through: the gate is the agents module's, tested there.

// asAgent is the context of an agent's run for agentID, holding what a reply
// needs.
func asAgent(t *testing.T, h *modtest.Harness, agentID uuid.UUID) (context.Context, uuid.UUID) {
	t.Helper()
	_, user := h.AgentContext(t, nil, "communications:conversations-reply", "communications:conversations-view", "communications:conversations-manage")
	return h.AgentGrantContext(t, user, agentID, nil), user
}

// agentResult is what an agent's call answered.
type agentResult struct {
	Status int
	Body   []byte
}

func agentReply(t *testing.T, h *modtest.Harness, ctx context.Context, conversation string, body map[string]any) (agentResult, error) {
	t.Helper()
	status, resp, err := h.AgentCall(t, ctx, "postCommunicationsConversationsByIdReply", map[string]any{"id": conversation},
		map[string]string{"Idempotency-Key": uuid.NewString()}, body)
	return agentResult{status, resp}, err
}

func TestCapabilities_EmailTakesASubjectHtmlAndCcAndHasNoWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	conv, _ := replyableConversation(t, h, c)
	r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+conv, nil)
	var d struct {
		Capabilities struct {
			Subject            bool   `json:"subject"`
			HTML               bool   `json:"html"`
			CC                 bool   `json:"cc"`
			ReplyAll           bool   `json:"replyAll"`
			Components         bool   `json:"components"`
			MaxText            *int   `json:"maxText"`
			MaxAttachmentBytes *int64 `json:"maxAttachmentBytes"`
			Window             any    `json:"window"`
		} `json:"capabilities"`
	}
	r.JSON(&d)
	got := d.Capabilities
	if !got.Subject || !got.HTML || !got.CC || !got.ReplyAll || got.Components || got.MaxText != nil || got.Window != nil {
		t.Errorf("e-mail capabilities = %+v, want subject, html, cc and reply-all, no components, no limit, no window", got)
	}
}

func TestCapabilities_MessengerAnswersTheProvidersLimitsAndTheWindow(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	r := f.c.Do(http.MethodGet, "/api/v1/communications/conversations/"+conv, nil)
	var d struct {
		Capabilities struct {
			Subject    bool `json:"subject"`
			HTML       bool `json:"html"`
			CC         bool `json:"cc"`
			ReplyAll   bool `json:"replyAll"`
			Components bool `json:"components"`
			ShareEmail bool `json:"shareEmail"`
			MaxText    *int `json:"maxText"`
			Window     *struct {
				State string `json:"state"`
			} `json:"window"`
		} `json:"capabilities"`
	}
	r.JSON(&d)
	got := d.Capabilities
	if got.Subject || got.HTML || got.CC || got.ReplyAll || !got.Components || !got.ShareEmail || got.MaxText == nil || *got.MaxText != 2000 || got.Window == nil || got.Window.State != "open" {
		t.Errorf("messenger capabilities = %+v, want no subject, html or Cc, components and e-mail request, 2000 characters and an open window", got)
	}
}

func TestAgentReply_IsMarkedByCommunicationsFromThePrincipal(t *testing.T) {
	t.Parallel()
	smtp := &fakeSMTP{}
	h := newOutboxHarness(t, smtp)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	conv, address := replyableConversation(t, h, c)
	agentID := uuid.New()
	ctx, user := asAgent(t, h, agentID)

	// A body that claims another agent, or none, changes nothing: the mark is the
	// principal's.
	res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei fra assistenten", "agentId": uuid.NewString()})
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("an agent's reply = %d %s (%v), want 201", res.Status, res.Body, err)
	}
	var m conversationMutationJSON
	_ = json.Unmarshal(res.Body, &m)
	if got := modtest.One[*uuid.UUID](t, h, `SELECT agent_id FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(*m.MessageId)); got == nil || *got != agentID {
		t.Errorf("agent_id = %v, want %s", got, agentID)
	}
	if got := modtest.One[uuid.UUID](t, h, `SELECT author_user_id FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(*m.MessageId)); got != user {
		t.Errorf("author = %s, want the agent's user %s", got, user)
	}
	// A person's reply carries no mark.
	pm := mustReplyAs(t, c, conv)
	if got := modtest.One[*uuid.UUID](t, h, `SELECT agent_id FROM communications.conversation_messages WHERE id = $1`, pm); got != nil {
		t.Errorf("a person's message carries agent_id %v", got)
	}

	// The header goes out with the mail.
	w := communications.NewOutboxWorker(h.Deps())
	for range 2 {
		if _, err := w.ProcessOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	sent := smtp.sends()
	if len(sent) != 2 {
		t.Fatalf("sent = %d, want both messages", len(sent))
	}
	marks := 0
	for _, s := range sent {
		if v := s.Headers["X-Vantigo-Agent"]; v != "" {
			marks++
			if v != agentID.String()+"; ai-generated" {
				t.Errorf("X-Vantigo-Agent = %q, want %q", v, agentID.String()+"; ai-generated")
			}
		}
	}
	if marks != 1 {
		t.Errorf("marked messages = %d, want only the agent's", marks)
	}
	_ = address
}

func mustReplyAs(t *testing.T, c *modtest.Client, conv string) uuid.UUID {
	t.Helper()
	r := doReply(c, conv, uuid.NewString(), replyBody())
	if r.Status != http.StatusCreated {
		t.Fatalf("reply: %d %s", r.Status, r.Body)
	}
	var m conversationMutationJSON
	r.JSON(&m)
	return uuid.MustParse(*m.MessageId)
}

func TestAgentReply_NeverAnswersEveryone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	conv, _ := replyableConversation(t, h, c)
	ctx, _ := asAgent(t, h, uuid.New())
	res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei", "replyMode": "reply_all"})
	if err != nil || res.Status != http.StatusUnprocessableEntity || errorCode(res.Body) != "reply_all_not_allowed" {
		t.Errorf("an agent's reply-all = %d %s (%v), want 422 reply_all_not_allowed", res.Status, res.Body, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1 AND direction = 'outbound' AND agent_id IS NOT NULL`, uuid.MustParse(conv)); n != 0 {
		t.Errorf("a refused reply left %d messages", n)
	}
	// A person may.
	body := replyBody()
	body["replyMode"] = "reply_all"
	if r := doReply(c, conv, uuid.NewString(), body); r.Status != http.StatusCreated {
		t.Errorf("a person's reply-all = %d %s, want 201", r.Status, r.Body)
	}
}

func errorCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	return e.Error.Code
}

func TestAgentReply_ConversationTakenWhenAssignedToSomeoneElse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view", "communications:conversations-manage")
	conv, _ := replyableConversation(t, h, c)
	ctx, agentUser := asAgent(t, h, uuid.New())
	person := h.SignIn(t, "communications:conversations-view")
	_ = person
	_, me := h.SignInUser(t, "communications:conversations-view")

	// Assigned to a person: the agent's reply is refused.
	if r := assign(c, conv, me.String()); r.Status != http.StatusOK {
		t.Fatalf("assign to a person: %d %s", r.Status, r.Body)
	}
	res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei"})
	if err != nil || res.Status != http.StatusConflict || errorCode(res.Body) != "conversation_taken" {
		t.Fatalf("a reply to a conversation a person has = %d %s (%v), want 409 conversation_taken", res.Status, res.Body, err)
	}
	// Assigned to the agent, or to nobody: allowed.
	if r := assign(c, conv, agentUser.String()); r.Status != http.StatusOK {
		t.Fatalf("assign to the agent: %d %s", r.Status, r.Body)
	}
	if res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei"}); err != nil || res.Status != http.StatusCreated {
		t.Errorf("a reply to the agent's own conversation = %d %s (%v), want 201", res.Status, res.Body, err)
	}
	if r := assign(c, conv, nil); r.Status != http.StatusOK {
		t.Fatalf("unassign: %d %s", r.Status, r.Body)
	}
	if res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei igjen"}); err != nil || res.Status != http.StatusCreated {
		t.Errorf("a reply to an unassigned conversation = %d %s (%v), want 201", res.Status, res.Body, err)
	}
	// A person is not held to it.
	if r := assign(c, conv, me.String()); r.Status != http.StatusOK {
		t.Fatal(r.Body)
	}
	if r := doReply(c, conv, uuid.NewString(), replyBody()); r.Status != http.StatusCreated {
		t.Errorf("a person replying to an assigned conversation = %d %s, want 201", r.Status, r.Body)
	}
}

func TestAgentReply_MessengerIsMarkedInItsMetadataAndNeverSentInTheHumanAgentWindow(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	agentID := uuid.New()
	ctx, _ := asAgent(t, f.h, agentID)

	res, err := agentReply(t, f.h, ctx, conv, map[string]any{"textBody": "Hei fra assistenten"})
	if err != nil || res.Status != http.StatusCreated {
		t.Fatalf("an agent's reply on Messenger = %d %s (%v), want 201", res.Status, res.Body, err)
	}
	var m conversationMutationJSON
	_ = json.Unmarshal(res.Body, &m)
	meta := modtest.One[string](t, f.h, `SELECT coalesce(channel_metadata_json->>'agent', '') FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(*m.MessageId))
	if meta != agentID.String() {
		t.Errorf("channel_metadata_json.agent = %q, want %s", meta, agentID)
	}
	if got := modtest.One[string](t, f.h, `SELECT coalesce(channel_metadata_json->>'agent', '') FROM communications.conversation_messages WHERE id = $1`, f.mustReply(t, conv, map[string]any{"textBody": "A person"})); got != "" {
		t.Errorf("a person's message carries agent %q", got)
	}

	// Only the human-agent window is left: a person may still reply there, an
	// agent never.
	f.fake.SetCapabilities(contracts.MessengerCapabilities{
		MaxText: 2000, MaxAnswerOptions: 13, MaxAnswerTitle: 20, ShareEmail: true,
		Window: 24 * time.Hour, HumanAgentWindow: 7 * 24 * time.Hour,
	})
	f.h.Advance(25 * time.Hour)
	f.resign(t)
	ctx, _ = asAgent(t, f.h, agentID) // the grant lives ten minutes
	res, err = agentReply(t, f.h, ctx, conv, map[string]any{"textBody": "Sent tagged?"})
	if err != nil || res.Status != http.StatusUnprocessableEntity || errorCode(res.Body) != "reply_window_closed" {
		t.Errorf("an agent's reply in the human-agent window = %d %s (%v), want 422 reply_window_closed", res.Status, res.Body, err)
	}
	if r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "A person, tagged"}); r.Status != http.StatusCreated {
		t.Errorf("a person's reply in the human-agent window = %d %s, want 201", r.Status, r.Body)
	}
}

func TestReply_PublishesWhoAnswered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	conv, _ := replyableConversation(t, h, c)
	ctx, user := asAgent(t, h, uuid.New())
	mustReplyAs(t, c, conv)
	if res, err := agentReply(t, h, ctx, conv, map[string]any{"textBody": "Hei"}); err != nil || res.Status != http.StatusCreated {
		t.Fatalf("agent reply: %d %v", res.Status, err)
	}
	payloads, aggregates := publishedEvents(t, h, "communications.conversation.replied")
	if len(payloads) != 2 {
		t.Fatalf("replied events = %d, want 2", len(payloads))
	}
	for _, a := range aggregates {
		if a != conv {
			t.Errorf("aggregate = %s, want the conversation %s", a, conv)
		}
	}
	if payloads[0]["byAgent"] != false || payloads[1]["byAgent"] != true || payloads[1]["byUserId"] != user.String() {
		t.Errorf("payloads = %v, want a person's then the agent's", payloads)
	}
}
