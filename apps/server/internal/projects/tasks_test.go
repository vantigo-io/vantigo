package projects_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The task operations (design §2 D5–D7, §4.1, §7): a project's work, nested
// one level deep, plus the cross-project list of what the caller still owes.
// Two things run through every case here — who may write (see the project ⇒
// read, member or manager ⇒ write, outsiders 404) and where a task sits among
// its siblings — so most tests read the tree back rather than trusting the
// writer's own answer.

// postTask sends one create and returns the response, for a case whose
// subject is the refusal.
func postTask(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, tasksPath(projectID), taskBody(overrides))
}

// updateTaskBody is the update request a task round-trips into: every field
// as it currently stands, carrying the revision the caller read, which a test
// then overrides one field of. A nil override value removes that field, the
// same convention updateBody uses for a project.
func updateTaskBody(task taskJSON, overrides map[string]any) map[string]any {
	body := map[string]any{
		"title":    task.Title,
		"status":   task.Status,
		"revision": task.Revision,
	}
	if task.Description != nil {
		body["description"] = *task.Description
	}
	if task.Assignee != nil {
		body["assigneeUserId"] = task.Assignee.UserId
	}
	if task.StartDate != nil {
		body["startDate"] = *task.StartDate
	}
	if task.DueDate != nil {
		body["dueDate"] = *task.DueDate
	}
	if task.EstimateHours != nil {
		body["estimateHours"] = *task.EstimateHours
	}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	return body
}

// putTask sends one update and returns the response.
func putTask(t *testing.T, c *modtest.Client, task taskJSON, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, taskPath(task.Id), updateTaskBody(task, overrides))
}

// changeTask is putTask for a test that expects the update to be applied.
func changeTask(t *testing.T, c *modtest.Client, task taskJSON, overrides map[string]any) taskJSON {
	t.Helper()
	r := putTask(t, c, task, overrides)
	if r.Status != http.StatusOK {
		t.Fatalf("update task: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated taskJSON
	r.JSON(&updated)
	return updated
}

// readTask reads one task, so a case can assert on the status before
// decoding.
func readTask(t *testing.T, c *modtest.Client, taskID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, taskPath(taskID), nil)
}

// showTask is readTask for a test that expects to be shown the task.
func showTask(t *testing.T, c *modtest.Client, taskID int32) taskJSON {
	t.Helper()
	r := readTask(t, c, taskID)
	if r.Status != http.StatusOK {
		t.Fatalf("get task: status %d body %s, want 200", r.Status, r.Body)
	}
	var task taskJSON
	r.JSON(&task)
	return task
}

func deleteTask(t *testing.T, c *modtest.Client, taskID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, taskPath(taskID), nil)
}

// moveTask sends one position change and returns the response. parentTaskID
// is nil for a move among top-level tasks.
func moveTask(t *testing.T, c *modtest.Client, taskID int32, parentTaskID *int32, position int32) *modtest.Response {
	t.Helper()
	body := map[string]any{"position": position}
	if parentTaskID != nil {
		body["parentTaskId"] = *parentTaskID
	}
	return c.Do(http.MethodPut, taskPath(taskID)+"/position", body)
}

// repositionTask is moveTask for a test that expects the move to be applied.
func repositionTask(t *testing.T, c *modtest.Client, taskID int32, parentTaskID *int32, position int32) taskJSON {
	t.Helper()
	r := moveTask(t, c, taskID, parentTaskID, position)
	if r.Status != http.StatusOK {
		t.Fatalf("move task: status %d body %s, want 200", r.Status, r.Body)
	}
	var task taskJSON
	r.JSON(&task)
	return task
}

// myTasks reads the caller's cross-project task list, failing the test unless
// it answered 200.
func myTasks(t *testing.T, c *modtest.Client) []taskJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/projects/my-tasks", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("my tasks: status %d body %s, want 200", r.Status, r.Body)
	}
	var tasks []taskJSON
	r.JSON(&tasks)
	return tasks
}

// taskTitles and taskPositions are what a tree assertion is actually about:
// which tasks came back and in what order, and the numbers they carry.
func taskTitles(tasks []taskJSON) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Title)
	}
	return out
}

func taskPositions(tasks []taskJSON) []int32 {
	out := make([]int32, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.Position)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInt32s(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A create appends: the first task of a project is position 1 and every
// further one takes the next number among its siblings. The assignee is
// embedded, named through the user directory, so the tree needs no second
// call to render who is on a task.
func TestPostProjectsByIdTasks_AppendsAndEmbedsTheAssignee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TSK1000"})
	_, memberID := signIn(t, h)
	setDisplayName(t, h, memberID, "Mia Medlem")

	first := createTask(t, c, project.Id, map[string]any{
		"title":          "  Skriv spesifikasjonen  ",
		"description":    "Hele omfanget",
		"assigneeUserId": memberID,
		"startDate":      "2026-03-01",
		"dueDate":        "2026-03-15",
		"estimateHours":  7.5,
	})
	if first.Title != "Skriv spesifikasjonen" {
		t.Errorf("Title = %q, want it trimmed", first.Title)
	}
	if first.ProjectId != project.Id || first.ParentTaskId != nil || first.Position != 1 {
		t.Errorf("task = %+v, want a top-level task of the project at position 1", first)
	}
	if first.Status != "todo" || first.CompletedAt != nil || first.Revision != 1 {
		t.Errorf("task = %+v, want status 'todo', no completedAt and revision 1", first)
	}
	if first.Assignee == nil || first.Assignee.UserId != memberID ||
		first.Assignee.DisplayName != "Mia Medlem" || !first.Assignee.Active {
		t.Errorf("Assignee = %+v, want the member, named and active", first.Assignee)
	}
	if first.DueDate == nil || *first.DueDate != "2026-03-15" || first.EstimateHours == nil || *first.EstimateHours != 7.5 {
		t.Errorf("task = %+v, want the due date and estimate that were sent", first)
	}
	if first.Checklist.Total != 0 || first.CommentCount != 0 {
		t.Errorf("Checklist = %+v, CommentCount = %d, want a new task to carry nothing", first.Checklist, first.CommentCount)
	}

	second := createTask(t, c, project.Id, map[string]any{"title": "Sett opp miljøet"})
	if second.Position != 2 {
		t.Errorf("Position = %d, want 2: a create appends", second.Position)
	}
	if second.Assignee != nil {
		t.Errorf("Assignee = %+v, want no assignee at all", second.Assignee)
	}
}

// Design §4.1's task rules, one case each. A rule that fails names the field
// it is about, because that is what the form renders the message under.
func TestPostProjectsByIdTasks_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TVAL1000"})
	_, disabledID := signIn(t, h)
	disableUser(t, h, disabledID)

	cases := []struct {
		name        string
		overrides   map[string]any
		wantedField string
	}{
		{"blank title", map[string]any{"title": "   "}, "title"},
		{"title longer than 200 characters", map[string]any{"title": longText(201)}, "title"},
		{"description longer than 4000 characters", map[string]any{"description": longText(4001)}, "description"},
		{"status outside the set", map[string]any{"status": "blocked"}, "status"},
		{"due date before the start date", map[string]any{"startDate": "2026-03-10", "dueDate": "2026-03-09"}, "dueDate"},
		{"estimate of zero", map[string]any{"estimateHours": 0}, "estimateHours"},
		{"negative estimate", map[string]any{"estimateHours": -1}, "estimateHours"},
		{"assignee who does not exist", map[string]any{"assigneeUserId": uuid.New()}, "assigneeUserId"},
		{"assignee whose account is disabled", map[string]any{"assigneeUserId": disabledID}, "assigneeUserId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := postTask(t, c, project.Id, tc.overrides)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if len(problem.Errors[tc.wantedField]) == 0 {
				t.Errorf("errors = %v, want a message on %q", problem.Errors, tc.wantedField)
			}
		})
	}
}

// longText builds a string of n runes, for the two length rules.
func longText(n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

// D6's one level of nesting: a subtask is appended among its parent's
// subtasks, numbered inside that group rather than among the project's
// top-level tasks, and the tree nests it under the parent.
func TestPostProjectsByIdTasks_Subtask_IsNumberedInsideItsParent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TSUB1000"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	createTask(t, c, project.Id, map[string]any{"title": "Gjennomføring"})

	first := createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id})
	second := createTask(t, c, project.Id, map[string]any{"title": "Book rom", "parentTaskId": parent.Id})
	if first.ParentTaskId == nil || *first.ParentTaskId != parent.Id || first.Position != 1 {
		t.Errorf("first subtask = %+v, want position 1 under the parent", first)
	}
	if second.Position != 2 {
		t.Errorf("second subtask Position = %d, want 2", second.Position)
	}

	tree := getTasks(t, c, project.Id)
	if got := taskTitles(tree); !equalStrings(got, []string{"Forberedelser", "Gjennomføring"}) {
		t.Fatalf("top level = %v, want only the two top-level tasks", got)
	}
	if got := taskTitles(tree[0].Subtasks); !equalStrings(got, []string{"Bestill maskinvare", "Book rom"}) {
		t.Errorf("subtasks = %v, want both, by position", got)
	}
	if len(tree[1].Subtasks) != 0 {
		t.Errorf("second task's subtasks = %v, want none", tree[1].Subtasks)
	}
}

// The two nesting refusals: a parent has to be a task of this project, and it
// has to be top-level itself (D6 — one level, no deeper).
func TestPostProjectsByIdTasks_ImpossibleParent_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TPAR1000"})
	other := createProject(t, c, map[string]any{"code": "TPAR1001"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	subtask := createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id})
	elsewhere := createTask(t, c, other.Id, map[string]any{"title": "På et annet prosjekt"})

	cases := []struct {
		name        string
		parent      int32
		wantMessage string
	}{
		{"a task of another project", elsewhere.Id, "does not exist"},
		{"a task that is itself a subtask", subtask.Id, "nested one level deep"},
		{"a task that does not exist", 999_999, "does not exist"},
		// Zero is no task's id, so it is unknown rather than "itself": a
		// create has no task yet for a parent to be.
		{"a parent id of zero", 0, "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := postTask(t, c, project.Id, map[string]any{"title": "Umulig", "parentTaskId": tc.parent})
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			messages := problem.Errors["parentTaskId"]
			if len(messages) == 0 {
				t.Fatalf("errors = %v, want a message on 'parentTaskId'", problem.Errors)
			}
			if !strings.Contains(messages[0], tc.wantMessage) {
				t.Errorf("message = %q, want it to say %q", messages[0], tc.wantMessage)
			}
		})
	}
}

// The tree carries the checklist progress and the comment count of every
// task, subtasks included, computed for the whole project in one query.
func TestGetProjectsByIdTasks_CarriesChecklistProgressAndCommentCounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TCNT1000"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	subtask := createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id})
	createTask(t, c, project.Id, map[string]any{"title": "Gjennomføring"})

	tickChecklistItem(t, c, parent.Id, "Avklar omfang")
	addChecklistItem(t, c, parent.Id, "Avklar budsjett")
	addChecklistItem(t, c, parent.Id, "Avklar frist")
	addComment(t, c, parent.Id, "Ser bra ut")
	tickChecklistItem(t, c, subtask.Id, "Velg leverandør")
	addComment(t, c, subtask.Id, "Bestilt")
	addComment(t, c, subtask.Id, "Levert")

	tree := getTasks(t, c, project.Id)
	if len(tree) != 2 {
		t.Fatalf("tree = %v, want two top-level tasks", taskTitles(tree))
	}
	if tree[0].Checklist != (checklistJSON{Total: 3, Done: 1}) || tree[0].CommentCount != 1 {
		t.Errorf("parent Checklist = %+v, CommentCount = %d, want 3/1 and 1", tree[0].Checklist, tree[0].CommentCount)
	}
	if len(tree[0].Subtasks) != 1 ||
		tree[0].Subtasks[0].Checklist != (checklistJSON{Total: 1, Done: 1}) || tree[0].Subtasks[0].CommentCount != 2 {
		t.Errorf("subtask = %+v, want 1/1 and two comments", tree[0].Subtasks)
	}
	if tree[1].Checklist != (checklistJSON{}) || tree[1].CommentCount != 0 {
		t.Errorf("task with nothing on it = %+v, want zero counts", tree[1])
	}
}

// The list's two filters. A status outside the enumeration is a mistake worth
// reporting rather than a filter that silently matches nothing, exactly as the
// project list's own status filter is.
func TestGetProjectsByIdTasks_FiltersByStatusAndAssignee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TFLT1000"})
	_, memberID := signIn(t, h)
	createTask(t, c, project.Id, map[string]any{"title": "Min oppgave", "assigneeUserId": memberID})
	createTask(t, c, project.Id, map[string]any{"title": "Ferdig", "status": "done"})
	createTask(t, c, project.Id, map[string]any{"title": "Uten noen"})

	byStatus := listTasks(t, c, project.Id, "status=done")
	if got := taskTitles(byStatus); !equalStrings(got, []string{"Ferdig"}) {
		t.Errorf("status=done = %v, want only the done task", got)
	}
	byAssignee := listTasks(t, c, project.Id, "assigneeUserId="+memberID.String())
	if got := taskTitles(byAssignee); !equalStrings(got, []string{"Min oppgave"}) {
		t.Errorf("assigneeUserId = %v, want only the assigned task", got)
	}
	if got := taskTitles(listTasks(t, c, project.Id, "status=")); len(got) != 3 {
		t.Errorf("status= (empty) = %v, want no filter at all", got)
	}

	// A subtask that matches while its parent does not is answered at the top
	// level rather than dropped: a filter never hides a task it matched. It
	// still says whose subtask it is.
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id, "assigneeUserId": memberID})
	promoted := listTasks(t, c, project.Id, "assigneeUserId="+memberID.String())
	if got := taskTitles(promoted); !equalStrings(got, []string{"Min oppgave", "Bestill maskinvare"}) {
		t.Fatalf("assigneeUserId = %v, want the matching subtask answered beside the matching task", got)
	}
	if promoted[1].ParentTaskId == nil || *promoted[1].ParentTaskId != parent.Id {
		t.Errorf("promoted subtask = %+v, want it to keep its parentTaskId", promoted[1])
	}
	if len(promoted[1].Subtasks) != 0 || len(promoted[0].Subtasks) != 0 {
		t.Errorf("promoted = %+v, want no nesting when the parent did not match", promoted)
	}

	r := c.Do(http.MethodGet, tasksPath(project.Id)+"?status=blocked", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status=blocked: status %d body %s, want 400", r.Status, r.Body)
	}
}

// listTasks is getTasks with a query string, for the filter cases.
func listTasks(t *testing.T, c *modtest.Client, projectID int32, query string) []taskJSON {
	t.Helper()
	r := c.Do(http.MethodGet, tasksPath(projectID)+"?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list tasks ?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var tasks []taskJSON
	r.JSON(&tasks)
	return tasks
}

// One task reads back with its own subtasks, so the drawer opens on a task id
// alone — the task's project is resolved from the task, never from the path.
func TestGetProjectsTasksByTaskId_CarriesItsSubtasks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TGET1000"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id})
	createTask(t, c, project.Id, map[string]any{"title": "Book rom", "parentTaskId": parent.Id})

	got := showTask(t, c, parent.Id)
	if got.Id != parent.Id || got.ProjectId != project.Id {
		t.Errorf("task = %+v, want the parent of the project", got)
	}
	if titles := taskTitles(got.Subtasks); !equalStrings(titles, []string{"Bestill maskinvare", "Book rom"}) {
		t.Errorf("Subtasks = %v, want both, by position", titles)
	}
	if r := readTask(t, c, 999_999); r.Status != http.StatusNotFound {
		t.Errorf("unknown task: status %d body %s, want 404", r.Status, r.Body)
	}
}

// An update carries every field of the task as it should stand afterwards.
// Moving into 'done' stamps completedAt from the clock; leaving it clears the
// stamp again (§4.1), and the revision moves on either way.
func TestPutProjectsTasksByTaskId_ReplacesTheTaskAndTracksCompletedAt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TUPD1000"})
	_, memberID := signIn(t, h)
	task := createTask(t, c, project.Id, map[string]any{
		"title": "Skriv spesifikasjonen", "description": "Hele omfanget", "estimateHours": 4,
	})

	changed := changeTask(t, c, task, map[string]any{
		"title": "Skriv spesifikasjonen på nytt", "assigneeUserId": memberID, "dueDate": "2026-04-01",
		"description": nil, "estimateHours": nil,
	})
	if changed.Title != "Skriv spesifikasjonen på nytt" || changed.Revision != 2 {
		t.Errorf("task = %+v, want the new title at revision 2", changed)
	}
	if changed.Description != nil || changed.EstimateHours != nil {
		t.Errorf("task = %+v, want the omitted fields cleared: an update is a full replace", changed)
	}
	if changed.Assignee == nil || changed.Assignee.UserId != memberID || changed.DueDate == nil || *changed.DueDate != "2026-04-01" {
		t.Errorf("task = %+v, want the assignee and due date that were sent", changed)
	}
	if changed.Position != task.Position {
		t.Errorf("Position = %d, want the update to leave the ordering alone (%d)", changed.Position, task.Position)
	}

	done := changeTask(t, c, changed, map[string]any{"status": "done"})
	if done.Status != "done" || done.CompletedAt == nil {
		t.Fatalf("task = %+v, want status 'done' with a completedAt", done)
	}
	if !done.CompletedAt.Equal(h.Now()) {
		t.Errorf("CompletedAt = %s, want the harness clock's %s", done.CompletedAt, h.Now())
	}

	stillDone := changeTask(t, c, done, map[string]any{"title": "Ferdig, men omdøpt"})
	if stillDone.CompletedAt == nil || !stillDone.CompletedAt.Equal(*done.CompletedAt) {
		t.Errorf("CompletedAt = %v, want the original stamp: the task never left 'done'", stillDone.CompletedAt)
	}

	reopened := changeTask(t, c, stillDone, map[string]any{"status": "in-progress"})
	if reopened.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want it cleared when the task leaves 'done'", reopened.CompletedAt)
	}
}

// The revision guard, the same one a project update carries: an edit built on
// a task somebody else has since changed is refused rather than applied on
// top of theirs.
func TestPutProjectsTasksByTaskId_StaleRevision_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TREV1000"})
	task := createTask(t, c, project.Id, nil)
	changeTask(t, c, task, map[string]any{"title": "Først"})

	r := putTask(t, c, task, map[string]any{"title": "Så"})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Status != http.StatusConflict || problem.Detail == "" {
		t.Errorf("problem = %+v, want a 409 naming both revisions", problem)
	}
	if got := showTask(t, c, task.Id); got.Title != "Først" {
		t.Errorf("Title = %q, want the first writer's: the stale update wrote nothing", got.Title)
	}
}

// A delete takes the task's subtasks, checklist items and comments with it
// (§4.1). Nothing is left behind pointing at a task that is gone.
func TestDeleteProjectsTasksByTaskId_CascadesToSubtasksChecklistAndComments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TDEL1000"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	subtask := createTask(t, c, project.Id, map[string]any{"title": "Bestill maskinvare", "parentTaskId": parent.Id})
	survivor := createTask(t, c, project.Id, map[string]any{"title": "Gjennomføring"})
	addChecklistItem(t, c, parent.Id, "Avklar omfang")
	addChecklistItem(t, c, subtask.Id, "Velg leverandør")
	addComment(t, c, parent.Id, "Ser bra ut")
	addComment(t, c, subtask.Id, "Bestilt")
	addChecklistItem(t, c, survivor.Id, "Kjør i gang")

	if r := deleteTask(t, c, parent.Id); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}

	if r := readTask(t, c, parent.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleted task: status %d, want 404", r.Status)
	}
	if r := readTask(t, c, subtask.Id); r.Status != http.StatusNotFound {
		t.Errorf("subtask of a deleted task: status %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.tasks WHERE project_id = $1`, project.Id); n != 1 {
		t.Errorf("tasks left = %d, want only the survivor", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.task_checklist_items`); n != 1 {
		t.Errorf("checklist items left = %d, want only the survivor's", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.task_comments`); n != 0 {
		t.Errorf("comments left = %d, want none", n)
	}
	// The group the task was in closes its gap: a delete decides positions the
	// same way a move does.
	if got := taskPositions(getTasks(t, c, project.Id)); !equalInt32s(got, []int32{1}) {
		t.Errorf("positions = %v, want 1..n with the deleted task's number reused", got)
	}
	if r := deleteTask(t, c, parent.Id); r.Status != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d, want 404", r.Status)
	}
}

// A move among siblings renumbers the whole group 1..n, so the tree never
// shows a gap or two tasks sharing a number.
func TestPutProjectsTasksByTaskIdPosition_ReordersSiblings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TPOS1000"})
	first := createTask(t, c, project.Id, map[string]any{"title": "En"})
	createTask(t, c, project.Id, map[string]any{"title": "To"})
	third := createTask(t, c, project.Id, map[string]any{"title": "Tre"})

	moved := repositionTask(t, c, third.Id, nil, 1)
	if moved.Position != 1 {
		t.Errorf("Position = %d, want 1", moved.Position)
	}
	tree := getTasks(t, c, project.Id)
	if got := taskTitles(tree); !equalStrings(got, []string{"Tre", "En", "To"}) {
		t.Errorf("tree = %v, want the moved task first", got)
	}
	if got := taskPositions(tree); !equalInt32s(got, []int32{1, 2, 3}) {
		t.Errorf("positions = %v, want 1..n", got)
	}

	// A position past the end is the end, not a refusal: "move it last" is
	// what a drag to the bottom of the list means.
	repositionTask(t, c, first.Id, nil, 99)
	if got := taskTitles(getTasks(t, c, project.Id)); !equalStrings(got, []string{"Tre", "To", "En"}) {
		t.Errorf("tree = %v, want the first task moved to the end", got)
	}
	if got := taskPositions(getTasks(t, c, project.Id)); !equalInt32s(got, []int32{1, 2, 3}) {
		t.Errorf("positions = %v, want 1..n", got)
	}
}

// Re-parenting moves a task between two sibling groups, and renumbers both:
// the group it left must close its gap, and the group it joined must make
// room.
func TestPutProjectsTasksByTaskIdPosition_ReparentsAndRenumbersBothGroups(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TPOS1001"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	existing := createTask(t, c, project.Id, map[string]any{"title": "Book rom", "parentTaskId": parent.Id})
	middle := createTask(t, c, project.Id, map[string]any{"title": "Gjennomføring"})
	createTask(t, c, project.Id, map[string]any{"title": "Etterarbeid"})

	moved := repositionTask(t, c, middle.Id, &parent.Id, 1)
	if moved.ParentTaskId == nil || *moved.ParentTaskId != parent.Id || moved.Position != 1 {
		t.Fatalf("moved = %+v, want it first under the parent", moved)
	}

	tree := getTasks(t, c, project.Id)
	if got := taskTitles(tree); !equalStrings(got, []string{"Forberedelser", "Etterarbeid"}) {
		t.Errorf("top level = %v, want the moved task gone from it", got)
	}
	if got := taskPositions(tree); !equalInt32s(got, []int32{1, 2}) {
		t.Errorf("top-level positions = %v, want the gap closed", got)
	}
	if got := taskTitles(tree[0].Subtasks); !equalStrings(got, []string{"Gjennomføring", "Book rom"}) {
		t.Errorf("subtasks = %v, want the moved task first", got)
	}
	if got := taskPositions(tree[0].Subtasks); !equalInt32s(got, []int32{1, 2}) {
		t.Errorf("subtask positions = %v, want 1..n", got)
	}

	// And back out again: a subtask becomes a top-level task by moving to no
	// parent at all.
	repositionTask(t, c, existing.Id, nil, 2)
	tree = getTasks(t, c, project.Id)
	if got := taskTitles(tree); !equalStrings(got, []string{"Forberedelser", "Book rom", "Etterarbeid"}) {
		t.Errorf("top level = %v, want the promoted task second", got)
	}
	if len(tree[0].Subtasks) != 1 {
		t.Errorf("subtasks = %v, want only the one that stayed", taskTitles(tree[0].Subtasks))
	}
}

// The move's own refusals: the parent rules a create enforces hold here too,
// plus the one only a move can break — a task that has subtasks cannot become
// a subtask, because that would nest three levels deep (D6).
func TestPutProjectsTasksByTaskIdPosition_ImpossibleMove_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TPOS1002"})
	other := createProject(t, c, map[string]any{"code": "TPOS1003"})
	parent := createTask(t, c, project.Id, map[string]any{"title": "Forberedelser"})
	subtask := createTask(t, c, project.Id, map[string]any{"title": "Book rom", "parentTaskId": parent.Id})
	loose := createTask(t, c, project.Id, map[string]any{"title": "Gjennomføring"})
	elsewhere := createTask(t, c, other.Id, map[string]any{"title": "På et annet prosjekt"})

	cases := []struct {
		name        string
		taskID      int32
		parentID    *int32
		position    int32
		wantedField string
	}{
		{"under a parent on another project", loose.Id, &elsewhere.Id, 1, "parentTaskId"},
		{"under a parent that is itself a subtask", loose.Id, &subtask.Id, 1, "parentTaskId"},
		{"under a parent that does not exist", loose.Id, ptr(int32(999_999)), 1, "parentTaskId"},
		{"under itself", loose.Id, &loose.Id, 1, "parentTaskId"},
		{"a task with subtasks becoming a subtask", parent.Id, &loose.Id, 1, "parentTaskId"},
		{"to a position below one", loose.Id, nil, 0, "position"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := moveTask(t, c, tc.taskID, tc.parentID, tc.position)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if len(problem.Errors[tc.wantedField]) == 0 {
				t.Errorf("errors = %v, want a message on %q", problem.Errors, tc.wantedField)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// my-tasks is the caller's own open work across every project they can see,
// due date first (nulls last), then project code, then position. A task on a
// project they hold no role on is not theirs to see, even when it is assigned
// to them.
func TestGetProjectsMyTasks_ListsOpenAssignedTasksOnVisibleProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	worker, workerID := signIn(t, h)
	first := createProject(t, manager, map[string]any{"code": "MYT1000", "name": "Første prosjekt"})
	second := createProject(t, manager, map[string]any{"code": "MYT1001"})
	hidden := createProject(t, manager, map[string]any{"code": "MYT1002"})
	addRole(t, h, first.Id, workerID, "member")
	addRole(t, h, second.Id, workerID, "viewer")

	createTask(t, manager, first.Id, map[string]any{"title": "Sent", "assigneeUserId": workerID, "dueDate": "2026-03-20"})
	createTask(t, manager, first.Id, map[string]any{"title": "Uten frist", "assigneeUserId": workerID})
	createTask(t, manager, first.Id, map[string]any{"title": "Ferdig", "assigneeUserId": workerID, "status": "done", "dueDate": "2026-01-01"})
	createTask(t, manager, first.Id, map[string]any{"title": "Andres", "dueDate": "2026-01-02"})
	createTask(t, manager, second.Id, map[string]any{"title": "Tidlig", "assigneeUserId": workerID, "dueDate": "2026-03-01"})
	createTask(t, manager, hidden.Id, map[string]any{"title": "Usynlig", "assigneeUserId": workerID, "dueDate": "2026-02-01"})

	mine := myTasks(t, worker)
	if got := taskTitles(mine); !equalStrings(got, []string{"Tidlig", "Sent", "Uten frist"}) {
		t.Fatalf("my tasks = %v, want the open, visible, assigned ones by due date with nulls last", got)
	}
	if mine[0].ProjectCode != "MYT1001" || mine[1].ProjectCode != "MYT1000" || mine[1].ProjectName != "Første prosjekt" {
		t.Errorf("my tasks = %+v, want each row to name its project", mine)
	}

	// The manager assigned all of it and owns none of it.
	if got := myTasks(t, manager); len(got) != 0 {
		t.Errorf("the manager's my-tasks = %v, want none: they are assigned nothing", taskTitles(got))
	}
	// A caller who sees every project still only gets their own tasks.
	viewAll, _ := signIn(t, h, "projects:view-all")
	if got := myTasks(t, viewAll); len(got) != 0 {
		t.Errorf("view-all's my-tasks = %v, want none", taskTitles(got))
	}
}

// The literal path segment wins over {id}: /projects/my-tasks is the task
// list, not a project whose id is "my-tasks".
func TestGetProjectsMyTasks_IsNotReadAsAProjectId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "MYT1003"})
	_, workerID := signIn(t, h)
	addRole(t, h, project.Id, workerID, "member")

	r := c.Do(http.MethodGet, "/api/v1/projects/my-tasks", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 — the literal route, not the project one", r.Status, r.Body)
	}
	// The same shape for the single task route, which shares its first
	// segment with /projects/{id}/...
	task := createTask(t, c, project.Id, nil)
	if got := showTask(t, c, task.Id); got.Id != task.Id {
		t.Errorf("task = %+v, want /projects/tasks/{taskId} to read the task", got)
	}
}

// Design §7's authorization: seeing the project is enough to read its tasks,
// writing takes the member or manager role, and an outsider is told the task
// does not exist — in exactly the words an unknown id is told that.
func TestTasks_Authorization_ViewerReadsMemberWritesOutsiderIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "TAUT1000"})
	task := createTask(t, manager, project.Id, nil)

	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	outsider, _ := signIn(t, h)

	if got := getTasks(t, viewer, project.Id); len(got) != 1 {
		t.Errorf("viewer's tree = %v, want the project's one task", taskTitles(got))
	}
	if got := showTask(t, viewer, task.Id); got.Id != task.Id {
		t.Errorf("viewer's task = %+v, want the task", got)
	}
	refusals := map[string]*modtest.Response{
		"create":   postTask(t, viewer, project.Id, nil),
		"update":   putTask(t, viewer, task, map[string]any{"title": "Nei"}),
		"delete":   deleteTask(t, viewer, task.Id),
		"position": moveTask(t, viewer, task.Id, nil, 1),
	}
	for name, r := range refusals {
		if r.Status != http.StatusForbidden {
			t.Errorf("viewer's %s: status %d body %s, want 403", name, r.Status, r.Body)
		}
	}

	created := createTask(t, member, project.Id, map[string]any{"title": "Medlemmets oppgave"})
	if changed := changeTask(t, member, created, map[string]any{"title": "Endret"}); changed.Title != "Endret" {
		t.Errorf("member's update = %+v, want it applied", changed)
	}
	if r := deleteTask(t, member, created.Id); r.Status != http.StatusNoContent {
		t.Errorf("member's delete: status %d body %s, want 204", r.Status, r.Body)
	}

	if r := getTasksResponse(t, outsider, project.Id); r.Status != http.StatusNotFound {
		t.Errorf("outsider's tree: status %d body %s, want 404", r.Status, r.Body)
	}
	existing := readTask(t, outsider, task.Id)
	unknown := readTask(t, outsider, 999_999)
	if existing.Status != http.StatusNotFound || unknown.Status != http.StatusNotFound {
		t.Fatalf("outsider: existing %d, unknown %d, want 404 for both", existing.Status, unknown.Status)
	}
	if string(existing.Body) != string(unknown.Body) {
		t.Errorf("outsider's 404 body %q differs from an unknown task's %q", existing.Body, unknown.Body)
	}
	if r := postTask(t, outsider, project.Id, nil); r.Status != http.StatusNotFound {
		t.Errorf("outsider's create: status %d body %s, want 404", r.Status, r.Body)
	}
}

// getTasksResponse is the raw list response the authorization case needs,
// where getTasks' "must be 200" would fail the test instead.
func getTasksResponse(t *testing.T, c *modtest.Client, projectID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, tasksPath(projectID), nil)
}

// capabilities.canContribute is the project response's answer to "may I write
// tasks here", so the frontend never re-derives it from a role.
func TestGetProjectsById_Capabilities_CanContribute(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "TCAP1000"})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")

	cases := []struct {
		name   string
		client *modtest.Client
		want   bool
	}{
		{"manager", manager, true},
		{"member", member, true},
		{"viewer", viewer, false},
	}
	for _, tc := range cases {
		r := getProject(t, tc.client, project.Id)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: status %d body %s, want 200", tc.name, r.Status, r.Body)
		}
		var read projectJSON
		r.JSON(&read)
		if read.Capabilities.CanContribute != tc.want {
			t.Errorf("%s: canContribute = %v, want %v", tc.name, read.Capabilities.CanContribute, tc.want)
		}
	}
}

// An assignment survives the account it points at: a user disabled after they
// were given a task keeps the task, and the task renders them inactive rather
// than losing the assignment (the same rule a project role follows).
func TestGetProjectsByIdTasks_AssigneeDisabledAfterwards_StaysAssignedAndInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TDIS1000"})
	_, memberID := signIn(t, h)
	setDisplayName(t, h, memberID, "Mia Medlem")
	task := createTask(t, c, project.Id, map[string]any{"assigneeUserId": memberID})

	disableUser(t, h, memberID)

	got := showTask(t, c, task.Id)
	if got.Assignee == nil || got.Assignee.UserId != memberID || got.Assignee.DisplayName != "Mia Medlem" {
		t.Fatalf("Assignee = %+v, want the assignment kept and named", got.Assignee)
	}
	if got.Assignee.Active {
		t.Error("Assignee.Active = true, want false for a disabled account")
	}
	// The task is still editable: an update that re-sends the assignment it
	// already carries is not making a new one, so a disabled account does not
	// freeze the task — it only stops being somebody anything new can be given
	// to.
	kept := changeTask(t, c, got, map[string]any{"assigneeUserId": memberID, "title": "Fortsatt tildelt"})
	if kept.Title != "Fortsatt tildelt" {
		t.Errorf("Title = %q, want the rename applied", kept.Title)
	}
	if kept.Assignee == nil || kept.Assignee.UserId != memberID || kept.Assignee.Active {
		t.Errorf("Assignee = %+v, want the same person, still inactive", kept.Assignee)
	}

	// Handing it to a *different* disabled account is a new assignment, and
	// that is what the rule is about.
	_, otherDisabledID := signIn(t, h)
	disableUser(t, h, otherDisabledID)
	r := putTask(t, c, kept, map[string]any{"assigneeUserId": otherDisabledID})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("assigning another disabled user: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["assigneeUserId"]) == 0 {
		t.Errorf("errors = %v, want a message on 'assigneeUserId'", problem.Errors)
	}

	// And it can always be edited away.
	if cleared := changeTask(t, c, kept, map[string]any{"assigneeUserId": nil}); cleared.Assignee != nil {
		t.Errorf("Assignee = %+v, want the assignment cleared", cleared.Assignee)
	}
}

// A task on a project the caller cannot see is not a task they may edit or
// delete either, and the refusal is the same bare 404 a reader gets.
func TestTasks_Outsider_CannotWriteAndLearnsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "TOUT1000"})
	task := createTask(t, manager, project.Id, nil)
	outsider, _ := signIn(t, h)

	for name, r := range map[string]*modtest.Response{
		"update":   putTask(t, outsider, task, map[string]any{"title": "Nei"}),
		"delete":   deleteTask(t, outsider, task.Id),
		"position": moveTask(t, outsider, task.Id, nil, 1),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider's %s: status %d body %s, want 404", name, r.Status, r.Body)
		}
	}
	if got := showTask(t, manager, task.Id); got.Title != task.Title {
		t.Errorf("task = %+v, want it untouched", got)
	}
}

// A caller with view-all sees every project's tasks but writes none of them:
// seeing everything is not contributing to everything (that is manage-all).
func TestTasks_ViewAll_ReadsEveryProjectButWritesNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "TVA1000"})
	task := createTask(t, manager, project.Id, nil)
	viewAll, _ := signIn(t, h, "projects:view-all")
	manageAll, _ := signIn(t, h, "projects:manage-all")

	if got := getTasks(t, viewAll, project.Id); len(got) != 1 {
		t.Errorf("view-all's tree = %v, want the project's one task", taskTitles(got))
	}
	if r := postTask(t, viewAll, project.Id, nil); r.Status != http.StatusForbidden {
		t.Errorf("view-all's create: status %d body %s, want 403", r.Status, r.Body)
	}
	if got := changeTask(t, manageAll, task, map[string]any{"title": "Styrt"}); got.Title != "Styrt" {
		t.Errorf("manage-all's update = %+v, want it applied", got)
	}
}
