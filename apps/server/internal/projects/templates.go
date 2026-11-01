package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

func templateResponseFromRow(r store.ProjectsTemplate) (gen.ProjectTemplateResponse, error) {
	var milestones []gen.ProjectTemplateMilestone
	if len(r.Milestones) > 0 {
		if err := json.Unmarshal(r.Milestones, &milestones); err != nil {
			return gen.ProjectTemplateResponse{}, fmt.Errorf("projects: unmarshal template milestones: %w", err)
		}
	}
	if milestones == nil {
		milestones = []gen.ProjectTemplateMilestone{}
	}

	var tasks []gen.ProjectTemplateTask
	if len(r.Tasks) > 0 {
		if err := json.Unmarshal(r.Tasks, &tasks); err != nil {
			return gen.ProjectTemplateResponse{}, fmt.Errorf("projects: unmarshal template tasks: %w", err)
		}
	}
	if tasks == nil {
		tasks = []gen.ProjectTemplateTask{}
	}

	var deps []gen.ProjectTemplateDependency
	if len(r.Dependencies) > 0 {
		if err := json.Unmarshal(r.Dependencies, &deps); err != nil {
			return gen.ProjectTemplateResponse{}, fmt.Errorf("projects: unmarshal template dependencies: %w", err)
		}
	}
	if deps == nil {
		deps = []gen.ProjectTemplateDependency{}
	}

	return gen.ProjectTemplateResponse{
		Id:           r.ID,
		Name:         r.Name,
		Description:  r.Description,
		BillingType:  r.BillingType,
		Milestones:   milestones,
		Tasks:        tasks,
		Dependencies: deps,
		Revision:     r.Revision,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}, nil
}

// GetProjectsTemplates lists all reusable project templates.
// (GET /api/v1/projects/templates)
func (s *server) GetProjectsTemplates(ctx context.Context, _ gen.GetProjectsTemplatesRequestObject) (gen.GetProjectsTemplatesResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("projects: list templates: %w", err)
	}

	list := make([]gen.ProjectTemplateResponse, 0, len(rows))
	for _, r := range rows {
		resp, err := templateResponseFromRow(r)
		if err != nil {
			return nil, err
		}
		list = append(list, resp)
	}
	return gen.GetProjectsTemplates200JSONResponse{Templates: list}, nil
}

// PostProjectsTemplates creates a new project template.
// (POST /api/v1/projects/templates)
func (s *server) PostProjectsTemplates(ctx context.Context, req gen.PostProjectsTemplatesRequestObject) (gen.PostProjectsTemplatesResponseObject, error) {
	if req.Body == nil {
		return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}
	body := *req.Body

	name := strings.TrimSpace(body.Name)
	if name == "" {
		return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is required."}}),
		), nil
	}
	if utf8.RuneCountInString(name) > 200 {
		return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is at most 200 characters."}}),
		), nil
	}

	var desc *string
	if body.Description != nil {
		d := strings.TrimSpace(*body.Description)
		if utf8.RuneCountInString(d) > 2000 {
			return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(
				apicommon.ValidationProblem("Invalid template", map[string][]string{"description": {"Description is at most 2000 characters."}}),
			), nil
		}
		if d != "" {
			desc = &d
		}
	}

	var milestones []gen.ProjectTemplateMilestone
	if body.Milestones != nil {
		milestones = *body.Milestones
	}
	var tasks []gen.ProjectTemplateTask
	if body.Tasks != nil {
		tasks = *body.Tasks
	}
	var deps []gen.ProjectTemplateDependency
	if body.Dependencies != nil {
		deps = *body.Dependencies
	}

	if d := validateTemplateData(milestones, tasks, deps); d != nil {
		return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(*d), nil
	}

	milestonesJSON, err := json.Marshal(milestones)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal milestones: %w", err)
	}
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal tasks: %w", err)
	}
	depsJSON, err := json.Marshal(deps)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal dependencies: %w", err)
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()

	q := store.New(s.deps.Pool)
	row, err := q.InsertTemplate(ctx, store.InsertTemplateParams{
		Name:            name,
		Description:     desc,
		BillingType:     body.BillingType,
		Milestones:      milestonesJSON,
		Tasks:           tasksJSON,
		Dependencies:    depsJSON,
		CreatedByUserID: by.UserID,
		Now:             now,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: insert template: %w", err)
	}

	resp, err := templateResponseFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsTemplates201JSONResponse(resp), nil
}

// GetProjectsTemplatesByTemplateId reads one template by id.
// (GET /api/v1/projects/templates/{templateId})
func (s *server) GetProjectsTemplatesByTemplateId(ctx context.Context, req gen.GetProjectsTemplatesByTemplateIdRequestObject) (gen.GetProjectsTemplatesByTemplateIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetTemplate(ctx, req.TemplateId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsTemplatesByTemplateId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get template: %w", err)
	}

	resp, err := templateResponseFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsTemplatesByTemplateId200JSONResponse(resp), nil
}

// PutProjectsTemplatesByTemplateId updates an existing template.
// (PUT /api/v1/projects/templates/{templateId})
func (s *server) PutProjectsTemplatesByTemplateId(ctx context.Context, req gen.PutProjectsTemplatesByTemplateIdRequestObject) (gen.PutProjectsTemplatesByTemplateIdResponseObject, error) {
	if req.Body == nil {
		return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}
	body := *req.Body

	name := strings.TrimSpace(body.Name)
	if name == "" {
		return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is required."}}),
		), nil
	}
	if utf8.RuneCountInString(name) > 200 {
		return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is at most 200 characters."}}),
		), nil
	}

	var desc *string
	if body.Description != nil {
		d := strings.TrimSpace(*body.Description)
		if utf8.RuneCountInString(d) > 2000 {
			return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(
				apicommon.ValidationProblem("Invalid template", map[string][]string{"description": {"Description is at most 2000 characters."}}),
			), nil
		}
		if d != "" {
			desc = &d
		}
	}

	var milestones []gen.ProjectTemplateMilestone
	if body.Milestones != nil {
		milestones = *body.Milestones
	}
	var tasks []gen.ProjectTemplateTask
	if body.Tasks != nil {
		tasks = *body.Tasks
	}
	var deps []gen.ProjectTemplateDependency
	if body.Dependencies != nil {
		deps = *body.Dependencies
	}

	if d := validateTemplateData(milestones, tasks, deps); d != nil {
		return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(*d), nil
	}

	milestonesJSON, err := json.Marshal(milestones)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal milestones: %w", err)
	}
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal tasks: %w", err)
	}
	depsJSON, err := json.Marshal(deps)
	if err != nil {
		return nil, fmt.Errorf("projects: marshal dependencies: %w", err)
	}

	now := s.deps.Clock()
	q := store.New(s.deps.Pool)

	existing, err := q.GetTemplate(ctx, req.TemplateId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsTemplatesByTemplateId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get template: %w", err)
	}
	if existing.Revision != body.Revision {
		return gen.PutProjectsTemplatesByTemplateId409ApplicationProblemPlusJSONResponse(
			revisionConflict(existing.Revision, body.Revision),
		), nil
	}

	row, err := q.UpdateTemplate(ctx, store.UpdateTemplateParams{
		ID:           req.TemplateId,
		Name:         name,
		Description:  desc,
		BillingType:  body.BillingType,
		Milestones:   milestonesJSON,
		Tasks:        tasksJSON,
		Dependencies: depsJSON,
		Revision:     body.Revision,
		Now:          now,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: update template: %w", err)
	}

	resp, err := templateResponseFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsTemplatesByTemplateId200JSONResponse(resp), nil
}

// DeleteProjectsTemplatesByTemplateId deletes a template.
// (DELETE /api/v1/projects/templates/{templateId})
func (s *server) DeleteProjectsTemplatesByTemplateId(ctx context.Context, req gen.DeleteProjectsTemplatesByTemplateIdRequestObject) (gen.DeleteProjectsTemplatesByTemplateIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	_, err := q.GetTemplate(ctx, req.TemplateId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProjectsTemplatesByTemplateId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: check template: %w", err)
	}

	if err := q.DeleteTemplate(ctx, req.TemplateId); err != nil {
		return nil, fmt.Errorf("projects: delete template: %w", err)
	}
	return gen.DeleteProjectsTemplatesByTemplateId204Response{}, nil
}

// PostProjectsByIdApplyTemplate instantiates a template onto a project with relative day offsets.
// (POST /api/v1/projects/{id}/apply-template)
func (s *server) PostProjectsByIdApplyTemplate(ctx context.Context, req gen.PostProjectsByIdApplyTemplateRequestObject) (gen.PostProjectsByIdApplyTemplateResponseObject, error) {
	if req.Body == nil {
		return gen.PostProjectsByIdApplyTemplate400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template application", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}
	body := *req.Body

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdApplyTemplate404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdApplyTemplate404Response{}, nil
	}
	if !a.CanContribute {
		return gen.PostProjectsByIdApplyTemplate403JSONResponse(forbidden()), nil
	}

	tmplRow, err := q.GetTemplate(ctx, body.TemplateId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdApplyTemplate404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get template: %w", err)
	}

	tmpl, err := templateResponseFromRow(tmplRow)
	if err != nil {
		return nil, err
	}
	// The stored blueprint passed these rules when it was saved; asking them
	// again trims what is inserted and refuses, rather than half-applies, a
	// template saved before a rule existed.
	if d := validateTemplateData(tmpl.Milestones, tmpl.Tasks, tmpl.Dependencies); d != nil {
		return gen.PostProjectsByIdApplyTemplate400ApplicationProblemPlusJSONResponse(*d), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	kickoff := body.KickoffDate.Time

	var milestonesCount, tasksCount, depsCount int32

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass,
			ProjectID: project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire task order lock: %w", err)
		}

		// 1. Instantiate Delivery Milestones
		maxMstonePos, err := txq.MaxDeliveryMilestonePosition(ctx, project.ID)
		if err != nil {
			return fmt.Errorf("projects: max milestone position: %w", err)
		}

		milestoneIndexToID := make(map[int32]int32, len(tmpl.Milestones))
		for i, m := range tmpl.Milestones {
			targetDate := kickoff.AddDate(0, 0, int(m.TargetDayOffset))
			pos := maxMstonePos + int32(i) + 1
			row, err := txq.InsertDeliveryMilestone(ctx, store.InsertDeliveryMilestoneParams{
				ProjectID:       project.ID,
				Name:            m.Name,
				Description:     m.Description,
				TargetDate:      pgtype.Date{Time: targetDate, Valid: true},
				Position:        pos,
				CreatedByUserID: by.UserID,
				Now:             now,
			})
			if err != nil {
				return fmt.Errorf("projects: insert delivery milestone from template: %w", err)
			}
			if err := recordDeliveryMilestoneEvent(ctx, txq, now, eventDeliveryMilestoneAdded, row, nil, by); err != nil {
				return err
			}
			milestoneIndexToID[int32(i)] = row.ID
			milestonesCount++
		}

		// 2. Instantiate Tasks & Subtasks
		maxTaskPos, err := txq.MaxSiblingPosition(ctx, store.MaxSiblingPositionParams{
			ProjectID:    project.ID,
			ParentTaskID: nil,
		})
		if err != nil {
			return fmt.Errorf("projects: max task position: %w", err)
		}

		taskIndexToID := make(map[int32]int32, len(tmpl.Tasks))
		for i, t := range tmpl.Tasks {
			var startDatePg, dueDatePg pgtype.Date
			if t.StartDayOffset != nil {
				startDatePg = pgtype.Date{Time: kickoff.AddDate(0, 0, int(*t.StartDayOffset)), Valid: true}
			}
			if t.DueDayOffset != nil {
				dueDatePg = pgtype.Date{Time: kickoff.AddDate(0, 0, int(*t.DueDayOffset)), Valid: true}
			}

			var milestoneID *int32
			if t.MilestoneIndex != nil {
				if mid, ok := milestoneIndexToID[*t.MilestoneIndex]; ok {
					milestoneID = &mid
				}
			}

			var estimateHoursPg pgtype.Numeric
			if t.EstimateHours != nil {
				est, err := numericFromFloatPtr(t.EstimateHours)
				if err != nil {
					return fmt.Errorf("projects: parse estimate hours: %w", err)
				}
				estimateHoursPg = est
			}

			pos := maxTaskPos + int32(i) + 1
			row, err := txq.InsertTask(ctx, store.InsertTaskParams{
				ProjectID:           project.ID,
				ParentTaskID:        nil,
				Title:               t.Title,
				Description:         t.Description,
				Status:              "todo",
				AssigneeUserID:      nil,
				StartDate:           startDatePg,
				DueDate:             dueDatePg,
				EstimateHours:       estimateHoursPg,
				DeliveryMilestoneID: milestoneID,
				Position:            pos,
				CompletedAt:         nil,
				CreatedByUserID:     by.UserID,
				Now:                 now,
			})
			if err != nil {
				return fmt.Errorf("projects: insert task from template: %w", err)
			}
			taskIndexToID[int32(i)] = row.ID
			tasksCount++

			// Subtasks
			if t.Subtasks != nil {
				for subIdx, sub := range *t.Subtasks {
					var subStartPg, subDuePg pgtype.Date
					if sub.StartDayOffset != nil {
						subStartPg = pgtype.Date{Time: kickoff.AddDate(0, 0, int(*sub.StartDayOffset)), Valid: true}
					}
					if sub.DueDayOffset != nil {
						subDuePg = pgtype.Date{Time: kickoff.AddDate(0, 0, int(*sub.DueDayOffset)), Valid: true}
					}

					var subEstimateHoursPg pgtype.Numeric
					if sub.EstimateHours != nil {
						est, err := numericFromFloatPtr(sub.EstimateHours)
						if err != nil {
							return fmt.Errorf("projects: parse subtask estimate hours: %w", err)
						}
						subEstimateHoursPg = est
					}

					parentID := row.ID
					_, err := txq.InsertTask(ctx, store.InsertTaskParams{
						ProjectID:           project.ID,
						ParentTaskID:        &parentID,
						Title:               sub.Title,
						Description:         sub.Description,
						Status:              "todo",
						AssigneeUserID:      nil,
						StartDate:           subStartPg,
						DueDate:             subDuePg,
						EstimateHours:       subEstimateHoursPg,
						DeliveryMilestoneID: milestoneID,
						Position:            int32(subIdx + 1),
						CompletedAt:         nil,
						CreatedByUserID:     by.UserID,
						Now:                 now,
					})
					if err != nil {
						return fmt.Errorf("projects: insert subtask from template: %w", err)
					}
					tasksCount++
				}
			}
		}

		// 3. Instantiate Dependencies
		for _, dep := range tmpl.Dependencies {
			predID, predOk := taskIndexToID[dep.PredecessorTaskIndex]
			succID, succOk := taskIndexToID[dep.SuccessorTaskIndex]
			if predOk && succOk && predID != succID {
				_, err := txq.InsertTaskDependency(ctx, store.InsertTaskDependencyParams{
					ProjectID:         project.ID,
					PredecessorTaskID: predID,
					SuccessorTaskID:   succID,
					CreatedByUserID:   by.UserID,
					Now:               now,
				})
				if err != nil {
					return fmt.Errorf("projects: insert dependency from template: %w", err)
				}
				depsCount++
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return gen.PostProjectsByIdApplyTemplate200JSONResponse{
		MilestonesCreated:   milestonesCount,
		TasksCreated:        tasksCount,
		DependenciesCreated: depsCount,
	}, nil
}

// validateTemplateData checks a template's blueprint against the rules the
// rows it becomes are held to, so applying a valid template never fails on a
// rule a hand-made milestone or task would have been refused for: the
// delivery milestone name and description rules, the task title, description
// and estimate rules, non-negative day offsets, a due offset no earlier than
// the start offset, and indices that point at a milestone or top-level task
// of the same template. Dependencies are additionally refused when a pair is
// listed twice (the project's dependency table holds each pair once) or when
// they form a cycle, which no project's dependency graph may contain.
//
// It trims the names, titles and descriptions in place, so what is stored —
// and what apply inserts — is the value the rules were asked of.
func validateTemplateData(
	milestones []gen.ProjectTemplateMilestone,
	tasks []gen.ProjectTemplateTask,
	dependencies []gen.ProjectTemplateDependency,
) *apicommon.HttpValidationProblemDetails {
	invalid := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			invalid[field] = append(invalid[field], msg)
		}
	}
	offsets := func(prefix string, start, due *int32) {
		if start != nil && *start < 0 {
			add(prefix+".startDayOffset", "Start day offset cannot be negative.")
		}
		if due != nil && *due < 0 {
			add(prefix+".dueDayOffset", "Due day offset cannot be negative.")
		}
		if start != nil && due != nil && *due < *start {
			add(prefix+".dueDayOffset", "Due day offset cannot be earlier than start day offset.")
		}
	}

	for i := range milestones {
		m := &milestones[i]
		prefix := fmt.Sprintf("milestones[%d]", i)
		var msg string
		m.Name, msg = validateDeliveryMilestoneName(m.Name)
		add(prefix+".name", msg)
		m.Description, msg = validateDeliveryMilestoneDescription(m.Description)
		add(prefix+".description", msg)
		if m.TargetDayOffset < 0 {
			add(prefix+".targetDayOffset", "Target day offset cannot be negative.")
		}
	}

	for i := range tasks {
		t := &tasks[i]
		prefix := fmt.Sprintf("tasks[%d]", i)
		var msg string
		t.Title, msg = validateTaskTitle(t.Title)
		add(prefix+".title", msg)
		t.Description, msg = validateTaskDescription(t.Description)
		add(prefix+".description", msg)
		add(prefix+".estimateHours", validateEstimateHours(t.EstimateHours))
		offsets(prefix, t.StartDayOffset, t.DueDayOffset)
		if t.MilestoneIndex != nil {
			idx := int(*t.MilestoneIndex)
			if idx < 0 || idx >= len(milestones) {
				add(prefix+".milestoneIndex", "Milestone index out of bounds.")
			}
		}
		if t.Subtasks == nil {
			continue
		}
		for j := range *t.Subtasks {
			sub := &(*t.Subtasks)[j]
			subPrefix := fmt.Sprintf("%s.subtasks[%d]", prefix, j)
			sub.Title, msg = validateTaskTitle(sub.Title)
			add(subPrefix+".title", msg)
			sub.Description, msg = validateTaskDescription(sub.Description)
			add(subPrefix+".description", msg)
			add(subPrefix+".estimateHours", validateEstimateHours(sub.EstimateHours))
			offsets(subPrefix, sub.StartDayOffset, sub.DueDayOffset)
		}
	}

	indicesValid := true
	seen := make(map[[2]int32]int, len(dependencies))
	for i, d := range dependencies {
		prefix := fmt.Sprintf("dependencies[%d]", i)
		pred := int(d.PredecessorTaskIndex)
		succ := int(d.SuccessorTaskIndex)
		if pred < 0 || pred >= len(tasks) {
			add(prefix+".predecessorTaskIndex", "Predecessor task index out of bounds.")
			indicesValid = false
		}
		if succ < 0 || succ >= len(tasks) {
			add(prefix+".successorTaskIndex", "Successor task index out of bounds.")
			indicesValid = false
		}
		if pred == succ {
			add(prefix+".successorTaskIndex", "A task cannot depend on itself.")
			indicesValid = false
		}
		pair := [2]int32{d.PredecessorTaskIndex, d.SuccessorTaskIndex}
		if first, ok := seen[pair]; ok {
			add(prefix+".successorTaskIndex", fmt.Sprintf("This dependency is already listed as dependency %d.", first+1))
			continue
		}
		seen[pair] = i
	}
	if indicesValid && templateDependenciesCycle(len(tasks), dependencies) {
		add("dependencies", "The dependencies form a cycle: a task would have to wait for itself.")
	}

	if len(invalid) > 0 {
		p := apicommon.ValidationProblem("Invalid template", invalid)
		return &p
	}
	return nil
}

// templateDependenciesCycle reports whether the dependencies, every index of
// which is already known to be in bounds, form a cycle among the template's
// tasks. Kahn's algorithm: a graph is acyclic exactly when repeatedly removing
// the tasks nothing waits on removes every task.
func templateDependenciesCycle(taskCount int, dependencies []gen.ProjectTemplateDependency) bool {
	incoming := make([]int, taskCount)
	successors := make([][]int32, taskCount)
	for _, d := range dependencies {
		successors[d.PredecessorTaskIndex] = append(successors[d.PredecessorTaskIndex], d.SuccessorTaskIndex)
		incoming[d.SuccessorTaskIndex]++
	}
	ready := make([]int32, 0, taskCount)
	for i, n := range incoming {
		if n == 0 {
			ready = append(ready, int32(i))
		}
	}
	removed := 0
	for len(ready) > 0 {
		next := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		removed++
		for _, succ := range successors[next] {
			incoming[succ]--
			if incoming[succ] == 0 {
				ready = append(ready, succ)
			}
		}
	}
	return removed < taskCount
}
