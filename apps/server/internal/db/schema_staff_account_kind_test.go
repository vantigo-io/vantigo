package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffAccountKindVersion is 00060_staff_account_kind.sql: the account kind
// per person and the installation's default, the directory profile's
// switches, the providers' last status, and the provider link's columns
// (docs/superpowers/specs/2026-10-19-entra-design.md, D5, D7 and D8).
const staffAccountKindVersion = 60

// staffDirectoryFieldsDefault is settings.directory_fields' default, the
// plan's "Exact values": the switchable fields of the directory profile,
// the work address and the phones off.
const staffDirectoryFieldsDefault = `{"phones": false, "manager": true, "jobTitle": true, "department": true, "companyName": true, "workAddress": false, "employeeType": true, "officeLocation": true, "employeeOrgData": true, "employeeHireDate": true, "preferredLanguage": true, "employeeLeaveDateTime": true}`

// TestStaffAccountKind_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what delivery B's Staff rests
// on: people.account_kind and settings.default_account_kind, local by
// default and held to the three kinds by the module, not by a CHECK (00042's
// house style, which the foundation's and absence's tests pin); the seed's
// marker,
// false until Staff seeds the kinds from the recorded mode; the directory
// profile's switches with their defaults; the providers' last status, empty;
// and the provider link's five columns on staff.identity_links, the reported
// codes an empty text array. A person's second link under a provider's key
// sits beside the identity link, and the Down deletes it.
func TestStaffAccountKind_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffAccountKindVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	column := func(table, name string) []string {
		return staffCollect(t, ctx, pool, "staff."+table+"."+name, `
			SELECT data_type || coalesce('(' || character_maximum_length || ')', '') || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = $1 AND column_name = $2`, table, name)
	}
	for _, c := range []struct{ table, name, want string }{
		{"people", "account_kind", "character varying(10):NO:'local'::character varying"},
		{"settings", "default_account_kind", "character varying(10):NO:'local'::character varying"},
		{"settings", "account_kind_seeded", "boolean:NO:false"},
		{"settings", "directory_fields", "jsonb:NO:'" + staffDirectoryFieldsDefault + "'::jsonb"},
		{"settings", "provider_status", "jsonb:NO:'{}'::jsonb"},
		{"identity_links", "external_upn", "character varying(254):YES:"},
		{"identity_links", "external_email", "character varying(254):YES:"},
		{"identity_links", "announced_at", "timestamp with time zone:YES:"},
		{"identity_links", "pass_issued_at", "timestamp with time zone:YES:"},
		{"identity_links", "reported_codes", "ARRAY:NO:'{}'::text[]"},
	} {
		if got := column(c.table, c.name); !equalStrings(got, []string{c.want}) {
			t.Errorf("staff.%s.%s = %v, want %s", c.table, c.name, got, c.want)
		}
	}

	// The settings row 00042 wrote reads the defaults: not seeded, local,
	// the switches as the plan sets them, no provider seen.
	settings := staffCollect(t, ctx, pool, "the settings row", `
		SELECT default_account_kind || ':' || account_kind_seeded || ':' || directory_fields::text || ':' || provider_status::text
		FROM staff.settings WHERE id = 1`)
	if want := []string{"local:false:" + staffDirectoryFieldsDefault + ":{}"}; !equalStrings(settings, want) {
		t.Errorf("the settings row = %v, want %v", settings, want)
	}

	var kari int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "a new person's kind", `SELECT account_kind FROM staff.people WHERE id = $1`, kari); !equalStrings(got, []string{"local"}) {
		t.Errorf("a new person's kind = %v, want local", got)
	}
	for _, kind := range []string{"local", "external", "entra"} {
		if _, err := pool.Exec(ctx, `UPDATE staff.people SET account_kind = $1 WHERE id = $2`, kind, kari); err != nil {
			t.Errorf("kind %q: %v, want it admitted", kind, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE staff.settings SET default_account_kind = $1 WHERE id = 1`, kind); err != nil {
			t.Errorf("default kind %q: %v, want it admitted", kind, err)
		}
	}

	// The identity link and a provider link side by side: one row per
	// (person, provider), the provider link's columns its own.
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'identity', NULL, 'preparing', true, now(), now())`, kari); err != nil {
		t.Fatalf("the identity link: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, external_upn, external_email,
		    announced_at, pass_issued_at, reported_codes, created_at, updated_at)
		VALUES ($1, 'entra', 'oid-1', 'enabled', true, 'kari.nordmann@fake.example', 'kari.nordmann@fake.example',
		    now(), now(), '{hire_date_not_written}', now(), now())`, kari); err != nil {
		t.Fatalf("a provider link beside it: %v", err)
	}
	links := staffCollect(t, ctx, pool, "the links", `
		SELECT provider || ':' || state || ':' || array_to_string(reported_codes, ',') FROM staff.identity_links
		WHERE person_id = $1 ORDER BY provider`, kari)
	if want := []string{"entra:enabled:hire_date_not_written", "identity:preparing:"}; !equalStrings(links, want) {
		t.Errorf("the links = %v, want %v", links, want)
	}

	migrateTo(t, url, staffAccountKindVersion-1)
	if got := staffCollect(t, ctx, pool, "the links after the rollback", `
		SELECT provider FROM staff.identity_links WHERE person_id = $1 ORDER BY provider`, kari); !equalStrings(got, []string{"identity"}) {
		t.Errorf("the links after the rollback = %v, want the provider link deleted and the identity link kept", got)
	}
	for _, c := range []struct{ table, name string }{
		{"people", "account_kind"}, {"settings", "default_account_kind"}, {"settings", "account_kind_seeded"},
		{"settings", "directory_fields"}, {"settings", "provider_status"}, {"identity_links", "external_upn"},
		{"identity_links", "external_email"}, {"identity_links", "announced_at"}, {"identity_links", "pass_issued_at"},
		{"identity_links", "reported_codes"},
	} {
		if got := column(c.table, c.name); len(got) != 0 {
			t.Errorf("the rollback left staff.%s.%s", c.table, c.name)
		}
	}
	for _, sql := range []string{`DELETE FROM staff.identity_links`, `DELETE FROM staff.people`} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("clean up (%s): %v", sql, err)
		}
	}
}
