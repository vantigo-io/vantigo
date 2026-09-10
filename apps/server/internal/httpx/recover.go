package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover turns a handler panic into a logged, sanitised 500 problem.
// http.ErrAbortHandler is re-panicked: it is net/http's own signal to abort
// the response silently.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				WriteProblem(w, r, http.StatusInternalServerError, UnexpectedErrorDetail)
			}()
			next.ServeHTTP(w, r)
		})
	}
}
