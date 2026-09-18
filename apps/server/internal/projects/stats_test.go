package projects_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

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
