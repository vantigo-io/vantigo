package modtest

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
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
// existing one — an update naming a Key writes it as the event's marker, so
// an unmarked event becomes a copy — and answers Gone for an update or a
// delete of an event that no longer exists; every event carries a ChangeKey
// that moves with each write to it, and each create's and update's result
// answers the new one; Find answers the events carrying a key in any calendar
// — an event with none is never the provider's — starting at or after its
// from when that is set (a timed event's StartsAt, an all-day one's
// StartDate). A mailbox is made on its first use.
//
// Its default calendar keeps a change log, as a directory's delta does
// (design O9.1): every create, update and delete of an event in it — a copy
// Apply writes, an event PutEvent places, the person's own events
// SeedForeign, MoveForeign and RemoveForeign shape — is logged with a
// sequence, so Delta answers the calendar's copies as a real delta would,
// beside the person's events: an event as a change with its times and its
// ShowAs (a copy's from its MailboxEvent), Cancelled and Series as the test
// set them. Delta with link "" answers every event of the default calendar
// whose times overlap from..to; with a link, the events changed since it,
// one gone or out of the window as Removed. It answers pages of at most
// SetDeltaPageSize changes (default 1000): Done false with a next link while
// more remain, Done true with the delta link at the end. Its links are
// "fake-delta:<mailbox>:<n>"; one for another mailbox, or one it never
// answered, is refused with a plain error, and ExpireDelta makes every link
// it answered for a mailbox so far answer ErrMailboxDeltaExpired. Marked
// answers, among the ids asked, the events carrying a key.
//
// The person's edits in Outlook are shaped as the write-back reads them
// (design O10): EditEvent rewrites an event as they would, TouchEvent moves
// its change key alone (Exchange's own touch), DeleteEvent deletes it,
// MoveEvent moves it to another calendar under a new id, SetMeeting gives it
// attendees, SetHTMLBody makes its body come as HTML, SetOtherInstallation
// gives it another installation's marker, and PutOwnEvent places an
// unmarked single event of the person's in any calendar. Versions answers,
// for the default calendar, the events carrying a key that start at or
// after from (Find's rule), each with its key and change key; for a
// secondary calendar every event of it overlapping from..to, with no key;
// ordered by id, at most contracts.MailboxVersionsMax, Complete when not
// cut. Events answers, among the ids asked, the events of that calendar
// whole, in the order asked — an event of another calendar, or one gone, in
// Gone, and one SetUnreadable marks in neither. Find answers Meeting for an
// event SetMeeting made one.
//
// Its notifications are a set and a queue (design O11.3): Watch adds the
// mailbox to the watched set and Unwatch takes it out — Watched, WatchCalls
// and UnwatchCalls answer them — and Notify queues a notice a directory
// would have sent, which Notified answers once its At is after since, oldest
// first, at most contracts.MailboxNoticesMax.
//
// Its connections are the person's own (Outlook connection design M9):
// SetConnection makes every call naming a mailbox answer
// contracts.ConnectionNeededError with a reason, except the removals —
// DeleteCalendar, Find, Unwatch, and Apply when every op is a delete — while
// the reason is closing and SetConnectionToken says the token is held; a
// removal on a closing connection with no token, or on none
// (not_connected), answers ErrMailboxGone. ChangeConnection records a
// change of a connection, which Connections answers once its At is after
// since, oldest first, at most contracts.ConnectionChangesMax.
//
// Its refusals are the contract's: while the status is
// contracts.MailboxUnavailable every call but Status answers
// ErrMailboxUnavailable; under contracts.MailboxCleanup EnsureCalendar
// answers it and Apply takes deletes only, every other op answering it in
// its result, and Delta, Marked, Versions, Events and Watch answer it;
// Unwatch is allowed, and Notified, the provider's own rows, answers under
// any status. A mailbox
// SetUnknown marks answers ErrMailboxUnknown to every
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
	pageSize  int // SetDeltaPageSize
	links     map[string]*fakeDeltaLink
	expired   map[string]bool           // the links ExpireDelta expired
	linkNext  int                       // the last link number handed out
	onDelta   func(n int)               // BeforeDelta's
	onStatus  func()                    // BeforeStatus's
	deltas    int                       // the Delta calls so far
	watched   map[string]bool           // the mailboxes Watch watches
	watches   []string                  // every mailbox Watch was asked for, in order
	unwatches []string                  // every mailbox Unwatch was asked for, in order
	notices   []contracts.MailboxNotice // Notify's
	conns     map[string]string         // by mailbox: SetConnection's reason
	tokens    map[string]bool           // by mailbox: SetConnectionToken's
	changes   []contracts.ConnectionChange
}

// fakeDeltaLink is what a link Delta answered stands for: the window the
// delta was started over and, for a delta link (not next), the sequence of
// the mailbox's log it was read to; for a next link, the changes still to
// answer.
type fakeDeltaLink struct {
	from, to time.Time
	seq      int
	next     bool
	pending  []contracts.MailboxChange
}

// fakeDeltaPrefix starts every link the fake answers.
const fakeDeltaPrefix = "fake-delta:"

// MailboxCall is one call a FakeMailboxCalendar recorded: the method, the
// mailbox and the calendar it named, a copy of the ops Apply was given, the
// from Find was given, the window and the link Delta was given, and a copy
// of the ids Marked was given.
type MailboxCall struct {
	Method, Mailbox, CalendarID string
	Ops                         []contracts.MailboxOp
	From, To                    time.Time
	Link                        string
	EventIDs                    []string
}

// FakeMailboxEvent is one event of the fake's mailbox, as EventsIn answers
// it. Cancelled and Series are what Delta, Versions and Events answer of it:
// set only on the person's own events (SeedForeign, MoveForeign). Meeting,
// HTMLBody and OtherInstallation are what Events answers of it, as
// SetMeeting, SetHTMLBody and SetOtherInstallation set them.
type FakeMailboxEvent struct {
	ID, CalendarID, Key string
	Event               contracts.MailboxEvent
	ChangeKey           string
	Cancelled, Series   bool
	Meeting, HTMLBody   bool
	OtherInstallation   bool
	// Unreadable: Events skips it (SetUnreadable).
	Unreadable bool
}

// FakeBusyEvent is one of the person's own events of the default calendar,
// as SeedForeign and MoveForeign shape it: its times, its ShowAs, and
// whether it is cancelled or belongs to a series. AllDay events are
// StartDate..EndDate, civil dates at midnight UTC, EndDate inclusive.
type FakeBusyEvent struct {
	ID                 string
	AllDay             bool
	StartDate, EndDate time.Time
	StartsAt, EndsAt   time.Time
	ShowAs             string
	Cancelled, Series  bool
}

// fakeMailbox is one mailbox of the fake: its secondary calendars, the ones
// of them the provider did not make, its events, by id, and its default
// calendar's change log.
type fakeMailbox struct {
	calendars map[string]string // id → name
	foreign   map[string]bool
	events    map[string]*FakeMailboxEvent
	seq       int // the last sequence logged
	log       []fakeLogEntry
}

// fakeLogEntry is one change of the default calendar's log: the event
// created, rewritten or deleted, and its sequence.
type fakeLogEntry struct {
	seq int
	id  string
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
		pageSize:  1000,
		links:     map[string]*fakeDeltaLink{},
		expired:   map[string]bool{},
		watched:   map[string]bool{},
		conns:     map[string]string{},
		tokens:    map[string]bool{},
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

// SetConnection makes every call naming mailbox answer
// contracts.ConnectionNeededError with reason — contracts.ConnectionNotConnected,
// contracts.ConnectionClosingReason or a reconnect code — the removals
// excepted as the type says, or (reason "") clears it.
func (f *FakeMailboxCalendar) SetConnection(mailbox, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reason == "" {
		delete(f.conns, mailbox)
	} else {
		f.conns[mailbox] = reason
	}
}

// SetConnectionToken says whether mailbox's closing connection still holds
// its token, so its removals are served (held) or answer ErrMailboxGone.
func (f *FakeMailboxCalendar) SetConnectionToken(mailbox string, held bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[mailbox] = held
}

// ChangeConnection records that mailbox's connection moved to state
// (contracts.ConnectionActive, ConnectionReconnect or ConnectionClosing) at
// at, for Connections to answer. It changes no call's answer: SetConnection
// does.
func (f *FakeMailboxCalendar) ChangeConnection(mailbox, state string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, contracts.ConnectionChange{Mailbox: mailbox, State: state, At: at})
}

// FailWith makes every later call of method ("Status", "EnsureCalendar",
// "DeleteCalendar", "Apply", "Find", "Delta", "Marked", "Versions",
// "Events", "Watch", "Unwatch", "Notified", "Connections") answer err, or (err nil) clears
// it. A Watch or an Unwatch refused so changes nothing.
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

// SeedForeign places one of the person's own events in a mailbox's default
// calendar — no key, never the provider's — and answers its id: event.ID,
// or a new one when that is "". It is logged, so the next delta answers it.
func (f *FakeMailboxCalendar) SeedForeign(mailbox string, event FakeBusyEvent) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	id := event.ID
	if id == "" {
		id = f.id("event")
	}
	m.events[id] = &FakeMailboxEvent{ID: id, Event: busyEvent(event), ChangeKey: f.id("change"), Cancelled: event.Cancelled, Series: event.Series}
	f.logChange(m, "", id)
	return id
}

// MoveForeign rewrites the default calendar's event event.ID to event's
// times, ShowAs and flags, as the person editing it in Outlook would — a
// copy of the provider's keeps its key and its subject — and logs it; an
// event not there is placed, as SeedForeign does.
func (f *FakeMailboxCalendar) MoveForeign(mailbox string, event FakeBusyEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	e := m.events[event.ID]
	if e == nil || e.CalendarID != "" {
		e = &FakeMailboxEvent{ID: event.ID}
		m.events[event.ID] = e
	}
	moved := busyEvent(event)
	moved.Subject, moved.Body, moved.Location, moved.TimeZone, moved.Private = e.Event.Subject, e.Event.Body, e.Event.Location, e.Event.TimeZone, e.Event.Private
	e.Event, e.Cancelled, e.Series, e.ChangeKey = moved, event.Cancelled, event.Series, f.id("change")
	f.logChange(m, "", event.ID)
}

// RemoveForeign deletes the default calendar's event id, as the person
// deleting it in Outlook would, and logs it.
func (f *FakeMailboxCalendar) RemoveForeign(mailbox, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	if e := m.events[id]; e != nil && e.CalendarID == "" {
		delete(m.events, id)
		f.logChange(m, "", id)
	}
}

// BeforeStatus makes every Status call fn first, with no lock held — for a
// test holding a sync in the middle of its calls out.
func (f *FakeMailboxCalendar) BeforeStatus(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onStatus = fn
}

// BeforeDelta makes every Delta call fn first, with no lock held, numbered
// from 1 — for a test changing the person's setting in the middle of a read.
func (f *FakeMailboxCalendar) BeforeDelta(fn func(n int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onDelta = fn
}

// SetDeltaPageSize makes Delta answer pages of at most n changes.
func (f *FakeMailboxCalendar) SetDeltaPageSize(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSize = max(n, 1)
}

// ExpireDelta makes every link Delta answered for mailbox so far — next and
// delta links alike — answer ErrMailboxDeltaExpired from now on, as a
// directory that forgot the delta's state does; a delta started afresh
// answers links that work.
func (f *FakeMailboxCalendar) ExpireDelta(mailbox string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for link := range f.links {
		if strings.HasPrefix(link, fakeDeltaPrefix+mailbox+":") {
			f.expired[link] = true
			delete(f.links, link)
		}
	}
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

// EditEvent rewrites the event id, in whichever calendar of the mailbox
// holds it, by fn, as the person editing it in Outlook would: its key and
// its flags kept, its change key moved, the change logged. An event not
// there is left so.
func (f *FakeMailboxCalendar) EditEvent(mailbox, id string, fn func(*contracts.MailboxEvent)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	if e := m.events[id]; e != nil {
		fn(&e.Event)
		e.ChangeKey = f.id("change")
		f.logChange(m, e.CalendarID, id)
	}
}

// TouchEvent moves the event id's change key and nothing else, as Exchange
// touching an event of its own accord does, and logs it.
func (f *FakeMailboxCalendar) TouchEvent(mailbox, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	if e := m.events[id]; e != nil {
		e.ChangeKey = f.id("change")
		f.logChange(m, e.CalendarID, id)
	}
}

// DeleteEvent deletes the event id, in whichever calendar holds it, as the
// person deleting it in Outlook would, and logs it.
func (f *FakeMailboxCalendar) DeleteEvent(mailbox, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	if e := m.events[id]; e != nil {
		delete(m.events, id)
		f.logChange(m, e.CalendarID, id)
	}
}

// MoveEvent moves the event id to the calendar toCalendarID ("" the default
// one), as the person dragging it there in Outlook would, and answers its
// new id: a directory without immutable ids gives a moved event a new one.
// Its key, its words and its flags go with it, its change key moves, and
// both calendars log the change. An event not there answers "".
func (f *FakeMailboxCalendar) MoveEvent(mailbox, id, toCalendarID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	e := m.events[id]
	if e == nil {
		return ""
	}
	delete(m.events, id)
	f.logChange(m, e.CalendarID, id)
	moved := *e
	moved.ID, moved.CalendarID, moved.ChangeKey = f.id("event"), toCalendarID, f.id("change")
	m.events[moved.ID] = &moved
	f.logChange(m, toCalendarID, moved.ID)
	return moved.ID
}

// SetMeeting gives the event id attendees, or (on false) takes them away,
// as the person inviting someone to it in Outlook would: its change key
// moves and the change is logged.
func (f *FakeMailboxCalendar) SetMeeting(mailbox, id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.mailbox(mailbox)
	if e := m.events[id]; e != nil {
		e.Meeting = on
		e.ChangeKey = f.id("change")
		f.logChange(m, e.CalendarID, id)
	}
}

// SetUnreadable makes Events skip the event id — neither answered nor
// Gone, as a provider that could not read it this time — or (on false)
// read it again.
func (f *FakeMailboxCalendar) SetUnreadable(mailbox, id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.mailbox(mailbox).events[id]; e != nil {
		e.Unreadable = on
	}
}

// SetHTMLBody makes the event id's body come as HTML to Events (TextBody
// false), or (on false) as text again. It is how the body is answered, not
// a write: the change key stays, so a test pairs it with EditEvent.
func (f *FakeMailboxCalendar) SetHTMLBody(mailbox, id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.mailbox(mailbox).events[id]; e != nil {
		e.HTMLBody = on
	}
}

// SetOtherInstallation gives the event id another installation's marker in
// place of this one's — its key cleared, Events answering it Foreign — or
// (on false) takes that marker away. The change key stays.
func (f *FakeMailboxCalendar) SetOtherInstallation(mailbox, id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.mailbox(mailbox).events[id]; e != nil {
		e.OtherInstallation = on
		if on {
			e.Key = ""
		}
	}
}

// PutOwnEvent places one of the person's own events — single, no marker —
// in a mailbox's calendar (calendarID "" the default one) and answers its
// id: an event they made in Outlook, in the provider's own calendar what
// the write-back takes in (design O10.9). It is logged.
func (f *FakeMailboxCalendar) PutOwnEvent(mailbox, calendarID string, event contracts.MailboxEvent) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.create(f.mailbox(mailbox), calendarID, "", event)
}

// EventsIn answers the events in a mailbox's calendar — calendarID "" the
// default one — ordered by id.
func (f *FakeMailboxCalendar) EventsIn(mailbox, calendarID string) []FakeMailboxEvent {
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

// Status answers what SetStatus set, or FailWith's error, BeforeStatus's fn
// called first.
func (f *FakeMailboxCalendar) Status(context.Context) (contracts.MailboxStatus, error) {
	f.mu.Lock()
	fn := f.onStatus
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
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
			if op.Key != "" {
				e.Key, e.OtherInstallation = op.Key, false
			}
			e.ChangeKey = f.id("change")
			f.logChange(m, calendarID, op.EventID)
			out[i] = contracts.MailboxResult{EventID: op.EventID, ChangeKey: e.ChangeKey}
		case contracts.MailboxOpDelete:
			e := m.events[op.EventID]
			if e == nil || e.CalendarID != calendarID {
				out[i] = contracts.MailboxResult{EventID: op.EventID, Gone: true}
				continue
			}
			delete(m.events, op.EventID)
			f.logChange(m, calendarID, op.EventID)
			out[i] = contracts.MailboxResult{EventID: op.EventID}
		default:
			out[i] = contracts.MailboxResult{EventID: op.EventID, Err: errors.New("modtest: unknown op kind " + strconv.Quote(op.Kind))}
		}
	}
	return out, nil
}

// Find answers the events of the calendar carrying a key, starting at or
// after from when it is set, ordered by id, each Meeting as SetMeeting made
// it; a secondary calendar the mailbox does not hold answers none.
func (f *FakeMailboxCalendar) Find(_ context.Context, mailbox, calendarID string, from time.Time) ([]contracts.MailboxEventRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Find", Mailbox: mailbox, CalendarID: calendarID, From: from})
	if err := f.refusal("Find", mailbox, true, nil); err != nil {
		return nil, err
	}
	m := f.mailbox(mailbox)
	var out []contracts.MailboxEventRef
	if _, ok := m.calendars[calendarID]; calendarID != "" && !ok {
		// A calendar that is gone holds nothing of ours.
		return out, nil
	}
	for _, e := range m.events {
		if e.CalendarID != calendarID || e.Key == "" {
			continue
		}
		if !from.IsZero() && start(e).Before(from) {
			continue
		}
		out = append(out, contracts.MailboxEventRef{EventID: e.ID, Key: e.Key, Meeting: e.Meeting})
	}
	slices.SortFunc(out, func(a, b contracts.MailboxEventRef) int { return compareIDs(a.EventID, b.EventID) })
	return out, nil
}

// Delta answers a page of the default calendar's changes: with link "",
// every event overlapping from..to; with a link, the changes since it.
func (f *FakeMailboxCalendar) Delta(_ context.Context, mailbox string, from, to time.Time, link string) (contracts.MailboxDelta, error) {
	f.mu.Lock()
	f.deltas++
	fn, n := f.onDelta, f.deltas
	f.mu.Unlock()
	if fn != nil {
		fn(n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Delta", Mailbox: mailbox, From: from, To: to, Link: link})
	if err := f.refusal("Delta", mailbox, false, nil); err != nil {
		return contracts.MailboxDelta{}, err
	}
	m := f.mailbox(mailbox)
	var state fakeDeltaLink
	switch {
	case link == "":
		state = fakeDeltaLink{from: from, to: to, seq: m.seq, pending: f.window(m, from, to)}
	case !strings.HasPrefix(link, fakeDeltaPrefix+mailbox+":"):
		return contracts.MailboxDelta{}, errors.New("modtest: a delta link not answered for this mailbox")
	case f.expired[link]:
		return contracts.MailboxDelta{}, contracts.ErrMailboxDeltaExpired
	case f.links[link] == nil:
		return contracts.MailboxDelta{}, errors.New("modtest: no such delta link")
	case f.links[link].next:
		state = *f.links[link]
	default:
		was := f.links[link]
		state = fakeDeltaLink{from: was.from, to: was.to, seq: m.seq, pending: f.since(m, was)}
	}
	page, rest := state.pending, []contracts.MailboxChange(nil)
	if len(page) > f.pageSize {
		page, rest = page[:f.pageSize], page[f.pageSize:]
	}
	f.linkNext++
	answered := fakeDeltaPrefix + mailbox + ":" + strconv.Itoa(f.linkNext)
	f.links[answered] = &fakeDeltaLink{from: state.from, to: state.to, seq: state.seq, next: len(rest) > 0, pending: slices.Clone(rest)}
	return contracts.MailboxDelta{Changes: slices.Clone(page), Link: answered, Done: len(rest) == 0}, nil
}

// Marked answers, among eventIDs, the events carrying a key, in the order
// asked; more than contracts.MailboxMarkedMax ids is a failure.
func (f *FakeMailboxCalendar) Marked(_ context.Context, mailbox string, eventIDs []string) ([]contracts.MailboxEventRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Marked", Mailbox: mailbox, EventIDs: slices.Clone(eventIDs)})
	if err := f.refusal("Marked", mailbox, false, nil); err != nil {
		return nil, err
	}
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return nil, errors.New("modtest: Marked asked for more than MailboxMarkedMax ids")
	}
	m := f.mailbox(mailbox)
	out := []contracts.MailboxEventRef{}
	for _, id := range eventIDs {
		if e := m.events[id]; e != nil && e.Key != "" {
			out = append(out, contracts.MailboxEventRef{EventID: id, Key: e.Key})
		}
	}
	return out, nil
}

// Versions answers the versions of the calendar's events: for the default
// one, those carrying a key that start at or after from when it is set,
// with their keys; for a secondary one, every event of it overlapping
// from..to, with no key; ordered by id, cut at
// contracts.MailboxVersionsMax.
func (f *FakeMailboxCalendar) Versions(_ context.Context, mailbox, calendarID string, from, to time.Time) (contracts.MailboxVersions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Versions", Mailbox: mailbox, CalendarID: calendarID, From: from, To: to})
	if err := f.refusal("Versions", mailbox, false, nil); err != nil {
		return contracts.MailboxVersions{}, err
	}
	m := f.mailbox(mailbox)
	if _, ok := m.calendars[calendarID]; calendarID != "" && !ok {
		return contracts.MailboxVersions{}, errFakeNoSuchCalendar
	}
	out := contracts.MailboxVersions{Events: []contracts.MailboxEventVersion{}, Complete: true}
	for _, id := range slices.SortedFunc(maps.Keys(m.events), compareIDs) {
		e := m.events[id]
		if e.CalendarID != calendarID {
			continue
		}
		version := contracts.MailboxEventVersion{EventID: id, ChangeKey: e.ChangeKey, Series: e.Series}
		if calendarID == "" {
			if e.Key == "" || !from.IsZero() && start(e).Before(from) {
				continue
			}
			version.Key = e.Key
		} else if !overlaps(e, from, to) {
			continue
		}
		if len(out.Events) == contracts.MailboxVersionsMax {
			out.Complete = false
			break
		}
		out.Events = append(out.Events, version)
	}
	return out, nil
}

// Events answers, among eventIDs, the events of the calendar whole, in the
// order asked; an id of another calendar, or gone, is answered in Gone, one
// SetUnreadable marks in neither, and more than contracts.MailboxMarkedMax
// ids is a failure.
func (f *FakeMailboxCalendar) Events(_ context.Context, mailbox, calendarID string, eventIDs []string) (contracts.MailboxEventsRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Events", Mailbox: mailbox, CalendarID: calendarID, EventIDs: slices.Clone(eventIDs)})
	if err := f.refusal("Events", mailbox, false, nil); err != nil {
		return contracts.MailboxEventsRead{}, err
	}
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return contracts.MailboxEventsRead{}, errors.New("modtest: Events asked for more than MailboxMarkedMax ids")
	}
	m := f.mailbox(mailbox)
	if _, ok := m.calendars[calendarID]; calendarID != "" && !ok {
		return contracts.MailboxEventsRead{}, errFakeNoSuchCalendar
	}
	out := contracts.MailboxEventsRead{Events: []contracts.MailboxEventState{}, Gone: []string{}}
	for _, id := range eventIDs {
		e := m.events[id]
		if e == nil || e.CalendarID != calendarID {
			out.Gone = append(out.Gone, id)
			continue
		}
		if e.Unreadable {
			continue
		}
		out.Events = append(out.Events, contracts.MailboxEventState{
			EventID: id, Key: e.Key, ChangeKey: e.ChangeKey,
			Foreign: e.OtherInstallation, Series: e.Series, Cancelled: e.Cancelled,
			Meeting: e.Meeting, TextBody: !e.HTMLBody, Event: e.Event,
		})
	}
	return out, nil
}

// Watch watches the mailbox: it joins the watched set, unless the call is
// refused.
func (f *FakeMailboxCalendar) Watch(_ context.Context, mailbox string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watches = append(f.watches, mailbox)
	if err := f.begin("Watch", mailbox, "", nil, false); err != nil {
		return err
	}
	f.watched[mailbox] = true
	return nil
}

// Unwatch stops watching the mailbox, allowed under cleanup: it leaves the
// watched set, unless the call is refused. A mailbox not watched is done.
func (f *FakeMailboxCalendar) Unwatch(_ context.Context, mailbox string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unwatches = append(f.unwatches, mailbox)
	if err := f.begin("Unwatch", mailbox, "", nil, true); err != nil {
		return err
	}
	delete(f.watched, mailbox)
	return nil
}

// Notified answers the notices Notify queued whose At is after since, oldest
// first, at most contracts.MailboxNoticesMax; its From is since in Calls.
func (f *FakeMailboxCalendar) Notified(_ context.Context, since time.Time) ([]contracts.MailboxNotice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Notified", From: since})
	if err := f.fail["Notified"]; err != nil {
		return nil, err
	}
	out := []contracts.MailboxNotice{}
	for _, n := range f.notices {
		if n.At.After(since) {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b contracts.MailboxNotice) int { return a.At.Compare(b.At) })
	if len(out) > contracts.MailboxNoticesMax {
		out = out[:contracts.MailboxNoticesMax]
	}
	return out, nil
}

// Connections answers the changes ChangeConnection recorded whose At is
// after since, oldest first, at most contracts.ConnectionChangesMax; its
// From is since in Calls.
func (f *FakeMailboxCalendar) Connections(_ context.Context, since time.Time) ([]contracts.ConnectionChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, MailboxCall{Method: "Connections", From: since})
	if err := f.fail["Connections"]; err != nil {
		return nil, err
	}
	out := []contracts.ConnectionChange{}
	for _, c := range f.changes {
		if c.At.After(since) {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(out, func(a, b contracts.ConnectionChange) int { return a.At.Compare(b.At) })
	if len(out) > contracts.ConnectionChangesMax {
		out = out[:contracts.ConnectionChangesMax]
	}
	return out, nil
}

// Notify queues a notice that mailbox changed at at, as a directory's
// notification would, for Notified to answer.
func (f *FakeMailboxCalendar) Notify(mailbox string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, contracts.MailboxNotice{Mailbox: mailbox, At: at})
}

// Watched reports whether the mailbox is watched: a Watch done and no
// Unwatch since.
func (f *FakeMailboxCalendar) Watched(mailbox string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.watched[mailbox]
}

// WatchCalls answers every mailbox Watch was asked for, in order, refused
// or not.
func (f *FakeMailboxCalendar) WatchCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.watches)
}

// UnwatchCalls answers every mailbox Unwatch was asked for, in order,
// refused or not.
func (f *FakeMailboxCalendar) UnwatchCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.unwatches)
}

// window answers every event of the default calendar overlapping from..to
// as a change, ordered by id. f.mu is held.
func (f *FakeMailboxCalendar) window(m *fakeMailbox, from, to time.Time) []contracts.MailboxChange {
	var out []contracts.MailboxChange
	for _, id := range slices.SortedFunc(maps.Keys(m.events), compareIDs) {
		if e := m.events[id]; e.CalendarID == "" && overlaps(e, from, to) {
			out = append(out, change(e))
		}
	}
	return out
}

// since answers the events of the default calendar logged after link's
// sequence, each once, in the order of its last change: a change when it is
// there and overlaps link's window, else Removed. f.mu is held.
func (f *FakeMailboxCalendar) since(m *fakeMailbox, link *fakeDeltaLink) []contracts.MailboxChange {
	last := map[string]int{}
	for _, entry := range m.log {
		if entry.seq > link.seq {
			last[entry.id] = entry.seq
		}
	}
	ids := slices.SortedFunc(maps.Keys(last), func(a, b string) int { return last[a] - last[b] })
	out := make([]contracts.MailboxChange, 0, len(ids))
	for _, id := range ids {
		if e := m.events[id]; e != nil && e.CalendarID == "" && overlaps(e, link.from, link.to) {
			out = append(out, change(e))
		} else {
			out = append(out, contracts.MailboxChange{EventID: id, Removed: true})
		}
	}
	return out
}

// logChange logs a change of the event id when it is in the default
// calendar, the only one a delta reads. f.mu is held.
func (f *FakeMailboxCalendar) logChange(m *fakeMailbox, calendarID, id string) {
	if calendarID != "" {
		return
	}
	m.seq++
	m.log = append(m.log, fakeLogEntry{seq: m.seq, id: id})
}

// start is e's first instant: a timed event's StartsAt, an all-day one's
// StartDate.
func start(e *FakeMailboxEvent) time.Time {
	if e.Event.AllDay {
		return e.Event.StartDate
	}
	return e.Event.StartsAt
}

// overlaps reports whether e's times overlap from..to: a timed event's
// instants, an all-day one's dates (EndDate inclusive).
func overlaps(e *FakeMailboxEvent, from, to time.Time) bool {
	if e.Event.AllDay {
		return e.Event.StartDate.Before(to) && e.Event.EndDate.AddDate(0, 0, 1).After(from)
	}
	return e.Event.StartsAt.Before(to) && e.Event.EndsAt.After(from)
}

// change is e as a delta answers it.
func change(e *FakeMailboxEvent) contracts.MailboxChange {
	return contracts.MailboxChange{
		EventID: e.ID, Series: e.Series, Cancelled: e.Cancelled,
		AllDay: e.Event.AllDay, StartDate: e.Event.StartDate, EndDate: e.Event.EndDate,
		StartsAt: e.Event.StartsAt, EndsAt: e.Event.EndsAt, ShowAs: e.Event.ShowAs,
	}
}

// busyEvent is event's times and ShowAs as a MailboxEvent.
func busyEvent(event FakeBusyEvent) contracts.MailboxEvent {
	return contracts.MailboxEvent{
		AllDay: event.AllDay, StartDate: event.StartDate, EndDate: event.EndDate,
		StartsAt: event.StartsAt, EndsAt: event.EndsAt, ShowAs: event.ShowAs,
	}
}

// begin records the call and answers the refusal it meets, if any: the
// status (cleanupOK: the call is one MailboxCleanup allows), the mailbox
// unknown or refused, a FailWith. f.mu is held.
func (f *FakeMailboxCalendar) begin(method, mailbox, calendarID string, ops []contracts.MailboxOp, cleanupOK bool) error {
	f.calls = append(f.calls, MailboxCall{Method: method, Mailbox: mailbox, CalendarID: calendarID, Ops: slices.Clone(ops)})
	return f.refusal(method, mailbox, cleanupOK, ops)
}

// refusal is the refusal a call of method naming mailbox meets, if any:
// the status, the mailbox unknown, its connection (a removal is a call
// cleanup allows, Apply's only when every op of ops is a delete), its
// refusal, a FailWith. f.mu is held.
func (f *FakeMailboxCalendar) refusal(method, mailbox string, cleanupOK bool, ops []contracts.MailboxOp) error {
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
	if reason := f.conns[mailbox]; reason != "" {
		removal := cleanupOK && (method != "Apply" || !slices.ContainsFunc(ops, func(op contracts.MailboxOp) bool {
			return op.Kind != contracts.MailboxOpDelete
		}))
		switch {
		case removal && reason == contracts.ConnectionClosingReason && f.tokens[mailbox]:
		case removal && (reason == contracts.ConnectionClosingReason || reason == contracts.ConnectionNotConnected):
			return contracts.ErrMailboxGone
		default:
			return &contracts.ConnectionNeededError{Reason: reason}
		}
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
	f.logChange(m, calendarID, id)
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
