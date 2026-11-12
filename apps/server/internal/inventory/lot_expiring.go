package inventory

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The expiring list (manufacturing phase 2 M72): the lots holding stock
// whose expiry falls within a number of days of today in the workspace zone,
// or has passed, by expiry — the list, its CSV, and the dashboard's two lot
// items. Today is read on the pool before the read, as every lot screen
// reads it (M62).

// The expiring list's limits.
const (
	defaultExpiringDays = 30
	maxExpiringDays     = 365
	maxExpiringLots     = 1000
	// expiringAttentionDays is how far ahead the dashboard's lotsExpiring
	// looks.
	expiringAttentionDays = 14
)

// expiringColumns are the expiring CSV's header.
var expiringColumns = []string{"lot", "sku", "product", "supplier_lot", "expiry", "best_before", "days_left", "status", "on_hand"}

// expiringTextColumns are the CSV's columns of text, the ones the formula
// guard judges.
var expiringTextColumns = map[int]bool{0: true, 1: true, 2: true, 3: true, 7: true}

// expiringFilter is the list's filter judged, and the last expiry day it
// reaches from today.
func expiringFilter(f *fieldErrors, days *int32, siteID *int64, status *gen.InventoryLotStatus, today time.Time) store.ExpiringLotsParams {
	n := defaultExpiringDays
	if days != nil {
		n = int(*days)
		if n < 1 || n > maxExpiringDays {
			f.add("days", fmt.Sprintf("The days are 1 to %d", maxExpiringDays))
		}
	}
	params := store.ExpiringLotsParams{SiteID: siteID, Until: pgtype.Date{Time: today.AddDate(0, 0, n), Valid: true}, RowLimit: maxExpiringLots + 1}
	if status != nil {
		if !status.Valid() {
			f.add("status", "The status is available, quarantine or blocked")
		}
		params.Status = ptr(string(*status))
	}
	return params
}

// expiringRows reads the list: the lots, more than the limit cut, and their
// variants from the catalog after the read.
func (s *server) expiringRows(ctx context.Context, params store.ExpiringLotsParams) ([]store.ExpiringLotsRow, bool, map[int32]gen.InventoryVariantRef, error) {
	rows, err := store.New(s.deps.Pool).ExpiringLots(ctx, params)
	if err != nil {
		return nil, false, nil, fmt.Errorf("inventory: list the expiring lots: %w", err)
	}
	more := len(rows) > maxExpiringLots
	if more {
		rows = rows[:maxExpiringLots]
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VariantID)
	}
	refs, err := s.variantRefs(ctx, ids)
	if err != nil {
		return nil, false, nil, err
	}
	return rows, more, refs, nil
}

// expiringLot is a row's lot.
func expiringLot(r store.ExpiringLotsRow) store.InventoryLot {
	return store.InventoryLot{
		ID: r.ID, VariantID: r.VariantID, Number: r.Number, Serial: r.Serial, SupplierLot: r.SupplierLot,
		ProductionDate: r.ProductionDate, BestBefore: r.BestBefore, Expiry: r.Expiry, ReceivedAt: r.ReceivedAt,
		Status: r.Status, Sku: r.Sku, ProductName: r.ProductName,
	}
}

// ListInventoryExpiringLots List lots expiring soon
// (GET /api/v1/inventory/lots/expiring)
func (s *server) ListInventoryExpiringLots(ctx context.Context, req gen.ListInventoryExpiringLotsRequestObject) (gen.ListInventoryExpiringLotsResponseObject, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	params := expiringFilter(&f, req.Params.Days, req.Params.SiteId, req.Params.Status, today)
	if f.failed() {
		return gen.ListInventoryExpiringLots400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	rows, more, refs, err := s.expiringRows(ctx, params)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ListInventoryExpiringLots503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := gen.InventoryLotExpiringPage{
		Lots: make([]gen.InventoryLotSummary, 0, len(rows)), More: more,
		Today: *wireDate(&today), Until: *wireDate(&params.Until.Time),
	}
	for _, r := range rows {
		out.Lots = append(out.Lots, lotSummaryOf(expiringLot(r), r.OnHand, refs[r.VariantID], today))
	}
	return gen.ListInventoryExpiringLots200JSONResponse(out), nil
}

// expiringCSV is the expiring list as an attachment.
type expiringCSV csvDownload

func (d expiringCSV) VisitExportInventoryExpiringLotsResponse(w http.ResponseWriter) error {
	return csvDownload(d).VisitExportInventoryLowStockResponse(w)
}

// ExportInventoryExpiringLots Export lots expiring soon as CSV
// (GET /api/v1/inventory/lots/expiring.csv)
//
// The list's filter, every row it answers: days_left is the expiry less
// today, negative once it has passed.
func (s *server) ExportInventoryExpiringLots(ctx context.Context, req gen.ExportInventoryExpiringLotsRequestObject) (gen.ExportInventoryExpiringLotsResponseObject, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	params := expiringFilter(&f, req.Params.Days, req.Params.SiteId, req.Params.Status, today)
	if f.failed() {
		return gen.ExportInventoryExpiringLots400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	rows, _, _, err := s.expiringRows(ctx, params)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ExportInventoryExpiringLots503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, expiringColumns, nil)
	for _, r := range rows {
		cells := []string{r.Number, r.Sku, r.ProductName, deref(r.SupplierLot), "", "", "", r.Status, csvQuantity(wireQuantity(ratFromText(r.OnHand)))}
		if r.Expiry.Valid {
			cells[4] = r.Expiry.Time.Format(time.DateOnly)
			cells[6] = strconv.Itoa(int(r.Expiry.Time.Sub(today).Hours() / 24))
		}
		if r.BestBefore.Valid {
			cells[5] = r.BestBefore.Time.Format(time.DateOnly)
		}
		writeCSVRow(&b, cells, expiringTextColumns)
	}
	return expiringCSV{body: b.Bytes(), fileName: "expiring-" + today.Format(time.DateOnly) + ".csv"}, nil
}

// expiringItems is the dashboard's two lot items (M72): lotsExpiring, the
// lots with stock whose expiry falls within 14 days, and lotsExpired, those
// whose expiry has passed — each one item, counted, when there is any.
func (s *server) expiringItems(ctx context.Context, q *store.Queries, now time.Time) ([]gen.InventoryStatsAttentionItem, error) {
	today, err := s.today(ctx)
	if err != nil {
		// The lot items need the business day; without the calendar they are
		// left out and the rest of the list still answers.
		s.logger().WarnContext(ctx, "inventory: the expiring lots left out of the attention list", "error", err)
		return nil, nil
	}
	counts, err := q.ExpiringLotCounts(ctx, store.ExpiringLotCountsParams{
		Today: pgtype.Date{Time: today, Valid: true}, Until: pgtype.Date{Time: today.AddDate(0, 0, expiringAttentionDays), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("inventory: count the expiring lots: %w", err)
	}
	var out []gen.InventoryStatsAttentionItem
	item := func(kind gen.InventoryStatsAttentionItemType, n int32, one, many string) {
		if n == 0 {
			return
		}
		title := fmt.Sprintf(many, n)
		if n == 1 {
			title = one
		}
		out = append(out, gen.InventoryStatsAttentionItem{
			Id: string(kind), Type: kind, EntityId: "lots", OccurredAt: now, Title: title, Count: ptr(n),
		})
	}
	item(gen.LotsExpired, counts.Expired, "1 lot in stock has expired", "%d lots in stock have expired")
	item(gen.LotsExpiring, counts.Expiring, "1 lot in stock expires within 14 days", "%d lots in stock expire within 14 days")
	return out, nil
}
