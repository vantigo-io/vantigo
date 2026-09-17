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

func bootstrapBody(email string) map[string]string {
	return map[string]string{"email": email, "displayName": "Integration Owner", "password": ownerPassword}
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
// end to end, with no secret asked for: status available, 201 with Location
// and a non-persistent session cookie, the Owner signed in holding both
// Owner and SystemAdmin (listed in .NET's order in the 201 and in
// orderRoles order by /session), the email trimmed but not confirmed, the
// marker and the audit event written, status no longer available, and
// neither the password nor the session token in any log line.
func TestBootstrap_CreatesTheOwnerAndSignsThemIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	if !bootstrapAvailable(t, c) {
		t.Fatal("bootstrap-status: not available on a fresh installation")
	}

	body := bootstrapBody("  Owner@Example.test  ")
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
	if u.Email == nil || *u.Email != "Owner@Example.test" || u.DisplayName != "Integration Owner" || !slices.Equal(u.Roles, []string{"Owner", "SystemAdmin"}) {
		t.Errorf("user = %+v, want the trimmed email and name with roles Owner and SystemAdmin", u)
	}

	r = c.do(http.MethodGet, sessionPath, nil)
	var session struct {
		User          authUser `json:"user"`
		IsSystemAdmin bool     `json:"isSystemAdmin"`
	}
	r.json(&session)
	if r.status != http.StatusOK || session.User.ID != u.ID || !slices.Equal(session.User.Roles, []string{"SystemAdmin", "Owner"}) || !session.IsSystemAdmin {
		t.Errorf("session after bootstrap: status %d %+v, want a SystemAdmin Owner", r.status, session)
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
		e.After != `{"UserId":"`+u.ID.String()+`","Roles":["Owner","SystemAdmin"],"PermissionKeys":["*"]}` {
		t.Errorf("audit details %s before %s after %s", e.Details, e.Before, e.After)
	}

	for _, rec := range h.logRecords(t) {
		line, _ := json.Marshal(rec)
		for _, secret := range []string{ownerPassword, cookie.Value} {
			if strings.Contains(string(line), secret) {
				t.Errorf("log record %s carries a secret", line)
			}
		}
	}
}

// TestBootstrap_DisplayNameLengthCountsUTF16Units: the 200-character bound
// counts UTF-16 code units, as .NET's string.Length did and every other
// display-name bound here does (utf16Length). An emoji outside the Basic
// Multilingual Plane is one rune but two units: 101 of them (202 units) are
// refused, 100 (200 units) are accepted.
func TestBootstrap_DisplayNameLengthCountsUTF16Units(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)

	body := bootstrapBody(ownerEmail)
	body["displayName"] = strings.Repeat("😀", 101)
	r := c.do(http.MethodPost, bootstrapPath, body)
	var refused struct {
		Error struct {
			Code   string              `json:"code"`
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}
	r.json(&refused)
	if r.status != http.StatusBadRequest || refused.Error.Code != "invalid_request" ||
		!slices.Equal(refused.Error.Fields["displayName"], []string{"Display name must be at most 200 characters."}) {
		t.Fatalf("101 emoji (202 UTF-16 units): status %d body %s, want 400 on displayName", r.status, r.body)
	}

	body["displayName"] = strings.Repeat("😀", 100)
	r = c.do(http.MethodPost, bootstrapPath, body)
	var created struct {
		User authUser `json:"user"`
	}
	r.json(&created)
	if r.status != http.StatusCreated || created.User.DisplayName != body["displayName"] {
		t.Errorf("100 emoji (200 UTF-16 units): status %d body %s, want 201 with the name kept", r.status, r.body)
	}
}

// TestBootstrap_InvalidRequestListsEveryProblem proves validation comes
// first, answering 400 invalid_request with .NET's message and one entry per
// field, and that a refused bootstrap creates nothing and stays available.
func TestBootstrap_InvalidRequestListsEveryProblem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)

	cases := []struct {
		body any
		want map[string][]string
	}{
		{map[string]string{}, map[string][]string{
			"email":       {"A valid email is required."},
			"displayName": {"Display name is required."},
			"password":    {"Password is required."},
		}},
		{map[string]string{"email": "no-at-sign", "displayName": " ", "password": ownerPassword}, map[string][]string{
			"email":       {"A valid email is required."},
			"displayName": {"Display name is required."},
		}},
		{map[string]string{"email": ownerEmail, "displayName": strings.Repeat("é", 201), "password": ownerPassword}, map[string][]string{
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
// Owner exists, another attempt gets 409 bootstrap_unavailable and creates
// nothing.
func TestBootstrap_SecondBootstrapIs409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.bootstrapOwner(t)

	r := h.client(t).do(http.MethodPost, bootstrapPath, bootstrapBody("second@example.test"))
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
	if r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(ownerEmail)); r.status != http.StatusConflict || r.code() != "bootstrap_unavailable" {
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

	r := c.do(http.MethodPost, bootstrapPath, bootstrapBody(ownerEmail))
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
// bootstraps: exactly one gets 201 and the other 409
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
			r := c.do(http.MethodPost, bootstrapPath, bootstrapBody("racer"+string(rune('a'+i))+"@example.test"))
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

// TestBootstrap_IsRateLimitedPerClient proves the Bootstrap IP policy: 20
// attempts a minute per client address (invalid ones, so none succeeds),
// the 21st refused with Retry-After, another client unaffected.
func TestBootstrap_IsRateLimitedPerClient(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	for i := range 20 {
		if r := c.do(http.MethodPost, bootstrapPath, map[string]string{}); r.status != http.StatusBadRequest {
			t.Fatalf("attempt %d: status %d, want 400", i+1, r.status)
		}
	}
	r := c.do(http.MethodPost, bootstrapPath, map[string]string{})
	if r.status != http.StatusTooManyRequests || r.code() != "rate_limited" || r.header("Retry-After") == "" {
		t.Errorf("21st attempt: status %d code %q Retry-After %q, want 429 rate_limited with Retry-After", r.status, r.code(), r.header("Retry-After"))
	}
	if r := h.client(t).do(http.MethodPost, bootstrapPath, map[string]string{}); r.status != http.StatusBadRequest {
		t.Errorf("another client: status %d, want 400", r.status)
	}
}

// Ported from IdentitySystemAdminBootstrapperTests.ExistingUserCreatedSystemAdminRoleConflictsWithProtectedMetadata.
// Go's built-in roles are migration rows with fixed ids, so a conflict is
// a role named SystemAdmin that is not that protected row: a user-created
// one in its place, or the row itself stripped of its protection. Either
// fails startup.
func TestRunStartup_UserCreatedSystemAdminRoleFailsClosed(t *testing.T) {
	t.Parallel()

	replaced := newHarness(t)
	replaced.exec(t, `DELETE FROM identity.roles WHERE id = $1`, identity.RoleSystemAdminID)
	replaced.exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, description, is_system, is_built_in, version, created_at, updated_at)
	                  VALUES ($1, 'SystemAdmin', 'SYSTEMADMIN', 'User-created SystemAdmin', 'Not protected', false, false, $2, $3, $3)`,
		uuid.New(), uuid.New(), start)
	if err := replaced.runStartup(); err == nil || !strings.Contains(err.Error(), "conflicting metadata") {
		t.Errorf("user-created SystemAdmin: RunStartup = %v, want conflicting metadata", err)
	}

	stripped := newHarness(t)
	stripped.exec(t, `UPDATE identity.roles SET is_system = false WHERE id = $1`, identity.RoleSystemAdminID)
	if err := stripped.runStartup(); err == nil || !strings.Contains(err.Error(), "conflicting metadata") {
		t.Errorf("unprotected SystemAdmin row: RunStartup = %v, want conflicting metadata", err)
	}
}

// TestRunStartup_WarnsWhileBootstrapIsOpen proves startup logs a WARN while
// no Owner exists, since /setup then makes its first visitor the
// administrator, and stays quiet once bootstrap is consumed.
func TestRunStartup_WarnsWhileBootstrapIsOpen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	warnings := func() int {
		n := 0
		for _, rec := range h.logRecords(t) {
			if msg, _ := rec["msg"].(string); strings.Contains(msg, "no Owner yet") {
				if rec["level"] != "WARN" {
					t.Errorf("level = %v, want WARN", rec["level"])
				}
				n++
			}
		}
		return n
	}

	if err := h.runStartup(); err != nil {
		t.Fatalf("fresh installation: RunStartup: %v", err)
	}
	if n := warnings(); n != 1 {
		t.Errorf("%d warnings on a fresh installation, want 1", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 0 {
		t.Errorf("%d users after startup, want none: startup creates nobody", n)
	}

	h.bootstrapOwner(t)
	if err := h.runStartup(); err != nil {
		t.Fatalf("bootstrapped installation: RunStartup: %v", err)
	}
	if n := warnings(); n != 1 {
		t.Errorf("%d warnings after bootstrap, want the fresh installation's 1 only", n)
	}
}
