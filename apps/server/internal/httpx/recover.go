package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a handler panic into a logged, sanitised 500 problem. If the
// handler had already started the response, a problem document could only be
// spliced onto it, so the panic is logged and re-raised as
// http.ErrAbortHandler: net/http aborts the connection and the client sees a
// truncated response instead of a corrupted one. http.ErrAbortHandler itself
// is re-panicked untouched: it is net/http's own signal to abort silently.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tracked := &startTracker{ResponseWriter: w}
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger.ErrorContext(r.Context(), "panic while serving a request",
					"method", r.Method, "path", r.URL.Path, "panic", v,
					"stack", string(debug.Stack()), "trace_id", TraceID(r))
				if tracked.started {
					panic(http.ErrAbortHandler)
				}
				WriteProblem(w, r, http.StatusInternalServerError, UnexpectedErrorDetail)
			}()
			next.ServeHTTP(tracked, r)
		})
	}
}

// startTracker records whether the response has started: a status or a body
// byte was written. Unwrap keeps http.ResponseController working through it.
type startTracker struct {
	http.ResponseWriter
	started bool
}

func (s *startTracker) WriteHeader(code int) {
	s.started = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *startTracker) Write(b []byte) (int, error) {
	s.started = true
	return s.ResponseWriter.Write(b)
}

func (s *startTracker) Unwrap() http.ResponseWriter { return s.ResponseWriter }
