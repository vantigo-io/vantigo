package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
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

// bootstrapSecretBytes is the entropy of a generated development secret.
const bootstrapSecretBytes = 32

// maxDisplayNameLength is identity.users.display_name's CHECK bound, .NET's
// HasMaxLength(200).
const maxDisplayNameLength = 200

// systemAdminAttempts is how often RunStartup tries the SystemAdmin grant
// when it loses a serialization race (SV/SystemAdminBootstrapper.cs:22).
const systemAdminAttempts = 4

// sessionLocation is GET /session's path, the Location of a 201 that
// signs its caller in.
const sessionLocation = "/api/v1/identity/session"

// errBootstrapRefused rolls the bootstrap transaction back when createOwner
// refuses it with a client answer.
var errBootstrapRefused = errors.New("identity: bootstrap refused")

// resolveBootstrapSecret is the secret POST /bootstrap accepts, as .NET's
// BootstrapSecretProvider resolved it (SV/BootstrapSecretProvider.cs:19-48).
// A configured BOOTSTRAP_SECRET is used verbatim and never logged. In
// development without one, it is 32 random bytes as base64url, logged once
// at WARN for the operator and held only in memory; it stays valid until
// bootstrap completes or the process restarts. Outside development
// config.Load has already refused a missing secret; "", which nothing
// matches, keeps that failing closed regardless.
func resolveBootstrapSecret(cfg *config.Config, logger *slog.Logger) string {
	if strings.TrimSpace(cfg.BootstrapSecret) != "" {
		return cfg.BootstrapSecret
	}
	if !cfg.IsDevelopment() {
		return ""
	}
	b := make([]byte, bootstrapSecretBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	secret := base64.RawURLEncoding.EncodeToString(b)
	// The one secret identity ever logs, and only in development, as .NET did.
	logger.Warn("bootstrap secret generated",
		"bootstrap_secret", secret,
		"note", "valid only until setup completes or this process restarts; treat it as a secret")
	return secret
}

// GetIdentityBootstrapStatus reports whether bootstrap is still available:
// a secret exists and bootstrap is not consumed (EA/AuthEndpoints.cs:91-104).
// It is a hint for the UI only; POST /bootstrap decides.
func (s *server) GetIdentityBootstrapStatus(ctx context.Context, _ gen.GetIdentityBootstrapStatusRequestObject) (gen.GetIdentityBootstrapStatusResponseObject, error) {
	if s.bootstrapSecret == "" {
		return gen.GetIdentityBootstrapStatus200JSONResponse{Available: false}, nil
	}
	consumed, err := s.q.BootstrapConsumed(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: bootstrap status: %w", err)
	}
	return gen.GetIdentityBootstrapStatus200JSONResponse{Available: !consumed}, nil
}

// PostIdentityBootstrap creates the installation's first Owner and signs
// them in (EA/AuthEndpoints.cs:155-268). In order: the request is validated
// (400 invalid_request with per-field errors); the secret is compared in
// constant time (401 invalid_secret); then one serializable transaction,
// under the owner lock, refuses a consumed bootstrap (409
// bootstrap_unavailable), applies the password policy and the account
// checks ASP.NET Identity's CreateAsync made (400
// identity_validation_failed), creates the user with Owner (and
// SystemAdmin when the email is SYSTEM_ADMIN_EMAIL, compared
// case-insensitively), and records the marker and the audit event. Only
// after commit does it start a session: 201 with Location /session.
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
	if !secretMatches(s.bootstrapSecret, *body.Secret) {
		return gen.PostIdentityBootstrap401JSONResponse(authErrorBody("invalid_secret", "The bootstrap secret is invalid.", nil)), nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}

	email, displayName := strings.TrimSpace(*body.Email), strings.TrimSpace(*body.DisplayName)
	// .NET's literal order for this response, not orderRoles
	// (EA/AuthEndpoints.cs:257-258).
	roles := []string{RoleOwner}
	if strings.EqualFold(strings.TrimSpace(s.deps.Config.SystemAdminEmail), email) {
		roles = append(roles, RoleSystemAdmin)
	}
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
	if blank(b.Secret) {
		fields["secret"] = []string{"Secret is required."}
	}
	if blank(b.Email) || !strings.Contains(*b.Email, "@") {
		fields["email"] = []string{"A valid email is required."}
	}
	switch {
	case blank(b.DisplayName):
		fields["displayName"] = []string{"Display name is required."}
	case utf8.RuneCountInString(strings.TrimSpace(*b.DisplayName)) > maxDisplayNameLength:
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

// secretMatches compares a supplied bootstrap secret with the configured
// one as .NET did (EA/AuthEndpoints.cs:532-538): the exact bytes, in
// constant time for a given length. An empty configured secret matches
// nothing.
func secretMatches(configured, supplied string) bool {
	return configured != "" && subtle.ConstantTimeCompare([]byte(configured), []byte(supplied)) == 1
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
// listens; its error must stop startup. It reproduces .NET's startup
// SystemAdminBootstrapper (SV/SystemAdminBootstrapper.cs:28-107):
//
//   - The built-in roles must be the protected rows the baseline inserted.
//     A role with a built-in name that is not (it lost is_system or
//     is_built_in, or another role took the name) fails closed, whatever
//     SYSTEM_ADMIN_EMAIL says, as .NET's EnsureBuiltInRolesAsync did.
//   - SYSTEM_ADMIN_EMAIL unset: nothing more to do.
//   - The account with that email (matched case-insensitively) is granted
//     SystemAdmin if it lacks it; the grant rotates its version, revokes
//     its sessions and spends its reset links, as .NET rotated the security
//     stamp. Run again, it changes nothing.
//   - No such account on an installation already in use (any user, or the
//     bootstrap marker): an error, because the configured break-glass
//     administrator is missing.
//   - No such account on a fresh installation: nil, since bootstrap may
//     create it.
//
// The grant is a serializable transaction, tried up to four times.
//
// The bootstrap secret is resolved when the module mounts, not here:
// RunStartup has no way to hand a generated secret to the handlers, and an
// installation composed without it must still serve /bootstrap.
func RunStartup(ctx context.Context, d module.Deps) error {
	if err := verifyBuiltInRoles(ctx, store.New(d.Pool)); err != nil {
		return err
	}
	email := strings.TrimSpace(d.Config.SystemAdminEmail)
	if email == "" {
		return nil
	}
	return db.RetrySerializable(ctx, systemAdminAttempts, func() error {
		return db.WithTx(ctx, d.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
			return grantSystemAdmin(ctx, d, store.New(tx), email)
		})
	})
}

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

// errSystemAdminUnverifiedAccount is RunStartup's refusal to make an
// unverified, passwordless account SystemAdmin (see grantSystemAdmin).
var errSystemAdminUnverifiedAccount = errors.New("identity: SYSTEM_ADMIN_EMAIL matches an account whose email is unconfirmed " +
	"and which has no local password, such as one provisioned from an unverified OIDC email claim; SystemAdmin is not granted to it")

// grantSystemAdmin is RunStartup's transaction on q for the configured
// email.
func grantSystemAdmin(ctx context.Context, d module.Deps, q *store.Queries, email string) error {
	user, err := q.GetUserByNormalizedEmail(ctx, normalizeEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		inUse, err := q.InstallationInUse(ctx)
		if err != nil {
			return fmt.Errorf("identity: SystemAdmin grant: %w", err)
		}
		if inUse {
			return fmt.Errorf("identity: SYSTEM_ADMIN_EMAIL %q does not match an existing user: "+
				"the configured break-glass administrator is missing from an already bootstrapped installation", email)
		}
		d.Logger.InfoContext(ctx, "SYSTEM_ADMIN_EMAIL matches no account yet; the first-run bootstrap may create it", "email", email)
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: SystemAdmin grant: %w", err)
	}
	// Deliberate hardening beyond .NET, whose bootstrapper matched the email
	// alone (SV/SystemAdminBootstrapper.cs:50). An account with an
	// unconfirmed email and no local password is, in practice, one workforce
	// OIDC provisioned from an email claim nobody verified (Entra's email
	// claim is unverified), so anyone in the tenant who asserted
	// SYSTEM_ADMIN_EMAIL would become SystemAdmin at the next start. Startup
	// fails closed instead, granting nothing, as for a conflicting built-in
	// role; the error names the reason, never the address. The bootstrap
	// Owner (unconfirmed, but with a local password) and any confirmed
	// account are granted as before.
	if !user.EmailConfirmed && user.PasswordHash == nil {
		return errSystemAdminUnverifiedAccount
	}

	granted, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: user.ID, RoleID: RoleSystemAdminID})
	if err != nil {
		return fmt.Errorf("identity: SystemAdmin grant: %w", err)
	}
	if granted == 0 {
		return nil
	}
	now := d.Clock()
	if err := q.RotateUserVersion(ctx, store.RotateUserVersionParams{ID: user.ID, Version: uuid.New(), Now: now}); err != nil {
		return fmt.Errorf("identity: SystemAdmin grant: %w", err)
	}
	// The security stamp rotation (SV/SystemAdminBootstrapper.cs:69-82):
	// every session ends and every reset link the account holds dies.
	if err := NewAccess(d).rotateSecurityStamp(ctx, q, user.ID, uuid.Nil); err != nil {
		return fmt.Errorf("identity: SystemAdmin grant: %w", err)
	}
	d.Logger.InfoContext(ctx, "granted SystemAdmin to the configured account and revoked its sessions and reset links", "user_id", user.ID)
	return nil
}
