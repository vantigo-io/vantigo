package identity_test

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	accountPath         = "/api/v1/identity/account"
	accountProfilePath  = "/api/v1/identity/account/profile"
	accountPasswordPath = "/api/v1/identity/account/password"
)

// account is AccountResponse as a test reads it.
type account struct {
	ID                uuid.UUID `json:"id"`
	DisplayName       string    `json:"displayName"`
	Email             *string   `json:"email"`
	PreferredLanguage *string   `json:"preferredLanguage"`
	AvatarUrl         *string   `json:"avatarUrl"`
}

func getAccount(t *testing.T, c *client) account {
	t.Helper()
	r := c.do(http.MethodGet, accountPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", accountPath, r.status, r.body)
	}
	var a account
	r.json(&a)
	return a
}

// Ported from IdentityAccountEndpointsTests.AccountProfile_NorwegianLanguageRoundTripsCaseInsensitivelyAndNormalizesAutomatic.
// Ported from IdentityAccountEndpointsTests.AccountProfile_IsolatedAndRejectsInvalidLanguageWithoutMutation.
// The second's isolation probe, GET /account/{otherId}, names a path no
// contract operation has: the router answers it with its 404 problem, so
// one account never reads another through the self-service routes.
func TestAccountProfile_LanguageRoundTripsCaseInsensitivelyAndRejectsInvalidWithoutMutation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "profile@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	before := getAccount(t, c)

	invalid := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "Changed", "preferredLanguage": "fr"})
	if invalid.status != http.StatusBadRequest || invalid.code() != "invalid_request" {
		t.Fatalf("invalid language: status %d code %q, want 400 invalid_request", invalid.status, invalid.code())
	}
	if after := getAccount(t, c); after.DisplayName != before.DisplayName {
		t.Errorf("displayName mutated to %q by a rejected request, want %q unchanged", after.DisplayName, before.DisplayName)
	}

	blank := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "Language Test", "preferredLanguage": "   "})
	var blankAccount account
	blank.json(&blankAccount)
	if blank.status != http.StatusOK || blankAccount.PreferredLanguage != nil {
		t.Errorf("blank language: status %d language %v, want 200 and nil", blank.status, blankAccount.PreferredLanguage)
	}

	automatic := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "Language Test", "preferredLanguage": "AUTOMATIC"})
	var automaticAccount account
	automatic.json(&automaticAccount)
	if automatic.status != http.StatusOK || automaticAccount.PreferredLanguage != nil {
		t.Errorf("AUTOMATIC: status %d language %v, want 200 and nil", automatic.status, automaticAccount.PreferredLanguage)
	}

	norwegian := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "Language Test", "preferredLanguage": "NB"})
	var norwegianAccount account
	norwegian.json(&norwegianAccount)
	if norwegian.status != http.StatusOK || norwegianAccount.PreferredLanguage == nil || *norwegianAccount.PreferredLanguage != "nb" {
		t.Errorf("NB: status %d language %v, want 200 and \"nb\"", norwegian.status, norwegianAccount.PreferredLanguage)
	}

	roundTrip := getAccount(t, c)
	if roundTrip.PreferredLanguage == nil || *roundTrip.PreferredLanguage != "nb" {
		t.Errorf("GET %s preferredLanguage = %v, want \"nb\"", accountPath, roundTrip.PreferredLanguage)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND preferred_language = 'nb'`, id); got != 1 {
		t.Errorf("preferred_language in the database is not 'nb'")
	}

	other := h.seedUser(t, "other-profile@example.test", userPassword, identity.RoleUserID)
	foreign := c.do(http.MethodGet, accountPath+"/"+other.String(), nil,
		skipContract("GET /account/{id} is no contract operation; the router's 404 problem answers it"))
	if foreign.status != http.StatusNotFound || foreign.header("Content-Type") != "application/problem+json" {
		t.Errorf("GET %s/{otherId}: status %d Content-Type %q, want the 404 problem", accountPath, foreign.status, foreign.header("Content-Type"))
	}
}

// TestAccountProfile_AllThreeRoutesShareTheSameHandler proves PUT /account,
// PUT /account/profile and PATCH /account/profile all apply the same edit
// (EA/AccountSettingsEndpoints.cs:43-46): each gets its own successful,
// contract-validated exchange here.
func TestAccountProfile_AllThreeRoutesShareTheSameHandler(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "routes@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	put := c.do(http.MethodPut, accountPath, map[string]string{"displayName": "Put Account"})
	var putAccount account
	put.json(&putAccount)
	if put.status != http.StatusOK || putAccount.DisplayName != "Put Account" {
		t.Fatalf("PUT %s: status %d displayName %q", accountPath, put.status, putAccount.DisplayName)
	}

	if r := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "Put Profile"}); r.status != http.StatusOK {
		t.Fatalf("PUT %s: status %d body %s", accountProfilePath, r.status, r.body)
	}

	patch := c.do(http.MethodPatch, accountProfilePath, map[string]string{"displayName": "Patched Profile"})
	var patched account
	patch.json(&patched)
	if patch.status != http.StatusOK || patched.DisplayName != "Patched Profile" {
		t.Fatalf("PATCH %s: status %d displayName %q", accountProfilePath, patch.status, patched.DisplayName)
	}
}

// TestAccountProfile_DisplayNameIsRequiredTrimmedAndBounded proves .NET's
// ValidateProfile bound (EA/AccountSettingsEndpoints.cs:908-909): blank
// and over-200 both 400 invalid_request; a value with surrounding
// whitespace is trimmed before it is stored.
func TestAccountProfile_DisplayNameIsRequiredTrimmedAndBounded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "bounds@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	if r := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "   "}); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("blank displayName: status %d code %q, want 400 invalid_request", r.status, r.code())
	}
	if r := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": strings.Repeat("a", 201)}); r.status != http.StatusBadRequest || r.code() != "invalid_request" {
		t.Errorf("201-character displayName: status %d code %q, want 400 invalid_request", r.status, r.code())
	}

	ok := c.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "  Trimmed Name  "})
	var a account
	ok.json(&a)
	if ok.status != http.StatusOK || a.DisplayName != "Trimmed Name" {
		t.Errorf("status %d displayName %q, want 200 and \"Trimmed Name\"", ok.status, a.DisplayName)
	}
}

// TestAccountProfile_DoesNotRevokeOtherSessions proves the deliberate
// divergence from .NET (spec *Sessions*, Divergences): editing the
// caller's own displayName or language rotates nothing, so a second
// signed-in session survives untouched.
func TestAccountProfile_DoesNotRevokeOtherSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "stays@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	first, second := h.login(t, email, userPassword), h.login(t, email, userPassword)

	if r := first.do(http.MethodPut, accountProfilePath, map[string]string{"displayName": "New Name"}); r.status != http.StatusOK {
		t.Fatalf("profile edit: status %d body %s", r.status, r.body)
	}
	admitted(t, first)
	admitted(t, second)
}

// Ported from IdentityAccountEndpointsTests.PasswordChange_InvalidPasswordDoesNotMutateAndReturnsValidation.
func TestAccountPassword_WrongCurrentPasswordIsRejectedAndOriginalStillWorks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "password@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	invalid := c.do(http.MethodPost, accountPasswordPath, map[string]string{
		"currentPassword": "wrong-current-password",
		"newPassword":     "NewIntegrationPassword123",
	})
	if invalid.status != http.StatusBadRequest || invalid.code() != "reauthentication_required" {
		t.Fatalf("wrong current password: status %d code %q, want 400 reauthentication_required", invalid.status, invalid.code())
	}

	admitted(t, h.login(t, email, userPassword))
}

// TestAccountPassword_NoLocalPasswordIs409 proves an OIDC-only account (no
// password_hash) cannot change a password it does not have
// (EA/AccountSettingsEndpoints.cs:674-678).
func TestAccountPassword_NoLocalPasswordIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "oidc-only@example.test", identity.RoleUserID)
	c := h.signIn(t, id, false)

	r := c.do(http.MethodPost, accountPasswordPath, map[string]string{
		"currentPassword": "whatever-password",
		"newPassword":     "NewIntegrationPassword123",
	})
	if r.status != http.StatusConflict || r.code() != "local_password_unavailable" {
		t.Errorf("status %d code %q, want 409 local_password_unavailable", r.status, r.code())
	}
}

// TestAccountPassword_PolicyFailureIsIdentityValidationFailedWithoutMutation
// proves a new password that passes the presence check but fails the
// policy (all-whitespace, so it is blank after trimming) is 400
// identity_validation_failed with fields, and changes nothing.
func TestAccountPassword_PolicyFailureIsIdentityValidationFailedWithoutMutation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "policy@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	r := c.do(http.MethodPost, accountPasswordPath, map[string]string{"currentPassword": userPassword, "newPassword": " "})
	if r.status != http.StatusBadRequest || r.code() != "identity_validation_failed" {
		t.Fatalf("status %d code %q, want 400 identity_validation_failed", r.status, r.code())
	}
	if !strings.Contains(string(r.body), "PasswordTooShort") {
		t.Errorf("body %s, want the PasswordTooShort field", r.body)
	}
	admitted(t, h.login(t, email, userPassword))
}

// TestAccountPassword_SuccessRevokesOtherSessionsDeletesResetTokensAndBumpsVersion
// proves the brief's success path: other sessions die, the caller's own
// survives, every password-reset token of this user is deleted, and
// version changes (spec *Sessions*, *Credentials*).
func TestAccountPassword_SuccessRevokesOtherSessionsDeletesResetTokensAndBumpsVersion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "change@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	caller, other := h.login(t, email, userPassword), h.login(t, email, userPassword)
	admitted(t, other)

	hash := sha256.Sum256([]byte("a reset token this change must delete"))
	h.exec(t, `INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash[:], id, h.now().Add(24*time.Hour))
	var before uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}

	const newPassword = "ChangedIntegrationPassword123"
	r := caller.do(http.MethodPost, accountPasswordPath, map[string]string{"currentPassword": userPassword, "newPassword": newPassword})
	var body struct {
		Success bool `json:"success"`
	}
	r.json(&body)
	if r.status != http.StatusOK || !body.Success {
		t.Fatalf("change password: status %d body %s", r.status, r.body)
	}

	admitted(t, caller)
	rejected(t, other)

	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 0 {
		t.Errorf("%d password reset tokens remain, want 0", n)
	}
	var after uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Errorf("version unchanged by the password change")
	}

	if r := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusUnauthorized {
		t.Errorf("the old password still works: status %d, want 401", r.status)
	}
	admitted(t, h.login(t, email, newPassword))
}
