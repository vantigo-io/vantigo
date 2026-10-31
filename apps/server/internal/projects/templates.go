package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

var (
	errTemplateGone = errors.New("projects: the template no longer exists")
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
	if len(name) > 200 {
		return gen.PostProjectsTemplates400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is at most 200 characters."}}),
		), nil
	}

	var desc *string
	if body.Description != nil {
		d := strings.TrimSpace(*body.Description)
		if len(d) > 2000 {
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
	if len(name) > 200 {
		return gen.PutProjectsTemplatesByTemplateId400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"name": {"Template name is at most 200 characters."}}),
		), nil
	}

	var desc *string
	if body.Description != nil {
		d := strings.TrimSpace(*body.Description)
		if len(d) > 2000 {
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

func validateTemplateData(
	milestones []gen.ProjectTemplateMilestone,
	tasks []gen.ProjectTemplateTask,
	dependencies []gen.ProjectTemplateDependency,
) *apicommon.HttpValidationProblemDetails {
	invalid := map[string][]string{}

	for i, m := range milestones {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			invalid[fmt.Sprintf("milestones[%d].name", i)] = []string{"Milestone name is required."}
		} else if len(name) > 200 {
			invalid[fmt.Sprintf("milestones[%d].name", i)] = []string{"Milestone name is at most 200 characters."}
		}
		if m.TargetDayOffset < 0 {
			invalid[fmt.Sprintf("milestones[%d].targetDayOffset", i)] = []string{"Target day offset cannot be negative."}
		}
	}

	for i, t := range tasks {
		title := strings.TrimSpace(t.Title)
		if title == "" {
			invalid[fmt.Sprintf("tasks[%d].title", i)] = []string{"Task title is required."}
		} else if len(title) > 200 {
			invalid[fmt.Sprintf("tasks[%d].title", i)] = []string{"Task title is at most 200 characters."}
		}
		if t.StartDayOffset != nil && *t.StartDayOffset < 0 {
			invalid[fmt.Sprintf("tasks[%d].startDayOffset", i)] = []string{"Start day offset cannot be negative."}
		}
		if t.DueDayOffset != nil && *t.DueDayOffset < 0 {
			invalid[fmt.Sprintf("tasks[%d].dueDayOffset", i)] = []string{"Due day offset cannot be negative."}
		}
		if t.StartDayOffset != nil && t.DueDayOffset != nil && *t.DueDayOffset < *t.StartDayOffset {
			invalid[fmt.Sprintf("tasks[%d].dueDayOffset", i)] = []string{"Due day offset cannot be earlier than start day offset."}
		}
		if t.MilestoneIndex != nil {
			idx := int(*t.MilestoneIndex)
			if idx < 0 || idx >= len(milestones) {
				invalid[fmt.Sprintf("tasks[%d].milestoneIndex", i)] = []string{"Milestone index out of bounds."}
			}
		}
		if t.Subtasks != nil {
			for j, sub := range *t.Subtasks {
				subTitle := strings.TrimSpace(sub.Title)
				if subTitle == "" {
					invalid[fmt.Sprintf("tasks[%d].subtasks[%d].title", i, j)] = []string{"Subtask title is required."}
				} else if len(subTitle) > 200 {
					invalid[fmt.Sprintf("tasks[%d].subtasks[%d].title", i, j)] = []string{"Subtask title is at most 200 characters."}
				}
				if sub.StartDayOffset != nil && *sub.StartDayOffset < 0 {
					invalid[fmt.Sprintf("tasks[%d].subtasks[%d].startDayOffset", i, j)] = []string{"Start day offset cannot be negative."}
				}
				if sub.DueDayOffset != nil && *sub.DueDayOffset < 0 {
					invalid[fmt.Sprintf("tasks[%d].subtasks[%d].dueDayOffset", i, j)] = []string{"Due day offset cannot be negative."}
				}
				if sub.StartDayOffset != nil && sub.DueDayOffset != nil && *sub.DueDayOffset < *sub.StartDayOffset {
					invalid[fmt.Sprintf("tasks[%d].subtasks[%d].dueDayOffset", i, j)] = []string{"Due day offset cannot be earlier than start day offset."}
				}
			}
		}
	}

	for i, d := range dependencies {
		pred := int(d.PredecessorTaskIndex)
		succ := int(d.SuccessorTaskIndex)
		if pred < 0 || pred >= len(tasks) {
			invalid[fmt.Sprintf("dependencies[%d].predecessorTaskIndex", i)] = []string{"Predecessor task index out of bounds."}
		}
		if succ < 0 || succ >= len(tasks) {
			invalid[fmt.Sprintf("dependencies[%d].successorTaskIndex", i)] = []string{"Successor task index out of bounds."}
		}
		if pred == succ {
			invalid[fmt.Sprintf("dependencies[%d].successorTaskIndex", i)] = []string{"A task cannot depend on itself."}
		}
	}

	if len(invalid) > 0 {
		p := apicommon.ValidationProblem("Invalid template", invalid)
		return &p
	}
	return nil
}
