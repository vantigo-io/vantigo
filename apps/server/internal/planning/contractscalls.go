package planning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach outside its own schema (design
// S8, S9, S25): Staff's register (deps.Workforce, contracts.Workforce — the
// people of a window, their capacity, the caller's own person and the
// competences held), Projects' directory (deps.Projects,
// contracts.ProjectDirectory — the listing by status, consumer-gated, and the
// entries), Customers' names (deps.Directory, contracts.CustomerDirectory,
// which Projects requires) and identity's users (deps.Users,
// contracts.UserDirectory — the actor's name, the person's mail, the
// permission holders). Every call is made through one of the thin accessors
// below and through nowhere else, so "what does Planning ask of its
// neighbours, and when" has one place to read the answer and one place to
// check it from; the business calendar's zone is read in authorize.go
// through the same check.
//
// What is checked is the lock rule (S23, module-boundaries rule 10 restated):
// no call that takes its own connection or leaves the process is made inside
// one of this module's transactions — a locked one, or a read-only snapshot,
// which holds a pool connection just the same. Every accessor reports its call
// to contractCallHook, which the tests install to record it with whether it
// was made inside one, and every accessor refuses a call made inside one with
// errCallInsideTransaction: the mistake is a programming error, and failing
// the request loudly is better than holding a connection or a lock across a
// directory read or a mail server. A contract not composed is
// errDependencyUnavailable, never a panic (S25).

// errCallInsideTransaction is every accessor's answer to a call made inside
// one of this module's transactions. Nothing answers it to a client except as
// the 500 any other fault is.
var errCallInsideTransaction = errors.New("planning: a call out of the module was made inside one of its transactions")

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

// workforcePeople is the people in employment on today or a later day of
// from..to as Staff answers them to the caller (S8): nobody to a caller
// without staff:view, and contracts.ErrWindowOutOfBounds for a window outside
// the bound.
func (s *server) workforcePeople(ctx context.Context, from, to time.Time) ([]contracts.PersonEntry, error) {
	if err := noteContractCall(ctx, "Workforce.People"); err != nil {
		return nil, err
	}
	if s.deps.Workforce == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Workforce.People(ctx, from, to)
}

// workforceCapacity is each person's hours a week in force over from..to and
// their days away (S8). It answers nothing for an empty batch without asking:
// a cross-module call that can only answer nothing is a call worth not
// making.
func (s *server) workforceCapacity(ctx context.Context, from, to time.Time, personIDs []int64) (map[int64]contracts.PersonCapacity, error) {
	if len(personIDs) == 0 {
		return map[int64]contracts.PersonCapacity{}, nil
	}
	if err := noteContractCall(ctx, "Workforce.Capacity"); err != nil {
		return nil, err
	}
	if s.deps.Workforce == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Workforce.Capacity(ctx, from, to, personIDs)
}

// workforceSelf is the caller's own person under Staff's Self rule, or nil
// (S8, S19). Handlers read it through callerSelf, once per request.
func (s *server) workforceSelf(ctx context.Context) (*contracts.PersonEntry, error) {
	if err := noteContractCall(ctx, "Workforce.Self"); err != nil {
		return nil, err
	}
	if s.deps.Workforce == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Workforce.Self(ctx)
}

// workforceHeld is the competences each person holds valid on on (S10's
// competence filter, S12's expiring certificates). It answers nothing for an
// empty batch without asking.
func (s *server) workforceHeld(ctx context.Context, on time.Time, personIDs []int64) (map[int64][]contracts.HeldCompetence, error) {
	if len(personIDs) == 0 {
		return map[int64][]contracts.HeldCompetence{}, nil
	}
	if err := noteContractCall(ctx, "Workforce.HeldCompetences"); err != nil {
		return nil, err
	}
	if s.deps.Workforce == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Workforce.HeldCompetences(ctx, on, personIDs)
}

// projectsByStatus is every project in any of statuses, by code, at most
// contracts.MaxDirectoryProjects (S9). The read is consumer-gated: Projects
// performs no authorization, and planning:view — the operation's own rule —
// is what decides who sees the list.
func (s *server) projectsByStatus(ctx context.Context, statuses []string) ([]contracts.ProjectEntry, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Projects.ProjectsByStatus"); err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Projects.ProjectsByStatus(ctx, statuses)
}

// projectEntry is one project, nil when the directory does not resolve it.
func (s *server) projectEntry(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	if err := noteContractCall(ctx, "Projects.Project"); err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.Projects.Project(ctx, id)
}

// projectEntries is the projects of ids by id; an id the directory does not
// resolve is absent from the map, which a response reads by its id (rule 4).
// It answers nothing for an empty batch without asking.
func (s *server) projectEntries(ctx context.Context, ids []int32) (map[int32]contracts.ProjectEntry, error) {
	out := map[int32]contracts.ProjectEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Projects.Projects"); err != nil {
		return nil, err
	}
	if s.deps.Projects == nil {
		return nil, errDependencyUnavailable
	}
	entries, err := s.deps.Projects.Projects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("planning: read projects: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e
	}
	return out, nil
}

// customerNames is the name of each customer of ids, by id, through the
// customer directory (S9); an id it does not resolve is absent from the map.
// It answers nothing for an empty batch without asking.
func (s *server) customerNames(ctx context.Context, ids []int32) (map[int32]string, error) {
	out := map[int32]string{}
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Directory.Customers"); err != nil {
		return nil, err
	}
	if s.deps.Directory == nil {
		return nil, errDependencyUnavailable
	}
	entries, err := s.deps.Directory.Customers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("planning: read customers: %w", err)
	}
	for _, e := range entries {
		out[e.ID] = e.Name
	}
	return out, nil
}

// usersUser is identity's user directory, the only way this module may read a
// user: the actor of an event (actorFor).
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if err := noteContractCall(ctx, "Users.User"); err != nil {
		return nil, err
	}
	return s.deps.Users.User(ctx, id)
}

// usersNotify mails one user through identity, which holds the address and
// picks the language (S19): the person whose row was confirmed, changed,
// deleted or set back to tentative — after the commit, never inside the
// transaction.
func (s *server) usersNotify(ctx context.Context, userID uuid.UUID, n contracts.Notice) error {
	if err := noteContractCall(ctx, "Users.Notify"); err != nil {
		return err
	}
	return s.deps.Users.Notify(ctx, userID, n)
}

// usersPermissionHolders is the active users holding key — the planners a new
// wish is mailed to (delivery C).
func (s *server) usersPermissionHolders(ctx context.Context, key string) ([]contracts.Holder, error) {
	if err := noteContractCall(ctx, "Users.PermissionHolders"); err != nil {
		return nil, err
	}
	return s.deps.Users.PermissionHolders(ctx, key)
}
