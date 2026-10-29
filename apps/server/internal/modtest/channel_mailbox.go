package modtest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeChannelMailbox is a contracts.ChannelMailbox a test shapes, for
// Communications' channels and its outbox to be tested without composing
// Entra beside them (Outlook connection design M11). It holds the mailboxes
// SetMailbox gave, by address in lower case; each shared one is one Send may
// write to from the start, as if Check had resolved it, until Forget drops
// it — a personal one never, as Entra records only a shared mailbox.
//
// Check answers the mailbox set for an address, any case, and
// ErrMailboxUnknown for one never set; Send records the message whole in
// Sent and answers ErrMailboxUnknown for a mailbox it does not hold and
// ErrMailTooLarge for MIME larger than MaxMessage; Forget drops a mailbox and
// records it in Forgotten, a mailbox it does not hold included. Check and
// Send answer ErrMailboxUnavailable while SetStatus says anything but
// available. Every call but Key, DisplayName and MaxMessage — which make no
// network call — is recorded in Calls.
//
// It is safe for concurrent use: the outbox calls it from its own goroutine
// while a test reads it.
type FakeChannelMailbox struct {
	mu         sync.Mutex
	key        string
	status     contracts.MailboxStatus
	maxMessage int64
	byAddress  map[string]contracts.ChannelMailboxInfo // SetMailbox's, by lower-cased address
	known      map[string]bool                         // the mailboxes Send may write to
	fail       map[string]error                        // by method: FailWith
	sent       []FakeSend
	forgotten  []string
	calls      []ChannelMailboxCall
}

// FakeSend is one message a FakeChannelMailbox sent: the mailbox, a copy of
// the MIME and of the Bcc recipients.
type FakeSend struct {
	Mailbox string
	MIME    []byte
	Bcc     []string
}

// ChannelMailboxCall is one call a FakeChannelMailbox recorded: the method,
// the mailbox Forget or Send named, the address Check was given.
type ChannelMailboxCall struct {
	Method, Mailbox, Address string
}

var _ contracts.ChannelMailbox = (*FakeChannelMailbox)(nil)

// FakeChannelMailboxMaxMessage is what MaxMessage answers until
// SetMaxMessage sets another: Entra's, 3 000 000 bytes of MIME.
const FakeChannelMailboxMaxMessage = 3_000_000

// NewFakeChannelMailbox is an available provider under the key "entra",
// named "Microsoft 365", holding no mailbox.
func NewFakeChannelMailbox() *FakeChannelMailbox {
	return &FakeChannelMailbox{
		key:        "entra",
		status:     contracts.MailboxStatus{State: contracts.MailboxAvailable},
		maxMessage: FakeChannelMailboxMaxMessage,
		byAddress:  map[string]contracts.ChannelMailboxInfo{},
		known:      map[string]bool{},
		fail:       map[string]error{},
	}
}

// WithKey makes Key answer key. It answers the fake, for a constructor's
// chain.
func (f *FakeChannelMailbox) WithKey(key string) *FakeChannelMailbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
	return f
}

// SetMailbox makes Check answer info for address, in any case, and Send
// take info.Mailbox when it is shared.
func (f *FakeChannelMailbox) SetMailbox(address string, info contracts.ChannelMailboxInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byAddress[strings.ToLower(address)] = info
	if info.Shared {
		f.known[info.Mailbox] = true
	}
}

// SetStatus sets what Status answers: contracts.MailboxAvailable — with
// reason contracts.MailboxTenantWide for the warning — or
// MailboxUnavailable with a reason code.
func (f *FakeChannelMailbox) SetStatus(state, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = contracts.MailboxStatus{State: state, Reason: reason}
}

// SetMaxMessage sets what MaxMessage answers, and the size above which Send
// answers ErrMailTooLarge.
func (f *FakeChannelMailbox) SetMaxMessage(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxMessage = n
}

// FailWith makes every later call of method ("Status", "Check", "Forget",
// "Send") answer err, or (err nil) clears it. A call refused so changes
// nothing: a Send sends nothing, a Forget forgets nothing.
func (f *FakeChannelMailbox) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
	} else {
		f.fail[method] = err
	}
}

// Sent answers every message sent so far, in order.
func (f *FakeChannelMailbox) Sent() []FakeSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// Forgotten answers every mailbox Forget was asked for, in order.
func (f *FakeChannelMailbox) Forgotten() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.forgotten)
}

// Calls answers every call recorded so far, in order.
func (f *FakeChannelMailbox) Calls() []ChannelMailboxCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Key is the provider's key, "entra" unless WithKey set another.
func (f *FakeChannelMailbox) Key() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

// DisplayName is "Microsoft 365".
func (f *FakeChannelMailbox) DisplayName() string { return "Microsoft 365" }

// MaxMessage answers SetMaxMessage's size, 3 000 000 until then.
func (f *FakeChannelMailbox) MaxMessage() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxMessage
}

// Status answers what SetStatus set, or FailWith's error.
func (f *FakeChannelMailbox) Status(context.Context) (contracts.MailboxStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Status"})
	if err := f.fail["Status"]; err != nil {
		return contracts.MailboxStatus{}, err
	}
	return f.status, nil
}

// Check answers the mailbox SetMailbox set for address, and makes a shared
// one Send takes again after a Forget.
func (f *FakeChannelMailbox) Check(_ context.Context, address string) (contracts.ChannelMailboxInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Check", Address: address})
	if err := f.fail["Check"]; err != nil {
		return contracts.ChannelMailboxInfo{}, err
	}
	if f.status.State != contracts.MailboxAvailable {
		return contracts.ChannelMailboxInfo{}, contracts.ErrMailboxUnavailable
	}
	info, ok := f.byAddress[strings.ToLower(address)]
	if !ok {
		return contracts.ChannelMailboxInfo{}, contracts.ErrMailboxUnknown
	}
	if info.Shared {
		f.known[info.Mailbox] = true
	}
	return info, nil
}

// Forget drops mailbox, so Send refuses it until a Check.
func (f *FakeChannelMailbox) Forget(_ context.Context, mailbox string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Forget", Mailbox: mailbox})
	if err := f.fail["Forget"]; err != nil {
		return err
	}
	delete(f.known, mailbox)
	f.forgotten = append(f.forgotten, mailbox)
	return nil
}

// Send records the message, or refuses it as the contract says.
func (f *FakeChannelMailbox) Send(_ context.Context, mailbox string, mime []byte, bcc []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Send", Mailbox: mailbox})
	if err := f.fail["Send"]; err != nil {
		return err
	}
	if !f.known[mailbox] {
		return contracts.ErrMailboxUnknown
	}
	if int64(len(mime)) > f.maxMessage {
		return contracts.ErrMailTooLarge
	}
	if f.status.State != contracts.MailboxAvailable {
		return contracts.ErrMailboxUnavailable
	}
	f.sent = append(f.sent, FakeSend{Mailbox: mailbox, MIME: slices.Clone(mime), Bcc: slices.Clone(bcc)})
	return nil
}
