package google

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the person's own access token (G25, G27, G28): cached in the
// process by the row's revision and refreshed with its sealed refresh token
// and the web client's secret. Every write is guarded by the revision the
// call read: a refresh that lost a race keeps the winner's token and answers
// its own access token all the same; a refusal of a token a newer connect
// has replaced changes nothing. Google's refusals the person must answer
// move the row to reconnect with their code; the web client's own are the
// connection's personal_last_error; a 5xx, a 429 or no answer is transient
// and leaves the row alone. Before any token is answered the account is
// checked again (on use, G27).
//
// The mailbox calendar (E, mailbox.go) and personal sending (G,
// personal_mailbox.go) take their tokens from personalTokens.source. Nothing here logs, audits or
// answers a token or the secret.

// errWebClient: the web client is not set up, or Google refused it on a
// refresh (invalid_client, unauthorized_client) — the Owner's, not the
// person's.
var errWebClient = errors.New("google: the web client is not set up or Google refused it")

// needsPerson is a call the person's connection cannot serve until they
// connect again: reason is not_connected, closing, or the reconnect's code.
func needsPerson(reason string) error {
	return &contracts.ConnectionNeededError{Reason: reason}
}

// The connection's error codes (G28): what a reconnect says the person must
// do.
const (
	personalCodeExpired        = "expired"
	personalCodeBlocked        = "blocked"
	personalCodeConsentMissing = "consent_missing"
	personalCodeSignInAgain    = "sign_in_again"
)

// personalRefusalCode reads the token endpoint's refusal of a refresh: the
// person's error code and true for one only they can answer; the OAuth error
// and false for the web client's own (clientRefusals); "" and false for a
// transient one — a 5xx, a 429, temporarily_unavailable, or an answer that
// is no refusal at all.
func personalRefusalCode(status int, a tokenAnswer) (string, bool) {
	switch {
	case slices.Contains(clientRefusals, a.Error):
		return a.Error, false
	case status >= http.StatusInternalServerError || status == http.StatusTooManyRequests,
		a.Error == "temporarily_unavailable", a.Error == "":
		return "", false
	case a.Error == "invalid_grant":
		// Revoked, expired, unused for six months, or a password change
		// revoking a Gmail scope's token.
		return personalCodeExpired, true
	case a.Error == "admin_policy_enforced", a.Error == "org_internal":
		return personalCodeBlocked, true
	case a.Error == "invalid_scope":
		return personalCodeConsentMissing, true
	}
	return personalCodeSignInAgain, true
}

// personalKey names one connection's access token: its Google id, the row's
// revision, the web client, and a digest of the sealed refresh token — so a
// token outlives no write to the row, and two test databases holding one
// Google id never share one.
type personalKey struct {
	gid      string
	revision int64
	clientID string
	sealed   [sha256.Size]byte
}

// personalCache is the process's personal access tokens, under a mutex; an
// expired one is dropped when another is kept.
type personalCache struct {
	mu     sync.Mutex
	tokens map[personalKey]cachedToken
}

// processPersonal is the cache every token source of the process shares.
var processPersonal = &personalCache{}

func (d *personalCache) get(key personalKey, now time.Time) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tok, ok := d.tokens[key]
	if !ok || !now.Before(tok.until) {
		delete(d.tokens, key)
		return "", false
	}
	return tok.value, true
}

func (d *personalCache) put(key personalKey, tok cachedToken, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tokens == nil {
		d.tokens = map[personalKey]cachedToken{}
	}
	for k, old := range d.tokens {
		if !now.Before(old.until) {
			delete(d.tokens, k)
		}
	}
	d.tokens[key] = tok
}

func (d *personalCache) drop(key personalKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.tokens, key)
}

// personalTokens is the token source's home: the module's client, its
// queries on the pool (never a transaction: the token endpoint may be
// called) and the pool its writes take a transaction on.
type personalTokens struct {
	c    *googleClient
	q    *store.Queries
	pool db.TxBeginner
}

// newPersonalTokens is the token source over d.
func newPersonalTokens(d module.Deps) *personalTokens {
	return &personalTokens{c: newGoogleClient(d), q: store.New(d.Pool), pool: d.Pool}
}

// personalIssued is a token answered with the key it is cached under and the
// revision of the row it came from.
type personalIssued struct {
	value    string
	key      personalKey
	revision int64
}

// issue is the access token for gid's connection, for feature (when not
// empty, the row's granted scopes must allow it). removal asks for a removal,
// which a closing connection, or any connection while the switch is off,
// still allows while it holds its token.
func (t *personalTokens) issue(ctx context.Context, gid, feature string, removal bool) (personalIssued, error) {
	row, err := t.q.GetPersonal(ctx, gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return personalIssued{}, needsPerson(contracts.ConnectionNotConnected)
	}
	if err != nil {
		return personalIssued{}, fmt.Errorf("google: read the personal connection: %w", err)
	}
	conn, creds, err := readFooting(ctx, t.q)
	if err != nil {
		return personalIssued{}, err
	}
	switch {
	case row.State == personalReconnect:
		reason := personalCodeSignInAgain
		if row.ErrorCode != nil {
			reason = *row.ErrorCode
		}
		return personalIssued{}, needsPerson(reason)
	case row.RefreshSealed == nil, row.State == personalClosing && !removal, !conn.PersonalConnection && !removal:
		return personalIssued{}, needsPerson(contracts.ConnectionClosingReason)
	case row.State == personalActive:
		if err := t.checkAccount(ctx, row); err != nil {
			return personalIssued{}, err
		}
		if feature != "" && !hasScopes(row.Scopes, personalFeatures[feature]) {
			return personalIssued{}, needsPerson(personalCodeConsentMissing)
		}
	}
	secret := liveSecret(creds)
	if conn.OauthClientID == nil || secret == nil {
		return personalIssued{}, errWebClient
	}
	key := personalKey{gid: gid, revision: row.Revision, clientID: *conn.OauthClientID, sealed: sha256.Sum256(row.RefreshSealed)}
	now := t.c.now()
	if value, ok := processPersonal.get(key, now); ok {
		if err := t.q.TouchPersonalUsed(ctx, store.TouchPersonalUsedParams{Now: now, GoogleUserID: gid}); err != nil {
			return personalIssued{}, fmt.Errorf("google: record the personal connection used: %w", err)
		}
		return personalIssued{value: value, key: key, revision: row.Revision}, nil
	}
	return t.refresh(ctx, row, key, *secret, feature)
}

// checkAccount is the connection's check on use (G27): the account must
// still be one the module keeps trusted and enabled, else the connection
// closes at the revision row was read at, its token forgotten, and the call
// needs its person.
func (t *personalTokens) checkAccount(ctx context.Context, row store.GooglePersonalConnection) error {
	account, err := t.q.GetAccount(ctx, row.GoogleUserID)
	code := ""
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		code = personalClosedDeleted
	case err != nil:
		return fmt.Errorf("google: read the account: %w", err)
	case account.State == stateDeleted:
		code = personalClosedDeleted
	case !usableAccount(account):
		code = personalClosedDisabled
	}
	if code == "" {
		return nil
	}
	revision := row.Revision
	if err := closePersonalIn(ctx, t.pool, t.q, t.c.now(), contracts.Actor{System: systemActor}, row.GoogleUserID, &revision, true, code); err != nil {
		return err
	}
	t.c.logger.InfoContext(ctx, "google: a personal connection closed on use; its account is no longer usable",
		"google_user_id", row.GoogleUserID, "code", code)
	return needsPerson(contracts.ConnectionClosingReason)
}

// refresh redeems row's refresh token for an access token with the web
// client's secret, and stores what Google answered at the revision row was
// read at.
func (t *personalTokens) refresh(ctx context.Context, row store.GooglePersonalConnection, key personalKey, secretRow store.GoogleCredential, feature string) (personalIssued, error) {
	c := t.c
	if c.box == nil {
		return personalIssued{}, errors.New("google: no secrets box to open the refresh token with")
	}
	refreshToken, err := c.box.Open(personalRefreshPurpose, row.RefreshSealed)
	if err != nil {
		// Sealed under another APP_SECRET: only a new connect mends it.
		return personalIssued{}, t.reconnect(ctx, row, personalCodeSignInAgain)
	}
	secret, err := c.box.Open(oauthClientSecretPurpose, secretRow.Sealed)
	if err != nil {
		return personalIssued{}, fmt.Errorf("%w: the stored secret cannot be opened on this server", errWebClient)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {string(refreshToken)},
		"client_id":     {key.clientID},
		"client_secret": {string(secret)},
	}
	now := c.now()
	answer, status, err := c.postToken(ctx, form)
	if err != nil {
		return personalIssued{}, err
	}
	if status != http.StatusOK || answer.Error != "" || answer.AccessToken == "" {
		c.logger.InfoContext(ctx, "google: the token endpoint refused a personal connection's refresh",
			"google_user_id", row.GoogleUserID, "status", status, "code", answer.Error)
		code, person := personalRefusalCode(status, answer)
		switch {
		case person:
			return personalIssued{}, t.reconnect(ctx, row, code)
		case code != "":
			if err := t.q.SetPersonalLastError(ctx, store.SetPersonalLastErrorParams{Code: &code, Now: now}); err != nil {
				return personalIssued{}, fmt.Errorf("google: record the web client refused: %w", err)
			}
			return personalIssued{}, fmt.Errorf("%w: %s", errWebClient, code)
		default:
			return personalIssued{}, fmt.Errorf("%w: the token endpoint answered %d %s", errGoogleUnreachable, status, answer.Error)
		}
	}

	// The stored scopes are Google's answer, never assumed (U9): a refresh
	// that answers none leaves them; one that keeps no feature the row held
	// is a consent withdrawn.
	scopes := row.Scopes
	if answer.Scope != "" {
		scopes = grantedScopes(answer.Scope)
	}
	if len(featuresOf(scopes)) == 0 && len(featuresOf(row.Scopes)) > 0 {
		return personalIssued{}, t.reconnect(ctx, row, personalCodeConsentMissing)
	}
	var sealed []byte
	if answer.RefreshToken != "" {
		if sealed, err = c.box.Seal(personalRefreshPurpose, []byte(answer.RefreshToken)); err != nil {
			return personalIssued{}, fmt.Errorf("google: seal the refresh token: %w", err)
		}
	}
	tok := cachedToken{value: answer.AccessToken, until: now.Add(time.Duration(answer.ExpiresIn)*time.Second - tokenEarlyExpiry)}
	out := personalIssued{value: answer.AccessToken, key: key, revision: row.Revision}
	stored, err := t.q.StorePersonalRefresh(ctx, store.StorePersonalRefreshParams{
		RefreshSealed: sealed, Scopes: scopes, Now: now, GoogleUserID: row.GoogleUserID, Revision: row.Revision,
	})
	if err != nil {
		return personalIssued{}, fmt.Errorf("google: store the refresh: %w", err)
	}
	if stored > 0 {
		out.revision = row.Revision + 1
		digest := key.sealed
		if sealed != nil {
			digest = sha256.Sum256(sealed)
		}
		out.key = personalKey{gid: row.GoogleUserID, revision: out.revision, clientID: key.clientID, sealed: digest}
		if now.Before(tok.until) {
			processPersonal.put(out.key, tok, now)
		}
	}
	// A lost race keeps the winner's token, and this access token is as
	// valid as the winner's; it is not cached under a revision the row has
	// left.
	if feature != "" && !hasScopes(scopes, personalFeatures[feature]) {
		return personalIssued{}, needsPerson(personalCodeConsentMissing)
	}
	return out, nil
}

// reconnect moves row to reconnect with code at the revision it was read
// at, recording google.personal.reconnect_needed, and answers the call's
// error: needsPerson when it moved the row; needsPerson closing when the row
// is closing at that revision, its refused token forgotten so no removal
// tries it again; a transient failure when a newer connect, close or refusal
// had moved it first, which the call may retry against the row as it is now.
func (t *personalTokens) reconnect(ctx context.Context, row store.GooglePersonalConnection, code string) error {
	now := t.c.now()
	moved, forgotten := false, false
	err := db.WithTx(ctx, t.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := t.q.WithTx(tx)
		n, err := q.MarkPersonalReconnect(ctx, store.MarkPersonalReconnectParams{Code: &code, Now: now, GoogleUserID: row.GoogleUserID, Revision: row.Revision})
		if err != nil {
			return fmt.Errorf("google: record the personal connection's reconnect: %w", err)
		}
		if n == 0 {
			n, err := q.ForgetClosingPersonalRefresh(ctx, store.ForgetClosingPersonalRefreshParams{Now: now, GoogleUserID: row.GoogleUserID, Revision: row.Revision})
			if err != nil {
				return fmt.Errorf("google: forget the closing connection's refused token: %w", err)
			}
			forgotten = n > 0
			return nil
		}
		moved = true
		return recordEvent(ctx, q, now, contracts.Actor{System: systemActor}, actionPersonalReconnectNeeded, row.GoogleUserID, []string{code})
	})
	if err != nil {
		return err
	}
	if forgotten {
		t.c.logger.InfoContext(ctx, "google: a closing personal connection's token was refused and is forgotten",
			"google_user_id", row.GoogleUserID, "code", code)
		return needsPerson(contracts.ConnectionClosingReason)
	}
	if !moved {
		if current, err := t.q.GetPersonal(ctx, row.GoogleUserID); err == nil && current.State == personalReconnect && current.ErrorCode != nil {
			return needsPerson(*current.ErrorCode)
		}
		return fmt.Errorf("%w: the personal connection changed while its token was refused", errGoogleUnreachable)
	}
	t.c.logger.InfoContext(ctx, "google: a personal connection needs its person to reconnect",
		"google_user_id", row.GoogleUserID, "code", code)
	return needsPerson(code)
}

// unauthorized records Google's refusal of gid's token, issued from the row
// at revision, after the client fetched a fresh one (the calendar, E, and
// personal sending, G): the row read now, moved to reconnect (sign_in_again)
// when it is still at that revision, or its closing token forgotten,
// through reconnect; a row moved on since is a failure to retry.
func (t *personalTokens) unauthorized(ctx context.Context, gid string, revision int64) error {
	row, err := t.q.GetPersonal(ctx, gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return needsPerson(contracts.ConnectionNotConnected)
	}
	if err != nil {
		return fmt.Errorf("google: read the personal connection: %w", err)
	}
	if row.Revision != revision {
		return fmt.Errorf("%w: the personal connection changed while Google refused its token", errGoogleUnreachable)
	}
	return t.reconnect(ctx, row, personalCodeSignInAgain)
}

// source is gid's token for feature as a call's tokenSource (client.go):
// removal allows a closing connection's token for a removal.
func (t *personalTokens) source(gid, feature string, removal bool) *personalSource {
	return &personalSource{t: t, gid: gid, feature: feature, removal: removal}
}

// personalSource is one call's personal token: the token it last answered,
// which a 401 drops before the one retry.
type personalSource struct {
	t       *personalTokens
	gid     string
	feature string
	removal bool

	mu   sync.Mutex
	last personalIssued
}

func (s *personalSource) token(ctx context.Context) (string, error) {
	tok, err := s.t.issue(ctx, s.gid, s.feature, s.removal)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.last = tok
	s.mu.Unlock()
	return tok.value, nil
}

func (s *personalSource) dropped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	processPersonal.drop(s.last.key)
}

// lastRevision is the revision of the row the last token came from.
func (s *personalSource) lastRevision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last.revision
}

var _ tokenSource = (*personalSource)(nil)

// connectionChanges is what the mailbox calendar's Connections answers
// (delivery E, G30): the connections whose state moved after since, oldest
// first, at most contracts.ConnectionChangesMax, each by its Google id.
func connectionChanges(ctx context.Context, q *store.Queries, since time.Time) ([]contracts.ConnectionChange, error) {
	rows, err := q.ListPersonalChanges(ctx, store.ListPersonalChangesParams{Since: since, RowLimit: contracts.ConnectionChangesMax})
	if err != nil {
		return nil, fmt.Errorf("google: list the personal connections' changes: %w", err)
	}
	out := make([]contracts.ConnectionChange, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.ConnectionChange{Mailbox: r.GoogleUserID, State: r.State, At: r.ChangedAt})
	}
	return out, nil
}
