package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the revalidation of a client's metadata document at a refresh
// (OAuth connections design A3 as A28 amends it): once the cached document
// has expired, a refresh of one of its connections fetches it again, in the
// background, after the refresh has been answered.
//
// Only a document that speaks for the client can end its connection. It is
// terminal — the connection is revoked (client_document_changed) — when the
// answer is a JSON object that names a different, non-empty client_id, or
// names this client_id and breaks a rule of C6 or no longer lists the
// connection's redirect URI. Anything else is transient and changes
// nothing, neither revoked nor cached: a failure to read (the host
// unreachable, a timeout, a status other than 200, a redirect, a body over
// 16 KiB), a 200 whose Content-Type is not application/json or +json, a body
// that is not a JSON object, or an object with no client_id. A captive page,
// a CDN's error body or a maintenance page is not the client changing its
// mind.
//
// No fetch happens while a transaction or a pooled connection is held: the
// cache is read on the pool before the goroutine starts, the fetch holds
// nothing, and the revocation or the cache write each take one connection
// afterwards.

const (
	// maxOAuthRevalidations bounds the revalidations running at once in this
	// process; a refresh that finds them all busy starts none, and a later
	// refresh tries again.
	maxOAuthRevalidations = 8
	// revokeReasonClientDocumentChanged ends a connection whose client's
	// document no longer holds (A3, A28).
	revokeReasonClientDocumentChanged = "client_document_changed"
	// revalidationShutdownWait is how long the request log's close waits for
	// the revalidations running.
	revalidationShutdownWait = 5 * time.Second
)

// revalidationVerdict is what a revalidation found.
type revalidationVerdict int

const (
	revalidationTransient revalidationVerdict = iota
	revalidationHolds
	revalidationTerminal
)

// judgeRevalidation classifies a document fetched again for c (A28) and,
// when it holds, answers it compacted for the cache.
func judgeRevalidation(c store.IdentityOauthConnection, host, contentType string, body []byte) (revalidationVerdict, []byte) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return revalidationTransient, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return revalidationTransient, nil
	}
	var named string
	if id, ok := raw["client_id"]; !ok || json.Unmarshal(id, &named) != nil || named == "" {
		return revalidationTransient, nil
	}
	if named != c.ClientID {
		return revalidationTerminal, nil
	}
	client, err := acceptOAuthDocument(c.ClientID, host, body)
	if err != nil || !slices.ContainsFunc(client.redirectURIs, func(registered string) bool { return redirectMatches(registered, c.RedirectUri) }) {
		return revalidationTerminal, nil
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, body); err != nil {
		return revalidationTransient, nil // accepted, so valid JSON: cannot happen
	}
	return revalidationHolds, compact.Bytes()
}

// revalidateAfterRefresh starts c's client document's revalidation when the
// cached document has expired (A3). It reads the cache on the pool and
// returns at once; the fetch runs in its own goroutine with a context that
// outlives the request. At most maxOAuthRevalidations run at once, and at
// most one per client host.
func (a *Access) revalidateAfterRefresh(ctx context.Context, c store.IdentityOauthConnection) {
	if c.ClientKind != oauthClientKindMetadata {
		return
	}
	_, err := a.q.GetOAuthClientDocument(ctx, store.GetOAuthClientDocumentParams{ClientID: c.ClientID, Now: a.now()})
	if err == nil {
		return // still fresh
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		if a.log != nil {
			a.log.WarnContext(ctx, "identity: could not read a client's document for its revalidation", "connection", c.ID, "error", err)
		}
		return
	}
	if !a.claimRevalidation(c.ClientHost) {
		return
	}
	a.revalidations.Add(1)
	bg := context.WithoutCancel(ctx)
	go func() {
		defer a.revalidations.Done()
		defer a.releaseRevalidation(c.ClientHost)
		defer func() {
			if v := recover(); v != nil && a.log != nil {
				a.log.ErrorContext(bg, "identity: a client document's revalidation panicked", "connection", c.ID, "panic", fmt.Sprint(v))
			}
		}()
		fetchCtx, cancel := context.WithTimeout(bg, 2*oauthDocumentBudget)
		defer cancel()
		a.revalidate(fetchCtx, c)
	}()
}

// claimRevalidation takes one of the process's slots for host, or reports
// false: all are busy (logged), or host has one already.
func (a *Access) claimRevalidation(host string) bool {
	a.revalidatingMu.Lock()
	defer a.revalidatingMu.Unlock()
	if a.revalidating == nil {
		a.revalidating = map[string]bool{}
	}
	if a.revalidating[host] {
		return false
	}
	if len(a.revalidating) >= maxOAuthRevalidations {
		if a.log != nil {
			a.log.Warn("identity: every client document revalidation slot is busy; this refresh starts none", "slots", maxOAuthRevalidations)
		}
		return false
	}
	a.revalidating[host] = true
	return true
}

func (a *Access) releaseRevalidation(host string) {
	a.revalidatingMu.Lock()
	defer a.revalidatingMu.Unlock()
	delete(a.revalidating, host)
}

// WaitOAuthRevalidations waits for every revalidation this Access started to
// finish. Tests wait for it to judge what a revalidation did; the request
// log's close waits for it, bounded, at shutdown.
func (a *Access) WaitOAuthRevalidations() { a.revalidations.Wait() }

// waitRevalidations waits for the revalidations running, at most d.
func (a *Access) waitRevalidations(d time.Duration) {
	done := make(chan struct{})
	go func() {
		a.revalidations.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// revalidate fetches c's client document again and acts on the verdict: the
// document cached for its max-age when it holds for c, the connection
// revoked when it is terminal, nothing when it is transient.
func (a *Access) revalidate(ctx context.Context, c store.IdentityOauthConnection) {
	u, err := oauthClientIDURL(c.ClientID, a.cfg.AppHostname, a.cfg.PublicHostname)
	if err != nil {
		return // held at consent; nothing to fetch now
	}
	host := strings.ToLower(u.Hostname())
	f := a.oauthFetcherFor()
	if err := f.claim(c.ClientID, a.now()); err != nil {
		return // fetched moments ago
	}
	body, maxAge, contentType, err := f.fetchDocument(ctx, u.String())
	if err != nil {
		return // transient: not read
	}
	verdict, document := judgeRevalidation(c, host, contentType, body)
	now := a.now()
	switch verdict {
	case revalidationTransient:
		return
	case revalidationTerminal:
		if err := db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return a.revokeConnection(ctx, store.New(tx), nil, c.ID, nil, oauthSystem, revokeReasonClientDocumentChanged, now)
		}); err != nil {
			if a.log != nil {
				a.log.ErrorContext(ctx, "identity: could not revoke a connection whose client's document changed", "connection", c.ID, "error", err)
			}
			return
		}
		if a.log != nil {
			a.log.InfoContext(ctx, "identity: a connection was revoked: its client's document no longer holds", "connection", c.ID)
		}
	case revalidationHolds:
		if err := a.q.PutOAuthClientDocument(ctx, store.PutOAuthClientDocumentParams{
			ClientID: c.ClientID, Document: document, Now: now, ExpiresAt: now.Add(maxAge),
		}); err != nil && a.log != nil {
			a.log.WarnContext(ctx, "identity: could not keep a revalidated client document", "connection", c.ID, "error", err)
		}
	}
}
