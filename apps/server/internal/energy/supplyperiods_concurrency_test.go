package energy_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports
// Integration/EnergyEndpointsTests.Concurrent_overlapping_supply_period_creates_yield_one_success_and_conflicts
// (energy inventory §5): concurrent creates race past
// CreateSupplyPeriodEndpoint's friendly overlap pre-check
// (SupplyPeriodOverlapExists), so it is the database's GiST exclusion
// constraint on supply_periods — not the app-level pre-check — that
// actually decides the race. The loser must surface as a *generic* 409:
// httpx.WriteError's host-wide mapping of Postgres 23P01 to
// httpx.ConflictDetail's fixed text, never the endpoint's own "Overlapping
// supply period" title/detail, which only the sequential pre-check path
// (already pinned by TestCreateSupplyPeriod_OverlapConflictThenEndAllowsHandover)
// can produce. Dispatch correction 4: both 409 paths exist and must be
// asserted by body, not status alone — a sibling module proved that a
// status-only check cannot tell them apart.
//
// A bare race() (goroutines released together with no gate) is not
// reliable: each request's pre-check is a fast, un-transacted SELECT that
// can finish well before a sibling request even starts, so most runs would
// see some losers answer through the friendly pre-check instead of the DB
// backstop. Forcing genuine simultaneity uses the same lock-gate technique
// internal/products/concurrency_test.go and
// internal/customers/contacts_concurrency_test.go use: a gate transaction
// takes `LOCK TABLE ... IN EXCLUSIVE MODE` on energy.supply_periods before
// any request starts. That mode is compatible with the plain SELECT
// (ACCESS SHARE) every pre-check runs — so every pre-check still sees no
// existing row and passes — but conflicts with the ROW EXCLUSIVE lock every
// INSERT needs, so every request queues behind the gate at the INSERT
// itself, after every pre-check has already run. Only once every request is
// confirmed waiting does the gate release; Postgres's own exclusion
// constraint then serializes the INSERTs, giving exactly one commit and the
// rest 23P01s.

// race runs fns at once, each released only when every one is ready, and
// returns their responses in the same order — duplicated from
// internal/products/concurrency_test.go's race (itself duplicated from
// internal/customers/contacts_concurrency_test.go): unexported per package,
// not importable.
func race(fns ...func() *modtest.Response) []*modtest.Response {
	out := make([]*modtest.Response, len(fns))
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i, fn := range fns {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			out[i] = fn()
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()
	return out
}

// awaitLockWaiters waits until n backends on h's database are waiting on a
// lock, failing t if finished closes first or ten seconds pass — duplicated
// from internal/products/concurrency_test.go's awaitLockWaiters for the
// same reason as race above.
func awaitLockWaiters(t *testing.T, h *modtest.Harness, n int, finished <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		select {
		case <-finished:
			t.Fatalf("the requests answered without waiting on the gate's lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d requests ever waited on the gate's lock", n)
		}
	}
}

// Ported from
// EnergyEndpointsTests.Concurrent_overlapping_supply_period_creates_yield_one_success_and_conflicts.
func TestConcurrentOverlappingSupplyPeriodCreates_YieldOneSuccessAndConflicts(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	start := h.Now().Add(time.Hour)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE energy.supply_periods IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock supply_periods: %v", err)
	}

	createWith := func(customerID int) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
				map[string]any{"customerId": customerID, "start": start})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(createWith(1001), createWith(1001), createWith(1002), createWith(1002))
		close(finished)
	}()
	awaitLockWaiters(t, h, 4, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var created, conflicted int
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
			var problem problemJSON
			r.JSON(&problem)
			// The DB-backstop path (httpx.WriteError), not
			// CreateSupplyPeriodEndpoint's own friendly "Overlapping supply
			// period" title/detail: httpx.WriteError's host-wide mapping of
			// every 23P01 answers this fixed title and detail, discarding
			// the endpoint's own — dispatch correction 4's generic body.
			if problem.Title != "Conflict" {
				t.Errorf("loser Title = %q, want %q (the generic conflict problem, not the endpoint's own)", problem.Title, "Conflict")
			}
			want := "The request conflicts with data that already exists. Verify the values and try again."
			if problem.Detail != want {
				t.Errorf("loser Detail = %q, want %q", problem.Detail, want)
			}
		default:
			t.Errorf("status %d body %s, want 201 or 409", r.Status, r.Body)
		}
	}
	if created != 1 {
		t.Errorf("created = %d, want exactly 1", created)
	}
	if conflicted != 3 {
		t.Errorf("conflicted = %d, want exactly 3", conflicted)
	}
}

// TestConcurrentEndSupplyPeriodExtensions_YieldOneSuccessAndConflicts is
// this task's fix-round item 2, pinned the same way as the Create race
// above: re-ending an already-Ended period *extends* its range (energy
// inventory §2.3), which the GiST exclusion constraint treats exactly like
// inserting a fresh overlapping row, deadlock hazard (40P01) included if
// the write is not serialized the way lockSupplyPeriods now serializes
// every write to supply_periods (Create, Switch, End and Cancel alike).
//
// Four short, mutually non-overlapping, already-Ended periods are each
// extended, concurrently, to one common far-future end — making all four
// overlap one another once extended. The gate here blocks each request
// earlier than Create's (at the advisory lock itself, since acquiring it
// needs no table access and so is never blocked by the table-level gate;
// only the UPDATE that follows it is) — awaitLockWaiters does not care
// which kind of lock a backend is waiting on, only that it is waiting on
// one, so the same technique still proves every request reached its write
// step before any of them proceeds. Exactly one extension must succeed and
// the rest must answer a generic 409 from a real 23P01 — never a 500 from
// an unresolved 40P01 deadlock.
func TestConcurrentEndSupplyPeriodExtensions_YieldOneSuccessAndConflicts(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)
	base := h.Now()

	periodIDs := make([]int32, 4)
	for i := range periodIDs {
		start := base.Add(time.Duration(i) * 10 * time.Minute)
		create := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
			map[string]any{"customerId": 1001, "start": start})
		if create.Status != http.StatusCreated {
			t.Fatalf("create period %d: status %d body %s, want 201", i, create.Status, create.Body)
		}
		var period supplyPeriodJSON
		create.JSON(&period)
		end := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, period.Id),
			map[string]any{"end": start.Add(5 * time.Minute)})
		if end.Status != http.StatusOK {
			t.Fatalf("end period %d: status %d body %s, want 200", i, end.Status, end.Body)
		}
		periodIDs[i] = period.Id
	}
	// Extending any one of the four to farEnd overlaps every other one:
	// all four currently end within the first 35 minutes after base, all
	// start before that too.
	farEnd := base.Add(time.Hour)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE energy.supply_periods IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock supply_periods: %v", err)
	}

	extendWith := func(periodID int32) func() *modtest.Response {
		return func() *modtest.Response {
			// The contract now declares this operation's 409 (fix-round
			// item 2: it was auto-generated from EndSupplyPeriodEndpoint.cs's
			// own Results<> signature, which names no Conflict-producing
			// branch — only the .NET host-wide exception handler's fallback
			// can produce one, invisible to that generator — a gap in the
			// *contract*, not a divergence this port introduced), so this
			// exchange is validated like every other one; no SkipContract.
			return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d/end", point.Id, periodID),
				map[string]any{"end": farEnd})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(extendWith(periodIDs[0]), extendWith(periodIDs[1]), extendWith(periodIDs[2]), extendWith(periodIDs[3]))
		close(finished)
	}()
	awaitLockWaiters(t, h, 4, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var succeeded, conflicted int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			conflicted++
			var problem problemJSON
			r.JSON(&problem)
			if problem.Title != "Conflict" {
				t.Errorf("loser Title = %q, want %q (the generic conflict problem)", problem.Title, "Conflict")
			}
			want := "The request conflicts with data that already exists. Verify the values and try again."
			if problem.Detail != want {
				t.Errorf("loser Detail = %q, want %q", problem.Detail, want)
			}
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1", succeeded)
	}
	if conflicted != 3 {
		t.Errorf("conflicted = %d, want exactly 3", conflicted)
	}
}
