package manufacturing

// The cycle and depth walk (manufacturing design M14, M14a): run inside a
// structural BoM write, after LockBomGraph, so the graph it reads is the one
// the write commits into.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// maxLevels is how many BoMs deep a walk and an overview go (M14): the BoM
// itself is level 1, its components' BoMs level 2.
const maxLevels = 10

// cycleError is a walk's cycle, the variants from the BoM's own back to it.
// It leaves the transaction as it is and is turned into the 400 bom_cycle
// after it, where the catalog may be read for the path's SKUs (cycleRefusal).
type cycleError struct{ path []int32 }

func (e *cycleError) Error() string {
	return fmt.Sprintf("manufacturing: a BoM cycle through %v", e.path)
}

// tooDeep is the 400 bom_too_deep.
func tooDeep() error {
	return refuse(problem(http.StatusBadRequest, codeBomTooDeep, "The bill of materials is too deep",
		fmt.Sprintf("Through its components' default BoMs it goes more than %d levels deep. Nothing was saved.", maxLevels)))
}

// walkBom refuses a BoM of product whose components, through each
// component's default active BoM, reach product (bom_cycle), or go more than
// maxLevels BoMs deep (bom_too_deep). The BoM being written is level 1 and
// is never read: its components are given, and product is where every walk
// stops. Its by-products, outputs, stop a walk as product does (manufacturing
// phase 2 M120): a by-product reached through the components is one of its
// own components, a cycle. The graph gains an edge from each by-product of a
// default active BoM to that BoM's components, so a BoM consuming another's
// by-product walks into the BoM that makes it. A BoM that is, or becomes,
// product's default also counts the
// default BoMs above it — those that consume product, through theirs — since
// an overview of the topmost one explodes through it: their levels and its
// own together go no more than maxLevels deep.
func walkBom(ctx context.Context, txq *store.Queries, product int32, components, outputs []int32, asDefault bool) error {
	components = dedupe(components)
	if slices.Contains(components, product) {
		return &cycleError{path: []int32{product, product}}
	}
	for _, o := range outputs {
		if slices.Contains(components, o) {
			return &cycleError{path: []int32{product, o, product}}
		}
	}
	// Load the reachable graph a level at a time: each variant's default
	// active BoM's components, as far as the limit could need.
	children := map[int32][]int32{}
	seen := map[int32]bool{product: true}
	frontier := components
	for _, v := range frontier {
		seen[v] = true
	}
	for level := 2; len(frontier) > 0 && level <= maxLevels+1; level++ {
		defaults, err := txq.DefaultActiveBomsOfVariants(ctx, frontier)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(defaults))
		variantsOf := map[int64][]int32{}
		for _, d := range defaults {
			ids = append(ids, d.ID)
			variantsOf[d.ID] = append(variantsOf[d.ID], d.VariantID)
			children[d.VariantID] = []int32{}
		}
		// A by-product's edge to the BoM that makes it (M120).
		making, err := txq.DefaultBomsMakingVariants(ctx, frontier)
		if err != nil {
			return err
		}
		for _, m := range making {
			if !slices.Contains(ids, m.BomID) {
				ids = append(ids, m.BomID)
			}
			variantsOf[m.BomID] = append(variantsOf[m.BomID], m.OutputID)
			if _, ok := children[m.OutputID]; !ok {
				children[m.OutputID] = []int32{}
			}
		}
		lines, err := txq.LinesOfBoms(ctx, ids)
		if err != nil {
			return err
		}
		var next []int32
		for _, l := range lines {
			for _, parent := range variantsOf[l.BomID] {
				children[parent] = append(children[parent], l.VariantID)
			}
			if !seen[l.VariantID] {
				seen[l.VariantID] = true
				next = append(next, l.VariantID)
			}
		}
		frontier = next
	}

	w := walker{product: product, outputs: outputs, children: children, height: map[int32]int{}, onPath: map[int32]bool{}}
	deepest := 0
	for _, c := range components {
		h, err := w.visit(c, []int32{product})
		if err != nil {
			return err
		}
		deepest = max(deepest, h)
	}
	above := 0
	if asDefault {
		var err error
		if above, err = heightAbove(ctx, txq, product); err != nil {
			return err
		}
	}
	if above+1+deepest > maxLevels {
		return tooDeep()
	}
	return nil
}

// kitTooDeep is the 400 bom_too_deep of a kit (manufacturing phase 2 M144).
func kitTooDeep() error {
	return refuse(problem(http.StatusBadRequest, codeBomTooDeep, "The kit nests too deep",
		fmt.Sprintf("Kits nest at most %d levels: a kit, a kit among its components and one among theirs. Nothing was saved.", contracts.MaxKitLevels)))
}

// walkKit refuses a kit that nests more than contracts.MaxKitLevels kits deep
// (bom_too_deep, manufacturing phase 2 M144): the kit is level 1, a
// component whose default active BoM is a kit level 2, and so on. A kit that
// is, or becomes, product's default also counts the kits above it — the
// variants whose default active BoM is a kit naming product, through theirs —
// since contracts.KitStructure explodes the topmost one through it. A normal
// BoM nests no kit and is not walked. It runs after walkBom, which has
// refused every cycle, so each level is read at most MaxKitLevels+1 times.
func walkKit(ctx context.Context, txq *store.Queries, product int32, kind string, components []int32, asDefault bool) error {
	if kind != kindKit {
		return nil
	}
	below, err := kitDepth(ctx, txq, components)
	if err != nil {
		return err
	}
	above := 0
	if asDefault {
		frontier := []int32{product}
		for above <= contracts.MaxKitLevels && len(frontier) > 0 {
			rows, err := txq.DefaultKitsNamingVariants(ctx, frontier)
			if err != nil {
				return err
			}
			var next []int32
			for _, r := range rows {
				next = append(next, r.ParentID)
			}
			if frontier = dedupe(next); len(frontier) > 0 {
				above++
			}
		}
	}
	if above+1+below > contracts.MaxKitLevels {
		return kitTooDeep()
	}
	return nil
}

// kitDepth is how many kits deep components go below a kit: 0 when none of
// them is a kit, one more for each level of default active kit BoMs, read a
// level at a time up to one past the limit.
func kitDepth(ctx context.Context, txq *store.Queries, components []int32) (int, error) {
	depth := 0
	frontier := dedupe(components)
	for depth <= contracts.MaxKitLevels && len(frontier) > 0 {
		defaults, err := txq.DefaultActiveBomsOfVariants(ctx, frontier)
		if err != nil {
			return 0, err
		}
		var kits []int64
		for _, d := range defaults {
			if d.Kind == kindKit {
				kits = append(kits, d.ID)
			}
		}
		if len(kits) == 0 {
			break
		}
		depth++
		lines, err := txq.LinesOfBoms(ctx, kits)
		if err != nil {
			return 0, err
		}
		frontier = dedupe(storedComponents(lines))
	}
	return depth, nil
}

// heightAbove is how many default active BoMs stand above product at most:
// the longest chain of variants whose default BoM consumes the one below,
// product's consumers first. It is read a level at a time, up to maxLevels,
// past which the answer is already too deep. The graph above is acyclic, as
// every write walks it; a loop met anyway ends the chain where it closes.
func heightAbove(ctx context.Context, txq *store.Queries, product int32) (int, error) {
	parents := map[int32][]int32{}
	seen := map[int32]bool{product: true}
	frontier := []int32{product}
	for level := 1; len(frontier) > 0 && level <= maxLevels; level++ {
		rows, err := txq.DefaultBomsNamingVariants(ctx, frontier)
		if err != nil {
			return 0, err
		}
		var next []int32
		for _, r := range rows {
			parents[r.ComponentID] = append(parents[r.ComponentID], r.ParentID)
			if !seen[r.ParentID] {
				seen[r.ParentID] = true
				next = append(next, r.ParentID)
			}
		}
		frontier = next
	}
	height := map[int32]int{}
	onPath := map[int32]bool{}
	var up func(v int32) int
	up = func(v int32) int {
		if h, ok := height[v]; ok {
			return h
		}
		onPath[v] = true
		highest := 0
		for _, p := range parents[v] {
			if onPath[p] {
				continue
			}
			highest = max(highest, 1+up(p))
		}
		delete(onPath, v)
		height[v] = highest
		return highest
	}
	return up(product), nil
}

// walker is one walk's depth-first search: each variant's height (how many
// BoMs deep it goes, 0 for a bought one), memoised, and the variants on the
// current path, so reaching one again is a cycle.
type walker struct {
	product  int32
	outputs  []int32
	children map[int32][]int32
	height   map[int32]int
	onPath   map[int32]bool
}

func (w *walker) visit(v int32, path []int32) (int, error) {
	path = append(slices.Clone(path), v)
	if v == w.product || w.onPath[v] || slices.Contains(w.outputs, v) {
		return 0, &cycleError{path: path}
	}
	if h, ok := w.height[v]; ok {
		return h, nil
	}
	kids, made := w.children[v]
	if !made {
		w.height[v] = 0
		return 0, nil
	}
	w.onPath[v] = true
	deepest := 0
	for _, c := range dedupe(kids) {
		h, err := w.visit(c, path)
		if err != nil {
			return 0, err
		}
		deepest = max(deepest, h)
	}
	delete(w.onPath, v)
	w.height[v] = 1 + deepest
	return w.height[v], nil
}

// dedupe is ids without repeats, in first-seen order.
func dedupe(ids []int32) []int32 {
	out := make([]int32, 0, len(ids))
	seen := map[int32]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// cycleRefusal turns a walk's cycle into the 400 bom_cycle, naming each
// variant of its path by its SKU — read from the catalog now, after the
// transaction has ended. Any other error is handed back as it is.
func (s *server) cycleRefusal(ctx context.Context, err error) error {
	var cycle *cycleError
	if !errors.As(err, &cycle) {
		return err
	}
	entries, readErr := s.catalogVariants(ctx, dedupe(cycle.path))
	if readErr != nil {
		return readErr
	}
	path := make([]gen.ManufacturingProblemPathStep, 0, len(cycle.path))
	for _, id := range cycle.path {
		path = append(path, gen.ManufacturingProblemPathStep{VariantId: id, Sku: entries[id].SKU})
	}
	return refuse(cycleProblem(path))
}
