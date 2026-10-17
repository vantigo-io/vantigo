package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The identity accounts schema (00050_identity_accounts.sql; staff foundation
// design S24–S26, delivery C): the Staff-managed columns on identity.users,
// the invitation that names the user it completes and why one was revoked,
// the authority mode the API process records, and the roles Staff may give.
// Identity's first migration after its baseline, in its own file.

// identityAccountsVersion is 00050's goose version.
const identityAccountsVersion = 50

// identityAccountsColumns is every column 00050 adds, as
// table.column:type:length:nullable:default.
var identityAccountsColumns = []string{
	"invitations.revoked_reason:character varying:20:YES:",
	"invitations.user_id:uuid::YES:",
	"provisioning_roles.created_at:timestamp with time zone::NO:",
	"provisioning_roles.role_id:uuid::NO:",
	"system_settings.authority_mode:character varying:10:YES:",
	"system_settings.authority_mode_at:timestamp with time zone::YES:",
	"users.disabled_by:character varying:10:YES:",
	"users.employee_number:character varying:16:YES:",
	"users.employee_number_key:text::YES:",
	"users.invitations_since:timestamp with time zone::YES:",
	"users.managed_by:character varying:20:YES:",
	"users.pending_since:timestamp with time zone::YES:",
}

// identityAccountsColumnsQuery answers the columns 00050 adds that exist.
const identityAccountsColumnsQuery = `
	SELECT table_name || '.' || column_name || ':' || data_type || ':'
	    || coalesce(character_maximum_length::text, '') || ':' || is_nullable || ':' || coalesce(column_default, '')
	FROM information_schema.columns
	WHERE table_schema = 'identity' AND (table_name = 'provisioning_roles'
	   OR (table_name, column_name) IN (
	       ('users', 'managed_by'), ('users', 'employee_number'), ('users', 'employee_number_key'), ('users', 'pending_since'), ('users', 'invitations_since'), ('users', 'disabled_by'),
	       ('invitations', 'user_id'), ('invitations', 'revoked_reason'),
	       ('system_settings', 'authority_mode'), ('system_settings', 'authority_mode_at')))
	ORDER BY 1`

// TestIdentityAccounts_AppliesBackfillsAndIsIdempotent proves
// 00050_identity_accounts.sql applies over an installation with users and
// invitations, rolls back and re-applies cleanly, and pins what delivery C's
// identity rests on: every column it adds; the employee number unique by its
// key — written by identity's one Go normaliser, never by a collation's
// upper() — and only where set, the key present exactly with the number; the
// invitations' user index; the values
// the four enumerated columns admit (identity's baseline style: a CHECK per
// column); provisioning_roles cascading with its role; and the backfills —
// a user disabled before 00050 was disabled by an Owner (only an Owner could
// disable one), an invitation revoked before it was revoked as an Owner's
// (plan reading 14), and nothing else touched. The Down removes all of it
// and leaves the baseline as it was.
func TestIdentityAccounts_AppliesBackfillsAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	ctx := context.Background()

	// The installation before 00050: an enabled and a disabled user, a
	// pending and a revoked invitation.
	migrateTo(t, url, identityAccountsVersion-1)
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	enabled, disabled := uuid.New(), uuid.New()
	for _, u := range []struct {
		id       uuid.UUID
		email    string
		disabled bool
	}{{enabled, "enabled@example.test", false}, {disabled, "disabled@example.test", true}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.users (id, email, normalized_email, display_name, is_disabled, version, created_at, updated_at)
			VALUES ($1, $2, upper($2), $2, $3, gen_random_uuid(), now(), now())`, u.id, u.email, u.disabled); err != nil {
			t.Fatalf("insert user %s: %v", u.email, err)
		}
	}
	pending, revoked := uuid.New(), uuid.New()
	for _, inv := range []struct {
		id      uuid.UUID
		email   string
		revoked bool
	}{{pending, "pending@example.test", false}, {revoked, "revoked@example.test", true}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO identity.invitations (id, email, normalized_email, role, token_hash, created_at, expires_at, revoked_at)
			VALUES ($1, $2, upper($2), 'User', $3, now(), now() + interval '7 days', CASE WHEN $4 THEN now() END)`,
			inv.id, inv.email, []byte(inv.id.String()), inv.revoked); err != nil {
			t.Fatalf("insert invitation %s: %v", inv.email, err)
		}
	}

	migrateTo(t, url, identityAccountsVersion)
	backfilled := staffCollect(t, ctx, pool, "the users' backfill", `
		SELECT email || ':' || coalesce(disabled_by, '-') || ':' || coalesce(managed_by, '-') || ':'
		    || coalesce(employee_number, '-') || ':' || (pending_since IS NULL)::text
		FROM identity.users ORDER BY email`)
	if want := []string{"disabled@example.test:owner:-:-:true", "enabled@example.test:-:-:-:true"}; !equalStrings(backfilled, want) {
		t.Errorf("users after 00050 = %v, want %v", backfilled, want)
	}
	backfilled = staffCollect(t, ctx, pool, "the invitations' backfill", `
		SELECT email || ':' || coalesce(revoked_reason, '-') || ':' || (user_id IS NULL)::text
		FROM identity.invitations ORDER BY email`)
	if want := []string{"pending@example.test:-:true", "revoked@example.test:owner:true"}; !equalStrings(backfilled, want) {
		t.Errorf("invitations after 00050 = %v, want %v", backfilled, want)
	}

	applyUpDownUp(t, url, identityAccountsVersion)

	columns := staffCollect(t, ctx, pool, "columns", identityAccountsColumnsQuery)
	if !equalStrings(columns, identityAccountsColumns) {
		t.Errorf("columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(identityAccountsColumns, "\n"))
	}

	indexes := staffCollect(t, ctx, pool, "indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'identity' AND indexname IN ('ux_users_employee_number', 'ix_invitations_user', 'provisioning_roles_pkey')
		ORDER BY indexname`)
	wantIndexes := []string{
		"CREATE INDEX ix_invitations_user ON identity.invitations USING btree (user_id) WHERE (user_id IS NOT NULL)",
		"CREATE UNIQUE INDEX provisioning_roles_pkey ON identity.provisioning_roles USING btree (role_id)",
		"CREATE UNIQUE INDEX ux_users_employee_number ON identity.users USING btree (employee_number_key) WHERE (employee_number_key IS NOT NULL)",
	}
	if !equalStrings(indexes, wantIndexes) {
		t.Errorf("indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(wantIndexes, "\n"))
	}

	foreignKeys := staffCollect(t, ctx, pool, "foreign keys", `
		SELECT conrelid::regclass::text || ':' || pg_get_constraintdef(oid)
		FROM pg_constraint WHERE conrelid = 'identity.provisioning_roles'::regclass AND contype = 'f'`)
	if want := []string{"identity.provisioning_roles:FOREIGN KEY (role_id) REFERENCES identity.roles(id) ON DELETE CASCADE"}; !equalStrings(foreignKeys, want) {
		t.Errorf("foreign keys = %v, want %v", foreignKeys, want)
	}

	checks := staffCollect(t, ctx, pool, "checks", `
		SELECT conname || ':' || pg_get_constraintdef(oid) FROM pg_constraint
		WHERE connamespace = 'identity'::regnamespace AND contype = 'c'
		  AND conname IN ('users_managed_by_check', 'users_disabled_by_check', 'users_employee_number_key_check', 'invitations_revoked_reason_check', 'system_settings_authority_mode_check')
		ORDER BY 1`)
	wantChecks := []string{
		"invitations_revoked_reason_check:CHECK (((revoked_reason)::text = ANY ((ARRAY['owner'::character varying, 'replaced'::character varying, 'delivery_failed'::character varying, 'email_changed'::character varying, 'deactivated'::character varying])::text[])))",
		"system_settings_authority_mode_check:CHECK (((authority_mode)::text = ANY ((ARRAY['local'::character varying, 'federated'::character varying])::text[])))",
		"users_disabled_by_check:CHECK (((disabled_by)::text = ANY ((ARRAY['owner'::character varying, 'staff'::character varying])::text[])))",
		"users_employee_number_key_check:CHECK (((employee_number IS NULL) = (employee_number_key IS NULL)))",
		"users_managed_by_check:CHECK (((managed_by)::text = 'staff'::text))",
	}
	if !equalStrings(checks, wantChecks) {
		t.Errorf("checks =\n%s\nwant\n%s", strings.Join(checks, "\n"), strings.Join(wantChecks, "\n"))
	}

	// The employee number is unique by its key, the key exactly where the
	// number is, and the key is what identity's normaliser wrote: the database
	// never folds it (two numbers with keys that differ only as a collation's
	// upper() would fold them are both admitted).
	number := func(id uuid.UUID, n, key *string) error {
		_, err := pool.Exec(ctx, `UPDATE identity.users SET managed_by = 'staff', employee_number = $2, employee_number_key = $3 WHERE id = $1`, id, n, key)
		return err
	}
	lower, upper, key := "a-17", "A-17", "A-17"
	if err := number(enabled, &lower, &key); err != nil {
		t.Fatalf("number a user: %v", err)
	}
	if err := number(disabled, &upper, &key); !uniqueViolationOf(err, "ux_users_employee_number") {
		t.Errorf("a second user keyed %q: %v, want the unique index to refuse it", key, err)
	}
	capitalSharp, sharp := "ẞ-17", "ß-17"
	if err := number(enabled, &capitalSharp, &capitalSharp); err != nil {
		t.Fatalf("renumber the first user: %v", err)
	}
	if err := number(disabled, &sharp, &sharp); err != nil {
		t.Errorf("a second user keyed %q beside %q: %v, want it written (only the normaliser folds)", sharp, capitalSharp, err)
	}
	if err := number(disabled, nil, nil); err != nil {
		t.Errorf("a second user with no number: %v, want it written", err)
	}
	if err := number(disabled, &upper, nil); !checkViolationOf(err, "users_employee_number_key_check") {
		t.Errorf("a number without its key: %v, want the check to refuse it", err)
	}
	if err := number(disabled, nil, &key); !checkViolationOf(err, "users_employee_number_key_check") {
		t.Errorf("a key without its number: %v, want the check to refuse it", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.users SET managed_by = 'entra' WHERE id = $1`, enabled); !checkViolationOf(err, "users_managed_by_check") {
		t.Errorf("managed_by = 'entra': %v, want the check to refuse it", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.users SET disabled_by = 'scim' WHERE id = $1`, enabled); !checkViolationOf(err, "users_disabled_by_check") {
		t.Errorf("disabled_by = 'scim': %v, want the check to refuse it", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.invitations SET revoked_reason = 'expired' WHERE id = $1`, revoked); !checkViolationOf(err, "invitations_revoked_reason_check") {
		t.Errorf("revoked_reason = 'expired': %v, want the check to refuse it", err)
	}

	// A provisioning role goes with its role.
	role := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
		VALUES ($1, 'Employee', 'EMPLOYEE', 'Employee', gen_random_uuid(), now(), now())`, role); err != nil {
		t.Fatalf("insert a role: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO identity.provisioning_roles (role_id, created_at) VALUES ($1, now())`, role); err != nil {
		t.Fatalf("name it a provisioning role: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM identity.roles WHERE id = $1`, role); err != nil {
		t.Fatalf("delete the role: %v", err)
	}
	if left := staffCollect(t, ctx, pool, "provisioning roles", `SELECT role_id::text FROM identity.provisioning_roles`); len(left) != 0 {
		t.Errorf("provisioning roles after the role's delete = %v, want none", left)
	}

	migrateTo(t, url, identityAccountsVersion-1)
	if left := staffCollect(t, ctx, pool, "columns after the rollback", identityAccountsColumnsQuery); len(left) != 0 {
		t.Errorf("the rollback left %v", left)
	}
	if left := staffCollect(t, ctx, pool, "indexes after the rollback", `
		SELECT indexname FROM pg_indexes WHERE schemaname = 'identity' AND indexname IN ('ux_users_employee_number', 'ix_invitations_user')`); len(left) != 0 {
		t.Errorf("the rollback left the indexes %v", left)
	}
	if n := staffCollect(t, ctx, pool, "users after the rollback", `SELECT email FROM identity.users ORDER BY email`); len(n) != 2 {
		t.Errorf("users after the rollback = %v, want both kept", n)
	}
}
