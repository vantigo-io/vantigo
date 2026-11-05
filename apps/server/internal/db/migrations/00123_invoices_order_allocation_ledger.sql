-- +goose Up
-- Invoices' ledger of the order allocations it took (orders design O36, as
-- amended for delivery B's review): Invoices owns the cleanup of what it
-- allocated. Every allocation id Orders' Allocate answers is recorded here at
-- once, on a short transaction of its own, before any draft is written — by
-- the From orders wizard and by a billing run's item, which names itself in
-- run_item_id. A row lives until its allocation is invoiced — the issue that
-- stamps it deletes the row — or given back: released_at is set when
-- ReleaseAllocations for it has answered, after a draft's delete, a save
-- that dropped it, a wizard that failed, a billing run item's retry, or the
-- invoices.allocation_reconcile worker, which gives back every allocation
-- taken over an hour ago that no live line source holds. claimed_until is
-- that worker's per-row lease: a replica claims its rows by it, so it never
-- holds a connection while it calls Orders. The allocation id is Orders'
-- and opaque here: this migration names no other schema.
CREATE TABLE invoices.order_allocations_taken (
    allocation_id bigint      PRIMARY KEY,
    taken_at      timestamptz NOT NULL,
    run_item_id   bigint      REFERENCES invoices.billing_run_items (id) ON DELETE SET NULL,
    claimed_until timestamptz,
    released_at   timestamptz
);
-- The reconcile's scan: the rows not yet given back, oldest first.
CREATE INDEX ix_order_allocations_taken_open ON invoices.order_allocations_taken (taken_at)
    WHERE released_at IS NULL;
-- A billing run item's retry: its earlier allocations not yet given back.
CREATE INDEX ix_order_allocations_taken_item ON invoices.order_allocations_taken (run_item_id)
    WHERE run_item_id IS NOT NULL AND released_at IS NULL;

-- +goose Down
DROP TABLE invoices.order_allocations_taken;
