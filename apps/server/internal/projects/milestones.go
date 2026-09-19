package projects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a project's invoice plan (design §2 E1/E5, §3.2): the billing
// milestones it expects to bill, in a manual order, each moving through
// planned → ready → invoiced with cancelled off to the side.
//
// Four things run through every operation here.
//
// **A milestone is money.** Reading one already takes financial rights on the
// project, so the access ladder has three rungs rather than two: an outsider
// gets the bare 404 an unknown id gets (D7), a member who sees the project
// but not its money gets 403 — the milestone's existence is not the secret,
// its amount is — and writing takes being the project's manager. The one
// exception is the move to and from 'invoiced', which financial rights alone
// are enough for (milestoneMoves): whoever may see the money may say it was
// billed.
//
// **A milestone is addressed by its own id**, not by its project's:
// /projects/milestones/{milestoneId} resolves the project by loading the
// milestone first (milestoneScopeFor), which is also why an unknown milestone
// and a milestone on an invisible project answer identically — the shape
// tasks.go uses for /projects/tasks/{taskId}.
//
// **Every write locks the project first.** What a milestone may be depends on
// the project — it needs a currency, and a percent one needs a fixed price —
// and the project's own update can take either away (design §3.3). So every
// write here opens its transaction with LockProject, re-decides the
// currency-dependent rules against the row that lock returns, and only then
// takes the milestone's own row lock; the ordering advisory lock, when a
// write needs one at all, comes last. Project row → milestone row → advisory,
// in that order on every path, is what keeps two writers queuing rather than
// deadlocking. A contracts directory (deps.Users) is never read inside one of
// these transactions: the caller is resolved before it and the stamps' names
// after it.
//
// **Everything the caller may do is answered back** in the milestone's own
// `capabilities`, so the frontend renders buttons from the API rather than
// from a second copy of the move table.

// milestoneOrderLockClass is the class every milestone-ordering advisory lock
// is taken in, with the project's id as the object — one lock per project,
// held for the transaction that is deciding a position in it. Tasks use 9;
// Postgres keeps two-argument advisory locks in a lock space of their own, so
// neither can collide with identity's single-argument ones.
const milestoneOrderLockClass = 10

// errMilestoneGone is what LockMilestone finding no row means: the milestone
// was deleted between the handler's own read and its transaction, which is
// the same answer as never having existed.
var errMilestoneGone = errors.New("projects: the milestone no longer exists")

// errMilestoneMoveForbidden carries the status move's own 403 out of its
// transaction. Which right a move needs is a property of the move, and the
// move is only settled under the milestone's lock, so the denial has to roll
// the transaction back rather than return a response from inside it — the
// same reason fieldRefusal exists for a 400 decided there.
var errMilestoneMoveForbidden = errors.New("projects: the caller may not make this move")

// milestoneScope is one milestone, the project it belongs to and what the
// caller may do with that project: everything the five milestone-scoped
// operations decide from, resolved once.
type milestoneScope struct {
	Milestone store.ProjectsBillingMilestone
	Project   store.ProjectsProject
	Access    access
}

// milestoneScopeFor loads a milestone addressed by its own id and resolves
// the caller's access to the project it belongs to. It reports false for a
// milestone nobody has *and* for a milestone on a project the caller cannot
// see, because those two must be indistinguishable (D7): the caller learns
// nothing about a project they hold no role on, not even that one of its
// milestones exists.
//
// Financial rights are deliberately *not* part of it. A caller who can see
// the project but not its money is a different answer — 403, decided by each
// operation — and folding it in here would turn it into a 404 that tells a
// member their own project has no such milestone.
func (s *server) milestoneScopeFor(ctx context.Context, q *store.Queries, milestoneID int32) (milestoneScope, bool, error) {
	milestone, err := q.GetMilestone(ctx, milestoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return milestoneScope{}, false, nil
	}
	if err != nil {
		return milestoneScope{}, false, fmt.Errorf("projects: get milestone: %w", err)
	}
	project, err := q.GetProject(ctx, milestone.ProjectID)
	if err != nil {
		return milestoneScope{}, false, fmt.Errorf("projects: get the milestone's project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return milestoneScope{}, false, err
	}
	if !a.CanSee {
		return milestoneScope{}, false, nil
	}
	return milestoneScope{Milestone: milestone, Project: project, Access: a}, true, nil
}

// GetProjectsByIdMilestones List a project's billing milestones
// (GET /api/v1/projects/{id}/milestones)
//
// The plan, in the project's manual order with cancelled milestones last, and
// what it adds up to. Financial rights, not merely seeing the project: every
// row of it is an amount.
func (s *server) GetProjectsByIdMilestones(ctx context.Context, req gen.GetProjectsByIdMilestonesRequestObject) (gen.GetProjectsByIdMilestonesResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdMilestones404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdMilestones404Response{}, nil
	}
	if !a.canSeeMilestones() {
		return gen.GetProjectsByIdMilestones403JSONResponse(forbidden()), nil
	}

	rows, err := q.ListProjectMilestones(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list milestones: %w", err)
	}
	plan, err := s.milestonePlanResponse(ctx, project, a, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsByIdMilestones200JSONResponse(plan), nil
}

// PostProjectsByIdMilestones Add a billing milestone to a project
// (POST /api/v1/projects/{id}/milestones)
//
// A create appends: the new milestone takes the number after the last one.
// That number is read and written under the project's ordering advisory lock,
// because two creates racing for the end of the same plan would otherwise
// both read the same maximum and both write the number after it — and no row
// lock can cover that, since what they race for is the gap after the last row.
//
// The project's own row lock comes first all the same, and the body is
// re-validated against the row it returns: whether this project has a
// currency at all, and a fixed price for a percent to be a share of, is
// exactly what a concurrent update of the project can take away (design
// §3.3), and only a lock both writers take serialises them.
func (s *server) PostProjectsByIdMilestones(ctx context.Context, req gen.PostProjectsByIdMilestonesRequestObject) (gen.PostProjectsByIdMilestonesResponseObject, error) {
	body := gen.BillingMilestoneRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdMilestones404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdMilestones404Response{}, nil
	}
	if !a.canManageMilestones() {
		return gen.PostProjectsByIdMilestones403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := validateMilestone(body, project)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdMilestones400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var locked store.ProjectsProject
	var created store.ProjectsBillingMilestone
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		locked, err = s.lockProjectFor(ctx, txq, project.ID, body)
		if err != nil {
			return err
		}
		if err := txq.AcquireMilestoneOrderLock(ctx, store.AcquireMilestoneOrderLockParams{
			LockClass: milestoneOrderLockClass, ProjectID: locked.ID,
		}); err != nil {
			return fmt.Errorf("projects: take the milestone ordering lock: %w", err)
		}
		last, err := txq.MaxMilestonePosition(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("projects: read the last milestone's position: %w", err)
		}
		created, err = txq.InsertMilestone(ctx, store.InsertMilestoneParams{
			ProjectID:       locked.ID,
			Name:            parsed.Name,
			Description:     parsed.Description,
			PlannedDate:     parsed.PlannedDate,
			Amount:          parsed.Amount,
			Percent:         parsed.Percent,
			Position:        last + 1,
			CreatedByUserID: by.UserID,
			Now:             now,
		})
		if err != nil {
			return fmt.Errorf("projects: insert milestone: %w", err)
		}
		return recordMilestoneEvent(ctx, txq, now, eventMilestoneAdded, created, by)
	})
	var refusal fieldRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PostProjectsByIdMilestones400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.Is(err, errProjectVanished):
		return gen.PostProjectsByIdMilestones404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("projects: create milestone: %w", err)
	}

	resp, err := s.milestoneResponseFor(ctx, locked, a, created)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdMilestones201JSONResponse(resp), nil
}

// lockProjectFor takes the project's row lock — the first statement of every
// milestone write — and re-runs the body's currency-dependent rules against
// the row it returns. It is the one place design §3.3's "decide under the
// project's lock" is spelled out for milestones, so no path can forget half
// of it, and it answers the locked row every caller then writes against.
//
// Only the rules that depend on the project are re-asked; everything else
// about the body is a property of the body alone and cannot have moved since
// the handler validated it. body is the create's shape, which an update is
// converted into (milestoneFromUpdate), so one function serves both.
func (s *server) lockProjectFor(ctx context.Context, txq *store.Queries, projectID int32, body gen.BillingMilestoneRequest) (store.ProjectsProject, error) {
	locked, err := txq.LockProject(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ProjectsProject{}, errProjectVanished
	}
	if err != nil {
		return store.ProjectsProject{}, fmt.Errorf("projects: lock project: %w", err)
	}
	if _, lockedErrs, err := validateMilestone(body, locked); err != nil {
		return store.ProjectsProject{}, err
	} else if len(lockedErrs) > 0 {
		return store.ProjectsProject{}, fieldRefusal{errs: lockedErrs}
	}
	return locked, nil
}

// GetProjectsMilestonesByMilestoneId Get a billing milestone by id
// (GET /api/v1/projects/milestones/{milestoneId})
func (s *server) GetProjectsMilestonesByMilestoneId(ctx context.Context, req gen.GetProjectsMilestonesByMilestoneIdRequestObject) (gen.GetProjectsMilestonesByMilestoneIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.milestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.GetProjectsMilestonesByMilestoneId404Response{}, nil
	}
	if !scope.Access.canSeeMilestones() {
		return gen.GetProjectsMilestonesByMilestoneId403JSONResponse(forbidden()), nil
	}

	resp, err := s.milestoneResponseFor(ctx, scope.Project, scope.Access, scope.Milestone)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsMilestonesByMilestoneId200JSONResponse(resp), nil
}

// PutProjectsMilestonesByMilestoneId Update a billing milestone
// (PUT /api/v1/projects/milestones/{milestoneId})
//
// An update carries every field of the milestone as it should stand
// afterwards, plus the revision the caller read it at. Where it sits and what
// status it is in are deliberately not part of it: position is the move's and
// status is the status operation's, so an edit saved from a form somebody
// left open cannot undo a reordering or a marking made since.
//
// The revision and §3.2's read-only rule are both decided against the row
// this transaction holds under LockMilestone, not against the one the handler
// read: whether the milestone has since been invoiced by somebody else is
// exactly the question a check taken beforehand would miss.
func (s *server) PutProjectsMilestonesByMilestoneId(ctx context.Context, req gen.PutProjectsMilestonesByMilestoneIdRequestObject) (gen.PutProjectsMilestonesByMilestoneIdResponseObject, error) {
	body := gen.BillingMilestoneUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.milestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsMilestonesByMilestoneId404Response{}, nil
	}
	if !scope.Access.canManageMilestones() {
		return gen.PutProjectsMilestonesByMilestoneId403JSONResponse(forbidden()), nil
	}

	content := milestoneFromUpdate(body)
	parsed, fieldErrs, err := validateMilestone(content, scope.Project)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsMilestonesByMilestoneId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	now := s.deps.Clock()
	var locked store.ProjectsProject
	var changed store.ProjectsBillingMilestone
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		locked, err = s.lockProjectFor(ctx, txq, scope.Project.ID, content)
		if err != nil {
			return err
		}
		before, err := txq.LockMilestone(ctx, scope.Milestone.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock milestone: %w", err)
		}
		if before.Revision != body.Revision {
			return revisionRefusal{current: before.Revision}
		}
		if before.Status != milestoneStatusPlanned && before.Status != milestoneStatusReady {
			return singleFieldRefusal("status", milestoneNotEditable(before.Status))
		}
		changed, err = txq.UpdateMilestone(ctx, store.UpdateMilestoneParams{
			ID:          before.ID,
			Name:        parsed.Name,
			Description: parsed.Description,
			PlannedDate: parsed.PlannedDate,
			Amount:      parsed.Amount,
			Percent:     parsed.Percent,
			Now:         now,
		})
		return err
	})
	var refusal fieldRefusal
	var revConflict revisionRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PutProjectsMilestonesByMilestoneId400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.As(err, &revConflict):
		return gen.PutProjectsMilestonesByMilestoneId409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision)), nil
	case errors.Is(err, errProjectVanished), errors.Is(err, errMilestoneGone):
		return gen.PutProjectsMilestonesByMilestoneId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("projects: update milestone: %w", err)
	}

	resp, err := s.milestoneResponseFor(ctx, locked, scope.Access, changed)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsMilestonesByMilestoneId200JSONResponse(resp), nil
}

// DeleteProjectsMilestonesByMilestoneId Delete a billing milestone
// (DELETE /api/v1/projects/milestones/{milestoneId})
//
// A mis-created milestone is deleted; anything that has ever moved is
// cancelled instead (§3.2), so the plan keeps the record of what was billed
// and what was dropped. The delete's own row count is what decides the 404 —
// two deletes racing must not both answer 204 — and it renumbers what is left
// so a removed milestone closes its gap, exactly as a task's delete does.
//
// The project is locked first even though nothing here reads its currency: a
// fixed lock order across every writer that can take more than one lock is
// what keeps two of them queuing rather than deadlocking, and the create,
// the update and the status move all take this one first.
func (s *server) DeleteProjectsMilestonesByMilestoneId(ctx context.Context, req gen.DeleteProjectsMilestonesByMilestoneIdRequestObject) (gen.DeleteProjectsMilestonesByMilestoneIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.milestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.DeleteProjectsMilestonesByMilestoneId404Response{}, nil
	}
	if !scope.Access.canManageMilestones() {
		return gen.DeleteProjectsMilestonesByMilestoneId403JSONResponse(forbidden()), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	deleted := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockProject(ctx, scope.Project.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errProjectVanished
			}
			return fmt.Errorf("projects: lock project: %w", err)
		}
		milestone, err := txq.LockMilestone(ctx, scope.Milestone.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("projects: lock milestone: %w", err)
		}
		// Decided from the row this transaction holds: whether the milestone
		// has moved since the handler read it is exactly what another
		// manager's status change can answer differently.
		if milestone.Status != milestoneStatusPlanned || milestone.EverMoved {
			return singleFieldRefusal("status", milestoneNotDeletable())
		}
		if err := txq.AcquireMilestoneOrderLock(ctx, store.AcquireMilestoneOrderLockParams{
			LockClass: milestoneOrderLockClass, ProjectID: milestone.ProjectID,
		}); err != nil {
			return fmt.Errorf("projects: take the milestone ordering lock: %w", err)
		}
		rows, err := txq.DeleteMilestone(ctx, milestone.ID)
		if err != nil {
			return fmt.Errorf("projects: delete milestone: %w", err)
		}
		if rows == 0 {
			return nil
		}
		deleted = true
		ids, err := txq.MilestoneIDs(ctx, milestone.ProjectID)
		if err != nil {
			return fmt.Errorf("projects: lock the remaining milestones: %w", err)
		}
		if err := txq.RenumberMilestones(ctx, store.RenumberMilestonesParams{Ids: ids, Now: now}); err != nil {
			return fmt.Errorf("projects: renumber the remaining milestones: %w", err)
		}
		return recordMilestoneEvent(ctx, txq, now, eventMilestoneRemoved, milestone, by)
	})
	var refusal fieldRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.DeleteProjectsMilestonesByMilestoneId400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.Is(err, errProjectVanished):
		return gen.DeleteProjectsMilestonesByMilestoneId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("projects: delete milestone: %w", err)
	}
	if !deleted {
		return gen.DeleteProjectsMilestonesByMilestoneId404Response{}, nil
	}
	return gen.DeleteProjectsMilestonesByMilestoneId204Response{}, nil
}

// PutProjectsMilestonesByMilestoneIdPosition Move a billing milestone in the plan
// (PUT /api/v1/projects/milestones/{milestoneId}/position)
//
// The whole plan is renumbered 1..n inside one transaction, under the
// project's ordering lock and with the milestone rows themselves held: two
// moves otherwise interleave two renumberings and leave a gap or a duplicate.
// Cancelled milestones are renumbered along with the rest — they keep their
// place in the plan and only sort last when it is read.
//
// Unlike a task's move this one carries a revision, because a milestone's
// place in the invoice plan is part of what the plan says: dragging one
// against a copy of the plan somebody else has already reordered is the
// conflict the guard exists for. The renumbering itself moves no revision,
// so it never makes an open edit form stale.
func (s *server) PutProjectsMilestonesByMilestoneIdPosition(ctx context.Context, req gen.PutProjectsMilestonesByMilestoneIdPositionRequestObject) (gen.PutProjectsMilestonesByMilestoneIdPositionResponseObject, error) {
	body := gen.BillingMilestonePositionRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.milestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutProjectsMilestonesByMilestoneIdPosition404Response{}, nil
	}
	if !scope.Access.canManageMilestones() {
		return gen.PutProjectsMilestonesByMilestoneIdPosition403JSONResponse(forbidden()), nil
	}
	// The position rule is the module's one, shared with a task among its
	// siblings and a checklist item among its task's: 1-based, no upper
	// bound, because a position past the end means last.
	if msg := validateTaskPosition(body.Position); msg != "" {
		return gen.PutProjectsMilestonesByMilestoneIdPosition400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("position", msg))), nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockProject(ctx, scope.Project.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errProjectVanished
			}
			return fmt.Errorf("projects: lock project: %w", err)
		}
		milestone, err := txq.LockMilestone(ctx, scope.Milestone.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock milestone: %w", err)
		}
		if milestone.Revision != body.Revision {
			return revisionRefusal{current: milestone.Revision}
		}
		if err := txq.AcquireMilestoneOrderLock(ctx, store.AcquireMilestoneOrderLockParams{
			LockClass: milestoneOrderLockClass, ProjectID: milestone.ProjectID,
		}); err != nil {
			return fmt.Errorf("projects: take the milestone ordering lock: %w", err)
		}
		ids, err := txq.MilestoneIDs(ctx, milestone.ProjectID)
		if err != nil {
			return fmt.Errorf("projects: lock the project's milestones: %w", err)
		}
		if err := txq.RenumberMilestones(ctx, store.RenumberMilestonesParams{
			Ids: moveWithin(ids, milestone.ID, body.Position), Now: now,
		}); err != nil {
			return fmt.Errorf("projects: renumber the project's milestones: %w", err)
		}
		return nil
	})
	var revConflict revisionRefusal
	switch {
	case errors.As(err, &revConflict):
		return gen.PutProjectsMilestonesByMilestoneIdPosition409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision)), nil
	case errors.Is(err, errProjectVanished), errors.Is(err, errMilestoneGone):
		return gen.PutProjectsMilestonesByMilestoneIdPosition404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("projects: move milestone: %w", err)
	}

	moved, err := q.GetMilestone(ctx, scope.Milestone.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsMilestonesByMilestoneIdPosition404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: read the moved milestone back: %w", err)
	}
	resp, err := s.milestoneResponseFor(ctx, scope.Project, scope.Access, moved)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsMilestonesByMilestoneIdPosition200JSONResponse(resp), nil
}

// PostProjectsMilestonesByMilestoneIdStatus Move a billing milestone through its status flow
// (POST /api/v1/projects/milestones/{milestoneId}/status)
//
// E5's manual invoicing and the flow around it. Everything the move itself
// decides — whether the pair is in the table, which right it needs, and
// whether a reference and a date are allowed — is decided against the row the
// transaction holds, *after* the revision has been checked against it, and is
// carried back out as a refusal. Only what the body can be judged on alone
// (the status is one of the four, the reference is short enough) is settled
// before the transaction opens.
//
// Deciding it any earlier gets the answer wrong in exactly the case the
// revision guard exists for: two people acting on one plan at once. The loser
// would be told "a milestone cannot move from cancelled to ready", which is
// true of the row but says nothing about what actually happened, instead of
// the 409 that tells them to re-read the plan.
//
// Marking invoiced freezes the effective amount as it stands at that instant
// (§3.2), which is why this transaction needs the project's own row under
// LockProject: the frozen number is the fixed price times the percent, and
// the fixed price is the project's to change.
func (s *server) PostProjectsMilestonesByMilestoneIdStatus(ctx context.Context, req gen.PostProjectsMilestonesByMilestoneIdStatusRequestObject) (gen.PostProjectsMilestonesByMilestoneIdStatusResponseObject, error) {
	body := gen.BillingMilestoneStatusRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.milestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostProjectsMilestonesByMilestoneIdStatus404Response{}, nil
	}
	// Reading a milestone at all takes financial rights, so a caller without
	// them is refused before the body is looked at — otherwise the shape of
	// the refusal would tell a member what status the milestone is in.
	if !scope.Access.canSeeMilestones() {
		return gen.PostProjectsMilestonesByMilestoneIdStatus403JSONResponse(forbidden()), nil
	}

	status, msg := validateMilestoneStatus(body.Status)
	if msg != "" {
		return gen.PostProjectsMilestonesByMilestoneIdStatus400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("status", msg))), nil
	}
	reference, msg := validateInvoiceReference(body.InvoiceReference)
	if msg != "" {
		return gen.PostProjectsMilestonesByMilestoneIdStatus400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("invoiceReference", msg))), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var locked store.ProjectsProject
	var moved store.ProjectsBillingMilestone
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		locked, err = txq.LockProject(ctx, scope.Project.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errProjectVanished
		}
		if err != nil {
			return fmt.Errorf("projects: lock project: %w", err)
		}
		before, err := txq.LockMilestone(ctx, scope.Milestone.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock milestone: %w", err)
		}
		if before.Revision != body.Revision {
			return revisionRefusal{current: before.Revision}
		}
		// The move itself, decided against the locked row and only once the
		// revision has said the caller is looking at that row.
		under, allowed := milestoneMoveFor(before.Status, status)
		if !allowed {
			return singleFieldRefusal("status", milestoneMoveNotAllowed(before.Status, status))
		}
		if under.Right == rightManager && !scope.Access.canManageMilestones() {
			return errMilestoneMoveForbidden
		}
		// The reference and the date belong to the invoicing and to nothing
		// else, so which move this is decides whether they are accepted.
		if !under.SetInvoice {
			invoiceErrs := map[string][]string{}
			if body.InvoiceReference != nil {
				invoiceErrs["invoiceReference"] = []string{invoiceFieldNotAllowed("reference")}
			}
			if body.InvoiceDate != nil {
				invoiceErrs["invoiceDate"] = []string{invoiceFieldNotAllowed("date")}
			}
			if len(invoiceErrs) > 0 {
				return fieldRefusal{errs: invoiceErrs}
			}
		}

		params, err := milestoneStatusParams(before, locked, under, status, reference, body.InvoiceDate, by, now)
		if err != nil {
			return err
		}
		moved, err = txq.UpdateMilestoneStatus(ctx, params)
		if err != nil {
			return fmt.Errorf("projects: move milestone status: %w", err)
		}
		return recordMilestoneEvent(ctx, txq, now, under.Event, moved, by)
	})
	var refusal fieldRefusal
	var revConflict revisionRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PostProjectsMilestonesByMilestoneIdStatus400ApplicationProblemPlusJSONResponse(
			invalidProject(refusal.errs)), nil
	case errors.As(err, &revConflict):
		return gen.PostProjectsMilestonesByMilestoneIdStatus409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision)), nil
	case errors.Is(err, errMilestoneMoveForbidden):
		return gen.PostProjectsMilestonesByMilestoneIdStatus403JSONResponse(forbidden()), nil
	case errors.Is(err, errProjectVanished), errors.Is(err, errMilestoneGone):
		return gen.PostProjectsMilestonesByMilestoneIdStatus404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("projects: move milestone status: %w", err)
	}

	resp, err := s.milestoneResponseFor(ctx, locked, scope.Access, moved)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsMilestonesByMilestoneIdStatus200JSONResponse(resp), nil
}

// milestoneStatusParams is one move turned into the row it writes: the new
// status, and whichever of the two sets of stamps the move sets, clears or
// leaves alone (milestoneMoves). Everything the move does not touch is
// carried over from the locked row rather than left out, because
// UpdateMilestoneStatus writes every one of those columns unconditionally —
// one statement, so a move can never half-apply.
//
// The frozen amount is computed here, from the locked project and the locked
// milestone, which is what makes E5's "freezes the amount" true at exactly
// the instant the move commits.
func milestoneStatusParams(
	before store.ProjectsBillingMilestone,
	project store.ProjectsProject,
	move milestoneMove,
	status string,
	reference *string,
	invoiceDate *openapi_types.Date,
	by actor,
	now time.Time,
) (store.UpdateMilestoneStatusParams, error) {
	params := store.UpdateMilestoneStatusParams{
		ID:               before.ID,
		Status:           status,
		ReadyAt:          before.ReadyAt,
		ReadyByUserID:    before.ReadyByUserID,
		InvoicedAt:       before.InvoicedAt,
		InvoicedByUserID: before.InvoicedByUserID,
		InvoiceReference: before.InvoiceReference,
		InvoiceDate:      before.InvoiceDate,
		InvoicedAmount:   before.InvoicedAmount,
		Now:              now,
	}
	switch {
	case move.SetReady:
		stamped := now
		params.ReadyAt, params.ReadyByUserID = &stamped, &by.UserID
	case move.ClearReady:
		params.ReadyAt, params.ReadyByUserID = nil, nil
	}
	switch {
	case move.SetInvoice:
		amount, err := milestoneEffectiveAmount(before, project)
		if err != nil {
			return store.UpdateMilestoneStatusParams{}, err
		}
		frozen, err := numericFromFloat(amount)
		if err != nil {
			return store.UpdateMilestoneStatusParams{}, err
		}
		stamped := now
		params.InvoicedAt, params.InvoicedByUserID = &stamped, &by.UserID
		params.InvoiceReference, params.InvoiceDate = reference, dateToPgtype(invoiceDate)
		params.InvoicedAmount = frozen
	case move.ClearInvoice:
		params.InvoicedAt, params.InvoicedByUserID = nil, nil
		params.InvoiceReference, params.InvoiceDate = nil, pgtype.Date{}
		params.InvoicedAmount = pgtype.Numeric{}
	}
	return params, nil
}
