package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// energySettlementsVersion is 00077_energy_settlements.sql (energy
// consumption billing design D5): settlements and their lines.
const energySettlementsVersion = 77

// TestEnergySettlements_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what the provider and the
// invoiced-work holder rest on: one live settlement per (supply period,
// period, kind) while a released one is forgotten, the stamp whole exactly
// when invoiced, a correction naming its original, the blockers and units the
// CHECKs allow, and a supply period with settlements that cannot be deleted.
func TestEnergySettlements_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, energySettlementsVersion)

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

	var pointID, periodID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.metering_points (gsrn, street_address, postal_code, city, country_code, price_area, connection_status, created_at, updated_at)
		VALUES ('707057500077000001', 'Gata 1', '0150', 'Oslo', 'NO', 'NO1', 'Connected', now(), now())
		RETURNING id`).Scan(&pointID); err != nil {
		t.Fatalf("insert a metering point: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, status)
		VALUES ($1, 42, '2026-01-01T00:00:00+01:00', 'Active') RETURNING id`, pointID).Scan(&periodID); err != nil {
		t.Fatalf("insert a supply period: %v", err)
	}

	insert := func(kind string, correctsID any, start, state string, stamped bool, blocked any) (int64, error) {
		var id int64
		var invoiceID, number, invoicedAt, invoiceDate, releasedAt any
		if stamped {
			invoiceID, number, invoicedAt, invoiceDate = 7001, 10001, "2026-02-03T10:00:00Z", "2026-02-03"
		}
		if state == "released" {
			releasedAt = "2026-03-01T10:00:00Z"
		}
		err := pool.QueryRow(ctx, `
			INSERT INTO energy.settlements (supply_period_id, metering_point_id, customer_id, kind, corrects_id,
				period_start, period_end, state, kwh, interval_count, expected_interval_count, blocked, amount,
				computed_at, touched_at, created_at, updated_at,
				invoiced_invoice_id, invoiced_number, invoiced_at, invoice_date, released_at)
			VALUES ($1, $2, 42, $3, $4, $5::timestamptz, $5::timestamptz + interval '1 month', $6, 100, 744, 744, $7, 120.5,
				now(), now(), now(), now(), $8, $9, $10::timestamptz, $11::date, $12::timestamptz)
			RETURNING id`,
			periodID, pointID, kind, correctsID, start, state, blocked,
			invoiceID, number, invoicedAt, invoiceDate, releasedAt).Scan(&id)
		return id, err
	}

	january := "2026-01-01T00:00:00+01:00"
	regular, err := insert("regular", nil, january, "draft", false, nil)
	if err != nil {
		t.Fatalf("insert a draft: %v", err)
	}
	_, err = insert("regular", nil, january, "draft", false, nil)
	wantCode("a second live regular settlement of one period", err, "23505")
	_, err = insert("regular", nil, january, "invoiced", true, nil)
	wantCode("an invoiced regular beside the live draft", err, "23505")
	_, err = insert("regular", nil, "2026-02-01T00:00:00+01:00", "invoiced", false, nil)
	wantCode("an invoiced settlement without its stamp", err, "23514")
	_, err = insert("regular", nil, "2026-02-01T00:00:00+01:00", "draft", true, nil)
	wantCode("a draft carrying a stamp", err, "23514")
	_, err = insert("correction", nil, "2026-02-01T00:00:00+01:00", "draft", false, nil)
	wantCode("a correction naming no original", err, "23514")
	_, err = insert("regular", regular, "2026-02-01T00:00:00+01:00", "draft", false, nil)
	wantCode("a regular settlement naming an original", err, "23514")
	_, err = insert("regular", nil, "2026-02-01T00:00:00+01:00", "draft", false, "price_unknown")
	wantCode("a blocker the module does not know", err, "23514")
	if _, err := insert("regular", nil, "2026-03-01T00:00:00+01:00", "draft", false, "price_preliminary"); err != nil {
		t.Errorf("insert a draft blocked price_preliminary: %v", err)
	}

	// A correction lives beside the regular settlement of its period; a
	// second live one waits until the first is invoiced or released.
	if _, err := insert("correction", regular, january, "draft", false, nil); err != nil {
		t.Errorf("insert a correction beside its period's regular settlement: %v", err)
	}
	_, err = insert("correction", regular, january, "draft", false, nil)
	wantCode("a second correction draft of one period", err, "23505")
	// An invoiced correction is a baseline, not a slot: the next one is
	// drafted beside it.
	if _, err := insert("correction", regular, "2026-03-01T00:00:00+01:00", "invoiced", true, nil); err != nil {
		t.Fatalf("insert an invoiced correction: %v", err)
	}
	if _, err := insert("correction", regular, "2026-03-01T00:00:00+01:00", "draft", false, nil); err != nil {
		t.Errorf("draft a correction beside an invoiced one: %v", err)
	}

	// A released settlement is forgotten by the live index: the period is
	// drafted afresh.
	if _, err := pool.Exec(ctx, `
		UPDATE energy.settlements SET state = 'released', released_at = now(), invoiced_invoice_id = 7001,
			invoiced_number = 10001, invoiced_at = now(), invoice_date = '2026-02-03'
		WHERE id = $1`, regular); err != nil {
		t.Fatalf("release the regular settlement: %v", err)
	}
	if _, err := insert("regular", nil, january, "draft", false, nil); err != nil {
		t.Errorf("draft a released period afresh: %v", err)
	}

	line := func(position int, kind, unit, band, vat string) error {
		var b any
		if band != "" {
			b = band
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO energy.settlement_lines (settlement_id, position, kind, text, band, quantity, unit, unit_price, amount, vat_hint)
			VALUES ($1, $2, $3, 'x', $4, 100, $5, 1.2, -120, $6)`, regular, position, kind, b, unit, vat)
		return err
	}
	if err := line(0, "grid_energy", "kWh", "night", "exempt_household_north"); err != nil {
		t.Errorf("insert a line: %v", err)
	}
	if err := line(1, "enova_levy", "year", "", "standard"); err != nil {
		t.Errorf("insert a line priced per year: %v", err)
	}
	wantCode("a line at a position already taken", line(1, "markup", "kWh", "", "standard"), "23505")
	wantCode("a line kind the module does not know", line(2, "correction", "kWh", "", "standard"), "23514")
	wantCode("a unit the module does not know", line(2, "markup", "hour", "", "standard"), "23514")
	wantCode("a band that is neither day nor night", line(2, "grid_energy", "kWh", "evening", "standard"), "23514")
	wantCode("a VAT hint the module does not know", line(2, "markup", "kWh", "", "zero"), "23514")

	_, err = pool.Exec(ctx, `DELETE FROM energy.supply_periods WHERE id = $1`, periodID)
	wantCode("deleting a supply period with settlements", err, "23001")
}
