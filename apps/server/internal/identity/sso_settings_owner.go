package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the Owner's single sign-on endpoints (Entra design D2): the
// workforce OIDC provider and the SCIM credential, read and written on
// identity.sso_settings. Every write is one transaction holding the row
// that moves its version on, audits sso-settings.changed with the row as
// GET answers it before and after — so neither the client secret nor a
// token digest is ever in the trail — re-records the authority mode the row
// derives in the same transaction, and evicts this process's reader after
// the commit; every other process reads the change within
// ssoSettingsCacheTTL. Every write answers the whole row, so a screen
// holding it carries the new version into its next PUT.

// ssoSettingsChanged is the audit action of an Owner's write.
const ssoSettingsChanged = "sso-settings.changed"

// scimPreviousTokenOverlap is how long, at most and by default, the token a
// new one replaces is still accepted.
const scimPreviousTokenOverlap = 24 * time.Hour

// maxProviderDisplayNameLength bounds the provider button's label, in
// characters.
const maxProviderDisplayNameLength = 100

// The refusals' fixed words.
const (
	ssoVersionMismatchMessage = "The single sign-on settings have been changed since they were read. Reload them and try again."
	oidcLinksExistMessage     = "People are linked to the provider under its current authority, and each of them would be provisioned anew at their next sign-in. Confirm the change to make it."
	ssoIfMatchMessage         = "If-Match is the version the settings were read at, such as 3."
)

// GetIdentityOwnerSso answers the single sign-on settings, read from the
// row rather than the cache, so an Owner sees their own save at once.
// (GET /api/v1/identity/owner/sso)
func (s *server) GetIdentityOwnerSso(ctx context.Context, _ gen.GetIdentityOwnerSsoRequestObject) (gen.GetIdentityOwnerSsoResponseObject, error) {
	row, err := s.q.GetSsoSettings(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("identity: read the single sign-on settings: %w", err)
	}
	events, err := lastSSOEvents(ctx, s.q)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentityOwnerSso200JSONResponse(s.ssoSettingsBody(row, events, s.deps.Clock())), nil
}

// PutIdentityOwnerSsoOidc replaces the workforce OIDC provider and turns it
// on, under If-Match on the row's version. The request is validated
// against the row it replaces (validateOIDCSettings), since whether the
// sealed secret is kept depends on it; an authority that moves the issuer
// while people are linked under the old one needs confirmIssuerChange.
// (PUT /api/v1/identity/owner/sso/oidc)
func (s *server) PutIdentityOwnerSsoOidc(ctx context.Context, req gen.PutIdentityOwnerSsoOidcRequestObject) (gen.PutIdentityOwnerSsoOidcResponseObject, error) {
	version, ok := parseSSOVersion(req.Params.IfMatch)
	if !ok {
		return gen.PutIdentityOwnerSsoOidc400JSONResponse(authErrorBody("invalid_request", ssoIfMatchMessage,
			map[string][]string{"If-Match": {ssoIfMatchMessage}})), nil
	}
	var in gen.SsoOidcSettingsRequest
	if req.Body != nil {
		in = *req.Body
	}
	// The token file is checked here, before the transaction opens: no
	// filesystem access happens while the row is held.
	fileProblem := tokenFileProblem(string(in.Provider), deref(in.WorkloadTokenFile))
	body, err := s.changeSSOSettings(ctx, func(ctx context.Context, q *store.Queries, current store.IdentitySsoSetting, now time.Time) (store.SaveSsoSettingsParams, bool, error) {
		if current.Version != version {
			return store.SaveSsoSettingsParams{}, false, refuse(http.StatusPreconditionFailed, "version_mismatch", ssoVersionMismatchMessage, nil)
		}
		v, fields := validateOIDCSettings(in, current, fileProblem)
		if len(fields) > 0 {
			return store.SaveSsoSettingsParams{}, false, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
		}
		confirmed := in.ConfirmIssuerChange != nil && *in.ConfirmIssuerChange
		newIssuer, _ := normalizeIssuer(v.authority)
		if oldIssuer, on := currentIssuer(current); on && oldIssuer != newIssuer && !confirmed {
			if err := refuseWhileLinked(ctx, q, oldIssuer); err != nil {
				return store.SaveSsoSettingsParams{}, false, err
			}
		}
		p := saveParamsOf(current)
		p.OidcEnabled = true
		p.OidcProvider, p.OidcAuthority, p.OidcClientID = &v.provider, &v.authority, &v.clientID
		p.OidcAllowedDomains, p.OidcDisplayName = v.allowedDomains, &v.displayName
		p.OidcWorkloadTokenFile = nil
		if v.workloadTokenFile != "" {
			p.OidcWorkloadTokenFile = &v.workloadTokenFile
		}
		switch {
		case v.clientSecret != "":
			if s.deps.Secrets == nil {
				return p, false, errors.New("identity: no secrets box to seal the OIDC client secret with")
			}
			sealed, err := s.deps.Secrets.Seal(oidcClientSecretPurpose, []byte(v.clientSecret))
			if err != nil {
				return p, false, fmt.Errorf("identity: seal the OIDC client secret: %w", err)
			}
			p.OidcClientSecretSealed, p.OidcClientSecretSetAt = sealed, &now
		case !v.keepSecret:
			p.OidcClientSecretSealed, p.OidcClientSecretSetAt = nil, nil
		}
		p.ImportedFromEnvironment = false
		return p, true, nil
	})
	if err != nil {
		return refusalOr[gen.PutIdentityOwnerSsoOidcResponseObject](err)
	}
	return gen.PutIdentityOwnerSsoOidc200JSONResponse(body), nil
}

// DeleteIdentityOwnerSsoOidc turns the workforce OIDC provider off and
// clears its block, the sealed secret included, refused while people are
// linked under its issuer unless confirm is true. A provider already off
// answers the row with nothing written.
// (DELETE /api/v1/identity/owner/sso/oidc)
func (s *server) DeleteIdentityOwnerSsoOidc(ctx context.Context, req gen.DeleteIdentityOwnerSsoOidcRequestObject) (gen.DeleteIdentityOwnerSsoOidcResponseObject, error) {
	confirmed := req.Params.Confirm != nil && *req.Params.Confirm
	body, err := s.changeSSOSettings(ctx, func(ctx context.Context, q *store.Queries, current store.IdentitySsoSetting, _ time.Time) (store.SaveSsoSettingsParams, bool, error) {
		if !current.OidcEnabled {
			return store.SaveSsoSettingsParams{}, false, nil
		}
		if issuer, on := currentIssuer(current); on && !confirmed {
			if err := refuseWhileLinked(ctx, q, issuer); err != nil {
				return store.SaveSsoSettingsParams{}, false, err
			}
		}
		p := saveParamsOf(current)
		p.OidcEnabled = false
		p.OidcProvider, p.OidcAuthority, p.OidcClientID = nil, nil, nil
		p.OidcClientSecretSealed, p.OidcClientSecretSetAt = nil, nil
		p.OidcWorkloadTokenFile, p.OidcAllowedDomains, p.OidcDisplayName = nil, nil, nil
		return p, true, nil
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityOwnerSsoOidcResponseObject](err)
	}
	return gen.DeleteIdentityOwnerSsoOidc200JSONResponse(body), nil
}

// PostIdentityOwnerSsoScimToken issues a SCIM token and turns SCIM on. A
// current token moves to the previous slot, accepted until
// previousExpiresAt (now + scimPreviousTokenOverlap by default, at most
// that); a previous token still overlapping is dropped at once. The token
// is answered this once: only its SHA-256 is kept, and it is never logged
// or audited. (POST /api/v1/identity/owner/sso/scim/token)
func (s *server) PostIdentityOwnerSsoScimToken(ctx context.Context, req gen.PostIdentityOwnerSsoScimTokenRequestObject) (gen.PostIdentityOwnerSsoScimTokenResponseObject, error) {
	var in gen.SsoScimTokenRequest
	if req.Body != nil {
		in = *req.Body
	}
	token, digest := newSCIMToken()
	rotated := false
	body, err := s.changeSSOSettings(ctx, func(_ context.Context, _ *store.Queries, current store.IdentitySsoSetting, now time.Time) (store.SaveSsoSettingsParams, bool, error) {
		p := saveParamsOf(current)
		p.ScimPreviousTokenSha256, p.ScimPreviousExpiresAt = nil, nil
		if current.ScimEnabled && len(current.ScimTokenSha256) == sha256.Size {
			expires := now.Add(scimPreviousTokenOverlap)
			if in.PreviousExpiresAt != nil {
				switch at := *in.PreviousExpiresAt; {
				case !at.After(now):
					return p, false, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage,
						map[string][]string{"previousExpiresAt": {"The previous token's expiry must be in the future."}})
				case at.After(now.Add(scimPreviousTokenOverlap)):
					return p, false, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage,
						map[string][]string{"previousExpiresAt": {"The previous token's expiry must be at most 24 hours from now."}})
				default:
					expires = at
				}
			}
			p.ScimPreviousTokenSha256, p.ScimPreviousExpiresAt = slices.Clone(current.ScimTokenSha256), &expires
			rotated = true
		}
		p.ScimEnabled = true
		p.ScimTokenSha256, p.ScimTokenSetAt = digest[:], &now
		return p, true, nil
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerSsoScimTokenResponseObject](err)
	}
	answer := gen.PostIdentityOwnerSsoScimToken200JSONResponse{Token: token, EndpointUrl: body.Scim.EndpointUrl, Settings: body}
	if rotated {
		answer.PreviousExpiresAt = body.Scim.PreviousExpiresAt
	}
	return scimTokenAnswer{answer}, nil
}

// scimTokenAnswer is the issued token's 200, which no cache may keep: it
// carries the one copy of the token there will ever be.
type scimTokenAnswer struct {
	body gen.PostIdentityOwnerSsoScimToken200JSONResponse
}

func (r scimTokenAnswer) VisitPostIdentityOwnerSsoScimTokenResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	return r.body.VisitPostIdentityOwnerSsoScimTokenResponse(w)
}

// DeleteIdentityOwnerSsoScimPrevious ends a rotation's overlap: the
// previous token's digest and its expiry are cleared. With no previous
// token it answers the row with nothing written.
// (DELETE /api/v1/identity/owner/sso/scim/previous)
func (s *server) DeleteIdentityOwnerSsoScimPrevious(ctx context.Context, _ gen.DeleteIdentityOwnerSsoScimPreviousRequestObject) (gen.DeleteIdentityOwnerSsoScimPreviousResponseObject, error) {
	body, err := s.changeSSOSettings(ctx, func(_ context.Context, _ *store.Queries, current store.IdentitySsoSetting, _ time.Time) (store.SaveSsoSettingsParams, bool, error) {
		if len(current.ScimPreviousTokenSha256) == 0 && current.ScimPreviousExpiresAt == nil {
			return store.SaveSsoSettingsParams{}, false, nil
		}
		p := saveParamsOf(current)
		p.ScimPreviousTokenSha256, p.ScimPreviousExpiresAt = nil, nil
		return p, true, nil
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteIdentityOwnerSsoScimPrevious200JSONResponse(body), nil
}

// DeleteIdentityOwnerSsoScim turns SCIM off and clears its block, both
// digests included; the users, mappings and groups it provisioned stay.
// SCIM already off answers the row with nothing written.
// (DELETE /api/v1/identity/owner/sso/scim)
func (s *server) DeleteIdentityOwnerSsoScim(ctx context.Context, _ gen.DeleteIdentityOwnerSsoScimRequestObject) (gen.DeleteIdentityOwnerSsoScimResponseObject, error) {
	body, err := s.changeSSOSettings(ctx, func(_ context.Context, _ *store.Queries, current store.IdentitySsoSetting, _ time.Time) (store.SaveSsoSettingsParams, bool, error) {
		if !current.ScimEnabled {
			return store.SaveSsoSettingsParams{}, false, nil
		}
		p := saveParamsOf(current)
		p.ScimEnabled = false
		p.ScimTokenSha256, p.ScimTokenSetAt = nil, nil
		p.ScimPreviousTokenSha256, p.ScimPreviousExpiresAt = nil, nil
		return p, true, nil
	})
	if err != nil {
		return nil, err
	}
	return gen.DeleteIdentityOwnerSsoScim200JSONResponse(body), nil
}

func (r refusal) VisitPutIdentityOwnerSsoOidcResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitDeleteIdentityOwnerSsoOidcResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerSsoScimTokenResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// ssoChange is one write's change to current, the row its transaction
// holds, at now: the whole row to save, false to write nothing because the
// row already is as asked, or an error — a refusal rolls the transaction
// back and is answered.
type ssoChange func(ctx context.Context, q *store.Queries, current store.IdentitySsoSetting, now time.Time) (store.SaveSsoSettingsParams, bool, error)

// changeSSOSettings runs change in one transaction holding the row, which
// it ensures exists first, and answers the row as GET answers it. A change
// that writes saves the row as the caller's (moving its version on),
// audits sso-settings.changed with the answer before and after, and
// records the authority mode the saved row derives in the same
// transaction; after the commit this process's reader is evicted, so its
// next request reads the row.
func (s *server) changeSSOSettings(ctx context.Context, change ssoChange) (gen.SsoSettings, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return gen.SsoSettings{}, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return gen.SsoSettings{}, err
	}
	now := s.deps.Clock()
	var answer gen.SsoSettings
	written := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.EnsureSsoSettings(ctx, now); err != nil {
			return fmt.Errorf("identity: ensure the single sign-on settings: %w", err)
		}
		current, err := q.LockSsoSettings(ctx)
		if err != nil {
			return fmt.Errorf("identity: lock the single sign-on settings: %w", err)
		}
		events, err := lastSSOEvents(ctx, q)
		if err != nil {
			return err
		}
		before := s.ssoSettingsBody(current, events, now)
		params, write, err := change(ctx, q, current, now)
		if err != nil {
			return err
		}
		if !write {
			answer = before
			return nil
		}
		params.Now, params.UpdatedByUserID = now, &p.UserID
		if err := q.SaveSsoSettings(ctx, params); err != nil {
			return fmt.Errorf("identity: save the single sign-on settings: %w", err)
		}
		saved, err := q.GetSsoSettings(ctx)
		if err != nil {
			return fmt.Errorf("identity: read the saved single sign-on settings: %w", err)
		}
		answer = s.ssoSettingsBody(saved, events, now)
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: ssoSettingsChanged,
			before: before,
			after:  answer,
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		if err := recordAuthorityModeIn(ctx, q, deriveAuthorityMode(parseSSOSettings(saved, nil)), now); err != nil {
			return fmt.Errorf("identity: record the authority mode: %w", err)
		}
		written = true
		return nil
	})
	if err != nil {
		return gen.SsoSettings{}, err
	}
	if written {
		s.access.sso.evict()
	}
	return answer, nil
}

// lastSSOEvents is the last OIDC sign-in and the last authenticated SCIM
// request, as the system status reads them.
func lastSSOEvents(ctx context.Context, q *store.Queries) (store.GetLastOperationalEventsRow, error) {
	events, err := q.GetLastOperationalEvents(ctx, store.GetLastOperationalEventsParams{
		OidcKind: operationalEventStaticOIDCSignIn,
		ScimKind: operationalEventScimRequest,
	})
	if err != nil {
		return events, fmt.Errorf("identity: read the single sign-on heartbeats: %w", err)
	}
	return events, nil
}

// ssoSettingsBody is row as the Owner sees it at now, with the client
// secret and every digest withheld: the secret only as when it was set,
// SCIM's tokens only as when the current one was set and, while a rotation
// overlaps, when the previous one stops being accepted. The callback and
// the SCIM endpoint are always answered, so they can be registered before
// anything is set. The zero row is a row never written.
func (s *server) ssoSettingsBody(row store.IdentitySsoSetting, events store.GetLastOperationalEventsRow, now time.Time) gen.SsoSettings {
	base := s.deps.Config.AppOrigin + s.deps.Config.BasePath
	body := gen.SsoSettings{
		Version:                 row.Version,
		ImportedFromEnvironment: row.ImportedFromEnvironment,
		LastOidcSignInAt:        events.LastOidcSignInAt,
		LastScimRequestAt:       events.LastScimRequestAt,
		Oidc:                    gen.SsoOidcSettings{Enabled: row.OidcEnabled, AllowedDomains: []string{}, CallbackUrl: base + oidcCallbackPath},
		Scim:                    gen.SsoScimSettings{Enabled: row.ScimEnabled, EndpointUrl: base + scimBasePath},
	}
	if row.OidcEnabled {
		o := &body.Oidc
		if row.OidcProvider != nil {
			provider := gen.SsoOidcSettingsProvider(*row.OidcProvider)
			o.Provider = &provider
		}
		o.Authority, o.ClientId = cloneString(row.OidcAuthority), cloneString(row.OidcClientID)
		o.WorkloadTokenFile = cloneString(row.OidcWorkloadTokenFile)
		if len(row.OidcClientSecretSealed) > 0 {
			o.ClientSecretSetAt = cloneTime(row.OidcClientSecretSetAt)
		}
		if len(row.OidcAllowedDomains) > 0 {
			o.AllowedDomains = slices.Clone(row.OidcAllowedDomains)
		}
		name := deref(row.OidcDisplayName)
		if name == "" {
			name = defaultOIDCDisplayName
		}
		o.DisplayName = &name
	}
	if row.ScimEnabled {
		if len(row.ScimTokenSha256) > 0 {
			body.Scim.TokenSetAt = cloneTime(row.ScimTokenSetAt)
		}
		if len(row.ScimPreviousTokenSha256) > 0 && row.ScimPreviousExpiresAt != nil && row.ScimPreviousExpiresAt.After(now) {
			body.Scim.PreviousExpiresAt = cloneTime(row.ScimPreviousExpiresAt)
		}
	}
	return body
}

func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func cloneTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// saveParamsOf is row as SaveSsoSettings writes it back, so a write that
// changes one block carries the other as it was.
func saveParamsOf(row store.IdentitySsoSetting) store.SaveSsoSettingsParams {
	return store.SaveSsoSettingsParams{
		OidcEnabled:             row.OidcEnabled,
		OidcProvider:            row.OidcProvider,
		OidcAuthority:           row.OidcAuthority,
		OidcClientID:            row.OidcClientID,
		OidcClientSecretSealed:  row.OidcClientSecretSealed,
		OidcClientSecretSetAt:   row.OidcClientSecretSetAt,
		OidcWorkloadTokenFile:   row.OidcWorkloadTokenFile,
		OidcAllowedDomains:      row.OidcAllowedDomains,
		OidcDisplayName:         row.OidcDisplayName,
		ScimEnabled:             row.ScimEnabled,
		ScimTokenSha256:         row.ScimTokenSha256,
		ScimTokenSetAt:          row.ScimTokenSetAt,
		ScimPreviousTokenSha256: row.ScimPreviousTokenSha256,
		ScimPreviousExpiresAt:   row.ScimPreviousExpiresAt,
		ImportedFromEnvironment: row.ImportedFromEnvironment,
	}
}

// parseSSOVersion reads the version an If-Match header carries, bare or
// quoted (and weakly tagged), as calendar's parseIfMatch does — except that
// 0 is a version here: the row never written, which the first save
// replaces.
func parseSSOVersion(raw string) (int32, bool) {
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

// currentIssuer is the normalised issuer row's provider links people
// under, and false while OIDC is off.
func currentIssuer(row store.IdentitySsoSetting) (string, bool) {
	if !row.OidcEnabled || row.OidcAuthority == nil {
		return "", false
	}
	if issuer, ok := normalizeIssuer(*row.OidcAuthority); ok {
		return issuer, true
	}
	return *row.OidcAuthority, true
}

// refuseWhileLinked is the 409 oidc_links_exist while anyone is linked
// under issuer, nil otherwise.
func refuseWhileLinked(ctx context.Context, q *store.Queries, issuer string) error {
	n, err := q.CountOidcLinksByIssuer(ctx, issuer)
	if err != nil {
		return fmt.Errorf("identity: count the OIDC links: %w", err)
	}
	if n > 0 {
		return refuse(http.StatusConflict, "oidc_links_exist", oidcLinksExistMessage, nil)
	}
	return nil
}

// newSCIMToken is a fresh SCIM bearer token — 32 random bytes,
// base64url without padding, calendar's RSVP token's shape — and the
// SHA-256 of its text, which scimTokenValid compares a presented token's
// with. crypto/rand.Read never returns an error; it crashes the process
// instead.
func newSCIMToken() (string, [sha256.Size]byte) {
	b := make([]byte, tokenBytes)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, sha256.Sum256([]byte(token))
}

// oidcSettingsInput is a PUT's request once validated: the values to save,
// the client secret to seal ("" for none), and whether the sealed secret
// the row holds is kept.
type oidcSettingsInput struct {
	provider, authority, clientID   string
	clientSecret, workloadTokenFile string
	allowedDomains                  []string
	displayName                     string
	keepSecret                      bool
}

// The authority and client id forms of config's oidc loader
// (config.go's entraAuthorityPattern and guidPattern), which mirror
// .NET's WorkforceOidcOptions.
var (
	entraAuthorityForm = regexp.MustCompile(`^https://login\.microsoftonline\.com/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/v2\.0$`)
	guidForm           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// googleAuthority is the one authority the google provider has.
const googleAuthority = "https://accounts.google.com"

// validateOIDCSettings holds in to the rules config's oidc loader held the
// OIDC_* variables to (config.go), each refusal a field error under the
// request's field name, and applies the credential rules against current,
// the row being replaced: a present clientSecret replaces the sealed one
// and clears the token file; a present workloadTokenFile clears the sealed
// secret; with neither, the sealed secret is kept only while the provider
// and the client id are the ones it was set for — a new application never
// inherits the old one's credential. An empty string is absent. The token
// file is judged by tokenFileProblem before the caller's transaction opens,
// and its answer is fileProblem ("" when the file passes); this function
// touches no file.
func validateOIDCSettings(in gen.SsoOidcSettingsRequest, current store.IdentitySsoSetting, fileProblem string) (oidcSettingsInput, map[string][]string) {
	fields := map[string][]string{}
	add := func(field, message string) { fields[field] = append(fields[field], message) }
	v := oidcSettingsInput{provider: string(in.Provider), authority: in.Authority, clientID: in.ClientId}

	switch v.provider {
	case "entra":
		if !entraAuthorityForm.MatchString(v.authority) {
			add("authority", "The authority must be exactly https://login.microsoftonline.com/<tenant-guid>/v2.0 for the entra provider.")
		}
		if !guidForm.MatchString(v.clientID) {
			add("clientId", "The client id must be a GUID for the entra provider.")
		}
	case "google":
		if v.authority != googleAuthority {
			add("authority", "The authority must be exactly https://accounts.google.com for the google provider.")
		}
		if !strings.HasSuffix(v.clientID, ".apps.googleusercontent.com") {
			add("clientId", "The client id must end with .apps.googleusercontent.com for the google provider.")
		}
	default:
		add("provider", "The provider must be entra or google.")
	}

	secret, tokenFile := deref(in.ClientSecret), deref(in.WorkloadTokenFile)
	sameApplication := current.OidcEnabled && deref(current.OidcProvider) == v.provider && deref(current.OidcClientID) == v.clientID
	switch {
	case secret != "" && tokenFile != "":
		add("clientSecret", "The client secret and the workload token file are mutually exclusive.")
	case secret != "":
		v.clientSecret = secret
	case tokenFile != "":
		if fileProblem != "" {
			add("workloadTokenFile", fileProblem)
		} else {
			v.workloadTokenFile = tokenFile
		}
	case current.OidcEnabled && !sameApplication:
		add("clientSecret", "A change of provider or client id needs the new application's client secret or workload token file.")
	case sameApplication && len(current.OidcClientSecretSealed) > 0:
		v.keepSecret = true
	default:
		add("clientSecret", "The client secret or the workload token file is required.")
	}

	var domains []string
	if in.AllowedDomains != nil {
		domains = *in.AllowedDomains
	}
	normalised, ok := normalizeDomains(domains)
	if !ok {
		add("allowedDomains", "Allowed domains must be bare DNS names such as example.com.")
	}
	switch v.provider {
	case "entra":
		if len(normalised) > 0 {
			add("allowedDomains", "Allowed domains are only valid for the google provider.")
		}
	case "google":
		if len(normalised) == 0 && ok {
			add("allowedDomains", "The google provider needs at least one allowed domain.")
		}
	}
	v.allowedDomains = normalised

	v.displayName = strings.TrimSpace(deref(in.DisplayName))
	switch {
	case utf8.RuneCountInString(v.displayName) > maxProviderDisplayNameLength:
		add("displayName", fmt.Sprintf("The display name must be at most %d characters.", maxProviderDisplayNameLength))
	case v.displayName == "":
		v.displayName = defaultOIDCDisplayName
	}

	if len(fields) == 0 {
		return v, nil
	}
	return v, fields
}

// dnsNamePattern is .NET's bare-DNS-name pattern for an allowed domain,
// less its length lookahead, which normalizeDomains checks itself. It is
// config.go's dnsNamePattern.
var dnsNamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// normalizeDomains is config.go's allowedDomains (which parsed
// OIDC_ALLOWED_DOMAINS as a comma list; WorkforceOidcOptions.NormalizeDomains
// in .NET) over a list: each entry trimmed, its trailing dots stripped and
// lower-cased, then held to 1 to 253 characters of the bare-DNS-name
// pattern, so a URL, an address, a port, whitespace or a single label is
// refused; the result is distinct and in ordinal order, nil when empty. A
// blank entry is skipped; one that is only dots is not blank and is
// refused. false reports a refused entry; the valid ones are still
// answered. Copied rather than exported from config, whose loader keeps
// its own for the import.
func normalizeDomains(raw []string) ([]string, bool) {
	seen := map[string]bool{}
	ok := true
	for _, entry := range raw {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		d := strings.ToLower(strings.TrimRight(strings.TrimSpace(entry), "."))
		if len(d) == 0 || len(d) > 253 || !dnsNamePattern.MatchString(d) {
			ok = false
			continue
		}
		seen[d] = true
	}
	if len(seen) == 0 {
		return nil, ok
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, ok
}

// tokenFileProblem is why a PUT's workload identity token file may not be
// saved for provider, "" when it may (or when there is none): Entra only,
// an absolute path, and a regular file of at most maxAssertionBytes this
// process can open. It reads the filesystem, so the PUT calls it before its
// transaction opens.
func tokenFileProblem(provider, path string) string {
	switch {
	case path == "":
		return ""
	case provider != "entra":
		return "The workload token file is supported only for the entra provider."
	case !filepath.IsAbs(path):
		return "The workload token file must be an absolute path."
	}
	switch readable, regular := readableFile(path); {
	case !regular:
		return fmt.Sprintf("The workload token file must be a regular file of at most %d KiB.", maxAssertionBytes/1024)
	case !readable:
		return "The workload token file must be a file this server can read, at the same path on every replica."
	}
	return ""
}

// readableFile reports whether path names a file this process can open for
// reading — config.go's readableFile, the check WorkforceOidcOptions made of
// the workload identity token file — and whether it is a regular file of at
// most maxAssertionBytes. A device, a FIFO or a socket is not: /dev/zero
// would be read without end at every sign-in, and a FIFO's open would block.
// The open is non-blocking for the same reason, should the path be swapped
// for a FIFO between the stat and the open.
func readableFile(path string) (readable, regular bool) {
	info, err := os.Stat(path)
	if err != nil {
		return false, true
	}
	if !info.Mode().IsRegular() || info.Size() > maxAssertionBytes {
		return false, false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, true
	}
	_ = f.Close()
	return true, true
}
