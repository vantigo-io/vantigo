package communications_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Reply on a Messenger conversation (Messenger design N4, N6, N7,
// delivery A): the components checked against the provider's Capabilities, the
// idempotency fingerprint that includes them, the reply window at 24 hours, at 7
// days and closed, and every 422 the channel adds. The fake provider's
// Capabilities are the limits of the design unless a test sets others.

// chat starts a conversation: the participant 4242 writes, and the sync imports
// it. It answers the conversation's id.
func (f chatFixture) chat(t *testing.T) string {
	t.Helper()
	f.text("4242", "m-"+uuid.NewString(), "Hello", 0)
	f.sync(t)
	convs := f.conversations(t)
	if len(convs) != 1 {
		t.Fatalf("conversations = %v, want the one", convs)
	}
	return convs[0]
}

// reply posts a reply under key.
func (f chatFixture) reply(conversation, key string, body map[string]any) *modtest.Response {
	return f.c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conversation+"/reply", body,
		modtest.Header("Idempotency-Key", key))
}

// mustReply posts a reply under a fresh key and fails t unless it is queued
// (201), answering the message's id.
func (f chatFixture) mustReply(t *testing.T, conversation string, body map[string]any) uuid.UUID {
	t.Helper()
	r := f.reply(conversation, uuid.NewString(), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("reply: status %d body %s, want 201", r.Status, r.Body)
	}
	var out conversationMutationJSON
	r.JSON(&out)
	return uuid.MustParse(*out.MessageId)
}

func yesNo() map[string]any {
	return map[string]any{"answerOptions": []map[string]any{{"id": "yes", "title": "Yes"}, {"id": "no", "title": "No"}}}
}

// TestReplyMessenger_QueuesTheTextWithItsComponents: a reply carries the text
// and the components of a message: they are stored on it — no subject, no HTML,
// one delivery to the participant's id and no Cc — and the thread answers them,
// with the window and the provider's limits.
func TestReplyMessenger_QueuesTheTextWithItsComponents(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)

	message := f.mustReply(t, conv, map[string]any{
		"subject": "ignored", "htmlBody": "<b>ignored</b>", "textBody": "Shall we?",
		"components": map[string]any{
			"answerOptions": yesNo()["answerOptions"], "shareEmail": true,
		},
	})
	if got := modtest.One[string](t, f.h, `SELECT coalesce(subject, '-') || '|' || coalesce(html_body, '-') || '|' || text_body FROM communications.conversation_messages WHERE id = $1`, message); got != "-|-|Shall we?" {
		t.Errorf("subject|html|text = %q, want none, none and the text", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.message_deliveries WHERE message_id = $1 AND recipient_address = '4242' AND recipient_type = 'to'`, message); got != 1 {
		t.Errorf("deliveries to the participant = %d, want 1", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.message_deliveries WHERE message_id = $1`, message); got != 1 {
		t.Errorf("deliveries = %d, want only the one", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.outbox_jobs WHERE message_id = $1 AND status = 'pending'`, message); got != 1 {
		t.Errorf("outbox jobs = %d, want one pending", got)
	}

	d := f.detail(t, conv)
	if d.ComponentLimits == nil || d.ComponentLimits.MaxText != 2000 || d.ComponentLimits.MaxAnswerOptions != 13 || d.ComponentLimits.MaxAnswerTitle != 20 || !d.ComponentLimits.ShareEmail {
		t.Errorf("componentLimits = %+v, want the provider's", d.ComponentLimits)
	}
	if d.ReplyWindow == nil || d.ReplyWindow.State != "open" || d.ReplyWindow.ClosesAt == nil || !d.ReplyWindow.ClosesAt.Equal(f.h.Now().Add(24*time.Hour)) {
		t.Errorf("replyWindow = %+v, want open until 24 h after the participant's message", d.ReplyWindow)
	}
	var sent *chatMessageJSON
	for i := range d.Messages {
		if d.Messages[i].Id == message.String() {
			sent = &d.Messages[i]
		}
	}
	if sent == nil || sent.Components == nil || sent.Components.AnswerOptions == nil || len(*sent.Components.AnswerOptions) != 2 ||
		(*sent.Components.AnswerOptions)[0].Id != "yes" || sent.Components.ShareEmail == nil || !*sent.Components.ShareEmail {
		t.Fatalf("the sent message's components = %+v, want its options in order and the e-mail request", sent)
	}
	var recipients struct {
		ReplyRecipients struct {
			CanReply bool    `json:"canReply"`
			ReplyTo  *string `json:"replyTo"`
		} `json:"replyRecipients"`
	}
	f.c.Do(http.MethodGet, "/api/v1/communications/conversations/"+conv, nil).JSON(&recipients)
	if !recipients.ReplyRecipients.CanReply || recipients.ReplyRecipients.ReplyTo == nil || *recipients.ReplyRecipients.ReplyTo != "4242" {
		t.Errorf("replyRecipients = %+v, want the participant", recipients.ReplyRecipients)
	}
}

// TestReplyMessenger_Refusals: every 422 a Messenger channel adds, each with its
// code and, where it has them, its fields.
func TestReplyMessenger_Refusals(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)

	options := func(n int) []map[string]any {
		var out []map[string]any
		for i := range n {
			out = append(out, map[string]any{"id": "o" + strings.Repeat("x", i), "title": "Option"})
		}
		return out
	}
	cases := []struct {
		name   string
		body   map[string]any
		status int
		code   string
		field  string
	}{
		{"sendFromPersonal", map[string]any{"textBody": "Hi", "sendFromPersonal": true}, 422, "not_supported_on_channel", ""},
		{"reply all", map[string]any{"textBody": "Hi", "replyMode": "reply_all"}, 422, "not_supported_on_channel", ""},
		{"2001 characters", map[string]any{"textBody": strings.Repeat("å", 2001)}, 422, "text_too_long", ""},
		{"no text", map[string]any{"htmlBody": "<p>Only html</p>"}, 400, "invalid_request", "textBody"},
		{"14 options", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": options(14)}}, 422, "components_invalid", "answerOptions"},
		{"an empty list of options", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []any{}}}, 422, "components_invalid", "answerOptions"},
		{"a 21-character title", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []map[string]any{{"id": "a", "title": strings.Repeat("a", 21)}}}}, 422, "components_invalid", "answerOptions[0].title"},
		{"a blank title", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []map[string]any{{"id": "a", "title": "  "}}}}, 422, "components_invalid", "answerOptions[0].title"},
		{"an id out of shape", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []map[string]any{{"id": "Yes!", "title": "Yes"}}}}, 422, "components_invalid", "answerOptions[0].id"},
		{"an id of 41 characters", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []map[string]any{{"id": strings.Repeat("a", 41), "title": "Yes"}}}}, 422, "components_invalid", "answerOptions[0].id"},
		{"a repeated id", map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []map[string]any{{"id": "a", "title": "A"}, {"id": "a", "title": "B"}}}}, 422, "components_invalid", "answerOptions[1].id"},
		{"cards", map[string]any{"textBody": "Hi", "components": map[string]any{"cards": []map[string]any{{"title": "Card"}}}}, 422, "components_invalid", "cards"},
	}
	for _, tc := range cases {
		r := f.reply(conv, uuid.NewString(), tc.body)
		if r.Status != tc.status {
			t.Errorf("%s: status %d body %s, want %d", tc.name, r.Status, r.Body, tc.status)
			continue
		}
		var got commErrorJSON
		r.JSON(&got)
		if got.Error.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, got.Error.Code, tc.code)
		}
		if tc.field != "" && len(got.field(tc.field)) == 0 {
			t.Errorf("%s: fields = %v, want %q named", tc.name, got.Error.Fields, tc.field)
		}
	}
	// 2000 characters are taken, counted as characters and not bytes.
	f.mustReply(t, conv, map[string]any{"textBody": strings.Repeat("å", 2000)})
	// 13 options and a 20-character title are taken.
	f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": append(options(12), map[string]any{"id": "last", "title": strings.Repeat("å", 20)})}})

	// A provider that takes no components, or has none to take.
	f.fake.SetCapabilities(contracts.MessengerCapabilities{MaxText: 2000})
	r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hi", "components": yesNo()})
	wantCode(t, r, http.StatusUnprocessableEntity, "components_unsupported")
	if r := f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hi", "components": map[string]any{"answerOptions": []any{}, "shareEmail": false}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("empty options on a provider with no components: status %d, want 422", r.Status)
	}
	// Components of no content are none.
	f.mustReply(t, conv, map[string]any{"textBody": "Hi", "components": map[string]any{}})

	// The request for an e-mail address needs the provider to have it.
	f.fake.SetCapabilities(contracts.MessengerCapabilities{MaxText: 2000, MaxAnswerOptions: 13, MaxAnswerTitle: 20, Window: 24 * time.Hour})
	r = f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hi", "components": map[string]any{"shareEmail": true}})
	wantCode(t, r, http.StatusUnprocessableEntity, "components_invalid")
}

// TestReplyMessenger_AnEmailChannelTakesNoComponents: components on a reply in an
// e-mail conversation are 422 components_unsupported.
func TestReplyMessenger_AnEmailChannelTakesNoComponents(t *testing.T) {
	t.Parallel()
	h, _ := messengerHarness(t)
	c := h.SignIn(t, "communications:channels-manage", "communications:conversations-reply", "communications:conversations-view")
	setupChannel(t, h)
	conv := createConversation(t, c, newConversationBody("someone@example.test")).ConversationId

	r := c.Do(http.MethodPost, "/api/v1/communications/conversations/"+conv+"/reply", map[string]any{
		"textBody": "Hi", "components": yesNo(),
	}, modtest.Header("Idempotency-Key", uuid.NewString()))
	wantCode(t, r, http.StatusUnprocessableEntity, "components_unsupported")
}

// TestReplyMessenger_TheFingerprintIncludesTheComponents: an idempotent replay of
// the same reply is the same answer, a replay with other components under the
// same key the existing mismatch refusal.
func TestReplyMessenger_TheFingerprintIncludesTheComponents(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	key := uuid.NewString()
	body := map[string]any{"textBody": "Shall we?", "components": yesNo()}

	first := f.reply(conv, key, body)
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s", first.Status, first.Body)
	}
	if again := f.reply(conv, key, body); again.Status != http.StatusOK {
		t.Errorf("the same reply again: status %d body %s, want 200", again.Status, again.Body)
	}
	other := map[string]any{"textBody": "Shall we?", "components": map[string]any{"answerOptions": []map[string]any{{"id": "yes", "title": "Yes"}, {"id": "no", "title": "Never"}}}}
	wantCode(t, f.reply(conv, key, other), http.StatusConflict, "idempotency_key_reused")
	wantCode(t, f.reply(conv, key, map[string]any{"textBody": "Shall we?"}), http.StatusConflict, "idempotency_key_reused")
	if got := f.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE direction = 'outbound'`); got != 1 {
		t.Errorf("outbound messages = %d, want the one", got)
	}
}

// TestReplyMessenger_TheWindow: the window is 24 hours after the participant's
// newest message — an echo does not count —, runs to 7 days with the provider's
// Human Agent permission, and is closed after; a reply to a closed one is 422
// reply_window_closed, and an idempotent replay of one accepted inside it is
// still answered.
func TestReplyMessenger_TheWindow(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	conv := f.chat(t)
	state := func() string {
		f.resign(t)
		d := f.detail(t, conv)
		if d.ReplyWindow == nil {
			t.Fatal("no replyWindow")
		}
		return d.ReplyWindow.State
	}

	// Without the Human Agent permission: open to 24 h, closed after.
	f.h.Advance(24 * time.Hour)
	f.resign(t)
	if got := state(); got != "open" {
		t.Errorf("at 24 h the window is %s, want open: it closes after 24 h", got)
	}
	key := uuid.NewString()
	if r := f.reply(conv, key, map[string]any{"textBody": "Just in time"}); r.Status != http.StatusCreated {
		t.Fatalf("a reply at 24 h: status %d body %s, want 201", r.Status, r.Body)
	}
	f.h.Advance(time.Second)
	f.resign(t)
	if got := state(); got != "closed" {
		t.Errorf("after 24 h the window is %s, want closed", got)
	}
	wantCode(t, f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Too late"}), http.StatusUnprocessableEntity, "reply_window_closed")
	if r := f.reply(conv, key, map[string]any{"textBody": "Just in time"}); r.Status != http.StatusOK {
		t.Errorf("the replay of a reply accepted inside the window: status %d body %s, want 200", r.Status, r.Body)
	}

	// With it: human_agent to 7 days.
	caps := contracts.MessengerCapabilities{
		MaxText: 2000, MaxAnswerOptions: 13, MaxAnswerTitle: 20, ShareEmail: true,
		Window: 24 * time.Hour, HumanAgentWindow: 7 * 24 * time.Hour,
	}
	f.fake.SetCapabilities(caps)
	if got := state(); got != "human_agent" {
		t.Errorf("after 24 h with the permission the window is %s, want human_agent", got)
	}
	f.resign(t)
	d := f.detail(t, conv)
	if d.ReplyWindow.ClosesAt == nil || !d.ReplyWindow.ClosesAt.Equal(f.h.Now().Add(-24*time.Hour-time.Second).Add(7*24*time.Hour)) {
		t.Errorf("closesAt = %v, want 7 days after the participant's message", d.ReplyWindow.ClosesAt)
	}
	f.mustReply(t, conv, map[string]any{"textBody": "Tagged"})
	f.h.Advance(6*24*time.Hour - time.Second) // 7 days exactly since the message
	f.resign(t)
	if got := state(); got != "human_agent" {
		t.Errorf("at 7 days the window is %s, want human_agent", got)
	}
	f.h.Advance(time.Second)
	f.resign(t)
	if got := state(); got != "closed" {
		t.Errorf("after 7 days the window is %s, want closed", got)
	}
	wantCode(t, f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Too late"}), http.StatusUnprocessableEntity, "reply_window_closed")

	// The participant writes again: open again.
	f.text("4242", "m-again", "I am back", 0)
	f.sync(t)
	f.resign(t)
	if got := state(); got != "open" {
		t.Errorf("after the participant wrote again the window is %s, want open", got)
	}
}

// TestReplyMessenger_AnEchoDoesNotOpenTheWindow: a conversation the Page's own
// echo made, with no message of the participant's, has a closed window, and the
// participant's message that follows opens it.
func TestReplyMessenger_AnEchoDoesNotOpenTheWindow(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.fake.Push(f.channel.Address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: "9999", At: f.h.Now(), PlatformMessageID: "echo-1", Text: "From the suite", Echo: true,
	})
	f.sync(t)
	conv := f.conversations(t)[0]
	d := f.detail(t, conv)
	if d.ReplyWindow == nil || d.ReplyWindow.State != "closed" || d.ReplyWindow.ClosesAt != nil {
		t.Errorf("replyWindow = %+v, want closed with no end: nothing of the participant's yet", d.ReplyWindow)
	}
	wantCode(t, f.reply(conv, uuid.NewString(), map[string]any{"textBody": "Hello"}), http.StatusUnprocessableEntity, "reply_window_closed")

	f.text("9999", "m-1", "Hi", time.Second)
	f.sync(t)
	f.mustReply(t, conv, map[string]any{"textBody": "Hello"})
}
