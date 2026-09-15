package energy

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the customer-scoped energy area (EP/CustomerEnergyEndpoints.cs,
// group /customers): getEnergyCustomersByCustomerIdMeteringPoints,
// getEnergyCustomersByCustomerIdConsumption and
// getEnergyCustomersByCustomerIdConsumptionAggregate. None of these three
// call contracts.CustomerDirectory (energy inventory §1.2): they trust
// customerId as an opaque foreign key and simply return empty results for
// an unknown one — unlike supplyperiods.go's Create/Switch, the only two
// operations in this module that actually resolve a customer.

// GetEnergyCustomersByCustomerIdMeteringPoints List a customer's metering points
// (GET /api/v1/energy/customers/{customerId}/metering-points)
//
// GetCustomerMeteringPointsEndpoint.cs:15-32: every metering point joined to
// at least one of the customer's non-cancelled supply periods, ordered by
// point id, each carrying that filtered, Start-ordered period list. No
// existence check on customerId; an unknown id answers [], always 200
// (energy inventory §1.2).
func (s *server) GetEnergyCustomersByCustomerIdMeteringPoints(ctx context.Context, req gen.GetEnergyCustomersByCustomerIdMeteringPointsRequestObject) (gen.GetEnergyCustomersByCustomerIdMeteringPointsResponseObject, error) {
	q := store.New(s.deps.Pool)
	points, err := q.ListCustomerMeteringPoints(ctx, req.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("energy: list customer metering points: %w", err)
	}
	periods, err := q.ListCustomerSupplyPeriods(ctx, req.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("energy: list customer supply periods: %w", err)
	}
	periodsByPoint := make(map[int32][]gen.SupplyPeriodResponse, len(points))
	for _, p := range periods {
		periodsByPoint[p.MeteringPointID] = append(periodsByPoint[p.MeteringPointID], supplyPeriodResponseOf(p))
	}

	data := make([]gen.CustomerMeteringPointResponse, 0, len(points))
	for _, r := range points {
		data = append(data, gen.CustomerMeteringPointResponse{
			MeteringPoint: gen.MeteringPointResponse{
				Id:                           r.ID,
				Gsrn:                         r.Gsrn,
				MeterNumber:                  r.ActiveMeterNumber,
				Address:                      gen.AddressResponse{StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City, CountryCode: r.CountryCode},
				PriceArea:                    r.PriceArea,
				GridArea:                     r.GridArea,
				ExpectedAnnualConsumptionKwh: floatPtrFromNumeric(r.ExpectedAnnualConsumptionKwh),
				Latitude:                     r.Latitude,
				Longitude:                    r.Longitude,
				ConnectionStatus:             r.ConnectionStatus,
				CreatedAt:                    r.CreatedAt,
				UpdatedAt:                    r.UpdatedAt,
			},
			SupplyPeriods: periodsByPoint[r.ID],
		})
	}
	return gen.GetEnergyCustomersByCustomerIdMeteringPoints200JSONResponse(data), nil
}

// GetEnergyCustomersByCustomerIdConsumption List a customer's consumption
// (GET /api/v1/energy/customers/{customerId}/consumption)
//
// GetCustomerConsumptionEndpoint.cs:9-25: to<=from (both given) -> 400 plain
// Problem, same title/detail as the metering-point version -> current
// intervals covered by one of the customer's non-cancelled supply periods
// (CustomerConsumptionFilter.CurrentIntervals), optionally narrowed to one
// metering point, further narrowed by from/to, ordered by Start. No
// existence check on customerId (energy inventory §1.2).
func (s *server) GetEnergyCustomersByCustomerIdConsumption(ctx context.Context, req gen.GetEnergyCustomersByCustomerIdConsumptionRequestObject) (gen.GetEnergyCustomersByCustomerIdConsumptionResponseObject, error) {
	p := req.Params
	if consumptionIntervalRangeInvalid(p.From, p.To) {
		return gen.GetEnergyCustomersByCustomerIdConsumption400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid interval", "to must be later than from.")), nil
	}

	q := store.New(s.deps.Pool)
	rows, err := q.ListConsumptionByCustomer(ctx, store.ListConsumptionByCustomerParams{
		CustomerID: req.CustomerId, MeteringPointID: p.MeteringPointId, FromTs: p.From, ToTs: p.To,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list customer consumption: %w", err)
	}
	data := make([]gen.ConsumptionResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.ConsumptionResponse{
			Id: r.ID, MeteringPointId: r.MeteringPointID, Start: r.Start, End: r.End,
			QuantityKwh: floatFromNumeric(r.QuantityKwh), Quality: r.Quality, Source: r.Source, ReceivedAt: r.ReceivedAt,
		})
	}
	return gen.GetEnergyCustomersByCustomerIdConsumption200JSONResponse(data), nil
}

// GetEnergyCustomersByCustomerIdConsumptionAggregate Aggregate a customer's consumption
// (GET /api/v1/energy/customers/{customerId}/consumption/aggregate)
//
// GetCustomerConsumptionAggregateEndpoint.cs:13-44: the same
// ConsumptionAggregateValidation as the metering-point version -> the set of
// metering points covered by one of the customer's non-cancelled supply
// periods (optionally narrowed to one) -> one aggregate query per matching
// point, N+1 by design (energy inventory §8 oddity 10) -> merged and sorted
// by (meteringPointId, bucketStart).
func (s *server) GetEnergyCustomersByCustomerIdConsumptionAggregate(ctx context.Context, req gen.GetEnergyCustomersByCustomerIdConsumptionAggregateRequestObject) (gen.GetEnergyCustomersByCustomerIdConsumptionAggregateResponseObject, error) {
	p := req.Params
	resolution, errs := validateConsumptionAggregate(p.From, p.To, p.Resolution)
	if len(errs) > 0 {
		return gen.GetEnergyCustomersByCustomerIdConsumptionAggregate400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem(validationErrorsOneOrMore, errs)), nil
	}

	q := store.New(s.deps.Pool)
	points, err := q.ListCustomerMeteringPointPriceAreas(ctx, store.ListCustomerMeteringPointPriceAreasParams{
		CustomerID: req.CustomerId, MeteringPointID: p.MeteringPointId,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list customer metering points: %w", err)
	}

	data := make([]gen.CustomerConsumptionAggregateResponse, 0)
	for _, point := range points {
		rows, aerr := q.AggregateConsumptionByCustomerMeteringPoint(ctx, store.AggregateConsumptionByCustomerMeteringPointParams{
			CustomerID: req.CustomerId, MeteringPointID: point.ID, FromTs: *p.From, ToTs: *p.To,
			Resolution: resolution, TimeZone: marketTimeZone(&point.PriceArea),
		})
		if aerr != nil {
			return nil, fmt.Errorf("energy: aggregate customer consumption: %w", aerr)
		}
		for _, r := range rows {
			data = append(data, gen.CustomerConsumptionAggregateResponse{
				MeteringPointId: r.MeteringPointID, BucketStart: r.BucketStart, BucketEnd: r.BucketEnd,
				QuantityKwh: floatFromNumeric(r.QuantityKwh), IntervalCount: r.IntervalCount, HasEstimated: r.HasEstimated,
			})
		}
	}

	// ListCustomerMeteringPointPriceAreas already orders points by id and
	// AggregateConsumptionByCustomerMeteringPoint orders each point's own
	// rows by bucket, so data is already sorted by (meteringPointId,
	// bucketStart) — GetCustomerConsumptionAggregateEndpoint's explicit
	// OrderBy/ThenBy (:40-43) is redundant with the loop order above, not a
	// separate sort this port needs to perform.
	return gen.GetEnergyCustomersByCustomerIdConsumptionAggregate200JSONResponse(data), nil
}
