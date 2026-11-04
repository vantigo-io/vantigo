package google

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the credentials (Google Workspace design G13): the service
// account's keys and the Internal web client's secret. A key file is parsed,
// its private key sealed under google/service-account-key at once and its
// four clear fields kept — nothing else of the file; a client secret is
// sealed under google/oauth-client-secret. Neither is ever answered, logged
// or audited. Several keys may be live, the newest used; a key for another
// service account is refused while one of the current account's is live,
// because delegation is granted to a client id. Revoked, never deleted.

const (
	// maxKeyFile is the most of a key file accepted, in bytes: Google's are
	// about 2.4 KiB.
	maxKeyFile = 8 << 10
	// maxEmail is the longest address accepted.
	maxEmail = 254
)

var (
	// clientIDPattern is a service account's numeric client id, what the
	// Admin console's domain-wide delegation takes.
	clientIDPattern = regexp.MustCompile(`^[0-9]{1,40}$`)
	// keyIDPattern is a key's id, hex.
	keyIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{1,64}$`)
	// projectIDPattern is a Cloud project's id: 6 to 30 lower-case letters,
	// digits and hyphens, a letter first — with room for the domain-scoped
	// legacy form (example.com:project).
	projectIDPattern = regexp.MustCompile(`^[a-z0-9.:-]{1,64}$`)
	// addressPattern is an address in its plainest shape: a local part, an
	// @ and a domain of two labels or more.
	addressPattern = regexp.MustCompile(`^[^\s@]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)
)

// keyFile is what a service account's JSON key file holds that the module
// reads. Any other field is ignored and never kept.
type keyFile struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
	TokenURI     string `json:"token_uri"`
}

// parseKeyFile reads a key file as the Owner pasted it or the page read it,
// under the rules of G13: at most maxKeyFile bytes, type service_account,
// token_uri tokenURL, the private key an RSA key in PKCS #8 PEM, and the
// four clear fields present and well-formed. Every failure is a sentence on
// the json field; none repeats the file's content.
func parseKeyFile(text string) (keyFile, []byte, fieldErrors) {
	var f fieldErrors
	switch {
	case strings.TrimSpace(text) == "":
		f.add("json", "The key file's content is required.")
		return keyFile{}, nil, f
	case len(text) > maxKeyFile:
		f.add("json", fmt.Sprintf("A key file is at most %d KiB.", maxKeyFile>>10))
		return keyFile{}, nil, f
	}
	var k keyFile
	if err := json.Unmarshal([]byte(text), &k); err != nil {
		f.add("json", "This is not a JSON key file. Create a key of type JSON on the service account and upload the file Google downloads.")
		return keyFile{}, nil, f
	}
	if k.Type != "service_account" {
		f.add("json", "The file is not a service account's key (its type is not service_account).")
	}
	if k.TokenURI != tokenURL {
		f.add("json", "The file's token_uri is not "+tokenURL+".")
	}
	if len(k.ClientEmail) > maxEmail || !addressPattern.MatchString(k.ClientEmail) {
		f.add("json", "The file names no service account address (client_email).")
	}
	if !clientIDPattern.MatchString(k.ClientID) {
		f.add("json", "The file names no numeric client id (client_id).")
	}
	if !keyIDPattern.MatchString(k.PrivateKeyID) {
		f.add("json", "The file names no key id (private_key_id).")
	}
	if !projectIDPattern.MatchString(k.ProjectID) {
		f.add("json", "The file names no project (project_id).")
	}
	key, ok := pkcs8RSA(k.PrivateKey)
	if !ok {
		f.add("json", "The file's private_key is not an RSA private key in PKCS #8 PEM.")
	}
	if f.failed() {
		return keyFile{}, nil, f
	}
	return k, key, f
}

// pkcs8RSA is s's one PEM block when it is a PKCS #8 RSA private key, as
// PEM, ready to seal; false otherwise.
func pkcs8RSA(s string) ([]byte, bool) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(s)))
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) > 0 {
		return nil, false
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, false
	}
	if _, ok := key.(*rsa.PrivateKey); !ok {
		return nil, false
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: block.Bytes}), true
}

// inUseIDs are the credentials a call is made with now: the first live key
// (liveKeys) and the newest live client secret.
func inUseIDs(rows []store.GoogleCredential) (key, secret int64) {
	if keys := liveKeys(rows); len(keys) > 0 {
		key = keys[0].ID
	}
	if s := liveSecret(rows); s != nil {
		secret = s.ID
	}
	return key, secret
}

// liveSecret is the newest unrevoked client secret, nil without one. The
// rows come newest first.
func liveSecret(rows []store.GoogleCredential) *store.GoogleCredential {
	for i, r := range rows {
		if r.Kind == credentialKindClientSecret && r.RevokedAt == nil {
			return &rows[i]
		}
	}
	return nil
}

// credentialBody is a credential as the contract answers it — never its
// value.
func credentialBody(r store.GoogleCredential, inUse bool) gen.GoogleCredential {
	return gen.GoogleCredential{
		Id: r.ID, Kind: gen.GoogleCredentialKind(r.Kind),
		ClientEmail: r.ClientEmail, ClientId: r.ClientID, PrivateKeyId: r.PrivateKeyID, ProjectId: r.ProjectID,
		CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, LastFailedAt: r.LastFailedAt, RevokedAt: r.RevokedAt,
		InUse: inUse,
	}
}

// credentialsBody is every credential, newest first, those in use marked.
func credentialsBody(rows []store.GoogleCredential) []gen.GoogleCredential {
	key, secret := inUseIDs(rows)
	out := make([]gen.GoogleCredential, 0, len(rows))
	for _, r := range rows {
		out = append(out, credentialBody(r, r.ID == key || r.ID == secret))
	}
	return out
}

// credentialCodes are a credential's event codes: its row id, and a key's
// id and its service account's numeric client id — never an address, the
// key or the secret.
func credentialCodes(r store.GoogleCredential) []string {
	codes := []string{"credential:" + strconv.FormatInt(r.ID, 10), "kind:" + r.Kind}
	if r.PrivateKeyID != nil {
		codes = append(codes, "key_id:"+*r.PrivateKeyID)
	}
	if r.ClientID != nil {
		codes = append(codes, "client_id:"+*r.ClientID)
	}
	return codes
}

// PostGoogleConnectionCredentials uploads a service account's key file:
// parsed under G13's rules, the private key sealed at once, the four clear
// fields kept. Under the connection's lock, a key for another service
// account than a live key's is refused 422 client_mismatch. Audited
// google.credential.added. (POST /api/v1/google/connection/credentials)
func (s *server) PostGoogleConnectionCredentials(ctx context.Context, req gen.PostGoogleConnectionCredentialsRequestObject) (gen.PostGoogleConnectionCredentialsResponseObject, error) {
	var in gen.GoogleCredentialRequest
	if req.Body != nil {
		in = *req.Body
	}
	k, key, f := parseKeyFile(in.Json)
	if f.failed() {
		return gen.PostGoogleConnectionCredentials400ApplicationProblemPlusJSONResponse(invalid("Invalid key file", f)), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.Secrets == nil {
		return nil, errors.New("google: no secrets box to seal the key with")
	}
	sealed, err := s.deps.Secrets.Seal(serviceAccountKeyPurpose, key)
	clear(key)
	if err != nil {
		return nil, fmt.Errorf("google: seal the key: %w", err)
	}
	now := s.deps.Clock()
	var answer gen.GoogleCredential
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockConnection(ctx); err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		rows, err := q.ListCredentials(ctx)
		if err != nil {
			return fmt.Errorf("google: read the credentials: %w", err)
		}
		if slices.ContainsFunc(liveKeys(rows), func(r store.GoogleCredential) bool { return deref(r.ClientID) != k.ClientID }) {
			return refusal{clientMismatch()}
		}
		row, err := q.InsertKey(ctx, store.InsertKeyParams{
			ClientEmail: &k.ClientEmail, ClientID: &k.ClientID, PrivateKeyID: &k.PrivateKeyID, ProjectID: &k.ProjectID,
			Sealed: sealed, Now: now, CreatedByUserID: actor.UserID,
		})
		if err != nil {
			return fmt.Errorf("google: add the key: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionCredentialAdded, k.ClientID, credentialCodes(row)); err != nil {
			return err
		}
		rows, err = q.ListCredentials(ctx)
		if err != nil {
			return fmt.Errorf("google: read the credentials: %w", err)
		}
		for _, b := range credentialsBody(rows) {
			if b.Id == row.ID {
				answer = b
			}
		}
		return nil
	})
	var r refusal
	switch {
	case errors.As(err, &r):
		return gen.PostGoogleConnectionCredentials422ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PostGoogleConnectionCredentials201JSONResponse(answer), nil
}

// DeleteGoogleConnectionCredentialsById revokes a key or a client secret:
// kept, marked revoked, never used again, a key's cached tokens forgotten;
// nothing changes at Google. One already revoked answers as it is. Revoking
// the last live key leaves the connection unconfigured on its next read.
// (DELETE /api/v1/google/connection/credentials/{id})
func (s *server) DeleteGoogleConnectionCredentialsById(ctx context.Context, req gen.DeleteGoogleConnectionCredentialsByIdRequestObject) (gen.DeleteGoogleConnectionCredentialsByIdResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var (
		answer  gen.GoogleCredential
		found   bool
		revoked *store.GoogleCredential
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockConnection(ctx); err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		rows, err := q.ListCredentials(ctx)
		if err != nil {
			return fmt.Errorf("google: read the credentials: %w", err)
		}
		i := slices.IndexFunc(rows, func(r store.GoogleCredential) bool { return r.ID == req.Id })
		if i < 0 {
			return nil
		}
		found = true
		row := rows[i]
		if row.RevokedAt != nil {
			answer = credentialBody(row, false)
			return nil
		}
		updated, err := q.RevokeCredential(ctx, store.RevokeCredentialParams{Now: now, ID: row.ID})
		if err != nil {
			return fmt.Errorf("google: revoke the credential: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionCredentialRevoked, deref(row.ClientID), credentialCodes(updated)); err != nil {
			return err
		}
		answer = credentialBody(updated, false)
		revoked = &row
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case !found:
		return gen.DeleteGoogleConnectionCredentialsById404Response{}, nil
	}
	if revoked != nil && revoked.Kind == credentialKindKey {
		s.keys.c.tokens.dropKey(revoked.ID, revoked.Sealed)
	}
	return gen.DeleteGoogleConnectionCredentialsById200JSONResponse(answer), nil
}
