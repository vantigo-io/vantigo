package inventory

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// A variant's rules in Inventory and the inspection on receipt
// (manufacturing phase 2 M100): a variant whose receipts are inspected has
// every lot a receipt creates or adds to put under a quarantine hold of
// cause inspection, source inventory/receipt/<movement id>, in the receipt's
// transaction. Receiving into a lot that exists puts a new hold on it from
// this receipt, so the quantity added is never used before it is inspected —
// and the lot, one quantity, is in quarantine as a whole until it is released
// on the lot page (ReleaseInventoryLotHold, an Inventory hold).

// codeInspectionRequiresTracking is M100's refusal of the rule on a variant
// that is not tracked.
const codeInspectionRequiresTracking = "inspection_requires_tracking"

// variantRulesInputSchema is the rules' closed request schema.
const variantRulesInputSchema = "InventoryVariantRulesInput"

// The inspection hold's source and cause (M100).
const (
	receiptSourceKind = "receipt"
	inspectionCause   = "inspection"
	quarantineKind    = "quarantine"
)

// variantRulesOf is the rules as the contract answers them; a variant with no
// row has them off at revision 0.
func variantRulesOf(variantID int32, r *store.InventoryVariantRule) gen.InventoryVariantRules {
	out := gen.InventoryVariantRules{VariantId: variantID}
	if r == nil {
		return out
	}
	out.InspectOnReceipt, out.Revision, out.UpdatedAt = r.InspectOnReceipt, r.Revision, &r.UpdatedAt
	if r.UpdatedByDisplay != nil {
		out.UpdatedBy = ptr(gen.InventoryActor{DisplayName: *r.UpdatedByDisplay, UserId: r.UpdatedByUserID})
	}
	return out
}

// GetInventoryVariantRules Get a variant's rules
// (GET /api/v1/inventory/variants/{variantId}/rules)
func (s *server) GetInventoryVariantRules(ctx context.Context, req gen.GetInventoryVariantRulesRequestObject) (gen.GetInventoryVariantRulesResponseObject, error) {
	r, err := store.New(s.deps.Pool).GetVariantRules(ctx, req.VariantId)
	if noRows(err) {
		return gen.GetInventoryVariantRules200JSONResponse(variantRulesOf(req.VariantId, nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read the rules of variant %d: %w", req.VariantId, err)
	}
	return gen.GetInventoryVariantRules200JSONResponse(variantRulesOf(req.VariantId, &r)), nil
}

// PutInventoryVariantRules Set a variant's rules
// (PUT /api/v1/inventory/variants/{variantId}/rules)
//
// The variant is locked through the catalog first (rule 21), so its tracking
// is judged as it is and a tracking change waits for the write; then the
// rules' row FOR UPDATE under the revision read, 0 for none.
func (s *server) PutInventoryVariantRules(ctx context.Context, req gen.PutInventoryVariantRulesRequestObject) (gen.PutInventoryVariantRulesResponseObject, error) {
	if req.Body == nil {
		return gen.PutInventoryVariantRules400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", "Invalid rules", "The request has no body.")), nil
	}
	in := *req.Body
	if f := s.unknownProperties(ctx, variantRulesInputSchema); f.failed() {
		return gen.PutInventoryVariantRules400ApplicationProblemPlusJSONResponse(invalid("Invalid rules", f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.InventoryVariantRule
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		entries, err := s.lockVariants(ctx, tx, []int32{req.VariantId})
		if err != nil {
			return err
		}
		if len(entries) != 1 {
			return notFound()
		}
		if in.InspectOnReceipt && !tracked(entries[0]) {
			var f fieldErrors
			f.addCoded("inspectOnReceipt", codeInspectionRequiresTracking, fmt.Sprintf("%s is not tracked in lots or serials; only a tracked variant's receipts are inspected.", entries[0].SKU))
			return refuseInvalid("Invalid rules", f)
		}
		current, err := txq.GetVariantRulesForUpdate(ctx, req.VariantId)
		switch {
		case noRows(err):
			if in.Revision != 0 {
				return refuse(revisionConflict("variant's rules", 0, in.Revision))
			}
			saved, err = txq.InsertVariantRules(ctx, store.InsertVariantRulesParams{VariantID: req.VariantId, InspectOnReceipt: in.InspectOnReceipt,
				UpdatedByUserID: who.UserID, UpdatedByDisplay: &who.Display, Now: s.deps.Clock()})
			if noRows(err) {
				return refuse(revisionConflict("variant's rules", 1, in.Revision))
			}
			return err
		case err != nil:
			return fmt.Errorf("inventory: lock the rules of variant %d: %w", req.VariantId, err)
		case current.Revision != in.Revision:
			return refuse(revisionConflict("variant's rules", current.Revision, in.Revision))
		}
		saved, err = txq.UpdateVariantRules(ctx, store.UpdateVariantRulesParams{VariantID: req.VariantId, InspectOnReceipt: in.InspectOnReceipt,
			UpdatedByUserID: who.UserID, UpdatedByDisplay: &who.Display, Now: s.deps.Clock()})
		return err
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutInventoryVariantRules400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutInventoryVariantRules404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryVariantRules409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutInventoryVariantRules503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventoryVariantRules200JSONResponse(variantRulesOf(req.VariantId, &saved)), nil
}

// inspectedVariants is the variants of a receipt's lines that are inspected
// on receipt (M100), read under the variants' lock, before the lots are
// locked: their lots are to be locked FOR NO KEY UPDATE, as the hold after the
// posting takes it, so two receipts into one lot never both hold it FOR
// SHARE and then wait for each other to upgrade.
func inspectedVariants(ctx context.Context, txq *store.Queries, lines []engineLine, variants map[int32]contracts.VariantEntry) (map[int32]bool, error) {
	var ids []int32
	for _, l := range lines {
		if v, ok := variants[l.variantID]; ok && tracked(v) && l.to != nil {
			ids = append(ids, l.variantID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	rows, err := txq.InspectedVariants(ctx, slices.Compact(ids))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the variants inspected on receipt: %w", err)
	}
	out := make(map[int32]bool, len(rows))
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// holdForInspection puts the receipt's inspection hold on every lot a posted
// line of an inspected variant received into (M100), after the balances,
// through the same work as any hold (holdLots): the lots are locked FOR NO
// KEY UPDATE already.
func (s *server) holdForInspection(ctx context.Context, tx pgx.Tx, txq *store.Queries, movementID int64, posted []engineLine, inspected map[int32]bool, poster actor) error {
	var holds []contracts.LotHold
	var ids []int64
	seen := map[int64]bool{}
	for _, l := range posted {
		if !inspected[l.variantID] || l.lotID == 0 || l.quantity == nil || l.quantity.Cmp(new(big.Rat)) <= 0 || seen[l.lotID] {
			continue
		}
		seen[l.lotID] = true
		ids = append(ids, l.lotID)
		holds = append(holds, contracts.LotHold{LotID: l.lotID, Kind: quarantineKind, Cause: inspectionCause,
			By: contracts.LotActor{UserID: poster.UserID, Display: poster.Display}})
	}
	if len(holds) == 0 {
		return nil
	}
	src := movementSource{module: lotOriginModule, kind: receiptSourceKind, id: strconv.FormatInt(movementID, 10)}
	return s.holdLots(ctx, tx, txq, src, holds, ids)
}
