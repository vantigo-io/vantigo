package module

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// fourOpsContract has one operation of each access kind the router must
// distinguish: anonymous, session, a policy, and a permission.
const fourOpsContract = `
openapi: 3.0.3
info:
  title: Test
  version: "1"
paths:
  /api/v1/x/anon:
    get:
      operationId: getAnon
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
  /api/v1/x/session:
    get:
      operationId: getSession
      x-vantigo-access: session
      responses:
        "204": { description: ok }
  /api/v1/x/policy:
    get:
      operationId: getPolicy
      x-vantigo-access: policy:SystemAdmin
      responses:
        "204": { description: ok }
  /api/v1/x/permission:
    get:
      operationId: getPermission
      x-vantigo-access: permission:identity:manage-access
      responses:
        "204": { description: ok }
`

// missingRuleContract has one operation with no x-vantigo-access extension.
const missingRuleContract = `
openapi: 3.0.3
info:
  title: Test
  version: "1"
paths:
  /api/v1/x/norule:
    get:
      operationId: getNoRule
      responses:
        "204": { description: ok }
`

func loadDoc(t *testing.T, yaml string) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(yaml))
	if err != nil {
		t.Fatalf("load test contract: %v", err)
	}
	return doc
}

func noopHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// fakeAccess is a contracts.Access that records every call, for the
// wrapping-order tests. check, when set, controls the outcome; otherwise
// Check succeeds with a zero Principal.
type fakeAccess struct {
	check func(*http.Request, contracts.Rule) (contracts.Principal, error)

	checked  []contracts.Rule
	rejected []rejection
}

type rejection struct {
	rule contracts.Rule
	err  error
}

func (f *fakeAccess) Check(r *http.Request, rule contracts.Rule) (contracts.Principal, error) {
	f.checked = append(f.checked, rule)
	if f.check != nil {
		return f.check(r, rule)
	}
	return contracts.Principal{}, nil
}

func (f *fakeAccess) Reject(w http.ResponseWriter, _ *http.Request, rule contracts.Rule, err error) {
	f.rejected = append(f.rejected, rejection{rule, err})
	status := http.StatusForbidden
	if errors.Is(err, contracts.ErrUnauthenticated) {
		status = http.StatusUnauthorized
	}
	w.WriteHeader(status)
}

// Registration problems (Err): an unknown pattern, a missing rule, a
// permission not in the catalog, and an operation that was never registered.
func TestRouter_RegistrationProblems(t *testing.T) {
	t.Run("unknown pattern", func(t *testing.T) {
		r := NewRouter(RouterOptions{Doc: loadDoc(t, fourOpsContract), Access: &fakeAccess{}})
		r.HandleFunc("GET /api/v1/x/does-not-exist", noopHandler)
		if err := r.Err(); err == nil {
			t.Fatal("Err() = nil, want a problem for the unknown pattern")
		}
	})

	t.Run("missing rule", func(t *testing.T) {
		r := NewRouter(RouterOptions{Doc: loadDoc(t, missingRuleContract), Access: &fakeAccess{}})
		r.HandleFunc("GET /api/v1/x/norule", noopHandler)
		if err := r.Err(); err == nil {
			t.Fatal("Err() = nil, want a problem for the missing x-vantigo-access")
		}
	})

	t.Run("permission not in catalog", func(t *testing.T) {
		r := NewRouter(RouterOptions{Doc: loadDoc(t, fourOpsContract), Access: &fakeAccess{}, Catalog: map[string]contracts.Permission{}})
		r.HandleFunc("GET /api/v1/x/permission", noopHandler)
		if err := r.Err(); err == nil {
			t.Fatal("Err() = nil, want a problem for the permission missing from Catalog")
		}
	})

	t.Run("unregistered operation", func(t *testing.T) {
		r := NewRouter(RouterOptions{Doc: loadDoc(t, fourOpsContract), Access: &fakeAccess{}})
		r.HandleFunc("GET /api/v1/x/anon", noopHandler)
		r.HandleFunc("GET /api/v1/x/session", noopHandler)
		r.HandleFunc("GET /api/v1/x/policy", noopHandler)
		// /api/v1/x/permission is never registered.
		if err := r.Err(); err == nil {
			t.Fatal("Err() = nil, want a problem for the never-registered operation")
		}
	})

	t.Run("Limits names an unknown operation", func(t *testing.T) {
		pool, _ := testdb.Migrated(t)
		r := NewRouter(RouterOptions{
			Doc:     loadDoc(t, fourOpsContract),
			Access:  &fakeAccess{},
			Limiter: ratelimit.New(pool),
			Limits:  map[string]ratelimit.Policy{"noSuchOperation": {Name: "test", Limit: 1, Window: time.Minute}},
		})
		r.HandleFunc("GET /api/v1/x/anon", noopHandler)
		r.HandleFunc("GET /api/v1/x/session", noopHandler)
		r.HandleFunc("GET /api/v1/x/policy", noopHandler)
		r.HandleFunc("GET /api/v1/x/permission", noopHandler)
		if err := r.Err(); err == nil {
			t.Fatal("Err() = nil, want a problem for the Limits key naming no operation")
		}
	})

	t.Run("a clean registration has no problems", func(t *testing.T) {
		r := NewRouter(RouterOptions{
			Doc:     loadDoc(t, fourOpsContract),
			Access:  &fakeAccess{},
			Catalog: map[string]contracts.Permission{"identity:manage-access": {Key: "identity:manage-access", Display: "d", Description: "d"}},
		})
		r.HandleFunc("GET /api/v1/x/anon", noopHandler)
		r.HandleFunc("GET /api/v1/x/session", noopHandler)
		r.HandleFunc("GET /api/v1/x/policy", noopHandler)
		r.HandleFunc("GET /api/v1/x/permission", noopHandler)
		if err := r.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
	})
}

func newValidRouter(t *testing.T, access *fakeAccess, limits map[string]ratelimit.Policy) *Router {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	r := NewRouter(RouterOptions{
		Doc:     loadDoc(t, fourOpsContract),
		Access:  access,
		Limiter: ratelimit.New(pool),
		Limits:  limits,
		Catalog: map[string]contracts.Permission{"identity:manage-access": {Key: "identity:manage-access", Display: "d", Description: "d"}},
	})
	r.HandleFunc("GET /api/v1/x/anon", noopHandler)
	r.HandleFunc("GET /api/v1/x/session", noopHandler)
	r.HandleFunc("GET /api/v1/x/policy", noopHandler)
	r.HandleFunc("GET /api/v1/x/permission", noopHandler)
	if err := r.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	return r
}

// The rate limit is checked before Access: a client past the limit never
// reaches Access.Check.
func TestRouter_RateLimitIsCheckedBeforeAccess(t *testing.T) {
	access := &fakeAccess{}
	r := newValidRouter(t, access, map[string]ratelimit.Policy{"getSession": {Name: "test-session", Limit: 1, Window: time.Minute}})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x/session", nil)
		req.RemoteAddr = "203.0.113.7:1000"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := get(); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: status = %d, want 204", rec.Code)
	}
	if len(access.checked) != 1 {
		t.Fatalf("after the first request, Access.Check was called %d times, want 1", len(access.checked))
	}

	rec := get()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", rec.Code)
	}
	if len(access.checked) != 1 {
		t.Errorf("Access.Check was called after the rate limit rejected the request: %d calls, want still 1", len(access.checked))
	}
}

// Reject receives the error Access.Check returned, whether it is
// ErrUnauthenticated or ErrForbidden.
func TestRouter_RejectReceivesTheAccessError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"unauthenticated", contracts.ErrUnauthenticated, http.StatusUnauthorized},
		{"forbidden", contracts.ErrForbidden, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
				return contracts.Principal{}, tc.err
			}}
			r := newValidRouter(t, access, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/x/session", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if len(access.rejected) != 1 {
				t.Fatalf("Reject was called %d times, want 1", len(access.rejected))
			}
			if !errors.Is(access.rejected[0].err, tc.err) {
				t.Errorf("Reject received %v, want %v", access.rejected[0].err, tc.err)
			}
		})
	}
}

// An infrastructure error (anything but ErrUnauthenticated/ErrForbidden)
// becomes a 500 problem, and Reject is never called.
func TestRouter_InfrastructureErrorIsA500(t *testing.T) {
	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, errors.New("database is on fire")
	}}
	r := newValidRouter(t, access, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x/session", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if len(access.rejected) != 0 {
		t.Errorf("Reject was called for an infrastructure error")
	}
}

// The Principal Access.Check returns reaches the generated handler through
// the request context.
func TestRouter_PrincipalReachesTheHandler(t *testing.T) {
	want := contracts.Principal{Roles: []string{"owner"}}
	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return want, nil
	}}

	pool, _ := testdb.Migrated(t)
	r := NewRouter(RouterOptions{Doc: loadDoc(t, fourOpsContract), Access: access, Limiter: ratelimit.New(pool)})

	var got contracts.Principal
	var ok bool
	r.HandleFunc("GET /api/v1/x/session", func(_ http.ResponseWriter, req *http.Request) {
		got, ok = contracts.PrincipalFrom(req.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x/session", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if !ok {
		t.Fatal("the handler saw no Principal in its context")
	}
	if len(got.Roles) != 1 || got.Roles[0] != "owner" {
		t.Errorf("Principal = %+v, want %+v", got, want)
	}
}
