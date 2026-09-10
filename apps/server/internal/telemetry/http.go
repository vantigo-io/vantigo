package telemetry

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPHandler wraps the process handler in a server span per API request.
// It must be the outermost layer, so httpx.TraceID — and with it every
// problem response and request-log line — carries the span's trace id. Health
// probes and SPA/static requests are not traced: at probe frequency they would
// drown the useful spans.
func HTTPHandler(h http.Handler, basePath string) http.Handler {
	return otelhttp.NewHandler(h, "http.server", otelhttp.WithFilter(func(r *http.Request) bool {
		return IsAPIPath(r.URL.Path, basePath)
	}))
}

// IsAPIPath reports whether path, as received (still carrying basePath), is
// in the /api namespace.
func IsAPIPath(path, basePath string) bool {
	p := strings.TrimPrefix(path, basePath)
	return p == "/api" || strings.HasPrefix(p, "/api/")
}
