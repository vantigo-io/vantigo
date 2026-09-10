package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logOne(t *testing.T, h http.Handler, basePath string, req *http.Request) (map[string]any, string) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	Chain(h, RequestID, RequestLog(logger, basePath)).ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log line: %v (%q)", err, logs.String())
	}
	return entry, logs.String()
}

func TestRequestLog_RecordsTheOutcomeWithoutTheQueryString(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})
	entry, raw := logOne(t, h, "", httptest.NewRequest(http.MethodPost, "/api/v1/invitations/accept?token=secret-token", nil))

	if entry["msg"] != "http request" || entry["level"] != "INFO" {
		t.Errorf("msg/level = %v/%v", entry["msg"], entry["level"])
	}
	if entry["method"] != "POST" || entry["path"] != "/api/v1/invitations/accept" {
		t.Errorf("method/path = %v/%v", entry["method"], entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) || entry["bytes"] != float64(len("created")) {
		t.Errorf("status/bytes = %v/%v", entry["status"], entry["bytes"])
	}
	if id, _ := entry["trace_id"].(string); !traceIDShape.MatchString(id) {
		t.Errorf("trace_id = %v", entry["trace_id"])
	}
	// Query strings carry invitation and reset tokens; they never reach a log.
	if strings.Contains(raw, "secret-token") {
		t.Errorf("log contains the query string: %s", raw)
	}
}

func TestRequestLog_ImplicitOKIsRecordedAs200(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if entry, _ := logOne(t, h, "", httptest.NewRequest(http.MethodGet, "/", nil)); entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}

// RequestLog runs outside StripBasePath, so it sees the prefixed path.
func TestRequestLog_HealthProbesLogAtDebug(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, tc := range []struct{ base, path, want string }{
		{"", "/health/ready", "DEBUG"},
		{"/vantigo", "/vantigo/health/ready", "DEBUG"},
		// A probe outside the prefix passes through StripBasePath untouched.
		{"/vantigo", "/health/ready", "DEBUG"},
		{"/vantigo", "/other/health/ready", "INFO"},
	} {
		if entry, _ := logOne(t, h, tc.base, httptest.NewRequest(http.MethodGet, tc.path, nil)); entry["level"] != tc.want {
			t.Errorf("%s under base path %q: level = %v, want %s", tc.path, tc.base, entry["level"], tc.want)
		}
	}
}

func TestRequestLog_KeepsTheResponseControllerWorking(t *testing.T) {
	var flushErr error
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	})
	logOne(t, h, "", httptest.NewRequest(http.MethodGet, "/", nil))
	if flushErr != nil {
		t.Errorf("Flush through the log wrapper: %v", flushErr)
	}
}
