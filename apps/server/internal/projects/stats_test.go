package projects_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The four stats operations (design §4.3). Every test here asserts over the
// projects one caller may see rather than over the table: the numbers share
// the list's visibility predicate (projects.visible, in the baseline
// migration), and a number computed over everything would be the one way
// this module leaks the existence of a project a caller may not open.
//
// Each harness has its own database (internal/modtest), so every count below
// is exact rather than a lower bound.

// statsJSON decodes GetProjectStatsResponse, the list page's key-figure row.
type statsJSON struct {
	Planned   int32 `json:"planned"`
	Active    int32 `json:"active"`
	OnHold    int32 `json:"onHold"`
	Completed int32 `json:"completed"`
	Cancelled int32 `json:"cancelled"`
}

// summaryJSON decodes ProjectStatsSummaryResponse, the dashboard's card.
type summaryJSON struct {
	From                time.Time `json:"from"`
	To                  time.Time `json:"to"`
	ActiveProjects      int32     `json:"activeProjects"`
	ActiveProjectsDelta int32     `json:"activeProjectsDelta"`
	NewProjects         int32     `json:"newProjects"`
	NewProjectsDelta    int32     `json:"newProjectsDelta"`
	ReadyMilestones     int32     `json:"readyMilestones"`
}

// bucketJSON decodes ProjectStatsDailyBucket, one UTC calendar day.
type bucketJSON struct {
	Date  string `json:"date"`
	Value int64  `json:"value"`
}

// attentionJSON decodes ProjectStatsAttentionItem, the dashboard's shared
// attention shape.
type attentionJSON struct {
	Id         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	OccurredAt time.Time `json:"occurredAt"`
	EntityId   string    `json:"entityId"`
}

// readStats reads the status counts and fails the test unless they answered
// 200.
func readStats(t *testing.T, c *modtest.Client) statsJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/projects/stats", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("stats: status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)
	return stats
}

// statsRange is the from/to query string for a dashboard period, in the
// RFC 3339 the contract declares.
func statsRange(from, to time.Time) string {
	return url.Values{
		"from": {from.Format(time.RFC3339)},
		"to":   {to.Format(time.RFC3339)},
	}.Encode()
}

// readSummary reads the dashboard summary over one period.
func readSummary(t *testing.T, c *modtest.Client, query string) summaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/projects/stats/summary?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("summary %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var summary summaryJSON
	r.JSON(&summary)
	return summary
}

// readAttention reads the attention items.
func readAttention(t *testing.T, c *modtest.Client) []attentionJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/projects/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("attention: status %d body %s, want 200", r.Status, r.Body)
	}
	var items []attentionJSON
	r.JSON(&items)
	return items
}

// Every status is counted, and every status is present in the answer: a KPI
// row whose "cancelled" tile disappeared when nothing was cancelled would
// reflow on data rather than on a filter.
func TestGetProjectsStats_CountsProjectsInEveryStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")

	createProject(t, creator, map[string]any{"code": "PLANNEDA"})
	createProject(t, creator, map[string]any{"code": "PLANNEDB"})
	for code, status := range map[string]string{
		"ACTIVEA": "active", "HOLDA": "on-hold", "DONEA": "completed", "CANCELA": "cancelled",
	} {
		p := createProject(t, creator, map[string]any{"code": code})
		setStatus(t, creator, p.Id, status)
	}

	stats := readStats(t, creator)
	want := statsJSON{Planned: 2, Active: 1, OnHold: 1, Completed: 1, Cancelled: 1}
	if stats != want {
		t.Errorf("stats = %+v, want %+v", stats, want)
	}
}

// D7 in the KPI row: a member's counts cover the projects they hold a role
// on and nothing else, while view-all counts everything. The two callers
// read the same database in the same test, so a predicate dropped from the
// query would make both answers identical.
func TestGetProjectsStats_MemberCountsOnlyTheirProjectsWhileViewAllCountsAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	mine := createProject(t, creator, map[string]any{"code": "MINESTAT"})
	setStatus(t, creator, mine.Id, "active")
	theirs := createProject(t, creator, map[string]any{"code": "THEIRSTAT"})
	setStatus(t, creator, theirs.Id, "active")

	member, memberID := signIn(t, h)
	addRole(t, h, mine.Id, memberID, "member")
	if stats := readStats(t, member); stats.Active != 1 {
		t.Errorf("member's Active = %d, want 1: the count covers only the project they hold a role on", stats.Active)
	}

	viewer, _ := signIn(t, h, "projects:view-all")
	if stats := readStats(t, viewer); stats.Active != 2 {
		t.Errorf("view-all's Active = %d, want 2", stats.Active)
	}
}

// The dashboard summary over a period: newProjects counts what was created
// inside it, activeProjects is a state now rather than a count over the
// period, and both deltas compare against the window immediately before.
// The second read narrows the period past the older project, which is what
// makes "created in range" more than "created at all".
func TestGetProjectsStatsSummary_CountsProjectsCreatedInThePeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{"code": "OLDONE"})

	// A fresh sign-in after the clock moves: a harness session's absolute
	// lifetime is shorter than the two days this test crosses.
	h.Advance(48 * time.Hour)
	later, _ := signIn(t, h, "projects:create")
	recent := createProject(t, later, map[string]any{"code": "NEWONE"})
	setStatus(t, later, recent.Id, "active")
	h.Advance(time.Second) // the period ends at now, and created_at must fall strictly before it.

	viewer, _ := signIn(t, h, "projects:view-all")
	whole := readSummary(t, viewer, statsRange(modtest.Start, h.Now()))
	if whole.NewProjects != 2 {
		t.Errorf("NewProjects = %d, want 2: both projects were created inside the period", whole.NewProjects)
	}
	if whole.NewProjectsDelta != 2 {
		t.Errorf("NewProjectsDelta = %d, want 2: nothing existed in the window before", whole.NewProjectsDelta)
	}
	if whole.ActiveProjects != 1 || whole.ActiveProjectsDelta != 1 {
		t.Errorf("ActiveProjects = %d delta %d, want 1 and 1", whole.ActiveProjects, whole.ActiveProjectsDelta)
	}
	if !whole.From.Equal(modtest.Start) || !whole.To.Equal(h.Now()) {
		t.Errorf("From = %v To = %v, want the period the request named", whole.From, whole.To)
	}

	narrow := readSummary(t, viewer, statsRange(modtest.Start.Add(24*time.Hour), h.Now()))
	if narrow.NewProjects != 1 {
		t.Errorf("narrowed NewProjects = %d, want 1: the older project is outside this period", narrow.NewProjects)
	}
	if narrow.ActiveProjects != 1 {
		t.Errorf("narrowed ActiveProjects = %d, want 1: an active project is active whatever the period", narrow.ActiveProjects)
	}
}

// A caller who holds no role and no global permission has no numbers, not
// the tenant's numbers: the summary runs the list's visibility predicate.
func TestGetProjectsStatsSummary_StrangerCountsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	p := createProject(t, creator, map[string]any{"code": "HIDDENA"})
	setStatus(t, creator, p.Id, "active")
	h.Advance(time.Second)

	stranger, _ := signIn(t, h)
	summary := readSummary(t, stranger, statsRange(modtest.Start, h.Now()))
	if summary.NewProjects != 0 || summary.ActiveProjects != 0 {
		t.Errorf("summary = %+v, want zeroes: the caller holds no role on the one project", summary)
	}
}

// The timeseries buckets by UTC calendar day: two projects created two days
// apart are two buckets, not one of two.
func TestGetProjectsStatsTimeseries_BucketsNewProjectsByDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	createProject(t, creator, map[string]any{"code": "DAYONE"})

	h.Advance(48 * time.Hour)
	later, _ := signIn(t, h, "projects:create")
	createProject(t, later, map[string]any{"code": "DAYTHREE"})
	h.Advance(time.Second)

	viewer, _ := signIn(t, h, "projects:view-all")
	r := viewer.Do(http.MethodGet, "/api/v1/projects/stats/timeseries?metric=newProjects&"+statsRange(modtest.Start, h.Now()), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []bucketJSON
	r.JSON(&buckets)

	want := []bucketJSON{
		{Date: modtest.Start.Format(time.DateOnly), Value: 1},
		{Date: modtest.Start.Add(48 * time.Hour).Format(time.DateOnly), Value: 1},
	}
	if fmt.Sprint(buckets) != fmt.Sprint(want) {
		t.Errorf("buckets = %+v, want %+v", buckets, want)
	}
}

// An unknown metric is a mistake worth naming, not an empty series: the
// dashboard asks for one metric per card and a silent [] would read as "no
// projects yet".
func TestGetProjectsStatsTimeseries_UnknownMetricIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	r := c.Do(http.MethodGet, "/api/v1/projects/stats/timeseries?metric=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid metric" || problem.Detail != "Metric must be: newProjects." {
		t.Errorf("problem = %+v, want title %q detail %q", problem, "Invalid metric", "Metric must be: newProjects.")
	}
}

// A period whose start is after its end is the one 400 the summary answers,
// the same one every module's dashboard endpoints answer.
func TestGetProjectsStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	r := c.Do(http.MethodGet, "/api/v1/projects/stats/summary?"+statsRange(modtest.Start, modtest.Start.Add(-time.Hour)), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid period")
	}
}

// Attention is "active and past its end date". A completed project past the
// same date is not attention — it finished late, which is history rather
// than something to act on — and an active project whose end date is still
// ahead is not attention either.
func TestGetProjectsStatsAttention_ListsOverdueActiveProjectsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	// The day after the harness clock starts, as the midnight UTC instant an
	// item's occurredAt is and as the date a create body carries.
	dueDay := modtest.Start.Add(24 * time.Hour).Truncate(24 * time.Hour)
	due := dueDay.Format(time.DateOnly)

	overdue := createProject(t, creator, map[string]any{"code": "OVERDUEA", "endDate": due})
	setStatus(t, creator, overdue.Id, "active")
	finished := createProject(t, creator, map[string]any{"code": "FINISHEDA", "endDate": due})
	setStatus(t, creator, finished.Id, "completed")
	ahead := createProject(t, creator, map[string]any{
		"code": "AHEADA", "endDate": modtest.Start.Add(30 * 24 * time.Hour).Format(time.DateOnly),
	})
	setStatus(t, creator, ahead.Id, "active")

	// Two days on, the first two projects' end date is behind the clock.
	h.Advance(48 * time.Hour)
	viewer, _ := signIn(t, h, "projects:view-all")
	items := readAttention(t, viewer)

	if len(items) != 1 {
		t.Fatalf("items = %+v, want exactly the one overdue active project", items)
	}
	want := attentionJSON{
		Id:         fmt.Sprint(overdue.Id),
		Type:       "projectOverdue",
		Title:      overdue.Name,
		OccurredAt: dueDay,
		EntityId:   fmt.Sprint(overdue.Id),
	}
	if items[0] != want {
		t.Errorf("item = %+v, want %+v", items[0], want)
	}
}

// Attention respects visibility too: an overdue project a caller may not
// open is not something the dashboard tells them about.
func TestGetProjectsStatsAttention_StrangerSeesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	overdue := createProject(t, creator, map[string]any{
		"code": "HIDDENOD", "endDate": modtest.Start.Add(24 * time.Hour).Format(time.DateOnly),
	})
	setStatus(t, creator, overdue.Id, "active")

	h.Advance(48 * time.Hour)
	stranger, _ := signIn(t, h)
	if items := readAttention(t, stranger); len(items) != 0 {
		t.Errorf("items = %+v, want none: the caller holds no role on the overdue project", items)
	}
}

// The economy signals on the dashboard (design §5, delivery B): the ready
// milestone count on the summary card, and the four attention types. Each
// one is addressed to somebody — a budget alert to the people running the
// project, an invoicing one to whoever may see the money — because an item
// nobody is expected to act on is noise in everybody's list.

// attentionOfType is the items of one type, which is what every assertion
// below compares: the list is merged from several sources and a test about
// one of them says so.
func attentionOfType(items []attentionJSON, want string) []attentionJSON {
	out := []attentionJSON{}
	for _, item := range items {
		if item.Type == want {
			out = append(out, item)
		}
	}
	return out
}

// attentionEntityIds is the entity ids of a set of items, in order.
func attentionEntityIds(items []attentionJSON) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.EntityId)
	}
	return out
}

// readyMilestones counts what is waiting to be invoiced on the projects whose
// money the caller may see — the same rule the portfolio lists rows by, not
// the wider "can see the project" the rest of this card counts over.
func TestGetProjectsStatsSummary_CountsReadyMilestonesTheCallerMaySee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	first := portfolioProject(t, creator, "SRMFIRST", nil)
	second := portfolioProject(t, creator, "SRMSECND", nil)
	for _, seed := range []struct {
		project projectJSON
		count   int
	}{{first, 2}, {second, 1}} {
		for i := 0; i < seed.count; i++ {
			movedMilestone(t, creator,
				createMilestone(t, creator, seed.project.Id, map[string]any{"name": fmt.Sprintf("M%d", i)}), "ready", nil)
		}
	}
	// One more that is not ready, so the count is a status and not a total.
	createMilestone(t, creator, first.Id, map[string]any{"name": "Senere"})
	h.Advance(time.Second)

	period := statsRange(modtest.Start, h.Now())
	if got := readSummary(t, creator, period).ReadyMilestones; got != 3 {
		t.Errorf("the manager's readyMilestones = %d, want 3", got)
	}

	member, memberID := signIn(t, h)
	addRole(t, h, first.Id, memberID, "member")
	if got := readSummary(t, member, period).ReadyMilestones; got != 0 {
		t.Errorf("the member's readyMilestones = %d, want 0: they may not see the project's money", got)
	}

	financials, _ := signIn(t, h, "projects:view-all", "projects:view-financials")
	if got := readSummary(t, financials, period).ReadyMilestones; got != 3 {
		t.Errorf("view-financials' readyMilestones = %d, want 3", got)
	}
}

// The two budget alerts, at their boundaries: nothing below 80 %, a warning
// from 80 % up to and including 100 % exactly, and exceeded past it — decided
// on the exact ratio, so a project at 100.001 % is exceeded although its
// percentage prints 100.0. One project never raises both.
func TestGetProjectsStatsAttention_BudgetAlertsAtTheirThresholds(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	billed := func(amount string) contracts.ActualsTotals {
		return loggedTotals(loggedBucket(1, amount, "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))
	}
	for _, seed := range []struct{ code, name, amount string }{
		{"SBUNDER1", "Godt innenfor", "799.99"},
		{"SBWARN11", "Nesten brukt opp", "800.00"},
		{"SBEDGE11", "Akkurat brukt opp", "1000.00"},
		{"SBOVER11", "Over budsjett", "1000.01"},
	} {
		project := portfolioProject(t, creator, seed.code, map[string]any{"name": seed.name})
		actuals.set(project.Id, billed(seed.amount))
	}

	items := readAttention(t, creator)
	warnings := attentionOfType(items, "budgetWarning")
	if len(warnings) != 2 {
		t.Fatalf("budgetWarning items = %+v, want the 80 %% and the 100 %% projects", warnings)
	}
	names := []string{warnings[0].Title, warnings[1].Title}
	slices.Sort(names)
	if fmt.Sprint(names) != "[Akkurat brukt opp Nesten brukt opp]" {
		t.Errorf("budgetWarning titles = %v, want the two projects' names", names)
	}
	exceeded := attentionOfType(items, "budgetExceeded")
	if len(exceeded) != 1 || exceeded[0].Title != "Over budsjett" {
		t.Errorf("budgetExceeded items = %+v, want only the project past 100 %%", exceeded)
	}
	for _, item := range append(warnings, exceeded...) {
		if item.EntityId == "" || strings.Contains(item.EntityId, "/") {
			t.Errorf("item %+v entityId = %q, want the bare project id", item, item.EntityId)
		}
	}
	ids := map[string]bool{}
	for _, item := range items {
		if ids[item.Id] {
			t.Errorf("two items share the id %q: the dashboard keys its list on it", item.Id)
		}
		ids[item.Id] = true
	}
}

// A budget alert is addressed to the people running the project, which is the
// manager *role*. projects:manage-all is not a substitute: an administrator
// who can manage every project would be sent every project's alerts and read
// none of them. occurredAt is the day work was last logged, which is what the
// dashboard prints as "3 days ago".
func TestGetProjectsStatsAttention_BudgetAlertsGoToTheProjectsManagers(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "SBWHO111", nil)
	onHold := portfolioProject(t, creator, "SBHOLD11", map[string]any{"status": "on-hold"})
	totals := loggedTotals(loggedBucket(10, "1500.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00"))
	lastDay := modtest.Start.Add(-48 * time.Hour).Format(time.DateOnly)
	totals.LastEntryDate = &lastDay
	actuals.set(project.Id, totals)
	actuals.set(onHold.Id, totals)

	items := attentionOfType(readAttention(t, creator), "budgetExceeded")
	if len(items) != 1 || items[0].EntityId != fmt.Sprint(project.Id) {
		t.Fatalf("the manager's budgetExceeded items = %+v, want only the active project", items)
	}
	wantDay, _ := time.Parse(time.DateOnly, lastDay)
	if !items[0].OccurredAt.Equal(wantDay) {
		t.Errorf("occurredAt = %v, want %v — midnight UTC on the day work was last logged", items[0].OccurredAt, wantDay)
	}

	admin, _ := signIn(t, h, "projects:manage-all")
	if got := attentionOfType(readAttention(t, admin), "budgetExceeded"); len(got) != 0 {
		t.Errorf("manage-all's budgetExceeded items = %+v, want none: an alert is addressed to a project's managers", got)
	}
	financials, financialsID := signIn(t, h, "projects:view-financials")
	addRole(t, h, project.Id, financialsID, "viewer")
	if got := attentionOfType(readAttention(t, financials), "budgetExceeded"); len(got) != 0 {
		t.Errorf("view-financials' budgetExceeded items = %+v, want none", got)
	}
}

// milestoneReady goes to everybody who may see the project's money, because
// whoever may see it is who invoices. Its entityId names the project as well
// as the milestone, which is how the dashboard builds a link to a milestone
// that is addressed by its own id.
func TestGetProjectsStatsAttention_MilestoneReadyGoesToFinancialRights(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	active := portfolioProject(t, creator, "SMRACTIV", nil)
	planned := portfolioProject(t, creator, "SMRPLAND", map[string]any{"status": ""})
	done := portfolioProject(t, creator, "SMRDONE1", nil)
	cancelled := portfolioProject(t, creator, "SMRCANCL", nil)
	ready := movedMilestone(t, creator,
		createMilestone(t, creator, active.Id, map[string]any{"name": "Klar til fakturering"}), "ready", nil)
	movedMilestone(t, creator, createMilestone(t, creator, planned.Id, nil), "ready", nil)
	movedMilestone(t, creator, createMilestone(t, creator, done.Id, nil), "ready", nil)
	movedMilestone(t, creator, createMilestone(t, creator, cancelled.Id, nil), "ready", nil)
	setStatus(t, creator, done.Id, "completed")
	setStatus(t, creator, cancelled.Id, "cancelled")

	items := attentionOfType(readAttention(t, creator), "milestoneReady")
	if len(items) != 2 {
		t.Fatalf("milestoneReady items = %+v, want the active and the planned project's, and neither finished one's", items)
	}
	var found bool
	for _, item := range items {
		if item.EntityId != fmt.Sprintf("%d/%d", active.Id, ready.Id) {
			continue
		}
		found = true
		if item.Title != "Klar til fakturering" {
			t.Errorf("title = %q, want the milestone's own name", item.Title)
		}
		if ready.ReadyAt == nil || !item.OccurredAt.Equal(*ready.ReadyAt) {
			t.Errorf("occurredAt = %v, want the moment it was marked ready (%v)", item.OccurredAt, ready.ReadyAt)
		}
	}
	if !found {
		t.Errorf("items = %+v, want one with entityId %d/%d", items, active.Id, ready.Id)
	}

	member, memberID := signIn(t, h)
	addRole(t, h, active.Id, memberID, "member")
	if got := attentionOfType(readAttention(t, member), "milestoneReady"); len(got) != 0 {
		t.Errorf("the member's milestoneReady items = %+v, want none: they may not see the money", got)
	}
	financials, financialsID := signIn(t, h, "projects:view-financials")
	addRole(t, h, active.Id, financialsID, "viewer")
	if got := attentionOfType(readAttention(t, financials), "milestoneReady"); len(got) != 1 {
		t.Errorf("view-financials' milestoneReady items = %+v, want the one on the project they can see", got)
	}
}

// milestoneOverdue is a milestone still *planned* whose day has passed, on an
// active project, for the people running it. A ready one past its date is
// already its own milestoneReady item, and telling the same person twice
// about the same milestone is noise.
func TestGetProjectsStatsAttention_MilestoneOverdueGoesToManagersOfActiveProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	active := portfolioProject(t, creator, "SMOACTIV", nil)
	held := portfolioProject(t, creator, "SMOHELD1", map[string]any{"status": "on-hold"})
	dueDay := modtest.Start.Add(24 * time.Hour).Truncate(24 * time.Hour)
	due := dueDay.Format(time.DateOnly)
	late := createMilestone(t, creator, active.Id, map[string]any{"name": "Forfalt", "plannedDate": due})
	movedMilestone(t, creator,
		createMilestone(t, creator, active.Id, map[string]any{"name": "Klar", "plannedDate": due}), "ready", nil)
	createMilestone(t, creator, active.Id, map[string]any{"name": "Senere", "plannedDate": modtest.Start.Add(30 * 24 * time.Hour).Format(time.DateOnly)})
	createMilestone(t, creator, held.Id, map[string]any{"name": "Pauset", "plannedDate": due})

	// Two days on, the milestone's day is behind the clock. A session does not
	// live that long, so the callers below are fresh ones given the roles the
	// rule is about.
	h.Advance(48 * time.Hour)
	reader, readerID := signIn(t, h, "projects:view-financials")
	addRole(t, h, active.Id, readerID, "viewer")
	if got := attentionOfType(readAttention(t, reader), "milestoneOverdue"); len(got) != 0 {
		t.Errorf("view-financials' milestoneOverdue items = %+v, want none: it is the managers who are asked to act", got)
	}

	manager, managerID := signIn(t, h)
	addRole(t, h, active.Id, managerID, "manager")
	addRole(t, h, held.Id, managerID, "manager")
	items := attentionOfType(readAttention(t, manager), "milestoneOverdue")
	if got := attentionEntityIds(items); fmt.Sprint(got) != fmt.Sprint([]string{fmt.Sprintf("%d/%d", active.Id, late.Id)}) {
		t.Fatalf("milestoneOverdue items = %+v, want only the planned, overdue milestone on the active project", items)
	}
	if items[0].Title != "Forfalt" || !items[0].OccurredAt.Equal(dueDay) {
		t.Errorf("item = %+v, want the milestone's name and its planned day at midnight UTC (%v)", items[0], dueDay)
	}
}

// Without a module reporting what has been logged there are no budget alerts
// at all — not alerts computed against zeroes — and everything this module
// owns by itself is still there.
func TestGetProjectsStatsAttention_WithoutTimeTrackingThereAreNoBudgetItems(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "SBNOTIME", nil)
	movedMilestone(t, creator, createMilestone(t, creator, project.Id, nil), "ready", nil)

	items := readAttention(t, creator)
	if got := attentionOfType(items, "budgetWarning"); len(got) != 0 {
		t.Errorf("budgetWarning items = %+v, want none", got)
	}
	if got := attentionOfType(items, "budgetExceeded"); len(got) != 0 {
		t.Errorf("budgetExceeded items = %+v, want none", got)
	}
	if got := attentionOfType(items, "milestoneReady"); len(got) != 1 {
		t.Errorf("milestoneReady items = %+v, want the one this module knows about on its own", got)
	}
}

// A provider that cannot answer costs the dashboard its budget alerts and
// nothing else. This is the one read in the feature that degrades: the
// dashboard merges many modules' items into one list, and one module that
// cannot compute one of its five kinds must not empty the list — unlike the
// portfolio and the per-project economy, where a budget compared against
// zeroes would be the whole answer.
func TestGetProjectsStatsAttention_AFailingProviderLeavesTheRestOfTheList(t *testing.T) {
	t.Parallel()
	actuals := newFakeActuals()
	h := newHarnessWithActuals(t, actuals)
	creator, _ := signIn(t, h, "projects:create")
	project := portfolioProject(t, creator, "SBBROKEN", nil)
	movedMilestone(t, creator, createMilestone(t, creator, project.Id, nil), "ready", nil)
	actuals.set(project.Id, loggedTotals(loggedBucket(10, "1500.00", "0.00"), loggedBucket(0, "0.00", "0.00"), loggedBucket(0, "0.00", "0.00")))

	if got := attentionOfType(readAttention(t, creator), "budgetExceeded"); len(got) != 1 {
		t.Fatalf("budgetExceeded items = %+v, want one while the provider answers", got)
	}

	actuals.fail(errors.New("time: the entries could not be read"))
	items := readAttention(t, creator)
	if got := attentionOfType(items, "budgetExceeded"); len(got) != 0 {
		t.Errorf("budgetExceeded items = %+v, want none once the provider is broken", got)
	}
	if got := attentionOfType(items, "milestoneReady"); len(got) != 1 {
		t.Errorf("milestoneReady items = %+v, want the rest of the list to survive a broken provider", got)
	}
}
