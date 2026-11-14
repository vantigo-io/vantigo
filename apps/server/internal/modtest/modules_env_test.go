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

// TestModulesEnv_ListsAStubbedModuleAsEnabled proves a module a harness
// stands in for with a fake contract (WithProjects) counts as enabled: time
// is only valid configuration beside projects (config.go's "time requires
// projects"), and a harness testing time composes a fake
// contracts.ProjectDirectory rather than projects itself, so without this
// config.Load would refuse the harness before Compose ever ran.
func TestModulesEnv_ListsAStubbedModuleAsEnabled(t *testing.T) {
	t.Parallel()

	got := modulesEnv([]module.Module{{Name: "time"}}, "projects")
	if got != "customers,projects,time" {
		t.Errorf("modulesEnv(time; stubbed projects) = %q, want exactly %q", got, "customers,projects,time")
	}

	got = modulesEnv([]module.Module{{Name: "projects"}}, "projects")
	if got != "customers,projects" {
		t.Errorf("modulesEnv(projects; stubbed projects) = %q, want exactly %q", got, "customers,projects")
	}

	// planning is valid only beside projects and staff (config.go), and its
	// harness stands in for both with fakes (WithProjects, WithWorkforce).
	got = modulesEnv([]module.Module{{Name: "planning"}}, "projects", "staff")
	if got != "customers,planning,projects,staff" {
		t.Errorf("modulesEnv(planning; stubbed projects, staff) = %q, want exactly %q", got, "customers,planning,projects,staff")
	}
}

// TestModulesEnv_ListsStaffBesideEntra proves a harness composing entra lists
// staff as enabled: config.go refuses "entra" without "staff", and entra's
// own tests compose the provider without Staff, so without it config.Load
// would refuse the harness before Compose ever ran. Staff is listed, not
// mounted: enabledModules filters only the modules actually composed.
func TestModulesEnv_ListsStaffBesideEntra(t *testing.T) {
	t.Parallel()

	if got := modulesEnv([]module.Module{{Name: "entra"}}); got != "customers,entra,staff" {
		t.Errorf("modulesEnv(entra) = %q, want exactly %q", got, "customers,entra,staff")
	}
	if got := modulesEnv([]module.Module{{Name: "staff"}, {Name: "entra"}}); got != "customers,entra,staff" {
		t.Errorf("modulesEnv(staff, entra) = %q, want exactly %q", got, "customers,entra,staff")
	}
	if got := modulesEnv([]module.Module{{Name: "staff"}}); got != "customers,staff" {
		t.Errorf("modulesEnv(staff) = %q, want exactly %q (entra is never added)", got, "customers,staff")
	}
}

// TestModulesEnv_ListsProductsBesideInventory proves a harness composing
// inventory lists products as enabled: config.go refuses "inventory" without
// "products", and inventory's own tests stand in a fake catalog
// (WithProducts) rather than compose products.
func TestModulesEnv_ListsProductsBesideInventory(t *testing.T) {
	t.Parallel()

	if got := modulesEnv([]module.Module{{Name: "inventory"}}); got != "customers,inventory,products" {
		t.Errorf("modulesEnv(inventory) = %q, want exactly %q", got, "customers,inventory,products")
	}
}

// TestModulesEnv_ListsProductsAndInvoicesBesideOrders proves a harness
// composing orders lists products and invoices as enabled: config.go refuses
// "orders" without either, and orders' own tests stand in fakes of the
// catalog and the sales tax rather than compose them.
func TestModulesEnv_ListsProductsAndInvoicesBesideOrders(t *testing.T) {
	t.Parallel()

	if got := modulesEnv([]module.Module{{Name: "orders"}}); got != "customers,invoices,orders,products" {
		t.Errorf("modulesEnv(orders) = %q, want exactly %q", got, "customers,invoices,orders,products")
	}
}

// TestModulesEnv_MetaBringsCommunications: config.go rejects "meta" without
// "communications" (Messenger design N9), and meta's own tests compose the
// provider alone, so the harness lists communications as enabled beside it.
func TestModulesEnv_MetaBringsCommunications(t *testing.T) {
	t.Parallel()

	got := strings.Split(modulesEnv([]module.Module{{Name: "meta"}}), ",")
	want := map[string]bool{"meta": true, "communications": true, "customers": true}
	if len(got) != len(want) {
		t.Fatalf("modulesEnv(meta) = %v, want exactly %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("modulesEnv(meta) contains unexpected module %q", name)
		}
	}
}

// TestModulesEnv_ListsProductsAndInventoryBesideManufacturing proves a
// harness composing manufacturing lists products and inventory as enabled:
// config.go refuses "manufacturing" without either, and manufacturing's own
// tests stand in fakes of the catalog and the stock rather than compose them.
func TestModulesEnv_ListsProductsAndInventoryBesideManufacturing(t *testing.T) {
	t.Parallel()

	if got := modulesEnv([]module.Module{{Name: "manufacturing"}}); got != "customers,inventory,manufacturing,products" {
		t.Errorf("modulesEnv(manufacturing) = %q, want exactly %q", got, "customers,inventory,manufacturing,products")
	}
}

// TestModulesEnv_ListsInvoicesBesideAccounting proves a harness composing
// accounting lists invoices as enabled: config.go refuses "accounting" without
// it, and accounting's own tests stand in a fake sales journal rather than
// compose Invoices.
func TestModulesEnv_ListsInvoicesBesideAccounting(t *testing.T) {
	t.Parallel()

	if got := modulesEnv([]module.Module{{Name: "accounting"}}); got != "accounting,customers,invoices" {
		t.Errorf("modulesEnv(accounting) = %q, want exactly %q", got, "accounting,customers,invoices")
	}
}
