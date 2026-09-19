package timetracking

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the module's contracts.ProjectActuals (design §4): what has
// been logged against a project, for whoever compares it with what was
// planned — projects' economy first, and one day an invoice.
//
// Two rules shape it. The first is that it authorizes nothing: the caller
// has already decided who may see the project and who may see amounts, and
// the answer carries hours and money together. The second is that it serves
// from this module's own tables and nothing else: the project's currency
// arrives in the request because asking the project directory for it while
// serving projects' own request would be a module cycle at request time.
// Everything the answer needs is on the entry already, rates and currencies
// snapshotted when the work was logged (D3).
//
// Arithmetic: hours are exact int64 hundredths all the way from SQL, and
// amounts are exact decimals — the grouped query sums the unrounded products
// per group as numeric and hands them over as text, the groups are added up
// in math/big.Rat, and each bucket is rounded once, half-up, at the end.
// Rounding every group instead and adding the rounded figures is a different
// number, and not the one an invoice would show.

// actuals is this module's contracts.ProjectActuals. Like projects' own
// directory it is read-only and holds nothing but the queries: Compose builds
// it once, before any module mounts.
type actuals struct{ q *store.Queries }

var _ contracts.ProjectActuals = (*actuals)(nil)

// newActuals is Module's Actuals: the constructor Compose calls with the
// dependencies it was given. It takes the pool and nothing else — no
// directory reaches the served answer, and holding one here would only
// invite that.
func newActuals(d module.Deps) contracts.ProjectActuals {
	return &actuals{q: store.New(d.Pool)}
}

// Actuals is one project's totals and the same totals per billing line.
func (a *actuals) Actuals(ctx context.Context, req contracts.ActualsRequest) (contracts.ProjectActualsEntry, error) {
	rows, err := a.q.ProjectActualGroups(ctx, []int32{req.ProjectID})
	if err != nil {
		return contracts.ProjectActualsEntry{}, fmt.Errorf("time: read what has been logged on the project: %w", err)
	}

	var project actualsSum
	var noLine *actualsSum
	lines := map[int32]*actualsSum{}
	var lineIDs []int32
	for _, row := range rows {
		if row.ProjectID != req.ProjectID {
			continue
		}
		if err := project.add(row, req.Currency); err != nil {
			return contracts.ProjectActualsEntry{}, err
		}
		var line *actualsSum
		if row.BillingLineID == nil {
			if noLine == nil {
				noLine = &actualsSum{}
			}
			line = noLine
		} else {
			line = lines[*row.BillingLineID]
			if line == nil {
				line = &actualsSum{}
				lines[*row.BillingLineID] = line
				lineIDs = append(lineIDs, *row.BillingLineID)
			}
		}
		if err := line.add(row, req.Currency); err != nil {
			return contracts.ProjectActualsEntry{}, err
		}
	}

	// Lines by id, and the hours logged on no line last — the order the
	// project summary lists them in too (stats.go's lineHours).
	slices.Sort(lineIDs)
	entry := contracts.ProjectActualsEntry{Totals: project.totals()}
	for _, id := range lineIDs {
		entry.Lines = append(entry.Lines, contracts.LineActuals{BillingLineID: &id, Totals: lines[id].totals()})
	}
	if noLine != nil {
		entry.Lines = append(entry.Lines, contracts.LineActuals{Totals: noLine.totals()})
	}
	return entry, nil
}

// ActualsForProjects is many projects' totals in one query. Every requested
// project is in the result, zero-valued when nothing was logged on it, and
// each is folded in the currency its own request named.
func (a *actuals) ActualsForProjects(ctx context.Context, reqs []contracts.ActualsRequest) (map[int32]contracts.ActualsTotals, error) {
	if len(reqs) == 0 {
		return map[int32]contracts.ActualsTotals{}, nil
	}
	if len(reqs) > contracts.MaxActualsRequests {
		return nil, fmt.Errorf("time: %d projects asked for at once, at most %d: page the request",
			len(reqs), contracts.MaxActualsRequests)
	}

	// A project named more than once takes the last request's currency:
	// asking for one project in two currencies has one answer at most, and
	// the caller's last word is it.
	currencies := make(map[int32]*string, len(reqs))
	ids := make([]int32, 0, len(reqs))
	sums := make(map[int32]*actualsSum, len(reqs))
	for _, req := range reqs {
		if _, seen := currencies[req.ProjectID]; !seen {
			ids = append(ids, req.ProjectID)
			sums[req.ProjectID] = &actualsSum{}
		}
		currencies[req.ProjectID] = req.Currency
	}

	rows, err := a.q.ProjectActualGroups(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: read what has been logged on the projects: %w", err)
	}
	for _, row := range rows {
		sum, ok := sums[row.ProjectID]
		if !ok {
			continue
		}
		if err := sum.add(row, currencies[row.ProjectID]); err != nil {
			return nil, err
		}
	}

	out := make(map[int32]contracts.ActualsTotals, len(sums))
	for id, sum := range sums {
		out[id] = sum.totals()
	}
	return out, nil
}

// actualsSum adds up the query's groups for one project, or for one of its
// billing lines. The hours are exact integers; the amounts are exact
// rationals until totals rounds them.
type actualsSum struct {
	approved, submitted, draft bucketSum
	unpricedHundredths         int64
	billableHundredths         int64
	nonBillableHundredths      int64
	last                       time.Time
}

// bucketSum is one of the three buckets while it is being added up.
type bucketSum struct {
	hundredths int64
	bill, cost big.Rat
}

// add folds one group into the sum, in the currency the caller asked for.
// Hours always count, whatever currency anything is in; an amount counts only
// when the work's own currency is the one asked for, and hours priced in
// another currency are unpriced instead — adding two currencies would be a
// number in neither (the rule projectBilling already applies to the project
// summary). With no currency asked for, no amount is summed and only hours
// with no bill rate at all are unpriced.
func (s *actualsSum) add(row store.ProjectActualGroupsRow, want *string) error {
	bucket := &s.draft
	switch row.Bucket {
	case statusApproved:
		bucket = &s.approved
	case statusSubmitted:
		bucket = &s.submitted
	}
	bucket.hundredths += row.HoursHundredths

	s.billableHundredths += row.BillableHoursHundredths
	s.nonBillableHundredths += row.HoursHundredths - row.BillableHoursHundredths
	s.unpricedHundredths += row.HoursHundredths - row.PricedHoursHundredths

	if want != nil {
		if row.BillCurrency != nil && *row.BillCurrency == *want {
			amount, err := exactAmount(row.BillAmount)
			if err != nil {
				return err
			}
			bucket.bill.Add(&bucket.bill, amount)
		} else {
			s.unpricedHundredths += row.PricedHoursHundredths
		}
		if row.CostCurrency != nil && *row.CostCurrency == *want {
			amount, err := exactAmount(row.CostAmount)
			if err != nil {
				return err
			}
			bucket.cost.Add(&bucket.cost, amount)
		}
	}

	if row.LastEntryDate.Valid && row.LastEntryDate.Time.After(s.last) {
		s.last = row.LastEntryDate.Time
	}
	return nil
}

// totals is the finished shape: each bucket's amounts rounded once, and the
// last entry date as the contract's YYYY-MM-DD.
func (s *actualsSum) totals() contracts.ActualsTotals {
	totals := contracts.ActualsTotals{
		Approved:                   s.approved.bucket(),
		Submitted:                  s.submitted.bucket(),
		Draft:                      s.draft.bucket(),
		UnpricedHoursHundredths:    s.unpricedHundredths,
		BillableHoursHundredths:    s.billableHundredths,
		NonBillableHoursHundredths: s.nonBillableHundredths,
	}
	if !s.last.IsZero() {
		date := s.last.Format(time.DateOnly)
		totals.LastEntryDate = &date
	}
	return totals
}

// bucket renders one bucket for the contract.
func (b *bucketSum) bucket() contracts.ActualsBucket {
	return contracts.ActualsBucket{
		HoursHundredths: b.hundredths,
		BillAmount:      amountText(&b.bill),
		CostAmount:      amountText(&b.cost),
	}
}

// exactAmount reads one group's unrounded numeric sum, as the query hands it
// over: decimal text, so nothing is rounded on the way out of PostgreSQL and
// no float ever touches money. Text the column cannot have produced is an
// infrastructure failure, reported rather than silently dropped.
func exactAmount(text string) (*big.Rat, error) {
	amount, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("time: %q is not a decimal amount", text)
	}
	return amount, nil
}

// amountText renders an exact sum as the contract's decimal text: two
// decimals, rounded half away from zero, "0.00" for nothing.
func amountText(amount *big.Rat) string {
	hundredths := new(big.Rat).Mul(amount, big.NewRat(100, 1))
	quotient, remainder := new(big.Int).QuoRem(hundredths.Num(), hundredths.Denom(), new(big.Int))
	// Half away from zero: twice the remainder reaching the denominator is
	// the half that rounds up, so 0.005 is 0.01 and −0.005 is −0.01.
	twiceRemainder := new(big.Int).Abs(remainder)
	twiceRemainder.Lsh(twiceRemainder, 1)
	if twiceRemainder.Cmp(hundredths.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(hundredths.Sign())))
	}
	return hundredthsText(quotient)
}

// hundredthsText renders hundredths as decimal text with exactly two
// decimals: 1 is "0.01", −1234 is "-12.34", 0 is "0.00".
func hundredthsText(hundredths *big.Int) string {
	digits := new(big.Int).Abs(hundredths).String()
	for len(digits) < 3 {
		digits = "0" + digits
	}
	text := digits[:len(digits)-2] + "." + digits[len(digits)-2:]
	if hundredths.Sign() < 0 {
		text = "-" + text
	}
	return text
}
