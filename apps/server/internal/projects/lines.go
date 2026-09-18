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

// This file is a project's billing lines (D9, D10, D15). A line is "variant +
// pricing rule", and that is all it is: Projects stores which variant an hour
// is billed against and by what rule, and Invoices resolves the amount when
// it bills. Nothing here multiplies anything.
//
// Every one of the three operations is ordered the same way — the project,
// then the caller's access to it (404 before 403), then whether this
// installation has billing lines at all, then the body. The products check
// comes *after* the access gates on purpose: 409 says "this installation has
// no products module", and a stranger must not be able to learn that a
// project exists by getting that answer instead of a 404.
//
// There is no DELETE. A line other modules may already have billed against is
// deactivated through PUT `active: false` and reactivated the same way, and a
// deactivated line stays listed: the rule behind an hour logged last month is
// still the answer to what that hour cost.

// errLineNotFound carries "this project has no such line" out of the change's
// transaction. Whether the line exists is only known under the row lock the
// transaction takes, and a miss has to roll back rather than return a
// response from inside it.
var errLineNotFound = errors.New("projects: the project has no such billing line")

// GetProjectsByIdBillingLines List a project's billing lines
// (GET /api/v1/projects/{id}/billing-lines)
//
// Anyone who sees the project sees its lines: which products it bills and in
// what unit is how a member reads their own project (D15). What they do not
// see is the pricing block, which is shaped out for a caller who may not see
// the project's financials (D12).
func (s *server) GetProjectsByIdBillingLines(ctx context.Context, req gen.GetProjectsByIdBillingLinesRequestObject) (gen.GetProjectsByIdBillingLinesResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdBillingLines404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdBillingLines404Response{}, nil
	}
	if !s.billingLinesAvailable() {
		return gen.GetProjectsByIdBillingLines409ApplicationProblemPlusJSONResponse(productsDisabled()), nil
	}

	lines, err := q.ListBillingLines(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list billing lines: %w", err)
	}
	data, err := s.billingLineResponses(ctx, row, a, lines)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsByIdBillingLines200JSONResponse(data), nil
}

// PostProjectsByIdBillingLines Add a billing line to a project
// (POST /api/v1/projects/{id}/billing-lines)
//
// Lines are allowed on a project of any billing type: what a project bills by
// and what it is priced from are separate questions, and a non-billable
// project still wants to record which products its hours went to.
//
// The code's uniqueness inside the project is enforced by
// ux_billing_lines_project_id_code, not by a read-then-write check, exactly
// as a project's own code is: two managers adding the same code both reach
// the insert, one wins and the other's 23505 becomes the ordinary `code`
// field error.
func (s *server) PostProjectsByIdBillingLines(ctx context.Context, req gen.PostProjectsByIdBillingLinesRequestObject) (gen.PostProjectsByIdBillingLinesResponseObject, error) {
	body := gen.BillingLineRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdBillingLines404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdBillingLines404Response{}, nil
	}
	if !a.CanManage {
		return gen.PostProjectsByIdBillingLines403JSONResponse(forbidden()), nil
	}
	if !s.billingLinesAvailable() {
		return gen.PostProjectsByIdBillingLines409ApplicationProblemPlusJSONResponse(productsDisabled()), nil
	}

	parsed, fieldErrs, err := s.validateLine(ctx, body, project)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdBillingLines400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.ProjectsBillingLine
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertBillingLine(ctx, store.InsertBillingLineParams{
			ProjectID:       project.ID,
			Code:            parsed.Code,
			VariantID:       parsed.VariantID,
			PricingMode:     parsed.PricingMode,
			FixedAmount:     parsed.FixedAmount,
			DiscountPercent: parsed.DiscountPercent,
			Now:             now,
		})
		if err != nil {
			return err
		}
		return recordLineAdded(ctx, txq, now, project.ID, created, by)
	})
	if db.IsUniqueViolation(err, "ux_billing_lines_project_id_code") {
		return gen.PostProjectsByIdBillingLines400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("code", lineCodeTaken(parsed.Code)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: create billing line: %w", err)
	}

	resp, err := s.billingLineResponseFor(ctx, project, a, created)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdBillingLines201JSONResponse(resp), nil
}

// PutProjectsByIdBillingLinesByLineId Change a project's billing line
// (PUT /api/v1/projects/{id}/billing-lines/{lineId})
//
// One operation changes a line's fields and switches it on or off, because
// from the caller's side there is one intention — "this line is now X" — and
// `active` is just another field of it. The timeline still tells the two
// apart: a change that only toggled `active` writes the (de)activation entry
// and no line-changed, and a change that moved nothing writes nothing at all.
//
// The line is read under a lock and changed in the same transaction, and the
// entries are decided from the row that lock returned: two managers editing
// one line at once must not both be told they deactivated it, and a line
// renamed between a read and a write must not have the old code recorded
// against the new state. A body without `active` leaves it as it stands.
func (s *server) PutProjectsByIdBillingLinesByLineId(ctx context.Context, req gen.PutProjectsByIdBillingLinesByLineIdRequestObject) (gen.PutProjectsByIdBillingLinesByLineIdResponseObject, error) {
	body := gen.BillingLineRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsByIdBillingLinesByLineId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsByIdBillingLinesByLineId404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsByIdBillingLinesByLineId403JSONResponse(forbidden()), nil
	}
	if !s.billingLinesAvailable() {
		return gen.PutProjectsByIdBillingLinesByLineId409ApplicationProblemPlusJSONResponse(productsDisabled()), nil
	}

	parsed, fieldErrs, err := s.validateLine(ctx, body, project)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsByIdBillingLinesByLineId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var changed store.ProjectsBillingLine
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		before, err := txq.LockBillingLine(ctx, store.LockBillingLineParams{ID: req.LineId, ProjectID: project.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errLineNotFound
		}
		if err != nil {
			return fmt.Errorf("projects: lock billing line: %w", err)
		}
		changed, err = txq.UpdateBillingLine(ctx, store.UpdateBillingLineParams{
			ID:              req.LineId,
			ProjectID:       project.ID,
			Code:            parsed.Code,
			VariantID:       parsed.VariantID,
			PricingMode:     parsed.PricingMode,
			FixedAmount:     parsed.FixedAmount,
			DiscountPercent: parsed.DiscountPercent,
			Active:          parsed.Active,
			Now:             now,
		})
		if err != nil {
			return err
		}
		d, err := diffLines(before, changed)
		if err != nil {
			return err
		}
		if d.empty() {
			return nil
		}
		return recordLineUpdated(ctx, txq, now, d, project.ID, changed.Code, by)
	})
	switch {
	case errors.Is(err, errLineNotFound):
		return gen.PutProjectsByIdBillingLinesByLineId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_billing_lines_project_id_code"):
		return gen.PutProjectsByIdBillingLinesByLineId400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("code", lineCodeTaken(parsed.Code)))), nil
	case err != nil:
		return nil, fmt.Errorf("projects: change billing line: %w", err)
	}

	resp, err := s.billingLineResponseFor(ctx, project, a, changed)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsByIdBillingLinesByLineId200JSONResponse(resp), nil
}
