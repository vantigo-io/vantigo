package calendar_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		if a.Owner.Id != me || a.Revision != 1 || a.Visibility != gen.Shared || a.Transparency != gen.AppointmentTransparencyBusy || len(a.Attendees) != 0 {
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
		{"a 51st attendee", with(base, "attendees", userAttendees(many...)), "attendees"},
		{"an inactive attendee", with(base, "attendees", userAttendees(inactive.String())), "attendees[0]"},
		{"an unknown attendee", with(base, "attendees", userAttendees(many[0], uuid.NewString())), "attendees[1]"},
		{"the owner as attendee", with(base, "attendees", userAttendees(me.String())), "attendees[0]"},
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
		"attendees", userAttendees(ann.String(), bob.String(), ann.String()),
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
	if a.Visibility != gen.Private || a.Transparency != gen.AppointmentTransparencyFree || a.Location == nil || *a.Location != "Room 4" || *a.Description != "Agenda" {
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
		"attendees", userAttendees(attendeeID.String())))

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
	body := with(timed(t, "Standup", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:15"), "attendees", userAttendees(attendeeID.String()))
	a := create(t, owner, body)
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, nil)

	var moved gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id),
		with(body, "startsAt", at(t, "Europe/Oslo", "2026-11-02T10:00").UTC(), "endsAt", at(t, "Europe/Oslo", "2026-11-02T10:15").UTC()),
		http.StatusOK, &moved, ifMatch(a.Revision))
	if moved.Attendees[0].Status != gen.AppointmentAttendeeStatusNeedsAction || moved.Attendees[0].RespondedAt != nil {
		t.Errorf("after the move the attendee is %+v, want needs-action", moved.Attendees[0])
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 1 {
		t.Errorf("sequence = %d, want 1", seq)
	}
	if moved.Revision != a.Revision+1 {
		t.Errorf("revision = %d, want %d", moved.Revision, a.Revision+1)
	}
}

func TestPutAppointment_ChangingTheTitleNeitherResetsTheAnswersNorBumpsTheSequence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Standup", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:15"), "attendees", userAttendees(attendeeID.String()))
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
	a := create(t, c, with(body, "attendees", userAttendees(ann.String())))
	var got gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "attendees", userAttendees(bob.String())), http.StatusOK, &got, ifMatch(a.Revision))
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
		"attendees", userAttendees(ann.String()), "recurrence", map[string]any{"rule": "FREQ=WEEKLY"}))
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
		"attendees", userAttendees(attendeeID.String())))

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
	c, _ := signIn(t, h, "customers:view", "projects:view-all")
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

// A directory that fails fails the POST and the PUT before anything is
// written: the links are named between the body's validation and the
// transaction.
func TestAppointment_ADirectoryThatFailsWritesNothing(t *testing.T) {
	t.Parallel()
	var down atomic.Bool
	h := newHarness(t, modtest.WithDirectory(failingDirectory{fail: &down}))
	c, _ := signIn(t, h, "customers:view")
	body := with(timed(t, "Kickoff", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "customerId", 42)
	a := create(t, c, body)

	down.Store(true)
	before := h.Count(t, `SELECT count(*) FROM calendar.appointments`)
	down500 := modtest.SkipContract("a 500 is documented nowhere: the failing directory is the subject")
	if r := c.Do(http.MethodPost, appointmentsPath, body, down500); r.Status != http.StatusInternalServerError {
		t.Errorf("POST with the directory down: %d %s, want 500", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointments`); n != before {
		t.Errorf("%d appointments after the failed POST, want %d", n, before)
	}
	if r := c.Do(http.MethodPut, appointmentPath(a.Id), with(body, "title", "Moved"), ifMatch(a.Revision), down500); r.Status != http.StatusInternalServerError {
		t.Errorf("PUT with the directory down: %d %s, want 500", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointments WHERE id = $1 AND title = 'Kickoff' AND revision = $2`, a.Id, a.Revision); n != 1 {
		t.Error("the failed PUT wrote the appointment")
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

func TestAppointment_NamesTheLinksOnlyToAViewerWhoMaySeeThem(t *testing.T) {
	t.Parallel()
	projects := fakeProjects{projects: map[int32][2]string{7: {"P-7", "Website"}, 8: {"P-8", "Secret"}}, roles: map[int32]map[uuid.UUID]string{}}
	h := newHarness(t,
		modtest.WithDirectory(fakeDirectory{customers: map[int32]string{42: "Fjord AS"}}),
		modtest.WithProjects(projects))
	owner, _ := signIn(t, h, "customers:view", "projects:manage-all")
	plain, plainID := signIn(t, h)
	member, memberID := signIn(t, h)
	projects.roles[7] = map[uuid.UUID]string{memberID: "member"}

	// Probing ids through a create of one's own names nothing.
	probe := create(t, plain, with(timed(t, "Probe", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "customerId", 42, "projectId", 8))
	if probe.Customer == nil || probe.Customer.Name != nil || probe.Project == nil || probe.Project.Code != nil || probe.Project.Name != nil {
		t.Errorf("a caller without the keys named %+v, %+v", probe.Customer, probe.Project)
	}

	a := create(t, owner, with(timed(t, "Kickoff", "Europe/Oslo", "2026-11-02T11:00", "2026-11-02T12:00"),
		"customerId", 42, "projectId", 7, "attendees", userAttendees(plainID.String(), memberID.String())))
	if a.Customer.Name == nil || a.Project.Code == nil {
		t.Errorf("the owner with the keys sees %+v, %+v", a.Customer, a.Project)
	}
	var got gen.Appointment
	do(t, plain, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if got.Customer == nil || got.Customer.Id != 42 || got.Customer.Name != nil || got.Project == nil || got.Project.Id != 7 || got.Project.Code != nil {
		t.Errorf("an attendee without customers:view or a role sees %+v, %+v", got.Customer, got.Project)
	}
	do(t, member, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if got.Customer.Name != nil || got.Project.Code == nil || *got.Project.Code != "P-7" || *got.Project.Name != "Website" {
		t.Errorf("an attendee with a role on the project sees %+v, %+v", got.Customer, got.Project)
	}
	do(t, member, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, &got)
	if got.Customer.Name != nil || got.Project.Code == nil {
		t.Errorf("the answer's response names %+v, %+v", got.Customer, got.Project)
	}
}

func TestPutAppointment_ExtendingASeriesEndKeepsItsExceptions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY"}, "attendees", userAttendees(attendeeID.String()))
	a := create(t, owner, body)
	do(t, owner, http.MethodPut, occurrencePath(a.Id, "2026-11-09"), map[string]any{"cancelled": true}, http.StatusOK, nil)
	var answered gen.Appointment
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, &answered)
	seqBefore := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id)

	var longer gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body, "endsAt", at(t, "Europe/Oslo", "2026-11-02T11:00").UTC()),
		http.StatusOK, &longer, ifMatch(answered.Revision))
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointment_exceptions WHERE appointment_id = $1 AND cancelled`, a.Id); n != 1 {
		t.Errorf("cancelled exceptions after the end moved = %d, want 1", n)
	}
	if got := ids(ofKind(items(t, owner, windowPath("2026-11-09", "2026-11-09")), "calendar.appointment")); len(got) != 0 {
		t.Errorf("the cancelled occurrence came back: %v", got)
	}
	if longer.Attendees[0].Status != gen.AppointmentAttendeeStatusNeedsAction {
		t.Errorf("a change of the end left the answer at %s, want needs-action", longer.Attendees[0].Status)
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != seqBefore+1 {
		t.Errorf("sequence = %d, want %d", seq, seqBefore+1)
	}

	// Moving the start drops them.
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body,
		"startsAt", at(t, "Europe/Oslo", "2026-11-02T08:00").UTC(), "endsAt", at(t, "Europe/Oslo", "2026-11-02T11:00").UTC()),
		http.StatusOK, nil, ifMatch(longer.Revision))
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointment_exceptions WHERE appointment_id = $1`, a.Id); n != 0 {
		t.Errorf("exceptions after the start moved = %d, want 0", n)
	}
}

func TestPutAppointment_AZoneOnlyChangeOfASingleTimedAppointmentIsNoChangeOfWhen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Call", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "attendees", userAttendees(attendeeID.String()))
	a := create(t, owner, body)
	var answered gen.Appointment
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, &answered)
	var got gen.Appointment
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body, "timeZone", "America/New_York"), http.StatusOK, &got, ifMatch(answered.Revision))
	if got.TimeZone != "America/New_York" || got.Attendees[0].Status != gen.AppointmentAttendeeStatusAccepted {
		t.Errorf("after a zone-only change = %+v", got)
	}
	if seq := h.Count(t, `SELECT sequence FROM calendar.appointments WHERE id = $1`, a.Id); seq != 0 {
		t.Errorf("sequence = %d, want 0", seq)
	}
}

func TestPostAppointment_CountsTheFiftyAttendeeLimitAfterDeduplication(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	list := make([]string, 0, 51)
	for i := 0; i < 50; i++ {
		list = append(list, directoryUser(t, h, fmt.Sprintf("Colleague %d", i), true).String())
	}
	list = append(list, list[0])
	a := create(t, c, with(timed(t, "All hands", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "attendees", userAttendees(list...)))
	if len(a.Attendees) != 50 {
		t.Errorf("attendees = %d, want 50", len(a.Attendees))
	}
}

func TestPutAppointment_KeepsAnAttendeeWhoHasSinceBecomeInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	ann := directoryUser(t, h, "Ann", true)
	bob := directoryUser(t, h, "Bob", false)
	body := with(timed(t, "Sync", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T09:30"), "attendees", userAttendees(ann.String()))
	a := create(t, c, body)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, ann)

	var got gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Sync, still"), http.StatusOK, &got, ifMatch(a.Revision))
	if len(got.Attendees) != 1 || got.Attendees[0].User.Id != ann || got.Attendees[0].User.DisplayName != "Ann" {
		t.Errorf("attendees = %+v, want Ann kept", got.Attendees)
	}
	// A newly added inactive user is still refused.
	fieldRefused(t, c.Do(http.MethodPut, appointmentPath(a.Id), with(body, "attendees", userAttendees(ann.String(), bob.String())),
		ifMatch(got.Revision)), "attendees[1]")
}

func TestResponse_AnAnswerLeavesTheRevisionSoTheOwnersIfMatchStillHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	attendee, attendeeID := signIn(t, h)
	body := with(timed(t, "Review", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"), "attendees", userAttendees(attendeeID.String()))
	a := create(t, owner, body)
	var answered gen.Appointment
	do(t, attendee, http.MethodPost, responsePath(a.Id), map[string]any{"status": "accepted"}, http.StatusOK, &answered)
	if answered.Revision != a.Revision || !answered.UpdatedAt.Equal(a.UpdatedAt) {
		t.Errorf("after an answer revision %d updatedAt %s, want %d %s", answered.Revision, answered.UpdatedAt, a.Revision, a.UpdatedAt)
	}
	do(t, owner, http.MethodPut, appointmentPath(a.Id), with(body, "title", "Design review"), http.StatusOK, nil, ifMatch(a.Revision))
}

func TestAppointment_AnswersItsExceptionsInDateOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	single := create(t, c, timed(t, "Once", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"))
	if single.Exceptions == nil || len(single.Exceptions) != 0 {
		t.Errorf("a single appointment's exceptions = %#v, want an empty list", single.Exceptions)
	}
	a := create(t, c, with(timed(t, "Weekly", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY"}))
	moved := at(t, "Europe/Oslo", "2026-11-17T12:00")
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-16"),
		map[string]any{"startsAt": moved.UTC(), "endsAt": moved.Add(time.Hour).UTC()}, http.StatusOK, nil)
	do(t, c, http.MethodPut, occurrencePath(a.Id, "2026-11-09"), map[string]any{"cancelled": true}, http.StatusOK, nil)

	var got gen.Appointment
	do(t, c, http.MethodGet, appointmentPath(a.Id), nil, http.StatusOK, &got)
	if len(got.Exceptions) != 2 || got.Exceptions[0].OccurrenceDate.String() != "2026-11-09" || !got.Exceptions[0].Cancelled ||
		got.Exceptions[1].OccurrenceDate.String() != "2026-11-16" || got.Exceptions[1].Cancelled ||
		got.Exceptions[1].StartsAt == nil || !got.Exceptions[1].StartsAt.Equal(moved) {
		t.Errorf("exceptions = %+v", got.Exceptions)
	}
	var renamed gen.Appointment
	do(t, c, http.MethodPut, appointmentPath(a.Id), with(timed(t, "Weekly sync", "Europe/Oslo", "2026-11-02T09:00", "2026-11-02T10:00"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY"}), http.StatusOK, &renamed, ifMatch(got.Revision))
	if len(renamed.Exceptions) != 2 {
		t.Errorf("the PUT's response carries %d exceptions, want 2", len(renamed.Exceptions))
	}
}
