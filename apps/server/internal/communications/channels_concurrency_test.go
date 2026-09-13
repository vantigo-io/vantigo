package communications_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is fix round 1's item 2: concurrent creates that all read
// AnyChannelExists()=false race to become the one forced-default channel,
// colliding on the (type, is_default) partial unique index
// (migration 00006_communications_baseline.sql:48) in addition to the
// (type, address) index channel_exists already covers. Left unhandled that
// collision reached httpx.WriteError's host-wide 23505 fallback — a bare
// RFC 7807 409 — which is the wrong vocabulary for every non-stats endpoint
// in this module (design doc §3; only the three stats endpoints use
// ProblemDetails). A bare race() with no gate is not reliable here for the
// same reason energy's own concurrent-create test gives for supply periods:
// AnyChannelExists() is a fast, un-transacted-relative-to-the-others SELECT
// that can finish well before a sibling request even starts. The gate
// technique is duplicated from
// internal/energy/supplyperiods_concurrency_test.go (itself duplicated from
// internal/customers/contacts_concurrency_test.go and
// internal/identity/twofactor_test.go): a gate transaction takes
// `LOCK TABLE ... IN EXCLUSIVE MODE` before any request starts. That mode
// is compatible with the plain SELECT every AnyChannelExists() check runs
// (ACCESS SHARE), so every check still sees no existing channel and every
// request decides isDefault=true — but EXCLUSIVE conflicts with the ROW
// EXCLUSIVE lock ClearDefaultChannels's UPDATE and InsertChannel's INSERT
// both need, so every request queues behind the gate at its first write,
// after every AnyChannelExists() check has already run. Only once every
// request is confirmed waiting does the gate release; Postgres's own
// partial unique index then serializes the inserts, giving exactly one 201
// and the rest 409s in the module's own error shape.

// race runs fns at once, each released only when every one is ready, and
// returns their responses in the same order — duplicated from
// internal/energy/supplyperiods_concurrency_test.go's race (itself
// duplicated from internal/customers/contacts_concurrency_test.go):
// unexported per package, not importable.
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
// from internal/energy/supplyperiods_concurrency_test.go's awaitLockWaiters
// for the same reason as race above.
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
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCreateChannel_ConcurrentDefaultRaceAnswersModuleShape is fix round
// 1's item 2's teeth check: four concurrent creates with distinct
// addresses, forced to race for the default slot by the gate above. Before
// the fix, three of the four answered a bare RFC 7807 409 (Content-Type
// application/problem+json, no "error" key at all — modtest.Response.Code
// only decodes the access layer's shape, so this test decodes the module's
// own CommunicationErrorResponse directly and checks Content-Type too,
// since a wrong-shape body can still happen to contain a top-level "error"
// key by accident). After the fix, exactly one creates and the rest answer
// 409 default_channel_conflict in the module's real
// {"error":{"code","message"}} shape.
func TestCreateChannel_ConcurrentDefaultRaceAnswersModuleShape(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.channels IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock channels: %v", err)
	}

	const n = 4
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		address := channelAddress(t)
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/channels", newChannelBody(address))
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
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
			if ct := r.Header("Content-Type"); ct != "application/json" {
				t.Errorf("loser Content-Type = %q, want application/json (the module's own vocabulary, not application/problem+json)", ct)
			}
			var body commErrorJSON
			r.JSON(&body)
			if body.Error.Code != "default_channel_conflict" {
				t.Errorf("loser code = %q, want default_channel_conflict", body.Error.Code)
			}
			if body.Error.Message == "" {
				t.Error("loser message is empty, want a human-readable explanation")
			}
		default:
			t.Errorf("status %d body %s, want 201 or 409", r.Status, r.Body)
		}
	}
	if created != 1 {
		t.Errorf("created = %d, want exactly 1", created)
	}
	if conflicted != n-1 {
		t.Errorf("conflicted = %d, want exactly %d", conflicted, n-1)
	}
}
