package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A policy PUT racing a merge (D7, D18, plan reading 11), both ways, on a
// pool of two with the merge on a raw transaction of its own, as the
// customers module runs it.
//
// The merge first: it holds the absorbed customer's documents FOR UPDATE and
// has moved its policy; a PUT on that customer waits on the documents —
// pg_blocking_pids names the merge — and, once the merge commits, finds the
// customer has no documents left: 404, and no row for the absorbed
// customer, the moved one untouched. Checking the documents on the pool
// instead leaves an orphan row for it.
//
// The PUT first: parked on SetPolicyAfterLock, it holds the documents FOR
// SHARE and the policy row FOR UPDATE — each mode proved by NOWAIT probes —
// and the merge waits on the documents; released, the PUT commits its new
// mode and the merge moves the new row. Locking the policy row before the
// documents turns this into a deadlock.
//
// Never 40P01, and pg_stat_database.deadlocks unchanged. Not parallel: the
// seams are the package's.
func TestPolicy_MergeRacesPolicyPut(t *testing.T) {
	h := raceHarness(t)
	createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25)))
	createDraft(t, h, draftBody(customerForeign, line("C", 1, 100, vat25)))
	createDraft(t, h, draftBody(customerForeign, line("D", 1, 100, vat25)))
	plantPolicy(t, h, customerAcme, "no_charges", "Fast kunde")
	plantPolicy(t, h, customerForeign, "no_charges", "Fast kunde")
	holder := invoices.Module().CustomerReferences(h.Deps())
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	t.Run("the merge first", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		mergeConn := ownConn(t, h)
		merge, err := mergeConn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the merge: %v", err)
		}
		defer func() { _ = merge.Rollback(context.Background()) }()
		if _, err := holder.RepointCustomer(ctx, merge, customerAcme, customerNoTerms); err != nil {
			t.Fatalf("the merge: %v", err)
		}
		if mode := heldMode(t, probeConn, "invoices.invoices", "customer_id = $1", customerAcme); mode != modeUpdate {
			t.Fatalf("the merge holds the documents %q, want FOR UPDATE", mode)
		}

		answered := make(chan *modtest.Response, 1)
		client := payer(t, h)
		go func() {
			answered <- client.Do(http.MethodPut, reminderPolicyPath(customerAcme),
				map[string]any{"mode": "none", "note": "Tvist"}, modtest.Context(ctx))
		}()
		put := newWaiter(t, probeConn)
		if got, want := blockersOf(t, probeConn, put), []uint32{mergeConn.PgConn().PID()}; !slices.Equal(got, want) {
			t.Fatalf("pg_blocking_pids(the PUT) = %v, want the merge %v", got, want)
		}
		if err := merge.Commit(ctx); err != nil {
			t.Fatalf("commit the merge: %v", err)
		}
		res := <-answered
		if res.Status != http.StatusNotFound {
			t.Errorf("the PUT after the merge = %d %s, want 404: the customer has no documents left", res.Status, res.Body)
		}
		if got := policyRow(t, h, customerAcme); got != "" {
			t.Errorf("the absorbed customer's policy = %q, want none — an orphan the merge never sees", got)
		}
		if got := policyRow(t, h, customerNoTerms); got != "no_charges|Fast kunde" {
			t.Errorf("the survivor's policy = %q, want the moved row untouched", got)
		}
	})

	t.Run("the PUT first", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		parked, release := make(chan struct{}), make(chan struct{})
		restore := invoices.SetPolicyAfterLock(func(_ context.Context, customerID int32) error {
			if customerID != customerForeign {
				return nil
			}
			close(parked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		defer restore()
		answered := make(chan *modtest.Response, 1)
		client := payer(t, h)
		go func() {
			answered <- client.Do(http.MethodPut, reminderPolicyPath(customerForeign),
				map[string]any{"mode": "none", "note": "Tvist om leveransen"}, modtest.Context(ctx))
		}()
		select {
		case <-parked:
		case <-ctx.Done():
			t.Fatal("the PUT never reached the seam")
		}
		put := idleInTransaction(t, probeConn)
		if mode := heldMode(t, probeConn, "invoices.invoices", "customer_id = $1", customerForeign); mode != modeShare {
			t.Errorf("the PUT holds the documents %q, want FOR SHARE", mode)
		}
		if mode := heldMode(t, probeConn, "invoices.customer_reminder_policies", "customer_id = $1", customerForeign); mode != modeUpdate {
			t.Errorf("the PUT holds the policy row %q, want FOR UPDATE", mode)
		}

		mergeConn := ownConn(t, h)
		merged := make(chan error, 1)
		go func() {
			tx, err := mergeConn.Begin(ctx)
			if err != nil {
				merged <- err
				return
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := holder.RepointCustomer(ctx, tx, customerForeign, customerNoAddress); err != nil {
				merged <- err
				return
			}
			merged <- tx.Commit(ctx)
		}()
		merge := newWaiter(t, probeConn)
		if got, want := blockersOf(t, probeConn, merge), []uint32{put}; !slices.Equal(got, want) {
			t.Errorf("pg_blocking_pids(the merge) = %v, want the PUT %v", got, want)
		}
		close(release)
		if res := <-answered; res.Status != http.StatusOK {
			t.Errorf("the PUT = %d %s, want 200", res.Status, res.Body)
		}
		if err := <-merged; err != nil {
			t.Fatalf("the merge = %v, want it committed — never 40P01", err)
		}
		if got := policyRow(t, h, customerNoAddress); got != "none|Tvist om leveransen" {
			t.Errorf("the survivor's policy = %q, want the PUT's new row moved", got)
		}
		if got := policyRow(t, h, customerForeign); got != "" {
			t.Errorf("the absorbed customer's policy = %q, want none", got)
		}
	})

	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, was %d: Postgres broke a deadlock", after, before)
	}
}
