package invoices

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// This file is the Consumption section's read and the From consumption
// wizard (energy consumption billing design D7, D11; invoices from
// consumption plan readings 4 and 5). Every read of Energy is made on the
// pool, before the wizard's transaction, never in it.

var errNotYet = errors.New("invoices: not implemented yet")

// GetInvoicesConsumption List a customer's consumption due
// (GET /api/v1/invoices/consumption)
func (s *server) GetInvoicesConsumption(ctx context.Context, req gen.GetInvoicesConsumptionRequestObject) (gen.GetInvoicesConsumptionResponseObject, error) {
	return nil, errNotYet
}

// GetInvoicesConsumptionSettlements Settle a customer's consumption for the wizard
// (GET /api/v1/invoices/consumption/settlements)
func (s *server) GetInvoicesConsumptionSettlements(ctx context.Context, req gen.GetInvoicesConsumptionSettlementsRequestObject) (gen.GetInvoicesConsumptionSettlementsResponseObject, error) {
	return nil, errNotYet
}

// PostInvoicesFromConsumption Invoice consumption
// (POST /api/v1/invoices/from-consumption)
func (s *server) PostInvoicesFromConsumption(ctx context.Context, req gen.PostInvoicesFromConsumptionRequestObject) (gen.PostInvoicesFromConsumptionResponseObject, error) {
	return nil, errNotYet
}
