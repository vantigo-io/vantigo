package communications_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// liveEvent is one notification on live.Channel.
type liveEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// liveListener LISTENs on the harness's database, as a hub does.
type liveListener struct{ events chan liveEvent }

func listenLive(t *testing.T, h *modtest.Harness) *liveListener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect the listener: %v", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &liveListener{events: make(chan liveEvent, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				l.events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return l
}

// expect waits for every one of want, in any order, among what arrives.
func (l *liveListener) expect(t *testing.T, want ...liveEvent) {
	t.Helper()
	missing := map[liveEvent]bool{}
	for _, w := range want {
		missing[w] = true
	}
	deadline := time.After(10 * time.Second)
	var seen []liveEvent
	for len(missing) > 0 {
		select {
		case e := <-l.events:
			seen = append(seen, e)
			delete(missing, e)
		case <-deadline:
			t.Fatalf("live events missing %v; heard %v", missing, seen)
		}
	}
}

func inboxEvent(conversationID string) liveEvent {
	return liveEvent{T: "communications.inbox", K: "conversation.changed", ID: conversationID}
}

func conversationEvent(conversationID, kind, id string) liveEvent {
	return liveEvent{T: "communications.conversation:" + conversationID, K: kind, ID: id}
}

// A new conversation, a reply and a note are heard by the inbox and on the
// conversation's topic as message.added, with the message's id (L18).
func TestLive_NewConversationReplyAndNoteAreHeard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	l := listenLive(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view", "communications:conversations-manage")

	convID, _ := replyableConversation(t, h, c)
	l.expect(t, inboxEvent(convID))

	r := doReply(c, convID, uuid.NewString(), replyBody())
	if r.Status != http.StatusCreated {
		t.Fatalf("reply: status %d body %s", r.Status, r.Body)
	}
	var reply conversationMutationJSON
	r.JSON(&reply)
	l.expect(t, inboxEvent(convID), conversationEvent(convID, "message.added", *reply.MessageId))

	r = c.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID+"/notes", map[string]any{"textBody": "a note"})
	if r.Status != http.StatusCreated {
		t.Fatalf("note: status %d body %s", r.Status, r.Body)
	}
	var note conversationMutationJSON
	r.JSON(&note)
	l.expect(t, inboxEvent(convID), conversationEvent(convID, "message.added", *note.MessageId))
}

// A PATCH is heard as conversation.changed (L18).
func TestLive_PatchIsHeard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	l := listenLive(t, h)
	setupChannel(t, h)
	c := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view", "communications:conversations-manage")
	m := createConversation(t, c, newConversationBody("x@example.test"))
	l.expect(t, inboxEvent(m.ConversationId), conversationEvent(m.ConversationId, "message.added", *m.MessageId))

	r := c.Do(http.MethodPatch, "/api/v1/communications/conversations/"+m.ConversationId, map[string]any{"status": "closed"})
	if r.Status != http.StatusOK {
		t.Fatalf("patch: status %d body %s", r.Status, r.Body)
	}
	l.expect(t, inboxEvent(m.ConversationId), conversationEvent(m.ConversationId, "conversation.changed", m.ConversationId))
}

// The delivery worker's outcome is heard as message.changed, on the
// conversation's topic, with the message's id (L18).
func TestLive_DeliveryOutcomeIsHeard(t *testing.T) {
	t.Parallel()
	h := newOutboxHarness(t, &fakeSMTP{})
	fx := seedOutboxJob(t, h)
	l := listenLive(t, h)
	if _, err := communications.NewOutboxWorker(h.Deps()).ProcessOne(context.Background()); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	l.expect(t, inboxEvent(fx.conversationID.String()),
		conversationEvent(fx.conversationID.String(), "message.changed", fx.messageID.String()))
}

// Mail the mailbox sync imports is heard as message.added (L18).
func TestLive_InboundMailIsHeard(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	l := listenLive(t, in.h)
	id := in.deliver(in.plainMail("Hello"), in.h.Now().Add(time.Minute), "graph-conv-live")
	in.sync(t)
	m := in.importedMessage(t, id)
	l.expect(t, inboxEvent(m.conversationID.String()),
		conversationEvent(m.conversationID.String(), "message.added", m.id.String()))
}

// The topics' Authorize (L9, L17): the inbox needs conversations-view; a
// conversation needs it and an existing conversation; a key that names
// nothing, a malformed key and a missing permission are all forbidden — the
// same answer, so the topic is no existence oracle.
func TestLive_TopicsAuthorizeAsTheConversationsGET(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	setupChannel(t, h)
	c, viewer := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
	_, outsider := h.SignInUser(t, "communications:channels-manage")
	m := createConversation(t, c, newConversationBody("x@example.test"))

	topics := map[string]live.Topic{}
	for _, tp := range communications.Module().LiveTopics(h.Deps()) {
		topics[tp.Kind] = tp
	}
	inbox, conversation := topics["communications.inbox"], topics["communications.conversation"]
	if inbox.Presence || !conversation.Presence {
		t.Errorf("presence: inbox %v, conversation %v; want only the conversation's", inbox.Presence, conversation.Presence)
	}
	access := h.Deps().Access
	ctx := context.Background()
	for name, tc := range map[string]struct {
		user  uuid.UUID
		topic live.Topic
		key   string
		ok    bool
	}{
		"inbox, a viewer":               {viewer, inbox, "", true},
		"inbox, with a key":             {viewer, inbox, m.ConversationId, false},
		"inbox, no permission":          {outsider, inbox, "", false},
		"conversation, a viewer":        {viewer, conversation, m.ConversationId, true},
		"conversation, no permission":   {outsider, conversation, m.ConversationId, false},
		"conversation, none such":       {viewer, conversation, uuid.NewString(), false},
		"conversation, not an id":       {viewer, conversation, "x", false},
		"conversation, without its key": {viewer, conversation, "", false},
	} {
		err := live.Authorize(ctx, access, tc.user, tc.topic, tc.key)
		if tc.ok && err != nil {
			t.Errorf("%s: %v, want allowed", name, err)
		}
		if !tc.ok && !errors.Is(err, live.ErrForbidden) {
			t.Errorf("%s: %v, want live.ErrForbidden", name, err)
		}
	}
}
