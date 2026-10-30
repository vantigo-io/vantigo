package modtest

import (
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakePersonalMailbox is a contracts.PersonalMailbox a test shapes, for
// Communications' personal sending and its outbox to be tested without
// composing Entra beside them (Outlook connection design M20). It holds per-user
// status entries set by SetUser; a user not set answers Reason "no_account" on
// Status and ConnectionNeededError{Reason: "no_account"} on Send.
//
// Send records the message in Sent when the user's status is Available, and
// answers ConnectionNeededError when Available is false, ErrMailTooLarge when
// mime exceeds MaxMessage, or the error configured via FailWith or
// SetSendError.
//
// It is safe for concurrent use: the outbox calls it from its own goroutine
// while a test reads it.
type FakePersonalMailbox struct {
	mu         sync.Mutex
	maxMessage int64
	users      map[uuid.UUID]contracts.PersonalMailStatus
	userErr    map[uuid.UUID]error
	fail       map[string]error
	sent       []FakePersonalSend
	calls      []PersonalMailboxCall
}

// FakePersonalSend is one message a FakePersonalMailbox sent on a person's
// behalf: the user ID in context, their mailbox and address, and copies of the
// MIME and Bcc recipients.
type FakePersonalSend struct {
	UserID  uuid.UUID
	Mailbox string
	Address string
	MIME    []byte
	Bcc     []string
}

// PersonalMailboxCall is one call a FakePersonalMailbox recorded.
type PersonalMailboxCall struct {
	Method string
	UserID uuid.UUID
}

var _ contracts.PersonalMailbox = (*FakePersonalMailbox)(nil)

// NewFakePersonalMailbox returns an empty FakePersonalMailbox with the default
// 3 000 000-byte message limit.
func NewFakePersonalMailbox() *FakePersonalMailbox {
	return &FakePersonalMailbox{
		maxMessage: FakeChannelMailboxMaxMessage,
		users:      map[uuid.UUID]contracts.PersonalMailStatus{},
		userErr:    map[uuid.UUID]error{},
		fail:       map[string]error{},
	}
}

// SetUser sets the PersonalMailStatus Status answers for userID, and controls
// whether Send accepts messages from userID.
func (f *FakePersonalMailbox) SetUser(userID uuid.UUID, status contracts.PersonalMailStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[userID] = status
}

// SetSendError makes Send answer err for userID, or (err nil) clears it.
func (f *FakePersonalMailbox) SetSendError(userID uuid.UUID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.userErr, userID)
	} else {
		f.userErr[userID] = err
	}
}

// SetMaxMessage sets the MIME byte limit above which Send answers
// ErrMailTooLarge.
func (f *FakePersonalMailbox) SetMaxMessage(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxMessage = n
}

// FailWith makes every later call of method ("Status", "Send") answer err, or
// (err nil) clears it.
func (f *FakePersonalMailbox) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
	} else {
		f.fail[method] = err
	}
}

// Sent answers every personal message sent so far, in order.
func (f *FakePersonalMailbox) Sent() []FakePersonalSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// Calls answers every call recorded so far, in order.
func (f *FakePersonalMailbox) Calls() []PersonalMailboxCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Status answers the PersonalMailStatus SetUser configured for the caller in
// contracts.UserID(ctx), or Reason "no_account" when unset.
func (f *FakePersonalMailbox) Status(ctx context.Context) (contracts.PersonalMailStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid, ok := contracts.UserID(ctx)
	f.calls = append(f.calls, PersonalMailboxCall{Method: "Status", UserID: uid})
	if err := f.fail["Status"]; err != nil {
		return contracts.PersonalMailStatus{}, err
	}
	if !ok {
		return contracts.PersonalMailStatus{Reason: "no_account"}, nil
	}
	st, exists := f.users[uid]
	if !exists {
		return contracts.PersonalMailStatus{Reason: "no_account"}, nil
	}
	return st, nil
}

// Send records the message for the caller in contracts.UserID(ctx), or refuses
// it as the contract says.
func (f *FakePersonalMailbox) Send(ctx context.Context, mime []byte, bcc []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid, ok := contracts.UserID(ctx)
	f.calls = append(f.calls, PersonalMailboxCall{Method: "Send", UserID: uid})
	if err := f.fail["Send"]; err != nil {
		return err
	}
	if !ok {
		return &contracts.ConnectionNeededError{Reason: contracts.ConnectionNotConnected}
	}
	if err := f.userErr[uid]; err != nil {
		return err
	}
	st, exists := f.users[uid]
	if !exists {
		return &contracts.ConnectionNeededError{Reason: "no_account"}
	}
	if !st.Available {
		reason := st.Reason
		if reason == "reconnect" && st.ErrorCode != "" {
			reason = st.ErrorCode
		}
		if reason == "" {
			reason = contracts.ConnectionNotConnected
		}
		return &contracts.ConnectionNeededError{Reason: reason}
	}
	if int64(len(mime)) > f.maxMessage {
		return contracts.ErrMailTooLarge
	}
	f.sent = append(f.sent, FakePersonalSend{
		UserID:  uid,
		Mailbox: st.Mailbox,
		Address: st.Address,
		MIME:    slices.Clone(mime),
		Bcc:     slices.Clone(bcc),
	})
	return nil
}
