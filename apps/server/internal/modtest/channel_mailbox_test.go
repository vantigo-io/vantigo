package modtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fake channel mailbox answers Check for a mailbox set, in any case, and
// ErrMailboxUnknown for one not; Send records the message whole and refuses
// a mailbox it does not hold, a message over MaxMessage and an unavailable
// provider; Forget drops a mailbox, idempotently, until a Check takes it
// again; FailWith refuses a method; every call but the three without the
// network is recorded.
func TestFakeChannelMailbox(t *testing.T) {
	ctx := context.Background()
	f := NewFakeChannelMailbox()
	if f.Key() != "entra" || f.DisplayName() != "Microsoft 365" || f.MaxMessage() != 3_000_000 {
		t.Errorf("Key, DisplayName, MaxMessage = %q, %q, %d", f.Key(), f.DisplayName(), f.MaxMessage())
	}
	if got := NewFakeChannelMailbox().WithKey("other").Key(); got != "other" {
		t.Errorf("WithKey's Key = %q, want other", got)
	}
	post := contracts.ChannelMailboxInfo{Mailbox: "0b9c1f6e-2a4d-4c38-9a51-7e0f3b2d8c11", Address: "post@example.com", DisplayName: "Post", Shared: true}
	f.SetMailbox("Post@Example.com", post)

	if status, err := f.Status(ctx); err != nil || status.State != contracts.MailboxAvailable {
		t.Errorf("Status = %+v, %v; want available", status, err)
	}
	if got, err := f.Check(ctx, "POST@example.com"); err != nil || got != post {
		t.Errorf("Check of the mailbox set = %+v, %v; want %+v", got, err, post)
	}
	if _, err := f.Check(ctx, "nobody@example.com"); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Check of an address not set = %v, want ErrMailboxUnknown", err)
	}

	mime := []byte("Message-Id: <1@vantigo.invalid>\r\n\r\nHello")
	if err := f.Send(ctx, post.Mailbox, mime, []string{"hidden@example.com"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mime[0] = 'X' // the fake keeps its own copy
	sent := f.Sent()
	if len(sent) != 1 || sent[0].Mailbox != post.Mailbox || !strings.HasPrefix(string(sent[0].MIME), "Message-Id:") ||
		!slices.Equal(sent[0].Bcc, []string{"hidden@example.com"}) {
		t.Errorf("Sent = %+v", sent)
	}
	if err := f.Send(ctx, "another", mime, nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send to a mailbox not held = %v, want ErrMailboxUnknown", err)
	}
	f.SetMaxMessage(int64(len(mime) - 1))
	if err := f.Send(ctx, post.Mailbox, mime, nil); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("Send over MaxMessage = %v, want ErrMailTooLarge", err)
	}
	f.SetMaxMessage(FakeChannelMailboxMaxMessage)

	f.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if status, _ := f.Status(ctx); status.State != contracts.MailboxUnavailable || status.Reason != "switch_off" {
		t.Errorf("Status after SetStatus = %+v", status)
	}
	if _, err := f.Check(ctx, post.Address); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Check while unavailable = %v, want ErrMailboxUnavailable", err)
	}
	if err := f.Send(ctx, post.Mailbox, mime, nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Send while unavailable = %v, want ErrMailboxUnavailable", err)
	}
	f.SetStatus(contracts.MailboxAvailable, contracts.MailboxTenantWide)

	for range 2 {
		if err := f.Forget(ctx, post.Mailbox); err != nil {
			t.Errorf("Forget: %v", err)
		}
	}
	if got := f.Forgotten(); !slices.Equal(got, []string{post.Mailbox, post.Mailbox}) {
		t.Errorf("Forgotten = %v", got)
	}
	if err := f.Send(ctx, post.Mailbox, mime, nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send after Forget = %v, want ErrMailboxUnknown", err)
	}
	if _, err := f.Check(ctx, post.Address); err != nil {
		t.Fatalf("Check again: %v", err)
	}
	if err := f.Send(ctx, post.Mailbox, mime, nil); err != nil {
		t.Errorf("Send after a new Check: %v", err)
	}

	boom := errors.New("boom")
	f.FailWith("Send", boom)
	if err := f.Send(ctx, post.Mailbox, mime, nil); !errors.Is(err, boom) {
		t.Errorf("Send under FailWith = %v", err)
	}
	f.FailWith("Send", nil)
	if got := len(f.Sent()); got != 2 {
		t.Errorf("Sent holds %d messages, want 2: the refused sends sent nothing", got)
	}

	var methods []string
	for _, c := range f.Calls() {
		methods = append(methods, c.Method)
	}
	if want := []string{"Status", "Check", "Check", "Send", "Send", "Send", "Status", "Check", "Send", "Forget", "Forget", "Send", "Check", "Send", "Send"}; !slices.Equal(methods, want) {
		t.Errorf("Calls' methods =\n%v\nwant\n%v", methods, want)
	}
	if c := f.Calls()[1]; c.Address != "POST@example.com" {
		t.Errorf("Check's call = %+v, want the address as given", c)
	}
}

// A personal mailbox is answered by Check but never one Send takes, as
// Entra records only a shared one.
func TestFakeChannelMailbox_APersonalMailboxIsNotSentThrough(t *testing.T) {
	ctx := context.Background()
	f := NewFakeChannelMailbox()
	kari := contracts.ChannelMailboxInfo{Mailbox: "5d1e8a2c-7b3f-4e6a-9c0d-1f2e3a4b5c6d", Address: "kari@example.com", DisplayName: "Kari"}
	f.SetMailbox("kari@example.com", kari)
	if got, err := f.Check(ctx, "kari@example.com"); err != nil || got.Shared {
		t.Errorf("Check of a personal mailbox = %+v, %v; want it answered, Shared false", got, err)
	}
	if err := f.Send(ctx, kari.Mailbox, []byte("x"), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send from a personal mailbox = %v, want ErrMailboxUnknown", err)
	}
}

// The fake's Inbox: a fresh Delta answers the messages received at or after
// since, a page at a time, each next link then the delta link; the delta
// link answers the changes since — an addition whatever its received time, a
// removal as Removed —; ExpireDelta refuses every link issued so far, and a
// link of another mailbox is refused. Raw answers the bytes under max,
// ErrMailTooLarge above and ErrMailItemGone for a message removed; both
// refuse an unknown mailbox, an unavailable provider and FailWith.
func TestFakeChannelMailbox_Inbox(t *testing.T) {
	ctx := context.Background()
	f := NewFakeChannelMailbox()
	post := contracts.ChannelMailboxInfo{Mailbox: "0b9c1f6e-2a4d-4c38-9a51-7e0f3b2d8c11", Address: "post@example.com", Shared: true}
	f.SetMailbox(post.Address, post)
	since := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	item := func(id string, received time.Time) contracts.MailItem {
		return contracts.MailItem{ID: id, ReceivedAt: received, MessageID: "<" + id + "@example.com>", ConversationID: "conv-" + id}
	}
	f.AddInbox(post.Mailbox, item("old", since.Add(-time.Hour)), []byte("old"))
	for _, id := range []string{"a", "b", "c"} {
		f.AddInbox(post.Mailbox, item(id, since.Add(time.Minute)), []byte("raw "+id))
	}
	ids := func(items []contracts.MailItem) []string {
		var out []string
		for _, it := range items {
			id := it.ID
			if it.Removed {
				id += " removed"
			}
			out = append(out, id)
		}
		return out
	}

	f.SetDeltaPage(2)
	first, err := f.Delta(ctx, post.Mailbox, "inbox", "", since)
	if err != nil || first.Done || first.Link == "" || !slices.Equal(ids(first.Items), []string{"a", "b"}) {
		t.Fatalf("the first page = %+v, %v; want a and b, a next link", first, err)
	}
	if first.Items[0] != item("a", since.Add(time.Minute)) {
		t.Errorf("an item = %+v", first.Items[0])
	}
	second, err := f.Delta(ctx, post.Mailbox, "inbox", first.Link, time.Time{})
	if err != nil || !second.Done || second.Link == "" || !slices.Equal(ids(second.Items), []string{"c"}) {
		t.Fatalf("the second page = %+v, %v; want c and the delta link", second, err)
	}
	if empty, err := f.Delta(ctx, post.Mailbox, "inbox", second.Link, time.Time{}); err != nil || !empty.Done || len(empty.Items) != 0 {
		t.Errorf("the delta link with no change = %+v, %v", empty, err)
	}

	f.SetDeltaPage(contracts.MailDeltaPageMax)
	f.AddInbox(post.Mailbox, item("moved", since.Add(-48*time.Hour)), []byte("moved"))
	f.RemoveInbox(post.Mailbox, "b")
	f.AddInbox(post.Mailbox, item("a", since.Add(time.Minute)), []byte("raw a")) // a change: answered once
	f.AddInbox(post.Mailbox, item("a", since.Add(time.Minute)), []byte("raw a"))
	changes, err := f.Delta(ctx, post.Mailbox, "inbox", second.Link, time.Time{})
	if err != nil || !changes.Done || !slices.Equal(ids(changes.Items), []string{"moved", "b removed", "a"}) {
		t.Fatalf("the changes = %+v, %v; want moved, b removed, a", changes, err)
	}
	if removed := changes.Items[1]; removed != (contracts.MailItem{ID: "b", Removed: true}) {
		t.Errorf("a removed item = %+v, want its ID alone", removed)
	}

	if _, err := NewFakeChannelMailbox().Delta(ctx, post.Mailbox, "inbox", "", since); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Delta of a mailbox never checked = %v, want ErrMailboxUnknown", err)
	}
	other := contracts.ChannelMailboxInfo{Mailbox: "7f3e2d1c-0b9a-4876-8543-210fedcba987", Address: "other@example.com", Shared: true}
	f.SetMailbox(other.Address, other)
	if _, err := f.Delta(ctx, other.Mailbox, "inbox", changes.Link, time.Time{}); err == nil || errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("another mailbox's link = %v, want a failure", err)
	}
	if _, err := f.Delta(ctx, post.Mailbox, "inbox", "not-a-link", time.Time{}); err == nil {
		t.Error("a link never issued was followed")
	}

	f.ExpireDelta(post.Mailbox)
	for _, link := range []string{first.Link, changes.Link} {
		if _, err := f.Delta(ctx, post.Mailbox, "inbox", link, time.Time{}); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("a link after ExpireDelta = %v, want ErrMailboxDeltaExpired", err)
		}
	}
	fresh, err := f.Delta(ctx, post.Mailbox, "inbox", "", since)
	if err != nil || !fresh.Done || !slices.Equal(ids(fresh.Items), []string{"c", "a"}) {
		t.Errorf("a fresh read after ExpireDelta = %+v, %v; want c and a (b removed, the rest before since)", fresh, err)
	}

	if raw, err := f.Raw(ctx, post.Mailbox, "c", 5); err != nil || string(raw) != "raw c" {
		t.Errorf("Raw of c at its size = %q, %v", raw, err)
	}
	if _, err := f.Raw(ctx, post.Mailbox, "c", 4); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("Raw over max = %v, want ErrMailTooLarge", err)
	}
	if _, err := f.Raw(ctx, post.Mailbox, "b", 1<<20); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Raw of a removed message = %v, want ErrMailItemGone", err)
	}
	if _, err := f.Raw(ctx, "unknown", "c", 1<<20); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Raw of a mailbox not held = %v, want ErrMailboxUnknown", err)
	}

	f.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if _, err := f.Delta(ctx, post.Mailbox, "inbox", "", since); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Delta while unavailable = %v", err)
	}
	if _, err := f.Raw(ctx, post.Mailbox, "c", 1<<20); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Raw while unavailable = %v", err)
	}
	f.SetStatus(contracts.MailboxAvailable, "")

	boom := errors.New("boom")
	f.FailWith("Delta", boom)
	f.FailWith("Raw", boom)
	if _, err := f.Delta(ctx, post.Mailbox, "inbox", "", since); !errors.Is(err, boom) {
		t.Errorf("Delta under FailWith = %v", err)
	}
	if _, err := f.Raw(ctx, post.Mailbox, "c", 1<<20); !errors.Is(err, boom) {
		t.Errorf("Raw under FailWith = %v", err)
	}

	calls := f.Calls()
	if c := calls[0]; c != (ChannelMailboxCall{Method: "Delta", Mailbox: post.Mailbox, Folder: "inbox", Since: since}) {
		t.Errorf("the first Delta's call = %+v", c)
	}
	if c := calls[1]; c.Method != "Delta" || c.Link != first.Link {
		t.Errorf("the second Delta's call = %+v, want its link", c)
	}
	var raw *ChannelMailboxCall
	for i := range calls {
		if calls[i].Method == "Raw" {
			raw = &calls[i]
			break
		}
	}
	if raw == nil || *raw != (ChannelMailboxCall{Method: "Raw", Mailbox: post.Mailbox, MessageID: "c", Max: 5}) {
		t.Errorf("the first Raw's call = %+v", raw)
	}
}

func TestFakeChannelMailbox_NotificationsAndMarks(t *testing.T) {
	ctx := context.Background()
	f := NewFakeChannelMailbox()
	post := contracts.ChannelMailboxInfo{Mailbox: "0b9c1f6e-2a4d-4c38-9a51-7e0f3b2d8c11", Address: "post@example.com", Shared: true}
	f.SetMailbox(post.Address, post)

	// Watch / Unwatch / Notified.
	if f.Watched(post.Mailbox) {
		t.Fatal("Watched before Watch = true")
	}
	if err := f.Watch(ctx, post.Mailbox, "inbox"); err != nil || !f.Watched(post.Mailbox) {
		t.Fatalf("Watch = %v, Watched = %v", err, f.Watched(post.Mailbox))
	}
	if err := f.Watch(ctx, post.Mailbox, "sentitems"); err == nil {
		t.Error("Watch of sentitems succeeded, want error")
	}
	if err := f.Unwatch(ctx, post.Mailbox, "inbox"); err != nil || f.Watched(post.Mailbox) {
		t.Fatalf("Unwatch = %v, Watched = %v", err, f.Watched(post.Mailbox))
	}
	if !slices.Equal(f.WatchCalls(), []string{post.Mailbox, post.Mailbox}) || !slices.Equal(f.UnwatchCalls(), []string{post.Mailbox}) {
		t.Errorf("WatchCalls = %v, UnwatchCalls = %v", f.WatchCalls(), f.UnwatchCalls())
	}

	t0 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	f.Notify(post.Mailbox, t0.Add(2*time.Minute))
	f.Notify("other", t0.Add(time.Minute))
	notices, err := f.Notified(ctx, t0)
	if err != nil || len(notices) != 2 || notices[0].Mailbox != "other" || notices[1].Mailbox != post.Mailbox {
		t.Fatalf("Notified = %+v, %v", notices, err)
	}
	later, err := f.Notified(ctx, t0.Add(90*time.Second))
	if err != nil || len(later) != 1 || later[0].Mailbox != post.Mailbox {
		t.Fatalf("Notified after 90s = %+v, %v", later, err)
	}

	// EnsureFolder & Mark (read, category, move).
	f.AddInbox(post.Mailbox, contracts.MailItem{ID: "m1", ReceivedAt: t0}, []byte("raw m1"))
	folderID, err := f.EnsureFolder(ctx, post.Mailbox, "Handled in Vantigo")
	if err != nil || folderID == "" {
		t.Fatalf("EnsureFolder = %q, %v", folderID, err)
	}
	if sameID, err := f.EnsureFolder(ctx, post.Mailbox, "handled in vantigo"); err != nil || sameID != folderID {
		t.Errorf("second EnsureFolder = %q, %v; want %q", sameID, err, folderID)
	}
	if err := f.Mark(ctx, post.Mailbox, "m1", contracts.MailMark{Kind: contracts.MailMarkRead}); err != nil {
		t.Fatalf("Mark read: %v", err)
	}
	if err := f.Mark(ctx, post.Mailbox, "m1", contracts.MailMark{Kind: contracts.MailMarkCategory, Categories: []string{"Handled"}}); err != nil {
		t.Fatalf("Mark category: %v", err)
	}
	if err := f.Mark(ctx, post.Mailbox, "m1", contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: folderID}); err != nil {
		t.Fatalf("Mark move: %v", err)
	}
	st, ok := f.MessageState(post.Mailbox, "m1")
	if !ok || !st.IsRead || !slices.Equal(st.Categories, []string{"Handled"}) || st.FolderID != folderID || st.FolderName != "Handled in Vantigo" {
		t.Errorf("MessageState = %+v, %v", st, ok)
	}
	// Moving removes it from Inbox Raw, while re-marking by immutable ID remains idempotent.
	if _, err := f.Raw(ctx, post.Mailbox, "m1", 1024); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Raw after move = %v, want ErrMailItemGone", err)
	}
	if err := f.Mark(ctx, post.Mailbox, "m1", contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: folderID}); err != nil {
		t.Errorf("idempotent Mark move: %v", err)
	}
	if err := f.Mark(ctx, post.Mailbox, "missing", contracts.MailMark{Kind: contracts.MailMarkRead}); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Mark missing = %v, want ErrMailItemGone", err)
	}
}
