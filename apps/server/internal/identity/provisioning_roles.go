package identity

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	// role: built in, system, or unknown.
	roleNotAssignable = refuse(http.StatusBadRequest, "role_not_assignable", "Only custom roles can be roles Staff may give.", nil)
	// provisioningRolesConflict answers a replacement that lost a race on
	// every attempt.
	provisioningRolesConflict = refuse(http.StatusConflict, "account_conflict", "The roles Staff may give were changed at the same time; try again.", nil)
)

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
// serializable transaction the named roles are taken FOR KEY SHARE, so none
// is deleted before the set commits; any that is not a custom role is 400
// role_not_assignable and nothing changes. A change is audited
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
		locked, err := q.LockAssignableRoles(ctx, ids)
		if err != nil {
			return err
		}
		if len(locked) != len(ids) {
			return roleNotAssignable
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
		out.Roles[i] = gen.ProvisioningRole{Id: role.ID, Name: role.Name, DisplayName: role.DisplayName}
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
