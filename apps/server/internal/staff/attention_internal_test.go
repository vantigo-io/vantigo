package staff

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// countingDB is a store.DBTX that counts the statements sent through it.
type countingDB struct {
	store.DBTX
	n int
}

func (c *countingDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.n++
	return c.DBTX.Exec(ctx, sql, args...)
}

func (c *countingDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.n++
	return c.DBTX.Query(ctx, sql, args...)
}

func (c *countingDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.n++
	return c.DBTX.QueryRow(ctx, sql, args...)
}

// The attention read is every staff:access caller's on every dashboard view,
// so it reads each table once and only what the caller's roles need: a
// linked caller who manages nobody gets [] after the employments, their
// terms and the departments — no people, no contract documents, no ledger —
// a manager adds only their reports' names, and HR the people, the contract
// documents and the ledger.
func TestReadAttention_ReadsOnlyWhatTheCallerNeeds(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Clock: time.Now}}
	today := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)

	one := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	unit := one(`
		WITH le AS (
			INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at)
			VALUES ('Kraft-Verket AS', false, now(), now()) RETURNING id
		)
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		SELECT id, 'Oslo', false, now(), now() FROM le RETURNING id`)
	person := func(number string, manager *int64) int64 {
		t.Helper()
		id := one(`
			INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
			VALUES ($1, $1, 'Test', $1, now(), now()) RETURNING id`, number)
		employment := one(`
			INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date,
				created_at, updated_at)
			VALUES ($1, $2, 'ordinary', $3, '2025-01-01', now(), now()) RETURNING id`, id, "E-"+number, unit)
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
				full_time_hours_per_week, manager_person_id, probation_end_date, created_at, updated_at)
			VALUES ($1, '2025-01-01', 'fast', 'fast', 100, 37.5, $2, '2026-10-20', now(), now())`, employment, manager); err != nil {
			t.Fatalf("seed terms: %v", err)
		}
		return id
	}
	manager := person("M1", nil)
	person("R1", &manager)
	nobody := person("N1", nil)

	for _, tc := range []struct {
		name    string
		caller  callerContext
		queries int
		items   int
	}{
		{"a linked caller who manages nobody", callerContext{Person: &nobody, Today: today}, 3, 0},
		{"a manager", callerContext{Person: &manager, Today: today}, 4, 1},
		{"staff:manage", callerContext{Manage: true, Today: today}, 6, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			db := &countingDB{DBTX: tx}
			items, err := s.readAttention(ctx, store.New(db), &tc.caller, map[string]time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if db.n != tc.queries {
				t.Errorf("%d statements, want %d", db.n, tc.queries)
			}
			if items == nil {
				t.Error("items = nil, want a list, [] when empty")
			}
			if tc.items >= 0 && len(items) != tc.items {
				t.Errorf("items = %+v, want %d", items, tc.items)
			}
		})
	}
}
