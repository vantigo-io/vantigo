package inventory

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// GetInventoryStats Inventory dashboard figures
// (GET /api/v1/inventory/stats)
//
// The dashboard card: active sites, active locations below the roots, the
// balances (one per variant and location holding stock), and the receipts
// posted since Monday of the business week, in the workspace's zone, and the
// counts still counting.
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
		OpenCounts: row.OpenCounts,
	}), nil
}

// weekStart is the instant the business week of now began: Monday's midnight
// in zone.
func weekStart(now time.Time, zone *time.Location) time.Time {
	local := now.In(zone)
	back := (int(local.Weekday()) + 6) % 7
	return time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, zone)
}

// countOpenLongAfter is how long a count may stay counting before the
// dashboard asks someone to post or cancel it: a week, by which a count
// meant for a day or a weekend has been forgotten, while its expected
// quantities drift from the stock.
const countOpenLongAfter = 7 * 24 * time.Hour

// GetInventoryStatsAttention Inventory attention items
// (GET /api/v1/inventory/stats/attention)
//
// countOpenLong: one item per count still counting more than a week after it
// opened, oldest first, at most 20 — raised only to a caller who could post
// or cancel it (counts-manage with both views); anyone else gets none.
func (s *server) GetInventoryStatsAttention(ctx context.Context, _ gen.GetInventoryStatsAttentionRequestObject) (gen.GetInventoryStatsAttentionResponseObject, error) {
	items := []gen.InventoryStatsAttentionItem{}
	if !s.has(ctx, permCountsManage) || !s.has(ctx, permLocationsView) || !s.has(ctx, permStockView) {
		return gen.GetInventoryStatsAttention200JSONResponse(items), nil
	}
	now := s.deps.Clock()
	rows, err := store.New(s.deps.Pool).LongOpenCounts(ctx, now.Add(-countOpenLongAfter))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the counts open long: %w", err)
	}
	for _, r := range rows {
		days := int(now.Sub(r.OpenedAt) / (24 * time.Hour))
		id := strconv.FormatInt(r.ID, 10)
		items = append(items, gen.InventoryStatsAttentionItem{
			Id: string(gen.CountOpenLong) + "/" + id, Type: gen.CountOpenLong, EntityId: id, OccurredAt: r.OpenedAt,
			Title: fmt.Sprintf("The count of %s has been open for %d days", r.FullCode, days),
		})
	}
	return gen.GetInventoryStatsAttention200JSONResponse(items), nil
}
