package communications_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the messages that would hold a channel (delivery D's review,
// M1): what the parser makes safe to store, and the import that fails each
// time — at once for a value Postgres refuses, after three tries for any
// other failure but the provider's or the store's — recorded as failed so the
// page goes on.

func receiptReason(r receipt) string {
	if r.reason == nil {
		return ""
	}
	return *r.reason
}

// TestMailboxSync_NULsAndLongAddressesImport: a message with NULs in its
// subject, body and Message-Id, and one from an address of 321 characters,
// are imported — the NULs gone, the id and the address none.
func TestMailboxSync_NULsAndLongAddressesImport(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	nul := in.plainMail("A body\x00 with a NUL.")
	nul.Subject = "Hei\x00 der"
	nul.MessageID = "<nul\x00-1@example.test>"
	nulID := in.deliver(nul, in.h.Now().Add(time.Minute), "")
	long := in.plainMail("From far away.")
	long.From = "Long <" + strings.Repeat("a", 310) + "@example.no>"
	longID := in.deliver(long, in.h.Now().Add(2*time.Minute), "")
	in.sync(t)

	m := in.importedMessage(t, nulID)
	if m.subject == nil || *m.subject != "Hei der" || m.text == nil || *m.text != "A body with a NUL." || m.rfc != nil {
		t.Errorf("the NUL message = subject %v, text %v, rfc %v", m.subject, m.text, m.rfc)
	}
	l := in.importedMessage(t, longID)
	if l.sender != nil || l.metadata["from"] != "" {
		t.Errorf("the long sender = %v, metadata from %v; want none", l.sender, l.metadata["from"])
	}
	if s := readSync(t, in.h, in.channelID); s.state != "active" {
		t.Errorf("channel_sync = %+v, want active", s)
	}
}

// TestMailboxSync_ANULInGraphsMessageIDIsNoID: the too_large and gone
// receipts keep Graph's internetMessageId only when it is an id.
func TestMailboxSync_ANULInGraphsMessageIDIsNoID(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil, modtest.WithEnv("COMMUNICATIONS_INBOUND_MAX_BYTES", "1048576"))
	large := in.plainMail(strings.Repeat("x", 1<<20))
	large.MessageID = "<large\x00@example.test>"
	id := in.deliver(large, in.h.Now().Add(time.Minute), "")
	in.sync(t)
	r := readReceipts(t, in.h, in.channelID)[id]
	if r.outcome != "skipped" || receiptReason(r) != "too_large" || r.rfc != nil {
		t.Errorf("receipt = %+v, want skipped too_large without an id", r)
	}
}

// poisonTrigger makes inserting a message with subject fail with SQLSTATE
// code, for every try.
func poisonTrigger(t *testing.T, in *inbox, subject, code string) {
	t.Helper()
	in.h.Exec(t, `CREATE FUNCTION communications.test_poison() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'poison' USING ERRCODE = '`+code+`'; END $$`)
	in.h.Exec(t, `CREATE TRIGGER test_poison BEFORE INSERT ON communications.conversation_messages
		FOR EACH ROW WHEN (NEW.subject = '`+subject+`') EXECUTE FUNCTION communications.test_poison()`)
}

// TestMailboxSync_AValuePostgresRefusesFailsAtOnce: an import Postgres
// refuses with a data exception gets a failed import_failed receipt on its
// first try, and the page goes on to the next message.
func TestMailboxSync_AValuePostgresRefusesFailsAtOnce(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	poison := in.plainMail("poison")
	poison.Subject = "poison-22"
	poisonTrigger(t, in, poison.Subject, "22023")
	poisonID := in.deliver(poison, in.h.Now().Add(time.Minute), "")
	nextID := in.deliver(in.plainMail("next"), in.h.Now().Add(2*time.Minute), "")
	in.sync(t)

	receipts := readReceipts(t, in.h, in.channelID)
	if r := receipts[poisonID]; r.outcome != "failed" || receiptReason(r) != "import_failed" || r.messageID != nil {
		t.Errorf("the poison's receipt = %+v, want failed import_failed", r)
	}
	if receipts[nextID].outcome != "imported" {
		t.Errorf("the next message's receipt = %+v, want imported", receipts[nextID])
	}
	if s := readSync(t, in.h, in.channelID); s.state != "active" || s.attempts != 0 || s.errorCode != nil {
		t.Errorf("channel_sync = %+v, want active", s)
	}
}

// TestMailboxSync_ThreeFailedTriesMoveOn: an import that fails otherwise ends
// the cycle without a receipt; the third failed try of the same message gets
// a failed import_failed receipt and the page goes on. A message whose read
// fails at the provider is never given up on.
func TestMailboxSync_ThreeFailedTriesMoveOn(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	poison := in.plainMail("poison")
	poison.Subject = "poison-p0001"
	poisonTrigger(t, in, poison.Subject, "P0001")
	poisonID := in.deliver(poison, in.h.Now().Add(time.Minute), "")
	nextID := in.deliver(in.plainMail("next"), in.h.Now().Add(2*time.Minute), "")

	for try, wait := range []time.Duration{0, time.Minute} {
		in.h.Advance(wait)
		in.sync(t)
		s := readSync(t, in.h, in.channelID)
		if s.errorCode == nil || *s.errorCode != "sync_failed" || s.attempts != int32(try+1) {
			t.Fatalf("after try %d channel_sync = %+v, want sync_failed", try+1, s)
		}
		if n := in.h.Count(t, `SELECT failing_tries FROM communications.channel_sync WHERE channel_id = $1`, in.channelID); n != try+1 {
			t.Fatalf("after try %d failing_tries = %d", try+1, n)
		}
		if len(readReceipts(t, in.h, in.channelID)) != 0 {
			t.Fatalf("after try %d a receipt was written", try+1)
		}
	}
	in.h.Advance(2 * time.Minute)
	in.sync(t)
	receipts := readReceipts(t, in.h, in.channelID)
	if r := receipts[poisonID]; r.outcome != "failed" || receiptReason(r) != "import_failed" {
		t.Errorf("the poison's receipt after three tries = %+v, want failed import_failed", r)
	}
	if receipts[nextID].outcome != "imported" {
		t.Errorf("the next message's receipt = %+v, want imported", receipts[nextID])
	}
	s := readSync(t, in.h, in.channelID)
	if s.state != "active" || s.errorCode != nil || s.attempts != 0 {
		t.Errorf("channel_sync = %+v, want active", s)
	}
	if n := in.h.Count(t, `SELECT failing_tries FROM communications.channel_sync WHERE channel_id = $1 AND failing_item IS NULL`, in.channelID); n != 0 {
		t.Errorf("failing_tries = %d after the page, want the count forgotten", n)
	}

	t.Run("the provider's failures", func(t *testing.T) {
		t.Parallel()
		in := newInbox(t, nil, nil)
		id := in.deliver(in.plainMail("x"), in.h.Now().Add(time.Minute), "")
		in.fake.FailWith("Raw", errors.New("graph did not answer"))
		for range 5 {
			in.h.Advance(time.Hour)
			in.sync(t)
		}
		if _, ok := readReceipts(t, in.h, in.channelID)[id]; ok {
			t.Error("a message whose read failed five times has a receipt")
		}
	})
}

// TestMailboxSync_FromTheChannelItselfIsOwn: a message from the channel's own
// address — one we sent, its Message-Id rewritten by Exchange — is skipped as
// own_message.
func TestMailboxSync_FromTheChannelItselfIsOwn(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	ours := in.plainMail("sent from Vantigo")
	ours.From = "Post <" + strings.ToUpper(in.address) + ">"
	id := in.deliver(ours, in.h.Now().Add(time.Minute), "")
	in.sync(t)
	r := readReceipts(t, in.h, in.channelID)[id]
	if r.outcome != "skipped" || receiptReason(r) != "own_message" || r.messageID != nil {
		t.Errorf("receipt = %+v, want skipped own_message", r)
	}
	if n := countInbound(t, in.h, in.channelID); n != 0 {
		t.Errorf("%d messages, want none", n)
	}
}

// TestMailboxSync_GraphsMessageIDIsStoredWhenTheHeaderHasNone: a message with
// no Message-Id header is stored with Graph's internetMessageId, the id its
// duplicate check used, so a second copy is a duplicate.
func TestMailboxSync_GraphsMessageIDIsStoredWhenTheHeaderHasNone(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	mail := in.plainMail("no Message-Id")
	mail.MessageID = ""
	graphID := "<graph-1@example.test>"
	at := in.h.Now().Add(time.Minute)
	in.fake.AddInbox(in.mailbox.Mailbox, contracts.MailItem{ID: "AAMk-first", ReceivedAt: at, MessageID: graphID}, mail.bytes())
	in.fake.AddInbox(in.mailbox.Mailbox, contracts.MailItem{ID: "AAMk-copy", ReceivedAt: at.Add(time.Second), MessageID: graphID}, mail.bytes())
	in.sync(t)

	m := in.importedMessage(t, "AAMk-first")
	if m.rfc == nil || *m.rfc != graphID {
		t.Errorf("rfc_message_id = %v, want Graph's %s", m.rfc, graphID)
	}
	receipts := readReceipts(t, in.h, in.channelID)
	if r := receipts["AAMk-copy"]; r.outcome != "duplicate" || r.messageID == nil || *r.messageID != m.id {
		t.Errorf("the copy's receipt = %+v, want a duplicate of the first", r)
	}
}

// TestMailboxSync_AFinishedPassClearsNotified: delivery E's notice flag is
// cleared once a pass finishes.
func TestMailboxSync_AFinishedPassClearsNotified(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	in.h.Exec(t, `UPDATE communications.channel_sync SET notified = true WHERE channel_id = $1`, in.channelID)
	in.sync(t)
	if n := in.h.Count(t, `SELECT count(*) FROM communications.channel_sync WHERE channel_id = $1 AND notified`, in.channelID); n != 0 {
		t.Error("notified stayed set after a finished pass")
	}
}
