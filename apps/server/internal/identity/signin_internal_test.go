package identity

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

var internalNow = time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)

// newInternalServer is identity's server over its own migrated database, a
// development configuration and a fixed clock, for tests that call an
// operation's parts directly.
func newInternalServer(t *testing.T) (*server, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	clock := func() time.Time { return internalNow }
	cfg := &config.Config{Env: config.Development, Sessions: config.SessionConfig{
		Idle: 8 * time.Hour, PrivilegedIdle: 2 * time.Hour, Absolute: 24 * time.Hour, PrivilegedAbsolute: 8 * time.Hour,
	}}
	d := module.Deps{
		Config:  cfg,
		Pool:    pool,
		Logger:  slog.New(slog.DiscardHandler),
		Clock:   clock,
		Limiter: ratelimit.NewWithClock(pool, clock),
	}
	srv, err := newServer(NewAccess(d), d)
	if err != nil {
		t.Fatal(err)
	}
	return srv, pool
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestCompletePasswordLogin_TheLockWinsTheRace proves the success path
// re-checks the lockout it cannot have seen: the account was read unlocked,
// then parallel wrong guesses locked it (lockout_end in the future, the
// count reset by the lock) before the right password's success path ran.
// That path answers 429 account_locked, starts no session and leaves the
// throttle as it was. An account whose lockout has already ended at now
// signs in, and its failure count is cleared.
func TestCompletePasswordLogin_TheLockWinsTheRace(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	ctx := context.Background()
	insert := func(email string, lockoutEnd time.Time, failures int) store.GetLoginCandidateRow {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, display_name, lockout_end, failed_login_count, version, created_at, updated_at)
		                             VALUES ($1, $2, upper($2), $2, $3, $4, $5, $6, $6)`, id, email, lockoutEnd, failures, uuid.New(), internalNow); err != nil {
			t.Fatal(err)
		}
		// The row as GetLoginCandidate read it before the lock landed.
		return store.GetLoginCandidateRow{ID: id, Email: email, DisplayName: email, RoleNames: []string{}}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil)

	locked := insert("locked@example.test", internalNow.Add(lockoutDuration), 0)
	const throttle = "throttle-key|198.18.0.1"
	if _, err := srv.deps.Limiter.Hit(ctx, policyLoginAttempts, throttle); err != nil {
		t.Fatal(err)
	}
	res, err := srv.completePasswordLogin(ctx, req, locked, throttle, internalNow)
	if err != nil {
		t.Fatal(err)
	}
	refused, ok := res.(gen.PostIdentityLogin429JSONResponse)
	if !ok || refused.Body.Error.Code != "account_locked" || refused.Body.Error.Message != accountLockedMessage || refused.Headers.RetryAfter != nil {
		t.Fatalf("locked account: response %#v, want 429 account_locked", res)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, locked.ID); n != 0 {
		t.Errorf("%d sessions for the locked account, want none", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM platform.rate_limit WHERE key = $1`, "login-attempts:"+throttle); n != 1 {
		t.Error("the refused sign-in cleared the throttle")
	}

	expired := insert("expired@example.test", internalNow, 3) // the lock ends exactly now
	res, err = srv.completePasswordLogin(ctx, req, expired, throttle, internalNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.(loginOK); !ok {
		t.Fatalf("expired lockout: response %#v, want the 200", res)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM identity.sessions WHERE user_id = $1`, expired.ID); n != 1 {
		t.Errorf("%d sessions after signing in, want 1", n)
	}
	if n := countRows(t, pool, `SELECT failed_login_count FROM identity.users WHERE id = $1`, expired.ID); n != 0 {
		t.Errorf("failed_login_count = %d, want it cleared", n)
	}
}

// TestNewServerComputesTheDummyHashUpFront proves the dummy password hash
// exists as soon as the server does, a valid Argon2id PHC string, and that
// checkPassword never counts a dummy verification as a match, not even for
// the dummy's own password.
func TestNewServerComputesTheDummyHashUpFront(t *testing.T) {
	t.Parallel()
	srv, _ := newInternalServer(t)
	if _, err := verifyPassword(srv.dummyPasswordHash, "anything"); err != nil {
		t.Fatalf("dummyPasswordHash %q is not a hash identity wrote: %v", srv.dummyPasswordHash, err)
	}
	if ok, err := srv.checkPassword(nil, dummyPassword); ok || err != nil {
		t.Errorf("checkPassword(no hash, the dummy's password) = %v, %v; want false, nil", ok, err)
	}
}

// TestSystemAdminGrantIsTriedFourTimes pins RunStartup's attempts at .NET's
// MaxEnsureAttempts (SV/SystemAdminBootstrapper.cs:22).
func TestSystemAdminGrantIsTriedFourTimes(t *testing.T) {
	if systemAdminAttempts != 4 {
		t.Errorf("systemAdminAttempts = %d, want 4", systemAdminAttempts)
	}
}
