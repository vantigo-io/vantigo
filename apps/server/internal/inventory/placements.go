package inventory

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Placements (phase 2 design I54, I55, I61): per variant and site, the
// primary location, the reorder point and the maximum — a setting, never
// stock. A PUT creates one with If-None-Match: * (one already there is
// revision_conflict) or changes one with If-Match; neither is the 400 a bad
// If-Match answers. A placement with nothing set is never stored: a PUT of
// three nulls is placement_empty, and DELETE removes it. The variant is read
// from the catalog before the transaction and its SKU and name snapshotted
// for the low-stock list's order and search (I58). Inside it, the site is
// taken FOR SHARE and must be active (site_archived), and the primary
// location FOR SHARE and must be an active location of that site
// (placement_location_invalid) — so an archive of either, which takes them
// FOR UPDATE and clears the primaries naming them (clearPrimaries), is
// ordered against the write.

const invalidPlacementTitle = "Invalid placement"

// The record's words for a placement (I64). A placement's events carry the
// variant as their entity and its site; the import's one event is keyed by
// the file's hash (PlacementImportSeen).
const (
	entityPlacement = "placement"

	eventPlacementSet       = "placement_set"
	eventPlacementCleared   = "placement_cleared"
	eventPlacementsImported = "placements_imported"
)

// placementOf is a placement as the contract answers it, low judged on what
// lies at its site (I56) — never at a site that is not active, which the
// rule leaves out.
func placementOf(p store.InventoryPlacement, site store.InventorySite, onHand, reserved, incoming *big.Rat) gen.InventoryPlacement {
	reorderPoint := optionalRat(p.ReorderPoint)
	v := judgeLow(lowLevels{onHand: onHand, reserved: reserved, incoming: incoming, reorderPoint: reorderPoint})
	return gen.InventoryPlacement{
		PrimaryLocationId: p.PrimaryLocationID, ReorderPoint: optionalWire(reorderPoint),
		Maximum: optionalWire(optionalRat(p.Maximum)), Multiple: optionalWire(optionalRat(p.Multiple)), Revision: p.Revision, Low: v.low && site.Status == string(gen.Active),
	}
}

// optionalRat is a nullable numeric as an exact decimal, nil for NULL.
func optionalRat(n pgtype.Numeric) *big.Rat {
	if !n.Valid {
		return nil
	}
	return ratFromNumeric(n)
}

// optionalWire is an optional decimal as the contract's nullable number.
func optionalWire(v *big.Rat) *float64 {
	if v == nil {
		return nil
	}
	return ptr(wireQuantity(v))
}

// optionalNumeric is an optional decimal as a nullable numeric column.
func optionalNumeric(v *big.Rat) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFromRat(v)
}

// cmpBool orders false before true.
func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// placementFields is a placement's fields, read and judged.
type placementFields struct {
	primaryLocationID               *int64
	reorderPoint, maximum, multiple *big.Rat
}

// readPlacementFields judges a PUT's body (I54): a reorder point of 0 or
// more, a maximum of more than 0 and never below the reorder point, a
// multiple of more than 0 (manufacturing phase 2 M105), at least one of the
// primary location, the reorder point and the maximum set.
func readPlacementFields(f *fieldErrors, in gen.InventoryPlacementInput) placementFields {
	var out placementFields
	out.primaryLocationID = in.PrimaryLocationId
	if in.ReorderPoint != nil {
		q, msg := parseQuantity(*in.ReorderPoint, nonNegativeQuantity)
		if msg == "The new on hand must be 0 or more" {
			msg = "The reorder point must be 0 or more"
		}
		f.addCoded("reorderPoint", codeQuantityInvalid, msg)
		out.reorderPoint = q
	}
	if in.Maximum != nil {
		q, msg := parseQuantity(*in.Maximum, positiveQuantity)
		if msg == "The quantity must be more than 0" {
			msg = "The maximum must be more than 0"
		}
		f.addCoded("maximum", codeQuantityInvalid, msg)
		out.maximum = q
	}
	if in.Multiple != nil {
		q, msg := parseQuantity(*in.Multiple, positiveQuantity)
		if msg == "The quantity must be more than 0" {
			msg = "The multiple must be more than 0"
		}
		f.addCoded("multiple", codeQuantityInvalid, msg)
		out.multiple = q
	}
	switch {
	case in.PrimaryLocationId == nil && in.ReorderPoint == nil && in.Maximum == nil:
		f.addCoded("primaryLocationId", codePlacementEmpty, "Set a primary location, a reorder point or a maximum, or remove the placement instead")
	case out.reorderPoint != nil && out.maximum != nil && out.maximum.Cmp(out.reorderPoint) < 0:
		f.addCoded("maximum", codeMaximumBelowReorderPoint,
			fmt.Sprintf("The maximum %s is below the reorder point %s", decimalText(out.maximum), decimalText(out.reorderPoint)))
	}
	return out
}

// placementChanges is what a write changed, by the contract's field names.
func placementChanges(before *store.InventoryPlacement, after placementFields) changes {
	c := changes{}
	var beforeLocation *int64
	var beforeReorder, beforeMaximum, beforeMultiple *big.Rat
	if before != nil {
		beforeLocation, beforeReorder, beforeMaximum = before.PrimaryLocationID, optionalRat(before.ReorderPoint), optionalRat(before.Maximum)
		beforeMultiple = optionalRat(before.Multiple)
	}
	if !equalPtr(beforeLocation, after.primaryLocationID) {
		c["primaryLocationId"] = fieldChange{From: beforeLocation, To: after.primaryLocationID}
	}
	quantity := func(name string, from, to *big.Rat) {
		if (from == nil) != (to == nil) || (from != nil && from.Cmp(to) != 0) {
			fc := fieldChange{}
			if from != nil {
				fc.From = decimalText(from)
			}
			if to != nil {
				fc.To = decimalText(to)
			}
			c[name] = fc
		}
	}
	quantity("reorderPoint", beforeReorder, after.reorderPoint)
	quantity("maximum", beforeMaximum, after.maximum)
	quantity("multiple", beforeMultiple, after.multiple)
	return c
}

func equalPtr[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// placementHeaders reads PUT's preconditions: If-None-Match * to create, or
// If-Match with a revision to change — one of them, never both.
func placementHeaders(p gen.PutInventoryPlacementParams) (create bool, revision int32, f fieldErrors) {
	switch {
	case p.IfNoneMatch != nil && p.IfMatch != nil:
		f.add("If-Match", "Send If-None-Match: * to create the placement, or If-Match to change it, not both")
	case p.IfNoneMatch != nil:
		if strings.TrimSpace(*p.IfNoneMatch) != "*" {
			f.add("If-None-Match", "If-None-Match must be *, to create the placement")
		}
		create = true
	case p.IfMatch != nil:
		var ok bool
		if revision, ok = parseIfMatch(*p.IfMatch); !ok {
			f = badIfMatch()
		}
	default:
		// Neither: the 400 a missing If-Match answers everywhere else.
		f = badIfMatch()
	}
	return create, revision, f
}

// sharePlacementSite takes the site FOR SHARE: 404 when there is none, and
// site_archived when it is not active.
func sharePlacementSite(ctx context.Context, txq *store.Queries, siteID int64) (store.InventorySite, error) {
	sites, err := txq.ShareTransferSites(ctx, []int64{siteID})
	if err != nil {
		return store.InventorySite{}, fmt.Errorf("inventory: share site %d: %w", siteID, err)
	}
	if len(sites) == 0 {
		return store.InventorySite{}, notFound()
	}
	if sites[0].Status != string(gen.Active) {
		return store.InventorySite{}, refuse(problem(http.StatusBadRequest, codeSiteArchived, "Site is archived",
			fmt.Sprintf("The site %s is archived; reactivate it before setting placements there.", sites[0].Code)))
	}
	return sites[0], nil
}

// PutInventoryPlacement Set a placement
// (PUT /api/v1/inventory/variants/{variantId}/placements/{siteId})
func (s *server) PutInventoryPlacement(ctx context.Context, req gen.PutInventoryPlacementRequestObject) (gen.PutInventoryPlacementResponseObject, error) {
	create, revision, hf := placementHeaders(req.Params)
	if hf.failed() {
		return gen.PutInventoryPlacement400ApplicationProblemPlusJSONResponse(invalid(invalidPlacementTitle, hf)), nil
	}
	if req.Body == nil {
		return gen.PutInventoryPlacement400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidPlacementTitle, "The request has no body.")), nil
	}
	f := s.unknownProperties(ctx, placementInputSchema)
	in := readPlacementFields(&f, *req.Body)
	if f.failed() {
		return gen.PutInventoryPlacement400ApplicationProblemPlusJSONResponse(invalid(invalidPlacementTitle, f)), nil
	}
	entries, err := s.catalogVariants(ctx, []int32{req.VariantId})
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PutInventoryPlacement503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	var vf fieldErrors
	switch {
	case len(entries) != 1:
		vf.addCoded("variantId", codeVariantNotFound, "No variant has this id")
	case entries[0].ProductType != productTypeGoods:
		vf.addCoded("variantId", codeVariantNotStockable, entries[0].SKU+" is a service; only goods are kept in stock")
	}
	if vf.failed() {
		return gen.PutInventoryPlacement400ApplicationProblemPlusJSONResponse(invalid(invalidPlacementTitle, vf)), nil
	}
	variant := entries[0]
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	reorderPoint, err := optionalNumeric(in.reorderPoint)
	if err != nil {
		return nil, err
	}
	maximum, err := optionalNumeric(in.maximum)
	if err != nil {
		return nil, err
	}
	multiple, err := optionalNumeric(in.multiple)
	if err != nil {
		return nil, err
	}
	var out store.InventoryPlacement
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := sharePlacementSite(ctx, txq, req.SiteId); err != nil {
			return err
		}
		if in.primaryLocationID != nil {
			loc, err := txq.ShareLocation(ctx, *in.primaryLocationID)
			if err != nil && !noRows(err) {
				return fmt.Errorf("inventory: share location %d: %w", *in.primaryLocationID, err)
			}
			var lf fieldErrors
			switch {
			case noRows(err) || loc.SiteID != req.SiteId:
				lf.addCoded("primaryLocationId", codePlacementLocationInvalid, "The primary location must be a location of this site")
			case loc.Status != string(gen.Active):
				lf.addCoded("primaryLocationId", codePlacementLocationInvalid, loc.FullCode+" is archived")
			}
			if lf.failed() {
				return refuseInvalid(invalidPlacementTitle, lf)
			}
		}
		var before *store.InventoryPlacement
		if create {
			out, err = txq.InsertPlacement(ctx, store.InsertPlacementParams{
				VariantID: req.VariantId, SiteID: req.SiteId, PrimaryLocationID: in.primaryLocationID,
				ReorderPoint: reorderPoint, Maximum: maximum, Multiple: multiple, Sku: variant.SKU, ProductName: variant.ProductName,
				UpdatedByUserID: who.UserID, UpdatedByDisplay: &who.Display, Now: s.deps.Clock(),
			})
			if noRows(err) {
				// One was there, or a concurrent create committed first: the
				// insert waited for it on the key and did nothing.
				return refuse(problem(http.StatusConflict, codeRevisionConflict, "Revision conflict",
					"The variant already has a placement at this site. Read it and change it with If-Match."))
			}
			if err != nil {
				return fmt.Errorf("inventory: insert the placement of variant %d at site %d: %w", req.VariantId, req.SiteId, err)
			}
		} else {
			cur, err := txq.LockPlacement(ctx, store.LockPlacementParams{VariantID: req.VariantId, SiteID: req.SiteId})
			if noRows(err) {
				return notFound()
			}
			if err != nil {
				return fmt.Errorf("inventory: lock the placement of variant %d at site %d: %w", req.VariantId, req.SiteId, err)
			}
			if cur.Revision != revision {
				return refuse(revisionConflict("placement", cur.Revision, revision))
			}
			before = &cur
			out, err = txq.UpdatePlacement(ctx, store.UpdatePlacementParams{
				VariantID: req.VariantId, SiteID: req.SiteId, PrimaryLocationID: in.primaryLocationID,
				ReorderPoint: reorderPoint, Maximum: maximum, Multiple: multiple, Sku: variant.SKU, ProductName: variant.ProductName,
				UpdatedByUserID: who.UserID, UpdatedByDisplay: &who.Display, Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("inventory: update the placement of variant %d at site %d: %w", req.VariantId, req.SiteId, err)
			}
		}
		c := placementChanges(before, in)
		if len(c) == 0 {
			return nil
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityPlacement, EntityID: int64(req.VariantId), SiteID: &req.SiteId, Kind: eventPlacementSet,
			Payload: map[string]any{"variantId": req.VariantId, "sku": variant.SKU, "fields": c},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutInventoryPlacement400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutInventoryPlacement404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryPlacement409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	answer, err := s.placementAnswer(ctx, out)
	if err != nil {
		return nil, err
	}
	if create {
		return gen.PutInventoryPlacement201JSONResponse(answer), nil
	}
	return gen.PutInventoryPlacement200JSONResponse(answer), nil
}

// placementAnswer is a placement just written, low judged on what lies at
// its site as read after the write.
func (s *server) placementAnswer(ctx context.Context, p store.InventoryPlacement) (gen.InventoryPlacement, error) {
	sn, err := s.balancesOf(ctx, []int32{p.VariantID}, nil)
	if err != nil {
		return gen.InventoryPlacement{}, err
	}
	k := siteKey{p.VariantID, p.SiteID}
	onHand := new(big.Rat)
	for _, b := range sn.balances {
		if sn.locations[b.locationID].SiteID == p.SiteID {
			onHand.Add(onHand, b.onHand)
		}
	}
	// Low is judged on usable on hand (manufacturing phase 2 M63).
	onHand.Sub(onHand, sn.held(k))
	return placementOf(p, sn.sites[p.SiteID], onHand, orZero(sn.reserved[k]), orZero(sn.incoming[k])), nil
}

// DeleteInventoryPlacement Remove a placement
// (DELETE /api/v1/inventory/variants/{variantId}/placements/{siteId})
func (s *server) DeleteInventoryPlacement(ctx context.Context, req gen.DeleteInventoryPlacementRequestObject) (gen.DeleteInventoryPlacementResponseObject, error) {
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.DeleteInventoryPlacement400ApplicationProblemPlusJSONResponse(invalid(invalidPlacementTitle, badIfMatch())), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		cur, err := txq.LockPlacement(ctx, store.LockPlacementParams{VariantID: req.VariantId, SiteID: req.SiteId})
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("inventory: lock the placement of variant %d at site %d: %w", req.VariantId, req.SiteId, err)
		}
		if cur.Revision != revision {
			return refuse(revisionConflict("placement", cur.Revision, revision))
		}
		if _, err := txq.DeletePlacement(ctx, store.DeletePlacementParams{VariantID: req.VariantId, SiteID: req.SiteId}); err != nil {
			return fmt.Errorf("inventory: delete the placement of variant %d at site %d: %w", req.VariantId, req.SiteId, err)
		}
		return s.recordEvent(ctx, txq, who, event{
			EntityKind: entityPlacement, EntityID: int64(req.VariantId), SiteID: &req.SiteId, Kind: eventPlacementCleared,
			Payload: map[string]any{"variantId": req.VariantId, "sku": cur.Sku, "removed": true,
				"fields": placementChanges(&cur, placementFields{})},
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.DeleteInventoryPlacement404Response{}, nil
		case http.StatusConflict:
			return gen.DeleteInventoryPlacement409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteInventoryPlacement204Response{}, nil
}

// clearPrimaries is what archiving or deleting locations does to the
// placements naming them or a location below them as primary (I55), inside
// the write's transaction and before a delete's rows go: a placement with
// nothing else set is removed, every other loses its primary location, and
// each is a placement_cleared event naming why. The foreign key's set null
// is only the backstop.
func (s *server) clearPrimaries(ctx context.Context, txq *store.Queries, who actor, locationIDs []int64, why string) error {
	removed, err := txq.DeletePrimaryOnlyPlacementsIn(ctx, locationIDs)
	if err != nil {
		return fmt.Errorf("inventory: remove the placements at locations %v: %w", locationIDs, err)
	}
	cleared, err := txq.ClearPrimaryLocationsIn(ctx, store.ClearPrimaryLocationsInParams{Now: s.deps.Clock(), LocationIds: locationIDs})
	if err != nil {
		return fmt.Errorf("inventory: clear the primary locations at locations %v: %w", locationIDs, err)
	}
	for _, r := range removed {
		if err := s.recordEvent(ctx, txq, who, event{
			EntityKind: entityPlacement, EntityID: int64(r.VariantID), SiteID: &r.SiteID, Kind: eventPlacementCleared,
			Payload: map[string]any{"variantId": r.VariantID, "removed": true, "reason": why,
				"fields": changes{"primaryLocationId": {From: r.PrimaryLocationID}}},
		}); err != nil {
			return err
		}
	}
	for _, r := range cleared {
		if err := s.recordEvent(ctx, txq, who, event{
			EntityKind: entityPlacement, EntityID: int64(r.VariantID), SiteID: &r.SiteID, Kind: eventPlacementCleared,
			Payload: map[string]any{"variantId": r.VariantID, "reason": why,
				"fields": changes{"primaryLocationId": {From: r.PrimaryLocationID}}},
		}); err != nil {
			return err
		}
	}
	return nil
}
