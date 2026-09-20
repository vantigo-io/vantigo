package expenses

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the four reads the host's dashboard makes of every module
// through one generic URL shape (apps/host/frontend/src/routes/dashboard.tsx),
// so they copy time's stats.go, which copies projects', which copies
// customers': the same from/to parameters, the same period normalization, the
// same daily bucket, the same attention item, and the same "invalid period
// before invalid metric" order. Only the figures differ.
//
// Every figure is scoped in SQL (queries/stats.sql) to what the caller may
// see: their own expenses, or the approval queue's scope (approvalScope).
// None of it runs in a transaction, so the one directory read the scope needs
// is a plain call; the names the attention items need are one bulk call for
// the whole list. The statement count is fixed whatever the installation
// holds — a dashboard that costs a query per expense is a dashboard nobody
// keeps open.

// metricNetAmount is the one metric the timeseries answers, matched
// case-insensitively as every other module matches its own.
const metricNetAmount = "netAmount"

// The attention items' types. The dashboard keys its per-module link building
// on these strings, so they are part of the contract in everything but the
// schema: an approvalWaiting item's entityId is the owner's user id, an
// expenseRejected item's is the expense id, and a reimbursementWaiting item's
// is the literal reimbursementsEntity — the payroll list is what it links to,
// not any one expense.
const (
	attentionApprovalWaiting      = "approvalWaiting"
	attentionExpenseRejected      = "expenseRejected"
	attentionReimbursementWaiting = "reimbursementWaiting"

	reimbursementsEntity = "reimbursements"

	// claimEntityPrefix is how a travel claim names itself among the attention
	// items. An expense's entity is its bare id, so a trip's needs a word in
	// front of it: the two units number independently and "12" would say
	// nothing about which page to open.
	claimEntityPrefix = "claim/"
)

// claimEntityID is one travel claim's entity id among the attention items.
func claimEntityID(id int64) string { return claimEntityPrefix + strconv.FormatInt(id, 10) }

// rejectedAttentionLimit is how many of the caller's own sent-back **units** —
// standalone expenses and travel claims together — the dashboard is told about.
// It is a dashboard rather than a list: somebody with a hundred rejections has a
// page to open.
//
// It bounds each of the two queries as well as their merge, which is why both
// read it: neither can hold more than the cap on its own, so the merge sorts at
// most twice the cap and cuts it to the cap.
const rejectedAttentionLimit = 20

// GetExpensesStats Get the caller's expense key figures
// (GET /api/v1/expenses/stats)
//
// The strip above the caller's own expense list: how many of theirs stand in
// each status, what they are still owed, and how much is waiting for them to
// approve. Three statements, whatever the installation holds.
func (s *server) GetExpensesStats(ctx context.Context, _ gen.GetExpensesStatsRequestObject) (gen.GetExpensesStatsResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	counts, err := s.statusCounts(ctx, q, c.UserID)
	if err != nil {
		return nil, err
	}
	owed, err := s.unreimbursedFor(ctx, q, c.UserID)
	if err != nil {
		return nil, err
	}
	awaiting, _, err := s.awaitingApproval(ctx, q, c, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesStats200JSONResponse{
		Draft:              counts.Drafts,
		Submitted:          counts.Submitted,
		Approved:           counts.Approved,
		Rejected:           counts.Rejected,
		Unreimbursed:       owed,
		AwaitingMyApproval: awaiting,
	}, nil
}

// unitCounts is how many of the caller's own **units** stand in each status:
// their standalone expenses and their travel claims added together. A trip
// counts once whatever it holds — its lines keep their own status column at its
// default and are never counted (unitOf in authorize.go), because a figure of
// five drafts for a trip its owner can do nothing with one at a time would
// point at nothing anybody can act on.
type unitCounts struct{ Drafts, Submitted, Approved, Rejected int32 }

func (s *server) statusCounts(ctx context.Context, q *store.Queries, userID uuid.UUID) (unitCounts, error) {
	entries, err := q.StatsMyStatusCounts(ctx, userID)
	if err != nil {
		return unitCounts{}, fmt.Errorf("expenses: count the caller's expenses: %w", err)
	}
	claims, err := q.StatsMyClaimStatusCounts(ctx, userID)
	if err != nil {
		return unitCounts{}, fmt.Errorf("expenses: count the caller's travel claims: %w", err)
	}
	return unitCounts{
		Drafts:    int32(entries.Drafts + claims.Drafts),
		Submitted: int32(entries.Submitted + claims.Submitted),
		Approved:  int32(entries.Approved + claims.Approved),
		Rejected:  int32(entries.Rejected + claims.Rejected),
	}, nil
}

// unreimbursedFor is what one person is still owed, one line per currency and
// nothing when there is nothing — never a sum across currencies, which would
// be a number in neither (design §4).
func (s *server) unreimbursedFor(ctx context.Context, q *store.Queries, userID uuid.UUID) ([]gen.ExpensesCurrencyAmount, error) {
	rows, err := q.StatsMyUnreimbursed(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("expenses: sum what the caller is owed: %w", err)
	}
	out := make([]gen.ExpensesCurrencyAmount, 0, len(rows))
	for _, row := range rows {
		amount, err := ratFromNumeric(row.Amount)
		if err != nil {
			return nil, err
		}
		out = append(out, gen.ExpensesCurrencyAmount{Currency: row.Currency, Amount: floatOfRat(amount)})
	}
	return out, nil
}

// awaitingApproval is the approval figure both dashboard reads carry, and how
// many of the same expenses were already waiting when periodFrom began.
func (s *server) awaitingApproval(ctx context.Context, q *store.Queries, c *caller, periodFrom time.Time) (int32, int32, error) {
	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return 0, 0, err
	}
	if !scope.approvesAny() {
		return 0, 0, nil
	}
	row, err := q.StatsAwaitingApproval(ctx, store.StatsAwaitingApprovalParams{
		PeriodFrom: periodFrom, SeeAll: scope.seeAll,
		ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("expenses: count the expenses awaiting approval: %w", err)
	}
	// The other unit, counted once each: a trip waiting for this approver is
	// one thing to decide, not one per line it holds.
	claims, err := q.StatsAwaitingApprovalClaims(ctx, store.StatsAwaitingApprovalClaimsParams{
		PeriodFrom: periodFrom, SeeAll: scope.seeAll,
		ManagedProjectIds: scope.managed, LockedBefore: scope.lock, TimeZone: c.Settings.TimeZone,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("expenses: count the travel claims awaiting approval: %w", err)
	}
	awaiting := row.Awaiting + claims.Awaiting
	atStart := row.AwaitingAtPeriodStart + claims.AwaitingAtPeriodStart
	return int32(awaiting), int32(awaiting - atStart), nil
}

// GetExpensesStatsSummary Get expenses dashboard summary
// (GET /api/v1/expenses/stats/summary)
//
// The period is echoed and moves only awaitingMyApprovalDelta, as time's does.
// The other two figures are a state now and carry no delta: a draft count
// cannot be reconstructed for a past moment — a rejected expense becomes a
// draft again, and the row keeps no record of when it last did — and a
// currency-mixed amount would have no meaning as one, which is the reason
// projects' readyMilestones has none either.
func (s *server) GetExpensesStatsSummary(ctx context.Context, req gen.GetExpensesStatsSummaryRequestObject) (gen.GetExpensesStatsSummaryResponseObject, error) {
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetExpensesStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	counts, err := s.statusCounts(ctx, q, c.UserID)
	if err != nil {
		return nil, err
	}
	owed, err := s.unreimbursedFor(ctx, q, c.UserID)
	if err != nil {
		return nil, err
	}
	awaiting, delta, err := s.awaitingApproval(ctx, q, c, periodFrom)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesStatsSummary200JSONResponse{
		From:                    periodFrom,
		To:                      periodTo,
		AwaitingMyApproval:      awaiting,
		AwaitingMyApprovalDelta: delta,
		MyDrafts:                counts.Drafts,
		MyUnreimbursed:          owed,
	}, nil
}

// GetExpensesStatsTimeseries Get expenses dashboard time series
// (GET /api/v1/expenses/stats/timeseries)
//
// The caller's own approved expenses per entry date, as net. It is their own
// for the reason time's hours are: a figure whose scope moved with the
// reader's permissions would mean a different thing to every reader of the
// same card. Only the installation's default currency is in it — two
// currencies never add up — and the period is checked before the metric, the
// order every other module's timeseries answers in.
func (s *server) GetExpensesStatsTimeseries(ctx context.Context, req gen.GetExpensesStatsTimeseriesRequestObject) (gen.GetExpensesStatsTimeseriesResponseObject, error) {
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetExpensesStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}
	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	if !strings.EqualFold(metric, metricNetAmount) {
		return gen.GetExpensesStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.Problem(
			"Invalid metric", "Metric must be one of: "+metricNetAmount+".")), nil
	}

	q := store.New(s.deps.Pool)
	settings, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	rows, err := q.StatsNetBuckets(ctx, store.StatsNetBucketsParams{
		UserID: callerID(ctx), Currency: settings.DefaultCurrency,
		RangeFrom: periodFrom, RangeTo: periodTo,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: timeseries: %w", err)
	}
	buckets := make([]gen.ExpensesStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		value, err := ratFromNumeric(r.Value)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, gen.ExpensesStatsDailyBucket{
			Date:  &openapi_types.Date{Time: r.Day.Time},
			Value: apicommon.Ptr(floatOfRat(value)),
		})
	}
	return gen.GetExpensesStatsTimeseries200JSONResponse(buckets), nil
}

// GetExpensesStatsAttention Get expenses dashboard attention items
// (GET /api/v1/expenses/stats/attention)
//
// First the caller's own expenses that were sent back, newest decision first —
// the thing only they can do something about. Then, for an approver, the
// people whose expenses are waiting for them, the person who has waited
// longest first. Last, for expenses:manage, a single item saying that a
// payroll run is due.
//
// The titles are names — a person's, an expense's — because the host
// translates the sentence from the type. Where a sentence needs a number, the
// number is its own field: a count written into a title on this side could
// never be translated on the other.
func (s *server) GetExpensesStatsAttention(ctx context.Context, _ gen.GetExpensesStatsAttentionRequestObject) (gen.GetExpensesStatsAttentionResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return nil, err
	}

	rejected, err := q.StatsMyRejected(ctx, store.StatsMyRejectedParams{
		UserID: c.UserID, RowLimit: rejectedAttentionLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's rejected expenses: %w", err)
	}
	rejectedClaims, err := q.StatsMyRejectedClaims(ctx, store.StatsMyRejectedClaimsParams{
		UserID: c.UserID, RowLimit: rejectedAttentionLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's rejected travel claims: %w", err)
	}
	var groups []store.StatsApprovalWaitingGroupsRow
	if scope.approvesAny() {
		if groups, err = q.StatsApprovalWaitingGroups(ctx, store.StatsApprovalWaitingGroupsParams{
			SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
			TimeZone: c.Settings.TimeZone,
		}); err != nil {
			return nil, fmt.Errorf("expenses: list the approvals waiting: %w", err)
		}
	}
	var payroll []store.StatsReimbursementsWaitingRow
	if c.Manage {
		if payroll, err = q.StatsReimbursementsWaiting(ctx); err != nil {
			return nil, fmt.Errorf("expenses: count what is waiting to be reimbursed: %w", err)
		}
	}

	// Every name the list needs, in one call, whatever the list holds.
	userIDs := make([]uuid.UUID, 0, len(groups))
	for _, g := range groups {
		userIDs = append(userIDs, g.UserID)
	}
	users, err := s.usersUsers(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("expenses: resolve the waiting owners: %w", err)
	}
	named := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		named[u.ID] = u.DisplayName
	}
	nameOf := func(id uuid.UUID) string {
		if name, ok := named[id]; ok {
			return name
		}
		return unknownUser
	}

	items := make(gen.GetExpensesStatsAttention200JSONResponse,
		0, len(rejected)+len(rejectedClaims)+len(groups)+1)
	// What was sent back is one list to the person reading it, whichever unit
	// each item is, so the cap is taken across both: they are gathered here,
	// merged newest decision first and cut to the cap before anything else is
	// added. A person with twenty rejected expenses and twenty rejected trips is
	// told about twenty things, not forty.
	sentBack := make([]gen.ExpensesStatsAttentionItem, 0, len(rejected)+len(rejectedClaims))
	for _, r := range rejected {
		if r.DecidedAt == nil {
			// StatsMyRejected's own predicate excludes these, so this cannot
			// fire; it is here so that removing that predicate one day is a
			// missing item rather than a 500 on everybody's dashboard.
			continue
		}
		id := strconv.FormatInt(r.ID, 10)
		sentBack = append(sentBack, gen.ExpensesStatsAttentionItem{
			Id:         id,
			Type:       attentionExpenseRejected,
			Title:      r.Description,
			OccurredAt: r.DecidedAt.UTC(),
			EntityId:   id,
		})
	}
	// A rejected trip is **one** item, titled by its purpose — never one per
	// line, which would bury the dashboard under a trip nobody can act on a
	// piece of. Its entity is written "claim/<id>" so the host can tell which
	// of the two units a numeric id belongs to and link to the right page; an
	// expense and a trip can share a number, and a bare one would be ambiguous.
	for _, r := range rejectedClaims {
		if r.DecidedAt == nil {
			continue
		}
		id := claimEntityID(r.ID)
		sentBack = append(sentBack, gen.ExpensesStatsAttentionItem{
			Id:         id,
			Type:       attentionExpenseRejected,
			Title:      r.Purpose,
			OccurredAt: r.DecidedAt.UTC(),
			EntityId:   id,
		})
	}
	// Stable, so two units decided in the same instant — which a batch rejection
	// makes of everything in it — keep the order their own query read them in:
	// the newer id first.
	slices.SortStableFunc(sentBack, func(a, b gen.ExpensesStatsAttentionItem) int {
		return b.OccurredAt.Compare(a.OccurredAt)
	})
	if len(sentBack) > rejectedAttentionLimit {
		sentBack = sentBack[:rejectedAttentionLimit]
	}
	items = append(items, sentBack...)
	slices.SortFunc(groups, func(a, b store.StatsApprovalWaitingGroupsRow) int {
		return cmp.Or(
			a.OldestSubmittedAt.Compare(b.OldestSubmittedAt),
			strings.Compare(nameOf(a.UserID), nameOf(b.UserID)),
		)
	})
	for _, g := range groups {
		items = append(items, gen.ExpensesStatsAttentionItem{
			Id:         g.UserID.String(),
			Type:       attentionApprovalWaiting,
			Title:      nameOf(g.UserID),
			OccurredAt: g.OldestSubmittedAt.UTC(),
			EntityId:   g.UserID.String(),
			Count:      apicommon.Ptr(int32(g.Waiting)),
		})
	}
	for _, p := range payroll {
		items = append(items, gen.ExpensesStatsAttentionItem{
			Id:   reimbursementsEntity,
			Type: attentionReimbursementWaiting,
			// The host writes this sentence from the type and the count; what
			// is here is the fallback for a client that does not know the
			// type, and it is deliberately not translated. It counts units —
			// standalone expenses and whole travel claims — so it says so:
			// "expenses" alone would be a lie about a figure that holds trips.
			Title:      fmt.Sprintf("%d expenses and travel claims are waiting to be reimbursed", p.Waiting),
			OccurredAt: p.OldestDecidedAt.UTC(),
			EntityId:   reimbursementsEntity,
			Count:      apicommon.Ptr(int32(p.Waiting)),
		})
	}
	return items, nil
}
