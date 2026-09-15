package energy

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the dashboard's three stats operations
// (EP/EnergyStatsEndpoints.cs): getEnergyStatsAttention, getEnergyStatsSummary
// and getEnergyStatsTimeseries — mounted directly under /stats, no
// metering-point or customer sub-group.

// energyStatsAttentionWindowDays is Attention's hard-coded look-ahead
// (:113: now.AddDays(30)).
const energyStatsAttentionWindowDays = 30

// clampInt32 narrows a bigint count to int32, saturating at the int32
// bounds rather than silently wrapping — EnergyStatsSummaryCounts' four
// counts are Postgres COUNT(*) results (bigint/int64), but
// EnergyStatsSummaryResponse's fields are int32 (matching .NET's int).
// Fix-round finding: the unguarded int32(n) conversion this replaced would
// wrap a count past 2^31-1 into a negative number with no error and no log
// line — a dashboard silently lying is worse than one capped at a
// deliberately-visible ceiling no real installation is expected to reach.
// Deltas are computed at int64 precision by the caller *before* this narrows
// them, so a delta near the boundary is not itself corrupted by narrowing
// each side first and subtracting after.
func clampInt32(n int64) int32 {
	switch {
	case n > math.MaxInt32:
		return math.MaxInt32
	case n < math.MinInt32:
		return math.MinInt32
	default:
		return int32(n)
	}
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
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetEnergyStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
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
	meteringPointCountDelta := counts.MeteringPointCount - counts.MeteringPointCountAtPeriodStart
	activeSupplyPeriodsDelta := counts.ActiveSupplyPeriods - counts.ActiveSupplyPeriodsAtPeriodStart
	return gen.GetEnergyStatsSummary200JSONResponse{
		From:                     periodFrom,
		To:                       periodTo,
		MeteringPointCount:       clampInt32(counts.MeteringPointCount),
		MeteringPointCountDelta:  clampInt32(meteringPointCountDelta),
		ActiveSupplyPeriods:      clampInt32(counts.ActiveSupplyPeriods),
		ActiveSupplyPeriodsDelta: clampInt32(activeSupplyPeriodsDelta),
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
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetEnergyStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	if !strings.EqualFold(metric, "consumptionKwh") {
		return gen.GetEnergyStatsTimeseries400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid metric", "Metric must be: consumptionKwh.")), nil
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
