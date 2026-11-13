-- Make-to-order and sub-assemblies (manufacturing design M43–M48, M44a,
-- M44b): the requests the handler queues and the worker drains, the orders
-- made for sales order lines, and the flag a cancelled sales order leaves.

-- name: InsertMtoRequest :exec
-- InsertMtoRequest queues one sales order's request, once: a redelivery of
-- the event, or a second confirm, finds it there and adds nothing (M44a).
INSERT INTO manufacturing.mto_requests (sales_order_id, event_id, next_attempt_at, created_at)
VALUES (@sales_order_id, @event_id, @now, @now)
ON CONFLICT (sales_order_id) DO NOTHING;

-- name: DueMtoRequests :many
-- DueMtoRequests is the pending requests due by now, oldest first.
SELECT * FROM manufacturing.mto_requests
WHERE status = 'pending' AND next_attempt_at <= @now
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: GetMtoRequest :one
SELECT * FROM manufacturing.mto_requests WHERE id = @id;

-- name: FinishMtoRequest :execrows
-- FinishMtoRequest records a request's outcome, once: done when an order was
-- made, skipped when none was, with each line left out and why.
UPDATE manufacturing.mto_requests
SET status = @status, reasons = @reasons, done_at = @now, last_error = NULL
WHERE id = @id AND status = 'pending';

-- name: FailMtoRequest :exec
-- FailMtoRequest counts a failed attempt and backs off; the max_attempts-th
-- leaves the request failed for good.
UPDATE manufacturing.mto_requests
SET attempts = attempts + 1, last_error = @last_error, next_attempt_at = @next_attempt_at,
    status = CASE WHEN attempts + 1 >= @max_attempts::int THEN 'failed' ELSE status END
WHERE id = @id AND status = 'pending';

-- name: LockMto :exec
-- LockMto is the make-to-order lease (M44a), taken by every transaction
-- that makes orders for a sales order, before LockVariants: the bigint
-- overload, a key space apart from the schedule's two-int one.
SELECT pg_advisory_xact_lock(@lease_key::bigint);

-- name: DefaultBomsOfVariants :many
-- DefaultBomsOfVariants is, per variant among variant_ids that has one, its
-- default active BoM whole: make-to-order reads make_to_order on it (M44).
SELECT * FROM manufacturing.boms
WHERE variant_id = ANY(@variant_ids::int[]) AND is_default AND status = 'active'
ORDER BY variant_id;

-- name: LinesWithLiveOrder :many
-- LinesWithLiveOrder is which of the sales order lines already have their
-- one live order (M44b's index).
SELECT origin_line_id::bigint AS line_id FROM manufacturing.orders
WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND split_from_id IS NULL AND status <> 'cancelled'
  AND origin_line_id = ANY(@line_ids::bigint[]);

-- name: OrdersForLines :many
-- OrdersForLines is every order made for the sales order lines, the
-- backorders of the chain too (M44b), cancelled ones left out, by line and
-- number.
SELECT * FROM manufacturing.orders
WHERE origin_kind = 'sales_order' AND origin_line_id = ANY(@line_ids::bigint[]) AND status <> 'cancelled'
ORDER BY origin_line_id, number, suffix;

-- name: FlagOriginCancelled :many
-- FlagOriginCancelled marks the orders made for a cancelled sales order
-- (M44), leaving them as they are for a person to decide; the ones already
-- flagged are left alone. The rows are locked in id order first, as every
-- write of several orders locks them (M26a). It answers the orders it
-- flagged.
WITH flagged AS (
    SELECT f.id FROM manufacturing.orders f
    WHERE f.origin_kind = 'sales_order' AND f.origin_order_id = @sales_order_id AND NOT f.origin_cancelled
    ORDER BY f.id
    FOR UPDATE
)
UPDATE manufacturing.orders o
SET origin_cancelled = true, updated_at = @now
FROM flagged
WHERE o.id = flagged.id
RETURNING o.id;

-- name: CancelMtoRequest :exec
-- CancelMtoRequest marks a cancelled sales order's pending request
-- cancelled, or inserts it cancelled when the cancel is handled before the
-- confirm (M44): the worker, under the same lease, then makes nothing for it.
-- A request already finished is left as it is.
INSERT INTO manufacturing.mto_requests (sales_order_id, event_id, status, next_attempt_at, created_at, done_at)
VALUES (@sales_order_id, @event_id, 'cancelled', @now, @now, @now)
ON CONFLICT (sales_order_id) DO UPDATE
SET status = 'cancelled', done_at = EXCLUDED.done_at, last_error = NULL
WHERE manufacturing.mto_requests.status = 'pending';

-- name: MtoRequestOfSalesOrder :one
-- MtoRequestOfSalesOrder is the one request of a sales order, for what
-- contracts.Production says of the lines it made no order for (M46a).
SELECT * FROM manufacturing.mto_requests WHERE sales_order_id = @sales_order_id;

-- name: OpenChildrenOfOrder :many
-- OpenChildrenOfOrder is, per variant, what the parent's sub-assembly orders
-- not done or cancelled have left to make (quantity less produced, at least
-- 0): a second sub-assembly order is made only for what they do not cover
-- (M47).
SELECT variant_id, COALESCE(SUM(GREATEST(quantity - produced, 0)), 0)::numeric(14, 3) AS remaining
FROM manufacturing.orders
WHERE parent_order_id = @parent_order_id AND status IN ('draft', 'confirmed', 'in_progress')
GROUP BY variant_id
ORDER BY variant_id;
