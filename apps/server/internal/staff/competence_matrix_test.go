package staff_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const (
	matrixPath            = "/api/v1/staff/competence-matrix"
	interestsOverviewPath = "/api/v1/staff/competence-interests"
)

// getMatrix reads the matrix with query as c and fails the test unless it
// answered 200.
func getMatrix(t *testing.T, c *modtest.Client, query string) gen.StaffCompetenceMatrix {
	t.Helper()
	var out gen.StaffCompetenceMatrix
	do(t, c, http.MethodGet, matrixPath+query, nil, http.StatusOK, &out)
	return out
}

// getInterestsOverview reads the interests overview as c and fails the test
// unless it answered 200.
func getInterestsOverview(t *testing.T, c *modtest.Client) []gen.StaffCompetenceInterestsEntry {
	t.Helper()
	var out []gen.StaffCompetenceInterestsEntry
	do(t, c, http.MethodGet, interestsOverviewPath, nil, http.StatusOK, &out)
	return out
}

// matrixWorld is competenceWorldA's Kari and Bjørn with rows written
// directly, Ola active and holding nothing, a hire not yet started and an
// erased person, each holding a truck certificate. Today is 12 September
// 2026.
type matrixWorld struct {
	competenceWorldA
	hms, truck  int64
	ola         int64
	hire, gone  int64
	unpermitted *modtest.Client
}

func newMatrixWorld(t *testing.T) matrixWorld {
	t.Helper()
	w := matrixWorld{competenceWorldA: newCompetenceWorldA(t)}
	h := w.h
	w.hms, w.truck = competenceID(t, h, "hms-kort"), competenceID(t, h, "truck")
	two, four := int16(2), int16(4)
	// Kari: a declared golang at 2, an FSE that ended on 1 September, a
	// declared HMS-kort and a verified truck certificate.
	rawPersonCompetence(t, h, competenceRow{person: w.kari.Id, competence: w.golang, level: &two, from: "2026-01-01"})
	rawPersonCompetence(t, h, competenceRow{person: w.kari.Id, competence: w.fse, from: "2025-09-02", to: strPtr("2026-09-01"), verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.kari.Id, competence: w.hms, from: "2026-01-01", to: strPtr("2027-12-31")})
	rawPersonCompetence(t, h, competenceRow{person: w.kari.Id, competence: w.truck, from: "2020-01-01", verified: true})
	// Bjørn: golang at 4 and the truck, both verified.
	rawPersonCompetence(t, h, competenceRow{person: w.boss.Id, competence: w.golang, level: &four, from: "2026-01-01", verified: true})
	rawPersonCompetence(t, h, competenceRow{person: w.boss.Id, competence: w.truck, from: "2020-01-01", verified: true})
	w.ola = seedPerson(t, h, "Ola", "Nordmann")
	seedEmployment(t, h, w.ola, 0)
	w.hire = seedPerson(t, h, "Nina", "Ny")
	seedEmploymentDated(t, h, w.hire, 0, "2026-10-01", nil)
	rawPersonCompetence(t, h, competenceRow{person: w.hire, competence: w.truck, from: "2020-01-01", verified: true})
	w.gone = seedPerson(t, h, "Eva", "Glemt")
	seedEmployment(t, h, w.gone, 0)
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, w.gone, h.Now())
	rawPersonCompetence(t, h, competenceRow{person: w.gone, competence: w.truck, from: "2020-01-01", verified: true})
	w.unpermitted = w.kariC
	return w
}

// matrixPeople is the people's ids in the matrix's order.
func matrixPeople(m gen.StaffCompetenceMatrix) []int64 {
	out := []int64{}
	for _, p := range m.People {
		out = append(out, p.PersonId)
	}
	return out
}

// matrixCells is personID's cells in the matrix, by competence, nil when
// they are not listed.
func matrixCells(m gen.StaffCompetenceMatrix, personID int64) map[int64]gen.StaffCompetenceMatrixCell {
	for _, p := range m.People {
		if p.PersonId == personID {
			out := map[int64]gen.StaffCompetenceMatrixCell{}
			for _, c := range p.Competences {
				out[c.CompetenceId] = c
			}
			return out
		}
	}
	return nil
}

// matrixColumns is the columns' competence ids in order.
func matrixColumns(m gen.StaffCompetenceMatrix) []int64 {
	out := []int64{}
	for _, c := range m.Competences {
		out = append(out, c.Id)
	}
	return out
}

func idsQuery(name string, ids ...int64) string {
	q := ""
	for _, id := range ids {
		q += "&" + name + "=" + strconv.FormatInt(id, 10)
	}
	return q
}

// The matrix (staff competence design S14; plan reading 29): everyone active
// today and not erased, by name, with the rows valid on the day — today by
// default — of the competences selected; the filters.
func TestCompetenceMatrix_Filters(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	everyone := []int64{w.boss.Id, w.kari.Id, w.ola} // Bjørn, Kari, Ola by name

	all := getMatrix(t, w.viewer, "")
	if !slices.Equal(matrixPeople(all), everyone) {
		t.Errorf("people = %v, want Bjørn, Kari and Ola, by name; not the hire, not the erased", matrixPeople(all))
	}
	if all.ValidOn.String() != today(t, w.h) {
		t.Errorf("validOn = %s, want today", all.ValidOn)
	}
	kari := matrixCells(all, w.kari.Id)
	if len(kari) != 3 {
		t.Errorf("Kari's cells = %+v, want golang, HMS-kort and the truck; not the ended FSE", kari)
	}
	if c := kari[w.golang]; c.Verified || c.Level == nil || *c.Level != 2 || c.ValidFrom.String() != "2026-01-01" || c.ValidTo != nil {
		t.Errorf("Kari's golang = %+v, want declared at 2 from 1 January", c)
	}
	if c := kari[w.hms]; c.Verified || c.Level != nil || c.ValidTo == nil || c.ValidTo.String() != "2027-12-31" {
		t.Errorf("Kari's HMS-kort = %+v, want shown declared, with its end", c)
	}
	if c := matrixCells(all, w.ola); c == nil || len(c) != 0 {
		t.Errorf("Ola's cells = %+v, want listed with none", c)
	}
	// The columns are the competences shown, in the catalogue's order.
	if got := matrixColumns(all); !slices.Equal(got, []int64{w.golang, w.hms, w.truck}) {
		t.Errorf("columns = %v, want golang, HMS-kort and the truck in the catalogue's order", got)
	}

	before := getMatrix(t, w.viewer, "?validOn=2026-08-15")
	if _, ok := matrixCells(before, w.kari.Id)[w.fse]; !ok || before.ValidOn.String() != "2026-08-15" {
		t.Errorf("on 15 August Kari's cells = %+v, want the FSE then valid", matrixCells(before, w.kari.Id))
	}

	both := getMatrix(t, w.viewer, "?"+idsQuery("competenceId", w.truck, w.golang)[1:])
	if !slices.Equal(matrixPeople(both), []int64{w.boss.Id, w.kari.Id}) {
		t.Errorf("golang and the truck: people = %v, want Bjørn and Kari, who hold both", matrixPeople(both))
	}
	if got := matrixColumns(both); len(got) != 2 || !slices.Contains(got, w.golang) || !slices.Contains(got, w.truck) {
		t.Errorf("golang and the truck: columns = %v", got)
	}
	if c := matrixCells(both, w.kari.Id); len(c) != 2 {
		t.Errorf("golang and the truck: Kari's cells = %+v, want the two alone", c)
	}

	level3 := getMatrix(t, w.viewer, "?minLevel=3")
	if c := matrixCells(level3, w.kari.Id); len(c) != 2 || c[w.golang].CompetenceId != 0 {
		t.Errorf("minLevel 3: Kari's cells = %+v, want her golang at 2 dropped, the certificates kept", c)
	}
	if _, ok := matrixCells(level3, w.boss.Id)[w.golang]; !ok {
		t.Error("minLevel 3: Bjørn's golang at 4 is dropped")
	}
	if got := getMatrix(t, w.viewer, "?minLevel=3"+idsQuery("competenceId", w.golang)); !slices.Equal(matrixPeople(got), []int64{w.boss.Id}) {
		t.Errorf("golang at 3 or more: people = %v, want Bjørn alone", matrixPeople(got))
	}

	verified := getMatrix(t, w.viewer, "?verifiedOnly=true")
	if c := matrixCells(verified, w.kari.Id); len(c) != 1 || c[w.truck].CompetenceId != w.truck || !c[w.truck].Verified {
		t.Errorf("verifiedOnly: Kari's cells = %+v, want the truck alone", c)
	}

	fag := getMatrix(t, w.viewer, "?categoryId="+strconv.FormatInt(competenceCategoryID(t, w.h, "fag"), 10))
	if c := matrixCells(fag, w.kari.Id); len(c) != 1 || c[w.golang].CompetenceId != w.golang {
		t.Errorf("the trade category: Kari's cells = %+v, want golang alone", c)
	}
	if got := matrixColumns(fag); !slices.Equal(got, []int64{w.golang}) {
		t.Errorf("the trade category: columns = %v, want golang", got)
	}
	unknown := getMatrix(t, w.viewer, "?categoryId=9999999")
	if len(unknown.Competences) != 0 || !slices.Equal(matrixPeople(unknown), everyone) {
		t.Errorf("an unknown category = %+v, want no columns and everyone with no cells", unknown)
	}

	for _, tc := range []struct{ name, query, field string }{
		{"a level of 5", "?minLevel=5", "minLevel"},
		{"a level below 0", "?minLevel=-1", "minLevel"},
		{"an unknown competence", "?competenceId=9999999", "competenceId"},
	} {
		p := refused(t, w.viewer.Do(http.MethodGet, matrixPath+tc.query, nil), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	if r := w.viewer.Do(http.MethodGet, matrixPath+"?validOn=12.09.2026", nil); r.Status != http.StatusBadRequest {
		t.Errorf("a bad validOn = %d, want 400", r.Status)
	}
}

// The matrix is staff:view's and staff:manage's (reading 29): the Approver,
// the person and a caller with staff:access alone get the uniform 403.
func TestCompetenceMatrix_PerAudience(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	for name, c := range map[string]*modtest.Client{"staff:view": w.viewer, "staff:manage": w.hr} {
		if got := getMatrix(t, c, ""); len(got.People) != 3 {
			t.Errorf("%s reads %d people, want 3", name, len(got.People))
		}
	}
	for name, c := range map[string]*modtest.Client{
		"an Approver": w.approver, "nobody": w.nobody, "the person": w.unpermitted, "staff:personal-data": w.pd,
	} {
		if r := c.Do(http.MethodGet, matrixPath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s reads the matrix: %d, want 403", name, r.Status)
		}
	}
}

// The interests overview (reading 30): staff:manage's alone; every interest
// of a person active today and not erased, grouped by competence in the
// catalogue's order — only the competences somebody wants, archived ones
// included — under wantsMore when the person holds the competence and
// wantsToLearn otherwise, by name.
func TestCompetenceInterests_Overview(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	h := w.h
	kran := competenceID(t, h, "kran")
	interest := func(personID, competenceID int64) {
		t.Helper()
		h.Exec(t, `INSERT INTO staff.person_interests (person_id, competence_id, created_at) VALUES ($1, $2, $3)`,
			personID, competenceID, h.Now())
	}
	interest(w.kari.Id, w.golang) // held
	interest(w.kari.Id, kran)
	interest(w.boss.Id, kran)
	interest(w.hire, kran) // not active today
	interest(w.gone, kran) // erased
	leaver := seedPerson(t, h, "Per", "Sluttet")
	seedEmploymentDated(t, h, leaver, 0, "2024-01-01", dayPtr("2026-08-31"))
	interest(leaver, kran)
	h.Exec(t, `UPDATE staff.competences SET archived_at = $1 WHERE id = $2`, h.Now(), kran)

	got := getInterestsOverview(t, w.hr)
	if len(got) != 2 || got[0].Competence.Id != w.golang || got[1].Competence.Id != kran {
		t.Fatalf("overview = %+v, want golang then kran (the catalogue's order), nothing else", got)
	}
	ids := func(people []gen.StaffPersonSummary) []int64 {
		out := []int64{}
		for _, p := range people {
			out = append(out, p.Id)
		}
		return out
	}
	if k := got[1]; !k.Competence.Archived || !slices.Equal(ids(k.WantsToLearn), []int64{w.boss.Id, w.kari.Id}) || len(k.WantsMore) != 0 {
		t.Errorf("kran = %+v, want archived, Bjørn and Kari wanting to learn it", k)
	}
	if g := got[0]; !slices.Equal(ids(g.WantsMore), []int64{w.kari.Id}) || len(g.WantsToLearn) != 0 ||
		g.WantsMore[0].DisplayName != w.kari.DisplayName || g.WantsMore[0].EmployeeNumber != w.kari.EmployeeNumber {
		t.Errorf("golang = %+v, want Kari wanting more of it, named", g)
	}

	for name, c := range map[string]*modtest.Client{
		"an Approver": w.approver, "staff:view": w.viewer, "nobody": w.nobody, "staff:personal-data": w.pd, "a manager": w.bossC,
	} {
		if r := c.Do(http.MethodGet, interestsOverviewPath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s reads the overview: %d, want 403", name, r.Status)
		}
	}
}

// assertUniformForbidden fails the test unless r is the uniform 403, its body
// apicommon.ForbiddenBody() exactly.
func assertUniformForbidden(t *testing.T, r *modtest.Response, what string) {
	t.Helper()
	if r.Status != http.StatusForbidden {
		t.Errorf("%s: %d, want 403", what, r.Status)
		return
	}
	var body apicommon.AuthErrorResponse
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("%s: decode %q: %v", what, r.Body, err)
	}
	got, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(apicommon.ForbiddenBody())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s: body = %s, want the uniform %s", what, r.Body, want)
	}
}

// The Approver gets the uniform 403's body on the matrix and on the interests
// overview (readings 29 and 30), the same as for any read it may not make.
func TestCompetenceMatrixAndOverview_TheApproversUniform403(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	assertUniformForbidden(t, w.approver.Do(http.MethodGet, matrixPath, nil), "the Approver reads the matrix")
	assertUniformForbidden(t, w.approver.Do(http.MethodGet, interestsOverviewPath, nil), "the Approver reads the overview")
}

// categoryId and competenceId together intersect (reading 29): the columns
// are the ids given, and an id outside the category lists nobody.
func TestCompetenceMatrix_CategoryAndCompetenceTogether(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	fag := "?categoryId=" + strconv.FormatInt(competenceCategoryID(t, w.h, "fag"), 10)

	inside := getMatrix(t, w.viewer, fag+idsQuery("competenceId", w.golang))
	if got := matrixColumns(inside); !slices.Equal(got, []int64{w.golang}) {
		t.Errorf("the trade category and golang: columns = %v, want golang", got)
	}
	if !slices.Equal(matrixPeople(inside), []int64{w.boss.Id, w.kari.Id}) {
		t.Errorf("the trade category and golang: people = %v, want Bjørn and Kari", matrixPeople(inside))
	}

	outside := getMatrix(t, w.viewer, fag+idsQuery("competenceId", w.truck))
	if got := matrixColumns(outside); !slices.Equal(got, []int64{w.truck}) {
		t.Errorf("the trade category and the truck: columns = %v, want the truck, the id given", got)
	}
	if len(outside.People) != 0 {
		t.Errorf("the trade category and the truck: people = %v, want nobody — the truck is outside the category", matrixPeople(outside))
	}

	both := getMatrix(t, w.viewer, fag+idsQuery("competenceId", w.truck, w.golang))
	if got := matrixColumns(both); !slices.Equal(got, []int64{w.golang, w.truck}) {
		t.Errorf("the trade category, golang and the truck: columns = %v, want golang and the truck in the catalogue's order", got)
	}
	if len(both.People) != 0 {
		t.Errorf("the trade category, golang and the truck: people = %v, want nobody", matrixPeople(both))
	}
}

// An interest in a hidden competence is listed in the overview (reading 30):
// every existing interest is kept, hidden or archived.
func TestCompetenceInterests_OverviewListsAHiddenCompetence(t *testing.T) {
	t.Parallel()
	w := newMatrixWorld(t)
	h := w.h
	kran := competenceID(t, h, "kran")
	h.Exec(t, `INSERT INTO staff.person_interests (person_id, competence_id, created_at) VALUES ($1, $2, $3)`,
		w.kari.Id, kran, h.Now())
	h.Exec(t, `UPDATE staff.competences SET hidden = true WHERE id = $1`, kran)

	got := getInterestsOverview(t, w.hr)
	if len(got) != 1 || got[0].Competence.Id != kran || len(got[0].WantsToLearn) != 1 || got[0].WantsToLearn[0].Id != w.kari.Id {
		t.Errorf("overview = %+v, want the hidden kran with Kari wanting to learn it", got)
	}
}
