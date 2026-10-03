package invoices

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
type server struct {
	deps module.Deps
	// objects is where issued documents' PDFs live (pdfstore.go), scoped to
	// storageScope. No call on it is ever made inside withLockedTx.
	objects storage.ObjectStore
	// storageConfigured is whether objects can store anything at all: an
	// object store a test harness set, or a configured provider. Without one
	// the store answers storage.ErrNotConfigured to everything, and issuing is
	// refused before any number is allocated (D6).
	storageConfigured bool
}

var _ gen.StrictServerInterface = (*server)(nil)

// storageScope is this module's namespace in the object store: a document's
// key documents/<id>/<number>-<sha256>.pdf is physically
// invoices/documents/... (docs/src/content/docs/en/admin/object-storage.md).
const storageScope = "invoices"

// newServer builds the module's operations over d. It fails only when the
// configured object store cannot be built. An unset provider is not an error:
// the process starts, and issuing and downloading fail closed with a 503 at the
// operation (docs/src/content/docs/en/admin/object-storage.md), never at startup.
func newServer(d module.Deps) (*server, error) {
	if d.ObjectStore != nil {
		return &server{deps: d, objects: d.ObjectStore, storageConfigured: true}, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("invoices: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("invoices: scope the object store: %w", err)
	}
	return &server{deps: d, objects: scoped, storageConfigured: cfg.StorageProvider != ""}, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// lockedTxKey marks a context as belonging to a transaction that may hold row
// locks (withLockedTx).
type lockedTxKey struct{}

// withLockedTx runs fn in one READ COMMITTED transaction on the module's pool —
// every write here that takes a row lock goes through it. fn gets a context
// marked as locked and its queries bound to the transaction.
//
// The rule the mark carries: nothing inside fn calls another module or the
// object store (docs/src/content/docs/en/contributing/module-boundaries.md, docs/src/content/docs/en/reference/expenses.md). Whatever a
// decision inside fn needs from the customer directory is read before the
// transaction, and a PDF is stored after it has committed.
func (s *server) withLockedTx(ctx context.Context, fn func(ctx context.Context, txq *store.Queries) error) error {
	locked := context.WithValue(ctx, lockedTxKey{}, true)
	return db.WithTx(locked, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(locked, store.New(tx))
	})
}

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (invoices:access), so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// inLockedTx reports whether ctx is one withLockedTx marked. Nothing in the
// module branches on it; the tests' contract-call hook does
// (contractscalls.go), to prove that no call into another module or the
// object store is ever made while locks are held.
func inLockedTx(ctx context.Context) bool {
	locked, _ := ctx.Value(lockedTxKey{}).(bool)
	return locked
}
