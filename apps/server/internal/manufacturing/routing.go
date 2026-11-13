package manufacturing

// Routing (manufacturing phase 2 M112–M119): the operations' graph and its
// cycle check, the work orders' graph walked in a topological order, a
// group work order's member and the figures taken again from it, and the
// send-ahead quantity's arithmetic.

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// ---- the operations' graph ------------------------------------------------

// judgeDependencies settles each operation's dependsOn (M112): absent is the
// operation before it by sequence, a key no operation of the request has is
// operation_unknown on the field, and a cycle is operation_cycle naming its
// path. ops are in the request's order; deps is answered in the same order,
// as keys.
func judgeDependencies(f *fieldErrors, ops []opValues, dependsOn [][]string, sent []bool) [][]string {
	out := make([][]string, len(ops))
	keys := map[string]int{}
	for i, o := range ops {
		keys[o.key] = i
	}
	bySequence := make([]int, len(ops))
	for i := range ops {
		bySequence[i] = i
	}
	slices.SortStableFunc(bySequence, func(a, b int) int { return cmp.Compare(ops[a].sequence, ops[b].sequence) })
	for at, i := range bySequence {
		if !sent[i] {
			if at > 0 {
				out[i] = []string{ops[bySequence[at-1]].key}
			}
			continue
		}
		seen := map[string]bool{}
		for _, raw := range dependsOn[i] {
			key := strings.TrimSpace(raw)
			if _, ok := keys[key]; !ok {
				f.addCoded(fmt.Sprintf("operations[%d].dependsOn", i), codeOperationUnknown,
					fmt.Sprintf("No operation of this bill of materials has the key %q", key))
				continue
			}
			if !seen[key] {
				seen[key] = true
				out[i] = append(out[i], key)
			}
		}
	}
	if f.failed() {
		return out
	}
	if path := operationCycle(ops, out, keys); path != nil {
		names := make([]string, 0, len(path))
		for _, i := range path {
			names = append(names, ops[i].name)
		}
		f.addCoded("operations", codeOperationCycle, "The operations depend on each other in a circle: "+strings.Join(names, " → "))
	}
	return out
}

// operationCycle is a cycle of the operations' graph as indexes, the first
// repeated at the end, nil when there is none.
func operationCycle(ops []opValues, deps [][]string, keys map[string]int) []int {
	const (
		unseen = iota
		open
		closed
	)
	state := make([]int, len(ops))
	var stack []int
	var found []int
	var visit func(i int) bool
	visit = func(i int) bool {
		state[i] = open
		stack = append(stack, i)
		for _, key := range deps[i] {
			j := keys[key]
			switch state[j] {
			case open:
				at := slices.Index(stack, j)
				found = append(slices.Clone(stack[at:]), j)
				return true
			case unseen:
				if visit(j) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[i] = closed
		return false
	}
	for i := range ops {
		if state[i] == unseen && visit(i) {
			return found
		}
	}
	return nil
}

// ---- the work orders' graph -----------------------------------------------

// workOrderGraph is an order's work orders' edges: each work order's
// predecessors and successors.
type workOrderGraph struct {
	preds map[int64][]int64
	succs map[int64][]int64
}

// graphOf is the edges read for some orders as a graph.
func graphOf(rows []store.ManufacturingWorkOrderDependency) workOrderGraph {
	g := workOrderGraph{preds: map[int64][]int64{}, succs: map[int64][]int64{}}
	for _, r := range rows {
		g.preds[r.WorkOrderID] = append(g.preds[r.WorkOrderID], r.DependsOnID)
		g.succs[r.DependsOnID] = append(g.succs[r.DependsOnID], r.WorkOrderID)
	}
	return g
}

// readGraph is the orders' work order graph.
func readGraph(ctx context.Context, q *store.Queries, orderIDs []int64) (workOrderGraph, error) {
	rows, err := q.DependenciesOfOrders(ctx, orEmpty(orderIDs))
	if err != nil {
		return workOrderGraph{}, err
	}
	return graphOf(rows), nil
}

// walk is every work order reachable from id along next, id left out.
func walk(id int64, next map[int64][]int64) map[int64]bool {
	out := map[int64]bool{}
	todo := []int64{id}
	for len(todo) > 0 {
		x := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, y := range next[x] {
			if !out[y] && y != id {
				out[y] = true
				todo = append(todo, y)
			}
		}
	}
	return out
}

// ancestors is every work order id depends on, directly or not.
func (g workOrderGraph) ancestors(id int64) map[int64]bool { return walk(id, g.preds) }

// descendants is every work order that depends on id, directly or not.
func (g workOrderGraph) descendants(id int64) map[int64]bool { return walk(id, g.succs) }

// topological is wos in an order where each comes after its predecessors
// among them, by sequence then id among those ready together (M115). A
// cycle, which the BoM's judgement never lets through, leaves the rest in
// sequence order at the end.
func (g workOrderGraph) topological(wos []store.ManufacturingWorkOrder) []store.ManufacturingWorkOrder {
	in := map[int64]bool{}
	for _, w := range wos {
		in[w.ID] = true
	}
	waiting := map[int64]int{}
	for _, w := range wos {
		for _, p := range g.preds[w.ID] {
			if in[p] {
				waiting[w.ID]++
			}
		}
	}
	bySequence := slices.Clone(wos)
	slices.SortFunc(bySequence, func(a, b store.ManufacturingWorkOrder) int {
		return cmp.Or(cmp.Compare(a.Sequence, b.Sequence), cmp.Compare(a.ID, b.ID))
	})
	out := make([]store.ManufacturingWorkOrder, 0, len(wos))
	done := map[int64]bool{}
	for len(out) < len(wos) {
		progressed := false
		for _, w := range bySequence {
			if done[w.ID] || waiting[w.ID] > 0 {
				continue
			}
			done[w.ID] = true
			out = append(out, w)
			for _, s := range g.succs[w.ID] {
				waiting[s]--
			}
			progressed = true
			break
		}
		if !progressed {
			for _, w := range bySequence {
				if !done[w.ID] {
					done[w.ID] = true
					out = append(out, w)
				}
			}
		}
	}
	return out
}

// ---- figures and the member -----------------------------------------------

// runMinutes is the run of quantity units at a center (M28a): cycles ×
// minutes per cycle × 100 / efficiency, cycles = ⌈(quantity / output) /
// capacity⌉, exact.
func runMinutes(quantity, output, perCycle *big.Rat, capacity, efficiency int32) *big.Rat {
	if output.Sign() <= 0 {
		output = big.NewRat(1, 1)
	}
	cycles := ceilInt(new(big.Rat).Quo(new(big.Rat).Quo(quantity, output), big.NewRat(int64(max(capacity, 1)), 1)))
	run := new(big.Rat).Mul(new(big.Rat).SetInt(cycles), perCycle)
	run.Mul(run, hundred)
	return run.Quo(run, big.NewRat(int64(max(efficiency, 1)), 1))
}

// expectedFor is M28a's expected minutes: setup + cleanup + the run of the
// order's quantity, half-up to the minute, held at maxExpectedMinutes.
func expectedFor(setup, cleanup, perCycle *big.Rat, capacity, efficiency int32, quantity, output *big.Rat) *big.Int {
	total := new(big.Rat).Add(setup, cleanup)
	total.Add(total, runMinutes(quantity, output, perCycle, capacity, efficiency))
	minutes := roundInt(total)
	if minutes.Cmp(maxExpectedMinutes) > 0 {
		return maxExpectedMinutes
	}
	return minutes
}

// ratDuration is minutes as a duration, to the second.
func ratDuration(minutes *big.Rat) time.Duration {
	seconds := roundInt(new(big.Rat).Mul(minutes, big.NewRat(60, 1)))
	if !seconds.IsInt64() || seconds.Int64() > int64(maxPlanDuration) {
		return maxPlanDuration
	}
	return time.Duration(seconds.Int64()) * time.Second
}

// maxPlanDuration bounds a duration the planner computes: far past the
// horizon, so a fill of it never fits.
const maxPlanDuration = 400 * 24 * time.Hour

// memberFigures is the work order's figures at member c (M118): the setup the
// operation's own when it set one, else the member's; the member's cleanup,
// capacity, efficiency and rates; and the expected minutes recomputed with
// them for the order's quantity.
func memberFigures(w store.ManufacturingWorkOrder, c store.ManufacturingWorkCenter, quantity *big.Rat) store.SetWorkOrderMemberParams {
	setup := c.SetupMinutes
	if w.OwnSetup {
		setup = w.SetupMinutes
	}
	minutes := expectedFor(ratOrZero(setup), ratOrZero(c.CleanupMinutes), ratOrZero(w.MinutesPerCycle), c.Capacity, c.TimeEfficiency,
		quantity, ratOrZero(w.BomOutputQuantity))
	return store.SetWorkOrderMemberParams{
		ID: w.ID, WorkCenterID: c.ID, SetupMinutes: setup, CleanupMinutes: c.CleanupMinutes, Capacity: c.Capacity,
		TimeEfficiency: c.TimeEfficiency, MachineRate: c.MachineCostPerHour, LabourRate: c.LabourCostPerHour,
		ExpectedMinutes: minutes.Int64(),
	}
}

// sendAheadOf is the work order's send-ahead quantity when it counts: nil
// for none, or one at or above the order's quantity (M116).
func sendAheadOf(w store.ManufacturingWorkOrder, quantity *big.Rat) *big.Rat {
	q := ratOf(w.SendAheadQuantity)
	if q == nil || q.Cmp(quantity) >= 0 {
		return nil
	}
	return q
}

// groupMembers is the members of the groups, by group, by code.
func groupMembers(ctx context.Context, q *store.Queries, groupIDs []int64) (map[int64][]store.ManufacturingWorkCenter, error) {
	out := map[int64][]store.ManufacturingWorkCenter{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := q.GroupMembers(ctx, slices.Compact(slices.Sorted(slices.Values(groupIDs))))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GroupID] = append(out[r.GroupID], r.ManufacturingWorkCenter)
	}
	return out, nil
}

// withLogs is which of the work orders have a time log: a group work order's
// member is fixed once one does (M119).
func withLogs(ctx context.Context, q *store.Queries, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.WorkOrdersWithLogs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// notInGroup is the 400 work_center_not_in_group.
func notInGroup(title string) error {
	return refuseField(title, "workCenterId", codeWorkCenterNotInGroup, "Not a member of the work order's work center group")
}

// groupWork notes the queue of every member of the groups wos target
// (manufacturing phase 2 M119, §17): a group work order is on each one's
// Group work until its member is fixed, so each of them changes with it.
func (n *notices) groupWork(ctx context.Context, q *store.Queries, wos ...store.ManufacturingWorkOrder) error {
	var groups []int64
	for _, w := range wos {
		if w.WorkCenterGroupID != nil {
			groups = append(groups, *w.WorkCenterGroupID)
		}
	}
	members, err := groupMembers(ctx, q, groups)
	if err != nil {
		return err
	}
	for _, centers := range members {
		for _, c := range centers {
			n.workCenters[c.ID] = true
		}
	}
	return nil
}
