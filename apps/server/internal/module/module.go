// Package module is the platform every business module (identity, customers,
// products, energy, communications) mounts through: a router that enforces
// each operation's x-vantigo-access rule and rate limit at runtime, and a
// Compose that assembles the modules under /api.
package module

import (
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
}
