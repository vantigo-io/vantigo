package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
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
//  6. The right password finishes in completePasswordLogin.
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
	throttle := loginThrottleKey(email, httpx.ClientIP(r))
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
	ok, err := s.checkPassword(hash, password)
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

	return s.completePasswordLogin(ctx, r, u, throttle, now)
}

// completePasswordLogin finishes a sign-in whose password was right for u,
// the account as read before the check. With TOTP enrolled it clears the
// throttle and answers requiresTwoFactor with a login ticket
// (beginTwoFactor); ASP.NET clears the failure count only once the second
// factor passes, and /login/2fa refuses a locked account then. Otherwise it
// clears the failure count only if no lockout is in force at now: parallel
// wrong guesses may have locked the account since u was read, and then the
// lock wins with 429 account_locked and no session. Once the count is
// cleared, so is the throttle, and a non-persistent session starts that has
// not verified a second factor.
func (s *server) completePasswordLogin(ctx context.Context, r *http.Request, u store.GetLoginCandidateRow, throttle string, now time.Time) (gen.PostIdentityLoginResponseObject, error) {
	if u.TotpEnabled {
		if err := s.deps.Limiter.Reset(ctx, policyLoginAttempts, throttle); err != nil {
			return nil, fmt.Errorf("identity: login throttle: %w", err)
		}
		return s.beginTwoFactor(ctx, r, u.ID, now)
	}
	unlocked, err := s.q.ResetLoginFailures(ctx, store.ResetLoginFailuresParams{ID: u.ID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: login: %w", err)
	}
	if unlocked == 0 {
		return loginRefused("account_locked", accountLockedMessage), nil
	}
	if err := s.deps.Limiter.Reset(ctx, policyLoginAttempts, throttle); err != nil {
		return nil, fmt.Errorf("identity: login throttle: %w", err)
	}
	token, err := s.access.createSession(ctx, s.deps.Pool, u.ID, false, false, r)
	if err != nil {
		return nil, err
	}
	roles := orderRoles(u.RoleNames)
	user := authUser(u.ID, u.DisplayName, u.Email, roles)
	return loginOK{
		cookies: cookies{s.access.newSessionCookie(token, false)},
		body: authSuccessBody{AuthSuccessResponse: gen.AuthSuccessResponse{
			User:                  &user,
			RequiresTwoFactor:     false,
			TwoFactorEnabled:      false,
			MfaEnrollmentRequired: slices.Contains(roles, RoleOwner) && s.deps.Config.OwnersRequireMFA,
			Tenants:               noTenants(),
		}},
	}, nil
}

// loginThrottleKey is the per-account throttle's key: the hex SHA-256 of
// the normalized email, then "|" and the client address. Hashing bounds the
// key's length whatever email a caller sends (the limiter's key is an
// indexed primary key, and PostgreSQL refuses an oversize index row), and
// keeps the addresses callers tried out of the limiter table in clear.
func loginThrottleKey(email, clientIP string) string {
	sum := sha256.Sum256([]byte(normalizeEmail(email)))
	return hex.EncodeToString(sum[:]) + "|" + clientIP
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
		body: authSuccessBody{AuthSuccessResponse: gen.AuthSuccessResponse{
			User:                  nil,
			RequiresTwoFactor:     true,
			TwoFactorEnabled:      true,
			MfaEnrollmentRequired: false,
			Tenants:               noTenants(),
		}},
	}, nil
}

// checkPassword verifies pw against hash. Without one (an unknown email, or
// an account without a password) it verifies against the server's dummy
// hash and never counts that as a match, so an unknown email costs what a
// wrong password does and the two are indistinguishable in time as well as
// in the answer.
func (s *server) checkPassword(hash *string, pw string) (bool, error) {
	if hash == nil {
		_, err := verifyPassword(s.dummyPasswordHash, pw)
		return false, err
	}
	return verifyPassword(*hash, pw)
}

func loginRefused(code, message string) gen.PostIdentityLogin429JSONResponse {
	return gen.PostIdentityLogin429JSONResponse{Body: authErrorBody(code, message, nil)}
}

// authSuccessBody is AuthSuccessResponse as identity writes it. The
// generated type drops a nil activeTenantId (omitempty); the contract's
// tenancy leftover is written as an explicit null instead (spec *Contract
// leftovers*). The outer field shadows the embedded one of the same JSON
// name.
type authSuccessBody struct {
	gen.AuthSuccessResponse
	ActiveTenantID *uuid.UUID `json:"activeTenantId"`
}

// loginOK is sign-in's 200: its cookies, then the body.
type loginOK struct {
	cookies
	body authSuccessBody
}

func (r loginOK) VisitPostIdentityLoginResponse(w http.ResponseWriter) error {
	r.set(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(r.body)
}

// Two-factor sign-in's messages, .NET's (EA/AuthEndpoints.cs:370-401).
const (
	twoFactorCodeRequiredMessage = "A two-factor code is required."
	twoFactorExpiredMessage      = "The two-factor sign-in session has expired."
	invalidTwoFactorCodeMessage  = "The two-factor code is invalid."
)

// PostIdentityLogin2fa is sign-in's second step (EA/AuthEndpoints.cs:361-419):
// it redeems the login ticket beginTwoFactor issued. The order and the
// answers are .NET's:
//
//  1. A blank code: 400 invalid_request.
//  2. No ticket cookie, or a ticket that is unknown, spent or expired at
//     now: 401 two_factor_session_expired.
//  3. The account is disabled (or SCIM-inactive and not an Owner) or
//     locked out: the ticket is discarded and both cookies cleared, as
//     .NET signed both schemes out (:381-386), and 429 account_locked.
//  4. Exactly six digits are a TOTP code, anything else a recovery code
//     (:388-396). A TOTP code must match a step within one of now, and that
//     step must be later than the last one spent, so a replay fails. A
//     recovery code is spent by the attempt that uses it.
//  5. A code that fails counts toward lockout, as a wrong password does,
//     a failed recovery code included (deliberate hardening, see
//     redeemLoginTicket): 429 account_locked when this failure locked the
//     account, else 401
//     invalid_two_factor_code. The ticket survives a failed code, so the
//     user can try again while it lasts, bounded by the lockout: .NET's
//     failure path (:397-402) answers without signing the two-factor scheme
//     out, unlike the refusal in 3, and ASP.NET's SignInManager signs it out
//     only once a code succeeds.
//  6. A code that verifies clears the failure count. The reset is
//     conditional on no lockout being in force, a guard the row lock now
//     makes unreachable (it would answer 429 account_locked and keep
//     nothing of the attempt). Then the ticket is spent and a session
//     starts that verified a second factor, persistent when rememberMe is
//     set.
//
// Everything after 1 and 2's cookie check runs in one transaction that
// locks the user row first and then the ticket (redeemLoginTicket gives
// the lock order), so two redemptions of one ticket take turns and the
// second finds it spent. The success also spends the ticket with a
// conditional delete, so the ticket is single-use without the locks too.
func (s *server) PostIdentityLogin2fa(ctx context.Context, req gen.PostIdentityLogin2faRequestObject) (gen.PostIdentityLogin2faResponseObject, error) {
	var code string
	var rememberMe bool
	if req.Body != nil {
		code = twoFactorCode(req.Body.Code)
		rememberMe = req.Body.RememberMe != nil && *req.Body.RememberMe
	}
	if strings.TrimSpace(code) == "" {
		return gen.PostIdentityLogin2fa400JSONResponse(authErrorBody("invalid_request", twoFactorCodeRequiredMessage, nil)), nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	raw, ok := loginTicketFrom(r)
	if !ok {
		return s.twoFactorExpired(r), nil
	}
	ticket, ok := parseToken(raw)
	if !ok {
		return s.twoFactorExpired(r), nil
	}
	var answer gen.PostIdentityLogin2faResponseObject
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		answer, err = s.redeemLoginTicket(ctx, tx, r, ticket, code, rememberMe)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityLogin2faResponseObject](err)
	}
	return answer, nil
}

// redeemLoginTicket is PostIdentityLogin2fa's transaction, from 3 on. Its
// answers commit what they did (a discarded ticket, a counted failure, a
// session); only the refusals it returns as errors, a lock that won the
// race and a ticket spent meanwhile, roll the attempt back.
func (s *server) redeemLoginTicket(ctx context.Context, tx pgx.Tx, r *http.Request, ticket []byte, code string, rememberMe bool) (gen.PostIdentityLogin2faResponseObject, error) {
	now := s.deps.Clock()
	q := store.New(tx)
	userID, err := q.FindLoginTicket(ctx, store.FindLoginTicketParams{TokenHash: ticket, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.twoFactorExpired(r), nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	// Lock order: the user row first, then the ticket, then the step or the
	// recovery code. Every MFA change (setup, enable, disable, regeneration,
	// the owner reset) locks the user row before it touches the recovery
	// codes, and deleting a user locks the row before its tickets go with
	// it. Taking the row first here keeps every path in that one order, so
	// none can deadlock; spending a recovery code before the row once did,
	// against a concurrent disable or regeneration (40P01, a 500). Holding
	// the row also makes this read authoritative: no failure or lockout can
	// land between it and the end of the step.
	u, err := q.GetTwoFactorCandidate(ctx, store.GetTwoFactorCandidateParams{ScimEnabled: s.deps.Config.SCIM != nil, ID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		// The user was deleted since the ticket was read, and the ticket
		// went with them (ON DELETE CASCADE).
		return s.twoFactorExpired(r), nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	// Under the row lock, lock the ticket: a redemption of the same ticket
	// that held the row first has spent it by now.
	switch _, err := q.LockLoginTicket(ctx, store.LockLoginTicketParams{TokenHash: ticket, Now: now}); {
	case errors.Is(err, pgx.ErrNoRows):
		return s.twoFactorExpired(r), nil
	case err != nil:
		return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	if u.IsDisabled || u.ScimInactive || isLockedOut(u.LockoutEnd, now) {
		if err := q.DeleteLoginTicket(ctx, ticket); err != nil {
			return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
		}
		return twoFactorRefused{
			cookies: s.twoFactorSignOut(r),
			body:    gen.PostIdentityLogin2fa429JSONResponse{Body: authErrorBody("account_locked", accountUnavailableMessage, nil)},
		}, nil
	}
	if !u.TotpEnabled {
		// TOTP was turned off (or reset) after the password step: the
		// second factor this ticket was issued for is gone, and a new
		// sign-in answers without one.
		if err := q.DeleteLoginTicket(ctx, ticket); err != nil {
			return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
		}
		return s.twoFactorExpired(r), nil
	}

	verified, err := s.verifySecondFactor(ctx, q, u, code, now)
	if err != nil {
		return nil, err
	}
	// A failed code counts toward the password lockout, a failed recovery
	// code included. For recovery codes that is deliberate hardening, not
	// parity: ASP.NET's TwoFactorRecoveryCodeSignInAsync counted no failure
	// (it relies on the codes being random), while its authenticator path
	// did. Keep it: every guess at either factor costs one of the five
	// attempts.
	if !verified {
		lockoutEnd, err := q.RecordLoginFailure(ctx, store.RecordLoginFailureParams{
			ID:           u.ID,
			MaxFailures:  maxFailedLoginAttempts,
			LockoutUntil: now.Add(lockoutDuration),
		})
		if err != nil {
			return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
		}
		if isLockedOut(lockoutEnd, now) {
			return gen.PostIdentityLogin2fa429JSONResponse{Body: authErrorBody("account_locked", accountLockedMessage, nil)}, nil
		}
		return gen.PostIdentityLogin2fa401JSONResponse(authErrorBody("invalid_two_factor_code", invalidTwoFactorCodeMessage, nil)), nil
	}

	unlocked, err := q.ResetLoginFailures(ctx, store.ResetLoginFailuresParams{ID: u.ID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	if unlocked == 0 {
		// Unreachable under the row lock, which kept any lockout from
		// landing after the read above; kept as a guard should the lock
		// order ever change.
		return nil, refuse(http.StatusTooManyRequests, "account_locked", accountLockedMessage, nil)
	}
	spent, err := q.SpendLoginTicket(ctx, store.SpendLoginTicketParams{TokenHash: ticket, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	if spent == 0 {
		// The row lock makes this unreachable; the condition keeps the
		// ticket single-use without relying on it, and rolls the attempt
		// back so the factor it used is not spent.
		return nil, refuse(http.StatusUnauthorized, "two_factor_session_expired", twoFactorExpiredMessage, nil)
	}
	token, err := s.access.createSession(ctx, tx, u.ID, rememberMe, true, r)
	if err != nil {
		return nil, err
	}
	roles := orderRoles(u.RoleNames)
	user := authUser(u.ID, u.DisplayName, u.Email, roles)
	return loginOK{
		cookies: cookies{s.access.newSessionCookie(token, rememberMe), s.access.expiredLoginTicketCookie()},
		body: authSuccessBody{AuthSuccessResponse: gen.AuthSuccessResponse{
			User:                  &user,
			RequiresTwoFactor:     false,
			TwoFactorEnabled:      true,
			MfaEnrollmentRequired: s.mfaEnrollmentRequired(roles, u.TotpEnabled),
			Tenants:               noTenants(),
		}},
	}, nil
}

// verifySecondFactor checks code for u on q, the sign-in's transaction, and
// spends what it used: a TOTP code's step (RecordTOTPStep, which a replay
// or a concurrent use of the same code fails) or the recovery code itself
// (ConsumeRecoveryCode, which only one of two concurrent uses succeeds at).
func (s *server) verifySecondFactor(ctx context.Context, q *store.Queries, u store.GetTwoFactorCandidateRow, code string, now time.Time) (bool, error) {
	if isTOTPCode(code) {
		step, ok, err := s.verifyTOTP(u.TotpSecret, code, now)
		if err != nil || !ok {
			return false, err
		}
		spent, err := q.RecordTOTPStep(ctx, store.RecordTOTPStepParams{Step: step, ID: u.ID, TotpSecret: u.TotpSecret})
		if err != nil {
			return false, fmt.Errorf("identity: two-factor sign-in: %w", err)
		}
		return spent == 1, nil
	}
	spent, err := q.ConsumeRecoveryCode(ctx, store.ConsumeRecoveryCodeParams{UserID: u.ID, CodeHash: hashRecoveryCode(code)})
	if err != nil {
		return false, fmt.Errorf("identity: two-factor sign-in: %w", err)
	}
	return spent == 1, nil
}

// twoFactorExpired is 401 two_factor_session_expired, clearing a ticket
// cookie the browser still sends.
func (s *server) twoFactorExpired(r *http.Request) gen.PostIdentityLogin2faResponseObject {
	body := gen.PostIdentityLogin2fa401JSONResponse(authErrorBody("two_factor_session_expired", twoFactorExpiredMessage, nil))
	if _, ok := loginTicketFrom(r); !ok {
		return body
	}
	return twoFactorRefused{cookies: cookies{s.access.expiredLoginTicketCookie()}, body: body}
}

// twoFactorSignOut clears the ticket cookie, and a session cookie the
// browser still holds, as .NET's refusal signed both schemes out.
func (s *server) twoFactorSignOut(r *http.Request) cookies {
	cs := cookies{s.access.expiredLoginTicketCookie()}
	if _, ok := sessionTokenFrom(r); ok {
		cs = append(cs, s.access.expiredSessionCookie())
	}
	return cs
}

// twoFactorRefused is a refusal of the second step that also clears
// cookies: its cookies, then the generated answer.
type twoFactorRefused struct {
	cookies
	body gen.PostIdentityLogin2faResponseObject
}

func (r twoFactorRefused) VisitPostIdentityLogin2faResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.body.VisitPostIdentityLogin2faResponse(w)
}

// loginOK is also the second step's 200.
func (r loginOK) VisitPostIdentityLogin2faResponse(w http.ResponseWriter) error {
	return r.VisitPostIdentityLoginResponse(w)
}

func (r refusal) VisitPostIdentityLogin2faResponse(w http.ResponseWriter) error { return r.write(w) }

// PostIdentityLogout ends the caller's session and clears its cookie
// (EA/AuthEndpoints.cs:421-425). Unlike .NET, which only cleared the cookie,
// it revokes the session row, so a copy of the cookie dies with it (spec
// *Sessions*, a deliberate divergence). A pending two-factor sign-in the
// browser holds ends too (discardLoginTicket).
func (s *server) PostIdentityLogout(ctx context.Context, _ gen.PostIdentityLogoutRequestObject) (gen.PostIdentityLogoutResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.q.RevokeSession(ctx, store.RevokeSessionParams{ID: p.SessionID, Now: s.deps.Clock()}); err != nil {
		return nil, fmt.Errorf("identity: logout: %w", err)
	}
	cs := cookies{s.access.expiredSessionCookie()}
	ticket, err := s.discardLoginTicket(ctx, r)
	if err != nil {
		return nil, err
	}
	if ticket != nil {
		cs = append(cs, ticket)
	}
	return logoutOK{
		cookies: cs,
		body:    gen.PostIdentityLogout200JSONResponse{Success: true},
	}, nil
}

// discardLoginTicket ends a pending two-factor sign-in r carries, as .NET's
// SignOutAsync signed the TwoFactorUserId scheme out along with the app
// cookie: the ticket's row is deleted, so a copy of the cookie is dead too,
// and the returned cookie clears it. Without a ticket cookie it returns
// nil.
func (s *server) discardLoginTicket(ctx context.Context, r *http.Request) (*http.Cookie, error) {
	raw, ok := loginTicketFrom(r)
	if !ok {
		return nil, nil
	}
	if hash, ok := parseToken(raw); ok {
		if err := s.q.DeleteLoginTicket(ctx, hash); err != nil {
			return nil, fmt.Errorf("identity: discard login ticket: %w", err)
		}
	}
	return s.access.expiredLoginTicketCookie(), nil
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
// *Contract leftovers*). authSuccessBody writes activeTenantId as null.
func noTenants() *[]gen.TenantSessionResponse {
	return &[]gen.TenantSessionResponse{}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
