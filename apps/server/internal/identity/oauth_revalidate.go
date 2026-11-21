package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the revalidation of a client's metadata document at a refresh
// (OAuth connections design A3): once the cached document has expired, a
// refresh of one of its connections fetches it again, in the background,
// after the refresh has been answered. A document that is read and no longer
// holds — it names another client_id, breaks a rule of C6, or no longer
// lists the connection's redirect URI — revokes the connection
// (client_document_changed). Anything that keeps Vantigo from reading the
// document — the host unreachable, a timeout, a status other than 200, a
// redirect, a body over 16 KiB — is transient and changes nothing; the next
// refresh after the cache has expired tries again.
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
	// document no longer holds (A3).
	revokeReasonClientDocumentChanged = "client_document_changed"
)

// revalidateAfterRefresh starts c's client document's revalidation when the
// cached document has expired (A3). It reads the cache on the pool and
// returns at once; the fetch runs in its own goroutine with a context that
// outlives the request.
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
	if a.revalidating.Add(1) > maxOAuthRevalidations {
		a.revalidating.Add(-1)
		return
	}
	a.revalidations.Add(1)
	bg := context.WithoutCancel(ctx)
	go func() {
		defer a.revalidations.Done()
		defer a.revalidating.Add(-1)
		fetchCtx, cancel := context.WithTimeout(bg, 2*oauthDocumentBudget)
		defer cancel()
		a.revalidate(fetchCtx, c)
	}()
}

// WaitOAuthRevalidations waits for every revalidation this Access started to
// finish. It is for tests, which judge what a revalidation did.
func (a *Access) WaitOAuthRevalidations() { a.revalidations.Wait() }

// revalidate fetches c's client document again and acts on what it finds:
// the document cached for its max-age when it holds for c, the connection
// revoked when it was read and does not, nothing when it could not be read.
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
	body, maxAge, err := f.fetch(ctx, u.String())
	if err != nil {
		return // transient
	}
	client, err := acceptOAuthDocument(c.ClientID, host, body)
	if err == nil && !slices.ContainsFunc(client.redirectURIs, func(registered string) bool { return redirectMatches(registered, c.RedirectUri) }) {
		err = clientRefused("The client's metadata document no longer lists the connection's redirect URI.")
	}
	now := a.now()
	if err != nil {
		if rerr := db.WithTx(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			return a.revokeConnection(ctx, store.New(tx), nil, c.ID, nil, oauthSystem, revokeReasonClientDocumentChanged, now)
		}); rerr != nil {
			if a.log != nil {
				a.log.ErrorContext(ctx, "identity: could not revoke a connection whose client's document changed", "connection", c.ID, "error", rerr)
			}
			return
		}
		if a.log != nil {
			a.log.InfoContext(ctx, "identity: a connection was revoked: its client's document no longer holds", "connection", c.ID, "reason", err.Error())
		}
		return
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, body); err != nil {
		return // accepted, so valid JSON: cannot happen
	}
	if err := a.q.PutOAuthClientDocument(ctx, store.PutOAuthClientDocumentParams{
		ClientID: c.ClientID, Document: compact.Bytes(), Now: now, ExpiresAt: now.Add(maxAge),
	}); err != nil && a.log != nil {
		a.log.WarnContext(ctx, "identity: could not keep a revalidated client document", "connection", c.ID, "error", err)
	}
}
