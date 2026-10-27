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

// The mailbox provider's reads (calendar Outlook sync design O9.1–O9.4)
// against the fake Graph: Delta's fresh and linked reads, its pages and
// their bound, its refusals and its link check; Marked.

// The delta's window in these tests.
var (
	deltaFrom = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	deltaTo   = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
)

// ownEvent is an event of the person's own, timed in UTC from start for an
// hour, with words the provider must never need.
func ownEvent(start time.Time, edit ...func(e *graphtest.Event)) graphtest.Event {
	e := graphtest.Event{
		Subject: "Salary review", BodyType: "html", Body: "<p>Confidential</p>", ShowAs: "busy",
		Start: graphtest.DateTimeZone{DateTime: start.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
		End:   graphtest.DateTimeZone{DateTime: start.UTC().Add(time.Hour).Format("2006-01-02T15:04:05"), TimeZone: "UTC"},
	}
	for _, fn := range edit {
		fn(&e)
	}
	return e
}

// deltaPages counts the GETs of the user's delta among calls.
func deltaPages(calls []graphtest.Call, user string) int {
	n := 0
	for _, c := range calls {
		if c.Method == http.MethodGet && c.Path == "/v1.0/users/"+user+"/calendarView/delta" {
			n++
		}
	}
	return n
}

// byEvent is changes by their event id.
func byEvent(changes []contracts.MailboxChange) map[string]contracts.MailboxChange {
	out := map[string]contracts.MailboxChange{}
	for _, c := range changes {
		out[c.EventID] = c
	}
	return out
}

// A fresh delta answers every event of the window — the person's, with its
// times and showAs, series and cancelled marked, an all-day event's dates
// from its midnight in its zone, the copies the provider wrote too — and
// none outside it; the link then answers the changes since: an edit, a
// deletion and a move out of the window removed, a new event; then nothing.
func TestMailboxDelta_FreshAndLinked(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

	busy := g.AddEvent(mailbox, "", ownEvent(at))
	tentative := g.AddEvent(mailbox, "", ownEvent(at.Add(2*time.Hour), func(e *graphtest.Event) { e.ShowAs = "tentative" }))
	free := g.AddEvent(mailbox, "", ownEvent(at.Add(4*time.Hour), func(e *graphtest.Event) { e.ShowAs = "free" }))
	unknown := g.AddEvent(mailbox, "", ownEvent(at.Add(5*time.Hour), func(e *graphtest.Event) { e.ShowAs = "unknown" }))
	cancelled := g.AddEvent(mailbox, "", ownEvent(at.AddDate(0, 0, 1), func(e *graphtest.Event) { e.IsCancelled = true }))
	occurrence := g.AddEvent(mailbox, "", ownEvent(at.AddDate(0, 0, 2), func(e *graphtest.Event) { e.Type = "occurrence" }))
	exception := g.AddEvent(mailbox, "", ownEvent(at.AddDate(0, 0, 3), func(e *graphtest.Event) { e.Type = "exception" }))
	// An all-day absence laid at midnight in Oslo's Windows zone: under UTC
	// Graph answers 23:00 the day before, rounded back to the day.
	allDay := g.AddEvent(mailbox, "", graphtest.Event{Subject: "Holiday", IsAllDay: true, ShowAs: "oof",
		Start: graphtest.DateTimeZone{DateTime: "2026-11-10T00:00:00", TimeZone: "W. Europe Standard Time"},
		End:   graphtest.DateTimeZone{DateTime: "2026-11-13T00:00:00", TimeZone: "W. Europe Standard Time"}})
	g.AddEvent(mailbox, "", ownEvent(deltaTo.AddDate(0, 1, 0)))
	cal := g.AddCalendar(mailbox, "Family")
	g.AddEvent(mailbox, cal.ID, ownEvent(at))
	copies, err := p.Apply(ctx, mailbox, "", creates(1, at.AddDate(0, 0, 5)))
	if err != nil || copies[0].Err != nil {
		t.Fatalf("a copy: %+v %v", copies, err)
	}

	d, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Done || !strings.HasPrefix(d.Link, "https://graph.microsoft.com/v1.0/users/"+mailbox+"/calendarView/delta?") {
		t.Fatalf("the fresh delta: Done %v, link %q", d.Done, d.Link)
	}
	got := byEvent(d.Changes)
	if len(d.Changes) != 9 || len(got) != 9 {
		t.Fatalf("%d changes, want the nine in the window of the default calendar: %+v", len(d.Changes), d.Changes)
	}
	if c := got[busy.ID]; c.AllDay || c.Series || c.Cancelled || c.Removed || c.ShowAs != contracts.ShowAsBusy ||
		!c.StartsAt.Equal(at) || !c.EndsAt.Equal(at.Add(time.Hour)) || c.StartsAt.Location() != time.UTC {
		t.Errorf("the busy event: %+v", c)
	}
	for id, want := range map[string]string{tentative.ID: contracts.ShowAsTentative, free.ID: contracts.ShowAsFree, unknown.ID: ""} {
		if c := got[id]; c.ShowAs != want {
			t.Errorf("showAs %q, want %q", c.ShowAs, want)
		}
	}
	if !got[cancelled.ID].Cancelled || !got[occurrence.ID].Series || !got[exception.ID].Series || got[busy.ID].Series {
		t.Errorf("cancelled %+v, occurrence %+v, exception %+v", got[cancelled.ID], got[occurrence.ID], got[exception.ID])
	}
	if c := got[allDay.ID]; !c.AllDay || c.ShowAs != contracts.ShowAsOOF ||
		!c.StartDate.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) || !c.EndDate.Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)) ||
		!c.StartsAt.IsZero() {
		t.Errorf("the all-day event: %+v, want 10–12 November inclusive", c)
	}
	if _, ok := got[copies[0].EventID]; !ok {
		t.Error("the copy the provider wrote is not in the delta")
	}
	if n := deltaPages(g.Calls(), mailbox); n != 1 {
		t.Errorf("%d pages for nine events, want one of fifty", n)
	}

	// The changes since.
	g.EditEvent(mailbox, tentative.ID, func(e *graphtest.Event) { e.ShowAs = "busy" })
	g.EditEvent(mailbox, busy.ID, func(e *graphtest.Event) {
		e.Start.DateTime, e.End.DateTime = "2027-03-01T09:00:00", "2027-03-01T10:00:00"
	})
	g.RemoveEvent(mailbox, free.ID)
	added := g.AddEvent(mailbox, "", ownEvent(at.AddDate(0, 0, 7)))
	next, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, d.Link)
	if err != nil {
		t.Fatal(err)
	}
	got = byEvent(next.Changes)
	if !next.Done || len(next.Changes) != 4 {
		t.Fatalf("the linked delta: %+v, want four changes, done", next)
	}
	if c := got[tentative.ID]; c.Removed || c.ShowAs != contracts.ShowAsBusy {
		t.Errorf("the edit: %+v", c)
	}
	if c := got[busy.ID]; c != (contracts.MailboxChange{EventID: busy.ID, Removed: true}) {
		t.Errorf("the move out of the window: %+v, want removed alone", c)
	}
	if c := got[free.ID]; c != (contracts.MailboxChange{EventID: free.ID, Removed: true}) {
		t.Errorf("the deletion: %+v, want removed alone", c)
	}
	if c := got[added.ID]; c.Removed || !c.StartsAt.Equal(at.AddDate(0, 0, 7)) {
		t.Errorf("the new event: %+v", c)
	}

	last, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, next.Link)
	if err != nil || !last.Done || len(last.Changes) != 0 || last.Changes == nil {
		t.Errorf("a delta with nothing new: %+v %v, want done and empty", last, err)
	}

	// A fresh delta needs a window.
	if _, err := p.Delta(ctx, mailbox, deltaTo, deltaFrom, ""); err == nil {
		t.Error("a fresh delta whose window ends before it starts: no error")
	}
}

// Pages of fifty (the Prefer the provider sends), and at most forty a call:
// a call cut short answers the next link, not done, and the next call takes
// it up.
func TestMailboxDelta_PagesAndTheirBound(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for i := range 120 {
		g.AddEvent(mailbox, "", ownEvent(at.Add(time.Duration(i)*time.Hour)))
	}
	d, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, "")
	if err != nil || !d.Done || len(d.Changes) != 120 {
		t.Fatalf("120 events: %d changes, done %v, %v", len(d.Changes), d.Done, err)
	}
	if n := deltaPages(g.Calls(), mailbox); n != 3 {
		t.Errorf("%d pages for 120 events, want three of fifty", n)
	}

	g.Configure(func(g *graphtest.FakeGraph) { g.DeltaPageSize = 2 })
	before := deltaPages(g.Calls(), mailbox)
	cut, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, "")
	if err != nil {
		t.Fatal(err)
	}
	if cut.Done || len(cut.Changes) != 80 || !strings.Contains(cut.Link, "$skiptoken=") {
		t.Fatalf("a delta of 60 pages: done %v, %d changes, link %q; want cut at 40 pages", cut.Done, len(cut.Changes), cut.Link)
	}
	if n := deltaPages(g.Calls(), mailbox) - before; n != 40 {
		t.Errorf("%d pages in one call, want 40", n)
	}
	rest, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, cut.Link)
	if err != nil || !rest.Done || len(rest.Changes) != 40 || !strings.Contains(rest.Link, "$deltatoken=") {
		t.Errorf("the rest: done %v, %d changes, link %q, %v", rest.Done, len(rest.Changes), rest.Link, err)
	}
	if cut.Changes[0].EventID == rest.Changes[0].EventID {
		t.Error("the next call answered the first page again")
	}
}

// A link Graph no longer takes is ErrMailboxDeltaExpired — a 410, or a 400
// syncStateNotFound — and a fresh delta starts again.
func TestMailboxDelta_Expired(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	g.AddEvent(mailbox, "", ownEvent(time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)))
	for _, status := range []int{http.StatusGone, http.StatusBadRequest} {
		d, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, "")
		if err != nil {
			t.Fatal(err)
		}
		g.ExpireDelta(mailbox, status)
		if _, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, d.Link); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("a link refused %d: %v, want ErrMailboxDeltaExpired", status, err)
		}
		if again, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, ""); err != nil || len(again.Changes) != 1 {
			t.Errorf("a fresh delta after %d: %+v %v", status, again, err)
		}
	}
	// A link the fake never issued is refused by its code too.
	unknown := "https://graph.microsoft.com/v1.0/users/" + mailbox + "/calendarView/delta?$deltatoken=never"
	if _, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, unknown); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("an unknown token: %v, want ErrMailboxDeltaExpired", err)
	}
}

// A link that is not this mailbox's delta — another mailbox's, off Graph,
// another path, another case — is refused with a failure, no sentinel, and
// never fetched.
func TestMailboxDelta_AForeignLinkIsRefusedUnsent(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	other := mailboxUser(t, h, g)
	d, err := p.Delta(ctx, other, deltaFrom, deltaTo, "")
	if err != nil {
		t.Fatal(err)
	}
	before := len(g.Calls())
	for name, link := range map[string]string{
		"another mailbox's":   d.Link,
		"off Graph":           "https://graph.example.com/v1.0/users/" + mailbox + "/calendarView/delta?$deltatoken=T1",
		"plain http":          "http://graph.microsoft.com/v1.0/users/" + mailbox + "/calendarView/delta?$deltatoken=T1",
		"beta":                "https://graph.microsoft.com/beta/users/" + mailbox + "/calendarView/delta?$deltatoken=T1",
		"the events":          "https://graph.microsoft.com/v1.0/users/" + mailbox + "/events?$top=10",
		"another case":        "https://graph.microsoft.com/v1.0/users/" + mailbox + "/calendarview/delta?$deltatoken=T1",
		"an upper-case oid":   "https://graph.microsoft.com/v1.0/users/" + strings.ToUpper(mailbox) + "/calendarView/delta?$deltatoken=T1",
		"a path after it":     "https://graph.microsoft.com/v1.0/users/" + mailbox + "/calendarView/delta/../../x?$deltatoken=T1",
		"a user inside":       "https://graph.microsoft.com@evil.example/v1.0/users/" + mailbox + "/calendarView/delta?x",
		"no link of Graph's":  "not a link",
		"another oid's query": "https://graph.microsoft.com/v1.0/users/" + uuid.NewString() + "/calendarView/delta?$deltatoken=T1",
	} {
		_, err := p.Delta(ctx, mailbox, time.Time{}, time.Time{}, link)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, s := range []error{contracts.ErrMailboxDeltaExpired, contracts.ErrMailboxUnavailable, contracts.ErrMailboxUnknown,
			contracts.ErrMailboxDenied, contracts.ErrMailboxGone} {
			if errors.Is(err, s) {
				t.Errorf("%s: %v, want a plain failure", name, err)
			}
		}
	}
	if calls := g.Calls()[before:]; len(calls) != 0 {
		t.Errorf("a foreign link was fetched: %+v", calls)
	}
}

// Both reads are refused under cleanup and while unavailable, for an
// account the module does not manage, and through A's refusals for a
// mailbox outside the scope or gone.
func TestMailboxDelta_Refusals(t *testing.T) {
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
		if _, err := p.Delta(ctx, c.mailbox, deltaFrom, deltaTo, ""); !errors.Is(err, c.want) {
			t.Errorf("Delta, %s: %v, want %v", name, err, c.want)
		}
		if _, err := p.Marked(ctx, c.mailbox, []string{"AAMkone="}); !errors.Is(err, c.want) {
			t.Errorf("Marked, %s: %v, want %v", name, err, c.want)
		}
	}

	mailbox := mailboxUser(t, h, g)
	setCalendarSync(t, o, false)
	if st, _ := p.Status(ctx); st.State != contracts.MailboxCleanup {
		t.Fatalf("Status = %+v, want cleanup", st)
	}
	before := len(mailboxCalls(g.Calls()))
	if _, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, ""); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Delta in cleanup: %v, want ErrMailboxUnavailable", err)
	}
	if _, err := p.Marked(ctx, mailbox, []string{"AAMkone="}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Marked in cleanup: %v, want ErrMailboxUnavailable", err)
	}
	h.Exec(t, `UPDATE entra.credentials SET revoked_at = $1`, h.Now())
	if _, err := p.Delta(ctx, mailbox, deltaFrom, deltaTo, ""); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Delta while unavailable: %v, want ErrMailboxUnavailable", err)
	}
	if n := len(mailboxCalls(g.Calls())) - before + deltaPages(g.Calls(), mailbox); n != 0 {
		t.Errorf("%d calls in cleanup or unavailable, want none", n)
	}
}

// Marked answers this installation's copies with their keys, in the order
// asked, across batches of twenty; never the person's own event, another
// installation's copy, or an id gone; a throttled read is sent again; more
// than a hundred ids or one that is no id is refused unsent.
func TestMailboxMarked(t *testing.T) {
	ctx := context.Background()
	h, g, _ := newMailboxHarness(t)
	p := mailboxProvider(h)
	mailbox := mailboxUser(t, h, g)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

	made, err := p.Apply(ctx, mailbox, "", creates(22, at))
	if err != nil {
		t.Fatal(err)
	}
	own := g.AddEvent(mailbox, "", ownEvent(at))
	others := g.AddEvent(mailbox, "", ownEvent(at, func(e *graphtest.Event) {
		e.Properties = map[string]string{vantigoMarker: otherInstallKey + "appointment:1"}
	}))
	gone := made[3].EventID
	g.RemoveEvent(mailbox, gone)

	ids := []string{own.ID, made[21].EventID, others.ID, gone}
	for i, r := range made[:21] {
		if i != 3 {
			ids = append(ids, r.EventID)
		}
	}
	before := batches(g.Calls())
	g.ThrottleBatch(1)
	refs, err := p.Marked(ctx, mailbox, ids)
	if err != nil {
		t.Fatal(err)
	}
	want := []contracts.MailboxEventRef{{EventID: made[21].EventID, Key: "appointment:21"}}
	for i, r := range made[:21] {
		if i != 3 {
			want = append(want, contracts.MailboxEventRef{EventID: r.EventID, Key: fmt.Sprintf("appointment:%d", i)})
		}
	}
	if len(refs) != len(want) {
		t.Fatalf("%d refs, want %d: %+v", len(refs), len(want), refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("ref %d: %+v, want %+v", i, refs[i], want[i])
		}
	}
	if n := batches(g.Calls()) - before; n != 3 {
		t.Errorf("%d batches for 24 ids and a throttle, want two and the retry", n)
	}
	for _, c := range g.Calls() {
		if c.Batch && c.Method == http.MethodGet && (!strings.Contains(c.Query, "%24select=id") || !strings.Contains(c.Query, "VantigoKey")) {
			t.Errorf("a read without the select and the marker's expand: %s", c.Query)
		}
	}

	if refs, err := p.Marked(ctx, mailbox, nil); err != nil || refs == nil || len(refs) != 0 {
		t.Errorf("no ids: %+v %v, want none", refs, err)
	}
	before = len(g.Calls())
	if _, err := p.Marked(ctx, mailbox, make([]string, contracts.MailboxMarkedMax+1)); err == nil {
		t.Error("101 ids: no error")
	}
	if _, err := p.Marked(ctx, mailbox, []string{own.ID, ".."}); err == nil {
		t.Error("a dot segment: no error")
	}
	if _, err := p.Marked(ctx, mailbox, []string{"a b"}); err == nil {
		t.Error("an id with a space: no error")
	}
	if n := len(g.Calls()) - before; n != 0 {
		t.Errorf("%d calls for refused ids, want none", n)
	}
}
