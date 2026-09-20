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
	canManage := s.has(ctx, "expenses:manage")
	current, err := settingsResponse(row, canManage)
	if err != nil {
		return nil, err
	}
	categories, err := listCategories(ctx, q)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesMeta200JSONResponse(gen.ExpensesMetaResponse{
		ProjectsAvailable: s.projectsAvailable(),
		DefaultCurrency:   current.DefaultCurrency,
		// The business time zone, through the same shaping GET /settings uses:
		// meta is the one read an expense or a travel-claim form makes, and a
		// form labelling a trip's days has to label them in the installation's
		// zone or it will disagree with the server about which day a save lands
		// on.
		TimeZone:             current.TimeZone,
		DefaultMarkupPercent: current.DefaultMarkupPercent,
		LockedBefore:         current.LockedBefore,
		ReceiptRequiredOver:  current.ReceiptRequiredOver,
		Categories:           categories,
		Capabilities: gen.ExpensesMetaCapabilities{
			CanApprove: s.has(ctx, "expenses:approve"),
			CanViewAll: s.has(ctx, "expenses:view-all"),
			CanManage:  canManage,
		},
	}), nil
}
