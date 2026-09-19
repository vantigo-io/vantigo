package projects

import (
	"context"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the economy portfolio (design §5, delivery B): one row per
// project the caller has financial rights on, so somebody responsible for
// several projects can see which of them need looking at without opening each
// one. Four things run through it.
//
// **The row list is an authorization decision, not a shaping one.** Every
// other read in this module answers a caller who may not see the money
// without it; here every column is money, so a project whose financials the
// caller may not see has no row at all. The rule — the project's manager,
// projects:manage-all, or projects:view-financials on a project they can see
// — is the module's existing one (authorize.go), expressed in SQL because the
// portfolio pages and counts over it: a predicate applied to rows already
// fetched gives a page whose total lies, exactly as it would in the project
// list. The `cost` block is not here at all; it is the per-project read's,
// behind a second permission.
//
// **Three reads and one call, whatever the size of the portfolio.** One
// query for the matching projects, one call into the module that owns the
// hours for all of them at once, one grouped read of their open milestones,
// and the customer names of the *page* only. Nothing is per row, no
// transaction is opened and no lock is held while another module's pool is
// waited on.
//
// **A cap rather than a truncation.** The actuals contract refuses a batch
// past contracts.MaxActualsRequests, so more matching projects than that is a
// 400 asking for a narrower filter. Answering the first two thousand would
// mean totals computed over part of the set — a wrong number rather than a
// missing one.
//
// **Filtering, sorting and paging in Go.** Two of the filters and two of the
// sorts are decided from what the other module reported, which no SQL of this
// module may read, so all four happen in one place over the whole set; the
// totals are taken before the page is cut, which is what makes them stable as
// the reader turns pages.

// The four orders the portfolio can be read in. They are contract values —
// the sort query parameter — so they are written exactly once, here.
const (
	portfolioSortBudgetUsed    = "budgetUsed"
	portfolioSortReadyAmount   = "readyAmount"
	portfolioSortNextMilestone = "nextMilestone"
	portfolioSortCode          = "code"
)

// portfolioSorts is the enumeration in the order a message names it, and
// portfolioStatusAll is the status filter's own extra value: every status at
// once, which no project is ever actually in.
var portfolioSorts = []string{
	portfolioSortBudgetUsed, portfolioSortReadyAmount, portfolioSortNextMilestone, portfolioSortCode,
}

const portfolioStatusAll = "all"

// portfolioMaxProjects is how many projects one read may ask the actuals
// contract about. It is that contract's own batch cap rather than a second
// number: the whole set is one call, so what that call can carry is what a
// read can carry.
//
// The two readers that hit it answer differently, and deliberately. The
// portfolio refuses (400) — it publishes totals, and a total over part of a
// set is a wrong number rather than a missing one. The dashboard's budget
// alerts keep the capped set and log that they did — an attention list is
// already a selection of things worth looking at, it totals nothing, and there
// is no filter for the reader to narrow.
const portfolioMaxProjects = contracts.MaxActualsRequests

// GetProjectsEconomy List the economy of the projects whose money the caller may see
// (GET /api/v1/projects/economy)
func (s *server) GetProjectsEconomy(ctx context.Context, req gen.GetProjectsEconomyRequestObject) (gen.GetProjectsEconomyResponseObject, error) {
	if errs := validatePortfolioParams(req.Params); len(errs) > 0 {
		return gen.GetProjectsEconomy400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid query parameters", errs)), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	search := ""
	if req.Params.Search != nil {
		search = likeReplacer.Replace(strings.TrimSpace(*req.Params.Search))
	}

	v := s.financialVisibilityFor(ctx)
	q := store.New(s.deps.Pool)
	// One row past the cap, so "more matched than one answer may carry" is
	// this read's own answer rather than a count that could disagree with it.
	projects, err := q.PortfolioProjects(ctx, store.PortfolioProjectsParams{
		ManageAll: v.ManageAll, UserID: v.UserID, ViewFinancials: v.ViewFinancials, SeeAll: v.SeeAll,
		Status:     portfolioStatus(req.Params.Status),
		CustomerID: req.Params.CustomerId,
		Search:     search,
		RowLimit:   portfolioMaxProjects + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: list the economy portfolio: %w", err)
	}
	if len(projects) > portfolioMaxProjects {
		return gen.GetProjectsEconomy400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Too many projects", map[string][]string{"status": {portfolioCapMessage()}})), nil
	}

	logged, err := s.portfolioActuals(ctx, projects)
	if err != nil {
		return nil, err
	}
	milestones, err := q.OpenMilestonesForProjects(ctx, projectIDs(projects))
	if err != nil {
		return nil, fmt.Errorf("projects: read the portfolio's open milestones: %w", err)
	}

	rows, err := s.portfolioRows(ctx, projects, logged, milestones, s.deps.Actuals != nil)
	if err != nil {
		return nil, err
	}
	rows = filterPortfolio(rows, req.Params)
	totals := portfolioTotals(rows)
	sortPortfolio(rows, req.Params.Sort)

	data, err := s.portfolioData(ctx, portfolioPage(rows, page, pageSize))
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsEconomy200JSONResponse{
		Data:         data,
		Pagination:   apicommon.Pagination(page, pageSize, int32(len(rows))),
		TimeTracking: s.deps.Actuals != nil,
		Totals:       totals,
	}, nil
}

// validatePortfolioParams is the portfolio's query rule as a field map: every
// failure is reported at once and against the parameter it is about, so a
// caller fixes their request in one round trip. The paging messages are the
// project list's own, asked for one parameter at a time.
func validatePortfolioParams(p gen.GetProjectsEconomyParams) map[string][]string {
	errs := map[string][]string{}
	if msgs := validatePageParams(p.Page, nil); len(msgs) > 0 {
		errs["page"] = msgs
	}
	if msgs := validatePageParams(nil, p.PageSize); len(msgs) > 0 {
		errs["pageSize"] = msgs
	}
	if p.Status != nil && *p.Status != "" && *p.Status != portfolioStatusAll && !validProjectStatus(*p.Status) {
		errs["status"] = []string{fmt.Sprintf("'status' must be one of %s, or '%s', but was '%s'.",
			projectStatusList(), portfolioStatusAll, *p.Status)}
	}
	if p.Sort != nil && *p.Sort != "" && !slices.Contains(portfolioSorts, *p.Sort) {
		errs["sort"] = []string{fmt.Sprintf("'sort' must be one of %s, but was '%s'.", quotedList(portfolioSorts), *p.Sort)}
	}
	return errs
}

// portfolioCapMessage names the cap and the three filters that can get under
// it. It is on 'status' because that is the filter a portfolio of everything
// is almost always missing — the default is 'active' and a caller past the
// cap has asked for 'all'.
func portfolioCapMessage() string {
	return fmt.Sprintf(
		"More than %d projects match, which is more than one answer can carry. Narrow the portfolio with 'status', 'customerId' or 'search'.",
		portfolioMaxProjects)
}

// portfolioStatus is the status filter as the query takes it: the caller's
// status, 'active' when they named none — a portfolio is about the work being
// done now — and no filter at all for 'all'.
func portfolioStatus(raw *string) *string {
	status := statusActive
	if raw != nil && *raw != "" {
		status = *raw
	}
	if status == portfolioStatusAll {
		return nil
	}
	return &status
}

// projectIDs is a set of project rows as the ids a grouped read takes.
func projectIDs(projects []store.ProjectsProject) []int32 {
	ids := make([]int32, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	return ids
}

// portfolioActuals is the one call into the module that owns the hours: every
// matching project at once, each in its own currency, outside any transaction
// — the same rule the per-project economy follows, applied to a whole
// portfolio. It answers nil for an installation without time tracking, which
// is not "nothing has been logged".
//
// The duplicate guard is belt and braces: the read these ids come from
// returns one row per project, so a repeat is impossible by construction, and
// the contract makes naming a project twice an error rather than a resolvable
// ambiguity — a guard here costs one map and turns a future bug into a
// missing figure rather than a failed request.
func (s *server) portfolioActuals(ctx context.Context, projects []store.ProjectsProject) (map[int32]loggedWork, error) {
	if s.deps.Actuals == nil || len(projects) == 0 {
		return nil, nil
	}
	reqs := make([]contracts.ActualsRequest, 0, len(projects))
	seen := make(map[int32]bool, len(projects))
	for _, project := range projects {
		if seen[project.ID] {
			continue
		}
		seen[project.ID] = true
		reqs = append(reqs, contracts.ActualsRequest{ProjectID: project.ID, Currency: project.Currency})
	}
	totals, err := s.deps.Actuals.ActualsForProjects(ctx, reqs)
	if err != nil {
		return nil, fmt.Errorf("projects: read what has been logged on the portfolio's projects: %w", err)
	}
	out := make(map[int32]loggedWork, len(totals))
	for id, t := range totals {
		work, err := loggedWorkOf(t)
		if err != nil {
			return nil, err
		}
		out[id] = work
	}
	return out, nil
}

// portfolioRow is one row while it is still being decided: the contract's row
// beside the two exact figures the orders are taken on. Both are kept as
// exact rationals rather than read back off the response, because a ratio
// rounded for display must never be what a comparison is made on, and two
// amounts that print the same may not be the same.
type portfolioRow struct {
	project store.ProjectsProject
	row     gen.ProjectEconomyRow
	// ratio is budgetUsed's exact used ÷ basis, nil when the project has no
	// basis — those rows sort last.
	ratio *big.Rat
	// ready is what the project's ready milestones add up to, exactly, nil
	// when it has nothing ready that anything can price.
	ready *big.Rat
}

// portfolioRows builds every matching project's row. The milestones arrive as
// one ordered list for the whole set and are grouped here rather than read
// per project; their order is already the "next milestone" rule's, so the
// first of each group is that project's next one.
func (s *server) portfolioRows(
	ctx context.Context,
	projects []store.ProjectsProject,
	logged map[int32]loggedWork,
	milestones []store.ProjectsBillingMilestone,
	timeTracking bool,
) ([]portfolioRow, error) {
	open := make(map[int32][]store.ProjectsBillingMilestone, len(projects))
	for _, m := range milestones {
		open[m.ProjectID] = append(open[m.ProjectID], m)
	}
	now := s.deps.Clock()
	rows := make([]portfolioRow, 0, len(projects))
	for _, project := range projects {
		row, err := s.portfolioRowFor(ctx, project, logged[project.ID], open[project.ID], timeTracking, now)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// portfolioRowFor is one project's row. seesAmounts is only "does the project
// have a currency": the caller's financial rights are already settled by the
// project being in the list at all, and a number in no currency is a number
// nobody can read.
func (s *server) portfolioRowFor(
	ctx context.Context,
	project store.ProjectsProject,
	logged loggedWork,
	open []store.ProjectsBillingMilestone,
	timeTracking bool,
	now time.Time,
) (portfolioRow, error) {
	seesAmounts := project.Currency != nil
	basis, err := projectBudgetBasis(project, seesAmounts)
	if err != nil {
		return portfolioRow{}, err
	}

	out := portfolioRow{project: project, row: gen.ProjectEconomyRow{
		Project: gen.ProjectEconomyRowProject{
			Id: project.ID, Code: project.Code, Name: project.Name, Status: project.Status,
		},
		Currency: project.Currency,
	}}

	if timeTracking {
		if logged.Bill.Approved.Hours == nil {
			// A project the provider did not answer for, which the contract
			// forbids: an empty row is still an answer about a project nobody
			// has logged against.
			logged = zeroWork()
		}
		actuals := portfolioActualsOf(logged, seesAmounts)
		pending := decimalNumber(new(big.Rat).Add(logged.Bill.Submitted.Hours, logged.Bill.Draft.Hours))
		out.row.Actuals, out.row.PendingHours = &actuals, &pending
		if use, ok := budgetUsed(basis, logged.Bill); ok {
			out.row.BudgetUsed = &gen.ProjectEconomyBudgetUsed{
				Basis: use.Basis, Percent: use.Percent, ApprovedPercent: use.ApprovedPercent,
			}
			out.row.OverBudget, out.ratio = use.OverBudget, use.Ratio
		}
	}

	for i, m := range open {
		next, ready := i == 0, m.Status == milestoneStatusReady
		if !next && !ready {
			// A planned milestone that is not the next one contributes only to
			// nothing: it is not reported and it is not in readyAmount. Pricing
			// it anyway would be arithmetic per milestone across the whole
			// filtered set, and — worse — milestoneEffective logs a warning per
			// unpriceable row, so a handful of orphaned percent milestones would
			// warn on every portfolio read about rows nobody is even shown.
			continue
		}
		// The effective amount is the invoice plan's own, degrading to no
		// amount for a milestone nobody can price rather than failing a whole
		// portfolio over one row.
		amount, err := s.milestoneEffective(ctx, m, project)
		if err != nil {
			return portfolioRow{}, err
		}
		if next {
			out.row.NextMilestone = &gen.ProjectEconomyNextMilestone{
				Id:              m.ID,
				Name:            m.Name,
				Status:          m.Status,
				PlannedDate:     dateFromPgtype(m.PlannedDate),
				EffectiveAmount: amount,
				Overdue:         milestoneOverdue(m, now),
			}
		}
		if !ready {
			continue
		}
		out.row.ReadyCount++
		if amount == nil || !seesAmounts {
			continue
		}
		if out.ready == nil {
			out.ready = new(big.Rat)
		}
		out.ready.Add(out.ready, exactCents(*amount))
	}
	out.row.ReadyAmount = numberPtr(out.ready)
	return out, nil
}

// portfolioActualsOf is a row's actuals: the three buckets and their totals,
// and no more — the unpriced, billable and non-billable splits belong to the
// per-project read, which is about one project rather than hundreds.
func portfolioActualsOf(w loggedWork, seesAmounts bool) gen.ProjectEconomyRowActuals {
	bucket := func(b bucketSum) gen.ProjectEconomyBucket {
		out := gen.ProjectEconomyBucket{Hours: decimalNumber(b.Hours)}
		if seesAmounts {
			out.Amount = numberPtr(b.Amount)
		}
		return out
	}
	actuals := gen.ProjectEconomyRowActuals{
		Approved:   bucket(w.Bill.Approved),
		Submitted:  bucket(w.Bill.Submitted),
		Draft:      bucket(w.Bill.Draft),
		TotalHours: decimalNumber(w.Bill.totalHours()),
	}
	if seesAmounts {
		actuals.TotalAmount = numberPtr(w.Bill.totalAmount())
	}
	return actuals
}

// filterPortfolio applies the two filters no query of this module could: over
// budget, and having something ready to invoice. Both are "true keeps only
// these"; false and absent are the same thing, because "show me the projects
// that are not over budget" is not a question a portfolio is read with.
func filterPortfolio(rows []portfolioRow, p gen.GetProjectsEconomyParams) []portfolioRow {
	overBudget := p.OverBudget != nil && *p.OverBudget
	hasReady := p.HasReady != nil && *p.HasReady
	if !overBudget && !hasReady {
		return rows
	}
	kept := make([]portfolioRow, 0, len(rows))
	for _, row := range rows {
		if overBudget && !row.row.OverBudget {
			continue
		}
		if hasReady && row.row.ReadyCount == 0 {
			continue
		}
		kept = append(kept, row)
	}
	return kept
}

// portfolioTotals is what the filtered set adds up to, taken before the page
// is cut. readyAmounts is one sum per currency, by currency code: two
// currencies never add up, and a single figure over a mixed portfolio would
// be a number in neither.
func portfolioTotals(rows []portfolioRow) gen.ProjectEconomyTotals {
	totals := gen.ProjectEconomyTotals{
		ProjectCount: int32(len(rows)),
		ReadyAmounts: []gen.ProjectEconomyReadyAmount{},
	}
	amounts := map[string]*big.Rat{}
	for _, row := range rows {
		if row.row.OverBudget {
			totals.OverBudgetCount++
		}
		totals.ReadyCount += row.row.ReadyCount
		if row.ready == nil {
			continue
		}
		currency := portfolioCurrency(row)
		sum, ok := amounts[currency]
		if !ok {
			sum = new(big.Rat)
			amounts[currency] = sum
		}
		sum.Add(sum, row.ready)
	}
	for _, currency := range slices.Sorted(maps.Keys(amounts)) {
		totals.ReadyAmounts = append(totals.ReadyAmounts, gen.ProjectEconomyReadyAmount{
			Currency: currency, Amount: decimalNumber(amounts[currency]),
		})
	}
	return totals
}

// portfolioCurrency is a row's currency, "" for a project that carries none —
// which is also a project that can carry no ready amount, so the empty key
// never reaches the totals.
func portfolioCurrency(row portfolioRow) string {
	if row.project.Currency == nil {
		return ""
	}
	return *row.project.Currency
}

// sortPortfolio orders the whole filtered set. Every order breaks its ties by
// project code, which is unique, so a page is the same page however many
// times it is asked for — a portfolio whose row order wobbled between
// requests would shuffle rows across page boundaries and hide some entirely.
func sortPortfolio(rows []portfolioRow, sort *string) {
	name := portfolioSortBudgetUsed
	if sort != nil && *sort != "" {
		name = *sort
	}
	slices.SortStableFunc(rows, func(a, b portfolioRow) int {
		if c := comparePortfolio(name, a, b); c != 0 {
			return c
		}
		return strings.Compare(a.project.Code, b.project.Code)
	})
}

func comparePortfolio(name string, a, b portfolioRow) int {
	switch name {
	case portfolioSortReadyAmount:
		return compareReadyAmount(a, b)
	case portfolioSortNextMilestone:
		return compareNextMilestone(a, b)
	case portfolioSortCode:
		return 0
	default:
		return compareBudgetUsed(a, b)
	}
}

// compareBudgetUsed is most-used first, on the exact ratio rather than on the
// rounded percent — two projects whose percentages both print 99.9 are not
// the same project — with the ones that have no basis to measure against
// last. They are not at 0 %: nothing can be a share of nothing, and sorting
// them as zero would bury a project nobody has budgeted among the ones that
// are comfortably inside theirs.
func compareBudgetUsed(a, b portfolioRow) int {
	switch {
	case a.ratio == nil && b.ratio == nil:
		return 0
	case a.ratio == nil:
		return 1
	case b.ratio == nil:
		return -1
	default:
		return -a.ratio.Cmp(b.ratio)
	}
}

// compareReadyAmount is by currency code first and the largest amount second.
// Amounts in different currencies are not comparable — 100 EUR against
// 1 000 NOK is a question about exchange rates, which this module does not
// answer — so they are grouped rather than interleaved, and the reader sees
// each currency's largest first. Projects with nothing priced and ready come
// last whatever their currency.
func compareReadyAmount(a, b portfolioRow) int {
	switch {
	case a.ready == nil && b.ready == nil:
		return 0
	case a.ready == nil:
		return 1
	case b.ready == nil:
		return -1
	}
	if c := strings.Compare(portfolioCurrency(a), portfolioCurrency(b)); c != 0 {
		return c
	}
	return -a.ready.Cmp(b.ready)
}

// compareNextMilestone is soonest first: dated open milestones in date order,
// then the ones nobody has dated, then the projects with no open milestone at
// all. An undated milestone is not overdue and not imminent, and a project
// with nothing open needs no date at all — neither belongs among the dates.
func compareNextMilestone(a, b portfolioRow) int {
	next, other := a.row.NextMilestone, b.row.NextMilestone
	switch {
	case next == nil && other == nil:
		return 0
	case next == nil:
		return 1
	case other == nil:
		return -1
	}
	switch {
	case next.PlannedDate == nil && other.PlannedDate == nil:
		return 0
	case next.PlannedDate == nil:
		return 1
	case other.PlannedDate == nil:
		return -1
	}
	return next.PlannedDate.Compare(other.PlannedDate.Time)
}

// portfolioPage is the slice of rows one page asks for. The offset is
// computed in int rather than int32: a page number is not bounded by
// anything, and page × pageSize overflowing would wrap into a negative
// offset.
func portfolioPage(rows []portfolioRow, page, pageSize int32) []portfolioRow {
	from := (int(page) - 1) * int(pageSize)
	if from >= len(rows) {
		return nil
	}
	return rows[from:min(from+int(pageSize), len(rows))]
}

// portfolioData is the page as the contract carries it, with the customer
// names resolved — for the page alone, and once per *distinct* customer on
// it, the way the project list resolves them. A portfolio of two thousand
// projects therefore costs the customer directory nothing beyond the
// twenty-five rows somebody is actually reading.
func (s *server) portfolioData(ctx context.Context, rows []portfolioRow) ([]gen.ProjectEconomyRow, error) {
	projects := make([]store.ProjectsProject, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, row.project)
	}
	names, err := s.customerNamesForPage(ctx, projects)
	if err != nil {
		return nil, err
	}
	data := make([]gen.ProjectEconomyRow, 0, len(rows))
	for _, row := range rows {
		out := row.row
		if row.project.CustomerID != nil {
			out.Project.Customer = &gen.ProjectEconomyCustomer{
				Id: *row.project.CustomerID, Name: names[row.project.ID],
			}
		}
		data = append(data, out)
	}
	return data, nil
}
