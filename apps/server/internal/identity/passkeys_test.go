package identity_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// passkeyAccount seeds a user who signs in with userPassword and returns
// its id and a client signed in as it.
func passkeyAccount(t *testing.T, h *harness, email string) (uuid.UUID, *client) {
	t.Helper()
	id := h.seedUser(t, email, userPassword)
	return id, h.login(t, email, userPassword)
}

// insertPasskey stores a passkey for userID directly, as an enrolment would
// have, under a credential nobody can sign in with. It is test-only
// scaffolding for tests that need a user to hold passkeys, and a count of
// them, without enrolling each through the Mfa-limited endpoints.
func insertPasskey(t *testing.T, h *harness, userID uuid.UUID) {
	t.Helper()
	h.exec(t, `INSERT INTO identity.passkeys (credential_id, user_id, name, credential, user_verified, backup_eligible, backed_up, created_at)
	           VALUES ($1, $2, 'Seeded', '{}', true, false, false, $3)`, []byte(rand.Text()), userID, h.now())
}

// passkeyEntry is PasskeyResponse as a test reads it.
type passkeyEntry struct {
	CredentialID     string    `json:"credentialId"`
	Name             string    `json:"name"`
	CreatedAt        time.Time `json:"createdAt"`
	Transports       []string  `json:"transports"`
	IsUserVerified   bool      `json:"isUserVerified"`
	IsBackupEligible bool      `json:"isBackupEligible"`
	IsBackedUp       bool      `json:"isBackedUp"`
}

// listPasskeys is GET /account/passkeys for c, which must succeed.
func listPasskeys(t *testing.T, c *client) []passkeyEntry {
	t.Helper()
	r := c.do(http.MethodGet, accountPasskeysPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET passkeys: status %d body %s", r.status, r.body)
	}
	var out []passkeyEntry
	r.json(&out)
	return out
}

// wantAuthError fails unless r is status with the AuthErrorResponse code
// and message.
func wantAuthError(t *testing.T, what string, r *resp, status int, code, message string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &body)
	if r.status != status || body.Error.Code != code || body.Error.Message != message {
		t.Errorf("%s: status %d body %s, want %d %s %q", what, r.status, r.body, status, code, message)
	}
}

const passkeySignInFailed = "The passkey sign-in failed."

// TestPasskeys_EnrolThenSignIn is the round trip with the software
// authenticator: the creation options, the enrolment and its row, the
// list, then two passwordless sign-ins that each start an MFA-verified,
// non-persistent session and advance the stored signature counter, leaving
// the failure count as .NET's SignInWithClaimsAsync left it.
func TestPasskeys_EnrolThenSignIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "passkey@example.test"
	id, c := passkeyAccount(t, h, email)
	h.exec(t, `UPDATE identity.users SET failed_login_count = 2 WHERE id = $1`, id)
	version := func() uuid.UUID {
		var v uuid.UUID
		if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := version()

	ceremony := beginPasskeyEnrolment(t, c, userPassword, "  Laptop  ")
	var opts struct {
		RP struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rp"`
		User struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
		Challenge              string `json:"challenge"`
		Timeout                int    `json:"timeout"`
		ExcludeCredentials     []any  `json:"excludeCredentials"`
		AuthenticatorSelection struct {
			RequireResidentKey bool   `json:"requireResidentKey"`
			ResidentKey        string `json:"residentKey"`
			UserVerification   string `json:"userVerification"`
		} `json:"authenticatorSelection"`
		Attestation      string `json:"attestation"`
		PubKeyCredParams []struct {
			Alg int `json:"alg"`
		} `json:"pubKeyCredParams"`
	}
	if err := json.Unmarshal(ceremony.Options, &opts); err != nil {
		t.Fatal(err)
	}
	if opts.RP.ID != "identity.example.com" || opts.RP.Name != "identity.example.com" ||
		opts.User.ID != b64(id[:]) || opts.User.Name != email || opts.User.DisplayName != email ||
		opts.Timeout != 300_000 || opts.ExcludeCredentials != nil ||
		!opts.AuthenticatorSelection.RequireResidentKey || opts.AuthenticatorSelection.ResidentKey != "required" ||
		opts.AuthenticatorSelection.UserVerification != "required" || opts.Attestation != "none" ||
		len(mustB64(t, opts.Challenge)) < 16 ||
		!slices.ContainsFunc(opts.PubKeyCredParams, func(p struct {
			Alg int `json:"alg"`
		}) bool {
			return p.Alg == -7
		}) {
		t.Errorf("creation options = %s", ceremony.Options)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies
	        WHERE id = $1 AND kind = 'enroll' AND user_id = $2 AND credential_name = 'Laptop' AND expires_at = $3 AND consumed_at IS NULL`,
		ceremony.CeremonyID, id, h.now().Add(5*time.Minute)); n != 1 {
		t.Errorf("no live enrolment ceremony row for the caller named Laptop")
	}

	a := newAuthenticator(t)
	credentialJSON := a.create(t, ceremony.Options)
	r := c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
		"ceremonyId": ceremony.CeremonyID, "credentialJson": credentialJSON, "currentPassword": userPassword,
	})
	if r.status != http.StatusOK || string(bytes.TrimSpace(r.body)) != `{"success":true}` {
		t.Fatalf("complete: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE id = $1 AND consumed_at = $2`, ceremony.CeremonyID, h.now()); n != 1 {
		t.Errorf("the enrolment ceremony was not consumed at now")
	}
	list := listPasskeys(t, c)
	if len(list) != 1 || list[0].CredentialID != b64(a.id) || list[0].Name != "Laptop" || !list[0].CreatedAt.Equal(h.now()) ||
		!slices.Equal(list[0].Transports, []string{"internal", "hybrid"}) ||
		!list[0].IsUserVerified || list[0].IsBackupEligible || list[0].IsBackedUp {
		t.Errorf("passkeys = %+v", list)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkeys WHERE credential_id = $1 AND user_id = $2
	        AND credential->>'publicKey' <> '' AND (credential->'flags'->>'userVerified')::boolean`, a.id, id); n != 1 {
		t.Errorf("the stored credential is not go-webauthn's Credential with its key and flags")
	}
	if version() == before {
		t.Errorf("enrolment did not rotate the user's version")
	}

	signCount := func() int {
		return h.count(t, `SELECT coalesce((credential->'authenticator'->>'signCount')::integer, 0) FROM identity.passkeys WHERE credential_id = $1`, a.id)
	}
	for i := 1; i <= 2; i++ {
		h.advance(time.Minute)
		login := h.client(t)
		r = passkeyLogin(t, login, email, a)
		if r.status != http.StatusOK {
			t.Fatalf("sign-in %d: status %d body %s", i, r.status, r.body)
		}
		var body struct {
			User struct {
				ID          uuid.UUID `json:"id"`
				DisplayName string    `json:"displayName"`
				Email       string    `json:"email"`
				Roles       []string  `json:"roles"`
			} `json:"user"`
			RequiresTwoFactor     bool  `json:"requiresTwoFactor"`
			TwoFactorEnabled      bool  `json:"twoFactorEnabled"`
			MfaEnrollmentRequired bool  `json:"mfaEnrollmentRequired"`
			Tenants               []any `json:"tenants"`
		}
		r.json(&body)
		if body.User.ID != id || body.User.Email != email || body.User.Roles == nil || len(body.User.Roles) != 0 ||
			body.RequiresTwoFactor || body.TwoFactorEnabled || body.MfaEnrollmentRequired || body.Tenants == nil ||
			!bytes.Contains(r.body, []byte(`"activeTenantId":null`)) {
			t.Errorf("sign-in %d body %s", i, r.body)
		}
		ck := r.setCookie(identity.SessionCookieName)
		if ck == nil || ck.MaxAge != 0 || !ck.HttpOnly {
			t.Fatalf("sign-in %d session cookie = %+v, want a non-persistent HttpOnly cookie", i, ck)
		}
		if !sessionMFA(t, login) {
			t.Errorf("sign-in %d: the session is not MFA-verified", i)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE id = $1 AND NOT persistent AND mfa_verified_at = $2`,
			h.sessionID(t, ck.Value), h.now()); n != 1 {
			t.Errorf("sign-in %d: the session row is not non-persistent with mfa_verified_at = now", i)
		}
		if got := signCount(); got != i {
			t.Errorf("sign-in %d: stored signature counter %d", i, got)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.passkeys WHERE credential_id = $1 AND last_used_at = $2`, a.id, h.now()); n != 1 {
			t.Errorf("sign-in %d: last_used_at is not now", i)
		}
	}
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE id = $1`, id); n != 2 {
		t.Errorf("failed_login_count = %d, want 2: .NET's passkey sign-in reset no lockout", n)
	}
	logged := h.log.Bytes()
	for _, secret := range []string{opts.Challenge, credentialJSON, b64(a.id)} {
		if bytes.Contains(logged, []byte(secret)) {
			t.Errorf("the log holds a challenge or credential: %q", secret)
		}
	}
}

// TestPasskeyLogin_RefusesAssertionsThatDoNotVerify answers a sign-in
// ceremony begun for an account with every kind of bad assertion: each is
// the same 401 invalid_credentials, starts no session, and spends its
// ceremony.
func TestPasskeyLogin_RefusesAssertionsThatDoNotVerify(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "owner-of-a@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "A")
	_, other := passkeyAccount(t, h, "owner-of-b@example.test")
	b := enrollPasskey(t, other, userPassword, "B")
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	variant := func(change func(*authenticator)) *authenticator {
		v := *a
		change(&v)
		return &v
	}
	stranger := newAuthenticator(t)
	stranger.userHandle = a.userHandle
	cases := []struct {
		name string
		with *authenticator
	}{
		{"without user verification", variant(func(v *authenticator) { v.noUV = true })},
		{"from another origin", variant(func(v *authenticator) { v.origin = "http://evil.example.com" })},
		{"for another RP ID", variant(func(v *authenticator) { v.rpID = "evil.example.com" })},
		{"signed by another key", variant(func(v *authenticator) { v.key = otherKey })},
		{"with another account's passkey", b},
		{"with an unknown credential", stranger},
	}
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)
	for _, tc := range cases {
		login := h.client(t)
		ceremony := beginPasskeyLogin(t, login, email)
		r := completePasskeyLogin(t, login, ceremony, tc.with)
		wantAuthError(t, tc.name, r, http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
		if r.setCookie(identity.SessionCookieName) != nil {
			t.Errorf("%s: a session cookie was set", tc.name)
		}
		// The ceremony is spent: even the right passkey cannot use it now.
		r = completePasskeyLogin(t, login, ceremony, a)
		wantAuthError(t, tc.name+", then the right passkey", r, http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("%d sessions, want %d: a refused sign-in started one", n, sessions)
	}
	if r := passkeyLogin(t, h.client(t), email, a); r.status != http.StatusOK {
		t.Errorf("the right passkey afterwards: status %d body %s", r.status, r.body)
	}
}

// TestPasskeyLogin_ACounterThatDidNotAdvanceIsRefused: a signature counter
// no higher than the stored one signals a cloned authenticator. ASP.NET
// rejected it (PasskeyHandler.PerformAssertionCoreAsync throws
// SignCountLessThanOrEqualToStoredSignCount, which PerformAssertionAsync
// returns as a failed result, answered 401 at
// EA/AccountSettingsEndpoints.cs:588-591), and so does go-webauthn's clone
// warning here; the stored counter stays where it was.
func TestPasskeyLogin_ACounterThatDidNotAdvanceIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "counter@example.test"
	_, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	for range 2 {
		if r := passkeyLogin(t, h.client(t), email, a); r.status != http.StatusOK {
			t.Fatalf("sign-in: status %d body %s", r.status, r.body)
		}
	}
	// The stored counter is 2. A clone set to 1 reports 2, equal to it; one
	// set to 0 reports 1, below it.
	for _, count := range []uint32{1, 0} {
		clone := *a
		clone.signCount = count
		r := passkeyLogin(t, h.client(t), email, &clone)
		wantAuthError(t, "a counter that did not advance", r, http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
	}
	if n := h.count(t, `SELECT (credential->'authenticator'->>'signCount')::integer FROM identity.passkeys WHERE credential_id = $1`, a.id); n != 2 {
		t.Errorf("stored counter %d, want 2", n)
	}
	if r := passkeyLogin(t, h.client(t), email, a); r.status != http.StatusOK {
		t.Errorf("the advancing authenticator: status %d body %s", r.status, r.body)
	}
}

// Ported from IdentityMfaIntegrationTests.PasskeyBeginRejectsLockedAccountWithGenericAvailabilityResponse,
// extended: the ceremony holds no account, so the account's own passkey
// cannot complete it.
func TestPasskeyLogin_BeginAnswersALockedAccountLikeAnyOther(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "passkey-locked@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, id, h.now().Add(10*time.Minute))

	login := h.client(t)
	r := login.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": email})
	var ceremony passkeyCeremony
	r.json(&ceremony)
	if r.status != http.StatusOK || len(ceremony.Options) == 0 || ceremony.CeremonyID == uuid.Nil {
		t.Fatalf("begin: status %d body %s, want 200 with options", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE id = $1 AND user_id IS NULL`, ceremony.CeremonyID); n != 1 {
		t.Errorf("the locked account's ceremony is bound to it")
	}
	wantAuthError(t, "complete", completePasskeyLogin(t, login, ceremony, a), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
}

// Ported from IdentityMfaIntegrationTests.PasskeyBeginDoesNotEnumerateUnknownOrNoPasskeyAccounts,
// extended to every account passkeys cannot sign in: an account with a
// passkey, one without, an unknown email, a disabled and a locked account
// get the same answer, key for key, allowCredentials left out, differing
// only in the challenge. Every one took the same path: one ceremony row
// each, holding an account only for the one that can sign in.
func TestPasskeyLogin_BeginDoesNotEnumerateAccounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	usable, c := passkeyAccount(t, h, "usable@example.test")
	enrollPasskey(t, c, userPassword, "Key")
	h.seedUser(t, "no-passkey@example.test", userPassword)
	disabled := h.seedUser(t, "disabled@example.test", userPassword)
	insertPasskey(t, h, disabled)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	locked := h.seedUser(t, "locked@example.test", userPassword)
	insertPasskey(t, h, locked)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, locked, h.now().Add(time.Minute))

	login := h.client(t)
	emails := []string{"usable@example.test", "no-passkey@example.test", "missing-" + uuid.NewString() + "@integration.test", "disabled@example.test", "LOCKED@example.test"}
	type answer struct {
		top, options map[string]json.RawMessage
	}
	var answers []answer
	for _, email := range emails {
		r := login.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": email})
		if r.status != http.StatusOK {
			t.Fatalf("begin %s: status %d body %s", email, r.status, r.body)
		}
		var an answer
		r.json(&an.top)
		if err := json.Unmarshal(an.top["options"], &an.options); err != nil {
			t.Fatal(err)
		}
		answers = append(answers, an)
	}
	keys := func(m map[string]json.RawMessage) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	want := answers[0]
	if !slices.Equal(keys(want.top), []string{"ceremonyId", "options"}) ||
		!slices.Equal(keys(want.options), []string{"challenge", "rpId", "timeout", "userVerification"}) {
		t.Errorf("answer keys %v, options keys %v", keys(want.top), keys(want.options))
	}
	for i, an := range answers {
		if !slices.Equal(keys(an.top), keys(want.top)) || !slices.Equal(keys(an.options), keys(want.options)) {
			t.Errorf("%s: keys %v / %v, want %v / %v", emails[i], keys(an.top), keys(an.options), keys(want.top), keys(want.options))
		}
		if _, ok := an.options["allowCredentials"]; ok {
			t.Errorf("%s: options carry allowCredentials", emails[i])
		}
		for _, k := range []string{"rpId", "timeout", "userVerification"} {
			if !bytes.Equal(an.options[k], want.options[k]) {
				t.Errorf("%s: %s = %s, want %s", emails[i], k, an.options[k], want.options[k])
			}
		}
		if i > 0 && bytes.Equal(an.options["challenge"], want.options["challenge"]) {
			t.Errorf("%s: the challenge repeats", emails[i])
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE kind = 'login' AND client_ip = $1`, login.ip); n != len(emails) {
		t.Errorf("%d ceremony rows, want one per begin", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE kind = 'login' AND user_id IS NOT NULL`); n != 1 ||
		h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE user_id = $1`, usable) != 1 {
		t.Errorf("only the usable account's ceremony should hold an account")
	}
}

// Ported from IdentityMfaIntegrationTests.PasskeyCeremoniesAreRemovedAfterFailureAndExpiredRowsArePurged:
// begin purges an expired ceremony; a completion that fails still spends
// its ceremony (consumed_at set in the same statement that claims it), so
// the replay is 409; and the next begin purges the spent row.
func TestPasskeyCeremonies_AreSpentByAFailureAndPurgedOnBegin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "ceremony-cleanup@example.test"
	id, c := passkeyAccount(t, h, email)
	enrollPasskey(t, c, userPassword, "Key")
	expired := uuid.New()
	h.exec(t, `INSERT INTO identity.passkey_ceremonies (id, user_id, kind, session_data, client_ip, expires_at)
	           VALUES ($1, $2, 'login', '{}', '127.0.0.1', $3)`, expired, id, h.now().Add(-time.Minute))

	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE id = $1`, expired); n != 0 {
		t.Errorf("begin left the expired ceremony")
	}
	complete := func() *resp {
		return login.do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{"ceremonyId": ceremony.CeremonyID, "credentialJson": "{}"})
	}
	wantAuthError(t, "complete", complete(), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE id = $1 AND consumed_at = $2`, ceremony.CeremonyID, h.now()); n != 1 {
		t.Errorf("the failed completion did not spend the ceremony")
	}
	wantAuthError(t, "replay", complete(), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
	beginPasskeyLogin(t, login, email)
	if n := h.count(t, `SELECT count(*) FROM identity.passkey_ceremonies WHERE id = ANY($1)`, []uuid.UUID{expired, ceremony.CeremonyID}); n != 0 {
		t.Errorf("%d spent or expired ceremonies survived the next begin", n)
	}
}

// TestPasskeyLogin_CeremonyExpiresAfterFiveMinutes: a ceremony completes
// until its fifth minute is up, on the Deps clock, and is 409 from then on.
func TestPasskeyLogin_CeremonyExpiresAfterFiveMinutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "expiry@example.test"
	_, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")

	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	h.advance(5*time.Minute - time.Second)
	if r := completePasskeyLogin(t, login, ceremony, a); r.status != http.StatusOK {
		t.Errorf("at 4:59: status %d body %s", r.status, r.body)
	}
	ceremony = beginPasskeyLogin(t, login, email)
	h.advance(5 * time.Minute)
	wantAuthError(t, "at 5:00", completePasskeyLogin(t, login, ceremony, a), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
}

// TestPasskeyCeremonies_AreBoundToTheirKind: a ceremony completes only in
// the flow that began it. An enrolment ceremony's id sent to sign-in
// completion, and a sign-in ceremony's id sent to enrolment completion
// (the same account's, so only the kind tells them apart), are each 409
// passkey_ceremony_invalid and leave the ceremony live: its own flow then
// completes it with the very answer that was refused.
func TestPasskeyCeremonies_AreBoundToTheirKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "kinds@example.test"
	_, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	const invalid = "The passkey ceremony is expired or already used."

	enrolment := beginPasskeyEnrolment(t, c, userPassword, "Second")
	attestation := newAuthenticator(t).create(t, enrolment.Options)
	r := h.client(t).do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{
		"ceremonyId": enrolment.CeremonyID, "credentialJson": attestation,
	})
	wantAuthError(t, "an enrolment ceremony at sign-in", r, http.StatusConflict, "passkey_ceremony_invalid", invalid)
	r = c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
		"ceremonyId": enrolment.CeremonyID, "credentialJson": attestation, "currentPassword": userPassword,
	})
	if r.status != http.StatusOK {
		t.Errorf("the enrolment ceremony at enrolment afterwards: status %d body %s", r.status, r.body)
	}

	login := h.client(t)
	signIn := beginPasskeyLogin(t, login, email)
	assertion := a.get(t, signIn.Options)
	r = c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
		"ceremonyId": signIn.CeremonyID, "credentialJson": assertion, "currentPassword": userPassword,
	})
	wantAuthError(t, "a sign-in ceremony at enrolment", r, http.StatusConflict, "passkey_ceremony_invalid", invalid)
	r = login.do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{"ceremonyId": signIn.CeremonyID, "credentialJson": assertion})
	if r.status != http.StatusOK {
		t.Errorf("the sign-in ceremony at sign-in afterwards: status %d body %s", r.status, r.body)
	}
}

// TestPasskeyEnrolment_CeremonyExpiresAfterFiveMinutes: an enrolment
// ceremony completes until its fifth minute is up, on the Deps clock, and
// is 409 from then on, enrolling nothing.
func TestPasskeyEnrolment_CeremonyExpiresAfterFiveMinutes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id, c := passkeyAccount(t, h, "enrolment-expiry@example.test")
	complete := func(ceremony passkeyCeremony) *resp {
		return c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
			"ceremonyId": ceremony.CeremonyID, "credentialJson": newAuthenticator(t).create(t, ceremony.Options), "currentPassword": userPassword,
		})
	}
	ceremony := beginPasskeyEnrolment(t, c, userPassword, "In time")
	h.advance(5*time.Minute - time.Second)
	if r := complete(ceremony); r.status != http.StatusOK {
		t.Errorf("at 4:59: status %d body %s", r.status, r.body)
	}
	ceremony = beginPasskeyEnrolment(t, c, userPassword, "Too late")
	h.advance(5 * time.Minute)
	wantAuthError(t, "at 5:00", complete(ceremony), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
	if n := h.count(t, `SELECT count(*) FROM identity.passkeys WHERE user_id = $1`, id); n != 1 {
		t.Errorf("%d passkeys, want 1", n)
	}
}

// TestPasskeyLogin_APasskeyRemovedMidSignInIsRefused: a removal takes no
// account lock, so it can commit while a sign-in with that passkey sits
// between reading the account's passkeys and recording the use. A gate
// holds the passkey's row until the sign-in waits on it, then deletes the
// row and commits: the sign-in finds nothing to record and is 401
// invalid_credentials, with no session.
func TestPasskeyLogin_APasskeyRemovedMidSignInIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "removed-mid-sign-in@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)
	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	assertion := a.get(t, ceremony.Options)

	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM identity.passkeys WHERE credential_id = $1 FOR UPDATE`, a.id); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var r *resp
	go func() {
		defer close(finished)
		r = login.do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{"ceremonyId": ceremony.CeremonyID, "credentialJson": assertion})
	}()
	awaitLockWaiters(t, h, 1, finished)
	if _, err := tx.Exec(ctx, `DELETE FROM identity.passkeys WHERE credential_id = $1`, a.id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	<-finished

	wantAuthError(t, "sign-in", r, http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
	if r.setCookie(identity.SessionCookieName) != nil {
		t.Errorf("a session cookie was set")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("%d sessions, want %d", n, sessions)
	}
}

// TestPasskeyLogin_OneCeremonyCompletesOnce races two valid assertions for
// one ceremony: exactly one signs in, the other is 409, and one session
// starts. Run it with -count=10.
func TestPasskeyLogin_OneCeremonyCompletesOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "race@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	first, second := a.get(t, ceremony.Options), a.get(t, ceremony.Options)
	complete := func(credential string) func() *resp {
		return func() *resp {
			return h.client(t).do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{"ceremonyId": ceremony.CeremonyID, "credentialJson": credential})
		}
	}
	rs := race(complete(first), complete(second))
	statuses := []int{rs[0].status, rs[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Errorf("statuses %v, want one 200 and one 409", statuses)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions+1 {
		t.Errorf("%d sessions, want %d", n, sessions+1)
	}
}

// TestPasskeyLogin_LiveCeremoniesAreCappedPerClientAddress: twenty live
// sign-in ceremonies per client address (.NET's MaxLoginCeremoniesPerAddress);
// the next is 429 rate_limited without Retry-After. Another address is not
// affected, a spent ceremony frees its place, and so does expiry.
func TestPasskeyLogin_LiveCeremoniesAreCappedPerClientAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	begin := func(c *client) *resp {
		return c.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": "anyone@example.test"})
	}
	var first passkeyCeremony
	for i := range 20 {
		r := begin(c)
		if r.status != http.StatusOK {
			t.Fatalf("begin %d: status %d body %s", i+1, r.status, r.body)
		}
		if i == 0 {
			r.json(&first)
		}
	}
	r := begin(c)
	wantAuthError(t, "the 21st", r, http.StatusTooManyRequests, "rate_limited", "Too many passkey ceremonies are active.")
	if r.header("Retry-After") != "" {
		t.Errorf("the ceremony cap's 429 carries Retry-After %q", r.header("Retry-After"))
	}
	if r := begin(h.client(t)); r.status != http.StatusOK {
		t.Errorf("another address: status %d", r.status)
	}
	c.do(http.MethodPost, passkeyLoginPath+"/complete", map[string]any{"ceremonyId": first.CeremonyID, "credentialJson": "{}"})
	if r := begin(c); r.status != http.StatusOK {
		t.Errorf("after a ceremony was spent: status %d body %s", r.status, r.body)
	}
	wantAuthError(t, "full again", begin(c), http.StatusTooManyRequests, "rate_limited", "Too many passkey ceremonies are active.")
	h.advance(5 * time.Minute)
	if r := begin(c); r.status != http.StatusOK {
		t.Errorf("after the ceremonies expired: status %d body %s", r.status, r.body)
	}
}

// TestPasskeyCeremonyCaps_HoldUnderConcurrentBegins races four begins
// against a cap with one place left, for sign-in (19 live of 20 per
// address) and for enrolment (2 live of 3 per user). A gate holds the
// ceremony table until all four are waiting on a lock, so they truly
// overlap once it opens; the ceremony lock then admits exactly one. Run it
// with -count=10.
func TestPasskeyCeremonyCaps_HoldUnderConcurrentBegins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id, c := passkeyAccount(t, h, "caps@example.test")
	login := h.client(t)
	seed := func(kind string, userID *uuid.UUID, ip *string, n int) {
		for range n {
			h.exec(t, `INSERT INTO identity.passkey_ceremonies (id, user_id, kind, session_data, credential_name, client_ip, expires_at)
			           VALUES ($1, $2, $3, '{}', 'Seeded', $4, $5)`, uuid.New(), userID, kind, ip, h.now().Add(time.Minute))
		}
	}
	seed("login", nil, &login.ip, 19)
	seed("enroll", &id, nil, 2)

	count := func(rs []*resp) (ok, capped int) {
		for _, r := range rs {
			switch {
			case r.status == http.StatusOK:
				ok++
			case r.status == http.StatusTooManyRequests && r.code() == "rate_limited":
				capped++
			default:
				t.Errorf("status %d body %s", r.status, r.body)
			}
		}
		return ok, capped
	}
	var logins, enrolments []func() *resp
	for range 4 {
		logins = append(logins, func() *resp {
			return login.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": "caps@example.test"})
		})
		enrolments = append(enrolments, func() *resp {
			return c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": "Key", "currentPassword": userPassword})
		})
	}
	// gated races fns while an ACCESS EXCLUSIVE lock on the ceremony table
	// holds every one of them at its first touch of it (or at the ceremony
	// lock behind the first), and releases them together.
	gated := func(fns []func() *resp) []*resp {
		ctx := context.Background()
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `LOCK TABLE identity.passkey_ceremonies IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		var rs []*resp
		go func() {
			defer close(finished)
			rs = race(fns...)
		}()
		awaitLockWaiters(t, h, len(fns), finished)
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		<-finished
		return rs
	}
	if ok, capped := count(gated(logins)); ok != 1 || capped != 3 {
		t.Errorf("sign-in begins: %d admitted and %d capped, want 1 and 3", ok, capped)
	}
	if ok, capped := count(gated(enrolments)); ok != 1 || capped != 3 {
		t.Errorf("enrolment begins: %d admitted and %d capped, want 1 and 3", ok, capped)
	}
}

// TestPasskeyLogin_BeginValidatesTheEmail: a blank email, or one longer
// than 256 characters, is 400 invalid_request.
func TestPasskeyLogin_BeginValidatesTheEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	long := strings.Repeat("a", 256-len("@example.test")) + "@example.test"
	for _, email := range []any{nil, "", "   ", long + "x"} {
		r := c.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": email})
		wantAuthError(t, "begin", r, http.StatusBadRequest, "invalid_request", "Email is required.")
	}
	if r := c.do(http.MethodPost, passkeyLoginPath+"/begin", map[string]any{"email": long}); r.status != http.StatusOK {
		t.Errorf("a 256-character email: status %d body %s", r.status, r.body)
	}
}

// TestPasskeyLogin_AnAccountDisabledOrLockedBeforeCompletionIsRefused: the
// account is checked again at completion, as .NET did
// (EA/AccountSettingsEndpoints.cs:565-571): disabled or locked out since the
// ceremony began is 401 invalid_credentials, and no session starts.
func TestPasskeyLogin_AnAccountDisabledOrLockedBeforeCompletionIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "late-refusal@example.test"
	id, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Key")
	sessions := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id)

	login := h.client(t)
	ceremony := beginPasskeyLogin(t, login, email)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)
	wantAuthError(t, "disabled", completePasskeyLogin(t, login, ceremony, a), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
	h.exec(t, `UPDATE identity.users SET is_disabled = false WHERE id = $1`, id)

	ceremony = beginPasskeyLogin(t, login, email)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, id, h.now().Add(15*time.Minute))
	wantAuthError(t, "locked", completePasskeyLogin(t, login, ceremony, a), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
	if n := h.count(t, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, id); n != sessions {
		t.Errorf("%d sessions, want %d", n, sessions)
	}
	h.advance(15 * time.Minute)
	if r := passkeyLogin(t, login, email, a); r.status != http.StatusOK {
		t.Errorf("once the lockout ended: status %d body %s", r.status, r.body)
	}
}

// TestPasskeyLogin_IsTheSecondFactorForATotpUser: a user with TOTP signs in
// with a passkey alone, and a two-factor sign-in the browser had pending
// ends, as .NET's SignOutAsync ended it.
func TestPasskeyLogin_IsTheSecondFactorForATotpUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "totp-and-passkey@example.test"
	_, c := passkeyAccount(t, h, email)
	h.enrollTOTP(t, c, userPassword)
	a := enrollPasskey(t, c, userPassword, "Key")

	browser := h.startTwoFactor(t, email, userPassword)
	ticket := browser.cookie(identity.LoginTicketCookieName)
	r := passkeyLogin(t, browser, email, a)
	var body struct {
		RequiresTwoFactor bool `json:"requiresTwoFactor"`
		TwoFactorEnabled  bool `json:"twoFactorEnabled"`
	}
	r.json(&body)
	if r.status != http.StatusOK || body.RequiresTwoFactor || !body.TwoFactorEnabled {
		t.Fatalf("passkey sign-in: status %d body %s", r.status, r.body)
	}
	if ck := r.setCookie(identity.LoginTicketCookieName); ck == nil || ck.MaxAge >= 0 {
		t.Errorf("the pending two-factor ticket cookie was not cleared: %+v", ck)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.login_tickets`); n != 0 || ticket == "" {
		t.Errorf("%d login tickets left", n)
	}
	if !sessionMFA(t, browser) {
		t.Errorf("the passkey session is not MFA-verified")
	}
}

// TestPasskeyEnrolment_RequiresTheCurrentPassword at begin and at complete,
// where a wrong one leaves the ceremony unspent; an account without a local
// password is 409 local_password_unavailable.
func TestPasskeyEnrolment_RequiresTheCurrentPassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "reauth@example.test")
	for _, password := range []any{nil, "", "WrongPassword123"} {
		r := c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": "Key", "currentPassword": password})
		wantAuthError(t, "begin", r, http.StatusBadRequest, "reauthentication_required", "The current password is invalid.")
	}
	ceremony := beginPasskeyEnrolment(t, c, userPassword, "Key")
	a := newAuthenticator(t)
	credential := a.create(t, ceremony.Options)
	complete := func(password any) *resp {
		return c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
			"ceremonyId": ceremony.CeremonyID, "credentialJson": credential, "currentPassword": password,
		})
	}
	wantAuthError(t, "complete", complete("WrongPassword123"), http.StatusBadRequest, "reauthentication_required", "The current password is invalid.")
	if r := complete(userPassword); r.status != http.StatusOK {
		t.Errorf("complete with the password after a wrong one: status %d body %s", r.status, r.body)
	}

	oidcOnly := h.signIn(t, h.insertUser(t, "oidc-only@example.test"), false)
	r := oidcOnly.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": "Key", "currentPassword": "anything"})
	wantAuthError(t, "an OIDC-only account", r, http.StatusConflict, "local_password_unavailable", "This OIDC-only account does not have a local password.")
	if n := h.count(t, `SELECT failed_login_count FROM identity.users WHERE email = 'reauth@example.test'`); n != 0 {
		t.Errorf("wrong passwords counted %d failures", n)
	}
}

// TestPasskeyEnrolment_ValidatesTheName as .NET's ValidatePasskeyName: not
// blank, at most 100 UTF-16 units once trimmed, no control character
// anywhere; the name is stored trimmed.
func TestPasskeyEnrolment_ValidatesTheName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "names@example.test")
	for _, name := range []any{nil, "", "   ", strings.Repeat("a", 101), strings.Repeat("😀", 51), "tab\tname", "Key\n"} {
		r := c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": name, "currentPassword": userPassword})
		wantAuthError(t, "begin", r, http.StatusBadRequest, "invalid_request", "A passkey name is required and must be at most 100 characters.")
	}
	longest := strings.Repeat("😀", 50) // 100 UTF-16 units
	enrollPasskey(t, c, userPassword, "  "+longest+"  ")
	if list := listPasskeys(t, c); len(list) != 1 || list[0].Name != longest {
		t.Errorf("passkeys = %+v, want one named with the trimmed name", list)
	}
}

// TestPasskeyEnrolment_IsCappedAtTenPasskeys, checked at begin and again at
// complete: 409 passkey_limit_reached.
func TestPasskeyEnrolment_IsCappedAtTenPasskeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id, c := passkeyAccount(t, h, "ten@example.test")
	for range 9 {
		insertPasskey(t, h, id)
	}
	first, second := beginPasskeyEnrolment(t, c, userPassword, "Tenth"), beginPasskeyEnrolment(t, c, userPassword, "Eleventh")
	complete := func(ceremony passkeyCeremony) *resp {
		return c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
			"ceremonyId": ceremony.CeremonyID, "credentialJson": newAuthenticator(t).create(t, ceremony.Options), "currentPassword": userPassword,
		})
	}
	if r := complete(first); r.status != http.StatusOK {
		t.Fatalf("the tenth: status %d body %s", r.status, r.body)
	}
	wantAuthError(t, "complete the eleventh", complete(second), http.StatusConflict, "passkey_limit_reached", "The maximum number of passkeys has been reached.")
	r := c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": "Key", "currentPassword": userPassword})
	wantAuthError(t, "begin the eleventh", r, http.StatusConflict, "passkey_limit_reached", "The maximum number of passkeys has been reached.")
	if n := h.count(t, `SELECT count(*) FROM identity.passkeys WHERE user_id = $1`, id); n != 10 {
		t.Errorf("%d passkeys, want 10", n)
	}
}

// TestPasskeyEnrolment_CapsLiveCeremoniesAtThree per user: the fourth is
// 429 rate_limited; a completed ceremony frees its place, expiry frees them
// all, and another user is not affected.
func TestPasskeyEnrolment_CapsLiveCeremoniesAtThree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "three@example.test")
	begin := func() *resp {
		return c.do(http.MethodPost, accountPasskeysPath+"/begin", map[string]any{"name": "Key", "currentPassword": userPassword})
	}
	var first passkeyCeremony
	for i := range 3 {
		r := begin()
		if r.status != http.StatusOK {
			t.Fatalf("begin %d: status %d", i+1, r.status)
		}
		if i == 0 {
			r.json(&first)
		}
	}
	wantAuthError(t, "the fourth", begin(), http.StatusTooManyRequests, "rate_limited", "Too many passkey ceremonies are active.")
	_, other := passkeyAccount(t, h, "another@example.test")
	beginPasskeyEnrolment(t, other, userPassword, "Key")

	r := c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
		"ceremonyId": first.CeremonyID, "credentialJson": newAuthenticator(t).create(t, first.Options), "currentPassword": userPassword,
	})
	if r.status != http.StatusOK {
		t.Fatalf("complete: status %d body %s", r.status, r.body)
	}
	if r := begin(); r.status != http.StatusOK {
		t.Errorf("after one completed: status %d", r.status)
	}
	wantAuthError(t, "full again", begin(), http.StatusTooManyRequests, "rate_limited", "Too many passkey ceremonies are active.")
	h.advance(5 * time.Minute)
	if r := begin(); r.status != http.StatusOK {
		t.Errorf("after expiry: status %d", r.status)
	}
}

// TestPasskeyEnrolment_CompletionRefusesWhatDoesNotVerify: a malformed
// request is 400 invalid_request; an attestation without user verification,
// from another origin, for another RP ID, malformed, or of a credential
// already registered is 400 invalid_passkey and spends the ceremony;
// another user's ceremony is 409 and stays theirs to complete.
func TestPasskeyEnrolment_CompletionRefusesWhatDoesNotVerify(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "attest@example.test")
	complete := func(c *client, ceremony uuid.UUID, credential any) *resp {
		return c.do(http.MethodPost, accountPasskeysPath+"/complete", map[string]any{
			"ceremonyId": ceremony, "credentialJson": credential, "currentPassword": userPassword,
		})
	}
	const invalid = "The passkey response is invalid."
	wantAuthError(t, "a nil ceremony id", complete(c, uuid.Nil, "{}"), http.StatusBadRequest, "invalid_request", invalid)
	wantAuthError(t, "no credential", complete(c, uuid.New(), nil), http.StatusBadRequest, "invalid_request", invalid)
	wantAuthError(t, "an unknown ceremony", complete(c, uuid.New(), "{}"), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")

	cases := []struct {
		name, message string
		answer        func(options json.RawMessage) string
	}{
		{"without user verification", "A user-verified passkey is required.", func(o json.RawMessage) string {
			a := newAuthenticator(t)
			a.noUV = true
			return a.create(t, o)
		}},
		{"from another origin", invalid, func(o json.RawMessage) string {
			a := newAuthenticator(t)
			a.origin = "http://evil.example.com"
			return a.create(t, o)
		}},
		{"for another RP ID", invalid, func(o json.RawMessage) string {
			a := newAuthenticator(t)
			a.rpID = "evil.example.com"
			return a.create(t, o)
		}},
		{"malformed", invalid, func(json.RawMessage) string { return `{"id":"AAAA","type":"public-key"}` }},
	}
	for _, tc := range cases {
		ceremony := beginPasskeyEnrolment(t, c, userPassword, "Key")
		wantAuthError(t, tc.name, complete(c, ceremony.CeremonyID, tc.answer(ceremony.Options)), http.StatusBadRequest, "invalid_passkey", tc.message)
		wantAuthError(t, tc.name+", replayed", complete(c, ceremony.CeremonyID, "{}"), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
	}

	// The same credential enrolled again.
	a := enrollPasskey(t, c, userPassword, "Key")
	ceremony := beginPasskeyEnrolment(t, c, userPassword, "Again")
	wantAuthError(t, "a registered credential", complete(c, ceremony.CeremonyID, a.create(t, ceremony.Options)), http.StatusBadRequest, "invalid_passkey", invalid)

	// Another user's ceremony.
	_, other := passkeyAccount(t, h, "someone-else@example.test")
	theirs := beginPasskeyEnrolment(t, other, userPassword, "Theirs")
	credential := newAuthenticator(t).create(t, theirs.Options)
	wantAuthError(t, "another user's ceremony", complete(c, theirs.CeremonyID, credential), http.StatusConflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.")
	if r := complete(other, theirs.CeremonyID, credential); r.status != http.StatusOK {
		t.Errorf("its owner completes it: status %d body %s", r.status, r.body)
	}
	if list := listPasskeys(t, c); len(list) != 1 {
		t.Errorf("%d passkeys, want 1", len(list))
	}
}

// TestPasskeys_ListShowsOnlyTheCallersPasskeys, oldest first.
func TestPasskeys_ListShowsOnlyTheCallersPasskeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "lister@example.test")
	_, other := passkeyAccount(t, h, "other-lister@example.test")
	if list := listPasskeys(t, c); list == nil || len(list) != 0 {
		t.Errorf("no passkeys yet: %+v, want []", list)
	}
	first := enrollPasskey(t, c, userPassword, "First")
	h.advance(time.Minute)
	second := enrollPasskey(t, c, userPassword, "Second")
	enrollPasskey(t, other, userPassword, "Theirs")
	list := listPasskeys(t, c)
	if len(list) != 2 || list[0].CredentialID != b64(first.id) || list[0].Name != "First" ||
		list[1].CredentialID != b64(second.id) || list[1].Name != "Second" {
		t.Errorf("passkeys = %+v", list)
	}
}

// Ported from IdentityMfaIntegrationTests.PasskeyRemovalAcceptsMaximumBoundedCredentialIdLength,
// extended to the bounds either side: 1023 bytes (1364 characters) is a
// credential id, which the caller does not hold, so 404; 1024 bytes, or
// something that is not unpadded base64url, is 400 invalid_request.
func TestPasskeyRemoval_AcceptsTheLongestCredentialID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, c := passkeyAccount(t, h, "credential-bound@example.test")
	remove := func(id string) *resp {
		return c.do(http.MethodDelete, accountPasskeysPath+"/"+id, map[string]any{"currentPassword": userPassword})
	}
	longest := b64(make([]byte, 1023))
	if len(longest) != 1364 {
		t.Fatalf("len = %d", len(longest))
	}
	if r := remove(longest); r.status != http.StatusNotFound || len(r.body) != 0 {
		t.Errorf("1023 bytes: status %d body %q, want the bare 404", r.status, r.body)
	}
	for _, id := range []string{b64(make([]byte, 1024)), "AAAA====", "not~base64"} {
		wantAuthError(t, id[:min(len(id), 12)], remove(id), http.StatusBadRequest, "invalid_request", "The passkey identifier is invalid.")
	}
}

// TestPasskeyRemoval_IsBoundToTheCallersCredential: the current password,
// then only the caller's own credential, matched by its id, is removed;
// another user's is the bare 404 and survives. A removed passkey signs no
// one in.
func TestPasskeyRemoval_IsBoundToTheCallersCredential(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "remover@example.test"
	_, c := passkeyAccount(t, h, email)
	a := enrollPasskey(t, c, userPassword, "Mine")
	otherID, other := passkeyAccount(t, h, "keeper@example.test")
	b := enrollPasskey(t, other, userPassword, "Theirs")
	remove := func(id []byte, password any) *resp {
		return c.do(http.MethodDelete, accountPasskeysPath+"/"+b64(id), map[string]any{"currentPassword": password})
	}

	if r := remove(b.id, userPassword); r.status != http.StatusNotFound {
		t.Errorf("another user's passkey: status %d", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.passkeys WHERE credential_id = $1 AND user_id = $2`, b.id, otherID); n != 1 {
		t.Errorf("another user's passkey is gone")
	}
	wantAuthError(t, "a wrong password", remove(a.id, "WrongPassword123"), http.StatusBadRequest, "reauthentication_required", "The current password is invalid.")
	r := remove(a.id, userPassword)
	if r.status != http.StatusNoContent || len(r.body) != 0 {
		t.Fatalf("remove: status %d body %s", r.status, r.body)
	}
	if list := listPasskeys(t, c); len(list) != 0 {
		t.Errorf("passkeys = %+v", list)
	}
	if r := remove(a.id, userPassword); r.status != http.StatusNotFound {
		t.Errorf("removed again: status %d", r.status)
	}
	wantAuthError(t, "sign in with the removed passkey", passkeyLogin(t, h.client(t), email, a), http.StatusUnauthorized, "invalid_credentials", passkeySignInFailed)
}

// TestPasskeys_EnrolmentAndRemovalLeaveOtherSessionsAndResetLinks applies
// the security-stamp rule where .NET applied it: ASP.NET's
// AddOrUpdatePasskeyAsync and RemovePasskeyAsync never rotated the stamp
// (they store the change and call UpdateUserAsync), so the user's other
// sessions and pending reset links survive both. The version, the
// concurrency stamp's analogue, rotates.
func TestPasskeys_EnrolmentAndRemovalLeaveOtherSessionsAndResetLinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "stamp@example.test"
	id, c := passkeyAccount(t, h, email)
	elsewhere := h.login(t, email, userPassword)
	h.exec(t, `INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		bytes.Repeat([]byte{7}, 32), id, h.now().Add(time.Hour))
	var before uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}

	a := enrollPasskey(t, c, userPassword, "Key")
	check := func(step string) {
		t.Helper()
		if r := elsewhere.do(http.MethodGet, sessionPath, nil); r.status != http.StatusOK {
			t.Errorf("%s: the other session got %d", step, r.status)
		}
		if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 1 {
			t.Errorf("%s: %d reset links, want 1", step, n)
		}
		var now uuid.UUID
		if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&now); err != nil {
			t.Fatal(err)
		}
		if now == before {
			t.Errorf("%s: the version did not rotate", step)
		}
		before = now
	}
	check("enrolment")
	if r := c.do(http.MethodDelete, accountPasskeysPath+"/"+b64(a.id), map[string]any{"currentPassword": userPassword}); r.status != http.StatusNoContent {
		t.Fatalf("remove: status %d", r.status)
	}
	check("removal")
}
