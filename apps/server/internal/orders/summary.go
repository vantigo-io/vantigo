package orders

// The customer summary (orders design O31a). It answers 501 until delivery
// A's orders lane implements it.

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// GetOrdersCustomerSummary is not implemented yet.
func (s *server) GetOrdersCustomerSummary(_ context.Context, _ gen.GetOrdersCustomerSummaryRequestObject) (gen.GetOrdersCustomerSummaryResponseObject, error) {
	return nil, module.ErrNotImplemented
}
