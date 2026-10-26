package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// energyTariffsVersion is 00076_energy_tariffs.sql (energy consumption
// billing design D3, D4): tariffs with their versions and components, and the
// billing facts on supply periods and metering points.
const energyTariffsVersion = 76

// TestEnergyTariffs_AppliesAndIsIdempotent proves the migration applies, rolls
// back and re-applies cleanly, and pins what the write paths rest on: no two
// versions of one tariff overlapping (an open-ended one included), the kinds,
// statuses, cadences and tax classes the CHECKs allow, a day window that
// opens before it closes, and a tariff in use that cannot be deleted.
func TestEnergyTariffs_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, energyTariffsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	wantCode := func(name string, err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Errorf("%s: err = %v, want SQLSTATE %s", name, err, code)
		}
	}

	var tariffID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.tariffs (kind, name, created_at, updated_at) VALUES ('power', 'Spot', now(), now())
		RETURNING id`).Scan(&tariffID); err != nil {
		t.Fatalf("insert a tariff: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM energy.tariffs WHERE id = $1`, tariffID).Scan(&status); err != nil || status != "active" {
		t.Errorf("a new tariff's status = %q (%v), want active", status, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO energy.tariffs (kind, name, created_at, updated_at) VALUES ('water', 'x', now(), now())`)
	wantCode("a kind that is neither power nor grid", err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO energy.tariffs (kind, name, status, created_at, updated_at) VALUES ('grid', 'x', 'deleted', now(), now())`)
	wantCode("a status that is neither active nor archived", err, "23514")

	version := func(from string, to any) (int32, error) {
		var id int32
		err := pool.QueryRow(ctx, `
			INSERT INTO energy.tariff_versions (tariff_id, valid_from, valid_to) VALUES ($1, $2::date, $3::date)
			RETURNING id`, tariffID, from, to).Scan(&id)
		return id, err
	}
	versionID, err := version("2026-01-01", "2026-07-01")
	if err != nil {
		t.Fatalf("insert a version: %v", err)
	}
	// The day valid_to names is the next version's first: half-open.
	if _, err := version("2026-07-01", nil); err != nil {
		t.Errorf("insert the adjacent open-ended version: %v", err)
	}
	_, err = version("2027-01-01", "2027-02-01")
	wantCode("a version inside the open-ended one", err, "23P01")
	_, err = version("2025-12-01", "2026-01-02")
	wantCode("a version overlapping the first by a day", err, "23P01")
	_, err = version("2025-01-01", "2025-01-01")
	wantCode("a version that ends where it starts", err, "23514")

	component := func(position int, kind string, dayFrom, dayTo any, tiers any) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO energy.tariff_components (version_id, position, kind, name, rate, day_from, day_to, tiers)
			VALUES ($1, $2, $3, 'x', 0.1, $4, $5, $6::jsonb)`, versionID, position, kind, dayFrom, dayTo, tiers)
		return err
	}
	if err := component(0, "spot_energy", nil, nil, nil); err != nil {
		t.Errorf("insert a component: %v", err)
	}
	if err := component(1, "grid_energy", 6, 22, nil); err != nil {
		t.Errorf("insert a component with a day window: %v", err)
	}
	if err := component(2, "grid_capacity", nil, nil, `[{"fromKw": 0, "perMonth": 135}]`); err != nil {
		t.Errorf("insert a component with tiers: %v", err)
	}
	wantCode("the same position twice", component(0, "markup", nil, nil, nil), "23505")
	wantCode("a kind outside the nine", component(3, "vat", nil, nil, nil), "23514")
	wantCode("a day window that closes first", component(3, "grid_energy", 22, 6, nil), "23514")
	wantCode("a day window past midnight", component(3, "grid_energy", 6, 25, nil), "23514")
	wantCode("half a day window", component(3, "grid_energy", 6, nil, nil), "23514")
	wantCode("tiers that are not an array", component(3, "grid_capacity", nil, nil, `{"fromKw": 0}`), "23514")
	_, err = pool.Exec(ctx, `
		INSERT INTO energy.tariff_components (version_id, position, kind, name, rate) VALUES ($1, 4, 'markup', 'x', -0.01)`, versionID)
	wantCode("a negative rate", err, "23514")

	var pointID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.metering_points (gsrn, street_address, postal_code, city, country_code, price_area, connection_status, created_at, updated_at)
		VALUES ('707057500000000001', 'Storgata 1', '9008', 'Tromsø', 'NO', 'NO4', 'New', now(), now())
		RETURNING id`).Scan(&pointID); err != nil {
		t.Fatalf("insert a metering point: %v", err)
	}
	var household bool
	var taxClass string
	if err := pool.QueryRow(ctx, `SELECT household, electricity_tax_class FROM energy.metering_points WHERE id = $1`, pointID).Scan(&household, &taxClass); err != nil {
		t.Fatalf("read the point's billing facts: %v", err)
	}
	if household || taxClass != "ordinary" {
		t.Errorf("a point's defaults = household %v, class %q; want false, ordinary", household, taxClass)
	}
	_, err = pool.Exec(ctx, `UPDATE energy.metering_points SET electricity_tax_class = 'free' WHERE id = $1`, pointID)
	wantCode("a tax class outside the three", err, "23514")

	var cadence string
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status, tariff_id)
		VALUES ($1, 1, '2026-01-01T00:00:00Z', 'Active', $2) RETURNING cadence`, pointID, tariffID).Scan(&cadence); err != nil {
		t.Fatalf("insert a supply period with a tariff: %v", err)
	}
	if cadence != "monthly" {
		t.Errorf("a supply period's default cadence = %q, want monthly", cadence)
	}
	_, err = pool.Exec(ctx, `UPDATE energy.supply_periods SET cadence = 'weekly' WHERE metering_point_id = $1`, pointID)
	wantCode("a cadence outside the three", err, "23514")

	_, err = pool.Exec(ctx, `DELETE FROM energy.tariffs WHERE id = $1`, tariffID)
	wantCode("deleting a tariff a supply period names", err, "23001")

	var gridID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.tariffs (kind, name, created_at, updated_at) VALUES ('grid', 'Nettleie', now(), now())
		RETURNING id`).Scan(&gridID); err != nil {
		t.Fatalf("insert a grid tariff: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE energy.metering_points SET grid_tariff_id = $1 WHERE id = $2`, gridID, pointID); err != nil {
		t.Fatalf("assign the grid tariff: %v", err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM energy.tariffs WHERE id = $1`, gridID)
	wantCode("deleting a tariff a metering point names", err, "23001")

	// A tariff nobody names deletes with its versions and components.
	if _, err := pool.Exec(ctx, `UPDATE energy.supply_periods SET tariff_id = NULL WHERE metering_point_id = $1`, pointID); err != nil {
		t.Fatalf("unassign the power tariff: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM energy.tariffs WHERE id = $1`, tariffID); err != nil {
		t.Fatalf("delete the unused tariff: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM energy.tariff_components WHERE version_id = $1`, versionID).Scan(&left); err != nil || left != 0 {
		t.Errorf("components left after the delete = %d (%v), want 0", left, err)
	}
}
