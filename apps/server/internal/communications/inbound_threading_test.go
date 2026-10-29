package communications_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is threading (M15; delivery D, reading 18): In-Reply-To, then the
// References newest first, then Graph's conversation within 90 days, always
// within the channel; a closed conversation reopened unless an automatic
// message answered it; an archived one never written to again.

// importOne delivers m now-ish in Graph's conversation graphID, syncs, and
// answers the imported message.
func (in *inbox) importOne(t *testing.T, m testMail, graphID string) inboundMessage {
	t.Helper()
	in.h.Advance(5 * time.Minute)
	id := in.deliver(m, in.h.Now(), graphID)
	in.sync(t)
	return in.importedMessage(t, id)
}

// TestThreading_Order: In-Reply-To wins over References; without a match for
// it, the newest reference that matches; without either, Graph's
// conversation; without any, a new conversation.
func TestThreading_Order(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	a := in.importOne(t, in.plainMail("a"), "")
	b := in.importOne(t, in.plainMail("b"), "graph-b")
	if a.conversationID == b.conversationID {
		t.Fatal("two unrelated messages share a conversation")
	}

	replyToB := in.plainMail("re b")
	replyToB.InReplyTo, replyToB.References = *b.rfc, *a.rfc
	if got := in.importOne(t, replyToB, "").conversationID; got != b.conversationID {
		t.Errorf("In-Reply-To b, References a: conversation %s, want b's %s", got, b.conversationID)
	}

	newestRef := in.plainMail("refs")
	newestRef.InReplyTo, newestRef.References = "<unknown@example.test>", *a.rfc+" "+*b.rfc
	if got := in.importOne(t, newestRef, "").conversationID; got != b.conversationID {
		t.Errorf("References a b: conversation %s, want the newest reference's, b's %s", got, b.conversationID)
	}

	if got := in.importOne(t, in.plainMail("graph"), "graph-b").conversationID; got != b.conversationID {
		t.Errorf("Graph's conversation: %s, want b's %s", got, b.conversationID)
	}

	fresh := in.importOne(t, in.plainMail("new"), "graph-other")
	if fresh.conversationID == a.conversationID || fresh.conversationID == b.conversationID {
		t.Error("an unrelated message joined a conversation")
	}
}

// TestThreading_AReplyToOurOutboundMessage: a customer's answer names our
// bracketed Message-Id and threads onto the conversation we started.
func TestThreading_AReplyToOurOutboundMessage(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	c := in.h.SignIn(t, "communications:conversations-reply")
	sent := createConversation(t, c, newConversationBody("customer@example.test"))
	reply := in.plainMail("thanks")
	reply.InReplyTo = "<" + expectedMessageID(uuid.MustParse(*sent.MessageId)) + ">"
	if got := in.importOne(t, reply, "").conversationID.String(); got != sent.ConversationId {
		t.Errorf("conversation = %s, want ours %s", got, sent.ConversationId)
	}
}

// TestThreading_GraphConversationWithinNinetyDays: Graph's conversation
// threads onto an inbound message received in the last 90 days, not an older
// one.
func TestThreading_GraphConversationWithinNinetyDays(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	first := in.importOne(t, in.plainMail("first"), "graph-old")
	in.h.Exec(t, `UPDATE communications.conversation_messages SET occurred_at = $2 WHERE id = $1`,
		first.id, in.h.Now().Add(-91*24*time.Hour))
	if got := in.importOne(t, in.plainMail("later"), "graph-old").conversationID; got == first.conversationID {
		t.Error("a message threaded onto Graph's conversation of a message 91 days old")
	}
}

// TestThreading_NeverAcrossChannels: a reply naming a message of another
// channel starts a conversation of its own.
func TestThreading_NeverAcrossChannels(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	other := addInbox(t, in.h, in.fake)
	a := in.importOne(t, in.plainMail("a"), "graph-shared")
	reply := other.plainMail("re a")
	reply.InReplyTo = *a.rfc
	got := other.importOne(t, reply, "graph-shared")
	if got.conversationID == a.conversationID {
		t.Error("a message of one channel threaded onto another channel's conversation")
	}
}

// TestThreading_StatusOfTheMatch: a closed conversation is reopened by a
// person's answer but not by an auto-reply, which the detail marks; an
// archived one is never written to — the answer starts a new conversation.
func TestThreading_StatusOfTheMatch(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	status := func(id uuid.UUID) string {
		return modtest.One[string](t, in.h, `SELECT status FROM communications.conversations WHERE id = $1`, id)
	}
	first := in.importOne(t, in.plainMail("first"), "")
	in.h.Exec(t, `UPDATE communications.conversations SET status = 'closed' WHERE id = $1`, first.conversationID)

	auto := in.plainMail("I am out of office")
	auto.InReplyTo = *first.rfc
	auto.Headers = map[string]string{"Auto-Submitted": "auto-replied"}
	autoMessage := in.importOne(t, auto, "")
	if autoMessage.conversationID != first.conversationID || status(first.conversationID) != "closed" {
		t.Errorf("auto-reply: conversation %s %s, want %s still closed", autoMessage.conversationID, status(first.conversationID), first.conversationID)
	}
	detail := detailMessage(t, conversationDetail(t, in.h, first.conversationID), autoMessage.id)
	if detail["autoSubmitted"] != true {
		t.Errorf("the auto-reply's detail = %v, want autoSubmitted", detail)
	}

	person := in.plainMail("still there?")
	person.InReplyTo = *first.rfc
	if got := in.importOne(t, person, "").conversationID; got != first.conversationID || status(got) != "open" {
		t.Errorf("a person's answer: conversation %s %s, want %s reopened", got, status(got), first.conversationID)
	}

	in.h.Exec(t, `UPDATE communications.conversations SET status = 'archived' WHERE id = $1`, first.conversationID)
	late := in.plainMail("hello again")
	late.InReplyTo = *first.rfc
	got := in.importOne(t, late, "")
	if got.conversationID == first.conversationID || status(got.conversationID) != "open" {
		t.Errorf("an answer to an archived conversation joined it (%s), want a new open one", got.conversationID)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE conversation_id = $1`, first.conversationID); n != 3 {
		t.Errorf("the archived conversation holds %d messages, want its 3", n)
	}
	c := in.h.SignIn(t, "communications:conversations-view")
	if r := c.Do(http.MethodGet, "/api/v1/communications/conversations/"+got.conversationID.String(), nil); r.Status != http.StatusOK {
		t.Errorf("the new conversation: status %d", r.Status)
	}
}
