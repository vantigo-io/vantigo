package staff_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// queueObject puts an object in the fake store and its row in the ledger,
// due at due and claimed until claimedUntil (nil for unclaimed), as a delete
// or an erase leaves it; it answers the key.
func queueObject(t *testing.T, h *harness, name string, due time.Time, claimedUntil *time.Time) string {
	t.Helper()
	key := "people/1/" + name
	if err := h.objects.Put(context.Background(), key, strings.NewReader("x"), "application/pdf"); err != nil {
		t.Fatalf("seed the object: %v", err)
	}
	h.Exec(t, `INSERT INTO staff.object_deletions (object_key, next_attempt_at, claimed_until, created_at) VALUES ($1, $2, $3, $4)`,
		key, due, claimedUntil, h.Now())
	return key
}

// ledgerRow is one ledger row's attempts, last error and next attempt; ok is
// false when the row is gone.
func ledgerRow(t *testing.T, h *harness, key string) (attempts int32, lastError string, next time.Time, ok bool) {
	t.Helper()
	err := h.Pool().QueryRow(context.Background(), `
		SELECT attempts, coalesce(last_error, ''), next_attempt_at FROM staff.object_deletions WHERE object_key = $1`, key).
		Scan(&attempts, &lastError, &next)
	if err != nil {
		return 0, "", time.Time{}, false
	}
	return attempts, lastError, next, true
}

// The ledger (design S15, M7): a pass claims every due row nobody else has
// claimed — a row another replica claimed, its window still open, is left to
// it, as is a row not due yet — and makes one attempt at each. A confirmed
// delete removes the row; a refusal keeps it, counted, with the constant
// reason (the store's own words stay in the log, since they may name the key)
// and the next attempt after the backoff, doubling, without a ceiling on the
// attempts; a key the store no longer has is a confirmed delete.
func TestLedger_ClaimsBacksOffAndFinishes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	now := h.Now()
	inAMinute := now.Add(time.Minute)
	due := queueObject(t, h, "due", now, nil)
	claimed := queueObject(t, h, "claimed", now, &inAMinute)
	expired := now.Add(-time.Second)
	abandoned := queueObject(t, h, "abandoned", now, &expired)
	later := queueObject(t, h, "later", now.Add(time.Hour), nil)
	h.Exec(t, `INSERT INTO staff.object_deletions (object_key, next_attempt_at, created_at) VALUES ('people/1/missing', $1, $1)`, now)

	if attempted, succeeded := runLedger(t, h); attempted != 3 || succeeded != 3 {
		t.Errorf("pass = %d attempted, %d confirmed; want the due, the abandoned and the missing", attempted, succeeded)
	}
	for _, key := range []string{due, abandoned, "people/1/missing"} {
		if _, _, _, ok := ledgerRow(t, h, key); ok || h.objects.has(key) {
			t.Errorf("%s: row or object left after a confirmed delete", key)
		}
	}
	for _, key := range []string{claimed, later} {
		if _, _, _, ok := ledgerRow(t, h, key); !ok || !h.objects.has(key) {
			t.Errorf("%s: touched, want it left for its claim or its time", key)
		}
	}

	// A store that refuses: the row stays, counted, and waits.
	h.Advance(time.Hour)
	h.objects.failDelete(errors.New("the bucket at s3://secret-key is down"))
	if attempted, succeeded := runLedger(t, h); attempted != 2 || succeeded != 0 {
		t.Errorf("pass over a refusing store = %d attempted, %d confirmed; want 2 and 0", attempted, succeeded)
	}
	attempts, reason, next, ok := ledgerRow(t, h, later)
	if !ok || attempts != 1 || reason != "the object store refused the delete" || !next.Equal(h.Now().Add(2*time.Second)) {
		t.Errorf("after one refusal = %d, %q, %s, %v; want 1, the constant reason, now + 2s", attempts, reason, next, ok)
	}
	runLedger(t, h)
	if a, _, _, _ := ledgerRow(t, h, later); a != 1 {
		t.Errorf("attempts = %d before the backoff ran, want the row left alone", a)
	}
	h.Advance(2 * time.Second)
	runLedger(t, h)
	if attempts, _, next, _ := ledgerRow(t, h, later); attempts != 2 || !next.Equal(h.Now().Add(4*time.Second)) {
		t.Errorf("after two refusals = %d, %s; want 2, now + 4s", attempts, next)
	}
	h.Exec(t, `UPDATE staff.object_deletions SET attempts = 40, next_attempt_at = $1 WHERE object_key = $2`, h.Now(), later)
	runLedger(t, h)
	if attempts, _, next, ok := ledgerRow(t, h, later); !ok || attempts != 41 || !next.Equal(h.Now().Add(time.Hour)) {
		t.Errorf("after forty-one refusals = %d, %s, %v; want the row kept, retried hourly", attempts, next, ok)
	}

	h.objects.failDelete(nil)
	h.Advance(time.Hour)
	runLedger(t, h)
	if _, _, _, ok := ledgerRow(t, h, later); ok || h.objects.has(later) {
		t.Error("the row outlived the store's recovery")
	}
	if logs := h.Logs(); !strings.Contains(logs, "secret-key") {
		t.Error("the store's own error is not in the log")
	}
}

// A ledger pass holds no connection while it calls the store (the plan's
// second discipline): each claim and each outcome is a statement of its own on
// the pool, so a store that hangs holds no connection hostage. On a pool of
// one the pass completes, and at every delete the pool has nothing acquired.
func TestLedger_CallsTheStoreHoldingNoConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, name := range []string{"a", "b", "c"} {
		queueObject(t, h, name, h.Now(), nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	var held []int32
	h.objects.whenDeleting(func(string) { held = append(held, one.Stat().AcquiredConns()) })
	deps := h.DepsOn(one)
	attempted, succeeded, err := staff.NewRetentionWorker(deps).RunLedger(ctx)
	if err != nil || attempted != 3 || succeeded != 3 {
		t.Fatalf("pass on a pool of one = %d, %d, %v; want 3, 3, nil", attempted, succeeded, err)
	}
	if len(held) != 3 || held[0] != 0 || held[1] != 0 || held[2] != 0 {
		t.Errorf("connections acquired at each delete = %v, want 0 every time", held)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.object_deletions`); n != 0 {
		t.Errorf("ledger rows = %d, want none", n)
	}
}

// Each claim is timed when it is taken, not when the pass began: a store slow
// enough that the clock moves between two claims still gives the second row
// its whole five-minute window, so no other pass takes it while it is tried.
func TestLedger_EachClaimIsTimedWhenTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := queueObject(t, h, "first", h.Now().Add(-2*time.Second), nil)
	second := queueObject(t, h, "second", h.Now().Add(-time.Second), nil)
	var claimedAt, claimedUntil time.Time
	h.objects.whenDeleting(func(key string) {
		switch key {
		case first:
			h.Advance(10 * time.Minute)
		case second:
			claimedAt = h.Now()
			claimedUntil = modtest.One[time.Time](t, h.Harness, `SELECT claimed_until FROM staff.object_deletions WHERE object_key = $1`, second)
		}
	})
	if attempted, succeeded := runLedger(t, h); attempted != 2 || succeeded != 2 {
		t.Fatalf("pass = %d attempted, %d confirmed; want 2 and 2", attempted, succeeded)
	}
	if want := claimedAt.Add(5 * time.Minute); !claimedUntil.Equal(want) {
		t.Errorf("the second row's claim runs until %s, want five minutes after its own claim, %s", claimedUntil, want)
	}
}

// POST /retention/run bounds its ledger pass (thirty seconds; a test's budget
// here): a store that has stopped answering ends the attempt under way when
// the budget runs out, the run answers 200 with what it attempted, and the
// rows wait in the ledger for the worker. The budget is a package variable, so
// this test does not run in parallel.
func TestRetention_TheRunsLedgerPassIsBounded(t *testing.T) {
	staff.SetRetentionRunLedgerBudget(t, 200*time.Millisecond)
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	keys := []string{queueObject(t, h, "a", h.Now(), nil), queueObject(t, h, "b", h.Now(), nil)}
	h.objects.hang()

	start := time.Now()
	var run gen.StaffRetentionRun
	do(t, hr, http.MethodPost, retentionRunPath, nil, http.StatusOK, &run)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the run answered after %s, want within its ledger budget", elapsed)
	}
	if run.Deletions.Attempted < 1 || run.Deletions.Succeeded != 0 {
		t.Errorf("deletions = %+v, want at least one attempted, none confirmed", run.Deletions)
	}
	for _, key := range keys {
		if _, _, _, ok := ledgerRow(t, h, key); !ok || !h.objects.has(key) {
			t.Errorf("%s: the row or the object is gone, want both kept for the worker", key)
		}
	}
}
