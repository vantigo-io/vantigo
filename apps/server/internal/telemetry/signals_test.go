package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	isolate(t)
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return recorder
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestBuildResource_Tenant(t *testing.T) {
	isolate(t)
	res, err := buildResource(context.Background(), Options{TenantID: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := res.Set().Value(TenantAttribute); v.AsString() != "acme" {
		t.Errorf("%s = %q, want acme", TenantAttribute, v.AsString())
	}

	res, err = buildResource(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Set().Value(TenantAttribute); ok {
		t.Errorf("%s is set without a tenant", TenantAttribute)
	}

	// OTEL_RESOURCE_ATTRIBUTES still overrides (KR17).
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", TenantAttribute+"=other")
	res, err = buildResource(context.Background(), Options{TenantID: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := res.Set().Value(TenantAttribute); v.AsString() != "other" {
		t.Errorf("%s = %q, want the environment's other", TenantAttribute, v.AsString())
	}
}

func TestNewLogger_TenantAndTraceIDsOnStdout(t *testing.T) {
	recordSpans(t)
	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelInfo, false, "acme").With("k", "v")

	ctx, span := otel.Tracer("test").Start(context.Background(), "work")
	logger.InfoContext(ctx, "inside")
	span.End()
	logger.Info("outside")
	logger.InfoContext(ctx, "own", "trace_id", "mine")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3:\n%s", len(lines), buf.String())
	}
	var inside, outside map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &inside)
	_ = json.Unmarshal([]byte(lines[1]), &outside)
	sc := span.SpanContext()
	if inside["tenant"] != "acme" || inside["k"] != "v" || inside["trace_id"] != sc.TraceID().String() || inside["span_id"] != sc.SpanID().String() {
		t.Errorf("inside a span: %s", lines[0])
	}
	if outside["tenant"] != "acme" || outside["trace_id"] != nil {
		t.Errorf("outside a span: %s", lines[1])
	}
	if strings.Count(lines[2], `"trace_id"`) != 1 || !strings.Contains(lines[2], `"trace_id":"mine"`) {
		t.Errorf("a record naming its own trace_id gets a second one: %s", lines[2])
	}

	buf.Reset()
	NewLogger(&buf, slog.LevelInfo, false, "").Info("unnamed")
	if strings.Contains(buf.String(), `"tenant"`) {
		t.Errorf("tenant is logged without a tenant ID: %s", buf.String())
	}
}

func TestOutboundTransport_SpanWithoutPathQueryOrTraceparent(t *testing.T) {
	recorder := recordSpans(t)
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	client := &http.Client{Transport: OutboundTransport(nil)}
	ctx, parent := otel.Tracer("test").Start(context.Background(), "request")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/people/ola@example.com?token=secret", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	parent.End()

	if got.Get("traceparent") != "" || got.Get("tracestate") != "" {
		t.Errorf("the provider received a trace context: %v", got)
	}
	var client0 sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			client0 = s
		}
	}
	if client0 == nil {
		t.Fatal("no client span")
	}
	if client0.Name() != "GET" || client0.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("span %q, parent %v", client0.Name(), client0.Parent())
	}
	if v, _ := spanAttr(client0, "http.response.status_code"); v.AsInt64() != http.StatusTeapot {
		t.Errorf("status = %v", v)
	}
	if v, _ := spanAttr(client0, "server.address"); v.AsString() != "127.0.0.1" {
		t.Errorf("server.address = %v", v)
	}
	for _, kv := range client0.Attributes() {
		if s := kv.Value.String(); strings.Contains(s, "secret") || strings.Contains(s, "ola@") || strings.Contains(s, "/people") {
			t.Errorf("attribute %s = %q leaks the URL", kv.Key, s)
		}
	}
}

func TestOutboundTransport_WrapsOnce(t *testing.T) {
	rt := OutboundTransport(nil)
	if OutboundTransport(rt) != rt {
		t.Error("wrapping a wrapped transport wraps it again")
	}
}

func TestHTTPHandler_AnIncomingTraceparentIsALinkNotAParent(t *testing.T) {
	recorder := recordSpans(t)
	h := HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), r.Method, "/api/v1/invoices/{id}")
	}), "")
	remote := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/invoices/42", nil)
	req.Header.Set("traceparent", remote)
	h.ServeHTTP(httptest.NewRecorder(), req)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans", len(spans))
	}
	s := spans[0]
	if s.Parent().IsValid() || s.SpanContext().TraceID().String() == "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("the incoming context became the parent: %v", s.Parent())
	}
	if len(s.Links()) != 1 || s.Links()[0].SpanContext.TraceID().String() != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("links = %v, want the incoming context", s.Links())
	}
	if s.Name() != "GET /api/v1/invoices/{id}" {
		t.Errorf("name = %q, want the route pattern", s.Name())
	}
	if v, _ := spanAttr(s, "http.route"); v.AsString() != "/api/v1/invoices/{id}" {
		t.Errorf("http.route = %v", v)
	}
}
