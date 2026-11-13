package manufacturing

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

var oslo = mustZone("Europe/Oslo")

func mustZone(name string) *time.Location {
	z, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return z
}

// at is a wall time in Oslo.
func at(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, oslo) }

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func iv(start, end string) gen.ManufacturingInterval {
	return gen.ManufacturingInterval{Start: start, End: end}
}

// weekdays is a pattern with the same intervals Monday to Friday.
func weekdays(day ...gen.ManufacturingInterval) gen.ManufacturingWeekPattern {
	return gen.ManufacturingWeekPattern{Monday: day, Tuesday: day, Wednesday: day, Thursday: day, Friday: day,
		Saturday: []gen.ManufacturingInterval{}, Sunday: []gen.ManufacturingInterval{}}
}

func testCalendar(t *testing.T, p gen.ManufacturingWeekPattern, closures ...[2]time.Time) workCalendar {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.ManufacturingWorkCenterClosure
	for _, c := range closures {
		rows = append(rows, store.ManufacturingWorkCenterClosure{FromDate: pgtype.Date{Time: c[0], Valid: true}, ToDate: pgtype.Date{Time: c[1], Valid: true}})
	}
	return calendarOf(store.ManufacturingWorkCenter{ID: 1, Pattern: b}, rows, oslo)
}

func wantPieces(t *testing.T, got []span, want ...span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pieces %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].start.Equal(want[i].start) || !got[i].end.Equal(want[i].end) {
			t.Fatalf("piece %d is %v–%v, want %v–%v", i, got[i].start.In(oslo), got[i].end.In(oslo), want[i].start.In(oslo), want[i].end.In(oslo))
		}
	}
}

// TestCalendar_FillAcrossLunchGap is M35a's pieces: a work order fills the
// open intervals in order, around the lunch break.
func TestCalendar_FillAcrossLunchGap(t *testing.T) {
	t.Parallel()
	cal := testCalendar(t, weekdays(iv("07:00", "11:00"), iv("11:30", "15:00")))
	from := at(2026, time.March, 2, 7, 0) // a Monday
	pieces, finish, ok := cal.fill(from, 300*time.Minute, nil, from.AddDate(0, 0, planHorizonDays))
	if !ok {
		t.Fatal("no capacity")
	}
	wantPieces(t, pieces, span{at(2026, 3, 2, 7, 0), at(2026, 3, 2, 11, 0)}, span{at(2026, 3, 2, 11, 30), at(2026, 3, 2, 12, 30)})
	if !finish.Equal(at(2026, 3, 2, 12, 30)) {
		t.Errorf("finish %v", finish.In(oslo))
	}
}

// TestCalendar_FillSkipsHolidayAndClosure is M9: Ascension Day 2026
// (Thursday 14 May) is a public holiday and the Friday a closure, so the
// work goes on the next Monday.
func TestCalendar_FillSkipsHolidayAndClosure(t *testing.T) {
	t.Parallel()
	cal := testCalendar(t, weekdays(iv("07:00", "15:00")), [2]time.Time{date(2026, 5, 15), date(2026, 5, 15)})
	from := at(2026, time.May, 13, 14, 0)
	pieces, _, ok := cal.fill(from, 2*time.Hour, nil, from.AddDate(0, 0, planHorizonDays))
	if !ok {
		t.Fatal("no capacity")
	}
	wantPieces(t, pieces, span{at(2026, 5, 13, 14, 0), at(2026, 5, 13, 15, 0)}, span{at(2026, 5, 18, 7, 0), at(2026, 5, 18, 8, 0)})
	if _, why := cal.day(date(2026, 5, 14)); why != closedHoliday {
		t.Errorf("14 May is %q, want holiday", why)
	}
	if _, why := cal.day(date(2026, 5, 15)); why != closedClosure {
		t.Errorf("15 May is %q, want closure", why)
	}
}

// TestCalendar_FillAroundBookings is M35a: free time is the open intervals
// less what other work orders booked; a piece goes around a booking.
func TestCalendar_FillAroundBookings(t *testing.T) {
	t.Parallel()
	cal := testCalendar(t, weekdays(iv("07:00", "15:00")))
	booked := []span{{at(2026, 3, 2, 8, 0), at(2026, 3, 2, 10, 0)}, {at(2026, 3, 2, 9, 0), at(2026, 3, 2, 11, 0)}}
	from := at(2026, time.March, 2, 7, 0)
	pieces, _, ok := cal.fill(from, 3*time.Hour, booked, from.AddDate(0, 0, planHorizonDays))
	if !ok {
		t.Fatal("no capacity")
	}
	wantPieces(t, pieces, span{at(2026, 3, 2, 7, 0), at(2026, 3, 2, 8, 0)}, span{at(2026, 3, 2, 11, 0), at(2026, 3, 2, 13, 0)})
}

// TestCalendar_DaylightSaving is §11: the pattern is local wall time, so a
// day's intervals move in UTC across a clock change, and a whole-day pattern
// has 23 hours on the spring day and 25 on the autumn day.
func TestCalendar_DaylightSaving(t *testing.T) {
	t.Parallel()
	whole := []gen.ManufacturingInterval{iv("00:00", "24:00")}
	cal := testCalendar(t, gen.ManufacturingWeekPattern{Monday: []gen.ManufacturingInterval{iv("07:00", "15:00")},
		Friday: []gen.ManufacturingInterval{iv("07:00", "15:00")}, Sunday: whole})
	if got := cal.capacity(date(2026, 3, 29)); got != 23*time.Hour {
		t.Errorf("29 March has %v, want 23h", got)
	}
	if got := cal.capacity(date(2026, 10, 25)); got != 25*time.Hour {
		t.Errorf("25 October has %v, want 25h", got)
	}
	friday, _ := cal.day(date(2026, 3, 27))
	monday, _ := cal.day(date(2026, 3, 30))
	if friday[0].start.Hour() != 6 || monday[0].start.Hour() != 5 {
		t.Errorf("07:00 is %v UTC on the Friday and %v UTC on the Monday, want 06 and 05", friday[0].start, monday[0].start)
	}
	// A fill across the night of the change counts real time.
	from := at(2026, time.March, 28, 23, 0)
	_, finish, ok := cal.fill(from, 3*time.Hour, nil, from.AddDate(0, 0, planHorizonDays))
	if !ok || !finish.Equal(at(2026, 3, 29, 4, 0)) {
		t.Errorf("finish %v (ok %v), want 04:00 on the 29th: from 00:00 Sunday, three real hours with 02:00–03:00 skipped", finish.In(oslo), ok)
	}
}

// TestCalendar_NoCapacity is M35's horizon: a calendar with no open day
// finds nothing within 365 days.
func TestCalendar_NoCapacity(t *testing.T) {
	t.Parallel()
	cal := testCalendar(t, gen.ManufacturingWeekPattern{})
	from := at(2026, time.March, 2, 7, 0)
	if _, _, ok := cal.fill(from, time.Minute, nil, from.AddDate(0, 0, planHorizonDays)); ok {
		t.Fatal("a closed calendar had room")
	}
}

// TestPlanChain_PredecessorAndInProgress is M35 and M35a: each work order
// starts after its predecessor's finish, on its own center; a running one
// plans from now.
func TestPlanChain_PredecessorAndInProgress(t *testing.T) {
	t.Parallel()
	cals := map[int64]workCalendar{1: testCalendar(t, weekdays(iv("07:00", "15:00"))), 2: testCalendar(t, weekdays(iv("07:00", "15:00")))}
	start := at(2026, time.March, 2, 7, 0)
	booked := map[int64][]span{2: {{at(2026, 3, 2, 9, 0), at(2026, 3, 2, 12, 0)}}}
	works := []planWork{work(10, 1, 2*time.Hour), work(11, 2, 4*time.Hour, 10)}
	got, _, ok := planGraph(cals, booked, works, start, start)
	if !ok {
		t.Fatal("no capacity")
	}
	if !got[0].finish.Equal(at(2026, 3, 2, 9, 0)) {
		t.Errorf("first finishes %v", got[0].finish.In(oslo))
	}
	// The second may not start before 09:00, and 09:00–12:00 is booked.
	wantPieces(t, got[1].pieces, span{at(2026, 3, 2, 12, 0), at(2026, 3, 2, 15, 0)}, span{at(2026, 3, 3, 7, 0), at(2026, 3, 3, 8, 0)})
	if len(booked[2]) != 1 {
		t.Error("planGraph wrote to booked")
	}

	now := at(2026, time.March, 2, 10, 0)
	later := at(2026, time.March, 4, 7, 0)
	running := work(10, 1, time.Hour)
	running.fromNow = true
	got, _, ok = planGraph(cals, nil, []planWork{running, work(11, 2, time.Hour, 10)}, later, now)
	if !ok || !got[0].start.Equal(now) || !got[1].start.Equal(later) {
		t.Errorf("running work order at %v, next at %v; want now and the requested start", got[0].start.In(oslo), got[1].start.In(oslo))
	}
}

// TestReschedule_MovesOverlappingSuccessors is M36: the moved work order
// takes the first free time from the drop; the later one moves after it
// only when it would overlap.
func TestReschedule_MovesOverlappingSuccessors(t *testing.T) {
	t.Parallel()
	cals := map[int64]workCalendar{1: testCalendar(t, weekdays(iv("07:00", "15:00")))}
	now := at(2026, time.March, 2, 6, 0)
	target, next := work(1, 1, 2*time.Hour), work(2, 1, time.Hour, 1)
	nextStart, nextFinish := at(2026, 3, 2, 9, 0), at(2026, 3, 2, 10, 0)
	existing := map[int64]placement{2: {id: 2, center: 1, pieces: []span{{nextStart, nextFinish}}, start: nextStart, finish: nextFinish, option: next.options[0]}}
	works := map[int64]planWork{1: target, 2: next}
	got, _, ok := reschedule(cals, nil, target, target.options[0], []planWork{next}, existing, works, at(2026, 3, 2, 13, 0), now)
	if !ok || len(got) != 2 {
		t.Fatalf("placements %v", got)
	}
	wantPieces(t, got[0].pieces, span{at(2026, 3, 2, 13, 0), at(2026, 3, 2, 15, 0)})
	wantPieces(t, got[1].pieces, span{at(2026, 3, 3, 7, 0), at(2026, 3, 3, 8, 0)})

	// Dropped early, the later one stays where it is, and the moved one goes
	// around it.
	got, _, ok = reschedule(cals, nil, target, target.options[0], []planWork{next}, existing, works, at(2026, 3, 2, 7, 0), now)
	if !ok || len(got) != 1 {
		t.Fatalf("placements %v", got)
	}
	wantPieces(t, got[0].pieces, span{at(2026, 3, 2, 7, 0), at(2026, 3, 2, 9, 0)})
}

// TestReschedule_SuccessorsKeepClearOfEachOther is M36 with two successors
// at one center: the one that moves goes around the one that stays, and a
// successor the moved work order lands on moves.
func TestReschedule_SuccessorsKeepClearOfEachOther(t *testing.T) {
	t.Parallel()
	cals := map[int64]workCalendar{1: testCalendar(t, weekdays(iv("07:00", "15:00"))), 2: testCalendar(t, weekdays(iv("07:00", "15:00")))}
	now := at(2026, time.March, 2, 6, 0)
	target, s1, s2 := work(1, 1, time.Hour), work(2, 2, 2*time.Hour, 1), work(3, 2, time.Hour, 1)
	planned := func(w planWork, start, finish time.Time) placement {
		return placement{id: w.id, center: w.options[0].center, pieces: []span{{start, finish}}, start: start, finish: finish, option: w.options[0]}
	}
	existing := map[int64]placement{
		1: planned(target, at(2026, 3, 2, 8, 0), at(2026, 3, 2, 9, 0)),
		2: planned(s1, at(2026, 3, 2, 9, 0), at(2026, 3, 2, 11, 0)),
		3: planned(s2, at(2026, 3, 2, 12, 0), at(2026, 3, 2, 13, 0)),
	}
	works := map[int64]planWork{1: target, 2: s1, 3: s2}
	got, _, ok := reschedule(cals, nil, target, target.options[0], []planWork{s1, s2}, existing, works, at(2026, 3, 2, 10, 0), now)
	if !ok || len(got) != 2 {
		t.Fatalf("placements %v", got)
	}
	wantPieces(t, got[0].pieces, span{at(2026, 3, 2, 10, 0), at(2026, 3, 2, 11, 0)})
	// S2 keeps 12:00–13:00, so S1 runs 11:00–12:00 and 13:00–14:00.
	wantPieces(t, got[1].pieces, span{at(2026, 3, 2, 11, 0), at(2026, 3, 2, 12, 0)}, span{at(2026, 3, 2, 13, 0), at(2026, 3, 2, 14, 0)})

	// A send-ahead successor at the target's own center that the target
	// lands on moves, though its bounds hold.
	ahead := work(1, 2, time.Hour)
	ahead.sendAhead = big.NewRat(1, 1)
	ahead.aheadMet = true
	next := work(2, 2, time.Hour, 1)
	existing = map[int64]placement{2: planned(next, at(2026, 3, 2, 10, 0), at(2026, 3, 2, 11, 0))}
	works = map[int64]planWork{1: ahead, 2: next}
	got, _, ok = reschedule(cals, nil, ahead, ahead.options[0], []planWork{next}, existing, works, at(2026, 3, 2, 10, 0), now)
	if !ok || len(got) != 2 {
		t.Fatalf("placements %v", got)
	}
	if overlapsAny(got[0].pieces, got[1].pieces) {
		t.Errorf("the target %v overlaps its successor %v", got[0].pieces, got[1].pieces)
	}
}

// work is a planWork at center needing need there, after preds, with a
// minute a cycle at full efficiency.
func work(id, center int64, need time.Duration, preds ...int64) planWork {
	return planWork{id: id, preds: preds, output: big.NewRat(1, 1), options: []planOption{{
		center: center, need: need, efficiency: 100, setup: new(big.Rat), perCycle: big.NewRat(1, 1), capacity: 1,
	}}}
}

// TestDefaultRequestedStart is M35a's default: the due date less ⌈minutes /
// 450⌉ business days less one, clamped to today.
func TestDefaultRequestedStart(t *testing.T) {
	t.Parallel()
	due := date(2026, 5, 18) // a Monday; 14 May is Ascension Day
	if got := defaultRequestedStart(&due, 900, date(2026, 5, 1)); !got.Equal(date(2026, 5, 12)) {
		t.Errorf("got %v, want 12 May: two days' work and one of slack, skipping the weekend and Ascension Day", got)
	}
	if got := defaultRequestedStart(&due, 900, date(2026, 5, 15)); !got.Equal(date(2026, 5, 15)) {
		t.Errorf("got %v, want today", got)
	}
	if got := defaultRequestedStart(nil, 900, date(2026, 5, 15)); !got.Equal(date(2026, 5, 15)) {
		t.Errorf("got %v, want today without a due date", got)
	}
}

// TestLateByDays is M35's flag: the finish's day in the zone after the due
// date; a finish at midnight belongs to the day it ends.
func TestLateByDays(t *testing.T) {
	t.Parallel()
	due := date(2026, 3, 2)
	for _, tc := range []struct {
		finish time.Time
		want   int32
	}{{at(2026, 3, 2, 15, 0), 0}, {at(2026, 3, 3, 0, 0), 0}, {at(2026, 3, 4, 8, 0), 2}} {
		got := lateByDays(&tc.finish, &due, oslo)
		if (got == nil && tc.want != 0) || (got != nil && *got != tc.want) {
			t.Errorf("finish %v: late by %v, want %d", tc.finish, got, tc.want)
		}
	}
}

func money(s string) pgtype.Numeric { return numeric(mustRat(s), 2) }

// TestCost_EstimateAndActual is M39 and M40: the estimate from the
// snapshots; the actual with two operators in parallel — labour counts both,
// machine the union — and downtime apart.
func TestCost_EstimateAndActual(t *testing.T) {
	t.Parallel()
	components := []store.ManufacturingOrderComponent{
		{Required: numeric(mustRat("2"), 3), Consumed: numeric(mustRat("2"), 3), Scrapped: numeric(mustRat("1"), 3), StandardCost: money("150.50")},
		{Required: numeric(mustRat("1"), 3)}, // no standard cost: incomplete
	}
	wos := []store.ManufacturingWorkOrder{{ID: 1, ExpectedMinutes: 90, MachineRate: money("600"), LabourRate: money("500")}}
	e := estimateOf(components, wos)
	if amountText(e.components) != "301.00" || amountText(e.machine) != "900.00" || amountText(e.labour) != "750.00" || !e.incomplete {
		t.Errorf("estimate %s %s %s %v", amountText(e.components), amountText(e.machine), amountText(e.labour), e.incomplete)
	}
	wo := int64(1)
	reason := "breakdown"
	end := func(t time.Time) *time.Time { return &t }
	t0 := at(2026, 3, 2, 8, 0)
	logs := []store.ManufacturingTimeLog{
		{WorkOrderID: &wo, Kind: logProductive, StartedAt: t0, EndedAt: end(t0.Add(time.Hour))},
		{WorkOrderID: &wo, Kind: logProductive, StartedAt: t0.Add(30 * time.Minute), EndedAt: end(t0.Add(90 * time.Minute))},
		{WorkOrderID: &wo, Kind: logDowntime, Reason: &reason, StartedAt: t0.Add(2 * time.Hour), EndedAt: end(t0.Add(150 * time.Minute))},
	}
	a, stopped := actualOf(components, wos, logs, t0.Add(3*time.Hour))
	// components 3 × 150.50; machine 1.5 h × 600; labour 2 h × 500.
	if amountText(a.components) != "451.50" || amountText(a.machine) != "900.00" || amountText(a.labour) != "1000.00" {
		t.Errorf("actual %s %s %s", amountText(a.components), amountText(a.machine), amountText(a.labour))
	}
	if stopped[reason] != 30*time.Minute {
		t.Errorf("downtime %v", stopped)
	}
	if got := costPerUnitOf(mustRat("100"), mustRat("3")); got == nil || *got != "33.33" {
		t.Errorf("cost per unit %v", got)
	}
	if got := deviationPart("110.00", "100.00"); got.Amount != "10.00" || *got.Percent != "10.0" {
		t.Errorf("deviation %+v", got)
	}
	if got := percentOf(big.NewRat(1, 1), new(big.Rat)); got != nil {
		t.Error("a percent of 0")
	}
}
