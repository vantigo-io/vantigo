package calendar_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The write-back from Outlook (calendar Outlook sync design O10), against
// modtest's fake mailbox provider: the person's edits of a copy, as EditEvent
// and its neighbours shape them, taken into the appointment or put back. The
// harness clock starts on Saturday 12 September 2026 at 14:00 in Oslo; every
// harness also holds the lock rule — no call out of the module from inside
// one of its transactions — over each test (newHarness).

// nextSync moves the clock past the poll and runs a cycle.
func nextSync(t *testing.T, r *mailboxRig) {
	t.Helper()
	r.Advance(16 * time.Minute)
	mailboxCycle(t, r)
}

// copyOf is the copy carrying key in the mailbox's calendar, failing when
// there is none.
func copyOf(t *testing.T, r *mailboxRig, mailbox, calendarID, key string) modtest.FakeMailboxEvent {
	t.Helper()
	e, ok := byKey(r, mailbox, calendarID)[key]
	if !ok {
		t.Fatalf("no copy carries %s", key)
	}
	return e
}

// appointment is the appointment as its owner reads it.
func appointment(t *testing.T, c *modtest.Client, id int64) gen.Appointment {
	t.Helper()
	var out gen.Appointment
	do(t, c, http.MethodGet, appointmentPath(id), nil, http.StatusOK, &out)
	return out
}

// changeKey is the map row's change key, "" for none.
func changeKey(t *testing.T, r *mailboxRig, userID uuid.UUID, itemKey string) string {
	t.Helper()
	v := modtest.One[*string](t, r.Harness, `SELECT change_key FROM calendar.mailbox_events WHERE user_id = $1 AND item_key = $2`, userID, itemKey)
	if v == nil {
		return ""
	}
	return *v
}

// writeBackPerson is a person whose sync is on with detail, and the single
// appointment "Standup", Monday 14 September 09:00–09:30 in Oslo, synced.
func writeBackPerson(t *testing.T, r *mailboxRig, detail string, extra ...any) (*modtest.Client, uuid.UUID, string, gen.Appointment) {
	t.Helper()
	c, me, mb := r.person(t)
	turnOn(t, c, "default", detail, "calendar.appointment", "staff.absence")
	a := create(t, c, with(timed(t, "Standup", oslo, "2026-09-14T09:00", "2026-09-14T09:30"), append([]any{"location", "Room 4", "description", "Agenda"}, extra...)...))
	mailboxCycle(t, r)
	return c, me, mb, a
}

func TestMailboxWriteBack_TakesTheOwnersEditOfEachFieldAndTellsTheInvitees(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb, a := writeBackPerson(t, r, "full", "attendees", []map[string]any{emailAttendee(perMail, "")})
	r.mail.take()
	ev := copyOf(t, r, mb, "", key(a.Id))

	r.box.EditEvent(mb, ev.ID, func(e *contracts.MailboxEvent) {
		e.Subject = "Standup, moved"
		e.Body = strings.Replace(e.Body, "Agenda", "New agenda\r\n", 1)
		e.Location = "Room 5 "
		e.StartsAt, e.EndsAt = at(t, oslo, "2026-09-14T10:00"), at(t, oslo, "2026-09-14T10:45")
		e.ShowAs, e.Private = contracts.ShowAsFree, true
	})
	nextSync(t, r)

	got := appointment(t, c, a.Id)
	if got.Title != "Standup, moved" || got.Description == nil || *got.Description != "New agenda" ||
		got.Location == nil || *got.Location != "Room 5" || got.StartsAt == nil || !got.StartsAt.Equal(at(t, oslo, "2026-09-14T10:00")) ||
		!got.EndsAt.Equal(at(t, oslo, "2026-09-14T10:45")) || got.Transparency != gen.AppointmentTransparencyFree ||
		got.Visibility != gen.Private || got.Revision == a.Revision {
		t.Fatalf("the appointment after the edit = %+v", got)
	}
	// The update is the PUT's: SEQUENCE moved on, the outside attendee sent
	// the REQUEST.
	msgs := r.mail.take()
	if m := to(t, msgs, perMail); m.Calendar.Method != "REQUEST" || !strings.Contains(icsOf(t, m), "SEQUENCE:1\r\n") {
		t.Errorf("the invitee was sent %q", icsOf(t, m))
	}
	// The copy is the appointment's own form again, tagged, its version kept.
	cp := copyOf(t, r, mb, "", key(a.Id))
	if cp.Event.Subject != "Standup, moved" || !strings.HasPrefix(cp.Event.Body, "New agenda\n\nKept by Vantigo") || cp.Event.Location != "Room 5" {
		t.Errorf("the copy = %+v", cp.Event)
	}
	if changeKey(t, r, me, key(a.Id)) != cp.ChangeKey {
		t.Error("the copy's version was not kept")
	}
	if pb := getSync(t, c).PutBack; pb != nil {
		t.Errorf("a change taken was put back: %+v", pb)
	}

	// The next sync sees nothing to take or to write.
	before := len(r.box.Calls())
	nextSync(t, r)
	if n := len(calls(r, before, "Events")) + len(calls(r, before, "Apply")); n != 0 {
		t.Errorf("a sync with nothing changed read or wrote %d times", n)
	}

	// Made all-day in Outlook, it is all-day in Vantigo, in its zone.
	r.box.EditEvent(mb, cp.ID, func(e *contracts.MailboxEvent) {
		e.AllDay, e.StartsAt, e.EndsAt = true, time.Time{}, time.Time{}
		e.StartDate, e.EndDate = day("2026-09-15"), day("2026-09-16")
	})
	nextSync(t, r)
	got = appointment(t, c, a.Id)
	if !got.AllDay || got.StartDate == nil || got.StartDate.Format(time.DateOnly) != "2026-09-15" ||
		got.EndDate.Format(time.DateOnly) != "2026-09-16" || got.StartsAt != nil || got.TimeZone != oslo {
		t.Errorf("the all-day appointment = %+v", got)
	}
}

func TestMailboxWriteBack_ExchangesOwnTouchIsNoiseAndAVersionUnknownIsABaseline(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb, a := writeBackPerson(t, r, "full")
	ev := copyOf(t, r, mb, "", key(a.Id))

	r.box.TouchEvent(mb, ev.ID)
	before := len(r.box.Calls())
	nextSync(t, r)
	touched := copyOf(t, r, mb, "", key(a.Id))
	if got := appointment(t, c, a.Id); got.Revision != a.Revision {
		t.Errorf("a touch changed the appointment: %+v", got)
	}
	if len(calls(r, before, "Events")) != 1 || len(calls(r, before, "Apply")) != 0 || changeKey(t, r, me, key(a.Id)) != touched.ChangeKey {
		t.Errorf("a touch: read %d, applied %d, version %q (the copy's %q)",
			len(calls(r, before, "Events")), len(calls(r, before, "Apply")), changeKey(t, r, me, key(a.Id)), touched.ChangeKey)
	}

	// A row written before the write-back has no version: the one answered
	// is its baseline, taken unread — an edit made before it is not seen.
	r.Exec(t, `UPDATE calendar.mailbox_events SET change_key = NULL WHERE user_id = $1`, me)
	r.box.EditEvent(mb, ev.ID, func(e *contracts.MailboxEvent) { e.Subject = "Before the baseline" })
	before = len(r.box.Calls())
	nextSync(t, r)
	if len(calls(r, before, "Events")) != 0 || appointment(t, c, a.Id).Title != "Standup" ||
		changeKey(t, r, me, key(a.Id)) != copyOf(t, r, mb, "", key(a.Id)).ChangeKey {
		t.Error("a row with no version was not given the one answered as its baseline")
	}
}

func TestMailboxWriteBack_AnOccurrenceMovesAndItsTextIsPutBack(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	a := create(t, c, with(timed(t, "Weekly", oslo, "2026-09-15T10:00", "2026-09-15T11:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	mailboxCycle(t, r)

	second := copyOf(t, r, mb, "", key(a.Id, "2026-09-22"))
	r.box.EditEvent(mb, second.ID, func(e *contracts.MailboxEvent) {
		e.StartsAt, e.EndsAt = at(t, oslo, "2026-09-23T12:00"), at(t, oslo, "2026-09-23T13:00")
	})
	third := copyOf(t, r, mb, "", key(a.Id, "2026-09-29"))
	r.box.EditEvent(mb, third.ID, func(e *contracts.MailboxEvent) { e.Subject = "Only this week" })
	nextSync(t, r)

	got := appointment(t, c, a.Id)
	if len(got.Exceptions) != 1 || got.Exceptions[0].OccurrenceDate.Format(time.DateOnly) != "2026-09-22" ||
		got.Exceptions[0].StartsAt == nil || !got.Exceptions[0].StartsAt.Equal(at(t, oslo, "2026-09-23T12:00")) || got.Title != "Weekly" {
		t.Fatalf("the series after the edits = %+v", got)
	}
	if cp := copyOf(t, r, mb, "", key(a.Id, "2026-09-22")); !cp.Event.StartsAt.Equal(at(t, oslo, "2026-09-23T12:00")) {
		t.Errorf("the moved occurrence's copy = %+v", cp.Event)
	}
	if cp := copyOf(t, r, mb, "", key(a.Id, "2026-09-29")); cp.Event.Subject != "Weekly" {
		t.Errorf("the occurrence's text was not put back: %q", cp.Event.Subject)
	}
	pb := getSync(t, c).PutBack
	if pb == nil || pb.Reason != gen.NotEditable || pb.AppointmentId == nil || *pb.AppointmentId != a.Id ||
		pb.OccurrenceDate == nil || pb.OccurrenceDate.Format(time.DateOnly) != "2026-09-29" ||
		pb.Title == nil || *pb.Title != "Weekly" || !pb.At.Equal(r.Now()) {
		t.Errorf("putBack = %+v", pb)
	}

	// Turning the sync off forgets it.
	off := putSync(t, c, getSync(t, c).Revision, syncBody(false, "default", "full", "calendar.appointment"))
	if off.PutBack != nil || getSync(t, c).PutBack != nil {
		t.Error("turning the sync off kept the put-back")
	}
}

func TestMailboxWriteBack_VantigoWinsAConflict(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, mb, a := writeBackPerson(t, r, "full")
	ev := copyOf(t, r, mb, "", key(a.Id))

	r.box.EditEvent(mb, ev.ID, func(e *contracts.MailboxEvent) { e.Subject = "Outlook's title" })
	do(t, c, http.MethodPut, appointmentPath(a.Id), timed(t, "Vantigo's title", oslo, "2026-09-14T09:00", "2026-09-14T09:30"), http.StatusOK, nil, ifMatch(a.Revision))
	nextSync(t, r)

	if got := appointment(t, c, a.Id); got.Title != "Vantigo's title" {
		t.Errorf("the appointment = %q", got.Title)
	}
	if cp := copyOf(t, r, mb, "", key(a.Id)); cp.Event.Subject != "Vantigo's title" {
		t.Errorf("the copy = %q", cp.Event.Subject)
	}
	if pb := getSync(t, c).PutBack; pb == nil || pb.Reason != gen.Conflict || pb.Title == nil || *pb.Title != "Vantigo's title" {
		t.Errorf("putBack = %+v", pb)
	}
}

func TestMailboxWriteBack_PutsBackAnAttendeesCopyAProjectedItemAndARefusal(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	colleague, _ := signIn(t, r.harness)
	c, me, mb := r.person(t, "staff:access")
	turnOn(t, c, "default", "full", "calendar.appointment", "staff.absence")
	mine := create(t, c, timed(t, "Standup", oslo, "2026-09-14T09:00", "2026-09-14T09:30"))
	theirs := create(t, colleague, with(timed(t, "Their meeting", oslo, "2026-09-16T09:00", "2026-09-16T10:00"), "attendees", userAttendees(me.String())))
	absenceDay := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	r.sources.staff.give(me, contracts.CalendarItem{Kind: "staff.absence", ID: "7", Title: "Vacation", AllDay: true, StartDate: absenceDay, EndDate: absenceDay})
	nextSync(t, r)

	steps := []struct {
		name, key string
		edit      func(e *contracts.MailboxEvent)
		reason    gen.MailboxSyncPutBackReason
		check     func(e contracts.MailboxEvent) bool
	}{
		{"an attendee's copy", key(theirs.Id), func(e *contracts.MailboxEvent) { e.Subject = "Mine now" }, gen.NotOwner,
			func(e contracts.MailboxEvent) bool { return e.Subject == "Their meeting" }},
		{"a projected item", "staff.absence:7", func(e *contracts.MailboxEvent) { e.Subject = "Holiday!" }, gen.NotEditable,
			func(e contracts.MailboxEvent) bool { return e.Subject == "Vacation" }},
		{"a span the rules refuse", key(mine.Id), func(e *contracts.MailboxEvent) { e.EndsAt = e.StartsAt.Add(40 * 24 * time.Hour) }, gen.Refused,
			func(e contracts.MailboxEvent) bool { return e.EndsAt.Equal(at(t, oslo, "2026-09-14T09:30")) }},
	}
	for _, s := range steps {
		r.box.EditEvent(mb, copyOf(t, r, mb, "", s.key).ID, s.edit)
		nextSync(t, r)
		if cp := copyOf(t, r, mb, "", s.key); !s.check(cp.Event) {
			t.Errorf("%s was not put back: %+v", s.name, cp.Event)
		}
		if pb := getSync(t, c).PutBack; pb == nil || pb.Reason != s.reason {
			t.Errorf("%s: putBack = %+v", s.name, pb)
		}
	}
	if got := appointment(t, colleague, theirs.Id); got.Title != "Their meeting" || got.Revision != theirs.Revision {
		t.Errorf("the colleague's appointment changed: %+v", got)
	}
	if got := appointment(t, c, mine.Id); got.Revision != mine.Revision {
		t.Errorf("the refused change was written: %+v", got)
	}
	// The attendee's put-back names the appointment, which they attend.
	r.box.EditEvent(mb, copyOf(t, r, mb, "", key(theirs.Id)).ID, func(e *contracts.MailboxEvent) { e.Location = "Elsewhere" })
	nextSync(t, r)
	if pb := getSync(t, c).PutBack; pb == nil || pb.Title == nil || *pb.Title != "Their meeting" || *pb.AppointmentId != theirs.Id {
		t.Errorf("the attendee's putBack = %+v", pb)
	}
}

func TestMailboxWriteBack_DeletesInOutlook(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	single := create(t, c, timed(t, "Alone", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	invited := create(t, c, with(timed(t, "With a customer", oslo, "2026-09-15T09:00", "2026-09-15T10:00"),
		"attendees", []map[string]any{emailAttendee(perMail, "")}))
	series := create(t, c, with(timed(t, "Weekly", oslo, "2026-09-16T10:00", "2026-09-16T11:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	mailboxCycle(t, r)
	r.mail.take()

	r.box.DeleteEvent(mb, copyOf(t, r, mb, "", key(single.Id)).ID)
	r.box.DeleteEvent(mb, copyOf(t, r, mb, "", key(invited.Id)).ID)
	r.box.DeleteEvent(mb, copyOf(t, r, mb, "", key(series.Id, "2026-09-23")).ID)
	nextSync(t, r)

	do(t, c, http.MethodGet, appointmentPath(single.Id), nil, http.StatusNotFound, nil)
	if got := appointment(t, c, invited.Id); got.CancelledAt == nil {
		t.Errorf("the appointment with an invitee was not cancelled: %+v", got)
	}
	if m := to(t, r.mail.take(), perMail); m.Calendar.Method != "CANCEL" {
		t.Errorf("the invitee was sent %s", m.Calendar.Method)
	}
	got := appointment(t, c, series.Id)
	if len(got.Exceptions) != 1 || got.Exceptions[0].OccurrenceDate.Format(time.DateOnly) != "2026-09-23" || !got.Exceptions[0].Cancelled {
		t.Errorf("the occurrence was not cancelled: %+v", got.Exceptions)
	}
	// Nothing is made again: the map holds the series' two others.
	if n := mapped(t, r, me); n != 2 || len(r.box.EventsIn(mb, "")) != 2 {
		t.Errorf("mapped %d, copies %d", n, len(r.box.EventsIn(mb, "")))
	}
}

func TestMailboxWriteBack_AMoveOutOfTheWindowIsAChangeAndToAnotherCalendarADelete(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	early := create(t, c, timed(t, "Moved early", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	away := create(t, c, timed(t, "Moved away", oslo, "2026-09-15T09:00", "2026-09-15T10:00"))
	mailboxCycle(t, r)

	earlyID := copyOf(t, r, mb, "", key(early.Id)).ID
	r.box.EditEvent(mb, earlyID, func(e *contracts.MailboxEvent) {
		e.StartsAt, e.EndsAt = at(t, oslo, "2026-08-01T09:00"), at(t, oslo, "2026-08-01T10:00")
	})
	r.box.MoveEvent(mb, copyOf(t, r, mb, "", key(away.Id)).ID, "personal")
	nextSync(t, r)

	if got := appointment(t, c, early.Id); !got.StartsAt.Equal(at(t, oslo, "2026-08-01T09:00")) {
		t.Errorf("the move before the window was not taken: %+v", got)
	}
	// Before the window, the copy stays where the person put it, forgotten.
	if e := byKey(r, mb, ""); e[key(early.Id)].ID != earlyID || mapped(t, r, me) != 0 {
		t.Errorf("the early copy: %+v, mapped %d", e, mapped(t, r, me))
	}
	do(t, c, http.MethodGet, appointmentPath(away.Id), nil, http.StatusNotFound, nil)
}

func TestMailboxWriteBack_AnIncompleteListInfersNoDelete(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, _, mb, a := writeBackPerson(t, r, "full")
	for i := range contracts.MailboxVersionsMax + 1 {
		r.box.PutEvent(mb, "", "other:"+strconv.Itoa(i), contracts.MailboxEvent{
			Subject: "x", StartsAt: at(t, oslo, "2026-09-20T09:00"), EndsAt: at(t, oslo, "2026-09-20T10:00"),
		})
	}
	r.box.DeleteEvent(mb, copyOf(t, r, mb, "", key(a.Id)).ID)
	nextSync(t, r)
	if got := appointment(t, c, a.Id); got.Title != "Standup" || got.CancelledAt != nil {
		t.Errorf("an absence from a cut list deleted the appointment: %+v", got)
	}
}

func TestMailboxWriteBack_AMeetingIsReleasedAndNeverWrittenOrDeleted(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb, a := writeBackPerson(t, r, "full")
	ev := copyOf(t, r, mb, "", key(a.Id))
	r.box.SetMeeting(mb, ev.ID, true)
	r.box.EditEvent(mb, ev.ID, func(e *contracts.MailboxEvent) { e.Subject = "Team meeting" })
	nextSync(t, r)

	if got := appointment(t, c, a.Id); got.Title != "Standup" || got.Revision != a.Revision {
		t.Errorf("a meeting was written back: %+v", got)
	}
	if n := r.Count(t, `SELECT count(*) FROM calendar.mailbox_events WHERE user_id = $1 AND released_at IS NOT NULL`, me); n != 1 {
		t.Fatalf("released %d", n)
	}
	// Vantigo's change is not pushed to it.
	before := len(r.box.Calls())
	do(t, c, http.MethodPut, appointmentPath(a.Id), timed(t, "Standup B", oslo, "2026-09-14T09:00", "2026-09-14T09:30"), http.StatusOK, nil, ifMatch(a.Revision))
	nextSync(t, r)
	for _, op := range opsOf(calls(r, before, "Apply")) {
		if op.EventID == ev.ID {
			t.Errorf("the meeting was written: %+v", op)
		}
	}
	// The clean-up forgets it without a delete.
	putSync(t, c, getSync(t, c).Revision, syncBody(false, "default", "full", "calendar.appointment"))
	nextSync(t, r)
	if e := r.box.EventsIn(mb, ""); len(e) != 1 || e[0].ID != ev.ID || e[0].Event.Subject != "Team meeting" {
		t.Errorf("the meeting after the clean-up: %+v", e)
	}
	if mapped(t, r, me) != 0 {
		t.Error("the clean-up kept the map")
	}
}

func TestMailboxWriteBack_ANewEventInTheVantigoCalendarIsTakenOnce(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "own", "full", "calendar.appointment")
	mailboxCycle(t, r)
	cal := modtest.One[string](t, r.Harness, `SELECT calendar_id FROM calendar.mailbox_sync WHERE user_id = $1`, me)

	dentist := r.box.PutOwnEvent(mb, cal, contracts.MailboxEvent{
		Subject: "Dentist", Body: "Bring the card", Location: "Storgata 2",
		StartsAt: at(t, oslo, "2026-09-17T08:00"), EndsAt: at(t, oslo, "2026-09-17T09:00"), ShowAs: contracts.ShowAsBusy, Private: true,
	})
	blank := r.box.PutOwnEvent(mb, cal, contracts.MailboxEvent{
		AllDay: true, StartDate: day("2026-09-18"), EndDate: day("2026-09-18"), ShowAs: contracts.ShowAsFree,
	})
	meeting := r.box.PutOwnEvent(mb, cal, contracts.MailboxEvent{Subject: "With others", StartsAt: at(t, oslo, "2026-09-19T08:00"), EndsAt: at(t, oslo, "2026-09-19T09:00")})
	r.box.SetMeeting(mb, meeting, true)
	// The push that would tag them fails.
	r.box.FailOps(2, errors.New("graph: 503"))
	nextSync(t, r)

	owned := func() int {
		return r.Count(t, `SELECT count(*) FROM calendar.appointments WHERE owner_user_id = $1`, me)
	}
	if n := owned(); n != 2 {
		t.Fatalf("appointments taken in: %d", n)
	}
	if s := readSyncRow(t, r, me); s.attempts != 1 {
		t.Errorf("the failed tag was not counted: %+v", s)
	}
	r.Advance(2 * time.Minute)
	mailboxCycle(t, r)
	if n := owned(); n != 2 {
		t.Fatalf("taken twice: %d", n)
	}
	for _, id := range []string{dentist, blank} {
		var found modtest.FakeMailboxEvent
		for _, e := range r.box.EventsIn(mb, cal) {
			if e.ID == id {
				found = e
			}
		}
		appt, err := strconv.ParseInt(strings.TrimPrefix(found.Key, "appointment:"), 10, 64)
		if err != nil {
			t.Fatalf("event %s was not tagged: %+v", id, found)
		}
		got := appointment(t, c, appt)
		switch id {
		case dentist:
			if got.Title != "Dentist" || got.Description == nil || *got.Description != "Bring the card" || *got.Location != "Storgata 2" ||
				got.Visibility != gen.Private || got.Transparency != gen.AppointmentTransparencyBusy ||
				!got.StartsAt.Equal(at(t, oslo, "2026-09-17T08:00")) || got.TimeZone != oslo {
				t.Errorf("the dentist = %+v", got)
			}
		case blank:
			if got.Title != "Untitled" || !got.AllDay || got.StartDate.Format(time.DateOnly) != "2026-09-18" || got.Transparency != gen.AppointmentTransparencyFree {
				t.Errorf("the untitled one = %+v", got)
			}
		}
	}
}

func TestMailboxWriteBack_WritesAtMostTwentyASync(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	for i := range 25 {
		d := time.Date(2026, 9, 14+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		create(t, c, timed(t, "Day "+strconv.Itoa(i), oslo, d+"T09:00", d+"T10:00"))
	}
	mailboxCycle(t, r)
	for _, e := range r.box.EventsIn(mb, "") {
		r.box.EditEvent(mb, e.ID, func(ev *contracts.MailboxEvent) { ev.Subject += " (edited)" })
	}
	edited := func() int {
		return r.Count(t, `SELECT count(*) FROM calendar.appointments WHERE owner_user_id = $1 AND title LIKE '%(edited)'`, me)
	}
	nextSync(t, r)
	if n := edited(); n != 20 {
		t.Errorf("the first sync wrote %d", n)
	}
	nextSync(t, r)
	if n := edited(); n != 25 {
		t.Errorf("the second sync left %d written", n)
	}
}

func TestMailboxWriteBack_AFailedReadDoesNotHideThePushAndARefusalStops(t *testing.T) {
	t.Parallel()
	t.Run("a failure", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, me, mb := r.person(t)
		turnOn(t, c, "default", "full", "calendar.appointment")
		create(t, c, timed(t, "Pushed", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
		r.box.FailWith("Versions", errors.New("graph: 503"))
		mailboxCycle(t, r)
		if len(r.box.EventsIn(mb, "")) != 1 {
			t.Error("the push did not go on")
		}
		if s := readSyncRow(t, r, me); s.state != "active" || s.errCode == nil || *s.errCode != "failed" || s.attempts != 1 ||
			!s.next.Equal(r.Now().Add(time.Minute)) {
			t.Errorf("the row = %+v", s)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, me, _ := r.person(t)
		turnOn(t, c, "default", "full", "calendar.appointment")
		create(t, c, timed(t, "Not pushed", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
		r.box.FailWith("Versions", contracts.ErrMailboxDenied)
		mailboxCycle(t, r)
		if n := len(calls(r, 0, "Apply")); n != 0 {
			t.Errorf("applied %d after a refusal", n)
		}
		if s := readSyncRow(t, r, me); s.state != "unavailable" || s.errCode == nil || *s.errCode != "mailbox_denied" {
			t.Errorf("the row = %+v", s)
		}
	})
}

func TestMailboxWriteBack_TakesOnlyTheTimesUnderBusyAndNoBodyThatIsNotText(t *testing.T) {
	t.Parallel()
	t.Run("detail busy", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, _, mb, a := writeBackPerson(t, r, "busy")
		r.box.EditEvent(mb, copyOf(t, r, mb, "", key(a.Id)).ID, func(e *contracts.MailboxEvent) {
			e.Subject, e.Location = "Something else", "Somewhere"
			e.StartsAt, e.EndsAt = at(t, oslo, "2026-09-14T11:00"), at(t, oslo, "2026-09-14T11:30")
		})
		nextSync(t, r)
		got := appointment(t, c, a.Id)
		if got.Title != "Standup" || *got.Location != "Room 4" || !got.StartsAt.Equal(at(t, oslo, "2026-09-14T11:00")) {
			t.Errorf("under busy = %+v", got)
		}
		if cp := copyOf(t, r, mb, "", key(a.Id)); cp.Event.Subject != "Busy" || cp.Event.Location != "" {
			t.Errorf("the copy was not put back to its placeholders: %+v", cp.Event)
		}
	})
	t.Run("an HTML body", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, _, mb, a := writeBackPerson(t, r, "full")
		id := copyOf(t, r, mb, "", key(a.Id)).ID
		r.box.SetHTMLBody(mb, id, true)
		r.box.EditEvent(mb, id, func(e *contracts.MailboxEvent) { e.Body = "<p>New</p>" })
		nextSync(t, r)
		if got := appointment(t, c, a.Id); *got.Description != "Agenda" || got.Revision != a.Revision {
			t.Errorf("an HTML body was taken: %+v", got)
		}
	})
}
