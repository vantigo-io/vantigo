package staff

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the external spec (Entra design D7, D8): what a person's
// account in a provider's directory should look like, from the register —
// the names, the work email only (never the private one), the language,
// the work country (the usage location), the hire and leave dates while
// their switches are on, the directory profile's switched-on fields
// (directory_profile.go, which the Account tab shows from the same values),
// and the manager as their own external account's id, and the licences
// the person's groups carry (Entra design D10, delivery D); Flags.Fresh is
// set by the caller for HR's Run now.

// externalSpecFacts is the external spec the worker hands a provider, and
// whether the person's effective manager has no external account under the
// provider (the code manager_not_in_entra on the link, once).
type externalSpecFacts struct {
	spec           contracts.ExternalSpec
	managerMissing bool
}

// readExternalSpec builds personID's external spec under provider on q (the
// claim's snapshot), as of today, with the settings' directory switches.
func readExternalSpec(ctx context.Context, q *store.Queries, set store.StaffSetting, row store.GetPersonRow, provider string, today time.Time) (externalSpecFacts, error) {
	var out externalSpecFacts
	values, err := directoryProfileValues(ctx, q, row.ID, today)
	if err != nil {
		return out, err
	}
	on := directoryFieldsOf(set)
	spec := contracts.ExternalSpec{
		EmployeeNumber: row.EmployeeNumber, GivenName: row.GivenName, FamilyName: row.FamilyName,
		DisplayName: row.DisplayName, WorkEmail: deref(row.WorkEmail), WorkCountry: row.WorkCountry,
		Fields: directorySpecFields(on, values),
	}
	if on["preferredLanguage"] {
		spec.PreferredLanguage = row.PreferredLanguage
	}
	if on["employeeHireDate"] {
		spec.HireDate = specDate(values["employeeHireDate"])
	}
	if on["employeeLeaveDateTime"] {
		spec.LeaveDate = specDate(values["employeeLeaveDateTime"])
	}
	if on["manager"] {
		manager, err := externalManagerOf(ctx, q, row.ID, today)
		if err != nil {
			return out, err
		}
		if manager != nil {
			links, err := q.ProviderLinksOfPeople(ctx, store.ProviderLinksOfPeopleParams{PersonIds: []int64{*manager}, Provider: provider})
			if err != nil {
				return out, fmt.Errorf("staff: read person %d's manager's external account: %w", row.ID, err)
			}
			if len(links) > 0 {
				spec.ManagerExternalID = links[0].ExternalID
			} else {
				out.managerMissing = true
			}
		}
	}
	// The licences are the union of the licences traits of the person's
	// groups under the provider (D10): a change of a group's trait or of
	// the person's membership moves the spec's hash, and the next claim is
	// an Update. None leaves the field nil, as before any group had one.
	licences, err := q.LicencesOfPerson(ctx, store.LicencesOfPersonParams{PersonID: row.ID, Provider: provider})
	if err != nil {
		return out, fmt.Errorf("staff: read person %d's licences: %w", row.ID, err)
	}
	if len(licences) > 0 {
		slices.Sort(licences)
		spec.Licences = slices.Compact(licences)
	}
	out.spec = spec
	return out, nil
}

// specDate is a "2006-01-02" day as the spec's civil date, nil for "".
func specDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil
	}
	return &d
}

// externalManagerOf is the person's effective manager (the terms' today,
// else the department's chain — the rule reportsWithoutManager reads) on the
// employment the directory profile reads the place from, nil for none.
func externalManagerOf(ctx context.Context, q *store.Queries, personID int64, today time.Time) (*int64, error) {
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	if len(employments) == 0 {
		return nil, nil
	}
	e := directoryPlace(employments, today)
	g, _, err := loadManagerGraphAndDepartments(ctx, q, []int64{personID})
	if err != nil {
		return nil, err
	}
	for _, ge := range g.employments[personID] {
		if ge.ID == e.ID {
			return g.effectiveManager(ge, judgedOn(e, today)), nil
		}
	}
	return nil, nil
}

// externalSpecHash is a provider link's last_spec_hash: SHA-256 over the
// spec as JSON (its map's keys sorted), the flags left out — a Run now is
// not a change of the account.
func externalSpecHash(spec contracts.ExternalSpec) []byte {
	spec.Flags = contracts.SpecFlags{}
	b, err := json.Marshal(spec)
	if err != nil {
		// A spec of strings, dates and a string map always marshals.
		panic(fmt.Sprintf("staff: hash an external spec: %v", err))
	}
	sum := sha256.Sum256(b)
	return sum[:]
}
