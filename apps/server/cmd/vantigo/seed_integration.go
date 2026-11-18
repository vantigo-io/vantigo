package main

// The demo seed's integration (API access design §11, plan step A.7): "Webshop
// sync", an external web shop's account, holding the custom role "Webshop"
// with what a shop needs of Customers, Products and Orders, and one key of
// 90 days with no narrowing. The key is made, and printed once to stdout,
// only when APP_URL is this machine: it is never written anywhere, and on a
// hosted demo the Owner makes one in Workspace admin › API access. A rerun
// leaves an integration that exists as it is and makes no second key.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	demoIntegrationName = "Webshop sync"
	demoIntegrationRole = "Webshop"
)

// demoIntegrationPermissions are the Webshop role's keys.
var demoIntegrationPermissions = []string{
	"customers:view", "customers:create", "customers:update",
	"products:products-view", "products:pricing-view",
	"orders:access",
}

// seedLocalHost reports whether APP_URL names this machine, the only place
// the seed prints a secret.
func seedLocalHost(cfg *config.Config) bool {
	if cfg.AppHostname == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(cfg.AppHostname)
	return err == nil && addr.IsLoopback()
}

func seedDemoIntegration(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config, stdout io.Writer, now time.Time) error {
	var key string
	created := false
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity.integrations WHERE lower(name) = lower($1) AND archived_at IS NULL)`,
			demoIntegrationName).Scan(&exists); err != nil {
			return fmt.Errorf("check the demo integration: %w", err)
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

		var roleID uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM identity.roles WHERE normalized_name = upper($1)`, demoIntegrationRole).Scan(&roleID)
		if errors.Is(err, pgx.ErrNoRows) {
			roleID = uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO identity.roles (id, name, normalized_name, display_name, description, version, created_at, updated_at)
				VALUES ($1, $2, upper($2), $2, 'What the web shop reads and writes: customers, products and their prices, and orders.', $3, $4, $4)`,
				roleID, demoIntegrationRole, uuid.New(), now); err != nil {
				return fmt.Errorf("create the Webshop role: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO identity.role_permissions (role_id, permission_key) SELECT $1, k FROM unnest($2::text[]) AS k`,
				roleID, demoIntegrationPermissions); err != nil {
				return fmt.Errorf("give the Webshop role its keys: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("read the Webshop role: %w", err)
		}

		id := uuid.New()
		email := "integration-" + id.String() + "@integrations.invalid"
		if _, err := tx.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, email_confirmed, display_name, kind, version, created_at, updated_at)
			VALUES ($1, $2, upper($2), true, $3, 'integration', $4, $5, $5)`, id, email, demoIntegrationName, uuid.New(), now); err != nil {
			return fmt.Errorf("create the demo integration's user: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.integrations (user_id, name, description, contact_user_id, created_by, created_at, version)
			VALUES ($1, $2, 'The web shop: it keeps customers, reads the catalogue and its prices, and places orders.', $3, $3, $4, $5)`,
			id, demoIntegrationName, owner, now, uuid.New()); err != nil {
			return fmt.Errorf("create the demo integration: %w", err)
		}
		// The integrations service's licence (00194), on this transaction only.
		if _, err := tx.Exec(ctx, `SELECT set_config('vantigo.integration_roles', $1, true)`, id.String()); err != nil {
			return fmt.Errorf("licence the demo integration's roles: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, roleID); err != nil {
			return fmt.Errorf("give the demo integration its role: %w", err)
		}
		created = true
		if !seedLocalHost(cfg) {
			return nil
		}
		var hash []byte
		var display string
		key, hash, display = identity.MintAPIKey()
		if _, err := tx.Exec(ctx, `INSERT INTO identity.api_keys (id, integration_id, name, token_hash, display, expires_at, created_by, created_at)
			VALUES ($1, $2, 'Development', $3, $4, $5, $6, $7)`, uuid.New(), id, hash, display, now.AddDate(0, 0, 90), owner, now); err != nil {
			return fmt.Errorf("create the demo integration's key: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	logger.Info("integration demo seeded", "integration", demoIntegrationName, "role", demoIntegrationRole, "key_printed", key != "")
	if key != "" {
		_, _ = fmt.Fprintf(stdout, "\nThe %q integration's API key, shown this once (it expires in 90 days):\n\n    %s\n\n", demoIntegrationName, key)
	}
	return nil
}
