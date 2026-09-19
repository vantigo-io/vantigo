package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
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
// recorded and rendered under. A timeline entry keeps the name it was
// written with forever, so a deleted account leaves a readable history
// rather than a blank one.
const unknownUser = "Unknown user"

// actor is the signed-in caller of one request, as the timeline records
// them: the id for machine use and the display name captured at the moment
// of the change.
type actor struct {
	UserID  uuid.UUID
	Display string
}

// callerAs resolves the request's principal into an actor, naming them
// through contracts.UserDirectory — the only way this module may read
// identity's users. A user the directory does not know is not an error: the
// change still happened, and unknownUser records that it did.
func (s *server) callerAs(ctx context.Context) (actor, error) {
	p, _ := contracts.PrincipalFrom(ctx)
	entry, err := s.usersUser(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("projects: resolve the caller: %w", err)
	}
	a := actor{UserID: p.UserID, Display: unknownUser}
	if entry != nil {
		a.Display = entry.DisplayName
	}
	return a, nil
}

// displayNames resolves ids to display names in one directory call, falling
// back to unknownUser for an id the directory does not know — an assignment
// of a since-deleted account stays readable (design §4.1).
func (s *server) displayNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	names := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	entries, err := s.usersUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: resolve user display names: %w", err)
	}
	for _, e := range entries {
		names[e.ID] = e.DisplayName
	}
	for _, id := range ids {
		if _, ok := names[id]; !ok {
			names[id] = unknownUser
		}
	}
	return names, nil
}

// billingLinesAvailable reports whether this installation has the products
// module enabled, and so whether a project can carry priced billing lines at
// all (D9/D10). Deps.Products is the optional contract: nil when products is
// disabled. It is answered on every project response so the frontend never
// has to ask separately.
func (s *server) billingLinesAvailable() bool { return s.deps.Products != nil }

// lockedTxKey marks a context as belonging to a transaction that holds the
// project's row lock (withProjectLock).
type lockedTxKey struct{}

// inLockedTx reports whether ctx is such a transaction's context. Nothing in
// the module branches on it; the tests' hook does (contracts.go), to prove
// that no cross-module call is ever made while the lock is held.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// lockProject takes the project's row lock and answers the row it returns,
// which is the only row a guarded write then decides against. A project that
// vanished under it maps to errProjectVanished. It is withProjectLock's first
// statement and has no other caller.
func lockProject(ctx context.Context, txq *store.Queries, projectID int32) (store.ProjectsProject, error) {
	locked, err := txq.LockProject(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ProjectsProject{}, errProjectVanished
	}
	if err != nil {
		return store.ProjectsProject{}, fmt.Errorf("projects: lock project: %w", err)
	}
	return locked, nil
}

// withProjectLock runs fn in one transaction whose first statement takes the
// project's row lock — every guarded write in this module goes through it
// (design §3.3, docs/projects.md's "Locking"): the project's own update, a
// billing line's create and change, and all five milestone writes. fn gets the
// locked row, the queries bound to the transaction, and a context marked
// inLockedTx which shadows the handler's own.
//
// A project that vanished under the lock is errProjectVanished, which every
// caller maps to its own 404.
//
// The rule the mark carries: nothing inside fn calls another module. Whatever
// a decision in there needs from a neighbour is read before the transaction
// (the catalog's answer about a variant, the caller's own name), and whatever
// the response needs is read after it. See contracts.go for why, and for the
// check that keeps it true.
func (s *server) withProjectLock(ctx context.Context, projectID int32, fn func(ctx context.Context, txq *store.Queries, locked store.ProjectsProject) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		row, err := lockProject(locked, txq, projectID)
		if err != nil {
			return err
		}
		return fn(locked, txq, row)
	})
}
