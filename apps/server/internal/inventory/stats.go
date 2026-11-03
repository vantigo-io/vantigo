package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// GetInventoryStats Inventory dashboard figures
// (GET /api/v1/inventory/stats)
//
// The dashboard card: active sites, active locations below the roots, the
// balances (one per variant and location holding stock), and the receipts
// posted since Monday of the business week, in the workspace's zone.
// openCounts is 0 until delivery B brings counts.
func (s *server) GetInventoryStats(ctx context.Context, _ gen.GetInventoryStatsRequestObject) (gen.GetInventoryStatsResponseObject, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	row, err := store.New(s.deps.Pool).Stats(ctx, weekStart(s.deps.Clock(), zone))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the stats: %w", err)
	}
	return gen.GetInventoryStats200JSONResponse(gen.InventoryStats{
		Sites: row.Sites, Locations: row.Locations, StockLines: row.StockLines, ReceiptsThisWeek: row.ReceiptsThisWeek,
	}), nil
}

// weekStart is the instant the business week of now began: Monday's midnight
// in zone.
func weekStart(now time.Time, zone *time.Location) time.Time {
	local := now.In(zone)
	back := (int(local.Weekday()) + 6) % 7
	return time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, zone)
}

// GetInventoryStatsAttention Inventory attention items
// (GET /api/v1/inventory/stats/attention)
//
// Delivery A raises none; delivery B adds countOpenLong.
func (s *server) GetInventoryStatsAttention(context.Context, gen.GetInventoryStatsAttentionRequestObject) (gen.GetInventoryStatsAttentionResponseObject, error) {
	return gen.GetInventoryStatsAttention200JSONResponse([]gen.InventoryStatsAttentionItem{}), nil
}
