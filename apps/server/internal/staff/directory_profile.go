package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the directory profile (Entra design D8, research §7.4): the
// fixed mapping of a person's register fields onto the attributes an
// external provider writes on their account, the installation's switches
// over the rows it may turn off (staff.settings.directory_fields, 00060's),
// and the value each row would carry today. The Account tab shows it to
// whoever reads the account (plan reading 9: the person's own data as the
// directory would see it); the accounts worker builds ExternalSpec.Fields
// from the same two (directorySpecFields), so what HR reads is what the
// provider is sent. Private contact details are never mapped.

// directoryField is one row of the profile: its key, whether the
// installation may switch it off, and the directory attributes it fills on
// ExternalSpec.Fields — none for a row the spec carries in a field of its
// own (the names, the number, the dates, the language, the manager).
type directoryField struct {
	key        string
	switchable bool
	attributes []string
}

// directoryFields is the profile's rows in the order the Account tab lists
// them: the six always written first, then the switchable ones.
var directoryFields = []directoryField{
	{key: "givenName"},
	{key: "familyName"},
	{key: "displayName"},
	{key: "employeeId"},
	{key: "userPrincipalName"},
	{key: "usageLocation"},
	{key: "employeeHireDate", switchable: true},
	{key: "employeeLeaveDateTime", switchable: true},
	{key: "employeeType", switchable: true, attributes: []string{"employeeType"}},
	{key: "jobTitle", switchable: true, attributes: []string{"jobTitle"}},
	{key: "department", switchable: true, attributes: []string{"department"}},
	{key: "companyName", switchable: true, attributes: []string{"companyName"}},
	{key: "employeeOrgData", switchable: true, attributes: []string{"costCenter", "division"}},
	{key: "officeLocation", switchable: true, attributes: []string{"officeLocation"}},
	{key: "workAddress", switchable: true, attributes: []string{"streetAddress", "postalCode", "city", "country"}},
	{key: "phones", switchable: true, attributes: []string{"businessPhone", "mobilePhone"}},
	{key: "preferredLanguage", switchable: true},
	{key: "manager", switchable: true},
}

// directoryFieldDefaults is each switchable row's switch until the
// installation changes it (plan "Exact values", the column's default): the
// work address and the phones off, the rest on.
var directoryFieldDefaults = map[string]bool{
	"employeeHireDate": true, "employeeLeaveDateTime": true, "employeeType": true, "jobTitle": true,
	"department": true, "companyName": true, "employeeOrgData": true, "officeLocation": true,
	"workAddress": false, "phones": false, "preferredLanguage": true, "manager": true,
}

// switchableDirectoryField reports whether key is a row the installation may
// switch.
func switchableDirectoryField(key string) bool {
	_, ok := directoryFieldDefaults[key]
	return ok
}

// directoryFieldsOf is the installation's switches over every switchable row:
// the stored map over the defaults, a stored key that names no switchable
// row ignored. A column the module did not write reads as the defaults.
func directoryFieldsOf(set store.StaffSetting) map[string]bool {
	out := make(map[string]bool, len(directoryFieldDefaults))
	for k, v := range directoryFieldDefaults {
		out[k] = v
	}
	stored := map[string]bool{}
	if err := json.Unmarshal(set.DirectoryFields, &stored); err != nil {
		return out
	}
	for k, v := range stored {
		if switchableDirectoryField(k) {
			out[k] = v
		}
	}
	return out
}

// parseDirectoryFields judges a replace's switches into f: every key a
// switchable row (directoryFields.<key> otherwise), nil when the body left
// them out, which keeps every switch. The caller merges what it answers over
// the row it locked.
func parseDirectoryFields(body *map[string]bool, f *fieldErrors) map[string]bool {
	if body == nil {
		return nil
	}
	out := make(map[string]bool, len(*body))
	for k, v := range *body {
		if !switchableDirectoryField(k) {
			f.add("directoryFields."+k, fmt.Sprintf("'%s' is not a directory field the installation may switch", k))
			continue
		}
		out[k] = v
	}
	return out
}

// mergeDirectoryFields is the stored switches with changed laid over them, as
// the column stores them; nil when changed is nil, which keeps the column.
func mergeDirectoryFields(current store.StaffSetting, changed map[string]bool) ([]byte, error) {
	if changed == nil {
		return nil, nil
	}
	merged := directoryFieldsOf(current)
	for k, v := range changed {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("staff: write the directory fields: %w", err)
	}
	return out, nil
}

// directoryLanguages is the person's preferred language as the directory
// names it.
var directoryLanguages = map[string]string{"nb": "nb-NO", "en": "en-GB"}

// directoryProfileValues is the value every row would carry today for the
// person, by key, "" when the register holds none — and, beside the rows,
// the directory attributes ExternalSpec.Fields carries (costCenter,
// division, streetAddress, postalCode, city, country, businessPhone,
// mobilePhone; jobTitle, department, companyName, employeeType and
// officeLocation are rows of their own). Read on q, so the caller's snapshot
// decides what "today" sees; errPersonNotFound when there is no such person.
//
// The place is the person's earliest employment in force today, else the
// earliest to come (judged on its first day, so a joiner prepared before it
// is sent the place they start in), else the one that ended last (judged on
// its last day). The hire date is the earliest start of an employment not
// ended before today, else that last one's start; the leave date the latest
// end when every employment has one, else none. The manager is the place's
// effective manager (effectiveManager), by their display name.
func directoryProfileValues(ctx context.Context, q *store.Queries, personID int64, today time.Time) (map[string]string, error) {
	person, err := q.GetPerson(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errPersonNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d for the directory profile: %w", personID, err)
	}
	out := make(map[string]string, 32)
	for _, f := range directoryFields {
		out[f.key] = ""
		for _, a := range f.attributes {
			out[a] = ""
		}
	}
	out["givenName"] = person.GivenName
	out["familyName"] = person.FamilyName
	out["displayName"] = person.DisplayName
	out["employeeId"] = person.EmployeeNumber
	out["userPrincipalName"] = deref(person.WorkEmail)
	out["usageLocation"] = person.WorkCountry
	out["preferredLanguage"] = directoryLanguages[person.PreferredLanguage]
	out["businessPhone"] = deref(person.PhoneWork)
	out["mobilePhone"] = deref(person.PhoneMobile)
	out["phones"] = joinPresent(" / ", out["businessPhone"], out["mobilePhone"])

	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	if len(employments) == 0 {
		return out, nil
	}
	hire, leave := directoryDates(employments, today)
	if hire != nil {
		out["employeeHireDate"] = hire.Format(time.DateOnly)
	}
	if leave != nil {
		out["employeeLeaveDateTime"] = leave.Format(time.DateOnly)
	}

	e := directoryPlace(employments, today)
	on := judgedOn(e, today)
	terms, err := q.ListTermsOfEmployments(ctx, []int64{e.ID})
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's terms: %w", personID, err)
	}
	g, departments, err := loadManagerGraphAndDepartments(ctx, q, []int64{personID})
	if err != nil {
		return nil, err
	}
	if err := directoryCompany(ctx, q, e.BusinessUnitID, out); err != nil {
		return nil, err
	}
	t := termsInForce(e, terms, on)
	if t == nil {
		return out, nil
	}
	out["jobTitle"] = deref(t.Title)
	out["employeeType"] = deref(t.EmployeeType)
	costCentre, division := t.CostCentre, t.Division
	if t.DepartmentID != nil {
		if i := slices.IndexFunc(departments, func(d store.StaffDepartment) bool { return d.ID == *t.DepartmentID }); i >= 0 {
			d := departments[i]
			out["department"] = d.Name
			if costCentre == nil {
				costCentre = d.CostCentre
			}
			if division == nil {
				division = d.Division
			}
		}
	}
	out["costCenter"] = deref(costCentre)
	out["division"] = deref(division)
	out["employeeOrgData"] = joinPresent(" / ", out["costCenter"], out["division"])
	if t.LocationID != nil {
		if err := directoryLocation(ctx, q, *t.LocationID, out); err != nil {
			return nil, err
		}
	}
	for _, ge := range g.employments[personID] {
		if ge.ID != e.ID {
			continue
		}
		if m := g.effectiveManager(ge, on); m != nil {
			names, err := personSummaries(ctx, q, []int64{*m})
			if err != nil {
				return nil, err
			}
			out["manager"] = names[*m].DisplayName
		}
	}
	return out, nil
}

// directoryPlace is the employment the profile reads the place from: the
// earliest in force today, else the earliest to come, else the one that
// ended last. employments is not empty and is ordered by start.
func directoryPlace(employments []store.StaffEmployment, today time.Time) store.StaffEmployment {
	var upcoming, ended *store.StaffEmployment
	for i := range employments {
		e := &employments[i]
		start := civil(e.StartDate)
		switch {
		case start.After(today):
			if upcoming == nil {
				upcoming = e
			}
		case !e.EndDate.Valid || !e.EndDate.Time.Before(today):
			return *e
		default:
			if ended == nil || e.EndDate.Time.After(ended.EndDate.Time) {
				ended = e
			}
		}
	}
	if upcoming != nil {
		return *upcoming
	}
	return *ended
}

// directoryDates is the hire date and the leave date the profile carries:
// the earliest start of an employment not ended before today, else the start
// of the one that ended last; the latest end when every employment has one,
// else none.
func directoryDates(employments []store.StaffEmployment, today time.Time) (hire, leave *time.Time) {
	var lastEnded *store.StaffEmployment
	open := false
	for i := range employments {
		e := &employments[i]
		if !e.EndDate.Valid {
			open = true
		} else if leave == nil || e.EndDate.Time.After(*leave) {
			end := e.EndDate.Time
			leave = &end
		}
		if e.EndDate.Valid && e.EndDate.Time.Before(today) {
			if lastEnded == nil || e.EndDate.Time.After(lastEnded.EndDate.Time) {
				lastEnded = e
			}
			continue
		}
		if start := civil(e.StartDate); hire == nil || start.Before(*hire) {
			hire = &start
		}
	}
	if hire == nil && lastEnded != nil {
		start := civil(lastEnded.StartDate)
		hire = &start
	}
	if open {
		leave = nil
	}
	return hire, leave
}

// directoryCompany fills companyName with the legal entity of the business
// unit the employment is in.
func directoryCompany(ctx context.Context, q *store.Queries, unitID int64, out map[string]string) error {
	units, err := q.ListBusinessUnits(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the business units: %w", err)
	}
	i := slices.IndexFunc(units, func(u store.StaffBusinessUnit) bool { return u.ID == unitID })
	if i < 0 {
		return nil
	}
	entity, err := q.GetLegalEntity(ctx, units[i].LegalEntityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("staff: read legal entity %d: %w", units[i].LegalEntityID, err)
	}
	out["companyName"] = entity.Name
	return nil
}

// directoryLocation fills officeLocation and the work address from the
// location the terms name.
func directoryLocation(ctx context.Context, q *store.Queries, id int64, out map[string]string) error {
	locations, err := q.ListLocations(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the locations: %w", err)
	}
	i := slices.IndexFunc(locations, func(l store.StaffLocation) bool { return l.ID == id })
	if i < 0 {
		return nil
	}
	l := locations[i]
	out["officeLocation"] = l.Name
	out["streetAddress"] = deref(l.Street)
	out["postalCode"] = deref(l.PostalCode)
	out["city"] = deref(l.City)
	out["country"] = l.Country
	out["workAddress"] = joinPresent(", ", out["streetAddress"], joinPresent(" ", out["postalCode"], out["city"]), out["country"])
	return nil
}

// directoryProfile is the profile as the Account tab reads it: every row in
// its order with its switch — always on for a row that is not switchable —
// and its value, absent when empty.
func directoryProfile(on map[string]bool, values map[string]string) []gen.StaffDirectoryField {
	out := make([]gen.StaffDirectoryField, 0, len(directoryFields))
	for _, f := range directoryFields {
		row := gen.StaffDirectoryField{Key: f.key, Switchable: f.switchable, On: !f.switchable || on[f.key]}
		if v := values[f.key]; v != "" {
			row.Value = &v
		}
		out = append(out, row)
	}
	return out
}

// directorySpecFields is ExternalSpec.Fields: the directory attributes of
// every switchable row switched on, each with its value, "" to clear it; a
// row switched off sends nothing, and the rows the spec carries in fields of
// its own (the dates, the language, the manager) send nothing here.
func directorySpecFields(on map[string]bool, values map[string]string) map[string]string {
	out := map[string]string{}
	for _, f := range directoryFields {
		if !f.switchable || !on[f.key] {
			continue
		}
		for _, a := range f.attributes {
			out[a] = values[a]
		}
	}
	return out
}

// joinPresent is the non-empty parts joined by sep.
func joinPresent(sep string, parts ...string) string {
	present := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			present = append(present, p)
		}
	}
	return strings.Join(present, sep)
}
