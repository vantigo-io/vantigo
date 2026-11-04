package google

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the callback's redemption (G26): the code, the PKCE verifier
// and the web client's secret, opened for this one request, posted to
// Google's token endpoint through the module's guarded client — no
// oauth2.Config, so the answer's OAuth error is read and the netguard
// dialler and the host list are used — and the ID token verified with
// go-oidc against https://accounts.google.com, its discovery document and
// published keys fetched through the same client: signature, audience and
// expiry, the issuer, the nonce in constant time, then hd among the
// organisation's verified domains. Google's refusals are read by their
// OAuth error alone, never error_description; nothing here logs a code, a
// token or the secret.

// personalRedeemed is a redeemed code: the account signed in (its sub and
// address), its granted scopes and its refresh token.
type personalRedeemed struct {
	sub     string
	email   string
	scopes  []string
	refresh string
}

// tokenAnswer is the token endpoint's answer to an authorization code or a
// refresh token, success or refusal.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// personalProviders is the process's discovered Google, by the client that
// discovered it, so a provider is never shared between two transports — two
// tests, in production the one guarded transport per server. go-oidc's key
// set refetches by itself when a token names a key it does not hold.
var personalProviders sync.Map

// googleProvider is Google's OIDC provider, discovered through c's guarded
// client at the first need; a failed discovery is not kept, so the next
// callback tries again.
func googleProvider(ctx context.Context, c *googleClient) (*oidc.Provider, error) {
	if p, ok := personalProviders.Load(c.http); ok {
		return p.(*oidc.Provider), nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, c.http), googleIssuer)
	if err != nil {
		return nil, err
	}
	actual, _ := personalProviders.LoadOrStore(c.http, p)
	return actual.(*oidc.Provider), nil
}

// redeemPersonalCode redeems code for state's connect with the web client's
// secret: the account and its token, or the callback's code ("" on success)
// with a reason to log. An error is a fault, never Google's answer.
func (s *server) redeemPersonalCode(ctx context.Context, conn store.GoogleConnection, secretRow store.GoogleCredential, state personalStateBody, code string) (personalRedeemed, string, string, error) {
	c := s.keys.c
	if c.box == nil {
		return personalRedeemed{}, "", "", errors.New("google: no secrets box to open the client secret with")
	}
	secret, err := c.box.Open(oauthClientSecretPurpose, secretRow.Sealed)
	if err != nil {
		return personalRedeemed{}, personalFailed, "the stored secret cannot be opened on this server", nil
	}
	clientID := deref(conn.OauthClientID)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {personalRedirectURI(s.deps.Config)},
		"code_verifier": {state.Verifier},
		"client_id":     {clientID},
		"client_secret": {string(secret)},
	}
	answer, status, err := c.postToken(ctx, form)
	if err != nil {
		if ctx.Err() != nil {
			return personalRedeemed{}, "", "", ctx.Err()
		}
		return personalRedeemed{}, personalFailed, "the token endpoint did not answer", nil
	}
	if status != http.StatusOK || answer.Error != "" {
		if slices.Contains(clientRefusals, answer.Error) {
			// The web client itself, the Owner's to mend (G28): recorded
			// only now, behind a validated state.
			if err := s.q.SetPersonalLastError(ctx, store.SetPersonalLastErrorParams{Code: ptr(answer.Error), Now: c.now()}); err != nil {
				return personalRedeemed{}, "", "", fmt.Errorf("google: record the web client refused: %w", err)
			}
		}
		code := personalFailed
		if answer.Error == "invalid_grant" {
			// A code expired, used already, or issued for another verifier.
			code = codeExpired
		}
		return personalRedeemed{}, code, fmt.Sprintf("the token endpoint refused the code: %d %s", status, answer.Error), nil
	}
	if answer.RefreshToken == "" || answer.IDToken == "" {
		return personalRedeemed{}, personalFailed, "the token answer lacks a refresh token or an ID token", nil
	}

	provider, err := googleProvider(ctx, c)
	if err != nil {
		if ctx.Err() != nil {
			return personalRedeemed{}, "", "", ctx.Err()
		}
		return personalRedeemed{}, personalFailed, "Google's discovery document could not be read", nil
	}
	idToken, err := provider.Verifier(&oidc.Config{
		ClientID:             clientID,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  c.now,
	}).Verify(ctx, answer.IDToken)
	if err != nil {
		if ctx.Err() != nil {
			return personalRedeemed{}, "", "", ctx.Err()
		}
		return personalRedeemed{}, personalFailed, "the ID token failed verification", nil
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(state.Nonce)) != 1 {
		return personalRedeemed{}, personalFailed, "the ID token's nonce does not match", nil
	}
	var claims struct {
		HD    string `json:"hd"`
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return personalRedeemed{}, personalFailed, "the ID token's claims do not decode", nil
	}
	// hd names the account's organisation; a consumer account carries none.
	if !domainAllowed(claims.HD, conn.Domains) {
		return personalRedeemed{}, personalDomainMismatch, "the ID token's hd is not one of the organisation's domains", nil
	}
	if !validUserID(idToken.Subject) {
		return personalRedeemed{}, personalFailed, "the ID token's sub is not a Google id", nil
	}
	return personalRedeemed{
		sub:     idToken.Subject,
		email:   strings.TrimSpace(claims.Email),
		scopes:  grantedScopes(answer.Scope),
		refresh: answer.RefreshToken,
	}, "", "", nil
}

// postToken posts form to Google's token endpoint through the guarded
// client, its answer read through maxBody: the decoded answer and the
// status, or a transport error (errGoogleUnreachable). The form carries a
// secret; nothing of it is logged.
func (c *googleClient) postToken(ctx context.Context, form url.Values) (tokenAnswer, int, error) {
	status, data, err := c.postForm(ctx, tokenURL, form)
	if err != nil {
		return tokenAnswer{}, 0, err
	}
	var answer tokenAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return tokenAnswer{}, status, fmt.Errorf("%w: the token answer is not JSON", errGoogleUnreachable)
	}
	return answer, status, nil
}

// postForm posts form to endpoint, one of the fixed hosts, through the
// guarded client: the status and the body read through maxBody, or a
// transport error (errGoogleUnreachable). Nothing of the form is logged.
func (c *googleClient) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, fmt.Errorf("google: build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, fmt.Errorf("%w: POST %s: %w", errGoogleUnreachable, req.URL.Host+req.URL.Path, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCapped(resp.Body, maxBody)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: POST %s: read the answer: %w", errGoogleUnreachable, req.URL.Host+req.URL.Path, err)
	}
	return resp.StatusCode, data, nil
}
