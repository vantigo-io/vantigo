package staff

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file turns stored rows into the contract's responses. Every optional
// field is absent when the column is NULL, never null and never an empty
// string — a nil pointer is what omitempty leaves out.

// settingsResponse is the settings the deliveries so far expose, the
// retention block and the acknowledged thresholds.
func settingsResponse(row store.StaffSetting, retention gen.StaffRetentionState, zone *time.Location) (gen.StaffSettings, error) {
	acknowledged, err := parseAcknowledged(row.ThresholdsAcknowledged)
	if err != nil {
		return gen.StaffSettings{}, err
	}
	return gen.StaffSettings{
		TimeZone:               zone.String(),
		EmployeeNumberPrefix:   row.EmployeeNumberPrefix,
		NextEmployeeNumber:     row.NextEmployeeNumber,
		PensionProvider:        row.PensionProvider,
		InjuryInsurer:          row.InjuryInsurer,
		RetentionGraceDays:     int32(row.RetentionGraceDays),
		HealthRetentionDays:    int32(row.HealthRetentionDays),
		Retention:              retention,
		ThresholdsAcknowledged: thresholdsResponse(acknowledged),
		Revision:               row.Revision,

		SelfCertPolicy:             gen.StaffSelfCertPolicy(row.SelfCertPolicy),
		SelfCertDaysPerOccurrence:  int32(row.SelfCertDaysPerOccurrence),
		SelfCertOccurrencesPerYear: int32(row.SelfCertOccurrencesPerYear),
		SelfCertDaysPerYear:        int32(row.SelfCertDaysPerYear),
		SelfCertAfterMonths:        int32(row.SelfCertAfterMonths),
		AutoCarryOver:              row.AutoCarryOver,
		AutoCarryOverSince:         dateOf(row.AutoCarryOverSince),

		CreateBeforeDays:        int32(row.CreateBeforeDays),
		DeleteExternalAfterDays: int32Of(row.DeleteExternalAfterDays),

		ErasePregnancyHealthTypes: row.ErasePregnancyHealthTypes,
		EraseSickCaseDates:        row.EraseSickCaseDates,

		DefaultAccountKind: gen.StaffAccountKind(row.DefaultAccountKind),
		DirectoryFields:    directoryFieldsOf(row),
	}, nil
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

// stringOf is an optional value of one of the contract's enum types as the
// text column stores it, nil when absent — enumOf the other way.
func stringOf[T ~string](v *T) *string {
	if v == nil {
		return nil
	}
	out := string(*v)
	return &out
}
