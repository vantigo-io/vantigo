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
// stops.
func walkBom(ctx context.Context, txq *store.Queries, product int32, components []int32) error {
	components = dedupe(components)
	if slices.Contains(components, product) {
		return &cycleError{path: []int32{product, product}}
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
		variantOf := map[int64]int32{}
		for _, d := range defaults {
			ids = append(ids, d.ID)
			variantOf[d.ID] = d.VariantID
			children[d.VariantID] = []int32{}
		}
		lines, err := txq.LinesOfBoms(ctx, ids)
		if err != nil {
			return err
		}
		var next []int32
		for _, l := range lines {
			parent := variantOf[l.BomID]
			children[parent] = append(children[parent], l.VariantID)
			if !seen[l.VariantID] {
				seen[l.VariantID] = true
				next = append(next, l.VariantID)
			}
		}
		frontier = next
	}

	w := walker{product: product, children: children, height: map[int32]int{}, onPath: map[int32]bool{}}
	deepest := 0
	for _, c := range components {
		h, err := w.visit(c, []int32{product})
		if err != nil {
			return err
		}
		deepest = max(deepest, h)
	}
	if 1+deepest > maxLevels {
		return tooDeep()
	}
	return nil
}

// walker is one walk's depth-first search: each variant's height (how many
// BoMs deep it goes, 0 for a bought one), memoised, and the variants on the
// current path, so reaching one again is a cycle.
type walker struct {
	product  int32
	children map[int32][]int32
	height   map[int32]int
	onPath   map[int32]bool
}

func (w *walker) visit(v int32, path []int32) (int, error) {
	path = append(slices.Clone(path), v)
	if v == w.product || w.onPath[v] {
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
