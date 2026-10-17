package identity_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const provisioningRolesPath = "/api/v1/identity/owner/provisioning-roles"

// staffUser is OwnerUserResponse with the fields delivery C adds, as a test
// reads it.
type staffUser struct {
	ID             uuid.UUID `json:"id"`
	Active         bool      `json:"active"`
	Disabled       bool      `json:"disabled"`
	Pending        bool      `json:"pending"`
	ManagedBy      *string   `json:"managedBy"`
	EmployeeNumber *string   `json:"employeeNumber"`
	DisabledBy     *string   `json:"disabledBy"`
	Role           string    `json:"role"`
}

// listedStaffUser is the user id as GET /owner/users lists them.
func listedStaffUser(t *testing.T, owner *client, id uuid.UUID) staffUser {
	t.Helper()
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list users: status %d body %s", r.status, r.body)
	}
	var list []staffUser
	r.json(&list)
	for _, u := range list {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("user %s is not listed", id)
	return staffUser{}
}

// manage adopts id for Staff with number as owner.
func manage(owner *client, id uuid.UUID, number string) *resp {
	return owner.do(http.MethodPost, userPath(id, "/manage"), map[string]string{"module": "staff", "employeeNumber": number})
}

// mustManage adopts id for Staff with number, which must succeed.
func mustManage(t *testing.T, owner *client, id uuid.UUID, number string) staffUser {
	t.Helper()
	r := manage(owner, id, number)
	if r.status != http.StatusOK {
		t.Fatalf("manage %s: status %d body %s", id, r.status, r.body)
	}
	var u staffUser
	r.json(&u)
	return u
}

// staffColumns is the user's Staff-managed columns as one string:
// managed_by:employee_number:pending:disabled_by, "-" for a null.
func staffColumns(t *testing.T, h *harness, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := h.pool.QueryRow(t.Context(), `
		SELECT coalesce(managed_by, '-') || ':' || coalesce(employee_number, '-') || ':'
		    || (pending_since IS NOT NULL)::text || ':' || coalesce(disabled_by, '-')
		FROM identity.users WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("staff columns of %s: %v", id, err)
	}
	return s
}

func strOr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// An Owner adopts an ordinary user for Staff and stops managing them: the
// columns, the list's fields, and one audit row each, by the Owner.
func TestStaffManaged_AdoptAndUnmanage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "kari@example.test", identity.RoleUser)

	u := mustManage(t, owner, id, " E-17 ")
	if strOr(u.ManagedBy) != "staff" || strOr(u.EmployeeNumber) != "E-17" || u.Pending {
		t.Errorf("adopted: managedBy %s employeeNumber %s pending %v, want staff E-17 false", strOr(u.ManagedBy), strOr(u.EmployeeNumber), u.Pending)
	}
	if got := staffColumns(t, h, id); got != "staff:E-17:false:-" {
		t.Errorf("columns after the adoption = %s", got)
	}
	if listed := listedStaffUser(t, owner, id); strOr(listed.ManagedBy) != "staff" || strOr(listed.EmployeeNumber) != "E-17" {
		t.Errorf("listed after the adoption: %+v", listed)
	}
	events := h.auditEvents(t, "user.staff-managed")
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id ||
		events[0].Before != `{"managedBy":null,"employeeNumber":null}` || events[0].After != `{"managedBy":"staff","employeeNumber":"E-17"}` {
		t.Errorf("user.staff-managed = %+v", events)
	}

	// A pending user unmanaged stays pending: finishing them is the Owner's.
	h.exec(t, `UPDATE identity.users SET pending_since = $2 WHERE id = $1`, id, h.now())
	r := owner.do(http.MethodPost, userPath(id, "/unmanage"), nil)
	if r.status != http.StatusOK {
		t.Fatalf("unmanage: status %d body %s", r.status, r.body)
	}
	u = staffUser{}
	r.json(&u)
	if u.ManagedBy != nil || u.EmployeeNumber != nil || !u.Pending {
		t.Errorf("unmanaged: %+v, want no managedBy, no number, still pending", u)
	}
	if got := staffColumns(t, h, id); got != "-:-:true:-" {
		t.Errorf("columns after the unmanage = %s", got)
	}
	events = h.auditEvents(t, "user.staff-unmanaged")
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id ||
		events[0].Before != `{"managedBy":"staff","employeeNumber":"E-17"}` || events[0].After != `{"managedBy":null,"employeeNumber":null}` {
		t.Errorf("user.staff-unmanaged = %+v", events)
	}

	// Unmanaging an unmanaged user changes nothing and audits nothing.
	if r := owner.do(http.MethodPost, userPath(id, "/unmanage"), nil); r.status != http.StatusOK {
		t.Errorf("unmanage again: status %d body %s", r.status, r.body)
	}
	if n := len(h.auditEvents(t, "user.staff-unmanaged")); n != 1 {
		t.Errorf("user.staff-unmanaged rows = %d, want 1", n)
	}
}

// A SCIM-mapped user may be adopted: in federated mode the managed user is
// the identity provider's (plan reading 15).
func TestStaffManaged_AdoptsAScimMappedUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := h.insertUser(t, "scim@example.test", identity.RoleUserID)
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	          VALUES ($1, $2, $3, $3, true, 1, 'etag', $4, $4)`, uuid.New(), id, id.String(), h.now())
	if u := mustManage(t, owner, id, "S-1"); strOr(u.ManagedBy) != "staff" {
		t.Errorf("a SCIM-mapped user adopted: %+v", u)
	}
}

// Adoption refuses an Owner and a SystemAdmin (account_privileged), a
// number another user holds however it is cased (employee_number_taken),
// a body it cannot use, the caller themselves and an unknown user; none of
// them writes anything.
func TestStaffManaged_AdoptionRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	otherOwner := h.createUser(t, owner, "other-owner@example.test", identity.RoleOwner)
	admin := h.insertUser(t, "admin@example.test", identity.RoleUserID, identity.RoleSystemAdminID)
	holder := h.createUser(t, owner, "holder@example.test", identity.RoleUser)
	plain := h.createUser(t, owner, "plain@example.test", identity.RoleUser)
	mustManage(t, owner, holder, "e-7")

	for _, c := range []struct {
		name   string
		id     uuid.UUID
		body   any
		status int
		code   string
	}{
		{"an Owner", otherOwner, map[string]string{"module": "staff", "employeeNumber": "O-1"}, http.StatusConflict, "account_privileged"},
		{"a SystemAdmin", admin, map[string]string{"module": "staff", "employeeNumber": "A-1"}, http.StatusConflict, "account_privileged"},
		{"a taken number", plain, map[string]string{"module": "staff", "employeeNumber": "E-7"}, http.StatusConflict, "employee_number_taken"},
		{"another module", plain, map[string]string{"module": "entra", "employeeNumber": "P-1"}, http.StatusBadRequest, "invalid_request"},
		{"no number", plain, map[string]string{"module": "staff", "employeeNumber": "  "}, http.StatusBadRequest, "invalid_request"},
		{"a number too long", plain, map[string]string{"module": "staff", "employeeNumber": strings.Repeat("9", 17)}, http.StatusBadRequest, "invalid_request"},
		{"no body", plain, nil, http.StatusBadRequest, "invalid_request"},
		{"the caller", ownerID, map[string]string{"module": "staff", "employeeNumber": "S-1"}, http.StatusBadRequest, "invalid_request"},
	} {
		r := owner.do(http.MethodPost, userPath(c.id, "/manage"), c.body)
		if r.status != c.status || r.code() != c.code {
			t.Errorf("%s: status %d code %q, want %d %s", c.name, r.status, r.code(), c.status, c.code)
		}
	}
	if r := manage(owner, uuid.New(), "U-1"); r.status != http.StatusNotFound {
		t.Errorf("an unknown user: status %d, want 404", r.status)
	}
	if r := owner.do(http.MethodPost, userPath(uuid.New(), "/unmanage"), nil); r.status != http.StatusNotFound {
		t.Errorf("unmanage an unknown user: status %d, want 404", r.status)
	}
	if r := owner.do(http.MethodPost, userPath(ownerID, "/unmanage"), nil); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("unmanage the caller: status %d code %q, want 400 invalid_request", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE managed_by IS NOT NULL OR employee_number IS NOT NULL`); n != 1 {
		t.Errorf("managed users = %d, want only the first adoption", n)
	}
	if n := len(h.auditEvents(t, "user.staff-managed")); n != 1 {
		t.Errorf("user.staff-managed rows = %d, want 1", n)
	}
	// Adopting the holder again with another number renumbers them.
	if u := mustManage(t, owner, holder, "E-8"); strOr(u.EmployeeNumber) != "E-8" {
		t.Errorf("re-adopted with E-8: %+v", u)
	}
}

// The four operations delivery C adds to identity are an Owner's with MFA
// when Owners must use it, and nobody else's.
func TestStaffManaged_OperationsAreOwnerManagement(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	owner, _ := h.bootstrapOwner(t)
	id := h.insertUser(t, "target@example.test", identity.RoleUserID)
	standard := h.signIn(t, h.seedUser(t, "standard@example.test", userPassword, identity.RoleUserID), false)
	anonymous := h.client(t)

	ops := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, userPath(id, "/manage"), map[string]string{"module": "staff", "employeeNumber": "E-1"}},
		{http.MethodPost, userPath(id, "/unmanage"), nil},
		{http.MethodGet, provisioningRolesPath, nil},
		{http.MethodPut, provisioningRolesPath, map[string]any{"roleIds": []uuid.UUID{}}},
	}
	for _, op := range ops {
		if r := anonymous.do(op.method, op.path, op.body); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
			t.Errorf("anonymous %s %s: status %d code %q, want 401", op.method, op.path, r.status, r.code())
		}
		if r := standard.do(op.method, op.path, op.body); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("standard %s %s: status %d code %q, want 403", op.method, op.path, r.status, r.code())
		}
		if r := owner.do(op.method, op.path, op.body); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("Owner without MFA %s %s: status %d code %q, want 403", op.method, op.path, r.status, r.code())
		}
	}
	h.enrollTOTP(t, owner, ownerPassword)
	for _, op := range ops {
		if r := owner.do(op.method, op.path, op.body); r.status != http.StatusOK {
			t.Errorf("Owner with MFA %s %s: status %d body %s, want 200", op.method, op.path, r.status, r.body)
		}
	}
}

// Promoting a Staff-managed user to Owner unmanages them in the same
// change, audited with the reason.
func TestStaffManaged_PromotionToOwnerUnmanages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const email = "promoted@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	mustManage(t, owner, id, "P-1")

	r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": email, "email": email, "role": identity.RoleOwner})
	if r.status != http.StatusOK {
		t.Fatalf("promote: status %d body %s", r.status, r.body)
	}
	var u staffUser
	r.json(&u)
	if u.Role != identity.RoleOwner || u.ManagedBy != nil || u.EmployeeNumber != nil {
		t.Errorf("promoted: %+v, want an unmanaged Owner", u)
	}
	if got := staffColumns(t, h, id); got != "-:-:false:-" {
		t.Errorf("columns after the promotion = %s", got)
	}
	events := h.auditEvents(t, "user.staff-unmanaged")
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id ||
		events[0].After != `{"managedBy":null,"employeeNumber":null,"reason":"promoted"}` {
		t.Errorf("user.staff-unmanaged = %+v", events)
	}

	// An unmanaged user's promotion audits no unmanage.
	other := h.createUser(t, owner, "other@example.test", identity.RoleUser)
	if r := owner.do(http.MethodPut, userPath(other, ""), map[string]string{"displayName": "other", "email": "other@example.test", "role": identity.RoleOwner}); r.status != http.StatusOK {
		t.Fatalf("promote an unmanaged user: status %d body %s", r.status, r.body)
	}
	if n := len(h.auditEvents(t, "user.staff-unmanaged")); n != 1 {
		t.Errorf("user.staff-unmanaged rows = %d, want 1", n)
	}
}

// Deleting a Staff-managed user is refused account_managed; once unmanaged
// the user is deleted as before.
func TestStaffManaged_DeleteRefusesAManagedUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "managed@example.test", identity.RoleUser)
	mustManage(t, owner, id, "D-1")

	if r := owner.do(http.MethodDelete, userPath(id, ""), nil); r.status != http.StatusConflict || r.code() != "account_managed" {
		t.Errorf("delete a managed user: status %d code %q, want 409 account_managed", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1`, id); n != 1 {
		t.Fatalf("the managed user was deleted")
	}
	if n := len(h.auditEvents(t, "user.deleted")); n != 0 {
		t.Errorf("user.deleted rows = %d, want 0", n)
	}
	if r := owner.do(http.MethodPost, userPath(id, "/unmanage"), nil); r.status != http.StatusOK {
		t.Fatalf("unmanage: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, userPath(id, ""), nil); r.status != http.StatusNoContent {
		t.Errorf("delete the unmanaged user: status %d body %s, want 204", r.status, r.body)
	}
}

// An Owner's disable records disabled_by = owner, also over a user Staff
// disabled, so Staff never re-enables them; enabling clears it.
func TestStaffManaged_DisableAndEnableRecordWhoDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := h.createUser(t, owner, "worker@example.test", identity.RoleUser)

	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	if u := listedStaffUser(t, owner, id); !u.Disabled || strOr(u.DisabledBy) != "owner" {
		t.Errorf("disabled by the Owner: %+v", u)
	}
	if r := owner.do(http.MethodPost, userPath(id, "/enable"), nil); r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	if u := listedStaffUser(t, owner, id); u.Disabled || u.DisabledBy != nil {
		t.Errorf("enabled: %+v", u)
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'staff' WHERE id = $1`, id)
	if u := listedStaffUser(t, owner, id); strOr(u.DisabledBy) != "staff" {
		t.Errorf("disabled by Staff: %+v", u)
	}
	before := len(h.auditEvents(t, "user.disabled"))
	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("disable a Staff-disabled user: status %d body %s", r.status, r.body)
	}
	if got := staffColumns(t, h, id); got != "-:-:false:owner" {
		t.Errorf("columns after the Owner's disable over Staff's = %s", got)
	}
	if n := len(h.auditEvents(t, "user.disabled")); n != before+1 {
		t.Errorf("user.disabled rows = %d, want %d", n, before+1)
	}
	// Disabling it again changes nothing.
	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("disable again: status %d body %s", r.status, r.body)
	}
	if n := len(h.auditEvents(t, "user.disabled")); n != before+1 {
		t.Errorf("user.disabled rows after a second disable = %d, want %d", n, before+1)
	}
}

// The list's new fields: a pending user is listed disabled and pending,
// not active; the others as before. pending is always sent, managedBy,
// employeeNumber and disabledBy only when set (optional fields absent: the
// recorded .NET exchanges predate them, so none is required).
func TestStaffManaged_TheListShowsPendingManagedAndWhoDisabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	plain := h.createUser(t, owner, "plain@example.test", identity.RoleUser)
	pending := h.insertUser(t, "pending@example.test", identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'N-1', pending_since = $2, is_disabled = true, disabled_by = 'staff'
	           WHERE id = $1`, pending, h.now())

	// pending is always sent; the other three only when set.
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	var raw []map[string]json.RawMessage
	r.json(&raw)
	for _, u := range raw {
		var id uuid.UUID
		_ = json.Unmarshal(u["id"], &id)
		for _, k := range []string{"pending", "managedBy", "employeeNumber", "disabledBy"} {
			if _, ok := u[k]; ok != (k == "pending" || id == pending) {
				t.Errorf("user %s: %s sent %v", id, k, ok)
			}
		}
	}
	if u := listedStaffUser(t, owner, plain); u.Pending || u.ManagedBy != nil || u.EmployeeNumber != nil || u.DisabledBy != nil || !u.Active || u.Disabled {
		t.Errorf("an ordinary user: %+v", u)
	}
	if u := listedStaffUser(t, owner, pending); !u.Pending || !u.Disabled || u.Active || strOr(u.ManagedBy) != "staff" ||
		strOr(u.EmployeeNumber) != "N-1" || strOr(u.DisabledBy) != "staff" {
		t.Errorf("a pending Staff user: %+v", u)
	}
	// A pending user an Owner enabled is still listed disabled until they
	// choose a credential.
	h.exec(t, `UPDATE identity.users SET is_disabled = false, disabled_by = NULL WHERE id = $1`, pending)
	if u := listedStaffUser(t, owner, pending); !u.Pending || !u.Disabled || u.Active {
		t.Errorf("a pending user enabled: %+v, want listed disabled, pending, not active", u)
	}
}

// revokedReason is the revoked_reason of invitation id, "-" for null.
func revokedReason(t *testing.T, h *harness, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := h.pool.QueryRow(t.Context(), `SELECT coalesce(revoked_reason, '-') FROM identity.invitations WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("revoked_reason of %s: %v", id, err)
	}
	return s
}

// Every revoke site writes why: the Owner's revoke owner, a resend's
// replacement replaced, an account created for the address replaced, a
// failed delivery delivery_failed, a user's email change email_changed.
func TestStaffManaged_EveryRevokeRecordsItsReason(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)

	revoked, _ := invite(t, h, owner, map[string]string{"email": "revoked@example.test", "role": identity.RoleUser})
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+revoked.ID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke: status %d body %s", r.status, r.body)
	}
	if got := revokedReason(t, h, revoked.ID); got != "owner" {
		t.Errorf("the Owner's revoke: %s, want owner", got)
	}
	// A second revoke leaves the reason as it was.
	if r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+revoked.ID.String()+"/revoke", nil); r.status != http.StatusOK {
		t.Fatalf("revoke again: status %d body %s", r.status, r.body)
	}

	resent, _ := invite(t, h, owner, map[string]string{"email": "resent@example.test", "role": identity.RoleUser})
	r := owner.do(http.MethodPost, ownerInvitationsPath+"/"+resent.ID.String()+"/resend", nil)
	if r.status != http.StatusOK {
		t.Fatalf("resend: status %d body %s", r.status, r.body)
	}
	var fresh invitation
	r.json(&fresh)
	if got := revokedReason(t, h, resent.ID); got != "replaced" {
		t.Errorf("the resent invitation: %s, want replaced", got)
	}
	if got := revokedReason(t, h, fresh.ID); got != "-" {
		t.Errorf("the resend: %s, want none", got)
	}

	created, _ := invite(t, h, owner, map[string]string{"email": "created@example.test", "role": identity.RoleUser})
	h.createUser(t, owner, "created@example.test", identity.RoleUser)
	if got := revokedReason(t, h, created.ID); got != "replaced" {
		t.Errorf("an invitation to an address an account was created for: %s, want replaced", got)
	}

	h.mail.FailNext(errors.New(mailFailureMarker))
	if r := owner.do(http.MethodPost, ownerInvitationsPath, map[string]string{"email": "undelivered@example.test", "role": identity.RoleUser},
		skipContract("send failure 500")); r.status != http.StatusInternalServerError {
		t.Fatalf("a failed send: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE email = 'undelivered@example.test' AND revoked_reason = 'delivery_failed'`); n != 1 {
		t.Errorf("undelivered invitations revoked delivery_failed = %d, want 1", n)
	}

	moved, _ := invite(t, h, owner, map[string]string{"email": "moved@example.test", "role": identity.RoleUser})
	id := h.createUser(t, owner, "mover@example.test", identity.RoleUser)
	if r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Mover", "email": "moved@example.test", "role": identity.RoleUser}); r.status != http.StatusOK {
		t.Fatalf("change the email: status %d body %s", r.status, r.body)
	}
	if got := revokedReason(t, h, moved.ID); got != "email_changed" {
		t.Errorf("an invitation to the new address: %s, want email_changed", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE revoked_at IS NOT NULL AND revoked_reason IS NULL`); n != 0 {
		t.Errorf("revoked invitations without a reason = %d", n)
	}
}

// provisioningRoles is ProvisioningRolesResponse as a test reads it.
type provisioningRoles struct {
	Roles []struct {
		ID             uuid.UUID `json:"id"`
		Name           string    `json:"name"`
		DisplayName    string    `json:"displayName"`
		PermissionKeys []string  `json:"permissionKeys"`
	} `json:"roles"`
}

func (p provisioningRoles) ids() []uuid.UUID {
	out := make([]uuid.UUID, len(p.Roles))
	for i, r := range p.Roles {
		out[i] = r.ID
	}
	return out
}

func getProvisioningRoles(t *testing.T, owner *client) provisioningRoles {
	t.Helper()
	r := owner.do(http.MethodGet, provisioningRolesPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("get provisioning roles: status %d body %s", r.status, r.body)
	}
	var p provisioningRoles
	r.json(&p)
	return p
}

func putProvisioningRoles(owner *client, ids ...uuid.UUID) *resp {
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return owner.do(http.MethodPut, provisioningRolesPath, map[string]any{"roleIds": ids})
}

// The roles Staff may give: custom roles only, the set replaced whole,
// listed by name, audited, and gone with a deleted role.
func TestProvisioningRoles_CustomOnlyReplacedWholeAndCascading(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)

	if p := getProvisioningRoles(t, owner); len(p.Roles) != 0 {
		t.Fatalf("a fresh installation's provisioning roles = %+v, want none", p)
	}
	employee := createRole(t, owner, "Employee", customersView.Key)
	seasonal := createRole(t, owner, "Seasonal")

	r := putProvisioningRoles(owner, seasonal.ID, employee.ID)
	if r.status != http.StatusOK {
		t.Fatalf("put: status %d body %s", r.status, r.body)
	}
	var put provisioningRoles
	r.json(&put)
	if len(put.Roles) != 2 || put.Roles[0].Name != "Employee" || put.Roles[0].DisplayName != "Employee" || put.Roles[1].Name != "Seasonal" {
		t.Errorf("put answered %+v, want Employee and Seasonal by name", put)
	}
	if got := getProvisioningRoles(t, owner).ids(); !slices.Equal(got, []uuid.UUID{employee.ID, seasonal.ID}) {
		t.Errorf("get = %v, want [Employee Seasonal]", got)
	}
	events := h.auditEvents(t, "provisioning-roles.replaced")
	want := []uuid.UUID{employee.ID, seasonal.ID}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	wantAfter, _ := json.Marshal(map[string][]uuid.UUID{"roleIds": want})
	if len(events) != 1 || *events[0].Actor != ownerID || events[0].Before != `{"roleIds":[]}` || events[0].After != string(wantAfter) {
		t.Errorf("provisioning-roles.replaced = %+v, want after %s", events, wantAfter)
	}

	// Built-in, system and unknown roles are not Staff's to give; a refusal
	// leaves the set as it was.
	for name, id := range map[string]uuid.UUID{
		"User": identity.RoleUserID, "Owner": identity.RoleOwnerID, "SystemAdmin": identity.RoleSystemAdminID, "unknown": uuid.New(),
	} {
		if r := putProvisioningRoles(owner, employee.ID, id); r.status != http.StatusBadRequest || r.code() != "role_not_assignable" {
			t.Errorf("put with %s: status %d code %q, want 400 role_not_assignable", name, r.status, r.code())
		}
	}
	if got := getProvisioningRoles(t, owner).ids(); len(got) != 2 {
		t.Errorf("after the refusals = %v, want the two kept", got)
	}

	// Replaced whole: one role names the set.
	if r := putProvisioningRoles(owner, employee.ID); r.status != http.StatusOK {
		t.Fatalf("put one: status %d body %s", r.status, r.body)
	}
	if got := getProvisioningRoles(t, owner).ids(); !slices.Equal(got, []uuid.UUID{employee.ID}) {
		t.Errorf("after replacing with Employee = %v", got)
	}
	// A deleted role leaves the set.
	if r := deleteRole(owner, employee.ID, employee.Version); r.status != http.StatusOK && r.status != http.StatusNoContent {
		t.Fatalf("delete the role: status %d body %s", r.status, r.body)
	}
	if got := getProvisioningRoles(t, owner).ids(); len(got) != 0 {
		t.Errorf("after the role's delete = %v, want none", got)
	}
	if r := putProvisioningRoles(owner); r.status != http.StatusOK {
		t.Errorf("put an empty set: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, provisioningRolesPath, map[string]any{}); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("put without roleIds: status %d code %q, want 400 invalid_request", r.status, r.code())
	}
}

// makePending marks id a pending Staff-managed user, enabled, as a test
// arranges what no endpoint makes yet.
func makePending(t *testing.T, h *harness, id uuid.UUID, number string) {
	t.Helper()
	h.exec(t, `UPDATE identity.users SET managed_by = 'staff', employee_number = $2, pending_since = $3, is_disabled = false, disabled_by = NULL
	           WHERE id = $1`, id, number, h.now())
}

// A pending user becomes usable only by accepting their invitation: an
// Owner can neither promote them to Owner nor enable them (account_pending),
// and neither writes anything.
func TestStaffManaged_APendingUserIsNeitherPromotedNorEnabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "pending@example.test"
	id := h.insertUser(t, email, identity.RoleUserID)
	makePending(t, h, id, "P-9")

	r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": email, "email": email, "role": identity.RoleOwner})
	if r.status != http.StatusConflict || r.code() != "account_pending" {
		t.Errorf("promote a pending user: status %d code %q, want 409 account_pending", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, id, identity.RoleOwnerID); n != 0 {
		t.Errorf("the pending user holds Owner")
	}
	if n := len(h.auditEvents(t, "user.role-changed")); n != 0 {
		t.Errorf("user.role-changed rows = %d, want 0", n)
	}
	// Editing a pending user's name, as a User, is still an Owner's.
	if r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Renamed", "email": email, "role": identity.RoleUser}); r.status != http.StatusOK {
		t.Errorf("rename a pending user: status %d body %s, want 200", r.status, r.body)
	}

	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'staff' WHERE id = $1`, id)
	if r := owner.do(http.MethodPost, userPath(id, "/enable"), nil); r.status != http.StatusConflict || r.code() != "account_pending" {
		t.Errorf("enable a pending user: status %d code %q, want 409 account_pending", r.status, r.code())
	}
	if got := staffColumns(t, h, id); got != "staff:P-9:true:staff" {
		t.Errorf("columns after the refused enable = %s", got)
	}
	if n := len(h.auditEvents(t, "user.enabled")); n != 0 {
		t.Errorf("user.enabled rows = %d, want 0", n)
	}
	// Disabling one is still an Owner's.
	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Errorf("disable a pending user: status %d body %s, want 200", r.status, r.body)
	}
}

// A pending user holding Owner (no endpoint makes one; a test does) is no
// active Owner: with them and one real Owner, the real one is the last.
func TestStaffManaged_APendingOwnerIsNotAnActiveOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, ownerID := h.bootstrapOwner(t)
	pending := h.insertUser(t, "pending-owner@example.test", identity.RoleOwnerID)
	makePending(t, h, pending, "P-10")
	caller := h.signIn(t, pending, true)

	if r := caller.do(http.MethodPost, userPath(ownerID, "/disable"), nil); r.status != http.StatusConflict || r.code() != "last_active_owner" {
		t.Errorf("disable the only real Owner: status %d code %q, want 409 last_active_owner", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND NOT is_disabled`, ownerID); n != 1 {
		t.Errorf("the only real Owner was disabled")
	}
}

// A role that grants administration is never Staff's to give: a custom role
// carrying identity:manage, staff:manage, staff:personal-data or
// staff:approve is refused role_not_assignable naming the key, and nothing
// is written. The set lists each role's permission keys.
func TestProvisioningRoles_RefuseAdministrativePermissionsAndListKeys(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	employee := createRole(t, owner, "Employee", customersView.Key)
	empty := createRole(t, owner, "Empty")
	if r := putProvisioningRoles(owner, employee.ID, empty.ID); r.status != http.StatusOK {
		t.Fatalf("put: status %d body %s", r.status, r.body)
	}
	got := getProvisioningRoles(t, owner)
	if len(got.Roles) != 2 || !slices.Equal(got.Roles[0].PermissionKeys, []string{customersView.Key}) ||
		got.Roles[1].PermissionKeys == nil || len(got.Roles[1].PermissionKeys) != 0 {
		t.Errorf("the set's keys = %+v, want Employee [customers:view] and Empty []", got)
	}

	for _, key := range []string{"identity:manage", "staff:manage", "staff:personal-data", "staff:approve"} {
		role := h.insertRole(t, "Admin "+key, customersView.Key, key)
		r := putProvisioningRoles(owner, employee.ID, role)
		e := errorOf(t, r)
		if r.status != http.StatusBadRequest || e.Code != "role_not_assignable" || !strings.Contains(e.Message, key) {
			t.Errorf("a role with %s: status %d error %+v, want 400 role_not_assignable naming the key", key, r.status, e)
		}
	}
	if ids := getProvisioningRoles(t, owner).ids(); len(ids) != 2 {
		t.Errorf("after the refusals = %v, want the two kept", ids)
	}
	if n := len(h.auditEvents(t, "provisioning-roles.replaced")); n != 1 {
		t.Errorf("provisioning-roles.replaced rows = %d, want 1", n)
	}
}
