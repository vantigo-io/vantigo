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
	refs, err := f.Find(ctx, "oid-1", "")
	if err != nil || !slices.Equal(refs, []contracts.MailboxEventRef{{EventID: created, Key: "appointment:1"}}) {
		t.Errorf("Find = %v, %v; want the keyed event alone, not %s", refs, err, foreign)
	}
	res, err = f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpUpdate, EventID: created, Event: mailboxEvent("Retro")}})
	if err != nil || res[0].Gone || res[0].Err != nil {
		t.Fatalf("the update = %v, %v", res, err)
	}
	if got := f.Events("oid-1", ""); len(got) != 2 || got[1].Event.Subject != "Retro" || got[1].Key != "appointment:1" {
		t.Errorf("Events = %+v, want the foreign event and the rewritten one, its key kept", got)
	}
	if res, err := f.Apply(ctx, "oid-1", "", []contracts.MailboxOp{{Kind: contracts.MailboxOpDelete, EventID: created}}); err != nil || res[0].Gone {
		t.Errorf("the delete = %v, %v; want done", res, err)
	}
	if got := f.Events("oid-1", ""); len(got) != 1 || got[0].ID != foreign {
		t.Errorf("Events after the delete = %+v, want the foreign event alone", got)
	}
	if got := len(f.Calls()); got != 4 {
		t.Errorf("len(Calls()) = %d, want 4: three Applies and a Find", got)
	}
}

// EnsureCalendar keeps a calendar that still exists and makes a new one
// otherwise; DeleteCalendar drops it with its events, and Find answers every
// event in a secondary calendar.
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
	if refs, err := f.Find(ctx, "oid-1", id); err != nil || len(refs) != 1 {
		t.Errorf("Find in the secondary calendar = %v, %v; want its one event", refs, err)
	}
	if err := f.DeleteCalendar(ctx, "oid-1", id); err != nil {
		t.Fatalf("DeleteCalendar: %v", err)
	}
	if len(f.Calendars("oid-1")) != 0 || len(f.Events("oid-1", id)) != 0 {
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
	if _, err := f.Find(ctx, "oid-2", ""); !errors.Is(err, contracts.ErrMailboxUnknown) {
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
