package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const groupsPath = accessPath + "/groups"

// The refusal messages the access group tests expect, as .NET wrote them
// (EA/IdentityControlPlaneEndpoints.cs; SV/AccessGroupManagementService.cs).
const (
	invalidGroupMessage    = "A safe display name is required."
	groupExistsMessage     = "An access group with this display name already exists."
	stampRequiredMessage   = "A concurrency stamp is required."
	groupChangedMessage    = "The access group changed concurrently."
	resourceChangedMessage = "The resource changed concurrently."
	protectedRoleMessage   = "Owner, system, and built-in roles cannot be mapped to access groups."
	protectedKeysMessage   = "Roles carrying authorization-management permissions cannot be mapped to access groups."
	scimManagedMessage     = "SCIM group content is managed by the SCIM protocol and cannot be changed through the Owner group API."
)

// accessGroup is AccessGroupResponse as a test reads it.
type accessGroup struct {
	ID               uuid.UUID   `json:"id"`
	DisplayName      string      `json:"displayName"`
	Source           string      `json:"source"`
	ScimConnectionID *uuid.UUID  `json:"scimConnectionId"`
	IsActive         bool        `json:"isActive"`
	CreatedAt        time.Time   `json:"createdAt"`
	UpdatedAt        time.Time   `json:"updatedAt"`
	ConcurrencyStamp string      `json:"concurrencyStamp"`
	MemberUserIDs    []uuid.UUID `json:"memberUserIds"`
	RoleIDs          []uuid.UUID `json:"roleIds"`
}

// groupFrom decodes r, which must have status, as an access group.
func groupFrom(t testing.TB, what string, r *resp, status int) accessGroup {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: status %d body %s, want %d", what, r.status, r.body, status)
	}
	var g accessGroup
	r.json(&g)
	return g
}

// createGroup creates the active local group name as c, which must
// succeed.
func createGroup(t testing.TB, c *client, name string) accessGroup {
	t.Helper()
	return groupFrom(t, "create group "+name, c.do(http.MethodPost, groupsPath, map[string]any{"displayName": name, "isActive": true}), http.StatusCreated)
}

func groupPath(id uuid.UUID) string { return groupsPath + "/" + id.String() }

func memberPath(groupID, userID uuid.UUID) string {
	return groupPath(groupID) + "/members/" + userID.String()
}

func mappingPath(groupID, roleID uuid.UUID) string {
	return groupPath(groupID) + "/role-mappings/" + roleID.String()
}

// stamped is a MutationRequest carrying stamp.
func stamped(stamp string) map[string]any { return map[string]any{"concurrencyStamp": stamp} }

// seedGroup inserts an active group from source directly in the database,
// as SCIM creates its groups, and returns its id.
func (h *harness) seedGroup(t testing.TB, name, source string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.access_groups (id, display_name, source, version, created_at, updated_at)
	        VALUES ($1, $2, $3, $4, $5, $5)`, id, name, source, uuid.New(), h.now())
	return id
}

// seedMembership writes a membership row as SCIM, or an override, left it.
func (h *harness) seedMembership(t testing.TB, groupID, userID uuid.UUID, source string, upstream bool, override *string) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
	        VALUES ($1, $2, $3, $4, $5)`, groupID, userID, source, upstream, override)
}

// membershipRow is userID's row in groupID as "source/upstream/override",
// or "" when there is none.
func (h *harness) membershipRow(t testing.TB, groupID, userID uuid.UUID) string {
	t.Helper()
	var source string
	var upstream bool
	var override *string
	err := h.pool.QueryRow(context.Background(), `
		SELECT source, is_upstream_present, membership_override FROM identity.access_group_memberships
		WHERE group_id = $1 AND user_id = $2`, groupID, userID).Scan(&source, &upstream, &override)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("harness: membership: %v", err)
	}
	o := "none"
	if override != nil {
		o = *override
	}
	return fmt.Sprintf("%s/%t/%s", source, upstream, o)
}

// byUUID is ids in the order PostgreSQL sorts uuids, byte by byte.
func byUUID(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// snapshotJSON is .NET's Snapshot of g (SV/AccessGroupManagementService.cs:352-362)
// as the audit trail records it.
func snapshotJSON(g accessGroup) string {
	return `{"Id":"` + g.ID.String() + `","DisplayName":` + jsonString(g.DisplayName) + `,"Source":"` + g.Source +
		`","IsActive":` + strconv.FormatBool(g.IsActive) + `,"ScimConnectionId":null,"CreatedAt":"` + g.CreatedAt.Format(time.RFC3339Nano) +
		`","UpdatedAt":"` + g.UpdatedAt.Format(time.RFC3339Nano) + `","ConcurrencyStamp":"` + g.ConcurrencyStamp + `"}`
}

// entityJSON is the local AccessGroup entity g as .NET's serializer wrote
// it: declaration order, and Source as the enum's number, 0 for Local.
func entityJSON(g accessGroup) string {
	return `{"Id":"` + g.ID.String() + `","ScimConnectionId":null,"DisplayName":` + jsonString(g.DisplayName) +
		`,"Source":0,"ExternalId":null,"IsActive":` + strconv.FormatBool(g.IsActive) + `,"CreatedAt":"` + g.CreatedAt.Format(time.RFC3339Nano) +
		`","UpdatedAt":"` + g.UpdatedAt.Format(time.RFC3339Nano) + `","ConcurrencyStamp":"` + g.ConcurrencyStamp + `"}`
}

// wantGroupAudit fails t unless action has n rows and the last is by actor
// at the harness clock, with no target, .NET's details, and exactly before
// and after: .NET's group events named no target user or role
// (SV/AccessGroupManagementService.cs:342-350).
func wantGroupAudit(t testing.TB, h *harness, action string, n int, actor uuid.UUID, before, after string) {
	t.Helper()
	events := h.auditEvents(t, action)
	if len(events) != n {
		t.Fatalf("%d %s rows, want %d", len(events), action, n)
	}
	e := events[n-1]
	if e.Actor == nil || *e.Actor != actor || e.TargetUser != nil || e.TargetRole != nil || e.Details != `{"action":"`+action+`"}` ||
		e.Before != before || e.After != after || !e.At.Equal(h.now()) {
		t.Errorf("%s: actor %v target %v/%v details %s at %s\nbefore %s\nafter  %s\nwant before %s\nwant after  %s",
			action, e.Actor, e.TargetUser, e.TargetRole, e.Details, e.At, e.Before, e.After, before, after)
	}
}

// Ported from IdentityControlPlaneIntegrationTests.LocalAccessGroupAdministrationRemainsOwnerOnly.
// Extended: the list shows the new group exactly as the 201 did. Users and
// delegates are refused every group operation in
// TestRbac_AnonymousCallersAndNonOwnersCannotManage, with the rest of
// /access.
func TestAccessGroups_LocalAdministrationRemainsOwnerOnly(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	if r := h.client(t).do(http.MethodGet, groupsPath, nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Errorf("anonymous list: status %d body %s, want 401 unauthenticated", r.status, r.body)
	}
	created := groupFrom(t, "create", owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": "Local integration group", "isActive": true}), http.StatusCreated)
	r := owner.do(http.MethodGet, groupsPath, nil)
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte("Local integration group")) {
		t.Fatalf("list: status %d body %s, want the new group", r.status, r.body)
	}
	var listed []accessGroup
	r.json(&listed)
	if len(listed) != 1 || !jsonEqual(listed[0], created) {
		t.Errorf("list %+v, want exactly %+v", listed, created)
	}
}

// queryCounter is a pgx QueryTracer that counts the queries a pool runs.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (*queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Ported from AccessGroupQueryCountTests.Group_listing_query_count_does_not_grow_with_the_number_of_groups.
// The count is every query the installation's pool runs for one Owner
// request (withQueryTracer), the access check's session lookup included,
// which is the same for every request. .NET counted two groups and then
// ten, each with a member; here one and then twenty, each with a member and
// a role mapping. Extended: one group's details cost the same with one
// member and role as with twenty of each.
func TestAccessGroups_ListingAndDetailsRunAConstantNumberOfQueries(t *testing.T) {
	t.Parallel()
	counter := &queryCounter{}
	h := newHarness(t, withQueryTracer(counter))
	owner, ownerID := h.bootstrapOwner(t)
	role := h.insertRole(t, "Counted")
	seed := func(n int) uuid.UUID {
		var id uuid.UUID
		for range n {
			id = h.seedGroup(t, "Query count group "+uuid.NewString(), "local")
			h.seedMembership(t, id, ownerID, "local", false, new("force_member"))
			h.exec(t, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, 'local')`, id, role)
		}
		return id
	}
	queries := func(path string, want func(*resp) bool) int64 {
		t.Helper()
		counter.n.Store(0)
		r := owner.do(http.MethodGet, path, nil)
		n := counter.n.Load()
		if r.status != http.StatusOK || !want(r) {
			t.Fatalf("GET %s: status %d body %s", path, r.status, r.body)
		}
		return n
	}
	groups := func(n int) func(*resp) bool {
		return func(r *resp) bool {
			var listed []accessGroup
			r.json(&listed)
			return len(listed) == n && len(listed[0].MemberUserIDs) == 1 && len(listed[0].RoleIDs) == 1
		}
	}

	small := seed(1)
	withFew := queries(groupsPath, groups(1))
	seed(19)
	withMany := queries(groupsPath, groups(20))
	if withFew == 0 || withFew != withMany {
		t.Errorf("listing 1 group ran %d queries and 20 groups %d, want the same, and more than none", withFew, withMany)
	}

	large := seed(1)
	for i := range 19 {
		user := h.insertUser(t, fmt.Sprintf("counted-%d@example.test", i), identity.RoleUserID)
		h.seedMembership(t, large, user, "local", false, new("force_member"))
		h.exec(t, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, 'local')`, large, h.insertRole(t, fmt.Sprintf("Counted %d", i)))
	}
	details := func(n int) func(*resp) bool {
		return func(r *resp) bool {
			var g accessGroup
			r.json(&g)
			return len(g.MemberUserIDs) == n && len(g.RoleIDs) == n
		}
	}
	one := queries(groupPath(small), details(1))
	twenty := queries(groupPath(large), details(20))
	if one == 0 || one != twenty {
		t.Errorf("the details of a group with 1 member and role ran %d queries and with 20 %d, want the same, and more than none", one, twenty)
	}
}

// TestAccessGroups_CreateValidatesTrimsAndAudits is .NET's CreateGroup
// (EA/IdentityControlPlaneEndpoints.cs:54-67, Normalize :130-135): a null
// body, or a display name that is blank, longer than 200 UTF-16 units or
// holds a control character, is 400 invalid_group, and one another group
// has exactly is 409 group_exists. The 201 has the trimmed name, source
// Local, no SCIM connection, the stored version and the Location; isActive
// left out is false, as .NET's AccessGroupRequest bound it; and
// access_group.created records {} before and the entity after.
func TestAccessGroups_CreateValidatesTrimsAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	for what, name := range map[string]any{
		"a null name":                     nil,
		"an empty name":                   "",
		"a blank name":                    " \t ",
		"201 characters":                  strings.Repeat("a", 201),
		"101 characters outside the BMP":  strings.Repeat("😀", 101),
		"a control character":             "Ops\u0007",
		"a line break inside":             "Ops\nTeam",
		"a C1 control character, trimmed": " Ops\u0085Team ",
	} {
		wantFlat(t, what, owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": name, "isActive": true}),
			http.StatusBadRequest, "invalid_group", invalidGroupMessage)
	}
	wantFlat(t, "a null body", owner.do(http.MethodPost, groupsPath, nil, rawBody("application/json", []byte("null"))),
		http.StatusBadRequest, "invalid_group", invalidGroupMessage)
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups`); n != 0 {
		t.Fatalf("%d groups were created", n)
	}

	r := owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": "  Operations  ", "isActive": true})
	g := groupFrom(t, "create", r, http.StatusCreated)
	if got, want := r.header("Location"), groupPath(g.ID); got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	want := accessGroup{
		ID: g.ID, DisplayName: "Operations", Source: "Local", IsActive: true, CreatedAt: h.now(), UpdatedAt: h.now(),
		ConcurrencyStamp: h.version(t, "access_groups", g.ID), MemberUserIDs: []uuid.UUID{}, RoleIDs: []uuid.UUID{},
	}
	if !jsonEqual(g, want) || !bytes.Contains(r.body, []byte(`"scimConnectionId":null`)) {
		t.Errorf("created %s, want %+v", r.body, want)
	}
	wantGroupAudit(t, h, "access_group.created", 1, ownerID, `{}`, entityJSON(g))

	if d := groupFrom(t, "create without isActive", owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": "Dormant"}), http.StatusCreated); d.IsActive {
		t.Error("a group created without isActive is active")
	}
	for _, name := range []string{strings.Repeat("a", 200), strings.Repeat("😀", 100), "operations"} {
		createGroup(t, owner, name)
	}
	for what, name := range map[string]string{"the same name": "Operations", "the same name untrimmed": " Operations "} {
		wantFlat(t, what, owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": name, "isActive": true}),
			http.StatusConflict, "group_exists", groupExistsMessage)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups`); n != 5 {
		t.Errorf("%d groups, want 5", n)
	}
}

// TestAccessGroups_UpdateInDotNetsOrderAndAudits is .NET's UpdateGroup
// (EA/IdentityControlPlaneEndpoints.cs:69-85): 400 invalid_group before the
// lookup, the bare 404, 400 concurrency_required, 409 group_conflict for a
// stale stamp (before the name), and 409 group_exists; none of them
// changes the group. The 200 has the trimmed name, the new state, a new
// version and updatedAt; a missing isActive deactivates, as .NET bound it;
// and access_group.updated records the snapshot before and the entity
// after.
func TestAccessGroups_UpdateInDotNetsOrderAndAudits(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	g := createGroup(t, owner, "Alpha")
	createGroup(t, owner, "Beta")
	put := func(id uuid.UUID, name string, stamp any) *resp {
		body := map[string]any{"displayName": name, "isActive": true}
		if stamp != nil {
			body["concurrencyStamp"] = stamp
		}
		return owner.do(http.MethodPut, groupPath(id), body)
	}

	wantFlat(t, "a blank name for an unknown group", put(uuid.New(), " ", g.ConcurrencyStamp), http.StatusBadRequest, "invalid_group", invalidGroupMessage)
	if r := put(uuid.New(), "Gamma", g.ConcurrencyStamp); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown group: status %d body %s, want the bare 404", r.status, r.body)
	}
	wantFlat(t, "no stamp", put(g.ID, "Gamma", nil), http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	wantFlat(t, "a blank stamp", put(g.ID, "Gamma", "  "), http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	wantFlat(t, "a stale stamp", put(g.ID, "Gamma", uuid.NewString()), http.StatusConflict, "group_conflict", groupChangedMessage)
	wantFlat(t, "a stale stamp and a taken name", put(g.ID, "Beta", uuid.NewString()), http.StatusConflict, "group_conflict", groupChangedMessage)
	wantFlat(t, "a taken name", put(g.ID, "Beta", g.ConcurrencyStamp), http.StatusConflict, "group_exists", groupExistsMessage)
	if v := h.version(t, "access_groups", g.ID); v != g.ConcurrencyStamp || len(h.auditEvents(t, "access_group.updated")) != 0 {
		t.Fatalf("the refusals changed the group: version %s", v)
	}

	h.advance(time.Minute)
	updated := groupFrom(t, "update", owner.do(http.MethodPut, groupPath(g.ID), map[string]any{"displayName": "  Alpha Prime  ", "concurrencyStamp": g.ConcurrencyStamp}), http.StatusOK)
	want := g
	want.DisplayName, want.IsActive, want.UpdatedAt, want.ConcurrencyStamp = "Alpha Prime", false, h.now(), h.version(t, "access_groups", g.ID)
	if !jsonEqual(updated, want) || updated.ConcurrencyStamp == g.ConcurrencyStamp {
		t.Errorf("updated %+v, want %+v with a new stamp", updated, want)
	}
	wantGroupAudit(t, h, "access_group.updated", 1, ownerID, snapshotJSON(g), entityJSON(updated))
	wantFlat(t, "the replaced stamp", put(g.ID, "Alpha", g.ConcurrencyStamp), http.StatusConflict, "group_conflict", groupChangedMessage)
	if again := groupFrom(t, "keep the name", put(g.ID, "Alpha Prime", updated.ConcurrencyStamp), http.StatusOK); !again.IsActive || again.DisplayName != "Alpha Prime" {
		t.Errorf("keeping the name and reactivating: %+v", again)
	}
}

// TestAccessGroups_DeleteTakesTheCurrentStampAndCascades is .NET's
// DeleteGroup (EA/IdentityControlPlaneEndpoints.cs:87-94,
// SV/AccessGroupManagementService.cs:78-94): the bare 404, 400
// concurrency_required, and a stale stamp, which .NET's service threw as a
// concurrency exception and the route group's filter answered 409
// concurrency_conflict (EA/IdentityControlPlaneEndpoints.cs:17-22). With
// the current stamp the group goes with its memberships and mappings, and
// access_group.deleted records its snapshot, with {} after.
func TestAccessGroups_DeleteTakesTheCurrentStampAndCascades(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	g := createGroup(t, owner, "Doomed")
	role := createRole(t, owner, "doomed-viewer", "customers:view")
	memberID := h.insertUser(t, "doomed@example.test", identity.RoleUserID)
	g = groupFrom(t, "add a member", owner.do(http.MethodPost, memberPath(g.ID, memberID), stamped(g.ConcurrencyStamp)), http.StatusOK)
	g = groupFrom(t, "map a role", owner.do(http.MethodPost, mappingPath(g.ID, role.ID), stamped(g.ConcurrencyStamp)), http.StatusOK)

	if r := owner.do(http.MethodDelete, groupPath(uuid.New()), stamped(g.ConcurrencyStamp)); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown group: status %d body %s, want the bare 404", r.status, r.body)
	}
	wantFlat(t, "a null stamp", owner.do(http.MethodDelete, groupPath(g.ID), map[string]any{"concurrencyStamp": nil}), http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	wantFlat(t, "a null body", owner.do(http.MethodDelete, groupPath(g.ID), nil, rawBody("application/json", []byte("null"))), http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	wantFlat(t, "a stale stamp", owner.do(http.MethodDelete, groupPath(g.ID), stamped(uuid.NewString())), http.StatusConflict, "concurrency_conflict", resourceChangedMessage)
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups WHERE id = $1`, g.ID); n != 1 {
		t.Fatal("a refused delete removed the group")
	}

	if r := owner.do(http.MethodDelete, groupPath(g.ID), stamped(g.ConcurrencyStamp)); r.status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.status, r.body)
	}
	for _, table := range []string{"access_groups WHERE id", "access_group_memberships WHERE group_id", "access_group_role_mappings WHERE group_id"} {
		if n := h.count(t, `SELECT count(*) FROM identity.`+table+` = $1`, g.ID); n != 0 {
			t.Errorf("identity.%s: %d rows remain", table, n)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.roles WHERE id = $1`, role.ID); n != 1 {
		t.Error("deleting the group deleted its role")
	}
	wantGroupAudit(t, h, "access_group.deleted", 1, ownerID, snapshotJSON(g), `{}`)
}

// TestAccessGroups_MembersAreLocalForcedMembers is .NET's MemberMutation
// (EA/IdentityControlPlaneEndpoints.cs:99-108) with AddMemberAsync and
// RemoveMemberAsync (SV/AccessGroupManagementService.cs:96-156), in their
// order: 400 concurrency_required before the lookup, the bare 404, a stale
// stamp (409 concurrency_conflict, the filter), and 400 user_not_found,
// none of which changes the group. POST and PUT each make the user a local
// forced member, overwriting a row that said otherwise; DELETE removes the
// row; each gives the group a new version and is audited. A
// force_non_member row is no member. The member's session survives: .NET
// rotated no security stamp for a membership.
func TestAccessGroups_MembersAreLocalForcedMembers(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	g := createGroup(t, owner, "Members")
	member, memberID := userClient(t, h, owner, "member@example.test")
	overridden := h.insertUser(t, "overridden@example.test", identity.RoleUserID)

	wantFlat(t, "no stamp for an unknown group", owner.do(http.MethodPost, memberPath(uuid.New(), memberID), map[string]any{"concurrencyStamp": nil}),
		http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	if r := owner.do(http.MethodPut, memberPath(uuid.New(), memberID), stamped(g.ConcurrencyStamp)); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown group: status %d body %s, want the bare 404", r.status, r.body)
	}
	wantFlat(t, "a stale stamp", owner.do(http.MethodPost, memberPath(g.ID, memberID), stamped(uuid.NewString())), http.StatusConflict, "concurrency_conflict", resourceChangedMessage)
	wantFlat(t, "an unknown user", owner.do(http.MethodPost, memberPath(g.ID, uuid.New()), stamped(g.ConcurrencyStamp)), http.StatusBadRequest, "user_not_found", "The selected user does not exist.")
	if v := h.version(t, "access_groups", g.ID); v != g.ConcurrencyStamp || len(h.auditEvents(t, "access_group.member_added")) != 0 {
		t.Fatalf("the refusals changed the group: version %s", v)
	}

	h.advance(time.Minute)
	added := groupFrom(t, "POST a member", owner.do(http.MethodPost, memberPath(g.ID, memberID), stamped(g.ConcurrencyStamp)), http.StatusOK)
	if !slices.Equal(added.MemberUserIDs, []uuid.UUID{memberID}) || added.ConcurrencyStamp == g.ConcurrencyStamp ||
		added.ConcurrencyStamp != h.version(t, "access_groups", g.ID) || !added.UpdatedAt.Equal(h.now()) {
		t.Errorf("after POST: %+v", added)
	}
	if got := h.membershipRow(t, g.ID, memberID); got != "local/false/force_member" {
		t.Errorf("the POSTed row is %s, want local/false/force_member", got)
	}
	wantGroupAudit(t, h, "access_group.member_added", 1, ownerID, snapshotJSON(g),
		`{"Group":`+snapshotJSON(added)+`,"UserId":"`+memberID.String()+`"}`)

	h.seedMembership(t, g.ID, overridden, "scim", true, new("force_non_member"))
	if listed := groupFrom(t, "details", owner.do(http.MethodGet, groupPath(g.ID), nil), http.StatusOK); !slices.Equal(listed.MemberUserIDs, []uuid.UUID{memberID}) {
		t.Errorf("members %v: a force_non_member row counts", listed.MemberUserIDs)
	}
	put := groupFrom(t, "PUT over an overriding row", owner.do(http.MethodPut, memberPath(g.ID, overridden), stamped(added.ConcurrencyStamp)), http.StatusOK)
	if got := h.membershipRow(t, g.ID, overridden); got != "local/false/force_member" || !slices.Equal(put.MemberUserIDs, byUUID(memberID, overridden)) {
		t.Errorf("after PUT: row %s, members %v", got, put.MemberUserIDs)
	}
	again := groupFrom(t, "PUT an existing member", owner.do(http.MethodPut, memberPath(g.ID, memberID), stamped(put.ConcurrencyStamp)), http.StatusOK)
	if again.ConcurrencyStamp == put.ConcurrencyStamp || h.membershipRow(t, g.ID, memberID) != "local/false/force_member" {
		t.Errorf("a repeated addition: %+v", again)
	}
	if n := len(h.auditEvents(t, "access_group.member_added")); n != 3 {
		t.Errorf("%d member_added rows, want 3", n)
	}

	removed := groupFrom(t, "DELETE a member", owner.do(http.MethodDelete, memberPath(g.ID, overridden), stamped(again.ConcurrencyStamp)), http.StatusOK)
	if got := h.membershipRow(t, g.ID, overridden); got != "" || !slices.Equal(removed.MemberUserIDs, []uuid.UUID{memberID}) {
		t.Errorf("after DELETE: row %q, members %v", got, removed.MemberUserIDs)
	}
	wantGroupAudit(t, h, "access_group.member_removed", 1, ownerID, snapshotJSON(again),
		`{"Group":`+snapshotJSON(removed)+`,"UserId":"`+overridden.String()+`"}`)
	if gone := groupFrom(t, "DELETE a non-member", owner.do(http.MethodDelete, memberPath(g.ID, overridden), stamped(removed.ConcurrencyStamp)), http.StatusOK); gone.ConcurrencyStamp == removed.ConcurrencyStamp {
		t.Error("removing a non-member left the version")
	}
	admitted(t, member)
}

// TestAccessGroups_RoleMappingsRefuseProtectedRoles is .NET's
// RoleMappingMutation (EA/IdentityControlPlaneEndpoints.cs:113-122) with
// AddRoleMappingAsync and RemoveRoleMappingAsync
// (SV/AccessGroupManagementService.cs:158-247), in their order: 400
// concurrency_required, the bare 404, 400 scim_scope_conflict for a local
// group named with a SCIM connection, a stale stamp (409
// concurrency_conflict, the filter), 400 role_not_found, and 400
// protected_role, .NET's validation status (:370-373), for each built-in
// role and for a role holding identity:manage. None maps anything or
// changes the group. A custom role maps with the group's source; POST and
// PUT both map, DELETE unmaps, each with a new version, and each audited.
func TestAccessGroups_RoleMappingsRefuseProtectedRoles(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	g := createGroup(t, owner, "Mapped")
	viewer := createRole(t, owner, "viewer", "customers:view")
	manager := createRole(t, owner, "manager", "customers:view", "identity:manage")
	post := func(roleID uuid.UUID, body map[string]any) *resp {
		return owner.do(http.MethodPost, mappingPath(g.ID, roleID), body)
	}

	wantFlat(t, "no stamp", post(viewer.ID, map[string]any{"concurrencyStamp": nil}), http.StatusBadRequest, "concurrency_required", stampRequiredMessage)
	if r := owner.do(http.MethodPut, mappingPath(uuid.New(), viewer.ID), stamped(g.ConcurrencyStamp)); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("an unknown group: status %d body %s, want the bare 404", r.status, r.body)
	}
	wantFlat(t, "a local group named with a SCIM connection", post(viewer.ID, map[string]any{"concurrencyStamp": g.ConcurrencyStamp, "scimConnectionId": uuid.New()}),
		http.StatusBadRequest, "scim_scope_conflict", "A local group cannot be scoped to a SCIM connection.")
	wantFlat(t, "a stale stamp", post(viewer.ID, stamped(uuid.NewString())), http.StatusConflict, "concurrency_conflict", resourceChangedMessage)
	wantFlat(t, "an unknown role", post(uuid.New(), stamped(g.ConcurrencyStamp)), http.StatusBadRequest, "role_not_found", "The selected role does not exist.")
	for name, id := range map[string]uuid.UUID{"SystemAdmin": identity.RoleSystemAdminID, identity.RoleOwner: identity.RoleOwnerID, identity.RoleUser: identity.RoleUserID} {
		wantFlat(t, name, post(id, stamped(g.ConcurrencyStamp)), http.StatusBadRequest, "protected_role", protectedRoleMessage)
	}
	wantFlat(t, "a role holding identity:manage", post(manager.ID, stamped(g.ConcurrencyStamp)), http.StatusBadRequest, "protected_role", protectedKeysMessage)
	if v := h.version(t, "access_groups", g.ID); v != g.ConcurrencyStamp || h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings`) != 0 {
		t.Fatalf("the refusals changed the group or mapped a role: version %s", v)
	}

	h.advance(time.Minute)
	mapped := groupFrom(t, "POST a mapping", post(viewer.ID, stamped(g.ConcurrencyStamp)), http.StatusOK)
	if !slices.Equal(mapped.RoleIDs, []uuid.UUID{viewer.ID}) || mapped.ConcurrencyStamp == g.ConcurrencyStamp || !mapped.UpdatedAt.Equal(h.now()) {
		t.Errorf("after POST: %+v", mapped)
	}
	source := func() string {
		var s string
		if err := h.pool.QueryRow(context.Background(), `SELECT string_agg(source, ',') FROM identity.access_group_role_mappings WHERE group_id = $1`, g.ID).Scan(&s); err != nil {
			t.Fatalf("mapping source: %v", err)
		}
		return s
	}
	if got := source(); got != "local" {
		t.Errorf("the mapping's source is %q, want local", got)
	}
	wantGroupAudit(t, h, "access_group.role_mapped", 1, ownerID, snapshotJSON(g),
		`{"Group":`+snapshotJSON(mapped)+`,"RoleId":"`+viewer.ID.String()+`"}`)
	again := groupFrom(t, "PUT the same mapping", owner.do(http.MethodPut, mappingPath(g.ID, viewer.ID), stamped(mapped.ConcurrencyStamp)), http.StatusOK)
	if again.ConcurrencyStamp == mapped.ConcurrencyStamp || source() != "local" {
		t.Errorf("a repeated mapping: %+v, mappings %q", again, source())
	}

	unmapped := groupFrom(t, "DELETE the mapping", owner.do(http.MethodDelete, mappingPath(g.ID, viewer.ID), stamped(again.ConcurrencyStamp)), http.StatusOK)
	if len(unmapped.RoleIDs) != 0 || h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings`) != 0 {
		t.Errorf("after DELETE: %+v", unmapped)
	}
	wantGroupAudit(t, h, "access_group.role_unmapped", 1, ownerID, snapshotJSON(again),
		`{"Group":`+snapshotJSON(unmapped)+`,"RoleId":"`+viewer.ID.String()+`"}`)
	if gone := groupFrom(t, "DELETE a missing mapping", owner.do(http.MethodDelete, mappingPath(g.ID, viewer.ID), stamped(unmapped.ConcurrencyStamp)), http.StatusOK); gone.ConcurrencyStamp == unmapped.ConcurrencyStamp {
		t.Error("removing a missing mapping left the version")
	}
}

// TestAccessGroups_SCIMGroupContentIsProtocolOwned is .NET's protection of
// a SCIM group (SV/AccessGroupManagementService.cs:320-340,
// EA/IdentityControlPlaneEndpoints.cs:41-51): GET of it is the bare 404
// while the list shows it; an update, a delete and each member change is
// refused 409 scim_group_managed, and the access_group.*_rejected row is
// committed first, in its own transaction, so it stays although nothing
// else changes. The rejection comes where .NET checked it: after an
// update's stamp and name checks, and before a delete's or a member
// change's stamp check. Role mappings stay the Owner's to change, with the
// group's source; a SCIM connection id is refused, since the one static
// connection has none.
func TestAccessGroups_SCIMGroupContentIsProtocolOwned(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	id := h.seedGroup(t, "Directory group", "scim")
	upstream := h.insertUser(t, "upstream@example.test", identity.RoleUserID)
	other := h.insertUser(t, "other@example.test", identity.RoleUserID)
	h.seedMembership(t, id, upstream, "scim", true, nil)
	stamp := h.version(t, "access_groups", id)

	if r := owner.do(http.MethodGet, groupPath(id), nil); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("GET a SCIM group: status %d body %s, want the bare 404", r.status, r.body)
	}
	r := owner.do(http.MethodGet, groupsPath, nil)
	var listed []accessGroup
	r.json(&listed)
	want := accessGroup{ID: id, DisplayName: "Directory group", Source: "Scim", IsActive: true, CreatedAt: h.now(), UpdatedAt: h.now(),
		ConcurrencyStamp: stamp, MemberUserIDs: []uuid.UUID{upstream}, RoleIDs: []uuid.UUID{}}
	if r.status != http.StatusOK || len(listed) != 1 || !jsonEqual(listed[0], want) {
		t.Fatalf("list: status %d body %s, want the SCIM group %+v", r.status, r.body, want)
	}
	scim := listed[0]

	wantFlat(t, "an update with a stale stamp", owner.do(http.MethodPut, groupPath(id), map[string]any{"displayName": "Renamed", "concurrencyStamp": uuid.NewString()}),
		http.StatusConflict, "group_conflict", groupChangedMessage)
	if n := len(h.auditEvents(t, "access_group.update_rejected")); n != 0 {
		t.Fatalf("a stale update wrote %d rejected rows; the stamp is checked first", n)
	}
	rejected := func(userID *uuid.UUID) string {
		user := "null"
		if userID != nil {
			user = `"` + userID.String() + `"`
		}
		return `{"GroupId":"` + id.String() + `","ScimConnectionId":null,"UserId":` + user + `,"Rejected":true,"Reason":"scim_group_content_is_protocol_owned"}`
	}
	for _, c := range []struct {
		action string
		n      int
		user   *uuid.UUID
		r      func() *resp
	}{
		{"access_group.update_rejected", 1, nil, func() *resp {
			return owner.do(http.MethodPut, groupPath(id), map[string]any{"displayName": "Renamed", "concurrencyStamp": stamp})
		}},
		{"access_group.delete_rejected", 1, nil, func() *resp { return owner.do(http.MethodDelete, groupPath(id), stamped(uuid.NewString())) }},
		{"access_group.member_add_rejected", 1, &other, func() *resp { return owner.do(http.MethodPost, memberPath(id, other), stamped(uuid.NewString())) }},
		{"access_group.member_add_rejected", 2, &other, func() *resp { return owner.do(http.MethodPut, memberPath(id, other), stamped(stamp)) }},
		{"access_group.member_remove_rejected", 1, &upstream, func() *resp { return owner.do(http.MethodDelete, memberPath(id, upstream), stamped(stamp)) }},
	} {
		wantFlat(t, c.action, c.r(), http.StatusConflict, "scim_group_managed", scimManagedMessage)
		wantGroupAudit(t, h, c.action, c.n, ownerID, snapshotJSON(scim), rejected(c.user))
	}
	if v := h.version(t, "access_groups", id); v != stamp || h.membershipRow(t, id, upstream) != "scim/true/none" || h.membershipRow(t, id, other) != "" ||
		h.count(t, `SELECT count(*) FROM identity.access_groups WHERE id = $1 AND display_name = 'Directory group' AND is_active`, id) != 1 {
		t.Error("a refused change to the SCIM group changed it")
	}

	role := createRole(t, owner, "directory-viewer", "customers:view")
	wantFlat(t, "a SCIM connection id", owner.do(http.MethodPost, mappingPath(id, role.ID), map[string]any{"concurrencyStamp": stamp, "scimConnectionId": uuid.New()}),
		http.StatusBadRequest, "scim_scope_conflict", "The SCIM group belongs to a different SCIM connection.")
	mapped := groupFrom(t, "map a role to the SCIM group", owner.do(http.MethodPost, mappingPath(id, role.ID), stamped(stamp)), http.StatusOK)
	if mapped.Source != "Scim" || !slices.Equal(mapped.RoleIDs, []uuid.UUID{role.ID}) ||
		h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings WHERE group_id = $1 AND source = 'scim'`, id) != 1 {
		t.Errorf("the SCIM group's mapping: %+v", mapped)
	}
}

// TestAccessGroups_AGroupMappedRoleGrantsItsPermissionWhileActive shows
// the live permission check counting group roles
// (AZ/PermissionAuthorization.cs:80-99), with every change made through the
// group endpoints and each applying to the member's very next check on the
// same session: a membership alone grants nothing, the mapping grants the
// role's key, deactivating the group withdraws it and reactivating restores
// it, and removing the member or the mapping withdraws it. The session
// survives it all, as .NET rotated no security stamp for any of it.
func TestAccessGroups_AGroupMappedRoleGrantsItsPermissionWhileActive(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	member, memberID := userClient(t, h, owner, "grouped@example.test")
	viewer := createRole(t, owner, "group-viewer", "customers:view")
	g := createGroup(t, owner, "Readers")
	step := func(what string, r *resp, want bool) {
		t.Helper()
		g = groupFrom(t, what, r, http.StatusOK)
		if got := h.permitted(t, member, "customers:view"); got != want {
			t.Errorf("after %s the member is granted customers:view: %t, want %t", what, got, want)
		}
	}
	if h.permitted(t, member, "customers:view") {
		t.Fatal("the member is granted customers:view before any group change")
	}
	step("the member is added", owner.do(http.MethodPost, memberPath(g.ID, memberID), stamped(g.ConcurrencyStamp)), false)
	step("the role is mapped", owner.do(http.MethodPut, mappingPath(g.ID, viewer.ID), stamped(g.ConcurrencyStamp)), true)
	step("the group is deactivated", owner.do(http.MethodPut, groupPath(g.ID), map[string]any{"displayName": "Readers", "isActive": false, "concurrencyStamp": g.ConcurrencyStamp}), false)
	step("the group is reactivated", owner.do(http.MethodPut, groupPath(g.ID), map[string]any{"displayName": "Readers", "isActive": true, "concurrencyStamp": g.ConcurrencyStamp}), true)
	step("the member is removed", owner.do(http.MethodDelete, memberPath(g.ID, memberID), stamped(g.ConcurrencyStamp)), false)
	step("the member is added again", owner.do(http.MethodPut, memberPath(g.ID, memberID), stamped(g.ConcurrencyStamp)), true)
	step("the role is unmapped", owner.do(http.MethodDelete, mappingPath(g.ID, viewer.ID), stamped(g.ConcurrencyStamp)), false)
	admitted(t, member)
}

// TestAccessGroups_ForceNonMemberExcludesAnUpstreamPresentMember is the
// override rule (AZ/PermissionAuthorization.cs:80-93): in a SCIM group
// whose role the Owner mapped, a user upstream says is present is granted
// its key while there is no override, and not once a force_non_member
// override excludes them, nor once upstream says they are absent; the
// group's members list them accordingly.
func TestAccessGroups_ForceNonMemberExcludesAnUpstreamPresentMember(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	upstream, upstreamID := userClient(t, h, owner, "upstream@example.test")
	viewer := createRole(t, owner, "directory-viewer", "customers:view")
	id := h.seedGroup(t, "Directory readers", "scim")
	h.seedMembership(t, id, upstreamID, "scim", true, nil)
	groupFrom(t, "map the role", owner.do(http.MethodPost, mappingPath(id, viewer.ID), stamped(h.version(t, "access_groups", id))), http.StatusOK)
	members := func() []uuid.UUID {
		var listed []accessGroup
		owner.do(http.MethodGet, groupsPath, nil).json(&listed)
		return listed[0].MemberUserIDs
	}

	if !h.permitted(t, upstream, "customers:view") || !slices.Equal(members(), []uuid.UUID{upstreamID}) {
		t.Fatal("an upstream-present member without an override is not granted the group's role")
	}
	h.exec(t, `UPDATE identity.access_group_memberships SET membership_override = 'force_non_member' WHERE group_id = $1`, id)
	if h.permitted(t, upstream, "customers:view") || len(members()) != 0 {
		t.Error("force_non_member does not exclude an upstream-present member")
	}
	h.exec(t, `UPDATE identity.access_group_memberships SET membership_override = NULL, is_upstream_present = false WHERE group_id = $1`, id)
	if h.permitted(t, upstream, "customers:view") || len(members()) != 0 {
		t.Error("a member upstream says is absent is granted the group's role")
	}
}

// TestAccessGroups_AMappingQueuedBehindARoleEditSeesTheEditsKeys is the
// per-role lock from the mapping's side (AZ/AuthorizationMutationService.cs:20-31,
// SV/AccessGroupManagementService.cs:172-197): a gate holds the role's lock,
// as an edit does, and gives the role identity:manage, uncommitted; a
// mapping of the role waits on the lock. Once the gate commits, the mapping
// reads the role's keys after the lock and is refused 400 protected_role,
// and no group maps the role.
func TestAccessGroups_AMappingQueuedBehindARoleEditSeesTheEditsKeys(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "rising", "customers:view")
	g := createGroup(t, owner, "Queued")
	gate := holdRoleLock(t, h, role.ID)
	if _, err := gate.Exec(context.Background(), `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, 'identity:manage')`, role.ID); err != nil {
		t.Fatalf("gate: %v", err)
	}
	out := raceBehind(t, h, gate, func() *resp {
		return owner.do(http.MethodPost, mappingPath(g.ID, role.ID), stamped(g.ConcurrencyStamp))
	})
	wantFlat(t, "the queued mapping", out[0], http.StatusBadRequest, "protected_role", protectedKeysMessage)
	if n := h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings`); n != 0 || h.version(t, "access_groups", g.ID) != g.ConcurrencyStamp {
		t.Errorf("the refused mapping changed the group: %d mappings", n)
	}
}

// TestAccessGroups_AMappingAndARoleEditSerialise runs a real mapping of a
// role and a real edit giving it identity:manage together, both queued
// behind a gate holding the role's lock and released at once. Whichever
// takes the lock first wins, and the other reads what it left: the mapping
// (200) and the edit refused 403 role_group_protected_permission, or the
// edit (200) and the mapping refused 400 protected_role. Either way no
// group maps a role holding identity:manage.
func TestAccessGroups_AMappingAndARoleEditSerialise(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := createRole(t, owner, "contested", "customers:view")
	g := createGroup(t, owner, "Contested")
	out := raceBehind(t, h, holdRoleLock(t, h, role.ID),
		func() *resp {
			return owner.do(http.MethodPost, mappingPath(g.ID, role.ID), stamped(g.ConcurrencyStamp))
		},
		func() *resp {
			return updateRole(owner, role.ID, "contested", role.Version, "customers:view", "identity:manage")
		},
	)
	mapped := h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings WHERE role_id = $1`, role.ID) == 1
	holds := slices.Contains(h.rolePermissions(t, role.ID), "identity:manage")
	switch {
	case out[0].status == http.StatusOK:
		wantFlat(t, "the edit behind the mapping", out[1], http.StatusForbidden, "role_group_protected_permission",
			"A role mapped to an access group cannot receive Owner or protected authorization-management permissions.")
	case out[1].status == http.StatusOK:
		wantFlat(t, "the mapping behind the edit", out[0], http.StatusBadRequest, "protected_role", protectedKeysMessage)
	default:
		t.Fatalf("neither won: mapping %d %s, edit %d %s", out[0].status, out[0].body, out[1].status, out[1].body)
	}
	if mapped && holds {
		t.Fatal("a group maps a role holding identity:manage")
	}
	if mapped == holds {
		t.Errorf("mapped %t, role holds identity:manage %t: exactly one change should have stuck", mapped, holds)
	}
}

// TestAccessGroups_ChangesWithOneStampSucceedOnce queues two member
// additions and a role mapping, all with one stamp, behind a gate holding
// the group's row. Each reads the version the one before it left: one
// answers 200 and the others the route group's 409 concurrency_conflict
// (flat), and one change is audited.
func TestAccessGroups_ChangesWithOneStampSucceedOnce(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	g := createGroup(t, owner, "Contended")
	role := createRole(t, owner, "contended-viewer", "customers:view")
	first := h.insertUser(t, "first@example.test", identity.RoleUserID)
	second := h.insertUser(t, "second@example.test", identity.RoleUserID)
	out := raceBehind(t, h, gate(t, h, stmt(`SELECT 1 FROM identity.access_groups WHERE id = $1 FOR UPDATE`, g.ID)),
		func() *resp { return owner.do(http.MethodPost, memberPath(g.ID, first), stamped(g.ConcurrencyStamp)) },
		func() *resp { return owner.do(http.MethodPut, memberPath(g.ID, second), stamped(g.ConcurrencyStamp)) },
		func() *resp {
			return owner.do(http.MethodPost, mappingPath(g.ID, role.ID), stamped(g.ConcurrencyStamp))
		},
	)
	won := 0
	for i, r := range out {
		if r.status == http.StatusOK {
			won++
			continue
		}
		wantFlat(t, fmt.Sprintf("change %d", i), r, http.StatusConflict, "concurrency_conflict", resourceChangedMessage)
	}
	audited := len(h.auditEvents(t, "access_group.member_added")) + len(h.auditEvents(t, "access_group.role_mapped"))
	changed := h.count(t, `SELECT count(*) FROM identity.access_group_memberships WHERE group_id = $1`, g.ID) +
		h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings WHERE group_id = $1`, g.ID)
	if won != 1 || audited != 1 || changed != 1 {
		t.Errorf("%d changes answered 200, %d were audited and %d stuck, want 1 each", won, audited, changed)
	}
}

// TestAccessGroups_ANameRaceIsAConflict: a gate inserts a group, and holds
// it uncommitted; a create of the same name finds no such group and then
// waits on the unique index. After the commit it is 409 group_conflict, the
// create's own catch (EA/IdentityControlPlaneEndpoints.cs:66). A rename
// waiting the same way is the route group's 409 concurrency_conflict: .NET's
// update did not catch the unique violation (:79-84), and its filter did
// (:17-22).
func TestAccessGroups_ANameRaceIsAConflict(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	insert := func(name string) func(pgx.Tx) error {
		return stmt(`INSERT INTO identity.access_groups (id, display_name, source, version, created_at, updated_at)
		        VALUES ($1, $2, 'local', $3, $4, $4)`, uuid.New(), name, uuid.New(), h.now())
	}
	out := raceBehind(t, h, gate(t, h, insert("Raced")),
		func() *resp {
			return owner.do(http.MethodPost, groupsPath, map[string]any{"displayName": "Raced", "isActive": true})
		})
	wantFlat(t, "the create", out[0], http.StatusConflict, "group_conflict", "The access group conflicts with another change.")

	g := createGroup(t, owner, "Renamed later")
	out = raceBehind(t, h, gate(t, h, insert("Raced again")),
		func() *resp {
			return owner.do(http.MethodPut, groupPath(g.ID), map[string]any{"displayName": "Raced again", "isActive": true, "concurrencyStamp": g.ConcurrencyStamp})
		})
	wantFlat(t, "the rename", out[0], http.StatusConflict, "concurrency_conflict", resourceChangedMessage)
	if n := h.count(t, `SELECT count(*) FROM identity.access_groups WHERE display_name LIKE 'Raced%'`); n != 2 {
		t.Errorf("%d groups named Raced…, want the gate's two", n)
	}
}

// raceInOrder starts fns one at a time while gate holds a lock they all
// need, starting each only once every one before it is waiting on a lock,
// then commits the gate. PostgreSQL grants a contended lock to its waiters
// in the order they queued, so fns take it in the order given.
func raceInOrder(t *testing.T, h *harness, gate interface{ Commit(context.Context) error }, fns ...func() *resp) []*resp {
	t.Helper()
	answers := make([]chan *resp, len(fns))
	for i, fn := range fns {
		answers[i] = make(chan *resp, 1)
		finished := make(chan struct{})
		go func() {
			r := fn()
			close(finished)
			answers[i] <- r
		}()
		awaitLockWaiters(t, h, i+1, finished)
	}
	if err := gate.Commit(context.Background()); err != nil {
		t.Fatalf("gate: commit: %v", err)
	}
	out := make([]*resp, len(fns))
	for i := range answers {
		out[i] = <-answers[i]
	}
	return out
}

// TestAccessGroups_AMappingAndARoleEditInEitherOrder pins both orders
// TestAccessGroups_AMappingAndARoleEditSerialise leaves to chance: a real
// mapping of a role and a real edit giving it identity:manage queue on the
// role's lock in a fixed order behind a gate. Mapping first: the mapping
// answers 200 and the edit, reading the mapping, 403
// role_group_protected_permission. Edit first: the edit answers 200 and the
// mapping, reading the edit's keys, 400 protected_role. Either way exactly
// one change sticks and no group maps a role holding identity:manage.
func TestAccessGroups_AMappingAndARoleEditInEitherOrder(t *testing.T) {
	t.Parallel()
	for _, mappingFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("mappingFirst=%t", mappingFirst), func(t *testing.T) {
			t.Parallel()
			h, owner, _ := rbacHarness(t)
			role := createRole(t, owner, "ordered", "customers:view")
			g := createGroup(t, owner, "Ordered")
			mapping := func() *resp {
				return owner.do(http.MethodPost, mappingPath(g.ID, role.ID), stamped(g.ConcurrencyStamp))
			}
			edit := func() *resp {
				return updateRole(owner, role.ID, "ordered", role.Version, "customers:view", "identity:manage")
			}

			var mapped, edited *resp
			if mappingFirst {
				out := raceInOrder(t, h, holdRoleLock(t, h, role.ID), mapping, edit)
				mapped, edited = out[0], out[1]
			} else {
				out := raceInOrder(t, h, holdRoleLock(t, h, role.ID), edit, mapping)
				edited, mapped = out[0], out[1]
			}

			isMapped := h.count(t, `SELECT count(*) FROM identity.access_group_role_mappings WHERE role_id = $1`, role.ID) == 1
			holds := slices.Contains(h.rolePermissions(t, role.ID), "identity:manage")
			if mappingFirst {
				if mapped.status != http.StatusOK {
					t.Fatalf("the first, mapping: status %d body %s, want 200", mapped.status, mapped.body)
				}
				wantFlat(t, "the edit behind the mapping", edited, http.StatusForbidden, "role_group_protected_permission",
					"A role mapped to an access group cannot receive Owner or protected authorization-management permissions.")
				if !isMapped || holds {
					t.Errorf("mapped %t, role holds identity:manage %t: want only the mapping to stick", isMapped, holds)
				}
				return
			}
			if edited.status != http.StatusOK {
				t.Fatalf("the first, edit: status %d body %s, want 200", edited.status, edited.body)
			}
			wantFlat(t, "the mapping behind the edit", mapped, http.StatusBadRequest, "protected_role", protectedKeysMessage)
			if isMapped || !holds {
				t.Errorf("mapped %t, role holds identity:manage %t: want only the edit to stick", isMapped, holds)
			}
		})
	}
}
