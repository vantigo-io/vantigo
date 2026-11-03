package inventory

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The movements (design I30): a list filtered by site, location (its
// subtree), variant, kind and business day, newest first, at most 500 a page
// by a cursor on (posted_at, id); and one movement with its lines.

const (
	defaultMovementPage = 100
	maxMovementPage     = 500
	unknownPoster       = "Unknown user"
)

// posterOf is who posted a movement, as snapshotted when they did. The
// snapshot is the ledger's record and outlives an erasure of the person.
func posterOf(userID *uuid.UUID, display *string) gen.InventoryActor {
	name := unknownPoster
	if display != nil && *display != "" {
		name = *display
	}
	return gen.InventoryActor{DisplayName: name, UserId: userID}
}

// reasonOf is an adjustment's reason as the contract names it.
func reasonOf(r *string) *gen.InventoryAdjustmentReason {
	if r == nil {
		return nil
	}
	reason := gen.InventoryAdjustmentReason(*r)
	return &reason
}

// sourceOf is a movement's source reference (design §12), nil when none.
func sourceOf(m store.InventoryMovement) *gen.InventorySourceRef {
	if m.SourceModule == nil || m.SourceKind == nil || m.SourceID == nil {
		return nil
	}
	return &gen.InventorySourceRef{Module: *m.SourceModule, Kind: *m.SourceKind, Id: *m.SourceID, LineId: m.SourceLineID}
}

// movementOf reads one movement as the contract answers it: its header, site
// and lines from one snapshot, then the lines' variants from the catalog
// outside it — a variant the catalog no longer knows named by the line's own
// snapshot. An unknown id is the 404.
func (s *server) movementOf(ctx context.Context, id int64) (gen.InventoryMovement, error) {
	var (
		m         store.InventoryMovement
		lines     []store.InventoryMovementLine
		site      store.InventorySite
		locations = map[int64]store.InventoryLocation{}
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		m, err = q.GetMovement(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: read movement %d: %w", id, err)
		}
		if lines, err = q.MovementLines(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the lines of movement %d: %w", id, err)
		}
		sites, err := q.SitesByIDs(ctx, []int64{m.SiteID})
		if err != nil || len(sites) != 1 {
			return fmt.Errorf("inventory: read the site of movement %d: %w", id, err)
		}
		site = sites[0]
		var ids []int64
		for _, l := range lines {
			for _, p := range []*int64{l.FromLocationID, l.ToLocationID} {
				if p != nil {
					ids = append(ids, *p)
				}
			}
		}
		rows, err := q.LocationsByIDs(ctx, distinct(ids))
		if err != nil {
			return fmt.Errorf("inventory: read the locations of movement %d: %w", id, err)
		}
		for _, r := range rows {
			locations[r.ID] = r
		}
		return nil
	})
	if err != nil {
		return gen.InventoryMovement{}, err
	}
	variantIDs := make([]int32, 0, len(lines))
	for _, l := range lines {
		variantIDs = append(variantIDs, l.VariantID)
	}
	entries, err := s.catalogVariants(ctx, distinct(variantIDs))
	if err != nil {
		return gen.InventoryMovement{}, err
	}
	refs := make(map[int32]gen.InventoryVariantRef, len(entries))
	for _, e := range entries {
		refs[e.ID] = variantRefOf(e)
	}
	out := gen.InventoryMovement{
		Id: m.ID, ClientRequestId: m.ClientRequestID, Kind: gen.InventoryMovementKind(m.Kind), Site: siteRefOf(site),
		Reason: reasonOf(m.Reason), Note: m.Note, SupplierName: m.SupplierName, SupplierReference: m.SupplierReference,
		Source: sourceOf(m), PostedBy: posterOf(m.PostedByUserID, m.PostedByDisplay), PostedAt: m.PostedAt, CountId: m.CountID,
		Lines: make([]gen.InventoryMovementLine, 0, len(lines)),
	}
	for _, l := range lines {
		ref, ok := refs[l.VariantID]
		if !ok {
			ref = deletedVariantRef(l.VariantID, l.Sku, l.ProductName, l.Unit)
		}
		line := gen.InventoryMovementLine{LineNo: l.LineNo, Variant: ref, Quantity: wireQuantity(ratFromNumeric(l.Quantity)), Entered: snapshotOf(l.Entered)}
		if l.FromLocationID != nil {
			line.From = ptr(locationRefOf(locations[*l.FromLocationID]))
		}
		if l.ToLocationID != nil {
			line.To = ptr(locationRefOf(locations[*l.ToLocationID]))
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}

// GetInventoryMovement Get a movement
// (GET /api/v1/inventory/movements/{id})
func (s *server) GetInventoryMovement(ctx context.Context, req gen.GetInventoryMovementRequestObject) (gen.GetInventoryMovementResponseObject, error) {
	mv, err := s.movementOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryMovement404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.GetInventoryMovement503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryMovement200JSONResponse(mv), nil
}

// ListInventoryMovements List movements
// (GET /api/v1/inventory/movements)
//
// from and to are business days in the workspace's zone, to inclusive. The
// cursor is the last row's (posted_at, id), opaque to the client.
func (s *server) ListInventoryMovements(ctx context.Context, req gen.ListInventoryMovementsRequestObject) (gen.ListInventoryMovementsResponseObject, error) {
	p := req.Params
	var f fieldErrors
	params := store.ListMovementsParams{SiteID: p.SiteId, VariantID: p.VariantId, LocationID: p.LocationId, RowLimit: defaultMovementPage + 1}
	if p.Kind != nil {
		if !p.Kind.Valid() {
			f.add("kind", "The kind must be adjustment, move, receipt or count")
		}
		params.Kind = ptr(string(*p.Kind))
	}
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxMovementPage {
			f.add("limit", fmt.Sprintf("The limit is 1 to %d", maxMovementPage))
		}
		params.RowLimit = *p.Limit + 1
	}
	if p.Cursor != nil {
		at, id, ok := parseCursor(*p.Cursor)
		if !ok {
			f.add("cursor", "The cursor is not one this list gave")
		}
		params.CursorAt, params.CursorID = &at, &id
	}
	if p.From != nil && p.To != nil && p.To.Before(p.From.Time) {
		f.add("to", "The last day is before the first")
	}
	if f.failed() {
		return gen.ListInventoryMovements400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	if p.From != nil || p.To != nil {
		zone, err := s.businessZone(ctx)
		if err != nil {
			return nil, err
		}
		if p.From != nil {
			params.PostedFrom = ptr(startOfDay(p.From.Time, zone))
		}
		if p.To != nil {
			params.PostedBefore = ptr(startOfDay(p.To.Time, zone).AddDate(0, 0, 1))
		}
	}
	rows, err := store.New(s.deps.Pool).ListMovements(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("inventory: list movements: %w", err)
	}
	page := int(params.RowLimit) - 1
	out := gen.InventoryMovementPage{Movements: make([]gen.InventoryMovementSummary, 0, min(len(rows), page))}
	if len(rows) > page {
		last := rows[page-1]
		out.NextCursor = ptr(formatCursor(last.PostedAt, last.ID))
		rows = rows[:page]
	}
	var siteIDs []int64
	for _, r := range rows {
		siteIDs = append(siteIDs, r.SiteID)
	}
	sites, err := store.New(s.deps.Pool).SitesByIDs(ctx, distinct(siteIDs))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites of movements: %w", err)
	}
	byID := make(map[int64]store.InventorySite, len(sites))
	for _, s := range sites {
		byID[s.ID] = s
	}
	for _, r := range rows {
		out.Movements = append(out.Movements, gen.InventoryMovementSummary{
			Id: r.ID, Kind: gen.InventoryMovementKind(r.Kind), Site: siteRefOf(byID[r.SiteID]), LineCount: r.LineCount,
			Reason: reasonOf(r.Reason), Note: r.Note, SupplierName: r.SupplierName, SupplierReference: r.SupplierReference,
			PostedBy: posterOf(r.PostedByUserID, r.PostedByDisplay), PostedAt: r.PostedAt, CountId: r.CountID,
		})
	}
	return gen.ListInventoryMovements200JSONResponse(out), nil
}

// startOfDay is the instant a civil day begins in zone.
func startOfDay(day time.Time, zone *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
}

// formatCursor is a page's last row as the opaque cursor of the next page:
// its posting time in microseconds — the column's precision — and its id.
func formatCursor(at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + ":" + strconv.FormatInt(id, 10)))
}

// parseCursor reads a cursor formatCursor made.
func parseCursor(cursor string) (time.Time, int64, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, false
	}
	micros, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, 0, false
	}
	m, err1 := strconv.ParseInt(micros, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil {
		return time.Time{}, 0, false
	}
	return time.UnixMicro(m).UTC(), n, true
}
