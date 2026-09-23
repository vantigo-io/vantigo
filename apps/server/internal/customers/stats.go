package customers

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
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
		resp.BusinessCount = apicommon.Ptr(int32(idFigures.BusinessCount))
		resp.PersonCount = apicommon.Ptr(int32(idFigures.PersonCount))
		resp.MissingIdentityCount = apicommon.Ptr(int32(idFigures.MissingIdentityCount))
		resp.DistinctCountryCount = apicommon.Ptr(int32(idFigures.DistinctCountryCount))
	}
	return gen.GetCustomersStats200JSONResponse(resp), nil
}

// The four attention types design D4 defines, each computed from the stored
// registry record against the current customer rather than from events —
// which is what lets the list clear itself the moment the underlying fact
// does, with no "dismiss" action anywhere.
const (
	attentionRegistryBankrupt    = "registryBankrupt"
	attentionRegistryLiquidation = "registryLiquidation"
	attentionRegistryDeleted     = "registryDeleted"
	attentionRegistryRenamed     = "registryRenamed"
)

// GetCustomersStatsAttention Get customer dashboard attention items
// (GET /api/v1/customers/stats/attention)
//
// No longer .NET's stub (CustomerStatsEndpoints.cs:111-112, inventory
// §8.5's "always an empty array"): design D4 gives this operation a real
// feature, new to the port. Two families of item, both computed from state each
// time this is asked — no stored list, no dismiss action: the four registry ones
// (Brreg in full design D4), whose own work attentionItemsFrom does, pure and
// table-tested on its own (stats_internal_test.go), and the two follow-up ones
// (follow-ups design D2).
//
// The follow-up half is the first item here that depends on WHO is asking: it
// reports open follow-ups assigned to the caller or unassigned, so the endpoint
// now reads the principal from the context the way time's and expenses' own
// items do. An unauthenticated caller cannot reach this (the router refuses
// first), and if one somehow did, a nil caller answers only the unassigned
// follow-ups — never everybody's.
//
// It is also the first half of this endpoint that is response-shaped by a
// second permission, for the same reason the identity figures above are: this
// operation admits a caller on customers:view alone, a follow-up item names a
// timeline entry, and customers:timeline-view is a SENSITIVE permission
// (module.go) — so a caller who cannot open the timeline must not be handed its
// entry ids by the dashboard instead. Absent that permission the query is not
// even run: the items are not merely withheld, they are never read.
//
// The follow-up half is capped at twenty by the query itself (its LIMIT, and
// the comment there says why): the twenty most overdue are what a dashboard
// card can usefully name, and a backlog of three hundred is read on the
// Follow-ups page instead. The registry half needs no such cap — it is at most
// one item per customer whose registry record says something is wrong — and
// sortAttentionItems below still orders the merged list by date, not by which
// half an item came from.
func (s *server) GetCustomersStatsAttention(ctx context.Context, _ gen.GetCustomersStatsAttentionRequestObject) (gen.GetCustomersStatsAttentionResponseObject, error) {
	q := store.New(s.deps.Pool)
	registryRows, err := q.RegistryAttentionCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: stats attention: %w", err)
	}
	items := attentionItemsFrom(registryRows)

	if s.hasPermission(ctx, timelineView) {
		var caller *uuid.UUID
		if p, ok := contracts.PrincipalFrom(ctx); ok && p.UserID != uuid.Nil {
			id := p.UserID
			caller = &id
		}
		today := civilDate(s.deps.Clock())
		followUpRows, err := q.FollowUpAttentionCandidates(ctx, store.FollowUpAttentionCandidatesParams{
			Today: pgtype.Date{Time: today, Valid: true}, CallerID: caller,
		})
		if err != nil {
			return nil, fmt.Errorf("customers: stats attention follow-ups: %w", err)
		}
		items = append(items, followUpAttentionItems(followUpRows, today)...)
	}

	sortAttentionItems(items)
	return gen.GetCustomersStatsAttention200JSONResponse(items), nil
}

// attentionItemsFrom is GetCustomersStatsAttention's pure half: one item per
// customer at most (registryAttentionType's precedence), ordered newest
// fetch first, ties broken by id — the dashboard reads a list, not a
// timeline, so "what changed most recently" is the useful order and a
// stable tiebreaker keeps repeated calls from reshuffling ties.
func attentionItemsFrom(rows []store.RegistryAttentionCandidatesRow) []gen.CustomerStatsAttentionItem {
	items := make([]gen.CustomerStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		typ, ok := registryAttentionType(r)
		if !ok {
			continue
		}
		id := strconv.FormatInt(int64(r.CustomerID), 10)
		items = append(items, gen.CustomerStatsAttentionItem{
			Id: typ + "/" + id, Type: typ, Title: r.Name, OccurredAt: attentionOccurredAt(typ, r), EntityId: id,
		})
	}
	sortAttentionItems(items)
	return items
}

// sortAttentionItems is the order every item on this endpoint answers in,
// whatever produced it: occurredAt DESCENDING — the most recent date first —
// with ties broken by id so repeated calls cannot reshuffle them. Descending is
// not a claim about urgency, and for the follow-up items it is the opposite of
// it: a follow-up due today sorts above one ten days overdue, because its date
// is later. It is the order the host's dashboard expects from every module's
// /stats/attention, and the host merges all of them newest-first anyway, so the
// useful thing this function guarantees is that the order is total and stable.
func sortAttentionItems(items []gen.CustomerStatsAttentionItem) {
	slices.SortStableFunc(items, func(a, b gen.CustomerStatsAttentionItem) int {
		if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(a.Id, b.Id)
	})
}

// attentionOccurredAt is the day the thing itself happened, not the moment we
// noticed it (fix round 2, I3 — projects' own rule, projects/stats.go): the
// dashboard sorts its merged list by occurredAt and prints it as "3 days ago",
// so dating a 2019 bankruptcy by the last refresh would re-float it to the top
// of everybody's list every time anyone clicked Refresh.
//
// Each of the three status items takes the registry's own date for it, falling
// back to the fetch time when the registry sends the flag without a date (it
// does, and a missing date must not read as the epoch). A rename has no date
// at all — the registry says what an entity is called, never since when — so
// for that one the fetch time is the honest answer: the day we first could
// have known.
func attentionOccurredAt(typ string, r store.RegistryAttentionCandidatesRow) time.Time {
	switch typ {
	case attentionRegistryDeleted:
		return registryDayOr(r.DeletedOn, r.FetchedAt)
	case attentionRegistryBankrupt:
		return registryDayOr(r.BankruptOn, r.FetchedAt)
	case attentionRegistryLiquidation:
		return registryDayOr(r.LiquidationOn, r.FetchedAt)
	default:
		return r.FetchedAt
	}
}

// registryDayOr is one of the record's dates as an instant: midnight UTC of
// that day, the same way deleted_on already reaches the API (registry.go's
// registryDateResponse), or fallback when the column is NULL.
func registryDayOr(d pgtype.Date, fallback time.Time) time.Time {
	if !d.Valid {
		return fallback
	}
	return time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, time.UTC)
}

// registryAttentionType is one row's attention type, and whether it has
// one at all (design D4, controller ruling): deleted beats bankrupt beats
// liquidation beats renamed — a struck-off company's most useful single
// sentence is that it is deleted, whatever else is also true of it. A
// rename only surfaces once none of the three status flags do, and only
// when the customer has a legal name to compare against at all (a nil
// LegalName means no legal identity, or one whose name was never set —
// either way there is nothing to call a rename).
func registryAttentionType(r store.RegistryAttentionCandidatesRow) (string, bool) {
	switch {
	case r.DeletedOn.Valid:
		return attentionRegistryDeleted, true
	case r.Bankrupt:
		return attentionRegistryBankrupt, true
	case r.UnderLiquidation || r.UnderForcedLiquidation:
		return attentionRegistryLiquidation, true
	case r.LegalName != nil && strings.TrimSpace(r.RecordName) != strings.TrimSpace(*r.LegalName):
		return attentionRegistryRenamed, true
	default:
		return "", false
	}
}

// GetCustomersStatsSummary Get customer dashboard summary
// (GET /api/v1/customers/stats/summary)
func (s *server) GetCustomersStatsSummary(ctx context.Context, req gen.GetCustomersStatsSummaryRequestObject) (gen.GetCustomersStatsSummaryResponseObject, error) {
	now := s.deps.Clock()
	periodFrom, periodTo, previousFrom, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCustomersStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
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
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, now)
	if !ok {
		return gen.GetCustomersStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	metric := deref(req.Params.Metric)
	newCustomers := strings.EqualFold(metric, "newCustomers")
	if !newCustomers && !strings.EqualFold(metric, "newContacts") {
		return gen.GetCustomersStatsTimeseries400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid metric", "Metric must be one of: newCustomers, newContacts.")), nil
	}

	q := store.New(s.deps.Pool)
	buckets := make([]gen.CustomerStatsDailyBucket, 0)
	if newCustomers {
		rows, err := q.CustomerCreationBuckets(ctx, store.CustomerCreationBucketsParams{RangeFrom: periodFrom, RangeTo: periodTo})
		if err != nil {
			return nil, fmt.Errorf("customers: timeseries: %w", err)
		}
		for _, r := range rows {
			buckets = append(buckets, gen.CustomerStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: apicommon.Ptr(r.Value)})
		}
	} else {
		rows, err := q.ContactCreationBuckets(ctx, store.ContactCreationBucketsParams{RangeFrom: periodFrom, RangeTo: periodTo})
		if err != nil {
			return nil, fmt.Errorf("customers: timeseries: %w", err)
		}
		for _, r := range rows {
			buckets = append(buckets, gen.CustomerStatsDailyBucket{Date: dateFromPgtype(r.Day), Value: apicommon.Ptr(r.Value)})
		}
	}
	return gen.GetCustomersStatsTimeseries200JSONResponse(buckets), nil
}
