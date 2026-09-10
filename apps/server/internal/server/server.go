// Package server assembles the production HTTP handler from the platform
// packages. cmd/vantigo builds the Options; tests drive the whole stack
// through New exactly as production runs it.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/security"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

// Options are the collaborators the handler is assembled from.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	Index  *web.Index
	Assets fs.FS
	Health http.Handler
	// API serves everything under /api/ and receives the full path. Nil
	// answers every API path with a 404 problem.
	API http.Handler
}

// New returns the process's HTTP handler.
func New(o Options) http.Handler {
	api := o.API
	if api == nil {
		api = http.HandlerFunc(httpx.NotFound)
	}

	// CSRF: unsafe cross-site browser requests are rejected (Sec-Fetch-Site,
	// falling back to Origin against Host). APP_URL is trusted explicitly so a
	// proxy that rewrites Host does not turn same-origin requests into
	// rejections. Requests with neither header are non-browser clients and
	// pass. Session cookies are SameSite=Strict as the second layer.
	csrf := http.NewCrossOriginProtection()
	if err := csrf.AddTrustedOrigin(o.Config.AppOrigin); err != nil {
		panic("server: APP_URL is not a valid origin: " + err.Error()) // config.Load already validated it
	}
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteProblem(w, r, http.StatusForbidden, "Cross-origin requests are not allowed.")
	}))
	apiHandler := csrf.Handler(api)

	mux := http.NewServeMux()
	mux.Handle("/health/", o.Health)
	mux.Handle("/api/", apiHandler)
	mux.Handle("/api", apiHandler)
	mux.Handle("/", web.Handler(o.Assets, o.Index))

	return httpx.Chain(mux,
		httpx.Forwarded(o.Config.TrustedProxyHops),
		httpx.RequestID,
		httpx.RequestLog(o.Logger, o.Config.BasePath),
		httpx.Recover(o.Logger),
		security.Headers(security.HeaderOptions{
			InlineScriptHash: o.Index.InlineScriptHash,
			ReportOnly:       o.Config.CSPReportOnly,
			HSTS:             !o.Config.IsDevelopment(),
		}),
		security.HostFilter(security.AllowedHosts(o.Config.AppHostname)),
		httpx.StripBasePath(o.Config.BasePath),
	)
}
