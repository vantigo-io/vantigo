package expenses

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// billableExpenses is this module's contracts.BillableExpenses (invoices work
// design D3): the lines ready to invoice and not invoiced yet, row by row, the
// way the invoices module builds an invoice from them. Like projectExpenses it
// is read-only and holds nothing but the queries over the pool: no directory
// reaches the answer, and the project's own facts are the caller's to read.
type billableExpenses struct{ q *store.Queries }

var _ contracts.BillableExpenses = (*billableExpenses)(nil)

// newBillableExpenses is Module's BillableExpenses.
func newBillableExpenses(d module.Deps) contracts.BillableExpenses {
	return &billableExpenses{q: store.New(d.Pool)}
}

// billableRow is the one shape both queries answer, so the conversion below is
// written once.
type billableRow = store.BillableExpensesForProjectsRow

// BillableExpenses answers req's lines: by project, at most MaxBillableRows of
// them with More past that, or exactly the ids still billable. "Billable" is
// expenses.ready_to_invoice and not invoiced yet — the rule the list's
// toInvoice filter and the project page's ready figure are.
func (b *billableExpenses) BillableExpenses(ctx context.Context, req contracts.BillableRequest) (contracts.BillableExpensesPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.BillableExpensesPage{}, fmt.Errorf("expenses: billable expenses: %w", err)
	}
	until := pgtype.Date{}
	if !req.Until.IsZero() {
		until = pgDate(req.Until)
	}
	var rows []billableRow
	if len(req.IDs) > 0 {
		byIDs, err := b.q.BillableExpensesByIDs(ctx, store.BillableExpensesByIDsParams{Ids: req.IDs, Until: until})
		if err != nil {
			return contracts.BillableExpensesPage{}, fmt.Errorf("expenses: read billable expenses by id: %w", err)
		}
		rows = make([]billableRow, len(byIDs))
		for i, r := range byIDs {
			rows[i] = billableRow(r)
		}
	} else {
		var err error
		rows, err = b.q.BillableExpensesForProjects(ctx, store.BillableExpensesForProjectsParams{
			ProjectIds: req.ProjectIDs, Until: until, RowLimit: contracts.MaxBillableRows + 1,
		})
		if err != nil {
			return contracts.BillableExpensesPage{}, fmt.Errorf("expenses: read billable expenses by project: %w", err)
		}
	}
	page := contracts.BillableExpensesPage{}
	if len(rows) > contracts.MaxBillableRows {
		rows, page.More = rows[:contracts.MaxBillableRows], true
	}
	page.Expenses = make([]contracts.BillableExpense, 0, len(rows))
	for _, r := range rows {
		e, err := billableExpense(r)
		if err != nil {
			return contracts.BillableExpensesPage{}, err
		}
		page.Expenses = append(page.Expenses, e)
	}
	return page, nil
}

// billableExpense is one row as the contract carries it: every amount as the
// exact decimal text the column holds, never through a float.
func billableExpense(r billableRow) (contracts.BillableExpense, error) {
	markup, err := numericText(r.MarkupPercent)
	if err != nil {
		return contracts.BillableExpense{}, err
	}
	distance, err := numericText(r.DistanceKm)
	if err != nil {
		return contracts.BillableExpense{}, err
	}
	rate, err := numericText(r.BillRatePerKm)
	if err != nil {
		return contracts.BillableExpense{}, err
	}
	return contracts.BillableExpense{
		ID: r.ID, Revision: r.Revision, ProjectID: r.ProjectID, ClaimID: r.ClaimID,
		Kind: r.Kind, Date: r.EntryDate.Time, Description: r.Description,
		Supplier: r.Supplier, SupplierInvoiceNumber: r.SupplierInvoiceNumber, NetAmount: r.NetAmount,
		MarkupPercent: markup, DistanceKm: distance, BillRatePerKm: rate,
		BillAmount: r.BillAmount, Currency: r.Currency, SupplierInvoiceRebilled: r.SupplierInvoiceRebilled,
	}, nil
}

// numericText is an optional numeric column as the exact decimal text
// PostgreSQL itself would print for it, nil for a SQL NULL.
func numericText(n pgtype.Numeric) (*string, error) {
	if !n.Valid {
		return nil, nil
	}
	v, err := n.Value()
	if err != nil {
		return nil, fmt.Errorf("expenses: read a stored decimal: %w", err)
	}
	text, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("expenses: read a stored decimal: got %T", v)
	}
	return &text, nil
}
