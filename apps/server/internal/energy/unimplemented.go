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

// GetEnergyMeteringPoints List metering points
// (GET /api/v1/energy/metering-points)
func (s *server) GetEnergyMeteringPoints(context.Context, gen.GetEnergyMeteringPointsRequestObject) (gen.GetEnergyMeteringPointsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPoints Create a metering point
// (POST /api/v1/energy/metering-points)
func (s *server) PostEnergyMeteringPoints(context.Context, gen.PostEnergyMeteringPointsRequestObject) (gen.PostEnergyMeteringPointsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyMeteringPoint Get a metering point
// (GET /api/v1/energy/metering-points/{id})
func (s *server) GetEnergyMeteringPoint(context.Context, gen.GetEnergyMeteringPointRequestObject) (gen.GetEnergyMeteringPointResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PutEnergyMeteringPointsById Update a metering point
// (PUT /api/v1/energy/metering-points/{id})
func (s *server) PutEnergyMeteringPointsById(context.Context, gen.PutEnergyMeteringPointsByIdRequestObject) (gen.PutEnergyMeteringPointsByIdResponseObject, error) {
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

// GetEnergyMeteringPointsByIdMeters List meter history
// (GET /api/v1/energy/metering-points/{id}/meters)
func (s *server) GetEnergyMeteringPointsByIdMeters(context.Context, gen.GetEnergyMeteringPointsByIdMetersRequestObject) (gen.GetEnergyMeteringPointsByIdMetersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPointsByIdMeters Replace a meter
// (POST /api/v1/energy/metering-points/{id}/meters)
func (s *server) PostEnergyMeteringPointsByIdMeters(context.Context, gen.PostEnergyMeteringPointsByIdMetersRequestObject) (gen.PostEnergyMeteringPointsByIdMetersResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// GetEnergyMeteringPointsByIdSupplyPeriods List supply periods
// (GET /api/v1/energy/metering-points/{id}/supply-periods)
func (s *server) GetEnergyMeteringPointsByIdSupplyPeriods(context.Context, gen.GetEnergyMeteringPointsByIdSupplyPeriodsRequestObject) (gen.GetEnergyMeteringPointsByIdSupplyPeriodsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPointsByIdSupplyPeriods Create a supply period
// (POST /api/v1/energy/metering-points/{id}/supply-periods)
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriods(context.Context, gen.PostEnergyMeteringPointsByIdSupplyPeriodsRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPointsByIdSupplyPeriodsSwitch Switch supply period customer
// (POST /api/v1/energy/metering-points/{id}/supply-periods/switch)
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriodsSwitch(context.Context, gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitchRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitchResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId Cancel a supply period
// (DELETE /api/v1/energy/metering-points/{id}/supply-periods/{periodId})
func (s *server) DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId(context.Context, gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdRequestObject) (gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdResponseObject, error) {
	return nil, module.ErrNotImplemented
}

// PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd End a supply period
// (POST /api/v1/energy/metering-points/{id}/supply-periods/{periodId}/end)
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd(context.Context, gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEndRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEndResponseObject, error) {
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
