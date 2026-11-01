package projects

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the way back from a project to a template (spec §4): the
// project's plan — its delivery milestones, its tasks with their subtasks and
// checklists, and the links between them — read as it stands and stored as a
// new template, so a project that went well can start the next one.
//
// What is copied is the plan and not the work: titles, descriptions,
// estimates, dates and links, never assignees, statuses, comments or logged
// time. A template links only top-level tasks, so a dependency that touches a
// subtask is left out, and a subtask carries no milestone of its own (apply
// gives it its parent's). Dates become day offsets from an anchor — the
// project's start date when it has one, otherwise the earliest date among the
// copied rows — and a date before the project's start becomes 0, because a
// template's offsets cannot be negative.
//
// Saving is a read of the project: it records nothing on the project's
// timeline, and the snapshot is taken in one read-only transaction so it never
// mixes two states of a plan somebody is editing.

// insertTemplateChecklist adds a template task's checklist to the task apply
// just created from it, in order and unchecked, in apply's transaction.
func insertTemplateChecklist(ctx context.Context, txq *store.Queries, taskID int32, items *[]string, now time.Time) error {
	if items == nil {
		return nil
	}
	for k, text := range *items {
		if _, err := txq.InsertChecklistItem(ctx, store.InsertChecklistItemParams{
			TaskID: taskID, Text: text, Position: int32(k + 1), Now: now,
		}); err != nil {
			return fmt.Errorf("projects: insert checklist item from template: %w", err)
		}
	}
	return nil
}

// validateTemplateHeader is a template's name and description rule: a name of
// 1–200 characters and a description of at most 2000, both trimmed, a blank
// description stored as none. It answers the first field that fails.
func validateTemplateHeader(rawName string, rawDescription *string) (string, *string, map[string][]string) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "", nil, fieldError("name", "Template name is required.")
	}
	if utf8.RuneCountInString(name) > 200 {
		return "", nil, fieldError("name", "Template name is at most 200 characters.")
	}
	if rawDescription == nil {
		return name, nil, nil
	}
	d := strings.TrimSpace(*rawDescription)
	if utf8.RuneCountInString(d) > 2000 {
		return "", nil, fieldError("description", "Description is at most 2000 characters.")
	}
	if d == "" {
		return name, nil, nil
	}
	return name, &d, nil
}

// projectPlan is what a snapshot reads of one project.
type projectPlan struct {
	project      store.ProjectsProject
	milestones   []store.ListProjectDeliveryMilestonesRow
	tasks        []store.ListProjectTasksRow
	checklists   []store.ProjectsTaskChecklistItem
	dependencies []store.ProjectsTaskDependency
}

// PostProjectsByIdSaveAsTemplate saves a project's plan as a new template.
// (POST /api/v1/projects/{id}/save-as-template)
//
// The router has already asked for projects:create, which every change to the
// template catalog needs; the handler asks the rest, which only it can: that
// the caller sees this project, answering the bare 404 an unknown one gets
// when they do not.
func (s *server) PostProjectsByIdSaveAsTemplate(ctx context.Context, req gen.PostProjectsByIdSaveAsTemplateRequestObject) (gen.PostProjectsByIdSaveAsTemplateResponseObject, error) {
	if req.Body == nil {
		return gen.PostProjectsByIdSaveAsTemplate400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdSaveAsTemplate404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdSaveAsTemplate404Response{}, nil
	}

	name, desc, errs := validateTemplateHeader(req.Body.Name, req.Body.Description)
	if errs != nil {
		return gen.PostProjectsByIdSaveAsTemplate400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid template", errs),
		), nil
	}

	plan := projectPlan{project: project}
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		if plan.milestones, err = txq.ListProjectDeliveryMilestones(ctx, project.ID); err != nil {
			return fmt.Errorf("projects: list delivery milestones: %w", err)
		}
		if plan.tasks, err = txq.ListProjectTasks(ctx, store.ListProjectTasksParams{ProjectID: project.ID}); err != nil {
			return fmt.Errorf("projects: list tasks: %w", err)
		}
		if plan.checklists, err = txq.ListProjectChecklistItems(ctx, project.ID); err != nil {
			return fmt.Errorf("projects: list checklist items: %w", err)
		}
		if plan.dependencies, err = txq.ListTaskDependenciesByProject(ctx, project.ID); err != nil {
			return fmt.Errorf("projects: list task dependencies: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	milestones, tasks, deps, err := snapshotProjectPlan(plan)
	if err != nil {
		return nil, err
	}
	// The snapshot is held to the rules a template sent by hand is, so a plan
	// that breaks one — a checklist longer than a template holds, a date
	// before another it must follow — is refused, naming the row, rather
	// than stored as a template apply would refuse.
	if d := validateTemplateData(milestones, tasks, deps); d != nil {
		return gen.PostProjectsByIdSaveAsTemplate400ApplicationProblemPlusJSONResponse(*d), nil
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
	billingType := project.BillingType
	row, err := q.InsertTemplate(ctx, store.InsertTemplateParams{
		Name:            name,
		Description:     desc,
		BillingType:     &billingType,
		Milestones:      milestonesJSON,
		Tasks:           tasksJSON,
		Dependencies:    depsJSON,
		CreatedByUserID: by.UserID,
		Now:             s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("projects: insert template: %w", err)
	}

	resp, err := templateResponseFromRow(row)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdSaveAsTemplate201JSONResponse(resp), nil
}

// snapshotProjectPlan turns a project's plan into a template's blueprint. It
// is pure, so what a template holds is decided from the rows alone.
func snapshotProjectPlan(plan projectPlan) ([]gen.ProjectTemplateMilestone, []gen.ProjectTemplateTask, []gen.ProjectTemplateDependency, error) {
	// The milestones in the order they are planned in. The list query puts
	// completed milestones last, which is how they are read but not how they
	// were planned.
	milestoneRows := slices.Clone(plan.milestones)
	slices.SortStableFunc(milestoneRows, func(a, b store.ListProjectDeliveryMilestonesRow) int {
		return cmp.Or(cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})

	var topLevel []store.ProjectsTask
	subtasksOf := map[int32][]store.ProjectsTask{}
	for _, r := range plan.tasks {
		t := r.ProjectsTask
		if t.ParentTaskID == nil {
			topLevel = append(topLevel, t)
		} else {
			subtasksOf[*t.ParentTaskID] = append(subtasksOf[*t.ParentTaskID], t)
		}
	}
	checklistOf := map[int32][]string{}
	for _, item := range plan.checklists {
		checklistOf[item.TaskID] = append(checklistOf[item.TaskID], item.Text)
	}

	offset := dayOffsetsFrom(plan)

	milestoneIndex := make(map[int32]int32, len(milestoneRows))
	milestones := make([]gen.ProjectTemplateMilestone, 0, len(milestoneRows))
	for i, m := range milestoneRows {
		milestoneIndex[m.ID] = int32(i)
		var target int32
		if o := offset(m.TargetDate); o != nil {
			target = *o
		}
		milestones = append(milestones, gen.ProjectTemplateMilestone{
			Name: m.Name, Description: m.Description, TargetDayOffset: target,
		})
	}

	taskIndex := make(map[int32]int32, len(topLevel))
	tasks := make([]gen.ProjectTemplateTask, 0, len(topLevel))
	for i, t := range topLevel {
		taskIndex[t.ID] = int32(i)
		estimate, err := floatPtrFromNumeric(t.EstimateHours)
		if err != nil {
			return nil, nil, nil, err
		}
		task := gen.ProjectTemplateTask{
			Title:          t.Title,
			Description:    t.Description,
			StartDayOffset: offset(t.StartDate),
			DueDayOffset:   offset(t.DueDate),
			EstimateHours:  estimate,
			Checklist:      nonEmpty(checklistOf[t.ID]),
		}
		if t.DeliveryMilestoneID != nil {
			if idx, ok := milestoneIndex[*t.DeliveryMilestoneID]; ok {
				task.MilestoneIndex = &idx
			}
		}
		var subtasks []gen.ProjectTemplateSubtask
		for _, sub := range subtasksOf[t.ID] {
			subEstimate, err := floatPtrFromNumeric(sub.EstimateHours)
			if err != nil {
				return nil, nil, nil, err
			}
			subtasks = append(subtasks, gen.ProjectTemplateSubtask{
				Title:          sub.Title,
				Description:    sub.Description,
				StartDayOffset: offset(sub.StartDate),
				DueDayOffset:   offset(sub.DueDate),
				EstimateHours:  subEstimate,
				Checklist:      nonEmpty(checklistOf[sub.ID]),
			})
		}
		task.Subtasks = nonEmpty(subtasks)
		tasks = append(tasks, task)
	}

	deps := make([]gen.ProjectTemplateDependency, 0, len(plan.dependencies))
	for _, d := range plan.dependencies {
		pred, predOK := taskIndex[d.PredecessorTaskID]
		succ, succOK := taskIndex[d.SuccessorTaskID]
		if !predOK || !succOK {
			continue // a link that touches a subtask: templates link top-level tasks only
		}
		deps = append(deps, gen.ProjectTemplateDependency{PredecessorTaskIndex: pred, SuccessorTaskIndex: succ})
	}
	return milestones, tasks, deps, nil
}

// dayOffsetsFrom picks a snapshot's anchor and answers the function that turns
// a date into a day offset from it, nil for no date. The anchor is the
// project's start date, and a date before it becomes 0; a project without one
// is anchored on the earliest date among the rows being copied, which no date
// can come before. Without any date at all every offset is nil, and the
// anchor is never asked.
func dayOffsetsFrom(plan projectPlan) func(pgtype.Date) *int32 {
	anchor := plan.project.StartDate
	if !anchor.Valid {
		consider := func(d pgtype.Date) {
			if d.Valid && (!anchor.Valid || d.Time.Before(anchor.Time)) {
				anchor = d
			}
		}
		for _, m := range plan.milestones {
			consider(m.TargetDate)
		}
		for _, r := range plan.tasks {
			consider(r.ProjectsTask.StartDate)
			consider(r.ProjectsTask.DueDate)
		}
	}
	return func(d pgtype.Date) *int32 {
		if !d.Valid {
			return nil
		}
		days := int32(max(0, d.Time.Sub(anchor.Time).Round(24*time.Hour)/(24*time.Hour)))
		return &days
	}
}

// nonEmpty is a list as the contract's optional arrays carry it: absent when
// there is nothing in it.
func nonEmpty[T any](items []T) *[]T {
	if len(items) == 0 {
		return nil
	}
	return &items
}
