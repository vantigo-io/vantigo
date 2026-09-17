package identity_test

import (
	"context"
	"strings"
	"testing"

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
	email := strings.ToLower(strings.NewReplacer(" ", ".", "%", "pct", "_", "underscore").Replace(displayName)) + "-" + id.String() + "@example.test"
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

// TestUserDirectory_SearchUsersEscapesWildcards proves a literal % or _ in
// the query matches only that literal character, not ILIKE's wildcard
// meaning.
func TestUserDirectory_SearchUsersEscapesWildcards(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	percent := insertUserNamed(t, h, "100% Match", false)
	insertUserNamed(t, h, "100X Match", false)

	got, err := newUserDirectory(t, h).SearchUsers(context.Background(), "100% match", 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(got) != 1 || got[0].ID != percent {
		t.Errorf("SearchUsers(100%% match) = %+v, want only the literal %% match", got)
	}
}
