package calendar

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach into its neighbours: the
// calendar sources (deps.CalendarSources, calendar design D1), the business
// calendar (deps.Calendar), the user directory (deps.Users) and, when wired,
// the customer directory (deps.Directory) and the project directory
// (deps.Projects). Every call is made through one of the thin accessors below
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
