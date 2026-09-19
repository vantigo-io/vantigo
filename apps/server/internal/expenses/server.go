package expenses

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
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

// lockedTxKey marks a context as belonging to a transaction that may hold row
// or advisory locks (withLockedTx).
type lockedTxKey struct{}

// inLockedTx reports whether ctx is such a transaction's context. Nothing in
// the module branches on it; the tests' contract-call hook does
// (contractscalls.go), to prove that no call into another module is ever made
// while locks are held.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// withLockedTx runs fn in one transaction on the module's pool — every write
// here that takes a row or advisory lock goes through it. fn gets a context
// marked inLockedTx, which shadows the handler's own, and its queries bound to
// the transaction.
//
// The rule the mark carries: nothing inside fn calls another module. The
// project and user directories read through the same pool, so a transaction
// that holds locks and then waits for a second connection can starve the pool
// when enough of them run at once — every connection held by a transaction
// waiting for one more. Whatever a decision inside fn needs from a neighbour is
// read before the transaction, and what a response needs, after it. See
// contractscalls.go for the check that keeps it true.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}
