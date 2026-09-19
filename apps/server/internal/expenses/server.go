package expenses

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
type server struct {
	deps module.Deps
	// objects is where receipts' bytes live (attachments.go). Nothing else of
	// this module touches the object store, and no call on it is ever made
	// inside withLockedTx.
	objects storage.ObjectStore
}

var _ gen.StrictServerInterface = (*server)(nil)

// storageScope is this module's namespace in the object store. Every key the
// module writes is relative to it, and the scoped store refuses to be handed
// its own prefix, so no key of this module can ever reach another's objects.
const storageScope = "expenses"

// newServer builds the module's operations over d. It fails only when the
// configured object store cannot be built — StorageProvider "fs" with a root
// that cannot be opened. An unset provider is not an error: the process starts
// and every storage operation fails closed with storage.ErrNotConfigured
// (docs/storage.md), which this module answers as a 503.
func newServer(d module.Deps) (*server, error) {
	objects, err := moduleObjectStore(d)
	if err != nil {
		return nil, err
	}
	return &server{deps: d, objects: objects}, nil
}

// moduleObjectStore is this module's object store: d.ObjectStore when a test
// harness set one (module.Deps.ObjectStore), otherwise this module's own
// scoped wrapper over internal/storage.New. d.Config is nil in the white-box
// tests that build a bare module.Deps to probe routing rather than storage; an
// empty config stands in for it, which is the unconfigured, fail-closed store.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	if d.ObjectStore != nil {
		return d.ObjectStore, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("expenses: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("expenses: scope the object store: %w", err)
	}
	return scoped, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (expenses:access), so a handler always has
// one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
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
