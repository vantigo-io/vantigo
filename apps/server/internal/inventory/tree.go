package inventory

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The tree (design I2–I4, I21) and its lock order (§4). Every structural
// write — a create, a bulk create, a change, a re-parent, an archive, a
// reactivation, a delete, and a site's new code — first takes the site row
// FOR UPDATE, so tree changes in one site are serial; then the locations it
// changes FOR UPDATE in id order; then it reads the subtree's balances or
// line history, and writes. A create shares its parent FOR SHARE instead.
//
// A posting takes no lock on the site row of its own, but its movement's key
// to the site does (FOR KEY SHARE, which FOR UPDATE waits for): a tree write
// begins once the postings in flight in its site have committed, and a
// posting arriving meanwhile waits at its key until the tree write commits.
// Neither holds what the other waits for — a tree write holds nothing a
// posting has taken before it reaches the site row, and a posting that has
// passed it holds no location yet — so the two meet only on location rows,
// which both take in id order. A posting's balances come after its
// locations, created and locked by one INSERT … ON CONFLICT DO UPDATE in
// (variant, location) order (posting.go, step 5); a tree write locks no
// balance and only reads them, so it never waits on one.
//
// A location's ancestors are the ids from the root down to its parent, so its
// subtree is the location and every row whose ancestors contain it, and its
// depth is the length of its ancestors. Both, and the full code, are
// rewritten for a whole subtree by one statement when a code or a parent
// changes.

// lockSite takes the site row FOR UPDATE: the first lock of every structural
// write (§4). An unknown site is the 404.
func lockSite(ctx context.Context, txq *store.Queries, id int64) (store.InventorySite, error) {
	site, err := txq.LockSite(ctx, id)
	if noRows(err) {
		return store.InventorySite{}, notFound()
	}
	if err != nil {
		return store.InventorySite{}, fmt.Errorf("inventory: lock site %d: %w", id, err)
	}
	return site, nil
}

// subtree is a location and everything below it, locked FOR UPDATE in id
// order, with the other locations the write locked beside it.
type subtree struct {
	node   store.InventoryLocation
	rows   []store.InventoryLocation
	ids    []int64
	others map[int64]store.InventoryLocation
}

// lockSubtree takes FOR UPDATE on the location, its subtree and the locations
// in also (a re-parent's new parent), in id order. An unknown location is the
// 404.
func lockSubtree(ctx context.Context, txq *store.Queries, id int64, also ...int64) (subtree, error) {
	rows, err := txq.LockSubtree(ctx, store.LockSubtreeParams{ID: id, Also: append([]int64{}, also...)})
	if err != nil {
		return subtree{}, fmt.Errorf("inventory: lock the subtree of location %d: %w", id, err)
	}
	out := subtree{others: map[int64]store.InventoryLocation{}}
	found := false
	for _, r := range rows {
		switch {
		case r.ID == id:
			out.node, found = r, true
			out.rows = append(out.rows, r)
			out.ids = append(out.ids, r.ID)
		case slices.Contains(r.Ancestors, id):
			out.rows = append(out.rows, r)
			out.ids = append(out.ids, r.ID)
		default:
			out.others[r.ID] = r
		}
	}
	if !found {
		return subtree{}, notFound()
	}
	return out, nil
}

// contains reports whether id is the node or below it.
func (s subtree) contains(id int64) bool { return slices.Contains(s.ids, id) }

// height is how many levels the subtree has below its node.
func (s subtree) height() int {
	deepest := s.node.Depth
	for _, r := range s.rows {
		deepest = max(deepest, r.Depth)
	}
	return int(deepest - s.node.Depth)
}

// fullCodesUnder is the full code every location of the subtree takes when
// the node's full code becomes newFull.
func (s subtree) fullCodesUnder(newFull string) []string {
	out := make([]string, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, rePrefixed(r.FullCode, s.node.FullCode, newFull))
	}
	return out
}

// childAncestors is the ancestors a child of parent has.
func childAncestors(parent store.InventoryLocation) []int64 {
	return append(slices.Clone(parent.Ancestors), parent.ID)
}

// depthExceeded is the refusal of a write that would put a location more
// than maxDepth levels below the root.
func depthExceeded(field string, depth int) error {
	var f fieldErrors
	f.addCoded(field, codeDepthExceeded,
		fmt.Sprintf("This would put a location %d levels below the site; a site has at most %d", depth, maxDepth))
	return refuseInvalid(invalidLocationTitle, f)
}

// parentInvalid is the refusal of a parent that cannot take the location.
func parentInvalid(field, message string) error {
	var f fieldErrors
	f.addCoded(field, codeParentInvalid, message)
	return refuseInvalid(invalidLocationTitle, f)
}

// codeInvalid is the refusal of a code that is not one.
func codeInvalid(title, field, message string) error {
	var f fieldErrors
	f.addCoded(field, codeCodeInvalid, message)
	return refuseInvalid(title, f)
}

// takenFullCodes is the 409 of full codes another location holds already:
// full_code_taken, every one listed.
func takenFullCodes(ctx context.Context, txq *store.Queries, codes []string, excluded []int64) error {
	taken, err := txq.TakenFullCodes(ctx, store.TakenFullCodesParams{FullCodes: codes, Excluded: append([]int64{}, excluded...)})
	if err != nil {
		return fmt.Errorf("inventory: check full codes: %w", err)
	}
	if len(taken) == 0 {
		return nil
	}
	return refuse(problem(http.StatusConflict, codeFullCodeTaken, "Full code taken",
		"These full codes are in use: "+strings.Join(taken, ", ")+"."))
}

// siblingCodeTaken is the 409 of a code another child of the parent has:
// location_code_taken.
func siblingCodeTaken(ctx context.Context, txq *store.Queries, parentID int64, code string, self int64, fullCode string) error {
	taken, err := txq.SiblingCodeTaken(ctx, store.SiblingCodeTakenParams{ParentID: &parentID, Code: code, ID: self})
	if err != nil {
		return fmt.Errorf("inventory: check sibling codes: %w", err)
	}
	if !taken {
		return nil
	}
	return refuse(problem(http.StatusConflict, codeLocationCodeTaken, "Location code taken",
		fmt.Sprintf("Another location under the same parent has the code %s: %s.", code, fullCode)))
}

// stockRefusal is the 409 of an archive while something in it holds stock
// (I6): location_has_stock. The quantity and the number of balances are said
// only to a caller who sees stock (inventory:stock-view, read before the
// transaction); to one who sees only the structure (§5), the detail says that
// something holds stock and nothing of how much.
func stockRefusal(ctx context.Context, txq *store.Queries, what string, ids []int64, seesStock bool) error {
	stock, err := txq.StockIn(ctx, ids)
	if err != nil {
		return fmt.Errorf("inventory: read the stock of %s: %w", what, err)
	}
	if stock.Lines == 0 {
		return nil
	}
	detail := what + " holds stock, or something below it does. Move or adjust the stock to zero before archiving."
	if seesStock {
		detail = fmt.Sprintf("%s holds %s on hand in %d stock line(s). Move or adjust the stock to zero before archiving.",
			what, decimalText(ratFromText(stock.Quantity)), stock.Lines)
	}
	return refuse(problem(http.StatusConflict, codeLocationHasStock, "Location has stock", detail))
}

// historyRefusal is the 409 of a delete of locations a movement line has
// named (I6): location_has_history.
func historyRefusal(ctx context.Context, txq *store.Queries, what string, ids []int64) error {
	used, err := txq.HistoryIn(ctx, ids)
	if err != nil {
		return fmt.Errorf("inventory: read the history of %s: %w", what, err)
	}
	if !used {
		return nil
	}
	return refuse(problem(http.StatusConflict, codeLocationHasHistory, "Location has history",
		what+" has movements and cannot be deleted; archive it instead."))
}

// rootRefusal is the 409 of a write a site's root takes only with its site.
func rootRefusal(detail string) error {
	return refuse(problem(http.StatusConflict, codeRootLocation, "Site root", detail))
}

// openCountRefusal is the 409 of an archive while a count is still counting
// in what it archives (location_has_open_count): the count's lines would name
// locations that take no stock.
func openCountRefusal(what string) error {
	return refuse(problem(http.StatusConflict, codeLocationHasOpenCount, "Location has an open count",
		what+" is being counted, or something below or above it is. Post or cancel the count before archiving."))
}
