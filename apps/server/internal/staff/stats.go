package staff

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the dashboard's figures for the register (design S20, plan
// Task 6): who is employed today, the full-time equivalents in force, who
// starts and who ends soon, and the records that never got an employment.
// Everything is read in one snapshot and judged in Go on the caller's today,
// in the settings' zone — the terms chain's rules (terms_derived.go) are Go's,
// and the attention items (attention.go) read the same snapshot.

// soonDays is the window of startingSoon, endingSoon and a probation ending
// soon: today and the thirty days after it.
const soonDays = 30

// register is the snapshot the dashboard judges: everyone, every employment
// with its terms chain, and the manager graph.
type register struct {
	people      []store.StatsPeopleRow
	employments []store.StaffEmployment // by person, by start
	chains      map[int64][]store.StaffEmploymentTerm
	graph       managerGraph
	names       map[int64]string // each person's name
	counts      map[int64]int    // how many employments each person has
}

// loadRegister reads the register on q, which is a snapshot's.
func loadRegister(ctx context.Context, q *store.Queries) (register, error) {
	var r register
	var err error
	if r.people, err = q.StatsPeople(ctx); err != nil {
		return r, fmt.Errorf("staff: read the people for the dashboard: %w", err)
	}
	if r.employments, err = q.ListAllEmployments(ctx); err != nil {
		return r, fmt.Errorf("staff: read the employments for the dashboard: %w", err)
	}
	r.names, r.counts = map[int64]string{}, map[int64]int{}
	for _, p := range r.people {
		r.names[p.ID] = p.DisplayName
	}
	ids := make([]int64, 0, len(r.employments))
	for _, e := range r.employments {
		ids = append(ids, e.ID)
		r.counts[e.PersonID]++
	}
	terms, err := q.ListTermsOfEmployments(ctx, ids)
	if err != nil {
		return r, fmt.Errorf("staff: read the terms for the dashboard: %w", err)
	}
	r.chains = map[int64][]store.StaffEmploymentTerm{}
	for _, t := range terms {
		r.chains[t.EmploymentID] = append(r.chains[t.EmploymentID], t)
	}
	if r.graph, err = loadManagerGraph(ctx, q, nil); err != nil {
		return r, err
	}
	return r, nil
}

// inForce reports whether e is in force on day: started on or before it and
// not ended before it (the end day itself is still worked).
func inForce(e store.StaffEmployment, day time.Time) bool {
	return !day.Before(civil(e.StartDate)) && (!e.EndDate.Valid || !e.EndDate.Time.Before(day))
}

// activePeople is the people with an employment in force on today, and the
// sum of the percentage in force that day over each one's employments, in
// hundredths of a percent (100 % is 10000).
func (r register) activePeople(today time.Time) map[int64]int64 {
	out := map[int64]int64{}
	for _, e := range r.employments {
		if !inForce(e, today) {
			continue
		}
		fte := out[e.PersonID]
		if t := termsInForce(e, r.chains[e.ID], today); t != nil {
			fte += hundredthsOfNumeric(t.FtePercent)
		}
		out[e.PersonID] = fte
	}
	return out
}

// registerStats is the figures on today.
func registerStats(r register, today time.Time) gen.StaffStats {
	var out gen.StaffStats
	horizon := today.AddDate(0, 0, soonDays)
	employed := map[int64]bool{}
	for _, e := range r.employments {
		employed[e.PersonID] = true
		start := civil(e.StartDate)
		if today.Before(start) {
			if !start.After(horizon) {
				out.StartingSoon++
			}
			continue
		}
		if inForce(e, today) && e.EndDate.Valid && !e.EndDate.Time.After(horizon) {
			out.EndingSoon++
		}
	}
	var fte int64
	active := r.activePeople(today)
	for _, f := range active {
		fte += f
	}
	out.Headcount = int32(len(active))
	// Hundredths of a percent to full-time equivalents, to two decimals.
	out.FteSum = math.Round(float64(fte)/100) / 100
	for _, p := range r.people {
		if p.ErasedAt == nil && !employed[p.ID] {
			out.PeopleWithoutEmployment++
		}
	}
	return out
}

// GetStaffStats Get the register's figures
// (GET /api/v1/staff/stats)
//
// The contract asks only staff:access; the figures are the directory's and
// HR's (the pre-flight's answer 6), and anyone else is the uniform 403 —
// GET /people's rule.
func (s *server) GetStaffStats(ctx context.Context, _ gen.GetStaffStatsRequestObject) (gen.GetStaffStatsResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.View && !caller.Manage {
		return gen.GetStaffStats403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	var out gen.StaffStats
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		r, err := loadRegister(ctx, q)
		if err != nil {
			return err
		}
		out = registerStats(r, caller.Today)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffStats200JSONResponse(out), nil
}
