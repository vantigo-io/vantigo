package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is how Access answers a request that presents an integration's
// API key (API access design K9-K19): Authorization: Bearer vtg_…, judged as
// the key's alone, never by a session cookie the request also carries.

// apiKeyAuthScheme is the Authorization scheme a key is presented under.
const apiKeyAuthScheme = "Bearer"

// The reasons an APIKeyRefusal carries.
const (
	apiKeyRevoked      = contracts.APIReasonRevoked
	apiKeyDisabled     = contracts.APIReasonDisabled
	apiKeyArchived     = contracts.APIReasonArchived
	apiKeyExpired      = contracts.APIReasonExpired
	apiKeyNetwork      = contracts.APIReasonNetwork
	apiKeyNotAvailable = contracts.APIReasonNever
	apiKeyReadOnly     = contracts.APIReasonReadOnly
	apiKeyForbidden    = contracts.APIReasonForbidden
)

// The words of K19's answers.
const (
	apiNotAvailableMessage = "This operation is not available to API keys."
	apiKeyReadOnlyMessage  = "This API key may only read."
	apiKeyExpiredMessage   = "This API key has expired."
)

// apiKeyFrom returns the key of an Authorization: Bearer vtg_… header; the
// scheme is matched without regard to case. A bearer token without the prefix
// is no key, and the request is judged as it would be without one.
func apiKeyFrom(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, apiKeyAuthScheme) {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, strings.HasPrefix(token, apiKeyPrefix)
}

// isAPIKeyRequest reports whether r presents an API key in any of its
// Authorization headers: its refusal is the key's, never the session's.
func isAPIKeyRequest(r *http.Request) bool {
	return slices.ContainsFunc(r.Header.Values("Authorization"), func(v string) bool {
		_, ok := apiKeyFrom(v)
		return ok
	})
}

// checkAPIKey is Check for a request whose one Authorization header is
// Bearer vtg_… (K16, K17). It refuses, in order: a key that does not parse or
// is unknown (ErrUnauthenticated); one whose integration's user is not of kind
// integration (ErrUnauthenticated); one presented from outside its networks
// (401 unauthenticated, reason network, whatever else is true of the key); one
// revoked, whose integration is archived or disabled (401 unauthenticated);
// one expired (401 api_key_expired); an operation marked x-vantigo-api: never (403
// api_not_available); an operation that is not a read, for a read-only key
// (403 api_key_read_only); and then the rule by its kind (K9): a session or a
// policy rule is 403 forbidden, an anonymous one admits the integration, and a
// permission rule is evaluated for the integration's user, each key held
// through its roles, none in contracts.APINever (K7, K51), and every one in the
// key's narrowing when it has one. Every refusal of a key identity found is a
// contracts.APIKeyRefusal. Only an admitted call records the key's use.
// Nothing resolved is cached, so a revocation holds at the very next request
// on every replica.
func (a *Access) checkAPIKey(ctx context.Context, r *http.Request, rule contracts.Rule, raw string, now time.Time) (contracts.Principal, error) {
	hash, ok := parseAPIKey(raw)
	if !ok {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	k, err := a.q.GetAPIKeyByTokenHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve API key: %w", err)
	}
	if k.Kind != contracts.UserKindIntegration {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}
	refuse := func(reason string, err error) (contracts.Principal, error) {
		return contracts.Principal{}, &contracts.APIKeyRefusal{KeyID: k.ID, IntegrationID: k.IntegrationID, Reason: reason, Err: err}
	}
	// The network is judged first, so a caller outside it learns nothing of
	// the key's state, not even that it has expired.
	ip := httpx.ClientIP(r)
	if !addressAllowed(k.AllowedNetworks, ip) {
		return refuse(apiKeyNetwork, contracts.ErrUnauthenticated)
	}
	switch {
	case k.RevokedAt != nil:
		return refuse(apiKeyRevoked, contracts.ErrUnauthenticated)
	case k.ArchivedAt != nil:
		return refuse(apiKeyArchived, contracts.ErrUnauthenticated)
	case k.IsDisabled || isLockedOut(k.LockoutEnd, now):
		return refuse(apiKeyDisabled, contracts.ErrUnauthenticated)
	case !k.ExpiresAt.After(now):
		return refuse(apiKeyExpired, contracts.ErrAPIKeyExpired)
	}

	if rule.API == contracts.APIRefused {
		return refuse(apiKeyNotAvailable, contracts.ErrAPINotAvailable)
	}
	if k.ReadOnly && rule.API != contracts.APIRead {
		return refuse(apiKeyReadOnly, contracts.ErrAPIKeyReadOnly)
	}
	// An integration never holds Owner or SystemAdmin (00141, 00194); this
	// is the same line drawn where the rule is evaluated, as for an agent.
	if slices.Contains(k.RoleNames, RoleOwner) || slices.Contains(k.RoleNames, RoleSystemAdmin) {
		return refuse(apiKeyForbidden, contracts.ErrForbidden)
	}
	roles := orderRoles(k.RoleNames)
	p := contracts.Principal{
		UserID: k.IntegrationID,
		Roles:  roles,
		Via:    &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: k.ID, RateLimitPerMinute: int(k.RateLimitPerMinute)},
	}
	switch rule.Kind {
	case contracts.RuleAnonymous:
	case contracts.RulePermission:
		sub := subject{
			userID: k.IntegrationID, roles: roles, disabled: k.IsDisabled, lockoutEnd: k.LockoutEnd,
			apiKey: true, narrowed: k.Permissions != nil, narrowing: k.Permissions,
		}
		p, err = a.evaluate(ctx, p, sub, rule, now)
		if errors.Is(err, contracts.ErrForbidden) {
			return refuse(apiKeyForbidden, err)
		}
		if err != nil {
			return p, err
		}
	default:
		// A session rule is a person's self-service, a policy the Owner tier,
		// and a scim rule never reaches here (K9).
		return refuse(apiKeyForbidden, contracts.ErrForbidden)
	}

	// Only an admitted call is a use, so "last used" never names a refused
	// one; failing to record it is logged and never fails the call.
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) >= time.Minute {
		var ipp *string
		if ip != "" {
			ipp = &ip
		}
		if err := a.q.TouchAPIKey(ctx, store.TouchAPIKeyParams{ID: k.ID, Now: now, Ip: ipp}); err != nil && a.log != nil {
			a.log.WarnContext(ctx, "identity: could not record an API key's use", "key", k.ID, "error", err)
		}
	}
	return p, nil
}

// apiNever reports whether key is one no API key ever holds (K7): an
// identity: or agents: key, or one the composed catalog does not let be
// delegated. A key missing from the catalog is one too, so a catalog that was
// never set fails closed.
func (a *Access) apiNever(key string) bool { return apiNeverIn(a.catalog, key) }

// apiNeverIn is apiNever under catalog: what the webhooks' fan-out and
// sender decide by, built where worker mode never composes (W7, W33).
func apiNeverIn(catalog map[string]contracts.Permission, key string) bool {
	if strings.HasPrefix(key, "identity:") || strings.HasPrefix(key, "agents:") {
		return true
	}
	p, ok := catalog[key]
	return !ok || !p.Delegable
}

// rejectAPIKey writes K19's answer to a refused key request: the code of the
// sentinel the refusal wraps, and on a 401 WWW-Authenticate: Bearer. It never
// touches the session cookie the browser may also hold (K18).
func rejectAPIKey(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, contracts.ErrAPINotAvailable):
		authError(w, http.StatusForbidden, "api_not_available", apiNotAvailableMessage, nil)
	case errors.Is(err, contracts.ErrAPIKeyReadOnly):
		authError(w, http.StatusForbidden, "api_key_read_only", apiKeyReadOnlyMessage, nil)
	case errors.Is(err, contracts.ErrForbidden):
		authError(w, http.StatusForbidden, "forbidden", forbiddenMessage, nil)
	case errors.Is(err, contracts.ErrAPIKeyExpired):
		w.Header().Set("WWW-Authenticate", `Bearer realm="vantigo"`)
		authError(w, http.StatusUnauthorized, "api_key_expired", apiKeyExpiredMessage, nil)
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="vantigo"`)
		authError(w, http.StatusUnauthorized, "unauthenticated", unauthenticatedMessage, nil)
	}
}

// Scope is contracts.APIKeyScope (K36): what the key of p reaches now — its
// integration's effective permissions less APINever, cut down to the key's
// narrowing — and whether it only reads.
func (a *Access) Scope(ctx context.Context, p contracts.Principal) ([]string, bool, error) {
	m, err := a.MCP(ctx, p)
	if err != nil {
		return nil, false, err
	}
	return m.Permissions, m.ReadOnly, nil
}

// MCP is contracts.MCPAccess (MCP server design M6, M22): the key of p's
// integration's MCP setting and the key's scope, from one read of the key and
// its integration.
func (a *Access) MCP(ctx context.Context, p contracts.Principal) (contracts.MCPKeyAccess, error) {
	if p.Via == nil || p.Via.Kind != contracts.ViaAPIKey || p.Via.KeyID == uuid.Nil {
		return contracts.MCPKeyAccess{}, errors.New("identity: not an API key's principal")
	}
	k, err := a.q.GetAPIKeyScope(ctx, p.Via.KeyID)
	if err != nil {
		return contracts.MCPKeyAccess{}, fmt.Errorf("identity: read the API key's scope: %w", err)
	}
	held, err := a.q.EffectivePermissionsForUser(ctx, k.IntegrationID)
	if err != nil {
		return contracts.MCPKeyAccess{}, fmt.Errorf("identity: read the API key's scope: %w", err)
	}
	perms := make([]string, 0, len(held))
	for _, key := range held {
		if a.apiNever(key) || (k.Permissions != nil && !slices.Contains(k.Permissions, key)) {
			continue
		}
		perms = append(perms, key)
	}
	return contracts.MCPKeyAccess{Mode: k.McpAccess, Permissions: perms, ReadOnly: k.ReadOnly}, nil
}

var (
	_ contracts.APIKeyScope = (*Access)(nil)
	_ contracts.MCPAccess   = (*Access)(nil)
)
