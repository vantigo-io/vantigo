package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

var login = Policy{Name: "login", Limit: 2, Window: time.Minute}

// newLimiter returns a limiter on a fresh database with a controllable clock
// starting 10 s into a minute window.
func newLimiter(t *testing.T) (*Limiter, *time.Time) {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	now := time.Date(2026, 9, 10, 12, 0, 10, 0, time.UTC)
	l := New(pool)
	l.now = func() time.Time { return now }
	return l, &now
}

func mustAllow(t *testing.T, l *Limiter, p Policy, client string) Decision {
	t.Helper()
	d, err := l.Allow(context.Background(), p, client)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	return d
}

func TestAllow_LimitsHitsWithinAWindow(t *testing.T) {
	l, _ := newLimiter(t)

	for i := range 2 {
		if d := mustAllow(t, l, login, "203.0.113.7"); !d.Allowed {
			t.Fatalf("hit %d rejected", i+1)
		}
	}
	d := mustAllow(t, l, login, "203.0.113.7")
	if d.Allowed {
		t.Fatal("third hit allowed with a limit of 2")
	}
	if d.RetryAfter != 50*time.Second {
		t.Errorf("RetryAfter = %v, want 50s (the rest of the window)", d.RetryAfter)
	}
}

func TestAllow_ANewWindowStartsAfresh(t *testing.T) {
	l, now := newLimiter(t)
	for range 3 {
		mustAllow(t, l, login, "203.0.113.7")
	}
	*now = now.Add(time.Minute)
	if d := mustAllow(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Error("first hit of the next window was rejected")
	}
}

// A replica whose clock lags must neither reset the counter nor move the
// window back: its hit counts toward the newest window already recorded.
func TestAllow_ALaggingClockCountsTowardTheCurrentWindow(t *testing.T) {
	l, now := newLimiter(t)
	lagging := *now                 // 12:00:10, window 12:00
	*now = lagging.Add(time.Minute) // 12:01:10, window 12:01
	for range 2 {
		mustAllow(t, l, login, "203.0.113.7")
	}

	*now = lagging
	d := mustAllow(t, l, login, "203.0.113.7")
	if d.Allowed {
		t.Error("a hit from a lagging clock reset the counter: third hit allowed with a limit of 2")
	}
	if want := 110 * time.Second; d.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v (until the current window ends, by this clock)", d.RetryAfter, want)
	}

	var windowStart time.Time
	var hits int
	if err := l.pool.QueryRow(context.Background(), "SELECT window_start, hits FROM platform.rate_limit WHERE key = $1",
		"login:203.0.113.7").Scan(&windowStart, &hits); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 10, 12, 1, 0, 0, time.UTC); !windowStart.Equal(want) || hits != 3 {
		t.Errorf("row = %v with %d hits, want %v with 3 (the window never moves back)", windowStart.UTC(), hits, want)
	}
}

func TestAllow_ClientsAndPoliciesAreCountedSeparately(t *testing.T) {
	l, _ := newLimiter(t)
	for range 3 {
		mustAllow(t, l, login, "203.0.113.7")
	}
	if !mustAllow(t, l, login, "198.51.100.1").Allowed {
		t.Error("another client was limited by the first client's hits")
	}
	other := Policy{Name: "password-recovery", Limit: 1, Window: time.Minute}
	if !mustAllow(t, l, other, "203.0.113.7").Allowed {
		t.Error("another policy was limited by the login policy's hits")
	}
}

func TestAllow_IsAtomicUnderConcurrency(t *testing.T) {
	l, _ := newLimiter(t)
	p := Policy{Name: "burst", Limit: 10, Window: time.Minute}

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := l.Allow(context.Background(), p, "203.0.113.7")
			if err != nil {
				t.Errorf("Allow: %v", err) // Errorf, not Fatalf: this is not the test goroutine
				return
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Errorf("%d concurrent hits allowed, want exactly 10", got)
	}
}

func TestAllow_RejectsAnInvalidPolicy(t *testing.T) {
	l, _ := newLimiter(t)
	for _, p := range []Policy{{Name: "", Limit: 1, Window: time.Minute}, {Name: "x", Limit: 0, Window: time.Minute}, {Name: "x", Limit: 1, Window: time.Millisecond}, {Name: "login:2001", Limit: 1, Window: time.Minute}} {
		if _, err := l.Allow(context.Background(), p, "c"); err == nil {
			t.Errorf("policy %+v accepted", p)
		}
	}
}

func TestMiddleware_RejectsWith429AndRetryAfter(t *testing.T) {
	l, _ := newLimiter(t)
	var calls int
	h := l.Middleware(Policy{Name: "login", Limit: 1, Window: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))

	serve := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve("203.0.113.7:1000"); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	rec := serve("203.0.113.7:2000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "50" {
		t.Errorf("Retry-After = %q, want 50", rec.Header().Get("Retry-After"))
	}
	// frontend-api-client shows error.message; the code alone renders nothing.
	if rec.Body.String() != `{"error":{"code":"rate_limited","message":"Too many attempts. Please try again later."}}`+"\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
	if rec := serve("198.51.100.1:1000"); rec.Code != http.StatusNoContent {
		t.Errorf("a different client was rejected: %d", rec.Code)
	}
}

func TestMiddleware_PanicsOnAnInvalidPolicy(t *testing.T) {
	l, _ := newLimiter(t)
	defer func() {
		if recover() == nil {
			t.Error("Middleware accepted a zero limit")
		}
	}()
	l.Middleware(Policy{Name: "x", Limit: 0, Window: time.Minute})
}

func mustBlocked(t *testing.T, l *Limiter, p Policy, client string) Decision {
	t.Helper()
	d, err := l.Blocked(context.Background(), p, client)
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}
	return d
}

func TestBlocked_AFreshKeyIsAllowedAndRecordsNothing(t *testing.T) {
	l, _ := newLimiter(t)
	if d := mustBlocked(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Fatal("a fresh key was blocked")
	}
	var count int
	if err := l.pool.QueryRow(context.Background(), "SELECT count(*) FROM platform.rate_limit").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("Blocked inserted %d rows, want 0", count)
	}
}

func TestBlocked_IsBlockedOnceTheLimitIsHit(t *testing.T) {
	l, _ := newLimiter(t)
	for range login.Limit {
		mustAllow(t, l, login, "203.0.113.7")
	}
	d := mustBlocked(t, l, login, "203.0.113.7")
	if d.Allowed {
		t.Fatal("blocked after the limit was hit, but Blocked allowed it")
	}
	if d.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", d.RetryAfter)
	}
}

func TestBlocked_ARowFromAnOlderWindowIsNotBlocked(t *testing.T) {
	l, now := newLimiter(t)
	for range login.Limit {
		mustAllow(t, l, login, "203.0.113.7")
	}
	*now = now.Add(time.Minute) // a fresh window; the old row is stale
	if d := mustBlocked(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Error("a row left over from an earlier window blocked the next one")
	}
}

func TestReset_ClearsTheCounter(t *testing.T) {
	l, _ := newLimiter(t)
	for range login.Limit {
		mustAllow(t, l, login, "203.0.113.7")
	}
	if err := l.Reset(context.Background(), login, "203.0.113.7"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if d := mustBlocked(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Error("Blocked still reports blocked after Reset")
	}
	if d := mustAllow(t, l, login, "203.0.113.7"); !d.Allowed {
		t.Error("Allow still limited after Reset")
	}
}

func TestHit_IsAllowUnderItsNewName(t *testing.T) {
	l, _ := newLimiter(t)
	d, err := l.Hit(context.Background(), login, "203.0.113.7")
	if err != nil {
		t.Fatalf("Hit: %v", err)
	}
	if !d.Allowed {
		t.Error("first hit rejected")
	}
}

func TestReject_DefaultBodyIsUnchanged(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
	Reject(rec, req, login, Decision{RetryAfter: 50 * time.Second})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := rec.Header().Get("Retry-After"); got != "50" {
		t.Errorf("Retry-After = %q, want 50", got)
	}
	want := `{"error":{"code":"rate_limited","message":"Too many attempts. Please try again later."}}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}

func TestReject_CustomMessageIsJSONEscapedInTheBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
	p := Policy{Name: "login", Limit: 1, Window: time.Minute, Message: `Too many attempts, "friend".`}
	Reject(rec, req, p, Decision{RetryAfter: 3 * time.Second})

	want := `{"error":{"code":"rate_limited","message":"Too many attempts, \"friend\"."}}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}

func TestReject_NoRetryAfterOmitsTheHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
	p := Policy{Name: "login", Limit: 1, Window: time.Minute, NoRetryAfter: true}
	Reject(rec, req, p, Decision{RetryAfter: 3 * time.Second})

	if _, ok := rec.Header()["Retry-After"]; ok {
		t.Errorf("Retry-After present: %q", rec.Header().Get("Retry-After"))
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestReject_RetryAfterRoundsUpWithAMinimumOfOneSecond(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
	Reject(rec, req, login, Decision{RetryAfter: 400 * time.Millisecond})

	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1 (rounded up, minimum 1)", got)
	}
}

func TestMiddleware_FailsClosedWhenTheCounterCannotBeRecorded(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	l := New(pool)
	pool.Close()

	var handlerCalled bool
	h := l.Middleware(Policy{Name: "login", Limit: 1, Window: time.Minute})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)
	req.RemoteAddr = "203.0.113.7:1000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if handlerCalled {
		t.Error("handler was called despite database error")
	}
}
