package google_test

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The mailbox calendar on the person's own Google connection (Google
// Workspace design G30, G31): the status, the copies written with the
// installation's marker in batches, the calendars the module makes, the
// delta with its sync token and the link, the write-back's reads, the
// connection's answers, against the fake Calendar v3.

// calendarFixture is a connected organisation with Connection for people on
// and the module's mailbox provider.
type calendarFixture struct {
	*personalFixture
	p contracts.MailboxCalendar
}

func newCalendarFixture(t *testing.T, opts ...modtest.Option) *calendarFixture {
	t.Helper()
	f := newPersonalFixture(t, opts...)
	return &calendarFixture{personalFixture: f, p: google.Module().MailboxCalendar(f.h.Deps())}
}

// marker is the installation's marker.
func (f *calendarFixture) marker(t *testing.T) string {
	t.Helper()
	var m string
	if err := f.h.Pool().QueryRow(context.Background(), `SELECT marker FROM google.connection`).Scan(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// calendarCalls are the fake's Calendar calls since from, batches' parts
// included.
func (f *calendarFixture) calendarCalls(from int) []googletest.Call {
	var out []googletest.Call
	for _, c := range f.g.Calls()[from:] {
		if c.Host == googletest.APIsHost && (strings.HasPrefix(c.Path, "/calendar/v3/") || strings.HasPrefix(c.Path, "/batch/calendar/v3")) {
			out = append(out, c)
		}
	}
	return out
}

// timedEvent is a timed event from start for d, in Oslo.
func timedEvent(subject string, start time.Time, d time.Duration) *contracts.MailboxEvent {
	return &contracts.MailboxEvent{Subject: subject, Body: "Body of " + subject, StartsAt: start, EndsAt: start.Add(d), TimeZone: "Europe/Oslo", ShowAs: contracts.ShowAsBusy}
}

// create is a create op of key for ev.
func create(key string, ev *contracts.MailboxEvent) contracts.MailboxOp {
	return contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: key, Event: ev}
}

// apply runs ops, failing on the call's error or any op's.
func apply(t *testing.T, p contracts.MailboxCalendar, mailbox, calendarID string, ops ...contracts.MailboxOp) []contracts.MailboxResult {
	t.Helper()
	results, err := p.Apply(context.Background(), mailbox, calendarID, ops)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, r := range results {
		if r.Err != nil {
			t.Fatalf("Apply op %d: %v", i, r.Err)
		}
	}
	return results
}

// day is a fixed Monday in the fake's future, at 09:00 UTC.
func day(f *calendarFixture) time.Time {
	y, m, d := f.h.Now().AddDate(0, 0, 7).Date()
	return time.Date(y, m, d, 9, 0, 0, 0, time.UTC)
}

func TestMailbox_Status(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	ctx := context.Background()
	if f.p.Key() != "google" || f.p.DisplayName() != "Google Calendar" {
		t.Errorf("Key, DisplayName = %q, %q", f.p.Key(), f.p.DisplayName())
	}
	if st, err := f.p.Status(ctx); err != nil || st.State != contracts.MailboxAvailable {
		t.Errorf("on: %+v %v, want available", st, err)
	}
	// The switch off, nobody's token held: unavailable.
	setPersonal(t, f.o, false)
	if st, _ := f.p.Status(ctx); st.State != contracts.MailboxUnavailable || st.Reason != "switch_off" {
		t.Errorf("off, no one connected: %+v, want unavailable switch_off", st)
	}
	// Someone connected, then the switch off: their token serves removals.
	setPersonal(t, f.o, true)
	f.connected(t, "kari@nordlys.no")
	setPersonal(t, f.o, false)
	if st, _ := f.p.Status(ctx); st.State != contracts.MailboxCleanup || st.Reason != "switch_off" {
		t.Errorf("off, a token held: %+v, want cleanup switch_off", st)
	}
	// The switch on but the domains never read: unavailable, not_tested.
	g := newCalendarFixture(t)
	g.h.Exec(t, `UPDATE google.connection SET domains = '{}'`)
	if st, _ := g.p.Status(ctx); st.State != contracts.MailboxUnavailable || st.Reason != "not_tested" {
		t.Errorf("not tested: %+v, want unavailable not_tested", st)
	}
}

func TestMailbox_ApplyWritesCopiesWithTheMarkerInBatches(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	marker := f.marker(t)
	if len(marker) != 32 {
		t.Fatalf("marker %q, want 32 hex digits", marker)
	}
	start := day(f)
	before := len(f.g.Calls())
	allDay := &contracts.MailboxEvent{Subject: "Holiday", AllDay: true, StartDate: civil(start), EndDate: civil(start).AddDate(0, 0, 1), ShowAs: contracts.ShowAsOutOfOffice}
	free := timedEvent("Lunch", start.Add(3*time.Hour), time.Hour)
	free.ShowAs = contracts.ShowAsFree
	private := timedEvent("Doctor", start.Add(5*time.Hour), time.Hour)
	private.Private = true
	results := apply(t, f.p, gid, "",
		create("appointment:1", timedEvent("Plan", start, time.Hour)),
		create("appointment:2", allDay),
		create("appointment:3", free),
		create("appointment:4", private),
	)
	calls := f.calendarCalls(before)
	batches := 0
	for _, c := range calls {
		q, _ := url.ParseQuery(c.Query)
		if q.Get("quotaUser") != gid {
			t.Errorf("%s %s without the person's quotaUser: %q", c.Method, c.Path, c.Query)
		}
		if c.Path == "/batch/calendar/v3" {
			batches++
		}
	}
	if batches != 1 {
		t.Errorf("%d batches, want 1", batches)
	}
	events := f.g.LiveEvents(gid, "")
	if len(events) != 4 {
		t.Fatalf("%d events, want 4", len(events))
	}
	for i, ev := range events {
		if results[i].EventID != ev.ID || results[i].ChangeKey != ev.Etag || ev.Etag == "" {
			t.Errorf("result %d = %+v, event %s %s", i, results[i], ev.ID, ev.Etag)
		}
		if ev.Private["vantigo"] != marker || ev.Private["vantigoKey"] != "appointment:"+string(rune('1'+i)) {
			t.Errorf("event %d's marker %v", i, ev.Private)
		}
	}
	if e := events[0]; e.Start.DateTime == "" || e.Start.TimeZone != "Europe/Oslo" || e.Transparency != "opaque" || e.Summary != "Plan" {
		t.Errorf("the timed event: %+v", e)
	}
	if e := events[1]; e.Start.Date != civil(start).Format(time.DateOnly) || e.End.Date != civil(start).AddDate(0, 0, 2).Format(time.DateOnly) || e.Start.DateTime != "" {
		t.Errorf("the all-day event: %+v, want its end the day after the last", e)
	}
	if events[2].Transparency != "transparent" {
		t.Errorf("a free event's transparency %q", events[2].Transparency)
	}
	if events[3].Visibility != "private" {
		t.Errorf("a private event's visibility %q", events[3].Visibility)
	}

	// An update with no key keeps the marker; the change key moves.
	moved := timedEvent("Plan, moved", start.Add(time.Hour), time.Hour)
	up := apply(t, f.p, gid, "", contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: results[0].EventID, Event: moved})
	after := f.g.LiveEvents(gid, "")[0]
	if up[0].ChangeKey != after.Etag || up[0].ChangeKey == results[0].ChangeKey || after.Summary != "Plan, moved" || after.Private["vantigoKey"] != "appointment:1" {
		t.Errorf("the update: %+v, event %+v", up[0], after)
	}
	// An all-day event moved to timed clears its date.
	toTimed := apply(t, f.p, gid, "", contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: results[1].EventID, Event: timedEvent("Holiday", start, time.Hour)})
	if toTimed[0].Gone {
		t.Fatal("an update answered gone")
	}
	for _, ev := range f.g.LiveEvents(gid, "") {
		if ev.ID == results[1].EventID && (ev.Start.Date != "" || ev.Start.DateTime == "") {
			t.Errorf("the all-day event made timed: %+v", ev.Start)
		}
	}

	// A delete; again, gone; an update of it, gone.
	del := contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: results[2].EventID}
	if r := apply(t, f.p, gid, "", del); r[0].Gone {
		t.Errorf("the first delete: %+v", r[0])
	}
	if r := apply(t, f.p, gid, "", del); !r[0].Gone {
		t.Errorf("the second delete: %+v, want gone", r[0])
	}
	if r := apply(t, f.p, gid, "", contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: results[2].EventID, Event: free}); !r[0].Gone {
		t.Errorf("an update of a deleted event: %+v, want gone", r[0])
	}

	// Find answers the copies alone: not the person's own, not another
	// installation's.
	f.g.PutEvent(gid, "", googletest.Event{Summary: "Mine", Start: googletest.EventTime{DateTime: start.Format(time.RFC3339)}, End: googletest.EventTime{DateTime: start.Add(time.Hour).Format(time.RFC3339)}})
	f.g.PutEvent(gid, "", googletest.Event{Summary: "Theirs", Private: map[string]string{"vantigo": strings.Repeat("0", 32), "vantigoKey": "appointment:9"},
		Start: googletest.EventTime{DateTime: start.Format(time.RFC3339)}, End: googletest.EventTime{DateTime: start.Add(time.Hour).Format(time.RFC3339)}})
	found, err := f.p.Find(context.Background(), gid, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, r := range found {
		keys = append(keys, r.Key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"appointment:1", "appointment:2", "appointment:4"}) {
		t.Errorf("Find = %v", keys)
	}
	// from bounds the start.
	later, err := f.p.Find(context.Background(), gid, "", start.Add(4*time.Hour))
	if err != nil || len(later) != 1 || later[0].Key != "appointment:4" {
		t.Errorf("Find from: %+v %v", later, err)
	}
	// Versions on the default calendar: the copies with their change keys.
	v, err := f.p.Versions(context.Background(), gid, "", time.Time{}, time.Time{})
	if err != nil || !v.Complete || len(v.Events) != 3 {
		t.Fatalf("Versions: %+v %v", v, err)
	}
	for _, ev := range v.Events {
		if ev.EventID == results[0].EventID && (ev.ChangeKey != up[0].ChangeKey || ev.Key != "appointment:1") {
			t.Errorf("Versions of the updated copy: %+v, want %q", ev, up[0].ChangeKey)
		}
	}
	f.noSecretsLeaked(t, gid)
	if strings.Contains(f.h.Logs(), "Body of") || strings.Contains(f.h.Logs(), "Plan") {
		t.Error("the log holds an event's words")
	}
}

// civil is t's date at midnight UTC.
func civil(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestMailbox_ApplyRefusalsAndThrottles(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	start := day(f)
	// A throttled part is sent again once and done.
	f.g.ThrottleParts(1)
	apply(t, f.p, gid, "", create("appointment:1", timedEvent("Plan", start, time.Hour)))
	// Throttled twice: errThrottled for that op alone.
	f.g.ThrottleParts(2)
	results, err := f.p.Apply(context.Background(), gid, "", []contracts.MailboxOp{create("appointment:2", timedEvent("Plan", start, time.Hour))})
	if err != nil || results[0].Err == nil || results[0].EventID != "" {
		t.Errorf("throttled twice: %+v %v, want the op's failure", results, err)
	}
	// A 403 of the calendar is ErrMailboxDenied, and the rest answer it unsent.
	f.g.FailOn("POST /calendar/v3/calendars/primary/events", 403)
	ops := make([]contracts.MailboxOp, 60)
	for i := range ops {
		ops[i] = create("appointment:x", timedEvent("Plan", start, time.Hour))
	}
	results, err = f.p.Apply(context.Background(), gid, "", ops)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if !errors.Is(r.Err, contracts.ErrMailboxDenied) {
			t.Fatalf("op %d: %v, want denied", i, r.Err)
		}
	}
}

func TestMailbox_Calendars(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()

	id, err := f.p.EnsureCalendar(ctx, gid, "", "Vantigo")
	if err != nil || id == "" {
		t.Fatalf("EnsureCalendar: %q %v", id, err)
	}
	if got := f.g.Calendars(gid); !slices.Equal(got, []string{id}) {
		t.Errorf("calendars %v", got)
	}
	again, err := f.p.EnsureCalendar(ctx, gid, id, "Vantigo")
	if err != nil || again != id {
		t.Errorf("known: %q %v", again, err)
	}
	// Known lost by the caller: the module's own calendar is adopted.
	if adopted, err := f.p.EnsureCalendar(ctx, gid, "", "Vantigo"); err != nil || adopted != id {
		t.Errorf("adopted: %q %v", adopted, err)
	}
	if n := len(f.g.Calendars(gid)); n != 1 {
		t.Errorf("%d calendars, want one", n)
	}
	// Copies in it, and its view's versions.
	start := day(f)
	r := apply(t, f.p, gid, id, create("appointment:1", timedEvent("Plan", start, time.Hour)))
	v, err := f.p.Versions(ctx, gid, id, start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil || len(v.Events) != 1 || v.Events[0].EventID != r[0].EventID || v.Events[0].Key != "" || v.Events[0].ChangeKey != r[0].ChangeKey {
		t.Errorf("the secondary view: %+v %v", v, err)
	}
	// The person deletes it: a new one is made, the row of the old dropped.
	f.g.DeleteCalendarAsPerson(gid, id)
	made, err := f.p.EnsureCalendar(ctx, gid, id, "Vantigo")
	if err != nil || made == id || made == "" {
		t.Fatalf("after the person's delete: %q %v", made, err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.calendars WHERE google_user_id = $1`, gid); n != 1 {
		t.Errorf("%d rows, want the new calendar's alone", n)
	}
	// A calendar the module did not make is never deleted.
	if err := f.p.DeleteCalendar(ctx, gid, "primary"); !errors.Is(err, contracts.ErrCalendarNotOurs) {
		t.Errorf("DeleteCalendar(primary): %v, want not ours", err)
	}
	if err := f.p.DeleteCalendar(ctx, gid, made); err != nil {
		t.Fatalf("DeleteCalendar: %v", err)
	}
	if len(f.g.Calendars(gid)) != 0 || f.h.Count(t, `SELECT count(*) FROM google.calendars`) != 0 {
		t.Error("the calendar or its row is left")
	}
	if err := f.p.DeleteCalendar(ctx, gid, made); err != nil {
		t.Errorf("DeleteCalendar again: %v, want done", err)
	}
	// A grant that may not make a calendar (U8): denied.
	f.g.DenyCalendarCreate(true)
	if _, err := f.p.EnsureCalendar(ctx, gid, "", "Vantigo"); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("EnsureCalendar denied: %v, want ErrMailboxDenied", err)
	}
}

// own is an event the person put in their primary calendar.
func own(start time.Time, d time.Duration, change func(*googletest.Event)) googletest.Event {
	ev := googletest.Event{Summary: "Secret meeting", Description: "Secret words",
		Start: googletest.EventTime{DateTime: start.Format(time.RFC3339)}, End: googletest.EventTime{DateTime: start.Add(d).Format(time.RFC3339)}}
	if change != nil {
		change(&ev)
	}
	return ev
}

func changesByID(d contracts.MailboxDelta) map[string]contracts.MailboxChange {
	out := map[string]contracts.MailboxChange{}
	for _, c := range d.Changes {
		out[c.EventID] = c
	}
	return out
}

func TestMailbox_Delta(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	start := day(f)
	from, to := start.Add(-48*time.Hour), start.Add(30*24*time.Hour)

	busy := f.g.PutEvent(gid, "", own(start, time.Hour, nil))
	oof := f.g.PutEvent(gid, "", own(start.Add(24*time.Hour), 8*time.Hour, func(e *googletest.Event) { e.EventType = "outOfOffice" }))
	free := f.g.PutEvent(gid, "", own(start.Add(2*time.Hour), time.Hour, func(e *googletest.Event) { e.Transparency = "transparent" }))
	where := f.g.PutEvent(gid, "", own(start.Add(3*time.Hour), time.Hour, func(e *googletest.Event) { e.EventType = "workingLocation"; e.Transparency = "opaque" }))
	declined := f.g.PutEvent(gid, "", own(start.Add(4*time.Hour), time.Hour, func(e *googletest.Event) {
		e.Attendees = []googletest.Attendee{{Email: "boss@nordlys.no"}, {Email: "kari@nordlys.no", Self: true, ResponseStatus: "declined"}}
	}))
	maybe := f.g.PutEvent(gid, "", own(start.Add(5*time.Hour), time.Hour, func(e *googletest.Event) {
		e.Attendees = []googletest.Attendee{{Email: "kari@nordlys.no", Self: true, ResponseStatus: "tentative"}}
	}))
	allDay := f.g.PutEvent(gid, "", googletest.Event{Summary: "Away", Start: googletest.EventTime{Date: civil(start).Format(time.DateOnly)}, End: googletest.EventTime{Date: civil(start).AddDate(0, 0, 2).Format(time.DateOnly)}})
	instance := f.g.PutEvent(gid, "", own(start.Add(6*time.Hour), time.Hour, func(e *googletest.Event) { e.RecurringEventID = "series1" }))
	outside := f.g.PutEvent(gid, "", own(start.Add(60*24*time.Hour), time.Hour, nil))

	before := len(f.g.Calls())
	d, err := f.p.Delta(ctx, gid, from, to, "")
	if err != nil || !d.Done || !strings.HasPrefix(d.Link, "g1.") {
		t.Fatalf("Delta: %+v %v", d, err)
	}
	for _, c := range f.calendarCalls(before) {
		q, _ := url.ParseQuery(c.Query)
		if strings.Contains(q.Get("fields"), "summary") || strings.Contains(q.Get("fields"), "description") || strings.Contains(q.Get("fields"), "email") {
			t.Errorf("the delta asked for words: %s", q.Get("fields"))
		}
	}
	got := changesByID(d)
	want := map[string]string{busy.ID: "busy", oof.ID: "out_of_office", free.ID: "free", where.ID: "working_elsewhere",
		declined.ID: "free", maybe.ID: "tentative", allDay.ID: "busy", instance.ID: "busy"}
	for id, showAs := range want {
		if c, ok := got[id]; !ok || c.ShowAs != showAs || c.Removed {
			t.Errorf("change %s = %+v, want %s", id, c, showAs)
		}
	}
	if _, ok := got[outside.ID]; ok {
		t.Error("an event outside the window was answered")
	}
	if c := got[allDay.ID]; !c.AllDay || !c.StartDate.Equal(civil(start)) || !c.EndDate.Equal(civil(start).AddDate(0, 0, 1)) {
		t.Errorf("the all-day change %+v, want its end inclusive", c)
	}
	if c := got[busy.ID]; !c.StartsAt.Equal(start) || !c.EndsAt.Equal(start.Add(time.Hour)) {
		t.Errorf("the timed change %+v", c)
	}
	if !got[instance.ID].Series || got[busy.ID].Series {
		t.Error("Series is an instance of a recurring event alone")
	}

	// The person moves one out of the window, deletes another, adds one; the
	// continuation answers them, the moved one Removed (the increment is not
	// windowed, U7).
	f.g.EditEvent(gid, "", busy.ID, func(e *googletest.Event) {
		e.Start.DateTime = start.Add(90 * 24 * time.Hour).Format(time.RFC3339)
		e.End.DateTime = start.Add(90*24*time.Hour + time.Hour).Format(time.RFC3339)
	})
	f.g.EditEvent(gid, "", oof.ID, func(e *googletest.Event) { e.Status = "cancelled" })
	added := f.g.PutEvent(gid, "", own(start.Add(7*time.Hour), time.Hour, nil))
	d2, err := f.p.Delta(ctx, gid, time.Time{}, time.Time{}, d.Link)
	if err != nil || !d2.Done {
		t.Fatalf("the continuation: %+v %v", d2, err)
	}
	got = changesByID(d2)
	if len(got) != 3 || !got[busy.ID].Removed || !got[oof.ID].Removed || got[added.ID].Removed || got[added.ID].ShowAs != "busy" {
		t.Errorf("the continuation's changes %+v", d2.Changes)
	}

	// Another mailbox's link, or no link of the provider's, is refused unsent.
	_, other := f.connected(t, "ola@nordlys.no")
	n := len(f.g.Calls())
	for _, link := range []string{d2.Link, "https://www.googleapis.com/calendar/v3/calendars/primary/events", "g1.bm9uc2Vuc2U"} {
		if _, err := f.p.Delta(ctx, other, time.Time{}, time.Time{}, link); err == nil || errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("a foreign link %.20q: %v, want a failure", link, err)
		}
	}
	if len(f.calendarCalls(n)) != 0 {
		t.Error("a foreign link reached Google")
	}

	// 410: the delta starts again.
	f.g.ExpireSyncTokens()
	if _, err := f.p.Delta(ctx, gid, time.Time{}, time.Time{}, d2.Link); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("an expired token: %v, want ErrMailboxDeltaExpired", err)
	}
	f.noSecretsLeaked(t, gid)
	if strings.Contains(f.h.Logs(), "Secret") {
		t.Error("the log holds an event's words")
	}
}

func TestMailbox_DeltaPagesAndTheFallbackWithoutAToken(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	start := day(f)
	for i := range 260 {
		f.g.PutEvent(gid, "", own(start.Add(time.Duration(i)*time.Hour), 30*time.Minute, nil))
	}
	d, err := f.p.Delta(ctx, gid, start.Add(-time.Hour), start.Add(400*time.Hour), "")
	if err != nil || !d.Done || len(d.Changes) != 260 {
		t.Fatalf("two pages: %d changes, done %v, %v", len(d.Changes), d.Done, err)
	}

	// No sync token answered (U7): the next read answers expired, at once.
	f.g.SetNoSyncToken(true)
	d, err = f.p.Delta(ctx, gid, start.Add(-time.Hour), start.Add(400*time.Hour), "")
	if err != nil || !d.Done {
		t.Fatalf("a windowed read with no token: %v", err)
	}
	n := len(f.g.Calls())
	for range 2 {
		if _, err := f.p.Delta(ctx, gid, time.Time{}, time.Time{}, d.Link); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("a continuation with no token: %v, want ErrMailboxDeltaExpired", err)
		}
	}
	if len(f.calendarCalls(n)) != 0 {
		t.Error("a continuation with no token reached Google")
	}
	if c := strings.Count(f.h.Logs(), "no sync token"); c != 1 {
		t.Errorf("the fallback logged %d times, want once", c)
	}
}

func TestMailbox_MarkedAndEvents(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	start := day(f)
	results := apply(t, f.p, gid, "",
		create("appointment:1", timedEvent("Plan", start, time.Hour)),
		create("appointment:2", timedEvent("Gone", start, time.Hour)),
	)
	apply(t, f.p, gid, "", contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: results[1].EventID})
	mine := f.g.PutEvent(gid, "", own(start, time.Hour, func(e *googletest.Event) { e.Description = "<b>bold</b> words" }))
	foreign := f.g.PutEvent(gid, "", own(start, time.Hour, func(e *googletest.Event) {
		e.Private = map[string]string{"vantigo": strings.Repeat("1", 32), "vantigoKey": "appointment:7"}
	}))

	marked, err := f.p.Marked(ctx, gid, []string{mine.ID, results[0].EventID, results[1].EventID, "nosuchevent", foreign.ID})
	if err != nil || len(marked) != 1 || marked[0].EventID != results[0].EventID || marked[0].Key != "appointment:1" {
		t.Errorf("Marked: %+v %v", marked, err)
	}

	// The person edits the copy's title.
	f.g.EditEvent(gid, "", results[0].EventID, func(e *googletest.Event) { e.Summary = "Plan, renamed" })
	read, err := f.p.Events(ctx, gid, "", []string{results[0].EventID, mine.ID, results[1].EventID, "nosuchevent", foreign.ID})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]contracts.MailboxEventState{}
	for _, e := range read.Events {
		byID[e.EventID] = e
	}
	copyRead := byID[results[0].EventID]
	if copyRead.Key != "appointment:1" || copyRead.Event.Subject != "Plan, renamed" || !copyRead.TextBody || copyRead.Event.Body != "Body of Plan" ||
		!copyRead.Event.StartsAt.Equal(start) || copyRead.ChangeKey == results[0].ChangeKey || copyRead.Foreign {
		t.Errorf("the copy read whole: %+v", copyRead)
	}
	if m := byID[mine.ID]; m.Key != "" || m.TextBody || m.Foreign {
		t.Errorf("the person's own event: %+v, want no key and an HTML body", m)
	}
	if fr := byID[foreign.ID]; !fr.Foreign || fr.Key != "" {
		t.Errorf("another installation's: %+v, want foreign", fr)
	}
	slices.Sort(read.Gone)
	want := []string{results[1].EventID, "nosuchevent"}
	slices.Sort(want)
	if !slices.Equal(read.Gone, want) {
		t.Errorf("Gone %v, want %v", read.Gone, want)
	}
	if strings.Contains(f.h.Logs(), "bold") || strings.Contains(f.h.Logs(), "renamed") {
		t.Error("the log holds an event's words")
	}
}

func TestMailbox_TheConnection(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	ctx := context.Background()
	start := day(f)

	// An id the module does not manage.
	if _, err := f.p.Find(ctx, "100000000000000000077", "", time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("an unknown account: %v, want ErrMailboxUnknown", err)
	}
	// An account never connected: the person is needed; a removal is past
	// doing.
	_, _, gid := f.person(t, "per@nordlys.no")
	if _, err := f.p.Delta(ctx, gid, start, start.Add(time.Hour), ""); needs(err) != contracts.ConnectionNotConnected {
		t.Errorf("never connected: %v, want not_connected", err)
	}
	if _, err := f.p.Find(ctx, gid, "", time.Time{}); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("a removal never connected: %v, want ErrMailboxGone", err)
	}

	// A disconnect: the rest needs the person, the removals work while the
	// token is held.
	c, kari := f.connected(t, "kari@nordlys.no")
	results := apply(t, f.p, kari, "", create("appointment:1", timedEvent("Plan", start, time.Hour)))
	if r := c.Do("DELETE", personalMePath, nil); r.Status != 204 {
		t.Fatal(r.Status)
	}
	if _, err := f.p.EnsureCalendar(ctx, kari, "", "Vantigo"); needs(err) != contracts.ConnectionClosingReason {
		t.Errorf("closing: %v, want closing", err)
	}
	if found, err := f.p.Find(ctx, kari, "", time.Time{}); err != nil || len(found) != 1 {
		t.Errorf("Find on a closing connection: %+v %v", found, err)
	}
	if r := apply(t, f.p, kari, "", contracts.MailboxOp{Kind: contracts.MailboxOpDelete, EventID: results[0].EventID}); r[0].Gone {
		t.Errorf("a delete on a closing connection: %+v", r[0])
	}
	// The token forgotten (a leaver): the removals answer gone.
	f.h.Exec(t, `UPDATE google.connections SET refresh_sealed = NULL WHERE google_user_id = $1`, kari)
	if _, err := f.p.Find(ctx, kari, "", time.Time{}); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("a removal with no token: %v, want ErrMailboxGone", err)
	}

	// Google refuses the token on a call: the row to reconnect, sign_in_again.
	_, ola := f.connected(t, "ola@nordlys.no")
	f.g.RefuseCalendar(ola, true)
	if _, err := f.p.Delta(ctx, ola, start, start.Add(time.Hour), ""); needs(err) != "sign_in_again" {
		t.Errorf("a refused token: %v, want sign_in_again", err)
	}
	if state, code := f.rowState(t, ola); state != "reconnect" || code != "sign_in_again" {
		t.Errorf("the row %s %s, want reconnect sign_in_again", state, code)
	}
	// The connection's changes are Connections.
	changes, err := f.p.Connections(ctx, time.Time{})
	states := map[string]string{}
	for _, c := range changes {
		states[c.Mailbox] = c.State
	}
	if err != nil || states[kari] != "closing" || states[ola] != "reconnect" {
		t.Errorf("Connections: %+v %v", changes, err)
	}
}

func TestMailbox_TheFeatureIsNeeded(t *testing.T) {
	t.Parallel()
	f := newCalendarFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	// The calendar's scopes gone from the grant: consent_missing, the row
	// left as it is.
	f.h.Exec(t, `UPDATE google.connections SET scopes = ARRAY['openid', $2], revision = revision + 1 WHERE google_user_id = $1`, gid, scopeGmailSend)
	if _, err := f.p.Find(context.Background(), gid, "", time.Time{}); needs(err) != "consent_missing" {
		t.Errorf("no calendar feature: %v, want consent_missing", err)
	}
	if state, _ := f.rowState(t, gid); state != "active" {
		t.Errorf("the row %s, want active still", state)
	}
}

func TestMailbox_UsageLimitsAndRefusals(t *testing.T) {
	t.Parallel()
	f := newNoticesFixture(t)
	_, gid := f.connected(t, "kari@nordlys.no")
	ctx := context.Background()
	start := day(f)
	ops := func(n int) []contracts.MailboxOp {
		out := make([]contracts.MailboxOp, n)
		for i := range out {
			out[i] = create("appointment:x", timedEvent("Plan", start, time.Hour))
		}
		return out
	}
	sent := func(from int, method, path string) int {
		n := 0
		for _, c := range f.calendarCalls(from) {
			if c.Method == method && c.Path == path {
				n++
			}
		}
		return n
	}

	// A quota spent is a usage limit, not a refusal: each op throttled, sent
	// once, and the next batch still sent.
	f.g.RefuseOn("POST /calendar/v3/calendars/primary/events", 403, "quotaExceeded")
	n := len(f.g.Calls())
	results, err := f.p.Apply(ctx, gid, "", ops(60))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if !google.Throttled(r.Err) || errors.Is(r.Err, contracts.ErrMailboxDenied) {
			t.Fatalf("op %d: %v, want throttled", i, r.Err)
		}
	}
	if got := sent(n, "POST", "/calendar/v3/calendars/primary/events"); got != 60 {
		t.Errorf("%d creates sent, want 60: each once", got)
	}
	f.g.RefuseOn("POST /calendar/v3/calendars/primary/events", 0, "")
	f.g.RefuseOn("GET /calendar/v3/calendars/primary/events", 403, "dailyLimitExceeded")
	n = len(f.g.Calls())
	if _, err := f.p.Delta(ctx, gid, start, start.Add(time.Hour), ""); !google.Throttled(err) || errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("Delta on a day's limit spent: %v, want throttled", err)
	}
	if got := sent(n, "GET", "/calendar/v3/calendars/primary/events"); got != 1 {
		t.Errorf("%d lists sent, want one: a quota spent is not waited", got)
	}

	// The primary calendar missing (404) is ErrMailboxUnknown; a secondary
	// calendar gone answers no events.
	f.g.RefuseOn("GET /calendar/v3/calendars/primary/events", 404, "notFound")
	if _, err := f.p.Delta(ctx, gid, start, start.Add(time.Hour), ""); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Delta with no primary calendar: %v, want ErrMailboxUnknown", err)
	}
	if _, err := f.p.Find(ctx, gid, "", time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Find with no primary calendar: %v, want ErrMailboxUnknown", err)
	}
	if _, err := f.p.Versions(ctx, gid, "", time.Time{}, time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Versions with no primary calendar: %v, want ErrMailboxUnknown", err)
	}
	if found, err := f.p.Find(ctx, gid, "nosuch@group.calendar.google.com", time.Time{}); err != nil || len(found) != 0 {
		t.Errorf("Find on a secondary calendar gone: %+v %v, want none", found, err)
	}
	f.g.RefuseOn("GET /calendar/v3/calendars/primary/events", 0, "")
	f.g.RefuseOn("POST /calendar/v3/calendars/primary/events/watch", 404, "notFound")
	if err := f.p.Watch(ctx, gid); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Watch with no primary calendar: %v, want ErrMailboxUnknown", err)
	}
	f.g.RefuseOn("POST /calendar/v3/calendars/primary/events/watch", 0, "")

	// An event's own 403 is that op's refusal: the mailbox is not denied, and
	// the ops after it — the next batch's too — are written.
	made := apply(t, f.p, gid, "", create("appointment:1", timedEvent("Plan", start, time.Hour)))
	f.g.RefuseOn("PATCH /calendar/v3/calendars/primary/events/"+made[0].EventID, 403, "forbiddenForNonOrganizer")
	batch := append([]contracts.MailboxOp{{Kind: contracts.MailboxOpUpdate, EventID: made[0].EventID, Event: timedEvent("Plan, moved", start, time.Hour)}}, ops(59)...)
	results, err = f.p.Apply(ctx, gid, "", batch)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Err == nil || errors.Is(results[0].Err, contracts.ErrMailboxDenied) || google.Throttled(results[0].Err) {
		t.Errorf("the refused update: %v, want its own refusal", results[0].Err)
	}
	for i, r := range results[1:] {
		if r.Err != nil || r.EventID == "" {
			t.Fatalf("op %d after an event's 403: %+v", i+1, r)
		}
	}

	// A 401 on every part asks the person's connection once: each op needs
	// the person, sign_in_again.
	_, ola := f.connected(t, "ola@nordlys.no")
	f.g.RefuseCalendar(ola, true)
	results, err = f.p.Apply(ctx, ola, "", ops(3))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if needs(r.Err) != "sign_in_again" {
			t.Errorf("op %d on a refused token: %v, want sign_in_again", i, r.Err)
		}
	}
	if state, code := f.rowState(t, ola); state != "reconnect" || code != "sign_in_again" {
		t.Errorf("the row %s %s, want reconnect sign_in_again", state, code)
	}
}
