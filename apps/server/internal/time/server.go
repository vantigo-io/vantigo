package timetracking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }

// unknownUser is the display name a user the directory cannot resolve is
// rendered under, so an entry of a since-deleted account stays readable
// rather than blank.
const unknownUser = "Unknown user"

// unknownProject is the name a project the directory cannot resolve is
// rendered under. Projects cannot be deleted, so this only ever shows for a
// directory that has lost a row it once had; an entry still renders rather
// than failing the whole read.
const unknownProject = "Unknown project"

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (time:access), so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// today is the calendar day of Deps.Clock in UTC — the day "the current
// week" is read from wherever the module needs one (the people overview, the
// dashboard stats), as entry dates are UTC calendar days.
func (s *server) today() time.Time {
	now := s.deps.Clock().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// userEntries resolves ids through contracts.UserDirectory — the only way
// this module may read identity's users — in one call, answering an entry
// for every id: one the directory does not know gets unknownUser, inactive,
// so an entry survives the account it belongs to.
func (s *server) userEntries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]contracts.UserEntry, error) {
	entries := make(map[uuid.UUID]contracts.UserEntry, len(ids))
	if len(ids) == 0 {
		return entries, nil
	}
	found, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: resolve user display names: %w", err)
	}
	for _, e := range found {
		entries[e.ID] = e
	}
	for _, id := range ids {
		if _, ok := entries[id]; !ok {
			entries[id] = contracts.UserEntry{ID: id, DisplayName: unknownUser}
		}
	}
	return entries, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// lockedTxKey marks a context as belonging to a transaction that may hold row
// or advisory locks (withLockedTx).
type lockedTxKey struct{}

// inLockedTx reports whether ctx is a withLockedTx transaction's context.
// Nothing in the module branches on it; the tests' fake directories do, to
// prove that no directory call is ever made while locks are held.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// withLockedTx runs fn in one transaction on the module's pool — every write
// here that takes a row or advisory lock goes through it. fn gets a context
// marked inLockedTx, which shadows the handler's own, and its queries bound
// to the transaction.
//
// The rule it carries: nothing inside fn calls another module's directory.
// The project and user directories read through the same pool, so a
// transaction that holds locks and then waits for a second connection can
// starve the pool when enough of them run at once — every connection held by
// a transaction waiting for one more. Whatever a decision inside fn needs
// from a directory is read before the transaction (for access, the caller's
// role cache: warmRoles), and what a response needs, after it.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}
