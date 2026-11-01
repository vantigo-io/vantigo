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
	errDependencyPredecessorGone = errors.New("projects: predecessor task not found on this project")
	errDependencySuccessorGone   = errors.New("projects: successor task not found on this project")
	errDependencyAlreadyExists   = errors.New("projects: dependency link already exists")
	errDependencyCycleDetected   = errors.New("projects: adding this dependency creates a cyclic dependency")
)

// maxDependencyChainLinks bounds the longest chain of linked tasks a project's
// dependencies may make (spec M7): a hundred links is far past any plan a
// person reads, and short of what a reschedule or a Gantt walks in one go.
// Templates are held to the same limit, so applying one never builds a chain
// a hand-made link would have been refused for.
const maxDependencyChainLinks = 100

// dependencyChainTooLongTitle is the problem title of that refusal, which is
// how a client tells it from the other dependency 400s — this module's
// refusals carry no codes of their own (errors.go).
const dependencyChainTooLongTitle = "Dependency chain too long"

// dependencyChainTooLongMessage is that refusal's field message, naming the
// chain's length as it would have been.
func dependencyChainTooLongMessage(links int) string {
	return fmt.Sprintf("This would make a chain of %d linked dependencies; a chain may hold at most %d.", links, maxDependencyChainLinks)
}

// dependencyChainTooLong carries the refusal, and the length that decided it,
// out of the create's transaction.
type dependencyChainTooLong struct{ links int }

func (e dependencyChainTooLong) Error() string {
	return fmt.Sprintf("projects: the dependency would make a chain of %d links", e.links)
}

// longestChainThrough is the number of links in the longest chain that would
// run through a new edge predecessorID -> successorID: the longest chain
// ending at the predecessor, the new link, and the longest chain starting at
// the successor. It is asked only once createsCycle has said no, so both walks
// run over an acyclic graph and each task's answer is worked out once.
func longestChainThrough(existingDeps []store.ProjectsTaskDependency, predecessorID, successorID int32) int {
	successors := make(map[int32][]int32, len(existingDeps))
	predecessors := make(map[int32][]int32, len(existingDeps))
	for _, d := range existingDeps {
		successors[d.PredecessorTaskID] = append(successors[d.PredecessorTaskID], d.SuccessorTaskID)
		predecessors[d.SuccessorTaskID] = append(predecessors[d.SuccessorTaskID], d.PredecessorTaskID)
	}
	longest := func(edges map[int32][]int32, from int32) int {
		memo := map[int32]int{}
		var walk func(int32) int
		walk = func(id int32) int {
			if n, ok := memo[id]; ok {
				return n
			}
			n := 0
			for _, next := range edges[id] {
				n = max(n, walk(next)+1)
			}
			memo[id] = n
			return n
		}
		return walk(from)
	}
	return longest(predecessors, predecessorID) + 1 + longest(successors, successorID)
}

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
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("projects: check predecessor task: %w", err)
		}
		if err != nil || predTask.ProjectID != project.ID {
			return errDependencyPredecessorGone
		}

		succTask, err := txq.GetTask(ctx, body.SuccessorTaskId)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("projects: check successor task: %w", err)
		}
		if err != nil || succTask.ProjectID != project.ID {
			return errDependencySuccessorGone
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
		if links := longestChainThrough(existingDeps, body.PredecessorTaskId, body.SuccessorTaskId); links > maxDependencyChainLinks {
			return dependencyChainTooLong{links: links}
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

	var tooLong dependencyChainTooLong
	switch {
	case errors.As(err, &tooLong):
		return gen.PostProjectsByIdDependencies400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem(dependencyChainTooLongTitle, map[string][]string{"successorTaskId": {dependencyChainTooLongMessage(tooLong.links)}}),
		), nil
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

// maxRescheduleDays bounds a downstream shift to ten years either way, far
// past any real plan and short of the date arithmetic overflowing.
const maxRescheduleDays = 3650

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
	if daysDelta < -maxRescheduleDays || daysDelta > maxRescheduleDays {
		return gen.PostProjectsTasksByTaskIdRescheduleDownstream400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid reschedule request", map[string][]string{
				"daysDelta": {fmt.Sprintf("Shift by at most %d days either way.", maxRescheduleDays)},
			}),
		), nil
	}
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
