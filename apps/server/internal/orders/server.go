package orders

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations in its own file: quotes.go,
// quotesend.go, templates.go and settings.go (the quotes lane); orders.go and
// summary.go (the orders lane). The transaction helpers below are Inventory's
// (internal/inventory/server.go), copied because no module imports another.
type server struct {
	deps module.Deps
	// objects is where the stored PDFs live (pdfdocs.go), scoped to
	// storageScope. No call on it is ever made inside a transaction.
	objects storage.ObjectStore
}

var _ gen.StrictServerInterface = (*server)(nil)

// storageScope is this module's namespace in the object store: a stored PDF's
// key quote/<id>/... is physically orders/quote/<id>/...
const storageScope = "orders"

// newServer builds the module's operations over d. It fails only when the
// configured object store cannot be built: an unset provider is not an
// error, and storing a PDF then fails at the operation, never at startup.
func newServer(d module.Deps) (*server, error) {
	objects, err := objectStoreOf(d)
	if err != nil {
		return nil, err
	}
	return &server{deps: d, objects: objects}, nil
}

// objectStoreOf is the module's scoped object store: the harness's when one
// is set, otherwise the one the configuration names.
func objectStoreOf(d module.Deps) (storage.ObjectStore, error) {
	base := d.ObjectStore
	if base == nil {
		cfg := d.Config
		if cfg == nil {
			cfg = &config.Config{}
		}
		var err error
		if base, err = storage.New(cfg); err != nil {
			return nil, fmt.Errorf("orders: build the object store: %w", err)
		}
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("orders: scope the object store: %w", err)
	}
	return scoped, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// callerID is the signed-in caller of one request; the router has already
// authenticated every operation, so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// The transaction marks: lockedTxKey marks a transaction that may hold row
// locks (withLockedTx), openTxKey a read-only snapshot (withReadTx). Only the
// accessors of contractscalls.go branch on them, refusing to call out of the
// module from inside one — all but lockVariants, the one call made on the
// transaction itself (MB rule 21).
type (
	lockedTxKey       struct{}
	openTxKey         struct{}
	lockedVariantsKey struct{}
)

// lockedVariants are the variants a locked transaction's lockVariants
// answered, by id: what the catalog says of them while their locks are held —
// whether each is goods (O45) — for the writes after it that settle a line's
// delivery method and invoice policy (stock.go).
type lockedVariants struct {
	byID map[int32]contracts.VariantEntry
}

// lockedVariantsOf is the transaction's record of its locked variants, nil
// outside a locked transaction.
func lockedVariantsOf(ctx context.Context) *lockedVariants {
	v, _ := ctx.Value(lockedVariantsKey{}).(*lockedVariants)
	return v
}

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
// transaction itself — for ProductCatalog.LockVariants and events.Publish,
// which run on it — and the queries bound to it.
//
// No call that takes its own connection or leaves the process is made while
// the transaction holds locks: the catalog, the customer, the VAT codes and
// the actor are read before it; lockVariants is the one exception, because it
// runs on this transaction's own connection, and it is the first thing a
// write that touches catalog lines does.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	locked := context.WithValue(context.WithValue(ctx, lockedTxKey{}, true), lockedVariantsKey{}, &lockedVariants{})
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
