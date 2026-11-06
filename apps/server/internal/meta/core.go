package meta

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/meta/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// The sealed secrets' purposes (module/thing).
const (
	purposeAppSecret = "meta/app-secret"
	purposePageToken = "meta/page-token"
)

// inboundScope is the storage scope the attachments fetched from Meta's CDN
// are kept under until Communications has them.
const inboundScope = "meta-inbound"

// webhookPath is the webhook's path under the app's base.
const webhookPath = "/api/v1/meta/webhook"

// core is what the module's HTTP operations (server), its ChannelMessenger
// (messenger) and its workers all hold: the pool, the queries, the clock, the
// logger and the Box.
type core struct {
	deps   module.Deps
	q      *store.Queries
	logger *slog.Logger
}

// newCore builds the shared part over d.
func newCore(d module.Deps) core {
	return core{deps: d, q: store.New(d.Pool), logger: loggerOf(d)}
}

// now is the module's clock.
func (c core) now() time.Time {
	if c.deps.Clock != nil {
		return c.deps.Clock()
	}
	return time.Now()
}

// settings reads the installation's one settings row.
func (c core) settings(ctx context.Context) (store.MetaSetting, error) {
	s, err := c.q.GetSettings(ctx)
	if err != nil {
		return store.MetaSetting{}, fmt.Errorf("meta: read the settings: %w", err)
	}
	return s, nil
}

// appSecret opens the sealed app secret; nil, false while none is stored.
func (c core) appSecret(s store.MetaSetting) ([]byte, bool, error) {
	if len(s.AppSecretSealed) == 0 {
		return nil, false, nil
	}
	secret, err := c.deps.Secrets.Open(purposeAppSecret, s.AppSecretSealed)
	if err != nil {
		return nil, false, fmt.Errorf("meta: open the app secret: %w", err)
	}
	return secret, true, nil
}

// pageToken opens a Page's sealed access token.
func (c core) pageToken(p store.MetaPage) (string, error) {
	token, err := c.deps.Secrets.Open(purposePageToken, p.TokenSealed)
	if err != nil {
		return "", fmt.Errorf("meta: open a Page's token: %w", err)
	}
	return string(token), nil
}

// configured is whether the app id and the secret are both stored.
func configured(s store.MetaSetting) bool {
	return s.AppID != nil && *s.AppID != "" && len(s.AppSecretSealed) > 0
}

// recordGraphError keeps the last refusal of the Graph API for the status
// line: a code, and Meta's words cut to 200 characters.
func (c core) recordGraphError(ctx context.Context, code, message string) {
	if r := []rune(message); len(r) > 200 {
		message = string(r[:200])
	}
	now := c.now()
	if err := c.q.RecordGraphError(ctx, store.RecordGraphErrorParams{Code: &code, Message: &message, Now: &now}); err != nil {
		c.logger.WarnContext(ctx, "meta: the last Graph error could not be recorded", "err", err)
	}
}

// inboundStore is the object store the attachments fetched from Meta's CDN
// are kept in: Deps.ObjectStore when a test harness set one, otherwise the
// storage provider the instance is configured with, under the scope
// meta-inbound.
func inboundStore(d module.Deps) (storage.ObjectStore, error) {
	if d.ObjectStore != nil {
		return d.ObjectStore, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("meta: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, inboundScope)
	if err != nil {
		return nil, fmt.Errorf("meta: scope the object store: %w", err)
	}
	return scoped, nil
}
