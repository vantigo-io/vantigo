package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// The one-time import of the environment's branding (brands design B30).
// APP_TITLE, APP_LOGO_URL and APP_SUPPORT_* are read once into the default
// brand, the way OIDC_* became the single sign-on row; the variables stay
// honoured, injected into the index exactly as before, until the import has
// run to completion (imported_env_at), so a release rolled back before then
// loses nothing.
//
// Creating the default brand is race-safe across replicas: an INSERT … ON
// CONFLICT on the unique partial index on is_default. The logo is fetched off
// the boot path, by a runner started after the API is composed, through a
// guarded transport: http(s) only, no redirect followed, 15 seconds, 2 MiB.

const (
	brandLogoFetchTimeout = 15 * time.Second
	brandLogoFetchMax     = 2 << 20
	// brandImportLease is how long a claimed logo import is left alone before
	// another start may take it over (a replica that died mid-import).
	brandImportLease = 2 * time.Minute
	// brandImportRetry is how often a runner that does not hold the lease
	// looks again.
	brandImportRetry = 30 * time.Second
)

// ensureDefaultBrand creates the default brand when there is none, from the
// built-in Vantigo values and whichever environment variables are set. It
// reports whether this call created it; a replica that lost the race reads the
// row the winner wrote.
func ensureDefaultBrand(ctx context.Context, d module.Deps, env config.Branding) (bool, error) {
	v, dropped := envBrand(env)
	for _, field := range dropped {
		d.Logger.WarnContext(ctx, "identity: an environment support contact is not valid and was not imported", "field", field)
	}
	now := d.Clock()
	params := store.InsertDefaultBrandParams{Published: v.marshal(), NameKey: nameKey(v.Name), Now: now}
	if env.LogoURL == "" {
		// Nothing left to do: the import is complete as it is created.
		params.ImportedEnvAt = &now
	}
	_, err := store.New(d.Pool).InsertDefaultBrand(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("identity: create the default brand: %w", err)
	}
	return true, nil
}

// envBrand is the built-in brand with the environment's title and support
// contacts. Support is validated by the same code as the editor's (B37); a
// contact that fails is left out and named.
func envBrand(env config.Branding) (brandVersion, []string) {
	title := strings.TrimSpace(env.Title)
	if utf8.RuneCountInString(title) > brandTitleMax {
		title = string([]rune(title)[:brandTitleMax])
	}
	name := builtinBrandName
	if title != "" {
		name = title
	}
	support, bad := validateSupport(brandSupport{Email: env.SupportEmail, Phone: env.SupportPhone, URL: env.SupportURL})
	var dropped []string
	for field := range bad {
		dropped = append(dropped, field)
		switch field {
		case "email":
			support.Email = ""
		case "phone":
			support.Phone = ""
		case "url":
			support.URL = ""
		}
	}
	v, refusal := newBrandVersion(brandInput{Name: name, Title: title, Primary: builtinPrimary, Radius: defaultBrandRadius, Support: support}, nil)
	if refusal != nil {
		panic("identity: the imported brand is invalid: " + refusal.message) // impossible: the inputs are validated above
	}
	return v, dropped
}

// BrandingImported reports whether the default brand exists and has imported
// the environment's branding to completion: the web index honours the
// variables until it has (B17).
func BrandingImported(ctx context.Context, d module.Deps) (bool, error) {
	imported, err := store.New(d.Pool).BrandingImported(ctx)
	if err != nil {
		return false, fmt.Errorf("identity: read whether the branding was imported: %w", err)
	}
	return imported, nil
}

// envBrandingSet reports whether any of the variables the import reads is set.
func envBrandingSet(b config.Branding) bool {
	return b.Title != "" || b.LogoURL != "" || b.SupportEmail != "" || b.SupportPhone != "" || b.SupportURL != ""
}

// importBrandingOnMount creates the default brand at mount and logs the
// import's state. The logo is not fetched here: a slow host must not block boot.
func importBrandingOnMount(ctx context.Context, d module.Deps, m *brandMetrics) error {
	created, err := ensureDefaultBrand(ctx, d, d.Config.Branding)
	if err != nil {
		return err
	}
	if created && envBrandingSet(d.Config.Branding) {
		m.imports.Add(ctx, 1, importOutcomeAttr("started"))
		d.Logger.InfoContext(ctx, "identity: the default brand was created from the environment's branding")
	}
	imported, err := BrandingImported(ctx, d)
	if err != nil {
		return err
	}
	if imported && envBrandingSet(d.Config.Branding) {
		d.Logger.WarnContext(ctx, "identity: APP_TITLE, APP_LOGO_URL and APP_SUPPORT_* are set but were imported into the default brand once and are ignored since; edit the brand at /workspace/theme and remove the variables")
	}
	return nil
}

// brandImportRunner fetches APP_LOGO_URL into the default brand (B30.2). It is
// an API runner: it starts after the API is composed, and holds the import's
// lease, so one replica runs the fetch.
type brandImportRunner struct {
	d       module.Deps
	a       *Access
	objects brandObjects
	metrics *brandMetrics
	wait    time.Duration
}

func brandImportRunners(a *Access, d module.Deps) []module.Runner {
	if d.Config.Branding.LogoURL == "" {
		return nil
	}
	objects, err := newBrandObjects(d)
	if err != nil {
		d.Logger.Error("identity: the brand logo import cannot reach the object store", "error", err.Error())
		return nil
	}
	return []module.Runner{&brandImportRunner{d: d, a: a, objects: objects, metrics: a.brandMetricsFor(), wait: brandImportRetry}}
}

func (r *brandImportRunner) Name() string { return "identity-brand-import" }

// Run takes the lease and imports the logo, or waits for whoever holds it and
// takes over if they die, until the import is complete.
func (r *brandImportRunner) Run(ctx context.Context) error {
	for {
		done, err := r.once(ctx)
		if err != nil {
			r.d.Logger.WarnContext(ctx, "identity: the brand logo import could not run", "error", err.Error())
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.wait):
		}
	}
}

// once is one look: it claims the import and runs it, or finds it complete.
func (r *brandImportRunner) once(ctx context.Context) (bool, error) {
	q := store.New(r.d.Pool)
	now := r.d.Clock()
	id, err := q.ClaimBrandImport(ctx, store.ClaimBrandImportParams{Now: now, StaleBefore: now.Add(-brandImportLease)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		imported, err := BrandingImported(ctx, r.d)
		return imported, err
	case err != nil:
		return false, fmt.Errorf("identity: claim the brand import: %w", err)
	}
	reason := r.importLogo(ctx, id.String())
	if err := ctx.Err(); err != nil {
		return false, nil // stopping: the lease goes stale and a later start takes over
	}
	var notice *string
	if reason != "" {
		n := "APP_LOGO_URL could not be imported (" + reason + "): upload the logo instead."
		notice = &n
		r.metrics.imports.Add(ctx, 1, importOutcomeAttr("logo_failed"))
		r.d.Logger.WarnContext(ctx, "identity: the brand logo could not be imported from APP_LOGO_URL", "reason", reason)
	}
	if err := q.CompleteBrandImport(ctx, store.CompleteBrandImportParams{ID: id, Now: r.d.Clock(), Notice: notice}); err != nil {
		return false, fmt.Errorf("identity: complete the brand import: %w", err)
	}
	r.metrics.imports.Add(ctx, 1, importOutcomeAttr("completed"))
	return true, nil
}

// The reasons a logo import fails, said to the admin: no URL, address or
// error text from the network.
const (
	importUnreachable = "the address could not be reached"
	importBlocked     = "the address is not allowed"
	importRedirect    = "the address redirects"
	importStatus      = "the address did not answer 200"
	importTooLarge    = "the file is over 2 MiB"
	importNotImage    = "the file is not a PNG or JPEG logo within the limits"
	importNoStorage   = "this installation has no object store"
	importTimeout     = "the address did not answer in time"
)

// importLogo fetches, judges and stores the logo and puts it in the brand's
// mark_light slot, published (and in the draft, if there is one). It answers
// "" on success and otherwise the reason.
func (r *brandImportRunner) importLogo(ctx context.Context, brandID string) string {
	if !r.objects.configured {
		return importNoStorage
	}
	data, reason := r.fetchLogo(ctx, r.d.Config.Branding.LogoURL)
	if reason != "" {
		return reason
	}
	logo, refusal, err := storeBrandLogo(ctx, r.objects, "mark_light", data)
	if refusal != nil {
		if refusal.code == codeStorageOff {
			return importNoStorage
		}
		return importNotImage
	}
	if err != nil {
		return importNoStorage
	}
	if err := r.putLogo(ctx, brandID, logo); err != nil {
		r.d.Logger.ErrorContext(ctx, "identity: the imported brand logo could not be recorded", "error", err.Error())
		return importUnreachable
	}
	return ""
}

// putLogo records the logo on the default brand, in one transaction.
func (r *brandImportRunner) putLogo(ctx context.Context, brandID string, logo brandLogo) error {
	now := r.d.Clock()
	return db.WithTx(ctx, r.d.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		raw, err := q.GetDefaultBrand(ctx)
		if err != nil {
			return err
		}
		b, err := loadBrandRow(raw)
		if err != nil {
			return err
		}
		if err := registerLogo(ctx, q, logo, now); err != nil {
			return err
		}
		put := func(v *brandVersion) {
			if v != nil {
				if _, has := v.Logos["mark_light"]; !has {
					v.Logos[logoSlotLight] = logo
				}
			}
		}
		put(b.pub)
		put(b.draft)
		params := store.SetImportedBrandLogoParams{ID: raw.ID, Now: now}
		if b.pub != nil {
			params.Published = b.pub.marshal()
		}
		if b.draft != nil {
			params.Draft = b.draft.marshal()
		}
		if err := q.SetImportedBrandLogo(ctx, params); err != nil {
			return err
		}
		return writeAudit(ctx, q, nil, now, auditEvent{
			system: "system:brand-import", action: "brand.logo_set",
			before: map[string]any{"id": raw.ID}, after: map[string]any{"id": raw.ID, "slot": logoSlotLight},
		})
	})
}

const logoSlotLight = "mark_light"

// fetchLogo reads the logo at raw through the guarded transport (B30.2): http
// or https only, no redirect followed, 15 seconds, 2 MiB read. A same-origin
// path is read from the app's own public address.
func (r *brandImportRunner) fetchLogo(ctx context.Context, raw string) ([]byte, string) {
	target := raw
	if strings.HasPrefix(raw, "/") {
		target = strings.TrimRight(r.d.Config.AppOrigin, "/") + r.d.Config.BasePath + raw
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, importBlocked
	}
	ctx, cancel := context.WithTimeout(ctx, brandLogoFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, importBlocked
	}
	req.Header.Set("User-Agent", "Vantigo-Brand-Import/1")
	req.Header.Set("Accept", "image/png, image/jpeg")
	resp, err := r.client().Do(req)
	if err != nil {
		switch {
		case errors.Is(err, netguard.ErrDisallowed):
			return nil, importBlocked
		case errors.Is(err, context.DeadlineExceeded):
			return nil, importTimeout
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, importTimeout
		}
		return nil, importUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, importRedirect
	case resp.StatusCode != http.StatusOK:
		return nil, importStatus
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, brandLogoFetchMax+1))
	switch {
	case err != nil:
		return nil, importUnreachable
	case len(data) > brandLogoFetchMax:
		return nil, importTooLarge
	}
	return data, ""
}

// client is the guarded client: the webhook sender's transport shape, built
// over the test's network when Access has one, with no proxy and no redirects.
func (r *brandImportRunner) client() *http.Client {
	n := webhookNetwork{}
	if r.a.brandNet != nil {
		n = *r.a.brandNet
	}
	return &http.Client{
		Transport:     webhookGuardedTransport(n),
		Timeout:       brandLogoFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
