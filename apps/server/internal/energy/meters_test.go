package energy_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Ported from EnergyEndpointsTests.Meter_swap_closes_old_meter_and_keeps_ordered_history.
func TestReplaceMeter_ClosesOldMeterAndKeepsOrderedHistory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	installedAt := h.Now().Add(24 * time.Hour)
	swap := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/meters", point.Id), map[string]any{
		"meterNumber": "Replacement", "installedAt": installedAt,
	})
	if swap.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", swap.Status, swap.Body)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/meters", point.Id), nil)
	var meters []meterJSON
	r.JSON(&meters)
	if len(meters) != 2 {
		t.Fatalf("meters = %d, want 2", len(meters))
	}
	if !meters[0].InstalledAt.Before(meters[1].InstalledAt) {
		t.Errorf("meters not ordered by InstalledAt ascending: %v, %v", meters[0].InstalledAt, meters[1].InstalledAt)
	}
	if meters[0].RemovedAt == nil || !meters[0].RemovedAt.Equal(installedAt) {
		t.Errorf("old meter RemovedAt = %v, want %v", meters[0].RemovedAt, installedAt)
	}
	if meters[1].RemovedAt != nil {
		t.Errorf("new meter RemovedAt = %v, want nil", meters[1].RemovedAt)
	}

	current := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), nil)
	var got meteringPointJSON
	current.JSON(&got)
	if got.MeterNumber == nil || *got.MeterNumber != "Replacement" {
		t.Errorf("current MeterNumber = %v, want \"Replacement\"", got.MeterNumber)
	}
}

func TestGetMeters_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodGet, "/api/v1/energy/metering-points/999999/meters", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestReplaceMeter_ExistenceBeforeValidation pins ReplaceMeterEndpoint's
// ordering (energy inventory §1.1 line 34, dispatch correction 3): a
// malformed body against a nonexistent metering point answers 404, not
// 400 — the opposite of PUT /{id} (meteringpoints_test.go's mirror-image
// test). A mutation that swapped the order (validate first) would turn
// this 404 into a 400.
func TestReplaceMeter_ExistenceBeforeValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/meters", map[string]any{})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (existence before validation)", r.Status, r.Body)
	}
}

func TestReplaceMeter_ValidationMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	cases := []struct {
		name       string
		body       map[string]any
		field      string
		wantErrMsg string
	}{
		{"missing meterNumber", map[string]any{"installedAt": h.Now().Add(time.Hour)}, "meterNumber", "Meter number is required."},
		{"missing installedAt", map[string]any{"meterNumber": "M2"}, "installedAt", "Installed at is required."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/meters", point.Id), tc.body)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if problem.Title != "Invalid meter" {
				t.Errorf("Title = %q, want %q", problem.Title, "Invalid meter")
			}
			got := problem.Errors[tc.field]
			if len(got) != 1 || got[0] != tc.wantErrMsg {
				t.Errorf("Errors[%q] = %v, want [%q]", tc.field, got, tc.wantErrMsg)
			}
		})
	}
}

// TestReplaceMeter_InstalledAtNotAfterActive_Returns400NotConflict pins
// dispatch correction 3: `installedAt <= active.InstalledAt` is a 400
// ValidationProblem on field installedAt, never a 409 — despite being a
// business conflict, and despite this task's supply-period operations using
// 409 for their own logical conflicts. A mutation that answered 409 here
// instead of 400 would pass a status-only check but fail this one, which
// also asserts the field-keyed validation body.
func TestReplaceMeter_InstalledAtNotAfterActive_Returns400NotConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	cases := []struct {
		name        string
		installedAt time.Time
	}{
		{"equal to active's installedAt", h.Now()},
		{"before active's installedAt", h.Now().Add(-time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/meters", point.Id), map[string]any{
				"meterNumber": "Conflicting", "installedAt": tc.installedAt,
			})
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400 (not 409)", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if problem.Title != "Invalid meter" {
				t.Errorf("Title = %q, want %q", problem.Title, "Invalid meter")
			}
			want := "Installed at must be after the active meter's installation time."
			got := problem.Errors["installedAt"]
			if len(got) != 1 || got[0] != want {
				t.Errorf("Errors[installedAt] = %v, want [%q]", got, want)
			}
		})
	}
}
