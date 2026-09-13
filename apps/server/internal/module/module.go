// Package module is the platform every business module (identity, customers,
// products, energy, communications) mounts through: a router that enforces
// each operation's x-vantigo-access rule and rate limit at runtime, a
// Compose that assembles the modules under /api, and a Workers (workers.go)
// that collects the background workers the enabled modules contribute for
// cmd/vantigo's worker runner to start.
package module

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Deps are the dependencies every module's Mount can use.
type Deps struct {
	Config  *config.Config
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
	Clock   func() time.Time
	Mail    mail.Sender
	Secrets *secrets.Box
	Limiter *ratelimit.Limiter
	Access  contracts.Access
	Catalog map[string]contracts.Permission // the composed catalog
	// Doc is this module's own contract. Compose sets it, on a copy of Deps,
	// before calling Mount, by loading the module's own specs/<name>.yaml.
	Doc *openapi3.T
	// Directory is the customer directory, the one sanctioned way a module
	// reads another's data (see contracts.CustomerDirectory). Compose sets
	// it, on every module's Deps copy, from whichever enabled module
	// declares Module.Directory; it is nil when no enabled module provides
	// one.
	Directory contracts.CustomerDirectory
	// HTTPTransport is the RoundTripper a module's own outbound HTTP client
	// (customers' Brreg lookup, so far the only one) dials through. nil in
	// production, meaning http.DefaultTransport; a test harness sets it to a
	// fake so no test ever touches the network, the same shape as Clock
	// makes time controllable.
	HTTPTransport http.RoundTripper
	// HTTPBackoff is the delay a module's own outbound HTTP client waits
	// before retry attempt n (1-indexed). nil in production, meaning that
	// client's own real backoff; a test harness sets it (typically to a
	// function returning 0) so a retry loop's tests never actually sleep.
	HTTPBackoff func(attempt int) time.Duration
	// SMTPVerify is the function a module's own SMTP connectivity check
	// (communications' channel verification, so far the only one) calls to
	// connect, authenticate and disconnect against a caller-supplied SMTP
	// configuration — no message sent. nil in production, meaning
	// mail.VerifyConnection itself, which dials through internal/mail's
	// real DNS-rebinding guard; a test harness sets it to a fake so a
	// verify-succeeds test can exercise a handler's success path without a
	// live SMTP server or the guard's network reach, the same shape
	// HTTPTransport gives an outbound HTTP client.
	SMTPVerify func(ctx context.Context, cfg config.MailConfig, allowInsecure bool) error
	// SMTPSend is the function a module's own outbound mail path
	// (communications' outbox delivery worker, so far the only one) calls to
	// deliver one message through a *caller-supplied* SMTP configuration —
	// the channel's own stored credentials, not the process's configured
	// Mail sender, which is why this is a function taking a config rather
	// than another mail.Sender. nil in production, meaning mail.SendOutbound
	// itself, which dials through internal/mail's real DNS-rebinding guard;
	// a test harness sets it to a fake that records the envelope, the same
	// seam SMTPVerify gives the verify-only path.
	SMTPSend func(ctx context.Context, cfg config.MailConfig, allowInsecure bool, msg mail.Outbound) error
	// ObjectStore is the unscoped object store a module's own attachment
	// paths (communications' staging and download, so far the only ones)
	// put and get bytes through. nil in production, meaning that module
	// builds its own from Config.StorageProvider via internal/storage.New,
	// wrapped in its own storage.NewScope; a test harness sets it to a fake
	// so a storage-failure path (a 503) can be exercised deterministically,
	// the same seam HTTPTransport and SMTPVerify give an outbound
	// dependency a module cannot let a test touch for real.
	ObjectStore storage.ObjectStore
}

// Module is one business module: its name (the path segment it mounts under,
// /api/v1/<name>/), the permissions it contributes to the composed catalog,
// how to build its handler, and, optionally, the customer directory it
// provides to every other module.
type Module struct {
	Name        string
	Permissions []contracts.Permission
	Mount       func(Deps) (http.Handler, error)
	// Directory builds this module's contracts.CustomerDirectory
	// implementation, if it provides one. At most one enabled module may
	// set it; Compose calls it before any Mount runs and puts the result on
	// every module's Deps, including the provider's own.
	Directory func(Deps) contracts.CustomerDirectory
	// Workers builds this module's background workers (worker.Worker), if
	// it has any. Unlike Directory, any number of enabled modules may set
	// it; Workers (workers.go) resolves it from deps the same way — before
	// any Mount runs — for every module MODULES enables, and concatenates
	// the results in mods order. A module with none leaves it nil.
	Workers func(Deps) []worker.Worker
}
