package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The staff foundation schema (00042_staff_foundation.sql; staff foundation
// design §3, delivery A1). Its own file, as 00041's tests are.

// staffFoundationVersion is 00042's goose version.
const staffFoundationVersion = 42

// staffTables are the tables 00042 creates.
var staffTables = []string{
	"business_units", "departments", "employment_terms", "employments", "events", "identity_links",
	"legal_entities", "locations", "next_of_kin", "people", "person_roles", "settings",
}

// staffCollect runs one catalogue query that answers a column of text.
func staffCollect(t *testing.T, ctx context.Context, pool *pgxpool.Pool, what, sql string, args ...any) []string {
	t.Helper()
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("query %s: %v", what, err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect %s: %v", what, err)
	}
	return got
}

// TestStaffFoundation_AppliesAndIsIdempotent proves
// 00042_staff_foundation.sql applies, rolls back and re-applies cleanly, and
// pins what delivery A1 rests on: the twelve tables; every column of
// staff.people and staff.settings the phase will need (the later deliveries
// never add one to them); employments without holiday_scheme_id, which is
// delivery B's; the unique indexes that hold the employee number and the
// work email however they are cased, the national id's MAC, the self-service
// link both ways, at most one default per structure table and the one
// settings row; no CHECK constraint anywhere; and the settings row's
// defaults. The Down removes the schema.
func TestStaffFoundation_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffFoundationVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := staffCollect(t, ctx, pool, "tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'staff' ORDER BY 1`)
	if !equalStrings(tables, staffTables) {
		t.Errorf("staff tables = %v, want %v", tables, staffTables)
	}

	columns := func(table string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for _, c := range staffCollect(t, ctx, pool, table+" columns", `
			SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = $1`, table) {
			out[c] = true
		}
		return out
	}
	for table, want := range map[string][]string{
		"people": {
			"id", "employee_number", "given_name", "family_name", "display_name", "birth_date", "gender",
			"national_id_kind", "national_id_sealed", "national_id_mac", "national_id_country", "work_email",
			"phone_work", "phone_mobile", "phone_private", "private_email", "street", "postal_code", "city",
			"country", "work_country", "preferred_language", "bank_account_sealed", "bank_account_last4",
			"bank_account_kind", "children_under_12", "sole_carer", "chronically_ill_children",
			"self_cert_withdrawn_until", "self_cert_withdrawn_reason", "provision_account", "provision_from",
			"account_email_kind", "legal_hold", "legal_hold_reason", "legal_hold_by_user_id", "legal_hold_at",
			"erased_at", "reassessed_at", "reassessed_by_user_id", "notes", "revision", "created_at", "updated_at",
		},
		"settings": {
			"id", "time_zone", "employee_number_prefix", "next_employee_number", "self_cert_policy",
			"self_cert_days_per_occurrence", "self_cert_occurrences_per_year", "self_cert_days_per_year",
			"self_cert_after_months", "auto_carry_over", "create_before_days", "delete_external_after_days",
			"retention_grace_days", "health_retention_days", "pension_provider", "injury_insurer",
			"thresholds_acknowledged", "revision", "updated_at",
		},
		"identity_links": {
			"id", "person_id", "provider", "external_id", "state", "managed", "blocked_reason", "last_error",
			"attempts", "next_attempt_at", "lease_id", "lease_until", "last_synced_at", "last_spec_hash",
		},
		"events": {
			"id", "person_id", "kind", "actor_kind", "actor_user_id", "actor_display", "at", "payload",
			"payload_classes",
		},
	} {
		got := columns(table)
		for _, c := range want {
			if !got[c] {
				t.Errorf("staff.%s has no column %s", table, c)
			}
		}
		if table == "people" || table == "settings" {
			if len(got) != len(want) {
				t.Errorf("staff.%s has %d columns, want exactly the %d of design §3", table, len(got), len(want))
			}
		}
	}
	if columns("employments")["holiday_scheme_id"] {
		t.Error("staff.employments has holiday_scheme_id, which is delivery B's with its key")
	}

	checks := staffCollect(t, ctx, pool, "checks", `
		SELECT conname FROM pg_constraint WHERE connamespace = 'staff'::regnamespace AND contype = 'c'`)
	if len(checks) != 0 {
		t.Errorf("staff has CHECK constraints %v, want none (house style)", checks)
	}

	indexes := map[string]bool{}
	for _, def := range staffCollect(t, ctx, pool, "indexes", `
		SELECT indexdef FROM pg_indexes WHERE schemaname = 'staff'`) {
		indexes[def] = true
	}
	for _, want := range []string{
		"CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people USING btree (upper((employee_number)::text))",
		"CREATE UNIQUE INDEX ux_people_work_email_lower ON staff.people USING btree (lower((work_email)::text)) WHERE (work_email IS NOT NULL)",
		"CREATE UNIQUE INDEX people_national_id_mac_key ON staff.people USING btree (national_id_mac)",
		"CREATE UNIQUE INDEX ux_identity_links_person_provider ON staff.identity_links USING btree (person_id, provider)",
		"CREATE UNIQUE INDEX ux_identity_links_provider_external ON staff.identity_links USING btree (provider, external_id)",
		"CREATE INDEX ix_identity_links_next_attempt ON staff.identity_links USING btree (next_attempt_at)",
		"CREATE INDEX ix_events_person_at ON staff.events USING btree (person_id, at)",
		"CREATE UNIQUE INDEX ux_employment_terms_valid_from ON staff.employment_terms USING btree (employment_id, valid_from)",
		"CREATE INDEX ix_employment_terms_manager ON staff.employment_terms USING btree (manager_person_id)",
		"CREATE INDEX ix_employment_terms_department ON staff.employment_terms USING btree (department_id)",
		"CREATE INDEX ix_employments_person_start ON staff.employments USING btree (person_id, start_date)",
		"CREATE INDEX ix_employments_end_date ON staff.employments USING btree (end_date) WHERE (end_date IS NOT NULL)",
		"CREATE UNIQUE INDEX ux_employments_number_upper ON staff.employments USING btree (upper((employment_number)::text))",
		"CREATE UNIQUE INDEX ux_legal_entities_default ON staff.legal_entities USING btree (is_default) WHERE is_default",
		"CREATE UNIQUE INDEX ux_business_units_default ON staff.business_units USING btree (is_default) WHERE is_default",
		"CREATE UNIQUE INDEX ux_departments_code_upper ON staff.departments USING btree (upper((code)::text))",
		"CREATE UNIQUE INDEX ux_settings_single_row ON staff.settings USING btree ((true))",
	} {
		if !indexes[want] {
			t.Errorf("missing index: %s", want)
		}
	}

	// The settings row, with the plan's defaults.
	var (
		zone, prefix, policy                           string
		next                                           int64
		perOccurrence, occurrences, perYear, afterMths int16
		createBefore, grace, health                    int16
		revision                                       int32
	)
	if err := pool.QueryRow(ctx, `
		SELECT time_zone, employee_number_prefix, next_employee_number, self_cert_policy,
		       self_cert_days_per_occurrence, self_cert_occurrences_per_year, self_cert_days_per_year,
		       self_cert_after_months, create_before_days, retention_grace_days, health_retention_days, revision
		FROM staff.settings`).Scan(&zone, &prefix, &next, &policy, &perOccurrence, &occurrences, &perYear,
		&afterMths, &createBefore, &grace, &health, &revision); err != nil {
		t.Fatalf("read the settings row: %v", err)
	}
	if zone != "Europe/Oslo" || prefix != "" || next != 1 || policy != "minimum" ||
		perOccurrence != 3 || occurrences != 4 || perYear != 24 || afterMths != 2 ||
		createBefore != 14 || grace != 90 || health != 365 || revision != 1 {
		t.Errorf("settings = %s %q %d %s %d/%d/%d/%d %d %d %d rev %d, want Europe/Oslo, no prefix, 1, minimum 3/4/24/2, 14, 90, 365, revision 1",
			zone, prefix, next, policy, perOccurrence, occurrences, perYear, afterMths, createBefore, grace, health, revision)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff.settings (id, updated_at) VALUES (2, now())`); !uniqueViolationOf(err, "ux_settings_single_row") {
		t.Errorf("a second settings row: %v, want the one-row index to refuse it", err)
	}

	// The employee number is unique however it is cased.
	insertPerson := func(number string) error {
		_, err := pool.Exec(ctx, `INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
			VALUES ($1, 'Kari', 'Nordmann', 'Kari Nordmann', now(), now())`, number)
		return err
	}
	if err := insertPerson("emp-7"); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := insertPerson("EMP-7"); !uniqueViolationOf(err, "ux_people_employee_number_upper") {
		t.Errorf("the same employee number upper-cased: %v, want the unique index to refuse it", err)
	}

	migrateTo(t, url, staffFoundationVersion-1)
	var schemas int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'staff'`).Scan(&schemas); err != nil {
		t.Fatalf("read what the rollback left: %v", err)
	}
	if schemas != 0 {
		t.Error("the staff schema survived the rollback")
	}
}
