package purchasing

// The dashboard's figures (P23, §11): drafts, open, late and unconfirmed
// orders, the caller's unconfirmed ones — the attention item of P19 — and
// the open orders whose last mailing failed.

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// GetPurchasingSummary Get the Purchasing figures
// (GET /api/v1/purchasing/summary)
func (s *server) GetPurchasingSummary(ctx context.Context, _ gen.GetPurchasingSummaryRequestObject) (gen.GetPurchasingSummaryResponseObject, error) {
	d, err := s.daysOf(ctx)
	if err != nil {
		return nil, err
	}
	var me *uuid.UUID
	if id := callerID(ctx); id != uuid.Nil {
		me = &id
	}
	row, err := s.store().Summary(ctx, store.SummaryParams{Today: dayCol(d.today), UnconfirmedBefore: d.unconfirmedBefore, BuyerID: me})
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the figures: %w", err)
	}
	return gen.GetPurchasingSummary200JSONResponse(gen.PurchasingSummary{
		Drafts: row.Drafts, Open: row.Open, Late: row.Late, Unconfirmed: row.Unconfirmed,
		MineUnconfirmed: row.MineUnconfirmed, FailedMailings: row.FailedMailings,
	}), nil
}
