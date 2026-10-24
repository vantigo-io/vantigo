package energy_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestModule_ComposesAndDemandsAPermission proves energy mounts through
// module.Compose without a router problem — newHarness fails the test on any
// Compose error, which is what catches an operation the generated server
// never registers or a permission the catalog is missing — and that the
// listing answers the access layer's 401 without a session, as the contract
// documents it.
func TestModule_ComposesAndDemandsAPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.Client(t).Do(http.MethodGet, "/api/v1/energy/metering-points", nil)
	if r.Status != http.StatusUnauthorized || r.Code() != "unauthenticated" {
		t.Errorf("status %d code %q body %s, want 401 unauthenticated", r.Status, r.Code(), r.Body)
	}
}

// TestModule_DeclaresItsPermissionCatalog pins the module's name and all
// eleven permissions — the eight ported ones, energy:import and the spot prices' two — field for field, against the .NET catalog
// (AZ/EnergyPermissionCatalogContributor.cs, energy inventory §6): every key
// is delegable and none is sensitive, and every key shares one category,
// "Energy". Ported from TS/Authorization/EnergyPermissionCatalogTests.cs,
// adapted to the Go catalog shape (a plain []contracts.Permission rather
// than a registered PermissionDescriptor set): that test asserts the
// catalog's keys come back in stable alphabetical order
// (EnergyPermissionCatalogTests.cs:14-23) — slices.IsSorted below is that
// assertion — and this test additionally pins every other field exactly,
// which the .NET test's ordering-only assertion did not: a permission
// silently dropped, renamed, mis-labelled, or added would fail here even
// though "the module has permissions" or "the keys are sorted" would still
// both hold.
func TestModule_DeclaresItsPermissionCatalog(t *testing.T) {
	t.Parallel()
	m := energy.Module()

	want := []contracts.Permission{
		{Key: "energy:consumption-manage", Display: "Manage energy consumption", Description: "Add and replace manual energy consumption intervals.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:consumption-view", Display: "View energy consumption", Description: "View energy consumption intervals and aggregates.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:import", Display: "Import energy data", Description: "Import metering points and consumption from a file; each thing written needs the permission it needs by hand.", Category: "Energy", Sensitive: true, Delegable: true},
		{Key: "energy:metering-points-manage", Display: "Manage energy metering points", Description: "Create and update energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:metering-points-view", Display: "View energy metering points", Description: "View energy metering point details and listings.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:meters-manage", Display: "Manage energy meters", Description: "Replace meters installed at energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:meters-view", Display: "View energy meters", Description: "View energy meter history for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:prices-manage", Display: "Manage energy prices", Description: "Import spot prices from a file.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:prices-view", Display: "View energy prices", Description: "View spot prices per price area and what is held of them.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:supply-periods-manage", Display: "Manage energy supply periods", Description: "Create, switch, end, and cancel energy supply periods.", Category: "Energy", Sensitive: false, Delegable: true},
		{Key: "energy:supply-periods-view", Display: "View energy supply periods", Description: "View energy supply periods for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	}
	if m.Name != "energy" {
		t.Errorf("Name = %q, want energy", m.Name)
	}
	if len(m.Permissions) != 11 {
		t.Fatalf("Permissions has %d entries, want exactly 11: %+v", len(m.Permissions), m.Permissions)
	}

	// Ported from EnergyPermissionCatalogTests.cs:14-23: the catalog's keys
	// come back in stable alphabetical order. want is itself written in
	// alphabetical order, so this also doubles as a check that want and
	// m.Permissions agree on order, not just membership.
	keys := make([]string, len(m.Permissions))
	for i, p := range m.Permissions {
		keys[i] = p.Key
	}
	if !slices.IsSorted(keys) {
		t.Errorf("Permissions keys = %v, want alphabetically sorted", keys)
	}

	if !slices.Equal(m.Permissions, want) {
		t.Errorf("Permissions = %+v, want %+v", m.Permissions, want)
	}
	for _, p := range m.Permissions {
		if p.Category != "Energy" {
			t.Errorf("permission %s: Category = %q, want %q (energy inventory §6: every entry shares one category)", p.Key, p.Category, "Energy")
		}
		if !p.Delegable {
			t.Errorf("permission %s: Delegable = false, want true (energy inventory §6: every key is delegable)", p.Key)
		}
		// energy:import (energy import design D5) is the one sensitive key: one
		// import writes thousands of points and periods under one name.
		if p.Sensitive != (p.Key == "energy:import") {
			t.Errorf("permission %s: Sensitive = %v, want %v (energy inventory §6: none is sensitive but energy:import)", p.Key, p.Sensitive, !p.Sensitive)
		}
	}
	if m.Directory != nil {
		t.Error("Module declares a customer directory, want nil: energy is a leaf module and publishes no contracts.CustomerDirectory")
	}
}

// There is no TestModule_StubbedOperationAnswers501 any more: that test
// pinned getEnergyStatsSummary as the one operation still answering
// unimplemented.go's 501 stub before Task 15. Task 15 implements
// consumption, aggregation and stats — the module's last area — so
// unimplemented.go is gone and every one of the 20 contract operations now
// has a real handler; router.Err()'s "never registered" check
// (TestModule_ComposesAndDemandsAPermission's newHarness call) is what
// still proves every operation is routed.

// TestModule_ContributesItsWorkers asserts the module's workers exactly: the
// import worker (energy import design D6) when ENERGY_IMPORT_ENABLED is on,
// the default, and none when it is off — a worker added without its name
// here fails rather than silently never running in production.
func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "the default installation runs the import worker", want: []string{"energy-import"}},
		{name: "the import worker turned off leaves none", env: map[string]string{"ENERGY_IMPORT_ENABLED": "0"}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := make([]modtest.Option, 0, len(tc.env))
			for k, v := range tc.env {
				opts = append(opts, modtest.WithEnv(k, v))
			}
			h := newHarness(t, opts...)
			var names []string
			for _, w := range module.Workers(h.Deps(), energy.Module()) {
				names = append(names, w.Name())
				if w.Interval() <= 0 {
					t.Errorf("worker %s has interval %v, want a positive poll interval", w.Name(), w.Interval())
				}
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("workers = %v, want exactly %v", names, tc.want)
			}
		})
	}
}
