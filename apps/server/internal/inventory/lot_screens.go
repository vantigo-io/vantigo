package inventory

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The lot screens (manufacturing phase 2 L1b): the lots list (M60), a lot's
// page (M61) with its edit, and the lot page's own holds (M64). Reading a lot
// needs both views, as every stock read; changing one needs
// inventory:lots-manage beside them (M68). Every write takes the lot FOR NO
// KEY UPDATE first — the lock a hold change and a serial increase take
// (M58) — and tells the lots' live topics on its transaction.

// The lots list's bounds.
const (
	defaultLotLimit = 100
	maxLotLimit     = 500
	// lotPageMovements is how many of a lot's movement lines its page shows.
	lotPageMovements = 200
)

// The lot page's own holds (M64): their source is the lot itself
// (inventory/lot/<id>), so the page releases Inventory's holds and nothing
// another module's document put.
const (
	lotSourceKind        = "lot"
	eventLotUpdated      = "lot_updated"
	codeHoldNotInventory = "hold_not_inventorys"
	invalidLotTitle      = "Invalid lot"
)

// wireDate is a date as the contract's date, nil when none.
func wireDate(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	return &openapi_types.Date{Time: *t}
}

// lotSummaryOf is one row of the lots list.
func lotSummaryOf(l store.InventoryLot, onHand string, ref gen.InventoryVariantRef, today time.Time) gen.InventoryLotSummary {
	return gen.InventoryLotSummary{
		Id: l.ID, Number: l.Number, Serial: l.Serial, Variant: ref, Status: gen.InventoryLotStatus(l.Status),
		Expired: lotExpired(l, today), Expiry: wireDate(dateOrNil(l.Expiry)), BestBefore: wireDate(dateOrNil(l.BestBefore)),
		SupplierLot: l.SupplierLot, OnHand: wireQuantity(ratFromText(onHand)),
	}
}

// listRowLot is a list row's lot.
func listRowLot(r store.ListLotsRow) store.InventoryLot {
	return store.InventoryLot{
		ID: r.ID, VariantID: r.VariantID, Number: r.Number, Serial: r.Serial, SupplierLot: r.SupplierLot,
		ProductionDate: r.ProductionDate, BestBefore: r.BestBefore, Expiry: r.Expiry, ReceivedAt: r.ReceivedAt,
		OriginModule: r.OriginModule, OriginKind: r.OriginKind, OriginID: r.OriginID, Status: r.Status, Note: r.Note,
		Sku: r.Sku, ProductName: r.ProductName, Revision: r.Revision, CreatedByUserID: r.CreatedByUserID,
		CreatedByDisplay: r.CreatedByDisplay, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// formatLotCursor is a page's last row as the opaque cursor of the next:
// (variant, lower(number), id). A number never holds "|".
func formatLotCursor(l store.InventoryLot) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d|%s|%d", l.VariantID, strings.ToLower(l.Number), l.ID))
}

// parseLotCursor reads a cursor formatLotCursor made.
func parseLotCursor(cursor string) (int32, string, int64, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", 0, false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return 0, "", 0, false
	}
	v, err1 := strconv.ParseInt(parts[0], 10, 32)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || parts[1] == "" {
		return 0, "", 0, false
	}
	return int32(v), parts[1], id, true
}

// ListInventoryLots List lots
// (GET /api/v1/inventory/lots)
//
// By (variant, number), a cursor on the last row. Today is read on the pool
// first: a lot is expired when its expiry is before it (M62).
func (s *server) ListInventoryLots(ctx context.Context, req gen.ListInventoryLotsRequestObject) (gen.ListInventoryLotsResponseObject, error) {
	p := req.Params
	var f fieldErrors
	limit := defaultLotLimit
	if p.Limit != nil {
		limit = int(*p.Limit)
		if limit < 1 || limit > maxLotLimit {
			f.add("limit", fmt.Sprintf("The limit is 1 to %d", maxLotLimit))
		}
	}
	params := store.ListLotsParams{SiteID: p.SiteId, VariantID: p.VariantId, WithStock: p.WithStock == nil || *p.WithStock, RowLimit: int32(limit + 1)}
	if p.Q != nil {
		q := strings.TrimSpace(*p.Q)
		switch {
		case utf8.RuneCountInString(q) > maxQueryTextLength:
			f.add("q", fmt.Sprintf("A search is at most %d characters", maxQueryTextLength))
		case q != "":
			params.Q = &q
		}
	}
	if p.Status != nil {
		if !p.Status.Valid() {
			f.add("status", "The status is available, quarantine or blocked")
		}
		params.Status = ptr(string(*p.Status))
	}
	if p.HoldCause != nil {
		if !slices.Contains(holdCauses, string(*p.HoldCause)) {
			f.add("holdCause", "The cause is inspection, deviation, complaint, recall or other")
		}
		params.HoldCause = ptr(string(*p.HoldCause))
	}
	if p.ExpiresBefore != nil {
		params.ExpiresBefore = pgDate(&p.ExpiresBefore.Time)
	}
	if p.Cursor != nil {
		v, number, id, ok := parseLotCursor(*p.Cursor)
		if !ok {
			f.add("cursor", "The cursor is not one this list gave")
		}
		params.AfterVariant, params.AfterNumber, params.AfterID = &v, &number, &id
	}
	if f.failed() {
		return gen.ListInventoryLots400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListLots(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("inventory: list lots: %w", err)
	}
	out := gen.InventoryLotPage{Lots: make([]gen.InventoryLotSummary, 0, min(len(rows), limit))}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextCursor = ptr(formatLotCursor(listRowLot(rows[limit-1])))
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.VariantID)
	}
	refs, err := s.variantRefs(ctx, ids)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ListInventoryLots503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Lots = append(out.Lots, lotSummaryOf(listRowLot(r), r.OnHand, refs[r.VariantID], today))
	}
	return gen.ListInventoryLots200JSONResponse(out), nil
}

// holdOf is one hold as the contract answers it.
func holdOf(h store.InventoryLotHold) gen.InventoryLotHold {
	out := gen.InventoryLotHold{
		Id: h.ID, Kind: gen.InventoryLotHoldKind(h.Kind), Cause: gen.InventoryLotHoldCause(h.Cause), Note: h.Note,
		Source:     gen.InventorySourceRef{Module: h.SourceModule, Kind: h.SourceKind, Id: h.SourceID},
		Inventorys: h.SourceModule == lotOriginModule, CreatedAt: h.CreatedAt, ReleasedAt: h.ReleasedAt, ReleaseNote: h.ReleaseNote,
	}
	if h.CreatedByDisplay != nil {
		out.CreatedBy = &gen.InventoryActor{DisplayName: *h.CreatedByDisplay, UserId: h.CreatedByUserID}
	}
	if h.ReleasedByDisplay != nil {
		out.ReleasedBy = &gen.InventoryActor{DisplayName: *h.ReleasedByDisplay, UserId: h.ReleasedByUserID}
	}
	return out
}

// lotPage reads a lot's page (M61) in one snapshot — the lot, its holds,
// where it lies, its latest movement lines and its files — with the places
// they name, then the variant through the catalog outside it. today is read
// on the pool first. An unknown lot is the 404 refusal.
func (s *server) lotPage(ctx context.Context, id int64) (gen.InventoryLot, error) {
	today, err := s.today(ctx)
	if err != nil {
		return gen.InventoryLot{}, err
	}
	var (
		lot       store.InventoryLot
		holds     []store.InventoryLotHold
		balances  []store.LotBalancesRow
		lines     []store.LotMovementLinesRow
		files     []store.InventoryLotAttachment
		locations = map[int64]store.InventoryLocation{}
		sites     = map[int64]store.InventorySite{}
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if lot, err = q.GetLot(ctx, id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("inventory: read lot %d: %w", id, err)
		}
		if holds, err = q.LotHolds(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the holds of lot %d: %w", id, err)
		}
		if balances, err = q.LotBalances(ctx, &id); err != nil {
			return fmt.Errorf("inventory: read where lot %d lies: %w", id, err)
		}
		if lines, err = q.LotMovementLines(ctx, store.LotMovementLinesParams{LotID: &id, RowLimit: lotPageMovements}); err != nil {
			return fmt.Errorf("inventory: read the movements of lot %d: %w", id, err)
		}
		if files, err = q.LotAttachments(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the files of lot %d: %w", id, err)
		}
		ids := make([]int64, 0, len(balances)+2*len(lines))
		for _, b := range balances {
			ids = append(ids, b.LocationID)
		}
		for _, l := range lines {
			ids = append(ids, idOrZero(l.FromLocationID), idOrZero(l.ToLocationID))
		}
		rows, err := q.LocationsByIDs(ctx, distinct(ids))
		if err != nil {
			return fmt.Errorf("inventory: read the locations of lot %d: %w", id, err)
		}
		siteIDs := make([]int64, 0, len(rows))
		for _, r := range rows {
			locations[r.ID] = r
			siteIDs = append(siteIDs, r.SiteID)
		}
		siteRows, err := q.SitesByIDs(ctx, distinct(siteIDs))
		if err != nil {
			return fmt.Errorf("inventory: read the sites of lot %d: %w", id, err)
		}
		for _, r := range siteRows {
			sites[r.ID] = r
		}
		return nil
	})
	if err != nil {
		return gen.InventoryLot{}, err
	}
	refs, err := s.variantRefs(ctx, []int32{lot.VariantID})
	if err != nil {
		return gen.InventoryLot{}, err
	}
	out := gen.InventoryLot{
		Id: lot.ID, Number: lot.Number, Serial: lot.Serial, Variant: refs[lot.VariantID], Status: gen.InventoryLotStatus(lot.Status),
		Expired: lotExpired(lot, today), Expiry: wireDate(dateOrNil(lot.Expiry)), BestBefore: wireDate(dateOrNil(lot.BestBefore)),
		ProductionDate: wireDate(dateOrNil(lot.ProductionDate)), SupplierLot: lot.SupplierLot, Note: lot.Note,
		ReceivedAt: lot.ReceivedAt, Revision: lot.Revision, CreatedAt: lot.CreatedAt,
		Holds: make([]gen.InventoryLotHold, 0, len(holds)), Locations: make([]gen.InventoryLotLocation, 0, len(balances)),
		Movements: make([]gen.InventoryLotMovementLine, 0, len(lines)), Attachments: make([]gen.InventoryLotAttachment, 0, len(files)),
	}
	if lot.CreatedByDisplay != nil {
		out.CreatedBy = &gen.InventoryActor{DisplayName: *lot.CreatedByDisplay, UserId: lot.CreatedByUserID}
	}
	if lot.OriginModule != nil && lot.OriginKind != nil && lot.OriginID != nil {
		out.Origin = &gen.InventorySourceRef{Module: *lot.OriginModule, Kind: *lot.OriginKind, Id: *lot.OriginID}
	}
	for _, h := range holds {
		out.Holds = append(out.Holds, holdOf(h))
	}
	total := ratFromText("0")
	for _, b := range balances {
		q := ratFromNumeric(b.OnHand)
		total.Add(total, q)
		l := locations[b.LocationID]
		out.Locations = append(out.Locations, gen.InventoryLotLocation{Site: siteRefOf(sites[l.SiteID]), Location: locationRefOf(l), OnHand: wireQuantity(q)})
	}
	slices.SortFunc(out.Locations, func(a, b gen.InventoryLotLocation) int {
		return cmp.Or(strings.Compare(a.Site.Code, b.Site.Code), strings.Compare(a.Location.FullCode, b.Location.FullCode))
	})
	out.OnHand = wireQuantity(total)
	for _, l := range lines {
		m := gen.InventoryLotMovementLine{MovementId: l.MovementID, Kind: gen.InventoryMovementKind(l.Kind), PostedAt: l.PostedAt, LineNo: l.LineNo,
			Quantity: wireQuantity(ratFromNumeric(l.Quantity))}
		if l.FromLocationID != nil {
			m.From = ptr(locationRefOf(locations[*l.FromLocationID]))
		}
		if l.ToLocationID != nil {
			m.To = ptr(locationRefOf(locations[*l.ToLocationID]))
		}
		out.Movements = append(out.Movements, m)
	}
	for _, a := range files {
		out.Attachments = append(out.Attachments, lotAttachmentOf(a))
	}
	return out, nil
}

// GetInventoryLot Get a lot
// (GET /api/v1/inventory/lots/{id})
func (s *server) GetInventoryLot(ctx context.Context, req gen.GetInventoryLotRequestObject) (gen.GetInventoryLotResponseObject, error) {
	out, err := s.lotPage(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryLot404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.GetInventoryLot503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryLot200JSONResponse(out), nil
}

// lockLotForChange takes one lot FOR NO KEY UPDATE, the lock every change of
// a lot takes (M58, M64); an unknown lot is the 404 refusal.
func lockLotForChange(ctx context.Context, txq *store.Queries, id int64) (store.InventoryLot, error) {
	rows, err := txq.LockLots(ctx, []int64{id})
	if err != nil {
		return store.InventoryLot{}, fmt.Errorf("inventory: lock lot %d: %w", id, err)
	}
	if len(rows) == 0 {
		return store.InventoryLot{}, notFound()
	}
	return rows[0], nil
}

// PutInventoryLot Change a lot
// (PUT /api/v1/inventory/lots/{id})
//
// The supplier lot, the three dates and the note, replaced whole under the
// lot's lock and its If-Match revision; the number and the variant never
// change. A changed expiry is judged from the next posting on.
func (s *server) PutInventoryLot(ctx context.Context, req gen.PutInventoryLotRequestObject) (gen.PutInventoryLotResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutInventoryLot400ApplicationProblemPlusJSONResponse(invalid(invalidLotTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.PutInventoryLot400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLotTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, lotUpdateSchema)
	supplierLot := optionalText(&f, "supplierLot", in.SupplierLot, 40)
	note := optionalText(&f, "note", in.Note, 2000)
	if f.failed() {
		return gen.PutInventoryLot400ApplicationProblemPlusJSONResponse(invalid(invalidLotTitle, f)), nil
	}
	date := func(d *openapi_types.Date) *time.Time {
		if d == nil {
			return nil
		}
		return &d.Time
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		cur, err := lockLotForChange(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if cur.Revision != revision {
			return refuse(revisionConflict("lot", cur.Revision, revision))
		}
		next, err := txq.UpdateLot(ctx, store.UpdateLotParams{
			ID: req.Id, SupplierLot: supplierLot, Note: note, ProductionDate: pgDate(date(in.ProductionDate)),
			BestBefore: pgDate(date(in.BestBefore)), Expiry: pgDate(date(in.Expiry)), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("inventory: change lot %d: %w", req.Id, err)
		}
		if err := s.recordChanges(ctx, txq, who, event{EntityKind: entityLot, EntityID: req.Id, Kind: eventLotUpdated}, lotChanges(cur, next)); err != nil {
			return err
		}
		return notifyLots(ctx, tx, []int64{req.Id})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.PutInventoryLot404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryLot409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.lotPage(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PutInventoryLot503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventoryLot200JSONResponse(out), nil
}

// lotChanges is the fields an edit changed, by the contract's names.
func lotChanges(before, after store.InventoryLot) changes {
	c := changes{}
	text := func(name string, a, b *string) {
		if deref(a) != deref(b) {
			c[name] = fieldChange{From: nilIfEmpty(deref(a)), To: nilIfEmpty(deref(b))}
		}
	}
	day := func(name string, a, b *time.Time) {
		fa, fb := "", ""
		if a != nil {
			fa = a.Format(time.DateOnly)
		}
		if b != nil {
			fb = b.Format(time.DateOnly)
		}
		if fa != fb {
			c[name] = fieldChange{From: nilIfEmpty(fa), To: nilIfEmpty(fb)}
		}
	}
	text("supplierLot", before.SupplierLot, after.SupplierLot)
	text("note", before.Note, after.Note)
	day("productionDate", dateOrNil(before.ProductionDate), dateOrNil(after.ProductionDate))
	day("bestBefore", dateOrNil(before.BestBefore), dateOrNil(after.BestBefore))
	day("expiry", dateOrNil(before.Expiry), dateOrNil(after.Expiry))
	return c
}

// nilIfEmpty is v, nil when it is "", so an emptied field leaves out its side.
func nilIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// lotPageSource is the lot page's own holds' source: inventory/lot/<id>.
func lotPageSource(id int64) movementSource {
	return movementSource{module: lotOriginModule, kind: lotSourceKind, id: strconv.FormatInt(id, 10)}
}

// PostInventoryLotHold Put a lot on hold
// (POST /api/v1/inventory/lots/{id}/holds)
//
// One hold of the lot page (M64), as another module's HoldLots puts one: the
// lot FOR NO KEY UPDATE, the hold, its event, the status derived again and
// the topics told, in one transaction.
func (s *server) PostInventoryLotHold(ctx context.Context, req gen.PostInventoryLotHoldRequestObject) (gen.PostInventoryLotHoldResponseObject, error) {
	if req.Body == nil {
		return gen.PostInventoryLotHold400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidLotTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, lotHoldInputSchema)
	if !slices.Contains(holdKinds, string(in.Kind)) {
		f.add("kind", "The kind is quarantine or block")
	}
	if !slices.Contains(holdCauses, string(in.Cause)) {
		f.add("cause", "The cause is inspection, deviation, complaint, recall or other")
	}
	note := optionalText(&f, "note", in.Note, 1000)
	if f.failed() {
		return gen.PostInventoryLotHold400ApplicationProblemPlusJSONResponse(invalid(invalidLotTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := store.New(s.deps.Pool).GetLot(ctx, req.Id); noRows(err) {
		return gen.PostInventoryLotHold404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("inventory: read lot %d: %w", req.Id, err)
	}
	hold := contracts.LotHold{LotID: req.Id, Kind: string(in.Kind), Cause: string(in.Cause), Note: deref(note),
		By: contracts.LotActor{UserID: who.UserID, Display: who.Display}}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		return s.holdLots(ctx, tx, txq, lotPageSource(req.Id), []contracts.LotHold{hold}, []int64{req.Id})
	})
	if err != nil {
		return nil, err
	}
	out, err := s.lotPage(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PostInventoryLotHold503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostInventoryLotHold200JSONResponse(out), nil
}

// ReleaseInventoryLotHold Release a lot's hold
// (POST /api/v1/inventory/lots/{id}/holds/{holdId}/release)
//
// One of Inventory's own holds — the lot page's, a receipt's inspection —
// released with its note under the lot's lock, the status derived again and
// the topics told. A hold another module's document put is shown with its
// source and released there: hold_not_inventorys. A hold released already
// answers the lot as it is.
func (s *server) ReleaseInventoryLotHold(ctx context.Context, req gen.ReleaseInventoryLotHoldRequestObject) (gen.ReleaseInventoryLotHoldResponseObject, error) {
	var f fieldErrors
	var note *string
	if req.Body != nil {
		f = s.unknownProperties(ctx, lotHoldReleaseSchema)
		note = optionalText(&f, "note", req.Body.Note, 1000)
	}
	if f.failed() {
		return gen.ReleaseInventoryLotHold400ApplicationProblemPlusJSONResponse(invalid(invalidLotTitle, f)), nil
	}
	hold, err := store.New(s.deps.Pool).GetLotHold(ctx, store.GetLotHoldParams{ID: req.HoldId, LotID: req.Id})
	if noRows(err) {
		return gen.ReleaseInventoryLotHold404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("inventory: read hold %d of lot %d: %w", req.HoldId, req.Id, err)
	}
	if hold.SourceModule != lotOriginModule {
		return gen.ReleaseInventoryLotHold409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, codeHoldNotInventory,
			"The hold is another module's",
			fmt.Sprintf("The hold was put by %s %s %s and is released there.", hold.SourceModule, hold.SourceKind, hold.SourceID))), nil
	}
	if hold.ReleasedAt == nil {
		who, err := s.actorFor(ctx)
		if err != nil {
			return nil, err
		}
		err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			if _, err := lockLotForChange(ctx, txq, req.Id); err != nil {
				return err
			}
			released, err := txq.ReleaseLotHold(ctx, store.ReleaseLotHoldParams{
				ID: req.HoldId, LotID: req.Id, ReleasedByUserID: who.UserID, ReleasedByDisplay: &who.Display, ReleaseNote: note, Now: s.deps.Clock(),
			})
			if noRows(err) {
				return nil // released meanwhile
			}
			if err != nil {
				return fmt.Errorf("inventory: release hold %d: %w", req.HoldId, err)
			}
			if err := s.recordEvent(ctx, txq, who, event{EntityKind: entityLot, EntityID: req.Id, Kind: eventLotReleased,
				Payload: map[string]any{"holdId": released.ID, "kind": released.Kind, "cause": released.Cause,
					"source": released.SourceModule + "/" + released.SourceKind + "/" + released.SourceID}}); err != nil {
				return err
			}
			if _, err := txq.DeriveLotStatuses(ctx, store.DeriveLotStatusesParams{Ids: []int64{req.Id}, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("inventory: derive the status of lot %d: %w", req.Id, err)
			}
			return notifyLots(ctx, tx, []int64{req.Id})
		})
		if err != nil {
			return nil, err
		}
	}
	out, err := s.lotPage(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.ReleaseInventoryLotHold503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ReleaseInventoryLotHold200JSONResponse(out), nil
}
