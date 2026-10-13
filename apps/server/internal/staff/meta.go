package staff

import (
	"context"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is GET /meta: the one read the Staff app makes before it draws
// anything. It answers what the caller may do, the business time zone with
// today's date in it, and — for staff:view or staff:manage — the structure the
// pickers and the labels need, so no client re-derives a rule this module owns.
// canSeeHealth is A2's — staff:personal-data, since the person reads their own
// health documents whatever the global answer says. Delivery B adds the
// absence types every caller may pick (the hidden ones never), the holiday
// schemes, and two capabilities: managesAnyone — the caller's linked person
// manages anyone today by the one manager rule (effectiveManager), so a
// manager who has left manages nobody — and canDecide — staff:approve,
// staff:manage, or managesAnyone: who may decide absence (design S12). C's
// accountMode arrives additively with C.

// GetStaffMeta Get the Staff metadata
// (GET /api/v1/staff/meta)
func (s *server) GetStaffMeta(ctx context.Context, _ gen.GetStaffMetaRequestObject) (gen.GetStaffMetaResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	managesAnyone, err := managesAnyoneToday(ctx, q, caller)
	if err != nil {
		return nil, err
	}
	types, err := q.ListAbsenceTypes(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("staff: list the absence types: %w", err)
	}
	schemes, err := q.ListHolidaySchemes(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the holiday schemes: %w", err)
	}
	meta := gen.StaffMeta{
		TimeZone: caller.Settings.TimeZone,
		Today:    openapi_types.Date{Time: caller.Today},
		Capabilities: gen.StaffCapabilities{
			CanViewDirectory:     caller.View,
			CanEdit:              caller.Manage,
			CanRevealIdentifiers: caller.PersonalData,
			CanSeePay:            caller.Manage || caller.PersonalData,
			CanSeeHealth:         caller.PersonalData,
			CanDecide:            caller.Approve || caller.Manage || managesAnyone,
			ManagesAnyone:        managesAnyone,
		},
		AbsenceTypes:   mapRows(types, absenceTypeResponse),
		HolidaySchemes: mapRows(schemes, holidaySchemeResponse),
	}
	// HR without the directory still names units and departments (plan
	// reading 15), so the structure is staff:manage's to read too.
	if caller.View || caller.Manage {
		structure, err := s.structure(ctx)
		if err != nil {
			return nil, err
		}
		meta.Structure = &structure
	}
	return gen.GetStaffMeta200JSONResponse(meta), nil
}

// managesAnyoneToday is whether the caller's linked person manages anyone
// today by the one manager rule (managersOf): someone's effective manager on
// an employment in force today. A caller linked to nobody manages nobody, and
// so does one who has left, whom effectiveManager skips. An EXISTS asks first
// whether any terms row or department names the person at all, so the walk
// over everyone's graph runs only for someone who may be a manager.
func managesAnyoneToday(ctx context.Context, q *store.Queries, c *callerContext) (bool, error) {
	if c.Person == nil {
		return false, nil
	}
	named, err := q.NamedAsManager(ctx, *c.Person)
	if err != nil {
		return false, fmt.Errorf("staff: read whether person %d is named as a manager: %w", *c.Person, err)
	}
	if !named {
		return false, nil
	}
	g, err := loadManagerGraph(ctx, q, nil)
	if err != nil {
		return false, err
	}
	return len(g.reportsOf(*c.Person, c.Today)) > 0, nil
}

// structure is the four structure tables read in one snapshot, so a business
// unit is never answered beside a list of legal entities that predates it.
func (s *server) structure(ctx context.Context) (gen.StaffStructure, error) {
	var out gen.StaffStructure
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		legalEntities, err := q.ListLegalEntities(ctx)
		if err != nil {
			return fmt.Errorf("staff: list the legal entities: %w", err)
		}
		businessUnits, err := q.ListBusinessUnits(ctx)
		if err != nil {
			return fmt.Errorf("staff: list the business units: %w", err)
		}
		departments, err := q.ListDepartments(ctx)
		if err != nil {
			return fmt.Errorf("staff: list the departments: %w", err)
		}
		locations, err := q.ListLocations(ctx)
		if err != nil {
			return fmt.Errorf("staff: list the locations: %w", err)
		}
		out = gen.StaffStructure{
			LegalEntities: mapRows(legalEntities, legalEntityResponse),
			BusinessUnits: mapRows(businessUnits, businessUnitResponse),
			Departments:   mapRows(departments, departmentResponse),
			Locations:     mapRows(locations, locationResponse),
		}
		return nil
	})
	return out, err
}
