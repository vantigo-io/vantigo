package orders

// The customer summary (orders design O31a): what the customer page's Orders
// tab shows above its lists, per currency — the open quotes (draft and sent)
// at their net totals; the confirmed orders' quantity still to deliver at its
// net value, each line's open quantity at line_net over quantity; and what is
// delivered and not invoiced — delivered less what issued invoices stamped
// (O38) — valued the same way; and the active and paused subscriptions at
// their value per year (O42). Amounts are net of VAT.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// currencySums are a sums query's rows as the API answers them.
func currencySums[R any](rows []R, of func(R) (string, int32, pgtype.Numeric)) []gen.OrdersCurrencySum {
	out := make([]gen.OrdersCurrencySum, 0, len(rows))
	for _, r := range rows {
		currency, n, amount := of(r)
		out = append(out, gen.OrdersCurrencySum{Currency: currency, Count: n, Amount: wire(ratOrZero(amount), amountPlaces)})
	}
	return out
}

// GetOrdersCustomerSummary Get a customer's orders summary
// (GET /api/v1/orders/customers/{customerId}/summary)
func (s *server) GetOrdersCustomerSummary(ctx context.Context, req gen.GetOrdersCustomerSummaryRequestObject) (gen.GetOrdersCustomerSummaryResponseObject, error) {
	out := gen.OrdersCustomerSummary{CustomerId: req.CustomerId}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		quotes, err := q.OpenQuoteSums(ctx, req.CustomerId)
		if err != nil {
			return fmt.Errorf("orders: sum customer %d's open quotes: %w", req.CustomerId, err)
		}
		ordered, err := q.OrderedNotDeliveredSums(ctx, req.CustomerId)
		if err != nil {
			return fmt.Errorf("orders: sum customer %d's orders to deliver: %w", req.CustomerId, err)
		}
		delivered, err := q.DeliveredNotInvoicedSums(ctx, req.CustomerId)
		if err != nil {
			return fmt.Errorf("orders: sum customer %d's orders to invoice: %w", req.CustomerId, err)
		}
		subscriptions, err := q.ActiveSubscriptionSums(ctx, req.CustomerId)
		if err != nil {
			return fmt.Errorf("orders: sum customer %d's subscriptions: %w", req.CustomerId, err)
		}
		out.ActiveSubscriptions = currencySums(subscriptions, func(r store.ActiveSubscriptionSumsRow) (string, int32, pgtype.Numeric) {
			return r.Currency, r.Documents, r.Amount
		})
		out.OpenQuotes = currencySums(quotes, func(r store.OpenQuoteSumsRow) (string, int32, pgtype.Numeric) {
			return r.Currency, r.Documents, r.Amount
		})
		out.OrderedNotDelivered = currencySums(ordered, func(r store.OrderedNotDeliveredSumsRow) (string, int32, pgtype.Numeric) {
			return r.Currency, r.Documents, r.Amount
		})
		out.DeliveredNotInvoiced = currencySums(delivered, func(r store.DeliveredNotInvoicedSumsRow) (string, int32, pgtype.Numeric) {
			return r.Currency, r.Documents, r.Amount
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.GetOrdersCustomerSummary200JSONResponse(out), nil
}
