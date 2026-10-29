package modtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

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
// Each mailbox has an Inbox AddInbox fills and RemoveInbox empties. Delta
// reads it as Entra's would: a fresh read (link "") answers the messages in
// it received at or after since, then each link answers the changes since —
// added or changed messages whatever their received time, removed ones as
// Removed —, SetDeltaPage's number at a time (MailDeltaPageMax until then),
// in the order they were made, a message changed twice once. Its links are
// opaque strings it issues; ExpireDelta makes every link of a mailbox issued
// so far answer ErrMailboxDeltaExpired, and a link it never issued, or issued
// for another mailbox, is refused. Raw answers a message's bytes, refusing
// one larger than max with ErrMailTooLarge and one not in the Inbox with
// ErrMailItemGone. Both refuse a mailbox Send would not take, and answer
// ErrMailboxUnavailable while the provider is not available.
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

	inbox     map[string]*fakeInbox // by mailbox
	links     map[string]fakeMailDeltaLink
	deltaPage int
	issued    int // links issued, for their names
}

// fakeInbox is one mailbox's Inbox: its messages now, and the log of every
// change, for the delta.
type fakeInbox struct {
	messages   map[string]fakeInboxMessage // by id
	log        []contracts.MailItem        // each change, in order; Removed for a removal
	generation int                         // ExpireDelta's
}

type fakeInboxMessage struct {
	item contracts.MailItem
	raw  []byte
}

// fakeMailDeltaLink is what a link the fake issued stands for: the items still
// to answer and the log's length once they are, or (pending nil, delta true)
// the changes after a point of the log.
type fakeMailDeltaLink struct {
	mailbox    string
	generation int
	pending    []contracts.MailItem
	at         int // the log's length the link reads on from
	delta      bool
}

// FakeSend is one message a FakeChannelMailbox sent: the mailbox, a copy of
// the MIME and of the Bcc recipients.
type FakeSend struct {
	Mailbox string
	MIME    []byte
	Bcc     []string
}

// ChannelMailboxCall is one call a FakeChannelMailbox recorded: the method,
// the mailbox Forget, Send, Delta or Raw named, the address Check was given;
// Delta's folder, link and since; Raw's message id and max.
type ChannelMailboxCall struct {
	Method, Mailbox, Address string
	Folder, Link             string
	Since                    time.Time
	MessageID                string
	Max                      int64
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
		inbox:      map[string]*fakeInbox{},
		links:      map[string]fakeMailDeltaLink{},
		deltaPage:  contracts.MailDeltaPageMax,
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
// "Send", "Delta", "Raw") answer err, or (err nil) clears it. A call refused so changes
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

// AddInbox puts a message in mailbox's Inbox — item.ID its id, raw its bytes
// —, or changes the one with that id, so the next Delta answers it.
// item.Removed is ignored and ReceivedAt kept in UTC.
func (f *FakeChannelMailbox) AddInbox(mailbox string, item contracts.MailItem, raw []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item.Removed = false
	item.ReceivedAt = item.ReceivedAt.UTC()
	in := f.inboxOf(mailbox)
	in.messages[item.ID] = fakeInboxMessage{item: item, raw: slices.Clone(raw)}
	in.log = append(in.log, item)
}

// RemoveInbox takes message id out of mailbox's Inbox: the next Delta
// answers it Removed, and Raw ErrMailItemGone.
func (f *FakeChannelMailbox) RemoveInbox(mailbox, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := f.inboxOf(mailbox)
	if _, ok := in.messages[id]; !ok {
		return
	}
	delete(in.messages, id)
	in.log = append(in.log, contracts.MailItem{ID: id, Removed: true})
}

// ExpireDelta makes every link Delta issued for mailbox so far answer
// contracts.ErrMailboxDeltaExpired; a fresh read starts anew.
func (f *FakeChannelMailbox) ExpireDelta(mailbox string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inboxOf(mailbox).generation++
}

// SetDeltaPage sets the most items one Delta page answers,
// contracts.MailDeltaPageMax until then.
func (f *FakeChannelMailbox) SetDeltaPage(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deltaPage = n
}

// inboxOf answers mailbox's Inbox, made empty on first use; f.mu held.
func (f *FakeChannelMailbox) inboxOf(mailbox string) *fakeInbox {
	in, ok := f.inbox[mailbox]
	if !ok {
		in = &fakeInbox{messages: map[string]fakeInboxMessage{}}
		f.inbox[mailbox] = in
	}
	return in
}

// readable answers why mailbox may not be read, or nil; f.mu held.
func (f *FakeChannelMailbox) readable(method, mailbox string) error {
	if err := f.fail[method]; err != nil {
		return err
	}
	if !f.known[mailbox] {
		return contracts.ErrMailboxUnknown
	}
	if f.status.State != contracts.MailboxAvailable {
		return contracts.ErrMailboxUnavailable
	}
	return nil
}

// Delta answers one page of the Inbox's changes, as the type's comment says.
func (f *FakeChannelMailbox) Delta(_ context.Context, mailbox, folder, link string, since time.Time) (contracts.MailDelta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Delta", Mailbox: mailbox, Folder: folder, Link: link, Since: since})
	if err := f.readable("Delta", mailbox); err != nil {
		return contracts.MailDelta{}, err
	}
	if folder != "inbox" {
		return contracts.MailDelta{}, fmt.Errorf("modtest: the fake channel mailbox reads only the inbox, not %q", folder)
	}
	in := f.inboxOf(mailbox)
	var state fakeMailDeltaLink
	if link == "" {
		state = fakeMailDeltaLink{mailbox: mailbox, generation: in.generation, at: len(in.log)}
		for _, change := range latestChanges(in.log) {
			if !change.Removed && !change.ReceivedAt.Before(since) {
				state.pending = append(state.pending, change)
			}
		}
	} else {
		known, ok := f.links[link]
		if !ok || known.mailbox != mailbox {
			return contracts.MailDelta{}, errors.New("modtest: not a delta link of this mailbox")
		}
		if known.generation != in.generation {
			return contracts.MailDelta{}, contracts.ErrMailboxDeltaExpired
		}
		state = known
		if state.delta {
			state = fakeMailDeltaLink{mailbox: mailbox, generation: in.generation, at: len(in.log),
				pending: latestChanges(in.log[known.at:])}
		}
	}

	n := min(f.deltaPage, len(state.pending))
	page := contracts.MailDelta{Items: slices.Clone(state.pending[:n])}
	next := fakeMailDeltaLink{mailbox: mailbox, generation: state.generation, at: state.at}
	if rest := state.pending[n:]; len(rest) > 0 {
		next.pending = slices.Clone(rest)
	} else {
		next.delta = true
		page.Done = true
	}
	f.issued++
	page.Link = fmt.Sprintf("fake-delta-%d", f.issued)
	f.links[page.Link] = next
	return page, nil
}

// latestChanges answers each id's last change in changes, in the order of
// those last changes.
func latestChanges(changes []contracts.MailItem) []contracts.MailItem {
	last := map[string]int{}
	for i, c := range changes {
		last[c.ID] = i
	}
	var out []contracts.MailItem
	for i, c := range changes {
		if last[c.ID] == i {
			out = append(out, c)
		}
	}
	return out
}

// Raw answers a copy of the message's bytes, as the type's comment says.
func (f *FakeChannelMailbox) Raw(_ context.Context, mailbox, messageID string, max int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ChannelMailboxCall{Method: "Raw", Mailbox: mailbox, MessageID: messageID, Max: max})
	if err := f.readable("Raw", mailbox); err != nil {
		return nil, err
	}
	m, ok := f.inboxOf(mailbox).messages[messageID]
	if !ok {
		return nil, contracts.ErrMailItemGone
	}
	if int64(len(m.raw)) > max {
		return nil, contracts.ErrMailTooLarge
	}
	return slices.Clone(m.raw), nil
}
