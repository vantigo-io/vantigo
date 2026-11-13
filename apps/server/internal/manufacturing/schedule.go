package manufacturing

// The production schedule (manufacturing design M34–M37, M35a): planning an
// order's work orders forward on their work centers' calendars, plan all,
// unplan, a work order moved on the board, and the board's read.
//
// The locks (M35a, M26a): every write here first takes the schedule's
// advisory lock of each work center it touches, in id order
// (LockScheduleCenter, through withScheduledOrders), before any row lock —
// LockVariants included — and then follows the canonical order: the
// variants, the orders FOR UPDATE in id order, the work orders in id order.
// Free time is read from work_order_slots under those locks, and every
// writer of a center's slots holds its lock, so two plans never book the
// same time. Plan all takes the locks of every work center at the site, then
// the orders, and only then plans them one by one in priority order, in
// memory.

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// codeNoCapacity is the 409 of a work order that does not fit within the
// horizon (M35).
const codeNoCapacity = "no_capacity"

// priorityUrgent is an urgent order's priority, planned first (M35).
const priorityUrgent = string(gen.ManufacturingPriorityUrgent)

// scheduleCentersKey carries, into a schedule write's fn, the work centers
// whose advisory locks the transaction holds.
type scheduleCentersKey struct{}

// lockedCenters is the work centers ctx's transaction holds the schedule
// lock of.
func lockedCenters(ctx context.Context) []int64 {
	c, _ := ctx.Value(scheduleCentersKey{}).([]int64)
	return c
}

// centersHeld reports whether every work order names a locked center; a
// write that finds one that does not starts again (errRelock).
func centersHeld(ctx context.Context, wos []store.ManufacturingWorkOrder) bool {
	held := lockedCenters(ctx)
	for _, w := range wos {
		if !slices.Contains(held, w.WorkCenterID) {
			return false
		}
	}
	return true
}

// ordersCenters is a centersOf: the work centers the orders' work orders
// name.
func ordersCenters(ctx context.Context, q *store.Queries, orderIDs []int64) ([]int64, error) {
	return q.WorkCenterIDsOfOrders(ctx, orderIDs)
}

// noCapacity is the 409 of a work order that found no room within 365 days.
func noCapacity(w planWork, from time.Time) error {
	return conflict(codeNoCapacity, "No capacity within 365 days",
		fmt.Sprintf("Work order %d does not fit on its work center's calendar within %d days of %s. Nothing was planned.",
			w.id, planHorizonDays, from.UTC().Format(time.RFC3339)))
}

// ---- planning, in memory -------------------------------------------------

// planOption is one center a work order may be placed at: its own, or for a
// group work order whose member is not fixed every active, unblocked member
// of the group (manufacturing phase 2 M119), with the working time it needs
// there and the figures the send-ahead offsets are computed from.
type planOption struct {
	center     int64
	need       time.Duration
	efficiency int32
	code       string
	setup      *big.Rat // minutes
	perCycle   *big.Rat // minutes
	capacity   int32
	member     *store.SetWorkOrderMemberParams // the figures taken again when this member is chosen (M118); nil for the current one
}

// planWork is one work order to place: the centers it may go to (the first
// its current one), its predecessors, whether it runs already and so starts
// from now (M35a), and its send-ahead quantity (M116).
type planWork struct {
	id        int64
	options   []planOption
	preds     []int64
	fromNow   bool
	output    *big.Rat      // the BoM's output quantity, for the send-ahead batches
	sendAhead *big.Rat      // nil, or the good units its successors may start after
	aheadMet  bool          // it has passed on its send-ahead quantity already
	logged    time.Duration // the productive time logged on it
}

// placement is where a work order went: its center, its pieces, in order,
// its window and the option it took.
type placement struct {
	id            int64
	center        int64
	pieces        []span
	start, finish time.Time
	option        planOption
}

// optionFigures is a work order's figures at its current center.
func optionFigures(w store.ManufacturingWorkOrder, logged map[int64]time.Duration, code string) planOption {
	return planOption{center: w.WorkCenterID, need: remaining(w, logged), efficiency: w.TimeEfficiency, code: code,
		setup: ratOrZero(w.SetupMinutes), perCycle: ratOrZero(w.MinutesPerCycle), capacity: w.Capacity}
}

// aheadAt is the working time p needs at o for its send-ahead quantity
// (M116): its setup and the cycles of that quantity, less what it has
// logged already.
func aheadAt(p planWork, o planOption) time.Duration {
	if p.sendAhead == nil {
		return 0
	}
	minutes := new(big.Rat).Add(o.setup, runMinutes(p.sendAhead, p.output, o.perCycle, o.capacity, o.efficiency))
	return max(ratDuration(minutes)-p.logged, 0)
}

// batchAt is the working time w needs at o for quantity q, setup left out:
// the last send-ahead batch it finishes after its predecessor (M116).
func batchAt(w planWork, o planOption, q *big.Rat) time.Duration {
	return ratDuration(runMinutes(q, w.output, o.perCycle, o.capacity, o.efficiency))
}

// instantAfter is the instant pl has worked d of its pieces; its finish when
// d is all of them or more.
func instantAfter(pl placement, d time.Duration) time.Time {
	if d <= 0 {
		return pl.start
	}
	for _, piece := range pl.pieces {
		if piece.length() >= d {
			return piece.start.Add(d)
		}
		d -= piece.length()
	}
	return pl.finish
}

// fillAfter is fill, the finish held at no earlier than minFinish: the start
// is pushed on by what the finish falls short, until it does not.
func fillAfter(cal workCalendar, from time.Time, need time.Duration, booked []span, until, minFinish time.Time) ([]span, time.Time, bool) {
	for range 64 {
		pieces, finish, ok := cal.fill(from, need, booked, until)
		if !ok || !finish.Before(minFinish) {
			return pieces, finish, ok
		}
		from = from.Add(minFinish.Sub(finish))
	}
	return cal.fill(from, need, booked, until)
}

// bounds is where a work order may start by its predecessors' placements
// (M115, M116): after each one's finish, or for one with a send-ahead
// quantity after it has worked that quantity — and then it may not finish
// before that predecessor's finish plus its own last batch.
type bounds struct {
	earliest time.Time
	batches  []struct {
		finish time.Time
		q      *big.Rat
	}
}

func boundsOf(w planWork, from time.Time, placed map[int64]placement, works map[int64]planWork) bounds {
	b := bounds{earliest: from}
	for _, id := range w.preds {
		pl, ok := placed[id]
		if !ok {
			continue // done, cancelled or not planned
		}
		p := works[id]
		if p.sendAhead == nil {
			if pl.finish.After(b.earliest) {
				b.earliest = pl.finish
			}
			continue
		}
		if !p.aheadMet {
			if t := instantAfter(pl, aheadAt(p, pl.option)); t.After(b.earliest) {
				b.earliest = t
			}
		}
		b.batches = append(b.batches, struct {
			finish time.Time
			q      *big.Rat
		}{pl.finish, p.sendAhead})
	}
	return b
}

// minFinishAt is the earliest w may finish at o by the send-ahead batches.
func (b bounds) minFinishAt(w planWork, o planOption) time.Time {
	var out time.Time
	for _, x := range b.batches {
		if t := x.finish.Add(batchAt(w, o, x.q)); t.After(out) {
			out = t
		}
	}
	return out
}

// placeAt places w at o from b, around booked.
func placeAt(cals map[int64]workCalendar, booked []span, w planWork, o planOption, b bounds, until time.Time) (placement, bool) {
	pieces, finish, ok := fillAfter(cals[o.center], b.earliest, o.need, booked, until, b.minFinishAt(w, o))
	if !ok {
		return placement{}, false
	}
	first := b.earliest
	if len(pieces) > 0 {
		first = pieces[0].start
	}
	return placement{id: w.id, center: o.center, pieces: pieces, start: first, finish: finish, option: o}, true
}

// better reports whether a is a better placement than b (M119): the earlier
// finish, then the higher time efficiency, then the code.
func better(a, b placement) bool {
	if c := a.finish.Compare(b.finish); c != 0 {
		return c < 0
	}
	if a.option.efficiency != b.option.efficiency {
		return a.option.efficiency > b.option.efficiency
	}
	return a.option.code < b.option.code
}

// planGraph places works, in a topological order, after their predecessors
// (M35, M115): each from the latest of its predecessors' finishes — or their
// send-ahead offsets (M116) — never before start, or from now when it runs
// already, into the first free time that holds it at each center it may go
// to, taking the one that finishes first (M119), around booked and around the
// pieces placed before it. It writes nothing to booked; the caller books the
// placements once it keeps them. The horizon is 365 days from start; the work
// order that does not fit is answered with ok false.
func planGraph(cals map[int64]workCalendar, booked map[int64][]span, works []planWork, start, now time.Time) ([]placement, planWork, bool) {
	until := start.AddDate(0, 0, planHorizonDays)
	local := map[int64][]span{}
	bookedAt := func(center int64) []span {
		if b, ok := local[center]; ok {
			return b
		}
		return booked[center]
	}
	byID := make(map[int64]planWork, len(works))
	for _, w := range works {
		byID[w.id] = w
	}
	placed := map[int64]placement{}
	out := make([]placement, 0, len(works))
	for _, w := range works {
		b := boundsOf(w, start, placed, byID)
		if w.fromNow {
			b = bounds{earliest: now}
		}
		var best *placement
		for _, o := range w.options {
			pl, ok := placeAt(cals, bookedAt(o.center), w, o, b, until)
			if ok && (best == nil || better(pl, *best)) {
				best = &pl
			}
		}
		if best == nil {
			return nil, w, false
		}
		local[best.center] = book(bookedAt(best.center), best.pieces)
		placed[w.id] = *best
		out = append(out, *best)
	}
	return out, planWork{}, true
}

// planner is one schedule write's view of its centers: their calendars and
// the time booked at them, in the business time zone at now.
type planner struct {
	zone   *time.Location
	now    time.Time
	today  time.Time
	cals   map[int64]workCalendar
	booked map[int64][]span
}

// loadPlanner reads, under the centers' locks, their calendars and the
// slots booked at them from now on, but for the work orders except names.
func loadPlanner(ctx context.Context, txq *store.Queries, centers, except []int64, zone *time.Location, now time.Time) (*planner, error) {
	p := &planner{zone: zone, now: now, today: holidays.DayIn(now, zone), cals: map[int64]workCalendar{}, booked: map[int64][]span{}}
	if len(centers) == 0 {
		return p, nil
	}
	rows, err := txq.WorkCentersByIDs(ctx, centers)
	if err != nil {
		return nil, err
	}
	closures, err := txq.ClosuresOfWorkCenters(ctx, store.ClosuresOfWorkCentersParams{
		WorkCenterIds: centers, FromDate: dateValue(p.today.AddDate(0, 0, -1)), ToDate: dateValue(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		return nil, err
	}
	byCenter := map[int64][]store.ManufacturingWorkCenterClosure{}
	for _, c := range closures {
		byCenter[c.WorkCenterID] = append(byCenter[c.WorkCenterID], c)
	}
	for _, w := range rows {
		p.cals[w.ID] = calendarOf(w, byCenter[w.ID], zone)
	}
	slots, err := txq.BookedSlots(ctx, store.BookedSlotsParams{WorkCenterIds: centers, FromAt: now, ExceptIds: orEmpty(except)})
	if err != nil {
		return nil, err
	}
	for _, sl := range slots {
		p.booked[sl.WorkCenterID] = append(p.booked[sl.WorkCenterID], span{start: sl.StartsAt, end: sl.EndsAt})
	}
	return p, nil
}

// orEmpty is ids, never nil: a nil array is NULL to Postgres, and
// NOT (x = ANY(NULL)) is never true.
func orEmpty(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// dateValue is a civil date as a date column's value.
func dateValue(d time.Time) pgtype.Date { return pgtype.Date{Time: civilDay(d), Valid: true} }

// dateOf is a date column as a civil date, nil when NULL.
func dateOf(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := civilDay(d.Time)
	return &t
}

// openWork is the work orders planning places: not done or cancelled, by
// sequence (M35a: a done work order is never planned).
func openWork(wos []store.ManufacturingWorkOrder) []store.ManufacturingWorkOrder {
	out := make([]store.ManufacturingWorkOrder, 0, len(wos))
	for _, w := range wos {
		if w.Status != workOrderDone && w.Status != workOrderCancelled {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b store.ManufacturingWorkOrder) int {
		return cmp.Or(cmp.Compare(a.Sequence, b.Sequence), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// expectedOf is the expected minutes of the work orders.
func expectedOf(wos []store.ManufacturingWorkOrder) int64 {
	var total int64
	for _, w := range wos {
		total += w.ExpectedMinutes
	}
	return total
}

// productiveLogged is each work order's productive time logged, open logs
// counted to now: what an in-progress work order has done already (M35a).
// It is the union of its logs, as the cost counts it (M40): two operators
// on the work order at once have done that time once.
func productiveLogged(logs []store.ManufacturingTimeLog, now time.Time) map[int64]time.Duration {
	spans := map[int64][]span{}
	for _, l := range logs {
		if l.WorkOrderID != nil && l.Kind == logProductive {
			spans[*l.WorkOrderID] = append(spans[*l.WorkOrderID], logSpan(l, now))
		}
	}
	out := make(map[int64]time.Duration, len(spans))
	for id, s := range spans {
		out[id] = unionLength(s)
	}
	return out
}

// remaining is the working time a work order still needs: expected less
// the productive time logged, at least 0 (M35a).
func remaining(w store.ManufacturingWorkOrder, logged map[int64]time.Duration) time.Duration {
	return max(time.Duration(w.ExpectedMinutes)*time.Minute-logged[w.ID], 0)
}

// started reports whether a work order runs already, so plans from now.
func started(w store.ManufacturingWorkOrder) bool {
	return w.Status == workOrderInProgress || w.Status == workOrderPaused
}

// requestedStartOf is the order's requested start (M35a): the stored one,
// else the default from its due date and the work it has left.
func (p *planner) requestedStartOf(o store.ManufacturingOrder, open []store.ManufacturingWorkOrder) time.Time {
	if d := dateOf(o.RequestedStart); d != nil {
		return *d
	}
	return defaultRequestedStart(dateOf(o.DueDate), expectedOf(open), p.today)
}

// searchStart is where planning the order starts: the later of now and its
// requested start's first instant (M35).
func (p *planner) searchStart(o store.ManufacturingOrder, open []store.ManufacturingWorkOrder) time.Time {
	start := dayStart(p.requestedStartOf(o, open), p.zone)
	if start.Before(p.now) {
		return p.now
	}
	return start
}

// orderPlan is an order planned in memory: where its work orders go and its
// window, not yet written.
type orderPlan struct {
	order      store.ManufacturingOrder
	clear      []int64 // the work orders whose slots go
	placements []placement
	start      *time.Time
	finish     *time.Time
}

// plan places the order's work orders (M35, M35a, M115) on p: those not done,
// from searchStart, each after its predecessors in the order's graph, an
// in-progress one from now with only its remaining minutes, a group work
// order at the member that finishes it first (M119). An order without work
// orders gets M34's window: its due date less the BoM's lead time in
// business days, to its due date. Nothing is written; no_capacity names the
// first work order that does not fit.
func (p *planner) plan(o store.ManufacturingOrder, in planInput) (orderPlan, error) {
	out := orderPlan{order: o}
	open := openWork(in.wos)
	start := p.searchStart(o, open)
	live := 0
	for _, w := range in.wos {
		if w.Status != workOrderCancelled {
			live++
		}
	}
	if live == 0 {
		s, f := start, start
		if due := dateOf(o.DueDate); due != nil {
			s = dayStart(previousBusinessDays(*due, int(max(in.lead, 0))), p.zone)
			f = dayStart(*due, p.zone)
		}
		out.start, out.finish = &s, &f
		return out, nil
	}
	for _, w := range open {
		out.clear = append(out.clear, w.ID)
	}
	placements, failed, ok := planGraph(p.cals, p.booked, worksOf(o, in, open, true), start, p.now)
	if !ok {
		return orderPlan{}, noCapacity(failed, start)
	}
	out.placements = placements
	out.start, out.finish = windowOf(in.wos, placements)
	return out, nil
}

// windowOf is an order's planned window (M34): the earliest planned start
// and the latest planned finish of its work orders, the placements standing
// in for the work orders they place.
func windowOf(wos []store.ManufacturingWorkOrder, placements []placement) (*time.Time, *time.Time) {
	placed := map[int64]placement{}
	for _, pl := range placements {
		placed[pl.id] = pl
	}
	var start, finish *time.Time
	consider := func(s, f *time.Time) {
		if s != nil && (start == nil || s.Before(*start)) {
			v := *s
			start = &v
		}
		if f != nil && (finish == nil || f.After(*finish)) {
			v := *f
			finish = &v
		}
	}
	for _, w := range wos {
		if w.Status == workOrderCancelled {
			continue
		}
		if pl, ok := placed[w.ID]; ok {
			consider(&pl.start, &pl.finish)
			continue
		}
		consider(w.PlannedStart, w.PlannedFinish)
	}
	return start, finish
}

// apply writes the plan: the cleared work orders' slots go, each placement's
// pieces become its slots and its window its planned dates, and the order
// takes its window. The placements are then booked on p, for the next order
// plan all places.
func (p *planner) apply(ctx context.Context, txq *store.Queries, pl orderPlan, notes *notices) (store.ManufacturingOrder, error) {
	if len(pl.clear) > 0 {
		if err := txq.DeleteSlotsOfWorkOrders(ctx, pl.clear); err != nil {
			return store.ManufacturingOrder{}, err
		}
	}
	for _, x := range pl.placements {
		if x.option.member != nil {
			// Another member of the group (M118, M119): its figures are taken
			// again, the old center's queue and board told.
			before, err := txq.GetWorkOrder(ctx, x.id)
			if err != nil {
				return store.ManufacturingOrder{}, err
			}
			if _, err := txq.SetWorkOrderMember(ctx, *x.option.member); err != nil {
				return store.ManufacturingOrder{}, err
			}
			notes.workOrder(before.ID, before.OrderID, before.WorkCenterID)
		}
		for _, piece := range x.pieces {
			if err := txq.InsertSlot(ctx, store.InsertSlotParams{WorkOrderID: x.id, WorkCenterID: x.center, StartsAt: piece.start, EndsAt: piece.end}); err != nil {
				return store.ManufacturingOrder{}, err
			}
		}
		start, finish := x.start, x.finish
		w, err := txq.SetWorkOrderPlan(ctx, store.SetWorkOrderPlanParams{ID: x.id, PlannedStart: &start, PlannedFinish: &finish})
		if err != nil {
			return store.ManufacturingOrder{}, err
		}
		notes.workOrder(w.ID, w.OrderID, w.WorkCenterID)
		// Planned, a group work order leaves the members' Group work (M119).
		if err := notes.groupWork(ctx, txq, w); err != nil {
			return store.ManufacturingOrder{}, err
		}
		p.booked[x.center] = book(p.booked[x.center], x.pieces)
	}
	o, err := txq.SetOrderPlan(ctx, store.SetOrderPlanParams{ID: pl.order.ID, PlannedStart: pl.start, PlannedFinish: pl.finish, Now: p.now})
	if err != nil {
		return o, err
	}
	notes.orders[o.ID] = true
	notes.sites[o.SiteID] = true
	return o, nil
}

// ---- planning, the operations --------------------------------------------

// planInput is what planning an order reads beside the order, under the
// locks: its work orders (locked), their logs, its BoM's lead time, its work
// order graph (M113) and the members of the groups its work orders target
// (M119).
type planInput struct {
	wos     []store.ManufacturingWorkOrder
	logged  map[int64]time.Duration
	hasLogs map[int64]bool
	lead    int32
	graph   workOrderGraph
	members map[int64][]store.ManufacturingWorkCenter // of the active groups, by group, by code
}

// worksOf is the order's open work orders as planWork, in a topological order
// (M115): each at its own center, or — a group work order with no time log —
// at whichever active, unblocked member of its group finishes first (M119),
// its own member among them when it is one; with none such, its own.
func worksOf(o store.ManufacturingOrder, in planInput, open []store.ManufacturingWorkOrder, choose bool) []planWork {
	quantity := ratOrZero(o.Quantity)
	out := make([]planWork, 0, len(open))
	for _, w := range in.graph.topological(open) {
		pw := planWork{id: w.ID, preds: in.graph.preds[w.ID], fromNow: started(w), output: ratOrZero(w.BomOutputQuantity),
			sendAhead: sendAheadOf(w, quantity), logged: in.logged[w.ID]}
		if pw.sendAhead != nil {
			pw.aheadMet = ratOrZero(w.QuantityDone).Cmp(pw.sendAhead) >= 0
		}
		code := ""
		var members []store.ManufacturingWorkCenter
		if w.WorkCenterGroupID != nil {
			members = in.members[*w.WorkCenterGroupID]
			for _, c := range members {
				if c.ID == w.WorkCenterID {
					code = c.Code
				}
			}
		}
		own := optionFigures(w, in.logged, code)
		if choose && w.WorkCenterGroupID != nil && !in.hasLogs[w.ID] {
			for _, c := range members {
				if c.Status != statusActive || c.WorkingState == stateBlocked {
					continue
				}
				if c.ID == w.WorkCenterID {
					pw.options = append(pw.options, own)
					continue
				}
				f := memberFigures(w, c, quantity)
				pw.options = append(pw.options, planOption{center: c.ID, need: time.Duration(f.ExpectedMinutes) * time.Minute,
					efficiency: c.TimeEfficiency, code: c.Code, setup: ratOrZero(f.SetupMinutes), perCycle: ratOrZero(w.MinutesPerCycle),
					capacity: c.Capacity, member: &f})
			}
		}
		if len(pw.options) == 0 {
			pw.options = []planOption{own}
		}
		out = append(out, pw)
	}
	return out
}

// planInputs reads what planning the order needs, under the locks: a work
// order, or a member of a group one targets, on a center not locked starts
// the write again (errRelock).
func planInputs(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, now time.Time) (planInput, error) {
	var in planInput
	wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
	if err != nil {
		return in, err
	}
	if !centersHeld(ctx, wos) {
		return in, errRelock
	}
	ids := make([]int64, 0, len(wos))
	var groups []int64
	for _, w := range wos {
		ids = append(ids, w.ID)
		if w.WorkCenterGroupID != nil {
			groups = append(groups, *w.WorkCenterGroupID)
		}
	}
	if len(ids) > 0 {
		if wos, err = txq.LockWorkOrders(ctx, ids); err != nil {
			return in, err
		}
	}
	slices.SortFunc(wos, func(a, b store.ManufacturingWorkOrder) int {
		return cmp.Or(cmp.Compare(a.Sequence, b.Sequence), cmp.Compare(a.ID, b.ID))
	})
	in.wos = wos
	if in.members, err = choosableMembers(ctx, txq, groups); err != nil {
		return in, err
	}
	held := lockedCenters(ctx)
	for _, members := range in.members {
		for _, c := range members {
			if !slices.Contains(held, c.ID) {
				return in, errRelock
			}
		}
	}
	logs, err := txq.LogsOfWorkOrders(ctx, ids)
	if err != nil {
		return in, err
	}
	in.logged = productiveLogged(logs, now)
	in.hasLogs = map[int64]bool{}
	for _, l := range logs {
		if l.WorkOrderID != nil {
			in.hasLogs[*l.WorkOrderID] = true
		}
	}
	if in.graph, err = readGraph(ctx, txq, []int64{o.ID}); err != nil {
		return in, err
	}
	bom, err := txq.GetBom(ctx, o.BomID)
	if err != nil {
		return in, err
	}
	in.lead = bom.LeadTimeDays
	return in, nil
}

// workOrderIDs is the ids of wos.
func workOrderIDs(wos []store.ManufacturingWorkOrder) []int64 {
	ids := make([]int64, 0, len(wos))
	for _, w := range wos {
		ids = append(ids, w.ID)
	}
	return ids
}

// scheduleClock is the business zone and now, read before any transaction.
// Now is cut to the second: a piece the planner starts at now is then never
// shorter than the microsecond Postgres keeps, which would store it empty
// (ck_work_order_slots_range).
func (s *server) scheduleClock(ctx context.Context) (*time.Location, time.Time, error) {
	zone, err := s.businessZone(ctx)
	return zone, s.deps.Clock().Truncate(time.Second), err
}

func planAnswers() answers[gen.PlanManufacturingOrderResponseObject] {
	return answers[gen.PlanManufacturingOrderResponseObject]{
		notFound: func() gen.PlanManufacturingOrderResponseObject { return gen.PlanManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.PlanManufacturingOrderResponseObject {
			return gen.PlanManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.PlanManufacturingOrderResponseObject {
			return gen.PlanManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// PlanManufacturingOrder plans one order (M35, M35a), replacing its earlier
// plan.
func (s *server) PlanManufacturingOrder(ctx context.Context, req gen.PlanManufacturingOrderRequestObject) (gen.PlanManufacturingOrderResponseObject, error) {
	zone, now, err := s.scheduleClock(ctx)
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withScheduledOrders(ctx, ordersCenters, func(context.Context, *store.Queries) ([]int64, error) { return []int64{req.Id}, nil }, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			o := orders[req.Id].order
			if err := checkRevision(o, req.Body.Revision); err != nil {
				return err
			}
			if !isOpen(o.Status) {
				return orderNotOpen(o.Status)
			}
			in, err := planInputs(ctx, txq, o, now)
			if err != nil {
				return err
			}
			p, err := loadPlanner(ctx, txq, lockedCenters(ctx), workOrderIDs(in.wos), zone, now)
			if err != nil {
				return err
			}
			pl, err := p.plan(o, in)
			if err != nil {
				return err
			}
			notes := newNotices()
			if _, err := p.apply(ctx, txq, pl, notes); err != nil {
				return err
			}
			if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindPlanned, who, now, planPayload(pl)); err != nil {
				return err
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, planAnswers())
	}
	out, err := s.orderAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, planAnswers())
	}
	return gen.PlanManufacturingOrder200JSONResponse(out), nil
}

// planPayload is a plan's timeline figures.
func planPayload(pl orderPlan) map[string]any {
	payload := map[string]any{"workOrders": len(pl.placements)}
	if pl.start != nil {
		payload["plannedStart"] = pl.start.UTC().Format(time.RFC3339)
	}
	if pl.finish != nil {
		payload["plannedFinish"] = pl.finish.UTC().Format(time.RFC3339)
	}
	return payload
}

func planAllAnswers() answers[gen.PlanAllManufacturingOrdersResponseObject] {
	return answers[gen.PlanAllManufacturingOrdersResponseObject]{
		conflict: func(p gen.ManufacturingProblem) gen.PlanAllManufacturingOrdersResponseObject {
			return gen.PlanAllManufacturingOrders409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.PlanAllManufacturingOrdersResponseObject {
			return gen.PlanAllManufacturingOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// planOrderPriority is plan all's order (M35): urgent first, then by due
// date (none last), then by number.
func planOrderPriority(a, b store.ManufacturingOrder) int {
	if (a.Priority == priorityUrgent) != (b.Priority == priorityUrgent) {
		if a.Priority == priorityUrgent {
			return -1
		}
		return 1
	}
	switch {
	case a.DueDate.Valid && b.DueDate.Valid:
		if c := a.DueDate.Time.Compare(b.DueDate.Time); c != 0 {
			return c
		}
	case a.DueDate.Valid:
		return -1
	case b.DueDate.Valid:
		return 1
	}
	return cmp.Or(cmp.Compare(a.Number, b.Number), cmp.Compare(a.Suffix, b.Suffix), cmp.Compare(a.ID, b.ID))
}

// PlanAllManufacturingOrders plans every open order at the site with no
// plan (M35, M35a): the locks of every work center at the site first, then
// the orders in id order, then each planned in priority order in memory, on
// the bookings of those before it. An order with no room is left unplanned
// and named.
func (s *server) PlanAllManufacturingOrders(ctx context.Context, req gen.PlanAllManufacturingOrdersRequestObject) (gen.PlanAllManufacturingOrdersResponseObject, error) {
	siteID := req.Body.SiteId
	zone, now, err := s.scheduleClock(ctx)
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var planned, skipped []store.ManufacturingOrder
	err = s.withScheduledOrders(ctx,
		func(ctx context.Context, q *store.Queries, orderIDs []int64) ([]int64, error) {
			centers, err := q.WorkCenterIDsAtSite(ctx, siteID)
			if err != nil {
				return nil, err
			}
			more, err := q.WorkCenterIDsOfOrders(ctx, orEmpty(orderIDs))
			return append(centers, more...), err
		},
		func(ctx context.Context, q *store.Queries) ([]int64, error) {
			return q.UnplannedOrderIDsAtSite(ctx, siteID)
		}, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			planned, skipped = nil, nil
			// Read again under the locks: an order is unplanned when none of
			// its open work orders holds a slot (M34), so planning one takes
			// no booking away from the time the others are planned around.
			unplanned, err := txq.UnplannedOrderIDsAtSite(ctx, siteID)
			if err != nil {
				return err
			}
			list := make([]store.ManufacturingOrder, 0, len(orders))
			for _, lo := range orders {
				if isOpen(lo.order.Status) && lo.order.SiteID == siteID && slices.Contains(unplanned, lo.order.ID) {
					list = append(list, lo.order)
				}
			}
			slices.SortFunc(list, planOrderPriority)
			read := map[int64]planInput{}
			for _, o := range slices.SortedFunc(slices.Values(list), func(a, b store.ManufacturingOrder) int { return cmp.Compare(a.ID, b.ID) }) {
				in, err := planInputs(ctx, txq, o, now)
				if err != nil {
					return err
				}
				read[o.ID] = in
			}
			// No booking is left out: the orders planned hold none, so an
			// order skipped as no_capacity keeps all it had and the others
			// are planned around every booking there is.
			p, err := loadPlanner(ctx, txq, lockedCenters(ctx), nil, zone, now)
			if err != nil {
				return err
			}
			notes := newNotices()
			notes.sites[siteID] = true
			for _, o := range list {
				pl, err := p.plan(o, read[o.ID])
				if _, ok := answerCode(err, codeNoCapacity); ok {
					skipped = append(skipped, o)
					continue
				}
				if err != nil {
					return err
				}
				written, err := p.apply(ctx, txq, pl, notes)
				if err != nil {
					return err
				}
				if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindPlanned, who, now, planPayload(pl)); err != nil {
					return err
				}
				planned = append(planned, written)
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, planAllAnswers())
	}
	out := gen.PlanAllManufacturingOrders200JSONResponse{Planned: []gen.ManufacturingOrderLink{}, NoCapacity: []gen.ManufacturingOrderLink{}}
	for _, o := range planned {
		out.Planned = append(out.Planned, linkOf(o))
	}
	for _, o := range skipped {
		out.NoCapacity = append(out.NoCapacity, linkOf(o))
	}
	return out, nil
}

// answerCode reports whether err is a refusal with code.
func answerCode(err error, code string) (gen.ManufacturingProblem, bool) {
	if err == nil {
		return gen.ManufacturingProblem{}, false
	}
	_, p, ok := answerOf(err)
	if !ok || p.Code == nil || *p.Code != code {
		return gen.ManufacturingProblem{}, false
	}
	return p, true
}

func unplanAnswers() answers[gen.UnplanManufacturingOrderResponseObject] {
	return answers[gen.UnplanManufacturingOrderResponseObject]{
		notFound: func() gen.UnplanManufacturingOrderResponseObject { return gen.UnplanManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.UnplanManufacturingOrderResponseObject {
			return gen.UnplanManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UnplanManufacturingOrderResponseObject {
			return gen.UnplanManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UnplanManufacturingOrder clears the order's plan (M35): its work orders
// not done lose their slots and planned dates, and the order its window.
func (s *server) UnplanManufacturingOrder(ctx context.Context, req gen.UnplanManufacturingOrderRequestObject) (gen.UnplanManufacturingOrderResponseObject, error) {
	now := s.deps.Clock()
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withScheduledOrders(ctx, ordersCenters, func(context.Context, *store.Queries) ([]int64, error) { return []int64{req.Id}, nil }, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			o := orders[req.Id].order
			if err := checkRevision(o, req.Body.Revision); err != nil {
				return err
			}
			if !isOpen(o.Status) {
				return orderNotOpen(o.Status)
			}
			in, err := planInputs(ctx, txq, o, now)
			if err != nil {
				return err
			}
			notes := newNotices()
			open := openWork(in.wos)
			if len(open) > 0 {
				if err := txq.DeleteSlotsOfWorkOrders(ctx, workOrderIDs(open)); err != nil {
					return err
				}
			}
			for _, w := range open {
				if _, err := txq.SetWorkOrderPlan(ctx, store.SetWorkOrderPlanParams{ID: w.ID}); err != nil {
					return err
				}
				notes.workOrder(w.ID, w.OrderID, w.WorkCenterID)
			}
			// Unplanned, a group work order with no log is Group work again (M119).
			if err := notes.groupWork(ctx, txq, open...); err != nil {
				return err
			}
			if _, err := txq.SetOrderPlan(ctx, store.SetOrderPlanParams{ID: o.ID, Now: now}); err != nil {
				return err
			}
			notes.orders[o.ID] = true
			notes.sites[o.SiteID] = true
			if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindUnplanned, who, now, nil); err != nil {
				return err
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, unplanAnswers())
	}
	out, err := s.orderAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, unplanAnswers())
	}
	return gen.UnplanManufacturingOrder200JSONResponse(out), nil
}

func rescheduleAnswers() answers[gen.RescheduleManufacturingWorkOrderResponseObject] {
	return answers[gen.RescheduleManufacturingWorkOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.RescheduleManufacturingWorkOrderResponseObject {
			return gen.RescheduleManufacturingWorkOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.RescheduleManufacturingWorkOrderResponseObject {
			return gen.RescheduleManufacturingWorkOrder404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.RescheduleManufacturingWorkOrderResponseObject {
			return gen.RescheduleManufacturingWorkOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.RescheduleManufacturingWorkOrderResponseObject {
			return gen.RescheduleManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// reschedule moves target to start in memory (M36): target takes the first
// free time from start (now when start is past) at its option, and each work
// order after it in the order's graph that is planned (M115) moves to after
// its predecessors — their finish, or their send-ahead offset (M116) — when it
// would otherwise start before that, or finish before its last send-ahead
// batch; the rest keep their slots. later is target's successors, direct or
// not, in a topological order; existing is every planned open work order's
// placement as it stands, and works every open work order of the order.
// booked holds every other booking, the order's other work orders included;
// it excludes target and later.
func reschedule(cals map[int64]workCalendar, booked map[int64][]span, target planWork, option planOption, later []planWork,
	existing map[int64]placement, works map[int64]planWork, start, now time.Time) ([]placement, planWork, bool) {
	if start.Before(now) {
		start = now
	}
	until := start.AddDate(0, 0, planHorizonDays)
	local := map[int64][]span{}
	bookedAt := func(center int64) []span {
		if b, ok := local[center]; ok {
			return b
		}
		return booked[center]
	}
	placed := make(map[int64]placement, len(existing)+1)
	for id, pl := range existing {
		placed[id] = pl
	}
	moved, ok := placeAt(cals, bookedAt(option.center), target, option, bounds{earliest: start}, until)
	if !ok {
		return nil, target, false
	}
	placed[target.id] = moved
	out := []placement{moved}
	// Every planned successor holds its slot until it moves, so one that moves
	// goes around those that stay; the target is placed before them, so one it
	// overlaps — a send-ahead successor at the same center — moves too.
	for _, w := range later {
		if was, planned := existing[w.id]; planned {
			local[was.center] = book(bookedAt(was.center), was.pieces)
		}
	}
	local[moved.center] = book(bookedAt(moved.center), moved.pieces)
	for _, w := range later {
		was, planned := existing[w.id]
		if !planned {
			continue // not planned: it stays so
		}
		b := boundsOf(w, start, placed, works)
		if !was.start.Before(b.earliest) && !was.finish.Before(b.minFinishAt(w, was.option)) &&
			(was.center != moved.center || !overlapsAny(was.pieces, moved.pieces)) {
			continue
		}
		local[was.center] = unbook(local[was.center], was.pieces)
		pl, ok := placeAt(cals, bookedAt(was.center), w, was.option, b, until)
		if !ok {
			return nil, w, false
		}
		local[pl.center] = book(bookedAt(pl.center), pl.pieces)
		placed[w.id] = pl
		out = append(out, pl)
	}
	return out, planWork{}, true
}

// unbook is booked without pieces, each taken out once.
func unbook(booked, pieces []span) []span {
	out := slices.Clone(booked)
	for _, p := range pieces {
		if i := slices.Index(out, p); i >= 0 {
			out = slices.Delete(out, i, i+1)
		}
	}
	return out
}

// overlapsAny reports whether any of a overlaps any of b.
func overlapsAny(a, b []span) bool {
	for _, x := range a {
		for _, y := range b {
			if x.start.Before(y.end) && y.start.Before(x.end) {
				return true
			}
		}
	}
	return false
}

// RescheduleManufacturingWorkOrder moves a work order on the board (M36),
// under its revision; no other order moves. A group work order dropped on
// another member's row (workCenterId) moves to that member, its figures taken
// again (manufacturing phase 2 M118, M119): a center outside its group, or any
// other center once the group is archived, is work_center_not_in_group, a
// blocked member work_center_blocked, and once a time log exists the member
// is fixed (work_order_started).
func (s *server) RescheduleManufacturingWorkOrder(ctx context.Context, req gen.RescheduleManufacturingWorkOrderRequestObject) (gen.RescheduleManufacturingWorkOrderResponseObject, error) {
	const title = "The work order was not moved"
	pre, err := s.workOrderOrNotFound(ctx, req.Id)
	if err != nil {
		return answer(err, rescheduleAnswers())
	}
	zone, now, err := s.scheduleClock(ctx)
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	orderID := pre.OrderID
	err = s.withScheduledOrders(ctx, ordersCenters, func(context.Context, *store.Queries) ([]int64, error) { return []int64{orderID}, nil }, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			o := orders[orderID].order
			in, err := planInputs(ctx, txq, o, now)
			if err != nil {
				return err
			}
			var target *store.ManufacturingWorkOrder
			for i := range in.wos {
				if in.wos[i].ID == req.Id {
					target = &in.wos[i]
				}
			}
			if target == nil {
				return notFound()
			}
			if target.Revision != req.Body.Revision {
				return revisionConflict("work order", target.Revision, req.Body.Revision)
			}
			if !isOpen(o.Status) {
				return orderNotOpen(o.Status)
			}
			if target.Status == workOrderDone || target.Status == workOrderCancelled {
				return workOrderClosed(target.Status)
			}
			open := openWork(in.wos)
			works := worksOf(o, in, open, false)
			byID := map[int64]planWork{}
			for _, w := range works {
				byID[w.id] = w
			}
			tw := byID[target.ID]
			option := tw.options[0]
			if at := req.Body.WorkCenterId; at != nil && *at != target.WorkCenterID {
				if target.WorkCenterGroupID == nil {
					return notInGroup(title)
				}
				i := slices.IndexFunc(in.members[*target.WorkCenterGroupID], func(c store.ManufacturingWorkCenter) bool {
					return c.ID == *at && c.Status == statusActive
				})
				if i < 0 {
					return notInGroup(title)
				}
				if in.hasLogs[target.ID] {
					return conflict(codeWorkOrderStarted, "The work order has started",
						"Time is logged on the work order, so it stays at its work center. Move it along its own row instead.")
				}
				c := in.members[*target.WorkCenterGroupID][i]
				if c.WorkingState == stateBlocked {
					return conflict(codeWorkCenterBlocked, "The work center is blocked",
						fmt.Sprintf("%s is blocked, so nothing is moved to it. Choose another work center of the group.", c.Code))
				}
				f := memberFigures(*target, c, ratOrZero(o.Quantity))
				option = planOption{center: c.ID, need: time.Duration(f.ExpectedMinutes) * time.Minute, efficiency: c.TimeEfficiency,
					code: c.Code, setup: ratOrZero(f.SetupMinutes), perCycle: ratOrZero(target.MinutesPerCycle), capacity: c.Capacity, member: &f}
			}
			after := in.graph.descendants(target.ID)
			var later []planWork
			for _, w := range works {
				if after[w.id] {
					later = append(later, w)
				}
			}
			except := []int64{target.ID}
			for _, w := range later {
				except = append(except, w.id)
			}
			p, err := loadPlanner(ctx, txq, lockedCenters(ctx), except, zone, now)
			if err != nil {
				return err
			}
			slots, err := txq.SlotsOfWorkOrders(ctx, workOrderIDs(open))
			if err != nil {
				return err
			}
			existing := map[int64]placement{}
			for _, w := range open {
				if w.PlannedStart != nil && w.PlannedFinish != nil {
					existing[w.ID] = placement{id: w.ID, center: w.WorkCenterID, start: *w.PlannedStart, finish: *w.PlannedFinish,
						option: byID[w.ID].options[0]}
				}
			}
			for _, sl := range slots {
				if pl, ok := existing[sl.WorkOrderID]; ok {
					pl.pieces = append(pl.pieces, span{start: sl.StartsAt, end: sl.EndsAt})
					existing[sl.WorkOrderID] = pl
				}
			}
			placements, failed, ok := reschedule(p.cals, p.booked, tw, option, later, existing, byID, req.Body.Start, now)
			if !ok {
				return noCapacity(failed, req.Body.Start)
			}
			pl := orderPlan{order: o, placements: placements}
			for _, x := range placements {
				pl.clear = append(pl.clear, x.id)
			}
			pl.start, pl.finish = windowOf(in.wos, placements)
			notes := newNotices()
			if _, err := p.apply(ctx, txq, pl, notes); err != nil {
				return err
			}
			if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindRescheduled, who, now, map[string]any{
				"workOrderId": target.ID, "start": placements[0].start.UTC().Format(time.RFC3339), "moved": len(placements),
			}); err != nil {
				return err
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, rescheduleAnswers())
	}
	got, err := s.ListManufacturingOrderWorkOrders(ctx, gen.ListManufacturingOrderWorkOrdersRequestObject{Id: orderID})
	if err != nil {
		return nil, err
	}
	return rescheduleAnswerOf(got), nil
}

// rescheduleAnswerOf is a reschedule's answer from the order's work orders
// read back: the list, the 503 of a module that is unavailable, else 404.
func rescheduleAnswerOf(got gen.ListManufacturingOrderWorkOrdersResponseObject) gen.RescheduleManufacturingWorkOrderResponseObject {
	switch list := got.(type) {
	case gen.ListManufacturingOrderWorkOrders200JSONResponse:
		return gen.RescheduleManufacturingWorkOrder200JSONResponse(list)
	case gen.ListManufacturingOrderWorkOrders503ApplicationProblemPlusJSONResponse:
		return gen.RescheduleManufacturingWorkOrder503ApplicationProblemPlusJSONResponse(list)
	}
	return gen.RescheduleManufacturingWorkOrder404Response{}
}

// ---- the board -----------------------------------------------------------

// scheduleDays are the board's spans (M36): a week, the default, or two.
const (
	scheduleWeek     = 7
	scheduleTwoWeeks = 14
)

// GetManufacturingSchedule is the board (M36): every active work center at
// the site with its days — open intervals, closed state, capacity, the
// planned minutes and the load — its closures, its blocked state and its
// bars; then the open orders with no plan.
func (s *server) GetManufacturingSchedule(ctx context.Context, req gen.GetManufacturingScheduleRequestObject) (gen.GetManufacturingScheduleResponseObject, error) {
	days := scheduleWeek
	if req.Params.Days != nil {
		days = int(*req.Params.Days)
	}
	if days != scheduleWeek && days != scheduleTwoWeeks {
		p := invalid("The schedule was not read", fieldErrors{errs: map[string][]string{"days": {"7 or 14"}}})
		return gen.GetManufacturingSchedule400ApplicationProblemPlusJSONResponse(p), nil
	}
	zone, now, err := s.scheduleClock(ctx)
	if err != nil {
		return nil, err
	}
	today := holidays.DayIn(now, zone)
	from := today
	if req.Params.From != nil {
		from = civilDay(req.Params.From.Time)
	}
	to := from.AddDate(0, 0, days-1)
	window := span{start: dayStart(from, zone), end: dayStart(to.AddDate(0, 0, 1), zone)}
	var (
		centers     []store.ManufacturingWorkCenter
		closures    []store.ManufacturingWorkCenterClosure
		slots       []store.ScheduleSlotsRow
		unscheduled []store.UnscheduledOrdersAtSiteRow
		members     map[int64][]store.ManufacturingWorkCenter
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		status := statusActive
		var err error
		if centers, err = q.ListWorkCenters(ctx, store.ListWorkCentersParams{Status: &status, SiteID: &req.Params.SiteId}); err != nil {
			return err
		}
		ids := make([]int64, 0, len(centers))
		for _, c := range centers {
			ids = append(ids, c.ID)
		}
		if closures, err = q.ClosuresOfWorkCenters(ctx, store.ClosuresOfWorkCentersParams{WorkCenterIds: orEmpty(ids), FromDate: dateValue(from), ToDate: dateValue(to)}); err != nil {
			return err
		}
		if slots, err = q.ScheduleSlots(ctx, store.ScheduleSlotsParams{WorkCenterIds: orEmpty(ids), FromAt: window.start, ToAt: window.end}); err != nil {
			return err
		}
		var groups []int64
		for _, sl := range slots {
			if sl.WorkCenterGroupID != nil {
				groups = append(groups, *sl.WorkCenterGroupID)
			}
		}
		if members, err = choosableMembers(ctx, q, groups); err != nil {
			return err
		}
		unscheduled, err = q.UnscheduledOrdersAtSite(ctx, req.Params.SiteId)
		return err
	})
	if err != nil {
		return nil, err
	}
	var variantIDs []int32
	for _, sl := range slots {
		variantIDs = append(variantIDs, sl.OrderVariantID)
	}
	for _, u := range unscheduled {
		variantIDs = append(variantIDs, u.ManufacturingOrder.VariantID)
	}
	entries, err := s.catalogVariants(ctx, slices.Compact(slices.Sorted(slices.Values(variantIDs))))
	if err != nil {
		return answer(err, answers[gen.GetManufacturingScheduleResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingScheduleResponseObject {
				return gen.GetManufacturingSchedule503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	costs := s.has(ctx, permCostsView)
	closuresOf := map[int64][]store.ManufacturingWorkCenterClosure{}
	for _, c := range closures {
		closuresOf[c.WorkCenterID] = append(closuresOf[c.WorkCenterID], c)
	}
	slotsOf := map[int64][]store.ScheduleSlotsRow{}
	for _, sl := range slots {
		slotsOf[sl.WorkCenterID] = append(slotsOf[sl.WorkCenterID], sl)
	}
	out := gen.GetManufacturingSchedule200JSONResponse{
		SiteId: req.Params.SiteId, From: openapi_types.Date{Time: from}, Days: int32(days), Today: openapi_types.Date{Time: today},
		WorkCenters: make([]gen.ManufacturingScheduleRow, 0, len(centers)), Unscheduled: make([]gen.ManufacturingScheduleOrder, 0, len(unscheduled)),
	}
	for _, c := range centers {
		cal := calendarOf(c, closuresOf[c.ID], zone)
		row := gen.ManufacturingScheduleRow{
			WorkCenter: centerRefOf(c, c.ID), Blocked: c.WorkingState == stateBlocked,
			Closures: make([]gen.ManufacturingClosure, 0, len(closuresOf[c.ID])),
			Days:     make([]gen.ManufacturingScheduleDay, 0, days), Bars: make([]gen.ManufacturingScheduleBar, 0, len(slotsOf[c.ID])),
		}
		for _, cl := range closuresOf[c.ID] {
			row.Closures = append(row.Closures, closureOf(cl))
		}
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			row.Days = append(row.Days, scheduleDayOf(cal, d, slotsOf[c.ID]))
		}
		for _, sl := range slotsOf[c.ID] {
			bar := barOf(sl, entries, costs, zone)
			// A group work order may be dragged to another active, unblocked
			// member's row of its active group until a time log fixes its
			// member (M119).
			others := []int64{}
			if sl.WorkCenterGroupID != nil && !sl.HasLogs {
				for _, m := range members[*sl.WorkCenterGroupID] {
					if m.ID != c.ID && m.Status == statusActive && m.WorkingState != stateBlocked {
						others = append(others, m.ID)
					}
				}
			}
			bar.MemberIds = &others
			row.Bars = append(row.Bars, bar)
		}
		out.WorkCenters = append(out.WorkCenters, row)
	}
	for _, u := range unscheduled {
		o := u.ManufacturingOrder
		item := gen.ManufacturingScheduleOrder{
			Id: o.ID, Number: orderNumber(o.Number, o.Suffix), Variant: refOf(entries, o.VariantID, costs),
			Quantity: numericText(o.Quantity, quantityPlaces), Priority: gen.ManufacturingPriority(o.Priority),
			Status: gen.ManufacturingOrderStatus(o.Status), ExpectedMinutes: u.ExpectedMinutes, Revision: o.Revision,
		}
		if d := dateOf(o.DueDate); d != nil {
			item.DueDate = &openapi_types.Date{Time: *d}
		}
		if d := dateOf(o.RequestedStart); d != nil {
			item.RequestedStart = &openapi_types.Date{Time: *d}
		}
		out.Unscheduled = append(out.Unscheduled, item)
	}
	return out, nil
}

// scheduleDayOf is one day of a work center on the board: its intervals,
// why it is closed, its capacity, the planned minutes of the slots that day
// and the load, planned ÷ capacity in per cent at one decimal (M36).
func scheduleDayOf(cal workCalendar, d time.Time, slots []store.ScheduleSlotsRow) gen.ManufacturingScheduleDay {
	open, why := cal.day(d)
	day := gen.ManufacturingScheduleDay{Date: openapi_types.Date{Time: d}, Intervals: make([]gen.ManufacturingScheduleInterval, 0, len(open))}
	if why != closedNone {
		closed := gen.ManufacturingScheduleDayClosed(why)
		day.Closed = &closed
	}
	var capacity, planned time.Duration
	for _, s := range open {
		day.Intervals = append(day.Intervals, gen.ManufacturingScheduleInterval{Start: s.start, End: s.end})
		capacity += s.length()
	}
	whole := span{start: dayStart(d, cal.zone), end: dayStart(d.AddDate(0, 0, 1), cal.zone)}
	for _, sl := range slots {
		planned += overlap(span{start: sl.StartsAt, end: sl.EndsAt}, whole)
	}
	day.CapacityMinutes = int64(capacity / time.Minute)
	day.PlannedMinutes = int64(planned / time.Minute)
	day.Overloaded = planned > capacity
	day.LoadPercent = percentOf(big.NewRat(int64(planned/time.Second), 1), big.NewRat(int64(capacity/time.Second), 1))
	return day
}

// percentOf is part ÷ whole × 100, half-up to one decimal (Rounding and
// codes); nil when whole is 0.
func percentOf(part, whole *big.Rat) *string {
	if whole.Sign() == 0 {
		return nil
	}
	v := new(big.Rat).Quo(part, whole)
	v.Mul(v, hundred)
	t := v.FloatString(percentDisplayPlaces)
	return &t
}

// percentDisplayPlaces is a percentage's decimals at display.
const percentDisplayPlaces = 1

// barOf is one slot as a bar (M36): late when its work order's planned
// finish falls after its order's due date.
func barOf(sl store.ScheduleSlotsRow, entries map[int32]contracts.VariantEntry, costs bool, zone *time.Location) gen.ManufacturingScheduleBar {
	return gen.ManufacturingScheduleBar{
		WorkOrderId: sl.WorkOrderID, WorkOrderRevision: sl.Revision, Sequence: sl.Sequence, Name: sl.Name, WorkCenterGroupId: sl.WorkCenterGroupID,
		Status: gen.ManufacturingWorkOrderStatus(sl.Status), OrderId: sl.OrderID, OrderNumber: orderNumber(sl.OrderNumber, sl.OrderSuffix),
		Variant: refOf(entries, sl.OrderVariantID, costs), Urgent: sl.OrderPriority == priorityUrgent,
		Late:     lateByDays(sl.PlannedFinish, dateOf(sl.OrderDueDate), zone) != nil,
		StartsAt: sl.StartsAt, EndsAt: sl.EndsAt, PlannedStart: sl.PlannedStart, PlannedFinish: sl.PlannedFinish,
	}
}
