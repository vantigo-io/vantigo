package orders

// Kits (manufacturing phase 2 M145, M146): a kit line's components are order
// lines of their own, its child lines, so each reserves, is delivered, picks
// lots and is cancelled like any goods line — a reservation names one variant.
//
// The rules:
//
//   - The composition is copied onto the order when the kit line is added —
//     a new product line, or a line whose item changed — from Manufacturing's
//     contracts.KitStructure, read on the pool before the save's transaction.
//     Without Manufacturing (a nil slot) a kit variant is an ordinary goods
//     line, and a later BoM edit never reaches a line already on the order.
//   - A save sends the kit line alone; its children follow it, kept under
//     their ids and rescaled to its quantity (the kit's × per kit, half-up to
//     three decimals) in the same transaction, so the reservation follows each
//     child as an edit of a confirmed line does (stock.go). A line sent with a
//     child's id is left out of the save.
//   - The kit line carries the price and is not reservable; its children are
//     priced 0, at the kit's VAT code, and not invoiced on their own: what
//     the kit's invoice covers is copied onto them (syncKits), and they never
//     count toward the order's invoice status, its done or what is left to
//     invoice — the kit line counts for them. A cancel of the remainder that
//     leaves a child delivered beyond the whole kits kept logs the excess
//     (beyondKits): it was delivered and is never invoiced.
//   - A kit line delivered by delivery documents is never picked itself: it
//     counts as delivered the whole kits its children's deliveries make
//     (syncKits).
//   - Its availability is the least over its children of available ÷ per kit
//     (M146).

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// kitsOf is the ids of the lines some line names as its kit: the kit lines.
func kitsOf(lines []store.OrdersLine) map[int64]bool {
	out := map[int64]bool{}
	for _, l := range lines {
		if l.ParentLineID != nil {
			out[*l.ParentLineID] = true
		}
	}
	return out
}

// childrenOf is each kit line's child lines, in position order.
func childrenOf(lines []store.OrdersLine) map[int64][]store.OrdersLine {
	out := map[int64][]store.OrdersLine{}
	for _, l := range lines {
		if l.ParentLineID != nil {
			out[*l.ParentLineID] = append(out[*l.ParentLineID], l)
		}
	}
	return out
}

// withoutKitChildren is inputs less the lines that name one of existing's
// child lines by id — a kit's children are written from their kit — and the
// index in inputs of each line kept.
func withoutKitChildren(inputs []lineInput, existing []store.OrdersLine) ([]lineInput, []int) {
	child := map[int64]bool{}
	for _, l := range existing {
		if l.ParentLineID != nil {
			child[l.ID] = true
		}
	}
	out, kept := make([]lineInput, 0, len(inputs)), make([]int, 0, len(inputs))
	for i, l := range inputs {
		if l.id != nil && child[*l.id] {
			continue
		}
		out, kept = append(out, l), append(kept, i)
	}
	return out, kept
}

// atInputs is err with each refused line's index, an index in lines, turned
// into the index of the request's line it came from (pricedLine.input): a
// kit's child line is refused on its kit's row, as the editor holds the kit
// alone (M145). Any other error, or an index beyond lines, is left.
func atInputs(err error, lines []pricedLine) error {
	at := make([]int, len(lines))
	for i, l := range lines {
		at[i] = l.input
	}
	return refusedAt(err, at)
}

// atTopLevel is err with each refused line's index, an index in an order's
// stored lines, turned into the index among the lines that are no kit's
// child, a child's its kit's (M145).
func atTopLevel(err error, lines []store.OrdersLine) error {
	top := map[int64]int{}
	for _, l := range lines {
		if l.ParentLineID == nil {
			top[l.ID] = len(top)
		}
	}
	at := make([]int, len(lines))
	for i, l := range lines {
		if l.ParentLineID != nil {
			at[i] = top[*l.ParentLineID]
		} else {
			at[i] = top[l.ID]
		}
	}
	return refusedAt(err, at)
}

// refusedAt rewrites a lines refusal's indexes through at, in place.
func refusedAt(err error, at []int) error {
	var r *refusal
	if !errors.As(err, &r) || r.problem.Lines == nil {
		return err
	}
	for k := range *r.problem.Lines {
		l := &(*r.problem.Lines)[k]
		if l.Index >= 0 && int(l.Index) < len(at) {
			l.Index = int32(at[l.Index])
		}
	}
	return err
}

// addsKit reports whether a priced line is added to the order as far as its
// kit is concerned: a product line with an item and a quantity that is new,
// or whose item changed. Only such a line reads KitStructure.
func addsKit(p pricedLine, old map[int64]store.OrdersLine) bool {
	if p.kind != kindProduct || p.variantID == nil || p.quantity == nil {
		return false
	}
	if p.id == nil {
		return true
	}
	o, ok := old[*p.id]
	return !ok || !equalInt32(o.VariantID, p.variantID)
}

// kitComponents is KitStructure's answer for the variants, before any
// transaction; nil when Manufacturing is off.
func (s *server) kitComponents(ctx context.Context, variantIDs []int32) (map[int32][]contracts.KitComponent, error) {
	if len(variantIDs) == 0 {
		return nil, nil
	}
	if err := noteContractCall(ctx, "KitStructure.Kits"); err != nil {
		return nil, err
	}
	if s.deps.KitStructure == nil {
		return nil, nil
	}
	kits, err := s.deps.KitStructure.Kits(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("orders: read the kits: %w", err)
	}
	return kits, nil
}

// withKits is an order's priced lines with each kit line's children after it
// (M145), before the save's transaction: a kept kit line's stored children,
// rescaled to its quantity; an added line's from KitStructure when its
// variant is a kit. existing is the order's lines as read before the
// transaction, nil on a create; the save's version check refuses a change
// made since.
func (s *server) withKits(ctx context.Context, priced []pricedLine, existing []store.OrdersLine, title string) ([]pricedLine, error) {
	old := byID(existing)
	stored := childrenOf(existing)
	var ask []int32
	for _, p := range priced {
		if addsKit(p, old) && !slices.Contains(ask, *p.variantID) {
			ask = append(ask, *p.variantID)
		}
	}
	slices.Sort(ask)
	kits, err := s.kitComponents(ctx, ask)
	if err != nil {
		return nil, err
	}
	var componentIDs []int32
	for _, comps := range kits {
		for _, c := range comps {
			if !slices.Contains(componentIDs, c.VariantID) {
				componentIDs = append(componentIDs, c.VariantID)
			}
		}
	}
	slices.Sort(componentIDs)
	entries, err := s.catalogVariants(ctx, componentIDs)
	if err != nil {
		return nil, err
	}
	if len(stored) == 0 && len(kits) == 0 {
		return priced, nil
	}

	out := make([]pricedLine, 0, len(priced))
	var failures []lineFailure
	for i, p := range priced {
		at := len(out)
		out = append(out, p)
		var children []pricedLine
		switch {
		case p.kind != kindProduct || p.variantID == nil || p.quantity == nil:
		case !addsKit(p, old):
			for _, c := range stored[*p.id] {
				child := pricedLineOf(c)
				child.id, child.frozen, child.perKit = &c.ID, true, ratOrZero(c.PerKit)
				children = append(children, child)
			}
		default:
			comps, isKit := kits[*p.variantID]
			if isKit && len(comps) == 0 {
				// A kit answered with no component (contracts.KitStructure)
				// is refused, never sold as an ordinary line.
				failures = append(failures, lineFailure{index: i, code: codeLineInvalid,
					detail: "The kit has no components: its bill of materials makes nothing per kit."})
				break
			}
			for _, c := range comps {
				e, ok := entries[c.VariantID]
				perKit, valid := new(big.Rat).SetString(c.PerKit)
				if !ok || !valid {
					failures = append(failures, lineFailure{index: i, code: codeVariantNotFound,
						detail: fmt.Sprintf("Component %d of the kit is not in the catalog.", c.VariantID)})
					children = nil
					break
				}
				children = append(children, pricedLine{
					kind: kindProduct, variantID: &e.ID, productID: &e.ProductID, sku: textPtr(e.SKU), name: textPtr(e.ProductName),
					unit: e.Unit, priceSource: ptr(priceManual), perKit: perKit,
				})
			}
		}
		for _, child := range children {
			// Its quantity is the kit's × per kit (M145), priced 0 at the
			// kit's VAT code: the kit line carries the price.
			child.quantity = new(big.Rat).Mul(p.quantity, child.perKit)
			child.quantity.SetString(child.quantity.FloatString(quantityPlaces))
			if child.quantity.Sign() <= 0 {
				failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid,
					detail: "The kit's quantity is too small: one of its components would be 0."})
				break
			}
			child.unitPrice, child.discount = new(big.Rat), new(big.Rat)
			child.packagingID, child.packagingQuantity, child.packagingName = nil, nil, nil
			child.vatCodeID, child.vatCode, child.vatRate, child.vatCategory = p.vatCodeID, p.vatCode, p.vatRate, p.vatCategory
			child.kit, child.input = &at, p.input
			child.computeAmounts()
			out = append(out, child)
		}
	}
	if len(failures) > 0 {
		return nil, refuseLines(title, failures)
	}
	return out, nil
}

// syncKits brings an order's kit lines and their children in step, in
// lines, and answers the lines it changed (M145): a kit line delivered by
// delivery documents counts as delivered the whole kits its children make —
// the least over them of delivered ÷ per kit, rounded down to three decimals
// and never past what is ordered and not cancelled — and each child counts
// as invoiced what its kit's invoice covers, the kit's invoiced × per kit,
// never past its own quantity. A kit line delivered by hand is marked by hand,
// as its children are.
func syncKits(lines []store.OrdersLine) []int {
	children := childrenOf(lines)
	if len(children) == 0 {
		return nil
	}
	var changed []int
	index := map[int64]int{}
	for i, l := range lines {
		index[l.ID] = i
	}
	for i := range lines {
		kit := &lines[i]
		kids := children[kit.ID]
		if len(kids) == 0 {
			continue
		}
		if kit.DeliveryMethod == methodDelivery {
			var made *big.Rat
			for _, c := range kids {
				per := ratOf(c.PerKit)
				if per == nil || per.Sign() <= 0 {
					continue
				}
				k := floorPlaces(new(big.Rat).Quo(ratOrZero(c.QtyDelivered), per), quantityPlaces)
				if made == nil || k.Cmp(made) < 0 {
					made = k
				}
			}
			if made != nil {
				if most := new(big.Rat).Sub(ratOrZero(kit.Quantity), ratOrZero(kit.QtyCancelled)); made.Cmp(most) > 0 {
					made = most
				}
				if made.Sign() < 0 {
					made = new(big.Rat)
				}
				if made.Cmp(ratOrZero(kit.QtyDelivered)) != 0 {
					kit.QtyDelivered = numeric(made, quantityPlaces)
					changed = append(changed, i)
				}
			}
		}
		for _, c := range kids {
			j := index[c.ID]
			child := &lines[j]
			per := ratOrZero(child.PerKit)
			invoiced := new(big.Rat).Mul(ratOrZero(kit.QtyInvoiced), per)
			invoiced.SetString(invoiced.FloatString(quantityPlaces))
			if most := ratOrZero(child.Quantity); invoiced.Cmp(most) > 0 {
				invoiced = most
			}
			if invoiced.Cmp(ratOrZero(child.QtyInvoiced)) != 0 {
				child.QtyInvoiced = numeric(invoiced, quantityPlaces)
				changed = append(changed, j)
			}
		}
	}
	slices.Sort(changed)
	return slices.Compact(changed)
}

// beyondKits is a delivered_beyond_kits action for each kit child line of
// lines delivered beyond the whole kits its kit keeps — the kit's ordered less
// cancelled × per kit — to the excess: what a cancel of the remainder leaves
// delivered and never invoiced, the kit line carrying the price (M145).
func beyondKits(lines []store.OrdersLine) []changeAction {
	kits := byID(lines)
	var out []changeAction
	for _, c := range lines {
		if c.ParentLineID == nil {
			continue
		}
		kit := kits[*c.ParentLineID]
		kept := new(big.Rat).Sub(ratOrZero(kit.Quantity), ratOrZero(kit.QtyCancelled))
		excess := new(big.Rat).Sub(ratOrZero(c.QtyDelivered), new(big.Rat).Mul(kept, ratOrZero(c.PerKit)))
		if excess.Sign() > 0 {
			out = append(out, changeAction{Kind: actionDeliveredBeyondKits, LineID: &c.ID, To: ptr(excess.FloatString(quantityPlaces))})
		}
	}
	return out
}

// floorPlaces is v rounded down to places decimals.
func floorPlaces(v *big.Rat, places int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	q := new(big.Int).Div(new(big.Int).Mul(v.Num(), scale), v.Denom())
	return new(big.Rat).SetFrac(q, scale)
}

// kitAvailable is how many kits a kit line's children make of what is
// available (M146): the least over them of available ÷ per kit, rounded down
// to three decimals; nil when a child's variant has no figure.
func kitAvailable(children []store.OrdersLine, available map[int32]*big.Rat) *big.Rat {
	var least *big.Rat
	for _, c := range children {
		per := ratOf(c.PerKit)
		if c.VariantID == nil || per == nil || per.Sign() <= 0 {
			continue
		}
		q, ok := available[*c.VariantID]
		if !ok {
			return nil
		}
		k := floorPlaces(new(big.Rat).Quo(q, per), quantityPlaces)
		if least == nil || k.Cmp(least) < 0 {
			least = k
		}
	}
	return least
}
