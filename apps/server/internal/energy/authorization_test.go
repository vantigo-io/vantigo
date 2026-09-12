package energy_test

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// authTestGsrnCounter makes each authorization-test create-metering-point
// body carry a fresh GSRN: the endpoint case can run against several
// clients in one sub-test (TestEveryEnergyEndpointRequiresItsRegisteredPermission's
// five, TestCompositeEnergyEndpoints_RequireEachRelatedPermission's one per
// missing permission), and this file's own gsrn() cannot be reused here — it
// requires a *testing.T to call t.Helper() on, which a package-level slice
// literal's closures do not have.
var authTestGsrnCounter atomic.Uint64

func authTestGsrn() string {
	n := authTestGsrnCounter.Add(1)
	return fmt.Sprintf("7070576%011d", n)[:18]
}

// Ported from TS/Integration/EnergyAuthorizationIntegrationTests.cs (energy
// inventory §7): three tests asserting the module's whole permission matrix,
// not just this task's own operations, since it exercises every operation
// the earlier metering-points/meters/supply-periods tasks built too — this
// module's authorization coverage was zero until this file, a gap found
// reviewing Task 14 and folded into this, the module's last task.
//
// Every required-permission set below was read directly from
// internal/openapi/specs/energy.yaml's x-vantigo-access lines (each
// confirmed against energy inventory §1.1/§1.2's own permission column,
// itself built from the .NET RequirePermission calls) — never grepped: this
// contract's YAML places x-vantigo-access after responses, so a grep for
// that key attributes the match to the *previous* operation, not the one it
// looks like it follows (a mistake a reviewer on this branch caught only on
// a second pass of Task 14). energyEndpointCases below omits the three
// /stats/* operations: EnergyAuthorizationIntegrationTests.Endpoints does
// too (its own array never mentions them), so nothing in this file's ported
// set exercises them either — this is a faithful port, not new scope.

// energyEndpointCase pairs one contract operation's request shape with the
// full permission set its x-vantigo-access AND-list names.
type energyEndpointCase struct {
	method      string
	path        string
	permissions []string
	body        func(now time.Time) any
}

// composite reports whether this operation's x-vantigo-access names more
// than one permission — EndpointCase.HasAdditionalPermissions's Go
// counterpart, gating which cases
// TestCompositeEnergyEndpoints_RequireEachRelatedPermission exercises.
func (c energyEndpointCase) composite() bool { return len(c.permissions) > 1 }

func (c energyEndpointCase) do(client *modtest.Client, now time.Time) *modtest.Response {
	var body any
	if c.body != nil {
		body = c.body(now)
	}
	return client.Do(c.method, c.path, body)
}

var energyEndpointCases = []energyEndpointCase{
	{method: http.MethodGet, path: "/api/v1/energy/metering-points",
		permissions: []string{"energy:metering-points-view", "energy:meters-view"}},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points",
		permissions: []string{"energy:metering-points-manage", "energy:metering-points-view", "energy:meters-manage", "energy:meters-view"},
		body:        func(time.Time) any { return newMeteringPointBody(authTestGsrn(), "Energy authorization test meter") }},
	{method: http.MethodGet, path: "/api/v1/energy/metering-points/999999",
		permissions: []string{"energy:metering-points-view", "energy:meters-view"}},
	{method: http.MethodPut, path: "/api/v1/energy/metering-points/999999",
		permissions: []string{"energy:metering-points-manage", "energy:metering-points-view", "energy:meters-view"},
		body:        func(time.Time) any { return newMeteringPointBody(authTestGsrn(), "Energy authorization test meter") }},
	{method: http.MethodGet, path: "/api/v1/energy/metering-points/999999/meters",
		permissions: []string{"energy:meters-view"}},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points/999999/meters",
		permissions: []string{"energy:meters-manage", "energy:meters-view"},
		body:        func(now time.Time) any { return map[string]any{"meterNumber": "Replacement", "installedAt": now} }},
	{method: http.MethodGet, path: "/api/v1/energy/metering-points/999999/consumption",
		permissions: []string{"energy:consumption-view"}},
	{method: http.MethodGet, path: "/api/v1/energy/metering-points/999999/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=day",
		permissions: []string{"energy:consumption-view"}},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points/999999/consumption",
		permissions: []string{"energy:consumption-manage", "energy:consumption-view"},
		body: func(now time.Time) any {
			return map[string]any{"start": now.Add(-2 * time.Hour), "end": now.Add(-time.Hour), "quantityKwh": 1}
		}},
	{method: http.MethodGet, path: "/api/v1/energy/metering-points/999999/supply-periods",
		permissions: []string{"energy:supply-periods-view"}},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points/999999/supply-periods",
		permissions: []string{"energy:supply-periods-manage", "energy:supply-periods-view"},
		body:        func(now time.Time) any { return map[string]any{"customerId": 1001, "start": now.Add(-24 * time.Hour)} }},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points/999999/supply-periods/switch",
		permissions: []string{"energy:supply-periods-manage", "energy:supply-periods-view"},
		body:        func(now time.Time) any { return map[string]any{"customerId": 1001, "switchAt": now} }},
	{method: http.MethodPost, path: "/api/v1/energy/metering-points/999999/supply-periods/999999/end",
		permissions: []string{"energy:supply-periods-manage", "energy:supply-periods-view"},
		body:        func(now time.Time) any { return map[string]any{"end": now} }},
	{method: http.MethodDelete, path: "/api/v1/energy/metering-points/999999/supply-periods/999999",
		permissions: []string{"energy:supply-periods-manage"}},
	{method: http.MethodGet, path: "/api/v1/energy/customers/1001/metering-points",
		permissions: []string{"energy:metering-points-view", "energy:meters-view", "energy:supply-periods-view"}},
	{method: http.MethodGet, path: "/api/v1/energy/customers/1001/consumption",
		permissions: []string{"energy:consumption-view"}},
	{method: http.MethodGet, path: "/api/v1/energy/customers/1001/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=day",
		permissions: []string{"energy:consumption-view"}},
}

// allEnergyEndpointPermissions is the distinct union of every case's
// permissions, ported from Endpoints.SelectMany(...).Distinct().
func allEnergyEndpointPermissions() []string {
	seen := map[string]bool{}
	var all []string
	for _, c := range energyEndpointCases {
		for _, p := range c.permissions {
			if !seen[p] {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	return all
}

// satisfiedBy reports whether held covers every permission in required.
func satisfiedBy(required, held []string) bool {
	set := map[string]bool{}
	for _, p := range held {
		set[p] = true
	}
	for _, p := range required {
		if !set[p] {
			return false
		}
	}
	return true
}

// without returns all minus exclude, preserving order.
func without(all []string, exclude string) []string {
	out := make([]string, 0, len(all))
	for _, p := range all {
		if p != exclude {
			out = append(out, p)
		}
	}
	return out
}

// Ported from EnergyAuthorizationIntegrationTests.EveryEnergyEndpointRequiresItsRegisteredEnergyPermission.
// "owner" in the .NET original is factory.CreateAuthenticatedClient(), a
// caller who holds every permission the endpoint set requires; this port
// follows the same convention products/customers already established
// (authenticatedClient/allEnergyPermissions) rather than modelling a
// distinct Owner role, so the .NET "owner" and "all permissions" clients
// collapse into the one allPermissions client below.
func TestEveryEnergyEndpointRequiresItsRegisteredPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	now := h.Now()

	viewPermissions := []string{
		"energy:metering-points-view", "energy:meters-view",
		"energy:consumption-view", "energy:supply-periods-view",
	}
	managePermissions := []string{
		"energy:metering-points-manage", "energy:meters-manage",
		"energy:consumption-manage", "energy:supply-periods-manage",
	}
	allPerms := allEnergyEndpointPermissions()

	noPermission := h.SignIn(t)
	viewOnly := h.SignIn(t, viewPermissions...)
	manageOnly := h.SignIn(t, managePermissions...)
	allPermissions := h.SignIn(t, allPerms...)
	disabled := h.SignInDisabled(t, allPerms...)

	for _, ec := range energyEndpointCases {
		t.Run(ec.method+" "+ec.path, func(t *testing.T) {
			if r := ec.do(noPermission, now); r.Status != http.StatusForbidden {
				t.Errorf("no permission: status %d body %s, want 403", r.Status, r.Body)
			}

			viewResp := ec.do(viewOnly, now)
			if satisfiedBy(ec.permissions, viewPermissions) {
				if viewResp.Status == http.StatusForbidden {
					t.Errorf("view-only: status 403 body %s, want not forbidden (holds %v)", viewResp.Body, ec.permissions)
				}
			} else if viewResp.Status != http.StatusForbidden {
				t.Errorf("view-only: status %d body %s, want 403 (requires a manage permission)", viewResp.Status, viewResp.Body)
			}

			manageResp := ec.do(manageOnly, now)
			if satisfiedBy(ec.permissions, managePermissions) {
				if manageResp.Status == http.StatusForbidden {
					t.Errorf("manage-only: status 403 body %s, want not forbidden (holds %v)", manageResp.Body, ec.permissions)
				}
			} else if manageResp.Status != http.StatusForbidden {
				t.Errorf("manage-only: status %d body %s, want 403 (requires a view permission)", manageResp.Status, manageResp.Body)
			}

			if r := ec.do(allPermissions, now); r.Status == http.StatusForbidden {
				t.Errorf("all permissions: status 403 body %s, want not forbidden", r.Body)
			}

			r := ec.do(disabled, now)
			if r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
				t.Errorf("disabled user reached %s %s: status %d body %s, want 401 or 403", ec.method, ec.path, r.Status, r.Body)
			}
		})
	}
}

// Ported from EnergyAuthorizationIntegrationTests.Customer_metering_points_requires_all_permissions_for_returned_data:
// getEnergyCustomersByCustomerIdMeteringPoints' x-vantigo-access ANDs three
// permissions, so holding only one or two of them must still 403.
func TestGetCustomerMeteringPoints_RequiresAllThreePermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const path = "/api/v1/energy/customers/1001/metering-points"

	meteringPointsOnly := h.SignIn(t, "energy:metering-points-view")
	metersOnly := h.SignIn(t, "energy:meters-view")
	supplyPeriodsOnly := h.SignIn(t, "energy:supply-periods-view")
	complete := h.SignIn(t, "energy:metering-points-view", "energy:meters-view", "energy:supply-periods-view")

	if r := meteringPointsOnly.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("metering-points-view only: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := metersOnly.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("meters-view only: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := supplyPeriodsOnly.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
		t.Errorf("supply-periods-view only: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := complete.Do(http.MethodGet, path, nil); r.Status == http.StatusForbidden {
		t.Errorf("complete: status 403 body %s, want not forbidden", r.Body)
	}
}

// Ported from EnergyAuthorizationIntegrationTests.Composite_resource_endpoints_require_each_related_permission:
// for every operation whose x-vantigo-access names more than one
// permission, dropping any single one of them from an otherwise-complete
// grant must 403 — proving each permission in the AND-set is independently
// load-bearing, not just present alongside ones that alone would suffice.
func TestCompositeEnergyEndpoints_RequireEachRelatedPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	now := h.Now()

	var composite []energyEndpointCase
	for _, ec := range energyEndpointCases {
		if ec.composite() {
			composite = append(composite, ec)
		}
	}
	required := allRequiredPermissions(composite)
	complete := h.SignIn(t, required...)

	for _, ec := range composite {
		t.Run(ec.method+" "+ec.path, func(t *testing.T) {
			if r := ec.do(complete, now); r.Status == http.StatusForbidden {
				t.Errorf("complete: status 403 body %s, want not forbidden", r.Body)
			}
			for _, missing := range ec.permissions {
				narrow := h.SignIn(t, without(required, missing)...)
				r := ec.do(narrow, now)
				if r.Status != http.StatusForbidden {
					t.Errorf("missing %s: status %d body %s, want 403", missing, r.Status, r.Body)
				}
			}
		})
	}
}

func allRequiredPermissions(cases []energyEndpointCase) []string {
	seen := map[string]bool{}
	var all []string
	for _, ec := range cases {
		for _, p := range ec.permissions {
			if !seen[p] {
				seen[p] = true
				all = append(all, p)
			}
		}
	}
	return all
}
