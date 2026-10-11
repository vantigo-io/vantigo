package staff

import (
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file turns stored rows into the contract's responses. Every optional
// field is absent when the column is NULL, never null and never an empty
// string — a nil pointer is what omitempty leaves out.

// settingsResponse is the settings delivery A1 exposes.
func settingsResponse(row store.StaffSetting) gen.StaffSettings {
	return gen.StaffSettings{
		TimeZone:             row.TimeZone,
		EmployeeNumberPrefix: row.EmployeeNumberPrefix,
		NextEmployeeNumber:   row.NextEmployeeNumber,
		PensionProvider:      row.PensionProvider,
		InjuryInsurer:        row.InjuryInsurer,
		Revision:             row.Revision,
	}
}

func legalEntityResponse(row store.StaffLegalEntity) gen.StaffLegalEntity {
	return gen.StaffLegalEntity{
		Id: row.ID, Name: row.Name, OrgNumber: row.OrgNumber, IsDefault: row.IsDefault,
		ArchivedAt: row.ArchivedAt, Revision: row.Revision,
	}
}

func businessUnitResponse(row store.StaffBusinessUnit) gen.StaffBusinessUnit {
	return gen.StaffBusinessUnit{
		Id: row.ID, LegalEntityId: row.LegalEntityID, Name: row.Name, OrgNumber: row.OrgNumber,
		IsDefault: row.IsDefault, ArchivedAt: row.ArchivedAt, Revision: row.Revision,
	}
}

func departmentResponse(row store.StaffDepartment) gen.StaffDepartment {
	return gen.StaffDepartment{
		Id: row.ID, Code: row.Code, Name: row.Name, ManagerPersonId: row.ManagerPersonID,
		CostCentre: row.CostCentre, Division: row.Division, ParentId: row.ParentID,
		ArchivedAt: row.ArchivedAt, Revision: row.Revision,
	}
}

func locationResponse(row store.StaffLocation) gen.StaffLocation {
	return gen.StaffLocation{
		Id: row.ID, Name: row.Name, Street: row.Street, PostalCode: row.PostalCode, City: row.City,
		Country: row.Country, ArchivedAt: row.ArchivedAt, Revision: row.Revision,
	}
}

// mapRows is rows through one response function, never nil: an empty list is
// [] on the wire, not null.
func mapRows[R, T any](rows []R, f func(R) T) []T {
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		out = append(out, f(r))
	}
	return out
}

// dateOf is a nullable date column as the contract's date, nil when NULL.
func dateOf(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// enumOf is a nullable text column as one of the contract's enum types, nil
// when NULL. The value is the module's own, written through the enum's
// validation, so it is not judged again on the way out.
func enumOf[T ~string](v *string) *T {
	if v == nil {
		return nil
	}
	out := T(*v)
	return &out
}
