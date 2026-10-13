package staff_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const absenceTypesPath = "/api/v1/staff/absence-types"

func absenceTypePath(id int64) string { return rowPath(absenceTypesPath, id) }

// absenceTypesAs is the list as a caller holding permissions reads it.
func absenceTypesAs(t *testing.T, h *harness, permissions ...string) []gen.StaffAbsenceType {
	t.Helper()
	c, _ := signIn(t, h, permissions...)
	var out []gen.StaffAbsenceType
	do(t, c, http.MethodGet, absenceTypesPath, nil, http.StatusOK, &out)
	return out
}

// absenceTypeByKey is the type with key in list, failing the test when none
// has it.
func absenceTypeByKey(t *testing.T, list []gen.StaffAbsenceType, key string) gen.StaffAbsenceType {
	t.Helper()
	for _, a := range list {
		if a.Key == key {
			return a
		}
	}
	t.Fatalf("no type %s among %d", key, len(list))
	return gen.StaffAbsenceType{}
}

func typeKeys(list []gen.StaffAbsenceType) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.Key)
	}
	return out
}

// The seeds as every caller reads them: the twenty-two of the statutory
// vocabulary in order, hr_only ones included (plan reading 19) — and to
// staff:manage alone, the hidden ones too, the reserved absent last. The
// a-melding descriptions read back as the seeds name them.
func TestAbsenceTypes_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seeds := []string{
		"ferie", "egenmelding", "sykmelding", "svangerskapskontroll", "svangerskapspermisjon", "omsorgspermisjon",
		"foreldrepermisjon", "fodselspermisjon", "ammefri", "sykt_barn", "pleie_naerstaende", "utdanning_lovfestet",
		"utdanning_ikke_lovfestet", "militaertjeneste", "offentlig_verv", "religios_hoytid", "velferdspermisjon",
		"permittering", "avspasering", "helse", "annen_lovfestet", "annen_ikke_lovfestet",
	}
	for _, perms := range [][]string{{}, {"staff:view"}, {"staff:approve"}, {"staff:personal-data"}} {
		if got := typeKeys(absenceTypesAs(t, h, perms...)); !slices.Equal(got, seeds) {
			t.Errorf("as %v: types = %v, want the twenty-two visible seeds", perms, got)
		}
	}
	hr := absenceTypesAs(t, h, "staff:manage")
	if got := typeKeys(hr); !slices.Equal(got, append(slices.Clone(seeds), "absent")) {
		t.Errorf("as staff:manage: types = %v, want the seeds and the reserved absent", got)
	}
	absent := absenceTypeByKey(t, hr, "absent")
	if !absent.Hidden || !absent.Reserved || !absent.Seeded {
		t.Errorf("absent = %+v, want hidden, reserved and seeded", absent)
	}

	ferie := absenceTypeByKey(t, hr, "ferie")
	if ferie.Kind != gen.StaffAbsenceKindHoliday || ferie.Unit != gen.StaffAbsenceUnitDays || !ferie.Paid || !ferie.DrawsHoliday ||
		ferie.AllowsPercent || !ferie.NeedsDecision || ferie.Visibility != gen.StaffAbsenceVisibilityAbsent ||
		ferie.AmeldingLeave != nil || ferie.Reserved || ferie.Names.En != "Holiday" || ferie.Names.Nb != "Ferie" {
		t.Errorf("ferie = %+v, want holiday in days, paid, drawing on the account, no percentage, decided, absent", ferie)
	}
	sykmelding := absenceTypeByKey(t, hr, "sykmelding")
	if sykmelding.Kind != gen.StaffAbsenceKindSickCertified || sykmelding.AllowsPercent || sykmelding.NeedsDecision {
		t.Errorf("sykmelding = %+v, want sick_certified, no percentage (the share is its degree), registered", sykmelding)
	}
	for key, want := range map[string]gen.StaffAmeldingLeave{
		"velferdspermisjon":        gen.AndreIkkeLovfestedePermisjoner,
		"omsorgspermisjon":         gen.AndreLovfestedePermisjoner,
		"utdanning_ikke_lovfestet": gen.UtdanningspermisjonIkkeLovfestet,
		"permittering":             gen.Permittering,
	} {
		if got := absenceTypeByKey(t, hr, key).AmeldingLeave; got == nil || *got != want {
			t.Errorf("%s reports %v, want %s", key, got, want)
		}
	}
	if v := absenceTypeByKey(t, hr, "religios_hoytid").Visibility; v != gen.StaffAbsenceVisibilityHrOnly {
		t.Errorf("religios_hoytid's visibility = %s, want hr_only", v)
	}
}

// The a-melding descriptions are exactly the seven the kodeverk
// PermisjonsOgPermitteringsBeskrivelse offers today, in NAV's spellings (plan
// Revision 2, Q4a); the expired velferdspermisjon and the unsplit
// utdanningspermisjon are refused as a company type's mapping (Q4b), as is
// revision 1's misspelt annenPermisjonLovfestet.
func TestAbsenceTypes_AmeldingDescriptions(t *testing.T) {
	t.Parallel()
	want := []gen.StaffAmeldingLeave{
		"permisjonMedForeldrepenger", "permisjonVedMilitaertjeneste", "utdanningspermisjonLovfestet",
		"utdanningspermisjonIkkeLovfestet", "andreLovfestedePermisjoner", "andreIkkeLovfestedePermisjoner", "permittering",
	}
	got := []gen.StaffAmeldingLeave{
		gen.PermisjonMedForeldrepenger, gen.PermisjonVedMilitaertjeneste,
		gen.UtdanningspermisjonLovfestet, gen.UtdanningspermisjonIkkeLovfestet,
		gen.AndreLovfestedePermisjoner, gen.AndreIkkeLovfestedePermisjoner,
		gen.Permittering,
	}
	if !slices.Equal(got, want) {
		t.Errorf("the contract's descriptions = %v, want %v", got, want)
	}
	for _, v := range want {
		if !v.Valid() {
			t.Errorf("%s is not valid to the contract", v)
		}
	}

	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	for _, expired := range []string{"velferdspermisjon", "utdanningspermisjon", "annenPermisjonLovfestet"} {
		r := hr.Do(http.MethodPost, absenceTypesPath, companyType("own_"+expired, map[string]any{"ameldingLeave": expired}))
		if r.Status != http.StatusBadRequest {
			t.Errorf("a type reported as %s: status %d body %s, want 400", expired, r.Status, r.Body)
		}
	}
	var ok gen.StaffAbsenceType
	do(t, hr, http.MethodPost, absenceTypesPath, companyType("own_leave", map[string]any{"ameldingLeave": "andreIkkeLovfestedePermisjoner"}), http.StatusCreated, &ok)
	if ok.AmeldingLeave == nil || *ok.AmeldingLeave != gen.AndreIkkeLovfestedePermisjoner {
		t.Errorf("added = %+v, want andreIkkeLovfestedePermisjoner", ok)
	}
}

// companyType is a valid company type's body with key, with overrides laid
// over it.
func companyType(key string, overrides map[string]any) map[string]any {
	body := map[string]any{
		"key": key, "names": map[string]any{"en": "Study day", "nb": "Lesedag"}, "kind": "leave", "unit": "days",
		"paid": true, "drawsHoliday": false, "allowsPercent": true, "needsDecision": true, "visibility": "absent",
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	return body
}

// A company type: a key of a–z, 0–9 and underscores, unique; names in both
// languages; a company kind (the sick kinds are the seeds'); a percentage and
// the holiday account only on a day type. Added after the seeds, by
// staff:manage alone, and recorded as an installation event.
func TestAbsenceTypes_Add(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")

	var added gen.StaffAbsenceType
	do(t, hr, http.MethodPost, absenceTypesPath, companyType("study_day", nil), http.StatusCreated, &added)
	if added.Key != "study_day" || added.Kind != gen.StaffAbsenceKindLeave || added.Seeded || added.Hidden || added.Reserved ||
		added.Sort != 24 || added.Revision != 1 || !added.AllowsPercent || added.AmeldingLeave != nil {
		t.Errorf("added = %+v, want study_day, leave, not seeded, after the seeds (sort 24), revision 1", added)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NULL AND kind = 'absence_type_created'
		AND actor_user_id = $1 AND (payload->>'absenceTypeId')::bigint = $2`, hrID, added.Id); n != 1 {
		t.Errorf("absence_type_created events = %d, want 1", n)
	}
	if got := typeKeys(absenceTypesAs(t, h)); got[len(got)-1] != "study_day" {
		t.Errorf("types as anyone = %v, want study_day last", got)
	}

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"a key another type has", companyType("ferie", nil), "key"},
		{"a key with a hyphen", companyType("study-day", nil), "key"},
		{"a key with capitals", companyType("Study", nil), "key"},
		{"a key of 41 characters", companyType("a2345678901234567890123456789012345678901", nil), "key"},
		{"no Norwegian name", companyType("x", map[string]any{"names": map[string]any{"en": "X", "nb": " "}}), "names.nb"},
		{"a sick kind", companyType("x", map[string]any{"kind": "sick_self"}), "kind"},
		{"a child-sick kind", companyType("x", map[string]any{"kind": "child_sick"}), "kind"},
		{"a percentage on an hour type", companyType("x", map[string]any{"unit": "hours"}), "allowsPercent"},
		{"the holiday account on an hour type", companyType("x", map[string]any{"unit": "hours", "allowsPercent": false, "drawsHoliday": true}), "drawsHoliday"},
		{"sort 0", companyType("x", map[string]any{"sort": 0}), "sort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := refused(t, hr.Do(http.MethodPost, absenceTypesPath, tc.body), http.StatusBadRequest, "")
			if len(p.Errors[tc.field]) == 0 {
				t.Errorf("errors = %v, want %s named", p.Errors, tc.field)
			}
		})
	}
	for _, perms := range [][]string{{"staff:view"}, {"staff:approve"}, {"staff:personal-data"}} {
		c, _ := signIn(t, h, perms...)
		do(t, c, http.MethodPost, absenceTypesPath, companyType("y", nil), http.StatusForbidden, nil)
	}
}

// changeOf is the change request that sends a type back as it was read.
func changeOf(a gen.StaffAbsenceType) map[string]any {
	body := map[string]any{
		"names": map[string]any{"en": a.Names.En, "nb": a.Names.Nb}, "paid": a.Paid, "drawsHoliday": a.DrawsHoliday,
		"allowsPercent": a.AllowsPercent, "needsDecision": a.NeedsDecision, "visibility": a.Visibility,
		"hidden": a.Hidden, "sort": a.Sort,
	}
	if a.AmeldingLeave != nil {
		body["ameldingLeave"] = *a.AmeldingLeave
	}
	return body
}

// A seed changes its names, its order and whether it is hidden — a hidden
// seed leaves everyone's list but HR's — and nothing else: a flag, its
// visibility or its a-melding mapping is seed_immutable, and so is showing
// the reserved absent. A company type changes its flags too, never its kind;
// each change is an installation event, under If-Match.
func TestAbsenceTypes_Change(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	types := absenceTypesAs(t, h, "staff:manage")

	velferd := absenceTypeByKey(t, types, "velferdspermisjon")
	hide := changeOf(velferd)
	hide["hidden"] = true
	hide["sort"] = 99
	hide["names"] = map[string]any{"en": "Welfare day", "nb": "Velferdsdag"}
	refused(t, hr.Do(http.MethodPut, absenceTypePath(velferd.Id), hide, ifMatch(velferd.Revision+1)), http.StatusConflict, "revision_conflict")
	var hidden gen.StaffAbsenceType
	do(t, hr, http.MethodPut, absenceTypePath(velferd.Id), hide, http.StatusOK, &hidden, ifMatch(velferd.Revision))
	if !hidden.Hidden || hidden.Sort != 99 || hidden.Names.En != "Welfare day" || hidden.Revision != velferd.Revision+1 {
		t.Errorf("hidden = %+v, want hidden, sort 99, renamed, revision moved on", hidden)
	}
	if slices.Contains(typeKeys(absenceTypesAs(t, h, "staff:view")), "velferdspermisjon") {
		t.Error("a hidden seed is listed to staff:view")
	}
	if !slices.Contains(typeKeys(absenceTypesAs(t, h, "staff:manage")), "velferdspermisjon") {
		t.Error("a hidden seed is not listed to staff:manage")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IS NULL AND kind = 'absence_type_updated'
		AND (payload->>'absenceTypeId')::bigint = $1`, velferd.Id); n != 1 {
		t.Errorf("absence_type_updated events = %d, want 1", n)
	}

	ferie := absenceTypeByKey(t, types, "ferie")
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"paid", "paid", false},
		{"the holiday account", "drawsHoliday", false},
		{"a percentage", "allowsPercent", true},
		{"the decision", "needsDecision", false},
		{"the visibility", "visibility", "hr_only"},
		{"the a-melding mapping", "ameldingLeave", "andreLovfestedePermisjoner"},
	} {
		t.Run("a seed's "+tc.name, func(t *testing.T) {
			body := changeOf(ferie)
			body[tc.field] = tc.value
			refused(t, hr.Do(http.MethodPut, absenceTypePath(ferie.Id), body, ifMatch(ferie.Revision)), http.StatusConflict, "seed_immutable")
		})
	}
	absent := absenceTypeByKey(t, types, "absent")
	show := changeOf(absent)
	show["hidden"] = false
	refused(t, hr.Do(http.MethodPut, absenceTypePath(absent.Id), show, ifMatch(absent.Revision)), http.StatusConflict, "seed_immutable")
	rename := changeOf(absent)
	rename["names"] = map[string]any{"en": "Away", "nb": "Borte"}
	do(t, hr, http.MethodPut, absenceTypePath(absent.Id), rename, http.StatusOK, nil, ifMatch(absent.Revision))

	var own gen.StaffAbsenceType
	do(t, hr, http.MethodPost, absenceTypesPath, companyType("study_day", nil), http.StatusCreated, &own)
	change := changeOf(own)
	change["paid"] = false
	change["visibility"] = "hr_only"
	change["ameldingLeave"] = "andreIkkeLovfestedePermisjoner"
	var changed gen.StaffAbsenceType
	do(t, hr, http.MethodPut, absenceTypePath(own.Id), change, http.StatusOK, &changed, ifMatch(own.Revision))
	if changed.Paid || changed.Visibility != gen.StaffAbsenceVisibilityHrOnly || changed.AmeldingLeave == nil ||
		changed.Kind != gen.StaffAbsenceKindLeave || changed.Unit != gen.StaffAbsenceUnitDays {
		t.Errorf("changed = %+v, want unpaid, hr_only, mapped, kind and unit as they were", changed)
	}
	withKind := changeOf(changed)
	withKind["kind"] = "holiday"
	if p := refused(t, hr.Do(http.MethodPut, absenceTypePath(own.Id), withKind, ifMatch(changed.Revision)), http.StatusBadRequest, ""); len(p.Errors["kind"]) == 0 {
		t.Errorf("a change naming a kind: errors = %v, want kind refused by name (it is fixed at creation)", p.Errors)
	}
	do(t, hr, http.MethodPut, absenceTypePath(own.Id+1000), changeOf(changed), http.StatusNotFound, nil, ifMatch(1))
	viewer, _ := signIn(t, h, "staff:view")
	do(t, viewer, http.MethodPut, absenceTypePath(own.Id), changeOf(changed), http.StatusForbidden, nil, ifMatch(changed.Revision))
}

// A company type's visibility is widened — hr_only to absent, which shows
// colleagues and the manager what was HR's — only while no absence names it:
// widening it under rows would reveal, after the fact, a type their people
// took on the promise it stayed HR's (absence_type_in_use). Narrowing —
// absent to hr_only — is always allowed, and so is widening a type nobody
// has taken.
func TestAbsenceTypes_VisibilityIsNotWidenedUnderRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")

	var private gen.StaffAbsenceType
	do(t, hr, http.MethodPost, absenceTypesPath, companyType("private_matter", map[string]any{"visibility": "hr_only"}), http.StatusCreated, &private)
	var widened gen.StaffAbsenceType
	do(t, hr, http.MethodPut, absenceTypePath(private.Id), changeOf(private), http.StatusOK, nil, ifMatch(private.Revision))
	open := changeOf(private)
	open["visibility"] = "absent"
	do(t, hr, http.MethodPut, absenceTypePath(private.Id), open, http.StatusOK, &widened, ifMatch(private.Revision+1))
	if widened.Visibility != gen.StaffAbsenceVisibilityAbsent {
		t.Fatalf("widened while unused = %+v, want absent", widened)
	}
	closed := changeOf(widened)
	closed["visibility"] = "hr_only"
	var narrowed gen.StaffAbsenceType
	do(t, hr, http.MethodPut, absenceTypePath(private.Id), closed, http.StatusOK, &narrowed, ifMatch(widened.Revision))

	person := seedPerson(t, h, "Kari", "Nordmann")
	h.Exec(t, `INSERT INTO staff.absences (person_id, type_id, first_day, last_day, status, requested_by_user_id, created_at, updated_at)
		VALUES ($1, $2, '2026-10-01', '2026-10-02', 'approved', $3, now(), now())`, person, private.Id, hrID)
	again := changeOf(narrowed)
	again["visibility"] = "absent"
	refused(t, hr.Do(http.MethodPut, absenceTypePath(private.Id), again, ifMatch(narrowed.Revision)), http.StatusConflict, "absence_type_in_use")
	// A rename under rows is fine; the visibility stays HR's.
	rename := changeOf(narrowed)
	rename["names"] = map[string]any{"en": "Private", "nb": "Privat"}
	var renamed gen.StaffAbsenceType
	do(t, hr, http.MethodPut, absenceTypePath(private.Id), rename, http.StatusOK, &renamed, ifMatch(narrowed.Revision))
	if renamed.Visibility != gen.StaffAbsenceVisibilityHrOnly {
		t.Errorf("renamed = %+v, want hr_only still", renamed)
	}
}
