package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func names(people []gen.StaffPersonSummary) []string {
	var out []string
	for _, p := range people {
		out = append(out, p.DisplayName)
	}
	slices.Sort(out)
	return out
}

// The org view walks the chain up — the terms' manager, else the
// department's, a department's head climbing to the parent's — and lists
// the direct reports, by the terms in force today: the head of a department
// reports to the parent department's head and is not their own report.
func TestOrg_UpAndDown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	start := shiftDay(t, today(t, h), -30)
	ceo := createPerson(t, hr, newPerson("Cato", "Sjef"))
	head := createPerson(t, hr, newPerson("Hilde", "Leder"))
	top := createDepartment(t, hr, map[string]any{"code": "TOP", "name": "Top", "managerPersonId": ceo.Id})
	dev := createDepartment(t, hr, map[string]any{"code": "DEV", "name": "Development", "managerPersonId": head.Id, "parentId": top.Id})
	createEmployment(t, hr, ceo.Id, newEmployment(start, terms("departmentId", top.Id)))
	createEmployment(t, hr, head.Id, newEmployment(start, terms("departmentId", dev.Id)))
	anne, _ := employed(t, hr, "Anne", start, terms("departmentId", dev.Id))
	employed(t, hr, "Bjørn", start, terms("departmentId", dev.Id))
	employed(t, hr, "Cecilie", start, terms("departmentId", dev.Id, "managerPersonId", anne.Id))
	// A report whose terms naming Anne take effect only later is not yet one.
	employed(t, hr, "Dag", start, terms("departmentId", dev.Id))
	_, later := employed(t, hr, "Eva", start, terms())
	addTerms(t, hr, later.Id, terms("validFrom", shiftDay(t, today(t, h), 10), "managerPersonId", anne.Id))

	viewer, _ := signIn(t, h, "staff:view")
	for _, c := range []struct {
		person         int64
		chain, reports []string
	}{
		{anne.Id, []string{"Cato Sjef", "Hilde Leder"}, []string{"Cecilie Nordmann"}},
		{head.Id, []string{"Cato Sjef"}, []string{"Anne Nordmann", "Bjørn Nordmann", "Dag Nordmann"}},
		{ceo.Id, nil, []string{"Hilde Leder"}},
	} {
		var org gen.StaffOrg
		do(t, viewer, http.MethodGet, orgPath(c.person), nil, http.StatusOK, &org)
		var chain []string
		for _, p := range org.Chain {
			chain = append(chain, p.DisplayName)
		}
		slices.Sort(chain)
		if org.Person.Id != c.person || !slices.Equal(chain, c.chain) || !slices.Equal(names(org.Reports), c.reports) {
			t.Errorf("org of %d: chain %v reports %v, want %v and %v", c.person, chain, names(org.Reports), c.chain, c.reports)
		}
	}
	var org gen.StaffOrg
	do(t, hr, http.MethodGet, orgPath(anne.Id), nil, http.StatusOK, &org)
	if len(org.Chain) != 2 || org.Chain[0].DisplayName != "Hilde Leder" {
		t.Errorf("chain %v, want Hilde first, then Cato", org.Chain)
	}
	outsider, _ := signIn(t, h)
	if r := outsider.Do(http.MethodGet, orgPath(anne.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("outsider: %d %q, want the bare 404", r.Status, r.Body)
	}
}

// One manager rule for the Manager audience, the org view and the person's
// shown manager (design §9, "a manager who leaves: their reports route to
// the department's manager"): the manager the terms name when they are in
// employment today, else the first department from the terms' up whose
// manager is someone else in employment today, else nobody. A manager who
// has left reads none of their former reports and lists none.
func TestOrg_DepartedManagerRoutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	perClient, perUser := signIn(t, h)
	olaClient, olaUser := signIn(t, h)
	carolClient, carolUser := signIn(t, h)
	aliceClient, aliceUser := signIn(t, h)
	left := day(t, h, -10)

	per := seedPerson(t, h, "Per", "Head")
	ola := seedPerson(t, h, "Ola", "Left")
	carol := seedPerson(t, h, "Carol", "Above")
	alice := seedPerson(t, h, "Alice", "Gone")
	dept := seedDepartment(t, h, "DEPT", per, 0)
	top := seedDepartment(t, h, "TOP", carol, 0)
	sub := seedDepartment(t, h, "SUB", alice, top)
	alone := seedDepartment(t, h, "ALONE", 0, 0)
	seedWork(t, h, per, work{start: "2020-01-01", department: dept, signed: "2020-01-01"})
	seedWork(t, h, ola, work{start: "2020-01-01", end: left, department: dept, signed: "2020-01-01"})
	seedWork(t, h, carol, work{start: "2020-01-01", department: top, signed: "2020-01-01"})
	seedWork(t, h, alice, work{start: "2020-01-01", end: left, department: sub, signed: "2020-01-01"})
	link(t, h, per, perUser)
	link(t, h, ola, olaUser)
	link(t, h, carol, carolUser)
	link(t, h, alice, aliceUser)

	// Kari's terms name Ola, who has left: Per, her department's head,
	// manages her. Bob's department's manager has left: the parent's does.
	// Dan names Alice, who has left, and nobody manages his department.
	kari := seedPerson(t, h, "Kari", "Named")
	seedWork(t, h, kari, work{start: "2024-01-01", manager: ola, department: dept, signed: "2024-01-01"})
	bob := seedPerson(t, h, "Bob", "Bydepartment")
	seedWork(t, h, bob, work{start: "2024-01-01", department: sub, signed: "2024-01-01"})
	dan := seedPerson(t, h, "Dan", "Nobody")
	seedWork(t, h, dan, work{start: "2024-01-01", manager: alice, department: alone, signed: "2024-01-01"})

	for _, c := range []struct {
		name   string
		client *modtest.Client
		person int64
		want   int
	}{
		{"the department's head reads the person whose named manager left", perClient, kari, http.StatusOK},
		{"the named manager who left", olaClient, kari, http.StatusNotFound},
		{"the parent department's manager reads the person", carolClient, bob, http.StatusOK},
		{"the department's manager who left", aliceClient, bob, http.StatusNotFound},
		{"the named manager who left, with nobody above", aliceClient, dan, http.StatusNotFound},
	} {
		for _, path := range []string{personPath(c.person), eventsPath(c.person), orgPath(c.person)} {
			if r := c.client.Do(http.MethodGet, path, nil); r.Status != c.want {
				t.Errorf("%s: GET %s %d, want %d", c.name, path, r.Status, c.want)
			}
		}
	}

	for _, c := range []struct {
		name    string
		client  *modtest.Client
		person  int64
		reports []string
	}{
		{"Per", perClient, per, []string{"Kari Named"}},
		{"Ola", olaClient, ola, nil},
		{"Carol", carolClient, carol, []string{"Bob Bydepartment"}},
		{"Alice", aliceClient, alice, nil},
	} {
		var org gen.StaffOrg
		do(t, c.client, http.MethodGet, orgPath(c.person), nil, http.StatusOK, &org)
		if got := names(org.Reports); !slices.Equal(got, c.reports) {
			t.Errorf("%s's reports = %v, want %v", c.name, got, c.reports)
		}
	}

	for _, c := range []struct {
		person  int64
		manager int64 // 0: none
	}{{kari, per}, {bob, carol}, {dan, 0}} {
		person := raw(t, hr.Do(http.MethodGet, personPath(c.person), nil))
		m, shownOne := person["manager"].(map[string]any)
		switch {
		case c.manager == 0 && shownOne:
			t.Errorf("person %d: manager %v, want none", c.person, m)
		case c.manager != 0 && (!shownOne || m["id"] != float64(c.manager)):
			t.Errorf("person %d: manager %v, want %d", c.person, person["manager"], c.manager)
		}
		var org gen.StaffOrg
		do(t, hr, http.MethodGet, orgPath(c.person), nil, http.StatusOK, &org)
		var chain []int64
		for _, p := range org.Chain {
			chain = append(chain, p.Id)
		}
		if want := []int64{c.manager}; (c.manager == 0 && len(chain) != 0) || (c.manager != 0 && !slices.Equal(chain, want)) {
			t.Errorf("person %d: chain %v, want %v", c.person, chain, c.manager)
		}
	}
}
