package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// ownerUsersPath is the owner user-management collection: the Location of
// a created user, and the prefix of each user's owner avatar URL.
const ownerUsersPath = "/api/v1/identity/owner/users"

// serializableAttempts is how often a serializable owner-management
// transaction runs before a race it keeps losing is answered as a conflict.
// Each retry starts from a fresh snapshot, so a request that lost to a
// concurrent change then sees that change and answers for it: of two
// concurrent demotions of the last two active Owners, the second gets
// last_active_owner, not a conflict.
const serializableAttempts = 3

// maxManagedEmailLength is the longest email an Owner may give an account
// (EA/AuthAccountEndpoints.cs:1034-1040).
const maxManagedEmailLength = 256

// Owner user management's messages (EA/AuthAccountEndpoints.cs:480-1040).
const (
	userInvalidMessage            = "The user request is invalid."
	selfManagementMessage         = "Use the self-service account endpoints for your own account."
	accountExistsMessage          = "An account already exists for this email address."
	userNotCreatedMessage         = "The user account could not be created."
	userPasswordNotChangedMessage = "The user password could not be changed."
	provenanceConflictMessage     = "This user has SCIM or federated identity history and cannot be deleted."
)

var (
	// userConflict answers a user change that lost a race on every attempt,
	// as each user handler's own catch did (EA/AuthAccountEndpoints.cs:523-526).
	userConflict = refuse(http.StatusConflict, "account_conflict", "The user account conflicts with another account change.", nil)
	// accountExists refuses an email another account already has.
	accountExists = refuse(http.StatusConflict, "account_exists", accountExistsMessage, nil)
)

// serializable runs fn in one SERIALIZABLE transaction, retried from a
// fresh snapshot while it loses a serialization race. A refusal fn returns
// rolls the transaction back and comes back as it is. A race lost on every
// attempt, or a unique violation, is answered with conflict, as .NET's
// owner-management filter answered serialization failures, deadlocks and
// unique violations (EA/AuthAccountEndpoints.cs:28-39). Anything else is a
// server error.
func (s *server) serializable(ctx context.Context, conflict refusal, fn func(tx pgx.Tx) error) error {
	err := db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
	})
	if db.IsSerializationConflict(err) || db.IsUniqueViolation(err, "") {
		return conflict
	}
	return err
}

// rejectSelf is .NET's RejectSelfManagement (EA/AuthAccountEndpoints.cs:958-961):
// an Owner manages their own account through the self-service endpoints,
// never through owner user management.
func rejectSelf(caller, target uuid.UUID) error {
	if caller == target {
		return refuse(http.StatusBadRequest, "invalid_request", selfManagementMessage, nil)
	}
	return nil
}

// managedTarget reads the user an owner operation names on q: the bare 404
// when there is none (EA/AuthAccountEndpoints.cs:938-956).
func managedTarget(ctx context.Context, q *store.Queries, id uuid.UUID) (store.IdentityUser, error) {
	u, err := q.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, notFound
	}
	return u, err
}

// emailTaken returns the account that has email, compared normalized, and
// whether there is one.
func emailTaken(ctx context.Context, q *store.Queries, email string) (store.IdentityUser, bool, error) {
	u, err := q.GetUserByNormalizedEmail(ctx, normalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// lockOwners takes the owner lock on q.
func lockOwners(ctx context.Context, q *store.Queries) error {
	return q.AcquireOwnerMutationLock(ctx, ownerMutationLock)
}

// GetIdentityOwnerUsers lists every user for an Owner, the Owners first,
// then by display name and id (EA/AuthAccountEndpoints.cs:401-457).
func (s *server) GetIdentityOwnerUsers(ctx context.Context, _ gen.GetIdentityOwnerUsersRequestObject) (gen.GetIdentityOwnerUsersResponseObject, error) {
	rows, err := s.q.ListManagedUsers(ctx, store.ListManagedUsersParams{OidcEnabled: s.deps.Config.OIDC != nil})
	if err != nil {
		return nil, fmt.Errorf("identity: list users: %w", err)
	}
	now := s.deps.Clock()
	users := make(gen.GetIdentityOwnerUsers200JSONResponse, len(rows))
	for i, u := range rows {
		users[i] = s.ownerUser(u, now)
	}
	return users, nil
}

// ownerUser is a user as owner user management shows one
// (EA/AuthAccountEndpoints.cs:440-456): the managed role, the account state
// at now, whether TOTP is enrolled, the owner avatar URL when an avatar is
// stored (base path prefixed, as accountAvatarPath is), and whether the
// user is linked to the workforce OIDC provider. The email is shown as
// stored, as .NET showed it.
func (s *server) ownerUser(u store.ListManagedUsersRow, now time.Time) gen.OwnerUserResponse {
	role := RoleUser
	if u.IsOwner {
		role = RoleOwner
	}
	var avatarURL *string
	if u.HasAvatar {
		v := s.deps.Config.BasePath + ownerUsersPath + "/" + u.ID.String() + "/avatar"
		avatarURL = &v
	}
	locked := isLockedOut(u.LockoutEnd, now)
	email := u.Email
	return gen.OwnerUserResponse{
		Id:               u.ID,
		DisplayName:      u.DisplayName,
		Email:            &email,
		Role:             role,
		Active:           !u.IsDisabled && !locked,
		Disabled:         u.IsDisabled,
		LockedOut:        locked,
		LockoutEnd:       u.LockoutEnd,
		TwoFactorEnabled: u.TotpEnabled,
		AvatarUrl:        avatarURL,
		SsoEnabled:       u.SsoEnabled,
	}
}

// managedUser reads one user on q as ownerUser shows them: the answer of
// every owner operation that returns the user it changed.
func (s *server) managedUser(ctx context.Context, q *store.Queries, id uuid.UUID, now time.Time) (gen.OwnerUserResponse, error) {
	rows, err := q.ListManagedUsers(ctx, store.ListManagedUsersParams{OidcEnabled: s.deps.Config.OIDC != nil, ID: &id})
	if err != nil {
		return gen.OwnerUserResponse{}, err
	}
	if len(rows) != 1 {
		return gen.OwnerUserResponse{}, fmt.Errorf("identity: user %s is missing from its own transaction", id)
	}
	return s.ownerUser(rows[0], now), nil
}

// userSnapshot is .NET's UserAuthorizationSnapshot, the before and after of
// the user audit events, with the property names its serializer wrote
// (AZ/AuthorizationAuditWriter.cs:130-135).
type userSnapshot struct {
	UserID         uuid.UUID `json:"UserId"`
	Roles          []string  `json:"Roles"`
	PermissionKeys []string  `json:"PermissionKeys"`
	IsDisabled     bool      `json:"IsDisabled"`
	IsDeleted      bool      `json:"IsDeleted"`
}

// captureUser is .NET's CaptureUserAsync on q
// (AZ/AuthorizationAuditWriter.cs:47-73): the user's direct roles, the
// permission keys they carry ("*" for an Owner), and whether the user is
// disabled.
func captureUser(ctx context.Context, q *store.Queries, id uuid.UUID) (userSnapshot, error) {
	row, err := q.GetUserAuthorizationSnapshot(ctx, id)
	if err != nil {
		return userSnapshot{}, err
	}
	snap := userSnapshot{UserID: id, Roles: nonNil(row.RoleNames), PermissionKeys: nonNil(row.PermissionKeys), IsDisabled: row.IsDisabled}
	if slices.Contains(snap.Roles, RoleOwner) {
		snap.PermissionKeys = []string{"*"}
	}
	return snap, nil
}

// rolesAudit is .NET's user.role-changed before/after shape
// (EA/AuthAccountEndpoints.cs:658-660).
type rolesAudit struct {
	Roles []string `json:"Roles"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// PostIdentityOwnerUsers creates a user with a password for an Owner
// (EA/AuthAccountEndpoints.cs:480-555). The request is validated first (400
// invalid_request, per field). Then, in one serializable transaction, under
// the owner lock when the role is Owner: an email some account already has
// is 409 account_exists; the email's pending invitations are revoked; the
// password policy applies (400 identity_validation_failed); the user is
// created with a confirmed email and the role, and user.created-with-role
// is audited. 201 with the user and its Location.
func (s *server) PostIdentityOwnerUsers(ctx context.Context, req gen.PostIdentityOwnerUsersRequestObject) (gen.PostIdentityOwnerUsersResponseObject, error) {
	var body gen.OwnerUserCreateRequest
	if req.Body != nil {
		body = *req.Body
	}
	// A temporary password, when given, is the password (:496-498).
	password := deref(body.Password)
	if !blank(body.TemporaryPassword) {
		password = *body.TemporaryPassword
	}
	fields := validateOwnerUserDetails(body.DisplayName, body.Email, body.Role)
	validateOwnerPassword(fields, password)
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

	email, displayName, role := strings.TrimSpace(*body.Email), strings.TrimSpace(*body.DisplayName), *body.Role
	userID := uuid.New()
	var created gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if role == RoleOwner {
			if err := lockOwners(ctx, q); err != nil {
				return err
			}
		}
		if _, taken, err := emailTaken(ctx, q, email); err != nil || taken {
			return cmpOr(err, error(accountExists))
		}
		now := s.deps.Clock()
		if err := q.RevokeActiveInvitationsForEmail(ctx, store.RevokeActiveInvitationsForEmailParams{NormalizedEmail: normalizeEmail(email), Now: now}); err != nil {
			return err
		}
		if problems := validatePassword(password, s.deps.Config.IsDevelopment()); problems != nil {
			return refuse(http.StatusBadRequest, "identity_validation_failed", userNotCreatedMessage, problems)
		}
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		if err := q.InsertUser(ctx, store.InsertUserParams{
			ID:              userID,
			Email:           email,
			NormalizedEmail: normalizeEmail(email),
			EmailConfirmed:  true,
			DisplayName:     displayName,
			PasswordHash:    &hash,
			Version:         uuid.New(),
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			return err
		}
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: builtInRoleID(role)}); err != nil {
			return err
		}
		after, err := captureUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetUser: &userID,
			action:     "user.created-with-role",
			// .NET's anonymous before has bootstrap's shape (:542-543).
			before: bootstrapAuditState{Roles: []string{}, PermissionKeys: []string{}},
			after:  after,
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		created, err = s.managedUser(ctx, q, userID, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersResponseObject](err)
	}
	return ownerUserCreated{
		location: s.deps.Config.BasePath + ownerUsersPath + "/" + userID.String(),
		body:     gen.PostIdentityOwnerUsers201JSONResponse(created),
	}, nil
}

// cmpOr is err when it is set, else fallback.
func cmpOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// PutIdentityOwnerUsersById edits another user's display name, email and
// managed role for an Owner (EA/AuthAccountEndpoints.cs:557-684). After
// validation (400 invalid_request) and the self check (400), one
// serializable transaction under the owner lock: an unknown user is 404; an
// email another account has is 409 account_exists; demoting the last active
// Owner is 409 last_active_owner. An email change revokes the pending
// invitations of the old and the new email. A role change replaces the
// managed role and audits user.role-changed. Any change to the email,
// display name or role ends every session of the user.
func (s *server) PutIdentityOwnerUsersById(ctx context.Context, req gen.PutIdentityOwnerUsersByIdRequestObject) (gen.PutIdentityOwnerUsersByIdResponseObject, error) {
	var body gen.OwnerUserUpdateRequest
	if req.Body != nil {
		body = *req.Body
	}
	if fields := validateOwnerUserDetails(body.DisplayName, body.Email, body.Role); len(fields) > 0 {
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
		return refusalOr[gen.PutIdentityOwnerUsersByIdResponseObject](err)
	}

	email, displayName, role := strings.TrimSpace(*body.Email), strings.TrimSpace(*body.DisplayName), *body.Role
	var updated gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if other, taken, err := emailTaken(ctx, q, email); err != nil || (taken && other.ID != target.ID) {
			return cmpOr(err, error(accountExists))
		}
		roles, err := q.GetUserRoleNames(ctx, target.ID)
		if err != nil {
			return err
		}
		roles = nonNil(roles)
		now := s.deps.Clock()
		if slices.Contains(roles, RoleOwner) && role == RoleUser {
			last, err := q.IsLastActiveOwner(ctx, store.IsLastActiveOwnerParams{UserID: target.ID, Now: now})
			if err != nil {
				return err
			}
			if last {
				return refuse(http.StatusConflict, "last_active_owner", "The last active Owner cannot be demoted.", nil)
			}
		}

		emailChanged := !strings.EqualFold(target.Email, email)
		detailsChanged := target.DisplayName != displayName || emailChanged
		if emailChanged {
			for _, e := range []string{target.Email, email} {
				if err := q.RevokeActiveInvitationsForEmail(ctx, store.RevokeActiveInvitationsForEmailParams{NormalizedEmail: normalizeEmail(e), Now: now}); err != nil {
					return err
				}
			}
		}
		if err := q.UpdateManagedUser(ctx, store.UpdateManagedUserParams{
			ID:              target.ID,
			DisplayName:     displayName,
			Email:           email,
			NormalizedEmail: normalizeEmail(email),
			Version:         uuid.New(),
			Now:             now,
		}); err != nil {
			return err
		}

		managed := slices.DeleteFunc(slices.Clone(roles), func(name string) bool { return !isManagedRole(name) })
		roleChanged := len(managed) != 1 || managed[0] != role
		if roleChanged {
			if err := s.replaceManagedRole(ctx, q, r, p.UserID, p.MFAVerified, target.ID, roles, managed, role, now); err != nil {
				return err
			}
		}
		if detailsChanged || roleChanged {
			if err := s.access.revokeAllSessions(ctx, q, target.ID); err != nil {
				return err
			}
		}
		updated, err = s.managedUser(ctx, q, target.ID, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PutIdentityOwnerUsersByIdResponseObject](err)
	}
	return gen.PutIdentityOwnerUsersById200JSONResponse(updated), nil
}

// replaceManagedRole gives the user role in place of the managed roles they
// hold and audits user.role-changed with their roles before and after
// (EA/AuthAccountEndpoints.cs:639-661).
func (s *server) replaceManagedRole(ctx context.Context, q *store.Queries, r *http.Request, actor uuid.UUID, mfa bool, userID uuid.UUID, before, managed []string, role string, now time.Time) error {
	if len(managed) > 0 {
		ids := make([]uuid.UUID, len(managed))
		for i, name := range managed {
			ids[i] = builtInRoleID(name)
		}
		if err := q.RemoveUserRoles(ctx, store.RemoveUserRolesParams{UserID: userID, RoleIds: ids}); err != nil {
			return err
		}
	}
	if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: builtInRoleID(role)}); err != nil {
		return err
	}
	after, err := q.GetUserRoleNames(ctx, userID)
	if err != nil {
		return err
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		actor:      &actor,
		targetUser: &userID,
		action:     "user.role-changed",
		before:     rolesAudit{Roles: before},
		after:      rolesAudit{Roles: nonNil(after)},
		mfa:        mfa,
	})
}

// PostIdentityOwnerUsersByIdDisable disables another user for an Owner
// (EA/AuthAccountEndpoints.cs:749-812): the last active Owner is 409
// last_active_owner. Disabling ends every session of the user and audits
// user.disabled; a lockout in force stays as it is. Disabling a disabled
// user changes nothing.
func (s *server) PostIdentityOwnerUsersByIdDisable(ctx context.Context, req gen.PostIdentityOwnerUsersByIdDisableRequestObject) (gen.PostIdentityOwnerUsersByIdDisableResponseObject, error) {
	u, err := s.setUserDisabled(ctx, req.Id, true)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdDisableResponseObject](err)
	}
	return gen.PostIdentityOwnerUsersByIdDisable200JSONResponse(u), nil
}

// PostIdentityOwnerUsersByIdEnable enables another user for an Owner
// (EA/AuthAccountEndpoints.cs:814-871). Enabling ends every session of the
// user, so a session from before the disable does not come back, and
// audits user.enabled. Enabling an enabled user changes nothing.
func (s *server) PostIdentityOwnerUsersByIdEnable(ctx context.Context, req gen.PostIdentityOwnerUsersByIdEnableRequestObject) (gen.PostIdentityOwnerUsersByIdEnableResponseObject, error) {
	u, err := s.setUserDisabled(ctx, req.Id, false)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdEnableResponseObject](err)
	}
	return gen.PostIdentityOwnerUsersByIdEnable200JSONResponse(u), nil
}

// setUserDisabled is disable and enable: after the self check, one
// serializable transaction under the owner lock.
func (s *server) setUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) (gen.OwnerUserResponse, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return gen.OwnerUserResponse{}, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return gen.OwnerUserResponse{}, err
	}
	if err := rejectSelf(p.UserID, id); err != nil {
		return gen.OwnerUserResponse{}, err
	}
	var out gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, id)
		if err != nil {
			return err
		}
		before, err := captureUser(ctx, q, id)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		if disabled {
			last, err := q.IsLastActiveOwner(ctx, store.IsLastActiveOwnerParams{UserID: id, Now: now})
			if err != nil {
				return err
			}
			if last {
				return refuse(http.StatusConflict, "last_active_owner", "The last active Owner cannot be disabled.", nil)
			}
		}
		if target.IsDisabled != disabled {
			if err := q.SetUserDisabled(ctx, store.SetUserDisabledParams{ID: id, IsDisabled: disabled, Version: uuid.New(), Now: now}); err != nil {
				return err
			}
			if err := s.access.revokeAllSessions(ctx, q, id); err != nil {
				return err
			}
			after := before
			after.IsDisabled = disabled
			action := "user.enabled"
			if disabled {
				action = "user.disabled"
			}
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor: &p.UserID, targetUser: &id, action: action, before: before, after: after, mfa: p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		out, err = s.managedUser(ctx, q, id, now)
		return err
	})
	return out, err
}

// DeleteIdentityOwnerUsersById deletes another user for an Owner
// (EA/AuthAccountEndpoints.cs:873-936), in one serializable transaction
// under the owner lock: the last active Owner is 409 last_active_owner, and
// a user SCIM provisioned is 409 provenance_conflict. The deletion takes
// the user's sessions with it and audits user.deleted. 204.
func (s *server) DeleteIdentityOwnerUsersById(ctx context.Context, req gen.DeleteIdentityOwnerUsersByIdRequestObject) (gen.DeleteIdentityOwnerUsersByIdResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.DeleteIdentityOwnerUsersByIdResponseObject](err)
	}
	provenanceConflict := refuse(http.StatusConflict, "provenance_conflict", provenanceConflictMessage, nil)
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		if _, err := managedTarget(ctx, q, req.Id); err != nil {
			return err
		}
		before, err := captureUser(ctx, q, req.Id)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		last, err := q.IsLastActiveOwner(ctx, store.IsLastActiveOwnerParams{UserID: req.Id, Now: now})
		if err != nil {
			return err
		}
		if last {
			return refuse(http.StatusConflict, "last_active_owner", "The last active Owner cannot be deleted.", nil)
		}
		scim, err := q.HasScimMapping(ctx, req.Id)
		if err != nil {
			return err
		}
		if scim {
			return provenanceConflict
		}
		if err := q.DeleteUser(ctx, req.Id); err != nil {
			if isProvenanceViolation(err) {
				return provenanceConflict
			}
			return err
		}
		after := before
		after.Roles, after.PermissionKeys, after.IsDeleted = []string{}, []string{}, true
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: "user.deleted", before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityOwnerUsersByIdResponseObject](err)
	}
	return gen.DeleteIdentityOwnerUsersById204Response{}, nil
}

// isProvenanceViolation reports whether err is the SCIM mapping's ON DELETE
// RESTRICT refusing a deletion: the backstop behind the HasScimMapping
// check (EA/AuthAccountState.cs:64-86).
func isProvenanceViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && strings.Contains(pgErr.ConstraintName, "scim")
}

// PostIdentityOwnerUsersByIdPassword sets another user's password for an
// Owner (EA/AuthAccountEndpoints.cs:711-747): 400 invalid_request for a
// missing or oversized password, the self check, 404 for an unknown user,
// 400 identity_validation_failed for the policy. The new password ends
// every session of the user and spends every password-reset token they
// hold, in one transaction.
func (s *server) PostIdentityOwnerUsersByIdPassword(ctx context.Context, req gen.PostIdentityOwnerUsersByIdPasswordRequestObject) (gen.PostIdentityOwnerUsersByIdPasswordResponseObject, error) {
	var password string
	if req.Body != nil {
		password = deref(req.Body.Password)
	}
	fields := map[string][]string{}
	validateOwnerPassword(fields, password)
	if len(fields) > 0 {
		return refuse(http.StatusBadRequest, "invalid_request", passwordInvalidMessage, fields), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResponseObject](err)
	}
	if _, err := managedTarget(ctx, s.q, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResponseObject](err)
	}
	if problems := validatePassword(password, s.deps.Config.IsDevelopment()); problems != nil {
		return refuse(http.StatusBadRequest, "identity_validation_failed", userPasswordNotChangedMessage, problems), nil
	}
	if err := s.setPassword(ctx, req.Id, password, nil); err != nil {
		return nil, err
	}
	return gen.PostIdentityOwnerUsersByIdPassword200JSONResponse{Success: true}, nil
}

// setPassword stores password as userID's, rotates their version, spends
// every password-reset token they hold and ends every session they have,
// in one transaction: an Owner setting it, or a password reset. A reset
// passes the hash of the token it spends: when a concurrent reset has spent
// it already, the change is refused with invalid_reset_token.
func (s *server) setPassword(ctx context.Context, userID uuid.UUID, password string, resetToken []byte) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if resetToken != nil {
			spent, err := q.ConsumePasswordResetToken(ctx, store.ConsumePasswordResetTokenParams{TokenHash: resetToken, UserID: userID, Now: now})
			if err != nil {
				return err
			}
			if spent == 0 {
				return invalidResetToken
			}
		}
		if err := q.UpdateAccountPassword(ctx, store.UpdateAccountPasswordParams{ID: userID, PasswordHash: &hash, Version: uuid.New(), Now: now}); err != nil {
			return err
		}
		if err := q.DeleteUserPasswordResetTokens(ctx, userID); err != nil {
			return err
		}
		return s.access.revokeAllSessions(ctx, q, userID)
	})
	if err != nil {
		return fmt.Errorf("identity: set password: %w", err)
	}
	return nil
}

// PostIdentityOwnerUsersByIdPasswordReset emails another user a
// password-reset link for an Owner (EA/AuthAccountEndpoints.cs:686-709),
// when they have a password and a confirmed email. It always answers
// {accepted:true} once the user is found; a failed send is swallowed.
func (s *server) PostIdentityOwnerUsersByIdPasswordReset(ctx context.Context, req gen.PostIdentityOwnerUsersByIdPasswordResetRequestObject) (gen.PostIdentityOwnerUsersByIdPasswordResetResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResetResponseObject](err)
	}
	target, err := managedTarget(ctx, s.q, req.Id)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdPasswordResetResponseObject](err)
	}
	if target.PasswordHash != nil && target.EmailConfirmed && strings.TrimSpace(target.Email) != "" {
		if err := s.sendPasswordReset(ctx, target); err != nil {
			return nil, err
		}
	}
	return gen.PostIdentityOwnerUsersByIdPasswordReset200JSONResponse{Accepted: true}, nil
}

// GetIdentityOwnerUsersByIdAvatar serves another user's avatar to an Owner
// (EA/AuthAccountEndpoints.cs:459-478), with the private, unsniffable,
// inline headers of the account's own avatar; 404 when the user or their
// avatar is absent.
func (s *server) GetIdentityOwnerUsersByIdAvatar(ctx context.Context, req gen.GetIdentityOwnerUsersByIdAvatarRequestObject) (gen.GetIdentityOwnerUsersByIdAvatarResponseObject, error) {
	row, err := s.q.GetProfileAvatar(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityOwnerUsersByIdAvatar404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: owner avatar: %w", err)
	}
	var body gen.GetIdentityOwnerUsersByIdAvatarResponseObject
	if row.ContentType == "image/png" {
		body = gen.GetIdentityOwnerUsersByIdAvatar200ImagepngResponse{Body: bytes.NewReader(row.Data), ContentLength: int64(len(row.Data))}
	} else {
		body = gen.GetIdentityOwnerUsersByIdAvatar200ImagejpegResponse{Body: bytes.NewReader(row.Data), ContentLength: int64(len(row.Data))}
	}
	return ownerAvatar{body: body}, nil
}

// ownerAvatar is the owner avatar read's 200: the avatar headers, then the
// generated response.
type ownerAvatar struct {
	body gen.GetIdentityOwnerUsersByIdAvatarResponseObject
}

func (r ownerAvatar) VisitGetIdentityOwnerUsersByIdAvatarResponse(w http.ResponseWriter) error {
	setAvatarHeaders(w)
	return r.body.VisitGetIdentityOwnerUsersByIdAvatarResponse(w)
}

// validateOwnerUserDetails is .NET's ValidateOwnerUserDetails
// (EA/AuthAccountEndpoints.cs:1013-1040), the checks create and update
// share. It returns the problems, an empty map when there are none.
func validateOwnerUserDetails(displayName, email, role *string) map[string][]string {
	fields := map[string][]string{}
	if blank(displayName) || utf16Length(strings.TrimSpace(*displayName)) > maxDisplayNameLength {
		fields["displayName"] = []string{"Display name is required and must be at most 200 characters."}
	}
	if blank(email) || !managedEmailValid(*email) {
		fields["email"] = []string{"A valid email is required."}
	}
	if !isManagedRole(deref(role)) {
		fields["role"] = []string{"Role must be User or Owner."}
	}
	return fields
}

// managedEmailValid is .NET's IsValidEmail (EA/AuthAccountEndpoints.cs:1034-1040):
// at most 256 characters, none of them whitespace or a control character
// (so the address as sent, untrimmed), and exactly one '@', neither first
// nor last.
func managedEmailValid(email string) bool {
	if utf16Length(email) > maxManagedEmailLength || strings.ContainsFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	at := strings.IndexByte(email, '@')
	return at > 0 && at == strings.LastIndexByte(email, '@') && at < len(email)-1
}

// isManagedRole reports whether role is one an Owner assigns: User or
// Owner, exactly (EA/AuthAccountEndpoints.cs:963-965).
func isManagedRole(role string) bool {
	return role == RoleUser || role == RoleOwner
}

// validateOwnerPassword adds the problem with a password an Owner sets to
// fields: missing, or over 256 characters (EA/AuthAccountEndpoints.cs:977-1011).
func validateOwnerPassword(fields map[string][]string, password string) {
	switch {
	case strings.TrimSpace(password) == "":
		fields["password"] = []string{"Password is required."}
	case utf16Length(password) > maxNewPasswordLength:
		fields["password"] = []string{"Password must be 256 characters or fewer."}
	}
}

// ownerUserCreated is the user creation's 201: its Location, then the
// generated body.
type ownerUserCreated struct {
	location string
	body     gen.PostIdentityOwnerUsers201JSONResponse
}

func (r ownerUserCreated) VisitPostIdentityOwnerUsersResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", r.location)
	return r.body.VisitPostIdentityOwnerUsersResponse(w)
}

// The refusals owner user management answers with.

func (r refusal) VisitPostIdentityOwnerUsersResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitPutIdentityOwnerUsersByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityOwnerUsersByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdDisableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdEnableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdPasswordResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdPasswordResetResponse(w http.ResponseWriter) error {
	return r.write(w)
}
