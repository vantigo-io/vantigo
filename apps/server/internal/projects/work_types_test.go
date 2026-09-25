package projects_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The three work-type operations (work types design D1). A work type is a
// rule defined once per project — a name and two percentages — that Time
// multiplies an entry's rates by; Projects stores it and multiplies nothing.
// Two things run through every case: the multipliers are visible to everyone
// who sees the project (a rule, not an amount), and only a manager writes.

// workTypeJSON decodes WorkTypeResponse.
type workTypeJSON struct {
	Id                    int32     `json:"id"`
	ProjectId             int32     `json:"projectId"`
	Name                  string    `json:"name"`
	BillMultiplierPercent float64   `json:"billMultiplierPercent"`
	CostMultiplierPercent float64   `json:"costMultiplierPercent"`
	Active                bool      `json:"active"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

func workTypesPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/work-types", projectID)
}

func workTypePath(projectID, workTypeID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/work-types/%d", projectID, workTypeID)
}

// workTypeBody is a valid body — overtime at 150 % on both sides — which
// tests override one field of at a time. A nil override value removes that
// field, the convention createBody and lineBody use.
func workTypeBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":                  "Overtid 50 %",
		"billMultiplierPercent": 150,
		"costMultiplierPercent": 150,
	}
	for field, value := range overrides {
		if value == nil {
			delete(body, field)
			continue
		}
		body[field] = value
	}
	return body
}

func postWorkType(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, workTypesPath(projectID), workTypeBody(overrides))
}

// createWorkType is postWorkType for a test that expects the type to be added.
func createWorkType(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) workTypeJSON {
	t.Helper()
	r := postWorkType(t, c, projectID, overrides)
	if r.Status != http.StatusCreated {
		t.Fatalf("create work type: status %d body %s, want 201", r.Status, r.Body)
	}
	var wt workTypeJSON
	r.JSON(&wt)
	return wt
}

func putWorkType(t *testing.T, c *modtest.Client, projectID, workTypeID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, workTypePath(projectID, workTypeID), body)
}

// changeWorkType is putWorkType for a test that expects the change applied.
func changeWorkType(t *testing.T, c *modtest.Client, projectID, workTypeID int32, body map[string]any) workTypeJSON {
	t.Helper()
	r := putWorkType(t, c, projectID, workTypeID, body)
	if r.Status != http.StatusOK {
		t.Fatalf("change work type: status %d body %s, want 200", r.Status, r.Body)
	}
	var wt workTypeJSON
	r.JSON(&wt)
	return wt
}

// listWorkTypes reads the project's types and fails the test unless it may.
func listWorkTypes(t *testing.T, c *modtest.Client, projectID int32) []workTypeJSON {
	t.Helper()
	r := c.Do(http.MethodGet, workTypesPath(projectID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list work types: status %d body %s, want 200", r.Status, r.Body)
	}
	var types []workTypeJSON
	r.JSON(&types)
	return types
}

func workTypeNames(types []workTypeJSON) []string {
	names := make([]string, 0, len(types))
	for _, wt := range types {
		names = append(names, wt.Name)
	}
	return names
}

// D1's happy path: a manager adds a type, it answers with the name trimmed
// and the percentages as given, the list carries it, and the timeline names
// it and the fields it was added with — never their values.
func TestPostProjectsByIdWorkTypes_AddsATypeAndRecordsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1000"})

	created := createWorkType(t, c, project.Id, map[string]any{
		"name": "  Overtid 50 %  ", "billMultiplierPercent": 150, "costMultiplierPercent": 137.5,
	})

	if created.Name != "Overtid 50 %" || created.ProjectId != project.Id || created.BillMultiplierPercent != 150 ||
		created.CostMultiplierPercent != 137.5 || !created.Active {
		t.Errorf("created = %+v, want the trimmed name, 150 %% and 137.5 %%, active", created)
	}
	if got := listWorkTypes(t, c, project.Id); len(got) != 1 || got[0].Id != created.Id {
		t.Errorf("list = %+v, want exactly the type just added", got)
	}
	payload := lastPayload(t, h, project.Id, "work-type-added")
	if payload["name"] != "Overtid 50 %" || payload["workTypeId"] != float64(created.Id) {
		t.Errorf("work-type-added payload = %v, want the type's id and name", payload)
	}
	if fields := payloadFields(t, payload); !slices.Equal(fields, []string{"name", "billMultiplierPercent", "costMultiplierPercent"}) {
		t.Errorf("work-type-added fields = %v, want name and both multipliers", fields)
	}
	// The key set, not a search for "150" in the text: the id is in the
	// payload too, and a number search would trip on an id that held the
	// digits.
	if keys := slices.Sorted(maps.Keys(payload)); !slices.Equal(keys, []string{"fields", "name", "workTypeId"}) {
		t.Errorf("work-type-added payload keys = %v, want exactly [fields name workTypeId]; the timeline names fields, never their values", keys)
	}
}

// POST adds a type active whatever the body says: `active` is PUT's, and a
// create that forwarded it would add a type nobody can pick.
func TestPostProjectsByIdWorkTypes_IgnoresActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1009"})

	created := createWorkType(t, c, project.Id, map[string]any{"active": false})
	if !created.Active {
		t.Errorf("created = %+v, want it active: POST does not read active", created)
	}
	if got := listWorkTypes(t, c, project.Id); len(got) != 1 || !got[0].Active {
		t.Errorf("list = %+v, want the one type, active", got)
	}
}

// D1's rules, one case each, on the field the form renders them under.
func TestPostProjectsByIdWorkTypes_InvalidBody_Returns400OnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1001"})

	for _, tc := range []struct {
		name      string
		overrides map[string]any
		field     string
		message   string
	}{
		{"blank name", map[string]any{"name": "   "}, "name", "A work type name cannot be empty"},
		{"name longer than 100", map[string]any{"name": strings.Repeat("x", 101)}, "name",
			"A work type name cannot be longer than 100 characters, the given value was 101 characters"},
		{"zero bill multiplier", map[string]any{"billMultiplierPercent": 0}, "billMultiplierPercent",
			"The bill multiplier must be greater than zero"},
		{"bill multiplier past 1000", map[string]any{"billMultiplierPercent": 1000.01}, "billMultiplierPercent",
			"The bill multiplier cannot be more than 1000 %"},
		{"bill multiplier with three decimals", map[string]any{"billMultiplierPercent": 150.125}, "billMultiplierPercent",
			"The bill multiplier cannot have more than two decimals"},
		{"negative cost multiplier", map[string]any{"costMultiplierPercent": -10}, "costMultiplierPercent",
			"The cost multiplier must be greater than zero"},
		{"cost multiplier past 1000", map[string]any{"costMultiplierPercent": 1001}, "costMultiplierPercent",
			"The cost multiplier cannot be more than 1000 %"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := postWorkType(t, c, project.Id, tc.overrides)
			var problem validationProblemJSON
			r.JSON(&problem)
			if r.Status != http.StatusBadRequest || problem.Title != "Invalid project" ||
				!slices.Equal(problem.Errors[tc.field], []string{tc.message}) {
				t.Errorf("status %d body %s, want 400 with %q on %s", r.Status, r.Body, tc.message, tc.field)
			}
		})
	}
	// PUT shares the rules; one case shows its 400 is wired too.
	wt := createWorkType(t, c, project.Id, map[string]any{"name": "Helg"})
	r := putWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"name": "Helg", "billMultiplierPercent": 0}))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Invalid project" ||
		!slices.Equal(problem.Errors["billMultiplierPercent"], []string{"The bill multiplier must be greater than zero"}) {
		t.Errorf("PUT zero bill multiplier: status %d body %s, want 400 on billMultiplierPercent", r.Status, r.Body)
	}
	changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"name": "Helg", "active": false}))
	// 1000 exactly and two decimals are inside the rule.
	createWorkType(t, c, project.Id, map[string]any{"name": "Maks", "billMultiplierPercent": 1000, "costMultiplierPercent": 0.01})
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Maks", "Helg"}) {
		t.Errorf("names = %v, want only the valid types stored, the PUT's refused change not applied", got)
	}
}

// A name is unique per project without regard to case (D1): the unique index
// on lower(name) refuses the second, on a create and on a rename alike, and
// another project may use the same name.
func TestWorkTypes_ANameTakenInAnyCase_Returns409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1002"})
	other := createProject(t, c, map[string]any{"code": "WT1003"})
	createWorkType(t, c, project.Id, nil)

	r := postWorkType(t, c, project.Id, map[string]any{"name": "OVERTID 50 %"})
	var problem problemJSON
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Title != "Work type exists" || problem.Status != http.StatusConflict {
		t.Errorf("duplicate in another case: status %d body %s, want 409 \"Work type exists\"", r.Status, r.Body)
	}
	createWorkType(t, c, other.Id, nil)

	weekend := createWorkType(t, c, project.Id, map[string]any{"name": "Helg", "billMultiplierPercent": 200})
	r = putWorkType(t, c, project.Id, weekend.Id, workTypeBody(map[string]any{"name": "overtid 50 %"}))
	if r.Status != http.StatusConflict {
		t.Errorf("rename onto a taken name: status %d body %s, want 409", r.Status, r.Body)
	}
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Helg", "Overtid 50 %"}) {
		t.Errorf("names = %v, want both types as they were", got)
	}

	// A type's own name in another case is not taken: the index holds the
	// name on this row, so the rename is a change of name like any other.
	renamed := changeWorkType(t, c, project.Id, weekend.Id, workTypeBody(map[string]any{"name": "HELG", "billMultiplierPercent": 200}))
	if renamed.Name != "HELG" {
		t.Errorf("renamed = %+v, want the name HELG", renamed)
	}
	if fields := payloadFields(t, lastPayload(t, h, project.Id, "work-type-changed")); !slices.Equal(fields, []string{"name"}) {
		t.Errorf("fields = %v, want [name]", fields)
	}
}

// A change names what it moved; deactivating is a change of `active`; a body
// without `active` leaves it as it stands; a change that moved nothing writes
// no entry; and a deactivated type stays listed, after the active ones.
func TestPutProjectsByIdWorkTypes_ChangesDeactivatesAndRecordsWhatMoved(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1004"})
	wt := createWorkType(t, c, project.Id, nil)

	changed := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175}))
	if changed.CostMultiplierPercent != 175 || changed.BillMultiplierPercent != 150 || !changed.Active {
		t.Errorf("changed = %+v, want cost 175 %%, bill 150 %%, still active", changed)
	}
	if fields := payloadFields(t, lastPayload(t, h, project.Id, "work-type-changed")); !slices.Equal(fields, []string{"costMultiplierPercent"}) {
		t.Errorf("fields = %v, want [costMultiplierPercent]", fields)
	}

	deactivated := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175, "active": false}))
	if deactivated.Active {
		t.Error("Active = true, want the type deactivated")
	}
	if fields := payloadFields(t, lastPayload(t, h, project.Id, "work-type-changed")); !slices.Equal(fields, []string{"active"}) {
		t.Errorf("fields = %v, want [active]", fields)
	}

	before := eventTypes(t, h, project.Id)
	same := changeWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"costMultiplierPercent": 175}))
	if same.Active {
		t.Error("Active = true after a body without active, want it left as it stood")
	}
	if after := eventTypes(t, h, project.Id); len(after) != len(before) {
		t.Errorf("timeline = %v, want no entry for a change that moved nothing (was %v)", after, before)
	}

	createWorkType(t, c, project.Id, map[string]any{"name": "Helg"})
	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"Helg", "Overtid 50 %"}) {
		t.Errorf("names = %v, want the active type first and the deactivated one kept", got)
	}
}

// The list's order is D2's: active first, each half by name without regard
// to case — the order Time's picker and the Billing tab both show.
func TestGetProjectsByIdWorkTypes_ListsTheActiveOnesFirstByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1005"})
	createWorkType(t, c, project.Id, map[string]any{"name": "Zeta"})
	alfa := createWorkType(t, c, project.Id, map[string]any{"name": "Alfa"})
	createWorkType(t, c, project.Id, map[string]any{"name": "beta"})
	changeWorkType(t, c, project.Id, alfa.Id, workTypeBody(map[string]any{"name": "Alfa", "active": false}))

	if got := workTypeNames(listWorkTypes(t, c, project.Id)); !slices.Equal(got, []string{"beta", "Zeta", "Alfa"}) {
		t.Errorf("names = %v, want [beta Zeta Alfa]", got)
	}
}

// Multipliers are visible to everyone who sees the project (D1): a member
// with no financial rights reads them. Writing is the manager's: a member
// and a viewer are the access layer's 403, an outsider the unknown id's 404.
func TestWorkTypes_EveryoneWhoSeesTheProjectReads_OnlyAManagerWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signIn(t, h, "projects:create")
	project := createProject(t, manager, map[string]any{"code": "WT1006"})
	wt := createWorkType(t, manager, project.Id, map[string]any{"costMultiplierPercent": 140})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")
	outsider, _ := signIn(t, h)

	got := listWorkTypes(t, member, project.Id)
	if len(got) != 1 || got[0].BillMultiplierPercent != 150 || got[0].CostMultiplierPercent != 140 {
		t.Errorf("member's list = %+v, want the type with both multipliers", got)
	}
	listWorkTypes(t, viewer, project.Id)

	for name, c := range map[string]*modtest.Client{"member": member, "viewer": viewer} {
		if r := postWorkType(t, c, project.Id, map[string]any{"name": "Helg"}); r.Status != http.StatusForbidden {
			t.Errorf("%s POST: status %d, want 403", name, r.Status)
		}
		if r := putWorkType(t, c, project.Id, wt.Id, workTypeBody(map[string]any{"active": false})); r.Status != http.StatusForbidden {
			t.Errorf("%s PUT: status %d, want 403", name, r.Status)
		}
	}
	for label, r := range map[string]*modtest.Response{
		"GET":  outsider.Do(http.MethodGet, workTypesPath(project.Id), nil),
		"POST": postWorkType(t, outsider, project.Id, nil),
		"PUT":  putWorkType(t, outsider, project.Id, wt.Id, workTypeBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("outsider %s: status %d, want 404", label, r.Status)
		}
	}
	if got := workTypeNames(listWorkTypes(t, manager, project.Id)); !slices.Equal(got, []string{"Overtid 50 %"}) {
		t.Errorf("names = %v, want nothing written by a refused request", got)
	}
}

// 404 for an unknown project, an unknown type, and a type that exists on
// another project — the scoped lock's answer.
func TestWorkTypes_UnknownProjectOrType_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "WT1007"})
	other := createProject(t, c, map[string]any{"code": "WT1008"})
	foreign := createWorkType(t, c, other.Id, nil)

	for label, r := range map[string]*modtest.Response{
		"GET an unknown project":  c.Do(http.MethodGet, workTypesPath(999999), nil),
		"POST an unknown project": postWorkType(t, c, 999999, nil),
		"PUT an unknown type":     putWorkType(t, c, project.Id, 999999, workTypeBody(nil)),
		"PUT another's type":      putWorkType(t, c, project.Id, foreign.Id, workTypeBody(nil)),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("%s: status %d body %s, want 404", label, r.Status, r.Body)
		}
	}
}
