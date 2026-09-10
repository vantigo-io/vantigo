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
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/health/") {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("client_ip", ClientIP(r)),
				slog.String("trace_id", TraceID(r)),
			)
		})
	}
}

// statusRecorder captures the status and size of a response. Unwrap keeps
// http.ResponseController (flush, deadlines) working through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
