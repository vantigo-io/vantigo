package communications_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// When change notifications are active (PUBLIC_BASE_URL set and
// CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED=1), a finished sync pass calls
// Watch(mailbox, "inbox") and schedules the next sync 1 hour ahead as a
// safety net; when Watch fails, it falls back to the 5-minute poll (M14 step
// 3, M17).
func TestMailboxSync_NotificationsActiveWatchesAndSetsSafetyInterval(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil, modtest.WithEnv("PUBLIC_BASE_URL", "https://vantigo.example"))

	in.sync(t)
	s := readSync(t, in.h, in.channelID)
	if !s.nextSyncAt.Equal(in.h.Now().Add(time.Hour)) {
		t.Errorf("nextSyncAt with notifications active = %v, want %v (now + 1h)", s.nextSyncAt, in.h.Now().Add(time.Hour))
	}
	if !in.fake.Watched(in.mailbox.Mailbox) {
		t.Errorf("Watched(%s) = false, want true", in.mailbox.Mailbox)
	}

	// Advance 1 hour and make Watch fail -> falls back to 5-minute poll.
	in.h.Advance(time.Hour)
	in.fake.FailWith("Watch", errors.New("graph: 503"))
	in.sync(t)
	s = readSync(t, in.h, in.channelID)
	if !s.nextSyncAt.Equal(in.h.Now().Add(5 * time.Minute)) {
		t.Errorf("nextSyncAt when Watch fails = %v, want %v (now + 5m)", s.nextSyncAt, in.h.Now().Add(5*time.Minute))
	}
}

// A mail change notice marks the channel notified and due at now, and the
// worker claims notified channels before unnotified ones that were due
// earlier; finishing the sync clears notified (M17, Testing › E).
func TestMailboxSync_NoticeWakesChannelAndOrdersNotifiedFirst(t *testing.T) {
	t.Parallel()
	first := newInbox(t, nil, nil, modtest.WithEnv("PUBLIC_BASE_URL", "https://vantigo.example"))
	second := addInbox(t, first.h, first.fake)

	// Finish initial sync on both channels -> both scheduled at now + 1h.
	first.sync(t)

	// Make first due 1 minute ago (unnotified), while second is still 50m in
	// the future, then emit a notice for second's mailbox.
	first.h.Advance(10 * time.Minute)
	now := first.h.Now()
	first.h.Exec(t, `UPDATE communications.channel_sync SET next_sync_at = $1 WHERE channel_id = $2`,
		now.Add(-time.Minute), first.channelID)
	second.deliver(second.plainMail("urgent"), now, "conv-second")
	first.fake.Notify(second.mailbox.Mailbox, now)

	w := first.worker()
	// First ProcessOne takes the notice and must claim second (notified) before first!
	processed, err := w.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("first ProcessOne = %v, %v", processed, err)
	}
	if n := countInbound(t, first.h, second.channelID); n != 1 {
		t.Errorf("second channel inbound after first ProcessOne = %d, want 1 (notified channel claimed first)", n)
	}
	var notified bool
	if err := first.h.Pool().QueryRow(context.Background(),
		`SELECT notified FROM communications.channel_sync WHERE channel_id = $1`, second.channelID).Scan(&notified); err != nil {
		t.Fatalf("scan notified: %v", err)
	}
	if notified {
		t.Error("second channel notified flag still true after finished sync, want false")
	}

	// Second ProcessOne claims first.
	if processed, err := w.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("second ProcessOne = %v, %v", processed, err)
	}
}
