package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person's roles for the company (design S6): verneombud,
// AMU member, HMS responsible, each with the dates the person was elected
// for. They are the work group — everyone who sees the person reads them —
// and HR replaces them whole, each change one roles_updated event.

var roleKinds = []string{"verneombud", "amu_member", "hms_responsible"}

// parseRoles judges a replace, each field named by the role's place in the
// list (roles[1].toDate). A period may be open at either end; one that ends
// before it starts is refused.
func parseRoles(body gen.StaffPersonRolesRequest) ([]gen.StaffPersonRole, fieldErrors) {
	var f fieldErrors
	out := make([]gen.StaffPersonRole, 0, len(body.Roles))
	for i, r := range body.Roles {
		field := func(name string) string { return fmt.Sprintf("roles[%d].%s", i, name) }
		role, msg := oneOf("A role", string(r.Role), roleKinds)
		f.add(field("role"), msg)
		parsed := gen.StaffPersonRole{FromDate: civilDate(r.FromDate), ToDate: civilDate(r.ToDate)}
		if role != nil {
			parsed.Role = gen.StaffPersonRoleKind(*role)
		}
		if parsed.FromDate != nil && parsed.ToDate != nil && parsed.ToDate.Before(parsed.FromDate.Time) {
			f.add(field("toDate"), "A role cannot end before it starts")
		}
		out = append(out, parsed)
	}
	return out, f
}

// civilDate is a request's date as the calendar day it names, at midnight
// UTC — the shape a date parameter takes to SQL.
func civilDate(d *openapi_types.Date) *openapi_types.Date {
	if d == nil {
		return nil
	}
	return &openapi_types.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)}
}

// pgDate is a contract date as a nullable date column.
func pgDate(d *openapi_types.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.Time, Valid: true}
}

func personRoleResponse(row store.ListPersonRolesRow) gen.StaffPersonRole {
	return gen.StaffPersonRole{
		Role: gen.StaffPersonRoleKind(row.Role), FromDate: dateOf(row.FromDate), ToDate: dateOf(row.ToDate),
	}
}

// GetStaffPeopleByIdRoles List a person's roles
// (GET /api/v1/staff/people/{id}/roles)
func (s *server) GetStaffPeopleByIdRoles(ctx context.Context, req gen.GetStaffPeopleByIdRolesRequestObject) (gen.GetStaffPeopleByIdRolesResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var rows []store.ListPersonRolesRow
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		manager, err := managesPerson(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !caller.audienceOn(req.Id, manager).show("roles") {
			return notFound()
		}
		if rows, err = q.ListPersonRoles(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's roles: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdRoles404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdRoles200JSONResponse(mapRows(rows, personRoleResponse)), nil
}

// PutStaffPeopleByIdRoles Replace a person's roles
// (PUT /api/v1/staff/people/{id}/roles)
func (s *server) PutStaffPeopleByIdRoles(ctx context.Context, req gen.PutStaffPeopleByIdRolesRequestObject) (gen.PutStaffPeopleByIdRolesResponseObject, error) {
	var body gen.StaffPersonRolesRequest
	if req.Body != nil {
		body = *req.Body
	}
	roles, f := parseRoles(body)
	if f.failed() {
		return gen.PutStaffPeopleByIdRoles400ApplicationProblemPlusJSONResponse(invalid(invalidRolesTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var rows []store.ListPersonRolesRow
	err = s.withPersonLock(ctx, req.Id, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, err := txq.ListPersonRoles(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's roles: %w", req.Id, err)
		}
		if err := txq.DeletePersonRoles(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: clear person %d's roles: %w", req.Id, err)
		}
		for _, r := range roles {
			if err := txq.InsertPersonRole(ctx, store.InsertPersonRoleParams{
				PersonID: req.Id, Role: string(r.Role), FromDate: pgDate(r.FromDate), ToDate: pgDate(r.ToDate), Now: s.deps.Clock(),
			}); err != nil {
				return fmt.Errorf("staff: add person %d's role: %w", req.Id, err)
			}
		}
		if rows, err = txq.ListPersonRoles(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's roles: %w", req.Id, err)
		}
		c, differ := listChange(mapRows(before, personRoleResponse), mapRows(rows, personRoleResponse))
		if !differ {
			return nil
		}
		return s.recordChanges(ctx, txq, req.Id, eventRolesUpdated, who, changes{"roles": c})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdRoles404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: replace person %d's roles: %w", req.Id, err)
	}
	return gen.PutStaffPeopleByIdRoles200JSONResponse(mapRows(rows, personRoleResponse)), nil
}
