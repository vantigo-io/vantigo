package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The roles Staff may give (staff foundation design S26, delivery C): the
// custom roles an Owner names in identity.provisioning_roles, the employee
// roles Staff's provisioning assigns to the users it activates. Identity
// holds the set, written only by an Owner, because staff:manage must not
// mint an account holding any role it names.

var (
	// roleNotAssignable refuses a provisioning role that is not a custom
	// role: built in, system, or unknown. A custom role granting
	// administration is refused with the same code by deniedRoleRefusal.
	roleNotAssignable = refuse(http.StatusBadRequest, "role_not_assignable", "Only custom roles can be roles Staff may give.", nil)
	// roleProvisioningProtected refuses an edit giving a role in the set
	// Staff may give a key the set refuses (provisioningDeniedKeys).
	roleProvisioningProtected = refuseFlat(http.StatusForbidden, "role_provisioning_protected", "A role Staff may give cannot receive a permission that grants administration.")
	// provisioningRolesConflict answers a replacement that lost a race on
	// every attempt.
	provisioningRolesConflict = refuse(http.StatusConflict, "account_conflict", "The roles Staff may give were changed at the same time; try again.", nil)
)

// staffAdministrativeKeys are Staff's keys that grant administration over
// its register and its decisions, delegable or not.
var staffAdministrativeKeys = []string{"staff:approve", "staff:manage", "staff:personal-data"}

// provisioningDeniedKeys are the permissions a role Staff may give must not
// carry, in ordinal order: each grants administration that staff:manage
// must never be able to hand an account it provisions — every non-delegable
// key of the catalog (identity:manage and any added later; protectedPermission)
// and Staff's administrative keys. PUT /owner/provisioning-roles checks the
// set against it, and a role edit may not give a role in the set one
// (guardProvisioningRole); the provider's Activate re-checks at assignment
// time.
func (s *server) provisioningDeniedKeys() []string {
	return provisioningDeniedKeys(s.deps.Catalog)
}

// provisioningDeniedKeys is the denied set over catalog: identity:manage
// and Staff's administrative keys always, and every key catalog holds that
// cannot be delegated. The provider, built before any module mounts, passes
// the catalog identity's mount gave its Access, which a worker-only process
// never mounts: there the set is the fixed keys alone, which today are every
// non-delegable key there is.
func provisioningDeniedKeys(catalog map[string]contracts.Permission) []string {
	keys := append([]string{"identity:manage"}, staffAdministrativeKeys...)
	for key, p := range catalog {
		if !p.Delegable {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// guardProvisioningRole refuses keys, the permissions a role is to have,
// when the role is in the set Staff may give and keys hold one the set
// refuses: 403 role_provisioning_protected. The caller holds the role's
// lock, which PUT /owner/provisioning-roles also takes for each role it
// names: an edit waits for a replacement naming its role to commit, and a
// replacement behind an edit judges the role as the edit left it
// (LockAssignableRoles).
func (s *server) guardProvisioningRole(ctx context.Context, q *store.Queries, roleID uuid.UUID, keys []string) error {
	inSet, err := q.IsProvisioningRole(ctx, roleID)
	if err != nil || !inSet {
		return err
	}
	denied := s.provisioningDeniedKeys()
	if slices.ContainsFunc(keys, func(key string) bool { return slices.Contains(denied, key) }) {
		return roleProvisioningProtected
	}
	return nil
}

// deniedProvisioningPermission reports the first of roleIDs, by name, that
// carries a key of provisioningDeniedKeys, as a refusal naming the role and
// the key, or nil when none does.
func (s *server) deniedProvisioningPermission(ctx context.Context, q *store.Queries, roleIDs []uuid.UUID) error {
	row, err := q.FirstDeniedRolePermission(ctx, store.FirstDeniedRolePermissionParams{RoleIds: roleIDs, Keys: s.provisioningDeniedKeys()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return refuse(http.StatusBadRequest, "role_not_assignable",
		fmt.Sprintf("The role %s carries %s, which grants administration; Staff may not give it.", row.Name, row.PermissionKey), nil)
}

// provisioningRolesAudit is provisioning-roles.replaced's before and after:
// the set's role ids in ordinal order.
type provisioningRolesAudit struct {
	RoleIDs []uuid.UUID `json:"roleIds"`
}

// GetIdentityOwnerProvisioningRoles lists the roles Staff may give, by name,
// for an Owner.
func (s *server) GetIdentityOwnerProvisioningRoles(ctx context.Context, _ gen.GetIdentityOwnerProvisioningRolesRequestObject) (gen.GetIdentityOwnerProvisioningRolesResponseObject, error) {
	out, err := provisioningRoles(ctx, s.q)
	if err != nil {
		return nil, fmt.Errorf("identity: list provisioning roles: %w", err)
	}
	return gen.GetIdentityOwnerProvisioningRoles200JSONResponse(out), nil
}

// PutIdentityOwnerProvisioningRoles replaces the roles Staff may give with
// roleIds, whole, for an Owner (400 invalid_request without roleIds). In one
// serializable transaction each named role's lock is taken in ordinal order,
// so no role edit starts while the set is judged, then the roles FOR SHARE
// (LockAssignableRoles): a role edit that committed while the replacement
// waited on a role's lock makes it fail to serialize and retry, so the check
// reads the role's keys as the edit left them, and no role is deleted or
// edited before the set commits. Any that is not a custom role is 400
// role_not_assignable and nothing changes, as is one carrying a permission
// of provisioningDeniedKeys (the message naming it). A change is audited
// provisioning-roles.replaced with the set before and after. 200 with the
// set.
func (s *server) PutIdentityOwnerProvisioningRoles(ctx context.Context, req gen.PutIdentityOwnerProvisioningRolesRequestObject) (gen.PutIdentityOwnerProvisioningRolesResponseObject, error) {
	if req.Body == nil || req.Body.RoleIds == nil {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, map[string][]string{"roleIds": {"roleIds is required."}}), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	ids := ordinalIDs(*req.Body.RoleIds)

	var out gen.ProvisioningRolesResponse
	err = s.serializable(ctx, provisioningRolesConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, id := range ids {
			if err := lockRole(ctx, q, id); err != nil {
				return err
			}
		}
		locked, err := q.LockAssignableRoles(ctx, ids)
		if err != nil {
			return err
		}
		if len(locked) != len(ids) {
			return roleNotAssignable
		}
		if err := s.deniedProvisioningPermission(ctx, q, ids); err != nil {
			return err
		}
		before, err := q.ListProvisioningRoles(ctx)
		if err != nil {
			return err
		}
		beforeIDs := make([]uuid.UUID, len(before))
		for i, role := range before {
			beforeIDs[i] = role.ID
		}
		beforeIDs = ordinalIDs(beforeIDs)
		if !slices.Equal(beforeIDs, ids) {
			now := s.deps.Clock()
			if err := q.DeleteProvisioningRolesExcept(ctx, ids); err != nil {
				return err
			}
			if err := q.InsertProvisioningRoles(ctx, store.InsertProvisioningRolesParams{RoleIds: ids, Now: now}); err != nil {
				return err
			}
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor:  &p.UserID,
				action: "provisioning-roles.replaced",
				before: provisioningRolesAudit{RoleIDs: beforeIDs},
				after:  provisioningRolesAudit{RoleIDs: ids},
				mfa:    p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		out, err = provisioningRoles(ctx, q)
		return err
	})
	if err != nil {
		return refusalOr[gen.PutIdentityOwnerProvisioningRolesResponseObject](err)
	}
	return gen.PutIdentityOwnerProvisioningRoles200JSONResponse(out), nil
}

// provisioningRoles reads the set on q as the contract shows it.
func provisioningRoles(ctx context.Context, q *store.Queries) (gen.ProvisioningRolesResponse, error) {
	rows, err := q.ListProvisioningRoles(ctx)
	if err != nil {
		return gen.ProvisioningRolesResponse{}, err
	}
	out := gen.ProvisioningRolesResponse{Roles: make([]gen.ProvisioningRole, len(rows))}
	for i, role := range rows {
		out.Roles[i] = gen.ProvisioningRole{Id: role.ID, Name: role.Name, DisplayName: role.DisplayName, PermissionKeys: nonNil(role.PermissionKeys)}
	}
	return out, nil
}

// ordinalIDs is ids without duplicates, in the ordinal order of their text,
// never nil.
func ordinalIDs(ids []uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return nonNil(slices.Compact(out))
}

func (r refusal) VisitPutIdentityOwnerProvisioningRolesResponse(w http.ResponseWriter) error {
	return r.write(w)
}
