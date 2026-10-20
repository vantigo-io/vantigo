package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// identityExpectedAccountsVersion is 00059_identity_expected_accounts.sql,
// the announced account (docs/superpowers/specs/2026-10-19-entra-design.md,
// D4) and the record of the provisioning roles given (plan reading 3).
const identityExpectedAccountsVersion = 59

// TestIdentityExpectedAccounts_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the announcement
// and its adoption rest on: every column of identity.expected_accounts with
// its type, length, nullability and default; the number's key as its primary
// key, so a second announcement for one number replaces the first; the two
// plain partial indexes the other two matches read (plan reading 5: neither
// is unique); the actor's CHECK, a user or a system and never both or
// neither; the cascade from the user an announcement replaces; and
// identity.users.provisioning_roles_at, null until Staff's Activate gives the
// roles.
func TestIdentityExpectedAccounts_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, identityExpectedAccountsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "identity.expected_accounts columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'identity' AND table_name = 'expected_accounts'
		ORDER BY ordinal_position`)
	if want := []string{
		"employee_number_key:text:NO:",
		"employee_number:character varying(16):NO:",
		"display_name:character varying(200):NO:",
		"preferred_language:character varying(2):YES:",
		"issuer:character varying(200):YES:",
		"object_id:character varying(200):YES:",
		"user_principal_name:character varying(254):YES:",
		"replace_user_id:uuid:YES:",
		"created_at:timestamp with time zone:NO:",
		"created_by_user_id:uuid:YES:",
		"created_by_system:character varying(40):YES:",
	}; !equalStrings(columns, want) {
		t.Errorf("identity.expected_accounts columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "identity", "expected_accounts"); !equalStrings(cols, []string{"employee_number_key"}) {
		t.Errorf("identity.expected_accounts primary key = %v, want [employee_number_key]", cols)
	}
	indexes := staffCollect(t, ctx, pool, "identity.expected_accounts indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'identity' AND tablename = 'expected_accounts' AND indexname <> 'expected_accounts_pkey'
		ORDER BY indexname`)
	if want := []string{
		"CREATE INDEX ix_expected_accounts_object_id ON identity.expected_accounts USING btree (object_id) WHERE (object_id IS NOT NULL)",
		"CREATE INDEX ix_expected_accounts_upn ON identity.expected_accounts USING btree (user_principal_name) WHERE (user_principal_name IS NOT NULL)",
	}; !equalStrings(indexes, want) {
		t.Errorf("identity.expected_accounts indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}
	checks := staffCollect(t, ctx, pool, "identity.expected_accounts checks", `
		SELECT conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'identity.expected_accounts'::regclass AND contype = 'c'
		ORDER BY 1`)
	if want := []string{
		"expected_accounts_created_by_check CHECK (((created_by_user_id IS NULL) <> (created_by_system IS NULL)))",
	}; !equalStrings(checks, want) {
		t.Errorf("identity.expected_accounts checks =\n%s\nwant\n%s", strings.Join(checks, "\n"), strings.Join(want, "\n"))
	}
	foreign := staffCollect(t, ctx, pool, "identity.expected_accounts foreign keys", `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'identity.expected_accounts'::regclass AND contype = 'f'
		ORDER BY 1`)
	if want := []string{"FOREIGN KEY (replace_user_id) REFERENCES identity.users(id) ON DELETE CASCADE"}; !equalStrings(foreign, want) {
		t.Errorf("identity.expected_accounts foreign keys = %v, want %v", foreign, want)
	}
	rolesAt := staffCollect(t, ctx, pool, "identity.users.provisioning_roles_at", `
		SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '') FROM information_schema.columns
		WHERE table_schema = 'identity' AND table_name = 'users' AND column_name = 'provisioning_roles_at'`)
	if want := []string{"timestamp with time zone:YES:"}; !equalStrings(rolesAt, want) {
		t.Errorf("identity.users.provisioning_roles_at = %v, want %v", rolesAt, want)
	}

	// A pending user an announcement may replace.
	pending := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
		VALUES ($1, 'kari@example.test', 'KARI@EXAMPLE.TEST', 'Kari Nordmann', gen_random_uuid(), now(), now())`, pending); err != nil {
		t.Fatalf("insert the pending user: %v", err)
	}
	announce := func(key string, userID *uuid.UUID, system *string, replace *uuid.UUID) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO identity.expected_accounts
			    (employee_number_key, employee_number, display_name, object_id, user_principal_name, replace_user_id,
			     created_at, created_by_user_id, created_by_system)
			VALUES ($1, $2, 'Kari Nordmann', 'oid-1', 'KARI@EXAMPLE.TEST', $3, now(), $4, $5)`, key, key, replace, userID, system)
		return err
	}
	system := "system:staff-accounts"
	actor := uuid.New()
	if err := announce("E-1", nil, &system, &pending); err != nil {
		t.Fatalf("an announcement by the worker: %v", err)
	}
	if err := announce("E-1", &actor, nil, nil); !uniqueViolationOf(err, "expected_accounts_pkey") {
		t.Errorf("a second row for one key: %v, want the primary key to refuse it (an upsert replaces)", err)
	}
	// One object id and one UPN on two numbers: the indexes are plain.
	if err := announce("E-2", &actor, nil, nil); err != nil {
		t.Errorf("a second number with the same object id and UPN: %v, want it admitted (plan reading 5)", err)
	}
	if err := announce("E-3", nil, nil, nil); !checkViolationOf(err, "expected_accounts_created_by_check") {
		t.Errorf("no actor: %v, want expected_accounts_created_by_check", err)
	}
	if err := announce("E-4", &actor, &system, nil); !checkViolationOf(err, "expected_accounts_created_by_check") {
		t.Errorf("two actors: %v, want expected_accounts_created_by_check", err)
	}
	gone := uuid.New()
	if err := announce("E-5", &actor, nil, &gone); !foreignKeyViolationOf(err, "expected_accounts_replace_user_id_fkey") {
		t.Errorf("replacing a user who does not exist: %v, want expected_accounts_replace_user_id_fkey", err)
	}

	// The user an announcement replaces is deleted: the announcement goes.
	if _, err := pool.Exec(ctx, `DELETE FROM identity.users WHERE id = $1`, pending); err != nil {
		t.Fatalf("delete the pending user: %v", err)
	}
	left := staffCollect(t, ctx, pool, "the announcements after the delete", `
		SELECT employee_number_key FROM identity.expected_accounts ORDER BY 1`)
	if want := []string{"E-2"}; !equalStrings(left, want) {
		t.Errorf("announcements after the replaced user's delete = %v, want %v (the cascade)", left, want)
	}
}

// TestIdentityExpectedAccounts_DownDropsBoth proves the down path removes
// the table, its announcements with it, and the users' column.
func TestIdentityExpectedAccounts_DownDropsBoth(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, identityExpectedAccountsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.expected_accounts (employee_number_key, employee_number, display_name, created_at, created_by_system)
		VALUES ('E-1', 'E-1', 'Kari Nordmann', now(), 'system:staff-accounts')`); err != nil {
		t.Fatalf("write an announcement: %v", err)
	}
	pool.Close()

	migrateTo(t, url, identityExpectedAccountsVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	if got := staffCollect(t, ctx, pool, "identity.expected_accounts", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'identity' AND table_name = 'expected_accounts'`); len(got) != 0 {
		t.Errorf("identity.expected_accounts after the down = %v, want it gone", got)
	}
	if got := staffCollect(t, ctx, pool, "identity.users.provisioning_roles_at", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'identity' AND table_name = 'users' AND column_name = 'provisioning_roles_at'`); len(got) != 0 {
		t.Errorf("identity.users.provisioning_roles_at after the down = %v, want it gone", got)
	}
}
