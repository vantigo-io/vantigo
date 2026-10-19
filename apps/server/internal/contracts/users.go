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
	// PreferredLanguage is the language the user chose for the interface,
	// "en", "nb" or "" when they chose none: a fact of the user that is not
	// sensitive, for a module writing something in the user's own language
	// (calendar invitations design D3).
	PreferredLanguage string
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
	// PermissionHolders lists the active users who hold the permission key,
	// ordered by display name. See Holder for who counts.
	PermissionHolders(ctx context.Context, key string) ([]Holder, error)
	// Notify mails the user through the installation's mail driver. An
	// inactive or unknown user is not mailed and is not an error; a driver
	// that is not configured is not an error either (the notice is simply not
	// sent). A send that fails is an error that names no address.
	//
	// Active means what it means for a Holder. The address is identity's and
	// stays there: Notify is how a module mails a colleague without ever
	// being handed it (calendar invitations design D4).
	Notify(ctx context.Context, userID uuid.UUID, n Notice) error
}

// Notice is a plain mail to one user, composed by a module in the languages it
// has, delivered by identity in the user's own, and never answering the
// address.
type Notice struct {
	// Texts is keyed by language, "en" and "nb"; identity picks the user's
	// preferred language, then "en", then any. A notice with no text is not
	// sent.
	Texts map[string]NoticeText
}

// NoticeText is a Notice in one language: a subject and a plain-text body.
type NoticeText struct{ Subject, Body string }

// Holder is a user PermissionHolders names: an active user holding the
// permission asked about.
//
// A user holds a key through a role they hold directly, through a role mapped
// to an active access group they are effectively a member of (forced in, or
// present upstream and not overridden out — exactly the rule a permission
// check applies), or as an Owner, who holds every key. A delegation grants
// authorization management only, never a key. Active means what a session
// needs: not disabled, not pending (a Staff-managed user who has not yet
// chosen a credential), and — while SCIM is configured — not deactivated by
// the directory unless an Owner. A lockout is temporary and ignored.
//
// Email is the address to mail the holder at, "" when identity holds none a
// mail can reach (a directory's or an identity provider's placeholder). It is
// for mailing the holder and nothing else: never put on a consumer's wire,
// in a log, an event or an error.
type Holder struct {
	ID                uuid.UUID
	DisplayName       string
	Email             string
	PreferredLanguage string // "en", "nb" or "" when the user chose none
}
