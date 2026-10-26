package planning

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The dashboard card's figures (design S22): this week's people planned and
// load, confirmed and tentative apart, over the people Staff answers to the
// caller, the unbooked list's count (S13), and how many requests are open
// (S16) — every open request, since a request names nobody Staff could hide.

// GetPlanningStats Planning stats
// (GET /api/v1/planning/stats)
//
// planning:view.
func (s *server) GetPlanningStats(ctx context.Context, _ gen.GetPlanningStatsRequestObject) (gen.GetPlanningStatsResponseObject, error) {
	out, err := s.stats(ctx)
	if status, problem := readProblem(err); status == http.StatusServiceUnavailable {
		return gen.GetPlanningStats503ApplicationProblemPlusJSONResponse(problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningStats200JSONResponse(out), nil
}

// stats builds GET /stats' answer over one window: this week's Monday to the
// later of its Sunday and the unbooked horizon's end.
func (s *server) stats(ctx context.Context) (gen.PlanningStats, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return gen.PlanningStats{}, err
	}
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.PlanningStats{}, err
	}
	monday, sunday := mondayOf(c.Today), sundayOf(c.Today)
	horizonFrom, horizonTo := horizonOf(c.Today, row)
	end := horizonTo
	if sunday.After(end) {
		end = sunday
	}
	w, err := s.loadWindow(ctx, monday, end, nil)
	if err != nil {
		return gen.PlanningStats{}, err
	}

	planned := map[int64]bool{}
	for _, a := range w.Allocations {
		if a.Status == statusConfirmed && !civil(a.FromDate).After(sunday) && !civil(a.ToDate).Before(monday) {
			planned[a.PersonID] = true
		}
	}
	var capacity, booked, tentative float64
	for _, e := range w.People {
		in := w.inputOf(e.ID, true, nil)
		in.From, in.To = monday, sunday
		for _, week := range capacityWeeks(in) {
			capacity += week.Capacity
			booked += week.Booked
			tentative += week.Tentative
		}
	}
	open, err := store.New(s.deps.Pool).CountOpenRequests(ctx)
	if err != nil {
		return gen.PlanningStats{}, fmt.Errorf("planning: count the open requests: %w", err)
	}
	out := gen.PlanningStats{
		PeoplePlannedThisWeek: int32(len(planned)),
		Unbooked:              int32(len(unbookedIn(w, horizonFrom, horizonTo, row.UnbookedThresholdPercent))),
		OpenRequests:          int32(open),
	}
	if hundredths(capacity) > 0 {
		out.LoadPercentThisWeek = ptrTo(roundHours(booked / capacity * 100))
		out.TentativeLoadPercentThisWeek = ptrTo(roundHours(tentative / capacity * 100))
	}
	return out, nil
}
