package calendar

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // every zone below wherever the tests run

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// The parse rules of calendar feed design D8, one case each.

// vcalendar is a calendar of lines, CRLF-terminated as RFC 5545 has it.
func vcalendar(lines ...string) string {
	all := append([]string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Test//Test//EN"}, lines...)
	all = append(all, "END:VCALENDAR")
	return strings.Join(all, "\r\n") + "\r\n"
}

// vevent is one VEVENT of lines.
func vevent(lines ...string) []string {
	return append(append([]string{"BEGIN:VEVENT", "DTSTAMP:20260901T000000Z"}, lines...), "END:VEVENT")
}

func events(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// testWindow is from..to judged in zone.
func testWindow(t *testing.T, from, to, zone string) window {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	f, err := time.Parse(time.DateOnly, from)
	if err != nil {
		t.Fatal(err)
	}
	l, err := time.Parse(time.DateOnly, to)
	if err != nil {
		t.Fatal(err)
	}
	return window{from: f, to: l, loc: loc, fromAt: wallClock(f, loc), toAt: wallClock(addDays(l, 1), loc)}
}

var testSub = subscriptionSource{id: 1001, name: "Football", colour: "teal"}

func parse(t *testing.T, body string, w window) []gen.CalendarItem {
	t.Helper()
	return parseSubscriptionItems(body, testSub, w, langEnglish, nil)
}

// one is the single item of list, failing unless there is exactly one.
func one(t *testing.T, list []gen.CalendarItem) gen.CalendarItem {
	t.Helper()
	if len(list) != 1 {
		t.Fatalf("items = %+v, want one", list)
	}
	return list[0]
}

func utc(t *testing.T, v string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func wantTimed(t *testing.T, it gen.CalendarItem, start, end string) {
	t.Helper()
	if it.AllDay || it.StartsAt == nil || it.EndsAt == nil || !it.StartsAt.Equal(utc(t, start)) || !it.EndsAt.Equal(utc(t, end)) {
		t.Errorf("item %s spans %v..%v, want %s..%s", it.Id, it.StartsAt, it.EndsAt, start, end)
	}
}

func wantDays(t *testing.T, it gen.CalendarItem, first, last string) {
	t.Helper()
	if !it.AllDay || it.StartDate == nil || it.EndDate == nil || it.StartDate.String() != first || it.EndDate.String() != last {
		t.Errorf("item %s spans %v..%v, want the days %s..%s", it.Id, it.StartDate, it.EndDate, first, last)
	}
}

func TestParseSubscription_ADateEventIsAllDayItsDTENDExclusive(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo")
	got := parse(t, vcalendar(events(
		vevent("UID:two-days", "SUMMARY:Seminar", "DTSTART;VALUE=DATE:20261103", "DTEND;VALUE=DATE:20261105"),
		vevent("UID:one-day", "SUMMARY:Holiday", "DTSTART;VALUE=DATE:20261110"),
		vevent("UID:by-duration", "SUMMARY:Trip", "DTSTART;VALUE=DATE:20261120", "DURATION:P3D"),
	)...), w)
	if len(got) != 3 {
		t.Fatalf("items = %+v", got)
	}
	wantDays(t, got[0], "2026-11-03", "2026-11-04")
	wantDays(t, got[1], "2026-11-10", "2026-11-10")
	wantDays(t, got[2], "2026-11-20", "2026-11-22")
	it := got[0]
	if it.Kind != subscriptionKind || it.Id != "1001:two-days" || it.Title != "Seminar" || it.Subtitle == nil || *it.Subtitle != "Football" ||
		it.Path != nil || it.Done || it.Subscription == nil || it.Subscription.Id != 1001 || it.Subscription.Colour != gen.Teal || it.Subscription.Name != "Football" {
		t.Errorf("the item = %+v (subscription %+v)", it, it.Subscription)
	}
}

func TestParseSubscription_AUTCEventIsAnInstantAndADurationGivesItsEnd(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo")
	got := parse(t, vcalendar(events(
		vevent("UID:utc", "SUMMARY:Call", "DTSTART:20261103T080000Z", "DTEND:20261103T090000Z"),
		vevent("UID:dur", "SUMMARY:Long call", "DTSTART:20261104T080000Z", "DURATION:PT1H30M"),
		vevent("UID:point", "SUMMARY:Reminder", "DTSTART:20261105T080000Z"),
		vevent("UID:backwards", "SUMMARY:Odd", "DTSTART:20261106T080000Z", "DTEND:20261106T070000Z"),
	)...), w)
	if len(got) != 4 {
		t.Fatalf("items = %+v", got)
	}
	wantTimed(t, got[0], "2026-11-03T08:00:00Z", "2026-11-03T09:00:00Z")
	wantTimed(t, got[1], "2026-11-04T08:00:00Z", "2026-11-04T09:30:00Z")
	wantTimed(t, got[2], "2026-11-05T08:00:00Z", "2026-11-05T08:00:00Z")
	wantTimed(t, got[3], "2026-11-06T08:00:00Z", "2026-11-06T08:00:00Z")
}

func TestParseSubscription_AnOsloSeriesKeepsItsWallClockAcrossTheClockChange(t *testing.T) {
	w := testWindow(t, "2026-10-19", "2026-11-01", "Europe/Oslo")
	got := parse(t, vcalendar(vevent("UID:standup", "SUMMARY:Standup",
		"DTSTART;TZID=Europe/Oslo:20261019T090000", "DTEND;TZID=Europe/Oslo:20261019T091500", "RRULE:FREQ=WEEKLY;COUNT=3")...), w)
	if len(got) != 2 {
		t.Fatalf("items = %+v", got)
	}
	wantTimed(t, got[0], "2026-10-19T07:00:00Z", "2026-10-19T07:15:00Z") // CEST
	wantTimed(t, got[1], "2026-10-26T08:00:00Z", "2026-10-26T08:15:00Z") // CET
	if got[0].Id != "1001:standup:20261019T070000Z" || got[1].Id != "1001:standup:20261026T080000Z" {
		t.Errorf("ids = %s, %s", got[0].Id, got[1].Id)
	}
}

func TestParseSubscription_AFloatingEventIsReadInTheViewersZone(t *testing.T) {
	body := vcalendar(vevent("UID:float", "SUMMARY:Breakfast", "DTSTART:20261103T090000", "DTEND:20261103T100000")...)
	wantTimed(t, one(t, parse(t, body, testWindow(t, "2026-11-01", "2026-11-30", "Asia/Tokyo"))), "2026-11-03T00:00:00Z", "2026-11-03T01:00:00Z")
	wantTimed(t, one(t, parse(t, body, testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo"))), "2026-11-03T08:00:00Z", "2026-11-03T09:00:00Z")
}

func TestParseSubscription_TZIDResolution(t *testing.T) {
	// July, so a fixed winter offset would be an hour off and show it.
	w := testWindow(t, "2026-07-01", "2026-07-31", "Europe/Oslo")
	outlook := []string{
		"BEGIN:VTIMEZONE", "TZID:W. Europe Standard Time",
		"BEGIN:STANDARD", "DTSTART:16010101T030000", "TZOFFSETFROM:+0200", "TZOFFSETTO:+0100", "END:STANDARD",
		"BEGIN:DAYLIGHT", "DTSTART:16010101T020000", "TZOFFSETFROM:+0100", "TZOFFSETTO:+0200", "END:DAYLIGHT",
		"END:VTIMEZONE",
	}
	hinted := []string{"BEGIN:VTIMEZONE", "TZID:/custom/Eastern", "X-LIC-LOCATION:America/New_York",
		"BEGIN:STANDARD", "DTSTART:19701101T020000", "TZOFFSETFROM:-0400", "TZOFFSETTO:-0500", "END:STANDARD", "END:VTIMEZONE"}
	offsetOnly := []string{"BEGIN:VTIMEZONE", "TZID:Somewhere Standard Time",
		"BEGIN:STANDARD", "DTSTART:19700101T000000", "TZOFFSETFROM:+0300", "TZOFFSETTO:+0300", "END:STANDARD", "END:VTIMEZONE"}
	for _, tc := range []struct {
		name, tzid string
		zone       []string
		start, end string
	}{
		{name: "a Windows name is resolved to Berlin", tzid: `"W. Europe Standard Time"`, zone: outlook, start: "2026-07-07T07:00:00Z", end: "2026-07-07T08:00:00Z"},
		{name: "an unknown TZID with X-LIC-LOCATION", tzid: "/custom/Eastern", zone: hinted, start: "2026-07-07T13:00:00Z", end: "2026-07-07T14:00:00Z"},
		{name: "an unknown TZID with a STANDARD offset", tzid: "Somewhere Standard Time", zone: offsetOnly, start: "2026-07-07T06:00:00Z", end: "2026-07-07T07:00:00Z"},
		{name: "an unknown TZID with nothing is UTC", tzid: "Nowhere", start: "2026-07-07T09:00:00Z", end: "2026-07-07T10:00:00Z"},
		{name: "an IANA name", tzid: "Asia/Tokyo", start: "2026-07-07T00:00:00Z", end: "2026-07-07T01:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := vcalendar(append(tc.zone, vevent("UID:x", "SUMMARY:Meeting",
				"DTSTART;TZID="+tc.tzid+":20260707T090000", "DTEND;TZID="+tc.tzid+":20260707T100000")...)...)
			wantTimed(t, one(t, parse(t, body, w)), tc.start, tc.end)
		})
	}
}

func TestParseSubscription_AWeeklyRuleWithAnExdateAndAMovedOccurrence(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo")
	got := parse(t, vcalendar(events(
		vevent("UID:weekly", "SUMMARY:Training", "DTSTART;TZID=Europe/Oslo:20261103T090000", "DTEND;TZID=Europe/Oslo:20261103T100000",
			"RRULE:FREQ=WEEKLY;BYDAY=TU;UNTIL=20261124T235959Z", "EXDATE;TZID=Europe/Oslo:20261110T090000"),
		vevent("UID:weekly", "SUMMARY:Training (moved)", "RECURRENCE-ID;TZID=Europe/Oslo:20261117T090000",
			"DTSTART;TZID=Europe/Oslo:20261118T140000", "DTEND;TZID=Europe/Oslo:20261118T150000"),
	)...), w)
	byID := map[string]gen.CalendarItem{}
	for _, it := range got {
		byID[it.Id] = it
	}
	if len(got) != 3 || len(byID) != 3 {
		t.Fatalf("items = %v", byID)
	}
	wantTimed(t, byID["1001:weekly:20261103T080000Z"], "2026-11-03T08:00:00Z", "2026-11-03T09:00:00Z")
	moved := byID["1001:weekly:20261117T080000Z"]
	wantTimed(t, moved, "2026-11-18T13:00:00Z", "2026-11-18T14:00:00Z")
	if moved.Title != "Training (moved)" {
		t.Errorf("the moved occurrence is titled %q", moved.Title)
	}
	wantTimed(t, byID["1001:weekly:20261124T080000Z"], "2026-11-24T08:00:00Z", "2026-11-24T09:00:00Z")
}

func TestParseSubscription_AMovedOccurrenceIsShownWhereItMovedTo(t *testing.T) {
	// The master's occurrence on 3 November is outside the window; it moved
	// into it.
	w := testWindow(t, "2026-11-09", "2026-11-15", "Europe/Oslo")
	got := parse(t, vcalendar(events(
		vevent("UID:s", "SUMMARY:Series", "DTSTART;VALUE=DATE:20261027", "RRULE:FREQ=WEEKLY;COUNT=2"),
		vevent("UID:s", "SUMMARY:Series", "RECURRENCE-ID;VALUE=DATE:20261103", "DTSTART;VALUE=DATE:20261112"),
	)...), w)
	it := one(t, got)
	if it.Id != "1001:s:20261103" {
		t.Errorf("id = %s", it.Id)
	}
	wantDays(t, it, "2026-11-12", "2026-11-12")
}

func TestParseSubscription_LeavesOutACancelledEventAndACancelledOccurrence(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo")
	got := parse(t, vcalendar(events(
		vevent("UID:gone", "SUMMARY:Cancelled", "STATUS:CANCELLED", "DTSTART:20261103T080000Z"),
		vevent("UID:daily", "SUMMARY:Daily", "DTSTART;VALUE=DATE:20261102", "RRULE:FREQ=DAILY;COUNT=2"),
		vevent("UID:daily", "SUMMARY:Daily", "RECURRENCE-ID;VALUE=DATE:20261103", "DTSTART;VALUE=DATE:20261103", "STATUS:CANCELLED"),
	)...), w)
	if it := one(t, got); it.Id != "1001:daily:20261102" {
		t.Errorf("the one item left = %s", it.Id)
	}
}

func TestParseSubscription_TitlesUnescapedOnOneLineUntitledInTheLanguageBlankWhenBusyOnly(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "Europe/Oslo")
	body := vcalendar(events(
		vevent("UID:a", `SUMMARY:Lunch\, then a walk\; bring\nshoes \\ boots`, "DTSTART:20261103T080000Z"),
		vevent("UID:b", "DTSTART:20261104T080000Z"),
		vevent("UID:c", "SUMMARY:"+strings.Repeat("x", 250), "DTSTART:20261105T080000Z"),
	)...)
	got := parse(t, body, w)
	if len(got) != 3 || got[0].Title != `Lunch, then a walk; bring shoes \ boots` || got[1].Title != "(untitled)" || len(got[2].Title) != maxTitle {
		t.Errorf("titles = %q", []string{got[0].Title, got[1].Title, got[2].Title})
	}
	if nb := parseSubscriptionItems(body, testSub, w, langNorwegian, nil); nb[1].Title != "(uten tittel)" {
		t.Errorf("an untitled event in Norwegian is %q", nb[1].Title)
	}
	busy := testSub
	busy.busyOnly = true
	for _, it := range parseSubscriptionItems(body, busy, w, langEnglish, nil) {
		if it.Title != "" || !it.Subscription.BusyOnly {
			t.Errorf("a busy-only subscription's item = %q, busyOnly %v", it.Title, it.Subscription.BusyOnly)
		}
	}
}

func TestParseSubscription_TakesAtMostAThousandOccurrencesFromOneRule(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-12-31", "UTC")
	got := parse(t, vcalendar(vevent("UID:hourly", "SUMMARY:Ping", "DTSTART:20261101T000000Z", "DURATION:PT5M", "RRULE:FREQ=HOURLY")...), w)
	if len(got) != maxRuleOccurrences {
		t.Fatalf("an hourly rule over 61 days answered %d items, want %d", len(got), maxRuleOccurrences)
	}
	wantTimed(t, got[0], "2026-11-01T00:00:00Z", "2026-11-01T00:05:00Z")
}

func TestParseSubscription_ARuleThatCannotBeReadIsASingleEvent(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "UTC")
	it := one(t, parse(t, vcalendar(vevent("UID:odd", "SUMMARY:Odd", "DTSTART:20261103T080000Z", "RRULE:FREQ=SOMETIMES")...), w))
	if it.Id != "1001:odd" {
		t.Errorf("id = %s", it.Id)
	}
}

func TestParseSubscription_ABodyThatIsNotACalendarContributesNothing(t *testing.T) {
	w := testWindow(t, "2026-11-01", "2026-11-30", "UTC")
	if got := parse(t, "<html>nope</html>", w); len(got) != 0 {
		t.Errorf("HTML answered %v", got)
	}
}

func TestParseICalDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT1H": time.Hour, "P1D": 24 * time.Hour, "P2W": 14 * 24 * time.Hour, "PT1H30M": 90 * time.Minute,
		"P1DT12H": 36 * time.Hour, "-PT15M": -15 * time.Minute, "PT45S": 45 * time.Second,
	} {
		if got, ok := parseICalDuration(in); !ok || got != want {
			t.Errorf("%s = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "P", "PT", "1H", "P1H", "PT1D", "P1Y", "PTXH"} {
		if _, ok := parseICalDuration(in); ok {
			t.Errorf("%q was read", in)
		}
	}
}

func TestNextFetchAt_IsThePollOrTheFeedsIntervalCappedAtADayPlusATenth(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	poll := 30 * time.Minute
	for _, tc := range []struct {
		refresh, base time.Duration
	}{
		{0, poll}, {time.Hour, time.Hour}, {10 * time.Minute, poll}, {7 * 24 * time.Hour, 24 * time.Hour},
	} {
		for range 50 {
			wait := nextFetchAt(now, poll, tc.refresh).Sub(now)
			if wait < tc.base || wait >= tc.base+poll/10 {
				t.Errorf("refresh %v waits %v, want [%v, %v)", tc.refresh, wait, tc.base, tc.base+poll/10)
			}
		}
	}
}

func TestWindowsZones_EveryZoneLoads(t *testing.T) {
	if len(windowsZones) < 130 {
		t.Errorf("the table has %d entries", len(windowsZones))
	}
	for windows, iana := range windowsZones {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("%s → %s: %v", windows, iana, err)
		}
	}
}
