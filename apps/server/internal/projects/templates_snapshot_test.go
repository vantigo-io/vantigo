package projects_test

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Checklists in templates and saving a project as a template (spec §4): the
// blueprint carries each task's checklist, apply turns it into real items, and
// a project's plan can be read back into a new template that applies to the
// same structure.

func projectSaveAsTemplatePath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/save-as-template", projectID)
}

func saveAsTemplate(t *testing.T, c *modtest.Client, projectID int32, body map[string]any) templateJSON {
	t.Helper()
	r := c.Do(http.MethodPost, projectSaveAsTemplatePath(projectID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("save as template: status %d body %s, want 201", r.Status, r.Body)
	}
	var out templateJSON
	r.JSON(&out)
	return out
}

func offsetPtr(n int32) *int32 { return &n }

func TestProjectTemplates_ChecklistsAreValidatedAndApplied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	tmpl := createProjectTemplate(t, c, map[string]any{
		"name": "With checklists",
		"tasks": []map[string]any{{
			"title":     "Kickoff",
			"checklist": []string{"  Book the room  ", "Send the agenda"},
			"subtasks": []map[string]any{
				{"title": "Agenda", "checklist": []string{"Draft", "Review"}},
			},
		}},
	})
	if got := tmpl.Tasks[0].Checklist; !reflect.DeepEqual(got, []string{"Book the room", "Send the agenda"}) {
		t.Errorf("stored checklist = %q, want it trimmed and in order", got)
	}

	p := createProject(t, c, map[string]any{"code": "TPLCHK1"})
	r := c.Do(http.MethodPost, projectApplyTemplatePath(p.Id), map[string]any{"templateId": tmpl.Id, "kickoffDate": "2026-11-10"})
	if r.Status != http.StatusOK {
		t.Fatalf("apply: status %d body %s, want 200", r.Status, r.Body)
	}
	tasks := getTasks(t, c, p.Id)
	if len(tasks) != 1 || len(tasks[0].Subtasks) != 1 {
		t.Fatalf("tasks = %+v, want one with one subtask", tasks)
	}
	items := getChecklist(t, c, tasks[0].Id)
	if got := checklistTexts(items); !reflect.DeepEqual(got, []string{"Book the room", "Send the agenda"}) {
		t.Errorf("task checklist = %q", got)
	}
	for i, it := range items {
		if it.Done || it.Position != int32(i+1) {
			t.Errorf("item %d = %+v, want open at position %d", i, it, i+1)
		}
	}
	if got := checklistTexts(getChecklist(t, c, tasks[0].Subtasks[0].Id)); !reflect.DeepEqual(got, []string{"Draft", "Review"}) {
		t.Errorf("subtask checklist = %q", got)
	}

	refused := []struct {
		name  string
		task  map[string]any
		field string
	}{
		{"a blank item", map[string]any{"title": "A", "checklist": []string{"ok", "   "}}, "tasks[0].checklist[1]"},
		{"an item over 500 characters", map[string]any{"title": "A", "checklist": []string{strings.Repeat("å", 501)}}, "tasks[0].checklist[0]"},
		{"more than 100 items", map[string]any{"title": "A", "checklist": make101Items()}, "tasks[0].checklist"},
		{"a blank subtask item", map[string]any{"title": "A", "subtasks": []map[string]any{{"title": "a", "checklist": []string{""}}}}, "tasks[0].subtasks[0].checklist[0]"},
	}
	for _, tc := range refused {
		r := c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{"name": "Bad", "tasks": []map[string]any{tc.task}})
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", tc.name, r.Status)
			continue
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors %v, want one under %q", tc.name, problem.Errors, tc.field)
		}
	}

	// 500 two-byte characters and exactly 100 items both fit.
	createProjectTemplate(t, c, map[string]any{"name": "Fits", "tasks": []map[string]any{
		{"title": "A", "checklist": append(make101Items()[:99], strings.Repeat("å", 500))},
	}})
}

func make101Items() []string {
	items := make([]string, 101)
	for i := range items {
		items[i] = fmt.Sprintf("Item %d", i+1)
	}
	return items
}

// The snapshot: milestones in planned order, top-level tasks with subtasks and
// checklists, milestone links, estimates and descriptions, and dependencies
// between top-level tasks — anchored on the project's start date. Assignees,
// statuses and links that touch a subtask are left behind.
func TestProjectTemplates_SaveAsTemplate_Snapshot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")
	p := createProject(t, c, map[string]any{"code": "SAVETPL1", "name": "Rollout", "startDate": "2026-11-02"})

	m1 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Design", "description": "Sketches", "targetDate": "2026-11-09"})
	m2 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Launch"}) // no target date: offset 0
	early := createTask(t, c, p.Id, map[string]any{
		"title": "Prepare", "description": "Before the start", "startDate": "2026-10-28", "dueDate": "2026-11-04",
		"estimateHours": 6, "deliveryMilestoneId": m1.Id, "assigneeUserId": userID.String(), "status": "in-progress",
	})
	second := createTask(t, c, p.Id, map[string]any{"title": "Build", "dueDate": "2026-11-20", "deliveryMilestoneId": m2.Id})
	sub := createTask(t, c, p.Id, map[string]any{"title": "Wireframes", "parentTaskId": early.Id, "startDate": "2026-11-03", "estimateHours": 2})
	addChecklistItem(t, c, early.Id, "Gather notes")
	tickChecklistItem(t, c, early.Id, "Book the room")
	addChecklistItem(t, c, sub.Id, "Home page")
	createTaskDependency(t, c, p.Id, early.Id, second.Id)
	createTaskDependency(t, c, p.Id, sub.Id, second.Id) // touches a subtask: skipped

	before := len(readTimeline(t, c, p.Id, "").Data)
	tmpl := saveAsTemplate(t, c, p.Id, map[string]any{"name": "  Rollout plan  ", "description": "From the rollout"})

	if tmpl.Name != "Rollout plan" || tmpl.Description == nil || *tmpl.Description != "From the rollout" {
		t.Errorf("header = %q / %v", tmpl.Name, tmpl.Description)
	}
	wantMilestones := []templateMilestoneJSON{
		{Name: "Design", Description: "Sketches", TargetDayOffset: 7},
		{Name: "Launch", TargetDayOffset: 0},
	}
	if !reflect.DeepEqual(tmpl.Milestones, wantMilestones) {
		t.Errorf("milestones = %+v, want %+v", tmpl.Milestones, wantMilestones)
	}
	six, two := 6.0, 2.0
	wantTasks := []templateTaskJSON{
		{
			// Started five days before the project: clamped to 0.
			Title: "Prepare", Description: "Before the start", StartDayOffset: offsetPtr(0), DueDayOffset: offsetPtr(2),
			EstimateHours: &six, MilestoneIndex: offsetPtr(0), Checklist: []string{"Gather notes", "Book the room"},
			Subtasks: []templateSubtaskJSON{{Title: "Wireframes", StartDayOffset: offsetPtr(1), EstimateHours: &two, Checklist: []string{"Home page"}}},
		},
		{Title: "Build", DueDayOffset: offsetPtr(18), MilestoneIndex: offsetPtr(1)},
	}
	if !reflect.DeepEqual(tmpl.Tasks, wantTasks) {
		t.Errorf("tasks = %+v, want %+v", tmpl.Tasks, wantTasks)
	}
	if want := []templateDependencyJSON{{PredecessorTaskIndex: 0, SuccessorTaskIndex: 1}}; !reflect.DeepEqual(tmpl.Dependencies, want) {
		t.Errorf("dependencies = %+v, want %+v", tmpl.Dependencies, want)
	}
	if tmpl.BillingType == nil || *tmpl.BillingType != "time-and-materials" {
		t.Errorf("billingType = %v, want the project's", tmpl.BillingType)
	}
	if after := len(readTimeline(t, c, p.Id, "").Data); after != before {
		t.Errorf("timeline entries %d -> %d, want saving to record nothing", before, after)
	}

	// Round trip: applying the saved template to a new project reproduces the
	// structure, its checklists open and its tasks unassigned and to do.
	q := createProject(t, c, map[string]any{"code": "SAVETPL2"})
	r := c.Do(http.MethodPost, projectApplyTemplatePath(q.Id), map[string]any{"templateId": tmpl.Id, "kickoffDate": "2026-12-01"})
	if r.Status != http.StatusOK {
		t.Fatalf("apply: status %d body %s", r.Status, r.Body)
	}
	milestones := getDeliveryMilestones(t, c, q.Id)
	if len(milestones) != 2 || milestones[0].Name != "Design" || *milestones[0].TargetDate != "2026-12-08" || *milestones[1].TargetDate != "2026-12-01" {
		t.Fatalf("milestones = %+v", milestones)
	}
	tasks := getTasks(t, c, q.Id)
	if len(tasks) != 2 || tasks[0].Title != "Prepare" || tasks[1].Title != "Build" {
		t.Fatalf("tasks = %+v", tasks)
	}
	got := tasks[0]
	if got.Assignee != nil || got.Status != "todo" || *got.StartDate != "2026-12-01" || *got.DueDate != "2026-12-03" ||
		got.DeliveryMilestoneId == nil || *got.DeliveryMilestoneId != milestones[0].Id {
		t.Errorf("first task = %+v", got)
	}
	if got.Checklist.Total != 2 || got.Checklist.Done != 0 {
		t.Errorf("first task checklist = %+v, want 2 open", got.Checklist)
	}
	if len(got.Subtasks) != 1 || got.Subtasks[0].Title != "Wireframes" || got.Subtasks[0].Checklist.Total != 1 {
		t.Errorf("subtasks = %+v", got.Subtasks)
	}
	deps := getTaskDependencies(t, c, q.Id)
	if len(deps) != 1 || deps[0].PredecessorTaskId != tasks[0].Id || deps[0].SuccessorTaskId != tasks[1].Id {
		t.Errorf("dependencies = %+v", deps)
	}
}

// Without a start date the anchor is the earliest date among the copied rows;
// without any date, every offset is absent and a milestone's is 0.
func TestProjectTemplates_SaveAsTemplate_Anchoring(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "SAVEANC1"})
	createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Review", "targetDate": "2026-11-15"})
	createTask(t, c, p.Id, map[string]any{"title": "Write", "startDate": "2026-11-12", "dueDate": "2026-11-14"})
	parent := createTask(t, c, p.Id, map[string]any{"title": "Ship"})
	createTask(t, c, p.Id, map[string]any{"title": "Earliest", "parentTaskId": parent.Id, "dueDate": "2026-11-10"})

	tmpl := saveAsTemplate(t, c, p.Id, map[string]any{"name": "Anchored"})
	if tmpl.Milestones[0].TargetDayOffset != 5 {
		t.Errorf("milestone offset = %d, want 5", tmpl.Milestones[0].TargetDayOffset)
	}
	if w := tmpl.Tasks[0]; *w.StartDayOffset != 2 || *w.DueDayOffset != 4 {
		t.Errorf("task offsets = %d, %d, want 2, 4", *w.StartDayOffset, *w.DueDayOffset)
	}
	if s := tmpl.Tasks[1].Subtasks[0]; s.DueDayOffset == nil || *s.DueDayOffset != 0 || s.StartDayOffset != nil {
		t.Errorf("subtask offsets = %v, %v, want none and 0", s.StartDayOffset, s.DueDayOffset)
	}

	undated := createProject(t, c, map[string]any{"code": "SAVEANC2"})
	createDeliveryMilestone(t, c, undated.Id, map[string]any{"name": "Someday"})
	createTask(t, c, undated.Id, map[string]any{"title": "Whenever"})
	tmpl = saveAsTemplate(t, c, undated.Id, map[string]any{"name": "Undated"})
	if tmpl.Milestones[0].TargetDayOffset != 0 || tmpl.Tasks[0].StartDayOffset != nil || tmpl.Tasks[0].DueDayOffset != nil {
		t.Errorf("undated snapshot = %+v / %+v", tmpl.Milestones, tmpl.Tasks)
	}
}

func TestProjectTemplates_SaveAsTemplate_Access(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h, "projects:create")
	p := createProject(t, owner, map[string]any{"code": "SAVEACC1"})
	body := map[string]any{"name": "Copy"}

	// A member who sees the project but cannot change the catalog.
	member, memberID := signIn(t, h)
	addRole(t, h, p.Id, memberID, "member")
	if r := member.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), body); r.Status != http.StatusForbidden {
		t.Errorf("without projects:create: status %d, want 403", r.Status)
	}

	// May change the catalog, but cannot see this project: the bare 404 an
	// unknown project gets.
	stranger, _ := signIn(t, h, "projects:create")
	r := stranger.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), body)
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unseen project: status %d body %q, want a bare 404", r.Status, r.Body)
	}
	if r := owner.Do(http.MethodPost, projectSaveAsTemplatePath(999999), body); r.Status != http.StatusNotFound {
		t.Errorf("unknown project: status %d, want 404", r.Status)
	}

	// A viewer with projects:create may snapshot what they can see.
	viewer, viewerID := signIn(t, h, "projects:create")
	addRole(t, h, p.Id, viewerID, "viewer")
	saveAsTemplate(t, viewer, p.Id, body)

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a blank name", map[string]any{"name": "  "}, "name"},
		{"a name over 200 characters", map[string]any{"name": strings.Repeat("n", 201)}, "name"},
		{"a description over 2000 characters", map[string]any{"name": "Ok", "description": strings.Repeat("d", 2001)}, "description"},
	} {
		r := owner.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), tc.body)
		var problem validationProblemJSON
		r.JSON(&problem)
		if r.Status != http.StatusBadRequest || len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%s: status %d errors %v, want 400 under %q", tc.name, r.Status, problem.Errors, tc.field)
		}
	}
}

// saveAsTemplateProblemJSON decodes the save-as-template refusal: the
// template's field errors, and the project row each is about.
type saveAsTemplateProblemJSON struct {
	Errors map[string][]string `json:"errors"`
	Rows   map[string]struct {
		Task      string `json:"task"`
		Subtask   string `json:"subtask"`
		Milestone string `json:"milestone"`
	} `json:"rows"`
}

// A plan that breaks a template rule is refused with the template's field
// errors rather than stored as a template apply would refuse, and each error
// names the project's own row, since tasks[1] is nothing a person can find.
func TestProjectTemplates_SaveAsTemplate_RefusesAPlanThatBreaksATemplateRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	p := createProject(t, c, map[string]any{"code": "SAVEBAD1"})
	createTask(t, c, p.Id, map[string]any{"title": "Fine"})
	parent := createTask(t, c, p.Id, map[string]any{"title": "Launch"})
	sub := createTask(t, c, p.Id, map[string]any{"title": "Long list", "parentTaskId": parent.Id})
	for i := range 101 {
		addChecklistItem(t, c, sub.Id, fmt.Sprintf("Item %d", i+1))
	}

	r := c.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), map[string]any{"name": "Too long"})
	var problem saveAsTemplateProblemJSON
	r.JSON(&problem)
	const field = "tasks[1].subtasks[0].checklist"
	if r.Status != http.StatusBadRequest || len(problem.Errors[field]) == 0 {
		t.Fatalf("status %d errors %v, want 400 under %s", r.Status, problem.Errors, field)
	}
	if row := problem.Rows[field]; row.Task != "Launch" || row.Subtask != "Long list" {
		t.Errorf("rows[%s] = %+v, want Launch / Long list", field, row)
	}
	res := c.Do(http.MethodGet, projectTemplatesPath(), nil)
	var list struct {
		Templates []templateJSON `json:"templates"`
	}
	res.JSON(&list)
	for _, tmpl := range list.Templates {
		if tmpl.Name == "Too long" {
			t.Errorf("a refused snapshot was stored")
		}
	}

	// A refusal of the request's own name names no row.
	r = c.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), map[string]any{"name": " "})
	problem = saveAsTemplateProblemJSON{}
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["name"]) == 0 || len(problem.Rows) != 0 {
		t.Errorf("blank name: status %d problem %+v, want 400 under name and no rows", r.Status, problem)
	}
}

// A chain longer than a project may now make — one linked before the cap
// existed — is refused naming the task the longest chain ends at.
func TestProjectTemplates_SaveAsTemplate_NamesTheEndOfATooLongChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")
	p := createProject(t, c, map[string]any{"code": "SAVEBAD2"})
	ids := make([]int32, 102)
	for i := range ids {
		ids[i] = createTask(t, c, p.Id, map[string]any{"title": fmt.Sprintf("Step %d", i)}).Id
	}
	// Behind the API, which would refuse the 101st link.
	for i := 1; i < len(ids); i++ {
		h.Exec(t, `INSERT INTO projects.task_dependencies (project_id, predecessor_task_id, successor_task_id, created_by_user_id)
			VALUES ($1, $2, $3, $4)`, p.Id, ids[i-1], ids[i], userID)
	}

	r := c.Do(http.MethodPost, projectSaveAsTemplatePath(p.Id), map[string]any{"name": "Chain"})
	var problem saveAsTemplateProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["dependencies"]) == 0 {
		t.Fatalf("status %d errors %v, want 400 under dependencies", r.Status, problem.Errors)
	}
	if row := problem.Rows["dependencies"]; row.Task != "Step 101" {
		t.Errorf("rows[dependencies] = %+v, want the chain's last task, Step 101", row)
	}
}

// A template's revision conflict names the template, not a project.
func TestProjectTemplates_RevisionConflictNamesTheTemplate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	tmpl := createProjectTemplate(t, c, map[string]any{"name": "Stale"})

	r := c.Do(http.MethodPut, projectTemplatePath(tmpl.Id), map[string]any{"name": "Stale", "revision": tmpl.Revision + 5})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d, want 409", r.Status)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("The template has revision %d; the supplied revision was %d.", tmpl.Revision, tmpl.Revision+5)
	if problem.Detail != want || problem.Title != "Template revision conflict" {
		t.Errorf("problem = %q / %q, want %q / %q", problem.Title, problem.Detail, "Template revision conflict", want)
	}
}

// A template is held to the dependency chain limit a hand-made link is.
func TestProjectTemplates_DependencyChainLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	chain := func(links int) map[string]any {
		tasks := make([]map[string]any, links+1)
		deps := make([]map[string]any, links)
		for i := range tasks {
			tasks[i] = map[string]any{"title": fmt.Sprintf("T%d", i)}
		}
		for i := range deps {
			deps[i] = map[string]any{"predecessorTaskIndex": i, "successorTaskIndex": i + 1}
		}
		return map[string]any{"name": "Chain", "tasks": tasks, "dependencies": deps}
	}
	createProjectTemplate(t, c, chain(100))

	r := c.Do(http.MethodPost, projectTemplatesPath(), chain(101))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["dependencies"]) == 0 {
		t.Errorf("101 links: status %d errors %v, want 400 under dependencies", r.Status, problem.Errors)
	}
}
