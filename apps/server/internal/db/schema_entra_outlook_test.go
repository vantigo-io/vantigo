package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// entraOutlookConnectVersion is 00088_entra_outlook_connect.sql, the switch
// renamed with when it was turned on and the last connect's failure, and
// entraOutlookConnectionsVersion is 00089_entra_outlook_connections.sql, the
// person's delegated connection
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md,
// M2 and M3).
const (
	entraOutlookConnectVersion     = 88
	entraOutlookConnectionsVersion = 89
)

// TestEntraOutlookConnect_AppliesAndIsIdempotent proves the rename applies,
// rolls back and re-applies cleanly: entra.connection's calendar_sync is
// outlook_connect, off, beside three null columns, and the down gives
// calendar_sync back without them.
func TestEntraOutlookConnect_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraOutlookConnectVersion)

	ctx := context.Background()
	columns := func() []string {
		pool, err := db.Open(ctx, url)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		defer pool.Close()
		return staffCollect(t, ctx, pool, "entra.connection's switch columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = 'connection'
			  AND (column_name LIKE 'outlook_%' OR column_name = 'calendar_sync')
			ORDER BY ordinal_position`)
	}
	if got, want := columns(), []string{
		"outlook_connect:boolean:NO:false",
		"outlook_connect_at:timestamp with time zone:YES:",
		"outlook_last_error:character varying(40):YES:",
		"outlook_last_error_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("entra.connection's switch columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	migrateTo(t, url, entraOutlookConnectVersion-1)
	if got, want := columns(), []string{"calendar_sync:boolean:NO:false"}; !equalStrings(got, want) {
		t.Errorf("entra.connection's switch columns after the down = %v, want %v", got, want)
	}
}

// TestEntraOutlookConnections_AppliesAndIsIdempotent proves the table's
// migration applies, rolls back and re-applies cleanly, and pins what the
// connection's queries rest on: every column's type, nullability and
// default, the object id the key, one live row per person — a closing one
// admitted beside it — and the index on changed_at. The down drops it.
func TestEntraOutlookConnections_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraOutlookConnectionsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if got, want := staffCollect(t, ctx, pool, "entra.outlook_connections columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'entra' AND table_name = 'outlook_connections'
		ORDER BY ordinal_position`), []string{
		"object_id:uuid:NO:",
		"user_id:uuid:NO:",
		"tenant_id:uuid:NO:",
		"scopes:ARRAY:NO:",
		"refresh_sealed:bytea:YES:",
		"state:character varying(20):NO:",
		"error_code:character varying(40):YES:",
		"connected_at:timestamp with time zone:NO:",
		"refreshed_at:timestamp with time zone:YES:",
		"used_at:timestamp with time zone:YES:",
		"account_checked_at:timestamp with time zone:YES:",
		"changed_at:timestamp with time zone:NO:",
		"revision:bigint:NO:1",
		"created_at:timestamp with time zone:NO:",
		"updated_at:timestamp with time zone:NO:",
	}; !equalStrings(got, want) {
		t.Errorf("entra.outlook_connections columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "entra", "outlook_connections"); !equalStrings(cols, []string{"object_id"}) {
		t.Errorf("entra.outlook_connections primary key = %v, want [object_id]", cols)
	}
	if got := staffCollect(t, ctx, pool, "the connections' indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'entra' AND tablename = 'outlook_connections' AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`); !equalStrings(got, []string{
		"CREATE INDEX ix_entra_outlook_connections_changed ON entra.outlook_connections USING btree (changed_at)",
		"CREATE UNIQUE INDEX ux_entra_outlook_connections_user ON entra.outlook_connections USING btree (user_id) WHERE ((state)::text <> 'closing'::text)",
	}) {
		t.Errorf("entra.outlook_connections indexes = %v", got)
	}

	user := uuid.New()
	connection := func(state string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.outlook_connections
			    (object_id, user_id, tenant_id, scopes, refresh_sealed, state, connected_at, changed_at, created_at, updated_at)
			VALUES ($1, $2, $3, '{calendars.readwrite}', '\x00', $4, now(), now(), now(), now())`,
			uuid.New(), user, uuid.New(), state)
		return err
	}
	if err := connection("active"); err != nil {
		t.Fatalf("a live connection: %v", err)
	}
	if err := connection("reconnect"); !uniqueViolationOf(err, "ux_entra_outlook_connections_user") {
		t.Errorf("a second live connection of one person: %v, want ux_entra_outlook_connections_user", err)
	}
	for range 2 {
		if err := connection("closing"); err != nil {
			t.Errorf("a closing connection beside the live one: %v", err)
		}
	}

	migrateTo(t, url, entraOutlookConnectionsVersion-1)
	if got := staffCollect(t, ctx, pool, "the table after the down", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'entra' AND table_name = 'outlook_connections'`); len(got) != 0 {
		t.Errorf("entra.outlook_connections after the down = %v, want none", got)
	}
}
