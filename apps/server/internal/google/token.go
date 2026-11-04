package google

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the delegated token (Google Workspace design G13, G14): a
// JWT signed with the service account's key, naming the admin or the person
// it acts as (sub) and the scopes it asks for, exchanged at tokenURL by
// golang.org/x/oauth2/jwt with the guarded client handed in the context as
// oauth2.HTTPClient — so the exchange reaches Google through hostGuard and
// netguard as every call does. A token is kept in the process until five
// minutes before it expires, per (key, subject, scope set), under a mutex.
//
// tokenSource(scopes, subject) is the one seam every caller takes its token
// from. The key used is the newest live one; an older one only when the
// newer is refused (invalid_grant on its JWT), which marks it failed and is
// an event. A subject Google does not know is not a refused key: it marks
// nothing and is errSubjectInvalid. A later credential kind, vantigo_hosted, fills the same seam
// without a key.

const (
	// serviceAccountKeyPurpose is the secrets box purpose a service
	// account's private key is sealed under.
	serviceAccountKeyPurpose = "google/service-account-key"
	// tokenEarlyExpiry is how long before it expires a token is no longer
	// used: a call made with it must still be answered before Google
	// refuses it.
	tokenEarlyExpiry = 5 * time.Minute
	// credentialUsedInterval is how often, at most, a key's last_used_at is
	// written.
	credentialUsedInterval = time.Minute
)

// fetchMode is whether a token may come from the cache and whether a
// fetched one is kept there.
type fetchMode int

const (
	// tokenCached answers a cached token while it is good, and keeps the
	// one it fetches.
	tokenCached fetchMode = iota
	// tokenFresh asks Google even when a token is cached, as the connection
	// test does for a needed scope, so a delegation granted since is read;
	// the token is kept for the test's own reads.
	tokenFresh
	// tokenProbe asks Google and keeps nothing: the test's probe of a scope
	// Vantigo never uses, whose token must not outlive the probe.
	tokenProbe
)

// tokenSource is where a call's bearer token comes from: token answers the
// token to send, dropped forgets it after Google refused it, before the one
// retry.
type tokenSource interface {
	token(ctx context.Context) (string, error)
	dropped()
}

// keyring finds the key a delegated token is signed with and answers the
// token: the module's rows through q, Google through c.
type keyring struct {
	q *store.Queries
	c *googleClient
}

// newKeyring is the keyring over q and c.
func newKeyring(q *store.Queries, c *googleClient) *keyring {
	return &keyring{q: q, c: c}
}

// tokenSource is the source of the token for scopes acting as subject: the
// seam every call to Google takes its token from.
func (k *keyring) tokenSource(scopes []string, subject string) tokenSource {
	return &delegatedSource{k: k, scopes: normaliseScopes(scopes), subject: subject}
}

// normaliseScopes is scopes sorted and without repeats, so one set is one
// cache entry whatever order it was asked in.
func normaliseScopes(scopes []string) []string {
	out := slices.Clone(scopes)
	slices.Sort(out)
	return slices.Compact(out)
}

// delegatedSource is one (scopes, subject)'s token source. key is the key
// its last token was found or fetched with, for dropped.
type delegatedSource struct {
	k       *keyring
	scopes  []string
	subject string

	mu  sync.Mutex
	key *tokenKey
}

func (s *delegatedSource) token(ctx context.Context) (string, error) {
	tok, key, err := s.k.token(ctx, s.scopes, s.subject, tokenCached)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.key = &key
	s.mu.Unlock()
	return tok, nil
}

func (s *delegatedSource) dropped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		s.k.c.tokens.drop(*s.key)
	}
}

// liveKeys are the service account keys a token may be asked with, in the
// order they are tried: unrevoked, newest first, and a key Google refused
// since it last worked after every other.
func liveKeys(rows []store.GoogleCredential) []store.GoogleCredential {
	var out []store.GoogleCredential
	for _, r := range rows {
		if r.Kind == credentialKindKey && r.RevokedAt == nil {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b store.GoogleCredential) int {
		if fa, fb := failedSinceUsed(a), failedSinceUsed(b); fa != fb {
			if fa {
				return 1
			}
			return -1
		}
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return out
}

// failedSinceUsed is a key Google refused after it last fetched a token, or
// never let fetch one.
func failedSinceUsed(r store.GoogleCredential) bool {
	return r.LastFailedAt != nil && (r.LastUsedAt == nil || r.LastFailedAt.After(*r.LastUsedAt))
}

// token is the token for scopes as subject, and the cache key it is kept
// under: the first live key (liveKeys) a token is found or fetched for, as
// mode says. A key Google refuses is marked failed, audited once until it
// works again, and the next is tried; when none is left the connection is
// set failing/credential_invalid and the answer is errNoCredential. A scope
// refused is errNotDelegated at once, and a subject refused errSubjectInvalid
// at once, marking nothing — the next key is the same service account's and
// would be refused too.
// errGoogleUnreachable (wrapped) when the token endpoint could not be
// asked, which marks nothing. The rows are read on the pool, never in a
// transaction: Google is asked between them.
func (k *keyring) token(ctx context.Context, scopes []string, subject string, mode fetchMode) (string, tokenKey, error) {
	if subject == "" {
		return "", tokenKey{}, errNotConfigured
	}
	rows, err := k.q.ListCredentials(ctx)
	if err != nil {
		return "", tokenKey{}, fmt.Errorf("google: read the keys: %w", err)
	}
	keys := liveKeys(rows)
	if len(keys) == 0 {
		return "", tokenKey{}, errNoCredential
	}
	for _, row := range keys {
		key := keyOf(row, subject, scopes)
		if mode == tokenCached {
			if tok, ok := k.c.tokens.get(key, k.c.now()); ok {
				return tok, key, nil
			}
		}
		tok, err := k.c.fetchToken(ctx, row, subject, scopes)
		switch {
		case err == nil:
			now := k.c.now()
			if err := k.q.MarkCredentialUsed(ctx, store.MarkCredentialUsedParams{Now: now, ID: row.ID, UsedBefore: now.Add(-credentialUsedInterval)}); err != nil {
				return "", tokenKey{}, fmt.Errorf("google: mark the key used: %w", err)
			}
			if mode != tokenProbe && now.Before(tok.until) {
				k.c.tokens.put(key, tok, now)
			}
			return tok.value, key, nil
		case errors.Is(err, errCredentialInvalid):
			k.c.tokens.drop(key)
			if err := k.refused(ctx, row); err != nil {
				return "", tokenKey{}, err
			}
		default:
			return "", tokenKey{}, err
		}
	}
	conn, err := k.q.GetConnection(ctx)
	if err != nil {
		return "", tokenKey{}, fmt.Errorf("google: read the connection: %w", err)
	}
	if conn.Status != statusFailing || conn.StatusReason == nil || *conn.StatusReason != reasonCredentialInvalid {
		reason := reasonCredentialInvalid
		if err := k.q.SetConnectionFailing(ctx, &reason); err != nil {
			return "", tokenKey{}, fmt.Errorf("google: record the connection failing: %w", err)
		}
	}
	return "", tokenKey{}, errNoCredential
}

// refused marks row failed and audits it, by the module's system actor:
// whatever call found it, Google refused the key itself. A key already
// failed since it last worked is not audited again.
func (k *keyring) refused(ctx context.Context, row store.GoogleCredential) error {
	now := k.c.now()
	if err := k.q.MarkCredentialFailed(ctx, store.MarkCredentialFailedParams{Now: now, ID: row.ID}); err != nil {
		return fmt.Errorf("google: mark the key failed: %w", err)
	}
	if failedSinceUsed(row) {
		return nil
	}
	return recordEvent(ctx, k.q, now, contracts.Actor{System: systemActor}, actionCredentialRefused, deref(row.ClientID), credentialCodes(row))
}

// tokenKey names one token: the key's row id and a digest of its sealed
// value — so a token is never handed to another key or (in tests, where
// several databases number their rows alike) another installation — the
// subject and the scope set.
type tokenKey struct {
	id      int64
	sealed  [sha256.Size]byte
	subject string
	scopes  string
}

// keyOf is the tokenKey of row's token for subject and scopes.
func keyOf(row store.GoogleCredential, subject string, scopes []string) tokenKey {
	return tokenKey{id: row.ID, sealed: sha256.Sum256(row.Sealed), subject: strings.ToLower(subject), scopes: strings.Join(scopes, " ")}
}

// cachedToken is a token and the instant it is no longer used (its
// expires_in less tokenEarlyExpiry).
type cachedToken struct {
	value string
	until time.Time
}

// tokenCache is the process's tokens, under a mutex. A token is fetched
// without the lock held, so a slow token endpoint holds up no other key;
// two callers missing at once may both fetch, and the later one is kept.
type tokenCache struct {
	mu     sync.Mutex
	tokens map[tokenKey]cachedToken
}

// processTokens is the cache every client of the process shares.
var processTokens = &tokenCache{}

// get answers key's token while it is good at now; an expired one is
// dropped.
func (t *tokenCache) get(key tokenKey, now time.Time) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok, ok := t.tokens[key]
	if !ok {
		return "", false
	}
	if !now.Before(tok.until) {
		delete(t.tokens, key)
		return "", false
	}
	return tok.value, true
}

// put keeps tok under key, and forgets every token expired at now.
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

// drop forgets key's token.
func (t *tokenCache) drop(key tokenKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tokens, key)
}

// dropKey forgets every token of the key whose row id is id: it was
// revoked.
func (t *tokenCache) dropKey(id int64, sealed []byte) {
	digest := sha256.Sum256(sealed)
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.tokens {
		if k.id == id && k.sealed == digest {
			delete(t.tokens, k)
		}
	}
}

// tokenRefusal is the token endpoint's OAuth refusal: { "error",
// "error_description" }. The description is read only to tell a refused
// subject from a refused key (subjectRefused), and never logged, wrapped or
// kept.
type tokenRefusal struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// subjectDescriptions are the invalid_grant descriptions, lower-cased, of a
// subject Google refuses — an address it does not know, a suspended user, a
// subject not an address — rather than the key.
var subjectDescriptions = []string{
	"invalid email or user id",
	"not a valid email",
	"invalid impersonation",
}

// subjectRefused reports whether an invalid_grant's description names the
// subject, not the key.
func subjectRefused(description string) bool {
	d := strings.ToLower(description)
	return slices.ContainsFunc(subjectDescriptions, func(s string) bool { return strings.Contains(d, s) })
}

// fetchToken asks the token endpoint for a token signed with row's key, for
// scopes as subject, through the guarded client, retrying a 429 or a 5xx
// once. The key is opened for this one request and never held open.
// golang.org/x/oauth2's own errors carry Google's words: they are read for
// the OAuth error code, the status and whether an invalid_grant names the
// subject (errSubjectInvalid) or the key (errCredentialInvalid), and never
// logged, wrapped or kept.
func (c *googleClient) fetchToken(ctx context.Context, row store.GoogleCredential, subject string, scopes []string) (cachedToken, error) {
	if c.box == nil {
		return cachedToken{}, errors.New("google: no secrets box to open the key with")
	}
	pem, err := c.box.Open(serviceAccountKeyPurpose, row.Sealed)
	if err != nil {
		// A key sealed under another APP_SECRET cannot be used here: as
		// good as refused.
		return cachedToken{}, fmt.Errorf("%w: the sealed key does not open", errCredentialInvalid)
	}
	cfg := &jwt.Config{
		Email:        deref(row.ClientEmail),
		PrivateKey:   pem,
		PrivateKeyID: deref(row.PrivateKeyID),
		Scopes:       scopes,
		Subject:      subject,
		TokenURL:     tokenURL,
	}
	hctx := context.WithValue(ctx, oauth2.HTTPClient, c.http)
	retried := false
	for attempt := 1; ; attempt++ {
		tok, err := cfg.TokenSource(hctx).Token()
		if err == nil {
			return c.cached(tok), nil
		}
		if ctx.Err() != nil {
			return cachedToken{}, ctx.Err()
		}
		var re *oauth2.RetrieveError
		if !errors.As(err, &re) {
			// A transport failure, or an answer that is not a token:
			// oauth2's text names the URL at most, never Google's words.
			return cachedToken{}, fmt.Errorf("%w: the token endpoint did not answer", errGoogleUnreachable)
		}
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		var body tokenRefusal
		_ = json.Unmarshal(re.Body, &body)
		code := body.Error
		if code == "" {
			code = re.ErrorCode
		}
		c.logger.DebugContext(ctx, "google: the token endpoint refused a token", "status", status, "error", code, "credential", row.ID)
		switch {
		case code == "invalid_grant" && subjectRefused(body.Description):
			return cachedToken{}, errSubjectInvalid
		case code == "invalid_grant" || code == "invalid_client":
			return cachedToken{}, errCredentialInvalid
		case code == "unauthorized_client" || code == "access_denied":
			return cachedToken{}, errNotDelegated
		case (status == http.StatusTooManyRequests || status >= http.StatusInternalServerError) && !retried:
			retried = true
			if err := c.pause(ctx, attempt, serverErrorDelay); err != nil {
				return cachedToken{}, err
			}
		case status == http.StatusTooManyRequests:
			return cachedToken{}, errThrottled
		default:
			return cachedToken{}, fmt.Errorf("%w: %w", errGoogleUnreachable, &googleError{Status: status, Reason: code})
		}
	}
}

// cached is tok as the cache keeps it: its value, and until read from the
// answer's expires_in on the module's clock — not from tok.Expiry, which
// oauth2 computed on the wall clock — less tokenEarlyExpiry.
func (c *googleClient) cached(tok *oauth2.Token) cachedToken {
	lifetime := time.Duration(0)
	switch v := tok.Extra("expires_in").(type) {
	case float64:
		lifetime = time.Duration(v) * time.Second
	case json.Number:
		if n, err := v.Int64(); err == nil {
			lifetime = time.Duration(n) * time.Second
		}
	}
	return cachedToken{value: tok.AccessToken, until: c.now().Add(lifetime - tokenEarlyExpiry)}
}

// deref is *s, "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
