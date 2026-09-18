package timetracking_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// pageIDs is the ids of a page's entries, in the order the page holds them.
func pageIDs(p entryPageJSON) []int64 { return entryIDs(p.Data...) }

func TestGetTimeEntries_WithoutUserId_ListsTheCallersOwnNewestFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember)

	first := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	second := createEntry(t, owner, map[string]any{"entryDate": "2026-09-16"})
	third := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14", "hours": 1})
	createEntry(t, colleague, nil)

	page := listEntries(t, owner, "")
	if want := []int64{second.Id, third.Id, first.Id}; !slices.Equal(pageIDs(page), want) {
		t.Errorf("ids = %v, want %v (date descending, then newest first)", pageIDs(page), want)
	}
	if page.Pagination.TotalCount != 3 || page.Pagination.Page != 1 || page.Pagination.PageSize != 25 {
		t.Errorf("pagination = %+v, want 3 in total on page 1 of 25", page.Pagination)
	}
	// Naming yourself is the same list.
	if got := listEntries(t, owner, "userId="+ownerID.String()); !slices.Equal(pageIDs(got), pageIDs(page)) {
		t.Errorf("userId=self ids = %v, want %v", pageIDs(got), pageIDs(page))
	}
	// Every row is rendered for the caller: their own, editable, priced.
	for _, e := range page.Data {
		if e.UserId != ownerID || !e.Capabilities.CanEdit || e.Billing == nil {
			t.Errorf("entry %d = %s/%+v/%v, want the owner's, editable, with billing", e.Id, e.UserId, e.Capabilities, e.Billing)
		}
	}
}

func TestGetTimeEntries_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, ownerID, roleMember)

	monday := createEntry(t, owner, map[string]any{"entryDate": "2026-09-14"})
	sunday := createEntry(t, owner, map[string]any{"entryDate": "2026-09-20", "projectId": projectEuro})
	nextMonday := createEntry(t, owner, map[string]any{"entryDate": "2026-09-21"})
	previousSunday := createEntry(t, owner, map[string]any{"entryDate": "2026-09-13"})
	submitEntries(t, owner, sunday.Id)

	for query, want := range map[string][]int64{
		"weekStart=2026-09-14":                                           {sunday.Id, monday.Id},
		fmt.Sprintf("projectId=%d", projectEuro):                         {sunday.Id},
		"status=draft":                                                   {nextMonday.Id, monday.Id, previousSunday.Id},
		"status=submitted&weekStart=2026-09-14":                          {sunday.Id},
		fmt.Sprintf("projectId=%d&status=submitted", projectKraftVerket): nil,
		"status=": {nextMonday.Id, sunday.Id, monday.Id, previousSunday.Id},
	} {
		if got := pageIDs(listEntries(t, owner, query)); !slices.Equal(got, want) {
			t.Errorf("?%s: ids = %v, want %v", query, got, want)
		}
	}
}

func TestGetTimeEntries_Paging(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	var created []int64
	for day := 14; day <= 18; day++ {
		created = append(created, createEntry(t, owner, map[string]any{"entryDate": fmt.Sprintf("2026-09-%d", day)}).Id)
	}
	slices.Reverse(created)

	page := listEntries(t, owner, "page=2&pageSize=2")
	if !slices.Equal(pageIDs(page), created[2:4]) {
		t.Errorf("page 2 ids = %v, want %v", pageIDs(page), created[2:4])
	}
	p := page.Pagination
	if p.TotalCount != 5 || p.TotalPages != 3 || !p.HasNextPage || !p.HasPreviousPage {
		t.Errorf("pagination = %+v, want 5 in 3 pages with both neighbours", p)
	}
}

func TestGetTimeEntries_InvalidQuery_IsABadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for _, query := range []string{"weekStart=2026-09-15", "status=pending", "page=0", "pageSize=101"} {
		r := owner.Do(http.MethodGet, entriesPath+"?"+query, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("?%s: %d %s, want 400", query, r.Status, r.Body)
			continue
		}
		var problem struct {
			Title string `json:"title"`
		}
		r.JSON(&problem)
		if problem.Title != "Invalid query parameters" {
			t.Errorf("?%s: title = %q, want Invalid query parameters", query, problem.Title)
		}
	}
}

// The list applies exactly the rule a single read applies (authorize.go):
// the owner, a manager of the entry's project, and time:view-all,
// time:approve and time:manage. Another person's entries are refused
// outright to a caller who could see none of them, and narrowed to the
// managed projects for a manager.
func TestGetTimeEntries_AnotherUser_FollowsTheReadRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, memberID := signInAs(t, h, projectKraftVerket, roleMember)
	h.projects.addRole(projectEuro, memberID, roleMember)
	colleague, _ := signInAs(t, h, projectKraftVerket, roleMember, "projects:view-financials", "projects:view-all")
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer)
	manager, managerID := signInAs(t, h, projectKraftVerket, roleManager)
	h.projects.addRole(projectEuro, managerID, roleMember)
	viewAll, _ := signIn(t, h, "time:view-all")
	approver, _ := signIn(t, h, "time:approve")
	timeManager, _ := signIn(t, h, "time:manage")

	onKraft := createEntry(t, member, nil)
	onKraftLater := createEntry(t, member, map[string]any{"entryDate": "2026-09-15"})
	onEuro := createEntry(t, member, map[string]any{"projectId": projectEuro, "entryDate": "2026-09-16"})
	createEntry(t, manager, nil)

	of := "userId=" + memberID.String()
	for name, c := range map[string]*modtest.Client{"colleague": colleague, "viewer": viewer} {
		if r := c.Do(http.MethodGet, entriesPath+"?"+of, nil); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
			t.Errorf("%s: %d %s, want the access layer's 403", name, r.Status, r.Body)
		}
	}

	// The manager of 1001 sees the member's entries on 1001, not on 1004,
	// and the count is the visible count, so paging never lies.
	page := listEntries(t, manager, of+"&pageSize=1")
	if !slices.Equal(pageIDs(page), []int64{onKraftLater.Id}) || page.Pagination.TotalCount != 2 {
		t.Errorf("manager: ids %v of %d, want [%d] of 2", pageIDs(page), page.Pagination.TotalCount, onKraftLater.Id)
	}
	if got := listEntries(t, manager, of+fmt.Sprintf("&projectId=%d", projectKraftVerket)); !slices.Equal(pageIDs(got), []int64{onKraftLater.Id, onKraft.Id}) {
		t.Errorf("manager on 1001: ids %v, want both 1001 entries", pageIDs(got))
	}
	if r := manager.Do(http.MethodGet, entriesPath+"?"+of+fmt.Sprintf("&projectId=%d", projectEuro), nil); r.Status != http.StatusForbidden {
		t.Errorf("manager narrowing to a project they only work on: %d, want 403", r.Status)
	}
	// Every entry a manager's list shows, a read shows too.
	for _, e := range page.Data {
		if r := manager.Do(http.MethodGet, entryPath(e.Id), nil); r.Status != http.StatusOK {
			t.Errorf("manager: entry %d is listed but its read answers %d", e.Id, r.Status)
		}
	}
	if r := manager.Do(http.MethodGet, entryPath(onEuro.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("manager: read of the unlisted 1004 entry = %d, want 404", r.Status)
	}

	all := []int64{onEuro.Id, onKraftLater.Id, onKraft.Id}
	for name, c := range map[string]*modtest.Client{"view-all": viewAll, "approver": approver, "time manager": timeManager} {
		if got := listEntries(t, c, of); !slices.Equal(pageIDs(got), all) {
			t.Errorf("%s: ids %v, want all of %v", name, pageIDs(got), all)
		}
	}

	// A user nobody logged time as is an empty list, not an error, for a
	// caller who may see anyone's.
	if got := listEntries(t, viewAll, "userId="+uuid.NewString()); len(got.Data) != 0 || got.Pagination.TotalCount != 0 {
		t.Errorf("view-all, unknown user: %d entries, want none", len(got.Data))
	}
}
