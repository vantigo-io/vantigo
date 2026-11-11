package manufacturing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func workOrderPath(id int64, rest ...string) string { return pathOf(base+"/work-orders", id, rest...) }

// floor is the shop floor's fixture: a bicycle of two wheels made in two
// operations, a at SAW (5 minutes a cycle, its wheels consumed there) and b
// at PAINT (2.5 minutes a cycle), with stock at the main site.
type floor struct {
	saw, paint gen.ManufacturingWorkCenter
	bom        gen.ManufacturingBom
}

func newFloor(t *testing.T, h *harness, c *modtest.Client) floor {
	t.Helper()
	f := floor{saw: createWorkCenter(t, c, workCenterInput("SAW", siteMain)), paint: createWorkCenter(t, c, workCenterInput("PAINT", siteMain))}
	in := bomInput(varBike, "1", line(varWheel, "2"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, f.saw.Id, "5"), operation("b", 20, f.paint.Id, "2.5")}
	in.Operations[0].Instructions = str("Cut the **frame**.")
	in.Lines[0].OperationKey = str("a")
	f.bom = createBom(t, c, in)
	h.stock.put(varWheel, locMainA1, "1000")
	return f
}

// openOrder is a confirmed order of qty bicycles and its two work orders.
func openOrder(t *testing.T, c *modtest.Client, qty string) (gen.ManufacturingOrder, []gen.ManufacturingWorkOrder) {
	t.Helper()
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, qty))).Order
	return o, workOrdersOf(t, c, o.Id)
}

func workOrdersOf(t *testing.T, c *modtest.Client, orderID int64) []gen.ManufacturingWorkOrder {
	t.Helper()
	var list gen.ManufacturingWorkOrderList
	res := c.Do(http.MethodGet, orderPath(orderID, "work-orders"), nil)
	mustStatus(t, res, http.StatusOK, "work orders")
	res.JSON(&list)
	return list.Items
}

func viewOf(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingWorkOrderView {
	t.Helper()
	var v gen.ManufacturingWorkOrderView
	res := c.Do(http.MethodGet, workOrderPath(id), nil)
	mustStatus(t, res, http.StatusOK, "work order view")
	res.JSON(&v)
	return v
}

// act posts a shop-floor action and answers its result.
func act(t *testing.T, c *modtest.Client, id int64, action string, body any) gen.ManufacturingShopFloorResult {
	t.Helper()
	res := c.Do(http.MethodPost, workOrderPath(id, action), body)
	mustStatus(t, res, http.StatusOK, action)
	var out gen.ManufacturingShopFloorResult
	res.JSON(&out)
	return out
}

func logsOfOrder(t *testing.T, c *modtest.Client, orderID int64) []gen.ManufacturingTimeLog {
	t.Helper()
	var list gen.ManufacturingTimeLogList
	res := c.Do(http.MethodGet, orderPath(orderID, "time-logs"), nil)
	mustStatus(t, res, http.StatusOK, "time logs")
	res.JSON(&list)
	return list.Items
}

func queueOf(t *testing.T, c *modtest.Client, center int64) gen.ManufacturingWorkCenterQueue {
	t.Helper()
	var q gen.ManufacturingWorkCenterQueue
	res := c.Do(http.MethodGet, workCenterPath(center, "queue"), nil)
	mustStatus(t, res, http.StatusOK, "queue")
	res.JSON(&q)
	return q
}

func openLogs(logs []gen.ManufacturingTimeLog) (out []gen.ManufacturingTimeLog) {
	for _, l := range logs {
		if l.EndedAt == nil {
			out = append(out, l)
		}
	}
	return out
}

var force = gen.ManufacturingStartInput{Force: &[]bool{true}[0]}

// TestWorkOrder_SnapshotAtConfirm is M27, M27a and M28a: one work order per
// operation, the first ready and the rest pending, with the work center's
// figures and the expected minutes fixed at confirm; the queue shows only
// what can be worked.
func TestWorkOrder_SnapshotAtConfirm(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	in := workCenterInput("SAW", siteMain)
	in.Capacity = i32(4)
	in.TimeEfficiency = i32(80)
	in.SetupMinutes = str("10")
	in.CleanupMinutes = str("5")
	saw := createWorkCenter(t, c, in)
	bom := bomInput(varBike, "2", line(varWheel, "2"))
	bom.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "3"), operation("b", 20, saw.Id, "1")}
	bom.Operations[1].SetupMinutes = str("0")
	createBom(t, c, bom)
	draft := createOrder(t, c, orderInput(varBike, "18"))
	if wos := workOrdersOf(t, c, draft.Id); len(wos) != 0 {
		t.Fatalf("a draft has work orders: %+v", wos)
	}
	o := confirmOrder(t, c, draft).Order
	wos := workOrdersOf(t, c, o.Id)
	// cycles = ⌈(18 / 2) / 4⌉ = 3; a: 10 + 5 + 3 × 3 × 100 / 80 = 26.25 → 26;
	// b: 0 + 5 + 3 × 1 × 100 / 80 = 8.75 → 9.
	if len(wos) != 2 || wos[0].Status != gen.ManufacturingWorkOrderStatusReady || wos[1].Status != gen.ManufacturingWorkOrderStatusPending ||
		wos[0].ExpectedMinutes != 26 || wos[1].ExpectedMinutes != 9 || wos[0].Capacity != 4 || wos[0].TimeEfficiency != 80 ||
		wos[0].SetupMinutes != "10" || wos[1].SetupMinutes != "0" || wos[0].CleanupMinutes != "5" || wos[0].Last || !wos[1].Last ||
		wos[0].Order.Number != o.Number || wos[0].Order.Remaining != "18" || wos[0].WorkCenter.Code != "SAW" {
		t.Fatalf("work orders = %+v", wos)
	}
	// A later edit of the work center reaches no snapshot.
	upd := gen.ManufacturingWorkCenterUpdate{Code: saw.Code, Name: saw.Name, SiteId: saw.SiteId, Capacity: i32(1), Revision: saw.Revision}
	mustStatus(t, c.Do(http.MethodPut, workCenterPath(saw.Id), upd), http.StatusOK, "edit the work center")
	if again := workOrdersOf(t, c, o.Id); again[0].Capacity != 4 || again[0].ExpectedMinutes != 26 {
		t.Errorf("the snapshot moved: %+v", again[0])
	}
	if q := queueOf(t, c, saw.Id); len(q.Items) != 1 || q.Items[0].Id != wos[0].Id || q.Block != nil {
		t.Errorf("queue = %+v", q)
	}
	// The order list's work-center filter reads the work orders.
	var page gen.ManufacturingOrderPage
	c.Do(http.MethodGet, fmt.Sprintf("%s?workCenterId=%d", ordersPath, saw.Id), nil).JSON(&page)
	if page.Total != 1 {
		t.Errorf("by work center = %+v", page)
	}
	// Archiving the work center is refused while it has open work; deleting
	// it while anything names it.
	saw = getWorkCenter(t, c, saw.Id)
	expectRefusal(t, c.Do(http.MethodPost, workCenterPath(saw.Id, "archive"), revisionInput(saw.Revision)), http.StatusConflict,
		"work_center_has_open_work", "archive with open work")
	mustStatus(t, c.Do(http.MethodGet, orderPath(999999, "work-orders"), nil), http.StatusNotFound, "work orders of no order")
}

func getWorkCenter(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingWorkCenter {
	t.Helper()
	var w gen.ManufacturingWorkCenter
	res := c.Do(http.MethodGet, workCenterPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get work center")
	res.JSON(&w)
	return w
}

// TestWorkOrder_StateMachine is M27 and M29a: start, the no-op restart,
// pause, the pending refusal and force, finish readying the next, finish on
// the last work order posting the production in the same transaction, and
// the refusals on a done work order.
func TestWorkOrder_StateMachine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "2")
	a, b := wos[0], wos[1]
	op := h.SignIn(t, operator...)

	expectRefusal(t, op.Do(http.MethodPost, workOrderPath(b.Id, "start"), nil), http.StatusConflict, "work_order_pending", "start pending")
	started := act(t, op, a.Id, "start", nil)
	if started.WorkOrder.WorkOrder.Status != gen.ManufacturingWorkOrderStatusInProgress || started.WorkOrder.MyOpenLog == nil ||
		len(started.WorkOrder.OpenLogs) != 1 || started.ClosedLog != nil || started.WorkOrder.WorkOrder.StartedAt == nil ||
		len(started.WorkOrder.WorkOrder.Operators) != 1 {
		t.Fatalf("started = %+v", started)
	}
	if got := getOrder(t, c, o.Id); got.Status != gen.ManufacturingOrderStatusInProgress {
		t.Errorf("the first start left the order %s", got.Status)
	}
	again := act(t, op, a.Id, "start", nil)
	if again.WorkOrder.MyOpenLog.Id != started.WorkOrder.MyOpenLog.Id || len(logsOfOrder(t, c, o.Id)) != 1 {
		t.Errorf("a restart was not a no-op: %+v", again)
	}
	h.Advance(30 * time.Minute)
	paused := act(t, op, a.Id, "pause", nil)
	if paused.WorkOrder.WorkOrder.Status != gen.ManufacturingWorkOrderStatusPaused || paused.WorkOrder.MyOpenLog != nil ||
		paused.WorkOrder.WorkOrder.LoggedMinutes != 30 {
		t.Fatalf("paused = %+v", paused.WorkOrder.WorkOrder)
	}
	act(t, op, a.Id, "pause", nil) // nothing of the caller's open: a no-op

	act(t, op, a.Id, "start", nil)
	h.Advance(10 * time.Minute)
	quantity := "2"
	done := act(t, op, a.Id, "finish", gen.ManufacturingFinishInput{Quantity: &quantity})
	if done.WorkOrder.WorkOrder.Status != gen.ManufacturingWorkOrderStatusDone || done.WorkOrder.WorkOrder.FinishedAt == nil || done.Production != nil {
		t.Fatalf("finished = %+v", done.WorkOrder.WorkOrder)
	}
	if next := viewOf(t, c, b.Id); next.WorkOrder.Status != gen.ManufacturingWorkOrderStatusReady {
		t.Errorf("the next work order is %s", next.WorkOrder.Status)
	}
	logs := logsOfOrder(t, c, o.Id)
	if len(logs) != 2 || logs[1].Quantity == nil || *logs[1].Quantity != "2" || len(openLogs(logs)) != 0 {
		t.Errorf("logs = %+v", logs)
	}
	expectRefusal(t, op.Do(http.MethodPost, workOrderPath(a.Id, "start"), nil), http.StatusConflict, "work_order_closed", "start done")
	expectRefusal(t, op.Do(http.MethodPost, workOrderPath(a.Id, "finish"), nil), http.StatusConflict, "work_order_closed", "finish done")

	// The last work order's finish posts the production with it.
	act(t, op, b.Id, "start", nil)
	view := viewOf(t, op, a.Id)
	wheel := component(t, getOrder(t, c, o.Id), varWheel)
	if len(view.Components) != 1 || *view.Components[0].Id != *wheel.Id || view.Instructions != "Cut the **frame**." {
		t.Errorf("the view of a = %+v", view)
	}
	production := gen.ManufacturingProductionInput{ClientRequestId: "finish-b", Quantity: "2",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("4")}}}
	bad := production
	bad.Quantity = "1"
	expectRefusal(t, op.Do(http.MethodPost, workOrderPath(b.Id, "finish"), gen.ManufacturingFinishInput{Production: &bad}),
		http.StatusConflict, "consumption_differs", "a deviating production")
	if v := viewOf(t, c, b.Id); v.WorkOrder.Status != gen.ManufacturingWorkOrderStatusInProgress {
		t.Fatalf("a refused finish changed the work order: %s", v.WorkOrder.Status)
	}
	finished := act(t, op, b.Id, "finish", gen.ManufacturingFinishInput{Production: &production})
	if finished.Production == nil || finished.Production.Quantity != "2" || finished.WorkOrder.WorkOrder.Status != gen.ManufacturingWorkOrderStatusDone {
		t.Fatalf("finish with production = %+v", finished)
	}
	if got := getOrder(t, c, o.Id); got.Produced != "2" || component(t, got, varWheel).Consumed != "4" {
		t.Errorf("after the finish the order = %+v", got)
	}
	replay := act(t, op, b.Id, "finish", gen.ManufacturingFinishInput{Production: &production})
	if replay.Production == nil || replay.Production.Id != finished.Production.Id {
		t.Errorf("the finish's replay = %+v", replay.Production)
	}
	unknown := gen.ManufacturingFinishInput{Production: &production}
	res := op.Do(http.MethodPost, workOrderPath(b.Id, "finish"), map[string]any{"production": unknown.Production, "colour": "red"})
	expectRefusal(t, res, http.StatusBadRequest, "", "an unknown property")
}

// TestWorkOrder_ForcedStart is M27's force: a pending work order started
// out of sequence, its log marked forced.
func TestWorkOrder_ForcedStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	out := act(t, c, wos[1].Id, "start", force)
	if out.WorkOrder.WorkOrder.Status != gen.ManufacturingWorkOrderStatusInProgress || !out.WorkOrder.MyOpenLog.Forced {
		t.Fatalf("forced start = %+v", out.WorkOrder)
	}
	if logs := logsOfOrder(t, c, o.Id); len(logs) != 1 || !logs[0].Forced {
		t.Errorf("logs = %+v", logs)
	}
}

// TestTimeLog_OneOpenLogPerUserAcrossCenters is §12 Time: one caller has
// one open log in the whole workspace; starting elsewhere — at another work
// center, on another order — ends the open log first, and my work shows
// both.
func TestTimeLog_OneOpenLogPerUserAcrossCenters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	first, firstWOs := openOrder(t, c, "1")
	second, secondWOs := openOrder(t, c, "1")
	op := h.SignIn(t, operator...)

	act(t, op, firstWOs[0].Id, "start", nil)
	h.Advance(5 * time.Minute)
	moved := act(t, op, secondWOs[1].Id, "start", force)
	if moved.ClosedLog == nil || moved.ClosedLog.EndedAt == nil || *moved.ClosedLog.WorkOrderId != firstWOs[0].Id {
		t.Fatalf("the start elsewhere did not end the open log: %+v", moved.ClosedLog)
	}
	if v := viewOf(t, c, firstWOs[0].Id); v.WorkOrder.Status != gen.ManufacturingWorkOrderStatusPaused || len(v.OpenLogs) != 0 {
		t.Errorf("the work order left = %+v", v.WorkOrder)
	}
	if n := len(openLogs(logsOfOrder(t, c, first.Id))) + len(openLogs(logsOfOrder(t, c, second.Id))); n != 1 {
		t.Errorf("%d open logs, want 1", n)
	}
	var mine gen.ManufacturingMyWork
	res := op.Do(http.MethodGet, base+"/shop-floor/mine", nil)
	mustStatus(t, res, http.StatusOK, "my work")
	res.JSON(&mine)
	if mine.OpenLog == nil || mine.OpenWorkOrder == nil || mine.OpenWorkOrder.Id != secondWOs[1].Id || len(mine.Today) != 2 ||
		mine.Today[0].Id != secondWOs[1].Id || mine.Today[1].Id != firstWOs[0].Id {
		t.Errorf("my work = %+v", mine)
	}
	var none gen.ManufacturingMyWork
	h.SignIn(t, reader...).Do(http.MethodGet, base+"/shop-floor/mine", nil).JSON(&none)
	if none.OpenLog != nil || len(none.Today) != 0 {
		t.Errorf("a reader's work = %+v", none)
	}

	// Open for more than twelve hours, the log is flagged and still open.
	h.Exec(t, `UPDATE manufacturing.time_logs SET started_at = started_at - interval '13 hours' WHERE id = $1`, moved.WorkOrder.MyOpenLog.Id)
	if v := viewOf(t, op, secondWOs[1].Id); v.MyOpenLog == nil || !v.MyOpenLog.LongRunning || v.MyOpenLog.EndedAt != nil {
		t.Errorf("after 13 hours = %+v", v.MyOpenLog)
	}
}

// TestWorkCenter_BlockConvertsLogs is M30 and M29a: a block ends the
// productive logs and opens a downtime log per operator and the center's
// own; starts are refused; pause during the block ends the caller's
// downtime; unblock ends the rest.
func TestWorkCenter_BlockConvertsLogs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	f := newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	a := wos[0]
	one, two := h.SignIn(t, operator...), h.SignIn(t, operator...)
	act(t, one, a.Id, "start", nil)
	act(t, two, a.Id, "start", nil)

	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown}),
		http.StatusForbidden, "block as a reader")
	expectRefusal(t, one.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), map[string]any{"reason": "lunch"}),
		http.StatusBadRequest, "", "an unknown reason")
	h.Advance(time.Minute)
	res := one.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown, Note: str("Belt snapped")})
	mustStatus(t, res, http.StatusOK, "block")
	var q gen.ManufacturingWorkCenterQueue
	res.JSON(&q)
	if q.WorkCenter.WorkingState != gen.ManufacturingWorkingStateBlocked || q.Block == nil || q.Block.Reason != gen.Breakdown || q.Block.Note != "Belt snapped" {
		t.Fatalf("blocked queue = %+v", q)
	}
	logs := logsOfOrder(t, c, o.Id)
	open := openLogs(logs)
	if len(logs) != 4 || len(open) != 2 || open[0].Kind != gen.Downtime || *open[0].Reason != gen.Breakdown || open[1].Kind != gen.Downtime {
		t.Fatalf("logs after the block = %+v", logs)
	}
	if v := viewOf(t, c, a.Id); v.WorkOrder.Status != gen.ManufacturingWorkOrderStatusPaused || v.Block == nil {
		t.Errorf("work order under a block = %+v", v.WorkOrder)
	}
	if h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE work_center_id = $1 AND user_id IS NULL AND ended_at IS NULL`, f.saw.Id) != 1 {
		t.Error("no open center-level downtime log")
	}
	var stats gen.ManufacturingStats
	c.Do(http.MethodGet, base+"/stats", nil).JSON(&stats)
	if stats.BlockedWorkCenters != 1 {
		t.Errorf("stats = %+v", stats)
	}
	act(t, one, a.Id, "pause", nil)
	expectRefusal(t, one.Do(http.MethodPost, workOrderPath(a.Id, "start"), nil), http.StatusConflict, "work_center_blocked", "start while blocked")
	mustStatus(t, two.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Other}), http.StatusOK, "block again")
	if n := len(openLogs(logsOfOrder(t, c, o.Id))); n != 1 {
		t.Errorf("after one operator paused, %d open logs on the order, want 1", n)
	}

	res = two.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil)
	mustStatus(t, res, http.StatusOK, "unblock")
	res.JSON(&q)
	if q.WorkCenter.WorkingState != gen.ManufacturingWorkingStateNormal || q.Block != nil {
		t.Errorf("unblocked queue = %+v", q)
	}
	if h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE work_center_id = $1 AND ended_at IS NULL`, f.saw.Id) != 0 {
		t.Error("open logs remain after the unblock")
	}
	mustStatus(t, two.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil), http.StatusOK, "unblock again")
	act(t, one, a.Id, "start", nil)
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(999999, "block"), gen.ManufacturingBlockInput{Reason: gen.Setup}), http.StatusNotFound, "block no center")
}

// TestWorkOrder_Permissions is M33: the shop-floor actions need
// manufacturing:shop-floor or manufacturing:orders-manage; reads need
// access; corrections need orders-manage.
func TestWorkOrder_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	f := newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	r := h.SignIn(t, reader...)
	for _, action := range []string{"start", "pause", "finish"} {
		expectRefusal(t, r.Do(http.MethodPost, workOrderPath(wos[0].Id, action), nil), http.StatusForbidden, "shop_floor_required", action+" as a reader")
	}
	scrap := gen.ManufacturingScrapInput{ClientRequestId: "s", Lines: []gen.ManufacturingScrapLineInput{}}
	expectRefusal(t, r.Do(http.MethodPost, workOrderPath(wos[0].Id, "scrap"), scrap), http.StatusForbidden, "shop_floor_required", "scrap as a reader")
	expectRefusal(t, r.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil), http.StatusForbidden, "shop_floor_required", "unblock as a reader")
	viewOf(t, r, wos[0].Id)
	queueOf(t, r, f.saw.Id)
	logsOfOrder(t, r, o.Id)
	mustStatus(t, r.Do(http.MethodGet, workOrderPath(wos[0].Id, "time-logs"), nil), http.StatusOK, "work order logs as a reader")
	mustStatus(t, r.Do(http.MethodGet, workOrderPath(999999), nil), http.StatusNotFound, "no work order")
	mustStatus(t, r.Do(http.MethodGet, workOrderPath(999999, "time-logs"), nil), http.StatusNotFound, "logs of no work order")
	act(t, h.SignIn(t, operator...), wos[0].Id, "start", nil)
	in := gen.ManufacturingTimeLogInput{UserId: uuid.New(), StartedAt: h.Now().Add(-2 * time.Hour), EndedAt: h.Now().Add(-time.Hour), Note: "Forgot"}
	mustStatus(t, h.SignIn(t, operator...).Do(http.MethodPost, workOrderPath(wos[0].Id, "time-logs"), in), http.StatusForbidden, "add a log as an operator")
}

// TestTimeLog_Corrections is M29's corrections and §12's correction
// limits: a closed log corrected and kept in the timeline, an open one
// closed by its correction, a missing one added; overlaps, more than 60
// days back, the future and a closed order refused.
func TestTimeLog_Corrections(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	op, opID := h.SignInUser(t, operator...)
	act(t, op, wos[0].Id, "start", nil)
	h.Advance(2 * time.Hour)
	act(t, op, wos[0].Id, "pause", nil)
	h.Advance(time.Hour)
	act(t, op, wos[0].Id, "start", nil) // left running
	logs := logsOfOrder(t, c, o.Id)
	closed, running := logs[0], logs[1]
	now := h.Now()

	fix := gen.ManufacturingTimeLogCorrection{StartedAt: closed.StartedAt, EndedAt: closed.StartedAt.Add(90 * time.Minute), Note: "Left at 1.5 h"}
	res := c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), fix)
	mustStatus(t, res, http.StatusOK, "correct")
	var got gen.ManufacturingTimeLog
	res.JSON(&got)
	if !got.EndedAt.Equal(fix.EndedAt) || got.CorrectedBy == nil || got.CorrectedAt == nil || got.Note != "Left at 1.5 h" {
		t.Errorf("corrected = %+v", got)
	}
	var events gen.ManufacturingOrderEventList
	c.Do(http.MethodGet, orderPath(o.Id, "events"), nil).JSON(&events)
	if last := events.Items[len(events.Items)-1]; last.Kind != gen.ManufacturingOrderEventKindTimeLogCorrected {
		t.Errorf("last event = %+v", last)
	}

	future := gen.ManufacturingTimeLogCorrection{StartedAt: closed.StartedAt, EndedAt: now.Add(time.Hour), Note: "Future"}
	expectRefusal(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), future), http.StatusBadRequest, "", "an end in the future")
	noNote := fix
	noNote.Note = " "
	expectRefusal(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), noNote), http.StatusBadRequest, "", "no note")
	mustStatus(t, op.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), fix), http.StatusForbidden, "correct as an operator")
	mustStatus(t, c.Do(http.MethodPatch, base+"/time-logs/999999", fix), http.StatusNotFound, "correct no log")

	// Correcting the running log closes it; the work order is paused.
	h.Advance(time.Hour)
	stop := gen.ManufacturingTimeLogCorrection{StartedAt: running.StartedAt, EndedAt: running.StartedAt.Add(30 * time.Minute), Note: "Forgot to pause"}
	mustStatus(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", running.Id), stop), http.StatusOK, "correct the running log")
	if v := viewOf(t, c, wos[0].Id); v.WorkOrder.Status != gen.ManufacturingWorkOrderStatusPaused || len(v.OpenLogs) != 0 {
		t.Errorf("after closing the running log = %+v", v.WorkOrder)
	}

	add := gen.ManufacturingTimeLogInput{UserId: opID, StartedAt: now.Add(-10 * time.Hour), EndedAt: now.Add(-9 * time.Hour), Note: "Night shift"}
	res = c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), add)
	mustStatus(t, res, http.StatusCreated, "add a log")
	res.JSON(&got)
	if got.User == nil || got.User.Id != opID || got.Kind != gen.Productive || got.EndedAt == nil {
		t.Errorf("added = %+v", got)
	}
	downtime := add
	downtime.Kind = &[]gen.ManufacturingTimeLogKind{gen.Downtime}[0]
	downtime.StartedAt, downtime.EndedAt = now.Add(-20*time.Hour), now.Add(-19*time.Hour)
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), downtime), http.StatusBadRequest, "", "downtime without a reason")
	downtime.Reason = &[]gen.ManufacturingDowntimeReason{gen.Quality}[0]
	mustStatus(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), downtime), http.StatusCreated, "add downtime")
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), add), http.StatusBadRequest, "log_overlaps", "add an overlap")
	overlap := gen.ManufacturingTimeLogCorrection{StartedAt: now.Add(-9*time.Hour - 30*time.Minute), EndedAt: closed.StartedAt.Add(time.Hour), Note: "Too long"}
	expectRefusal(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), overlap), http.StatusBadRequest, "log_overlaps", "a correction overlapping")
	old := add
	old.StartedAt, old.EndedAt = now.Add(-61*24*time.Hour), now.Add(-61*24*time.Hour+time.Hour)
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), old), http.StatusConflict, "log_locked", "add 61 days back")
	stranger := add
	stranger.UserId = uuid.New()
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "time-logs"), stranger), http.StatusBadRequest, "", "add for no user")

	// After the close, nothing is corrected.
	o = getOrder(t, c, o.Id)
	no := false
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision, Backorder: &no}), http.StatusOK, "close")
	expectRefusal(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", closed.Id), fix), http.StatusConflict, "order_closed", "correct after close")
}

// TestTimeLog_LockedAfterSixtyDays is log_locked on a correction of a log
// whose own start is more than 60 days back.
func TestTimeLog_LockedAfterSixtyDays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	act(t, c, wos[0].Id, "start", nil)
	act(t, c, wos[0].Id, "pause", nil)
	l := logsOfOrder(t, c, o.Id)[0]
	h.Exec(t, `UPDATE manufacturing.time_logs SET started_at = started_at - interval '61 days', ended_at = ended_at - interval '61 days' + interval '1 hour' WHERE id = $1`, l.Id)
	fix := gen.ManufacturingTimeLogCorrection{StartedAt: h.Now().Add(-2 * time.Hour), EndedAt: h.Now().Add(-time.Hour), Note: "Move it"}
	expectRefusal(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", l.Id), fix), http.StatusConflict, "log_locked", "correct an old log")
}

// TestOrder_CancelAndCloseEndLogs is M23 and M29a: cancel and close end
// every open log of the order and cancel its work orders not done; a
// backorder gets work orders of its own.
func TestOrder_CancelAndCloseEndLogs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	act(t, h.SignIn(t, operator...), wos[0].Id, "start", nil)
	o = getOrder(t, c, o.Id)
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), revisionInput(o.Revision)), http.StatusOK, "cancel")
	if len(openLogs(logsOfOrder(t, c, o.Id))) != 0 {
		t.Error("cancel left a log open")
	}
	for _, w := range workOrdersOf(t, c, o.Id) {
		if w.Status != gen.ManufacturingWorkOrderStatusCancelled {
			t.Errorf("after cancel a work order is %s", w.Status)
		}
	}

	o, wos = openOrder(t, c, "4")
	act(t, c, wos[0].Id, "finish", nil)
	act(t, c, wos[1].Id, "start", nil)
	produce := gen.ManufacturingProductionInput{ClientRequestId: "p", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), produce), http.StatusCreated, "produce")
	o = getOrder(t, c, o.Id)
	yes := true
	res := c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision, Backorder: &yes})
	mustStatus(t, res, http.StatusOK, "close")
	var out gen.ManufacturingCloseResult
	res.JSON(&out)
	after := workOrdersOf(t, c, o.Id)
	if after[0].Status != gen.ManufacturingWorkOrderStatusDone || after[1].Status != gen.ManufacturingWorkOrderStatusCancelled ||
		len(openLogs(logsOfOrder(t, c, o.Id))) != 0 {
		t.Errorf("after close = %+v", after)
	}
	back := workOrdersOf(t, c, out.Backorder.Id)
	if len(back) != 2 || back[0].Status != gen.ManufacturingWorkOrderStatusReady || back[0].Order.Quantity != "3" {
		t.Errorf("the backorder's work orders = %+v", back)
	}
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[1].Id, "start"), nil), http.StatusConflict, "order_not_open", "start on a closed order")
}

// TestWorkOrder_ReportScrap is M31's Report scrap: a production with only
// scrap, idempotent by its clientRequestId.
func TestWorkOrder_ReportScrap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "2")
	wheel := component(t, o, varWheel)
	op := h.SignIn(t, operator...)
	in := gen.ManufacturingScrapInput{ClientRequestId: "scrap-1", Lines: []gen.ManufacturingScrapLineInput{{ComponentId: *wheel.Id, Scrap: "1"}}}
	res := op.Do(http.MethodPost, workOrderPath(wos[0].Id, "scrap"), in)
	mustStatus(t, res, http.StatusCreated, "scrap")
	var p gen.ManufacturingProduction
	res.JSON(&p)
	if p.Quantity != "0" || len(p.Lines) != 1 || p.Lines[0].Scrapped != "1" {
		t.Errorf("scrap = %+v", p)
	}
	mustStatus(t, op.Do(http.MethodPost, workOrderPath(wos[0].Id, "scrap"), in), http.StatusOK, "scrap replay")
	if got := component(t, getOrder(t, c, o.Id), varWheel); got.Scrapped != "1" {
		t.Errorf("scrapped = %s", got.Scrapped)
	}
	zero := gen.ManufacturingScrapInput{ClientRequestId: "scrap-2", Lines: []gen.ManufacturingScrapLineInput{{ComponentId: *wheel.Id, Scrap: "0"}}}
	expectRefusal(t, op.Do(http.MethodPost, workOrderPath(wos[0].Id, "scrap"), zero), http.StatusBadRequest, "quantity_invalid", "scrap 0")
}

// TestTimeLog_StartAcrossOrdersOnPoolOfFour is M26a's cross-order start:
// two operators, each running a work order of their own order, start each
// other's at once, many times over, on a pool of four connections. Each
// start locks both orders in id order, so none deadlocks, and each operator
// ends with one open log.
func TestTimeLog_StartAcrossOrdersOnPoolOfFour(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	first, firstWOs := openOrder(t, c, "1")
	second, secondWOs := openOrder(t, c, "1")
	one, two := h.SignIn(t, operator...), h.SignIn(t, operator...)
	x, y := firstWOs[0].Id, secondWOs[0].Id
	act(t, one, x, "start", nil)
	act(t, two, y, "start", nil)
	for round := range 10 {
		var wg sync.WaitGroup
		statuses := make([]int, 2)
		to := []int64{y, x}
		if round%2 == 1 {
			to = []int64{x, y}
		}
		for i, cl := range []*modtest.Client{one, two} {
			wg.Go(func() { statuses[i] = cl.Do(http.MethodPost, workOrderPath(to[i], "start"), nil).Status })
		}
		wg.Wait()
		if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
			t.Fatalf("round %d answered %v", round, statuses)
		}
	}
	if n := len(openLogs(logsOfOrder(t, c, first.Id))) + len(openLogs(logsOfOrder(t, c, second.Id))); n != 2 {
		t.Errorf("%d open logs, want one per operator", n)
	}
}

// TestWorkOrder_LiveNotifies is M32: a start is heard on the work center's
// topic, the work order's, the order's and the list's.
func TestWorkOrder_LiveNotifies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	f := newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
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
	act(t, c, wos[0].Id, "start", nil)
	center, wo, order := strconv.FormatInt(f.saw.Id, 10), strconv.FormatInt(wos[0].Id, 10), strconv.FormatInt(o.Id, 10)
	want := map[liveEvent]bool{
		{T: "manufacturing.work-center:" + center, K: "work-center.changed", ID: center}: true,
		{T: "manufacturing.work-order:" + wo, K: "work-order.changed", ID: wo}:           true,
		{T: "manufacturing.order:" + order, K: "order.changed", ID: order}:               true,
		{T: "manufacturing.orders", K: "order.changed", ID: order}:                       true,
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

// TestWorkCenter_BlockBesideStartsOnPoolOfFour is M26a's block beside
// starts: operators move between two orders' work orders at one work center
// while another blocks and unblocks it, on a pool of four. Every request
// answers its rule — a start at a blocked center work_center_blocked, a
// block that saw the logs change revision_conflict — and none waits on
// another in a circle.
func TestWorkCenter_BlockBesideStartsOnPoolOfFour(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, planner...)
	f := newFloor(t, h, c)
	_, firstWOs := openOrder(t, c, "1")
	_, secondWOs := openOrder(t, c, "1")
	one, two, lead := h.SignIn(t, operator...), h.SignIn(t, operator...), h.SignIn(t, operator...)
	allowed := map[int]bool{http.StatusOK: true, http.StatusConflict: true}
	for round := range 8 {
		var wg sync.WaitGroup
		statuses := make([]int, 3)
		wg.Go(func() { statuses[0] = one.Do(http.MethodPost, workOrderPath(firstWOs[0].Id, "start"), nil).Status })
		wg.Go(func() { statuses[1] = two.Do(http.MethodPost, workOrderPath(secondWOs[0].Id, "start"), nil).Status })
		wg.Go(func() {
			if round%2 == 0 {
				statuses[2] = lead.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown}).Status
			} else {
				statuses[2] = lead.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil).Status
			}
		})
		wg.Wait()
		for i, s := range statuses {
			if !allowed[s] {
				t.Fatalf("round %d request %d answered %d", round, i, s)
			}
		}
	}
	if h.Count(t, `SELECT count(*) FROM (SELECT user_id FROM manufacturing.time_logs WHERE ended_at IS NULL AND user_id IS NOT NULL GROUP BY user_id HAVING count(*) > 1) x`) != 0 {
		t.Error("a user has more than one open log")
	}
}
