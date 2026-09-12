package energy_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Ported from EnergyEndpointsTests.Customer_consumption_after_switch_is_private_to_each_period.
func TestCustomerConsumption_AfterSwitchIsPrivateToEachPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	switchAt := utc(2026, time.August, 1, 12)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": switchAt.Add(-2 * time.Hour)})
	if first.Status != http.StatusCreated {
		t.Fatalf("create first period: status %d body %s, want 201", first.Status, first.Body)
	}
	switched := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/switch", point.Id),
		map[string]any{"customerId": 1002, "switchAt": switchAt})
	if switched.Status != http.StatusCreated {
		t.Fatalf("switch: status %d body %s, want 201", switched.Status, switched.Body)
	}

	addConsumption(t, c, point.Id, switchAt.Add(-2*time.Hour), switchAt.Add(-time.Hour), 1)
	addConsumption(t, c, point.Id, switchAt.Add(-time.Hour), switchAt, 2)
	addConsumption(t, c, point.Id, switchAt, switchAt.Add(time.Hour), 4)
	addConsumption(t, c, point.Id, switchAt.Add(time.Hour), switchAt.Add(2*time.Hour), 8)

	oldCustomer := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/customers/1001/consumption?meteringPointId=%d", point.Id), nil)
	newCustomer := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/customers/1002/consumption?meteringPointId=%d", point.Id), nil)
	var oldRows, newRows []consumptionJSON
	oldCustomer.JSON(&oldRows)
	newCustomer.JSON(&newRows)

	if got := quantities(oldRows); !equalFloats(got, []float64{1, 2}) {
		t.Errorf("customer 1001 quantities = %v, want [1 2]", got)
	}
	if got := quantities(newRows); !equalFloats(got, []float64{4, 8}) {
		t.Errorf("customer 1002 quantities = %v, want [4 8]", got)
	}
}

// Ported from EnergyEndpointsTests.Customer_consumption_respects_supply_period_boundaries.
func TestCustomerConsumption_RespectsSupplyPeriodBoundaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	handover := utc(2026, time.April, 15, 0)

	first := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": handover.AddDate(0, 0, -2)})
	if first.Status != http.StatusCreated {
		t.Fatalf("create first period: status %d body %s, want 201", first.Status, first.Body)
	}
	var firstPeriod supplyPeriodJSON
	first.JSON(&firstPeriod)
	if end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, firstPeriod.Id),
		map[string]any{"end": handover}); end.Status != http.StatusOK {
		t.Fatalf("end first period: status %d body %s, want 200", end.Status, end.Body)
	}
	if second := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1002, "start": handover}); second.Status != http.StatusCreated {
		t.Fatalf("create second period: status %d body %s, want 201", second.Status, second.Body)
	}

	addConsumption(t, c, point.Id, handover.Add(-2*time.Hour), handover.Add(-time.Hour), 1)
	addConsumption(t, c, point.Id, handover.Add(time.Hour), handover.Add(2*time.Hour), 2)
	addConsumption(t, c, point.Id, handover.Add(-time.Hour), handover.Add(time.Hour), 3)

	firstRows := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/customers/1001/consumption?meteringPointId=%d", point.Id), nil)
	secondRows := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/customers/1002/consumption?meteringPointId=%d", point.Id), nil)
	var firstList, secondList []consumptionJSON
	firstRows.JSON(&firstList)
	secondRows.JSON(&secondList)

	if got := quantities(firstList); !equalFloats(got, []float64{1}) {
		t.Errorf("customer 1001 quantities = %v, want [1] (the straddling interval belongs to neither period)", got)
	}
	if got := quantities(secondList); !equalFloats(got, []float64{2}) {
		t.Errorf("customer 1002 quantities = %v, want [2]", got)
	}
}

// Ported from EnergyEndpointsTests.Customer_consumption_aggregate_excludes_intervals_outside_supply_period.
func TestCustomerConsumptionAggregate_ExcludesIntervalsOutsideSupplyPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := utc(2026, time.January, 1, 0)
	end := utc(2026, time.January, 3, 0)

	created := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": start})
	if created.Status != http.StatusCreated {
		t.Fatalf("create period: status %d body %s, want 201", created.Status, created.Body)
	}
	var period supplyPeriodJSON
	created.JSON(&period)
	if r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
		map[string]any{"end": end}); r.Status != http.StatusOK {
		t.Fatalf("end period: status %d body %s, want 200", r.Status, r.Body)
	}

	addConsumption(t, c, point.Id, start.Add(-time.Hour), start, 1)
	addConsumption(t, c, point.Id, start.Add(time.Hour), start.Add(2*time.Hour), 2)
	addConsumption(t, c, point.Id, end, end.Add(time.Hour), 4)

	r := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/customers/1001/consumption/aggregate?meteringPointId=%d&from=2026-01-01T00:00:00Z&to=2026-01-04T00:00:00Z&resolution=day", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []customerConsumptionAggregateJSON
	r.JSON(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].MeteringPointId != point.Id {
		t.Errorf("MeteringPointId = %d, want %d", rows[0].MeteringPointId, point.Id)
	}
	if rows[0].QuantityKwh != 2 {
		t.Errorf("QuantityKwh = %v, want 2 (only the interval inside [start,end) counts)", rows[0].QuantityKwh)
	}
}

// TestGetCustomerConsumption_RangeInvalid pins GetCustomerConsumptionEndpoint's
// own to<=from check (:15-16), the same title/detail as the metering-point
// version.
func TestGetCustomerConsumption_RangeInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	from := utc(2026, time.January, 2, 0)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/customers/1001/consumption?from=%s&to=%s",
		from.Format(time.RFC3339), from.Format(time.RFC3339)), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid interval" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid interval")
	}
}

// TestCustomerConsumptionAggregate_InvalidRequestAgainstUnknownCustomer_Returns400
// pins customers.go's order for the customer-scoped aggregate: the same
// ConsumptionAggregateValidation runs before the metering-point listing
// query, so a bad resolution wins and an unknown customer id answers 400 —
// not the empty 200 a *valid* request for an unknown customer gets
// (TestGetCustomerConsumption_UnknownCustomerAnswersEmptyList below). Moving
// validation after the query would turn this into that 200.
func TestCustomerConsumptionAggregate_InvalidRequestAgainstUnknownCustomer_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet,
		"/api/v1/energy/customers/999999/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=week", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (validation must run before the listing query)", r.Status, r.Body)
	}
}

// TestGetCustomerConsumption_UnknownCustomerAnswersEmptyList pins energy
// inventory §1.2: the customer-scoped operations never call
// contracts.CustomerDirectory and trust customerId as opaque, so an unknown
// id is not a 404 — it is 200 with no rows.
func TestGetCustomerConsumption_UnknownCustomerAnswersEmptyList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet, "/api/v1/energy/customers/999999/consumption", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionJSON
	r.JSON(&rows)
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

// TestGetCustomerMeteringPoints_ListsPointsWithNonCancelledPeriodsOnly pins
// GetCustomerMeteringPointsEndpoint's join (:17-19): a metering point with
// only a cancelled period for the customer does not appear, one with a
// non-cancelled period does, carrying that period's own data.
func TestGetCustomerMeteringPoints_ListsPointsWithNonCancelledPeriodsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	included := createMeteringPoint(t, c)
	excluded := createMeteringPoint(t, c)

	createdIncluded := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", included.Id),
		map[string]any{"customerId": 1001, "start": utc(2026, time.January, 1, 0)})
	if createdIncluded.Status != http.StatusCreated {
		t.Fatalf("create included period: status %d body %s, want 201", createdIncluded.Status, createdIncluded.Body)
	}

	createdExcluded := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", excluded.Id),
		map[string]any{"customerId": 1001, "start": utc(2026, time.January, 1, 0)})
	if createdExcluded.Status != http.StatusCreated {
		t.Fatalf("create excluded period: status %d body %s, want 201", createdExcluded.Status, createdExcluded.Body)
	}
	var excludedPeriod supplyPeriodJSON
	createdExcluded.JSON(&excludedPeriod)
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d", excluded.Id, excludedPeriod.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("cancel excluded period: status %d body %s, want 204", r.Status, r.Body)
	}

	r := c.Do(http.MethodGet, "/api/v1/energy/customers/1001/metering-points", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []customerMeteringPointJSON
	r.JSON(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (only the point with a non-cancelled period)", len(rows))
	}
	if rows[0].MeteringPoint.Id != included.Id {
		t.Errorf("MeteringPoint.Id = %d, want %d", rows[0].MeteringPoint.Id, included.Id)
	}
	if len(rows[0].SupplyPeriods) != 1 || rows[0].SupplyPeriods[0].CustomerId != 1001 {
		t.Errorf("SupplyPeriods = %+v, want one period for customer 1001", rows[0].SupplyPeriods)
	}
}

func quantities(rows []consumptionJSON) []float64 {
	out := make([]float64, len(rows))
	for i, r := range rows {
		out[i] = r.QuantityKwh
	}
	return out
}

func equalFloats(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
