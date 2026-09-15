package products

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/products/store"
)

// This file is the dashboard's three stats operations
// (EP/ProductStatsEndpoints.cs), mirroring customers/stats.go's shape (the
// completed broader template) — its own products inventory §6 lists no
// .NET test class for ProductStatsEndpoints.cs at all (a repo-wide search
// of Products.Module.Tests turns up none, the same gap customers/stats.go's
// doc comment notes for its own module), so nothing here carries a
// "Ported from" marker; stats_test.go's tests are this task's own, written
// to the .NET handler's behavior since no test exists to port.

// allProductStatuses is Enum.GetValues<ProductStatus>() (Domain/Products/ProductStatus.cs)
// in declaration order: CreateStatusDictionary (:106-116) always emits one
// entry per status, present in the data or not.
var allProductStatuses = []string{"Draft", "Active", "Discontinued"}

// dateFromPgtype converts a nullable SQL date into the contract's date
// type, nil when the column was NULL. Duplicated from customers'
// customers.go: depguard forbids this module importing customers, and the
// function is six lines.
func dateFromPgtype(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// ptrInt64 returns a pointer to a copy of v, for ProductStatsDailyBucket's
// optional Value field.
func ptrInt64(v int64) *int64 { return &v }

// GetProductsStatsAttention Get products dashboard attention items
// (GET /api/v1/products/stats/attention)
//
// A stub in .NET too (products inventory §1.2, §7 oddity 4,
// ProductStatsEndpoints.cs:103-104): always answers an empty array,
// regardless of what data exists. This is intentional parity with .NET, not
// an unfinished operation — nothing in the database is queried.
func (s *server) GetProductsStatsAttention(context.Context, gen.GetProductsStatsAttentionRequestObject) (gen.GetProductsStatsAttentionResponseObject, error) {
	return gen.GetProductsStatsAttention200JSONResponse([]gen.ProductStatsAttentionItem{}), nil
}

// GetProductsStatsSummary Get products dashboard summary
// (GET /api/v1/products/stats/summary)
//
// ProductStatsEndpoints.Summary (:33-74): the default period is the last 30
// days; totalActiveProducts/newProducts and their deltas compare against
// the immediately preceding window of the same length; statusCounts and
// statusCountDeltas always carry one lowercase key per ProductStatus value
// (Draft/Active/Discontinued), 0 for a status with no products, never
// omitted.
func (s *server) GetProductsStatsSummary(ctx context.Context, req gen.GetProductsStatsSummaryRequestObject) (gen.GetProductsStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, previousFrom, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetProductsStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	q := store.New(s.deps.Pool)
	counts, err := q.ProductStatsSummaryCounts(ctx, store.ProductStatsSummaryCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("products: stats summary: %w", err)
	}

	currentRows, err := q.ProductStatusCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("products: status counts: %w", err)
	}
	currentByStatus := make(map[string]int64, len(currentRows))
	for _, r := range currentRows {
		currentByStatus[r.Status] = r.Value
	}
	beforeRows, err := q.ProductStatusCountsBefore(ctx, periodFrom)
	if err != nil {
		return nil, fmt.Errorf("products: status counts before period: %w", err)
	}
	beforeByStatus := make(map[string]int64, len(beforeRows))
	for _, r := range beforeRows {
		beforeByStatus[r.Status] = r.Value
	}

	statusCounts := make(map[string]int64, len(allProductStatuses))
	statusCountDeltas := make(map[string]int64, len(allProductStatuses))
	for _, status := range allProductStatuses {
		key := strings.ToLower(status)
		statusCounts[key] = currentByStatus[status]
		statusCountDeltas[key] = currentByStatus[status] - beforeByStatus[status]
	}

	return gen.GetProductsStatsSummary200JSONResponse{
		From:                     periodFrom,
		To:                       periodTo,
		TotalActiveProducts:      int32(counts.Active),
		TotalActiveProductsDelta: int32(counts.Active) - int32(counts.ActiveAtPeriodStart),
		NewProducts:              int32(counts.NewProducts),
		NewProductsDelta:         int32(counts.NewProducts) - int32(counts.PreviousNewProducts),
		StatusCounts:             statusCounts,
		StatusCountDeltas:        statusCountDeltas,
	}, nil
}

// GetProductsStatsTimeseries Get products dashboard time series
// (GET /api/v1/products/stats/timeseries)
//
// ProductStatsEndpoints.Timeseries (:76-101): the period is normalized (and
// its validity checked) before the metric name is, so a bad metric against
// an invalid period still answers "Invalid period" first. Only
// metric=newProducts is implemented (case-insensitively); anything else is
// 400 "Invalid metric" (products inventory §1.2).
func (s *server) GetProductsStatsTimeseries(ctx context.Context, req gen.GetProductsStatsTimeseriesRequestObject) (gen.GetProductsStatsTimeseriesResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetProductsStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	if !strings.EqualFold(metric, "newProducts") {
		return gen.GetProductsStatsTimeseries400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid metric", "Metric must be: newProducts.")), nil
	}

	q := store.New(s.deps.Pool)
	rows, err := q.ProductCreationBuckets(ctx, store.ProductCreationBucketsParams{RangeFrom: periodFrom, RangeTo: periodTo})
	if err != nil {
		return nil, fmt.Errorf("products: timeseries: %w", err)
	}
	buckets := make([]gen.ProductStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		buckets = append(buckets, gen.ProductStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: ptrInt64(r.Value)})
	}
	return gen.GetProductsStatsTimeseries200JSONResponse(buckets), nil
}
