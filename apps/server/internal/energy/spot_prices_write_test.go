package energy_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the one write of spot prices as the spot-price service uses
// it (spot-price service design S6, plan reading 7): a preliminary day
// turning final and a version bump replace the stored intervals, a day at
// another resolution replaces a fetched day whole, and an imported day is
// never overwritten.

const writeTestDay = "2026-09-10"

// serviceDayRows is area's writeTestDay in Oslo at step, every interval's
// NOK price nok, with the provenance given.
func serviceDayRows(t *testing.T, area string, step time.Duration, nok float64, state string, version int32) []energy.SpotPriceRow {
	t.Helper()
	d, err := time.Parse(time.DateOnly, writeTestDay)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, osloZone)
	end := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, osloZone)
	upstream, rate := "nordpool_portal", "nordpool"
	var rows []energy.SpotPriceRow
	for at := start; at.Before(end); at = at.Add(step) {
		eur, exr := nok/11.5, 11.5
		v := version
		rows = append(rows, energy.SpotPriceRow{
			PriceArea: area, Start: at.UTC(), End: at.Add(step).UTC(), NokPerKwh: nok, EurPerKwh: &eur, ExchangeRate: &exr,
			State: state, Upstream: &upstream, RateSource: &rate, UpstreamVersion: &v,
		})
	}
	return rows
}

func writeServiceDay(t *testing.T, h *modtest.Harness, rows []energy.SpotPriceRow) energy.SpotPriceWriteCounts {
	t.Helper()
	got, err := energy.WriteSpotPriceDaysForTest(context.Background(), h.Pool(), rows[0].PriceArea, rows, h.Now())
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return got
}

// storedDay counts area's intervals of writeTestDay with the given source
// and state.
func storedDay(t *testing.T, h *modtest.Harness, area, source, state string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = $1 AND source = $2 AND state = $3
	                    AND "start" >= '2026-09-10T00:00:00+02:00' AND "start" < '2026-09-11T00:00:00+02:00'`, area, source, state)
}

func TestSpotPriceWrite_APreliminaryDayTurningFinalIsReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	quarter := 15 * time.Minute
	if got := writeServiceDay(t, h, serviceDayRows(t, "NO1", quarter, 0.8, "preliminary", 1)); got != (energy.SpotPriceWriteCounts{Inserted: 96}) {
		t.Fatalf("the preliminary day = %+v, want 96 inserted", got)
	}
	if got := writeServiceDay(t, h, serviceDayRows(t, "NO1", quarter, 0.8, "final", 1)); got != (energy.SpotPriceWriteCounts{Replaced: 96}) {
		t.Errorf("the same figures turned final = %+v, want all 96 replaced", got)
	}
	if n := storedDay(t, h, "NO1", "Service", "final"); n != 96 {
		t.Errorf("%d final Service intervals stored, want 96", n)
	}
	if got := writeServiceDay(t, h, serviceDayRows(t, "NO1", quarter, 0.8, "final", 1)); got != (energy.SpotPriceWriteCounts{Unchanged: 96}) {
		t.Errorf("the same final day again = %+v, want all 96 unchanged", got)
	}
}

func TestSpotPriceWrite_AVersionBumpIsReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	quarter := 15 * time.Minute
	writeServiceDay(t, h, serviceDayRows(t, "NO2", quarter, 0.8, "final", 3))
	if got := writeServiceDay(t, h, serviceDayRows(t, "NO2", quarter, 0.8, "final", 4)); got != (energy.SpotPriceWriteCounts{Replaced: 96}) {
		t.Errorf("version 4 of equal figures = %+v, want all 96 replaced", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO2' AND upstream_version = 4
	                     AND upstream = 'nordpool_portal' AND rate_source = 'nordpool'`); n != 96 {
		t.Errorf("%d intervals carry version 4 and its provenance, want 96", n)
	}
	if got := writeServiceDay(t, h, serviceDayRows(t, "NO2", quarter, 0.81, "final", 4)); got != (energy.SpotPriceWriteCounts{Replaced: 96}) {
		t.Errorf("a corrected figure at the same version = %+v, want all 96 replaced", got)
	}
}

func TestSpotPriceWrite_QuarterHoursReplaceAFeedDayOfHours(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// The feed's day in hours, and the next day's first hour, which the
	// replacement leaves alone.
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           SELECT 'NO3', s, s + interval '1 hour', 0.5, 'Feed', now()
	             FROM generate_series('2026-09-10T00:00:00+02:00'::timestamptz, '2026-09-11T00:00:00+02:00'::timestamptz, interval '1 hour') s`)
	got := writeServiceDay(t, h, serviceDayRows(t, "NO3", 15*time.Minute, 0.8, "final", 1))
	if got != (energy.SpotPriceWriteCounts{Inserted: 96, Deleted: 24}) {
		t.Errorf("quarter-hours over the feed's hours = %+v, want 24 deleted and 96 inserted", got)
	}
	if n := storedDay(t, h, "NO3", "Service", "final"); n != 96 {
		t.Errorf("%d Service quarter-hours stored, want 96", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO3' AND source = 'Feed'`); n != 1 {
		t.Errorf("%d Feed hours left, want only the next day's first", n)
	}
}

func TestSpotPriceWrite_AnImportedDayIsNeverOverwrittenAtAnotherResolution(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           SELECT 'NO4', s, s + interval '1 hour', 0.5, 'Import', now()
	             FROM generate_series('2026-09-10T00:00:00+02:00'::timestamptz, '2026-09-10T23:00:00+02:00'::timestamptz, interval '1 hour') s`)
	got := writeServiceDay(t, h, serviceDayRows(t, "NO4", 15*time.Minute, 0.8, "final", 1))
	if got.Conflicts == 0 || got.Deleted != 0 {
		t.Errorf("quarter-hours over an imported day = %+v, want conflicts and nothing deleted", got)
	}
	if n := storedDay(t, h, "NO4", "Import", "final"); n != 24 {
		t.Errorf("%d imported hours left, want all 24", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO4' AND source = 'Service'`); n != 0 {
		t.Errorf("%d Service intervals written, want none", n)
	}
}

// TestSpotPriceWrite_AnImportedIntervalOfTheSameLengthStands pins the other
// half of S6's rule: a file wins over the service at its own resolution too
// — an imported hour the service answers with another figure is left as it
// is and counted unchanged.
func TestSpotPriceWrite_AnImportedIntervalOfTheSameLengthStands(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           SELECT 'NO5', s, s + interval '1 hour', 0.5, 'Import', now()
	             FROM generate_series('2026-09-10T00:00:00+02:00'::timestamptz, '2026-09-10T23:00:00+02:00'::timestamptz, interval '1 hour') s`)
	got := writeServiceDay(t, h, serviceDayRows(t, "NO5", time.Hour, 0.8, "final", 1))
	if got != (energy.SpotPriceWriteCounts{Unchanged: 24}) {
		t.Errorf("hours over the imported hours = %+v, want 24 unchanged", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO5' AND source = 'Import' AND nok_per_kwh = 0.5`); n != 24 {
		t.Errorf("%d imported hours with the file's figure left, want all 24", n)
	}
}
