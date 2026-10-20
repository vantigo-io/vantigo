package entra

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the application's client secrets (Entra design D11,
// credentials in the plural; D15 step 2): each sealed under
// entra/client-secret at once and never answered, logged or audited; the
// newest unrevoked one whose expiry is ahead used, a refused one marked
// failed and tried after the others; revoked, never deleted. Rotation is
// adding the new one and revoking the old.

const (
	// clientSecretPurpose is the secrets box purpose a client secret is
	// sealed under.
	clientSecretPurpose = "entra/client-secret"
	// credentialKindClientSecret is the one kind so far; a certificate is
	// the natural second.
	credentialKindClientSecret = "client_secret"
	// The request's bounds, in characters, and the longest lifetime the
	// portal gives a secret.
	maxCredentialLabel          = 100
	maxCredentialSecret         = 1000
	maxCredentialLifetimeMonths = 24
	// credentialUsedInterval is how often, at most, a secret's last_used_at
	// is written.
	credentialUsedInterval = time.Minute
)

// usableCredentials are the secrets a token may be asked with at now,
// unrevoked and unexpired, in the order they are tried: newest first, and a
// secret the tenant refused since it last worked after every other.
func usableCredentials(rows []store.EntraCredential, now time.Time) []store.EntraCredential {
	var out []store.EntraCredential
	for _, r := range rows {
		if r.RevokedAt == nil && r.ExpiresAt.After(now) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b store.EntraCredential) int {
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

// failedSinceUsed is a secret the tenant refused after it last fetched a
// token, or never let fetch one.
func failedSinceUsed(r store.EntraCredential) bool {
	return r.LastFailedAt != nil && (r.LastUsedAt == nil || r.LastFailedAt.After(*r.LastUsedAt))
}

// credentialFor is the credential a call to the tenant is made with: the
// first usable secret (usableCredentials) a token is found or fetched for.
// A secret the tenant refuses is marked failed and the next is tried; when
// none is left the connection is set failing/credential_invalid and the
// answer is errNoCredential. errNotConfigured when the connection names no
// tenant or client; errTenantUnreachable (wrapped) when the token endpoint
// could not be asked, which marks nothing. q must be on the pool, never a
// transaction: the token endpoint may be called.
func credentialFor(ctx context.Context, q *store.Queries, c *graphClient) (graphCredential, error) {
	return pickCredential(ctx, q, c, false)
}

// pickCredential is credentialFor; fresh fetches a new token even when one
// is cached, as the connection test does, so a consent granted since is
// read.
func pickCredential(ctx context.Context, q *store.Queries, c *graphClient, fresh bool) (graphCredential, error) {
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return graphCredential{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	if conn.TenantID == nil || conn.ClientID == nil {
		return graphCredential{}, errNotConfigured
	}
	rows, err := q.ListCredentials(ctx)
	if err != nil {
		return graphCredential{}, fmt.Errorf("entra: read the secrets: %w", err)
	}
	for _, row := range usableCredentials(rows, c.now()) {
		cred := graphCredential{id: row.ID, tenantID: *conn.TenantID, clientID: *conn.ClientID, sealed: row.Sealed}
		tok, fetched, err := c.tokenFor(ctx, cred, fresh)
		switch {
		case err == nil:
			if fetched {
				now := c.now()
				if err := q.MarkCredentialUsed(ctx, store.MarkCredentialUsedParams{Now: now, ID: row.ID, UsedBefore: now.Add(-credentialUsedInterval)}); err != nil {
					return graphCredential{}, fmt.Errorf("entra: mark the secret used: %w", err)
				}
			}
			cred.roles, cred.oid = tok.roles, tok.oid
			return cred, nil
		case errors.Is(err, errCredentialInvalid):
			if err := q.MarkCredentialFailed(ctx, store.MarkCredentialFailedParams{Now: c.now(), ID: row.ID}); err != nil {
				return graphCredential{}, fmt.Errorf("entra: mark the secret failed: %w", err)
			}
		default:
			return graphCredential{}, err
		}
	}
	if conn.Status != contracts.ProviderFailing || conn.StatusReason == nil || *conn.StatusReason != reasonCredentialInvalid {
		reason := reasonCredentialInvalid
		if _, err := q.SetConnectionStatus(ctx, store.SetConnectionStatusParams{
			Status: contracts.ProviderFailing, StatusReason: &reason, Now: c.now(),
		}); err != nil {
			return graphCredential{}, fmt.Errorf("entra: record the connection failing: %w", err)
		}
	}
	return graphCredential{}, errNoCredential
}

// reasonFor is the status reason a failure to reach the tenant reads as:
// credential_invalid when no secret fetched a token (or the connection is
// not set up), tenant_unreachable otherwise.
func reasonFor(err error) string {
	if errors.Is(err, errNoCredential) || errors.Is(err, errCredentialInvalid) || errors.Is(err, errNotConfigured) {
		return reasonCredentialInvalid
	}
	return reasonTenantUnreachable
}

// credentialBody is a secret as the contract answers it — never its value.
func credentialBody(r store.EntraCredential, inUse bool) gen.EntraCredential {
	return gen.EntraCredential{
		Id: r.ID, Kind: gen.EntraCredentialKind(r.Kind), Label: r.Label,
		ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
		LastUsedAt: r.LastUsedAt, LastFailedAt: r.LastFailedAt, RevokedAt: r.RevokedAt,
		InUse: inUse,
	}
}

// credentialsBody is every secret, newest first, the one credentialFor
// tries first marked in use.
func credentialsBody(rows []store.EntraCredential, now time.Time) []gen.EntraCredential {
	inUse := int64(0)
	if usable := usableCredentials(rows, now); len(usable) > 0 {
		inUse = usable[0].ID
	}
	out := make([]gen.EntraCredential, 0, len(rows))
	for _, r := range rows {
		out = append(out, credentialBody(r, r.ID == inUse))
	}
	return out
}

// credentialCodes are a secret's event codes: its id, its expiry and its
// label — never its value.
func credentialCodes(r store.EntraCredential) []string {
	codes := []string{"credential:" + strconv.FormatInt(r.ID, 10), "expires_at:" + r.ExpiresAt.UTC().Format(time.RFC3339)}
	if r.Label != nil {
		codes = append(codes, "label:"+*r.Label)
	}
	return codes
}

// PostEntraConnectionCredentials adds a client secret (step 2): kind
// client_secret, a label of at most 100 characters, the value 1 to 1000
// characters, an expiry ahead and at most 24 months away. The value is
// sealed at once; the answer, the log and the event carry the label and the
// dates only. (POST /api/v1/entra/connection/credentials)
func (s *server) PostEntraConnectionCredentials(ctx context.Context, req gen.PostEntraConnectionCredentialsRequestObject) (gen.PostEntraConnectionCredentialsResponseObject, error) {
	var in gen.EntraCredentialRequest
	if req.Body != nil {
		in = *req.Body
	}
	now := s.deps.Clock()
	var f fieldErrors
	if !in.Kind.Valid() {
		f.add("kind", "The kind is client_secret.")
	}
	var label *string
	if in.Label != nil {
		if l := strings.TrimSpace(*in.Label); l != "" {
			if utf8.RuneCountInString(l) > maxCredentialLabel {
				f.add("label", fmt.Sprintf("The label is at most %d characters.", maxCredentialLabel))
			}
			label = &l
		}
	}
	secret := strings.TrimSpace(in.Secret)
	switch n := utf8.RuneCountInString(secret); {
	case n == 0:
		f.add("secret", "The secret's value is required.")
	case n > maxCredentialSecret:
		f.add("secret", fmt.Sprintf("The secret's value is at most %d characters.", maxCredentialSecret))
	}
	switch {
	case !in.ExpiresAt.After(now):
		f.add("expiresAt", "The expiry is in the future.")
	case in.ExpiresAt.After(now.AddDate(0, maxCredentialLifetimeMonths, 0)):
		f.add("expiresAt", fmt.Sprintf("The expiry is at most %d months ahead.", maxCredentialLifetimeMonths))
	}
	if f.failed() {
		return gen.PostEntraConnectionCredentials400ApplicationProblemPlusJSONResponse(invalid("Invalid secret", f)), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.Secrets == nil {
		return nil, errors.New("entra: no secrets box to seal the client secret with")
	}
	sealed, err := s.deps.Secrets.Seal(clientSecretPurpose, []byte(secret))
	if err != nil {
		return nil, fmt.Errorf("entra: seal the client secret: %w", err)
	}
	var answer gen.EntraCredential
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.InsertCredential(ctx, store.InsertCredentialParams{
			Kind: credentialKindClientSecret, Label: label, Sealed: sealed,
			ExpiresAt: in.ExpiresAt, Now: now, CreatedByUserID: actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("entra: add the secret: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionCredentialAdded, nil, "", credentialCodes(row)); err != nil {
			return err
		}
		rows, err := q.ListCredentials(ctx)
		if err != nil {
			return fmt.Errorf("entra: read the secrets: %w", err)
		}
		for _, b := range credentialsBody(rows, now) {
			if b.Id == row.ID {
				answer = b
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.PostEntraConnectionCredentials201JSONResponse(answer), nil
}

// DeleteEntraConnectionCredentialsById revokes a secret: kept, marked
// revoked, never used again, its cached token forgotten; nothing changes in
// the tenant. The only valid secret is refused credential_in_use while the
// module keeps accounts, under the connection's lock so two revokes cannot
// both pass the check. A secret already revoked answers as it is.
// (DELETE /api/v1/entra/connection/credentials/{id})
func (s *server) DeleteEntraConnectionCredentialsById(ctx context.Context, req gen.DeleteEntraConnectionCredentialsByIdRequestObject) (gen.DeleteEntraConnectionCredentialsByIdResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var (
		answer  gen.EntraCredential
		found   bool
		revoked *graphCredential
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("entra: lock the connection: %w", err)
		}
		rows, err := q.ListCredentials(ctx)
		if err != nil {
			return fmt.Errorf("entra: read the secrets: %w", err)
		}
		i := slices.IndexFunc(rows, func(r store.EntraCredential) bool { return r.ID == req.Id })
		if i < 0 {
			return nil
		}
		found = true
		row := rows[i]
		if row.RevokedAt != nil {
			answer = credentialBody(row, false)
			return nil
		}
		usable := usableCredentials(rows, now)
		if len(usable) == 1 && usable[0].ID == row.ID {
			counts, err := q.CountAccounts(ctx)
			if err != nil {
				return fmt.Errorf("entra: count the accounts: %w", err)
			}
			if counts.Total > 0 {
				return refusal{credentialInUse()}
			}
		}
		updated, err := q.RevokeCredential(ctx, store.RevokeCredentialParams{Now: now, ID: row.ID})
		if err != nil {
			return fmt.Errorf("entra: revoke the secret: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionCredentialRevoked, nil, "", credentialCodes(updated)); err != nil {
			return err
		}
		answer = credentialBody(updated, false)
		if conn.TenantID != nil && conn.ClientID != nil {
			revoked = &graphCredential{id: row.ID, tenantID: *conn.TenantID, clientID: *conn.ClientID, sealed: row.Sealed}
		}
		return nil
	})
	var r refusal
	switch {
	case errors.As(err, &r):
		return gen.DeleteEntraConnectionCredentialsById409ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	case !found:
		return gen.DeleteEntraConnectionCredentialsById404Response{}, nil
	}
	if revoked != nil {
		s.client.tokens.drop(*revoked)
	}
	return gen.DeleteEntraConnectionCredentialsById200JSONResponse(answer), nil
}

// refusal carries a problem out of a transaction, which it rolls back; the
// handler answers the problem with the status it names.
type refusal struct {
	problem gen.EntraProblem
}

func (r refusal) Error() string {
	code := ""
	if r.problem.Code != nil {
		code = *r.problem.Code
	}
	status := 0
	if r.problem.Status != nil {
		status = int(*r.problem.Status)
	}
	return fmt.Sprintf("entra: refused %d %s", status, code)
}

// status is r's HTTP status.
func (r refusal) status() int {
	if r.problem.Status == nil {
		return http.StatusBadRequest
	}
	return int(*r.problem.Status)
}
