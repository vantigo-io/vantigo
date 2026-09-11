package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// maxActivityWriteInterval caps how long a session may go without its idle
// window sliding: last_seen_at is rewritten once it is min(idle/4, 5 min)
// old, not on every request (SV/SessionValidationService.cs:50, :204-208).
const maxActivityWriteInterval = 5 * time.Minute

// maxUserAgentLength is identity.sessions.user_agent's CHECK bound.
const maxUserAgentLength = 512

// Access is identity's contracts.Access. On every request it resolves the
// vantigo.session cookie to a row in identity.sessions and evaluates the
// operation's x-vantigo-access rule against that row and the user's current
// roles, so a revocation, a disable or a role change applies to the very
// next request.
type Access struct {
	cfg *config.Config
	q   *store.Queries
	now func() time.Time
	// catalog is the composed permission catalog, which decides whether a
	// delegation's keys may be delegated. Compose builds it before any
	// module mounts, and identity's mount sets it here.
	catalog map[string]contracts.Permission
	// oidcHTTPClient is what workforce OIDC's discovery, key and token
	// requests go through; nil means a client with oidcHTTPTimeout. Only a
	// test sets it (SetOIDCHTTPClient), before the module mounts, to reach a
	// fake provider.
	oidcHTTPClient *http.Client
}

var _ contracts.Access = (*Access)(nil)

// NewAccess returns identity's Access over d's configuration, pool and
// clock. It is built from Deps and then put into them:
//
//	deps := module.Deps{...}
//	a := identity.NewAccess(deps)
//	deps.Access = a
//	handler, err := module.Compose(deps, identity.Module(a))
func NewAccess(d module.Deps) *Access {
	return &Access{cfg: d.Config, q: store.New(d.Pool), now: d.Clock}
}

// Check evaluates rule for r. The scim rule admits a request that presents
// the SCIM bearer token (scimBearer, scimTokenValid), with a principal that
// names no user, and answers ErrUnauthenticated otherwise. Every other rule
// first resolves the session; anonymous admits a request without one and
// returns the caller when there is one. Any other rule answers
// ErrUnauthenticated without a live session and ErrForbidden when the
// session's user fails a requirement.
//
// A compound rule requires every name it lists, for policies and permissions
// alike. The contract's "+" stands for .NET's stacked RequireAuthorization
// calls: the /owner group's Owner policy plus each endpoint's
// OwnerManagement (EA/AuthAccountEndpoints.cs:22-27), or an endpoint's
// several RequirePermission calls
// (apps/products/backend/Products.Module/Endpoints/ProductsEndpoints.cs:16-21).
// ASP.NET Core authorizes such an endpoint only when every one of its
// policies succeeds.
func (a *Access) Check(r *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	if rule.Kind == contracts.RuleSCIM {
		if token, ok := scimBearer(r); ok && a.scimTokenValid(token, a.now()) {
			return contracts.Principal{SCIM: true}, nil
		}
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}

	ctx := r.Context()
	now := a.now()
	s, ok, err := a.authenticate(ctx, r, now)
	if err != nil {
		return contracts.Principal{}, err
	}
	if !ok {
		if rule.Kind == contracts.RuleAnonymous {
			return contracts.Principal{}, nil
		}
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	p := contracts.Principal{
		UserID:      s.UserID,
		SessionID:   s.ID,
		Roles:       orderRoles(s.RoleNames),
		MFAVerified: s.MfaVerifiedAt != nil,
	}

	switch rule.Kind {
	case contracts.RuleAnonymous, contracts.RuleSession:
		return p, nil
	case contracts.RulePolicy:
		if len(rule.Names) == 0 {
			return contracts.Principal{}, contracts.ErrForbidden // ParseRule admits no empty rule; fail closed regardless
		}
		for _, name := range rule.Names {
			ok, err := a.policySatisfied(ctx, name, s, now)
			if err != nil {
				return contracts.Principal{}, err
			}
			if !ok {
				return contracts.Principal{}, contracts.ErrForbidden
			}
		}
		return p, nil
	case contracts.RulePermission:
		if len(rule.Names) == 0 {
			return contracts.Principal{}, contracts.ErrForbidden // as for policies: an empty rule grants nothing
		}
		allowed, err := a.permitted(ctx, s, rule.Names, now)
		if err != nil {
			return contracts.Principal{}, err
		}
		if !allowed {
			return contracts.Principal{}, contracts.ErrForbidden
		}
		return p, nil
	default:
		return contracts.Principal{}, contracts.ErrForbidden
	}
}

// Reject answers a failed Check: 403 forbidden when the caller is signed in
// but a requirement failed, else 401 unauthenticated, both as
// AuthErrorResponse, as .NET's cookie events answer every module
// (EA/AuthServiceCollectionExtensions.cs:114-125). A 401 to a request that
// presented a session cookie also clears the cookie, as .NET signs a
// rejected cookie out (SV/SessionValidationService.cs:194-199). A scim rule
// answers with the SCIM 401 body.
func (a *Access) Reject(w http.ResponseWriter, r *http.Request, rule contracts.Rule, err error) {
	if rule.Kind == contracts.RuleSCIM {
		scimUnauthorized(w)
		return
	}
	if errors.Is(err, contracts.ErrForbidden) {
		authError(w, http.StatusForbidden, "forbidden", forbiddenMessage, nil)
		return
	}
	if _, ok := sessionTokenFrom(r); ok {
		a.clearSessionCookie(w)
	}
	authError(w, http.StatusUnauthorized, "unauthenticated", unauthenticatedMessage, nil)
}

// authenticate resolves r's session cookie at now. It reports false, with no
// error, when there is no cookie or its session is not (or no longer) live;
// GetSessionByTokenHash lists everything that ends a session. A live
// session's idle window slides once the session has been idle for
// min(idle/4, 5 min), with idle the bound that applies to the user right now
// (SV/SessionValidationService.cs:107-111).
func (a *Access) authenticate(ctx context.Context, r *http.Request, now time.Time) (store.GetSessionByTokenHashRow, bool, error) {
	var none store.GetSessionByTokenHashRow
	raw, ok := sessionTokenFrom(r)
	if !ok {
		return none, false, nil
	}
	hash, ok := parseToken(raw)
	if !ok {
		return none, false, nil
	}

	bounds := a.cfg.Sessions
	s, err := a.q.GetSessionByTokenHash(ctx, store.GetSessionByTokenHashParams{
		TokenHash:          hash,
		ScimEnabled:        a.cfg.SCIM != nil,
		Now:                now,
		Idle:               interval(bounds.Idle),
		PrivilegedIdle:     interval(bounds.PrivilegedIdle),
		Absolute:           interval(bounds.Absolute),
		PrivilegedAbsolute: interval(bounds.PrivilegedAbsolute),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}
	if err != nil {
		return none, false, fmt.Errorf("identity: resolve session: %w", err)
	}

	idle := bounds.Idle
	if s.Privileged {
		idle = bounds.PrivilegedIdle
	}
	if now.Sub(s.LastSeenAt) >= min(idle/4, maxActivityWriteInterval) {
		if err := a.q.TouchSession(ctx, store.TouchSessionParams{ID: s.ID, Now: now}); err != nil {
			return none, false, fmt.Errorf("identity: slide session: %w", err)
		}
	}
	return s, true, nil
}

func interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// policySatisfied evaluates one name of a policy: rule for the live session
// s, with the requirements each .NET policy carries
// (EA/AuthServiceCollectionExtensions.cs:281-304). Roles are the user's
// current ones, read with the session, where .NET read them from a cookie
// principal refreshed every 15 minutes.
func (a *Access) policySatisfied(ctx context.Context, name string, s store.GetSessionByTokenHashRow, now time.Time) (bool, error) {
	// ActiveAccountRequirement checks IsDisabled only, not lockout or SCIM
	// state (EA/AuthAuthorization.cs:44-45). Session validation already
	// refused a disabled user; it is spelled out so each policy reads as
	// its .NET definition.
	active := !s.IsDisabled
	switch name {
	case "ActiveAccount":
		return active, nil
	case "Owner", "OwnerManagement":
		// Two names, one definition: role Owner + ActiveAccount + MFA (:294-298).
		return hasRole(s, RoleOwner) && active && a.mfaSatisfied(s), nil
	case "SystemAdmin":
		return hasRole(s, RoleSystemAdmin) && active && a.mfaSatisfied(s), nil
	case "AuthorizationManagement":
		// ActiveAccount + MFA + AuthorizationManagementRequirement (:301-303).
		// The MFA requirement binds a delegate as it binds an Owner.
		if !active || !a.mfaSatisfied(s) {
			return false, nil
		}
		return a.managesAuthorization(ctx, s, now)
	case "Business":
		// ActiveAccount + BusinessAccessRequirement (:299-300).
		return active && a.businessSatisfied(s), nil
	default:
		return false, nil // ParseRule admits no other name; fail closed regardless
	}
}

// managesAuthorization is AuthorizationManagementHandler
// (EA/AuthAuthorization.cs:104-174), evaluated afresh on every request with
// no cache: the user holds Owner (:115-125), or holds a delegation active at
// now that is valid (:127-173), one whose stewarded roles all exist and are
// custom and whose keys the catalog all lets be delegated (loadScopes). A
// revoked, expired or malformed delegation is refused at the very next
// request.
func (a *Access) managesAuthorization(ctx context.Context, s store.GetSessionByTokenHashRow, now time.Time) (bool, error) {
	if hasRole(s, RoleOwner) {
		return true, nil
	}
	scopes, err := activeScopes(ctx, a.q, a.catalog, s.UserID, now)
	if err != nil {
		return false, err
	}
	return len(scopes) > 0, nil
}

// mfaSatisfied is MfaAuthenticatedRequirement (EA/AuthAuthorization.cs:80-97):
// met while owners are not required to use MFA, or when this session's
// sign-in verified a second factor. It binds every caller of a policy that
// carries it (SystemAdmins and delegates too), not only Owners.
func (a *Access) mfaSatisfied(s store.GetSessionByTokenHashRow) bool {
	return !a.cfg.OwnersRequireMFA || s.MfaVerifiedAt != nil
}

// businessSatisfied is BusinessAccessRequirement
// (EA/AuthAuthorization.cs:52-78): only an Owner is ever held to MFA here,
// and only while owners are required to use it.
func (a *Access) businessSatisfied(s store.GetSessionByTokenHashRow) bool {
	return !hasRole(s, RoleOwner) || a.mfaSatisfied(s)
}

// permitted evaluates a permission: rule's keys for the live session s.
// Every .NET permission policy is PermissionRequirement + ActiveAccount +
// Business (AZ/PermissionAuthorization.cs:36). The handler denies a
// disabled or locked-out user (:56-61), then allows an Owner every key
// without consulting the permission table (:70-74); anyone else needs each
// key through an effective role (EffectivePermissionForUser). A key missing
// from the catalog (:48-49) never gets here: the router refuses to mount it.
func (a *Access) permitted(ctx context.Context, s store.GetSessionByTokenHashRow, keys []string, now time.Time) (bool, error) {
	if !isActive(s.IsDisabled, s.LockoutEnd, now) || !a.businessSatisfied(s) {
		return false, nil
	}
	if hasRole(s, RoleOwner) {
		return true, nil
	}
	for _, key := range keys {
		ok, err := a.q.EffectivePermissionForUser(ctx, store.EffectivePermissionForUserParams{
			UserID:        s.UserID,
			PermissionKey: key,
			Now:           now,
		})
		if err != nil {
			return false, fmt.Errorf("identity: check permission: %w", err)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func hasRole(s store.GetSessionByTokenHashRow, role string) bool {
	return slices.Contains(s.RoleNames, role)
}

// isLockedOut reports whether a lockout is in force at now
// (EA/AuthAccountState.cs:11-12).
func isLockedOut(lockoutEnd *time.Time, now time.Time) bool {
	return lockoutEnd != nil && lockoutEnd.After(now)
}

// isActive is .NET's AuthAccountState.IsActive (EA/AuthAccountState.cs:14-15):
// neither disabled nor locked out. It is one of .NET's three notions of
// "active" (inventory §15, item 3): the ActiveAccount policy ignores lockout,
// and session validation ignores lockout but rejects a SCIM-inactive
// non-Owner.
func isActive(disabled bool, lockoutEnd *time.Time, now time.Time) bool {
	return !disabled && !isLockedOut(lockoutEnd, now)
}

// createSession starts a new session for userID on tx and returns its token,
// which the caller hands to the browser with setSessionCookie. Every sign-in
// creates one, so a token planted before sign-in never becomes a session.
// mfa records that the sign-in verified a second factor (TOTP, a recovery
// code, a passkey); password-only sign-in passes false. persistent is
// rememberMe. Running on the caller's transaction means a sign-in that fails
// later leaves no session behind.
//
// Each sign-in also purges the user's dead sessions, in the same
// transaction (PurgeDeadUserSessions: revoked, or past the standard
// absolute lifetime at now), so a user's rows stay bounded by the sessions
// that could still be valid, and a stamp rotation's revocation updates no
// long history.
func (a *Access) createSession(ctx context.Context, tx store.DBTX, userID uuid.UUID, persistent, mfa bool, r *http.Request) (token string, err error) {
	now := a.now()
	var mfaVerifiedAt *time.Time
	if mfa {
		mfaVerifiedAt = &now
	}
	var ip *string
	if v := httpx.ClientIP(r); v != "" {
		ip = &v
	}
	q := store.New(tx)
	if err := q.PurgeDeadUserSessions(ctx, store.PurgeDeadUserSessionsParams{
		UserID: userID, Now: now, Absolute: interval(a.cfg.Sessions.Absolute),
	}); err != nil {
		return "", fmt.Errorf("identity: purge dead sessions: %w", err)
	}
	raw, hash := newToken()
	if err := q.InsertSession(ctx, store.InsertSessionParams{
		ID:            uuid.New(),
		UserID:        userID,
		TokenHash:     hash,
		Persistent:    persistent,
		MfaVerifiedAt: mfaVerifiedAt,
		Now:           now,
		Ip:            ip,
		UserAgent:     userAgent(r),
	}); err != nil {
		return "", fmt.Errorf("identity: create session: %w", err)
	}
	return raw, nil
}

// userAgent is r's User-Agent as identity.sessions stores it: valid UTF-8,
// at most maxUserAgentLength characters, or nil when absent.
func userAgent(r *http.Request) *string {
	ua := strings.ToValidUTF8(r.UserAgent(), "")
	if ua == "" {
		return nil
	}
	if runes := []rune(ua); len(runes) > maxUserAgentLength {
		ua = string(runes[:maxUserAgentLength])
	}
	return &ua
}

// revokeOtherSessions ends every live session of userID except keep, the
// caller's own, which survives a change the user made themselves (spec
// *Sessions*, Revocation). q is the caller's transaction, so the revocation
// commits or rolls back with the change that caused it.
func (a *Access) revokeOtherSessions(ctx context.Context, q *store.Queries, userID, keep uuid.UUID) error {
	if err := q.RevokeUserSessionsExcept(ctx, store.RevokeUserSessionsExceptParams{UserID: userID, Keep: keep, Now: a.now()}); err != nil {
		return fmt.Errorf("identity: revoke other sessions: %w", err)
	}
	return nil
}

// revokeAllSessions ends every live session of userID, the caller's
// included, on the caller's transaction q.
func (a *Access) revokeAllSessions(ctx context.Context, q *store.Queries, userID uuid.UUID) error {
	if err := q.RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: userID, Now: a.now()}); err != nil {
		return fmt.Errorf("identity: revoke sessions: %w", err)
	}
	return nil
}

// rotateSecurityStamp does what rotating .NET's security stamp did beyond
// the cookie, on the caller's transaction q: it ends every live session of
// userID except keep, the caller's own (uuid.Nil keeps none), and spends
// every password-reset link the user holds, since .NET's reset tokens were
// bound to the stamp and died with it.
func (a *Access) rotateSecurityStamp(ctx context.Context, q *store.Queries, userID, keep uuid.UUID) error {
	var err error
	if keep == uuid.Nil {
		err = a.revokeAllSessions(ctx, q, userID)
	} else {
		err = a.revokeOtherSessions(ctx, q, userID, keep)
	}
	if err != nil {
		return err
	}
	if err := q.DeleteUserPasswordResetTokens(ctx, userID); err != nil {
		return fmt.Errorf("identity: spend reset links: %w", err)
	}
	return nil
}
