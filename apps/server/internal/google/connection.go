package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the connection (Google Workspace design G12, G13, G16): its
// read, the Owner's admin to impersonate and credential kind under If-Match,
// and the Internal web client. The PUT runs under the row's lock and moves
// its version on; so does a change of the web client. What a test finds is
// written beside them (test.go) and leaves the version alone, so an open
// form is not refused for it.

const (
	// oauthClientSecretPurpose is the secrets box purpose the Internal web
	// client's secret is sealed under.
	oauthClientSecretPurpose = "google/oauth-client-secret"
	// oauthClientSuffix ends every OAuth client id Google issues.
	oauthClientSuffix = ".apps.googleusercontent.com"
	// The web client's bounds, in characters.
	maxOAuthClientID     = 200
	maxOAuthClientSecret = 1000
	// connectCallbackPath is the person's connection's callback (delivery
	// D), registered on the web client from the start.
	connectCallbackPath = "/api/v1/google/connect/callback"
	// oidcCallbackPath is identity's single sign-on callback, which the same
	// web client serves when Google is the sign-in provider.
	oidcCallbackPath = "/api/v1/identity/oidc/callback"
)

// oauthClientIDPattern is an OAuth client id as the Cloud console shows it:
// the project number, a hyphen, a random part, then oauthClientSuffix.
var oauthClientIDPattern = regexp.MustCompile(`^[0-9]+-[0-9a-z]+\.apps\.googleusercontent\.com$`)

// ifMatchMessage is the field error of an If-Match that is not a version.
const ifMatchMessage = "If-Match is the version the connection was read at, such as 3."

// parseVersion reads an If-Match version, bare (3), quoted ("3") or weak
// (W/"3").
func parseVersion(raw string) (int32, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 0 {
		return 0, false
	}
	return int32(n), true
}

// badIfMatch is the 400 of an If-Match that is not a version.
func badIfMatch() gen.GoogleProblem {
	var f fieldErrors
	f.add("If-Match", ifMatchMessage)
	return invalid("Invalid If-Match", f)
}

// orEmpty is s, or an empty slice for nil, so an array is never null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// redirectURIs are the two redirect URIs to register on the Internal web
// client, under the installation's origin and base path: the person's
// connection's callback and identity's single sign-on callback.
func redirectURIs(cfg *config.Config) []string {
	if cfg == nil || cfg.AppOrigin == "" {
		return []string{}
	}
	base := cfg.AppOrigin + cfg.BasePath
	return []string{base + connectCallbackPath, base + oidcCallbackPath}
}

// connectionBody is the connection as GET /connection answers it.
func connectionBody(conn store.GoogleConnection, creds []store.GoogleCredential, cfg *config.Config) gen.GoogleConnection {
	status, reason := statusOf(conn, creds)
	body := gen.GoogleConnection{
		Status:           gen.GoogleConnectionStatus(status),
		CredentialKind:   gen.GoogleConnectionCredentialKind(conn.CredentialKind),
		AdminSubject:     conn.AdminSubject,
		CustomerId:       conn.CustomerID,
		PrimaryDomain:    conn.PrimaryDomain,
		Domains:          orEmpty(conn.Domains),
		OrganisationName: conn.OrganisationName,
		OauthClientId:    conn.OauthClientID,
		Credentials:      credentialsBody(creds),
		ScopesToDelegate: neededScopes(),
		ScopesGranted:    orEmpty(conn.ScopesGranted),
		ScopesMissing:    orEmpty(conn.ScopesMissing),
		ScopesUnexpected: orEmpty(conn.ScopesUnexpected),
		LastCheckedAt:    conn.LastCheckedAt,
		RedirectUris:     redirectURIs(cfg),
		Version:          conn.Version,

		AddressDomain:        conn.AddressDomain,
		AddressPattern:       gen.GoogleConnectionAddressPattern(conn.AddressPattern),
		OrgUnitPath:          conn.OrgUnitPath,
		PassLifetimeDays:     conn.PassLifetimeDays,
		PassDelivery:         gen.GoogleConnectionPassDelivery(conn.PassDelivery),
		LeaverRemoveLicences: conn.LeaverRemoveLicences,
		LeaverRemoveGroups:   gen.GoogleConnectionLeaverRemoveGroups(conn.LeaverRemoveGroups),
		LeaverSignOut:        conn.LeaverSignOut,
		Skus:                 []gen.GoogleSku{},
	}
	if skus, err := cachedSkus(conn); err == nil {
		for _, c := range skus {
			body.Skus = append(body.Skus, skuBody(c))
		}
	}
	if reason != "" {
		body.StatusReason = &reason
	}
	if keys := liveKeys(creds); len(keys) > 0 {
		body.DelegationClientId = keys[0].ClientID
	}
	if s := liveSecret(creds); s != nil {
		at := s.CreatedAt
		body.OauthClientSecretAddedAt = &at
	}
	return body
}

// readConnection is the connection's body read through q.
func readConnection(ctx context.Context, q *store.Queries, cfg *config.Config, conn store.GoogleConnection) (gen.GoogleConnection, error) {
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return gen.GoogleConnection{}, fmt.Errorf("google: read the credentials: %w", err)
	}
	return connectionBody(conn, creds, cfg), nil
}

// GetGoogleConnection answers the connection from the module's rows;
// nothing reaches Google. (GET /api/v1/google/connection)
func (s *server) GetGoogleConnection(ctx context.Context, _ gen.GetGoogleConnectionRequestObject) (gen.GetGoogleConnectionResponseObject, error) {
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the connection: %w", err)
	}
	body, err := readConnection(ctx, s.q, s.deps.Config, conn)
	if err != nil {
		return nil, err
	}
	return gen.GetGoogleConnection200JSONResponse(body), nil
}

// PutGoogleConnection sets the admin to impersonate and the credential kind
// under If-Match; a field left out keeps its value. vantigo_hosted is
// refused 422 credential_kind_unsupported. A changed admin sends the status
// back to unconfigured and forgets the scopes the last test found. The same
// values again write nothing. Audited google.connection.changed with the
// names of what changed — never the address.
// (PUT /api/v1/google/connection)
func (s *server) PutGoogleConnection(ctx context.Context, req gen.PutGoogleConnectionRequestObject) (gen.PutGoogleConnectionResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return gen.PutGoogleConnection400ApplicationProblemPlusJSONResponse(badIfMatch()), nil
	}
	var in gen.GoogleConnectionRequest
	if req.Body != nil {
		in = *req.Body
	}
	var f fieldErrors
	var admin *string
	if in.AdminSubject != nil {
		a := strings.ToLower(strings.TrimSpace(*in.AdminSubject))
		if utf8.RuneCountInString(a) > maxEmail || !addressPattern.MatchString(a) {
			f.add("adminSubject", "The admin to impersonate is an address in the organisation, such as admin@example.com.")
		}
		admin = &a
	}
	if in.CredentialKind != nil && *in.CredentialKind != credentialKindKey && *in.CredentialKind != connectionKindHosted {
		f.add("credentialKind", "The credential kind is service_account_key.")
	}
	if f.failed() {
		return gen.PutGoogleConnection400ApplicationProblemPlusJSONResponse(invalid("Invalid connection", f)), nil
	}
	if in.CredentialKind != nil && *in.CredentialKind == connectionKindHosted {
		return gen.PutGoogleConnection422ApplicationProblemPlusJSONResponse(credentialKindUnsupported()), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var body gen.GoogleConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		if conn.Version != version {
			return refusal{versionMismatch(conn.Version, version)}
		}
		var changed []string
		p := store.UpdateConnectionSettingsParams{AdminSubject: conn.AdminSubject, CredentialKind: conn.CredentialKind, Now: now, UpdatedByUserID: actor.UserID}
		if admin != nil && deref(conn.AdminSubject) != *admin {
			p.AdminSubject, p.Reset = admin, true
			changed = append(changed, "admin_subject_changed")
		}
		if in.CredentialKind != nil && conn.CredentialKind != *in.CredentialKind {
			p.CredentialKind = *in.CredentialKind
			changed = append(changed, "credential_kind:"+p.CredentialKind)
		}
		if len(changed) > 0 {
			if conn, err = q.UpdateConnectionSettings(ctx, p); err != nil {
				return fmt.Errorf("google: set the connection: %w", err)
			}
			if err := recordEvent(ctx, q, now, actor, actionConnectionChanged, "", changed); err != nil {
				return err
			}
		}
		body, err = readConnection(ctx, q, s.deps.Config, conn)
		return err
	})
	var r refusal
	switch {
	case errors.As(err, &r):
		return gen.PutGoogleConnection412ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PutGoogleConnection200JSONResponse(body), nil
}

// PutGoogleConnectionOauthClient sets the Internal web client (G13): its
// id, ending .apps.googleusercontent.com, and its secret, sealed at once. A
// changed id needs its secret; a secret alone rotates the current client's,
// the older one revoked. Either moves the connection's version on; the same
// id without a secret writes nothing. Audited google.oauth_client.changed,
// never with the secret. (PUT /api/v1/google/connection/oauth-client)
func (s *server) PutGoogleConnectionOauthClient(ctx context.Context, req gen.PutGoogleConnectionOauthClientRequestObject) (gen.PutGoogleConnectionOauthClientResponseObject, error) {
	var in gen.GoogleOAuthClientRequest
	if req.Body != nil {
		in = *req.Body
	}
	var f fieldErrors
	clientID := strings.TrimSpace(in.ClientId)
	if len(clientID) > maxOAuthClientID || !oauthClientIDPattern.MatchString(clientID) {
		f.add("clientId", "The client id is the web client's, ending "+oauthClientSuffix+".")
	}
	var secret string
	if in.ClientSecret != nil {
		secret = strings.TrimSpace(*in.ClientSecret)
		switch n := utf8.RuneCountInString(secret); {
		case n == 0:
			f.add("clientSecret", "The client secret is required to change it.")
		case n > maxOAuthClientSecret:
			f.add("clientSecret", fmt.Sprintf("The client secret is at most %d characters.", maxOAuthClientSecret))
		}
	}
	if f.failed() {
		return gen.PutGoogleConnectionOauthClient400ApplicationProblemPlusJSONResponse(invalid("Invalid web client", f)), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	var sealed []byte
	if secret != "" {
		if s.deps.Secrets == nil {
			return nil, errors.New("google: no secrets box to seal the client secret with")
		}
		if sealed, err = s.deps.Secrets.Seal(oauthClientSecretPurpose, []byte(secret)); err != nil {
			return nil, fmt.Errorf("google: seal the client secret: %w", err)
		}
	}
	now := s.deps.Clock()
	var body gen.GoogleConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		idChanged := deref(conn.OauthClientID) != clientID
		if idChanged && sealed == nil {
			var f fieldErrors
			f.add("clientSecret", "A new web client needs its client secret.")
			return refusal{invalid("Invalid web client", f)}
		}
		if !idChanged && sealed == nil {
			body, err = readConnection(ctx, q, s.deps.Config, conn)
			return err
		}
		codes := []string{"client_id:" + clientID}
		if idChanged {
			codes = append(codes, "client_changed")
		}
		row, err := q.InsertClientSecret(ctx, store.InsertClientSecretParams{Sealed: sealed, Now: now, CreatedByUserID: actor.UserID})
		if err != nil {
			return fmt.Errorf("google: add the client secret: %w", err)
		}
		if _, err := q.RevokeClientSecrets(ctx, store.RevokeClientSecretsParams{Now: now, KeepID: row.ID}); err != nil {
			return fmt.Errorf("google: revoke the older client secrets: %w", err)
		}
		codes = append(codes, "secret:"+strconv.FormatInt(row.ID, 10))
		updated, err := q.UpdateOAuthClient(ctx, store.UpdateOAuthClientParams{OauthClientID: &clientID, Now: now, UpdatedByUserID: actor.UserID})
		if err != nil {
			return fmt.Errorf("google: set the web client: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionOAuthClientChanged, "", codes); err != nil {
			return err
		}
		body, err = readConnection(ctx, q, s.deps.Config, updated)
		return err
	})
	var r refusal
	switch {
	case errors.As(err, &r) && r.status() == http.StatusBadRequest:
		return gen.PutGoogleConnectionOauthClient400ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PutGoogleConnectionOauthClient200JSONResponse(body), nil
}
