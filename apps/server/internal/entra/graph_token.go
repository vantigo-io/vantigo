package entra

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// This file is the client credentials token (Entra design D12, research
// §3.5): POST /{tenant}/oauth2/v2.0/token with the client id, the secret
// opened for this one request, the scope https://graph.microsoft.com/.default
// and grant_type client_credentials; the token kept in the process until five
// minutes before its expires_in, per credential, under a mutex; its roles
// claim read for the connection test, and its oid claim — the application's
// service principal in the tenant (plan reading 2) — for the groups' owner. A refusal of the secret
// (invalid_client, unauthorized_client) is errCredentialInvalid, which
// credentialFor answers by marking the secret failed and trying the next.

// tokenEarlyExpiry is how long before its expires_in a token is no longer
// used: a call made with it must still be answered before the tenant
// refuses it.
const tokenEarlyExpiry = 5 * time.Minute

// The OAuth error codes that refuse the credential itself.
var credentialRefusals = []string{"invalid_client", "unauthorized_client"}

// tokenKey names one credential's token: its row id, the tenant and the
// client it is for, and a digest of its sealed value — so a token is never
// handed to another credential, another registration or (in tests, where
// several databases number their rows alike) another installation.
type tokenKey struct {
	id       int64
	tenantID uuid.UUID
	clientID uuid.UUID
	sealed   [sha256.Size]byte
}

// keyOf is cred's tokenKey.
func keyOf(cred graphCredential) tokenKey {
	return tokenKey{id: cred.id, tenantID: cred.tenantID, clientID: cred.clientID, sealed: sha256.Sum256(cred.sealed)}
}

// cachedToken is a token and what it says: its roles claim, its oid claim
// (the service principal's object id, nil when absent), and the instant it
// is no longer used (expires_in less tokenEarlyExpiry).
type cachedToken struct {
	value string
	roles []string
	oid   *uuid.UUID
	until time.Time
}

// tokenCache is the process's tokens, by credential, under a mutex. A token
// is fetched without the lock held, so a slow token endpoint holds up no
// other credential; two callers missing at once may both fetch, and the
// later one is kept.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[tokenKey]cachedToken
}

// processTokens is the cache every client of the process shares, so the
// connection's endpoints and the provisioner reuse one token.
var processTokens = &tokenCache{}

// get answers key's token while it is good at now; an expired one is
// dropped.
func (t *tokenCache) get(key tokenKey, now time.Time) (cachedToken, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok, ok := t.tokens[key]
	if !ok {
		return cachedToken{}, false
	}
	if !now.Before(tok.until) {
		delete(t.tokens, key)
		return cachedToken{}, false
	}
	return tok, true
}

// put keeps tok under key, and forgets every token expired at now — a
// revoked or replaced credential's is never asked for again.
func (t *tokenCache) put(key tokenKey, tok cachedToken, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokens == nil {
		t.tokens = map[tokenKey]cachedToken{}
	}
	for k, old := range t.tokens {
		if !now.Before(old.until) {
			delete(t.tokens, k)
		}
	}
	t.tokens[key] = tok
}

// drop forgets cred's token: the credential was revoked or refused, or
// Graph refused the token.
func (t *tokenCache) drop(cred graphCredential) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tokens, keyOf(cred))
}

// token is cred's access token, from the cache or fetched.
func (c *graphClient) token(ctx context.Context, cred graphCredential) (string, error) {
	tok, _, err := c.tokenFor(ctx, cred, false)
	return tok.value, err
}

// tokenFor is cred's token and whether it was fetched just now: from the
// cache unless fresh, else from the token endpoint, kept when it lives
// longer than tokenEarlyExpiry.
func (c *graphClient) tokenFor(ctx context.Context, cred graphCredential, fresh bool) (cachedToken, bool, error) {
	key := keyOf(cred)
	if !fresh {
		if tok, ok := c.tokens.get(key, c.now()); ok {
			return tok, false, nil
		}
	}
	tok, err := c.fetchToken(ctx, cred)
	if err != nil {
		c.tokens.drop(cred)
		return cachedToken{}, false, err
	}
	if now := c.now(); now.Before(tok.until) {
		c.tokens.put(key, tok, now)
	}
	return tok, true, nil
}

// tokenAnswer is the token endpoint's answer, success or refusal.
type tokenAnswer struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	Error       string `json:"error"`
}

// fetchToken asks the token endpoint for cred's token. The secret is opened
// for this request alone and is never logged: a refusal logs the status and
// the OAuth code alone — not error_description, the endpoint's own words,
// which this request's secret could be echoed in.
func (c *graphClient) fetchToken(ctx context.Context, cred graphCredential) (cachedToken, error) {
	if c.box == nil {
		return cachedToken{}, errors.New("entra: no secrets box to open the client secret with")
	}
	secret, err := c.box.Open(clientSecretPurpose, cred.sealed)
	if err != nil {
		return cachedToken{}, fmt.Errorf("%w: the stored secret cannot be opened on this server", errCredentialInvalid)
	}
	form := url.Values{
		"client_id":     {cred.clientID.String()},
		"client_secret": {string(secret)},
		"scope":         {graphScope},
		"grant_type":    {"client_credentials"},
	}
	endpoint := fmt.Sprintf(graphTokenEndpoint, cred.tenantID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return cachedToken{}, fmt.Errorf("entra: build the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	now := c.now()
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return cachedToken{}, ctx.Err()
		}
		return cachedToken{}, fmt.Errorf("%w: the token request: %w", errTenantUnreachable, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCapped(resp.Body, graphMaxBody)
	if err != nil {
		return cachedToken{}, fmt.Errorf("%w: the token answer: %w", errTenantUnreachable, err)
	}
	var answer tokenAnswer
	decodeErr := json.Unmarshal(data, &answer)
	if resp.StatusCode != http.StatusOK || decodeErr != nil || answer.AccessToken == "" {
		c.logger.DebugContext(ctx, "entra: the token endpoint refused",
			"tenant", cred.tenantID.String(), "credential", cred.id, "status", resp.StatusCode, "code", answer.Error)
		if slices.Contains(credentialRefusals, answer.Error) {
			return cachedToken{}, fmt.Errorf("%w: %s", errCredentialInvalid, answer.Error)
		}
		return cachedToken{}, fmt.Errorf("%w: the token endpoint answered %d %s", errTenantUnreachable, resp.StatusCode, answer.Error)
	}
	roles, oid := claimsOf(answer.AccessToken)
	return cachedToken{
		value: answer.AccessToken,
		roles: roles,
		oid:   oid,
		until: now.Add(time.Duration(answer.ExpiresIn)*time.Second - tokenEarlyExpiry),
	}, nil
}

// rolesOf is the roles claim of an access token, sorted (claimsOf).
func rolesOf(token string) []string {
	roles, _ := claimsOf(token)
	return roles
}

// claimsOf is the roles claim of an access token, sorted, and its oid claim:
// the payload decoded and read with no signature checked — the token is the
// one the tenant just handed this client. The roles are read for the
// connection test's report; the oid of an app-only token is the
// application's service principal in the tenant, which every group the
// module makes names as an owner (plan reading 2). A token whose payload
// does not decode has neither; an oid that is not a GUID is none.
func claimsOf(token string) ([]string, *uuid.UUID) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, nil
	}
	var claims struct {
		Roles []string `json:"roles"`
		OID   string   `json:"oid"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return nil, nil
	}
	roles := slices.Clone(claims.Roles)
	slices.Sort(roles)
	var oid *uuid.UUID
	if id, ok := objectID(claims.OID); ok {
		oid = &id
	}
	return slices.Compact(roles), oid
}
