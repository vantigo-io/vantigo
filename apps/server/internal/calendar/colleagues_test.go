package calendar_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

func TestColleague_ShowsSharedTitlesBusyForPrivateAndNothingForFree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "calendar:view-colleagues")
	colleague, colleagueID := signIn(t, h)
	organiser, _ := signIn(t, h)

	shared := create(t, colleague, with(timed(t, "Board meeting", "Europe/Oslo", "2026-11-03T09:00", "2026-11-03T10:00"), "location", "Room 1"))
	private := create(t, colleague, with(timed(t, "Doctor", "Europe/Oslo", "2026-11-03T12:00", "2026-11-03T13:00"),
		"visibility", "private", "location", "Clinic"))
	create(t, colleague, with(timed(t, "Focus time", "Europe/Oslo", "2026-11-03T14:00", "2026-11-03T15:00"), "transparency", "free"))
	accepted := create(t, organiser, with(timed(t, "Accepted invite", "Europe/Oslo", "2026-11-04T09:00", "2026-11-04T10:00"),
		"attendees", userAttendees(colleagueID.String())))
	create(t, organiser, with(timed(t, "Unanswered invite", "Europe/Oslo", "2026-11-04T11:00", "2026-11-04T12:00"),
		"attendees", userAttendees(colleagueID.String())))
	declined := create(t, organiser, with(timed(t, "Declined invite", "Europe/Oslo", "2026-11-05T09:00", "2026-11-05T10:00"),
		"attendees", userAttendees(colleagueID.String())))
	tentative := create(t, organiser, with(timed(t, "Tentative invite", "Europe/Oslo", "2026-11-05T11:00", "2026-11-05T12:00"),
		"attendees", userAttendees(colleagueID.String())))
	do(t, colleague, http.MethodPost, responsePath(accepted.Id), map[string]any{"status": "accepted"}, http.StatusOK, nil)
	do(t, colleague, http.MethodPost, responsePath(declined.Id), map[string]any{"status": "declined"}, http.StatusOK, nil)
	do(t, colleague, http.MethodPost, responsePath(tentative.Id), map[string]any{"status": "tentative"}, http.StatusOK, nil)
	h.sources.customers.give(colleagueID)

	var got gen.CalendarItems
	do(t, viewer, http.MethodGet, colleaguePath(colleagueID, "2026-11-02", "2026-11-08"), nil, http.StatusOK, &got)
	if len(got.Items) != 3 {
		t.Fatalf("colleague's items = %+v, want the shared, the private and the accepted invitation — no free, unanswered, declined or tentative one", got.Items)
	}
	byID := map[int64]gen.CalendarItem{}
	for _, it := range got.Items {
		if it.Kind != "calendar.appointment" || it.Appointment == nil || it.Appointment.Transparency != gen.Busy {
			t.Errorf("item = %+v", it)
			continue
		}
		byID[it.Appointment.Id] = it
	}
	if it := byID[shared.Id]; it.Title != "Board meeting" || it.Subtitle == nil || *it.Subtitle != "Room 1" || it.Appointment.Visibility != gen.Shared {
		t.Errorf("shared = %+v", it)
	}
	if it := byID[private.Id]; it.Title != "" || it.Subtitle != nil || it.Appointment.Visibility != gen.Private {
		t.Errorf("private = %+v, want no title, no location and visibility private", it)
	}
	if it := byID[accepted.Id]; it.Title != "Accepted invite" || it.Appointment.IsOwner || it.Appointment.MyStatus != nil {
		t.Errorf("accepted invitation = %+v", it)
	}
	if len(got.Sources) != 0 || h.sources.customers.asked.Load() != 0 {
		t.Errorf("sources %+v, customers asked %d times: a colleague's calendar asks no provider", got.Sources, h.sources.customers.asked.Load())
	}
}

func TestColleague_TheCallersOwnIDAnswersTheirOwnCalendarWithPrivateTitles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, meID := signIn(t, h, "calendar:view-colleagues")
	create(t, me, with(timed(t, "Doctor", "Europe/Oslo", "2026-11-03T12:00", "2026-11-03T13:00"), "visibility", "private"))
	create(t, me, with(timed(t, "Focus", "Europe/Oslo", "2026-11-03T14:00", "2026-11-03T15:00"), "transparency", "free"))
	var got gen.CalendarItems
	do(t, me, http.MethodGet, colleaguePath(meID, "2026-11-02", "2026-11-08"), nil, http.StatusOK, &got)
	if len(got.Items) != 2 || got.Items[0].Title != "Doctor" || got.Items[1].Title != "Focus" || !got.Items[0].Appointment.IsOwner {
		t.Errorf("own calendar through the colleague door = %+v", got.Items)
	}
}

func TestColleague_IsA404ForAnInactiveOrUnknownUserAnd403WithoutTheKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "calendar:view-colleagues")
	plain, _ := signIn(t, h)
	inactive := directoryUser(t, h, "Left", false)
	active := directoryUser(t, h, "Here", true)
	for name, id := range map[string]uuid.UUID{"inactive": inactive, "unknown": uuid.New()} {
		if r := viewer.Do(http.MethodGet, colleaguePath(id, "2026-11-02", "2026-11-08"), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want a bare 404", name, r.Status, r.Body)
		}
	}
	if r := plain.Do(http.MethodGet, colleaguePath(active, "2026-11-02", "2026-11-08"), nil); r.Status != http.StatusForbidden {
		t.Errorf("without calendar:view-colleagues: status %d, want 403", r.Status)
	}
	fieldRefused(t, viewer.Do(http.MethodGet, colleaguePath(active, "2026-11-01", "2027-02-02"), nil), "to")
}
