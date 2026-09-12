package energy

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Every operation of energy.yaml, each answering module.ErrNotImplemented,
// which the strict server's response-error handler turns into a 501.
// Mounting them all is what satisfies module.Router's "never registered"
// check, so the contract is fully routed from the first commit and each
// later task replaces the stubs of the area it implements.

// GetEnergyCustomersByCustomerIdConsumption List a customer's consumption
// (GET /api/v1/energy/customers/{customerId}/consumption)
func (s *server) GetEnergyCustomersByCustomerIdConsumption(context.Context, gen.GetEnergyCustomersByCustomerIdConsumptionRequestObject) (gen.GetEnergyCustomersByCustomerIdConsumptionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyCustomersByCustomerIdConsumptionAggregate Aggregate a customer's consumption
// (GET /api/v1/energy/customers/{customerId}/consumption/aggregate)
func (s *server) GetEnergyCustomersByCustomerIdConsumptionAggregate(context.Context, gen.GetEnergyCustomersByCustomerIdConsumptionAggregateRequestObject) (gen.GetEnergyCustomersByCustomerIdConsumptionAggregateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyCustomersByCustomerIdMeteringPoints List a customer's metering points
// (GET /api/v1/energy/customers/{customerId}/metering-points)
func (s *server) GetEnergyCustomersByCustomerIdMeteringPoints(context.Context, gen.GetEnergyCustomersByCustomerIdMeteringPointsRequestObject) (gen.GetEnergyCustomersByCustomerIdMeteringPointsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyMeteringPointsByIdConsumption List current consumption intervals
// (GET /api/v1/energy/metering-points/{id}/consumption)
func (s *server) GetEnergyMeteringPointsByIdConsumption(context.Context, gen.GetEnergyMeteringPointsByIdConsumptionRequestObject) (gen.GetEnergyMeteringPointsByIdConsumptionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPointsByIdConsumption Add a manual consumption interval
// (POST /api/v1/energy/metering-points/{id}/consumption)
func (s *server) PostEnergyMeteringPointsByIdConsumption(context.Context, gen.PostEnergyMeteringPointsByIdConsumptionRequestObject) (gen.PostEnergyMeteringPointsByIdConsumptionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyMeteringPointsByIdConsumptionAggregate Aggregate consumption
// (GET /api/v1/energy/metering-points/{id}/consumption/aggregate)
func (s *server) GetEnergyMeteringPointsByIdConsumptionAggregate(context.Context, gen.GetEnergyMeteringPointsByIdConsumptionAggregateRequestObject) (gen.GetEnergyMeteringPointsByIdConsumptionAggregateResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyStatsAttention Get energy dashboard attention items
// (GET /api/v1/energy/stats/attention)
func (s *server) GetEnergyStatsAttention(context.Context, gen.GetEnergyStatsAttentionRequestObject) (gen.GetEnergyStatsAttentionResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyStatsSummary Get energy dashboard summary
// (GET /api/v1/energy/stats/summary)
func (s *server) GetEnergyStatsSummary(context.Context, gen.GetEnergyStatsSummaryRequestObject) (gen.GetEnergyStatsSummaryResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyStatsTimeseries Get energy dashboard time series
// (GET /api/v1/energy/stats/timeseries)
func (s *server) GetEnergyStatsTimeseries(context.Context, gen.GetEnergyStatsTimeseriesRequestObject) (gen.GetEnergyStatsTimeseriesResponseObject, error) {
	return nil, module.ErrNotImplemented
}
