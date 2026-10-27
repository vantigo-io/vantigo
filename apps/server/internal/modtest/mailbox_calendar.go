package modtest

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// FakeMailboxCalendar is a contracts.MailboxCalendar a test shapes: the
// provider's mailboxes in memory, for the calendar's Outlook sync and its
// worker to be tested before any provider module is composed beside it
// (calendar Outlook sync design O4). It holds mailboxes by external id, each
// with its secondary calendars (id → name) and its events — the default
// calendar's under calendar id "" — and records every call in Calls.
//
// Its writes act as a mailbox would: EnsureCalendar keeps a known calendar
// of the provider's that still exists, adopts one of the provider's by its
// name, and makes a new one otherwise — a name a foreign calendar holds
// (PutCalendar) skipped for "<name> (2)" up to "(5)"; DeleteCalendar drops a
// calendar of the provider's and its events, refuses a foreign one with
// ErrCalendarNotOurs, and a calendar already gone is done; Apply creates an
// event carrying the op's Key and answers its new id, rewrites or deletes an
// existing one, and answers Gone for an update or a delete of an event that
// no longer exists; Find answers the events carrying a key in any calendar
// — an event with none is never the provider's — starting at or after its
// from when that is set (a timed event's StartsAt, an all-day one's
// StartDate). A mailbox is made on its first use.
//
// Its refusals are the contract's: while the status is
// contracts.MailboxUnavailable every call but Status answers
// ErrMailboxUnavailable; under contracts.MailboxCleanup EnsureCalendar
// answers it and Apply takes deletes only, every other op answering it in
// its result. A mailbox SetUnknown marks answers ErrMailboxUnknown to every
// call naming it, and SetMailboxError makes every call naming a mailbox
// answer the error a test chooses (ErrMailboxDenied, ErrMailboxGone, a
// failure). FailWith makes a method answer an error a test chooses, and
// FailOps the next ops of Apply each answer one.
// It is safe for concurrent use: the calendar's worker calls it from its own
// goroutine while a test reads it.
type FakeMailboxCalendar struct {
	mu        sync.Mutex
	key       string
	status    contracts.MailboxStatus
	mailboxes map[string]*fakeMailbox
	unknown   map[string]bool
	refusals  map[string]error // by mailbox: SetMailboxError
	fail      map[string]error // by method: FailWith
	failOps   int              // FailOps: the ops still to fail
	failOpErr error
	calls     []MailboxCall
	next      int // the last id handed out
}

// MailboxCall is one call a FakeMailboxCalendar recorded: the method, the
// mailbox and the calendar it named, a copy of the ops Apply was given, and
// the from Find was given.
type MailboxCall struct {
	Method, Mailbox, CalendarID string
	Ops                         []contracts.MailboxOp
	From                        time.Time
}

// FakeMailboxEvent is one event of the fake's mailbox, as Events answers it.
type FakeMailboxEvent struct {
	ID, CalendarID, Key string
	Event               contracts.MailboxEvent
	ChangeKey           string
}

// fakeMailbox is one mailbox of the fake: its secondary calendars, the ones
// of them the provider did not make, and its events, by id.
type fakeMailbox struct {
	calendars map[string]string // id → name
	foreign   map[string]bool
	events    map[string]*FakeMailboxEvent
}

var _ contracts.MailboxCalendar = (*FakeMailboxCalendar)(nil)

// errFakeNoSuchCalendar answers an Apply or a Find naming a secondary
// calendar the mailbox does not hold: a failure to retry, as a directory's
// 404 on the calendar would be.
var errFakeNoSuchCalendar = errors.New("modtest: the fake mailbox holds no such calendar")

// NewFakeMailboxCalendar is an available provider under the key "entra",
// its mailboxes empty.
func NewFakeMailboxCalendar() *FakeMailboxCalendar {
	return &FakeMailboxCalendar{
		key:       "entra",
		status:    contracts.MailboxStatus{State: contracts.MailboxAvailable},
		mailboxes: map[string]*fakeMailbox{},
		unknown:   map[string]bool{},
		refusals:  map[string]error{},
		fail:      map[string]error{},
	}
}

// WithKey makes Key answer key. It answers the fake, for a constructor's
// chain.
func (f *FakeMailboxCalendar) WithKey(key string) *FakeMailboxCalendar {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
	return f
}

// SetStatus sets what Status answers: contracts.MailboxAvailable,
// MailboxCleanup or MailboxUnavailable, with a reason code.
func (f *FakeMailboxCalendar) SetStatus(state, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = contracts.MailboxStatus{State: state, Reason: reason}
}

// SetUnknown marks mailbox as one the provider does not manage, or (unknown
// false) clears the mark: every call naming it answers ErrMailboxUnknown.
func (f *FakeMailboxCalendar) SetUnknown(mailbox string, unknown bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if unknown {
		f.unknown[mailbox] = true
	} else {
		delete(f.unknown, mailbox)
	}
}

// SetMailboxError makes every call naming mailbox answer err, or (err nil)
// clears it.
func (f *FakeMailboxCalendar) SetMailboxError(mailbox string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.refusals, mailbox)
	} else {
		f.refusals[mailbox] = err
	}
}

// FailWith makes every later call of method ("Status", "EnsureCalendar",
// "DeleteCalendar", "Apply", "Find") answer err, or (err nil) clears it.
func (f *FakeMailboxCalendar) FailWith(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
	} else {
		f.fail[method] = err
	}
}

// FailOps makes the next n ops Apply is handed — after any refused for the
// status — answer err in their results, unsent: a throttled or failed op
// among others that are done.
func (f *FakeMailboxCalendar) FailOps(n int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOps, f.failOpErr = n, err
}

// PutEvent places an event in a mailbox's calendar as if someone else had
// made it — key "" for one carrying no key — and answers its id: what a
// test's reconcile finds.
func (f *FakeMailboxCalendar) PutEvent(mailbox, calendarID, key string, event contracts.MailboxEvent) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.create(f.mailbox(mailbox), calendarID, key, event)
}

// PutCalendar places a secondary calendar named name in a mailbox and
// answers its id: one the provider made (ours), or — ours false — one the
// person or another installation made, which EnsureCalendar never adopts
// and DeleteCalendar refuses.
func (f *FakeMailboxCalendar) PutCalendar(mailbox, name string, ours bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	id := f.id("calendar")
	m.calendars[id] = name
	if !ours {
		m.foreign[id] = true
	}
	return id
}

// Calendars answers a mailbox's secondary calendars, id → name.
func (f *FakeMailboxCalendar) Calendars(mailbox string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.mailboxes[mailbox]; m != nil {
		return maps.Clone(m.calendars)
	}
	return map[string]string{}
}

// Events answers the events in a mailbox's calendar — calendarID "" the
// default one — ordered by id.
func (f *FakeMailboxCalendar) Events(mailbox, calendarID string) []FakeMailboxEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FakeMailboxEvent
	if m := f.mailboxes[mailbox]; m != nil {
		for _, e := range m.events {
			if e.CalendarID == calendarID {
				out = append(out, *e)
			}
		}
	}
	slices.SortFunc(out, func(a, b FakeMailboxEvent) int { return compareIDs(a.ID, b.ID) })
	return out
}

// Calls answers every call recorded so far, in order.
func (f *FakeMailboxCalendar) Calls() []MailboxCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Key is the provider's key, "entra" unless WithKey set another.
func (f *FakeMailboxCalendar) Key() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

// Status answers what SetStatus set, or FailWith's error.
func (f *FakeMailboxCalendar) Status(context.Context) (contracts.MailboxStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Status"})
	if err := f.fail["Status"]; err != nil {
		return contracts.MailboxStatus{}, err
	}
	return f.status, nil
}

// EnsureCalendar keeps known when the mailbox still holds it and it is the
// provider's; otherwise it adopts the provider's calendar named name, or
// makes one — a name a foreign calendar holds skipped for the next, up to
// "<name> (5)", then a failure.
func (f *FakeMailboxCalendar) EnsureCalendar(_ context.Context, mailbox, known, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("EnsureCalendar", mailbox, known, nil, false); err != nil {
		return "", err
	}
	m := f.mailbox(mailbox)
	if _, ok := m.calendars[known]; known != "" && ok && !m.foreign[known] {
		return known, nil
	}
	for n := 1; n <= 5; n++ {
		want := name
		if n > 1 {
			want = name + " (" + strconv.Itoa(n) + ")"
		}
		taken := false
		for _, id := range slices.SortedFunc(maps.Keys(m.calendars), compareIDs) {
			if m.calendars[id] != want {
				continue
			}
			if !m.foreign[id] {
				return id, nil
			}
			taken = true
		}
		if !taken {
			id := f.id("calendar")
			m.calendars[id] = want
			return id, nil
		}
	}
	return "", errors.New("modtest: every name of the calendar is taken by a foreign one")
}

// DeleteCalendar drops the calendar and its events; one already gone is
// done, a foreign one ErrCalendarNotOurs.
func (f *FakeMailboxCalendar) DeleteCalendar(_ context.Context, mailbox, calendarID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("DeleteCalendar", mailbox, calendarID, nil, true); err != nil {
		return err
	}
	if calendarID == "" {
		return errors.New("modtest: the default calendar cannot be deleted")
	}
	m := f.mailbox(mailbox)
	if m.foreign[calendarID] {
		return contracts.ErrCalendarNotOurs
	}
	delete(m.calendars, calendarID)
	for id, e := range m.events {
		if e.CalendarID == calendarID {
			delete(m.events, id)
		}
	}
	return nil
}

// Apply runs ops against the calendar and answers one result per op, in
// order.
func (f *FakeMailboxCalendar) Apply(_ context.Context, mailbox, calendarID string, ops []contracts.MailboxOp) ([]contracts.MailboxResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Apply", mailbox, calendarID, ops, true); err != nil {
		return nil, err
	}
	m := f.mailbox(mailbox)
	if _, ok := m.calendars[calendarID]; calendarID != "" && !ok {
		return nil, errFakeNoSuchCalendar
	}
	cleanup := f.status.State == contracts.MailboxCleanup
	out := make([]contracts.MailboxResult, len(ops))
	for i, op := range ops {
		if cleanup && op.Kind != contracts.MailboxOpDelete {
			out[i] = contracts.MailboxResult{Err: contracts.ErrMailboxUnavailable}
			continue
		}
		if f.failOps > 0 {
			f.failOps--
			out[i] = contracts.MailboxResult{EventID: op.EventID, Err: f.failOpErr}
			continue
		}
		switch op.Kind {
		case contracts.MailboxOpCreate:
			if op.Event == nil {
				out[i] = contracts.MailboxResult{Err: errors.New("modtest: a create names no event")}
				continue
			}
			id := f.create(m, calendarID, op.Key, *op.Event)
			out[i] = contracts.MailboxResult{EventID: id, ChangeKey: m.events[id].ChangeKey}
		case contracts.MailboxOpUpdate:
			e := m.events[op.EventID]
			if e == nil || e.CalendarID != calendarID {
				out[i] = contracts.MailboxResult{EventID: op.EventID, Gone: true}
				continue
			}
			if op.Event == nil {
				out[i] = contracts.MailboxResult{EventID: op.EventID, Err: errors.New("modtest: an update names no event")}
				continue
			}
			e.Event = *op.Event
			e.ChangeKey = f.id("change")
			out[i] = contracts.MailboxResult{EventID: op.EventID, ChangeKey: e.ChangeKey}
		case contracts.MailboxOpDelete:
			e := m.events[op.EventID]
			if e == nil || e.CalendarID != calendarID {
				out[i] = contracts.MailboxResult{EventID: op.EventID, Gone: true}
				continue
			}
			delete(m.events, op.EventID)
			out[i] = contracts.MailboxResult{EventID: op.EventID}
		default:
			out[i] = contracts.MailboxResult{EventID: op.EventID, Err: errors.New("modtest: unknown op kind " + strconv.Quote(op.Kind))}
		}
	}
	return out, nil
}

// Find answers the events of the calendar carrying a key, starting at or
// after from when it is set, ordered by id.
func (f *FakeMailboxCalendar) Find(_ context.Context, mailbox, calendarID string, from time.Time) ([]contracts.MailboxEventRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Find", Mailbox: mailbox, CalendarID: calendarID, From: from})
	if err := f.refusal("Find", mailbox, true); err != nil {
		return nil, err
	}
	m := f.mailbox(mailbox)
	if _, ok := m.calendars[calendarID]; calendarID != "" && !ok {
		return nil, errFakeNoSuchCalendar
	}
	var out []contracts.MailboxEventRef
	for _, e := range m.events {
		if e.CalendarID != calendarID || e.Key == "" {
			continue
		}
		start := e.Event.StartsAt
		if e.Event.AllDay {
			start = e.Event.StartDate
		}
		if !from.IsZero() && start.Before(from) {
			continue
		}
		out = append(out, contracts.MailboxEventRef{EventID: e.ID, Key: e.Key})
	}
	slices.SortFunc(out, func(a, b contracts.MailboxEventRef) int { return compareIDs(a.EventID, b.EventID) })
	return out, nil
}

// begin records the call and answers the refusal it meets, if any: the
// status (cleanupOK: the call is one MailboxCleanup allows), the mailbox
// unknown or refused, a FailWith. f.mu is held.
func (f *FakeMailboxCalendar) begin(method, mailbox, calendarID string, ops []contracts.MailboxOp, cleanupOK bool) error {
	f.calls = append(f.calls, MailboxCall{Method: method, Mailbox: mailbox, CalendarID: calendarID, Ops: slices.Clone(ops)})
	return f.refusal(method, mailbox, cleanupOK)
}

// refusal is the refusal a call of method naming mailbox meets, if any.
// f.mu is held.
func (f *FakeMailboxCalendar) refusal(method, mailbox string, cleanupOK bool) error {
	switch f.status.State {
	case contracts.MailboxAvailable:
	case contracts.MailboxCleanup:
		if !cleanupOK {
			return contracts.ErrMailboxUnavailable
		}
	default:
		return contracts.ErrMailboxUnavailable
	}
	if f.unknown[mailbox] {
		return contracts.ErrMailboxUnknown
	}
	if err := f.refusals[mailbox]; err != nil {
		return err
	}
	return f.fail[method]
}

// mailbox answers the mailbox, made on its first use. f.mu is held.
func (f *FakeMailboxCalendar) mailbox(id string) *fakeMailbox {
	m := f.mailboxes[id]
	if m == nil {
		m = &fakeMailbox{calendars: map[string]string{}, foreign: map[string]bool{}, events: map[string]*FakeMailboxEvent{}}
		f.mailboxes[id] = m
	}
	return m
}

// create stores an event and answers its id. f.mu is held.
func (f *FakeMailboxCalendar) create(m *fakeMailbox, calendarID, key string, event contracts.MailboxEvent) string {
	id := f.id("event")
	m.events[id] = &FakeMailboxEvent{ID: id, CalendarID: calendarID, Key: key, Event: event, ChangeKey: f.id("change")}
	return id
}

// id answers a new id, "<prefix>-<n>", n counting every id handed out.
// f.mu is held.
func (f *FakeMailboxCalendar) id(prefix string) string {
	f.next++
	return prefix + "-" + strconv.Itoa(f.next)
}

// compareIDs orders the fake's ids by the number they end in, so the
// creation order holds past nine.
func compareIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
