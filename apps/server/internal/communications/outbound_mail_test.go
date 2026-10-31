package communications_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is Communications' side of contracts.OutboundMail (integrations
// design T4, T5): the channels as Invoices and Calendar send through them, and
// the end of the fall-back to the installation's SMTP. The provider is the
// module's own (communications.Module().OutboundMail) built over the harness's
// Deps, the way Compose builds it.

// outboundMailOf is the module's OutboundMail over h's Deps.
func outboundMailOf(h *modtest.Harness) contracts.OutboundMail {
	return communications.Module().OutboundMail(h.Deps())
}

// outboundMessage is a message a consumer would send: a recipient, a subject, a
// name beside the channel's address, a Reply-To and a Message-ID of its own.
func outboundMessage() mail.Outbound {
	return mail.Outbound{
		DisplayName: "Seller AS", To: []string{"kunde@example.test"}, Subject: "Faktura 1001",
		ReplyTo: "faktura@seller.example.test", TextBody: "Vedlagt.", MessageID: uuid.NewString() + "@invoices.example.test",
	}
}

// TestOutboundMail_ChannelsListsTheActiveMailChannelsWithWhetherTheyCanSend: an
// SMTP channel with its credentials is available; one whose credential row is
// gone is listed Unavailable; a deactivated channel is not listed; a Microsoft
// 365 channel is available while its provider is offered and not when it is
// not. Channels and Channel answer the same.
func TestOutboundMail_ChannelsListsTheActiveMailChannelsWithWhetherTheyCanSend(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake))
	admin := h.SignIn(t, "communications:channels-manage")

	ready := createChannel(t, admin, newChannelBody(channelAddress(t)))
	broken := createChannel(t, admin, newChannelBody(channelAddress(t)))
	inactive := createChannel(t, admin, newChannelBody(channelAddress(t)))
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, broken.Id)
	h.Exec(t, `UPDATE communications.channels SET is_active = false WHERE id = $1`, inactive.Id)
	post := channelAddress(t)
	sharedMailbox(fake, post, "")
	m365 := createChannel(t, admin, entraChannelBody(post))

	o := outboundMailOf(h)
	got, err := o.Channels(context.Background())
	if err != nil {
		t.Fatalf("Channels: %v", err)
	}
	byID := map[uuid.UUID]contracts.OutboundChannel{}
	for _, c := range got {
		byID[c.ID] = c
	}
	if len(got) != 3 {
		t.Fatalf("Channels = %+v, want the three active ones", got)
	}
	if c := byID[uuid.MustParse(ready.Id)]; !c.Available || c.Provider != "smtp" || c.ProviderDisplayName != "SMTP" || c.Address != ready.Address {
		t.Errorf("the credentialed SMTP channel = %+v, want available SMTP", c)
	}
	if c := byID[uuid.MustParse(broken.Id)]; c.Available || c.Provider != "smtp" {
		t.Errorf("the SMTP channel without credentials = %+v, want Unavailable", c)
	}
	if _, listed := byID[uuid.MustParse(inactive.Id)]; listed {
		t.Errorf("a deactivated channel is listed: %+v", byID[uuid.MustParse(inactive.Id)])
	}
	if c := byID[uuid.MustParse(m365.Id)]; !c.Available || c.Provider != "entra" || c.ProviderDisplayName != "Microsoft 365" {
		t.Errorf("the Microsoft 365 channel = %+v, want available Microsoft 365", c)
	}

	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if c, err := o.Channel(context.Background(), uuid.MustParse(m365.Id)); err != nil || c.Available {
		t.Errorf("Channel of the Microsoft 365 channel with its provider off = %+v, %v; want Unavailable", c, err)
	}
	if _, err := o.Channel(context.Background(), uuid.MustParse(inactive.Id)); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Channel of a deactivated channel = %v, want ErrChannelUnknown", err)
	}
	if _, err := o.Channel(context.Background(), uuid.New()); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Channel of an id that is none = %v, want ErrChannelUnknown", err)
	}
}

// TestOutboundMail_SendFromAnSMTPChannelUsesTheChannelsOwnCredentials: the
// message goes through the SMTP seam with the channel's host, From the
// channel's address, and the consumer's Reply-To, name and Message-ID as they
// were — never the host's SMTP.
func TestOutboundMail_SendFromAnSMTPChannelUsesTheChannelsOwnCredentials(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	h := newHarness(t,
		modtest.WithSMTPSend(f.send),
		modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "host-smtp.example.test"),
		modtest.WithEnv("SMTP_FROM", "host-noreply@example.test"),
	)
	admin := h.SignIn(t, "communications:channels-manage")
	ch := createChannel(t, admin, newChannelBody(channelAddress(t)))
	out := outboundMessage()

	if err := outboundMailOf(h).Send(context.Background(), uuid.MustParse(ch.Id), out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	cfgs, sent := f.configs(), f.sends()
	if len(cfgs) != 1 || len(sent) != 1 {
		t.Fatalf("sends = %d, want one", len(cfgs))
	}
	if cfgs[0].Host != "smtp.example.test" || cfgs[0].From != ch.Address {
		t.Errorf("config = %s From %s, want the channel's host and address", cfgs[0].Host, cfgs[0].From)
	}
	if sent[0].ReplyTo != out.ReplyTo || sent[0].MessageID != out.MessageID || sent[0].DisplayName != out.DisplayName {
		t.Errorf("the message = %+v, want the caller's Reply-To, Message-ID and name kept", sent[0])
	}
}

// TestOutboundMail_SendFromAMicrosoft365ChannelGoesThroughTheMailbox: the
// message is rendered as MIME from the channel's address and handed to the
// provider's Send for the channel's mailbox, carrying the caller's Message-ID
// and Reply-To; nothing goes over SMTP.
func TestOutboundMail_SendFromAMicrosoft365ChannelGoesThroughTheMailbox(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithSMTPSend(f.send), modtest.WithChannelMailbox(fake))
	admin := h.SignIn(t, "communications:channels-manage")
	address := channelAddress(t)
	info := sharedMailbox(fake, address, "")
	ch := createChannel(t, admin, entraChannelBody(address))
	out := outboundMessage()

	if err := outboundMailOf(h).Send(context.Background(), uuid.MustParse(ch.Id), out); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Mailbox != info.Mailbox {
		t.Fatalf("mailbox sends = %+v, want one to %s", sent, info.Mailbox)
	}
	raw := string(sent[0].MIME)
	for _, want := range []string{"From: \"Seller AS\" <" + address + ">", "Reply-To: <" + out.ReplyTo + ">", "Message-ID: <" + out.MessageID + ">", "To: <kunde@example.test>"} {
		if !strings.Contains(raw, want) {
			t.Errorf("the MIME lacks %q:\n%s", want, raw)
		}
	}
	if n := len(f.configs()); n != 0 {
		t.Errorf("SMTP sends = %d, want none", n)
	}
}

// TestOutboundMail_SendRefusesAChannelThatCannotSend: an SMTP channel without
// credentials, a Microsoft 365 channel whose provider is off, a Microsoft 365
// channel whose mailbox the provider denies are ErrChannelUnavailable with
// nothing sent over SMTP; an id that is none, and a deactivated channel, are
// ErrChannelUnknown; a message too large for the provider is ErrMailTooLarge.
func TestOutboundMail_SendRefusesAChannelThatCannotSend(t *testing.T) {
	t.Parallel()
	f := &fakeSMTP{}
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithSMTPSend(f.send), modtest.WithChannelMailbox(fake))
	admin := h.SignIn(t, "communications:channels-manage")
	ctx := context.Background()
	o := outboundMailOf(h)

	broken := createChannel(t, admin, newChannelBody(channelAddress(t)))
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, broken.Id)
	err := o.Send(ctx, uuid.MustParse(broken.Id), outboundMessage())
	if !errors.Is(err, contracts.ErrChannelUnavailable) || !strings.Contains(err.Error(), "the channel has no credentials") {
		t.Errorf("Send from an SMTP channel without credentials = %v, want ErrChannelUnavailable with the fixed sentence", err)
	}
	if n := len(f.configs()); n != 0 {
		t.Errorf("SMTP sends = %d, want none: no fall-back to the host's SMTP", n)
	}

	address := channelAddress(t)
	sharedMailbox(fake, address, "")
	m365 := createChannel(t, admin, entraChannelBody(address))
	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if err := o.Send(ctx, uuid.MustParse(m365.Id), outboundMessage()); !errors.Is(err, contracts.ErrChannelUnavailable) {
		t.Errorf("Send with the provider off = %v, want ErrChannelUnavailable", err)
	}
	fake.SetStatus(contracts.MailboxAvailable, "")
	fake.FailWith("Send", contracts.ErrMailboxDenied)
	if err := o.Send(ctx, uuid.MustParse(m365.Id), outboundMessage()); !errors.Is(err, contracts.ErrChannelUnavailable) {
		t.Errorf("Send to a mailbox the provider denies = %v, want ErrChannelUnavailable", err)
	}
	fake.FailWith("Send", contracts.ErrMailTooLarge)
	if err := o.Send(ctx, uuid.MustParse(m365.Id), outboundMessage()); !errors.Is(err, contracts.ErrMailTooLarge) {
		t.Errorf("Send of a message too large = %v, want ErrMailTooLarge", err)
	}
	fake.FailWith("Send", nil)

	if err := o.Send(ctx, uuid.New(), outboundMessage()); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Send from an id that is none = %v, want ErrChannelUnknown", err)
	}
	inactive := createChannel(t, admin, newChannelBody(channelAddress(t)))
	h.Exec(t, `UPDATE communications.channels SET is_active = false WHERE id = $1`, inactive.Id)
	if err := o.Send(ctx, uuid.MustParse(inactive.Id), outboundMessage()); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Send from a deactivated channel = %v, want ErrChannelUnknown", err)
	}
	if n := len(f.configs()); n != 0 {
		t.Errorf("SMTP sends = %d, want none from any refused send", n)
	}
}

// TestChannels_AnSMTPChannelWithoutCredentialsIsUnavailable: GET /channels and
// GET /channels/{id} answer available false for it and true for a credentialed
// one, and a Microsoft 365 channel follows its provider's status.
func TestChannels_AnSMTPChannelWithoutCredentialsIsUnavailable(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake))
	admin := h.SignIn(t, "communications:channels-manage")
	ready := createChannel(t, admin, newChannelBody(channelAddress(t)))
	broken := createChannel(t, admin, newChannelBody(channelAddress(t)))
	h.Exec(t, `DELETE FROM communications.channel_credentials WHERE channel_id = $1`, broken.Id)
	address := channelAddress(t)
	sharedMailbox(fake, address, "")
	m365 := createChannel(t, admin, entraChannelBody(address))

	available := func(id string) bool {
		t.Helper()
		r := admin.Do(http.MethodGet, "/api/v1/communications/channels/"+id, nil)
		var ch struct {
			Available *bool `json:"available"`
		}
		r.JSON(&ch)
		if ch.Available == nil {
			t.Fatalf("GET /channels/%s answered no available: %s", id, r.Body)
		}
		return *ch.Available
	}
	if !available(ready.Id) || available(broken.Id) || !available(m365.Id) {
		t.Errorf("available: credentialed %v, without credentials %v, Microsoft 365 %v; want true, false, true",
			available(ready.Id), available(broken.Id), available(m365.Id))
	}
	var list []struct {
		Id        string `json:"id"`
		Available *bool  `json:"available"`
	}
	admin.Do(http.MethodGet, "/api/v1/communications/channels", nil).JSON(&list)
	if len(list) != 3 {
		t.Fatalf("channels = %d, want 3", len(list))
	}
	for _, c := range list {
		want := c.Id != broken.Id
		if c.Available == nil || *c.Available != want {
			t.Errorf("channel %s available = %v in the list, want %v", c.Id, c.Available, want)
		}
	}
	fake.SetStatus(contracts.MailboxUnavailable, "switch_off")
	if available(m365.Id) {
		t.Errorf("the Microsoft 365 channel is available with its provider off")
	}
}

// TestCommunications_ReadsNoConfigMail is the compile-time absence of the
// fall-back (integrations design T5): nothing in this package's own files reads
// the installation's Config.Mail. The system mailbox is the tenant's
// employees', and a channel's mail goes out on the channel's own credentials or
// not at all.
func TestCommunications_ReadsNoConfigMail(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Mail" {
				return true
			}
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Config" {
				t.Errorf("%s reads Config.Mail: a channel never sends on the installation's SMTP credentials", fset.Position(sel.Pos()))
			}
			return true
		})
	}
}
