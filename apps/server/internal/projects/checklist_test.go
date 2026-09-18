package projects_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A task's checklist (design §3.1, §4.1, §7): the small list of things a task
// is made of, ordered inside the task the way tasks are ordered inside their
// project. Two things run through every case here — who may write (see the
// project ⇒ read, member or manager ⇒ write, outsiders 404) and that the
// positions are always 1..n — so most tests read the list back rather than
// trusting the writer's own answer, and the ones that change something read
// the task tree back too: the tree's checklist progress is what a reader of a
// project actually sees.

// checklistPath is one task's checklist; checklistItemPath is one item of it,
// which is always addressed under the task it belongs to — an item id alone
// would name a task the caller never asked about.
func checklistPath(taskID int32) string {
	return fmt.Sprintf("/api/v1/projects/tasks/%d/checklist", taskID)
}

func checklistItemPath(taskID int32, itemID int64) string {
	return fmt.Sprintf("%s/%d", checklistPath(taskID), itemID)
}

// checklistItemJSON decodes ChecklistItemResponse.
type checklistItemJSON struct {
	Id       int64  `json:"id"`
	Text     string `json:"text"`
	Done     bool   `json:"done"`
	Position int32  `json:"position"`
}

// readChecklist returns the raw list response, for a case whose subject is the
// refusal; getChecklist is it for a test that expects to be shown the list.
func readChecklist(t *testing.T, c *modtest.Client, taskID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, checklistPath(taskID), nil)
}

func getChecklist(t *testing.T, c *modtest.Client, taskID int32) []checklistItemJSON {
	t.Helper()
	r := readChecklist(t, c, taskID)
	if r.Status != http.StatusOK {
		t.Fatalf("list checklist: status %d body %s, want 200", r.Status, r.Body)
	}
	var items []checklistItemJSON
	r.JSON(&items)
	return items
}

// postChecklistItem sends one add and returns the response; addChecklistItem
// is it for a test that expects the item to be created.
func postChecklistItem(t *testing.T, c *modtest.Client, taskID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, checklistPath(taskID), body)
}

func addChecklistItem(t *testing.T, c *modtest.Client, taskID int32, text string) checklistItemJSON {
	t.Helper()
	r := postChecklistItem(t, c, taskID, map[string]any{"text": text})
	if r.Status != http.StatusCreated {
		t.Fatalf("add checklist item: status %d body %s, want 201", r.Status, r.Body)
	}
	var item checklistItemJSON
	r.JSON(&item)
	return item
}

// tickChecklistItem adds one item already ticked off, which takes two calls
// because an item is always added open: a case whose subject is the task's
// progress needs done items without saying so three times.
func tickChecklistItem(t *testing.T, c *modtest.Client, taskID int32, text string) checklistItemJSON {
	t.Helper()
	item := addChecklistItem(t, c, taskID, text)
	return changeChecklistItem(t, c, taskID, item.Id, map[string]any{"done": true})
}

// putChecklistItem sends one change and returns the response;
// changeChecklistItem is it for a test that expects the change to be applied.
func putChecklistItem(t *testing.T, c *modtest.Client, taskID int32, itemID int64, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, checklistItemPath(taskID, itemID), body)
}

func changeChecklistItem(t *testing.T, c *modtest.Client, taskID int32, itemID int64, body map[string]any) checklistItemJSON {
	t.Helper()
	r := putChecklistItem(t, c, taskID, itemID, body)
	if r.Status != http.StatusOK {
		t.Fatalf("change checklist item: status %d body %s, want 200", r.Status, r.Body)
	}
	var item checklistItemJSON
	r.JSON(&item)
	return item
}

func deleteChecklistItem(t *testing.T, c *modtest.Client, taskID int32, itemID int64) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, checklistItemPath(taskID, itemID), nil)
}

// checklistTexts and checklistPositions are what a list assertion is actually
// about: which items came back and in what order, and the numbers they carry.
func checklistTexts(items []checklistItemJSON) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Text)
	}
	return out
}

func checklistPositions(items []checklistItemJSON) []int32 {
	out := make([]int32, 0, len(items))
	for _, item := range items {
		out = append(out, item.Position)
	}
	return out
}

// taskInTree is the one task of a project's tree a counts assertion is about,
// read the way the frontend reads it.
func taskInTree(t *testing.T, c *modtest.Client, projectID, taskID int32) taskJSON {
	t.Helper()
	for _, task := range getTasks(t, c, projectID) {
		if task.Id == taskID {
			return task
		}
		for _, sub := range task.Subtasks {
			if sub.Id == taskID {
				return sub
			}
		}
	}
	t.Fatalf("task %d is not in the project's tree", taskID)
	return taskJSON{}
}

// An add appends: the first item of a task is position 1 and every further one
// takes the next number. The task's own progress moves with the list, so a
// reader of the project's tree sees the checklist grow without reading it.
func TestPostProjectsTasksByTaskIdChecklist_AppendsAndCountsOnTheTask(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CLADD100"})
	task := createTask(t, c, project.Id, nil)

	first := addChecklistItem(t, c, task.Id, "Avklar omfang")
	addChecklistItem(t, c, task.Id, "Avklar budsjett")
	// The text is trimmed before it is stored, exactly as a task's title is.
	third := addChecklistItem(t, c, task.Id, "  Avklar frist  ")

	if first.Position != 1 || third.Position != 3 || third.Text != "Avklar frist" {
		t.Errorf("first = %+v, third = %+v, want positions 1 and 3 and a trimmed text", first, third)
	}
	if first.Done {
		t.Error("a new item is done, want it open")
	}
	items := getChecklist(t, c, task.Id)
	if got := checklistTexts(items); !equalStrings(got, []string{"Avklar omfang", "Avklar budsjett", "Avklar frist"}) {
		t.Errorf("checklist = %v, want the three items in the order they were added", got)
	}
	if got := checklistPositions(items); !equalInt32s(got, []int32{1, 2, 3}) {
		t.Errorf("positions = %v, want 1..3", got)
	}
	if got := taskInTree(t, c, project.Id, task.Id); got.Checklist != (checklistJSON{Total: 3, Done: 0}) {
		t.Errorf("the task's checklist = %+v, want 3 total and none done", got.Checklist)
	}
}

// One change covers all three things a checklist item can be: its text, its
// done flag and where it sits. Ticking one off moves the task's progress;
// moving one renumbers the rest, so the list never has a gap.
func TestPutProjectsTasksByTaskIdChecklistByItemId_TogglesRenamesAndMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CLUPD100"})
	task := createTask(t, c, project.Id, nil)
	first := addChecklistItem(t, c, task.Id, "Avklar omfang")
	second := addChecklistItem(t, c, task.Id, "Avklar budsjett")
	third := addChecklistItem(t, c, task.Id, "Avklar frist")

	// Ticking one off says nothing about the others, and nothing about its own
	// text or its place: a field the body leaves out is left as it stands.
	ticked := changeChecklistItem(t, c, task.Id, second.Id, map[string]any{"done": true})
	if !ticked.Done || ticked.Text != "Avklar budsjett" || ticked.Position != 2 {
		t.Errorf("ticked = %+v, want it done, still named and still second", ticked)
	}
	if got := taskInTree(t, c, project.Id, task.Id); got.Checklist != (checklistJSON{Total: 3, Done: 1}) {
		t.Errorf("the task's checklist = %+v, want one of three done", got.Checklist)
	}

	renamed := changeChecklistItem(t, c, task.Id, first.Id, map[string]any{"text": "  Avklar hele omfanget  "})
	if renamed.Text != "Avklar hele omfanget" || renamed.Done {
		t.Errorf("renamed = %+v, want the trimmed text and still open", renamed)
	}

	// Moving the last item to the top renumbers the whole list 1..n.
	moved := changeChecklistItem(t, c, task.Id, third.Id, map[string]any{"position": 1})
	if moved.Position != 1 {
		t.Errorf("moved = %+v, want position 1", moved)
	}
	items := getChecklist(t, c, task.Id)
	if got := checklistTexts(items); !equalStrings(got, []string{"Avklar frist", "Avklar hele omfanget", "Avklar budsjett"}) {
		t.Errorf("checklist = %v, want the moved item first", got)
	}
	if got := checklistPositions(items); !equalInt32s(got, []int32{1, 2, 3}) {
		t.Errorf("positions = %v, want 1..3 with no gap", got)
	}
	// A position past the end is the end, the way dragging an item to the
	// bottom of a list sends whatever number the list happened to have.
	if moved := changeChecklistItem(t, c, task.Id, third.Id, map[string]any{"position": 99}); moved.Position != 3 {
		t.Errorf("moved to 99 = %+v, want the last position", moved)
	}
	if got := checklistTexts(getChecklist(t, c, task.Id)); got[2] != "Avklar frist" {
		t.Errorf("checklist = %v, want the moved item last", got)
	}
}

// A delete closes the gap it leaves: the items after it move up, so the list
// stays 1..n and the task's progress drops by one.
func TestDeleteProjectsTasksByTaskIdChecklistByItemId_RenumbersTheRest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CLDEL100"})
	task := createTask(t, c, project.Id, nil)
	addChecklistItem(t, c, task.Id, "Avklar omfang")
	second := addChecklistItem(t, c, task.Id, "Avklar budsjett")
	addChecklistItem(t, c, task.Id, "Avklar frist")
	changeChecklistItem(t, c, task.Id, second.Id, map[string]any{"done": true})

	if r := deleteChecklistItem(t, c, task.Id, second.Id); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	items := getChecklist(t, c, task.Id)
	if got := checklistTexts(items); !equalStrings(got, []string{"Avklar omfang", "Avklar frist"}) {
		t.Errorf("checklist = %v, want the two survivors", got)
	}
	if got := checklistPositions(items); !equalInt32s(got, []int32{1, 2}) {
		t.Errorf("positions = %v, want 1..2 with no gap", got)
	}
	if got := taskInTree(t, c, project.Id, task.Id); got.Checklist != (checklistJSON{Total: 2, Done: 0}) {
		t.Errorf("the task's checklist = %+v, want two open items", got.Checklist)
	}
	// Two deletes racing must not both answer 204.
	if r := deleteChecklistItem(t, c, task.Id, second.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d body %s, want 404", r.Status, r.Body)
	}
}

// The text rule: non-blank, at most 500 characters, on the add and on the
// change alike. A position below one is the move's own refusal.
func TestChecklist_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CLVAL100"})
	task := createTask(t, c, project.Id, nil)
	item := addChecklistItem(t, c, task.Id, "Avklar omfang")

	cases := []struct {
		name  string
		field string
		r     *modtest.Response
	}{
		{"empty text", "text", postChecklistItem(t, c, task.Id, map[string]any{"text": ""})},
		{"blank text", "text", postChecklistItem(t, c, task.Id, map[string]any{"text": "   "})},
		{"long text", "text", postChecklistItem(t, c, task.Id, map[string]any{"text": longText(501)})},
		{"blank rename", "text", putChecklistItem(t, c, task.Id, item.Id, map[string]any{"text": "  "})},
		{"long rename", "text", putChecklistItem(t, c, task.Id, item.Id, map[string]any{"text": longText(501)})},
		{"position zero", "position", putChecklistItem(t, c, task.Id, item.Id, map[string]any{"position": 0})},
	}
	for _, tc := range cases {
		if tc.r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, tc.r.Status, tc.r.Body)
			continue
		}
		var problem validationProblemJSON
		tc.r.JSON(&problem)
		if len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one under %q", tc.name, problem.Errors, tc.field)
		}
	}
	// A text of exactly 500 characters is allowed: the rule is the column's.
	if r := postChecklistItem(t, c, task.Id, map[string]any{"text": longText(500)}); r.Status != http.StatusCreated {
		t.Errorf("500 characters: status %d body %s, want 201", r.Status, r.Body)
	}
}

// An item is addressed under the task it belongs to, and an item of another
// task is not found there — the same bare 404 an unknown item gets, so a
// caller cannot probe which ids exist by trying them on a task they can see.
func TestChecklist_ItemOfAnotherTask_IsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "CLOTH100"})
	mine := createTask(t, c, project.Id, map[string]any{"title": "Min oppgave"})
	other := createTask(t, c, project.Id, map[string]any{"title": "Annen oppgave"})
	item := addChecklistItem(t, c, other.Id, "Hører til den andre")

	elsewhere := putChecklistItem(t, c, mine.Id, item.Id, map[string]any{"done": true})
	unknown := putChecklistItem(t, c, mine.Id, 999_999, map[string]any{"done": true})
	if elsewhere.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("elsewhere %d, unknown %d, want 404 for both", elsewhere.Status, unknown.Status)
	}
	if string(elsewhere.Body) != string(unknown.Body) {
		t.Errorf("another task's item answered %q, an unknown one %q", elsewhere.Body, unknown.Body)
	}
	if r := deleteChecklistItem(t, c, mine.Id, item.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleting another task's item: status %d body %s, want 404", r.Status, r.Body)
	}
	if got := getChecklist(t, c, other.Id); len(got) != 1 || got[0].Done {
		t.Errorf("the other task's checklist = %+v, want it untouched", got)
	}
	// A checklist on a task nobody has is the task's own 404.
	if r := readChecklist(t, c, 999_999); r.Status != http.StatusNotFound {
		t.Errorf("unknown task's checklist: status %d body %s, want 404", r.Status, r.Body)
	}
}

// Design §7's authorization: seeing the project is enough to read a task's
// checklist, changing it takes the member or manager role, and an outsider is
// told the task does not exist — in exactly the words an unknown id is told
// that.
func TestChecklist_Authorization_ViewerReadsMemberWritesOutsiderIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "CLAUT100"})
	task := createTask(t, manager, project.Id, nil)
	item := addChecklistItem(t, manager, task.Id, "Avklar omfang")

	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	outsider, _ := signIn(t, h)

	if got := getChecklist(t, viewer, task.Id); len(got) != 1 {
		t.Errorf("viewer's checklist = %+v, want the task's one item", got)
	}
	for name, r := range map[string]*modtest.Response{
		"add":    postChecklistItem(t, viewer, task.Id, map[string]any{"text": "Nei"}),
		"change": putChecklistItem(t, viewer, task.Id, item.Id, map[string]any{"done": true}),
		"delete": deleteChecklistItem(t, viewer, task.Id, item.Id),
	} {
		if r.Status != http.StatusForbidden {
			t.Errorf("viewer's %s: status %d body %s, want 403", name, r.Status, r.Body)
		}
	}

	added := addChecklistItem(t, member, task.Id, "Medlemmets punkt")
	if got := changeChecklistItem(t, member, task.Id, added.Id, map[string]any{"done": true}); !got.Done {
		t.Errorf("member's change = %+v, want it applied", got)
	}
	if r := deleteChecklistItem(t, member, task.Id, added.Id); r.Status != http.StatusNoContent {
		t.Errorf("member's delete: status %d body %s, want 204", r.Status, r.Body)
	}

	existing := readChecklist(t, outsider, task.Id)
	unknown := readChecklist(t, outsider, 999_999)
	if existing.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("outsider: existing %d, unknown %d, want 404 for both", existing.Status, unknown.Status)
	}
	if string(existing.Body) != string(unknown.Body) {
		t.Errorf("outsider's 404 body %q differs from an unknown task's %q", existing.Body, unknown.Body)
	}
	for name, r := range map[string]*modtest.Response{
		"add":    postChecklistItem(t, outsider, task.Id, map[string]any{"text": "Nei"}),
		"change": putChecklistItem(t, outsider, task.Id, item.Id, map[string]any{"done": true}),
		"delete": deleteChecklistItem(t, outsider, task.Id, item.Id),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider's %s: status %d body %s, want 404", name, r.Status, r.Body)
		}
	}
	if got := getChecklist(t, manager, task.Id); len(got) != 1 || got[0].Done {
		t.Errorf("the checklist = %+v, want it untouched by the refusals", got)
	}
}
