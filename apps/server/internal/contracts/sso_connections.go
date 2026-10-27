package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrSSONotConfigured is SSOConnections.Unconnected's answer to an Owner
// while OIDC is off: there is no issuer to connect anybody under, which a
// consumer tells apart from nobody being left to connect.
var ErrSSONotConfigured = errors.New("contracts: single sign-on is not configured")

// The kinds of holder KeyHolders answers: who already holds a directory key
// under the configured issuer.
const (
	// KeyHolderExpected: an Owner's announced link names the key for an
	// existing user (KeyHolder.UserID).
	KeyHolderExpected = "expected"
	// KeyHolderMapped: a SCIM mapping holds the key for a user
	// (KeyHolder.UserID) — its userName equal to the principal name, or its
	// externalId to the object id.
	KeyHolderMapped = "mapped"
	// KeyHolderAccount: an account Staff announced, for a user yet to
	// arrive, names the key. It has no user.
	KeyHolderAccount = "account"
)

// SSOConnections is what connecting existing users to the workforce
// provider needs of identity (Entra phase 2 design, delivery B; plan reading
// 1): the users not yet connected under the configured issuer, and who
// already holds a directory account's keys. Identity is its only provider
// (module.Module.SSOConnections) and Entra its consumer, which reads it
// through module.Deps.SSOConnections and never identity's tables.
//
// Identity is its own gatekeeper (module-boundaries rule 12): it checks the
// request in ctx (contracts.RequestFrom) against policy:Owner+OwnerManagement
// through its own access, and answers nothing — (nil, nil), not an error —
// to anyone else, and to a context with no request. Only a failure to read
// is an error. While OIDC is off there is no issuer to connect under:
// Unconnected answers ErrSSONotConfigured to an Owner, and KeyHolders
// nothing.
//
// A ConnectCandidate's Email is for matching a directory account and nothing
// else: it never leaves the consumer — not on the wire, not in a log, an
// event or an error.
type SSOConnections interface {
	// Unconnected lists every user who is not pending, holds no link under
	// the configured issuer and no announced link under it, the caller
	// excluded (an Owner connects themselves by signing in), in the order
	// the Owner's user list shows them: the Owners first, then by display
	// name, then by id — at most limit of them, so that a caller asking
	// for one more than it reads learns whether more wait. While OIDC is
	// off it answers an Owner ErrSSONotConfigured.
	Unconnected(ctx context.Context, limit int) ([]ConnectCandidate, error)
	// KeyHolders answers, for each key that is held under the configured
	// issuer, who holds it: one KeyHolder per held key, carrying the key as
	// given, the first holder in the order an announced link, a SCIM
	// mapping, an announced account. A key nobody holds is absent. Keys are
	// compared as identity normalises them — the object id as a GUID in
	// lower-case dashed form, the principal name as an email address,
	// case-insensitively — so a caller passes them as the directory answers
	// them.
	KeyHolders(ctx context.Context, keys []DirectoryKey) ([]KeyHolder, error)
}

// ConnectCandidate is a user Unconnected lists.
type ConnectCandidate struct {
	UserID uuid.UUID
	// Email is the user's address, for the match alone (see
	// SSOConnections); "" for an address identity made up for an account the
	// provider created.
	Email string
	// EmployeeNumber is the user's employee number, "" for none.
	EmployeeNumber string
	Disabled       bool
}

// DirectoryKey is a directory account's two keys, either of which may be
// "".
type DirectoryKey struct {
	ObjectID, UserPrincipalName string
}

// KeyHolder is who holds a DirectoryKey (KeyHolders).
type KeyHolder struct {
	// ObjectID and UserPrincipalName are the key asked about, as given.
	ObjectID, UserPrincipalName string
	// Kind is KeyHolderExpected, KeyHolderMapped or KeyHolderAccount.
	Kind string
	// UserID is the user holding the key; nil for KeyHolderAccount.
	UserID *uuid.UUID
}
