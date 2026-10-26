package planning

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is a request as the contract answers it (S16, rule 4) and the two
// reads of requests: the list and the one request. Nothing of a competence,
// a project or a person is stored, so every response names them through the
// contracts, read before any transaction: each requirement from Staff's
// catalogue as the caller may see it — one it no longer answers reads by its
// id — the project through the directory, and the person of the allocation
// that fills it through Workforce.People over that row's window, cut to the
// bound. The note is planning:manage's alone (S5).

// requestResponse is r as the contract answers it: its requirements named
// from catalogue when it still answers them (rule 4), the project named when
// the directory resolved it, filledBy when a row fills it, and the note only
// when withNote — the caller holds planning:manage.
func requestResponse(r store.PlanningRequest, requirements []store.PlanningRequestRequirement, catalogue map[int64]contracts.CompetenceEntry, project *contracts.ProjectEntry, filledBy *gen.PlanningRequestFilledBy, withNote bool) gen.PlanningRequest {
	out := gen.PlanningRequest{
		Id:           r.ID,
		Project:      gen.PlanningProjectRef{Id: r.ProjectID},
		Title:        r.Title,
		FromDate:     apiDate(civil(r.FromDate)),
		ToDate:       apiDate(civil(r.ToDate)),
		HoursPerWeek: loadOf(r.HoursPerWeek),
		Status:       gen.PlanningRequestStatus(r.Status),
		Requirements: make([]gen.PlanningRequirement, 0, len(requirements)),
		FilledBy:     filledBy,
		Revision:     r.Revision,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
	if project != nil {
		code, name, status := project.Code, project.Name, gen.PlanningProjectStatus(project.Status)
		out.Project.Code, out.Project.Name, out.Project.Status = &code, &name, &status
	}
	for _, req := range requirements {
		v := gen.PlanningRequirement{CompetenceId: req.CompetenceID}
		if req.MinLevel != nil {
			level := int32(*req.MinLevel)
			v.MinLevel = &level
		}
		if e, ok := catalogue[req.CompetenceID]; ok {
			key, nameEn, nameNb, kind, hasLevel := e.Key, e.NameEn, e.NameNb, gen.PlanningRequirementKind(e.Kind), e.HasLevel
			v.Key, v.NameEn, v.NameNb, v.Kind, v.HasLevel = &key, &nameEn, &nameNb, &kind, &hasLevel
		}
		out.Requirements = append(out.Requirements, v)
	}
	if withNote {
		out.Note = r.Note
	}
	return out
}

// filledByOf is the allocation a that fills a request and its person, nil
// when Staff did not answer them (rule 4: an id with displayName null).
func filledByOf(a store.PlanningAllocation, person *contracts.PersonEntry) *gen.PlanningRequestFilledBy {
	ref := allocationResponse(a, person, nil, false).Person
	return &gen.PlanningRequestFilledBy{AllocationId: a.ID, Person: ref}
}

// requirementsByRequest is the requirements of the requests of ids, by
// request, in the order they were written. q may be the pool's or a
// transaction's.
func requirementsByRequest(ctx context.Context, q *store.Queries, ids []int64) (map[int64][]store.PlanningRequestRequirement, error) {
	out := map[int64][]store.PlanningRequestRequirement{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.RequirementsOfRequests(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("planning: read the requests' requirements: %w", err)
	}
	for _, r := range rows {
		out[r.RequestID] = append(out[r.RequestID], r)
	}
	return out, nil
}

// requestsOf is rows as the contract answers them, in their order, read
// before any transaction: the requirements, the catalogue (asked only when a
// requirement needs naming), the projects, the allocations that fill the
// filled ones and the people of those allocations. Never nil.
func (s *server) requestsOf(ctx context.Context, c *callerContext, rows []store.PlanningRequest) ([]gen.PlanningRequest, error) {
	out, _, _, err := s.requestsRead(ctx, c, rows)
	return out, err
}

// requestsRead is requestsOf with what it read to name the rows beside the
// answer — their requirements by request and Staff's catalogue by id — for a
// caller that judges the requests too (the intake), so neither is read
// twice.
func (s *server) requestsRead(ctx context.Context, c *callerContext, rows []store.PlanningRequest) (
	[]gen.PlanningRequest, map[int64][]store.PlanningRequestRequirement, map[int64]contracts.CompetenceEntry, error) {
	out := make([]gen.PlanningRequest, 0, len(rows))
	if len(rows) == 0 {
		return out, map[int64][]store.PlanningRequestRequirement{}, map[int64]contracts.CompetenceEntry{}, nil
	}
	q := store.New(s.deps.Pool)
	ids := make([]int64, len(rows))
	projectIDs := make([]int32, len(rows))
	for i, r := range rows {
		ids[i], projectIDs[i] = r.ID, r.ProjectID
	}
	requirements, err := requirementsByRequest(ctx, q, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	catalogue, err := s.catalogueFor(ctx, requirements)
	if err != nil {
		return nil, nil, nil, err
	}
	projects, err := s.projectEntries(ctx, slices.Compact(slices.Sorted(slices.Values(projectIDs))))
	if err != nil {
		return nil, nil, nil, err
	}
	fillers, err := q.AllocationsFillingRequests(ctx, ids)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("planning: read the allocations filling the requests: %w", err)
	}
	people, err := s.peopleOfRows(ctx, c, fillers)
	if err != nil {
		return nil, nil, nil, err
	}
	filledBy := make(map[int64]*gen.PlanningRequestFilledBy, len(fillers))
	for _, a := range fillers {
		var person *contracts.PersonEntry
		if p, ok := people[a.PersonID]; ok {
			person = &p
		}
		filledBy[*a.RequestID] = filledByOf(a, person)
	}
	for _, r := range rows {
		var project *contracts.ProjectEntry
		if p, ok := projects[r.ProjectID]; ok {
			project = &p
		}
		out = append(out, requestResponse(r, requirements[r.ID], catalogue, project, filledBy[r.ID], c.Manage))
	}
	return out, requirements, catalogue, nil
}

// peopleOfRows is the people Staff answers over rows' window, cut to the
// Workforce bound (a window wholly outside it names nobody), by id.
func (s *server) peopleOfRows(ctx context.Context, c *callerContext, rows []store.PlanningAllocation) (map[int64]contracts.PersonEntry, error) {
	out := map[int64]contracts.PersonEntry{}
	if len(rows) == 0 {
		return out, nil
	}
	from, to := civil(rows[0].FromDate), civil(rows[0].ToDate)
	for _, r := range rows {
		from, to = earliestOf(from, civil(r.FromDate)), latestOf(to, civil(r.ToDate))
	}
	lo, hi, ok := clipToBound(from, to, c.Today)
	if !ok {
		return out, nil
	}
	people, err := s.workforcePeople(ctx, lo, hi)
	if err != nil {
		return nil, err
	}
	for _, p := range people {
		out[p.ID] = p
	}
	return out, nil
}

// ListPlanningRequests List requests
// (GET /api/v1/planning/requests)
//
// planning:view's (the contract's rule): the requests of a project, a status
// and touching from..to, each filter optional, at most 500 by from date and
// id. Without a window every request is listed.
func (s *server) ListPlanningRequests(ctx context.Context, req gen.ListPlanningRequestsRequestObject) (gen.ListPlanningRequestsResponseObject, error) {
	p := req.Params
	params := store.ListRequestsFilteredParams{ProjectID: p.ProjectId, MaxRows: maxRequestsListed}
	if p.From != nil {
		params.FromDate = dayParam(dayOf(*p.From))
	}
	if p.To != nil {
		params.ToDate = dayParam(dayOf(*p.To))
	}
	if p.From != nil && p.To != nil && dayOf(*p.To).Before(dayOf(*p.From)) {
		return gen.ListPlanningRequests400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle,
			fieldErrors{errs: fieldError("to", "to must be on or after from")})), nil
	}
	if p.Status != nil {
		status := string(*p.Status)
		params.Status = &status
	}
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.deps.Pool).ListRequestsFiltered(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("planning: list requests: %w", err)
	}
	requests, err := s.requestsOf(ctx, c, rows)
	if errors.Is(err, errDependencyUnavailable) {
		return gen.ListPlanningRequests503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.ListPlanningRequests200JSONResponse(gen.PlanningRequestList{Requests: requests}), nil
}

// GetPlanningRequest A request
// (GET /api/v1/planning/requests/{id})
//
// planning:view's; an unknown id is the bare 404.
func (s *server) GetPlanningRequest(ctx context.Context, req gen.GetPlanningRequestRequestObject) (gen.GetPlanningRequestResponseObject, error) {
	c, err := s.callerFor(ctx)
	if err != nil {
		return nil, err
	}
	row, found, err := s.request(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetPlanningRequest404Response{}, nil
	}
	out, err := s.requestsOf(ctx, c, []store.PlanningRequest{row})
	if errors.Is(err, errDependencyUnavailable) {
		return gen.GetPlanningRequest503ApplicationProblemPlusJSONResponse(dependencyUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetPlanningRequest200JSONResponse(out[0]), nil
}

// request reads one request outside any transaction; false when there is
// none.
func (s *server) request(ctx context.Context, id int64) (store.PlanningRequest, bool, error) {
	row, err := store.New(s.deps.Pool).GetRequest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningRequest{}, false, nil
	}
	if err != nil {
		return store.PlanningRequest{}, false, fmt.Errorf("planning: read request %d: %w", id, err)
	}
	return row, true, nil
}

// lockedRequest reads one request FOR UPDATE inside a write's transaction;
// an unknown id is the 404 refusal.
func lockedRequest(ctx context.Context, txq *store.Queries, id int64) (store.PlanningRequest, error) {
	row, err := txq.LockRequest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PlanningRequest{}, notFound()
	}
	if err != nil {
		return store.PlanningRequest{}, fmt.Errorf("planning: lock request %d: %w", id, err)
	}
	return row, nil
}
