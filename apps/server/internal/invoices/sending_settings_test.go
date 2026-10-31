package invoices_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The sending channel (integrations design T6): Invoice settings name the
// Communications channel documents and reminder letters go out on, or none;
// nothing is sent on the installation's own SMTP credentials. These tests drive
// the real module against modtest.FakeOutboundMail, with a channel that can
// send and one that cannot.

// sendingSentence is the one sentence Invoices says when sending is disabled
// or the chosen channel cannot send.
const sendingSentence = "Sending is disabled in Invoice settings, or the chosen channel cannot send."

// sendingChannels is the two channels the tests offer: one that can send and
// one whose credentials are missing.
func sendingChannels() (ready, broken contracts.OutboundChannel) {
	ready = contracts.OutboundChannel{
		ID: uuid.New(), Address: "faktura@selger.example", DisplayName: "Faktura", Provider: "smtp", ProviderDisplayName: "SMTP", Available: true,
	}
	broken = contracts.OutboundChannel{
		ID: uuid.New(), Address: "post@selger.example", Provider: "entra", ProviderDisplayName: "Microsoft 365",
	}
	return ready, broken
}

// sendingHarness is an installation with the OutboundMail slot filled by fake,
// a complete seller and Acme with an invoice e-mail; sending is disabled until
// a test chooses a channel.
func sendingHarness(t *testing.T) (*harness, *modtest.FakeOutboundMail, contracts.OutboundChannel, contracts.OutboundChannel) {
	t.Helper()
	fake := modtest.NewFakeOutboundMail()
	ready, broken := sendingChannels()
	fake.SetChannels(ready, broken)
	h := newHarness(t, modtest.WithOutboundMail(fake))
	saveSeller(t, h, completeSeller(1))
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@acme.example" })
	return h, fake, ready, broken
}

// choose stores channel as the sending channel, as the settings page does;
// nil is Disabled.
func choose(t *testing.T, h *harness, channel *uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE invoices.settings SET send_channel = $1`, channel)
}

// The settings answer the stored channel, what it is now and the channels a
// choice may name; a replace stores a channel that exists, keeps the stored one
// when it leaves the field out, clears it with null, and refuses an id no
// active channel has or a malformed one with a 400 on sendChannel. A stored
// channel that has since gone is shown as gone, kept by a replace that leaves
// it out or names it again, and sends nothing.
func TestSettings_TheSendingChannel(t *testing.T) {
	t.Parallel()
	h, fake, ready, broken := sendingHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	read := func() settingsJSON {
		t.Helper()
		res := manager.Do(http.MethodGet, settingsPath, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET /settings = %d %s", res.Status, res.Body)
		}
		var s settingsJSON
		res.JSON(&s)
		return s
	}
	put := func(revision int32, sendChannel any, omit bool) *modtest.Response {
		t.Helper()
		body := completeSeller(revision)
		if !omit {
			body["sendChannel"] = sendChannel
		}
		return manager.Do(http.MethodPut, settingsPath, body)
	}

	s := read()
	if s.SendChannel != nil || s.SendChannelState != "disabled" || len(s.SendChannels) != 2 {
		t.Fatalf("a fresh installation: sendChannel %v state %q channels %d; want none, disabled, two", s.SendChannel, s.SendChannelState, len(s.SendChannels))
	}
	if got := s.SendChannels[0]; got.ID != ready.ID.String() || got.Address != ready.Address || got.Provider != "smtp" ||
		got.ProviderDisplayName != "SMTP" || !got.Available || got.DisplayName != "Faktura" {
		t.Errorf("the first choice = %+v, want the ready channel with its provider", got)
	}
	if got := s.SendChannels[1]; got.ID != broken.ID.String() || got.Available || got.ProviderDisplayName != "Microsoft 365" {
		t.Errorf("the second choice = %+v, want the channel that cannot send, Microsoft 365", got)
	}

	// Chosen, kept, cleared.
	res := put(s.Revision, ready.ID.String(), false)
	if res.Status != http.StatusOK {
		t.Fatalf("choose the ready channel = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	if s.SendChannel == nil || *s.SendChannel != ready.ID.String() || s.SendChannelState != "ready" {
		t.Errorf("after choosing: %v %q, want the ready channel, ready", s.SendChannel, s.SendChannelState)
	}
	res = put(s.Revision, nil, true)
	res.JSON(&s)
	if res.Status != http.StatusOK || s.SendChannel == nil || *s.SendChannel != ready.ID.String() {
		t.Errorf("a replace without sendChannel = %d, channel %v; want 200 and the stored one kept", res.Status, s.SendChannel)
	}
	res = put(s.Revision, broken.ID.String(), false)
	res.JSON(&s)
	if res.Status != http.StatusOK || s.SendChannelState != "unavailable" {
		t.Errorf("choosing the channel that cannot send = %d %q, want 200 and unavailable", res.Status, s.SendChannelState)
	}
	res = put(s.Revision, nil, false)
	res.JSON(&s)
	if res.Status != http.StatusOK || s.SendChannel != nil || s.SendChannelState != "disabled" {
		t.Errorf("a null sendChannel = %d %v %q, want 200, none, disabled", res.Status, s.SendChannel, s.SendChannelState)
	}

	// Refused.
	for _, bad := range []any{uuid.NewString(), "not-a-uuid", 7} {
		res := put(s.Revision, bad, false)
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["sendChannel"]) == 0 {
			t.Errorf("sendChannel %v = %d %s, want 400 on sendChannel", bad, res.Status, res.Body)
		}
	}
	if read().SendChannel != nil {
		t.Errorf("a refused replace changed the channel")
	}

	// A channel that goes away.
	res = put(s.Revision, ready.ID.String(), false)
	res.JSON(&s)
	fake.SetChannels(broken)
	if gone := read(); gone.SendChannel == nil || *gone.SendChannel != ready.ID.String() || gone.SendChannelState != "gone" {
		t.Errorf("after the channel went: %v %q, want the id kept and gone", gone.SendChannel, gone.SendChannelState)
	}
	if meta := getMeta(t, h, "invoices:access", "invoices:issue"); meta.MailAvailable || meta.Capabilities.CanSend {
		t.Errorf("with the chosen channel gone: mailAvailable %v, canSend %v; want false, false", meta.MailAvailable, meta.Capabilities.CanSend)
	}
	res = put(s.Revision, nil, true)
	res.JSON(&s)
	if res.Status != http.StatusOK || s.SendChannel == nil || *s.SendChannel != ready.ID.String() {
		t.Errorf("a replace that leaves a gone channel out = %d %v, want 200 and the id kept", res.Status, s.SendChannel)
	}
	res = put(s.Revision, ready.ID.String(), false)
	res.JSON(&s)
	if res.Status != http.StatusOK {
		t.Errorf("a replace that names the gone stored channel again = %d %s, want 200", res.Status, res.Body)
	}
}

// Settings need no channel list to be saved or read when Communications is
// off: the slot is empty, every choice but none is refused, and the settings
// read as Disabled.
func TestSettings_TheSendingChannelWithoutCommunications(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := h.SignIn(t, "invoices:access", "invoices:manage")
	body := completeSeller(1)
	body["sendChannel"] = uuid.NewString()
	res := manager.Do(http.MethodPut, settingsPath, body)
	if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["sendChannel"]) == 0 {
		t.Errorf("a channel with no Communications = %d %s, want 400 on sendChannel", res.Status, res.Body)
	}
	var s settingsJSON
	manager.Do(http.MethodGet, settingsPath, nil).JSON(&s)
	if s.SendChannel != nil || s.SendChannelState != "disabled" || len(s.SendChannels) != 0 {
		t.Errorf("settings = %v %q %d channels, want none, disabled, none", s.SendChannel, s.SendChannelState, len(s.SendChannels))
	}
}

// A document goes out on the chosen channel - from its address, the seller's
// Reply-To and a Message-ID of the module's own kept - and is refused with the
// one sentence when no channel is chosen, the chosen one cannot send or has
// gone: a 503 before anything is read, nothing sent, nothing logged. A channel
// whose send fails is a 502 and no delivery row.
func TestSend_GoesThroughTheChosenChannel(t *testing.T) {
	t.Parallel()
	h, fake, ready, broken := sendingHarness(t)
	inv := issuedAcme(t, h)

	refused := func(what string) {
		t.Helper()
		res := sendAs(sender(t, h), inv.ID, nil)
		sendRefused(t, what, res, http.StatusServiceUnavailable, "mail_unavailable")
		if d := problemOf(t, res).Detail; d != sendingSentence {
			t.Errorf("%s: detail %q, want %q", what, d, sendingSentence)
		}
		if n := len(fake.Sent()); n != 0 {
			t.Errorf("%s: %d mails sent, want none", what, n)
		}
		if n := deliveryRows(t, h, inv.ID); n != 0 {
			t.Errorf("%s: %d delivery rows, want none", what, n)
		}
	}
	refused("send with no channel chosen")
	choose(t, h, &broken.ID)
	refused("send on a channel that cannot send")
	gone := uuid.New()
	choose(t, h, &gone)
	refused("send on a channel that is gone")

	choose(t, h, &ready.ID)
	sent(t, h, inv.ID, nil)
	mails := fake.Sent()
	if len(mails) != 1 || mails[0].Channel != ready.ID {
		t.Fatalf("mails = %+v, want one, on the ready channel", mails)
	}
	out := mails[0].Out
	if out.ReplyTo != "faktura@kraft-verket.no" || out.DisplayName != "Kraft-Verket AS" || !slices.Equal(out.To, []string{"faktura@acme.example"}) || out.MessageID == "" {
		t.Errorf("envelope = reply-to %q name %q to %v message-id %q; want the seller's, Acme's invoice e-mail and a Message-ID of our own",
			out.ReplyTo, out.DisplayName, out.To, out.MessageID)
	}
	if n := deliveryRows(t, h, inv.ID); n != 1 {
		t.Errorf("delivery rows = %d, want 1", n)
	}

	fake.FailWith("Send", errors.New("the mail server said no"))
	sendRefused(t, "send when the channel's send fails", sendAs(sender(t, h), inv.ID, nil), http.StatusBadGateway, "mail_failed")
	if n := deliveryRows(t, h, inv.ID); n != 1 {
		t.Errorf("delivery rows after a failed send = %d, want still 1", n)
	}
	fake.FailWith("Send", contracts.ErrChannelUnavailable)
	sendRefused(t, "send when the channel became unavailable", sendAs(sender(t, h), inv.ID, nil), http.StatusBadGateway, "mail_failed")
}

// A reminder letter goes out on the chosen channel; with sending disabled -
// nothing chosen, or the chosen channel gone or unable to send - the attempt
// fails with the one sentence as its last_error and nothing is mailed, and the
// letter is tried again once an Owner chooses a channel.
func TestReminderWorker_SendsOnTheChosenChannelOrNotAtAll(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	_, letter := dueLetter(t, h, 1)
	choose(t, h, nil)
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	r := letterOf(t, h, letter)
	if r.Status != "queued" || r.Attempts != 1 || deref(r.LastError) != sendingSentence || len(mails.tried()) != 0 {
		t.Fatalf("with sending disabled: the letter = %s attempts %d error %q after %d sends; want a failed attempt with the sentence, nothing mailed",
			r.Status, r.Attempts, deref(r.LastError), len(mails.tried()))
	}
	gone := uuid.New()
	choose(t, h, &gone)
	h.Advance(2 * time.Hour)
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	if r := letterOf(t, h, letter); deref(r.LastError) != sendingSentence || len(mails.tried()) != 0 {
		t.Errorf("with the channel gone: error %q after %d sends; want the sentence, nothing mailed", deref(r.LastError), len(mails.tried()))
	}
	chooseChannel(t, h)
	h.Advance(2 * time.Hour)
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
		t.Errorf("with a channel chosen: the letter = %s after %d mails, want sent and one mail", r.Status, len(mails.delivered()))
	}
	if got := mails.delivered()[0]; got.ReplyTo != sellerMailbox {
		t.Errorf("the letter's Reply-To = %q, want the seller's %q", got.ReplyTo, sellerMailbox)
	}
}
