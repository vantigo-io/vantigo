package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is a project's work types (work types design D1): a rule defined
// once per project — "Overtid 50 %", "Helg" — that a time entry may pick, and
// that multiplies every rate the chain resolves for it, on every line. A type
// is a name and two percentages and that is all: Time multiplies, snapshots
// and freezes; nothing here multiplies anything.
//
// The three operations are ordered the billing lines' way — the project, the
// caller's access to it (404 before 403), then the body — and differ from
// them in one place on purpose: no project-row lock. The lock guards writes
// whose validity depends on the project's currency, fixed price or billing
// type (docs/projects.md's "Locking"), and a work type depends on none of
// them. Each write is one plain transaction; the name's uniqueness is the
// unique index's, whose 23505 is the 409.
//
// There is no DELETE. Entries that picked a type snapshot its name, but a
// type that was ever picked stays readable in the list, so it is deactivated
// through PUT `active: false` and reactivated the same way.

// errWorkTypeNotFound carries the change's 404 out of its transaction: whether
// the type is on this project is decided under the row lock.
var errWorkTypeNotFound = errors.New("projects: the project has no such work type")

// maxWorkTypeName is the name column's width.
const maxWorkTypeName = 100

// maxMultiplierPercent is D1's ceiling: ten times the rate. numeric(6,2)
// could hold more; the rule is the design's, not the column's.
const maxMultiplierPercent = 1000.0

// parsedWorkType is a body that passed D1's rules, in the shape the queries
// take.
type parsedWorkType struct {
	Name                  string
	BillMultiplierPercent pgtype.Numeric
	CostMultiplierPercent pgtype.Numeric
}

// validateWorkType runs every rule over a body and answers the parsed type and
// the field errors (nil when there are none). Every rule runs regardless of
// the others, so one round trip reports every problem. The error is an
// infrastructure one only: a multiplier that passed the rules and still cannot
// be stored.
func validateWorkType(body gen.WorkTypeRequest) (parsedWorkType, map[string][]string, error) {
	var errs map[string][]string
	name := strings.TrimSpace(body.Name)
	switch n := utf8.RuneCountInString(name); {
	case name == "":
		errs = withFieldError(errs, "name", "A work type name cannot be empty")
	case n > maxWorkTypeName:
		errs = withFieldError(errs, "name", fmt.Sprintf(
			"A work type name cannot be longer than %d characters, the given value was %d characters", maxWorkTypeName, n))
	}
	if msg := validateMultiplier("The bill multiplier", body.BillMultiplierPercent); msg != "" {
		errs = withFieldError(errs, "billMultiplierPercent", msg)
	}
	if msg := validateMultiplier("The cost multiplier", body.CostMultiplierPercent); msg != "" {
		errs = withFieldError(errs, "costMultiplierPercent", msg)
	}
	if len(errs) > 0 {
		return parsedWorkType{}, errs, nil
	}
	bill, err := numericFromFloat(body.BillMultiplierPercent)
	if err != nil {
		return parsedWorkType{}, nil, err
	}
	cost, err := numericFromFloat(body.CostMultiplierPercent)
	if err != nil {
		return parsedWorkType{}, nil, err
	}
	return parsedWorkType{Name: name, BillMultiplierPercent: bill, CostMultiplierPercent: cost}, nil, nil
}

// validateMultiplier is D1's percentage rule: more than nothing, at most
// maxMultiplierPercent, and no more precision than the numeric(6,2) column
// keeps — a third decimal would be silently rounded away, and a rate
// multiplied by something the manager did not type is worse than a refusal
// (the reasoning validatePositiveAmount gives for amounts). An absent field
// decodes as 0 and is refused here as "must be greater than zero".
func validateMultiplier(label string, v float64) string {
	switch {
	case v <= 0:
		return label + " must be greater than zero"
	case v > maxMultiplierPercent:
		return fmt.Sprintf("%s cannot be more than %g %%", label, maxMultiplierPercent)
	case decimalPlaces(v) > 2:
		return label + " cannot have more than two decimals"
	default:
		return ""
	}
}

// workTypeResponse renders one row. The percentages are NOT NULL columns, so
// floatFromNumeric's zero-for-NULL never fires; its error is an unreadable
// decimal, an infrastructure failure.
func workTypeResponse(row store.ProjectsWorkType) (gen.WorkTypeResponse, error) {
	bill, err := floatFromNumeric(row.BillMultiplierPercent)
	if err != nil {
		return gen.WorkTypeResponse{}, err
	}
	cost, err := floatFromNumeric(row.CostMultiplierPercent)
	if err != nil {
		return gen.WorkTypeResponse{}, err
	}
	return gen.WorkTypeResponse{
		Id:                    row.ID,
		ProjectId:             row.ProjectID,
		Name:                  row.Name,
		BillMultiplierPercent: bill,
		CostMultiplierPercent: cost,
		Active:                row.Active,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}, nil
}

// GetProjectsByIdWorkTypes List a project's work types
// (GET /api/v1/projects/{id}/work-types)
//
// Anyone who sees the project sees its types and their multipliers: a
// percentage is a rule, not an amount (D1), the reading that keeps budgetHours
// visible while budgetAmount is shaped away. A member logging overtime is
// entitled to know it bills at 150 %.
func (s *server) GetProjectsByIdWorkTypes(ctx context.Context, req gen.GetProjectsByIdWorkTypesRequestObject) (gen.GetProjectsByIdWorkTypesResponseObject, error) {
	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdWorkTypes404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdWorkTypes404Response{}, nil
	}

	rows, err := q.ListWorkTypes(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list work types: %w", err)
	}
	out := make([]gen.WorkTypeResponse, 0, len(rows))
	for _, row := range rows {
		resp, err := workTypeResponse(row)
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return gen.GetProjectsByIdWorkTypes200JSONResponse(out), nil
}

// PostProjectsByIdWorkTypes Add a work type to a project
// (POST /api/v1/projects/{id}/work-types)
//
// Manager only. A new type is active whatever the body says: `active` is a
// PUT field. Two managers adding the same name both reach the insert; one
// wins and the other's 23505 becomes the 409.
func (s *server) PostProjectsByIdWorkTypes(ctx context.Context, req gen.PostProjectsByIdWorkTypesRequestObject) (gen.PostProjectsByIdWorkTypesResponseObject, error) {
	body := gen.WorkTypeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostProjectsByIdWorkTypes404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PostProjectsByIdWorkTypes404Response{}, nil
	}
	if !a.CanManage {
		return gen.PostProjectsByIdWorkTypes403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := validateWorkType(body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PostProjectsByIdWorkTypes400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	// The caller is named before the transaction opens: the user directory is
	// another module, and nothing inside a transaction asks one.
	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.ProjectsWorkType
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		created, err = txq.InsertWorkType(ctx, store.InsertWorkTypeParams{
			ProjectID:             project.ID,
			Name:                  parsed.Name,
			BillMultiplierPercent: parsed.BillMultiplierPercent,
			CostMultiplierPercent: parsed.CostMultiplierPercent,
			Now:                   now,
		})
		if err != nil {
			return err
		}
		return recordWorkTypeAdded(ctx, txq, now, project.ID, created, by)
	})
	switch {
	case db.IsUniqueViolation(err, "ux_work_types_project_id_name"):
		return gen.PostProjectsByIdWorkTypes409ApplicationProblemPlusJSONResponse(workTypeExists(parsed.Name)), nil
	case err != nil:
		return nil, fmt.Errorf("projects: create work type: %w", err)
	}

	resp, err := workTypeResponse(created)
	if err != nil {
		return nil, err
	}
	return gen.PostProjectsByIdWorkTypes201JSONResponse(resp), nil
}

// PutProjectsByIdWorkTypesByWorkTypeId Change a project's work type
// (PUT /api/v1/projects/{id}/work-types/{workTypeId})
//
// Manager only; one intention, "this type is now X", `active` included. The
// type is read under its own row lock and changed in the same transaction, so
// the timeline names the fields that moved against the row nobody else could
// move meanwhile. Changing a multiplier moves no entry already submitted:
// Time froze the percentages with the rates (work types design D3).
func (s *server) PutProjectsByIdWorkTypesByWorkTypeId(ctx context.Context, req gen.PutProjectsByIdWorkTypesByWorkTypeIdRequestObject) (gen.PutProjectsByIdWorkTypesByWorkTypeIdResponseObject, error) {
	body := gen.WorkTypeRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	project, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, project.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId403JSONResponse(forbidden()), nil
	}

	parsed, fieldErrs, err := validateWorkType(body)
	if err != nil {
		return nil, err
	}
	if len(fieldErrs) > 0 {
		return gen.PutProjectsByIdWorkTypesByWorkTypeId400ApplicationProblemPlusJSONResponse(invalidProject(fieldErrs)), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var changed store.ProjectsWorkType
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		before, err := txq.LockWorkType(ctx, store.LockWorkTypeParams{ID: req.WorkTypeId, ProjectID: project.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errWorkTypeNotFound
		}
		if err != nil {
			return fmt.Errorf("projects: lock work type: %w", err)
		}
		changed, err = txq.UpdateWorkType(ctx, store.UpdateWorkTypeParams{
			ID:                    req.WorkTypeId,
			ProjectID:             project.ID,
			Name:                  parsed.Name,
			BillMultiplierPercent: parsed.BillMultiplierPercent,
			CostMultiplierPercent: parsed.CostMultiplierPercent,
			Active:                body.Active,
			Now:                   now,
		})
		if err != nil {
			return err
		}
		fields, err := diffWorkTypes(before, changed)
		if err != nil {
			return err
		}
		if len(fields) == 0 {
			return nil
		}
		return recordWorkTypeUpdated(ctx, txq, now, project.ID, changed, fields, by)
	})
	switch {
	case errors.Is(err, errWorkTypeNotFound):
		return gen.PutProjectsByIdWorkTypesByWorkTypeId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_work_types_project_id_name"):
		return gen.PutProjectsByIdWorkTypesByWorkTypeId409ApplicationProblemPlusJSONResponse(workTypeExists(parsed.Name)), nil
	case err != nil:
		return nil, fmt.Errorf("projects: change work type: %w", err)
	}

	resp, err := workTypeResponse(changed)
	if err != nil {
		return nil, err
	}
	return gen.PutProjectsByIdWorkTypesByWorkTypeId200JSONResponse(resp), nil
}
