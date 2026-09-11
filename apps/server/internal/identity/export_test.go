package identity

// Hooks for the identity_test package. Tests start and revoke sessions
// directly, exactly as the endpoints do, where no endpoint yet reaches the
// state they need (an MFA session, a caller-sparing revocation), and seed
// password users until the owner user-management endpoints exist.
const SessionCookieName = sessionCookieName

const LoginTicketCookieName = loginTicketCookieName

var (
	CreateSession       = (*Access).createSession
	RevokeOtherSessions = (*Access).revokeOtherSessions
	RevokeAllSessions   = (*Access).revokeAllSessions
	HashPassword        = hashPassword
)
