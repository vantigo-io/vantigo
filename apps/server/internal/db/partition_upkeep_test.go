package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// partitionUpkeep is every function the server calls at runtime that runs
// DDL, with an argument it accepts (00220, 00221).
var partitionUpkeep = []struct{ name, call string }{
	{"energy.ensure_consumption_partition", `SELECT energy.ensure_consumption_partition($1::date)`},
	{"identity.ensure_api_requests_partition", `SELECT identity.ensure_api_requests_partition($1::date)`},
	{"identity.drop_api_requests_before", `SELECT identity.drop_api_requests_before($1::date)`},
}

// TestPartitionUpkeep_RunsAsTheOwnerForTheRuntimeRole is 00220 and 00221: the runtime
// role the installation guide prescribes owns nothing and may create nothing,
// and still keeps the partitions, because each function runs as its owner
// with search_path pinned. Each refuses a NULL or an out-of-range date, so the
// elevated function makes or drops only partitions of its own table within
// its range.
func TestPartitionUpkeep_RunsAsTheOwnerForTheRuntimeRole(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.MigratedAsRuntime(t)
	ctx := context.Background()

	var owns int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE pg_get_userbyid(c.relowner) = current_user OR pg_get_userbyid(n.nspowner) = current_user`).Scan(&owns); err != nil {
		t.Fatal(err)
	}
	if owns != 0 {
		t.Fatalf("the runtime role owns %d relations, want none", owns)
	}
	var canCreate bool
	if err := pool.QueryRow(ctx, `SELECT has_schema_privilege('energy', 'CREATE') OR has_schema_privilege('identity', 'CREATE')`).Scan(&canCreate); err != nil {
		t.Fatal(err)
	}
	if canCreate {
		t.Fatal("the runtime role may create in energy or identity, want neither")
	}

	for _, f := range partitionUpkeep {
		var definer bool
		var config []string
		var publicExecute bool
		if err := pool.QueryRow(ctx, `
			SELECT p.prosecdef, coalesce(p.proconfig, '{}'),
			       EXISTS (SELECT 1 FROM aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a
			               WHERE a.grantee = 0 AND a.privilege_type = 'EXECUTE')
			FROM pg_proc p WHERE p.oid = $1::regprocedure`, f.name+"(date)").Scan(&definer, &config, &publicExecute); err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		if !definer {
			t.Errorf("%s is not SECURITY DEFINER", f.name)
		}
		if len(config) != 1 || config[0] != "search_path=pg_catalog, pg_temp" {
			t.Errorf("%s config %v, want search_path pinned to pg_catalog, pg_temp", f.name, config)
		}
		if publicExecute {
			t.Errorf("PUBLIC may execute %s", f.name)
		}
		for _, bad := range []any{nil, "1899-12-31", "2100-01-01"} {
			_, err := pool.Exec(ctx, f.call, bad)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "22023" {
				t.Errorf("%s(%v): %v, want invalid_parameter_value", f.name, bad, err)
			}
		}
	}

	if _, err := pool.Exec(ctx, `SELECT energy.ensure_consumption_partition('2041-05-17')`); err != nil {
		t.Fatalf("ensure_consumption_partition as the runtime role: %v", err)
	}
	var created bool
	if err := pool.QueryRow(ctx, `SELECT identity.ensure_api_requests_partition('2041-05-17')`).Scan(&created); err != nil || !created {
		t.Fatalf("ensure_api_requests_partition as the runtime role: %v, created %v", err, created)
	}
	var dropped int
	if err := pool.QueryRow(ctx, `SELECT identity.drop_api_requests_before('2041-05-18')`).Scan(&dropped); err != nil || dropped == 0 {
		t.Fatalf("drop_api_requests_before as the runtime role: %v, dropped %d", err, dropped)
	}
	for _, name := range []string{"energy.consumption_intervals_2041_05", "identity.api_requests_20410517"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != (name == "energy.consumption_intervals_2041_05") {
			t.Errorf("%s exists: %v", name, exists)
		}
	}
	// The partitions were made by the owner, not the caller.
	var owner string
	if err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'energy.consumption_intervals_2041_05'::regclass`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner == testdb.RuntimeRole {
		t.Errorf("the partition is the runtime role's, want the owner's")
	}
}
