package communications_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeMessenger is a contracts.ChannelMessenger a test shapes, for Communications'
// messenger channels, its sync and its outbox to be tested without the Meta
// module beside them (Messenger design N2, delivery A). It holds the Pages
// AddPage connected, and an inbox per Page: Push queues an event under the next
// cursor — an insertion id, as the provider's own bigserial — and Pending reads
// the events after a cursor, oldest cursor first, at most limit, while Ack
// forgets those up to one. Notified answers the Pages with an event not yet
// acknowledged that Push stamped after since by the fake's clock. OpenAttachment
// answers the bytes AddFile gave for a ref, Profile the name SetProfile gave
// for a participant, and Send records each part it was handed in Sent and
// answers a platform id of its own, unless FailSend made that call fail.
//
// Every call but Key, DisplayName and Capabilities is recorded in Calls. It is
// safe for concurrent use: the workers call it from their own goroutines while a
// test reads it.
type fakeMessenger struct {
	mu     sync.Mutex
	key    string
	status contracts.MessengerStatus
	caps   contracts.MessengerCapabilities
	now    func() time.Time

	pages  map[string]contracts.MessengerEndpoint
	inbox  map[string][]fakeInboxEvent
	cursor int64
	acked  map[string]int64
	files  map[string]fakeMessengerFile
	names  map[string]string

	sent      []fakeSentPart
	sendCalls int
	sendFail  map[int]error // by the 1-based count of the Send call
	sendHook  func(ctx context.Context, address string, part contracts.MessengerPart) error
	fail      map[string]error
	calls     []string

	profileCalls int
	pendingHook  func(ctx context.Context, address string)
}

type fakeInboxEvent struct {
	event   contracts.MessengerEvent
	arrived time.Time
}

type fakeMessengerFile struct {
	file    contracts.MessengerFile
	content []byte
}

// fakeSentPart is one part Send was handed.
type fakeSentPart struct {
	Address string
	Part    contracts.MessengerPart
	Sent    contracts.MessengerSent
}

var _ contracts.ChannelMessenger = (*fakeMessenger)(nil)

// fakeMessengerKey is the key a fakeMessenger answers until WithKey.
const fakeMessengerKey = "fakechat"

func newFakeMessenger() *fakeMessenger {
	return &fakeMessenger{
		key:    fakeMessengerKey,
		status: contracts.MessengerStatus{Available: true},
		caps: contracts.MessengerCapabilities{
			MaxText: 2000, MaxAnswerOptions: 13, MaxAnswerTitle: 20, ShareEmail: true,
			MaxCards: 10, MaxCardChoices: 3, MaxCardTitle: 80, MaxCardSubtitle: 80, MaxChoiceTitle: 20,
			Window: 24 * time.Hour, MaxAttachmentBytes: 25 << 20,
		},
		now:      time.Now,
		pages:    map[string]contracts.MessengerEndpoint{},
		inbox:    map[string][]fakeInboxEvent{},
		acked:    map[string]int64{},
		files:    map[string]fakeMessengerFile{},
		names:    map[string]string{},
		sendFail: map[int]error{},
		fail:     map[string]error{},
	}
}

// WithKey makes the fake answer key as its provider's.
func (f *fakeMessenger) WithKey(key string) *fakeMessenger {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
	return f
}

// SetClock sets the clock Push stamps events' arrival by, the harness's own.
func (f *fakeMessenger) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// SetStatus sets what Status answers.
func (f *fakeMessenger) SetStatus(available bool, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = contracts.MessengerStatus{Available: available, Reason: reason}
}

// SetCapabilities sets what Capabilities answers.
func (f *fakeMessenger) SetCapabilities(c contracts.MessengerCapabilities) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caps = c
}

// AddPage connects a Page.
func (f *fakeMessenger) AddPage(address, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[address] = contracts.MessengerEndpoint{Address: address, DisplayName: name, Available: true}
}

// SetPageUnavailable marks a connected Page unavailable with a reason.
func (f *fakeMessenger) SetPageUnavailable(address, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.pages[address]
	e.Available, e.Reason = false, reason
	f.pages[address] = e
}

// SetProfile sets the name Profile answers for a participant.
func (f *fakeMessenger) SetProfile(participant, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.names[participant] = name
}

// AddFile makes OpenAttachment answer content for ref.
func (f *fakeMessenger) AddFile(ref, fileName, contentType string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[ref] = fakeMessengerFile{
		file:    contracts.MessengerFile{FileName: fileName, ContentType: contentType, Size: int64(len(content))},
		content: content,
	}
}

// FailWith makes every call of method fail with err until FailWith(method, nil).
func (f *fakeMessenger) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
		return
	}
	f.fail[method] = err
}

// FailSend makes the nth Send call, counted from 1 over the fake's life, fail
// with err; the part is not recorded.
func (f *fakeMessenger) FailSend(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendFail[n] = err
}

// OnSend installs a hook run at the start of each Send, after the call is
// counted and before it answers: a test blocks or fails the call from it.
func (f *fakeMessenger) OnSend(hook func(ctx context.Context, address string, part contracts.MessengerPart) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendHook = hook
}

// OnPending installs a hook run at the start of each Pending call.
func (f *fakeMessenger) OnPending(hook func(ctx context.Context, address string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingHook = hook
}

// Push queues event under the next cursor in the inbox of address, which it
// answers; the event's Cursor is set.
func (f *fakeMessenger) Push(address string, event contracts.MessengerEvent) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursor++
	event.Cursor = f.cursor
	f.inbox[address] = append(f.inbox[address], fakeInboxEvent{event: event, arrived: f.now()})
	return f.cursor
}

// Text pushes a message of the participant's.
func (f *fakeMessenger) Text(address, participant, platformID, text string, at time.Time) int64 {
	return f.Push(address, contracts.MessengerEvent{
		Kind: contracts.MessengerEventMessage, Participant: participant, At: at,
		PlatformMessageID: platformID, Text: text,
	})
}

// Sent is every part Send recorded, in order.
func (f *fakeMessenger) Sent() []fakeSentPart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// Calls is the name of every call recorded, in order.
func (f *fakeMessenger) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// CallCount is how many times method was called.
func (f *fakeMessenger) CallCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// Acked is the cursor the provider was last acknowledged up to for address.
func (f *fakeMessenger) Acked(address string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acked[address]
}

// record notes one call and answers its injected failure. mu is held.
func (f *fakeMessenger) record(method string) error {
	f.calls = append(f.calls, method)
	return f.fail[method]
}

func (f *fakeMessenger) Key() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

func (f *fakeMessenger) DisplayName() string { return "Fake chat" }

func (f *fakeMessenger) Status(context.Context) (contracts.MessengerStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Status"); err != nil {
		return contracts.MessengerStatus{}, err
	}
	return f.status, nil
}

func (f *fakeMessenger) Capabilities() contracts.MessengerCapabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.caps
}

func (f *fakeMessenger) Endpoints(context.Context) ([]contracts.MessengerEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Endpoints"); err != nil {
		return nil, err
	}
	var out []contracts.MessengerEndpoint
	for _, e := range f.pages {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b contracts.MessengerEndpoint) int {
		switch {
		case a.Address < b.Address:
			return -1
		case a.Address > b.Address:
			return 1
		}
		return 0
	})
	return out, nil
}

func (f *fakeMessenger) Check(_ context.Context, address string) (contracts.MessengerEndpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Check"); err != nil {
		return contracts.MessengerEndpoint{}, err
	}
	if !f.status.Available {
		return contracts.MessengerEndpoint{}, contracts.ErrMessengerUnavailable
	}
	e, ok := f.pages[address]
	if !ok {
		return contracts.MessengerEndpoint{}, contracts.ErrMessengerEndpointUnknown
	}
	return e, nil
}

func (f *fakeMessenger) Send(ctx context.Context, address string, part contracts.MessengerPart) (contracts.MessengerSent, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "Send")
	f.sendCalls++
	n := f.sendCalls
	hook, injected, failed := f.sendHook, f.sendFail[n], f.fail["Send"]
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, address, part); err != nil {
			return contracts.MessengerSent{}, err
		}
	}
	if injected != nil {
		return contracts.MessengerSent{}, injected
	}
	if failed != nil {
		return contracts.MessengerSent{}, failed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sent := contracts.MessengerSent{PlatformMessageID: fmt.Sprintf("m_fake_%d", n), At: f.now().UTC()}
	f.sent = append(f.sent, fakeSentPart{Address: address, Part: part, Sent: sent})
	return sent, nil
}

func (f *fakeMessenger) Notified(_ context.Context, since time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Notified"); err != nil {
		return nil, err
	}
	var out []string
	for address, events := range f.inbox {
		for _, e := range events {
			if e.event.Cursor > f.acked[address] && e.arrived.After(since) {
				out = append(out, address)
				break
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeMessenger) Pending(ctx context.Context, address string, after int64, limit int) ([]contracts.MessengerEvent, error) {
	f.mu.Lock()
	hook := f.pendingHook
	f.mu.Unlock()
	if hook != nil {
		hook(ctx, address)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Pending"); err != nil {
		return nil, err
	}
	var out []contracts.MessengerEvent
	for _, e := range f.inbox[address] {
		if e.event.Cursor > max(after, f.acked[address]) {
			out = append(out, e.event)
		}
	}
	slices.SortFunc(out, func(a, b contracts.MessengerEvent) int {
		switch {
		case a.Cursor < b.Cursor:
			return -1
		case a.Cursor > b.Cursor:
			return 1
		}
		return 0
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessenger) OpenAttachment(_ context.Context, _, ref string) (io.ReadCloser, contracts.MessengerFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("OpenAttachment"); err != nil {
		return nil, contracts.MessengerFile{}, err
	}
	file, ok := f.files[ref]
	if !ok {
		return nil, contracts.MessengerFile{}, errors.New("fake messenger: no such attachment")
	}
	return io.NopCloser(bytes.NewReader(file.content)), file.file, nil
}

func (f *fakeMessenger) Ack(_ context.Context, address string, upTo int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("Ack"); err != nil {
		return err
	}
	f.acked[address] = max(f.acked[address], upTo)
	return nil
}

func (f *fakeMessenger) Profile(_ context.Context, _, participant string) (contracts.MessengerProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profileCalls++
	if err := f.record("Profile"); err != nil {
		return contracts.MessengerProfile{}, err
	}
	return contracts.MessengerProfile{DisplayName: f.names[participant]}, nil
}

// messengerHarness is a communications installation with fake as its messenger
// provider, its clock the harness's own.
func messengerHarness(t *testing.T, opts ...modtest.Option) (*modtest.Harness, *fakeMessenger) {
	t.Helper()
	fake := newFakeMessenger()
	h := newHarness(t, append([]modtest.Option{modtest.WithChannelMessenger(fake)}, opts...)...)
	fake.SetClock(h.Now)
	return h, fake
}
