package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

var traceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestRequestID_GivesEveryRequestADistinctTraceShapedID(t *testing.T) {
	var ids []string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ids = append(ids, TraceID(r))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	for _, id := range ids {
		if !traceIDShape.MatchString(id) {
			t.Errorf("id %q is not 32 lower-case hex characters", id)
		}
	}
	if ids[0] == ids[1] {
		t.Error("two requests got the same id")
	}
}

func TestTraceID_PrefersTheActiveSpan(t *testing.T) {
	traceID := trace.TraceID{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{1}})

	var got string
	RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = TraceID(r)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(trace.ContextWithSpanContext(context.Background(), sc)))

	if got != traceID.String() {
		t.Errorf("TraceID = %q, want the span's %q", got, traceID.String())
	}
}

func TestTraceID_EmptyWithoutASpanOrRequestID(t *testing.T) {
	if got := TraceID(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("TraceID = %q, want empty", got)
	}
}
