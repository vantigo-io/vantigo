package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

type requestIDKey struct{}

// RequestID gives every request an id shaped like a W3C trace id (32 hex
// characters), so TraceID has something to report when no span is recording.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [16]byte
		_, _ = rand.Read(b[:])
		ctx := context.WithValue(r.Context(), requestIDKey{}, hex.EncodeToString(b[:]))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TraceID returns the id that correlates a response with its log lines: the
// OpenTelemetry trace id when a span is active, otherwise the id RequestID
// assigned, otherwise "".
func TraceID(r *http.Request) string {
	if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
		return sc.TraceID().String()
	}
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return id
}
