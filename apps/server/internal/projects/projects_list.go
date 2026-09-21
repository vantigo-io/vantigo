package projects

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// The project list. It lives beside projects.go rather than in it because
// the list is the one operation of this area that is mostly not about a
// single project: the visibility predicate, the filters, the paging and the
// two batched lookups that embed a customer name and a manager list on every
// row are all its own.

// listDefaultPageSize and listMaxPageSize are customers' list defaults,
// reused so every list in the product pages the same way.
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
)

// visibility is the "sees every project, or holds a role on this one" pair
// every query that has to respect design §5 filters on: whether the caller's
// global permissions let them see everything, and who they are when those
// permissions do not. The stats queries (Task 8) count over exactly the same
// predicate as the list, so they take exactly the same pair — one place
// decides what "the projects I can see" means.
type visibility struct {
	SeeAll bool
	UserID uuid.UUID
}

// visibilityFor is the caller's visibility. It reads the global permissions
// once through globalAccess, so "can see every project" has one definition
// here and in authorize.
func (s *server) visibilityFor(ctx context.Context) visibility {
	p, _ := contracts.PrincipalFrom(ctx)
	return visibility{SeeAll: s.globalAccess(ctx).CanSee, UserID: p.UserID}
}

// likeReplacer escapes ILIKE's own characters, so a '%' or '_' the caller
// typed into the search box matches itself instead of everything. Backslash
// is escaped first, or escaping % and _ would double the backslashes it just
// introduced. The wildcards around the pattern are added by the query, not
// here.
var likeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// validatePageParams is the paging rule both paged operations share, in
// customers' own words: every failure is collected rather than the first one
// reported, and they are joined into one problem detail.
func validatePageParams(page, pageSize *int32) []string {
	var errs []string
	if page != nil && *page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
	}
	if pageSize != nil && (*pageSize < 1 || *pageSize > listMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", listMaxPageSize, *pageSize))
	}
	return errs
}

// pageParams is the validated paging as numbers: page 1 and
// listDefaultPageSize rows unless the caller asked otherwise.
func pageParams(page, pageSize *int32) (int32, int32) {
	p, size := int32(1), int32(listDefaultPageSize)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	return p, size
}

// validateGetProjectsParams is the paging rule plus the list's own filter:
// a status outside the enumeration is a mistake worth reporting, not a
// filter that silently matches nothing.
func validateGetProjectsParams(p gen.GetProjectsParams) []string {
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && *p.Status != "" && !validProjectStatus(*p.Status) {
		errs = append(errs, fmt.Sprintf("'status' must be one of %s, but was '%s'.", projectStatusList(), *p.Status))
	}
	return errs
}

// GetProjects List projects
// (GET /api/v1/projects)
//
// Visibility is the query's own first predicate, not a filter applied to the
// rows it returned (design §5): a member's totalCount has to be the number
// of projects that member can see, or the last page of their list is empty
// and the page count lies.
//
// The two embedded lookups are batched over the whole page rather than made
// per row: one ManagersForProjects for every project on it, one Users call
// for every manager on it, and one Customers call for the page's distinct
// customer ids. A list of 25 projects for one customer therefore costs one
// directory call, not 25 — and a list of 25 projects across five customers
// still costs exactly one.
func (s *server) GetProjects(ctx context.Context, req gen.GetProjectsRequestObject) (gen.GetProjectsResponseObject, error) {
	if msgs := validateGetProjectsParams(req.Params); len(msgs) > 0 {
		return gen.GetProjects400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	search := ""
	if req.Params.Search != nil {
		search = likeReplacer.Replace(strings.TrimSpace(*req.Params.Search))
	}

	// An empty status is no filter, not a filter for the empty string: a
	// frontend that clears its status dropdown sends `status=`.
	status := req.Params.Status
	if status != nil && *status == "" {
		status = nil
	}

	v := s.visibilityFor(ctx)
	filter := store.CountProjectsParams{
		SeeAll:     v.SeeAll,
		UserID:     v.UserID,
		Mine:       req.Params.Mine != nil && *req.Params.Mine,
		Status:     status,
		CustomerID: req.Params.CustomerId,
		Internal:   req.Params.Internal,
		Search:     search,
	}

	q := store.New(s.deps.Pool)
	total, err := q.CountProjects(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("projects: count projects: %w", err)
	}
	rows, err := q.ListProjects(ctx, store.ListProjectsParams{
		SeeAll:     filter.SeeAll,
		UserID:     filter.UserID,
		Mine:       filter.Mine,
		Status:     filter.Status,
		CustomerID: filter.CustomerID,
		Internal:   filter.Internal,
		Search:     filter.Search,
		PageSize:   pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: list projects: %w", err)
	}

	managers, err := s.managersForPage(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	customerNames, err := s.customerNamesForPage(ctx, rows)
	if err != nil {
		return nil, err
	}

	data := make([]gen.ProjectSummaryResponse, 0, len(rows))
	for _, row := range rows {
		summary, err := projectSummaryResponse(row, customerNames[row.ID], managers[row.ID])
		if err != nil {
			return nil, err
		}
		data = append(data, summary)
	}
	return gen.GetProjects200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// managersForPage is every project on the page's managers, named in one
// directory call: the page's manager ids are gathered first and resolved
// together, so a page of projects sharing a project manager asks for that
// name once.
func (s *server) managersForPage(ctx context.Context, q *store.Queries, rows []store.ProjectsProject) (map[int32][]gen.ProjectPersonSummary, error) {
	byProject := make(map[int32][]gen.ProjectPersonSummary, len(rows))
	for _, row := range rows {
		byProject[row.ID] = []gen.ProjectPersonSummary{}
	}
	if len(rows) == 0 {
		return byProject, nil
	}

	ids := make([]int32, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	assignments, err := q.ManagersForProjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: list managers for a page: %w", err)
	}

	userIDs := make([]uuid.UUID, 0, len(assignments))
	for _, a := range assignments {
		userIDs = append(userIDs, a.UserID)
	}
	names, err := s.displayNames(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	for _, a := range assignments {
		byProject[a.ProjectID] = append(byProject[a.ProjectID], gen.ProjectPersonSummary{
			UserId: a.UserID, DisplayName: names[a.UserID],
		})
	}
	return byProject, nil
}

// customerNamesForPage resolves the page's customers in one directoryCustomers
// call, over the page's distinct customer ids: a list of one customer's
// projects must not make one lookup per row, and a list spanning several
// customers must not make one per distinct id either. A page with no
// customer ids at all (every project internal) skips the call entirely.
func (s *server) customerNamesForPage(ctx context.Context, rows []store.ProjectsProject) (map[int32]*string, error) {
	byProject := make(map[int32]*string, len(rows))

	ids := make([]int32, 0, len(rows))
	seen := map[int32]bool{}
	for _, row := range rows {
		if row.CustomerID == nil || seen[*row.CustomerID] {
			continue
		}
		seen[*row.CustomerID] = true
		ids = append(ids, *row.CustomerID)
	}
	if len(ids) == 0 {
		return byProject, nil
	}

	found, err := s.directoryCustomers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: look up customers for a page: %w", err)
	}
	names := make(map[int32]*string, len(found))
	for i := range found {
		names[found[i].ID] = &found[i].Name
	}

	for _, row := range rows {
		if row.CustomerID == nil {
			continue
		}
		byProject[row.ID] = names[*row.CustomerID]
	}
	return byProject, nil
}
