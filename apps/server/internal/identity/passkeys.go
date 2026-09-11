package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// Passkey limits, .NET's (EA/AccountSettingsEndpoints.cs:29-34, 390-392,
// 492): passkeys per user, the name and credential id bounds, live
// ceremonies per user (enrolment) and per client address (sign-in), how long
// a ceremony lives, and the longest credential JSON and email accepted.
const (
	maxPasskeys                   = 10
	maxPasskeyNameLength          = 100
	maxCredentialIDChars          = 1364
	maxCredentialIDBytes          = 1023
	maxEnrolmentCeremoniesPerUser = 3
	maxLoginCeremoniesPerAddress  = 20
	passkeyCeremonyLifetime       = 5 * time.Minute
	maxCredentialJSONLength       = 100_000
	maxPasskeyLoginEmailLength    = 256
)

// The kinds of identity.passkey_ceremonies rows.
const (
	ceremonyEnrol = "enroll"
	ceremonyLogin = "login"
)

// Passkey messages, .NET's (EA/AccountSettingsEndpoints.cs:291-749).
const (
	passkeyNameMessage              = "A passkey name is required and must be at most 100 characters."
	passkeyLimitMessage             = "The maximum number of passkeys has been reached."
	passkeyCeremoniesMessage        = "Too many passkey ceremonies are active."
	passkeyResponseInvalidMessage   = "The passkey response is invalid."
	passkeyCeremonyInvalidMessage   = "The passkey ceremony is expired or already used."
	passkeyNotUserVerifiedMessage   = "A user-verified passkey is required."
	passkeyIdentifierInvalidMessage = "The passkey identifier is invalid."
	passkeyEmailRequiredMessage     = "Email is required."
	passkeySignInFailedMessage      = "The passkey sign-in failed."
	passkeyConfigurationMessage     = "Passkey options could not be generated."
)

var (
	passkeyRequestInvalid   = refuse(http.StatusBadRequest, "invalid_request", passkeyResponseInvalidMessage, nil)
	passkeyLimitReached     = refuse(http.StatusConflict, "passkey_limit_reached", passkeyLimitMessage, nil)
	passkeyCeremoniesActive = refuse(http.StatusTooManyRequests, "rate_limited", passkeyCeremoniesMessage, nil)
	passkeyCeremonyInvalid  = refuse(http.StatusConflict, "passkey_ceremony_invalid", passkeyCeremonyInvalidMessage, nil)
	// invalidPasskey refuses an attestation that does not verify, with a
	// deliberately different message from .NET's. ASP.NET's
	// PerformAttestationAsync returns a failed result instead of throwing
	// (Microsoft.AspNetCore.Identity 10.0.12), so the catch at
	// EA/AccountSettingsEndpoints.cs:424 never ran, and :429-431 answered
	// every failure "A user-verified passkey is required.". Go keeps the
	// code and the status, but gives that message (passkeyNotUserVerified)
	// only when the user was not verified, and this one otherwise.
	invalidPasskey         = refuse(http.StatusBadRequest, "invalid_passkey", passkeyResponseInvalidMessage, nil)
	passkeyNotUserVerified = refuse(http.StatusBadRequest, "invalid_passkey", passkeyNotUserVerifiedMessage, nil)
	passkeySignInFailed    = refuse(http.StatusUnauthorized, "invalid_credentials", passkeySignInFailedMessage, nil)
	// passkeysUnconfigured answers every ceremony when APP_URL's host
	// cannot be a relying party ID (newRelyingParty), as .NET answered
	// options it could not generate (:524-527).
	passkeysUnconfigured = refuse(http.StatusInternalServerError, "passkey_configuration", passkeyConfigurationMessage, nil)
)

// newRelyingParty is the installation's WebAuthn relying party, configured
// as .NET configured IdentityPasskeyOptions
// (EA/AuthServiceCollectionExtensions.cs:41-62): the RP ID is APP_URL's host,
// never the request's Host, and the only origin accepted is APP_URL's;
// user verification and a resident (discoverable) key are required; the
// browser gets five minutes. The RP name is the host too, as ASP.NET's
// handler named it after the server domain. Attestation is "none": .NET
// requested none (ASP.NET's default leaves the preference out, which
// browsers treat as none), and nothing verifies authenticator makes.
//
// The library's own expiry is off (Enforce false), because it reads the
// wall clock: a ceremony's row expires on Deps.Clock instead. It fails when
// there is no APP_URL, or its host is not a valid RP ID, an IP address for
// one, which no browser accepts either.
func newRelyingParty(cfg *config.Config) (*webauthn.WebAuthn, error) {
	if cfg.AppHostname == "" || cfg.AppOrigin == "" {
		return nil, errors.New("identity: passkeys: no APP_URL")
	}
	timeout := webauthn.TimeoutConfig{Timeout: passkeyCeremonyLifetime, TimeoutUVD: passkeyCeremonyLifetime}
	return webauthn.New(&webauthn.Config{
		RPID:                  cfg.AppHostname,
		RPDisplayName:         cfg.AppHostname,
		RPOrigins:             []string{cfg.AppOrigin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// passkeyUser is an account as the relying party sees it. Its user handle
// is the 16 bytes of the user id.
type passkeyUser struct {
	id          uuid.UUID
	name        string
	displayName string
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return u.id[:] }
func (u passkeyUser) WebAuthnName() string                       { return u.name }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.displayName }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// GetIdentityAccountPasskeys lists the caller's passkeys, oldest first
// (EA/AccountSettingsEndpoints.cs:291-315).
func (s *server) GetIdentityAccountPasskeys(ctx context.Context, _ gen.GetIdentityAccountPasskeysRequestObject) (gen.GetIdentityAccountPasskeysResponseObject, error) {
	_, u, err := s.mfaCaller(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityAccountPasskeysResponseObject](err)
	}
	rows, err := s.q.ListUserPasskeys(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("identity: passkeys: %w", err)
	}
	out := make(gen.GetIdentityAccountPasskeys200JSONResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, gen.PasskeyResponse{
			CredentialId:     base64.RawURLEncoding.EncodeToString(row.CredentialID),
			Name:             row.Name,
			CreatedAt:        row.CreatedAt,
			Transports:       nonNil(row.Transports),
			IsUserVerified:   row.UserVerified,
			IsBackupEligible: row.BackupEligible,
			IsBackedUp:       row.BackedUp,
		})
	}
	return out, nil
}

// PostIdentityAccountPasskeysBegin starts enrolling a passkey for the caller
// (EA/AccountSettingsEndpoints.cs:317-379). In .NET's order:
//
//  1. A name that is blank, longer than 100 UTF-16 units once trimmed, or
//     holds a control character: 400 invalid_request.
//  2. The current password, as MFA management requires it.
//  3. Ten passkeys already: 409 passkey_limit_reached.
//  4. Spent ceremonies are purged; three live enrolment ceremonies already:
//     429 rate_limited.
//  5. A ceremony row keeps the session data (the challenge) for five
//     minutes, and the answer is its id and the creation options, which
//     exclude the caller's existing credentials.
//
// 3 to 5 run under the caller's ceremony lock, so concurrent begins cannot
// overshoot either cap.
func (s *server) PostIdentityAccountPasskeysBegin(ctx context.Context, req gen.PostIdentityAccountPasskeysBeginRequestObject) (gen.PostIdentityAccountPasskeysBeginResponseObject, error) {
	var rawName *string
	var password string
	if req.Body != nil {
		rawName, password = req.Body.Name, deref(req.Body.CurrentPassword)
	}
	name, ok := passkeyName(rawName)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", passkeyNameMessage, nil), nil
	}
	_, u, err := s.mfaCaller(ctx)
	if err == nil {
		err = requireLocalPassword(u, password)
	}
	if err == nil && s.relyingParty == nil {
		err = passkeysUnconfigured
	}
	if err != nil {
		return refusalOr[gen.PostIdentityAccountPasskeysBeginResponseObject](err)
	}

	now := s.deps.Clock()
	var answer gen.PasskeyOptionsResponse
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockPasskeyCeremonies(ctx, enrolmentScope(u.ID)); err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		existing, err := q.ListUserPasskeys(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		if len(existing) >= maxPasskeys {
			return passkeyLimitReached
		}
		if err := q.DeleteSpentPasskeyCeremonies(ctx, now); err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		live, err := q.CountLiveEnrolmentCeremonies(ctx, store.CountLiveEnrolmentCeremoniesParams{UserID: &u.ID, Now: now})
		if err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		if live >= maxEnrolmentCeremoniesPerUser {
			return passkeyCeremoniesActive
		}

		exclude := make([]protocol.CredentialDescriptor, 0, len(existing))
		for _, p := range existing {
			exclude = append(exclude, protocol.CredentialDescriptor{
				Type:         protocol.PublicKeyCredentialType,
				CredentialID: p.CredentialID,
				Transport:    transports(p.Transports),
			})
		}
		user := passkeyUser{id: u.ID, name: u.Email, displayName: u.DisplayName}
		creation, session, err := s.relyingParty.BeginRegistration(user, webauthn.WithExclusions(exclude))
		if err != nil {
			return fmt.Errorf("identity: passkey enrolment options: %w", err)
		}
		id, err := s.insertCeremony(ctx, q, store.InsertPasskeyCeremonyParams{
			UserID:         &u.ID,
			Kind:           ceremonyEnrol,
			CredentialName: &name,
			ExpiresAt:      now.Add(passkeyCeremonyLifetime),
		}, session)
		if err != nil {
			return err
		}
		answer = gen.PasskeyOptionsResponse{CeremonyId: id, Options: creation.Response}
		return nil
	})
	if err != nil {
		return refusalOr[gen.PostIdentityAccountPasskeysBeginResponseObject](err)
	}
	return gen.PostIdentityAccountPasskeysBegin200JSONResponse(answer), nil
}

// PostIdentityAccountPasskeysComplete finishes an enrolment
// (EA/AccountSettingsEndpoints.cs:381-447). In .NET's order:
//
//  1. No ceremony id, or a credential JSON that is blank or longer than
//     100,000 characters: 400 invalid_request.
//  2. The current password.
//  3. The caller's ceremony is consumed, whatever follows: one that is
//     unknown, another user's, expired or already used is 409
//     passkey_ceremony_invalid.
//  4. The attestation must verify against the ceremony's challenge, the
//     RP ID and APP_URL's origin: else 400 invalid_passkey. One whose
//     authenticator did not verify the user is 400 invalid_passkey too,
//     with its own message (see invalidPasskey for how the messages differ
//     from .NET's).
//  5. Ten passkeys meanwhile: 409 passkey_limit_reached. A credential id
//     already registered, to anyone: 400 invalid_passkey (ASP.NET's
//     CredentialAlreadyRegistered, a failed attestation result, :429-431).
//  6. The passkey is stored under the ceremony's name, as go-webauthn's
//     Credential with its flags and transports.
//
// Adding a passkey did not rotate .NET's security stamp: ASP.NET's
// AddOrUpdatePasskeyAsync stores the passkey and calls UpdateUserAsync, which
// only validates and saves the user (Microsoft.Extensions.Identity.Core
// 10.0.12, UserManager.AddOrUpdatePasskeyCoreAsync; SetTwoFactorEnabledAsync,
// by contrast, calls UpdateSecurityStampInternal). So no session ends and no
// reset link is spent. The save did regenerate the concurrency stamp, so
// the user's version rotates.
func (s *server) PostIdentityAccountPasskeysComplete(ctx context.Context, req gen.PostIdentityAccountPasskeysCompleteRequestObject) (gen.PostIdentityAccountPasskeysCompleteResponseObject, error) {
	credentialJSON, ok := passkeyResponse(req.Body)
	if !ok {
		return passkeyRequestInvalid, nil
	}
	_, u, err := s.mfaCaller(ctx)
	if err == nil {
		err = requireLocalPassword(u, deref(req.Body.CurrentPassword))
	}
	if err == nil && s.relyingParty == nil {
		err = passkeysUnconfigured
	}
	if err != nil {
		return refusalOr[gen.PostIdentityAccountPasskeysCompleteResponseObject](err)
	}

	now := s.deps.Clock()
	ceremony, err := s.q.ConsumeEnrolmentCeremony(ctx, store.ConsumeEnrolmentCeremonyParams{Now: now, ID: req.Body.CeremonyId, UserID: &u.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeyCeremonyInvalid, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: passkey enrolment: %w", err)
	}
	session, err := ceremonySession(ceremony.SessionData)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes([]byte(credentialJSON))
	if err != nil {
		return invalidPasskey, nil
	}
	if !parsed.Response.AttestationObject.AuthData.Flags.HasUserVerified() {
		return passkeyNotUserVerified, nil
	}
	user := passkeyUser{id: u.ID, name: u.Email, displayName: u.DisplayName}
	credential, err := s.relyingParty.CreateCredential(user, session, parsed)
	if err != nil {
		return invalidPasskey, nil
	}
	if !credential.Flags.UserVerified {
		return passkeyNotUserVerified, nil
	}
	stored, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("identity: passkey enrolment: %w", err)
	}
	name := "Passkey"
	if ceremony.CredentialName != nil {
		name = *ceremony.CredentialName
	}

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockPasskeyCeremonies(ctx, enrolmentScope(u.ID)); err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		n, err := q.CountUserPasskeys(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		if n >= maxPasskeys {
			return passkeyLimitReached
		}
		inserted, err := q.InsertPasskey(ctx, store.InsertPasskeyParams{
			CredentialID:   credential.ID,
			UserID:         u.ID,
			Name:           name,
			Credential:     stored,
			UserVerified:   credential.Flags.UserVerified,
			BackupEligible: credential.Flags.BackupEligible,
			BackedUp:       credential.Flags.BackupState,
			Transports:     transportNames(credential.Transport),
			Now:            now,
		})
		if err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		if inserted == 0 {
			return invalidPasskey
		}
		if err := q.RotateUserVersion(ctx, store.RotateUserVersionParams{Version: uuid.New(), Now: now, ID: u.ID}); err != nil {
			return fmt.Errorf("identity: passkey enrolment: %w", err)
		}
		return nil
	})
	if err != nil {
		return refusalOr[gen.PostIdentityAccountPasskeysCompleteResponseObject](err)
	}
	return gen.PostIdentityAccountPasskeysComplete200JSONResponse{Success: true}, nil
}

// DeleteIdentityAccountPasskeysByCredentialId removes one of the caller's
// passkeys (EA/AccountSettingsEndpoints.cs:449-482): the current password,
// then a credential id that is not unpadded base64url of 1 to 1023 bytes (at
// most 1364 characters) is 400 invalid_request, and one the caller does not
// hold, another user's included, is the bare 404.
//
// Like adding one, removing a passkey did not rotate .NET's security stamp
// (UserManager.RemovePasskeyCoreAsync: the store's RemovePasskeyAsync, then
// UpdateUserAsync), so no session ends and no reset link is spent; the
// version rotates.
func (s *server) DeleteIdentityAccountPasskeysByCredentialId(ctx context.Context, req gen.DeleteIdentityAccountPasskeysByCredentialIdRequestObject) (gen.DeleteIdentityAccountPasskeysByCredentialIdResponseObject, error) {
	var password string
	if req.Body != nil {
		password = deref(req.Body.CurrentPassword)
	}
	_, u, err := s.mfaCaller(ctx)
	if err == nil {
		err = requireLocalPassword(u, password)
	}
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccountPasskeysByCredentialIdResponseObject](err)
	}
	credentialID, ok := decodeCredentialID(req.CredentialId)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", passkeyIdentifierInvalidMessage, nil), nil
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		removed, err := q.DeletePasskey(ctx, store.DeletePasskeyParams{UserID: u.ID, CredentialID: credentialID})
		if err != nil {
			return fmt.Errorf("identity: passkey removal: %w", err)
		}
		if removed == 0 {
			return notFound
		}
		if err := q.RotateUserVersion(ctx, store.RotateUserVersionParams{Version: uuid.New(), Now: now, ID: u.ID}); err != nil {
			return fmt.Errorf("identity: passkey removal: %w", err)
		}
		return nil
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccountPasskeysByCredentialIdResponseObject](err)
	}
	return gen.DeleteIdentityAccountPasskeysByCredentialId204Response{}, nil
}

// PostIdentityPasskeysLoginBegin starts a passkey sign-in
// (EA/AccountSettingsEndpoints.cs:484-542):
//
//  1. A blank email, or one longer than 256 UTF-16 units: 400
//     invalid_request.
//  2. Spent ceremonies are purged; twenty live sign-in ceremonies from the
//     client's address already: 429 rate_limited.
//  3. The answer never tells whether the email names an account that can
//     sign in with a passkey. Every email takes the same path: one lookup
//     (GetPasskeyLoginCandidate), a discoverable-credential ceremony
//     (allowCredentials left out), and a ceremony row. Only the row records
//     the account found, and holds no account for an email that is unknown,
//     disabled, locked out or without a passkey, so that ceremony cannot
//     complete. .NET built the same options for a placeholder user and
//     stripped allowCredentials (:507-522, 723-743).
//
// 2 and 3 run under the address's ceremony lock, so concurrent begins
// cannot overshoot the cap.
func (s *server) PostIdentityPasskeysLoginBegin(ctx context.Context, req gen.PostIdentityPasskeysLoginBeginRequestObject) (gen.PostIdentityPasskeysLoginBeginResponseObject, error) {
	var email string
	if req.Body != nil {
		email = deref(req.Body.Email)
	}
	if strings.TrimSpace(email) == "" || utf16Length(email) > maxPasskeyLoginEmailLength {
		return refuse(http.StatusBadRequest, "invalid_request", passkeyEmailRequiredMessage, nil), nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if s.relyingParty == nil {
		return passkeysUnconfigured, nil
	}
	now := s.deps.Clock()
	clientIP := httpx.ClientIP(r)
	if err := s.q.DeleteSpentPasskeyCeremonies(ctx, now); err != nil {
		return nil, fmt.Errorf("identity: passkey sign-in: %w", err)
	}

	var answer gen.PasskeyOptionsResponse
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockPasskeyCeremonies(ctx, "passkey-login|"+clientIP); err != nil {
			return fmt.Errorf("identity: passkey sign-in: %w", err)
		}
		live, err := q.CountLiveLoginCeremonies(ctx, store.CountLiveLoginCeremoniesParams{ClientIp: &clientIP, Now: now})
		if err != nil {
			return fmt.Errorf("identity: passkey sign-in: %w", err)
		}
		if live >= maxLoginCeremoniesPerAddress {
			return passkeyCeremoniesActive
		}
		candidate, err := q.GetPasskeyLoginCandidate(ctx, store.GetPasskeyLoginCandidateParams{
			NormalizedEmail: normalizeEmail(email),
			Now:             now,
			ScimEnabled:     s.deps.Config.SCIM != nil,
		})
		if err != nil {
			return fmt.Errorf("identity: passkey sign-in: %w", err)
		}
		assertion, session, err := s.relyingParty.BeginDiscoverableLogin()
		if err != nil {
			return fmt.Errorf("identity: passkey sign-in options: %w", err)
		}
		id, err := s.insertCeremony(ctx, q, store.InsertPasskeyCeremonyParams{
			UserID:    candidate,
			Kind:      ceremonyLogin,
			ClientIp:  &clientIP,
			ExpiresAt: now.Add(passkeyCeremonyLifetime),
		}, session)
		if err != nil {
			return err
		}
		answer = gen.PasskeyOptionsResponse{CeremonyId: id, Options: assertion.Response}
		return nil
	})
	if err != nil {
		return refusalOr[gen.PostIdentityPasskeysLoginBeginResponseObject](err)
	}
	return gen.PostIdentityPasskeysLoginBegin200JSONResponse(answer), nil
}

// errPasskeyUserMismatch refuses an assertion whose user handle is not the
// account its ceremony was begun for.
var errPasskeyUserMismatch = errors.New("identity: the passkey belongs to another account")

// PostIdentityPasskeysLoginComplete finishes a passkey sign-in
// (EA/AccountSettingsEndpoints.cs:544-610):
//
//  1. No ceremony id, or a credential JSON that is blank or longer than
//     100,000 characters: 400 invalid_request.
//  2. The ceremony is consumed, whatever follows: one that is unknown,
//     expired or already used is 409 passkey_ceremony_invalid.
//  3. Every failure after that is the same 401 invalid_credentials: a
//     ceremony begun for no account; an account that is gone, disabled,
//     locked out (checked here too, as .NET did at :568-571) or deactivated
//     by the directory; an assertion that does not parse or verify (its
//     challenge, APP_URL's origin, the RP ID hash, user presence and
//     verification, the signature); a user handle or credential that is not
//     the account's; a signature counter that did not advance, which
//     ASP.NET rejected (PasskeyHandler.PerformAssertionCoreAsync throws
//     SignCountLessThanOrEqualToStoredSignCount, which PerformAssertionAsync
//     returns as a failed result, answered at :588-591) and go-webauthn
//     reports as a clone warning; and a passkey removed while the sign-in
//     ran.
//  4. Success stores the passkey's new counter and flags and starts a
//     non-persistent session that counts as MFA-verified. The failure count
//     is left as it is: .NET signed in with SignInWithClaimsAsync, which
//     resets no lockout (only the password and two-factor sign-ins call
//     ResetLockoutWithResult). A pending two-factor sign-in the browser
//     holds ends, as .NET's SignOutAsync (:599) signed that scheme out.
//
// 3 and 4 run in one transaction that locks the account's row first, so
// two completions for one account take turns: each sees the counter the
// other stored, and a lockout written meanwhile.
func (s *server) PostIdentityPasskeysLoginComplete(ctx context.Context, req gen.PostIdentityPasskeysLoginCompleteRequestObject) (gen.PostIdentityPasskeysLoginCompleteResponseObject, error) {
	credentialJSON, ok := passkeyResponse(req.Body)
	if !ok {
		return passkeyRequestInvalid, nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if s.relyingParty == nil {
		return passkeysUnconfigured, nil
	}
	now := s.deps.Clock()
	ceremony, err := s.q.ConsumeLoginCeremony(ctx, store.ConsumeLoginCeremonyParams{Now: now, ID: req.Body.CeremonyId})
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeyCeremonyInvalid, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: passkey sign-in: %w", err)
	}
	if ceremony.UserID == nil {
		return passkeySignInFailed, nil
	}
	session, err := ceremonySession(ceremony.SessionData)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes([]byte(credentialJSON))
	if err != nil {
		return passkeySignInFailed, nil
	}

	var answer loginOK
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		answer, err = s.verifyPasskeyLogin(ctx, tx, r, *ceremony.UserID, session, parsed, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityPasskeysLoginCompleteResponseObject](err)
	}
	ticket, err := s.discardLoginTicket(ctx, r)
	if err != nil {
		return nil, err
	}
	if ticket != nil {
		answer.cookies = append(answer.cookies, ticket)
	}
	return answer, nil
}

// verifyPasskeyLogin is PostIdentityPasskeysLoginComplete's transaction, 3
// and 4 for the account userID the ceremony was begun for. Every refusal is
// passkeySignInFailed, returned as an error so that nothing is kept.
func (s *server) verifyPasskeyLogin(ctx context.Context, tx pgx.Tx, r *http.Request, userID uuid.UUID, session webauthn.SessionData, parsed *protocol.ParsedCredentialAssertionData, now time.Time) (loginOK, error) {
	q := store.New(tx)
	u, err := q.LockPasskeyLoginUser(ctx, store.LockPasskeyLoginUserParams{ScimEnabled: s.deps.Config.SCIM != nil, ID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return loginOK{}, passkeySignInFailed
	}
	if err != nil {
		return loginOK{}, fmt.Errorf("identity: passkey sign-in: %w", err)
	}
	if u.IsDisabled || u.ScimInactive || isLockedOut(u.LockoutEnd, now) {
		return loginOK{}, passkeySignInFailed
	}
	rows, err := q.ListUserPasskeys(ctx, u.ID)
	if err != nil {
		return loginOK{}, fmt.Errorf("identity: passkey sign-in: %w", err)
	}
	user := passkeyUser{id: u.ID, name: u.Email, displayName: u.DisplayName}
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(row.Credential, &c); err != nil {
			return loginOK{}, fmt.Errorf("identity: passkey sign-in: stored credential: %w", err)
		}
		user.credentials = append(user.credentials, c)
	}
	owner := func(_, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(userHandle, user.WebAuthnID()) {
			return nil, errPasskeyUserMismatch
		}
		return user, nil
	}
	_, credential, err := s.relyingParty.ValidatePasskeyLogin(owner, session, parsed)
	if err != nil || !parsed.Response.AuthenticatorData.Flags.HasUserVerified() || credential.Authenticator.CloneWarning {
		return loginOK{}, passkeySignInFailed
	}
	stored, err := json.Marshal(credential)
	if err != nil {
		return loginOK{}, fmt.Errorf("identity: passkey sign-in: %w", err)
	}
	used, err := q.RecordPasskeyUse(ctx, store.RecordPasskeyUseParams{
		Credential:   stored,
		UserVerified: credential.Flags.UserVerified,
		BackedUp:     credential.Flags.BackupState,
		Now:          now,
		CredentialID: credential.ID,
		UserID:       u.ID,
	})
	if err != nil {
		return loginOK{}, fmt.Errorf("identity: passkey sign-in: %w", err)
	}
	if used == 0 {
		// Removed since ListUserPasskeys read it: DeletePasskey takes no
		// account lock, so a removal can commit in between, and a removed
		// passkey signs no one in.
		return loginOK{}, passkeySignInFailed
	}
	token, err := s.access.createSession(ctx, tx, u.ID, false, true, r)
	if err != nil {
		return loginOK{}, err
	}
	roles := orderRoles(u.RoleNames)
	authed := authUser(u.ID, u.DisplayName, u.Email, roles)
	return loginOK{
		cookies: cookies{s.access.newSessionCookie(token, false)},
		body: authSuccessBody{AuthSuccessResponse: gen.AuthSuccessResponse{
			User:              &authed,
			RequiresTwoFactor: false,
			TwoFactorEnabled:  u.TotpEnabled,
			// .NET's literal (:607-609): the passkey was the second factor.
			MfaEnrollmentRequired: false,
			Tenants:               noTenants(),
		}},
	}, nil
}

// insertCeremony stores a ceremony row for session, with a new id, which it
// returns: the handle the client completes the ceremony with, as the
// contract's ceremonyId.
func (s *server) insertCeremony(ctx context.Context, q *store.Queries, row store.InsertPasskeyCeremonyParams, session *webauthn.SessionData) (uuid.UUID, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: passkey ceremony: %w", err)
	}
	row.ID, row.SessionData = uuid.New(), data
	if err := q.InsertPasskeyCeremony(ctx, row); err != nil {
		return uuid.Nil, fmt.Errorf("identity: passkey ceremony: %w", err)
	}
	return row.ID, nil
}

// ceremonySession decodes a ceremony row's session data.
func ceremonySession(data []byte) (webauthn.SessionData, error) {
	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return session, fmt.Errorf("identity: passkey ceremony session: %w", err)
	}
	return session, nil
}

// enrolmentScope is the ceremony lock of userID's enrolments: its live
// ceremonies and its passkey count.
func enrolmentScope(userID uuid.UUID) string {
	return "passkey-enroll|" + userID.String()
}

// passkeyName is an enrolment's passkey name, trimmed, as .NET's
// ValidatePasskeyName (EA/AccountSettingsEndpoints.cs:745-755) accepts one:
// not blank, at most 100 UTF-16 units once trimmed, and without a control
// character anywhere.
func passkeyName(v *string) (string, bool) {
	if v == nil || strings.ContainsFunc(*v, unicode.IsControl) {
		return "", false
	}
	name := strings.TrimSpace(*v)
	if name == "" || utf16Length(name) > maxPasskeyNameLength {
		return "", false
	}
	return name, true
}

// passkeyResponse is a completion's credential JSON, when the request names
// a ceremony and carries one of acceptable size (:390-394, 553-557).
func passkeyResponse(body *gen.PasskeyCompleteRequest) (string, bool) {
	if body == nil || body.CeremonyId == uuid.Nil {
		return "", false
	}
	v := deref(body.CredentialJson)
	if strings.TrimSpace(v) == "" || utf16Length(v) > maxCredentialJSONLength {
		return "", false
	}
	return v, true
}

// decodeCredentialID is a credential id from a path, as .NET's
// TryDecodeCredentialId bounds it (EA/AccountSettingsEndpoints.cs:757-774):
// at most 1364 characters of unpadded base64url, decoding to 1 to 1023
// bytes.
func decodeCredentialID(v string) ([]byte, bool) {
	if strings.TrimSpace(v) == "" || len(v) > maxCredentialIDChars {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || len(b) == 0 || len(b) > maxCredentialIDBytes {
		return nil, false
	}
	return b, true
}

func transports(names []string) []protocol.AuthenticatorTransport {
	out := make([]protocol.AuthenticatorTransport, len(names))
	for i, n := range names {
		out[i] = protocol.AuthenticatorTransport(n)
	}
	return out
}

func transportNames(ts []protocol.AuthenticatorTransport) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// The refusals the passkey operations answer with, and sign-in's 200.

func (r refusal) VisitGetIdentityAccountPasskeysResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountPasskeysBeginResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccountPasskeysCompleteResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityAccountPasskeysByCredentialIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityPasskeysLoginBeginResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityPasskeysLoginCompleteResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r loginOK) VisitPostIdentityPasskeysLoginCompleteResponse(w http.ResponseWriter) error {
	return r.VisitPostIdentityLoginResponse(w)
}
