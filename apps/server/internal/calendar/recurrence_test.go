package calendar_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestPostAppointment_StoresTheRuleCanonicalWithItsLastDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	base := timed(t, "Series", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00")
	for _, tc := range []struct{ rule, canonical, last string }{
		{"FREQ=WEEKLY;BYDAY=WE,MO;COUNT=4", "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;COUNT=4", "2026-11-11"},
		{"FREQ=DAILY;UNTIL=20261110", "FREQ=DAILY;INTERVAL=1;UNTIL=20261110", "2026-11-10"},
		{"FREQ=MONTHLY;BYDAY=1MO", "FREQ=MONTHLY;INTERVAL=1;BYDAY=1MO", ""},
	} {
		a := create(t, c, with(base, "recurrence", map[string]any{"rule": tc.rule}))
		if a.Recurrence == nil || a.Recurrence.Rule != tc.canonical {
			t.Errorf("%s: recurrence = %+v, want %s", tc.rule, a.Recurrence, tc.canonical)
			continue
		}
		stored := modtest.One[string](t, h.Harness, `SELECT rrule FROM calendar.appointments WHERE id = $1`, a.Id)
		last := modtest.One[string](t, h.Harness, `SELECT coalesce(series_last_date::text, '') FROM calendar.appointments WHERE id = $1`, a.Id)
		if stored != tc.canonical || last != tc.last {
			t.Errorf("%s: stored %q last %q, want %q %q", tc.rule, stored, last, tc.canonical, tc.last)
		}
		if (a.Recurrence.LastDate == nil) != (tc.last == "") || (a.Recurrence.LastDate != nil && a.Recurrence.LastDate.String() != tc.last) {
			t.Errorf("%s: lastDate = %v, want %q", tc.rule, a.Recurrence.LastDate, tc.last)
		}
	}
}

func TestPostAppointment_RefusesARuleOutsideTheSubsetOnRecurrenceRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	base := allDay("Series", "Europe/Oslo", "2026-11-02", "2026-11-02")
	for _, rule := range []string{
		"FREQ=YEARLY",
		"FREQ=MONTHLY;BYDAY=MO,TU;BYSETPOS=1",
		"FREQ=WEEKLY;BYDAY=MO;BYDAY=TU",
		"FREQ=DAILY;UNTIL=20311103", // more than five years after the start
		"FREQ=DAILY;UNTIL=20261101", // before the start
	} {
		fieldRefused(t, c.Do(http.MethodPost, appointmentsPath, with(base, "recurrence", map[string]any{"rule": rule})), "recurrence.rule")
	}
	// Five years to the day is the limit, and is accepted.
	create(t, c, with(base, "recurrence", map[string]any{"rule": "FREQ=MONTHLY;UNTIL=20311102"}))
}

func TestItems_AWeeklyOsloSeriesExpandedAcrossTheOctoberClockChangeKeeps0900WallClock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	a := create(t, c, with(timed(t, "Team meeting", "Europe/Oslo", "2026-10-19T09:00", "2026-10-19T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY"}))

	got := ofKind(items(t, c, windowPath("2026-10-19", "2026-11-08", "timeZone=Europe/Oslo")), "calendar.appointment")
	want := []struct{ date, startsAt, endsAt string }{
		{"2026-10-19", "2026-10-19T07:00:00Z", "2026-10-19T08:00:00Z"},
		{"2026-10-26", "2026-10-26T08:00:00Z", "2026-10-26T09:00:00Z"},
		{"2026-11-02", "2026-11-02T08:00:00Z", "2026-11-02T09:00:00Z"},
	}
	if len(got) != len(want) {
		t.Fatalf("occurrences = %v, want %d", ids(got), len(want))
	}
	oslo, _ := time.LoadLocation("Europe/Oslo")
	for i, w := range want {
		g := got[i]
		if g.Id != itemID(a.Id, w.date) || g.StartsAt.UTC().Format(time.RFC3339) != w.startsAt || g.EndsAt.UTC().Format(time.RFC3339) != w.endsAt {
			t.Errorf("occurrence %d = %s %s..%s, want %+v", i, g.Id, g.StartsAt.UTC(), g.EndsAt.UTC(), w)
		}
		if g.StartsAt.In(oslo).Format("15:04") != "09:00" {
			t.Errorf("occurrence %d starts at %s in Oslo", i, g.StartsAt.In(oslo).Format("15:04"))
		}
		if !g.Appointment.Recurring || g.Appointment.OccurrenceDate == nil || g.Appointment.OccurrenceDate.String() != w.date || !g.Appointment.IsOwner {
			t.Errorf("occurrence %d appointment = %+v", i, g.Appointment)
		}
	}
}

func TestOccurrence_CancelledIsGoneAndRestoredComesBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	a := create(t, c, with(allDay("Gym", "Europe/Oslo", "2026-11-02", "2026-11-02"), "recurrence", map[string]any{"rule": "FREQ=DAILY;COUNT=5"}))

	var exc gen.AppointmentOccurrence
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-04"), map[string]any{"cancelled": true}, http.StatusOK, &exc)
	if !exc.Cancelled || exc.OccurrenceDate.String() != "2026-11-04" {
		t.Errorf("exception = %+v", exc)
	}
	got := ids(ofKind(items(t, c, windowPath("2026-11-01", "2026-11-10")), "calendar.appointment"))
	if len(got) != 4 || contains(got, itemID(a.Id, "2026-11-04")) {
		t.Errorf("after the cancel = %v", got)
	}
	var now gen.Appointment
	do(t, c, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &now)
	if now.Revision != a.Revision+1 {
		t.Errorf("revision after the exception = %d, want %d", now.Revision, a.Revision+1)
	}

	do(t, c, http.MethodDelete, occurrencePath(a.Id, "2026-11-04"), nil, http.StatusNoContent, nil)
	do(t, c, http.MethodDelete, occurrencePath(a.Id, "2026-11-04"), nil, http.StatusNoContent, nil)
	got = ids(ofKind(items(t, c, windowPath("2026-11-01", "2026-11-10")), "calendar.appointment"))
	if len(got) != 5 || !contains(got, itemID(a.Id, "2026-11-04")) {
		t.Errorf("after the restore = %v", got)
	}
}

func TestOccurrence_MovedToAnotherDayStillAnswersToItsDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	a := create(t, c, with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	newStart := at(t, "Europe/Oslo", "2026-11-12T14:00")
	var exc gen.AppointmentOccurrence
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-09"),
		map[string]any{"startsAt": newStart.UTC(), "endsAt": newStart.Add(90 * time.Minute).UTC()}, http.StatusOK, &exc)
	if exc.Cancelled || exc.StartsAt == nil || !exc.StartsAt.Equal(newStart) {
		t.Errorf("exception = %+v", exc)
	}

	// The week it came from no longer has it; the day it moved to does, under
	// its old date.
	if got := ofKind(items(t, c, windowPath("2026-11-09", "2026-11-11")), "calendar.appointment"); len(got) != 0 {
		t.Errorf("the original week still shows %v", ids(got))
	}
	got := ofKind(items(t, c, windowPath("2026-11-12", "2026-11-12")), "calendar.appointment")
	if len(got) != 1 || got[0].Id != itemID(a.Id, "2026-11-09") || !got[0].StartsAt.Equal(newStart) ||
		!got[0].EndsAt.Equal(newStart.Add(90*time.Minute)) || got[0].Appointment.OccurrenceDate.String() != "2026-11-09" {
		t.Errorf("the moved occurrence = %+v", got)
	}

	// A timed series is moved by its instants, never its dates.
	fieldRefused(t, c.Do(http.MethodPut, occurrencePath(a.Id, "2026-11-16"), map[string]any{"startDate": "2026-11-17", "endDate": "2026-11-17"}), "startsAt")
	// Cancelled and a move at once is no exception at all.
	fieldRefused(t, c.Do(http.MethodPut, occurrencePath(a.Id, "2026-11-16"),
		map[string]any{"cancelled": true, "startsAt": newStart.UTC(), "endsAt": newStart.Add(time.Hour).UTC()}), "cancelled")
}

func TestOccurrence_IsRefusedForADayTheRuleNeverProducesAndOnASingleAppointment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	other, _ := signIn(t, h)
	series := create(t, c, with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=3"}))
	refused(t, c.Do(http.MethodPut, occurrencePath(series.Id, "2026-11-03"), map[string]any{"cancelled": true}), http.StatusConflict, "occurrence_not_in_series")
	refused(t, c.Do(http.MethodPut, occurrencePath(series.Id, "2026-11-23"), map[string]any{"cancelled": true}), http.StatusConflict, "occurrence_not_in_series")

	single := create(t, c, timed(t, "Once", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"))
	refused(t, c.Do(http.MethodPut, occurrencePath(single.Id, "2026-11-02"), map[string]any{"cancelled": true}), http.StatusBadRequest, "")

	if r := other.Do(http.MethodPut, occurrencePath(series.Id, "2026-11-09"), map[string]any{"cancelled": true}); r.Status != http.StatusNotFound {
		t.Errorf("someone else's series: status %d, want 404", r.Status)
	}
	if r := other.Do(http.MethodDelete, occurrencePath(series.Id, "2026-11-09"), nil); r.Status != http.StatusNotFound {
		t.Errorf("someone else's restore: status %d, want 404", r.Status)
	}
}

func TestPutAppointment_ChangingTheRuleDropsEveryException(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	body := with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "recurrence", map[string]any{"rule": "FREQ=WEEKLY"})
	a := create(t, c, body)
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-09"), map[string]any{"cancelled": true}, http.StatusOK, nil)

	// A change of the title keeps it.
	var renamed gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Weekly sync"), http.StatusOK, &renamed, ifMatch(a.Revision+1))
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointment_exceptions WHERE appointment_id = $1`, a.Id); n != 1 {
		t.Errorf("exceptions after a rename = %d, want 1", n)
	}
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "recurrence", map[string]any{"rule": "FREQ=WEEKLY;INTERVAL=2"}),
		http.StatusOK, nil, ifMatch(renamed.Revision))
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointment_exceptions WHERE appointment_id = $1`, a.Id); n != 0 {
		t.Errorf("exceptions after the rule changed = %d, want 0", n)
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 1 {
		t.Errorf("sequence = %d, want 1", seq)
	}
}
