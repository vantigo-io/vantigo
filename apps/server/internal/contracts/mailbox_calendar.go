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

	// EnsureCalendar answers the id of the calendar named name that the
	// provider made in mailbox: known when it still exists, else a new one.
	// Refused under MailboxCleanup.
	EnsureCalendar(ctx context.Context, mailbox, known, name string) (string, error)
	// DeleteCalendar deletes the calendar and every event in it. Idempotent:
	// a calendar already gone answers nil. Allowed under MailboxCleanup, so a
	// person's copies can still be removed.
	DeleteCalendar(ctx context.Context, mailbox, calendarID string) error

	// Apply runs ops against the calendar — calendarID "" is the mailbox's
	// default calendar — and answers one MailboxResult per op, in order. A
	// refusal of one op is its result's Err, carrying the sentinels below;
	// the returned error is the whole call failing (ErrMailboxUnavailable,
	// ErrMailboxUnknown, ErrMailboxGone, ErrMailboxDenied, or a failure to
	// retry), and then no result is answered. Under MailboxCleanup only
	// deletes are taken: every other op answers ErrMailboxUnavailable.
	Apply(ctx context.Context, mailbox, calendarID string, ops []MailboxOp) ([]MailboxResult, error)

	// Find answers the events carrying a key — in a secondary calendar,
	// every event in it — bounded at 5000: what the calendar reconciles its
	// map against.
	Find(ctx context.Context, mailbox, calendarID string) ([]MailboxEventRef, error)
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
// EventID and Event; a delete EventID.
type MailboxOp struct {
	Kind    string
	EventID string
	// Key is written on create as the event's marker and never changed: the
	// calendar's own key for the item ("appointment:42").
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

// MailboxEventRef is one event Find answers: its id and the key it carries.
type MailboxEventRef struct {
	EventID, Key string
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
)
