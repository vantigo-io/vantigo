package modtest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// The fake outbound mail answers the channels set, in the order set; Send
// records the message for a channel that can send and refuses an unknown one
// (ErrChannelUnknown) and one that cannot (ErrChannelUnavailable), sending
// nothing; FailWith refuses a method; every call is recorded.
func TestFakeOutboundMail(t *testing.T) {
	ctx := context.Background()
	f := NewFakeOutboundMail()
	ready := contracts.OutboundChannel{ID: uuid.New(), Address: "faktura@example.com", Provider: "smtp", ProviderDisplayName: "SMTP", Available: true}
	broken := contracts.OutboundChannel{ID: uuid.New(), Address: "post@example.com", Provider: "smtp", ProviderDisplayName: "SMTP"}
	f.SetChannels(ready, broken)

	if got, err := f.Channels(ctx); err != nil || len(got) != 2 || got[0] != ready || got[1] != broken {
		t.Errorf("Channels = %+v, %v; want the two set, in order", got, err)
	}
	if got, err := f.Channel(ctx, broken.ID); err != nil || got != broken {
		t.Errorf("Channel of a channel set = %+v, %v; want it", got, err)
	}
	if _, err := f.Channel(ctx, uuid.New()); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Channel of an id not set = %v, want ErrChannelUnknown", err)
	}

	to := []string{"kunde@example.com"}
	if err := f.Send(ctx, ready.ID, mail.Outbound{To: to, Subject: "Faktura", MessageID: "1@example.com"}); err != nil {
		t.Fatalf("Send from a channel that can send: %v", err)
	}
	to[0] = "changed@example.com" // the fake keeps its own copy
	sent := f.Sent()
	if len(sent) != 1 || sent[0].Channel != ready.ID || sent[0].Out.To[0] != "kunde@example.com" || sent[0].Out.MessageID != "1@example.com" {
		t.Errorf("Sent = %+v", sent)
	}
	if err := f.Send(ctx, broken.ID, mail.Outbound{To: to}); !errors.Is(err, contracts.ErrChannelUnavailable) {
		t.Errorf("Send from a channel that cannot send = %v, want ErrChannelUnavailable", err)
	}
	if err := f.Send(ctx, uuid.New(), mail.Outbound{To: to}); !errors.Is(err, contracts.ErrChannelUnknown) {
		t.Errorf("Send from an unknown channel = %v, want ErrChannelUnknown", err)
	}
	if len(f.Sent()) != 1 {
		t.Errorf("a refused Send recorded a message: %+v", f.Sent())
	}

	boom := errors.New("boom")
	f.FailWith("Send", boom)
	if err := f.Send(ctx, ready.ID, mail.Outbound{To: to}); !errors.Is(err, boom) {
		t.Errorf("Send after FailWith = %v, want the configured error", err)
	}
	f.FailWith("Send", nil)
	if err := f.Send(ctx, ready.ID, mail.Outbound{To: to}); err != nil {
		t.Errorf("Send after FailWith cleared: %v", err)
	}
	if calls := f.Calls(); len(calls) != 8 || calls[0].Method != "Channels" || calls[2].Channel == (uuid.UUID{}) {
		t.Errorf("Calls = %+v, want eight, the first Channels", calls)
	}
}
