package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// identityManagedAvatarsVersion is 00070_identity_managed_avatars.sql, the
// managed avatar (docs/superpowers/specs/2026-10-25-staff-photo-sync-design.md,
// S1 and §3.1; the plan, docs/superpowers/plans/2026-10-25-staff-photo-sync.md,
// task 2).
const identityManagedAvatarsVersion = 70

// TestIdentityManagedAvatars_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the managed
// avatar rests on: every column of identity.managed_avatars with its type,
// nullability and no default; the user as its primary key, one managed
// picture per user; the type's CHECK, the one profile_avatars has; the
// cascade from the user; and the user's own avatar untouched beside it. The
// Down drops the table.
func TestIdentityManagedAvatars_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, identityManagedAvatarsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "identity.managed_avatars columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'identity' AND table_name = 'managed_avatars'
		ORDER BY ordinal_position`)
	if want := []string{
		"user_id:uuid:NO:",
		"data:bytea:NO:",
		"content_type:text:NO:",
		"source:text:NO:",
		"source_stamp:text:NO:",
		"version:integer:NO:",
		"updated_at:timestamp with time zone:NO:",
	}; !equalStrings(columns, want) {
		t.Errorf("identity.managed_avatars columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "identity", "managed_avatars"); !equalStrings(cols, []string{"user_id"}) {
		t.Errorf("identity.managed_avatars primary key = %v, want [user_id]", cols)
	}
	checks := staffCollect(t, ctx, pool, "identity.managed_avatars checks", `
		SELECT conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'identity.managed_avatars'::regclass AND contype = 'c'
		ORDER BY 1`)
	if want := []string{
		"managed_avatars_content_type_check CHECK ((content_type = ANY (ARRAY['image/png'::text, 'image/jpeg'::text])))",
	}; !equalStrings(checks, want) {
		t.Errorf("identity.managed_avatars checks =\n%s\nwant\n%s", strings.Join(checks, "\n"), strings.Join(want, "\n"))
	}
	foreign := staffCollect(t, ctx, pool, "identity.managed_avatars foreign keys", `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'identity.managed_avatars'::regclass AND contype = 'f'
		ORDER BY 1`)
	if want := []string{"FOREIGN KEY (user_id) REFERENCES identity.users(id) ON DELETE CASCADE"}; !equalStrings(foreign, want) {
		t.Errorf("identity.managed_avatars foreign keys = %v, want %v", foreign, want)
	}

	// A user with their own avatar and a managed one: both rows stand.
	kari := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
		VALUES ($1, 'kari@example.test', 'KARI@EXAMPLE.TEST', 'Kari Nordmann', gen_random_uuid(), now(), now())`, kari); err != nil {
		t.Fatalf("insert the user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.profile_avatars (user_id, data, content_type, version, updated_at)
		VALUES ($1, '\x89504e47', 'image/png', 1, now())`, kari); err != nil {
		t.Fatalf("insert the user's own avatar: %v", err)
	}
	manage := func(user uuid.UUID, contentType string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO identity.managed_avatars (user_id, data, content_type, source, source_stamp, version, updated_at)
			VALUES ($1, '\xffd8ffe0', $2, 'staff', '2026-10-25T10:00:00Z', 1, now())`, user, contentType)
		return err
	}
	if err := manage(kari, "image/jpeg"); err != nil {
		t.Fatalf("a managed avatar beside the user's own: %v", err)
	}
	if err := manage(kari, "image/jpeg"); !uniqueViolationOf(err, "managed_avatars_pkey") {
		t.Errorf("a second managed avatar for one user: %v, want managed_avatars_pkey (one per user)", err)
	}
	if err := manage(uuid.New(), "image/jpeg"); !foreignKeyViolationOf(err, "managed_avatars_user_id_fkey") {
		t.Errorf("a managed avatar for a user who does not exist: %v, want managed_avatars_user_id_fkey", err)
	}
	other := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
		VALUES ($1, 'ola@example.test', 'OLA@EXAMPLE.TEST', 'Ola Nordmann', gen_random_uuid(), now(), now())`, other); err != nil {
		t.Fatalf("insert the other user: %v", err)
	}
	if err := manage(other, "image/gif"); !checkViolationOf(err, "managed_avatars_content_type_check") {
		t.Errorf("a GIF: %v, want managed_avatars_content_type_check", err)
	}

	// The user's delete takes both pictures.
	if _, err := pool.Exec(ctx, `DELETE FROM identity.users WHERE id = $1`, kari); err != nil {
		t.Fatalf("delete the user: %v", err)
	}
	if left := staffCollect(t, ctx, pool, "the managed avatars after the delete", `
		SELECT user_id::text FROM identity.managed_avatars`); len(left) != 0 {
		t.Errorf("managed avatars after the user's delete = %v, want none (the cascade)", left)
	}

	migrateTo(t, url, identityManagedAvatarsVersion-1)
	if got := staffCollect(t, ctx, pool, "identity.managed_avatars", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'identity' AND table_name = 'managed_avatars'`); len(got) != 0 {
		t.Errorf("identity.managed_avatars after the down = %v, want it gone", got)
	}
}
