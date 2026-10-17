package identity

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

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

// NewTokenForTest mints a token and its stored hash the way issueInvitation does.
func NewTokenForTest() (string, []byte) { return newToken() }

// NormalizeEmailForTest is normalizeEmail.
func NormalizeEmailForTest(email string) string { return normalizeEmail(email) }

// IssueInvitationForTest issues a User invitation to email at now, through
// issueInvitation in its own transaction, naming the user userID names (nil
// for none) as Staff's provisioning will, and returns its id and token. No
// endpoint issues a named invitation yet.
func IssueInvitationForTest(ctx context.Context, pool *pgxpool.Pool, email string, userID *uuid.UUID, now time.Time) (uuid.UUID, string, error) {
	var inv store.IdentityInvitation
	var token string
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		inv, token, err = issueInvitation(ctx, store.New(tx), 7*24*time.Hour, store.InsertInvitationParams{
			Email: email, NormalizedEmail: normalizeEmail(email), Role: RoleUser, UserID: userID,
		}, now)
		return err
	})
	return inv.ID, token, err
}
