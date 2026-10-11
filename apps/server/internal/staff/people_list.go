package staff

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /people, the directory: listed for staff:manage (everyone,
// by where they stand today) and staff:view (the people active today, plan
// reading 15), the work group only, searched by name, employee number and
// work email and filtered by the structure — and every person's placement
// today, which GET /people/{id} answers too: where they stand, and the
// manager and the department of their terms in force.

// likeReplacer escapes ILIKE's special characters, backslash first so the
// escaping of % and _ never doubles a backslash it just wrote (customers'
// likePattern, copied because no module imports another).
var likeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// searchPatterns is q as one escaped "%word%" pattern per word; a person
// passes when every word matches one of the searched fields.
func searchPatterns(q *string) []string {
	if q == nil {
		return []string{}
	}
	patterns := []string{}
	for _, word := range strings.Fields(*q) {
		patterns = append(patterns, "%"+likeReplacer.Replace(word)+"%")
	}
	return patterns
}

// GetStaffPeople List people
// (GET /api/v1/staff/people)
//
// The contract asks only staff:access; the directory is staff:view's and
// staff:manage's, and anyone else is the uniform 403 — Expenses' rule for a
// list whose audience the router cannot name in one permission.
func (s *server) GetStaffPeople(ctx context.Context, req gen.GetStaffPeopleRequestObject) (gen.GetStaffPeopleResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.View && !caller.Manage {
		return gen.GetStaffPeople403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if f := validatePageParams(req.Params.Page, req.Params.PageSize); f.failed() {
		return gen.GetStaffPeople400ApplicationProblemPlusJSONResponse(invalid(invalidPagingTitle, f)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	patterns := searchPatterns(req.Params.Q)
	// staff:view without staff:manage lists the people active today only
	// (plan reading 15), whatever the status filter says; HR lists by it.
	activeOnly := !caller.Manage
	status := "current"
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	if !slices.Contains([]string{"current", "ended", "all"}, status) {
		return gen.GetStaffPeople400ApplicationProblemPlusJSONResponse(invalid(invalidListTitle, fieldErrors{
			errs: fieldError("status", fmt.Sprintf("'status' is current, ended or all, but was '%s'.", status)),
		})), nil
	}
	today := pgtype.Date{Time: caller.Today, Valid: true}
	unit, department, location := req.Params.UnitId, req.Params.DepartmentId, req.Params.LocationId

	var (
		rows       []store.ListPeopleRow
		total      int64
		placements map[int64]placement
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if total, err = q.CountPeople(ctx, store.CountPeopleParams{
			Patterns: patterns, ActiveOnly: activeOnly, Today: today, Status: status,
			UnitID: unit, DepartmentID: department, LocationID: location,
		}); err != nil {
			return fmt.Errorf("staff: count people: %w", err)
		}
		rows, err = q.ListPeople(ctx, store.ListPeopleParams{
			Patterns: patterns, ActiveOnly: activeOnly, Today: today, Status: status,
			UnitID: unit, DepartmentID: department, LocationID: location,
			PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list people: %w", err)
		}
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		placements, err = placementsOf(ctx, q, ids, caller.Today)
		return err
	})
	if err != nil {
		return nil, err
	}
	// Every listed person is one the caller reads the work group of: HR's
	// everyone, the colleague's the people the filter kept as active today.
	a := audience{Colleague: caller.View, Manage: caller.Manage}
	return gen.GetStaffPeople200JSONResponse(gen.PaginatedResponseOfStaffPersonListItem{
		Data: mapRows(rows, func(r store.ListPeopleRow) gen.StaffPersonListItem {
			return personListItemResponse(r, placements[r.ID], a)
		}),
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}

// placement is where a person stands today and whom their terms in force
// place them under: the status, and the manager and the department of their
// earliest employment in force today — the manager its terms name, else the
// department's (design §4 Managers) — nil when there is none.
type placement struct {
	status     gen.StaffPersonStatus
	manager    *gen.StaffPersonSummary
	department *gen.StaffDepartmentSummary
	// managerActive is whether the manager is active today, without which a
	// colleague alone is not shown them (shownManager).
	managerActive bool
}

// statusOf is where a person with these employments stands on today: active
// when one has started and not ended (the end day itself is still worked),
// else upcoming from the earliest later start, else ended on the latest end,
// else none.
func statusOf(employments []graphEmployment, today time.Time) gen.StaffPersonStatus {
	var upcoming, ended *time.Time
	for _, e := range employments {
		switch {
		case e.Start.After(today):
			if upcoming == nil || e.Start.Before(*upcoming) {
				d := e.Start
				upcoming = &d
			}
		case e.End == nil || !e.End.Before(today):
			return gen.StaffPersonStatus{State: gen.StaffPersonStatusStateActive}
		default:
			if ended == nil || e.End.After(*ended) {
				ended = e.End
			}
		}
	}
	switch {
	case upcoming != nil:
		return gen.StaffPersonStatus{State: gen.StaffPersonStatusStateUpcoming, Date: timeDate(upcoming)}
	case ended != nil:
		return gen.StaffPersonStatus{State: gen.StaffPersonStatusStateEnded, Date: timeDate(ended)}
	}
	return gen.StaffPersonStatus{State: gen.StaffPersonStatusStateNone}
}

// placementsOf is the placement of each of ids on today, read on q.
func placementsOf(ctx context.Context, q *store.Queries, ids []int64, today time.Time) (map[int64]placement, error) {
	out := make(map[int64]placement, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	g, departments, err := loadManagerGraphAndDepartments(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.StaffDepartment, len(departments))
	for _, d := range departments {
		byID[d.ID] = d
	}
	managers := map[int64]int64{}
	var managerIDs []int64
	for _, id := range ids {
		p := placement{status: statusOf(g.employments[id], today)}
		for _, e := range g.employments[id] {
			t := g.inForce(e, today)
			if t == nil {
				continue
			}
			if t.DepartmentID != nil {
				if d, ok := byID[*t.DepartmentID]; ok {
					p.department = &gen.StaffDepartmentSummary{Id: d.ID, Code: d.Code, Name: d.Name}
				}
			}
			m := t.Manager
			if m == nil && t.DepartmentID != nil {
				m = g.departmentManager(*t.DepartmentID, id)
			}
			if m != nil {
				managers[id] = *m
				managerIDs = append(managerIDs, *m)
			}
			break
		}
		out[id] = p
	}
	if len(managerIDs) > 0 {
		names, err := personSummaries(ctx, q, managerIDs)
		if err != nil {
			return nil, err
		}
		active, err := q.PeopleActiveOn(ctx, store.PeopleActiveOnParams{Ids: managerIDs, Today: dayParam(today)})
		if err != nil {
			return nil, fmt.Errorf("staff: read which managers are active today: %w", err)
		}
		for id, m := range managers {
			if name, ok := names[m]; ok {
				p := out[id]
				p.manager = &name
				p.managerActive = slices.Contains(active, m)
				out[id] = p
			}
		}
	}
	return out, nil
}
