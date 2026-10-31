package projects_test

import (
	"net/http"
	"testing"
)

func TestTaskDependencies_CreateAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DEPS1", "name": "Dependencies Project"})

	t1 := createTask(t, c, p.Id, map[string]any{"title": "Task 1"})
	t2 := createTask(t, c, p.Id, map[string]any{"title": "Task 2"})
	t3 := createTask(t, c, p.Id, map[string]any{"title": "Task 3"})

	// Initially empty
	deps := getTaskDependencies(t, c, p.Id)
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependencies initially, got %d", len(deps))
	}

	// Create t1 -> t2
	d1 := createTaskDependency(t, c, p.Id, t1.Id, t2.Id)
	if d1.ProjectId != p.Id || d1.PredecessorTaskId != t1.Id || d1.SuccessorTaskId != t2.Id {
		t.Fatalf("unexpected dependency: %+v", d1)
	}

	// Create t2 -> t3
	d2 := createTaskDependency(t, c, p.Id, t2.Id, t3.Id)
	if d2.ProjectId != p.Id || d2.PredecessorTaskId != t2.Id || d2.SuccessorTaskId != t3.Id {
		t.Fatalf("unexpected dependency: %+v", d2)
	}

	// List dependencies
	deps = getTaskDependencies(t, c, p.Id)
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(deps))
	}
	if deps[0].Id != d1.Id || deps[1].Id != d2.Id {
		t.Errorf("deps ids = [%d, %d], want [%d, %d]", deps[0].Id, deps[1].Id, d1.Id, d2.Id)
	}
}

func TestTaskDependencies_ValidationAndCycleDetection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p1 := createProject(t, c, map[string]any{"code": "DEPC1", "name": "Cycle Project 1"})
	p2 := createProject(t, c, map[string]any{"code": "DEPC2", "name": "Cycle Project 2"})

	t1 := createTask(t, c, p1.Id, map[string]any{"title": "Task 1"})
	t2 := createTask(t, c, p1.Id, map[string]any{"title": "Task 2"})
	t3 := createTask(t, c, p1.Id, map[string]any{"title": "Task 3"})
	tOther := createTask(t, c, p2.Id, map[string]any{"title": "Task Other"})

	// Self-dependency: t1 -> t1
	res := c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": t1.Id,
		"successorTaskId":   t1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("self-dependency status = %d, want 400", res.Status)
	}

	// Task from another project
	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": t1.Id,
		"successorTaskId":   tOther.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("cross-project successor status = %d, want 400", res.Status)
	}

	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": tOther.Id,
		"successorTaskId":   t1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("cross-project predecessor status = %d, want 400", res.Status)
	}

	// Non-existent task
	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": 999999,
		"successorTaskId":   t1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("non-existent predecessor status = %d, want 400", res.Status)
	}

	// Create t1 -> t2 and t2 -> t3
	_ = createTaskDependency(t, c, p1.Id, t1.Id, t2.Id)
	_ = createTaskDependency(t, c, p1.Id, t2.Id, t3.Id)

	// Duplicate dependency: t1 -> t2
	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": t1.Id,
		"successorTaskId":   t2.Id,
	})
	if res.Status != http.StatusConflict {
		t.Errorf("duplicate dependency status = %d, want 409", res.Status)
	}

	// Direct cycle: t2 -> t1
	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": t2.Id,
		"successorTaskId":   t1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("direct cycle status = %d, want 400", res.Status)
	}

	// Transitive cycle: t3 -> t1
	res = c.Do(http.MethodPost, projectDependenciesPath(p1.Id), map[string]any{
		"predecessorTaskId": t3.Id,
		"successorTaskId":   t1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("transitive cycle status = %d, want 400", res.Status)
	}
}

func TestTaskDependencies_DeleteAndCascades(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DEPD1", "name": "Delete Project"})
	t1 := createTask(t, c, p.Id, map[string]any{"title": "Task 1"})
	t2 := createTask(t, c, p.Id, map[string]any{"title": "Task 2"})
	t3 := createTask(t, c, p.Id, map[string]any{"title": "Task 3"})

	d1 := createTaskDependency(t, c, p.Id, t1.Id, t2.Id)
	d2 := createTaskDependency(t, c, p.Id, t2.Id, t3.Id)

	// Delete d1
	deleteTaskDependency(t, c, d1.Id)

	deps := getTaskDependencies(t, c, p.Id)
	if len(deps) != 1 || deps[0].Id != d2.Id {
		t.Fatalf("expected only d2 to remain, got %+v", deps)
	}

	// Delete non-existent dependency -> 404
	res := c.Do(http.MethodDelete, projectDependencyPath(999999), nil)
	if res.Status != http.StatusNotFound {
		t.Errorf("delete unknown dependency status = %d, want 404", res.Status)
	}

	// Task deletion cascades to task_dependencies
	deleteTask(t, c, t2.Id)

	deps = getTaskDependencies(t, c, p.Id)
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependencies after deleting task 2, got %d", len(deps))
	}
}

func TestTaskDependencies_RescheduleDownstream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DEPR1", "name": "Reschedule Project"})

	startDate := "2026-11-01"
	dueDate := "2026-11-05"

	t1 := createTask(t, c, p.Id, map[string]any{
		"title":     "Task 1",
		"startDate": startDate,
		"dueDate":   dueDate,
	})
	t2 := createTask(t, c, p.Id, map[string]any{
		"title":     "Task 2",
		"startDate": "2026-11-06",
		"dueDate":   "2026-11-10",
	})
	t3 := createTask(t, c, p.Id, map[string]any{
		"title":     "Task 3",
		"startDate": "2026-11-11",
		"dueDate":   "2026-11-15",
	})
	tUnrelated := createTask(t, c, p.Id, map[string]any{
		"title":     "Unrelated Task",
		"startDate": "2026-11-01",
		"dueDate":   "2026-11-05",
	})

	_ = createTaskDependency(t, c, p.Id, t1.Id, t2.Id)
	_ = createTaskDependency(t, c, p.Id, t2.Id, t3.Id)

	// Reschedule downstream from t1 by +3 days
	shiftedCount, updatedIDs := rescheduleDownstreamTasks(t, c, t1.Id, 3)
	if shiftedCount != 2 {
		t.Fatalf("expected 2 shifted tasks, got %d", shiftedCount)
	}
	if len(updatedIDs) != 2 {
		t.Fatalf("expected 2 updated IDs, got %v", updatedIDs)
	}

	// Check t2 shifted by 3 days: 2026-11-06 -> 2026-11-09, 2026-11-10 -> 2026-11-13
	updatedT2 := getTask(t, c, t2.Id)
	if updatedT2.StartDate == nil || *updatedT2.StartDate != "2026-11-09" {
		t.Errorf("t2 startDate = %v, want 2026-11-09", updatedT2.StartDate)
	}
	if updatedT2.DueDate == nil || *updatedT2.DueDate != "2026-11-13" {
		t.Errorf("t2 dueDate = %v, want 2026-11-13", updatedT2.DueDate)
	}

	// Check t3 shifted by 3 days: 2026-11-11 -> 2026-11-14, 2026-11-15 -> 2026-11-18
	updatedT3 := getTask(t, c, t3.Id)
	if updatedT3.StartDate == nil || *updatedT3.StartDate != "2026-11-14" {
		t.Errorf("t3 startDate = %v, want 2026-11-14", updatedT3.StartDate)
	}
	if updatedT3.DueDate == nil || *updatedT3.DueDate != "2026-11-18" {
		t.Errorf("t3 dueDate = %v, want 2026-11-18", updatedT3.DueDate)
	}

	// Check t1 and tUnrelated were NOT shifted
	updatedT1 := getTask(t, c, t1.Id)
	if updatedT1.StartDate == nil || *updatedT1.StartDate != startDate {
		t.Errorf("t1 startDate = %v, want %s", updatedT1.StartDate, startDate)
	}
	updatedUnrelated := getTask(t, c, tUnrelated.Id)
	if updatedUnrelated.StartDate == nil || *updatedUnrelated.StartDate != "2026-11-01" {
		t.Errorf("tUnrelated startDate = %v, want 2026-11-01", updatedUnrelated.StartDate)
	}

	// Reschedule with daysDelta = 0 returns 0 shifted
	count0, ids0 := rescheduleDownstreamTasks(t, c, t1.Id, 0)
	if count0 != 0 || len(ids0) != 0 {
		t.Errorf("daysDelta=0: count = %d, ids = %v, want 0 and []", count0, ids0)
	}

	// Reschedule downstream from leaf task (t3) has 0 downstream tasks
	countLeaf, _ := rescheduleDownstreamTasks(t, c, t3.Id, 2)
	if countLeaf != 0 {
		t.Errorf("leaf task downstream count = %d, want 0", countLeaf)
	}
}

func TestTaskDependencies_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "projects:create")

	p := createProject(t, admin, map[string]any{"code": "DEPP1", "name": "Perms Project"})
	t1 := createTask(t, admin, p.Id, map[string]any{"title": "Task 1"})
	t2 := createTask(t, admin, p.Id, map[string]any{"title": "Task 2"})
	dep := createTaskDependency(t, admin, p.Id, t1.Id, t2.Id)

	// Outsider cannot see project -> 404
	outsider, outsiderID := signIn(t, h)
	res := outsider.Do(http.MethodGet, projectDependenciesPath(p.Id), nil)
	if res.Status != http.StatusNotFound {
		t.Errorf("outsider list status = %d, want 404", res.Status)
	}

	// Add outsider as viewer
	addRole(t, h, p.Id, outsiderID, "viewer")

	// Viewer can read dependencies (200)
	res = outsider.Do(http.MethodGet, projectDependenciesPath(p.Id), nil)
	if res.Status != http.StatusOK {
		t.Errorf("viewer list status = %d, want 200", res.Status)
	}

	// Viewer cannot create dependency (403)
	res = outsider.Do(http.MethodPost, projectDependenciesPath(p.Id), map[string]any{
		"predecessorTaskId": t1.Id,
		"successorTaskId":   t2.Id,
	})
	if res.Status != http.StatusForbidden {
		t.Errorf("viewer create status = %d, want 403", res.Status)
	}

	// Viewer cannot delete dependency (403)
	res = outsider.Do(http.MethodDelete, projectDependencyPath(dep.Id), nil)
	if res.Status != http.StatusForbidden {
		t.Errorf("viewer delete status = %d, want 403", res.Status)
	}

	// Viewer cannot reschedule downstream (403)
	res = outsider.Do(http.MethodPost, taskRescheduleDownstreamPath(t1.Id), map[string]any{
		"daysDelta": 2,
	})
	if res.Status != http.StatusForbidden {
		t.Errorf("viewer reschedule status = %d, want 403", res.Status)
	}
}
