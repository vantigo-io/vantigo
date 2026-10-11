package staff

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /people, the directory: listed for staff:manage (everyone)
// and staff:view (the people active today, plan reading 15), the work group
// only, searched by name, employee number and work email. Task 4 adds the
// structure filters, staff:manage's status filter and column, and the
// manager and department summaries.

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
	// (plan reading 15); HR lists everyone.
	activeOnly := !caller.Manage
	today := pgtype.Date{Time: caller.Today, Valid: true}

	var (
		rows  []store.ListPeopleRow
		total int64
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if total, err = q.CountPeople(ctx, store.CountPeopleParams{Patterns: patterns, ActiveOnly: activeOnly, Today: today}); err != nil {
			return fmt.Errorf("staff: count people: %w", err)
		}
		rows, err = q.ListPeople(ctx, store.ListPeopleParams{
			Patterns: patterns, ActiveOnly: activeOnly, Today: today, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
		})
		if err != nil {
			return fmt.Errorf("staff: list people: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Every listed person is one the caller reads the work group of: HR's
	// everyone, the colleague's the people the filter kept as active today.
	a := audience{Colleague: caller.View, Manage: caller.Manage}
	return gen.GetStaffPeople200JSONResponse(gen.PaginatedResponseOfStaffPersonListItem{
		Data:       mapRows(rows, func(r store.ListPeopleRow) gen.StaffPersonListItem { return personListItemResponse(r, a) }),
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}
