package identity

// Hooks for the identity_test package. Until the sign-in endpoints exist,
// tests start and revoke sessions directly, exactly as those endpoints will.
const SessionCookieName = sessionCookieName

var (
	CreateSession       = (*Access).createSession
	RevokeOtherSessions = (*Access).revokeOtherSessions
	RevokeAllSessions   = (*Access).revokeAllSessions
)
