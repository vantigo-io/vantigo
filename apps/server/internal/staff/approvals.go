package staff

import (
	"context"
	"fmt"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the approvals inbox (design §7, plan Task 4): the requests
// the caller decides, oldest first — every person's for staff:approve and
// staff:manage, a manager's reports' today by the one manager rule — never
// the caller's own but the self-approver's (deciders.go). Each row is shaped
// as GET /absences shapes it for the caller, so an HR-only request is
// "leave" to all but staff:personal-data, with no revision to anyone who
// does not write it.

// GetStaffApprovals List the requests I decide
// (GET /api/v1/staff/approvals)
func (s *server) GetStaffApprovals(ctx context.Context, req gen.GetStaffApprovalsRequestObject) (gen.GetStaffApprovalsResponseObject, error) {
	if f := validatePageParams(req.Params.Page, req.Params.PageSize); f.failed() {
		return gen.GetStaffApprovals400ApplicationProblemPlusJSONResponse(invalid(invalidPagingTitle, f)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	everyone := caller.Approve || caller.Manage
	exclude := caller.Person
	if everyone && caller.Person != nil {
		d, err := s.judgeDecider(ctx, caller, *caller.Person)
		if err != nil {
			return nil, err
		}
		if d.selfApproves {
			exclude = nil
		}
	}
	out := gen.PaginatedResponseOfStaffApproval{Data: []gen.StaffApproval{}, Pagination: apicommon.Pagination(page, pageSize, 0)}
	if !everyone && caller.Person == nil {
		return gen.GetStaffApprovals200JSONResponse(out), nil
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var reports []int64
		if caller.Person != nil {
			g, err := loadManagerGraph(ctx, q, nil)
			if err != nil {
				return err
			}
			reports = g.reportsOf(*caller.Person, caller.Today)
		}
		if !everyone && len(reports) == 0 {
			return nil
		}
		total, err := q.CountRequestedAbsences(ctx, store.CountRequestedAbsencesParams{Everyone: everyone, PersonIds: reports, ExcludePerson: exclude})
		if err != nil {
			return fmt.Errorf("staff: count the requests: %w", err)
		}
		rows, err := q.RequestedAbsences(ctx, store.RequestedAbsencesParams{
			Everyone: everyone, PersonIds: reports, ExcludePerson: exclude, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list the requests: %w", err)
		}
		var ids []int64
		for _, r := range rows {
			if !slices.Contains(ids, r.StaffAbsence.PersonID) {
				ids = append(ids, r.StaffAbsence.PersonID)
			}
		}
		names, err := personSummaries(ctx, q, ids)
		if err != nil {
			return err
		}
		out.Data = mapRows(rows, func(r store.RequestedAbsencesRow) gen.StaffApproval {
			pid := r.StaffAbsence.PersonID
			a := caller.audienceOn(pid, slices.Contains(reports, pid), false)
			return gen.StaffApproval{Absence: absenceResponse(r.StaffAbsence, r.Kind, r.Visibility, r.Unit, a, s.absenceTag), Person: names[pid]}
		})
		out.Pagination = apicommon.Pagination(page, pageSize, int32(total))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffApprovals200JSONResponse(out), nil
}
