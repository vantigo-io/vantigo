package telemetry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const outboundScope = "github.com/vantigo-io/vantigo/server/internal/telemetry/outbound"

var (
	outboundDuration, _ = otel.Meter(outboundScope).Float64Histogram("http.client.request.duration",
		metric.WithDescription("Outbound HTTP requests, until the response headers, by method, host and status."),
		metric.WithUnit("s"))
)

// OutboundTransport wraps base — http.DefaultTransport when nil — so every
// request through it is a client span with its method, host, port and
// status, and a point on http.client.request.duration (Kubernetes readiness
// KR20). Every http.Client the server builds outside a test goes through it;
// TestEveryHTTPClientIsTraced holds that line.
//
// It records no URL path or query string, which carry IDs, tokens and
// search terms (the attribute policy, KR24), and no error text, which for a
// transport failure repeats the URL: only error.type. It injects no
// traceparent either: our trace IDs are ours, not every provider's.
// Wrapping a transport this returned returns it unchanged, so a client
// built over another's Transport is not traced twice.
func OutboundTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*outbound); ok {
		return base
	}
	return &outbound{base: base}
}

type outbound struct{ base http.RoundTripper }

func (o *outbound) RoundTrip(req *http.Request) (*http.Response, error) {
	host, port := req.URL.Hostname(), req.URL.Port()
	if port == "" {
		port = "443"
		if req.URL.Scheme == "http" {
			port = "80"
		}
	}
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", req.Method),
		attribute.String("server.address", host),
	}
	if n, err := strconv.Atoi(port); err == nil {
		attrs = append(attrs, attribute.Int("server.port", n))
	}
	ctx, span := otel.Tracer(outboundScope).Start(req.Context(), req.Method,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	defer span.End()
	start := time.Now()

	resp, err := o.base.RoundTrip(req.WithContext(ctx))

	metricAttrs := []attribute.KeyValue{attrs[0], attrs[1]}
	if err != nil {
		kind := errorType(err)
		span.SetAttributes(attribute.String("error.type", kind))
		span.SetStatus(codes.Error, kind)
		metricAttrs = append(metricAttrs, attribute.String("error.type", kind))
	} else {
		status := attribute.Int("http.response.status_code", resp.StatusCode)
		span.SetAttributes(status)
		metricAttrs = append(metricAttrs, status)
		if resp.StatusCode >= 400 {
			kind := strconv.Itoa(resp.StatusCode)
			span.SetAttributes(attribute.String("error.type", kind))
			if resp.StatusCode >= 500 {
				span.SetStatus(codes.Error, kind)
			}
		}
	}
	outboundDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(metricAttrs...))
	return resp, err
}

// errorType names a transport failure without its message.
func errorType(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	default:
		return "transport"
	}
}

// Base is the transport OutboundTransport wrapped, or rt itself when rt is
// not one it returned: for a test that pins which transport a client dials
// through.
func Base(rt http.RoundTripper) http.RoundTripper {
	if o, ok := rt.(*outbound); ok {
		return o.base
	}
	return rt
}
