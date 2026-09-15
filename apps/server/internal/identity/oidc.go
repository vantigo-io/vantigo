package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// Workforce OIDC sign-in (inventory §9, spec *Workforce OIDC*): one
// startup-bound Entra or Google provider, the authorization code flow with
// PKCE and a nonce, and just-in-time provisioning keyed on the normalized
// issuer and the subject. .NET ran it through ASP.NET's OpenIdConnect
// handler (EA/AuthServiceCollectionExtensions.cs:155-279) and
// WorkforceOidcEndpoints; Go runs the same steps itself:
//
//   - challenge seals state, nonce and the PKCE verifier into the
//     vantigo.oidc cookie and redirects to the provider;
//   - callback opens that cookie (spending it), redeems the code, verifies
//     the id_token and runs the claim policy, then seals the validated
//     identity into vantigo.identity.external;
//   - complete consumes that cookie and signs the linked account in, or
//     provisions one.
//
// Every failure is a 302 to /sign-in?error=<code>; nothing here answers
// with a JSON error, and no redirect target ever comes from the request.

// The two cookies of the flow, both HttpOnly, SameSite=Lax (the provider's
// redirect back is a cross-site top-level navigation, which Lax admits and
// Strict would not) and scoped to the OIDC endpoints under the base path.
const (
	// oidcStateCookieName carries challenge's state, nonce and PKCE verifier
	// to callback, sealed under oidcStatePurpose.
	oidcStateCookieName = "vantigo.oidc"
	// oidcExternalCookieName carries callback's validated identity to
	// complete, sealed under oidcExternalPurpose. The name is .NET's external
	// cookie's (EA/AuthServiceCollectionExtensions.cs:142-147).
	oidcExternalCookieName = "vantigo.identity.external"
)

// The secrets.Box purposes the two cookies are sealed under. Each is bound
// into AES-GCM as additional data, so a state cookie cannot be replayed as
// an external identity or the other way round.
const (
	oidcStatePurpose    = "identity/oidc-state"
	oidcExternalPurpose = "identity/oidc-external"
)

// The cookies' lifetimes, enforced on the Deps clock from the expiry sealed
// inside each, not only by the browser's Max-Age: ten minutes to sign in at
// the provider, and five from callback to complete, the ASP.NET Identity
// external cookie's lifetime.
const (
	oidcStateLifetime    = 10 * time.Minute
	oidcExternalLifetime = 5 * time.Minute
)

// The fixed paths of the flow, each prefixed with the base path when used.
// The callback path is .NET's fixed CallbackPath and the completion path
// its fixed CompletionPath (CFG/WorkforceOidcOptions.cs:28-29).
const (
	oidcPathPrefix   = "/api/v1/identity/oidc"
	oidcCallbackPath = oidcPathPrefix + "/callback"
	oidcCompletePath = oidcPathPrefix + "/complete"
	oidcSignInPath   = "/sign-in"
)

// The error codes a failed sign-in redirects with, .NET's
// (EA/AuthServiceCollectionExtensions.cs:194-205,
// EA/WorkforceOidcEndpoints.cs:61-174). .NET's oidc_local_sign_in_failed
// is not among them: ASP.NET answered it only when CanSignInAsync refused,
// and .NET required no confirmed account, email or phone number to sign in.
const (
	oidcRemoteFailure           = "oidc_remote_failure"
	oidcAuthenticationFailed    = "oidc_authentication_failed"
	oidcExternalIdentityMissing = "oidc_external_identity_missing"
	oidcIdentityInvalid         = "oidc_identity_invalid"
	oidcEmailConflict           = "oidc_email_conflict"
	oidcSignInUnavailable       = "oidc_sign_in_unavailable"
	oidcAccountLocked           = "account_locked"
	oidcLocalMFARequired        = "local_mfa_required"
)

// jwtBearerAssertionType is client_assertion_type for workload identity
// (EA/WorkforceOidcClientAssertion.cs:9).
const jwtBearerAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// oidcScopes are exactly .NET's (EA/AuthServiceCollectionExtensions.cs:170-173).
var oidcScopes = []string{oidc.ScopeOpenID, "profile", "email"}

// oidcHTTPTimeout bounds each discovery, key and token request in
// production, where the client is not injected.
const oidcHTTPTimeout = 30 * time.Second

// oidcRelyingParty is the installation's one workforce OIDC client: the
// configured provider, the HTTP client every provider request goes through,
// and the provider's discovered metadata, fetched once and cached.
type oidcRelyingParty struct {
	cfg *config.OIDCConfig
	// issuer is the configured authority normalized: the issuer every
	// oidc_links row is keyed on.
	issuer      string
	redirectURL string
	client      *http.Client
	now         func() time.Time

	mu       sync.Mutex
	provider *oidc.Provider
}

// newOIDCRelyingParty builds the relying party for cfg, or nil while OIDC
// is off. Nothing is fetched here: a provider that is down at startup
// fails its first sign-in, never the installation's start. client is the
// test's fake provider route; nil in production, which gets a client with
// oidcHTTPTimeout.
func newOIDCRelyingParty(cfg *config.Config, client *http.Client, now func() time.Time) (*oidcRelyingParty, error) {
	if cfg.OIDC == nil {
		return nil, nil
	}
	issuer, ok := normalizeIssuer(cfg.OIDC.Authority)
	if !ok {
		return nil, fmt.Errorf("identity: OIDC_AUTHORITY %q is not a valid issuer", cfg.OIDC.Authority)
	}
	if client == nil {
		client = &http.Client{Timeout: oidcHTTPTimeout}
	}
	return &oidcRelyingParty{
		cfg:         cfg.OIDC,
		issuer:      issuer,
		redirectURL: cfg.AppOrigin + cfg.BasePath + oidcCallbackPath,
		client:      client,
		now:         now,
	}, nil
}

// clientContext is ctx carrying the relying party's HTTP client, which
// go-oidc and oauth2 both take from the context.
func (rp *oidcRelyingParty) clientContext(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, rp.client)
}

// discover returns the provider's metadata, fetching it on first use. No
// lock is held across the fetch: while the provider is slow, each sign-in
// fetches on its own request's context, none queues behind another, and
// one whose request ends stops waiting. The first success is cached, and a
// racing fetch's result is dropped for it; a failure is not cached, so the
// next sign-in tries again. go-oidc refuses a discovery document whose
// issuer is not exactly the configured authority, which config pins to
// Entra's tenant-specific https://login.microsoftonline.com/<tenant>/v2.0
// (whose metadata names that concrete issuer, never the multi-tenant
// {tenantid} template) or to https://accounts.google.com
// (CFG/WorkforceOidcOptions.cs:75-83, :151-158).
func (rp *oidcRelyingParty) discover(ctx context.Context) (*oidc.Provider, error) {
	rp.mu.Lock()
	cached := rp.provider
	rp.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	p, err := oidc.NewProvider(rp.clientContext(ctx), rp.cfg.Authority)
	if err != nil {
		return nil, err
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.provider == nil {
		rp.provider = p
	}
	return rp.provider, nil
}

// oauth2Config is the code flow's client for provider p. The client secret,
// when the installation uses one, goes in the token request's form
// (client_secret_post, as ASP.NET's handler sent it); with workload
// identity there is none and callback adds a client assertion instead.
func (rp *oidcRelyingParty) oauth2Config(p *oidc.Provider) *oauth2.Config {
	endpoint := p.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &oauth2.Config{
		ClientID:     rp.cfg.ClientID,
		ClientSecret: rp.cfg.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  rp.redirectURL,
		Scopes:       oidcScopes,
	}
}

// oidcState is the vantigo.oidc cookie's sealed content.
type oidcState struct {
	State     string    `json:"state"`
	Nonce     string    `json:"nonce"`
	Verifier  string    `json:"verifier"`
	ExpiresAt time.Time `json:"exp"`
}

func (s oidcState) expires() time.Time { return s.ExpiresAt }

// externalIdentity is the vantigo.identity.external cookie's sealed
// content: the id_token claims callback validated, with the issuer replaced
// by the validated, normalized one, as .NET stamped its
// vantigo:oidc:validated-issuer claim (EA/AuthServiceCollectionExtensions.cs:268-273).
// It keeps what complete needs to re-run the claim policy and read the
// account, and nothing else.
type externalIdentity struct {
	Claims    oidcClaims `json:"claims"`
	ExpiresAt time.Time  `json:"exp"`
}

func (e externalIdentity) expires() time.Time { return e.ExpiresAt }

// oidcIssuer is the issuer the owner user list's ssoEnabled looks for: the
// configured authority normalized, or nil while OIDC is off.
func (s *server) oidcIssuer() *string {
	if s.oidc == nil {
		return nil
	}
	issuer := s.oidc.issuer
	return &issuer
}

// GetIdentityProviders lists the workforce sign-in providers: the configured
// OIDC provider's display name, or none (EA/WorkforceOidcEndpoints.cs:27-29).
func (s *server) GetIdentityProviders(context.Context, gen.GetIdentityProvidersRequestObject) (gen.GetIdentityProvidersResponseObject, error) {
	if s.oidc == nil {
		return gen.GetIdentityProviders200JSONResponse{Oidc: nil}, nil
	}
	return gen.GetIdentityProviders200JSONResponse{Oidc: &gen.OidcProviderResponse{DisplayName: s.oidc.cfg.DisplayName}}, nil
}

// GetIdentityOidcChallenge starts a sign-in at the provider
// (EA/WorkforceOidcEndpoints.cs:31-43): 404 while OIDC is off; otherwise a
// fresh state, nonce and PKCE verifier are sealed into the state cookie and
// the browser goes to the provider's authorization endpoint with the S256
// challenge, response_mode=query (so the Lax cookie comes back on the
// provider's GET redirect) and the fixed callback. The browser supplies no
// return URL: completion always lands on the base path's root. A provider
// whose metadata cannot be fetched is a remote failure.
func (s *server) GetIdentityOidcChallenge(ctx context.Context, _ gen.GetIdentityOidcChallengeRequestObject) (gen.GetIdentityOidcChallengeResponseObject, error) {
	if s.oidc == nil {
		return gen.GetIdentityOidcChallenge404Response{}, nil
	}
	p, err := s.oidc.discover(ctx)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "identity: workforce OIDC discovery failed", "error", err.Error())
		return s.oidcFailure(ctx, oidcRemoteFailure, "discovery failed"), nil
	}
	st := oidcState{
		State:     randomOIDCValue(),
		Nonce:     randomOIDCValue(),
		Verifier:  oauth2.GenerateVerifier(),
		ExpiresAt: s.deps.Clock().Add(oidcStateLifetime),
	}
	sealed, err := s.sealOIDC(oidcStatePurpose, st)
	if err != nil {
		return nil, err
	}
	location := s.oidc.oauth2Config(p).AuthCodeURL(st.State,
		oauth2.S256ChallengeOption(st.Verifier),
		oidc.Nonce(st.Nonce),
		oauth2.SetAuthURLParam("response_mode", "query"))
	return oidcRedirect{location: location, cookies: cookies{s.oidcCookie(oidcStateCookieName, sealed, oidcStateLifetime)}}, nil
}

// GetIdentityOidcCallback is the redirect URI with the authorization
// response in the query, the response mode challenge requests.
func (s *server) GetIdentityOidcCallback(ctx context.Context, req gen.GetIdentityOidcCallbackRequestObject) (gen.GetIdentityOidcCallbackResponseObject, error) {
	return s.oidcCallback(ctx, req.Params.Code, req.Params.State, req.Params.Error)
}

// PostIdentityOidcCallback is the same redirect URI for response_mode=
// form_post, which the contract documents as ASP.NET's default. It behaves
// exactly as the GET, reading the form instead of the query. A real
// provider's form_post cannot reach it, though, and that is deliberate:
// the provider's POST is a cross-site unsafe request, which server.New's
// CrossOriginProtection refuses, and a SameSite=Lax state cookie is not
// sent on a cross-site POST anyway. Challenge therefore always asks for
// response_mode=query; this handler serves same-origin posts only, and CSRF
// protection is not weakened to admit the provider's.
func (s *server) PostIdentityOidcCallback(ctx context.Context, req gen.PostIdentityOidcCallbackRequestObject) (gen.PostIdentityOidcCallbackResponseObject, error) {
	var form gen.PostIdentityOidcCallbackFormdataRequestBody
	if req.Body != nil {
		form = *req.Body
	}
	return s.oidcCallback(ctx, form.Code, form.State, form.Error)
}

// oidcCallback handles the provider's authorization response, the steps
// ASP.NET's OpenIdConnect handler ran with .NET's hooks
// (EA/AuthServiceCollectionExtensions.cs:190-275). The state cookie is
// single-use: every answer, success or failure, clears it. The failures map
// to .NET's two codes as ASP.NET routed them:
//
//   - oidc_remote_failure (OnRemoteFailure): the state cookie is missing,
//     expired or unreadable, or its state does not match the response's
//     (compared in constant time); the provider answered with an error; the
//     provider's metadata or the workload identity assertion could not be
//     read; or the validated token failed the issuer re-check or the claim
//     policy, which .NET's OnTokenValidated failed.
//   - oidc_authentication_failed (OnAuthenticationFailed): there is no
//     code, the code redemption failed, or the id_token failed validation:
//     signature, issuer, audience, expiry, nonce or a required claim.
//
// On success the validated identity is sealed into the external cookie and
// the browser goes on to complete.
func (s *server) oidcCallback(ctx context.Context, code, state, providerError *string) (_ oidcRedirect, err error) {
	r, err := requestFrom(ctx)
	if err != nil {
		return oidcRedirect{}, err
	}
	spent := s.expiredOIDCCookie(oidcStateCookieName)
	defer func() { clearOnServerError(ctx, err, spent) }()
	fail := func(errorCode, reason string) (oidcRedirect, error) {
		return s.oidcFailure(ctx, errorCode, reason, spent), nil
	}
	if s.oidc == nil {
		// OIDC is off: there is no challenge this response can belong to.
		// .NET's disabled scheme still owned the callback path and failed it
		// the same way.
		return fail(oidcRemoteFailure, "OIDC is not configured")
	}

	var st oidcState
	if !s.openOIDCCookie(r, oidcStateCookieName, oidcStatePurpose, &st) {
		return fail(oidcRemoteFailure, "the state cookie is missing, expired or invalid")
	}
	if deref(state) == "" || subtle.ConstantTimeCompare([]byte(*state), []byte(st.State)) != 1 {
		return fail(oidcRemoteFailure, "the state does not match")
	}
	if deref(providerError) != "" {
		return fail(oidcRemoteFailure, "the provider answered with an error")
	}
	if deref(code) == "" {
		return fail(oidcAuthenticationFailed, "the response carries no code")
	}

	p, err := s.oidc.discover(ctx)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "identity: workforce OIDC discovery failed", "error", err.Error())
		return fail(oidcRemoteFailure, "discovery failed")
	}
	options := []oauth2.AuthCodeOption{oauth2.VerifierOption(st.Verifier)}
	if s.oidc.cfg.WorkloadTokenFile != "" {
		// OnAuthorizationCodeReceived (EA/AuthServiceCollectionExtensions.cs:206-232):
		// the projected token is read for every redemption, never cached,
		// and no client secret exists in this mode.
		assertion, err := readClientAssertion(s.oidc.cfg.WorkloadTokenFile)
		if err != nil {
			return fail(oidcRemoteFailure, "the workload identity token file is unreadable or invalid")
		}
		options = append(options,
			oauth2.SetAuthURLParam("client_assertion_type", jwtBearerAssertionType),
			oauth2.SetAuthURLParam("client_assertion", assertion))
	}
	clientCtx := s.oidc.clientContext(ctx)
	token, err := s.oidc.oauth2Config(p).Exchange(clientCtx, *code, options...)
	if err != nil {
		// Only the OAuth error code is logged: a token endpoint's error body
		// is the provider's to word, and nothing of it is worth the risk.
		reason := "the code redemption failed"
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) && retrieve.ErrorCode != "" {
			reason += ": " + truncateRunes(retrieve.ErrorCode, 64)
		}
		return fail(oidcAuthenticationFailed, reason)
	}
	claims, errorCode, reason := s.oidc.validate(clientCtx, p, token, st.Nonce)
	if errorCode != "" {
		return fail(errorCode, reason)
	}

	sealed, err := s.sealOIDC(oidcExternalPurpose, externalIdentity{Claims: claims, ExpiresAt: s.deps.Clock().Add(oidcExternalLifetime)})
	if err != nil {
		return oidcRedirect{}, err
	}
	return oidcRedirect{
		location: s.deps.Config.BasePath + oidcCompletePath,
		cookies:  cookies{spent, s.oidcCookie(oidcExternalCookieName, sealed, oidcExternalLifetime)},
	}, nil
}

// validate checks the id_token of token as ASP.NET's handler and .NET's
// OnTokenValidated did, and returns its claims, or the failure's error code
// and a reason to log:
//
//  1. go-oidc verifies the signature against the provider's keys and the
//     audience (the client id); lifetimeProblem checks exp and nbf on the
//     Deps clock with ASP.NET's five minutes of clock skew.
//  2. The issuer must be exactly the configured authority: go-oidc lets
//     Google's scheme-less "accounts.google.com" through, which ASP.NET's
//     exact issuer validation refused.
//  3. The nonce must be the one challenge sealed, compared in constant
//     time; at_hash, when present, must match the access token.
//  4. iat and sub must be present, and azp, when present, must be the
//     client id: the checks of Microsoft.IdentityModel's
//     OpenIdConnectProtocolValidator.ValidateIdToken.
//
// Those are authentication failures. Then OnTokenValidated's two checks,
// whose failures are remote failures (EA/AuthServiceCollectionExtensions.cs:233-275):
//
//  5. the token's issuer, normalized, must equal the authority normalized;
//  6. the provider's claim policy (checkClaimPolicy) must pass.
func (rp *oidcRelyingParty) validate(ctx context.Context, p *oidc.Provider, token *oauth2.Token, nonce string) (oidcClaims, string, string) {
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return oidcClaims{}, oidcAuthenticationFailed, "the token response carries no id_token"
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: rp.cfg.ClientID, SkipExpiryCheck: true}).Verify(ctx, raw)
	if err != nil {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token failed verification: " + err.Error()
	}
	if problem := rp.lifetimeProblem(idToken); problem != "" {
		return oidcClaims{}, oidcAuthenticationFailed, problem
	}
	if idToken.Issuer != rp.cfg.Authority {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token's issuer is not the authority"
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token's nonce does not match"
	}
	if idToken.AccessTokenHash != "" {
		if err := idToken.VerifyAccessToken(token.AccessToken); err != nil {
			return oidcClaims{}, oidcAuthenticationFailed, "the id_token's at_hash does not match"
		}
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token's claims do not decode"
	}
	if idToken.IssuedAt.IsZero() || idToken.Subject == "" {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token lacks iat or sub"
	}
	if claims.AuthorizedParty != "" && claims.AuthorizedParty != rp.cfg.ClientID {
		return oidcClaims{}, oidcAuthenticationFailed, "the id_token's azp is not the client"
	}

	issuer, ok := normalizeIssuer(idToken.Issuer)
	if !ok || issuer != rp.issuer {
		return oidcClaims{}, oidcRemoteFailure, "the validated issuer does not match the configured authority"
	}
	claims.Issuer = issuer
	if err := checkClaimPolicy(rp.cfg, claims); err != nil {
		return oidcClaims{}, oidcRemoteFailure, err.Error()
	}
	return claims, "", ""
}

// idTokenClockSkew is the lifetime tolerance ASP.NET's token validation
// allowed, Microsoft.IdentityModel's TokenValidationParameters.DefaultClockSkew:
// a token is expired once now - 5 min is past its exp, and not yet valid
// while now + 5 min is before its nbf.
const idTokenClockSkew = 5 * time.Minute

// lifetimeProblem judges idToken's lifetime on the Deps clock, or returns
// "" when it holds. go-oidc allows the skew on nbf but none on exp, so the
// verifier skips its expiry check (which also skips its nbf check) and both
// are judged here, nbf exactly as go-oidc did. A token without exp is
// expired, as ASP.NET required one.
func (rp *oidcRelyingParty) lifetimeProblem(idToken *oidc.IDToken) string {
	var times struct {
		NotBefore *float64 `json:"nbf"`
	}
	if err := idToken.Claims(&times); err != nil {
		return "the id_token's nbf does not decode"
	}
	now := rp.now()
	if idToken.Expiry.Before(now.Add(-idTokenClockSkew)) {
		return "the id_token has expired"
	}
	if times.NotBefore != nil && now.Add(idTokenClockSkew).Before(time.Unix(int64(*times.NotBefore), 0)) {
		return "the id_token is not valid yet"
	}
	return ""
}

// GetIdentityOidcComplete signs the federated identity in
// (EA/WorkforceOidcEndpoints.cs:45-175). 404 while OIDC is off. Every
// answer consumes the external cookie. In .NET's order:
//
//  1. no valid, unexpired external cookie: oidc_external_identity_missing;
//  2. the claim policy, re-run, or the identity (the issuer against the
//     authority, and the subject: non-empty, no whitespace, at most 512)
//     fails: oidc_identity_invalid;
//  3. an account linked to (issuer, subject): account_locked when it is
//     disabled or locked out, local_mfa_required when it has TOTP (an OIDC
//     sign-in never counts as MFA and never skips the local second
//     factor), otherwise it is signed in;
//  4. no link, but the email belongs to an account: account_locked when
//     that account is unavailable, else oidc_email_conflict. An email never
//     links an identity to an existing account: that is the takeover guard;
//  5. otherwise a new User account is provisioned with its link, in one
//     serializable transaction. Losing a race to a concurrent provisioning
//     of the same identity re-reads the link and signs that account in, as
//     3 does; without one it is oidc_sign_in_unavailable.
//
// A sign-in creates a new non-persistent session with no MFA, records the
// operational heartbeat, and lands on the base path's root. A server error
// (a failed query) is a 500, as .NET's unhandled exception was, but it
// consumes the external cookie too (clearOnServerError).
func (s *server) GetIdentityOidcComplete(ctx context.Context, _ gen.GetIdentityOidcCompleteRequestObject) (_ gen.GetIdentityOidcCompleteResponseObject, err error) {
	if s.oidc == nil {
		return gen.GetIdentityOidcComplete404Response{}, nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	consumed := s.expiredOIDCCookie(oidcExternalCookieName)
	defer func() { clearOnServerError(ctx, err, consumed) }()
	fail := func(errorCode, reason string) (gen.GetIdentityOidcCompleteResponseObject, error) {
		return s.oidcFailure(ctx, errorCode, reason, consumed), nil
	}

	var external externalIdentity
	if !s.openOIDCCookie(r, oidcExternalCookieName, oidcExternalPurpose, &external) {
		return fail(oidcExternalIdentityMissing, "the external identity cookie is missing, expired or invalid")
	}
	if err := checkClaimPolicy(s.oidc.cfg, external.Claims); err != nil {
		return fail(oidcIdentityInvalid, err.Error())
	}
	account, ok := readExternalAccount(external.Claims, s.oidc.issuer)
	if !ok {
		return fail(oidcIdentityInvalid, "the external identity's issuer or subject is invalid")
	}

	linked, err := s.q.GetOidcLinkedUser(ctx, store.GetOidcLinkedUserParams{Issuer: account.issuer, Subject: account.subject})
	switch {
	case err == nil:
		return s.oidcSignInLinked(ctx, r, linked, consumed)
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("identity: OIDC link: %w", err)
	}

	if account.email != "" {
		u, taken, err := emailTaken(ctx, s.q, account.email)
		if err != nil {
			return nil, fmt.Errorf("identity: OIDC email: %w", err)
		}
		if taken {
			// A completion racing this one for the same identity may have
			// committed its account and link between the two reads: then
			// the email is that account's, and the link, which only a
			// completion for this very identity creates, decides, as after
			// a lost provisioning race. The email alone never links.
			linked, err := s.q.GetOidcLinkedUser(ctx, store.GetOidcLinkedUserParams{Issuer: account.issuer, Subject: account.subject})
			if err == nil {
				return s.oidcSignInLinked(ctx, r, linked, consumed)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("identity: OIDC link: %w", err)
			}
			if u.IsDisabled || isLockedOut(u.LockoutEnd, s.deps.Clock()) {
				return fail(oidcAccountLocked, "the email's account is unavailable")
			}
			return fail(oidcEmailConflict, "the email belongs to an account the identity is not linked to")
		}
	}

	// A serialization failure is retried before the link is consulted: SSI
	// dooms the losing transaction during the winner's pre-commit, so the
	// loser can fail and go looking for the link a moment before the
	// winner's commit is visible — and would then, wrongly, answer
	// oidc_sign_in_unavailable for an identity that was provisioned fine.
	// The retry re-runs the email check under a fresh snapshot: once the
	// winner has committed it reports the email taken
	// (errOIDCProvisioningLost), and the committed link decides below.
	var userID uuid.UUID
	err = db.RetrySerializable(ctx, serializableAttempts, func() error {
		var err error
		userID, err = s.provisionOIDCAccount(ctx, account)
		return err
	})
	if errors.Is(err, errOIDCProvisioningLost) || db.IsSerializationConflict(err) || db.IsUniqueViolation(err, "") {
		// A concurrent completion committed this identity while this one
		// provisioned (EA/WorkforceOidcEndpoints.cs:140-163): the rollback
		// leaves nothing behind, and the committed link decides.
		linked, err := s.q.GetOidcLinkedUser(ctx, store.GetOidcLinkedUserParams{Issuer: account.issuer, Subject: account.subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(oidcSignInUnavailable, "provisioning conflicted with another account change")
		}
		if err != nil {
			return nil, fmt.Errorf("identity: OIDC link: %w", err)
		}
		return s.oidcSignInLinked(ctx, r, linked, consumed)
	}
	if err != nil {
		return nil, err
	}
	return s.oidcSignIn(ctx, r, userID, consumed)
}

// oidcSignInLinked signs in the account an identity is linked to, as .NET's
// ExternalLoginSignInAsync(bypassTwoFactor: false) did
// (EA/WorkforceOidcEndpoints.cs:86-111): an unavailable account is
// account_locked, and an account with TOTP is local_mfa_required, since
// external claims are never local MFA proof and ASP.NET started its local
// two-factor step instead of signing in.
func (s *server) oidcSignInLinked(ctx context.Context, r *http.Request, u store.GetOidcLinkedUserRow, consumed *http.Cookie) (oidcRedirect, error) {
	if u.IsDisabled || isLockedOut(u.LockoutEnd, s.deps.Clock()) {
		return s.oidcFailure(ctx, oidcAccountLocked, "the linked account is unavailable", consumed), nil
	}
	if u.TotpEnabled {
		return s.oidcFailure(ctx, oidcLocalMFARequired, "the linked account has TOTP", consumed), nil
	}
	return s.oidcSignIn(ctx, r, u.ID, consumed)
}

// oidcSignIn starts a new non-persistent session for userID without MFA:
// ASP.NET's external sign-in added only an authentication-method claim
// naming the provider, never MfaClaims' "mfa" (EA/WorkforceOidcEndpoints.cs:95-99,
// :165, Authorization/MfaClaims.cs:15-28). An Owner or a SystemAdmin signs
// in the same way; while OWNERS_REQUIRE_MFA holds, their privileged
// endpoints then refuse the session as .NET's MfaAuthenticatedHandler did
// (EA/AuthAuthorization.cs:80-97), and nothing in .NET's completion
// refused them. It then records the heartbeat, best effort, and sends the
// browser to the base path's root.
func (s *server) oidcSignIn(ctx context.Context, r *http.Request, userID uuid.UUID, consumed *http.Cookie) (oidcRedirect, error) {
	token, err := s.access.createSession(ctx, s.deps.Pool, userID, false, false, r)
	if err != nil {
		return oidcRedirect{}, err
	}
	s.recordOperationalEvent(ctx, operationalEventStaticOIDCSignIn)
	return oidcRedirect{
		location: s.deps.Config.BasePath + "/",
		cookies:  cookies{consumed, s.access.newSessionCookie(token, false)},
	}, nil
}

// errOIDCProvisioningLost is provisionOIDCAccount's answer when, inside its
// transaction, the email already belongs to an account: a concurrent
// provisioning got there first.
var errOIDCProvisioningLost = errors.New("identity: OIDC provisioning lost a race")

// provisionOIDCAccount creates the just-in-time account for a (ProvisionNewUser,
// EA/WorkforceOidcEndpoints.cs:177-251) in one serializable transaction: the
// email re-checked, the user (the claimed email, else the opaque one; the
// email confirmed only when the provider verified it; no password), the
// User role and nothing more, and the (issuer, subject) link. Provider
// claims never become roles. A concurrent provisioning of the same identity
// makes this one fail on the email's unique index, the link's primary key,
// or serialization, and roll back.
func (s *server) provisionOIDCAccount(ctx context.Context, a externalAccount) (uuid.UUID, error) {
	now := s.deps.Clock()
	userID := uuid.New()
	email := a.email
	if email == "" {
		email = opaqueEmail(a.issuer, a.subject)
	}
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, taken, err := emailTaken(ctx, q, email); err != nil {
			return err
		} else if taken {
			return errOIDCProvisioningLost
		}
		if err := q.InsertUser(ctx, store.InsertUserParams{
			ID:              userID,
			Email:           email,
			NormalizedEmail: normalizeEmail(email),
			EmailConfirmed:  a.emailVerified,
			DisplayName:     a.displayName,
			Version:         uuid.New(),
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			return err
		}
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: RoleUserID}); err != nil {
			return err
		}
		return q.InsertOidcLink(ctx, store.InsertOidcLinkParams{Issuer: a.issuer, Subject: a.subject, UserID: userID, Now: now})
	})
	return userID, err
}

// readClientAssertion reads the workload identity token, as .NET's
// WorkforceOidcClientAssertion.ReadFresh did (EA/WorkforceOidcClientAssertion.cs:11-30):
// the file's content, trimmed, which must be non-empty and contain no
// whitespace. It is read for every redemption, so a rotated projected
// token is used as soon as it lands. Its errors never carry the content.
func readClientAssertion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("identity: the workload identity token file could not be read")
	}
	assertion := strings.TrimSpace(string(b))
	if assertion == "" || hasWhitespace(assertion) {
		return "", errors.New("identity: the workload identity token file is empty or invalid")
	}
	return assertion, nil
}

// oidcRedirect is every OIDC operation's answer: a 302 to location, a
// server-side constant or the provider's discovered authorization endpoint,
// with the cookies the step sets or clears. It is never cached.
type oidcRedirect struct {
	location string
	cookies  cookies
}

func (r oidcRedirect) write(w http.ResponseWriter) error {
	r.cookies.set(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", r.location)
	w.WriteHeader(http.StatusFound)
	return nil
}

func (r oidcRedirect) VisitGetIdentityOidcChallengeResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r oidcRedirect) VisitGetIdentityOidcCallbackResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r oidcRedirect) VisitPostIdentityOidcCallbackResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r oidcRedirect) VisitGetIdentityOidcCompleteResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// oidcFailure is a failed step's answer, 302 to the base path's
// /sign-in?error=<errorCode>, clearing the step's cookie
// (EA/WorkforceOidcEndpoints.cs:359-363). errorCode is always one of the
// oidc… constants. reason is logged beside it, at INFO: a failed federated
// sign-in is the browser's or the provider's problem, not the server's.
// Neither ever carries a code, token, assertion, secret, state or nonce.
func (s *server) oidcFailure(ctx context.Context, errorCode, reason string, clear ...*http.Cookie) oidcRedirect {
	s.deps.Logger.InfoContext(ctx, "identity: workforce OIDC sign-in refused", "code", errorCode, "reason", reason)
	return oidcRedirect{
		location: s.deps.Config.BasePath + oidcSignInPath + "?error=" + errorCode,
		cookies:  clear,
	}
}

// oidcCookie is one of the flow's cookies: the session cookie's HttpOnly
// and Secure policy, but SameSite=Lax, a path of the OIDC endpoints under
// the base path, and Max-Age lifetime.
func (s *server) oidcCookie(name, value string, lifetime time.Duration) *http.Cookie {
	c := s.access.cookie(name, value)
	c.Path = s.deps.Config.BasePath + oidcPathPrefix
	c.SameSite = http.SameSiteLaxMode
	c.MaxAge = int(lifetime.Seconds())
	return c
}

// clearOnServerError sets cookies on the response when a step ends in err,
// a server error module.ResponseError then answers with a 500. A step's
// cookie is spent on every answer, a 500 included, so a browser never
// keeps a half-used state or identity. This is deliberate: .NET's
// unhandled exceptions left its cookie in place.
func clearOnServerError(ctx context.Context, err error, cs ...*http.Cookie) {
	if err == nil {
		return
	}
	if w, ok := responseWriterFrom(ctx); ok {
		for _, c := range cs {
			http.SetCookie(w, c)
		}
	}
}

// expiredOIDCCookie tells the browser to drop one of the flow's cookies.
func (s *server) expiredOIDCCookie(name string) *http.Cookie {
	c := s.oidcCookie(name, "", 0)
	c.MaxAge = -1
	return c
}

// sealOIDC seals v's JSON under purpose, as cookie text.
func (s *server) sealOIDC(purpose string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("identity: seal OIDC cookie: %w", err)
	}
	sealed, err := s.deps.Secrets.SealString(purpose, string(b))
	if err != nil {
		return "", fmt.Errorf("identity: seal OIDC cookie: %w", err)
	}
	return sealed, nil
}

// openOIDCCookie opens the cookie name, sealed under purpose, into v and
// reports whether it is there, authentic and unexpired on the Deps clock.
// Why it is not says nothing a caller could act on differently, so it
// reports only false.
func (s *server) openOIDCCookie(r *http.Request, name, purpose string, v interface{ expires() time.Time }) bool {
	value, ok := cookieFrom(r, name)
	if !ok {
		return false
	}
	plain, err := s.deps.Secrets.OpenString(purpose, value)
	if err != nil || json.Unmarshal([]byte(plain), v) != nil {
		return false
	}
	return s.deps.Clock().Before(v.expires())
}

// randomOIDCValue is a fresh state or nonce: 256 random bits, base64url.
func randomOIDCValue() string {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return base64.RawURLEncoding.EncodeToString(b)
}

// truncateRunes is s cut to at most n runes, for logging provider-supplied
// text.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
