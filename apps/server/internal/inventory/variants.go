package inventory

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// How a response names a variant (design §6): as the catalog answers it,
// read in one call per response and never inside a transaction; or, when the
// catalog no longer knows it, as its latest movement line snapshotted it,
// marked deleted. Stock cannot outlive its variant (I23), so the fallback
// names history.

// variantRefOf is a catalog entry as the contract names it, with its
// packagings, which the app breaks quantities down in (I26).
func variantRefOf(v contracts.VariantEntry) gen.InventoryVariantRef {
	ref := gen.InventoryVariantRef{
		Id: v.ID, ProductId: &v.ProductID, Sku: v.SKU, ProductName: v.ProductName, Unit: v.Unit, Barcode: v.Barcode,
		Packagings: packagingsOf(v),
	}
	// How its stock is kept (manufacturing phase 2 M49): the forms show the
	// lot column for a tracked variant only.
	if v.Tracking != "" {
		ref.Tracking = ptr(gen.InventoryVariantRefTracking(v.Tracking))
	}
	return ref
}

// deletedVariantRef is a variant the catalog no longer knows, named by a
// snapshot.
func deletedVariantRef(id int32, sku, productName, unit string) gen.InventoryVariantRef {
	deleted := true
	return gen.InventoryVariantRef{Id: id, Sku: sku, ProductName: productName, Unit: unit, Deleted: &deleted}
}

// variantRefs names every variant of ids: the catalog's in one call, the
// rest by their latest movement line's snapshot. It runs outside any
// transaction.
func (s *server) variantRefs(ctx context.Context, ids []int32) (map[int32]gen.InventoryVariantRef, error) {
	ids = distinct(ids)
	entries, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int32]gen.InventoryVariantRef, len(ids))
	for _, e := range entries {
		out[e.ID] = variantRefOf(e)
	}
	var missing []int32
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	snapshots, err := store.New(s.deps.Pool).LastLineSnapshots(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("inventory: read variant snapshots: %w", err)
	}
	for _, r := range snapshots {
		out[r.VariantID] = deletedVariantRef(r.VariantID, r.Sku, r.ProductName, r.Unit)
	}
	for _, id := range missing {
		if _, ok := out[id]; !ok {
			out[id] = deletedVariantRef(id, fmt.Sprintf("#%d", id), "", "")
		}
	}
	return out, nil
}

// distinct is ids without repeats, in first-seen order.
func distinct[T comparable](ids []T) []T {
	seen := make(map[T]bool, len(ids))
	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// SearchInventoryVariants Search goods variants
// (GET /api/v1/inventory/variants)
//
// The line editor's picker (I22): goods variants by SKU, product name or
// barcode, through the catalog's own search, so a warehouse worker needs no
// Products key to pick what they receive; with siteId, each with its
// primary location there (phase 2 I55), the line editor's default.
func (s *server) SearchInventoryVariants(ctx context.Context, req gen.SearchInventoryVariantsRequestObject) (gen.SearchInventoryVariantsResponseObject, error) {
	var f fieldErrors
	q := strings.TrimSpace(req.Params.Q)
	switch {
	case q == "":
		f.add("q", "Type something to search for")
	case utf8.RuneCountInString(q) > maxQueryTextLength:
		f.add("q", fmt.Sprintf("A search is at most %d characters", maxQueryTextLength))
	}
	limit := defaultVariantLimit
	if req.Params.Limit != nil {
		limit = int(*req.Params.Limit)
		if limit < 1 || limit > maxVariantLimit {
			f.add("limit", fmt.Sprintf("The limit is 1 to %d", maxVariantLimit))
		}
	}
	if f.failed() {
		return gen.SearchInventoryVariants400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	entries, err := s.catalogSearchVariants(ctx, q, limit)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.SearchInventoryVariants503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	primary := map[int32]int64{}
	if req.Params.SiteId != nil && len(entries) > 0 {
		ids := make([]int32, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.ID)
		}
		rows, err := store.New(s.deps.Pool).PrimaryLocationsAt(ctx, store.PrimaryLocationsAtParams{SiteID: *req.Params.SiteId, VariantIds: ids})
		if err != nil {
			return nil, fmt.Errorf("inventory: read the primary locations at site %d: %w", *req.Params.SiteId, err)
		}
		for _, r := range rows {
			primary[r.VariantID] = r.PrimaryLocationID
		}
	}
	out := gen.InventoryVariantList{Variants: make([]gen.InventoryVariantRef, 0, len(entries))}
	for _, e := range entries {
		ref := variantRefOf(e)
		if id, ok := primary[e.ID]; ok {
			ref.PrimaryLocationId = &id
		}
		out.Variants = append(out.Variants, ref)
	}
	return gen.SearchInventoryVariants200JSONResponse(out), nil
}
