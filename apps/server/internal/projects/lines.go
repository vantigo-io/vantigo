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

// errLineNotFound and errVariantNotFound carry the change's two refusals out
// of its transaction. Both are decided under the row lock — whether the line
// exists at all, and whether this request is moving it to another variant —
// and a refusal has to roll the transaction back rather than return a
// response from inside it.
var (
	errLineNotFound    = errors.New("projects: the project has no such billing line")
	errVariantNotFound = errors.New("projects: the line's new variant does not exist")
)

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

	parsed, fieldErrs, err := validateLine(body, project)
	if err != nil {
		return nil, err
	}
	// D9: a line is pinned to a variant, so a variant nobody has is a body
	// this module cannot store rather than a line with a dangling reference.
	// A create always asks, because there is no line yet whose variant this
	// one could be inheriting — the question the change has to lock for.
	exists, err := s.variantExists(ctx, body.VariantId)
	if err != nil {
		return nil, err
	}
	if !exists {
		fieldErrs = withFieldError(fieldErrs, "variantId", variantNotFound(body.VariantId))
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

		// The project row, locked FOR UPDATE, is this transaction's first
		// statement — the same lock and the same query PutProjectsById
		// takes before deciding to clear or swap the currency (design
		// §3.3), so the two writers serialise on this row instead of racing
		// past each other: a 'fixed' amount and a budget amount are both
		// denominated in the project's currency, exactly what that guard
		// protects. Re-running validateLine against the row this
		// transaction now holds — rather than trusting the pool read from
		// above the guards ran against — is what actually decides the
		// currency-dependent rules under the lock; everything else about
		// the body already passed and cannot have changed.
		locked, err := txq.LockProject(ctx, project.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errProjectVanished
		}
		if err != nil {
			return fmt.Errorf("projects: lock project: %w", err)
		}
		if _, lockedErrs, err := validateLine(body, locked); err != nil {
			return err
		} else if len(lockedErrs) > 0 {
			return fieldRefusal{errs: lockedErrs}
		}

		created, err = txq.InsertBillingLine(ctx, store.InsertBillingLineParams{
			ProjectID:       project.ID,
			Code:            parsed.Code,
			VariantID:       parsed.VariantID,
			PricingMode:     parsed.PricingMode,
			FixedAmount:     parsed.FixedAmount,
			DiscountPercent: parsed.DiscountPercent,
			BudgetHours:     parsed.BudgetHours,
			BudgetAmount:    parsed.BudgetAmount,
			Now:             now,
		})
		if err != nil {
			return err
		}
		return recordLineAdded(ctx, txq, now, project.ID, created, by)
	})
	var refusal fieldRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PostProjectsByIdBillingLines400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.Is(err, errProjectVanished):
		return gen.PostProjectsByIdBillingLines404Response{}, nil
	case db.IsUniqueViolation(err, "ux_billing_lines_project_id_code"):
		return gen.PostProjectsByIdBillingLines400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("code", lineCodeTaken(parsed.Code)))), nil
	case err != nil:
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

	parsed, fieldErrs, err := validateLine(body, project)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsByIdBillingLinesByLineId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	// D9's variant rule is asked here, unconditionally, before any lock is
	// taken — never from inside the transaction below. The catalog is an
	// in-process call into another module (contracts.ProductCatalog); a
	// transaction that holds LockProject (and, below, LockBillingLine) must
	// never wait on it, or a slow or blocked catalog call would stall every
	// other writer of this project, not just this line. Whether the answer
	// is even relevant is a separate question — the request may not be
	// moving the line to a different variant at all, and keeping the
	// variant a line is already pinned to is always allowed regardless of
	// what the catalog says today — and that question can only be decided
	// from the row LockBillingLine returns, so it is decided there, not
	// here; this is only the read, done early enough not to matter to the
	// lock's holder.
	requestedVariantExists, err := s.variantExists(ctx, parsed.VariantID)
	if err != nil {
		return nil, err
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var changed store.ProjectsBillingLine
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)

		// The project's own lock comes before the line's (design §3.3): a
		// fixed lock ordering across every writer that can take both is
		// what keeps two guarded writers from deadlocking against each
		// other rather than simply queuing. Re-running validateLine against
		// the row this transaction now holds is what decides the
		// currency-dependent rules under that lock, the same way the
		// create does (PostProjectsByIdBillingLines) — everything else
		// about the body already passed and cannot have changed.
		locked, err := txq.LockProject(ctx, project.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errProjectVanished
		}
		if err != nil {
			return fmt.Errorf("projects: lock project: %w", err)
		}
		if _, lockedErrs, err := validateLine(body, locked); err != nil {
			return err
		} else if len(lockedErrs) > 0 {
			return fieldRefusal{errs: lockedErrs}
		}

		before, err := txq.LockBillingLine(ctx, store.LockBillingLineParams{ID: req.LineId, ProjectID: project.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errLineNotFound
		}
		if err != nil {
			return fmt.Errorf("projects: lock billing line: %w", err)
		}
		// D9's variant rule. Whether it applies at all — whether this
		// request is actually moving the line to a different variant — is
		// decided from the row this transaction holds rather than from a
		// read taken before it: keeping the variant a line is already
		// pinned to is always allowed (products may have dropped it since,
		// and a line whose product is gone must stay editable, not least to
		// be deactivated), and only the locked row can say whether that is
		// what this request does. The catalog was already asked, above,
		// before this transaction opened; requestedVariantExists is simply
		// set aside, never re-asked, when the variant turns out unchanged.
		if before.VariantID != parsed.VariantID && !requestedVariantExists {
			return errVariantNotFound
		}
		changed, err = txq.UpdateBillingLine(ctx, store.UpdateBillingLineParams{
			ID:              req.LineId,
			ProjectID:       project.ID,
			Code:            parsed.Code,
			VariantID:       parsed.VariantID,
			PricingMode:     parsed.PricingMode,
			FixedAmount:     parsed.FixedAmount,
			DiscountPercent: parsed.DiscountPercent,
			BudgetHours:     parsed.BudgetHours,
			BudgetAmount:    parsed.BudgetAmount,
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
	var refusal fieldRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PutProjectsByIdBillingLinesByLineId400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.Is(err, errProjectVanished):
		return gen.PutProjectsByIdBillingLinesByLineId404Response{}, nil
	case errors.Is(err, errLineNotFound):
		return gen.PutProjectsByIdBillingLinesByLineId404Response{}, nil
	case errors.Is(err, errVariantNotFound):
		return gen.PutProjectsByIdBillingLinesByLineId400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("variantId", variantNotFound(parsed.VariantID)))), nil
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
