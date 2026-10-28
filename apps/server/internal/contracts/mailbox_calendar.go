package contracts

import (
	"context"
	"errors"
	"time"
)

// MailboxCalendar is a provider's mailbox calendars as a module offers them
// to the calendar (calendar Outlook sync design O4): Entra's Exchange Online
// today, another directory's mail later. The calendar is the authority — it
// decides what a person's copies are, from their appointments and the
// projected items — and the provider writes them to the person's mailbox and
// answers what it did. The interface names no vendor: a mailbox is an
// account's external id, a calendar an opaque id, an event a MailboxEvent.
//
// It is the sixth sanctioned cross-module write, the calendar into a
// provider's directory, held to rule 13's discipline: every call runs on the
// provider's own pool and reaches the network, so the calendar never makes
// one inside a transaction or holding a connection — only through
// contractscalls.go accessors that refuse such a call — and every call is
// idempotent or answers what it did per operation. The provider writes only
// to an account it manages: an object id it made, or whose adoption is
// confirmed, and not deleted (ErrMailboxUnknown otherwise, design O3), so a
// caller can never point it at a mailbox it does not own.
//
// It is a single-provider slot: a module fills it through
// module.Module.MailboxCalendar, and Compose and Workers resolve the enabled
// module's into module.Deps.MailboxCalendar, refusing two. Its mailbox is the
// person's own contracts.DirectoryAccount (Workforce.DirectoryAccount),
// whose Provider a caller matches against Key. The sentinels below are the
// provider's answers; anything else is a failure to retry — a throttled
// request is retried by the provider within the call, then a failure.
type MailboxCalendar interface {
	// Key is the provider's key — the DirectoryAccount.Provider value,
	// "entra" — answered with no context and no network, so the calendar can
	// tell a person's account is this provider's before it writes.
	Key() string

	// Status is whether the provider may write now: MailboxAvailable,
	// MailboxCleanup or MailboxUnavailable, with a reason code. It reads the
	// provider's own state and never the network.
	Status(ctx context.Context) (MailboxStatus, error)

	// EnsureCalendar answers the id of a calendar the provider made in
	// mailbox, carrying its own marker (calendar Outlook sync design O4):
	// known when it still exists and carries the marker, else one named name
	// that does — made anew, or adopted when the mailbox already holds a
	// calendar of that name carrying this installation's marker. A calendar
	// of the name that is not the provider's is never adopted: the next name
	// is tried ("Vantigo (2)", up to "(5)"), then the call fails. Refused
	// under MailboxCleanup.
	EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error)
	// DeleteCalendar deletes the calendar and every event in it — only a
	// calendar carrying this installation's marker: any other answers
	// ErrCalendarNotOurs and is left whole, so a person's own calendar is
	// never deleted. Idempotent: a calendar already gone answers nil. Allowed
	// under MailboxCleanup, so a person's copies can still be removed.
	DeleteCalendar(ctx context.Context, mailbox, calendarID string) error

	// Apply runs ops against the calendar — calendarID "" is the mailbox's
	// default calendar — and answers one MailboxResult per op, in order. A
	// refusal of one op is its result's Err, carrying the sentinels below;
	// the returned error is the whole call failing (ErrMailboxUnavailable,
	// ErrMailboxUnknown, ErrMailboxGone, ErrMailboxDenied, or a failure to
	// retry), and then no result is answered. Under MailboxCleanup only
	// deletes are taken: every other op answers ErrMailboxUnavailable.
	Apply(ctx context.Context, mailbox, calendarID string, ops []MailboxOp) ([]MailboxResult, error)

	// Find answers the events of the calendar — calendarID "" the default
	// one — that carry this installation's marker, each with its key, and no
	// other: an event of the person's own, or one another installation
	// sharing the tenant wrote, is never answered. A from that is not zero
	// answers only the events that start at or after it; zero is no bound.
	// Bounded at 5000: what the calendar reconciles its map against, and
	// what its clean-up deletes.
	Find(ctx context.Context, mailbox, calendarID string, from time.Time) ([]MailboxEventRef, error)

	// Delta reads the changes to the mailbox's default calendar — the one
	// the directory computes free/busy from — over a window, a page at a
	// time (calendar Outlook sync design O9.1, O9.4). link "" starts a delta
	// afresh over from..to, answering every event whose times overlap the
	// window; from and to are used only then, the window being fixed in the
	// link from that call on. A link the provider answered continues it:
	// the changes since, an event deleted or moved out of the window
	// answered Removed. The link is the provider's opaque string and valid
	// only for the mailbox it was answered for: one that is not — another
	// mailbox's, or no link of the provider's at all — is refused with a
	// failure, so a stored link can never point the provider anywhere else.
	// ErrMailboxDeltaExpired: the provider no longer takes the link, and
	// the caller starts again with "". Refused under MailboxCleanup with
	// ErrMailboxUnavailable: a read is never needed to remove copies.
	//
	// The directory answers an event's subject, body, organizer and
	// attendees in a delta; the contract carries none of them, and the
	// provider decodes none — what a change answers is its times and its
	// ShowAs (design O9.3).
	Delta(ctx context.Context, mailbox string, from, to time.Time, link string) (MailboxDelta, error)

	// Marked answers, among eventIDs — at most MailboxMarkedMax of them —
	// the events that carry this installation's marker, each with its key,
	// in the order asked: the read that tells a copy of the calendar's own
	// that the delta cannot mark (it answers no extended property) from the
	// person's own event (design O9.2). An event of the person's, or one
	// another installation wrote, is not answered, and neither is an id no
	// longer there. Refused under MailboxCleanup with ErrMailboxUnavailable.
	Marked(ctx context.Context, mailbox string, eventIDs []string) ([]MailboxEventRef, error)

	// Versions answers the versions of the events the calendar may have to
	// take back from the mailbox — ids and change keys, nothing of what the
	// events say (calendar Outlook sync design O10.1, O10.2). calendarID ""
	// is the default calendar: only the events carrying this installation's
	// marker that start at or after from (Find's rule), each with its Key;
	// to is not used there. A secondary calendar — the one EnsureCalendar
	// made — answers every event of its view whose times overlap from..to,
	// with no Key: the view is not asked for the marker, the calendar being
	// the provider's whole. At most MailboxVersionsMax, Complete false when
	// the list was cut, so a caller infers nothing from an absence then.
	// Refused under MailboxCleanup with ErrMailboxUnavailable.
	Versions(ctx context.Context, mailbox, calendarID string, from, to time.Time) (MailboxVersions, error)

	// Events reads whole, among eventIDs — at most MailboxMarkedMax of them —
	// the events of the calendar (calendarID "" the default one), in the
	// order asked (design O10.2). Each id is read through the calendar's own
	// path, so an id not answered is not in that calendar any more: deleted,
	// or moved to another calendar or folder. What an event says crosses
	// the contract here for the copies and the provider's own calendar only,
	// for the write it feeds, and a provider never logs a word of it.
	// Refused under MailboxCleanup with ErrMailboxUnavailable.
	Events(ctx context.Context, mailbox, calendarID string, eventIDs []string) ([]MailboxEventState, error)
}

// The states of MailboxStatus.State.
const (
	// MailboxAvailable: the provider may write (the admin switch on, the
	// permission granted, the connection working).
	MailboxAvailable = "available"
	// MailboxCleanup: the switch off or the permission missing, a credential
	// still working — Apply takes deletes only and DeleteCalendar works, so a
	// person's copies can still be removed.
	MailboxCleanup = "cleanup"
	// MailboxUnavailable: the provider can write nothing — not configured,
	// or its credential refused.
	MailboxUnavailable = "unavailable"
)

// MailboxStatus is the provider's state as Status answers it.
type MailboxStatus struct {
	// State is MailboxAvailable, MailboxCleanup or MailboxUnavailable.
	State string
	// Reason is a code — never the directory's words — for a state other
	// than available ("switch_off", "permission_missing"); "" when none.
	Reason string
}

// The kinds of MailboxOp.Kind.
const (
	// MailboxOpCreate makes a new event carrying the op's Key.
	MailboxOpCreate = "create"
	// MailboxOpUpdate rewrites the event EventID to the op's Event.
	MailboxOpUpdate = "update"
	// MailboxOpDelete deletes the event EventID.
	MailboxOpDelete = "delete"
)

// MailboxOp is one write Apply makes. A create names Key and Event; an update
// EventID and Event, and Key when it writes the marker too; a delete EventID.
type MailboxOp struct {
	Kind    string
	EventID string
	// Key is written on create as the event's marker: the calendar's own key
	// for the item ("appointment:42"). An update writes it as the marker when
	// it is set — the same value a copy already carries — so an event the
	// calendar took in from its own calendar is tagged by the push that
	// rewrites it (design O10.2); "" leaves the marker as it is.
	Key   string
	Event *MailboxEvent
}

// The values of MailboxEvent.ShowAs.
const (
	ShowAsFree             = "free"
	ShowAsTentative        = "tentative"
	ShowAsBusy             = "busy"
	ShowAsOOF              = "oof"
	ShowAsWorkingElsewhere = "workingElsewhere"
)

// MailboxEvent is an event as the calendar wants it in the mailbox: no
// attendees, no reminder, no category (design O6).
type MailboxEvent struct {
	Subject, Body, Location string
	// AllDay events are StartDate..EndDate, civil dates at midnight UTC,
	// EndDate inclusive; the provider lays them at midnight in TimeZone.
	AllDay             bool
	StartDate, EndDate time.Time
	// StartsAt and EndsAt are a timed event's instants.
	StartsAt, EndsAt time.Time
	// TimeZone is the IANA zone of an all-day event ("Europe/Oslo"); ""
	// is UTC.
	TimeZone string
	// ShowAs is one of the ShowAs constants.
	ShowAs string
	// Private marks the event private in the mailbox.
	Private bool
}

// MailboxResult is what Apply did with one op.
type MailboxResult struct {
	// EventID is the event the op made or wrote.
	EventID string
	// Gone: the update's or delete's event no longer exists. A delete is
	// then done; an update is the caller's to re-create.
	Gone bool
	// ChangeKey is the event's version after the write, "" when the
	// provider has none.
	ChangeKey string
	// Err is the op's refusal or failure, nil when it was done.
	Err error
}

// MailboxEventRef is one event Find or Marked answers: its id and the key it
// carries.
type MailboxEventRef struct {
	EventID, Key string
}

// MailboxMarkedMax is the most ids one Marked call takes; the caller hands it
// slices of at most this many.
const MailboxMarkedMax = 100

// MailboxVersionsMax is the most events one Versions call answers; past it
// the list is cut and MailboxVersions.Complete is false.
const MailboxVersionsMax = 5000

// MailboxVersions is what Versions answers (calendar Outlook sync design
// O10.2).
type MailboxVersions struct {
	// Events are the events' versions, in the provider's order.
	Events []MailboxEventVersion
	// Complete: every event asked for is answered. False when the list was
	// cut at MailboxVersionsMax — an event not answered may then still be
	// there.
	Complete bool
}

// MailboxEventVersion is one event's version as Versions answers it: its id,
// the key it carries and its change key, nothing of what it is about.
type MailboxEventVersion struct {
	// EventID is the directory's id of the event.
	EventID string
	// Key is this installation's key the event carries; "" when the read
	// was not asked for the marker (a secondary calendar's view).
	Key string
	// ChangeKey is the event's version: it moves with every write to the
	// event, the person's, the directory's own or the calendar's.
	ChangeKey string
	// Series: the event is an occurrence, an exception or a series master.
	Series bool
}

// MailboxEventState is one event as Events reads it whole (calendar Outlook
// sync design O10.2): what the write-back compares with the copy the
// calendar wrote, and what it takes from an edit.
type MailboxEventState struct {
	// EventID is the directory's id of the event.
	EventID string
	// Key is this installation's key the event carries; "" when it carries
	// none.
	Key string
	// ChangeKey is the event's version as read.
	ChangeKey string
	// Foreign: the event carries another installation's marker, never
	// written back.
	Foreign bool
	// Series: the event is an occurrence, an exception or a series master.
	Series bool
	// Cancelled: the meeting is cancelled.
	Cancelled bool
	// Meeting: the event has attendees — their count alone is decoded,
	// never an address.
	Meeting bool
	// TextBody: the body came as text; one that did not is never taken.
	TextBody bool
	// Event is the subject, the body, the location's display name, the
	// times (all-day dates at midnight UTC, as Delta's), ShowAs and
	// Private (a private or confidential sensitivity).
	Event MailboxEvent
}

// MailboxDelta is one page Delta answers (calendar Outlook sync design O9.4).
type MailboxDelta struct {
	// Changes are the page's changes, in the provider's order.
	Changes []MailboxChange
	// Link is the provider's opaque link: with Done, the delta link to keep
	// for the next read; without, a next link to continue this one with.
	Link string
	// Done: the delta is read to its end, and Link is the one to keep.
	Done bool
}

// MailboxChange is one event of the default calendar as a delta answers it:
// its times and its ShowAs, and nothing of what it is about.
type MailboxChange struct {
	// EventID is the directory's id of the event, or of the occurrence.
	EventID string
	// Removed: the event was deleted or left the window; nothing else of
	// the change is set.
	Removed bool
	// Series: the event is an occurrence or an exception of a recurring
	// series — never a copy, which the calendar always writes single.
	Series bool
	// Cancelled: the meeting is cancelled.
	Cancelled bool
	// AllDay events are StartDate..EndDate, civil dates at midnight UTC,
	// EndDate inclusive.
	AllDay             bool
	StartDate, EndDate time.Time
	// StartsAt and EndsAt are a timed event's instants.
	StartsAt, EndsAt time.Time
	// ShowAs is one of the ShowAs constants, or "" when the directory
	// answered a value the contract does not name.
	ShowAs string
}

// The errors a mailbox provider answers. A caller tests them with errors.Is;
// anything else is a failure to retry, never an answer.
var (
	// ErrMailboxUnavailable: the provider is not available, or is in
	// cleanup and was asked for anything but a delete.
	ErrMailboxUnavailable = errors.New("contracts: the mailbox provider is unavailable")
	// ErrMailboxUnknown: the mailbox is not an account the provider manages
	// (design O3's check), so it wrote nothing.
	ErrMailboxUnknown = errors.New("contracts: the mailbox is not an account the provider manages")
	// ErrMailboxDenied: the directory refused access to the mailbox (outside
	// the provider's permission scope).
	ErrMailboxDenied = errors.New("contracts: access to the mailbox is denied")
	// ErrMailboxGone: the directory has no such user, or the user has no
	// mailbox.
	ErrMailboxGone = errors.New("contracts: the mailbox is gone")
	// ErrCalendarNotOurs: DeleteCalendar was asked to delete a calendar that
	// does not carry the provider's marker — one the person, or another
	// installation, made — so it deleted nothing (design O8).
	ErrCalendarNotOurs = errors.New("contracts: the calendar is not one the provider made")
	// ErrMailboxDeltaExpired: the provider no longer takes Delta's link —
	// the directory forgot the delta's state — so the caller starts it again
	// with "" (design O9.4).
	ErrMailboxDeltaExpired = errors.New("contracts: the mailbox delta link has expired")
)
