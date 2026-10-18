package db_test

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The staff accounts schema (00051_staff_accounts.sql; staff foundation
// design S27, §3.6, delivery C): identity_links.external_id nullable, so the
// accounts worker's preparing link names no user until identity created one
// (plan reading 7), and staff.people's employee number unique by an
// expression no database collation decides. Its own file, as 00047's test is.

// staffAccountsVersion is 00051's goose version.
const staffAccountsVersion = 51

// The employee number's unique index before and after 00051. upper() under
// the database's collation is the collation's: under a Turkish one upper('i')
// is 'İ', and Staff would admit i-1 beside I-1 where identity's key
// (contracts.NormalizeEmployeeNumber, Go's case mapping) sees one number.
// Under "C" upper() maps a–z to A–Z and nothing else, which on Staff's
// alphabet (letters a–z, digits, hyphens) is Go's mapping exactly.
const (
	employeeNumberIndexBefore = "CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people USING btree (upper((employee_number)::text))"
	employeeNumberIndexAfter  = `CREATE UNIQUE INDEX ux_people_employee_number_upper ON staff.people USING btree (upper(((employee_number)::text COLLATE "C")))`
)

// TestStaffAccounts_AppliesAndIsIdempotent proves 00051_staff_accounts.sql
// applies, rolls back and re-applies cleanly, and pins what delivery C's
// Staff rests on: a link may name no user (a preparing link), and two links
// naming none do not collide on the (provider, external_id) index; the
// employee number is unique on upper() under the "C" collation, so i-1 is
// refused beside I-1; and the account flag has its own revision,
// people.account_revision, 1 by default; and the link keeps the employee
// number the worker last synced, identity_links.synced_employee_number, and
// when Disable now asked for its user to be disabled,
// identity_links.disable_requested_at. The Down deletes the links that name no user — the
// preparing ones — restores NOT NULL and the collation-dependent index, and
// leaves the links that name a user as they were.
func TestStaffAccounts_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffAccountsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	nullable := staffCollect(t, ctx, pool, "external_id", `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'external_id'`)
	if !equalStrings(nullable, []string{"YES"}) {
		t.Errorf("identity_links.external_id nullable = %v, want YES", nullable)
	}
	// The account flag's own revision (review M1): a change of the flag moves
	// it and never people.revision, which a manager reads.
	accountRevision := staffCollect(t, ctx, pool, "account_revision", `
		SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '') FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'people' AND column_name = 'account_revision'`)
	if !equalStrings(accountRevision, []string{"integer:NO:1"}) {
		t.Errorf("people.account_revision = %v, want integer:NO:1", accountRevision)
	}
	// The employee number the accounts worker last wrote to identity for the
	// link (Task 4b): a person HR renumbered is still known by the number
	// identity holds.
	synced := staffCollect(t, ctx, pool, "synced_employee_number", `
		SELECT data_type || ':' || character_maximum_length || ':' || is_nullable FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'synced_employee_number'`)
	if !equalStrings(synced, []string{"character varying:16:YES"}) {
		t.Errorf("identity_links.synced_employee_number = %v, want character varying:16:YES", synced)
	}
	// When Disable now asked for the link's user to be disabled and that is
	// not yet confirmed (Task 4c review M1): what the worker finishes, and
	// what raises accountFailing.
	requested := staffCollect(t, ctx, pool, "disable_requested_at", `
		SELECT data_type || ':' || is_nullable FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'disable_requested_at'`)
	if !equalStrings(requested, []string{"timestamp with time zone:YES"}) {
		t.Errorf("identity_links.disable_requested_at = %v, want timestamp with time zone:YES", requested)
	}
	// The absence settings the owner asked for (Task 5b): ammefri is paid
	// (aml. § 12-8 (2)), the reserved absent_hours is seeded hidden beside
	// absent, and the two erase settings are booleans, off by default.
	pay := staffCollect(t, ctx, pool, "ammefri's pay", `SELECT paid::text FROM staff.absence_types WHERE key = 'ammefri' AND seeded`)
	if !equalStrings(pay, []string{"true"}) {
		t.Errorf("ammefri paid = %v, want true", pay)
	}
	// The reserved types 00051 seeds (Task 5b): absent_hours beside absent,
	// and the two HR-only ones an erased HR-only row takes, so it reads to
	// every leave-reader as it did (the coordinator's decision).
	reserved := staffCollect(t, ctx, pool, "the reserved types", `
		SELECT key || ':' || kind || ':' || unit || ':' || paid::text || ':' || needs_decision::text || ':' || visibility || ':'
		    || coalesce(amelding_leave, '-') || ':' || hidden::text || ':' || seeded::text || ':' || sort
		FROM staff.absence_types WHERE key IN ('absent_hours', 'absent_hr_only', 'absent_hr_only_hours') ORDER BY sort`)
	if want := []string{
		"absent_hours:other:hours:false:false:absent:-:true:true:24",
		"absent_hr_only:other:days:false:false:hr_only:-:true:true:25",
		"absent_hr_only_hours:other:hours:false:false:hr_only:-:true:true:26",
	}; !equalStrings(reserved, want) {
		t.Errorf("the reserved types = %v, want %v", reserved, want)
	}
	erase := staffCollect(t, ctx, pool, "the erase settings", `
		SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '') FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'settings' AND column_name LIKE 'erase_%' ORDER BY column_name`)
	if want := []string{"erase_pregnancy_health_types:boolean:NO:false", "erase_sick_case_dates:boolean:NO:false"}; !equalStrings(erase, want) {
		t.Errorf("the erase settings = %v, want %v", erase, want)
	}

	index := staffCollect(t, ctx, pool, "the employee number index", `
		SELECT indexdef FROM pg_indexes WHERE schemaname = 'staff' AND indexname = 'ux_people_employee_number_upper'`)
	if !equalStrings(index, []string{employeeNumberIndexAfter}) {
		t.Errorf("the employee number index = %v, want %s", index, employeeNumberIndexAfter)
	}

	insertPerson := func(number string) (int64, error) {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
			VALUES ($1, 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`, number).Scan(&id)
		return id, err
	}
	kari, err := insertPerson("I-1")
	if err != nil {
		t.Fatalf("insert I-1: %v", err)
	}
	if _, err := insertPerson("i-1"); !uniqueViolationOf(err, "ux_people_employee_number_upper") {
		t.Errorf("i-1 beside I-1: %v, want the unique index to refuse it", err)
	}
	ola, err := insertPerson("I-2")
	if err != nil {
		t.Fatalf("insert I-2: %v", err)
	}
	per, err := insertPerson("I-3")
	if err != nil {
		t.Fatalf("insert I-3: %v", err)
	}

	// Two preparing links name no user, and a linked one names one.
	insertLink := func(person int64, externalID *string, state string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
			VALUES ($1, 'identity', $2, $3, false, now(), now())`, person, externalID, state)
		return err
	}
	if err := insertLink(kari, nil, "preparing"); err != nil {
		t.Fatalf("a preparing link with no user: %v, want it written", err)
	}
	if err := insertLink(ola, nil, "preparing"); err != nil {
		t.Errorf("a second preparing link with no user: %v, want no collision on the external index", err)
	}
	user := uuid.NewString()
	if err := insertLink(per, &user, "linked"); err != nil {
		t.Fatalf("a link naming a user: %v", err)
	}

	// A row naming a reserved type 00051 seeds makes the Down refuse, naming
	// the types: it cannot give the row another type without widening who
	// reads it (an HR-only erased row taking absent would read as absent to a
	// manager). Each row alone is refused; with none, the Down runs.
	for _, key := range []string{"absent_hours", "absent_hr_only", "absent_hr_only_hours"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff.absences (person_id, type_id, first_day, last_day, hours, status, requested_by_user_id, created_at, updated_at)
			SELECT $1, id, '2024-03-04', '2024-03-04', 2, 'registered', gen_random_uuid(), now(), now()
			FROM staff.absence_types WHERE key = $2`, kari, key); err != nil {
			t.Fatalf("a %s row: %v", key, err)
		}
		err := migrateErr(url, staffAccountsVersion-1)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "00051") {
			t.Errorf("the Down with a %s row: %v, want it refused naming %s", key, err, key)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM staff.absences WHERE person_id = $1`, kari); err != nil {
			t.Fatalf("delete the %s row: %v", key, err)
		}
	}

	migrateTo(t, url, staffAccountsVersion-1)
	if got := staffCollect(t, ctx, pool, "ammefri's pay after the rollback", `SELECT paid::text FROM staff.absence_types WHERE key = 'ammefri'`); !equalStrings(got, []string{"false"}) {
		t.Errorf("after the rollback ammefri paid = %v, want false", got)
	}
	if got := staffCollect(t, ctx, pool, "the reserved types after the rollback", `
		SELECT key FROM staff.absence_types WHERE key IN ('absent_hours', 'absent_hr_only', 'absent_hr_only_hours')`); len(got) != 0 {
		t.Errorf("the rollback left %v", got)
	}
	if got := staffCollect(t, ctx, pool, "the erase settings after the rollback", `
		SELECT column_name FROM information_schema.columns WHERE table_schema = 'staff' AND table_name = 'settings' AND column_name LIKE 'erase_%'`); len(got) != 0 {
		t.Errorf("the rollback left the erase settings %v", got)
	}
	left := staffCollect(t, ctx, pool, "links after the rollback", `
		SELECT state || ':' || coalesce(external_id, '-') FROM staff.identity_links ORDER BY id`)
	if want := []string{"linked:" + user}; !equalStrings(left, want) {
		t.Errorf("links after the rollback = %v, want the preparing ones deleted and %v kept", left, want)
	}
	nullable = staffCollect(t, ctx, pool, "external_id after the rollback", `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'external_id'`)
	if !equalStrings(nullable, []string{"NO"}) {
		t.Errorf("after the rollback identity_links.external_id nullable = %v, want NO", nullable)
	}
	index = staffCollect(t, ctx, pool, "the employee number index after the rollback", `
		SELECT indexdef FROM pg_indexes WHERE schemaname = 'staff' AND indexname = 'ux_people_employee_number_upper'`)
	if !equalStrings(index, []string{employeeNumberIndexBefore}) {
		t.Errorf("after the rollback the employee number index = %v, want %s", index, employeeNumberIndexBefore)
	}
	if left := staffCollect(t, ctx, pool, "account_revision after the rollback", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'people' AND column_name = 'account_revision'`); len(left) != 0 {
		t.Errorf("the rollback left people.account_revision")
	}
	if left := staffCollect(t, ctx, pool, "synced_employee_number after the rollback", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'synced_employee_number'`); len(left) != 0 {
		t.Errorf("the rollback left identity_links.synced_employee_number")
	}
	if left := staffCollect(t, ctx, pool, "disable_requested_at after the rollback", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'disable_requested_at'`); len(left) != 0 {
		t.Errorf("the rollback left identity_links.disable_requested_at")
	}
	for _, sql := range []string{`DELETE FROM staff.absences`, `DELETE FROM staff.identity_links`, `DELETE FROM staff.people`} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("clean up (%s): %v", sql, err)
		}
	}
}

// migrateErr moves the database to version, up or down, and answers the
// migration's error rather than failing the test.
func migrateErr(databaseURL string, version int64) error {
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return err
	}
	sqlDB := sql.OpenDB(stdlib.GetConnector(*cfg))
	defer func() { _ = sqlDB.Close() }()
	dir, err := fs.Sub(db.MigrationsFS, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		return err
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	if version >= current {
		_, err = provider.UpTo(ctx, version)
		return err
	}
	_, err = provider.DownTo(ctx, version)
	return err
}

// 00051 refuses to run, naming the key, when a company type already holds
// one of the keys it reserves (review M3): a company type keyed absent_hours
// would otherwise fail the seed's insert on the unique key with a bare
// constraint error, or worse be mistaken for the reserved type.
func TestStaffAccounts_RefusesACompanyTypeHoldingAReservedKey(t *testing.T) {
	for _, key := range []string{"absent_hours", "absent_hr_only", "absent_hr_only_hours"} {
		url := testdb.URL(t)
		migrateTo(t, url, staffAccountsVersion-1)
		ctx := context.Background()
		pool, err := db.Open(ctx, url)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff.absence_types (key, name_en, name_nb, kind, unit, paid, needs_decision, sort, created_at, updated_at)
			VALUES ($1, 'Ours', 'Vår', 'other', 'days', true, true, 50, now(), now())`, key); err != nil {
			t.Fatalf("a company type keyed %s: %v", key, err)
		}
		err = migrateErr(url, staffAccountsVersion)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("00051 over a company type keyed %s: %v, want it refused naming the key", key, err)
		}
		pool.Close()
	}
}
