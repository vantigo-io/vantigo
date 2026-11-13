package manufacturing

// The BoM overview (manufacturing design M15, M15a, M28a): the BoM exploded
// through each component's default active BoM, with the requirement, the
// operations' expected time, the availability at a site and the rolled-up
// cost. The module's own rows are read level by level in one snapshot; the
// catalog and the stock are read after it, on the pool, each variant once.

import (
	"context"
	"fmt"
	"math/big"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// maxOverviewNodes is how many nodes an overview answers at most (M15a).
const maxOverviewNodes = 2000

// maxExpectedMinutes is the most expected minutes one operation of an
// overview answers: 10^12 minutes, about two million years. Past it the
// overview is refused bom_too_large rather than answer a number that wraps.
var maxExpectedMinutes = new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil)

// tooLarge is the 400 bom_too_large.
func tooLarge() error {
	return refuse(problem(http.StatusBadRequest, codeBomTooLarge, "The bill of materials is too large to explode",
		fmt.Sprintf("Exploded through its components' default BoMs it has more than %d nodes.", maxOverviewNodes)))
}

// tooManyMinutes is the 400 bom_too_large for an operation whose expected
// time at the quantity passes maxExpectedMinutes.
func tooManyMinutes() error {
	return refuse(problem(http.StatusBadRequest, codeBomTooLarge, "The bill of materials is too large to explode",
		fmt.Sprintf("At this quantity an operation's expected time passes %s minutes. Ask for a smaller quantity.", maxExpectedMinutes)))
}

// graphBom is one BoM of an overview's graph, with its operations and lines.
type graphBom struct {
	row   store.ManufacturingBom
	ops   []store.OperationsOfBomsRow
	lines []store.ManufacturingBomLine
	// loaded is whether ops and lines were read: a BoM first met past the
	// depth limit is not, and is never exploded.
	loaded bool
}

// bomGraph is the BoMs an overview can reach, and each component's default
// active BoM.
type bomGraph struct {
	boms      map[int64]*graphBom
	defaultOf map[int32]int64
}

// loadGraph reads, level by level in one snapshot, the BoM root and every
// default active BoM its components reach, up to maxLevels BoMs deep.
func loadGraph(ctx context.Context, q *store.Queries, root store.ManufacturingBom) (bomGraph, error) {
	g := bomGraph{boms: map[int64]*graphBom{root.ID: {row: root}}, defaultOf: map[int32]int64{}}
	resolved := map[int32]bool{}
	frontier := []int64{root.ID}
	for level := 1; len(frontier) > 0 && level <= maxLevels; level++ {
		ops, err := q.OperationsOfBoms(ctx, frontier)
		if err != nil {
			return g, err
		}
		lines, err := q.LinesOfBoms(ctx, frontier)
		if err != nil {
			return g, err
		}
		for _, id := range frontier {
			g.boms[id].loaded = true
		}
		for _, o := range ops {
			g.boms[o.BomID].ops = append(g.boms[o.BomID].ops, o)
		}
		var components []int32
		for _, l := range lines {
			g.boms[l.BomID].lines = append(g.boms[l.BomID].lines, l)
			if !resolved[l.VariantID] {
				resolved[l.VariantID] = true
				components = append(components, l.VariantID)
			}
		}
		defaults, err := q.DefaultActiveBomsOfVariants(ctx, components)
		if err != nil {
			return g, err
		}
		var next []int64
		for _, d := range defaults {
			g.defaultOf[d.VariantID] = d.ID
			if _, ok := g.boms[d.ID]; !ok {
				g.boms[d.ID] = &graphBom{row: store.ManufacturingBom{ID: d.ID, VariantID: d.VariantID, OutputQuantity: d.OutputQuantity}}
				next = append(next, d.ID)
			}
		}
		frontier = next
	}
	return g, nil
}

// node is one node of the exploded tree, before the catalog is read.
type node struct {
	variant  int32
	bom      *graphBom
	depth    int
	quantity *big.Rat
	scrap    *big.Rat
	ops      []nodeOperation
	children []*node
}

// nodeOperation is an operation of a made node at the node's quantity.
type nodeOperation struct {
	row     store.OperationsOfBomsRow
	minutes *big.Int
	cost    *big.Rat
}

// explosion builds the tree, counting its nodes.
type explosion struct {
	graph bomGraph
	count int
}

// explode is the node of variant at quantity, made by bom when it is not
// nil: its operations' expected time (M28a) and its children's requirements,
// each line.quantity × quantity / outputQuantity × (1 + scrap/100), rounded
// up to three decimals (M18).
func (e *explosion) explode(variant int32, bom *graphBom, quantity, scrap *big.Rat, depth int) (*node, error) {
	e.count++
	if e.count > maxOverviewNodes {
		return nil, tooLarge()
	}
	n := &node{variant: variant, bom: bom, depth: depth, quantity: quantity, scrap: scrap}
	if bom == nil {
		return n, nil
	}
	if depth >= maxLevels || !bom.loaded {
		return nil, tooDeep()
	}
	output := ratOrZero(bom.row.OutputQuantity)
	for _, o := range bom.ops {
		minutes := expectedMinutes(o, quantity, output)
		if minutes.Cmp(maxExpectedMinutes) > 0 {
			return nil, tooManyMinutes()
		}
		rate := new(big.Rat).Add(ratOrZero(o.WorkCenterMachineCostPerHour), ratOrZero(o.WorkCenterLabourCostPerHour))
		cost := new(big.Rat).Mul(new(big.Rat).Quo(new(big.Rat).SetInt(minutes), sixty), rate)
		n.ops = append(n.ops, nodeOperation{row: o, minutes: minutes, cost: cost})
	}
	for _, l := range bom.lines {
		lineScrap := ratOrZero(l.ScrapPercent)
		need := new(big.Rat).Mul(ratOrZero(l.Quantity), quantity)
		need.Quo(need, output)
		need.Mul(need, new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(lineScrap, hundred)))
		var child *graphBom
		if id, ok := e.graph.defaultOf[l.VariantID]; ok {
			child = e.graph.boms[id]
		}
		c, err := e.explode(l.VariantID, child, ceilPlaces(need, quantityPlaces), lineScrap, depth+1)
		if err != nil {
			return nil, err
		}
		n.children = append(n.children, c)
	}
	return n, nil
}

// expectedMinutes is an operation's expected time at quantity (M28a):
// cycles = ⌈(quantity / outputQuantity) / capacity⌉, and setup (the
// operation's, else the work center's) + cleanup + cycles × minutesPerCycle ×
// 100 / timeEfficiency, half-up to the minute.
func expectedMinutes(o store.OperationsOfBomsRow, quantity, output *big.Rat) *big.Int {
	capacity := big.NewRat(int64(max(o.WorkCenterCapacity, 1)), 1)
	cycles := ceilInt(new(big.Rat).Quo(new(big.Rat).Quo(quantity, output), capacity))
	setup := ratOf(o.SetupMinutes)
	if setup == nil {
		setup = ratOrZero(o.WorkCenterSetupMinutes)
	}
	run := new(big.Rat).Mul(new(big.Rat).SetInt(cycles), ratOrZero(o.MinutesPerCycle))
	run.Mul(run, hundred)
	run.Quo(run, big.NewRat(int64(max(o.WorkCenterTimeEfficiency, 1)), 1))
	total := new(big.Rat).Add(setup, ratOrZero(o.WorkCenterCleanupMinutes))
	total.Add(total, run)
	return roundInt(total)
}

// variants is every variant of the tree, once.
func (n *node) variants(into map[int32]bool, out []int32) []int32 {
	if !into[n.variant] {
		into[n.variant] = true
		out = append(out, n.variant)
	}
	for _, c := range n.children {
		out = c.variants(into, out)
	}
	return out
}

// standardCost is the catalog's standard cost of a variant, nil when it has
// none.
func standardCost(entries map[int32]contracts.VariantEntry, id int32) *big.Rat {
	v, ok := entries[id]
	if !ok || v.StandardCost == "" {
		return nil
	}
	r, ok := new(big.Rat).SetString(v.StandardCost)
	if !ok {
		return nil
	}
	return r
}

// rollUp is the rolled-up cost (M15), from the root's children: a node with
// a standard cost of its own counts its extended value and stands for its
// subtree; a made node without one is descended into, its operations
// counting; a bought node without one counts 0 and makes the cost
// incomplete. The root's own operations always count.
type rollUp struct {
	components, operations *big.Rat
	incomplete             bool
}

func (r *rollUp) root(n *node, entries map[int32]contracts.VariantEntry) {
	r.ops(n)
	for _, c := range n.children {
		r.visit(c, entries)
	}
}

func (r *rollUp) visit(n *node, entries map[int32]contracts.VariantEntry) {
	if std := standardCost(entries, n.variant); std != nil {
		r.components.Add(r.components, new(big.Rat).Mul(n.quantity, std))
		return
	}
	if n.bom == nil {
		r.incomplete = true
		return
	}
	r.ops(n)
	for _, c := range n.children {
		r.visit(c, entries)
	}
}

func (r *rollUp) ops(n *node) {
	for _, o := range n.ops {
		r.operations.Add(r.operations, o.cost)
	}
}

// nodeOf is a node as the contract answers it.
func nodeOf(n *node, entries map[int32]contracts.VariantEntry, available map[int32]contracts.StockQuantity, site *int64, costs bool) gen.ManufacturingOverviewNode {
	out := gen.ManufacturingOverviewNode{
		Variant: refOf(entries, n.variant, costs), Made: n.bom != nil, Depth: int32(n.depth),
		QuantityRequired: text(n.quantity, quantityPlaces), ScrapPercent: text(n.scrap, percentPlaces),
		Operations: make([]gen.ManufacturingOverviewOperation, 0, len(n.ops)),
		Children:   make([]gen.ManufacturingOverviewNode, 0, len(n.children)),
	}
	if n.bom != nil {
		id := n.bom.row.ID
		out.BomId = &id
	}
	std := standardCost(entries, n.variant)
	out.StandardCost = amountJSON(costs, std)
	var extended *big.Rat
	if std != nil {
		extended = new(big.Rat).Mul(n.quantity, std)
	}
	out.ExtendedValue = amountJSON(costs, extended)
	if site != nil {
		q := "0"
		if a, ok := available[n.variant]; ok {
			q = a.Quantity
		}
		out.Available = &q
	}
	for _, o := range n.ops {
		out.Operations = append(out.Operations, gen.ManufacturingOverviewOperation{
			OperationId: o.row.ID, Sequence: o.row.Sequence, Name: o.row.Name, WorkCenterId: o.row.CenterID,
			WorkCenterCode: o.row.WorkCenterCode, ExpectedMinutes: o.minutes.Int64(), Cost: amountJSON(costs, o.cost),
		})
	}
	for _, c := range n.children {
		out.Children = append(out.Children, nodeOf(c, entries, available, site, costs))
	}
	return out
}

// GetManufacturingBomOverview explodes a BoM at a quantity (its output
// quantity by default), with the availability at a site when one is asked.
func (s *server) GetManufacturingBomOverview(ctx context.Context, req gen.GetManufacturingBomOverviewRequestObject) (gen.GetManufacturingBomOverviewResponseObject, error) {
	reply := answers[gen.GetManufacturingBomOverviewResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.GetManufacturingBomOverviewResponseObject {
			return gen.GetManufacturingBomOverview400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.GetManufacturingBomOverviewResponseObject {
			return gen.GetManufacturingBomOverview404Response{}
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingBomOverviewResponseObject {
			return gen.GetManufacturingBomOverview503ApplicationProblemPlusJSONResponse(p)
		},
	}
	const title = "The overview was not made"
	var quantity *big.Rat
	if req.Params.Quantity != nil {
		q, msg := parsePositive(*req.Params.Quantity, quantityPlaces, maxQuantity)
		if msg != "" {
			return answer(refuseField(title, "quantity", codeQuantityInvalid, msg), reply)
		}
		quantity = q
	}
	if req.Params.SiteId != nil {
		sites, err := s.activeSites(ctx)
		if err != nil {
			return answer(err, reply)
		}
		if _, ok := siteOf(sites, *req.Params.SiteId); !ok {
			return answer(refuseField(title, "siteId", codeSiteUnknown, "Not an active Inventory site"), reply)
		}
	}

	var root *node
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		b, err := q.GetBom(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if quantity == nil {
			quantity = ratOrZero(b.OutputQuantity)
		}
		g, err := loadGraph(ctx, q, b)
		if err != nil {
			return err
		}
		e := explosion{graph: g}
		root, err = e.explode(b.VariantID, g.boms[b.ID], quantity, new(big.Rat), 0)
		return err
	})
	if err != nil {
		return answer(err, reply)
	}

	ids := root.variants(map[int32]bool{}, nil)
	entries, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return answer(err, reply)
	}
	var available map[int32]contracts.StockQuantity
	if req.Params.SiteId != nil {
		if available, err = s.stockAvailable(ctx, ids, req.Params.SiteId); err != nil {
			return answer(err, reply)
		}
	}
	costs := s.has(ctx, permCostsView)
	out := gen.ManufacturingBomOverview{
		BomId: req.Id, Quantity: text(quantity, quantityPlaces), SiteId: req.Params.SiteId, NodeCount: int32(countNodes(root)),
		Root: nodeOf(root, entries, available, req.Params.SiteId, costs),
	}
	if costs {
		r := rollUp{components: new(big.Rat), operations: new(big.Rat)}
		r.root(root, entries)
		total := new(big.Rat).Add(r.components, r.operations)
		out.RolledUpCost = &gen.ManufacturingRolledUpCost{
			Components: amountText(r.components), Operations: amountText(r.operations), Total: amountText(total),
		}
		incomplete := r.incomplete
		out.CostIncomplete = &incomplete
	}
	return gen.GetManufacturingBomOverview200JSONResponse(out), nil
}

// countNodes is how many nodes the tree has, the root included.
func countNodes(n *node) int {
	total := 1
	for _, c := range n.children {
		total += countNodes(c)
	}
	return total
}
