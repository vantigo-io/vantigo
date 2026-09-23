package customers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is GET /customers/{id}/overview (customer 360 design D1, D2): what
// is going on with one customer across the modules that know — its projects,
// the work approved on them and not yet billed, the expenses ready to invoice,
// and when anything last happened. It is the first place this module reads
// another module's data, and it reads it the way every consumer does: through
// the three contracts Compose hands every module (Deps.Projects, Deps.Actuals,
// Deps.Expenses), each nil when its module is off — a state this endpoint
// answers by leaving the section out, never by failing.
//
// Three rules shape it.
//
// **Shaped, never refused.** Every section but lastActivity is absent when it
// is not for this caller or its module is not installed, and the response
// never says which — a 403 on one tile would make the whole page a permission
// probe. The other modules' keys are read through s.hasPermission exactly as
// this module's own are: a key is a string the access contract answers for
// any module. Cost never appears; the panel is about the customer, not the
// margin.
//
// **No transaction.** Nothing here writes or locks; each contract method is
// asked at most once per request, and the two that take projects take them
// all in one batch. The project directory itself is asked twice for a caller
// who sees only their role projects: the customer's projects, and theirs.
//
// **Exact money, per currency.** Hours are int64 hundredths and subtract
// exactly. Amounts arrive as the contracts' decimal text, are added and
// subtracted in math/big.Rat, and become a JSON number only at the very end —
// the rule projects' economy_math.go states for the same reason: adding money
// in binary floating point is how a sum grows a tenth of a cent. Nothing is
// ever added across currencies.
//
// A contract that fails fails the request: absent must never also mean
// "could not be read".

// The projects module's keys the overview is shaped by (customer 360 design
// D2). They are that module's (internal/projects/module.go); this module only
// asks about them, and none of them admits the request — customers:view does.
const (
	projectsAccess         = "projects:access"
	projectsViewAll        = "projects:view-all"
	projectsManageAll      = "projects:manage-all"
	projectsViewFinancials = "projects:view-financials"
)

// overviewOpenLimit is how many open projects the overview names. Ten is a
// panel's worth; the counts are never cut, and the Projects tab lists them all.
const overviewOpenLimit = 10

// overviewRights is what the caller may see of the other modules, asked once
// per request and only as far as the answer can still change anything.
type overviewRights struct {
	// projects is the projects module on and projects:access held.
	projects bool
	// allProjects is every one of the customer's projects rather than the
	// caller's own role projects: projects:view-all or projects:manage-all.
	allProjects bool
	// financials is money: projects:view-financials or projects:manage-all.
	// That is the global half of the rule projects and expenses grant money
	// by, not all of it: both also show a project's managers their own
	// project's money, and here a manager's role opens nothing, because
	// ProjectsForUser carries no role and money is all-or-nothing per
	// section. Matching them needs each visible project's role and money
	// shaped per project (customer 360 design D2) — a delivery of its own.
	financials bool
}

// overviewRights asks the access contract as little as the answer needs: no
// projects module, or no projects:access, and nothing else is asked at all;
// manage-all answers both of the other two questions at once.
func (s *server) overviewRights(ctx context.Context) overviewRights {
	if s.deps.Projects == nil || !s.hasPermission(ctx, projectsAccess) {
		return overviewRights{}
	}
	if s.hasPermission(ctx, projectsManageAll) {
		return overviewRights{projects: true, allProjects: true, financials: true}
	}
	return overviewRights{
		projects:    true,
		allProjects: s.hasPermission(ctx, projectsViewAll),
		financials:  s.hasPermission(ctx, projectsViewFinancials),
	}
}

// GetCustomersByIdOverview Get a customer's overview across modules
// (GET /api/v1/customers/{id}/overview)
func (s *server) GetCustomersByIdOverview(ctx context.Context, req gen.GetCustomersByIdOverviewRequestObject) (gen.GetCustomersByIdOverviewResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.CustomerExists(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdOverview404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: overview: customer exists: %w", err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: overview: timeline summary: %w", err)
	}
	resp := gen.CustomerOverviewResponse{
		LastActivity: gen.CustomerOverviewLastActivity{TimelineOn: dateFromPgtype(summary.LatestOccurredOn)},
	}

	rights := s.overviewRights(ctx)
	if !rights.projects {
		return gen.GetCustomersByIdOverview200JSONResponse(resp), nil
	}
	visible, truncated, err := s.overviewVisibleProjects(ctx, req.Id, rights.allProjects)
	if err != nil {
		return nil, err
	}

	// Work first, because the open rows are ordered by it.
	var actuals map[int32]contracts.ActualsTotals
	if s.deps.Actuals != nil {
		reqs := make([]contracts.ActualsRequest, 0, len(visible))
		for _, p := range visible {
			// Each project in its own currency (the caller owns the currency
			// fact): one with none is asked for hours only. Currency is a
			// financial field, and it goes no further than this request.
			reqs = append(reqs, contracts.ActualsRequest{ProjectID: p.ID, Currency: p.Currency})
		}
		if actuals, err = s.deps.Actuals.ActualsForProjects(ctx, reqs); err != nil {
			return nil, fmt.Errorf("customers: overview: actuals for projects: %w", err)
		}
		work, err := overviewWork(visible, actuals, rights.financials)
		if err != nil {
			return nil, err
		}
		resp.Work = &work
		resp.LastActivity.WorkOn = work.LastWorkOn
	}
	projects, err := overviewProjectsSection(visible, truncated, actuals)
	if err != nil {
		return nil, err
	}
	resp.Projects = &projects

	if s.deps.Expenses != nil && rights.financials {
		ids := make([]int32, 0, len(visible))
		for _, p := range visible {
			ids = append(ids, p.ID)
		}
		totals, err := s.deps.Expenses.ExpensesForProjects(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("customers: overview: expenses for projects: %w", err)
		}
		expenses, err := overviewExpenses(visible, totals)
		if err != nil {
			return nil, err
		}
		resp.Expenses = &expenses
		resp.LastActivity.ExpenseOn = expenses.LastExpenseOn
	}
	return gen.GetCustomersByIdOverview200JSONResponse(resp), nil
}

// overviewVisibleProjects is the customer's projects the caller may see, and
// whether the directory cut the list at its cap. For view-all or manage-all it
// is all of them; otherwise it is the projects list endpoint's own rule
// restated with two directory calls instead of one per project — the
// customer's projects, kept only where the caller holds a role. The customer's
// list is the one intersected, so the cap means the same thing for both.
//
// A request with no user principal (the router has already refused an
// unauthenticated one, so this is unreachable in production) sees no role
// projects rather than anybody's.
func (s *server) overviewVisibleProjects(ctx context.Context, customerID int32, all bool) ([]contracts.ProjectEntry, bool, error) {
	entries, err := s.deps.Projects.ProjectsForCustomer(ctx, customerID)
	if err != nil {
		return nil, false, fmt.Errorf("customers: overview: projects for customer: %w", err)
	}
	// "Reached the cap", which the directory cannot tell from "exactly the
	// cap": at two thousand projects the difference is not one a panel owes
	// anybody.
	truncated := len(entries) >= contracts.MaxActualsRequests
	if all {
		return entries, truncated, nil
	}
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return []contracts.ProjectEntry{}, truncated, nil
	}
	mine, err := s.deps.Projects.ProjectsForUser(ctx, p.UserID)
	if err != nil {
		return nil, false, fmt.Errorf("customers: overview: projects for user: %w", err)
	}
	held := make(map[int32]bool, len(mine))
	for _, e := range mine {
		held[e.ID] = true
	}
	visible := make([]contracts.ProjectEntry, 0, len(entries))
	for _, e := range entries {
		if held[e.ID] {
			visible = append(visible, e)
		}
	}
	return visible, truncated, nil
}

// overviewProjectsSection is the projects section: the counts over every
// visible project, and the open ones newest work first — a project nobody has
// logged on after every one somebody has, ties by id — cut at
// overviewOpenLimit. The dates compare as their YYYY-MM-DD text, which orders
// exactly as the dates do.
func overviewProjectsSection(visible []contracts.ProjectEntry, truncated bool, actuals map[int32]contracts.ActualsTotals) (gen.CustomerOverviewProjects, error) {
	type openRow struct {
		entry      contracts.ProjectEntry
		lastWorkOn string
	}
	var rows []openRow
	for _, p := range visible {
		if !p.OpenForWork {
			continue
		}
		row := openRow{entry: p}
		if totals, ok := actuals[p.ID]; ok && totals.LastEntryDate != nil {
			row.lastWorkOn = *totals.LastEntryDate
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b openRow) int {
		if c := strings.Compare(b.lastWorkOn, a.lastWorkOn); c != 0 {
			return c // "" sorts after every date, which is "never" last
		}
		return cmp.Compare(a.entry.ID, b.entry.ID)
	})

	section := gen.CustomerOverviewProjects{
		OpenCount:  int32(len(rows)),
		TotalCount: int32(len(visible)),
		Truncated:  truncated,
		Open:       make([]gen.CustomerOverviewProject, 0, min(len(rows), overviewOpenLimit)),
	}
	for _, row := range rows[:min(len(rows), overviewOpenLimit)] {
		project := gen.CustomerOverviewProject{Id: row.entry.ID, Code: row.entry.Code, Name: row.entry.Name, Status: row.entry.Status}
		if row.lastWorkOn != "" {
			on, err := overviewDate(row.lastWorkOn)
			if err != nil {
				return gen.CustomerOverviewProjects{}, err
			}
			project.LastWorkOn = on
		}
		section.Open = append(section.Open, project)
	}
	return section, nil
}

// overviewWork is the work section. Unbilled is Approved less Invoiced
// (customer 360 design D1): the actuals contract puts invoiced work inside
// Approved and reports that part beside it, so the subtraction is exact for
// hours and exact for each project's two published amounts. Amounts are summed
// per project currency, and only for a caller with financial rights — for
// anybody else they are never even parsed. The unpriced hours are the
// contract's figure summed as it stands, for everybody: it is hours, and the
// actuals contract makes surfacing it the price of showing what a project
// will bill, since billable work without a rate in the project's currency is
// in the unbilled hours and in no amount.
func overviewWork(visible []contracts.ProjectEntry, actuals map[int32]contracts.ActualsTotals, financials bool) (gen.CustomerOverviewWork, error) {
	var work gen.CustomerOverviewWork
	unbilled := map[string]*big.Rat{}
	var last string
	var unpriced int64
	for _, p := range visible {
		totals := actuals[p.ID]
		unpriced += totals.UnpricedHoursHundredths
		work.UnbilledHoursHundredths += totals.Approved.HoursHundredths - totals.Invoiced.HoursHundredths
		work.ApprovedHoursHundredths += totals.Approved.HoursHundredths
		work.SubmittedHoursHundredths += totals.Submitted.HoursHundredths
		work.DraftHoursHundredths += totals.Draft.HoursHundredths
		if totals.LastEntryDate != nil && *totals.LastEntryDate > last {
			last = *totals.LastEntryDate
		}
		if !financials || p.Currency == nil {
			continue
		}
		approved, err := overviewAmount(totals.Approved.BillAmount)
		if err != nil {
			return gen.CustomerOverviewWork{}, err
		}
		invoiced, err := overviewAmount(totals.Invoiced.BillAmount)
		if err != nil {
			return gen.CustomerOverviewWork{}, err
		}
		sum := unbilled[*p.Currency]
		if sum == nil {
			sum = new(big.Rat)
			unbilled[*p.Currency] = sum
		}
		sum.Add(sum, approved.Sub(approved, invoiced))
	}
	work.UnpricedHoursHundredths = &unpriced
	if financials {
		amounts := overviewAmounts(unbilled)
		work.UnbilledAmounts = &amounts
	}
	if last != "" {
		on, err := overviewDate(last)
		if err != nil {
			return gen.CustomerOverviewWork{}, err
		}
		work.LastWorkOn = on
	}
	return work, nil
}

// overviewExpenses is the expenses section: the expenses contract's own
// "ready to invoice" figures, added up per currency across the projects it
// was asked about. Only ready lines count; a currency with nothing ready is
// not a currency this customer has anything to invoice in. It walks the
// visible projects in order and looks each up, as overviewWork does, rather
// than ranging over the map: the result is the same either way, but a walk
// whose order Go randomises would let a test pass by luck. A project with
// nothing recorded is absent from totals and adds nothing.
func overviewExpenses(visible []contracts.ProjectEntry, totals map[int32]contracts.ProjectExpenseTotals) (gen.CustomerOverviewExpenses, error) {
	ready := map[string]*big.Rat{}
	var count int64
	var last string
	for _, p := range visible {
		project, ok := totals[p.ID]
		if !ok {
			continue
		}
		if project.LastEntryDate != nil && *project.LastEntryDate > last {
			last = *project.LastEntryDate
		}
		for _, c := range project.Currencies {
			if c.ReadyCount == 0 {
				continue
			}
			amount, err := overviewAmount(c.ReadyAmount)
			if err != nil {
				return gen.CustomerOverviewExpenses{}, err
			}
			sum := ready[c.Currency]
			if sum == nil {
				sum = new(big.Rat)
				ready[c.Currency] = sum
			}
			sum.Add(sum, amount)
			count += c.ReadyCount
		}
	}
	section := gen.CustomerOverviewExpenses{ReadyCount: int32(count), ReadyAmounts: overviewAmounts(ready)}
	if last != "" {
		on, err := overviewDate(last)
		if err != nil {
			return gen.CustomerOverviewExpenses{}, err
		}
		section.LastExpenseOn = on
	}
	return section, nil
}

// overviewAmount reads the decimal text both contracts carry amounts in. An
// empty text is nothing — a bucket a provider left blank has no money in it,
// projects' own exactAmount rule — but text that is not a decimal is an error:
// reading it as zero would report a figure nobody computed.
func overviewAmount(text string) (*big.Rat, error) {
	if text == "" {
		return new(big.Rat), nil
	}
	amount, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("customers: overview: %q is not a decimal amount", text)
	}
	return amount, nil
}

// overviewAmounts renders per-currency sums as the wire's list: by ISO code,
// a currency whose sum is exactly zero left out, and never nil — a required
// array marshals nil as null. Every sum is of two-decimal figures and so has
// two decimals itself; Float64 is the nearest float, whose shortest text is
// those two decimals, and this is the one place a float meets money.
func overviewAmounts(sums map[string]*big.Rat) []gen.CustomerOverviewAmount {
	out := make([]gen.CustomerOverviewAmount, 0, len(sums))
	for currency, sum := range sums {
		if sum.Sign() == 0 {
			continue
		}
		amount, _ := sum.Float64()
		out = append(out, gen.CustomerOverviewAmount{Currency: currency, Amount: amount})
	}
	slices.SortFunc(out, func(a, b gen.CustomerOverviewAmount) int { return strings.Compare(a.Currency, b.Currency) })
	return out
}

// overviewDate reads a contract's YYYY-MM-DD. Text the contract cannot have
// produced is an infrastructure failure, reported rather than dropped.
func overviewDate(text string) (*openapi_types.Date, error) {
	on, err := time.Parse(time.DateOnly, text)
	if err != nil {
		return nil, fmt.Errorf("customers: overview: %q is not a date: %w", text, err)
	}
	return &openapi_types.Date{Time: on}, nil
}
