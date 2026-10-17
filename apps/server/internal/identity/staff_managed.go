package identity

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// Staff-managed users (staff foundation design S24, delivery C). A user is
// Staff-managed when identity.users.managed_by is 'staff': Staff's
// provisioning may then enable, disable and update them, and nothing else
// may delete them. "Managed" alone already means an Owner's User/Owner role
// management in this package (isManagedRole, ListManagedUsers), so the Go
// names here say staffManaged. An Owner adopts an existing user for Staff
// with the employee number that ties them to a person in Staff's register,
// and stops Staff managing them; the columns are identity's, and Staff
// reaches them only through the provisioning contract.

const (
	// staffModule is the one module that manages users.
	staffModule = "staff"
	// disabledByOwnerValue is identity.users.disabled_by for an Owner's
	// disable.
	disabledByOwnerValue = "owner"
	// disabledByStaffValue is identity.users.disabled_by for Staff's.
	disabledByStaffValue = "staff"
	// staffUnmanagedPromoted is user.staff-unmanaged's reason when a
	// promotion to Owner ended the management.
	staffUnmanagedPromoted = "promoted"
)

var (
	// accountPrivileged refuses Staff managing an Owner or a SystemAdmin:
	// an Owner handles Owners by hand.
	accountPrivileged = refuse(http.StatusConflict, "account_privileged", "An Owner or a SystemAdmin cannot be managed by Staff.", nil)
	// employeeNumberTaken refuses an employee number another user holds.
	employeeNumberTaken = refuse(http.StatusConflict, "employee_number_taken", "Another user already holds this employee number.", nil)
	// accountPending refuses making a pending user usable by any other way
	// than accepting their invitation: an Owner's enable, or a promotion.
	accountPending = refuse(http.StatusConflict, "account_pending", "This user has not accepted their invitation yet; the account becomes usable only by accepting it.", nil)
	// accountManaged refuses deleting a user Staff manages.
	accountManaged = refuse(http.StatusConflict, "account_managed", "This user is managed by Staff and cannot be deleted; stop Staff managing them first.", nil)
)

// employeeNumberPattern is Staff's own alphabet for an employee number
// (staff/values.go's numberPattern): letters a–z in either case, digits and
// the hyphen, at most 16 (identity.users.employee_number's bound, and
// staff.people.employee_number's). Identity can never usefully
// hold a number Staff cannot hold, and within ASCII the key's case mapping is
// the same under every Unicode version Go ships.
var employeeNumberPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,16}$`)

// normalizeEmployeeNumber is how identity takes an employee number, from an
// Owner's adoption or from Staff's provisioning alike: trimmed, then judged
// against Staff's alphabet (employeeNumberPattern). It returns the number to
// store and whether it is valid; its key is setStaffManaged's to derive.
func normalizeEmployeeNumber(raw string) (number string, valid bool) {
	number = strings.TrimSpace(raw)
	return number, employeeNumberPattern.MatchString(number)
}

// setStaffManaged is the one writer of a user's managed_by, employee number
// and its key on q: the key is derived here, from the number, by
// contracts.NormalizeEmployeeNumber — no caller passes one, so the number
// and the key ux_users_employee_number compares can never disagree. A nil
// number clears all three (the end of the management).
func setStaffManaged(ctx context.Context, q *store.Queries, id uuid.UUID, number *string, now time.Time) error {
	p := store.SetUserStaffManagedParams{ID: id, Version: uuid.New(), Now: now}
	if number != nil {
		module, key := staffModule, contracts.NormalizeEmployeeNumber(*number)
		p.ManagedBy, p.EmployeeNumber, p.EmployeeNumberKey = &module, number, &key
	}
	return q.SetUserStaffManaged(ctx, p)
}

// staffManaged reports whether Staff manages u.
func staffManaged(u store.IdentityUser) bool {
	return u.ManagedBy != nil
}

// isPending reports whether u is a pending user: one Staff created who has
// not yet chosen a credential through their invitation.
func isPending(u store.IdentityUser) bool {
	return u.PendingSince != nil
}

// disabledByOwner reports whether an Owner disabled u.
func disabledByOwner(u store.IdentityUser) bool {
	return u.DisabledBy != nil && *u.DisabledBy == disabledByOwnerValue
}

// disabledByAudit is user.disabled-by-changed's before and after: who had
// disabled the user, and who has now (null once enabled).
type disabledByAudit struct {
	DisabledBy *string `json:"disabledBy"`
}

// staffManagedAudit is the before and after of user.staff-managed and
// user.staff-unmanaged; reason is set only when the management ended for a
// reason other than an Owner's unmanage (promoted).
type staffManagedAudit struct {
	ManagedBy      *string `json:"managedBy"`
	EmployeeNumber *string `json:"employeeNumber"`
	Reason         string  `json:"reason,omitempty"`
}

// privileged reports whether the user holds Owner or SystemAdmin, which
// only user_roles grants (an access group never maps a built-in role).
func privileged(ctx context.Context, q *store.Queries, id uuid.UUID) (bool, error) {
	roles, err := q.GetUserRoleNames(ctx, id)
	if err != nil {
		return false, err
	}
	return slices.Contains(roles, RoleOwner) || slices.Contains(roles, RoleSystemAdmin), nil
}

// PostIdentityOwnerUsersByIdManage adopts another user for Staff for an
// Owner: managed_by staff and the employee number. After validation (400
// invalid_request: the module is staff, the number 1–16 characters of
// Staff's alphabet once trimmed — see normalizeEmployeeNumber)
// and the self check, one serializable transaction under the owner
// lock: an unknown user is 404; an Owner or a SystemAdmin is 409
// account_privileged; a number another user holds, however it is cased, is
// 409 employee_number_taken, also when only the unique index sees a
// concurrent adoption's number. The comparison is the number's key on both
// sides (contracts.NormalizeEmployeeNumber), the index's and
// GetUserIDByEmployeeNumber's. A SCIM-mapped user is admitted (plan reading
// 15). A change is audited user.staff-managed; adopting a user again with
// the number they hold changes nothing.
func (s *server) PostIdentityOwnerUsersByIdManage(ctx context.Context, req gen.PostIdentityOwnerUsersByIdManageRequestObject) (gen.PostIdentityOwnerUsersByIdManageResponseObject, error) {
	var body gen.OwnerUserManageRequest
	if req.Body != nil {
		body = *req.Body
	}
	number, numberValid := normalizeEmployeeNumber(deref(body.EmployeeNumber))
	fields := map[string][]string{}
	if deref(body.Module) != staffModule {
		fields["module"] = []string{"Module must be staff."}
	}
	if !numberValid {
		fields["employeeNumber"] = []string{"An employee number of 1 to 16 characters, each a letter a–z, a digit or a hyphen, is required."}
	}
	if len(fields) > 0 {
		return refuse(http.StatusBadRequest, "invalid_request", userInvalidMessage, fields), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdManageResponseObject](err)
	}

	var out gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if priv, err := privileged(ctx, q, target.ID); err != nil || priv {
			return cmpOr(err, error(accountPrivileged))
		}
		holder, err := q.GetUserIDByEmployeeNumber(ctx, contracts.NormalizeEmployeeNumber(number))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case holder != target.ID:
			return employeeNumberTaken
		}
		now := s.deps.Clock()
		if !staffManaged(target) || deref(target.EmployeeNumber) != number {
			module := staffModule
			if err := setStaffManaged(ctx, q, target.ID, &number, now); err != nil {
				// A concurrent adoption that took the number first, seen
				// only by the index.
				if db.IsUniqueViolation(err, "ux_users_employee_number") {
					return employeeNumberTaken
				}
				return err
			}
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor:      &p.UserID,
				targetUser: &target.ID,
				action:     "user.staff-managed",
				before:     staffManagedAudit{ManagedBy: target.ManagedBy, EmployeeNumber: target.EmployeeNumber},
				after:      staffManagedAudit{ManagedBy: &module, EmployeeNumber: &number},
				mfa:        p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		out, err = s.managedUser(ctx, q, target.ID, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdManageResponseObject](err)
	}
	return gen.PostIdentityOwnerUsersByIdManage200JSONResponse(out), nil
}

// PostIdentityOwnerUsersByIdUnmanage stops Staff managing another user for
// an Owner: after the self check, one serializable transaction under the
// owner lock clears managed_by and the employee number, audited
// user.staff-unmanaged. pending_since is left: a pending user an Owner
// unmanages is the Owner's to finish or delete. An unknown user is 404; a
// user Staff does not manage changes nothing.
func (s *server) PostIdentityOwnerUsersByIdUnmanage(ctx context.Context, req gen.PostIdentityOwnerUsersByIdUnmanageRequestObject) (gen.PostIdentityOwnerUsersByIdUnmanageResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdUnmanageResponseObject](err)
	}
	var out gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, req.Id)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		if staffManaged(target) || target.EmployeeNumber != nil {
			if err := unmanageStaff(ctx, q, r, p, target, "", now); err != nil {
				return err
			}
		}
		out, err = s.managedUser(ctx, q, target.ID, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdUnmanageResponseObject](err)
	}
	return gen.PostIdentityOwnerUsersByIdUnmanage200JSONResponse(out), nil
}

// unmanageStaff clears target's managed_by and employee number on q, the
// caller's transaction under the owner lock, and audits
// user.staff-unmanaged by p with reason (empty for an Owner's unmanage).
func unmanageStaff(ctx context.Context, q *store.Queries, r *http.Request, p contracts.Principal, target store.IdentityUser, reason string, now time.Time) error {
	if err := setStaffManaged(ctx, q, target.ID, nil, now); err != nil {
		return err
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		actor:      &p.UserID,
		targetUser: &target.ID,
		action:     "user.staff-unmanaged",
		before:     staffManagedAudit{ManagedBy: target.ManagedBy, EmployeeNumber: target.EmployeeNumber},
		after:      staffManagedAudit{Reason: reason},
		mfa:        p.MFAVerified,
	})
}

func (r refusal) VisitPostIdentityOwnerUsersByIdManageResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdUnmanageResponse(w http.ResponseWriter) error {
	return r.write(w)
}
