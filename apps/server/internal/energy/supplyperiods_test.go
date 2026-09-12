package energy_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestGetSupplyPeriods_ListsOrderedByStart is GetSupplyPeriodsEndpoint's
// success path (GetSupplyPeriodsEndpoint.cs:14-16): every period for the
// metering point, ordered by Start.
func TestGetSupplyPeriods_ListsOrderedByStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	early := h.Now()
	later := early.Add(time.Hour)

	createEarly := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": early})
	if createEarly.Status != http.StatusCreated {
		t.Fatalf("create early: status %d body %s, want 201", createEarly.Status, createEarly.Body)
	}
	var earlyPeriod supplyPeriodJSON
	createEarly.JSON(&earlyPeriod)
	// End the early period exactly where the later one starts: half-open
	// [start, end) semantics mean the two are adjacent, not overlapping.
	if end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, earlyPeriod.Id),
		map[string]any{"end": later}); end.Status != http.StatusOK {
		t.Fatalf("end early: status %d body %s, want 200", end.Status, end.Body)
	}
	createLater := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1002, "start": later})
	if createLater.Status != http.StatusCreated {
		t.Fatalf("create later: status %d body %s, want 201", createLater.Status, createLater.Body)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var periods []supplyPeriodJSON
	r.JSON(&periods)
	if len(periods) != 2 {
		t.Fatalf("periods = %d, want 2", len(periods))
	}
	if !periods[0].Start.Before(periods[1].Start) {
		t.Errorf("periods not ordered by Start ascending: %v, %v", periods[0].Start, periods[1].Start)
	}
	if periods[0].CustomerId != 1001 || periods[1].CustomerId != 1002 {
		t.Errorf("periods = %+v, want the earlier-start period (customer 1001) first", periods)
	}
}

func TestGetSupplyPeriods_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodGet, "/api/v1/energy/metering-points/999999/supply-periods", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// Ported from EnergyEndpointsTests.Supply_period_overlap_returns_conflict_and_end_allows_handover.
func TestCreateSupplyPeriod_OverlapConflictThenEndAllowsHandover(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := h.Now().AddDate(0, 0, -2)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	if first.Status != http.StatusCreated {
		t.Fatalf("first create: status %d body %s, want 201", first.Status, first.Body)
	}
	var period supplyPeriodJSON
	first.JSON(&period)

	overlap := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1002, "start": start.AddDate(0, 0, 1)})
	if overlap.Status != http.StatusConflict {
		t.Fatalf("overlap: status %d body %s, want 409", overlap.Status, overlap.Body)
	}
	var problem problemJSON
	overlap.JSON(&problem)
	if problem.Title != "Overlapping supply period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Overlapping supply period")
	}
	wantDetail := "The metering point already has a non-cancelled supply period at that time. End the existing period first."
	if problem.Detail != wantDetail {
		t.Errorf("Detail = %q, want %q", problem.Detail, wantDetail)
	}

	end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
		map[string]any{"end": start.AddDate(0, 0, 1)})
	if end.Status != http.StatusOK {
		t.Fatalf("end: status %d body %s, want 200", end.Status, end.Body)
	}

	after := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1002, "start": start.AddDate(0, 0, 1)})
	if after.Status != http.StatusCreated {
		t.Errorf("create after end: status %d body %s, want 201", after.Status, after.Body)
	}
}

// TestCreateSupplyPeriod_Ordering pins CreateSupplyPeriodEndpoint's exact
// order (energy inventory §1.1 line 39, dispatch correction 2):
// customerId<=0 -> the UTC-offset check on start -> metering point
// existence -> the directory miss -> the overlap pre-check. Each subtest
// arranges the *earlier* checks to pass so only the one under test can
// fire; a mutation that reordered any two steps would make one of these
// fail by returning the wrong status/body for a request built to trip a
// later check.
func TestCreateSupplyPeriod_Ordering(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	t.Run("customerId<=0 before UTC check", func(t *testing.T) {
		t.Parallel()
		// customerId invalid AND start not UTC: if start's UTC check ran
		// first this would still 400, but the field must be customerId.
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
			map[string]any{"customerId": 0, "start": "2026-01-01T00:00:00+02:00"})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		got := problem.Errors["customerId"]
		if len(got) != 1 || got[0] != "Customer ID must be greater than zero." {
			t.Errorf("Errors[customerId] = %v, want the customerId message (customerId checked before the UTC offset)", got)
		}
	})

	t.Run("UTC check before existence", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/supply-periods",
			map[string]any{"customerId": 1001, "start": "2026-01-01T00:00:00+02:00"})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400 (UTC check before existence, nonexistent metering point)", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		got := problem.Errors["start"]
		if len(got) != 1 || got[0] != "Start and end must be UTC timestamps." {
			t.Errorf("Errors[start] = %v, want the UTC message", got)
		}
	})

	t.Run("existence before directory miss", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/supply-periods",
			map[string]any{"customerId": 404404, "start": h.Now()})
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404 (existence before the directory miss, nonexistent metering point and unknown customer)", r.Status, r.Body)
		}
	})
}

// TestCreateSupplyPeriod_UnknownCustomer_Returns400NotFound pins dispatch
// correction 1: a customer-directory miss is 400 ValidationProblem on field
// customerId, never 404 — the obvious wrong guess. A mutation answering 404
// here would look "more RESTful" but diverges from .NET.
func TestCreateSupplyPeriod_UnknownCustomer_Returns400NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 999999, "start": h.Now()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (not 404)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid supply period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid supply period")
	}
	want := "Customer 999999 does not exist."
	got := problem.Errors["customerId"]
	if len(got) != 1 || got[0] != want {
		t.Errorf("Errors[customerId] = %v, want [%q]", got, want)
	}
}

// Ported from EnergyEndpointsTests.Supply_period_switch_ends_current_period_and_creates_contiguous_period.
func TestSwitchSupplyPeriod_EndsCurrentAndCreatesContiguous(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	switchAt := start.AddDate(0, 0, 3)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	var firstPeriod supplyPeriodJSON
	first.JSON(&firstPeriod)

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1002, "switchAt": switchAt})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var switched switchSupplyPeriodJSON
	r.JSON(&switched)
	if switched.EndedPeriod == nil {
		t.Fatalf("EndedPeriod = nil, want the first period")
	}
	if switched.EndedPeriod.Id != firstPeriod.Id {
		t.Errorf("EndedPeriod.Id = %d, want %d", switched.EndedPeriod.Id, firstPeriod.Id)
	}
	if switched.EndedPeriod.Status != "Ended" {
		t.Errorf("EndedPeriod.Status = %q, want Ended", switched.EndedPeriod.Status)
	}
	if switched.EndedPeriod.End == nil || !switched.EndedPeriod.End.Equal(switchAt) {
		t.Errorf("EndedPeriod.End = %v, want %v", switched.EndedPeriod.End, switchAt)
	}
	if !switched.NewPeriod.Start.Equal(switchAt) {
		t.Errorf("NewPeriod.Start = %v, want %v", switched.NewPeriod.Start, switchAt)
	}
	if switched.NewPeriod.End != nil {
		t.Errorf("NewPeriod.End = %v, want nil", switched.NewPeriod.End)
	}
	if switched.NewPeriod.CustomerId != 1002 {
		t.Errorf("NewPeriod.CustomerId = %d, want 1002", switched.NewPeriod.CustomerId)
	}
}

// Ported from EnergyEndpointsTests.Supply_period_switch_without_active_period_behaves_as_move_in.
func TestSwitchSupplyPeriod_WithoutActivePeriod_BehavesAsMoveIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1001, "switchAt": time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var switched switchSupplyPeriodJSON
	r.JSON(&switched)
	if switched.EndedPeriod != nil {
		t.Errorf("EndedPeriod = %v, want nil", switched.EndedPeriod)
	}
	if switched.NewPeriod.CustomerId != 1001 {
		t.Errorf("NewPeriod.CustomerId = %d, want 1001", switched.NewPeriod.CustomerId)
	}
}

// Ported from EnergyEndpointsTests.Supply_period_switch_rejects_before_start_and_same_customer.
func TestSwitchSupplyPeriod_RejectsBeforeStartAndSameCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	if first.Status != http.StatusCreated {
		t.Fatalf("first create: status %d body %s, want 201", first.Status, first.Body)
	}

	beforeStart := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1002, "switchAt": start.Add(-time.Minute)})
	if beforeStart.Status != http.StatusBadRequest {
		t.Fatalf("beforeStart: status %d body %s, want 400", beforeStart.Status, beforeStart.Body)
	}
	var beforeProblem validationProblemJSON
	beforeStart.JSON(&beforeProblem)
	want := "Switch date must be after the active period's start."
	if got := beforeProblem.Errors["switchAt"]; len(got) != 1 || got[0] != want {
		t.Errorf("Errors[switchAt] = %v, want [%q]", got, want)
	}

	sameCustomer := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1001, "switchAt": start.AddDate(0, 0, 1)})
	if sameCustomer.Status != http.StatusBadRequest {
		t.Fatalf("sameCustomer: status %d body %s, want 400", sameCustomer.Status, sameCustomer.Body)
	}
	var sameProblem validationProblemJSON
	sameCustomer.JSON(&sameProblem)
	wantSame := "The customer is already the active customer."
	if got := sameProblem.Errors["customerId"]; len(got) != 1 || got[0] != wantSame {
		t.Errorf("Errors[customerId] = %v, want [%q]", got, wantSame)
	}
}

// Ported from EnergyEndpointsTests.Supply_period_switch_rejects_overlap_with_historical_period.
// Dispatch correction 5: the switch move-in overlap pre-check must 409
// against a *historical* (Ended) period whose end is still in the future —
// easy to omit by checking status alone instead of the end-boundary
// COALESCE(end, infinity) comparison.
func TestSwitchSupplyPeriod_RejectsOverlapWithHistoricalPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	var period supplyPeriodJSON
	first.JSON(&period)

	end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
		map[string]any{"end": start.AddDate(0, 0, 2)})
	if end.Status != http.StatusOK {
		t.Fatalf("end: status %d body %s, want 200", end.Status, end.Body)
	}
	// The period is now historical (Ended), with End one day *after*
	// switchAt below — it must still conflict.

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1002, "switchAt": start.AddDate(0, 0, 1)})
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409 (overlap with a historical, still-future-ended period)", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Overlapping supply period" {
		t.Errorf("Title = %q, want %q (the friendly pre-check, not the generic constraint body)", problem.Title, "Overlapping supply period")
	}
}

// TestSwitchSupplyPeriod_Ordering pins SwitchSupplyPeriodEndpoint's exact
// order (energy inventory §1.1 line 40, dispatch correction 2): metering
// point existence *first* (the opposite of Create above), then
// customerId<=0, then the switchAt UTC check, then the directory miss.
func TestSwitchSupplyPeriod_Ordering(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	t.Run("existence before customerId<=0", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/supply-periods/switch",
			map[string]any{"customerId": 0, "switchAt": h.Now()})
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404 (existence before customerId<=0, nonexistent metering point and invalid customerId)", r.Status, r.Body)
		}
	})

	t.Run("customerId<=0 before UTC check", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
			map[string]any{"customerId": 0, "switchAt": "2026-01-01T00:00:00+02:00"})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		got := problem.Errors["customerId"]
		if len(got) != 1 || got[0] != "Customer ID must be greater than zero." {
			t.Errorf("Errors[customerId] = %v, want the customerId message (checked before the UTC offset)", got)
		}
	})

	t.Run("UTC check before directory miss", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
			map[string]any{"customerId": 999999, "switchAt": "2026-01-01T00:00:00+02:00"})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		got := problem.Errors["switchAt"]
		if len(got) != 1 || got[0] != "Start and end must be UTC timestamps." {
			t.Errorf("Errors[switchAt] = %v, want the UTC message (checked before the directory miss, unknown customer too)", got)
		}
	})

	t.Run("unknown customer is 400 not 404", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
			map[string]any{"customerId": 999999, "switchAt": h.Now()})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400 (not 404)", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if got := problem.Errors["customerId"]; len(got) != 1 || got[0] != "Customer 999999 does not exist." {
			t.Errorf("Errors[customerId] = %v, want the unknown-customer message", got)
		}
	})
}

// Ported from EnergyEndpointsTests's EndSupplyPeriodEndpoint coverage plus
// its "cancelled period" 400 branch (energy inventory §1.1 line 41).
func TestEndSupplyPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/supply-periods/999999/end", map[string]any{"end": h.Now()})
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404", r.Status, r.Body)
		}
	})

	t.Run("bad end date", func(t *testing.T) {
		t.Parallel()
		point := createMeteringPoint(t, c)
		start := h.Now()
		create := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
			map[string]any{"customerId": 1001, "start": start})
		var period supplyPeriodJSON
		create.JSON(&period)

		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
			map[string]any{"end": start.Add(-time.Hour)})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if problem.Title != "Invalid supply period" {
			t.Errorf("Title = %q, want %q", problem.Title, "Invalid supply period")
		}
		want := "End must be later than start."
		if got := problem.Errors["end"]; len(got) != 1 || got[0] != want {
			t.Errorf("Errors[end] = %v, want [%q]", got, want)
		}
	})

	// TestEndSupplyPeriod_CancelledPeriod pins dispatch/inventory oddity 2:
	// this branch's 400 is a *plain* Problem body (title/detail, no
	// "errors" field) despite the contract's declared
	// HttpValidationProblemDetails shape for this operation's 400 — the
	// same operation's other 400 (bad end date, above) does carry "errors".
	// A mutation that made both branches share one shape would still pass
	// a status-only assertion; this one checks Errors is empty/absent
	// specifically for the cancelled branch.
	t.Run("cancelled period cannot be ended", func(t *testing.T) {
		t.Parallel()
		point := createMeteringPoint(t, c)
		start := h.Now()
		create := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
			map[string]any{"customerId": 1001, "start": start})
		var period supplyPeriodJSON
		create.JSON(&period)

		cancel := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d", point.Id, period.Id), nil)
		if cancel.Status != http.StatusNoContent {
			t.Fatalf("cancel: status %d body %s, want 204", cancel.Status, cancel.Body)
		}

		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
			map[string]any{"end": start.Add(time.Hour)})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var validation validationProblemJSON
		r.JSON(&validation)
		if validation.Title != "Invalid supply period" {
			t.Errorf("Title = %q, want %q", validation.Title, "Invalid supply period")
		}
		if len(validation.Errors) != 0 {
			t.Errorf("Errors = %v, want empty (this branch's 400 carries no field errors)", validation.Errors)
		}
		var plain problemJSON
		r.JSON(&plain)
		if plain.Detail != "A cancelled period cannot be ended." {
			t.Errorf("Detail = %q, want %q", plain.Detail, "A cancelled period cannot be ended.")
		}
	})
}

// TestCancelSupplyPeriod_HasNoStatusGuard pins energy inventory §1.1/§8
// oddity 5: an already-Ended or already-Cancelled period can be cancelled
// again unconditionally — there is no "cannot cancel a closed period" rule,
// unlike End. A mutation adding such a guard would look like a reasonable
// hardening but would diverge from .NET.
func TestCancelSupplyPeriod_HasNoStatusGuard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := h.Now()
	create := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	var period supplyPeriodJSON
	create.JSON(&period)

	first := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d", point.Id, period.Id), nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first cancel: status %d body %s, want 204", first.Status, first.Body)
	}
	second := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d", point.Id, period.Id), nil)
	if second.Status != http.StatusNoContent {
		t.Errorf("second cancel: status %d body %s, want 204 (no status guard)", second.Status, second.Body)
	}
}

func TestCancelSupplyPeriod_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodDelete, "/api/v1/energy/metering-points/999999/supply-periods/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
