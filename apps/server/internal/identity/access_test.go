package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// outcome is what Check answered for one rule.
type outcome int

const (
	allowed outcome = iota
	u401            // ErrUnauthenticated
	f403            // ErrForbidden
)

func (o outcome) String() string {
	switch o {
	case allowed:
		return "allowed"
	case u401:
		return "ErrUnauthenticated"
	default:
		return "ErrForbidden"
	}
}

// check runs Check for rule on a request carrying token (none when "").
func check(t *testing.T, h *harness, token, rule string) (contracts.Principal, outcome) {
	t.Helper()
	r, err := contracts.ParseRule(rule)
	if err != nil {
		t.Fatalf("rule %q: %v", rule, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
	}
	p, err := h.access.Check(req, r)
	switch {
	case err == nil:
		return p, allowed
	case errors.Is(err, contracts.ErrUnauthenticated):
		return p, u401
	case errors.Is(err, contracts.ErrForbidden):
		return p, f403
	default:
		t.Fatalf("Check(%s): unexpected error %v", rule, err)
		return p, 0
	}
}

func (h *harness) insertRole(t testing.TB, name string, permissions ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	        VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, name, uuid.New(), h.now())
	for _, key := range permissions {
		h.exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, $2)`, id, key)
	}
	return id
}

// accessRules are the matrix's columns: every rule kind, every policy, and
// compound policy rules that an Owner meets (Owner+OwnerManagement) and
// does not (Owner+SystemAdmin).
var accessRules = []string{
	"anonymous",
	"session",
	"scim",
	"policy:ActiveAccount",
	"policy:Business",
	"policy:Owner",
	"policy:OwnerManagement",
	"policy:Owner+OwnerManagement",
	"policy:SystemAdmin",
	"policy:Owner+SystemAdmin",
	"policy:AuthorizationManagement",
	"permission:identity:manage",
}

type accessRow struct {
	user string
	want []outcome // one per accessRules entry
}

// accessFixture is one harness with a user per account state the rules
// distinguish, each signed in (tokens) unless noted.
type accessFixture struct {
	h      *harness
	users  map[string]uuid.UUID
	tokens map[string]string
}

func newAccessFixture(t *testing.T, opts ...harnessOption) *accessFixture {
	// SCIM is configured so a directory-deactivated account is enforced.
	h := newHarness(t, append([]harnessOption{withEnv("SCIM_TOKEN", "identity-harness-scim-token")}, opts...)...)
	f := &accessFixture{h: h, users: map[string]uuid.UUID{}, tokens: map[string]string{}}
	auditor := h.insertRole(t, "Auditor", "identity:manage")

	add := func(name string, mfa bool, roles ...uuid.UUID) uuid.UUID {
		id := h.insertUser(t, name+"@example.test", roles...)
		f.users[name] = id
		f.tokens[name] = h.session(t, id, mfa)
		return id
	}
	add("ownerMFA", true, identity.RoleOwnerID)
	add("ownerNoMFA", false, identity.RoleOwnerID)
	add("ownerAdminMFA", true, identity.RoleSystemAdminID, identity.RoleOwnerID)
	add("adminMFA", true, identity.RoleSystemAdminID)
	add("adminNoMFA", false, identity.RoleSystemAdminID)
	add("user", false, identity.RoleUserID)
	add("userPermitted", false, identity.RoleUserID, auditor)

	lockedUntil := h.now().Add(15 * time.Minute)
	locked := add("lockedPermitted", false, identity.RoleUserID, auditor)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, locked, lockedUntil)
	lockedOwner := add("lockedOwnerMFA", true, identity.RoleOwnerID)
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, lockedOwner, lockedUntil)

	disabled := add("disabledUser", false, identity.RoleUserID)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	disabledOwner := add("disabledOwnerMFA", true, identity.RoleOwnerID)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabledOwner)

	scimInactive := add("scimInactiveUser", false, identity.RoleUserID)
	f.deactivateInDirectory(t, scimInactive)
	scimInactiveOwner := add("scimInactiveOwnerMFA", true, identity.RoleOwnerID)
	f.deactivateInDirectory(t, scimInactiveOwner)

	revoked := add("revoked", false, identity.RoleUserID)
	if err := identity.RevokeAllSessions(h.access, context.Background(), store.New(h.pool), revoked); err != nil {
		t.Fatal(err)
	}

	f.users["noSession"] = h.insertUser(t, "nosession@example.test", identity.RoleUserID)
	return f
}

func (f *accessFixture) deactivateInDirectory(t testing.TB, userID uuid.UUID) {
	f.h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
	          VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), userID, userID.String(), f.h.now())
}

// run checks every rule for every row. Where anonymous is allowed, the
// principal must be the row's user when its session is live (the session
// rule allows) and the zero principal otherwise.
func (f *accessFixture) run(t *testing.T, rows []accessRow) {
	t.Helper()
	for _, row := range rows {
		t.Run(row.user, func(t *testing.T) {
			if len(row.want) != len(accessRules) {
				t.Fatalf("row has %d outcomes for %d rules", len(row.want), len(accessRules))
			}
			live := row.want[1] == allowed
			for i, rule := range accessRules {
				p, got := check(t, f.h, f.tokens[row.user], rule)
				if got != row.want[i] {
					t.Errorf("%s: got %v, want %v", rule, got, row.want[i])
					continue
				}
				if got != allowed {
					continue
				}
				wantUser := uuid.Nil
				if live {
					wantUser = f.users[row.user]
				}
				if p.UserID != wantUser {
					t.Errorf("%s: principal user %v, want %v", rule, p.UserID, wantUser)
				}
			}
		})
	}
}

// TestAccess_RulesAgainstAccountStates evaluates every rule kind for every
// account state with OWNERS_REQUIRE_MFA on. It pins .NET's three notions of
// "active": session validation rejects a disabled user and a SCIM-inactive
// non-Owner (so even the session rule answers 401, as .NET rejected the
// cookie principal), ActiveAccount and the role policies ignore lockout, and
// a permission check denies a locked-out user, Owner included.
func TestAccess_RulesAgainstAccountStates(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "1"))

	A, U, F := allowed, u401, f403
	signedOut := []outcome{A, U, U, U, U, U, U, U, U, U, U, U}
	f.run(t, []accessRow{
		// anon session scim Active Business Owner OwnerMgmt Owner+OM SysAdmin Owner+SA AuthzMgmt perm
		{"noSession", signedOut},
		{"ownerMFA", []outcome{A, A, U, A, A, A, A, A, F, F, A, A}},
		{"ownerNoMFA", []outcome{A, A, U, A, F, F, F, F, F, F, F, F}},
		{"ownerAdminMFA", []outcome{A, A, U, A, A, A, A, A, A, A, A, A}},
		{"adminMFA", []outcome{A, A, U, A, A, F, F, F, A, F, F, F}},
		{"adminNoMFA", []outcome{A, A, U, A, A, F, F, F, F, F, F, F}},
		{"user", []outcome{A, A, U, A, A, F, F, F, F, F, F, F}},
		{"userPermitted", []outcome{A, A, U, A, A, F, F, F, F, F, F, A}},
		{"lockedPermitted", []outcome{A, A, U, A, A, F, F, F, F, F, F, F}},
		{"lockedOwnerMFA", []outcome{A, A, U, A, A, A, A, A, F, F, A, F}},
		{"disabledUser", signedOut},
		{"disabledOwnerMFA", signedOut},
		{"scimInactiveUser", signedOut},
		{"scimInactiveOwnerMFA", []outcome{A, A, U, A, A, A, A, A, F, F, A, A}},
		{"revoked", signedOut},
	})
}

// TestAccess_RulesWithoutOwnerMFA is the same matrix with OWNERS_REQUIRE_MFA
// off (the development default): the MFA requirement, and Business's Owner
// MFA check, pass for everyone.
func TestAccess_RulesWithoutOwnerMFA(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "0"))

	A, U, F := allowed, u401, f403
	f.run(t, []accessRow{
		// anon session scim Active Business Owner OwnerMgmt Owner+OM SysAdmin Owner+SA AuthzMgmt perm
		{"ownerNoMFA", []outcome{A, A, U, A, A, A, A, A, F, F, A, A}},
		{"adminNoMFA", []outcome{A, A, U, A, A, F, F, F, A, F, F, F}},
		{"user", []outcome{A, A, U, A, A, F, F, F, F, F, F, F}},
		{"userPermitted", []outcome{A, A, U, A, A, F, F, F, F, F, F, A}},
	})
}

// TestAccess_EmptyRulesFailClosed proves a policy or permission rule that
// names nothing grants nothing, even to an Owner with MFA. ParseRule never
// produces one; Check must refuse it regardless.
func TestAccess_EmptyRulesFailClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	token := h.session(t, h.insertUser(t, "owner@example.test", identity.RoleOwnerID, identity.RoleSystemAdminID), true)

	for _, rule := range []contracts.Rule{
		{Kind: contracts.RulePolicy},
		{Kind: contracts.RulePolicy, Names: []string{}},
		{Kind: contracts.RulePermission},
		{Kind: contracts.RulePermission, Names: []string{}},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		if _, err := h.access.Check(req, rule); !errors.Is(err, contracts.ErrForbidden) {
			t.Errorf("Check(%+v) = %v, want ErrForbidden", rule, err)
		}
	}
}

// TestAccess_PermissionEffectiveRoles pins the permission check's effective
// roles (AZ/PermissionAuthorization.cs:76-99): direct roles plus roles mapped
// to active groups the user effectively belongs to, with the mapping's
// source equal to the group's. It also pins compound permission rules as
// all-of: a user holding one of two keys is denied, one holding both
// (from two sources) is allowed. The keys need not be in the catalog here:
// the router, not Check, enforces that.
func TestAccess_PermissionEffectiveRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	auditor := h.insertRole(t, "Auditor", "identity:manage")
	reader := h.insertRole(t, "Reader", "identity:read")

	group := func(name, source string, active bool, mappingSource string) uuid.UUID {
		id := uuid.New()
		h.exec(t, `INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, $5, $6, $6)`, id, name, source, active, uuid.New(), h.now())
		h.exec(t, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, $3)`, id, reader, mappingSource)
		return id
	}
	localGroup := group("Local readers", "local", true, "local")
	directoryGroup := group("Directory readers", "scim", true, "scim")
	inactiveGroup := group("Retired readers", "local", false, "local")
	mismatchedGroup := group("Mismatched readers", "scim", true, "local")

	member := func(groupID, userID uuid.UUID, source string, upstream bool, override *string) {
		h.exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
		        VALUES ($1, $2, $3, $4, $5)`, groupID, userID, source, upstream, override)
	}
	forceMember, forceNonMember := "force_member", "force_non_member"

	users := map[string]uuid.UUID{
		"direct":         h.insertUser(t, "direct@example.test", identity.RoleUserID, auditor),
		"directAndGroup": h.insertUser(t, "both@example.test", identity.RoleUserID, auditor),
		"forcedMember":   h.insertUser(t, "forced@example.test", identity.RoleUserID),
		"upstreamMember": h.insertUser(t, "upstream@example.test", identity.RoleUserID),
		"upstreamAbsent": h.insertUser(t, "absent@example.test", identity.RoleUserID),
		"forcedOut":      h.insertUser(t, "forcedout@example.test", identity.RoleUserID),
		"inactiveGroup":  h.insertUser(t, "inactive@example.test", identity.RoleUserID),
		"sourceMismatch": h.insertUser(t, "mismatch@example.test", identity.RoleUserID),
		"owner":          h.insertUser(t, "owner@example.test", identity.RoleOwnerID),
	}
	member(localGroup, users["directAndGroup"], "local", false, &forceMember)
	member(localGroup, users["forcedMember"], "local", false, &forceMember)
	member(directoryGroup, users["upstreamMember"], "scim", true, nil)
	member(directoryGroup, users["upstreamAbsent"], "scim", false, nil)
	member(directoryGroup, users["forcedOut"], "scim", true, &forceNonMember)
	member(inactiveGroup, users["inactiveGroup"], "local", false, &forceMember)
	member(mismatchedGroup, users["sourceMismatch"], "scim", true, nil)

	const read, manage, both = "permission:identity:read", "permission:identity:manage", "permission:identity:manage+identity:read"
	cases := []struct {
		user string
		rule string
		want outcome
	}{
		{"direct", manage, allowed},
		{"direct", read, f403},
		{"direct", both, f403}, // compound rule, one key missing
		{"directAndGroup", both, allowed},
		{"forcedMember", read, allowed},
		{"upstreamMember", read, allowed},
		{"upstreamAbsent", read, f403},
		{"forcedOut", read, f403},
		{"inactiveGroup", read, f403},
		{"sourceMismatch", read, f403},
		{"owner", both, allowed}, // Owner short-circuits every key
	}
	for _, c := range cases {
		token := h.session(t, users[c.user], false)
		if _, got := check(t, h, token, c.rule); got != c.want {
			t.Errorf("%s %s: got %v, want %v", c.user, c.rule, got, c.want)
		}
	}
}

// TestAccess_PrincipalCarriesTheSessionAndOrderedRoles proves the principal
// handed to handlers: the session's user and id, MFA state, and roles in
// orderRoles order.
func TestAccess_PrincipalCarriesTheSessionAndOrderedRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	billing := h.insertRole(t, "Billing")
	id := h.insertUser(t, "admin@example.test", identity.RoleUserID, billing, identity.RoleOwnerID, identity.RoleSystemAdminID)

	p, got := check(t, h, h.session(t, id, true), "session")
	if got != allowed {
		t.Fatalf("session: got %v", got)
	}
	if p.UserID != id || p.SessionID == uuid.Nil || !p.MFAVerified || p.SCIM {
		t.Errorf("principal = %+v", p)
	}
	if want := []string{"SystemAdmin", "Owner", "User", "Billing"}; !equal(p.Roles, want) {
		t.Errorf("roles = %v, want %v", p.Roles, want)
	}

	if p, _ := check(t, h, h.session(t, id, false), "session"); p.MFAVerified {
		t.Error("a password-only session reports MFAVerified")
	}
}

// TestAccess_MalformedAndUnknownTokensAreUnauthenticated proves a cookie
// that is not a token identity minted, or names no session, is no session.
func TestAccess_MalformedAndUnknownTokensAreUnauthenticated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, token := range []string{
		"not a token",
		"c2hvcnQ", // base64url, but not 32 bytes
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // 32 bytes no session has
	} {
		if _, got := check(t, h, token, "session"); got != u401 {
			t.Errorf("token %q: got %v, want %v", token, got, u401)
		}
		if p, got := check(t, h, token, "anonymous"); got != allowed || p.UserID != uuid.Nil {
			t.Errorf("token %q under anonymous: got %v with %+v, want the zero principal", token, got, p)
		}
	}
}

// TestAccess_RevocationEndsSessionsAtOnce proves revokeOtherSessions keeps
// exactly the caller's session and revokeAllSessions ends that one too, both
// effective on the next check.
func TestAccess_RevocationEndsSessionsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, q := context.Background(), store.New(h.pool)
	id := h.insertUser(t, "user@example.test", identity.RoleUserID)
	mine, other := h.session(t, id, false), h.session(t, id, false)
	bystander := h.session(t, h.insertUser(t, "other@example.test", identity.RoleUserID), false)

	p, _ := check(t, h, mine, "session")
	if err := identity.RevokeOtherSessions(h.access, ctx, q, id, p.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, got := check(t, h, mine, "session"); got != allowed {
		t.Errorf("kept session: got %v, want allowed", got)
	}
	if _, got := check(t, h, other, "session"); got != u401 {
		t.Errorf("other session: got %v, want %v", got, u401)
	}

	if err := identity.RevokeAllSessions(h.access, ctx, q, id); err != nil {
		t.Fatal(err)
	}
	if _, got := check(t, h, mine, "session"); got != u401 {
		t.Errorf("caller's session after revoking all: got %v, want %v", got, u401)
	}
	if _, got := check(t, h, bystander, "session"); got != allowed {
		t.Errorf("another user's session: got %v, want allowed", got)
	}
}

// lastSeen reads a session's last_seen_at.
func lastSeen(t *testing.T, h *harness, sessionID uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT last_seen_at FROM identity.sessions WHERE id = $1`, sessionID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// TestAccess_ActivitySlidesOnlyPastTheWriteInterval proves last_seen_at is
// rewritten once a session has been idle min(idle/4, 5 min) (5 minutes for
// the 8 h standard idle window) and not before, and that it is written with
// the clock's time.
func TestAccess_ActivitySlidesOnlyPastTheWriteInterval(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	token := h.session(t, h.insertUser(t, "user@example.test", identity.RoleUserID), false)
	p, _ := check(t, h, token, "session")

	h.advance(5*time.Minute - time.Second)
	check(t, h, token, "session")
	if got := lastSeen(t, h, p.SessionID); !got.Equal(start) {
		t.Errorf("after 4m59s: last_seen_at = %v, want it unchanged at %v", got, start)
	}

	h.advance(time.Second)
	check(t, h, token, "session")
	if got := lastSeen(t, h, p.SessionID); !got.Equal(h.now()) {
		t.Errorf("after 5m: last_seen_at = %v, want %v", got, h.now())
	}
}

// TestAccess_IdleBoundRejectsAtExactlyTheIdleTimeout proves the idle bound
// is .NET's now - lastSeen >= idle (SV/SessionValidationService.cs:89): one
// second short survives, the full timeout does not.
func TestAccess_IdleBoundRejectsAtExactlyTheIdleTimeout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	token := h.session(t, h.insertUser(t, "user@example.test", identity.RoleUserID), false)

	h.advance(h.cfg.Sessions.Idle - time.Second)
	if _, got := check(t, h, token, "session"); got != allowed {
		t.Fatalf("idle 7h59m59s: got %v, want allowed", got)
	}
	h.advance(h.cfg.Sessions.Idle)
	if _, got := check(t, h, token, "session"); got != u401 {
		t.Errorf("idle exactly 8h since the last slide: got %v, want %v", got, u401)
	}
}

// TestAccess_PrivilegedAbsoluteLifetime proves an Owner's session ends at the
// 8 h privileged absolute lifetime however active it is.
func TestAccess_PrivilegedAbsoluteLifetime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	token := h.session(t, h.insertUser(t, "owner@example.test", identity.RoleOwnerID), true)

	for elapsed := time.Hour; elapsed < h.cfg.Sessions.PrivilegedAbsolute; elapsed += time.Hour {
		h.advance(time.Hour)
		if _, got := check(t, h, token, "session"); got != allowed {
			t.Fatalf("after %v: got %v, want allowed", elapsed, got)
		}
	}
	h.advance(time.Hour - time.Second)
	if _, got := check(t, h, token, "session"); got != allowed {
		t.Fatalf("one second short of 8h: got %v, want allowed", got)
	}
	h.advance(time.Second)
	if _, got := check(t, h, token, "session"); got != u401 {
		t.Errorf("at 8h: got %v, want %v", got, u401)
	}
}

// TestAccess_PromotionTightensTheBoundsAtOnce proves privilege is decided
// per request: a User session idle 3 h is live, but the same session is
// over the privileged idle bound the moment its user becomes an Owner.
func TestAccess_PromotionTightensTheBoundsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	promoted := h.insertUser(t, "promoted@example.test", identity.RoleUserID)
	promotedToken := h.session(t, promoted, false)
	control := h.session(t, h.insertUser(t, "control@example.test", identity.RoleUserID), false)

	h.advance(3 * time.Hour)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, promoted, identity.RoleOwnerID)

	if _, got := check(t, h, control, "session"); got != allowed {
		t.Errorf("User idle 3h: got %v, want allowed", got)
	}
	if _, got := check(t, h, promotedToken, "session"); got != u401 {
		t.Errorf("newly promoted Owner idle 3h: got %v, want %v", got, u401)
	}
}

const sessionPath = "/api/v1/identity/session"

// admitted proves c's session is live: GET /session answers a
// contract-validated 200.
func admitted(t *testing.T, c *client) {
	t.Helper()
	if r := c.do(http.MethodGet, sessionPath, nil); r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200 (admitted)", sessionPath, r.status)
	}
}

func rejected(t *testing.T, c *client) {
	t.Helper()
	if r := c.do(http.MethodGet, sessionPath, nil); r.status != http.StatusUnauthorized || r.code() != "unauthenticated" {
		t.Fatalf("GET %s: status %d code %q, want 401 unauthenticated", sessionPath, r.status, r.code())
	}
}

// Ported from IdentitySessionLifetimeIntegrationTests.PrivilegedSessionEndsAfterTheShorterIdleWindow.
func TestSessionLifetime_PrivilegedSessionEndsAfterTheShorterIdleWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.signIn(t, h.insertUser(t, "owner@example.test", identity.RoleOwnerID), true)
	admitted(t, c)

	h.advance(h.cfg.Sessions.PrivilegedIdle + time.Second)
	rejected(t, c)
}

// Ported from IdentitySessionLifetimeIntegrationTests.StandardSessionSurvivesThePrivilegedIdleWindow.
func TestSessionLifetime_StandardSessionSurvivesThePrivilegedIdleWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), false)

	h.advance(h.cfg.Sessions.PrivilegedIdle + time.Second)
	admitted(t, c)
}

// Ported from IdentitySessionLifetimeIntegrationTests.ActivityRenewsAPrivilegedSessionInsideTheIdleWindow.
func TestSessionLifetime_ActivityRenewsAPrivilegedSessionInsideTheIdleWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.signIn(t, h.insertUser(t, "owner@example.test", identity.RoleOwnerID), true)

	// Requests spanning longer than the 2 h idle window. Each one has to
	// renew the window, otherwise the session dies partway through.
	for range 5 {
		admitted(t, c)
		h.advance(time.Hour)
	}
	admitted(t, c)
}

// Ported from IdentitySessionLifetimeIntegrationTests.ContinuouslyUsedSessionStillEndsAtTheAbsoluteLifetime.
func TestSessionLifetime_ContinuouslyUsedSessionStillEndsAtTheAbsoluteLifetime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), false)

	// The 8 h idle window never elapses between hourly requests, so nothing
	// but the 24 h absolute lifetime can end this session.
	for elapsed := time.Duration(0); elapsed < h.cfg.Sessions.Absolute; elapsed += time.Hour {
		admitted(t, c)
		h.advance(time.Hour)
	}
	rejected(t, c)
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
