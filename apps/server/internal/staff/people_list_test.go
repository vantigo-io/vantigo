package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// numbersListed is the employee numbers a list answers, sorted.
func numbersListed(t *testing.T, c *modtest.Client, query string) []string {
	t.Helper()
	var out []string
	for _, p := range listPeople(t, c, query).Data {
		out = append(out, p.EmployeeNumber)
	}
	slices.Sort(out)
	return out
}

// staff:manage lists by where people stand today — current by default
// (active, upcoming and no employment yet), ended, or all — and every row
// carries its status with the date that matters; staff:view lists the
// people active today whatever the filter says.
func TestPeopleList_StatusPerAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	day := today(t, h)
	tomorrow, yesterday := shiftDay(t, day, 1), shiftDay(t, day, -1)

	active := createPerson(t, hr, with(newPerson("Active", "Today"), "employeeNumber", "S-ACT"))
	createEmployment(t, hr, active.Id, newEmployment(shiftDay(t, day, -10), terms()))
	upcoming := createPerson(t, hr, with(newPerson("Starts", "Tomorrow"), "employeeNumber", "S-UP"))
	createEmployment(t, hr, upcoming.Id, newEmployment(tomorrow, terms()))
	ended := createPerson(t, hr, with(newPerson("Ended", "Yesterday"), "employeeNumber", "S-END"))
	e := createEmployment(t, hr, ended.Id, newEmployment(shiftDay(t, day, -100), terms()))
	do(t, hr, http.MethodPost, endPath(e.Id), map[string]any{"endDate": yesterday, "endReason": "employee_resigned"}, http.StatusOK, nil)
	createPerson(t, hr, with(newPerson("No", "Employment"), "employeeNumber", "S-NONE"))

	for query, want := range map[string][]string{
		"":                {"S-ACT", "S-NONE", "S-UP"},
		"?status=current": {"S-ACT", "S-NONE", "S-UP"},
		"?status=ended":   {"S-END"},
		"?status=all":     {"S-ACT", "S-END", "S-NONE", "S-UP"},
	} {
		if got := numbersListed(t, hr, query); !slices.Equal(got, want) {
			t.Errorf("staff:manage %q lists %v, want %v", query, got, want)
		}
	}
	statuses := map[string]string{}
	for _, p := range listPeople(t, hr, "?status=all").Data {
		s := string(p.Status.State)
		if p.Status.Date != nil {
			s += " " + p.Status.Date.Format("2006-01-02")
		}
		statuses[p.EmployeeNumber] = s
	}
	for number, want := range map[string]string{"S-ACT": "active", "S-UP": "upcoming " + tomorrow, "S-END": "ended " + yesterday, "S-NONE": "none"} {
		if statuses[number] != want {
			t.Errorf("%s status = %q, want %q", number, statuses[number], want)
		}
	}
	var person gen.StaffPerson
	do(t, hr, http.MethodGet, personPath(upcoming.Id), nil, http.StatusOK, &person)
	if person.Status.State != "upcoming" {
		t.Errorf("GET person status = %+v, want upcoming", person.Status)
	}

	colleague, _ := signIn(t, h, "staff:view")
	for _, query := range []string{"", "?status=all", "?status=ended"} {
		want := []string{"S-ACT"}
		if query == "?status=ended" {
			want = nil
		}
		if got := numbersListed(t, colleague, query); !slices.Equal(got, want) {
			t.Errorf("staff:view %q lists %v, want %v", query, got, want)
		}
	}
	refused(t, hr.Do(http.MethodGet, peoplePath+"?status=gone", nil), http.StatusBadRequest, "")
}

// The structure filters keep the people with an employment in force today in
// the business unit, or whose terms in force today name the department or the
// location — all on the same employment; each row names the manager and the
// department from the terms in force today, the manager the department's
// when the terms name none.
func TestPeopleList_FiltersAndSummaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	bergen := createBusinessUnit(t, hr, map[string]any{"legalEntityId": unit.LegalEntityId, "name": "Bergen"})
	start := shiftDay(t, today(t, h), -30)
	boss := createPerson(t, hr, with(newPerson("Berit", "Boss"), "employeeNumber", "F-BOSS"))
	createEmployment(t, hr, boss.Id, newEmployment(start, terms()))
	sales := createDepartment(t, hr, map[string]any{"code": "SALES", "name": "Sales", "managerPersonId": boss.Id})
	dev := createDepartment(t, hr, map[string]any{"code": "DEV", "name": "Development"})
	oslo := createLocation(t, hr, "Oslo office")

	seller := createPerson(t, hr, with(newPerson("Selma", "Seller"), "employeeNumber", "F-SELL"))
	createEmployment(t, hr, seller.Id, newEmployment(start, terms("departmentId", sales.Id, "locationId", oslo.Id)))
	coder := createPerson(t, hr, with(newPerson("Kari", "Coder"), "employeeNumber", "F-CODE"))
	createEmployment(t, hr, coder.Id, newEmployment(start, terms("departmentId", dev.Id, "managerPersonId", boss.Id), "businessUnitId", bergen.Id))
	// Moving to Sales only next month: not in Sales today.
	mover := createPerson(t, hr, with(newPerson("Mona", "Mover"), "employeeNumber", "F-MOVE"))
	m := createEmployment(t, hr, mover.Id, newEmployment(start, terms("departmentId", dev.Id)))
	addTerms(t, hr, m.Id, terms("validFrom", shiftDay(t, today(t, h), 20), "departmentId", sales.Id))

	for query, want := range map[string][]string{
		fmt.Sprintf("?departmentId=%d", sales.Id):                      {"F-SELL"},
		fmt.Sprintf("?departmentId=%d", dev.Id):                        {"F-CODE", "F-MOVE"},
		fmt.Sprintf("?unitId=%d", bergen.Id):                           {"F-CODE"},
		fmt.Sprintf("?locationId=%d", oslo.Id):                         {"F-SELL"},
		fmt.Sprintf("?unitId=%d&departmentId=%d", bergen.Id, sales.Id): nil,
	} {
		if got := numbersListed(t, hr, query); !slices.Equal(got, want) {
			t.Errorf("%s lists %v, want %v", query, got, want)
		}
	}
	rows := map[string]gen.StaffPersonListItem{}
	for _, p := range listPeople(t, hr, "").Data {
		rows[p.EmployeeNumber] = p
	}
	if r := rows["F-SELL"]; r.Manager == nil || r.Manager.Id != boss.Id || r.Department == nil || r.Department.Code != "SALES" {
		t.Errorf("seller = manager %+v department %+v, want the department's head and Sales", r.Manager, r.Department)
	}
	if r := rows["F-CODE"]; r.Manager == nil || r.Manager.DisplayName != "Berit Boss" || r.Department == nil || r.Department.Id != dev.Id {
		t.Errorf("coder = manager %+v department %+v, want Berit by the terms and Development", r.Manager, r.Department)
	}
	if r := rows["F-BOSS"]; r.Manager != nil || r.Department != nil {
		t.Errorf("boss = manager %+v department %+v, want neither", r.Manager, r.Department)
	}
	var person gen.StaffPerson
	do(t, hr, http.MethodGet, personPath(seller.Id), nil, http.StatusOK, &person)
	if person.Manager == nil || person.Manager.Id != boss.Id || person.Department == nil || person.Department.Name != "Sales" {
		t.Errorf("GET seller = manager %+v department %+v, want Berit and Sales", person.Manager, person.Department)
	}
}

// Under the current status the structure filters also find an upcoming hire
// by their first terms row — HR looks for next month's new starter in their
// department — while a person already active is still judged on the row in
// force today.
func TestPeopleList_FiltersFindAnUpcomingHire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	unit := company(t, hr)
	sales := createDepartment(t, hr, map[string]any{"code": "SALES", "name": "Sales"})
	oslo := createLocation(t, hr, "Oslo office")
	hire := createPerson(t, hr, with(newPerson("Nina", "Ny"), "employeeNumber", "U-NEW"))
	createEmployment(t, hr, hire.Id, newEmployment(shiftDay(t, today(t, h), 20), terms("departmentId", sales.Id, "locationId", oslo.Id)))

	for _, query := range []string{
		fmt.Sprintf("?departmentId=%d", sales.Id),
		fmt.Sprintf("?locationId=%d&unitId=%d", oslo.Id, unit.Id),
		fmt.Sprintf("?departmentId=%d&status=all", sales.Id),
	} {
		if got := numbersListed(t, hr, query); !slices.Equal(got, []string{"U-NEW"}) {
			t.Errorf("%s lists %v, want the upcoming hire", query, got)
		}
	}
	colleague, _ := signIn(t, h, "staff:view")
	if got := numbersListed(t, colleague, fmt.Sprintf("?departmentId=%d", sales.Id)); len(got) != 0 {
		t.Errorf("the colleague lists %v, want nobody: the hire is not active today", got)
	}
}

// A colleague, who sees only the people active today, is shown a manager —
// on the list, on the person and up the org chain — only while that manager
// is active today too; HR sees the manager whoever they are.
func TestPeopleList_AColleagueSeesOnlyAnActiveManager(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	start := shiftDay(t, today(t, h), -60)
	gone := createPerson(t, hr, newPerson("Gunnar", "Gone"))
	g := createEmployment(t, hr, gone.Id, newEmployment(start, terms()))
	do(t, hr, http.MethodPost, endPath(g.Id), map[string]any{"endDate": shiftDay(t, today(t, h), -1), "endReason": "employee_resigned"}, http.StatusOK, nil)
	report := createPerson(t, hr, with(newPerson("Rita", "Report"), "employeeNumber", "M-REP"))
	createEmployment(t, hr, report.Id, newEmployment(start, terms("managerPersonId", gone.Id)))

	colleague, _ := signIn(t, h, "staff:view")
	for name, c := range map[string]struct {
		client *modtest.Client
		want   bool
	}{"colleague": {colleague, false}, "staff:manage": {hr, true}} {
		row := listPeople(t, c.client, "?q=Rita").Data[0]
		var person gen.StaffPerson
		do(t, c.client, http.MethodGet, personPath(report.Id), nil, http.StatusOK, &person)
		var org gen.StaffOrg
		do(t, c.client, http.MethodGet, orgPath(report.Id), nil, http.StatusOK, &org)
		if (row.Manager != nil) != c.want || (person.Manager != nil) != c.want || (len(org.Chain) == 1) != c.want {
			t.Errorf("%s: list %+v person %+v chain %+v, want the manager shown %v", name, row.Manager, person.Manager, org.Chain, c.want)
		}
	}
}
