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

// AUDITING THIS SCHEMA: ENUMERATE WRITERS, NOT CONSTRAINTS.
//
// ux_channels_type_is_default has now produced three separate defects across
// three tasks, and the reason is a method error worth writing down where the
// next person will hit it. Each pass examined the CONSTRAINT, found one pair
// of racing callers, guarded that pair, and recorded the constraint as
// "fixed". But **a guard is a property of a code path, not of a constraint.**
// A constraint with N writers has N×(N+1)/2 ordered pairs, and each pair
// needs its own verdict:
//
//	task 3   POST vs POST   guarded (409 default_channel_conflict)
//	task 14  PUT  vs PUT    fixed by ClearOtherDefaultChannels' predicate
//	         PUT  vs POST   MISSED — and unfixable by any statement rewrite,
//	                        because the conflicting row does not exist yet
//
// So the method for auditing this schema is: for each unique index and each
// composite primary key, enumerate every code path that INSERTs or UPDATEs
// the covered columns, then take each pair of those writers in turn — the
// self-pair included — and rule on it. Two writers that can produce the same
// key value are a pair to test even when one of them is an UPDATE and the
// other an INSERT, which is exactly the pair three passes walked past. Where
// a pair cannot be made safe by a statement or a caught violation, serialise
// it (lockDefaultChannelSlot) rather than leaving it to timing.
//
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

// TestCreateChannel_ConcurrentCreatesSerialiseIntoExactlyOneDefault is the
// POST-vs-POST pair, and it **replaces an assertion that task 14's C1 fix
// made obsolete**. The history matters, because the change is a behavioural
// one and not a test-only edit:
//
// Fix round 1 of task 3 found that four concurrent creates all read
// AnyChannelExists() = false, all decided isDefault = true, and collided on
// the partial unique index; the loser's raw 23505 escaped as a bare RFC 7807
// 409. It was guarded by mapping that violation to 409
// default_channel_conflict, and this test asserted one 201 plus three of
// those 409s.
//
// lockDefaultChannelSlot now serialises every writer for a channel type, so
// each create's AnyChannelExists() and demote run AFTER the previous
// create committed. The collision no longer happens — concurrent creates now
// behave exactly as sequential ones do — so all four succeed and exactly one
// row is default. That is strictly better (the caller gets the channel it
// asked for instead of a retry-me conflict) and it is what sequential
// callers have always seen, but it does mean **default_channel_conflict is
// now a defensive path rather than a reachable one**, which is why the old
// assertion could not simply be kept.
//
// Each create explicitly requests isDefault, so this exercises the demote
// under the lock rather than only the forced-default-of-the-first-channel
// rule.
func TestCreateChannel_ConcurrentCreatesSerialiseIntoExactlyOneDefault(t *testing.T) {
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
		body := newChannelBody(channelAddress(t))
		body["isDefault"] = true
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/channels", body)
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

	for i, r := range responses {
		if r.Status != http.StatusCreated {
			t.Errorf("request %d: status %d body %s (Content-Type %q), want 201: serialised creates do not conflict",
				i, r.Status, r.Body, r.Header("Content-Type"))
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels`); got != n {
		t.Errorf("channels = %d, want %d: every create must persist", got, n)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels WHERE is_default`); got != 1 {
		t.Errorf("default channels = %d, want exactly 1: the last writer wins the slot", got)
	}
}

// TestUpdateChannel_ConcurrentDefaultRaceLeavesExactlyOneDefault is task
// 14's constraint audit finding, and the reason the audit re-examined a
// constraint the ledger already called fixed: task 3 fixed
// ux_channels_type_is_default on the CREATE path only. The UPDATE path
// raced on the same index and nothing guarded it.
//
// Two PUTs each claiming the default for a different channel of the same
// type: under READ COMMITTED the loser's ClearOtherDefaultChannels used to
// take its snapshot before the winner committed, so the winner's row was
// still is_default = false there, did not match the statement's own
// `is_default AND id != @id` predicate, and was never visited. The loser
// demoted nothing and then set its own row true — two defaults of one type,
// and a 23505 the handler does not catch.
//
// It could not be caught the way CreateChannel's collision is, either:
// putCommunicationsChannelsById's contract declares 200/400/401/403/404 and
// no 409 at all, so the only correct answer was to stop producing the
// violation. queries/channels.sql's ClearOtherDefaultChannels drops the
// `is_default` term so the statement visits the winner's row, blocks on it,
// and re-checks it under EvalPlanQual — which the surviving `id != @id`
// still matches, so the loser demotes the winner and the race resolves as
// last-writer-wins with no violation at all.
//
// Teeth: restore `AND is_default` in that query and this test fails with one
// request answering a bare application/problem+json instead of 200.
func TestUpdateChannel_ConcurrentDefaultRaceLeavesExactlyOneDefault(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, "communications:channels-manage")

	// The first channel is forced default; the next two are the contenders.
	createChannel(t, c, newChannelBody(channelAddress(t)))
	first := createChannel(t, c, newChannelBody(channelAddress(t)))
	second := createChannel(t, c, newChannelBody(channelAddress(t)))

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.channels IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock channels: %v", err)
	}

	fns := make([]func() *modtest.Response, 0, 2)
	for _, id := range []string{first.Id, second.Id} {
		client := h.SignIn(t, "communications:channels-manage")
		fns = append(fns, func() *modtest.Response {
			return client.Do(http.MethodPut, "/api/v1/communications/channels/"+id,
				map[string]any{"isDefault": true})
		})
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, len(fns), finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("request %d: status %d body %s (Content-Type %q), want 200: setting the default is last-writer-wins, not a conflict",
				i, r.Status, r.Body, r.Header("Content-Type"))
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels WHERE is_default`); got != 1 {
		t.Errorf("default channels = %d, want exactly 1: the partial unique index allows one per type", got)
	}
}

// TestChannels_ConcurrentUpdateAndCreateDefaultRace is the PUT-vs-POST pair:
// the third defect on ux_channels_type_is_default, and the one no statement
// rewrite can reach.
//
// **Both racers must be different verbs, and that is the whole point.** The
// test above races two PUTs and cannot see this: with two PUTs both rows
// already exist, so the loser's demote can visit the winner's row, block on
// it, and re-check it. Here POST's row does not exist when PUT takes its
// snapshot — there is nothing to visit, nothing to block on, and nothing for
// EvalPlanQual to re-check — so PUT demotes what it can see, sets its own row
// true, and collides with the row POST inserted meanwhile. The 23505 escapes
// through updateChannel's unguarded error return as a bare RFC 7807 409, on
// an operation whose contract declares no 409 at all.
//
// lockDefaultChannelSlot is what makes this pair safe: both paths take the
// type-keyed transaction advisory lock before demoting, so the second writer
// starts its demote AFTER the first has committed and therefore sees its row.
//
// Teeth: remove either lockDefaultChannelSlot call and this test reds with
// `duplicate key value violates unique constraint "ux_channels_type_is_default"`
// surfacing as an application/problem+json 409.
func TestChannels_ConcurrentUpdateAndCreateDefaultRace(t *testing.T) {
	h := newHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")

	// One existing default (the first channel is forced default), plus the
	// channel the PUT will promote.
	createChannel(t, admin, newChannelBody(channelAddress(t)))
	promoted := createChannel(t, admin, newChannelBody(channelAddress(t)))

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.channels IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock channels: %v", err)
	}

	updater := h.SignIn(t, "communications:channels-manage")
	creator := h.SignIn(t, "communications:channels-manage")
	createdBody := newChannelBody(channelAddress(t))
	createdBody["isDefault"] = true

	fns := []func() *modtest.Response{
		func() *modtest.Response {
			return updater.Do(http.MethodPut, "/api/v1/communications/channels/"+promoted.Id,
				map[string]any{"isDefault": true})
		},
		func() *modtest.Response {
			return creator.Do(http.MethodPost, "/api/v1/communications/channels", createdBody)
		},
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, len(fns), finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	if got := responses[0].Status; got != http.StatusOK {
		t.Errorf("PUT: status %d body %s (Content-Type %q), want 200: promoting a default is last-writer-wins, not a conflict",
			got, responses[0].Body, responses[0].Header("Content-Type"))
	}
	if got := responses[1].Status; got != http.StatusCreated {
		t.Errorf("POST: status %d body %s (Content-Type %q), want 201",
			got, responses[1].Body, responses[1].Header("Content-Type"))
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels WHERE is_default`); got != 1 {
		t.Errorf("default channels = %d, want exactly 1", got)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels`); got != 3 {
		t.Errorf("channels = %d, want 3: neither request may lose its whole transaction", got)
	}
}
