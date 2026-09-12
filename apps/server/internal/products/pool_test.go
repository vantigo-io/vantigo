package products

import (
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

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		for _, ctor := range []string{"pgxpool.New(", "pgxpool.NewWithConfig("} {
			if strings.Contains(string(b), ctor) {
				t.Errorf("%s calls %s: the module must use the shared module.Deps.Pool, never open a pool of its own", name, ctor)
			}
		}
	}
}
