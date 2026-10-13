package vipps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tokenCache holds the access token. Its mutex is held across a fetch, so
// one fetch runs at a time and the callers waiting on it take its token.
type tokenCache struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// refreshBefore is how long before its expiry a token is no longer used: a
// call made with it must still be valid when Vipps reads it.
const refreshBefore = time.Minute

// drop forgets token, unless a newer one has replaced it already.
func (t *tokenCache) drop(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token == token {
		t.token = ""
	}
}

// token is the cached access token, fetched when there is none or it is
// within a minute of its expiry by the adapter's clock (1 hour in test, 24
// in production, R4 §4.6).
func (c *Client) token(ctx context.Context) (string, error) {
	c.tokens.mu.Lock()
	defer c.tokens.mu.Unlock()
	now := c.now()
	if c.tokens.token != "" && now.Before(c.tokens.expires.Add(-refreshBefore)) {
		return c.tokens.token, nil
	}
	token, lifetime, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	c.tokens.token, c.tokens.expires = token, now.Add(lifetime)
	return token, nil
}

// fetchToken is POST /accesstoken/get with the sales unit's keys as headers
// and an empty body.
func (c *Client) fetchToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/accesstoken/get", http.NoBody)
	if err != nil {
		return "", 0, err
	}
	c.setUnitHeaders(req)
	req.Header.Set("client_id", c.clientID)
	req.Header.Set("client_secret", c.clientSecret)
	req.Header.Set("Accept", "application/json")
	a, err := c.do(req, "token")
	if err != nil {
		return "", 0, err
	}
	if err := c.classify("token", a); err != nil {
		return "", 0, err
	}
	var body struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(a.body, &body); err != nil || body.AccessToken == "" {
		return "", 0, errors.New("vipps: the access token Vipps answered does not decode")
	}
	// Vipps writes expires_in as a string of seconds; a number is read too.
	seconds, err := strconv.Atoi(strings.Trim(string(body.ExpiresIn), `"`))
	if err != nil || seconds <= 0 {
		return "", 0, errors.New("vipps: the access token Vipps answered has no lifetime")
	}
	return body.AccessToken, time.Duration(seconds) * time.Second, nil
}
