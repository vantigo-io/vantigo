package telemetry

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// HTTPHandler wraps the process handler in a server span per API request.
// It must be the outermost layer, so httpx.TraceID — and with it every
// problem response and request-log line — carries the span's trace id. Health
// probes and SPA/static requests are not traced: at probe frequency they would
// drown the useful spans. Nor is the live socket (live design L2), which
// would become a span hours long.
//
// The listener is public, so an incoming traceparent never decides anything
// (Kubernetes readiness KR25): any client on the internet could otherwise
// force every request to be sampled, or graft our spans onto its trace. The
// server span is always a new root, sampled by the root sampler
// OTEL_TRACES_SAMPLER names, and the incoming context, when there is one, is
// recorded as a span link. The span is named "http.server" until the router
// matches the route; SetRoute then renames it to the route pattern (KR23).
func HTTPHandler(h http.Handler, basePath string) http.Handler {
	return otelhttp.NewHandler(h, "http.server",
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return IsAPIPath(r.URL.Path, basePath) && strings.TrimPrefix(r.URL.Path, basePath) != livePath
		}))
}

// SetRoute names the request's server span and its metrics after the
// matched route (KR23): the span becomes "GET /api/v1/invoices/{id}" and
// both carry http.route, never the raw path, which holds IDs and would make
// every request its own series. method and route are the router's own
// registration ("GET", "/api/v1/invoices/{id}"). Without a span or outside
// HTTPHandler it does nothing.
func SetRoute(ctx context.Context, method, route string) {
	attr := attribute.String("http.route", route)
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetName(method + " " + route)
		span.SetAttributes(attr)
	}
	if l, ok := otelhttp.LabelerFromContext(ctx); ok {
		l.Add(attr)
	}
}

// livePath is the live socket's path (live.Path), excluded from tracing.
const livePath = "/api/v1/live"

// IsAPIPath reports whether path, as received (still carrying basePath), is
// in the /api namespace.
func IsAPIPath(path, basePath string) bool {
	p := strings.TrimPrefix(path, basePath)
	return p == "/api" || strings.HasPrefix(p, "/api/")
}
