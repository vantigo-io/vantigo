package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffPhotoPushVersion is 00071_staff_photo_push.sql: the stamp of the
// photo last pushed to the directory on a provider link
// (docs/superpowers/specs/2026-10-25-staff-photo-sync-design.md, S2 and
// §3.2; the plan, docs/superpowers/plans/2026-10-25-staff-photo-sync.md,
// task 2).
const staffPhotoPushVersion = 71

// TestStaffPhotoPush_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins staff.identity_links'
// photo_pushed_stamp with its type, length, nullability and no default — and
// no CHECK, the worker writing it, as 00042's house style has it. The Down
// drops the column and leaves the link.
func TestStaffPhotoPush_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffPhotoPushVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func() []string {
		return staffCollect(t, ctx, pool, "staff.identity_links' photo_pushed_stamp", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = 'identity_links' AND column_name = 'photo_pushed_stamp'`)
	}
	if got, want := columns(), []string{"photo_pushed_stamp:character varying(80):YES:"}; !equalStrings(got, want) {
		t.Errorf("staff.identity_links' photo_pushed_stamp =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the identity_links checks naming the photo", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c' AND conrelid = 'staff.identity_links'::regclass
		    AND pg_get_constraintdef(oid) LIKE '%photo%'`); len(checks) != 0 {
		t.Errorf("the photo checks = %v, want none: the worker writes it", checks)
	}

	// A provider link with a pushed stamp, then the rollback: the column
	// goes, the link stays.
	var kari, link int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, photo_pushed_stamp, created_at, updated_at)
		VALUES ($1, 'entra', 'oid-1', 'enabled', true, 'managed:2026-10-25T10:00:00Z', now(), now()) RETURNING id`, kari).Scan(&link); err != nil {
		t.Fatalf("insert a provider link with a pushed stamp: %v", err)
	}

	migrateTo(t, url, staffPhotoPushVersion-1)
	if got := columns(); len(got) != 0 {
		t.Errorf("the rollback left %v on staff.identity_links", got)
	}
	var links int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM staff.identity_links WHERE id = $1`, link).Scan(&links); err != nil || links != 1 {
		t.Errorf("the link after the rollback: %d rows, %v, want it kept", links, err)
	}
}
