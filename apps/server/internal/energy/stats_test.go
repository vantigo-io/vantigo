package energy_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is not a port: energy inventory §7's test table lists no .NET
// test class for EnergyStatsEndpoints.cs, and neither EnergyEndpointsTests
// nor EnergyAuthorizationIntegrationTests exercises any /stats/* route
// (products/stats_test.go and customers/stats_test.go document the same gap
// for their own modules' dashboard endpoints). These tests are this task's
// own, written directly against EnergyStatsEndpoints.cs's behavior; the
// module's coverage gate (main_test.go's RequireCoverage) requires every
// implemented operation be exercised regardless of a .NET original to port.

// insertActiveSupplyPeriod inserts a supply period whose status is Active
// and whose end is set — a combination the module's own state machine never
// produces through any endpoint (End always transitions status to Ended in
// the same write, energy inventory §2.3), so GetEnergyStatsAttention's rule
// ("active periods whose end falls within [now, now+30d]") is otherwise
// unreachable via HTTP. The DST aggregate test uses the same kind of direct
// bypass for the same reason: some states this module's queries defend
// against only exist by direct construction, not through the public API.
func insertActiveSupplyPeriod(t *testing.T, h *modtest.Harness, meteringPointID, customerID int32, start, end time.Time) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, $2, $3, $4, 'Active') RETURNING id`, meteringPointID, customerID, start, end)
}

// TestGetEnergyStatsSummary_CountsDeltasAndConsumption pins
// EnergyStatsEndpoints.Summary's counts, as-of-period-start deltas, and the
// current-vs-previous-period consumption sum (:39-75), all in one scenario:
// a metering point old enough to predate the default 30-day period, a
// second one created inside it, and one consumption interval in each of the
// current and immediately preceding periods.
func TestGetEnergyStatsSummary_CountsDeltasAndConsumption(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	old := createMeteringPoint(t, c)
	previousConsumptionStart := h.Now().Add(-time.Hour)
	addConsumption(t, c, old.Id, previousConsumptionStart, previousConsumptionStart.Add(time.Hour), 2)

	h.Advance(31 * 24 * time.Hour) // old.createdAt now predates the default period's "from"
	// The session SignIn minted above is now older than SESSION_ABSOLUTE_LIFETIME
	// (24h default): sign in again rather than reuse a session the clock jump
	// has expired.
	c = h.SignIn(t, allEnergyPermissions...)
	createMeteringPoint(t, c)

	currentConsumptionStart := h.Now().Add(-time.Hour)
	addConsumption(t, c, old.Id, currentConsumptionStart, currentConsumptionStart.Add(time.Hour), 3)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary energyStatsSummaryJSON
	r.JSON(&summary)

	if summary.MeteringPointCount != 2 {
		t.Errorf("MeteringPointCount = %d, want 2", summary.MeteringPointCount)
	}
	if summary.MeteringPointCountDelta != 1 {
		t.Errorf("MeteringPointCountDelta = %d, want 1 (only the newer point was created inside the period)", summary.MeteringPointCountDelta)
	}
	if summary.ConsumptionKwh != 3 {
		t.Errorf("ConsumptionKwh = %v, want 3", summary.ConsumptionKwh)
	}
	if summary.PreviousConsumptionKwh != 2 {
		t.Errorf("PreviousConsumptionKwh = %v, want 2", summary.PreviousConsumptionKwh)
	}
	if summary.ConsumptionKwhDelta != 1 {
		t.Errorf("ConsumptionKwhDelta = %v, want 1 (3 - 2)", summary.ConsumptionKwhDelta)
	}
	if !summary.From.Before(summary.To) {
		t.Errorf("From = %v, To = %v, want From before To", summary.From, summary.To)
	}
}

// TestGetEnergyStatsSummary_InvalidPeriodIsRejected pins TryNormalizePeriod's
// 400 (:136-147): from after to.
func TestGetEnergyStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/summary?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid period")
	}
}

// TestGetEnergyStatsTimeseries_BucketsByUTCCalendarDate pins
// EnergyStatsEndpoints.Timeseries's grouping (:97): item.Start.Date, the
// *UTC* calendar date of Start — unlike the two aggregate endpoints, this is
// not timezone-aware per metering point (energy inventory §1.1 line 64).
func TestGetEnergyStatsTimeseries_BucketsByUTCCalendarDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	day1 := utc(2026, time.June, 1, 10)
	addConsumption(t, c, point.Id, day1, day1.Add(time.Hour), 2)
	addConsumption(t, c, point.Id, day1.Add(3*time.Hour), day1.Add(4*time.Hour), 3)
	day2 := utc(2026, time.June, 2, 10)
	addConsumption(t, c, point.Id, day2, day2.Add(time.Hour), 5)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/timeseries?metric=consumptionKwh&from=2026-06-01T00:00:00Z&to=2026-06-03T00:00:00Z", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []energyStatsDailyBucketJSON
	r.JSON(&buckets)
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}
	if buckets[0].Date != "2026-06-01" || buckets[0].Value != 5 {
		t.Errorf("buckets[0] = %+v, want date 2026-06-01 value 5 (2 + 3)", buckets[0])
	}
	if buckets[1].Date != "2026-06-02" || buckets[1].Value != 5 {
		t.Errorf("buckets[1] = %+v, want date 2026-06-02 value 5", buckets[1])
	}
}

// TestGetEnergyStatsTimeseries_MetricIsCaseInsensitive pins
// string.Equals(metric, "consumptionKwh", OrdinalIgnoreCase) (:86).
func TestGetEnergyStatsTimeseries_MetricIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/timeseries?metric=CONSUMPTIONKWH", nil)
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestGetEnergyStatsTimeseries_InvalidMetricIsRejected pins the exact
// detail text of the metric-check 400 (:89-93).
func TestGetEnergyStatsTimeseries_InvalidMetricIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/timeseries?metric=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid metric" || problem.Detail != "Metric must be: consumptionKwh." {
		t.Errorf("problem = %+v, want title %q detail %q", problem, "Invalid metric", "Metric must be: consumptionKwh.")
	}
}

// TestGetEnergyStatsTimeseries_InvalidPeriodRunsBeforeInvalidMetric pins the
// order Timeseries checks its two 400 conditions (:87-93): a request that is
// both an invalid period and an invalid metric must answer "Invalid period".
func TestGetEnergyStatsTimeseries_InvalidPeriodRunsBeforeInvalidMetric(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/timeseries?metric=bogus&from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q (the period check must run first)", problem.Title, "Invalid period")
	}
}

// TestGetEnergyStatsAttention_ListsActivePeriodsExpiringWithinThirtyDays
// pins Attention's hard-coded rule (:110-114): only Active periods whose end
// falls within [now, now+30d] appear; one further out does not.
func TestGetEnergyStatsAttention_ListsActivePeriodsExpiringWithinThirtyDays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	// Two different metering points: the GiST exclusion constraint
	// (energy inventory §3.2) still applies to a direct SQL insert, so two
	// overlapping Active periods on the *same* metering point would 23P01
	// against each other regardless of the bypass above.
	soon := createMeteringPoint(t, c)
	far := createMeteringPoint(t, c)

	insertActiveSupplyPeriod(t, h, soon.Id, 1001, h.Now().Add(-48*time.Hour), h.Now().AddDate(0, 0, 10))
	insertActiveSupplyPeriod(t, h, far.Id, 1002, h.Now().Add(-48*time.Hour), h.Now().AddDate(0, 0, 40))

	r := c.Do(http.MethodGet, "/api/v1/energy/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []energyStatsAttentionItemJSON
	r.JSON(&items)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (only the period expiring within 30 days)", len(items))
	}
	if items[0].Type != "supplyPeriodExpiring" {
		t.Errorf("Type = %q, want %q", items[0].Type, "supplyPeriodExpiring")
	}
	if items[0].EntityId != strconv.Itoa(int(soon.Id)) {
		t.Errorf("EntityId = %q, want %q", items[0].EntityId, strconv.Itoa(int(soon.Id)))
	}
}
