package identity

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

var (
	CreateSession       = (*Access).createSession
	RevokeOtherSessions = (*Access).revokeOtherSessions
	RevokeAllSessions   = (*Access).revokeAllSessions
	HashPassword        = hashPassword
)
