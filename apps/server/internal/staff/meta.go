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
// pickers and the labels need, so no client re-derives a rule this module owns. The
// capabilities delivery B and A2 add (canDecide, canSeeHealth) and C's
// accountMode arrive additively with them.

// GetStaffMeta Get the Staff metadata
// (GET /api/v1/staff/meta)
func (s *server) GetStaffMeta(ctx context.Context, _ gen.GetStaffMetaRequestObject) (gen.GetStaffMetaResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	meta := gen.StaffMeta{
		TimeZone: caller.Settings.TimeZone,
		Today:    openapi_types.Date{Time: caller.Today},
		Capabilities: gen.StaffCapabilities{
			CanViewDirectory:     caller.View,
			CanEdit:              caller.Manage,
			CanRevealIdentifiers: caller.PersonalData,
			CanSeePay:            caller.Manage || caller.PersonalData,
		},
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
