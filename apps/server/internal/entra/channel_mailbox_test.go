package entra_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newChannelHarness is a connected tenant with the Mail channels switch
// on: the channel mailboxes available.
func newChannelHarness(t *testing.T) (*harness, *graphtest.FakeGraph, *modtest.Client) {
	t.Helper()
	h, g := newTenantHarness(t)
	o := connected(t, h)
	setMailChannels(t, o, true)
	return h, g, o
}

// channelProvider is the module's ChannelMailbox built on the harness's
// Deps, as Compose builds it.
func channelProvider(h *harness) contracts.ChannelMailbox {
	return entra.Module().ChannelMailbox(h.Deps())
}

// graphCalls are the calls to Graph since from, the token endpoint's left
// out.
func graphCalls(g *graphtest.FakeGraph, from int) []graphtest.Call {
	var out []graphtest.Call
	for _, c := range g.Calls()[from:] {
		if c.Host == graphtest.GraphHost {
			out = append(out, c)
		}
	}
	return out
}

// channelMIME is a message as the outbox renders one.
func channelMIME(bcc string) []byte {
	m := "From: post@nordlys.no\r\nTo: kunde@example.com\r\n"
	if bcc != "" {
		m += "Bcc: " + bcc + "\r\n"
	}
	return []byte(m + "Subject: Hei\r\nMessage-Id: <abc@vantigo.invalid>\r\n\r\nHello.\r\n")
}

// sharedMailbox seeds a shared mailbox, post@nordlys.no, its UPN the same.
func sharedMailbox(g *graphtest.FakeGraph, edit ...func(u *graphtest.User)) graphtest.User {
	u := graphtest.User{ID: strings.ToUpper(uuid.NewString()), UserPrincipalName: "post@nordlys.no", Mail: "Post@Nordlys.no", DisplayName: "Post", Shared: true}
	for _, fn := range edit {
		fn(&u)
	}
	return g.AddUser(u)
}

// checked is Check of address, which must reach a mailbox.
func checked(t *testing.T, p contracts.ChannelMailbox, address string) contracts.ChannelMailboxInfo {
	t.Helper()
	info, err := p.Check(context.Background(), address)
	if err != nil {
		t.Fatalf("Check(%s): %v", address, err)
	}
	return info
}

func TestChannelMailbox_KeyNameAndSize(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	p := channelProvider(h)
	if p.Key() != "entra" || p.DisplayName() != "Microsoft 365" || p.MaxMessage() != 3_000_000 {
		t.Errorf("Key %q, DisplayName %q, MaxMessage %d", p.Key(), p.DisplayName(), p.MaxMessage())
	}
}

// Status (plan reading 9): available while the switch is on and the
// connection connected, Reason tenant_wide when the token holds a mail
// role; unavailable switch_off, or the connection's reason.
func TestChannelMailbox_Status(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	p := channelProvider(h)
	ctx := context.Background()
	want := func(state, reason string) {
		t.Helper()
		st, err := p.Status(ctx)
		if err != nil || st.State != state || st.Reason != reason {
			t.Errorf("Status = %+v, %v; want %s/%s", st, err, state, reason)
		}
	}
	want(contracts.MailboxUnavailable, "switch_off")
	o := connected(t, h)
	want(contracts.MailboxUnavailable, "switch_off")
	setMailChannels(t, o, true)
	want(contracts.MailboxAvailable, "")
	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = append(g.Roles, "Mail.Send") })
	testConnection(t, o)
	want(contracts.MailboxAvailable, contracts.MailboxTenantWide)
	h.Exec(t, `UPDATE entra.connection SET status = 'failing', status_reason = 'tenant_unreachable'`)
	want(contracts.MailboxUnavailable, "tenant_unreachable")
}

// Check of a shared mailbox by its UPN: its three reads with the
// application's token, the row written with the object id and the mail
// address in lower case.
func TestChannelMailbox_CheckASharedMailbox(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	from := len(g.Calls())
	info := checked(t, channelProvider(h), "Post@nordlys.no")
	oid := strings.ToLower(post.ID)
	if info != (contracts.ChannelMailboxInfo{Mailbox: oid, Address: "post@nordlys.no", DisplayName: "Post", Shared: true}) {
		t.Errorf("info = %+v", info)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.channel_mailboxes WHERE object_id = $1 AND address = 'post@nordlys.no' AND checked_at = $2`, oid, h.Now()); n != 1 {
		t.Errorf("%d rows for the mailbox, want one", n)
	}
	var paths []string
	for _, c := range graphCalls(g, from) {
		paths = append(paths, c.Method+" "+c.Path)
	}
	want := []string{
		"GET /v1.0/users/Post@nordlys.no",
		"GET /v1.0/users/" + oid + "/mailboxSettings/userPurpose",
		"GET /v1.0/users/" + oid + "/mailFolders/inbox",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("the calls = %v, want %v", paths, want)
	}

	// Checked again: the row rewritten, not doubled.
	h.Advance(time.Minute)
	checked(t, channelProvider(h), "post@nordlys.no")
	if n := h.Count(t, `SELECT count(*) FROM entra.channel_mailboxes WHERE checked_at = $1`, h.Now()); n != 1 {
		t.Errorf("%d rows after a second Check, want the one rewritten", n)
	}
}

// An address that is the mailbox's mail but not its UPN is found by the
// mail eq fallback; one beginning with $ through the key form.
func TestChannelMailbox_CheckByMailAndADollarAddress(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g, func(u *graphtest.User) { u.UserPrincipalName = "post@nordlys.onmicrosoft.com" })
	from := len(g.Calls())
	info := checked(t, channelProvider(h), "post@nordlys.no")
	if info.Mailbox != strings.ToLower(post.ID) || info.Address != "post@nordlys.no" {
		t.Errorf("by its mail: %+v", info)
	}
	calls := graphCalls(g, from)
	if len(calls) != 4 || calls[0].Status != http.StatusNotFound || calls[1].Path != "/v1.0/users" || !strings.Contains(calls[1].Query, "mail+eq") && !strings.Contains(calls[1].Query, "mail%20eq") {
		t.Errorf("the calls = %+v", calls)
	}

	dollar := sharedMailbox(g, func(u *graphtest.User) { u.UserPrincipalName, u.Mail = "$faktura@nordlys.no", "$faktura@nordlys.no" })
	if info := checked(t, channelProvider(h), "$faktura@nordlys.no"); info.Mailbox != strings.ToLower(dollar.ID) {
		t.Errorf("a $ address: %+v", info)
	}
}

// A personal mailbox answers Shared false; Communications refuses it. It is
// not recorded, so Send refuses it as never checked, with no call.
func TestChannelMailbox_CheckAPersonalMailbox(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	sharedMailbox(g, func(u *graphtest.User) {
		u.Shared = false
		u.UserPrincipalName, u.Mail = "kari@nordlys.no", "kari@nordlys.no"
	})
	p := channelProvider(h)
	info := checked(t, p, "kari@nordlys.no")
	if info.Shared {
		t.Errorf("a personal mailbox: %+v, want Shared false", info)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.channel_mailboxes`); n != 0 {
		t.Errorf("%d rows for a personal mailbox, want none", n)
	}
	from := len(g.Calls())
	if err := p.Send(context.Background(), info.Mailbox, channelMIME(""), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send from a personal mailbox: %v, want ErrMailboxUnknown", err)
	}
	if calls := graphCalls(g, from); len(calls) != 0 || len(g.Sent()) != 0 {
		t.Errorf("Send from a personal mailbox reached Graph: %+v", calls)
	}
}

// Check's refusals: outside the scope ErrMailboxDenied, no such address
// ErrMailboxUnknown, no mailbox ErrMailboxGone, the switch off
// ErrMailboxUnavailable with no call, a throttle passing as a failure; no
// row written for any.
func TestChannelMailbox_CheckRefusals(t *testing.T) {
	t.Parallel()
	h, g, o := newChannelHarness(t)
	sharedMailbox(g, func(u *graphtest.User) { u.MailboxDenied = true })
	sharedMailbox(g, func(u *graphtest.User) {
		u.UserPrincipalName, u.Mail, u.NoMailbox = "gone@nordlys.no", "gone@nordlys.no", true
	})
	p := channelProvider(h)
	ctx := context.Background()
	for address, want := range map[string]error{
		"post@nordlys.no":        contracts.ErrMailboxDenied,
		"nobody@nordlys.no":      contracts.ErrMailboxUnknown,
		"gone@nordlys.no":        contracts.ErrMailboxGone,
		"not an address":         contracts.ErrMailboxUnknown,
		"a/../b@nordlys.no":      contracts.ErrMailboxUnknown,
		"a/b@nordlys.no":         contracts.ErrMailboxUnknown,
		"":                       contracts.ErrMailboxUnknown,
		"o'brien@nordlys.no":     contracts.ErrMailboxUnknown,
		"post\x00@nordlys.no":    contracts.ErrMailboxUnknown,
		"Post <post@nordlys.no>": contracts.ErrMailboxUnknown,
	} {
		if _, err := p.Check(ctx, address); !errors.Is(err, want) {
			t.Errorf("Check(%q) = %v, want %v", address, err, want)
		}
	}

	g.Throttle(2)
	if _, err := p.Check(ctx, "post@nordlys.no"); err == nil || errors.Is(err, contracts.ErrMailboxDenied) || errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("throttled: %v, want a failure to retry", err)
	}

	setMailChannels(t, o, false)
	from := len(g.Calls())
	if _, err := p.Check(ctx, "post@nordlys.no"); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("the switch off: %v, want ErrMailboxUnavailable", err)
	}
	if calls := graphCalls(g, from); len(calls) != 0 {
		t.Errorf("the switch off reached Graph: %+v", calls)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.channel_mailboxes`); n != 0 {
		t.Errorf("%d rows written by refusals", n)
	}
}

// Send without Bcc: the MIME posted once to sendMail, byte for byte, with
// the application's token.
func TestChannelMailbox_Send(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	from := len(g.Calls())
	if err := p.Send(context.Background(), info.Mailbox, channelMIME(""), nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sent := g.Sent()
	if len(sent) != 1 || sent[0].UserID != post.ID || !bytes.Equal(sent[0].MIME, channelMIME("")) {
		t.Errorf("sent = %+v", sent)
	}
	if calls := graphCalls(g, from); len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Path != "/v1.0/users/"+info.Mailbox+"/sendMail" {
		t.Errorf("the calls = %+v", calls)
	}
}

// Send's refusals before any call: a mailbox Check never resolved, a
// message over MaxMessage, the switch off.
func TestChannelMailbox_SendRefusedBeforeAnyCall(t *testing.T) {
	t.Parallel()
	h, g, o := newChannelHarness(t)
	sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	from := len(g.Calls())
	if err := p.Send(ctx, uuid.NewString(), channelMIME(""), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("a mailbox never checked: %v", err)
	}
	if err := p.Send(ctx, "not-an-id", channelMIME(""), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("not an id: %v", err)
	}
	if err := p.Send(ctx, info.Mailbox, bytes.Repeat([]byte("a"), 3_000_001), nil); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("over MaxMessage: %v", err)
	}
	setMailChannels(t, o, false)
	if err := p.Send(ctx, info.Mailbox, channelMIME(""), nil); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("the switch off: %v", err)
	}
	if calls := graphCalls(g, from); len(calls) != 0 || len(g.Sent()) != 0 {
		t.Errorf("a refusal reached Graph: %+v", calls)
	}
}

// Send's refusals by Graph: 413, or 400 ErrorMessageSizeExceeded,
// ErrMailTooLarge; 403 ErrMailboxDenied; a
// mailbox gone ErrMailboxGone, a 5xx a failure made once — never retried
// by the client —, a throttle a failure.
func TestChannelMailbox_SendRefusedByGraph(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	send := func() ([]graphtest.Call, error) {
		from := len(g.Calls())
		err := p.Send(ctx, info.Mailbox, channelMIME(""), nil)
		return graphCalls(g, from), err
	}

	g.FailNext(http.StatusRequestEntityTooLarge)
	if _, err := send(); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("413: %v", err)
	}
	// Exchange's own send limit, below Graph's: a 400 with its size code.
	g.SetMailSizeLimit(len(channelMIME("")) - 1)
	if _, err := send(); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("400 ErrorMessageSizeExceeded: %v, want ErrMailTooLarge", err)
	}
	g.SetMailSizeLimit(0)
	// One 5xx queued: a retry would have sent the message.
	g.FailNext(http.StatusInternalServerError)
	calls, err := send()
	if err == nil || errors.Is(err, contracts.ErrMailboxDenied) || len(calls) != 1 {
		t.Errorf("a 5xx: %v after %d calls, want a failure after one", err, len(calls))
	}
	g.Throttle(2)
	if _, err := send(); err == nil || errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("throttled: %v", err)
	}
	g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied = true })
	if _, err := send(); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("403: %v", err)
	}
	g.EditUser(post.ID, func(u *graphtest.User) { u.MailboxDenied, u.NoMailbox = false, true })
	if _, err := send(); !errors.Is(err, contracts.ErrMailboxGone) {
		t.Errorf("no mailbox: %v", err)
	}
	if len(g.Sent()) != 0 {
		t.Errorf("sent %+v", g.Sent())
	}
}

// Send with Bcc: a MIME draft carrying the Bcc header, its bccRecipients
// checked, then sent; no draft left. A draft whose Bcc was lost is deleted
// and nothing sent; so is one whose send fails.
func TestChannelMailbox_SendWithBccThroughADraft(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	mime := channelMIME("skjult@example.com, Arkiv@Example.com")
	bcc := []string{"skjult@example.com", "arkiv@example.com"}

	from := len(g.Calls())
	if err := p.Send(ctx, info.Mailbox, mime, bcc); err != nil {
		t.Fatalf("Send with Bcc: %v", err)
	}
	sent := g.Sent()
	if len(sent) != 1 || sent[0].UserID != post.ID || !bytes.Equal(sent[0].MIME, mime) || len(sent[0].Bcc) != 2 || len(g.Drafts()) != 0 {
		t.Errorf("sent %+v, drafts %+v", sent, g.Drafts())
	}
	var paths []string
	for _, c := range graphCalls(g, from) {
		paths = append(paths, c.Method+" "+strings.TrimPrefix(c.Path, "/v1.0/users/"+info.Mailbox))
	}
	if len(paths) != 2 || paths[0] != "POST /messages" || !strings.HasPrefix(paths[1], "POST /messages/") || !strings.HasSuffix(paths[1], "/send") {
		t.Errorf("the calls = %v", paths)
	}

	g.DropDraftBcc(true)
	if err := p.Send(ctx, info.Mailbox, mime, bcc); !errors.Is(err, contracts.ErrBccUnsupported) {
		t.Errorf("a draft without its Bcc: %v, want ErrBccUnsupported", err)
	}
	g.DropDraftBcc(false)
	if len(g.Sent()) != 1 || len(g.Drafts()) != 0 {
		t.Errorf("a draft without its Bcc: sent %d, drafts %+v", len(g.Sent()), g.Drafts())
	}

	g.FailNextDraftSend(http.StatusForbidden)
	if err := p.Send(ctx, info.Mailbox, mime, bcc); !errors.Is(err, contracts.ErrMailboxDenied) {
		t.Errorf("the draft's send refused: %v, want ErrMailboxDenied", err)
	}
	if len(g.Sent()) != 1 || len(g.Drafts()) != 0 {
		t.Errorf("the draft's send refused: sent %d, drafts %+v", len(g.Sent()), g.Drafts())
	}
}

// Forget drops the mailbox: Send then refuses it. Twice is done.
func TestChannelMailbox_Forget(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	ctx := context.Background()
	for range 2 {
		if err := p.Forget(ctx, info.Mailbox); err != nil {
			t.Errorf("Forget: %v", err)
		}
	}
	if err := p.Forget(ctx, "not-an-id"); err != nil {
		t.Errorf("Forget of no id: %v", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.channel_mailboxes`); n != 0 {
		t.Errorf("%d rows after Forget", n)
	}
	if err := p.Send(ctx, info.Mailbox, channelMIME(""), nil); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Send after Forget: %v", err)
	}
}

// A draft's create answers the message whole, its body with it: a message
// near MaxMessage answers more than the client's 1 MiB and is still read,
// the draft sent with its Bcc.
func TestChannelMailbox_SendALargeMessageWithBcc(t *testing.T) {
	t.Parallel()
	h, g, _ := newChannelHarness(t)
	sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	mime := append(channelMIME("skjult@example.com"), bytes.Repeat([]byte("Lorem ipsum dolor sit amet.\r\n"), 2_500_000/29)...)
	if len(mime) < 2<<20 || int64(len(mime)) > p.MaxMessage() {
		t.Fatalf("the MIME is %d bytes, want over 2 MiB and within MaxMessage", len(mime))
	}
	if err := p.Send(context.Background(), info.Mailbox, mime, []string{"skjult@example.com"}); err != nil {
		t.Fatalf("Send of a large message with Bcc: %v", err)
	}
	if sent := g.Sent(); len(sent) != 1 || !bytes.Equal(sent[0].MIME, mime) || len(g.Drafts()) != 0 {
		t.Errorf("sent %d, drafts %d", len(sent), len(g.Drafts()))
	}
}
