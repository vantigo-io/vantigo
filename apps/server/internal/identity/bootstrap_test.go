package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	bootstrapPath       = "/api/v1/identity/bootstrap"
	bootstrapStatusPath = "/api/v1/identity/bootstrap-status"
)

func bootstrapBody(secret, email string) map[string]string {
	return map[string]string{"secret": secret, "email": email, "displayName": "Integration Owner", "password": ownerPassword}
}

// bootstrapAvailable is GET /bootstrap-status's answer.
func bootstrapAvailable(t *testing.T, c *client) bool {
	t.Helper()
	r := c.do(http.MethodGet, bootstrapStatusPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("bootstrap-status: status %d", r.status)
	}
	var body struct {
		Available bool `json:"available"`
	}
	r.json(&body)
	return body.Available
}

// authUser is AuthUserResponse as a test reads it.
type authUser struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
	Email       *string   `json:"email"`
	Roles       []string  `json:"roles"`
}

// TestBootstrap_CreatesTheOwnerAndSignsThemIn proves the one-time bootstrap
// end to end: status available, 201 with Location and a non-persistent
// session cookie, the Owner signed in, the email trimmed but not confirmed,
// the marker and the audit event written, status no longer available, and
// neither the secret, the password nor the session token in any log line.
func TestBootstrap_CreatesTheOwnerAndSignsThemIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	if !bootstrapAvailable(t, c) {
		t.Fatal("bootstrap-status: not available on a fresh installation")
	}

	body := bootstrapBody(bootstrapSecret, "  Owner@Example.test  ")
	body["displayName"] = " Integration Owner "
	r := c.do(http.MethodPost, bootstrapPath, body)
	if r.status != http.StatusCreated {
		t.Fatalf("bootstrap: status %d body %s", r.status, r.body)
	}
	if got := r.header("Location"); got != "/api/v1/identity/session" {
		t.Errorf("Location = %q", got)
	}
	cookie := r.setCookie(identity.SessionCookieName)
	if cookie == nil || cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("session cookie = %+v, want a non-persistent HttpOnly SameSite=Strict cookie", cookie)
	}
	var created struct {
		User authUser `json:"user"`
	}
	r.json(&created)
	u := created.User
	if u.Email == nil || *u.Email != "Owner@Example.test" || u.DisplayName != "Integration Owner" || !slices.Equal(u.Roles, []string{"Owner"}) {
		t.Errorf("user = %+v, want the trimmed email and name with role Owner", u)
	}

	r = c.do(http.MethodGet, sessionPath, nil)
	var session struct {
		User          authUser `json:"user"`
		IsSystemAdmin bool     `json:"isSystemAdmin"`
	}
	r.json(&session)
	if r.status != http.StatusOK || session.User.ID != u.ID || !slices.Equal(session.User.Roles, []string{"Owner"}) || session.IsSystemAdmin {
		t.Errorf("session after bootstrap: status %d %+v", r.status, session)
	}
	if bootstrapAvailable(t, c) {
		t.Error("bootstrap-status: still available after bootstrap")
	}

	var confirmed bool
	var normalized string
	if err := h.pool.QueryRow(context.Background(), `SELECT email_confirmed, normalized_email FROM identity.users WHERE id = $1`, u.ID).Scan(&confirmed, &normalized); err != nil {
		t.Fatal(err)
	}
	if confirmed || normalized != "OWNER@EXAMPLE.TEST" {
		t.Errorf("email_confirmed %v normalized_email %q, want false and OWNER@EXAMPLE.TEST", confirmed, normalized)
	}
	var completedAt time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT completed_at FROM identity.bootstrap_state WHERE id = 1`).Scan(&completedAt); err != nil || !completedAt.Equal(start) {
		t.Errorf("bootstrap_state completed_at %v (%v), want %v", completedAt, err, start)
	}

	events := h.auditEvents(t, "bootstrap.owner-created")
	if len(events) != 1 {
		t.Fatalf("%d bootstrap.owner-created events, want 1", len(events))
	}
	e := events[0]
	if e.Actor != nil || e.TargetUser == nil || *e.TargetUser != u.ID || e.TargetRole == nil || *e.TargetRole != identity.RoleOwnerID || e.MFA || !e.At.Equal(start) {
		t.Errorf("audit event = %+v", e)
	}
	if e.Details != `{"action":"bootstrap.owner-created"}` ||
		e.Before != `{"UserId":null,"Roles":[],"PermissionKeys":[]}` ||
		e.After != `{"UserId":"`+u.ID.String()+`","Roles":["Owner"],"PermissionKeys":["*"]}` {
		t.Errorf("audit details %s before %s after %s", e.Details, e.Before, e.After)
	}

	for _, rec := range h.logRecords(t) {
		line, _ := json.Marshal(rec)
		for _, secret := range []string{bootstrapSecret, ownerPassword, cookie.Value} {
			if strings.Contains(string(line), secret) {
				t.Errorf("log record %s carries a secret", line)
			}
		}
	}
}

// TestBootstrap_ConfiguredSystemAdminEmailAlsoGetsSystemAdmin proves an
// Owner whose email is SYSTEM_ADMIN_EMAIL, compared case-insensitively, is
// also a SystemAdmin, listed in .NET's order in the 201 and in orderRoles
// order by /session.
func TestBootstrap_ConfiguredSystemAdminEmailAlsoGetsSystemAdmin(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("SYSTEM_ADMIN_EMAIL", " OWNER@example.TEST "))
	c := h.client(t)

	r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, ownerEmail))
	var created struct {
		User authUser `json:"user"`
	}
	r.json(&created)
	if r.status != http.StatusCreated || !slices.Equal(created.User.Roles, []string{"Owner", "SystemAdmin"}) {
		t.Fatalf("bootstrap: status %d roles %v", r.status, created.User.Roles)
	}

	r = c.do(http.MethodGet, sessionPath, nil)
	var session struct {
		User          authUser `json:"user"`
		IsSystemAdmin bool     `json:"isSystemAdmin"`
	}
	r.json(&session)
	if !session.IsSystemAdmin || !slices.Equal(session.User.Roles, []string{"SystemAdmin", "Owner"}) {
		t.Errorf("session: %+v", session)
	}
	if e := h.auditEvents(t, "bootstrap.owner-created"); len(e) != 1 || !strings.Contains(e[0].After, `"Roles":["Owner","SystemAdmin"]`) {
		t.Errorf("audit events = %+v", e)
	}
}

// TestBootstrap_InvalidRequestListsEveryProblem proves validation comes
// first, answering 400 invalid_request with .NET's message and one entry per
// field, before the secret is looked at.
func TestBootstrap_InvalidRequestListsEveryProblem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)

	cases := []struct {
		body any
		want map[string][]string
	}{
		{map[string]string{}, map[string][]string{
			"secret":      {"Secret is required."},
			"email":       {"A valid email is required."},
			"displayName": {"Display name is required."},
			"password":    {"Password is required."},
		}},
		{map[string]string{"secret": "wrong", "email": "no-at-sign", "displayName": " ", "password": ownerPassword}, map[string][]string{
			"email":       {"A valid email is required."},
			"displayName": {"Display name is required."},
		}},
		{map[string]string{"secret": "wrong", "email": ownerEmail, "displayName": strings.Repeat("é", 201), "password": ownerPassword}, map[string][]string{
			"displayName": {"Display name must be at most 200 characters."},
		}},
	}
	for _, tc := range cases {
		r := c.do(http.MethodPost, bootstrapPath, tc.body)
		var body struct {
			Error struct {
				Code    string              `json:"code"`
				Message string              `json:"message"`
				Fields  map[string][]string `json:"fields"`
			} `json:"error"`
		}
		r.json(&body)
		if r.status != http.StatusBadRequest || body.Error.Code != "invalid_request" || body.Error.Message != "The bootstrap request is invalid." {
			t.Errorf("%v: status %d body %s", tc.body, r.status, r.body)
			continue
		}
		if len(body.Error.Fields) != len(tc.want) {
			t.Errorf("%v: fields %v, want %v", tc.body, body.Error.Fields, tc.want)
		}
		for k, v := range tc.want {
			if !slices.Equal(body.Error.Fields[k], v) {
				t.Errorf("%v: field %s = %v, want %v", tc.body, k, body.Error.Fields[k], v)
			}
		}
	}
}

// TestBootstrap_WrongSecretIs401 proves the secret must match exactly: a
// wrong one and the right one with a trailing space both get 401
// invalid_secret, and bootstrap stays available.
func TestBootstrap_WrongSecretIs401(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)

	for _, secret := range []string{"not-the-secret", bootstrapSecret + " "} {
		r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(secret, ownerEmail))
		if r.status != http.StatusUnauthorized || r.code() != "invalid_secret" || !strings.Contains(string(r.body), "The bootstrap secret is invalid.") {
			t.Errorf("secret %q: status %d body %s, want 401 invalid_secret", secret, r.status, r.body)
		}
	}
	if !bootstrapAvailable(t, c) {
		t.Error("a refused bootstrap consumed it")
	}
	for what, sql := range map[string]string{
		"users":        `SELECT count(*) FROM identity.users`,
		"marker rows":  `SELECT count(*) FROM identity.bootstrap_state`,
		"audit events": `SELECT count(*) FROM identity.authorization_audit_events`,
	} {
		if n := h.count(t, sql); n != 0 {
			t.Errorf("%d %s after refused bootstraps, want none", n, what)
		}
	}
}

// TestBootstrap_SecondBootstrapIs409 proves bootstrap is one-time: once the
// Owner exists, another attempt with the right secret gets 409
// bootstrap_unavailable and creates nothing.
func TestBootstrap_SecondBootstrapIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)

	r := h.client(t).do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, "second@example.test"))
	if r.status != http.StatusConflict || r.code() != "bootstrap_unavailable" || !strings.Contains(string(r.body), "The local Owner has already been created.") {
		t.Errorf("second bootstrap: status %d body %s, want 409 bootstrap_unavailable", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want only the first Owner", n)
	}
}

// TestBootstrap_AnyOwnerConsumesIt proves bootstrap is consumed by any
// Owner, not only by its own marker (EA/AuthEndpoints.cs:491-504).
func TestBootstrap_AnyOwnerConsumesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.insertUser(t, "owner@elsewhere.test", identity.RoleOwnerID)
	c := h.client(t)

	if bootstrapAvailable(t, c) {
		t.Error("bootstrap-status: available although an Owner exists")
	}
	if r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, ownerEmail)); r.status != http.StatusConflict || r.code() != "bootstrap_unavailable" {
		t.Errorf("bootstrap: status %d code %q, want 409 bootstrap_unavailable", r.status, r.code())
	}
}

// TestBootstrap_ExistingAccountWithTheEmailIsAValidationFailure proves an
// account that already has the email (one provisioned before any Owner)
// makes bootstrap a 400 identity_validation_failed with ASP.NET's duplicate
// codes, not a false conflict, and leaves bootstrap available.
func TestBootstrap_ExistingAccountWithTheEmailIsAValidationFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.insertUser(t, "OWNER@example.test", identity.RoleUserID)
	c := h.client(t)

	r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, ownerEmail))
	var body struct {
		Error struct {
			Code    string              `json:"code"`
			Message string              `json:"message"`
			Fields  map[string][]string `json:"fields"`
		} `json:"error"`
	}
	r.json(&body)
	if r.status != http.StatusBadRequest || body.Error.Code != "identity_validation_failed" || body.Error.Message != "The Owner account could not be created." ||
		!slices.Equal(body.Error.Fields["DuplicateUserName"], []string{"Username 'owner@example.test' is already taken."}) ||
		!slices.Equal(body.Error.Fields["DuplicateEmail"], []string{"Email 'owner@example.test' is already taken."}) {
		t.Errorf("status %d body %s", r.status, r.body)
	}
	if !bootstrapAvailable(t, c) {
		t.Error("a refused bootstrap consumed it")
	}
}

// TestBootstrap_ConcurrentBootstrapsCreateExactlyOneOwner races two
// bootstraps with the right secret: exactly one gets 201 and the other 409
// bootstrap_unavailable, whether it loses on the owner lock's snapshot, a
// serialization failure or a unique violation. Run with -count to repeat
// the race.
func TestBootstrap_ConcurrentBootstrapsCreateExactlyOneOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	clients := []*client{h.client(t), h.client(t)}

	statuses := make([]int, len(clients))
	codes := make([]string, len(clients))
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i, c := range clients {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, "racer"+string(rune('a'+i))+"@example.test"))
			statuses[i], codes[i] = r.status, r.code()
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()

	got := slices.Clone(statuses)
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusCreated, http.StatusConflict}) {
		t.Fatalf("statuses %v (codes %v), want one 201 and one 409", statuses, codes)
	}
	if loser := codes[slices.Index(statuses, http.StatusConflict)]; loser != "bootstrap_unavailable" {
		t.Errorf("the loser's code = %q, want bootstrap_unavailable", loser)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE role_id = $1`, identity.RoleOwnerID); n != 1 {
		t.Errorf("%d Owners, want exactly 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Errorf("%d users, want exactly 1", n)
	}
}

// TestBootstrap_GeneratedDevelopmentSecretIsTheOneLogged proves that in
// development without BOOTSTRAP_SECRET the installation generates one
// secret when it mounts, logs it once at WARN, and that /bootstrap-status
// and /bootstrap both use exactly that secret.
func TestBootstrap_GeneratedDevelopmentSecretIsTheOneLogged(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("BOOTSTRAP_SECRET", ""))

	var secret string
	for _, rec := range h.logRecords(t) {
		if rec["msg"] == "bootstrap secret generated" {
			if secret != "" {
				t.Fatal("the secret was generated more than once")
			}
			if rec["level"] != "WARN" {
				t.Errorf("level = %v, want WARN", rec["level"])
			}
			secret, _ = rec["bootstrap_secret"].(string)
		}
	}
	if len(secret) < 43 {
		t.Fatalf("logged secret %q, want a generated one", secret)
	}

	c := h.client(t)
	if !bootstrapAvailable(t, c) {
		t.Error("bootstrap-status: not available with a generated secret")
	}
	if r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(bootstrapSecret, ownerEmail)); r.status != http.StatusUnauthorized {
		t.Errorf("the harness's usual secret: status %d, want 401", r.status)
	}
	if r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(secret, ownerEmail)); r.status != http.StatusCreated {
		t.Errorf("the logged secret: status %d body %s, want 201", r.status, r.body)
	}
}

// TestBootstrap_IsRateLimitedPerClient proves the Bootstrap IP policy: 20
// attempts a minute per client address, the 21st refused with Retry-After,
// another client unaffected.
func TestBootstrap_IsRateLimitedPerClient(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	for i := range 20 {
		if r := c.do(http.MethodPost, bootstrapPath, bootstrapBody("wrong", ownerEmail)); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, r.status)
		}
	}
	r := c.do(http.MethodPost, bootstrapPath, bootstrapBody("wrong", ownerEmail))
	if r.status != http.StatusTooManyRequests || r.code() != "rate_limited" || r.header("Retry-After") == "" {
		t.Errorf("21st attempt: status %d code %q Retry-After %q, want 429 rate_limited with Retry-After", r.status, r.code(), r.header("Retry-After"))
	}
	if r := h.client(t).do(http.MethodPost, bootstrapPath, bootstrapBody("wrong", ownerEmail)); r.status != http.StatusUnauthorized {
		t.Errorf("another client: status %d, want 401", r.status)
	}
}

// userVersion reads a user's version.
func userVersion(t *testing.T, h *harness, id uuid.UUID) uuid.UUID {
	t.Helper()
	var v uuid.UUID
	if err := h.pool.QueryRow(context.Background(), `SELECT version FROM identity.users WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func hasRole(t *testing.T, h *harness, userID, roleID uuid.UUID) bool {
	t.Helper()
	return h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, userID, roleID) == 1
}

// Ported from IdentitySystemAdminBootstrapperTests.MixedCaseConfiguredEmailUsesIdentityLookupNormalization.
// The configured email and the stored one differ in case (and the
// configured one in surrounding spaces); the lookup goes through the same
// normalization the store uses.
func TestRunStartup_MatchesTheConfiguredEmailCaseInsensitively(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := h.insertUser(t, "breakglass-1f2e@integration.test", identity.RoleUserID)
	// Confirmed: an unconfirmed, passwordless account is refused
	// (TestRunStartup_RefusesAnUnverifiedPasswordlessAccount).
	h.exec(t, `UPDATE identity.users SET email_confirmed = true WHERE id = $1`, id)

	if err := h.runStartup("  BreakGlass-1F2E@Integration.Test "); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if !hasRole(t, h, id, identity.RoleSystemAdminID) {
		t.Error("the matching account was not granted SystemAdmin")
	}
}

// New: hardening beyond .NET. An account workforce OIDC provisioned from an
// unverified email claim (Entra's), unconfirmed and passwordless, is never
// made SystemAdmin by asserting SYSTEM_ADMIN_EMAIL: startup fails with the
// reason, not the address, and nothing is granted or revoked.
func TestRunStartup_RefusesAnUnverifiedPasswordlessAccount(t *testing.T) {
	t.Parallel()
	const email = "claimed-admin-5d1c@integration.test"
	f := newFakeOIDC(t)
	f.set("email", email)
	f.unset("email_verified")
	h := newHarness(t, f.options()...)
	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	id := oidcSession(t, c).User.ID
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND NOT email_confirmed AND password_hash IS NULL`, id); n != 1 {
		t.Fatal("the provisioned account is not unconfirmed and passwordless")
	}

	err := h.runStartup(email)
	if err == nil || !strings.Contains(err.Error(), "unconfirmed") || strings.Contains(strings.ToLower(err.Error()), email) {
		t.Errorf("RunStartup = %v, want the unverified-account refusal without the address", err)
	}
	if hasRole(t, h, id, identity.RoleSystemAdminID) {
		t.Error("the unverified account was granted SystemAdmin")
	}
	if r := c.do(http.MethodGet, "/api/v1/identity/session", nil); r.status != http.StatusOK {
		t.Errorf("the account's session: status %d, want 200 (nothing revoked)", r.status)
	}
}

// New: the refusal spares the bootstrap Owner, whose email is unconfirmed
// but who has a local password: granted as before.
func TestRunStartup_GrantsTheUnconfirmedBootstrapOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, id := h.bootstrapOwner(t)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND NOT email_confirmed AND password_hash IS NOT NULL`, id); n != 1 {
		t.Fatal("the bootstrap Owner is not unconfirmed with a password")
	}
	if err := h.runStartup(ownerEmail); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if !hasRole(t, h, id, identity.RoleSystemAdminID) {
		t.Error("the bootstrap Owner was not granted SystemAdmin")
	}
}

// New: an account OIDC provisioned from a verified email claim is
// confirmed, and granted without a local password.
func TestRunStartup_GrantsAConfirmedAccountWithoutAPassword(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	c := h.client(t)
	assertOIDCRedirect(t, h.oidcSignIn(t, c, f), "/")
	id := oidcSession(t, c).User.ID
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email_confirmed AND password_hash IS NULL`, id); n != 1 {
		t.Fatal("the provisioned account is not confirmed and passwordless")
	}
	if err := h.runStartup(fakeEmail); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if !hasRole(t, h, id, identity.RoleSystemAdminID) {
		t.Error("the confirmed account was not granted SystemAdmin")
	}
}

// Ported from IdentitySystemAdminBootstrapperTests.MissingConfiguredAccountOnBootstrappedInstallationFailsStartup.
// An installation is in use once any user exists, or its bootstrap marker
// does.
func TestRunStartup_MissingAccountFailsOnABootstrappedInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	err := h.runStartup("missing-7c1d@integration.test")
	if err == nil || !strings.Contains(err.Error(), "break-glass administrator is missing") {
		t.Errorf("with users: RunStartup = %v, want the missing break-glass administrator", err)
	}

	markerOnly := newHarness(t)
	markerOnly.exec(t, `INSERT INTO identity.bootstrap_state (id, completed_at) VALUES (1, $1)`, start)
	if err := markerOnly.runStartup("missing-7c1d@integration.test"); err == nil || !strings.Contains(err.Error(), "break-glass administrator is missing") {
		t.Errorf("with only the marker: RunStartup = %v, want the missing break-glass administrator", err)
	}
}

// Ported from IdentitySystemAdminBootstrapperTests.MissingConfiguredAccountOnUnconsumedInstallationIsAllowed.
func TestRunStartup_MissingAccountIsAllowedOnAFreshInstallation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.runStartup("first-run-2b9a@integration.test"); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users, want none", n)
	}
	if !bootstrapAvailable(t, h.client(t)) {
		t.Error("bootstrap is no longer available")
	}
}

// Ported from IdentitySystemAdminBootstrapperTests.RepeatedRunsAreIdempotentAndUpdateStampsWhenGranting.
// Go's security stamp is the session table: the grant rotates the user's
// version and revokes their sessions; a repeat run changes neither.
func TestRunStartup_GrantIsIdempotentAndRevokesSessionsOnlyWhenGranting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email, password = "stamp-4d0e@integration.test", "StampPassword123"
	id := h.seedUser(t, email, password, identity.RoleUserID)
	before := userVersion(t, h, id)
	first := h.login(t, email, password)
	// A reset link the account holds, as a recovery request would mint it.
	holdResetLink := func() {
		h.exec(t, `INSERT INTO identity.password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
			[]byte(uuid.NewString()), id, start.Add(time.Hour))
	}
	resetLinks := func() int {
		return h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id)
	}
	holdResetLink()

	if err := h.runStartup(email); err != nil {
		t.Fatalf("first run: %v", err)
	}
	afterGrant := userVersion(t, h, id)
	if afterGrant == before {
		t.Error("the grant did not rotate the version")
	}
	if !hasRole(t, h, id, identity.RoleSystemAdminID) {
		t.Fatal("the account was not granted SystemAdmin")
	}
	rejected(t, first)
	if n := resetLinks(); n != 0 {
		t.Errorf("%d reset links survived the grant, want 0", n)
	}

	second := h.login(t, email, password)
	holdResetLink()
	if err := h.runStartup(email); err != nil {
		t.Fatalf("repeat run: %v", err)
	}
	if got := userVersion(t, h, id); got != afterGrant {
		t.Error("the repeat run rotated the version")
	}
	admitted(t, second)
	if n := resetLinks(); n != 1 {
		t.Errorf("%d reset links after the repeat run, want the 1 it left alone", n)
	}
}

// Ported from IdentitySystemAdminBootstrapperTests.ExistingUserCreatedSystemAdminRoleConflictsWithProtectedMetadata.
// Go's built-in roles are migration rows with fixed ids, so a conflict is
// a role named SystemAdmin that is not that protected row: a user-created
// one in its place, or the row itself stripped of its protection. Either
// fails startup, even with SYSTEM_ADMIN_EMAIL unset.
func TestRunStartup_UserCreatedSystemAdminRoleFailsClosed(t *testing.T) {
	t.Parallel()

	replaced := newHarness(t)
	replaced.exec(t, `DELETE FROM identity.roles WHERE id = $1`, identity.RoleSystemAdminID)
	replaced.exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, description, is_system, is_built_in, version, created_at, updated_at)
	                  VALUES ($1, 'SystemAdmin', 'SYSTEMADMIN', 'User-created SystemAdmin', 'Not protected', false, false, $2, $3, $3)`,
		uuid.New(), uuid.New(), start)
	if err := replaced.runStartup(""); err == nil || !strings.Contains(err.Error(), "conflicting metadata") {
		t.Errorf("user-created SystemAdmin: RunStartup = %v, want conflicting metadata", err)
	}

	stripped := newHarness(t)
	stripped.exec(t, `UPDATE identity.roles SET is_system = false WHERE id = $1`, identity.RoleSystemAdminID)
	if err := stripped.runStartup(""); err == nil || !strings.Contains(err.Error(), "conflicting metadata") {
		t.Errorf("unprotected SystemAdmin row: RunStartup = %v, want conflicting metadata", err)
	}
}

// TestRunStartup_WithoutAnEmailGrantsNothing proves SYSTEM_ADMIN_EMAIL
// unset leaves every account as it is.
func TestRunStartup_WithoutAnEmailGrantsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)
	if err := h.runStartup(""); err != nil {
		t.Fatalf("RunStartup: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE role_id = $1`, identity.RoleSystemAdminID); n != 0 {
		t.Errorf("%d SystemAdmins, want none", n)
	}
}
