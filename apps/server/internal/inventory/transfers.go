package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Transfers between sites (phase 2 design I35–I53): a document of the
// module's own that sends stock out of one site, holds it in transit on its
// lines, and receives it at another. This file is the document — its drafts,
// created, changed and deleted freely, since a draft holds nothing (I37) — and
// its reads; transfer_actions.go is what moves stock: send, receive and
// cancel.
//
// A draft's lines are judged against the catalog as it is when they are
// written, read before the transaction: each a goods variant, its quantity
// given in units or in packagings resolved to units now, its SKU and name
// snapshotted (I36). The send resolves and snapshots them again. Every write
// takes the transfer row FOR UPDATE first and checks If-Match against its
// revision; the sites are taken FOR SHARE after it and must be active.

// The transfer's number series (I36), the Orders pattern.
const (
	transferSeries      = "transfer"
	transferSeriesStart = 1001
)

// The limits a transfer's request is held to (I36).
const (
	maxTransferLines     = 500
	maxTransferReference = 100
	maxTransferNote      = 2000
	maxCloseNote         = 500
	defaultTransferPage  = 50
	invalidTransferTitle = "Invalid transfer"
)

// transferOverdueAfter is how long a transfer without an expected arrival may
// stay in transit before it is overdue (I62).
const transferOverdueAfter = 7 * 24 * time.Hour

// The record's words for a transfer (I64).
const (
	entityTransfer = "transfer"

	eventTransferCreated   = "transfer_created"
	eventTransferUpdated   = "transfer_updated"
	eventTransferDeleted   = "transfer_deleted"
	eventTransferSent      = "transfer_sent"
	eventTransferReceived  = "transfer_received"
	eventTransferClosed    = "transfer_closed"
	eventTransferCancelled = "transfer_cancelled"
)

// The statuses as the store keeps them.
const (
	transferDraft          = string(gen.InventoryTransferStatusDraft)
	transferSent           = string(gen.InventoryTransferStatusSent)
	transferPartlyReceived = string(gen.InventoryTransferStatusPartlyReceived)
	transferReceived       = string(gen.InventoryTransferStatusReceived)
	transferCancelled      = string(gen.InventoryTransferStatusCancelled)
)

// transferInTransit reports whether a transfer's status keeps stock on its
// way: sent, or partly received.
func transferInTransit(status string) bool {
	return status == transferSent || status == transferPartlyReceived
}

// draftLine is one line of a draft as its request gave it, its shape judged.
type draftLine struct {
	index      int
	variantID  int32
	quantity   *big.Rat
	entry      *entry
	suggestion []byte
	failCode   string
	failText   string
}

// draft is a transfer's request judged: its header and its lines.
type draft struct {
	fromSiteID, toSiteID int64
	reference, note      *string
	expectedArrival      pgtype.Date
	lines                []draftLine
}

// readDraft judges a create's or a replace's body for its shape: two
// different sites, the texts within their lengths, 1–500 lines, each a
// quantity or an entry.
func (s *server) readDraft(ctx context.Context, in gen.InventoryTransferInput) (draft, fieldErrors) {
	f := s.unknownProperties(ctx, transferInputSchema)
	out := draft{
		fromSiteID: in.FromSiteId, toSiteID: in.ToSiteId,
		reference: optionalText(&f, "reference", in.Reference, maxTransferReference),
		note:      optionalText(&f, "note", in.Note, maxTransferNote),
	}
	if in.FromSiteId == in.ToSiteId {
		f.addCoded("toSiteId", codeTransferSameSite, "A transfer goes from one site to another")
	}
	if in.ExpectedArrival != nil {
		out.expectedArrival = pgtype.Date{Time: in.ExpectedArrival.Time, Valid: true}
	}
	switch n := len(in.Lines); {
	case n == 0:
		f.add("lines", "A transfer has at least one line")
	case n > maxTransferLines:
		f.addCoded("lines", codeTooManyLines, fmt.Sprintf("A transfer has at most %d lines; this one has %d", maxTransferLines, n))
	}
	for i, l := range in.Lines {
		line := draftLine{index: i, variantID: l.VariantId}
		line.quantity, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, positiveQuantity)
		if l.Suggestion != nil {
			raw, err := json.Marshal(l.Suggestion)
			if err != nil {
				f.add(fmt.Sprintf("lines[%d].suggestion", i), "The suggestion could not be kept")
			}
			line.suggestion = raw
		}
		out.lines = append(out.lines, line)
	}
	return out, f
}

// resolvedLine is a draft line judged against the catalog: its quantity in
// units, its entry's snapshot, and the variant's SKU and name.
type resolvedLine struct {
	variantID  int32
	sku, name  string
	quantity   *big.Rat
	entered    []byte
	suggestion []byte
	lineNo     int32
}

// resolveDraft judges the lines against the catalog, read before the
// transaction (I36, I37): each variant known and goods, an entry resolved
// against its packagings as they are now. Every failing line is answered at
// once.
func (s *server) resolveDraft(ctx context.Context, d draft) ([]resolvedLine, error) {
	ids := make([]int32, 0, len(d.lines))
	for _, l := range d.lines {
		ids = append(ids, l.variantID)
	}
	entries, err := s.catalogVariants(ctx, distinct(ids))
	if err != nil {
		return nil, err
	}
	variants := make(map[int32]contracts.VariantEntry, len(entries))
	for _, e := range entries {
		variants[e.ID] = e
	}
	var failures []lineFailure
	out := make([]resolvedLine, 0, len(d.lines))
	for _, l := range d.lines {
		if l.failCode != "" {
			failures = append(failures, lineFailure{Index: l.index, Code: l.failCode, Detail: l.failText})
			continue
		}
		v, ok := variants[l.variantID]
		switch {
		case !ok:
			failures = append(failures, lineFailure{Index: l.index, Code: codeVariantNotFound, Detail: fmt.Sprintf("No variant has the id %d.", l.variantID)})
			continue
		case v.ProductType != productTypeGoods:
			failures = append(failures, lineFailure{Index: l.index, Code: codeVariantNotStockable, Detail: fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU)})
			continue
		}
		r := resolvedLine{variantID: v.ID, sku: v.SKU, name: v.ProductName, quantity: l.quantity, suggestion: l.suggestion, lineNo: int32(l.index + 1)}
		if l.entry != nil {
			q, snap, code, detail := l.entry.resolve(v)
			switch {
			case code != "":
				failures = append(failures, lineFailure{Index: l.index, Code: code, Detail: detail})
				continue
			case q.Sign() == 0:
				failures = append(failures, lineFailure{Index: l.index, Code: codeQuantityInvalid, Detail: "The entry comes to 0; the quantity must be more than 0"})
				continue
			}
			r.quantity, r.entered = q, snap
		}
		out = append(out, r)
	}
	if len(failures) > 0 {
		return nil, refuseLines(failures)
	}
	return out, nil
}

// insertDraftLines writes a draft's lines.
func insertDraftLines(ctx context.Context, txq *store.Queries, transferID int64, lines []resolvedLine) error {
	p := store.InsertTransferLinesParams{TransferID: transferID}
	for _, l := range lines {
		q, err := numericFromRat(l.quantity)
		if err != nil {
			return err
		}
		p.LineNos = append(p.LineNos, l.lineNo)
		p.VariantIds = append(p.VariantIds, l.variantID)
		p.Skus = append(p.Skus, l.sku)
		p.ProductNames = append(p.ProductNames, l.name)
		p.Quantities = append(p.Quantities, q)
		p.Entereds = append(p.Entereds, string(l.entered))
		p.Suggestions = append(p.Suggestions, string(l.suggestion))
	}
	if err := txq.InsertTransferLines(ctx, p); err != nil {
		return fmt.Errorf("inventory: write the lines of transfer %d: %w", transferID, err)
	}
	return nil
}

// shareSites takes the transfer's two sites FOR SHARE in id order (I38 step
// 4) and judges them: each known and active (site_archived).
func shareSites(ctx context.Context, txq *store.Queries, fromID, toID int64) (map[int64]store.InventorySite, error) {
	rows, err := txq.ShareTransferSites(ctx, []int64{fromID, toID})
	if err != nil {
		return nil, fmt.Errorf("inventory: share sites %d and %d: %w", fromID, toID, err)
	}
	sites := make(map[int64]store.InventorySite, len(rows))
	for _, r := range rows {
		sites[r.ID] = r
	}
	var f fieldErrors
	for field, id := range map[string]int64{"fromSiteId": fromID, "toSiteId": toID} {
		site, ok := sites[id]
		switch {
		case !ok:
			f.add(field, "No site has this id")
		case site.Status != string(gen.Active):
			f.addCoded(field, codeSiteArchived, fmt.Sprintf("The site %s is archived", site.Code))
		}
	}
	if f.failed() {
		return nil, refuseInvalid(invalidTransferTitle, f)
	}
	return sites, nil
}

// lockTransfer takes the transfer row FOR UPDATE (I38 step 1); an unknown id
// is the 404.
func lockTransfer(ctx context.Context, txq *store.Queries, id int64) (store.InventoryTransfer, error) {
	t, err := txq.LockTransfer(ctx, id)
	if noRows(err) {
		return store.InventoryTransfer{}, notFound()
	}
	if err != nil {
		return store.InventoryTransfer{}, fmt.Errorf("inventory: lock transfer %d: %w", id, err)
	}
	return t, nil
}

// notDraft is the 409 of an edit, a delete or a send of a transfer that is no
// longer a draft (I37, I39).
func notDraft(t store.InventoryTransfer) error {
	return refuse(problem(http.StatusConflict, codeTransferNotDraft, "Transfer is not a draft",
		fmt.Sprintf("Transfer %d is %s; only a draft is edited, deleted or sent.", t.Number, strings.ReplaceAll(t.Status, "_", " "))))
}

// CreateInventoryTransfer Create a transfer
// (POST /api/v1/inventory/transfers)
//
// A draft (I36, I37): its number taken from the series in the creating
// transaction, so a rolled-back create rolls the number back.
func (s *server) CreateInventoryTransfer(ctx context.Context, req gen.CreateInventoryTransferRequestObject) (gen.CreateInventoryTransferResponseObject, error) {
	if req.Body == nil {
		return gen.CreateInventoryTransfer400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidTransferTitle, "The request has no body.")), nil
	}
	d, f := s.readDraft(ctx, *req.Body)
	if f.failed() {
		return gen.CreateInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, f)), nil
	}
	id, err := s.createTransfer(ctx, d)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.CreateInventoryTransfer400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.CreateInventoryTransfer503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.transferOf(ctx, id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.CreateInventoryTransfer503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.CreateInventoryTransfer201JSONResponse(out), nil
}

// createTransfer writes a draft and answers its id.
func (s *server) createTransfer(ctx context.Context, d draft) (int64, error) {
	lines, err := s.resolveDraft(ctx, d)
	if err != nil {
		return 0, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := shareSites(ctx, txq, d.fromSiteID, d.toSiteID); err != nil {
			return err
		}
		number, err := txq.AllocateNumber(ctx, store.AllocateNumberParams{CounterName: transferSeries, SeriesStart: transferSeriesStart})
		if err != nil {
			return fmt.Errorf("inventory: take a transfer number: %w", err)
		}
		id, err = txq.InsertTransfer(ctx, store.InsertTransferParams{
			Number: number, FromSiteID: d.fromSiteID, ToSiteID: d.toSiteID, Reference: d.reference, Note: d.note,
			ExpectedArrival: d.expectedArrival, CreatedByUserID: who.UserID, CreatedByDisplay: &who.Display, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("inventory: insert transfer: %w", err)
		}
		if err := insertDraftLines(ctx, txq, id, lines); err != nil {
			return err
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityTransfer, EntityID: id, SiteID: &d.fromSiteID, Kind: eventTransferCreated,
			Payload: map[string]any{"number": number, "fromSiteId": d.fromSiteID, "toSiteId": d.toSiteID, "lines": len(lines)},
		})
	})
	return id, err
}

// PutInventoryTransfer Change a draft transfer
// (PUT /api/v1/inventory/transfers/{id})
//
// A draft's header and lines, replaced whole under its row's lock (I37).
func (s *server) PutInventoryTransfer(ctx context.Context, req gen.PutInventoryTransferRequestObject) (gen.PutInventoryTransferResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, badIfMatch())), nil
	}
	if req.Body == nil {
		return gen.PutInventoryTransfer400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidTransferTitle, "The request has no body.")), nil
	}
	d, f := s.readDraft(ctx, *req.Body)
	if f.failed() {
		return gen.PutInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, f)), nil
	}
	err := s.replaceTransfer(ctx, req.Id, revision, d)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutInventoryTransfer400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutInventoryTransfer404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryTransfer409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutInventoryTransfer503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.transferOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PutInventoryTransfer503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventoryTransfer200JSONResponse(out), nil
}

// replaceTransfer is PutInventoryTransfer's write.
func (s *server) replaceTransfer(ctx context.Context, id int64, revision int32, d draft) error {
	lines, err := s.resolveDraft(ctx, d)
	if err != nil {
		return err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return err
	}
	return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		t, err := lockTransfer(ctx, txq, id)
		if err != nil {
			return err
		}
		if t.Status != transferDraft {
			return notDraft(t)
		}
		if t.Revision != revision {
			return refuse(revisionConflict("transfer", t.Revision, revision))
		}
		if _, err := shareSites(ctx, txq, d.fromSiteID, d.toSiteID); err != nil {
			return err
		}
		if err := txq.UpdateTransferHeader(ctx, store.UpdateTransferHeaderParams{
			ID: t.ID, FromSiteID: d.fromSiteID, ToSiteID: d.toSiteID, Reference: d.reference, Note: d.note,
			ExpectedArrival: d.expectedArrival, Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("inventory: update transfer %d: %w", t.ID, err)
		}
		if err := txq.DeleteTransferLines(ctx, t.ID); err != nil {
			return fmt.Errorf("inventory: clear the lines of transfer %d: %w", t.ID, err)
		}
		if err := insertDraftLines(ctx, txq, t.ID, lines); err != nil {
			return err
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityTransfer, EntityID: t.ID, SiteID: &d.fromSiteID, Kind: eventTransferUpdated,
			Payload: map[string]any{"number": t.Number, "fromSiteId": d.fromSiteID, "toSiteId": d.toSiteID, "lines": len(lines)},
		})
	})
}

// DeleteInventoryTransfer Delete a draft transfer
// (DELETE /api/v1/inventory/transfers/{id})
//
// Only a draft is deleted (I37); its lines go with it. Its number is not
// handed out again.
func (s *server) DeleteInventoryTransfer(ctx context.Context, req gen.DeleteInventoryTransferRequestObject) (gen.DeleteInventoryTransferResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.DeleteInventoryTransfer400ApplicationProblemPlusJSONResponse(invalid(invalidTransferTitle, badIfMatch())), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		t, err := lockTransfer(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if t.Status != transferDraft {
			return notDraft(t)
		}
		if t.Revision != revision {
			return refuse(revisionConflict("transfer", t.Revision, revision))
		}
		if err := txq.DeleteTransfer(ctx, t.ID); err != nil {
			return fmt.Errorf("inventory: delete transfer %d: %w", t.ID, err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityTransfer, EntityID: t.ID, SiteID: &t.FromSiteID, Kind: eventTransferDeleted,
			Payload: map[string]any{"number": t.Number},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.DeleteInventoryTransfer404Response{}, nil
		case http.StatusConflict:
			return gen.DeleteInventoryTransfer409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInventoryTransfer204Response{}, nil
}

// transferRead is one transfer as one snapshot found it: the row, its lines,
// actions and action lines, its two sites, and every location they name.
type transferRead struct {
	transfer    store.InventoryTransfer
	lines       []store.InventoryTransferLine
	actions     []store.InventoryTransferAction
	actionLines []store.InventoryTransferActionLine
	sites       map[int64]store.InventorySite
	locations   map[int64]store.InventoryLocation
	variants    map[int32]gen.InventoryVariantRef
}

// readTransfer reads a transfer in one snapshot, then its lines' variants
// from the catalog outside it — a variant the catalog no longer knows named
// by the line's own snapshot (I37). An unknown id is the 404.
func (s *server) readTransfer(ctx context.Context, id int64) (transferRead, error) {
	var r transferRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r.transfer, err = q.GetTransfer(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: read transfer %d: %w", id, err)
		}
		if r.lines, err = q.TransferLines(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the lines of transfer %d: %w", id, err)
		}
		if r.actions, err = q.TransferActions(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the actions of transfer %d: %w", id, err)
		}
		if r.actionLines, err = q.TransferActionLines(ctx, id); err != nil {
			return fmt.Errorf("inventory: read the action lines of transfer %d: %w", id, err)
		}
		sites, err := q.SitesByIDs(ctx, []int64{r.transfer.FromSiteID, r.transfer.ToSiteID})
		if err != nil {
			return fmt.Errorf("inventory: read the sites of transfer %d: %w", id, err)
		}
		r.sites = make(map[int64]store.InventorySite, len(sites))
		for _, s := range sites {
			r.sites[s.ID] = s
		}
		var ids []int64
		for _, l := range r.lines {
			if l.FromLocationID != nil {
				ids = append(ids, *l.FromLocationID)
			}
		}
		for _, l := range r.actionLines {
			if l.LocationID != nil {
				ids = append(ids, *l.LocationID)
			}
		}
		locations, err := q.LocationsByIDs(ctx, distinct(ids))
		if err != nil {
			return fmt.Errorf("inventory: read the locations of transfer %d: %w", id, err)
		}
		r.locations = make(map[int64]store.InventoryLocation, len(locations))
		for _, l := range locations {
			r.locations[l.ID] = l
		}
		return nil
	})
	if err != nil {
		return transferRead{}, err
	}
	ids := make([]int32, 0, len(r.lines))
	for _, l := range r.lines {
		ids = append(ids, l.VariantID)
	}
	entries, err := s.catalogVariants(ctx, distinct(ids))
	if err != nil {
		return transferRead{}, err
	}
	r.variants = make(map[int32]gen.InventoryVariantRef, len(entries))
	for _, e := range entries {
		r.variants[e.ID] = variantRefOf(e)
	}
	return r, nil
}

// variantOf is a line's variant as the catalog names it, or by the line's
// snapshot when the catalog no longer knows it.
func (r transferRead) variantOf(l store.InventoryTransferLine) gen.InventoryVariantRef {
	if ref, ok := r.variants[l.VariantID]; ok {
		return ref
	}
	unit := ""
	if l.Unit != nil {
		unit = *l.Unit
	}
	return deletedVariantRef(l.VariantID, l.Sku, l.ProductName, unit)
}

// inTransitOf is a line's in transit: sent less received, missing and
// returned (I46).
func inTransitOf(l store.InventoryTransferLine) *big.Rat {
	out := ratFromNumeric(l.Sent)
	for _, n := range []pgtype.Numeric{l.Received, l.Missing, l.Returned} {
		out.Sub(out, ratFromNumeric(n))
	}
	return out
}

// suggestionOf is a stored suggestion as the contract answers it, nil for
// none or one that does not read.
func suggestionOf(raw []byte) *gen.InventoryTransferSuggestion {
	if len(raw) == 0 {
		return nil
	}
	var out gen.InventoryTransferSuggestion
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}

// dateOf is a date column as the contract answers it, nil for none.
func dateOf(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// actorOrNil is a snapshotted actor, nil when nobody is recorded.
func actorOrNil(at *time.Time, userID *uuid.UUID, display *string) *gen.InventoryActor {
	if at == nil {
		return nil
	}
	return ptr(posterOf(userID, display))
}

// closeReasonOf is a stored close reason as the contract names it.
func closeReasonOf(r *string) *gen.InventoryTransferCloseReason {
	if r == nil {
		return nil
	}
	return ptr(gen.InventoryTransferCloseReason(*r))
}

// answer is the transfer as the contract answers it.
func (r transferRead) answer() gen.InventoryTransfer {
	t := r.transfer
	out := gen.InventoryTransfer{
		Id: t.ID, Number: t.Number, FromSite: siteRefOf(r.sites[t.FromSiteID]), ToSite: siteRefOf(r.sites[t.ToSiteID]),
		Status: gen.InventoryTransferStatus(t.Status), Reference: t.Reference, Note: t.Note, ExpectedArrival: dateOf(t.ExpectedArrival),
		CloseReason: closeReasonOf(t.CloseReason), CloseNote: t.CloseNote, Revision: t.Revision,
		CreatedBy: posterOf(t.CreatedByUserID, t.CreatedByDisplay), CreatedAt: t.CreatedAt,
		SentAt: t.SentAt, SentBy: actorOrNil(t.SentAt, t.SentByUserID, t.SentByDisplay),
		ClosedAt: t.ClosedAt, ClosedBy: actorOrNil(t.ClosedAt, t.ClosedByUserID, t.ClosedByDisplay), UpdatedAt: t.UpdatedAt,
		Lines: make([]gen.InventoryTransferLine, 0, len(r.lines)), Actions: make([]gen.InventoryTransferAction, 0, len(r.actions)),
	}
	total := new(big.Rat)
	for _, l := range r.lines {
		inTransit := inTransitOf(l)
		total.Add(total, inTransit)
		line := gen.InventoryTransferLine{
			Id: l.ID, LineNo: l.LineNo, Variant: r.variantOf(l), Quantity: wireQuantity(ratFromNumeric(l.Quantity)),
			Entered: snapshotOf(l.Entered), Suggestion: suggestionOf(l.Suggestion),
			Sent: wireQuantity(ratFromNumeric(l.Sent)), Received: wireQuantity(ratFromNumeric(l.Received)),
			Missing: wireQuantity(ratFromNumeric(l.Missing)), Returned: wireQuantity(ratFromNumeric(l.Returned)),
			InTransit: wireQuantity(inTransit),
		}
		if l.FromLocationID != nil {
			line.FromLocation = ptr(locationRefOf(r.locations[*l.FromLocationID]))
		}
		out.Lines = append(out.Lines, line)
	}
	out.InTransit = wireQuantity(total)
	byAction := map[int64][]gen.InventoryTransferActionLine{}
	for _, l := range r.actionLines {
		line := gen.InventoryTransferActionLine{
			LineId: l.TransferLineID, Kind: gen.InventoryTransferActionLineKind(l.Kind), Quantity: wireQuantity(ratFromNumeric(l.Quantity)),
		}
		if l.LocationID != nil {
			line.Location = ptr(locationRefOf(r.locations[*l.LocationID]))
		}
		byAction[l.ActionID] = append(byAction[l.ActionID], line)
	}
	for _, a := range r.actions {
		lines := byAction[a.ID]
		if lines == nil {
			lines = []gen.InventoryTransferActionLine{}
		}
		out.Actions = append(out.Actions, gen.InventoryTransferAction{
			Id: a.ID, Kind: gen.InventoryTransferActionKind(a.Kind), MovementId: a.MovementID,
			CloseReason: closeReasonOf(a.CloseReason), CloseNote: a.CloseNote,
			PostedBy: posterOf(a.PostedByUserID, a.PostedByDisplay), PostedAt: a.PostedAt, Lines: lines,
		})
	}
	return out
}

// transferOf is one transfer as the contract answers it.
func (s *server) transferOf(ctx context.Context, id int64) (gen.InventoryTransfer, error) {
	r, err := s.readTransfer(ctx, id)
	if err != nil {
		return gen.InventoryTransfer{}, err
	}
	return r.answer(), nil
}

// GetInventoryTransfer Get a transfer
// (GET /api/v1/inventory/transfers/{id})
func (s *server) GetInventoryTransfer(ctx context.Context, req gen.GetInventoryTransferRequestObject) (gen.GetInventoryTransferResponseObject, error) {
	out, err := s.transferOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.GetInventoryTransfer404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.GetInventoryTransfer503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.GetInventoryTransfer200JSONResponse(out), nil
}

// transferStatuses is the list's status filter as the statuses it matches:
// open is draft, sent and partly received together (§6); none is every one.
func transferStatuses(f *gen.InventoryTransferStatusFilter) ([]string, bool) {
	if f == nil {
		return nil, true
	}
	switch *f {
	case gen.InventoryTransferStatusFilterOpen:
		return []string{transferDraft, transferSent, transferPartlyReceived}, true
	case gen.InventoryTransferStatusFilterDraft, gen.InventoryTransferStatusFilterSent, gen.InventoryTransferStatusFilterPartlyReceived,
		gen.InventoryTransferStatusFilterReceived, gen.InventoryTransferStatusFilterCancelled:
		return []string{string(*f)}, true
	}
	return nil, false
}

// transferNumberOf is the transfer number q names — 1042, or the note's token
// TRF-1042 — and whether it names one.
func transferNumberOf(q string) (int64, bool) {
	t := strings.ToUpper(strings.TrimSpace(q))
	t = strings.TrimPrefix(t, transferTokenPrefix)
	n, err := strconv.ParseInt(t, 10, 64)
	return n, err == nil && n > 0
}

// transferTokenPrefix starts a transfer's token on its note (I36, I52).
const transferTokenPrefix = "TRF-"

// transferToken is the token a transfer's note encodes.
func transferToken(number int64) string { return transferTokenPrefix + strconv.FormatInt(number, 10) }

// overdue reports whether a transfer in transit is past its expected arrival
// — judged in the business zone's today — or, without one, was sent more than
// a week ago (I62).
func overdue(status string, expected pgtype.Date, sentAt *time.Time, today time.Time, now time.Time) bool {
	if !transferInTransit(status) {
		return false
	}
	if expected.Valid {
		return expected.Time.Before(today)
	}
	return sentAt != nil && sentAt.Before(now.Add(-transferOverdueAfter))
}

// businessToday is today's date in the business zone, as a UTC midnight to
// compare a date column with.
func businessToday(now time.Time, zone *time.Location) time.Time {
	local := now.In(zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// ListInventoryTransfers List transfers
// (GET /api/v1/inventory/transfers)
//
// Newest first, 50 a page by a cursor on (created_at, id), filtered by
// status (open: draft, sent and partly received), sites, a variant on a
// line, something recorded missing (I46's differences on the variant page),
// and q — a number, TRF-<number>, or a line's SKU.
func (s *server) ListInventoryTransfers(ctx context.Context, req gen.ListInventoryTransfersRequestObject) (gen.ListInventoryTransfersResponseObject, error) {
	p := req.Params
	var f fieldErrors
	statuses, ok := transferStatuses(p.Status)
	if !ok {
		f.add("status", "The status must be open, draft, sent, partly_received, received or cancelled")
	}
	params := store.ListTransfersParams{
		Statuses: statuses, FromSiteID: p.FromSiteId, ToSiteID: p.ToSiteId, VariantID: p.VariantId,
		WithMissing: p.WithMissing != nil && *p.WithMissing, RowLimit: defaultTransferPage + 1,
	}
	if p.Q != nil && strings.TrimSpace(*p.Q) != "" {
		q := strings.TrimSpace(*p.Q)
		if len([]rune(q)) > maxQueryTextLength {
			f.add("q", fmt.Sprintf("A search is at most %d characters", maxQueryTextLength))
		}
		params.Q = &q
		if n, ok := transferNumberOf(q); ok {
			params.QNumber = &n
		}
	}
	if p.Cursor != nil {
		at, id, ok := parseCursor(*p.Cursor)
		if !ok {
			f.add("cursor", "The cursor is not one this list gave")
		}
		params.CursorAt, params.CursorID = &at, &id
	}
	if f.failed() {
		return gen.ListInventoryTransfers400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	q := store.New(s.deps.Pool)
	rows, err := q.ListTransfers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("inventory: list transfers: %w", err)
	}
	out := gen.InventoryTransferPage{Transfers: make([]gen.InventoryTransferSummary, 0, min(len(rows), defaultTransferPage))}
	if len(rows) > defaultTransferPage {
		last := rows[defaultTransferPage-1]
		out.Next = ptr(formatCursor(last.CreatedAt, last.ID))
		rows = rows[:defaultTransferPage]
	}
	var siteIDs []int64
	anyInTransit := false
	for _, r := range rows {
		siteIDs = append(siteIDs, r.FromSiteID, r.ToSiteID)
		anyInTransit = anyInTransit || transferInTransit(r.Status)
	}
	sites, err := q.SitesByIDs(ctx, distinct(siteIDs))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites of transfers: %w", err)
	}
	byID := make(map[int64]store.InventorySite, len(sites))
	for _, site := range sites {
		byID[site.ID] = site
	}
	now := s.deps.Clock()
	var today time.Time
	if anyInTransit {
		zone, err := s.businessZone(ctx)
		if err != nil {
			return nil, err
		}
		today = businessToday(now, zone)
	}
	for _, r := range rows {
		out.Transfers = append(out.Transfers, gen.InventoryTransferSummary{
			Id: r.ID, Number: r.Number, FromSite: siteRefOf(byID[r.FromSiteID]), ToSite: siteRefOf(byID[r.ToSiteID]),
			Status: gen.InventoryTransferStatus(r.Status), Reference: r.Reference, ExpectedArrival: dateOf(r.ExpectedArrival),
			LineCount: r.LineCount, InTransit: wireQuantity(ratFromNumeric(r.InTransit)), Missing: wireQuantity(ratFromNumeric(r.Missing)),
			Overdue: overdue(r.Status, r.ExpectedArrival, r.SentAt, today, now), SentAt: r.SentAt,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return gen.ListInventoryTransfers200JSONResponse(out), nil
}
