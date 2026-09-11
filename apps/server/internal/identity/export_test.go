package identity

import "net/http"

// Hooks for the identity_test package. Tests start and revoke sessions
// directly, exactly as the endpoints do, where no endpoint yet reaches the
// state they need (an MFA session, a caller-sparing revocation), and seed
// password users no endpoint creates (an unconfirmed email, a test without
// an Owner).
const SessionCookieName = sessionCookieName

const LoginTicketCookieName = loginTicketCookieName

// OwnerMutationLock is the owner lock's key, for a test that holds the lock
// itself to force a race's interleaving.
const OwnerMutationLock = ownerMutationLock

// RoleMutationLockKey is a role's advisory lock key, for a test that holds
// the lock itself to force a race's interleaving.
var RoleMutationLockKey = roleMutationLockKey

// ScimLockKey is the SCIM lock's key, for a test that holds the lock itself
// to make SCIM writes queue behind it.
const ScimLockKey = scimLockKey

// ScimConnectionID is the one static SCIM connection the audit facts name.
var ScimConnectionID = scimConnectionID

// The workforce OIDC flow's two cookies, for tests that assert on them.
const (
	OIDCStateCookieName    = oidcStateCookieName
	OIDCExternalCookieName = oidcExternalCookieName
)

// SetOIDCHTTPClient routes a's workforce OIDC discovery, key and token
// requests through c, a test's fake provider. It must run before the
// module mounts, which is when the relying party takes its client.
func SetOIDCHTTPClient(a *Access, c *http.Client) { a.oidcHTTPClient = c }

var (
	CreateSession       = (*Access).createSession
	RevokeOtherSessions = (*Access).revokeOtherSessions
	RevokeAllSessions   = (*Access).revokeAllSessions
	HashPassword        = hashPassword
)
