package identity

import (
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the revocation endpoint, POST /api/oauth/revoke (OAuth
// connections design C18, A23; RFC 7009): a client ends its own connection
// by presenting either of its tokens with its client_id. It is anonymous,
// reads no cookie and is mounted, as the token endpoint is, ahead of the
// cross-origin guard. An access token revokes its connection only until it
// expires; a refresh token revokes its connection whether it is live, spent,
// superseded or expired, since whoever holds any of a family's refresh tokens
// may end it (A25). A token that is unknown, malformed, an expired access
// token, of another kind (an API key, a registered client_id) or of another
// client's connection is answered 200 and nothing is revoked, so the
// endpoint tells no one whether a token exists.

// policyOAuthRevoke limits the endpoint per client address, as the token
// endpoint is limited (A23).
var policyOAuthRevoke = ratelimit.Policy{Name: "OAuthRevoke", Limit: 120, Window: time.Minute}

// ServeOAuthRevoke is contracts.OAuthEndpoints: the revocation endpoint.
func (a *Access) ServeOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if a.cfg == nil || !a.cfg.OAuthEnabled {
		httpx.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeTokenError(w, tokenError{http.StatusMethodNotAllowed, "invalid_request", "The revocation endpoint takes POST only."})
		return
	}
	if a.limiter != nil {
		d, err := a.limiter.Hit(r.Context(), policyOAuthRevoke, httpx.ClientIP(r))
		if err != nil {
			if a.log != nil {
				a.log.ErrorContext(r.Context(), "identity: the revocation endpoint's limit could not be counted", "error", err)
			}
			writeTokenError(w, tokenError{http.StatusInternalServerError, "server_error", "The request could not be handled."})
			return
		}
		if !d.Allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(d.RetryAfter.Seconds())), 1)))
			writeTokenError(w, tokenError{http.StatusTooManyRequests, "invalid_request", "Too many revocation requests from this address; try again shortly."})
			return
		}
	}
	err := a.revokeRequest(r)
	var te tokenError
	switch {
	case errors.As(err, &te):
		writeTokenError(w, te)
	case err != nil:
		if a.log != nil {
			a.log.ErrorContext(r.Context(), "identity: the revocation endpoint failed", "error", err)
		}
		writeTokenError(w, tokenError{http.StatusInternalServerError, "server_error", "The request could not be handled."})
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.WriteHeader(http.StatusOK)
	}
}

// revokeRequest reads one revocation request and revokes the connection its
// token belongs to when the client_id is that connection's (A23).
func (a *Access) revokeRequest(r *http.Request) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return invalidRequest("The body must be application/x-www-form-urlencoded.")
	}
	if r.Header.Get("Authorization") != "" {
		return tokenError{http.StatusUnauthorized, "invalid_client", "Clients authenticate with their client_id alone, not with Authorization."}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, oauthTokenMaxBody+1))
	if err != nil {
		return invalidRequest("The body could not be read.")
	}
	if len(body) > oauthTokenMaxBody {
		return invalidRequest("The body is larger than 16 KiB.")
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return invalidRequest("The body is not valid form encoding.")
	}
	for _, v := range values {
		if len(v) > 1 {
			return invalidRequest("A parameter is repeated.")
		}
		if len(v[0]) > maxTokenParamLength {
			return invalidRequest("A parameter is too long.")
		}
	}
	if values.Has("client_secret") || values.Has("client_assertion") || values.Has("client_assertion_type") {
		return tokenError{http.StatusUnauthorized, "invalid_client", "Clients authenticate with their client_id alone, not with a secret or an assertion."}
	}
	token, clientID := values.Get("token"), values.Get("client_id")
	if token == "" || clientID == "" {
		return invalidRequest("token and client_id are required.")
	}
	// token_type_hint is a hint (RFC 7009 2.1): the prefix says which kind a
	// token is, and a hint naming a kind is never needed.
	var hash []byte
	var kind string
	switch {
	case strings.HasPrefix(token, accessTokenPrefix):
		hash, kind = parsedOr(parsePrefixedToken(accessTokenPrefix, token)), tokenKindAccess
	case strings.HasPrefix(token, refreshTokenPrefix):
		hash, kind = parsedOr(parsePrefixedToken(refreshTokenPrefix, token)), tokenKindRefresh
	}
	if hash == nil {
		return nil // not a token of a connection: nothing to revoke
	}
	ctx := r.Context()
	connectionID, err := a.q.GetOAuthTokenConnection(ctx, store.GetOAuthTokenConnectionParams{TokenHash: hash, Kind: kind, Now: a.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: read the token: %w", err)
	}
	return db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.LockOAuthConnection(ctx, connectionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: read the connection: %w", err)
		}
		if c.ClientID != clientID || c.RevokedAt != nil {
			return nil
		}
		return a.revokeConnection(ctx, q, r, c.ID, nil, oauthSystem, revokeReasonRevokedByClient, a.now())
	})
}

// parsedOr is a parsed token's hash, nil when it did not parse.
func parsedOr(hash []byte, ok bool) []byte {
	if !ok {
		return nil
	}
	return hash
}
