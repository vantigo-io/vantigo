package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// ownerMutationLock is the key of the transaction advisory lock that
// serialises every change to who holds Owner: bootstrap, Owner creation,
// Owner invitation acceptance and Owner demotion (EA/AuthAccountState.cs:9,
// :20-25). 0x56414e54 is "VANT".
const ownerMutationLock = 0x56414e54

// maxDisplayNameLength is identity.users.display_name's CHECK bound, .NET's
// HasMaxLength(200).
const maxDisplayNameLength = 200

// sessionLocation is GET /session's path, the Location of a 201 that
// signs its caller in.
const sessionLocation = "/api/v1/identity/session"

// errBootstrapRefused rolls the bootstrap transaction back when createOwner
// refuses it with a client answer.
var errBootstrapRefused = errors.New("identity: bootstrap refused")

// bootstrapRoles are the roles the first-run bootstrap grants its Owner:
// Owner, the permission wildcard for every module, and SystemAdmin, the
// control plane (/admin, maintenance mode). The first account to complete
// setup is the installation's full administrator, there being no other way
// to grant SystemAdmin (GET /access/roles hides it). The order is the 201's
// literal order, not orderRoles (EA/AuthEndpoints.cs:257-258).
var bootstrapRoles = []string{RoleOwner, RoleSystemAdmin}

// GetIdentityBootstrapStatus reports whether bootstrap is still available:
// no Owner exists and the marker is not written. It is a hint for the UI
// only; POST /bootstrap decides.
func (s *server) GetIdentityBootstrapStatus(ctx context.Context, _ gen.GetIdentityBootstrapStatusRequestObject) (gen.GetIdentityBootstrapStatusResponseObject, error) {
	consumed, err := s.q.BootstrapConsumed(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: bootstrap status: %w", err)
	}
	return gen.GetIdentityBootstrapStatus200JSONResponse{Available: !consumed}, nil
}

// PostIdentityBootstrap creates the installation's first Owner and signs
// them in (EA/AuthEndpoints.cs:155-268). It is anonymous and needs no
// secret: whoever completes setup first on a fresh installation is its
// administrator, and the consumed check below makes that a one-time event.
// In order: the request is validated (400 invalid_request with per-field
// errors); then one serializable transaction, under the owner lock, refuses
// a consumed bootstrap (409 bootstrap_unavailable), applies the password
// policy and the account checks ASP.NET Identity's CreateAsync made (400
// identity_validation_failed), creates the user with bootstrapRoles, and
// records the marker and the audit event. Only after commit does it start a
// session: 201 with Location /session.
//
// A concurrent bootstrap that wins makes this one fail on a serialization
// failure, a deadlock or a unique violation; each is the same 409. It is
// tried once: a retry would only find bootstrap consumed.
func (s *server) PostIdentityBootstrap(ctx context.Context, req gen.PostIdentityBootstrapRequestObject) (gen.PostIdentityBootstrapResponseObject, error) {
	var body gen.BootstrapRequest
	if req.Body != nil {
		body = *req.Body
	}
	if fields := validateBootstrapRequest(body); fields != nil {
		return gen.PostIdentityBootstrap400JSONResponse(authErrorBody("invalid_request", "The bootstrap request is invalid.", fields)), nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}

	email, displayName := strings.TrimSpace(*body.Email), strings.TrimSpace(*body.DisplayName)
	roles := bootstrapRoles
	userID := uuid.New()

	var refused gen.PostIdentityBootstrapResponseObject
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		var err error
		refused, err = s.createOwner(ctx, store.New(tx), r, userID, email, displayName, *body.Password, roles)
		if err == nil && refused != nil {
			return errBootstrapRefused
		}
		return err
	})
	switch {
	case refused != nil:
		return refused, nil
	case isBootstrapRace(err):
		return bootstrapUnavailable(), nil
	case err != nil:
		return nil, fmt.Errorf("identity: bootstrap: %w", err)
	}

	token, err := s.access.createSession(ctx, s.deps.Pool, userID, false, false, r)
	if err != nil {
		return nil, err
	}
	return bootstrapCreated{
		cookies:  cookies{s.access.newSessionCookie(token, false)},
		location: s.deps.Config.BasePath + sessionLocation,
		body:     gen.PostIdentityBootstrap201JSONResponse{User: authUser(userID, displayName, email, roles)},
	}, nil
}

// createOwner is bootstrap's transaction on q. It returns a client answer
// when it refuses, and an error when the database fails.
func (s *server) createOwner(ctx context.Context, q *store.Queries, r *http.Request, userID uuid.UUID, email, displayName, password string, roles []string) (gen.PostIdentityBootstrapResponseObject, error) {
	if err := q.AcquireOwnerMutationLock(ctx, ownerMutationLock); err != nil {
		return nil, err
	}
	consumed, err := q.BootstrapConsumed(ctx)
	if err != nil {
		return nil, err
	}
	if consumed {
		return bootstrapUnavailable(), nil
	}

	// UserManager.CreateAsync checked the password before the account
	// (EA/AuthEndpoints.cs:202-206).
	if problems := validatePassword(password, s.deps.Config.IsDevelopment()); problems != nil {
		return ownerNotCreated(problems), nil
	}
	if !emailAddressValid(email) {
		return ownerNotCreated(map[string][]string{"InvalidEmail": {fmt.Sprintf("Email '%s' is invalid.", email)}}), nil
	}
	switch _, err := q.GetUserByNormalizedEmail(ctx, normalizeEmail(email)); {
	case err == nil:
		// An account can exist without an Owner, e.g. one OIDC provisioned.
		return ownerNotCreated(map[string][]string{
			"DuplicateUserName": {fmt.Sprintf("Username '%s' is already taken.", email)},
			"DuplicateEmail":    {fmt.Sprintf("Email '%s' is already taken.", email)},
		}), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	if err := q.InsertUser(ctx, store.InsertUserParams{
		ID:              userID,
		Email:           email,
		NormalizedEmail: normalizeEmail(email),
		EmailConfirmed:  false, // as .NET: the bootstrap Owner's email is never confirmed
		DisplayName:     displayName,
		PasswordHash:    &hash,
		Version:         uuid.New(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		return nil, err
	}
	for _, role := range roles {
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: builtInRoleID(role)}); err != nil {
			return nil, err
		}
	}
	if err := q.InsertBootstrapState(ctx, now); err != nil {
		return nil, err
	}
	ownerRole := RoleOwnerID
	return nil, writeAudit(ctx, q, r, now, auditEvent{
		targetUser: &userID,
		targetRole: &ownerRole,
		action:     "bootstrap.owner-created",
		before:     bootstrapAuditState{Roles: []string{}, PermissionKeys: []string{}},
		after:      bootstrapAuditState{UserID: &userID, Roles: roles, PermissionKeys: []string{"*"}},
	})
}

// bootstrapAuditState is .NET's bootstrap.owner-created before/after shape,
// with the property names its serializer wrote (EA/AuthEndpoints.cs:234-247).
type bootstrapAuditState struct {
	UserID         *uuid.UUID `json:"UserId"`
	Roles          []string   `json:"Roles"`
	PermissionKeys []string   `json:"PermissionKeys"`
}

// validateBootstrapRequest is .NET's ValidateBootstrapRequest
// (EA/AuthEndpoints.cs:506-530), plus the display-name bound .NET left to a
// database error. It returns nil when the request is complete.
func validateBootstrapRequest(b gen.BootstrapRequest) map[string][]string {
	fields := map[string][]string{}
	if blank(b.Email) || !strings.Contains(*b.Email, "@") {
		fields["email"] = []string{"A valid email is required."}
	}
	switch {
	case blank(b.DisplayName):
		fields["displayName"] = []string{"Display name is required."}
	case utf16Length(strings.TrimSpace(*b.DisplayName)) > maxDisplayNameLength:
		fields["displayName"] = []string{fmt.Sprintf("Display name must be at most %d characters.", maxDisplayNameLength)}
	}
	if blank(b.Password) {
		fields["password"] = []string{"Password is required."}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func blank(s *string) bool {
	return s == nil || strings.TrimSpace(*s) == ""
}

// emailAddressValid is .NET's EmailAddressAttribute, which ASP.NET
// Identity's UserValidator applied: exactly one '@', neither first nor
// last, and no line break.
func emailAddressValid(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at == strings.LastIndexByte(email, '@') && at < len(email)-1 && !strings.ContainsAny(email, "\r\n")
}

// isBootstrapRace reports whether err is how a concurrent bootstrap makes
// this one lose: a serialization failure or deadlock, or a unique violation
// on the marker, the email or the role assignment (EA/AuthEndpoints.cs:548-565).
// Any other failure is a server error, not a conflict.
func isBootstrapRace(err error) bool {
	return db.IsSerializationConflict(err) ||
		db.IsUniqueViolation(err, "bootstrap_state_pkey") ||
		db.IsUniqueViolation(err, "ux_users_normalized_email") ||
		db.IsUniqueViolation(err, "user_roles_pkey")
}

func bootstrapUnavailable() gen.PostIdentityBootstrap409JSONResponse {
	return gen.PostIdentityBootstrap409JSONResponse(authErrorBody("bootstrap_unavailable", "The local Owner has already been created.", nil))
}

func ownerNotCreated(fields map[string][]string) gen.PostIdentityBootstrap400JSONResponse {
	return gen.PostIdentityBootstrap400JSONResponse(authErrorBody("identity_validation_failed", "The Owner account could not be created.", fields))
}

// bootstrapCreated is bootstrap's 201: the new session's cookie and the
// Location of GET /session, then the generated body.
type bootstrapCreated struct {
	cookies
	location string
	body     gen.PostIdentityBootstrap201JSONResponse
}

func (r bootstrapCreated) VisitPostIdentityBootstrapResponse(w http.ResponseWriter) error {
	r.set(w)
	w.Header().Set("Location", r.location)
	return r.body.VisitPostIdentityBootstrapResponse(w)
}

// builtInRoles are the protected roles the baseline migration inserts with
// fixed ids (CT/Identity/AuthRoles.cs:5-7).
var builtInRoles = []struct {
	id   uuid.UUID
	name string
}{
	{RoleSystemAdminID, RoleSystemAdmin},
	{RoleOwnerID, RoleOwner},
	{RoleUserID, RoleUser},
}

func builtInRoleID(name string) uuid.UUID {
	for _, r := range builtInRoles {
		if r.name == name {
			return r.id
		}
	}
	panic("identity: not a built-in role: " + name) // callers pass the role constants
}

// RunStartup is identity's work after migrations and before the server
// listens; its error must stop startup. The built-in roles must be the
// protected rows the baseline inserted: a role with a built-in name that is
// not (it lost is_system or is_built_in, or another role took the name)
// fails closed, as .NET's EnsureBuiltInRolesAsync did.
//
// Then, while bootstrap is still available, it logs a warning: with no
// secret guarding /setup, the first visitor to complete it becomes the
// installation's administrator, and the operator should be the one to do
// so before anyone else can reach the address.
func RunStartup(ctx context.Context, d module.Deps) error {
	q := store.New(d.Pool)
	if err := verifyBuiltInRoles(ctx, q); err != nil {
		return err
	}
	consumed, err := q.BootstrapConsumed(ctx)
	if err != nil {
		return fmt.Errorf("identity: bootstrap status: %w", err)
	}
	if !consumed {
		d.Logger.WarnContext(ctx, bootstrapOpenNotice)
	}
	return nil
}

// bootstrapOpenNotice is RunStartup's warning while no Owner exists.
const bootstrapOpenNotice = "this installation has no Owner yet: the first visitor to complete /setup becomes its Owner and SystemAdmin; complete setup before exposing it"

// verifyBuiltInRoles fails unless each built-in role's name belongs to its
// fixed-id row and that row is marked system and built-in
// (Identity.Module/Authorization/AuthorizationCatalogExtensions.cs:130-160).
func verifyBuiltInRoles(ctx context.Context, q *store.Queries) error {
	names := make([]string, len(builtInRoles))
	for i, r := range builtInRoles {
		names[i] = strings.ToUpper(r.name)
	}
	rows, err := q.RolesByNormalizedName(ctx, names)
	if err != nil {
		return fmt.Errorf("identity: read built-in roles: %w", err)
	}
	for _, want := range builtInRoles {
		i := -1
		for j, row := range rows {
			if row.NormalizedName == strings.ToUpper(want.name) {
				i = j
			}
		}
		if i < 0 {
			return fmt.Errorf("identity: built-in role %q is missing", want.name)
		}
		if got := rows[i]; got.ID != want.id || got.Name != want.name || !got.IsSystem || !got.IsBuiltIn {
			return fmt.Errorf("identity: role %q already exists with conflicting metadata and cannot be adopted as a protected role", want.name)
		}
	}
	return nil
}
