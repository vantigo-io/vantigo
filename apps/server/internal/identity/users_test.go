package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const ownerUsersPath = "/api/v1/identity/owner/users"

func userPath(id uuid.UUID, suffix string) string {
	return ownerUsersPath + "/" + id.String() + suffix
}

// ownerUser is OwnerUserResponse as a test reads it.
type ownerUser struct {
	ID               uuid.UUID  `json:"id"`
	DisplayName      string     `json:"displayName"`
	Email            *string    `json:"email"`
	Role             string     `json:"role"`
	Active           bool       `json:"active"`
	Disabled         bool       `json:"disabled"`
	LockedOut        bool       `json:"lockedOut"`
	LockoutEnd       *time.Time `json:"lockoutEnd"`
	TwoFactorEnabled bool       `json:"twoFactorEnabled"`
	AvatarURL        *string    `json:"avatarUrl"`
	SsoEnabled       bool       `json:"ssoEnabled"`
}

// ownerUsers lists the users as owner.
func ownerUsers(t *testing.T, owner *client) []ownerUser {
	t.Helper()
	r := owner.do(http.MethodGet, ownerUsersPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list users: status %d body %s", r.status, r.body)
	}
	var list []ownerUser
	r.json(&list)
	return list
}

// apiError is an AuthErrorResponse's error as a test reads it.
type apiError struct {
	Code    string              `json:"code"`
	Message string              `json:"message"`
	Fields  map[string][]string `json:"fields"`
}

// errorOf is r's AuthErrorResponse error, or the zero value for any other
// body.
func errorOf(t *testing.T, r *resp) apiError {
	t.Helper()
	var body struct {
		Error apiError `json:"error"`
	}
	_ = json.Unmarshal(r.body, &body)
	return body.Error
}

func fieldsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// mailedLink is the link that ends the last mail sent to `to`.
func mailedLink(t *testing.T, h *harness, to string) *url.URL {
	t.Helper()
	msgs := h.mailTo(to)
	if len(msgs) == 0 {
		t.Fatalf("no mail to %s", to)
	}
	body := msgs[len(msgs)-1].TextBody
	link, err := url.Parse(body[strings.LastIndex(body, " ")+1:])
	if err != nil {
		t.Fatalf("the mail to %s ends with no link: %q", to, body)
	}
	return link
}

// race runs fns at once, each released only when all are ready, and
// returns their responses in order.
func race(fns ...func() *resp) []*resp {
	out := make([]*resp, len(fns))
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i, fn := range fns {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			out[i] = fn()
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()
	return out
}

// Ported from IdentityAccountEndpointsTests.OwnerManagement_IsUnauthorizedForAnonymousAndForbiddenForStandardUser,
// extended from the list to every owner user operation.
func TestOwnerUsers_AnonymousIs401AndStandardUserIs403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "user@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	standard := h.login(t, email, userPassword)
	anonymous := h.client(t)

	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, ownerUsersPath, nil},
		{http.MethodPost, ownerUsersPath, map[string]string{"displayName": "New", "email": "new@example.test", "role": identity.RoleUser, "password": userPassword}},
		{http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Edited", "email": email, "role": identity.RoleOwner}},
		{http.MethodDelete, userPath(id, ""), nil},
		{http.MethodPost, userPath(id, "/disable"), nil},
		{http.MethodPost, userPath(id, "/enable"), nil},
		{http.MethodPost, userPath(id, "/password"), map[string]string{"password": newUserPassword}},
		{http.MethodPost, userPath(id, "/password-reset"), nil},
		{http.MethodGet, userPath(id, "/avatar"), nil},
	} {
		if r := anonymous.do(op.method, op.path, op.body); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
			t.Errorf("anonymous %s %s: status %d code %q, want 401 unauthenticated", op.method, op.path, r.status, r.code())
		}
		if r := standard.do(op.method, op.path, op.body); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("standard %s %s: status %d code %q, want 403 forbidden", op.method, op.path, r.status, r.code())
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND display_name = $2 AND NOT is_disabled`, id, email); n != 1 {
		t.Errorf("a refused caller changed the user")
	}
	admitted(t, standard)
	if len(h.mail.Messages()) != 0 {
		t.Errorf("a refused caller sent mail")
	}
}

// Ported from IdentityAccountEndpointsTests.OwnerCannotMutateOwnAccount,
// extended to every operation that names a user.
func TestOwnerUsers_OwnerCannotManageTheirOwnAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	for _, op := range []struct {
		method, suffix string
		body           any
	}{
		{http.MethodPut, "", map[string]string{"displayName": "Attempted self mutation", "email": ownerEmail, "role": identity.RoleOwner}},
		{http.MethodDelete, "", nil},
		{http.MethodPost, "/disable", nil},
		{http.MethodPost, "/enable", nil},
		{http.MethodPost, "/password", map[string]string{"password": newUserPassword}},
		{http.MethodPost, "/password-reset", nil},
	} {
		r := owner.do(op.method, userPath(ownerID, op.suffix), op.body)
		if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_request" ||
			e.Message != "Use the self-service account endpoints for your own account." {
			t.Errorf("%s self%s: status %d error %+v, want 400 invalid_request", op.method, op.suffix, r.status, e)
		}
	}
	admitted(t, owner)
	h.login(t, ownerEmail, ownerPassword)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND display_name = 'Integration Owner' AND NOT is_disabled`, ownerID); n != 1 {
		t.Errorf("the Owner's account changed")
	}
	if len(h.mail.Messages()) != 0 || h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action LIKE 'user.%'`) != 0 {
		t.Errorf("a refused self-management sent mail or wrote an audit event")
	}
}

// Ported from IdentityAccountEndpointsTests.UserEndpoints_ReturnCoherentContractAndDeterministicOwnerOrdering,
// plus the exact ordering, the Location, the audit event and the new
// account's sign-in.
func TestOwnerUsers_CreateListsTheUserAndOrdersOwnersFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	r := owner.do(http.MethodPost, ownerUsersPath, map[string]string{
		"displayName": "  Managed User ", "email": "managed@example.test", "role": identity.RoleUser, "password": userPassword,
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.status, r.body)
	}
	var created ownerUser
	r.json(&created)
	if got, want := r.header("Location"), userPath(created.ID, ""); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if str(created.Email) != "managed@example.test" || created.DisplayName != "Managed User" || created.Role != identity.RoleUser ||
		!created.Active || created.Disabled || created.LockedOut || created.LockoutEnd != nil || created.TwoFactorEnabled ||
		created.AvatarURL != nil || created.SsoEnabled {
		t.Errorf("created = %+v", created)
	}
	beta := h.createUser(t, owner, "beta@example.test", identity.RoleOwner)
	alpha := h.createUser(t, owner, "alpha@example.test", identity.RoleUser)

	// Owners first, then display names in ordinal order: upper case sorts
	// before lower case.
	var order []uuid.UUID
	for _, u := range ownerUsers(t, owner) {
		order = append(order, u.ID)
	}
	if want := []uuid.UUID{ownerID, beta, created.ID, alpha}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v (Integration Owner, beta, Managed User, alpha)", order, want)
	}

	invalid := owner.do(http.MethodPost, ownerUsersPath, map[string]string{
		"displayName": "Invalid role", "email": "invalid-role@example.test", "role": "Administrator", "password": userPassword,
	})
	if e := errorOf(t, invalid); invalid.status != http.StatusBadRequest ||
		!fieldsEqual(e.Fields, map[string][]string{"role": {"Role must be User or Owner."}}) {
		t.Errorf("invalid role: status %d error %+v, want 400 on role", invalid.status, e)
	}

	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email_confirmed AND normalized_email = 'MANAGED@EXAMPLE.TEST'`, created.ID); n != 1 {
		t.Errorf("the created account's email is not confirmed")
	}
	admitted(t, h.login(t, "managed@example.test", userPassword))

	events := h.auditEvents(t, "user.created-with-role")
	if len(events) != 3 {
		t.Fatalf("%d user.created-with-role events, want 3", len(events))
	}
	e := events[0]
	wantAfter := `{"UserId":"` + created.ID.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}`
	if e.Actor == nil || *e.Actor != ownerID || e.TargetUser == nil || *e.TargetUser != created.ID || e.TargetRole != nil ||
		e.Before != `{"UserId":null,"Roles":[],"PermissionKeys":[]}` || e.After != wantAfter || e.Details != `{"action":"user.created-with-role"}` {
		t.Errorf("audit = %+v, want the Owner creating the user with after %s", e, wantAfter)
	}
	if want := `{"UserId":"` + beta.String() + `","Roles":["Owner"],"PermissionKeys":["*"],"IsDisabled":false,"IsDeleted":false}`; events[1].After != want {
		t.Errorf("the Owner's after = %s, want %s", events[1].After, want)
	}
}

// TestOwnerUsers_CreateRefusesInvalidRequestsAndExistingEmails covers each
// field rule, the temporary password, and the existing-email conflict;
// no refused request creates anything.
func TestOwnerUsers_CreateRefusesInvalidRequestsAndExistingEmails(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	valid := func(overrides ...string) map[string]string {
		body := map[string]string{"displayName": "Someone", "email": "someone@example.test", "role": identity.RoleUser, "password": userPassword}
		for i := 0; i+1 < len(overrides); i += 2 {
			body[overrides[i]] = overrides[i+1]
		}
		return body
	}
	const emailProblem = "A valid email is required."
	for _, c := range []struct {
		body map[string]string
		want map[string][]string
	}{
		{valid("displayName", "", "email", "", "role", "", "password", ""), map[string][]string{
			"displayName": {"Display name is required and must be at most 200 characters."},
			"email":       {emailProblem},
			"role":        {"Role must be User or Owner."},
			"password":    {"Password is required."},
		}},
		{valid("email", " someone@example.test"), map[string][]string{"email": {emailProblem}}},
		{valid("email", "some one@example.test"), map[string][]string{"email": {emailProblem}}},
		{valid("email", "two@@example.test"), map[string][]string{"email": {emailProblem}}},
		{valid("email", "@example.test"), map[string][]string{"email": {emailProblem}}},
		{valid("email", "trailing@"), map[string][]string{"email": {emailProblem}}},
		{valid("email", strings.Repeat("e", 245)+"@example.test"), map[string][]string{"email": {emailProblem}}},
		{valid("displayName", strings.Repeat("n", 201)), map[string][]string{"displayName": {"Display name is required and must be at most 200 characters."}}},
		{valid("role", "user"), map[string][]string{"role": {"Role must be User or Owner."}}},
		{valid("password", strings.Repeat("p", 257)), map[string][]string{"password": {"Password must be 256 characters or fewer."}}},
	} {
		r := owner.do(http.MethodPost, ownerUsersPath, c.body)
		if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_request" || e.Message != "The user request is invalid." || !fieldsEqual(e.Fields, c.want) {
			t.Errorf("%v: status %d error %+v, want 400 invalid_request with %v", c.body, r.status, e, c.want)
		}
	}

	r := owner.do(http.MethodPost, ownerUsersPath, valid("email", "OWNER@EXAMPLE.TEST"))
	if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "account_exists" || e.Message != "An account already exists for this email address." {
		t.Errorf("existing email: status %d error %+v, want 409 account_exists", r.status, e)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users`); n != 1 {
		t.Fatalf("%d users after the refusals, want only the Owner", n)
	}

	// A temporary password, when given, is the password.
	r = owner.do(http.MethodPost, ownerUsersPath, map[string]string{
		"displayName": "Temporary", "email": "temporary@example.test", "role": identity.RoleUser, "temporaryPassword": "TemporaryPassword123",
	})
	if r.status != http.StatusCreated {
		t.Fatalf("temporary password: status %d body %s", r.status, r.body)
	}
	h.login(t, "temporary@example.test", "TemporaryPassword123")
}

// TestOwnerUsers_CreatingAUserRevokesTheEmailsInvitation: an account made
// directly supersedes a pending invitation for its email.
func TestOwnerUsers_CreatingAUserRevokesTheEmailsInvitation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	inv, token := invite(t, h, owner, map[string]string{"email": "both@example.test", "role": identity.RoleUser})
	h.createUser(t, owner, "both@example.test", identity.RoleUser)
	if list := ownerInvitations(t, owner); len(list) != 1 || list[0].ID != inv.ID || list[0].RevokedAt == nil || !list[0].RevokedAt.Equal(start) {
		t.Errorf("invitations = %+v, want the invitation revoked at the clock", list)
	}
	if validateToken(t, h.client(t), token).Valid {
		t.Errorf("the invitation still validates")
	}
}

// Ported from IdentityAccountEndpointsTests.Disable_PreservesTransientLockoutAndRejectsLoginAndStaleBusinessSession.
// .NET's stale business session called /api/v1/customers; identity's own
// ActiveAccount endpoint, GET /account, stands in for it. Enabling is
// covered here too: it ends every session again, so one started while the
// user was disabled never comes back.
func TestOwnerUsers_DisableKeepsTheLockoutAndEndsSessions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const email = "target@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	target := h.login(t, email, userPassword)
	lockoutEnd := start.Add(10 * time.Minute)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, id, lockoutEnd)

	r := owner.do(http.MethodPost, userPath(id, "/disable"), nil)
	if r.status != http.StatusOK {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	var disabled ownerUser
	r.json(&disabled)
	if !disabled.Disabled || !disabled.LockedOut || disabled.Active || disabled.LockoutEnd == nil || !disabled.LockoutEnd.Equal(lockoutEnd) {
		t.Errorf("disabled = %+v, want disabled, still locked out until %v, inactive", disabled, lockoutEnd)
	}
	rejected(t, target)
	if r := target.do(http.MethodGet, accountPath, nil); r.status != http.StatusUnauthorized {
		t.Errorf("GET /account with the old session: status %d, want 401", r.status)
	}
	login := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword))
	if e := errorOf(t, login); login.status != http.StatusTooManyRequests || e.Code != "account_locked" || strings.Contains(strings.ToLower(e.Message), "disabled") {
		t.Errorf("login: status %d error %+v, want 429 account_locked not naming the disable", login.status, e)
	}
	before := `{"UserId":"` + id.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}`
	after := `{"UserId":"` + id.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":true,"IsDeleted":false}`
	events := h.auditEvents(t, "user.disabled")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || *events[0].TargetUser != id || events[0].Before != before || events[0].After != after {
		t.Fatalf("user.disabled events = %+v, want one from %s to %s", events, before, after)
	}
	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK || len(h.auditEvents(t, "user.disabled")) != 1 {
		t.Errorf("disabling again: status %d, or a second audit event", r.status)
	}

	stale := h.signIn(t, id, false) // started while disabled, as no endpoint can
	h.advance(11 * time.Minute)     // past the lockout
	r = owner.do(http.MethodPost, userPath(id, "/enable"), nil)
	if r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	var enabled ownerUser
	r.json(&enabled)
	if enabled.Disabled || enabled.LockedOut || !enabled.Active {
		t.Errorf("enabled = %+v, want active", enabled)
	}
	rejected(t, stale)
	events = h.auditEvents(t, "user.enabled")
	if len(events) != 1 || events[0].Before != after || events[0].After != before {
		t.Errorf("user.enabled events = %+v, want one from %s to %s", events, after, before)
	}
	admitted(t, h.login(t, email, userPassword))
	if r := owner.do(http.MethodPost, userPath(id, "/enable"), nil); r.status != http.StatusOK || len(h.auditEvents(t, "user.enabled")) != 1 {
		t.Errorf("enabling again: status %d, or a second audit event", r.status)
	}
}

// Ported from IdentityAccountEndpointsTests.ConcurrentOwnerDemotions_LeaveOneActiveOwnerAndReturnConflict,
// tightened. .NET's acting Owner stayed active, so both demotions were
// legal and the test let one or two succeed. Here the acting Owner is
// locked out, which keeps their session but makes them an inactive Owner,
// so the two targets are the last two active Owners: exactly one demotion
// succeeds and the other is 409 last_active_owner, whether it waited on the
// owner lock or lost a serialization race and was retried. Run with -count
// to repeat the race.
func TestOwnerUsers_ConcurrentDemotionsOfTheLastTwoActiveOwnersLeaveOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	first := h.createUser(t, owner, "first-owner@example.test", identity.RoleOwner)
	second := h.createUser(t, owner, "second-owner@example.test", identity.RoleOwner)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, ownerID, start.Add(time.Hour))

	demote := func(id uuid.UUID, email string) func() *resp {
		return func() *resp {
			return owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Demoted", "email": email, "role": identity.RoleUser})
		}
	}
	responses := race(demote(first, "first-owner@example.test"), demote(second, "second-owner@example.test"))

	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Fatalf("statuses %v (bodies %s / %s), want one 200 and one 409", statuses, responses[0].body, responses[1].body)
	}
	for _, r := range responses {
		if r.status != http.StatusConflict {
			continue
		}
		if e := errorOf(t, r); e.Code != "last_active_owner" || e.Message != "The last active Owner cannot be demoted." {
			t.Errorf("the loser's error = %+v, want last_active_owner", e)
		}
	}
	active := h.count(t, `
		SELECT count(*) FROM identity.user_roles ur JOIN identity.users u ON u.id = ur.user_id
		WHERE ur.role_id = $1 AND NOT u.is_disabled AND (u.lockout_end IS NULL OR u.lockout_end <= $2)`, identity.RoleOwnerID, h.now())
	if active != 1 {
		t.Errorf("%d active Owners, want exactly 1", active)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = ANY($1) AND role_id = $2`, []uuid.UUID{first, second}, identity.RoleUserID); n != 1 {
		t.Errorf("%d of the two targets hold User, want exactly the demoted one", n)
	}
}

// TestOwnerUsers_ADemotionThatQueuedOnTheOwnerLockIsRetriedToLastActiveOwner
// forces the race's worst interleaving on every run. The test holds the
// owner lock while both demotions of the last two active Owners queue
// behind it, so each takes its SERIALIZABLE snapshot before the other
// commits. Released, the first demotes; the second, still seeing two
// active Owners, demotes too and loses a serialization race, and only its
// retry from a fresh snapshot answers 409 last_active_owner. Without the
// retry it would be 409 account_conflict. Run with -count to repeat.
func TestOwnerUsers_ADemotionThatQueuedOnTheOwnerLockIsRetriedToLastActiveOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	first := h.createUser(t, owner, "first-owner@example.test", identity.RoleOwner)
	second := h.createUser(t, owner, "second-owner@example.test", identity.RoleOwner)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, ownerID, start.Add(time.Hour))

	ctx := context.Background()
	gate, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(identity.OwnerMutationLock)); err != nil {
		t.Fatalf("gate: take the owner lock: %v", err)
	}

	demote := func(id uuid.UUID, email string) func() *resp {
		return func() *resp {
			return owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Demoted", "email": email, "role": identity.RoleUser})
		}
	}
	done := make(chan []*resp, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(demote(first, "first-owner@example.test"), demote(second, "second-owner@example.test"))
		close(finished)
	}()

	awaitLockWaiters(t, h, 2, finished) // both demotions are queued on the owner lock
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release the owner lock: %v", err)
	}
	responses := <-done

	statuses := []int{responses[0].status, responses[1].status}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Fatalf("statuses %v (bodies %s / %s), want one 200 and one 409", statuses, responses[0].body, responses[1].body)
	}
	for _, r := range responses {
		if r.status == http.StatusConflict && r.code() != "last_active_owner" {
			t.Errorf("the queued loser's code = %q, want last_active_owner", r.code())
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles WHERE user_id = ANY($1) AND role_id = $2`, []uuid.UUID{first, second}, identity.RoleOwnerID); n != 1 {
		t.Errorf("%d of the two targets still hold Owner, want exactly 1", n)
	}
}

// TestOwnerUsers_EditsSpendTheUsersResetLinks: an Owner's edit rotates
// what .NET's security stamp guarded, so a reset link mailed before it is
// dead. After a display-name edit; and after an email change, the case
// that matters: the link went to the replaced (say, compromised) mailbox
// and is submitted with the new address.
func TestOwnerUsers_EditsSpendTheUsersResetLinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const oldEmail, newEmail = "compromised@example.test", "fresh@example.test"
	id := h.createUser(t, owner, oldEmail, identity.RoleUser)
	edit := func(displayName, email string) {
		t.Helper()
		r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": displayName, "email": email, "role": identity.RoleUser})
		if r.status != http.StatusOK {
			t.Fatalf("edit: status %d body %s", r.status, r.body)
		}
	}

	requestRecovery(t, h, oldEmail)
	beforeRename := mailedLink(t, h, oldEmail).Query().Get("token")
	edit("Renamed", oldEmail)
	if r := reset(h, t, oldEmail, beforeRename, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("a link from before a rename: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}

	requestRecovery(t, h, oldEmail)
	toOldMailbox := mailedLink(t, h, oldEmail).Query().Get("token")
	edit("Renamed", newEmail)
	if r := reset(h, t, newEmail, toOldMailbox, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("the old mailbox's link with the new address: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 0 {
		t.Errorf("%d reset tokens survived the edits, want 0", n)
	}
	h.login(t, newEmail, userPassword) // the password never changed
}

// TestOwnerUsers_DisableAndEnableSpendTheUsersResetLinks: disabling spends
// the user's reset links with their sessions, so a link mailed before the
// disable does not work once the user is enabled again.
func TestOwnerUsers_DisableAndEnableSpendTheUsersResetLinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "suspended@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	requestRecovery(t, h, email)
	token := mailedLink(t, h, email).Query().Get("token")

	if r := owner.do(http.MethodPost, userPath(id, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("disable: status %d body %s", r.status, r.body)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1`, id); n != 0 {
		t.Errorf("%d reset tokens survived the disable, want 0", n)
	}
	if r := owner.do(http.MethodPost, userPath(id, "/enable"), nil); r.status != http.StatusOK {
		t.Fatalf("enable: status %d body %s", r.status, r.body)
	}
	if r := reset(h, t, email, token, newUserPassword); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("a link from before the disable: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}
	h.login(t, email, userPassword)
}

// backends describes what every other backend on this test's database is
// doing, for a failure message.
func backends(t *testing.T, h *harness) string {
	t.Helper()
	rows, err := h.pool.Query(context.Background(), `
		SELECT coalesce(state, ''), coalesce(wait_event_type, ''), coalesce(wait_event, ''), left(query, 60)
		FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var state, waitType, wait, query string
		if err := rows.Scan(&state, &waitType, &wait, &query); err != nil {
			return err.Error()
		}
		out = append(out, state+"/"+waitType+"/"+wait+": "+query)
	}
	return strings.Join(out, " | ")
}

// TestOwnerUsers_TheLastActiveOwnerCannotBeDemotedDisabledOrDeleted: a
// disabled Owner and a locked-out one do not count as active, so the one
// remaining active Owner is kept, until the lockout ends.
func TestOwnerUsers_TheLastActiveOwnerCannotBeDemotedDisabledOrDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const lastEmail = "last-owner@example.test"
	last := h.createUser(t, owner, lastEmail, identity.RoleOwner)
	disabledOwner := h.createUser(t, owner, "disabled-owner@example.test", identity.RoleOwner)
	if r := owner.do(http.MethodPost, userPath(disabledOwner, "/disable"), nil); r.status != http.StatusOK {
		t.Fatalf("disable an Owner while others are active: status %d body %s", r.status, r.body)
	}
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, ownerID, start.Add(10*time.Minute))

	demote := map[string]string{"displayName": "Last", "email": lastEmail, "role": identity.RoleUser}
	for _, c := range []struct {
		method, suffix string
		body           any
		message        string
	}{
		{http.MethodPut, "", demote, "The last active Owner cannot be demoted."},
		{http.MethodPost, "/disable", nil, "The last active Owner cannot be disabled."},
		{http.MethodDelete, "", nil, "The last active Owner cannot be deleted."},
	} {
		r := owner.do(c.method, userPath(last, c.suffix), c.body)
		if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "last_active_owner" || e.Message != c.message {
			t.Errorf("%s%s: status %d error %+v, want 409 last_active_owner %q", c.method, c.suffix, r.status, e, c.message)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.user_roles ur JOIN identity.users u ON u.id = ur.user_id WHERE u.id = $1 AND ur.role_id = $2 AND NOT u.is_disabled`, last, identity.RoleOwnerID); n != 1 {
		t.Errorf("the last active Owner was changed")
	}

	h.advance(11 * time.Minute) // the acting Owner's lockout ends: two active Owners again
	if r := owner.do(http.MethodPut, userPath(last, ""), demote); r.status != http.StatusOK {
		t.Errorf("demote once another Owner is active: status %d body %s", r.status, r.body)
	}
}

// TestOwnerUsers_EditingAUserEndsTheirSessionsAndRevokesInvitations: an
// email change revokes the pending invitations of both emails; a change to
// the email, display name or role ends the user's sessions, and saving the
// same details does not.
func TestOwnerUsers_EditingAUserEndsTheirSessionsAndRevokesInvitations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const oldEmail, newEmail = "old@example.test", "new@example.test"
	id := h.createUser(t, owner, oldEmail, identity.RoleUser)
	target := h.login(t, oldEmail, userPassword)
	// A pending invitation for the old email, issued before the account
	// existed (the endpoint refuses to invite an existing account's email).
	h.exec(t, `INSERT INTO identity.invitations (id, email, normalized_email, role, token_hash, created_at, expires_at)
	        VALUES ($1, $2, upper($2), 'User', $3, $4, $5)`, uuid.New(), oldEmail, bytes.Repeat([]byte{7}, 32), start, start.Add(time.Hour))
	invite(t, h, owner, map[string]string{"email": newEmail, "role": identity.RoleUser})

	r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "  Renamed ", "email": newEmail, "role": identity.RoleUser})
	if r.status != http.StatusOK {
		t.Fatalf("edit: status %d body %s", r.status, r.body)
	}
	var edited ownerUser
	r.json(&edited)
	if edited.DisplayName != "Renamed" || str(edited.Email) != newEmail || edited.Role != identity.RoleUser {
		t.Errorf("edited = %+v", edited)
	}
	rejected(t, target)
	if n := h.count(t, `SELECT count(*) FROM identity.invitations WHERE revoked_at IS NULL`); n != 0 {
		t.Errorf("%d pending invitations after the email change, want both revoked", n)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND normalized_email = 'NEW@EXAMPLE.TEST' AND email_confirmed`, id); n != 1 {
		t.Errorf("the new email is not stored normalized and confirmed")
	}
	if len(h.auditEvents(t, "user.role-changed")) != 0 {
		t.Errorf("an edit that kept the role audited a role change")
	}

	fresh := h.login(t, newEmail, userPassword)
	if r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Renamed", "email": "NEW@example.test", "role": identity.RoleUser}); r.status != http.StatusOK {
		t.Fatalf("same details: status %d body %s", r.status, r.body)
	}
	admitted(t, fresh) // the same details, the email in another case: nothing changed

	r = owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Renamed", "email": newEmail, "role": identity.RoleOwner})
	var promoted ownerUser
	r.json(&promoted)
	if r.status != http.StatusOK || promoted.Role != identity.RoleOwner {
		t.Fatalf("promote: status %d body %s", r.status, r.body)
	}
	rejected(t, fresh)
	events := h.auditEvents(t, "user.role-changed")
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id ||
		events[0].Before != `{"Roles":["User"]}` || events[0].After != `{"Roles":["Owner"]}` {
		t.Errorf("user.role-changed events = %+v, want one from User to Owner", events)
	}

	if r := owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "Renamed", "email": ownerEmail, "role": identity.RoleOwner}); r.status != http.StatusConflict || r.code() != "account_exists" {
		t.Errorf("another account's email: status %d code %q, want 409 account_exists", r.status, r.code())
	}
	r = owner.do(http.MethodPut, userPath(id, ""), map[string]string{"displayName": "", "email": "", "role": ""})
	if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Message != "The user request is invalid." || len(e.Fields) != 3 {
		t.Errorf("invalid edit: status %d error %+v, want 400 on displayName, email and role", r.status, e)
	}
}

// TestOwnerUsers_DeleteRemovesTheUserButNotAScimProvisionedOne.
func TestOwnerUsers_DeleteRemovesTheUserButNotAScimProvisionedOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	const email = "gone@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	target := h.login(t, email, userPassword)

	if r := owner.do(http.MethodDelete, userPath(id, ""), nil); r.status != http.StatusNoContent || len(r.body) != 0 {
		t.Fatalf("delete: status %d body %q, want 204", r.status, r.body)
	}
	rejected(t, target)
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1`, id); n != 0 {
		t.Errorf("the user survived the delete")
	}
	events := h.auditEvents(t, "user.deleted")
	wantBefore := `{"UserId":"` + id.String() + `","Roles":["User"],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":false}`
	wantAfter := `{"UserId":"` + id.String() + `","Roles":[],"PermissionKeys":[],"IsDisabled":false,"IsDeleted":true}`
	if len(events) != 1 || *events[0].Actor != ownerID || *events[0].TargetUser != id || events[0].Before != wantBefore || events[0].After != wantAfter {
		t.Errorf("user.deleted events = %+v, want one from %s to %s", events, wantBefore, wantAfter)
	}
	if r := owner.do(http.MethodDelete, userPath(id, ""), nil); r.status != http.StatusNotFound {
		t.Errorf("delete again: status %d, want 404", r.status)
	}

	provisioned := h.createUser(t, owner, "provisioned@example.test", identity.RoleUser)
	h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	        VALUES ($1, $2, 'ext-1', 'provisioned@example.test', true, 1, 'etag', $3, $3)`, uuid.New(), provisioned, start)
	r := owner.do(http.MethodDelete, userPath(provisioned, ""), nil)
	if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "provenance_conflict" ||
		e.Message != "This user has SCIM or federated identity history and cannot be deleted." {
		t.Errorf("delete a SCIM user: status %d error %+v, want 409 provenance_conflict", r.status, e)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1`, provisioned); n != 1 {
		t.Errorf("the SCIM user was deleted")
	}
}

// TestOwnerUsers_DeletingADelegationCreatorIsAConflict: a delegation keeps
// naming who created it (created_by_user_id, ON DELETE RESTRICT), so
// deleting an Owner who created one is refused 409 account_conflict, where
// .NET let the violation surface as a 500. Nothing is deleted or audited.
// Once the delegation is gone — here by deleting its grantee, which
// cascades — the same deletion succeeds. The delegation is seeded
// directly, as only an Owner creates one and this Owner need not sign in.
func TestOwnerUsers_DeletingADelegationCreatorIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	creator := h.createUser(t, owner, "creator@example.test", identity.RoleOwner)
	grantee := h.createUser(t, owner, "grantee@example.test", identity.RoleUser)
	h.exec(t, `INSERT INTO identity.authorization_delegations (id, grantee_user_id, created_by_user_id, version, created_at, updated_at)
	        VALUES ($1, $2, $3, $4, $5, $5)`, uuid.New(), grantee, creator, uuid.New(), start)

	r := owner.do(http.MethodDelete, userPath(creator, ""), nil)
	if e := errorOf(t, r); r.status != http.StatusConflict || e.Code != "account_conflict" ||
		e.Message != "This user created authorization delegations and cannot be deleted." {
		t.Errorf("delete a delegation creator: status %d error %+v, want 409 account_conflict", r.status, e)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1`, creator); n != 1 {
		t.Errorf("the delegation creator was deleted")
	}
	if events := h.auditEvents(t, "user.deleted"); len(events) != 0 {
		t.Errorf("user.deleted events = %+v, want none for a refused deletion", events)
	}

	if r := owner.do(http.MethodDelete, userPath(grantee, ""), nil); r.status != http.StatusNoContent {
		t.Fatalf("delete the grantee: status %d body %s, want 204", r.status, r.body)
	}
	if r := owner.do(http.MethodDelete, userPath(creator, ""), nil); r.status != http.StatusNoContent {
		t.Errorf("delete the creator once no delegation names them: status %d body %s, want 204", r.status, r.body)
	}
}

// TestOwnerUsers_SettingAPasswordEndsSessionsAndSpendsResetTokens: an
// Owner's new password for a user ends the user's sessions and kills the
// reset link the user holds.
func TestOwnerUsers_SettingAPasswordEndsSessionsAndSpendsResetTokens(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "reset-by-owner@example.test"
	id := h.createUser(t, owner, email, identity.RoleUser)
	target := h.login(t, email, userPassword)
	if r := owner.do(http.MethodPost, userPath(id, "/password-reset"), nil); r.status != http.StatusOK {
		t.Fatalf("password-reset: status %d body %s", r.status, r.body)
	}
	token := mailedLink(t, h, email).Query().Get("token")

	r := owner.do(http.MethodPost, userPath(id, "/password"), map[string]string{"password": newUserPassword})
	if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"success":true}` {
		t.Fatalf("set password: status %d body %s", r.status, r.body)
	}
	rejected(t, target)
	if r := h.client(t).do(http.MethodPost, loginPath, credentials(email, userPassword)); r.status != http.StatusUnauthorized {
		t.Errorf("the old password: status %d, want 401", r.status)
	}
	h.login(t, email, newUserPassword)
	if r := reset(h, t, email, token, "AnotherIntegrationPassword123"); r.status != http.StatusBadRequest || r.code() != "invalid_reset_token" {
		t.Errorf("the reset link after the Owner's change: status %d code %q, want 400 invalid_reset_token", r.status, r.code())
	}

	for _, c := range []struct{ password, problem string }{
		{"", "Password is required."},
		{"   ", "Password is required."},
		{strings.Repeat("p", 257), "Password must be 256 characters or fewer."},
	} {
		r := owner.do(http.MethodPost, userPath(id, "/password"), map[string]string{"password": c.password})
		if e := errorOf(t, r); r.status != http.StatusBadRequest || e.Code != "invalid_request" || e.Message != "The password request is invalid." ||
			!fieldsEqual(e.Fields, map[string][]string{"password": {c.problem}}) {
			t.Errorf("password %q: status %d error %+v, want 400 %q", c.password, r.status, e, c.problem)
		}
	}
}

// TestOwnerUsers_PasswordResetMailsOnlyAnAccountWithAPasswordAndAConfirmedEmail.
func TestOwnerUsers_PasswordResetMailsOnlyAnAccountWithAPasswordAndAConfirmedEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	eligible := h.createUser(t, owner, "eligible@example.test", identity.RoleUser)
	passwordless := h.insertUser(t, "passwordless@example.test", identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET email_confirmed = true WHERE id = $1`, passwordless)
	unconfirmed := h.seedUser(t, "unconfirmed@example.test", userPassword, identity.RoleUserID)

	for _, id := range []uuid.UUID{eligible, passwordless, unconfirmed} {
		r := owner.do(http.MethodPost, userPath(id, "/password-reset"), nil)
		if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"accepted":true}` {
			t.Errorf("password-reset %s: status %d body %s, want {\"accepted\":true}", id, r.status, r.body)
		}
	}
	msgs := h.mail.Messages()
	if len(msgs) != 1 || msgs[0].To != "eligible@example.test" {
		t.Fatalf("mails = %+v, want one, to the eligible account", msgs)
	}
	wantPrefix := "Use this link to reset your password: " + h.url + "/password-reset?email=eligible%40example.test&token="
	if msgs[0].Subject != "Reset your Vantigo password" || !strings.HasPrefix(msgs[0].TextBody, wantPrefix) {
		t.Errorf("mail = %q / %q, want the reset template with %s…", msgs[0].Subject, msgs[0].TextBody, wantPrefix)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.password_reset_tokens WHERE user_id = $1 AND expires_at = $2`, eligible, start.Add(24*time.Hour)); n != 1 {
		t.Errorf("no reset token expiring 24 hours on")
	}
}

// TestOwnerUsers_AnUnknownUserIsNotFound: every operation that names a user
// answers an unknown one with the bare 404.
func TestOwnerUsers_AnUnknownUserIsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	id := uuid.New()
	for _, op := range []struct {
		method, suffix string
		body           any
	}{
		{http.MethodPut, "", map[string]string{"displayName": "Nobody", "email": "nobody@example.test", "role": identity.RoleUser}},
		{http.MethodDelete, "", nil},
		{http.MethodPost, "/disable", nil},
		{http.MethodPost, "/enable", nil},
		{http.MethodPost, "/password", map[string]string{"password": newUserPassword}},
		{http.MethodPost, "/password-reset", nil},
		{http.MethodGet, "/avatar", nil},
	} {
		if r := owner.do(op.method, userPath(id, op.suffix), op.body); r.status != http.StatusNotFound || len(r.body) != 0 {
			t.Errorf("%s unknown%s: status %d body %q, want a bare 404", op.method, op.suffix, r.status, r.body)
		}
	}
}

// Ported from the owner half of IdentityAccountEndpointsTests.Avatar_IsPrivateValidatedAndDeletedWithoutCrossUserAccess
// (the self-service half is TestAccountAvatar_UploadPNGAndJPEGServeAndDelete).
func TestOwnerUsers_AvatarReadServesAnotherUsersAvatar(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	first := h.createUser(t, owner, "first@example.test", identity.RoleUser)
	second := h.createUser(t, owner, "second@example.test", identity.RoleUser)
	firstClient := h.login(t, "first@example.test", userPassword)
	secondClient := h.login(t, "second@example.test", userPassword)

	img := testPNG(t, 2, 2)
	ct, body := avatarUpload(t, "image/png", img)
	if r := firstClient.do(http.MethodPut, avatarPath, nil, rawBody(ct, body)); r.status != http.StatusOK {
		t.Fatalf("upload: status %d body %s", r.status, r.body)
	}

	var listed *ownerUser
	for _, u := range ownerUsers(t, owner) {
		switch u.ID {
		case first:
			listed = &u
		case second:
			if u.AvatarURL != nil {
				t.Errorf("second's avatarUrl = %q, want null", *u.AvatarURL)
			}
		}
	}
	if listed == nil || listed.AvatarURL == nil || *listed.AvatarURL != userPath(first, "/avatar") {
		t.Fatalf("first listed as %+v, want avatarUrl %s", listed, userPath(first, "/avatar"))
	}
	r := owner.do(http.MethodGet, *listed.AvatarURL, nil)
	if r.status != http.StatusOK || r.header("Content-Type") != "image/png" || !bytes.Equal(r.body, img) {
		t.Fatalf("owner read: status %d Content-Type %q, %d bytes; want the uploaded PNG", r.status, r.header("Content-Type"), len(r.body))
	}
	for k, want := range map[string]string{"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Disposition": "inline"} {
		if got := r.header(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if r := secondClient.do(http.MethodGet, userPath(first, "/avatar"), nil); r.status != http.StatusForbidden {
		t.Errorf("a standard user's read: status %d, want 403", r.status)
	}
	if r := owner.do(http.MethodGet, userPath(second, "/avatar"), nil); r.status != http.StatusNotFound {
		t.Errorf("a user without an avatar: status %d, want 404", r.status)
	}
}
