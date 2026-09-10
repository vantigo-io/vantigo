// Package httpx holds the HTTP plumbing every handler shares: RFC 7807
// problem responses, request and trace ids, panic recovery, request logging,
// forwarded-header trust and base-path mounting.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// The only details a failed request can carry. Nothing from an error ever
// reaches a caller; the trace id correlates the response with the log line
// that has the real cause.
const (
	UnexpectedErrorDetail = "The request could not be completed. Quote the trace id when reporting this problem."
	ConflictDetail        = "The request conflicts with data that already exists. Verify the values and try again."
	TooLargeDetail        = "The request body is too large."
)

// Problem is an RFC 7807 problem document in the shape the frontend API
// client parses (packages/frontend-api-client).
type Problem struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Detail  string `json:"detail,omitempty"`
	TraceID string `json:"traceId"`
}

// problemTypes are ASP.NET Core's defaults, so responses keep the shape the
// .NET host produced.
var problemTypes = map[int]struct{ typ, title string }{
	http.StatusBadRequest:            {"https://tools.ietf.org/html/rfc9110#section-15.5.1", "Bad Request"},
	http.StatusUnauthorized:          {"https://tools.ietf.org/html/rfc9110#section-15.5.2", "Unauthorized"},
	http.StatusForbidden:             {"https://tools.ietf.org/html/rfc9110#section-15.5.4", "Forbidden"},
	http.StatusNotFound:              {"https://tools.ietf.org/html/rfc9110#section-15.5.5", "Not Found"},
	http.StatusMethodNotAllowed:      {"https://tools.ietf.org/html/rfc9110#section-15.5.6", "Method Not Allowed"},
	http.StatusConflict:              {"https://tools.ietf.org/html/rfc9110#section-15.5.10", "Conflict"},
	http.StatusRequestEntityTooLarge: {"https://tools.ietf.org/html/rfc9110#section-15.5.14", "Content Too Large"},
	http.StatusUnprocessableEntity:   {"https://tools.ietf.org/html/rfc9110#section-15.5.21", "Unprocessable Entity"},
	http.StatusTooManyRequests:       {"https://tools.ietf.org/html/rfc6585#section-4", "Too Many Requests"},
	http.StatusInternalServerError:   {"https://tools.ietf.org/html/rfc9110#section-15.6.1", "An error occurred while processing your request."},
	http.StatusServiceUnavailable:    {"https://tools.ietf.org/html/rfc9110#section-15.6.4", "Service Unavailable"},
}

// WriteProblem writes a problem response for status. detail may be empty.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	meta, ok := problemTypes[status]
	if !ok {
		meta.typ, meta.title = "about:blank", http.StatusText(status)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:    meta.typ,
		Title:   meta.title,
		Status:  status,
		Detail:  detail,
		TraceID: TraceID(r),
	})
}

// NotFound is the /api catch-all: an unknown API path answers 404 and never
// falls through to the SPA's index.html.
func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteProblem(w, r, http.StatusNotFound, "")
}

// WriteError turns a handler error into a sanitised problem. Unique and
// exclusion violations are the database backstop behind a handler's friendly
// pre-check — two concurrent requests can both pass it — so the loser gets an
// expected 409, not a server fault.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	logger := slog.Default()
	var pgErr *pgconn.PgError
	var tooLarge *http.MaxBytesError

	switch {
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		// The caller is gone: there is no one to answer and nothing to alert on.
		logger.DebugContext(ctx, "request aborted by the caller", "method", r.Method, "path", r.URL.Path)
	case errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23P01"):
		logger.WarnContext(ctx, "request conflicts with existing data",
			"method", r.Method, "path", r.URL.Path, "constraint", pgErr.ConstraintName, "trace_id", TraceID(r))
		WriteProblem(w, r, http.StatusConflict, ConflictDetail)
	case errors.As(err, &tooLarge):
		WriteProblem(w, r, http.StatusRequestEntityTooLarge, TooLargeDetail)
	default:
		logger.ErrorContext(ctx, "request failed",
			"method", r.Method, "path", r.URL.Path, "error", err, "trace_id", TraceID(r))
		WriteProblem(w, r, http.StatusInternalServerError, UnexpectedErrorDetail)
	}
}
