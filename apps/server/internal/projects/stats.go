package projects

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

// The five kinds of attention item this module raises. The dashboard keys
// its own per-module link building and its translated sentence on these
// strings, so they are part of the contract in everything but the schema.
//
// Each one is addressed to somebody, and the four economy ones differ in who
// (design §5): a budget alert and an overdue milestone go to the people
// running the project — its manager *role*, not projects:manage-all, because
// an administrator who can manage every project would be sent every project's
// alerts and read none of them — while something ready to invoice goes to
// everyone who may see the project's money, since whoever may see it is who
// invoices.
const (
	overdueType               = "projectOverdue"
	attentionBudgetWarning    = "budgetWarning"
	attentionBudgetExceeded   = "budgetExceeded"
	attentionMilestoneReady   = "milestoneReady"
	attentionMilestoneOverdue = "milestoneOverdue"
)

// budgetAttentionTypes maps the band a project's budget usage falls in
// (economy_math.go's one definition, decided on the exact ratio) to the item
// it raises. A band with no entry raises nothing, and no project is ever in
// two bands, which is what makes "never both a warning and an exceedance" a
// property of the arithmetic rather than a rule anybody has to remember.
var budgetAttentionTypes = map[string]string{
	budgetLevelWarning:  attentionBudgetWarning,
	budgetLevelExceeded: attentionBudgetExceeded,
}

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
	// readyMilestones counts over the narrower predicate the rest of this card
	// does not: what is waiting to be invoiced is a financial fact, so it is
	// the projects whose *money* the caller may see (design §5).
	f := s.financialVisibilityFor(ctx)
	ready, err := q.ReadyMilestoneCount(ctx, store.ReadyMilestoneCountParams{
		ManageAll: f.ManageAll, UserID: f.UserID, ViewFinancials: f.ViewFinancials, SeeAll: f.SeeAll,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: count the milestones ready to invoice: %w", err)
	}

	return gen.GetProjectsStatsSummary200JSONResponse{
		From:                periodFrom,
		To:                  periodTo,
		ActiveProjects:      int32(counts.Active),
		ActiveProjectsDelta: int32(counts.Active) - int32(counts.ActiveAtPeriodStart),
		NewProjects:         int32(counts.NewProjects),
		NewProjectsDelta:    int32(counts.NewProjects) - int32(counts.PreviousNewProjects),
		ReadyMilestones:     int32(ready),
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
// Five kinds of item, from four reads and — for the budget alerts alone — one
// call into the module that owns the hours. occurredAt is always the day the
// thing happened rather than the moment it was noticed: the dashboard sorts
// its merged list by it and prints it as "3 days ago", and the day a project
// fell behind is what that sentence should be about.
//
// The budget alerts are the one place in this feature where a failure
// degrades. Everywhere else a provider that cannot answer is a 500, because a
// budget compared against zeroes is a wrong answer rather than a missing one;
// here the answer is a list the dashboard merges with five other modules', and
// emptying it over one module's alerts would hide everything else somebody
// needs to act on. So the failure is logged and the other four kinds are
// returned.
//
// The list is oldest first across all five kinds, which is what the existing
// overdue items promised on their own.
func (s *server) GetProjectsStatsAttention(ctx context.Context, _ gen.GetProjectsStatsAttentionRequestObject) (gen.GetProjectsStatsAttentionResponseObject, error) {
	now := s.deps.Clock()
	// Truncate on a UTC instant is midnight UTC, which is the calendar day a
	// date column is compared against.
	today := pgtype.Date{Time: now.UTC().Truncate(24 * time.Hour), Valid: true}

	v := s.visibilityFor(ctx)
	f := s.financialVisibilityFor(ctx)
	q := store.New(s.deps.Pool)

	overdue, err := q.OverdueActiveProjects(ctx, store.OverdueActiveProjectsParams{
		UserID: v.UserID, SeeAll: v.SeeAll, Today: today,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: stats attention: %w", err)
	}
	ready, err := q.ReadyMilestonesForCaller(ctx, store.ReadyMilestonesForCallerParams{
		ManageAll: f.ManageAll, UserID: f.UserID, ViewFinancials: f.ViewFinancials, SeeAll: f.SeeAll,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: read the milestones ready to invoice: %w", err)
	}
	late, err := q.OverdueMilestonesForManager(ctx, store.OverdueMilestonesForManagerParams{
		UserID: f.UserID, Today: today,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: read the overdue milestones: %w", err)
	}
	budget, err := s.budgetAttention(ctx, q, f.UserID, now)
	if err != nil {
		return nil, err
	}

	items := make([]gen.ProjectStatsAttentionItem, 0, len(overdue)+len(ready)+len(late)+len(budget))
	for _, r := range overdue {
		// The one item whose id is the bare project id: it was that before
		// there was anything else to collide with, and nothing gains from
		// renaming it.
		id := strconv.FormatInt(int64(r.ID), 10)
		items = append(items, gen.ProjectStatsAttentionItem{
			Id: id, Type: overdueType, Title: r.Name, OccurredAt: r.EndDate.Time, EntityId: id,
		})
	}
	for _, m := range ready {
		items = append(items, attentionItem(
			attentionMilestoneReady, milestoneEntityID(m.ProjectID, m.ID), m.Name, m.OccurredAt.UTC()))
	}
	for _, m := range late {
		items = append(items, attentionItem(
			attentionMilestoneOverdue, milestoneEntityID(m.ProjectID, m.ID), m.Name, m.PlannedDate.Time))
	}
	items = append(items, budget...)
	slices.SortStableFunc(items, func(a, b gen.ProjectStatsAttentionItem) int {
		if c := a.OccurredAt.Compare(b.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(a.Id, b.Id)
	})
	return gen.GetProjectsStatsAttention200JSONResponse(items), nil
}

// attentionItem is one of the four economy items. Its id is the type and the
// entity joined, which keeps it unique when one project raises several kinds
// at once — the dashboard keys its rendered list on the module and the id, so
// two items sharing one would be one item on screen.
func attentionItem(kind, entityID, title string, occurredAt time.Time) gen.ProjectStatsAttentionItem {
	return gen.ProjectStatsAttentionItem{
		Id: kind + ":" + entityID, Type: kind, Title: title, OccurredAt: occurredAt, EntityId: entityID,
	}
}

// milestoneEntityID names a milestone the way the dashboard can build a link
// from it: its project and then itself. A milestone is addressed by its own id
// everywhere in this module's API, but the surface it is linked to — the
// project's Economy tab — lives under the project, so the pair is what an
// item has to carry.
func milestoneEntityID(projectID, milestoneID int32) string {
	return fmt.Sprintf("%d/%d", projectID, milestoneID)
}

// budgetAttention is the two budget alerts: the active projects the caller
// holds the manager role on, compared against what has been logged on them in
// one call for all of them at once.
//
// It answers nothing rather than failing when the provider cannot answer —
// see the handler's comment — and nothing at all for an installation without
// time tracking, where there is no logged work to compare a budget against.
// A manager always has financial rights on their own project, so the amount
// bases apply and the alert is about money whenever the project has a
// currency.
func (s *server) budgetAttention(ctx context.Context, q *store.Queries, userID uuid.UUID, now time.Time) ([]gen.ProjectStatsAttentionItem, error) {
	if s.deps.Actuals == nil {
		return nil, nil
	}
	// One row past the cap, so a truncation is noticed rather than assumed
	// away. The portfolio answers 400 in this situation because a partial
	// total is a wrong number; a dashboard has no filters to narrow and
	// nothing to total, so it keeps the capped set and says in the log that
	// the rest were not looked at.
	projects, err := q.ManagedActiveProjects(ctx, store.ManagedActiveProjectsParams{
		UserID: userID, RowLimit: portfolioMaxProjects + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: read the projects the caller manages: %w", err)
	}
	if len(projects) == 0 {
		return nil, nil
	}
	if len(projects) > portfolioMaxProjects {
		projects = projects[:portfolioMaxProjects]
		s.deps.Logger.WarnContext(ctx, "projects: the dashboard's budget alerts cover only the first projects the caller manages",
			"user_id", userID, "cap", portfolioMaxProjects)
	}
	logged, err := s.portfolioActuals(ctx, projects)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "projects: the dashboard's budget alerts were left out",
			"projects", len(projects), "error", err.Error())
		return nil, nil
	}

	items := make([]gen.ProjectStatsAttentionItem, 0, len(projects))
	for _, project := range projects {
		work, ok := logged[project.ID]
		if !ok {
			continue
		}
		basis, err := projectBudgetBasis(project, project.Currency != nil)
		if err != nil {
			return nil, err
		}
		use, ok := budgetUsed(basis, work.Bill)
		if !ok {
			continue
		}
		kind, ok := budgetAttentionTypes[budgetLevel(use)]
		if !ok {
			continue
		}
		items = append(items, attentionItem(kind,
			strconv.FormatInt(int64(project.ID), 10), project.Name, budgetOccurredAt(work, now)))
	}
	return items, nil
}

// budgetOccurredAt is the day a budget alert is about: the day work was last
// logged against the project, at midnight UTC, and the present moment for a
// project that has a budget and nothing logged — which cannot raise an alert
// today, but would read as 1 January year one if it ever did.
//
// It is clamped to now, which is a deliberate narrowing of "the day work was
// last logged". Nothing stops somebody logging work against a future date, and
// the dashboard both sorts on this field and prints it as "3 days ago": an
// alert dated next Friday would sort to the bottom of the merged list — the
// opposite of urgent — and read as "in 3 days" about something that is true
// today.
func budgetOccurredAt(w loggedWork, now time.Time) time.Time {
	if w.LastEntryDate != nil {
		if day, err := time.Parse(time.DateOnly, *w.LastEntryDate); err == nil && !day.After(now) {
			return day
		}
	}
	return now.UTC()
}
