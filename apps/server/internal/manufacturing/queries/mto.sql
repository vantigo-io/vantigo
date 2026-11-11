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
WHERE origin_kind = 'sales_order' AND backorder_of_id IS NULL AND status <> 'cancelled'
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
-- flagged are left alone. It answers the orders it flagged.
UPDATE manufacturing.orders
SET origin_cancelled = true, updated_at = @now
WHERE origin_kind = 'sales_order' AND origin_order_id = @sales_order_id AND NOT origin_cancelled
RETURNING id;
