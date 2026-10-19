package calendar

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// update rewrites testdata/invite from the fixtures:
// go test ./internal/calendar -run TestBuildICS_MatchesTheGoldens -update.
var update = flag.Bool("update", false, "rewrite testdata/invite from the fixtures")

// icsNow is the fixtures' clock, DTSTAMP's value.
var icsNow = time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func date(t *testing.T, s string) pgtype.Date {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return pgDate(d)
}

func instant(t *testing.T, zone, wall string) *time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02T15:04", wall, loc)
	if err != nil {
		t.Fatal(err)
	}
	v = v.UTC()
	return &v
}

// icsFixtures is every golden's input, by its file name.
func icsFixtures(t *testing.T) map[string]icsInput {
	t.Helper()
	guests := []icsAttendee{
		{name: "Ola Hansen", address: "ola@kunde.example", status: "accepted"},
		{name: "O'Brien, Per", address: "per@example.com", status: "needs-action"},
		{address: "anon@example.com", status: "tentative"},
	}
	base := icsInput{
		attendees: guests, link: "https://vantigo.example.com/rsvp?token=TOKEN",
		rsvpSentence: english.rsvpSentence, organiser: "Kari Nordmann", from: "calendar@vantigo.example.com", now: icsNow,
	}
	single := store.CalendarAppointment{
		ID: 1, IcalUid: "single@vantigo", Title: "Kundemøte; review, round 2", TimeZone: "Europe/Oslo",
		Description: ptr("Agenda:\nthe contract"), Location: ptr("Storgata 1, Oslo"),
		StartsAt: instant(t, "Europe/Oslo", "2026-11-02T09:00"), EndsAt: instant(t, "Europe/Oslo", "2026-11-02T10:00"),
		Visibility: visibilityShared, Transparency: transparencyBusy,
	}
	allDay := store.CalendarAppointment{
		ID: 2, IcalUid: "all-day@vantigo", Title: "Offsite", TimeZone: "Europe/Oslo", AllDay: true,
		StartDate: date(t, "2026-11-02"), EndDate: date(t, "2026-11-03"), Sequence: 2,
		Visibility: visibilityPrivate, Transparency: transparencyFree,
	}
	series := store.CalendarAppointment{
		ID: 3, IcalUid: "series@vantigo", Title: "Stand-up", TimeZone: "Europe/Oslo",
		StartsAt: instant(t, "Europe/Oslo", "2026-10-19T09:00"), EndsAt: instant(t, "Europe/Oslo", "2026-10-19T09:30"),
		Rrule: ptr("FREQ=WEEKLY;INTERVAL=1;BYDAY=MO;UNTIL=20261130"), SeriesLastDate: date(t, "2026-11-30"), Sequence: 4,
		Visibility: visibilityShared, Transparency: transparencyBusy,
	}
	seriesExcs := []store.CalendarAppointmentException{
		{AppointmentID: 3, OccurrenceDate: date(t, "2026-11-02"),
			StartsAt: instant(t, "Europe/Oslo", "2026-11-02T11:00"), EndsAt: instant(t, "Europe/Oslo", "2026-11-02T11:30")},
		{AppointmentID: 3, OccurrenceDate: date(t, "2026-10-26"), Cancelled: true},
	}
	daily := store.CalendarAppointment{
		ID: 4, IcalUid: "daily@vantigo", Title: "Course", TimeZone: "Europe/Oslo", AllDay: true,
		StartDate: date(t, "2026-11-02"), EndDate: date(t, "2026-11-02"),
		Rrule: ptr("FREQ=DAILY;INTERVAL=1;COUNT=5"), SeriesLastDate: date(t, "2026-11-06"), Sequence: 1,
		Visibility: visibilityShared, Transparency: transparencyBusy,
	}
	dailyExcs := []store.CalendarAppointmentException{
		{AppointmentID: 4, OccurrenceDate: date(t, "2026-11-03"), Cancelled: true},
		{AppointmentID: 4, OccurrenceDate: date(t, "2026-11-04"), StartDate: date(t, "2026-11-09"), EndDate: date(t, "2026-11-09")},
	}
	cancelledSingle := single
	cancelledSingle.Sequence = 1

	out := map[string]icsInput{}
	add := func(name, method string, a store.CalendarAppointment, excs []store.CalendarAppointmentException) {
		in := base
		in.method, in.appt, in.excs = method, a, excs
		out[name] = in
	}
	add("single-timed", methodRequest, single, nil)
	add("all-day", methodRequest, allDay, nil)
	add("series", methodRequest, series, seriesExcs)
	add("all-day-series", methodRequest, daily, dailyExcs)
	add("cancel", methodCancel, cancelledSingle, nil)
	return out
}

// The goldens are the fixtures built, byte for byte, CRLF and folding
// included; -update rewrites them.
func TestBuildICS_MatchesTheGoldens(t *testing.T) {
	t.Parallel()
	fixtures := icsFixtures(t)
	for name, in := range fixtures {
		got, err := buildICS(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		path := filepath.Join("testdata", "invite", name+".ics")
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to write it)", name, err)
		}
		if !bytes.Equal([]byte(got), want) {
			t.Errorf("%s: the object differs from %s (run with -update after checking the diff):\n%s", name, path, got)
		}
	}
	files, err := filepath.Glob(filepath.Join("testdata", "invite", "*.ics"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, ok := fixtures[strings.TrimSuffix(filepath.Base(f), ".ics")]; !ok {
			t.Errorf("%s has no fixture", f)
		}
	}
}

// unfoldICS joins an object's folded lines.
func unfoldICS(s string) string { return strings.ReplaceAll(s, "\r\n ", "") }

func TestBuildICS_ATimedSeriesIsWallClockInItsZoneWithAVTimezone(t *testing.T) {
	t.Parallel()
	got, err := buildICS(icsFixtures(t)["series"])
	if err != nil {
		t.Fatal(err)
	}
	ics := unfoldICS(got)
	for _, want := range []string{
		// The VTIMEZONE comes before the events and covers the series: the
		// summer time in force at its start, then the change of 25 October.
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Vantigo//Calendar//EN\r\nMETHOD:REQUEST\r\nBEGIN:VTIMEZONE\r\nTZID:Europe/Oslo\r\n",
		"BEGIN:DAYLIGHT\r\nDTSTART:20260329T020000\r\nTZOFFSETFROM:+0100\r\nTZOFFSETTO:+0200\r\nTZNAME:CEST\r\nEND:DAYLIGHT\r\n",
		"BEGIN:STANDARD\r\nDTSTART:20261025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nTZNAME:CET\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n",
		"DTSTART;TZID=Europe/Oslo:20261019T090000\r\n", "DTEND;TZID=Europe/Oslo:20261019T093000\r\n",
		// The UNTIL beside a zoned DTSTART is UTC: the last second of the
		// 30th in Oslo.
		"RRULE:FREQ=WEEKLY;INTERVAL=1;BYDAY=MO;UNTIL=20261130T225959Z\r\n",
		"EXDATE;TZID=Europe/Oslo:20261026T090000\r\n",
		"RECURRENCE-ID;TZID=Europe/Oslo:20261102T090000\r\n",
		"DTSTART;TZID=Europe/Oslo:20261102T110000\r\n",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("the series lacks %q:\n%s", want, ics)
		}
	}
	if strings.Count(ics, "BEGIN:STANDARD")+strings.Count(ics, "BEGIN:DAYLIGHT") != 2 {
		t.Errorf("the series' VTIMEZONE is not its two periods:\n%s", ics)
	}
	if strings.Contains(ics, "T090000Z") {
		t.Errorf("a series instant is written in UTC:\n%s", ics)
	}

	// A single timed appointment is UTC and needs no VTIMEZONE; an all-day
	// one is dates.
	single, _ := buildICS(icsFixtures(t)["single-timed"])
	if !strings.Contains(single, "DTSTART:20261102T080000Z\r\n") || strings.Contains(single, "VTIMEZONE") || strings.Contains(single, "TZID") {
		t.Errorf("the single appointment:\n%s", single)
	}
	daily, _ := buildICS(icsFixtures(t)["all-day-series"])
	daily = unfoldICS(daily)
	for _, want := range []string{"DTSTART;VALUE=DATE:20261102\r\n", "DTEND;VALUE=DATE:20261103\r\n", "RRULE:FREQ=DAILY;INTERVAL=1;COUNT=5\r\n",
		"EXDATE;VALUE=DATE:20261103\r\n", "RECURRENCE-ID;VALUE=DATE:20261104\r\n", "DTSTART;VALUE=DATE:20261109\r\n"} {
		if !strings.Contains(daily, want) {
			t.Errorf("the all-day series lacks %q:\n%s", want, daily)
		}
	}
	if strings.Contains(daily, "VTIMEZONE") {
		t.Errorf("an all-day series carries a VTIMEZONE:\n%s", daily)
	}
}

func TestVTimezone_CoversThreeYearsOfAnUnboundedSeriesAndAZoneWithoutTransitions(t *testing.T) {
	t.Parallel()
	in := icsFixtures(t)["series"]
	in.appt.Rrule, in.appt.SeriesLastDate, in.excs = ptr("FREQ=WEEKLY;INTERVAL=1"), pgtype.Date{}, nil
	got, err := buildICS(in)
	if err != nil {
		t.Fatal(err)
	}
	// From the summer time of 2026 to that of 2029: 2026-03-29, then six
	// changes up to 2029-03-25, before the series' third anniversary.
	if n := strings.Count(got, "BEGIN:STANDARD") + strings.Count(got, "BEGIN:DAYLIGHT"); n != 7 {
		t.Errorf("an unbounded series' VTIMEZONE has %d periods, want 7:\n%s", n, got)
	}
	if !strings.Contains(got, "DTSTART:20290325T020000\r\n") || strings.Contains(got, "DTSTART:20291028") {
		t.Errorf("the VTIMEZONE does not end at the third year:\n%s", got)
	}

	cal := ics.NewCalendar()
	cal.AddVTimezone(vtimezone(time.UTC, icsNow, icsNow.AddDate(1, 0, 0)))
	utc := cal.Serialize(ics.WithNewLineWindows)
	if !strings.Contains(utc, "TZID:UTC\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0000\r\nTZOFFSETTO:+0000\r\nTZNAME:UTC\r\nEND:STANDARD\r\nEND:VTIMEZONE") {
		t.Errorf("UTC's VTIMEZONE:\n%s", utc)
	}
	for seconds, want := range map[int]string{3600: "+0100", -18000: "-0500", 19800: "+0530", 4500: "+0115", -3661: "-010101", 0: "+0000"} {
		if got := utcOffset(seconds); got != want {
			t.Errorf("utcOffset(%d) = %q, want %q", seconds, got, want)
		}
	}
}

func TestSafeCN_WritesANameWithoutTheCharactersGolangICalWouldEscape(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Kari Nordmann":     "Kari Nordmann",
		"O'Brien, Per":      "O\u2019Brien Per",
		`Ola "Kunde"; AS:x`: "Ola Kunde AS x",
		"  ":                "",
	} {
		if got := safeCN(in); got != want {
			t.Errorf("safeCN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanText_NormalisesLineBreaksAndDropsControlsBeforeAMailOrAnObject(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"a\r\nb": "a\nb", "a\rb\x00c\x07d\te\x1b": "a\nbcd\te", "plain": "plain"} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
	in := icsFixtures(t)["single-timed"]
	in.appt.Title, in.appt.Location, in.appt.Description = "a\r\nb", ptr("Storgata\r1"), ptr("a\r\nb\x00")
	got, err := buildICS(in)
	if err != nil {
		t.Fatal(err)
	}
	ics := unfoldICS(got)
	for _, want := range []string{"SUMMARY:a b\r\n", "LOCATION:Storgata 1\r\n", "DESCRIPTION:a\\nb\\n\\nAnswer"} {
		if !strings.Contains(ics, want) {
			t.Errorf("the object lacks %q:\n%s", want, ics)
		}
	}
	if strings.Count(got, "\r") != strings.Count(got, "\r\n") || strings.Contains(got, "\x00") {
		t.Errorf("a lone CR or a control reached the object:\n%q", got)
	}
	subject, text, html := invitationTexts(english, mailInvitation, in.appt, "Kari\r\nNordmann", in.link)
	if strings.ContainsAny(subject, "\r\n") || strings.Contains(text, "\r") || strings.Contains(html, "\r") || strings.Contains(text, "\x00") ||
		!strings.HasPrefix(subject, "Invitation: a b · ") || !strings.Contains(text, "Kari Nordmann invites you") {
		t.Errorf("the mail carries what was typed raw: %q\n%q", subject, text)
	}
}
