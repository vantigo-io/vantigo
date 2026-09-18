package projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The four people operations (design §5, D6, D11): who holds a role on a
// project, who may change that, and who is left to assign. Every case here is
// about the same two questions — what a role does to what its holder can see,
// and what the timeline says happened — so the tests read the project back
// through the user whose role just moved rather than trusting the role list
// alone.

// roleJSON decodes ProjectRoleResponse. active is the directory's answer, not
// the assignment's: a disabled account keeps its role and lists as inactive.
type roleJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
}

func rolesPath(projectID int32) string {
	return fmt.Sprintf("/api/v1/projects/%d/roles", projectID)
}

func rolePath(projectID int32, userID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/projects/%d/roles/%s", projectID, userID)
}

// listRoles reads a project's role list, failing the test unless it answered
// 200.
func listRoles(t *testing.T, c *modtest.Client, projectID int32) []roleJSON {
	t.Helper()
	r := c.Do(http.MethodGet, rolesPath(projectID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list roles: status %d body %s, want 200", r.Status, r.Body)
	}
	var roles []roleJSON
	r.JSON(&roles)
	return roles
}

// putRole assigns a role and returns the raw response, for a case whose
// subject is the refusal.
func putRole(t *testing.T, c *modtest.Client, projectID int32, userID uuid.UUID, role string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, rolePath(projectID, userID), map[string]any{"role": role})
}

// assignRole is putRole for a test that expects the assignment to be applied.
func assignRole(t *testing.T, c *modtest.Client, projectID int32, userID uuid.UUID, role string) roleJSON {
	t.Helper()
	r := putRole(t, c, projectID, userID, role)
	if r.Status != http.StatusOK {
		t.Fatalf("assign %q: status %d body %s, want 200", role, r.Status, r.Body)
	}
	var assigned roleJSON
	r.JSON(&assigned)
	return assigned
}

func deleteRole(t *testing.T, c *modtest.Client, projectID int32, userID uuid.UUID) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, rolePath(projectID, userID), nil)
}

// assignableUsers reads the assignable-user search, failing the test unless it
// answered 200. An empty search reads the first page of everyone assignable.
func assignableUsers(t *testing.T, c *modtest.Client, projectID int32, search string) []personJSON {
	t.Helper()
	path := fmt.Sprintf("/api/v1/projects/%d/assignable-users", projectID)
	if search != "" {
		path += "?search=" + url.QueryEscape(search)
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("assignable users %q: status %d body %s, want 200", search, r.Status, r.Body)
	}
	var users []personJSON
	r.JSON(&users)
	return users
}

// setDisplayName names a seeded user, so a test about ordering or searching by
// display name has names to order and search by: modtest seeds users under a
// generated address.
func setDisplayName(t *testing.T, h *modtest.Harness, userID uuid.UUID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, userID, name)
}

// disableUser flips the flag contracts.UserDirectory reports as
// Active: false, the state an account is left in rather than deleted.
func disableUser(t *testing.T, h *modtest.Harness, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, userID)
}

// payloads is every timeline payload of one type on one project, oldest
// first, for a case whose subject is how many entries a mutation wrote and
// what each of them said.
func payloads(t *testing.T, h *modtest.Harness, projectID int32, eventType string) []map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h, `SELECT coalesce(json_agg(payload ORDER BY id)::text, '[]')
	                                  FROM projects.timeline_entries WHERE project_id = $1 AND event_type = $2`,
		projectID, eventType)
	var out []map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

// lastPayload is the payload of the newest timeline entry of one type, which
// is what a mutation's assertion is actually about.
func lastPayload(t *testing.T, h *modtest.Harness, projectID int32, eventType string) map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = $2 ORDER BY id DESC LIMIT 1`,
		projectID, eventType)
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return payload
}

func displayNames(users []personJSON) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.DisplayName)
	}
	return out
}

func roleDisplayNames(roles []roleJSON) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.DisplayName)
	}
	return out
}

// The whole life of one assignment: a stranger cannot see the project, a role
// lets them, changing it keeps the assignment, and removing it takes the
// project away again. Each step's timeline entry is checked where it is
// written, because an entry written for the wrong step is indistinguishable
// from the right one at the end.
func TestProjectRoles_AddChangeAndRemoveAMember_TracksVisibilityAndTheTimeline(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "PPL1000"})
	member, memberID := signIn(t, h)
	setDisplayName(t, h, memberID, "Mia Medlem")

	if r := getProject(t, member, project.Id); r.Status != http.StatusNotFound {
		t.Fatalf("before the assignment: status %d body %s, want 404", r.Status, r.Body)
	}

	added := assignRole(t, creator, project.Id, memberID, "member")
	if added.UserId != memberID || added.Role != "member" || !added.Active {
		t.Errorf("added = %+v, want the member, active", added)
	}
	if added.DisplayName != "Mia Medlem" {
		t.Errorf("DisplayName = %q, want the directory's name", added.DisplayName)
	}
	if r := getProject(t, member, project.Id); r.Status != http.StatusOK {
		t.Fatalf("after the assignment: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := roleDisplayNames(listRoles(t, creator, project.Id)); len(got) != 2 {
		t.Errorf("roles = %v, want the creator and the new member", got)
	}
	payload := lastPayload(t, h, project.Id, "role-added")
	if payload["userId"] != memberID.String() || payload["displayName"] != "Mia Medlem" || payload["role"] != "member" {
		t.Errorf("role-added payload = %v, want the member, their name and 'member'", payload)
	}

	changed := assignRole(t, creator, project.Id, memberID, "viewer")
	if changed.Role != "viewer" {
		t.Errorf("Role = %q, want 'viewer'", changed.Role)
	}
	if !changed.CreatedAt.Equal(added.CreatedAt) {
		t.Errorf("CreatedAt = %s, want the assignment's original %s: a change is not a new assignment",
			changed.CreatedAt, added.CreatedAt)
	}
	payload = lastPayload(t, h, project.Id, "role-changed")
	if payload["oldRole"] != "member" || payload["newRole"] != "viewer" || payload["userId"] != memberID.String() {
		t.Errorf("role-changed payload = %v, want member → viewer for the member", payload)
	}

	// Assigning the role the user already holds happened to nobody: it
	// answers the assignment and writes nothing.
	before := eventTypes(t, h, project.Id)
	if again := assignRole(t, creator, project.Id, memberID, "viewer"); again.Role != "viewer" {
		t.Errorf("re-assigning the same role answered %+v, want the unchanged assignment", again)
	}
	if after := eventTypes(t, h, project.Id); len(after) != len(before) {
		t.Errorf("timeline = %v, want no entry for a role that did not change (was %v)", after, before)
	}

	if r := deleteRole(t, creator, project.Id, memberID); r.Status != http.StatusNoContent {
		t.Fatalf("remove: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := getProject(t, member, project.Id); r.Status != http.StatusNotFound {
		t.Fatalf("after the removal: status %d body %s, want 404", r.Status, r.Body)
	}
	payload = lastPayload(t, h, project.Id, "role-removed")
	if payload["userId"] != memberID.String() || payload["role"] != "viewer" {
		t.Errorf("role-removed payload = %v, want the member and the role they held", payload)
	}
	if r := deleteRole(t, creator, project.Id, memberID); r.Status != http.StatusNotFound {
		t.Errorf("removing it twice: status %d body %s, want 404", r.Status, r.Body)
	}
}

// Managers first, then everyone else, each group by display name: the list is
// read to find out who runs the project, so the answer to that is at the top.
func TestGetProjectsByIdRoles_ManagersFirstThenByDisplayName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, creatorID := signIn(t, h, "projects:create")
	setDisplayName(t, h, creatorID, "Zara Leder")
	project := createProject(t, creator, map[string]any{"code": "ROL1000"})

	for _, person := range []struct{ name, role string }{
		{"Bjørn Medlem", "member"},
		{"Anna Seer", "viewer"},
		{"Arne Leder", "manager"},
	} {
		_, id := signIn(t, h)
		setDisplayName(t, h, id, person.name)
		assignRole(t, creator, project.Id, id, person.role)
	}

	want := []string{"Arne Leder", "Zara Leder", "Anna Seer", "Bjørn Medlem"}
	if got := roleDisplayNames(listRoles(t, creator, project.Id)); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("roles = %v, want %v (managers first, then by display name)", got, want)
	}
}

// An assignment whose user the directory no longer knows still lists: the row
// is the record that the project had that person on it, and a name it cannot
// resolve is said so rather than dropped.
func TestGetProjectsByIdRoles_UnresolvableUser_ListsAsUnknownAndInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "GHOST1000"})
	ghost := uuid.New()
	addRole(t, h, project.Id, ghost, "member")

	roles := listRoles(t, creator, project.Id)
	var found *roleJSON
	for i := range roles {
		if roles[i].UserId == ghost {
			found = &roles[i]
		}
	}
	if found == nil {
		t.Fatalf("roles = %+v, want the assignment of a user the directory does not know", roles)
	}
	if found.DisplayName != "Unknown user" || found.Active {
		t.Errorf("unresolvable assignment = %+v, want 'Unknown user' and active false", *found)
	}
}

// Design §7: the role list is readable by anyone who sees the project, a
// viewer included.
func TestGetProjectsByIdRoles_Viewer_MayRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "ROLV1000"})
	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")

	if got := listRoles(t, viewer, project.Id); len(got) != 2 {
		t.Errorf("roles = %+v, want the creator and the viewer", got)
	}
}

func TestPutProjectsByIdRolesByUserId_InvalidRole_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "BADR1000"})
	_, id := signIn(t, h)

	r := putRole(t, creator, project.Id, id, "boss")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["role"]) == 0 {
		t.Errorf("errors = %v, want a message on 'role'", problem.Errors)
	}
}

func TestPutProjectsByIdRolesByUserId_UnknownUser_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "NOUSR1000"})

	r := putRole(t, creator, project.Id, uuid.New(), "member")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["userId"]) == 0 {
		t.Errorf("errors = %v, want a message on 'userId'", problem.Errors)
	}
}

// A disabled account cannot be given a project role: nothing should let a
// manager assign work to somebody who can no longer act.
func TestPutProjectsByIdRolesByUserId_DisabledUser_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "DISU1000"})
	_, id := signIn(t, h)
	disableUser(t, h, id)

	r := putRole(t, creator, project.Id, id, "member")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["userId"]) == 0 {
		t.Errorf("errors = %v, want a message on 'userId'", problem.Errors)
	}
}

// Disabling somebody who already holds a role changes nothing about the
// assignment: it lists as inactive, its role can still be changed, and it can
// still be removed. Only *adding* one is refused.
func TestProjectRoles_UserDisabledAfterAssignment_ListsInactiveAndStaysManageable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "DISA1000"})
	_, id := signIn(t, h)
	setDisplayName(t, h, id, "Dagny Deaktivert")
	assignRole(t, creator, project.Id, id, "member")
	disableUser(t, h, id)

	var listed *roleJSON
	roles := listRoles(t, creator, project.Id)
	for i := range roles {
		if roles[i].UserId == id {
			listed = &roles[i]
		}
	}
	if listed == nil {
		t.Fatalf("roles = %+v, want the disabled user's assignment", roles)
	}
	if listed.Active || listed.DisplayName != "Dagny Deaktivert" {
		t.Errorf("assignment = %+v, want the name and active false", *listed)
	}

	if changed := assignRole(t, creator, project.Id, id, "viewer"); changed.Role != "viewer" || changed.Active {
		t.Errorf("changed = %+v, want 'viewer' and active false", changed)
	}
	if r := deleteRole(t, creator, project.Id, id); r.Status != http.StatusNoContent {
		t.Errorf("remove: status %d body %s, want 204", r.Status, r.Body)
	}
}

// A member sees the project and its people; managing them is the manager's
// (D7: a caller who can see the project but may not manage it gets 403).
func TestProjectRoles_Member_MayReadButNotManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "MEMR1000"})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")
	_, otherID := signIn(t, h)

	if got := listRoles(t, member, project.Id); len(got) != 2 {
		t.Errorf("roles = %+v, want the member to read the list", got)
	}
	if r := putRole(t, member, project.Id, otherID, "member"); r.Status != http.StatusForbidden {
		t.Errorf("member assigning: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := deleteRole(t, member, project.Id, memberID); r.Status != http.StatusForbidden {
		t.Errorf("member removing: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := member.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/assignable-users", project.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("member searching: status %d body %s, want 403", r.Status, r.Body)
	}
}

// projects:view-all sees every project but manages none of them (design §5):
// the same 403 a member gets, from a caller who reached the project through a
// global permission rather than a role.
func TestProjectRoles_ViewAllWithoutARole_MayReadButNotManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "VALL1000"})
	viewer, _ := signIn(t, h, "projects:view-all")
	_, otherID := signIn(t, h)

	if got := roleDisplayNames(listRoles(t, viewer, project.Id)); len(got) != 1 {
		t.Errorf("roles = %v, want view-all to read the list", got)
	}
	if r := putRole(t, viewer, project.Id, otherID, "member"); r.Status != http.StatusForbidden {
		t.Errorf("view-all assigning: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := deleteRole(t, viewer, project.Id, otherID); r.Status != http.StatusForbidden {
		t.Errorf("view-all removing: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/assignable-users", project.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("view-all searching: status %d body %s, want 403", r.Status, r.Body)
	}
}

// D7: an outsider cannot tell any of the four operations' projects from one
// that does not exist.
func TestProjectRoles_Outsider_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "OUTP1000"})
	outsider, outsiderID := signIn(t, h)

	cases := []struct {
		name     string
		existing func() *modtest.Response
		unknown  func() *modtest.Response
	}{
		{"list roles",
			func() *modtest.Response { return outsider.Do(http.MethodGet, rolesPath(project.Id), nil) },
			func() *modtest.Response { return outsider.Do(http.MethodGet, rolesPath(999999), nil) }},
		{"assign",
			func() *modtest.Response { return putRole(t, outsider, project.Id, outsiderID, "member") },
			func() *modtest.Response { return putRole(t, outsider, 999999, outsiderID, "member") }},
		{"remove",
			func() *modtest.Response { return deleteRole(t, outsider, project.Id, outsiderID) },
			func() *modtest.Response { return deleteRole(t, outsider, 999999, outsiderID) }},
		{"assignable users",
			func() *modtest.Response {
				return outsider.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/assignable-users", project.Id), nil)
			},
			func() *modtest.Response {
				return outsider.Do(http.MethodGet, "/api/v1/projects/999999/assignable-users", nil)
			}},
	}
	for _, c := range cases {
		existing, unknown := c.existing(), c.unknown()
		if existing.Status != http.StatusNotFound {
			t.Errorf("%s on an existing project: status %d body %s, want 404", c.name, existing.Status, existing.Body)
		}
		if unknown.Status != http.StatusNotFound {
			t.Errorf("%s on an unknown id: status %d body %s, want 404", c.name, unknown.Status, unknown.Body)
		}
		if string(existing.Body) != string(unknown.Body) {
			t.Errorf("%s: outsider's 404 body %q differs from an unknown id's %q", c.name, existing.Body, unknown.Body)
		}
	}
}

// A user id that is not a UUID never reaches a handler: the generated decoder
// refuses it.
func TestProjectRoles_MalformedUserId_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "UUID1000"})
	path := fmt.Sprintf("/api/v1/projects/%d/roles/not-a-uuid", project.Id)

	if r := creator.Do(http.MethodPut, path, map[string]any{"role": "member"}); r.Status != http.StatusBadRequest {
		t.Errorf("assign: status %d body %s, want 400", r.Status, r.Body)
	}
	if r := creator.Do(http.MethodDelete, path, nil); r.Status != http.StatusBadRequest {
		t.Errorf("remove: status %d body %s, want 400", r.Status, r.Body)
	}
}

// D11's search: active users this project does not already have, matched by
// display name.
func TestGetProjectsByIdAssignableUsers_ExcludesAssignedAndDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, creatorID := signIn(t, h, "projects:create")
	setDisplayName(t, h, creatorID, "Kandidat Leder")
	project := createProject(t, creator, map[string]any{"code": "CAND1000"})

	_, freeID := signIn(t, h)
	setDisplayName(t, h, freeID, "Kandidat Ledig")
	_, assignedID := signIn(t, h)
	setDisplayName(t, h, assignedID, "Kandidat Tildelt")
	_, disabledID := signIn(t, h)
	setDisplayName(t, h, disabledID, "Kandidat Deaktivert")
	disableUser(t, h, disabledID)
	assignRole(t, creator, project.Id, assignedID, "member")

	want := []string{"Kandidat Ledig"}
	if got := displayNames(assignableUsers(t, creator, project.Id, "Kandidat")); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("search 'Kandidat' = %v, want %v: the manager and the member are assigned, the third is disabled", got, want)
	}
	if got := displayNames(assignableUsers(t, creator, project.Id, "Tildelt")); len(got) != 0 {
		t.Errorf("search 'Tildelt' = %v, want none: that user already holds a role", got)
	}
	// An empty search is the first page of everybody assignable, not an empty
	// answer: the picker opens before anything is typed.
	empty := assignableUsers(t, creator, project.Id, "")
	if !contains(displayNames(empty), "Kandidat Ledig") {
		t.Errorf("empty search = %v, want it to include the assignable user", displayNames(empty))
	}
	for _, unwanted := range []string{"Kandidat Leder", "Kandidat Tildelt", "Kandidat Deaktivert"} {
		if contains(displayNames(empty), unwanted) {
			t.Errorf("empty search = %v, want %q left out", displayNames(empty), unwanted)
		}
	}
}

// Two managers removing the same person at once: exactly one 204, exactly one
// role-removed. A second entry would record a removal that did not happen.
//
// Unlike the concurrency tests in projects_concurrency_test.go, this does not
// reproduce the interleaving it names: the window between the locked read and
// the delete is microseconds against a request preamble of milliseconds, and
// the assertions hold either way (checked by re-running it against the
// pre-lock implementation). What it pins is the invariant; what guarantees it
// is the row lock and the delete's own row count.
func TestDeleteProjectsByIdRolesByUserId_ConcurrentRemovals_RemoveItOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "RACED1000"})
	_, memberID := signIn(t, h)
	setDisplayName(t, h, memberID, "Rita Racer")
	assignRole(t, creator, project.Id, memberID, "member")

	responses := race(
		func() *modtest.Response { return deleteRole(t, creator, project.Id, memberID) },
		func() *modtest.Response { return deleteRole(t, creator, project.Id, memberID) },
	)
	statuses := map[int]int{}
	for _, r := range responses {
		statuses[r.Status]++
	}
	if statuses[http.StatusNoContent] != 1 || statuses[http.StatusNotFound] != 1 {
		t.Errorf("statuses = %v, want exactly one 204 and one 404", statuses)
	}
	if got := payloads(t, h, project.Id, "role-removed"); len(got) != 1 {
		t.Errorf("role-removed entries = %v, want exactly one", got)
	}
}

// Two managers changing the same person's role at once. Each change is
// recorded against the role that was actually there when it was applied, so
// the two entries chain — the second's oldRole is the first's newRole — and
// only one of them can have started from the role the user held before either
// request arrived. The same caveat as the removal race above applies: this
// pins the invariant rather than reproducing the interleaving.
func TestPutProjectsByIdRolesByUserId_ConcurrentChanges_ChainTheTimeline(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "RACEP1000"})
	_, memberID := signIn(t, h)
	setDisplayName(t, h, memberID, "Rolf Racer")
	assignRole(t, creator, project.Id, memberID, "member")

	responses := race(
		func() *modtest.Response { return putRole(t, creator, project.Id, memberID, "viewer") },
		func() *modtest.Response { return putRole(t, creator, project.Id, memberID, "manager") },
	)
	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Fatalf("change %d: status %d body %s, want 200", i, r.Status, r.Body)
		}
	}

	entries := payloads(t, h, project.Id, "role-changed")
	if len(entries) != 2 {
		t.Fatalf("role-changed entries = %v, want exactly two", entries)
	}
	if entries[0]["oldRole"] != "member" {
		t.Errorf("first entry = %v, want it to start from the role the user held", entries[0])
	}
	if entries[1]["oldRole"] == "member" {
		t.Errorf("entries = %v, want the second change recorded against what the first left behind, not against 'member'", entries)
	}
	if entries[0]["newRole"] != entries[1]["oldRole"] {
		t.Errorf("entries = %v, want the second to continue from the first", entries)
	}
	roles := listRoles(t, creator, project.Id)
	for _, r := range roles {
		if r.UserId == memberID && r.Role != entries[1]["newRole"] {
			t.Errorf("stored role = %q, want the last entry's newRole %v", r.Role, entries[1]["newRole"])
		}
	}
}

// D6: there is no last-manager rule. A manager may step off the project they
// run, and manage-all is what puts somebody back on it.
func TestDeleteProjectsByIdRolesByUserId_LastManagerMayRemoveThemself(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, creatorID := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "LAST1000"})

	if r := deleteRole(t, creator, project.Id, creatorID); r.Status != http.StatusNoContent {
		t.Fatalf("manager removing themself: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := getProject(t, creator, project.Id); r.Status != http.StatusNotFound {
		t.Errorf("after stepping off: status %d body %s, want 404", r.Status, r.Body)
	}

	admin, _ := signIn(t, h, "projects:manage-all")
	_, newManagerID := signIn(t, h)
	setDisplayName(t, h, newManagerID, "Ny Leder")
	if assigned := assignRole(t, admin, project.Id, newManagerID, "manager"); assigned.Role != "manager" {
		t.Errorf("assigned = %+v, want manage-all to appoint a new manager", assigned)
	}
	if got := roleDisplayNames(listRoles(t, admin, project.Id)); fmt.Sprint(got) != fmt.Sprint([]string{"Ny Leder"}) {
		t.Errorf("roles = %v, want only the new manager", got)
	}
}
