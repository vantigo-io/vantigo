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

// seedDemoInvoiceBrands puts the demo customers' brands on their invoices
// (brands design B27, B28, delivery E2): a draft starts in the brand its
// customer's default (or its group's) names, and an issued invoice carries the
// snapshot an issue would have written, the brand's name and, when an object
// store is configured, its light mark copied into Invoices' scope under the
// content-addressed key the module itself uses. Without a store the issued
// invoices carry the name alone, which is logged.
//
// It runs after seedDemoBrands, each run, and touches only an invoice that has
// no brand yet, so a database seeded before brands existed gets them once.
// Issued rows are immutable, so the trigger is switched off for the update's
// transaction, as the seed's own issued rows are written past it.
func seedDemoInvoiceBrands(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config) error {
	if cfg == nil || !slices.Contains(cfg.Modules, "invoices") || !slices.Contains(cfg.Modules, "customers") {
		return nil
	}
	type brandMark struct {
		id   uuid.UUID
		name string
		key  *string
		sha  *string
	}
	marks := map[string]color.NRGBA{"nordlys": {0x0f, 0x76, 0x6e, 0xff}, "fjordkraft demo": {0xb4, 0x53, 0x09, 0xff}}
	brands := map[uuid.UUID]*brandMark{}
	var scoped storage.ObjectStore
	if cfg.StorageProvider != "" {
		base, err := storage.New(cfg)
		if err != nil {
			return fmt.Errorf("build the object store: %w", err)
		}
		if scoped, err = storage.NewScope(base, "invoices"); err != nil {
			return fmt.Errorf("scope the object store: %w", err)
		}
	}
	for nameKey, square := range marks {
		var b brandMark
		if err := pool.QueryRow(ctx, `SELECT id, published->>'name' FROM identity.brands WHERE name_key = $1 AND state = 'published'`, nameKey).Scan(&b.id, &b.name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the brand was not seeded: nothing to assign
			}
			return fmt.Errorf("seed the demo invoice brands: %w", err)
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
		brands[b.id] = &b
	}
	if len(brands) == 0 {
		return nil
	}

	return db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT i.id, i.status, coalesce(c.brand_id, g.brand_id), c.brand_id IS NOT NULL
			FROM invoices.invoices i
			JOIN customers.customers c ON c.id = i.customer_id
			LEFT JOIN customers.customer_groups g ON g.id = c.group_id
			WHERE i.kind = 'invoice' AND i.brand_id IS NULL AND coalesce(c.brand_id, g.brand_id) IS NOT NULL`)
		if err != nil {
			return err
		}
		type target struct {
			id     int64
			issued bool
			brand  uuid.UUID
			own    bool
		}
		var targets []target
		for rows.Next() {
			var t target
			var status string
			if err := rows.Scan(&t.id, &status, &t.brand, &t.own); err != nil {
				rows.Close()
				return err
			}
			t.issued = status == "issued"
			targets = append(targets, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable`); err != nil {
			return fmt.Errorf("seed the demo invoice brands: %w", err)
		}
		logos := 0
		for _, t := range targets {
			b, ok := brands[t.brand]
			if !ok {
				continue
			}
			source := "group"
			if t.own {
				source = "customer"
			}
			if !t.issued {
				_, err = tx.Exec(ctx, `UPDATE invoices.invoices SET brand_id = $2, brand_source = $3 WHERE id = $1`, t.id, t.brand, source)
			} else {
				_, err = tx.Exec(ctx, `UPDATE invoices.invoices SET brand_id = $2, brand_source = $3, brand_name = $4,
					brand_logo_object_key = $5, brand_logo_sha256 = $6 WHERE id = $1`, t.id, t.brand, source, b.name, b.key, b.sha)
				if b.key != nil {
					logos++
				}
			}
			if err != nil {
				return fmt.Errorf("seed the demo invoice brands: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE invoices.invoices ENABLE TRIGGER tr_invoices_immutable`); err != nil {
			return fmt.Errorf("seed the demo invoice brands: %w", err)
		}
		if scoped == nil {
			logger.Info("development seed: demo invoices were given their brands without logos; no object store is configured")
		}
		logger.Info("development seed: demo invoices were given their customers' brands", "invoices", len(targets), "snapshotted_logos", logos)
		return nil
	})
}
