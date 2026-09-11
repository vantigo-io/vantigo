package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

const (
	accountMFAPath   = "/api/v1/identity/account/mfa"
	ownerMFAPath     = "/api/v1/identity/owner/mfa"
	systemStatusPath = "/api/v1/identity/owner/system-status"
)

// recoveryCodeShape is a recovery code as identity issues them: two groups
// of five characters from the unambiguous alphabet.
var recoveryCodeShape = regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{5}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{5}$`)

// mfaStatus is MfaStatusResponse as a test reads it.
type mfaStatus struct {
	TwoFactorEnabled      bool `json:"twoFactorEnabled"`
	MfaEnrollmentRequired bool `json:"mfaEnrollmentRequired"`
}

// mfaSetup is MfaSetupResponse as a test reads it.
type mfaSetup struct {
	SharedKey        *string `json:"sharedKey"`
	AuthenticatorURI *string `json:"authenticatorUri"`
	Initialized      bool    `json:"initialized"`
}

// mfaCodes is MfaEnableResponse, MfaRecoveryCodesResponse and
// MfaResetResponse as a test reads them.
type mfaCodes struct {
	TwoFactorEnabled bool      `json:"twoFactorEnabled"`
	RecoveryCodes    []string  `json:"recoveryCodes"`
	UserID           uuid.UUID `json:"userId"`
}

// sessionMFA reports whether c's session counts as MFA-verified, as GET
// /session says.
func sessionMFA(t *testing.T, c *client) bool {
	t.Helper()
	r := c.do(http.MethodGet, sessionPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d", sessionPath, r.status)
	}
	var s struct {
		MfaAuthenticated bool `json:"mfaAuthenticated"`
	}
	r.json(&s)
	return s.MfaAuthenticated
}

// recoveryHash is what identity stores for a recovery code.
func recoveryHash(code string) []byte {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.ReplaceAll(code, "-", ""))))
	return sum[:]
}

// checkRecoveryCodes fails t unless codes is a fresh set of ten distinct
// recovery codes and exactly they are userID's stored codes.
func checkRecoveryCodes(t *testing.T, h *harness, userID uuid.UUID, codes []string) {
	t.Helper()
	if len(codes) != 10 {
		t.Fatalf("%d recovery codes %v, want 10", len(codes), codes)
	}
	hashes := make([][]byte, len(codes))
	for i, code := range codes {
		if !recoveryCodeShape.MatchString(code) {
			t.Errorf("recovery code %q is not XXXXX-XXXXX from the unambiguous alphabet", code)
		}
		hashes[i] = recoveryHash(code)
	}
	if sorted := slices.Sorted(slices.Values(codes)); len(slices.Compact(sorted)) != 10 {
		t.Errorf("recovery codes %v are not distinct", codes)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, userID); n != 10 {
		t.Errorf("%d stored recovery codes, want 10", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1 AND code_hash = ANY($2)`, userID, hashes); n != 10 {
		t.Errorf("%d stored hashes are SHA-256 of the issued codes (upper case, no dash), want 10", n)
	}
}

// totpState is identity.users' TOTP columns for one user.
type totpState struct {
	secret   []byte
	enabled  bool
	lastStep *int64
}

func readTOTP(t *testing.T, h *harness, userID uuid.UUID) totpState {
	t.Helper()
	var s totpState
	if err := h.pool.QueryRow(context.Background(), `SELECT totp_secret, totp_enabled, totp_last_step FROM identity.users WHERE id = $1`, userID).
		Scan(&s.secret, &s.enabled, &s.lastStep); err != nil {
		t.Fatalf("totp state: %v", err)
	}
	return s
}

// TestHarnessTOTP_MatchesRFC6238 proves the harness's own generator against
// RFC 6238's SHA-1 test vectors (Appendix B, the last six digits), so the
// tests check identity against an independent implementation.
func TestHarnessTOTP_MatchesRFC6238(t *testing.T) {
	secret := base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
	for at, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		if got := totp(secret, time.Unix(at, 0)); got != want {
			t.Errorf("totp at %d = %s, want %s", at, got, want)
		}
	}
}

// Ported from IdentityMfaIntegrationTests.OwnerManagement_RequiresMfaWhenConfigured.
func TestMfa_OwnerManagementRequiresMfaWhenConfigured(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	owner, _ := h.bootstrapOwner(t)

	if r := owner.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("GET %s without MFA: status %d code %q, want 403 forbidden", ownerUsersPath, r.status, r.code())
	}
}

// Ported from IdentityMfaIntegrationTests.AuthorizationManagement_RequiresMfaWhenConfigured.
func TestMfa_AuthorizationManagementRequiresMfaWhenConfigured(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	owner, _ := h.bootstrapOwner(t)

	if r := owner.do(http.MethodGet, "/api/v1/identity/access/roles", nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("GET /access/roles without MFA: status %d code %q, want 403 forbidden", r.status, r.code())
	}
}

// Ported from IdentityMfaIntegrationTests.OwnerPolicy_RequiresMfaWhenConfigured.
func TestMfa_OwnerPolicyRequiresMfaWhenConfigured(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "policy-owner@example.test"
	h.seedUser(t, email, userPassword, identity.RoleOwnerID)
	owner := h.login(t, email, userPassword)

	if r := owner.do(http.MethodGet, systemStatusPath, nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("GET %s without MFA: status %d code %q, want 403 forbidden", systemStatusPath, r.status, r.code())
	}
}

// Ported from IdentityMfaIntegrationTests.SystemAdminPolicy_RequiresMfaWhenConfigured.
func TestMfa_SystemAdminPolicyRequiresMfaWhenConfigured(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "policy-admin@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleSystemAdminID)
	admin := h.login(t, email, userPassword)

	path := "/api/v1/identity/system/users/" + id.String() + "/sessions/revoke"
	if r := admin.do(http.MethodPost, path, nil); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("POST %s without MFA: status %d code %q, want 403 forbidden", path, r.status, r.code())
	}
	admitted(t, admin)
}

// Ported from IdentityMfaIntegrationTests.UnenrolledOwnerCanReachMfaEnrollmentAndGainsPrivilegedAccessAfterEnrolling.
// The enrolment goes through the /owner/mfa aliases, as .NET's did.
func TestMfa_UnenrolledOwnerCanReachEnrolmentAndGainsPrivilegedAccessAfterEnrolling(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "enrolling-owner@example.test"
	h.seedUser(t, email, userPassword, identity.RoleOwnerID)
	owner := h.login(t, email, userPassword)

	r := owner.do(http.MethodGet, ownerMFAPath, nil)
	var status mfaStatus
	r.json(&status)
	if r.status != http.StatusOK || status != (mfaStatus{TwoFactorEnabled: false, MfaEnrollmentRequired: true}) {
		t.Fatalf("status before enrolment: %d %s", r.status, r.body)
	}
	for _, path := range []string{systemStatusPath, ownerUsersPath} {
		if r := owner.do(http.MethodGet, path, nil); r.status != http.StatusForbidden {
			t.Errorf("GET %s before enrolment: status %d, want 403", path, r.status)
		}
	}

	r = owner.do(http.MethodPost, ownerMFAPath+"/setup", map[string]any{"password": userPassword})
	var setup mfaSetup
	r.json(&setup)
	if r.status != http.StatusOK || setup.SharedKey == nil {
		t.Fatalf("setup: status %d body %s", r.status, r.body)
	}
	r = owner.do(http.MethodPost, ownerMFAPath+"/enable", map[string]any{"code": totp(*setup.SharedKey, h.now()), "password": userPassword})
	if r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}

	r = owner.do(http.MethodGet, ownerMFAPath, nil)
	r.json(&status)
	if status != (mfaStatus{TwoFactorEnabled: true, MfaEnrollmentRequired: false}) {
		t.Errorf("status after enrolment: %s", r.body)
	}
	if r := owner.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusOK {
		t.Errorf("GET %s after enrolment: status %d, want 200", ownerUsersPath, r.status)
	}
	if r := owner.do(http.MethodGet, systemStatusPath, nil); r.status != http.StatusOK {
		t.Errorf("GET %s after enrolment: status %d, want 200", systemStatusPath, r.status)
	}
}

// Ported from IdentityMfaIntegrationTests.OrdinaryAuthenticatedUserCanEnrollMfaAndCrossUserResetRemainsDenied.
// .NET's check that no MFA claim was persisted on the user has no Go
// counterpart (MFA lives on the session row); TestMfa_TotpVerificationIsSessionBound
// ports that property. Added: an Owner whose session has not verified a
// second factor is refused the reset too.
func TestMfa_OrdinaryUserCanEnrolAndCrossUserResetRemainsDenied(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const email = "mfa-delegate@example.test"
	h.createUser(t, owner, email, identity.RoleUser)
	other := h.createUser(t, owner, "other-owner@example.test", identity.RoleOwner)
	c := h.login(t, email, userPassword)

	r := c.do(http.MethodPost, ownerMFAPath+"/setup", map[string]any{"password": userPassword})
	var setup mfaSetup
	r.json(&setup)
	if r.status != http.StatusOK || setup.SharedKey == nil {
		t.Fatalf("setup: status %d body %s", r.status, r.body)
	}
	if r := c.do(http.MethodPost, ownerMFAPath+"/enable", map[string]any{"code": totp(*setup.SharedKey, h.now()), "password": userPassword}); r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}

	if r := c.do(http.MethodPost, ownerMFAPath+"/reset/"+ownerID.String(), map[string]any{}); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("a user's reset of the Owner: status %d code %q, want 403 forbidden", r.status, r.code())
	}
	// The bootstrap Owner's session verified no second factor.
	r = owner.do(http.MethodPost, ownerMFAPath+"/reset/"+other.String(), map[string]any{})
	if r.status != http.StatusForbidden || r.code() != "forbidden" || !strings.Contains(string(r.body), "Only an Owner can reset Owner MFA.") {
		t.Errorf("an Owner's reset without MFA: status %d body %s, want 403 forbidden", r.status, r.body)
	}
	for _, id := range []uuid.UUID{ownerID, other} {
		if s := readTOTP(t, h, id); s.secret != nil || s.enabled {
			t.Errorf("a refused reset changed Owner %s's TOTP state", id)
		}
	}
}

// Ported from IdentityMfaIntegrationTests.AccountMfaAliasesRequirePasswordAndDoNotPersistMfaClaims,
// extended to every operation that takes the password, on both aliases.
// .NET's persisted-claim check is ported by TestMfa_TotpVerificationIsSessionBound.
func TestMfa_AccountAliasesRequireThePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	if r := owner.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": nil}); r.status != http.StatusBadRequest || r.code() != "reauthentication_required" {
		t.Errorf("setup without a password: status %d code %q, want 400 reauthentication_required", r.status, r.code())
	}
	for _, base := range []string{accountMFAPath, ownerMFAPath} {
		for _, op := range []string{"/setup", "/enable", "/disable", "/recovery-codes"} {
			// nil was sent above; each alias and operation gets a blank and a
			// wrong password (17 requests, inside the Mfa limit of 20).
			for _, password := range []any{"", "WrongPassword123"} {
				r := owner.do(http.MethodPost, base+op, map[string]any{"password": password, "code": "123456"})
				if r.status != http.StatusBadRequest || r.code() != "reauthentication_required" ||
					!strings.Contains(string(r.body), "The current password is invalid.") {
					t.Errorf("POST %s%s with password %v: status %d body %s, want 400 reauthentication_required", base, op, password, r.status, r.body)
				}
			}
		}
	}
	if s := readTOTP(t, h, ownerID); s.secret != nil || s.enabled {
		t.Errorf("refused requests changed the Owner's TOTP state")
	}
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, ownerID); n != 0 {
		t.Errorf("failed_login_count = %d: a wrong password here is not a sign-in failure", n)
	}

	const email = "alias-user@example.test"
	h.createUser(t, owner, email, identity.RoleUser)
	c := h.login(t, email, userPassword)
	r := c.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": userPassword})
	var setup mfaSetup
	r.json(&setup)
	if r.status != http.StatusOK || setup.SharedKey == nil {
		t.Fatalf("setup: status %d body %s", r.status, r.body)
	}
	if r := c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": totp(*setup.SharedKey, h.now()), "password": userPassword}); r.status != http.StatusOK {
		t.Errorf("enable: status %d body %s", r.status, r.body)
	}

	// An OIDC-only account has no local password to confirm.
	sso := h.signIn(t, h.insertUser(t, "sso-only@example.test", identity.RoleUserID), false)
	if r := sso.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": "anything"}); r.status != http.StatusConflict || r.code() != "local_password_unavailable" {
		t.Errorf("setup without a local password: status %d code %q, want 409 local_password_unavailable", r.status, r.code())
	}
}

// Ported from IdentityMfaIntegrationTests.TotpLoginMfaClaimIsCookieOnly.
// Go keeps the verification on the session row: the session /login/2fa
// started counts as MFA-verified, and another session of the same user does
// not, which the Owner policy tells apart while owners must use MFA.
func TestMfa_TotpVerificationIsSessionBound(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const email = "session-bound@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleOwnerID)
	c := h.login(t, email, userPassword)
	secret, _ := h.enrollTOTP(t, c, userPassword)
	if r := c.do(http.MethodPost, logoutPath, nil); r.status != http.StatusOK {
		t.Fatalf("logout: status %d", r.status)
	}

	c = h.startTwoFactor(t, email, userPassword)
	r := c.do(http.MethodPost, login2faPath, map[string]any{"code": totp(secret, h.now())})
	var body authSuccess
	r.json(&body)
	if r.status != http.StatusOK || body.MfaEnrollmentRequired || !body.TwoFactorEnabled {
		t.Fatalf("login/2fa: status %d body %s", r.status, r.body)
	}
	if !sessionMFA(t, c) {
		t.Errorf("the two-factor session does not count as MFA-verified")
	}
	if r := c.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusOK {
		t.Errorf("GET %s with the two-factor session: status %d, want 200", ownerUsersPath, r.status)
	}

	other := h.signIn(t, id, false)
	if sessionMFA(t, other) {
		t.Errorf("another session of the same user counts as MFA-verified")
	}
	if r := other.do(http.MethodGet, ownerUsersPath, nil); r.status != http.StatusForbidden {
		t.Errorf("GET %s with the other session: status %d, want 403", ownerUsersPath, r.status)
	}
}

// TestMfa_SetupReturnsTheSecretOnceEncryptsItAndEndsOtherSessions proves
// setup: the status and setup reads before it; the secret and its otpauth
// URI with the configured issuer, both escaped as .NET escaped them; the
// secret stored only sealed under identity/totp; TOTP left as it was; the
// other session ended and the caller's kept; and the reads after it never
// replaying the secret. A second setup while TOTP is on keeps it on,
// replaces the secret, and leaves the caller's session no longer
// MFA-verified, as .NET's reissued cookie dropped its MFA claim.
func TestMfa_SetupReturnsTheSecretOnceEncryptsItAndEndsOtherSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("MFA_ISSUER", "Acme: Corp"))
	owner, _ := h.bootstrapOwner(t)
	const email = "setup+mfa@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	c := h.login(t, email, userPassword)
	other := h.login(t, email, userPassword)

	r := c.do(http.MethodGet, accountMFAPath, nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"mfaEnrollmentRequired":false,"twoFactorEnabled":false}` {
		t.Errorf("status: %d %s", r.status, r.body)
	}
	r = c.do(http.MethodGet, accountMFAPath+"/setup", nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"authenticatorUri":null,"initialized":false,"sharedKey":null}` {
		t.Errorf("setup read before setup: %d %s", r.status, r.body)
	}

	r = c.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": userPassword})
	var setup mfaSetup
	r.json(&setup)
	if r.status != http.StatusOK || setup.SharedKey == nil || setup.AuthenticatorURI == nil || !setup.Initialized {
		t.Fatalf("setup: status %d body %s", r.status, r.body)
	}
	key := *setup.SharedKey
	if raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(key); err != nil || len(raw) != 20 {
		t.Errorf("shared key %q: %d bytes (%v), want 20 random bytes in unpadded base32", key, len(raw), err)
	}
	// The issuer's colon is escaped, so the one raw colon in the label is
	// the separator between issuer and account.
	wantURI := "otpauth://totp/Acme%3A%20Corp:setup%2Bmfa%40example.test?secret=" + key + "&issuer=Acme%3A%20Corp&digits=6"
	if *setup.AuthenticatorURI != wantURI {
		t.Errorf("authenticator URI\n got %s\nwant %s", *setup.AuthenticatorURI, wantURI)
	}
	if label, _, _ := strings.Cut(strings.TrimPrefix(*setup.AuthenticatorURI, "otpauth://totp/"), "?"); strings.Count(label, ":") != 1 || !strings.Contains(label, "%3A") {
		t.Errorf("label %q, want the issuer's colon escaped as %%3A and one raw separator", label)
	}

	s := readTOTP(t, h, id)
	if s.enabled || s.lastStep != nil || bytes.Contains(s.secret, []byte(key)) {
		t.Errorf("stored state enabled=%v lastStep=%v, secret in clear=%v: want the secret sealed and TOTP still off",
			s.enabled, s.lastStep, bytes.Contains(s.secret, []byte(key)))
	}
	box, err := secrets.New(h.cfg.AppSecret)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := box.Open("identity/totp", s.secret); err != nil || string(opened) != key {
		t.Errorf("the stored secret does not open under identity/totp to the shared key: %v", err)
	}
	admitted(t, c)
	rejected(t, other)

	r = c.do(http.MethodGet, ownerMFAPath+"/setup", nil)
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"authenticatorUri":null,"initialized":true,"sharedKey":null}` {
		t.Errorf("setup read after setup: %d %s, want initialized without the secret", r.status, r.body)
	}

	// Enable, then set up again while TOTP is on.
	if r := c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": totp(key, h.now()), "password": userPassword}); r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	if !sessionMFA(t, c) {
		t.Fatalf("enable left the session without MFA")
	}
	r = c.do(http.MethodPost, ownerMFAPath+"/setup", map[string]any{"password": userPassword})
	var again mfaSetup
	r.json(&again)
	if r.status != http.StatusOK || again.SharedKey == nil || *again.SharedKey == key {
		t.Fatalf("second setup: status %d body %s, want a new secret", r.status, r.body)
	}
	if s := readTOTP(t, h, id); !s.enabled || s.lastStep != nil {
		t.Errorf("after the second setup enabled=%v lastStep=%v, want TOTP still on and the old secret's step forgotten", s.enabled, s.lastStep)
	}
	if sessionMFA(t, c) {
		t.Errorf("the caller's session still counts as MFA-verified after its authenticator was replaced")
	}
	// The new secret's current code enables it, though the old secret's step
	// at this time was spent a moment ago.
	if r := c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": totp(*again.SharedKey, h.now()), "password": userPassword}); r.status != http.StatusOK {
		t.Errorf("enable with the new secret: status %d body %s", r.status, r.body)
	}
}

// TestMfa_EnableVerifiesTheCodeAndIssuesRecoveryCodes proves enable: a
// blank or wrong code is 400 invalid_mfa_code and changes nothing; the
// right one turns TOTP on, spends its step, returns ten recovery codes
// stored only as hashes, makes the caller's session MFA-verified, and, as
// .NET's stamp rotation did, ends the user's other session and spends
// their reset link; the same code a second time is refused.
func TestMfa_EnableVerifiesTheCodeAndIssuesRecoveryCodes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "enable@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	c := h.login(t, email, userPassword)

	r := c.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": userPassword})
	var setup mfaSetup
	r.json(&setup)
	key := *setup.SharedKey
	other := h.login(t, email, userPassword) // after setup, which ends other sessions
	requestRecovery(t, h, email)
	token := mailedLink(t, h, email).Query().Get("token")

	for name, code := range map[string]any{"blank": nil, "spaces": "   ", "wrong": totp(key, h.now().Add(-10*time.Minute))} {
		r := c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": code, "password": userPassword})
		if r.status != http.StatusBadRequest || r.code() != "invalid_mfa_code" || !strings.Contains(string(r.body), "The authenticator code is invalid.") {
			t.Errorf("%s code: status %d body %s, want 400 invalid_mfa_code", name, r.status, r.body)
		}
	}
	if s := readTOTP(t, h, id); s.enabled || s.lastStep != nil {
		t.Errorf("a refused enable changed the state: enabled=%v lastStep=%v", s.enabled, s.lastStep)
	}

	code := totp(key, h.now())
	// A code typed with a space, as authenticator apps show it, is accepted.
	r = c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": code[:3] + " " + code[3:], "password": userPassword})
	var enabled mfaCodes
	r.json(&enabled)
	if r.status != http.StatusOK || !enabled.TwoFactorEnabled {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	checkRecoveryCodes(t, h, id, enabled.RecoveryCodes)
	if s := readTOTP(t, h, id); !s.enabled || s.lastStep == nil || *s.lastStep != h.now().Unix()/30 {
		t.Errorf("after enable enabled=%v lastStep=%v, want on with step %d spent", s.enabled, s.lastStep, h.now().Unix()/30)
	}
	if !sessionMFA(t, c) {
		t.Errorf("the enabling session does not count as MFA-verified")
	}
	// Enabling rotated .NET's security stamp: the other session ends and the
	// reset link dies with it, while the caller's session carries on.
	rejected(t, other)
	if r := reset(h, t, email, token, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("the reset link after enable: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}

	if r := c.do(http.MethodPost, accountMFAPath+"/enable", map[string]any{"code": code, "password": userPassword}); r.status != http.StatusBadRequest || r.code() != "invalid_mfa_code" {
		t.Errorf("the spent code again: status %d code %q, want 400 invalid_mfa_code", r.status, r.code())
	}
}

// TestMfa_TotpAcceptsOneStepEitherSideAndOnlyLaterSteps proves the window:
// a code one step old or one step ahead is accepted, two steps either way
// is not, and a code for a step not later than the last one spent is
// refused even inside the window.
func TestMfa_TotpAcceptsOneStepEitherSideAndOnlyLaterSteps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "window@example.test"
	h.createUser(t, owner, email, identity.RoleUser)
	c := h.login(t, email, userPassword)
	secret, _ := h.enrollTOTP(t, c, userPassword) // the clock is one step past the one enable spent
	base := h.now().Add(-totpStep)
	h.advance(2 * totpStep) // the current step is base's + 3

	regenerate := func(stepsAfterBase int) *resp {
		return c.do(http.MethodPost, ownerMFAPath+"/recovery-codes", map[string]any{
			"code": totp(secret, base.Add(time.Duration(stepsAfterBase)*totpStep)), "password": userPassword,
		})
	}
	for _, tc := range []struct {
		name  string
		steps int
		want  int
	}{
		{"two steps old", 1, http.StatusBadRequest},
		{"one step old", 2, http.StatusOK},
		{"one step ahead", 4, http.StatusOK},
		{"the current step, earlier than the one spent", 3, http.StatusBadRequest},
		{"two steps ahead", 5, http.StatusBadRequest},
	} {
		if r := regenerate(tc.steps); r.status != tc.want {
			t.Errorf("%s: status %d body %s, want %d", tc.name, r.status, r.body, tc.want)
		}
	}
}

// TestMfa_DisableIsRefusedToOwnersWhileRequiredAndOtherwiseForgetsTheSecret
// proves disable: while owners must use MFA an Owner gets 403 mfa_required
// on both aliases and stays enrolled; anyone else needs the password, and
// then the secret, the recovery codes and TOTP are gone, the other sessions
// end, the caller's session keeps going without MFA, and the next sign-in
// asks for no second factor.
func TestMfa_DisableIsRefusedToOwnersWhileRequiredAndOtherwiseForgetsTheSecret(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	const ownerEmail = "required-owner@example.test"
	ownerID := h.seedUser(t, ownerEmail, userPassword, identity.RoleOwnerID)
	owner := h.login(t, ownerEmail, userPassword)
	h.enrollTOTP(t, owner, userPassword)
	for _, base := range []string{accountMFAPath, ownerMFAPath} {
		r := owner.do(http.MethodPost, base+"/disable", map[string]any{"password": userPassword})
		if r.status != http.StatusForbidden || r.code() != "mfa_required" ||
			!strings.Contains(string(r.body), "MFA cannot be disabled while privileged management access requires it.") {
			t.Errorf("an Owner's %s/disable: status %d body %s, want 403 mfa_required", base, r.status, r.body)
		}
	}
	if !readTOTP(t, h, ownerID).enabled {
		t.Errorf("a refused disable turned the Owner's TOTP off")
	}

	const email = "disabling-user@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)
	h.enrollTOTP(t, c, userPassword)
	other := h.signIn(t, id, true)

	if r := c.do(http.MethodPost, ownerMFAPath+"/disable", map[string]any{"password": "WrongPassword123"}); r.status != http.StatusBadRequest || r.code() != "reauthentication_required" {
		t.Errorf("disable with a wrong password: status %d code %q, want 400 reauthentication_required", r.status, r.code())
	}
	r := c.do(http.MethodPost, ownerMFAPath+"/disable", map[string]any{"password": userPassword})
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"mfaEnrollmentRequired":false,"twoFactorEnabled":false}` {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	if s := readTOTP(t, h, id); s.secret != nil || s.enabled || s.lastStep != nil {
		t.Errorf("after disable secret=%v enabled=%v lastStep=%v, want all cleared", s.secret != nil, s.enabled, s.lastStep)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE user_id = $1`, id); n != 0 {
		t.Errorf("%d recovery codes left, want 0", n)
	}
	if sessionMFA(t, c) {
		t.Errorf("the caller's session still counts as MFA-verified")
	}
	rejected(t, other)

	r = h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword))
	var body authSuccess
	r.json(&body)
	if r.status != http.StatusOK || body.RequiresTwoFactor || body.User == nil {
		t.Errorf("sign-in after disable: status %d body %s, want a session without a second factor", r.status, r.body)
	}
}

// TestMfa_RecoveryCodeRegenerationNeedsACurrentCode proves regeneration:
// without TOTP on, or with a wrong code, 400 invalid_mfa_code; with the
// password and a current code a fresh set replaces the old, so an old code
// no longer signs in and a new one does.
func TestMfa_RecoveryCodeRegenerationNeedsACurrentCode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "regenerate@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	h.createUser(t, owner, "not-enrolled@example.test", identity.RoleUser)

	unenrolled := h.login(t, "not-enrolled@example.test", userPassword)
	r := unenrolled.do(http.MethodPost, accountMFAPath+"/recovery-codes", map[string]any{"code": "123456", "password": userPassword})
	if r.status != http.StatusBadRequest || r.code() != "invalid_mfa_code" || !strings.Contains(string(r.body), "A valid authenticator code is required.") {
		t.Errorf("without TOTP on: status %d body %s, want 400 invalid_mfa_code", r.status, r.body)
	}

	c := h.login(t, email, userPassword)
	secret, old := h.enrollTOTP(t, c, userPassword)
	if r := c.do(http.MethodPost, accountMFAPath+"/recovery-codes", map[string]any{"code": totp(secret, h.now().Add(-10*time.Minute)), "password": userPassword}); r.status != http.StatusBadRequest || r.code() != "invalid_mfa_code" {
		t.Errorf("a wrong code: status %d code %q, want 400 invalid_mfa_code", r.status, r.code())
	}
	checkRecoveryCodes(t, h, id, old)

	r = c.do(http.MethodPost, accountMFAPath+"/recovery-codes", map[string]any{"code": totp(secret, h.now()), "password": userPassword})
	var fresh mfaCodes
	r.json(&fresh)
	if r.status != http.StatusOK {
		t.Fatalf("regenerate: status %d body %s", r.status, r.body)
	}
	checkRecoveryCodes(t, h, id, fresh.RecoveryCodes)
	for _, code := range old {
		if slices.Contains(fresh.RecoveryCodes, code) {
			t.Errorf("the new set repeats the old code %s", code)
		}
	}

	if r := h.startTwoFactor(t, email, userPassword).do(http.MethodPost, login2faPath, map[string]any{"code": old[0]}); r.status != http.StatusUnauthorized || r.code() != "invalid_two_factor_code" {
		t.Errorf("an old recovery code: status %d code %q, want 401 invalid_two_factor_code", r.status, r.code())
	}
	if r := h.startTwoFactor(t, email, userPassword).do(http.MethodPost, login2faPath, map[string]any{"code": fresh.RecoveryCodes[0]}); r.status != http.StatusOK {
		t.Errorf("a new recovery code: status %d body %s, want 200", r.status, r.body)
	}
}

// TestMfa_OwnerResetsAnotherOwnersMfa proves the reset: the target's TOTP
// is off with a new secret nobody holds, a fresh recovery set is returned,
// and every session of theirs ends, after which a password alone signs
// them in. The caller's own account is 400, a user who is not an Owner and
// an unknown id are the bare 404, and a caller session without MFA is 403.
func TestMfa_OwnerResetsAnotherOwnersMfa(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	caller, callerID := h.bootstrapOwner(t)
	h.enrollTOTP(t, caller, ownerPassword)
	const targetEmail = "reset-target@example.test"
	target := h.createUser(t, caller, targetEmail, identity.RoleOwner)
	b := h.login(t, targetEmail, userPassword)
	_, oldCodes := h.enrollTOTP(t, b, userPassword)
	b2 := h.signIn(t, target, true)
	before := readTOTP(t, h, target)

	r := caller.do(http.MethodPost, ownerMFAPath+"/reset/"+target.String(), map[string]any{})
	var reset mfaCodes
	r.json(&reset)
	if r.status != http.StatusOK || reset.UserID != target {
		t.Fatalf("reset: status %d body %s", r.status, r.body)
	}
	checkRecoveryCodes(t, h, target, reset.RecoveryCodes)
	if n := h.count(t, `SELECT count(*) FROM identity.recovery_codes WHERE code_hash = $1`, recoveryHash(oldCodes[0])); n != 0 {
		t.Errorf("an old recovery code survived the reset")
	}
	after := readTOTP(t, h, target)
	if after.enabled || after.secret == nil || bytes.Equal(after.secret, before.secret) || after.lastStep != nil {
		t.Errorf("after the reset enabled=%v secret replaced=%v lastStep=%v, want TOTP off with a new secret",
			after.enabled, after.secret != nil && !bytes.Equal(after.secret, before.secret), after.lastStep)
	}
	rejected(t, b)
	rejected(t, b2)
	admitted(t, caller)

	r = h.client(t).do(http.MethodPost, loginPath, credentials(targetEmail, userPassword))
	var body authSuccess
	r.json(&body)
	if r.status != http.StatusOK || body.RequiresTwoFactor || body.User == nil {
		t.Errorf("the target's sign-in after the reset: status %d body %s, want a session from the password alone", r.status, r.body)
	}

	r = caller.do(http.MethodPost, ownerMFAPath+"/reset/"+callerID.String(), map[string]any{})
	if r.status != http.StatusBadRequest || r.code() != "invalid_request" || !strings.Contains(string(r.body), "Use the self-service MFA endpoints for your own account.") {
		t.Errorf("resetting their own MFA: status %d body %s, want 400 invalid_request", r.status, r.body)
	}
	user := h.createUser(t, caller, "reset-user@example.test", identity.RoleUser)
	for _, id := range []uuid.UUID{user, uuid.New()} {
		if r := caller.do(http.MethodPost, ownerMFAPath+"/reset/"+id.String(), map[string]any{}); r.status != http.StatusNotFound || len(r.body) != 0 {
			t.Errorf("resetting %s: status %d body %q, want the bare 404", id, r.status, r.body)
		}
	}
	withoutMFA := h.signIn(t, callerID, false)
	if r := withoutMFA.do(http.MethodPost, ownerMFAPath+"/reset/"+target.String(), map[string]any{}); r.status != http.StatusForbidden || r.code() != "forbidden" {
		t.Errorf("a caller session without MFA: status %d code %q, want 403 forbidden", r.status, r.code())
	}
}

// TestMfa_SetupDisableAndOwnerResetSpendResetLinks is the Task 11 review
// carry: .NET's reset tokens were bound to the security stamp, so the MFA
// changes that rotated it also killed every outstanding reset link. Go
// binds reset tokens to the user, so each of them deletes the user's reset
// tokens in the transaction that ends their sessions.
func TestMfa_SetupDisableAndOwnerResetSpendResetLinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	h.enrollTOTP(t, owner, ownerPassword)

	cases := []struct {
		name  string
		email string
		role  string
		// before runs before the reset link is requested; change is the
		// MFA change that must spend it.
		before func(c *client)
		change func(c *client, id uuid.UUID) *resp
	}{
		{"setup", "links-setup@example.test", identity.RoleUser, func(*client) {}, func(c *client, _ uuid.UUID) *resp {
			return c.do(http.MethodPost, accountMFAPath+"/setup", map[string]any{"password": userPassword})
		}},
		{"disable", "links-disable@example.test", identity.RoleUser, func(c *client) { h.enrollTOTP(t, c, userPassword) }, func(c *client, _ uuid.UUID) *resp {
			return c.do(http.MethodPost, accountMFAPath+"/disable", map[string]any{"password": userPassword})
		}},
		{"owner reset", "links-reset@example.test", identity.RoleOwner, func(c *client) { h.enrollTOTP(t, c, userPassword) }, func(_ *client, id uuid.UUID) *resp {
			return owner.do(http.MethodPost, ownerMFAPath+"/reset/"+id.String(), map[string]any{})
		}},
	}
	for _, tc := range cases {
		id := h.createUser(t, owner, tc.email, tc.role)
		c := h.login(t, tc.email, userPassword)
		tc.before(c)
		requestRecovery(t, h, tc.email)
		token := mailedLink(t, h, tc.email).Query().Get("token")

		if r := tc.change(c, id); r.status != http.StatusOK {
			t.Fatalf("%s: status %d body %s", tc.name, r.status, r.body)
		}
		if r := reset(h, t, tc.email, token, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
			t.Errorf("%s: the reset link afterwards: status %d code %q, want 400 invalid_reset_token", tc.name, r.status, r.code())
		}
		if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 0 {
			t.Errorf("%s: %d reset tokens left, want 0", tc.name, n)
		}
	}
}
