package manufacturing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/live"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The routing tests (manufacturing phase 2 M112–M119): the operations'
// graph, readying and planning by it, the send-ahead quantity, work center
// groups and the member a group work order is planned or started at.

const groupsPath = base + "/work-center-groups"

func groupPath(id int64, rest ...string) string { return pathOf(groupsPath, id, rest...) }

func createGroup(t *testing.T, c *modtest.Client, code string, members ...int64) gen.ManufacturingWorkCenterGroup {
	t.Helper()
	res := c.Do(http.MethodPost, groupsPath, gen.ManufacturingWorkCenterGroupInput{Code: code, Name: "Group " + code, SiteId: siteMain, MemberIds: members})
	mustStatus(t, res, http.StatusCreated, "create group "+code)
	var g gen.ManufacturingWorkCenterGroup
	res.JSON(&g)
	return g
}

// after is op made to depend on keys.
func after(op gen.ManufacturingBomOperationInput, keys ...string) gen.ManufacturingBomOperationInput {
	if keys == nil {
		keys = []string{}
	}
	op.DependsOn = &keys
	return op
}

// atGroup is a BoM operation that targets a group.
func atGroup(key string, sequence int32, group int64, perCycle string) gen.ManufacturingBomOperationInput {
	return gen.ManufacturingBomOperationInput{Key: key, Sequence: sequence, Name: "Op " + key, WorkCenterGroupId: &group, MinutesPerCycle: perCycle}
}

// bikeWith makes ops the default BoM of the bicycle, with no lines, and
// answers it.
func bikeWith(t *testing.T, c *modtest.Client, ops ...gen.ManufacturingBomOperationInput) gen.ManufacturingBom {
	t.Helper()
	in := bomInput(varBike, "1")
	in.Operations = ops
	return createBom(t, c, in)
}

// confirmedBike is a confirmed order of qty bicycles and its work orders by
// sequence.
func confirmedBike(t *testing.T, c *modtest.Client, qty string) (gen.ManufacturingOrder, []gen.ManufacturingWorkOrder) {
	t.Helper()
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, qty))).Order
	return o, workOrdersOf(t, c, o.Id)
}

func statusesOf(wos []gen.ManufacturingWorkOrder) []gen.ManufacturingWorkOrderStatus {
	out := make([]gen.ManufacturingWorkOrderStatus, 0, len(wos))
	for _, w := range wos {
		out = append(out, w.Status)
	}
	return out
}

func dependsOf(w gen.ManufacturingWorkOrder) []int64 {
	if w.DependsOn == nil {
		return nil
	}
	return *w.DependsOn
}

// TestRouting_BomGraph is M112: dependsOn answered by key, absent meaning the
// operation before by sequence and empty meaning none; a cycle, an unknown key
// and an operation with both or neither target refused.
func TestRouting_BomGraph(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1")
	in.Operations = []gen.ManufacturingBomOperationInput{
		operation("a", 10, saw.Id, "1"), operation("b", 20, saw.Id, "1"), after(operation("c", 30, saw.Id, "1")),
		after(operation("d", 40, saw.Id, "1"), "b", "c"), operation("e", 50, saw.Id, "1"),
	}
	b := createBom(t, c, in)
	key := map[string]string{}
	for _, op := range b.Operations {
		key[op.Name[3:]] = op.Key
	}
	want := map[string][]string{"a": {}, "b": {key["a"]}, "c": {}, "d": {key["b"], key["c"]}, "e": {key["d"]}}
	for _, op := range b.Operations {
		got := slices.Sorted(slices.Values(op.DependsOn))
		if w := slices.Sorted(slices.Values(want[op.Name[3:]])); !slices.Equal(got, w) {
			t.Errorf("%s depends on %v, want %v", op.Name, got, w)
		}
	}

	in.Operations[0] = after(in.Operations[0], "d")
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "operation_order", "a after a later one")
	if p.Errors == nil || len((*p.Errors)["operations[0].dependsOn"]) != 1 {
		t.Errorf("the order's errors %+v", p.Errors)
	}
	in.Operations[0] = after(in.Operations[0])
	in.Operations[4] = after(in.Operations[4])
	p = expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "operation_unjoined", "d leading nowhere")
	if p.Errors == nil || len((*p.Errors)["operations[3].dependsOn"]) != 1 {
		t.Errorf("the unjoined branch's errors %+v", p.Errors)
	}
	in.Operations[4] = operation("e", 50, saw.Id, "1")
	in.Operations[0] = after(in.Operations[0], "zz")
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "operation_unknown", "an unknown key")
	in.Operations[0] = after(in.Operations[0])
	in.Operations[0].WorkCenterId = nil
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "", "an operation with no target")
}

// TestRouting_DiamondReadyingAndPlanning is M113–M115: a diamond graph is
// snapshotted on the work orders; the two branches are ready together once
// the first is done, the join only once both are; the planner places the
// branches side by side and the join after the later of them.
func TestRouting_DiamondReadyingAndPlanning(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	paint := createWorkCenter(t, c, workCenterInput("PAINT", siteMain))
	drill := createWorkCenter(t, c, workCenterInput("DRILL", siteMain))
	bikeWith(t, c, operation("a", 10, saw.Id, "1"), after(operation("b", 20, paint.Id, "1"), "a"),
		after(operation("c", 30, drill.Id, "2"), "a"), after(operation("d", 40, saw.Id, "1"), "b", "c"))
	o, wos := confirmedBike(t, c, "60")
	a, b, cc, d := wos[0], wos[1], wos[2], wos[3]
	if got := statusesOf(wos); !slices.Equal(got, []gen.ManufacturingWorkOrderStatus{"ready", "pending", "pending", "pending"}) {
		t.Fatalf("statuses at confirm %v", got)
	}
	if !slices.Equal(dependsOf(b), []int64{a.Id}) || !slices.Equal(dependsOf(d), []int64{b.Id, cc.Id}) || len(dependsOf(a)) != 0 {
		t.Errorf("graph %v %v %v", dependsOf(a), dependsOf(b), dependsOf(d))
	}

	// Planning: a 07:00–08:00; b at PAINT 08:00–09:00 beside c at DRILL
	// 08:00–10:00; the join after the later, 10:00–11:00.
	planOrder(t, c, o)
	wos = workOrdersOf(t, c, o.Id)
	for i, want := range [][2]time.Time{
		{osloAt(9, 14, 7, 0), osloAt(9, 14, 8, 0)}, {osloAt(9, 14, 8, 0), osloAt(9, 14, 9, 0)},
		{osloAt(9, 14, 8, 0), osloAt(9, 14, 10, 0)}, {osloAt(9, 14, 10, 0), osloAt(9, 14, 11, 0)},
	} {
		if !sameInstant(wos[i].PlannedStart, want[0]) || !sameInstant(wos[i].PlannedFinish, want[1]) {
			t.Errorf("%s planned %v–%v, want %v–%v", wos[i].Name, wos[i].PlannedStart, wos[i].PlannedFinish, want[0], want[1])
		}
	}

	act(t, c, a.Id, "start", nil)
	act(t, c, a.Id, "finish", nil)
	if got := statusesOf(workOrdersOf(t, c, o.Id)); !slices.Equal(got, []gen.ManufacturingWorkOrderStatus{"done", "ready", "ready", "pending"}) {
		t.Fatalf("after a %v", got)
	}
	act(t, c, b.Id, "start", nil)
	act(t, c, b.Id, "finish", nil)
	if got := statusesOf(workOrdersOf(t, c, o.Id)); got[3] != "pending" {
		t.Fatalf("the join is %s with one branch open", got[3])
	}
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(d.Id, "start"), nil), http.StatusConflict, "work_order_pending", "the join before its branches")
	act(t, c, cc.Id, "start", nil)
	act(t, c, cc.Id, "finish", nil)
	if got := statusesOf(workOrdersOf(t, c, o.Id)); got[3] != "ready" {
		t.Fatalf("the join is %s once both branches are done", got[3])
	}
}

// TestRouting_SendAhead is M114 and M116: Register quantity readies the
// successor once the send-ahead quantity is passed on, sent again it
// registers once, and the planner starts the successor at the send-ahead
// offset, holding its finish after the predecessor's plus its last batch.
func TestRouting_SendAhead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	paint := createWorkCenter(t, c, workCenterInput("PAINT", siteMain))
	a := operation("a", 10, saw.Id, "1")
	a.SendAheadQuantity = str("10")
	a.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Instruction, Title: "First piece", Frequency: gen.FirstPiece}}
	bikeWith(t, c, a, operation("b", 20, paint.Id, "0.5"))
	o, wos := confirmedBike(t, c, "40")
	if wos[0].SendAheadQuantity == nil || *wos[0].SendAheadQuantity != "10" {
		t.Fatalf("send-ahead %v", *wos[0].SendAheadQuantity)
	}

	// a: 40 minutes from 07:00; b may start once 10 are through, at 07:10,
	// but needs only 20 minutes and may not finish before 07:40 + 5 for its
	// last batch: 07:25–07:45.
	planOrder(t, c, o)
	wos = workOrdersOf(t, c, o.Id)
	if !sameInstant(wos[0].PlannedFinish, osloAt(9, 14, 7, 40)) || !sameInstant(wos[1].PlannedStart, osloAt(9, 14, 7, 25)) ||
		!sameInstant(wos[1].PlannedFinish, osloAt(9, 14, 7, 45)) {
		t.Errorf("a until %v; b %v–%v", wos[0].PlannedFinish, wos[1].PlannedStart, wos[1].PlannedFinish)
	}

	register := func(id uuid.UUID, workOrder int64, qty string) *modtest.Response {
		return c.Do(http.MethodPost, workOrderPath(workOrder, "quantity"), gen.ManufacturingQuantityInput{ClientRequestId: id, Quantity: qty})
	}
	expectRefusal(t, register(uuid.New(), wos[1].Id, "1"), http.StatusConflict, "work_order_pending", "a quantity on a pending work order")
	expectRefusal(t, register(uuid.New(), wos[0].Id, "5"), http.StatusConflict, "checks_incomplete", "a quantity before the first piece")
	recordCheck(t, c, wos[0].Id, checksOf(wos[0])[0].Id, gen.ManufacturingCheckResultInput{})
	first := uuid.New()
	mustStatus(t, register(first, wos[0].Id, "5"), http.StatusOK, "five through")
	if got := workOrdersOf(t, c, o.Id); got[1].Status != "pending" || got[0].QuantityDone == nil || *got[0].QuantityDone != "5" {
		t.Fatalf("after five: %s, done %v", got[1].Status, got[0].QuantityDone)
	}
	mustStatus(t, register(first, wos[0].Id, "5"), http.StatusOK, "the same registration again")
	expectRefusal(t, register(first, wos[0].Id, "6"), http.StatusConflict, "client_request_reused", "the id with another quantity")
	expectRefusal(t, register(uuid.New(), wos[0].Id, "0"), http.StatusBadRequest, "quantity_invalid", "zero")
	res := register(uuid.New(), wos[0].Id, "5")
	mustStatus(t, res, http.StatusOK, "ten through")
	got := workOrdersOf(t, c, o.Id)
	if got[1].Status != "ready" || *got[0].QuantityDone != "10" || got[0].Status != "ready" {
		t.Fatalf("after ten: a %s done %v, b %s", got[0].Status, *got[0].QuantityDone, got[1].Status)
	}
	// The last work order passes nothing on, and what a work order passes on
	// stays within the order's 40.
	expectRefusal(t, register(uuid.New(), wos[1].Id, "1"), http.StatusConflict, "work_order_is_output", "a quantity on the output work order")
	p := expectRefusal(t, register(uuid.New(), wos[0].Id, "31"), http.StatusBadRequest, "quantity_over_order", "past the order's quantity")
	if p.Errors == nil || len((*p.Errors)["quantity"]) != 1 {
		t.Errorf("the errors %+v", p.Errors)
	}
	mustStatus(t, register(uuid.New(), wos[0].Id, "30"), http.StatusOK, "up to the order's quantity")
}

// TestRouting_SendAheadAtTheQuantityIsNone is M116's last rule: a send-ahead
// quantity at or above the order's quantity behaves as none.
func TestRouting_SendAheadAtTheQuantityIsNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	a := operation("a", 10, saw.Id, "1")
	a.SendAheadQuantity = str("10")
	bikeWith(t, c, a, operation("b", 20, saw.Id, "1"))
	o, wos := confirmedBike(t, c, "10")
	mustStatus(t, c.Do(http.MethodPost, workOrderPath(wos[0].Id, "quantity"), gen.ManufacturingQuantityInput{ClientRequestId: uuid.New(), Quantity: "10"}),
		http.StatusOK, "all ten through")
	if got := workOrdersOf(t, c, o.Id); got[1].Status != "pending" {
		t.Errorf("b is %s before a is done", got[1].Status)
	}
	planOrder(t, c, o)
	if got := workOrdersOf(t, c, o.Id); !sameInstant(got[1].PlannedStart, *got[0].PlannedFinish) {
		t.Errorf("b from %v, a until %v", got[1].PlannedStart, got[0].PlannedFinish)
	}
}

// groupFloor is two members of group G — M1 and M2, both at full
// efficiency unless effM2 says otherwise — and PAINT beside them.
type groupFloor struct {
	m1, m2, paint gen.ManufacturingWorkCenter
	group         gen.ManufacturingWorkCenterGroup
}

func newGroupFloor(t *testing.T, c *modtest.Client, effM2 int32) groupFloor {
	t.Helper()
	var f groupFloor
	f.m1 = createWorkCenter(t, c, workCenterInput("M1", siteMain))
	in := workCenterInput("M2", siteMain)
	in.TimeEfficiency = &effM2
	f.m2 = createWorkCenter(t, c, in)
	f.paint = createWorkCenter(t, c, workCenterInput("PAINT", siteMain))
	f.group = createGroup(t, c, "G", f.m2.Id, f.m1.Id)
	return f
}

// TestRouting_Groups is M117 and M118: a group's members, its code, its
// site; an operation that targets it, answered with the group; the work
// order snapshotted at the first active member by code; archiving refused
// while a BoM targets it; a member's site kept.
func TestRouting_Groups(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	if len(f.group.Members) != 2 || f.group.Members[0].Code != "M1" || f.group.SiteCode != "MAIN" {
		t.Fatalf("group %+v", f.group)
	}
	res := c.Do(http.MethodPost, groupsPath, gen.ManufacturingWorkCenterGroupInput{Code: "g", Name: "Again", SiteId: siteMain, MemberIds: []int64{f.m1.Id}})
	expectRefusal(t, res, http.StatusConflict, "work_center_code_taken", "a taken code")
	branch := createWorkCenter(t, c, workCenterInput("FAR", siteBranch))
	res = c.Do(http.MethodPost, groupsPath, gen.ManufacturingWorkCenterGroupInput{Code: "H", Name: "Far", SiteId: siteMain, MemberIds: []int64{f.m1.Id, branch.Id}})
	expectRefusal(t, res, http.StatusBadRequest, "work_center_other_site", "a member at another site")
	expectRefusal(t, c.Do(http.MethodPost, groupsPath, gen.ManufacturingWorkCenterGroupInput{Code: "H", Name: "None", SiteId: siteMain, MemberIds: []int64{}}),
		http.StatusBadRequest, "", "no member")

	b := bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	if op := b.Operations[0]; op.WorkCenterId != nil || op.WorkCenterGroupId == nil || op.WorkCenterCode != "G" || b.SiteId == nil || *b.SiteId != siteMain {
		t.Fatalf("operation %+v", op)
	}
	_, wos := confirmedBike(t, c, "10")
	if wos[0].WorkCenter.Id != f.m1.Id || wos[0].WorkCenterGroup == nil || wos[0].WorkCenterGroup.Code != "G" {
		t.Errorf("work order at %s, group %+v", wos[0].WorkCenter.Code, wos[0].WorkCenterGroup)
	}
	expectRefusal(t, c.Do(http.MethodPost, groupPath(f.group.Id, "archive"), revisionInput(f.group.Revision)), http.StatusConflict,
		"work_center_in_use", "archiving a group a BoM targets")
	update := gen.ManufacturingWorkCenterUpdate{Code: "M1", Name: "M1", SiteId: siteBranch, Revision: f.m1.Revision}
	expectRefusal(t, c.Do(http.MethodPut, workCenterPath(f.m1.Id), update), http.StatusBadRequest, "work_center_other_site", "a member moved away")

	res = c.Do(http.MethodPut, groupPath(f.group.Id), gen.ManufacturingWorkCenterGroupUpdate{Code: "G", Name: "Saws", SiteId: siteMain,
		MemberIds: []int64{f.m1.Id}, Revision: f.group.Revision})
	mustStatus(t, res, http.StatusOK, "update the group")
	var g gen.ManufacturingWorkCenterGroup
	res.JSON(&g)
	if g.Name != "Saws" || len(g.Members) != 1 {
		t.Errorf("updated %+v", g)
	}
	res = c.Do(http.MethodGet, groupsPath, nil)
	mustStatus(t, res, http.StatusOK, "list groups")
	var list gen.ManufacturingWorkCenterGroupList
	res.JSON(&list)
	if len(list.Items) != 1 {
		t.Errorf("groups %+v", list.Items)
	}

	// A group no BoM targets archives and reactivates.
	other := createGroup(t, c, "SPARE", f.paint.Id)
	res = c.Do(http.MethodPost, groupPath(other.Id, "archive"), revisionInput(other.Revision))
	mustStatus(t, res, http.StatusOK, "archive")
	res.JSON(&other)
	mustStatus(t, c.Do(http.MethodPost, groupPath(other.Id, "reactivate"), revisionInput(other.Revision)), http.StatusOK, "reactivate")
	mustStatus(t, c.Do(http.MethodGet, groupPath(other.Id), nil), http.StatusOK, "get")
	if res := h.SignIn(t, reader...).Do(http.MethodPost, groupsPath, gen.ManufacturingWorkCenterGroupInput{Code: "X", Name: "X", SiteId: siteMain,
		MemberIds: []int64{f.m1.Id}}); res.Status != http.StatusForbidden {
		t.Errorf("a reader created a group: %d", res.Status)
	}
}

// TestRouting_MemberChoice is M119's choice: the member with the earliest
// finish, ties by the higher efficiency, then the code — its figures and
// expected minutes taken again with it.
func TestRouting_MemberChoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 200)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))

	// 60 minutes at M1, 30 at M2 (twice as efficient): M2 finishes first.
	o, wos := confirmedBike(t, c, "60")
	if wos[0].WorkCenter.Id != f.m1.Id || wos[0].ExpectedMinutes != 60 {
		t.Fatalf("snapshot at %s, %d minutes", wos[0].WorkCenter.Code, wos[0].ExpectedMinutes)
	}
	planOrder(t, c, o)
	wos = workOrdersOf(t, c, o.Id)
	if wos[0].WorkCenter.Id != f.m2.Id || wos[0].ExpectedMinutes != 30 || wos[0].TimeEfficiency != 200 ||
		!sameInstant(wos[0].PlannedFinish, osloAt(9, 14, 7, 30)) {
		t.Errorf("planned at %s, %d minutes, efficiency %d, until %v", wos[0].WorkCenter.Code, wos[0].ExpectedMinutes, wos[0].TimeEfficiency, wos[0].PlannedFinish)
	}
	// A second order finishes at M1 at 08:00, and at M2 after the first at
	// 08:00 too: the tie goes to M2's efficiency. A third then finishes
	// first at M1, at 08:00, M2 being booked until then.
	o2, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o2)
	o3, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o3)
	w2, w3 := workOrdersOf(t, c, o2.Id)[0], workOrdersOf(t, c, o3.Id)[0]
	if w2.WorkCenter.Id != f.m2.Id || !sameInstant(w2.PlannedFinish, osloAt(9, 14, 8, 0)) {
		t.Errorf("second at %s until %v", w2.WorkCenter.Code, w2.PlannedFinish)
	}
	if w3.WorkCenter.Id != f.m1.Id || !sameInstant(w3.PlannedFinish, osloAt(9, 14, 8, 0)) {
		t.Errorf("third at %s until %v", w3.WorkCenter.Code, w3.PlannedFinish)
	}
}

// TestRouting_MemberTieByCode is M119's last tie: equal finish and
// efficiency go to the code.
func TestRouting_MemberTieByCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	o, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o)
	if w := workOrdersOf(t, c, o.Id)[0]; w.WorkCenter.Id != f.m1.Id {
		t.Errorf("planned at %s, want M1", w.WorkCenter.Code)
	}
	o2, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o2)
	if w := workOrdersOf(t, c, o2.Id)[0]; w.WorkCenter.Id != f.m2.Id {
		t.Errorf("second planned at %s, want M2 beside the first", w.WorkCenter.Code)
	}
}

// TestRouting_GroupWorkAndTheFixedMember is M119 on the floor and the
// board: an unplanned group work order shows under every member's Group
// work; started at a member it moves there; then a start at another member
// is work_order_member_fixed and a drag work_order_started; a center outside
// the group is work_center_not_in_group.
func TestRouting_GroupWorkAndTheFixedMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	other := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	o, wos := confirmedBike(t, c, "60")
	w := wos[0]
	for _, center := range []int64{f.m1.Id, f.m2.Id} {
		q := queueOf(t, c, center)
		if q.GroupWork == nil || len(*q.GroupWork) != 1 || len(q.Items) != 0 {
			t.Fatalf("queue of %d: items %d, group work %v", center, len(q.Items), q.GroupWork)
		}
	}
	at := func(center int64) gen.ManufacturingStartInput {
		return gen.ManufacturingStartInput{WorkCenterId: &center}
	}
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(w.Id, "start"), at(f.paint.Id)), http.StatusBadRequest, "work_center_not_in_group", "a start outside the group")
	got := act(t, c, w.Id, "start", at(f.m2.Id))
	if got.WorkOrder.WorkOrder.WorkCenter.Id != f.m2.Id || got.WorkOrder.WorkOrder.MemberFixed == nil || !*got.WorkOrder.WorkOrder.MemberFixed {
		t.Fatalf("started at %s, fixed %v", got.WorkOrder.WorkOrder.WorkCenter.Code, got.WorkOrder.WorkOrder.MemberFixed)
	}
	if q := queueOf(t, c, f.m1.Id); len(*q.GroupWork)+len(q.Items) != 0 {
		t.Errorf("M1 still shows it")
	}
	if q := queueOf(t, c, f.m2.Id); len(q.Items) != 1 || len(*q.GroupWork) != 0 {
		t.Errorf("M2's queue %d items, %d group work", len(q.Items), len(*q.GroupWork))
	}
	p := expectRefusal(t, other.Do(http.MethodPost, workOrderPath(w.Id, "start"), at(f.m1.Id)), http.StatusConflict, "work_order_member_fixed", "a stale group work list")
	if p.Detail == nil || !strings.Contains(*p.Detail, "M2") {
		t.Errorf("the refusal names %v", p.Detail)
	}
	planOrder(t, c, getOrder(t, c, o.Id))
	w = workOrdersOf(t, c, o.Id)[0]
	if w.WorkCenter.Id != f.m2.Id {
		t.Errorf("a started work order planned at %s", w.WorkCenter.Code)
	}
	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	for _, row := range s.WorkCenters {
		for _, bar := range row.Bars {
			if bar.MemberIds == nil || len(*bar.MemberIds) != 0 {
				t.Errorf("a fixed bar may go to %v", bar.MemberIds)
			}
		}
	}
	m1 := f.m1.Id
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(w.Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: osloAt(9, 15, 7, 0), Revision: w.Revision, WorkCenterId: &m1}),
		http.StatusConflict, "work_order_started", "a drag to another member after a log")
}

// TestRouting_ArchivedGroupKeepsItsMember is M117 on archiving: the work
// orders of an archived group keep their member — on its own queue, planned
// there and started nowhere else — and Group work lists only active members'
// groups.
func TestRouting_ArchivedGroupKeepsItsMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 200)
	b := bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	o, wos := confirmedBike(t, c, "60")
	w := wos[0]

	// M2 archived: its Group work no longer lists the work order.
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(f.m2.Id, "archive"), revisionInput(f.m2.Revision)), http.StatusOK, "archive M2")
	if q := queueOf(t, c, f.m2.Id); q.GroupWork != nil && len(*q.GroupWork) != 0 {
		t.Errorf("an archived member's group work %v", *q.GroupWork)
	}
	m2 := getWorkCenter(t, c, f.m2.Id)
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(f.m2.Id, "reactivate"), revisionInput(m2.Revision)), http.StatusOK, "reactivate M2")

	b = getBom(t, c, b.Id)
	mustStatus(t, c.Do(http.MethodPost, bomPath(b.Id, "archive"), revisionInput(b.Revision)), http.StatusOK, "archive the BoM")
	mustStatus(t, c.Do(http.MethodPost, groupPath(f.group.Id, "archive"), revisionInput(f.group.Revision)), http.StatusOK, "archive the group")
	if q := queueOf(t, c, f.m1.Id); len(q.Items) != 1 || (q.GroupWork != nil && len(*q.GroupWork) != 0) {
		t.Fatalf("M1's queue %d items, group work %v", len(q.Items), q.GroupWork)
	}
	if q := queueOf(t, c, f.m2.Id); len(q.Items) != 0 || (q.GroupWork != nil && len(*q.GroupWork) != 0) {
		t.Errorf("M2's queue %d items, group work %v", len(q.Items), q.GroupWork)
	}
	m2Id := f.m2.Id
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(w.Id, "start"), gen.ManufacturingStartInput{WorkCenterId: &m2Id}),
		http.StatusBadRequest, "work_center_not_in_group", "a start at another member of an archived group")
	// M2 is twice as efficient, but the member is kept.
	planOrder(t, c, o)
	if got := workOrdersOf(t, c, o.Id)[0]; got.WorkCenter.Id != f.m1.Id || got.ExpectedMinutes != 60 {
		t.Errorf("planned at %s, %d minutes", got.WorkCenter.Code, got.ExpectedMinutes)
	}
}

// TestRouting_GroupWithNoActiveMember is M118 at confirm: an operation whose
// group has no active work center is work_center_group_empty.
func TestRouting_GroupWithNoActiveMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	for _, m := range []gen.ManufacturingWorkCenter{f.m1, f.m2} {
		mustStatus(t, c.Do(http.MethodPost, workCenterPath(m.Id, "archive"), revisionInput(m.Revision)), http.StatusOK, "archive "+m.Code)
	}
	draft := createOrder(t, c, orderInput(varBike, "1"))
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "confirm"), revisionInput(draft.Revision)), http.StatusConflict,
		"work_center_group_empty", "a confirm with no member to run at")
	if p.Detail == nil || !strings.Contains(*p.Detail, "G") {
		t.Errorf("the refusal names %v", p.Detail)
	}
}

// TestRouting_BlockedMemberIsNoDropTarget is M119 on the board with a
// blocked member: its row is not offered, and a drag to it is
// work_center_blocked.
func TestRouting_BlockedMemberIsNoDropTarget(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 200)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	o, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o)
	w := workOrdersOf(t, c, o.Id)[0]
	if w.WorkCenter.Id != f.m2.Id {
		t.Fatalf("planned at %s", w.WorkCenter.Code)
	}
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(f.m1.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown}), http.StatusOK, "block M1")
	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	for _, row := range s.WorkCenters {
		for _, bar := range row.Bars {
			if bar.MemberIds == nil || len(*bar.MemberIds) != 0 {
				t.Errorf("the bar may go to %v", bar.MemberIds)
			}
		}
	}
	m1 := f.m1.Id
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(w.Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: osloAt(9, 14, 9, 0), Revision: w.Revision, WorkCenterId: &m1}),
		http.StatusConflict, "work_center_blocked", "a drag to a blocked member")
}

// TestRouting_DragToAnotherMember is M119 on the board: a planned group
// work order dropped on another member's row moves there with its figures;
// a row outside the group is refused; its successor on another center moves
// after it.
func TestRouting_DragToAnotherMember(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 200)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"), operation("b", 20, f.paint.Id, "1"))
	o, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o)
	wos := workOrdersOf(t, c, o.Id)
	if wos[0].WorkCenter.Id != f.m2.Id {
		t.Fatalf("planned at %s", wos[0].WorkCenter.Code)
	}
	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	for _, row := range s.WorkCenters {
		if row.WorkCenter.Id == f.m2.Id && (len(row.Bars) != 1 || row.Bars[0].MemberIds == nil || !slices.Equal(*row.Bars[0].MemberIds, []int64{f.m1.Id})) {
			t.Errorf("M2's bars %+v", row.Bars)
		}
	}
	paint := f.paint.Id
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[0].Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: osloAt(9, 14, 9, 0), Revision: wos[0].Revision, WorkCenterId: &paint}),
		http.StatusBadRequest, "work_center_not_in_group", "a drag outside the group")
	m1 := f.m1.Id
	res := c.Do(http.MethodPost, workOrderPath(wos[0].Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: osloAt(9, 14, 9, 0), Revision: wos[0].Revision, WorkCenterId: &m1})
	mustStatus(t, res, http.StatusOK, "drag to M1")
	var list gen.ManufacturingWorkOrderList
	res.JSON(&list)
	a, b := list.Items[0], list.Items[1]
	if a.WorkCenter.Id != f.m1.Id || a.ExpectedMinutes != 60 || !sameInstant(a.PlannedStart, osloAt(9, 14, 9, 0)) || !sameInstant(a.PlannedFinish, osloAt(9, 14, 10, 0)) {
		t.Errorf("a at %s, %d minutes, %v–%v", a.WorkCenter.Code, a.ExpectedMinutes, a.PlannedStart, a.PlannedFinish)
	}
	if !sameInstant(b.PlannedStart, osloAt(9, 14, 10, 0)) {
		t.Errorf("b from %v, want after a", b.PlannedStart)
	}
}

// TestRouting_DragTakesTheSuccessorsLock is M115's pre-lock set: a drag
// whose successor is on another center waits for that center's schedule
// lock before it writes anything.
func TestRouting_DragTakesTheSuccessorsLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	paint := createWorkCenter(t, c, workCenterInput("PAINT", siteMain))
	bikeWith(t, c, operation("a", 10, saw.Id, "1"), operation("b", 20, paint.Id, "1"))
	o, _ := confirmedBike(t, c, "60")
	planOrder(t, c, o)
	wos := workOrdersOf(t, c, o.Id)

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(19783, $1::int)", paint.Id); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- c.Do(http.MethodPost, workOrderPath(wos[0].Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: osloAt(9, 15, 9, 0), Revision: wos[0].Revision}).Status
	}()
	select {
	case st := <-done:
		t.Fatalf("the drag answered %d while PAINT's lock was held", st)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if st := <-done; st != http.StatusOK {
		t.Fatalf("the drag answered %d", st)
	}
	if b := workOrdersOf(t, c, o.Id)[1]; !sameInstant(b.PlannedStart, osloAt(9, 15, 10, 0)) {
		t.Errorf("b from %v", b.PlannedStart)
	}
}

// TestRouting_ConcurrentPlansAndDragsOnAGroup is the locks under load, on a
// pool of four: plans of group work orders and drags between members at once
// never book one center's time twice.
func TestRouting_ConcurrentPlansAndDragsOnAGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"), operation("b", 20, f.paint.Id, "1"))
	var orders []gen.ManufacturingOrder
	for range 6 {
		o, _ := confirmedBike(t, c, "30")
		orders = append(orders, o)
	}
	// The first three are planned now and dragged while the others plan.
	for _, o := range orders[:3] {
		planOrder(t, c, o)
	}
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	for i, o := range orders {
		wg.Go(func() {
			if i >= 3 {
				statuses[i] = c.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(getOrder(t, c, o.Id).Revision)).Status
				return
			}
			w := workOrdersOf(t, c, o.Id)[0]
			to := f.m1.Id
			if w.WorkCenter.Id == f.m1.Id {
				to = f.m2.Id
			}
			statuses[i] = c.Do(http.MethodPost, workOrderPath(w.Id, "reschedule"),
				gen.ManufacturingRescheduleInput{Start: osloAt(9, 14, 8, 0), Revision: w.Revision, WorkCenterId: &to}).Status
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("write %d answered %d", i, st)
		}
	}
	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14&days=14", siteMain))
	for _, row := range s.WorkCenters {
		for i, a := range row.Bars {
			for _, b := range row.Bars[i+1:] {
				if a.StartsAt.Before(b.EndsAt) && b.StartsAt.Before(a.EndsAt) {
					t.Errorf("%s: %s %v–%v overlaps %s %v–%v", row.WorkCenter.Code, a.OrderNumber, a.StartsAt, a.EndsAt, b.OrderNumber, b.StartsAt, b.EndsAt)
				}
			}
		}
	}
	// Every work order's bars stand on its own center's row.
	for _, o := range orders {
		for _, w := range workOrdersOf(t, c, o.Id) {
			found := false
			for _, row := range s.WorkCenters {
				for _, bar := range row.Bars {
					if bar.WorkOrderId == w.Id {
						found = true
						if row.WorkCenter.Id != w.WorkCenter.Id {
							t.Errorf("work order %d at %s has a bar at %s", w.Id, w.WorkCenter.Code, row.WorkCenter.Code)
						}
					}
				}
			}
			if !found {
				t.Errorf("work order %d has no bar", w.Id)
			}
		}
	}
}

// TestRouting_ProductionGateByTheGraph is M98 after F: a production is gated
// on the checks of the work orders the output work order is reachable from —
// a parallel branch included, since every branch joins the last (M112).
func TestRouting_ProductionGateByTheGraph(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, quality...)
	mix := createWorkCenter(t, c, workCenterInput("MIX", siteMain))
	fill := createWorkCenter(t, c, workCenterInput("FILL", siteMain))
	label := createWorkCenter(t, c, workCenterInput("LABEL", siteMain))
	in := bomInput(varJam, "1", line(varBerry, "2"), line(varSugar, "1"))
	a, side, b := operation("a", 10, mix.Id, "1"), after(operation("s", 15, label.Id, "1")), operation("b", 20, fill.Id, "1")
	a.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Instruction, Title: "Recipe", Frequency: gen.EachWorkOrder}}
	side.Checks = &[]gen.ManufacturingCheckTemplateInput{{Kind: gen.Instruction, Title: "Labels", Frequency: gen.EachWorkOrder}}
	b = after(b, "a", "s")
	in.Operations = []gen.ManufacturingBomOperationInput{a, side, b}
	createBom(t, c, in)
	h.stock.receiveLot(varBerry, "B-1", locMainA1, "100", h.Now().Add(-time.Hour), day(2030, 1, 1))
	h.stock.put(varSugar, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "4"))).Order
	wos := workOrdersOf(t, c, o.Id)
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("g1", "1")), http.StatusConflict,
		"checks_incomplete", "a production before the recipe")
	if p.Checks == nil || len(*p.Checks) != 2 {
		t.Fatalf("missing %+v", p.Checks)
	}
	recordCheck(t, c, wos[0].Id, checksOf(wos[0])[0].Id, gen.ManufacturingCheckResultInput{})
	// The labels are a branch that joins the output: asked too.
	p = expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("g2", "1")), http.StatusConflict,
		"checks_incomplete", "a production before the labels")
	if p.Checks == nil || len(*p.Checks) != 1 || (*p.Checks)[0].WorkOrderId != wos[1].Id {
		t.Fatalf("missing %+v", p.Checks)
	}
	recordCheck(t, c, wos[1].Id, checksOf(wos[1])[0].Id, gen.ManufacturingCheckResultInput{})
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), jamProduction("g3", "1")), http.StatusCreated, "a production with every check")
}

// TestRouting_EditKeepsTheGraph is M112 on an edit: the keys of kept
// operations are their ids, and an edit sending dependsOn by them replaces
// the graph.
func TestRouting_EditKeepsTheGraph(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	b := bikeWith(t, c, operation("a", 10, saw.Id, "1"), operation("b", 20, saw.Id, "1"), operation("c", 30, saw.Id, "1"))
	u := updateOf(b)
	u.Operations[1] = after(u.Operations[1])
	u.Operations[2] = after(u.Operations[2], strconv.FormatInt(b.Operations[0].Id, 10), strconv.FormatInt(b.Operations[1].Id, 10))
	res := c.Do(http.MethodPut, bomPath(b.Id), u)
	mustStatus(t, res, http.StatusOK, "make b parallel")
	got := bomOf(t, res)
	if len(got.Operations[1].DependsOn) != 0 || len(got.Operations[2].DependsOn) != 2 {
		t.Fatalf("b depends on %v, c on %v", got.Operations[1].DependsOn, got.Operations[2].DependsOn)
	}
	u = updateOf(got)
	u.Operations[0] = after(u.Operations[0], strconv.FormatInt(got.Operations[1].Id, 10))
	u.Operations[1].DependsOn = nil // absent: the one before by sequence
	expectRefusal(t, c.Do(http.MethodPut, bomPath(b.Id), u), http.StatusBadRequest, "operation_order", "a after b")
}

// TestRouting_GroupWorkLive is §17 for groups: a group work order started at
// one member is heard on every member's work center topic, since it leaves
// each one's Group work.
func TestRouting_GroupWorkLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newGroupFloor(t, c, 100)
	bikeWith(t, c, atGroup("a", 10, f.group.Id, "1"))
	_, wos := confirmedBike(t, c, "10")
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatal(err)
	}
	events := make(chan liveEvent, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	m2 := f.m2.Id
	act(t, c, wos[0].Id, "start", gen.ManufacturingStartInput{WorkCenterId: &m2})
	want := map[liveEvent]bool{}
	for _, id := range []int64{f.m1.Id, f.m2.Id} {
		center := strconv.FormatInt(id, 10)
		want[liveEvent{T: "manufacturing.work-center:" + center, K: "work-center.changed", ID: center}] = true
	}
	deadline := time.After(10 * time.Second)
	for len(want) > 0 {
		select {
		case e := <-events:
			delete(want, e)
		case <-deadline:
			t.Fatalf("live events missing: %v", want)
		}
	}
}
