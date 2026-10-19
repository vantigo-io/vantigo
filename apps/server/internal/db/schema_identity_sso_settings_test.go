package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// identitySsoSettingsVersion is 00058_identity_sso_settings.sql, the
// installation's single sign-on settings
// (docs/superpowers/specs/2026-10-19-entra-design.md, D1).
const identitySsoSettingsVersion = 58

// TestIdentitySsoSettings_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the settings
// reader, the Owner's endpoints and the import rest on: every column of
// identity.sso_settings with its type, length, nullability and default; the
// one row its primary key admits; the two providers its CHECK admits; and a
// fresh row, as EnsureSsoSettings writes it, reading never written, with
// OIDC and SCIM off and no allowed domains.
func TestIdentitySsoSettings_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, identitySsoSettingsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "identity.sso_settings columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'identity' AND table_name = 'sso_settings'
		ORDER BY ordinal_position`)
	if want := []string{
		"id:smallint:NO:",
		"version:integer:NO:0",
		"oidc_enabled:boolean:NO:false",
		"oidc_provider:character varying(10):YES:",
		"oidc_authority:character varying(200):YES:",
		"oidc_client_id:character varying(200):YES:",
		"oidc_client_secret_sealed:bytea:YES:",
		"oidc_client_secret_set_at:timestamp with time zone:YES:",
		"oidc_workload_token_file:character varying(500):YES:",
		"oidc_allowed_domains:ARRAY:NO:'{}'::text[]",
		"oidc_display_name:character varying(100):YES:",
		"scim_enabled:boolean:NO:false",
		"scim_token_sha256:bytea:YES:",
		"scim_token_set_at:timestamp with time zone:YES:",
		"scim_previous_token_sha256:bytea:YES:",
		"scim_previous_expires_at:timestamp with time zone:YES:",
		"imported_from_environment:boolean:NO:false",
		"updated_at:timestamp with time zone:NO:",
		"updated_by_user_id:uuid:YES:",
	}; !equalStrings(columns, want) {
		t.Errorf("identity.sso_settings columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}

	// information_schema says ARRAY; the element type is text.
	element := staffCollect(t, ctx, pool, "the allowed domains' element type", `
		SELECT e.data_type FROM information_schema.columns c
		JOIN information_schema.element_types e
		  ON (e.object_catalog, e.object_schema, e.object_name, e.object_type, e.collection_type_identifier)
		   = (c.table_catalog, c.table_schema, c.table_name, 'TABLE', c.dtd_identifier)
		WHERE c.table_schema = 'identity' AND c.table_name = 'sso_settings' AND c.column_name = 'oidc_allowed_domains'`)
	if want := []string{"text"}; !equalStrings(element, want) {
		t.Errorf("identity.sso_settings.oidc_allowed_domains element type = %v, want %v", element, want)
	}

	if cols := primaryKeyColumns(t, ctx, pool, "identity", "sso_settings"); !equalStrings(cols, []string{"id"}) {
		t.Errorf("identity.sso_settings primary key = %v, want [id]", cols)
	}
	checks := staffCollect(t, ctx, pool, "identity.sso_settings checks", `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'identity.sso_settings'::regclass AND contype = 'c'
		ORDER BY 1`)
	if want := []string{
		"CHECK (((oidc_provider)::text = ANY ((ARRAY['entra'::character varying, 'google'::character varying])::text[])))",
		"CHECK ((id = 1))",
	}; !equalStrings(checks, want) {
		t.Errorf("identity.sso_settings checks =\n%s\nwant\n%s", strings.Join(checks, "\n"), strings.Join(want, "\n"))
	}

	// The row EnsureSsoSettings writes: never written, everything off.
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.sso_settings (id, version, updated_at) VALUES (1, 0, now())
		ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("ensure the settings row: %v", err)
	}
	fresh := staffCollect(t, ctx, pool, "a fresh row", `
		SELECT version || ':' || oidc_enabled || ':' || scim_enabled || ':' || imported_from_environment || ':'
		    || cardinality(oidc_allowed_domains)
		FROM identity.sso_settings`)
	if want := []string{"0:false:false:false:0"}; !equalStrings(fresh, want) {
		t.Errorf("a fresh row = %v, want %v (version 0, OIDC and SCIM off, not imported, no domains)", fresh, want)
	}

	// A second row, and a provider outside the two, are refused.
	if _, err := pool.Exec(ctx, `INSERT INTO identity.sso_settings (id, updated_at) VALUES (2, now())`); !checkViolationOf(err, "sso_settings_id_check") {
		t.Errorf("a second row: err = %v, want the sso_settings_id_check violation", err)
	}
	for _, provider := range []string{"entra", "google"} {
		if _, err := pool.Exec(ctx, `UPDATE identity.sso_settings SET oidc_provider = $1 WHERE id = 1`, provider); err != nil {
			t.Errorf("provider %q: %v, want it admitted", provider, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE identity.sso_settings SET oidc_provider = 'okta' WHERE id = 1`); !checkViolationOf(err, "sso_settings_oidc_provider_check") {
		t.Errorf("provider okta: err = %v, want the sso_settings_oidc_provider_check violation", err)
	}
}

// TestIdentitySsoSettings_DownDropsTheTable proves the down path removes the
// table, its row and the sealed secret with it.
func TestIdentitySsoSettings_DownDropsTheTable(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, identitySsoSettingsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.sso_settings (id, version, oidc_enabled, oidc_provider, oidc_client_secret_sealed, updated_at)
		VALUES (1, 1, true, 'entra', '\x00'::bytea, now())
		ON CONFLICT (id) DO UPDATE SET version = 1`); err != nil {
		t.Fatalf("write the settings row: %v", err)
	}
	pool.Close()

	migrateTo(t, url, identitySsoSettingsVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	if got := staffCollect(t, ctx, pool, "identity.sso_settings", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'identity' AND table_name = 'sso_settings'`); len(got) != 0 {
		t.Errorf("identity.sso_settings after the down = %v, want it gone", got)
	}
}
