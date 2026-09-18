package timetracking_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// approvalPageJSON decodes PaginatedResponseOfTimeApprovalGroup.
type approvalPageJSON struct {
	Data []struct {
		UserId      uuid.UUID   `json:"userId"`
		DisplayName string      `json:"displayName"`
		WeekStart   string      `json:"weekStart"`
		Hours       float64     `json:"hours"`
		Entries     []entryJSON `json:"entries"`
	} `json:"data"`
	Pagination struct {
		Page        int32 `json:"page"`
		PageSize    int32 `json:"pageSize"`
		TotalCount  int32 `json:"totalCount"`
		TotalPages  int32 `json:"totalPages"`
		HasNextPage bool  `json:"hasNextPage"`
	} `json:"pagination"`
}

// getApprovals reads the caller's approval queue and fails the test unless it
// answered 200.
func getApprovals(t *testing.T, c *modtest.Client, query string) approvalPageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, approvalsPath+"?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("approvals ?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var page approvalPageJSON
	r.JSON(&page)
	return page
}

// group is one approval group as a test states it: whose, which week, how
// many hours and which entries.
type group struct {
	user    uuid.UUID
	week    string
	hours   float64
	entries []int64
}

func groupsOf(p approvalPageJSON) []group {
	out := []group{}
	for _, g := range p.Data {
		out = append(out, group{g.UserId, g.WeekStart, g.Hours, entryIDs(g.Entries...)})
	}
	return out
}

func equalGroups(a, b []group) bool {
	return slices.EqualFunc(a, b, func(x, y group) bool {
		return x.user == y.user && x.week == y.week && x.hours == y.hours && slices.Equal(x.entries, y.entries)
	})
}

// queueFixture is two people's time on 1001 and 1004 over two weeks: Anna
// and Bjørn are members of both, and every entry the queue is about is
// submitted; a draft and an approved entry show it leaves those out.
type queueFixture struct {
	anna, bjorn             uuid.UUID
	annaEarly, annaLate     entryJSON
	bjornKraft, bjornEuro   entryJSON
	annaDraft, annaApproved entryJSON
	annaClient, bjornClient *modtest.Client
	approver                *modtest.Client
}

func newQueueFixture(t *testing.T, h *harness) queueFixture {
	t.Helper()
	var f queueFixture
	f.annaClient, f.anna = signInAs(t, h, projectKraftVerket, roleMember)
	f.bjornClient, f.bjorn = signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, f.anna, roleMember)
	h.projects.addRole(projectEuro, f.bjorn, roleMember)
	setDisplayName(t, h, f.anna, "Anna")
	setDisplayName(t, h, f.bjorn, "Bjørn")
	f.approver, _ = signIn(t, h, "time:approve")

	f.bjornEuro = submittedEntry(t, f.bjornClient, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-09", "hours": 1.5})
	f.bjornKraft = submittedEntry(t, f.bjornClient, map[string]any{"entryDate": "2026-09-08", "hours": 2})
	f.annaEarly = submittedEntry(t, f.annaClient, map[string]any{"entryDate": "2026-09-08", "hours": 1})
	f.annaLate = submittedEntry(t, f.annaClient, map[string]any{"entryDate": "2026-09-14", "hours": 3})
	f.annaDraft = createEntry(t, f.annaClient, map[string]any{"entryDate": "2026-09-15", "hours": 2})
	f.annaApproved = submittedEntry(t, f.annaClient, map[string]any{"entryDate": "2026-09-16", "hours": 4})
	approveEntries(t, f.approver, f.annaApproved.Id)
	return f
}

func TestGetTimeApprovals_GroupsByPersonAndWeek_OldestWeekFirstThenByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	f := newQueueFixture(t, h)

	page := getApprovals(t, f.approver, "")
	want := []group{
		{f.anna, "2026-09-07", 1, []int64{f.annaEarly.Id}},
		{f.bjorn, "2026-09-07", 3.5, []int64{f.bjornKraft.Id, f.bjornEuro.Id}},
		{f.anna, "2026-09-14", 3, []int64{f.annaLate.Id}},
	}
	if got := groupsOf(page); !equalGroups(got, want) {
		t.Errorf("groups = %+v, want %+v", got, want)
	}
	if page.Data[0].DisplayName != "Anna" || page.Data[1].DisplayName != "Bjørn" {
		t.Errorf("names = %q, %q, want Anna, Bjørn", page.Data[0].DisplayName, page.Data[1].DisplayName)
	}
	for _, g := range page.Data {
		for _, e := range g.Entries {
			if !e.Capabilities.CanApprove {
				t.Errorf("entry %d: canApprove = false in the approver's queue", e.Id)
			}
		}
	}
	if page.Pagination.TotalCount != 3 || page.Pagination.Page != 1 || page.Pagination.PageSize != 25 {
		t.Errorf("pagination = %+v, want 3 groups on page 1 of size 25", page.Pagination)
	}

	first := getApprovals(t, f.approver, "pageSize=2")
	second := getApprovals(t, f.approver, "pageSize=2&page=2")
	if got := groupsOf(first); !equalGroups(got, want[:2]) || !first.Pagination.HasNextPage || first.Pagination.TotalPages != 2 {
		t.Errorf("page 1 = %+v %+v, want the first two groups of two pages", got, first.Pagination)
	}
	if got := groupsOf(second); !equalGroups(got, want[2:]) || second.Pagination.HasNextPage {
		t.Errorf("page 2 = %+v %+v, want the last group", got, second.Pagination)
	}

	// Approving an entry takes it out of the queue.
	approveEntries(t, f.approver, f.annaEarly.Id)
	if got := groupsOf(getApprovals(t, f.approver, "")); !equalGroups(got, want[1:]) {
		t.Errorf("after approving Anna's early entry: %+v, want %+v", got, want[1:])
	}
}

func TestGetTimeApprovals_AManagerSeesOnlyTheirProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	f := newQueueFixture(t, h)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	want := []group{
		{f.anna, "2026-09-07", 1, []int64{f.annaEarly.Id}},
		{f.bjorn, "2026-09-07", 2, []int64{f.bjornKraft.Id}},
		{f.anna, "2026-09-14", 3, []int64{f.annaLate.Id}},
	}
	if got := groupsOf(getApprovals(t, manager, "")); !equalGroups(got, want) {
		t.Errorf("manager of 1001: groups = %+v, want %+v", got, want)
	}

	if r := f.annaClient.Do(http.MethodGet, approvalsPath, nil); r.Status != http.StatusForbidden {
		t.Errorf("member: status %d, want 403", r.Status)
	}
	if r := manager.Do(http.MethodGet, approvalsPath+"?pageSize=0", nil); r.Status != http.StatusBadRequest {
		t.Errorf("pageSize=0: status %d, want 400", r.Status)
	}
}

func TestGetTimeApprovals_LeavesOutWhatTheLockHoldsBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	f := newQueueFixture(t, h)
	setLock(t, h, "2026-09-09")
	manage, _ := signIn(t, h, "time:approve", "time:manage")

	want := []group{
		{f.bjorn, "2026-09-07", 1.5, []int64{f.bjornEuro.Id}},
		{f.anna, "2026-09-14", 3, []int64{f.annaLate.Id}},
	}
	if got := groupsOf(getApprovals(t, f.approver, "")); !equalGroups(got, want) {
		t.Errorf("past the lock: groups = %+v, want %+v", got, want)
	}
	if got := getApprovals(t, manage, ""); got.Pagination.TotalCount != 3 {
		t.Errorf("time:manage: %d groups, want all 3", got.Pagination.TotalCount)
	}
}
