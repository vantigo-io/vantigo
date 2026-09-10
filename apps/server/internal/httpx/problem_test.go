package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v (body %q)", err, rec.Body.String())
	}
	return p
}

// captureDefaultLog swaps slog.Default for a buffer for one test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestWriteProblem_WritesAnRFC7807Document(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req = r
		WriteProblem(w, r, http.StatusNotFound, "No such customer.")
	})).ServeHTTP(rec, req)

	p := decodeProblem(t, rec)
	want := Problem{
		Type:    "https://tools.ietf.org/html/rfc9110#section-15.5.5",
		Title:   "Not Found",
		Status:  http.StatusNotFound,
		Detail:  "No such customer.",
		TraceID: TraceID(req),
	}
	if p != want {
		t.Errorf("problem = %+v, want %+v", p, want)
	}
	if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status/cache = %d/%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestWriteProblem_UnknownStatusUsesAboutBlankAndOmitsEmptyDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteProblem(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusTeapot, "")

	p := decodeProblem(t, rec)
	if p.Type != "about:blank" || p.Title != "I'm a teapot" {
		t.Errorf("type/title = %q/%q", p.Type, p.Title)
	}
	if strings.Contains(rec.Body.String(), `"detail"`) {
		t.Errorf("empty detail was serialised: %s", rec.Body.String())
	}
}

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	NotFound(rec, httptest.NewRequest(http.MethodGet, "/api/anything", nil))
	if p := decodeProblem(t, rec); p.Status != http.StatusNotFound {
		t.Errorf("status = %d", p.Status)
	}
}

func TestWriteError_MapsConstraintViolationsToConflict(t *testing.T) {
	captureDefaultLog(t)
	for _, code := range []string{"23505", "23P01"} {
		t.Run(code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := &pgconn.PgError{Code: code, ConstraintName: "users_email_key", Message: "duplicate key value"}
			WriteError(rec, httptest.NewRequest(http.MethodPost, "/api/x", nil), err)

			p := decodeProblem(t, rec)
			if p.Status != http.StatusConflict || p.Detail != ConflictDetail {
				t.Errorf("problem = %+v", p)
			}
			if strings.Contains(rec.Body.String(), "users_email_key") {
				t.Errorf("response leaks the constraint name: %s", rec.Body.String())
			}
		})
	}
}

func TestWriteError_MapsAnOversizedBodyTo413(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodPost, "/api/x", nil), &http.MaxBytesError{Limit: 10})
	if p := decodeProblem(t, rec); p.Status != http.StatusRequestEntityTooLarge || p.Detail != TooLargeDetail {
		t.Errorf("problem = %+v", p)
	}
}

func TestWriteError_SanitisesUnexpectedErrorsButLogsThem(t *testing.T) {
	logs := captureDefaultLog(t)
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil), errors.New("dial tcp: password hunter2 rejected"))

	p := decodeProblem(t, rec)
	if p.Status != http.StatusInternalServerError || p.Detail != UnexpectedErrorDetail {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaks the error: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "hunter2") {
		t.Errorf("the error was not logged: %s", logs.String())
	}
}

func TestWriteError_WritesNothingWhenTheCallerIsGone(t *testing.T) {
	captureDefaultLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil).WithContext(ctx), context.Canceled)

	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Errorf("wrote a response to a caller that left: %d %q", rec.Code, rec.Body.String())
	}
}
