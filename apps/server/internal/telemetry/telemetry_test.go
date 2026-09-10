package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// isolate blanks every OTEL_* variable Setup reads and restores the global
// providers afterwards, so tests cannot leak into each other.
func isolate(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		t.Setenv(k, "")
	}
	for _, s := range []string{"TRACES", "METRICS", "LOGS"} {
		t.Setenv("OTEL_EXPORTER_OTLP_"+s+"_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_"+s+"_PROTOCOL", "")
	}
	t.Cleanup(func() {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		global.SetLoggerProvider(lognoop.NewLoggerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
}

// collector is a fake OTLP/HTTP endpoint that records which paths were posted to.
type collector struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newCollector(t *testing.T) *collector {
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		c.mu.Lock()
		c.paths = append(c.paths, r.URL.Path)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) received(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.paths, path)
}

func TestSetup_NothingConfiguredInstallsNothing(t *testing.T) {
	isolate(t)
	signals, shutdown, err := Setup(context.Background(), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if signals != (Signals{}) {
		t.Errorf("signals = %+v, want none", signals)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestSetup_SDKDisabledWins(t *testing.T) {
	isolate(t)
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	if signals, _, err := Setup(context.Background(), Options{}); err != nil || signals != (Signals{}) {
		t.Errorf("signals = %+v err = %v, want none", signals, err)
	}
}

func TestSetup_RejectsProtocolsOtherThanHTTPProtobuf(t *testing.T) {
	isolate(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "grpc")
	if _, _, err := Setup(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL") {
		t.Errorf("err = %v, want a rejection naming the variable", err)
	}
}

func TestSetup_ExportsEveryConfiguredSignal(t *testing.T) {
	isolate(t)
	c := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", c.URL)

	ctx := context.Background()
	signals, shutdown, err := Setup(ctx, Options{Version: "1.2.3", Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if signals != (Signals{Traces: true, Metrics: true, Logs: true}) {
		t.Fatalf("signals = %+v, want all three", signals)
	}

	_, span := otel.Tracer("test").Start(ctx, "work")
	span.End()
	counter, err := otel.Meter("test").Int64Counter("test.hits")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1)
	NewLogger(io.Discard, slog.LevelInfo, true).Info("hello")

	if err := shutdown(ctx); err != nil { // flushes every signal
		t.Fatalf("shutdown: %v", err)
	}
	for _, path := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		if !c.received(path) {
			t.Errorf("nothing was exported to %s (got %v)", path, c.paths)
		}
	}
}

func TestSetup_OnlyTheSignalWithAnEndpointIsExported(t *testing.T) {
	isolate(t)
	c := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", c.URL+"/v1/traces")

	signals, shutdown, err := Setup(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	if signals != (Signals{Traces: true}) {
		t.Errorf("signals = %+v, want traces only", signals)
	}
}

func TestIsAPIPath(t *testing.T) {
	for _, tc := range []struct {
		path, base string
		want       bool
	}{
		{"/api", "", true},
		{"/api/v1/customers", "", true},
		{"/vantigo/api/v1/customers", "/vantigo", true},
		{"/health/ready", "", false},
		{"/", "", false},
		{"/customers/42", "", false},
		{"/apiary", "", false},
		{"/assets/index-abc.js", "", false},
	} {
		if got := IsAPIPath(tc.path, tc.base); got != tc.want {
			t.Errorf("IsAPIPath(%q, %q) = %v, want %v", tc.path, tc.base, got, tc.want)
		}
	}
}

func TestHTTPHandler_TracesAPIRequestsOnlyAndExposesTheTraceID(t *testing.T) {
	isolate(t)
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	var seen string
	h := HTTPHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if IsAPIPath(r.URL.Path, "/vantigo") {
			seen = httpx.TraceID(r)
		}
	}), "/vantigo")
	for _, path := range []string{"/vantigo/api/v1/ping", "/health/ready", "/vantigo/customers"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans, want 1 (API requests only)", len(spans))
	}
	if want := spans[0].SpanContext().TraceID().String(); seen != want {
		t.Errorf("handler saw trace id %q, span has %q", seen, want)
	}
}

func TestNewLogger_WritesJSONAtTheConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelInfo, false)
	logger.Debug("hidden")
	logger.Info("shown", "k", "v")

	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), `"msg":"shown"`) || !strings.Contains(buf.String(), `"k":"v"`) {
		t.Errorf("log output = %s", buf.String())
	}
}

func TestFanout_SendsEachRecordToEveryHandlerAboveTheLevel(t *testing.T) {
	var a, b bytes.Buffer
	logger := slog.New(fanout{min: slog.LevelInfo, handlers: []slog.Handler{
		slog.NewJSONHandler(&a, &slog.HandlerOptions{Level: slog.LevelDebug}),
		slog.NewJSONHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug}),
	}}).With("service", "vantigo")
	logger.Debug("dropped")
	logger.Info("kept")

	for _, out := range []string{a.String(), b.String()} {
		if strings.Contains(out, "dropped") || !strings.Contains(out, `"msg":"kept"`) || !strings.Contains(out, `"service":"vantigo"`) {
			t.Errorf("handler output = %s", out)
		}
	}
}
