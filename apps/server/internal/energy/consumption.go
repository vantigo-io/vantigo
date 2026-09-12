package energy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the metering-point-scoped Consumption area
// (EP/Consumption/*.cs): getEnergyMeteringPointsByIdConsumption,
// postEnergyMeteringPointsByIdConsumption and
// getEnergyMeteringPointsByIdConsumptionAggregate. customers.go covers the
// customer-scoped consumption operations, which share this file's
// validation and market-time-zone helpers.

// hasUTCOffset, defined in supplyperiods.go, is reused below:
// ConsumptionInterval.Validate's UTC check (ConsumptionInterval.cs:25) is the
// identical `Offset != TimeSpan.Zero` shape SupplyPeriod.Validate uses.

// validateConsumptionInterval is ConsumptionInterval.Validate(start, end,
// quantityKwh) (ConsumptionInterval.cs:23-28): both timestamps must carry a
// zero UTC offset, end must be strictly after start (equal is rejected),
// and quantity must be zero or greater — zero itself is allowed.
func validateConsumptionInterval(start, end time.Time, quantityKwh float64) string {
	if !hasUTCOffset(start) || !hasUTCOffset(end) {
		return "Start and end must be UTC timestamps."
	}
	if !end.After(start) {
		return "End must be later than start."
	}
	if quantityKwh < 0 {
		return "Quantity must be zero or greater."
	}
	return ""
}

// consumptionIntervalRangeInvalid is GetConsumptionEndpoint/GetCustomerConsumptionEndpoint's
// shared from/to sanity check (GetConsumptionEndpoint.cs:14-15): only
// checked when both bounds are supplied, and rejects to<=from — to equal to
// from is invalid, not just to before from.
func consumptionIntervalRangeInvalid(from, to *time.Time) bool {
	return from != nil && to != nil && !to.After(*from)
}

// validationErrorsOneOrMore is TypedResults.ValidationProblem's default
// title when no title is given (Microsoft.AspNetCore.Mvc.ValidationProblemDetails'
// own constructor default): GetConsumptionAggregateEndpoint.cs:21 and
// GetCustomerConsumptionAggregateEndpoint.cs:23 both call
// `TypedResults.ValidationProblem(errors)` with no title argument.
const validationErrorsOneOrMore = "One or more validation errors occurred."

// consumptionResolutions are ConsumptionAggregateValidation's three accepted
// resolution strings (:19-20), matched against the normalized
// (ToLowerInvariant) input.
var consumptionResolutions = map[string]bool{"hour": true, "day": true, "month": true}

// validateConsumptionAggregate is ConsumptionAggregateValidation.TryValidate
// (:5-23), shared verbatim by the metering-point and customer aggregate
// endpoints: from and to are each independently required, from>=to is a
// separate (overwriting) error on "to", and resolution is normalized to
// lowercase before being checked against the fixed set — every check runs
// regardless of the others, so a request can carry more than one field
// error at once.
func validateConsumptionAggregate(from, to *time.Time, resolution *string) (normalizedResolution string, errs map[string][]string) {
	if resolution != nil {
		normalizedResolution = strings.ToLower(*resolution)
	}
	errs = map[string][]string{}
	if from == nil {
		errs["from"] = []string{"The from field is required."}
	}
	if to == nil {
		errs["to"] = []string{"The to field is required."}
	}
	if from != nil && to != nil && !from.Before(*to) {
		errs["to"] = []string{"The to field must be later than from."}
	}
	if !consumptionResolutions[normalizedResolution] {
		errs["resolution"] = []string{"Resolution must be one of: hour, day, month."}
	}
	return normalizedResolution, errs
}

// marketTimeZone is MarketTimeZone.GetId (MarketTimeZone.cs:10-20): maps the
// price area's first two characters to an IANA zone. Oslo is the fallback
// default for anything else — including "NO", an empty string, or a nil
// price area — never a Norway-specific match (energy inventory §4.1, this
// task's dispatch correction 1: a NO->Oslo entry alongside a default would
// still pass every realistic case and only fail on "XX1" or nil, which is
// exactly what MarketTimeZoneTests' six facts pin). priceArea is a pointer
// only so the nil-input .NET fact ports directly; production callers always
// have a real (non-nullable) column value to pass.
func marketTimeZone(priceArea *string) string {
	var prefix string
	if priceArea != nil && len(*priceArea) >= 2 {
		prefix = (*priceArea)[:2]
	}
	switch prefix {
	case "SE":
		return "Europe/Stockholm"
	case "DK":
		return "Europe/Copenhagen"
	case "FI":
		return "Europe/Helsinki"
	default:
		return "Europe/Oslo"
	}
}

// GetEnergyMeteringPointsByIdConsumption List current consumption intervals
// (GET /api/v1/energy/metering-points/{id}/consumption)
//
// GetConsumptionEndpoint.cs:11-24: to<=from (both given) -> 400 plain
// Problem (title "Invalid interval") -> existence (404) -> current rows
// only, ordered by Start, with the asymmetric Start>=from/End<=to filter
// (energy inventory §4 line 341) applied only when the caller supplied that
// bound.
func (s *server) GetEnergyMeteringPointsByIdConsumption(ctx context.Context, req gen.GetEnergyMeteringPointsByIdConsumptionRequestObject) (gen.GetEnergyMeteringPointsByIdConsumptionResponseObject, error) {
	p := req.Params
	if consumptionIntervalRangeInvalid(p.From, p.To) {
		return gen.GetEnergyMeteringPointsByIdConsumption400ApplicationProblemPlusJSONResponse(
			problem("Invalid interval", "to must be later than from.")), nil
	}

	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.GetEnergyMeteringPointsByIdConsumption404Response{}, nil
	}

	rows, err := q.ListConsumptionByMeteringPoint(ctx, store.ListConsumptionByMeteringPointParams{
		MeteringPointID: req.Id, FromTs: p.From, ToTs: p.To,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list consumption: %w", err)
	}
	data := make([]gen.ConsumptionResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.ConsumptionResponse{
			Id: r.ID, MeteringPointId: r.MeteringPointID, Start: r.Start, End: r.End,
			QuantityKwh: floatFromNumeric(r.QuantityKwh), Quality: r.Quality, Source: r.Source, ReceivedAt: r.ReceivedAt,
		})
	}
	return gen.GetEnergyMeteringPointsByIdConsumption200JSONResponse(data), nil
}

// PostEnergyMeteringPointsByIdConsumption Add a manual consumption interval
// (POST /api/v1/energy/metering-points/{id}/consumption)
//
// AddManualConsumptionEndpoint.cs:12-38: interval validation -> 400
// ValidationProblem field "interval" -> existence (404) -> the exact-tuple
// supersede lookup (energy inventory §2.4: no overlap check, only an exact
// (start,end) match among current rows) -> insert, with the superseded
// row's is_current flipped first. Deliberately no advisory lock and no
// serializable retry here (unlike supplyperiods.go's Create/Switch): .NET
// runs this non-transactionally too, relying on the unique index as the
// real backstop for a race (energy inventory §5), and
// module.ResponseError/httpx.WriteError already map a 23505 to a generic
// 409 — adding overlap protection here would silently diverge from .NET
// (this task's dispatch correction 2).
func (s *server) PostEnergyMeteringPointsByIdConsumption(ctx context.Context, req gen.PostEnergyMeteringPointsByIdConsumptionRequestObject) (gen.PostEnergyMeteringPointsByIdConsumptionResponseObject, error) {
	body := gen.ManualConsumptionRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	if msg := validateConsumptionInterval(body.Start, body.End, body.QuantityKwh); msg != "" {
		return gen.PostEnergyMeteringPointsByIdConsumption400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid consumption interval", map[string][]string{"interval": {msg}})), nil
	}

	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.PostEnergyMeteringPointsByIdConsumption404Response{}, nil
	}

	now := s.deps.Clock()
	var result store.InsertConsumptionIntervalRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if perr := txq.EnsureConsumptionPartition(ctx, body.Start); perr != nil {
			return perr
		}

		previous, perr := txq.FindCurrentConsumptionInterval(ctx, store.FindCurrentConsumptionIntervalParams{
			MeteringPointID: req.Id, Start: body.Start, EndAt: body.End,
		})
		hasPrevious := true
		switch {
		case errors.Is(perr, pgx.ErrNoRows):
			hasPrevious = false
		case perr != nil:
			return perr
		}

		var supersedesID *int64
		var supersedesStart *time.Time
		if hasPrevious {
			if uerr := txq.SetConsumptionIntervalNotCurrent(ctx, store.SetConsumptionIntervalNotCurrentParams{
				ID: previous.ID, Start: previous.Start,
			}); uerr != nil {
				return uerr
			}
			supersedesID = &previous.ID
			supersedesStart = &previous.Start
		}

		quantity, qerr := numericFromFloat(body.QuantityKwh)
		if qerr != nil {
			return qerr
		}
		var ierr error
		result, ierr = txq.InsertConsumptionInterval(ctx, store.InsertConsumptionIntervalParams{
			MeteringPointID: req.Id, Start: body.Start, EndAt: body.End, QuantityKwh: quantity,
			ReceivedAt: now, SupersedesID: supersedesID, SupersedesStart: supersedesStart,
		})
		return ierr
	})
	if err != nil {
		return nil, fmt.Errorf("energy: add manual consumption: %w", err)
	}
	return gen.PostEnergyMeteringPointsByIdConsumption200JSONResponse(gen.ConsumptionResponse{
		Id: result.ID, MeteringPointId: result.MeteringPointID, Start: result.Start, End: result.End,
		QuantityKwh: floatFromNumeric(result.QuantityKwh), Quality: result.Quality, Source: result.Source, ReceivedAt: result.ReceivedAt,
	}), nil
}

// GetEnergyMeteringPointsByIdConsumptionAggregate Aggregate consumption
// (GET /api/v1/energy/metering-points/{id}/consumption/aggregate)
//
// GetConsumptionAggregateEndpoint.cs:12-33: ConsumptionAggregateValidation
// (400, no title — validationErrorsOneOrMore) -> existence (404) -> the
// verbatim aggregation query (energy inventory §4), keyed to the metering
// point's own market time zone (§4.1).
func (s *server) GetEnergyMeteringPointsByIdConsumptionAggregate(ctx context.Context, req gen.GetEnergyMeteringPointsByIdConsumptionAggregateRequestObject) (gen.GetEnergyMeteringPointsByIdConsumptionAggregateResponseObject, error) {
	p := req.Params
	resolution, errs := validateConsumptionAggregate(p.From, p.To, p.Resolution)
	if len(errs) > 0 {
		return gen.GetEnergyMeteringPointsByIdConsumptionAggregate400ApplicationProblemPlusJSONResponse(
			validationProblem(validationErrorsOneOrMore, errs)), nil
	}

	q := store.New(s.deps.Pool)
	point, err := q.GetMeteringPointByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetEnergyMeteringPointsByIdConsumptionAggregate404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get metering point: %w", err)
	}

	rows, err := q.AggregateConsumptionByMeteringPoint(ctx, store.AggregateConsumptionByMeteringPointParams{
		MeteringPointID: req.Id, FromTs: *p.From, ToTs: *p.To, Resolution: resolution, TimeZone: marketTimeZone(&point.PriceArea),
	})
	if err != nil {
		return nil, fmt.Errorf("energy: aggregate consumption: %w", err)
	}
	data := make([]gen.ConsumptionAggregateResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.ConsumptionAggregateResponse{
			BucketStart: r.BucketStart, BucketEnd: r.BucketEnd,
			QuantityKwh: floatFromNumeric(r.QuantityKwh), IntervalCount: r.IntervalCount, HasEstimated: r.HasEstimated,
		})
	}
	return gen.GetEnergyMeteringPointsByIdConsumptionAggregate200JSONResponse(data), nil
}
