package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the token endpoint, POST /api/oauth/token (OAuth connections
// design C15-C17, A9-A11): a public client exchanges its code for an access
// and a refresh token, and renews them with the refresh token. It is
// anonymous and reads no cookie; every answer is RFC 6749's JSON with
// Cache-Control: no-store, never a problem document.

const (
	// oauthTokenMaxBody caps a token request's body (C15).
	oauthTokenMaxBody = 16 << 10
	// The lifetimes of C16 and C17.
	accessTokenLifetime  = time.Hour
	refreshTokenLifetime = 30 * 24 * time.Hour
	// refreshGrace is how long a spent refresh token is honoured once more
	// (A11), for a client that lost the answer.
	refreshGrace = 60 * time.Second
	// oauthSystem is the actor of the revocations the endpoint makes itself.
	oauthSystem = "system:oauth"
)

// The token kinds oauth_tokens holds.
const (
	tokenKindAccess  = "access"
	tokenKindRefresh = "refresh"
)

// The reasons a connection ends with (C18).
const (
	revokeReasonRevoked             = "revoked"
	revokeReasonIntegrationArchived = "integration_archived"
	revokeReasonAuthorizerRemoved   = "authorizer_removed"
	revokeReasonCodeReplayed        = "code_replayed"
	revokeReasonRefreshReused       = "refresh_reused"
)

// The audit actions of a connection (C14, C18).
const (
	auditConnectionAuthorized = "connection.authorized"
	auditConnectionRevoked    = "connection.revoked"
)

// policyOAuthToken limits the endpoint per client address (C15).
var policyOAuthToken = ratelimit.Policy{Name: "OAuthToken", Limit: 120, Window: time.Minute}

// codeVerifierPattern is a PKCE verifier (A9).
var codeVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// The bounds of the token request's other parameters (A9).
const (
	maxTokenParamLength = 1024
)

// tokenError is an RFC 6749 error answer.
type tokenError struct {
	status      int
	code        string
	description string
}

func (e tokenError) Error() string { return e.code }

func invalidRequest(description string) tokenError {
	return tokenError{http.StatusBadRequest, "invalid_request", description}
}

func invalidGrant(description string) tokenError {
	return tokenError{http.StatusBadRequest, "invalid_grant", description}
}

func writeTokenJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeTokenError(w http.ResponseWriter, e tokenError) {
	if e.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="vantigo"`)
	}
	writeTokenJSON(w, e.status, map[string]string{"error": e.code, "error_description": e.description})
}

// tokenResponse is the answer that issues tokens (C16).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// ServeOAuthToken is contracts.OAuthEndpoints: the token endpoint.
func (a *Access) ServeOAuthToken(w http.ResponseWriter, r *http.Request) {
	if a.cfg == nil || !a.cfg.OAuthEnabled {
		httpx.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTokenError(w, tokenError{http.StatusMethodNotAllowed, "invalid_request", "The token endpoint takes POST only."})
		return
	}
	if a.limiter != nil {
		d, err := a.limiter.Hit(r.Context(), policyOAuthToken, httpx.ClientIP(r))
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !d.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(d.RetryAfter.Seconds())), 1)))
			writeTokenError(w, tokenError{http.StatusTooManyRequests, "invalid_request", "Too many token requests from this address; try again shortly."})
			return
		}
	}
	resp, err := a.tokenRequest(r)
	var te tokenError
	switch {
	case errors.As(err, &te):
		writeTokenError(w, te)
	case err != nil:
		if a.log != nil {
			a.log.ErrorContext(r.Context(), "identity: the token endpoint failed", "error", err)
		}
		writeTokenError(w, tokenError{http.StatusInternalServerError, "server_error", "The request could not be handled."})
	default:
		writeTokenJSON(w, http.StatusOK, resp)
	}
}

// tokenRequest reads and answers one token request.
func (a *Access) tokenRequest(r *http.Request) (tokenResponse, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return tokenResponse{}, invalidRequest("The body must be application/x-www-form-urlencoded.")
	}
	// A client that authenticates is not a public client (C15, A1).
	if r.Header.Get("Authorization") != "" {
		return tokenResponse{}, tokenError{http.StatusUnauthorized, "invalid_client", "Clients authenticate with PKCE alone, not with Authorization."}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, oauthTokenMaxBody+1))
	if err != nil {
		return tokenResponse{}, invalidRequest("The body could not be read.")
	}
	if len(body) > oauthTokenMaxBody {
		return tokenResponse{}, invalidRequest("The body is larger than 16 KiB.")
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return tokenResponse{}, invalidRequest("The body is not valid form encoding.")
	}
	for name, v := range values {
		if len(v) > 1 {
			return tokenResponse{}, invalidRequest("The parameter " + name + " is repeated.")
		}
		if len(v[0]) > maxTokenParamLength {
			return tokenResponse{}, invalidRequest("The parameter " + name + " is too long.")
		}
	}
	if values.Has("client_secret") || values.Has("client_assertion") || values.Has("client_assertion_type") {
		return tokenResponse{}, tokenError{http.StatusUnauthorized, "invalid_client", "Clients authenticate with PKCE alone, not with a secret or an assertion."}
	}
	if values.Has("resource") && !a.resourceMatches(values.Get("resource")) {
		return tokenResponse{}, tokenError{http.StatusBadRequest, "invalid_target", "The resource is not this installation's MCP endpoint."}
	}
	switch values.Get("grant_type") {
	case "authorization_code":
		return a.exchangeCode(r, values)
	case "refresh_token":
		return a.refresh(r, values)
	case "":
		return tokenResponse{}, invalidRequest("grant_type is required.")
	default:
		return tokenResponse{}, tokenError{http.StatusBadRequest, "unsupported_grant_type", "The grant types are authorization_code and refresh_token."}
	}
}

// pkceMatches reports whether BASE64URL(SHA256(verifier)) is the challenge,
// compared in constant time (C15).
func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// exchangeCode is grant_type=authorization_code (C15): the code consumed
// atomically, its connection's client_id and redirect_uri, the PKCE
// verifier; then the connection is activated and tokens issued. A code
// presented a second time revokes its connection (code_replayed).
func (a *Access) exchangeCode(r *http.Request, values url.Values) (tokenResponse, error) {
	code, clientID, redirectURI, verifier := values.Get("code"), values.Get("client_id"), values.Get("redirect_uri"), values.Get("code_verifier")
	if code == "" || clientID == "" || redirectURI == "" || verifier == "" {
		return tokenResponse{}, invalidRequest("code, client_id, redirect_uri and code_verifier are required.")
	}
	if !codeVerifierPattern.MatchString(verifier) {
		return tokenResponse{}, invalidRequest("The code_verifier is 43 to 128 characters of [A-Za-z0-9._~-].")
	}
	hash, ok := parseToken(code)
	if !ok {
		return tokenResponse{}, invalidGrant("The code is invalid, expired or used.")
	}
	ctx := r.Context()
	var out tokenResponse
	var refusal error
	err := db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := a.now()
		consumed, err := q.ConsumeOAuthCode(ctx, store.ConsumeOAuthCodeParams{CodeHash: hash, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			prior, perr := q.GetOAuthCode(ctx, hash)
			if perr == nil && prior.ConsumedAt != nil {
				// A replay: whoever holds the code is not to be trusted, nor
				// the tokens the first exchange issued (C15).
				if err := a.revokeConnection(ctx, q, r, prior.ConnectionID, nil, oauthSystem, revokeReasonCodeReplayed, now); err != nil {
					return err
				}
			} else if perr != nil && !errors.Is(perr, pgx.ErrNoRows) {
				return fmt.Errorf("identity: read the code: %w", perr)
			}
			refusal = invalidGrant("The code is invalid, expired or used.")
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: consume the code: %w", err)
		}
		c, err := q.LockOAuthConnection(ctx, consumed.ConnectionID)
		if err != nil {
			return fmt.Errorf("identity: read the connection: %w", err)
		}
		if c.ClientID != clientID || c.RedirectUri != redirectURI || !pkceMatches(verifier, consumed.CodeChallenge) ||
			c.RevokedAt != nil || c.ActivatedAt != nil || !c.ExpiresAt.After(now) {
			// The code is spent either way: a wrong verifier does not get a
			// second try.
			refusal = invalidGrant("The code does not match the client, the redirect_uri or the code_verifier.")
			return nil
		}
		if err := q.ActivateOAuthConnection(ctx, store.ActivateOAuthConnectionParams{ID: c.ID, Now: now}); err != nil {
			return fmt.Errorf("identity: activate the connection: %w", err)
		}
		integration := c.IntegrationID
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor: c.AuthorizedBy, targetUser: &integration, action: auditConnectionAuthorized,
			before: struct{}{}, after: connectionSnapshot(c),
		}); err != nil {
			return err
		}
		out, err = issueTokens(ctx, q, c, nil, now)
		return err
	})
	if err != nil {
		return tokenResponse{}, err
	}
	if refusal != nil {
		return tokenResponse{}, refusal
	}
	return out, nil
}

// refresh is grant_type=refresh_token (C17, A11): the refresh token spent
// and a new pair issued. A spent token presented within 60 seconds is
// honoured once more; one superseded by that is invalid_grant; one spent and
// presented after the 60 seconds revokes the connection (refresh_reused).
func (a *Access) refresh(r *http.Request, values url.Values) (tokenResponse, error) {
	raw, clientID := values.Get("refresh_token"), values.Get("client_id")
	if raw == "" || clientID == "" {
		return tokenResponse{}, invalidRequest("refresh_token and client_id are required.")
	}
	hash, ok := parsePrefixedToken(refreshTokenPrefix, raw)
	if !ok {
		return tokenResponse{}, invalidGrant("The refresh token is invalid, expired or revoked.")
	}
	var askedWrite *bool
	if values.Has("scope") {
		write, ok := parseScopes(values.Get("scope"))
		if !ok {
			return tokenResponse{}, tokenError{http.StatusBadRequest, "invalid_scope", "The scopes are read, write and offline_access."}
		}
		askedWrite = &write
	}
	ctx := r.Context()
	var out tokenResponse
	var refusal error
	dead := invalidGrant("The refresh token is invalid, expired or revoked.")
	err := db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := a.now()
		t, err := q.LockOAuthRefreshToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = dead
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: read the refresh token: %w", err)
		}
		c, err := q.LockOAuthConnection(ctx, t.ConnectionID)
		if err != nil {
			return fmt.Errorf("identity: read the connection: %w", err)
		}
		if c.ClientID != clientID || c.RevokedAt != nil || c.ActivatedAt == nil || !c.ExpiresAt.After(now) || !t.ExpiresAt.After(now) {
			refusal = dead
			return nil
		}
		if askedWrite != nil && *askedWrite && !slices.Contains(c.Scopes, oauthScopeWrite) {
			refusal = tokenError{http.StatusBadRequest, "invalid_scope", "The scope asked is more than the connection holds."}
			return nil
		}
		if t.UsedAt != nil {
			children, err := q.CountOAuthTokenChildren(ctx, hash)
			if err != nil {
				return fmt.Errorf("identity: read the refresh token: %w", err)
			}
			switch {
			case children == 0:
				// Superseded by a retry's pair, never spent itself.
				refusal = dead
				return nil
			case now.Sub(*t.UsedAt) > refreshGrace:
				if err := a.revokeConnection(ctx, q, r, c.ID, nil, oauthSystem, revokeReasonRefreshReused, now); err != nil {
					return err
				}
				refusal = dead
				return nil
			case children > 1:
				// Honoured once more already.
				refusal = dead
				return nil
			}
		}
		// The token is spent, and whatever the connection holds unspent
		// beside it is superseded: a connection never has two live ones.
		if err := q.SpendOAuthRefreshTokens(ctx, store.SpendOAuthRefreshTokensParams{ConnectionID: c.ID, Now: now}); err != nil {
			return fmt.Errorf("identity: spend the refresh tokens: %w", err)
		}
		out, err = issueTokens(ctx, q, c, hash, now)
		return err
	})
	if err != nil {
		return tokenResponse{}, err
	}
	if refusal != nil {
		return tokenResponse{}, refusal
	}
	return out, nil
}

// issueTokens mints an access and a refresh token for c on q (C16), each
// expiring at the earlier of its own lifetime and the connection's end
// (A11), the refresh token issued from parent when there is one.
func issueTokens(ctx context.Context, q *store.Queries, c store.IdentityOauthConnection, parent []byte, now time.Time) (tokenResponse, error) {
	access, accessHash := newPrefixedToken(accessTokenPrefix)
	refreshTok, refreshHash := newPrefixedToken(refreshTokenPrefix)
	accessUntil := earliest(now.Add(accessTokenLifetime), c.ExpiresAt)
	refreshUntil := earliest(now.Add(refreshTokenLifetime), c.ExpiresAt)
	if err := q.InsertOAuthToken(ctx, store.InsertOAuthTokenParams{
		TokenHash: accessHash, ConnectionID: c.ID, Kind: tokenKindAccess, Now: now, ExpiresAt: accessUntil,
	}); err != nil {
		return tokenResponse{}, fmt.Errorf("identity: issue the access token: %w", err)
	}
	if err := q.InsertOAuthToken(ctx, store.InsertOAuthTokenParams{
		TokenHash: refreshHash, ConnectionID: c.ID, Kind: tokenKindRefresh, ParentHash: parent, Now: now, ExpiresAt: refreshUntil,
	}); err != nil {
		return tokenResponse{}, fmt.Errorf("identity: issue the refresh token: %w", err)
	}
	return tokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: max(int(accessUntil.Sub(now).Seconds()), 0),
		RefreshToken: refreshTok, Scope: strings.Join(c.Scopes, " "),
	}, nil
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// connectionAudit is a connection as the audit trail records it: never a
// token, a code or a verifier.
type connectionAudit struct {
	ConnectionID  uuid.UUID  `json:"connectionId"`
	IntegrationID uuid.UUID  `json:"integrationId"`
	ClientID      string     `json:"clientId"`
	ClientName    string     `json:"clientName"`
	ClientHost    string     `json:"clientHost"`
	Scopes        []string   `json:"scopes"`
	AuthorizedBy  *uuid.UUID `json:"authorizedBy"`
	ExpiresAt     time.Time  `json:"expiresAt"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	RevokeReason  *string    `json:"revokeReason,omitempty"`
}

func connectionSnapshot(c store.IdentityOauthConnection) connectionAudit {
	return connectionAudit{
		ConnectionID: c.ID, IntegrationID: c.IntegrationID, ClientID: c.ClientID, ClientName: c.ClientName,
		ClientHost: c.ClientHost, Scopes: nonNil(c.Scopes), AuthorizedBy: c.AuthorizedBy, ExpiresAt: c.ExpiresAt,
		RevokedAt: c.RevokedAt, RevokeReason: c.RevokeReason,
	}
}

// revokeConnection ends one live connection on q with reason, audited
// connection.revoked by actor or by the system named; one ended already is
// left as it is.
func (a *Access) revokeConnection(ctx context.Context, q *store.Queries, r *http.Request, id uuid.UUID, actor *uuid.UUID, system, reason string, now time.Time) error {
	revoked, err := q.RevokeOAuthConnection(ctx, store.RevokeOAuthConnectionParams{ID: id, Now: now, RevokedBy: actor, Reason: reason})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: revoke the connection: %w", err)
	}
	return auditRevoked(ctx, q, r, revoked, actor, system, false, now)
}

// auditRevoked records connection.revoked for a connection just ended.
func auditRevoked(ctx context.Context, q *store.Queries, r *http.Request, revoked store.IdentityOauthConnection, actor *uuid.UUID, system string, mfa bool, now time.Time) error {
	before := revoked
	before.RevokedAt, before.RevokedBy, before.RevokeReason = nil, nil, nil
	integration := revoked.IntegrationID
	return writeAudit(ctx, q, r, now, auditEvent{
		actor: actor, system: system, targetUser: &integration, action: auditConnectionRevoked,
		before: connectionSnapshot(before), after: connectionSnapshot(revoked), mfa: mfa,
	})
}

var _ contracts.OAuthEndpoints = (*Access)(nil)
