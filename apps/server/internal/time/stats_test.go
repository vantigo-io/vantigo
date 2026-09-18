package timetracking_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The dashboard's three stats operations and the project hours summary
// (design §4.2, §7). The dashboard reads every module through one generic
// URL and response shape (apps/host/frontend/src/routes/dashboard.tsx), so
// the first thing each dashboard test pins is the field names; the rest is
// scoping — a caller's hours are their own, an approver's count is the
// approval queue's — and the numbers.
//
// The harness clock starts on Saturday 12 September 2026 (modtest.Start), so
// the current week is the one of Monday 7 September and the previous one
// starts on 31 August.

const (
	statsSummaryPath    = "/api/v1/time/stats/summary"
	statsTimeseriesPath = "/api/v1/time/stats/timeseries"
	statsAttentionPath  = "/api/v1/time/stats/attention"
)

func projectSummaryPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/time/projects/%d/summary", projectID)
}

// statsSummaryJSON decodes TimeStatsSummaryResponse.
type statsSummaryJSON struct {
	From                    time.Time `json:"from"`
	To                      time.Time `json:"to"`
	HoursThisWeek           float64   `json:"hoursThisWeek"`
	HoursThisWeekDelta      float64   `json:"hoursThisWeekDelta"`
	AwaitingMyApproval      int32     `json:"awaitingMyApproval"`
	AwaitingMyApprovalDelta int32     `json:"awaitingMyApprovalDelta"`
}

// statsBucketJSON decodes TimeStatsDailyBucket.
type statsBucketJSON struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

// statsAttentionJSON decodes TimeStatsAttentionItem.
type statsAttentionJSON struct {
	Id         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	OccurredAt time.Time `json:"occurredAt"`
	EntityId   string    `json:"entityId"`
}

// problemDetailJSON decodes a plain ProblemDetails body.
type problemDetailJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// periodQuery is the from/to query string of a dashboard period, in the
// RFC 3339 the contract declares.
func periodQuery(from, to time.Time) string {
	return url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}.Encode()
}

// statsGet reads path and fails the test unless it answered 200, decoding
// into v; it answers the body as a bare object or array too, for the tests
// about which keys are there.
func statsGet(t *testing.T, c *modtest.Client, path string, v any) {
	t.Helper()
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s, want 200", path, r.Status, r.Body)
	}
	r.JSON(v)
}

func readStatsSummary(t *testing.T, c *modtest.Client, query string) statsSummaryJSON {
	t.Helper()
	var s statsSummaryJSON
	statsGet(t, c, statsSummaryPath+"?"+query, &s)
	return s
}

func readStatsSeries(t *testing.T, c *modtest.Client, metric, query string) []statsBucketJSON {
	t.Helper()
	var buckets []statsBucketJSON
	statsGet(t, c, statsTimeseriesPath+"?metric="+metric+"&"+query, &buckets)
	return buckets
}

func readStatsAttention(t *testing.T, c *modtest.Client) []statsAttentionJSON {
	t.Helper()
	var items []statsAttentionJSON
	statsGet(t, c, statsAttentionPath, &items)
	return items
}

// orZero is *p, or T's zero value for nil.
func orZero[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// keysOf is the sorted keys of a JSON object.
func keysOf(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

// The summary is the dashboard's envelope — from, to and a delta beside
// every figure, the names projects and customers answer with — and its
// hours are the caller's own in the current ISO week, rejected ones left
// out, with the delta against the week before.
func TestGetTimeStatsSummary_HoursThisWeekAreTheCallersOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, _ := signInAs(t, h, projectKraftVerket, roleMember)
	other, _ := signInAs(t, h, projectKraftVerket, roleMember)

	createEntry(t, me, map[string]any{"entryDate": "2026-09-07", "hours": 2})
	submittedEntry(t, me, map[string]any{"entryDate": "2026-09-08", "hours": 3})
	rejected := createEntry(t, me, map[string]any{"entryDate": "2026-09-09", "hours": 1.5})
	setStatus(t, h, rejected.Id, "rejected")
	createEntry(t, me, map[string]any{"entryDate": "2026-09-13", "hours": 0.25})
	createEntry(t, me, map[string]any{"entryDate": "2026-09-01", "hours": 4})
	createEntry(t, me, map[string]any{"entryDate": "2026-09-14", "hours": 5})
	createEntry(t, other, map[string]any{"entryDate": "2026-09-08", "hours": 6})

	var raw map[string]any
	statsGet(t, me, statsSummaryPath, &raw)
	wantKeys := []string{"awaitingMyApproval", "awaitingMyApprovalDelta", "from", "hoursThisWeek", "hoursThisWeekDelta", "to"}
	if got := keysOf(raw); !slices.Equal(got, wantKeys) {
		t.Errorf("summary keys = %v, want %v", got, wantKeys)
	}

	s := readStatsSummary(t, me, "")
	if s.HoursThisWeek != 5.25 {
		t.Errorf("HoursThisWeek = %v, want 5.25: 7–13 September, the caller's own, without the rejected entry", s.HoursThisWeek)
	}
	if s.HoursThisWeekDelta != 1.25 {
		t.Errorf("HoursThisWeekDelta = %v, want 1.25: 5.25 this week against 4 the week before", s.HoursThisWeekDelta)
	}
	if s.AwaitingMyApproval != 0 || s.AwaitingMyApprovalDelta != 0 {
		t.Errorf("awaiting = %d delta %d, want 0 and 0: a member approves nothing", s.AwaitingMyApproval, s.AwaitingMyApprovalDelta)
	}
	if !s.To.Equal(h.Now()) || !s.From.Equal(h.Now().AddDate(0, 0, -30)) {
		t.Errorf("period = %v – %v, want the default thirty days up to now", s.From, s.To)
	}
}

// awaitingMyApproval is the approval queue's scope counted in entries: a
// project's manager counts its submitted entries, time:approve counts
// everyone's, and a member counts none. Its delta compares against how many
// were waiting when the period began, which an approval inside the period
// lowers.
func TestGetTimeStatsSummary_AwaitingMyApprovalIsTheQueuesScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	anna, annaID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, annaID, roleMember)
	first := submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-07", "hours": 1})
	submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-08", "hours": 2})
	submittedEntry(t, anna, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-08", "hours": 3})
	createEntry(t, anna, map[string]any{"entryDate": "2026-09-09", "hours": 1})

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "time:approve")
	h.Advance(time.Hour)

	before := periodQuery(modtest.Start.Add(-24*time.Hour), h.Now())
	if s := readStatsSummary(t, manager, before); s.AwaitingMyApproval != 2 || s.AwaitingMyApprovalDelta != 2 {
		t.Errorf("manager: awaiting = %d delta %d, want 2 and 2: 1001's two, none of them waiting a day ago", s.AwaitingMyApproval, s.AwaitingMyApprovalDelta)
	}
	if s := readStatsSummary(t, approver, before); s.AwaitingMyApproval != 3 {
		t.Errorf("time:approve: awaiting = %d, want 3", s.AwaitingMyApproval)
	}
	if s := readStatsSummary(t, anna, before); s.AwaitingMyApproval != 0 {
		t.Errorf("member: awaiting = %d, want 0", s.AwaitingMyApproval)
	}

	since := periodQuery(modtest.Start.Add(30*time.Minute), h.Now())
	if s := readStatsSummary(t, approver, since); s.AwaitingMyApprovalDelta != 0 {
		t.Errorf("delta since after the submissions = %d, want 0: all three were already waiting", s.AwaitingMyApprovalDelta)
	}
	approveEntries(t, approver, first.Id)
	h.Advance(time.Minute)
	since = periodQuery(modtest.Start.Add(30*time.Minute), h.Now())
	if s := readStatsSummary(t, approver, since); s.AwaitingMyApproval != 2 || s.AwaitingMyApprovalDelta != -1 {
		t.Errorf("after an approval: awaiting = %d delta %d, want 2 and -1", s.AwaitingMyApproval, s.AwaitingMyApprovalDelta)
	}
}

func TestGetTimeStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	r := c.Do(http.MethodGet, statsSummaryPath+"?"+periodQuery(modtest.Start, modtest.Start.Add(-time.Hour)), nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemDetailJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q", problem.Title, "Invalid period")
	}
}

// The timeseries is the caller's own hours per entry date inside the
// period, days without any absent; billableHours counts billable entries
// only. Rejected hours and other people's hours are in neither.
func TestGetTimeStatsTimeseries_TheCallersHoursPerDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, meID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectInternal, meID, roleMember)
	other, _ := signInAs(t, h, projectKraftVerket, roleMember)

	createEntry(t, me, map[string]any{"entryDate": "2026-09-07", "hours": 2})
	createEntry(t, me, map[string]any{"projectId": projectInternal, "entryDate": "2026-09-07", "hours": 1})
	submittedEntry(t, me, map[string]any{"entryDate": "2026-09-09", "hours": 3})
	rejected := createEntry(t, me, map[string]any{"entryDate": "2026-09-10", "hours": 4})
	setStatus(t, h, rejected.Id, "rejected")
	createEntry(t, me, map[string]any{"entryDate": "2026-09-14", "hours": 5})
	createEntry(t, other, map[string]any{"entryDate": "2026-09-08", "hours": 6})

	period := periodQuery(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), h.Now())

	var raw []map[string]any
	statsGet(t, me, statsTimeseriesPath+"?metric=hours&"+period, &raw)
	if len(raw) == 0 || !slices.Equal(keysOf(raw[0]), []string{"date", "value"}) {
		t.Errorf("buckets = %v, want objects of date and value", raw)
	}

	hours := readStatsSeries(t, me, "hours", period)
	want := []statsBucketJSON{{"2026-09-07", 3}, {"2026-09-09", 3}}
	if !slices.Equal(hours, want) {
		t.Errorf("hours = %+v, want %+v", hours, want)
	}
	billable := readStatsSeries(t, me, "BillableHours", period)
	want = []statsBucketJSON{{"2026-09-07", 2}, {"2026-09-09", 3}}
	if !slices.Equal(billable, want) {
		t.Errorf("billableHours = %+v, want %+v (matched case-insensitively)", billable, want)
	}
}

func TestGetTimeStatsTimeseries_UnknownMetricIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	for _, query := range []string{"metric=bogus", ""} {
		r := c.Do(http.MethodGet, statsTimeseriesPath+"?"+query, nil)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("?%s: status %d body %s, want 400", query, r.Status, r.Body)
		}
		var problem problemDetailJSON
		r.JSON(&problem)
		want := problemDetailJSON{Title: "Invalid metric", Detail: "Metric must be one of: hours, billableHours."}
		if problem != want {
			t.Errorf("?%s: problem = %+v, want %+v", query, problem, want)
		}
	}

	r := c.Do(http.MethodGet, statsTimeseriesPath+"?metric=bogus&"+periodQuery(modtest.Start, modtest.Start.Add(-time.Hour)), nil)
	var problem problemDetailJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Invalid period" {
		t.Errorf("bad metric over a bad period: status %d title %q, want 400 %q first", r.Status, problem.Title, "Invalid period")
	}
}

// The caller's past weeks with drafts in them are attention, one item per
// week, oldest first; the current week is not (it is still being written),
// a week with nothing but submitted entries is not, other people's drafts
// are not, and neither is a week the lock has closed to the caller.
func TestGetTimeStatsAttention_TheCallersUnsubmittedPastWeeks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	me, _ := signInAs(t, h, projectKraftVerket, roleMember)
	other, _ := signInAs(t, h, projectKraftVerket, roleMember)

	createEntry(t, me, map[string]any{"entryDate": "2026-08-24", "hours": 1})
	createEntry(t, me, map[string]any{"entryDate": "2026-09-01", "hours": 2})
	createEntry(t, me, map[string]any{"entryDate": "2026-09-03", "hours": 1.5})
	submittedEntry(t, me, map[string]any{"entryDate": "2026-08-18", "hours": 2})
	createEntry(t, me, map[string]any{"entryDate": "2026-09-08", "hours": 2})
	createEntry(t, other, map[string]any{"entryDate": "2026-08-11", "hours": 2})

	var raw []map[string]any
	statsGet(t, me, statsAttentionPath, &raw)
	if len(raw) == 0 || !slices.Equal(keysOf(raw[0]), []string{"entityId", "id", "occurredAt", "title", "type"}) {
		t.Errorf("items = %v, want objects of exactly the dashboard's five keys", raw)
	}

	want := []statsAttentionJSON{
		{
			Id: "2026-08-24", Type: "weekUnsubmitted", EntityId: "2026-08-24",
			Title:      "Your week of 2026-08-24 is not submitted (1 h)",
			OccurredAt: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			Id: "2026-08-31", Type: "weekUnsubmitted", EntityId: "2026-08-31",
			Title:      "Your week of 2026-08-31 is not submitted (3.5 h)",
			OccurredAt: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		},
	}
	if items := readStatsAttention(t, me); !slices.Equal(items, want) {
		t.Errorf("items = %+v, want %+v", items, want)
	}

	setLock(t, h, "2026-08-31")
	if items := readStatsAttention(t, me); !slices.Equal(items, want[1:]) {
		t.Errorf("under the lock: items = %+v, want only the open week %+v", items, want[1:])
	}
}

// Submitted entries that have waited more than seven days are attention for
// whoever may approve them, one item per person and week: time:approve sees
// every project's, a manager only their project's, and a member none. An
// entry submitted since is not late yet.
func TestGetTimeStatsAttention_SubmissionsWaitingAWeekForTheirApprovers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	anna, annaID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, annaID, roleMember)
	setDisplayName(t, h, annaID, "Anna")
	submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-07", "hours": 1})
	submittedEntry(t, anna, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-08", "hours": 2})
	submittedEntry(t, anna, map[string]any{"entryDate": "2026-08-31", "hours": 3})

	h.Advance(8 * 24 * time.Hour)
	bjorn, bjornID := signInAs(t, h, projectKraftVerket, roleMember)
	setDisplayName(t, h, bjornID, "Bjørn")
	submittedEntry(t, bjorn, map[string]any{"entryDate": "2026-09-14", "hours": 2})

	approver, _ := signIn(t, h, "time:approve")
	euroManager, _ := signInAs(t, h, projectEuro, roleManager)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)

	annaWeek := func(week string, hours string) statsAttentionJSON {
		key := annaID.String() + "/" + week
		return statsAttentionJSON{
			Id: key, Type: "approvalWaiting", EntityId: key,
			Title:      "Anna's week of " + week + " is awaiting approval (" + hours + " h)",
			OccurredAt: modtest.Start,
		}
	}
	want := []statsAttentionJSON{annaWeek("2026-08-31", "3"), annaWeek("2026-09-07", "3")}
	if items := readStatsAttention(t, approver); !slices.Equal(items, want) {
		t.Errorf("time:approve: items = %+v, want %+v", items, want)
	}
	want = []statsAttentionJSON{annaWeek("2026-09-07", "2")}
	if items := readStatsAttention(t, euroManager); !slices.Equal(items, want) {
		t.Errorf("1004's manager: items = %+v, want %+v", items, want)
	}
	if items := readStatsAttention(t, member); len(items) != 0 {
		t.Errorf("member: items = %+v, want none", items)
	}
}

// projectSummaryJSON decodes TimeProjectSummaryResponse.
type projectSummaryJSON struct {
	ProjectId int32 `json:"projectId"`
	Hours     struct {
		Total    float64 `json:"total"`
		ByStatus struct {
			Draft     float64 `json:"draft"`
			Submitted float64 `json:"submitted"`
			Approved  float64 `json:"approved"`
			Rejected  float64 `json:"rejected"`
			Invoiced  float64 `json:"invoiced"`
		} `json:"byStatus"`
		ByLine []struct {
			BillingLineId   *int32  `json:"billingLineId"`
			BillingLineCode *string `json:"billingLineCode"`
			TrackableCode   *string `json:"trackableCode"`
			Hours           float64 `json:"hours"`
		} `json:"byLine"`
		ByPerson []struct {
			UserId      uuid.UUID `json:"userId"`
			DisplayName string    `json:"displayName"`
			Hours       float64   `json:"hours"`
		} `json:"byPerson"`
	} `json:"hours"`
	Billing *struct {
		Amount        float64 `json:"amount"`
		Currency      *string `json:"currency"`
		UnpricedHours float64 `json:"unpricedHours"`
	} `json:"billing"`
}

func readProjectSummary(t *testing.T, c *modtest.Client, projectID int32) (projectSummaryJSON, map[string]any) {
	t.Helper()
	var s projectSummaryJSON
	statsGet(t, c, projectSummaryPath(projectID), &s)
	var raw map[string]any
	statsGet(t, c, projectSummaryPath(projectID), &raw)
	return s, raw
}

// clearBillRate takes an entry's bill rate away, the state an entry is in
// when no step of the rate chain resolved one — which 1001, with its default
// rate, never leaves an entry in on its own.
func clearBillRate(t *testing.T, h *harness, id int64) {
	t.Helper()
	h.Exec(t, `UPDATE time.entries SET bill_rate = NULL, bill_currency = NULL, rate_source = 'none' WHERE id = $1`, id)
}

// summaryFixture is two people's time on 1001 in every status, on two lines
// and on none, with one billable submitted entry left unpriced and one of
// Anna's entries on another project the summary must leave out.
func summaryFixture(t *testing.T, h *harness) (annaID, bjornID uuid.UUID, anna *modtest.Client) {
	t.Helper()
	anna, annaID = signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, annaID, roleMember)
	bjorn, bjornID := signInAs(t, h, projectKraftVerket, roleMember)
	setDisplayName(t, h, annaID, "Anna")
	setDisplayName(t, h, bjornID, "Bjørn")
	approver, _ := signIn(t, h, "time:approve")

	// Billed: 2 h at 1001's default 900, 1.5 h and 3 h on the fixed 1500 line
	// — 8550 NOK; the 2 h whose rate is cleared are unpriced.
	submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-08", "hours": 2})
	approved := submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-09", "hours": 1.5, "billingLineId": lineFixed})
	approveEntries(t, approver, approved.Id)
	createEntry(t, anna, map[string]any{"entryDate": "2026-09-10", "hours": 1, "billingLineId": lineList})
	rejected := createEntry(t, anna, map[string]any{"entryDate": "2026-09-11", "hours": 0.5})
	setStatus(t, h, rejected.Id, "rejected")
	createEntry(t, anna, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-08", "hours": 7})

	invoiced := submittedEntry(t, bjorn, map[string]any{"entryDate": "2026-09-08", "hours": 3, "billingLineId": lineFixed})
	setStatus(t, h, invoiced.Id, "invoiced")
	submittedEntry(t, bjorn, map[string]any{"entryDate": "2026-09-09", "hours": 1, "billable": false})
	unpriced := submittedEntry(t, bjorn, map[string]any{"entryDate": "2026-09-10", "hours": 2})
	clearBillRate(t, h, unpriced.Id)
	return annaID, bjornID, anna
}

// The project summary's hours: every status apart, and the hours that stand
// — everything but rejected — per line (the line's codes beside it, the
// entries without a line last) and per person by name. A manager also gets
// the billing: submitted, approved and invoiced billable hours at their bill
// rates, in the project's currency, with the billable hours that have no
// rate counted beside it.
func TestGetTimeProjectSummary_HoursAndBillingForAManager(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	annaID, bjornID, _ := summaryFixture(t, h)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	s, raw := readProjectSummary(t, manager, projectKraftVerket)
	if got := keysOf(raw); !slices.Equal(got, []string{"billing", "hours", "projectId"}) {
		t.Errorf("keys = %v, want billing, hours and projectId", got)
	}
	if s.ProjectId != projectKraftVerket || s.Hours.Total != 10.5 {
		t.Errorf("project %d total %v, want %d and 10.5", s.ProjectId, s.Hours.Total, projectKraftVerket)
	}
	byStatus := s.Hours.ByStatus
	if byStatus.Draft != 1 || byStatus.Submitted != 5 || byStatus.Approved != 1.5 || byStatus.Rejected != 0.5 || byStatus.Invoiced != 3 {
		t.Errorf("byStatus = %+v, want draft 1, submitted 5, approved 1.5, rejected 0.5, invoiced 3", byStatus)
	}

	type line struct {
		id              *int32
		code, trackable string
		hours           float64
	}
	var lines []line
	for _, l := range s.Hours.ByLine {
		lines = append(lines, line{l.BillingLineId, orZero(l.BillingLineCode), orZero(l.TrackableCode), l.Hours})
	}
	wantLines := []line{
		{ptr(int32(lineList)), "DEV", "KVEM1000-DEV", 1},
		{ptr(int32(lineFixed)), "PM", "KVEM1000-PM", 4.5},
		{nil, "", "", 5},
	}
	if !slices.EqualFunc(lines, wantLines, func(a, b line) bool {
		return deref(a.id) == deref(b.id) && a.code == b.code && a.trackable == b.trackable && a.hours == b.hours
	}) {
		t.Errorf("byLine = %+v, want %+v", lines, wantLines)
	}

	type person struct {
		id    uuid.UUID
		name  string
		hours float64
	}
	var people []person
	for _, p := range s.Hours.ByPerson {
		people = append(people, person{p.UserId, p.DisplayName, p.Hours})
	}
	if want := []person{{annaID, "Anna", 4.5}, {bjornID, "Bjørn", 6}}; !slices.Equal(people, want) {
		t.Errorf("byPerson = %+v, want %+v", people, want)
	}

	if s.Billing == nil {
		t.Fatal("billing absent, want it for the project's manager")
	}
	if s.Billing.Amount != 8550 || orZero(s.Billing.Currency) != "NOK" || s.Billing.UnpricedHours != 2 {
		t.Errorf("billing = %v %s unpriced %v, want 8550 NOK with 2 unpriced hours",
			s.Billing.Amount, orZero(s.Billing.Currency), s.Billing.UnpricedHours)
	}
}

// D8 on the summary: a member sees the project's hours — everyone's, as an
// aggregate — but not its money; projects:view-financials on a summary the
// caller may read, or projects:manage-all, adds the billing; projects:view-all
// alone does not. The time permissions that see every entry — time:view-all,
// time:approve — read the hours without a role on the project, and never the
// money on their own.
func TestGetTimeProjectSummary_BillingOnlyForThoseWhoSeeTheFinancials(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, _, anna := summaryFixture(t, h)

	for _, tc := range []struct {
		name        string
		client      *modtest.Client
		wantBilling bool
	}{
		{"member", anna, false},
		{"projects:view-all", func() *modtest.Client { c, _ := signIn(t, h, "projects:view-all"); return c }(), false},
		{"projects:view-all+view-financials", func() *modtest.Client {
			c, _ := signIn(t, h, "projects:view-all", "projects:view-financials")
			return c
		}(), true},
		{"projects:manage-all", func() *modtest.Client { c, _ := signIn(t, h, "projects:manage-all"); return c }(), true},
		{"time:view-all", func() *modtest.Client { c, _ := signIn(t, h, "time:view-all"); return c }(), false},
		{"time:approve", func() *modtest.Client { c, _ := signIn(t, h, "time:approve"); return c }(), false},
		{"time:manage", func() *modtest.Client { c, _ := signIn(t, h, "time:manage"); return c }(), false},
		{"time:view-all+projects:view-financials", func() *modtest.Client {
			c, _ := signIn(t, h, "time:view-all", "projects:view-financials")
			return c
		}(), true},
	} {
		s, raw := readProjectSummary(t, tc.client, projectKraftVerket)
		if _, has := raw["billing"]; has != tc.wantBilling {
			t.Errorf("%s: billing present = %v, want %v", tc.name, has, tc.wantBilling)
		}
		if s.Hours.Total != 10.5 || len(s.Hours.ByPerson) != 2 {
			t.Errorf("%s: total %v over %d people, want 10.5 over 2", tc.name, s.Hours.Total, len(s.Hours.ByPerson))
		}
	}
}

// A caller with no path to the project — no role on it, no project-wide
// projects permission and no time permission that sees every entry — gets
// the bare 404 an unknown project gets, projects:view-financials alone
// included, and a project the directory does not know is 404 even to
// someone who sees every project and every entry.
func TestGetTimeProjectSummary_NotFoundForOutsidersAndUnknownProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	summaryFixture(t, h)
	outsider, _ := signIn(t, h)
	financialsOnly, _ := signIn(t, h, "projects:view-financials")
	otherManager, _ := signInAs(t, h, projectEuro, roleManager)
	seesEverything, _ := signIn(t, h, "projects:view-all", "time:view-all")

	for name, c := range map[string]*modtest.Client{"outsider": outsider, "projects:view-financials": financialsOnly, "another project's manager": otherManager} {
		if r := c.Do(http.MethodGet, projectSummaryPath(projectKraftVerket), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s: status %d body %q, want a bare 404", name, r.Status, r.Body)
		}
	}
	if r := seesEverything.Do(http.MethodGet, projectSummaryPath(projectUnknown), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown project: status %d body %q, want a bare 404", r.Status, r.Body)
	}
}

// A project with nothing logged answers zeroes and empty lists, not nulls:
// the panel renders the same shape from its first day.
func TestGetTimeProjectSummary_EmptyProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectEuro, roleManager)

	s, raw := readProjectSummary(t, manager, projectEuro)
	hours, _ := raw["hours"].(map[string]any)
	if hours["byLine"] == nil || hours["byPerson"] == nil {
		t.Errorf("hours = %v, want empty byLine and byPerson arrays", hours)
	}
	if s.Hours.Total != 0 || s.Billing == nil || s.Billing.Amount != 0 || orZero(s.Billing.Currency) != "EUR" {
		t.Errorf("summary = %+v, want zero hours and a zero EUR billing", s)
	}
}

// A project with no currency of its own bills in the one currency its priced
// entries share — person rates, in the card's currency — and hours on a line
// the project directory no longer lists keep the line's id without its codes,
// after the lines it does list.
func TestGetTimeProjectSummary_NoProjectCurrencyAndAVanishedLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, memberID := signInAs(t, h, projectNoCurrency, roleMember)
	seedRate(t, h, memberID, "2026-01-01", 1000.0, nil, "NOK")
	submittedEntry(t, member, map[string]any{"projectId": projectNoCurrency, "hours": 2})
	vanished := createEntry(t, member, map[string]any{"projectId": projectNoCurrency, "hours": 1})
	h.Exec(t, `UPDATE time.entries SET billing_line_id = 3999 WHERE id = $1`, vanished.Id)
	manager, _ := signInAs(t, h, projectNoCurrency, roleManager)

	s, _ := readProjectSummary(t, manager, projectNoCurrency)
	if s.Billing == nil || s.Billing.Amount != 2000 || orZero(s.Billing.Currency) != "NOK" || s.Billing.UnpricedHours != 0 {
		t.Errorf("billing = %+v, want 2000 NOK in the person rates' currency", s.Billing)
	}
	if len(s.Hours.ByLine) != 2 {
		t.Fatalf("byLine = %+v, want the vanished line, then no line", s.Hours.ByLine)
	}
	gone, none := s.Hours.ByLine[0], s.Hours.ByLine[1]
	if orZero(gone.BillingLineId) != 3999 || gone.BillingLineCode != nil || gone.TrackableCode != nil || gone.Hours != 1 {
		t.Errorf("vanished line = %+v, want id 3999, no codes, 1 h", gone)
	}
	if none.BillingLineId != nil || none.Hours != 2 {
		t.Errorf("no line = %+v, want 2 h", none)
	}
}
