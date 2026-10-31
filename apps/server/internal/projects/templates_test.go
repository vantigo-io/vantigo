package projects_test

import (
	"net/http"
	"testing"
)

func TestProjectTemplates_CRUD(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	// 1. Create template
	payload := map[string]any{
		"name":        "Client Onboarding Blueprint",
		"description": "Standard 3-week onboarding process",
		"billingType": "fixed_price",
		"milestones": []map[string]any{
			{
				"name":            "Kickoff & Discovery",
				"description":     "Week 1 alignment",
				"targetDayOffset": 7,
			},
			{
				"name":            "Final Sign-off",
				"description":     "Handover complete",
				"targetDayOffset": 21,
			},
		},
		"tasks": []map[string]any{
			{
				"title":          "Discovery Call",
				"description":    "Meet key stakeholders",
				"startDayOffset": 1,
				"dueDayOffset":   3,
				"milestoneIndex": 0,
				"subtasks": []map[string]any{
					{
						"title":          "Prepare agenda",
						"startDayOffset": 1,
						"dueDayOffset":   1,
					},
				},
			},
			{
				"title":          "Solution Architecture",
				"description":    "Draft system design",
				"startDayOffset": 4,
				"dueDayOffset":   10,
				"milestoneIndex": 1,
			},
		},
		"dependencies": []map[string]any{
			{
				"predecessorTaskIndex": 0,
				"successorTaskIndex":   1,
			},
		},
	}

	tmpl := createProjectTemplate(t, c, payload)
	if tmpl.Id == 0 {
		t.Fatalf("expected template id > 0, got 0")
	}
	if tmpl.Name != "Client Onboarding Blueprint" {
		t.Errorf("tmpl.Name = %q, want 'Client Onboarding Blueprint'", tmpl.Name)
	}
	if tmpl.Description == nil || *tmpl.Description != "Standard 3-week onboarding process" {
		t.Errorf("tmpl.Description = %v", tmpl.Description)
	}
	if tmpl.BillingType == nil || *tmpl.BillingType != "fixed_price" {
		t.Errorf("tmpl.BillingType = %v", tmpl.BillingType)
	}
	if len(tmpl.Milestones) != 2 {
		t.Fatalf("len(milestones) = %d, want 2", len(tmpl.Milestones))
	}
	if tmpl.Milestones[0].TargetDayOffset != 7 {
		t.Errorf("milestone 0 offset = %d, want 7", tmpl.Milestones[0].TargetDayOffset)
	}
	if len(tmpl.Tasks) != 2 {
		t.Fatalf("len(tasks) = %d, want 2", len(tmpl.Tasks))
	}
	if len(tmpl.Tasks[0].Subtasks) != 1 {
		t.Fatalf("len(tasks[0].subtasks) = %d, want 1", len(tmpl.Tasks[0].Subtasks))
	}
	if len(tmpl.Dependencies) != 1 {
		t.Fatalf("len(dependencies) = %d, want 1", len(tmpl.Dependencies))
	}

	// 2. List templates
	res := c.Do(http.MethodGet, projectTemplatesPath(), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("list templates: status = %d, want 200", res.Status)
	}
	var listOut struct {
		Templates []templateJSON `json:"templates"`
	}
	res.JSON(&listOut)
	if len(listOut.Templates) < 1 {
		t.Errorf("list total = %d, want >= 1", len(listOut.Templates))
	}

	// 3. Get template by id
	fetched := getProjectTemplate(t, c, tmpl.Id)
	if fetched.Id != tmpl.Id || fetched.Name != tmpl.Name {
		t.Errorf("fetched template mismatch: got %+v, want %+v", fetched, tmpl)
	}

	// 4. Update template
	updatePayload := map[string]any{
		"name":         "Updated Blueprint",
		"description":  "Updated desc",
		"revision":     tmpl.Revision,
		"milestones":   tmpl.Milestones,
		"tasks":        tmpl.Tasks,
		"dependencies": tmpl.Dependencies,
	}
	res = c.Do(http.MethodPut, projectTemplatePath(tmpl.Id), updatePayload)
	if res.Status != http.StatusOK {
		t.Fatalf("update template: status = %d, want 200; body = %s", res.Status, string(res.Body))
	}
	var updated templateJSON
	res.JSON(&updated)
	if updated.Name != "Updated Blueprint" {
		t.Errorf("updated name = %q, want 'Updated Blueprint'", updated.Name)
	}
	if updated.Revision != tmpl.Revision+1 {
		t.Errorf("updated revision = %d, want %d", updated.Revision, tmpl.Revision+1)
	}

	// Revision conflict (409)
	res = c.Do(http.MethodPut, projectTemplatePath(tmpl.Id), updatePayload) // using old revision
	if res.Status != http.StatusConflict {
		t.Errorf("stale revision update status = %d, want 409", res.Status)
	}

	// 5. Delete template
	res = c.Do(http.MethodDelete, projectTemplatePath(tmpl.Id), nil)
	if res.Status != http.StatusNoContent {
		t.Fatalf("delete template status = %d, want 204", res.Status)
	}

	// 404 after deletion
	res = c.Do(http.MethodGet, projectTemplatePath(tmpl.Id), nil)
	if res.Status != http.StatusNotFound {
		t.Errorf("deleted template get status = %d, want 404", res.Status)
	}

	// 404 delete unknown
	res = c.Do(http.MethodDelete, projectTemplatePath(999999), nil)
	if res.Status != http.StatusNotFound {
		t.Errorf("delete unknown template status = %d, want 404", res.Status)
	}

	// 404 update unknown
	res = c.Do(http.MethodPut, projectTemplatePath(999999), map[string]any{
		"name":     "Nonexistent",
		"revision": 1,
	})
	if res.Status != http.StatusNotFound {
		t.Errorf("update unknown template status = %d, want 404", res.Status)
	}
}

func TestProjectTemplates_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	// Empty name
	res := c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "   ",
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", res.Status)
	}

	// Negative milestone targetDayOffset
	res = c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "Invalid Milestone",
		"milestones": []map[string]any{
			{"name": "M1", "targetDayOffset": -5},
		},
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("negative milestone targetDayOffset status = %d, want 400", res.Status)
	}

	// Due day offset before start day offset
	res = c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "Invalid Task Dates",
		"tasks": []map[string]any{
			{"title": "Task 1", "startDayOffset": 10, "dueDayOffset": 5},
		},
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("dueDayOffset < startDayOffset status = %d, want 400", res.Status)
	}

	// Invalid milestoneIndex out-of-bounds
	res = c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "Invalid Milestone Index",
		"tasks": []map[string]any{
			{"title": "Task 1", "milestoneIndex": 3},
		},
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("milestoneIndex out of bounds status = %d, want 400", res.Status)
	}

	// Dependency referencing out-of-bounds task index
	res = c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "Invalid Dep Index",
		"tasks": []map[string]any{
			{"title": "Task 1"},
		},
		"dependencies": []map[string]any{
			{"predecessorTaskIndex": 0, "successorTaskIndex": 5},
		},
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("dependency out of bounds status = %d, want 400", res.Status)
	}

	// Self-referencing dependency
	res = c.Do(http.MethodPost, projectTemplatesPath(), map[string]any{
		"name": "Self Dep",
		"tasks": []map[string]any{
			{"title": "Task 1"},
		},
		"dependencies": []map[string]any{
			{"predecessorTaskIndex": 0, "successorTaskIndex": 0},
		},
	})
	if res.Status != http.StatusBadRequest {
		t.Errorf("self dependency status = %d, want 400", res.Status)
	}
}

func TestProjectTemplates_ApplyTemplate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")

	// Create a blueprint template
	tmpl := createProjectTemplate(t, c, map[string]any{
		"name": "SaaS Launch Blueprint",
		"milestones": []map[string]any{
			{
				"name":            "Phase 1: Foundations",
				"description":     "Core infrastructure",
				"targetDayOffset": 7,
			},
			{
				"name":            "Phase 2: Launch",
				"description":     "Public release",
				"targetDayOffset": 20,
			},
		},
		"tasks": []map[string]any{
			{
				"title":          "Setup CI/CD",
				"description":    "GitHub actions and docker",
				"startDayOffset": 0,
				"dueDayOffset":   3,
				"milestoneIndex": 0,
				"subtasks": []map[string]any{
					{
						"title":          "Deploy staging runner",
						"startDayOffset": 1,
						"dueDayOffset":   2,
					},
				},
			},
			{
				"title":          "Marketing Campaign",
				"description":    "Socials and press release",
				"startDayOffset": 5,
				"dueDayOffset":   15,
				"milestoneIndex": 1,
			},
		},
		"dependencies": []map[string]any{
			{
				"predecessorTaskIndex": 0,
				"successorTaskIndex":   1,
			},
		},
	})

	// Create a project
	p := createProject(t, c, map[string]any{
		"code":      "APPTMP1",
		"name":      "Template Applied Project",
		"startDate": "2026-11-01",
	})

	// Apply template with explicit kickoff date 2026-11-10
	kickoff := "2026-11-10"
	res := c.Do(http.MethodPost, projectApplyTemplatePath(p.Id), map[string]any{
		"templateId":  tmpl.Id,
		"kickoffDate": kickoff,
	})
	if res.Status != http.StatusOK {
		t.Fatalf("apply template status = %d, want 200; body = %s", res.Status, string(res.Body))
	}

	var applyResult struct {
		MilestonesCreated   int32   `json:"milestonesCreated"`
		TasksCreated        int32   `json:"tasksCreated"`
		DependenciesCreated int32   `json:"dependenciesCreated"`
		CreatedMilestoneIds []int32 `json:"createdMilestoneIds"`
		CreatedTaskIds      []int32 `json:"createdTaskIds"`
	}
	res.JSON(&applyResult)

	if applyResult.MilestonesCreated != 2 {
		t.Errorf("milestonesCreated = %d, want 2", applyResult.MilestonesCreated)
	}
	// 2 top-level tasks + 1 subtask = 3 tasks created
	if applyResult.TasksCreated != 3 {
		t.Errorf("tasksCreated = %d, want 3", applyResult.TasksCreated)
	}
	if applyResult.DependenciesCreated != 1 {
		t.Errorf("dependenciesCreated = %d, want 1", applyResult.DependenciesCreated)
	}

	// Verify Delivery Milestones created on project
	milestones := getDeliveryMilestones(t, c, p.Id)
	if len(milestones) != 2 {
		t.Fatalf("len(milestones) = %d, want 2", len(milestones))
	}
	// Milestone 0: targetDayOffset 7 -> 2026-11-10 + 7 days = 2026-11-17
	if milestones[0].Name != "Phase 1: Foundations" {
		t.Errorf("milestones[0].Name = %q", milestones[0].Name)
	}
	if milestones[0].TargetDate == nil || *milestones[0].TargetDate != "2026-11-17" {
		t.Errorf("milestones[0].TargetDate = %v, want 2026-11-17", milestones[0].TargetDate)
	}
	// Milestone 1: targetDayOffset 20 -> 2026-11-10 + 20 days = 2026-11-30
	if milestones[1].Name != "Phase 2: Launch" {
		t.Errorf("milestones[1].Name = %q", milestones[1].Name)
	}
	if milestones[1].TargetDate == nil || *milestones[1].TargetDate != "2026-11-30" {
		t.Errorf("milestones[1].TargetDate = %v, want 2026-11-30", milestones[1].TargetDate)
	}

	// Verify Tasks created on project
	tasks := getTasks(t, c, p.Id)
	if len(tasks) != 2 {
		t.Fatalf("len(top-level tasks) = %d, want 2", len(tasks))
	}

	// Find top-level task 0: "Setup CI/CD"
	var t0, t1, sub *taskJSON
	for i := range tasks {
		tk := &tasks[i]
		if tk.Title == "Setup CI/CD" {
			t0 = tk
			if len(tk.Subtasks) > 0 {
				sub = &tk.Subtasks[0]
			}
		} else if tk.Title == "Marketing Campaign" {
			t1 = tk
		}
	}

	if t0 == nil || t1 == nil || sub == nil {
		t.Fatalf("missing expected tasks: t0=%v, t1=%v, sub=%v", t0, t1, sub)
	}

	// Task 0: start 2026-11-10, due 2026-11-13 (kickoff + 3 days)
	if t0.StartDate == nil || *t0.StartDate != "2026-11-10" {
		t.Errorf("t0.StartDate = %v, want 2026-11-10", t0.StartDate)
	}
	if t0.DueDate == nil || *t0.DueDate != "2026-11-13" {
		t.Errorf("t0.DueDate = %v, want 2026-11-13", t0.DueDate)
	}
	if t0.DeliveryMilestoneId == nil || *t0.DeliveryMilestoneId != milestones[0].Id {
		t.Errorf("t0.DeliveryMilestoneId = %v, want %d", t0.DeliveryMilestoneId, milestones[0].Id)
	}

	// Subtask: parentID == t0.ID, start 2026-11-11, due 2026-11-12
	if sub.ParentTaskId == nil || *sub.ParentTaskId != t0.Id {
		t.Errorf("sub.ParentTaskId = %v, want %d", sub.ParentTaskId, t0.Id)
	}
	if sub.StartDate == nil || *sub.StartDate != "2026-11-11" {
		t.Errorf("sub.StartDate = %v, want 2026-11-11", sub.StartDate)
	}
	if sub.DueDate == nil || *sub.DueDate != "2026-11-12" {
		t.Errorf("sub.DueDate = %v, want 2026-11-12", sub.DueDate)
	}

	// Task 1: start 2026-11-15, due 2026-11-25
	if t1.StartDate == nil || *t1.StartDate != "2026-11-15" {
		t.Errorf("t1.StartDate = %v, want 2026-11-15", t1.StartDate)
	}
	if t1.DueDate == nil || *t1.DueDate != "2026-11-25" {
		t.Errorf("t1.DueDate = %v, want 2026-11-25", t1.DueDate)
	}
	if t1.DeliveryMilestoneId == nil || *t1.DeliveryMilestoneId != milestones[1].Id {
		t.Errorf("t1.DeliveryMilestoneId = %v, want %d", t1.DeliveryMilestoneId, milestones[1].Id)
	}

	// Verify Dependencies created
	deps := getTaskDependencies(t, c, p.Id)
	if len(deps) != 1 {
		t.Fatalf("len(deps) = %d, want 1", len(deps))
	}
	if deps[0].PredecessorTaskId != t0.Id || deps[0].SuccessorTaskId != t1.Id {
		t.Errorf("dependency mismatch: pred=%d, succ=%d; want pred=%d, succ=%d",
			deps[0].PredecessorTaskId, deps[0].SuccessorTaskId, t0.Id, t1.Id)
	}

	// 404 for unknown project
	res = c.Do(http.MethodPost, projectApplyTemplatePath(999999), map[string]any{
		"templateId":  tmpl.Id,
		"kickoffDate": "2026-11-10",
	})
	if res.Status != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", res.Status)
	}

	// 404 for unknown template
	res = c.Do(http.MethodPost, projectApplyTemplatePath(p.Id), map[string]any{
		"templateId":  999999,
		"kickoffDate": "2026-11-10",
	})
	if res.Status != http.StatusNotFound {
		t.Errorf("unknown template status = %d, want 404", res.Status)
	}

	// 403 when user cannot contribute to project
	viewer, viewerID := signIn(t, h)
	addRole(t, h, p.Id, viewerID, "viewer")
	res = viewer.Do(http.MethodPost, projectApplyTemplatePath(p.Id), map[string]any{
		"templateId":  tmpl.Id,
		"kickoffDate": "2026-11-10",
	})
	if res.Status != http.StatusForbidden {
		t.Errorf("viewer without contribute status = %d, want 403", res.Status)
	}
}
