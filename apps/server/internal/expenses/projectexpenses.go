package expenses

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.ProjectExpenses: what a project's
// expenses cost and bill, for whoever compares them with what was planned —
// projects' economy first, and this module's own project summary beside it.
// It is the mirror of what time/actuals.go does for hours, and it is written
// to the same three rules.
//
// It authorizes nothing: the caller has already decided who may see the
// project and who may see amounts, and the answer carries cost and bill
// together.
//
// It serves from this module's own tables and nothing else. It never asks
// contracts.ProjectDirectory anything while serving — that would be a module
// cycle at request time — which is also why it reports *per currency* instead
// of folding into the project's: a line carries its own currency, the project
// carries the fact of which one is its, and the two meet in the consumer.
// Nothing is converted.
//
// It takes no lock and opens no transaction. Both its callers are reads on a
// page somebody is looking at, and this module's rule is that nothing is
// asked of anyone else from inside a transaction that holds locks; a read
// that holds none can never be the one that breaks it.
//
// Arithmetic: the counts are exact int64 straight from SQL, and the amounts
// are exact decimals — the grouped query sums the unrounded values as numeric
// and hands them over as text, the groups are added up in math/big.Rat, and
// each published figure is rounded once at the end. Rounding every group and
// adding the rounded figures is a different number, and not the one an
// invoice would show.

// projectExpenses is this module's contracts.ProjectExpenses. Like time's
// actuals it is read-only and holds nothing but the queries: Compose builds
// it once, before any module mounts.
type projectExpenses struct{ q *store.Queries }

var _ contracts.ProjectExpenses = (*projectExpenses)(nil)

// newProjectExpenses is Module's Expenses: the constructor Compose calls with
// the dependencies it was given. It takes the pool and nothing else — no
// directory reaches the served answer, and holding one here would only invite
// that.
func newProjectExpenses(d module.Deps) contracts.ProjectExpenses {
	return &projectExpenses{q: store.New(d.Pool)}
}

// ExpensesForProjects is many projects' totals in one query, keyed by project
// id, with a project that has nothing recorded absent from the map.
func (p *projectExpenses) ExpensesForProjects(ctx context.Context, projectIDs []int32) (map[int32]contracts.ProjectExpenseTotals, error) {
	return projectExpenseTotals(ctx, p.q, projectIDs)
}

// projectExpenseTotals is the summation itself, and the one place it lives:
// the contract above and this module's own per-project summary both call it,
// so the figures a project page shows on the Expenses tab and the figures
// Projects reads across the boundary can never drift apart. Project ids in,
// the contract's totals out, keyed by project id and with a project that has
// nothing recorded absent.
//
// It takes the queries rather than a pool so a handler can hand it the
// store it already has. It holds no lock and opens no transaction.
func projectExpenseTotals(ctx context.Context, q *store.Queries, projectIDs []int32) (map[int32]contracts.ProjectExpenseTotals, error) {
	if len(projectIDs) == 0 {
		return map[int32]contracts.ProjectExpenseTotals{}, nil
	}
	if len(projectIDs) > contracts.MaxExpensesProjects {
		return nil, fmt.Errorf("expenses: %d projects asked for at once, at most %d: page the request",
			len(projectIDs), contracts.MaxExpensesProjects)
	}

	// One project has one answer, so naming it twice is the caller's bug: a
	// merge, the first or the last would each be defensible, which is reason
	// enough to answer none of them.
	wanted := make(map[int32]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		if _, seen := wanted[id]; seen {
			return nil, fmt.Errorf("expenses: project %d asked for twice in one batch", id)
		}
		wanted[id] = struct{}{}
	}

	rows, err := q.ProjectExpenseGroups(ctx, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("expenses: read what the projects' expenses cost and bill: %w", err)
	}

	// No row needs checking against wanted: the query filters on the very same
	// slice, so every group it answers is one that was asked for. The map is
	// built for the duplicate check above and nothing else.
	sums := make(map[int32]*projectExpenseSum, len(rows))
	for _, row := range rows {
		sum := sums[row.ProjectID]
		if sum == nil {
			sum = &projectExpenseSum{currencies: map[string]*currencyExpenseSum{}}
			sums[row.ProjectID] = sum
		}
		if err := sum.add(row); err != nil {
			return nil, err
		}
	}

	// A project with nothing recorded is absent rather than zero-valued: the
	// provider knows only what was recorded, and it cannot name a currency
	// for a project nothing was recorded in without asking the directory it
	// must not ask.
	out := make(map[int32]contracts.ProjectExpenseTotals, len(sums))
	for id, sum := range sums {
		out[id] = sum.totals()
	}
	return out, nil
}

// projectExpenseSum adds up the query's groups for one project, one currency
// at a time.
type projectExpenseSum struct {
	currencies map[string]*currencyExpenseSum
	last       time.Time
}

// currencyExpenseSum is one currency's figures while they are being added up:
// the three buckets, the four figures that span them, and the supplier
// invoices' share of the buckets (supplier invoices design D3), summed beside
// them rather than instead of them — every bucket above keeps meaning every
// line.
type currencyExpenseSum struct {
	approved, submitted, draft expenseBucketSum
	readyCount                 int64
	ready                      big.Rat
	invoicedCount              int64
	invoiced                   big.Rat
	unpricedCount              int64
	supplier                   splitSum
}

// splitSum is one kind's share of the three buckets while it is being added up.
type splitSum struct {
	approved, submitted, draft expenseBucketSum
}

// expenseBucketSum is one bucket while it is being added up. The count is an
// exact integer; the amounts are exact rationals until totals rounds them.
type expenseBucketSum struct {
	count      int64
	cost, bill big.Rat
}

// add folds one group into the sum. The group already carries the bucket the
// unit's status puts it in, and the ready and invoiced figures it holds are
// already restricted to what qualifies — this only has to place them.
func (s *projectExpenseSum) add(row store.ProjectExpenseGroupsRow) error {
	currency := s.currencies[row.Currency]
	if currency == nil {
		currency = &currencyExpenseSum{}
		s.currencies[row.Currency] = currency
	}

	cost, err := exactDecimal(row.CostAmount)
	if err != nil {
		return err
	}
	bill, err := exactDecimal(row.BillAmount)
	if err != nil {
		return err
	}
	// A supplier invoice's group lands in its bucket like any other and, a
	// second time, in the supplier invoices' own share of that bucket.
	into := []*expenseBucketSum{pickBucket(row.Bucket, &currency.approved, &currency.submitted, &currency.draft)}
	if row.SupplierInvoice {
		into = append(into, pickBucket(row.Bucket, &currency.supplier.approved, &currency.supplier.submitted, &currency.supplier.draft))
	}
	for _, bucket := range into {
		bucket.count += row.LineCount
		bucket.cost.Add(&bucket.cost, cost)
		bucket.bill.Add(&bucket.bill, bill)
	}

	// Ready and invoiced span the buckets: only an approved unit is ever
	// ready, and an invoiced line is still an approved one, so both are
	// carried on the currency rather than on a bucket.
	currency.readyCount += row.ReadyCount
	ready, err := exactDecimal(row.ReadyAmount)
	if err != nil {
		return err
	}
	currency.ready.Add(&currency.ready, ready)

	currency.invoicedCount += row.InvoicedCount
	invoiced, err := exactDecimal(row.InvoicedAmount)
	if err != nil {
		return err
	}
	currency.invoiced.Add(&currency.invoiced, invoiced)

	currency.unpricedCount += row.UnpricedCount

	if row.LastEntryDate.Valid && row.LastEntryDate.Time.After(s.last) {
		s.last = row.LastEntryDate.Time
	}
	return nil
}

// totals is the finished shape: one entry per currency by code ascending,
// each figure rounded once, and the last entry date as the contract's
// YYYY-MM-DD.
func (s *projectExpenseSum) totals() contracts.ProjectExpenseTotals {
	codes := make([]string, 0, len(s.currencies))
	for code := range s.currencies {
		codes = append(codes, code)
	}
	slices.Sort(codes)

	totals := contracts.ProjectExpenseTotals{Currencies: make([]contracts.CurrencyExpenses, 0, len(codes))}
	for _, code := range codes {
		totals.Currencies = append(totals.Currencies, s.currencies[code].currency(code))
	}
	if !s.last.IsZero() {
		date := s.last.Format(time.DateOnly)
		totals.LastEntryDate = &date
	}
	return totals
}

// currency renders one currency for the contract.
func (c *currencyExpenseSum) currency(code string) contracts.CurrencyExpenses {
	return contracts.CurrencyExpenses{
		Currency:       code,
		Approved:       c.approved.bucket(),
		Submitted:      c.submitted.bucket(),
		Draft:          c.draft.bucket(),
		Total:          c.total(),
		ReadyCount:     c.readyCount,
		ReadyAmount:    expenseAmountText(&c.ready),
		InvoicedCount:  c.invoicedCount,
		InvoicedAmount: expenseAmountText(&c.invoiced),
		UnpricedCount:  c.unpricedCount,

		SupplierInvoices: c.supplier.split(),
	}
}

// total is the three buckets as one unrounded sum, so the contract's Total is
// rounded exactly once — from everything that went into the currency, not
// from three figures that have each already been rounded. Three buckets worth
// 0.005 each publish "0.01" apiece while the expenses are worth 0.02
// altogether, and a consumer adding them would report 0.03; that is the whole
// reason the contract carries the field.
func (c *currencyExpenseSum) total() contracts.ExpenseBucket {
	whole := sumOf(&c.approved, &c.submitted, &c.draft)
	return whole.bucket()
}

// split renders the supplier invoices' share for the contract, nil when there
// is none — "absent when none" is the provider's answer, so every consumer's
// shaping by absence starts here. Its Total is the three buckets as one
// unrounded sum, rounded once, for the reason total gives.
func (s *splitSum) split() *contracts.ExpenseSplit {
	whole := sumOf(&s.approved, &s.submitted, &s.draft)
	if whole.count == 0 {
		return nil
	}
	return &contracts.ExpenseSplit{
		Approved:  s.approved.bucket(),
		Submitted: s.submitted.bucket(),
		Draft:     s.draft.bucket(),
		Total:     whole.bucket(),
	}
}

// sumOf is buckets added up exactly, before anything is rounded.
func sumOf(buckets ...*expenseBucketSum) *expenseBucketSum {
	var whole expenseBucketSum
	for _, bucket := range buckets {
		whole.count += bucket.count
		whole.cost.Add(&whole.cost, &bucket.cost)
		whole.bill.Add(&whole.bill, &bucket.bill)
	}
	return &whole
}

// pickBucket is the bucket a group's unit status puts it in; anything that is
// neither approved nor submitted — a draft or a rejected unit — is a draft.
func pickBucket(status string, approved, submitted, draft *expenseBucketSum) *expenseBucketSum {
	switch status {
	case statusApproved:
		return approved
	case statusSubmitted:
		return submitted
	}
	return draft
}

// bucket renders one bucket for the contract.
func (b *expenseBucketSum) bucket() contracts.ExpenseBucket {
	return contracts.ExpenseBucket{
		Count:      b.count,
		CostAmount: expenseAmountText(&b.cost),
		BillAmount: expenseAmountText(&b.bill),
	}
}

// exactDecimal reads one group's unrounded numeric sum, as the query hands it
// over: decimal text, so nothing is rounded on the way out of PostgreSQL and
// no float ever touches money. Text the column cannot have produced is an
// infrastructure failure, reported rather than silently read as nothing — a
// zero where an amount belongs is the one answer this contract must never
// invent.
func exactDecimal(text string) (*big.Rat, error) {
	amount, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("expenses: %q is not a decimal amount", text)
	}
	return amount, nil
}

// expenseAmountText renders an exact sum as the contract's decimal text: two
// decimals, "0.00" for nothing, and the half rounded away from zero.
//
// It is this module's own decimalText, which every stored amount already goes
// through, so the contract's figures round exactly the way the columns behind
// them do. The contract says "half away from zero" and money.go says "half
// up"; they are the same rule here because that is what big.Rat.FloatString
// does — it rounds the magnitude and keeps the sign, so −0.005 renders
// "-0.01", not "0.00". The distinction never arises for these figures anyway:
// a cost is a gross less its VAT and a bill amount is a rate times a quantity,
// and neither is negative.
func expenseAmountText(amount *big.Rat) string { return decimalText(amount, moneyPlaces) }
