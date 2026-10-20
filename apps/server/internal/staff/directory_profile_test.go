package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// directoryOrder is the directory profile's rows in the order the Account
// tab lists them (Entra design D8): the six always written, then the twelve
// the installation may switch.
var directoryOrder = []string{
	"givenName", "familyName", "displayName", "employeeId", "userPrincipalName", "usageLocation",
	"employeeHireDate", "employeeLeaveDateTime", "employeeType", "jobTitle", "department", "companyName",
	"employeeOrgData", "officeLocation", "workAddress", "phones", "preferredLanguage", "manager",
}

// profileRows is the person's directory profile by key, failing the test
// unless it is there in its order.
func profileRows(t *testing.T, acc gen.StaffPersonAccount) map[string]gen.StaffDirectoryField {
	t.Helper()
	if acc.DirectoryProfile == nil || len(*acc.DirectoryProfile) != len(directoryOrder) {
		t.Fatalf("directoryProfile = %v, want %d rows", acc.DirectoryProfile, len(directoryOrder))
	}
	out := map[string]gen.StaffDirectoryField{}
	for i, row := range *acc.DirectoryProfile {
		if row.Key != directoryOrder[i] {
			t.Errorf("row %d = %s, want %s", i, row.Key, directoryOrder[i])
		}
		out[row.Key] = row
	}
	return out
}

// The directory profile carries the value each row would have today, from
// the register: the names, the number, the work email as the UPN, the work
// country, the hire date, the terms' title and employee type, the
// department's name, the legal entity's name, the cost centre (the
// department's, the terms naming none) and the division (the terms'), the
// location's name and address, the phones, the language as the directory
// names it, and the effective manager — the department's. The six rows that
// are always written are on and not switchable; the work address and the
// phones are off by default, their values still shown.
func TestDirectoryProfile_TheValuesToday(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithIdentityProvisioners(entraProvider()))
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	day := today(t, h)
	boss := createPerson(t, hr, newPerson("Berit", "Sjef"))
	createEmployment(t, hr, boss.Id, newEmployment(day, terms()))
	dept := createDepartment(t, hr, map[string]any{
		"code": "ENG", "name": "Engineering", "costCentre": "CC-7", "division": "Platform", "managerPersonId": boss.Id,
	})
	var place gen.StaffLocation
	do(t, hr, http.MethodPost, locationsPath, map[string]any{
		"name": "Oslo HQ", "street": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "NO",
	}, http.StatusCreated, &place)
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"),
		"workEmail", "kari@example.test", "phoneWork", "+47 22 00 00 00", "phoneMobile", "+47 900 00 000",
		"preferredLanguage", "en", "workCountry", "NO"))
	createEmployment(t, hr, p.Id, newEmployment(day, terms(
		"title", "Engineer", "employeeType", "employee", "departmentId", dept.Id, "locationId", place.Id, "division", "Payments")))
	acc := getAccount(t, hr, p.Id)
	do(t, hr, http.MethodPut, accountPath(p.Id), map[string]any{"provisionAccount": false, "accountKind": "entra"},
		http.StatusOK, &acc, ifMatch(acc.Revision))

	rows := profileRows(t, acc)
	want := map[string]string{
		"givenName": "Kari", "familyName": "Nordmann", "displayName": "Kari Nordmann", "employeeId": p.EmployeeNumber,
		"userPrincipalName": "kari@example.test", "usageLocation": "NO", "employeeHireDate": day, "employeeLeaveDateTime": "",
		"employeeType": "employee", "jobTitle": "Engineer", "department": "Engineering", "companyName": "Kraft-Verket AS",
		"employeeOrgData": "CC-7 / Payments", "officeLocation": "Oslo HQ", "workAddress": "Storgata 1, 0155 Oslo, NO",
		"phones": "+47 22 00 00 00 / +47 900 00 000", "preferredLanguage": "en-GB", "manager": "Berit Sjef",
	}
	for key, value := range want {
		row := rows[key]
		got := ""
		if row.Value != nil {
			got = *row.Value
		}
		if got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	for i, key := range directoryOrder {
		row := rows[key]
		switch {
		case i < 6 && (row.Switchable || !row.On):
			t.Errorf("%s = %+v, want on and not switchable", key, row)
		case i >= 6 && !row.Switchable:
			t.Errorf("%s = %+v, want switchable", key, row)
		case (key == "workAddress" || key == "phones") && row.On:
			t.Errorf("%s is on, want off by default", key)
		case i >= 6 && key != "workAddress" && key != "phones" && !row.On:
			t.Errorf("%s is off, want on by default", key)
		}
	}

	// The person reads their own profile (plan reading 9); a colleague reads
	// no account at all.
	self, selfID := signIn(t, h)
	link(t, h, p.Id, selfID)
	if own := getAccount(t, self, p.Id); own.DirectoryProfile == nil {
		t.Errorf("the person's own account carries no directory profile")
	}

	// A switch turned on under Settings shows on the profile.
	settings := getSettings(t, hr)
	do(t, hr, http.MethodPut, settingsPath, settingsBody(map[string]any{"directoryFields": map[string]bool{"phones": true}}),
		http.StatusOK, nil, ifMatch(settings.Revision))
	if rows := profileRows(t, getAccount(t, hr, p.Id)); !rows["phones"].On || rows["workAddress"].On {
		t.Errorf("after the switch: phones %v, workAddress %v; want on, off", rows["phones"].On, rows["workAddress"].On)
	}
}

// A joiner prepared before their first day is profiled from the employment
// to come; a leaver carries the last end as the leave date and keeps the
// hire date.
func TestDirectoryProfile_TheDates(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithIdentityProvisioners(entraProvider()))
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	entra := func(id int64) gen.StaffPersonAccount {
		acc := getAccount(t, hr, id)
		do(t, hr, http.MethodPut, accountPath(id), map[string]any{"provisionAccount": false, "accountKind": "entra"},
			http.StatusOK, &acc, ifMatch(acc.Revision))
		return acc
	}
	value := func(rows map[string]gen.StaffDirectoryField, key string) string {
		if rows[key].Value == nil {
			return ""
		}
		return *rows[key].Value
	}

	joiner := createPerson(t, hr, newPerson("Ola", "Ny"))
	createEmployment(t, hr, joiner.Id, newEmployment("2099-03-01", terms("title", "Analyst")))
	rows := profileRows(t, entra(joiner.Id))
	if value(rows, "employeeHireDate") != "2099-03-01" || value(rows, "jobTitle") != "Analyst" {
		t.Errorf("a joiner: hire date %q, title %q; want 2099-03-01, Analyst", value(rows, "employeeHireDate"), value(rows, "jobTitle"))
	}

	leaver := createPerson(t, hr, newPerson("Per", "Slutt"))
	ended := createEmployment(t, hr, leaver.Id, newEmployment("2020-01-01", terms("title", "Clerk")))
	do(t, hr, http.MethodPost, endPath(ended.Id), map[string]any{"endDate": "2021-06-30", "endReason": "employee_resigned"}, http.StatusOK, nil)
	rows = profileRows(t, entra(leaver.Id))
	if value(rows, "employeeHireDate") != "2020-01-01" || value(rows, "employeeLeaveDateTime") != "2021-06-30" || value(rows, "jobTitle") != "Clerk" {
		t.Errorf("a leaver: hire %q, leave %q, title %q; want 2020-01-01, 2021-06-30, Clerk",
			value(rows, "employeeHireDate"), value(rows, "employeeLeaveDateTime"), value(rows, "jobTitle"))
	}
}
