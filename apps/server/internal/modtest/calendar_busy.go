package modtest

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeCalendarBusy is a contracts.CalendarBusy a test shapes, for a module
// that reads the calendar's busy time — Planning's board — to be tested
// without composing the calendar beside it (calendar Outlook sync design
// O9.8). Busy answers, of the ids asked, the ones SetBusy gave a busy time,
// as set, whatever the window; SetDeny makes it answer nothing, as the
// provider does for a caller without the calendar's permission, and
// SetError makes it fail. Every call is recorded in Calls. It is safe for
// concurrent use.
type FakeCalendarBusy struct {
	mu    sync.Mutex
	busy  map[uuid.UUID]contracts.BusyTime
	deny  bool
	err   error
	calls []BusyCall
}

// BusyCall is one call a FakeCalendarBusy recorded: a copy of the ids and
// the window asked.
type BusyCall struct {
	UserIDs  []uuid.UUID
	From, To time.Time
}

var _ contracts.CalendarBusy = (*FakeCalendarBusy)(nil)

// NewFakeCalendarBusy is a provider that knows no one's busy time.
func NewFakeCalendarBusy() *FakeCalendarBusy {
	return &FakeCalendarBusy{busy: map[uuid.UUID]contracts.BusyTime{}}
}

// SetBusy sets what Busy answers for userID.
func (f *FakeCalendarBusy) SetBusy(userID uuid.UUID, busy contracts.BusyTime) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy[userID] = busy
}

// SetDeny makes Busy answer an empty map — the caller may see no one — or
// (deny false) what SetBusy set.
func (f *FakeCalendarBusy) SetDeny(deny bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deny = deny
}

// SetError makes Busy answer err, or (err nil) clears it.
func (f *FakeCalendarBusy) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Calls answers every call recorded so far, in order.
func (f *FakeCalendarBusy) Calls() []BusyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Busy answers the busy time SetBusy set for the ids asked, nothing under
// SetDeny, or SetError's error.
func (f *FakeCalendarBusy) Busy(_ context.Context, userIDs []uuid.UUID, from, to time.Time) (map[uuid.UUID]contracts.BusyTime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, BusyCall{UserIDs: slices.Clone(userIDs), From: from, To: to})
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]contracts.BusyTime{}
	if f.deny {
		return out, nil
	}
	for _, id := range userIDs {
		if busy, ok := f.busy[id]; ok {
			out[id] = busy
		}
	}
	return out, nil
}
