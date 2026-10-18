package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

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
// people.account_revision, 1 by default. The Down deletes the links that name no user — the
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

	migrateTo(t, url, staffAccountsVersion-1)
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
	for _, sql := range []string{`DELETE FROM staff.identity_links`, `DELETE FROM staff.people`} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("clean up (%s): %v", sql, err)
		}
	}
}
