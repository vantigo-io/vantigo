package calendar_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestPostAppointment_AllDayAndTimedAre201WithTheirZoneUIDAndSequenceZero(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h)

	day := create(t, c, allDay("Offsite", "Europe/Oslo", "2026-11-02", "2026-11-03"))
	if !day.AllDay || day.StartDate == nil || day.StartDate.String() != "2026-11-02" || day.EndDate.String() != "2026-11-03" || day.StartsAt != nil {
		t.Errorf("all-day appointment = %+v", day)
	}
	block := create(t, c, timed(t, "Review", "America/New_York", "2026-11-02T09:00", "2026-11-02T10:30"))
	if block.AllDay || block.StartsAt == nil || !block.StartsAt.Equal(at(t, "America/New_York", "2026-11-02T09:00")) || block.StartDate != nil {
		t.Errorf("timed appointment = %+v", block)
	}
	for _, a := range []gen.Appointment{day, block} {
		if a.Owner.Id != me || a.Revision != 1 || a.Visibility != gen.Shared || a.Transparency != gen.Busy || len(a.Attendees) != 0 {
			t.Errorf("appointment %d = %+v, want owned by the caller at revision 1, shared and busy", a.Id, a)
		}
		zone := modtest.One[string](t, h.Harness, `SELECT time_zone FROM calendar.appointments WHERE id = $1`, a.Id)
		uid := modtest.One[string](t, h.Harness, `SELECT ical_uid FROM calendar.appointments WHERE id = $1`, a.Id)
		seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id)
		if zone != a.TimeZone || !strings.HasSuffix(uid, "@vantigo") || seq != 0 {
			t.Errorf("appointment %d stored zone %q uid %q sequence %d", a.Id, zone, uid, seq)
		}
		if _, err := uuid.Parse(strings.TrimSuffix(uid, "@vantigo")); err != nil {
			t.Errorf("ical_uid %q is not <uuid>@vantigo", uid)
		}
	}
	if day.TimeZone != "Europe/Oslo" || block.TimeZone != "America/New_York" {
		t.Errorf("zones = %q, %q", day.TimeZone, block.TimeZone)
	}
}

func TestPostAppointment_RefusesEachShapeRuleOnItsField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h)
	inactive := directoryUser(t, h, "Gone Away", false)
	many := make([]string, 51)
	for i := range many {
		many[i] = directoryUser(t, h, fmt.Sprintf("Colleague %d", i), true).String()
	}
	base := timed(t, "Meeting", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00")
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"both pairs", with(base, "startDate", "2026-11-02", "endDate", "2026-11-02"), "startDate"},
		{"neither pair", map[string]any{"title": "x", "allDay": false, "timeZone": "Europe/Oslo"}, "startsAt"},
		{"all-day without dates", map[string]any{"title": "x", "allDay": true, "timeZone": "Europe/Oslo"}, "startDate"},
		{"timed end before start", with(base, "endsAt", at(t, "Europe/Oslo", "2026-11-02T08:00").UTC()), "endsAt"},
		{"timed end at start", with(base, "endsAt", at(t, "Europe/Oslo", "2026-11-02T09:00").UTC()), "endsAt"},
		{"all-day end before start", allDay("x", "Europe/Oslo", "2026-11-02", "2026-11-01"), "endDate"},
		{"a 32-day timed span", with(base, "endsAt", at(t, "Europe/Oslo", "2026-12-04T09:00").UTC()), "endsAt"},
		{"a 367-day all-day span", allDay("x", "Europe/Oslo", "2026-01-01", "2027-01-02"), "endDate"},
		{"a blank title", with(base, "title", "  "), "title"},
		{"a 201-character title", with(base, "title", strings.Repeat("a", 201)), "title"},
		{"a 201-character location", with(base, "location", strings.Repeat("a", 201)), "location"},
		{"a 10001-character description", with(base, "description", strings.Repeat("a", 10001)), "description"},
		{"a zone Postgres reads differently", with(base, "timeZone", "CET"), "timeZone"},
		{"a zone nobody knows", with(base, "timeZone", "Mars/Olympus"), "timeZone"},
		{"no zone", with(base, "timeZone", ""), "timeZone"},
		{"a visibility outside the vocabulary", with(base, "visibility", "secret"), "visibility"},
		{"a transparency outside the vocabulary", with(base, "transparency", "maybe"), "transparency"},
		{"a 51st attendee", with(base, "attendees", many), "attendees"},
		{"an inactive attendee", with(base, "attendees", []string{inactive.String()}), "attendees[0]"},
		{"an unknown attendee", with(base, "attendees", []string{many[0], uuid.NewString()}), "attendees[1]"},
		{"the owner as attendee", with(base, "attendees", []string{me.String()}), "attendees[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fieldRefused(t, c.Do(http.MethodPost, appointmentsPath, tc.body), tc.field)
		})
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointments`); n != 0 {
		t.Errorf("%d appointments stored after refusals", n)
	}
}

func TestPostAppointment_AttendeesAreDeduplicatedAtNeedsActionWithTheirNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	ann := directoryUser(t, h, "Ann Berg", true)
	bob := directoryUser(t, h, "Bob Dahl", true)
	a := create(t, c, with(timed(t, "Planning", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"attendees", []string{ann.String(), bob.String(), ann.String()},
		"visibility", "private", "transparency", "free", "location", " Room 4 ", "description", "Agenda"))
	if len(a.Attendees) != 2 || a.Attendees[0].User.Id != ann || a.Attendees[0].User.DisplayName != "Ann Berg" ||
		a.Attendees[1].User.DisplayName != "Bob Dahl" {
		t.Fatalf("attendees = %+v", a.Attendees)
	}
	for _, at := range a.Attendees {
		if at.Status != gen.AppointmentAttendeeStatusNeedsAction || at.RespondedAt != nil {
			t.Errorf("attendee %+v, want needs-action", at)
		}
	}
	if a.Visibility != gen.Private || a.Transparency != gen.Free || a.Location == nil || *a.Location != "Room 4" || *a.Description != "Agenda" {
		t.Errorf("appointment = %+v", a)
	}
	if a.MyStatus != nil {
		t.Errorf("the owner's myStatus = %v, want absent", *a.MyStatus)
	}
}

func TestGetAppointment_TheOwnerAndAnAttendeeReadItAndAnyoneElseGets404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	stranger, _ := signIn(t, h)
	a := create(t, owner, with(timed(t, "1:1", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:30"),
		"attendees", []string{attendeeID.String()}))

	var got gen.Appointment
	do(t, owner, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if got.Id != a.Id || got.MyStatus != nil {
		t.Errorf("owner's read = %+v", got)
	}
	do(t, attendee, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if got.MyStatus == nil || *got.MyStatus != gen.AppointmentAttendeeStatusNeedsAction {
		t.Errorf("attendee's myStatus = %v, want needs-action", got.MyStatus)
	}
	for name, r := range map[string]*modtest.Response{
		"a stranger": stranger.Do(http.MethodGet, appointmentPath(a.Id), nil),
		"unknown id": owner.Do(http.MethodGet, appointmentPath(999999), nil),
		"attendee PUT": attendee.Do(http.MethodPut, appointmentPath(a.Id),
			timed(t, "Mine now", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:30"), ifMatch(a.Revision)),
		"attendee DELETE": attendee.Do(http.MethodDelete, appointmentPath(a.Id), nil),
	} {
		if r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want a bare 404", name, r.Status, r.Body)
		}
	}
}

func TestPutAppointment_MovingTheTimeResetsTheAnswersAndBumpsTheSequence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Standup", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:15"), "attendees", []string{attendeeID.String()})
	a := create(t, owner, body)
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, nil)

	var moved gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id),
		with(body, "startsAt", at(t, "Europe/Oslo", "2026-11-02T10:00").UTC(), "endsAt", at(t, "Europe/Oslo", "2026-11-02T10:15").UTC()),
		http.StatusOK, &moved, ifMatch(a.Revision+1))
	if moved.Attendees[0].Status != gen.AppointmentAttendeeStatusNeedsAction || moved.Attendees[0].RespondedAt != nil {
		t.Errorf("after the move the attendee is %+v, want needs-action", moved.Attendees[0])
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 1 {
		t.Errorf("sequence = %d, want 1", seq)
	}
	if moved.Revision != a.Revision+2 {
		t.Errorf("revision = %d, want %d", moved.Revision, a.Revision+2)
	}
}

func TestPutAppointment_ChangingTheTitleNeitherResetsTheAnswersNorBumpsTheSequence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Standup", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:15"), "attendees", []string{attendeeID.String()})
	a := create(t, owner, body)
	var answered gen.Appointment
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "tentative"}, http.StatusOK, &answered)

	var renamed gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Daily standup"), http.StatusOK, &renamed,
		modtest.Header("If-Match", fmt.Sprintf("%q", fmt.Sprint(answered.Revision))))
	if renamed.Title != "Daily standup" || renamed.Attendees[0].Status != gen.AppointmentAttendeeStatusTentative {
		t.Errorf("after the rename = %+v", renamed)
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 0 {
		t.Errorf("sequence = %d, want 0", seq)
	}

	// Where it is bumps the sequence too, and leaves the answers alone.
	var relocated gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Daily standup", "location", "Room 2"),
		http.StatusOK, &relocated, ifMatch(renamed.Revision))
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 1 {
		t.Errorf("sequence after the location changed = %d, want 1", seq)
	}
	if relocated.Attendees[0].Status != gen.AppointmentAttendeeStatusTentative {
		t.Errorf("after the location changed the attendee is %s", relocated.Attendees[0].Status)
	}
}

func TestPutAppointment_AStaleIfMatchIs409AndAMalformedOneIs400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	body := allDay("Holiday", "Europe/Oslo", "2026-12-24", "2026-12-26")
	a := create(t, c, body)
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Christmas"), http.StatusOK, nil, ifMatch(a.Revision))

	refused(t, c.Do(http.MethodPut, appointmentPath(a.Id), with(body, "title", "Jul"), ifMatch(a.Revision)), http.StatusConflict, "revision_mismatch")
	fieldRefused(t, c.Do(http.MethodPut, appointmentPath(a.Id), body, modtest.Header("If-Match", "latest")), "If-Match")
	if title := modtest.One[string](t, h.Harness, `SELECT title FROM calendar.appointments WHERE id = $1`, a.Id); title != "Christmas" {
		t.Errorf("title = %q", title)
	}
}

func TestPutAppointment_ReplacesTheAttendeeList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	ann := directoryUser(t, h, "Ann", true)
	bob := directoryUser(t, h, "Bob", true)
	body := timed(t, "Sync", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:30")
	a := create(t, c, with(body, "attendees", []string{ann.String()}))
	var got gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "attendees", []string{bob.String()}), http.StatusOK, &got, ifMatch(a.Revision))
	if len(got.Attendees) != 1 || got.Attendees[0].User.Id != bob {
		t.Errorf("attendees = %+v, want Bob alone", got.Attendees)
	}
}

func TestDeleteAppointment_RemovesItWithItsAttendeesAndExceptions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	ann := directoryUser(t, h, "Ann", true)
	a := create(t, c, with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"attendees", []string{ann.String()}, "recurrence", map[string]any{"rule": "FREQ=WEEKLY"}))
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-09"), map[string]any{"cancelled": true}, http.StatusOK, nil)

	do(t, c, http.MethodDelete, appointmentPath(a.Id), nil, http.StatusNoContent, nil)
	for _, table := range []string{"appointments", "appointment_attendees", "appointment_exceptions"} {
		if n := h.Count(t, `SELECT count(*) FROM calendar.`+table); n != 0 {
			t.Errorf("%d rows left in %s", n, table)
		}
	}
	if r := c.Do(http.MethodDelete, appointmentPath(a.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("second delete: status %d, want 404", r.Status)
	}
}

func TestResponse_FromANonAttendeeOrTheOwnerIs404AndFromAnAttendeeLandsInMyStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	stranger, _ := signIn(t, h)
	a := create(t, owner, with(timed(t, "Demo", "Europe/Oslo", "2026-11-02T13:00", "2026-11-02T14:00"),
		"attendees", []string{attendeeID.String()}))

	for name, c := range map[string]*modtest.Client{"a stranger": stranger, "the owner": owner} {
		if r := c.Do(http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want a bare 404", name, r.Status, r.Body)
		}
	}
	fieldRefused(t, attendee.Do(http.MethodPost, responsePath(a.Id), map[string]any{"status": "needs-action"},
		modtest.SkipContract("needs-action is not in the request enum: the server's own refusal is the subject")), "status")

	var got gen.Appointment
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "declined"}, http.StatusOK, &got)
	if got.MyStatus == nil || *got.MyStatus != gen.AppointmentAttendeeStatusDeclined || got.Attendees[0].RespondedAt == nil {
		t.Errorf("after declining = %+v", got)
	}
	// A declined attendee still sees it, and may change their mind.
	list := items(t, attendee, windowPath("2026-11-02", "2026-11-02"))
	appts := ofKind(list, "calendar.appointment")
	if len(appts) != 1 || appts[0].Appointment.MyStatus == nil || *appts[0].Appointment.MyStatus != gen.AppointmentAttendeeStatusDeclined || appts[0].Appointment.IsOwner {
		t.Errorf("the declined attendee's calendar = %+v", appts)
	}
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, &got)
	if *got.MyStatus != gen.AppointmentAttendeeStatusAccepted {
		t.Errorf("after accepting myStatus = %s", *got.MyStatus)
	}
}

func TestAppointment_NamesTheCustomerAndProjectWhenTheirModulesAreWired(t *testing.T) {
	t.Parallel()
	h := newHarness(t,
		modtest.WithDirectory(fakeDirectory{customers: map[int32]string{42: "Fjord AS"}}),
		modtest.WithProjects(fakeProjects{projects: map[int32][2]string{7: {"P-7", "Website"}}}))
	c, _ := signIn(t, h)
	a := create(t, c, with(timed(t, "Kickoff", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "customerId", 42, "projectId", 7))
	if a.Customer == nil || a.Customer.Id != 42 || a.Customer.Name == nil || *a.Customer.Name != "Fjord AS" {
		t.Errorf("customer = %+v", a.Customer)
	}
	if a.Project == nil || a.Project.Code == nil || *a.Project.Code != "P-7" || *a.Project.Name != "Website" {
		t.Errorf("project = %+v", a.Project)
	}
	// An id the directory cannot resolve is kept, unnamed — never refused.
	b := create(t, c, with(timed(t, "Call", "Europe/Oslo", "2026-11-02T11:00", "2026-11-02T12:00"), "customerId", 43))
	if b.Customer == nil || b.Customer.Id != 43 || b.Customer.Name != nil {
		t.Errorf("unresolvable customer = %+v", b.Customer)
	}
}

func TestAppointment_KeepsTheLinksUnnamedWhenNoModuleIsWired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	a := create(t, c, with(timed(t, "Kickoff", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "customerId", 42, "projectId", 7))
	if a.Customer == nil || a.Customer.Id != 42 || a.Customer.Name != nil || a.Project == nil || a.Project.Id != 7 || a.Project.Code != nil {
		t.Errorf("links = %+v, %+v", a.Customer, a.Project)
	}
}

func TestEveryOperationIs403WithoutCalendarAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	a := create(t, owner, timed(t, "x", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"))
	c, _ := h.SignInUser(t, "calendar:view-colleagues")
	body := timed(t, "x", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00")
	for _, req := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, metaPath, nil},
		{http.MethodGet, windowPath("2026-11-02", "2026-11-08"), nil},
		{http.MethodGet, usersPath, nil},
		{http.MethodPost, appointmentsPath, body},
		{http.MethodGet, appointmentPath(a.Id), nil},
		{http.MethodPut, appointmentPath(a.Id), body},
		{http.MethodDelete, appointmentPath(a.Id), nil},
		{http.MethodPut, occurrencePath(a.Id, "2026-11-02"), map[string]any{"cancelled": true}},
		{http.MethodDelete, occurrencePath(a.Id, "2026-11-02"), nil},
		{http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}},
		{http.MethodGet, colleaguePath(uuid.New(), "2026-11-02", "2026-11-08"), nil},
	} {
		opts := []modtest.RequestOption{}
		if req.method == http.MethodPut && req.path == appointmentPath(a.Id) {
			opts = append(opts, ifMatch(1))
		}
		if r := c.Do(req.method, req.path, req.body, opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s %s: status %d body %s, want 403", req.method, req.path, r.Status, r.Body)
		}
	}
}
