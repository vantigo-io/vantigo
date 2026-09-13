package communications

import (
	"context"
	"fmt"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// This file is the dashboard's three stats operations
// (EP/CommunicationsStatsEndpoints.cs, communications inventory §1.6,
// §2's "Stats summary/timeseries" bullet, §3 items 1-2, §3.3, §19 items 19
// and 21): getCommunicationsStatsSummary, getCommunicationsStatsTimeseries
// and getCommunicationsStatsAttention.
//
// Unlike every other operation in this module, all three answer RFC 7807
// ProblemDetails on 400 (errors.go's problem helper), not this module's own
// {"error":{...}} shape — and attention alone among the three never answers
// 400 at all: it takes no query parameters and performs no validation
// (CommunicationsStatsEndpoints.cs:26-29 declares no 400 response, unlike
// summary and timeseries at :19/:24).
//
// attention is also the one dashboard "attention items" operation in this
// project's three ported modules (customers, products, communications) that
// is NOT a parity stub: CommunicationsStatsEndpoints.Attention (:115-151)
// runs a real query, concatenating failed deliveries with unanswered-open
// conversations, ordered OccurredAt ASCENDING then Take(100) — the hundred
// OLDEST items, not the newest (inventory §19 item 19: "reads like an
// accident but is what the code does"). See
// queries/stats.sql's CommunicationsStatsAttentionItems for the ported
// query and TestGetCommunicationsStatsAttention_OrdersOldestFirst for the
// pin.

// communicationsStatsDefaultPeriodDays is
// CommunicationsStatsEndpoints.DefaultPeriodDays (:12).
const communicationsStatsDefaultPeriodDays = 30

// normalizeCommunicationsStatsPeriod is TryNormalizePeriod (:153-174),
// shared by Summary and Timeseries: to defaults to now, from defaults to 30
// days before to; ok is false when from is after to (strictly — from == to
// is valid, :161's `normalizedFrom <= normalizedTo`), the only way either
// handler answers 400.
func normalizeCommunicationsStatsPeriod(from, to *time.Time, now time.Time) (periodFrom, periodTo, previousFrom time.Time, ok bool) {
	periodTo = now
	if to != nil {
		periodTo = *to
	}
	periodFrom = periodTo.AddDate(0, 0, -communicationsStatsDefaultPeriodDays)
	if from != nil {
		periodFrom = *from
	}
	if periodFrom.After(periodTo) {
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	// The immediately preceding window of the same length as
	// [periodFrom, periodTo) (Period.Previous, :178).
	previousFrom = periodFrom.Add(-periodTo.Sub(periodFrom))
	return periodFrom, periodTo, previousFrom, true
}

// invalidCommunicationsPeriodTitle/Detail are TryNormalizePeriod's problem
// text (:169-172), shared verbatim by Summary and Timeseries — byte for
// byte, inventory §3.3's first row.
const (
	invalidCommunicationsPeriodTitle  = "Invalid period"
	invalidCommunicationsPeriodDetail = "The 'from' value must be earlier than or equal to the 'to' value."
)

// GetCommunicationsStatsSummary Get communications dashboard summary
// (GET /api/v1/communications/stats/summary)
//
// Summary (:32-72): open is the current TOTAL open-conversation count
// (unwindowed), so openConversationsDelta is NOT a period delta like the
// other three deltas — it is open minus openBeforePeriod, the count of
// conversations that were already open and already existed before the
// window started (inventory §19 item 21, CommunicationsStatsEndpoints.cs:64).
// Getting this "right" as a symmetrical period delta is exactly the
// plausible-looking bug a port must not introduce; pinned by
// TestGetCommunicationsStatsSummary_OpenConversationsDeltaIsNotAPeriodDelta.
func (s *server) GetCommunicationsStatsSummary(ctx context.Context, req gen.GetCommunicationsStatsSummaryRequestObject) (gen.GetCommunicationsStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, previousFrom, ok := normalizeCommunicationsStatsPeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCommunicationsStatsSummary400ApplicationProblemPlusJSONResponse(
			problem(invalidCommunicationsPeriodTitle, invalidCommunicationsPeriodDetail)), nil
	}

	q := store.New(s.deps.Pool)
	convCounts, err := q.CommunicationsConversationStatsSummaryCounts(ctx, store.CommunicationsConversationStatsSummaryCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: stats summary: conversation counts: %w", err)
	}
	msgCounts, err := q.CommunicationsMessageStatsSummaryCounts(ctx, store.CommunicationsMessageStatsSummaryCountsParams{
		PeriodFrom: periodFrom, PeriodTo: periodTo, PreviousFrom: previousFrom,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: stats summary: message counts: %w", err)
	}

	return gen.GetCommunicationsStatsSummary200JSONResponse{
		From:                     periodFrom,
		To:                       periodTo,
		OpenConversations:        int32(convCounts.Open),
		OpenConversationsDelta:   int32(convCounts.Open) - int32(convCounts.OpenBeforePeriod),
		NewConversations:         int32(convCounts.NewConversations),
		NewConversationsDelta:    int32(convCounts.NewConversations) - int32(convCounts.PreviousNewConversations),
		Messages:                 int32(msgCounts.Messages),
		MessagesDelta:            int32(msgCounts.Messages) - int32(msgCounts.PreviousMessages),
		ClosedConversations:      int32(convCounts.Closed),
		ClosedConversationsDelta: int32(convCounts.Closed) - int32(convCounts.PreviousClosed),
	}, nil
}

// GetCommunicationsStatsTimeseries Get communications dashboard time series
// (GET /api/v1/communications/stats/timeseries)
//
// Timeseries (:74-113): the period is normalized (and its validity checked)
// BEFORE the metric name is (:81 runs before :82-89), so a bad metric
// against an invalid period still answers "Invalid period" first — pinned
// by TestGetCommunicationsStatsTimeseries_PeriodErrorWinsOverMetricError.
// The metric comparison runs on the LOWERCASED value (:82-83,
// `metric?.Trim().ToLowerInvariant()` against "newconversations"/"messages"),
// so "NEWCONVERSATIONS" is accepted even though the message and the contract
// both say "newConversations" — a case-sensitive port would be STRICTER
// than .NET and reject input .NET accepts. Pinned by
// TestGetCommunicationsStatsTimeseries_MetricComparisonIsCaseInsensitive.
func (s *server) GetCommunicationsStatsTimeseries(ctx context.Context, req gen.GetCommunicationsStatsTimeseriesRequestObject) (gen.GetCommunicationsStatsTimeseriesResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, _, ok := normalizeCommunicationsStatsPeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCommunicationsStatsTimeseries400ApplicationProblemPlusJSONResponse(
			problem(invalidCommunicationsPeriodTitle, invalidCommunicationsPeriodDetail)), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	normalizedMetric := strings.ToLower(strings.TrimSpace(metric))
	if normalizedMetric != "newconversations" && normalizedMetric != "messages" {
		return gen.GetCommunicationsStatsTimeseries400ApplicationProblemPlusJSONResponse(
			problem("Invalid metric", "Metric must be one of: newConversations, messages.")), nil
	}

	q := store.New(s.deps.Pool)
	if normalizedMetric == "newconversations" {
		rows, err := q.CommunicationsConversationCreationBuckets(ctx, store.CommunicationsConversationCreationBucketsParams{
			RangeFrom: periodFrom, RangeTo: periodTo,
		})
		if err != nil {
			return nil, fmt.Errorf("communications: stats timeseries: conversation buckets: %w", err)
		}
		return gen.GetCommunicationsStatsTimeseries200JSONResponse(communicationsStatsBuckets(rows)), nil
	}

	rows, err := q.CommunicationsMessageOccurredBuckets(ctx, store.CommunicationsMessageOccurredBucketsParams{
		RangeFrom: periodFrom, RangeTo: periodTo,
	})
	if err != nil {
		return nil, fmt.Errorf("communications: stats timeseries: message buckets: %w", err)
	}
	return gen.GetCommunicationsStatsTimeseries200JSONResponse(communicationsMessageStatsBuckets(rows)), nil
}

// communicationsStatsBuckets converts the newConversations metric's rows.
// Duplicated (rather than made generic) alongside
// communicationsMessageStatsBuckets below because the two row types are
// distinct sqlc-generated structs with no shared interface — the same
// small-duplication convention this module's other area files already
// follow (e.g. suppressions.go's own doc comment on
// validSuppressionReason).
func communicationsStatsBuckets(rows []store.CommunicationsConversationCreationBucketsRow) []gen.CommunicationsStatsDailyBucket {
	buckets := make([]gen.CommunicationsStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		buckets = append(buckets, gen.CommunicationsStatsDailyBucket{
			Date:  openapi_types.Date{Time: r.Day.Time},
			Value: r.Value,
		})
	}
	return buckets
}

// communicationsMessageStatsBuckets converts the messages metric's rows.
func communicationsMessageStatsBuckets(rows []store.CommunicationsMessageOccurredBucketsRow) []gen.CommunicationsStatsDailyBucket {
	buckets := make([]gen.CommunicationsStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		buckets = append(buckets, gen.CommunicationsStatsDailyBucket{
			Date:  openapi_types.Date{Time: r.Day.Time},
			Value: r.Value,
		})
	}
	return buckets
}

// GetCommunicationsStatsAttention Get communications dashboard attention items
// (GET /api/v1/communications/stats/attention)
//
// Attention (:115-151) — the dispatch's central correction: unlike the
// customers and products modules' own /stats/attention (each a parity stub
// always answering []), this one runs a real query. See
// queries/stats.sql's CommunicationsStatsAttentionItems for the ported
// UNION ALL and this file's own doc comment for the ordering hazard. No
// query parameters, no validation, no 400 — the contract declares only
// 200/401/403 for this operation (communications.yaml:1916-1943).
func (s *server) GetCommunicationsStatsAttention(ctx context.Context, _ gen.GetCommunicationsStatsAttentionRequestObject) (gen.GetCommunicationsStatsAttentionResponseObject, error) {
	cutoff := s.deps.Clock().Add(-24 * time.Hour)
	q := store.New(s.deps.Pool)
	rows, err := q.CommunicationsStatsAttentionItems(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("communications: stats attention: %w", err)
	}
	items := make([]gen.CommunicationsStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, gen.CommunicationsStatsAttentionItem{
			Id:         r.ID,
			Type:       r.Type,
			Title:      r.Title,
			OccurredAt: r.OccurredAt,
			EntityId:   r.EntityID,
		})
	}
	return gen.GetCommunicationsStatsAttention200JSONResponse(items), nil
}
