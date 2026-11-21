package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the clients an Owner registers by hand (OAuth connections
// design C8), for a client that publishes no metadata document — Cursor, VS
// Code, an in-house app — and the list of client hosts that may connect by
// their document (C11). Both are an Owner's session operations, never the
// API's.

const (
	// registeredClientPrefix starts every client_id Vantigo mints (C8).
	registeredClientPrefix = "vtc_"
	// registeredClientChars is how many base62 characters follow it.
	registeredClientChars = 24
	// registeredClientBytes is the randomness behind them: 17 bytes, 136 bits,
	// fit 24 base62 characters (62^24 > 2^142).
	registeredClientBytes = 17
	// maxRegisteredClients is how many clients may be registered at once.
	maxRegisteredClients = 50
	// maxRegisteredClientName is a registered name's bound, the consent
	// page's (C13).
	maxRegisteredClientName = 80
	// maxOAuthClientHosts is how many hosts the allow-list may name (C11).
	maxOAuthClientHosts = 50
	// oauthClientKindRegistered is a client registered here.
	oauthClientKindRegistered = "registered"
)

// The audit actions of registered clients and the allow-list.
const (
	auditOAuthClientCreated      = "oauth_client.created"
	auditOAuthClientUpdated      = "oauth_client.updated"
	auditOAuthClientArchived     = "oauth_client.archived"
	auditOAuthClientHostsChanged = "oauth.client_hosts_changed"
	// revokeReasonClientChanged ends a registered client's connection whose
	// redirect URI an edit removed (A29).
	revokeReasonClientChanged = "client_changed"
)

const (
	registeredClientNameMessage      = "A name of 1 to 80 characters is required."
	registeredClientRedirectsMessage = "1 to 20 redirect URIs, each https with a host, or http on 127.0.0.1, [::1] or localhost, with no fragment and at most 1,024 characters."
	registeredClientIfMatchMessage   = "If-Match is the version the client was read at."
	oauthClientHostsMessage          = "At most 50 host names, each a DNS name of at least two labels such as claude.ai: letters, digits and hyphens, with no address, port, wildcard or punycode."
)

var (
	registeredClientNotFound = refuse(http.StatusNotFound, "not_found", "No such registered client.", nil)
	registeredClientMismatch = refuse(http.StatusPreconditionFailed, "version_mismatch", "The client has been changed since it was read.", nil)
	registeredClientLimit    = refuse(http.StatusConflict, "oauth_client_limit", "50 clients are registered already; archive one first.", nil)
)

// newRegisteredClientID mints a client_id: vtc_ and 24 base62 characters.
func newRegisteredClientID() string {
	b := make([]byte, registeredClientBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error; it crashes the process instead
	return registeredClientPrefix + base62(new(big.Int).SetBytes(b), registeredClientChars)
}

// MintOAuthClientID mints a registered client's client_id as identity does,
// for the development seed.
func MintOAuthClientID() string { return newRegisteredClientID() }

// isRegisteredClientID reports whether a client_id has a registered client's
// shape: such a client_id is never fetched as a document.
func isRegisteredClientID(clientID string) bool {
	if len(clientID) != len(registeredClientPrefix)+registeredClientChars || !strings.HasPrefix(clientID, registeredClientPrefix) {
		return false
	}
	for _, c := range clientID[len(registeredClientPrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// registeredClientAudit is a registered client as the audit trail records it.
type registeredClientAudit struct {
	ID           uuid.UUID  `json:"id"`
	ClientID     string     `json:"clientId"`
	Name         string     `json:"name"`
	RedirectURIs []string   `json:"redirectUris"`
	ArchivedAt   *time.Time `json:"archivedAt,omitempty"`
}

func registeredClientSnapshot(c store.IdentityOauthClient) registeredClientAudit {
	return registeredClientAudit{ID: c.ID, ClientID: c.ClientID, Name: c.Name, RedirectURIs: nonNil(c.RedirectUris), ArchivedAt: c.ArchivedAt}
}

// readRegisteredClient holds a create or update body to C8 and C9: the name
// as oauthDisplayName leaves it, 1 to 80 characters; 1 to 20 redirect URIs,
// each valid by C9, repeats dropped and the order kept.
func readRegisteredClient(body *gen.OAuthRegisteredClientRequest) (name string, uris []string, err error) {
	var in gen.OAuthRegisteredClientRequest
	if body != nil {
		in = *body
	}
	fields := map[string][]string{}
	name = oauthDisplayName(in.Name)
	if name == "" || utf8.RuneCountInString(strings.TrimSpace(in.Name)) > maxRegisteredClientName {
		fields["name"] = []string{registeredClientNameMessage}
	}
	for _, uri := range in.RedirectUris {
		if !validRedirectURI(uri) {
			fields["redirectUris"] = []string{registeredClientRedirectsMessage}
			break
		}
		if !slices.Contains(uris, uri) {
			uris = append(uris, uri)
		}
	}
	if len(uris) == 0 || len(uris) > maxOAuthRedirectURIs {
		fields["redirectUris"] = []string{registeredClientRedirectsMessage}
	}
	if len(fields) > 0 {
		return "", nil, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
	}
	return name, uris, nil
}

func registeredClientBody(row store.ListOAuthClientsRow) gen.OAuthRegisteredClient {
	return gen.OAuthRegisteredClient{
		Id: row.ID, ClientId: row.ClientID, Name: row.Name, RedirectUris: nonNil(row.RedirectUris),
		CreatedBy: row.CreatedBy, CreatedByName: row.CreatedByName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Version: row.Version.String(), LiveConnections: int(row.LiveConnections),
	}
}

// registeredClient answers one registered client after a write committed.
func (s *server) registeredClient(ctx context.Context, id uuid.UUID) (gen.OAuthRegisteredClient, error) {
	rows, err := s.q.ListOAuthClients(ctx, store.ListOAuthClientsParams{Now: s.deps.Clock(), ID: &id})
	if err != nil {
		return gen.OAuthRegisteredClient{}, fmt.Errorf("identity: read the registered client: %w", err)
	}
	if len(rows) == 0 {
		return gen.OAuthRegisteredClient{}, registeredClientNotFound
	}
	return registeredClientBody(rows[0]), nil
}

// GetIdentityOauthClients lists the registered clients (C8).
// (GET /api/v1/identity/oauth-clients)
func (s *server) GetIdentityOauthClients(ctx context.Context, _ gen.GetIdentityOauthClientsRequestObject) (gen.GetIdentityOauthClientsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	rows, err := s.q.ListOAuthClients(ctx, store.ListOAuthClientsParams{Now: s.deps.Clock()})
	if err != nil {
		return nil, fmt.Errorf("identity: list the registered clients: %w", err)
	}
	out := gen.OAuthRegisteredClientList{Clients: make([]gen.OAuthRegisteredClient, 0, len(rows))}
	for _, row := range rows {
		out.Clients = append(out.Clients, registeredClientBody(row))
	}
	return gen.GetIdentityOauthClients200JSONResponse(out), nil
}

// PostIdentityOauthClients registers a client, audited oauth_client.created.
// (POST /api/v1/identity/oauth-clients)
func (s *server) PostIdentityOauthClients(ctx context.Context, req gen.PostIdentityOauthClientsRequestObject) (gen.PostIdentityOauthClientsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	name, uris, err := readRegisteredClient(req.Body)
	if err != nil {
		return refusalOr[gen.PostIdentityOauthClientsResponseObject](err)
	}
	id := uuid.New()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.deps.Clock()
		// Two registrations at once queue on the singleton row, so the limit
		// holds.
		if err := q.EnsureSystemSettings(ctx, now); err != nil {
			return fmt.Errorf("identity: ensure the system settings: %w", err)
		}
		if _, err := q.LockOAuthClientHosts(ctx); err != nil {
			return fmt.Errorf("identity: lock the OAuth settings: %w", err)
		}
		n, err := q.CountLiveOAuthClients(ctx)
		if err != nil {
			return fmt.Errorf("identity: count the registered clients: %w", err)
		}
		if n >= maxRegisteredClients {
			return registeredClientLimit
		}
		row := store.IdentityOauthClient{ID: id, ClientID: newRegisteredClientID(), Name: name, RedirectUris: uris, CreatedBy: &p.UserID, Version: uuid.New()}
		if err := q.InsertOAuthClient(ctx, store.InsertOAuthClientParams{
			ID: row.ID, ClientID: row.ClientID, Name: row.Name, RedirectUris: row.RedirectUris, CreatedBy: row.CreatedBy, Now: now, Version: row.Version,
		}); err != nil {
			return fmt.Errorf("identity: register the client: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditOAuthClientCreated, before: struct{}{}, after: registeredClientSnapshot(row), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOauthClientsResponseObject](err)
	}
	out, err := s.registeredClient(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PostIdentityOauthClients201JSONResponse(out), nil
}

// registeredClientTx runs fn on the registered client id, locked and not
// archived, at the version If-Match names.
func (s *server) registeredClientTx(ctx context.Context, id uuid.UUID, ifMatch string, fn func(q *store.Queries, c store.IdentityOauthClient, now time.Time) error) error {
	version, ok := parseIntegrationVersion(ifMatch)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", registeredClientIfMatchMessage, map[string][]string{"If-Match": {registeredClientIfMatchMessage}})
	}
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.LockOAuthClient(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && c.ArchivedAt != nil) {
			return registeredClientNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the registered client: %w", err)
		}
		if c.Version != version {
			return registeredClientMismatch
		}
		return fn(q, c, s.deps.Clock())
	})
}

// PatchIdentityOauthClientsById replaces a registered client's name and
// redirect URIs under If-Match, audited oauth_client.updated. A connection
// whose redirect URI the new list no longer matches is revoked in the same
// transaction (client_changed, A29); the others are kept.
// (PATCH /api/v1/identity/oauth-clients/{id})
func (s *server) PatchIdentityOauthClientsById(ctx context.Context, req gen.PatchIdentityOauthClientsByIdRequestObject) (gen.PatchIdentityOauthClientsByIdResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	name, uris, err := readRegisteredClient(req.Body)
	if err != nil {
		return refusalOr[gen.PatchIdentityOauthClientsByIdResponseObject](err)
	}
	err = s.registeredClientTx(ctx, req.Id, req.Params.IfMatch, func(q *store.Queries, c store.IdentityOauthClient, now time.Time) error {
		after := c
		after.Name, after.RedirectUris, after.Version = name, uris, uuid.New()
		if err := q.UpdateOAuthClient(ctx, store.UpdateOAuthClientParams{ID: c.ID, Name: name, RedirectUris: uris, Now: now, Version: after.Version}); err != nil {
			return fmt.Errorf("identity: change the registered client: %w", err)
		}
		// A connection whose redirect URI the edit removes ends with it
		// (A29): its code went to an address the Owner no longer vouches for.
		connections, err := q.LockRegisteredClientOAuthConnections(ctx, c.ClientID)
		if err != nil {
			return fmt.Errorf("identity: read the client's connections: %w", err)
		}
		for _, k := range connections {
			if slices.ContainsFunc(uris, func(registered string) bool { return redirectMatches(registered, k.RedirectUri) }) {
				continue
			}
			revoked, err := q.RevokeOAuthConnection(ctx, store.RevokeOAuthConnectionParams{ID: k.ID, Now: now, RevokedBy: &p.UserID, Reason: revokeReasonClientChanged})
			if err != nil {
				return fmt.Errorf("identity: revoke the client's connection: %w", err)
			}
			if err := auditRevoked(ctx, q, r, revoked, &p.UserID, "", p.MFAVerified, now); err != nil {
				return err
			}
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditOAuthClientUpdated, before: registeredClientSnapshot(c), after: registeredClientSnapshot(after), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PatchIdentityOauthClientsByIdResponseObject](err)
	}
	out, err := s.registeredClient(ctx, req.Id)
	if err != nil {
		return refusalOr[gen.PatchIdentityOauthClientsByIdResponseObject](err)
	}
	return gen.PatchIdentityOauthClientsById200JSONResponse(out), nil
}

// PostIdentityOauthClientsByIdArchive archives a registered client under
// If-Match and revokes every connection it has (client_archived), in one
// transaction, audited oauth_client.archived and connection.revoked.
// (POST /api/v1/identity/oauth-clients/{id}/archive)
func (s *server) PostIdentityOauthClientsByIdArchive(ctx context.Context, req gen.PostIdentityOauthClientsByIdArchiveRequestObject) (gen.PostIdentityOauthClientsByIdArchiveResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.registeredClientTx(ctx, req.Id, req.Params.IfMatch, func(q *store.Queries, c store.IdentityOauthClient, now time.Time) error {
		after := c
		after.ArchivedAt, after.Version = &now, uuid.New()
		if err := q.ArchiveOAuthClient(ctx, store.ArchiveOAuthClientParams{ID: c.ID, Now: now, ArchivedBy: &p.UserID, Version: after.Version}); err != nil {
			return fmt.Errorf("identity: archive the registered client: %w", err)
		}
		revoked, err := q.RevokeClientOAuthConnections(ctx, store.RevokeClientOAuthConnectionsParams{ClientID: c.ClientID, Now: now, RevokedBy: &p.UserID})
		if err != nil {
			return fmt.Errorf("identity: revoke the client's connections: %w", err)
		}
		for _, k := range revoked {
			if err := auditRevoked(ctx, q, r, k, &p.UserID, "", p.MFAVerified, now); err != nil {
				return err
			}
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditOAuthClientArchived, before: registeredClientSnapshot(c), after: registeredClientSnapshot(after), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOauthClientsByIdArchiveResponseObject](err)
	}
	return gen.PostIdentityOauthClientsByIdArchive204Response{}, nil
}

// normalizeClientHosts holds an allow-list to C11's entries: each a DNS name
// in ASCII of at least two labels, lower-cased, no punycode and no address
// (the rules a client_id's host is held to), repeats dropped, sorted.
func normalizeClientHosts(hosts []string) ([]string, bool) {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if !validDNSName(h) || net.ParseIP(h) != nil {
			return nil, false
		}
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	if len(out) > maxOAuthClientHosts {
		return nil, false
	}
	slices.Sort(out)
	return out, true
}

// clientHostAllowed is C11's rule: an empty list admits any host; otherwise
// host must equal an entry or be a subdomain of one.
func clientHostAllowed(allowed []string, host string) bool {
	if len(allowed) == 0 {
		return true
	}
	host = strings.ToLower(host)
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// oauthClientHosts reads the allow-list on the pool.
func (a *Access) oauthClientHosts(ctx context.Context) ([]string, error) {
	hosts, err := a.q.GetOAuthClientHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: read the OAuth client hosts: %w", err)
	}
	return hosts, nil
}

// clientHostsAudit is the allow-list as the audit trail records it.
type clientHostsAudit struct {
	ClientHosts []string `json:"clientHosts"`
}

// GetIdentityOauthSettings reads the client hosts that may connect (C11).
// (GET /api/v1/identity/oauth/settings)
func (s *server) GetIdentityOauthSettings(ctx context.Context, _ gen.GetIdentityOauthSettingsRequestObject) (gen.GetIdentityOauthSettingsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	hosts, err := s.access.oauthClientHosts(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentityOauthSettings200JSONResponse{ClientHosts: nonNil(hosts)}, nil
}

// PutIdentityOauthSettings replaces the client hosts that may connect (C11),
// audited oauth.client_hosts_changed; the list already held writes nothing.
// No connection is ended: the list is held at the next consent and refresh.
// (PUT /api/v1/identity/oauth/settings)
func (s *server) PutIdentityOauthSettings(ctx context.Context, req gen.PutIdentityOauthSettingsRequestObject) (gen.PutIdentityOauthSettingsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	var asked []string
	if req.Body != nil {
		asked = req.Body.ClientHosts
	}
	hosts, ok := normalizeClientHosts(asked)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, map[string][]string{"clientHosts": {oauthClientHostsMessage}}), nil
	}
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.deps.Clock()
		if err := q.EnsureSystemSettings(ctx, now); err != nil {
			return fmt.Errorf("identity: ensure the system settings: %w", err)
		}
		current, err := q.LockOAuthClientHosts(ctx)
		if err != nil {
			return fmt.Errorf("identity: lock the OAuth client hosts: %w", err)
		}
		if slices.Equal(current, hosts) {
			return nil
		}
		if err := q.SetOAuthClientHosts(ctx, store.SetOAuthClientHostsParams{Hosts: hosts, Now: now, UpdatedByUserID: &p.UserID}); err != nil {
			return fmt.Errorf("identity: set the OAuth client hosts: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: auditOAuthClientHostsChanged,
			before: clientHostsAudit{ClientHosts: nonNil(current)}, after: clientHostsAudit{ClientHosts: hosts}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityOauthSettings200JSONResponse{ClientHosts: hosts}, nil
}

func (r refusal) VisitGetIdentityOauthClientsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOauthClientsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPatchIdentityOauthClientsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOauthClientsByIdArchiveResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityOauthSettingsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityOauthSettingsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
