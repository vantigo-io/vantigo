package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func personProfilesPath(personID int64) string { return personPath(personID) + "/competence-profiles" }

// profileIDs is an assignment's request body.
func profileIDs(ids ...int64) map[string]any {
	list := make([]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, id)
	}
	return map[string]any{"profileIds": list}
}

// putPersonProfiles assigns personID the profiles ids as c and fails the
// test unless it answered 200.
func putPersonProfiles(t *testing.T, c *modtest.Client, personID int64, ids ...int64) []gen.StaffPersonProfile {
	t.Helper()
	var out gen.StaffPersonProfileList
	do(t, c, http.MethodPut, personProfilesPath(personID), profileIDs(ids...), http.StatusOK, &out)
	return out.Profiles
}

// assignedKeys is the profiles' keys in order.
func assignedKeys(list []gen.StaffPersonProfile) []string {
	var out []string
	for _, p := range list {
		out = append(out, p.Profile.Key)
	}
	return out
}

// HR replaces the profiles assigned to a person; the answer is every profile
// that reaches them, by key, each assigned. A change of the set is
// profiles_assigned with the sorted keys before and after, which the manager
// and the person read; a replace that changes nothing records nothing.
// Anyone else who sees the person gets the uniform 403, a caller who does
// not the bare 404.
func TestPersonProfiles_HRAssigns(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	welder := addProfile(t, w.hr, profileBody("welder", profileItem(w.fse)))
	operator := addProfile(t, w.hr, profileBody("operator"))

	list := putPersonProfiles(t, w.hr, w.kari.Id, welder.Id, operator.Id)
	if !slices.Equal(assignedKeys(list), []string{"operator", "welder"}) || !list[0].Assigned || !list[1].Assigned ||
		list[0].Department != nil || list[1].Profile.Archived || list[1].Profile.Names.En != "Profile welder" {
		t.Errorf("assigned = %+v, want both by key, assigned", list)
	}
	for name, c := range map[string]*modtest.Client{"the manager": w.bossC, "the person": w.kariC, "HR": w.hr} {
		events := eventsOfKind(t, c, w.kari.Id, "profiles_assigned")
		if len(events) != 1 {
			t.Fatalf("%s reads %d profiles_assigned events, want one", name, len(events))
		}
		if c := change(t, events[0], "profiles"); c["from"] != nil || !slices.Equal(anyStrings(c["to"]), []string{"operator", "welder"}) {
			t.Errorf("%s reads the change %v, want to the two keys sorted", name, c)
		}
	}

	if list := putPersonProfiles(t, w.hr, w.kari.Id, operator.Id); !slices.Equal(assignedKeys(list), []string{"operator"}) {
		t.Errorf("after the removal = %+v, want operator alone", list)
	}
	putPersonProfiles(t, w.hr, w.kari.Id, operator.Id)
	events := eventsOfKind(t, w.hr, w.kari.Id, "profiles_assigned")
	if len(events) != 2 {
		t.Fatalf("%d profiles_assigned events, want two — the unchanged replace records nothing", len(events))
	}
	if c := change(t, events[0], "profiles"); !slices.Equal(anyStrings(c["from"]), []string{"operator", "welder"}) ||
		!slices.Equal(anyStrings(c["to"]), []string{"operator"}) {
		t.Errorf("the removal's change = %v, want from both to operator", c)
	}
	if list := putPersonProfiles(t, w.hr, w.kari.Id); len(list) != 0 {
		t.Errorf("after [] = %+v, want none", list)
	}
	if c := change(t, eventsOfKind(t, w.hr, w.kari.Id, "profiles_assigned")[0], "profiles"); c["to"] != nil {
		t.Errorf("the change to none = %v, want no to", c)
	}

	for name, tc := range map[string]struct {
		c    *modtest.Client
		want int
	}{
		"the manager":   {w.bossC, http.StatusForbidden},
		"the approver":  {w.approver, http.StatusForbidden},
		"the person":    {w.kariC, http.StatusForbidden},
		"personal-data": {w.pd, http.StatusForbidden},
		"a colleague":   {w.viewer, http.StatusNotFound},
		"a stranger":    {w.nobody, http.StatusNotFound},
	} {
		if r := tc.c.Do(http.MethodPut, personProfilesPath(w.kari.Id), profileIDs(operator.Id)); r.Status != tc.want {
			t.Errorf("%s's assignment = %d, want %d", name, r.Status, tc.want)
		}
	}
	if r := w.hr.Do(http.MethodPut, personProfilesPath(9999999), profileIDs(operator.Id)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person = %d, want 404", r.Status)
	}
}

// anyStrings is a JSON array of strings as a slice.
func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.(string))
	}
	return out
}

// An archived or unknown profile is a plain field error on its index
// (reading 26), as is one named twice; at most twenty; the list must be
// said; a profile already assigned and archived since is kept. An erased
// person's list is person_erased.
func TestPersonProfiles_Refusals(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	op := addProfile(t, w.hr, profileBody("operator"))
	old := addProfile(t, w.hr, profileBody("old"))
	changeProfile(t, w.hr, old, "archived", true)
	many := make([]int64, 21)
	for i := range many {
		many[i] = int64(i + 1)
	}
	for _, tc := range []struct {
		name, field string
		body        map[string]any
	}{
		{"an archived profile", "profileIds[0]", profileIDs(old.Id)},
		{"an unknown profile", "profileIds[1]", profileIDs(op.Id, 9999999)},
		{"a profile twice", "profileIds[1]", profileIDs(op.Id, op.Id)},
		{"twenty-one", "profileIds", profileIDs(many...)},
		{"no list", "profileIds", map[string]any{}},
		{"a property the list does not name", "note", with(profileIDs(op.Id), "note", "x")},
	} {
		p := refused(t, w.hr.Do(http.MethodPut, personProfilesPath(w.kari.Id), tc.body), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.person_profiles WHERE person_id = $1`, w.kari.Id); n != 0 {
		t.Errorf("%d profiles assigned, want none", n)
	}

	putPersonProfiles(t, w.hr, w.kari.Id, op.Id)
	h.Exec(t, `UPDATE staff.competence_profiles SET archived_at = $1 WHERE id = $2`, h.Now(), op.Id)
	if list := putPersonProfiles(t, w.hr, w.kari.Id, op.Id); len(list) != 1 || !list[0].Profile.Archived || !list[0].Assigned {
		t.Errorf("kept = %+v, want the archived assignment kept and listed archived", list)
	}

	h.Exec(t, `UPDATE staff.people SET erased_at = now() WHERE id = $1`, w.kari.Id)
	refused(t, w.hr.Do(http.MethodPut, personProfilesPath(w.kari.Id), profileIDs()), http.StatusConflict, "person_erased")
}

// The department the terms in force today of a person's place name brings
// its default profile (reading 25): listed with assigned false and the
// department named, and as one row with both when HR assigns it too. An
// archived default reaches nobody.
func TestPersonProfiles_TheDepartmentDefault(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	operator := addProfile(t, w.hr, profileBody("operator"))
	welder := addProfile(t, w.hr, profileBody("welder"))
	ops := createDepartment(t, w.hr, map[string]any{"code": "OPS", "name": "Operations"})
	do(t, w.hr, http.MethodPut, rowPath(departmentsPath, ops.Id),
		map[string]any{"code": "OPS", "name": "Operations", "competenceProfileId": operator.Id}, http.StatusOK, nil, ifMatch(ops.Revision))
	ola, _ := employed(t, w.hr, "Ola", "2020-01-01", terms("departmentId", ops.Id))

	list := putPersonProfiles(t, w.hr, ola.Id, welder.Id)
	if !slices.Equal(assignedKeys(list), []string{"operator", "welder"}) || list[0].Assigned || list[0].Department == nil ||
		list[0].Department.Code != "OPS" || list[0].Department.Id != ops.Id || !list[1].Assigned || list[1].Department != nil {
		t.Errorf("profiles = %+v, want operator by the department, welder assigned", list)
	}
	list = putPersonProfiles(t, w.hr, ola.Id, welder.Id, operator.Id)
	if !slices.Equal(assignedKeys(list), []string{"operator", "welder"}) || !list[0].Assigned || list[0].Department == nil {
		t.Errorf("profiles = %+v, want operator once, assigned and by the department", list)
	}

	putPersonProfiles(t, w.hr, ola.Id)
	h.Exec(t, `UPDATE staff.competence_profiles SET archived_at = $1 WHERE id = $2`, h.Now(), operator.Id)
	if list := putPersonProfiles(t, w.hr, ola.Id); len(list) != 0 {
		t.Errorf("profiles = %+v, want none: an archived default reaches nobody", list)
	}
}

// The D erase deletes the profiles assigned to a person with the rest of
// class D (reading 34).
func TestPersonProfiles_TheEraseRemovesThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	op := addProfile(t, hr, profileBody("operator"))
	gone := createPerson(t, hr, newPerson("Erik", "Glemt"))
	pastEmployment(t, hr, gone.Id, "2022-01-01", "2024-12-31", terms())
	putPersonProfiles(t, hr, gone.Id, op.Id)
	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if n := h.Count(t, `SELECT count(*) FROM staff.person_profiles WHERE person_id = $1`, gone.Id); n != 0 {
		t.Errorf("%d profiles left after the erase, want none", n)
	}
}
