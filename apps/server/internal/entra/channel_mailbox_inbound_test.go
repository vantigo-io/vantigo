package entra_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// inboxSelect is the $select every fresh Delta sends.
const inboxSelect = "id,receivedDateTime,internetMessageId,conversationId"

// inboxMIME is a message a customer sent the shared mailbox.
func inboxMIME(subject string) []byte {
	return []byte("From: Kari Kunde <kari@kunde.example>\r\nTo: post@nordlys.no\r\nSubject: " + subject +
		"\r\nMessage-Id: <" + strings.ToLower(subject) + "@kunde.example>\r\n\r\nHei.\r\n")
}

// A fresh Delta asks the Inbox's message delta from since, in UTC, with
// $select's four fields, immutable ids and pages of fifty; a page cut
// answers its next link, the last its delta link and Done; a link is
// followed on our own path with its query alone. The changes after it: a
// message removed is Removed, its id alone; one marked read an update.
func TestChannelMailbox_DeltaReadsTheInbox(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	oslo := time.FixedZone("CET", 3600)
	since := time.Date(2026, 10, 29, 9, 0, 0, 0, oslo)
	g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: since.Add(-time.Minute), MIME: inboxMIME("Before")})
	var ids []string
	for i, subject := range []string{"One", "Two", "Three"} {
		ids = append(ids, g.AddInboxMessage(post.ID, graphtest.InboxMessage{
			ReceivedAt: since.Add(time.Duration(i) * time.Minute), MIME: inboxMIME(subject),
			InternetMessageID: "<" + strings.ToLower(subject) + "@kunde.example>", ConversationID: "AAQkADconv" + subject,
		}))
	}
	g.Configure(func(g *graphtest.FakeGraph) { g.DeltaPageSize = 2 })

	from := len(g.Calls())
	page, err := p.Delta(ctx, info.Mailbox, "inbox", "", since)
	if err != nil {
		t.Fatalf("Delta afresh: %v", err)
	}
	if len(page.Items) != 2 || page.Done || page.Link == "" {
		t.Fatalf("the first page: %+v", page)
	}
	calls := graphCalls(g, from)
	if len(calls) != 1 || calls[0].Path != "/v1.0/users/"+info.Mailbox+"/mailFolders/inbox/messages/delta" {
		t.Fatalf("the calls: %+v", calls)
	}
	q, _ := url.ParseQuery(calls[0].Query)
	if q.Get("$filter") != "receivedDateTime ge 2026-10-29T08:00:00Z" || q.Get("$select") != inboxSelect || len(q) != 2 {
		t.Errorf("the fresh query: %s", calls[0].Query)
	}
	if calls[0].Prefer != `IdType="ImmutableId", odata.maxpagesize=50` {
		t.Errorf("the Prefer: %q", calls[0].Prefer)
	}
	first := page.Items[0]
	if first.ID != ids[0] || !first.ReceivedAt.Equal(since) || first.ReceivedAt.Location() != time.UTC ||
		first.MessageID != "<one@kunde.example>" || first.ConversationID != "AAQkADconvOne" || first.Removed {
		t.Errorf("the first item: %+v", first)
	}

	from = len(g.Calls())
	page, err = p.Delta(ctx, info.Mailbox, "inbox", page.Link, time.Time{})
	if err != nil {
		t.Fatalf("Delta with the next link: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != ids[2] || !page.Done || page.Link == "" {
		t.Fatalf("the last page: %+v", page)
	}
	calls = graphCalls(g, from)
	if len(calls) != 1 || calls[0].Path != "/v1.0/users/"+info.Mailbox+"/mailFolders/inbox/messages/delta" ||
		!strings.HasPrefix(calls[0].Query, "$skiptoken=") || calls[0].Prefer != `IdType="ImmutableId", odata.maxpagesize=50` {
		t.Errorf("the next link followed: %+v", calls)
	}

	g.RemoveInboxMessage(post.ID, ids[0])
	g.MarkInboxMessageRead(post.ID, ids[1])
	page, err = p.Delta(ctx, info.Mailbox, "inbox", page.Link, time.Time{})
	if err != nil {
		t.Fatalf("Delta with the delta link: %v", err)
	}
	if len(page.Items) != 2 || !page.Done {
		t.Fatalf("the changes: %+v", page)
	}
	// In no promised order.
	byID := map[string]contracts.MailItem{}
	for _, it := range page.Items {
		byID[it.ID] = it
	}
	if read := byID[ids[1]]; read.Removed || !read.ReceivedAt.Equal(since.Add(time.Minute)) || read.MessageID != "<two@kunde.example>" {
		t.Errorf("the message read: %+v", read)
	}
	if gone := byID[ids[0]]; gone != (contracts.MailItem{ID: ids[0], Removed: true}) {
		t.Errorf("the message removed: %+v", gone)
	}
}

// A link is followed only when it is this mailbox's Inbox delta: another
// mailbox's, another host, plain http, a user, a fragment, a dot segment,
// a percent-encoded path, no query, another resource — each refused with
// no call. Graph's own odd case is taken.
func TestChannelMailbox_DeltaLinksChecked(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	base := "https://graph.microsoft.com/v1.0/users/" + info.Mailbox
	other := uuid.NewString()

	from := len(g.Calls())
	for _, link := range []string{
		"https://graph.microsoft.com/v1.0/users/" + other + "/mailFolders/inbox/messages/delta?$deltatoken=x",
		"https://graph.example.com/v1.0/users/" + info.Mailbox + "/mailFolders/inbox/messages/delta?$deltatoken=x",
		"http://graph.microsoft.com/v1.0/users/" + info.Mailbox + "/mailFolders/inbox/messages/delta?$deltatoken=x",
		"https://evil@graph.microsoft.com/v1.0/users/" + info.Mailbox + "/mailFolders/inbox/messages/delta?$deltatoken=x",
		base + "/mailFolders/inbox/messages/delta?$deltatoken=x#frag",
		base + "/mailFolders/../messages/delta?$deltatoken=x",
		base + "/mailFolders/%69nbox/messages/delta?$deltatoken=x",
		base + "/mailFolders/inbox/messages/delta",
		base + "/mailFolders/inbox/child/messages/delta?$deltatoken=x",
		base + "/messages/delta?$deltatoken=x",
		base + "/calendarView/delta?$deltatoken=x",
		"https://graph.microsoft.com/beta/users/" + info.Mailbox + "/mailFolders/inbox/messages/delta?$deltatoken=x",
		"not a link",
	} {
		if _, err := p.Delta(ctx, info.Mailbox, "inbox", link, time.Time{}); err == nil || errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("%s: %v, want a refusal", link, err)
		}
	}
	if calls := graphCalls(g, from); len(calls) != 0 {
		t.Errorf("a refused link reached Graph: %+v", calls)
	}

	// The fake answers mailfolders/{the Inbox's id}; Graph may write the
	// user's id upper-case and the folder by name.
	page, err := p.Delta(ctx, info.Mailbox, "inbox", "", time.Now().Add(-time.Hour))
	if err != nil || !page.Done || !strings.Contains(page.Link, "/mailfolders/AQMkADinbox") {
		t.Fatalf("a fresh Delta: %+v, %v", page, err)
	}
	if _, err := p.Delta(ctx, info.Mailbox, "inbox", page.Link, time.Time{}); err != nil {
		t.Errorf("a lower-case mailfolders link: %v", err)
	}
	odd := strings.Replace(page.Link, info.Mailbox, strings.ToUpper(info.Mailbox), 1)
	odd = strings.Replace(odd, "/mailfolders/", "/MailFolders/", 1)
	if _, err := p.Delta(ctx, info.Mailbox, "inbox", odd, time.Time{}); err != nil {
		t.Errorf("an odd-cased link: %v", err)
	}
}

// An item whose id is no id Graph could have answered is left out, the
// rest of the page kept.
func TestChannelMailbox_DeltaLeavesOutABadID(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	now := time.Now()
	g.AddInboxMessage(post.ID, graphtest.InboxMessage{ID: "..", ReceivedAt: now, MIME: inboxMIME("Dots")})
	g.AddInboxMessage(post.ID, graphtest.InboxMessage{ID: "with space", ReceivedAt: now, MIME: inboxMIME("Space")})
	good := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: now, MIME: inboxMIME("Good")})
	page, err := p.Delta(context.Background(), info.Mailbox, "inbox", "", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != good {
		t.Errorf("the items: %+v", page.Items)
	}
}

// A delta link Graph no longer takes — 410, or 400 syncStateNotFound — is
// ErrMailboxDeltaExpired.
func TestChannelMailbox_DeltaExpired(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	page, err := p.Delta(ctx, info.Mailbox, "inbox", "", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusGone, http.StatusBadRequest} {
		g.ExpireMailDelta(post.ID, status)
		if _, err := p.Delta(ctx, info.Mailbox, "inbox", page.Link, time.Time{}); !errors.Is(err, contracts.ErrMailboxDeltaExpired) {
			t.Errorf("%d: %v, want ErrMailboxDeltaExpired", status, err)
		}
	}
}

// Delta's and Raw's refusals before any call: a mailbox Check never
// recorded, not an id, a folder that is no segment, a message id that is
// no id, the switch off.
func TestChannelMailbox_InboundRefusedBeforeAnyCall(t *testing.T) {
	t.Parallel()
	h, g, o := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: time.Now(), MIME: inboxMIME("Hei")})
	ctx := context.Background()
	since := time.Now().Add(-time.Hour)

	from := len(g.Calls())
	for _, mailbox := range []string{uuid.NewString(), "not-an-id"} {
		if _, err := p.Delta(ctx, mailbox, "inbox", "", since); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("Delta of %s: %v, want ErrMailboxUnknown", mailbox, err)
		}
		if _, err := p.Raw(ctx, mailbox, id, 1<<20); !errors.Is(err, contracts.ErrMailboxUnknown) {
			t.Errorf("Raw of %s: %v, want ErrMailboxUnknown", mailbox, err)
		}
	}
	for _, folder := range []string{"", "..", "inbox/child", "a b"} {
		if _, err := p.Delta(ctx, info.Mailbox, folder, "", since); err == nil {
			t.Errorf("the folder %q: no refusal", folder)
		}
	}
	if _, err := p.Delta(ctx, info.Mailbox, "inbox", "", time.Time{}); err == nil {
		t.Error("a fresh Delta with no since: no refusal")
	}
	for _, bad := range []string{"", "..", "%2e%2e", "a b", strings.Repeat("A", 513)} {
		if _, err := p.Raw(ctx, info.Mailbox, bad, 1<<20); err == nil || errors.Is(err, contracts.ErrMailItemGone) {
			t.Errorf("Raw of the id %q: %v, want a refusal", bad, err)
		}
	}
	setMailChannels(t, o, false)
	if _, err := p.Delta(ctx, info.Mailbox, "inbox", "", since); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Delta with the switch off: %v", err)
	}
	if _, err := p.Raw(ctx, info.Mailbox, id, 1<<20); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Raw with the switch off: %v", err)
	}
	if calls := graphCalls(g, from); len(calls) != 0 {
		t.Errorf("a refusal reached Graph: %+v", calls)
	}
}

// Graph's refusals of Delta and Raw are Send's: 403 ErrMailboxDenied, a
// mailbox gone ErrMailboxGone, a throttle a failure to retry.
func TestChannelMailbox_InboundRefusedByGraph(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: time.Now(), MIME: inboxMIME("Hei")})
	ctx := context.Background()
	since := time.Now().Add(-time.Hour)
	delta := func() error { _, err := p.Delta(ctx, info.Mailbox, "inbox", "", since); return err }
	raw := func() error { _, err := p.Raw(ctx, info.Mailbox, id, 1<<20); return err }

	for name, call := range map[string]func() error{"Delta": delta, "Raw": raw} {
		g.Throttle(2)
		if err := call(); err == nil || errors.Is(err, contracts.ErrMailboxUnavailable) || errors.Is(err, contracts.ErrMailboxDenied) {
			t.Errorf("%s throttled: %v, want a failure", name, err)
		}
		g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied = true })
		if err := call(); !errors.Is(err, contracts.ErrMailboxDenied) {
			t.Errorf("%s 403: %v", name, err)
		}
		g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied, u.NoMailbox = false, true })
		if err := call(); !errors.Is(err, contracts.ErrMailboxGone) {
			t.Errorf("%s with no mailbox: %v", name, err)
		}
		g.EditUser(post.ID, func(u *graphtest.User) { u.NoMailbox = false })
	}
}

// Raw answers the MIME byte for byte, asked with immutable ids; above max
// ErrMailTooLarge; a message gone ErrMailItemGone.
func TestChannelMailbox_Raw(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	mime := append(inboxMIME("Hei"), bytes.Repeat([]byte("x"), 4096)...)
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: time.Now(), MIME: mime})

	from := len(g.Calls())
	got, err := p.Raw(ctx, info.Mailbox, id, int64(len(mime)))
	if err != nil || !bytes.Equal(got, mime) {
		t.Fatalf("Raw: %v, %d bytes of %d", err, len(got), len(mime))
	}
	calls := graphCalls(g, from)
	if len(calls) != 1 || calls[0].Method != http.MethodGet || calls[0].Path != "/v1.0/users/"+info.Mailbox+"/messages/"+id+"/$value" ||
		calls[0].Prefer != `IdType="ImmutableId"` {
		t.Errorf("the calls: %+v", calls)
	}
	if _, err := p.Raw(ctx, info.Mailbox, id, int64(len(mime))-1); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("over max: %v, want ErrMailTooLarge", err)
	}
	g.RemoveInboxMessage(post.ID, id)
	if _, err := p.Raw(ctx, info.Mailbox, id, int64(len(mime))); !errors.Is(err, contracts.ErrMailItemGone) || errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("a message gone: %v, want ErrMailItemGone", err)
	}
}

// syncBuffer is a log sink safe for the client's concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Nothing of a message — its subject, its sender, its Message-Id — reaches
// a log line, at debug level, through a page read, a raw read or a
// refusal.
func TestChannelMailbox_InboundLogsNothingOfAMessage(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	logs := &syncBuffer{}
	d := h.Deps()
	d.Logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := entra.Module().ChannelMailbox(d)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	id := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: time.Now(), MIME: inboxMIME("Hemmelig"),
		InternetMessageID: "<hemmelig@kunde.example>", ConversationID: "AAQkADhemmelig"})
	g.AddInboxMessage(post.ID, graphtest.InboxMessage{ID: "..", ReceivedAt: time.Now(), MIME: inboxMIME("Hemmelig")})
	if _, err := p.Delta(ctx, info.Mailbox, "inbox", "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Raw(ctx, info.Mailbox, id, 1<<20); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Raw(ctx, info.Mailbox, id, 10)
	g.RemoveInboxMessage(post.ID, id)
	_, _ = p.Raw(ctx, info.Mailbox, id, 1<<20)
	g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied = true })
	_, _ = p.Delta(ctx, info.Mailbox, "inbox", "", time.Now().Add(-time.Hour))
	out := strings.ToLower(logs.String())
	for _, secret := range []string{"hemmelig", "kari", "kunde.example"} {
		if strings.Contains(out, secret) {
			t.Errorf("a log line names %q: %s", secret, logs.String())
		}
	}
}
