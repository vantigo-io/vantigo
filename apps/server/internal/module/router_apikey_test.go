package module

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// recordingAccess is a fakeAccess that is also the request log
// (contracts.APIRequestRecorder), as identity's Access is (API access
// design K35).
type recordingAccess struct {
	fakeAccess
	mu       sync.Mutex
	recorded []contracts.APIRequest
}

func (a *recordingAccess) Record(_ context.Context, req contracts.APIRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.recorded = append(a.recorded, req)
}

func keyPrincipal(integration, key uuid.UUID, limit int) contracts.Principal {
	return contracts.Principal{UserID: integration, Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: key, RateLimitPerMinute: limit}}
}

// An admitted key request is counted against its integration's limit, its
// response carries the RateLimit headers, and it is recorded with its
// operation and status — never its path.
func TestRouter_AKeyRequestIsLimitedAndRecorded(t *testing.T) {
	integration, key := uuid.New(), uuid.New()
	access := &recordingAccess{}
	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return keyPrincipal(integration, key, 2), nil
	}
	r := newValidRouter(t, &access.fakeAccess, nil)
	r.opts.Access, r.recorder = access, access

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x/permission?secret=1", nil)
		req.RemoteAddr = "203.0.113.7:1000"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	for i, want := range []string{"1", "0"} {
		rec := get()
		if rec.Code != http.StatusNoContent || rec.Header().Get("RateLimit-Limit") != "2" || rec.Header().Get("RateLimit-Remaining") != want || rec.Header().Get("RateLimit-Reset") == "" {
			t.Fatalf("request %d: %d %v, want 204 with the headers, %s remaining", i+1, rec.Code, rec.Header(), want)
		}
	}
	rec := get()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || rec.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("third request: %d %v, want 429 with Retry-After and the headers", rec.Code, rec.Header())
	}
	if len(access.recorded) != 3 {
		t.Fatalf("%d recorded, want 3", len(access.recorded))
	}
	for i, got := range access.recorded {
		want := http.StatusNoContent
		reason := ""
		if i == 2 {
			want, reason = http.StatusTooManyRequests, contracts.APIReasonRateLimited
		}
		if got.KeyID != key || got.IntegrationID != integration || got.OperationID != "getPermission" || got.Method != http.MethodGet ||
			got.Status != want || got.Reason != reason || got.ClientIP != "203.0.113.7" {
			t.Errorf("recorded %d: %+v, want %d %q", i+1, got, want, reason)
		}
	}
}

// A refused key identity found is recorded with its reason and the status
// Reject wrote; a key identity did not find, and a person's request, are not.
func TestRouter_ARefusedKeyIsRecordedWithItsReason(t *testing.T) {
	integration, key := uuid.New(), uuid.New()
	access := &recordingAccess{}
	var answer error
	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, answer
	}
	r := newValidRouter(t, &access.fakeAccess, nil)
	r.opts.Access, r.recorder = access, access
	get := func() int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x/permission", nil))
		return rec.Code
	}

	answer = &contracts.APIKeyRefusal{KeyID: key, IntegrationID: integration, Reason: contracts.APIReasonNever, Err: contracts.ErrAPINotAvailable}
	if got := get(); got != http.StatusForbidden {
		t.Fatalf("a refusal: %d, want 403", got)
	}
	answer = contracts.ErrUnauthenticated // an unknown key
	if got := get(); got != http.StatusUnauthorized {
		t.Fatalf("an unknown key: %d, want 401", got)
	}
	if len(access.recorded) != 1 {
		t.Fatalf("%d recorded, want only the refusal", len(access.recorded))
	}
	if got := access.recorded[0]; got.KeyID != key || got.IntegrationID != integration || got.Reason != "never" || got.Status != http.StatusForbidden || got.OperationID != "getPermission" {
		t.Errorf("recorded %+v", got)
	}

	// A person's request is neither limited nor recorded.
	answer = nil
	access.check = nil
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x/permission", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("RateLimit-Limit") != "" || len(access.recorded) != 1 {
		t.Errorf("a person's request: %d %v, %d recorded", rec.Code, rec.Header(), len(access.recorded))
	}
}

// Without a recorder (an Access that is not one), a key request is still
// limited and nothing fails.
func TestRouter_NoRecorderStillLimits(t *testing.T) {
	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return keyPrincipal(uuid.New(), uuid.New(), 60), nil
	}}
	r := newValidRouter(t, access, nil)
	if r.recorder != nil {
		t.Fatal("fakeAccess is no recorder")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x/permission", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get("RateLimit-Limit") != "60" {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
}

// keyRouter is a router over fourOpsContract whose permission operation runs
// h, for an API key of a recording Access.
func keyRouter(t *testing.T, h http.HandlerFunc) (*Router, *recordingAccess) {
	t.Helper()
	access := &recordingAccess{}
	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return keyPrincipal(uuid.New(), uuid.New(), 600), nil
	}
	r := newValidRouter(t, &access.fakeAccess, nil)
	r.opts.Access, r.recorder = access, access
	r.routes = map[string][]*route{}
	r.normalized = map[string]string{}
	r.HandleFunc("GET /api/v1/x/permission", h)
	return r, access
}

// A HEAD is served by the GET operation and recorded as a HEAD of it.
func TestRouter_AKeysHEADIsRecordedAsItsGET(t *testing.T) {
	r, access := keyRouter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/api/v1/x/permission", nil))
	if rec.Code != http.StatusOK || len(access.recorded) != 1 ||
		access.recorded[0].Method != http.MethodHead || access.recorded[0].OperationID != "getPermission" || access.recorded[0].At.IsZero() {
		t.Errorf("%d, recorded %+v", rec.Code, access.recorded)
	}
}

// A streamed answer flushes through the status recorder as it would without
// it, and is recorded once it has finished.
func TestRouter_AKeysStreamedAnswerStillFlushes(t *testing.T) {
	r, access := keyRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush: %v", err)
		}
		_, _ = w.Write([]byte("second\n"))
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x/permission", nil))
	if !rec.Flushed || rec.Body.String() != "first\nsecond\n" {
		t.Errorf("flushed %v, body %q", rec.Flushed, rec.Body.String())
	}
	if len(access.recorded) != 1 || access.recorded[0].Status != http.StatusOK {
		t.Errorf("recorded %+v", access.recorded)
	}
}

// A handler that panics is recorded as the 500 the recovery answers, and the
// panic goes on.
func TestRouter_APanickingKeyRequestIsRecordedAs500(t *testing.T) {
	r, access := keyRouter(t, func(http.ResponseWriter, *http.Request) { panic("boom") })
	func() {
		defer func() {
			if v := recover(); v != "boom" {
				t.Errorf("recovered %v, want the handler's panic", v)
			}
		}()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x/permission", nil))
	}()
	if len(access.recorded) != 1 || access.recorded[0].Status != http.StatusInternalServerError {
		t.Errorf("recorded %+v, want one 500", access.recorded)
	}
}
