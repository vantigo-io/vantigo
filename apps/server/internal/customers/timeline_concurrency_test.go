package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the two DB-level concurrency guards customers inventory §4
// describes beyond the Go-side manual comparison timeline_more_test.go's
// TestDeleteCustomersByIdTimelineByEntryId_AlreadyDeleted_IsImmutable and
// friends already exercise sequentially:
//
//   - current_revision as the guarded UPDATE's WHERE-clause token (the
//     "second guard"), proven with a lock gate exactly as
//     contacts_concurrency_test.go proves the contacts row lock: two
//     concurrent PUTs against the same entry with the same expectedRevision,
//     forced to genuinely overlap at the database, not just in Go's
//     goroutine scheduler — a sleep-and-hope race would not prove this,
//     since a mutation removing the guard would still pass a race that
//     merely got lucky with interleaving;
//   - the unique index on (entry id, revision number) (the "third guard"),
//     proven directly at the SQL layer: the guarded UPDATE's row locking
//     already makes this constraint unreachable through the handler in
//     practice (whichever writer loses the guarded UPDATE never reaches the
//     revision insert at all), so the only honest way to pin the
//     constraint's existence and exact name is to violate it directly, the
//     same way .NET's own comment describes it as a backstop rather than
//     something the code paths above depend on.
//
// race and awaitLockWaiters are contacts_concurrency_test.go's, duplicated
// there from internal/identity's own row-lock tests; this file reuses them
// rather than declaring a third copy in the same package.

// TestPutCustomersByIdTimelineByEntryId_ConcurrentUpdates_ExactlyOneWins pins
// UpdateManualTimelineEntry's WHERE current_revision = expectedRevision
// clause (queries/timeline.sql): two PUTs, identical expectedRevision, both
// read the entry before either writes (forced by a gate lock taken on the
// entry row before either request starts, so both requests' Go-side
// pre-check sees the same, still-current revision and neither is rejected by
// it) — only the database's row-level locking can decide a winner once the
// gate releases. A one-line mutation dropping "AND current_revision =
// @expected_revision" from that UPDATE's WHERE clause would let both PUTs
// succeed (two 200s, current_revision incremented twice, no 409 at all),
// which this test's "exactly one winner" assertion catches; the loser's
// exact detail text — "The timeline entry was changed by another request."
// — is guard 2's own wording (TimelineEndpoints.cs:225), distinct from guard
// 1's numbered wording a sequential stale update gets
// (TestManualTimelineEntry_CanBeEditedAndSoftDeletedWithHistory's "stale"
// case), so a mutation that made guard 1 fire instead of letting the race
// reach the database would also be caught by this test's message assertion.
func TestPutCustomersByIdTimelineByEntryId_ConcurrentUpdates_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Race Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "original")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers_timeline_entries WHERE id = $1 FOR UPDATE`, entry.Id); err != nil {
		t.Fatalf("gate: lock the entry row: %v", err)
	}

	updateWith := func(note string) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), map[string]any{
				"eventType": "note", "occurredOn": "2026-07-27", "note": note, "expectedRevision": 1,
			})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(updateWith("writer A"), updateWith("writer B"))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var winners, losers int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			winners++
		case http.StatusConflict:
			losers++
			var problem problemDetailsJSON
			r.JSON(&problem)
			if problemTitle(problem.Title) != "Timeline revision conflict" {
				t.Errorf("loser Title = %q, want %q", problemTitle(problem.Title), "Timeline revision conflict")
			}
			want := "The timeline entry was changed by another request."
			if problemTitle(problem.Detail) != want {
				t.Errorf("loser Detail = %q, want %q (guard 2's wording, not guard 1's numbered stale-revision text)", problemTitle(problem.Detail), want)
			}
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want exactly one of each", winners, losers)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2 (the entry's creation plus exactly one winning update)", n)
	}
	if rev := h.Count(t, `SELECT current_revision FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id); rev != 2 {
		t.Errorf("current_revision = %d, want 2 (bumped exactly once)", rev)
	}
}

// TestTimelineRevisionsUniqueIndex_RejectsDuplicateRevisionNumber pins the
// third concurrency guard directly: ux_customers_timeline_entries_revisions_entry_revision
// on (customer_timeline_entry_id, revision_number). A one-line mutation that
// dropped this index from 00003_customers_baseline.sql, or renamed it so
// db.IsUniqueViolation's constraint-name match in
// PutCustomersByIdTimelineByEntryId/DeleteCustomersByIdTimelineByEntryId no
// longer matches, would let this insert through where it must fail —
// the guarded UPDATE's own row locking already makes this constraint
// unreachable through the handler in ordinary operation (the guard-2 test
// above never drives a writer as far as a second revision-1 insert), so this
// is the only test that can catch either mutation.
func TestTimelineRevisionsUniqueIndex_RejectsDuplicateRevisionNumber(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Unique Index Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "seed")

	now := h.Now()
	_, err := h.Pool().Exec(context.Background(), `
		INSERT INTO customers.customers_timeline_entries_revisions (
			customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
			occurred_on, summary, payload_version, current_revision, state, actor_kind, actor_display,
			created_at, updated_at
		) VALUES ($1, 1, $2, 'manual', 'customers.api', 'note', $3, 'duplicate', 1, 1, 'active', 'unattributed', 'Unattributed', $4, $4)
	`, entry.Id, customer.Id, pgtype.Date{Time: now, Valid: true}, now)
	if err == nil {
		t.Fatalf("duplicate (entry id, revision number) insert succeeded, want a unique_violation")
	}
	if !db.IsUniqueViolation(err, "ux_customers_timeline_entries_revisions_entry_revision") {
		t.Fatalf("insert failed with %v, want a 23505 on ux_customers_timeline_entries_revisions_entry_revision", err)
	}
}
