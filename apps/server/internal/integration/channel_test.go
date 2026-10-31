package integration_test

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the invoices installations' stand-in for Communications'
// channels (integrations design T4, T6): customers and invoices are composed
// without communications, so the OutboundMail slot is a modtest fake offering
// one channel that can send, and the seller's Invoice settings choose it.
// Everything a send hands the channel is recorded by the older smtpRecorder.

// sendChannelAddress is the address of the one channel the installation offers:
// the From a document goes out on.
const sendChannelAddress = "faktura@example.invalid"

// sendChannelID is the id of that channel.
var sendChannelID = uuid.MustParse("b3e6d1a4-52c9-4f7e-8a30-6d2c9e1f4b58")

// channelMail is a FakeOutboundMail whose Send also hands the message to a
// recorder's send, with the mail configuration it would have gone out on - the
// channel's address as From.
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
