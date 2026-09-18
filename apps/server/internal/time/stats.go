package timetracking

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the three stats operations the host dashboard reads out of
// every module through one generic URL shape
// (apps/host/frontend/src/routes/dashboard.tsx) — so they copy
// projects/stats.go, which copies customers': the same from/to parameters,
// the same period normalization, the same daily-bucket and attention-item
// shapes and the same "invalid period before invalid metric" order — and the
// project time summary the project page's time panel reads.
//
// Every figure is over what the caller may see, in SQL (queries/stats.sql):
// their own hours, or the approval queue's scope (approvalScope). None of it
// runs in a transaction, so the directory reads the scope needs are plain
// calls.

// The timeseries metrics, matched case-insensitively as every other module
// matches its own.
const (
	metricHours         = "hours"
	metricBillableHours = "billableHours"
)

// The attention items' types. The dashboard keys its per-module link building
// on these strings, so they are part of the contract in everything but the
// schema: a weekUnsubmitted item's entityId is the week's Monday
// (YYYY-MM-DD), an approvalWaiting item's is the approval queue's group key
// (approvalGroupKey: '<user id>/<Monday>').
const (
	attentionWeekUnsubmitted = "weekUnsubmitted"
	attentionApprovalWaiting = "approvalWaiting"
)

// approvalWaitingAfter is how long a submitted entry waits before its
// approvers are told about it (§4.2: more than seven days).
const approvalWaitingAfter = daysInWeek * 24 * time.Hour

// approvalScope is which submitted entries a caller may approve, in the
// terms ListApprovalGroups and the stats queries filter on: every project's
// (seeAll, time:approve) or the projects they manage (managed), and not
// those dated before lock (invalid for no lock, or for time:manage, whom the
// lock does not hold back).
type approvalScope struct {
	seeAll  bool
	managed []int32
	lock    pgtype.Date
}

// approvesAny reports whether the scope holds any project at all.
func (a approvalScope) approvesAny() bool { return a.seeAll || len(a.managed) > 0 }

// approvalScopeFor reads c's approval scope; for a caller without
// time:approve that is a directory call per project they hold a role on
// (managedProjects), so it must not run inside a locked transaction.
func (s *server) approvalScopeFor(ctx context.Context, c *caller) (approvalScope, error) {
	scope := approvalScope{seeAll: c.Approve, managed: []int32{}}
	if !c.Approve {
		managed, err := c.managedProjects(ctx, s)
		if err != nil {
			return approvalScope{}, err
		}
		scope.managed = managed
	}
	if c.LockedBefore != nil && !c.Manage {
		scope.lock = pgDate(*c.LockedBefore)
	}
	return scope, nil
}

// GetTimeStatsSummary Get time dashboard summary
// (GET /api/v1/time/stats/summary)
//
// Both figures are a state now, as projects' activeProjects is: the period
// is echoed and only moves awaitingMyApprovalDelta, which compares against
// how many of the same entries were already waiting when it began.
// hoursThisWeek's delta is against the week before.
func (s *server) GetTimeStatsSummary(ctx context.Context, req gen.GetTimeStatsSummaryRequestObject) (gen.GetTimeStatsSummaryResponseObject, error) {
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetTimeStatsSummary400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	week := mondayOf(s.today())
	hours, err := q.StatsWeekHours(ctx, store.StatsWeekHoursParams{
		UserID:        c.UserID,
		WeekStart:     pgDate(week),
		PreviousStart: pgDate(week.AddDate(0, 0, -daysInWeek)),
		WeekEnd:       pgDate(weekEnd(week)),
	})
	if err != nil {
		return nil, fmt.Errorf("time: sum the caller's weeks: %w", err)
	}
	thisWeek, err := centsFromNumeric(hours.ThisWeek)
	if err != nil {
		return nil, err
	}
	previousWeek, err := centsFromNumeric(hours.PreviousWeek)
	if err != nil {
		return nil, err
	}

	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return nil, err
	}
	awaiting, err := q.StatsAwaitingApproval(ctx, store.StatsAwaitingApprovalParams{
		PeriodFrom: periodFrom, SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
	})
	if err != nil {
		return nil, fmt.Errorf("time: count the entries awaiting approval: %w", err)
	}

	return gen.GetTimeStatsSummary200JSONResponse{
		From:                    periodFrom,
		To:                      periodTo,
		HoursThisWeek:           float64(thisWeek) / 100,
		HoursThisWeekDelta:      float64(thisWeek-previousWeek) / 100,
		AwaitingMyApproval:      int32(awaiting.Awaiting),
		AwaitingMyApprovalDelta: int32(awaiting.Awaiting - awaiting.AwaitingAtPeriodStart),
	}, nil
}

// GetTimeStatsTimeseries Get time dashboard time series
// (GET /api/v1/time/stats/timeseries)
//
// The caller's own hours per entry date. The period is checked before the
// metric, the order every other module's timeseries answers in.
func (s *server) GetTimeStatsTimeseries(ctx context.Context, req gen.GetTimeStatsTimeseriesRequestObject) (gen.GetTimeStatsTimeseriesResponseObject, error) {
	periodFrom, periodTo, _, ok := apicommon.NormalizePeriod(req.Params.From, req.Params.To, s.deps.Clock())
	if !ok {
		return gen.GetTimeStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.InvalidPeriod()), nil
	}

	metric := ""
	if req.Params.Metric != nil {
		metric = *req.Params.Metric
	}
	var billableOnly bool
	switch {
	case strings.EqualFold(metric, metricHours):
	case strings.EqualFold(metric, metricBillableHours):
		billableOnly = true
	default:
		return gen.GetTimeStatsTimeseries400ApplicationProblemPlusJSONResponse(apicommon.Problem(
			"Invalid metric", "Metric must be one of: "+metricHours+", "+metricBillableHours+".")), nil
	}

	rows, err := store.New(s.deps.Pool).StatsHourBuckets(ctx, store.StatsHourBucketsParams{
		UserID: callerID(ctx), BillableOnly: billableOnly, RangeFrom: periodFrom, RangeTo: periodTo,
	})
	if err != nil {
		return nil, fmt.Errorf("time: timeseries: %w", err)
	}
	buckets := make([]gen.TimeStatsDailyBucket, 0, len(rows))
	for _, r := range rows {
		cents, err := centsFromNumeric(r.Value)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, gen.TimeStatsDailyBucket{
			Date:  &openapi_types.Date{Time: r.Day.Time},
			Value: apicommon.Ptr(float64(cents) / 100),
		})
	}
	return gen.GetTimeStatsTimeseries200JSONResponse(buckets), nil
}

// GetTimeStatsAttention Get time dashboard attention items
// (GET /api/v1/time/stats/attention)
//
// First the caller's own past weeks with drafts in them, oldest first — the
// current week is still being written, and a draft the lock has closed to
// its owner is nothing they can act on. occurredAt is the Monday after the
// week, when it ended. Then, for an approver, the approval queue's groups
// whose entries have waited more than seven days, oldest week first and then
// by name, as the queue orders them; occurredAt is the oldest submission.
func (s *server) GetTimeStatsAttention(ctx context.Context, _ gen.GetTimeStatsAttentionRequestObject) (gen.GetTimeStatsAttentionResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return nil, err
	}

	weeks, err := q.StatsUnsubmittedWeeks(ctx, store.StatsUnsubmittedWeeksParams{
		UserID: c.UserID, CurrentWeek: pgDate(mondayOf(s.today())), LockedBefore: scope.lock,
	})
	if err != nil {
		return nil, fmt.Errorf("time: list the caller's unsubmitted weeks: %w", err)
	}
	items := make(gen.GetTimeStatsAttention200JSONResponse, 0, len(weeks))
	for _, w := range weeks {
		cents, err := centsFromNumeric(w.Hours)
		if err != nil {
			return nil, err
		}
		week := w.WeekStart.Time.Format(time.DateOnly)
		items = append(items, gen.TimeStatsAttentionItem{
			Id:         week,
			Type:       attentionWeekUnsubmitted,
			Title:      fmt.Sprintf("Your week of %s is not submitted (%s h)", week, formatCents(cents)),
			OccurredAt: w.WeekStart.Time.AddDate(0, 0, daysInWeek),
			EntityId:   week,
		})
	}

	if !scope.approvesAny() {
		return items, nil
	}
	groups, err := q.StatsWaitingApprovals(ctx, store.StatsWaitingApprovalsParams{
		SubmittedBefore:   s.deps.Clock().Add(-approvalWaitingAfter),
		SeeAll:            scope.seeAll,
		ManagedProjectIds: scope.managed,
		LockedBefore:      scope.lock,
	})
	if err != nil {
		return nil, fmt.Errorf("time: list the approvals waiting: %w", err)
	}
	userIDs := make([]uuid.UUID, 0, len(groups))
	for _, g := range groups {
		if !slices.Contains(userIDs, g.UserID) {
			userIDs = append(userIDs, g.UserID)
		}
	}
	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(groups, func(a, b store.StatsWaitingApprovalsRow) int {
		return cmp.Or(a.WeekStart.Time.Compare(b.WeekStart.Time), compareNames(users[a.UserID], users[b.UserID]))
	})
	for _, g := range groups {
		cents, err := centsFromNumeric(g.Hours)
		if err != nil {
			return nil, err
		}
		key := approvalGroupKey(g.UserID, g.WeekStart.Time)
		items = append(items, gen.TimeStatsAttentionItem{
			Id:   key,
			Type: attentionApprovalWaiting,
			Title: fmt.Sprintf("%s's week of %s is awaiting approval (%s h)",
				users[g.UserID].DisplayName, g.WeekStart.Time.Format(time.DateOnly), formatCents(cents)),
			OccurredAt: g.OldestSubmittedAt.UTC(),
			EntityId:   key,
		})
	}
	return items, nil
}

// GetTimeProjectsByProjectIdSummary Get a project's time summary
// (GET /api/v1/time/projects/{projectId}/summary)
//
// For any caller who sees the project (seesProject); anyone else, and a
// project the directory does not know, gets the bare 404. The hours are an
// aggregate — everyone's, per person — which a member sees although they see
// only their own entries: it exposes no entry. The billing is D8's, for
// callers who may see the project's financials (seesProjectFinancials).
func (s *server) GetTimeProjectsByProjectIdSummary(ctx context.Context, req gen.GetTimeProjectsByProjectIdSummaryRequestObject) (gen.GetTimeProjectsByProjectIdSummaryResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	role, err := c.role(ctx, s, req.ProjectId)
	if err != nil {
		return nil, err
	}
	if !c.seesProject(role) {
		return gen.GetTimeProjectsByProjectIdSummary404Response{}, nil
	}
	project, err := s.deps.Projects.Project(ctx, req.ProjectId)
	if err != nil {
		return nil, fmt.Errorf("time: read the project: %w", err)
	}
	if project == nil {
		return gen.GetTimeProjectsByProjectIdSummary404Response{}, nil
	}

	groups, err := q.ProjectHourGroups(ctx, req.ProjectId)
	if err != nil {
		return nil, fmt.Errorf("time: sum the project's hours: %w", err)
	}
	var total, noLine int64
	byStatus := map[string]int64{}
	byLine := map[int32]int64{}
	byPerson := map[uuid.UUID]int64{}
	var lineIDs []int32
	var userIDs []uuid.UUID
	for _, g := range groups {
		cents, err := centsFromNumeric(g.Hours)
		if err != nil {
			return nil, err
		}
		byStatus[g.Status] += cents
		if g.Status == statusRejected {
			continue
		}
		total += cents
		if g.BillingLineID == nil {
			noLine += cents
		} else {
			if _, ok := byLine[*g.BillingLineID]; !ok {
				lineIDs = append(lineIDs, *g.BillingLineID)
			}
			byLine[*g.BillingLineID] += cents
		}
		if _, ok := byPerson[g.UserID]; !ok {
			userIDs = append(userIDs, g.UserID)
		}
		byPerson[g.UserID] += cents
	}

	lines, err := s.lineHours(ctx, *project, lineIDs, byLine, noLine)
	if err != nil {
		return nil, err
	}
	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(userIDs, func(a, b uuid.UUID) int { return compareNames(users[a], users[b]) })
	people := make([]gen.TimeProjectPersonHours, 0, len(userIDs))
	for _, id := range userIDs {
		people = append(people, gen.TimeProjectPersonHours{
			UserId: id, DisplayName: users[id].DisplayName, Hours: float64(byPerson[id]) / 100,
		})
	}

	resp := gen.GetTimeProjectsByProjectIdSummary200JSONResponse{
		ProjectId: req.ProjectId,
		Hours: gen.TimeProjectHours{
			Total: float64(total) / 100,
			ByStatus: gen.TimeProjectHoursByStatus{
				Draft:     float64(byStatus[statusDraft]) / 100,
				Submitted: float64(byStatus[statusSubmitted]) / 100,
				Approved:  float64(byStatus[statusApproved]) / 100,
				Rejected:  float64(byStatus[statusRejected]) / 100,
				Invoiced:  float64(byStatus[statusInvoiced]) / 100,
			},
			ByLine:   lines,
			ByPerson: people,
		},
	}
	if c.seesProjectFinancials(role) {
		billing, err := projectBilling(ctx, q, req.ProjectId, project.Currency)
		if err != nil {
			return nil, err
		}
		resp.Billing = &billing
	}
	return resp, nil
}

// lineHours is the summary's per-line hours: the lines the project directory
// lists, in its code order, with their codes; a line it no longer lists by id
// after them, without codes; and the hours on no line last.
func (s *server) lineHours(ctx context.Context, project contracts.ProjectEntry, lineIDs []int32, byLine map[int32]int64, noLine int64) ([]gen.TimeProjectLineHours, error) {
	out := make([]gen.TimeProjectLineHours, 0, len(lineIDs)+1)
	if len(lineIDs) > 0 {
		known, err := s.deps.Projects.BillingLines(ctx, project.ID)
		if err != nil {
			return nil, fmt.Errorf("time: read the project's billing lines: %w", err)
		}
		codes := make(map[int32]string, len(known))
		for _, l := range known {
			codes[l.ID] = l.Code
		}
		slices.SortFunc(lineIDs, func(a, b int32) int {
			codeA, knownA := codes[a]
			codeB, knownB := codes[b]
			switch {
			case knownA && knownB:
				return cmp.Or(cmp.Compare(codeA, codeB), cmp.Compare(a, b))
			case knownA != knownB:
				if knownA {
					return -1
				}
				return 1
			}
			return cmp.Compare(a, b)
		})
		for _, id := range lineIDs {
			line := gen.TimeProjectLineHours{BillingLineId: apicommon.Ptr(id), Hours: float64(byLine[id]) / 100}
			if code, ok := codes[id]; ok {
				line.BillingLineCode = apicommon.Ptr(code)
				line.TrackableCode = apicommon.Ptr(trackableCode(project.Code, code))
			}
			out = append(out, line)
		}
	}
	if noLine > 0 {
		out = append(out, gen.TimeProjectLineHours{Hours: float64(noLine) / 100})
	}
	return out, nil
}

// projectBilling is what a project's submitted, approved and invoiced
// billable hours are worth at their snapshotted bill rates. Rates resolve in
// the project's currency (D3), so that is the currency; a project with none
// takes the one currency its priced entries share, if they share one. Hours
// with no rate, and — should any exist — hours priced in another currency,
// are unpriced: summing two currencies would be a number in neither.
func projectBilling(ctx context.Context, q *store.Queries, projectID int32, projectCurrency *string) (gen.TimeProjectBilling, error) {
	rows, err := q.ProjectBillingTotals(ctx, projectID)
	if err != nil {
		return gen.TimeProjectBilling{}, fmt.Errorf("time: sum the project's billing: %w", err)
	}
	currency := projectCurrency
	if currency == nil {
		var priced []string
		for _, r := range rows {
			if r.BillCurrency != nil && !slices.Contains(priced, *r.BillCurrency) {
				priced = append(priced, *r.BillCurrency)
			}
		}
		if len(priced) == 1 {
			currency = &priced[0]
		}
	}
	var amount, unpriced int64
	for _, r := range rows {
		rowAmount, err := centsFromNumeric(r.Amount)
		if err != nil {
			return gen.TimeProjectBilling{}, err
		}
		pricedHours, err := centsFromNumeric(r.PricedHours)
		if err != nil {
			return gen.TimeProjectBilling{}, err
		}
		unpricedHours, err := centsFromNumeric(r.UnpricedHours)
		if err != nil {
			return gen.TimeProjectBilling{}, err
		}
		unpriced += unpricedHours
		if currency != nil && r.BillCurrency != nil && *r.BillCurrency == *currency {
			amount += rowAmount
		} else {
			unpriced += pricedHours
		}
	}
	return gen.TimeProjectBilling{Amount: float64(amount) / 100, Currency: currency, UnpricedHours: float64(unpriced) / 100}, nil
}
