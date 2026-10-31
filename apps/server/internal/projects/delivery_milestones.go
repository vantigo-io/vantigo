package projects

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

var (
	errDeliveryMilestoneGone = errors.New("projects: the delivery milestone no longer exists")
)

type deliveryMilestoneScope struct {
	Milestone store.ProjectsDeliveryMilestone
	Project   store.ProjectsProject
	Access    access
}

func (s *server) deliveryMilestoneScopeFor(ctx context.Context, q *store.Queries, milestoneID int32) (deliveryMilestoneScope, bool, error) {
	milestone, err := q.GetDeliveryMilestone(ctx, milestoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliveryMilestoneScope{}, false, nil
	}
	if err != nil {
		return deliveryMilestoneScope{}, false, fmt.Errorf("projects: get delivery milestone: %w", err)
	}
	project, err := q.GetProject(ctx, milestone.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliveryMilestoneScope{}, false, nil
	}
	if err != nil {
		return deliveryMilestoneScope{}, false, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return deliveryMilestoneScope{}, false, err
	}
	if !a.CanSee {
		return deliveryMilestoneScope{}, false, nil
	}
	return deliveryMilestoneScope{Milestone: milestone, Project: project, Access: a}, true, nil
}

func (s *server) deliveryMilestoneResponseFromListRow(ctx context.Context, r store.ListProjectDeliveryMilestonesRow) (gen.DeliveryMilestoneResponse, error) {
	var estTotal, estComp *float64
	if r.TasksWithEstimatesCount > 0 {
		t, err := floatPtrFromNumeric(r.EstimatedHoursTotal)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		c, err := floatPtrFromNumeric(r.EstimatedHoursCompleted)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		estTotal = t
		estComp = c
	}
	var completedBy *gen.DeliveryMilestonePerson
	if r.CompletedByUserID != nil {
		entry, err := s.usersUser(ctx, *r.CompletedByUserID)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		name := unknownUser
		if entry != nil {
			name = entry.DisplayName
		}
		completedBy = &gen.DeliveryMilestonePerson{
			Id:          *r.CompletedByUserID,
			DisplayName: name,
		}
	}
	dm := store.ProjectsDeliveryMilestone{
		ID:                r.ID,
		ProjectID:         r.ProjectID,
		Name:              r.Name,
		Description:       r.Description,
		TargetDate:        r.TargetDate,
		Status:            r.Status,
		Position:          r.Position,
		CompletedAt:       r.CompletedAt,
		CompletedByUserID: r.CompletedByUserID,
		Revision:          r.Revision,
		CreatedByUserID:   r.CreatedByUserID,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
	return deliveryMilestoneResponse(dm, r.TotalTasks, r.CompletedTasks, estTotal, estComp, completedBy), nil
}

func (s *server) deliveryMilestoneResponseFromProgressRow(ctx context.Context, r store.GetDeliveryMilestoneWithProgressRow) (gen.DeliveryMilestoneResponse, error) {
	var estTotal, estComp *float64
	if r.TasksWithEstimatesCount > 0 {
		t, err := floatPtrFromNumeric(r.EstimatedHoursTotal)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		c, err := floatPtrFromNumeric(r.EstimatedHoursCompleted)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		estTotal = t
		estComp = c
	}
	var completedBy *gen.DeliveryMilestonePerson
	if r.CompletedByUserID != nil {
		entry, err := s.usersUser(ctx, *r.CompletedByUserID)
		if err != nil {
			return gen.DeliveryMilestoneResponse{}, err
		}
		name := unknownUser
		if entry != nil {
			name = entry.DisplayName
		}
		completedBy = &gen.DeliveryMilestonePerson{
			Id:          *r.CompletedByUserID,
			DisplayName: name,
		}
	}
	dm := store.ProjectsDeliveryMilestone{
		ID:                r.ID,
		ProjectID:         r.ProjectID,
		Name:              r.Name,
		Description:       r.Description,
		TargetDate:        r.TargetDate,
		Status:            r.Status,
		Position:          r.Position,
		CompletedAt:       r.CompletedAt,
		CompletedByUserID: r.CompletedByUserID,
		Revision:          r.Revision,
		CreatedByUserID:   r.CreatedByUserID,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
	return deliveryMilestoneResponse(dm, r.TotalTasks, r.CompletedTasks, estTotal, estComp, completedBy), nil
}

func deliveryMilestoneResponse(m store.ProjectsDeliveryMilestone, totalTasks, completedTasks int32, estHoursTotal, estHoursComp *float64, completedBy *gen.DeliveryMilestonePerson) gen.DeliveryMilestoneResponse {
	var progress int32
	if totalTasks > 0 {
		progress = int32(math.Round(float64(completedTasks) / float64(totalTasks) * 100))
	}
	return gen.DeliveryMilestoneResponse{
		Id:                      m.ID,
		ProjectId:               m.ProjectID,
		Name:                    m.Name,
		Description:             m.Description,
		TargetDate:              dateFromPgtype(m.TargetDate),
		Status:                  m.Status,
		Position:                m.Position,
		CompletedAt:             m.CompletedAt,
		CompletedBy:             completedBy,
		TotalTasks:              totalTasks,
		CompletedTasks:          completedTasks,
		ProgressPercent:         progress,
		EstimatedHoursTotal:     estHoursTotal,
		EstimatedHoursCompleted: estHoursComp,
		Revision:                m.Revision,
		CreatedAt:               m.CreatedAt,
		UpdatedAt:               m.UpdatedAt,
	}
}

// GetProjectsByIdDeliveryMilestones lists a project's delivery milestones
// (GET /api/v1/projects/{id}/delivery-milestones)
func (s *server) GetProjectsByIdDeliveryMilestones(ctx context.Context, req gen.GetProjectsByIdDeliveryMilestonesRequestObject) (gen.GetProjectsByIdDeliveryMilestonesResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdDeliveryMilestones404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdDeliveryMilestones404Response{}, nil
	}

	rows, err := q.ListProjectDeliveryMilestones(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list delivery milestones: %w", err)
	}

	list := make([]gen.DeliveryMilestoneResponse, 0, len(rows))
	for _, r := range rows {
		resp, err := s.deliveryMilestoneResponseFromListRow(ctx, r)
		if err != nil {
			return nil, err
		}
		list = append(list, resp)
	}
	return gen.GetProjectsByIdDeliveryMilestones200JSONResponse{Milestones: list}, nil
}

// PostProjectsByIdDeliveryMilestones creates a new delivery milestone on a project
// (POST /api/v1/projects/{id}/delivery-milestones)
func (s *server) PostProjectsByIdDeliveryMilestones(ctx context.Context, req gen.PostProjectsByIdDeliveryMilestonesRequestObject) (gen.PostProjectsByIdDeliveryMilestonesResponseObject, error) {
	body := gen.DeliveryMilestoneCreateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdDeliveryMilestones404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdDeliveryMilestones404Response{}, nil
	}
	if !a.CanContribute {
		return gen.PostProjectsByIdDeliveryMilestones403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs := validateDeliveryMilestoneCreate(body)
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdDeliveryMilestones400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var createdID int32
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire order lock: %w", err)
		}

		last, err := txq.MaxDeliveryMilestonePosition(ctx, project.ID)
		if err != nil {
			return fmt.Errorf("projects: read max delivery milestone position: %w", err)
		}

		created, err := txq.InsertDeliveryMilestone(ctx, store.InsertDeliveryMilestoneParams{
			ProjectID:       project.ID,
			Name:            parsed.Name,
			Description:     parsed.Description,
			TargetDate:      parsed.TargetDate,
			Position:        last + 1,
			CreatedByUserID: by.UserID,
			Now:             now,
		})
		if err != nil {
			return fmt.Errorf("projects: insert delivery milestone: %w", err)
		}
		createdID = created.ID

		return recordDeliveryMilestoneEvent(ctx, txq, now, eventDeliveryMilestoneAdded, created, nil, by)
	})
	if err != nil {
		return nil, err
	}

	progressRow, err := q.GetDeliveryMilestoneWithProgress(ctx, createdID)
	if err != nil {
		return nil, fmt.Errorf("projects: read created delivery milestone: %w", err)
	}
	resp, err := s.deliveryMilestoneResponseFromProgressRow(ctx, progressRow)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdDeliveryMilestones201JSONResponse(resp), nil
}

// GetProjectsDeliveryMilestonesByMilestoneId fetches a single delivery milestone by id
// (GET /api/v1/projects/delivery-milestones/{milestoneId})
func (s *server) GetProjectsDeliveryMilestonesByMilestoneId(ctx context.Context, req gen.GetProjectsDeliveryMilestonesByMilestoneIdRequestObject) (gen.GetProjectsDeliveryMilestonesByMilestoneIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.deliveryMilestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok || !scope.Access.CanSee {
		return gen.GetProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	}

	progressRow, err := q.GetDeliveryMilestoneWithProgress(ctx, req.MilestoneId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get delivery milestone with progress: %w", err)
	}

	resp, err := s.deliveryMilestoneResponseFromProgressRow(ctx, progressRow)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsDeliveryMilestonesByMilestoneId200JSONResponse(resp), nil
}

// PutProjectsDeliveryMilestonesByMilestoneId updates a delivery milestone
// (PUT /api/v1/projects/delivery-milestones/{milestoneId})
func (s *server) PutProjectsDeliveryMilestonesByMilestoneId(ctx context.Context, req gen.PutProjectsDeliveryMilestonesByMilestoneIdRequestObject) (gen.PutProjectsDeliveryMilestonesByMilestoneIdResponseObject, error) {
	body := gen.DeliveryMilestoneUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.deliveryMilestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok || !scope.Access.CanSee {
		return gen.PutProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PutProjectsDeliveryMilestonesByMilestoneId403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs := validateDeliveryMilestoneUpdate(body)
	if len(fieldErrs) > 0 {
		return gen.PutProjectsDeliveryMilestonesByMilestoneId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var updated store.ProjectsDeliveryMilestone
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := txq.LockDeliveryMilestone(ctx, req.MilestoneId)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDeliveryMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock delivery milestone: %w", err)
		}
		if locked.Revision != body.Revision {
			return revisionRefusal{current: locked.Revision}
		}

		updated, err = txq.UpdateDeliveryMilestone(ctx, store.UpdateDeliveryMilestoneParams{
			ID:          locked.ID,
			Name:        parsed.Name,
			Description: parsed.Description,
			TargetDate:  parsed.TargetDate,
			Now:         now,
		})
		if err != nil {
			return fmt.Errorf("projects: update delivery milestone: %w", err)
		}

		return recordDeliveryMilestoneEvent(ctx, txq, now, eventDeliveryMilestoneChanged, updated, nil, by)
	})

	var revConflict revisionRefusal
	switch {
	case errors.Is(err, errDeliveryMilestoneGone):
		return gen.PutProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	case errors.As(err, &revConflict):
		return gen.PutProjectsDeliveryMilestonesByMilestoneId409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision),
		), nil
	case err != nil:
		return nil, err
	}

	progressRow, err := q.GetDeliveryMilestoneWithProgress(ctx, req.MilestoneId)
	if err != nil {
		return nil, fmt.Errorf("projects: read updated delivery milestone: %w", err)
	}
	resp, err := s.deliveryMilestoneResponseFromProgressRow(ctx, progressRow)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsDeliveryMilestonesByMilestoneId200JSONResponse(resp), nil
}

// DeleteProjectsDeliveryMilestonesByMilestoneId deletes a delivery milestone
// (DELETE /api/v1/projects/delivery-milestones/{milestoneId})
func (s *server) DeleteProjectsDeliveryMilestonesByMilestoneId(ctx context.Context, req gen.DeleteProjectsDeliveryMilestonesByMilestoneIdRequestObject) (gen.DeleteProjectsDeliveryMilestonesByMilestoneIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	scope, ok, err := s.deliveryMilestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok || !scope.Access.CanSee {
		return gen.DeleteProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.DeleteProjectsDeliveryMilestonesByMilestoneId403JSONResponse(forbidden()), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire order lock: %w", err)
		}

		locked, err := txq.LockDeliveryMilestone(ctx, req.MilestoneId)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDeliveryMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock delivery milestone: %w", err)
		}

		rows, err := txq.DeleteDeliveryMilestone(ctx, req.MilestoneId)
		if err != nil {
			return fmt.Errorf("projects: delete delivery milestone: %w", err)
		}
		if rows == 0 {
			return errDeliveryMilestoneGone
		}

		remainingIDs, err := txq.DeliveryMilestoneIDs(ctx, scope.Project.ID)
		if err != nil {
			return fmt.Errorf("projects: read remaining delivery milestone ids: %w", err)
		}
		if len(remainingIDs) > 0 {
			if err := txq.RenumberDeliveryMilestones(ctx, store.RenumberDeliveryMilestonesParams{
				Ids: remainingIDs, Now: now,
			}); err != nil {
				return fmt.Errorf("projects: renumber delivery milestones: %w", err)
			}
		}

		return recordDeliveryMilestoneEvent(ctx, txq, now, eventDeliveryMilestoneRemoved, locked, nil, by)
	})

	if errors.Is(err, errDeliveryMilestoneGone) {
		return gen.DeleteProjectsDeliveryMilestonesByMilestoneId404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteProjectsDeliveryMilestonesByMilestoneId204Response{}, nil
}

// PutProjectsDeliveryMilestonesByMilestoneIdPosition renumbers a delivery milestone's position
// (PUT /api/v1/projects/delivery-milestones/{milestoneId}/position)
func (s *server) PutProjectsDeliveryMilestonesByMilestoneIdPosition(ctx context.Context, req gen.PutProjectsDeliveryMilestonesByMilestoneIdPositionRequestObject) (gen.PutProjectsDeliveryMilestonesByMilestoneIdPositionResponseObject, error) {
	body := gen.DeliveryMilestonePositionRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.deliveryMilestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok || !scope.Access.CanSee {
		return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition403JSONResponse(forbidden()), nil
	}

	if msg := validateTaskPosition(body.Position); msg != "" {
		return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("position", msg)),
		), nil
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.AcquireTaskOrderLock(ctx, store.AcquireTaskOrderLockParams{
			LockClass: taskOrderLockClass, ProjectID: scope.Project.ID,
		}); err != nil {
			return fmt.Errorf("projects: acquire order lock: %w", err)
		}

		locked, err := txq.LockDeliveryMilestone(ctx, req.MilestoneId)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDeliveryMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock delivery milestone: %w", err)
		}
		if locked.Revision != body.Revision {
			return revisionRefusal{current: locked.Revision}
		}

		ids, err := txq.DeliveryMilestoneIDs(ctx, scope.Project.ID)
		if err != nil {
			return fmt.Errorf("projects: read milestone ids: %w", err)
		}

		newIDs := make([]int32, 0, len(ids))
		for _, id := range ids {
			if id != req.MilestoneId {
				newIDs = append(newIDs, id)
			}
		}

		targetIdx := int(body.Position - 1)
		if targetIdx > len(newIDs) {
			targetIdx = len(newIDs)
		}

		finalIDs := make([]int32, 0, len(ids))
		finalIDs = append(finalIDs, newIDs[:targetIdx]...)
		finalIDs = append(finalIDs, req.MilestoneId)
		finalIDs = append(finalIDs, newIDs[targetIdx:]...)

		return txq.RenumberDeliveryMilestones(ctx, store.RenumberDeliveryMilestonesParams{
			Ids: finalIDs, Now: now,
		})
	})

	var revConflict revisionRefusal
	switch {
	case errors.Is(err, errDeliveryMilestoneGone):
		return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition404Response{}, nil
	case errors.As(err, &revConflict):
		return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision),
		), nil
	case err != nil:
		return nil, err
	}

	progressRow, err := q.GetDeliveryMilestoneWithProgress(ctx, req.MilestoneId)
	if err != nil {
		return nil, fmt.Errorf("projects: read renumbered delivery milestone: %w", err)
	}
	resp, err := s.deliveryMilestoneResponseFromProgressRow(ctx, progressRow)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsDeliveryMilestonesByMilestoneIdPosition200JSONResponse(resp), nil
}

// PostProjectsDeliveryMilestonesByMilestoneIdStatus updates a delivery milestone status
// (POST /api/v1/projects/delivery-milestones/{milestoneId}/status)
func (s *server) PostProjectsDeliveryMilestonesByMilestoneIdStatus(ctx context.Context, req gen.PostProjectsDeliveryMilestonesByMilestoneIdStatusRequestObject) (gen.PostProjectsDeliveryMilestonesByMilestoneIdStatusResponseObject, error) {
	body := gen.DeliveryMilestoneStatusRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	scope, ok, err := s.deliveryMilestoneScopeFor(ctx, q, req.MilestoneId)
	if err != nil {
		return nil, err
	}
	if !ok || !scope.Access.CanSee {
		return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus404Response{}, nil
	}
	if !scope.Access.CanContribute {
		return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus403JSONResponse(forbidden()), nil
	}

	if msg := validateDeliveryMilestoneStatus(body.Status); msg != "" {
		return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("status", msg)),
		), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var updated store.ProjectsDeliveryMilestone
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := txq.LockDeliveryMilestone(ctx, req.MilestoneId)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDeliveryMilestoneGone
		}
		if err != nil {
			return fmt.Errorf("projects: lock delivery milestone: %w", err)
		}
		if locked.Revision != body.Revision {
			return revisionRefusal{current: locked.Revision}
		}

		var compAt *time.Time
		var compBy *uuid.UUID
		if body.Status == "completed" {
			compAt = &now
			compBy = &by.UserID
		}

		updated, err = txq.UpdateDeliveryMilestoneStatus(ctx, store.UpdateDeliveryMilestoneStatusParams{
			ID:                locked.ID,
			Status:            body.Status,
			CompletedAt:       compAt,
			CompletedByUserID: compBy,
			Now:               now,
		})
		if err != nil {
			return fmt.Errorf("projects: update delivery milestone status: %w", err)
		}

		return recordDeliveryMilestoneEvent(ctx, txq, now, eventDeliveryMilestoneStatusChanged, updated, map[string]any{
			"old": locked.Status, "new": body.Status,
		}, by)
	})

	var revConflict revisionRefusal
	switch {
	case errors.Is(err, errDeliveryMilestoneGone):
		return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus404Response{}, nil
	case errors.As(err, &revConflict):
		return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus409ApplicationProblemPlusJSONResponse(
			revisionConflict(revConflict.current, body.Revision),
		), nil
	case err != nil:
		return nil, err
	}

	progressRow, err := q.GetDeliveryMilestoneWithProgress(ctx, req.MilestoneId)
	if err != nil {
		return nil, fmt.Errorf("projects: read updated delivery milestone: %w", err)
	}
	resp, err := s.deliveryMilestoneResponseFromProgressRow(ctx, progressRow)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsDeliveryMilestonesByMilestoneIdStatus200JSONResponse(resp), nil
}
