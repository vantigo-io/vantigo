package calendar_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The calendar-mailbox-sync worker (calendar Outlook sync design O6–O8),
// against modtest's fake mailbox provider. The harness clock starts on
// Saturday 12 September 2026 at 14:00 in Oslo, the business zone.

const oslo = "Europe/Oslo"

// mailboxWorker is the worker as worker mode builds it (module.Workers):
// the single-provider slots — the user directory among them — resolved.
func mailboxWorker(t *testing.T, r *mailboxRig) *calendar.MailboxSyncWorker {
	t.Helper()
	for _, w := range r.Workers(r.Deps()) {
		if mw, ok := w.(*calendar.MailboxSyncWorker); ok {
			return mw
		}
	}
	t.Fatal("no calendar-mailbox-sync worker")
	return nil
}

// mailboxCycle runs one cycle, failing unless it ran, and answers how many
// people it synced.
func mailboxCycle(t *testing.T, r *mailboxRig) int {
	t.Helper()
	ran, n, err := mailboxWorker(t, r).RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("cycle: ran %v, %v", ran, err)
	}
	return n
}

// calls is the fake's calls of method since the first n calls.
func calls(r *mailboxRig, from int, method string) []modtest.MailboxCall {
	var out []modtest.MailboxCall
	for _, c := range r.box.Calls()[from:] {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// opsOf is every op the calls carried.
func opsOf(list []modtest.MailboxCall) []contracts.MailboxOp {
	var out []contracts.MailboxOp
	for _, c := range list {
		out = append(out, c.Ops...)
	}
	return out
}

// byKey is the mailbox's events in a calendar by the key they carry.
func byKey(r *mailboxRig, mailbox, calendarID string) map[string]modtest.FakeMailboxEvent {
	out := map[string]modtest.FakeMailboxEvent{}
	for _, e := range r.box.Events(mailbox, calendarID) {
		out[e.Key] = e
	}
	return out
}

// syncRow is the person's row as the worker left it.
type syncRow struct {
	enabled   bool
	state     string
	errCode   *string
	next      *time.Time
	attempts  int
	reconcile bool
}

func readSyncRow(t *testing.T, r *mailboxRig, id uuid.UUID) syncRow {
	t.Helper()
	var s syncRow
	ctx := context.Background()
	if err := r.Pool().QueryRow(ctx, `SELECT enabled, state, error, next_sync_at, attempts, reconcile FROM calendar.mailbox_sync WHERE user_id = $1`, id).
		Scan(&s.enabled, &s.state, &s.errCode, &s.next, &s.attempts, &s.reconcile); err != nil {
		t.Fatal(err)
	}
	return s
}

func mapped(t *testing.T, r *mailboxRig, id uuid.UUID) int {
	t.Helper()
	return r.Count(t, `SELECT count(*) FROM calendar.mailbox_events WHERE user_id = $1`, id)
}

// key is an appointment's item key, with the occurrence's date for a series.
func key(id int64, date ...string) string {
	k := "appointment:" + strconv.FormatInt(id, 10)
	for _, d := range date {
		k += ":" + d
	}
	return k
}

func TestMailboxSyncWorker_TheFirstSyncWritesEachCopyInItsShape(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "staff:access", "customers:view")
	colleague, _ := signIn(t, r.harness)
	turnOn(t, c, "default", "full", "calendar.appointment", "staff.absence")

	single := create(t, c, with(timed(t, "Standup", oslo, "2026-09-14T09:00", "2026-09-14T09:30"),
		"location", "Room 4", "description", "Agenda"))
	series := create(t, c, with(timed(t, "Weekly", oslo, "2026-09-15T10:00", "2026-09-15T11:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	private := create(t, c, with(timed(t, "Doctor", oslo, "2026-09-16T08:00", "2026-09-16T09:00"), "visibility", "private", "transparency", "free"))
	holiday := create(t, c, allDay("Holiday", oslo, "2026-09-21", "2026-09-22"))
	// The colleague's appointments the person is on: one declined, one
	// answered tentative, one private.
	declined := create(t, colleague, with(timed(t, "Declined", oslo, "2026-09-17T12:00", "2026-09-17T13:00"), "attendees", userAttendees(me.String())))
	tentative := create(t, colleague, with(timed(t, "Maybe", oslo, "2026-09-17T14:00", "2026-09-17T15:00"), "attendees", userAttendees(me.String())))
	theirs := create(t, colleague, with(timed(t, "Their secret", oslo, "2026-09-18T14:00", "2026-09-18T15:00"),
		"visibility", "private", "attendees", userAttendees(me.String())))
	do(t, c, http.MethodPost, responsePath(declined.Id), map[string]any{"status": "declined"}, http.StatusOK, nil)
	do(t, c, http.MethodPost, responsePath(tentative.Id), map[string]any{"status": "tentative"}, http.StatusOK, nil)

	absenceDay := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	r.sources.staff.give(me, contracts.CalendarItem{Kind: "staff.absence", ID: "7", Title: "Vacation", AllDay: true, StartDate: absenceDay, EndDate: absenceDay.AddDate(0, 0, 2)})
	followUp := at(t, oslo, "2026-09-14T15:00")
	r.sources.customers.give(me, contracts.CalendarItem{Kind: "customers.follow-up", ID: "3", Title: "Call Acme", StartsAt: followUp, EndsAt: followUp})

	if n := mailboxCycle(t, r); n != 1 {
		t.Fatalf("synced %d", n)
	}
	got := byKey(r, mb, "")
	want := []string{
		key(single.Id), key(series.Id, "2026-09-15"), key(series.Id, "2026-09-22"), key(series.Id, "2026-09-29"),
		key(private.Id), key(holiday.Id), key(tentative.Id), key(theirs.Id), "staff.absence:7",
	}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("copies = %v, want %v", keys, want)
	}

	s := got[key(single.Id)].Event
	if s.Subject != "Standup" || s.Location != "Room 4" || s.ShowAs != contracts.ShowAsBusy || s.Private || s.AllDay ||
		!s.StartsAt.Equal(at(t, oslo, "2026-09-14T09:00")) || !s.EndsAt.Equal(at(t, oslo, "2026-09-14T09:30")) || s.TimeZone != oslo {
		t.Errorf("the single one = %+v", s)
	}
	link := fmt.Sprintf("/calendar?date=2026-09-14&appointment=%d", single.Id)
	if !strings.HasPrefix(s.Body, "Agenda\n\nKept by Vantigo — change it there:\n") || !strings.HasSuffix(s.Body, link) {
		t.Errorf("the single one's body = %q", s.Body)
	}
	if o := got[key(series.Id, "2026-09-22")].Event; !o.StartsAt.Equal(at(t, oslo, "2026-09-22T10:00")) || !strings.Contains(o.Body, "date=2026-09-22&") {
		t.Errorf("the second occurrence = %+v", o)
	}
	if p := got[key(private.Id)].Event; p.Subject != "Doctor" || !p.Private || p.ShowAs != contracts.ShowAsFree {
		t.Errorf("the owner's private one = %+v", p)
	}
	if p := got[key(theirs.Id)].Event; p.Subject != "Busy" || !p.Private || p.Location != "" || strings.Contains(p.Body, "Kept by") {
		t.Errorf("a colleague's private one = %+v", p)
	}
	if p := got[key(tentative.Id)].Event; p.ShowAs != contracts.ShowAsTentative {
		t.Errorf("the tentative one shows %s", p.ShowAs)
	}
	h := got[key(holiday.Id)].Event
	if !h.AllDay || !h.StartDate.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)) || !h.EndDate.Equal(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the all-day one = %+v", h)
	}
	if a := got["staff.absence:7"].Event; a.ShowAs != contracts.ShowAsOOF || !a.AllDay || a.Subject != "Vacation" {
		t.Errorf("the absence = %+v", a)
	}
	row := readSyncRow(t, r, me)
	if row.state != "active" || row.errCode != nil || row.attempts != 0 || row.reconcile ||
		row.next == nil || !row.next.Equal(r.Now().Add(15*time.Minute)) {
		t.Errorf("the row = %+v", row)
	}
	if n := mapped(t, r, me); n != len(want) {
		t.Errorf("mapped %d, want %d", n, len(want))
	}
	if len(calls(r, 0, "Find")) != 1 {
		t.Error("the first sync did not reconcile")
	}

	// A second sync with nothing changed writes nothing.
	before := len(r.box.Calls())
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if ops := calls(r, before, "Apply"); len(ops) != 0 {
		t.Errorf("a sync with nothing changed applied %v", opsOf(ops))
	}
	if len(calls(r, before, "Find")) != 0 {
		t.Error("a second sync the same day reconciled")
	}

	// A follow-up is written once its kind is chosen, as a point of 30
	// minutes, busy.
	cur := getSync(t, c)
	putSync(t, c, cur.Revision, syncBody(true, "default", "full", "calendar.appointment", "staff.absence", "customers.follow-up"))
	mailboxCycle(t, r)
	if f := byKey(r, mb, "")["customers.follow-up:3"].Event; f.ShowAs != contracts.ShowAsBusy || !f.EndsAt.Equal(followUp.Add(30*time.Minute)) {
		t.Errorf("the follow-up = %+v", f)
	}
}

func TestMailboxSyncWorker_AnAppointmentWriteNudgesItsPeopleAndIsFollowed(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	colleague, colleagueID, colleagueMB := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	turnOn(t, colleague, "default", "full", "calendar.appointment")
	a := create(t, c, with(timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"), "attendees", userAttendees(colleagueID.String())))
	if n := mailboxCycle(t, r); n != 2 {
		t.Fatalf("synced %d", n)
	}
	if len(r.box.Events(mb, "")) != 1 || len(r.box.Events(colleagueMB, "")) != 1 {
		t.Fatal("the appointment was not written to both")
	}

	// An edit makes both due at once — no clock moved — and is an update.
	before := len(r.box.Calls())
	full := with(timed(t, "Plan B", oslo, "2026-09-14T09:00", "2026-09-14T10:00"), "attendees", userAttendees(colleagueID.String()))
	var got struct{ Revision int32 }
	do(t, c, http.MethodPut, appointmentPath(a.Id), full, http.StatusOK, &got, ifMatch(a.Revision))
	for _, id := range []uuid.UUID{me, colleagueID} {
		if next := readSyncRow(t, r, id).next; next == nil || next.After(r.Now()) {
			t.Errorf("%s not due after the edit: %v", id, next)
		}
	}
	if n := mailboxCycle(t, r); n != 2 {
		t.Fatalf("after the edit synced %d", n)
	}
	ops := opsOf(calls(r, before, "Apply"))
	if len(ops) != 2 || ops[0].Kind != contracts.MailboxOpUpdate || ops[1].Kind != contracts.MailboxOpUpdate {
		t.Errorf("the edit applied %+v", ops)
	}
	if e := r.box.Events(colleagueMB, ""); len(e) != 1 || e[0].Event.Subject != "Plan B" {
		t.Errorf("the colleague's copy = %+v", e)
	}

	// The colleague declines: their copy goes.
	before = len(r.box.Calls())
	do(t, colleague, http.MethodPost, responsePath(a.Id), map[string]any{"status": "declined"}, http.StatusOK, nil)
	mailboxCycle(t, r)
	if e := r.box.Events(colleagueMB, ""); len(e) != 0 {
		t.Errorf("a declined one is still there: %+v", e)
	}
	if n := mapped(t, r, colleagueID); n != 0 {
		t.Errorf("the colleague's map holds %d", n)
	}

	// The owner deletes it: the copy goes.
	do(t, c, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	mailboxCycle(t, r)
	if e := r.box.Events(mb, ""); len(e) != 0 {
		t.Errorf("a deleted appointment's copy is still there: %+v", e)
	}
	if ops := opsOf(calls(r, before, "Apply")); len(ops) != 2 || ops[0].Kind != contracts.MailboxOpDelete || ops[1].Kind != contracts.MailboxOpDelete {
		t.Errorf("applied %+v", ops)
	}
}

func TestMailboxSyncWorker_ACopyThatSlidesBeforeTheWindowIsForgottenNotDeleted(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "staff:access")
	turnOn(t, c, "default", "full", "staff.absence")
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	r.sources.staff.give(me, contracts.CalendarItem{Kind: "staff.absence", ID: "1", Title: "Off", AllDay: true, StartDate: day, EndDate: day})
	mailboxCycle(t, r)
	// Forty days on, the provider no longer answers it (it is outside the
	// window it is asked about): its copy stays, its map row goes.
	r.sources.staff.give(me)
	r.Advance(40 * 24 * time.Hour)
	before := len(r.box.Calls())
	mailboxCycle(t, r)
	if ops := opsOf(calls(r, before, "Apply")); len(ops) != 0 {
		t.Errorf("applied %+v", ops)
	}
	if len(r.box.Events(mb, "")) != 1 || mapped(t, r, me) != 0 {
		t.Errorf("events %d, mapped %d: want the copy kept and forgotten", len(r.box.Events(mb, "")), mapped(t, r, me))
	}
	// The next day's reconcile leaves the forgotten copy alone too.
	r.Advance(24 * time.Hour)
	mailboxCycle(t, r)
	if len(r.box.Events(mb, "")) != 1 {
		t.Error("the next reconcile deleted the past")
	}
}

func TestMailboxSyncWorker_TheReconcileAdoptsAndDropsDuplicates(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, mb := r.person(t)
	a := create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	// Written before: the copy (whose create's answer was lost) and a
	// duplicate of it, an event of the person's own and a copy of something
	// no longer wanted.
	starts := at(t, oslo, "2026-09-14T09:00")
	old := contracts.MailboxEvent{Subject: "old", StartsAt: starts, EndsAt: starts.Add(time.Hour)}
	adopted := r.box.PutEvent(mb, "", key(a.Id), old)
	duplicate := r.box.PutEvent(mb, "", key(a.Id), old)
	own := r.box.PutEvent(mb, "", "", contracts.MailboxEvent{Subject: "Mine", StartsAt: starts, EndsAt: starts.Add(time.Hour)})
	gone := r.box.PutEvent(mb, "", "appointment:999999", contracts.MailboxEvent{Subject: "Past", StartsAt: starts, EndsAt: starts.Add(time.Hour)})
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	events := map[string]string{}
	for _, e := range r.box.Events(mb, "") {
		events[e.ID] = e.Event.Subject
	}
	if events[adopted] != "Plan" {
		t.Errorf("the adopted copy = %q, want it rewritten", events[adopted])
	}
	if _, ok := events[duplicate]; ok {
		t.Error("the duplicate was kept")
	}
	if events[own] != "Mine" || events[gone] != "Past" {
		t.Errorf("events not ours to judge were touched: %v", events)
	}
	if ops := opsOf(calls(r, 0, "Apply")); slices.ContainsFunc(ops, func(o contracts.MailboxOp) bool { return o.Kind == contracts.MailboxOpCreate }) {
		t.Errorf("the reconcile created %+v", ops)
	}
}

func TestMailboxSyncWorker_TargetOwnWritesAVantigoCalendarAndMovingCleansItUp(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	on := turnOn(t, c, "own", "full", "calendar.appointment")
	mailboxCycle(t, r)
	cals := r.box.Calendars(mb)
	if len(cals) != 1 {
		t.Fatalf("calendars = %v", cals)
	}
	var calID string
	for id, name := range cals {
		calID = id
		if name != "Vantigo" {
			t.Errorf("the calendar is named %q", name)
		}
	}
	if len(r.box.Events(mb, calID)) != 1 || len(r.box.Events(mb, "")) != 0 {
		t.Fatal("the copy is not in the Vantigo calendar alone")
	}
	// A second sync keeps the calendar.
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if len(r.box.Calendars(mb)) != 1 {
		t.Error("a second sync made another calendar")
	}

	putSync(t, c, on.Revision, syncBody(true, "default", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	if len(r.box.Calendars(mb)) != 0 {
		t.Errorf("the Vantigo calendar is still there: %v", r.box.Calendars(mb))
	}
	if len(r.box.Events(mb, "")) != 1 || readSyncRow(t, r, me).state != "active" {
		t.Errorf("after the move: events %d, state %s", len(r.box.Events(mb, "")), readSyncRow(t, r, me).state)
	}
	if id := modtest.One[*string](t, r.Harness, `SELECT calendar_id FROM calendar.mailbox_sync WHERE user_id = $1`, me); id != nil {
		t.Errorf("calendar_id = %q after the move", *id)
	}
}

func TestMailboxSyncWorker_TurnedOffItDeletesEveryCopy(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	create(t, c, timed(t, "Review", oslo, "2026-09-15T09:00", "2026-09-15T10:00"))
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	putSync(t, c, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	if len(r.box.Events(mb, "")) != 0 || mapped(t, r, me) != 0 {
		t.Errorf("events %d, mapped %d after turning off", len(r.box.Events(mb, "")), mapped(t, r, me))
	}
	if row := readSyncRow(t, r, me); row.state != "off" || row.enabled || row.next != nil {
		t.Errorf("the row = %+v", row)
	}
	// An off row is never due again.
	before := len(r.box.Calls())
	r.Advance(time.Hour)
	if n := mailboxCycle(t, r); n != 0 || len(r.box.Calls()) != before {
		t.Errorf("an off row synced %d", n)
	}
}

func TestMailboxSyncWorker_APersonWhoLosesCalendarOrTheirAccountIsCleanedUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		lose func(r *mailboxRig, id uuid.UUID)
		code string
	}{
		{name: "disabled", lose: func(r *mailboxRig, id uuid.UUID) {
			r.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)
		}, code: "unavailable"},
		{name: "no account", lose: func(r *mailboxRig, id uuid.UUID) { r.staff.set(id, nil) }, code: "no_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			turnOn(t, c, "default", "full", "calendar.appointment")
			mailboxCycle(t, r)
			tc.lose(r, me)
			r.Advance(15 * time.Minute)
			mailboxCycle(t, r)
			if len(r.box.Events(mb, "")) != 0 || mapped(t, r, me) != 0 {
				t.Errorf("events %d, mapped %d", len(r.box.Events(mb, "")), mapped(t, r, me))
			}
			row := readSyncRow(t, r, me)
			if row.state != "unavailable" || row.errCode == nil || *row.errCode != tc.code || !row.enabled ||
				row.next == nil || !row.next.Equal(r.Now().Add(time.Hour)) {
				t.Errorf("the row = %+v, want unavailable %s in an hour", row, tc.code)
			}
		})
	}
}

func TestMailboxSyncWorker_AnotherMailboxCleansUpTheOldOneFirst(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	r.staff.set(me, &contracts.DirectoryAccount{Provider: "entra", ExternalID: "oid-new", Enabled: true})
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if len(r.box.Events(mb, "")) != 0 || len(r.box.Events("oid-new", "")) != 1 {
		t.Errorf("old %d, new %d", len(r.box.Events(mb, "")), len(r.box.Events("oid-new", "")))
	}
	if got := modtest.One[string](t, r.Harness, `SELECT mailbox FROM calendar.mailbox_sync WHERE user_id = $1`, me); got != "oid-new" {
		t.Errorf("the stored mailbox = %q", got)
	}
}

func TestMailboxSyncWorker_AnUnavailableProviderIsAskedNothing(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, _ := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.SetStatus(contracts.MailboxUnavailable, "not_connected")
	mailboxCycle(t, r)
	for _, call := range r.box.Calls() {
		if call.Method != "Status" {
			t.Errorf("an unavailable provider was called: %+v", call)
		}
	}
	if row := readSyncRow(t, r, me); row.state != "unavailable" || row.next == nil || !row.next.Equal(r.Now().Add(time.Hour)) {
		t.Errorf("the row = %+v", row)
	}
}

func TestMailboxSyncWorker_TheSwitchTurnedOffOnlyDeletes(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
	create(t, c, timed(t, "Later", oslo, "2026-09-15T09:00", "2026-09-15T10:00"))
	before := len(r.box.Calls())
	mailboxCycle(t, r)
	for _, op := range opsOf(calls(r, before, "Apply")) {
		if op.Kind != contracts.MailboxOpDelete {
			t.Errorf("under cleanup the worker applied %+v", op)
		}
	}
	if len(calls(r, before, "EnsureCalendar")) != 0 {
		t.Error("under cleanup the worker asked for a calendar")
	}
	// The clean-up lists every copy, whatever its date: no bound.
	for _, f := range calls(r, before, "Find") {
		if !f.From.IsZero() {
			t.Errorf("the clean-up's Find was bounded from %v", f.From)
		}
	}
	if len(r.box.Events(mb, "")) != 0 {
		t.Error("the copies were not removed")
	}
	if row := readSyncRow(t, r, me); row.state != "unavailable" || !row.enabled {
		t.Errorf("the row = %+v", row)
	}
	// Turned back on, the sync resumes from a reconcile.
	r.box.SetStatus(contracts.MailboxAvailable, "")
	r.Advance(time.Hour)
	before = len(r.box.Calls())
	mailboxCycle(t, r)
	if len(calls(r, before, "Find")) != 1 || len(r.box.Events(mb, "")) != 2 {
		t.Errorf("resumed: finds %d, events %d", len(calls(r, before, "Find")), len(r.box.Events(mb, "")))
	}
}

func TestMailboxSyncWorker_ADeniedMailboxStopsThePerson(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.SetMailboxError(mb, contracts.ErrMailboxDenied)
	mailboxCycle(t, r)
	row := readSyncRow(t, r, me)
	if row.state != "unavailable" || row.errCode == nil || *row.errCode != "mailbox_denied" ||
		row.next == nil || !row.next.Equal(r.Now().Add(6*time.Hour)) {
		t.Errorf("the row = %+v", row)
	}
	var got struct {
		Error string `json:"error"`
	}
	do(t, c, http.MethodGet, mailboxSyncPath, nil, http.StatusOK, &got)
	if got.Error != "mailbox_denied" {
		t.Errorf("GET error = %q", got.Error)
	}
}

func TestMailboxSyncWorker_AFailureBacksOffAndIsFailedFromTheFifth(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, _ := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	r.box.FailWith("Apply", errors.New("graph: 503"))
	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, time.Hour} {
		mailboxCycle(t, r)
		row := readSyncRow(t, r, me)
		wantState := "pending"
		if i == 4 {
			wantState = "failed"
		}
		if row.attempts != i+1 || row.state != wantState || row.errCode == nil || *row.errCode != "failed" ||
			row.next == nil || !row.next.Equal(r.Now().Add(wait)) {
			t.Fatalf("failure %d: the row = %+v, want %s in %v", i+1, row, wantState, wait)
		}
		r.Advance(wait)
	}
	r.box.FailWith("Apply", nil)
	mailboxCycle(t, r)
	if row := readSyncRow(t, r, me); row.state != "active" || row.attempts != 0 || row.errCode != nil {
		t.Errorf("after recovering: %+v", row)
	}
}

func TestMailboxSyncWorker_KeepsAtMost1500CopiesAndEveryAppointment(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "time:access")
	far := create(t, c, timed(t, "Far", oslo, "2027-03-01T09:00", "2027-03-01T10:00"))
	var entries []contracts.CalendarItem
	start := at(t, oslo, "2026-09-14T08:00")
	for i := range 1600 {
		s := start.Add(time.Duration(i) * 2 * time.Hour)
		entries = append(entries, contracts.CalendarItem{Kind: "time.entry", ID: strconv.Itoa(i), Title: "Work", StartsAt: s, EndsAt: s.Add(time.Hour)})
	}
	r.sources.time.give(me, entries...)
	turnOn(t, c, "default", "full", "calendar.appointment", "time.entry")
	mailboxCycle(t, r)
	got := byKey(r, mb, "")
	if len(got) != 1500 {
		t.Errorf("copies = %d, want 1500", len(got))
	}
	if _, ok := got[key(far.Id)]; !ok {
		t.Error("the appointment was left out")
	}
	if _, ok := got["time.entry:0"]; !ok {
		t.Error("the nearest entry was left out")
	}
	if _, ok := got["time.entry:1599"]; ok {
		t.Error("the farthest entry was kept")
	}
	if ops := calls(r, 0, "Apply"); len(ops) != 15 {
		t.Errorf("applied in %d slices, want 15 of 100", len(ops))
	}
	if !strings.Contains(r.Logs(), "farthest calendar source items were left out") {
		t.Error("the cut was not logged")
	}
}

func TestMailboxSyncWorker_BusyDetailTitlesInThePersonsLanguage(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "staff:access")
	r.Exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, me)
	a := create(t, c, with(timed(t, "Lønnssamtale", oslo, "2026-09-14T09:00", "2026-09-14T10:00"), "location", "Rom 2", "description", "Hemmelig"))
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	r.sources.staff.give(me, contracts.CalendarItem{Kind: "staff.absence", ID: "7", Title: "Ferie", AllDay: true, StartDate: day, EndDate: day})
	turnOn(t, c, "default", "busy", "calendar.appointment", "staff.absence")
	mailboxCycle(t, r)
	got := byKey(r, mb, "")
	ev := got[key(a.Id)].Event
	if ev.Subject != "Opptatt" || ev.Location != "" || strings.Contains(ev.Body, "Hemmelig") || !strings.HasPrefix(ev.Body, "http") {
		t.Errorf("a busy copy = %+v", ev)
	}
	if abs := got["staff.absence:7"].Event; abs.Subject != "Opptatt" || abs.ShowAs != contracts.ShowAsOOF {
		t.Errorf("a busy absence = %+v", abs)
	}
	// Full detail in Norwegian keeps the title and says where to change it.
	cur := getSync(t, c)
	putSync(t, c, cur.Revision, syncBody(true, "default", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	if ev := byKey(r, mb, "")[key(a.Id)].Event; ev.Subject != "Lønnssamtale" || !strings.Contains(ev.Body, "Holdes av Vantigo — endre det der:") {
		t.Errorf("a full copy = %+v", ev)
	}
}

// A calendar the provider did not make is never deleted (design O8): the
// clean-up deletes this installation's copies in it, leaves the person's own
// events, and forgets the calendar. And a Vantigo calendar of the person's
// own is passed over for the next name.
func TestMailboxSyncWorker_ACalendarNotOursIsNeverDeleted(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	theirs := r.box.PutCalendar(mb, "Vantigo", false)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	on := turnOn(t, c, "own", "full", "calendar.appointment")
	mailboxCycle(t, r)
	cals := r.box.Calendars(mb)
	if len(cals) != 2 || len(r.box.Events(mb, theirs)) != 0 {
		t.Fatalf("calendars = %v; the copy went to the person's own calendar", cals)
	}

	// The row comes to name the person's own calendar, holding a copy of
	// ours and one of theirs.
	r.Exec(t, `UPDATE calendar.mailbox_sync SET calendar_id = $2 WHERE user_id = $1`, me, theirs)
	starts := at(t, oslo, "2026-09-20T09:00")
	r.box.PutEvent(mb, theirs, "appointment:424242", contracts.MailboxEvent{Subject: "A copy", StartsAt: starts, EndsAt: starts.Add(time.Hour)})
	own := r.box.PutEvent(mb, theirs, "", contracts.MailboxEvent{Subject: "Mine", StartsAt: starts, EndsAt: starts.Add(time.Hour)})
	putSync(t, c, on.Revision, syncBody(false, "own", "full", "calendar.appointment"))
	mailboxCycle(t, r)
	if _, ok := r.box.Calendars(mb)[theirs]; !ok {
		t.Fatal("the person's calendar was deleted")
	}
	if e := r.box.Events(mb, theirs); len(e) != 1 || e[0].ID != own {
		t.Errorf("the person's calendar holds %+v, want their own event alone", e)
	}
	if row := readSyncRow(t, r, me); row.state != "off" {
		t.Errorf("the row = %+v, want off", row)
	}
	if id := modtest.One[*string](t, r.Harness, `SELECT calendar_id FROM calendar.mailbox_sync WHERE user_id = $1`, me); id != nil {
		t.Errorf("calendar_id = %q, want forgotten", *id)
	}
}

// A clean-up the mailbox refuses for now — denied, the provider unusable —
// keeps the map and the clean-up owed, backing off, until it can be done; a
// mailbox gone is forgotten (design O8).
func TestMailboxSyncWorker_ACleanUpRefusedForNowStaysOwed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"denied", contracts.ErrMailboxDenied, "mailbox_denied"},
		{"the provider unusable", contracts.ErrMailboxUnavailable, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newMailboxRig(t)
			c, me, mb := r.person(t)
			create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
			on := turnOn(t, c, "default", "full", "calendar.appointment")
			mailboxCycle(t, r)
			r.box.SetMailboxError(mb, tc.err)
			putSync(t, c, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
			mailboxCycle(t, r)
			row := readSyncRow(t, r, me)
			if row.state != "stopping" || row.errCode == nil || *row.errCode != tc.code || row.attempts != 1 ||
				row.next == nil || !row.next.Equal(r.Now().Add(time.Minute)) {
				t.Errorf("the row = %+v, want stopping, %s, in a minute", row, tc.code)
			}
			if mapped(t, r, me) != 1 || len(r.box.Events(mb, "")) != 1 {
				t.Errorf("mapped %d, events %d: want the map and the copy kept", mapped(t, r, me), len(r.box.Events(mb, "")))
			}
			r.box.SetMailboxError(mb, nil)
			r.Advance(time.Minute)
			mailboxCycle(t, r)
			if len(r.box.Events(mb, "")) != 0 || mapped(t, r, me) != 0 || readSyncRow(t, r, me).state != "off" {
				t.Errorf("after the retry: events %d, mapped %d, state %s", len(r.box.Events(mb, "")), mapped(t, r, me), readSyncRow(t, r, me).state)
			}
		})
	}
	t.Run("gone", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, me, mb := r.person(t)
		create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
		on := turnOn(t, c, "default", "full", "calendar.appointment")
		mailboxCycle(t, r)
		r.box.SetMailboxError(mb, contracts.ErrMailboxGone)
		putSync(t, c, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
		mailboxCycle(t, r)
		if row := readSyncRow(t, r, me); row.state != "off" || mapped(t, r, me) != 0 {
			t.Errorf("the row = %+v, mapped %d: want off and forgotten", row, mapped(t, r, me))
		}
		if strings.Contains(r.Logs(), mb) {
			t.Error("the mailbox was logged")
		}
	})
}

// The clean-up deletes every copy this installation wrote, those slid before
// the window too (design O8); the reconcile asks only from the window's
// first instant.
func TestMailboxSyncWorker_TheCleanUpDeletesCopiesBeforeTheWindow(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "staff:access")
	turnOn(t, c, "default", "full", "staff.absence")
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	r.sources.staff.give(me, contracts.CalendarItem{Kind: "staff.absence", ID: "1", Title: "Off", AllDay: true, StartDate: day, EndDate: day})
	mailboxCycle(t, r)
	if f := calls(r, 0, "Find"); len(f) != 1 || !f[0].From.Equal(at(t, oslo, "2026-08-13T00:00")) {
		t.Errorf("the reconcile's Find = %+v, want from the window's first instant", f)
	}
	r.sources.staff.give(me)
	r.Advance(40 * 24 * time.Hour)
	mailboxCycle(t, r)
	if len(r.box.Events(mb, "")) != 1 || mapped(t, r, me) != 0 {
		t.Fatalf("events %d, mapped %d: want the past copy kept and forgotten", len(r.box.Events(mb, "")), mapped(t, r, me))
	}
	// Turned off (as the PUT does; the session has long expired).
	r.Exec(t, `UPDATE calendar.mailbox_sync SET enabled = false, state = 'stopping', next_sync_at = NULL WHERE user_id = $1`, me)
	mailboxCycle(t, r)
	if n := len(r.box.Events(mb, "")); n != 0 {
		t.Errorf("%d copies left after turning off, want none", n)
	}
}

// A disabled directory account — a leaver's — is no account: the copies are
// cleaned up, the row unavailable no_account (design O3).
func TestMailboxSyncWorker_ADisabledAccountIsNoAccount(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	r.staff.set(me, &contracts.DirectoryAccount{Provider: "entra", ExternalID: mb, Enabled: false})
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if len(r.box.Events(mb, "")) != 0 || mapped(t, r, me) != 0 {
		t.Errorf("events %d, mapped %d", len(r.box.Events(mb, "")), mapped(t, r, me))
	}
	if row := readSyncRow(t, r, me); row.state != "unavailable" || row.errCode == nil || *row.errCode != "no_account" {
		t.Errorf("the row = %+v, want unavailable no_account", row)
	}
}

// The first op that fails stops the person's sync after its slice: no
// further slice is sent, the done ones are mapped, and the rest follow on
// the retry. The log names the code, never the error's words.
func TestMailboxSyncWorker_AFailedOpStopsTheSlices(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "time:access")
	var entries []contracts.CalendarItem
	start := at(t, oslo, "2026-09-14T08:00")
	for i := range 250 {
		s := start.Add(time.Duration(i) * 2 * time.Hour)
		entries = append(entries, contracts.CalendarItem{Kind: "time.entry", ID: strconv.Itoa(i), Title: "Work", StartsAt: s, EndsAt: s.Add(time.Hour)})
	}
	r.sources.time.give(me, entries...)
	turnOn(t, c, "default", "full", "time.entry")
	r.box.FailOps(1, errors.New("graph: /users/"+mb+"/events throttled"))
	mailboxCycle(t, r)
	if n := len(calls(r, 0, "Apply")); n != 1 {
		t.Errorf("%d slices sent, want the first alone", n)
	}
	if n := mapped(t, r, me); n != 99 {
		t.Errorf("mapped %d, want the 99 done", n)
	}
	row := readSyncRow(t, r, me)
	if row.attempts != 1 || row.errCode == nil || *row.errCode != "failed" || !row.reconcile {
		t.Errorf("the row = %+v, want a failure that reconciles next", row)
	}
	if strings.Contains(r.Logs(), mb) {
		t.Error("the error's words, with the mailbox's path, were logged")
	}
	r.Advance(time.Minute)
	mailboxCycle(t, r)
	if n := len(r.box.Events(mb, "")); n != 250 {
		t.Errorf("%d copies after the retry, want 250", n)
	}
}

// The cap is hard: the appointments' occurrences alone over 1500 lose the
// farthest from today, and no projected item is written.
func TestMailboxSyncWorker_TheCapTrimsAppointmentsToo(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t, "time:access")
	var series []gen.Appointment
	for h := range 8 {
		start := fmt.Sprintf("2026-08-13T%02d:00", 8+h)
		end := fmt.Sprintf("2026-08-13T%02d:30", 8+h)
		series = append(series, create(t, c, with(timed(t, "Daily", oslo, start, end), "recurrence", map[string]any{"rule": "FREQ=DAILY"})))
	}
	s := at(t, oslo, "2026-09-12T15:00")
	r.sources.time.give(me, contracts.CalendarItem{Kind: "time.entry", ID: "1", Title: "Work", StartsAt: s, EndsAt: s.Add(time.Hour)})
	turnOn(t, c, "default", "full", "calendar.appointment", "time.entry")
	mailboxCycle(t, r)
	got := byKey(r, mb, "")
	if len(got) != 1500 {
		t.Errorf("copies = %d, want 1500", len(got))
	}
	if _, ok := got[key(series[0].Id, "2026-09-12")]; !ok {
		t.Error("today's occurrence was left out")
	}
	if _, ok := got[key(series[0].Id, "2027-03-11")]; ok {
		t.Error("the farthest occurrence was kept")
	}
	if _, ok := got["time.entry:1"]; ok {
		t.Error("a projected item was written past the cap")
	}
	if !strings.Contains(r.Logs(), "farthest appointment occurrences were left out") {
		t.Error("the cut was not logged")
	}
}
