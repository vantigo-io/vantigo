package staff_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// Two valid organisation numbers (Brønnøysundregistrene's and Equinor's) and
// one with its check digit flipped.
const (
	orgBrreg   = "974760673"
	orgEquinor = "923609016"
	orgBadMod  = "923609017"
)

func createLegalEntity(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffLegalEntity {
	t.Helper()
	var out gen.StaffLegalEntity
	do(t, c, http.MethodPost, legalEntitiesPath, body, http.StatusCreated, &out)
	return out
}

func createBusinessUnit(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffBusinessUnit {
	t.Helper()
	var out gen.StaffBusinessUnit
	do(t, c, http.MethodPost, businessUnitsPath, body, http.StatusCreated, &out)
	return out
}

func createDepartment(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffDepartment {
	t.Helper()
	var out gen.StaffDepartment
	do(t, c, http.MethodPost, departmentsPath, body, http.StatusCreated, &out)
	return out
}

func listLegalEntities(t *testing.T, c *modtest.Client) []gen.StaffLegalEntity {
	t.Helper()
	var out []gen.StaffLegalEntity
	do(t, c, http.MethodGet, legalEntitiesPath, nil, http.StatusOK, &out)
	return out
}

func listBusinessUnits(t *testing.T, c *modtest.Client) []gen.StaffBusinessUnit {
	t.Helper()
	var out []gen.StaffBusinessUnit
	do(t, c, http.MethodGet, businessUnitsPath, nil, http.StatusOK, &out)
	return out
}

// defaults is the ids of the rows marked default.
func defaults[T any](rows []T, id func(T) int64, isDefault func(T) bool) []int64 {
	var out []int64
	for _, r := range rows {
		if isDefault(r) {
			out = append(out, id(r))
		}
	}
	return out
}

func defaultLegalEntities(t *testing.T, c *modtest.Client) []int64 {
	t.Helper()
	return defaults(listLegalEntities(t, c),
		func(r gen.StaffLegalEntity) int64 { return r.Id }, func(r gen.StaffLegalEntity) bool { return r.IsDefault })
}

func defaultBusinessUnits(t *testing.T, c *modtest.Client) []int64 {
	t.Helper()
	return defaults(listBusinessUnits(t, c),
		func(r gen.StaffBusinessUnit) int64 { return r.Id }, func(r gen.StaffBusinessUnit) bool { return r.IsDefault })
}

// The structure is the directory's to read (staff:view) and HR's to change
// (staff:manage): either alone is the uniform 403 on the other's operations.
func TestStructure_ReadIsStaffViewAndWriteIsStaffManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "staff:view")
	writer, _ := signIn(t, h, "staff:manage")
	member, _ := signIn(t, h)

	for _, path := range []string{legalEntitiesPath, businessUnitsPath, departmentsPath, locationsPath} {
		do(t, viewer, http.MethodGet, path, nil, http.StatusOK, nil)
		for _, c := range []*modtest.Client{writer, member} {
			if r := c.Do(http.MethodGet, path, nil); r.Status != http.StatusForbidden {
				t.Errorf("GET %s without staff:view: %d, want 403", path, r.Status)
			}
		}
		if r := viewer.Do(http.MethodPost, path, map[string]any{"name": "X", "code": "X", "legalEntityId": 1}); r.Status != http.StatusForbidden {
			t.Errorf("POST %s without staff:manage: %d, want 403", path, r.Status)
		}
	}
}

// Legal entities: the first becomes the default, a later one does not until a
// PUT with isDefault true moves the default to it — exactly one at every
// step. The default is never archived and never un-defaulted in place; an
// archived row is never made the default.
func TestLegalEntities_ExactlyOneDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")

	first := createLegalEntity(t, hr, map[string]any{"name": "Kraft-Verket AS", "orgNumber": orgBrreg})
	if !first.IsDefault || first.Revision != 1 || first.OrgNumber == nil || *first.OrgNumber != orgBrreg {
		t.Errorf("the first legal entity = %+v, want it the default, revision 1", first)
	}
	second := createLegalEntity(t, hr, map[string]any{"name": "Kraft-Verket Holding AS"})
	if second.IsDefault {
		t.Errorf("the second legal entity = %+v, want it not the default", second)
	}
	var moved gen.StaffLegalEntity
	do(t, hr, http.MethodPut, rowPath(legalEntitiesPath, second.Id),
		map[string]any{"name": "Kraft-Verket Holding AS", "isDefault": true}, http.StatusOK, &moved, ifMatch(second.Revision))
	if !moved.IsDefault || moved.Revision != 2 {
		t.Errorf("moved = %+v, want the default at revision 2", moved)
	}
	if got := defaultLegalEntities(t, hr); len(got) != 1 || got[0] != second.Id {
		t.Errorf("defaults = %v, want only %d", got, second.Id)
	}

	refused(t, hr.Do(http.MethodPost, archivePath(legalEntitiesPath, second.Id), nil), http.StatusConflict, "structure_default")
	refused(t, hr.Do(http.MethodPut, rowPath(legalEntitiesPath, second.Id),
		map[string]any{"name": "Holding", "isDefault": false}, ifMatch(moved.Revision)), http.StatusConflict, "structure_default")

	// A PUT that leaves isDefault out keeps the flag as it is.
	var renamed gen.StaffLegalEntity
	do(t, hr, http.MethodPut, rowPath(legalEntitiesPath, second.Id), map[string]any{"name": "Holding AS"},
		http.StatusOK, &renamed, ifMatch(moved.Revision))
	if !renamed.IsDefault || renamed.Name != "Holding AS" {
		t.Errorf("renamed = %+v, want still the default", renamed)
	}

	// The first is no longer the default, so it may be archived — and then
	// never made the default again.
	var archived gen.StaffLegalEntity
	do(t, hr, http.MethodPost, archivePath(legalEntitiesPath, first.Id), nil, http.StatusOK, &archived)
	if archived.ArchivedAt == nil || archived.IsDefault {
		t.Errorf("archived = %+v, want an archive stamp", archived)
	}
	var again gen.StaffLegalEntity
	do(t, hr, http.MethodPost, archivePath(legalEntitiesPath, first.Id), nil, http.StatusOK, &again)
	if again.Revision != archived.Revision || !again.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Errorf("archiving again = %+v, want nothing changed from %+v", again, archived)
	}
	refused(t, hr.Do(http.MethodPut, rowPath(legalEntitiesPath, first.Id),
		map[string]any{"name": "Kraft-Verket AS", "isDefault": true}, ifMatch(archived.Revision)), http.StatusConflict, "structure_archived")
	if got := defaultLegalEntities(t, hr); len(got) != 1 || got[0] != second.Id {
		t.Errorf("defaults = %v, want still only %d", got, second.Id)
	}

	// Archived rows stay in the list, so a stored reference can be named.
	if got := listLegalEntities(t, hr); len(got) != 2 {
		t.Errorf("list = %+v, want both, the archived one included", got)
	}
}

// A POST with isDefault true takes the default from whichever row had it.
func TestLegalEntities_ACreateMayTakeTheDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")

	createLegalEntity(t, hr, map[string]any{"name": "A"})
	b := createLegalEntity(t, hr, map[string]any{"name": "B", "isDefault": true})
	if got := defaultLegalEntities(t, hr); len(got) != 1 || got[0] != b.Id {
		t.Errorf("defaults = %v, want only %d", got, b.Id)
	}
}

// The organisation number is nine digits with a valid MOD11 check digit,
// spaces ignored, unique among legal entities; a wrong one is
// org_number_invalid on its field, and a taken one a field error.
func TestLegalEntities_TheOrganisationNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")

	for _, bad := range []string{orgBadMod, "12345678", "1234567890", "92360901A", "912345678"} {
		p := refused(t, hr.Do(http.MethodPost, legalEntitiesPath, map[string]any{"name": "X", "orgNumber": bad}),
			http.StatusBadRequest, "org_number_invalid")
		if len(p.Errors["orgNumber"]) == 0 {
			t.Errorf("%q: errors %v, want one on orgNumber", bad, p.Errors)
		}
	}
	spaced := createLegalEntity(t, hr, map[string]any{"name": "Equinor", "orgNumber": "923 609 016"})
	if spaced.OrgNumber == nil || *spaced.OrgNumber != orgEquinor {
		t.Errorf("orgNumber = %v, want the spaces taken out", spaced.OrgNumber)
	}
	p := refused(t, hr.Do(http.MethodPost, legalEntitiesPath, map[string]any{"name": "Equinor again", "orgNumber": orgEquinor}),
		http.StatusBadRequest, "")
	if len(p.Errors["orgNumber"]) == 0 {
		t.Errorf("a taken number: errors %v, want one on orgNumber", p.Errors)
	}
	for _, bad := range []map[string]any{{"name": ""}, {"name": "   "}, {"name": strings.Repeat("x", 201)}} {
		if r := hr.Do(http.MethodPost, legalEntitiesPath, bad); r.Status != http.StatusBadRequest {
			t.Errorf("name %q: %d, want 400", bad["name"], r.Status)
		}
	}
}

// A change is guarded by the revision it was read at, and an unknown id is a
// bare 404 on both the change and the archive.
func TestStructure_RevisionsAndUnknownIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")
	le := createLegalEntity(t, hr, map[string]any{"name": "A"})

	do(t, hr, http.MethodPut, rowPath(legalEntitiesPath, le.Id), map[string]any{"name": "B"}, http.StatusOK, nil, ifMatch(1))
	refused(t, hr.Do(http.MethodPut, rowPath(legalEntitiesPath, le.Id), map[string]any{"name": "C"}, ifMatch(1)),
		http.StatusConflict, "revision_conflict")

	for _, path := range []string{legalEntitiesPath, businessUnitsPath, departmentsPath, locationsPath} {
		body := map[string]any{"name": "X", "code": "X", "legalEntityId": le.Id}
		if r := hr.Do(http.MethodPut, rowPath(path, 999999), body, ifMatch(1)); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("PUT %s unknown: %d %q, want a bare 404", path, r.Status, r.Body)
		}
		if r := hr.Do(http.MethodPost, archivePath(path, 999999), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("archive %s unknown: %d %q, want a bare 404", path, r.Status, r.Body)
		}
	}
}

// Business units live under a legal entity: an unknown one is a field error,
// an archived one structure_archived for a new unit or one moved under it —
// but a unit already under a legal entity archived since keeps it. The
// default rules are the legal entities' own.
func TestBusinessUnits_UnderALegalEntity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")
	parent := createLegalEntity(t, hr, map[string]any{"name": "Parent AS"})
	other := createLegalEntity(t, hr, map[string]any{"name": "Other AS"})

	p := refused(t, hr.Do(http.MethodPost, businessUnitsPath, map[string]any{"legalEntityId": 999999, "name": "U"}),
		http.StatusBadRequest, "")
	if len(p.Errors["legalEntityId"]) == 0 {
		t.Errorf("an unknown legal entity: errors %v, want one on legalEntityId", p.Errors)
	}

	oslo := createBusinessUnit(t, hr, map[string]any{"legalEntityId": other.Id, "name": "Oslo", "orgNumber": orgBrreg})
	if !oslo.IsDefault {
		t.Errorf("the first unit = %+v, want it the default", oslo)
	}
	bergen := createBusinessUnit(t, hr, map[string]any{"legalEntityId": parent.Id, "name": "Bergen", "orgNumber": orgEquinor})
	if bergen.IsDefault {
		t.Errorf("the second unit = %+v, want it not the default", bergen)
	}
	refused(t, hr.Do(http.MethodPost, businessUnitsPath, map[string]any{"legalEntityId": parent.Id, "name": "Dup", "orgNumber": orgBrreg}),
		http.StatusBadRequest, "")
	refused(t, hr.Do(http.MethodPost, businessUnitsPath, map[string]any{"legalEntityId": parent.Id, "name": "Bad", "orgNumber": orgBadMod}),
		http.StatusBadRequest, "org_number_invalid")

	// The default unit's legal entity is never archived: Oslo is the default
	// and sits under Other AS.
	r := hr.Do(http.MethodPost, archivePath(legalEntitiesPath, other.Id), nil)
	refused(t, r, http.StatusConflict, "structure_default")
	if !strings.Contains(string(r.Body), "business unit") {
		t.Errorf("refusal %s, want it to name the default business unit", r.Body)
	}
	var moved gen.StaffBusinessUnit
	do(t, hr, http.MethodPut, rowPath(businessUnitsPath, bergen.Id),
		map[string]any{"legalEntityId": parent.Id, "name": "Bergen", "isDefault": true}, http.StatusOK, &moved, ifMatch(bergen.Revision))
	if got := defaultBusinessUnits(t, hr); len(got) != 1 || got[0] != bergen.Id {
		t.Errorf("defaults = %v, want only %d", got, bergen.Id)
	}
	if moved.OrgNumber != nil {
		t.Errorf("orgNumber = %v, want it cleared by a replace that left it out", *moved.OrgNumber)
	}

	// With the default moved away, Other AS may be archived.
	do(t, hr, http.MethodPost, archivePath(legalEntitiesPath, other.Id), nil, http.StatusOK, nil)
	refused(t, hr.Do(http.MethodPost, businessUnitsPath, map[string]any{"legalEntityId": other.Id, "name": "Trondheim"}),
		http.StatusConflict, "structure_archived")
	refused(t, hr.Do(http.MethodPut, rowPath(businessUnitsPath, bergen.Id),
		map[string]any{"legalEntityId": other.Id, "name": "Bergen"}, ifMatch(moved.Revision)), http.StatusConflict, "structure_archived")
	// Losing the default moved Oslo's revision on.
	var kept gen.StaffBusinessUnit
	do(t, hr, http.MethodPut, rowPath(businessUnitsPath, oslo.Id),
		map[string]any{"legalEntityId": other.Id, "name": "Oslo sentrum", "orgNumber": orgBrreg}, http.StatusOK, &kept, ifMatch(oslo.Revision+1))
	if kept.Name != "Oslo sentrum" || kept.LegalEntityId != other.Id {
		t.Errorf("kept = %+v, want renamed under its archived legal entity", kept)
	}

	refused(t, hr.Do(http.MethodPost, archivePath(businessUnitsPath, bergen.Id), nil), http.StatusConflict, "structure_default")
	// A unit under an archived legal entity keeps it, but is never made the
	// default there.
	refused(t, hr.Do(http.MethodPut, rowPath(businessUnitsPath, oslo.Id),
		map[string]any{"legalEntityId": other.Id, "name": "Oslo sentrum", "isDefault": true}, ifMatch(kept.Revision)),
		http.StatusConflict, "structure_archived")
	do(t, hr, http.MethodPost, archivePath(businessUnitsPath, oslo.Id), nil, http.StatusOK, nil)

	// A legal entity holding the default business unit is not archived either,
	// even once it is no longer the default legal entity itself: the default
	// unit would sit under an archived legal entity. Moving the default unit
	// elsewhere lets it go.
	holding := createLegalEntity(t, hr, map[string]any{"name": "Holding AS", "isDefault": true})
	r = hr.Do(http.MethodPost, archivePath(legalEntitiesPath, parent.Id), nil)
	refused(t, r, http.StatusConflict, "structure_default")
	if !strings.Contains(string(r.Body), "business unit") {
		t.Errorf("refusal %s, want it to name the default business unit", r.Body)
	}
	createBusinessUnit(t, hr, map[string]any{"legalEntityId": holding.Id, "name": "Tromsø", "isDefault": true})
	do(t, hr, http.MethodPost, archivePath(legalEntitiesPath, parent.Id), nil, http.StatusOK, nil)
}

// Departments: the code is unique however it is cased; the manager is an
// existing person; the parent an existing department that is neither the
// department itself nor below it (department_cycle), and not archived unless
// the department already had it.
func TestDepartments_CodesManagersAndParents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")
	head := seedPerson(t, h, "Ola", "Nordmann")

	top := createDepartment(t, hr, map[string]any{"code": "OPS", "name": "Operations", "managerPersonId": head,
		"costCentre": "4000", "division": "Field"})
	if top.ManagerPersonId == nil || *top.ManagerPersonId != head || top.CostCentre == nil || *top.CostCentre != "4000" {
		t.Errorf("top = %+v, want its manager and cost centre", top)
	}
	mid := createDepartment(t, hr, map[string]any{"code": "OPS-N", "name": "North", "parentId": top.Id})
	leaf := createDepartment(t, hr, map[string]any{"code": "OPS-N-1", "name": "North 1", "parentId": mid.Id})

	lone := hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": "LONE", "name": "Lone"})
	if lone.Status != http.StatusCreated {
		t.Fatalf("a department with only a code and a name: %d %s", lone.Status, lone.Body)
	}
	for _, key := range []string{"managerPersonId", "parentId", "costCentre", "division", "archivedAt"} {
		if _, ok := raw(t, lone)[key]; ok {
			t.Errorf("a department without %s carries it: %s", key, lone.Body)
		}
	}

	p := refused(t, hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": "ops", "name": "Again"}), http.StatusBadRequest, "")
	if len(p.Errors["code"]) == 0 {
		t.Errorf("a code taken in another case: errors %v, want one on code", p.Errors)
	}
	for _, bad := range []string{"", "has space", "ÆØÅ", "ABCDEFGHIJKLMNOPQRSTU"} {
		p := refused(t, hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": bad, "name": "N"}), http.StatusBadRequest, "")
		if len(p.Errors["code"]) == 0 {
			t.Errorf("code %q: errors %v, want one on code", bad, p.Errors)
		}
	}
	p = refused(t, hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": "M", "name": "M", "managerPersonId": 999999}),
		http.StatusBadRequest, "")
	if len(p.Errors["managerPersonId"]) == 0 {
		t.Errorf("an unknown manager: errors %v, want one on managerPersonId", p.Errors)
	}
	p = refused(t, hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": "P", "name": "P", "parentId": 999999}),
		http.StatusBadRequest, "")
	if len(p.Errors["parentId"]) == 0 {
		t.Errorf("an unknown parent: errors %v, want one on parentId", p.Errors)
	}

	refused(t, hr.Do(http.MethodPut, rowPath(departmentsPath, top.Id),
		map[string]any{"code": "OPS", "name": "Operations", "parentId": top.Id}, ifMatch(top.Revision)), http.StatusConflict, "department_cycle")
	refused(t, hr.Do(http.MethodPut, rowPath(departmentsPath, top.Id),
		map[string]any{"code": "OPS", "name": "Operations", "parentId": leaf.Id}, ifMatch(top.Revision)), http.StatusConflict, "department_cycle")

	other := createDepartment(t, hr, map[string]any{"code": "FIN", "name": "Finance"})
	do(t, hr, http.MethodPost, archivePath(departmentsPath, other.Id), nil, http.StatusOK, nil)
	refused(t, hr.Do(http.MethodPost, departmentsPath, map[string]any{"code": "FIN-1", "name": "Finance 1", "parentId": other.Id}),
		http.StatusConflict, "structure_archived")
	refused(t, hr.Do(http.MethodPut, rowPath(departmentsPath, leaf.Id),
		map[string]any{"code": "OPS-N-1", "name": "North 1", "parentId": other.Id}, ifMatch(leaf.Revision)), http.StatusConflict, "structure_archived")

	// The middle department archived, its child keeps it as a parent.
	do(t, hr, http.MethodPost, archivePath(departmentsPath, mid.Id), nil, http.StatusOK, nil)
	var renamed gen.StaffDepartment
	do(t, hr, http.MethodPut, rowPath(departmentsPath, leaf.Id),
		map[string]any{"code": "ops-n-1", "name": "North one", "parentId": mid.Id}, http.StatusOK, &renamed, ifMatch(leaf.Revision))
	if renamed.Code != "ops-n-1" || renamed.ParentId == nil || *renamed.ParentId != mid.Id {
		t.Errorf("renamed = %+v, want its own code re-cased and its archived parent kept", renamed)
	}
	// Moving a department to another branch of the tree is no cycle.
	do(t, hr, http.MethodPut, rowPath(departmentsPath, leaf.Id),
		map[string]any{"code": "OPS-N-1", "name": "North 1", "parentId": top.Id}, http.StatusOK, nil, ifMatch(renamed.Revision))
}

// Locations: Norway when no country is given, an alpha-2 code upper-cased
// otherwise, and the optional address lines absent when not set.
func TestLocations_TheCountryAndTheAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")

	var office gen.StaffLocation
	r := hr.Do(http.MethodPost, locationsPath, map[string]any{"name": "Hovedkontor"})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	r.JSON(&office)
	if office.Country != "NO" {
		t.Errorf("country = %q, want NO by default", office.Country)
	}
	for _, key := range []string{"street", "postalCode", "city", "archivedAt"} {
		if _, ok := raw(t, r)[key]; ok {
			t.Errorf("a location without %s carries it", key)
		}
	}
	var moved gen.StaffLocation
	do(t, hr, http.MethodPut, rowPath(locationsPath, office.Id), map[string]any{
		"name": "Stockholm", "street": "Drottninggatan 1", "postalCode": "111 51", "city": "Stockholm", "country": "se",
	}, http.StatusOK, &moved, ifMatch(office.Revision))
	if moved.Country != "SE" || moved.City == nil || *moved.City != "Stockholm" {
		t.Errorf("moved = %+v, want SE upper-cased and the address kept", moved)
	}
	for _, bad := range []map[string]any{
		{"name": "X", "country": "XX"},
		{"name": "X", "country": "NOR"},
		{"name": "X", "postalCode": "12345678901234567"},
		{"name": ""},
	} {
		if r := hr.Do(http.MethodPost, locationsPath, bad); r.Status != http.StatusBadRequest {
			t.Errorf("%v: %d %s, want 400", bad, r.Status, r.Body)
		}
	}
	var archived gen.StaffLocation
	do(t, hr, http.MethodPost, archivePath(locationsPath, office.Id), nil, http.StatusOK, &archived)
	if archived.ArchivedAt == nil {
		t.Errorf("archived = %+v, want a stamp", archived)
	}
	var list []gen.StaffLocation
	do(t, hr, http.MethodGet, locationsPath, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].ArchivedAt == nil {
		t.Errorf("list = %+v, want the archived location still listed", list)
	}
	var departments []gen.StaffDepartment
	do(t, hr, http.MethodGet, departmentsPath, nil, http.StatusOK, &departments)
}
