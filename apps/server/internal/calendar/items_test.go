package calendar_test

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

func TestItems_AnswersOwnedAndAttendedAppointmentsSortedAllDayFirstWithinADay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, meID := signIn(t, h)
	colleague, _ := signIn(t, h)
	stranger, _ := signIn(t, h)

	late := create(t, me, timed(t, "Late", "Europe/Oslo", "2026-11-03T15:00", "2026-11-03T16:00"))
	early := create(t, me, timed(t, "Early", "Europe/Oslo", "2026-11-03T08:00", "2026-11-03T09:00"))
	whole := create(t, me, allDay("Conference", "Europe/Oslo", "2026-11-03", "2026-11-04"))
	invited := create(t, colleague, with(timed(t, "Invited", "Europe/Oslo", "2026-11-02T12:00", "2026-11-02T13:00"),
		"attendees", []string{meID.String()}))
	create(t, stranger, timed(t, "Not mine", "Europe/Oslo", "2026-11-03T10:00", "2026-11-03T11:00"))
	create(t, me, timed(t, "Outside", "Europe/Oslo", "2026-11-10T10:00", "2026-11-10T11:00"))

	got := items(t, me, windowPath("2026-11-02", "2026-11-08", "timeZone=Europe/Oslo"))
	appts := ofKind(got, "calendar.appointment")
	want := []int64{invited.Id, whole.Id, early.Id, late.Id}
	if len(appts) != len(want) {
		t.Fatalf("appointments = %v", ids(appts))
	}
	for i, id := range want {
		if appts[i].Appointment == nil || appts[i].Appointment.Id != id {
			t.Errorf("item %d = %s, want appointment %d", i, appts[i].Id, id)
		}
	}
	if appts[0].Appointment.IsOwner || appts[0].Appointment.MyStatus == nil || *appts[0].Appointment.MyStatus != gen.AppointmentAttendeeStatusNeedsAction {
		t.Errorf("the invitation = %+v", appts[0].Appointment)
	}
	if !appts[1].AllDay || appts[1].StartDate.String() != "2026-11-03" || appts[1].EndDate.String() != "2026-11-04" || appts[1].Id != itemIDSingle(whole.Id) {
		t.Errorf("the all-day item = %+v", appts[1])
	}
	if appts[2].Path != nil || appts[2].Done || appts[2].Appointment.Recurring || appts[2].Appointment.OccurrenceDate != nil {
		t.Errorf("a single appointment's item = %+v", appts[2])
	}
	if got.From.String() != "2026-11-02" || got.To.String() != "2026-11-08" || got.TimeZone != "Europe/Oslo" {
		t.Errorf("window = %s..%s in %s", got.From, got.To, got.TimeZone)
	}
}

func TestItems_RefusesA94DayWindowAndAnswersA93DayOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	items(t, c, windowPath("2026-11-01", "2027-02-01")) // 93 days, both counted
	fieldRefused(t, c.Do(http.MethodGet, windowPath("2026-11-01", "2027-02-02"), nil), "to")
	fieldRefused(t, c.Do(http.MethodGet, windowPath("2026-11-02", "2026-11-01"), nil), "to")
	fieldRefused(t, c.Do(http.MethodGet, windowPath("2026-11-01", "2026-11-02", "timeZone=CET"), nil), "timeZone")
	fieldRefused(t, c.Do(http.MethodGet, windowPath("2026-11-01", "2026-11-02", "timeZone=Nowhere/Land"), nil), "timeZone")
}

func TestItems_A2330OsloAppointmentIsOnTheOsloDayInOsloAndOnTheNextDayInTokyo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	create(t, c, timed(t, "Night call", "Europe/Oslo", "2026-11-03T23:30", "2026-11-04T00:00"))

	inOslo := func(path string) int { return len(ofKind(items(t, c, path), "calendar.appointment")) }
	if n := inOslo(windowPath("2026-11-03", "2026-11-03", "timeZone=Europe/Oslo")); n != 1 {
		t.Errorf("Oslo viewer on 3 November: %d items, want 1", n)
	}
	if n := inOslo(windowPath("2026-11-03", "2026-11-03", "timeZone=Asia/Tokyo")); n != 0 {
		t.Errorf("Tokyo viewer on 3 November: %d items, want 0", n)
	}
	if n := inOslo(windowPath("2026-11-04", "2026-11-04", "timeZone=Asia/Tokyo")); n != 1 {
		t.Errorf("Tokyo viewer on 4 November: %d items, want 1", n)
	}

	// Without timeZone the window is judged in the business zone.
	h.SetBusinessTimeZone(t, "Asia/Tokyo")
	got := items(t, c, windowPath("2026-11-04", "2026-11-04"))
	if got.TimeZone != "Asia/Tokyo" || len(ofKind(got, "calendar.appointment")) != 1 {
		t.Errorf("default zone %q with %d items, want Asia/Tokyo with 1", got.TimeZone, len(got.Items))
	}
	if n := len(ofKind(items(t, c, windowPath("2026-11-03", "2026-11-03")), "calendar.appointment")); n != 0 {
		t.Errorf("business zone Tokyo on 3 November: %d items, want 0", n)
	}
}

func TestItems_EachProviderAnswersTheCallersRowsWithItsGateOnAndNothingWithItOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	all := []string{"customers:view", "staff:access", "projects:access", "time:access", "expenses:access"}
	gated, gatedID := signIn(t, h, all...)
	bare, bareID := signIn(t, h)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	give := func(f *fakeSource, kind, rowID string, allDay bool) {
		item := contracts.CalendarItem{Kind: kind, ID: rowID, Title: kind + " " + rowID, Subtitle: "sub", AllDay: allDay, Path: "/" + kind}
		if allDay {
			item.StartDate, item.EndDate = day, day
		} else {
			item.StartsAt, item.EndsAt = start, start.Add(time.Hour)
		}
		f.give(gatedID, item)
		f.give(bareID, item)
	}
	give(h.sources.customers, "customers.follow-up", "11", true)
	give(h.sources.staff, "staff.absence", "12", true)
	give(h.sources.projects, "projects.task", "13", true)
	give(h.sources.time, "time.entry", "14", false)
	give(h.sources.expenses, "expenses.claim", "15", false)

	got := items(t, gated, windowPath("2026-11-02", "2026-11-08", "timeZone=Europe/Oslo"))
	if len(got.Items) != 5 {
		t.Fatalf("gated caller's items = %v, want the five providers' rows", ids(got.Items))
	}
	wantOrder := []string{"customers.follow-up", "staff.absence", "projects.task", "time.entry", "expenses.claim"}
	for i, kind := range wantOrder {
		it := got.Items[i]
		if it.Kind != kind || it.Path == nil || *it.Path != "/"+kind || it.Subtitle == nil || *it.Subtitle != "sub" || it.Appointment != nil {
			t.Errorf("item %d = %+v, want %s", i, it, kind)
		}
	}
	if len(got.Sources) != 6 || got.Sources[0].Kind != "customers.follow-up" || got.Sources[1].Kind != "customers.interaction" || got.Sources[5].Module != "expenses" {
		t.Errorf("sources = %+v", got.Sources)
	}
	if n := len(items(t, bare, windowPath("2026-11-02", "2026-11-08")).Items); n != 0 {
		t.Errorf("a caller without the gates got %d items", n)
	}
	// Each provider was asked about the caller, in the window, in the viewer's zone.
	q, ok := h.sources.time.lastQuery()
	if !ok || q.UserID != bareID || !q.From.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) ||
		!q.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) || q.Zone == nil || q.Zone.String() != "Europe/Oslo" {
		t.Errorf("the provider's query = %+v", q)
	}
}

func TestItems_AProviderThatFailsMakesTheRequest503NamingNoModule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:access")
	h.sources.projects.failWith(errors.New("projects: the database is on fire"))
	r := c.Do(http.MethodGet, windowPath("2026-11-02", "2026-11-08"), nil)
	p := refused(t, r, http.StatusServiceUnavailable, "")
	if strings.Contains(string(r.Body), "projects") || p.Title == "" {
		t.Errorf("the 503 = %s", r.Body)
	}
	if !strings.Contains(h.Logs(), "projects") {
		t.Error("the log does not say which source failed")
	}
}

func TestItems_KindsNarrowsWhichProvidersAreAsked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, id := signIn(t, h, "customers:view", "projects:access")
	create(t, c, timed(t, "Mine", "Europe/Oslo", "2026-11-03T09:00", "2026-11-03T10:00"))
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	h.sources.customers.give(id,
		contracts.CalendarItem{Kind: "customers.follow-up", ID: "1", Title: "Call back", AllDay: true, StartDate: day, EndDate: day},
		contracts.CalendarItem{Kind: "customers.interaction", ID: "2", Title: "Met", AllDay: true, StartDate: day, EndDate: day})
	h.sources.projects.give(id, contracts.CalendarItem{Kind: "projects.task", ID: "3", Title: "Ship", AllDay: true, StartDate: day, EndDate: day})

	got := items(t, c, windowPath("2026-11-02", "2026-11-08", "kinds=customers.follow-up,unknown.kind"))
	if len(got.Items) != 1 || got.Items[0].Kind != "customers.follow-up" {
		t.Errorf("narrowed items = %+v", got.Items)
	}
	for _, f := range []*fakeSource{h.sources.projects, h.sources.staff, h.sources.time, h.sources.expenses} {
		if n := f.asked.Load(); n != 0 {
			t.Errorf("%s was asked %d times", f.kinds[0].Module, n)
		}
	}
	if n := h.sources.customers.asked.Load(); n != 1 {
		t.Errorf("customers asked %d times, want 1", n)
	}
	if len(got.Sources) != 6 {
		t.Errorf("sources narrowed too: %+v", got.Sources)
	}

	only := items(t, c, windowPath("2026-11-02", "2026-11-08", "kinds=calendar.appointment"))
	if len(only.Items) != 1 || only.Items[0].Kind != "calendar.appointment" || h.sources.customers.asked.Load() != 1 {
		t.Errorf("appointments only = %+v", only.Items)
	}
}

func TestItems_AnswersNorwaysPublicHolidaysWithTheirNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	got := items(t, c, windowPath("2026-12-20", "2027-01-05"))
	var names []string
	for _, d := range got.PublicHolidays {
		names = append(names, d.Date.String()+" "+d.Name)
	}
	want := "2026-12-25 Første juledag|2026-12-26 Andre juledag|2027-01-01 Nyttårsdag"
	if strings.Join(names, "|") != want {
		t.Errorf("holidays = %v, want %s", names, want)
	}
	easter := items(t, c, windowPath("2027-03-20", "2027-06-01"))
	var spring []string
	for _, d := range easter.PublicHolidays {
		spring = append(spring, d.Date.String()+" "+d.Name)
	}
	wantSpring := "2027-03-25 Skjærtorsdag|2027-03-26 Langfredag|2027-03-28 Første påskedag|2027-03-29 Andre påskedag|" +
		"2027-05-01 Arbeidernes dag|2027-05-06 Kristi himmelfartsdag|2027-05-16 Første pinsedag|2027-05-17 Grunnlovsdag og andre pinsedag"
	if strings.Join(spring, "|") != wantSpring {
		t.Errorf("spring holidays = %v,\nwant %s", spring, wantSpring)
	}
}

// itemIDSingle is the id GET /items gives a single appointment.
func itemIDSingle(id int64) string { return strconv.FormatInt(id, 10) }
