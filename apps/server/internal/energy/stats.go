package energy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the dashboard's three stats operations
// (EP/EnergyStatsEndpoints.cs): getEnergyStatsAttention, getEnergyStatsSummary
// and getEnergyStatsTimeseries — mounted directly under /stats, no
// metering-point or customer sub-group.

// energyStatsDefaultPeriodDays is EnergyStatsEndpoints.DefaultPeriodDays
// (EnergyStatsEndpoints.cs:12).
const energyStatsDefaultPeriodDays = 30

// energyStatsAttentionWindowDays is Attention's hard-coded look-ahead
// (:113: now.AddDays(30)).
const energyStatsAttentionWindowDays = 30

// invalidEnergyPeriodDetail is TryNormalizePeriod's problem detail
// (:145-147), shared verbatim by Summary and Timeseries.
const invalidEnergyPeriodDetail = "The 'from' value must be earlier than or equal to the 'to' value."

// normalizeEnergyStatsPeriod is EnergyStatsEndpoints.TryNormalizePeriod
// (:132-149): to defaults to now, from defaults to 30 days before to; ok is
// false only when the normalized from is after the normalized to (from ==
// to is allowed, matching the source's `normalizedFrom <= normalizedTo`
// check).
func normalizeEnergyStatsPeriod(from, to *time.Time, now time.Time) (periodFrom, periodTo time.Time, ok bool) {
	periodTo = now
	if to != nil {
		periodTo = *to
	}
	periodFrom = periodTo.AddDate(0, 0, -energyStatsDefaultPeriodDays)
	if from != nil {
		periodFrom = *from
	}
	return periodFrom, periodTo, !periodFrom.After(periodTo)
}

// GetEnergyStatsAttention Get energy dashboard attention items
// (GET /api/v1/energy/stats/attention)
//
// EnergyStatsEndpoints.Attention (:108-123): active supply periods whose End
// falls within [now, now+30d], one attention item per period, type
// "supplyPeriodExpiring". No error branch at all (energy inventory §1.3).
func (s *server) GetEnergyStatsAttention(ctx context.Context, _ gen.GetEnergyStatsAttentionRequestObject) (gen.GetEnergyStatsAttentionResponseObject, error) {
	now := s.deps.Clock()
	q := store.New(s.deps.Pool)
	rows, err := q.ListExpiringSupplyPeriods(ctx, store.ListExpiringSupplyPeriodsParams{
		Now: now, ExpiresBy: now.AddDate(0, 0, energyStatsAttentionWindowDays),
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list expiring supply periods: %w", err)
	}
	items := make([]gen.EnergyStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, gen.EnergyStatsAttentionItem{
			Id:         strconv.Itoa(int(r.ID)),
			Type:       "supplyPeriodExpiring",
			Title:      "Supply period expires soon",
			OccurredAt: *r.End,
			EntityId:   strconv.Itoa(int(r.MeteringPointID)),
		})
	}
	return gen.GetEnergyStatsAttention200JSONResponse(items), nil
}

// GetEnergyStatsSummary Get energy dashboard summary
// (GET /api/v1/energy/stats/summary)
//
// EnergyStatsEndpoints.Summary (:33-75): the default period is the last 30
// days; metering-point and active-supply-period counts compare current vs.
// as-of-period-start; the consumption sum compares the period against the
// immediately preceding period of equal length.
func (s *server) GetEnergyStatsSummary(ctx context.Context, req gen.GetEnergyStatsSummaryRequestObject) (gen.GetEnergyStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, ok := normalizeEnergyStatsPeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetEnergyStatsSummary400ApplicationProblemPlusJSONResponse(problem("Invalid period", invalidEnergyPeriodDetail)), nil
	}
	previousFrom := periodFrom.Add(-periodTo.Sub(periodFrom))

	q := store.New(s.deps.Pool)
	counts, err := q.EnergyStatsSummaryCounts(ctx, store.EnergyStatsSummaryCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: stats summary: %w", err)
	}

	consumptionKwh := floatFromNumeric(counts.ConsumptionKwh)
	previousConsumptionKwh := floatFromNumeric(counts.PreviousConsumptionKwh)
	return gen.GetEnergyStatsSummary200JSONResponse{
		From:                     periodFrom,
		To:                       periodTo,
		MeteringPointCount:       int32(counts.MeteringPointCount),
		MeteringPointCountDelta:  int32(counts.MeteringPointCount) - int32(counts.MeteringPointCountAtPeriodStart),
		ActiveSupplyPeriods:      int32(counts.ActiveSupplyPeriods),
		ActiveSupplyPeriodsDelta: int32(counts.ActiveSupplyPeriods) - int32(counts.ActiveSupplyPeriodsAtPeriodStart),
		ConsumptionKwh:           consumptionKwh,
		ConsumptionKwhDelta:      consumptionKwh - previousConsumptionKwh,
		PreviousConsumptionKwh:   previousConsumptionKwh,
	}, nil
}

// GetEnergyStatsTimeseries Get energy dashboard time series
// (GET /api/v1/energy/stats/timeseries)
//
// EnergyStatsEndpoints.Timeseries (:76-105): the period is normalized (and
// its validity checked) before the metric name is, so a bad metric against
// an invalid period still answers "Invalid period" first. Only
// metric=consumptionKwh is implemented (case-insensitively); anything else
// is 400 "Invalid metric". Bucketed by the *UTC* calendar date of Start, not
// the metering point's own market zone (energy inventory §1.3 line 64).
func (s *server) GetEnergyStatsTimeseries(ctx context.Context, req gen.GetEnergyStatsTimeseriesRequestObject) (gen.GetEnergyStatsTimeseriesResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, ok := normalizeEnergyStatsPeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetEnergyStatsTimeseries400ApplicationProblemPlusJSONResponse(problem("Invalid period", invalidEnergyPeriodDetail)), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	if !strings.EqualFold(metric, "consumptionKwh") {
		return gen.GetEnergyStatsTimeseries400ApplicationProblemPlusJSONResponse(
			problem("Invalid metric", "Metric must be: consumptionKwh.")), nil
	}

	q := store.New(s.deps.Pool)
	rows, err := q.EnergyConsumptionDailyBuckets(ctx, store.EnergyConsumptionDailyBucketsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: consumption daily buckets: %w", err)
	}
	buckets := make([]gen.EnergyStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		date := openapi_types.Date{Time: r.BucketDate.Time}
		value := floatFromNumeric(r.QuantityKwh)
		buckets = append(buckets, gen.EnergyStatsDailyBucket{Date: &date, Value: &value})
	}
	return gen.GetEnergyStatsTimeseries200JSONResponse(buckets), nil
}
