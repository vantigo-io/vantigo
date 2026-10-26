package planning

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is the one reader of a window the views share (design S6, S8,
// S23): the people Staff answers for it, their capacity, and the allocations
// touching it — the contract calls first, before any transaction, then the
// module's own rows — and S7's judgement of a write over the same figures.

// window is one read of the plan over From..To: the people Staff answers to
// the caller for it, in Staff's order, their capacity by person id, and every
// allocation of those people touching it, by person, from date and id. A
// person Staff does not answer — not visible to this caller, left, erased —
// is not in it, and neither are their rows (S26: off the board).
type window struct {
	From, To    time.Time
	People      []contracts.PersonEntry
	Capacity    map[int64]contracts.PersonCapacity
	Allocations []store.PlanningAllocation
}

// loadWindow reads the window From..To: Workforce.People and
// Workforce.Capacity through their accessors with the request's context, then
// the allocations of the people answered from the module's own tables. It
// opens no transaction and must not be called inside one — the accessors
// refuse it. personIDs narrows the people to those ids when not nil (a
// timeline, a picker's choice); nil is everyone Staff answers.
// contracts.ErrWindowOutOfBounds is answered as it is, for the caller to
// refuse as a 400 on its window.
func (s *server) loadWindow(ctx context.Context, from, to time.Time, personIDs []int64) (*window, error) {
	people, err := s.workforcePeople(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if personIDs != nil {
		people = slices.DeleteFunc(people, func(p contracts.PersonEntry) bool { return !slices.Contains(personIDs, p.ID) })
	}
	w := &window{From: from, To: to, People: people, Capacity: map[int64]contracts.PersonCapacity{}}
	if len(people) == 0 {
		return w, nil
	}
	ids := make([]int64, len(people))
	for i, p := range people {
		ids[i] = p.ID
	}
	if w.Capacity, err = s.workforceCapacity(ctx, from, to, ids); err != nil {
		return nil, err
	}
	if w.Allocations, err = store.New(s.deps.Pool).AllocationsInWindow(ctx, store.AllocationsInWindowParams{
		FromDate: dayParam(from), ToDate: dayParam(to), PersonIds: ids,
	}); err != nil {
		return nil, fmt.Errorf("planning: read the allocations of %s..%s: %w", from.Format(time.DateOnly), to.Format(time.DateOnly), err)
	}
	return w, nil
}

// rowsOf is personID's allocations in the window, in from-date order.
func (w *window) rowsOf(personID int64) []store.PlanningAllocation {
	var out []store.PlanningAllocation
	for _, a := range w.Allocations {
		if a.PersonID == personID {
			out = append(out, a)
		}
	}
	return out
}

// inputOf is personID's capacity input over the window: Staff's segments and
// away spans, and the person's rows split by status. includeTentative false
// leaves the tentative rows out (S10's Show tentative off); keep, when not
// nil, keeps only the rows it accepts (S10's includePlanned, a project
// filter).
func (w *window) inputOf(personID int64, includeTentative bool, keep func(store.PlanningAllocation) bool) capacityInput {
	c := w.Capacity[personID]
	in := capacityInput{From: w.From, To: w.To, Segments: c.Segments, Away: c.Away}
	for _, a := range w.rowsOf(personID) {
		if keep != nil && !keep(a) {
			continue
		}
		switch a.Status {
		case statusConfirmed:
			in.Confirmed = append(in.Confirmed, rowOf(a))
		case statusTentative:
			if includeTentative {
				in.Tentative = append(in.Tentative, rowOf(a))
			}
		}
	}
	return in
}

// The statuses an allocation is in (S3), as the contract names them.
const (
	statusTentative = "tentative"
	statusConfirmed = "confirmed"
)

// rowOf is an allocation as the capacity calculation reads it.
func rowOf(a store.PlanningAllocation) capacityRow {
	return capacityRow{From: civil(a.FromDate), To: civil(a.ToDate), LoadUnit: a.LoadUnit, Load: loadOf(a.Load)}
}

// overbooking is S7's judgement of one write on one person's confirmed rows,
// over the whole weeks the write touches.
type overbooking struct {
	// Over is the Mondays of the weeks whose confirmed booked exceeds their
	// capacity once the write is made: the warning overbooked.
	Over []time.Time
	// WhileAway is the Mondays of the weeks whose confirmed booked exceeds
	// available but not capacity once the write is made: the warning
	// booked_while_away, never a refusal.
	WhileAway []time.Time
	// Grows is whether any of those weeks' excess over capacity is larger
	// after the write than before it: what the setting refuse refuses. A write
	// that keeps or shrinks an excess already there does not grow it, so a
	// plan can be mended one row at a time.
	Grows bool
}

// overbookingOf judges a write of one person's confirmed row (S7): others are
// the person's other confirmed rows as read under the person's lock — never
// the row being written — previous is the row as it stood before the write
// (nil on a create, or when it was tentative), and candidate the row as the
// write leaves it (nil on a delete, or when it is left tentative). The weeks
// judged are every whole week, Monday to Sunday, either period touches.
//
// capacity is Workforce.Capacity read before the transaction (rule 3) over a
// window covering those weeks — mondayOf the earliest from date to sundayOf
// the latest to date — or the days outside it count as outside employment.
func overbookingOf(capacity contracts.PersonCapacity, others []capacityRow, previous, candidate *capacityRow) overbooking {
	var spans []capacityRow
	for _, r := range []*capacityRow{previous, candidate} {
		if r != nil {
			spans = append(spans, *r)
		}
	}
	if len(spans) == 0 {
		return overbooking{}
	}
	from, to := spans[0].From, spans[0].To
	for _, r := range spans[1:] {
		if r.From.Before(from) {
			from = r.From
		}
		if r.To.After(to) {
			to = r.To
		}
	}
	from, to = mondayOf(from), sundayOf(to)

	weeks := func(write *capacityRow) []weekFigures {
		rows := slices.Clone(others)
		if write != nil {
			rows = append(rows, *write)
		}
		return capacityWeeks(capacityInput{From: from, To: to, Segments: capacity.Segments, Away: capacity.Away, Confirmed: rows})
	}
	before, after := weeks(previous), weeks(candidate)

	var out overbooking
	for i, w := range after {
		booked, capacityH, available := hundredths(w.Booked), hundredths(w.Capacity), hundredths(w.Available)
		switch {
		case booked > capacityH:
			out.Over = append(out.Over, w.Monday)
		case booked > available:
			out.WhileAway = append(out.WhileAway, w.Monday)
		}
		if excess(after[i]) > excess(before[i]) {
			out.Grows = true
		}
	}
	return out
}

// excess is how many hundredths of an hour a week's confirmed booked exceeds
// its capacity by, 0 when it does not.
func excess(w weekFigures) int64 {
	return max(0, hundredths(w.Booked)-hundredths(w.Capacity))
}

// hundredths is an hour figure in hundredths of an hour, as an integer, so
// two figures compare exactly.
func hundredths(v float64) int64 { return int64(math.Round(v * 100)) }
