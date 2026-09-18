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

// The status change (D14). It is its own operation, rather than a field of
// the update, for two reasons that outlive this task: it gets its own
// timeline entry, which a details-changed entry listing "status" among five
// other field names would not be, and when transitions do grow rules they
// will have one place to live.

// PutProjectsByIdStatus Change a project's status
// (PUT /api/v1/projects/{id}/status)
//
// Any transition is allowed, reopening a completed project included — a
// project that came back is an ordinary thing and refusing it would only
// teach people to cancel and recreate, losing the history. Setting the
// status a project already has is not a transition at all: it answers the
// project unchanged, writes nothing, and leaves the revision alone, because
// a timeline that recorded it would be recording that somebody pressed a
// button rather than that anything happened.
func (s *server) PutProjectsByIdStatus(ctx context.Context, req gen.PutProjectsByIdStatusRequestObject) (gen.PutProjectsByIdStatusResponseObject, error) {
	body := gen.ProjectStatusChangeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsByIdStatus404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}

	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsByIdStatus404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsByIdStatus403JSONResponse(forbidden()), nil
	}

	// The status is validated after the caller's access to the project, so a
	// stranger cannot learn a project exists by sending it a bad status.
	status, msg := validateProjectStatus(body.Status)
	if msg != "" {
		return gen.PutProjectsByIdStatus400ApplicationProblemPlusJSONResponse(invalidProject(fieldError("status", msg))), nil
	}

	if status != row.Status {
		by, err := s.callerAs(ctx)
		if err != nil {
			return nil, err
		}
		now := s.deps.Clock()
		old := row.Status
		err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			var err error
			row, err = txq.UpdateProjectStatus(ctx, store.UpdateProjectStatusParams{ID: req.Id, Status: status, Now: now})
			if err != nil {
				return err
			}
			return recordStatusChanged(ctx, txq, now, row.ID, old, status, by)
		})
		if err != nil {
			return nil, fmt.Errorf("projects: change project status: %w", err)
		}
	}

	resp, err := s.projectResponseFor(ctx, q, row, a)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsByIdStatus200JSONResponse(resp), nil
}
