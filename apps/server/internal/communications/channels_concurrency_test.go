package communications_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file holds every concurrency test for the (type, is_default) partial
// unique index (migration 00006_communications_baseline.sql:48), which has
// now produced FOUR defects across four passes. Each pass fixed the writer
// pair it was written against and left another live; the table in "AUDITING
// THIS SCHEMA" below is the history, and the reason that comment exists.
//
// The shared shape of all four: an unhandled 23505 on this index reaches
// httpx.WriteError's host-wide fallback as a bare RFC 7807 409 — the wrong
// vocabulary for every non-stats endpoint in this module (design doc §3;
// only the three stats endpoints use ProblemDetails) and, for
// putCommunicationsChannelsById, a status its contract does not declare at
// all.
//
// **The outcome these tests assert changed in the task 14 fix wave, so read
// the individual tests rather than assuming.** Before it, concurrent writers
// collided and the losers answered 409 default_channel_conflict; the gate
// released and "the partial unique index serialized the inserts, giving
// exactly one 201 and the rest 409s". lockDefaultChannelSlot now serialises
// the writers *before* they collide, so concurrent writers behave exactly as
// sequential ones do: they all succeed and the last one holds the default
// slot. That 409 is now a defensive path no test can drive (design §6 item
// 15).
//
// The gate technique is duplicated from
// internal/energy/supplyperiods_concurrency_test.go (itself duplicated from
// internal/customers/contacts_concurrency_test.go and
// internal/identity/twofactor_test.go): a gate transaction takes
// `LOCK TABLE ... IN EXCLUSIVE MODE` before any request starts. That mode is
// compatible with the plain SELECTs each request runs first (ACCESS SHARE),
// so every request reaches its first write with its decisions already made —
// but EXCLUSIVE conflicts with the ROW EXCLUSIVE that every demote, insert
// and update needs, so they all queue there. A bare race() with no gate is
// not reliable for this, for the same reason energy's own concurrent-create
// test gives: the reads are fast and un-transacted relative to each other, so
// one request can finish before a sibling even starts. awaitLockWaiters polls
// pg_stat_activity for genuinely blocked backends and never sleeps.

// AUDITING THIS SCHEMA: ENUMERATE WRITERS, NOT CONSTRAINTS.
//
// ux_channels_type_is_default produced FOUR separate defects across four
// passes, and the reason is a method error worth writing down where the next
// person will hit it. Each pass examined the CONSTRAINT, found one pair of
// racing callers, guarded that pair, and recorded the constraint as "fixed".
// But **a guard is a property of a code path, not of a constraint.** A
// constraint with N writers has N×(N+1)/2 ordered pairs, and each pair needs
// its own verdict:
//
//	task 3     POST vs POST   guarded (409 default_channel_conflict)
//	task 14    PUT  vs PUT    fixed by ClearOtherDefaultChannels' predicate
//	fix wave   PUT  vs POST   fixed by lockDefaultChannelSlot — and unfixable
//	                          by any statement rewrite, because the
//	                          conflicting row does not exist yet to be
//	                          visited, blocked on, or locked
//	fix round 2 PUT vs POST,  fixed by re-reading inside the transaction: the
//	            stale-value   lock serialises transactions but cannot refresh
//	            variant       a value read BEFORE one began, and is_default
//	                          was written back from a pre-transaction snapshot
//
// **The refinement the fourth defect bought, and the one this table exists
// for: a single writer can supply the same column from more than one VALUE
// SOURCE, and each source is its own pair.** PUT writes is_default either
// from the request (when it claims the default) or from a row it read earlier
// (when it says nothing about it). Those are two different pairings against
// the same POST, they fail for different reasons, and fixing one leaves the
// other live — which is exactly what happened. So enumerate writers, then for
// each writer enumerate where each written column's value COMES FROM, and
// pair those.
//
// The method, then: for each unique index and each composite primary key,
// enumerate every code path that INSERTs or UPDATEs the covered columns; for
// each such path enumerate each value source for those columns; then take
// each pair in turn — the self-pair included — and rule on it. Two writers
// that can produce the same key value are a pair to test even when one is an
// UPDATE and the other an INSERT. Where a pair cannot be made safe by a
// statement or a caught violation, serialise it (lockDefaultChannelSlot)
// rather than leaving it to timing — and remember that serialising alone does
// not fix a stale value, only a concurrent one.
//
// The same value-source reasoning applies beyond this constraint, to columns
// no constraint covers at all: the credential ciphertext was merged from a
// pre-transaction read and silently reverted a concurrent password rotation,
// with no violation to detect it (see
// TestUpdateChannel_ConcurrentCredentialWritesDoNotRevertARotatedPassword).
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
// one global transaction advisory lock before demoting, so the second writer
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

// TestUpdateChannel_PutNotClaimingTheDefaultRacingACreateThatDoes is the
// FOURTH defect on ux_channels_type_is_default, and the one the advisory
// lock does not close on its own.
//
// **The PUT here deliberately says nothing about the default.** Every other
// test in this file has its PUT claim it, which is precisely why none of them
// could see this: the value that collides is not one the request supplied,
// it is `existing.IsDefault`, read through the pool BEFORE the transaction
// and written back unconditionally. Serialising the transactions does not
// refresh it. So a `{"isActive": true}` PUT against the row that was the
// default re-asserts is_default = true from its stale snapshot after the
// concurrent create has demoted it — two default rows, and a 23505 that
// escapes as a bare application/problem+json 409 on an operation whose
// contract declares no 409 at all.
//
// The lock is not even load-bearing for this pair: the same collision occurs
// uncontended whenever a create commits inside the window between the PUT's
// GetChannelByID and its BeginTx, and that window holds a credential lookup,
// a secret open, a secret seal and a JSON marshal.
//
// The fix is the re-read under the lock in PutChannelById. Teeth: take
// is_default from the pre-transaction `existing` again and this test reds
// with that exact 23505.
func TestUpdateChannel_PutNotClaimingTheDefaultRacingACreateThatDoes(t *testing.T) {
	h := newHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")

	// The first channel is forced default, and it is the one the PUT targets
	// — so its stale snapshot says is_default = true.
	incumbent := createChannel(t, admin, newChannelBody(channelAddress(t)))

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
			// No isDefault key at all: this request is about isActive.
			return updater.Do(http.MethodPut, "/api/v1/communications/channels/"+incumbent.Id,
				map[string]any{"isActive": true})
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
		t.Errorf("PUT: status %d body %s (Content-Type %q), want 200: a PUT that never mentions the default must not conflict over it",
			got, responses[0].Body, responses[0].Header("Content-Type"))
	}
	if got := responses[1].Status; got != http.StatusCreated {
		t.Errorf("POST: status %d body %s (Content-Type %q), want 201",
			got, responses[1].Body, responses[1].Header("Content-Type"))
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.channels WHERE is_default`); got != 1 {
		t.Errorf("default channels = %d, want exactly 1: the create's demotion must survive the concurrent update", got)
	}
	// The create claimed the default, so the incumbent must have lost it —
	// a PUT that never mentioned the flag cannot have kept it.
	var incumbentDefault bool
	if err := h.Pool().QueryRow(ctx, `SELECT is_default FROM communications.channels WHERE id = $1`,
		uuid.MustParse(incumbent.Id)).Scan(&incumbentDefault); err != nil {
		t.Fatalf("read the incumbent: %v", err)
	}
	if incumbentDefault {
		t.Error("the incumbent is still default: the update wrote back a stale is_default over the create's demotion")
	}
}

// TestUpdateChannel_ConcurrentCredentialWritesDoNotRevertARotatedPassword is
// the credential lost update, and it is the only detector there is.
//
// No constraint covers a credential blob, so this failure produces no 23505,
// no error and no log line — the password simply reverts. The password is
// also sealed at rest and never echoed in any response, and sealing is
// randomised, so comparing ciphertexts proves nothing either. The test
// therefore decrypts (export_test.go's OpenChannelPasswordForTest) and
// asserts WHICH password survived.
//
// The two requests are staggered rather than started together, and that is
// load-bearing for the teeth check rather than incidental. Both PUTs carry an
// smtp block: one rotates the password, one omits it and so reuses the stored
// one. The defect only manifests when the REUSING request commits LAST — it
// is the one carrying a stale snapshot — so a symmetric race would reproduce
// it only half the time and the teeth check would be a coin flip. Starting
// the rotator first and waiting until it is genuinely blocked (it therefore
// already holds the advisory lock) forces the reusing request to queue behind
// it and commit second, deterministically.
//
// Expected either way round, and this is the invariant: the rotated password
// wins. If the reusing request runs first, the rotation lands after it; if it
// runs second, its re-read under the lock sees the rotation and re-seals
// that. Only a merge built before the transaction can produce "original".
//
// Teeth: move the password merge back before BeginTx (build the ciphertext
// from the pool read) and this test reds with the original password restored.
func TestUpdateChannel_ConcurrentCredentialWritesDoNotRevertARotatedPassword(t *testing.T) {
	h := newHarness(t)
	admin := h.SignIn(t, "communications:channels-manage")

	const original = "original-password"
	const rotated = "rotated-password"

	body := newChannelBody(channelAddress(t))
	body["smtp"] = map[string]any{"host": "smtp.example.test", "port": 587, "password": original}
	ch := createChannel(t, admin, body)
	path := "/api/v1/communications/channels/" + ch.Id

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.channels IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock channels: %v", err)
	}

	// never is awaitLockWaiters' "the requests have finished" channel; these
	// requests are deliberately still in flight, so it never closes.
	never := make(chan struct{})

	rotator := h.SignIn(t, "communications:channels-manage")
	rotateDone := make(chan *modtest.Response, 1)
	go func() {
		rotateDone <- rotator.Do(http.MethodPut, path, map[string]any{
			"smtp": map[string]any{"host": "smtp.example.test", "port": 587, "password": rotated},
		})
	}()
	// One blocked backend means the rotator is parked on the gate's table
	// lock, which it can only have reached after taking the advisory lock.
	awaitLockWaiters(t, h, 1, never)

	reuser := h.SignIn(t, "communications:channels-manage")
	reuseDone := make(chan *modtest.Response, 1)
	go func() {
		// No password key: reuse whatever is stored.
		reuseDone <- reuser.Do(http.MethodPut, path, map[string]any{
			"smtp": map[string]any{"host": "smtp.example.test", "port": 587},
		})
	}()
	awaitLockWaiters(t, h, 2, never)

	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	rotateResp, reuseResp := <-rotateDone, <-reuseDone

	if rotateResp.Status != http.StatusOK {
		t.Errorf("rotating PUT: status %d body %s, want 200", rotateResp.Status, rotateResp.Body)
	}
	if reuseResp.Status != http.StatusOK {
		t.Errorf("reusing PUT: status %d body %s, want 200", reuseResp.Status, reuseResp.Body)
	}

	var stored string
	if err := h.Pool().QueryRow(ctx,
		`SELECT secret_ciphertext FROM communications.channel_credentials WHERE channel_id = $1`,
		uuid.MustParse(ch.Id)).Scan(&stored); err != nil {
		t.Fatalf("read the stored credential: %v", err)
	}
	got, err := communications.OpenChannelPasswordForTest(h.Deps().Secrets, stored)
	if err != nil {
		t.Fatalf("open the stored password: %v", err)
	}
	if got != rotated {
		t.Errorf("stored password = %q, want %q: a reuse merge built before the transaction reverted the rotation", got, rotated)
	}
}
