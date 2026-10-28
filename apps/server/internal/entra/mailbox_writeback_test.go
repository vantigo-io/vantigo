package entra_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// The mailbox provider's write-back reads (calendar Outlook sync design
// O10.1, O10.2) against the fake Graph: Versions of the default calendar and
// of a secondary one, their pages, links and bound; Events read whole
// through the calendar's own path; their refusals; and the update that
// writes the marker.

// eventsPrefer is the Prefer every request of Events carries.
const eventsPrefer = `outlook.timezone="UTC", outlook.body-content-type="text"`

// marked is an event of the person's calendar carrying this installation's
// marker for key, timed in UTC from start for an hour.
func marked(key string, start time.Time) graphtest.Event {
	return ownEvent(start, func(e *graphtest.Event) {
		e.Properties = map[string]string{vantigoMarker: keyPrefix + key}
	})
}

// listGets counts the GETs, outside a batch, of path among calls.
func listGets(calls []graphtest.Call, path string) int {
	n := 0
	for _, c := range calls {
		if c.Method == http.MethodGet && !c.Batch && c.Path == path {
			n++
		}
	}
	return n
}

// byVersion is versions by their event id.
func byVersion(vs []contracts.MailboxEventVersion) map[string]contracts.MailboxEventVersion {
	out := map[string]contracts.MailboxEventVersion{}
	for _, v := range vs {
		out[v.EventID] = v
	}
	return out
}

// Versions of the default calendar answers this installation's copies from
// from, each with its key, its change key and whether it is of a series —
// never the person's own event, another installation's copy or a copy
// before from — and asks for no more than ids, change keys and types.
func TestMailboxVersions_TheDefaultCalendar(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

	made, err := p.Apply(ctx, mailbox, "", creates(3, at))
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{{Kind: contracts.MailboxOpCreate, Key: "appointment:old",
		Event: timed("Old", at.AddDate(0, 0, -7))}})
	if err != nil || before[0].Err != nil {
		t.Fatalf("an old copy: %+v %v", before, err)
	}
	g.AddEvent(mailbox, "", ownEvent(at))
	g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) {
		e.Properties = map[string]string{vantigoMarker: otherInstallKey + "appointment:1"}
	}))
	g.EditEvent(mailbox, made[2].EventID, func(e *graphtest.Event) { e.Type = "occurrence" })
	edited := g.Events(mailbox, "")[2]

	v, err := p.Versions(ctx, mailbox, "", at.Add(-time.Hour), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Complete || len(v.Events) != 3 {
		t.Fatalf("versions %+v, want the three copies from from, complete", v)
	}
	got := byVersion(v.Events)
	for i, r := range made[:2] {
		if w := (contracts.MailboxEventVersion{EventID: r.EventID, Key: fmt.Sprintf("appointment:%d", i), ChangeKey: r.ChangeKey}); got[r.EventID] != w {
			t.Errorf("copy %d: %+v, want %+v", i, got[r.EventID], w)
		}
	}
	if c := got[made[2].EventID]; !c.Series || c.ChangeKey != edited.ChangeKey || c.ChangeKey == made[2].ChangeKey || c.Key != "appointment:2" {
		t.Errorf("the edited copy: %+v, want a series with the moved change key %q", c, edited.ChangeKey)
	}

	all, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
	if err != nil || len(all.Events) != 4 || byVersion(all.Events)[before[0].EventID].Key != "appointment:old" {
		t.Errorf("no from: %+v %v, want the four copies", all, err)
	}
	for _, c := range g.Calls() {
		if c.Method == http.MethodGet && c.Path == "/v1.0/users/"+mailbox+"/events" &&
			(!strings.Contains(c.Query, "%24select=id%2CchangeKey%2Ctype") || !strings.Contains(c.Query, "%24top=500") ||
				!strings.Contains(c.Query, "VantigoKey")) {
			t.Errorf("a versions read: %s", c.Query)
		}
	}
}

// Versions of a secondary calendar answers every event of its view over
// from..to — the copies and an event the person made there alike — with no
// key, and none outside the window; a window that ends before it starts is
// refused unsent.
func TestMailboxVersions_ASecondaryCalendar(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	made, err := p.Apply(ctx, mailbox, cal, creates(2, at))
	if err != nil {
		t.Fatal(err)
	}
	own := g.AddEvent(mailbox, cal, ownEvent(at.AddDate(0, 0, 1), func(e *graphtest.Event) { e.Type = "seriesMaster" }))
	g.AddEvent(mailbox, cal, ownEvent(deltaTo.AddDate(0, 1, 0)))
	g.AddEvent(mailbox, "", ownEvent(at))

	v, err := p.Versions(ctx, mailbox, cal, deltaFrom, deltaTo)
	if err != nil {
		t.Fatal(err)
	}
	got := byVersion(v.Events)
	if !v.Complete || len(v.Events) != 3 {
		t.Fatalf("versions %+v, want the three events of the window", v)
	}
	for _, r := range made {
		if c := got[r.EventID]; c.Key != "" || c.ChangeKey != r.ChangeKey || c.Series {
			t.Errorf("a copy: %+v, want no key and change key %q", c, r.ChangeKey)
		}
	}
	if c := got[own.ID]; !c.Series || c.ChangeKey != own.ChangeKey || c.Key != "" {
		t.Errorf("the person's series: %+v", c)
	}
	if n := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/calendars/"+cal+"/calendarView"); n != 1 {
		t.Errorf("%d calendarView reads, want one", n)
	}

	before := len(g.Calls())
	if _, err := p.Versions(ctx, mailbox, cal, deltaTo, deltaFrom); err == nil {
		t.Error("a window that ends before it starts: no error")
	}
	if _, err := p.Versions(ctx, mailbox, cal, time.Time{}, deltaTo); err == nil {
		t.Error("a view with no start: no error")
	}
	if _, err := p.Versions(ctx, mailbox, "..", deltaFrom, deltaTo); err == nil {
		t.Error("a calendar id that is no id: no error")
	}
	if n := len(g.Calls()) - before; n != 0 {
		t.Errorf("%d calls for refused reads, want none", n)
	}
}

// Both forms page through @odata.nextLink, five hundred at a time; a link
// of the very list in another case is followed on the provider's own path.
func TestMailboxVersions_Pages(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for i := range 1100 {
		g.AddEvent(mailbox, "", marked(fmt.Sprintf("appointment:%d", i), at.Add(time.Duration(i)*time.Minute)))
	}
	v, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
	if err != nil || !v.Complete || len(v.Events) != 1100 {
		t.Fatalf("1100 copies: %d versions, complete %v, %v", len(v.Events), v.Complete, err)
	}
	if n := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/events"); n != 3 {
		t.Errorf("%d pages for 1100 copies, want three of 500", n)
	}

	cal := g.AddCalendar(mailbox, "Vantigo")
	for i := range 600 {
		g.AddEvent(mailbox, cal.ID, ownEvent(at.Add(time.Duration(i)*time.Minute)))
	}
	g.Configure(func(g *graphtest.FakeGraph) {
		g.NextLinks = func(link string) string {
			return strings.Replace(strings.Replace(link, "/calendarView?", "/calendarview?", 1), mailbox, strings.ToUpper(mailbox), 1)
		}
	})
	view, err := p.Versions(ctx, mailbox, cal.ID, deltaFrom, deltaTo)
	if err != nil || !view.Complete || len(view.Events) != 600 {
		t.Fatalf("600 events in the view: %d versions, complete %v, %v", len(view.Events), view.Complete, err)
	}
	if n := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/calendars/"+cal.ID+"/calendarView"); n != 2 {
		t.Errorf("%d view pages on the provider's path, want two", n)
	}
	for _, c := range g.Calls() {
		if strings.Contains(c.Path, "calendarview") || strings.Contains(c.Path, strings.ToUpper(mailbox)) {
			t.Errorf("a request sent on the link's path: %s", c.Path)
		}
	}
}

// A next link that is not a page of the very list — another mailbox's, off
// Graph, another list — fails the read with no sentinel, unsent.
func TestMailboxVersions_AForeignNextLinkIsRefused(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for i := range 501 {
		g.AddEvent(mailbox, "", marked(fmt.Sprintf("appointment:%d", i), at.Add(time.Duration(i)*time.Minute)))
	}
	other := uuid.NewString()
	for name, rewrite := range map[string]func(string) string{
		"another mailbox's": func(l string) string { return strings.Replace(l, mailbox, other, 1) },
		"off Graph":         func(l string) string { return strings.Replace(l, "graph.microsoft.com", "graph.example.com", 1) },
		"another list":      func(l string) string { return strings.Replace(l, "/events?", "/calendar/events?", 1) },
		"beta":              func(l string) string { return strings.Replace(l, "/v1.0/", "/beta/", 1) },
		"an encoded path":   func(l string) string { return strings.Replace(l, "/events?", "/event%73?", 1) },
		"a fragment":        func(l string) string { return l + "#x" },
		"no query":          func(l string) string { return strings.SplitN(l, "?", 2)[0] },
	} {
		g.Configure(func(g *graphtest.FakeGraph) { g.NextLinks = rewrite })
		before := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/events")
		_, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, s := range []error{contracts.ErrMailboxUnavailable, contracts.ErrMailboxUnknown, contracts.ErrMailboxDenied, contracts.ErrMailboxGone} {
			if errors.Is(err, s) {
				t.Errorf("%s: %v, want a plain failure", name, err)
			}
		}
		if n := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/events") - before; n != 1 {
			t.Errorf("%s: %d reads, want the first page alone", name, n)
		}
		for _, c := range g.Calls() {
			if strings.Contains(c.Path, other) || (c.Host != graphtest.GraphHost && c.Host != graphtest.TokenHost) {
				t.Errorf("%s: the link was fetched: %+v", name, c)
			}
		}
	}
}

// At most 5000 versions: a list longer is answered cut, not complete, and
// no page past the bound is read.
func TestMailboxVersions_TheBound(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for i := range contracts.MailboxVersionsMax {
		g.AddEvent(mailbox, "", marked(fmt.Sprintf("appointment:%d", i), at.Add(time.Duration(i)*time.Minute)))
	}
	v, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
	if err != nil || !v.Complete || len(v.Events) != contracts.MailboxVersionsMax {
		t.Fatalf("5000 copies: %d versions, complete %v, %v; want all, complete", len(v.Events), v.Complete, err)
	}
	g.AddEvent(mailbox, "", marked("appointment:last", at.AddDate(1, 0, 0)))
	before := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/events")
	v, err = p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
	if err != nil || v.Complete || len(v.Events) != contracts.MailboxVersionsMax {
		t.Fatalf("5001 copies: %d versions, complete %v, %v; want 5000, cut", len(v.Events), v.Complete, err)
	}
	if n := listGets(g.Calls(), "/v1.0/users/"+mailbox+"/events") - before; n != 10 {
		t.Errorf("%d pages, want the ten of the bound", n)
	}
}

// An event whose id is no id is left out, the rest of the page answered.
func TestMailboxVersions_AnEventWithNoIDIsLeftOut(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	g.AddEvent(mailbox, "", marked("appointment:1", at))
	bad := marked("appointment:2", at)
	bad.ID = "AAMk" + strings.Repeat("x", 600)
	g.AddEvent(mailbox, "", bad)
	v, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{})
	if err != nil || len(v.Events) != 1 || v.Events[0].Key != "appointment:1" || !v.Complete {
		t.Errorf("versions %+v %v, want the one event with an id", v, err)
	}
}

// Both reads are refused under cleanup and for an account the module does
// not manage, and through A's refusals for a mailbox outside the scope or
// gone.
func TestMailboxWriteBack_Refusals(t *testing.T) {
	ctx := context.Background()
	h, g, o := newMailboxHarness(t)
	p := mailboxProvider(h)
	denied := mailboxUser(t, h, g, func(u *graphtest.User) { u.MailboxDenied = true })
	noMailbox := mailboxUser(t, h, g, func(u *graphtest.User) { u.NoMailbox = true })
	missing := addAccount(t, h, "E900", "gone@nordlys.no").String()
	stranger := uuid.New()
	g.AddUser(graphtest.User{ID: stranger.String(), UserPrincipalName: "stranger@nordlys.no", AccountEnabled: true})

	for name, c := range map[string]struct {
		mailbox string
		want    error
	}{
		"denied": {denied, contracts.ErrMailboxDenied}, "no mailbox": {noMailbox, contracts.ErrMailboxGone},
		"no such user": {missing, contracts.ErrMailboxGone}, "unknown account": {stranger.String(), contracts.ErrMailboxUnknown},
	} {
		if _, err := p.Versions(ctx, c.mailbox, "", time.Time{}, time.Time{}); !errors.Is(err, c.want) {
			t.Errorf("Versions, %s: %v, want %v", name, err, c.want)
		}
		if _, err := p.Versions(ctx, c.mailbox, "AAMkcal=", deltaFrom, deltaTo); !errors.Is(err, c.want) {
			t.Errorf("Versions of a calendar, %s: %v, want %v", name, err, c.want)
		}
		read, err := p.Events(ctx, c.mailbox, "", []string{"AAMkone="})
		if name == "denied" {
			// Inside the $batch a 403 is that event's alone: skipped,
			// neither answered nor gone — Versions answers the refusal.
			if err != nil || len(read.Events) != 0 || len(read.Gone) != 0 {
				t.Errorf("Events, denied: %+v %v, want nothing read and no error", read, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("Events, %s: %v, want %v", name, err, c.want)
		}
	}

	mailbox := mailboxUser(t, h, g)
	setCalendarSync(t, o, false)
	if st, _ := p.Status(ctx); st.State != contracts.MailboxCleanup {
		t.Fatalf("Status = %+v, want cleanup", st)
	}
	before := len(mailboxCalls(g.Calls()))
	if _, err := p.Versions(ctx, mailbox, "", time.Time{}, time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Versions in cleanup: %v, want ErrMailboxUnavailable", err)
	}
	if _, err := p.Events(ctx, mailbox, "", []string{"AAMkone="}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Events in cleanup: %v, want ErrMailboxUnavailable", err)
	}
	if n := len(mailboxCalls(g.Calls())) - before; n != 0 {
		t.Errorf("%d calls in cleanup, want none", n)
	}
}

// byState is states by their event id.
func byState(states []contracts.MailboxEventState) map[string]contracts.MailboxEventState {
	out := map[string]contracts.MailboxEventState{}
	for _, s := range states {
		out[s.EventID] = s
	}
	return out
}

// Events reads the default calendar's events whole, in the order asked:
// a copy with its key and what the person changed in it, another
// installation's copy Foreign, the person's own event with no key, a
// meeting, an HTML body answered as text, a series, a cancelled meeting, an
// all-day event's dates; an event deleted or in another calendar is not
// answered. Each request carries the Prefer headers.
func TestMailboxEvents_TheDefaultCalendar(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

	copyEvent := timed("Standup", at)
	copyEvent.Location, copyEvent.Private = "Rom 1", true
	made, err := p.Apply(ctx, mailbox, "", []contracts.MailboxOp{
		{Kind: contracts.MailboxOpCreate, Key: "appointment:42", Event: copyEvent},
		{Kind: contracts.MailboxOpCreate, Key: "appointment:43", Event: timed("Gone", at)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ours := made[0].EventID
	g.EditEvent(mailbox, ours, func(e *graphtest.Event) {
		e.Subject = "Standup, moved"
		e.Start.DateTime, e.End.DateTime = "2026-11-03T10:00:00", "2026-11-03T10:30:00"
		e.ShowAs = "tentative"
	})
	g.RemoveEvent(mailbox, made[1].EventID)
	others := g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) {
		e.Properties = map[string]string{vantigoMarker: otherInstallKey + "appointment:42"}
	}))
	own := g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) { e.Sensitivity = "confidential"; e.ShowAs = "oof" }))
	meeting := g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) {
		e.Attendees = []string{"kari@nordlys.no", "ola@example.com"}
		e.IsCancelled = true
	}))
	series := g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) { e.Type = "exception" }))
	allDay := g.AddEvent(mailbox, "", graphtest.Event{Subject: "Holiday", IsAllDay: true, ShowAs: "oof",
		Start: graphtest.DateTimeZone{DateTime: "2026-11-10T00:00:00", TimeZone: "W. Europe Standard Time"},
		End:   graphtest.DateTimeZone{DateTime: "2026-11-13T00:00:00", TimeZone: "W. Europe Standard Time"}})
	family := g.AddCalendar(mailbox, "Family")
	elsewhere := g.AddEvent(mailbox, family.ID, ownEvent(at))

	ids := []string{allDay.ID, ours, made[1].EventID, others.ID, own.ID, meeting.ID, series.ID, elsewhere.ID}
	read, err := p.Events(ctx, mailbox, "", ids)
	if err != nil {
		t.Fatal(err)
	}
	states := read.Events
	if len(read.Gone) != 2 || read.Gone[0] != made[1].EventID || read.Gone[1] != elsewhere.ID {
		t.Errorf("gone %v, want the deleted copy and the event of another calendar", read.Gone)
	}
	wantOrder := []string{allDay.ID, ours, others.ID, own.ID, meeting.ID, series.ID}
	if len(states) != len(wantOrder) {
		t.Fatalf("%d states, want %d: %+v", len(states), len(wantOrder), states)
	}
	for i, id := range wantOrder {
		if states[i].EventID != id {
			t.Errorf("state %d is %s, want %s", i, states[i].EventID, id)
		}
	}
	got := byState(states)
	current := g.Events(mailbox, "")[0]
	if s := got[ours]; s.Key != "appointment:42" || s.Foreign || s.ChangeKey != current.ChangeKey || s.Meeting || s.Series ||
		s.Cancelled || !s.TextBody || s.Event.Subject != "Standup, moved" || s.Event.Body != "Kept by Vantigo" ||
		s.Event.Location != "Rom 1" || !s.Event.Private || s.Event.ShowAs != contracts.ShowAsTentative || s.Event.AllDay ||
		!s.Event.StartsAt.Equal(at.Add(time.Hour)) || !s.Event.EndsAt.Equal(at.Add(90*time.Minute)) ||
		s.Event.StartsAt.Location() != time.UTC || s.Event.TimeZone != "" {
		t.Errorf("the copy: %+v", s)
	}
	if s := got[others.ID]; !s.Foreign || s.Key != "" {
		t.Errorf("another installation's copy: %+v, want Foreign with no key", s)
	}
	if s := got[own.ID]; s.Foreign || s.Key != "" || !s.Event.Private || s.Event.ShowAs != contracts.ShowAsOOF ||
		!s.TextBody || s.Event.Body != "Confidential" || s.Event.Subject != "Salary review" {
		t.Errorf("the person's own event, its HTML body asked as text: %+v", s)
	}
	if s := got[meeting.ID]; !s.Meeting || !s.Cancelled {
		t.Errorf("the meeting: %+v", s)
	}
	if s := got[series.ID]; !s.Series {
		t.Errorf("the exception: %+v", s)
	}
	if s := got[allDay.ID]; !s.Event.AllDay || !s.Event.StartDate.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) ||
		!s.Event.EndDate.Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)) || !s.Event.StartsAt.IsZero() {
		t.Errorf("the all-day event: %+v, want 10–12 November inclusive", s.Event)
	}

	reads := 0
	for _, c := range g.Calls() {
		if !c.Batch || c.Method != http.MethodGet || !strings.Contains(c.Path, "/calendar/events/") {
			continue
		}
		reads++
		if c.Prefer != eventsPrefer || !strings.Contains(c.Query, "attendees") || !strings.Contains(c.Query, "VantigoKey") ||
			!strings.Contains(c.Query, "originalStartTimeZone") {
			t.Errorf("a read: prefer %q, query %s", c.Prefer, c.Query)
		}
	}
	if reads != len(ids) {
		t.Errorf("%d reads through the default calendar's path, want %d", reads, len(ids))
	}

	// A body Graph answers as HTML whatever the Prefer is not text.
	g.Configure(func(g *graphtest.FakeGraph) { g.HTMLBodies = true })
	read, err = p.Events(ctx, mailbox, "", []string{own.ID})
	states = read.Events
	if err != nil || len(states) != 1 || states[0].TextBody || states[0].Event.Body != "<p>Confidential</p>" {
		t.Errorf("an HTML body: %+v %v, want TextBody false", states, err)
	}

	if read, err := p.Events(ctx, mailbox, "", nil); err != nil || read.Events == nil || len(read.Events) != 0 || read.Gone == nil || len(read.Gone) != 0 {
		t.Errorf("no ids: %+v %v, want none", read, err)
	}
}

// Events of a secondary calendar reads through its path: an event of the
// default calendar is not answered there.
func TestMailboxEvents_ASecondaryCalendar(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	made, err := p.Apply(ctx, mailbox, cal, creates(1, at))
	if err != nil {
		t.Fatal(err)
	}
	mine := g.AddEvent(mailbox, cal, ownEvent(at))
	inDefault := g.AddEvent(mailbox, "", ownEvent(at))
	read, err := p.Events(ctx, mailbox, cal, []string{made[0].EventID, inDefault.ID, mine.ID})
	if err != nil {
		t.Fatal(err)
	}
	states := read.Events
	if len(read.Gone) != 1 || read.Gone[0] != inDefault.ID {
		t.Errorf("gone %v, want the default calendar's event", read.Gone)
	}
	if len(states) != 2 || states[0].Key != "appointment:0" || states[1].EventID != mine.ID || states[1].Key != "" {
		t.Errorf("states %+v, want the copy and the person's event of the calendar", states)
	}
	for _, c := range g.Calls() {
		if c.Batch && c.Method == http.MethodGet && strings.Contains(c.Path, "/events/") &&
			!strings.HasPrefix(c.Path, "/v1.0/users/"+mailbox+"/calendars/"+cal+"/events/") {
			t.Errorf("a read off the calendar's path: %s", c.Path)
		}
	}
}

// Events across batches of twenty; a throttled read is sent again once,
// then the call fails; more than a hundred ids, or one that is no id, is
// refused unsent; a 403 inside the batch skips that event, a 403 of the
// $batch itself is ErrMailboxDenied.
func TestMailboxEvents_BatchesAndRefusals(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	made, err := p.Apply(ctx, mailbox, "", creates(22, at))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(made))
	for i, r := range made {
		ids[i] = r.EventID
	}
	before := batches(g.Calls())
	g.ThrottleBatch(1)
	read, err := p.Events(ctx, mailbox, "", ids)
	states := read.Events
	if err != nil || len(states) != 22 {
		t.Fatalf("22 copies and a throttle: %d states, %v", len(states), err)
	}
	for i, s := range states {
		if s.EventID != ids[i] || s.Key != fmt.Sprintf("appointment:%d", i) {
			t.Errorf("state %d: %s %q", i, s.EventID, s.Key)
		}
	}
	if n := batches(g.Calls()) - before; n != 3 {
		t.Errorf("%d batches for 22 ids and a throttle, want two and the retry", n)
	}

	g.ThrottleBatch(2)
	if _, err := p.Events(ctx, mailbox, "", ids[:1]); err == nil {
		t.Error("a read throttled twice: no error")
	}

	before2 := len(g.Calls())
	if _, err := p.Events(ctx, mailbox, "", make([]string, contracts.MailboxMarkedMax+1)); err == nil {
		t.Error("101 ids: no error")
	}
	if _, err := p.Events(ctx, mailbox, "", []string{ids[0], ".."}); err == nil {
		t.Error("a dot segment: no error")
	}
	if _, err := p.Events(ctx, mailbox, "a b", ids[:1]); err == nil {
		t.Error("a calendar id with a space: no error")
	}
	if n := len(g.Calls()) - before2; n != 0 {
		t.Errorf("%d calls for refused ids, want none", n)
	}

	// A 403 of one event inside the $batch skips it alone; the rest are
	// read.
	g.Configure(func(g *graphtest.FakeGraph) { g.ForbiddenEvents = map[string]bool{ids[1]: true} })
	read, err = p.Events(ctx, mailbox, "", ids[:3])
	if err != nil || len(read.Events) != 2 || read.Events[0].EventID != ids[0] || read.Events[1].EventID != ids[2] || len(read.Gone) != 0 {
		t.Errorf("a 403 of one event: %+v %v, want the other two read, none gone", read, err)
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.ForbiddenEvents = nil })

	// A refusal of the $batch POST itself is the mailbox's.
	g.FailNext(http.StatusForbidden)
	if _, err := p.Events(ctx, mailbox, "", ids[:1]); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("the $batch refused 403: %v, want ErrMailboxDenied", err)
	}
}

// A $batch whose answer does not decode, or is larger than the client
// reads, is sent again an id at a time; the id whose read alone still
// fails is skipped, neither answered nor gone, and the rest are read.
func TestMailboxEvents_AnUnreadableBatchIsReadOneByOne(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	made, err := p.Apply(ctx, mailbox, "", creates(4, at))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(made))
	for i, r := range made {
		ids[i] = r.EventID
	}
	gone := g.AddEvent(mailbox, "", ownEvent(at)).ID
	g.RemoveEvent(mailbox, gone)
	for name, set := range map[string]func(g *graphtest.FakeGraph){
		"not JSON":  func(g *graphtest.FakeGraph) { g.GarbledEvents = map[string]bool{ids[1]: true} },
		"too large": func(g *graphtest.FakeGraph) { g.OversizedEvents = map[string]bool{ids[2]: true} },
	} {
		g.Configure(func(g *graphtest.FakeGraph) { g.GarbledEvents, g.OversizedEvents = nil, nil })
		g.Configure(set)
		before := batches(g.Calls())
		read, err := p.Events(ctx, mailbox, "", append(append([]string{}, ids...), gone))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(read.Events) != 3 || len(read.Gone) != 1 || read.Gone[0] != gone {
			t.Errorf("%s: %d read, gone %v; want three read and the deleted one gone", name, len(read.Events), read.Gone)
		}
		if n := batches(g.Calls()) - before; n != 1+len(ids)+1 {
			t.Errorf("%s: %d batches, want the one and one per id", name, n)
		}
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.GarbledEvents, g.OversizedEvents = nil, nil })
}

// An update naming a Key writes the marker — an event the calendar took in
// is tagged by the push that rewrites it — and one without leaves the
// event's properties as they are.
func TestMailboxApply_AnUpdateWithAKeyCarriesTheMarker(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	cal, err := p.EnsureCalendar(ctx, mailbox, "", "Vantigo")
	if err != nil {
		t.Fatal(err)
	}
	taken := g.AddEvent(mailbox, cal, ownEvent(at))
	untouched := g.AddEvent(mailbox, cal, ownEvent(at))
	results, err := p.Apply(ctx, mailbox, cal, []contracts.MailboxOp{
		{Kind: contracts.MailboxOpUpdate, EventID: taken.ID, Key: "appointment:7", Event: timed("Taken", at)},
		{Kind: contracts.MailboxOpUpdate, EventID: untouched.ID, Event: timed("Updated", at)},
	})
	if err != nil || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("the updates: %+v %v", results, err)
	}
	for _, e := range g.Events(mailbox, cal) {
		switch e.ID {
		case taken.ID:
			if e.Properties[vantigoMarker] != keyPrefix+"appointment:7" || e.Subject != "Taken" {
				t.Errorf("the update with a key: %+v", e)
			}
			if results[0].ChangeKey != e.ChangeKey {
				t.Errorf("the update's change key %q, the event's %q", results[0].ChangeKey, e.ChangeKey)
			}
		case untouched.ID:
			if _, sent := e.LastWrite["singleValueExtendedProperties"]; sent || len(e.Properties) != 0 {
				t.Errorf("the update without a key wrote a marker: %+v", e.Properties)
			}
		}
	}
	read, err := p.Events(ctx, mailbox, cal, []string{taken.ID})
	if err != nil || len(read.Events) != 1 || read.Events[0].Key != "appointment:7" {
		t.Errorf("the tagged event read back: %+v %v", read, err)
	}
}
