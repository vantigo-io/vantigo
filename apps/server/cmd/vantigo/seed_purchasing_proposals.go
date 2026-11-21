package main

// The demo seed's proposals' source (purchasing design, delivery C): the
// workshop's screws, plywood and powder paint get a reorder point and a
// maximum above what the workshop holds and has on order, and — Manufacturing
// being on wherever the workshop is, its buy suggestions are the proposals'
// one source (A4) — a replenishment run for every site is asked for and run
// as the worker would, after the purchase orders were sent, so Purchasing's
// **Proposals** shows rows from the first run, each with its supplier item.
// Every write goes through the modules' own API as the owner and the planner.
// It reruns cleanly, judging each part on its own: a placement that already
// has a reorder point is left as it is, and the run is asked for only while
// none of these variants has an open buy suggestion. It runs after the
// purchasing demo, whose supplier items the proposals are ordered from, and
// does nothing without them.

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// demoProposalPlacements are the reorder points the proposals come from, in
// the variant's unit, at the workshop's raw-material store.
var demoProposalPlacements = []struct {
	product               string
	reorderPoint, maximum float64
}{
	{"screw", 4000, 8000},
	{"plywood", 40, 60},
	{"paint", 120, 200},
}

// seedDemoPurchaseProposals seeds the proposals' source.
func seedDemoPurchaseProposals(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config) error {
	for _, m := range []string{"purchasing", "inventory", "manufacturing", "products"} {
		if !slices.Contains(cfg.Modules, m) {
			logger.Info("purchase proposals demo not seeded: MODULES leaves out " + m)
			return nil
		}
	}
	var siteID int64
	var items int
	if err := pool.QueryRow(ctx, `SELECT coalesce((SELECT id FROM inventory.sites WHERE code = $1), 0),
		(SELECT count(*) FROM purchasing.supplier_items)`, demoWorkshopSiteCode).Scan(&siteID, &items); err != nil {
		return fmt.Errorf("check the purchase proposals demo: %w", err)
	}
	if siteID == 0 || items == 0 {
		logger.Info("purchase proposals demo: no workshop or no supplier items; skipping")
		return nil
	}
	deps, access, err := moduleDeps(cfg, pool, logger, time.Now)
	if err != nil {
		return err
	}
	mods := businessModules(access)
	handler, _, err := module.ComposeAPI(deps, mods...)
	if err != nil {
		return fmt.Errorf("compose the API: %w", err)
	}
	w := &workshop{ctx: ctx, pool: pool, handler: handler, variants: map[string]int32{}, siteID: siteID}
	defer w.signOut()
	if err := w.load(); err != nil {
		return err
	}
	var store int64
	if err := pool.QueryRow(ctx, `SELECT id FROM inventory.locations WHERE site_id = $1 AND code = 'RAA'`, siteID).Scan(&store); err != nil {
		return fmt.Errorf("read the raw-material store: %w", err)
	}
	var ids []int32
	for _, p := range demoProposalPlacements {
		v := w.variants[p.product]
		ids = append(ids, v)
		var revision int32
		var reorder bool
		err := pool.QueryRow(ctx, `SELECT revision, reorder_point IS NOT NULL FROM inventory.placements WHERE variant_id = $1 AND site_id = $2`,
			v, siteID).Scan(&revision, &reorder)
		body := obj{"primaryLocationId": store, "reorderPoint": p.reorderPoint, "maximum": p.maximum}
		path := fmt.Sprintf("/api/v1/inventory/variants/%d/placements/%d", v, siteID)
		switch {
		case err == nil && reorder:
			continue
		case err == nil:
			_, err = w.put(w.owner, path, body, "If-Match", strconv.Itoa(int(revision)))
		default:
			_, err = w.put(w.owner, path, body, "If-None-Match", "*")
		}
		if err != nil {
			return fmt.Errorf("purchase proposals demo: place %s: %w", p.product, err)
		}
	}
	var open bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM manufacturing.suggestions
		WHERE status = 'open' AND kind = 'buy' AND site_id = $1 AND variant_id = ANY($2))`, siteID, ids).Scan(&open); err != nil {
		return fmt.Errorf("read the buy suggestions: %w", err)
	}
	if open {
		logger.Info("purchase proposals demo: already seeded; skipping")
		return nil
	}
	// Every site's run: the workshop asked for its own site's a moment ago,
	// and a person's request within the minute is answered with that one.
	if _, err := w.post(w.planner, "/api/v1/manufacturing/replenishment/runs", obj{}); err != nil {
		return fmt.Errorf("purchase proposals demo: ask for a replenishment run: %w", err)
	}
	type cycler interface {
		RunCycle(context.Context) (int, error)
	}
	for _, wk := range module.Workers(deps, mods...) {
		if c, ok := wk.(cycler); ok && wk.Name() == "manufacturing.replenishment" {
			if _, err := c.RunCycle(ctx); err != nil {
				return fmt.Errorf("purchase proposals demo: run the replenishment: %w", err)
			}
		}
	}
	logger.Info("purchase proposals demo seeded", "placements", len(demoProposalPlacements))
	return nil
}
