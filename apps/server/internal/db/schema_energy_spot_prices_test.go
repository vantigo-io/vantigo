package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// energySpotPricesVersion is 00065_energy_spot_prices.sql (energy consumption
// billing design D2): the day-ahead prices per price area and the feed
// worker's record per area and day.
const energySpotPricesVersion = 65

// TestEnergySpotPrices_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what the write paths rest on:
// one price per (area, start), no two prices of one area overlapping — a day
// at another resolution over a stored one is refused, not doubled — an
// interval that must close after it opens, and the two sources and four
// fetch states the CHECKs allow.
func TestEnergySpotPrices_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, energySpotPricesVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if cols := indexColumns(t, ctx, pool, "energy", "ux_spot_prices_area_start"); !equalStrings(cols, []string{"price_area", "start"}) {
		t.Errorf("ux_spot_prices_area_start columns = %v, want [price_area start]", cols)
	}
	var exclusions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'energy' AND t.relname = 'spot_prices' AND c.contype = 'x' AND c.conname = 'spot_prices_no_overlap'`).Scan(&exclusions); err != nil {
		t.Fatalf("count the exclusion constraint: %v", err)
	}
	if exclusions != 1 {
		t.Fatalf("spot_prices_no_overlap: found %d, want 1", exclusions)
	}

	insert := func(area, start, end, source string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
			VALUES ($1, $2, $3, 0.5, $4, now())`, area, start, end, source)
		return err
	}
	if err := insert("NO1", "2026-10-01T00:00:00+02:00", "2026-10-01T01:00:00+02:00", "Feed"); err != nil {
		t.Fatalf("insert an hourly price: %v", err)
	}
	// The next hour is fine; a quarter inside the stored hour is an overlap;
	// another area's quarter is not.
	if err := insert("NO1", "2026-10-01T01:00:00+02:00", "2026-10-01T02:00:00+02:00", "Import"); err != nil {
		t.Errorf("insert the adjacent hour: %v", err)
	}
	if err := insert("NO2", "2026-10-01T00:15:00+02:00", "2026-10-01T00:30:00+02:00", "Feed"); err != nil {
		t.Errorf("insert another area's quarter: %v", err)
	}
	for name, tc := range map[string]struct{ area, start, end, source, code string }{
		"a quarter inside a stored hour":  {"NO1", "2026-10-01T00:15:00+02:00", "2026-10-01T00:30:00+02:00", "Feed", "23P01"},
		"the same start again":            {"NO1", "2026-10-01T00:00:00+02:00", "2026-10-01T00:15:00+02:00", "Feed", "23P01"},
		"an interval that closes first":   {"NO3", "2026-10-01T01:00:00+02:00", "2026-10-01T00:00:00+02:00", "Feed", "23514"},
		"a source that is neither of two": {"NO3", "2026-10-01T00:00:00+02:00", "2026-10-01T01:00:00+02:00", "Elhub", "23514"},
	} {
		err := insert(tc.area, tc.start, tc.end, tc.source)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
			t.Errorf("%s: err = %v, want SQLSTATE %s", name, err, tc.code)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.spot_price_fetches (price_area, day, status, attempts) VALUES ('NO1', '2026-10-01', 'ok', 1)`); err != nil {
		t.Errorf("record a fetch: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO energy.spot_price_fetches (price_area, day, status, attempts) VALUES ('NO1', '2026-10-02', 'done', 1)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("a fetch status outside the four: err = %v, want SQLSTATE 23514", err)
	}
}
