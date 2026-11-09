package inventory

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The lookup (design I28): one field a keyboard-wedge scanner types into. It
// matches, in order, a location's LOC token, a location's full code, a
// transfer's TRF-<number> token (phase 2 design I52), and a variant's SKU or
// barcode through the catalog. Its access is the session: each kind of match
// is shown by its own key — a location with inventory:locations-view, a
// variant and a transfer with locations-view and stock-view, as every stock
// read — so a caller holding neither gets no matches rather than a 403.

// lookupLocations is the lookup's location half: the location a LOC token
// names, then the one whose full code is the text, ignoring case.
func lookupLocations(ctx context.Context, q *store.Queries, text string) ([]store.InventoryLocation, error) {
	var out []store.InventoryLocation
	if id, ok := parseLocationToken(text); ok {
		l, err := q.GetLocation(ctx, id)
		switch {
		case err == nil:
			out = append(out, l)
		case !noRows(err):
			return nil, fmt.Errorf("inventory: read location %d: %w", id, err)
		}
	}
	l, err := q.LocationByFullCode(ctx, strings.ToUpper(text))
	switch {
	case err == nil:
		if len(out) == 0 || out[0].ID != l.ID {
			out = append(out, l)
		}
	case !noRows(err):
		return nil, fmt.Errorf("inventory: find a location by full code: %w", err)
	}
	return out, nil
}

// LookupInventory Find a location or an item
// (GET /api/v1/inventory/lookup)
func (s *server) LookupInventory(ctx context.Context, req gen.LookupInventoryRequestObject) (gen.LookupInventoryResponseObject, error) {
	text := strings.TrimSpace(req.Params.Q)
	var f fieldErrors
	switch {
	case text == "":
		f.add("q", "Scan or type a code")
	case utf8.RuneCountInString(text) > maxQueryTextLength:
		f.add("q", fmt.Sprintf("A code is at most %d characters", maxQueryTextLength))
	}
	if f.failed() {
		return gen.LookupInventory400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	out := gen.InventoryLookupResult{Matches: []gen.InventoryLookupMatch{}}
	canLocations := s.has(ctx, permLocationsView)
	if canLocations {
		locations, err := lookupLocations(ctx, store.New(s.deps.Pool), text)
		if err != nil {
			return nil, err
		}
		for _, l := range locations {
			out.Matches = append(out.Matches, gen.InventoryLookupMatch{Kind: gen.Location, Location: ptr(locationRefOf(l))})
		}
	}
	if canLocations && s.has(ctx, permStockView) {
		// A transfer by its note's token (phase 2 design I52).
		if number, ok := transferTokenNumber(text); ok {
			t, err := store.New(s.deps.Pool).GetTransferByNumber(ctx, number)
			switch {
			case err == nil:
				out.Matches = append(out.Matches, gen.InventoryLookupMatch{Kind: gen.Transfer, Transfer: &gen.InventoryTransferRef{Id: t.ID, Number: t.Number}})
			case !noRows(err):
				return nil, fmt.Errorf("inventory: find transfer %d: %w", number, err)
			}
		}
		matches, err := s.catalogLookupCode(ctx, text)
		if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
			return gen.LookupInventory503ApplicationProblemPlusJSONResponse(p), nil
		}
		if err != nil {
			return nil, err
		}
		seen := map[int32]bool{}
		for _, m := range matches {
			if seen[m.Variant.ID] {
				continue
			}
			seen[m.Variant.ID] = true
			out.Matches = append(out.Matches, gen.InventoryLookupMatch{Kind: gen.Variant, Variant: ptr(variantRefOf(m.Variant))})
		}
	}
	return gen.LookupInventory200JSONResponse(out), nil
}

// transferTokenNumber is the number a scanned TRF-<number> names; ok false
// when text is not one.
func transferTokenNumber(text string) (int64, bool) {
	t := strings.ToUpper(strings.TrimSpace(text))
	if !strings.HasPrefix(t, transferTokenPrefix) {
		return 0, false
	}
	return transferNumberOf(t)
}
