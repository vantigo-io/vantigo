package identity_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// newUserDirectory builds the module's user directory over the harness's
// dependencies, exactly as module.Compose builds it before any module mounts.
func newUserDirectory(t *testing.T, h *harness) contracts.UserDirectory {
	t.Helper()
	d := identity.Module(h.access).Users
	if d == nil {
		t.Fatal("the module declares no user directory")
	}
	return d(h.deps)
}

// insertUserNamed inserts a user directly, with an arbitrary display name and
// disabled flag, for directory tests that need names insertUser's
// email-as-display-name shortcut cannot express.
func insertUserNamed(t testing.TB, h *harness, displayName string, disabled bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := strings.ToLower(strings.NewReplacer(" ", ".", "%", "pct", "_", "underscore", `\`, "backslash").Replace(displayName)) + "-" + id.String() + "@example.test"
	h.exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, is_disabled, version, created_at, updated_at)
		VALUES ($1, $2, upper($2), $3, $4, $5, $6, $6)`, id, email, displayName, disabled, uuid.New(), h.now())
	return id
}

// TestUserDirectory_ResolvesAUser proves an active user resolves to the three
// fields the directory ever exposes: id, display name, and Active true.
func TestUserDirectory_ResolvesAUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertUserNamed(t, h, "Ola Nordmann", false)

	got, err := newUserDirectory(t, h).User(context.Background(), id)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if got == nil || *got != (contracts.UserEntry{ID: id, DisplayName: "Ola Nordmann", Active: true}) {
		t.Errorf("User = %+v, want {%s Ola Nordmann true}", got, id)
	}
}

// TestUserDirectory_DisabledUserAnswersInactive proves a disabled user still
// resolves, since a role or an old assignment may still reference them, but
// with Active false.
func TestUserDirectory_DisabledUserAnswersInactive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertUserNamed(t, h, "Disabled Person", true)

	got, err := newUserDirectory(t, h).User(context.Background(), id)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	if got == nil || got.Active {
		t.Errorf("User = %+v, want Active false", got)
	}
}

// TestUserDirectory_UnknownIDIsNilWithoutAnError proves a missing user is
// (nil, nil), never an error: a caller tells "does not exist" from "the
// lookup failed" by checking err.
func TestUserDirectory_UnknownIDIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newUserDirectory(t, h).User(context.Background(), uuid.New())
	if got != nil || err != nil {
		t.Errorf("User(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestUserDirectory_UsersResolvesKnownIDsAndSkipsUnknown proves Users
// resolves every id it knows and simply omits the ones it does not, rather
// than failing the whole lookup.
func TestUserDirectory_UsersResolvesKnownIDsAndSkipsUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := insertUserNamed(t, h, "First Person", false)
	second := insertUserNamed(t, h, "Second Person", false)
	unknown := uuid.New()

	got, err := newUserDirectory(t, h).Users(context.Background(), []uuid.UUID{first, second, unknown})
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Users = %+v, want two entries", got)
	}
	byID := map[uuid.UUID]contracts.UserEntry{got[0].ID: got[0], got[1].ID: got[1]}
	if e, ok := byID[first]; !ok || e.DisplayName != "First Person" || !e.Active {
		t.Errorf("Users missing or wrong entry for first: %+v", byID[first])
	}
	if e, ok := byID[second]; !ok || e.DisplayName != "Second Person" || !e.Active {
		t.Errorf("Users missing or wrong entry for second: %+v", byID[second])
	}
}

// TestUserDirectory_UsersEmptyInputIsEmptyOutput proves an empty slice of ids
// answers an empty result with no query error, rather than a malformed
// ANY($1) query against an empty array.
func TestUserDirectory_UsersEmptyInputIsEmptyOutput(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newUserDirectory(t, h).Users(context.Background(), []uuid.UUID{})
	if err != nil {
		t.Fatalf("Users(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Users(empty) = %+v, want empty", got)
	}
}

// TestUserDirectory_SearchUsersMatchesCaseInsensitiveSubstring proves a
// search matches a display name that merely contains the query, regardless
// of case.
func TestUserDirectory_SearchUsersMatchesCaseInsensitiveSubstring(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	match := insertUserNamed(t, h, "Ola Nordmann", false)
	insertUserNamed(t, h, "Kari Hansen", false)

	got, err := newUserDirectory(t, h).SearchUsers(context.Background(), "ola", 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(got) != 1 || got[0].ID != match {
		t.Errorf("SearchUsers(ola) = %+v, want only %s", got, match)
	}
}

// TestUserDirectory_SearchUsersExcludesDisabledUsers proves a disabled user
// never comes back from search, even when their name matches: nothing should
// let a caller newly assign work to someone who can no longer act.
func TestUserDirectory_SearchUsersExcludesDisabledUsers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	insertUserNamed(t, h, "Ola Disabled", true)

	got, err := newUserDirectory(t, h).SearchUsers(context.Background(), "ola", 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("SearchUsers(ola) = %+v, want no disabled matches", got)
	}
}

// TestUserDirectory_SearchUsersOrdersByDisplayNameAndHonoursLimit proves
// results come back ordered by display name and capped at limit.
func TestUserDirectory_SearchUsersOrdersByDisplayNameAndHonoursLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := insertUserNamed(t, h, "Search Charlie", false)
	insertUserNamed(t, h, "Search Alice", false)
	insertUserNamed(t, h, "Search Bravo", false)

	got, err := newUserDirectory(t, h).SearchUsers(context.Background(), "search", 2)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(got) != 2 || got[0].DisplayName != "Search Alice" || got[1].DisplayName != "Search Bravo" {
		t.Errorf("SearchUsers(search, 2) = %+v, want [Search Alice, Search Bravo]", got)
	}
	for _, e := range got {
		if e.ID == c {
			t.Errorf("SearchUsers honoured limit 2 but included Search Charlie")
		}
	}
}

// TestUserDirectory_SearchUsersEscapesWildcards proves a literal %, _ or \ in
// the query matches only that literal character, never as ILIKE's wildcard
// or escape-introducer meaning: each case seeds a display name holding the
// literal character and a decoy that would also match were the character
// treated as a wildcard (any-characters for %, any-single-character for _)
// or as an escape introducer that swallows the character after it (\).
func TestUserDirectory_SearchUsersEscapesWildcards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		literal string // display name holding the literal special character
		decoy   string // matches only if the character were a wildcard/escape
		query   string // search query holding the literal special character
	}{
		{name: "percent", literal: "100% Match", decoy: "100X Match", query: "100% match"},
		{name: "underscore", literal: "a_b Match", decoy: "aXb Match", query: "a_b match"},
		{name: "backslash", literal: `Path\File Match`, decoy: "PathFile Match", query: `path\file match`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			literal := insertUserNamed(t, h, tc.literal, false)
			insertUserNamed(t, h, tc.decoy, false)

			got, err := newUserDirectory(t, h).SearchUsers(context.Background(), tc.query, 10)
			if err != nil {
				t.Fatalf("SearchUsers(%q): %v", tc.query, err)
			}
			if len(got) != 1 || got[0].ID != literal {
				t.Errorf("SearchUsers(%q) = %+v, want only the literal match %s", tc.query, got, literal)
			}
		})
	}
}

// holdersFixture is one installation's users for PermissionHolders: every
// way to hold or not hold staff:approve, by name. Each display name sorts
// by the letter it starts with, so the expected order is the alphabet.
type holdersFixture struct {
	h     *harness
	users map[string]uuid.UUID
}

func newHoldersFixture(t *testing.T, opts ...harnessOption) *holdersFixture {
	t.Helper()
	h := newHarness(t, opts...)
	f := &holdersFixture{h: h, users: map[string]uuid.UUID{}}
	approver := h.insertRole(t, "Approver", "staff:approve")
	manager := h.insertRole(t, "Manager", "staff:manage")

	add := func(name string, roles ...uuid.UUID) uuid.UUID {
		id := h.insertUser(t, name+"@example.test", roles...)
		f.users[name] = id
		return id
	}
	group := func(name, source string, active bool, mappingSource string) uuid.UUID {
		id := uuid.New()
		h.exec(t, `INSERT INTO identity.access_groups (id, display_name, source, is_active, version, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, $5, $6, $6)`, id, name, source, active, uuid.New(), h.now())
		h.exec(t, `INSERT INTO identity.access_group_role_mappings (group_id, role_id, source) VALUES ($1, $2, $3)`, id, approver, mappingSource)
		return id
	}
	member := func(groupID, userID uuid.UUID, upstream bool, override *string) {
		h.exec(t, `INSERT INTO identity.access_group_memberships (group_id, user_id, source, is_upstream_present, membership_override)
		        VALUES ($1, $2, 'local', $3, $4)`, groupID, userID, upstream, override)
	}
	forceMember, forceNonMember := "force_member", "force_non_member"
	approvers := group("Approvers", "local", true, "local")
	retired := group("Retired approvers", "local", false, "local")
	mismatched := group("Mismatched approvers", "scim", true, "local")

	direct := add("a-direct", identity.RoleUserID, approver)
	h.exec(t, `UPDATE identity.users SET preferred_language = 'nb' WHERE id = $1`, direct)
	member(approvers, add("b-forced-member", identity.RoleUserID), false, &forceMember)
	member(approvers, add("c-upstream-member", identity.RoleUserID), true, nil)
	member(approvers, add("x-overridden-out", identity.RoleUserID), true, &forceNonMember)
	member(approvers, add("x-upstream-absent", identity.RoleUserID), false, nil)
	member(retired, add("x-retired-group", identity.RoleUserID), true, &forceMember)
	member(mismatched, add("x-source-mismatch", identity.RoleUserID), true, &forceMember)
	add("d-owner-without-the-key", identity.RoleOwnerID)

	h.exec(t, `UPDATE identity.users SET is_disabled = true, disabled_by = 'owner' WHERE id = $1`, add("x-disabled", approver))
	// Activated by Staff, so enabled, but the invitation not yet accepted.
	h.exec(t, `UPDATE identity.users SET managed_by = 'staff', employee_number = 'E-1', employee_number_key = 'E-1', pending_since = $2 WHERE id = $1`,
		add("x-pending", approver), h.now())
	h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, add("e-locked-out", approver), h.now().Add(time.Hour))

	deactivate := func(userID uuid.UUID) {
		h.exec(t, `INSERT INTO identity.scim_user_mappings (resource_id, user_id, external_id, user_name, upstream_active, version, etag, created_at, updated_at)
		        VALUES ($1, $2, $3, $3, false, 1, 'etag', $4, $4)`, uuid.New(), userID, userID.String(), h.now())
	}
	deactivate(add("f-scim-inactive-owner", identity.RoleOwnerID))
	deactivate(add("g-scim-inactive", approver))

	owner := h.insertUser(t, "z-delegator@example.test", identity.RoleOwnerID)
	f.users["z-delegator"] = owner
	h.exec(t, `INSERT INTO identity.authorization_delegations (id, grantee_user_id, created_by_user_id, version, created_at, updated_at)
	        VALUES ($1, $2, $3, $4, $5, $5)`, uuid.New(), add("x-delegate", identity.RoleUserID), owner, uuid.New(), h.now())
	add("x-holds-another-key", identity.RoleUserID, manager)

	// A directory user named no address keeps a placeholder no mail reaches.
	placeholder := uuid.New()
	email := "scim-" + uuid.NewString() + "@sso.invalid"
	h.exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	        VALUES ($1, $2, upper($2), 'h-placeholder', $3, $4, $4)`, placeholder, email, uuid.New(), h.now())
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, placeholder, approver)
	f.users["h-placeholder"] = placeholder
	return f
}

func (f *holdersFixture) holders(t *testing.T) []contracts.Holder {
	t.Helper()
	got, err := newUserDirectory(t, f.h).PermissionHolders(context.Background(), "staff:approve")
	if err != nil {
		t.Fatalf("PermissionHolders: %v", err)
	}
	return got
}

func (f *holdersFixture) names(got []contracts.Holder) []string {
	byID := map[uuid.UUID]string{}
	for name, id := range f.users {
		byID[id] = name
	}
	names := make([]string, 0, len(got))
	for _, holder := range got {
		names = append(names, byID[holder.ID])
	}
	return names
}

// TestUserDirectory_PermissionHoldersUnderSCIM proves who holds a key (the
// owner's answer 4, reading 3): a direct role, a role mapped to an active
// group the user is effectively a member of (forced in, or present upstream
// and not overridden out, the mapping's source the group's), or the Owner
// role without the key; never a disabled or pending user, a delegate
// without the key, or — while SCIM is configured — a directory-deactivated
// non-Owner; a lockout ignored. The holders come ordered by display name,
// each with their address and language, a placeholder address empty.
func TestUserDirectory_PermissionHoldersUnderSCIM(t *testing.T) {
	t.Parallel()
	f := newHoldersFixture(t, withEnv("SCIM_TOKEN", "identity-harness-scim-token"))

	got := f.holders(t)
	want := []string{"a-direct", "b-forced-member", "c-upstream-member", "d-owner-without-the-key", "e-locked-out", "f-scim-inactive-owner", "h-placeholder", "z-delegator"}
	if names := f.names(got); !slices.Equal(names, want) {
		t.Fatalf("holders = %v, want %v", names, want)
	}
	if got[0] != (contracts.Holder{ID: f.users["a-direct"], DisplayName: "a-direct@example.test", Email: "a-direct@example.test", PreferredLanguage: "nb"}) {
		t.Errorf("the direct holder = %+v, want their name, address and language", got[0])
	}
	if got[1].PreferredLanguage != "" {
		t.Errorf("a holder who chose no language = %q, want empty", got[1].PreferredLanguage)
	}
	if placeholder := got[6]; placeholder.Email != "" || placeholder.DisplayName != "h-placeholder" {
		t.Errorf("the placeholder holder = %+v, want them named with an empty address", placeholder)
	}
}

// TestUserDirectory_PermissionHoldersWithoutSCIM proves a directory's
// deactivation counts only while SCIM is configured, as a session's does:
// without it the deactivated non-Owner holds the key again.
func TestUserDirectory_PermissionHoldersWithoutSCIM(t *testing.T) {
	t.Parallel()
	f := newHoldersFixture(t)

	want := []string{"a-direct", "b-forced-member", "c-upstream-member", "d-owner-without-the-key", "e-locked-out", "f-scim-inactive-owner", "g-scim-inactive", "h-placeholder", "z-delegator"}
	if names := f.names(f.holders(t)); !slices.Equal(names, want) {
		t.Errorf("holders = %v, want %v", names, want)
	}
}

// TestUserDirectory_PermissionHoldersOfAKeyNobodyHolds proves a key no role
// carries is held by the Owners alone, and with no active Owner by nobody: an
// empty answer, not an error.
func TestUserDirectory_PermissionHoldersOfAKeyNobodyHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, h.insertUser(t, "former-owner@example.test", identity.RoleOwnerID))

	got, err := newUserDirectory(t, h).PermissionHolders(context.Background(), "staff:personal-data")
	if err != nil || len(got) != 0 {
		t.Errorf("PermissionHolders = %+v, %v, want none and no error", got, err)
	}
}

// TestUserDirectory_PermissionHoldersAreWhoTheCheckAdmits is the
// differential test between the two answers to "who holds staff:approve":
// for every fixture user, a session of theirs passes
// Access.Check("permission:staff:approve") exactly when PermissionHolders
// names them, under SCIM and without. The one documented difference is the
// lockout, which ends a permission check for a while but not a holding.
// Sessions are MFA-verified: MFA belongs to a sign-in, not a user, so an
// Owner held to MFA (OwnersRequireMFA) is a holder whose sessions without a
// second factor are refused — the session's matter, not the holding's.
func TestUserDirectory_PermissionHoldersAreWhoTheCheckAdmits(t *testing.T) {
	t.Parallel()
	for name, opts := range map[string][]harnessOption{
		"under SCIM":   {withEnv("SCIM_TOKEN", "identity-harness-scim-token")},
		"without SCIM": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newHoldersFixture(t, opts...)
			holders := map[string]bool{}
			for _, holder := range f.names(f.holders(t)) {
				holders[holder] = true
			}
			for user, id := range f.users {
				_, o := check(t, f.h, f.h.session(t, id, true), "permission:staff:approve")
				want := holders[user]
				if user == "e-locked-out" {
					want = false // a holder, locked out of checks for now
				}
				if got := o == allowed; got != want {
					t.Errorf("%s: the check admits = %v (%v), PermissionHolders names = %v", user, got, o, holders[user])
				}
			}
		})
	}
}
