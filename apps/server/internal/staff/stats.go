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
// with its terms chain, and the manager graph built from those same rows.
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
	r, err := loadEmployments(ctx, q)
	if err != nil {
		return r, err
	}
	return r, r.loadPeople(ctx, q)
}

// loadEmployments reads on q every employment, their terms and the
// departments, and builds the manager graph from those rows — each table
// read once. The people are loadPeople's, and names is empty until then.
func loadEmployments(ctx context.Context, q *store.Queries) (register, error) {
	var r register
	var err error
	if r.employments, err = q.ListAllEmployments(ctx); err != nil {
		return r, fmt.Errorf("staff: read the employments for the dashboard: %w", err)
	}
	r.names, r.counts = map[int64]string{}, map[int64]int{}
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
	departments, err := q.ListDepartments(ctx)
	if err != nil {
		return r, fmt.Errorf("staff: read the departments for the dashboard: %w", err)
	}
	ge := make([]graphEmployment, 0, len(r.employments))
	for _, e := range r.employments {
		ge = append(ge, asGraphEmployment(e))
	}
	gt := make([]graphTerms, 0, len(terms))
	for _, t := range terms {
		gt = append(gt, graphTerms{
			EmploymentID: t.EmploymentID, ValidFrom: civil(t.ValidFrom), DepartmentID: t.DepartmentID, Manager: t.ManagerPersonID,
			Title: t.Title, LocationID: t.LocationID,
		})
	}
	gd := make([]graphDepartment, 0, len(departments))
	for _, d := range departments {
		gd = append(gd, graphDepartment{ID: d.ID, Manager: d.ManagerPersonID, ParentID: d.ParentID})
	}
	r.graph = newManagerGraph(ge, gt, gd)
	return r, nil
}

// loadPeople reads everyone onto r, with their names.
func (r *register) loadPeople(ctx context.Context, q *store.Queries) error {
	var err error
	if r.people, err = q.StatsPeople(ctx); err != nil {
		return fmt.Errorf("staff: read the people for the dashboard: %w", err)
	}
	for _, p := range r.people {
		r.names[p.ID] = p.DisplayName
	}
	return nil
}

// asGraphEmployment is e as the manager graph reads it.
func asGraphEmployment(e store.StaffEmployment) graphEmployment {
	return graphEmployment{ID: e.ID, PersonID: e.PersonID, BusinessUnitID: e.BusinessUnitID, Start: civil(e.StartDate), End: civilPtr(e.EndDate)}
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
// GET /people's rule. The sick figures are staff:manage's, staff:approve's
// and staff:personal-data's alone (plan reading 14).
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
		return absenceStats(ctx, q, r, caller, &out)
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffStats200JSONResponse(out), nil
}

// sickRateWindowMonths is the sick-leave rate's window: the twelve months
// ending today (plan reading 7).
const sickRateWindowMonths = 12

// readsSickFigures is whether the caller is shown the sick figures on
// GET /stats (plan reading 14, open question 3's interim default):
// staff:manage, staff:approve and staff:personal-data.
func (c *callerContext) readsSickFigures() bool { return c.Manage || c.Approve || c.PersonalData }

// absenceStats adds delivery B's figures to out (design S20, plan Task 6),
// read on q in the same snapshot as r: onLeaveToday to every reader, the
// open cases and the sick-leave rate to the readers of the sick figures.
func absenceStats(ctx context.Context, q *store.Queries, r register, c *callerContext, out *gen.StaffStats) error {
	today := c.Today
	rows, err := q.AbsencesBetween(ctx, store.AbsencesBetweenParams{FromDay: dayParam(today), ToDay: dayParam(today)})
	if err != nil {
		return fmt.Errorf("staff: read today's absences: %w", err)
	}
	out.OnLeaveToday = int32(onLeave(rows, r.activePeople(today), today))
	if !c.readsSickFigures() {
		return nil
	}
	open, err := q.CountOpenSickCases(ctx, dayParam(today))
	if err != nil {
		return fmt.Errorf("staff: count the open sick-leave cases: %w", err)
	}
	from := addMonths(today, -sickRateWindowMonths).AddDate(0, 0, 1)
	window, err := q.AbsencesBetween(ctx, store.AbsencesBetweenParams{FromDay: dayParam(from), ToDay: dayParam(today)})
	if err != nil {
		return fmt.Errorf("staff: read the absences of the twelve months: %w", err)
	}
	rate := sickRate(r, window, from, today)
	out.OpenSickCases, out.SickAbsenceRate = &open, &rate
	return nil
}

// onLeave is how many of active — the people employed today — have an
// approved or registered day row of any kind covering today. Every kind
// counts: a figure without the sick ones, beside the calendar's bars a
// colleague reads as "absent", would tell them who is sick.
func onLeave(rows []store.AbsencesBetweenRow, active map[int64]int64, today time.Time) int {
	away := map[int64]bool{}
	for _, a := range rows {
		if _, employed := active[a.PersonID]; employed && covers(a, today) {
			away[a.PersonID] = true
		}
	}
	return len(away)
}

// sickRate is the sick-leave rate from..to in percent to two decimals (plan
// reading 7), as SSB and NAV count it: the working days lost to sickness —
// each working day of every employment in force, its sick share (sharesOn:
// by degree, beside leave the part left, a ferie day sick only under a 100 %
// sykmelding) by the full-time percentage in force — over the working days
// agreed: the same days by the same percentage, less the share of each that
// was holiday or leave. Child sickness is not the employee's and counts
// nothing; no agreed day, a rate of 0.
func sickRate(r register, rows []store.AbsencesBetweenRow, from, to time.Time) float64 {
	byPerson := map[int64][]store.AbsencesBetweenRow{}
	for _, a := range rows {
		byPerson[a.PersonID] = append(byPerson[a.PersonID], a)
	}
	var agreed, sick int64
	for _, e := range r.employments {
		first, last := later(from, civil(e.StartDate)), to
		if e.EndDate.Valid {
			last = earlier(last, e.EndDate.Time)
		}
		chain := r.chains[e.ID]
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			if !worksOn(d) {
				continue
			}
			t := termsInForce(e, chain, d)
			if t == nil {
				continue
			}
			fte := hundredthsOfNumeric(t.FtePercent)
			shares := sharesOn(byPerson[e.PersonID], d)
			agreed += fte * max(wholeDay-shares.Holiday-shares.Leave, 0)
			sick += fte * shares.sick()
		}
	}
	if agreed == 0 {
		return 0
	}
	return math.Round(float64(sick)*10000/float64(agreed)) / 100
}
