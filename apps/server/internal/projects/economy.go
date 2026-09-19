package projects

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a project's economy (design §5, delivery B): what it budgeted
// beside what has actually been logged against it. Four things run through
// it.
//
// **Projects owns the view, Time owns the hours.** Nothing here reads a time
// entry — depguard would not allow it and no SQL crosses the schema. The
// hours arrive through contracts.ProjectActuals (deps.Actuals), which is
// optional: nil is an installation without time tracking, and the answer is
// then the budget and the invoice plan with nothing to compare them against
// (timeTracking: false), never zeroes pretending nobody logged anything.
//
// **One call, no lock, no transaction.** The provider is asked exactly once
// per request, after every row this module needs has been read and outside
// any transaction at all: this read takes no lock, so nothing another writer
// wants is held while another module's pool is waited on (design §3.3's rule,
// applied to the one read that could most easily break it). A failure to
// reach it fails the request — a 500 — because a budget compared against
// zeroes is a wrong answer, not a degraded one.
//
// **Shaping by absence.** Hours are planning data: everyone who can see the
// project sees them. Amounts, the currency, the fixed price and the milestone
// totals need financial rights on the project, and the cost block needs
// projects:view-costs on top of them (design §2 E7). What a caller may not
// see is simply not in their copy — absent, never null and never zero, the
// module's rule everywhere else.
//
// **One definition of the arithmetic.** Every percentage, ratio and sum is
// economy_math.go's, and the milestone totals are milestonePlanTotals' — the
// same function GET /{id}/milestones answers from — so no surface of this
// feature can disagree with another about what a project has used or planned.

// GetProjectsByIdEconomy Get a project's budget against what has been logged on it
// (GET /api/v1/projects/{id}/economy)
//
// Visible to everyone who can see the project; an outsider gets the bare 404
// an unknown id gets (D7). There is no 403 of its own: a caller who may not
// see the money is not refused, they are answered without it.
func (s *server) GetProjectsByIdEconomy(ctx context.Context, req gen.GetProjectsByIdEconomyRequestObject) (gen.GetProjectsByIdEconomyResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdEconomy404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdEconomy404Response{}, nil
	}

	// Every line of the project, deactivated ones included and in the order
	// the billing tab lists them: a line that was switched off still has
	// hours logged against it and a budget those hours were measured against.
	lines, err := q.ListBillingLines(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list the project's billing lines: %w", err)
	}
	estimate, err := q.ProjectTaskEstimateHours(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: sum the project's task estimates: %w", err)
	}
	// The invoice plan is read only for a caller who may see it, so a member's
	// request does not pay for rows their answer cannot carry.
	var milestones []store.ProjectsBillingMilestone
	if a.CanSeeFinancials {
		if milestones, err = q.ListProjectMilestones(ctx, project.ID); err != nil {
			return nil, fmt.Errorf("projects: list the project's milestones: %w", err)
		}
	}

	logged, err := s.projectActuals(ctx, project)
	if err != nil {
		return nil, err
	}

	resp, err := s.economyResponse(ctx, project, a, lines, milestones, estimate, logged)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsByIdEconomy200JSONResponse(resp), nil
}

// projectActuals is the one call into the module that owns the hours. It
// answers nil for an installation without time tracking — deps.Actuals is the
// optional contract, exactly as deps.Products is (D10) — and an error for a
// provider that could not answer, which the handler turns into a 500 rather
// than into a project that has apparently had nothing logged on it.
//
// The currency is the project's own, nil when it has none: the contract's
// caller owns that fact, because a provider asking the project directory
// while serving Projects' own request would be a module cycle at request
// time. With no currency no amounts are summed at all.
func (s *server) projectActuals(ctx context.Context, project store.ProjectsProject) (*contracts.ProjectActualsEntry, error) {
	if s.deps.Actuals == nil {
		return nil, nil
	}
	entry, err := s.deps.Actuals.Actuals(ctx, contracts.ActualsRequest{
		ProjectID: project.ID,
		Currency:  project.Currency,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: read what has been logged on the project: %w", err)
	}
	return &entry, nil
}

// economyResponse assembles the answer for one caller. seesAmounts is the
// whole of the financial shaping: financial rights on the project *and* a
// currency to denominate an amount in, since a number in no currency is a
// number nobody can read.
func (s *server) economyResponse(
	ctx context.Context,
	project store.ProjectsProject,
	a access,
	lines []store.ProjectsBillingLine,
	milestones []store.ProjectsBillingMilestone,
	estimate pgtype.Numeric,
	logged *contracts.ProjectActualsEntry,
) (gen.ProjectEconomyResponse, error) {
	seesAmounts := a.CanSeeFinancials && project.Currency != nil

	budget, basis, err := economyBudget(project, lines, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	taskEstimate, err := floatPtrFromNumeric(estimate)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}

	resp := gen.ProjectEconomyResponse{
		TimeTracking:      logged != nil,
		Budget:            budget,
		Lines:             []gen.ProjectEconomyLine{},
		TaskEstimateHours: taskEstimate,
	}
	if seesAmounts {
		resp.Currency = project.Currency
	}
	if a.CanSeeFinancials {
		_, totals, err := s.milestonePlanTotals(ctx, project, milestones)
		if err != nil {
			return gen.ProjectEconomyResponse{}, err
		}
		resp.Milestones = &totals
	}

	if logged == nil {
		// Without time tracking there is nothing to compare, so the lines
		// carry their budgets and no actuals, and budgetUsed is absent rather
		// than zero — a project nobody has logged against and a project this
		// installation cannot see the hours of are different answers.
		resp.Lines = economyLinesWithoutActuals(lines, seesAmounts)
		return resp, nil
	}

	totals, err := loggedWorkOf(logged.Totals)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	actuals, err := economyActuals(totals, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	resp.Actuals = &actuals

	if use, ok := budgetUsed(basis, totals.Bill); ok {
		resp.BudgetUsed = &gen.ProjectEconomyBudgetUsed{
			Basis:           use.Basis,
			Percent:         use.Percent,
			ApprovedPercent: use.ApprovedPercent,
		}
		resp.OverBudget = use.OverBudget
	}
	if a.canSeeCosts() && project.Currency != nil {
		resp.Cost = economyCost(totals)
	}

	resp.Lines, err = economyLines(lines, logged.Lines, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyResponse{}, err
	}
	return resp, nil
}

// economyBudget is the planning half of the answer, and the basis the
// comparison will be made against — one read of the project's numbers, so the
// figure shown and the figure divided by can never be two different things.
//
// Hours are outside the shaping, exactly as the project's own budgetHours is
// (D12): they are planning data. The lines' sums count deactivated lines,
// because an hour logged last month against a line switched off since was
// still measured against that line's budget.
func economyBudget(project store.ProjectsProject, lines []store.ProjectsBillingLine, seesAmounts bool) (gen.ProjectEconomyBudget, budgetBasis, error) {
	basis, err := projectBudgetBasis(project, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyBudget{}, budgetBasis{}, err
	}
	hours, amount, fixedPrice := basis.Hours, basis.Amount, basis.FixedPrice
	linesHours, err := sumNumerics(lines, func(l store.ProjectsBillingLine) pgtype.Numeric { return l.BudgetHours })
	if err != nil {
		return gen.ProjectEconomyBudget{}, budgetBasis{}, err
	}
	linesAmount, err := sumNumerics(lines, func(l store.ProjectsBillingLine) pgtype.Numeric { return l.BudgetAmount })
	if err != nil {
		return gen.ProjectEconomyBudget{}, budgetBasis{}, err
	}

	budget := gen.ProjectEconomyBudget{
		Hours:      numberPtr(hours),
		LinesHours: numberPtr(linesHours),
	}
	if seesAmounts {
		budget.Amount = numberPtr(amount)
		budget.FixedPrice = numberPtr(fixedPrice)
		budget.LinesAmount = numberPtr(linesAmount)
	}
	return budget, basis, nil
}

// projectBudgetBasis is one project's three candidate bases read out of its
// row, exactly, and the two facts that decide which of them may be used. It
// is the only place a project row becomes a budgetBasis, so the project's own
// economy, the portfolio's rows and the dashboard's budget alerts all measure
// against the same three numbers — a portfolio that picked the fixed price
// where the project's page picked the budget amount would be two answers to
// one question.
func projectBudgetBasis(project store.ProjectsProject, seesAmounts bool) (budgetBasis, error) {
	hours, err := exactNumeric(project.BudgetHours)
	if err != nil {
		return budgetBasis{}, err
	}
	amount, err := exactNumeric(project.BudgetAmount)
	if err != nil {
		return budgetBasis{}, err
	}
	fixedPrice, err := exactNumeric(project.FixedPriceAmount)
	if err != nil {
		return budgetBasis{}, err
	}
	return budgetBasis{
		Amount:      amount,
		FixedPrice:  fixedPrice,
		Hours:       hours,
		BillingType: project.BillingType,
		SeesAmounts: seesAmounts,
	}, nil
}

// economyActuals renders one subject's logged work. Every hour figure is
// there for anyone who gets an answer at all; the amounts ride on
// seesAmounts, one field at a time, so a bucket always says how many hours
// are in it even when it may not say what they bill.
func economyActuals(w loggedWork, seesAmounts bool) (gen.ProjectEconomyActuals, error) {
	bucket := func(b bucketSum) gen.ProjectEconomyBucket {
		out := gen.ProjectEconomyBucket{Hours: decimalNumber(b.Hours)}
		if seesAmounts {
			out.Amount = numberPtr(b.Amount)
		}
		return out
	}
	lastEntryDate, err := economyDate(w.LastEntryDate)
	if err != nil {
		return gen.ProjectEconomyActuals{}, err
	}
	actuals := gen.ProjectEconomyActuals{
		Approved:         bucket(w.Bill.Approved),
		Submitted:        bucket(w.Bill.Submitted),
		Draft:            bucket(w.Bill.Draft),
		TotalHours:       decimalNumber(w.Bill.totalHours()),
		UnpricedHours:    decimalNumber(exactHours(w.UnpricedHundredths)),
		BillableHours:    decimalNumber(exactHours(w.BillableHundredths)),
		NonBillableHours: decimalNumber(exactHours(w.NonBillableHundredths)),
		LastEntryDate:    lastEntryDate,
	}
	if seesAmounts {
		actuals.TotalAmount = numberPtr(w.Bill.totalAmount())
	}
	return actuals, nil
}

// economyCost is design §2 E7's block, reached only by a caller who has both
// halves of canSeeCosts. The margin is the bill total minus the cost total,
// both in the project's currency; uncostedHours is what the cost leaves out,
// so a surface never presents a margin as complete when it is short by the
// cost of hours nobody carded.
func economyCost(w loggedWork) *gen.ProjectEconomyCost {
	total := w.Cost.totalAmount()
	return &gen.ProjectEconomyCost{
		Approved:      decimalNumber(w.Cost.Approved.Amount),
		Submitted:     decimalNumber(w.Cost.Submitted.Amount),
		Draft:         decimalNumber(w.Cost.Draft.Amount),
		Total:         decimalNumber(total),
		Margin:        decimalNumber(new(big.Rat).Sub(w.Bill.totalAmount(), total)),
		UncostedHours: decimalNumber(exactHours(w.UncostedHundredths)),
	}
}

// economyLines is the per-line breakdown: every billing line of the project
// in the billing tab's order, each with what has been logged against it, and
// one row without a billingLineId for work logged against no line.
//
// The provider knows only what was logged, so its list holds only lines
// anything is on; a line with nothing logged is therefore built from a zero
// rather than left out, which is what lets a line show a budget and an empty
// bar. In the other direction, work the provider attributes to a line this
// project does not have — which should not happen, and would mean a line was
// deleted, which this module never does — folds into the no-line row rather
// than being dropped: an hour somebody logged must appear somewhere.
func economyLines(lines []store.ProjectsBillingLine, reported []contracts.LineActuals, seesAmounts bool) ([]gen.ProjectEconomyLine, error) {
	byLine := make(map[int32]loggedWork, len(reported))
	noLine := zeroWork()
	hasNoLine := false
	known := make(map[int32]bool, len(lines))
	for _, l := range lines {
		known[l.ID] = true
	}
	for _, reportedLine := range reported {
		work, err := loggedWorkOf(reportedLine.Totals)
		if err != nil {
			return nil, err
		}
		if reportedLine.BillingLineID != nil && known[*reportedLine.BillingLineID] {
			byLine[*reportedLine.BillingLineID] = work
			continue
		}
		noLine, hasNoLine = noLine.add(work), true
	}

	out := make([]gen.ProjectEconomyLine, 0, len(lines)+1)
	for _, line := range lines {
		work, ok := byLine[line.ID]
		if !ok {
			work = zeroWork()
		}
		row, err := economyLine(&line, work, seesAmounts)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	// The no-line row is last, the order the provider itself reports its
	// lines in, and it exists only when something was actually logged without
	// a line — an empty one would read as a line somebody created.
	if hasNoLine && noLine.logged() {
		row, err := economyLine(nil, noLine, seesAmounts)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// economyLine renders one row. line is nil for the no-line row, which has no
// code, no active flag and no budget of its own — only what was logged.
func economyLine(line *store.ProjectsBillingLine, work loggedWork, seesAmounts bool) (gen.ProjectEconomyLine, error) {
	actuals, err := economyActuals(work, seesAmounts)
	if err != nil {
		return gen.ProjectEconomyLine{}, err
	}
	row := gen.ProjectEconomyLine{Actuals: &actuals}
	if line == nil {
		return row, nil
	}

	budgetHours, err := exactNumeric(line.BudgetHours)
	if err != nil {
		return gen.ProjectEconomyLine{}, err
	}
	budgetAmount, err := exactNumeric(line.BudgetAmount)
	if err != nil {
		return gen.ProjectEconomyLine{}, err
	}
	id, code, active := line.ID, line.Code, line.Active
	row.BillingLineId, row.Code, row.Active = &id, &code, &active
	row.BudgetHours = numberPtr(budgetHours)
	if seesAmounts {
		row.BudgetAmount = numberPtr(budgetAmount)
	}
	if use, ok := lineBudgetUsed(budgetAmount, budgetHours, seesAmounts, work.Bill); ok {
		percent := use.Percent
		row.UsedPercent, row.OverBudget = &percent, use.OverBudget
	}
	// Only a budget in hours has a remainder in hours to report; a line
	// budgeted in money says how much of it is used and nothing more.
	if positive(budgetHours) {
		left := remainingHours(budgetHours, work.Bill.totalHours())
		row.RemainingHours = &left
	}
	return row, nil
}

// economyLinesWithoutActuals is the line list for an installation with no
// time tracking: the budgets, and no comparison at all. There is no no-line
// row, because nothing reported any work to put in it.
func economyLinesWithoutActuals(lines []store.ProjectsBillingLine, seesAmounts bool) []gen.ProjectEconomyLine {
	out := make([]gen.ProjectEconomyLine, 0, len(lines))
	for _, line := range lines {
		id, code, active := line.ID, line.Code, line.Active
		row := gen.ProjectEconomyLine{BillingLineId: &id, Code: &code, Active: &active}
		// Errors here are impossible in practice and already reported by the
		// budget block, which read the same kind of column a moment ago; a
		// line whose budget cannot be read is still worth listing.
		if hours, err := exactNumeric(line.BudgetHours); err == nil {
			row.BudgetHours = numberPtr(hours)
		}
		if seesAmounts {
			if amount, err := exactNumeric(line.BudgetAmount); err == nil {
				row.BudgetAmount = numberPtr(amount)
			}
		}
		out = append(out, row)
	}
	return out
}

// economyDate turns the contract's YYYY-MM-DD text into the contract's date.
// A text that is not one is an error rather than an absent date: the provider
// promises the format, and silently dropping the day work was last logged
// would read as a project nobody has touched.
func economyDate(text *string) (*openapi_types.Date, error) {
	if text == nil {
		return nil, nil
	}
	day, err := time.Parse(time.DateOnly, *text)
	if err != nil {
		return nil, fmt.Errorf("projects: %q is not a calendar date: %w", *text, err)
	}
	return &openapi_types.Date{Time: day}, nil
}

// exactNumeric is a stored decimal as the exact rational its column holds,
// nil for SQL NULL — numericText's reading rather than floatPtrFromNumeric's,
// so a number that is about to be divided by never passes through float64.
func exactNumeric(n pgtype.Numeric) (*big.Rat, error) {
	text, ok, err := numericText(n)
	if err != nil || !ok {
		return nil, err
	}
	r, valid := new(big.Rat).SetString(text)
	if !valid {
		return nil, fmt.Errorf("projects: %q is not a stored decimal", text)
	}
	return r, nil
}

// sumNumerics adds one column across a project's billing lines, exactly, and
// answers nil when no line carries it — "no line has a budget" and "the
// budgets add up to nothing" are different things, and only the first is an
// absent field.
func sumNumerics(lines []store.ProjectsBillingLine, column func(store.ProjectsBillingLine) pgtype.Numeric) (*big.Rat, error) {
	var sum *big.Rat
	for _, line := range lines {
		value, err := exactNumeric(column(line))
		if err != nil {
			return nil, err
		}
		if value == nil {
			continue
		}
		if sum == nil {
			sum = new(big.Rat)
		}
		sum.Add(sum, value)
	}
	return sum, nil
}

// numberPtr is an optional exact decimal as the contract's optional JSON
// number: absent for a value nobody set, never 0.
func numberPtr(r *big.Rat) *float64 {
	if r == nil {
		return nil
	}
	n := decimalNumber(r)
	return &n
}
