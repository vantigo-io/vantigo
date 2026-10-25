package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffPersonPhotoVersion is 00069_staff_person_photo.sql: the person's photo
// (docs/superpowers/specs/2026-10-25-staff-person-glance-design.md, S1 and
// §3.1; the plan, docs/superpowers/plans/2026-10-25-staff-person-glance.md,
// task 2).
const staffPersonPhotoVersion = 69

// TestStaffPersonPhoto_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins the three columns on
// staff.people with their type, length, nullability and no default — and no
// CHECK, the module's validation owning the type, as 00042's house style has
// it. The Down drops the three columns.
func TestStaffPersonPhoto_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffPersonPhotoVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	photoColumns := []string{"photo_object_key", "photo_content_type", "photo_updated_at"}
	columns := func() []string {
		return staffCollect(t, ctx, pool, "staff.people's photo columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = 'people' AND column_name = ANY($1)
			ORDER BY ordinal_position`, photoColumns)
	}
	if got, want := columns(), []string{
		"photo_object_key:character varying(200):YES:",
		"photo_content_type:character varying(40):YES:",
		"photo_updated_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("staff.people's photo columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the people checks naming the photo", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c' AND conrelid = 'staff.people'::regclass
		    AND pg_get_constraintdef(oid) LIKE '%photo%'`); len(checks) != 0 {
		t.Errorf("the photo checks = %v, want none: the module validates", checks)
	}

	// A person with a photo, then the rollback: the columns go, the row stays.
	var kari int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name,
		    photo_object_key, photo_content_type, photo_updated_at, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann',
		    'people/1/photo/0b4e', 'image/jpeg', now(), now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person with a photo: %v", err)
	}

	migrateTo(t, url, staffPersonPhotoVersion-1)
	if got := columns(); len(got) != 0 {
		t.Errorf("the rollback left %v on staff.people", got)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); err != nil {
		t.Fatalf("clean up: %v", err)
	}
}
