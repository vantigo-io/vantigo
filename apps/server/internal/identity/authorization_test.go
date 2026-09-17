package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const accessPath = "/api/v1/identity/access"

// The customers keys the RBAC tests' catalog adds beside identity:manage,
// as .NET's tests used customers:view and customers:create
// (TS/Integration/IdentityRbacIntegrationTests.cs:20-22).
var (
	customersView   = contracts.Permission{Key: "customers:view", Display: "View customers", Description: "Read customer records.", Category: "Customers", Delegable: true}
	customersCreate = contracts.Permission{Key: "customers:create", Display: "Create customers", Description: "Create customer records.", Category: "Customers", Delegable: true}
)

// rbacHarness is a harness whose catalog also holds customers:view and
// customers:create, with its first Owner signed in.
func rbacHarness(t *testing.T, opts ...harnessOption) (*harness, *client, uuid.UUID) {
	t.Helper()
	h := newHarness(t, append([]harnessOption{withPermissions(customersView, customersCreate)}, opts...)...)
	owner, ownerID := h.bootstrapOwner(t)
	return h, owner, ownerID
}

// createdRole is RoleCreateResponse as a test reads it.
type createdRole struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Permissions []string  `json:"permissions"`
}

// createRole creates the custom role name with keys as c, which must
// succeed; its display name is its name.
func createRole(t testing.TB, c *client, name string, keys ...string) createdRole {
	t.Helper()
	r := c.do(http.MethodPost, accessPath+"/roles", roleBody(name, "Integration role", keys, nil))
	if r.status != http.StatusCreated {
		t.Fatalf("create role %s: status %d body %s", name, r.status, r.body)
	}
	var role createdRole
	r.json(&role)
	return role
}

// roleBody is a RoleUpsertRequest for name, its display name, with stamp
// as the concurrency stamp when it is not nil.
func roleBody(name, description string, keys []string, stamp *string) map[string]any {
	if keys == nil {
		keys = []string{}
	}
	body := map[string]any{"name": name, "displayName": name, "description": description, "permissionKeys": keys}
	if stamp != nil {
		body["concurrencyStamp"] = *stamp
	}
	return body
}

// updateRole replaces role id's permissions with keys, keeping its name,
// under stamp.
func updateRole(c *client, id uuid.UUID, name, stamp string, keys ...string) *resp {
	return c.do(http.MethodPut, accessPath+"/roles/"+id.String(), roleBody(name, "Integration role", keys, &stamp))
}

// deleteRole deletes role id under stamp.
func deleteRole(c *client, id uuid.UUID, stamp string) *resp {
	return c.do(http.MethodDelete, accessPath+"/roles/"+id.String(), map[string]any{"concurrencyStamp": stamp})
}

// assignRoles replaces userID's custom roles with roleIDs under stamp.
func assignRoles(c *client, userID uuid.UUID, stamp string, roleIDs ...uuid.UUID) *resp {
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	return c.do(http.MethodPut, accessPath+"/users/"+userID.String()+"/roles", map[string]any{"roleIds": roleIDs, "concurrencyStamp": stamp})
}

// version is the version column of the identity table row id, as the API
// shows it.
func (h *harness) version(t testing.TB, table string, id uuid.UUID) string {
	t.Helper()
	var v string
	if err := h.pool.QueryRow(context.Background(), `SELECT version::text FROM identity.`+table+` WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatalf("harness: %s %s version: %v", table, id, err)
	}
	return v
}

// roleNames is every role userID holds directly, by name.
func (h *harness) roleNames(t testing.TB, userID uuid.UUID) []string {
	t.Helper()
	var names []string
	if err := h.pool.QueryRow(context.Background(), `
		SELECT coalesce(array_agg(r.name ORDER BY r.name COLLATE "C"), '{}') FROM identity.user_roles ur
		JOIN identity.roles r ON r.id = ur.role_id WHERE ur.user_id = $1`, userID).Scan(&names); err != nil {
		t.Fatalf("harness: roles of %s: %v", userID, err)
	}
	return names
}

// rolePermissions is role id's permission keys, in ordinal order.
func (h *harness) rolePermissions(t testing.TB, id uuid.UUID) []string {
	t.Helper()
	var keys []string
	if err := h.pool.QueryRow(context.Background(), `
		SELECT coalesce(array_agg(permission_key ORDER BY permission_key COLLATE "C"), '{}') FROM identity.role_permissions
		WHERE role_id = $1`, id).Scan(&keys); err != nil {
		t.Fatalf("harness: permissions of %s: %v", id, err)
	}
	return keys
}

// permitted reports whether c's session is granted key: the check every
// module's router makes of identity's Access on each request to a
// permission-guarded operation, with c's session cookie.
func (h *harness) permitted(t testing.TB, c *client, key string) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, h.url+"/api/v1/customers", nil)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: c.cookie(identity.SessionCookieName)})
	_, err := h.access.Check(req, contracts.Rule{Kind: contracts.RulePermission, Names: []string{key}})
	if errors.Is(err, contracts.ErrForbidden) {
		return false
	}
	if err != nil {
		t.Fatalf("permission %s: %v", key, err)
	}
	return true
}

// mapRoleToGroup maps roleID to a new active local access group with
// members as forced members, directly in the database (access groups get
// their endpoints later), and returns the group's id.
func (h *harness) mapRoleToGroup(t testing.TB, roleID uuid.UUID, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.access_groups (id, display_name, source, version, created_at, updated_at)
	        VALUES ($1, $2, 'local', $3, $4, $4)`, id, "group-"+id.String(), uuid.New(), h.now())
	h.exec(t, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, 'local')`, id, roleID)
	for _, m := range members {
		h.exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, membership_override)
		        VALUES ($1, $2, 'local', 'force_member')`, id, m)
	}
	return id
}

// wantFlat fails t unless r is status with exactly the flat
// CodeMessageError {code, message}.
func wantFlat(t testing.TB, what string, r *resp, status int, code, message string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.body, &body); err != nil || r.status != status || len(body) != 2 || body["code"] != code || body["message"] != message {
		t.Errorf("%s: status %d body %s, want %d {code %q, message %q}", what, r.status, r.body, status, code, message)
	}
}

// userClient creates a User as owner and signs them in with password,
// returning the new client and the user's id.
func userClient(t testing.TB, h *harness, owner *client, email string) (*client, uuid.UUID) {
	t.Helper()
	id := h.createUser(t, owner, email, identity.RoleUser)
	return h.login(t, email, userPassword), id
}

// Ported from IdentityRbacIntegrationTests.FreshBootstrapCreatesNormalizedProtectedOwnerAndUserRoles.
// The Owner bootstrap created also holds SystemAdmin, as .NET's factory's
// did. Extended: GET /access/roles shows the
// two built-in roles it does not hide as they are stored.
func TestRbac_BuiltInRolesAreNormalizedAndProtected(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)

	rows, err := h.pool.Query(context.Background(), `SELECT id, name, normalized_name, is_system, is_built_in FROM identity.roles ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([5]any, error) {
		var id uuid.UUID
		var name, normalized string
		var system, builtIn bool
		err := row.Scan(&id, &name, &normalized, &system, &builtIn)
		return [5]any{id, name, normalized, system, builtIn}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 3 {
		t.Fatalf("roles %v, want the three built-in roles", roles)
	}
	for _, r := range roles {
		if r[2] != strings.ToUpper(r[1].(string)) || r[3] != true || r[4] != true {
			t.Errorf("role %v: want its name upper-cased as its normalized name, system and built in", r)
		}
	}
	for _, id := range []uuid.UUID{identity.RoleOwnerID, identity.RoleSystemAdminID} {
		var holders []uuid.UUID
		if err := h.pool.QueryRow(context.Background(), `SELECT array_agg(user_id) FROM identity.user_roles WHERE role_id = $1`, id).Scan(&holders); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(holders, []uuid.UUID{ownerID}) {
			t.Errorf("role %s is held by %v, want only the bootstrap Owner", id, holders)
		}
	}

	r := owner.do(http.MethodGet, accessPath+"/roles", nil)
	var listed []map[string]any
	r.json(&listed)
	if r.status != http.StatusOK || len(listed) != 2 {
		t.Fatalf("GET /access/roles: status %d body %s, want Owner and User", r.status, r.body)
	}
	for i, id := range []uuid.UUID{identity.RoleOwnerID, identity.RoleUserID} {
		name := []string{identity.RoleOwner, identity.RoleUser}[i]
		want := map[string]any{
			"id": id.String(), "name": name, "normalizedName": strings.ToUpper(name), "isSystem": true, "isBuiltIn": true,
			"stewardUserId": nil, "version": h.version(t, "roles", id), "permissions": []any{},
		}
		for k, v := range want {
			if got := listed[i][k]; !jsonEqual(got, v) {
				t.Errorf("role %d %s = %v, want %v", i, k, got, v)
			}
		}
	}
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// Ported from IdentityRbacIntegrationTests.OwnerIsImplicitlyAllowedAndUserIsDeniedUntilAnAdditiveRoleIsAssigned.
// The permission is checked as every module's router checks it, with a
// real session. The assignment ended the user's sessions (the security
// stamp rule), so the user signs in again before the role counts.
func TestRbac_OwnerHoldsEveryKeyAndAUserOnlyAnAssignedRolesKeys(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	user, userID := userClient(t, h, owner, "additive@example.test")

	if !h.permitted(t, owner, "identity:manage") {
		t.Error("the Owner is denied identity:manage, which no role of theirs carries")
	}
	if h.permitted(t, user, "identity:manage") {
		t.Error("the user is allowed identity:manage without a role that carries it")
	}

	role := createRole(t, owner, "rbac-additive", "identity:manage")
	if r := assignRoles(owner, userID, h.version(t, "users", userID), role.ID); r.status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}
	user = h.login(t, "additive@example.test", userPassword)
	if !h.permitted(t, user, "identity:manage") {
		t.Error("the user is denied identity:manage with a role that carries it")
	}
	if got := h.roleNames(t, userID); !slices.Equal(got, []string{identity.RoleUser, "rbac-additive"}) {
		t.Errorf("the user holds %v, want User and rbac-additive", got)
	}
}

// Ported from IdentityRbacIntegrationTests.PermissionReplacementAndAssignmentRevocationApplyImmediately.
// The user's very next permission check after each change is denied.
// Extended: /access/me shows the key before the replacement and none after,
// on the same session, which the role edit did not end.
func TestRbac_PermissionReplacementAndAssignmentRemovalApplyAtOnce(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	_, userID := userClient(t, h, owner, "revoke@example.test")
	role := createRole(t, owner, "rbac-revoke", "identity:manage")
	if r := assignRoles(owner, userID, h.version(t, "users", userID), role.ID); r.status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}
	user := h.login(t, "revoke@example.test", userPassword)
	if !h.permitted(t, user, "identity:manage") {
		t.Fatal("the user is denied identity:manage with the role")
	}
	permissions := func() []string {
		t.Helper()
		r := user.do(http.MethodGet, accessPath+"/me", nil)
		var me struct {
			Permissions []string `json:"permissions"`
		}
		r.json(&me)
		if r.status != http.StatusOK {
			t.Fatalf("GET /access/me: status %d body %s", r.status, r.body)
		}
		return me.Permissions
	}
	if got := permissions(); !slices.Equal(got, []string{"identity:manage"}) {
		t.Errorf("/access/me permissions %v before the replacement", got)
	}

	if r := updateRole(owner, role.ID, role.Name, h.version(t, "roles", role.ID)); r.status != http.StatusOK {
		t.Fatalf("replace permissions: status %d body %s", r.status, r.body)
	}
	if h.permitted(t, user, "identity:manage") {
		t.Error("the user is still allowed identity:manage after the role lost it")
	}
	if got := permissions(); len(got) != 0 {
		t.Errorf("/access/me permissions %v after the replacement, want none", got)
	}

	if r := updateRole(owner, role.ID, role.Name, h.version(t, "roles", role.ID), "identity:manage"); r.status != http.StatusOK {
		t.Fatalf("restore the permission: status %d body %s", r.status, r.body)
	}
	if !h.permitted(t, user, "identity:manage") {
		t.Fatal("the user is denied identity:manage once the role has it again")
	}
	if r := assignRoles(owner, userID, h.version(t, "users", userID)); r.status != http.StatusOK {
		t.Fatalf("remove the assignment: status %d body %s", r.status, r.body)
	}
	if h.permitted(t, h.login(t, "revoke@example.test", userPassword), "identity:manage") {
		t.Error("the user is still allowed identity:manage after losing the role")
	}
	if got := h.roleNames(t, userID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("the user holds %v, want User alone", got)
	}
}

// assignment is AssignRolesResponse as a test reads it.
type assignment struct {
	UserID  uuid.UUID   `json:"userId"`
	RoleIDs []uuid.UUID `json:"roleIds"`
	Version string      `json:"version"`
}

// Ported from IdentityRbacIntegrationTests.AssignmentPreservesProtectedSystemRolesAndReturnsARevision.
// Extended: the version is the user's new one, and the role ids are listed
// as every response lists roles, User first.
func TestRbac_AssignmentKeepsBuiltInRolesAndReturnsANewVersion(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	_, userID := userClient(t, h, owner, "preserve@example.test")
	role := createRole(t, owner, "rbac-system-preserve")
	before := h.version(t, "users", userID)

	r := assignRoles(owner, userID, before, role.ID)
	var body assignment
	r.json(&body)
	if r.status != http.StatusOK || body.UserID != userID || body.Version == "" || body.Version == before || body.Version != h.version(t, "users", userID) {
		t.Fatalf("assign: status %d body %s, want the user's new version (it was %s)", r.status, r.body, before)
	}
	if !slices.Equal(body.RoleIDs, []uuid.UUID{identity.RoleUserID, role.ID}) {
		t.Errorf("roleIds %v, want User then the role", body.RoleIDs)
	}
	if got := h.roleNames(t, userID); !slices.Equal(got, []string{identity.RoleUser, "rbac-system-preserve"}) {
		t.Errorf("the user holds %v", got)
	}

	if r := assignRoles(owner, userID, body.Version, role.ID); r.status != http.StatusOK {
		t.Errorf("a second assignment with the returned version: status %d body %s", r.status, r.body)
	}
}

// updatedRole is RoleUpdateResponse as a test reads it.
type updatedRole struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Permissions []string  `json:"permissions"`
}

// Ported from IdentityRbacIntegrationTests.RoleMutationsRequireCurrentVersionAndReturnAReplacementVersion.
// Extended to PUT …/permissions and DELETE, and to a missing stamp.
func TestRbac_RoleMutationsRequireTheCurrentVersion(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "rbac-version")

	wantFlat(t, "a stale version", updateRole(owner, role.ID, role.Name, "stale-version"),
		http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	current := h.version(t, "roles", role.ID)
	if current != role.Version {
		t.Fatalf("the created role's version %s, stored %s", role.Version, current)
	}
	r := updateRole(owner, role.ID, role.Name, current)
	var updated updatedRole
	r.json(&updated)
	if r.status != http.StatusOK || updated.Version == current || updated.Version != h.version(t, "roles", role.ID) {
		t.Fatalf("update: status %d body %s, want a new version replacing %s", r.status, r.body, current)
	}

	permissionsPath := accessPath + "/roles/" + role.ID.String() + "/permissions"
	wantFlat(t, "permissions under the replaced version", owner.do(http.MethodPut, permissionsPath, roleBody(role.Name, "Integration role", []string{"customers:view"}, &current)),
		http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	wantFlat(t, "permissions without a version", owner.do(http.MethodPut, permissionsPath, roleBody(role.Name, "Integration role", []string{"customers:view"}, nil)),
		http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	r = owner.do(http.MethodPut, permissionsPath, roleBody(role.Name, "Integration role", []string{"customers:view"}, &updated.Version))
	var replaced updatedRole
	r.json(&replaced)
	if r.status != http.StatusOK || !slices.Equal(replaced.Permissions, []string{"customers:view"}) || !slices.Equal(h.rolePermissions(t, role.ID), []string{"customers:view"}) {
		t.Fatalf("permissions: status %d body %s", r.status, r.body)
	}

	wantFlat(t, "delete under a replaced version", deleteRole(owner, role.ID, updated.Version),
		http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	if r := deleteRole(owner, role.ID, replaced.Version); r.status != http.StatusNoContent || len(r.body) != 0 {
		t.Fatalf("delete: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1`, role.ID); n != 0 {
		t.Error("the deleted role is still there")
	}
}

// Ported from IdentityRbacIntegrationTests.AuthorizationMutationConflictSurfacesAreDeterministicAcrossRoleAssignmentAndDelegationApis.
// Extended: the codes and messages, and a duplicate that differs only in
// case or is a built-in's name.
func TestRbac_ConflictAnswersAreDeterministic(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "rbac-conflict-map")

	for _, name := range []string{role.Name, "RBAC-Conflict-Map", "owner"} {
		wantFlat(t, "duplicate "+name, owner.do(http.MethodPost, accessPath+"/roles", roleBody(name, "duplicate", nil, nil)),
			http.StatusConflict, "role_exists", "The normalized role name is already in use.")
	}
	_, targetID := userClient(t, h, owner, "conflict@example.test")
	wantFlat(t, "a stale user version", assignRoles(owner, targetID, "stale-user-version", role.ID),
		http.StatusConflict, "user_conflict", "The user changed concurrently; refresh its version.")
	d := delegate(t, h, owner, targetID, nil)
	wantFlat(t, "a stale delegation version", updateDelegation(owner, d.ID, "stale-delegation-version", delegationBody(targetID, h.now().Add(2*time.Hour), nil, nil)),
		http.StatusConflict, "delegation_conflict", "The delegation changed concurrently.")
	wantFlat(t, "a stale role version", deleteRole(owner, role.ID, "stale-role-version"),
		http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE NOT is_built_in`); n != 1 {
		t.Errorf("%d custom roles, want the one", n)
	}
}

// Ported from IdentityRbacIntegrationTests.SystemRoleMutationAndDeletionAreRejected,
// extended to all three built-in roles.
func TestRbac_BuiltInRolesCannotBeChangedDeletedOrAssigned(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	_, targetID := userClient(t, h, owner, "system-target@example.test")

	for name, id := range map[string]uuid.UUID{
		identity.RoleSystemAdmin: identity.RoleSystemAdminID, identity.RoleOwner: identity.RoleOwnerID, identity.RoleUser: identity.RoleUserID,
	} {
		version := h.version(t, "roles", id)
		wantFlat(t, "update "+name, updateRole(owner, id, name+"-tampered", version),
			http.StatusConflict, "system_role", "Protected system and built-in roles cannot be changed.")
		wantFlat(t, "delete "+name, deleteRole(owner, id, version),
			http.StatusConflict, "system_role", "Protected system and built-in roles cannot be deleted.")
		wantFlat(t, "assign "+name, assignRoles(owner, targetID, h.version(t, "users", targetID), id),
			http.StatusBadRequest, "invalid_roles", "Only custom application roles may be assigned by this route.")
		if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1 AND name = $2 AND version::text = $3`, id, name, version); n != 1 {
			t.Errorf("role %s changed", name)
		}
	}
	wantFlat(t, "assign an unknown role", assignRoles(owner, targetID, h.version(t, "users", targetID), uuid.New()),
		http.StatusBadRequest, "invalid_roles", "Only custom application roles may be assigned by this route.")
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("the target holds %v", got)
	}
}

// accessOperation is one authorization management operation with a request
// the contract accepts.
type accessOperation struct {
	method, path string
	body         any
}

func accessOperations() []accessOperation {
	id := uuid.New().String()
	stamp := "stamp"
	role := roleBody("x", "x", nil, &stamp)
	return []accessOperation{
		{http.MethodGet, accessPath + "/catalog", nil},
		{http.MethodGet, accessPath + "/roles", nil},
		{http.MethodPost, accessPath + "/roles", roleBody("x", "x", nil, nil)},
		{http.MethodPut, accessPath + "/roles/" + id, role},
		{http.MethodDelete, accessPath + "/roles/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodPut, accessPath + "/roles/" + id + "/permissions", role},
		{http.MethodGet, accessPath + "/users", nil},
		{http.MethodGet, accessPath + "/users/" + id, nil},
		{http.MethodPut, accessPath + "/users/" + id + "/roles", map[string]any{"roleIds": []string{}, "concurrencyStamp": stamp}},
		{http.MethodGet, accessPath + "/audit", nil},
		{http.MethodGet, accessPath + "/delegations", nil},
		{http.MethodPost, accessPath + "/delegations", map[string]any{"permissionKeys": []string{}, "stewardedRoleIds": []string{}}},
		{http.MethodPut, accessPath + "/delegations/" + id, map[string]any{"permissionKeys": []string{}, "stewardedRoleIds": []string{}, "concurrencyStamp": stamp}},
		{http.MethodPost, accessPath + "/delegations/" + id + "/revoke", map[string]any{"concurrencyStamp": stamp}},
		{http.MethodGet, accessPath + "/groups", nil},
		{http.MethodPost, accessPath + "/groups", map[string]any{"displayName": "x", "isActive": true}},
		{http.MethodGet, accessPath + "/groups/" + id, nil},
		{http.MethodPut, accessPath + "/groups/" + id, map[string]any{"displayName": "x", "concurrencyStamp": stamp}},
		{http.MethodDelete, accessPath + "/groups/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodPost, accessPath + "/groups/" + id + "/members/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodPut, accessPath + "/groups/" + id + "/members/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodDelete, accessPath + "/groups/" + id + "/members/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodPost, accessPath + "/groups/" + id + "/role-mappings/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodPut, accessPath + "/groups/" + id + "/role-mappings/" + id, map[string]any{"concurrencyStamp": stamp}},
		{http.MethodDelete, accessPath + "/groups/" + id + "/role-mappings/" + id, map[string]any{"concurrencyStamp": stamp}},
	}
}

// ownerOnly reports whether op is OwnerManagement, which a delegate does
// not pass: the audit trail, the delegations and the access groups.
func (op accessOperation) ownerOnly() bool {
	return strings.HasSuffix(op.path, "/audit") || strings.Contains(op.path, "/delegations") || strings.Contains(op.path, "/groups")
}

// Ported from IdentityRbacIntegrationTests.NonOwnerAndAnonymousCannotUseManagementSurface,
// extended to every operation (and /access/me, which only needs a session).
// Extended: while Owners must use MFA, an Owner without it is refused too
// (AuthorizationManagement and OwnerManagement carry the MFA requirement),
// and is admitted once their session has verified a second factor.
func TestRbac_AnonymousCallersAndNonOwnersCannotManage(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	user, _ := userClient(t, h, owner, "standard@example.test")
	anonymous := h.client(t)

	for _, op := range accessOperations() {
		if r := anonymous.do(op.method, op.path, op.body); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
			t.Errorf("anonymous %s %s: status %d body %s", op.method, op.path, r.status, r.body)
		}
		if r := user.do(op.method, op.path, op.body); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("user %s %s: status %d body %s", op.method, op.path, r.status, r.body)
		}
	}
	if r := anonymous.do(http.MethodGet, accessPath+"/me", nil); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous /access/me: status %d", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE NOT is_built_in`); n != 0 {
		t.Errorf("%d custom roles were created", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations`); n != 0 {
		t.Errorf("%d delegations were created", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups`); n != 0 {
		t.Errorf("%d access groups were created", n)
	}
	delegateID := h.createUser(t, owner, "standard-delegate@example.test", identity.RoleUser)
	delegate(t, h, owner, delegateID, nil)
	delegated := h.login(t, "standard-delegate@example.test", userPassword)
	for _, op := range accessOperations() {
		if r := delegated.do(op.method, op.path, op.body); op.ownerOnly() && (r.status != http.StatusForbidden || r.code() != "forbidden") {
			t.Errorf("delegate %s %s: status %d body %s", op.method, op.path, r.status, r.body)
		}
	}

	t.Run("an Owner without MFA while Owners must use it", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
		owner, _ := h.bootstrapOwner(t)
		for _, path := range []string{"/roles", "/audit"} {
			if r := owner.do(http.MethodGet, accessPath+path, nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
				t.Errorf("%s without MFA: status %d body %s", path, r.status, r.body)
			}
		}
		h.enrollTOTP(t, owner, ownerPassword)
		for _, path := range []string{"/roles", "/audit"} {
			if r := owner.do(http.MethodGet, accessPath+path, nil); r.status != http.StatusOK {
				t.Errorf("%s with MFA: status %d body %s", path, r.status, r.body)
			}
		}
	})
}

// Ported from IdentityRbacIntegrationTests.AccessMeAndUserDirectoryExposeSafeManagementProjections.
// Extended: each projection's fields exactly.
func TestAccessMe_AndTheDirectoryExposeSafeProjections(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	ordinary, ordinaryID := userClient(t, h, owner, "ordinary@example.test")

	r := ordinary.do(http.MethodGet, accessPath+"/me", nil)
	var me map[string]json.RawMessage
	r.json(&me)
	want := map[string]string{
		"userId": `"` + ordinaryID.String() + `"`, "roles": `["User"]`, "roleIds": `["` + identity.RoleUserID.String() + `"]`,
		"permissions": `[]`, "version": `"` + h.version(t, "users", ordinaryID) + `"`, "canManageAuthorization": `false`, "administrationScope": `null`,
	}
	if r.status != http.StatusOK || len(me) != len(want) {
		t.Fatalf("ordinary /access/me: status %d body %s", r.status, r.body)
	}
	for k, v := range want {
		if string(me[k]) != v {
			t.Errorf("ordinary /access/me %s = %s, want %s", k, me[k], v)
		}
	}
	if r := ordinary.do(http.MethodGet, accessPath+"/users", nil); r.status != http.StatusForbidden {
		t.Errorf("ordinary /access/users: status %d", r.status)
	}

	r = owner.do(http.MethodGet, accessPath+"/me", nil)
	me = nil
	r.json(&me)
	if r.status != http.StatusOK || string(me["administrationScope"]) != `{"delegationScopes":[],"isOwner":true}` ||
		string(me["permissions"]) != `["*"]` || string(me["canManageAuthorization"]) != `true` || string(me["userId"]) != `"`+ownerID.String()+`"` {
		t.Errorf("owner /access/me: status %d body %s", r.status, r.body)
	}

	r = owner.do(http.MethodGet, accessPath+"/users", nil)
	var users []map[string]json.RawMessage
	r.json(&users)
	if r.status != http.StatusOK {
		t.Fatalf("/access/users: status %d body %s", r.status, r.body)
	}
	found := false
	for _, u := range users {
		keys := slices.Sorted(func(yield func(string) bool) {
			for k := range u {
				if !yield(k) {
					return
				}
			}
		})
		if !slices.Equal(keys, []string{"active", "displayName", "email", "id", "isDisabled", "roles", "version"}) {
			t.Errorf("a directory entry has the fields %v", keys)
		}
		found = found || string(u["id"]) == `"`+ordinaryID.String()+`"`
	}
	if !found {
		t.Errorf("the directory %s does not list the ordinary user", r.body)
	}
}

// TestAccessMe_LeavesOutGroupRolesThatStillGrantAccess pins the spec's
// divergence-in-kind that .NET had: /access/me projects direct roles only
// (EA/AuthorizationManagementEndpoints.cs:529-543), while the live
// permission check counts a role an access group grants.
func TestAccessMe_LeavesOutGroupRolesThatStillGrantAccess(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	user, userID := userClient(t, h, owner, "grouped@example.test")
	role := createRole(t, owner, "group-granted", "customers:view")
	h.mapRoleToGroup(t, role.ID, userID)

	if !h.permitted(t, user, "customers:view") {
		t.Error("the group's role does not grant customers:view")
	}
	r := user.do(http.MethodGet, accessPath+"/me", nil)
	var me struct {
		Roles       []string `json:"roles"`
		Permissions []string `json:"permissions"`
	}
	r.json(&me)
	if r.status != http.StatusOK || !slices.Equal(me.Roles, []string{identity.RoleUser}) || len(me.Permissions) != 0 {
		t.Errorf("/access/me: status %d body %s, want the direct User role and no permissions", r.status, r.body)
	}
}

// auditEvent is AuthorizationAuditEvent as a test reads it.
type auditEvent struct {
	ID               int64      `json:"id"`
	ActorUserID      *uuid.UUID `json:"actorUserId"`
	TargetUserID     *uuid.UUID `json:"targetUserId"`
	TargetRoleID     *uuid.UUID `json:"targetRoleId"`
	Action           string     `json:"action"`
	Details          string     `json:"details"`
	BeforeJSON       string     `json:"beforeJson"`
	AfterJSON        string     `json:"afterJson"`
	CorrelationID    string     `json:"correlationId"`
	MfaAuthenticated bool       `json:"mfaAuthenticated"`
	OccurredAt       time.Time  `json:"occurredAt"`
}

func auditTrail(t testing.TB, c *client) []auditEvent {
	t.Helper()
	r := c.do(http.MethodGet, accessPath+"/audit", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /access/audit: status %d body %s", r.status, r.body)
	}
	var events []auditEvent
	r.json(&events)
	return events
}

// Ported from IdentityRbacIntegrationTests.AuditContainsActorCorrelationMfaAndBeforeAfterAndAuditFailureRollsBackMutation.
// .NET forced the audit insert to fail with an oversize details value
// written beside a role in one EF save. Here the real endpoints fail it:
// role.created and role.updated record the permission keys as the request
// sent them, as .NET did (EA/AuthorizationManagementEndpoints.cs:179,
// :260), so a request repeating one valid key 800 times makes after_json
// longer than its 10000-character CHECK. The audit insert fails inside the
// role's transaction, and the create and the update are both undone.
func TestAudit_RecordsActorCorrelationAndMFA_AndAFailedAuditUndoesTheChange(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	role := createRole(t, owner, "rbac-audit")

	var created []auditEvent
	for _, e := range auditTrail(t, owner) {
		if e.Action == "role.created" && e.TargetRoleID != nil && *e.TargetRoleID == role.ID {
			created = append(created, e)
		}
	}
	if len(created) != 1 {
		t.Fatalf("role.created events for the role: %+v", created)
	}
	e := created[0]
	if e.ActorUserID == nil || *e.ActorUserID != ownerID || e.CorrelationID == "" || e.MfaAuthenticated || e.BeforeJSON == "" || e.AfterJSON == "" ||
		e.TargetUserID != nil || e.Details != `{"action":"role.created"}` || !e.OccurredAt.Equal(h.now()) {
		t.Errorf("role.created = %+v", e)
	}

	oversize := slices.Repeat([]string{"customers:view"}, 800)
	events := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events`)
	r := owner.do(http.MethodPost, accessPath+"/roles", roleBody("rbac-audit-failure", "failure", oversize, nil), skipContract("a failed audit insert is an undocumented 500"))
	if r.status != http.StatusInternalServerError {
		t.Fatalf("create with a failing audit: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE name = 'rbac-audit-failure'`); n != 0 {
		t.Error("the role was created although its audit row failed")
	}

	version := h.version(t, "roles", role.ID)
	r = owner.do(http.MethodPut, accessPath+"/roles/"+role.ID.String(), roleBody("rbac-audit-renamed", "failure", oversize, &version), skipContract("a failed audit insert is an undocumented 500"))
	if r.status != http.StatusInternalServerError {
		t.Fatalf("update with a failing audit: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1 AND name = 'rbac-audit' AND description = 'Integration role' AND version::text = $2`, role.ID, version); n != 1 ||
		len(h.rolePermissions(t, role.ID)) != 0 {
		t.Error("the role changed although its audit row failed")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events`); n != events {
		t.Errorf("%d audit events, want the %d from before the failures", n, events)
	}
}

// TestAudit_ListsTheLatest500NewestFirst proves the trail's window and
// order (EA/AuthorizationManagementEndpoints.cs:552-554), with a tie in
// time broken by the later write.
func TestAudit_ListsTheLatest500NewestFirst(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	h.exec(t, `INSERT INTO identity.authorization_audit_events (action, details, before_json, after_json, correlation_id, mfa_authenticated, occurred_at)
	        SELECT 'test.event', '{"action":"test.event"}', '{}', '{}', 'c-' || n, n % 2 = 0, $1::timestamptz + n * interval '1 second'
	        FROM generate_series(1, 510) AS n`, h.now())
	for _, action := range []string{"tie.first", "tie.second"} {
		h.exec(t, `INSERT INTO identity.authorization_audit_events (action, details, mfa_authenticated, occurred_at)
		        VALUES ($1, '{}', false, $2)`, action, h.now().Add(510*time.Second))
	}

	events := auditTrail(t, owner)
	if len(events) != 500 {
		t.Fatalf("%d events, want 500", len(events))
	}
	if events[0].Action != "tie.second" || events[1].Action != "tie.first" || events[0].CorrelationID != "" || events[0].BeforeJSON != "" {
		t.Errorf("the newest two: %+v, %+v", events[0], events[1])
	}
	if events[2].CorrelationID != "c-510" || !events[2].MfaAuthenticated || events[499].CorrelationID != "c-13" || events[499].MfaAuthenticated {
		t.Errorf("the window runs from %+v to %+v, want c-510 to c-13", events[2], events[499])
	}
	for i := 1; i < len(events); i++ {
		if events[i].OccurredAt.After(events[i-1].OccurredAt) {
			t.Fatalf("event %d is newer than the one before it", i)
		}
	}
}

// catalogEntry is PermissionDescriptor as a test reads it.
type catalogEntry struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Module      string `json:"module"`
	Category    string `json:"category"`
	Sensitive   bool   `json:"sensitive"`
	Delegable   bool   `json:"delegable"`
}

// TestAccessCatalog_ListsEveryModulesPermissionsInKeyOrder proves GET
// /access/catalog serves the composed catalog in key order, as .NET's
// PermissionCatalog listed it: the customers keys, contributed by a module
// composed after identity, come first.
func TestAccessCatalog_ListsEveryModulesPermissionsInKeyOrder(t *testing.T) {
	t.Parallel()
	manage := catalogEntry{"identity:manage", "Manage identity", "Manage accounts, roles, and access.", "identity", "Administration", true, false}
	for _, c := range []struct {
		name string
		opts []harnessOption
		want []catalogEntry
	}{
		{"identity alone", nil, []catalogEntry{manage}},
		{"with the customers keys", []harnessOption{withPermissions(customersView, customersCreate)}, []catalogEntry{
			{"customers:create", "Create customers", "Create customer records.", "customers", "Customers", false, true},
			{"customers:view", "View customers", "Read customer records.", "customers", "Customers", false, true},
			manage,
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, c.opts...)
			owner, _ := h.bootstrapOwner(t)
			r := owner.do(http.MethodGet, accessPath+"/catalog", nil)
			var got []catalogEntry
			r.json(&got)
			if r.status != http.StatusOK || !slices.Equal(got, c.want) {
				t.Errorf("catalog: status %d body %s", r.status, r.body)
			}
		})
	}
}

// TestRoles_CreateValidatesTrimsAndAudits covers role creation: .NET's
// validation (every field present, every key in the catalog) and the
// columns' lengths; an Owner naming a delegation; then a created role, its
// Location, its trimmed and normalized columns, its steward, each key
// once, and its exact role.created row.
func TestRoles_CreateValidatesTrimsAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)

	valid := func() map[string]any { return roleBody("Role", "Description", []string{"customers:view"}, nil) }
	with := func(k string, v any) map[string]any { b := valid(); b[k] = v; return b }
	for name, body := range map[string]map[string]any{
		"no name":                      with("name", nil),
		"a blank name":                 with("name", "   "),
		"no display name":              with("displayName", nil),
		"a blank display name":         with("displayName", ""),
		"no description":               with("description", nil),
		"no permission list":           with("permissionKeys", nil),
		"an unknown key":               with("permissionKeys", []string{"customers:view", "customers:delete"}),
		"a 257-character name":         with("name", strings.Repeat("n", 257)),
		"a 201-character display name": with("displayName", strings.Repeat("d", 201)),
		"a 2001-character description": with("description", strings.Repeat("d", 2001)),
	} {
		wantFlat(t, name, owner.do(http.MethodPost, accessPath+"/roles", body),
			http.StatusBadRequest, "invalid_role", "Role metadata and registered permission keys are required.")
	}
	wantFlat(t, "a null body", owner.do(http.MethodPost, accessPath+"/roles", nil, rawBody("application/json", []byte("null"))),
		http.StatusBadRequest, "invalid_role", "Role metadata and registered permission keys are required.")
	wantFlat(t, "an Owner naming a delegation", owner.do(http.MethodPost, accessPath+"/roles", with("delegationId", uuid.New())),
		http.StatusForbidden, "delegation_not_allowed", "Owners cannot select a delegation.")
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE NOT is_built_in`); n != 0 {
		t.Fatalf("%d roles were created by refused requests", n)
	}
	limits := with("name", strings.Repeat("n", 256))
	limits["displayName"], limits["description"] = strings.Repeat("d", 200), strings.Repeat("d", 2000)
	if r := owner.do(http.MethodPost, accessPath+"/roles", limits); r.status != http.StatusCreated {
		t.Errorf("a role at every length limit: status %d body %s", r.status, r.body)
	}

	keys := []string{"customers:view", "customers:view", "customers:create"}
	r := owner.do(http.MethodPost, accessPath+"/roles", map[string]any{
		"name": "  Support agents  ", "displayName": " Support ", "description": " Handles tickets ", "permissionKeys": keys,
	})
	var role createdRole
	r.json(&role)
	if r.status != http.StatusCreated || role.Name != "Support agents" || !slices.Equal(role.Permissions, keys) || role.Version != h.version(t, "roles", role.ID) {
		t.Fatalf("create: status %d body %s", r.status, r.body)
	}
	if got, want := r.header("Location"), "/api/v1/identity/access/roles/"+role.ID.String(); got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1 AND name = 'Support agents' AND normalized_name = 'SUPPORT AGENTS'
	        AND display_name = 'Support' AND description = 'Handles tickets' AND NOT is_system AND NOT is_built_in AND steward_user_id = $2 AND created_at = $3`,
		role.ID, ownerID, h.now()); n != 1 {
		t.Error("the stored role is not the trimmed, normalized, stewarded one")
	}
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, []string{"customers:create", "customers:view"}) {
		t.Errorf("the role carries %v, want each key once", got)
	}

	var events []auditRow
	for _, e := range h.auditEvents(t, "role.created") {
		if e.TargetRole != nil && *e.TargetRole == role.ID {
			events = append(events, e)
		}
	}
	if len(events) != 1 {
		t.Fatalf("role.created rows: %+v", events)
	}
	e := events[0]
	wantBefore := `{"RoleId":"` + role.ID.String() + `","Name":null,"DisplayName":null,"Description":null,"IsSystem":false,"IsBuiltIn":false,"StewardUserId":null,"Permissions":[]}`
	wantAfter := `{"Name":"Support agents","DisplayName":"Support","Description":"Handles tickets","Permissions":["customers:view","customers:view","customers:create"]}`
	if e.Actor == nil || *e.Actor != ownerID || e.TargetUser != nil || e.Details != `{"action":"role.created"}` || e.Before != wantBefore || e.After != wantAfter || e.MFA || !e.At.Equal(h.now()) {
		t.Errorf("role.created = %+v", e)
	}

	r = owner.do(http.MethodGet, accessPath+"/roles", nil)
	var listed []struct {
		ID            uuid.UUID  `json:"id"`
		StewardUserID *uuid.UUID `json:"stewardUserId"`
		Permissions   []string   `json:"permissions"`
	}
	r.json(&listed)
	i := slices.IndexFunc(listed, func(l struct {
		ID            uuid.UUID  `json:"id"`
		StewardUserID *uuid.UUID `json:"stewardUserId"`
		Permissions   []string   `json:"permissions"`
	}) bool {
		return l.ID == role.ID
	})
	if i < 0 || listed[i].StewardUserID == nil || *listed[i].StewardUserID != ownerID || !slices.Equal(listed[i].Permissions, []string{"customers:create", "customers:view"}) {
		t.Errorf("GET /access/roles %s, want the role with its steward and keys", r.body)
	}
}

// TestRoles_UpdateReplacesEverythingAndAudits covers the edit: the whole
// permission set replaced, the exact response and role.updated row, a
// normalized name another role has (role_exists) against the role's own
// name in another case, an unknown role, and validation before the lookup.
func TestRoles_UpdateReplacesEverythingAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	alpha := createRole(t, owner, "Alpha", "customers:view")
	createRole(t, owner, "Beta")

	version := h.version(t, "roles", alpha.ID)
	r := owner.do(http.MethodPut, accessPath+"/roles/"+alpha.ID.String(), map[string]any{
		"name": " Alpha Prime ", "displayName": "Alpha prime", "description": "New", "permissionKeys": []string{"customers:create"}, "concurrencyStamp": version,
	})
	var updated updatedRole
	r.json(&updated)
	want := updatedRole{alpha.ID, "Alpha Prime", "Alpha prime", "New", h.version(t, "roles", alpha.ID), []string{"customers:create"}}
	if r.status != http.StatusOK || !jsonEqual(updated, want) {
		t.Fatalf("update: status %d body %s", r.status, r.body)
	}
	if got := h.rolePermissions(t, alpha.ID); !slices.Equal(got, []string{"customers:create"}) {
		t.Errorf("the role carries %v", got)
	}
	events := h.auditEvents(t, "role.updated")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetRole == nil || *events[0].TargetRole != alpha.ID ||
		events[0].Before != `{"Name":"Alpha","DisplayName":"Alpha","Description":"Integration role","Permissions":["customers:view"]}` ||
		events[0].After != `{"Name":"Alpha Prime","DisplayName":"Alpha prime","Description":"New","Permissions":["customers:create"]}` {
		t.Errorf("role.updated rows: %+v", events)
	}

	for _, name := range []string{"Beta", "beta"} {
		wantFlat(t, "rename to "+name, updateRole(owner, alpha.ID, name, updated.Version),
			http.StatusConflict, "role_exists", "The normalized role name is already in use.")
	}
	if r := updateRole(owner, alpha.ID, "ALPHA PRIME", updated.Version); r.status != http.StatusOK {
		t.Errorf("the role's own name in another case: status %d body %s", r.status, r.body)
	}
	if r := updateRole(owner, uuid.New(), "Gamma", "stamp"); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown role: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "an invalid edit of an unknown role", updateRole(owner, uuid.New(), " ", "stamp"),
		http.StatusBadRequest, "invalid_role", "Role metadata and registered permission keys are required.")
}

// TestRoles_AGroupMappedRoleIsProtected covers what an access group's
// mapping forbids: a role it maps may not gain or keep a non-delegable key,
// nor be named Owner (403 before role_exists), nor be deleted (403
// role_mapped). Once unmapped, an assigned role still cannot be deleted
// (409 role_assigned); unassigned, it is, with the exact role.deleted row.
func TestRoles_AGroupMappedRoleIsProtected(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	mapped := createRole(t, owner, "Mapped", "customers:view")
	group := h.mapRoleToGroup(t, mapped.ID)
	protectedMessage := "A role mapped to an access group cannot receive Owner or protected authorization-management permissions."

	wantFlat(t, "gain identity:manage", updateRole(owner, mapped.ID, "Mapped", mapped.Version, "customers:view", "identity:manage"),
		http.StatusForbidden, "role_group_protected_permission", protectedMessage)
	wantFlat(t, "be named Owner", updateRole(owner, mapped.ID, "Owner", mapped.Version, "customers:view"),
		http.StatusForbidden, "role_group_protected_permission", protectedMessage)
	if r := updateRole(owner, mapped.ID, "Mapped", mapped.Version, "customers:create"); r.status != http.StatusOK {
		t.Errorf("a delegable key: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "delete", deleteRole(owner, mapped.ID, h.version(t, "roles", mapped.ID)),
		http.StatusForbidden, "role_mapped", "Roles mapped to access groups cannot be deleted; remove every mapping first.")

	holding := createRole(t, owner, "Holding", "identity:manage")
	h.mapRoleToGroup(t, holding.ID)
	wantFlat(t, "shed a non-delegable key it has", updateRole(owner, holding.ID, "Holding", holding.Version),
		http.StatusForbidden, "role_group_protected_permission", protectedMessage)

	h.exec(t, `DELETE FROM identity.access_groups WHERE id = $1`, group)
	_, userID := userClient(t, h, owner, "holder@example.test")
	if r := assignRoles(owner, userID, h.version(t, "users", userID), mapped.ID); r.status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "delete an assigned role", deleteRole(owner, mapped.ID, h.version(t, "roles", mapped.ID)),
		http.StatusConflict, "role_assigned", "Assigned roles cannot be deleted.")
	if r := assignRoles(owner, userID, h.version(t, "users", userID)); r.status != http.StatusOK {
		t.Fatalf("unassign: status %d body %s", r.status, r.body)
	}
	if r := deleteRole(owner, mapped.ID, h.version(t, "roles", mapped.ID)); r.status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.role_permissions WHERE role_id = $1`, mapped.ID); n != 0 {
		t.Errorf("the deleted role left %d permission rows", n)
	}
	events := h.auditEvents(t, "role.deleted")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetRole == nil || *events[0].TargetRole != mapped.ID ||
		events[0].Before != `{"Name":"Mapped","Description":"Integration role"}` || events[0].After != `"{}"` || events[0].Details != `{"action":"role.deleted"}` {
		t.Errorf("role.deleted rows: %+v", events)
	}
}

// TestUserRoles_ReplacementEndsTheTargetsSessionsAndResetLinks is the
// security-stamp rule for role assignment: .NET rotated the target's
// security stamp on every replacement (EA/AuthorizationManagementEndpoints.cs:397),
// so every session of the target ends and every reset link they hold is
// spent, while the Owner's own session stays. It also pins the exact
// user.roles-replaced row, and the refusals, which change nothing.
func TestUserRoles_ReplacementEndsTheTargetsSessionsAndResetLinks(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	const email = "stamp@example.test"
	target, targetID := userClient(t, h, owner, email)
	role := createRole(t, owner, "Support", "customers:view")
	stamp := h.version(t, "users", targetID)

	wantFlat(t, "the Owner's own roles", assignRoles(owner, ownerID, h.version(t, "users", ownerID), role.ID),
		http.StatusBadRequest, "self_change", "A user cannot change their own roles.")
	if r := assignRoles(owner, uuid.New(), stamp, role.ID); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown user: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "an Owner naming a delegation", owner.do(http.MethodPut, accessPath+"/users/"+targetID.String()+"/roles",
		map[string]any{"roleIds": []uuid.UUID{role.ID}, "concurrencyStamp": stamp, "delegationId": uuid.New()}),
		http.StatusForbidden, "delegation_not_allowed", "Owners cannot select a delegation.")
	wantFlat(t, "no version", owner.do(http.MethodPut, accessPath+"/users/"+targetID.String()+"/roles", map[string]any{"roleIds": []uuid.UUID{role.ID}, "concurrencyStamp": nil}),
		http.StatusConflict, "user_conflict", "The user changed concurrently; refresh its version.")
	if r := target.do(http.MethodGet, accessPath+"/me", nil); r.status != http.StatusOK || h.version(t, "users", targetID) != stamp {
		t.Fatalf("a refusal ended the target's session or rotated their version: status %d", r.status)
	}

	if r := h.client(t).do(http.MethodPost, "/api/v1/identity/password-recovery/request", map[string]string{"email": email}); r.status != http.StatusOK {
		t.Fatalf("recovery request: status %d", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, targetID); n != 1 {
		t.Fatalf("%d reset tokens, want the one requested", n)
	}
	r := assignRoles(owner, targetID, stamp, role.ID)
	var body assignment
	r.json(&body)
	if r.status != http.StatusOK || body.Version == stamp || body.Version != h.version(t, "users", targetID) {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}
	if r := target.do(http.MethodGet, accessPath+"/me", nil); r.status != http.StatusUnauthorized {
		t.Errorf("the target's session after the replacement: status %d, want 401", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, targetID); n != 0 {
		t.Errorf("%d reset tokens survive the replacement", n)
	}
	if r := owner.do(http.MethodGet, accessPath+"/me", nil); r.status != http.StatusOK {
		t.Errorf("the Owner's session: status %d", r.status)
	}

	events := h.auditEvents(t, "user.roles-replaced")
	id := targetID.String()
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != targetID || events[0].TargetRole != nil ||
		events[0].Before != `{"UserId":"`+id+`","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}` ||
		events[0].After != `{"UserId":"`+id+`","Roles":["Support","User"],"PermissionKeys":["customers:view"],"IsDisabled":false,"IsDeleted":false}` ||
		events[0].Details != `{"action":"user.roles-replaced"}` {
		t.Errorf("user.roles-replaced rows: %+v", events)
	}
}

// TestAccessUsers_ListTheDirectoryAndInspectOthers covers the directory
// (every user by display name, ordinal, with state, version and roles by
// name) and the effective access of another user (direct roles as every
// response orders them, their keys once each, "*" for an Owner), with the
// caller's own id and an unknown one both the bare 404.
func TestAccessUsers_ListTheDirectoryAndInspectOthers(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	lockedID := h.createUser(t, owner, "a-locked@example.test", identity.RoleUser)
	userID := h.createUser(t, owner, "b-user@example.test", identity.RoleUser)
	disabledID := h.createUser(t, owner, "c-disabled@example.test", identity.RoleUser)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, lockedID, h.now().Add(time.Minute))
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabledID)
	zeta := createRole(t, owner, "Zeta", "customers:view", "customers:create")
	alpha := createRole(t, owner, "Alpha", "customers:view")
	if r := assignRoles(owner, userID, h.version(t, "users", userID), zeta.ID, alpha.ID); r.status != http.StatusOK {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}

	r := owner.do(http.MethodGet, accessPath+"/users", nil)
	var users []struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"displayName"`
		Email       string    `json:"email"`
		IsDisabled  bool      `json:"isDisabled"`
		Active      bool      `json:"active"`
		Version     string    `json:"version"`
		Roles       []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"roles"`
	}
	r.json(&users)
	wantOrder := []uuid.UUID{ownerID, lockedID, userID, disabledID} // "Integration Owner" sorts before the lower-case addresses
	if r.status != http.StatusOK || len(users) != len(wantOrder) {
		t.Fatalf("/access/users: status %d body %s", r.status, r.body)
	}
	for i, u := range users {
		if u.ID != wantOrder[i] || u.Version != h.version(t, "users", u.ID) {
			t.Errorf("entry %d: %+v, want user %s at its version", i, u, wantOrder[i])
		}
	}
	if users[1].Active || users[1].IsDisabled || users[3].Active || !users[3].IsDisabled || !users[2].Active || users[2].Email != "b-user@example.test" {
		t.Errorf("the entries' states: %+v", users)
	}
	var names []string
	for _, role := range users[2].Roles {
		names = append(names, role.Name)
	}
	if !slices.Equal(names, []string{"Alpha", identity.RoleUser, "Zeta"}) || users[2].Roles[0].ID != alpha.ID {
		t.Errorf("the user's roles %+v, want Alpha, User, Zeta", users[2].Roles)
	}

	r = owner.do(http.MethodGet, accessPath+"/users/"+userID.String(), nil)
	var access map[string]json.RawMessage
	r.json(&access)
	want := map[string]string{
		"id": `"` + userID.String() + `"`, "roles": `["User","Alpha","Zeta"]`,
		"roleIds":     `["` + identity.RoleUserID.String() + `","` + alpha.ID.String() + `","` + zeta.ID.String() + `"]`,
		"permissions": `["customers:create","customers:view"]`, "version": `"` + h.version(t, "users", userID) + `"`,
	}
	if r.status != http.StatusOK || len(access) != len(want) {
		t.Fatalf("/access/users/{id}: status %d body %s", r.status, r.body)
	}
	for k, v := range want {
		if string(access[k]) != v {
			t.Errorf("%s = %s, want %s", k, access[k], v)
		}
	}

	otherOwner := h.createUser(t, owner, "second-owner@example.test", identity.RoleOwner)
	r = owner.do(http.MethodGet, accessPath+"/users/"+otherOwner.String(), nil)
	access = nil
	r.json(&access)
	if r.status != http.StatusOK || string(access["permissions"]) != `["*"]` {
		t.Errorf("another Owner's access: status %d body %s", r.status, r.body)
	}
	for _, id := range []uuid.UUID{ownerID, uuid.New()} {
		if r := owner.do(http.MethodGet, accessPath+"/users/"+id.String(), nil); r.status != http.StatusNotFound || len(r.body) != 0 {
			t.Errorf("/access/users/%s: status %d body %s, want the bare 404", id, r.status, r.body)
		}
	}
}

// holdRoleLock starts a transaction holding roleID's advisory lock, as a
// role edit, a role deletion or a group mapping holds it, and rolls it back
// at cleanup unless the test commits it first.
func holdRoleLock(t *testing.T, h *harness, roleID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, identity.RoleMutationLockKey(roleID)); err != nil {
		t.Fatalf("gate: take the role lock: %v", err)
	}
	return gate
}

// raceBehind runs fns at once while gate holds a lock they need, and
// commits the gate only once every one of them is waiting on a lock.
func raceBehind(t *testing.T, h *harness, gate pgx.Tx, fns ...func() *resp) []*resp {
	t.Helper()
	var out []*resp
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		out = race(fns...)
	}()
	awaitLockWaiters(t, h, len(fns), finished)
	if err := gate.Commit(context.Background()); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	<-finished
	return out
}

// TestRoles_TheRoleLockMakesAChangeSeeAMappingMadeWhileItWaited is the
// interleaving the per-role lock exists for (AZ/AuthorizationMutationService.cs:20-31,
// EA/AuthorizationManagementEndpoints.cs:308-316): a group mapping holds
// the role's lock and maps it while an edit and a deletion wait on that
// lock. Once it commits, the edit that would give the role a non-delegable
// key is refused, and so is the deletion.
func TestRoles_TheRoleLockMakesAChangeSeeAMappingMadeWhileItWaited(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "Contended", "customers:view")

	gate := holdRoleLock(t, h, role.ID)
	groupID := uuid.New()
	ctx := context.Background()
	if _, err := gate.Exec(ctx, `INSERT INTO identity.access_groups (id, display_name, source, version, created_at, updated_at)
	        VALUES ($1, 'contended', 'local', gen_random_uuid(), $2, $2)`, groupID, h.now()); err != nil {
		t.Fatalf("gate: %v", err)
	}
	if _, err := gate.Exec(ctx, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, 'local')`, groupID, role.ID); err != nil {
		t.Fatalf("gate: %v", err)
	}
	out := raceBehind(t, h, gate,
		func() *resp {
			return updateRole(owner, role.ID, role.Name, role.Version, "customers:view", "identity:manage")
		},
		func() *resp { return deleteRole(owner, role.ID, role.Version) },
	)
	wantFlat(t, "the edit", out[0], http.StatusForbidden, "role_group_protected_permission",
		"A role mapped to an access group cannot receive Owner or protected authorization-management permissions.")
	wantFlat(t, "the deletion", out[1], http.StatusForbidden, "role_mapped", "Roles mapped to access groups cannot be deleted; remove every mapping first.")
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, []string{"customers:view"}) {
		t.Errorf("the role carries %v", got)
	}
}

// TestRoles_ConcurrentEditsWithOneVersionSucceedOnce races two edits of one
// role under the same version, both queued on the role's lock: exactly one
// succeeds, and the other sees the version it replaced.
func TestRoles_ConcurrentEditsWithOneVersionSucceedOnce(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "Raced")

	out := raceBehind(t, h, holdRoleLock(t, h, role.ID),
		func() *resp { return updateRole(owner, role.ID, role.Name, role.Version, "customers:view") },
		func() *resp { return updateRole(owner, role.ID, role.Name, role.Version, "customers:create") },
	)
	winner, loser := out[0], out[1]
	if winner.status != http.StatusOK {
		winner, loser = loser, winner
	}
	var won updatedRole
	winner.json(&won)
	if winner.status != http.StatusOK || won.Version != h.version(t, "roles", role.ID) || !slices.Equal(h.rolePermissions(t, role.ID), won.Permissions) {
		t.Fatalf("statuses %d / %d (bodies %s / %s), want one 200 that stuck", out[0].status, out[1].status, out[0].body, out[1].body)
	}
	wantFlat(t, "the loser", loser, http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	if n := len(h.auditEvents(t, "role.updated")); n != 1 {
		t.Errorf("%d role.updated rows, want 1", n)
	}
}

// TestUserRoles_ConcurrentReplacementsWithOneVersionSucceedOnce races two
// role replacements for one user under the same version, both queued on
// the user's row: exactly one succeeds, and the other sees the version it
// replaced.
func TestUserRoles_ConcurrentReplacementsWithOneVersionSucceedOnce(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	_, userID := userClient(t, h, owner, "raced@example.test")
	a, b := createRole(t, owner, "A"), createRole(t, owner, "B")
	stamp := h.version(t, "users", userID)

	out := raceBehindUserRow(t, h, userID,
		func() *resp { return assignRoles(owner, userID, stamp, a.ID) },
		func() *resp { return assignRoles(owner, userID, stamp, b.ID) },
	)
	winner, loser := out[0], out[1]
	if winner.status != http.StatusOK {
		winner, loser = loser, winner
	}
	var won assignment
	winner.json(&won)
	if winner.status != http.StatusOK || won.Version != h.version(t, "users", userID) || len(won.RoleIDs) != 2 {
		t.Fatalf("statuses %d / %d (bodies %s / %s), want one 200 that stuck", out[0].status, out[1].status, out[0].body, out[1].body)
	}
	wantFlat(t, "the loser", loser, http.StatusConflict, "user_conflict", "The user changed concurrently; refresh its version.")
	if names := h.roleNames(t, userID); len(names) != 2 {
		t.Errorf("the user holds %v, want User and the winner's role", names)
	}
}

// TestRoles_ACreateRacingAnotherOfTheSameNameIsARoleConflict forces the
// lost race of a create: another transaction has inserted the name but not
// committed, so the create sees no such role, then waits on the unique
// index. Once the other commits, the insert violates the index, which the
// create's own catch answers (EA/AuthorizationManagementEndpoints.cs:192-195).
func TestRoles_ACreateRacingAnotherOfTheSameNameIsARoleConflict(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	        VALUES (gen_random_uuid(), 'raced', 'RACED', 'raced', gen_random_uuid(), $1, $1)`, h.now()); err != nil {
		t.Fatal(err)
	}
	out := raceBehind(t, h, gate, func() *resp { return owner.do(http.MethodPost, accessPath+"/roles", roleBody("Raced", "x", nil, nil)) })
	wantFlat(t, "the create", out[0], http.StatusConflict, "role_conflict", "The role conflicts with a concurrent authorization change.")
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE normalized_name = 'RACED'`); n != 1 {
		t.Errorf("%d roles named raced", n)
	}
}

// assignmentInFlight starts a transaction that is midway through assigning
// roleID to userID, as PutIdentityAccessUsersByIdRoles is: it holds a key
// share on the role (AssignUserRoleLocks) and has inserted the assignment.
// It rolls back at cleanup unless the test commits it first.
func assignmentInFlight(t *testing.T, h *harness, userID, roleID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM identity.roles WHERE id = $1 FOR KEY SHARE`, roleID); err != nil {
		t.Fatalf("gate: key share on the role: %v", err)
	}
	if _, err := gate.Exec(ctx, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID); err != nil {
		t.Fatalf("gate: assign: %v", err)
	}
	return gate
}

// TestRoles_AnAssignmentInFlightBlocksADeleteButNotAnEdit pins the two row
// lock modes a role mutation takes against an assignment's FOR KEY SHARE.
// An edit holds the role FOR NO KEY UPDATE, so it goes through while an
// assignment of the role is in flight. A deletion holds it FOR UPDATE, so
// it waits for that assignment, then sees the role assigned (409
// role_assigned) rather than deleting it and cascading the new assignment
// away.
func TestRoles_AnAssignmentInFlightBlocksADeleteButNotAnEdit(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	_, userID := userClient(t, h, owner, "in-flight@example.test")
	role := createRole(t, owner, "In flight")

	gate := assignmentInFlight(t, h, userID, role.ID)
	edited := make(chan *resp, 1)
	go func() { edited <- updateRole(owner, role.ID, role.Name, role.Version, "customers:view") }()
	select {
	case r := <-edited:
		if r.status != http.StatusOK {
			t.Fatalf("the edit beside an assignment in flight: status %d body %s", r.status, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the edit waited on the assignment's key share; backends: %s", backends(t, h))
	}
	if err := gate.Rollback(context.Background()); err != nil {
		t.Fatalf("gate: rollback: %v", err)
	}

	gate = assignmentInFlight(t, h, userID, role.ID)
	out := raceBehind(t, h, gate, func() *resp { return deleteRole(owner, role.ID, h.version(t, "roles", role.ID)) })
	wantFlat(t, "the deletion behind the assignment", out[0], http.StatusConflict, "role_assigned", "Assigned roles cannot be deleted.")
	if got := h.roleNames(t, userID); !slices.Equal(got, []string{"In flight", identity.RoleUser}) {
		t.Errorf("the user holds %v, want the role the assignment gave", got)
	}
}
