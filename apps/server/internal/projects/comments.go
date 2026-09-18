package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a task's comments (design §3.1, §4.1, §7). A task writes
// nothing to the project timeline (D6) because its history is here: the
// timeline is a record of what the project became, a task's comments are a
// record of what people said about the work.
//
// They are read oldest first and paged in the timeline's envelope — a
// conversation is read from the top — and every author is named through
// contracts.UserDirectory in one call for the whole page, never a join: this
// module's SQL never crosses into identity's schema, so author_user_id is as
// opaque here as a task's assignee is.
//
// Who may do what is narrower than the rest of a task, because a comment is
// somebody's writing rather than a field of the project's work: anyone who
// sees the project reads them, a member or a manager writes one
// (access.CanContribute), only the author changes what one says, and the
// author or a manager takes one down. A caller who cannot see the project is
// answered the bare 404 an unknown task gets, and a comment of another task
// answers that same 404 — otherwise a caller could learn which ids exist by
// trying them on a task they can see.

// commentScope is one comment, the task it hangs off and the caller's access
// to the project: everything the three comment-scoped operations decide from,
// resolved once.
type commentScope struct {
	Task    taskScope
	Comment store.ProjectsTaskComment
}

// authoredBy reports whether the caller wrote this comment — the rule an edit
// turns on, and half of the rule a delete turns on.
func (c commentScope) authoredBy(ctx context.Context) bool {
	p, _ := contracts.PrincipalFrom(ctx)
	return c.Comment.AuthorUserID == p.UserID
}

// commentScopeFor loads a comment addressed under the task in its path, with
// the caller's access to that task's project. It reports false for a comment
// nobody has, a comment of another task and a task the caller cannot see,
// because those three must be indistinguishable: the caller learns nothing
// about writing they were not shown.
func (s *server) commentScopeFor(ctx context.Context, q *store.Queries, taskID int32, commentID int64) (commentScope, bool, error) {
	task, ok, err := s.taskScopeFor(ctx, q, taskID)
	if err != nil || !ok {
		return commentScope{}, false, err
	}
	comment, err := q.GetTaskComment(ctx, store.GetTaskCommentParams{ID: commentID, TaskID: task.Task.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return commentScope{}, false, nil
	}
	if err != nil {
		return commentScope{}, false, fmt.Errorf("projects: get the task's comment: %w", err)
	}
	return commentScope{Task: task, Comment: comment}, true, nil
}

// GetProjectsTasksByTaskIdComments List a task's comments
// (GET /api/v1/projects/tasks/{taskId}/comments)
//
// Oldest first, paged the way the project timeline is: a task's conversation
// is read from the top, and the page envelope is the one every paged read in
// this module answers in. The task is loaded before the paging is validated so
// that an outsider sending a bad page still gets the 404 that tells them
// nothing (D7).
func (s *server) GetProjectsTasksByTaskIdComments(ctx context.Context, req gen.GetProjectsTasksByTaskIdCommentsRequestObject) (gen.GetProjectsTasksByTaskIdCommentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.GetProjectsTasksByTaskIdComments404Response{}, nil
	}

	if msgs := validatePageParams(req.Params.Page, req.Params.PageSize); len(msgs) > 0 {
		return gen.GetProjectsTasksByTaskIdComments400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)

	total, err := q.CountTaskComments(ctx, scope.Task.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: count the task's comments: %w", err)
	}
	rows, err := q.ListTaskComments(ctx, store.ListTaskCommentsParams{
		TaskID: scope.Task.ID, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: list the task's comments: %w", err)
	}
	data, err := s.commentResponses(ctx, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsTasksByTaskIdComments200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// PostProjectsTasksByTaskIdComments Comment on a task
// (POST /api/v1/projects/tasks/{taskId}/comments)
//
// Writing on a task is writing the project's work, so it takes the member or
// manager role — a viewer reads the conversation without joining it. The
// author is the caller and is never taken from the body: a comment says who
// wrote it, and nobody may write in somebody else's name.
func (s *server) PostProjectsTasksByTaskIdComments(ctx context.Context, req gen.PostProjectsTasksByTaskIdCommentsRequestObject) (gen.PostProjectsTasksByTaskIdCommentsResponseObject, error) {
	body := gen.CommentRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostProjectsTasksByTaskIdComments404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PostProjectsTasksByTaskIdComments403JSONResponse(forbidden()), nil
	}
	written, msg := validateCommentBody(body.Body)
	if msg != "" {
		return gen.PostProjectsTasksByTaskIdComments400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("body", msg))), nil
	}

	p, _ := contracts.PrincipalFrom(ctx)
	created, err := q.InsertTaskComment(ctx, store.InsertTaskCommentParams{
		TaskID: scope.Task.ID, AuthorUserID: p.UserID, Body: written, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("projects: write a comment: %w", err)
	}
	resp, err := s.commentResponseFor(ctx, created)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsTasksByTaskIdComments201JSONResponse(resp), nil
}

// PutProjectsTasksByTaskIdCommentsByCommentId Edit a comment
// (PUT /api/v1/projects/tasks/{taskId}/comments/{commentId})
//
// Only the author changes what a comment says. A manager may take one down —
// they answer for what stands on the project — but not put words in somebody
// else's mouth, which is why this is the one operation on a task that a
// manager is refused. The edit stamps edited_at, so a reader can tell a
// comment that was rewritten from one that was not.
func (s *server) PutProjectsTasksByTaskIdCommentsByCommentId(ctx context.Context, req gen.PutProjectsTasksByTaskIdCommentsByCommentIdRequestObject) (gen.PutProjectsTasksByTaskIdCommentsByCommentIdResponseObject, error) {
	body := gen.CommentRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.commentScopeFor(ctx, q, req.TaskId, req.CommentId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsTasksByTaskIdCommentsByCommentId404Response{}, nil
	}
	if !scope.authoredBy(ctx) {
		return gen.PutProjectsTasksByTaskIdCommentsByCommentId403JSONResponse(forbidden()), nil
	}
	written, msg := validateCommentBody(body.Body)
	if msg != "" {
		return gen.PutProjectsTasksByTaskIdCommentsByCommentId400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("body", msg))), nil
	}

	updated, err := q.UpdateTaskComment(ctx, store.UpdateTaskCommentParams{
		Body: written, Now: s.deps.Clock(), ID: scope.Comment.ID, TaskID: scope.Task.Task.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted between the read and the write, which is the same answer as
		// never having existed.
		return gen.PutProjectsTasksByTaskIdCommentsByCommentId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: edit a comment: %w", err)
	}
	resp, err := s.commentResponseFor(ctx, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsTasksByTaskIdCommentsByCommentId200JSONResponse(resp), nil
}

// DeleteProjectsTasksByTaskIdCommentsByCommentId Delete a comment
// (DELETE /api/v1/projects/tasks/{taskId}/comments/{commentId})
//
// The author takes back their own writing, and a manager takes down anything
// that should not stand on their project (§4.1). A member who wrote nothing
// here is refused, exactly as a viewer is. It is the delete's own row count
// that decides the 404: two deletes racing must not both answer 204.
func (s *server) DeleteProjectsTasksByTaskIdCommentsByCommentId(ctx context.Context, req gen.DeleteProjectsTasksByTaskIdCommentsByCommentIdRequestObject) (gen.DeleteProjectsTasksByTaskIdCommentsByCommentIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.commentScopeFor(ctx, q, req.TaskId, req.CommentId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.DeleteProjectsTasksByTaskIdCommentsByCommentId404Response{}, nil
	}
	if !scope.authoredBy(ctx) && !scope.Task.Access.CanManage {
		return gen.DeleteProjectsTasksByTaskIdCommentsByCommentId403JSONResponse(forbidden()), nil
	}

	rows, err := q.DeleteTaskComment(ctx, store.DeleteTaskCommentParams{
		ID: scope.Comment.ID, TaskID: scope.Task.Task.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: delete a comment: %w", err)
	}
	if rows == 0 {
		return gen.DeleteProjectsTasksByTaskIdCommentsByCommentId404Response{}, nil
	}
	return gen.DeleteProjectsTasksByTaskIdCommentsByCommentId204Response{}, nil
}
