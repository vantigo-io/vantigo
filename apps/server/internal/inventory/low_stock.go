package inventory

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The low-stock list (phase 2 design I56–I60): every placement with a reorder
// point at an active site whose available plus incoming is at or below it,
// by site code, the placement's SKU snapshot and the variant id — one
// statement within the schema (store LowStock), its cost the placements with
// a reorder point. Per page, after the statement: the variants through the
// catalog in one call (a variant gone from it is skipped, its placement
// left), the snapshots the catalog read found changed rewritten, what every
// other active site has available, and the draft transfers open to the site
// for the variant. The rule and the rounding are judgeLow's.

// lowStockPage is how many rows a page of the list holds; the CSV reads the
// statement in pages of lowStockExportPage.
const lowStockPage = 50

// lowStockExportPage is how many rows the CSV reads of the statement at a
// time, 1000; settable so a test can read several pages of a few rows.
var lowStockExportPage = func() *atomic.Int32 {
	var n atomic.Int32
	n.Store(1000)
	return &n
}()

// lowStockCursor is a page's last row: (site code, SKU snapshot, variant id).
type lowStockCursor struct {
	SiteCode  string `json:"s"`
	Sku       string `json:"k"`
	VariantID int32  `json:"v"`
}

func formatLowStockCursor(r store.LowStockRow) string {
	raw, _ := json.Marshal(lowStockCursor{SiteCode: r.SiteCode, Sku: r.Sku, VariantID: r.VariantID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseLowStockCursor(text string) (lowStockCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return lowStockCursor{}, false
	}
	var c lowStockCursor
	if json.Unmarshal(raw, &c) != nil || c.SiteCode == "" {
		return lowStockCursor{}, false
	}
	return c, true
}

// lowStockFilter reads the list's and the CSV's shared filter.
func lowStockFilter(f *fieldErrors, siteID *int64, q *string) store.LowStockParams {
	params := store.LowStockParams{SiteID: siteID}
	if q != nil {
		text := strings.TrimSpace(*q)
		if len([]rune(text)) > maxQueryTextLength {
			f.add("q", fmt.Sprintf("A search is at most %d characters", maxQueryTextLength))
		}
		if text != "" {
			params.Q = &text
		}
	}
	return params
}

// lowRow is one low placement as judged, its variant from the catalog.
type lowRow struct {
	row     store.LowStockRow
	variant contracts.VariantEntry
	verdict lowVerdict
}

// staleSnapshots is the snapshots a page's catalog read found changed, the
// catalog's values to write over them.
type staleSnapshots map[int32]contracts.VariantEntry

// refreshSnapshots writes the catalog's SKU and name over the stale snapshots, by
// variant id, so the next read orders and searches by them (I58).
func (s *server) refreshSnapshots(ctx context.Context, stale staleSnapshots) error {
	if len(stale) == 0 {
		return nil
	}
	var params store.RefreshPlacementSnapshotsParams
	for _, id := range slices.Sorted(maps.Keys(stale)) {
		v := stale[id]
		params.VariantIds = append(params.VariantIds, id)
		params.Skus = append(params.Skus, v.SKU)
		params.ProductNames = append(params.ProductNames, v.ProductName)
	}
	if err := store.New(s.deps.Pool).RefreshPlacementSnapshots(ctx, params); err != nil {
		return fmt.Errorf("inventory: refresh the placements' snapshots: %w", err)
	}
	return nil
}

// lowStockRows reads one page of the statement — limit rows after the
// cursor in params — and judges it: the rows whose variant the catalog still
// knows, whether the statement had more, and the snapshots the catalog read
// found changed, added to stale for the caller to refresh.
func (s *server) lowStockRows(ctx context.Context, params store.LowStockParams, limit int, stale staleSnapshots) ([]lowRow, *store.LowStockRow, error) {
	params.RowLimit = int32(limit + 1)
	rows, err := store.New(s.deps.Pool).LowStock(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: read the low-stock list: %w", err)
	}
	var last *store.LowStockRow
	if len(rows) > limit {
		rows = rows[:limit]
		last = &rows[limit-1]
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VariantID)
	}
	entries, err := s.catalogVariants(ctx, distinct(ids))
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int32]contracts.VariantEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	out := make([]lowRow, 0, len(rows))
	for _, r := range rows {
		v, ok := byID[r.VariantID]
		if !ok {
			continue // gone from the catalog (§9 15): skipped, the placement left
		}
		if v.SKU != r.Sku || v.ProductName != r.ProductName {
			stale[v.ID] = v
		}
		out = append(out, lowRow{row: r, variant: v, verdict: judgeLow(lowLevels{
			onHand: ratFromText(r.OnHand), reserved: ratFromText(r.Reserved), incoming: ratFromText(r.Incoming),
			reorderPoint: ratFromText(r.ReorderPoint), maximum: optionalRat(r.Maximum), multiple: optionalRat(r.Multiple),
			packagings: v.Packagings,
		})})
	}
	return out, last, nil
}

// ListInventoryLowStock List low stock
// (GET /api/v1/inventory/low-stock)
func (s *server) ListInventoryLowStock(ctx context.Context, req gen.ListInventoryLowStockRequestObject) (gen.ListInventoryLowStockResponseObject, error) {
	var f fieldErrors
	params := lowStockFilter(&f, req.Params.SiteId, req.Params.Q)
	if req.Params.Cursor != nil {
		c, ok := parseLowStockCursor(*req.Params.Cursor)
		if !ok {
			f.add("cursor", "The cursor is not one this list gave")
		}
		params.CursorSiteCode, params.CursorSku, params.CursorVariantID = &c.SiteCode, &c.Sku, &c.VariantID
	}
	if f.failed() {
		return gen.ListInventoryLowStock400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	// Available is usable on hand on today (manufacturing phase 2 M63).
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	params.Today = pgtype.Date{Time: today, Valid: true}
	stale := staleSnapshots{}
	rows, last, err := s.lowStockRows(ctx, params, lowStockPage, stale)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ListInventoryLowStock503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	// The page's changed snapshots are refreshed now, so a rename is
	// searchable from the next read. A refreshed row may move in the order:
	// past the cursor, the next page shows it again — a (variant, site) a
	// client has already shown is dropped there — and before it, the paging
	// has already passed it. The CSV refreshes after its last page instead.
	if err := s.refreshSnapshots(ctx, stale); err != nil {
		return nil, err
	}
	out := gen.InventoryLowStockPage{Rows: make([]gen.InventoryLowStockRow, 0, len(rows))}
	if last != nil {
		out.Next = ptr(formatLowStockCursor(*last))
	}
	if len(rows) == 0 {
		return gen.ListInventoryLowStock200JSONResponse(out), nil
	}
	q := store.New(s.deps.Pool)
	var variantIDs []int32
	var pairVariants []int32
	var pairSites []int64
	var siteIDs []int64
	for _, r := range rows {
		variantIDs = append(variantIDs, r.row.VariantID)
		pairVariants, pairSites = append(pairVariants, r.row.VariantID), append(pairSites, r.row.SiteID)
		siteIDs = append(siteIDs, r.row.SiteID)
	}
	sites, err := q.SitesByIDs(ctx, distinct(siteIDs))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites of the low-stock list: %w", err)
	}
	siteByID := make(map[int64]store.InventorySite, len(sites))
	for _, site := range sites {
		siteByID[site.ID] = site
	}
	available, err := q.AvailableAtActiveSites(ctx, store.AvailableAtActiveSitesParams{VariantIds: distinct(variantIDs), Today: params.Today})
	if err != nil {
		return nil, fmt.Errorf("inventory: read what other sites have available: %w", err)
	}
	elsewhere := map[int32][]store.AvailableAtActiveSitesRow{}
	for _, a := range available {
		elsewhere[a.VariantID] = append(elsewhere[a.VariantID], a)
	}
	drafts, err := q.OpenDraftsTo(ctx, store.OpenDraftsToParams{VariantIds: pairVariants, SiteIds: pairSites})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the drafts open to the low sites: %w", err)
	}
	open := map[siteKey][]int64{}
	for _, d := range drafts {
		k := siteKey{d.VariantID, d.SiteID}
		open[k] = append(open[k], d.Number)
	}
	for _, r := range rows {
		row := lowStockRowOf(r, siteByID[r.row.SiteID])
		for _, a := range elsewhere[r.row.VariantID] {
			if a.SiteID != r.row.SiteID {
				row.Elsewhere = append(row.Elsewhere, gen.InventoryLowStockElsewhere{
					SiteId: a.SiteID, SiteName: a.SiteName, Available: wireQuantity(ratFromText(a.Available)),
				})
			}
		}
		if numbers := open[siteKey{r.row.VariantID, r.row.SiteID}]; numbers != nil {
			row.OpenDrafts = numbers
		}
		out.Rows = append(out.Rows, row)
	}
	return gen.ListInventoryLowStock200JSONResponse(out), nil
}

// lowStockRowOf is a judged row as the contract answers it, with no
// elsewhere and no drafts yet.
func lowStockRowOf(r lowRow, site store.InventorySite) gen.InventoryLowStockRow {
	v := r.verdict
	row := gen.InventoryLowStockRow{
		Site: siteRefOf(site), Variant: variantRefOf(r.variant),
		OnHand: wireQuantity(ratFromText(r.row.OnHand)), Reserved: wireQuantity(ratFromText(r.row.Reserved)),
		Available: wireQuantity(v.available), Incoming: wireQuantity(ratFromText(r.row.Incoming)),
		ReorderPoint: wireQuantity(ratFromText(r.row.ReorderPoint)), Maximum: optionalWire(optionalRat(r.row.Maximum)), Multiple: optionalWire(optionalRat(r.row.Multiple)),
		Needed: wireQuantity(v.needed), Suggested: wireQuantity(v.suggested),
		Elsewhere: []gen.InventoryLowStockElsewhere{}, OpenDrafts: []int64{},
	}
	if p := v.packaging; p != nil {
		row.Packaging = &gen.InventoryLowStockPackaging{
			Id: p.ID, Name: p.Name, Kind: p.Kind, Quantity: wireQuantity(ratFromText(p.Quantity)), Count: int32(v.count),
		}
	}
	return row
}

// csvByteOrderMark opens the CSV, so a spreadsheet reads it as UTF-8.
const csvByteOrderMark = "\ufeff"

// lowStockColumns is the CSV's header (I60).
var lowStockColumns = []string{"site", "sku", "name", "barcode", "available", "incoming", "reorder_point", "maximum",
	"needed", "suggested", "packaging", "packaging_count"}

// csvDownload is a CSV answered as an attachment, kept out of every cache.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) VisitExportInventoryLowStockResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

// lowStockTextColumns are the CSV's columns of text — site, sku, name,
// barcode and packaging — the ones the formula guard judges; a number, a
// negative available included, is written as it is.
var lowStockTextColumns = map[int]bool{0: true, 1: true, 2: true, 3: true, 10: true}

// writeCSVRow writes one row the house's way (the customers export's):
// semicolons, CRLF, each text cell guarded against a spreadsheet reading it
// as a formula, and every cell quoted when it holds a separator, a quote or
// a line end. text reports which cells are text; nil is every cell.
func writeCSVRow(b *bytes.Buffer, cells []string, text map[int]bool) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteByte(';')
		}
		if (text == nil || text[i]) && strings.IndexAny(cell, "=+-@\t\r") == 0 {
			cell = "'" + cell
		}
		if strings.ContainsAny(cell, ";\"\r\n") {
			cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
		}
		b.WriteString(cell)
	}
	b.WriteString("\r\n")
}

// csvQuantity is a quantity with the decimal comma a Norwegian spreadsheet
// reads.
func csvQuantity(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// ExportInventoryLowStock Export low stock as CSV
// (GET /api/v1/inventory/low-stock.csv)
//
// The list's filter, every row, unpaged: the statement read in pages of a
// thousand, each page judged as the list's is.
func (s *server) ExportInventoryLowStock(ctx context.Context, req gen.ExportInventoryLowStockRequestObject) (gen.ExportInventoryLowStockResponseObject, error) {
	var f fieldErrors
	params := lowStockFilter(&f, req.Params.SiteId, req.Params.Q)
	if f.failed() {
		return gen.ExportInventoryLowStock400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	params.Today = pgtype.Date{Time: businessToday(s.deps.Clock(), zone), Valid: true}
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, lowStockColumns, nil)
	// The snapshots are refreshed after the last page, never between pages,
	// so no row moves past the cursor mid-export; written is the belt to
	// those braces.
	stale := staleSnapshots{}
	written := map[siteKey]bool{}
	for {
		rows, last, err := s.lowStockRows(ctx, params, int(lowStockExportPage.Load()), stale)
		if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
			return gen.ExportInventoryLowStock503ApplicationProblemPlusJSONResponse(p), nil
		}
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			k := siteKey{r.row.VariantID, r.row.SiteID}
			if written[k] {
				continue
			}
			written[k] = true
			row := lowStockRowOf(r, store.InventorySite{Code: r.row.SiteCode})
			cells := []string{r.row.SiteCode, r.variant.SKU, r.variant.ProductName, deref(r.variant.Barcode),
				csvQuantity(row.Available), csvQuantity(row.Incoming), csvQuantity(row.ReorderPoint), "",
				csvQuantity(row.Needed), csvQuantity(row.Suggested), "", ""}
			if row.Maximum != nil {
				cells[7] = csvQuantity(*row.Maximum)
			}
			if p := row.Packaging; p != nil {
				cells[10], cells[11] = p.Name, strconv.Itoa(int(p.Count))
			}
			writeCSVRow(&b, cells, lowStockTextColumns)
		}
		if last == nil {
			break
		}
		params.CursorSiteCode, params.CursorSku, params.CursorVariantID = &last.SiteCode, &last.Sku, &last.VariantID
	}
	if err := s.refreshSnapshots(ctx, stale); err != nil {
		return nil, err
	}
	today := s.deps.Clock().In(zone).Format(time.DateOnly)
	return csvDownload{body: b.Bytes(), fileName: "low-stock-" + today + ".csv"}, nil
}

// lowStockItems is the dashboard's lowStock items (I62): one per active site
// with any, the count I58's statement's, occurredAt the read's own time — a
// placement does not record when it went low.
func (s *server) lowStockItems(ctx context.Context, q *store.Queries, now time.Time) ([]gen.InventoryStatsAttentionItem, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.LowStockCounts(ctx, pgtype.Date{Time: businessToday(now, zone), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("inventory: count the low placements: %w", err)
	}
	out := make([]gen.InventoryStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		id := strconv.FormatInt(r.SiteID, 10)
		title := fmt.Sprintf("%d items at or below their reorder point at %s", r.Low, r.SiteName)
		if r.Low == 1 {
			title = "1 item at or below its reorder point at " + r.SiteName
		}
		out = append(out, gen.InventoryStatsAttentionItem{
			Id: string(gen.LowStock) + "/" + id, Type: gen.LowStock, EntityId: id, Count: ptr(r.Low), OccurredAt: now,
			Title: title,
		})
	}
	return out, nil
}
