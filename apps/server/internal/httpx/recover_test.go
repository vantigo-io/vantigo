package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecover_TurnsAPanicIntoASanitisedProblem(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("database password is hunter2")
	}), RequestID, Recover(logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))

	p := decodeProblem(t, rec)
	if p.Status != http.StatusInternalServerError || p.Detail != UnexpectedErrorDetail {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaks the panic value: %s", rec.Body.String())
	}
	for _, want := range []string{"hunter2", "goroutine", p.TraceID} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log is missing %q: %s", want, logs.String())
		}
	}
}

// Once the status and part of the body are on the wire, a problem document
// can only corrupt the response; net/http must abort the connection instead.
func TestRecover_AbortsAResponseThatHasAlreadyStarted(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("failed halfway through the body")
	}))

	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Errorf("recovered %v, want http.ErrAbortHandler", v)
			}
		}()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		t.Error("ServeHTTP returned; a started response must be aborted")
	}()

	if rec.Code != http.StatusOK || rec.Body.String() != "partial" {
		t.Errorf("response = %d %q, want the untouched 200 \"partial\"", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "failed halfway through the body") {
		t.Errorf("the panic was not logged: %s", logs.String())
	}
}

func TestRecover_LetsErrAbortHandlerThrough(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("ServeHTTP returned; the ErrAbortHandler panic must propagate to net/http")
}
