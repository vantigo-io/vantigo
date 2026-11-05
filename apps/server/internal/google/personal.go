package google

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the person's own Google connection (Google Workspace design
// G25–G29, docs/superpowers/specs/2026-11-03-google-workspace-design.md;
// delivery D, docs/superpowers/plans/2026-11-03-google-connection.md): the
// authorization code flow with PKCE and a nonce of the organisation's
// Internal web client, behind the Owner's Connection for people switch, its
// rows in google.connections. Entra's Outlook connection is the template:
// a sealed Lax state cookie to the anonymous callback (personal_connect.go,
// personal_redeem.go), then a sealed Lax connected cookie to complete under
// the Strict session, bound to it by a MAC of the session id
// (personal_complete.go). This file holds what they share: the paths, the
// cookies and their purposes, returnTo, the features and their scopes, and
// the person's certain account (G27), read from Staff's
// Workforce.DirectoryAccount under the request's own principal.
//
// Nothing here logs, audits or answers a code, a token, a state, a nonce,
// a verifier, a secret or an address.

// The flow's paths: the cookies' path, under which connect, the callback
// (connectCallbackPath, connection.go) and complete all lie; and the page
// every answer of the callback returns to.
const (
	personalCookiePath = "/api/v1/google"
	personalPagePath   = "/settings/connections"
)

// The flow's two cookies, both HttpOnly and SameSite=Lax (Google's redirect
// back is a cross-site top-level navigation, which Lax admits and Strict
// would not), on personalCookiePath under the base path.
const (
	// personalStateCookie carries connect's state, nonce and PKCE verifier
	// to the callback, sealed under personalStatePurpose.
	personalStateCookie = "vantigo.google.connect"
	// personalConnectedCookie carries the callback's validated account and
	// refresh token to complete, sealed under personalConnectedPurpose.
	personalConnectedCookie = "vantigo.google.connected"
)

// The secrets box purposes, each its own key, so neither cookie can be
// replayed as the other, nor either as a stored refresh token; and the MAC
// purpose binding a cookie to the session that started the connect.
const (
	personalStatePurpose     = "google/connect-state"
	personalConnectedPurpose = "google/connected"
	personalRefreshPurpose   = "google/connection-refresh"
	personalSessionPurpose   = "google/connect-session"
)

// The cookies' lifetimes, enforced on the Deps clock from the expiry sealed
// inside each, not only by the browser's Max-Age.
const (
	personalStateLifetime     = 10 * time.Minute
	personalConnectedLifetime = 5 * time.Minute
)

// maxConnectedCookie is the longest connected cookie value set, in bytes: a
// browser drops a cookie past about 4 KB silently.
const maxConnectedCookie = 3800

// maxReturnTo is the longest returnTo kept, in bytes.
const maxReturnTo = 512

// The personal connection's refusal codes (GoogleProblem).
const (
	codeSwitchOff       = "switch_off"
	codeExpired         = "expired"
	codeNoAccount       = "no_account"
	codeAccountTaken    = "account_taken"
	codeAccountMismatch = "account_mismatch"
	codeFeatureUnknown  = "feature_unknown"
)

// The callback's outcome codes besides codeExpired (G26).
const (
	personalAccessDenied   = "access_denied"
	personalDomainMismatch = "domain_mismatch"
	personalConsentMissing = "consent_missing"
	personalFailed         = "failed"
)

// The connection's states (00116).
const (
	personalActive    = "active"
	personalReconnect = "reconnect"
	personalClosing   = "closing"
)

// Google's authorization server, fixed like the module's other hosts.
const (
	// googleIssuer is the ID token's issuer and the OIDC discovery's base.
	googleIssuer = "https://accounts.google.com"
	// authorizeEndpoint is the authorization endpoint (not a call the
	// module makes: the browser is sent there).
	authorizeEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	// revokeURL is where a token is revoked.
	revokeURL = "https://oauth2.googleapis.com/revoke"
)

// personalBaseScopes are what every connect asks besides its features':
// the ID token, the address and the name. access_type=offline asks for the
// refresh token.
var personalBaseScopes = []string{"openid", "email", "profile"}

// personalFeatures maps each feature a connect may name to the scopes it
// asks (G26): a consumer names a feature, never the vendor's scope. No
// calendar or Gmail scope is ever delegated through the service account
// (Q3); these are the person's own consent.
var personalFeatures = map[string][]string{
	"calendar": {scopePrefix + "calendar.events", scopePrefix + "calendar.app.created"},
	"mail":     {scopePrefix + "gmail.send"},
}

// grantedScopes is a token answer's scope as stored: sorted and compacted,
// spelled as Google answered.
func grantedScopes(scope string) []string {
	out := strings.Fields(scope)
	slices.Sort(out)
	return slices.Compact(out)
}

// hasScopes reports whether granted holds every one of want.
func hasScopes(granted, want []string) bool {
	for _, w := range want {
		if !slices.Contains(granted, w) {
			return false
		}
	}
	return true
}

// featuresOf is the features granted allows, sorted.
func featuresOf(granted []string) []string {
	out := []string{}
	for feature, scopes := range personalFeatures {
		if hasScopes(granted, scopes) {
			out = append(out, feature)
		}
	}
	slices.Sort(out)
	return out
}

// connectScopes is the scope a connect for feature asks: the base, the
// feature's, and those of every feature held already allows — so a
// connect for mail never comes back without the calendar, whether or not
// Google answers the union of an incremental grant (U9).
func connectScopes(feature string, held []string) []string {
	rest := slices.Clone(personalFeatures[feature])
	for _, f := range featuresOf(held) {
		rest = append(rest, personalFeatures[f]...)
	}
	slices.Sort(rest)
	return append(slices.Clone(personalBaseScopes), slices.Compact(rest)...)
}

// safeReturnTo is returnTo when it is a path on this installation, else
// the Connections page (the Outlook design's M4 rule): at most maxReturnTo
// bytes, a single leading slash, no backslash, no control character, and
// neither a scheme nor a host once parsed.
func safeReturnTo(returnTo string) string {
	if returnTo == "" || len(returnTo) > maxReturnTo || returnTo[0] != '/' {
		return personalPagePath
	}
	if len(returnTo) > 1 && (returnTo[1] == '/' || returnTo[1] == '\\') {
		return personalPagePath
	}
	for i := 0; i < len(returnTo); i++ {
		if b := returnTo[i]; b < 0x20 || b == 0x7f || b == '\\' {
			return personalPagePath
		}
	}
	u, err := url.Parse(returnTo)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return personalPagePath
	}
	return returnTo
}

// personalRedirectURI is the redirect URI registered on the web client:
// APP_URL, the base path and the callback. Empty without a configuration.
func personalRedirectURI(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + connectCallbackPath
}

// basePath is the configuration's base path, empty without one.
func basePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.BasePath
}

// personalStateBody is the state cookie's content: what the callback needs
// to check Google's answer and carry it on to complete.
type personalStateBody struct {
	State      string    `json:"state"`
	Nonce      string    `json:"nonce"`
	Verifier   string    `json:"verifier"`
	UserID     uuid.UUID `json:"userId"`
	SessionMAC []byte    `json:"sessionMac"`
	Feature    string    `json:"feature"`
	Scopes     []string  `json:"scopes"`
	ReturnTo   string    `json:"returnTo"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (b personalStateBody) expires() time.Time { return b.ExpiresAt }

// personalConnectedBody is the connected cookie's content: the validated
// account (its sub and address), its granted scopes and refresh token, and
// the person and session the connect was started by.
type personalConnectedBody struct {
	UserID       uuid.UUID `json:"userId"`
	SessionMAC   []byte    `json:"sessionMac"`
	Sub          string    `json:"sub"`
	Email        string    `json:"email"`
	Scopes       []string  `json:"scopes"`
	RefreshToken string    `json:"refreshToken"`
	Feature      string    `json:"feature"`
	ReturnTo     string    `json:"returnTo"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (b personalConnectedBody) expires() time.Time { return b.ExpiresAt }

// personalCookie is one of the flow's cookies: identity's session cookie's
// HttpOnly and Secure policy, but SameSite=Lax, the flow's path and its
// lifetime as Max-Age.
func (s *server) personalCookie(name, value string, lifetime time.Duration) *http.Cookie {
	secure := s.deps.Config != nil && s.deps.Config.UsesHTTPS()
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     basePath(s.deps.Config) + personalCookiePath,
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// setPersonalCookie adds c to the answer through the response writer the
// router attached to ctx, so a cookie is set or cleared whatever the answer.
func setPersonalCookie(ctx context.Context, c *http.Cookie) error {
	w, ok := contracts.ResponseWriterFrom(ctx)
	if !ok {
		return errors.New("google: no response writer to set a cookie on")
	}
	http.SetCookie(w, c)
	return nil
}

// clearPersonalCookie tells the browser to drop one of the flow's cookies:
// each is single use, spent on every answer of the step that reads it.
func (s *server) clearPersonalCookie(ctx context.Context, name string) {
	c := s.personalCookie(name, "", 0)
	c.MaxAge = -1
	_ = setPersonalCookie(ctx, c)
}

// sealPersonal seals v's JSON under purpose, as cookie text.
func (s *server) sealPersonal(purpose string, v any) (string, error) {
	if s.deps.Secrets == nil {
		return "", errors.New("google: no secrets box to seal the connect with")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("google: seal the connect: %w", err)
	}
	sealed, err := s.deps.Secrets.SealString(purpose, string(b))
	if err != nil {
		return "", fmt.Errorf("google: seal the connect: %w", err)
	}
	return sealed, nil
}

// openPersonalCookie opens the request's cookie name, sealed under purpose,
// into v, and reports whether it is there, authentic and unexpired on the
// Deps clock.
func (s *server) openPersonalCookie(ctx context.Context, name, purpose string, v interface{ expires() time.Time }) bool {
	r, ok := contracts.RequestFrom(ctx)
	if !ok || s.deps.Secrets == nil {
		return false
	}
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return false
	}
	plain, err := s.deps.Secrets.OpenString(purpose, c.Value)
	if err != nil || json.Unmarshal([]byte(plain), v) != nil {
		return false
	}
	return s.keys.c.now().Before(v.expires())
}

// sessionMAC is the MAC binding a connect to the session that started it.
func (s *server) sessionMAC(sessionID uuid.UUID) ([]byte, error) {
	if s.deps.Secrets == nil {
		return nil, errors.New("google: no secrets box to bind the connect with")
	}
	mac, err := s.deps.Secrets.MAC(personalSessionPurpose, sessionID[:])
	if err != nil {
		return nil, fmt.Errorf("google: bind the connect to the session: %w", err)
	}
	return mac, nil
}

// randomValue is a fresh state or nonce: 256 random bits, base64url.
func randomValue() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return base64.RawURLEncoding.EncodeToString(b)
}

// requestPrincipal is the signed-in caller of a session operation, with the
// session it came with.
func requestPrincipal(ctx context.Context) (contracts.Principal, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return contracts.Principal{}, fmt.Errorf("google: a personal connection operation without a signed-in user: %w", contracts.ErrInvalidActor)
	}
	return p, nil
}

// certainAccount is the caller's certain account (G27): Staff's directory
// account for the principal in ctx, from this provider and enabled, whose
// Directory id the module keeps a trusted, enabled row for. Nil when there
// is none. It calls out of the module, so never inside one of its
// transactions.
func (s *server) certainAccount(ctx context.Context) (*store.GoogleAccount, error) {
	return certainAccountOf(ctx, s.deps.Workforce, s.q)
}

// certainAccountOf is certainAccount over Staff's workforce and the
// module's queries, for the connect and personal sending (G35) alike.
func certainAccountOf(ctx context.Context, workforce contracts.Workforce, q *store.Queries) (*store.GoogleAccount, error) {
	if workforce == nil {
		return nil, nil
	}
	da, err := workforce.DirectoryAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the directory account: %w", err)
	}
	if da == nil || da.Provider != providerKey || !da.Enabled || !validUserID(da.ExternalID) {
		return nil, nil
	}
	row, err := q.GetAccount(ctx, da.ExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("google: read the account: %w", err)
	}
	if !usableAccount(row) {
		return nil, nil
	}
	return &row, nil
}

// usableAccount reports whether a connection may stand on row: an account
// the module made or whose adoption is confirmed, enabled (G27).
func usableAccount(row store.GoogleAccount) bool {
	return trusted(row) && row.State == stateEnabled
}

// emailDomain is the domain of an address, lower-case; "" without one.
func emailDomain(address string) string {
	_, domain, ok := strings.Cut(address, "@")
	if !ok {
		return ""
	}
	return strings.ToLower(domain)
}

// The reasons Connection for people is not ready (GooglePersonalSwitch).
const (
	personalNoWebClient   = "no_web_client"
	personalNotTested     = "not_tested"
	personalClientRefused = "client_refused"
)

// clientRefusals are the token endpoint's refusals of the web client itself,
// not of a person (G28): personal_last_error, the Owner's to answer.
var clientRefusals = []string{"invalid_client", "unauthorized_client"}

// webClientReason is why the web client cannot connect anyone, "" when it
// can: no client id or no live secret, or no test has read the domains an
// ID token's hd is checked against.
func webClientReason(conn store.GoogleConnection, creds []store.GoogleCredential) string {
	switch {
	case conn.OauthClientID == nil || liveSecret(creds) == nil:
		return personalNoWebClient
	case len(conn.Domains) == 0:
		return personalNotTested
	}
	return ""
}

// personalSwitchBody is Connection for people as GET /connection answers it
// (G29): off while the switch is; unavailable with its reason while the web
// client is not ready or Google refused it; consent_missing while the last
// connect came back without its feature; ready otherwise.
func personalSwitchBody(conn store.GoogleConnection, creds []store.GoogleCredential, counts store.CountPersonalRow, cfg *config.Config) gen.GooglePersonalSwitch {
	body := gen.GooglePersonalSwitch{
		Enabled:     conn.PersonalConnection,
		Status:      gen.GooglePersonalSwitchStatusOff,
		Connected:   int32(counts.Connected),
		Reconnect:   int32(counts.Reconnect),
		RedirectUri: personalRedirectURI(cfg),
	}
	last := deref(conn.PersonalLastError)
	reason := webClientReason(conn, creds)
	if reason == "" && slices.Contains(clientRefusals, last) {
		reason = personalClientRefused
	}
	switch {
	case !conn.PersonalConnection:
	case reason != "":
		body.Status = gen.GooglePersonalSwitchStatusUnavailable
		body.Reason = &reason
	case last == personalConsentMissing:
		body.Status = gen.GooglePersonalSwitchStatusConsentMissing
	default:
		body.Status = gen.GooglePersonalSwitchStatusReady
	}
	return body
}

// personalOffered reports whether a person may connect at all: the switch
// on and the web client ready.
func personalOffered(conn store.GoogleConnection, creds []store.GoogleCredential) bool {
	return conn.PersonalConnection && webClientReason(conn, creds) == ""
}

// personalConnectionBody is the caller's connection as the contract
// answers it: whether they may connect and why not, the account Vantigo
// knows them by, and their row (nil for none).
func personalConnectionBody(offered bool, account *store.GoogleAccount, row *store.GooglePersonalConnection) gen.GooglePersonalConnection {
	body := gen.GooglePersonalConnection{
		Offered:  offered && account != nil,
		State:    gen.NotConnected,
		Features: []string{},
	}
	switch {
	case !offered:
		reason := gen.SwitchOff
		body.Reason = &reason
	case account == nil:
		reason := gen.NoAccount
		body.Reason = &reason
	}
	if account != nil {
		address := account.PrimaryEmail
		body.Account = &address
	}
	if row != nil {
		body.State = gen.GooglePersonalConnectionState(row.State)
		body.Features = featuresOf(row.Scopes)
		connectedAt := row.ConnectedAt
		body.ConnectedAt = &connectedAt
		if row.State == personalReconnect {
			body.ErrorCode = row.ErrorCode
		}
	}
	return body
}

// callerConnection is the caller's row: their live one, or else a closing
// one of their certain account's that is still theirs. Nil for none.
func (s *server) callerConnection(ctx context.Context, userID uuid.UUID, account *store.GoogleAccount) (*store.GooglePersonalConnection, error) {
	row, err := s.q.GetLivePersonalByUser(ctx, userID)
	if err == nil {
		return &row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("google: read the personal connection: %w", err)
	}
	if account == nil {
		return nil, nil
	}
	row, err = s.q.GetPersonal(ctx, account.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("google: read the personal connection: %w", err)
	}
	if row.UserID != userID {
		return nil, nil
	}
	return &row, nil
}

// lockedRow is a locking read's row, nil for none.
func lockedRow(row store.GooglePersonalConnection, err error) (*store.GooglePersonalConnection, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("google: lock the personal connection: %w", err)
	}
	return &row, nil
}

// readFooting is the connection and its credentials, read on the pool.
func readFooting(ctx context.Context, q *store.Queries) (store.GoogleConnection, []store.GoogleCredential, error) {
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return store.GoogleConnection{}, nil, fmt.Errorf("google: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return store.GoogleConnection{}, nil, fmt.Errorf("google: read the credentials: %w", err)
	}
	return conn, creds, nil
}
