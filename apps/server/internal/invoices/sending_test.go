package invoices_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the tests' side of sending (integrations design T6): documents
// and reminder letters go out through the OutboundMail slot, from the channel
// Invoice settings chose. The older fakes (fakeSMTP, letterMail) keep their
// shape - they record the envelope and fail or hold on demand - and are handed
// to withChannel, which stands them behind one available channel; chooseChannel
// then makes it the settings' choice.

// sendChannelAddress is the address of the one channel withChannel offers: the
// From a message goes out on.
const sendChannelAddress = "faktura@example.invalid"

// sendChannelID is the id of that channel.
var sendChannelID = uuid.MustParse("5c1f0e72-3b8a-4f0e-9d1a-2f6c8a4b7e10")

// channelMail is a FakeOutboundMail whose Send also hands the message to a
// recording fake's send, with the mail configuration it would have gone out on
// - the channel's address as From.
type channelMail struct {
	*modtest.FakeOutboundMail
	send func(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error
}

func (m *channelMail) Send(ctx context.Context, id uuid.UUID, out mail.Outbound) error {
	ch, err := m.Channel(ctx, id)
	if err != nil {
		return err
	}
	if !ch.Available {
		return contracts.ErrChannelUnavailable
	}
	return m.send(ctx, config.MailConfig{Driver: "smtp", From: ch.Address}, out)
}

// withChannel wires the OutboundMail slot with one available channel
// (sendChannelID) whose sends go to send.
func withChannel(send func(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error) modtest.Option {
	f := modtest.NewFakeOutboundMail()
	f.SetChannels(contracts.OutboundChannel{
		ID: sendChannelID, Address: sendChannelAddress, Provider: "smtp", ProviderDisplayName: "SMTP", Available: true,
	})
	return modtest.WithOutboundMail(&channelMail{FakeOutboundMail: f, send: send})
}

// chooseChannel makes sendChannelID the channel Invoice settings send from.
func chooseChannel(t *testing.T, h *harness) {
	t.Helper()
	h.Exec(t, `UPDATE invoices.settings SET send_channel = $1`, sendChannelID)
}
