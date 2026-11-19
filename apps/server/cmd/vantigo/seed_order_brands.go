package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/color"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// seedDemoOrderBrands puts the demo customers' brands on their quotes and
// orders (brands design B27, B28, delivery E3): a draft starts in the brand
// its customer's default (or its group's) names, and a sent quote or a
// confirmed order carries the snapshot a send or a confirm would have written,
// the brand's name and, when an object store is configured, its light mark
// copied into Orders' own scope under the content-addressed key the module
// itself uses. Without a store the snapshots carry the name alone, which is
// logged. Subscriptions keep the brand for their invoices.
//
// It runs after seedDemoBrands, each run, and touches only a document that has
// no brand yet, so a database seeded before brands existed gets them once. A
// sent or confirmed row's brand is immutable, so the triggers are switched off
// for the update's transaction, as the seed's own rows are written past them.
func seedDemoOrderBrands(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config) error {
	if cfg == nil || !slices.Contains(cfg.Modules, "orders") || !slices.Contains(cfg.Modules, "customers") {
		return nil
	}
	type brandMark struct {
		name     string
		key, sha *string
	}
	marks := map[string]color.NRGBA{"nordlys": {0x0f, 0x76, 0x6e, 0xff}, "fjordkraft demo": {0xb4, 0x53, 0x09, 0xff}}
	brands := map[uuid.UUID]*brandMark{}
	var scoped storage.ObjectStore
	if cfg.StorageProvider != "" {
		base, err := storage.New(cfg)
		if err != nil {
			return fmt.Errorf("build the object store: %w", err)
		}
		if scoped, err = storage.NewScope(base, "orders"); err != nil {
			return fmt.Errorf("scope the object store: %w", err)
		}
	}
	for nameKey, square := range marks {
		var id uuid.UUID
		b := brandMark{}
		if err := pool.QueryRow(ctx, `SELECT id, published->>'name' FROM identity.brands WHERE name_key = $1 AND state = 'published'`, nameKey).Scan(&id, &b.name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the brand was not seeded: nothing to assign
			}
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		if scoped != nil {
			data, err := demoBrandMark(square, color.NRGBA{0x18, 0x20, 0x33, 0xff})
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			sha := hex.EncodeToString(sum[:])
			key := "branding/logo-" + sha + ".png"
			if err := scoped.Put(ctx, key, bytes.NewReader(data), "image/png"); err != nil {
				return fmt.Errorf("store the demo brand's logo: %w", err)
			}
			b.key, b.sha = &key, &sha
		}
		brands[id] = &b
	}
	if len(brands) == 0 {
		return nil
	}

	return db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `ALTER TABLE orders.quotes DISABLE TRIGGER tr_quotes_brand_immutable`); err != nil {
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE orders.orders DISABLE TRIGGER tr_orders_brand_immutable`); err != nil {
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		total, logos := 0, 0
		for _, table := range []string{"quotes", "orders"} {
			rows, err := tx.Query(ctx, fmt.Sprintf(`
				SELECT d.id, d.status, coalesce(c.brand_id, g.brand_id), c.brand_id IS NOT NULL
				FROM orders.%s d
				JOIN customers.customers c ON c.id = d.customer_id
				LEFT JOIN customers.customer_groups g ON g.id = c.group_id
				WHERE d.brand_id IS NULL AND coalesce(c.brand_id, g.brand_id) IS NOT NULL`, table))
			if err != nil {
				return err
			}
			type target struct {
				id     int64
				status string
				brand  uuid.UUID
				own    bool
			}
			var targets []target
			for rows.Next() {
				var t target
				if err := rows.Scan(&t.id, &t.status, &t.brand, &t.own); err != nil {
					rows.Close()
					return err
				}
				targets = append(targets, t)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, t := range targets {
				b, ok := brands[t.brand]
				if !ok {
					continue
				}
				source := "group"
				if t.own {
					source = "customer"
				}
				if t.status == "draft" {
					_, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE orders.%s SET brand_id = $2, brand_source = $3 WHERE id = $1`, table), t.id, t.brand, source)
				} else {
					_, err = tx.Exec(ctx, fmt.Sprintf(`UPDATE orders.%s SET brand_id = $2, brand_source = $3, brand_name = $4,
						brand_logo_object_key = $5, brand_logo_sha256 = $6 WHERE id = $1`, table), t.id, t.brand, source, b.name, b.key, b.sha)
					if b.key != nil {
						logos++
					}
				}
				if err != nil {
					return fmt.Errorf("seed the demo order brands: %w", err)
				}
				total++
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE orders.subscriptions s SET brand_id = coalesce(c.brand_id, g.brand_id)
			FROM customers.customers c LEFT JOIN customers.customer_groups g ON g.id = c.group_id
			WHERE c.id = s.customer_id AND s.brand_id IS NULL AND coalesce(c.brand_id, g.brand_id) IS NOT NULL`); err != nil {
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE orders.orders ENABLE TRIGGER tr_orders_brand_immutable`); err != nil {
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE orders.quotes ENABLE TRIGGER tr_quotes_brand_immutable`); err != nil {
			return fmt.Errorf("seed the demo order brands: %w", err)
		}
		if total > 0 && scoped == nil {
			logger.Info("development seed: demo quotes and orders were given their brands without logos; no object store is configured")
		}
		if total > 0 {
			logger.Info("development seed: demo quotes and orders were given their customers' brands", "documents", total, "snapshotted_logos", logos)
		}
		return nil
	})
}
