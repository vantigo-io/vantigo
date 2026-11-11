package manufacturing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/live"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The harness starts on Saturday 12 September 2026, 14:00 in Oslo; the
// default pattern is Monday to Friday 07:00–15:00, so planning starts on
// Monday 14 September at 07:00.
var oslo = func() *time.Location {
	z, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		panic(err)
	}
	return z
}()

func osloAt(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, oslo) }

func planOrder(t *testing.T, c *modtest.Client, o gen.ManufacturingOrder) gen.ManufacturingOrder {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(o.Revision))
	mustStatus(t, res, http.StatusOK, "plan "+o.Number)
	var out gen.ManufacturingOrder
	res.JSON(&out)
	return out
}

func scheduleOf(t *testing.T, c *modtest.Client, query string) gen.ManufacturingSchedule {
	t.Helper()
	res := c.Do(http.MethodGet, base+"/schedule?"+query, nil)
	mustStatus(t, res, http.StatusOK, "schedule")
	var s gen.ManufacturingSchedule
	res.JSON(&s)
	return s
}

func sameInstant(a *time.Time, b time.Time) bool { return a != nil && a.Equal(b) }

// TestSchedule_PlanUnplanAndTheBoard is M34–M36: plan places the work
// orders forward from now, in sequence, on their centers' calendars; the
// board shows the bars and the load; a due date before the finish flags the
// order late; unplan clears it all.
func TestSchedule_PlanUnplanAndTheBoard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	f := newFloor(t, h, c)
	o, _ := openOrder(t, c, "60") // SAW 60 × 5 = 300 minutes, PAINT 60 × 2.5 = 150
	in := orderUpdateOf(o)
	due := openapi_types.Date{Time: time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)}
	in.DueDate = &due
	res := c.Do(http.MethodPut, orderPath(o.Id), in)
	mustStatus(t, res, http.StatusOK, "set the due date")
	res.JSON(&o)

	o = planOrder(t, c, o)
	wos := workOrdersOf(t, c, o.Id)
	if !sameInstant(wos[0].PlannedStart, osloAt(9, 14, 7, 0)) || !sameInstant(wos[0].PlannedFinish, osloAt(9, 14, 12, 0)) ||
		!sameInstant(wos[1].PlannedStart, osloAt(9, 14, 12, 0)) || !sameInstant(wos[1].PlannedFinish, osloAt(9, 14, 14, 30)) {
		t.Fatalf("work orders planned %v–%v and %v–%v", wos[0].PlannedStart, wos[0].PlannedFinish, wos[1].PlannedStart, wos[1].PlannedFinish)
	}
	if !sameInstant(o.PlannedStart, osloAt(9, 14, 7, 0)) || !sameInstant(o.PlannedFinish, osloAt(9, 14, 14, 30)) {
		t.Errorf("window %v–%v", o.PlannedStart, o.PlannedFinish)
	}
	if o.LateByDays == nil || *o.LateByDays != 3 {
		t.Errorf("late by %v, want 3 days", o.LateByDays)
	}

	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	if len(s.WorkCenters) != 2 || s.Days != 7 || len(s.Unscheduled) != 0 {
		t.Fatalf("schedule %+v", s)
	}
	saw := s.WorkCenters[1]
	if saw.WorkCenter.Id != f.saw.Id {
		saw = s.WorkCenters[0]
	}
	monday := saw.Days[0]
	if len(saw.Bars) != 1 || !saw.Bars[0].Late || saw.Bars[0].OrderNumber != o.Number || monday.CapacityMinutes != 480 ||
		monday.PlannedMinutes != 300 || monday.LoadPercent == nil || *monday.LoadPercent != "62.5" || monday.Overloaded {
		t.Errorf("SAW row %+v, Monday %+v", saw.Bars, monday)
	}
	if saturday := saw.Days[5]; saturday.CapacityMinutes != 0 || saturday.LoadPercent != nil {
		t.Errorf("Saturday %+v", saturday)
	}
	expectRefusal(t, c.Do(http.MethodGet, fmt.Sprintf("%s/schedule?siteId=%d&days=9", base, siteMain), nil), http.StatusBadRequest, "", "nine days")

	res = c.Do(http.MethodPost, orderPath(o.Id, "unplan"), revisionInput(o.Revision))
	mustStatus(t, res, http.StatusOK, "unplan")
	res.JSON(&o)
	if o.PlannedStart != nil || o.LateByDays != nil {
		t.Errorf("unplanned order %+v", o)
	}
	if s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain)); len(s.Unscheduled) != 1 || len(s.WorkCenters[0].Bars)+len(s.WorkCenters[1].Bars) != 0 {
		t.Errorf("after unplan %+v", s)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(o.Revision-1)), http.StatusConflict, "revision_conflict", "a stale plan")
	reader := h.SignIn(t, reader...)
	if res := reader.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(o.Revision)); res.Status != http.StatusForbidden {
		t.Errorf("a reader planned: %d", res.Status)
	}
}

// orderUpdateOf is the update that keeps o as it is.
func orderUpdateOf(o gen.ManufacturingOrder) gen.ManufacturingOrderUpdate {
	return gen.ManufacturingOrderUpdate{Revision: o.Revision, BomId: o.BomId, Quantity: o.Quantity, SiteId: o.SiteId, Priority: o.Priority,
		DueDate: o.DueDate, Note: &o.Note, OutputLocationId: &o.OutputLocationId, ComponentLocationId: o.ComponentLocationId,
		RequestedStart: o.RequestedStart}
}

// TestSchedule_RequestedStartAndInProgress is M35a: a requested start set by
// hand is where planning starts; a running work order plans only its
// remaining minutes, from now.
func TestSchedule_RequestedStartAndInProgress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "60")
	in := orderUpdateOf(o)
	wed := openapi_types.Date{Time: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)}
	in.RequestedStart = &wed
	res := c.Do(http.MethodPut, orderPath(o.Id), in)
	mustStatus(t, res, http.StatusOK, "set the requested start")
	res.JSON(&o)
	if o.RequestedStart == nil || !o.RequestedStart.Equal(wed.Time) {
		t.Fatalf("requested start %v", o.RequestedStart)
	}
	o = planOrder(t, c, o)
	if !sameInstant(o.PlannedStart, osloAt(9, 16, 7, 0)) {
		t.Errorf("planned from %v, want Wednesday 07:00", o.PlannedStart)
	}

	// An operator has run the saw for an hour since; plan again. The running
	// work order plans its 240 remaining minutes from now — the first open
	// time, Monday 07:00 — and the next still waits for Wednesday.
	op := h.SignIn(t, operator...)
	act(t, op, wos[0].Id, "start", nil)
	h.Advance(time.Hour)
	o = planOrder(t, c, getOrder(t, c, o.Id))
	got := workOrdersOf(t, c, o.Id)
	if !sameInstant(got[0].PlannedStart, osloAt(9, 14, 7, 0)) || !sameInstant(got[0].PlannedFinish, osloAt(9, 14, 11, 0)) ||
		!sameInstant(got[1].PlannedStart, osloAt(9, 16, 7, 0)) {
		t.Errorf("planned %v–%v, then %v", got[0].PlannedStart, got[0].PlannedFinish, got[1].PlannedStart)
	}
}

// TestSchedule_RequestedStartOnCreate is M35a's requested start given on
// the create: it is stored and planning starts from it.
func TestSchedule_RequestedStartOnCreate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	in := orderInput(varBike, "60")
	wed := openapi_types.Date{Time: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)}
	in.RequestedStart = &wed
	o := createOrder(t, c, in)
	if o.RequestedStart == nil || !o.RequestedStart.Equal(wed.Time) {
		t.Fatalf("requested start %v", o.RequestedStart)
	}
	o = planOrder(t, c, confirmOrder(t, c, o).Order)
	if !sameInstant(o.PlannedStart, osloAt(9, 16, 7, 0)) {
		t.Errorf("planned from %v, want Wednesday 07:00", o.PlannedStart)
	}
}

// TestSchedule_ConcurrentPlansDoNotDoubleBook is M35a's locks: plans of
// several orders on one work center at once, on a pool of four, never book
// the same time twice.
func TestSchedule_ConcurrentPlansDoNotDoubleBook(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	var orders []gen.ManufacturingOrder
	for range 4 {
		o, _ := openOrder(t, c, "30")
		orders = append(orders, o)
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(orders))
	for i, o := range orders {
		wg.Go(func() { statuses[i] = c.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(o.Revision)).Status })
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("plan %d answered %d", i, st)
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
}

// TestSchedule_PlanAllByPriority is M35: plan all takes the urgent order
// first, then by due date.
func TestSchedule_PlanAllByPriority(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	normal, _ := openOrder(t, c, "60")
	urgent, _ := openOrder(t, c, "60")
	in := orderUpdateOf(urgent)
	in.Priority = gen.ManufacturingPriorityUrgent
	mustStatus(t, c.Do(http.MethodPut, orderPath(urgent.Id), in), http.StatusOK, "make it urgent")

	res := c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	mustStatus(t, res, http.StatusOK, "plan all")
	var out gen.ManufacturingPlanAllResult
	res.JSON(&out)
	if len(out.Planned) != 2 || out.Planned[0].Id != urgent.Id || len(out.NoCapacity) != 0 {
		t.Fatalf("plan all %+v", out)
	}
	u, n := getOrder(t, c, urgent.Id), getOrder(t, c, normal.Id)
	if !sameInstant(u.PlannedStart, osloAt(9, 14, 7, 0)) || n.PlannedStart == nil || !n.PlannedStart.After(*u.PlannedStart) {
		t.Errorf("urgent from %v, normal from %v", u.PlannedStart, n.PlannedStart)
	}
	// Nothing is left to plan.
	res = c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	res.JSON(&out)
	if len(out.Planned) != 0 {
		t.Errorf("planned again %+v", out)
	}
}

// TestSchedule_RescheduleMovesSuccessors is M36: a work order dropped later
// takes the first free time from there, and the work order after it moves
// after it.
func TestSchedule_RescheduleMovesSuccessors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, _ := openOrder(t, c, "60")
	planOrder(t, c, o)
	wos := workOrdersOf(t, c, o.Id)
	start := osloAt(9, 15, 10, 0)
	res := c.Do(http.MethodPost, workOrderPath(wos[0].Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: start, Revision: wos[0].Revision})
	mustStatus(t, res, http.StatusOK, "reschedule")
	var list gen.ManufacturingWorkOrderList
	res.JSON(&list)
	got := list.Items
	if !sameInstant(got[0].PlannedStart, start) || !sameInstant(got[0].PlannedFinish, osloAt(9, 15, 15, 0)) ||
		!sameInstant(got[1].PlannedStart, osloAt(9, 16, 7, 0)) {
		t.Errorf("moved to %v–%v, the next to %v", got[0].PlannedStart, got[0].PlannedFinish, got[1].PlannedStart)
	}
	expectRefusal(t, c.Do(http.MethodPost, workOrderPath(wos[0].Id, "reschedule"), gen.ManufacturingRescheduleInput{Start: start, Revision: wos[0].Revision}),
		http.StatusConflict, "revision_conflict", "a stale reschedule")
}

// TestSchedule_NoCapacity is M35's horizon: a work center open no day is
// no_capacity, and nothing is planned.
func TestSchedule_NoCapacity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	in := workCenterInput("IDLE", siteMain)
	in.Pattern = &gen.ManufacturingWeekPattern{Monday: []gen.ManufacturingInterval{}, Tuesday: []gen.ManufacturingInterval{},
		Wednesday: []gen.ManufacturingInterval{}, Thursday: []gen.ManufacturingInterval{}, Friday: []gen.ManufacturingInterval{},
		Saturday: []gen.ManufacturingInterval{}, Sunday: []gen.ManufacturingInterval{}}
	idle := createWorkCenter(t, c, in)
	bom := bomInput(varBike, "1", line(varWheel, "2"))
	bom.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, idle.Id, "5")}
	createBom(t, c, bom)
	h.stock.put(varWheel, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "plan"), revisionInput(o.Revision)), http.StatusConflict, "no_capacity", "plan on a closed center")
	if got := getOrder(t, c, o.Id); got.PlannedStart != nil {
		t.Errorf("planned %v", got.PlannedStart)
	}
	res := c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	var out gen.ManufacturingPlanAllResult
	res.JSON(&out)
	if len(out.NoCapacity) != 1 || len(out.Planned) != 0 {
		t.Errorf("plan all %+v", out)
	}
}

// TestCost_EstimateActualAndShaping is M38–M41: the estimate fixed at
// confirm from the snapshots, the actual fixed at close with the cost per
// unit and the deviation, every cost left out without costs-view, and the
// production cost report needing costs-view.
func TestCost_EstimateActualAndShaping(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	in := workCenterInput("SAW", siteMain)
	in.MachineCostPerHour, in.LabourCostPerHour = json.RawMessage(`"600"`), json.RawMessage(`"300"`)
	saw := createWorkCenter(t, c, in)
	bom := bomInput(varBike, "1", line(varWheel, "2"))
	bom.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "30")}
	createBom(t, c, bom)
	h.stock.put(varWheel, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	// components 4 × 150.50 = 602; one hour at 600 and at 300.
	if o.Cost == nil || o.Cost.Estimate == nil || o.Cost.Estimate.Components != "602.00" || o.Cost.Estimate.Machine != "600.00" ||
		o.Cost.Estimate.Labour != "300.00" || o.Cost.Estimate.Total != "1502.00" || o.Cost.ActualFixed {
		t.Fatalf("cost %+v", o.Cost)
	}
	if res := h.SignIn(t, planner...).Do(http.MethodGet, orderPath(o.Id), nil); bytes.Contains(res.Body, []byte(`"cost"`)) ||
		bytes.Contains(res.Body, []byte(`standardCost`)) {
		t.Errorf("a caller without costs-view saw costs: %s", res.Body)
	}

	wos := workOrdersOf(t, c, o.Id)
	act(t, c, wos[0].Id, "start", nil)
	h.Advance(90 * time.Minute)
	finish := gen.ManufacturingFinishInput{}
	act(t, c, wos[0].Id, "finish", finish)
	post := gen.ManufacturingProductionInput{ClientRequestId: "p-1", Quantity: "2",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: str("4")}}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "post the production")
	o = getOrder(t, c, o.Id)
	res := c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision})
	mustStatus(t, res, http.StatusOK, "close")
	o = getOrder(t, c, o.Id)
	// 1.5 hours: machine 900, labour 450; components as consumed, 602.
	if !o.Cost.ActualFixed || o.Cost.Actual.Machine != "900.00" || o.Cost.Actual.Labour != "450.00" || o.Cost.Actual.Total != "1952.00" ||
		o.Cost.CostPerUnit == nil || *o.Cost.CostPerUnit != "976.00" || o.Cost.Deviation.Total.Amount != "450.00" ||
		*o.Cost.Deviation.Total.Percent != "30.0" {
		t.Errorf("fixed cost %+v, actual %+v, deviation %+v", o.Cost, o.Cost.Actual, o.Cost.Deviation)
	}
	if wo := workOrdersOf(t, c, o.Id)[0]; wo.ActualMinutes != 90 || wo.DeviationMinutes != 30 || wo.DeviationPercent == nil || *wo.DeviationPercent != "50.0" {
		t.Errorf("duration %d (%d, %v)", wo.ActualMinutes, wo.DeviationMinutes, wo.DeviationPercent)
	}

	res = c.Do(http.MethodGet, base+"/reports/production-cost", nil)
	mustStatus(t, res, http.StatusOK, "production cost")
	var report gen.ManufacturingProductionCostReport
	res.JSON(&report)
	if len(report.Groups) != 1 || report.Groups[0].Totals.Actual != "1952.00" || report.Groups[0].Orders[0].Number != o.Number {
		t.Errorf("report %+v", report)
	}
	res = c.Do(http.MethodGet, base+"/reports/production-cost?format=csv", nil)
	mustStatus(t, res, http.StatusOK, "production cost csv")
	if !strings.HasPrefix(res.Header("Content-Type"), "text/csv") || !bytes.Contains(res.Body, []byte("1952,00")) {
		t.Errorf("csv %s: %s", res.Header("Content-Type"), res.Body)
	}
	if res := h.SignIn(t, planner...).Do(http.MethodGet, base+"/reports/production-cost", nil); res.Status != http.StatusForbidden {
		t.Errorf("production cost without costs-view answered %d", res.Status)
	}
	var util gen.ManufacturingUtilisationReport
	res = c.Do(http.MethodGet, base+"/reports/utilisation", nil)
	mustStatus(t, res, http.StatusOK, "utilisation")
	res.JSON(&util)
	var productive int64
	for _, r := range util.Rows {
		productive += r.ProductiveMinutes
	}
	if productive != 90 {
		t.Errorf("utilisation %+v", util)
	}
	var dev gen.ManufacturingTimeDeviationReport
	res = c.Do(http.MethodGet, base+"/reports/time-deviation", nil)
	mustStatus(t, res, http.StatusOK, "time deviation")
	res.JSON(&dev)
	if len(dev.Rows) != 1 || dev.Rows[0].AverageActualMinutes != "90.0" || dev.Rows[0].AverageExpectedMinutes != "60.0" {
		t.Errorf("time deviation %+v", dev)
	}
	expectRefusal(t, c.Do(http.MethodGet, base+"/reports/utilisation?from=2026-09-12&to=2026-09-01", nil), http.StatusBadRequest, "", "a period backwards")
	expectRefusal(t, c.Do(http.MethodGet, base+"/reports/utilisation?from=2025-01-01&to=2026-09-01", nil), http.StatusBadRequest, "period_too_long", "a period over 366 days")
}

// TestSchedule_LiveNotifiesTheSite is M36's live board: a plan is heard on
// the site's schedule topic.
func TestSchedule_LiveNotifiesTheSite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	o, _ := openOrder(t, c, "1")
	heard := listenLive(t, h)
	planOrder(t, c, o)
	waitHeard(t, heard, scheduleEvent(siteMain), "plan")
}

// scheduleEvent is the board's notice of the site.
func scheduleEvent(site int64) liveEvent {
	key := strconv.FormatInt(site, 10)
	return liveEvent{T: "manufacturing.schedule:" + key, K: "schedule.changed", ID: key}
}

// listenLive is every live event the harness's database sends from now on.
func listenLive(t *testing.T, h *harness) <-chan liveEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatal(err)
	}
	heard := make(chan liveEvent, 64)
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
				select {
				case heard <- e:
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	return heard
}

// waitHeard waits for want on heard, the events before it passed over.
func waitHeard(t *testing.T, heard <-chan liveEvent, want liveEvent, after string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-heard:
			if e == want {
				return
			}
		case <-deadline:
			t.Fatalf("after %s, not heard: %v", after, want)
		}
	}
}

// TestSchedule_CalendarWritesNotifyTheBoard is the review's L2: the board
// draws blocked centers, closures and the pattern's days, so blocking,
// unblocking, a closure added or removed and a work center's edit each tell
// the site's board.
func TestSchedule_CalendarWritesNotifyTheBoard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newFloor(t, h, c)
	heard := listenLive(t, h)
	want := scheduleEvent(siteMain)

	res := c.Do(http.MethodPost, workCenterPath(f.saw.Id, "closures"), gen.ManufacturingClosureInput{FromDate: openapi_types.Date{Time: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}, ToDate: openapi_types.Date{Time: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}})
	mustStatus(t, res, http.StatusCreated, "add a closure")
	var closure gen.ManufacturingClosure
	res.JSON(&closure)
	waitHeard(t, heard, want, "a closure added")

	mustStatus(t, c.Do(http.MethodDelete, workCenterPath(f.saw.Id, "closures", strconv.FormatInt(closure.Id, 10)), nil), http.StatusNoContent, "remove the closure")
	waitHeard(t, heard, want, "a closure removed")
	mustStatus(t, c.Do(http.MethodDelete, workCenterPath(f.saw.Id, "closures", strconv.FormatInt(closure.Id, 10)), nil), http.StatusNotFound, "remove it again")

	mustStatus(t, c.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown}), http.StatusOK, "block")
	waitHeard(t, heard, want, "a block")
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil), http.StatusOK, "unblock")
	waitHeard(t, heard, want, "an unblock")

	saw := getWorkCenter(t, c, f.saw.Id)
	pattern := weekPattern("08:00", "16:00")
	res = c.Do(http.MethodPut, workCenterPath(saw.Id), gen.ManufacturingWorkCenterUpdate{Code: saw.Code, Name: saw.Name, SiteId: saw.SiteId,
		Pattern: &pattern, Revision: saw.Revision})
	mustStatus(t, res, http.StatusOK, "change the pattern")
	waitHeard(t, heard, want, "a pattern edit")
}

// weekPattern is Monday to Friday from start to end.
func weekPattern(start, end string) gen.ManufacturingWeekPattern {
	day := []gen.ManufacturingInterval{{Start: start, End: end}}
	return gen.ManufacturingWeekPattern{Monday: day, Tuesday: day, Wednesday: day, Thursday: day, Friday: day,
		Saturday: []gen.ManufacturingInterval{}, Sunday: []gen.ManufacturingInterval{}}
}

// TestSchedule_DoneWorkOrderEndsItsBars is the review's M1: a work order
// finished early holds no time after it finished — its later slots leave
// the board and the utilisation report's planned time, and the slot it
// finished in ends there.
func TestSchedule_DoneWorkOrderEndsItsBars(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	f := newFloor(t, h, c)
	o, wos := openOrder(t, c, "120") // SAW 600 minutes: Monday 07:00–15:00 and Tuesday 07:00–09:00
	planOrder(t, c, o)
	h.Advance(42 * time.Hour) // Monday 08:00
	c = h.SignIn(t, everything...)
	act(t, c, wos[0].Id, "start", nil)
	h.Advance(time.Hour)
	act(t, c, wos[0].Id, "finish", nil)

	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	var saw gen.ManufacturingScheduleRow
	for _, row := range s.WorkCenters {
		if row.WorkCenter.Id == f.saw.Id {
			saw = row
		}
	}
	var bars []gen.ManufacturingScheduleBar
	for _, b := range saw.Bars {
		if b.WorkOrderId == wos[0].Id {
			bars = append(bars, b)
		}
	}
	if len(bars) != 1 || !bars[0].StartsAt.Equal(osloAt(9, 14, 7, 0)) || !bars[0].EndsAt.Equal(osloAt(9, 14, 9, 0)) {
		t.Fatalf("the done work order's bars %+v, want Monday 07:00–09:00 only", bars)
	}
	if saw.Days[0].PlannedMinutes != 120 || saw.Days[1].PlannedMinutes != 0 {
		t.Errorf("planned Monday %d, Tuesday %d minutes", saw.Days[0].PlannedMinutes, saw.Days[1].PlannedMinutes)
	}

	res := c.Do(http.MethodGet, fmt.Sprintf("%s/reports/utilisation?from=2026-09-14&to=2026-09-20&siteId=%d", base, siteMain), nil)
	mustStatus(t, res, http.StatusOK, "utilisation")
	var report gen.ManufacturingUtilisationReport
	res.JSON(&report)
	for _, row := range report.Rows {
		if row.WorkCenter.Id == f.saw.Id && row.PlannedMinutes != 120 {
			t.Errorf("SAW planned %d minutes in the week, want 120", row.PlannedMinutes)
		}
	}
}

// TestSchedule_OnlyPlanningSetsTheWindow is the review's M2: the planned
// window is no input of the create or the update; an order is planned when
// its open work orders hold slots, so one with a window and no slots — set
// by hand before this rule — is still on the Unscheduled list, and plan all
// plans it; an update keeps the window planning set.
func TestSchedule_OnlyPlanningSetsTheWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	for _, field := range []string{"plannedStart", "plannedFinish"} {
		body := map[string]any{"variantId": varBike, "quantity": "1", field: "2026-09-14T07:00:00Z"}
		expectRefusal(t, c.Do(http.MethodPost, ordersPath, body), http.StatusBadRequest, "", "a create naming "+field)
	}

	handSet, _ := openOrder(t, c, "1")
	if _, err := h.Pool().Exec(context.Background(),
		"UPDATE manufacturing.orders SET planned_start = $2, planned_finish = $2 WHERE id = $1", handSet.Id, osloAt(9, 20, 7, 0)); err != nil {
		t.Fatal(err)
	}
	upd := orderUpdateOf(getOrder(t, c, handSet.Id))
	b, err := json.Marshal(upd)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	raw["plannedStart"] = "2026-09-14T07:00:00Z"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(handSet.Id), raw), http.StatusBadRequest, "", "an update naming plannedStart")

	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain))
	if len(s.Unscheduled) != 1 || s.Unscheduled[0].Id != handSet.Id {
		t.Fatalf("unscheduled %+v, want the order with a window and no slots", s.Unscheduled)
	}
	res := c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	mustStatus(t, res, http.StatusOK, "plan all")
	var out gen.ManufacturingPlanAllResult
	res.JSON(&out)
	if len(out.Planned) != 1 || out.Planned[0].Id != handSet.Id {
		t.Fatalf("plan all %+v", out)
	}
	planned := getOrder(t, c, handSet.Id)
	if !sameInstant(planned.PlannedStart, osloAt(9, 14, 7, 0)) {
		t.Errorf("planned from %v, want Monday 07:00", planned.PlannedStart)
	}

	res = c.Do(http.MethodPut, orderPath(planned.Id), orderUpdateOf(planned))
	mustStatus(t, res, http.StatusOK, "update the planned order")
	var updated gen.ManufacturingOrder
	res.JSON(&updated)
	if !sameInstant(updated.PlannedStart, *planned.PlannedStart) || !sameInstant(updated.PlannedFinish, *planned.PlannedFinish) {
		t.Errorf("window after the update %v–%v, want %v–%v", updated.PlannedStart, updated.PlannedFinish, planned.PlannedStart, planned.PlannedFinish)
	}
	if s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14", siteMain)); len(s.Unscheduled) != 0 {
		t.Errorf("unscheduled after plan all %+v", s.Unscheduled)
	}
}

// TestSchedule_PlanAllSkipsWithoutUnbooking is the review's L6: an order
// plan all skips as no_capacity takes nothing out of the time the others
// are planned around.
func TestSchedule_PlanAllSkipsWithoutUnbooking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	newFloor(t, h, c)
	first, _ := openOrder(t, c, "60")
	planOrder(t, c, first)
	in := workCenterInput("IDLE", siteMain)
	none := []gen.ManufacturingInterval{}
	in.Pattern = &gen.ManufacturingWeekPattern{Monday: none, Tuesday: none, Wednesday: none, Thursday: none, Friday: none, Saturday: none, Sunday: none}
	idle := createWorkCenter(t, c, in)
	bom := bomInput(varWheel, "1")
	bom.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, idle.Id, "5")}
	createBom(t, c, bom)
	skipped := confirmOrder(t, c, createOrder(t, c, orderInput(varWheel, "1"))).Order
	second, _ := openOrder(t, c, "60")

	res := c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	mustStatus(t, res, http.StatusOK, "plan all")
	var out gen.ManufacturingPlanAllResult
	res.JSON(&out)
	if len(out.Planned) != 1 || out.Planned[0].Id != second.Id || len(out.NoCapacity) != 1 || out.NoCapacity[0].Id != skipped.Id {
		t.Fatalf("plan all %+v", out)
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
	if got := getOrder(t, c, first.Id); !sameInstant(got.PlannedStart, osloAt(9, 14, 7, 0)) {
		t.Errorf("the first order moved to %v", got.PlannedStart)
	}
}

// TestSchedule_PlanFromAnInstantBetweenMicroseconds is the review's L3: now
// is cut to the second, so a running work order planned from an instant a
// fraction of a microsecond before its interval ends stores a slot Postgres
// keeps, not an empty one.
func TestSchedule_PlanFromAnInstantBetweenMicroseconds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	newFloor(t, h, c)
	o, wos := openOrder(t, c, "60")
	h.Advance(48*time.Hour + 59*time.Minute + 59*time.Second + 999_999_500*time.Nanosecond) // Monday 14:59:59.9999995
	c = h.SignIn(t, everything...)
	act(t, c, wos[0].Id, "start", nil)
	planOrder(t, c, getOrder(t, c, o.Id))
	got := workOrdersOf(t, c, o.Id)
	if !sameInstant(got[0].PlannedStart, osloAt(9, 14, 14, 59).Add(59*time.Second)) {
		t.Errorf("planned from %v, want Monday 14:59:59", got[0].PlannedStart)
	}
}
