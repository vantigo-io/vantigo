package modtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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
