package main

// The demo's OAuth connections (OAuth connections design C8, C25, A18): a
// client registered by hand, "VS Code", with the redirect URIs VS Code
// uses, and a connection of it to "Webshop sync" that the demo Owner
// approved for Read only an hour before the seed's clock, with a few
// requests in the log, so Registered clients, Connected apps and the
// Requests tab's filter by app have something to show. The connection holds
// no token: nothing can use it. A rerun, or a workspace that has a client of
// the name, adds nothing; without the demo Owner or the demo integration only
// the client is registered.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const demoOAuthClientName = "VS Code"

// demoOAuthRedirects are the redirect URIs VS Code signs in with: a loopback
// port of its choosing, and vscode.dev's.
var demoOAuthRedirects = []string{"http://127.0.0.1/", "https://vscode.dev/redirect"}

func seedDemoOAuth(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, now time.Time) error {
	seeded, connected := false, false
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity.oauth_clients WHERE name = $1 AND archived_at IS NULL)`,
			demoOAuthClientName).Scan(&exists); err != nil {
			return fmt.Errorf("check the demo OAuth client: %w", err)
		}
		if exists {
			return nil
		}
		var owner *uuid.UUID
		var ownerID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM identity.users WHERE normalized_email = upper($1)`, demoOwnerEmail).Scan(&ownerID); err == nil {
			owner = &ownerID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read the demo owner: %w", err)
		}
		clientID := identity.MintOAuthClientID()
		if _, err := tx.Exec(ctx, `INSERT INTO identity.oauth_clients (id, client_id, name, redirect_uris, created_by, created_at, updated_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $6, $7)`, uuid.New(), clientID, demoOAuthClientName, demoOAuthRedirects, owner, now, uuid.New()); err != nil {
			return fmt.Errorf("register the demo OAuth client: %w", err)
		}
		seeded = true
		if owner == nil {
			return nil
		}
		var integration uuid.UUID
		err := tx.QueryRow(ctx, `SELECT user_id FROM identity.integrations WHERE lower(name) = lower($1) AND archived_at IS NULL`,
			demoIntegrationName).Scan(&integration)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read the demo integration: %w", err)
		}
		connection := uuid.New()
		approved := now.Add(-time.Hour)
		if _, err := tx.Exec(ctx, `INSERT INTO identity.oauth_connections (id, integration_id, client_id, client_kind, client_name, client_host,
				redirect_uri, scopes, authorized_by, created_at, activated_at, expires_at, last_used_at, last_used_ip)
			VALUES ($1, $2, $3, 'registered', $4, '127.0.0.1', 'http://127.0.0.1:33418/', ARRAY['read'], $5, $6, $6, $7, $8, '127.0.0.1')`,
			connection, integration, clientID, demoOAuthClientName, owner, approved, approved.Add(180*24*time.Hour), now.Add(-10*time.Minute)); err != nil {
			return fmt.Errorf("connect the demo OAuth client: %w", err)
		}
		var rows [][]any
		for i, op := range []string{"getCustomers", "getProducts", "getProducts", "getSalesOrder", "getCustomers", "createSalesOrder"} {
			method, status, reason := "GET", int16(200), (*string)(nil)
			if op == "createSalesOrder" {
				// Approved for Read only: the write is the connection's scope's refusal.
				r := "insufficient_scope"
				method, status, reason = "POST", 403, &r
			}
			rows = append(rows, []any{approved.Add(time.Duration(i+1) * 7 * time.Minute), connection, integration, method, op, status, reason, int32(14 + i), "127.0.0.1"})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"identity", "api_requests"},
			[]string{"at", "connection_id", "integration_id", "method", "operation_id", "status", "reason", "duration_ms", "client_ip"},
			pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("seed the demo connection's requests: %w", err)
		}
		connected = true
		return nil
	})
	if err != nil {
		return err
	}
	if seeded {
		logger.Info("OAuth demo seeded", "client", demoOAuthClientName, "connected", connected)
	}
	return nil
}
