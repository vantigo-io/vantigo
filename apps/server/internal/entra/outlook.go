package entra

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
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the person's Outlook connection (Outlook connection and
// Microsoft 365 mail design M2–M8,
// docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md;
// delivery A, docs/superpowers/plans/2026-10-29-entra-outlook-connection.md):
// a confidential client's authorization code flow with PKCE, a nonce and
// cp1, behind the Owner's Outlook connection switch, its rows in
// entra.outlook_connections. Identity's two-cookie pattern carries it: a
// sealed Lax state cookie to the anonymous callback (outlook_connect.go,
// outlook_redeem.go), then a sealed Lax connected cookie to complete under
// the Strict session, bound to it by a MAC of the session id
// (outlook_complete.go). This file holds what they share: the paths, the
// cookies and their purposes, returnTo, the scopes and the person's certain
// account (M6), read from Staff's Workforce.DirectoryAccount under the
// request's own principal.
//
// Nothing here logs, audits or answers a code, a token, a state, a nonce,
// a verifier or a secret.

// The flow's paths: the redirect URI's, on APP_URL with the base path and
// never the public host; the cookies' path, under which every operation of
// the flow lies; and the page every answer of the callback returns to.
const (
	outlookCallbackPath = "/api/v1/entra/outlook/callback"
	outlookCookiePath   = "/api/v1/entra/outlook"
	outlookPagePath     = "/settings/connections"
)

// The flow's two cookies, both HttpOnly and SameSite=Lax (Microsoft's
// redirect back is a cross-site top-level navigation, which Lax admits and
// Strict would not), on outlookCookiePath under the base path.
const (
	// outlookStateCookie carries connect's state, nonce and PKCE verifier
	// to the callback, sealed under outlookStatePurpose.
	outlookStateCookie = "vantigo.entra.connect"
	// outlookConnectedCookie carries the callback's validated account and
	// refresh token to complete, sealed under outlookConnectedPurpose.
	outlookConnectedCookie = "vantigo.entra.connected"
)

// The secrets box purposes, each its own key (secrets.Box derives one per
// purpose), so neither cookie can be replayed as the other, nor either as
// a stored refresh token; and the MAC purpose binding a cookie to the
// session that started the connect.
const (
	outlookStatePurpose     = "entra/outlook-connect-state"
	outlookConnectedPurpose = "entra/outlook-connected"
	outlookRefreshPurpose   = "entra/outlook-refresh"
	outlookSessionPurpose   = "entra/outlook-connect-session"
)

// The cookies' lifetimes, enforced on the Deps clock from the expiry sealed
// inside each, not only by the browser's Max-Age: ten minutes at Microsoft,
// five from the callback to complete (identity's figures).
const (
	outlookStateLifetime     = 10 * time.Minute
	outlookConnectedLifetime = 5 * time.Minute
)

// maxConnectedCookie is the longest connected cookie value set, in bytes: a
// browser drops a cookie past about 4 KB silently, which would read as a
// connect that expired for no reason. The refresh token is most of it.
const maxConnectedCookie = 3800

// maxReturnTo is the longest returnTo kept, in bytes.
const maxReturnTo = 512

// The Outlook connection's refusal codes (the contract's EntraProblem).
const (
	codeSwitchOff       = "switch_off"
	codeExpired         = "expired"
	codeNoAccount       = "no_account"
	codeAccountTaken    = "account_taken"
	codeAccountMismatch = "account_mismatch"
	codeFeatureUnknown  = "feature_unknown"
)

// The callback's outcome codes besides codeExpired (design M4, plan
// reading 12).
const (
	outlookAccessDenied   = "access_denied"
	outlookTenantMismatch = "tenant_mismatch"
	outlookConsentMissing = "consent_missing"
	outlookFailed         = "failed"
)

// The connection's states (00089).
const (
	outlookActive    = "active"
	outlookReconnect = "reconnect"
	outlookClosing   = "closing"
)

// Microsoft's authorization server, fixed like the module's other hosts.
const (
	// microsoftLogin is the authority every tenant's endpoints are under.
	microsoftLogin = "https://login.microsoftonline.com/"
	// outlookAuthorizeEndpoint is the authorization endpoint; %s is the
	// tenant id.
	outlookAuthorizeEndpoint = microsoftLogin + "%s/oauth2/v2.0/authorize"
	// graphResource is the prefix of a Graph scope asked by name.
	graphResource = "https://graph.microsoft.com/"
	// outlookBaseScope is what every connect asks besides its feature's:
	// the id_token, the person's name and the refresh token.
	outlookBaseScope = "openid profile offline_access"
	// outlookClaims declares the client capability cp1, so Microsoft sends
	// a continuous access evaluation claims challenge instead of waiting
	// for the access token to expire (design M5).
	outlookClaims = `{"access_token":{"xms_cc":{"values":["cp1"]}}}`
)

// outlookFeatures maps each feature a connect may name to the delegated
// Graph scopes it asks: a consumer names a feature, never the vendor's
// permission (design M4, M20).
var outlookFeatures = map[string][]string{
	"calendar": {"Calendars.ReadWrite"},
	"mail":     {"Mail.Send"},
}

// outlookScope is the scope a connect for feature asks, and its code's
// redemption names again.
func outlookScope(feature string) string {
	return outlookScopeWith(outlookFeatures[feature])
}

// outlookScopeWith is the scope string for Graph scopes, prefixed with
// outlookBaseScope.
func outlookScopeWith(scopes []string) string {
	parts := []string{outlookBaseScope}
	for _, s := range scopes {
		parts = append(parts, graphResource+s)
	}
	return strings.Join(parts, " ")
}

// normaliseScopes is a token answer's scope as stored: each scope with
// Graph's resource prefix dropped, lower-cased, sorted and compacted.
func normaliseScopes(scope string) []string {
	out := []string{}
	for _, s := range strings.Fields(scope) {
		s = strings.ToLower(s)
		s = strings.TrimPrefix(s, graphResource)
		out = append(out, s)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// hasScopes reports whether granted, normalised, holds every one of want.
func hasScopes(granted []string, want []string) bool {
	for _, w := range want {
		if !slices.Contains(granted, strings.ToLower(w)) {
			return false
		}
	}
	return true
}

// featuresOf is the features granted, normalised, allows, sorted.
func featuresOf(granted []string) []string {
	out := []string{}
	for feature, scopes := range outlookFeatures {
		if hasScopes(granted, scopes) {
			out = append(out, feature)
		}
	}
	slices.Sort(out)
	return out
}

// safeReturnTo is returnTo when it is a path on this installation, else
// the Connections page (design M4, plan reading 15): at most maxReturnTo
// bytes, a single leading slash, no backslash, no control character, and
// neither a scheme nor a host once parsed. The browser checks it again
// before it navigates.
func safeReturnTo(returnTo string) string {
	if returnTo == "" || len(returnTo) > maxReturnTo || returnTo[0] != '/' {
		return outlookPagePath
	}
	if len(returnTo) > 1 && (returnTo[1] == '/' || returnTo[1] == '\\') {
		return outlookPagePath
	}
	for i := 0; i < len(returnTo); i++ {
		if b := returnTo[i]; b < 0x20 || b == 0x7f || b == '\\' {
			return outlookPagePath
		}
	}
	u, err := url.Parse(returnTo)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return outlookPagePath
	}
	return returnTo
}

// connectedCookieFits reports whether a sealed connected cookie value is
// short enough for every browser to keep.
func connectedCookieFits(value string) bool {
	return len(value) <= maxConnectedCookie
}

// outlookRedirectURI is the redirect URI to register on the app
// registration's Web platform: APP_URL, the base path and the callback.
// Empty without a configuration.
func outlookRedirectURI(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.AppOrigin + cfg.BasePath + outlookCallbackPath
}

// basePath is the configuration's base path, empty without one.
func basePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.BasePath
}

// outlookStateBody is the state cookie's content: what the callback needs
// to check Microsoft's answer and carry it on to complete.
type outlookStateBody struct {
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

func (b outlookStateBody) expires() time.Time { return b.ExpiresAt }

// outlookConnectedBody is the connected cookie's content: the validated
// account, its granted scopes and refresh token, and the person and session
// the connect was started by.
type outlookConnectedBody struct {
	UserID       uuid.UUID `json:"userId"`
	SessionMAC   []byte    `json:"sessionMac"`
	OID          uuid.UUID `json:"oid"`
	TID          uuid.UUID `json:"tid"`
	UPN          string    `json:"upn"`
	Scopes       []string  `json:"scopes"`
	RefreshToken string    `json:"refreshToken"`
	Feature      string    `json:"feature"`
	ReturnTo     string    `json:"returnTo"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func (b outlookConnectedBody) expires() time.Time { return b.ExpiresAt }

// outlookCookie is one of the flow's cookies: identity's session cookie's
// HttpOnly and Secure policy, but SameSite=Lax, the flow's path and
// lifetime as Max-Age. Entra cannot reach identity's Access, so the fields
// are set here as identity sets them.
func (s *server) outlookCookie(name, value string, lifetime time.Duration) *http.Cookie {
	secure := s.deps.Config != nil && s.deps.Config.UsesHTTPS()
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     basePath(s.deps.Config) + outlookCookiePath,
		MaxAge:   int(lifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	}
}

// setOutlookCookie adds c to the answer through the response writer the
// router attached to ctx, so a cookie is set or cleared whatever the
// answer, a 500 included.
func setOutlookCookie(ctx context.Context, c *http.Cookie) error {
	w, ok := contracts.ResponseWriterFrom(ctx)
	if !ok {
		return errors.New("entra: no response writer to set a cookie on")
	}
	http.SetCookie(w, c)
	return nil
}

// clearOutlookCookie tells the browser to drop one of the flow's cookies:
// each is single use, spent on every answer of the step that reads it.
func (s *server) clearOutlookCookie(ctx context.Context, name string) {
	c := s.outlookCookie(name, "", 0)
	c.MaxAge = -1
	_ = setOutlookCookie(ctx, c)
}

// sealOutlook seals v's JSON under purpose, as cookie text.
func (s *server) sealOutlook(purpose string, v any) (string, error) {
	if s.deps.Secrets == nil {
		return "", errors.New("entra: no secrets box to seal the Outlook connect with")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("entra: seal the Outlook connect: %w", err)
	}
	sealed, err := s.deps.Secrets.SealString(purpose, string(b))
	if err != nil {
		return "", fmt.Errorf("entra: seal the Outlook connect: %w", err)
	}
	return sealed, nil
}

// openOutlookCookie opens the request's cookie name, sealed under purpose,
// into v, and reports whether it is there, authentic and unexpired on the
// Deps clock. Why it is not says nothing a caller could act on differently.
func (s *server) openOutlookCookie(ctx context.Context, name, purpose string, v interface{ expires() time.Time }) bool {
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
	return s.client.now().Before(v.expires())
}

// sessionMAC is the MAC binding a connect to the session that started it.
func (s *server) sessionMAC(sessionID uuid.UUID) ([]byte, error) {
	if s.deps.Secrets == nil {
		return nil, errors.New("entra: no secrets box to bind the Outlook connect with")
	}
	mac, err := s.deps.Secrets.MAC(outlookSessionPurpose, sessionID[:])
	if err != nil {
		return nil, fmt.Errorf("entra: bind the Outlook connect to the session: %w", err)
	}
	return mac, nil
}

// randomOutlookValue is a fresh state or nonce: 256 random bits, base64url.
func randomOutlookValue() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return base64.RawURLEncoding.EncodeToString(b)
}

// requestPrincipal is the signed-in caller of a session operation, with
// the session it came with.
func requestPrincipal(ctx context.Context) (contracts.Principal, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return contracts.Principal{}, fmt.Errorf("entra: an Outlook operation without a signed-in user: %w", contracts.ErrInvalidActor)
	}
	return p, nil
}

// certainAccount is the caller's certain account (design M6): Staff's
// directory account for the principal in ctx, from this provider and
// enabled, whose object id the module keeps a trusted, enabled row for.
// Nil when there is none. It calls out of the module, so never inside one
// of its transactions.
func (s *server) certainAccount(ctx context.Context) (*store.EntraAccount, error) {
	da, err := s.workforceDirectoryAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the directory account: %w", err)
	}
	if da == nil || da.Provider != providerKey || !da.Enabled {
		return nil, nil
	}
	oid, ok := objectID(da.ExternalID)
	if !ok {
		return nil, nil
	}
	row, err := s.q.GetAccount(ctx, oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entra: read the account: %w", err)
	}
	if !trusted(row) || row.State != stateEnabled {
		return nil, nil
	}
	return &row, nil
}

// outlookConnectionBody is the caller's connection as the contract answers
// it: whether they may connect and why not, the account Vantigo knows them
// by, and their row (nil for none).
func outlookConnectionBody(conn store.EntraConnection, account *store.EntraAccount, row *store.EntraOutlookConnection) gen.EntraOutlookConnection {
	body := gen.EntraOutlookConnection{
		Offered:  conn.OutlookConnect && account != nil,
		State:    gen.EntraOutlookConnectionStateNotConnected,
		Scopes:   []string{},
		Features: []string{},
	}
	switch {
	case !conn.OutlookConnect:
		reason := gen.SwitchOff
		body.Reason = &reason
	case account == nil:
		reason := gen.NoAccount
		body.Reason = &reason
	}
	if account != nil {
		upn := account.UserPrincipalName
		body.Account = &upn
	}
	if row != nil {
		body.State = gen.EntraOutlookConnectionState(row.State)
		body.Scopes = append([]string{}, row.Scopes...)
		body.Features = featuresOf(row.Scopes)
		connectedAt := row.ConnectedAt
		body.ConnectedAt = &connectedAt
		if row.State == outlookReconnect {
			body.ErrorCode = row.ErrorCode
		}
	}
	return body
}

// callerConnection is the caller's row: their live one, or else a closing
// one of their certain account's that is still theirs. Nil for none.
func (s *server) callerConnection(ctx context.Context, userID uuid.UUID, account *store.EntraAccount) (*store.EntraOutlookConnection, error) {
	row, err := s.q.GetLiveOutlookConnectionByUser(ctx, userID)
	if err == nil {
		return &row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("entra: read the Outlook connection: %w", err)
	}
	if account == nil {
		return nil, nil
	}
	row, err = s.q.GetOutlookConnection(ctx, account.ObjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("entra: read the Outlook connection: %w", err)
	}
	if row.UserID != userID {
		return nil, nil
	}
	return &row, nil
}
