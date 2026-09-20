package projects

import (
	"fmt"
	"math/big"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the economy read's arithmetic and nothing else: no database,
// no request, no authorization, no generated types. Every function here is
// pure, which is what lets the project's own economy (economy.go), the
// portfolio and the dashboard's budget alerts answer from one definition
// rather than three that drift.
//
// Two rules run through all of it.
//
// **Exact decimal, never float64.** Hours arrive as int64 hundredths and
// amounts as the decimal text a numeric column holds (contracts.ActualsTotals),
// and both stay exact in math/big.Rat until the last step, where a JSON
// number is produced. Adding money in binary floating point answers
// 0.10 + 0.20 with 0.30000000000000004, and a ratio decided a hair either
// side of a threshold is the difference between a dashboard alert and
// silence.
//
// **The rounded percentage is for display only.** overBudget and the alert
// thresholds are decided on the exact ratio, so a project at 100.04 % is over
// budget although its percent prints 100.0. Rounding first and comparing
// afterwards would make the printed number the rule, which is the one thing a
// display figure must never be.

// The three bases "budget used" can be measured against, in the order
// budgetUsed tries them (design §2 E8). They are contract values — the
// response's budgetUsed.basis — so they are written exactly once, here.
const (
	basisAmount     = "amount"
	basisFixedPrice = "fixedPrice"
	basisHours      = "hours"
)

// The three bands a project's budget usage falls in (design §5's stats
// table). They are decided on the exact ratio, never on the rounded percent,
// and the warning band deliberately includes 100 % exactly: a project that
// has used precisely its budget is a warning, not an exceedance.
const (
	budgetLevelNone     = ""
	budgetLevelWarning  = "warning"
	budgetLevelExceeded = "exceeded"
)

// budgetBasis is the budget side of the comparison, as whoever loaded the
// project presents it: the three candidate bases as exact decimals (nil for
// "not set"), the project's billing type, and whether this caller may see
// amounts at all.
//
// It is deliberately not a project row. The portfolio compares hundreds of
// projects and the dashboard compares them again for its alerts; taking plain
// values means all three reach the same answer without any of them holding a
// database row.
type budgetBasis struct {
	// Amount is the project's budget amount, the budgeted value of the work.
	Amount *big.Rat
	// FixedPrice is the project's fixed price. It is a basis only on a
	// fixed-price project, which BillingType is what says.
	FixedPrice *big.Rat
	// Hours is the project's budget in hours, the one basis a caller without
	// financial rights ever gets.
	Hours *big.Rat
	// BillingType is the project's, as validateBillingType writes it. An
	// empty one — a billing line, which has no billing type of its own —
	// simply never reaches the fixed-price basis.
	BillingType string
	// SeesAmounts is the caller's financial rights on this project. Without
	// them the two amount bases are skipped entirely, so no percentage
	// derived from money ever reaches a caller who may not see money.
	SeesAmounts bool
}

// bucketSum is one of the three buckets (design §2 E2) as the arithmetic
// wants it: exact hours and the exact bill amount of those hours.
type bucketSum struct {
	Hours  *big.Rat
	Amount *big.Rat
}

// bucketSums is the logged side of the comparison. Approved carries invoiced
// work and Draft carries rejected work — the split the contract's provider
// already made, passed through rather than re-decided here.
//
// Total is the provider's own across-bucket figure, not the three added up.
// Each bucket is rounded on its own, so three buckets worth half a cent each
// are published as 0.01 apiece while the work is worth 0.02: adding them is
// the wrong number, and the contract carries Total precisely so nobody has to.
type bucketSums struct {
	Approved  bucketSum
	Submitted bucketSum
	Draft     bucketSum
	Total     bucketSum
}

// budgetUse is what one comparison answers: which basis was used, the exact
// ratio it was decided on, and the two percentages a surface prints.
type budgetUse struct {
	// Basis is one of basisAmount, basisFixedPrice and basisHours.
	Basis string
	// Ratio is the exact all-three-buckets ÷ basis. It is what overBudget
	// and budgetLevel are decided on; Percent is the same number rounded for
	// display and must never be compared against a threshold.
	Ratio *big.Rat
	// Percent is Ratio as a percentage, rounded half up to one decimal.
	Percent float64
	// ApprovedPercent is the approved bucket alone against the same basis,
	// rounded the same way.
	ApprovedPercent float64
	// OverBudget is Ratio > 1 exactly.
	OverBudget bool
}

// budgetUsed is design §2 E8's one definition, used by the project's economy,
// by the portfolio's rows and by the dashboard's budget alerts.
//
// The basis is chosen in a fixed order — the project's budget amount, then
// the fixed price of a fixed-price project, then the budget hours — and the
// first two are skipped for a caller without financial rights, so the only
// thing such a caller can ever be told is how much of an hour budget has been
// used. An amount basis compares the three buckets' bill amount; the hours
// basis compares their hours.
//
// It reports false when there is no basis at all, which is a project with no
// percentage rather than a project at 0 % — the portfolio sorts those last.
// A basis of zero is treated the same way: nothing can be a share of nothing,
// and a division by it would be an infinity dressed up as a budget.
func budgetUsed(basis budgetBasis, sums bucketSums) (budgetUse, bool) {
	name, amount, ok := basisFor(basis)
	if !ok {
		return budgetUse{}, false
	}
	used, approved := sums.totalHours(), sums.Approved.Hours
	if name != basisHours {
		used, approved = sums.totalAmount(), sums.Approved.Amount
	}
	ratio := new(big.Rat).Quo(used, amount)
	return budgetUse{
		Basis:           name,
		Ratio:           ratio,
		Percent:         percentNumber(ratio),
		ApprovedPercent: percentNumber(new(big.Rat).Quo(approved, amount)),
		OverBudget:      ratio.Cmp(oneExactly) > 0,
	}, true
}

// lineBudgetUsed is budgetUsed for one billing line. A line has no fixed
// price and no billing type of its own, so the order is just its budget
// amount — for a caller who may see amounts — and then its budget hours; the
// rule itself is the project's, called rather than restated, so a line and
// its project can never disagree about what "used" means.
func lineBudgetUsed(budgetAmount, budgetHours *big.Rat, seesAmounts bool, sums bucketSums) (budgetUse, bool) {
	return budgetUsed(budgetBasis{Amount: budgetAmount, Hours: budgetHours, SeesAmounts: seesAmounts}, sums)
}

// basisFor picks the basis, in design §2 E8's order, and reports false when
// none of the three is a number anything can be a share of.
func basisFor(b budgetBasis) (string, *big.Rat, bool) {
	if b.SeesAmounts {
		if positive(b.Amount) {
			return basisAmount, b.Amount, true
		}
		if b.BillingType == billingFixedPrice && positive(b.FixedPrice) {
			return basisFixedPrice, b.FixedPrice, true
		}
	}
	if positive(b.Hours) {
		return basisHours, b.Hours, true
	}
	return "", nil, false
}

// budgetLevel is the band a comparison falls in: nothing below 80 %, a
// warning from 80 % up to and including 100 %, and exceeded past it. It is
// decided on the exact ratio, which is the whole point of keeping it — a
// project at 100.04 % prints 100.0 and is still exceeded.
func budgetLevel(u budgetUse) string {
	switch {
	case u.Ratio == nil:
		return budgetLevelNone
	case u.Ratio.Cmp(oneExactly) > 0:
		return budgetLevelExceeded
	case u.Ratio.Cmp(fourFifths) >= 0:
		return budgetLevelWarning
	default:
		return budgetLevelNone
	}
}

// oneExactly and fourFifths are the two thresholds as exact rationals. They
// are package-level so that neither is re-parsed per project in a portfolio
// of hundreds; nothing ever mutates them.
var (
	oneExactly = big.NewRat(1, 1)
	fourFifths = big.NewRat(4, 5)
)

// positive reports whether r is a number greater than zero. A nil basis is
// "not set" and a zero one is a budget of nothing; neither can be divided by,
// and both mean the same thing to the caller — there is no percentage.
func positive(r *big.Rat) bool { return r != nil && r.Sign() > 0 }

// totalHours and totalAmount are the subject's own whole figure, as the
// provider computed it from the unrounded sum — never the three published
// buckets added together, which is a different number by up to a cent or two
// and is not the one an invoice would show. Adding *lines* to reach a
// project's figures is wrong for the same reason and is never done (see
// bucketSumsOf's caller).
func (s bucketSums) totalHours() *big.Rat { return s.Total.Hours }

func (s bucketSums) totalAmount() *big.Rat { return s.Total.Amount }

// bucketSumsOf converts one ActualsTotals into the exact decimals the
// arithmetic works in. An amount the provider spelled in a text big.Rat
// cannot read is an error rather than a zero: a margin or a percentage
// silently computed from nothing is worse than no answer at all.
//
// The same goes for a provider that forgets Total: hours are exact, so
// Total's hours are the three buckets' hours or the provider is broken, and a
// project reading "0 h logged" beside three filled buckets is refused here
// rather than shown. (Amounts cannot be checked that way — Total is rounded
// once from the unrounded sum and legitimately differs from the buckets by a
// cent.)
func bucketSumsOf(t contracts.ActualsTotals) (bucketSums, error) {
	if buckets := t.Approved.HoursHundredths + t.Submitted.HoursHundredths + t.Draft.HoursHundredths; t.Total.HoursHundredths != buckets {
		return bucketSums{}, fmt.Errorf("projects: the actuals provider reports %d hundredths of an hour in total and %d across its buckets", t.Total.HoursHundredths, buckets)
	}
	var out bucketSums
	for _, pair := range []struct {
		from contracts.ActualsBucket
		to   *bucketSum
	}{
		{t.Approved, &out.Approved},
		{t.Submitted, &out.Submitted},
		{t.Draft, &out.Draft},
		{t.Total, &out.Total},
	} {
		amount, err := exactAmount(pair.from.BillAmount)
		if err != nil {
			return bucketSums{}, err
		}
		pair.to.Hours, pair.to.Amount = exactHours(pair.from.HoursHundredths), amount
	}
	return out, nil
}

// costSumsOf is bucketSumsOf for the cost side: the same three buckets, read
// from CostAmount instead of BillAmount. The hours are the same hours, so
// they are carried along and never added twice.
func costSumsOf(t contracts.ActualsTotals) (bucketSums, error) {
	var out bucketSums
	for _, pair := range []struct {
		from contracts.ActualsBucket
		to   *bucketSum
	}{
		{t.Approved, &out.Approved},
		{t.Submitted, &out.Submitted},
		{t.Draft, &out.Draft},
		{t.Total, &out.Total},
	} {
		amount, err := exactAmount(pair.from.CostAmount)
		if err != nil {
			return bucketSums{}, err
		}
		pair.to.Hours, pair.to.Amount = exactHours(pair.from.HoursHundredths), amount
	}
	return out, nil
}

// exactHours is a count of hundredths of an hour as the exact decimal it
// stands for: 125 is 1.25 h, exactly, and never 1.2499999999999998.
func exactHours(hundredths int64) *big.Rat { return big.NewRat(hundredths, 100) }

// loggedWork is one subject's actuals — a whole project's, or one billing
// line's — as the response needs them: the three buckets on both the bill and
// the cost side, and the four figures that span all three.
//
// It exists so that folding two of them together (a billing line the project
// does not have, folded into the "no line" row) is one operation with one
// rule, rather than an ad-hoc merge at the call site that would quietly lose
// somebody's hours.
type loggedWork struct {
	Bill                  bucketSums
	Cost                  bucketSums
	UnpricedHundredths    int64
	UncostedHundredths    int64
	BillableHundredths    int64
	NonBillableHundredths int64
	LastEntryDate         *string
}

// loggedWorkOf reads one ActualsTotals into exact decimals, both sides at
// once, so the bill and the cost figures of one subject can never come from
// two different reads of it.
func loggedWorkOf(t contracts.ActualsTotals) (loggedWork, error) {
	bill, err := bucketSumsOf(t)
	if err != nil {
		return loggedWork{}, err
	}
	cost, err := costSumsOf(t)
	if err != nil {
		return loggedWork{}, err
	}
	return loggedWork{
		Bill: bill, Cost: cost,
		UnpricedHundredths:    t.UnpricedHoursHundredths,
		UncostedHundredths:    t.UncostedHoursHundredths,
		BillableHundredths:    t.BillableHoursHundredths,
		NonBillableHundredths: t.NonBillableHoursHundredths,
		LastEntryDate:         t.LastEntryDate,
	}, nil
}

// add folds another subject's work into this one. Hours and amounts add; the
// last entry date is the later of the two, which for YYYY-MM-DD is simply the
// greater text.
//
// It is never used to reach a project's own totals from its lines: the
// provider rounds each bucket once on its own, so adding the lines up is a
// different number from the totals it reports. The one place it is used is
// the no-line row, which is built from work the provider attributed to lines
// this project does not have.
func (w loggedWork) add(o loggedWork) loggedWork {
	w.Bill = w.Bill.add(o.Bill)
	w.Cost = w.Cost.add(o.Cost)
	w.UnpricedHundredths += o.UnpricedHundredths
	w.UncostedHundredths += o.UncostedHundredths
	w.BillableHundredths += o.BillableHundredths
	w.NonBillableHundredths += o.NonBillableHundredths
	if o.LastEntryDate != nil && (w.LastEntryDate == nil || *o.LastEntryDate > *w.LastEntryDate) {
		w.LastEntryDate = o.LastEntryDate
	}
	return w
}

// logged reports whether anything at all was logged: any hours in any of the
// three buckets. It is what decides whether a no-line row exists at all.
func (w loggedWork) logged() bool { return w.Bill.totalHours().Sign() != 0 }

// zeroWork is a subject nothing has been logged against — every bucket at
// zero rather than nil, so the arithmetic never has to check.
func zeroWork() loggedWork {
	return loggedWork{Bill: zeroSums(), Cost: zeroSums()}
}

func zeroSums() bucketSums {
	zero := func() bucketSum { return bucketSum{Hours: new(big.Rat), Amount: new(big.Rat)} }
	return bucketSums{Approved: zero(), Submitted: zero(), Draft: zero(), Total: zero()}
}

// add is bucketSums addition, bucket by bucket.
func (s bucketSums) add(o bucketSums) bucketSums {
	pair := func(a, b bucketSum) bucketSum {
		return bucketSum{
			Hours:  new(big.Rat).Add(a.Hours, b.Hours),
			Amount: new(big.Rat).Add(a.Amount, b.Amount),
		}
	}
	return bucketSums{
		Approved:  pair(s.Approved, o.Approved),
		Submitted: pair(s.Submitted, o.Submitted),
		Draft:     pair(s.Draft, o.Draft),
		Total:     pair(s.Total, o.Total),
	}
}

// expenseSum is one bucket of recorded expenses as the arithmetic wants it:
// an exact count of lines, and the exact cost and bill of those lines.
//
// It is deliberately not a bucketSum. An expense has no hours — a receipt is
// not measured in time — and the count is a count of lines rather than of
// anything that could be compared against a budget in hours, which is half of
// why nothing here can ever reach budgetUsed by accident.
type expenseSum struct {
	Count int64
	Cost  *big.Rat
	Bill  *big.Rat
}

// currencyExpenses is everything recorded in one currency, as exact decimals:
// the three buckets, the across-bucket Total the consumer reports as the
// project's own figure, and the invoicing figures that span the buckets.
//
// Total is the provider's own, rounded once from the unrounded whole — never
// the three buckets added up, for exactly the reason bucketSums says it of
// logged work.
type currencyExpenses struct {
	Currency                          string
	Approved, Submitted, Draft, Total expenseSum
	ReadyCount                        int64
	ReadyAmount                       *big.Rat
	InvoicedCount                     int64
	InvoicedAmount                    *big.Rat
	UnpricedCount                     int64
}

// expenseFigures is one project's expenses split the way every surface here
// needs them: the currency the project itself is in, every other currency it
// happens to have spent in, and the day the most recent line was for.
//
// The split is made once, here, because it is the whole of the currency rule
// (spec §4): a line counts towards a project's own figures only when its
// currency is the project's, and otherwise is reported as what it is. Own is
// nil for a project that carries no currency — then every currency is an
// other one and the project has no figures of its own at all.
type expenseFigures struct {
	Own           *currencyExpenses
	Others        []currencyExpenses
	LastEntryDate *string
}

// expensesOf reads one project's answer from the expenses contract into exact
// decimals and splits it by the project's currency.
//
// A project with nothing recorded is **absent from the provider's map**,
// unlike the actuals contract's zero-valued entry, so the caller hands the
// zero value in and it lands here as "a currency list with nothing in it" —
// which, for a project that carries a currency, is Own at zero rather than
// Own nil. "Nothing has been recorded" and "this installation cannot say" are
// different answers and only the second is an absent block.
func expensesOf(t contracts.ProjectExpenseTotals, currency *string) (expenseFigures, error) {
	out := expenseFigures{LastEntryDate: t.LastEntryDate}
	if currency != nil {
		own := zeroCurrencyExpenses(*currency)
		out.Own = &own
	}
	for _, reported := range t.Currencies {
		one, err := currencyExpensesOf(reported)
		if err != nil {
			return expenseFigures{}, err
		}
		if currency != nil && one.Currency == *currency {
			*out.Own = one
			continue
		}
		out.Others = append(out.Others, one)
	}
	return out, nil
}

// currencyExpensesOf converts one currency's reported figures. An amount the
// provider spelled in a text big.Rat cannot read is an error rather than a
// zero, exactly as it is on the actuals side: a margin silently computed from
// nothing is worse than no answer at all.
func currencyExpensesOf(c contracts.CurrencyExpenses) (currencyExpenses, error) {
	out := currencyExpenses{
		Currency:      c.Currency,
		ReadyCount:    c.ReadyCount,
		InvoicedCount: c.InvoicedCount,
		UnpricedCount: c.UnpricedCount,
	}
	for _, pair := range []struct {
		from contracts.ExpenseBucket
		to   *expenseSum
	}{
		{c.Approved, &out.Approved},
		{c.Submitted, &out.Submitted},
		{c.Draft, &out.Draft},
		{c.Total, &out.Total},
	} {
		cost, err := exactAmount(pair.from.CostAmount)
		if err != nil {
			return currencyExpenses{}, err
		}
		bill, err := exactAmount(pair.from.BillAmount)
		if err != nil {
			return currencyExpenses{}, err
		}
		pair.to.Count, pair.to.Cost, pair.to.Bill = pair.from.Count, cost, bill
	}
	ready, err := exactAmount(c.ReadyAmount)
	if err != nil {
		return currencyExpenses{}, err
	}
	invoiced, err := exactAmount(c.InvoicedAmount)
	if err != nil {
		return currencyExpenses{}, err
	}
	out.ReadyAmount, out.InvoicedAmount = ready, invoiced
	return out, nil
}

// zeroCurrencyExpenses is a currency nothing was recorded in — every figure at
// zero rather than nil, so the arithmetic never has to check.
func zeroCurrencyExpenses(currency string) currencyExpenses {
	zero := func() expenseSum { return expenseSum{Cost: new(big.Rat), Bill: new(big.Rat)} }
	return currencyExpenses{
		Currency: currency,
		Approved: zero(), Submitted: zero(), Draft: zero(), Total: zero(),
		ReadyAmount: new(big.Rat), InvoicedAmount: new(big.Rat),
	}
}

// exactAmount reads the decimal text the actuals contract carries amounts in.
// An empty text is nothing rather than an error — a bucket a provider left
// blank has no money in it — but a text that is not a decimal is an error,
// because treating it as zero would report a margin nobody computed.
func exactAmount(text string) (*big.Rat, error) {
	if text == "" {
		return new(big.Rat), nil
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("projects: %q is not a decimal amount", text)
	}
	return r, nil
}

// decimalNumber is an exact decimal as the JSON number the contract carries:
// rounded half up to two places. Every quantity this file produces holds two
// — money to the cent, hours as hundredths — so one renderer serves both and
// neither can be rounded by a rule the other does not use.
func decimalNumber(r *big.Rat) float64 { return roundHalfUpCents(r) }

// percentNumber is a ratio as the percentage a surface prints: times a
// hundred, rounded half up to one decimal. The rounding is the last thing
// that happens to the number and nothing is ever decided from its result.
func percentNumber(ratio *big.Rat) float64 {
	return roundHalfUpAt(new(big.Rat).Mul(ratio, oneHundred), 10)
}

// oneHundred is the percentage conversion's constant, package-level for the
// same reason the thresholds are.
var oneHundred = big.NewRat(100, 1)

// remainingHours is what is left of a budget in hours, clamped at zero: a
// line that has gone past its budget reports nothing left and says the rest
// through overBudget, rather than reporting a negative remainder that every
// surface would then have to decide how to render.
func remainingHours(budget, used *big.Rat) float64 {
	left := new(big.Rat).Sub(budget, used)
	if left.Sign() < 0 {
		return 0
	}
	return roundHalfUpCents(left)
}
