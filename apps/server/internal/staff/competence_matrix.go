package staff

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the Competence area's two reads (staff competence design S14,
// §5; plan readings 29 and 30): the matrix — who holds what, valid on a day —
// for staff:view and staff:manage, the directory with competences; and the
// interests overview — who wants to learn or use more of what — for
// staff:manage alone, never the Approver (amendment 16).

// invalidMatrixFilterTitle is the 400's title for a matrix filter that does
// not parse.
const invalidMatrixFilterTitle = "Invalid matrix filter"

// matrixCellOf is one row valid on the day as the matrix shows it.
func matrixCellOf(r store.MatrixRowsOnRow) gen.StaffCompetenceMatrixCell {
	return gen.StaffCompetenceMatrixCell{
		CompetenceId: r.CompetenceID,
		Level:        int32Of(r.Level),
		Verified:     r.VerifiedAt != nil,
		ValidFrom:    openapi_types.Date{Time: civil(r.ValidFrom)},
		ValidTo:      dateOf(r.ValidTo),
	}
}

// catalogueSummaryOf is a catalogue row as a person's row names its
// competence.
func catalogueSummaryOf(c store.StaffCompetence) gen.StaffCompetenceSummary {
	return gen.StaffCompetenceSummary{
		Id: c.ID, Key: c.Key, Names: gen.StaffCompetenceNames{En: c.NameEn, Nb: c.NameNb},
		Kind: gen.StaffCompetenceKind(c.Kind), HasLevel: c.HasLevel, CategoryId: c.CategoryID, Archived: c.ArchivedAt != nil,
	}
}

// GetStaffCompetenceMatrix Who holds what
// (GET /api/v1/staff/competence-matrix)
//
// Every person active today and not erased, by name, with their current rows
// valid on validOn (today when left out) of the competences selected: the
// competenceIds given, else categoryId's, else all (reading 29). verifiedOnly
// drops declared rows — without it a declared certificate is shown, its
// badge the verified flag; minLevel drops the rows of a competence with a
// level below it. With competenceIds a person is listed only when they hold
// every one after those filters; otherwise everyone is, with no cells when
// they hold nothing selected. The columns are the competenceIds given, in
// the catalogue's order, else the competences of the cells shown.
func (s *server) GetStaffCompetenceMatrix(ctx context.Context, req gen.GetStaffCompetenceMatrixRequestObject) (gen.GetStaffCompetenceMatrixResponseObject, error) {
	if !s.has(ctx, permView) && !s.has(ctx, permManage) {
		return gen.GetStaffCompetenceMatrix403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	p := req.Params
	var f fieldErrors
	if p.MinLevel != nil && (*p.MinLevel < 0 || *p.MinLevel > 4) {
		f.add("minLevel", "A level is 0 to 4")
	}
	if f.failed() {
		return gen.GetStaffCompetenceMatrix400ApplicationProblemPlusJSONResponse(invalid(invalidMatrixFilterTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	validOn := caller.Today
	if p.ValidOn != nil {
		validOn = p.ValidOn.Time
	}
	selected := map[int64]bool{}
	if p.CompetenceId != nil {
		for _, id := range *p.CompetenceId {
			selected[id] = true
		}
	}
	verifiedOnly := p.VerifiedOnly != nil && *p.VerifiedOnly

	out := gen.StaffCompetenceMatrix{ValidOn: openapi_types.Date{Time: validOn}}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		catalogue, err := q.ListCompetences(ctx, store.ListCompetencesParams{IncludeHidden: true, IncludeArchived: true})
		if err != nil {
			return fmt.Errorf("staff: read the catalogue for the matrix: %w", err)
		}
		known := make(map[int64]bool, len(catalogue))
		for _, c := range catalogue {
			known[c.ID] = true
		}
		if p.CompetenceId != nil {
			var f fieldErrors
			for _, id := range *p.CompetenceId {
				if !known[id] {
					f.add("competenceId", fmt.Sprintf("No competence has id %d", id))
				}
			}
			if f.failed() {
				return refuseInvalid(invalidMatrixFilterTitle, f)
			}
		}
		activeIDs, err := q.PeopleActiveOn(ctx, dayParam(caller.Today))
		if err != nil {
			return fmt.Errorf("staff: read the people active today for the matrix: %w", err)
		}
		ids, err := q.PeopleNotErased(ctx, activeIDs)
		if err != nil {
			return fmt.Errorf("staff: read the people not erased for the matrix: %w", err)
		}
		people, err := q.PeopleSummaries(ctx, ids)
		if err != nil {
			return fmt.Errorf("staff: read the names for the matrix: %w", err)
		}
		rows, err := q.MatrixRowsOn(ctx, store.MatrixRowsOnParams{Day: dayParam(validOn), CategoryID: p.CategoryId})
		if err != nil {
			return fmt.Errorf("staff: read the rows for the matrix: %w", err)
		}
		cells := map[int64][]gen.StaffCompetenceMatrixCell{}
		shown := map[int64]bool{}
		for _, r := range rows {
			switch {
			case len(selected) > 0 && !selected[r.CompetenceID],
				verifiedOnly && r.VerifiedAt == nil,
				p.MinLevel != nil && r.CompetenceHasLevel && (r.Level == nil || int32(*r.Level) < *p.MinLevel):
				continue
			}
			cells[r.PersonID] = append(cells[r.PersonID], matrixCellOf(r))
		}
		slices.SortFunc(people, func(a, b store.PeopleSummariesRow) int {
			return cmp.Or(cmp.Compare(a.DisplayName, b.DisplayName), cmp.Compare(a.ID, b.ID))
		})
		out.People = []gen.StaffCompetenceMatrixRow{}
		for _, person := range people {
			held := cells[person.ID]
			if len(selected) > 0 && len(held) < len(selected) {
				continue
			}
			if held == nil {
				held = []gen.StaffCompetenceMatrixCell{}
			}
			for _, c := range held {
				shown[c.CompetenceId] = true
			}
			out.People = append(out.People, gen.StaffCompetenceMatrixRow{PersonId: person.ID, Name: person.DisplayName, Competences: held})
		}
		out.Competences = []gen.StaffCompetenceSummary{}
		for _, c := range catalogue {
			if (len(selected) > 0 && selected[c.ID]) || (len(selected) == 0 && shown[c.ID]) {
				out.Competences = append(out.Competences, catalogueSummaryOf(c))
			}
		}
		return nil
	})
	if r, ok := asRefusal(err); ok && r.status == http.StatusBadRequest {
		return gen.GetStaffCompetenceMatrix400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffCompetenceMatrix200JSONResponse(out), nil
}

// GetStaffCompetenceInterests Who wants to learn or use more of what
// (GET /api/v1/staff/competence-interests)
//
// staff:manage's alone (reading 30): every interest of a person active today
// and not erased, grouped by competence in the catalogue's order — only
// competences somebody wants, archived or hidden ones included — the person
// under wantsMore when they hold a current row of it, wantsToLearn otherwise,
// by name.
func (s *server) GetStaffCompetenceInterests(ctx context.Context, _ gen.GetStaffCompetenceInterestsRequestObject) (gen.GetStaffCompetenceInterestsResponseObject, error) {
	if !s.has(ctx, permManage) {
		return gen.GetStaffCompetenceInterests403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	out := []gen.StaffCompetenceInterestsEntry{}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		activeIDs, err := q.PeopleActiveOn(ctx, dayParam(caller.Today))
		if err != nil {
			return fmt.Errorf("staff: read the people active today for the interests: %w", err)
		}
		active := make(map[int64]bool, len(activeIDs))
		for _, id := range activeIDs {
			active[id] = true
		}
		rows, err := q.ListAllInterests(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the interests: %w", err)
		}
		for _, r := range rows {
			i := r.StaffPersonInterest
			if r.PersonErasedAt != nil || !active[i.PersonID] {
				continue
			}
			if len(out) == 0 || out[len(out)-1].Competence.Id != i.CompetenceID {
				out = append(out, gen.StaffCompetenceInterestsEntry{
					Competence: gen.StaffCompetenceSummary{
						Id: i.CompetenceID, Key: r.CompetenceKey, Names: gen.StaffCompetenceNames{En: r.CompetenceNameEn, Nb: r.CompetenceNameNb},
						Kind: gen.StaffCompetenceKind(r.CompetenceKind), HasLevel: r.CompetenceHasLevel, CategoryId: r.CompetenceCategoryID,
						Archived: r.CompetenceArchivedAt != nil,
					},
					WantsToLearn: []gen.StaffPersonSummary{}, WantsMore: []gen.StaffPersonSummary{},
				})
			}
			entry := &out[len(out)-1]
			person := gen.StaffPersonSummary{Id: i.PersonID, DisplayName: r.PersonDisplayName, EmployeeNumber: r.PersonEmployeeNumber}
			if r.Held {
				entry.WantsMore = append(entry.WantsMore, person)
			} else {
				entry.WantsToLearn = append(entry.WantsToLearn, person)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffCompetenceInterests200JSONResponse(out), nil
}
