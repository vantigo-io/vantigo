package products

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Ported from Integration/DatabaseContextRegistrationTests.cs.
// ProductsContextUsesTheRegisteredSharedDataSource — re-expressed for Go's
// connection-pooling model exactly as products inventory §6 requires
// ("re-express as 'one connection pool is shared', not tenancy"). .NET
// asserted that ProductsDbContext resolves the process's single registered
// NpgsqlDataSource rather than building a pool of its own per DbContext
// (Assert.Same on the data source). The Go fact with the same content is
// that the module's data layer uses the one *pgxpool.Pool the composition
// root opened and Compose handed it through module.Deps, and never opens
// one itself.
//
// Both halves are asserted, because either alone is weak: pointer identity
// proves newServer keeps the pool it was given, and the source scan proves
// no code path in the package quietly constructs a second pool that
// identity check would never see.
func TestNewServer_UsesTheSharedConnectionPool(t *testing.T) {
	t.Parallel()

	// Never dialled — only its identity is under test, so a zero-value pool
	// is enough and needs no database.
	shared := new(pgxpool.Pool)
	s := newServer(module.Deps{Pool: shared})
	if s.deps.Pool != shared {
		t.Error("newServer holds a different *pgxpool.Pool than module.Deps carried; every query must run on the process's shared pool")
	}
}

// TestPackage_OpensNoConnectionPoolOfItsOwn is the other half of
// DatabaseContextRegistrationTests' fact: the module must consume the
// shared pool, not construct one. A pool built inside the module would be a
// second set of connections against the same database, invisible to the
// composition root's lifecycle and to Close.
func TestPackage_OpensNoConnectionPoolOfItsOwn(t *testing.T) {
	t.Parallel()

	// Walks the whole module subtree, not just this directory: the generated
	// query layer in products/store and the generated server in products/gen
	// are as capable of opening a pool as the hand-written files beside this
	// one, and scanning "." alone would miss them.
	var scanned int
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(filepath.Clean(path))
		if readErr != nil {
			return readErr
		}
		scanned++
		for _, ctor := range []string{"pgxpool.New(", "pgxpool.NewWithConfig("} {
			if strings.Contains(string(b), ctor) {
				t.Errorf("%s calls %s: the module must use the shared module.Deps.Pool, never open a pool of its own", path, ctor)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Guards the guard: a walk that silently matched nothing would pass
	// vacuously forever.
	if scanned < 10 {
		t.Errorf("scanned only %d non-test .go files; the walk is not reaching the module's sources", scanned)
	}
}
