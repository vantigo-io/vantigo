package calendar_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// updatingGoldens reports whether the run rewrites the goldens: the package's
// -update flag, which ics_internal_test.go declares for the invitations' and
// which the feed's share — one test binary holds both, and a second flag of
// the name would panic: go test ./internal/calendar -run TestFeed -update.
func updatingGoldens() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// matchesGolden fails the test unless got is testdata/feed/<name>.ics byte
// for byte; -update rewrites it.
func matchesGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "feed", name+".ics")
	if updatingGoldens() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update to write it)", name, err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("%s: the feed differs from %s (run with -update after checking the diff):\n%s", name, path, got)
	}
}

// feedOf is the anonymous GET of address, failing unless it answered want.
func feedOf(t *testing.T, h *harness, address string, want int, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	r := h.Client(t).Do(http.MethodGet, pathOf(t, address), nil, opts...)
	if r.Status != want {
		t.Fatalf("GET %s: status %d body %s, want %d", address, r.Status, r.Body, want)
	}
	return r
}

// feedText is the feed of address, unfolded, failing unless 200.
func feedText(t *testing.T, h *harness, address string) string {
	t.Helper()
	return unfold(string(feedOf(t, h, address, http.StatusOK).Body))
}

// setUID gives appointment id a stable ical_uid, for a golden.
func setUID(t *testing.T, h *harness, id int64, uid string) {
	t.Helper()
	h.Exec(t, `UPDATE calendar.appointments SET ical_uid = $1 WHERE id = $2`, uid, id)
}

// allSourceKeys is every fake provider's gate.
var allSourceKeys = []string{"customers:view", "staff:access", "projects:access", "time:access", "expenses:access"}

// feedFixture is the spec's person: a single timed appointment, an all-day
// one, a weekly Oslo series with a cancelled and a moved occurrence, a
// declined one, a cancelled one, a free one, a colleague's own, a timed and
// an all-day provider item and a done one, and a subscription with a body.
type feedFixture struct {
	h        *harness
	me       *modtest.Client
	meID     uuid.UUID
	single   gen.Appointment
	full     gen.FeedAddress
	busy     gen.FeedAddress
	language string
}

func newFeedFixture(t *testing.T, language string) feedFixture {
	t.Helper()
	h := newHarness(t)
	me, meID := signIn(t, h, allSourceKeys...)
	nameUser(t, h, meID, "Kari Nordmann", language)
	colleague, _ := signIn(t, h)

	single := create(t, me, with(timed(t, "Kundemøte; review, round 2", "Europe/Oslo", "2026-10-05T09:00", "2026-10-05T10:00"),
		"location", "Storgata 1, Oslo", "description", "Agenda:\nthe contract"))
	setUID(t, h, single.Id, "single@vantigo")
	offsite := create(t, me, with(allDay("Offsite", "Europe/Oslo", "2026-10-07", "2026-10-08"), "visibility", "private"))
	setUID(t, h, offsite.Id, "all-day@vantigo")
	series := create(t, me, with(timed(t, "Stand-up", "Europe/Oslo", "2026-10-19T09:00", "2026-10-19T09:30"),
		"recurrence", map[string]any{"rule": "FREQ=WEEKLY;COUNT=6"}))
	setUID(t, h, series.Id, "series@vantigo")
	do(t, me, http.MethodPut, occurrencePath(series.Id, "2026-10-26"), map[string]any{"cancelled": true}, http.StatusOK, nil)
	moved := at(t, "Europe/Oslo", "2026-11-02T11:00")
	do(t, me, http.MethodPut, occurrencePath(series.Id, "2026-11-02"),
		map[string]any{"startsAt": moved.UTC(), "endsAt": moved.Add(30 * time.Minute).UTC()}, http.StatusOK, nil)
	lunch := create(t, me, with(timed(t, "Lunch", "Europe/Oslo", "2026-10-06T11:00", "2026-10-06T12:00"), "transparency", "free"))
	setUID(t, h, lunch.Id, "free@vantigo")

	declined := create(t, colleague, with(timed(t, "Declined meeting", "Europe/Oslo", "2026-10-08T13:00", "2026-10-08T14:00"),
		"attendees", userAttendees(meID.String())))
	do(t, me, http.MethodPost, responsePath(declined.Id), map[string]any{"status": "declined"}, http.StatusOK, nil)
	create(t, colleague, timed(t, "Colleague only", "Europe/Oslo", "2026-10-05T12:00", "2026-10-05T13:00"))
	cancelled := create(t, me, with(timed(t, "Cancelled meeting", "Europe/Oslo", "2026-10-09T09:00", "2026-10-09T10:00"),
		"attendees", []map[string]any{emailAttendee(perMail, "")}))
	do(t, me, http.MethodDelete, appointmentPath(cancelled.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM calendar.appointments WHERE id = $1 AND cancelled_at IS NOT NULL`, cancelled.Id); n != 1 {
		t.Fatalf("the cancelled appointment is not B's cancellation (%d rows)", n)
	}

	h.sources.time.give(meID, contracts.CalendarItem{
		Kind: "time.entry", ID: "14", Title: "Development", Subtitle: "ACME-1 Website",
		StartsAt: at(t, "Europe/Oslo", "2026-10-05T13:00"), EndsAt: at(t, "Europe/Oslo", "2026-10-05T15:00"), Path: "/time/entries/14",
	})
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	h.sources.customers.give(meID, contracts.CalendarItem{
		Kind: "customers.follow-up", ID: "11", Title: "Call Ola", Subtitle: "Fjord AS",
		AllDay: true, StartDate: day("2026-10-06"), EndDate: day("2026-10-06"), Path: "/customers/42",
	})
	h.sources.projects.give(meID, contracts.CalendarItem{
		Kind: "projects.task", ID: "13", Title: "Ship the release", AllDay: true,
		StartDate: day("2026-10-08"), EndDate: day("2026-10-08"), Done: true, Path: "/projects/7",
	})
	h.Exec(t, `INSERT INTO calendar.subscriptions (user_id, name, url, colour, busy_only, body, created_at, updated_at)
	           VALUES ($1, 'Elsewhere', 'https://elsewhere.example.com/cal.ics', 'blue', false, $2, $3, $3)`, meID,
		"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:x\r\nBEGIN:VEVENT\r\nUID:sub@elsewhere\r\nDTSTAMP:20260912T120000Z\r\n"+
			"DTSTART:20261005T060000Z\r\nDTEND:20261005T070000Z\r\nSUMMARY:Subscribed thing\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", h.Now())

	return feedFixture{
		h: h, me: me, meID: meID, single: single, language: language,
		full: newAddress(t, me, "full"), busy: newAddress(t, me, "busy"),
	}
}

func TestFeed_TheFullFeedMatchesTheGolden(t *testing.T) {
	t.Parallel()
	f := newFeedFixture(t, "nb")
	r := feedOf(t, f.h, f.full.Address, http.StatusOK)
	if ct := r.Header("Content-Type"); ct != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc, cd := r.Header("Cache-Control"), r.Header("Content-Disposition"); cc != "private, no-cache" || cd != `inline; filename="vantigo.ics"` {
		t.Errorf("Cache-Control %q, Content-Disposition %q", cc, cd)
	}
	if tag := r.Header("ETag"); len(tag) != 66 || tag[0] != '"' || tag[65] != '"' {
		t.Errorf("ETag = %q, want a quoted hex SHA-256", tag)
	}
	body := string(r.Body)
	matchesGolden(t, "full", body)

	ics := unfold(body)
	for _, want := range []string{
		"NAME:Vantigo\r\n", "X-WR-CALNAME:Vantigo\r\n", "REFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n", "X-PUBLISHED-TTL:PT1H\r\n",
		"CATEGORIES:time\r\n", "CATEGORIES:customers\r\n", "SUMMARY:✓ Ship the release\r\n",
		"URL:http://modules.example.com/customers/42\r\n", "UID:time.entry:14@vantigo\r\n",
		fmt.Sprintf("URL:http://modules.example.com/calendar?date=2026-10-05&appointment=%d\r\n", f.single.Id),
		"TRANSP:TRANSPARENT\r\n", "CLASS:PRIVATE\r\n", "EXDATE;TZID=Europe/Oslo:20261026T090000\r\n",
		"RECURRENCE-ID;TZID=Europe/Oslo:20261102T090000\r\n",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("the full feed lacks %q", want)
		}
	}
	if n := strings.Count(ics, "BEGIN:VTIMEZONE"); n != 1 {
		t.Errorf("%d VTIMEZONEs, want one", n)
	}
	for _, never := range []string{"METHOD", "ORGANIZER", "ATTENDEE", "Declined meeting", "Cancelled meeting", "Colleague only", "Subscribed thing", "Nyttårsdag"} {
		if strings.Contains(ics, never) {
			t.Errorf("the full feed carries %q", never)
		}
	}
}

func TestFeed_TheBusyFeedIsSpansAloneAndMatchesItsGolden(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ language, golden, word, name string }{
		{"en", "busy", "Busy", "Vantigo (busy)"},
		{"nb", "busy-nb", "Opptatt", "Vantigo (opptatt)"},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			t.Parallel()
			f := newFeedFixture(t, tc.language)
			body := string(feedOf(t, f.h, f.busy.Address, http.StatusOK).Body)
			matchesGolden(t, tc.golden, body)
			ics := unfold(body)
			if !strings.Contains(ics, "NAME:"+tc.name+"\r\n") || !strings.Contains(ics, "X-WR-CALNAME:"+tc.name+"\r\n") {
				t.Errorf("the busy feed is not named %q", tc.name)
			}
			for _, l := range lines(ics, "SUMMARY") {
				if l != "SUMMARY:"+tc.word {
					t.Errorf("the busy feed carries %q, want SUMMARY:%s alone", l, tc.word)
				}
			}
			for _, never := range []string{"Kundemøte", "Offsite", "Stand-up", "Lunch", "free@vantigo", "LOCATION", "DESCRIPTION",
				"CATEGORIES", "URL", "CLASS", "TRANSPARENT", "follow-up", "projects.task", "ORGANIZER", "ATTENDEE", "METHOD"} {
				if strings.Contains(ics, never) {
					t.Errorf("the busy feed carries %q", never)
				}
			}
			for _, want := range []string{"UID:single@vantigo\r\n", "UID:all-day@vantigo\r\n", "UID:series@vantigo\r\n",
				"UID:time.entry:14@vantigo\r\n", "RECURRENCE-ID;TZID=Europe/Oslo:20261102T090000\r\n"} {
				if !strings.Contains(ics, want) {
					t.Errorf("the busy feed lacks %q", want)
				}
			}
		})
	}
}

func TestFeed_AProvidersKindIsInWithItsKeyHeldAndOutWithItRevoked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, meID := signIn(t, h, "time:access")
	start := at(t, "Europe/Oslo", "2026-10-05T13:00")
	h.sources.time.give(meID, contracts.CalendarItem{Kind: "time.entry", ID: "1", Title: "Entry", StartsAt: start, EndsAt: start.Add(time.Hour)})
	h.sources.customers.give(meID, contracts.CalendarItem{Kind: "customers.follow-up", ID: "2", Title: "Follow-up", StartsAt: start, EndsAt: start})
	a := newAddress(t, me, "full")

	ics := feedText(t, h, a.Address)
	if !strings.Contains(ics, "UID:time.entry:1@vantigo") || strings.Contains(ics, "customers.follow-up") {
		t.Errorf("with time:access alone the feed is:\n%s", ics)
	}
	h.Exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'time:access'
	           AND role_id IN (SELECT role_id FROM identity.user_roles WHERE user_id = $1)`, meID)
	if ics := feedText(t, h, a.Address); strings.Contains(ics, "time.entry") {
		t.Errorf("with time:access revoked the feed still carries the entry:\n%s", ics)
	}
}

func TestFeed_AProviderThatFailsMakesThe503(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, _ := signIn(t, h, allSourceKeys...)
	a := newAddress(t, me, "busy")
	h.sources.staff.failWith(errors.New("staff is down"))
	r := feedOf(t, h, a.Address, http.StatusServiceUnavailable)
	if strings.Contains(string(r.Body), "staff") {
		t.Errorf("the 503 names the module: %s", r.Body)
	}
	if got := addresses(t, me); got.Addresses[0].LastUsedAt != nil {
		t.Error("a 503 recorded the address as read")
	}
}

func TestFeed_TheETagHoldsAcrossAMinuteIs304AndChangesWithATitle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, _ := signIn(t, h)
	appt := create(t, me, timed(t, "Planning", "Europe/Oslo", "2026-10-05T09:00", "2026-10-05T10:00"))
	a := newAddress(t, me, "full")

	first := feedOf(t, h, a.Address, http.StatusOK)
	h.Advance(time.Minute)
	second := feedOf(t, h, a.Address, http.StatusOK)
	tag := first.Header("ETag")
	if tag == "" || second.Header("ETag") != tag {
		t.Errorf("ETag %q then %q, want one tag", tag, second.Header("ETag"))
	}
	if bytes.Equal(first.Body, second.Body) {
		t.Error("the two bodies are the same; DTSTAMP should be each request's time")
	}

	h.Advance(time.Minute)
	for _, inm := range []string{tag, "*", `"other", ` + tag, "W/" + tag} {
		r := feedOf(t, h, a.Address, http.StatusNotModified, modtest.Header("If-None-Match", inm))
		if r.Header("ETag") != tag || r.Header("Cache-Control") != "private, no-cache" || len(r.Body) != 0 {
			t.Errorf("If-None-Match %s: ETag %q, Cache-Control %q, body %q", inm, r.Header("ETag"), r.Header("Cache-Control"), r.Body)
		}
	}
	if got := addresses(t, me); got.Addresses[0].LastUsedAt == nil || !got.Addresses[0].LastUsedAt.Equal(h.Now()) {
		t.Errorf("a 304 did not record the read: %v", got.Addresses[0].LastUsedAt)
	}

	put(t, me, appt, timed(t, "Planning, moved on", "Europe/Oslo", "2026-10-05T09:00", "2026-10-05T10:00"))
	changed := feedOf(t, h, a.Address, http.StatusOK, modtest.Header("If-None-Match", tag))
	if changed.Header("ETag") == tag {
		t.Error("the ETag did not change with the title")
	}
}

func TestFeed_OverOneMiBOfProviderItemsDropsTheFarthestAndKeepsTheAppointments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, meID := signIn(t, h, "time:access")
	create(t, me, timed(t, "Far appointment", "Europe/Oslo", "2027-08-30T09:00", "2027-08-30T10:00"))
	a := newAddress(t, me, "full")
	const n = 6000
	list := make([]contracts.CalendarItem, n)
	for i := range list {
		start := h.Now().Add(time.Duration(i) * time.Hour)
		list[i] = contracts.CalendarItem{
			Kind: "time.entry", ID: fmt.Sprint(i), Title: "Entry " + strings.Repeat("x", 150), Subtitle: strings.Repeat("y", 100),
			StartsAt: start, EndsAt: start.Add(30 * time.Minute), Path: fmt.Sprintf("/time/entries/%d", i),
		}
	}
	h.sources.time.give(meID, list...)

	body := string(feedOf(t, h, a.Address, http.StatusOK).Body)
	if len(body) > 1<<20 {
		t.Errorf("the feed is %d bytes, over 1 MiB", len(body))
	}
	ics := unfold(body)
	if !strings.Contains(ics, "UID:time.entry:0@vantigo\r\n") || strings.Contains(ics, fmt.Sprintf("UID:time.entry:%d@vantigo\r\n", n-1)) {
		t.Error("the nearest entry is gone or the farthest kept")
	}
	if !strings.Contains(ics, "SUMMARY:Far appointment\r\n") {
		t.Error("an appointment was dropped")
	}
	if !strings.Contains(h.Logs(), "the feed was over 1 MiB") || !strings.Contains(h.Logs(), meID.String()) {
		t.Error("the drop was not logged with the person's id")
	}
}
