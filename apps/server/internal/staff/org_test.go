package staff_test

import (
	"net/http"
	"slices"
	"testing"

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
