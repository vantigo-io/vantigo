package modtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake mailbox provider's own rules, which the calendar's tests lean on:
// what each method does to a mailbox, and what it refuses.

func mailboxEvent(subject string) *contracts.MailboxEvent {
	return &contracts.MailboxEvent{Subject: subject, StartsAt: Start, EndsAt: Start.Add(time.Hour), ShowAs: contracts.ShowAsBusy}
}

// Apply creates an event carrying its key, rewrites and deletes it, and
// answers Gone for an event that no longer exists; Find answers the keyed
// events of the default calendar alone.
func TestFakeMailboxCalendar_ApplyAndFind(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	if got := f.Key(); got != "entra" {
		t.Errorf("Key() = %q, want entra", got)
	}
	foreign := f.PutEvent("oid-1", "", "", *mailboxEvent("Lunch"))
	res, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{
		{Kind: contracts.MailboxOpCreate, Key: "appointment:1", Event: mailboxEvent("Standup")},
		{Kind: contracts.MailboxOpUpdate, EventID: "event-404", Event: mailboxEvent("Gone")},
		{Kind: contracts.MailboxOpDelete, EventID: "event-404"},
	})
	if err != nil || len(res) != 3 {
		t.Fatalf("Apply = %v, %v; want three results", res, err)
	}
	created := res[0].EventID
	if created == "" || res[0].Err != nil || res[0].ChangeKey == "" {
		t.Errorf("the create answered %+v, want a new id and a change key", res[0])
	}
	if !res[1].Gone || !res[2].Gone {
		t.Errorf("the update and delete of a missing event answered %+v, %+v; want Gone", res[1], res[2])
	}
	refs, err := f.Find(ctx, "oid-1", "", time.Time{})
	if err != nil || !slices.Equal(refs, []contracts.MailboxEventRef{{EventID: created, Key: "appointment:1"}}) {
		t.Errorf("Find = %v, %v; want the keyed event alone, not %s", refs, err, foreign)
	}
	res, err = f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpUpdate, EventID: created, Event: mailboxEvent("Retro")}})
	if err != nil || res[0].Gone || res[0].Err != nil {
		t.Fatalf("the update = %v, %v", res, err)
	}
	if got := f.EventsIn("oid-1", ""); len(got) != 2 || got[1].Event.Subject != "Retro" || got[1].Key != "appointment:1" {
		t.Errorf("Events = %+v, want the foreign event and the rewritten one, its key kept", got)
	}
	if res, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: created}}); err != nil || res[0].Gone {
		t.Errorf("the delete = %v, %v; want done", res, err)
	}
	if got := f.EventsIn("oid-1", ""); len(got) != 1 || got[0].ID != foreign {
		t.Errorf("Events after the delete = %+v, want the foreign event alone", got)
	}
	if got := len(f.Calls()); got != 4 {
		t.Errorf("len(Calls()) = %d, want 4: three Applies and a Find", got)
	}
}

// EnsureCalendar keeps a calendar that still exists and makes a new one
// otherwise; DeleteCalendar drops it with its events, and Find answers the
// keyed events of a secondary calendar alone.
func TestFakeMailboxCalendar_Calendars(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	id, err := f.EnsureCalendar(ctx, "oid-1", "", "Vantigo")
	if err != nil || id == "" {
		t.Fatalf("EnsureCalendar = %q, %v", id, err)
	}
	if again, err := f.EnsureCalendar(ctx, "oid-1", id, "Vantigo"); err != nil || again != id {
		t.Errorf("EnsureCalendar(known) = %q, %v; want %q kept", again, err, id)
	}
	f.PutEvent("oid-1", id, "", *mailboxEvent("Untagged"))
	keyed := f.PutEvent("oid-1", id, "appointment:1", *mailboxEvent("Tagged"))
	if refs, err := f.Find(ctx, "oid-1", id, time.Time{}); err != nil || !slices.Equal(refs, []contracts.MailboxEventRef{{EventID: keyed, Key: "appointment:1"}}) {
		t.Errorf("Find in the secondary calendar = %v, %v; want its keyed event alone", refs, err)
	}
	if err := f.DeleteCalendar(ctx, "oid-1", id); err != nil {
		t.Fatalf("DeleteCalendar: %v", err)
	}
	if len(f.Calendars("oid-1")) != 0 || len(f.EventsIn("oid-1", id)) != 0 {
		t.Errorf("the calendar or its events survived the delete")
	}
	if err := f.DeleteCalendar(ctx, "oid-1", id); err != nil {
		t.Errorf("DeleteCalendar of a calendar gone = %v, want nil", err)
	}
	if fresh, err := f.EnsureCalendar(ctx, "oid-1", id, "Vantigo"); err != nil || fresh == id {
		t.Errorf("EnsureCalendar(gone) = %q, %v; want a new calendar", fresh, err)
	}
}

// Under cleanup Apply takes deletes only and DeleteCalendar works; while
// unavailable every call is refused; an unknown or refused mailbox answers
// its sentinel.
func TestFakeMailboxCalendar_Refusals(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	id := f.PutEvent("oid-1", "", "appointment:1", *mailboxEvent("Standup"))
	cal, _ := f.EnsureCalendar(ctx, "oid-1", "", "Vantigo")

	f.SetStatus(contracts.MailboxCleanup, "switch_off")
	res, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{
		{Kind: contracts.MailboxOpCreate, Key: "appointment:2", Event: mailboxEvent("New")},
		{Kind: contracts.MailboxOpDelete, EventID: id},
	})
	if err != nil || !errors.Is(res[0].Err, contracts.ErrMailboxUnavailable) || res[1].Err != nil || res[1].Gone {
		t.Errorf("Apply under cleanup = %+v, %v; want the create refused and the delete done", res, err)
	}
	if _, err := f.EnsureCalendar(ctx, "oid-1", cal, "Vantigo"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("EnsureCalendar under cleanup = %v, want ErrMailboxUnavailable", err)
	}
	if err := f.DeleteCalendar(ctx, "oid-1", cal); err != nil {
		t.Errorf("DeleteCalendar under cleanup = %v, want nil", err)
	}

	f.SetStatus(contracts.MailboxUnavailable, "credential_invalid")
	if _, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: id}}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Apply while unavailable = %v, want ErrMailboxUnavailable", err)
	}
	if st, err := f.Status(ctx); err != nil || st.State != contracts.MailboxUnavailable || st.Reason != "credential_invalid" {
		t.Errorf("Status = %+v, %v", st, err)
	}

	f.SetStatus(contracts.MailboxAvailable, "")
	f.SetUnknown("oid-2", true)
	if _, err := f.Find(ctx, "oid-2", "", time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Find of an unknown mailbox = %v, want ErrMailboxUnknown", err)
	}
	f.SetMailboxError("oid-3", contracts.ErrMailboxDenied)
	if _, err := f.Apply(ctx, "oid-3", "", nil); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("Apply of a denied mailbox = %v, want ErrMailboxDenied", err)
	}
	f.SetMailboxError("oid-3", nil)
	if _, err := f.Apply(ctx, "oid-3", "", nil); err != nil {
		t.Errorf("Apply after the refusal was cleared = %v", err)
	}
}

// A foreign calendar (PutCalendar ours false) is never adopted — the next
// name is made — and DeleteCalendar refuses it with ErrCalendarNotOurs; Find
// with a from answers the events starting at or after it.
func TestFakeMailboxCalendar_ForeignCalendarsAndFrom(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	theirs := f.PutCalendar("oid-1", "Vantigo", false)
	ours, err := f.EnsureCalendar(ctx, "oid-1", "", "Vantigo")
	if err != nil || ours == theirs || f.Calendars("oid-1")[ours] != "Vantigo (2)" {
		t.Fatalf("EnsureCalendar past a foreign one = %q, %v; calendars %v", ours, err, f.Calendars("oid-1"))
	}
	if kept, err := f.EnsureCalendar(ctx, "oid-1", theirs, "Vantigo"); err != nil || kept != ours {
		t.Errorf("EnsureCalendar(known foreign) = %q, %v; want ours %q", kept, err, ours)
	}
	if err := f.DeleteCalendar(ctx, "oid-1", theirs); !errors.Is(err, contracts.ErrCalendarNotOurs) {
		t.Errorf("DeleteCalendar(foreign) = %v, want ErrCalendarNotOurs", err)
	}
	if _, ok := f.Calendars("oid-1")[theirs]; !ok {
		t.Error("the foreign calendar was deleted")
	}

	early := f.PutEvent("oid-1", "", "appointment:1", *mailboxEvent("Early"))
	late := *mailboxEvent("Late")
	late.StartsAt, late.EndsAt = Start.Add(48*time.Hour), Start.Add(49*time.Hour)
	lateID := f.PutEvent("oid-1", "", "appointment:2", late)
	refs, err := f.Find(ctx, "oid-1", "", Start.Add(time.Hour))
	if err != nil || !slices.Equal(refs, []contracts.MailboxEventRef{{EventID: lateID, Key: "appointment:2"}}) {
		t.Errorf("Find from an hour on = %v, %v; want the late one, not %s", refs, err, early)
	}
	if calls := f.Calls(); !calls[len(calls)-1].From.Equal(Start.Add(time.Hour)) {
		t.Errorf("the call recorded From %v", calls[len(calls)-1].From)
	}
}

// readDelta follows Delta from link to its end and answers every change and
// the delta link, failing the test on an error.
func readDelta(t *testing.T, f *FakeMailboxCalendar, mailbox string, from, to time.Time, link string) ([]contracts.MailboxChange, string, int) {
	t.Helper()
	var all []contracts.MailboxChange
	for pages := 1; ; pages++ {
		d, err := f.Delta(context.Background(), mailbox, from, to, link)
		if err != nil {
			t.Fatalf("Delta(%q) = %v", link, err)
		}
		all, link = append(all, d.Changes...), d.Link
		if d.Done {
			return all, link, pages
		}
		if pages > 100 {
			t.Fatal("Delta never answered Done")
		}
	}
}

func changeIDs(changes []contracts.MailboxChange) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		id := c.EventID
		if c.Removed {
			id += " removed"
		}
		out = append(out, id)
	}
	return out
}

// A fresh delta answers every event of the default calendar overlapping the
// window — the person's own and the copies alike, with their times and
// ShowAs — and a linked one the changes since, in pages of the size set: a
// copy rewritten or deleted through Apply, an event moved out of the window
// or removed answered Removed, an event of a secondary calendar never.
func TestFakeMailboxCalendar_Delta(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	from, to := Start.AddDate(0, 0, -30), Start.AddDate(0, 0, 210)
	day := time.Date(2026, time.September, 14, 0, 0, 0, 0, time.UTC)

	meeting := f.SeedForeign("oid-1", FakeBusyEvent{ID: "AAMk-1", StartsAt: Start, EndsAt: Start.Add(time.Hour), ShowAs: contracts.ShowAsOOF, Series: true})
	holiday := f.SeedForeign("oid-1", FakeBusyEvent{AllDay: true, StartDate: day, EndDate: day.AddDate(0, 0, 1), ShowAs: contracts.ShowAsBusy, Cancelled: true})
	f.SeedForeign("oid-1", FakeBusyEvent{ID: "AAMk-past", StartsAt: from.AddDate(0, 0, -2), EndsAt: from.AddDate(0, 0, -1), ShowAs: contracts.ShowAsBusy})
	res, _ := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpCreate, Key: "appointment:1", Event: mailboxEvent("Standup")}})
	copyID := res[0].EventID
	cal, _ := f.EnsureCalendar(ctx, "oid-1", "", "Vantigo")
	f.PutEvent("oid-1", cal, "item:1", *mailboxEvent("Elsewhere"))

	f.SetDeltaPageSize(2)
	changes, link, pages := readDelta(t, f, "oid-1", from, to, "")
	if pages != 2 || !slices.Equal(changeIDs(changes), []string{"AAMk-1", holiday, copyID}) {
		t.Fatalf("the fresh delta answered %v in %d pages; want the meeting, the holiday and the copy in 2", changeIDs(changes), pages)
	}
	if c := changes[0]; c.EventID != meeting || !c.Series || c.ShowAs != contracts.ShowAsOOF || !c.StartsAt.Equal(Start) || c.AllDay {
		t.Errorf("the meeting answered %+v", c)
	}
	if c := changes[1]; !c.AllDay || !c.Cancelled || !c.StartDate.Equal(day) || !c.EndDate.Equal(day.AddDate(0, 0, 1)) {
		t.Errorf("the holiday answered %+v", c)
	}
	if c := changes[2]; c.ShowAs != contracts.ShowAsBusy || c.Series || !c.EndsAt.Equal(Start.Add(time.Hour)) {
		t.Errorf("the copy answered %+v", c)
	}

	if changes, link2, _ := readDelta(t, f, "oid-1", time.Time{}, time.Time{}, link); len(changes) != 0 || link2 == "" {
		t.Errorf("a delta with no change answered %v, %q", changes, link2)
	}

	f.MoveForeign("oid-1", FakeBusyEvent{ID: meeting, StartsAt: to.AddDate(0, 0, 1), EndsAt: to.AddDate(0, 0, 2), ShowAs: contracts.ShowAsBusy})
	f.RemoveForeign("oid-1", holiday)
	moved := *mailboxEvent("Standup")
	moved.StartsAt = Start.Add(2 * time.Hour)
	moved.EndsAt = moved.StartsAt.Add(time.Hour)
	if _, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpUpdate, EventID: copyID, Event: &moved}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	late := f.SeedForeign("oid-1", FakeBusyEvent{StartsAt: Start.AddDate(0, 0, 3), EndsAt: Start.AddDate(0, 0, 3).Add(time.Hour), ShowAs: contracts.ShowAsTentative})
	f.PutEvent("oid-1", cal, "item:2", *mailboxEvent("Elsewhere too"))
	changes, link, _ = readDelta(t, f, "oid-1", time.Time{}, time.Time{}, link)
	if want := []string{meeting + " removed", holiday + " removed", copyID, late}; !slices.Equal(changeIDs(changes), want) {
		t.Fatalf("the linked delta answered %v, want %v", changeIDs(changes), want)
	}
	if !changes[2].StartsAt.Equal(moved.StartsAt) {
		t.Errorf("the copy's update answered %+v", changes[2])
	}

	if _, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: copyID}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changes, _, _ := readDelta(t, f, "oid-1", time.Time{}, time.Time{}, link); !slices.Equal(changeIDs(changes), []string{copyID + " removed"}) {
		t.Errorf("the delta after the copy's delete answered %v", changeIDs(changes))
	}

	calls := f.Calls()
	if last := calls[len(calls)-1]; last.Method != "Delta" || last.Link != link {
		t.Errorf("the last call recorded %+v, want Delta with the link", last)
	}
}

// A link for another mailbox, or one never answered, is refused; an expired
// one answers ErrMailboxDeltaExpired until a fresh delta is started; Delta
// and Marked are refused under cleanup and while unavailable, and answer a
// mailbox's own refusal.
func TestFakeMailboxCalendar_DeltaRefusals(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	from, to := Start.AddDate(0, 0, -30), Start.AddDate(0, 0, 210)
	_, link, _ := readDelta(t, f, "oid-1", from, to, "")
	if _, err := f.Delta(ctx, "oid-2", from, to, link); err == nil || errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("Delta with another mailbox's link = %v, want a plain failure", err)
	}
	if _, err := f.Delta(ctx, "oid-1", from, to, "https://graph.microsoft.com/v1.0/users/x/calendarView/delta"); err == nil {
		t.Error("Delta with a link it never answered = nil, want a failure")
	}
	f.ExpireDelta("oid-1")
	if _, err := f.Delta(ctx, "oid-1", from, to, link); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("Delta with an expired link = %v, want ErrMailboxDeltaExpired", err)
	}
	if _, fresh, _ := readDelta(t, f, "oid-1", from, to, ""); fresh == link {
		t.Error("a fresh delta answered the expired link")
	} else if _, err := f.Delta(ctx, "oid-1", from, to, fresh); err != nil {
		t.Errorf("Delta with the fresh link = %v", err)
	}

	for _, state := range []string{contracts.MailboxCleanup, contracts.MailboxUnavailable} {
		f.SetStatus(state, "switch_off")
		if _, err := f.Delta(ctx, "oid-1", from, to, ""); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("Delta under %s = %v, want ErrMailboxUnavailable", state, err)
		}
		if _, err := f.Marked(ctx, "oid-1", []string{"event-1"}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("Marked under %s = %v, want ErrMailboxUnavailable", state, err)
		}
	}
	f.SetStatus(contracts.MailboxAvailable, "")
	f.SetMailboxError("oid-3", contracts.ErrMailboxGone)
	if _, err := f.Delta(ctx, "oid-3", from, to, ""); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("Delta of a gone mailbox = %v, want ErrMailboxGone", err)
	}
	if _, err := f.Marked(ctx, "oid-3", nil); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("Marked of a gone mailbox = %v, want ErrMailboxGone", err)
	}
}

// Marked answers, among the ids asked and in their order, the events
// carrying a key — not the person's own, not one gone — and refuses more
// than MailboxMarkedMax ids.
func TestFakeMailboxCalendar_Marked(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	theirs := f.SeedForeign("oid-1", FakeBusyEvent{StartsAt: Start, EndsAt: Start.Add(time.Hour), ShowAs: contracts.ShowAsBusy})
	a := f.PutEvent("oid-1", "", "appointment:1", *mailboxEvent("A"))
	b := f.PutEvent("oid-1", "", "item:7", *mailboxEvent("B"))
	refs, err := f.Marked(ctx, "oid-1", []string{b, theirs, "event-404", a})
	if want := []contracts.MailboxEventRef{{EventID: b, Key: "item:7"}, {EventID: a, Key: "appointment:1"}}; err != nil || !slices.Equal(refs, want) {
		t.Errorf("Marked = %v, %v; want %v", refs, err, want)
	}
	if calls := f.Calls(); !slices.Equal(calls[len(calls)-1].EventIDs, []string{b, theirs, "event-404", a}) {
		t.Errorf("the call recorded %+v", calls[len(calls)-1])
	}
	if _, err := f.Marked(ctx, "oid-1", make([]string, contracts.MailboxMarkedMax+1)); err == nil {
		t.Error("Marked of 101 ids = nil, want a failure")
	}
}

// Versions answers the default calendar's keyed events from from, with
// their keys and change keys, and a secondary calendar's every event
// overlapping the window with no key; an update naming a Key marks the
// event, and every write moves the change key Apply answers.
func TestFakeMailboxCalendar_Versions(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	f.SeedForeign("oid-1", FakeBusyEvent{StartsAt: Start, EndsAt: Start.Add(time.Hour), ShowAs: contracts.ShowAsBusy})
	early := *mailboxEvent("Early")
	early.StartsAt, early.EndsAt = Start.Add(-48*time.Hour), Start.Add(-47*time.Hour)
	f.PutEvent("oid-1", "", "appointment:9", early)
	copied := f.PutEvent("oid-1", "", "appointment:1", *mailboxEvent("Standup"))
	cal := f.PutCalendar("oid-1", "Vantigo", true)
	own := f.PutOwnEvent("oid-1", cal, *mailboxEvent("Made in Outlook"))
	tagged := f.PutEvent("oid-1", cal, "appointment:2", *mailboxEvent("Tagged"))
	past := *mailboxEvent("Past")
	past.StartsAt, past.EndsAt = Start.AddDate(0, 0, -60), Start.AddDate(0, 0, -60).Add(time.Hour)
	f.PutOwnEvent("oid-1", cal, past)

	v, err := f.Versions(ctx, "oid-1", "", Start, time.Time{})
	if err != nil || !v.Complete || len(v.Events) != 1 || v.Events[0].EventID != copied || v.Events[0].Key != "appointment:1" || v.Events[0].ChangeKey == "" {
		t.Fatalf("Versions(default) = %+v, %v; want the keyed event from Start alone", v, err)
	}
	from, to := Start.AddDate(0, 0, -30), Start.AddDate(0, 0, 30)
	v, err = f.Versions(ctx, "oid-1", cal, from, to)
	if err != nil || !v.Complete || len(v.Events) != 2 || v.Events[0].EventID != own || v.Events[1].EventID != tagged || v.Events[0].Key != "" || v.Events[1].Key != "" {
		t.Errorf("Versions(secondary) = %+v, %v; want both events of the window, no key", v, err)
	}
	if c := f.Calls(); c[len(c)-1].Method != "Versions" || !c[len(c)-1].From.Equal(from) || !c[len(c)-1].To.Equal(to) {
		t.Errorf("the call recorded %+v", c[len(c)-1])
	}

	was := v.Events[0].ChangeKey
	res, err := f.Apply(ctx, "oid-1", cal, []contracts.MailboxOp{{Kind: contracts.MailboxOpUpdate, EventID: own, Key: "appointment:3", Event: mailboxEvent("Taken in")}})
	if err != nil || res[0].ChangeKey == "" || res[0].ChangeKey == was {
		t.Fatalf("the update = %+v, %v; want a new change key", res, err)
	}
	if got := f.EventsIn("oid-1", cal); got[0].Key != "appointment:3" || got[0].ChangeKey != res[0].ChangeKey {
		t.Errorf("after the update %+v, want the event marked and its change key the one answered", got[0])
	}
}

// Events answers the events of that calendar whole, in the order asked —
// not one of another calendar, not one gone — with the flags the person's
// edits set; EditEvent, TouchEvent, SetMeeting and MoveEvent move the
// change key, SetHTMLBody does not, and MoveEvent answers a new id.
func TestFakeMailboxCalendar_EventsAndEdits(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	a := f.PutEvent("oid-1", "", "appointment:1", *mailboxEvent("A"))
	b := f.PutEvent("oid-1", "", "appointment:2", *mailboxEvent("B"))
	cal := f.PutCalendar("oid-1", "Vantigo", true)
	elsewhere := f.PutEvent("oid-1", cal, "appointment:3", *mailboxEvent("C"))
	changeKey := func(id string) string {
		for _, e := range append(f.EventsIn("oid-1", ""), f.EventsIn("oid-1", cal)...) {
			if e.ID == id {
				return e.ChangeKey
			}
		}
		return ""
	}

	was := changeKey(a)
	f.EditEvent("oid-1", a, func(e *contracts.MailboxEvent) { e.Subject = "A, edited" })
	if changeKey(a) == was {
		t.Error("EditEvent left the change key")
	}
	was = changeKey(b)
	f.TouchEvent("oid-1", b)
	if changeKey(b) == was {
		t.Error("TouchEvent left the change key")
	}
	was = changeKey(b)
	f.SetHTMLBody("oid-1", b, true)
	if changeKey(b) != was {
		t.Error("SetHTMLBody moved the change key")
	}
	f.SetMeeting("oid-1", b, true)
	if changeKey(b) == was {
		t.Error("SetMeeting left the change key")
	}

	got, err := f.Events(ctx, "oid-1", "", []string{b, elsewhere, "event-404", a})
	if err != nil || len(got) != 2 {
		t.Fatalf("Events = %+v, %v; want b and a alone", got, err)
	}
	if got[0].EventID != b || !got[0].Meeting || got[0].TextBody || got[0].Key != "appointment:2" {
		t.Errorf("Events[0] = %+v, want b, a meeting, its body HTML", got[0])
	}
	if got[1].EventID != a || got[1].Event.Subject != "A, edited" || !got[1].TextBody || got[1].ChangeKey != changeKey(a) || got[1].Foreign {
		t.Errorf("Events[1] = %+v, want a as edited, its body text", got[1])
	}
	if c := f.Calls(); c[len(c)-1].Method != "Events" || !slices.Equal(c[len(c)-1].EventIDs, []string{b, elsewhere, "event-404", a}) {
		t.Errorf("the call recorded %+v", c[len(c)-1])
	}

	moved := f.MoveEvent("oid-1", a, cal)
	if moved == "" || moved == a {
		t.Fatalf("MoveEvent = %q, want a new id", moved)
	}
	if got, _ := f.Events(ctx, "oid-1", "", []string{a, moved}); len(got) != 0 {
		t.Errorf("Events(default) after the move = %+v, want neither id", got)
	}
	if got, _ := f.Events(ctx, "oid-1", cal, []string{moved}); len(got) != 1 || got[0].Key != "appointment:1" {
		t.Errorf("Events(secondary) after the move = %+v, want the moved event, its key kept", got)
	}
	f.SetOtherInstallation("oid-1", b, true)
	if got, _ := f.Events(ctx, "oid-1", "", []string{b}); len(got) != 1 || !got[0].Foreign || got[0].Key != "" {
		t.Errorf("Events of another installation's event = %+v, want Foreign, no key", got)
	}
	f.DeleteEvent("oid-1", b)
	if got, _ := f.Events(ctx, "oid-1", "", []string{b}); len(got) != 0 {
		t.Errorf("Events after DeleteEvent = %+v, want none", got)
	}
	if _, err := f.Events(ctx, "oid-1", "", make([]string, contracts.MailboxMarkedMax+1)); err == nil {
		t.Error("Events of 101 ids = nil, want a failure")
	}
}

// Versions and Events are refused under cleanup and while unavailable, and
// answer a mailbox's own refusal and FailWith's error.
func TestFakeMailboxCalendar_WritebackRefusals(t *testing.T) {
	ctx := context.Background()
	f := NewFakeMailboxCalendar()
	for _, state := range []string{contracts.MailboxCleanup, contracts.MailboxUnavailable} {
		f.SetStatus(state, "switch_off")
		if _, err := f.Versions(ctx, "oid-1", "", Start, time.Time{}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("Versions under %s = %v, want ErrMailboxUnavailable", state, err)
		}
		if _, err := f.Events(ctx, "oid-1", "", []string{"event-1"}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
			t.Errorf("Events under %s = %v, want ErrMailboxUnavailable", state, err)
		}
	}
	f.SetStatus(contracts.MailboxAvailable, "")
	f.SetUnknown("oid-2", true)
	if _, err := f.Versions(ctx, "oid-2", "", Start, time.Time{}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Versions of an unknown mailbox = %v, want ErrMailboxUnknown", err)
	}
	f.SetMailboxError("oid-3", contracts.ErrMailboxDenied)
	if _, err := f.Events(ctx, "oid-3", "", nil); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("Events of a denied mailbox = %v, want ErrMailboxDenied", err)
	}
	boom := errors.New("boom")
	f.FailWith("Versions", boom)
	f.FailWith("Events", boom)
	if _, err := f.Versions(ctx, "oid-1", "", Start, time.Time{}); !errors.Is(err, boom) {
		t.Errorf("Versions under FailWith = %v, want boom", err)
	}
	if _, err := f.Events(ctx, "oid-1", "", nil); !errors.Is(err, boom) {
		t.Errorf("Events under FailWith = %v, want boom", err)
	}
}
