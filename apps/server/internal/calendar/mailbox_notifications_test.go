package calendar_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The change notifications' side of the calendar-mailbox-sync worker
// (calendar Outlook sync design O11.6), against modtest's fake mailbox
// provider: Watch last in a sync that pushed and left the person active,
// Unwatch in every clean-up but a target change's, and Notified each cycle
// making the notified people's syncs due. Every harness also holds the lock
// rule over each test (newHarness).

// mailboxClient is a person's client with the revision their setting was
// last written at.
type mailboxClient struct {
	*modtest.Client
	revision int32
}

// watchedPerson is a person with one appointment whose sync is on and has
// run once, at the harness's now.
func watchedPerson(t *testing.T, r *mailboxRig) (*mailboxClient, uuid.UUID, string) {
	t.Helper()
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	if !r.box.Watched(mb) {
		t.Fatal("the first sync did not watch the mailbox")
	}
	return &mailboxClient{Client: c, revision: on.Revision}, me, mb
}

func TestMailboxNotifications_ASyncThatWorkedWatchesTheMailboxLast(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, _, mb := watchedPerson(t, r)
	if got := r.box.WatchCalls(); !slices.Equal(got, []string{mb}) {
		t.Errorf("Watch calls = %v, want the mailbox once", got)
	}
	// Last: nothing is asked of the provider after it.
	list := r.box.Calls()
	if last := list[len(list)-1]; last.Method != "Watch" || last.Mailbox != mb {
		t.Errorf("the last call = %+v, want Watch", last)
	}
	// Each sync asks again: the provider decides whether to renew.
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
	if n := len(r.box.WatchCalls()); n != 2 {
		t.Errorf("Watch calls %d after the second sync, want 2", n)
	}
}

func TestMailboxNotifications_APushThatStoodBesideAFailedReadStillWatches(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"Delta", "Versions"} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			turnOn(t, c, "default", "full", "calendar.appointment")
			r.box.FailWith(method, errors.New("graph: 503"))
			mailboxCycle(t, r)
			if row := readSyncRow(t, r, me); row.state != "active" || row.errCode == nil || *row.errCode != "failed" {
				t.Fatalf("the row = %+v, want active and failed", row)
			}
			if !r.box.Watched(mb) {
				t.Error("a push that stood did not watch the mailbox")
			}
		})
	}
}

func TestMailboxNotifications_ARefusalOrAFailureDoesNotWatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		fail  func(r *mailboxRig, mb string)
		state string
	}{
		{"denied", func(r *mailboxRig, mb string) { r.box.SetMailboxError(mb, contracts.ErrMailboxDenied) }, "unavailable"},
		{"gone", func(r *mailboxRig, mb string) { r.box.SetMailboxError(mb, contracts.ErrMailboxGone) }, "unavailable"},
		{"the push failed", func(r *mailboxRig, _ string) { r.box.FailWith("Apply", errors.New("graph: 503")) }, "pending"},
		{"the busy read refused", func(r *mailboxRig, _ string) { r.box.FailWith("Delta", contracts.ErrMailboxDenied) }, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			turnOn(t, c, "default", "full", "calendar.appointment")
			tc.fail(r, mb)
			mailboxCycle(t, r)
			if row := readSyncRow(t, r, me); row.state != tc.state {
				t.Fatalf("the row = %+v, want %s", row, tc.state)
			}
			if got := r.box.WatchCalls(); len(got) != 0 {
				t.Errorf("Watch was asked for %v", got)
			}
		})
	}
}

func TestMailboxNotifications_AWatchErrorHidesNothing(t *testing.T) {
	t.Parallel()
	for _, err := range []error{errors.New("graph: 503"), contracts.ErrMailboxDenied, contracts.ErrMailboxGone} {
		t.Run(err.Error(), func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			turnOn(t, c, "default", "full", "calendar.appointment")
			r.box.FailWith("Watch", err)
			mailboxCycle(t, r)
			row := readSyncRow(t, r, me)
			if row.state != "active" || row.errCode != nil || row.attempts != 0 ||
				row.next == nil || !row.next.Equal(r.Now().Add(15*time.Minute)) {
				t.Errorf("the row = %+v, want active, no error, a poll later", row)
			}
			if got := getSync(t, c); got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(r.Now()) {
				t.Errorf("lastSyncedAt %v, want %v", got.LastSyncedAt, r.Now())
			}
			if len(r.box.EventsIn(mb, "")) != 1 || mapped(t, r, me) != 1 {
				t.Errorf("events %d, mapped %d: the push did not stand", len(r.box.EventsIn(mb, "")), mapped(t, r, me))
			}
			if r.box.Watched(mb) {
				t.Error("a refused Watch is watched")
			}
			if strings.Contains(r.Logs(), mb) {
				t.Error("the mailbox was logged")
			}
		})
	}
}

func TestMailboxNotifications_EveryCleanUpButATargetChangeUnwatches(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		stop func(t *testing.T, r *mailboxRig, c *mailboxClient, me uuid.UUID)
	}{
		{"turned off", func(t *testing.T, _ *mailboxRig, c *mailboxClient, _ uuid.UUID) {
			putSync(t, c.Client, c.revision, syncBody(false, "default", "full", "calendar.appointment"))
		}},
		{"calendar:access lost", func(t *testing.T, r *mailboxRig, _ *mailboxClient, me uuid.UUID) {
			r.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, me)
		}},
		{"the account gone", func(_ *testing.T, r *mailboxRig, _ *mailboxClient, me uuid.UUID) {
			r.staff.set(me, nil)
		}},
		{"the admin switch off", func(_ *testing.T, r *mailboxRig, _ *mailboxClient, _ uuid.UUID) {
			r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := watchedPerson(t, r)
			tc.stop(t, r, c, me)
			r.Advance(16 * time.Minute)
			mailboxCycle(t, r)
			if len(r.box.EventsIn(mb, "")) != 0 {
				t.Fatal("the clean-up did not run")
			}
			if got := r.box.UnwatchCalls(); !slices.Equal(got, []string{mb}) {
				t.Errorf("Unwatch calls = %v, want the mailbox once", got)
			}
			if r.box.Watched(mb) {
				t.Error("the mailbox is still watched")
			}
			if n := len(r.box.WatchCalls()); n != 1 {
				t.Errorf("Watch calls %d, want only the first sync's", n)
			}
		})
	}
}

func TestMailboxNotifications_AMovedMailboxUnwatchesTheOldOne(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, me, mb := watchedPerson(t, r)
	r.staff.set(me, &contracts.DirectoryAccount{Provider: "entra", ExternalID: "oid-new", Enabled: true})
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
	if got := r.box.UnwatchCalls(); !slices.Equal(got, []string{mb}) {
		t.Errorf("Unwatch calls = %v, want the old mailbox", got)
	}
	if r.box.Watched(mb) || !r.box.Watched("oid-new") {
		t.Errorf("watched: old %v, new %v", r.box.Watched(mb), r.box.Watched("oid-new"))
	}
}

func TestMailboxNotifications_ATargetChangeKeepsTheSubscription(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := watchedPerson(t, r)
	putSync(t, c.Client, c.revision, syncBody(true, "own", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	if len(r.box.Calendars(mb)) != 1 || readSyncRow(t, r, me).state != "active" {
		t.Fatalf("the move did not run: calendars %v, state %s", r.box.Calendars(mb), readSyncRow(t, r, me).state)
	}
	if got := r.box.UnwatchCalls(); len(got) != 0 {
		t.Errorf("a target change unwatched %v", got)
	}
	if !r.box.Watched(mb) {
		t.Error("the mailbox is no longer watched")
	}
}

func TestMailboxNotifications_AnUnwatchRefusedKeepsTheCleanUpOwed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"denied", contracts.ErrMailboxDenied, "mailbox_denied"},
		{"the provider unusable", contracts.ErrMailboxUnavailable, "unavailable"},
		{"a failure", errors.New("graph: 503"), "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := watchedPerson(t, r)
			r.box.FailWith("Unwatch", tc.err)
			putSync(t, c.Client, c.revision, syncBody(false, "default", "full", "calendar.appointment"))
			mailboxCycle(t, r)
			row := readSyncRow(t, r, me)
			if row.state != "stopping" || row.errCode == nil || *row.errCode != tc.code || row.attempts != 1 ||
				row.next == nil || !row.next.Equal(r.Now().Add(time.Minute)) {
				t.Errorf("the row = %+v, want stopping, %s, in a minute", row, tc.code)
			}
			// The copies went first; the subscription is still owed.
			if len(r.box.EventsIn(mb, "")) != 0 || !r.box.Watched(mb) {
				t.Errorf("events %d, watched %v", len(r.box.EventsIn(mb, "")), r.box.Watched(mb))
			}
			r.box.FailWith("Unwatch", nil)
			r.Advance(time.Minute)
			mailboxCycle(t, r)
			if row := readSyncRow(t, r, me); row.state != "off" || r.box.Watched(mb) || mapped(t, r, me) != 0 {
				t.Errorf("after the retry: the row %+v, watched %v, mapped %d", row, r.box.Watched(mb), mapped(t, r, me))
			}
		})
	}
	for _, err := range []error{contracts.ErrMailboxGone, contracts.ErrMailboxUnknown} {
		t.Run(err.Error(), func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := watchedPerson(t, r)
			r.box.FailWith("Unwatch", err)
			putSync(t, c.Client, c.revision, syncBody(false, "default", "full", "calendar.appointment"))
			mailboxCycle(t, r)
			if row := readSyncRow(t, r, me); row.state != "off" || row.errCode != nil || mapped(t, r, me) != 0 {
				t.Errorf("the row = %+v, mapped %d: want off and forgotten", row, mapped(t, r, me))
			}
			if strings.Contains(r.Logs(), mb) {
				t.Error("the mailbox was logged")
			}
		})
	}
}

// lastSynced is the person's last_synced_at.
func lastSynced(t *testing.T, r *mailboxRig, id uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := r.Pool().QueryRow(context.Background(), `SELECT last_synced_at FROM calendar.mailbox_sync WHERE user_id = $1`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestMailboxNotifications_ANoticeMakesTheRightSyncsDue(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	cycle := func() int {
		t.Helper()
		ran, n, err := w.RunCycle(context.Background())
		if err != nil || !ran {
			t.Fatalf("cycle: ran %v, %v", ran, err)
		}
		return n
	}
	_, notified, mbNotified := watchedPerson(t, r)
	_, before, mbBefore := watchedPerson(t, r)
	_, other, _ := watchedPerson(t, r)
	off, offID, mbOff := watchedPerson(t, r)
	putSync(t, off.Client, off.revision, syncBody(false, "default", "full", "calendar.appointment"))
	if n := cycle(); n != 1 || readSyncRow(t, r, offID).state != "off" {
		t.Fatalf("turning off: synced %d, state %s", n, readSyncRow(t, r, offID).state)
	}
	synced := r.Now()

	r.Advance(5 * time.Minute)
	// Named as the provider holds it: compared whatever its case.
	r.box.Notify(strings.ToUpper(mbNotified), synced.Add(4*time.Minute))
	// A change before the person's last sync started: that sync read it.
	r.box.Notify(mbBefore, synced.Add(-time.Minute))
	r.box.Notify(mbOff, synced.Add(4*time.Minute))
	r.box.Notify("oid-nobody", synced.Add(4*time.Minute))
	if n := cycle(); n != 1 {
		t.Fatalf("synced %d, want the notified person alone", n)
	}
	if !lastSynced(t, r, notified).Equal(r.Now()) {
		t.Errorf("the notified person was not synced: last %v", lastSynced(t, r, notified))
	}
	for name, id := range map[string]uuid.UUID{"synced after the notice": before, "another mailbox's": other} {
		if row := readSyncRow(t, r, id); row.next == nil || !row.next.Equal(synced.Add(15*time.Minute)) {
			t.Errorf("%s: next %v, want the poll's", name, row.next)
		}
	}
	if row := readSyncRow(t, r, offID); row.next != nil {
		t.Errorf("a row turned off was made due at %v", row.next)
	}

	// The notice is taken once: the next cycle asks from its At, and the
	// person is not made due again.
	from := len(r.box.Calls())
	r.Advance(time.Minute)
	if n := cycle(); n != 0 {
		t.Errorf("synced %d on the next cycle, want none", n)
	}
	asked := calls(r, from, "Notified")
	if len(asked) != 1 || !asked[0].From.Equal(synced.Add(4*time.Minute)) {
		t.Errorf("Notified asked %+v, want from the latest notice taken", asked)
	}
}

func TestMailboxNotifications_TheFirstCycleAsksFromAPollAgo(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	mailboxCycle(t, r)
	asked := calls(r, 0, "Notified")
	if len(asked) != 1 || !asked[0].From.Equal(r.Now().Add(-15*time.Minute)) {
		t.Errorf("Notified asked %+v, want from a poll ago", asked)
	}
}

func TestMailboxNotifications_AFailingNotifiedDoesNotStopTheCycle(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.FailWith("Notified", errors.New("entra: the database is down"))
	ran, n, err := w.RunCycle(context.Background())
	if err != nil || !ran || n != 1 {
		t.Fatalf("cycle: ran %v, synced %d, %v", ran, n, err)
	}
	if row := readSyncRow(t, r, me); row.state != "active" {
		t.Errorf("the row = %+v", row)
	}
	// Nothing was taken, so the next cycle asks from the same instant.
	first := calls(r, 0, "Notified")[0].From
	r.box.FailWith("Notified", nil)
	r.box.Notify(mb, r.Now().Add(time.Second))
	from := len(r.box.Calls())
	r.Advance(time.Minute)
	if _, _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if asked := calls(r, from, "Notified"); len(asked) != 1 || !asked[0].From.Equal(first) {
		t.Errorf("Notified asked %+v, want from %v", asked, first)
	}
}
