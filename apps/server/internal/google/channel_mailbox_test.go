package google_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Gmail mail channels (Google Workspace design G33, G34): the switch and the
// scopes Test connection asks, the provider's status, Check's refusals, the
// send with its Message-ID read back, the delta over the listing and the
// history, the raw read, the labels, against the fake Gmail.

// The mailbox the fixture's channel uses.
const postAddress = "post@nordlys.no"

// mailFixture is a connected organisation with every scope and the Gmail
// ones delegated, Mail channels on and tested, and post@ a licensed user.
type mailFixture struct {
	h    *harness
	g    *googletest.FakeGoogle
	o    *modtest.Client
	p    contracts.ChannelMailbox
	post googletest.User
}

func newMailFixture(t *testing.T, opts ...modtest.Option) *mailFixture {
	t.Helper()
	h := newHarness(t, opts...)
	h.g.Delegate(saClientID, append(slices.Clone(everyScope), gmailScopes...)...)
	o, _ := configured(t, h)
	setMailChannels(t, o, true)
	if r := testConnection(t, o); r.Status != "connected" {
		t.Fatalf("the test: %+v", r)
	}
	post := h.g.AddUser(googletest.User{PrimaryEmail: postAddress, GivenName: "Post", FamilyName: "Nordlys", Aliases: []string{"kontakt@nordlys.no"}})
	return &mailFixture{h: h, g: h.g, o: o, p: google.Module().ChannelMailbox(h.Deps()), post: post}
}

// checked is post@ checked: its Directory id.
func (f *mailFixture) checked(t *testing.T) string {
	t.Helper()
	info, err := f.p.Check(context.Background(), postAddress)
	if err != nil || !info.Shared {
		t.Fatalf("Check: %+v %v", info, err)
	}
	return info.Mailbox
}

// gmailCalls are the fake's Gmail calls since from.
func (f *mailFixture) gmailCalls(from int) []googletest.Call {
	var out []googletest.Call
	for _, c := range f.g.Calls()[from:] {
		if c.Host == googletest.GmailHost {
			out = append(out, c)
		}
	}
	return out
}

// mailFrom is a message from a customer to post@, its Message-ID id.
func mailFrom(id, inReplyTo, text string) []byte {
	b := fmt.Sprintf("From: Kari <kari@example.test>\r\nTo: %s\r\nSubject: Hello\r\nMessage-ID: %s\r\n", postAddress, id)
	if inReplyTo != "" {
		b += "In-Reply-To: " + inReplyTo + "\r\n"
	}
	return []byte(b + "Date: Thu, 29 Oct 2026 10:00:00 +0000\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + text + "\r\n")
}

func TestMailChannels_TheTestAsksTheGmailScopesAsAChannelMailbox(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	f.checked(t)
	before := len(f.g.Calls())
	if r := testConnection(t, f.o); r.Status != "connected" || !slices.Contains(r.Granted, googletest.ScopeGmailModify) {
		t.Fatalf("test: %+v", r)
	}
	asked := 0
	for _, c := range f.g.Calls()[before:] {
		if c.Host == googletest.TokenHost && slices.Contains(gmailScopes, c.Scope) {
			asked++
			if c.Subject != postAddress {
				t.Errorf("a Gmail scope asked as %q, want the channel mailbox", c.Subject)
			}
		}
	}
	if asked != 2 {
		t.Errorf("Gmail scopes asked %d times, want 2", asked)
	}
	// The mailbox suspended since: asked again as the admin, still granted.
	f.g.SetUser(f.post.ID, func(u *googletest.User) { u.Suspended = true })
	if r := testConnection(t, f.o); r.Status != "connected" || !slices.Contains(r.Granted, googletest.ScopeGmailSend) {
		t.Errorf("test with the mailbox suspended: %+v", r)
	}
}

func TestChannelMailbox_Status(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	ctx := context.Background()
	if f.p.Key() != "google" || f.p.DisplayName() != "Google Workspace" || f.p.MaxMessage() != 25_000_000 {
		t.Errorf("Key, DisplayName, MaxMessage = %q, %q, %d", f.p.Key(), f.p.DisplayName(), f.p.MaxMessage())
	}
	if st, err := f.p.Status(ctx); err != nil || st.State != contracts.MailboxAvailable || st.Reason != contracts.MailboxTenantWide {
		t.Errorf("ready: %+v %v, want available tenant_wide", st, err)
	}
	setMailChannels(t, f.o, false)
	if st, _ := f.p.Status(ctx); st.State != contracts.MailboxUnavailable || st.Reason != "switch_off" {
		t.Errorf("off: %+v", st)
	}
	if _, err := f.p.Check(ctx, postAddress); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Check while off: %v, want ErrMailboxUnavailable", err)
	}
	setMailChannels(t, f.o, true)
	f.h.Exec(t, `UPDATE google.connection SET status = 'failing', status_reason = 'credential_invalid'`)
	if st, _ := f.p.Status(ctx); st.State != contracts.MailboxUnavailable || st.Reason != "credential_invalid" {
		t.Errorf("failing: %+v", st)
	}
}

func TestChannelMailbox_Check(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	ctx := context.Background()

	// A group's address.
	f.g.AddGroup(googletest.Group{Email: "salg@nordlys.no", Name: "Salg"})
	if _, err := f.p.Check(ctx, "salg@nordlys.no"); !errors.Is(err, contracts.ErrMailboxGroup) {
		t.Errorf("a group: %v, want ErrMailboxGroup", err)
	}
	// Nobody's, and not an address at all (no call).
	if _, err := f.p.Check(ctx, "nobody@nordlys.no"); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("unknown: %v, want ErrMailboxUnknown", err)
	}
	before := len(f.g.Calls())
	if _, err := f.p.Check(ctx, "Post <post@nordlys.no>"); !errors.Is(err, contracts.ErrMailboxUnknown) || len(f.g.Calls()) != before {
		t.Errorf("a display name: %v, %d calls", err, len(f.g.Calls())-before)
	}
	// A suspended user, a user without Gmail.
	gone := f.g.AddUser(googletest.User{PrimaryEmail: "gone@nordlys.no", Suspended: true})
	if _, err := f.p.Check(ctx, gone.PrimaryEmail); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("suspended: %v, want ErrMailboxGone", err)
	}
	bare := f.g.AddUser(googletest.User{PrimaryEmail: "bare@nordlys.no"})
	f.g.SetNoGmail(bare.PrimaryEmail, true)
	if _, err := f.p.Check(ctx, bare.PrimaryEmail); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("no Gmail: %v, want ErrMailboxGone", err)
	}
	// A person Staff manages: answered not shared, nothing of the mailbox
	// read, nothing recorded.
	kari := f.g.AddUser(googletest.User{PrimaryEmail: "kari@nordlys.no", GivenName: "Kari", FamilyName: "Nordmann"})
	f.h.Exec(t, `INSERT INTO google.accounts (user_id, employee_number, primary_email, state, created_by_us, created_at, updated_at)
	             VALUES ($1, 'E-1', $2, 'enabled', true, now(), now())`, kari.ID, kari.PrimaryEmail)
	before = len(f.g.Calls())
	info, err := f.p.Check(ctx, kari.PrimaryEmail)
	if err != nil || info.Shared || info.Mailbox != kari.ID {
		t.Errorf("a person's: %+v %v, want not shared", info, err)
	}
	if calls := f.gmailCalls(before); len(calls) != 0 {
		t.Errorf("a person's mailbox was read: %+v", calls)
	}
	// The alias resolves to post@, recorded.
	info, err = f.p.Check(ctx, "kontakt@nordlys.no")
	if err != nil || !info.Shared || info.Mailbox != f.post.ID || info.Address != postAddress || info.DisplayName != "Post Nordlys" {
		t.Fatalf("an alias: %+v %v", info, err)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM google.channel_mailboxes`); n != 1 {
		t.Errorf("rows = %d, want post@ alone", n)
	}
	if conn := getConnection(t, f.o); conn.MailChannels.Mailboxes != 1 {
		t.Errorf("mailboxes = %d, want 1", conn.MailChannels.Mailboxes)
	}
	// Every Gmail call names the mailbox's quota user.
	for _, c := range f.gmailCalls(0) {
		if !strings.Contains(c.Query, "quotaUser=") {
			t.Errorf("%s %s without quotaUser", c.Method, c.Path)
		}
	}
	// Nothing of an address or of Google's words in the log.
	logs := f.h.Logs()
	for _, s := range []string{"@nordlys.no", f.g.Words} {
		if strings.Contains(logs, s) {
			t.Errorf("the log holds %q", s)
		}
	}
	// Forgotten, it sends nothing.
	if err := f.p.Forget(ctx, f.post.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Send(ctx, f.post.ID, mailFrom("<x@vantigo.invalid>", "", "hi"), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send after Forget: %v, want ErrMailboxUnknown", err)
	}
	if err := f.p.Forget(ctx, "not-an-id"); err != nil {
		t.Errorf("Forget of no id: %v", err)
	}
}

func TestChannelMailbox_SendReadsTheMessageIDBack(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	ctx := context.Background()
	gid := f.checked(t)
	ours := "<0f0e0d0c0b0a@vantigo.invalid>"

	// gmail.send not delegated (no token for it cached yet): unavailable,
	// held rather than refused.
	f.g.Delegate(saClientID, append(slices.Clone(everyScope), googletest.ScopeGmailModify)...)
	if _, err := f.p.Send(ctx, gid, mailFrom("<nd@vantigo.invalid>", "", "x"), nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("gmail.send not delegated: %v, want ErrMailboxUnavailable", err)
	}
	f.g.Delegate(saClientID, append(slices.Clone(everyScope), gmailScopes...)...)

	// Gmail keeps it: the answer carries it and the thread.
	sent, err := f.p.Send(ctx, gid, mailFrom(ours, "", "hello"), nil)
	if err != nil || sent.MessageID != ours || sent.ThreadID == "" {
		t.Fatalf("Send: %+v %v", sent, err)
	}
	// Gmail replaces it: the answer carries Gmail's.
	f.g.SetReplaceMessageID(true)
	sent2, err := f.p.Send(ctx, gid, mailFrom("<second@vantigo.invalid>", "", "again"), nil)
	if err != nil || sent2.MessageID == "<second@vantigo.invalid>" || !strings.HasSuffix(sent2.MessageID, "@mail.gmail.com>") {
		t.Errorf("replaced: %+v %v", sent2, err)
	}
	var send googletest.Call
	for _, c := range f.gmailCalls(0) {
		if c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/messages/send") {
			send = c
		}
	}
	if send.Path != "/upload/gmail/v1/users/me/messages/send" || !strings.Contains(send.Query, "uploadType=media") || !strings.Contains(send.Query, "quotaUser="+gid) {
		t.Errorf("the send: %+v", send)
	}

	// Too large: refused unsent; a 413 is too large too.
	before := len(f.gmailCalls(0))
	if _, err := f.p.Send(ctx, gid, bytes.Repeat([]byte("a"), 25_000_001), nil); !errors.Is(err, contracts.ErrMailTooLarge) || len(f.gmailCalls(0)) != before {
		t.Errorf("over 25 MB: %v", err)
	}
	f.g.SetGmailSendLimit(100)
	if _, err := f.p.Send(ctx, gid, mailFrom("<big@vantigo.invalid>", "", strings.Repeat("x", 200)), nil); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("a 413: %v, want ErrMailTooLarge", err)
	}
	f.g.SetGmailSendLimit(0)

	// A blind copy the Bcc header does not carry: refused unsent.
	if _, err := f.p.Send(ctx, gid, mailFrom("<bcc@vantigo.invalid>", "", "x"), []string{"hidden@example.test"}); !errors.Is(err, contracts.ErrBccUnsupported) {
		t.Errorf("Bcc not in the MIME: %v", err)
	}
	withBcc := append([]byte("Bcc: Hidden@Example.test\r\n"), mailFrom("<bcc2@vantigo.invalid>", "", "x")...)
	if _, err := f.p.Send(ctx, gid, withBcc, []string{"hidden@example.test"}); err != nil {
		t.Errorf("Bcc in the MIME: %v", err)
	}

	// The mailbox suspended: gone.
	f.g.SetUser(gid, func(u *googletest.User) { u.Suspended = true })
	if _, err := f.p.Send(ctx, gid, mailFrom("<s@vantigo.invalid>", "", "x"), nil); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("suspended: %v, want ErrMailboxGone", err)
	}
	f.g.SetUser(gid, func(u *googletest.User) { u.Suspended = false })
	// A 403: denied.
	f.g.FailOn("POST /messages/send", http.StatusForbidden)
	if _, err := f.p.Send(ctx, gid, mailFrom("<d@vantigo.invalid>", "", "x"), nil); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("403: %v, want ErrMailboxDenied", err)
	}
	f.g.FailOn("POST /messages/send", 0)
	// A 5xx is not sent again.
	before = len(f.gmailCalls(0))
	f.g.FailNext(http.StatusServiceUnavailable)
	if _, err := f.p.Send(ctx, gid, mailFrom("<e@vantigo.invalid>", "", "x"), nil); err == nil {
		t.Error("a 503 answered nil")
	}
	if n := len(f.gmailCalls(0)) - before; n != 1 {
		t.Errorf("a 503 was tried %d times, want once", n)
	}
	if strings.Contains(f.h.Logs(), f.g.Words) || strings.Contains(f.h.Logs(), postAddress) {
		t.Error("the log holds Google's words or the address")
	}
}

func TestChannelMailbox_DeltaListsThenFollowsTheHistory(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	ctx := context.Background()
	gid := f.checked(t)
	start := f.h.Now()
	old := f.g.Deliver(postAddress, mailFrom("<old@example.test>", "", "old"), start.Add(-2*time.Hour))
	first := f.g.Deliver(postAddress, mailFrom("<first@example.test>", "", "first"), start.Add(time.Minute))
	second := f.g.Deliver(postAddress, mailFrom("<second@example.test>", "<first@example.test>", "second"), start.Add(2*time.Minute))

	d, err := f.p.Delta(ctx, gid, "inbox", "", start)
	if err != nil || !d.Done || !strings.HasPrefix(d.Link, "g1.") {
		t.Fatalf("fresh: %+v %v", d, err)
	}
	got := map[string]contracts.MailItem{}
	for _, it := range d.Items {
		got[it.ID] = it
	}
	if _, ok := got[old.ID]; ok || len(got) != 2 {
		t.Errorf("fresh items = %+v, want first and second", d.Items)
	}
	if it := got[second.ID]; it.MessageID != "<second@example.test>" || it.ConversationID != first.ThreadID ||
		!it.ReceivedAt.Equal(start.Add(2*time.Minute).Truncate(time.Millisecond)) {
		t.Errorf("second = %+v, want its Message-ID, first's thread and its time", it)
	}

	// Nothing new: an empty page, done.
	d, err = f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{})
	if err != nil || !d.Done || len(d.Items) != 0 {
		t.Fatalf("nothing new: %+v %v", d, err)
	}
	// A new one, one archived, one deleted.
	third := f.g.Deliver(postAddress, mailFrom("<third@example.test>", "", "third"), start.Add(3*time.Minute))
	f.g.RemoveFromInbox(postAddress, first.ID)
	f.g.DeleteGmailMessage(postAddress, second.ID)
	d, err = f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{})
	if err != nil || !d.Done {
		t.Fatalf("history: %+v %v", d, err)
	}
	got = map[string]contracts.MailItem{}
	for _, it := range d.Items {
		got[it.ID] = it
	}
	if it := got[third.ID]; it.Removed || it.MessageID != "<third@example.test>" {
		t.Errorf("third = %+v", it)
	}
	if !got[first.ID].Removed || !got[second.ID].Removed {
		t.Errorf("the archived and the deleted = %+v, want removed", d.Items)
	}

	// Sixty at once: fifty, then ten, then done.
	for i := range 60 {
		f.g.Deliver(postAddress, mailFrom(fmt.Sprintf("<bulk-%d@example.test>", i), "", "bulk"), start.Add(time.Hour))
	}
	d, err = f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{})
	if err != nil || d.Done || len(d.Items) != contracts.MailDeltaPageMax {
		t.Fatalf("bulk page 1: %d items, done %v, %v", len(d.Items), d.Done, err)
	}
	total := len(d.Items)
	for !d.Done {
		if d, err = f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if len(d.Items) > contracts.MailDeltaPageMax {
			t.Fatalf("a page of %d items", len(d.Items))
		}
		total += len(d.Items)
	}
	if total != 60 {
		t.Errorf("bulk items = %d, want 60", total)
	}

	// A stale start: expired.
	f.g.ExpireHistory(postAddress)
	if _, err := f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{}); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("stale: %v, want ErrMailboxDeltaExpired", err)
	}
	// Another mailbox's link, or none of ours: refused unsent.
	before := len(f.g.Calls())
	if _, err := f.p.Delta(ctx, "123456789012345678901", "inbox", d.Link, time.Time{}); err == nil {
		t.Error("another mailbox's link was taken")
	}
	if _, err := f.p.Delta(ctx, gid, "inbox", "https://gmail.googleapis.com/x", time.Time{}); err == nil || errors.Is(err, contracts.ErrMailboxDeltaExpired) {
		t.Errorf("a foreign link: %v", err)
	}
	if len(f.g.Calls()) != before {
		t.Error("a refused link reached Google")
	}
}

func TestChannelMailbox_RawAndMarks(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t)
	ctx := context.Background()
	gid := f.checked(t)
	raw := mailFrom("<raw@example.test>", "", "the body "+strings.Repeat("é", 50))
	msg := f.g.Deliver(postAddress, raw, f.h.Now())

	got, err := f.p.Raw(ctx, gid, msg.ID, 1<<20)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("Raw: %v, equal %v", err, bytes.Equal(got, raw))
	}
	if _, err := mail.ReadMessage(bytes.NewReader(got)); err != nil {
		t.Errorf("Raw does not parse: %v", err)
	}
	if _, err := f.p.Raw(ctx, gid, msg.ID, 100); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("Raw over max: %v, want ErrMailTooLarge", err)
	}
	if _, err := f.p.Raw(ctx, gid, "18cffffffffffff", 1<<20); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Raw of none: %v, want ErrMailItemGone", err)
	}

	// Read, a category, a move to a label made once.
	if err := f.p.Mark(ctx, gid, msg.ID, contracts.MailMark{Kind: contracts.MailMarkRead}); err != nil {
		t.Fatal(err)
	}
	label, err := f.p.EnsureFolder(ctx, gid, "Handled in Vantigo")
	if err != nil || !strings.HasPrefix(label, "Label_") {
		t.Fatalf("EnsureFolder: %q %v", label, err)
	}
	if again, err := f.p.EnsureFolder(ctx, gid, "handled in vantigo"); err != nil || again != label {
		t.Errorf("EnsureFolder again: %q %v, want %q", again, err, label)
	}
	if err := f.p.Mark(ctx, gid, msg.ID, contracts.MailMark{Kind: contracts.MailMarkCategory, Categories: []string{"Vantigo"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.p.Mark(ctx, gid, msg.ID, contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: label}); err != nil {
		t.Fatal(err)
	}
	m, _ := f.g.GmailMessageOf(postAddress, msg.ID)
	labels := f.g.GmailLabels(postAddress)
	if slices.Contains(m.Labels, "UNREAD") || slices.Contains(m.Labels, "INBOX") || !slices.Contains(m.Labels, label) ||
		!slices.Contains(m.Labels, labels["Vantigo"]) {
		t.Errorf("labels = %v (user labels %v)", m.Labels, labels)
	}
	if err := f.p.Mark(ctx, gid, "18cffffffffffff", contracts.MailMark{Kind: contracts.MailMarkRead}); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Mark of none: %v, want ErrMailItemGone", err)
	}
	if err := f.p.Mark(ctx, gid, msg.ID, contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: "INBOX"}); err == nil {
		t.Error("a move to INBOX was taken")
	}

	// The notices: none, v1 polls.
	if err := f.p.Watch(ctx, gid, "inbox"); !errors.Is(err, contracts.ErrWatchUnsupported) {
		t.Errorf("Watch: %v, want ErrWatchUnsupported", err)
	}
	if err := f.p.Unwatch(ctx, gid, "inbox"); err != nil {
		t.Error(err)
	}
	if n, err := f.p.Notified(ctx, time.Time{}); err != nil || len(n) != 0 {
		t.Errorf("Notified: %v %v", n, err)
	}
}

// Every call of the channel mailbox runs on a pool of one connection: none
// holds the pool while it waits for Google (the 00087 lesson).
func TestChannelMailbox_OnAPoolOfOne(t *testing.T) {
	t.Parallel()
	f := newMailFixture(t, modtest.WithPoolMaxConns(1))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gid := f.checked(t)
	if _, err := f.p.Status(ctx); err != nil {
		t.Fatal(err)
	}
	msg := f.g.Deliver(postAddress, mailFrom("<pool@example.test>", "", "pool"), f.h.Now())
	if _, err := f.p.Send(ctx, gid, mailFrom("<p@vantigo.invalid>", "", "x"), nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	d, err := f.p.Delta(ctx, gid, "inbox", "", f.h.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if _, err := f.p.Delta(ctx, gid, "inbox", d.Link, time.Time{}); err != nil {
		t.Fatalf("Delta continued: %v", err)
	}
	if _, err := f.p.Raw(ctx, gid, msg.ID, 1<<20); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	label, err := f.p.EnsureFolder(ctx, gid, "Done")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if err := f.p.Mark(ctx, gid, msg.ID, contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: label}); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := f.p.Forget(ctx, gid); err != nil {
		t.Fatalf("Forget: %v", err)
	}
}
