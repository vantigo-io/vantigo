package projects

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the four stats operations (design §4.3): the list page's
// key-figure row at /stats, and the three under /stats/* the host dashboard
// reads out of every module through one generic URL shape
// (apps/host/frontend/src/routes/dashboard.tsx). The three dashboard
// operations therefore copy customers/stats.go exactly — the same from/to
// parameters, the same period normalization, the same daily-bucket and
// attention-item shapes, the same "invalid period before invalid metric"
// order — and differ only in which figures they carry.
//
// All four count over the caller's visibility (visibilityFor), never over
// the table: a member's numbers are the numbers of the list they can open.

// metricNewProjects is the only timeseries metric this module has. It is
// matched case-insensitively, as every other module matches its own.
const metricNewProjects = "newProjects"

// overdueType is the attention item's type. The dashboard keys its own
// per-module link building on this string, so it is part of the contract in
// everything but the schema.
const overdueType = "projectOverdue"

// GetProjectsStats Get project counts per status
// (GET /api/v1/projects/stats)
//
// The list page's KPI row. Unlike the three dashboard operations it takes no
// period: it is the state of the caller's projects now.
func (s *server) GetProjectsStats(ctx context.Context, _ gen.GetProjectsStatsRequestObject) (gen.GetProjectsStatsResponseObject, error) {
	v := s.visibilityFor(ctx)
	q := store.New(s.deps.Pool)
	counts, err := q.ProjectStatusCounts(ctx, store.ProjectStatusCountsParams{UserID: v.UserID, SeeAll: v.SeeAll})
	if err != nil {
		return nil, fmt.Errorf("projects: status counts: %w", err)
	}
	return gen.GetProjectsStats200JSONResponse{
		Planned:   int32(counts.Planned),
		Active:    int32(counts.Active),
		OnHold:    int32(counts.OnHold),
		Completed: int32(counts.Completed),
		Cancelled: int32(counts.Cancelled),
	}, nil
}

// GetProjectsStatsSummary Get project dashboard summary
// (GET /api/v1/projects/stats/summary)
//
// newProjects is what was created inside the period; activeProjects is a
// state now rather than a count over the period, which is why its delta
// compares against how many were active when the period began.
func (s *server) GetProjectsStatsSummary(ctx context.Context, req gen.GetProjectsStatsSummaryRequestObject) (gen.GetProjectsStatsSummaryResponseObject, error) {
	periodFrom, periodTo, previousFrom, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetProjectsStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	v := s.visibilityFor(ctx)
	q := store.New(s.deps.Pool)
	counts, err := q.ProjectStatsSummaryCounts(ctx, store.ProjectStatsSummaryCountsParams{
		UserID: v.UserID, SeeAll: v.SeeAll,
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: stats summary: %w", err)
	}

	return gen.GetProjectsStatsSummary200JSONResponse{
		From:                periodFrom,
		To:                  periodTo,
		ActiveProjects:      int32(counts.Active),
		ActiveProjectsDelta: int32(counts.Active) - int32(counts.ActiveAtPeriodStart),
		NewProjects:         int32(counts.NewProjects),
		NewProjectsDelta:    int32(counts.NewProjects) - int32(counts.PreviousNewProjects),
	}, nil
}

// GetProjectsStatsTimeseries Get project dashboard time series
// (GET /api/v1/projects/stats/timeseries)
//
// The period is normalized, and its validity checked, before the metric name
// is, so a bad metric against an invalid period still answers "Invalid
// period" first — the order every other module's timeseries answers in.
func (s *server) GetProjectsStatsTimeseries(ctx context.Context, req gen.GetProjectsStatsTimeseriesRequestObject) (gen.GetProjectsStatsTimeseriesResponseObject, error) {
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetProjectsStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	if !strings.EqualFold(metric, metricNewProjects) {
		return gen.GetProjectsStatsTimeseries400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid metric", "Metric must be: "+metricNewProjects+".")), nil
	}

	v := s.visibilityFor(ctx)
	q := store.New(s.deps.Pool)
	rows, err := q.ProjectCreationBuckets(ctx, store.ProjectCreationBucketsParams{
		UserID: v.UserID, SeeAll: v.SeeAll, RangeFrom: periodFrom, RangeTo: periodTo,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: timeseries: %w", err)
	}
	buckets := make([]gen.ProjectStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		buckets = append(buckets, gen.ProjectStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: apicommon.Ptr(r.Value)})
	}
	return gen.GetProjectsStatsTimeseries200JSONResponse(buckets), nil
}

// GetProjectsStatsAttention Get project dashboard attention items
// (GET /api/v1/projects/stats/attention)
//
// Active projects whose end date has passed. occurredAt is the end date
// itself, at midnight UTC: the dashboard sorts its merged list by it and
// prints it as "3 days ago", and the day a project fell behind is what that
// sentence should be about.
func (s *server) GetProjectsStatsAttention(ctx context.Context, _ gen.GetProjectsStatsAttentionRequestObject) (gen.GetProjectsStatsAttentionResponseObject, error) {
	// Truncate on a UTC instant is midnight UTC, which is the calendar day a
	// date column is compared against.
	today := pgtype.Date{Time: s.deps.Clock().UTC().Truncate(24 * time.Hour), Valid: true}

	v := s.visibilityFor(ctx)
	q := store.New(s.deps.Pool)
	rows, err := q.OverdueActiveProjects(ctx, store.OverdueActiveProjectsParams{
		UserID: v.UserID, SeeAll: v.SeeAll, Today: today,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: stats attention: %w", err)
	}

	items := make([]gen.ProjectStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		id := strconv.FormatInt(int64(r.ID), 10)
		items = append(items, gen.ProjectStatsAttentionItem{
			Id:         id,
			Type:       overdueType,
			Title:      r.Name,
			OccurredAt: r.EndDate.Time,
			EntityId:   id,
		})
	}
	return gen.GetProjectsStatsAttention200JSONResponse(items), nil
}
