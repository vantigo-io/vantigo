package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// Account lockout, ASP.NET Identity's options as .NET set them
// (EA/AuthServiceCollectionExtensions.cs:35-37): the fifth consecutive wrong
// password locks the account for 15 minutes.
const (
	maxFailedLoginAttempts = 5
	lockoutDuration        = 15 * time.Minute
)

// opaqueEmailDomain is the domain of the synthetic address OIDC
// provisioning gives an account whose provider sends no email. The domain
// is reserved, so no real address has it; responses show such an account's
// email as null (EA/AuthEndpoints.cs:587-588).
const opaqueEmailDomain = "sso.invalid"

// Password sign-in's messages, .NET's (EA/AuthEndpoints.cs:294-322).
const (
	loginInvalidMessage       = "The login request is invalid."
	accountUnavailableMessage = "The account is temporarily unavailable. Please try again later."
	accountLockedMessage      = "The account is temporarily locked. Please try again later."
	invalidCredentialsMessage = "Invalid email or password."
)

// dummyPasswordHash is what a password is verified against when the email
// names no account, or one without a password. An unknown email then costs
// what a wrong password does, so the two are indistinguishable in time as
// well as in the answer.
var dummyPasswordHash = sync.OnceValues(func() (string, error) {
	return hashPassword("vantigo-no-such-account")
})

// PostIdentityLogin signs in with email and password
// (EA/AuthEndpoints.cs:270-359). The order and the answers are .NET's:
//
//  1. A blank email or password: 400 invalid_request, per field.
//  2. The per-account throttle (policyLoginAttempts, keyed EMAIL|ip) is
//     already full: 429 rate_limited, without Retry-After.
//  3. The account exists but is disabled, or SCIM-inactive and not an
//     Owner: 429 account_locked, before the password is checked.
//  4. The account is locked out: 429 account_locked.
//  5. A wrong password counts a failure on the account, and the one that
//     locks it answers 429 account_locked. Otherwise the failure, like an
//     unknown email's, counts on the throttle: 401 invalid_credentials,
//     the same answer for both.
//  6. The right password clears the throttle. With TOTP enrolled it answers
//     requiresTwoFactor and a login ticket (beginTwoFactor). Otherwise it
//     clears the failure count and starts a non-persistent session that has
//     not verified a second factor.
func (s *server) PostIdentityLogin(ctx context.Context, req gen.PostIdentityLoginRequestObject) (gen.PostIdentityLoginResponseObject, error) {
	var email, password string
	if req.Body != nil {
		email, password = deref(req.Body.Email), deref(req.Body.Password)
	}
	fields := map[string][]string{}
	if strings.TrimSpace(email) == "" {
		fields["email"] = []string{"Email is required."}
	}
	if strings.TrimSpace(password) == "" {
		fields["password"] = []string{"Password is required."}
	}
	if len(fields) > 0 {
		return gen.PostIdentityLogin400JSONResponse(authErrorBody("invalid_request", loginInvalidMessage, fields)), nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}

	// The throttle is consulted before any credential work, for accounts
	// that do not exist too.
	throttle := normalizeEmail(email) + "|" + httpx.ClientIP(r)
	d, err := s.deps.Limiter.Blocked(ctx, policyLoginAttempts, throttle)
	if err != nil {
		return nil, fmt.Errorf("identity: login throttle: %w", err)
	}
	if !d.Allowed {
		return loginRefused("rate_limited", authRateLimitMessage), nil
	}

	now := s.deps.Clock()
	u, err := s.q.GetLoginCandidate(ctx, store.GetLoginCandidateParams{
		NormalizedEmail: normalizeEmail(email),
		ScimEnabled:     s.deps.Config.SCIM != nil,
	})
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identity: login: %w", err)
	}
	if found && (u.IsDisabled || u.ScimInactive) {
		return loginRefused("account_locked", accountUnavailableMessage), nil
	}
	if found && isLockedOut(u.LockoutEnd, now) {
		return loginRefused("account_locked", accountLockedMessage), nil
	}

	var hash *string
	if found {
		hash = u.PasswordHash
	}
	ok, err := checkPassword(hash, password)
	if err != nil {
		return nil, fmt.Errorf("identity: login: %w", err)
	}
	if !ok {
		if found {
			lockoutEnd, err := s.q.RecordLoginFailure(ctx, store.RecordLoginFailureParams{
				ID:           u.ID,
				MaxFailures:  maxFailedLoginAttempts,
				LockoutUntil: now.Add(lockoutDuration),
			})
			if err != nil {
				return nil, fmt.Errorf("identity: login: %w", err)
			}
			if isLockedOut(lockoutEnd, now) {
				return loginRefused("account_locked", accountLockedMessage), nil
			}
		}
		if _, err := s.deps.Limiter.Hit(ctx, policyLoginAttempts, throttle); err != nil {
			return nil, fmt.Errorf("identity: login throttle: %w", err)
		}
		return gen.PostIdentityLogin401JSONResponse(authErrorBody("invalid_credentials", invalidCredentialsMessage, nil)), nil
	}

	if err := s.deps.Limiter.Reset(ctx, policyLoginAttempts, throttle); err != nil {
		return nil, fmt.Errorf("identity: login throttle: %w", err)
	}
	if u.TotpEnabled {
		// ASP.NET resets the failure count only once the second factor passes.
		return s.beginTwoFactor(ctx, r, u.ID, now)
	}
	if err := s.q.ResetLoginFailures(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("identity: login: %w", err)
	}
	token, err := s.access.createSession(ctx, s.deps.Pool, u.ID, false, false, r)
	if err != nil {
		return nil, err
	}
	roles := orderRoles(u.RoleNames)
	user := authUser(u.ID, u.DisplayName, u.Email, roles)
	return loginOK{
		cookies: cookies{s.access.newSessionCookie(token, false)},
		body: gen.PostIdentityLogin200JSONResponse{
			User:                  &user,
			RequiresTwoFactor:     false,
			TwoFactorEnabled:      false,
			MfaEnrollmentRequired: slices.Contains(roles, RoleOwner) && s.deps.Config.OwnersRequireMFA,
			Tenants:               noTenants(),
		},
	}, nil
}

// beginTwoFactor answers the right password for an account with TOTP
// enrolled (EA/AuthEndpoints.cs:327-338). There is no session yet: a login
// ticket, a single-use row that lives loginTicketLifetime, travels in the
// vantigo.2fa cookie for POST /login/2fa to redeem. A session cookie the
// browser still holds is cleared, as .NET's SignOutAsync cleared it.
func (s *server) beginTwoFactor(ctx context.Context, r *http.Request, userID uuid.UUID, now time.Time) (gen.PostIdentityLoginResponseObject, error) {
	if err := s.q.DeleteExpiredLoginTickets(ctx, now); err != nil {
		return nil, fmt.Errorf("identity: login ticket: %w", err)
	}
	raw, hash := newToken()
	if err := s.q.InsertLoginTicket(ctx, store.InsertLoginTicketParams{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: now.Add(loginTicketLifetime),
	}); err != nil {
		return nil, fmt.Errorf("identity: login ticket: %w", err)
	}
	cs := cookies{s.access.newLoginTicketCookie(raw)}
	if _, ok := sessionTokenFrom(r); ok {
		cs = append(cs, s.access.expiredSessionCookie())
	}
	return loginOK{
		cookies: cs,
		body: gen.PostIdentityLogin200JSONResponse{
			User:                  nil,
			RequiresTwoFactor:     true,
			TwoFactorEnabled:      true,
			MfaEnrollmentRequired: false,
			Tenants:               noTenants(),
		},
	}, nil
}

// checkPassword verifies pw against hash, or, without one, against
// dummyPasswordHash, which never counts as a match.
func checkPassword(hash *string, pw string) (bool, error) {
	if hash == nil {
		dummy, err := dummyPasswordHash()
		if err != nil {
			return false, err
		}
		_, err = verifyPassword(dummy, pw)
		return false, err
	}
	return verifyPassword(*hash, pw)
}

func loginRefused(code, message string) gen.PostIdentityLogin429JSONResponse {
	return gen.PostIdentityLogin429JSONResponse{Body: authErrorBody(code, message, nil)}
}

// loginOK is sign-in's 200: its cookies, then the generated body.
type loginOK struct {
	cookies
	body gen.PostIdentityLogin200JSONResponse
}

func (r loginOK) VisitPostIdentityLoginResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.body.VisitPostIdentityLoginResponse(w)
}

// PostIdentityLogout ends the caller's session and clears its cookie
// (EA/AuthEndpoints.cs:421-425). Unlike .NET, which only cleared the cookie,
// it revokes the session row, so a copy of the cookie dies with it (spec
// *Sessions*, a deliberate divergence).
func (s *server) PostIdentityLogout(ctx context.Context, _ gen.PostIdentityLogoutRequestObject) (gen.PostIdentityLogoutResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.q.RevokeSession(ctx, store.RevokeSessionParams{ID: p.SessionID, Now: s.deps.Clock()}); err != nil {
		return nil, fmt.Errorf("identity: logout: %w", err)
	}
	return logoutOK{
		cookies: cookies{s.access.expiredSessionCookie()},
		body:    gen.PostIdentityLogout200JSONResponse{Success: true},
	}, nil
}

// logoutOK is logout's 200: the cleared cookie, then the generated body.
type logoutOK struct {
	cookies
	body gen.PostIdentityLogout200JSONResponse
}

func (r logoutOK) VisitPostIdentityLogoutResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.body.VisitPostIdentityLogoutResponse(w)
}

// GetIdentityProviders lists the workforce sign-in providers
// (EA/WorkforceOidcEndpoints.cs:27-29). OIDC sign-in is not served yet, so
// there is none to offer.
func (*server) GetIdentityProviders(context.Context, gen.GetIdentityProvidersRequestObject) (gen.GetIdentityProvidersResponseObject, error) {
	return gen.GetIdentityProviders200JSONResponse{Oidc: nil}, nil
}

// normalizeEmail is an email's lookup form: identity.users.normalized_email,
// and the account half of the throttle key. It is ASP.NET Identity's
// upper-invariant normalizer over the trimmed address, as .NET's callers
// trimmed it.
func normalizeEmail(email string) string {
	return strings.ToUpper(strings.TrimSpace(email))
}

// publicEmail is the email a response shows: null for an OIDC account's
// synthetic address.
func publicEmail(email string) *string {
	if strings.HasSuffix(email, "@"+opaqueEmailDomain) {
		return nil
	}
	return &email
}

// authUser is a user as the sign-in, bootstrap and session responses show
// one. roles is listed as given, never null.
func authUser(id uuid.UUID, displayName, email string, roles []string) gen.AuthUserResponse {
	if roles == nil {
		roles = []string{}
	}
	return gen.AuthUserResponse{Id: id, DisplayName: displayName, Email: publicEmail(email), Roles: roles}
}

// noTenants is AuthSuccessResponse's tenants: the contract still carries
// tenancy, and a single-tenant installation answers an empty list (spec
// *Contract leftovers*). activeTenantId, optional there, is left out.
func noTenants() *[]gen.TenantSessionResponse {
	return &[]gen.TenantSessionResponse{}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
