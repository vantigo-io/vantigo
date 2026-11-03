package inventory

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations in its own file.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// The transaction marks — Planning's mechanism (internal/planning/server.go),
// copied rather than imported because no module imports another: lockedTxKey
// marks a transaction that may hold row locks (withLockedTx), openTxKey a
// read-only snapshot (withReadTx). Only the accessors of contractscalls.go
// branch on them, refusing to call out of the module from inside one — all
// but lockVariants, the one call made on the transaction itself (I22).
type (
	lockedTxKey struct{}
	openTxKey   struct{}
)

// inLockedTx reports whether ctx is a locked transaction's.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// inOpenTx reports whether ctx is inside any of this module's transactions.
func inOpenTx(ctx context.Context) bool {
	open, _ := ctx.Value(openTxKey{}).(bool)
	return open || inLockedTx(ctx)
}

// withLockedTx runs fn in one READ COMMITTED transaction on the module's
// pool; every write goes through it. fn gets a context marked as locked, the
// transaction itself — for ProductCatalog.LockVariants, which runs on it — and
// the queries bound to it.
//
// No call that takes its own connection or leaves the process is made while
// the transaction holds locks (module-boundaries rule 10's reason): the actor
// and the catalog reads come before it; lockVariants is the one exception,
// because it runs on this transaction's own connection.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(locked, tx, store.New(tx))
	})
}

// withReadTx runs fn in one REPEATABLE READ, read-only transaction: one
// snapshot, no lock, for a read that must see several tables at one instant.
// No call out of the module is made inside fn.
func (s *server) withReadTx(ctx context.Context, fn func(ctx context.Context, q *store.Queries) error) error {
	open := context.WithValue(ctx, openTxKey{}, true)
	return db.WithTx(open, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return fn(open, store.New(tx))
	})
}
