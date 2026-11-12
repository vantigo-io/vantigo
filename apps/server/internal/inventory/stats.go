package inventory

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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
	q := store.New(s.deps.Pool)
	row, err := q.Stats(ctx, weekStart(s.deps.Clock(), zone))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the stats: %w", err)
	}
	out := gen.InventoryStats{
		Sites: row.Sites, Locations: row.Locations, StockLines: row.StockLines, ReceiptsThisWeek: row.ReceiptsThisWeek,
		OpenCounts: row.OpenCounts,
	}
	// The inspection queue (manufacturing phase 2 M100), to a caller who
	// may see stock.
	if s.has(ctx, permStockView) {
		n, err := q.CountLotsAwaitingInspection(ctx)
		if err != nil {
			return nil, fmt.Errorf("inventory: count the lots awaiting inspection: %w", err)
		}
		out.LotsAwaitingInspection = &n
	}
	return gen.GetInventoryStats200JSONResponse(out), nil
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
// Each kind of item is gated on its own (phase 2 design I62), so a caller
// sees the items they could act on and no others:
//
//   - countOpenLong: a count still counting more than a week after it opened,
//     oldest first — to a caller who could post or cancel it (counts-manage
//     with both views);
//   - transferOverdue: a transfer on its way past its expected arrival, or
//     without one sent more than a week ago, oldest first — to a caller who
//     could receive it (transfers-manage with both views);
//   - lowStock: one per active site with placements at or below their
//     reorder point, by site code, the count I58's statement's — to a caller
//     with both views;
//   - lotsExpired and lotsExpiring: the lots with stock whose expiry has
//     passed, and those whose expiry falls within 14 days, one item each,
//     counted (manufacturing phase 2 M72) — to a caller with both views.
//
// At most 20 items in all.
func (s *server) GetInventoryStatsAttention(ctx context.Context, _ gen.GetInventoryStatsAttentionRequestObject) (gen.GetInventoryStatsAttentionResponseObject, error) {
	items := []gen.InventoryStatsAttentionItem{}
	views := s.has(ctx, permLocationsView) && s.has(ctx, permStockView)
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	if views && s.has(ctx, permCountsManage) {
		rows, err := q.LongOpenCounts(ctx, now.Add(-countOpenLongAfter))
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
	}
	if views && s.has(ctx, permTransfersManage) && len(items) < maxAttentionItems {
		overdue, err := s.overdueItems(ctx, q, now)
		if err != nil {
			return nil, err
		}
		items = append(items, overdue...)
	}
	if views && len(items) < maxAttentionItems {
		low, err := s.lowStockItems(ctx, q, now)
		if err != nil {
			return nil, err
		}
		items = append(items, low...)
	}
	if views && len(items) < maxAttentionItems {
		lots, err := s.expiringItems(ctx, q, now)
		if err != nil {
			return nil, err
		}
		items = append(items, lots...)
	}
	if len(items) > maxAttentionItems {
		items = items[:maxAttentionItems]
	}
	return gen.GetInventoryStatsAttention200JSONResponse(items), nil
}

// maxAttentionItems is the most items the attention endpoint answers.
const maxAttentionItems = 20

// overdueItems is the transferOverdue items (I62): occurredAt is the
// expected arrival, at the start of that day in the business zone, or the
// moment it was sent.
func (s *server) overdueItems(ctx context.Context, q *store.Queries, now time.Time) ([]gen.InventoryStatsAttentionItem, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := businessToday(now, zone)
	rows, err := q.OverdueTransfers(ctx, store.OverdueTransfersParams{
		Today: pgtype.Date{Time: today, Valid: true}, SentBefore: now.Add(-transferOverdueAfter),
	})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the overdue transfers: %w", err)
	}
	var siteIDs []int64
	for _, r := range rows {
		siteIDs = append(siteIDs, r.FromSiteID, r.ToSiteID)
	}
	sites, err := q.SitesByIDs(ctx, distinct(siteIDs))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites of overdue transfers: %w", err)
	}
	codes := make(map[int64]string, len(sites))
	for _, site := range sites {
		codes[site.ID] = site.Code
	}
	out := make([]gen.InventoryStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		id := strconv.FormatInt(r.ID, 10)
		item := gen.InventoryStatsAttentionItem{Id: string(gen.TransferOverdue) + "/" + id, Type: gen.TransferOverdue, EntityId: id}
		if r.ExpectedArrival.Valid {
			d := r.ExpectedArrival.Time
			item.OccurredAt = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, zone)
			item.Title = fmt.Sprintf("Transfer %d from %s to %s was expected %s", r.Number, codes[r.FromSiteID], codes[r.ToSiteID], d.Format("2006-01-02"))
		} else if r.SentAt != nil {
			days := int(now.Sub(*r.SentAt) / (24 * time.Hour))
			item.OccurredAt = *r.SentAt
			item.Title = fmt.Sprintf("Transfer %d from %s to %s has been on its way for %d days", r.Number, codes[r.FromSiteID], codes[r.ToSiteID], days)
		}
		out = append(out, item)
	}
	return out, nil
}
