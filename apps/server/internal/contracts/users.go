package contracts

import (
	"context"

	"github.com/google/uuid"
)

// UserEntry is a user as another module may reference it: enough to name
// them and know whether they can still act, never enough to manage identity
// itself — that stays behind identity's own contract and permissions.
type UserEntry struct {
	ID          uuid.UUID
	DisplayName string
	Active      bool // not disabled
}

// UserDirectory is the one sanctioned way a module reads identity's user
// data: a read-only, in-process port over identity's users, so a module can
// name a user on a role or a billing line and let a caller search for one to
// assign, without either importing the identity package (barred by
// depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). identity implements it; Compose wires that
// implementation into every module's Deps before any Mount runs (see
// Module.Users). Unlike CustomerDirectory, it is always set once composed:
// identity is always mounted, so there is no "disabled" case for a caller to
// handle.
//
// Two rules hold, because neither is obvious from the signatures alone:
//
//   - A missing id is (nil, nil) from User, or simply absent from Users'
//     result — never an error. A caller tells "does not exist" from "the
//     lookup failed" by checking err, never by treating a nil result or a
//     shorter slice as failure.
//   - SearchUsers only ever returns active users. A disabled user can still
//     hold a long-lived reference elsewhere (a role, a past assignment), and
//     User/Users still resolve it so that reference can be decorated — but
//     nothing should let a caller newly assign work to someone who can no
//     longer act, so search excludes them.
type UserDirectory interface {
	// User looks up a user by ID. It returns (nil, nil) if id does not
	// exist.
	User(ctx context.Context, id uuid.UUID) (*UserEntry, error)
	// Users looks up users by ID. An id that does not exist is simply
	// absent from the result, not an error.
	Users(ctx context.Context, ids []uuid.UUID) ([]UserEntry, error)
	// SearchUsers finds active users whose display name matches query,
	// ordered by display name, capped at limit.
	SearchUsers(ctx context.Context, query string, limit int) ([]UserEntry, error)
}
