package staff

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
type server struct {
	deps module.Deps
	// objects is where documents' bytes live, reached only through the
	// accessors of objects.go; objectsErr is why it could not be built, which
	// every one of them answers instead.
	objects    storage.ObjectStore
	objectsErr error
}

var _ gen.StrictServerInterface = (*server)(nil)

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (staff:access), so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// The transaction marks. They are the Invoices module's mechanism
// (internal/invoices/server.go), copied rather than imported because no
// module imports another: lockedTxKey marks a transaction that may hold row or
// advisory locks (withLockedTx, withPersonLock), openTxKey a read-only
// snapshot (withReadTx). Nothing in the module branches on either except the
// accessors of contractscalls.go, which refuse to call out of the module from
// inside one — and the tests' contract-call hook, which records the attempt.
type (
	lockedTxKey struct{}
	openTxKey   struct{}
)

// inLockedTx reports whether ctx is a locked transaction's.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}

// inOpenTx reports whether ctx is inside any of this module's transactions,
// locked or a read-only snapshot.
func inOpenTx(ctx context.Context) bool {
	open, _ := ctx.Value(openTxKey{}).(bool)
	return open || inLockedTx(ctx)
}

// withLockedTx runs fn in one READ COMMITTED transaction on the module's pool —
// every write here goes through it, or through withPersonLock, which builds on
// it. fn gets a context marked as locked, the transaction itself and its
// queries bound to it.
//
// The rule the mark carries (module-boundaries, rule 10 restated, and design
// §4 Locking): no call that takes its own connection or leaves the process is
// made while a transaction holds locks. The user directory reads through the
// same connection pool, so a transaction that holds row locks and then waits
// for a second connection can starve the pool when enough of them run at once;
// the mail leaves the process, and a slow server under a row lock is the same
// hazard by another route. Whatever a decision inside fn needs from the
// directory is read before the transaction, and a mail is sent after it has
// committed. contractscalls.go is where that is checked.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(locked, tx, store.New(tx))
	})
}

// withReadTx runs fn in one REPEATABLE READ, read-only transaction on the
// module's pool: one snapshot, no lock, for a read that must see several
// tables at one instant. No call out of the module is made inside fn either: a
// snapshot holds a pool connection for as long as it lives, and a directory
// read asked for meanwhile takes a second one, which can starve a small pool
// just as a locked transaction's would — so whatever fn needs from the
// directory is read before it or after it.
func (s *server) withReadTx(ctx context.Context, fn func(ctx context.Context, q *store.Queries) error) error {
	open := context.WithValue(ctx, openTxKey{}, true)
	return db.WithTx(open, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return fn(open, store.New(tx))
	})
}
