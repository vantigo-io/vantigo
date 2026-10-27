package calendar

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file is the whole of this module's reach into its neighbours: the
// calendar sources (deps.CalendarSources, calendar design D1), the business
// calendar (deps.Calendar), the user directory (deps.Users), identity's
// UserHolds for a read with no request behind it (deps.Access) and, when wired,
// the customer directory (deps.Directory), the project directory
// (deps.Projects), the workforce register's DirectoryAccount (deps.Workforce)
// and the mailbox provider (deps.MailboxCalendar, calendar Outlook sync design
// O4). Every call is made through one of the thin accessors below
// and through nowhere else, so "what does Calendar ask of another module, and
// when" has one place to read the answer and one place to check it from.
//
// What is checked is the lock rule (module-boundaries rule 10, calendar design
// D3): no call out of the module is made inside one of its transactions.
// These are in-process calls that read through the same pool, so a
// transaction holding a row lock that waits for one of them can starve the
// pool. Every accessor reports its call to contractCallHook, which the tests
// install to record it with whether it was made inside one, and refuses a
// call made inside one with errCallInsideTransaction — Staff's rule: the
// mistake is a programming error, and failing the request loudly is better
// than holding a lock across a directory read.
//
// The mailbox provider is the sixth sanctioned cross-module write and the
// one that reaches the network (rule 13's discipline): its accessors are the
// same guard, and the Outlook sync's worker calls them only between its short
// transactions, never holding one open across a call.

// errCallInsideTransaction is every accessor's answer to a call made inside
// one of this module's transactions. Nothing answers it to a client except as
// the 500 any other fault is.
var errCallInsideTransaction = errors.New("calendar: a call out of the module was made inside one of its transactions")

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs,
// by this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call out of the module, and answers whether it
// may go ahead: never from inside one of the module's transactions.
func noteContractCall(ctx context.Context, method string) error {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
	if inOpenTx(ctx) {
		return errCallInsideTransaction
	}
	return nil
}

// sources is every calendar source an enabled module provides, in module
// order (calendar design D1). The slot is read here and nowhere else.
func (s *server) sources(ctx context.Context) ([]contracts.CalendarSource, error) {
	if err := noteContractCall(ctx, "CalendarSources"); err != nil {
		return nil, err
	}
	return s.deps.CalendarSources, nil
}

// sourceItems is one source's items for q, asked with the request's context
// — contracts.HasPermission reads the request from it, so the provider's
// gate sees the caller — and never inside a transaction of this module's.
//
// A provider trusts q.UserID: it answers that user's rows under the gate of
// the principal in ctx, and checks no further that the two are one person. So
// this module never asks about anyone but the principal — a colleague's
// calendar asks no source at all (calendar design D6) — and a query naming
// someone else is refused here, answered as nothing and logged, rather than
// handed to a provider that would answer it.
func (s *server) sourceItems(ctx context.Context, src contracts.CalendarSource, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	if err := noteContractCall(ctx, "CalendarSource.CalendarItems"); err != nil {
		return nil, err
	}
	if p, ok := contracts.PrincipalFrom(ctx); !ok || p.UserID != q.UserID {
		s.deps.Logger.ErrorContext(ctx, "calendar: a calendar source was asked about someone other than the caller; nothing asked",
			"module", sourceModule(src))
		return nil, nil
	}
	return src.CalendarItems(ctx, q)
}

// accessUserHolds is identity's answer for a read on userID's behalf with no
// request behind it (calendar feed design D5): whether the person may act at
// all — not disabled, locked out, pending or deactivated by the directory —
// and holds every key. The feed asks it before it answers a token, and the
// subscriptions worker before it fetches for a person, so neither serves
// someone who has left. A failure to answer is an error, never a false.
func (s *server) accessUserHolds(ctx context.Context, userID uuid.UUID, keys ...string) (bool, error) {
	if err := noteContractCall(ctx, "Access.UserHolds"); err != nil {
		return false, err
	}
	return s.deps.Access.UserHolds(ctx, userID, keys...)
}

// calendarZone and calendarToday are the business calendar: the workspace's
// zone, and today in it. Each reads a pool connection of its own.
func (s *server) calendarZone(ctx context.Context) (*time.Location, error) {
	if err := noteContractCall(ctx, "Calendar.Zone"); err != nil {
		return nil, err
	}
	return s.deps.Calendar.Zone(ctx)
}

func (s *server) calendarToday(ctx context.Context, now time.Time) (time.Time, error) {
	if err := noteContractCall(ctx, "Calendar.Today"); err != nil {
		return time.Time{}, err
	}
	return s.deps.Calendar.Today(ctx, now)
}

// usersUser, usersBatch and usersSearch are identity's user directory, the
// only way this module may read a user: a colleague whose calendar is asked
// for, the names on an appointment, the pickers.
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	return s.deps.Users.User(ctx, id)
}

// usersBatch answers nothing for an empty batch without asking anybody: a
// cross-module call that can only answer nothing is a call worth not making.
func (s *server) usersBatch(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Users.Users"); err != nil {
		return nil, err
	}
	return s.deps.Users.Users(ctx, ids)
}

func (s *server) usersSearch(ctx context.Context, query string, limit int) ([]contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.SearchUsers"); err != nil {
		return nil, err
	}
	return s.deps.Users.SearchUsers(ctx, query, limit)
}

// userActive reports whether id is an active user in the directory — a
// colleague whose calendar may be shown.
func (s *server) userActive(ctx context.Context, id uuid.UUID) (bool, error) {
	u, err := s.usersUser(ctx, id)
	if err != nil {
		return false, err
	}
	return u != nil && u.Active, nil
}

// directoryCustomer is the customer an appointment was made from, nil when
// Customers is off (deps.Directory nil) or knows no such id: the link is a
// courtesy and is never refused for being unresolvable (calendar design D2).
func (s *server) directoryCustomer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	if err := noteContractCall(ctx, "Directory.Customer"); err != nil {
		return nil, err
	}
	if s.deps.Directory == nil {
		return nil, nil
	}
	return s.deps.Directory.Customer(ctx, id)
}

// directoryContact is one of a customer's contacts — an attendee of the
// contact kind, named and addressed at read and at send time (calendar
// invitations design D1) — nil when Customers is off or answers nothing for
// the id, an erased contact included.
func (s *server) directoryContact(ctx context.Context, id int32) (*contracts.ContactEntry, error) {
	if err := noteContractCall(ctx, "Directory.Contact"); err != nil {
		return nil, err
	}
	if s.deps.Directory == nil {
		return nil, nil
	}
	return s.deps.Directory.Contact(ctx, id)
}

// directoryContactsFor is one customer's contacts with an address, for the
// attendee picker (calendar invitations design D2); the caller has already
// checked that Customers is on.
func (s *server) directoryContactsFor(ctx context.Context, customerID int32) ([]contracts.ContactEntry, error) {
	if err := noteContractCall(ctx, "Directory.ContactsForCustomer"); err != nil {
		return nil, err
	}
	return s.deps.Directory.ContactsForCustomer(ctx, customerID)
}

// usersNotify mails a colleague through identity (calendar invitations
// design D4), without this module ever holding their address.
func (s *server) usersNotify(ctx context.Context, id uuid.UUID, n contracts.Notice) error {
	if err := noteContractCall(ctx, "Users.Notify"); err != nil {
		return err
	}
	return s.deps.Users.Notify(ctx, id, n)
}

// smtpSend hands one invitation to the installation's SMTP server:
// Deps.SMTPSend when a harness set one, otherwise mail.SendOutbound, the
// platform's guarded path — Staff's and Invoices' accessor. A caller asks
// mailAvailable first.
func (s *server) smtpSend(ctx context.Context, cfg config.MailConfig, out mail.Outbound) error {
	if err := noteContractCall(ctx, "SMTPSend"); err != nil {
		return err
	}
	if s.deps.SMTPSend != nil {
		return s.deps.SMTPSend(ctx, cfg, out)
	}
	return mail.SendOutbound(ctx, cfg, out)
}

// mailAvailable is whether this installation sends invitations at all: its
// mail driver is smtp (calendar invitations design D3). Otherwise outside
// attendees are stored and left not sent, and GET /meta says so.
func (s *server) mailAvailable() bool {
	return s.deps.Config != nil && s.deps.Config.Mail.Driver == "smtp"
}

// projectsRole is the role userID holds on project id, "" for none or when
// Projects is off.
func (s *server) projectsRole(ctx context.Context, id int32, userID uuid.UUID) (string, error) {
	if err := noteContractCall(ctx, "Projects.Role"); err != nil {
		return "", err
	}
	if s.deps.Projects == nil {
		return "", nil
	}
	return s.deps.Projects.Role(ctx, id, userID)
}

// projectsProject is the project an appointment was made from, nil when
// Projects is off (deps.Projects nil) or knows no such id.
func (s *server) projectsProject(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	if err := noteContractCall(ctx, "Projects.Project"); err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return nil, nil
	}
	return s.deps.Projects.Project(ctx, id)
}

// mailboxCalendar is the mailbox provider, nil when no enabled module fills
// the slot (Entra off): the Outlook sync is then no_provider. Reading the
// slot calls nobody, so it is not a reported call.
func (s *server) mailboxCalendar() contracts.MailboxCalendar { return s.deps.MailboxCalendar }

// mailboxStatus is whether the provider may write now (design O4); no
// network. Without a provider it is unavailable.
func (s *server) mailboxStatus(ctx context.Context) (contracts.MailboxStatus, error) {
	if err := noteContractCall(ctx, "MailboxCalendar.Status"); err != nil {
		return contracts.MailboxStatus{}, err
	}
	if s.deps.MailboxCalendar == nil {
		return contracts.MailboxStatus{State: contracts.MailboxUnavailable}, nil
	}
	return s.deps.MailboxCalendar.Status(ctx)
}

// mailboxEnsure is the id of the calendar named name the provider made in
// mailbox: known when it still exists, else a new one.
func (s *server) mailboxEnsure(ctx context.Context, mailbox, known, name string) (string, error) {
	if err := noteContractCall(ctx, "MailboxCalendar.EnsureCalendar"); err != nil {
		return "", err
	}
	if s.deps.MailboxCalendar == nil {
		return "", contracts.ErrMailboxUnavailable
	}
	return s.deps.MailboxCalendar.EnsureCalendar(ctx, mailbox, known, name)
}

// mailboxDeleteCalendar deletes the provider's calendar and every event in
// it; one already gone is done.
func (s *server) mailboxDeleteCalendar(ctx context.Context, mailbox, calendarID string) error {
	if err := noteContractCall(ctx, "MailboxCalendar.DeleteCalendar"); err != nil {
		return err
	}
	if s.deps.MailboxCalendar == nil {
		return contracts.ErrMailboxUnavailable
	}
	return s.deps.MailboxCalendar.DeleteCalendar(ctx, mailbox, calendarID)
}

// mailboxApply runs ops against the mailbox's calendar ("" the default one)
// and answers one result per op, in order.
func (s *server) mailboxApply(ctx context.Context, mailbox, calendarID string, ops []contracts.MailboxOp) ([]contracts.MailboxResult, error) {
	if err := noteContractCall(ctx, "MailboxCalendar.Apply"); err != nil {
		return nil, err
	}
	if s.deps.MailboxCalendar == nil {
		return nil, contracts.ErrMailboxUnavailable
	}
	return s.deps.MailboxCalendar.Apply(ctx, mailbox, calendarID, ops)
}

// mailboxFind is the events of the mailbox's calendar carrying this
// installation's marker, starting at or after from unless it is zero.
func (s *server) mailboxFind(ctx context.Context, mailbox, calendarID string, from time.Time) ([]contracts.MailboxEventRef, error) {
	if err := noteContractCall(ctx, "MailboxCalendar.Find"); err != nil {
		return nil, err
	}
	if s.deps.MailboxCalendar == nil {
		return nil, contracts.ErrMailboxUnavailable
	}
	return s.deps.MailboxCalendar.Find(ctx, mailbox, calendarID, from)
}

// workforceDirectoryAccount is the principal's own account in an external
// directory (design O3) — the request's caller, or the person contracts.AsUser
// names for the worker — nil when Staff is off or links them to none.
func (s *server) workforceDirectoryAccount(ctx context.Context) (*contracts.DirectoryAccount, error) {
	if err := noteContractCall(ctx, "Workforce.DirectoryAccount"); err != nil {
		return nil, err
	}
	if s.deps.Workforce == nil {
		return nil, nil
	}
	return s.deps.Workforce.DirectoryAccount(ctx)
}
