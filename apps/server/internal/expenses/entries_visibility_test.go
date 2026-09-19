package expenses_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is design §5's visibility — who may see one expense, and which
// expenses a list holds — applied to a single read and to the list under the
// same rule, so the list never holds an entry its own read answers 404 for and
// never counts one it does not hold.

// The whole matrix on one entry: its owner, a manager of its project, the
// three global permissions, and everybody else.
func TestExpensesEntries_AreVisibleToTheirOwnerTheirManagerAndTheThreePermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	created := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	otherManager, _ := signInAs(t, h, projectEuro, roleManager)
	colleague, _ := signIn(t, h)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	viewAll, _ := signIn(t, h, "expenses:view-all")
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")
	projectsAdmin, _ := signIn(t, h, "projects:manage-all")

	for name, tc := range map[string]struct {
		client *modtest.Client
		sees   bool
	}{
		"its owner":                   {owner, true},
		"a manager of its project":    {manager, true},
		"expenses:view-all":           {viewAll, true},
		"expenses:approve":            {approver, true},
		"expenses:manage":             {admin, true},
		"a manager of another":        {otherManager, false},
		"another member of the same":  {member, false},
		"a colleague with no project": {colleague, false},
		"projects:manage-all alone":   {projectsAdmin, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := http.StatusOK
			if !tc.sees {
				want = http.StatusNotFound
			}
			if r := tc.client.Do(http.MethodGet, entryPath(created.Id), nil); r.Status != want {
				t.Errorf("read the entry: status %d body %s, want %d", r.Status, r.Body, want)
			}
		})
	}
}

// An entry nobody else may see reads exactly like an id nobody has: a bare
// 404, byte for byte.
func TestExpensesEntries_AnOutsiderGetsTheUnknownIdsAnswerByteForByte(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	created := createEntry(t, owner, outlayBody(nil))
	colleague, _ := signIn(t, h)

	hidden := colleague.Do(http.MethodGet, entryPath(created.Id), nil)
	unknown := colleague.Do(http.MethodGet, entryPath(999_999), nil)
	if hidden.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("statuses = %d and %d, want both 404", hidden.Status, unknown.Status)
	}
	if string(hidden.Body) != string(unknown.Body) {
		t.Errorf("bodies = %q and %q, want them identical", hidden.Body, unknown.Body)
	}
	// Changing and deleting it are the same 404, not a 403 that admits it is
	// there.
	if r := colleague.Do(http.MethodPut, entryPath(created.Id),
		outlayBody(map[string]any{"revision": created.Revision})); r.Status != http.StatusNotFound {
		t.Errorf("an outsider's update: status %d, want 404", r.Status)
	}
	if r := colleague.Do(http.MethodDelete, entryPath(created.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("an outsider's delete: status %d, want 404", r.Status)
	}
}

// Someone who may see an entry but not change it gets the access layer's own
// 403, not a 404 — a project's manager on somebody else's draft.
func TestExpensesEntries_AManagerSeesAnEntryButDoesNotOwnIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	created := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)

	got := getEntry(t, manager, created.Id)
	if got.Capabilities.CanEdit || got.Capabilities.CanDelete {
		t.Errorf("capabilities = %+v, want a manager not to own the draft", got.Capabilities)
	}
	if r := manager.Do(http.MethodPut, entryPath(created.Id),
		outlayBody(map[string]any{"revision": created.Revision, "projectId": projectKraftVerket})); r.Status != http.StatusForbidden {
		t.Errorf("a manager's update: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := manager.Do(http.MethodDelete, entryPath(created.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("a manager's delete: status %d body %s, want 403", r.Status, r.Body)
	}
}

// The list applies exactly the same rule, in SQL, and its total counts only
// what the caller may see.
func TestExpensesEntries_TheListHoldsAndCountsOnlyWhatTheCallerMaySee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	onProject := createEntry(t, owner, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	ownAlone := createEntry(t, owner, outlayBody(map[string]any{"entryDate": "2026-03-11"}))

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	managersOwn := createEntry(t, manager, outlayBody(map[string]any{"entryDate": "2026-03-12"}))
	colleague, _ := signIn(t, h)
	createEntry(t, colleague, outlayBody(map[string]any{"entryDate": "2026-03-13"}))

	// The owner: their own two, whoever manages the project.
	page := listEntries(t, owner, "")
	if got := entryIDs(page); len(got) != 2 || page.Pagination.TotalCount != 2 {
		t.Errorf("the owner's list = %v (total %d), want their own two", got, page.Pagination.TotalCount)
	}

	// The manager: their own, plus everything on the project they manage.
	page = listEntries(t, manager, "")
	got := entryIDs(page)
	if len(got) != 2 || page.Pagination.TotalCount != 2 {
		t.Fatalf("the manager's list = %v (total %d), want their own and the project's", got, page.Pagination.TotalCount)
	}
	if got[0] != managersOwn.Id || got[1] != onProject.Id {
		t.Errorf("the manager's list = %v, want the latest day first", got)
	}
	if contains(got, ownAlone.Id) {
		t.Error("the manager's list holds an entry on no project of theirs")
	}

	// expenses:view-all sees every one of the four.
	viewAll, _ := signIn(t, h, "expenses:view-all")
	page = listEntries(t, viewAll, "")
	if page.Pagination.TotalCount != 4 {
		t.Errorf("expenses:view-all total = %d, want every entry", page.Pagination.TotalCount)
	}

	// A colleague with nothing of their own sees nothing of anyone's.
	stranger, _ := signIn(t, h)
	page = listEntries(t, stranger, "")
	if len(page.Data) != 0 || page.Pagination.TotalCount != 0 {
		t.Errorf("a stranger's list = %+v, want it empty", page.Data)
	}
	// Naming somebody else does not widen it.
	page = listEntries(t, stranger, "?userId="+ownerID.String())
	if len(page.Data) != 0 {
		t.Errorf("a stranger filtering on the owner = %+v, want nothing", page.Data)
	}
}

func TestExpensesEntries_TheListFiltersAndPages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)

	outlay := createEntry(t, owner, outlayBody(map[string]any{"entryDate": "2026-03-01"}))
	onProject := createEntry(t, owner, outlayBody(map[string]any{
		"entryDate": "2026-03-05", "projectId": projectKraftVerket,
	}))
	mileage := createEntry(t, owner, mileageBody(map[string]any{"entryDate": "2026-03-09"}))

	for name, tc := range map[string]struct {
		query string
		want  []int64
	}{
		"by kind":      {"?kind=mileage", []int64{mileage.Id}},
		"by project":   {fmt.Sprintf("?projectId=%d", projectKraftVerket), []int64{onProject.Id}},
		"by status":    {"?status=draft", []int64{mileage.Id, onProject.Id, outlay.Id}},
		"from a date":  {"?from=2026-03-05", []int64{mileage.Id, onProject.Id}},
		"to a date":    {"?to=2026-03-05", []int64{onProject.Id, outlay.Id}},
		"between two":  {"?from=2026-03-02&to=2026-03-06", []int64{onProject.Id}},
		"by owner":     {"?userId=" + ownerID.String(), []int64{mileage.Id, onProject.Id, outlay.Id}},
		"none of them": {"?status=approved", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			page := listEntries(t, owner, tc.query)
			if got := entryIDs(page); !sameIDs(got, tc.want) {
				t.Errorf("%s = %v, want %v", tc.query, got, tc.want)
			}
			if page.Pagination.TotalCount != int32(len(tc.want)) {
				t.Errorf("%s total = %d, want %d", tc.query, page.Pagination.TotalCount, len(tc.want))
			}
		})
	}

	t.Run("paged", func(t *testing.T) {
		t.Parallel()
		first := listEntries(t, owner, "?page=1&pageSize=2")
		if got := entryIDs(first); !sameIDs(got, []int64{mileage.Id, onProject.Id}) {
			t.Errorf("page 1 = %v, want the two newest", got)
		}
		if first.Pagination.TotalCount != 3 || first.Pagination.TotalPages != 2 {
			t.Errorf("pagination = %+v, want 3 over two pages", first.Pagination)
		}
		second := listEntries(t, owner, "?page=2&pageSize=2")
		if got := entryIDs(second); !sameIDs(got, []int64{outlay.Id}) {
			t.Errorf("page 2 = %v, want the last one", got)
		}
	})

	t.Run("a query it cannot use", func(t *testing.T) {
		t.Parallel()
		for _, query := range []string{"?page=0", "?pageSize=0", "?pageSize=101", "?status=nonsense", "?kind=parking"} {
			r := owner.Do(http.MethodGet, entriesPath+query, nil)
			if r.Status != http.StatusBadRequest {
				t.Errorf("%s: status %d body %s, want 400", query, r.Status, r.Body)
			}
			var problem struct {
				Title string `json:"title"`
			}
			r.JSON(&problem)
			if problem.Title != invalidQueryTitle {
				t.Errorf("%s: title = %q, want %q", query, problem.Title, invalidQueryTitle)
			}
		}
	})
}

func contains(ids []int64, id int64) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
