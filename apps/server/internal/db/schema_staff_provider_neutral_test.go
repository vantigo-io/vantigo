package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The staff provider-neutral names (00111_staff_provider_neutral.sql; Google
// Workspace design G6): the code Staff records on a provider link when the
// person's manager has no account in the directory is renamed
// manager_not_in_entra → manager_not_in_directory, in reported_codes and in
// last_error.

// staffProviderNeutralVersion is 00111's goose version.
const staffProviderNeutralVersion = 111

// TestStaffProviderNeutral_RewritesTheManagerCode migrates to the version
// before 00111 with a provider link carrying the old code beside another in
// reported_codes and in last_error, and a link carrying neither; 00111 Up
// rewrites the code in both columns, leaves the other code and the other link
// as they were; Down restores the old code; Up again re-applies.
func TestStaffProviderNeutral_RewritesTheManagerCode(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, staffProviderNeutralVersion-1)
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var person int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('A-1', 'Anne', 'Før', 'Anne Før', now(), now()) RETURNING id`).Scan(&person); err != nil {
		t.Fatalf("a person: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, reported_codes, last_error, created_at, updated_at)
		VALUES ($1, 'entra', 'ext-1', 'enabled', true, '{hire_date_not_written,manager_not_in_entra}', 'manager_not_in_entra', now(), now()),
		       ($1, 'other', 'ext-2', 'enabled', true, '{photo_not_written}', 'unreachable', now(), now())`, person); err != nil {
		t.Fatalf("the provider links: %v", err)
	}
	links := func(what string) []string {
		return staffCollect(t, ctx, pool, what, `
			SELECT provider || ':' || array_to_string(reported_codes, ',') || ':' || coalesce(last_error, '-')
			FROM staff.identity_links ORDER BY provider`)
	}

	migrateTo(t, url, staffProviderNeutralVersion)
	if got, want := links("the links after 00111"), []string{
		"entra:hire_date_not_written,manager_not_in_directory:manager_not_in_directory",
		"other:photo_not_written:unreachable",
	}; !equalStrings(got, want) {
		t.Errorf("links after 00111 = %v, want %v", got, want)
	}

	migrateTo(t, url, staffProviderNeutralVersion-1)
	if got, want := links("the links after 00111 Down"), []string{
		"entra:hire_date_not_written,manager_not_in_entra:manager_not_in_entra",
		"other:photo_not_written:unreachable",
	}; !equalStrings(got, want) {
		t.Errorf("links after 00111 Down = %v, want %v", got, want)
	}

	migrateTo(t, url, staffProviderNeutralVersion)
	if got := links("the links after 00111 again"); len(got) != 2 || got[0] != "entra:hire_date_not_written,manager_not_in_directory:manager_not_in_directory" {
		t.Errorf("links after 00111 again = %v, want the code rewritten", got)
	}
}
