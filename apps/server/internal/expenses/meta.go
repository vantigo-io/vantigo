package expenses

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is GET /meta: the one read the Expenses app makes before it draws
// anything. It answers what this installation can do (decision X2's
// projectsAvailable), the settings a new expense starts from, the categories
// its form offers, and what the calling user may do — so no client ever
// re-derives a rule this module owns.

// GetExpensesMeta Get the Expenses metadata
// (GET /api/v1/expenses/meta)
func (s *server) GetExpensesMeta(ctx context.Context, _ gen.GetExpensesMetaRequestObject) (gen.GetExpensesMetaResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	current, err := settingsResponse(row)
	if err != nil {
		return nil, err
	}
	categories, err := listCategories(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesMeta200JSONResponse(gen.ExpensesMetaResponse{
		ProjectsAvailable:    s.projectsAvailable(),
		DefaultCurrency:      current.DefaultCurrency,
		DefaultMarkupPercent: current.DefaultMarkupPercent,
		LockedBefore:         current.LockedBefore,
		ReceiptRequiredOver:  current.ReceiptRequiredOver,
		Categories:           categories,
		Capabilities: gen.ExpensesMetaCapabilities{
			CanApprove: s.has(ctx, "expenses:approve"),
			CanViewAll: s.has(ctx, "expenses:view-all"),
			CanManage:  s.has(ctx, "expenses:manage"),
		},
	}), nil
}
