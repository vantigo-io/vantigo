package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The single sign-on settings (Entra design D1): the workforce OIDC
// provider and the SCIM credential, one row in identity.sso_settings that
// every reader — the session query's "is SCIM on", sign-in, the SCIM bearer
// check, the relying party, the status endpoints, the authority mode —
// reads through a short in-process cache, so a change is in effect on
// every replica within ssoSettingsCacheTTL, with no restart. An
// installation that still carries OIDC_* or SCIM_* imports them once, at
// the first API mount that finds the row never written.

// ssoSettingsCacheTTL is how long a reader answers the row it last read
// before it reads it again.
const ssoSettingsCacheTTL = 20 * time.Second

// oidcClientSecretPurpose is the secrets box purpose the OIDC client secret
// is sealed under.
const oidcClientSecretPurpose = "identity/oidc-client-secret"

// defaultOIDCDisplayName is the display name config gives the provider when
// OIDC_DISPLAY_NAME is empty.
const defaultOIDCDisplayName = "Workforce SSO"

// ssoSettings is the row parsed. A nil block is off. Version 0 is a row
// never written, or no row at all.
type ssoSettings struct {
	Version                 int32
	ImportedFromEnvironment bool
	OIDC                    *oidcSettings
	SCIM                    *scimSettings
}

// oidcSettings is the workforce OIDC provider. The client secret stays
// sealed until clientSecret opens it: a read of the row never opens the
// box.
type oidcSettings struct {
	Provider          string // "entra" | "google"
	Authority         string
	ClientID          string
	WorkloadTokenFile string
	AllowedDomains    []string
	DisplayName       string

	sealedSecret []byte
	box          *secrets.Box
}

// clientSecret opens the client secret, or answers "" when the provider
// uses workload identity and none is kept. It is never logged.
func (o *oidcSettings) clientSecret() (string, error) {
	if len(o.sealedSecret) == 0 {
		return "", nil
	}
	if o.box == nil {
		return "", errors.New("identity: no secrets box to open the OIDC client secret with")
	}
	secret, err := o.box.Open(oidcClientSecretPurpose, o.sealedSecret)
	if err != nil {
		return "", fmt.Errorf("identity: open the OIDC client secret: %w", err)
	}
	return string(secret), nil
}

// scimSettings is the SCIM credential: the SHA-256 of the current token,
// and of the previous one while a rotation overlaps, until
// PreviousExpiresAt. Only the digests are kept.
type scimSettings struct {
	Token             [sha256.Size]byte
	Previous          [sha256.Size]byte
	PreviousExpiresAt time.Time // zero: no previous token

	hasToken, hasPrevious bool
}

// digestOf is b as a SHA-256 digest, or false when b is not one.
func digestOf(b []byte) ([sha256.Size]byte, bool) {
	var d [sha256.Size]byte
	if len(b) != sha256.Size {
		return d, false
	}
	copy(d[:], b)
	return d, true
}

// parseSSOSettings is row as the readers use it. box is what the client
// secret is opened with, when someone asks for it.
func parseSSOSettings(row store.IdentitySsoSetting, box *secrets.Box) ssoSettings {
	s := ssoSettings{Version: row.Version, ImportedFromEnvironment: row.ImportedFromEnvironment}
	if row.OidcEnabled {
		s.OIDC = &oidcSettings{
			Provider:          deref(row.OidcProvider),
			Authority:         deref(row.OidcAuthority),
			ClientID:          deref(row.OidcClientID),
			WorkloadTokenFile: deref(row.OidcWorkloadTokenFile),
			AllowedDomains:    slices.Clone(row.OidcAllowedDomains),
			DisplayName:       deref(row.OidcDisplayName),
			sealedSecret:      slices.Clone(row.OidcClientSecretSealed),
			box:               box,
		}
		if s.OIDC.DisplayName == "" {
			s.OIDC.DisplayName = defaultOIDCDisplayName
		}
	}
	if row.ScimEnabled {
		scim := &scimSettings{}
		scim.Token, scim.hasToken = digestOf(row.ScimTokenSha256)
		if row.ScimPreviousExpiresAt != nil {
			scim.Previous, scim.hasPrevious = digestOf(row.ScimPreviousTokenSha256)
			if scim.hasPrevious {
				scim.PreviousExpiresAt = *row.ScimPreviousExpiresAt
			}
		}
		s.SCIM = scim
	}
	return s
}

// readSSOSettings reads the row on q, uncached. No row — before
// EnsureSsoSettings ever ran — is everything off, version 0.
func readSSOSettings(ctx context.Context, q *store.Queries, box *secrets.Box) (ssoSettings, error) {
	row, err := q.GetSsoSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return ssoSettings{}, nil
	}
	if err != nil {
		return ssoSettings{}, fmt.Errorf("identity: read the single sign-on settings: %w", err)
	}
	return parseSSOSettings(row, box), nil
}

// ssoSettingsReader is the row through a cache of maintenanceCache's
// period (system.go): re-read at most every ssoSettingsCacheTTL of clock,
// which a test moves with h.advance, and evicted by the process that writes
// it. Access holds the one reader, which the user directory shares.
//
// Unlike maintenanceCache it never holds its mutex across the database
// read: the lock is taken only to check and to store. Callers read it on
// every request, and a holder waiting on the mutex while the fetcher waits
// for a pool connection would be a deadlock on an exhausted pool. So two
// readers that find it stale at once may both fetch; the row is a single
// small read, which is cheaper than a single-flight. Nothing reads through
// it inside a transaction that already holds a connection.
type ssoSettingsReader struct {
	q     *store.Queries
	clock func() time.Time
	box   *secrets.Box

	mu        sync.Mutex
	cached    ssoSettings
	fetchedAt time.Time
	fresh     bool
	// evictions counts evict calls, so a fetch that began before one does
	// not store what it read as fresh.
	evictions uint64
}

// newSSOSettingsReader is a reader over pool, timed by clock, whose client
// secret opens with box.
func newSSOSettingsReader(pool *pgxpool.Pool, clock func() time.Time, box *secrets.Box) *ssoSettingsReader {
	return &ssoSettingsReader{q: store.New(pool), clock: clock, box: box}
}

// get answers the cached settings while they were read within
// ssoSettingsCacheTTL, else reads the row again, with no lock held. What it
// read is stored unless a fetch begun later already stored, or the reader
// was evicted meanwhile. A failed read is an error, never "off", and is
// not cached.
func (r *ssoSettingsReader) get(ctx context.Context) (ssoSettings, error) {
	now := r.clock()
	r.mu.Lock()
	if r.fresh && now.Sub(r.fetchedAt) < ssoSettingsCacheTTL {
		cached := r.cached
		r.mu.Unlock()
		return cached, nil
	}
	evictions := r.evictions
	r.mu.Unlock()

	s, err := readSSOSettings(ctx, r.q, r.box)
	if err != nil {
		return ssoSettings{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.evictions == evictions && (!r.fresh || !now.Before(r.fetchedAt)) {
		r.cached, r.fetchedAt, r.fresh = s, now, true
	}
	return s, nil
}

// scimEnabled is whether SCIM is on, which makes a directory's
// deactivation of a non-Owner count.
func (r *ssoSettingsReader) scimEnabled(ctx context.Context) (bool, error) {
	s, err := r.get(ctx)
	return s.SCIM != nil, err
}

// evict makes the next get read the row, and keeps a fetch already under
// way from storing what it read.
func (r *ssoSettingsReader) evict() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fresh = false
	r.evictions++
}

// importedVariablesAudit is sso-settings.imported's after: the names of the
// variables imported, never their values.
type importedVariablesAudit struct {
	Variables []string `json:"variables"`
}

// ssoEnvironmentVariables names, sorted, the OIDC_* and SCIM_* variables
// cfg carries a value for — never the values. The display name config
// defaults counts as unset. A token file config took from
// AZURE_FEDERATED_TOKEN_FILE is named OIDC_WORKLOAD_IDENTITY_TOKEN_FILE.
func ssoEnvironmentVariables(cfg *config.Config) []string {
	var names []string
	if cfg == nil {
		return names
	}
	if o := cfg.OIDC; o != nil {
		set := map[string]bool{
			"OIDC_PROVIDER":                     o.Provider != "",
			"OIDC_AUTHORITY":                    o.Authority != "",
			"OIDC_CLIENT_ID":                    o.ClientID != "",
			"OIDC_CLIENT_SECRET":                o.ClientSecret != "",
			"OIDC_WORKLOAD_IDENTITY_TOKEN_FILE": o.WorkloadTokenFile != "",
			"OIDC_ALLOWED_DOMAINS":              len(o.AllowedDomains) > 0,
			"OIDC_DISPLAY_NAME":                 o.DisplayName != "" && o.DisplayName != defaultOIDCDisplayName,
		}
		for name, ok := range set {
			if ok {
				names = append(names, name)
			}
		}
	}
	if s := cfg.SCIM; s != nil {
		if s.Token != "" {
			names = append(names, "SCIM_TOKEN")
		}
		if s.PreviousToken != "" {
			names = append(names, "SCIM_PREVIOUS_TOKEN")
		}
		if !s.PreviousTokenExpiresAt.IsZero() {
			names = append(names, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT")
		}
	}
	slices.Sort(names)
	return names
}

// The longest values identity.sso_settings holds, in characters, beside
// maxProviderDisplayNameLength: the varchar limits of the authority, the
// client id and the token file path.
const (
	maxAuthorityLength     = 200
	maxClientIDLength      = 200
	maxTokenFilePathLength = 500
)

// importCap is a variable the import could not store as it was: cut to
// the column's length (a display name), or — a value that cannot be cut —
// the reason the OIDC block was imported off.
type importCap struct {
	variable string
	cut      bool
}

// importedSSOSettings is the row the environment's cfg imports into at now:
// the client secret sealed under oidcClientSecretPurpose, the tokens
// digested, imported_from_environment true and no user behind it. A value
// longer than the row holds never stops the import: a display name is cut
// to maxProviderDisplayNameLength, and an authority, a client id or a
// token file path that does not fit — none of which can be cut — leaves
// the OIDC block off, for the Owner to set on the card. Each is answered as
// an importCap, by the variable's name.
func importedSSOSettings(cfg *config.Config, box *secrets.Box, now time.Time) (store.SaveSsoSettingsParams, []importCap, error) {
	p := store.SaveSsoSettingsParams{ImportedFromEnvironment: true, Now: now}
	var caps []importCap
	if o := cfg.OIDC; o != nil {
		for _, field := range []struct {
			variable, value string
			limit           int
		}{
			{"OIDC_AUTHORITY", o.Authority, maxAuthorityLength},
			{"OIDC_CLIENT_ID", o.ClientID, maxClientIDLength},
			{"OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", o.WorkloadTokenFile, maxTokenFilePathLength},
		} {
			if utf8.RuneCountInString(field.value) > field.limit {
				caps = append(caps, importCap{variable: field.variable})
			}
		}
		if len(caps) == 0 {
			p.OidcEnabled = true
			p.OidcProvider, p.OidcAuthority, p.OidcClientID = &o.Provider, &o.Authority, &o.ClientID
			p.OidcAllowedDomains = slices.Clone(o.AllowedDomains)
			if name := o.DisplayName; name != "" {
				if utf8.RuneCountInString(name) > maxProviderDisplayNameLength {
					name = string([]rune(name)[:maxProviderDisplayNameLength])
					caps = append(caps, importCap{variable: "OIDC_DISPLAY_NAME", cut: true})
				}
				p.OidcDisplayName = &name
			}
			if o.WorkloadTokenFile != "" {
				p.OidcWorkloadTokenFile = &o.WorkloadTokenFile
			}
			if o.ClientSecret != "" {
				if box == nil {
					return p, nil, errors.New("identity: no secrets box to seal the OIDC client secret with")
				}
				sealed, err := box.Seal(oidcClientSecretPurpose, []byte(o.ClientSecret))
				if err != nil {
					return p, nil, fmt.Errorf("identity: seal the OIDC client secret: %w", err)
				}
				p.OidcClientSecretSealed, p.OidcClientSecretSetAt = sealed, &now
			}
		}
	}
	if s := cfg.SCIM; s != nil {
		p.ScimEnabled = true
		token := sha256.Sum256([]byte(s.Token))
		p.ScimTokenSha256, p.ScimTokenSetAt = token[:], &now
		if s.PreviousToken != "" {
			previous := sha256.Sum256([]byte(s.PreviousToken))
			expires := s.PreviousTokenExpiresAt
			p.ScimPreviousTokenSha256, p.ScimPreviousExpiresAt = previous[:], &expires
		}
	}
	return p, caps, nil
}

// importSSOSettingsFromEnvironment imports d's OIDC_* and SCIM_* settings
// into identity.sso_settings, once, in one transaction holding the row: a
// row never written (version 0) while either block is configured is saved
// from it, audited sso-settings.imported with the variables' names and no
// actor, and a warning names them for removal. A row already written
// imports nothing: the row wins, and a variable still set is named in a
// warning that it is ignored. Two API processes starting together import
// once, since the second finds the row written. Only identity's mount — an
// API process — calls it; a worker never imports.
//
// The same transaction records the authority mode the row it holds derives
// (recordAuthorityModeIn), whether it imported or found the row written, so
// the mode is recorded from the row as committed — never from a snapshot a
// process read before another's save — and a rolling start cannot write a
// false authority-mode.changed. The locks are taken in the order every
// save takes them: sso_settings, then system_settings.
func importSSOSettingsFromEnvironment(ctx context.Context, d module.Deps) error {
	names := ssoEnvironmentVariables(d.Config)
	now := d.Clock()
	imported := false
	var caps []importCap
	err := db.WithTx(ctx, d.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.EnsureSsoSettings(ctx, now); err != nil {
			return err
		}
		row, err := q.LockSsoSettings(ctx)
		if err != nil {
			return err
		}
		if row.Version == 0 && d.Config != nil && (d.Config.OIDC != nil || d.Config.SCIM != nil) {
			var params store.SaveSsoSettingsParams
			params, caps, err = importedSSOSettings(d.Config, d.Secrets, now)
			if err != nil {
				return err
			}
			if err := q.SaveSsoSettings(ctx, params); err != nil {
				return err
			}
			imported = true
			if err := writeAudit(ctx, q, nil, now, auditEvent{
				action: "sso-settings.imported",
				after:  importedVariablesAudit{Variables: names},
			}); err != nil {
				return err
			}
			if row, err = q.GetSsoSettings(ctx); err != nil {
				return err
			}
		}
		if err := recordAuthorityModeIn(ctx, q, deriveAuthorityMode(parseSSOSettings(row, nil)), now); err != nil {
			return fmt.Errorf("identity: record the authority mode: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("identity: import the single sign-on settings from the environment: %w", err)
	}
	for _, c := range caps {
		if c.cut {
			d.Logger.Warn(fmt.Sprintf("identity: %s is longer than the %d characters the single sign-on settings hold; "+
				"it was imported cut to them", c.variable, maxProviderDisplayNameLength), "variable", c.variable)
			continue
		}
		d.Logger.Warn("identity: the OIDC provider was imported off, since "+c.variable+" is longer than the single "+
			"sign-on settings hold; set the provider on the Single sign-on card", "variable", c.variable)
	}
	switch {
	case imported:
		d.Logger.Warn("identity: the OIDC and SCIM settings were imported from the environment into the database; "+
			"the variables are now ignored and should be removed", "variables", names)
	case len(names) > 0:
		d.Logger.Warn("identity: these OIDC and SCIM variables are ignored, since the single sign-on settings "+
			"are kept in the database; remove them", "variables", names)
	}
	return nil
}
