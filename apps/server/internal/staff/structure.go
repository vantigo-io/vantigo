package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the company's structure (design S6, §3.4, §6): legal entities,
// business units, departments and locations, each listed, added, replaced and
// archived. A row is never deleted — an employment or a terms row may name it
// — so one no longer in use is archived: it stays on every row that names it
// and leaves the pickers. Every list answers archived rows too, so a stored
// reference can still be named.
//
// Exactly one legal entity and exactly one business unit is the default (plan
// reading 12): the first created becomes it, a write with isDefault true moves
// it, and the default is never archived nor un-flagged in place. Each table's
// default is serialised by an advisory lock of its own (locks.go), and the
// partial unique index is the backstop. A department's parent chain never
// cycles; every write of a department takes the manager-graph lock, since its
// manager and its parent are edges of the graph delivery A1's Task 4 judges
// manager cycles over.

// The unique indexes a structure write can trip, each answered as its field.
const (
	legalEntityOrgIndex  = "ux_legal_entities_org_number"
	businessUnitOrgIndex = "ux_business_units_org_number"
	departmentCodeIndex  = "ux_departments_code_upper"
)

// refusal is a structure write's answer that is not a success, returned from
// inside its transaction so everything the write did rolls back, and turned
// into the operation's own typed response by the handler.
type refusal struct {
	status  int
	problem gen.StaffProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("staff: refused with %d", r.status) }

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// refuseConflict is a 409 carrying p.
func refuseConflict(p gen.StaffProblem) error {
	return &refusal{status: http.StatusConflict, problem: p}
}

// refuseInvalid is a 400 carrying the field errors under title.
func refuseInvalid(title string, f fieldErrors) error {
	return &refusal{status: http.StatusBadRequest, problem: invalid(title, f)}
}

// asRefusal reports whether err is a refusal, and which.
func asRefusal(err error) (*refusal, bool) {
	var r *refusal
	ok := errors.As(err, &r)
	return r, ok
}

// checkRevision is the If-Match rule under the row's lock: the revision the
// caller read must be the row's.
func checkRevision(what string, current, supplied int32) error {
	if current != supplied {
		return refuseConflict(revisionConflict(what, current, supplied))
	}
	return nil
}

// departmentCycle reports whether making parentID the parent of department id
// would close a cycle: parentID is id itself, or id is among parentID's
// ancestors — that is, parentID sits below id. parents is every department's
// parent. The walk carries a visited set, so a graph that already holds a
// cycle (written past this module) answers rather than looping. id 0 is a
// department not yet created, which nothing can sit below.
func departmentCycle(parents map[int64]*int64, id, parentID int64) bool {
	visited := map[int64]bool{}
	for cur := parentID; ; {
		if cur == id {
			return true
		}
		if visited[cur] {
			return false
		}
		visited[cur] = true
		next, ok := parents[cur]
		if !ok || next == nil {
			return false
		}
		cur = *next
	}
}

// ---- Legal entities ---------------------------------------------------------

// parsedLegalEntity is one validated legal entity body.
type parsedLegalEntity struct {
	Name      string
	OrgNumber *string
}

func parseLegalEntity(body gen.StaffLegalEntityRequest) (parsedLegalEntity, fieldErrors) {
	var f fieldErrors
	var out parsedLegalEntity
	var msg string
	out.Name, msg = requiredText("A name", body.Name, structureNameMaxLength)
	f.add("name", msg)
	out.OrgNumber, msg = parseOrgNumber(body.OrgNumber)
	f.addCoded("orgNumber", codeOrgNumberInvalid, msg)
	return out, f
}

// orgNumberTaken is the field error a number another row of the table holds
// answers.
func orgNumberTaken(title string) error {
	return refuseInvalid(title, fieldErrors{errs: fieldError("orgNumber", "Another row already has this organisation number")})
}

// GetStaffLegalEntities List legal entities
// (GET /api/v1/staff/legal-entities)
func (s *server) GetStaffLegalEntities(ctx context.Context, _ gen.GetStaffLegalEntitiesRequestObject) (gen.GetStaffLegalEntitiesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListLegalEntities(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the legal entities: %w", err)
	}
	return gen.GetStaffLegalEntities200JSONResponse(mapRows(rows, legalEntityResponse)), nil
}

// PostStaffLegalEntities Add a legal entity
// (POST /api/v1/staff/legal-entities)
//
// The first legal entity becomes the default; isDefault true makes the new one
// the default in its place.
func (s *server) PostStaffLegalEntities(ctx context.Context, req gen.PostStaffLegalEntitiesRequestObject) (gen.PostStaffLegalEntitiesResponseObject, error) {
	var body gen.StaffLegalEntityRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseLegalEntity(body)
	if f.failed() {
		return gen.PostStaffLegalEntities400ApplicationProblemPlusJSONResponse(invalid(invalidLegalEntityTitle, f)), nil
	}
	var created store.StaffLegalEntity
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, legalEntityDefaultLock); err != nil {
			return err
		}
		hasDefault, err := txq.HasDefaultLegalEntity(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the default legal entity: %w", err)
		}
		makeDefault := !hasDefault || (body.IsDefault != nil && *body.IsDefault)
		if makeDefault && hasDefault {
			if err := txq.ClearLegalEntityDefault(ctx, store.ClearLegalEntityDefaultParams{Keep: 0, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: move the default legal entity: %w", err)
			}
		}
		created, err = txq.InsertLegalEntity(ctx, store.InsertLegalEntityParams{
			Name: parsed.Name, OrgNumber: parsed.OrgNumber, IsDefault: makeDefault, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, legalEntityOrgIndex) {
			return orgNumberTaken(invalidLegalEntityTitle)
		}
		return err
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffLegalEntities400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a legal entity: %w", err)
	}
	return gen.PostStaffLegalEntities201JSONResponse(legalEntityResponse(created)), nil
}

// PutStaffLegalEntitiesById Change a legal entity
// (PUT /api/v1/staff/legal-entities/{id})
//
// A full replace of the name and the organisation number. isDefault true makes
// it the default — an archived row never is — and false on the default is
// refused, since there is always exactly one; left out, the flag stays.
func (s *server) PutStaffLegalEntitiesById(ctx context.Context, req gen.PutStaffLegalEntitiesByIdRequestObject) (gen.PutStaffLegalEntitiesByIdResponseObject, error) {
	var body gen.StaffLegalEntityRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffLegalEntitiesById400ApplicationProblemPlusJSONResponse(invalid(invalidLegalEntityTitle, badIfMatch())), nil
	}
	parsed, f := parseLegalEntity(body)
	if f.failed() {
		return gen.PutStaffLegalEntitiesById400ApplicationProblemPlusJSONResponse(invalid(invalidLegalEntityTitle, f)), nil
	}
	var saved store.StaffLegalEntity
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, legalEntityDefaultLock); err != nil {
			return err
		}
		row, err := txq.LockLegalEntity(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock legal entity %d: %w", req.Id, err)
		}
		if err := checkRevision("legal entity", row.Revision, revision); err != nil {
			return err
		}
		makeDefault := false
		if body.IsDefault != nil {
			switch {
			case *body.IsDefault && row.ArchivedAt != nil:
				return refuseConflict(structureArchived("An archived legal entity cannot be the default."))
			case *body.IsDefault && !row.IsDefault:
				if err := txq.ClearLegalEntityDefault(ctx, store.ClearLegalEntityDefaultParams{Keep: row.ID, Now: s.deps.Clock()}); err != nil {
					return fmt.Errorf("staff: move the default legal entity: %w", err)
				}
				makeDefault = true
			case !*body.IsDefault && row.IsDefault:
				return refuseConflict(structureDefault("legal entity"))
			}
		}
		saved, err = txq.UpdateLegalEntity(ctx, store.UpdateLegalEntityParams{
			ID: row.ID, Name: parsed.Name, OrgNumber: parsed.OrgNumber, MakeDefault: makeDefault, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, legalEntityOrgIndex) {
			return orgNumberTaken(invalidLegalEntityTitle)
		}
		return err
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffLegalEntitiesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffLegalEntitiesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffLegalEntitiesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change legal entity %d: %w", req.Id, err)
	}
	return gen.PutStaffLegalEntitiesById200JSONResponse(legalEntityResponse(saved)), nil
}

// PostStaffLegalEntitiesByIdArchive Archive a legal entity
// (POST /api/v1/staff/legal-entities/{id}/archive)
//
// The default is never archived (structure_default); archiving an archived
// row changes nothing.
func (s *server) PostStaffLegalEntitiesByIdArchive(ctx context.Context, req gen.PostStaffLegalEntitiesByIdArchiveRequestObject) (gen.PostStaffLegalEntitiesByIdArchiveResponseObject, error) {
	var saved store.StaffLegalEntity
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, legalEntityDefaultLock); err != nil {
			return err
		}
		row, err := txq.LockLegalEntity(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock legal entity %d: %w", req.Id, err)
		}
		if row.IsDefault {
			return refuseConflict(structureDefault("legal entity"))
		}
		// The default business unit's legal entity is never archived either:
		// the unit an employment is put in by default must not sit under an
		// archived legal entity. A business unit write making a unit the
		// default share-locks its legal entity first (checkUnitLegalEntity),
		// which this transaction's row lock excludes, so the read cannot race.
		holds, err := txq.HoldsDefaultBusinessUnit(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("staff: read the default business unit: %w", err)
		}
		if holds {
			return refuseConflict(conflict(codeStructureDefault, "The default business unit",
				"The default business unit is under this legal entity. Make a business unit under another legal entity the default first."))
		}
		saved, err = txq.ArchiveLegalEntity(ctx, store.ArchiveLegalEntityParams{ID: row.ID, Now: s.deps.Clock()})
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PostStaffLegalEntitiesByIdArchive404Response{}, nil
		}
		return gen.PostStaffLegalEntitiesByIdArchive409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: archive legal entity %d: %w", req.Id, err)
	}
	return gen.PostStaffLegalEntitiesByIdArchive200JSONResponse(legalEntityResponse(saved)), nil
}

// ---- Business units ---------------------------------------------------------

// parsedBusinessUnit is one validated business unit body.
type parsedBusinessUnit struct {
	LegalEntityID int64
	Name          string
	OrgNumber     *string
}

func parseBusinessUnit(body gen.StaffBusinessUnitRequest) (parsedBusinessUnit, fieldErrors) {
	var f fieldErrors
	out := parsedBusinessUnit{LegalEntityID: body.LegalEntityId}
	var msg string
	out.Name, msg = requiredText("A name", body.Name, structureNameMaxLength)
	f.add("name", msg)
	out.OrgNumber, msg = parseOrgNumber(body.OrgNumber)
	f.addCoded("orgNumber", codeOrgNumberInvalid, msg)
	return out, f
}

// checkUnitLegalEntity is the rule for the legal entity a unit names, judged
// under a share lock on it so it cannot be archived between the check and the
// write: it must exist, and a unit is never put under an archived one — but a
// unit already under a legal entity archived since keeps it (current is its
// legal entity before this write, 0 for a new unit).
//
// makeDefault adds the default's own rule: the default unit is never under an
// archived legal entity, the mirror of the archive refusing a legal entity
// that holds the default unit.
func checkUnitLegalEntity(ctx context.Context, txq *store.Queries, id, current int64, makeDefault bool) error {
	le, err := txq.ShareLegalEntity(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return refuseInvalid(invalidBusinessUnitTitle,
			fieldErrors{errs: fieldError("legalEntityId", fmt.Sprintf("No legal entity has id %d", id))})
	} else if err != nil {
		return fmt.Errorf("staff: read legal entity %d: %w", id, err)
	}
	if le.ArchivedAt != nil && (le.ID != current || makeDefault) {
		return refuseConflict(structureArchived(fmt.Sprintf("Legal entity %s is archived; a business unit cannot be put under it or be the default under it.", le.Name)))
	}
	return nil
}

// GetStaffBusinessUnits List business units
// (GET /api/v1/staff/business-units)
func (s *server) GetStaffBusinessUnits(ctx context.Context, _ gen.GetStaffBusinessUnitsRequestObject) (gen.GetStaffBusinessUnitsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListBusinessUnits(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the business units: %w", err)
	}
	return gen.GetStaffBusinessUnits200JSONResponse(mapRows(rows, businessUnitResponse)), nil
}

// PostStaffBusinessUnits Add a business unit
// (POST /api/v1/staff/business-units)
func (s *server) PostStaffBusinessUnits(ctx context.Context, req gen.PostStaffBusinessUnitsRequestObject) (gen.PostStaffBusinessUnitsResponseObject, error) {
	var body gen.StaffBusinessUnitRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseBusinessUnit(body)
	if f.failed() {
		return gen.PostStaffBusinessUnits400ApplicationProblemPlusJSONResponse(invalid(invalidBusinessUnitTitle, f)), nil
	}
	var created store.StaffBusinessUnit
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, businessUnitDefaultLock); err != nil {
			return err
		}
		hasDefault, err := txq.HasDefaultBusinessUnit(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the default business unit: %w", err)
		}
		makeDefault := !hasDefault || (body.IsDefault != nil && *body.IsDefault)
		if err := checkUnitLegalEntity(ctx, txq, parsed.LegalEntityID, 0, makeDefault); err != nil {
			return err
		}
		if makeDefault && hasDefault {
			if err := txq.ClearBusinessUnitDefault(ctx, store.ClearBusinessUnitDefaultParams{Keep: 0, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: move the default business unit: %w", err)
			}
		}
		created, err = txq.InsertBusinessUnit(ctx, store.InsertBusinessUnitParams{
			LegalEntityID: parsed.LegalEntityID, Name: parsed.Name, OrgNumber: parsed.OrgNumber,
			IsDefault: makeDefault, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, businessUnitOrgIndex) {
			return orgNumberTaken(invalidBusinessUnitTitle)
		}
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffBusinessUnits409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffBusinessUnits400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a business unit: %w", err)
	}
	return gen.PostStaffBusinessUnits201JSONResponse(businessUnitResponse(created)), nil
}

// PutStaffBusinessUnitsById Change a business unit
// (PUT /api/v1/staff/business-units/{id})
func (s *server) PutStaffBusinessUnitsById(ctx context.Context, req gen.PutStaffBusinessUnitsByIdRequestObject) (gen.PutStaffBusinessUnitsByIdResponseObject, error) {
	var body gen.StaffBusinessUnitRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffBusinessUnitsById400ApplicationProblemPlusJSONResponse(invalid(invalidBusinessUnitTitle, badIfMatch())), nil
	}
	parsed, f := parseBusinessUnit(body)
	if f.failed() {
		return gen.PutStaffBusinessUnitsById400ApplicationProblemPlusJSONResponse(invalid(invalidBusinessUnitTitle, f)), nil
	}
	var saved store.StaffBusinessUnit
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, businessUnitDefaultLock); err != nil {
			return err
		}
		row, err := txq.LockBusinessUnit(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock business unit %d: %w", req.Id, err)
		}
		if err := checkRevision("business unit", row.Revision, revision); err != nil {
			return err
		}
		becomesDefault := body.IsDefault != nil && *body.IsDefault && !row.IsDefault
		if err := checkUnitLegalEntity(ctx, txq, parsed.LegalEntityID, row.LegalEntityID, becomesDefault); err != nil {
			return err
		}
		makeDefault := false
		if body.IsDefault != nil {
			switch {
			case *body.IsDefault && row.ArchivedAt != nil:
				return refuseConflict(structureArchived("An archived business unit cannot be the default."))
			case *body.IsDefault && !row.IsDefault:
				if err := txq.ClearBusinessUnitDefault(ctx, store.ClearBusinessUnitDefaultParams{Keep: row.ID, Now: s.deps.Clock()}); err != nil {
					return fmt.Errorf("staff: move the default business unit: %w", err)
				}
				makeDefault = true
			case !*body.IsDefault && row.IsDefault:
				return refuseConflict(structureDefault("business unit"))
			}
		}
		saved, err = txq.UpdateBusinessUnit(ctx, store.UpdateBusinessUnitParams{
			ID: row.ID, LegalEntityID: parsed.LegalEntityID, Name: parsed.Name, OrgNumber: parsed.OrgNumber,
			MakeDefault: makeDefault, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, businessUnitOrgIndex) {
			return orgNumberTaken(invalidBusinessUnitTitle)
		}
		return err
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffBusinessUnitsById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffBusinessUnitsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffBusinessUnitsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change business unit %d: %w", req.Id, err)
	}
	return gen.PutStaffBusinessUnitsById200JSONResponse(businessUnitResponse(saved)), nil
}

// PostStaffBusinessUnitsByIdArchive Archive a business unit
// (POST /api/v1/staff/business-units/{id}/archive)
func (s *server) PostStaffBusinessUnitsByIdArchive(ctx context.Context, req gen.PostStaffBusinessUnitsByIdArchiveRequestObject) (gen.PostStaffBusinessUnitsByIdArchiveResponseObject, error) {
	var saved store.StaffBusinessUnit
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, businessUnitDefaultLock); err != nil {
			return err
		}
		row, err := txq.LockBusinessUnit(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock business unit %d: %w", req.Id, err)
		}
		if row.IsDefault {
			return refuseConflict(structureDefault("business unit"))
		}
		saved, err = txq.ArchiveBusinessUnit(ctx, store.ArchiveBusinessUnitParams{ID: row.ID, Now: s.deps.Clock()})
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PostStaffBusinessUnitsByIdArchive404Response{}, nil
		}
		return gen.PostStaffBusinessUnitsByIdArchive409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: archive business unit %d: %w", req.Id, err)
	}
	return gen.PostStaffBusinessUnitsByIdArchive200JSONResponse(businessUnitResponse(saved)), nil
}

// ---- Departments ------------------------------------------------------------

// parsedDepartment is one validated department body.
type parsedDepartment struct {
	Code            string
	Name            string
	ManagerPersonID *int64
	CostCentre      *string
	Division        *string
	ParentID        *int64
}

func parseDepartment(body gen.StaffDepartmentRequest) (parsedDepartment, fieldErrors) {
	var f fieldErrors
	out := parsedDepartment{ManagerPersonID: body.ManagerPersonId, ParentID: body.ParentId}
	var msg string
	out.Code, msg = parseCode(body.Code)
	f.add("code", msg)
	out.Name, msg = requiredText("A name", body.Name, structureNameMaxLength)
	f.add("name", msg)
	out.CostCentre, msg = optionalText("A cost centre", body.CostCentre, costCentreMaxLength)
	f.add("costCentre", msg)
	out.Division, msg = optionalText("A division", body.Division, divisionMaxLength)
	f.add("division", msg)
	return out, f
}

// checkDepartmentEdges is the rule for a department's manager and parent,
// judged under the manager-graph lock the caller holds: the manager is an
// existing person; the parent an existing department, held against an archive
// by a share lock, never an archived one the department did not already have
// (current is its parent before this write, nil for a new department or one
// at the top), and never the department itself or one below it. id is the
// department, 0 for a new one.
func checkDepartmentEdges(ctx context.Context, txq *store.Queries, id int64, current *int64, d parsedDepartment) error {
	if d.ManagerPersonID != nil {
		exists, err := txq.PersonExists(ctx, *d.ManagerPersonID)
		if err != nil {
			return fmt.Errorf("staff: read person %d: %w", *d.ManagerPersonID, err)
		}
		if !exists {
			return refuseInvalid(invalidDepartmentTitle,
				fieldErrors{errs: fieldError("managerPersonId", fmt.Sprintf("No person has id %d", *d.ManagerPersonID))})
		}
	}
	if d.ParentID == nil {
		return nil
	}
	parent, err := txq.ShareDepartment(ctx, *d.ParentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return refuseInvalid(invalidDepartmentTitle,
			fieldErrors{errs: fieldError("parentId", fmt.Sprintf("No department has id %d", *d.ParentID))})
	} else if err != nil {
		return fmt.Errorf("staff: read department %d: %w", *d.ParentID, err)
	}
	if parent.ArchivedAt != nil && (current == nil || *current != parent.ID) {
		return refuseConflict(structureArchived(fmt.Sprintf("Department %s is archived; a department cannot be put under it.", parent.Code)))
	}
	rows, err := txq.DepartmentParents(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the department graph: %w", err)
	}
	parents := make(map[int64]*int64, len(rows))
	for _, r := range rows {
		parents[r.ID] = r.ParentID
	}
	if departmentCycle(parents, id, parent.ID) {
		return refuseConflict(conflict(codeDepartmentCycle, "Department cycle",
			fmt.Sprintf("Department %s is this department or sits below it, so it cannot be its parent.", parent.Code)))
	}
	return nil
}

// departmentWriteError is a department write's own database refusal: a code
// another department holds, however it is cased, or a manager deleted since
// the check.
func departmentWriteError(err error) error {
	switch {
	case db.IsUniqueViolation(err, departmentCodeIndex):
		return refuseInvalid(invalidDepartmentTitle, fieldErrors{errs: fieldError("code", "Another department already has this code")})
	case db.IsForeignKeyViolation(err, "departments_manager_person_id_fkey"):
		return refuseInvalid(invalidDepartmentTitle, fieldErrors{errs: fieldError("managerPersonId", "The person no longer exists")})
	}
	return err
}

// GetStaffDepartments List departments
// (GET /api/v1/staff/departments)
func (s *server) GetStaffDepartments(ctx context.Context, _ gen.GetStaffDepartmentsRequestObject) (gen.GetStaffDepartmentsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListDepartments(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the departments: %w", err)
	}
	return gen.GetStaffDepartments200JSONResponse(mapRows(rows, departmentResponse)), nil
}

// PostStaffDepartments Add a department
// (POST /api/v1/staff/departments)
func (s *server) PostStaffDepartments(ctx context.Context, req gen.PostStaffDepartmentsRequestObject) (gen.PostStaffDepartmentsResponseObject, error) {
	var body gen.StaffDepartmentRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseDepartment(body)
	if f.failed() {
		return gen.PostStaffDepartments400ApplicationProblemPlusJSONResponse(invalid(invalidDepartmentTitle, f)), nil
	}
	var created store.StaffDepartment
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		if err := checkDepartmentEdges(ctx, txq, 0, nil, parsed); err != nil {
			return err
		}
		var err error
		created, err = txq.InsertDepartment(ctx, store.InsertDepartmentParams{
			Code: parsed.Code, Name: parsed.Name, ManagerPersonID: parsed.ManagerPersonID,
			CostCentre: parsed.CostCentre, Division: parsed.Division, ParentID: parsed.ParentID, Now: s.deps.Clock(),
		})
		return departmentWriteError(err)
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffDepartments409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffDepartments400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a department: %w", err)
	}
	return gen.PostStaffDepartments201JSONResponse(departmentResponse(created)), nil
}

// PutStaffDepartmentsById Change a department
// (PUT /api/v1/staff/departments/{id})
func (s *server) PutStaffDepartmentsById(ctx context.Context, req gen.PutStaffDepartmentsByIdRequestObject) (gen.PutStaffDepartmentsByIdResponseObject, error) {
	var body gen.StaffDepartmentRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffDepartmentsById400ApplicationProblemPlusJSONResponse(invalid(invalidDepartmentTitle, badIfMatch())), nil
	}
	parsed, f := parseDepartment(body)
	if f.failed() {
		return gen.PutStaffDepartmentsById400ApplicationProblemPlusJSONResponse(invalid(invalidDepartmentTitle, f)), nil
	}
	var saved store.StaffDepartment
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		row, err := txq.LockDepartment(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock department %d: %w", req.Id, err)
		}
		if err := checkRevision("department", row.Revision, revision); err != nil {
			return err
		}
		if err := checkDepartmentEdges(ctx, txq, row.ID, row.ParentID, parsed); err != nil {
			return err
		}
		saved, err = txq.UpdateDepartment(ctx, store.UpdateDepartmentParams{
			ID: row.ID, Code: parsed.Code, Name: parsed.Name, ManagerPersonID: parsed.ManagerPersonID,
			CostCentre: parsed.CostCentre, Division: parsed.Division, ParentID: parsed.ParentID, Now: s.deps.Clock(),
		})
		return departmentWriteError(err)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffDepartmentsById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffDepartmentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffDepartmentsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change department %d: %w", req.Id, err)
	}
	return gen.PutStaffDepartmentsById200JSONResponse(departmentResponse(saved)), nil
}

// PostStaffDepartmentsByIdArchive Archive a department
// (POST /api/v1/staff/departments/{id}/archive)
//
// Its children keep it as their parent, and the terms that name it keep it.
func (s *server) PostStaffDepartmentsByIdArchive(ctx context.Context, req gen.PostStaffDepartmentsByIdArchiveRequestObject) (gen.PostStaffDepartmentsByIdArchiveResponseObject, error) {
	var saved store.StaffDepartment
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		saved, err = txq.ArchiveDepartment(ctx, store.ArchiveDepartmentParams{ID: req.Id, Now: s.deps.Clock()})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffDepartmentsByIdArchive404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: archive department %d: %w", req.Id, err)
	}
	return gen.PostStaffDepartmentsByIdArchive200JSONResponse(departmentResponse(saved)), nil
}

// ---- Locations --------------------------------------------------------------

// parsedLocation is one validated location body.
type parsedLocation struct {
	Name       string
	Street     *string
	PostalCode *string
	City       *string
	Country    string
}

func parseLocation(body gen.StaffLocationRequest) (parsedLocation, fieldErrors) {
	var f fieldErrors
	var out parsedLocation
	var msg string
	out.Name, msg = requiredText("A name", body.Name, structureNameMaxLength)
	f.add("name", msg)
	out.Street, msg = optionalText("A street", body.Street, streetMaxLength)
	f.add("street", msg)
	out.PostalCode, msg = optionalText("A postal code", body.PostalCode, postalCodeMaxLength)
	f.add("postalCode", msg)
	out.City, msg = optionalText("A city", body.City, cityMaxLength)
	f.add("city", msg)
	out.Country, msg = parseCountry(body.Country, "NO")
	f.add("country", msg)
	return out, f
}

// GetStaffLocations List locations
// (GET /api/v1/staff/locations)
func (s *server) GetStaffLocations(ctx context.Context, _ gen.GetStaffLocationsRequestObject) (gen.GetStaffLocationsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the locations: %w", err)
	}
	return gen.GetStaffLocations200JSONResponse(mapRows(rows, locationResponse)), nil
}

// PostStaffLocations Add a location
// (POST /api/v1/staff/locations)
func (s *server) PostStaffLocations(ctx context.Context, req gen.PostStaffLocationsRequestObject) (gen.PostStaffLocationsResponseObject, error) {
	var body gen.StaffLocationRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseLocation(body)
	if f.failed() {
		return gen.PostStaffLocations400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, f)), nil
	}
	created, err := store.New(s.deps.Pool).InsertLocation(ctx, store.InsertLocationParams{
		Name: parsed.Name, Street: parsed.Street, PostalCode: parsed.PostalCode, City: parsed.City,
		Country: parsed.Country, Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("staff: add a location: %w", err)
	}
	return gen.PostStaffLocations201JSONResponse(locationResponse(created)), nil
}

// PutStaffLocationsById Change a location
// (PUT /api/v1/staff/locations/{id})
func (s *server) PutStaffLocationsById(ctx context.Context, req gen.PutStaffLocationsByIdRequestObject) (gen.PutStaffLocationsByIdResponseObject, error) {
	var body gen.StaffLocationRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffLocationsById400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, badIfMatch())), nil
	}
	parsed, f := parseLocation(body)
	if f.failed() {
		return gen.PutStaffLocationsById400ApplicationProblemPlusJSONResponse(invalid(invalidLocationTitle, f)), nil
	}
	var saved store.StaffLocation
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockLocation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock location %d: %w", req.Id, err)
		}
		if err := checkRevision("location", row.Revision, revision); err != nil {
			return err
		}
		saved, err = txq.UpdateLocation(ctx, store.UpdateLocationParams{
			ID: row.ID, Name: parsed.Name, Street: parsed.Street, PostalCode: parsed.PostalCode, City: parsed.City,
			Country: parsed.Country, Now: s.deps.Clock(),
		})
		return err
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PutStaffLocationsById404Response{}, nil
		}
		return gen.PutStaffLocationsById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change location %d: %w", req.Id, err)
	}
	return gen.PutStaffLocationsById200JSONResponse(locationResponse(saved)), nil
}

// PostStaffLocationsByIdArchive Archive a location
// (POST /api/v1/staff/locations/{id}/archive)
func (s *server) PostStaffLocationsByIdArchive(ctx context.Context, req gen.PostStaffLocationsByIdArchiveRequestObject) (gen.PostStaffLocationsByIdArchiveResponseObject, error) {
	saved, err := store.New(s.deps.Pool).ArchiveLocation(ctx, store.ArchiveLocationParams{ID: req.Id, Now: s.deps.Clock()})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostStaffLocationsByIdArchive404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: archive location %d: %w", req.Id, err)
	}
	return gen.PostStaffLocationsByIdArchive200JSONResponse(locationResponse(saved)), nil
}
