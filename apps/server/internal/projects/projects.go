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

// This file is the Projects area's CRUD. Create and get live here; the list,
// the update and the status change join them as they are built.

// counterProjectCode is the counter the code suggestion counts from
// (§4.2). It advances by one on every create, whatever code that create
// actually used, so two projects made a minute apart never suggest the same
// number.
const counterProjectCode = "project_code"

// fieldRefusal is a validation decision made inside a transaction, carried
// out of it as a 400's full field-error map (invalidProject's own shape),
// because a response cannot be built from inside a transaction that might
// still be rolled back by something else. Design §3.3's two project guards
// use it (always one field), and a billing line's re-validation under the
// project's lock uses it too (lines.go — potentially more than one field, if
// the currency clearing under the lock breaks more than one rule of the
// body's at once).
type fieldRefusal struct {
	errs map[string][]string
}

func (e fieldRefusal) Error() string { return "projects: refused a write decided under a lock" }

// singleFieldRefusal is fieldRefusal for a guard that only ever blames one
// field, which is both of design §3.3's guards.
func singleFieldRefusal(field, message string) fieldRefusal {
	return fieldRefusal{errs: fieldError(field, message)}
}

// revisionRefusal carries the update's stale-revision decision out of the
// transaction, once it is decided against the project row the transaction
// holds locked (LockProject) rather than against a second read taken after
// the transaction has already rolled back. current is the revision the
// project actually carries, for the same 409 revisionConflict already
// builds from it.
type revisionRefusal struct {
	current int32
}

func (e revisionRefusal) Error() string { return "projects: refused a stale revision under a lock" }

// errProjectVanished is what LockProject finding no row means. Nothing in
// this module ever deletes a project (there is no such operation), so this
// is unreached today; it exists because a writer that locks a project deep
// inside its own transaction (a billing line's create or change, lines.go)
// must still answer exactly the 404 it would have answered had the project
// never been there at all, if that ever changes.
var errProjectVanished = errors.New("projects: the project vanished under its own lock")

// currencyLocked is D13's guard (design §3.3): whether the project currently
// carries an amount denominated in its currency that a currency change would
// silently reprice. It runs inside the update's own transaction, against the
// rows as they stand at that instant, under the same lock (LockProject) the
// currency and billing-type checks below run under.
func (s *server) currencyLocked(ctx context.Context, txq *store.Queries, projectID int32) (bool, error) {
	fixedLines, err := txq.CountFixedBillingLines(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("projects: count fixed billing lines: %w", err)
	}
	if fixedLines > 0 {
		return true, nil
	}
	budgetedLines, err := txq.CountBudgetedBillingLines(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("projects: count budgeted billing lines: %w", err)
	}
	if budgetedLines > 0 {
		return true, nil
	}
	milestones, err := txq.CountNonCancelledMilestones(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("projects: count milestones: %w", err)
	}
	return milestones > 0, nil
}

// PostProjects Create a project
// (POST /api/v1/projects)
//
// The creator becomes the project's manager (D6), which is what makes the
// response's capabilities both true and its financials present: whoever
// creates a project may always see its money. Insert, role, counter and
// timeline entry are one transaction, so a project never exists without its
// manager or its first timeline entry.
//
// The code's uniqueness is enforced by ux_projects_code, not by a read-then-
// write check: two creates racing for one code both reach the insert, one
// wins and the other's 23505 becomes the ordinary `code` field error. A
// pre-check would answer the same thing most of the time and lose the race
// the rest of it.
func (s *server) PostProjects(ctx context.Context, req gen.PostProjectsRequestObject) (gen.PostProjectsResponseObject, error) {
	body := gen.ProjectCreateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	parsed, fieldErrs, err := s.validateProject(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjects400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.ProjectsProject
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertProject(ctx, store.InsertProjectParams{
			Code:             parsed.Code,
			Name:             parsed.Name,
			Description:      parsed.Description,
			CustomerID:       parsed.CustomerID,
			StartDate:        parsed.StartDate,
			EndDate:          parsed.EndDate,
			BillingType:      parsed.BillingType,
			Currency:         parsed.Currency,
			FixedPriceAmount: parsed.FixedPriceAmount,
			BudgetHours:      parsed.BudgetHours,
			BudgetAmount:     parsed.BudgetAmount,
			DefaultBillRate:  parsed.DefaultBillRate,
			CreatedByUserID:  by.UserID,
			Now:              now,
		})
		if err != nil {
			return err
		}
		if err := txq.InsertProjectRole(ctx, store.InsertProjectRoleParams{
			ProjectID: created.ID, UserID: by.UserID, Role: roleManager, Now: now,
		}); err != nil {
			return err
		}
		if _, err := txq.NextCounterValue(ctx, counterProjectCode); err != nil {
			return err
		}
		return recordProjectCreated(ctx, txq, now, created.ID, created.Code, created.Name, by)
	})
	if db.IsUniqueViolation(err, "ux_projects_code") {
		return gen.PostProjects400ApplicationProblemPlusJSONResponse(invalidProject(fieldError("code", codeTaken(parsed.Code)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: create project: %w", err)
	}

	a := access{}.widenBy(roleManager)
	managers := []gen.ProjectPersonSummary{{UserId: by.UserID, DisplayName: by.Display}}
	resp, err := projectResponse(created, a, parsed.CustomerName, managers, s.billingLinesAvailable())
	if err != nil {
		return nil, err
	}
	return gen.PostProjects201JSONResponse(resp), nil
}

// GetProjectsById Get a project by id
// (GET /api/v1/projects/{id})
//
// An outsider gets the same bare 404 an unknown id gets (D7), which is why
// the row is loaded before the caller's access to it is resolved and then
// discarded: the two answers have to be indistinguishable, and a handler
// that checked access first would have to invent a project to be consistent
// about.
func (s *server) GetProjectsById(ctx context.Context, req gen.GetProjectsByIdRequestObject) (gen.GetProjectsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}

	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsById404Response{}, nil
	}

	resp, err := s.projectResponseFor(ctx, q, row, a)
	if err != nil {
		return nil, err
	}
	return gen.GetProjectsById200JSONResponse(resp), nil
}

// PutProjectsById Update a project
// (PUT /api/v1/projects/{id})
//
// An update carries every field of the project as it should stand
// afterwards, plus the revision the caller read it at. The order is the one
// D7 forces: the row first, then the caller's access to it (404 before 403
// before any field error), so a stranger never learns a project exists by
// the shape of the refusal they get.
//
// The revision is enforced by the UPDATE's own WHERE clause, not by
// comparing the loaded row: between the read and the write another request
// can commit, and only the database can decide that race. Zero rows updated
// is that loss, and answers 409.
func (s *server) PutProjectsById(ctx context.Context, req gen.PutProjectsByIdRequestObject) (gen.PutProjectsByIdResponseObject, error) {
	body := gen.ProjectUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	before, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}

	a, err := s.authorize(ctx, q, before.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsById404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsById403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := s.validateProject(ctx, projectFromUpdate(body))
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsById400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var after store.ProjectsProject
	// The project row, locked FOR NO KEY UPDATE, is this transaction's first
	// statement (design §3.3): design §3.3's two guards below, and the
	// revision check that follows, all have to be decided against the row this
	// transaction now holds rather than `before` — read before the transaction
	// opened — because a billing line's own create or change (lines.go) takes
	// the identical lock before writing a 'fixed' amount or a budget amount,
	// and only one lock, taken first by whichever request gets there first,
	// actually serialises the two against each other. `before` still answers
	// the questions that do not depend on freshness (access, §4.1's own field
	// rules).
	err = s.withProjectLock(ctx, before.ID, func(ctx context.Context, txq *store.Queries, locked store.ProjectsProject) error {
		// The revision the caller read is checked against the locked row
		// rather than left to UpdateProject's own WHERE clause: this
		// transaction holds the only lock that could let it move, so the
		// current revision named in a 409 can be read here, under that
		// lock, instead of via a second, unlocked read after this
		// transaction has already rolled back.
		if locked.Revision != body.Revision {
			return revisionRefusal{current: locked.Revision}
		}

		// D13's guard: a 'fixed' billing line, a line's budget amount and a
		// non-cancelled milestone are all amounts denominated in the
		// project's currency, so that currency can be neither cleared nor
		// swapped for another one while any of them exists — swapping it
		// would silently reprice them. Only a request that actually moves a
		// currency the project has pays for the reads: a project that never
		// had one cannot have any of the three to protect, since none of them
		// could have been created without it.
		if locked.Currency != nil && !equalStringPtr(locked.Currency, parsed.Currency) {
			amountsLocked, err := s.currencyLocked(ctx, txq, locked.ID)
			if err != nil {
				return err
			}
			if amountsLocked {
				return singleFieldRefusal("currency", currencyLockedByAmounts())
			}
		}
		// The fixed-price guard: a percent milestone that is still open
		// resolves its amount from the project's fixed price, so removing
		// that price — clearing it, or moving the project off fixed-price
		// billing — is refused while any exist.
		if field, trigger := fixedPriceGuardField(locked, parsed); trigger {
			names, err := txq.ListOpenPercentMilestoneNames(ctx, locked.ID)
			if err != nil {
				return fmt.Errorf("projects: list open percent milestones: %w", err)
			}
			if len(names) > 0 {
				return singleFieldRefusal(field, fixedPriceLockedByMilestones(names))
			}
		}

		var err error
		after, err = txq.UpdateProject(ctx, store.UpdateProjectParams{
			ID:               req.Id,
			Revision:         body.Revision,
			Code:             parsed.Code,
			Name:             parsed.Name,
			Description:      parsed.Description,
			CustomerID:       parsed.CustomerID,
			StartDate:        parsed.StartDate,
			EndDate:          parsed.EndDate,
			BillingType:      parsed.BillingType,
			Currency:         parsed.Currency,
			FixedPriceAmount: parsed.FixedPriceAmount,
			BudgetHours:      parsed.BudgetHours,
			BudgetAmount:     parsed.BudgetAmount,
			DefaultBillRate:  parsed.DefaultBillRate,
			Now:              now,
		})
		if err != nil {
			return err
		}
		diff, err := diffProjects(locked, after)
		if err != nil {
			return err
		}
		if diff.empty() {
			return nil
		}
		return recordProjectUpdated(ctx, txq, now, diff, after.ID, by)
	})
	var refusal fieldRefusal
	var revConflict revisionRefusal
	switch {
	case errors.As(err, &refusal):
		return gen.PutProjectsById400ApplicationProblemPlusJSONResponse(invalidProject(refusal.errs)), nil
	case errors.As(err, &revConflict):
		return gen.PutProjectsById409ApplicationProblemPlusJSONResponse(revisionConflict(revConflict.current, body.Revision)), nil
	case errors.Is(err, errProjectVanished):
		return gen.PutProjectsById404Response{}, nil
	case errors.Is(err, pgx.ErrNoRows):
		// Not reachable through this handler's own logic any more — the
		// revision check above already decides that under the same lock
		// UpdateProject's WHERE clause re-checks — but kept as the same
		// fallback the WHERE clause has always been, in case anything ever
		// calls UpdateProject without going through that check first.
		current, err := q.GetProject(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("projects: re-read the project after a revision conflict: %w", err)
		}
		return gen.PutProjectsById409ApplicationProblemPlusJSONResponse(revisionConflict(current.Revision, body.Revision)), nil
	case db.IsUniqueViolation(err, "ux_projects_code"):
		return gen.PutProjectsById400ApplicationProblemPlusJSONResponse(invalidProject(fieldError("code", codeTaken(parsed.Code)))), nil
	case err != nil:
		return nil, fmt.Errorf("projects: update project: %w", err)
	}

	resp, err := s.projectResponseFor(ctx, q, after, a)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsById200JSONResponse(resp), nil
}
