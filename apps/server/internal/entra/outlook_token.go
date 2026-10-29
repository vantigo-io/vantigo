package entra

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the person's delegated token (Outlook connection design M3,
// M5, M6; delivery A's plan reading 17): the access token for an account's
// connection, cached in the process by the row's revision and refreshed
// with its sealed refresh token, the client secret and cp1 declared. Every
// write is guarded by the revision the call read: a refresh that lost a
// race keeps the winner's token and answers its own access token all the
// same (both are valid at Microsoft); a refusal of a token a newer connect
// has replaced changes nothing. Microsoft's refusals the person must answer
// move the row to reconnect with their code; the client's own refusals are
// the credential's; a 5xx, a 429 or no answer is transient and leaves the
// row alone. A Graph call's continuous access evaluation claims challenge
// moves the row to reconnect with sign_in_again (delegatedSource).
//
// Every call of the mailbox calendar reaches it, through mailbox.go's
// mailboxSource. Nothing here logs, audits or answers a token, a code or
// the secret.

// needsPerson is a call the person's connection cannot serve until they
// connect again, as the contract answers it: reason is not_connected,
// closing, or the reconnect's error_code.
func needsPerson(reason string) error {
	return &contracts.ConnectionNeededError{Reason: reason}
}

// The reasons a connection needs its person besides a reconnect's code.
const (
	outlookNotConnected = contracts.ConnectionNotConnected
	outlookNeedsClosing = contracts.ConnectionClosingReason
)

// The connection's error codes (design M5): what a reconnect says the
// person must do.
const (
	outlookCodeExpired        = "expired"
	outlookCodeSignInAgain    = "sign_in_again"
	outlookCodeMFA            = "mfa"
	outlookCodeBlocked        = "blocked"
	outlookCodeConsentMissing = "consent_missing"
)

// outlookRefusals maps Microsoft's AADSTS codes to the connection's error
// code, read before the OAuth error, which Microsoft words variously for one
// cause.
var outlookRefusals = map[int]string{
	70008:  outlookCodeExpired,
	700082: outlookCodeExpired,
	50173:  outlookCodeExpired,
	70043:  outlookCodeSignInAgain,
	50132:  outlookCodeSignInAgain,
	50076:  outlookCodeMFA,
	50078:  outlookCodeMFA,
	50079:  outlookCodeMFA,
	53000:  outlookCodeBlocked,
	53003:  outlookCodeBlocked,
	65001:  outlookCodeConsentMissing,
}

// outlookRefusalCode reads the token endpoint's refusal of a refresh: the
// person's error code and true for one only they can answer; the OAuth
// error and false for the client's own refusal (credentialRefusals); "" and
// false for a transient one — a 5xx, a 429, temporarily_unavailable, or an
// answer that is no refusal at all.
func outlookRefusalCode(status int, a outlookTokenAnswer) (string, bool) {
	switch {
	case slices.Contains(credentialRefusals, a.Error):
		return a.Error, false
	case status >= http.StatusInternalServerError || status == http.StatusTooManyRequests,
		a.Error == "temporarily_unavailable", a.Error == "":
		return "", false
	}
	for _, c := range a.ErrorCodes {
		if code, ok := outlookRefusals[c]; ok {
			return code, true
		}
	}
	return outlookCodeSignInAgain, true
}

// openIDScopes are the scopes a connect asks besides its features' Graph
// scopes, which a refresh does not name again (offline_access aside).
var openIDScopes = []string{"openid", "profile", "email", "offline_access"}

// delegatedScope is the scope a refresh asks for the row's scopes: each
// Graph scope by its resource, then offline_access for the next refresh
// token.
func delegatedScope(scopes []string) string {
	var parts []string
	for _, s := range scopes {
		if !slices.Contains(openIDScopes, s) {
			parts = append(parts, graphResource+s)
		}
	}
	return strings.Join(append(parts, "offline_access"), " ")
}

// claimsChallenge reports whether a 401's WWW-Authenticate is a continuous
// access evaluation claims challenge: insufficient_claims with a claims
// value.
func claimsChallenge(h http.Header) bool {
	for _, v := range h.Values("WWW-Authenticate") {
		if strings.Contains(v, `error="insufficient_claims"`) && strings.Contains(v, `claims="`) {
			return true
		}
	}
	return false
}

// delegatedKey names one connection's access token: its object id, the
// row's revision and the client, and a digest of its sealed refresh token —
// so a token outlives no write to the row, and two test databases holding
// one object id never share one.
type delegatedKey struct {
	oid      uuid.UUID
	revision int64
	clientID uuid.UUID
	sealed   [sha256.Size]byte
}

// delegatedCache is the process's delegated access tokens, by key, under a
// mutex; an expired one is dropped when another is kept.
type delegatedCache struct {
	mu     sync.Mutex
	tokens map[delegatedKey]cachedToken
}

// processDelegated is the cache every token source of the process shares.
var processDelegated = &delegatedCache{}

func (d *delegatedCache) get(key delegatedKey, now time.Time) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tok, ok := d.tokens[key]
	if !ok || !now.Before(tok.until) {
		delete(d.tokens, key)
		return "", false
	}
	return tok.value, true
}

func (d *delegatedCache) put(key delegatedKey, tok cachedToken, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tokens == nil {
		d.tokens = map[delegatedKey]cachedToken{}
	}
	for k, old := range d.tokens {
		if !now.Before(old.until) {
			delete(d.tokens, k)
		}
	}
	d.tokens[key] = tok
}

func (d *delegatedCache) drop(key delegatedKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.tokens, key)
}

// outlookTokens is the token source's home: the module's Graph client, its
// queries on the pool (never a transaction: the token endpoint may be
// called) and the pool its writes take a transaction on.
type outlookTokens struct {
	c    *graphClient
	q    *store.Queries
	pool db.TxBeginner
}

// newOutlookTokens is the token source over d.
func newOutlookTokens(d module.Deps) *outlookTokens {
	return &outlookTokens{c: newGraphClient(d), q: store.New(d.Pool), pool: d.Pool}
}

// issued is a token answered with the key it is cached under and the
// revision and tenant of the row it came from.
type issued struct {
	value    string
	key      delegatedKey
	revision int64
	tenant   uuid.UUID
}

// token is the delegated access token for the account oid's connection:
// removal asks for a removal, which a closing connection still allows while
// it holds its token.
func (t *outlookTokens) token(ctx context.Context, oid uuid.UUID, removal bool) (string, error) {
	tok, err := t.issue(ctx, oid, removal)
	return tok.value, err
}

// issue is token with what a 401 needs to act on it. The switch off allows
// removals alone, whatever the row's state: the rows it closes may not be
// closed yet.
func (t *outlookTokens) issue(ctx context.Context, oid uuid.UUID, removal bool) (issued, error) {
	row, err := t.q.GetOutlookConnection(ctx, oid)
	if errors.Is(err, pgx.ErrNoRows) {
		return issued{}, needsPerson(outlookNotConnected)
	}
	if err != nil {
		return issued{}, fmt.Errorf("entra: read the Outlook connection: %w", err)
	}
	conn, err := t.q.GetConnection(ctx)
	if err != nil {
		return issued{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	switch {
	case row.State == outlookReconnect:
		reason := outlookCodeSignInAgain
		if row.ErrorCode != nil {
			reason = *row.ErrorCode
		}
		return issued{}, needsPerson(reason)
	case row.RefreshSealed == nil, row.State == outlookClosing && !removal, !conn.OutlookConnect && !removal:
		return issued{}, needsPerson(outlookNeedsClosing)
	case row.State == outlookActive:
		if err := t.checkAccount(ctx, row); err != nil {
			return issued{}, err
		}
	}
	if conn.ClientID == nil {
		return issued{}, errNotConfigured
	}
	key := delegatedKey{oid: oid, revision: row.Revision, clientID: *conn.ClientID, sealed: sha256.Sum256(row.RefreshSealed)}
	now := t.c.now()
	if value, ok := processDelegated.get(key, now); ok {
		if err := t.q.TouchOutlookUsed(ctx, store.TouchOutlookUsedParams{Now: now, ObjectID: oid}); err != nil {
			return issued{}, fmt.Errorf("entra: record the Outlook connection used: %w", err)
		}
		return issued{value: value, key: key, revision: row.Revision, tenant: row.TenantID}, nil
	}
	return t.refresh(ctx, row, key)
}

// checkAccount is the connection's check on use (design M6): the account
// must still be one the module keeps trusted and enabled, else the
// connection closes at the revision row was read at, its token forgotten —
// the account can no longer use it, and a leaver's removals are not this
// token's to make —, and the call needs its person.
func (t *outlookTokens) checkAccount(ctx context.Context, row store.EntraOutlookConnection) error {
	oid := row.ObjectID
	account, err := t.q.GetAccount(ctx, oid)
	code := ""
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		code = outlookClosedDeleted
	case err != nil:
		return fmt.Errorf("entra: read the account: %w", err)
	case account.State == stateDeleted:
		code = outlookClosedDeleted
	case !trusted(account) || account.State != stateEnabled:
		code = outlookClosedDisabled
	}
	if code == "" {
		return nil
	}
	revision := row.Revision
	if err := closeOutlookIn(ctx, t.pool, t.q, t.c.now(), outlookActor, oid, &revision, true, code); err != nil {
		return err
	}
	t.c.logger.InfoContext(ctx, "entra: an Outlook connection closed on use; its account is no longer enabled",
		"object_id", oid.String(), "code", code)
	return needsPerson(outlookNeedsClosing)
}

// refresh redeems row's refresh token for an access token and stores the
// new refresh token at the revision row was read at.
func (t *outlookTokens) refresh(ctx context.Context, row store.EntraOutlookConnection, key delegatedKey) (issued, error) {
	c := t.c
	cred, err := credentialFor(ctx, t.q, c)
	if err != nil {
		return issued{}, err
	}
	refreshToken, err := c.box.Open(outlookRefreshPurpose, row.RefreshSealed)
	if err != nil {
		// Sealed under another APP_SECRET: only a new connect mends it.
		return issued{}, t.reconnect(ctx, row, outlookCodeSignInAgain)
	}
	secret, err := c.box.Open(clientSecretPurpose, cred.sealed)
	if err != nil {
		return issued{}, fmt.Errorf("%w: the stored secret cannot be opened on this server", errCredentialInvalid)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {string(refreshToken)},
		"client_id":     {cred.clientID.String()},
		"client_secret": {string(secret)},
		"scope":         {delegatedScope(row.Scopes)},
		"claims":        {outlookClaims},
	}
	now := c.now()
	answer, status, err := c.postToken(ctx, cred.tenantID, form)
	if err != nil {
		if ctx.Err() != nil {
			return issued{}, ctx.Err()
		}
		return issued{}, err
	}
	if status != http.StatusOK || answer.Error != "" || answer.AccessToken == "" {
		c.logger.InfoContext(ctx, "entra: the token endpoint refused an Outlook connection's refresh",
			"object_id", row.ObjectID.String(), "status", status, "code", answer.Error, "codes", answer.ErrorCodes)
		code, person := outlookRefusalCode(status, answer)
		switch {
		case person:
			return issued{}, t.reconnect(ctx, row, code)
		case code != "":
			if err := t.q.MarkCredentialFailed(ctx, store.MarkCredentialFailedParams{Now: c.now(), ID: cred.id}); err != nil {
				return issued{}, fmt.Errorf("entra: mark the secret failed: %w", err)
			}
			return issued{}, fmt.Errorf("%w: %s", errCredentialInvalid, code)
		default:
			return issued{}, fmt.Errorf("%w: the token endpoint answered %d %s", errTenantUnreachable, status, answer.Error)
		}
	}

	tok := cachedToken{value: answer.AccessToken, until: now.Add(time.Duration(answer.ExpiresIn)*time.Second - tokenEarlyExpiry)}
	out := issued{value: answer.AccessToken, key: key, revision: row.Revision, tenant: row.TenantID}
	if answer.RefreshToken == "" {
		// No new refresh token: the stored one stays good.
		processDelegated.put(key, tok, now)
		return out, nil
	}
	sealed, err := c.box.Seal(outlookRefreshPurpose, []byte(answer.RefreshToken))
	if err != nil {
		return issued{}, fmt.Errorf("entra: seal the Outlook refresh token: %w", err)
	}
	scopes := row.Scopes
	if answer.Scope != "" {
		scopes = normaliseScopes(answer.Scope)
	}
	stored, err := t.q.StoreOutlookRefresh(ctx, store.StoreOutlookRefreshParams{
		RefreshSealed: sealed, Scopes: scopes, Now: now, ObjectID: row.ObjectID, Revision: row.Revision,
	})
	if err != nil {
		return issued{}, fmt.Errorf("entra: store the Outlook refresh token: %w", err)
	}
	if stored == 0 {
		// A lost race: the winner's token stays, and this access token is
		// as valid as the winner's. It is not cached under a revision the
		// row has left.
		return out, nil
	}
	out.revision = row.Revision + 1
	out.key = delegatedKey{oid: row.ObjectID, revision: out.revision, clientID: key.clientID, sealed: sha256.Sum256(sealed)}
	if now.Before(tok.until) {
		processDelegated.put(out.key, tok, now)
	}
	return out, nil
}

// reconnect moves row to reconnect with code at the revision it was read
// at, recording entra.outlook.reconnect_needed, and answers the call's
// error: needsPerson when it moved the row; needsPerson closing
// when the row is closing at that revision, its refused token forgotten so
// no removal tries it again; a transient failure when a newer connect,
// close or refusal had moved it first, which the call may retry against
// the row as it is now.
func (t *outlookTokens) reconnect(ctx context.Context, row store.EntraOutlookConnection, code string) error {
	now := t.c.now()
	moved, forgotten := false, false
	err := db.WithTx(ctx, t.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := t.q.WithTx(tx)
		n, err := q.MarkOutlookReconnect(ctx, store.MarkOutlookReconnectParams{Code: &code, Now: now, ObjectID: row.ObjectID, Revision: row.Revision})
		if err != nil {
			return fmt.Errorf("entra: record the Outlook connection's reconnect: %w", err)
		}
		if n == 0 {
			n, err := q.ForgetClosingOutlookRefresh(ctx, store.ForgetClosingOutlookRefreshParams{Now: now, ObjectID: row.ObjectID, Revision: row.Revision})
			if err != nil {
				return fmt.Errorf("entra: forget the closing Outlook connection's refused token: %w", err)
			}
			forgotten = n > 0
			return nil
		}
		moved = true
		oid := row.ObjectID
		return recordEvent(ctx, q, now, outlookActor, actionOutlookReconnect, &oid, accountUPN(ctx, q, oid), []string{code})
	})
	if err != nil {
		return err
	}
	if forgotten {
		t.c.logger.InfoContext(ctx, "entra: a closing Outlook connection's token was refused and is forgotten",
			"object_id", row.ObjectID.String(), "code", code)
		return needsPerson(outlookNeedsClosing)
	}
	if !moved {
		if current, err := t.q.GetOutlookConnection(ctx, row.ObjectID); err == nil && current.State == outlookReconnect && current.ErrorCode != nil {
			return needsPerson(*current.ErrorCode)
		}
		return fmt.Errorf("%w: the Outlook connection changed while its token was refused", errTenantUnreachable)
	}
	t.c.logger.InfoContext(ctx, "entra: an Outlook connection needs its person to reconnect",
		"object_id", row.ObjectID.String(), "code", code)
	return needsPerson(code)
}

// source is the delegated token for oid as a Graph call's tokenSource.
func (t *outlookTokens) source(oid uuid.UUID, removal bool) *delegatedSource {
	return &delegatedSource{t: t, oid: oid, removal: removal}
}

// delegatedSource is one call's delegated token (graph.go's tokenSource):
// the token it last answered, which a 401 drops, or — a claims challenge —
// turns into a reconnect.
type delegatedSource struct {
	t       *outlookTokens
	oid     uuid.UUID
	removal bool
	last    issued
}

func (s *delegatedSource) token(ctx context.Context) (string, error) {
	tok, err := s.t.issue(ctx, s.oid, s.removal)
	if err != nil {
		return "", err
	}
	s.last = tok
	return tok.value, nil
}

func (s *delegatedSource) dropped() { processDelegated.drop(s.last.key) }

func (s *delegatedSource) tenant() uuid.UUID { return s.last.tenant }

// challenged answers a claims challenge (design M5): the token dropped, the
// row moved to reconnect with sign_in_again at the revision the token came
// from — every CAE event, a disabled account, a password reset, a
// revocation or a risk, needs the person to sign in — and the call ended
// without a retry. Any other 401 is retried once, as for the application.
func (s *delegatedSource) challenged(ctx context.Context, header http.Header) error {
	if !claimsChallenge(header) {
		return nil
	}
	processDelegated.drop(s.last.key)
	return s.t.reconnect(ctx, store.EntraOutlookConnection{ObjectID: s.oid, Revision: s.last.revision}, outlookCodeSignInAgain)
}
