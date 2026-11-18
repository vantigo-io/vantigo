package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
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

// ErrInvalidSpec is the provider's refusal of a spec identity cannot store.
var ErrInvalidSpec = errInvalidSpec

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

// SetWebhookResolver makes r what a webhook endpoint's save resolves its
// host with, so no test touches DNS.
func SetWebhookResolver(a *Access, r netguard.Resolver) { a.webhookResolver = r }

// WebhookSecretPurpose is the secrets.Box purpose an endpoint's secret is
// sealed under, for a test that opens the column.
var WebhookSecretPurpose = webhookSecretPurpose

// SetWebhookServer makes every webhook the sender posts reach srv, through
// the guarded transport: each name resolves to a public address, netguard
// checks it, and the dialler then connects to srv, whose certificate (issued
// to example.com and *.example.com) is trusted. It must run before
// module.Workers builds the sender.
func SetWebhookServer(a *Access, srv *httptest.Server) {
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	addr := srv.Listener.Addr().String()
	a.webhookNet = &webhookNetwork{
		resolver: publicResolver{},
		dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		tls: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
}

type publicResolver struct{}

func (publicResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
}

// WebhookSender is identity.webhook-sender as the tests drive it.
type WebhookSender interface {
	Name() string
	Run(context.Context) error
	RunPass(context.Context) (int, error)
	Wait()
	Housekeep(context.Context) error
}

// SetWebhookJitter fixes the sender's jitter draw, in [0, 1): 0.5 is none.
func SetWebhookJitter(w WebhookSender, f func() float64) { w.(*webhookSender).jitter = f }

// SetWebhookAttemptBudget shortens one attempt's whole budget, for a test of
// a receiver that never answers.
func SetWebhookAttemptBudget(w WebhookSender, d time.Duration) {
	w.(*webhookSender).client.http.Timeout = d
}

// WebhookRetrySchedule is W17's schedule.
var WebhookRetrySchedule = webhookRetrySchedule

var (
	CreateSession       = (*Access).createSession
	RevokeOtherSessions = (*Access).revokeOtherSessions
	RevokeAllSessions   = (*Access).revokeAllSessions
	HashPassword        = hashPassword
)

// NewTokenForTest mints a token and its stored hash the way issueInvitation does.
func NewTokenForTest() (string, []byte) { return newToken() }

// NewAPIKeyForTest mints an API key and its stored hash as the integrations
// service does, stored nowhere: a key no installation knows.
func NewAPIKeyForTest() (string, []byte) { return newAPIKey() }

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

// The shared stations' names and helpers (manufacturing phase 2 M129–M143),
// for the station tests.
const StationCookieName = stationCookieName

// StationAttemptsLimit is how many operator sign-ins a station verifies in
// its window, failed or not.
var StationAttemptsLimit = policyStationAttempts.Limit

var (
	ValidatePIN          = validatePIN
	NormalizePairingCode = normalizePairingCode
	BackoffAfter         = backoffAfter
	VerifyPassword       = verifyPassword
)

// PepperPIN is the PIN as the installation's box peppers it before argon2id.
func PepperPIN(box interface {
	MAC(string, []byte) ([]byte, error)
}, pin string) (string, error) {
	mac, err := box.MAC(pinPepperPurpose, []byte(pin))
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(mac), nil
}
