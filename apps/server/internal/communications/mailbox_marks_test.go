package communications_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func closeConversation(t *testing.T, h *modtest.Harness, convID uuid.UUID) {
	t.Helper()
	agent := h.SignIn(t, "communications:conversations-view", "communications:conversations-manage")
	r := agent.Do(http.MethodPatch, "/api/v1/communications/conversations/"+convID.String(), map[string]any{
		"status": "closed",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("close conversation %s: status %d body %s", convID, r.Status, r.Body)
	}
}

// Closing a conversation on a Microsoft 365 channel applies its configured
// onClose rule (none, read, or move) to the conversation's imported inbound
// messages through communications.mailbox_marks, and re-closing after a
// customer reply reopens the conversation marks the new message too (M17,
// Testing › E).
func TestMailboxMarks_OnCloseReadAndMove(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	admin := in.h.SignIn(t, "communications:channels-manage")

	// 1. Default onClose=none: closing a conversation enqueues no mailbox_marks row.
	id1 := in.deliver(in.plainMail("first"), in.h.Now(), "conv-none")
	in.sync(t)
	conv1 := in.importedMessage(t, id1).conversationID
	closeConversation(t, in.h, conv1)
	if n := in.h.Count(t, `SELECT count(*) FROM communications.mailbox_marks WHERE conversation_id = $1`, conv1); n != 0 {
		t.Errorf("mailbox_marks rows for onClose=none = %d, want 0", n)
	}

	// 2. Update channel to onClose=read: closing marks all inbound messages as read.
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+in.channelID.String(), map[string]any{
		"onClose": "read",
	}); r.Status != http.StatusOK {
		t.Fatalf("set onClose=read: %d %s", r.Status, r.Body)
	}
	in.h.Advance(5 * time.Minute)
	m2a := in.plainMail("question part 1")
	id2a := in.deliver(m2a, in.h.Now(), "conv-read")
	m2b := in.plainMail("question part 2")
	m2b.InReplyTo = m2a.MessageID
	id2b := in.deliver(m2b, in.h.Now().Add(time.Second), "conv-read")
	in.sync(t)
	conv2 := in.importedMessage(t, id2a).conversationID

	closeConversation(t, in.h, conv2)
	in.sync(t)

	for _, id := range []string{id2a, id2b} {
		state, ok := in.fake.MessageState(in.mailbox.Mailbox, id)
		if !ok || !state.IsRead {
			t.Errorf("MessageState(%s) = %+v, %v, want IsRead=true", id, state, ok)
		}
	}
	if st := modtest.One[string](t, in.h, `SELECT status FROM communications.mailbox_marks WHERE conversation_id = $1`, conv2); st != "completed" {
		t.Errorf("mailbox_marks status = %q, want completed", st)
	}

	// 3. Update channel to onClose=move (default folder "Handled in Vantigo"),
	// receive a reply on conv2 (reopening it), and close it again -> moves all
	// inbound messages of conv2 to the ensured folder.
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+in.channelID.String(), map[string]any{
		"onClose": "move",
	}); r.Status != http.StatusOK {
		t.Fatalf("set onClose=move: %d %s", r.Status, r.Body)
	}
	in.h.Advance(5 * time.Minute)
	m2c := in.plainMail("follow-up")
	m2c.InReplyTo = m2b.MessageID
	id2c := in.deliver(m2c, in.h.Now(), "conv-read")
	in.sync(t)

	closeConversation(t, in.h, conv2)
	in.sync(t)

	state2c, ok := in.fake.MessageState(in.mailbox.Mailbox, id2c)
	if !ok || state2c.FolderName != "Handled in Vantigo" {
		t.Errorf("MessageState(%s) = %+v, %v, want FolderName='Handled in Vantigo'", id2c, state2c, ok)
	}
}

// ErrMailItemGone is treated as already handled; ErrMailboxUnavailable holds
// the job without spending an attempt; transient errors retry with backoff up
// to 8 attempts and then mark the row failed (M17, Testing › E).
func TestMailboxMarks_IdempotentOnGoneAndRetriesAndHolds(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	admin := in.h.SignIn(t, "communications:channels-manage")
	if r := admin.Do(http.MethodPut, "/api/v1/communications/channels/"+in.channelID.String(), map[string]any{
		"onClose": "read",
	}); r.Status != http.StatusOK {
		t.Fatalf("set onClose=read: %d %s", r.Status, r.Body)
	}

	id := in.deliver(in.plainMail("hello"), in.h.Now(), "conv-retry")
	in.sync(t)
	convID := in.importedMessage(t, id).conversationID
	closeConversation(t, in.h, convID)

	w := in.worker()

	// 1. Provider unavailable -> held without spending an attempt.
	in.fake.FailWith("Mark", contracts.ErrMailboxUnavailable)
	if _, err := w.ProcessOneMark(context.Background()); err != nil {
		t.Fatalf("ProcessOneMark held: %v", err)
	}
	if got := modtest.One[string](t, in.h,
		`SELECT status || ':' || COALESCE(last_error, '') || ':' || attempts::text FROM communications.mailbox_marks WHERE conversation_id = $1`,
		convID); got != "retry:provider_unavailable:0" {
		t.Errorf("after hold = %q, want retry:provider_unavailable:0", got)
	}

	// 2. ErrMailItemGone -> treated as done, job completes.
	in.h.Advance(5 * time.Minute)
	in.fake.FailWith("Mark", contracts.ErrMailItemGone)
	if _, err := w.ProcessOneMark(context.Background()); err != nil {
		t.Fatalf("ProcessOneMark gone: %v", err)
	}
	if st := modtest.One[string](t, in.h, `SELECT status FROM communications.mailbox_marks WHERE conversation_id = $1`, convID); st != "completed" {
		t.Errorf("after ErrMailItemGone status = %q, want completed", st)
	}

	// 3. Re-open and re-close, then inject transient failures across all 8 attempts -> failed.
	agent := in.h.SignIn(t, "communications:conversations-view", "communications:conversations-manage")
	agent.Do(http.MethodPatch, "/api/v1/communications/conversations/"+convID.String(), map[string]any{"status": "open"})
	closeConversation(t, in.h, convID)
	in.fake.FailWith("Mark", errors.New("graph: 503"))
	for attempt := 1; attempt <= 8; attempt++ {
		if _, err := w.ProcessOneMark(context.Background()); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		in.h.Advance(time.Hour)
	}
	if got := modtest.One[string](t, in.h,
		`SELECT status || ':' || COALESCE(last_error, '') || ':' || attempts::text FROM communications.mailbox_marks WHERE conversation_id = $1`,
		convID); got != "failed:mark_failed:8" {
		t.Errorf("after 8 failed attempts = %q, want failed:mark_failed:8", got)
	}
}
