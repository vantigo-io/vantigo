package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a task's checklist (design §3.1, §4.1, §7): the small list of
// things one task is made of. It is a sibling group like any other — numbered
// 1..n, appended to at the end, renumbered whenever something moves or goes —
// so it follows tasks.go's ordering rules rather than inventing its own:
// moveWithin decides the new order, RenumberChecklistItems writes it, and both
// happen under the project's ordering lock (taskOrderLockClass), because a
// checklist hangs off a task and a task hangs off a project.
//
// Access is the task's, resolved once by taskScopeFor: seeing the project is
// enough to read a checklist, changing one takes access.CanContribute, and a
// caller who cannot see the project is answered the bare 404 an unknown task
// gets. An item is always addressed under its task, and an item of another
// task answers that same 404 — otherwise a caller could learn which ids exist
// by trying them on a task they can see.
//
// Checklist changes write no timeline entries (D6): the project timeline is
// the project's own history, and a task's is its comments.

// errChecklistItemGone is the refusal a change or a delete carries out of its
// transaction when the item is not (or is no longer) on the task in the path.
// It has to roll the transaction back rather than return a response from
// inside it.
var errChecklistItemGone = errors.New("projects: the checklist item no longer exists")

// GetProjectsTasksByTaskIdChecklist List a task's checklist
// (GET /api/v1/projects/tasks/{taskId}/checklist)
//
// Anyone who sees the project sees its tasks' checklists: the checklist is how
// a task is read, and the counts the task tree carries are aggregates over
// exactly these rows.
func (s *server) GetProjectsTasksByTaskIdChecklist(ctx context.Context, req gen.GetProjectsTasksByTaskIdChecklistRequestObject) (gen.GetProjectsTasksByTaskIdChecklistResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.GetProjectsTasksByTaskIdChecklist404Response{}, nil
	}

	rows, err := q.ListChecklistItems(ctx, scope.Task.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list the task's checklist: %w", err)
	}
	return gen.GetProjectsTasksByTaskIdChecklist200JSONResponse(checklistItemResponses(rows)), nil
}

// PostProjectsTasksByTaskIdChecklist Add a checklist item to a task
// (POST /api/v1/projects/tasks/{taskId}/checklist)
//
// An add appends: the new item takes the number after the last one. That
// number is read and written under the project's ordering lock, because two
// adds racing for the end of one checklist would otherwise both read the same
// maximum and both write the number after it — and no row lock can cover that,
// since what they race for is the gap after the last row.
//
// The task itself is held for the transaction, so an item can never be written
// onto a task that is being deleted at the same moment.
func (s *server) PostProjectsTasksByTaskIdChecklist(ctx context.Context, req gen.PostProjectsTasksByTaskIdChecklistRequestObject) (gen.PostProjectsTasksByTaskIdChecklistResponseObject, error) {
	body := gen.ChecklistItemRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostProjectsTasksByTaskIdChecklist404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PostProjectsTasksByTaskIdChecklist403JSONResponse(forbidden()), nil
	}
	text, msg := validateChecklistText(body.Text)
	if msg != "" {
		return gen.PostProjectsTasksByTaskIdChecklist400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("text", msg))), nil
	}

	now := s.deps.Clock()
	var created store.ProjectsTaskChecklistItem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: take the task ordering lock: %w", err)
		}
		if _, err := txq.LockTask(ctx, scope.Task.ID); errors.Is(err, pgx.ErrNoRows) {
			return errTaskGone
		} else if err != nil {
			return fmt.Errorf("projects: lock task: %w", err)
		}
		last, err := txq.MaxChecklistPosition(ctx, scope.Task.ID)
		if err != nil {
			return fmt.Errorf("projects: read the last checklist item's position: %w", err)
		}
		created, err = txq.InsertChecklistItem(ctx, store.InsertChecklistItemParams{
			TaskID: scope.Task.ID, Text: text, Position: last + 1, Now: now,
		})
		return err
	})
	if errors.Is(err, errTaskGone) {
		return gen.PostProjectsTasksByTaskIdChecklist404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: add a checklist item: %w", err)
	}
	return gen.PostProjectsTasksByTaskIdChecklist201JSONResponse(checklistItemResponse(created)), nil
}

// PutProjectsTasksByTaskIdChecklistByItemId Change a checklist item
// (PUT /api/v1/projects/tasks/{taskId}/checklist/{itemId})
//
// One operation covers everything an item can become — its text, whether it is
// ticked off, and where it sits — because from the caller's side there is one
// intention: this item should now read like this. Every field is optional and
// an absent one leaves that part of the item as it stands, so ticking an item
// off from a drawer somebody left open cannot undo a rename made since.
//
// What the untouched fields become is decided from the row the transaction
// holds rather than from the row the handler read: between the two another
// writer can commit, and only the database can decide that race. A move
// renumbers the whole list 1..n under the project's ordering lock, so the
// checklist never shows a gap or two items sharing a number.
func (s *server) PutProjectsTasksByTaskIdChecklistByItemId(ctx context.Context, req gen.PutProjectsTasksByTaskIdChecklistByItemIdRequestObject) (gen.PutProjectsTasksByTaskIdChecklistByItemIdResponseObject, error) {
	body := gen.ChecklistItemUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsTasksByTaskIdChecklistByItemId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PutProjectsTasksByTaskIdChecklistByItemId403JSONResponse(forbidden()), nil
	}

	// Both rules run regardless of the other, so one round trip reports every
	// problem with the body.
	errs := map[string][]string{}
	var text *string
	if body.Text != nil {
		trimmed, msg := validateChecklistText(*body.Text)
		if msg != "" {
			errs = withFieldError(errs, "text", msg)
		}
		text = &trimmed
	}
	if body.Position != nil {
		if msg := validateTaskPosition(*body.Position); msg != "" {
			errs = withFieldError(errs, "position", msg)
		}
	}
	if len(errs) > 0 {
		return gen.PutProjectsTasksByTaskIdChecklistByItemId400ApplicationProblemPlusJSONResponse(
			invalidProject(errs)), nil
	}

	now := s.deps.Clock()
	var item store.ProjectsTaskChecklistItem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The ordering lock is taken only by a change that moves the item, and
		// always before the item's own row, so a change that renumbers and one
		// that merely ticks an item off can never take the two in opposite
		// orders.
		if body.Position != nil {
			if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
				LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
			}); err != nil {
				return fmt.Errorf("projects: take the task ordering lock: %w", err)
			}
		}
		before, err := txq.LockChecklistItem(ctx, store.LockChecklistItemParams{
			ID: req.ItemId, TaskID: scope.Task.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errChecklistItemGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock the checklist item: %w", err)
		}

		item = before
		if text != nil || body.Done != nil {
			done := before.Done
			if body.Done != nil {
				done = *body.Done
			}
			written := before.Text
			if text != nil {
				written = *text
			}
			item, err = txq.UpdateChecklistItem(ctx, store.UpdateChecklistItemParams{
				Text: written, Done: done, Now: now, ID: before.ID, TaskID: scope.Task.ID,
			})
			if err != nil {
				return fmt.Errorf("projects: change the checklist item: %w", err)
			}
		}
		if body.Position == nil {
			return nil
		}

		// The whole list, held row by row, reordered in Go and written back as
		// 1..n — then read again, because the number this item ended up with is
		// the renumbering's answer and not the request's.
		ids, err := txq.ChecklistItemIDs(ctx, scope.Task.ID)
		if err != nil {
			return fmt.Errorf("projects: lock the task's checklist: %w", err)
		}
		if err := txq.RenumberChecklistItems(ctx, store.RenumberChecklistItemsParams{
			Ids: moveWithin(ids, before.ID, *body.Position), Now: now,
		}); err != nil {
			return fmt.Errorf("projects: renumber the task's checklist: %w", err)
		}
		item, err = txq.GetChecklistItem(ctx, store.GetChecklistItemParams{
			ID: before.ID, TaskID: scope.Task.ID,
		})
		if err != nil {
			return fmt.Errorf("projects: read the checklist item back: %w", err)
		}
		return nil
	})
	if errors.Is(err, errChecklistItemGone) {
		return gen.PutProjectsTasksByTaskIdChecklistByItemId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: change a checklist item: %w", err)
	}
	return gen.PutProjectsTasksByTaskIdChecklistByItemId200JSONResponse(checklistItemResponse(item)), nil
}

// DeleteProjectsTasksByTaskIdChecklistByItemId Delete a checklist item
// (DELETE /api/v1/projects/tasks/{taskId}/checklist/{itemId})
//
// A checklist item is deleted rather than archived — there is nothing in one
// worth keeping once it is gone — and the rest of the list closes the gap it
// left, in the same transaction and under the project's ordering lock. It is
// the delete's own row count that decides the 404: two deletes racing must not
// both answer 204.
func (s *server) DeleteProjectsTasksByTaskIdChecklistByItemId(ctx context.Context, req gen.DeleteProjectsTasksByTaskIdChecklistByItemIdRequestObject) (gen.DeleteProjectsTasksByTaskIdChecklistByItemIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.taskScopeFor(ctx, q, req.TaskId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.DeleteProjectsTasksByTaskIdChecklistByItemId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.DeleteProjectsTasksByTaskIdChecklistByItemId403JSONResponse(forbidden()), nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: take the task ordering lock: %w", err)
		}
		rows, err := txq.DeleteChecklistItem(ctx, store.DeleteChecklistItemParams{
			ID: req.ItemId, TaskID: scope.Task.ID,
		})
		if err != nil {
			return fmt.Errorf("projects: delete the checklist item: %w", err)
		}
		if rows == 0 {
			return errChecklistItemGone
		}
		ids, err := txq.ChecklistItemIDs(ctx, scope.Task.ID)
		if err != nil {
			return fmt.Errorf("projects: lock the task's checklist: %w", err)
		}
		if err := txq.RenumberChecklistItems(ctx, store.RenumberChecklistItemsParams{
			Ids: ids, Now: now,
		}); err != nil {
			return fmt.Errorf("projects: renumber the task's checklist: %w", err)
		}
		return nil
	})
	if errors.Is(err, errChecklistItemGone) {
		return gen.DeleteProjectsTasksByTaskIdChecklistByItemId404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteProjectsTasksByTaskIdChecklistByItemId204Response{}, nil
}
