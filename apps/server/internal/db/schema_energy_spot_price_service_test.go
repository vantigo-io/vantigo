package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// energySpotPriceServiceVersion is 00075_energy_spot_price_service.sql
// (spot-price service design S2): the Service source, a price's state and
// provenance, and the history walk per area.
const energySpotPriceServiceVersion = 75

// TestEnergySpotPriceService_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the sync rests
// on: a Service row with its provenance, a row stored before the migration
// final, the two states and the three sources the CHECKs allow, and one
// history row per area — active unless told otherwise, with the last live
// day read.
func TestEnergySpotPriceService_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, energySpotPriceServiceVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
		VALUES ('NO1', '2026-10-01T00:00:00+02:00', '2026-10-01T01:00:00+02:00', 0.5, 'Feed', now())`); err != nil {
		t.Fatalf("insert a feed price: %v", err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM energy.spot_prices WHERE source = 'Feed'`).Scan(&state); err != nil || state != "final" {
		t.Errorf("a row without a state: state = %q, err = %v, want final", state, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, eur_per_kwh, exchange_rate, source, received_at,
		                                state, upstream, rate_source, upstream_version)
		VALUES ('NO1', '2026-10-02T00:00:00+02:00', '2026-10-02T00:15:00+02:00', 0.78726, 0.07523, 10.46476, 'Service', now(),
		        'preliminary', 'nordpool_portal', 'nordpool', 3)`); err != nil {
		t.Fatalf("insert a service price: %v", err)
	}
	for name, tc := range map[string]struct{ source, state string }{
		"a source that is none of three": {"Elhub", "final"},
		"a state that is neither of two": {"Service", "draft"},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at, state)
			VALUES ('NO2', '2026-10-01T00:00:00+02:00', '2026-10-01T01:00:00+02:00', 0.5, $1, now(), $2)`, tc.source, tc.state)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("%s: err = %v, want SQLSTATE 23514", name, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.spot_price_history (price_area, cursor_day, floor_day, updated_at) VALUES ('NO1', '2026-09-01', '2015-01-01', now())`); err != nil {
		t.Errorf("record a history walk: %v", err)
	}
	var active bool
	var liveThrough *string
	if err := pool.QueryRow(ctx, `SELECT active, live_through::text FROM energy.spot_price_history WHERE price_area = 'NO1'`).Scan(&active, &liveThrough); err != nil ||
		!active || liveThrough != nil {
		t.Errorf("a new history row: active %v, live_through %v, err %v; want active with no live day read", active, liveThrough, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE energy.spot_price_history SET live_through = '2026-10-27', active = false WHERE price_area = 'NO1'`); err != nil {
		t.Errorf("record the live day read and the area inactive: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE energy.spot_price_history SET active = NULL WHERE price_area = 'NO1'`); err == nil {
		t.Error("a null active: updated, want refused")
	}
	for name, sql := range map[string]string{
		"a second row for the area": `INSERT INTO energy.spot_price_history (price_area, updated_at) VALUES ('NO1', now())`,
		"negative skipped days":     `INSERT INTO energy.spot_price_history (price_area, days_skipped, updated_at) VALUES ('NO2', -1, now())`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Errorf("%s: inserted, want refused", name)
		}
	}
}
