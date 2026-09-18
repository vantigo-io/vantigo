package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a project's work (design §2 D5–D7, §4.1): tasks nested one
// level deep, where each one sits among its siblings, and the cross-project
// list of what the caller still owes.
//
// Two things run through every operation here. The first is who may write:
// seeing the project is enough to read its tasks, writing them takes the
// member or manager role (access.CanContribute), and an outsider is answered
// the bare 404 an unknown id gets — so the order is always the row, then the
// caller's access to it, then the body. The second is that a task is
// addressed by its own id, not by its project's: /projects/tasks/{taskId}
// resolves the project by loading the task first, which is also why an
// unknown task and a task on an invisible project answer identically.
//
// Tasks write no timeline entries (D6): the timeline is the project's own
// history, and a task's is its comments.

// taskOrderLockClass is the class every task-ordering advisory lock is taken
// in, with the project's id as the object — one lock per project, held for the
// transaction that is deciding a position in it. Postgres keeps two-argument
// advisory locks in a lock space of their own, so this can never collide with
// identity's single-argument ones.
const taskOrderLockClass = 9

// The refusals a task write carries out of its transaction. Each is decided
// against rows the transaction holds — whether the task is still there, and
// what the parent it names actually is — and a refusal has to roll the
// transaction back rather than return a response from inside it.
var (
	errTaskGone        = errors.New("projects: the task no longer exists")
	errParentNotFound  = errors.New("projects: the parent task does not exist on this project")
	errParentIsSubtask = errors.New("projects: the parent task is itself a subtask")
	errParentIsItself  = errors.New("projects: a task cannot be its own parent")
	errTaskHasSubtasks = errors.New("projects: the task has subtasks of its own")
)

// taskScope is one task, the project it belongs to and what the caller may do
// with that project: everything the four task-scoped operations decide from,
// resolved once.
type taskScope struct {
	Task    store.ProjectsTask
	Project store.ProjectsProject
	Access  access
}

// taskScopeFor loads a task addressed by its own id and resolves the caller's
// access to the project it belongs to. It reports false for a task nobody has
// *and* for a task on a project the caller cannot see, because those two must
// be indistinguishable (D7): the caller learns nothing about a project they
// hold no role on, not even that one of its tasks exists.
//
// It is the one place the task-scoped operations — and the checklist and
// comment operations built on them — resolve "which project is this about,
// and may the caller act here".
func (s *server) taskScopeFor(ctx context.Context, q *store.Queries, taskID int32) (taskScope, bool, error) {
	task, err := q.GetTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskScope{}, false, nil
	}
	if err != nil {
		return taskScope{}, false, fmt.Errorf("projects: get task: %w", err)
	}
	project, err := q.GetProject(ctx, task.ProjectID)
	if err != nil {
		return taskScope{}, false, fmt.Errorf("projects: get the task's project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return taskScope{}, false, err
	}
	if !a.CanSee {
		return taskScope{}, false, nil
	}
	return taskScope{Task: task, Project: project, Access: a}, true, nil
}

// checkParent is D6's nesting rule, asked of the rows the transaction holds: a
// parent has to be a task of this project, it has to be top-level itself, and
// it cannot be the task being moved. movingTaskID is 0 on a create, where
// there is no task yet for a parent to be.
func checkParent(ctx context.Context, q *store.Queries, projectID, parentID, movingTaskID int32) error {
	if parentID == movingTaskID {
		return errParentIsItself
	}
	parent, err := q.GetTask(ctx, parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errParentNotFound
	}
	if err != nil {
		return fmt.Errorf("projects: get the parent task: %w", err)
	}
	// A task of another project is refused exactly as an unknown one is: a
	// caller must not learn what lives on a project they were not asking
	// about.
	if parent.ProjectID != projectID {
		return errParentNotFound
	}
	if parent.ParentTaskID != nil {
		return errParentIsSubtask
	}
	return nil
}

// parentFieldError turns a parent refusal into the `parentTaskId` message it
// is reported under. Every one of them is an ordinary field error rather than
// a 404: the project exists and the caller may write on it, so what is wrong
// is the body they sent.
func parentFieldError(err error, parentID int32) map[string][]string {
	switch {
	case errors.Is(err, errParentIsSubtask):
		return fieldError("parentTaskId", parentTaskIsSubtask(parentID))
	case errors.Is(err, errParentIsItself):
		return fieldError("parentTaskId", taskCannotNestItself())
	case errors.Is(err, errTaskHasSubtasks):
		return fieldError("parentTaskId", taskHasSubtasks())
	default:
		return fieldError("parentTaskId", parentTaskNotFound(parentID))
	}
}

// isParentRefusal reports whether err is one of the refusals
// parentFieldError answers for.
func isParentRefusal(err error) bool {
	return errors.Is(err, errParentNotFound) || errors.Is(err, errParentIsSubtask) ||
		errors.Is(err, errParentIsItself) || errors.Is(err, errTaskHasSubtasks)
}

// GetProjectsByIdTasks List a project's tasks
// (GET /api/v1/projects/{id}/tasks)
//
// Anyone who sees the project sees its tasks (D7): what is being worked on is
// how a member reads their own project, and a viewer who could not see the
// tasks would have nothing to look at.
//
// The whole tree is one query, aggregates included (queries/tasks.sql), and
// the assignees are named in one directory call for the whole project: a
// project with fifty tasks costs two round trips, not one per task.
func (s *server) GetProjectsByIdTasks(ctx context.Context, req gen.GetProjectsByIdTasksRequestObject) (gen.GetProjectsByIdTasksResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdTasks404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdTasks404Response{}, nil
	}

	// An empty status is no filter, not a filter for the empty string: a
	// frontend that clears its status dropdown sends `status=`. A status
	// outside the enumeration is a mistake worth reporting rather than a
	// filter that silently matches nothing — the project list's own rule.
	status := req.Params.Status
	if status != nil && *status == "" {
		status = nil
	}
	if status != nil && !validTaskStatus(*status) {
		return gen.GetProjectsByIdTasks400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters",
				fmt.Sprintf("'status' must be one of %s, but was '%s'.", taskStatusList(), *status))), nil
	}

	rows, err := q.ListProjectTasks(ctx, store.ListProjectTasksParams{
		ProjectID:      project.ID,
		Status:         status,
		AssigneeUserID: req.Params.AssigneeUserId,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: list tasks: %w", err)
	}
	tasks, err := s.taskResponses(ctx, countedTaskRows(rows))
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsByIdTasks200JSONResponse(tasks), nil
}

// taskResponses is the tree of one set of rows, with every assignee named in
// one directory call. It is the shared tail of both reads that answer a tree.
func (s *server) taskResponses(ctx context.Context, rows []taskRow) ([]gen.TaskResponse, error) {
	assignees, err := s.taskAssignees(ctx, rows)
	if err != nil {
		return nil, err
	}
	return taskTree(rows, assignees)
}

// taskResponseFor is one task rendered the way a list of them is, so a create,
// an update and a move all answer through exactly the code a read does.
func (s *server) taskResponseFor(ctx context.Context, row taskRow) (gen.TaskResponse, error) {
	assignees, err := s.taskAssignees(ctx, []taskRow{row})
	if err != nil {
		return gen.TaskResponse{}, err
	}
	return taskResponse(row, assignees)
}

// countedTaskFor re-reads one task with its aggregates, for the answer to a
// write that did not touch its checklist or its comments but still has to
// report them.
func (s *server) countedTaskFor(ctx context.Context, q *store.Queries, taskID int32) (gen.TaskResponse, error) {
	row, err := q.GetTaskWithCounts(ctx, taskID)
	if err != nil {
		return gen.TaskResponse{}, fmt.Errorf("projects: read the task back: %w", err)
	}
	return s.taskResponseFor(ctx, taskRowOf(row))
}

// PostProjectsByIdTasks Add a task to a project
// (POST /api/v1/projects/{id}/tasks)
//
// A create appends: the new task takes the number after the last of its
// siblings. That number is read and written under the project's ordering lock
// (AcquireTaskOrderLock), because two creates racing for the end of the same
// group would otherwise both read the same maximum and both write the number
// after it — and no row lock can cover that, since what they race for is the
// gap after the last row.
//
// The parent's rules are checked under the same lock rather than before it: a
// parent that is being turned into a subtask by somebody else's move is
// exactly the case a check taken beforehand would miss.
func (s *server) PostProjectsByIdTasks(ctx context.Context, req gen.PostProjectsByIdTasksRequestObject) (gen.PostProjectsByIdTasksResponseObject, error) {
	body := gen.TaskRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdTasks404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdTasks404Response{}, nil
	}
	if !a.CanContribute {
		return gen.PostProjectsByIdTasks403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := s.validateTask(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdTasks400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.ProjectsTask
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: project.ID,
		}); err != nil {
			return fmt.Errorf("projects: take the task ordering lock: %w", err)
		}
		if parsed.ParentTaskID != nil {
			if err := checkParent(ctx, txq, project.ID, *parsed.ParentTaskID, 0); err != nil {
				return err
			}
		}
		last, err := txq.MaxSiblingPosition(ctx, store.MaxSiblingPositionParams{
			ProjectID: project.ID, ParentTaskID: parsed.ParentTaskID,
		})
		if err != nil {
			return fmt.Errorf("projects: read the last sibling's position: %w", err)
		}
		created, err = txq.InsertTask(ctx, store.InsertTaskParams{
			ProjectID:       project.ID,
			ParentTaskID:    parsed.ParentTaskID,
			Title:           parsed.Title,
			Description:     parsed.Description,
			Status:          parsed.Status,
			AssigneeUserID:  parsed.AssigneeUserID,
			StartDate:       parsed.StartDate,
			DueDate:         parsed.DueDate,
			EstimateHours:   parsed.EstimateHours,
			Position:        last + 1,
			CompletedAt:     completedAtFor("", parsed.Status, nil, now),
			CreatedByUserID: by.UserID,
			Now:             now,
		})
		return err
	})
	if isParentRefusal(err) {
		return gen.PostProjectsByIdTasks400ApplicationProblemPlusJSONResponse(
			invalidProject(parentFieldError(err, *parsed.ParentTaskID))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: create task: %w", err)
	}

	// A task that has just been created has no checklist items and no
	// comments, because nothing has had the chance to add any: its aggregates
	// are zero without asking for them.
	resp, err := s.taskResponseFor(ctx, newTaskRow(created))
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdTasks201JSONResponse(resp), nil
}

// GetProjectsTasksByTaskId Get a task by id
// (GET /api/v1/projects/tasks/{taskId})
//
// The task is addressed without its project, because a task id already names
// one: the project — and with it the caller's access — is resolved by loading
// the task first (taskScopeFor).
func (s *server) GetProjectsTasksByTaskId(ctx context.Context, req gen.GetProjectsTasksByTaskIdRequestObject) (gen.GetProjectsTasksByTaskIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.GetProjectsTasksByTaskId404Response{}, nil
	}

	rows, err := q.ListTaskWithSubtasks(ctx, scope.Task.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list the task and its subtasks: %w", err)
	}
	tasks, err := s.taskResponses(ctx, taskRowsOf(rows))
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		// Deleted between the two reads, which is the same answer as never
		// having existed.
		return gen.GetProjectsTasksByTaskId404Response{}, nil
	}
	return gen.GetProjectsTasksByTaskId200JSONResponse(tasks[0]), nil
}

// PutProjectsTasksByTaskId Update a task
// (PUT /api/v1/projects/tasks/{taskId})
//
// An update carries every field of the task as it should stand afterwards,
// plus the revision the caller read it at — a project's own update, applied to
// a task. Where the task sits is deliberately not part of it: position and
// parent are the move's, so an edit saved from a drawer somebody left open
// cannot undo a reordering made since.
//
// The revision is enforced by the UPDATE's own WHERE clause rather than by
// comparing the row that was read: between the read and the write another
// request can commit, and only the database can decide that race.
func (s *server) PutProjectsTasksByTaskId(ctx context.Context, req gen.PutProjectsTasksByTaskIdRequestObject) (gen.PutProjectsTasksByTaskIdResponseObject, error) {
	body := gen.TaskUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsTasksByTaskId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PutProjectsTasksByTaskId403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := s.validateTask(ctx, taskFromUpdate(body))
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsTasksByTaskId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The completion stamp is decided from the row this transaction holds,
		// not from the one the handler read: whether this update is the one
		// that moves the task into 'done' is exactly what another writer can
		// change underneath.
		before, err := txq.LockTask(ctx, scope.Task.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTaskGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock task: %w", err)
		}
		_, err = txq.UpdateTask(ctx, store.UpdateTaskParams{
			ID:             before.ID,
			Revision:       body.Revision,
			Title:          parsed.Title,
			Description:    parsed.Description,
			Status:         parsed.Status,
			AssigneeUserID: parsed.AssigneeUserID,
			StartDate:      parsed.StartDate,
			DueDate:        parsed.DueDate,
			EstimateHours:  parsed.EstimateHours,
			CompletedAt:    completedAtFor(before.Status, parsed.Status, before.CompletedAt, now),
			Now:            now,
		})
		return err
	})
	switch {
	case errors.Is(err, errTaskGone):
		return gen.PutProjectsTasksByTaskId404Response{}, nil
	case errors.Is(err, pgx.ErrNoRows):
		// The revision the task actually carries is read again rather than
		// taken from the row the handler loaded: the write that beat this one
		// committed after that read, so it would report the number the caller
		// already sent.
		current, err := q.GetTask(ctx, scope.Task.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutProjectsTasksByTaskId404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("projects: re-read the task after a revision conflict: %w", err)
		}
		return gen.PutProjectsTasksByTaskId409ApplicationProblemPlusJSONResponse(
			revisionConflict(current.Revision, body.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("projects: update task: %w", err)
	}

	resp, err := s.countedTaskFor(ctx, q, scope.Task.ID)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsTasksByTaskId200JSONResponse(resp), nil
}

// DeleteProjectsTasksByTaskId Delete a task
// (DELETE /api/v1/projects/tasks/{taskId})
//
// D5: a mis-created task is common, so tasks are deleted rather than archived.
// The delete takes the task's subtasks, checklist items and comments with it
// through the schema's cascades, and it is the delete's own row count that
// decides the 404 — two deletes racing must not both answer 204.
//
// A time entry that referenced the task keeps its own title snapshot (D5), so
// nothing another module holds becomes unreadable.
func (s *server) DeleteProjectsTasksByTaskId(ctx context.Context, req gen.DeleteProjectsTasksByTaskIdRequestObject) (gen.DeleteProjectsTasksByTaskIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.DeleteProjectsTasksByTaskId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.DeleteProjectsTasksByTaskId403JSONResponse(forbidden()), nil
	}

	rows, err := q.DeleteTask(ctx, scope.Task.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: delete task: %w", err)
	}
	if rows == 0 {
		return gen.DeleteProjectsTasksByTaskId404Response{}, nil
	}
	return gen.DeleteProjectsTasksByTaskId204Response{}, nil
}

// PutProjectsTasksByTaskIdPosition Move a task among its siblings
// (PUT /api/v1/projects/tasks/{taskId}/position)
//
// One operation moves a task inside its group and between groups, because
// from the caller's side there is one intention — "this task goes here" — and
// dragging it onto another parent is the same gesture as dragging it up.
//
// Both groups are renumbered 1..n inside one transaction, under the project's
// ordering lock and with the sibling rows themselves held: two moves in one
// group otherwise interleave two renumberings and leave a gap or a duplicate.
// The move writes no revision: position is not a field of the task's own form,
// so reordering must not make somebody's open edit stale.
func (s *server) PutProjectsTasksByTaskIdPosition(ctx context.Context, req gen.PutProjectsTasksByTaskIdPositionRequestObject) (gen.PutProjectsTasksByTaskIdPositionResponseObject, error) {
	body := gen.TaskPositionRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsTasksByTaskIdPosition404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PutProjectsTasksByTaskIdPosition403JSONResponse(forbidden()), nil
	}
	if msg := validateTaskPosition(body.Position); msg != "" {
		return gen.PutProjectsTasksByTaskIdPosition400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("position", msg))), nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: take the task ordering lock: %w", err)
		}
		task, err := txq.LockTask(ctx, scope.Task.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTaskGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock task: %w", err)
		}

		if body.ParentTaskId != nil {
			if err := checkParent(ctx, txq, task.ProjectID, *body.ParentTaskId, task.ID); err != nil {
				return err
			}
			// D6's depth rule seen from the other side: a task that has
			// subtasks cannot become one, or its children would sit three
			// levels deep.
			children, err := txq.CountSubtasks(ctx, &task.ID)
			if err != nil {
				return fmt.Errorf("projects: count the task's subtasks: %w", err)
			}
			if children > 0 {
				return errTaskHasSubtasks
			}
		}

		reparented := !equalInt32Ptr(task.ParentTaskID, body.ParentTaskId)
		if reparented {
			if err := txq.SetTaskParent(ctx, store.SetTaskParentParams{
				ID: task.ID, ParentTaskID: body.ParentTaskId, Now: now,
			}); err != nil {
				return fmt.Errorf("projects: move the task to its new parent: %w", err)
			}
		}

		// The group the task now belongs to, held row by row, reordered in Go
		// and written back as 1..n.
		siblings, err := txq.SiblingTaskIDs(ctx, store.SiblingTaskIDsParams{
			ProjectID: task.ProjectID, ParentTaskID: body.ParentTaskId,
		})
		if err != nil {
			return fmt.Errorf("projects: lock the task's siblings: %w", err)
		}
		if err := txq.RenumberTasks(ctx, store.RenumberTasksParams{
			Ids: moveWithin(siblings, task.ID, body.Position), Now: now,
		}); err != nil {
			return fmt.Errorf("projects: renumber the task's siblings: %w", err)
		}
		if !reparented {
			return nil
		}
		// The group it left has to close the gap it left behind.
		former, err := txq.SiblingTaskIDs(ctx, store.SiblingTaskIDsParams{
			ProjectID: task.ProjectID, ParentTaskID: task.ParentTaskID,
		})
		if err != nil {
			return fmt.Errorf("projects: lock the task's former siblings: %w", err)
		}
		if err := txq.RenumberTasks(ctx, store.RenumberTasksParams{Ids: former, Now: now}); err != nil {
			return fmt.Errorf("projects: renumber the task's former siblings: %w", err)
		}
		return nil
	})
	switch {
	case errors.Is(err, errTaskGone):
		return gen.PutProjectsTasksByTaskIdPosition404Response{}, nil
	case isParentRefusal(err):
		parentID := scope.Task.ID
		if body.ParentTaskId != nil {
			parentID = *body.ParentTaskId
		}
		return gen.PutProjectsTasksByTaskIdPosition400ApplicationProblemPlusJSONResponse(
			invalidProject(parentFieldError(err, parentID))), nil
	case err != nil:
		return nil, fmt.Errorf("projects: move task: %w", err)
	}

	resp, err := s.countedTaskFor(ctx, q, scope.Task.ID)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsTasksByTaskIdPosition200JSONResponse(resp), nil
}

// moveWithin is the new order of one sibling group: ids with taskID taken out
// and put back at position, which is 1-based. A position past the end is the
// end — dragging a task to the bottom of a list sends whatever number the list
// happened to have — and a taskID that is not in ids (it was never there, or
// it has just been re-parented into it) is simply inserted.
func moveWithin(ids []int32, taskID, position int32) []int32 {
	rest := make([]int32, 0, len(ids)+1)
	for _, id := range ids {
		if id != taskID {
			rest = append(rest, id)
		}
	}
	at := int(position) - 1
	if at > len(rest) {
		at = len(rest)
	}
	out := make([]int32, 0, len(rest)+1)
	out = append(out, rest[:at]...)
	out = append(out, taskID)
	out = append(out, rest[at:]...)
	return out
}

// GetProjectsMyTasks List the caller's open tasks
// (GET /api/v1/projects/my-tasks)
//
// The one task list that is not about a project: what the caller still owes,
// across every project they can see. Visibility is the query's own predicate
// (projects.visible, the same function the project list uses), so a task on a
// project the caller has lost their role on simply stops being listed — and a
// caller who sees every project still only gets their own tasks, because
// seeing a project is not being assigned its work.
func (s *server) GetProjectsMyTasks(ctx context.Context, _ gen.GetProjectsMyTasksRequestObject) (gen.GetProjectsMyTasksResponseObject, error) {
	v := s.visibilityFor(ctx)
	q := store.New(s.deps.Pool)
	rows, err := q.ListMyOpenTasks(ctx, store.ListMyOpenTasksParams{UserID: v.UserID, SeeAll: v.SeeAll})
	if err != nil {
		return nil, fmt.Errorf("projects: list my tasks: %w", err)
	}

	tasks := make([]taskRow, 0, len(rows))
	for _, row := range rows {
		tasks = append(tasks, taskRow{
			Task: row.ProjectsTask, ChecklistTotal: row.ChecklistTotal,
			ChecklistDone: row.ChecklistDone, CommentCount: row.CommentCount,
		})
	}
	assignees, err := s.taskAssignees(ctx, tasks)
	if err != nil {
		return nil, err
	}

	data := make([]gen.MyTaskResponse, 0, len(rows))
	for i, row := range rows {
		task, err := myTaskResponse(tasks[i], row.ProjectCode, row.ProjectName, assignees)
		if err != nil {
			return nil, err
		}
		data = append(data, task)
	}
	return gen.GetProjectsMyTasks200JSONResponse(data), nil
}
