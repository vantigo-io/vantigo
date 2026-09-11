package identity_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const delegationsPath = accessPath + "/delegations"

// createdDelegation is DelegationSaveResponse as a test reads it.
type createdDelegation struct {
	ID      uuid.UUID `json:"id"`
	Version string    `json:"version"`
}

// delegationBody is a DelegationRequest for grantee that may create roles,
// as .NET's tests sent one (TS/Integration/IdentityRbacIntegrationTests.cs:676-690).
// expiresAt is a time.Time, or nil for none.
func delegationBody(grantee uuid.UUID, expiresAt any, roles []uuid.UUID, keys []string) map[string]any {
	if roles == nil {
		roles = []uuid.UUID{}
	}
	if keys == nil {
		keys = []string{}
	}
	return map[string]any{"granteeUserId": grantee, "expiresAt": expiresAt, "permissionKeys": keys, "stewardedRoleIds": roles, "canCreateRoles": true}
}

// delegate makes grantee owner's delegate over roles and keys, for an hour
// from the harness clock, which must succeed.
func delegate(t testing.TB, h *harness, owner *client, grantee uuid.UUID, roles []uuid.UUID, keys ...string) createdDelegation {
	t.Helper()
	r := owner.do(http.MethodPost, delegationsPath, delegationBody(grantee, h.now().Add(time.Hour), roles, keys))
	if r.status != http.StatusCreated {
		t.Fatalf("delegate to %s: status %d body %s", grantee, r.status, r.body)
	}
	var d createdDelegation
	r.json(&d)
	return d
}

// updateDelegation replaces delegation id with body as c, under stamp.
func updateDelegation(c *client, id uuid.UUID, stamp any, body map[string]any) *resp {
	body["concurrencyStamp"] = stamp
	return c.do(http.MethodPut, delegationsPath+"/"+id.String(), body)
}

// revokeDelegation revokes delegation id as c, under stamp.
func revokeDelegation(c *client, id uuid.UUID, stamp any) *resp {
	return c.do(http.MethodPost, delegationsPath+"/"+id.String()+"/revoke", map[string]any{"concurrencyStamp": stamp})
}

// assignUnder replaces userID's custom roles with roleIDs as c, under
// stamp, naming delegation when it is not nil.
func assignUnder(c *client, userID uuid.UUID, stamp string, delegation *uuid.UUID, roleIDs ...uuid.UUID) *resp {
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	body := map[string]any{"roleIds": roleIDs, "concurrencyStamp": stamp}
	if delegation != nil {
		body["delegationId"] = *delegation
	}
	return c.do(http.MethodPut, accessPath+"/users/"+userID.String()+"/roles", body)
}

// createRoleUnder asks c to create the role name with keys, naming
// delegation when it is not nil.
func createRoleUnder(c *client, name string, delegation *uuid.UUID, keys ...string) *resp {
	body := roleBody(name, "Delegated role", keys, nil)
	if delegation != nil {
		body["delegationId"] = *delegation
	}
	return c.do(http.MethodPost, accessPath+"/roles", body)
}

// delegationScope is DelegationScopeResponse as a test reads it.
type delegationScope struct {
	ID                      uuid.UUID   `json:"id"`
	CanCreateRoles          bool        `json:"canCreateRoles"`
	GrantablePermissionKeys []string    `json:"grantablePermissionKeys"`
	StewardedRoleIDs        []uuid.UUID `json:"stewardedRoleIds"`
	AssignableRoleIDs       []uuid.UUID `json:"assignableRoleIds"`
}

// administration is /access/me's management authority as a test reads it.
type administration struct {
	CanManageAuthorization bool `json:"canManageAuthorization"`
	AdministrationScope    *struct {
		IsOwner          bool              `json:"isOwner"`
		DelegationScopes []delegationScope `json:"delegationScopes"`
	} `json:"administrationScope"`
}

// administrationOf is c's authority as /access/me shows it.
func administrationOf(t testing.TB, c *client) administration {
	t.Helper()
	r := c.do(http.MethodGet, accessPath+"/me", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /access/me: status %d body %s", r.status, r.body)
	}
	var a administration
	r.json(&a)
	return a
}

// scopeOf is the scope of delegation id in a, failing t without one.
func (a administration) scopeOf(t testing.TB, id uuid.UUID) delegationScope {
	t.Helper()
	if a.AdministrationScope != nil {
		for _, sc := range a.AdministrationScope.DelegationScopes {
			if sc.ID == id {
				return sc
			}
		}
	}
	t.Fatalf("no scope for delegation %s in %+v", id, a)
	return delegationScope{}
}

// manages reports whether c's session passes AuthorizationManagement: GET
// /access/roles answers 200, or 403 forbidden.
func manages(t testing.TB, c *client) bool {
	t.Helper()
	switch r := c.do(http.MethodGet, accessPath+"/roles", nil); {
	case r.status == http.StatusOK:
		return true
	case r.status == http.StatusForbidden && r.code() == "forbidden":
		return false
	default:
		t.Fatalf("GET /access/roles: status %d body %s, want 200 or 403", r.status, r.body)
		return false
	}
}

// fieldsOf is o's member names, sorted.
func fieldsOf(o map[string]json.RawMessage) []string { return slices.Sorted(maps.Keys(o)) }

// directoryOf is the user ids c's GET /access/users lists.
func directoryOf(t testing.TB, c *client) []uuid.UUID {
	t.Helper()
	r := c.do(http.MethodGet, accessPath+"/users", nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET /access/users: status %d body %s", r.status, r.body)
	}
	var users []struct {
		ID uuid.UUID `json:"id"`
	}
	r.json(&users)
	ids := make([]uuid.UUID, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return ids
}

// gate starts a transaction a test holds locks in, to force a race's
// interleaving, rolled back at cleanup unless the test commits it first.
// Each statement runs on it in turn.
func gate(t *testing.T, h *harness, statements ...func(pgx.Tx) error) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	for _, s := range statements {
		if err := s(tx); err != nil {
			t.Fatalf("gate: %v", err)
		}
	}
	return tx
}

// stmt is one gate statement.
func stmt(sql string, args ...any) func(pgx.Tx) error {
	return func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), sql, args...)
		return err
	}
}

// mailedResetToken is the token of the last password-reset link mailed to
// email.
func mailedResetToken(t testing.TB, h *harness, email string) string {
	t.Helper()
	sent := h.mailTo(email)
	if len(sent) == 0 {
		t.Fatalf("no mail to %s", email)
	}
	body := sent[len(sent)-1].TextBody
	link, err := url.Parse(strings.TrimSpace(body[strings.Index(body, "http"):]))
	if err != nil || link.Query().Get("token") == "" {
		t.Fatalf("no reset link in %q", body)
	}
	return link.Query().Get("token")
}

// Ported from IdentityRbacIntegrationTests.ActiveDelegateGetsManagementMarkerAndDirectoryExcludesProtectedTargets.
// .NET pinned the order of the scope's members; a JSON object's members are
// unordered (RFC 8259 §4), and the generated type writes them by name, so
// the set of members is pinned here, with each value. Extended: another
// delegate is left out of the directory too, and a user whose delegation
// has expired is listed again.
func TestRbac_AnActiveDelegateManagesAndTheirDirectoryLeavesOutOwnersAndDelegates(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	const email = "delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	ordinaryID := h.createUser(t, owner, "ordinary@example.test", identity.RoleUser)
	otherID := h.createUser(t, owner, "other-delegate@example.test", identity.RoleUser)
	lapsedID := h.createUser(t, owner, "lapsed@example.test", identity.RoleUser)
	d := delegate(t, h, owner, delegateID, nil)
	delegate(t, h, owner, otherID, nil)
	if r := owner.do(http.MethodPost, delegationsPath, delegationBody(lapsedID, h.now().Add(time.Minute), nil, nil)); r.status != http.StatusCreated {
		t.Fatalf("delegate to the lapsed user: status %d body %s", r.status, r.body)
	}
	h.advance(time.Minute)

	delegated := h.login(t, email, userPassword)
	r := delegated.do(http.MethodGet, accessPath+"/me", nil)
	var me map[string]json.RawMessage
	r.json(&me)
	if r.status != http.StatusOK || string(me["canManageAuthorization"]) != "true" || string(me["permissions"]) != "[]" {
		t.Fatalf("the delegate's /access/me: status %d body %s", r.status, r.body)
	}
	var administration map[string]json.RawMessage
	if err := json.Unmarshal(me["administrationScope"], &administration); err != nil ||
		!slices.Equal(fieldsOf(administration), []string{"delegationScopes", "isOwner"}) || string(administration["isOwner"]) != "false" {
		t.Fatalf("administrationScope %s, want exactly isOwner false and the delegation scopes", me["administrationScope"])
	}
	var scopes []map[string]json.RawMessage
	if err := json.Unmarshal(administration["delegationScopes"], &scopes); err != nil || len(scopes) != 1 {
		t.Fatalf("delegationScopes %s, want one", administration["delegationScopes"])
	}
	want := map[string]string{
		"id": `"` + d.ID.String() + `"`, "canCreateRoles": "true", "grantablePermissionKeys": "[]", "stewardedRoleIds": "[]", "assignableRoleIds": "[]",
	}
	if !slices.Equal(fieldsOf(scopes[0]), slices.Sorted(maps.Keys(want))) {
		t.Errorf("the scope's members %v, want %v", fieldsOf(scopes[0]), slices.Sorted(maps.Keys(want)))
	}
	for k, v := range want {
		if string(scopes[0][k]) != v {
			t.Errorf("the scope's %s = %s, want %s", k, scopes[0][k], v)
		}
	}

	listed := directoryOf(t, delegated)
	if !slices.Contains(listed, ordinaryID) || !slices.Contains(listed, lapsedID) {
		t.Errorf("the delegate's directory %v leaves out an ordinary user", listed)
	}
	for name, id := range map[string]uuid.UUID{"the Owner": ownerID, "the delegate": delegateID, "another delegate": otherID} {
		if slices.Contains(listed, id) {
			t.Errorf("the delegate's directory lists %s", name)
		}
	}
	if all := directoryOf(t, owner); len(all) != 5 {
		t.Errorf("the Owner's directory has %d users, want all 5", len(all))
	}
}

// Ported from IdentityRbacIntegrationTests.DelegateEmptyAssignmentPreservesOutOfBoundaryRoleButOwnerCanRemoveIt.
// Extended: an empty replacement under the managed role's delegation takes
// that role away and leaves the other in place, although the delegate's
// other delegation could assign it: a removal is confined to the named
// scope too.
func TestRbac_ADelegateLeavesARoleOutsideTheNamedScopeInPlace(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "boundary-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "boundary-target@example.test", identity.RoleUser)
	outside := createRole(t, owner, "rbac-outside")
	managed := createRole(t, owner, "rbac-managed")
	if r := assignRoles(owner, targetID, h.version(t, "users", targetID), outside.ID, managed.ID); r.status != http.StatusOK {
		t.Fatalf("initial assignment: status %d body %s", r.status, r.body)
	}
	d := delegate(t, h, owner, delegateID, []uuid.UUID{managed.ID})
	delegate(t, h, owner, delegateID, []uuid.UUID{outside.ID})

	delegated := h.login(t, email, userPassword)
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID, managed.ID); r.status != http.StatusOK {
		t.Fatalf("the delegate's assignment: status %d body %s", r.status, r.body)
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser, "rbac-managed", "rbac-outside"}) {
		t.Errorf("the target holds %v, want User and both roles", got)
	}
	r := owner.do(http.MethodGet, accessPath+"/users/"+targetID.String(), nil)
	var access struct {
		RoleIDs []uuid.UUID `json:"roleIds"`
	}
	r.json(&access)
	if r.status != http.StatusOK || !slices.Contains(access.RoleIDs, managed.ID) {
		t.Errorf("the Owner's view of the target: status %d body %s", r.status, r.body)
	}

	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID); r.status != http.StatusOK {
		t.Fatalf("the delegate's empty replacement: status %d body %s", r.status, r.body)
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser, "rbac-outside"}) {
		t.Errorf("after the delegate's empty replacement the target holds %v, want User and rbac-outside", got)
	}

	if r := assignRoles(owner, targetID, h.version(t, "users", targetID)); r.status != http.StatusOK {
		t.Fatalf("the Owner's empty replacement: status %d body %s", r.status, r.body)
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("after the Owner's empty replacement the target holds %v, want User alone", got)
	}
}

// Ported from IdentityRbacIntegrationTests.DisjointDelegationScopesCannotSynthesizeCombinedRoleAuthority.
// Extended: the codes and messages; the other scope is refused too; and
// neither an edit that keeps both keys nor one that sheds one, nor a
// deletion, can use the two scopes together.
func TestRbac_DisjointScopesNeverCombine(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "disjoint-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "disjoint-target@example.test", identity.RoleUser)
	combined := createRole(t, owner, "rbac-combined", "customers:view", "customers:create")
	a := delegate(t, h, owner, delegateID, []uuid.UUID{combined.ID}, "customers:view")
	b := delegate(t, h, owner, delegateID, []uuid.UUID{combined.ID}, "customers:create")

	delegated := h.login(t, email, userPassword)
	me := administrationOf(t, delegated)
	if !me.CanManageAuthorization || me.AdministrationScope == nil || len(me.AdministrationScope.DelegationScopes) != 2 {
		t.Fatalf("/access/me %+v, want two scopes", me)
	}
	for id, key := range map[uuid.UUID]string{a.ID: "customers:view", b.ID: "customers:create"} {
		sc := me.scopeOf(t, id)
		if !slices.Equal(sc.GrantablePermissionKeys, []string{key}) || len(sc.AssignableRoleIDs) != 0 {
			t.Errorf("scope %s = %+v, want %s alone and nothing assignable", id, sc, key)
		}
	}

	wantFlat(t, "no delegation named", assignUnder(delegated, targetID, h.version(t, "users", targetID), nil, combined.ID),
		http.StatusForbidden, "delegation_required", "A delegation must be selected.")
	for name, id := range map[string]uuid.UUID{"scope A": a.ID, "scope B": b.ID} {
		wantFlat(t, "assign under "+name, assignUnder(delegated, targetID, h.version(t, "users", targetID), &id, combined.ID),
			http.StatusForbidden, "assignment_denied", "The requested role is outside the selected delegation scope.")
	}
	editDenied := "Only delegated stewarded custom roles within the selected delegation boundary may be changed."
	wantFlat(t, "an edit keeping both keys", updateRole(delegated, combined.ID, combined.Name, combined.Version, "customers:view", "customers:create"),
		http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "an edit shedding one key", updateRole(delegated, combined.ID, combined.Name, combined.Version, "customers:view"),
		http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "a deletion", deleteRole(delegated, combined.ID, combined.Version),
		http.StatusForbidden, "role_delete_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be deleted.")
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("the target holds %v", got)
	}
	if got := h.rolePermissions(t, combined.ID); !slices.Equal(got, []string{"customers:create", "customers:view"}) {
		t.Errorf("the role carries %v", got)
	}
}

// Ported from IdentityRbacIntegrationTests.StewardedRoleFullyCoveredByOneDelegationScopeCanBeAssigned.
// Extended: the response and the audit row name the delegate.
func TestRbac_AStewardedRoleOneScopeFullyCoversCanBeAssigned(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "covered-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "covered-target@example.test", identity.RoleUser)
	role := createRole(t, owner, "rbac-covered", "customers:view", "customers:create")
	d := delegate(t, h, owner, delegateID, []uuid.UUID{role.ID}, "customers:view", "customers:create")

	delegated := h.login(t, email, userPassword)
	sc := administrationOf(t, delegated).scopeOf(t, d.ID)
	if !slices.Equal(sc.AssignableRoleIDs, []uuid.UUID{role.ID}) || !slices.Equal(sc.StewardedRoleIDs, []uuid.UUID{role.ID}) {
		t.Errorf("the scope %+v, want the role stewarded and assignable", sc)
	}
	r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID, role.ID)
	var body assignment
	r.json(&body)
	if r.status != http.StatusOK || !slices.Equal(body.RoleIDs, []uuid.UUID{identity.RoleUserID, role.ID}) {
		t.Fatalf("assign: status %d body %s", r.status, r.body)
	}
	events := h.auditEvents(t, "user.roles-replaced")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != delegateID || events[0].TargetUser == nil || *events[0].TargetUser != targetID {
		t.Errorf("user.roles-replaced rows %+v, want one by the delegate", events)
	}
}

// Ported from IdentityRbacIntegrationTests.DelegationRejectsNonDelegablePermissionAndExpiredOrRevokedBoundary.
// As .NET's test did, the expiry is moved into the past in the database;
// TestDelegations_ARevokedOrExpiredDelegationDeniesTheNextRequest lets the
// clock run out instead.
func TestRbac_ADelegationRefusesANonDelegableKeyAndEndsWhenItExpires(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "expiring-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	wantFlat(t, "identity:manage", owner.do(http.MethodPost, delegationsPath, delegationBody(delegateID, h.now().Add(time.Hour), nil, []string{"identity:manage"})),
		http.StatusBadRequest, "invalid_delegation_permissions", "Delegations may contain only registered delegable permissions.")

	boundary := delegate(t, h, owner, delegateID, nil)
	delegated := h.login(t, email, userPassword)
	if !manages(t, delegated) {
		t.Fatal("the delegate is refused GET /access/roles")
	}
	h.exec(t, `UPDATE identity.authorization_delegations SET expires_at = $2 WHERE id = $1`, boundary.ID, h.now().Add(-time.Minute))
	if manages(t, delegated) {
		t.Error("the delegate still manages with an expired delegation")
	}
}

// Ported from IdentityRbacIntegrationTests.DelegatedRoleStewardshipAndBoundaryAreEnforced.
// A delegate's own roles are refused with 400 self_change, as .NET's
// endpoint answered first for every caller
// (EA/AuthorizationManagementEndpoints.cs:359) and its test pinned
// (IdentityRbacIntegrationTests.cs:464-469). Extended: the codes; another
// delegate is an assignment target no delegate may have, even under the
// delegation; the edit is audited as the delegate's.
func TestRbac_DelegatedStewardshipAndTheSelfAndOwnerBoundaries(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	const email = "steward-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	ordinaryID := h.createUser(t, owner, "steward-target@example.test", identity.RoleUser)
	otherID := h.createUser(t, owner, "steward-other@example.test", identity.RoleUser)
	stewarded := createRole(t, owner, "rbac-stewarded")
	boundary := delegate(t, h, owner, delegateID, []uuid.UUID{stewarded.ID})
	delegate(t, h, owner, otherID, nil)

	delegated := h.login(t, email, userPassword)
	if r := updateRole(delegated, stewarded.ID, stewarded.Name, stewarded.Version); r.status != http.StatusOK {
		t.Fatalf("the delegate's edit: status %d body %s", r.status, r.body)
	}
	if events := h.auditEvents(t, "role.updated"); len(events) != 1 || events[0].Actor == nil || *events[0].Actor != delegateID {
		t.Errorf("role.updated rows %+v, want one by the delegate", events)
	}
	wantFlat(t, "the delegate's own roles", assignRoles(delegated, delegateID, h.version(t, "users", delegateID), stewarded.ID),
		http.StatusBadRequest, "self_change", "A user cannot change their own roles.")
	targetDenied := "Delegates may assign only ordinary users."
	wantFlat(t, "the Owner's roles", assignRoles(delegated, ownerID, h.version(t, "users", ownerID), stewarded.ID),
		http.StatusForbidden, "assignment_target_denied", targetDenied)
	wantFlat(t, "another delegate's roles", assignUnder(delegated, otherID, h.version(t, "users", otherID), &boundary.ID, stewarded.ID),
		http.StatusForbidden, "assignment_target_denied", targetDenied)
	if r := assignUnder(delegated, ordinaryID, h.version(t, "users", ordinaryID), &boundary.ID, stewarded.ID); r.status != http.StatusOK {
		t.Errorf("an ordinary user's roles: status %d body %s", r.status, r.body)
	}
	for _, id := range []uuid.UUID{ownerID, otherID, delegateID} {
		if slices.Contains(h.roleNames(t, id), "rbac-stewarded") {
			t.Errorf("user %s was given the role", id)
		}
	}
}

// Ported from IdentityRbacIntegrationTests.DelegatedRoleEditMustCoverCurrentAndReplacementPermissions.
// Extended: the code and message.
func TestRbac_ADelegatedEditMustCoverTheCurrentAndTheNewKeys(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	narrowID := h.createUser(t, owner, "narrow@example.test", identity.RoleUser)
	broadID := h.createUser(t, owner, "broad@example.test", identity.RoleUser)
	role := createRole(t, owner, "rbac-edit-current-boundary", "customers:view", "customers:create")
	delegate(t, h, owner, narrowID, []uuid.UUID{role.ID}, "customers:view")

	narrow := h.login(t, "narrow@example.test", userPassword)
	wantFlat(t, "the narrow delegate's edit", updateRole(narrow, role.ID, role.Name, role.Version, "customers:view"),
		http.StatusForbidden, "role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.")
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, []string{"customers:create", "customers:view"}) {
		t.Errorf("the role carries %v after the refused edit", got)
	}

	delegate(t, h, owner, broadID, []uuid.UUID{role.ID}, "customers:view", "customers:create")
	broad := h.login(t, "broad@example.test", userPassword)
	if r := updateRole(broad, role.ID, role.Name, role.Version, "customers:view"); r.status != http.StatusOK {
		t.Fatalf("the broad delegate's edit: status %d body %s", r.status, r.body)
	}
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, []string{"customers:view"}) {
		t.Errorf("the role carries %v after the broad edit", got)
	}
	if r := updateRole(owner, role.ID, role.Name, h.version(t, "roles", role.ID)); r.status != http.StatusOK {
		t.Fatalf("the Owner's edit: status %d body %s", r.status, r.body)
	}
	if got := h.rolePermissions(t, role.ID); len(got) != 0 {
		t.Errorf("the role carries %v after the Owner's edit", got)
	}
}

// Ported from IdentityRbacIntegrationTests.DelegatedDeleteRequiresSelectedScopeToCoverCurrentRolePermissions.
// Extended: the code; the role survives; once the delegation grants the
// key again, the same deletion goes through; and the delegation outlives
// the role it stewarded, as .NET's cascading foreign key kept it: the
// delegate still manages and creates a role under it.
func TestRbac_ADelegatedDeleteNeedsAScopeCoveringTheRolesKeys(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "delete-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	role := createRole(t, owner, "rbac-delete-boundary", "customers:view")
	d := delegate(t, h, owner, delegateID, []uuid.UUID{role.ID}, "customers:view")
	h.exec(t, `DELETE FROM identity.authorization_delegation_permissions WHERE delegation_id = $1`, d.ID)

	delegated := h.login(t, email, userPassword)
	wantFlat(t, "the deletion", deleteRole(delegated, role.ID, role.Version),
		http.StatusForbidden, "role_delete_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be deleted.")
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1`, role.ID); n != 1 {
		t.Fatal("the role was deleted")
	}
	h.exec(t, `INSERT INTO identity.authorization_delegation_permissions (delegation_id, permission_key) VALUES ($1, 'customers:view')`, d.ID)
	if r := deleteRole(delegated, role.ID, role.Version); r.status != http.StatusNoContent {
		t.Fatalf("the deletion once the scope covers the key: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegation_roles WHERE role_id = $1`, role.ID); n != 0 {
		t.Errorf("%d stewardships of the deleted role remain", n)
	}
	if !manages(t, delegated) {
		t.Fatal("the delegate no longer manages after deleting a role within their scope")
	}
	if sc := administrationOf(t, delegated).scopeOf(t, d.ID); len(sc.StewardedRoleIDs) != 0 || !slices.Equal(sc.GrantablePermissionKeys, []string{"customers:view"}) {
		t.Errorf("the scope after the deletion %+v, want no roles and its key", sc)
	}
	if r := createRoleUnder(delegated, "rbac-after-delete", &d.ID, "customers:view"); r.status != http.StatusCreated {
		t.Errorf("a role created under the delegation afterwards: status %d body %s", r.status, r.body)
	}
}

// TestRbac_AnOwnerDeletingAStewardedRoleLeavesTheDelegationUsable is
// .NET's cascading fk_authorization_delegation_roles_role_id
// (Configurations/AuthorizationDelegationEntityTypeConfiguration.cs:49-50):
// deleting a role takes it out of every delegation that stewards it, and
// each delegation stays valid with its other roles, or with none.
func TestRbac_AnOwnerDeletingAStewardedRoleLeavesTheDelegationUsable(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email, otherEmail = "outliving-delegate@example.test", "outliving-other@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	otherID := h.createUser(t, owner, otherEmail, identity.RoleUser)
	targetID := h.createUser(t, owner, "outliving-target@example.test", identity.RoleUser)
	gone := createRole(t, owner, "Gone", "customers:view")
	kept := createRole(t, owner, "Kept", "customers:view")
	d := delegate(t, h, owner, delegateID, []uuid.UUID{gone.ID, kept.ID}, "customers:view")
	only := delegate(t, h, owner, otherID, []uuid.UUID{gone.ID})
	delegated, other := h.login(t, email, userPassword), h.login(t, otherEmail, userPassword)

	if r := deleteRole(owner, gone.ID, gone.Version); r.status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegation_roles WHERE role_id = $1`, gone.ID); n != 0 {
		t.Errorf("%d stewardships of the deleted role remain", n)
	}
	if !manages(t, delegated) || !manages(t, other) {
		t.Fatal("a delegation that stewarded the deleted role no longer grants management")
	}
	if sc := administrationOf(t, delegated).scopeOf(t, d.ID); !slices.Equal(sc.StewardedRoleIDs, []uuid.UUID{kept.ID}) || !slices.Equal(sc.AssignableRoleIDs, []uuid.UUID{kept.ID}) {
		t.Errorf("the delegate's scope %+v, want Kept stewarded and assignable", sc)
	}
	if sc := administrationOf(t, other).scopeOf(t, only.ID); len(sc.StewardedRoleIDs) != 0 {
		t.Errorf("the other delegate's scope %+v, want no roles", sc)
	}
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID, kept.ID); r.status != http.StatusOK {
		t.Errorf("assign Kept under the delegation: status %d body %s", r.status, r.body)
	}
	r := owner.do(http.MethodGet, delegationsPath, nil)
	var listed []struct {
		ID               uuid.UUID   `json:"id"`
		StewardedRoleIDs []uuid.UUID `json:"stewardedRoleIds"`
	}
	r.json(&listed)
	for _, l := range listed {
		if slices.Contains(l.StewardedRoleIDs, gone.ID) {
			t.Errorf("delegation %s still lists the deleted role: %s", l.ID, r.body)
		}
	}
}

// TestDelegations_AnUpdateQueuedBehindARoleDeletionIsRefused is why
// validation holds the stewarded roles FOR KEY SHARE: a gate deletes a role,
// uncommitted and so holding its row, while an update that names it waits
// for that row. Once the gate commits, the role is missing, and the update
// is refused rather than leaving the delegation naming a role that no
// longer exists, which would disable it entirely.
func TestDelegations_AnUpdateQueuedBehindARoleDeletionIsRefused(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "queued-update@example.test"
	granteeID := h.createUser(t, owner, email, identity.RoleUser)
	kept := createRole(t, owner, "Kept")
	doomed := createRole(t, owner, "Doomed")
	d := delegate(t, h, owner, granteeID, []uuid.UUID{kept.ID})

	deleting := gate(t, h,
		stmt(`DELETE FROM identity.authorization_delegation_roles WHERE role_id = $1`, doomed.ID),
		stmt(`DELETE FROM identity.roles WHERE id = $1`, doomed.ID))
	out := raceBehind(t, h, deleting, func() *resp {
		return updateDelegation(owner, d.ID, d.Version, delegationBody(granteeID, h.now().Add(time.Hour), []uuid.UUID{kept.ID, doomed.ID}, nil))
	})
	wantFlat(t, "the update behind the deletion", out[0], http.StatusBadRequest, "invalid_delegation_roles", "Only custom roles may be stewarded.")
	grantee := h.login(t, email, userPassword)
	if sc := administrationOf(t, grantee).scopeOf(t, d.ID); !slices.Equal(sc.StewardedRoleIDs, []uuid.UUID{kept.ID}) {
		t.Errorf("the scope %+v, want Kept alone", sc)
	}
}

// Ported from IdentityDelegationMalformedMetadataTests.MissingReferencedRoleMetadataFailsClosedForDelegatedManagement.
// .NET deleted the role's metadata row, leaving the role and the
// delegation's reference to it. Go keeps a role's metadata on its roles
// row, and the role deletion endpoint takes the role out of every
// delegation (as .NET's foreign key cascaded), so the dangling reference is
// made directly in the database: a role deleted around the endpoint, or a
// stewarded role id that names no role. authorization_delegation_roles has
// no foreign key, so either reference survives and must fail closed.
// Extended to the other references that fail closed: a role since made
// protected, and a key the catalog lacks or does not let be delegated. Each
// delegate manages until their delegation is corrupted, and not after, at
// the very next request.
func TestDelegation_AMissingOrProtectedReferenceFailsClosed(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	for i, c := range []struct {
		name    string
		corrupt func(t *testing.T, roleID, delegationID uuid.UUID)
	}{
		{"the stewarded role deleted around the endpoint", func(t *testing.T, roleID, _ uuid.UUID) {
			h.exec(t, `DELETE FROM identity.roles WHERE id = $1`, roleID)
		}},
		{"a stewarded role id that names no role", func(t *testing.T, _, delegationID uuid.UUID) {
			h.exec(t, `INSERT INTO identity.authorization_delegation_roles (delegation_id, role_id) VALUES ($1, $2)`, delegationID, uuid.New())
		}},
		{"the stewarded role became a system role", func(t *testing.T, roleID, _ uuid.UUID) {
			h.exec(t, `UPDATE identity.roles SET is_system = true WHERE id = $1`, roleID)
		}},
		{"the stewarded role became built in", func(t *testing.T, roleID, _ uuid.UUID) {
			h.exec(t, `UPDATE identity.roles SET is_built_in = true WHERE id = $1`, roleID)
		}},
		{"a key the catalog lacks", func(t *testing.T, _, delegationID uuid.UUID) {
			h.exec(t, `INSERT INTO identity.authorization_delegation_permissions (delegation_id, permission_key) VALUES ($1, 'customers:delete')`, delegationID)
		}},
		{"a key that may not be delegated", func(t *testing.T, _, delegationID uuid.UUID) {
			h.exec(t, `INSERT INTO identity.authorization_delegation_permissions (delegation_id, permission_key) VALUES ($1, 'identity:manage')`, delegationID)
		}},
	} {
		// Not parallel: the subtests share the harness, and concurrent
		// POST /owner/users lose serializable races to one another.
		t.Run(c.name, func(t *testing.T) {
			email := fmt.Sprintf("malformed-%d@example.test", i)
			delegateID := h.createUser(t, owner, email, identity.RoleUser)
			role := createRole(t, owner, fmt.Sprintf("rbac-malformed-%d", i))
			d := delegate(t, h, owner, delegateID, []uuid.UUID{role.ID})
			delegated := h.login(t, email, userPassword)
			if !manages(t, delegated) {
				t.Fatal("the delegate is refused before the delegation is corrupted")
			}
			c.corrupt(t, role.ID, d.ID)
			if manages(t, delegated) {
				t.Error("the delegate still manages")
			}
			if me := administrationOf(t, delegated); me.CanManageAuthorization || me.AdministrationScope != nil {
				t.Errorf("/access/me %+v, want no authority", me)
			}
		})
	}
}

// TestDelegations_CreateValidatesInDotNetsOrderAndAudits covers creation:
// each of .NET's refusals (EA/AuthorizationManagementEndpoints.cs:719-740)
// and their order, none of which creates anything; then the stored
// delegation, its keys once each, its Location, the exact
// delegation.created row, and the list, by id descending.
func TestDelegations_CreateValidatesInDotNetsOrderAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	granteeID := h.createUser(t, owner, "grantee@example.test", identity.RoleUser)
	otherOwnerID := h.createUser(t, owner, "second-owner@example.test", identity.RoleOwner)
	custom := createRole(t, owner, "Stewardable", "customers:view")

	valid := func() map[string]any {
		return delegationBody(granteeID, h.now().Add(time.Hour), []uuid.UUID{custom.ID}, []string{"customers:view"})
	}
	with := func(kv ...any) map[string]any {
		b := valid()
		for i := 0; i < len(kv); i += 2 {
			if kv[i+1] == nil {
				delete(b, kv[i].(string))
			} else {
				b[kv[i].(string)] = kv[i+1]
			}
		}
		return b
	}
	const (
		invalid     = "A delegation must target another user and have valid scope."
		target      = "Owner accounts cannot be delegated."
		permissions = "Delegations may contain only registered delegable permissions."
		roles       = "Only custom roles may be stewarded."
	)
	for _, c := range []struct {
		name          string
		body          map[string]any
		code, message string
	}{
		{"the caller as grantee", with("granteeUserId", ownerID), "invalid_delegation", invalid},
		{"no key list", with("permissionKeys", nil), "invalid_delegation", invalid},
		{"no role list", with("stewardedRoleIds", nil), "invalid_delegation", invalid},
		{"an expiry of now", with("expiresAt", h.now()), "invalid_delegation", invalid},
		{"an expiry past", with("expiresAt", h.now().Add(-time.Second)), "invalid_delegation", invalid},
		{"an expiry past, before an unknown grantee", with("expiresAt", h.now().Add(-time.Second), "granteeUserId", uuid.New()), "invalid_delegation", invalid},
		{"an Owner grantee", with("granteeUserId", otherOwnerID), "invalid_delegation_target", target},
		{"an Owner grantee, before a non-delegable key", with("granteeUserId", otherOwnerID, "permissionKeys", []string{"identity:manage"}), "invalid_delegation_target", target},
		{"a key the catalog lacks", with("permissionKeys", []string{"customers:delete"}), "invalid_delegation_permissions", permissions},
		{"a non-delegable key", with("permissionKeys", []string{"customers:view", "identity:manage"}), "invalid_delegation_permissions", permissions},
		{"a non-delegable key, before a built-in role", with("permissionKeys", []string{"identity:manage"}, "stewardedRoleIds", []uuid.UUID{identity.RoleUserID}), "invalid_delegation_permissions", permissions},
		{"a built-in role", with("stewardedRoleIds", []uuid.UUID{identity.RoleUserID}), "invalid_delegation_roles", roles},
		{"the Owner role", with("stewardedRoleIds", []uuid.UUID{identity.RoleOwnerID}), "invalid_delegation_roles", roles},
		{"an unknown role", with("stewardedRoleIds", []uuid.UUID{custom.ID, uuid.New()}), "invalid_delegation_roles", roles},
		{"a role named twice", with("stewardedRoleIds", []uuid.UUID{custom.ID, custom.ID}), "invalid_delegation_roles", roles},
	} {
		wantFlat(t, c.name, owner.do(http.MethodPost, delegationsPath, c.body), http.StatusBadRequest, c.code, c.message)
	}
	wantFlat(t, "a null body", owner.do(http.MethodPost, delegationsPath, nil, rawBody("application/json", []byte("null"))),
		http.StatusBadRequest, "invalid_delegation", invalid)
	for name, body := range map[string]map[string]any{
		"an unknown grantee":                    with("granteeUserId", uuid.New()),
		"no grantee":                            with("granteeUserId", nil),
		"an unknown grantee, before a bad role": with("granteeUserId", uuid.New(), "stewardedRoleIds", []uuid.UUID{identity.RoleUserID}),
	} {
		if r := owner.do(http.MethodPost, delegationsPath, body); r.status != http.StatusNotFound || len(r.body) != 0 {
			t.Errorf("%s: status %d body %s, want the bare 404", name, r.status, r.body)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations`); n != 0 {
		t.Fatalf("%d delegations were created by refused requests", n)
	}

	r := owner.do(http.MethodPost, delegationsPath, map[string]any{
		"granteeUserId": granteeID, "expiresAt": nil, "permissionKeys": []string{"customers:view", "customers:view"}, "stewardedRoleIds": []uuid.UUID{custom.ID},
	})
	var first createdDelegation
	r.json(&first)
	if r.status != http.StatusCreated || first.Version != h.version(t, "authorization_delegations", first.ID) {
		t.Fatalf("create: status %d body %s", r.status, r.body)
	}
	if got, want := r.header("Location"), delegationsPath+"/"+first.ID.String(); got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations WHERE id = $1 AND grantee_user_id = $2 AND created_by_user_id = $3
	        AND NOT can_create_roles AND expires_at IS NULL AND revoked_at IS NULL AND created_at = $4`, first.ID, granteeID, ownerID, h.now()); n != 1 {
		t.Error("the stored delegation is not the one requested")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegation_permissions WHERE delegation_id = $1`, first.ID); n != 1 {
		t.Errorf("%d permission rows, want the key once", n)
	}
	events := h.auditEvents(t, "delegation.created")
	wantAfter := fmt.Sprintf(`{"Id":"%s","GranteeUserId":"%s","CreatedByUserId":"%s","ExpiresAt":null,"RevokedAt":null,"CanCreateRoles":false,`+
		`"PermissionKeys":["customers:view","customers:view"],"StewardedRoleIds":["%s"]}`, first.ID, granteeID, ownerID, custom.ID)
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != granteeID ||
		events[0].TargetRole != nil || events[0].Details != `{"action":"delegation.created"}` || events[0].Before != `{"Delegation":null}` ||
		events[0].After != wantAfter || !events[0].At.Equal(h.now()) {
		t.Errorf("delegation.created rows %+v, want after %s", events, wantAfter)
	}

	expires := h.now().Add(time.Hour)
	second := delegate(t, h, owner, granteeID, nil, "customers:create")
	if after := h.auditEvents(t, "delegation.created")[1].After; !strings.Contains(after, `"ExpiresAt":"`+expires.Format(time.RFC3339Nano)+`"`) {
		t.Errorf("the second delegation.created after %s, want its expiry", after)
	}

	r = owner.do(http.MethodGet, delegationsPath, nil)
	var listed []map[string]json.RawMessage
	r.json(&listed)
	if r.status != http.StatusOK || len(listed) != 2 {
		t.Fatalf("list: status %d body %s", r.status, r.body)
	}
	order := []createdDelegation{first, second}
	slices.SortFunc(order, func(a, b createdDelegation) int { return strings.Compare(b.ID.String(), a.ID.String()) })
	wants := map[uuid.UUID]map[string]string{
		first.ID: {
			"id": `"` + first.ID.String() + `"`, "granteeUserId": `"` + granteeID.String() + `"`, "expiresAt": "null", "revokedAt": "null",
			"version": `"` + first.Version + `"`, "canCreateRoles": "false", "permissionKeys": `["customers:view"]`, "stewardedRoleIds": `["` + custom.ID.String() + `"]`,
		},
		second.ID: {
			"id": `"` + second.ID.String() + `"`, "granteeUserId": `"` + granteeID.String() + `"`, "expiresAt": `"` + expires.Format(time.RFC3339Nano) + `"`, "revokedAt": "null",
			"version": `"` + second.Version + `"`, "canCreateRoles": "true", "permissionKeys": `["customers:create"]`, "stewardedRoleIds": "[]",
		},
	}
	for i, d := range order {
		if len(listed[i]) != len(wants[d.ID]) {
			t.Errorf("entry %d has the members %v", i, fieldsOf(listed[i]))
		}
		for k, v := range wants[d.ID] {
			if string(listed[i][k]) != v {
				t.Errorf("entry %d %s = %s, want %s", i, k, listed[i][k], v)
			}
		}
	}
}

// TestDelegations_UpdateReplacesTheScopeAndAudits covers the update, in
// .NET's order (EA/AuthorizationManagementEndpoints.cs:626-677): no stamp,
// then an unknown delegation, then a stale stamp, then validation, none of
// which changes anything; then the grantee, flag, expiry, keys and roles
// replaced, the exact delegation.updated row, the old grantee's authority
// gone at once, and a revoked delegation that stays revoked.
func TestDelegations_UpdateReplacesTheScopeAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	firstID := h.createUser(t, owner, "first@example.test", identity.RoleUser)
	secondID := h.createUser(t, owner, "second@example.test", identity.RoleUser)
	otherOwnerID := h.createUser(t, owner, "update-owner@example.test", identity.RoleOwner)
	alpha := createRole(t, owner, "Alpha", "customers:view")
	beta := createRole(t, owner, "Beta", "customers:create")
	expires := h.now().Add(time.Hour)
	d := delegate(t, h, owner, firstID, []uuid.UUID{alpha.ID}, "customers:view")
	first := h.login(t, "first@example.test", userPassword)
	changed := "The delegation changed concurrently."

	replacement := func() map[string]any {
		b := delegationBody(secondID, expires.Add(time.Hour), []uuid.UUID{beta.ID}, []string{"customers:create", "customers:create"})
		b["canCreateRoles"] = false
		return b
	}
	wantFlat(t, "no stamp", updateDelegation(owner, d.ID, nil, replacement()), http.StatusConflict, "delegation_conflict", "A delegation version is required.")
	wantFlat(t, "no body", owner.do(http.MethodPut, delegationsPath+"/"+d.ID.String(), nil, rawBody("application/json", []byte("null"))),
		http.StatusConflict, "delegation_conflict", "A delegation version is required.")
	if r := updateDelegation(owner, uuid.New(), d.Version, replacement()); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown delegation: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "a stale stamp", updateDelegation(owner, d.ID, "stale", replacement()), http.StatusConflict, "delegation_conflict", changed)
	stale := replacement()
	stale["granteeUserId"] = otherOwnerID
	wantFlat(t, "a stale stamp, before validation", updateDelegation(owner, d.ID, "stale", stale), http.StatusConflict, "delegation_conflict", changed)
	wantFlat(t, "an Owner grantee", updateDelegation(owner, d.ID, d.Version, stale), http.StatusBadRequest, "invalid_delegation_target", "Owner accounts cannot be delegated.")
	if v := h.version(t, "authorization_delegations", d.ID); v != d.Version || !manages(t, first) {
		t.Fatal("a refused update changed the delegation")
	}

	r := updateDelegation(owner, d.ID, d.Version, replacement())
	var updated createdDelegation
	r.json(&updated)
	if r.status != http.StatusOK || updated.ID != d.ID || updated.Version == d.Version || updated.Version != h.version(t, "authorization_delegations", d.ID) {
		t.Fatalf("update: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations WHERE id = $1 AND grantee_user_id = $2 AND created_by_user_id = $3
	        AND NOT can_create_roles AND expires_at = $4 AND revoked_at IS NULL`, d.ID, secondID, ownerID, expires.Add(time.Hour)); n != 1 {
		t.Error("the stored delegation is not the replacement")
	}
	events := h.auditEvents(t, "delegation.updated")
	wantBefore := fmt.Sprintf(`{"Id":"%s","GranteeUserId":"%s","CreatedByUserId":"%s","ExpiresAt":"%s","RevokedAt":null,"CanCreateRoles":true,`+
		`"PermissionKeys":["customers:view"],"StewardedRoleIds":["%s"]}`, d.ID, firstID, ownerID, expires.Format(time.RFC3339Nano), alpha.ID)
	wantAfter := fmt.Sprintf(`{"Id":"%s","GranteeUserId":"%s","CreatedByUserId":"%s","ExpiresAt":"%s","RevokedAt":null,"CanCreateRoles":false,`+
		`"PermissionKeys":["customers:create","customers:create"],"StewardedRoleIds":["%s"]}`, d.ID, secondID, ownerID, expires.Add(time.Hour).Format(time.RFC3339Nano), beta.ID)
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != secondID ||
		events[0].Before != wantBefore || events[0].After != wantAfter || events[0].Details != `{"action":"delegation.updated"}` {
		t.Errorf("delegation.updated rows %+v\nwant before %s\n     after  %s", events, wantBefore, wantAfter)
	}
	if manages(t, first) {
		t.Error("the former grantee still manages, on the same session")
	}
	second := h.login(t, "second@example.test", userPassword)
	if sc := administrationOf(t, second).scopeOf(t, d.ID); sc.CanCreateRoles || !slices.Equal(sc.GrantablePermissionKeys, []string{"customers:create"}) ||
		!slices.Equal(sc.StewardedRoleIDs, []uuid.UUID{beta.ID}) {
		t.Errorf("the new grantee's scope %+v", sc)
	}

	h.advance(time.Minute)
	r = revokeDelegation(owner, d.ID, updated.Version)
	var revoked struct {
		Version   string    `json:"version"`
		RevokedAt time.Time `json:"revokedAt"`
	}
	r.json(&revoked)
	if r.status != http.StatusOK {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	if r := updateDelegation(owner, d.ID, revoked.Version, replacement()); r.status != http.StatusOK {
		t.Fatalf("update a revoked delegation: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations WHERE id = $1 AND revoked_at = $2`, d.ID, revoked.RevokedAt); n != 1 {
		t.Error("the update un-revoked the delegation")
	}
	if manages(t, h.login(t, "second@example.test", userPassword)) {
		t.Error("the grantee manages under a revoked delegation")
	}
}

// TestDelegations_RevokeEndsTheGranteesSessionsAndResetLinks is the spec's
// revocation rule for a delegation revoke (docs/superpowers/specs/2026-09-11-identity-design.md,
// Sessions): every session of the grantee ends and the reset link they
// hold is dead, while the Owner's session stays. It also covers the
// refusals, which change nothing, the exact response and delegation.revoked
// row, the grantee's lost authority, and a second revoke, which moves
// revoked_at as .NET's did.
func TestDelegations_RevokeEndsTheGranteesSessionsAndResetLinks(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	const email = "revoked@example.test"
	granteeID := h.createUser(t, owner, email, identity.RoleUser)
	role := createRole(t, owner, "Stewarded", "customers:view")
	expires := h.now().Add(time.Hour)
	d := delegate(t, h, owner, granteeID, []uuid.UUID{role.ID}, "customers:view")
	grantee, other := h.login(t, email, userPassword), h.login(t, email, userPassword)
	if !manages(t, grantee) {
		t.Fatal("the grantee is refused before the revoke")
	}
	requestRecovery(t, h, email)
	token := mailedResetToken(t, h, email)

	if r := revokeDelegation(owner, uuid.New(), d.Version); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown delegation: status %d body %s", r.status, r.body)
	}
	changed := "The delegation changed concurrently."
	wantFlat(t, "no body", owner.do(http.MethodPost, delegationsPath+"/"+d.ID.String()+"/revoke", nil, rawBody("application/json", []byte("null"))),
		http.StatusConflict, "delegation_conflict", changed)
	wantFlat(t, "no stamp", revokeDelegation(owner, d.ID, nil), http.StatusConflict, "delegation_conflict", changed)
	wantFlat(t, "a stale stamp", revokeDelegation(owner, d.ID, "stale"), http.StatusConflict, "delegation_conflict", changed)
	admitted(t, grantee)
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, granteeID); n != 1 {
		t.Fatalf("%d reset tokens after the refusals, want the one requested", n)
	}

	// The clock carries nanoseconds timestamptz cannot hold, as time.Now
	// does in production: revokedAt is answered at the stored precision.
	h.advance(time.Minute + 1500*time.Nanosecond)
	revokedAt := `"` + h.now().Truncate(time.Microsecond).Format(time.RFC3339Nano) + `"`
	r := revokeDelegation(owner, d.ID, d.Version)
	var body map[string]json.RawMessage
	r.json(&body)
	version := h.version(t, "authorization_delegations", d.ID)
	if r.status != http.StatusOK || !slices.Equal(fieldsOf(body), []string{"id", "revokedAt", "version"}) || string(body["id"]) != `"`+d.ID.String()+`"` ||
		string(body["version"]) != `"`+version+`"` || version == d.Version || string(body["revokedAt"]) != revokedAt {
		t.Fatalf("revoke: status %d body %s, want revokedAt %s", r.status, r.body, revokedAt)
	}
	var listed []map[string]json.RawMessage
	owner.do(http.MethodGet, delegationsPath, nil).json(&listed)
	if len(listed) != 1 || string(listed[0]["revokedAt"]) != revokedAt {
		t.Errorf("GET /access/delegations reads back %s, want the revokedAt answered, %s", listed, revokedAt)
	}
	rejected(t, grantee)
	rejected(t, other)
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, granteeID); n != 0 {
		t.Errorf("%d reset tokens survive the revoke", n)
	}
	if r := reset(h, t, email, token, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("the reset link after the revoke: status %d body %s, want 400 invalid_reset_token", r.status, r.body)
	}
	admitted(t, owner)

	events := h.auditEvents(t, "delegation.revoked")
	state := func(revokedAt string) string {
		return fmt.Sprintf(`{"Id":"%s","GranteeUserId":"%s","CreatedByUserId":"%s","ExpiresAt":"%s","RevokedAt":%s,"CanCreateRoles":true,`+
			`"PermissionKeys":["customers:view"],"StewardedRoleIds":["%s"]}`, d.ID, granteeID, ownerID, expires.Format(time.RFC3339Nano), revokedAt, role.ID)
	}
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != granteeID ||
		events[0].Before != state("null") || events[0].After != state(revokedAt) || events[0].Details != `{"action":"delegation.revoked"}` {
		t.Errorf("delegation.revoked rows %+v", events)
	}
	again := h.login(t, email, userPassword)
	if manages(t, again) {
		t.Error("the grantee manages after the revoke")
	}
	if me := administrationOf(t, again); me.CanManageAuthorization || me.AdministrationScope != nil {
		t.Errorf("/access/me after the revoke: %+v", me)
	}

	h.advance(time.Minute)
	wantFlat(t, "the replaced stamp", revokeDelegation(owner, d.ID, d.Version), http.StatusConflict, "delegation_conflict", changed)
	if r := revokeDelegation(owner, d.ID, version); r.status != http.StatusOK {
		t.Fatalf("a second revoke: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations WHERE id = $1 AND revoked_at = $2`, d.ID, h.now().Truncate(time.Microsecond)); n != 1 {
		t.Error("the second revoke did not move revoked_at")
	}
	rejected(t, again)
}

// TestDelegations_ARevokedOrExpiredDelegationDeniesTheNextRequest proves
// AuthorizationManagement reads delegations afresh on every request: on
// one session, the grantee manages until the second their delegation
// expires and not after, and a revoke written straight to the database,
// which ends no session, refuses the grantee's very next request.
func TestDelegations_ARevokedOrExpiredDelegationDeniesTheNextRequest(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	expiringID := h.createUser(t, owner, "expiring@example.test", identity.RoleUser)
	if r := owner.do(http.MethodPost, delegationsPath, delegationBody(expiringID, h.now().Add(10*time.Minute), nil, nil)); r.status != http.StatusCreated {
		t.Fatalf("delegate: status %d body %s", r.status, r.body)
	}
	expiring := h.login(t, "expiring@example.test", userPassword)
	h.advance(10*time.Minute - time.Second)
	if !manages(t, expiring) || !administrationOf(t, expiring).CanManageAuthorization {
		t.Fatal("the grantee is refused a second before the expiry")
	}
	h.advance(time.Second)
	if manages(t, expiring) {
		t.Error("the grantee manages at the expiry")
	}
	if me := administrationOf(t, expiring); me.CanManageAuthorization || me.AdministrationScope != nil {
		t.Errorf("/access/me at the expiry: %+v", me)
	}

	revokedID := h.createUser(t, owner, "revoked-directly@example.test", identity.RoleUser)
	d := delegate(t, h, owner, revokedID, nil)
	revoked := h.login(t, "revoked-directly@example.test", userPassword)
	if !manages(t, revoked) {
		t.Fatal("the grantee is refused before the revoke")
	}
	h.exec(t, `UPDATE identity.authorization_delegations SET revoked_at = $2 WHERE id = $1`, d.ID, h.now())
	admitted(t, revoked)
	if manages(t, revoked) {
		t.Error("the grantee manages after the revoke, on the same session")
	}
}

// TestMfa_ADelegateMustUseMFAAndCannotDisableIt covers MFA for delegated
// administrators while Owners must use it: AuthorizationManagement holds a
// delegate to MFA as it holds an Owner (EA/AuthServiceCollectionExtensions.cs:301-303),
// and an active delegate may not turn MFA off, 403
// delegated_admin_mfa_required (EA/AuthAccountEndpoints.cs:253-264). Once
// the delegation has expired, they may.
func TestMfa_ADelegateMustUseMFAAndCannotDisableIt(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	h.enrollTOTP(t, owner, ownerPassword)
	const email = "mfa-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	delegate(t, h, owner, delegateID, nil)

	c := h.login(t, email, userPassword)
	if manages(t, c) {
		t.Error("a delegate manages with a password-only session")
	}
	h.enrollTOTP(t, c, userPassword)
	if !manages(t, c) {
		t.Error("a delegate is refused with an MFA session")
	}
	for _, base := range []string{accountMFAPath, ownerMFAPath} {
		r := c.do(http.MethodPost, base+"/disable", map[string]any{"password": userPassword})
		if r.status != http.StatusForbidden || r.code() != "delegated_admin_mfa_required" ||
			!strings.Contains(string(r.body), "MFA cannot be disabled while privileged management access requires it.") {
			t.Errorf("%s/disable: status %d body %s, want 403 delegated_admin_mfa_required", base, r.status, r.body)
		}
	}
	if !readTOTP(t, h, delegateID).enabled {
		t.Fatal("a refused disable turned the delegate's TOTP off")
	}

	h.advance(time.Hour)
	if r := c.do(http.MethodPost, accountMFAPath+"/disable", map[string]any{"password": userPassword}); r.status != http.StatusOK {
		t.Errorf("disable once the delegation expired: status %d body %s", r.status, r.body)
	}
}

// TestRbac_ADelegateCreatesRolesTheirDelegationStewards covers a
// delegate's role creation (AZ/AuthorizationMutationService.cs:33-54,
// EA/AuthorizationManagementEndpoints.cs:156-162): the delegation must be
// named, be the delegate's, allow creating roles and cover every key;
// validation comes first and role_exists after. A created role has the
// delegate as steward, joins the delegation's stewarded roles, and can be
// assigned under it.
func TestRbac_ADelegateCreatesRolesTheirDelegationStewards(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "creating-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "creating-target@example.test", identity.RoleUser)
	otherID := h.createUser(t, owner, "creating-other@example.test", identity.RoleUser)
	creating := delegate(t, h, owner, delegateID, nil, "customers:view")
	body := delegationBody(delegateID, h.now().Add(time.Hour), nil, []string{"customers:view"})
	body["canCreateRoles"] = false
	r := owner.do(http.MethodPost, delegationsPath, body)
	var nonCreating createdDelegation
	r.json(&nonCreating)
	if r.status != http.StatusCreated {
		t.Fatalf("delegate without role creation: status %d body %s", r.status, r.body)
	}
	others := delegate(t, h, owner, otherID, nil, "customers:view")

	delegated := h.login(t, email, userPassword)
	wantFlat(t, "an unknown key", createRoleUnder(delegated, "Delegated", &creating.ID, "customers:delete"),
		http.StatusBadRequest, "invalid_role", "Role metadata and registered permission keys are required.")
	wantFlat(t, "no delegation named", createRoleUnder(delegated, "Delegated", nil, "customers:view"),
		http.StatusForbidden, "delegation_required", "A delegation must be selected.")
	createDenied := "The selected delegation cannot create this role."
	for name, id := range map[string]uuid.UUID{"an unknown delegation": uuid.New(), "another user's delegation": others.ID, "one that may not create roles": nonCreating.ID} {
		wantFlat(t, name, createRoleUnder(delegated, "Delegated", &id, "customers:view"), http.StatusForbidden, "role_create_denied", createDenied)
	}
	boundary := "The role permissions exceed the selected delegation boundary."
	wantFlat(t, "a key outside the delegation", createRoleUnder(delegated, "Delegated", &creating.ID, "customers:view", "customers:create"),
		http.StatusForbidden, "role_boundary_exceeded", boundary)
	wantFlat(t, "a non-delegable key", createRoleUnder(delegated, "Delegated", &creating.ID, "identity:manage"),
		http.StatusForbidden, "role_boundary_exceeded", boundary)
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE NOT is_built_in`); n != 0 {
		t.Fatalf("%d roles were created by refused requests", n)
	}

	r = createRoleUnder(delegated, "Delegated", &creating.ID, "customers:view")
	var role createdRole
	r.json(&role)
	if r.status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1 AND steward_user_id = $2`, role.ID, delegateID); n != 1 {
		t.Error("the role's steward is not the delegate")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegation_roles WHERE delegation_id = $1 AND role_id = $2`, creating.ID, role.ID); n != 1 {
		t.Error("the delegation does not steward the role")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegation_roles WHERE role_id = $1`, role.ID); n != 1 {
		t.Error("another delegation stewards the role too")
	}
	if events := h.auditEvents(t, "role.created"); len(events) != 1 || events[0].Actor == nil || *events[0].Actor != delegateID {
		t.Errorf("role.created rows %+v, want one by the delegate", events)
	}
	sc := administrationOf(t, delegated).scopeOf(t, creating.ID)
	if !slices.Equal(sc.StewardedRoleIDs, []uuid.UUID{role.ID}) || !slices.Equal(sc.AssignableRoleIDs, []uuid.UUID{role.ID}) {
		t.Errorf("the scope %+v, want the new role stewarded and assignable", sc)
	}
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &creating.ID, role.ID); r.status != http.StatusOK {
		t.Errorf("assign the new role: status %d body %s", r.status, r.body)
	}
	wantFlat(t, "the name again", createRoleUnder(delegated, "delegated", &creating.ID, "customers:view"),
		http.StatusConflict, "role_exists", "The normalized role name is already in use.")
}

// TestRbac_ADelegateActsUnderOneScopeAtATime is the brief's confinement
// rule in every mutation: a delegate with scope A (R1 and R3; customers:view)
// and scope B (R2; customers:create) cannot give R1 B's key, nor R2 A's,
// nor touch R3, which A stewards but whose key only B grants; cannot
// create a role with both keys; and cannot assign a role under a scope
// that cannot. Under each scope alone it can, and a replacement under A
// leaves B's role in place.
func TestRbac_ADelegateActsUnderOneScopeAtATime(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "two-scope-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "two-scope-target@example.test", identity.RoleUser)
	r1 := createRole(t, owner, "R1", "customers:view")
	r2 := createRole(t, owner, "R2", "customers:create")
	r3 := createRole(t, owner, "R3", "customers:create")
	a := delegate(t, h, owner, delegateID, []uuid.UUID{r1.ID, r3.ID}, "customers:view")
	b := delegate(t, h, owner, delegateID, []uuid.UUID{r2.ID}, "customers:create")
	delegated := h.login(t, email, userPassword)
	if sc := administrationOf(t, delegated).scopeOf(t, a.ID); !slices.Equal(sc.AssignableRoleIDs, []uuid.UUID{r1.ID}) {
		t.Errorf("scope A assigns %v, want R1 alone", sc.AssignableRoleIDs)
	}

	editDenied := "Only delegated stewarded custom roles within the selected delegation boundary may be changed."
	wantFlat(t, "R1 gaining B's key", updateRole(delegated, r1.ID, "R1", r1.Version, "customers:view", "customers:create"), http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "R2 gaining A's key", updateRole(delegated, r2.ID, "R2", r2.Version, "customers:create", "customers:view"), http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "R3 kept as it is", updateRole(delegated, r3.ID, "R3", r3.Version, "customers:create"), http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "R3 shedding its key", updateRole(delegated, r3.ID, "R3", r3.Version), http.StatusForbidden, "role_edit_denied", editDenied)
	wantFlat(t, "R3 deleted", deleteRole(delegated, r3.ID, r3.Version),
		http.StatusForbidden, "role_delete_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be deleted.")
	for name, id := range map[string]uuid.UUID{"A": a.ID, "B": b.ID} {
		wantFlat(t, "a role with both keys under "+name, createRoleUnder(delegated, "Both", &id, "customers:view", "customers:create"),
			http.StatusForbidden, "role_boundary_exceeded", "The role permissions exceed the selected delegation boundary.")
	}
	assignDenied := "The requested role is outside the selected delegation scope."
	for name, c := range map[string]struct {
		under uuid.UUID
		role  uuid.UUID
	}{"R2 under A": {a.ID, r2.ID}, "R1 under B": {b.ID, r1.ID}, "R3 under A": {a.ID, r3.ID}, "R3 under B": {b.ID, r3.ID}} {
		wantFlat(t, "assign "+name, assignUnder(delegated, targetID, h.version(t, "users", targetID), &c.under, c.role), http.StatusForbidden, "assignment_denied", assignDenied)
	}
	for id, want := range map[uuid.UUID][]string{r1.ID: {"customers:view"}, r2.ID: {"customers:create"}, r3.ID: {"customers:create"}} {
		if got := h.rolePermissions(t, id); !slices.Equal(got, want) {
			t.Errorf("role %s carries %v, want %v", id, got, want)
		}
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("the target holds %v after the refusals", got)
	}

	if r := updateRole(delegated, r1.ID, "R1", r1.Version, "customers:view"); r.status != http.StatusOK {
		t.Errorf("R1 edited within A: status %d body %s", r.status, r.body)
	}
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &a.ID, r1.ID); r.status != http.StatusOK {
		t.Fatalf("R1 under A: status %d body %s", r.status, r.body)
	}
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &b.ID, r1.ID, r2.ID); r.status != http.StatusOK {
		t.Fatalf("R2 under B, keeping R1: status %d body %s", r.status, r.body)
	}
	if r := assignUnder(delegated, targetID, h.version(t, "users", targetID), &a.ID); r.status != http.StatusOK {
		t.Fatalf("an empty replacement under A: status %d body %s", r.status, r.body)
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{"R2", identity.RoleUser}) {
		t.Errorf("the target holds %v, want R2, which A cannot take away, and User", got)
	}
}

// TestAccessUsers_ADelegateInspectsOnlyUsersOneScopeCovers covers GET
// /access/users/{id} for a delegate (AZ/AuthorizationMutationService.cs:249-260):
// a user whose every custom role one scope can assign, or who has none, is
// shown; a user whose roles only two scopes together cover, or who holds a
// role no scope assigns, an Owner, another delegate, the delegate
// themselves and an unknown user are each the bare 404.
func TestAccessUsers_ADelegateInspectsOnlyUsersOneScopeCovers(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	const email = "inspecting-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	r1 := createRole(t, owner, "R1", "customers:view")
	r2 := createRole(t, owner, "R2", "customers:create")
	unscoped := createRole(t, owner, "Unscoped")
	delegate(t, h, owner, delegateID, []uuid.UUID{r1.ID}, "customers:view")
	delegate(t, h, owner, delegateID, []uuid.UUID{r2.ID}, "customers:create")
	user := func(email string, roles ...uuid.UUID) uuid.UUID {
		id := h.createUser(t, owner, email, identity.RoleUser)
		if len(roles) > 0 {
			if r := assignRoles(owner, id, h.version(t, "users", id), roles...); r.status != http.StatusOK {
				t.Fatalf("assign %s: status %d body %s", email, r.status, r.body)
			}
		}
		return id
	}
	plain := user("plain@example.test")
	inR1 := user("in-r1@example.test", r1.ID)
	inBoth := user("in-both@example.test", r1.ID, r2.ID)
	outside := user("outside@example.test", r1.ID, unscoped.ID)
	otherDelegate := user("inspected-delegate@example.test")
	delegate(t, h, owner, otherDelegate, nil)

	delegated := h.login(t, email, userPassword)
	for name, id := range map[string]uuid.UUID{"a user without custom roles": plain, "a user one scope covers": inR1} {
		if r := delegated.do(http.MethodGet, accessPath+"/users/"+id.String(), nil); r.status != http.StatusOK {
			t.Errorf("%s: status %d body %s", name, r.status, r.body)
		}
	}
	for name, id := range map[string]uuid.UUID{
		"a user only both scopes cover": inBoth, "a user with a role no scope assigns": outside, "the Owner": ownerID,
		"another delegate": otherDelegate, "the delegate": delegateID, "an unknown user": uuid.New(),
	} {
		if r := delegated.do(http.MethodGet, accessPath+"/users/"+id.String(), nil); r.status != http.StatusNotFound || len(r.body) != 0 {
			t.Errorf("%s: status %d body %s, want the bare 404", name, r.status, r.body)
		}
	}
}

// TestDelegations_ADelegatedChangeQueuedBehindARevokeOrANarrowingIsRefused
// is what holding the caller's delegations FOR SHARE (heldDelegations)
// buys: a gate narrows the delegation, then revokes it, each uncommitted and
// so holding the delegation's row, while delegated changes under it wait
// for that row. Once the gate commits they read the delegation as it left
// it: the narrowed scope no longer assigns the role, and the revoked one
// grants nothing, although the requests passed AuthorizationManagement
// before the gate committed.
func TestDelegations_ADelegatedChangeQueuedBehindARevokeOrANarrowingIsRefused(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "queued-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	targetID := h.createUser(t, owner, "queued-target@example.test", identity.RoleUser)
	role := createRole(t, owner, "Guarded", "customers:view")
	d := delegate(t, h, owner, delegateID, []uuid.UUID{role.ID}, "customers:view")
	delegated := h.login(t, email, userPassword)

	narrowing := gate(t, h,
		stmt(`UPDATE identity.authorization_delegations SET version = gen_random_uuid() WHERE id = $1`, d.ID),
		stmt(`DELETE FROM identity.authorization_delegation_permissions WHERE delegation_id = $1`, d.ID))
	out := raceBehind(t, h, narrowing, func() *resp {
		return assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID, role.ID)
	})
	wantFlat(t, "the assignment behind the narrowing", out[0], http.StatusForbidden, "assignment_denied", "The requested role is outside the selected delegation scope.")
	h.exec(t, `INSERT INTO identity.authorization_delegation_permissions (delegation_id, permission_key) VALUES ($1, 'customers:view')`, d.ID)

	revoking := gate(t, h, stmt(`UPDATE identity.authorization_delegations SET revoked_at = $2, version = gen_random_uuid() WHERE id = $1`, d.ID, h.now()))
	out = raceBehind(t, h, revoking,
		func() *resp { return updateRole(delegated, role.ID, role.Name, role.Version) },
		func() *resp { return assignUnder(delegated, targetID, h.version(t, "users", targetID), &d.ID, role.ID) },
	)
	wantFlat(t, "the edit behind the revoke", out[0], http.StatusForbidden, "role_edit_denied",
		"Only delegated stewarded custom roles within the selected delegation boundary may be changed.")
	wantFlat(t, "the assignment behind the revoke", out[1], http.StatusForbidden, "delegation_invalid", "The selected delegation is not active and valid.")
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, []string{"customers:view"}) || h.version(t, "roles", role.ID) != role.Version {
		t.Errorf("the role changed: it carries %v", got)
	}
	if got := h.roleNames(t, targetID); !slices.Equal(got, []string{identity.RoleUser}) {
		t.Errorf("the target holds %v", got)
	}
}

// TestDelegations_AnUpdateAndARevokeWithOneVersionSucceedOnce races an
// update and a revoke of one delegation under one version, both queued
// behind a gate holding its row: exactly one succeeds, and the other sees
// the version it replaced.
func TestDelegations_AnUpdateAndARevokeWithOneVersionSucceedOnce(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	granteeID := h.createUser(t, owner, "raced-grantee@example.test", identity.RoleUser)
	d := delegate(t, h, owner, granteeID, nil, "customers:view")

	held := gate(t, h, stmt(`SELECT 1 FROM identity.authorization_delegations WHERE id = $1 FOR UPDATE`, d.ID))
	out := raceBehind(t, h, held,
		func() *resp {
			return updateDelegation(owner, d.ID, d.Version, delegationBody(granteeID, h.now().Add(2*time.Hour), nil, []string{"customers:create"}))
		},
		func() *resp { return revokeDelegation(owner, d.ID, d.Version) },
	)
	winner, loser := out[0], out[1]
	if winner.status != http.StatusOK {
		winner, loser = loser, winner
	}
	if winner.status != http.StatusOK {
		t.Fatalf("statuses %d / %d (bodies %s / %s), want one 200", out[0].status, out[1].status, out[0].body, out[1].body)
	}
	wantFlat(t, "the loser", loser, http.StatusConflict, "delegation_conflict", "The delegation changed concurrently.")
	if n := len(h.auditEvents(t, "delegation.updated")) + len(h.auditEvents(t, "delegation.revoked")); n != 1 {
		t.Errorf("%d delegation.updated and delegation.revoked rows, want 1", n)
	}
}

// TestDelegations_ACreateQueuedBehindAPromotionSeesTheOwner is why
// creation takes the owner lock: a gate holds it and makes the grantee an
// Owner, as owner user management does, while a delegation to them waits.
// Once the gate commits, the grantee is an Owner, and the delegation is
// refused.
func TestDelegations_ACreateQueuedBehindAPromotionSeesTheOwner(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	granteeID := h.createUser(t, owner, "promoted@example.test", identity.RoleUser)

	promoting := gate(t, h,
		stmt(`SELECT pg_advisory_xact_lock($1)`, int64(identity.OwnerMutationLock)),
		stmt(`INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, granteeID, identity.RoleOwnerID))
	out := raceBehind(t, h, promoting, func() *resp {
		return owner.do(http.MethodPost, delegationsPath, delegationBody(granteeID, h.now().Add(time.Hour), nil, nil))
	})
	wantFlat(t, "the delegation behind the promotion", out[0], http.StatusBadRequest, "invalid_delegation_target", "Owner accounts cannot be delegated.")
	if n := h.count(t, `SELECT count(*) FROM identity.authorization_delegations`); n != 0 {
		t.Errorf("%d delegations, want none", n)
	}
}

// TestRbac_TwoScopesCannotCombineThroughConcurrentEdits races a
// delegate's two edits of one role, one within each of two scopes that
// both steward it, queued behind the role's lock. At most one wins, the
// role never carries both keys, and adding the other key afterwards is
// refused: the second edit sees the first's key under its own scope.
func TestRbac_TwoScopesCannotCombineThroughConcurrentEdits(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "concurrent-delegate@example.test"
	delegateID := h.createUser(t, owner, email, identity.RoleUser)
	role := createRole(t, owner, "Shared")
	delegate(t, h, owner, delegateID, []uuid.UUID{role.ID}, "customers:view")
	delegate(t, h, owner, delegateID, []uuid.UUID{role.ID}, "customers:create")
	delegated := h.login(t, email, userPassword)

	out := raceBehind(t, h, holdRoleLock(t, h, role.ID),
		func() *resp { return updateRole(delegated, role.ID, role.Name, role.Version, "customers:view") },
		func() *resp { return updateRole(delegated, role.ID, role.Name, role.Version, "customers:create") },
	)
	winner, loser := out[0], out[1]
	if winner.status != http.StatusOK {
		winner, loser = loser, winner
	}
	var won updatedRole
	winner.json(&won)
	if winner.status != http.StatusOK || len(won.Permissions) != 1 {
		t.Fatalf("statuses %d / %d (bodies %s / %s), want one 200", out[0].status, out[1].status, out[0].body, out[1].body)
	}
	wantFlat(t, "the loser", loser, http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")
	if got := h.rolePermissions(t, role.ID); !slices.Equal(got, won.Permissions) {
		t.Fatalf("the role carries %v, want the winner's %v", got, won.Permissions)
	}
	other := map[string]string{"customers:view": "customers:create", "customers:create": "customers:view"}[won.Permissions[0]]
	wantFlat(t, "adding the other scope's key", updateRole(delegated, role.ID, role.Name, won.Version, won.Permissions[0], other),
		http.StatusForbidden, "role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.")
	wantFlat(t, "swapping to the other scope's key", updateRole(delegated, role.ID, role.Name, won.Version, other),
		http.StatusForbidden, "role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.")
}

// TestDelegations_AnOwnerRevokingTheirOwnDelegationKeepsTheirSession
// covers the one revoke whose caller is the grantee: a delegate since made
// an Owner revokes their old delegation. Their other sessions end, as for
// any grantee, but the session they revoke from goes on, as a user's own
// change keeps the caller's session.
func TestDelegations_AnOwnerRevokingTheirOwnDelegationKeepsTheirSession(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "promoted-delegate@example.test"
	granteeID := h.createUser(t, owner, email, identity.RoleUser)
	d := delegate(t, h, owner, granteeID, nil)
	if r := owner.do(http.MethodPut, "/api/v1/identity/owner/users/"+granteeID.String(), map[string]string{"displayName": email, "email": email, "role": identity.RoleOwner}); r.status != http.StatusOK {
		t.Fatalf("promote: status %d body %s", r.status, r.body)
	}
	promoted, other := h.login(t, email, userPassword), h.login(t, email, userPassword)
	if r := revokeDelegation(promoted, d.ID, d.Version); r.status != http.StatusOK {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	admitted(t, promoted)
	rejected(t, other)
}
