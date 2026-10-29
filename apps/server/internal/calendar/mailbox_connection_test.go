package calendar_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The calendar-mailbox-sync worker on the person's own connection to the
// provider (Outlook connection design M9, M7), against modtest's fake:
// ErrConnectionNeeded parks the person a day with the attempts unchanged,
// Connections wakes them within a cycle, and a connection gone runs the
// clean-up — by the removals a closing connection serves, or forgetting the
// copies once none can.

// removalCalls is the clean-up's calls among the fake's since the first n.
func removalCalls(r *mailboxRig, from int) []string {
	var out []string
	for _, c := range r.box.Calls()[from:] {
		switch c.Method {
		case "Find", "Apply", "DeleteCalendar", "Unwatch":
			out = append(out, c.Method)
		}
	}
	return out
}

// wantParked fails unless the person's row is parked with code: state
// (unavailable, or stopping with a clean-up owed), the next sync a day
// from now, the attempts as given.
func wantParked(t *testing.T, r *mailboxRig, row syncRow, state, code string, attempts int) {
	t.Helper()
	if row.state != state || row.errCode == nil || *row.errCode != code || row.attempts != attempts ||
		row.next == nil || !row.next.Equal(r.Now().Add(24*time.Hour)) || !row.reconcile {
		t.Errorf("the row = %+v, want %s, %s, attempts %d, a day from now (%v)", row, state, code, attempts, r.Now().Add(24*time.Hour))
	}
}

func TestMailboxConnection_NotConnectedParksTheDayWithTheAttemptsUnchanged(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	// A failure first, so the attempts are not zero.
	r.box.FailWith("Apply", errors.New("graph: 503"))
	mailboxCycle(t, r)
	if row := readSyncRow(t, r, me); row.attempts != 1 {
		t.Fatalf("the row = %+v, want one failure", row)
	}
	r.box.FailWith("Apply", nil)
	r.box.SetConnection(mb, contracts.ConnectionNotConnected)
	r.Advance(time.Minute)
	from := len(r.box.Calls())
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 1)
	if got := getSync(t, c); got.Availability != gen.MailboxSyncAvailabilityNotConnected ||
		got.Error == nil || *got.Error != gen.MailboxSyncErrorNotConnected {
		t.Errorf("availability %s, error %v; want not_connected", got.Availability, got.Error)
	}
	// Never synced: no clean-up is owed.
	if got := removalCalls(r, from); len(got) != 0 {
		t.Errorf("a person who never synced was cleaned up: %v", got)
	}
	if !strings.Contains(r.Logs(), "not_connected") || strings.Contains(r.Logs(), mb) {
		t.Error("the parking was not logged by its code, or the mailbox was logged")
	}
}

func TestMailboxConnection_AReconnectParksAndKeepsTheCopies(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := watchedPerson(t, r)
	r.box.SetConnection(mb, "mfa")
	r.Advance(16 * time.Minute)
	from := len(r.box.Calls())
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "reconnect", 0)
	if got := getSync(t, c.Client); got.Availability != gen.MailboxSyncAvailabilityReconnect {
		t.Errorf("availability %s, want reconnect", got.Availability)
	}
	if mapped(t, r, me) != 1 || len(r.box.EventsIn(mb, "")) != 1 || !r.box.Watched(mb) {
		t.Errorf("mapped %d, events %d, watched %v: a reconnect keeps the copies",
			mapped(t, r, me), len(r.box.EventsIn(mb, "")), r.box.Watched(mb))
	}
	if got := removalCalls(r, from); len(got) != 0 {
		t.Errorf("a reconnect cleaned up: %v", got)
	}
}

// A parked person is claimed again only after the day — the long re-check
// — and is woken at the next cycle by their connection's change.
func TestMailboxConnection_AParkedPersonIsReCheckedADayLaterAndWokenByAChange(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	cycle := func() int {
		t.Helper()
		n, err := w.RunCycle(context.Background())
		if err != nil {
			t.Fatalf("cycle: %v", err)
		}
		return n
	}
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.SetConnection(mb, contracts.ConnectionNotConnected)
	if n := cycle(); n != 1 {
		t.Fatalf("synced %d, want the person", n)
	}
	r.Advance(24*time.Hour - time.Minute)
	if n := cycle(); n != 0 {
		t.Fatalf("a parked person was synced %d times before the day was out", n)
	}
	r.Advance(time.Minute)
	from := len(r.box.Calls())
	ctx, log := recordContractCalls(context.Background())
	if n, err := w.RunCycle(ctx); err != nil || n != 1 {
		t.Fatalf("the long re-check synced %d, %v; want the person", n, err)
	}
	if asked := calls(r, from, "Versions"); len(asked) != 1 {
		t.Errorf("the re-check asked Versions %d times, want once", len(asked))
	}
	// The cycle's steps, in order, before the provider's call that parks.
	steps := []string{"MailboxCalendar.Status", "Access.UserHolds", "Workforce.DirectoryAccount", "MailboxCalendar.Versions"}
	rest := log.list()
	for _, step := range steps {
		i := slices.Index(rest, step)
		if i < 0 {
			t.Errorf("the re-check's calls %v: want %v in order", log.list(), steps)
			break
		}
		rest = rest[i+1:]
	}
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)

	// The person connects: the change wakes them within a cycle.
	r.Advance(time.Hour)
	r.box.SetConnection(mb, "")
	at := r.Now().Add(-10 * time.Second)
	r.box.ChangeConnection(strings.ToUpper(mb), contracts.ConnectionActive, at)
	r.Advance(time.Minute)
	if n := cycle(); n != 1 {
		t.Fatalf("the change woke %d, want the person", n)
	}
	row := readSyncRow(t, r, me)
	if row.state != "active" || row.errCode != nil || len(r.box.EventsIn(mb, "")) != 1 {
		t.Errorf("the row = %+v, events %d: want active and synced", row, len(r.box.EventsIn(mb, "")))
	}

	// Taken twice — the overlap asks for it again — it marks nothing: the
	// row was written since.
	from = len(r.box.Calls())
	r.Advance(time.Minute)
	if n := cycle(); n != 0 {
		t.Errorf("the change taken again synced %d", n)
	}
	asked := calls(r, from, "Connections")
	if len(asked) != 1 || !asked[0].From.Equal(at.Add(-30*time.Second)) {
		t.Errorf("Connections asked %+v, want from the latest change taken, less the overlap", asked)
	}
}

// A change committed while the person's own sync was running — before the
// sync wrote their row, parked — still wakes them: the guard allows the
// overlap, so neither that nor a skew between the clocks loses it.
func TestMailboxConnection_AChangeDuringThePersonsSyncStillWakesThem(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.SetConnection(mb, contracts.ConnectionNotConnected)
	if n, err := w.RunCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("cycle: synced %d, %v; want the person", n, err)
	}
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)

	// The connect committed five seconds before the parked row was written.
	r.box.SetConnection(mb, "")
	r.box.ChangeConnection(mb, contracts.ConnectionActive, r.Now().Add(-5*time.Second))
	r.Advance(time.Minute)
	if n, err := w.RunCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("cycle: synced %d, %v; want the person, woken", n, err)
	}
	if row := readSyncRow(t, r, me); row.state != "active" || len(r.box.EventsIn(mb, "")) != 1 {
		t.Errorf("the row = %+v, events %d: want active and synced", row, len(r.box.EventsIn(mb, "")))
	}
}

// A change overrides a backoff, as a notice never does.
func TestMailboxConnection_AChangeOverridesTheBackoff(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.FailWith("Apply", errors.New("graph: 503"))
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if row := readSyncRow(t, r, me); row.attempts != 1 || row.next == nil || !row.next.Equal(r.Now().Add(time.Minute)) {
		t.Fatalf("the row = %+v, want on the backoff", row)
	}
	r.box.FailWith("Apply", nil)
	r.box.ChangeConnection(mb, contracts.ConnectionActive, r.Now().Add(10*time.Second))
	r.Advance(30 * time.Second)
	n, err := w.RunCycle(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cycle: synced %d, %v; want the person, woken", n, err)
	}
	if row := readSyncRow(t, r, me); row.state != "active" || row.attempts != 0 {
		t.Errorf("the row = %+v, want active", row)
	}
}

func TestMailboxConnection_AFailingConnectionsDoesNotStopTheCycle(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	w := mailboxWorker(t, r)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.FailWith("Connections", errors.New("entra: the database is down"))
	n, err := w.RunCycle(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cycle: synced %d, %v", n, err)
	}
	if row := readSyncRow(t, r, me); row.state != "active" {
		t.Errorf("the row = %+v", row)
	}
	if !strings.Contains(r.Logs(), "the connections that moved could not be read") {
		t.Error("the failing Connections was not logged")
	}
	// Nothing was taken, so the next cycle asks from the same instant.
	first := calls(r, 0, "Connections")[0].From
	r.box.FailWith("Connections", nil)
	r.box.ChangeConnection(mb, contracts.ConnectionActive, r.Now().Add(time.Second))
	from := len(r.box.Calls())
	r.Advance(time.Minute)
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if asked := calls(r, from, "Connections"); len(asked) != 1 || !asked[0].From.Equal(first) {
		t.Errorf("Connections asked %+v, want from %v", asked, first)
	}
}

// A disconnect — the connection closing, its token kept — cleans up with
// the removals, then parks; the next re-check does not clean up again.
func TestMailboxConnection_ADisconnectCleansUpByTheRemovalsThenParks(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"default", "own"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			turnOn(t, c, target, "full", "calendar.appointment")
			mailboxCycle(t, r)
			calendarID := ""
			if target == "own" {
				for id := range r.box.Calendars(mb) {
					calendarID = id
				}
			}
			if len(r.box.EventsIn(mb, calendarID)) != 1 || !r.box.Watched(mb) {
				t.Fatal("the first sync did not write and watch")
			}
			r.box.SetConnection(mb, contracts.ConnectionClosingReason)
			r.box.SetConnectionToken(mb, true)
			r.box.ChangeConnection(mb, contracts.ConnectionClosing, r.Now().Add(time.Second))
			r.Advance(time.Minute)
			if n := mailboxCycle(t, r); n != 1 {
				t.Fatalf("the close woke %d, want the person", n)
			}
			if len(r.box.EventsIn(mb, calendarID)) != 0 || mapped(t, r, me) != 0 || r.box.Watched(mb) {
				t.Errorf("events %d, mapped %d, watched %v: the clean-up did not run",
					len(r.box.EventsIn(mb, calendarID)), mapped(t, r, me), r.box.Watched(mb))
			}
			if target == "own" && len(r.box.Calendars(mb)) != 0 {
				t.Errorf("the Vantigo calendar is left: %v", r.box.Calendars(mb))
			}
			wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)

			r.Advance(24 * time.Hour)
			from := len(r.box.Calls())
			if n := mailboxCycle(t, r); n != 1 {
				t.Fatalf("the re-check synced %d", n)
			}
			if got := removalCalls(r, from); len(got) != 0 {
				t.Errorf("the re-check cleaned up again: %v", got)
			}
			wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)
		})
	}
}

// A leaver — the connection closing with its token forgotten — has their
// copies forgotten unremoved, left in the disabled mailbox.
func TestMailboxConnection_ALeaversCopiesAreForgottenUnremoved(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, me, mb := watchedPerson(t, r)
	r.box.SetConnection(mb, contracts.ConnectionClosingReason)
	r.box.ChangeConnection(mb, contracts.ConnectionClosing, r.Now().Add(time.Second))
	r.Advance(time.Minute)
	from := len(r.box.Calls())
	mailboxCycle(t, r)
	if mapped(t, r, me) != 0 || len(r.box.EventsIn(mb, "")) != 1 {
		t.Errorf("mapped %d, events %d: want forgotten and left", mapped(t, r, me), len(r.box.EventsIn(mb, "")))
	}
	if asked := calls(r, from, "Apply"); len(asked) != 0 {
		t.Errorf("a delete was sent with no token: %+v", asked)
	}
	if asked := calls(r, from, "Unwatch"); len(asked) != 1 {
		t.Errorf("Unwatch asked %d times, want once", len(asked))
	}
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)
	if !strings.Contains(r.Logs(), "their Outlook copies are forgotten") || strings.Contains(r.Logs(), mb) {
		t.Error("the forgetting was not logged, or the mailbox was logged")
	}
}

// The long re-check runs the cycle's steps before its provider call: an
// account disabled — the sweep closed the connection, its token forgotten —
// is cleaned up as no_account.
func TestMailboxConnection_TheLongReCheckCleansUpADisabledAccount(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, me, mb := watchedPerson(t, r)
	r.box.SetConnection(mb, "mfa")
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "reconnect", 0)

	r.staff.set(me, &contracts.DirectoryAccount{Provider: "entra", ExternalID: mb, Enabled: false})
	r.box.SetConnection(mb, contracts.ConnectionClosingReason)
	r.Advance(24 * time.Hour)
	if n := mailboxCycle(t, r); n != 1 {
		t.Fatalf("the long re-check synced %d", n)
	}
	row := readSyncRow(t, r, me)
	if row.state != "unavailable" || row.errCode == nil || *row.errCode != "no_account" || mapped(t, r, me) != 0 {
		t.Errorf("the row = %+v, mapped %d: want no_account and forgotten", row, mapped(t, r, me))
	}
}

// Turned on without a connection: pending, then parked at the first cycle
// with no clean-up, then woken by the connect.
func TestMailboxConnection_TurnedOnWithoutAConnectionItWaitsForOne(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	r.box.SetConnection(mb, contracts.ConnectionNotConnected)
	if got := getSync(t, c); got.Availability != gen.MailboxSyncAvailabilityAvailable {
		t.Fatalf("availability before the first sync %s, want available", got.Availability)
	}
	if on := turnOn(t, c, "default", "full", "calendar.appointment"); on.State != gen.MailboxSyncStatePending {
		t.Fatalf("state %s, want pending", on.State)
	}
	from := len(r.box.Calls())
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "not_connected", 0)
	if got := removalCalls(r, from); len(got) != 0 {
		t.Errorf("cleaned up: %v", got)
	}
	r.box.SetConnection(mb, "")
	r.box.ChangeConnection(mb, contracts.ConnectionActive, r.Now().Add(time.Second))
	r.Advance(time.Minute)
	mailboxCycle(t, r)
	if row := readSyncRow(t, r, me); row.state != "active" || len(r.box.EventsIn(mb, "")) != 1 {
		t.Errorf("the row = %+v, events %d: want synced", row, len(r.box.EventsIn(mb, "")))
	}
}

// Turned off while waiting to reconnect: parked stopping — the removals
// need the connection too — and cleaned up once the change arrives.
func TestMailboxConnection_TurnedOffWhileWaitingToReconnectCleansUpOnTheChange(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := watchedPerson(t, r)
	r.box.SetConnection(mb, "sign_in_again")
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "unavailable", "reconnect", 0)

	putSync(t, c.Client, getSync(t, c.Client).Revision, syncBody(false, "default", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	wantParked(t, r, readSyncRow(t, r, me), "stopping", "reconnect", 0)
	if len(r.box.EventsIn(mb, "")) != 1 {
		t.Fatal("the copies went without the connection")
	}
	// Off, with the clean-up owed: the card still asks for the reconnect.
	if got := getSync(t, c.Client); got.Enabled || got.Availability != gen.MailboxSyncAvailabilityReconnect {
		t.Errorf("enabled %v, availability %s; want off and reconnect, so the clean-up can run", got.Enabled, got.Availability)
	}

	r.box.SetConnection(mb, "")
	r.box.ChangeConnection(mb, contracts.ConnectionActive, r.Now().Add(time.Second))
	r.Advance(time.Minute)
	mailboxCycle(t, r)
	if row := readSyncRow(t, r, me); row.state != "off" || len(r.box.EventsIn(mb, "")) != 0 || mapped(t, r, me) != 0 || r.box.Watched(mb) {
		t.Errorf("the row = %+v, events %d, mapped %d, watched %v: want off and cleaned up",
			row, len(r.box.EventsIn(mb, "")), mapped(t, r, me), r.box.Watched(mb))
	}
}

// The switch off closes every connection, keeping its token: everyone is
// cleaned up on their own token. Back on, everyone parks not_connected.
func TestMailboxConnection_TheSwitchOffCleansUpEveryone(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, first, mbFirst := watchedPerson(t, r)
	_, second, mbSecond := watchedPerson(t, r)
	r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
	for _, mb := range []string{mbFirst, mbSecond} {
		r.box.SetConnection(mb, contracts.ConnectionClosingReason)
		r.box.SetConnectionToken(mb, true)
		r.box.ChangeConnection(mb, contracts.ConnectionClosing, r.Now().Add(time.Second))
	}
	r.Advance(time.Minute)
	if n := mailboxCycle(t, r); n != 2 {
		t.Fatalf("synced %d, want both", n)
	}
	for name, mb := range map[string]string{"first": mbFirst, "second": mbSecond} {
		if len(r.box.EventsIn(mb, "")) != 0 || r.box.Watched(mb) {
			t.Errorf("%s: events %d, watched %v", name, len(r.box.EventsIn(mb, "")), r.box.Watched(mb))
		}
	}
	if row := readSyncRow(t, r, first); row.state != "unavailable" || mapped(t, r, first) != 0 || mapped(t, r, second) != 0 {
		t.Errorf("the row = %+v", row)
	}

	r.box.SetStatus(contracts.MailboxAvailable, "")
	r.Advance(time.Hour)
	if n := mailboxCycle(t, r); n != 2 {
		t.Fatalf("synced %d after the switch on, want both", n)
	}
	wantParked(t, r, readSyncRow(t, r, first), "unavailable", "not_connected", 0)
	wantParked(t, r, readSyncRow(t, r, second), "unavailable", "not_connected", 0)
}

// A parked person may change their setting, and Sync now asks at once.
func TestMailboxConnection_PutAndSyncNowAreTakenWhileParked(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{contracts.ConnectionNotConnected, "mfa"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			turnOn(t, c, "default", "full", "calendar.appointment")
			r.box.SetConnection(mb, reason)
			mailboxCycle(t, r)
			if row := readSyncRow(t, r, me); row.state != "unavailable" {
				t.Fatalf("the row = %+v, want parked", row)
			}
			got := putSync(t, c, getSync(t, c).Revision, syncBody(true, "default", "busy", "calendar.appointment"))
			if got.Detail != gen.MailboxSyncDetailBusy {
				t.Errorf("the PUT answered %+v", got)
			}
			mailboxCycle(t, r)
			r.Advance(time.Minute)
			var out gen.MailboxSync
			do(t, c, http.MethodPost, mailboxSyncRunPath, nil, http.StatusOK, &out)
			if row := readSyncRow(t, r, me); row.next == nil || !row.next.Equal(r.Now()) {
				t.Errorf("Sync now left next %v, want now", row.next)
			}
		})
	}
}

// The calls are those of the removals the clean-up makes, the provider's
// rule for each judged by the fake; the worker never sends a create on a
// closing connection.
func TestMailboxConnection_AClosingConnectionIsSentNothingButRemovals(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, _, mb := watchedPerson(t, r)
	r.box.SetConnection(mb, contracts.ConnectionClosingReason)
	r.box.SetConnectionToken(mb, true)
	r.Advance(16 * time.Minute)
	from := len(r.box.Calls())
	mailboxCycle(t, r)
	for _, c := range r.box.Calls()[from:] {
		if c.Method == "Apply" && slices.ContainsFunc(c.Ops, func(op contracts.MailboxOp) bool { return op.Kind != contracts.MailboxOpDelete }) {
			t.Errorf("an Apply carried more than deletes: %+v", c.Ops)
		}
		if c.Method == "Watch" || c.Method == "EnsureCalendar" {
			t.Errorf("%s was asked of a closing connection", c.Method)
		}
	}
}
