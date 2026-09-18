package projects

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
)

// This file is design §4.1's task rules, written the way values.go writes a
// project's: one function per rule, each answering the normalized value and
// the message to report, "" when the rule holds. They live beside the task
// operations rather than in values.go for the reason lines_validation.go does:
// values.go is already the whole of a project's own validation, and one file
// holding both would be read by nobody looking for either.

// validateTaskTitle is the title rule: non-blank, at most 200 characters (the
// column's width), trimmed but case-preserved.
func validateTaskTitle(raw string) (string, string) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", "A task title cannot be null or empty"
	}
	if n := utf8.RuneCountInString(title); n > 200 {
		return "", fmt.Sprintf("A task title cannot be longer than 200 characters, the given value was %d characters", n)
	}
	return title, ""
}

// validateTaskDescription is the description rule: optional, at most 4000
// characters. A blank description is stored as no description at all, so "  "
// and an absent field mean the same thing — the same shape a project's own
// description rule has.
func validateTaskDescription(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if n := utf8.RuneCountInString(trimmed); n > 4000 {
		return nil, fmt.Sprintf("A task description cannot be longer than 4000 characters, the given value was %d characters", n)
	}
	return &trimmed, ""
}

// validateTaskStatus is the status rule: optional on the wire, because a new
// task is 'todo' and a body that says nothing about status means exactly that;
// one of the three when it is given, exactly as written, since the strings are
// what other modules key on (OpenTasksForUser's "not done" filter).
func validateTaskStatus(raw *string) (string, string) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return taskStatusTodo, ""
	}
	if !validTaskStatus(*raw) {
		return "", fmt.Sprintf("A task status must be one of %s, but was '%s'", taskStatusList(), *raw)
	}
	return *raw, ""
}

// validateTaskDateOrder is §4.1's date rule: a task cannot be due before it
// starts. Either date alone is fine — a task with a due date and no start is
// the ordinary case.
func validateTaskDateOrder(start, due *openapi_types.Date) string {
	if start == nil || due == nil || !due.Before(start.Time) {
		return ""
	}
	return "A due date cannot be before the start date"
}

// validateEstimateHours is the estimate rule: set or absent, never zero or
// negative. An estimate of nothing is an estimate nobody made, which is what
// leaving the field out says.
func validateEstimateHours(hours *float64) string {
	if hours == nil || *hours > 0 {
		return ""
	}
	return "An estimate must be greater than zero"
}

// assigneeNotFound and assigneeDisabled are the two ways `assigneeUserId` can
// fail. Both are ordinary field errors rather than a 404: the project exists
// and the caller may write on it, so what is wrong is the body they sent.
//
// The disabled rule applies to the assignment being *made*, not to one that
// already stands: an update that re-sends the assignee the task already
// carries is left alone even when that account has since been disabled, so a
// task nobody can reassign yet is still a task somebody can rename (validateTask's
// `assigned`). Moving it to a *different* disabled user is still refused, and
// the assignment itself renders inactive either way (taskResponse).
func assigneeNotFound(id uuid.UUID) string {
	return fmt.Sprintf("User %s does not exist", id)
}

func assigneeDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot be assigned a task", id)
}

// The parent rules' messages (D6 — one level of nesting). All three are
// decided against the tasks table rather than against the body alone, so they
// are reported by the handler from inside its transaction.
func parentTaskNotFound(id int32) string {
	return fmt.Sprintf("Task %d does not exist on this project", id)
}

func parentTaskIsSubtask(id int32) string {
	return fmt.Sprintf("Task %d is itself a subtask, and a task can only be nested one level deep", id)
}

func taskCannotNestItself() string {
	return "A task cannot be a subtask of itself"
}

func taskHasSubtasks() string {
	return "A task with subtasks of its own cannot become a subtask"
}

// validateChecklistText is the checklist item's only rule: non-blank, at most
// 500 characters (the column's width), trimmed but case-preserved — a task's
// title rule, in the smaller size a line of a checklist is written in.
func validateChecklistText(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", "A checklist item cannot be null or empty"
	}
	if n := utf8.RuneCountInString(text); n > 500 {
		return "", fmt.Sprintf("A checklist item cannot be longer than 500 characters, the given value was %d characters", n)
	}
	return text, ""
}

// validateCommentBody is the comment's only rule: non-blank, at most 4000
// characters (the column's width, and the same length a task's description
// gets). A blank comment is not an empty comment, it is a comment nobody
// wrote, so it is refused rather than stored.
func validateCommentBody(raw string) (string, string) {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "", "A comment cannot be null or empty"
	}
	if n := utf8.RuneCountInString(body); n > 4000 {
		return "", fmt.Sprintf("A comment cannot be longer than 4000 characters, the given value was %d characters", n)
	}
	return body, ""
}

// validateTaskPosition is the move's own rule, shared by a task among its
// siblings and a checklist item among its task's: positions are 1-based, so
// anything below one is a mistake. There is no upper bound — a position past
// the end of the group means last, which is what dragging a task to the
// bottom of a list sends.
func validateTaskPosition(position int32) string {
	if position >= 1 {
		return ""
	}
	return "A position must be 1 or greater"
}

// parsedTask is one validated task body, in the shape the write wants: the
// title trimmed, the dates already pgtype.Date, the estimate already
// pgtype.Numeric. ParentTaskID rides along unvalidated — whether it names a
// top-level task of this project is settled under the project's ordering lock,
// not here.
type parsedTask struct {
	Title          string
	Description    *string
	Status         string
	AssigneeUserID *uuid.UUID
	StartDate      pgtype.Date
	DueDate        pgtype.Date
	EstimateHours  pgtype.Numeric
	ParentTaskID   *int32
}

// validateTask runs every §4.1 rule that is a property of the body alone and
// returns the write-ready task, the field errors (nil when there are none),
// and an error for an infrastructure failure — a directory lookup that failed,
// or a number Postgres could not store — which is never the caller's fault and
// so is never a field error.
//
// Every rule runs regardless of the others, so one round trip reports every
// problem with the body. The assignee is resolved through
// contracts.UserDirectory, the only way this module may read identity's users.
//
// assigned is the assignee the task already carries, nil on a create and for
// an unassigned task. It is what makes "may this user be given this task"
// different from "is this task still assigned to them": an update that keeps
// the assignment it was given is not making one, so a since-disabled account
// does not freeze the task (assigneeDisabled).
func (s *server) validateTask(ctx context.Context, body gen.TaskRequest, assigned *uuid.UUID) (parsedTask, map[string][]string, error) {
	errs := map[string][]string{}
	add := func(field, msg string) {
		if msg != "" {
			errs[field] = append(errs[field], msg)
		}
	}

	title, msg := validateTaskTitle(body.Title)
	add("title", msg)
	description, msg := validateTaskDescription(body.Description)
	add("description", msg)
	status, msg := validateTaskStatus(body.Status)
	add("status", msg)
	add("dueDate", validateTaskDateOrder(body.StartDate, body.DueDate))
	add("estimateHours", validateEstimateHours(body.EstimateHours))

	if body.AssigneeUserId != nil {
		entry, err := s.deps.Users.User(ctx, *body.AssigneeUserId)
		if err != nil {
			return parsedTask{}, nil, fmt.Errorf("projects: resolve the task's assignee: %w", err)
		}
		kept := assigned != nil && *assigned == *body.AssigneeUserId
		switch {
		case entry == nil:
			add("assigneeUserId", assigneeNotFound(*body.AssigneeUserId))
		case !entry.Active && !kept:
			add("assigneeUserId", assigneeDisabled(*body.AssigneeUserId))
		}
	}

	if len(errs) > 0 {
		return parsedTask{}, errs, nil
	}

	estimateHours, err := numericFromFloatPtr(body.EstimateHours)
	if err != nil {
		return parsedTask{}, nil, err
	}

	return parsedTask{
		Title:          title,
		Description:    description,
		Status:         status,
		AssigneeUserID: body.AssigneeUserId,
		StartDate:      dateToPgtype(body.StartDate),
		DueDate:        dateToPgtype(body.DueDate),
		EstimateHours:  estimateHours,
		ParentTaskID:   body.ParentTaskId,
	}, nil, nil
}

// taskFromUpdate is an update body seen as the create body §4.1's rules are
// written against. An update carries every field of the task as it should
// stand afterwards, so the two bodies differ in exactly two things: the
// revision, which is a concurrency token rather than a value any rule has an
// opinion about, and the parent, which an update cannot move (that is the
// position operation's). One validator therefore serves both paths, and a rule
// can never be enforced on a create but forgotten on an update.
func taskFromUpdate(body gen.TaskUpdateRequest) gen.TaskRequest {
	return gen.TaskRequest{
		Title:          body.Title,
		Description:    body.Description,
		Status:         body.Status,
		AssigneeUserId: body.AssigneeUserId,
		StartDate:      body.StartDate,
		DueDate:        body.DueDate,
		EstimateHours:  body.EstimateHours,
	}
}

// completedAtFor is §4.1's completion stamp: entering 'done' stamps the task
// with the clock, leaving it clears the stamp, and an edit that leaves a done
// task done keeps the stamp it already carries — renaming something finished
// does not finish it again.
func completedAtFor(before, after string, stamped *time.Time, now time.Time) *time.Time {
	if after != taskStatusDone {
		return nil
	}
	if before == taskStatusDone && stamped != nil {
		return stamped
	}
	return &now
}
