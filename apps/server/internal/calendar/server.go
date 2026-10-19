package calendar

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations in its own file:
// meta.go, items.go, appointments.go, occurrences.go, responses.go,
// colleagues.go and users.go.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (calendar:access), so a handler always has
// one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// lockedTxKey marks a context as inside one of this module's transactions —
// Staff's mechanism, copied rather than imported because no module imports
// another. Nothing in the module branches on it except the accessors of
// contractscalls.go, which refuse to call out of the module from inside one,
// and the tests' contract-call hook, which records the attempt.
type lockedTxKey struct{}

// inOpenTx reports whether ctx is inside one of this module's transactions.
func inOpenTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// withLockedTx runs fn in one READ COMMITTED transaction on the module's
// pool — every write here goes through it, under the appointment's row lock
// (GetAppointmentForUpdate) taken first thing inside fn. fn gets a context
// marked as locked and the queries bound to the transaction.
//
// The rule the mark carries (module-boundaries rule 10, calendar design D3):
// no call out of the module is made while the transaction holds a lock. The
// directory reads through the same pool, so a transaction that waits for a
// second connection while holding a row lock can starve the pool under load.
// Whatever a write needs from the directory is read before the transaction
// opens; contractscalls.go is where that is checked.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}

// queries is the module's queries on its pool, for a read outside any
// transaction.
func (s *server) queries() *store.Queries { return store.New(s.deps.Pool) }
