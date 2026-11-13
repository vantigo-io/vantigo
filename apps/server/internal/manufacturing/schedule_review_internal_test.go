package manufacturing

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// naiveFill is the fill as it was first written — every day's open
// intervals against every booking — kept as the reference the fast fill must
// agree with.
func naiveFill(c workCalendar, from time.Time, need time.Duration, booked []span, until time.Time) ([]span, time.Time, bool) {
	if need <= 0 {
		return nil, from, true
	}
	sorted := slices.Clone(booked)
	slices.SortFunc(sorted, func(a, b span) int { return a.start.Compare(b.start) })
	free := func(open []span) []span {
		var out []span
		for _, o := range open {
			cur := o.start
			for _, b := range sorted {
				if !b.start.Before(o.end) {
					break
				}
				if !b.end.After(cur) {
					continue
				}
				if b.start.After(cur) {
					out = append(out, span{start: cur, end: b.start})
				}
				cur = b.end
				if !cur.Before(o.end) {
					break
				}
			}
			if cur.Before(o.end) {
				out = append(out, span{start: cur, end: o.end})
			}
		}
		return out
	}
	last := holidays.DayIn(until, c.zone)
	var pieces []span
	for d := holidays.DayIn(from, c.zone); !d.After(last); d = d.AddDate(0, 0, 1) {
		open, _ := c.day(d)
		for _, f := range free(after(open, from)) {
			if f.length() >= need {
				piece := span{start: f.start, end: f.start.Add(need)}
				return append(pieces, piece), piece.end, true
			}
			pieces = append(pieces, f)
			need -= f.length()
		}
	}
	return nil, time.Time{}, false
}

// randomBookings is n bookings on the 15-minute grid over the days from
// first, overlapping and touching each other at random, in no order.
func randomBookings(r *rand.Rand, first time.Time, days, n int) []span {
	out := make([]span, 0, n)
	for range n {
		start := first.Add(time.Duration(r.IntN(days*24*4)) * 15 * time.Minute)
		out = append(out, span{start: start, end: start.Add(time.Duration(1+r.IntN(16)) * 15 * time.Minute)})
	}
	return out
}

// TestCalendar_FillAgreesWithTheReference is the fast fill (the review's
// M5) placing exactly what the first fill placed, over random bookings —
// overlapping, touching, unsorted — random starts inside and outside them,
// and needs from a minute to a week.
func TestCalendar_FillAgreesWithTheReference(t *testing.T) {
	t.Parallel()
	cal := testCalendar(t, weekdays(iv("07:00", "11:00"), iv("11:30", "15:00")), [2]time.Time{date(2026, 3, 18), date(2026, 3, 19)})
	r := rand.New(rand.NewPCG(1, 2))
	first := at(2026, time.March, 2, 0, 0)
	for i := range 400 {
		booked := randomBookings(r, first, 30, r.IntN(120))
		from := first.Add(time.Duration(r.IntN(30*24*60)) * time.Minute)
		need := time.Duration(1+r.IntN(5*8*60)) * time.Minute
		until := from.AddDate(0, 0, 10+r.IntN(30))
		gotPieces, gotFinish, gotOK := cal.fill(from, need, booked, until)
		wantPieces, wantFinish, wantOK := naiveFill(cal, from, need, booked, until)
		if gotOK != wantOK || !gotFinish.Equal(wantFinish) || !slices.EqualFunc(gotPieces, wantPieces, func(a, b span) bool {
			return a.start.Equal(b.start) && a.end.Equal(b.end)
		}) {
			t.Fatalf("case %d (from %v, need %v): got %v %v %v, want %v %v %v", i, from.In(oslo), need,
				gotPieces, gotFinish.In(oslo), gotOK, wantPieces, wantFinish.In(oslo), wantOK)
		}
	}
}

// manyBookings is a work center booked solid for most of a year in pieces
// of an hour or so, open work spread between them: what plan all meets on a
// busy center.
func manyBookings(n int) []span {
	r := rand.New(rand.NewPCG(3, 4))
	return randomBookings(r, at(2026, time.March, 2, 0, 0), 300, n)
}

// TestPlanGraph_ManyBookingsStayFast is the review's M5: placing hundreds of
// work orders on a center with tens of thousands of bookings reads each
// booking a bounded number of times per work order, not once per day of the
// horizon, so it finishes well inside a second where the first fill took
// minutes — and books no time twice.
func TestPlanGraph_ManyBookingsStayFast(t *testing.T) {
	t.Parallel()
	cals := map[int64]workCalendar{1: testCalendar(t, weekdays(iv("07:00", "15:00")))}
	booked := map[int64][]span{1: book(nil, manyBookings(20000))}
	start := at(2026, time.March, 2, 7, 0)
	var took time.Duration
	for range 200 {
		began := time.Now()
		got, _, ok := planGraph(cals, booked, []planWork{work(1, 1, 90*time.Minute)}, start, start)
		took += time.Since(began)
		if !ok {
			t.Fatal("no capacity")
		}
		for _, piece := range got[0].pieces {
			for _, b := range booked[1] {
				if piece.start.Before(b.end) && b.start.Before(piece.end) {
					t.Fatalf("piece %v–%v overlaps the booking %v–%v", piece.start, piece.end, b.start, b.end)
				}
			}
		}
		booked[1] = book(booked[1], got[0].pieces)
	}
	// Only the planner is timed, not the overlap check above; the race
	// detector slows it by an order of magnitude, so its limit is wider.
	limit := 5 * time.Second
	if raceDetector {
		limit = 60 * time.Second
	}
	if took > limit {
		t.Errorf("200 work orders on 20,000 bookings took %v", took)
	}
}

func BenchmarkCalendar_FillOnManyBookings(b *testing.B) {
	cal := calendarOf(store.ManufacturingWorkCenter{ID: 1, Pattern: mustPattern(weekdays(iv("07:00", "15:00")))}, nil, oslo)
	booked := book(nil, manyBookings(20000))
	start := at(2026, time.March, 2, 7, 0)
	for b.Loop() {
		if _, _, ok := cal.fill(start, 90*time.Minute, booked, start.AddDate(0, 0, planHorizonDays)); !ok {
			b.Fatal("no capacity")
		}
	}
}

func mustPattern(p gen.ManufacturingWeekPattern) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return b
}

// TestProductiveLogged_CountsTheUnion is the review's M3: two operators on a
// work order at once have done the overlapping time once, so the remaining
// minutes planning places are expected less the union, as the cost counts
// machine time.
func TestProductiveLogged_CountsTheUnion(t *testing.T) {
	t.Parallel()
	wo := int64(7)
	t0 := at(2026, 3, 2, 8, 0)
	end := func(t time.Time) *time.Time { return &t }
	logs := []store.ManufacturingTimeLog{
		{WorkOrderID: &wo, Kind: logProductive, StartedAt: t0, EndedAt: end(t0.Add(time.Hour))},
		{WorkOrderID: &wo, Kind: logProductive, StartedAt: t0.Add(30 * time.Minute), EndedAt: end(t0.Add(90 * time.Minute))},
		{WorkOrderID: &wo, Kind: logProductive, StartedAt: t0.Add(2 * time.Hour)}, // open: counted to now
		{WorkOrderID: &wo, Kind: logDowntime, StartedAt: t0, EndedAt: end(t0.Add(3 * time.Hour))},
	}
	got := productiveLogged(logs, t0.Add(150*time.Minute))
	if got[wo] != 2*time.Hour {
		t.Errorf("logged %v, want 2h (08:00–09:30 and 10:00–10:30)", got[wo])
	}
	w := store.ManufacturingWorkOrder{ID: wo, ExpectedMinutes: 180}
	if r := remaining(w, got); r != time.Hour {
		t.Errorf("remaining %v, want 1h", r)
	}
}

// TestProductionCost_ProducedOnlyWithAnActual is the review's L4: a
// product's cost per unit is its actual total over the output that actual
// paid for, so an order closed without an actual adds no output.
func TestProductionCost_ProducedOnlyWithAnActual(t *testing.T) {
	t.Parallel()
	closed := at(2026, 3, 2, 12, 0)
	withActual := store.ManufacturingOrder{ID: 1, Number: 1, VariantID: 5, Produced: numeric(mustRat("4"), quantityPlaces), ClosedAt: &closed,
		Estimate: marshalCost(storedCost{Total: "80.00"}), Actual: marshalCost(storedCost{Total: "100.00"})}
	without := store.ManufacturingOrder{ID: 2, Number: 2, VariantID: 5, Produced: numeric(mustRat("6"), quantityPlaces), ClosedAt: &closed}
	p := reportPeriod{from: date(2026, 3, 1), to: date(2026, 3, 31), zone: oslo}
	out := productionCostOf([]store.ManufacturingOrder{withActual, without}, map[int32]contracts.VariantEntry{5: goodsEntry(5)}, p, nil)
	if len(out.Groups) != 1 {
		t.Fatalf("groups %+v", out.Groups)
	}
	totals := out.Groups[0].Totals
	if totals.Orders != 2 || totals.Produced != "4" || totals.CostPerUnit == nil || *totals.CostPerUnit != "25.00" {
		t.Errorf("totals %+v (cost per unit %v)", totals, totals.CostPerUnit)
	}
}

func goodsEntry(id int32) contracts.VariantEntry {
	return contracts.VariantEntry{ID: id, SKU: "BIKE", ProductName: "Bicycle"}
}

// TestUtilisation_ArchivedCenterNeedsTime is the review's L5: an archived
// work center's calendar is no reason for a row; it keeps one only for the
// time planned or logged at it.
func TestUtilisation_ArchivedCenterNeedsTime(t *testing.T) {
	t.Parallel()
	pattern := mustPattern(weekdays(iv("07:00", "15:00")))
	archived := store.ManufacturingWorkCenter{ID: 1, Code: "OLD", Status: statusArchived, Pattern: pattern}
	active := store.ManufacturingWorkCenter{ID: 2, Code: "NEW", Status: statusActive, Pattern: pattern}
	p := reportPeriod{from: date(2026, 3, 2), to: date(2026, 3, 8), zone: oslo, now: at(2026, 3, 9, 0, 0)}
	p.window = span{start: dayStart(p.from, oslo), end: dayStart(p.to.AddDate(0, 0, 1), oslo)}
	out := utilisationOf([]store.ManufacturingWorkCenter{archived, active}, nil, nil, nil, p, nil)
	if len(out.Rows) != 1 || out.Rows[0].WorkCenter.Id != active.ID {
		t.Fatalf("rows %+v, want the active center's only", out.Rows)
	}
	slots := []store.SlotsAtWorkCentersBetweenRow{{WorkCenterID: archived.ID, StartsAt: at(2026, 3, 3, 7, 0), EndsAt: at(2026, 3, 3, 8, 0)}}
	out = utilisationOf([]store.ManufacturingWorkCenter{archived, active}, nil, slots, nil, p, nil)
	if len(out.Rows) != 2 || out.Rows[0].PlannedMinutes != 60 {
		t.Errorf("rows %+v, want the archived center's planned hour too", out.Rows)
	}
}

// TestRescheduleAnswerOf_KeepsTheUnavailable is the review's L7: when the
// work orders cannot be read back after a reschedule because a module is
// unavailable, the answer is that 503, not a 404.
func TestRescheduleAnswerOf_KeepsTheUnavailable(t *testing.T) {
	t.Parallel()
	unavailableTitle := "Products is unavailable"
	got := rescheduleAnswerOf(gen.ListManufacturingOrderWorkOrders503ApplicationProblemPlusJSONResponse{Title: &unavailableTitle})
	if p, ok := got.(gen.RescheduleManufacturingWorkOrder503ApplicationProblemPlusJSONResponse); !ok || p.Title == nil || *p.Title != "Products is unavailable" {
		t.Errorf("answer %T %+v", got, got)
	}
	if _, ok := rescheduleAnswerOf(gen.ListManufacturingOrderWorkOrders200JSONResponse{}).(gen.RescheduleManufacturingWorkOrder200JSONResponse); !ok {
		t.Error("the list is not answered")
	}
	if _, ok := rescheduleAnswerOf(gen.ListManufacturingOrderWorkOrders404Response{}).(gen.RescheduleManufacturingWorkOrder404Response); !ok {
		t.Error("a 404 is not answered")
	}
}
