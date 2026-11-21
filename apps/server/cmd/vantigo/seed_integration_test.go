package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestSeedDemoIntegration_OnlyReusesItsOwnRole: a role named Webshop that
// holds other keys than the demo's, or is built in, is never given to the
// integration; the seed skips it and says so.
func TestSeedDemoIntegration_OnlyReusesItsOwnRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	cfg := &config.Config{AppHostname: "localhost"}
	for _, c := range []struct {
		name     string
		builtIn  bool
		keys     []string
		seededOK bool
	}{
		{"the demo's own", false, demoIntegrationPermissions, true},
		{"other keys", false, []string{"customers:view", "customers:delete"}, false},
		{"built in", true, demoIntegrationPermissions, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pool, _ := testdb.Migrated(t)
			roleID := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO identity.roles (id, name, normalized_name, display_name, is_built_in, version, created_at, updated_at)
				VALUES ($1, $2, upper($2), $2, $3, $4, $5, $5)`, roleID, demoIntegrationRole, c.builtIn, uuid.New(), now); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO identity.role_permissions (role_id, permission_key) SELECT $1, k FROM unnest($2::text[]) AS k`, roleID, c.keys); err != nil {
				t.Fatal(err)
			}
			var log bytes.Buffer
			if err := seedDemoIntegration(ctx, pool, slog.New(slog.NewTextHandler(&log, nil)), cfg, io.Discard, now); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.integrations WHERE name = $1`, demoIntegrationName).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if (n == 1) != c.seededOK {
				t.Errorf("%d integrations, want seeded %t; log: %s", n, c.seededOK, log.String())
			}
			if !c.seededOK && !bytes.Contains(log.Bytes(), []byte("integration demo skipped")) {
				t.Errorf("no log line of the skip: %s", log.String())
			}
		})
	}
}

// TestSeedDemoIntegration_ADayOfRequestsOnce: the demo integration gets a
// day of request log rows, naming its key, and a rerun adds none.
func TestSeedDemoIntegration_ADayOfRequestsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, _ := testdb.Migrated(t)
	now := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	cfg := &config.Config{AppHostname: "localhost"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for range 2 {
		if err := seedDemoIntegration(ctx, pool, logger, cfg, io.Discard, now); err != nil {
			t.Fatal(err)
		}
	}
	var rows, keyed, refused, oldest int
	if err := pool.QueryRow(ctx, `SELECT count(*),
			count(*) FILTER (WHERE r.key_id IN (SELECT id FROM identity.api_keys)),
			count(*) FILTER (WHERE r.reason IS NOT NULL),
			count(*) FILTER (WHERE r.at < $1)
		FROM identity.api_requests r JOIN identity.integrations i ON i.user_id = r.integration_id WHERE i.name = $2 AND r.connection_id IS NULL`,
		now.Add(-25*time.Hour), demoIntegrationName).Scan(&rows, &keyed, &refused, &oldest); err != nil {
		t.Fatal(err)
	}
	if rows != 210 || keyed != rows || refused != 5 || oldest != 0 {
		t.Errorf("%d rows, %d naming the key, %d refused, %d older than a day; want 210 of the day, all keyed, 5 refused", rows, keyed, refused, oldest)
	}
}

// TestSeedDemoOAuth_AClientAndAConnectionOnce: the demo registers VS Code,
// connects it to the demo integration as the demo Owner with a few requests
// in the log, and a rerun adds nothing.
func TestSeedDemoOAuth_AClientAndAConnectionOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, _ := testdb.Migrated(t)
	now := time.Date(2026, time.November, 21, 9, 0, 0, 0, time.UTC)
	owner := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at)
		VALUES ($1, $2, upper($2), true, 'Demo Owner', 'person', $3, $4, $4)`, owner, demoOwnerEmail, uuid.New(), now); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{AppHostname: "erp.example.com"}
	for range 2 {
		if err := seedDemoIntegration(ctx, pool, logger, cfg, io.Discard, now); err != nil {
			t.Fatal(err)
		}
	}
	var clients, connections, requests int
	var clientID string
	if err := pool.QueryRow(ctx, `SELECT count(*), min(client_id) FROM identity.oauth_clients WHERE name = $1`, demoOAuthClientName).Scan(&clients, &clientID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.oauth_connections WHERE client_id = $1 AND client_kind = 'registered'
		AND authorized_by = $2 AND activated_at IS NOT NULL`, clientID, owner).Scan(&connections); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.api_requests WHERE connection_id IS NOT NULL AND key_id IS NULL`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if clients != 1 || connections != 1 || requests != 6 || len(clientID) != 28 {
		t.Errorf("%d clients (%q), %d connections, %d requests; want one, one and six", clients, clientID, connections, requests)
	}
}
