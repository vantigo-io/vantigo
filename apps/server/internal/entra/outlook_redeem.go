package entra

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
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the callback's redemption (Outlook connection design M4,
// plan reading 12.4–12.5): the code, the PKCE verifier and the client
// secret, opened for this one request, posted to the tenant's token
// endpoint through the module's guarded client — no oauth2.Config, so the
// answer's AADSTS error_codes are read and the netguard dialler is used —
// and the id_token verified with go-oidc against the tenant's published
// keys: signature, audience and expiry, then the tenant, the issuer, the
// nonce in constant time and the object id. Microsoft's refusals are read
// by code alone, never error_description; nothing here logs a code, a
// token or the secret.

// outlookRedeemed is a redeemed code: the account signed in, its granted
// scopes normalised and its refresh token.
type outlookRedeemed struct {
	oid     uuid.UUID
	tid     uuid.UUID
	upn     string
	scopes  []string
	refresh string
}

// outlookTokenAnswer is the token endpoint's answer to a delegated grant,
// success or refusal.
type outlookTokenAnswer struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorCodes   []int  `json:"error_codes"`
}

// redeemRefusalCode is the callback's code for Microsoft's refusal of a
// code: the code expired or already redeemed (70008, 54005) is expired,
// anything else failed.
func redeemRefusalCode(oauthError string, codes []int) string {
	if oauthError == "invalid_grant" && (slices.Contains(codes, 70008) || slices.Contains(codes, 54005)) {
		return codeExpired
	}
	return outlookFailed
}

// keySetKey names one tenant's key set as one client fetches it, so a key
// set is never shared between two transports — two test tenants, in
// production the one guarded transport per Deps.
type keySetKey struct {
	tenant uuid.UUID
	client *http.Client
}

// outlookKeySets is the process's key sets, by tenant and client, kept
// like processTokens: go-oidc's RemoteKeySet refetches by itself when a
// token names a key it does not hold.
var outlookKeySets sync.Map

// keySetFor is the tenant's published signing keys, fetched through c's
// guarded client.
func keySetFor(c *graphClient, tenant uuid.UUID) *oidc.RemoteKeySet {
	key := keySetKey{tenant: tenant, client: c.http}
	if ks, ok := outlookKeySets.Load(key); ok {
		return ks.(*oidc.RemoteKeySet)
	}
	ks := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), c.http),
		microsoftLogin+tenant.String()+"/discovery/v2.0/keys")
	actual, _ := outlookKeySets.LoadOrStore(key, ks)
	return actual.(*oidc.RemoteKeySet)
}

// redeemOutlookCode redeems code for state's connect with cred: the
// account and its token, or the callback's code ("" on success) with a
// reason to log. An error is a fault, never Microsoft's answer.
func (s *server) redeemOutlookCode(ctx context.Context, conn store.EntraConnection, cred graphCredential, state outlookStateBody, code string) (outlookRedeemed, string, string, error) {
	c := s.client
	if c.box == nil {
		return outlookRedeemed{}, "", "", errors.New("entra: no secrets box to open the client secret with")
	}
	secret, err := c.box.Open(clientSecretPurpose, cred.sealed)
	if err != nil {
		return outlookRedeemed{}, outlookFailed, "the stored secret cannot be opened on this server", nil
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {outlookRedirectURI(s.deps.Config)},
		"code_verifier": {state.Verifier},
		"client_id":     {cred.clientID.String()},
		"client_secret": {string(secret)},
		"scope":         {outlookScope(state.Feature)},
		"claims":        {outlookClaims},
	}
	answer, status, err := c.postToken(ctx, cred.tenantID, form)
	if err != nil {
		if ctx.Err() != nil {
			return outlookRedeemed{}, "", "", ctx.Err()
		}
		return outlookRedeemed{}, outlookFailed, "the token endpoint did not answer", nil
	}
	if status != http.StatusOK || answer.Error != "" {
		// The client's own refusal (invalid_client, unauthorized_client) is
		// answered like any other: this anonymous callback never marks the
		// credential, which the application token's path judges.
		return outlookRedeemed{}, redeemRefusalCode(answer.Error, answer.ErrorCodes),
			fmt.Sprintf("the token endpoint refused the code: %d %s %v", status, answer.Error, answer.ErrorCodes), nil
	}
	if answer.RefreshToken == "" || answer.IDToken == "" {
		return outlookRedeemed{}, outlookFailed, "the token answer lacks a refresh token or an id_token", nil
	}

	verifier := oidc.NewVerifier("", keySetFor(c, *conn.TenantID), &oidc.Config{
		ClientID:             conn.ClientID.String(),
		SupportedSigningAlgs: []string{oidc.RS256},
		// The issuer is checked below, after the tenant, so a token of
		// another tenant answers tenant_mismatch rather than failed.
		SkipIssuerCheck: true,
		Now:             c.now,
	})
	idToken, err := verifier.Verify(ctx, answer.IDToken)
	if err != nil {
		if ctx.Err() != nil {
			return outlookRedeemed{}, "", "", ctx.Err()
		}
		return outlookRedeemed{}, outlookFailed, "the id_token failed verification", nil
	}
	var claims struct {
		TID string `json:"tid"`
		OID string `json:"oid"`
		UPN string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return outlookRedeemed{}, outlookFailed, "the id_token's claims do not decode", nil
	}
	tid, ok := objectID(claims.TID)
	if !ok || tid != *conn.TenantID {
		return outlookRedeemed{}, outlookTenantMismatch, "the id_token is another tenant's", nil
	}
	if idToken.Issuer != microsoftLogin+tid.String()+"/v2.0" {
		return outlookRedeemed{}, outlookFailed, "the id_token's issuer is not the tenant's", nil
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(state.Nonce)) != 1 {
		return outlookRedeemed{}, outlookFailed, "the id_token's nonce does not match", nil
	}
	oid, ok := objectID(claims.OID)
	if !ok {
		return outlookRedeemed{}, outlookFailed, "the id_token's oid is not an object id", nil
	}
	return outlookRedeemed{
		oid:     oid,
		tid:     tid,
		upn:     strings.TrimSpace(claims.UPN),
		scopes:  normaliseScopes(answer.Scope),
		refresh: answer.RefreshToken,
	}, "", "", nil
}

// postToken posts form to the tenant's token endpoint through the guarded
// client, its answer read through graphMaxBody: the decoded answer and the
// status, or a transport error. The form carries a secret; nothing of it
// is logged.
func (c *graphClient) postToken(ctx context.Context, tenant uuid.UUID, form url.Values) (outlookTokenAnswer, int, error) {
	endpoint := fmt.Sprintf(graphTokenEndpoint, tenant.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return outlookTokenAnswer{}, 0, fmt.Errorf("entra: build the token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return outlookTokenAnswer{}, 0, fmt.Errorf("%w: the token request: %w", errTenantUnreachable, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCapped(resp.Body, graphMaxBody)
	if err != nil {
		return outlookTokenAnswer{}, 0, fmt.Errorf("%w: the token answer: %w", errTenantUnreachable, err)
	}
	var answer outlookTokenAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return outlookTokenAnswer{}, resp.StatusCode, fmt.Errorf("%w: the token answer is not JSON", errTenantUnreachable)
	}
	return answer, resp.StatusCode, nil
}
