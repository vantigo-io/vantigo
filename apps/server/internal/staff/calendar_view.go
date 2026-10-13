package staff

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/calendar"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /calendar (design §7's Absence, plan Task 4): a range of
// at most 93 days, its public holidays from the platform calendar, and a row
// of bars per person the caller may see there. staff:view, staff:approve,
// staff:manage and staff:personal-data see everyone with an employment in
// the range — a colleague (staff:view alone) only those active today too, as
// the directory lists them (plan reading 15); anyone else their reports
// today and themselves. Each bar is shaped as GET /absences shapes the row:
// a colleague's carries the dates alone and only for approved and
// registered rows, so a sickness looks like any other absence.

// maxCalendarDays is the longest range the calendar answers.
const maxCalendarDays = 93

// GetStaffCalendar The absence calendar
// (GET /api/v1/staff/calendar)
func (s *server) GetStaffCalendar(ctx context.Context, req gen.GetStaffCalendarRequestObject) (gen.GetStaffCalendarResponseObject, error) {
	from, to := civil(dayParam(req.Params.From.Time)), civil(dayParam(req.Params.To.Time))
	var f fieldErrors
	switch {
	case to.Before(from):
		f.add("to", "to cannot be before from")
	case int(to.Sub(from).Hours()/24)+1 > maxCalendarDays:
		f.add("to", fmt.Sprintf("At most %d days from from", maxCalendarDays))
	}
	if f.failed() {
		return gen.GetStaffCalendar400ApplicationProblemPlusJSONResponse(invalid(invalidListTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	out := gen.StaffCalendar{
		From: openapi_types.Date{Time: from}, To: openapi_types.Date{Time: to},
		PublicHolidays: mapRows(calendar.PublicHolidays(from, to), func(d time.Time) openapi_types.Date { return openapi_types.Date{Time: d} }),
		People:         []gen.StaffCalendarPerson{},
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		g, err := loadManagerGraph(ctx, q, nil)
		if err != nil {
			return err
		}
		var reports, active, people []int64
		if caller.Person != nil {
			reports = g.reportsOf(*caller.Person, caller.Today)
			people = append(slices.Clone(reports), *caller.Person)
		}
		if caller.View {
			if active, err = q.PeopleActiveOn(ctx, dayParam(caller.Today)); err != nil {
				return fmt.Errorf("staff: read the people active today: %w", err)
			}
		}
		if caller.View || caller.Approve || caller.Manage || caller.PersonalData {
			employed, err := q.PeopleEmployedBetween(ctx, store.PeopleEmployedBetweenParams{FromDay: dayParam(from), ToDay: dayParam(to)})
			if err != nil {
				return fmt.Errorf("staff: read the people employed in the range: %w", err)
			}
			wide := caller.Approve || caller.Manage || caller.PersonalData
			for _, p := range employed {
				if (wide || slices.Contains(active, p)) && !slices.Contains(people, p) {
					people = append(people, p)
				}
			}
		}
		if req.Params.DepartmentId != nil {
			people = inDepartment(g, people, *req.Params.DepartmentId, clampDay(caller.Today, from, to))
		}
		if len(people) == 0 {
			return nil
		}
		rows, err := q.CalendarAbsences(ctx, store.CalendarAbsencesParams{PersonIds: people, FromDay: dayParam(from), ToDay: dayParam(to)})
		if err != nil {
			return fmt.Errorf("staff: read the calendar's absences: %w", err)
		}
		names, err := personSummaries(ctx, q, people)
		if err != nil {
			return err
		}
		bars := map[int64][]gen.StaffAbsence{}
		for _, r := range rows {
			pid := r.StaffAbsence.PersonID
			a := caller.audienceOn(pid, slices.Contains(reports, pid), slices.Contains(active, pid))
			if a.seesAbsence(r.StaffAbsence.Status, r.Visibility) {
				bars[pid] = append(bars[pid], absenceResponse(r.StaffAbsence, r.Kind, r.Visibility, r.Unit, a, s.absenceTag))
			}
		}
		for _, pid := range people {
			person, ok := names[pid]
			if !ok || !caller.audienceOn(pid, slices.Contains(reports, pid), slices.Contains(active, pid)).seesPerson() {
				continue
			}
			list := bars[pid]
			if list == nil {
				list = []gen.StaffAbsence{}
			}
			out.People = append(out.People, gen.StaffCalendarPerson{Person: person, Absences: list})
		}
		slices.SortFunc(out.People, func(a, b gen.StaffCalendarPerson) int {
			return cmp.Or(cmp.Compare(a.Person.DisplayName, b.Person.DisplayName), cmp.Compare(a.Person.Id, b.Person.Id))
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffCalendar200JSONResponse(out), nil
}

// clampDay is day held inside from..to: the day a department filter is
// judged on.
func clampDay(day, from, to time.Time) time.Time {
	if day.Before(from) {
		return from
	}
	if day.After(to) {
		return to
	}
	return day
}

// inDepartment is the people whose terms in force on day, on any of their
// employments, place them in department id.
func inDepartment(g managerGraph, people []int64, id int64, day time.Time) []int64 {
	var out []int64
	for _, p := range people {
		for _, e := range g.employments[p] {
			if t := g.inForce(e, day); t != nil && t.DepartmentID != nil && *t.DepartmentID == id {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
