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

var (
	errDependencySelf            = errors.New("projects: a task cannot depend on itself")
	errDependencyPredecessorGone = errors.New("projects: predecessor task not found on this project")
	errDependencySuccessorGone   = errors.New("projects: successor task not found on this project")
	errDependencyAlreadyExists   = errors.New("projects: dependency link already exists")
	errDependencyCycleDetected   = errors.New("projects: adding this dependency creates a cyclic dependency")
)

// createsCycle checks if adding an edge predecessorID -> successorID would create a cycle.
// Since adding predecessorID -> successorID creates a cycle iff predecessorID is reachable
// from successorID, we do a breadth-first search from successorID following existing edges.
func createsCycle(existingDeps []store.ProjectsTaskDependency, predecessorID, successorID int32) bool {
	if predecessorID == successorID {
		return true
	}
	graph := make(map[int32][]int32, len(existingDeps))
	for _, d := range existingDeps {
		graph[d.PredecessorTaskID] = append(graph[d.PredecessorTaskID], d.SuccessorTaskID)
	}

	visited := make(map[int32]bool)
	queue := []int32{successorID}
	visited[successorID] = true

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr == predecessorID {
			return true
		}
		for _, next := range graph[curr] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// findDownstreamTaskIDs finds all task IDs reachable from rootTaskID following successor edges.
func findDownstreamTaskIDs(existingDeps []store.ProjectsTaskDependency, rootTaskID int32) []int32 {
	graph := make(map[int32][]int32, len(existingDeps))
	for _, d := range existingDeps {
		graph[d.PredecessorTaskID] = append(graph[d.PredecessorTaskID], d.SuccessorTaskID)
	}

	visited := make(map[int32]bool)
	visited[rootTaskID] = true
	var result []int32
	queue := []int32{rootTaskID}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, next := range graph[curr] {
			if !visited[next] {
				visited[next] = true
				result = append(result, next)
				queue = append(queue, next)
			}
		}
	}
	return result
}

// GetProjectsByIdDependencies lists a project's task dependencies
// (GET /api/v1/projects/{id}/dependencies)
func (s *server) GetProjectsByIdDependencies(ctx context.Context, req gen.GetProjectsByIdDependenciesRequestObject) (gen.GetProjectsByIdDependenciesResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdDependencies404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdDependencies404Response{}, nil
	}

	rows, err := q.ListTaskDependenciesByProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list task dependencies: %w", err)
	}

	list := make([]gen.TaskDependencyResponse, 0, len(rows))
	for _, r := range rows {
		list = append(list, gen.TaskDependencyResponse{
			Id:                r.ID,
			ProjectId:         r.ProjectID,
			PredecessorTaskId: r.PredecessorTaskID,
			SuccessorTaskId:   r.SuccessorTaskID,
			CreatedAt:         r.CreatedAt,
		})
	}
	return gen.GetProjectsByIdDependencies200JSONResponse{Dependencies: list}, nil
}

// PostProjectsByIdDependencies creates a new task dependency on a project
// (POST /api/v1/projects/{id}/dependencies)
func (s *server) PostProjectsByIdDependencies(ctx context.Context, req gen.PostProjectsByIdDependenciesRequestObject) (gen.PostProjectsByIdDependenciesResponseObject, error) {
	if req.Body == nil {
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid task dependency", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}
	body := *req.Body

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdDependencies404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdDependencies404Response{}, nil
	}
	if !a.CanContribute {
		return gen.PostProjectsByIdDependencies403JSONResponse(forbidden()), nil
	}

	if body.PredecessorTaskId == body.SuccessorTaskId {
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid task dependency", map[string][]string{"successorTaskId": {"A task cannot depend on itself."}}),
		), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()

	var created store.ProjectsTaskDependency
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass,
			ProjectID: project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire task order lock: %w", err)
		}

		predTask, err := txq.GetTask(ctx, body.PredecessorTaskId)
		if errors.Is(err, pgx.ErrNoRows) || predTask.ProjectID != project.ID {
			return errDependencyPredecessorGone
		}
		if err != nil {
			return fmt.Errorf("projects: check predecessor task: %w", err)
		}

		succTask, err := txq.GetTask(ctx, body.SuccessorTaskId)
		if errors.Is(err, pgx.ErrNoRows) || succTask.ProjectID != project.ID {
			return errDependencySuccessorGone
		}
		if err != nil {
			return fmt.Errorf("projects: check successor task: %w", err)
		}

		_, err = txq.FindTaskDependencyByPair(ctx, store.FindTaskDependencyByPairParams{
			PredecessorTaskID: body.PredecessorTaskId,
			SuccessorTaskID:   body.SuccessorTaskId,
		})
		if err == nil {
			return errDependencyAlreadyExists
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("projects: check existing dependency: %w", err)
		}

		existingDeps, err := txq.ListTaskDependenciesByProject(ctx, project.ID)
		if err != nil {
			return fmt.Errorf("projects: list existing dependencies: %w", err)
		}

		if createsCycle(existingDeps, body.PredecessorTaskId, body.SuccessorTaskId) {
			return errDependencyCycleDetected
		}

		created, err = txq.InsertTaskDependency(ctx, store.InsertTaskDependencyParams{
			ProjectID:         project.ID,
			PredecessorTaskID: body.PredecessorTaskId,
			SuccessorTaskID:   body.SuccessorTaskId,
			CreatedByUserID:   by.UserID,
			Now:               now,
		})
		if err != nil {
			return fmt.Errorf("projects: insert task dependency: %w", err)
		}

		return nil
	})

	switch {
	case errors.Is(err, errDependencyPredecessorGone):
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid task dependency", map[string][]string{"predecessorTaskId": {"Predecessor task not found in this project."}}),
		), nil
	case errors.Is(err, errDependencySuccessorGone):
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid task dependency", map[string][]string{"successorTaskId": {"Successor task not found in this project."}}),
		), nil
	case errors.Is(err, errDependencyAlreadyExists):
		return gen.PostProjectsByIdDependencies409ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Dependency conflict", map[string][]string{"dependency": {"Dependency link already exists."}}),
		), nil
	case errors.Is(err, errDependencyCycleDetected):
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Cyclic dependency", map[string][]string{"cycle": {"Adding this dependency creates a cyclic dependency."}}),
		), nil
	case err != nil:
		return nil, err
	}

	return gen.PostProjectsByIdDependencies201JSONResponse{
		Id:                created.ID,
		ProjectId:         created.ProjectID,
		PredecessorTaskId: created.PredecessorTaskID,
		SuccessorTaskId:   created.SuccessorTaskID,
		CreatedAt:         created.CreatedAt,
	}, nil
}

// DeleteProjectsDependenciesByDependencyId removes a task dependency
// (DELETE /api/v1/projects/dependencies/{dependencyId})
func (s *server) DeleteProjectsDependenciesByDependencyId(ctx context.Context, req gen.DeleteProjectsDependenciesByDependencyIdRequestObject) (gen.DeleteProjectsDependenciesByDependencyIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	dep, err := q.GetTaskDependency(ctx, req.DependencyId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProjectsDependenciesByDependencyId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get task dependency: %w", err)
	}

	project, err := q.GetProject(ctx, dep.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProjectsDependenciesByDependencyId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}

	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.DeleteProjectsDependenciesByDependencyId404Response{}, nil
	}
	if !a.CanContribute {
		return gen.DeleteProjectsDependenciesByDependencyId403JSONResponse(forbidden()), nil
	}

	if err := q.DeleteTaskDependency(ctx, dep.ID); err != nil {
		return nil, fmt.Errorf("projects: delete task dependency: %w", err)
	}

	return gen.DeleteProjectsDependenciesByDependencyId204Response{}, nil
}

// PostProjectsTasksByTaskIdRescheduleDownstream shifts dates on all downstream tasks
// (POST /api/v1/projects/tasks/{taskId}/reschedule-downstream)
func (s *server) PostProjectsTasksByTaskIdRescheduleDownstream(ctx context.Context, req gen.PostProjectsTasksByTaskIdRescheduleDownstreamRequestObject) (gen.PostProjectsTasksByTaskIdRescheduleDownstreamResponseObject, error) {
	if req.Body == nil {
		return gen.PostProjectsTasksByTaskIdRescheduleDownstream400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid reschedule request", map[string][]string{"body": {"Request body is required."}}),
		), nil
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostProjectsTasksByTaskIdRescheduleDownstream404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PostProjectsTasksByTaskIdRescheduleDownstream403JSONResponse(forbidden()), nil
	}

	daysDelta := req.Body.DaysDelta
	if daysDelta == 0 {
		return gen.PostProjectsTasksByTaskIdRescheduleDownstream200JSONResponse{
			ShiftedTasksCount: 0,
			UpdatedTaskIds:    []int32{},
		}, nil
	}

	now := s.deps.Clock()
	var updatedIDs []int32

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass,
			ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire task order lock: %w", err)
		}

		deps, err := txq.ListTaskDependenciesByProject(ctx, scope.Project.ID)
		if err != nil {
			return fmt.Errorf("projects: list task dependencies: %w", err)
		}

		downstreamIDs := findDownstreamTaskIDs(deps, req.TaskId)
		if len(downstreamIDs) == 0 {
			updatedIDs = []int32{}
			return nil
		}

		shifted, err := txq.ShiftTasksDates(ctx, store.ShiftTasksDatesParams{
			Ids:       downstreamIDs,
			DaysDelta: daysDelta,
			Now:       now,
		})
		if err != nil {
			return fmt.Errorf("projects: shift tasks dates: %w", err)
		}
		updatedIDs = shifted
		return nil
	})
	if err != nil {
		return nil, err
	}

	if updatedIDs == nil {
		updatedIDs = []int32{}
	}

	return gen.PostProjectsTasksByTaskIdRescheduleDownstream200JSONResponse{
		ShiftedTasksCount: int32(len(updatedIDs)),
		UpdatedTaskIds:    updatedIDs,
	}, nil
}
