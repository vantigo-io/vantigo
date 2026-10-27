package calendar_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The busy read (calendar Outlook sync design O9.1–O9.3, O9.9) and its reads
// (O9.5, O9.7), against modtest's fake mailbox provider. The harness clock
// starts on Saturday 12 September 2026 at 14:00 in Oslo, so the read's window
// is 13 August 2026 to 10 April 2027.

// busyRow is one row of calendar.mailbox_busy as a test reads it.
type busyRow struct {
	allDay             bool
	showAs             string
	startsAt, endsAt   *time.Time
	startDate, endDate *string
}

func busyRows(t *testing.T, r *mailboxRig, id uuid.UUID) map[string]busyRow {
	t.Helper()
	rows, err := r.Pool().Query(context.Background(), `SELECT event_id, all_day, show_as, starts_at, ends_at, start_date::text, end_date::text
		FROM calendar.mailbox_busy WHERE user_id = $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]busyRow{}
	for rows.Next() {
		var eventID string
		var b busyRow
		if err := rows.Scan(&eventID, &b.allDay, &b.showAs, &b.startsAt, &b.endsAt, &b.startDate, &b.endDate); err != nil {
			t.Fatal(err)
		}
		out[eventID] = b
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func rowIDs(rows map[string]busyRow) []string {
	var out []string
	for id := range rows {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// busyState is the delta's state on the person's mailbox_sync row.
type busyState struct {
	link, from, to *string
	synced         *time.Time
}

func readBusyState(t *testing.T, r *mailboxRig, id uuid.UUID) busyState {
	t.Helper()
	var s busyState
	if err := r.Pool().QueryRow(context.Background(), `SELECT busy_link, busy_from::text, busy_to::text, busy_synced_at FROM calendar.mailbox_sync WHERE user_id = $1`, id).
		Scan(&s.link, &s.from, &s.to, &s.synced); err != nil {
		t.Fatal(err)
	}
	return s
}

// staleBusy writes a busy row the mailbox no longer answers.
func staleBusy(t *testing.T, r *mailboxRig, id uuid.UUID, eventID string) {
	t.Helper()
	r.Exec(t, `INSERT INTO calendar.mailbox_busy (user_id, event_id, all_day, starts_at, ends_at, show_as, updated_at)
	           VALUES ($1, $2, false, $3, $4, 'busy', $5)`, id, eventID, r.Now(), r.Now().Add(time.Hour), r.Now())
}

// foreign is one of the person's own timed events, wall-clock in Oslo.
func foreign(t *testing.T, id, start, end, showAs string) modtest.FakeBusyEvent {
	t.Helper()
	return modtest.FakeBusyEvent{ID: id, StartsAt: at(t, oslo, start), EndsAt: at(t, oslo, end), ShowAs: showAs}
}

func day(s string) time.Time {
	d, _ := time.Parse(time.DateOnly, s)
	return d
}

// outlookID is the id an Outlook event's item carries: the first 16 hex
// digits of the SHA-256 of its id.
func outlookID(eventID string) string {
	sum := sha256.Sum256([]byte(eventID))
	return hex.EncodeToString(sum[:])[:16]
}

// syncedPerson is a person whose sync is on, with two of their own events
// read back by a first sync.
func syncedPerson(t *testing.T, r *mailboxRig) (*modtest.Client, uuid.UUID, string, gen.MailboxSync) {
	t.Helper()
	c, me, mb := r.person(t)
	r.box.SeedForeign(mb, foreign(t, "ev-1", "2026-09-15T10:00", "2026-09-15T11:00", contracts.ShowAsBusy))
	r.box.SeedForeign(mb, foreign(t, "ev-2", "2026-09-16T10:00", "2026-09-16T11:00", contracts.ShowAsBusy))
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	if n := len(busyRows(t, r, me)); n != 2 {
		t.Fatalf("the first read stored %d rows", n)
	}
	return c, me, mb, on
}

func TestMailboxBusy_StoresThePersonsBusyTimeAndSkipsTheCalendarsOwnCopies(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	plan := create(t, c, timed(t, "Plan", oslo, "2026-09-14T09:00", "2026-09-14T10:00"))
	r.box.SeedForeign(mb, foreign(t, "ev-busy", "2026-09-15T10:00", "2026-09-15T11:00", contracts.ShowAsBusy))
	r.box.SeedForeign(mb, foreign(t, "ev-free", "2026-09-15T12:00", "2026-09-15T13:00", contracts.ShowAsFree))
	cancelled := foreign(t, "ev-cancel", "2026-09-15T14:00", "2026-09-15T15:00", contracts.ShowAsBusy)
	cancelled.Cancelled = true
	r.box.SeedForeign(mb, cancelled)
	r.box.SeedForeign(mb, foreign(t, "ev-tent", "2026-09-16T10:00", "2026-09-16T11:00", contracts.ShowAsTentative))
	r.box.SeedForeign(mb, modtest.FakeBusyEvent{ID: "ev-oof", AllDay: true, StartDate: day("2026-09-21"), EndDate: day("2026-09-22"), ShowAs: contracts.ShowAsOOF})
	series := foreign(t, "ev-series", "2026-09-17T10:00", "2026-09-17T11:00", contracts.ShowAsBusy)
	series.Series = true
	r.box.SeedForeign(mb, series)
	r.box.SeedForeign(mb, foreign(t, "ev-odd", "2026-09-18T10:00", "2026-09-18T11:00", ""))
	r.box.SeedForeign(mb, foreign(t, "ev-far", "2027-06-01T10:00", "2027-06-01T11:00", contracts.ShowAsBusy))
	// A copy of this installation's the map does not know: one whose
	// create's outcome was unknown, say.
	lost := r.box.PutEvent(mb, "", "appointment:999", contracts.MailboxEvent{
		StartsAt: at(t, oslo, "2026-09-19T10:00"), EndsAt: at(t, oslo, "2026-09-19T11:00"), ShowAs: contracts.ShowAsBusy})

	mailboxCycle(t, r)
	if _, pushed := byKey(r, mb, "")[key(plan.Id)]; !pushed {
		t.Fatal("the appointment was not pushed")
	}

	rows := busyRows(t, r, me)
	if got, want := rowIDs(rows), []string{"ev-busy", "ev-odd", "ev-oof", "ev-series", "ev-tent"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v: no free, cancelled, far, mapped or marked one", got, want)
	}
	if b := rows["ev-busy"]; b.allDay || b.showAs != "busy" || !b.startsAt.Equal(at(t, oslo, "2026-09-15T10:00")) || !b.endsAt.Equal(at(t, oslo, "2026-09-15T11:00")) || b.startDate != nil {
		t.Errorf("the busy one = %+v", b)
	}
	if b := rows["ev-oof"]; !b.allDay || b.showAs != "oof" || b.startDate == nil || *b.startDate != "2026-09-21" || *b.endDate != "2026-09-22" || b.startsAt != nil {
		t.Errorf("the all-day one = %+v", b)
	}
	if rows["ev-odd"].showAs != "busy" || rows["ev-tent"].showAs != "tentative" {
		t.Errorf("an unknown showAs is %q, a tentative one %q", rows["ev-odd"].showAs, rows["ev-tent"].showAs)
	}

	deltas := calls(r, 0, "Delta")
	if len(deltas) != 1 || deltas[0].Link != "" || !deltas[0].From.Equal(at(t, oslo, "2026-08-13T00:00")) || !deltas[0].To.Equal(at(t, oslo, "2027-04-11T00:00")) {
		t.Fatalf("the delta calls = %+v", deltas)
	}
	var asked []string
	for _, m := range calls(r, 0, "Marked") {
		asked = append(asked, m.EventIDs...)
	}
	slices.Sort(asked)
	want := []string{"ev-busy", "ev-odd", "ev-oof", "ev-tent", lost}
	slices.Sort(want)
	if !slices.Equal(asked, want) {
		t.Errorf("Marked asked about %v, want %v: never a series, a free, a cancelled or a mapped one", asked, want)
	}
	st := readBusyState(t, r, me)
	if st.link == nil || st.from == nil || *st.from != "2026-08-13" || *st.to != "2027-04-10" || st.synced == nil || !st.synced.Equal(r.Now()) {
		t.Errorf("the delta's state = %+v", st)
	}
	if got := getSync(t, c); got.BusySyncedAt == nil || !got.BusySyncedAt.Equal(r.Now()) || got.State != gen.MailboxSyncStateActive {
		t.Errorf("the sync = %+v", got)
	}

	// The person changes their calendar: the next read follows the link.
	r.box.MoveForeign(mb, foreign(t, "ev-busy", "2026-09-15T10:00", "2026-09-15T11:00", contracts.ShowAsFree))
	r.box.RemoveForeign(mb, "ev-tent")
	r.box.MoveForeign(mb, foreign(t, "ev-odd", "2026-09-18T12:00", "2026-09-18T13:00", contracts.ShowAsBusy))
	series.Cancelled = true
	r.box.MoveForeign(mb, series)
	r.box.SeedForeign(mb, foreign(t, "ev-new", "2026-09-22T10:00", "2026-09-22T11:00", contracts.ShowAsOOF))
	before := len(r.box.Calls())
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	deltas = calls(r, before, "Delta")
	if len(deltas) != 1 || deltas[0].Link == "" || deltas[0].Link != *st.link {
		t.Fatalf("the second read's delta calls = %+v, want one following %v", deltas, *st.link)
	}
	rows = busyRows(t, r, me)
	if got, want := rowIDs(rows), []string{"ev-new", "ev-odd", "ev-oof"}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if !rows["ev-odd"].startsAt.Equal(at(t, oslo, "2026-09-18T12:00")) {
		t.Errorf("the moved one starts %v", rows["ev-odd"].startsAt)
	}
	asked = nil
	for _, m := range calls(r, before, "Marked") {
		asked = append(asked, m.EventIDs...)
	}
	slices.Sort(asked)
	if !slices.Equal(asked, []string{"ev-new", "ev-odd"}) {
		t.Errorf("the second read asked Marked about %v", asked)
	}
}

func TestMailboxBusy_AFreshStartEmptiesTheRowsAndAMonthOnItStartsAgain(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	turnOn(t, c, "default", "full", "calendar.appointment")
	staleBusy(t, r, me, "stale-1")
	r.box.SeedForeign(mb, foreign(t, "ev-1", "2026-09-15T10:00", "2026-09-15T11:00", contracts.ShowAsBusy))
	mailboxCycle(t, r)
	if got := rowIDs(busyRows(t, r, me)); !slices.Equal(got, []string{"ev-1"}) {
		t.Fatalf("after the first read rows = %v", got)
	}

	// A window that still reaches 180 days ahead is followed.
	r.Exec(t, `UPDATE calendar.mailbox_sync SET busy_to = '2027-03-11' WHERE user_id = $1`, me)
	before := len(r.box.Calls())
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if d := calls(r, before, "Delta"); len(d) != 1 || d[0].Link == "" {
		t.Fatalf("a window to today+180 was not followed: %+v", d)
	}

	// One that ends before it is started afresh, and the rows emptied.
	r.Exec(t, `UPDATE calendar.mailbox_sync SET busy_to = '2027-03-10' WHERE user_id = $1`, me)
	staleBusy(t, r, me, "stale-2")
	before = len(r.box.Calls())
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	if d := calls(r, before, "Delta"); len(d) != 1 || d[0].Link != "" {
		t.Fatalf("a window ending before today+180 was not started afresh: %+v", d)
	}
	if got := rowIDs(busyRows(t, r, me)); !slices.Equal(got, []string{"ev-1"}) {
		t.Errorf("after the fresh start rows = %v", got)
	}
	if st := readBusyState(t, r, me); st.to == nil || *st.to != "2027-04-10" {
		t.Errorf("the window ends %v", st.to)
	}
}

func TestMailboxBusy_AnExpiredLinkStartsTheDeltaAfresh(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	_, me, mb, _ := syncedPerson(t, r)
	r.box.ExpireDelta(mb)
	staleBusy(t, r, me, "stale")
	before := len(r.box.Calls())
	r.Advance(15 * time.Minute)
	mailboxCycle(t, r)
	d := calls(r, before, "Delta")
	if len(d) != 2 || d[0].Link == "" || d[1].Link != "" {
		t.Fatalf("the delta calls = %+v, want the expired link then a fresh one", d)
	}
	if got := rowIDs(busyRows(t, r, me)); !slices.Equal(got, []string{"ev-1", "ev-2"}) {
		t.Errorf("rows = %v", got)
	}
	if row := readSyncRow(t, r, me); row.state != "active" || row.attempts != 0 {
		t.Errorf("the row = %+v", row)
	}
}

func TestMailboxBusy_ReadsEveryPage(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	r.box.SetDeltaPageSize(2)
	for i, id := range []string{"ev-1", "ev-2", "ev-3", "ev-4", "ev-5"} {
		start := time.Date(2026, 9, 14+i, 10, 0, 0, 0, time.UTC)
		r.box.SeedForeign(mb, modtest.FakeBusyEvent{ID: id, StartsAt: start, EndsAt: start.Add(time.Hour), ShowAs: contracts.ShowAsBusy})
	}
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	d := calls(r, 0, "Delta")
	if len(d) != 3 || d[0].Link != "" || d[1].Link == "" || d[2].Link == "" || d[1].Link == d[2].Link {
		t.Fatalf("the delta calls = %+v, want three pages", d)
	}
	if n := len(busyRows(t, r, me)); n != 5 {
		t.Errorf("rows %d, want 5", n)
	}
	if st := readBusyState(t, r, me); st.synced == nil || st.link == nil || *st.link == d[2].Link {
		t.Errorf("the delta's state = %+v: the delta link after the last page", st)
	}
}

// Not parallel: it lowers the package's cap while it runs.
func TestMailboxBusy_KeepsAtMostTheCapAndLogsIt(t *testing.T) {
	defer calendar.SetMailboxBusyCap(3)()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	for i := range 5 {
		start := time.Date(2026, 9, 14+i, 10, 0, 0, 0, time.UTC)
		r.box.SeedForeign(mb, modtest.FakeBusyEvent{StartsAt: start, EndsAt: start.Add(time.Hour), ShowAs: contracts.ShowAsBusy})
	}
	turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)
	if n := len(busyRows(t, r, me)); n != 3 {
		t.Errorf("rows %d, want the cap of 3", n)
	}
	if logs := r.Logs(); strings.Count(logs, "past its cap") != 1 || !strings.Contains(logs, me.String()) {
		t.Errorf("the cap was not logged once by the person's id:\n%s", logs)
	}
	if row := readSyncRow(t, r, me); row.state != "active" {
		t.Errorf("the row = %+v", row)
	}
}

func TestMailboxBusy_IsForgottenWhenTheSyncStopsAndKeptOnAFailure(t *testing.T) {
	t.Parallel()
	forgotten := func(t *testing.T, r *mailboxRig, me uuid.UUID) {
		t.Helper()
		if n := len(busyRows(t, r, me)); n != 0 {
			t.Errorf("rows %d after the sync stopped", n)
		}
		if st := readBusyState(t, r, me); st.link != nil || st.from != nil || st.to != nil || st.synced != nil {
			t.Errorf("the delta's state = %+v after the sync stopped", st)
		}
	}
	t.Run("turned off", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		c, me, _, on := syncedPerson(t, r)
		putSync(t, c, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
		mailboxCycle(t, r)
		forgotten(t, r, me)
		if got := getSync(t, c); got.BusySyncedAt != nil {
			t.Errorf("busySyncedAt %v after turning off", got.BusySyncedAt)
		}
	})
	t.Run("the provider in cleanup", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		_, me, _, _ := syncedPerson(t, r)
		r.box.SetStatus(contracts.MailboxCleanup, "switch_off")
		r.Advance(15 * time.Minute)
		mailboxCycle(t, r)
		if row := readSyncRow(t, r, me); row.state != "unavailable" {
			t.Fatalf("the row = %+v", row)
		}
		forgotten(t, r, me)
	})
	t.Run("the provider unavailable", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		_, me, _, _ := syncedPerson(t, r)
		r.box.SetStatus(contracts.MailboxUnavailable, "")
		r.Advance(15 * time.Minute)
		mailboxCycle(t, r)
		forgotten(t, r, me)
	})
	t.Run("Marked refused", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		_, me, mb, _ := syncedPerson(t, r)
		r.box.SeedForeign(mb, foreign(t, "ev-3", "2026-09-17T10:00", "2026-09-17T11:00", contracts.ShowAsBusy))
		r.box.FailWith("Marked", contracts.ErrMailboxDenied)
		r.Advance(15 * time.Minute)
		mailboxCycle(t, r)
		if row := readSyncRow(t, r, me); row.state != "unavailable" || row.errCode == nil || *row.errCode != "mailbox_denied" {
			t.Fatalf("the row = %+v", row)
		}
		forgotten(t, r, me)
	})
	t.Run("a failure keeps them", func(t *testing.T) {
		t.Parallel()
		r := newMailboxRig(t)
		_, me, _, _ := syncedPerson(t, r)
		was := readBusyState(t, r, me)
		r.box.FailWith("Delta", errors.New("graph: 503"))
		r.Advance(15 * time.Minute)
		mailboxCycle(t, r)
		if row := readSyncRow(t, r, me); row.state != "active" || row.attempts != 1 || row.errCode == nil || *row.errCode != "failed" {
			t.Fatalf("the row = %+v", row)
		}
		if n := len(busyRows(t, r, me)); n != 2 {
			t.Errorf("rows %d after a failure, want 2 kept", n)
		}
		if st := readBusyState(t, r, me); st.link == nil || *st.link != *was.link {
			t.Errorf("the link %v after a failure, want %v kept", st.link, *was.link)
		}
	})
}

func TestMailboxBusy_TheCalendarShowsItAsOutlookItems(t *testing.T) {
	t.Parallel()
	r := newMailboxRig(t)
	c, me, mb := r.person(t)
	viewer, _ := signIn(t, r.harness, "calendar:view-colleagues")
	r.box.SeedForeign(mb, foreign(t, "ev-busy", "2026-09-15T10:00", "2026-09-15T11:00", contracts.ShowAsBusy))
	r.box.SeedForeign(mb, foreign(t, "ev-tent", "2026-09-15T13:00", "2026-09-15T14:00", contracts.ShowAsTentative))
	r.box.SeedForeign(mb, modtest.FakeBusyEvent{ID: "ev-oof", AllDay: true, StartDate: day("2026-09-17"), EndDate: day("2026-09-18"), ShowAs: contracts.ShowAsOOF})
	r.box.SeedForeign(mb, foreign(t, "ev-else", "2026-09-16T08:00", "2026-09-16T16:00", contracts.ShowAsWorkingElsewhere))
	on := turnOn(t, c, "default", "full", "calendar.appointment")
	mailboxCycle(t, r)

	outlook := func(items []gen.CalendarItem) map[string]gen.CalendarItem {
		out := map[string]gen.CalendarItem{}
		for _, it := range items {
			if it.Kind == "calendar.outlook" {
				out[it.Id] = it
			}
		}
		return out
	}
	var got gen.CalendarItems
	do(t, c, http.MethodGet, windowPath("2026-09-14", "2026-09-20"), nil, http.StatusOK, &got)
	items := outlook(got.Items)
	if len(items) != 4 {
		t.Fatalf("outlook items = %+v", items)
	}
	b, ok := items[outlookID("ev-busy")]
	if !ok || b.Title != "" || b.Outlook == nil || b.Outlook.ShowAs != gen.CalendarItemOutlookShowAsBusy || b.AllDay ||
		b.StartsAt == nil || !b.StartsAt.Equal(at(t, oslo, "2026-09-15T10:00")) || !b.EndsAt.Equal(at(t, oslo, "2026-09-15T11:00")) {
		t.Errorf("the busy item = %+v", b)
	}
	o := items[outlookID("ev-oof")]
	if !o.AllDay || o.StartDate == nil || o.StartDate.String() != "2026-09-17" || o.EndDate.String() != "2026-09-18" || o.Outlook.ShowAs != gen.CalendarItemOutlookShowAsOof {
		t.Errorf("the all-day item = %+v", o)
	}
	for _, it := range got.Items {
		if strings.Contains(it.Id, "ev-") {
			t.Errorf("an item carries the mailbox's id: %+v", it)
		}
	}
	do(t, c, http.MethodGet, windowPath("2026-09-14", "2026-09-20", "kinds=calendar.appointment"), nil, http.StatusOK, &got)
	if n := len(outlook(got.Items)); n != 0 {
		t.Errorf("kinds=calendar.appointment answered %d outlook items", n)
	}
	do(t, c, http.MethodGet, windowPath("2026-09-14", "2026-09-20", "kinds=calendar.outlook"), nil, http.StatusOK, &got)
	if n := len(outlook(got.Items)); n != 4 || len(got.Items) != 4 {
		t.Errorf("kinds=calendar.outlook answered %d of %d items", n, len(got.Items))
	}

	var meta gen.CalendarMeta
	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if !slices.Contains(meta.Sources, gen.CalendarSourceKind{Kind: "calendar.outlook", Module: "calendar"}) {
		t.Errorf("meta's sources = %+v, want calendar.outlook while the sync is on", meta.Sources)
	}

	// A colleague sees the busy and out-of-office ones, untitled.
	do(t, viewer, http.MethodGet, colleaguePath(me, "2026-09-14", "2026-09-20"), nil, http.StatusOK, &got)
	items = outlook(got.Items)
	if _, busy := items[outlookID("ev-busy")]; len(items) != 2 || !busy || items[outlookID("ev-oof")].Kind == "" {
		t.Fatalf("the colleague's outlook items = %+v, want the busy and the oof one", items)
	}
	for _, it := range items {
		if it.Title != "" {
			t.Errorf("a colleague's outlook item carries a title: %+v", it)
		}
	}

	// Turned off, nothing is shown — before the worker has forgotten a row.
	putSync(t, c, on.Revision, syncBody(false, "default", "full", "calendar.appointment"))
	do(t, c, http.MethodGet, windowPath("2026-09-14", "2026-09-20"), nil, http.StatusOK, &got)
	if n := len(outlook(got.Items)); n != 0 {
		t.Errorf("turned off, the person's calendar answers %d outlook items", n)
	}
	do(t, viewer, http.MethodGet, colleaguePath(me, "2026-09-14", "2026-09-20"), nil, http.StatusOK, &got)
	if n := len(outlook(got.Items)); n != 0 {
		t.Errorf("turned off, the colleague's calendar answers %d outlook items", n)
	}
	do(t, c, http.MethodGet, metaPath, nil, http.StatusOK, &meta)
	if slices.ContainsFunc(meta.Sources, func(k gen.CalendarSourceKind) bool { return k.Kind == "calendar.outlook" }) {
		t.Errorf("meta's sources = %+v after turning off", meta.Sources)
	}
}
