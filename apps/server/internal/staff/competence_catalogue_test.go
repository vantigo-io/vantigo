package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The catalogue's paths.
const (
	competenceCategoriesPath = "/api/v1/staff/competence-categories"
	competencesPath          = "/api/v1/staff/competences"
)

func competenceCategoryPath(id int64) string { return rowPath(competenceCategoriesPath, id) }
func competencePath(id int64) string         { return rowPath(competencesPath, id) }

// competenceID is the id of the competence keyed key.
func competenceID(t *testing.T, h *harness, key string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM staff.competences WHERE key = $1`, key)
}

// competenceCategoryID is the id of the competence category keyed key.
func competenceCategoryID(t *testing.T, h *harness, key string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM staff.competence_categories WHERE key = $1`, key)
}

// listCompetences reads the catalogue as c with the given query and fails
// the test unless it answered 200.
func listCompetences(t *testing.T, c *modtest.Client, query string) []gen.StaffCompetence {
	t.Helper()
	var out []gen.StaffCompetence
	do(t, c, http.MethodGet, competencesPath+query, nil, http.StatusOK, &out)
	return out
}

// competenceKeys is the competences' keys in order.
func competenceKeys(list []gen.StaffCompetence) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Key)
	}
	return out
}

// addCompetence adds a company competence as HR and fails the test unless it
// was created.
func addCompetence(t *testing.T, hr *modtest.Client, body map[string]any) gen.StaffCompetence {
	t.Helper()
	var out gen.StaffCompetence
	do(t, hr, http.MethodPost, competencesPath, body, http.StatusCreated, &out)
	return out
}

// skillBody is a company skill with a level, filed under the fag category.
func skillBody(t *testing.T, h *harness, key string, kv ...any) map[string]any {
	t.Helper()
	return with(map[string]any{
		"categoryId": competenceCategoryID(t, h, "fag"), "key": key,
		"names": map[string]any{"en": "Skill " + key, "nb": "Ferdighet " + key}, "kind": "skill", "hasLevel": true,
	}, kv...)
}

// competenceChangeBody is c as a whole change request, with the given fields replaced.
func competenceChangeBody(c gen.StaffCompetence, kv ...any) map[string]any {
	body := map[string]any{
		"categoryId": c.CategoryId, "names": map[string]any{"en": c.Names.En, "nb": c.Names.Nb},
		"hasLevel": c.HasLevel, "hidden": c.Hidden, "archived": c.ArchivedAt != nil,
	}
	if c.ValidityMonths != nil {
		body["validityMonths"] = *c.ValidityMonths
	}
	if c.ReminderLeadDays != nil {
		body["reminderLeadDays"] = *c.ReminderLeadDays
	}
	if c.IssuerHint != nil {
		body["issuerHint"] = *c.IssuerHint
	}
	return with(body, kv...)
}

// The five seeded categories in their order, read by every caller — a
// person with staff:access alone among them, who declares from My page.
func TestCompetenceCategories_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, perms := range [][]string{nil, {"staff:view"}, {"staff:manage"}} {
		c, _ := signIn(t, h, perms...)
		var list []gen.StaffCompetenceCategory
		do(t, c, http.MethodGet, competenceCategoriesPath, nil, http.StatusOK, &list)
		var keys []string
		for _, k := range list {
			keys = append(keys, k.Key)
			if !k.Seeded || k.ArchivedAt != nil {
				t.Errorf("%v: seed %s = %+v, want seeded and not archived", perms, k.Key, k)
			}
		}
		if !slices.Equal(keys, []string{"fag", "sertifikat", "sprak", "verktoy", "domene"}) {
			t.Errorf("%v: categories = %v, want the five seeds in order", perms, keys)
		}
	}
}

// A company category is added after the seeds by staff:manage, renamed and
// archived; a seed never changes; a category a competence in use is filed
// under is not archived; a stale revision is a conflict.
func TestCompetenceCategories_AddAndChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	names := map[string]any{"en": "Welding", "nb": "Sveising"}

	var added gen.StaffCompetenceCategory
	do(t, hr, http.MethodPost, competenceCategoriesPath, map[string]any{"key": "sveis", "names": names}, http.StatusCreated, &added)
	if added.Key != "sveis" || added.Seeded || added.Sort != 6 || added.Revision != 1 {
		t.Errorf("added = %+v, want sveis after the seeds", added)
	}
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a key another category has", map[string]any{"key": "fag", "names": names}, "key"},
		{"a key with capitals", map[string]any{"key": "Sveis2", "names": names}, "key"},
		{"a key of 41 characters", map[string]any{"key": "a2345678901234567890123456789012345678901", "names": names}, "key"},
		{"no English name", map[string]any{"key": "x", "names": map[string]any{"en": " ", "nb": "X"}}, "names.en"},
	} {
		p := refused(t, hr.Do(http.MethodPost, competenceCategoriesPath, tc.body), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	viewer, _ := signIn(t, h, "staff:view")
	if r := viewer.Do(http.MethodPost, competenceCategoriesPath, map[string]any{"key": "y", "names": names}); r.Status != http.StatusForbidden {
		t.Errorf("a viewer's add = %d, want 403", r.Status)
	}

	seed := competenceCategoryID(t, h, "fag")
	refused(t, hr.Do(http.MethodPut, competenceCategoryPath(seed), map[string]any{"names": names, "archived": false}, ifMatch(1)),
		http.StatusConflict, "seed_immutable")

	skill := addCompetence(t, hr, skillBody(t, h, "mig", "categoryId", added.Id))
	refused(t, hr.Do(http.MethodPut, competenceCategoryPath(added.Id), map[string]any{"names": names, "archived": true}, ifMatch(added.Revision)),
		http.StatusConflict, "category_in_use")
	refused(t, hr.Do(http.MethodPut, competenceCategoryPath(added.Id), map[string]any{"names": names, "archived": false}, ifMatch(added.Revision+5)),
		http.StatusConflict, "revision_conflict")

	do(t, hr, http.MethodPut, competencePath(skill.Id), competenceChangeBody(skill, "archived", true), http.StatusOK, nil, ifMatch(skill.Revision))
	var archived gen.StaffCompetenceCategory
	do(t, hr, http.MethodPut, competenceCategoryPath(added.Id),
		map[string]any{"names": map[string]any{"en": "Welding work", "nb": "Sveisearbeid"}, "archived": true}, http.StatusOK, &archived, ifMatch(added.Revision))
	if archived.ArchivedAt == nil || archived.Names.En != "Welding work" || archived.Revision != added.Revision+1 {
		t.Errorf("archived = %+v, want archived, renamed, the revision moved on", archived)
	}
	if r := hr.Do(http.MethodPut, competenceCategoryPath(9999999), map[string]any{"names": names, "archived": false}, ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown category = %d, want 404", r.Status)
	}

	// A competence under an archived category is restored only once its
	// category is.
	var shelved gen.StaffCompetence
	for _, c := range listCompetences(t, hr, "?includeArchived=true") {
		if c.Id == skill.Id {
			shelved = c
		}
	}
	p := refused(t, hr.Do(http.MethodPut, competencePath(skill.Id), competenceChangeBody(shelved, "archived", false), ifMatch(shelved.Revision)),
		http.StatusBadRequest, "")
	if len(p.Errors["archived"]) == 0 {
		t.Errorf("a restore under an archived category: errors = %v, want one on archived", p.Errors)
	}
	do(t, hr, http.MethodPut, competenceCategoryPath(added.Id), map[string]any{"names": names, "archived": false}, http.StatusOK, nil, ifMatch(archived.Revision))
	var restored gen.StaffCompetence
	do(t, hr, http.MethodPut, competencePath(skill.Id), competenceChangeBody(shelved, "archived", false), http.StatusOK, &restored, ifMatch(shelved.Revision))
	if restored.ArchivedAt != nil {
		t.Errorf("restored = %+v, want it in the catalogue again", restored)
	}
}

// The fifteen seeded certificates are every caller's; a hidden or an
// archived competence is listed to staff:manage alone, and only when asked
// — a caller with staff:access alone sends the flags and never sees one.
func TestCompetences_ListPerCaller(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	access, _ := signIn(t, h)

	seeds := listCompetences(t, access, "")
	if len(seeds) != 15 || seeds[0].Key != "fagbrev" || seeds[14].Key != "arbeidsvarsling" {
		t.Fatalf("seeds = %v, want the fifteen certificates in order", competenceKeys(seeds))
	}
	for _, c := range seeds {
		if !c.Seeded || c.Kind != gen.StaffCompetenceKind("certificate") || c.HasLevel || c.Basis == nil {
			t.Errorf("seed %s = %+v, want a seeded certificate without a level, with its basis", c.Key, c)
		}
	}
	if fse := seeds[1]; fse.ValidityMonths == nil || *fse.ValidityMonths != 12 || fse.ReminderLeadDays == nil || *fse.ReminderLeadDays != 30 {
		t.Errorf("fse = %+v, want 12 months and a lead of 30 days", fse)
	}

	kran := seeds[4]
	do(t, hr, http.MethodPut, competencePath(kran.Id), competenceChangeBody(kran, "hidden", true), http.StatusOK, nil, ifMatch(kran.Revision))
	old := addCompetence(t, hr, skillBody(t, h, "cobol"))
	do(t, hr, http.MethodPut, competencePath(old.Id), competenceChangeBody(old, "archived", true), http.StatusOK, nil, ifMatch(old.Revision))

	for _, query := range []string{"", "?includeHidden=true&includeArchived=true"} {
		for name, c := range map[string]*modtest.Client{"access": access, "viewer": func() *modtest.Client { v, _ := signIn(t, h, "staff:view"); return v }()} {
			keys := competenceKeys(listCompetences(t, c, query))
			if slices.Contains(keys, "kran") || slices.Contains(keys, "cobol") || len(keys) != 14 {
				t.Errorf("%s %q lists %v, want neither the hidden nor the archived one", name, query, keys)
			}
		}
	}
	if keys := competenceKeys(listCompetences(t, hr, "")); slices.Contains(keys, "kran") || slices.Contains(keys, "cobol") {
		t.Errorf("HR's plain list = %v, want neither the hidden nor the archived one", keys)
	}
	if keys := competenceKeys(listCompetences(t, hr, "?includeHidden=true")); !slices.Contains(keys, "kran") || slices.Contains(keys, "cobol") {
		t.Errorf("HR's list with the hidden = %v, want kran and not cobol", keys)
	}
	if keys := competenceKeys(listCompetences(t, hr, "?includeHidden=true&includeArchived=true")); len(keys) != 16 {
		t.Errorf("HR's whole list = %v, want all sixteen", keys)
	}
	fag := competenceCategoryID(t, h, "fag")
	if keys := competenceKeys(listCompetences(t, hr, "?includeArchived=true&categoryId="+idText(fag))); !slices.Equal(keys, []string{"cobol"}) {
		t.Errorf("the fag category's = %v, want cobol alone", keys)
	}
}

// A company competence: a key of a–z, 0–9 and hyphens, unique; a validity
// only on a certificate, a lead only with a validity, the lead's default by
// the validity; an archived or unknown category refused. Each is the
// installation event competence_created.
func TestCompetences_Add(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	cert := competenceCategoryID(t, h, "sertifikat")

	skill := addCompetence(t, hr, skillBody(t, h, "go-lang"))
	if skill.Seeded || !skill.HasLevel || skill.Kind != "skill" || skill.ValidityMonths != nil || skill.Sort != 16 {
		t.Errorf("skill = %+v, want a company skill with a level, after the seeds", skill)
	}
	for validity, lead := range map[int]int32{12: 30, 24: 60, 36: 180} {
		c := addCompetence(t, hr, map[string]any{
			"categoryId": cert, "key": "kurs-" + idText(int64(validity)), "names": map[string]any{"en": "Course", "nb": "Kurs"},
			"kind": "certificate", "hasLevel": false, "validityMonths": validity,
		})
		if c.ReminderLeadDays == nil || *c.ReminderLeadDays != lead {
			t.Errorf("a validity of %d months has the lead %v, want %d", validity, c.ReminderLeadDays, lead)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'competence_created' AND person_id IS NULL
		AND payload->>'competenceId' = $1`, idText(skill.Id)); n != 1 {
		t.Errorf("%d competence_created events for the skill, want one", n)
	}

	archived := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.competence_categories (key, name_en, name_nb, sort, archived_at, created_at, updated_at)
		VALUES ('gammel', 'Old', 'Gammel', 9, $1, $1, $1) RETURNING id`, h.Now())
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a key another competence has", skillBody(t, h, "fse"), "key"},
		{"a key with an underscore", skillBody(t, h, "go_lang"), "key"},
		{"a validity on a skill", skillBody(t, h, "a1", "validityMonths", 12), "validityMonths"},
		{"a lead without a validity", skillBody(t, h, "a2", "kind", "certificate", "hasLevel", false, "reminderLeadDays", 10), "reminderLeadDays"},
		{"a validity of 241 months", skillBody(t, h, "a3", "kind", "certificate", "validityMonths", 241), "validityMonths"},
		{"a lead of 366 days", skillBody(t, h, "a4", "kind", "certificate", "validityMonths", 12, "reminderLeadDays", 366), "reminderLeadDays"},
		{"an unknown kind", skillBody(t, h, "a5", "kind", "diploma"), "kind"},
		{"an archived category", skillBody(t, h, "a6", "categoryId", archived), "categoryId"},
		{"an unknown category", skillBody(t, h, "a7", "categoryId", 9999999), "categoryId"},
	} {
		p := refused(t, hr.Do(http.MethodPost, competencesPath, tc.body), http.StatusBadRequest, "")
		if len(p.Errors[tc.field]) == 0 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, p.Errors, tc.field)
		}
	}
	viewer, _ := signIn(t, h, "staff:view")
	if r := viewer.Do(http.MethodPost, competencesPath, skillBody(t, h, "b1")); r.Status != http.StatusForbidden {
		t.Errorf("a viewer's add = %d, want 403", r.Status)
	}
}

// A seed changes its names, its lead, its issuer hint and whether it is
// hidden, and nothing else (seed_immutable); a property the change does not
// name — key, kind — is refused; a company competence's hasLevel is fixed
// once a person's row names it (competence_in_use), and its archive is never
// refused. Each change is the installation event competence_updated.
func TestCompetences_Change(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	var fse gen.StaffCompetence
	for _, c := range listCompetences(t, hr, "") {
		if c.Key == "fse" {
			fse = c
		}
	}

	var changed gen.StaffCompetence
	do(t, hr, http.MethodPut, competencePath(fse.Id), competenceChangeBody(fse,
		"names", map[string]any{"en": "FSE", "nb": "FSE-kurs"}, "reminderLeadDays", 45, "issuerHint", "Kurssenteret", "hidden", true),
		http.StatusOK, &changed, ifMatch(fse.Revision))
	if changed.Names.Nb != "FSE-kurs" || *changed.ReminderLeadDays != 45 || *changed.IssuerHint != "Kurssenteret" || !changed.Hidden {
		t.Errorf("changed = %+v, want the names, the lead, the hint and hidden", changed)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE kind = 'competence_updated' AND person_id IS NULL`); n != 1 {
		t.Errorf("%d competence_updated events, want one", n)
	}
	for name, kv := range map[string][]any{
		"its level":    {"hasLevel", true},
		"its validity": {"validityMonths", 24},
		"its category": {"categoryId", competenceCategoryID(t, h, "fag")},
		"an archive":   {"archived", true},
	} {
		refused(t, hr.Do(http.MethodPut, competencePath(fse.Id), competenceChangeBody(changed, kv...), ifMatch(changed.Revision)),
			http.StatusConflict, "seed_immutable")
		_ = name
	}
	p := refused(t, hr.Do(http.MethodPut, competencePath(fse.Id), competenceChangeBody(changed, "key", "x"), ifMatch(changed.Revision)),
		http.StatusBadRequest, "")
	if len(p.Errors["key"]) == 0 {
		t.Errorf("a key sent: errors = %v, want one on key", p.Errors)
	}
	refused(t, hr.Do(http.MethodPut, competencePath(fse.Id), competenceChangeBody(changed), ifMatch(fse.Revision)), http.StatusConflict, "revision_conflict")

	kari := seedPerson(t, h, "Kari", "Nordmann")
	kariC, kariUser := signIn(t, h)
	link(t, h, kari, kariUser)
	skill := addCompetence(t, hr, skillBody(t, h, "rust"))
	declare(t, kariC, kari, map[string]any{"competenceId": skill.Id, "level": 2})
	refused(t, hr.Do(http.MethodPut, competencePath(skill.Id), competenceChangeBody(skill, "hasLevel", false), ifMatch(skill.Revision)),
		http.StatusConflict, "competence_in_use")
	var archived gen.StaffCompetence
	do(t, hr, http.MethodPut, competencePath(skill.Id), competenceChangeBody(skill, "archived", true), http.StatusOK, &archived, ifMatch(skill.Revision))
	if archived.ArchivedAt == nil {
		t.Errorf("archived = %+v, want archived although a row names it", archived)
	}
	if r := hr.Do(http.MethodPut, competencePath(9999999), competenceChangeBody(skill), ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("an unknown competence = %d, want 404", r.Status)
	}
}
