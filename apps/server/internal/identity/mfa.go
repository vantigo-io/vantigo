package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// MFA management's messages, .NET's (EA/AuthAccountEndpoints.cs:190-399).
const (
	invalidMFACodeMessage         = "The authenticator code is invalid."
	mfaCodeRequiredMessage        = "A valid authenticator code is required."
	mfaRequiredMessage            = "MFA cannot be disabled while privileged management access requires it."
	ownerMFAResetForbiddenMessage = "Only an Owner can reset Owner MFA."
	selfMFAResetMessage           = "Use the self-service MFA endpoints for your own account."
)

var (
	// sessionUserGone answers a session whose user vanished between the
	// access check and the handler (GetIdentitySession answers it the same way).
	sessionUserGone = refuse(http.StatusUnauthorized, "unauthenticated", "The session is not authenticated.", nil)
	// reauthenticationRequired refuses a missing or wrong current password.
	reauthenticationRequired = refuse(http.StatusBadRequest, "reauthentication_required", reauthenticationRequiredMessage, nil)
)

// The /account/mfa* and /owner/mfa* operations share one handler each, as
// .NET mapped both groups to the same methods under ActiveAccount, so that
// an Owner who has not enrolled yet can reach enrolment
// (EA/AuthAccountEndpoints.cs:53-70).

func (s *server) GetIdentityAccountMfa(ctx context.Context, _ gen.GetIdentityAccountMfaRequestObject) (gen.GetIdentityAccountMfaResponseObject, error) {
	body, err := s.mfaStatus(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityAccountMfaResponseObject](err)
	}
	return gen.GetIdentityAccountMfa200JSONResponse(body), nil
}

func (s *server) GetIdentityOwnerMfa(ctx context.Context, _ gen.GetIdentityOwnerMfaRequestObject) (gen.GetIdentityOwnerMfaResponseObject, error) {
	body, err := s.mfaStatus(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityOwnerMfaResponseObject](err)
	}
	return gen.GetIdentityOwnerMfa200JSONResponse(body), nil
}

func (s *server) GetIdentityAccountMfaSetup(ctx context.Context, _ gen.GetIdentityAccountMfaSetupRequestObject) (gen.GetIdentityAccountMfaSetupResponseObject, error) {
	body, err := s.mfaSetupState(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityAccountMfaSetupResponseObject](err)
	}
	return gen.GetIdentityAccountMfaSetup200JSONResponse(body), nil
}

func (s *server) GetIdentityOwnerMfaSetup(ctx context.Context, _ gen.GetIdentityOwnerMfaSetupRequestObject) (gen.GetIdentityOwnerMfaSetupResponseObject, error) {
	body, err := s.mfaSetupState(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityOwnerMfaSetupResponseObject](err)
	}
	return gen.GetIdentityOwnerMfaSetup200JSONResponse(body), nil
}

func (s *server) PostIdentityAccountMfaSetup(ctx context.Context, req gen.PostIdentityAccountMfaSetupRequestObject) (gen.PostIdentityAccountMfaSetupResponseObject, error) {
	body, err := s.initializeMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityAccountMfaSetupResponseObject](err)
	}
	return gen.PostIdentityAccountMfaSetup200JSONResponse(body), nil
}

func (s *server) PostIdentityOwnerMfaSetup(ctx context.Context, req gen.PostIdentityOwnerMfaSetupRequestObject) (gen.PostIdentityOwnerMfaSetupResponseObject, error) {
	body, err := s.initializeMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerMfaSetupResponseObject](err)
	}
	return gen.PostIdentityOwnerMfaSetup200JSONResponse(body), nil
}

func (s *server) PostIdentityAccountMfaEnable(ctx context.Context, req gen.PostIdentityAccountMfaEnableRequestObject) (gen.PostIdentityAccountMfaEnableResponseObject, error) {
	body, err := s.enableMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityAccountMfaEnableResponseObject](err)
	}
	return gen.PostIdentityAccountMfaEnable200JSONResponse(body), nil
}

func (s *server) PostIdentityOwnerMfaEnable(ctx context.Context, req gen.PostIdentityOwnerMfaEnableRequestObject) (gen.PostIdentityOwnerMfaEnableResponseObject, error) {
	body, err := s.enableMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerMfaEnableResponseObject](err)
	}
	return gen.PostIdentityOwnerMfaEnable200JSONResponse(body), nil
}

func (s *server) PostIdentityAccountMfaDisable(ctx context.Context, req gen.PostIdentityAccountMfaDisableRequestObject) (gen.PostIdentityAccountMfaDisableResponseObject, error) {
	body, err := s.disableMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityAccountMfaDisableResponseObject](err)
	}
	return gen.PostIdentityAccountMfaDisable200JSONResponse(body), nil
}

func (s *server) PostIdentityOwnerMfaDisable(ctx context.Context, req gen.PostIdentityOwnerMfaDisableRequestObject) (gen.PostIdentityOwnerMfaDisableResponseObject, error) {
	body, err := s.disableMFA(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerMfaDisableResponseObject](err)
	}
	return gen.PostIdentityOwnerMfaDisable200JSONResponse(body), nil
}

func (s *server) PostIdentityAccountMfaRecoveryCodes(ctx context.Context, req gen.PostIdentityAccountMfaRecoveryCodesRequestObject) (gen.PostIdentityAccountMfaRecoveryCodesResponseObject, error) {
	body, err := s.regenerateRecoveryCodes(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityAccountMfaRecoveryCodesResponseObject](err)
	}
	return gen.PostIdentityAccountMfaRecoveryCodes200JSONResponse(body), nil
}

func (s *server) PostIdentityOwnerMfaRecoveryCodes(ctx context.Context, req gen.PostIdentityOwnerMfaRecoveryCodesRequestObject) (gen.PostIdentityOwnerMfaRecoveryCodesResponseObject, error) {
	body, err := s.regenerateRecoveryCodes(ctx, req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerMfaRecoveryCodesResponseObject](err)
	}
	return gen.PostIdentityOwnerMfaRecoveryCodes200JSONResponse(body), nil
}

// mfaCaller returns the signed-in caller and their account as it stands.
func (s *server) mfaCaller(ctx context.Context) (contracts.Principal, store.IdentityUser, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return p, store.IdentityUser{}, err
	}
	u, err := s.q.GetUserByID(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, u, sessionUserGone
	}
	if err != nil {
		return p, u, fmt.Errorf("identity: mfa: %w", err)
	}
	return p, u, nil
}

// mfaEnrollmentRequired is whether a user with roles must still enrol:
// an Owner without TOTP while owners are required to use MFA
// (EA/AuthAccountEndpoints.cs:121-124).
func (s *server) mfaEnrollmentRequired(roles []string, enrolled bool) bool {
	return slices.Contains(roles, RoleOwner) && s.deps.Config.OwnersRequireMFA && !enrolled
}

// mfaStatus is GET /mfa (EA/AuthAccountEndpoints.cs:108-125): whether TOTP
// is on, and whether the caller must still enrol. The roles are the
// caller's current ones, read with the session.
func (s *server) mfaStatus(ctx context.Context) (gen.MfaStatusResponse, error) {
	p, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaStatusResponse{}, err
	}
	return gen.MfaStatusResponse{
		TwoFactorEnabled:      u.TotpEnabled,
		MfaEnrollmentRequired: s.mfaEnrollmentRequired(p.Roles, u.TotpEnabled),
	}, nil
}

// mfaSetupState is GET /mfa/setup (EA/AuthAccountEndpoints.cs:127-142):
// whether a secret exists, never the secret itself. Only the POST that
// creates one ever returns it.
func (s *server) mfaSetupState(ctx context.Context) (gen.MfaSetupResponse, error) {
	_, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaSetupResponse{}, err
	}
	return gen.MfaSetupResponse{SharedKey: nil, AuthenticatorUri: nil, Initialized: u.TotpSecret != nil}, nil
}

// initializeMFA is POST /mfa/setup (EA/AuthAccountEndpoints.cs:144-188). The
// current password is required (requireLocalPassword). A new secret
// replaces any old one, encrypted at rest; totp_enabled stays as it was
// until enable. As .NET's key reset rotated the security stamp, the user's
// other sessions end and their reset links are spent, and the caller's own
// session keeps going but no longer counts as MFA-verified: .NET reissued
// the caller's cookie without its MFA claim (:178-182), since the
// authenticator it verified is being replaced. The answer is the secret and
// its otpauth URI, shown this once.
func (s *server) initializeMFA(ctx context.Context, body *gen.MfaCodeRequest) (gen.MfaSetupResponse, error) {
	p, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaSetupResponse{}, err
	}
	if err := requireLocalPassword(u, passwordOf(body)); err != nil {
		return gen.MfaSetupResponse{}, err
	}
	secret := newTOTPSecret()
	sealed, err := s.sealTOTPSecret(secret)
	if err != nil {
		return gen.MfaSetupResponse{}, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.SetTOTPSecret(ctx, store.SetTOTPSecretParams{TotpSecret: sealed, Version: uuid.New(), Now: now, ID: u.ID}); err != nil {
			return err
		}
		if err := s.access.rotateSecurityStamp(ctx, q, u.ID, p.SessionID); err != nil {
			return err
		}
		return q.SetSessionMFAVerified(ctx, store.SetSessionMFAVerifiedParams{MfaVerifiedAt: nil, ID: p.SessionID})
	})
	if err != nil {
		return gen.MfaSetupResponse{}, fmt.Errorf("identity: mfa setup: %w", err)
	}
	uri := s.otpauthURI(u.Email, secret)
	return gen.MfaSetupResponse{SharedKey: &secret, AuthenticatorUri: &uri, Initialized: true}, nil
}

// otpauthURI is the key URI an authenticator app scans, as .NET built it:
// the issuer and the account escaped as Uri.EscapeDataString does
// (EA/AuthAccountEndpoints.cs:184-186).
func (s *server) otpauthURI(account, secret string) string {
	issuer := escapeDataString(s.deps.Config.MFAIssuer)
	return "otpauth://totp/" + issuer + ":" + escapeDataString(account) +
		"?secret=" + secret + "&issuer=" + issuer + "&digits=6"
}

// enableMFA is POST /mfa/enable (EA/AuthAccountEndpoints.cs:190-237). The
// current password is required. A code that is blank, does not verify
// against the secret setup created, or whose step was already spent is 400
// invalid_mfa_code. Otherwise TOTP is on, the code's step is spent, a fresh
// set of recovery codes replaces any old one and is returned this once, and
// the caller's session now counts as MFA-verified, as .NET reissued the
// caller's cookie with the MFA claim.
func (s *server) enableMFA(ctx context.Context, body *gen.MfaCodeRequest) (gen.MfaEnableResponse, error) {
	p, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaEnableResponse{}, err
	}
	if err := requireLocalPassword(u, passwordOf(body)); err != nil {
		return gen.MfaEnableResponse{}, err
	}
	invalid := refuse(http.StatusBadRequest, "invalid_mfa_code", invalidMFACodeMessage, nil)
	now := s.deps.Clock()
	step, ok, err := s.verifyTOTP(u.TotpSecret, twoFactorCode(codeOf(body)), now)
	if err != nil {
		return gen.MfaEnableResponse{}, err
	}
	if !ok {
		return gen.MfaEnableResponse{}, invalid
	}
	var codes []string
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		enabled, err := q.EnableTOTP(ctx, store.EnableTOTPParams{Step: step, Version: uuid.New(), Now: now, ID: u.ID, TotpSecret: u.TotpSecret})
		if err != nil {
			return err
		}
		if enabled == 0 {
			return invalid
		}
		if codes, err = replaceRecoveryCodes(ctx, q, u.ID); err != nil {
			return err
		}
		return q.SetSessionMFAVerified(ctx, store.SetSessionMFAVerifiedParams{MfaVerifiedAt: &now, ID: p.SessionID})
	})
	if err != nil {
		return gen.MfaEnableResponse{}, err
	}
	return gen.MfaEnableResponse{TwoFactorEnabled: true, RecoveryCodes: codes}, nil
}

// disableMFA is POST /mfa/disable (EA/AuthAccountEndpoints.cs:239-299).
// While owners are required to use MFA, an Owner may not turn it off (403
// mfa_required), nor may an active delegated administrator (403
// delegated_admin_mfa_required). Otherwise the current password is
// required, and then the secret is forgotten, TOTP is off and the recovery
// codes are deleted. As .NET rotated the security stamp, the user's other
// sessions end and their reset links are spent; the caller's own session
// keeps going but no longer counts as MFA-verified, as .NET reissued the
// caller's cookie without claims.
func (s *server) disableMFA(ctx context.Context, body *gen.MfaCodeRequest) (gen.MfaStatusResponse, error) {
	p, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaStatusResponse{}, err
	}
	if s.deps.Config.OwnersRequireMFA {
		if slices.Contains(p.Roles, RoleOwner) {
			return gen.MfaStatusResponse{}, refuse(http.StatusForbidden, "mfa_required", mfaRequiredMessage, nil)
		}
		delegate, err := s.activeDelegatedAdministrator(ctx, u.ID)
		if err != nil {
			return gen.MfaStatusResponse{}, err
		}
		if delegate {
			return gen.MfaStatusResponse{}, refuse(http.StatusForbidden, "delegated_admin_mfa_required", mfaRequiredMessage, nil)
		}
	}
	if err := requireLocalPassword(u, passwordOf(body)); err != nil {
		return gen.MfaStatusResponse{}, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.DisableTOTP(ctx, store.DisableTOTPParams{Version: uuid.New(), Now: now, ID: u.ID}); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, u.ID); err != nil {
			return err
		}
		if err := s.access.rotateSecurityStamp(ctx, q, u.ID, p.SessionID); err != nil {
			return err
		}
		return q.SetSessionMFAVerified(ctx, store.SetSessionMFAVerifiedParams{MfaVerifiedAt: nil, ID: p.SessionID})
	})
	if err != nil {
		return gen.MfaStatusResponse{}, fmt.Errorf("identity: mfa disable: %w", err)
	}
	return gen.MfaStatusResponse{TwoFactorEnabled: false, MfaEnrollmentRequired: false}, nil
}

// activeDelegatedAdministrator reports whether userID holds an active
// authorization delegation (EA/AuthAccountEndpoints.cs:253-255), which,
// while owners are required to use MFA, keeps them from disabling it.
//
// TODO(Task 15): delegations arrive with authorization management, which
// replaces this with the real query (not revoked, and unexpired at the
// Deps clock). Until then no one holds one.
func (*server) activeDelegatedAdministrator(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

// regenerateRecoveryCodes is POST /mfa/recovery-codes
// (EA/AuthAccountEndpoints.cs:301-326): with the current password and a
// current TOTP code, a fresh set of recovery codes replaces the old one.
// Without TOTP on, or with a blank, wrong or already spent code, 400
// invalid_mfa_code. The code's step is spent, as at sign-in.
func (s *server) regenerateRecoveryCodes(ctx context.Context, body *gen.MfaCodeRequest) (gen.MfaRecoveryCodesResponse, error) {
	_, u, err := s.mfaCaller(ctx)
	if err != nil {
		return gen.MfaRecoveryCodesResponse{}, err
	}
	if err := requireLocalPassword(u, passwordOf(body)); err != nil {
		return gen.MfaRecoveryCodesResponse{}, err
	}
	invalid := refuse(http.StatusBadRequest, "invalid_mfa_code", mfaCodeRequiredMessage, nil)
	if !u.TotpEnabled {
		return gen.MfaRecoveryCodesResponse{}, invalid
	}
	step, ok, err := s.verifyTOTP(u.TotpSecret, twoFactorCode(codeOf(body)), s.deps.Clock())
	if err != nil {
		return gen.MfaRecoveryCodesResponse{}, err
	}
	if !ok {
		return gen.MfaRecoveryCodesResponse{}, invalid
	}
	var codes []string
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		spent, err := q.RecordTOTPStep(ctx, store.RecordTOTPStepParams{Step: step, ID: u.ID, TotpSecret: u.TotpSecret})
		if err != nil {
			return err
		}
		if spent == 0 {
			return invalid
		}
		codes, err = replaceRecoveryCodes(ctx, q, u.ID)
		return err
	})
	if err != nil {
		return gen.MfaRecoveryCodesResponse{}, err
	}
	return gen.MfaRecoveryCodesResponse{RecoveryCodes: codes}, nil
}

// PostIdentityOwnerMfaResetByUserId resets another Owner's MFA
// (EA/AuthAccountEndpoints.cs:328-383). The router has admitted an Owner;
// the caller's session must also have verified a second factor, whatever
// OWNERS_REQUIRE_MFA says (403 forbidden). Their own account is refused
// (400 invalid_request), and a user who is not an Owner, or none, is the
// bare 404. The target's secret is replaced by a new one nobody holds, TOTP
// is off, and a fresh set of recovery codes is returned to the caller to
// hand over; the target enrols again before TOTP is back on. As .NET
// rotated the target's security stamp, every session of theirs ends and
// their reset links are spent.
func (s *server) PostIdentityOwnerMfaResetByUserId(ctx context.Context, req gen.PostIdentityOwnerMfaResetByUserIdRequestObject) (gen.PostIdentityOwnerMfaResetByUserIdResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(p.Roles, RoleOwner) || !p.MFAVerified {
		return gen.PostIdentityOwnerMfaResetByUserId403JSONResponse(authErrorBody("forbidden", ownerMFAResetForbiddenMessage, nil)), nil
	}
	if p.UserID == req.UserId {
		return gen.PostIdentityOwnerMfaResetByUserId400JSONResponse(authErrorBody("invalid_request", selfMFAResetMessage, nil)), nil
	}
	sealed, err := s.sealTOTPSecret(newTOTPSecret())
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var codes []string
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		owner, err := q.IsOwner(ctx, req.UserId)
		if err != nil {
			return err
		}
		if !owner {
			return notFound
		}
		if err := q.ResetTOTP(ctx, store.ResetTOTPParams{TotpSecret: sealed, Version: uuid.New(), Now: now, ID: req.UserId}); err != nil {
			return err
		}
		if codes, err = replaceRecoveryCodes(ctx, q, req.UserId); err != nil {
			return err
		}
		return s.access.rotateSecurityStamp(ctx, q, req.UserId, uuid.Nil)
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerMfaResetByUserIdResponseObject](err)
	}
	return gen.PostIdentityOwnerMfaResetByUserId200JSONResponse{UserId: req.UserId, RecoveryCodes: codes}, nil
}

// requireLocalPassword is .NET's RequireLocalPassword
// (EA/AuthAccountEndpoints.cs:385-399): 409 local_password_unavailable for
// an account without a local password (OIDC-only), 400
// reauthentication_required for a missing or wrong one. A wrong password
// here is not a sign-in failure and counts toward no lockout, as .NET's
// CheckPasswordAsync counted none.
func requireLocalPassword(u store.IdentityUser, password string) error {
	if u.PasswordHash == nil {
		return refuse(http.StatusConflict, "local_password_unavailable", localPasswordUnavailableMessage, nil)
	}
	if password == "" {
		return reauthenticationRequired
	}
	ok, err := verifyPassword(*u.PasswordHash, password)
	if err != nil {
		return fmt.Errorf("identity: mfa: %w", err)
	}
	if !ok {
		return reauthenticationRequired
	}
	return nil
}

func passwordOf(body *gen.MfaCodeRequest) string {
	if body == nil {
		return ""
	}
	return deref(body.Password)
}

func codeOf(body *gen.MfaCodeRequest) *string {
	if body == nil {
		return nil
	}
	return body.Code
}

// The refusals MFA management answers with.

func (r refusal) VisitGetIdentityAccountMfaResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitGetIdentityOwnerMfaResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitGetIdentityAccountMfaSetupResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityOwnerMfaSetupResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountMfaSetupResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerMfaSetupResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountMfaEnableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerMfaEnableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountMfaDisableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerMfaDisableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountMfaRecoveryCodesResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerMfaRecoveryCodesResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerMfaResetByUserIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
