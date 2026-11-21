package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the consent page's two operations (OAuth connections design
// C13, C14, A6, A7, A9): the preview of an authorization request, and its
// approval or denial. Both are an Owner's, and both judge every parameter
// afresh from the query string the page received: nothing the preview
// answered is trusted by the approval.

const (
	// oauthCodeLifetime is how long a code may be exchanged (C14).
	oauthCodeLifetime = 60 * time.Second
	// oauthConnectionLifetime is how long a connection lasts at most (C17).
	oauthConnectionLifetime = 180 * 24 * time.Hour
	maxOAuthStateLength     = 512
	maxOAuthScopeLength     = 200
	maxOAuthResourceLength  = 1024
	maxOAuthQueryLength     = 8192
)

// The outcomes of a preview.
const (
	previewClientRefused = "client_refused"
	previewRedirect      = "redirect"
	previewConsent       = "consent"
)

// codeChallengePattern is a S256 challenge (A9): 43 characters of base64url.
var codeChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

var (
	oauthNotFound          = refuse(http.StatusNotFound, "not_found", "OAuth connections are not switched on.", nil)
	oauthQueryMessage      = "The authorization request's query string is required, at most 8,192 characters."
	integrationNotEligible = refuse(http.StatusConflict, "integration_not_eligible", "The client cannot be connected to this integration: it is archived or disabled, its MCP setting is off, or the access chosen is more than it or the request allows.", nil)
	oauthDecisionMessage   = "To approve, choose the integration and the access."
)

// authorizationRequest is an authorization request whose client and redirect
// URI Vantigo has verified (A6): from here on an error may be redirected.
type authorizationRequest struct {
	client      oauthClient
	redirectURI string
	state       string
	// hasState is whether state may be echoed: sent, and within its bound.
	hasState  bool
	challenge string
	// write is whether the request asks for write; read is always asked.
	write bool
}

// redirectError is a request wrong in a way the client is told about (C13):
// an OAuth error code and its description.
type redirectError struct{ code, description string }

// judgeAuthorization reads the query string of an authorization request and
// judges it in A6's order: the client and the redirect URI first, whose
// refusal is an errOAuthClient; then the rest, whose refusal is a
// redirectError carried with the request it is redirected to. Any other
// error is a failure to read.
func (a *Access) judgeAuthorization(ctx context.Context, raw string) (authorizationRequest, *redirectError, error) {
	var req authorizationRequest
	values, err := url.ParseQuery(raw)
	if err != nil {
		return req, nil, clientRefused("The authorization request's query string is not valid.")
	}
	one := func(name string) (string, bool) {
		v := values[name]
		if len(v) > 1 {
			return "", false
		}
		if len(v) == 0 {
			return "", true
		}
		return v[0], true
	}
	clientID, ok1 := one("client_id")
	redirectURI, ok2 := one("redirect_uri")
	if !ok1 || !ok2 {
		return req, nil, clientRefused("The authorization request repeats its client_id or redirect_uri.")
	}
	client, err := a.oauthClientFor(ctx, clientID)
	if err != nil {
		return req, nil, err
	}
	if redirectURI == "" || !slices.ContainsFunc(client.redirectURIs, func(registered string) bool { return redirectMatches(registered, redirectURI) }) {
		return req, nil, clientRefused("The redirect_uri is not one the client registered.")
	}
	req.client, req.redirectURI = client, redirectURI

	// The redirect is known: what is wrong now is the client's to be told.
	for name, v := range values {
		if len(v) > 1 {
			return req, &redirectError{"invalid_request", "The parameter " + name + " is repeated."}, nil
		}
	}
	state, _ := one("state")
	if len(state) > maxOAuthStateLength {
		return req, &redirectError{"invalid_request", "The state is longer than 512 characters."}, nil
	}
	req.state, req.hasState = state, values.Has("state")
	responseType, _ := one("response_type")
	if responseType != "code" {
		return req, &redirectError{"unsupported_response_type", "Only response_type=code is supported."}, nil
	}
	challenge, _ := one("code_challenge")
	method, _ := one("code_challenge_method")
	if !codeChallengePattern.MatchString(challenge) || method != "S256" {
		return req, &redirectError{"invalid_request", "PKCE is required: a code_challenge with code_challenge_method=S256."}, nil
	}
	req.challenge = challenge
	scope, _ := one("scope")
	if len(scope) > maxOAuthScopeLength {
		return req, &redirectError{"invalid_scope", "The scope is too long."}, nil
	}
	write, ok := parseScopes(scope)
	if !ok {
		return req, &redirectError{"invalid_scope", "The scopes are read, write and offline_access."}, nil
	}
	req.write = write
	if resource, _ := one("resource"); values.Has("resource") && !a.resourceMatches(resource) {
		return req, &redirectError{"invalid_target", "The resource is not this installation's MCP endpoint."}, nil
	}
	return req, nil, nil
}

// parseScopes reads a scope parameter (C3): read, write and offline_access,
// space-separated; none, or only offline_access, is read. It answers whether
// write is asked, and false for an unknown scope.
func parseScopes(scope string) (write, ok bool) {
	for _, s := range strings.Fields(scope) {
		switch s {
		case oauthScopeRead, oauthScopeOffline:
		case oauthScopeWrite:
			write = true
		default:
			return false, false
		}
	}
	return write, true
}

// scopeList is the scopes of a connection that may write or only read.
func scopeList(write bool) []string {
	if write {
		return []string{oauthScopeRead, oauthScopeWrite}
	}
	return []string{oauthScopeRead}
}

// resourceMatches compares a resource parameter with this installation's
// (C5, A12): scheme and host lower-cased and a default port dropped, a
// trailing slash tolerated; the MCP endpoint or the issuer itself.
func (a *Access) resourceMatches(resource string) bool {
	if resource == "" || len(resource) > maxOAuthResourceLength {
		return false
	}
	u, err := url.Parse(resource)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return false
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if port := u.Port(); (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	got := strings.TrimSuffix(scheme+"://"+host+u.EscapedPath(), "/")
	return got == a.cfg.OAuthResource || got == a.cfg.OAuthIssuer
}

// authorizationRedirect is the redirect URI with params added to its query
// (A4), and iss, and state when it may be echoed (C14).
func (a *Access) authorizationRedirect(req authorizationRequest, params map[string]string) string {
	u, err := url.Parse(req.redirectURI)
	if err != nil {
		return req.redirectURI // verified already; cannot happen
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if req.hasState {
		q.Set("state", req.state)
	}
	q.Set("iss", a.cfg.OAuthIssuer)
	u.RawQuery = q.Encode()
	return u.String()
}

// errorRedirect is the redirect that tells the client e.
func (a *Access) errorRedirect(req authorizationRequest, e redirectError) string {
	return a.authorizationRedirect(req, map[string]string{"error": e.code, "error_description": e.description})
}

// redirectHost is the host a redirect URI goes to, as shown.
func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// redirectsAutomatically is A6's rule as A26 narrows it: an error is
// redirected at once only when the redirect goes to the client_id's host or
// to this computer, and a client of that host has been connected here
// before. Otherwise a crafted, broken request could send a signed-in Owner to
// any host that publishes a document, with no click.
func (s *server) redirectsAutomatically(ctx context.Context, req authorizationRequest) (bool, error) {
	if !isLoopbackRedirect(req.redirectURI) && redirectHost(req.redirectURI) != req.client.host {
		return false, nil
	}
	seen, err := s.q.OAuthClientHostSeen(ctx, req.client.host)
	if err != nil {
		return false, fmt.Errorf("identity: read the client's host: %w", err)
	}
	return seen, nil
}

// consentLimited counts one preview or approval against the person's
// limit (A2): both may fetch a client's document. It answers the refusal to
// give when the limit is spent, nil otherwise.
func (s *server) consentLimited(ctx context.Context, p contracts.Principal) (*oauthRateLimited, error) {
	if s.deps.Limiter == nil {
		return nil, nil
	}
	d, err := s.deps.Limiter.Hit(ctx, policyOAuthConsent, p.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("identity: count the consent limit: %w", err)
	}
	if !d.Allowed {
		return &oauthRateLimited{decision: d}, nil
	}
	return nil, nil
}

// oauthRateLimited is the consent operations' 429, with Retry-After.
type oauthRateLimited struct{ decision ratelimit.Decision }

func (o oauthRateLimited) VisitPostIdentityOauthAuthorizationsPreviewResponse(w http.ResponseWriter) error {
	ratelimit.Reject(w, nil, policyOAuthConsent, o.decision)
	return nil
}

func (o oauthRateLimited) VisitPostIdentityOauthAuthorizationsResponse(w http.ResponseWriter) error {
	ratelimit.Reject(w, nil, policyOAuthConsent, o.decision)
	return nil
}

// integrationChoice is whether an integration may be connected (C13): active
// and its MCP setting not off, and the most a connection may do, capped by
// what the request asks.
func integrationChoice(row store.ListIntegrationsRow, write bool) gen.OAuthIntegrationChoice {
	choice := gen.OAuthIntegrationChoice{Id: row.UserID, Name: row.Name}
	switch {
	case row.IsDisabled:
		reason := gen.OAuthIntegrationChoiceReason("disabled")
		choice.Reason = &reason
	case row.McpAccess == contracts.MCPAccessOff:
		reason := gen.OAuthIntegrationChoiceReason("mcp_off")
		choice.Reason = &reason
	default:
		choice.Eligible = true
		access := gen.OAuthIntegrationChoiceMaxAccess(contracts.MCPAccessRead)
		if write && row.McpAccess == contracts.MCPAccessReadWrite {
			access = contracts.MCPAccessReadWrite
		}
		choice.MaxAccess = &access
	}
	return choice
}

// nameWarns is A7's warning: the name a client claims contains Vantigo or
// the workspace's name.
func (a *Access) nameWarns(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "vantigo") {
		return true
	}
	title := strings.ToLower(strings.TrimSpace(a.cfg.Branding.Title))
	return title != "" && strings.Contains(lower, title)
}

// PostIdentityOauthAuthorizationsPreview reads an authorization request for
// the consent page (C13, A6, A7).
// (POST /api/v1/identity/oauth/authorizations/preview)
func (s *server) PostIdentityOauthAuthorizationsPreview(ctx context.Context, req gen.PostIdentityOauthAuthorizationsPreviewRequestObject) (gen.PostIdentityOauthAuthorizationsPreviewResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	if req.Body == nil || req.Body.Query == "" || len(req.Body.Query) > maxOAuthQueryLength {
		return refuse(http.StatusBadRequest, "invalid_request", oauthQueryMessage, map[string][]string{"query": {oauthQueryMessage}}), nil
	}
	caller, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if limited, err := s.consentLimited(ctx, caller); err != nil || limited != nil {
		if err != nil {
			return nil, err
		}
		return *limited, nil
	}
	noStore := secretCacheControl
	answer := func(p gen.OAuthAuthorizationPreview) (gen.PostIdentityOauthAuthorizationsPreviewResponseObject, error) {
		return gen.PostIdentityOauthAuthorizationsPreview200JSONResponse{
			Body: p, Headers: gen.PostIdentityOauthAuthorizationsPreview200ResponseHeaders{CacheControl: &noStore},
		}, nil
	}
	areq, rerr, err := s.access.judgeAuthorization(ctx, strings.TrimPrefix(req.Body.Query, "?"))
	var refused errOAuthClient
	if errors.As(err, &refused) {
		code, message := "invalid_client", refused.reason
		return answer(gen.OAuthAuthorizationPreview{Outcome: previewClientRefused, Error: &code, Message: &message})
	}
	if err != nil {
		return nil, err
	}
	if rerr != nil {
		automatic, err := s.redirectsAutomatically(ctx, areq)
		if err != nil {
			return nil, err
		}
		to, host := s.access.errorRedirect(areq, *rerr), redirectHost(areq.redirectURI)
		return answer(gen.OAuthAuthorizationPreview{
			Outcome: previewRedirect, Error: &rerr.code, Message: &rerr.description, RedirectTo: &to, ReturnHost: &host, Automatic: &automatic,
		})
	}

	rows, err := s.q.ListIntegrations(ctx, store.ListIntegrationsParams{Now: s.deps.Clock()})
	if err != nil {
		return nil, fmt.Errorf("identity: list integrations: %w", err)
	}
	choices := make([]gen.OAuthIntegrationChoice, 0, len(rows))
	for _, row := range rows {
		if row.ArchivedAt == nil {
			choices = append(choices, integrationChoice(row, areq.write))
		}
	}
	seen, err := s.q.OAuthClientHostSeen(ctx, areq.client.host)
	if err != nil {
		return nil, fmt.Errorf("identity: read the client's host: %w", err)
	}
	name := oauthDisplayName(areq.client.name)
	firstUse, warn, scopes := !seen, s.access.nameWarns(name), scopeList(areq.write)
	return answer(gen.OAuthAuthorizationPreview{
		Outcome: previewConsent,
		Client: &gen.OAuthClientDisplay{
			Name: name, ClientId: areq.client.id, ClientIdHost: areq.client.host, Kind: oauthClientKindMetadata,
			RedirectHost: redirectHost(areq.redirectURI), Loopback: isLoopbackRedirect(areq.redirectURI),
		},
		Scopes: &scopes, Integrations: &choices, FirstUse: &firstUse, NameWarning: &warn,
	})
}

// PostIdentityOauthAuthorizations approves or denies an authorization request
// (C14, A9): the request is judged again; deny redirects with access_denied,
// approve creates the connection, not yet activated, and a code valid 60
// seconds, stored hashed, and redirects with it.
// (POST /api/v1/identity/oauth/authorizations)
func (s *server) PostIdentityOauthAuthorizations(ctx context.Context, req gen.PostIdentityOauthAuthorizationsRequestObject) (gen.PostIdentityOauthAuthorizationsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	if req.Body == nil || req.Body.Query == "" || len(req.Body.Query) > maxOAuthQueryLength {
		return refuse(http.StatusBadRequest, "invalid_request", oauthQueryMessage, map[string][]string{"query": {oauthQueryMessage}}), nil
	}
	body := *req.Body
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if limited, err := s.consentLimited(ctx, p); err != nil || limited != nil {
		if err != nil {
			return nil, err
		}
		return *limited, nil
	}
	noStore := secretCacheControl
	redirect := func(to string) (gen.PostIdentityOauthAuthorizationsResponseObject, error) {
		return gen.PostIdentityOauthAuthorizations200JSONResponse{
			Body: gen.OAuthAuthorizationRedirect{RedirectTo: to}, Headers: gen.PostIdentityOauthAuthorizations200ResponseHeaders{CacheControl: &noStore},
		}, nil
	}
	areq, rerr, err := s.access.judgeAuthorization(ctx, strings.TrimPrefix(body.Query, "?"))
	var refused errOAuthClient
	if errors.As(err, &refused) {
		return refuse(http.StatusBadRequest, "client_refused", refused.reason, nil), nil
	}
	if err != nil {
		return nil, err
	}
	if rerr != nil {
		return redirect(s.access.errorRedirect(areq, *rerr))
	}
	if body.Decision != "approve" {
		return redirect(s.access.errorRedirect(areq, redirectError{"access_denied", "The request was denied."}))
	}
	if body.IntegrationId == nil || body.Access == nil {
		return refuse(http.StatusBadRequest, "invalid_request", oauthDecisionMessage, map[string][]string{"integrationId": {oauthDecisionMessage}}), nil
	}
	now := s.deps.Clock()
	rows, err := s.q.ListIntegrations(ctx, store.ListIntegrationsParams{Now: now, ID: body.IntegrationId})
	if err != nil {
		return nil, fmt.Errorf("identity: read the integration: %w", err)
	}
	if len(rows) == 0 {
		return integrationNotFound, nil
	}
	choice := integrationChoice(rows[0], areq.write)
	access := string(*body.Access)
	if rows[0].ArchivedAt != nil || !choice.Eligible ||
		(access != contracts.MCPAccessRead && access != contracts.MCPAccessReadWrite) ||
		(access == contracts.MCPAccessReadWrite && string(*choice.MaxAccess) != contracts.MCPAccessReadWrite) {
		return integrationNotEligible, nil
	}
	code, codeHash := newToken()
	connectionID := uuid.New()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.InsertOAuthConnection(ctx, store.InsertOAuthConnectionParams{
			ID: connectionID, IntegrationID: rows[0].UserID, ClientID: areq.client.id, ClientKind: oauthClientKindMetadata,
			ClientName: oauthDisplayName(areq.client.name), ClientHost: areq.client.host, RedirectUri: areq.redirectURI,
			Scopes: scopeList(access == contracts.MCPAccessReadWrite), AuthorizedBy: &p.UserID, Now: now,
			ExpiresAt: now.Add(oauthConnectionLifetime),
		}); err != nil {
			return fmt.Errorf("identity: create the connection: %w", err)
		}
		if err := q.InsertOAuthCode(ctx, store.InsertOAuthCodeParams{
			CodeHash: codeHash, ConnectionID: connectionID, CodeChallenge: areq.challenge, Now: now, ExpiresAt: now.Add(oauthCodeLifetime),
		}); err != nil {
			return fmt.Errorf("identity: issue the code: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return redirect(s.access.authorizationRedirect(areq, map[string]string{"code": code}))
}

func (r refusal) VisitPostIdentityOauthAuthorizationsPreviewResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOauthAuthorizationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
