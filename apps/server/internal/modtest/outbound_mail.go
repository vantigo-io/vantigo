package modtest

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// FakeOutboundMail is a contracts.OutboundMail a test shapes, for Invoices'
// and Calendar's sending to be tested without composing Communications beside
// them (integrations design T4): depguard keeps it out of their tests. It
// holds the channels SetChannels was given, answers Channels and Channel from
// them, and records every Send in Sent.
//
// Send refuses as the contract says — ErrChannelUnknown for an id it holds
// none of, ErrChannelUnavailable for a channel set not Available — and
// otherwise records the message and answers nil, or the error FailWith
// configured. It is safe for concurrent use: a worker calls it from its own
// goroutine while a test reads it.
type FakeOutboundMail struct {
	mu       sync.Mutex
	channels []contracts.OutboundChannel
	fail     map[string]error
	sent     []FakeOutboundSend
	calls    []OutboundMailCall
}

// FakeOutboundSend is one message a FakeOutboundMail took: the channel it was
// sent from and a copy of the Outbound the consumer built.
type FakeOutboundSend struct {
	Channel uuid.UUID
	Out     mail.Outbound
}

// OutboundMailCall is one call a FakeOutboundMail recorded: the method and
// the channel it named (the zero id for Channels).
type OutboundMailCall struct {
	Method  string
	Channel uuid.UUID
}

var _ contracts.OutboundMail = (*FakeOutboundMail)(nil)

// NewFakeOutboundMail is a provider with no channels.
func NewFakeOutboundMail() *FakeOutboundMail {
	return &FakeOutboundMail{fail: map[string]error{}}
}

// SetChannels replaces the channels the provider knows, in the order given.
func (f *FakeOutboundMail) SetChannels(channels ...contracts.OutboundChannel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = slices.Clone(channels)
}

// FailWith makes every later call of method ("Channels", "Channel", "Send")
// answer err, or (err nil) clears it.
func (f *FakeOutboundMail) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
	} else {
		f.fail[method] = err
	}
}

// Sent answers every message sent so far, in order.
func (f *FakeOutboundMail) Sent() []FakeOutboundSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// Calls answers every call recorded so far, in order.
func (f *FakeOutboundMail) Calls() []OutboundMailCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Channels answers the channels SetChannels gave.
func (f *FakeOutboundMail) Channels(context.Context) ([]contracts.OutboundChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, OutboundMailCall{Method: "Channels"})
	if err := f.fail["Channels"]; err != nil {
		return nil, err
	}
	return slices.Clone(f.channels), nil
}

// Channel answers one channel, or ErrChannelUnknown.
func (f *FakeOutboundMail) Channel(_ context.Context, id uuid.UUID) (contracts.OutboundChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, OutboundMailCall{Method: "Channel", Channel: id})
	if err := f.fail["Channel"]; err != nil {
		return contracts.OutboundChannel{}, err
	}
	return f.find(id)
}

// Send records the message, or refuses it as the contract says.
func (f *FakeOutboundMail) Send(_ context.Context, id uuid.UUID, out mail.Outbound) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, OutboundMailCall{Method: "Send", Channel: id})
	if err := f.fail["Send"]; err != nil {
		return err
	}
	c, err := f.find(id)
	if err != nil {
		return err
	}
	if !c.Available {
		return contracts.ErrChannelUnavailable
	}
	out.To = slices.Clone(out.To)
	out.Cc = slices.Clone(out.Cc)
	out.Bcc = slices.Clone(out.Bcc)
	f.sent = append(f.sent, FakeOutboundSend{Channel: id, Out: out})
	return nil
}

// find is the channel with the id, ErrChannelUnknown otherwise. The caller
// holds the lock.
func (f *FakeOutboundMail) find(id uuid.UUID) (contracts.OutboundChannel, error) {
	for _, c := range f.channels {
		if c.ID == id {
			return c, nil
		}
	}
	return contracts.OutboundChannel{}, contracts.ErrChannelUnknown
}
