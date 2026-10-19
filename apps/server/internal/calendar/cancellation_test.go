package calendar_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

func TestDelete_WithNobodyInvitedRemovesTheRowAndTellsTheColleagues(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en", modtest.WithEnv("MAIL_DRIVER", "log"))
	colleague := directoryUser(t, w.h, "Liv Lund", true)
	a := create(t, w.owner, meeting(t, attendees(userAttendees(colleague.String()), []map[string]any{emailAttendee(perMail, "")})...))
	before := len(w.h.Notices())
	do(t, w.owner, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	if n := w.h.Count(t, `SELECT count(*) FROM calendar.appointments WHERE id = $1`, a.Id); n != 0 {
		t.Errorf("the row is still there (%d)", n)
	}
	notices := w.h.Notices()[before:]
	if len(notices) != 1 || notices[0].UserID != colleague || notices[0].Notice.Texts["en"].Subject != "Kari Nordmann cancelled: Kundemøte, Mon 2 Nov 09:00" {
		t.Errorf("the delete told %+v", notices)
	}
}

func TestDelete_WithAnInviteeCancelsMailsCancelAndMakesTheAppointmentReadOnly(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	colleagueClient, colleague := signIn(t, w.h)
	a := create(t, w.owner, meeting(t, attendees(userAttendees(colleague.String()), []map[string]any{contactAttendee(olaID), emailAttendee(perMail, "")})...))
	w.h.mail.take()
	before := len(w.h.Notices())

	do(t, w.owner, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	msgs := w.h.mail.take()
	if got := recipients(msgs); !slices.Equal(got, []string{olaMail, perMail}) {
		t.Fatalf("the cancellation mailed %v", got)
	}
	for _, m := range msgs {
		ics := icsOf(t, m)
		if m.Calendar.Method != "CANCEL" || !strings.Contains(ics, "METHOD:CANCEL\r\n") || !strings.Contains(ics, "SEQUENCE:1\r\n") ||
			!strings.Contains(ics, "STATUS:CANCELLED\r\n") || len(lines(ics, "ATTENDEE")) != 2 || !strings.HasPrefix(m.Subject, "Cancelled: Kundemøte") {
			t.Errorf("the CANCEL to %v: %q\n%s", m.To, m.Subject, ics)
		}
	}
	notices := w.h.Notices()[before:]
	if len(notices) != 1 || notices[0].UserID != colleague || !strings.Contains(notices[0].Notice.Texts["en"].Subject, "cancelled") {
		t.Errorf("the cancellation told %+v", notices)
	}

	var got gen.Appointment
	do(t, w.owner, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if got.CancelledAt == nil || !got.CancelledAt.Equal(w.h.Now()) {
		t.Errorf("cancelledAt = %v, want now", got.CancelledAt)
	}
	do(t, colleagueClient, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, nil)
	if appts := ofKind(items(t, w.owner, windowPath("2026-11-02", "2026-11-02")), "calendar.appointment"); len(appts) != 0 {
		t.Errorf("the cancelled appointment is on the owner's calendar: %+v", appts)
	}
	viewer, _ := signIn(t, w.h, "calendar:view-colleagues")
	var colleagueView gen.CalendarItems
	do(t, viewer, http.MethodGet, colleaguePath(w.ownerID, "2026-11-02", "2026-11-02"), nil, http.StatusOK, &colleagueView)
	if len(colleagueView.Items) != 0 {
		t.Errorf("the cancelled appointment is in the colleague view: %+v", colleagueView.Items)
	}

	per := attendeeOf(t, got, perMail)
	for name, r := range map[string]*modtest.Response{
		"PUT":                w.owner.Do(http.MethodPut, appointmentPath(a.Id), meeting(t), ifMatch(got.Revision)),
		"DELETE again":       w.owner.Do(http.MethodDelete, appointmentPath(a.Id), nil),
		"an occurrence PUT":  w.owner.Do(http.MethodPut, occurrencePath(a.Id, "2026-11-02"), map[string]any{"cancelled": true}),
		"an occurrence DEL":  w.owner.Do(http.MethodDelete, occurrencePath(a.Id, "2026-11-02"), nil),
		"a colleague answer": colleagueClient.Do(http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}),
		"a resend":           w.owner.Do(http.MethodPost, resendPath(a.Id, per.Id), nil),
	} {
		if r.Status != http.StatusConflict {
			t.Errorf("%s on a cancelled appointment = %d %s, want 409", name, r.Status, r.Body)
			continue
		}
		refused(t, r, http.StatusConflict, "appointment_cancelled")
	}
	stranger, _ := signIn(t, w.h)
	for name, r := range map[string]*modtest.Response{
		"a stranger's DELETE": stranger.Do(http.MethodDelete, appointmentPath(a.Id), nil),
		"a stranger's answer": stranger.Do(http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("%s = %d, want the bare 404 before the cancellation", name, r.Status)
		}
	}
	if n := len(w.h.mail.messages()); n != 0 {
		t.Errorf("a refused write sent %d mails", n)
	}
}

// cancellationsWorker is the installation's calendar-cancellations worker.
func cancellationsWorker(t *testing.T, h *harness) *calendar.CancellationsWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if cw, ok := w.(*calendar.CancellationsWorker); ok {
			return cw
		}
	}
	t.Fatal("no calendar-cancellations worker")
	return nil
}

// cancelled creates body with an invitee and deletes it, so it is cancelled.
func cancelled(t *testing.T, w inviting, body map[string]any) gen.Appointment {
	t.Helper()
	a := create(t, w.owner, with(body, "attendees", []map[string]any{emailAttendee(perMail, "")}))
	do(t, w.owner, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	return a
}

func TestCancellationsWorker_ErasesACancelledAppointmentAfterItsLastDayNotBefore(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	single := cancelled(t, w, timed(t, "Single", "Europe/Oslo", "2026-09-20T09:00", "2026-09-20T10:00"))
	token := tokenOf(t, w.h.mail.messages()[0])
	unbounded := cancelled(t, w, with(timed(t, "Weekly", "Europe/Oslo", "2026-09-14T09:00", "2026-09-14T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY"}))
	bounded := cancelled(t, w, with(allDay("Daily", "Europe/Oslo", "2026-09-14", "2026-09-14"),
		"recurrence", map[string]any{"rule": "FREQ=DAILY;COUNT=10"}))
	live := create(t, w.owner, timed(t, "Live", "Europe/Oslo", "2026-09-13T09:00", "2026-09-13T10:00"))
	worker := cancellationsWorker(t, w.h)
	exists := func(id int64) bool {
		return w.h.Count(t, `SELECT count(*) FROM calendar.appointments WHERE id = $1`, id) == 1
	}
	cycle := func() int64 {
		t.Helper()
		ran, n, err := worker.RunCycle(context.Background())
		if err != nil || !ran {
			t.Fatalf("cycle: ran %v, %v", ran, err)
		}
		return n
	}

	if n := cycle(); n != 0 {
		t.Errorf("a cycle before any last day erased %d", n)
	}
	w.h.Advance(8*24*time.Hour + time.Hour) // 2026-09-20 13:00 UTC: the single has ended
	if n := cycle(); n != 1 || exists(single.Id) || !exists(unbounded.Id) || !exists(bounded.Id) || !exists(live.Id) {
		t.Errorf("after the single's end the cycle erased %d", n)
	}
	if r := w.h.Client(t).Do(http.MethodPost, rsvpReadPath, map[string]any{"token": token}); r.Status != http.StatusNotFound {
		t.Errorf("the erased attendee's token reads %d, want 404", r.Status)
	}
	w.h.Advance(4 * 24 * time.Hour) // 2026-09-24: the bounded series' last day, the 23rd, has passed
	if n := cycle(); n != 1 || exists(bounded.Id) || !exists(unbounded.Id) {
		t.Errorf("after the bounded series' last day the cycle erased %d", n)
	}
	w.h.Advance(77 * 24 * time.Hour) // 89 days and some after the cancellation
	if n := cycle(); n != 0 || !exists(unbounded.Id) {
		t.Errorf("before ninety days the cycle erased %d", n)
	}
	w.h.Advance(2 * 24 * time.Hour)
	if n := cycle(); n != 1 || exists(unbounded.Id) || !exists(live.Id) {
		t.Errorf("after ninety days the cycle erased %d", n)
	}
}

func TestCancellationsWorker_SkipsTheCycleWhileTheLeaseIsHeldElsewhere(t *testing.T) {
	t.Parallel()
	w := newInviting(t, "en")
	cancelled(t, w, timed(t, "Single", "Europe/Oslo", "2026-09-12T09:00", "2026-09-12T10:00"))
	conn, err := w.h.Pool().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, int64(0x43414C43414E4331)); err != nil {
		t.Fatal(err)
	}
	worker := cancellationsWorker(t, w.h)
	if ran, n, err := worker.RunCycle(context.Background()); ran || n != 0 || err != nil {
		t.Errorf("with the lease held elsewhere the cycle ran %v, erased %d, %v", ran, n, err)
	}
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(0x43414C43414E4331)); err != nil {
		t.Fatal(err)
	}
	if ran, n, err := worker.RunCycle(context.Background()); !ran || n != 1 || err != nil {
		t.Errorf("with the lease free the cycle ran %v, erased %d, %v", ran, n, err)
	}
}

func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want []string
		poll time.Duration
	}{
		{name: "the default installation runs the sweeper", want: []string{"calendar-cancellations"}, poll: time.Hour},
		{name: "the poll is the operator's", env: map[string]string{"CALENDAR_WORKERS_POLL": "15m"}, want: []string{"calendar-cancellations"}, poll: 15 * time.Minute},
		{name: "turned off it is never started", env: map[string]string{"CALENDAR_CANCELLATIONS_ENABLED": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var opts []modtest.Option
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)
			var names []string
			for _, w := range module.Workers(h.Deps(), calendar.Module()) {
				names = append(names, w.Name())
				if w.Interval() != tc.poll {
					t.Errorf("worker %s polls every %v, want %v", w.Name(), w.Interval(), tc.poll)
				}
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("workers = %v, want exactly %v", names, tc.want)
			}
		})
	}
	if got := calendar.Module().Workers(module.Deps{}); got != nil {
		t.Errorf("a Deps with no Config gets workers %v", got)
	}
}
