package customers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is the dashboard's four stats operations: the tenant-wide key
// figures at /stats (CustomersEndpoints.cs:26, its own handler file
// GetCustomerStatsEndpoint.cs) and the three under /stats/* that
// CustomerStatsEndpoints.cs maps as one sub-group.

// GetCustomersStats Get tenant-wide customer key figures
// (GET /api/v1/customers/stats)
//
// GetCustomerStatsEndpoint.cs:29-74: every figure excludes archived
// customers; the four identity-derived figures are only computed, and only
// present in the response, when the caller holds legal-identity-view
// (inventory §6) — null otherwise, never omitted or zero.
func (s *server) GetCustomersStats(ctx context.Context, _ gen.GetCustomersStatsRequestObject) (gen.GetCustomersStatsResponseObject, error) {
	q := store.New(s.deps.Pool)
	now := s.deps.Clock()
	figures, err := q.CustomerKeyFigures(ctx, now.AddDate(0, 0, -30))
	if err != nil {
		return nil, fmt.Errorf("customers: key figures: %w", err)
	}

	resp := gen.GetCustomerStatsResponse{
		TotalCount:         int32(figures.TotalCount),
		ActiveCount:        int32(figures.ActiveCount),
		NewLast30DaysCount: int32(figures.NewLast30DaysCount),
	}
	if s.hasPermission(ctx, legalIdentityView) {
		idFigures, err := q.CustomerIdentityFigures(ctx)
		if err != nil {
			return nil, fmt.Errorf("customers: identity figures: %w", err)
		}
		resp.BusinessCount = ptr(int32(idFigures.BusinessCount))
		resp.PersonCount = ptr(int32(idFigures.PersonCount))
		resp.MissingIdentityCount = ptr(int32(idFigures.MissingIdentityCount))
		resp.DistinctCountryCount = ptr(int32(idFigures.DistinctCountryCount))
	}
	return gen.GetCustomersStats200JSONResponse(resp), nil
}

// GetCustomersStatsAttention Get customer dashboard attention items
// (GET /api/v1/customers/stats/attention)
//
// A stub in .NET too (CustomerStatsEndpoints.cs:111-112, inventory §8.5):
// always an empty array, since no dashboard "attention items" feature
// exists behind this operation yet.
func (s *server) GetCustomersStatsAttention(context.Context, gen.GetCustomersStatsAttentionRequestObject) (gen.GetCustomersStatsAttentionResponseObject, error) {
	return gen.GetCustomersStatsAttention200JSONResponse([]gen.CustomerStatsAttentionItem{}), nil
}

// summaryDefaultPeriodDays is CustomerStatsEndpoints.DefaultPeriodDays
// (CustomerStatsEndpoints.cs:13).
const summaryDefaultPeriodDays = 30

// normalizePeriod is CustomerStatsEndpoints.TryNormalizePeriod
// (CustomerStatsEndpoints.cs:114-135), shared by Summary and Timeseries:
// to defaults to now, from defaults to 30 days before to; ok is false when
// from is after to, the only way either handler answers 400.
func normalizePeriod(from, to *time.Time, now time.Time) (periodFrom, periodTo, previousFrom time.Time, ok bool) {
	periodTo = now
	if to != nil {
		periodTo = *to
	}
	periodFrom = periodTo.AddDate(0, 0, -summaryDefaultPeriodDays)
	if from != nil {
		periodFrom = *from
	}
	if periodFrom.After(periodTo) {
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	// The immediately preceding window of the same length as
	// [periodFrom, periodTo) (Period.Previous, CustomerStatsEndpoints.cs:139).
	previousFrom = periodFrom.Add(-periodTo.Sub(periodFrom))
	return periodFrom, periodTo, previousFrom, true
}

// invalidPeriodDetail is TryNormalizePeriod's problem detail
// (CustomerStatsEndpoints.cs:131-133), shared verbatim by Summary and
// Timeseries.
const invalidPeriodDetail = "The 'from' value must be earlier than or equal to the 'to' value."

// GetCustomersStatsSummary Get customer dashboard summary
// (GET /api/v1/customers/stats/summary)
func (s *server) GetCustomersStatsSummary(ctx context.Context, req gen.GetCustomersStatsSummaryRequestObject) (gen.GetCustomersStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, previousFrom, ok := normalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCustomersStatsSummary400ApplicationProblemPlusJSONResponse(problem("Invalid period", invalidPeriodDetail)), nil
	}

	q := store.New(s.deps.Pool)
	customerCounts, err := q.CustomerStatsSummaryCustomerCounts(ctx, store.CustomerStatsSummaryCustomerCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: stats summary: %w", err)
	}
	contactCounts, err := q.CustomerStatsSummaryContactCounts(ctx, store.CustomerStatsSummaryContactCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: stats summary: %w", err)
	}

	return gen.GetCustomersStatsSummary200JSONResponse{
		From:                      periodFrom,
		To:                        periodTo,
		TotalActiveCustomers:      int32(customerCounts.Active),
		TotalActiveCustomersDelta: int32(customerCounts.Active) - int32(customerCounts.ActiveAtPeriodStart),
		NewCustomers:              int32(customerCounts.NewCustomers),
		NewCustomersDelta:         int32(customerCounts.NewCustomers) - int32(customerCounts.PreviousNewCustomers),
		NewContacts:               int32(contactCounts.NewContacts),
		NewContactsDelta:          int32(contactCounts.NewContacts) - int32(contactCounts.PreviousNewContacts),
	}, nil
}

// GetCustomersStatsTimeseries Get customer dashboard time series
// (GET /api/v1/customers/stats/timeseries)
//
// CustomerStatsEndpoints.cs:70-108: the period is normalized (and its
// validity checked) before the metric name is, so a bad metric against an
// invalid period still answers "Invalid period" first.
func (s *server) GetCustomersStatsTimeseries(ctx context.Context, req gen.GetCustomersStatsTimeseriesRequestObject) (gen.GetCustomersStatsTimeseriesResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, _, ok := normalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCustomersStatsTimeseries400ApplicationProblemPlusJSONResponse(problem("Invalid period", invalidPeriodDetail)), nil
	}

	metric := deref(req.Params.Metric)
	newCustomers := strings.EqualFold(metric, "newCustomers")
	if !newCustomers && !strings.EqualFold(metric, "newContacts") {
		return gen.GetCustomersStatsTimeseries400ApplicationProblemPlusJSONResponse(
			problem("Invalid metric", "Metric must be one of: newCustomers, newContacts.")), nil
	}

	q := store.New(s.deps.Pool)
	buckets := make([]gen.CustomerStatsDailyBucket, 0)
	if newCustomers {
		rows, err := q.CustomerCreationBuckets(ctx, store.CustomerCreationBucketsParams{RangeFrom: periodFrom, RangeTo: periodTo})
		if err != nil {
			return nil, fmt.Errorf("customers: timeseries: %w", err)
		}
		for _, r := range rows {
			buckets = append(buckets, gen.CustomerStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: ptr(r.Value)})
		}
	} else {
		rows, err := q.ContactCreationBuckets(ctx, store.ContactCreationBucketsParams{RangeFrom: periodFrom, RangeTo: periodTo})
		if err != nil {
			return nil, fmt.Errorf("customers: timeseries: %w", err)
		}
		for _, r := range rows {
			buckets = append(buckets, gen.CustomerStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: ptr(r.Value)})
		}
	}
	return gen.GetCustomersStatsTimeseries200JSONResponse(buckets), nil
}
