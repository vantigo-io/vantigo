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

	// D13's other half, which only an update can break: a 'fixed' billing
	// line is an amount denominated in the project's currency, so the
	// currency cannot be cleared while one exists. It is checked here rather
	// than in validateProject because a project being *created* has no lines
	// yet, and it is checked whatever Deps.Products holds: lines stored
	// before products was switched off are still stored, and their amounts
	// are still in this currency (D10).
	//
	// Only a request that actually clears a currency the project has pays for
	// the query: a project that never had one cannot have a 'fixed' line to
	// protect, since such a line could not have been created without it.
	if before.Currency != nil && parsed.Currency == nil {
		fixedLines, err := q.CountFixedBillingLines(ctx, before.ID)
		if err != nil {
			return nil, fmt.Errorf("projects: count fixed billing lines: %w", err)
		}
		if fixedLines > 0 {
			return gen.PutProjectsById400ApplicationProblemPlusJSONResponse(
				invalidProject(fieldError("currency", currencyLockedByFixedLine()))), nil
		}
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var after store.ProjectsProject
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
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
			Now:              now,
		})
		if err != nil {
			return err
		}
		diff, err := diffProjects(before, after)
		if err != nil {
			return err
		}
		if diff.empty() {
			return nil
		}
		return recordProjectUpdated(ctx, txq, now, diff, after.ID, by)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The revision the project actually carries is read again rather
		// than taken from `before`: the row that beat this one to the write
		// committed after `before` was loaded, so `before`'s revision would
		// report the number the caller already sent.
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
