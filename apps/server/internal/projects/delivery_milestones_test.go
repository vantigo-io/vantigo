package projects_test

import (
	"net/http"
	"testing"
)

func TestPostProjectsByIdDeliveryMilestones_CreatesAppendedLast(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL1", "name": "Milestone Project"})

	m1 := createDeliveryMilestone(t, c, p.Id, map[string]any{
		"name":        "Phase 1 MVP",
		"description": "Initial deliverable for testing",
		"targetDate":  "2026-11-15",
	})

	if m1.Name != "Phase 1 MVP" {
		t.Errorf("m1.Name = %q, want %q", m1.Name, "Phase 1 MVP")
	}
	if m1.Position != 1 {
		t.Errorf("m1.Position = %d, want 1", m1.Position)
	}
	if m1.Status != "open" {
		t.Errorf("m1.Status = %q, want %q", m1.Status, "open")
	}
	if m1.TotalTasks != 0 || m1.CompletedTasks != 0 || m1.ProgressPercent != 0 {
		t.Errorf("m1 progress = %d/%d (%d%%), want 0/0 (0%%)", m1.CompletedTasks, m1.TotalTasks, m1.ProgressPercent)
	}

	m2 := createDeliveryMilestone(t, c, p.Id, map[string]any{
		"name": "Phase 2 Production",
	})
	if m2.Position != 2 {
		t.Errorf("m2.Position = %d, want 2", m2.Position)
	}

	// Validation errors
	res := c.Do(http.MethodPost, deliveryMilestonesPath(p.Id), map[string]any{"name": "   "})
	if res.Status != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", res.Status)
	}

	// 404 for unknown project
	res = c.Do(http.MethodPost, deliveryMilestonesPath(999999), map[string]any{"name": "Ghost"})
	if res.Status != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", res.Status)
	}

	// 403 for member without contribute (e.g. viewer)
	viewer, viewerID := signIn(t, h)
	res = viewer.Do(http.MethodPost, deliveryMilestonesPath(p.Id), map[string]any{"name": "Forbidden"})
	if res.Status != http.StatusNotFound { // Viewer cannot see the project yet -> 404
		t.Errorf("viewer cannot see project status = %d, want 404", res.Status)
	}
	addRole(t, h, p.Id, viewerID, "viewer")
	res = viewer.Do(http.MethodPost, deliveryMilestonesPath(p.Id), map[string]any{"name": "Forbidden"})
	if res.Status != http.StatusForbidden {
		t.Errorf("viewer cannot contribute status = %d, want 403", res.Status)
	}
}

func TestGetProjectsByIdDeliveryMilestones_ListAndTaskProgress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL2", "name": "Progress Project"})
	m := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Deliverable Alpha"})

	// Create two tasks linked to milestone: one todo, one done
	t1 := createTask(t, c, p.Id, map[string]any{
		"title":               "Task 1",
		"status":              "todo",
		"estimateHours":       4.0,
		"deliveryMilestoneId": m.Id,
	})
	if t1.DeliveryMilestoneId == nil || *t1.DeliveryMilestoneId != m.Id {
		t.Errorf("t1.DeliveryMilestoneId = %v, want %d", t1.DeliveryMilestoneId, m.Id)
	}
	if t1.DeliveryMilestone == nil || t1.DeliveryMilestone.Name != "Deliverable Alpha" {
		t.Errorf("t1.DeliveryMilestone = %+v, want summary for %q", t1.DeliveryMilestone, "Deliverable Alpha")
	}

	_ = createTask(t, c, p.Id, map[string]any{
		"title":               "Task 2",
		"status":              "done",
		"estimateHours":       6.0,
		"deliveryMilestoneId": m.Id,
	})

	milestones := getDeliveryMilestones(t, c, p.Id)
	if len(milestones) != 1 {
		t.Fatalf("len(milestones) = %d, want 1", len(milestones))
	}
	got := milestones[0]
	if got.TotalTasks != 2 {
		t.Errorf("got.TotalTasks = %d, want 2", got.TotalTasks)
	}
	if got.CompletedTasks != 1 {
		t.Errorf("got.CompletedTasks = %d, want 1", got.CompletedTasks)
	}
	if got.ProgressPercent != 50 {
		t.Errorf("got.ProgressPercent = %d, want 50", got.ProgressPercent)
	}
	if got.EstimatedHoursTotal == nil || *got.EstimatedHoursTotal != 10.0 {
		t.Errorf("got.EstimatedHoursTotal = %v, want 10.0", got.EstimatedHoursTotal)
	}
	if got.EstimatedHoursCompleted == nil || *got.EstimatedHoursCompleted != 6.0 {
		t.Errorf("got.EstimatedHoursCompleted = %v, want 6.0", got.EstimatedHoursCompleted)
	}

	// Single get
	single := getDeliveryMilestone(t, c, m.Id)
	if single.ProgressPercent != 50 {
		t.Errorf("single.ProgressPercent = %d, want 50", single.ProgressPercent)
	}
}

func TestPutProjectsDeliveryMilestonesByMilestoneId_UpdatesAndConcurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL3", "name": "Update Milestone"})
	m := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Initial Name"})

	// Successful update
	res := c.Do(http.MethodPut, deliveryMilestonePath(m.Id), map[string]any{
		"name":        "Updated Name",
		"description": "New description",
		"targetDate":  "2026-12-01",
		"revision":    m.Revision,
	})
	if res.Status != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body = %s", res.Status, string(res.Body))
	}
	var updated deliveryMilestoneJSON
	res.JSON(&updated)
	if updated.Name != "Updated Name" {
		t.Errorf("updated.Name = %q, want %q", updated.Name, "Updated Name")
	}
	if updated.Revision != m.Revision+1 {
		t.Errorf("updated.Revision = %d, want %d", updated.Revision, m.Revision+1)
	}

	// Stale revision -> 409
	resStale := c.Do(http.MethodPut, deliveryMilestonePath(m.Id), map[string]any{
		"name":     "Stale Attempt",
		"revision": m.Revision, // Stale!
	})
	if resStale.Status != http.StatusConflict {
		t.Errorf("stale update status = %d, want 409", resStale.Status)
	}
}

func TestPutProjectsDeliveryMilestonesByMilestoneIdPosition_Reorders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL4", "name": "Reorder Milestone"})
	m1 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "First"})
	m2 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Second"})
	m3 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Third"})

	// Move Third to position 1
	res := c.Do(http.MethodPut, deliveryMilestonePositionPath(m3.Id), map[string]any{
		"position": 1,
		"revision": m3.Revision,
	})
	if res.Status != http.StatusOK {
		t.Fatalf("position status = %d, want 200; body = %s", res.Status, string(res.Body))
	}

	milestones := getDeliveryMilestones(t, c, p.Id)
	if len(milestones) != 3 {
		t.Fatalf("len(milestones) = %d, want 3", len(milestones))
	}
	if milestones[0].Id != m3.Id || milestones[0].Position != 1 {
		t.Errorf("milestones[0] = id %d, pos %d, want m3 (pos 1)", milestones[0].Id, milestones[0].Position)
	}
	if milestones[1].Id != m1.Id || milestones[1].Position != 2 {
		t.Errorf("milestones[1] = id %d, pos %d, want m1 (pos 2)", milestones[1].Id, milestones[1].Position)
	}
	if milestones[2].Id != m2.Id || milestones[2].Position != 3 {
		t.Errorf("milestones[2] = id %d, pos %d, want m2 (pos 3)", milestones[2].Id, milestones[2].Position)
	}

	// Invalid position 0 -> 400
	resBad := c.Do(http.MethodPut, deliveryMilestonePositionPath(m3.Id), map[string]any{
		"position": 0,
		"revision": milestones[0].Revision,
	})
	if resBad.Status != http.StatusBadRequest {
		t.Errorf("position 0 status = %d, want 400", resBad.Status)
	}
}

func TestPostProjectsDeliveryMilestonesByMilestoneIdStatus_ToggleStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, aliceID := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL5", "name": "Status Milestone"})
	m := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Sprint 1"})

	// Mark completed
	res := c.Do(http.MethodPost, deliveryMilestoneStatusPath(m.Id), map[string]any{
		"status":   "completed",
		"revision": m.Revision,
	})
	if res.Status != http.StatusOK {
		t.Fatalf("status completed code = %d, want 200; body = %s", res.Status, string(res.Body))
	}
	var comp deliveryMilestoneJSON
	res.JSON(&comp)
	if comp.Status != "completed" {
		t.Errorf("comp.Status = %q, want %q", comp.Status, "completed")
	}
	if comp.CompletedAt == nil {
		t.Errorf("comp.CompletedAt is nil, want timestamp")
	}
	if comp.CompletedBy == nil || comp.CompletedBy.Id != aliceID {
		t.Errorf("comp.CompletedBy = %+v, want alice (%s)", comp.CompletedBy, aliceID)
	}

	// Reopen to open
	resReopen := c.Do(http.MethodPost, deliveryMilestoneStatusPath(m.Id), map[string]any{
		"status":   "open",
		"revision": comp.Revision,
	})
	if resReopen.Status != http.StatusOK {
		t.Fatalf("status open code = %d, want 200; body = %s", resReopen.Status, string(resReopen.Body))
	}
	var reopened deliveryMilestoneJSON
	resReopen.JSON(&reopened)
	if reopened.Status != "open" {
		t.Errorf("reopened.Status = %q, want %q", reopened.Status, "open")
	}
	if reopened.CompletedAt != nil {
		t.Errorf("reopened.CompletedAt = %v, want nil", reopened.CompletedAt)
	}

	// Invalid status -> 400
	resBad := c.Do(http.MethodPost, deliveryMilestoneStatusPath(m.Id), map[string]any{
		"status":   "invalid-status",
		"revision": reopened.Revision,
	})
	if resBad.Status != http.StatusBadRequest {
		t.Errorf("invalid status code = %d, want 400", resBad.Status)
	}
}

func TestDeleteProjectsDeliveryMilestonesByMilestoneId_UnlinksTasksAndRenumbers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p := createProject(t, c, map[string]any{"code": "DMIL6", "name": "Delete Milestone"})
	m1 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Delete Me"})
	m2 := createDeliveryMilestone(t, c, p.Id, map[string]any{"name": "Keep Me"})

	// Create a task linked to m1
	task := createTask(t, c, p.Id, map[string]any{
		"title":               "Linked Task",
		"deliveryMilestoneId": m1.Id,
	})
	if task.DeliveryMilestoneId == nil || *task.DeliveryMilestoneId != m1.Id {
		t.Fatalf("task.DeliveryMilestoneId = %v, want %d", task.DeliveryMilestoneId, m1.Id)
	}

	// Delete m1
	resDel := c.Do(http.MethodDelete, deliveryMilestonePath(m1.Id), nil)
	if resDel.Status != http.StatusNoContent {
		t.Fatalf("delete code = %d, want 204; body = %s", resDel.Status, string(resDel.Body))
	}

	// Deleting again -> 404
	resDelAgain := c.Do(http.MethodDelete, deliveryMilestonePath(m1.Id), nil)
	if resDelAgain.Status != http.StatusNotFound {
		t.Errorf("delete again code = %d, want 404", resDelAgain.Status)
	}

	// Verify task is still there and unlinked (deliveryMilestoneId = nil)
	resTask := c.Do(http.MethodGet, taskPath(task.Id), nil)
	if resTask.Status != http.StatusOK {
		t.Fatalf("get task code = %d, want 200", resTask.Status)
	}
	var taskResp taskJSON
	resTask.JSON(&taskResp)
	if taskResp.DeliveryMilestoneId != nil {
		t.Errorf("task.DeliveryMilestoneId = %v, want nil after milestone deletion", taskResp.DeliveryMilestoneId)
	}

	// Verify remaining milestone is renumbered to position 1
	remaining := getDeliveryMilestones(t, c, p.Id)
	if len(remaining) != 1 {
		t.Fatalf("len(remaining) = %d, want 1", len(remaining))
	}
	if remaining[0].Id != m2.Id || remaining[0].Position != 1 {
		t.Errorf("remaining milestone = id %d, pos %d, want m2 (pos 1)", remaining[0].Id, remaining[0].Position)
	}
}

func TestTasksDeliveryMilestoneMismatch_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	p1 := createProject(t, c, map[string]any{"code": "PROJ1", "name": "Project 1"})
	p2 := createProject(t, c, map[string]any{"code": "PROJ2", "name": "Project 2"})

	m1 := createDeliveryMilestone(t, c, p1.Id, map[string]any{"name": "Project 1 Milestone"})

	// Trying to create a task on p2 with m1 (from p1) -> 400
	res := c.Do(http.MethodPost, tasksPath(p2.Id), map[string]any{
		"title":               "Invalid Task",
		"deliveryMilestoneId": m1.Id,
	})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("create task mismatched milestone code = %d, want 400; body = %s", res.Status, string(res.Body))
	}
	var prob validationProblemJSON
	res.JSON(&prob)
	if errs, ok := prob.Errors["deliveryMilestoneId"]; !ok || len(errs) == 0 {
		t.Errorf("expected field error on deliveryMilestoneId, got %+v", prob.Errors)
	}

	// Trying with non-existent milestone ID -> 400
	resNonExistent := c.Do(http.MethodPost, tasksPath(p1.Id), map[string]any{
		"title":               "Invalid Task 2",
		"deliveryMilestoneId": 999999,
	})
	if resNonExistent.Status != http.StatusBadRequest {
		t.Fatalf("create task non-existent milestone code = %d, want 400", resNonExistent.Status)
	}

	// Updating a task to clear milestone (null) works
	taskP1 := createTask(t, c, p1.Id, map[string]any{
		"title":               "Task P1",
		"deliveryMilestoneId": m1.Id,
	})
	resUpdate := c.Do(http.MethodPut, taskPath(taskP1.Id), map[string]any{
		"title":               taskP1.Title,
		"status":              taskP1.Status,
		"revision":            taskP1.Revision,
		"deliveryMilestoneId": nil, // clear milestone
	})
	if resUpdate.Status != http.StatusOK {
		t.Fatalf("update task clear milestone code = %d, want 200; body = %s", resUpdate.Status, string(resUpdate.Body))
	}
	var updatedTask taskJSON
	resUpdate.JSON(&updatedTask)
	if updatedTask.DeliveryMilestoneId != nil {
		t.Errorf("updatedTask.DeliveryMilestoneId = %v, want nil", updatedTask.DeliveryMilestoneId)
	}
}
