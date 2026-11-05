-- name: RecordAllocationsTaken :exec
-- RecordAllocationsTaken writes the ledger's row of each allocation Orders'
-- Allocate answered (orders design O36), at once and before any draft holds
-- it; run_item_id names the billing run item that took it, NULL for the
-- wizard. An id recorded already is left as it is.
INSERT INTO invoices.order_allocations_taken (allocation_id, taken_at, run_item_id)
SELECT unnest(@ids::bigint[]), @taken_at, sqlc.narg(run_item_id)::bigint
ON CONFLICT (allocation_id) DO NOTHING;

-- name: MarkAllocationsReleased :exec
-- MarkAllocationsReleased records that Orders gave the named allocations
-- back: ReleaseAllocations answered for them.
UPDATE invoices.order_allocations_taken
SET released_at = @released_at, claimed_until = NULL
WHERE allocation_id = ANY(@ids::bigint[]) AND released_at IS NULL;

-- name: ForgetAllocationsInvoiced :exec
-- ForgetAllocationsInvoiced deletes the ledger's rows of the allocations an
-- issue stamps invoiced, on the issue's transaction: Orders holds them now.
DELETE FROM invoices.order_allocations_taken WHERE allocation_id = ANY(@ids::bigint[]);

-- name: ClaimUnheldAllocations :many
-- ClaimUnheldAllocations is the invoices.allocation_reconcile worker's claim:
-- at most row_limit allocations taken before cutoff, not given back, held by
-- no live line source (held or invoiced), and not claimed by a replica whose
-- claim is still running — each claimed until claimed_until, in one
-- statement, so two replicas never take the same row.
UPDATE invoices.order_allocations_taken t
SET claimed_until = @claimed_until
WHERE t.allocation_id IN (
    SELECT o.allocation_id FROM invoices.order_allocations_taken o
    WHERE o.released_at IS NULL AND o.taken_at < @cutoff
      AND (o.claimed_until IS NULL OR o.claimed_until < @now)
      AND NOT EXISTS (
          SELECT 1 FROM invoices.line_sources s
          WHERE s.source_kind = 'orders.allocation' AND s.source_id = o.allocation_id
            AND s.state IN ('held', 'invoiced'))
    ORDER BY o.taken_at, o.allocation_id
    LIMIT @row_limit
    FOR UPDATE SKIP LOCKED)
RETURNING t.allocation_id;

-- name: PruneReleasedAllocations :exec
-- PruneReleasedAllocations deletes the ledger's rows given back before
-- cutoff: nothing reads them again.
DELETE FROM invoices.order_allocations_taken WHERE released_at < @cutoff;

-- name: ItemAllocationsUnheld :many
-- ItemAllocationsUnheld is what a billing run item took on an earlier
-- attempt and no live line source holds — an attempt that ended between
-- Allocate and its drafts — not given back yet, in id order.
SELECT o.allocation_id FROM invoices.order_allocations_taken o
WHERE o.run_item_id = @run_item_id AND o.released_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM invoices.line_sources s
      WHERE s.source_kind = 'orders.allocation' AND s.source_id = o.allocation_id
        AND s.state IN ('held', 'invoiced'))
ORDER BY o.allocation_id;

-- name: CustomerDraftsHoldOrders :one
-- CustomerDraftsHoldOrders is whether a draft of the customer holds an
-- order allocation: the billing run's orders_held_by_drafts, when nothing is
-- left due.
SELECT EXISTS (
    SELECT 1 FROM invoices.line_sources s
    JOIN invoices.invoices i ON i.id = s.invoice_id
    WHERE i.customer_id = @customer_id AND i.status = 'draft'
      AND s.source_kind = 'orders.allocation' AND s.state = 'held'
)::boolean;
