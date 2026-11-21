package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is how Access answers a request that presents an OAuth access
// token (OAuth connections design C19-C21, A15-A17): Authorization: Bearer
// vta_…, judged as the token's connection's alone, never by a session cookie
// the request also carries. A connection presents its integration exactly as
// a key does, so the key's pipeline runs, with the connection's state where
// the key's would be read (A15's mapping).

// The prefixes of the two tokens a connection holds (C16).
const (
	accessTokenPrefix  = "vta_"
	refreshTokenPrefix = "vtr_"
)

// The scopes a connection may hold (C3): write includes read.
const (
	oauthScopeRead    = "read"
	oauthScopeWrite   = "write"
	oauthScopeOffline = "offline_access"
)

// The words of a token's refusals (C21).
const (
	tokenInvalidMessage      = "The access token is invalid, has expired or has been revoked."
	insufficientScopeMessage = "This connection may only read: it was approved for Read only."
)

// bearerWithPrefix returns the token of an Authorization: Bearer header when
// it starts with prefix; the scheme is matched without regard to case.
func bearerWithPrefix(header, prefix string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, apiKeyAuthScheme) {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, strings.HasPrefix(token, prefix)
}

// isConnectionRequest reports whether r presents an access token in any of
// its Authorization headers: its refusal is the token's, never the session's.
func isConnectionRequest(r *http.Request) bool {
	return slices.ContainsFunc(r.Header.Values("Authorization"), func(v string) bool {
		_, ok := bearerWithPrefix(v, accessTokenPrefix)
		return ok
	})
}

// checkConnection is Check for a request whose one Authorization header is
// Bearer vta_… (C19). With OAuth switched off it is no caller at all (C24).
// It refuses, in order: a token that does not parse, is unknown or is not an
// access token (ErrInvalidToken, unattributed); one whose integration's user
// is not of kind integration; then, attributed to the connection, one
// revoked, never activated, whose integration is archived or disabled, whose
// token or connection has expired, or whose authorizer no longer qualifies
// (A17) — each ErrInvalidToken; an operation marked x-vantigo-api: never (403
// api_not_available); and the rule by its kind as for a key, with no
// narrowing. A connection without the write scope is a read-only key: an
// operation that is not a read is 403 insufficient_scope when the scope is
// the only reason, and the plain 403 forbidden when the roles would refuse
// it too (A16). Only an admitted call records the connection's use.
func (a *Access) checkConnection(ctx context.Context, r *http.Request, rule contracts.Rule, raw string, now time.Time) (contracts.Principal, error) {
	if a.cfg == nil || !a.cfg.OAuthEnabled {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	hash, ok := parsePrefixedToken(accessTokenPrefix, raw)
	if !ok {
		return contracts.Principal{}, contracts.ErrInvalidToken
	}
	sso, err := a.sso.get(ctx)
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve access token: %w", err)
	}
	c, err := a.q.GetOAuthAccessToken(ctx, store.GetOAuthAccessTokenParams{TokenHash: hash, ScimEnabled: sso.SCIM != nil})
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Principal{}, contracts.ErrInvalidToken
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve access token: %w", err)
	}
	if c.Kind != contracts.UserKindIntegration {
		return contracts.Principal{}, contracts.ErrInvalidToken
	}
	refuse := func(reason string, err error) (contracts.Principal, error) {
		return contracts.Principal{}, &contracts.APIKeyRefusal{ConnectionID: c.ConnectionID, IntegrationID: c.IntegrationID, Reason: reason, Err: err}
	}
	switch {
	case c.RevokedAt != nil:
		return refuse(apiKeyRevoked, contracts.ErrInvalidToken)
	case c.ActivatedAt == nil:
		return refuse(contracts.APIReasonTokenInvalid, contracts.ErrInvalidToken)
	case c.ArchivedAt != nil:
		return refuse(apiKeyArchived, contracts.ErrInvalidToken)
	case c.IsDisabled || isLockedOut(c.LockoutEnd, now):
		return refuse(apiKeyDisabled, contracts.ErrInvalidToken)
	case !c.TokenExpiresAt.After(now) || !c.ConnectionExpiresAt.After(now):
		return refuse(apiKeyExpired, contracts.ErrInvalidToken)
	case !c.AuthorizerActive:
		return refuse(contracts.APIReasonTokenInvalid, contracts.ErrInvalidToken)
	}

	if rule.API == contracts.APIRefused {
		return refuse(apiKeyNotAvailable, contracts.ErrAPINotAvailable)
	}
	readOnly := !slices.Contains(c.Scopes, oauthScopeWrite)
	scopeRefuses := readOnly && rule.API != contracts.APIRead
	if slices.Contains(c.RoleNames, RoleOwner) || slices.Contains(c.RoleNames, RoleSystemAdmin) {
		return refuse(apiKeyForbidden, contracts.ErrForbidden)
	}
	roles := orderRoles(c.RoleNames)
	p := contracts.Principal{
		UserID: c.IntegrationID,
		Roles:  roles,
		Via:    &contracts.Via{Kind: contracts.ViaAPIKey, ConnectionID: c.ConnectionID, RateLimitPerMinute: int(c.RateLimitPerMinute)},
	}
	switch rule.Kind {
	case contracts.RuleAnonymous:
	case contracts.RulePermission:
		sub := subject{userID: c.IntegrationID, roles: roles, disabled: c.IsDisabled, lockoutEnd: c.LockoutEnd, apiKey: true}
		p, err = a.evaluate(ctx, p, sub, rule, now)
		if errors.Is(err, contracts.ErrForbidden) {
			return refuse(apiKeyForbidden, err)
		}
		if err != nil {
			return p, err
		}
	default:
		return refuse(apiKeyForbidden, contracts.ErrForbidden)
	}
	// The scope is the only reason left (A16): the roles would admit the
	// call, so a client that asks again for write can succeed.
	if scopeRefuses {
		return refuse(contracts.APIReasonInsufficientScope, contracts.ErrInsufficientScope)
	}

	if c.LastUsedAt == nil || now.Sub(*c.LastUsedAt) >= time.Minute {
		var ipp *string
		if ip := httpx.ClientIP(r); ip != "" {
			ipp = &ip
		}
		if err := a.q.TouchOAuthConnection(ctx, store.TouchOAuthConnectionParams{ID: c.ConnectionID, Now: now, Ip: ipp}); err != nil && a.log != nil {
			a.log.WarnContext(ctx, "identity: could not record a connection's use", "connection", c.ConnectionID, "error", err)
		}
	}
	return p, nil
}

// bearerChallenge is C21's WWW-Authenticate for an OAuth refusal, naming the
// protected resource's metadata.
func (a *Access) bearerChallenge(params string) string {
	return `Bearer ` + params + `, resource_metadata="` + a.cfg.OAuthResourceMetadata + `"`
}

// rejectConnection writes C21's answer to a refused access token: 401
// token_invalid with Bearer error="invalid_token", 403 insufficient_scope
// with Bearer error="insufficient_scope" when the scope alone refused it, and
// a key's 403 for anything else (A16). It never touches the session cookie a
// browser may also hold. With OAuth switched off a token is answered as an
// unknown key is.
func (a *Access) rejectConnection(w http.ResponseWriter, err error) {
	if a.cfg == nil || !a.cfg.OAuthEnabled {
		rejectAPIKey(w, contracts.ErrUnauthenticated)
		return
	}
	switch {
	case errors.Is(err, contracts.ErrInsufficientScope):
		w.Header().Set("WWW-Authenticate", a.bearerChallenge(`error="insufficient_scope", scope="read write"`))
		authError(w, http.StatusForbidden, "insufficient_scope", insufficientScopeMessage, nil)
	case errors.Is(err, contracts.ErrForbidden):
		rejectAPIKey(w, err)
	default:
		w.Header().Set("WWW-Authenticate", a.bearerChallenge(`error="invalid_token"`))
		authError(w, http.StatusUnauthorized, "token_invalid", tokenInvalidMessage, nil)
	}
}

// connectionMCP is MCP for a connection's principal (A15): the integration's
// MCP setting, read-only unless the connection holds write, and the
// integration's effective permissions less APINever, with no narrowing.
func (a *Access) connectionMCP(ctx context.Context, p contracts.Principal, scope bool) (contracts.MCPKeyAccess, error) {
	c, err := a.q.GetOAuthConnectionScope(ctx, p.Via.ConnectionID)
	if err != nil {
		return contracts.MCPKeyAccess{}, fmt.Errorf("identity: read the connection's scope: %w", err)
	}
	readOnly := !slices.Contains(c.Scopes, oauthScopeWrite)
	if !scope {
		return contracts.MCPKeyAccess{Mode: c.McpAccess, ReadOnly: readOnly}, nil
	}
	held, err := a.q.EffectivePermissionsForUser(ctx, c.IntegrationID)
	if err != nil {
		return contracts.MCPKeyAccess{}, fmt.Errorf("identity: read the connection's scope: %w", err)
	}
	perms := make([]string, 0, len(held))
	for _, key := range held {
		if !a.apiNever(key) {
			perms = append(perms, key)
		}
	}
	return contracts.MCPKeyAccess{Mode: c.McpAccess, Permissions: perms, ReadOnly: readOnly}, nil
}
