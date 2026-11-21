package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The request log's filter by connection reads ix_api_requests_connection_at
// (OAuth connections design A18): each of the three queries the Requests tab
// runs for one connection is explained, as sqlc sends it, over an integration
// with thousands of key rows and a few of the connection's. Sequential scans
// are switched off in the transaction, so the plan does not turn on the
// table's size, and the choice left is the integration's index or the
// connection's.
func TestAPIRequestsByConnectionUseItsIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, _ := testdb.Migrated(t)
	integration, key, connection := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.api_requests (at, key_id, connection_id, integration_id, method, operation_id, status, duration_ms)
		SELECT $4::timestamptz - make_interval(secs => g), CASE WHEN g % 100 = 0 THEN NULL ELSE $2::uuid END,
		       CASE WHEN g % 100 = 0 THEN $3::uuid END, $1, 'GET', 'getCustomers', 200, 5
		FROM generate_series(1, 6000) AS g`, integration, key, connection, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE identity.api_requests`); err != nil {
		t.Fatal(err)
	}
	from, to := now.Add(-30*24*time.Hour), now.Add(time.Minute)
	for name, c := range map[string]struct {
		query string
		args  []any
	}{
		"days":       {aPIRequestDailyCountsByConnection, []any{&connection, integration, from, to, nil, nil}},
		"operations": {aPIRequestTopOperationsByConnection, []any{&connection, integration, from, to, nil, nil, int32(10)}},
		"latest":     {aPIRequestsLatestByConnection, []any{&connection, integration, from, to, nil, nil, int32(200)}},
	} {
		plan := explain(ctx, t, pool, c.query, c.args)
		// A partition's copy of the index is named after its columns.
		if !strings.Contains(plan, "connection_id_at") && !strings.Contains(plan, "ix_api_requests_connection_at") {
			t.Errorf("%s does not read the connection's index:\n%s", name, plan)
		}
	}
}

// explain answers the plan of query with args, sequential scans off.
func explain(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args []any) string {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}
