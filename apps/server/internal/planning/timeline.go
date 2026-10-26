package planning

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// A person's timeline (design S12, §13 amendment 2): forward by default, the
// employment, the days away, the allocations and their flags to
// planning:view; to the person themselves, holding planning:access alone,
// their confirmed rows and nothing else — the reads that give employment,
// days away and certificates need staff:view, which they do not hold.

const (
	// timelineDefaultDays is the timeline's window when the request names
	// no to: today and the twenty-six weeks after it.
	timelineDefaultDays = 26 * 7
	// timelineMaxDays is how far after from the timeline's to may lie: 104
	// weeks (S12).
	timelineMaxDays = 104 * 7
)

// certificateKind is the kind of a held competence that expires (Staff's
// competences): a certificate, whose validTo against planned work is S12's
// flag.
const certificateKind = "certificate"

// GetPlanningPersonTimeline A person's timeline
// (GET /api/v1/planning/people/{personId}/timeline)
//
// planning:view reads anyone Staff answers; a caller with planning:access
// alone — or with planning:view but no person answered by Staff, as without
// staff:view — reads their own person in the self shape. Anyone else gets
// the bare 404 an unknown id gets.
func (s *server) GetPlanningPersonTimeline(ctx context.Context, req gen.GetPlanningPersonTimelineRequestObject) (gen.GetPlanningPersonTimelineResponseObject, error) {
	out, err := s.timeline(ctx, req.PersonId, req.Params)
	switch status, problem := readProblem(err); status {
	case http.StatusBadRequest:
		return gen.GetPlanningPersonTimeline400ApplicationProblemPlusJSONResponse(problem), nil
	case http.StatusNotFound:
		return gen.GetPlanningPersonTimeline404Response{}, nil
	case http.StatusServiceUnavailable:
		return gen.GetPlanningPersonTimeline503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningPersonTimeline200JSONResponse(out), nil
}

// timeline builds GET /people/{personId}/timeline's answer.
func (s *server) timeline(ctx context.Context, personID int64, p gen.GetPlanningPersonTimelineParams) (gen.PlanningTimeline, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningTimeline{}, err
	}
	from, to, f := windowOf(p.From, p.To, c.Today, func(from time.Time) time.Time { return addDays(from, timelineDefaultDays) })
	if !f.failed() && to.After(addDays(from, timelineMaxDays)) {
		f.add("to", "The timeline answers at most 104 weeks after from")
	}
	if f.failed() {
		return gen.PlanningTimeline{}, refuseInvalid(invalidQueryTitle, f)
	}
	if !c.View {
		return s.ownTimeline(ctx, c, personID, from, to)
	}

	w, err := s.loadWindow(ctx, from, to, []int64{personID})
	if err != nil {
		return gen.PlanningTimeline{}, windowOrInvalid(err, from, c.Today)
	}
	if len(w.People) == 0 {
		// Staff answers nobody to a planner without staff:view: their own
		// person is still theirs to read, in the self shape (amendment 2).
		return s.ownTimeline(ctx, c, personID, from, to)
	}
	person := w.People[0]
	held, err := s.workforceHeld(ctx, from, []int64{personID})
	if err != nil {
		return gen.PlanningTimeline{}, err
	}
	rows := w.rowsOf(personID)
	projects, err := s.projectsOfRows(ctx, rows)
	if err != nil {
		return gen.PlanningTimeline{}, err
	}
	flags := []gen.PlanningTimelineFlag{}
	for _, a := range rows {
		if day, ok := beyondEmployment(a, person.Employed, to); ok {
			flags = append(flags, gen.PlanningTimelineFlag{AllocationId: a.ID, Code: gen.PlanningTimelineFlagCodeBeyondEmployment, Date: ptrTo(apiDate(day))})
		}
		for _, h := range expiringCertificates(a, held[personID]) {
			flags = append(flags, gen.PlanningTimelineFlag{AllocationId: a.ID, Code: gen.PlanningTimelineFlagCodeCertificateExpires, Date: ptrTo(apiDate(*h.ValidTo))})
		}
	}
	return gen.PlanningTimeline{
		Person:      personView(person),
		Employed:    spansView(person.Employed),
		Away:        spansView(w.Capacity[personID].Away),
		Allocations: allocationsView(rows, peopleByID(w.People), projects, c.Manage),
		Flags:       flags,
	}, nil
}

// ownTimeline is the timeline of a caller holding planning:access alone
// (§13 amendment 2): their own person as Self answers it, their confirmed
// rows of the window without the note, and no employment, days away or
// flags. Another person's id is the bare 404.
func (s *server) ownTimeline(ctx context.Context, c *callerContext, personID int64, from, to time.Time) (gen.PlanningTimeline, error) {
	self, err := s.callerSelf(ctx, c)
	if err != nil {
		return gen.PlanningTimeline{}, err
	}
	if self == nil || self.ID != personID {
		return gen.PlanningTimeline{}, notFound()
	}
	rows, err := store.New(s.deps.Pool).AllocationsInWindow(ctx, store.AllocationsInWindowParams{
		FromDate: dayParam(from), ToDate: dayParam(to), PersonIds: []int64{personID},
	})
	if err != nil {
		return gen.PlanningTimeline{}, fmt.Errorf("planning: read the timeline of person %d: %w", personID, err)
	}
	rows = slices.DeleteFunc(rows, func(a store.PlanningAllocation) bool { return a.Status != statusConfirmed })
	projects, err := s.projectsOfRows(ctx, rows)
	if err != nil {
		return gen.PlanningTimeline{}, err
	}
	person := *self
	person.Employed = nil
	return gen.PlanningTimeline{
		Person:      personView(person),
		Employed:    []gen.PlanningDateSpan{},
		Away:        []gen.PlanningDateSpan{},
		Allocations: allocationsView(rows, map[int64]*contracts.PersonEntry{personID: &person}, projects, false),
		Flags:       []gen.PlanningTimelineFlag{},
	}, nil
}

// beyondEmployment is S12's beyond_employment flag on a row (rule 5): when
// the person's employment, as Staff answers it for the window ending on to,
// ends before to and the row runs past that last employed day, the flag and
// the employment's end — the day the flag is about, as the contract names
// it. An employment that reaches to may go on beyond it, so no row is judged
// past it.
func beyondEmployment(a store.PlanningAllocation, employed []contracts.DateSpan, to time.Time) (time.Time, bool) {
	if len(employed) == 0 {
		return time.Time{}, false
	}
	last := employed[0].To
	for _, span := range employed[1:] {
		if span.To.After(last) {
			last = span.To
		}
	}
	if !last.Before(to) || !civil(a.ToDate).After(last) {
		return time.Time{}, false
	}
	return last, true
}

// expiringCertificates is the held certificates whose validTo falls inside
// the row's period (S12's certificate_expires), by validTo.
func expiringCertificates(a store.PlanningAllocation, held []contracts.HeldCompetence) []contracts.HeldCompetence {
	var out []contracts.HeldCompetence
	for _, h := range held {
		if h.Kind != certificateKind || h.ValidTo == nil {
			continue
		}
		if !h.ValidTo.Before(civil(a.FromDate)) && !h.ValidTo.After(civil(a.ToDate)) {
			out = append(out, h)
		}
	}
	slices.SortFunc(out, func(x, y contracts.HeldCompetence) int { return x.ValidTo.Compare(*y.ValidTo) })
	return out
}
