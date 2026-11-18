package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// RequestLog writes one line per request. It logs the path, never the query
// string: invitation, recovery and OIDC callback URLs carry secrets there.
// Health probes log at debug so a 10-second probe does not bury real traffic.
// It runs outside StripBasePath, so a probe is recognised both under
// basePath and at the root (container probes bypass the prefix).
func RequestLog(logger *slog.Logger, basePath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := NewStatusRecorder(w)
			next.ServeHTTP(rec, r)

			status := rec.Status()
			level := slog.LevelInfo
			if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/health/") {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.Bytes()),
				slog.Duration("duration", time.Since(start)),
				slog.String("client_ip", ClientIP(r)),
				slog.String("trace_id", TraceID(r)),
			)
		})
	}
}

// StatusRecorder captures the status and size of a response: RequestLog's
// line, and the router's API request log (API access design K23). Unwrap
// keeps http.ResponseController (flush, deadlines) working through it, so a
// streamed response streams as it would without it.
type StatusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// NewStatusRecorder returns a StatusRecorder over w.
func NewStatusRecorder(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{ResponseWriter: w}
}

// Status is the status written, 200 when the handler wrote a body without
// one or nothing at all, as net/http answers then.
func (s *StatusRecorder) Status() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// Bytes is the size of the body written.
func (s *StatusRecorder) Bytes() int64 { return s.bytes }

func (s *StatusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *StatusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *StatusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
