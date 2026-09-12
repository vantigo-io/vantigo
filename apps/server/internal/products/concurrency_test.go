package products_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/ProductConcurrencyConflictTests.cs (products
// inventory §4/§6): concurrent creates race past CreateProductEndpoint's
// friendly SKU pre-check (products.go's VariantAnySkuExists), so it is the
// database's unique index on product_variants.sku, not the app-level
// pre-check, that actually decides the race. The loser must surface as a
// generic 409 — httpx.WriteError's host-wide mapping of Postgres 23505 to
// httpx.ConflictDetail's fixed text — never CreateProductEndpoint's own
// "Duplicate SKU" title/detail, which only the *sequential* pre-check path
// (already pinned by TestCreateProduct_WithDuplicateSku_ReturnsConflict) can
// produce.
//
// A bare race() (four goroutines released together) is not reliable here:
// each request's pre-check is a fast, un-transacted SELECT that can finish
// well before a sibling request even starts, so most runs saw 2-3 of the 3
// losers answer through the friendly pre-check instead of the DB backstop
// (confirmed empirically — see the fix-round report). Forcing genuine
// simultaneity needs the same lock-gate technique
// contacts_concurrency_test.go/timeline_concurrency_test.go use elsewhere in
// this project: a gate transaction takes `LOCK TABLE ... IN EXCLUSIVE MODE`
// on product_variants before any request starts. That mode is compatible
// with the plain SELECT (ACCESS SHARE) every pre-check runs — so all four
// pre-checks still see no existing row and pass — but conflicts with the
// ROW EXCLUSIVE lock every INSERT needs, so all four requests queue behind
// the gate at the INSERT statement itself, after every pre-check has
// already run. Only once all four are confirmed waiting does the gate
// release; Postgres's own unique-index insertion then serializes the four
// INSERTs, giving exactly one commit and three 23505s.

// race runs fns at once, each released only when every one is ready, and
// returns their responses in the same order — duplicated from
// internal/customers/contacts_concurrency_test.go's race (itself duplicated
// from internal/identity's own row-lock tests): unexported per package, not
// importable.
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
// from internal/customers/contacts_concurrency_test.go's awaitLockWaiters
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
	}
}

// Ported from Integration/ProductConcurrencyConflictTests.cs.
// Concurrent_creates_with_the_same_sku_yield_one_success_and_conflicts.
func TestConcurrentCreates_WithSameSku_YieldOneSuccessAndConflicts(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	raceSku := sku(t, "race")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE products.product_variants IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock product_variants: %v", err)
	}

	createWith := func(index int) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/products", map[string]any{
				"name": fmt.Sprintf("Race product %d", index), "type": "Goods", "taxCategoryId": taxCategoryID,
				"variants": []map[string]any{{"sku": raceSku}},
			})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(createWith(0), createWith(1), createWith(2), createWith(3))
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
			// CreateProductEndpoint's own friendly "Duplicate SKU"
			// title/detail: products inventory §4's host-wide exception
			// handler answers every 23505 with this fixed title and detail,
			// discarding the endpoint's own.
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
