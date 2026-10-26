package planning

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestOverbookingOf is S7's judgement of a write, case by case, on a person at
// 37.5 hours a week: the weeks over capacity, the weeks booked while away, and
// whether the excess grows — what the setting refuse refuses.
func TestOverbookingOf(t *testing.T) {
	full := contracts.PersonCapacity{Segments: fullTime}
	away := contracts.PersonCapacity{Segments: fullTime, Away: []contracts.DateSpan{{From: day("2026-10-21"), To: day("2026-10-21")}}}
	week := func(start, end string, unit string, load float64) capacityRow {
		return capacityRow{From: day(start), To: day(end), LoadUnit: unit, Load: load}
	}
	all43 := week(week43, "2026-10-25", loadUnitPercent, 100)
	both := week(week43, "2026-11-01", loadUnitPercent, 100)
	row := func(r capacityRow) *capacityRow { return &r }

	cases := []struct {
		name            string
		capacity        contracts.PersonCapacity
		others          []capacityRow
		previous, write *capacityRow
		over, whileAway []string
		grows           bool
	}{
		{name: "a full week at full time is not over", capacity: full, write: row(all43)},
		{
			name: "twenty hours on top of a full week is over and grows", capacity: full,
			others: []capacityRow{all43}, write: row(week(week43, "2026-10-25", loadUnitHours, 20)),
			over: []string{week43}, grows: true,
		},
		{
			name: "a full week with a day away is booked while away, not over", capacity: away,
			write: row(all43), whileAway: []string{week43},
		},
		{
			name: "shrinking an excess is over but does not grow", capacity: full,
			others: []capacityRow{all43}, previous: row(week(week43, "2026-10-25", loadUnitHours, 30)),
			write: row(week(week43, "2026-10-25", loadUnitHours, 10)), over: []string{week43},
		},
		{
			name: "keeping an excess as it is does not grow", capacity: full,
			others: []capacityRow{all43}, previous: row(week(week43, "2026-10-25", loadUnitHours, 10)),
			write: row(week(week43, "2026-10-25", loadUnitHours, 10)), over: []string{week43},
		},
		{
			name: "moving an excess to a week with none grows it there", capacity: full,
			others: []capacityRow{both}, previous: row(week(week43, "2026-10-25", loadUnitHours, 20)),
			write: row(week(week44, "2026-11-01", loadUnitHours, 20)), over: []string{week44}, grows: true,
		},
		{
			name: "a delete never grows", capacity: full,
			others: []capacityRow{all43}, previous: row(week(week43, "2026-10-25", loadUnitHours, 20)),
		},
		{
			name: "a row over a weekend only books nothing", capacity: full,
			others: []capacityRow{all43}, write: row(week("2026-10-24", "2026-10-25", loadUnitHours, 40)),
		},
		{name: "no row at all judges nothing", capacity: full, others: []capacityRow{all43}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := overbookingOf(tc.capacity, tc.others, tc.previous, tc.write)
			if !slices.Equal(mondays(got.Over), tc.over) || !slices.Equal(mondays(got.WhileAway), tc.whileAway) || got.Grows != tc.grows {
				t.Errorf("overbookingOf = over %v, while away %v, grows %v; want over %v, while away %v, grows %v",
					mondays(got.Over), mondays(got.WhileAway), got.Grows, tc.over, tc.whileAway, tc.grows)
			}
		})
	}
}

func mondays(days []time.Time) []string {
	var out []string
	for _, d := range days {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

// windowWorkforce is contracts.Workforce answering a fixed register: the
// people People answers and each one's capacity. It records the person ids
// Capacity was asked about.
type windowWorkforce struct {
	contracts.Workforce
	people   []contracts.PersonEntry
	capacity map[int64]contracts.PersonCapacity
	asked    [][]int64
}

func (w *windowWorkforce) People(context.Context, time.Time, time.Time) ([]contracts.PersonEntry, error) {
	return slices.Clone(w.people), nil
}

func (w *windowWorkforce) Capacity(_ context.Context, _, _ time.Time, ids []int64) (map[int64]contracts.PersonCapacity, error) {
	w.asked = append(w.asked, slices.Clone(ids))
	out := map[int64]contracts.PersonCapacity{}
	for _, id := range ids {
		if c, ok := w.capacity[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

// TestLoadWindow is the one reader of a window: the people Staff answers, their
// capacity asked for exactly them, and the allocations of exactly them
// touching the window — a row of a person Staff does not answer, and a row
// outside the window, left out.
func TestLoadWindow(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	workforce := &windowWorkforce{
		people: []contracts.PersonEntry{{ID: 1001, DisplayName: "Kari Nordmann"}, {ID: 1002, DisplayName: "Ola Nordmann"}},
		capacity: map[int64]contracts.PersonCapacity{
			1001: {Segments: fullTime},
			1002: {Segments: fullTime, Away: []contracts.DateSpan{{From: day("2026-10-21"), To: day("2026-10-21")}}},
		},
	}
	s := &server{deps: module.Deps{Pool: pool, Workforce: workforce}}

	insert := func(person int64, from, to, status string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO planning.allocations (person_id, project_id, from_date, to_date, load_unit, load, status, created_at, updated_at)
			VALUES ($1, 2001, $2, $3, 'percent', 50, $4, now(), now()) RETURNING id`, person, from, to, status).Scan(&id); err != nil {
			t.Fatalf("seed an allocation: %v", err)
		}
		return id
	}
	kari := insert(1001, "2026-10-01", "2026-10-21", statusConfirmed)
	olaTentative := insert(1002, "2026-10-23", "2026-11-30", statusTentative)
	olaConfirmed := insert(1002, "2026-10-19", "2026-10-25", statusConfirmed)
	insert(1003, "2026-10-19", "2026-10-25", statusConfirmed) // Staff does not answer 1003
	insert(1001, "2026-11-02", "2026-11-08", statusConfirmed) // after the window

	w, err := s.loadWindow(ctx, day(week43), day("2026-10-25"), nil)
	if err != nil {
		t.Fatalf("loadWindow: %v", err)
	}
	if got := allocationIDs(w.Allocations); !slices.Equal(got, []int64{kari, olaConfirmed, olaTentative}) {
		t.Errorf("allocations = %v, want Kari's %d then Ola's %d and %d", got, kari, olaConfirmed, olaTentative)
	}
	if len(w.People) != 2 || len(w.Capacity) != 2 || !slices.Equal(workforce.asked[0], []int64{1001, 1002}) {
		t.Errorf("people %v, capacity %v, Capacity asked about %v; want both people and their capacity", w.People, w.Capacity, workforce.asked)
	}

	confirmedOnly := capacityWeeks(w.inputOf(1002, false, nil))
	withTentative := capacityWeeks(w.inputOf(1002, true, nil))
	if len(confirmedOnly) != 1 || confirmedOnly[0].Booked != 18.75 || confirmedOnly[0].Tentative != 0 || !confirmedOnly[0].Away {
		t.Errorf("Ola's week without tentative = %+v, want 18.75 booked, nothing tentative, away", confirmedOnly)
	}
	// The tentative row covers Friday the 23rd alone of the week: 50 % of 7.5.
	if len(withTentative) != 1 || withTentative[0].Tentative != 3.75 {
		t.Errorf("Ola's week with tentative = %+v, want 3.75 tentative", withTentative)
	}
	kept := capacityWeeks(w.inputOf(1002, true, func(a store.PlanningAllocation) bool { return a.ID != olaConfirmed }))
	if kept[0].Booked != 0 {
		t.Errorf("Ola's week keeping only the tentative row booked %v, want 0", kept[0].Booked)
	}

	narrowed, err := s.loadWindow(ctx, day(week43), day("2026-10-25"), []int64{1002})
	if err != nil {
		t.Fatalf("loadWindow narrowed: %v", err)
	}
	if got := allocationIDs(narrowed.Allocations); len(narrowed.People) != 1 || !slices.Equal(got, []int64{olaConfirmed, olaTentative}) {
		t.Errorf("narrowed to Ola: people %v, allocations %v; want Ola and his two rows", narrowed.People, got)
	}

	workforce.people = nil
	empty, err := s.loadWindow(ctx, day(week43), day("2026-10-25"), nil)
	if err != nil || len(empty.People) != 0 || len(empty.Allocations) != 0 || len(workforce.asked) != 2 {
		t.Errorf("Staff answering nobody: %+v, %v, Capacity asked %d times; want an empty window and no Capacity call", empty, err, len(workforce.asked))
	}
}

func allocationIDs(rows []store.PlanningAllocation) []int64 {
	var out []int64
	for _, a := range rows {
		out = append(out, a.ID)
	}
	return out
}
