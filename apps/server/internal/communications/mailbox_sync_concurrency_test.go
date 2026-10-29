package communications_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the mailbox sync's lease (delivery D, reading 13): the
// conditional claim is the lock, every write of a cycle is guarded by the
// lease, and the receipts' unique index decides what two holders of one
// channel race on.

// rawHookMailbox is the fake with a hook run inside the first Raw only — a
// Raw the hook itself makes included, which a sync.Once would deadlock on.
type rawHookMailbox struct {
	*modtest.FakeChannelMailbox
	fired  atomic.Bool
	during func()
}

func (m *rawHookMailbox) Raw(ctx context.Context, mailbox, messageID string, max int64) ([]byte, error) {
	if m.during != nil && m.fired.CompareAndSwap(false, true) {
		m.during()
	}
	return m.FakeChannelMailbox.Raw(ctx, mailbox, messageID, max)
}

// TestMailboxSync_ALostLeaseWritesNothing: the row taken by another lease id
// while the first message is read — the import of that message commits (its
// receipt decides) — then the renewal before the second finds the lease gone:
// the cycle stops with no link stored and nothing written on the row.
func TestMailboxSync_ALostLeaseWritesNothing(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	slot := &rawHookMailbox{FakeChannelMailbox: fake}
	in := newInbox(t, slot, fake)
	at := in.h.Now().Add(time.Minute)
	in.deliver(in.plainMail("one"), at, "")
	in.deliver(in.plainMail("two"), at.Add(time.Second), "")
	slot.during = func() {
		in.h.Exec(t, `UPDATE communications.channel_sync SET lease_id = 'another0another0another0another0' WHERE channel_id = $1`, in.channelID)
	}

	if processed, err := in.worker().ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	s := readSync(t, in.h, in.channelID)
	if s.deltaLink != nil || s.state != "pending" || s.errorCode != nil || s.attempts != 0 ||
		s.leaseID == nil || *s.leaseID != "another0another0another0another0" {
		t.Errorf("channel_sync = %+v, want the other holder's row untouched: no link, no state, no error", s)
	}
	if n := countInbound(t, in.h, in.channelID); n != 1 {
		t.Errorf("%d messages imported, want the one read before the lease was lost", n)
	}
	if got := len(rawCalls(fake)); got != 1 {
		t.Errorf("Raw calls = %d, want the cycle to stop before the second message", got)
	}
}

// lookupHookDirectory is the package's directory with a hook run inside the
// first ContactsByEmail only: the linker's lookup, made after an import has
// stored its objects and before its transaction.
type lookupHookDirectory struct {
	fakeDirectory
	fired  atomic.Bool
	during func()
}

func (d *lookupHookDirectory) ContactsByEmail(ctx context.Context, email string) ([]contracts.ContactMatch, error) {
	if d.during != nil && d.fired.CompareAndSwap(false, true) {
		d.during()
	}
	return d.fakeDirectory.ContactsByEmail(ctx, email)
}

// TestMailboxSync_OverlappingHoldersImportEachMessageOnce: the first worker's
// lease runs out while it imports the first message, its objects already
// stored; a second worker claims the channel and imports both messages. The
// first's transaction then loses on the receipt — its object goes to the
// cleanup ledger, pending — and its renewal finds the lease gone. Each message
// is imported once.
func TestMailboxSync_OverlappingHoldersImportEachMessageOnce(t *testing.T) {
	t.Parallel()
	directory := &lookupHookDirectory{}
	in := newInbox(t, nil, nil, modtest.WithDirectory(directory))
	at := in.h.Now().Add(time.Minute)
	in.deliver(in.plainMail("one"), at, "")
	in.deliver(in.plainMail("two"), at.Add(time.Second), "")

	second := in.worker()
	directory.during = func() {
		in.h.Advance(3 * time.Minute) // past the first holder's two-minute lease
		if processed, err := second.ProcessOne(context.Background()); err != nil || !processed {
			t.Errorf("the second holder's ProcessOne = %v, %v", processed, err)
		}
	}
	if processed, err := in.worker().ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("the first holder's ProcessOne = %v, %v", processed, err)
	}

	if n := countInbound(t, in.h, in.channelID); n != 2 {
		t.Errorf("%d messages, want each of the two once", n)
	}
	if n := len(readReceipts(t, in.h, in.channelID)); n != 2 {
		t.Errorf("%d receipts, want 2", n)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'owned'`); n != 2 {
		t.Errorf("%d owned objects, want the two originals the second holder imported", n)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE status = 'pending'`); n != 1 {
		t.Errorf("%d objects queued for deletion, want the loser's one original", n)
	}
	if s := readSync(t, in.h, in.channelID); s.state != "active" || s.deltaLink == nil || s.leaseID != nil {
		t.Errorf("channel_sync = %+v, want the second holder's finished cycle", s)
	}
}

// TestMailboxSync_AHolderBehindAnotherStoresNothing: the second
// holder imports the message while the first reads it; the first then finds
// the Message-Id imported and stores nothing at all — its duplicate receipt
// loses to the second's on the provider id.
func TestMailboxSync_AHolderBehindAnotherStoresNothing(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	slot := &rawHookMailbox{FakeChannelMailbox: fake}
	in := newInbox(t, slot, fake)
	id := in.deliver(in.plainMail("one"), in.h.Now().Add(time.Minute), "")
	second := in.worker()
	slot.during = func() {
		in.h.Advance(3 * time.Minute)
		if _, err := second.ProcessOne(context.Background()); err != nil {
			t.Errorf("the second holder's ProcessOne: %v", err)
		}
	}
	if _, err := in.worker().ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := readReceipts(t, in.h, in.channelID); len(r) != 1 || r[id].outcome != "imported" {
		t.Errorf("receipts = %+v, want the second holder's one imported", r)
	}
	if n := in.h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records`); n != 1 {
		t.Errorf("%d cleanup records, want the second holder's original only", n)
	}
}
