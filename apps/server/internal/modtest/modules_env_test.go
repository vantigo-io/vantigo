package modtest

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestModulesEnv_IncludesEveryModuleUnderTestAndCustomers pins the fix that
// closed a real gap: before it, New left MODULES unset and every harness
// silently rode config's own default (customers, products, energy), which
// happened to include every business module that existed at the time. The
// day a module under test was not on that default (communications, not
// added to it until its own composition task) its harness's requests fell
// through to the platform's /api/ catch-all — a 404 that looks like a
// routing bug, not what it actually was: enabledModules filtering the
// module out before it ever reached its own router. A helper that can
// silently unmount the module it was asked to test is exactly the kind of
// failure this test exists to catch directly, rather than only through
// whichever module's harness tests happen to notice the fallout.
func TestModulesEnv_IncludesEveryModuleUnderTestAndCustomers(t *testing.T) {
	t.Parallel()

	got := modulesEnv([]module.Module{{Name: "communications"}})
	names := strings.Split(got, ",")

	want := map[string]bool{"communications": true, "customers": true}
	if len(names) != len(want) {
		t.Fatalf("modulesEnv = %q, want exactly %d entries: %v", got, len(want), want)
	}
	for _, name := range names {
		if !want[name] {
			t.Errorf("modulesEnv = %q, contains unexpected module %q", got, name)
		}
	}
	if !want["communications"] {
		t.Fatalf("test bug: want map missing communications")
	}
}

// TestModulesEnv_ExcludesModulesNotUnderTest proves the other half: a
// module never passed to modulesEnv is never included, so a harness testing
// one module cannot accidentally also mount a sibling it never asked for
// (and whose behaviour the test's own contract recorder is not equipped to
// validate).
func TestModulesEnv_ExcludesModulesNotUnderTest(t *testing.T) {
	t.Parallel()

	got := modulesEnv([]module.Module{{Name: "energy"}})
	names := strings.Split(got, ",")

	for _, excluded := range []string{"products", "communications"} {
		for _, name := range names {
			if name == excluded {
				t.Errorf("modulesEnv(energy) = %q, must not include %q (not under test)", got, excluded)
			}
		}
	}
	// The one always-included exception, documented on modulesEnv itself:
	// config.go's modules() rejects "energy" without "customers".
	if !strings.Contains(got, "customers") {
		t.Errorf("modulesEnv(energy) = %q, want it to include customers (energy depends on it)", got)
	}
}

// TestModulesEnv_DoesNotDuplicateCustomers proves customers is included
// exactly once whether or not it is itself the module under test, and that
// repeating a module across more than one WithModule call does not
// duplicate it either — both would otherwise let a malformed comma value
// like "customers,customers" reach config.Load.
func TestModulesEnv_DoesNotDuplicateCustomers(t *testing.T) {
	t.Parallel()

	got := modulesEnv([]module.Module{{Name: "customers"}})
	if got != "customers" {
		t.Errorf("modulesEnv(customers) = %q, want exactly %q", got, "customers")
	}

	got = modulesEnv([]module.Module{{Name: "products"}, {Name: "products"}})
	if got != "customers,products" {
		t.Errorf("modulesEnv(products, products) = %q, want exactly %q", got, "customers,products")
	}
}
