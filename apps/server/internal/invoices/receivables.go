package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.CustomerReceivables (customers
// receivables design D1): what one customer was invoiced and owes, for the
// customers module's overview of that customer. It is the mirror of what
// expenses/projectexpenses.go does for a project's costs, written to the same
// rules.
//
// It authorizes nothing: the caller has already decided who may see the
// customer and who may see money.
//
// It serves from this module's own tables and nothing else. It never asks
// contracts.CustomerDirectory anything while serving — that would be a module
// cycle at request time — and it holds no lock and opens no transaction: a
// panel's two figures need not be one snapshot, and the two queries read
// with no connection held between them.
//
// It owns "today". Every state is judged on the Oslo business day of this
// module's clock, as the list, the stats and the overdue list judge it
// (values.go's businessDay), and AsOf says which day that was, so the
// consumer can never show a state the Invoices app does not.
//
// Arithmetic: the query sums the stored two-decimal figures as numeric and
// hands each over whole; it crosses into decimal text here (ratFromNumeric,
// then two decimals) and is never a float on this side of the boundary.

// customerReceivables is this module's contracts.CustomerReceivables. Like
// expenses' provider it is read-only and holds the queries and the clock:
// Compose builds it once, before any module mounts.
type customerReceivables struct {
	q     *store.Queries
	clock func() time.Time
}

var _ contracts.CustomerReceivables = (*customerReceivables)(nil)

// newCustomerReceivables is Module's Receivables: the constructor Compose
// calls with the dependencies it was given. It takes the pool and the clock
// and nothing else — no directory reaches the served answer, and holding one
// here would only invite that.
func newCustomerReceivables(d module.Deps) contracts.CustomerReceivables {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return &customerReceivables{q: store.New(d.Pool), clock: clock}
}

// ReceivablesForCustomer is the contract's one read: the per-currency money
// and the cross-currency counts and dates, judged on today in Oslo.
func (r *customerReceivables) ReceivablesForCustomer(ctx context.Context, customerID int32) (contracts.CustomerReceivableTotals, error) {
	today := businessDay(r.clock())
	thisYearFrom := time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	rows, err := r.q.CustomerReceivablesByCurrency(ctx, store.CustomerReceivablesByCurrencyParams{
		CustomerID:   customerID,
		Today:        pgDate(today),
		ThisYearFrom: pgDate(thisYearFrom),
		NextYearFrom: pgDate(thisYearFrom.AddDate(1, 0, 0)),
		LastYearFrom: pgDate(thisYearFrom.AddDate(-1, 0, 0)),
	})
	if err != nil {
		return contracts.CustomerReceivableTotals{}, fmt.Errorf("invoices: read what customer %d was invoiced: %w", customerID, err)
	}
	counts, err := r.q.CustomerReceivablesCounts(ctx, store.CustomerReceivablesCountsParams{CustomerID: customerID, Today: pgDate(today)})
	if err != nil {
		return contracts.CustomerReceivableTotals{}, fmt.Errorf("invoices: count what customer %d owes: %w", customerID, err)
	}

	out := contracts.CustomerReceivableTotals{
		Currencies:         make([]contracts.CurrencyReceivables, 0, len(rows)),
		OutstandingCount:   int(counts.OutstandingCount),
		OverdueCount:       int(counts.OverdueCount),
		OnHoldCount:        int(counts.OnHoldCount),
		AtCollectionCount:  int(counts.AtCollectionCount),
		OldestOverdueDueOn: contractDate(counts.OldestOverdueDueOn),
		LastIssuedOn:       contractDate(counts.LastIssuedOn),
		LastPaymentOn:      contractDate(counts.LastPaymentOn),
		AsOf:               today.Format(time.DateOnly),
	}
	for _, row := range rows {
		c := contracts.CurrencyReceivables{Currency: row.Currency}
		for _, f := range []struct {
			into *string
			from pgtype.Numeric
		}{
			{&c.IssuedThisYear, row.IssuedThisYear},
			{&c.CreditedThisYear, row.CreditedThisYear},
			{&c.IssuedLastYear, row.IssuedLastYear},
			{&c.CreditedLastYear, row.CreditedLastYear},
			{&c.Outstanding, row.Outstanding},
			{&c.Overdue, row.Overdue},
		} {
			text, err := contractAmount(f.from)
			if err != nil {
				return contracts.CustomerReceivableTotals{}, err
			}
			*f.into = text
		}
		out.Currencies = append(out.Currencies, c)
	}
	return out, nil
}

// contractAmount is a summed numeric as the contract carries money: exact
// decimal text with two decimals, "0.00" for nothing.
func contractAmount(n pgtype.Numeric) (string, error) {
	amount, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return new(big.Rat).Set(amount).FloatString(2), nil
}

// contractDate is a nullable date column as the contract carries a day:
// YYYY-MM-DD, nil for NULL.
func contractDate(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	return ptr(utcDay(d.Time).Format(time.DateOnly))
}
