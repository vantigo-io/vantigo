package inventory

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The trace (manufacturing phase 2 M87–M89): where a lot went and where it
// came from, built from Inventory's own records and read on the pool. The
// nodes are lots; the edges are production movements — in one movement of
// kind production, every lot on a consume line (a line with only a from) is a
// parent of every lot on a produce line (only a to). No genealogy table is
// kept: the movement lines with a lot are the record. An unbuild (kind
// unbuild, M123) is a reverse edge: it is shown on both lots as an end and
// never expanded, by the walk nor by the block's cascade (M92).
//
// The walk reads level by level — one read of the lots' lines
// (ix_movement_lines_lot) and one of the productions they cross
// (ux_movement_lines_movement_line) per level — up to maxDepth productions
// deep and at most maxTraceNodes nodes (trace_too_large). The tree is then
// laid out depth first: a lot reached twice is expanded where it is first
// laid out, and every later reference carries seenAt, the first one's key.

// The trace's bounds (M89: D1 M15a's bound).
const (
	maxTraceNodes     = 2000
	maxTraceDepth     = 10
	codeTraceTooLarge = "trace_too_large"
)

// The node kinds: a lot, an untracked component a production consumed
// (origin, components=all), and the movement kinds that end a path.
const (
	traceLot       = "lot"
	traceComponent = "component"
)

// errTraceTooLarge is the 400 of a trace beyond maxTraceNodes nodes.
func errTraceTooLarge() error {
	return refuse(problem(http.StatusBadRequest, codeTraceTooLarge, "The trace is too large",
		fmt.Sprintf("A trace has at most %d nodes; this one would have more. Trace a lot further down the tree, or fewer levels.", maxTraceNodes)))
}

// traceVariant is a node's variant as the lines snapshotted it.
type traceVariant struct {
	id              int32
	sku, name, unit string
}

// traceEdge is the production that links a node to its parent: the parent
// lot's consumed quantity and the made lot's produced quantity in it.
type traceEdge struct {
	movementID         int64
	postedAt           time.Time
	consumed, produced *big.Rat
	source             *movementSource
}

// traceMovement is the movement an end node stands for.
type traceMovement struct {
	id                            int64
	kind                          string
	postedAt                      time.Time
	reason, supplier, supplierRef *string
	source                        *movementSource
}

// traceNode is one node of the tree, in the order it is laid out.
type traceNode struct {
	key, parent string
	depth       int
	kind        string
	lotID       int64 // a lot node's lot; an end's the lot it ends
	variant     traceVariant
	edge        *traceEdge
	movement    *traceMovement
	quantity    *big.Rat // an end's or a component's
	seenAt      string
	depthLimit  bool
}

// traceGraph is a walk's outcome: the nodes in tree order and the lots they
// name, with what was put in under each and what each holds now.
type traceGraph struct {
	nodes      []traceNode
	lots       map[int64]store.InventoryLot
	quantityIn map[int64]*big.Rat
	onHand     map[int64]*big.Rat
	units      map[int64]string
}

// traceOptions is what a walk is asked.
type traceOptions struct {
	root       int64
	usage      bool
	components bool
	maxDepth   int
}

// lotMovement is one lot's lines of one movement, summed per side.
type lotMovement struct {
	first   store.TraceLotLinesRow
	in, out *big.Rat
}

// walkTrace walks the graph from opts.root on q — the pool's snapshot for a
// trace, the block's own transaction for its cascade — and lays the tree
// out. An unknown root is the 404 refusal.
func walkTrace(ctx context.Context, q *store.Queries, opts traceOptions) (traceGraph, error) {
	roots, err := q.LotsByIDs(ctx, []int64{opts.root})
	if err != nil {
		return traceGraph{}, fmt.Errorf("inventory: read lot %d: %w", opts.root, err)
	}
	if len(roots) == 0 {
		return traceGraph{}, notFound()
	}
	lotLines := map[int64][]store.TraceLotLinesRow{}
	movLines := map[int64][]store.TraceMovementLinesRow{}
	depthOf := map[int64]int{opts.root: 0}
	frontier := []int64{opts.root}
	for level := 0; len(frontier) > 0; level++ {
		rows, err := q.TraceLotLines(ctx, frontier)
		if err != nil {
			return traceGraph{}, fmt.Errorf("inventory: read the lines of the traced lots: %w", err)
		}
		for _, r := range rows {
			lotLines[r.LotID] = append(lotLines[r.LotID], r)
		}
		if level >= opts.maxDepth {
			break // the last level's lots are shown, not expanded
		}
		// The productions this level crosses: those that consumed a lot of
		// it (usage) or produced one (origin).
		var need []int64
		for _, r := range rows {
			if r.Kind == movementKindProduction && r.Outgoing == opts.usage {
				if _, ok := movLines[r.MovementID]; !ok {
					movLines[r.MovementID] = nil
					need = append(need, r.MovementID)
				}
			}
		}
		if len(need) > 0 {
			lines, err := q.TraceMovementLines(ctx, need)
			if err != nil {
				return traceGraph{}, fmt.Errorf("inventory: read the productions of the traced lots: %w", err)
			}
			for _, l := range lines {
				movLines[l.MovementID] = append(movLines[l.MovementID], l)
			}
		}
		var next []int64
		for _, r := range rows {
			if r.Kind != movementKindProduction || r.Outgoing != opts.usage {
				continue
			}
			for _, l := range movLines[r.MovementID] {
				// usage follows the produce lines, origin the consume lines.
				if l.LotID == nil || l.Outgoing == opts.usage {
					continue
				}
				if _, seen := depthOf[*l.LotID]; !seen {
					depthOf[*l.LotID] = level + 1
					next = append(next, *l.LotID)
				}
			}
		}
		if len(depthOf) > maxTraceNodes {
			return traceGraph{}, errTraceTooLarge()
		}
		frontier = next
	}
	ids := make([]int64, 0, len(depthOf))
	for id := range depthOf {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	lots, err := q.LotsByIDs(ctx, ids)
	if err != nil {
		return traceGraph{}, fmt.Errorf("inventory: read the traced lots: %w", err)
	}
	held, err := q.TraceLotsOnHand(ctx, ids)
	if err != nil {
		return traceGraph{}, fmt.Errorf("inventory: read what the traced lots hold: %w", err)
	}
	g := traceGraph{lots: map[int64]store.InventoryLot{}, quantityIn: map[int64]*big.Rat{}, onHand: map[int64]*big.Rat{}, units: map[int64]string{}}
	for _, l := range lots {
		g.lots[l.ID] = l
		g.quantityIn[l.ID] = new(big.Rat)
		g.onHand[l.ID] = new(big.Rat)
	}
	for _, h := range held {
		g.onHand[h.LotID] = ratFromText(h.OnHand)
	}
	for id, rows := range lotLines {
		for _, r := range rows {
			g.units[id] = r.Unit
			// Put in under the lot: every line with only a to, but a
			// transfer's arrival, which is the same lot moving between sites.
			if !r.Outgoing && r.Kind != string(gen.TransferIn) {
				if in := g.quantityIn[id]; in != nil {
					in.Add(in, ratFromNumeric(r.Quantity))
				}
			}
		}
	}
	for _, lines := range movLines {
		for _, l := range lines {
			if l.LotID != nil && g.units[*l.LotID] == "" {
				g.units[*l.LotID] = l.Unit
			}
		}
	}
	b := traceBuilder{opts: opts, g: &g, lotLines: lotLines, movLines: movLines, expanded: map[int64]string{}, children: map[string]int{}}
	if err := b.lot(opts.root, "", 0, nil); err != nil {
		return traceGraph{}, err
	}
	return g, nil
}

// traceBuilder lays the walked graph out as a tree, depth first.
type traceBuilder struct {
	opts     traceOptions
	g        *traceGraph
	lotLines map[int64][]store.TraceLotLinesRow
	movLines map[int64][]store.TraceMovementLinesRow
	expanded map[int64]string // a lot's key where it is expanded
	children map[string]int   // how many children each key has so far
}

// add lays one node out under parent, keyed by its place there.
func (b *traceBuilder) add(n traceNode) (int, error) {
	if len(b.g.nodes) >= maxTraceNodes {
		return 0, errTraceTooLarge()
	}
	if n.parent == "" && len(b.g.nodes) == 0 {
		n.key = "0"
	} else {
		n.key = n.parent + "." + strconv.Itoa(b.children[n.parent])
		b.children[n.parent]++
	}
	b.g.nodes = append(b.g.nodes, n)
	return len(b.g.nodes) - 1, nil
}

// lotVariant is a lot's variant as the lot and its lines snapshotted it.
func (b *traceBuilder) lotVariant(id int64) traceVariant {
	l := b.g.lots[id]
	return traceVariant{id: l.VariantID, sku: l.Sku, name: l.ProductName, unit: b.g.units[id]}
}

// lot lays a lot out, and — once, unless it is at the depth limit — its
// movements: the ends of its paths and the lots it leads to.
func (b *traceBuilder) lot(id int64, parent string, depth int, edge *traceEdge) error {
	i, err := b.add(traceNode{parent: parent, depth: depth, kind: traceLot, lotID: id, variant: b.lotVariant(id), edge: edge})
	if err != nil {
		return err
	}
	key := b.g.nodes[i].key
	if first, ok := b.expanded[id]; ok {
		b.g.nodes[i].seenAt = first
		return nil
	}
	if depth >= b.opts.maxDepth {
		b.g.nodes[i].depthLimit = true
		return nil
	}
	b.expanded[id] = key
	for _, m := range groupLotMovements(b.lotLines[id]) {
		if err := b.movement(id, key, depth, m); err != nil {
			return err
		}
	}
	return nil
}

// groupLotMovements is a lot's lines summed per movement, in the order the
// movements were posted.
func groupLotMovements(rows []store.TraceLotLinesRow) []*lotMovement {
	var out []*lotMovement
	byID := map[int64]*lotMovement{}
	for _, r := range rows {
		m, ok := byID[r.MovementID]
		if !ok {
			m = &lotMovement{first: r, in: new(big.Rat), out: new(big.Rat)}
			byID[r.MovementID] = m
			out = append(out, m)
		}
		if r.Outgoing {
			m.out.Add(m.out, ratFromNumeric(r.Quantity))
		} else {
			m.in.Add(m.in, ratFromNumeric(r.Quantity))
		}
	}
	return out
}

// sourceOfRow is a lot line's movement source, nil when it has none.
func sourceOfRow(r store.TraceLotLinesRow) *movementSource {
	if r.SourceModule == nil || r.SourceKind == nil || r.SourceID == nil {
		return nil
	}
	return &movementSource{module: *r.SourceModule, kind: *r.SourceKind, id: *r.SourceID, lineID: r.SourceLineID}
}

// madeLot is a lot (or an untracked variant) on one side of a production,
// summed over its lines.
type madeLot struct {
	lotID    int64
	variant  traceVariant
	quantity *big.Rat
}

// productionSide is one side of a production — its produce lines (to-only)
// or its consume lines (from-only) — summed per lot, in line order, and per
// variant for the lines without a lot.
func productionSide(lines []store.TraceMovementLinesRow, outgoing bool) (lots, untracked []madeLot) {
	lotAt, variantAt := map[int64]int{}, map[int32]int{}
	for _, l := range lines {
		if l.Outgoing != outgoing {
			continue
		}
		q := ratFromNumeric(l.Quantity)
		if l.LotID != nil {
			if i, ok := lotAt[*l.LotID]; ok {
				lots[i].quantity.Add(lots[i].quantity, q)
				continue
			}
			lotAt[*l.LotID] = len(lots)
			lots = append(lots, madeLot{lotID: *l.LotID, quantity: q})
			continue
		}
		if i, ok := variantAt[l.VariantID]; ok {
			untracked[i].quantity.Add(untracked[i].quantity, q)
			continue
		}
		variantAt[l.VariantID] = len(untracked)
		untracked = append(untracked, madeLot{variant: traceVariant{id: l.VariantID, sku: l.Sku, name: l.ProductName, unit: l.Unit}, quantity: q})
	}
	return lots, untracked
}

// end lays out the movement that ends one of a lot's paths.
func (b *traceBuilder) end(id int64, parent string, depth int, m *lotMovement, quantity *big.Rat) error {
	r := m.first
	_, err := b.add(traceNode{parent: parent, depth: depth + 1, kind: r.Kind, lotID: id,
		variant:  traceVariant{id: r.VariantID, sku: r.Sku, name: r.ProductName, unit: r.Unit},
		movement: &traceMovement{id: r.MovementID, kind: r.Kind, postedAt: r.PostedAt, reason: r.Reason, supplier: r.SupplierName, supplierRef: r.SupplierReference, source: sourceOfRow(r)},
		quantity: quantity})
	return err
}

// movement lays out what one of a lot's movements leads to in the walk's
// direction: through a production, the lots made from it (usage) or that it
// was made from (origin); otherwise an end, or nothing when the movement is
// not on this side of the lot's path.
func (b *traceBuilder) movement(id int64, key string, depth int, m *lotMovement) error {
	r := m.first
	if b.opts.usage {
		if m.out.Sign() == 0 {
			return nil
		}
		switch r.Kind {
		case movementKindProduction:
			made, _ := productionSide(b.movLines[r.MovementID], false)
			if len(made) == 0 {
				return b.end(id, key, depth, m, m.out) // a production that made no lot
			}
			for _, c := range made {
				edge := &traceEdge{movementID: r.MovementID, postedAt: r.PostedAt, consumed: m.out, produced: c.quantity, source: sourceOfRow(r)}
				if err := b.lot(c.lotID, key, depth+1, edge); err != nil {
					return err
				}
			}
			return nil
		case movementKindIssue, movementKindUnbuild, string(gen.Adjustment), string(gen.Count),
			string(gen.TransferOut):
			return b.end(id, key, depth, m, m.out)
		}
		return nil
	}
	if m.in.Sign() == 0 {
		return nil
	}
	switch r.Kind {
	case movementKindProduction:
		parents, untracked := productionSide(b.movLines[r.MovementID], true)
		if len(parents) == 0 && (!b.opts.components || len(untracked) == 0) {
			return b.end(id, key, depth, m, m.in) // made of nothing traced
		}
		for _, p := range parents {
			edge := &traceEdge{movementID: r.MovementID, postedAt: r.PostedAt, consumed: p.quantity, produced: m.in, source: sourceOfRow(r)}
			if err := b.lot(p.lotID, key, depth+1, edge); err != nil {
				return err
			}
		}
		if b.opts.components {
			for _, c := range untracked {
				edge := &traceEdge{movementID: r.MovementID, postedAt: r.PostedAt, consumed: c.quantity, produced: m.in, source: sourceOfRow(r)}
				if _, err := b.add(traceNode{parent: key, depth: depth + 1, kind: traceComponent, variant: c.variant, edge: edge, quantity: c.quantity}); err != nil {
					return err
				}
			}
		}
		return nil
	case movementKindUnbuild, string(gen.Receipt), string(gen.Adjustment),
		string(gen.Count):
		return b.end(id, key, depth, m, m.in)
	}
	return nil
}

// downstreamLots is the lots of a usage walk below its root that still hold
// stock, in id order: what a block with the downstream list blocks (M92).
func (g traceGraph) downstreamLots(root int64) []int64 {
	var out []int64
	for id, lot := range g.lots {
		if id != root && lot.ID == id && g.onHand[id] != nil && g.onHand[id].Sign() > 0 {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
