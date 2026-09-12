package energy_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// utc is EnergyEndpointsTests.Utc: a UTC DateTimeOffset built from calendar
// fields, defaulting minutes to zero.
func utc(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}

// addConsumption adds a manual consumption interval through the endpoint
// itself, mirroring EnergyEndpointsTests.AddConsumptionAsync.
func addConsumption(t *testing.T, c *modtest.Client, pointID int32, start, end time.Time, quantity float64) {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", pointID),
		map[string]any{"start": start, "end": end, "quantityKwh": quantity})
	if r.Status != http.StatusOK {
		t.Fatalf("add consumption: status %d body %s, want 200", r.Status, r.Body)
	}
}

// addElhubConsumption inserts a current, Estimated/Elhub consumption row
// directly via SQL, bypassing AddManualConsumptionEndpoint entirely —
// EnergyEndpointsTests.AddElhubConsumptionAsync's own bypass, which exists
// only as a test seam (energy inventory §8 oddity 6): production traffic
// never writes Source=Elhub through any endpoint in this module. It also
// deliberately skips the supersede/exact-tuple dedup path, so back-to-back,
// non-overlapping, non-deduplicated intervals can be seeded directly — the
// DST test below depends on inserting 23 of them for the same metering
// point without any of AddManualConsumption's own logic interfering.
func addElhubConsumption(t *testing.T, h *modtest.Harness, pointID int32, start, end time.Time, quantity float64) {
	t.Helper()
	h.Exec(t, `INSERT INTO energy.consumption_intervals (metering_point_id, start, "end", quantity_kwh, quality, source, received_at)
	           VALUES ($1, $2, $3, $4, 'Estimated', 'Elhub', $5)`, pointID, start, end, quantity, h.Now())
}

// Ported from EnergyEndpointsTests.Manual_consumption_supersedes_current_revision.
func TestPostConsumption_ManualConsumptionSupersedesCurrentRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := utc(2026, time.January, 5, 1)

	addConsumption(t, c, point.Id, start, start.Add(time.Hour), 2.5)
	firstID := modtest.One[int64](t, h,
		`SELECT id FROM energy.consumption_intervals WHERE metering_point_id=$1 AND start=$2 AND is_current`, point.Id, start)

	addConsumption(t, c, point.Id, start, start.Add(time.Hour), 3.5)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionJSON
	r.JSON(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the superseded row must be invisible)", len(rows))
	}
	if rows[0].QuantityKwh != 3.5 {
		t.Errorf("QuantityKwh = %v, want 3.5 (the second write)", rows[0].QuantityKwh)
	}

	// The supersede is a revision chain, not a delete-and-replace: the
	// current row's supersedes_id points at the old row, and the old row
	// still exists with is_current flipped false (energy inventory §2.4) —
	// a mutation that deleted the old row outright instead of chaining it
	// would still pass the API-level assertions above but fail these.
	supersedesID := modtest.One[int64](t, h,
		`SELECT supersedes_id FROM energy.consumption_intervals WHERE metering_point_id=$1 AND start=$2 AND is_current`, point.Id, start)
	if supersedesID != firstID {
		t.Errorf("supersedes_id = %d, want %d (the first write's id)", supersedesID, firstID)
	}
	if got := h.Count(t, `SELECT count(*) FROM energy.consumption_intervals WHERE id=$1 AND NOT is_current`, firstID); got != 1 {
		t.Errorf("non-current rows with id %d = %d, want 1 (the superseded row must still exist, just hidden)", firstID, got)
	}
}

// TestPostConsumption_RejectsInvalidInterval pins ConsumptionInterval.Validate's
// byte-exact messages under field "interval" and 404 for a missing metering
// point — no overlap check runs here (this task's dispatch correction 2): a
// second, non-matching interval on the same metering point is accepted
// without complaint.
func TestPostConsumption_RejectsInvalidInterval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := utc(2026, time.January, 5, 1)

	t.Run("end before start", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", point.Id),
			map[string]any{"start": start, "end": start.Add(-time.Hour), "quantityKwh": 1})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if problem.Title != "Invalid consumption interval" {
			t.Errorf("Title = %q, want %q", problem.Title, "Invalid consumption interval")
		}
		if got := problem.Errors["interval"]; len(got) != 1 || got[0] != "End must be later than start." {
			t.Errorf("Errors[interval] = %v, want the end-before-start message", got)
		}
	})

	t.Run("negative quantity", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", point.Id),
			map[string]any{"start": start, "end": start.Add(time.Hour), "quantityKwh": -1})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if got := problem.Errors["interval"]; len(got) != 1 || got[0] != "Quantity must be zero or greater." {
			t.Errorf("Errors[interval] = %v, want the negative-quantity message", got)
		}
	})

	t.Run("validates before existence", func(t *testing.T) {
		t.Parallel()
		// An invalid interval against a nonexistent metering point must still
		// answer 400, never 404 (energy inventory §1.1 line 36: "validate
		// before existence", the same order as the POST above).
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/consumption",
			map[string]any{"start": start, "end": start.Add(-time.Hour), "quantityKwh": 1})
		if r.Status != http.StatusBadRequest {
			t.Errorf("status %d body %s, want 400 (validation must run before the existence check)", r.Status, r.Body)
		}
	})

	t.Run("missing metering point", func(t *testing.T) {
		t.Parallel()
		r := c.Do(http.MethodPost, "/api/v1/energy/metering-points/999999/consumption",
			map[string]any{"start": start, "end": start.Add(time.Hour), "quantityKwh": 1})
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404", r.Status, r.Body)
		}
	})
}

// TestPostConsumption_NoOverlapProtection is the explicit negative test for
// energy inventory §2.4/§8 oddity 6 (this task's dispatch correction 2): two
// different, overlapping (start,end) intervals are both accepted — there is
// no exclusion constraint and no application check, unlike supply periods.
// A one-line "helpful" overlap guard added while porting would fail this
// test by rejecting the second interval.
func TestPostConsumption_NoOverlapProtection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := utc(2026, time.January, 5, 1)

	addConsumption(t, c, point.Id, start, start.Add(2*time.Hour), 1)
	addConsumption(t, c, point.Id, start.Add(time.Hour), start.Add(3*time.Hour), 2)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionJSON
	r.JSON(&rows)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (both overlapping intervals must be kept, not rejected)", len(rows))
	}
}

// Ported from GetConsumptionEndpoint's to<=from check (GetConsumptionEndpoint.cs:14-15).
func TestGetConsumption_RangeInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	from := utc(2026, time.January, 2, 0)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption?from=%s&to=%s",
		point.Id, from.Format(time.RFC3339), from.Format(time.RFC3339)), nil) // to == from: rejected, not just to < from
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid interval" || problem.Detail != "to must be later than from." {
		t.Errorf("problem = %+v, want title %q detail %q", problem, "Invalid interval", "to must be later than from.")
	}
}

// TestGetConsumption_RangeInvalidRunsBeforeExistence pins
// GetConsumptionEndpoint.cs's order (energy inventory §1.1 line 35):
// "interval sanity before existence" — a bad range against a nonexistent
// metering point still answers 400, never 404.
func TestGetConsumption_RangeInvalidRunsBeforeExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	from := utc(2026, time.January, 2, 0)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/999999/consumption?from=%s&to=%s",
		from.Format(time.RFC3339), from.Format(time.RFC3339)), nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (interval sanity must run before the existence check)", r.Status, r.Body)
	}
}

func TestGetConsumption_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodGet, "/api/v1/energy/metering-points/999999/consumption", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetConsumption_QueryDatesWithoutUTCOffsetAnswer400 pins this task's
// recorded divergence from .NET (energy inventory §8 oddity 8, this task's
// dispatch correction 5): a query-string from/to with no UTC offset falls
// back to the server's local time zone in .NET's own DateTimeOffset.Parse
// semantics, silently accepted; the Go port has no such fallback and refuses
// the request instead. The divergence is scoped to query parameters only —
// request bodies (e.g. ManualConsumptionRequest.start/end) are already
// explicitly UTC-checked by ConsumptionInterval.Validate, both here and in
// .NET, so there is no equivalent divergence to record for bodies.
func TestGetConsumption_QueryDatesWithoutUTCOffsetAnswer400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption?from=2026-01-01T00:00:00", point.Id), nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (an offset-less query-string date-time must be refused, not defaulted)", r.Status, r.Body)
	}
}

// Ported from EnergyEndpointsTests.Consumption_aggregate_uses_oslo_day_and_month_boundaries.
func TestConsumptionAggregate_UsesOsloDayAndMonthBoundaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	// 23:30Z->00:30Z, the exact worked case energy inventory §4 line 345
	// quotes: Oslo local 00:30->01:30, landing entirely in the local day
	// that starts at 23:00Z. Fix-round finding: the original fixture used
	// 23:00Z, an interval starting exactly *at* local midnight — a weaker
	// case that never exercises date_trunc actually flooring a non-midnight
	// local instant down to the local day boundary.
	addConsumption(t, c, point.Id, utc(2026, time.January, 5, 23).Add(30*time.Minute), utc(2026, time.January, 6, 0).Add(30*time.Minute), 1)
	addConsumption(t, c, point.Id, utc(2026, time.January, 5, 23).Add(30*time.Minute).AddDate(0, 0, 1), utc(2026, time.January, 6, 0).Add(30*time.Minute).AddDate(0, 0, 1), 2)
	addConsumption(t, c, point.Id, utc(2026, time.February, 1, 0), utc(2026, time.February, 1, 1), 4)

	daily := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z&resolution=day", point.Id), nil)
	if daily.Status != http.StatusOK {
		t.Fatalf("daily: status %d body %s, want 200", daily.Status, daily.Body)
	}
	var dailyRows []consumptionAggregateJSON
	daily.JSON(&dailyRows)
	if len(dailyRows) != 3 {
		t.Fatalf("daily rows = %d, want 3", len(dailyRows))
	}
	if dailyRows[0].QuantityKwh != 1 {
		t.Errorf("daily[0].QuantityKwh = %v, want 1", dailyRows[0].QuantityKwh)
	}
	wantBucketStart := time.Date(2026, time.January, 5, 23, 0, 0, 0, time.UTC)
	if !dailyRows[0].BucketStart.Equal(wantBucketStart) {
		t.Errorf("daily[0].BucketStart = %v, want %v (23:30Z-00:30Z lands entirely in the earlier Oslo local day)", dailyRows[0].BucketStart, wantBucketStart)
	}
	if dailyRows[1].QuantityKwh != 2 {
		t.Errorf("daily[1].QuantityKwh = %v, want 2", dailyRows[1].QuantityKwh)
	}

	monthly := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z&resolution=month", point.Id), nil)
	if monthly.Status != http.StatusOK {
		t.Fatalf("monthly: status %d body %s, want 200", monthly.Status, monthly.Body)
	}
	var monthlyRows []consumptionAggregateJSON
	monthly.JSON(&monthlyRows)
	if len(monthlyRows) != 2 {
		t.Fatalf("monthly rows = %d, want 2 (the two January entries collapse into one bucket)", len(monthlyRows))
	}
	if monthlyRows[0].QuantityKwh != 3 || monthlyRows[1].QuantityKwh != 4 {
		t.Errorf("monthly quantities = [%v, %v], want [3, 4]", monthlyRows[0].QuantityKwh, monthlyRows[1].QuantityKwh)
	}
}

// Ported from EnergyEndpointsTests.Consumption_aggregate_marks_estimated_and_handles_oslo_dst_day.
// 23 back-to-back, one-hour, Estimated/Elhub intervals — inserted directly,
// bypassing the endpoint's supersede/dedup path entirely (energy inventory
// §8 oddity 6) — fold into one "day" bucket that is 23 hours of UTC, not 24,
// because Oslo's spring-forward DST transition falls inside it.
func TestConsumptionAggregate_MarksEstimatedAndHandlesOsloDstDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	base := utc(2026, time.March, 28, 23)
	for hour := 0; hour < 23; hour++ {
		addElhubConsumption(t, h, point.Id, base.Add(time.Duration(hour)*time.Hour), base.Add(time.Duration(hour+1)*time.Hour), 1)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=2026-03-28T23:00:00Z&to=2026-03-29T22:00:00Z&resolution=day", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionAggregateJSON
	r.JSON(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.QuantityKwh != 23 {
		t.Errorf("QuantityKwh = %v, want 23", row.QuantityKwh)
	}
	if row.IntervalCount != 23 {
		t.Errorf("IntervalCount = %d, want 23", row.IntervalCount)
	}
	if !row.HasEstimated {
		t.Error("HasEstimated = false, want true (every row in the bucket is Quality=Estimated)")
	}
	wantStart := time.Date(2026, time.March, 28, 23, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, time.March, 29, 22, 0, 0, 0, time.UTC)
	if !row.BucketStart.Equal(wantStart) {
		t.Errorf("BucketStart = %v, want %v", row.BucketStart, wantStart)
	}
	if !row.BucketEnd.Equal(wantEnd) {
		t.Errorf("BucketEnd = %v, want %v", row.BucketEnd, wantEnd)
	}
	if row.BucketEnd.Sub(row.BucketStart) != 23*time.Hour {
		t.Errorf("bucket span = %v, want 23h (the local Oslo day crossing DST spring-forward is 23 hours of UTC, not 24)", row.BucketEnd.Sub(row.BucketStart))
	}
}

// TestConsumptionAggregate_HandlesOsloAutumnDstDay is the fall-back mirror
// of the spring-forward test above — correct under the same verbatim query
// today, but fix-round finding: untested, so a mutation that only broke the
// "gain an hour" direction (e.g. a bucket-width computation that clamped at
// 24h, or one that only handled the spring case) could ship unnoticed. Last
// Sunday of October 2026 (2026-10-25) is when Europe/Oslo falls back from
// CEST (+02:00) to CET (+01:00) at 01:00Z; the local Oslo day of Oct 25
// therefore runs [2026-10-24T22:00Z, 2026-10-25T23:00Z) — 25 hours of UTC,
// not 24 — the same date_trunc-in-local-time mechanism as the spring case,
// just gaining an hour instead of losing one.
func TestConsumptionAggregate_HandlesOsloAutumnDstDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	base := utc(2026, time.October, 24, 22)
	for hour := 0; hour < 25; hour++ {
		addElhubConsumption(t, h, point.Id, base.Add(time.Duration(hour)*time.Hour), base.Add(time.Duration(hour+1)*time.Hour), 1)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=2026-10-24T22:00:00Z&to=2026-10-25T23:00:00Z&resolution=day", point.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionAggregateJSON
	r.JSON(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.QuantityKwh != 25 {
		t.Errorf("QuantityKwh = %v, want 25", row.QuantityKwh)
	}
	if row.IntervalCount != 25 {
		t.Errorf("IntervalCount = %d, want 25", row.IntervalCount)
	}
	if !row.HasEstimated {
		t.Error("HasEstimated = false, want true (every row in the bucket is Quality=Estimated)")
	}
	wantStart := time.Date(2026, time.October, 24, 22, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, time.October, 25, 23, 0, 0, 0, time.UTC)
	if !row.BucketStart.Equal(wantStart) {
		t.Errorf("BucketStart = %v, want %v", row.BucketStart, wantStart)
	}
	if !row.BucketEnd.Equal(wantEnd) {
		t.Errorf("BucketEnd = %v, want %v", row.BucketEnd, wantEnd)
	}
	if row.BucketEnd.Sub(row.BucketStart) != 25*time.Hour {
		t.Errorf("bucket span = %v, want 25h (the local Oslo day crossing DST fall-back is 25 hours of UTC, not 24)", row.BucketEnd.Sub(row.BucketStart))
	}
}

// TestConsumptionAggregate_ExcludesIntervalStraddlingTo is this task's own
// test for the deliberately asymmetric range filter (energy inventory §4
// line 341-342, this task's dispatch correction 4): WHERE c.start >= @from
// AND c.end <= @to requires full containment, not overlap — an interval
// that starts inside [from,to] but ends after to is excluded entirely, not
// partially counted. A mutation that "fixed" this into a symmetric overlap
// test (c.start < @to) would include the straddling interval and fail this
// assertion.
func TestConsumptionAggregate_ExcludesIntervalStraddlingTo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	from := utc(2026, time.May, 1, 0)
	to := utc(2026, time.May, 2, 0)

	addConsumption(t, c, point.Id, to.Add(-30*time.Minute), to.Add(30*time.Minute), 1) // starts inside, ends after `to`
	addConsumption(t, c, point.Id, from.Add(time.Hour), from.Add(2*time.Hour), 5)      // fully inside the window

	r := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=%s&to=%s&resolution=day",
		point.Id, from.Format(time.RFC3339), to.Format(time.RFC3339)), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var rows []consumptionAggregateJSON
	r.JSON(&rows)
	total := 0.0
	for _, row := range rows {
		total += row.QuantityKwh
	}
	if total != 5 {
		t.Errorf("total quantity = %v, want 5 (the straddling interval must be excluded entirely, not partially counted)", total)
	}
}

// Ported from EnergyEndpointsTests.Consumption_aggregate_rejects_invalid_resolution.
func TestConsumptionAggregate_RejectsInvalidResolution(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodGet, fmt.Sprintf(
		"/api/v1/energy/metering-points/%d/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=week", point.Id), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
}

// TestConsumptionAggregate_CollectsMultipleFieldErrors pins
// ConsumptionAggregateValidation.TryValidate's independent-ifs shape
// (:15-20, energy inventory §1.1 line 37): a request missing both from and
// to and carrying a bad resolution answers all three field errors at once,
// under the ASP.NET Core default ValidationProblemDetails title.
func TestConsumptionAggregate_CollectsMultipleFieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/consumption/aggregate?resolution=week", point.Id), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if problem.Title != "One or more validation errors occurred." {
		t.Errorf("Title = %q, want the ASP.NET Core default ValidationProblemDetails title", problem.Title)
	}
	for _, field := range []string{"from", "to", "resolution"} {
		if len(problem.Errors[field]) == 0 {
			t.Errorf("Errors[%s] is empty, want a message (every check runs regardless of the others)", field)
		}
	}
}

func TestConsumptionAggregate_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodGet,
		"/api/v1/energy/metering-points/999999/consumption/aggregate?from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00Z&resolution=day", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
