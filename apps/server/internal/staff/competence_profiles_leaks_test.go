package staff_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// sortedKeys is the keys of m, sorted.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Who reads what the profiles ask (staff competence design §5's Profiles
// and gaps row; plan readings 29, 30, 32 and 33): the person, their
// manager, the Approver, HR and staff:personal-data read the profiles that
// reach the person and the gap list whole; a colleague and a caller who does
// not see the person get the bare 404 on both. The person's own export and
// the complete one carry the assignments. The matrix shows a colleague a
// person's id, name and the five keys of each cell, nothing of the detail
// group; the interests overview shows HR names and ids only.
func TestCompetenceProfiles_NoLeaksPerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	h := w.h
	electrician := rawProfile(t, h, "electrician", false, gapItemFor{competence: w.fse, mustBeValid: true})
	operator := rawProfile(t, h, "operator", false, gapItemFor{competence: w.golang, minLevel: int16Ptr(2), mustBeValid: true})
	rawAssign(t, h, w.kari.Id, electrician)
	rawAssign(t, h, w.kari.Id, operator)
	rawDepartmentDefault(t, h, "OPS", w.kari.Id, operator)
	declare(t, w.bossC, w.kari.Id, map[string]any{"competenceId": w.golang, "level": 3})
	declare(t, w.bossC, w.kari.Id, map[string]any{"competenceId": w.fse, "validTo": "2027-09-01", "issuer": "Kurs AS", "reference": "FSE-1"})

	gapItemKeys := []string{"archived", "competence", "competenceId", "declared", "level", "minLevel", "mustBeValid", "status", "validTo", "verified"}
	for name, c := range map[string]*modtest.Client{
		"Kari": w.kariC, "her manager": w.bossC, "an Approver": w.approver, "HR": w.hr, "staff:personal-data": w.pd,
	} {
		r := c.Do(http.MethodGet, personProfileListPath(w.kari.Id), nil)
		if r.Status != http.StatusOK {
			t.Errorf("%s reads Kari's profiles: %d, want 200", name, r.Status)
			continue
		}
		profiles, _ := raw(t, r)["profiles"].([]any)
		if len(profiles) != 2 {
			t.Errorf("%s reads profiles %v, want electrician and operator", name, profiles)
			continue
		}
		if got := sortedKeys(profiles[1].(map[string]any)); !slices.Equal(got, []string{"assigned", "department", "profile"}) {
			t.Errorf("%s reads the operator profile's keys %v", name, got)
		}

		r = c.Do(http.MethodGet, competenceGapsPath(w.kari.Id), nil)
		if r.Status != http.StatusOK {
			t.Errorf("%s reads Kari's gaps: %d, want 200", name, r.Status)
			continue
		}
		gaps := raw(t, r)
		list, _ := gaps["profiles"].([]any)
		if _, ok := gaps["notMet"]; !ok || len(list) != 2 {
			t.Errorf("%s reads gaps %v", name, gaps)
			continue
		}
		operatorGaps := list[1].(map[string]any)
		if got := sortedKeys(operatorGaps); !slices.Equal(got, []string{"assigned", "department", "items", "notMet", "profile"}) {
			t.Errorf("%s reads the operator gap profile's keys %v", name, got)
		}
		items, _ := operatorGaps["items"].([]any)
		if len(items) != 1 {
			t.Errorf("%s reads items %v", name, items)
			continue
		}
		if got := sortedKeys(items[0].(map[string]any)); !slices.Equal(got, slices.DeleteFunc(slices.Clone(gapItemKeys), func(k string) bool { return k == "validTo" })) {
			t.Errorf("%s reads the golang item's keys %v", name, got)
		}
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "a stranger": w.nobody} {
		for _, path := range []string{personProfileListPath(w.kari.Id), competenceGapsPath(w.kari.Id)} {
			if r := c.Do(http.MethodGet, path, nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
				t.Errorf("%s reads %s: %d %s, want the bare 404", name, path, r.Status, r.Body)
			}
		}
	}

	// Both exports carry the assignments: the person's own and the
	// complete one.
	if got := exportOf(t, w.kariC.Do(http.MethodGet, meExportPath, nil)).ProfileAssignments; len(got) != 2 {
		t.Errorf("Kari's own export carries %+v, want her two profiles", got)
	}
	if got := exportOf(t, w.pd.Do(http.MethodGet, personalDataPath(w.kari.Id), nil)).ProfileAssignments; len(got) != 2 {
		t.Errorf("the complete export carries %+v, want her two profiles", got)
	}

	// The matrix: a person's id and name, each cell's five keys.
	r := w.viewer.Do(http.MethodGet, matrixPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("the colleague's matrix: %d", r.Status)
	}
	people, _ := raw(t, r)["people"].([]any)
	for _, p := range people {
		person := p.(map[string]any)
		if got := sortedKeys(person); !slices.Equal(got, []string{"competences", "name", "personId"}) {
			t.Errorf("a matrix row's keys = %v", got)
		}
		for _, c := range person["competences"].([]any) {
			for _, k := range sortedKeys(c.(map[string]any)) {
				if !slices.Contains([]string{"competenceId", "level", "validFrom", "validTo", "verified"}, k) {
					t.Errorf("a matrix cell carries %s: %v", k, c)
				}
			}
		}
	}
	for _, leak := range []string{"Kurs AS", "FSE-1", "issuer", "reference", "verifiedBy", "verifiedAt", "seenAt"} {
		if strings.Contains(string(r.Body), leak) {
			t.Errorf("the matrix carries %q: %s", leak, r.Body)
		}
	}

	// The overview: a competence, and people as id, name and number.
	putInterests(t, w.kariC, w.kari.Id, interestsOf(competenceID(t, h, "kran")))
	r = w.hr.Do(http.MethodGet, interestsOverviewPath, nil)
	var entries []map[string]any
	r.JSON(&entries)
	if len(entries) != 1 {
		t.Fatalf("the overview = %s, want kran", r.Body)
	}
	if got := sortedKeys(entries[0]); !slices.Equal(got, []string{"competence", "wantsMore", "wantsToLearn"}) {
		t.Errorf("an overview entry's keys = %v", got)
	}
	for _, p := range entries[0]["wantsToLearn"].([]any) {
		if got := sortedKeys(p.(map[string]any)); !slices.Equal(got, []string{"displayName", "employeeNumber", "id"}) {
			t.Errorf("an overview person's keys = %v", got)
		}
	}
}
