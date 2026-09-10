// Package ratelimit is a fixed-window rate limiter whose counters live in
// PostgreSQL (platform.rate_limit), so a limit holds across replicas and
// restarts rather than per process.
package ratelimit

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Policy is a named limit: at most Limit hits per client per Window.
type Policy struct {
	Name   string
	Limit  int
	Window time.Duration
}

func (p Policy) validate() error {
	if p.Name == "" || p.Limit < 1 || p.Window < time.Second {
		return fmt.Errorf("ratelimit: invalid policy %+v (need a name, a limit of at least 1 and a window of at least 1s)", p)
	}
	if contains(p.Name, ':') {
		return fmt.Errorf("ratelimit: invalid policy %+v (policy names may not contain ':')", p)
	}
	return nil
}

// contains reports whether the string s contains any byte equal to b.
func contains(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

// Decision is the outcome of one hit.
type Decision struct {
	Allowed bool
	// RetryAfter is the time left in the current window when a hit is rejected.
	RetryAfter time.Duration
}

// Limiter records hits in PostgreSQL.
type Limiter struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns a limiter on pool.
func New(pool *pgxpool.Pool) *Limiter {
	return &Limiter{pool: pool, now: time.Now}
}

// hitSQL counts one hit atomically: a new key or a newer window starts at 1,
// otherwise the counter increments. A hit stamped with an older window (a
// replica whose clock lags) counts toward the newest window recorded rather
// than resetting the counter or moving the window back. It returns the
// effective window start. Concurrent hits serialise on the row.
const hitSQL = `
INSERT INTO platform.rate_limit (key, window_start, hits)
VALUES ($1, $2, 1)
ON CONFLICT (key) DO UPDATE SET
    hits = CASE WHEN EXCLUDED.window_start > platform.rate_limit.window_start
                THEN 1
                ELSE platform.rate_limit.hits + 1 END,
    window_start = GREATEST(platform.rate_limit.window_start, EXCLUDED.window_start)
RETURNING hits, window_start`

// Allow records one hit by client under p and reports whether it is within
// the limit.
func (l *Limiter) Allow(ctx context.Context, p Policy, client string) (Decision, error) {
	if err := p.validate(); err != nil {
		return Decision{}, err
	}
	now := l.now().UTC()
	windowStart := now.Truncate(p.Window)

	var hits int
	var effectiveStart time.Time
	// Key construction: p.Name cannot contain ':' (validated above), so the first ':' is an unambiguous separator between policy and client.
	if err := l.pool.QueryRow(ctx, hitSQL, p.Name+":"+client, windowStart).Scan(&hits, &effectiveStart); err != nil {
		return Decision{}, fmt.Errorf("ratelimit: record hit: %w", err)
	}
	if hits <= p.Limit {
		return Decision{Allowed: true}, nil
	}
	return Decision{RetryAfter: effectiveStart.Add(p.Window).Sub(now)}, nil
}

// Middleware limits requests per client address (httpx.ClientIP, so it must
// run inside httpx.Forwarded). A counter that cannot be recorded fails the
// request closed with a 500 rather than letting it through unmetered.
func (l *Limiter) Middleware(p Policy) func(http.Handler) http.Handler {
	if err := p.validate(); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, err := l.Allow(r.Context(), p, httpx.ClientIP(r))
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if !d.Allowed {
				seconds := int(math.Ceil(d.RetryAfter.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(max(seconds, 1)))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				// frontend-api-client displays error.message; the code is for callers that branch on it.
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"Too many attempts. Please try again later."}}` + "\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
