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
// **Three reads and two calls, whatever the size of the portfolio.** One
// query for the matching projects, one call into the module that owns the
// hours and one into the module that owns the expenses — each for all of them
// at once — one grouped read of their open milestones, and the customer names
// of the *page* only. Nothing is per row, no transaction is opened and no
// lock is held while another module's pool is waited on.
//
// **A cap rather than a truncation.** The actuals contract refuses a batch
// past contracts.MaxActualsRequests, and the expenses contract's own cap is
// deliberately that same number, so more matching projects than that is a
// 400 asking for a narrower filter — once, for both. Answering the first two
// thousand would mean totals computed over part of the set — a wrong number
// rather than a missing one.
//
// **Ready to invoice means both halves.** readyAmount and readyCount keep
// meaning milestones; the expense lines are counted and priced beside them,
// and readyTotalAmount is the two together — which is what the ready order is
// taken on and what the hasReady filter asks about. Nothing from the expenses
// contract touches budgetUsed or overBudget here either (X12).
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
	spent, err := s.portfolioExpenses(ctx, projects)
	if err != nil {
		return nil, err
	}
	milestones, err := q.OpenMilestonesForProjects(ctx, projectIDs(projects))
	if err != nil {
		return nil, fmt.Errorf("projects: read the portfolio's open milestones: %w", err)
	}

	expenseTracking := s.deps.Expenses != nil
	rows, err := s.portfolioRows(ctx, projects, logged, spent, milestones, s.deps.Actuals != nil, expenseTracking)
	if err != nil {
		return nil, err
	}
	rows = filterPortfolio(rows, req.Params)
	totals := portfolioTotals(rows, expenseTracking)
	sortPortfolio(rows, req.Params.Sort)

	data, err := s.portfolioData(ctx, portfolioPage(rows, page, pageSize))
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsEconomy200JSONResponse{
		Data:            data,
		Pagination:      apicommon.Pagination(page, pageSize, int32(len(rows))),
		TimeTracking:    s.deps.Actuals != nil,
		ExpenseTracking: expenseTracking,
		Totals:          totals,
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
	totals, err := s.actualsForProjects(ctx, reqs)
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

// portfolioExpenses is the one call into the module that owns the expenses:
// the same projects the actuals batch covers, in one batch, outside any
// transaction. It answers nil for an installation without expense tracking,
// which is not "nothing has been spent".
//
// There is no second cap. portfolioMaxProjects is contracts.MaxActualsRequests
// and contracts.MaxExpensesProjects is deliberately the same number, so the
// set that got past the 400 above is a set both providers will accept — a
// caller who narrowed their filter once never has to narrow it again for the
// other module.
//
// The duplicate guard is the actuals batch's, for the actuals batch's reason:
// the read these ids come from returns one row per project, and the contract
// makes naming a project twice an error rather than a resolvable ambiguity.
func (s *server) portfolioExpenses(ctx context.Context, projects []store.ProjectsProject) (map[int32]contracts.ProjectExpenseTotals, error) {
	if s.deps.Expenses == nil || len(projects) == 0 {
		return nil, nil
	}
	ids := make([]int32, 0, len(projects))
	seen := make(map[int32]bool, len(projects))
	for _, project := range projects {
		if seen[project.ID] {
			continue
		}
		seen[project.ID] = true
		ids = append(ids, project.ID)
	}
	recorded, err := s.expensesForProjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: read what the portfolio's projects have spent: %w", err)
	}
	return recorded, nil
}

// portfolioRow is one row while it is still being decided: the contract's row
// beside the exact figures the orders are taken on. All of them are kept as
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
	// readyExpense is what its ready expense lines add up to in its own
	// currency, nil when it has none ready; readyTotal is the two together,
	// which is what the ready order is taken on. Without expense tracking
	// readyTotal is simply ready, so the order is the one it has always been.
	readyExpense *big.Rat
	readyTotal   *big.Rat
}

// readyExpenseCount is how many expense lines this row has ready, 0 for an
// installation that cannot say — the filter treats the two the same, because
// a row that cannot say has nothing to keep it in.
func (r portfolioRow) readyExpenseCount() int32 {
	if r.row.ReadyExpenseCount == nil {
		return 0
	}
	return *r.row.ReadyExpenseCount
}

// portfolioRows builds every matching project's row. The milestones arrive as
// one ordered list for the whole set and are grouped here rather than read
// per project; their order is already the "next milestone" rule's, so the
// first of each group is that project's next one.
func (s *server) portfolioRows(
	ctx context.Context,
	projects []store.ProjectsProject,
	logged map[int32]loggedWork,
	spent map[int32]contracts.ProjectExpenseTotals,
	milestones []store.ProjectsBillingMilestone,
	timeTracking bool,
	expenseTracking bool,
) ([]portfolioRow, error) {
	open := make(map[int32][]store.ProjectsBillingMilestone, len(projects))
	for _, m := range milestones {
		open[m.ProjectID] = append(open[m.ProjectID], m)
	}
	now := s.deps.Clock()
	rows := make([]portfolioRow, 0, len(projects))
	for _, project := range projects {
		// A project with nothing recorded is absent from the expenses
		// provider's map, so the zero value here is its answer: nothing
		// recorded, which is not the same as the installation having no
		// expenses module — that is what expenseTracking says.
		row, err := s.portfolioRowFor(ctx, project, logged[project.ID], spent[project.ID],
			open[project.ID], timeTracking, expenseTracking, now)
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
	spent contracts.ProjectExpenseTotals,
	open []store.ProjectsBillingMilestone,
	timeTracking bool,
	expenseTracking bool,
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

	// What is ready to invoice among the project's expenses, in the project's
	// own currency and no other: a receipt in another currency is the
	// per-project read's business, because a portfolio row is one line of a
	// table and a second currency in it would be a number nobody could add up.
	// readyAmount and readyCount keep meaning milestones; readyTotalAmount is
	// the two halves together and is what the ready order is taken on.
	out.readyTotal = out.ready
	if expenseTracking {
		// Only what is ready, and only in the row's own currency: a portfolio
		// row publishes two figures of the contract's answer, so it reads two
		// rather than parsing every bucket of every currency to throw the rest
		// away — and a malformed figure in a currency this table would never
		// print cannot then fail the whole page.
		ready, amount, err := readyExpensesOf(spent, project.Currency)
		if err != nil {
			return portfolioRow{}, err
		}
		count := int32(0)
		if ready > 0 {
			count, out.readyExpense = int32(ready), amount
		}
		out.row.ReadyExpenseCount = &count
		out.row.ReadyExpenseAmount = numberPtr(out.readyExpense)
		out.readyTotal = addReady(out.ready, out.readyExpense)
		out.row.ReadyTotalAmount = numberPtr(out.readyTotal)
	}
	return out, nil
}

// addReady is the two halves of "ready to invoice" added exactly, nil when
// neither half is there at all — a project with nothing ready has no total
// rather than a total of nothing, exactly as it has no readyAmount.
func addReady(milestones, expenses *big.Rat) *big.Rat {
	switch {
	case milestones == nil:
		return expenses
	case expenses == nil:
		return milestones
	default:
		return new(big.Rat).Add(milestones, expenses)
	}
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
//
// hasReady asks "is there anything to invoice here", so a project whose only
// ready thing is a billable receipt is kept: it is something to put on an
// invoice, and a filter that hid it would send whoever invoices past the very
// project they are looking for. On an installation without expense tracking
// the count is always 0 and the filter is the one it has always been.
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
		if hasReady && row.row.ReadyCount == 0 && row.readyExpenseCount() == 0 {
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
//
// Each currency's three figures are added **exactly across the rows** and
// rounded once at the end, never from the rows' own rounded figures — three
// rows worth half a cent each are 0.01 apiece on screen and 0.02 altogether.
//
// A currency appears when either half has something in it, so a portfolio
// whose only EUR project has nothing but a billable receipt still gets a EUR
// entry; its milestone `amount` is then 0, which is a sum over no milestones
// rather than a missing figure. Without expense tracking neither the expense
// figures nor the count appear at all, and the list is the one it has always
// been.
func portfolioTotals(rows []portfolioRow, expenseTracking bool) gen.ProjectEconomyTotals {
	totals := gen.ProjectEconomyTotals{
		ProjectCount: int32(len(rows)),
		ReadyAmounts: []gen.ProjectEconomyReadyAmount{},
	}
	amounts, expenseAmounts := map[string]*big.Rat{}, map[string]*big.Rat{}
	add := func(into map[string]*big.Rat, currency string, amount *big.Rat) {
		sum, ok := into[currency]
		if !ok {
			sum = new(big.Rat)
			into[currency] = sum
		}
		sum.Add(sum, amount)
	}
	readyExpenseCount := int32(0)
	for _, row := range rows {
		if row.row.OverBudget {
			totals.OverBudgetCount++
		}
		totals.ReadyCount += row.row.ReadyCount
		readyExpenseCount += row.readyExpenseCount()
		currency := portfolioCurrency(row)
		if row.ready != nil {
			add(amounts, currency, row.ready)
		}
		if row.readyExpense != nil {
			add(expenseAmounts, currency, row.readyExpense)
		}
	}
	if expenseTracking {
		totals.ReadyExpenseCount = &readyExpenseCount
	}

	currencies := slices.Sorted(maps.Keys(amounts))
	if expenseTracking {
		for currency := range expenseAmounts {
			if _, ok := amounts[currency]; !ok {
				currencies = append(currencies, currency)
			}
		}
		slices.Sort(currencies)
	}
	for _, currency := range currencies {
		entry := gen.ProjectEconomyReadyAmount{
			Currency: currency, Amount: decimalNumber(orZero(amounts[currency])),
		}
		if expenseTracking {
			expenseAmount := decimalNumber(orZero(expenseAmounts[currency]))
			total := decimalNumber(new(big.Rat).Add(orZero(amounts[currency]), orZero(expenseAmounts[currency])))
			entry.ExpenseAmount, entry.TotalAmount = &expenseAmount, &total
		}
		totals.ReadyAmounts = append(totals.ReadyAmounts, entry)
	}
	return totals
}

// orZero is an absent sum as the exact zero it stands for, for the one place
// a currency is in the list because of its *other* half.
func orZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
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
//
// The amount compared is readyTotalAmount — milestones and expenses together
// — because the order answers "where is the most to invoice", and a project
// whose receipts are worth more than another's milestone belongs above it.
// Without expense tracking readyTotal is the milestone figure and the order
// is unchanged.
func compareReadyAmount(a, b portfolioRow) int {
	switch {
	case a.readyTotal == nil && b.readyTotal == nil:
		return 0
	case a.readyTotal == nil:
		return 1
	case b.readyTotal == nil:
		return -1
	}
	if c := strings.Compare(portfolioCurrency(a), portfolioCurrency(b)); c != 0 {
		return c
	}
	return -a.readyTotal.Cmp(b.readyTotal)
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
