package manufacturing

import (
	"math/big"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// TestMemberFigures is M118: a member's setup (unless the operation set its
// own), cleanup, capacity, efficiency and both cost rates are taken, and the
// expected minutes recomputed with them — what the cost then multiplies.
func TestMemberFigures(t *testing.T) {
	t.Parallel()
	w := store.ManufacturingWorkOrder{ID: 7, SetupMinutes: numeric(big.NewRat(5, 1), 2), MinutesPerCycle: numeric(big.NewRat(2, 1), 2),
		BomOutputQuantity: numeric(big.NewRat(1, 1), 3)}
	c := store.ManufacturingWorkCenter{ID: 9, SetupMinutes: numeric(big.NewRat(10, 1), 2), CleanupMinutes: numeric(big.NewRat(3, 1), 2),
		Capacity: 2, TimeEfficiency: 50, MachineCostPerHour: numeric(big.NewRat(600, 1), 2), LabourCostPerHour: numeric(big.NewRat(300, 1), 2)}
	// 10 units, capacity 2: 5 cycles × 2 minutes × 100 / 50 = 20, + 10 setup + 3 cleanup.
	f := memberFigures(w, c, big.NewRat(10, 1))
	if f.WorkCenterID != 9 || f.ExpectedMinutes != 33 || f.Capacity != 2 || f.TimeEfficiency != 50 ||
		ratOrZero(f.MachineRate).Cmp(big.NewRat(600, 1)) != 0 || ratOrZero(f.LabourRate).Cmp(big.NewRat(300, 1)) != 0 {
		t.Errorf("figures %+v", f)
	}
	w.OwnSetup = true
	if f := memberFigures(w, c, big.NewRat(10, 1)); f.ExpectedMinutes != 28 || ratOrZero(f.SetupMinutes).Cmp(big.NewRat(5, 1)) != 0 {
		t.Errorf("with its own setup %+v", f)
	}
}

// TestJudgeDependencies is M112's rules: a diamond into the last is kept; a
// dependency on an operation not before it by sequence is operation_order,
// and a branch nothing joins is operation_unjoined, each on its field.
func TestJudgeDependencies(t *testing.T) {
	t.Parallel()
	ops := []opValues{{key: "a", name: "A", sequence: 1}, {key: "b", name: "B", sequence: 2}, {key: "c", name: "C", sequence: 3}, {key: "d", name: "D", sequence: 4}}
	all := []bool{true, true, true, true}
	var f fieldErrors
	judgeDependencies(&f, ops, [][]string{{}, {"a"}, {"a"}, {"b", "c"}}, all)
	if f.failed() {
		t.Fatalf("a diamond refused: %+v", f)
	}
	f = fieldErrors{}
	deps := judgeDependencies(&f, ops, [][]string{{"c"}, nil, {"b"}, nil}, []bool{true, false, true, false})
	if f.code != codeOperationOrder || len(f.errs["operations[0].dependsOn"]) != 1 {
		t.Fatalf("a dependency on a later one %+v (deps %v)", f, deps)
	}
	if got := f.errs["operations[0].dependsOn"][0]; got != "C does not come before A in sequence" {
		t.Errorf("message %q", got)
	}
	f = fieldErrors{}
	judgeDependencies(&f, ops, [][]string{{}, {"b"}, {"a"}, {"c"}}, all)
	if f.code != codeOperationOrder || len(f.errs["operations[1].dependsOn"]) != 1 {
		t.Errorf("a self-dependency %+v", f)
	}
	f = fieldErrors{}
	// B runs after A, but nothing runs after B: a branch that leads nowhere.
	judgeDependencies(&f, ops, [][]string{{}, {"a"}, {"a"}, {"c"}}, all)
	if f.code != codeOperationUnjoined || len(f.errs["operations[1].dependsOn"]) != 1 || len(f.errs) != 1 {
		t.Errorf("an unjoined branch %+v", f)
	}
}

// TestTopological is M115's walk: predecessors first, by sequence among
// those ready together.
func TestTopological(t *testing.T) {
	t.Parallel()
	wos := []store.ManufacturingWorkOrder{{ID: 4, Sequence: 40}, {ID: 1, Sequence: 10}, {ID: 3, Sequence: 30}, {ID: 2, Sequence: 20}}
	g := graphOf([]store.ManufacturingWorkOrderDependency{{WorkOrderID: 2, DependsOnID: 3}, {WorkOrderID: 4, DependsOnID: 2}, {WorkOrderID: 3, DependsOnID: 1}})
	var ids []int64
	for _, w := range g.topological(wos) {
		ids = append(ids, w.ID)
	}
	if !slices.Equal(ids, []int64{1, 3, 2, 4}) {
		t.Errorf("order %v", ids)
	}
	if a := g.ancestors(4); len(a) != 3 || !a[1] || !a[2] || !a[3] {
		t.Errorf("ancestors %v", a)
	}
}
