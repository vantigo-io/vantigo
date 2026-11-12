-- Transfers between sites (phase 2 design I35–I53): a draft document, sent
-- as a transfer_out movement at the from site, received in parts as
-- transfer_in movements at the to site, closed with a difference or
-- cancelled back. Every write locks the transfer row FOR UPDATE first (I38);
-- send, receive and cancel then look up their action's client request id,
-- judge If-Match and the status, take the two sites FOR SHARE and post
-- through the engine. In transit is sent - received - missing - returned on a
-- line (I46).

-- name: AllocateNumber :one
-- AllocateNumber takes the next number of a series (I36, the Orders
-- pattern): the first allocation is series_start, every later one one more.
-- The row's lock is held until the creating transaction commits, so it
-- serialises two creates, and a rolled-back create rolls its number back.
INSERT INTO inventory.counters (counter_name, next_value)
VALUES (sqlc.arg(counter_name), sqlc.arg(series_start)::bigint + 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = inventory.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS allocated;

-- name: InsertTransfer :one
INSERT INTO inventory.transfers (number, from_site_id, to_site_id, status, reference, note, expected_arrival,
                                 created_by_user_id, created_by_display, created_at, updated_at)
VALUES (@number, @from_site_id, @to_site_id, 'draft', sqlc.narg(reference), sqlc.narg(note), sqlc.narg(expected_arrival),
        sqlc.narg(created_by_user_id), sqlc.narg(created_by_display), @now::timestamptz, @now::timestamptz)
RETURNING id;

-- name: GetTransfer :one
SELECT * FROM inventory.transfers WHERE id = @id;

-- name: GetTransferByNumber :one
-- GetTransferByNumber is the lookup's TRF-<number> (I52).
SELECT * FROM inventory.transfers WHERE number = @number;

-- name: LockTransfer :one
-- LockTransfer takes the transfer row FOR UPDATE: step 1 of every write to a
-- transfer (I38).
SELECT * FROM inventory.transfers WHERE id = @id FOR UPDATE;

-- name: ShareTransferSites :many
-- ShareTransferSites takes FOR SHARE on a transfer's from and to site in id
-- order (I38 step 4), so an archive that committed first is seen and one
-- racing the action waits; their status is judged after.
SELECT * FROM inventory.sites WHERE id = ANY(@ids::bigint[]) ORDER BY id FOR SHARE;

-- name: UpdateTransferHeader :exec
-- UpdateTransferHeader replaces a draft's header and moves its revision on
-- (I37).
UPDATE inventory.transfers
SET from_site_id = @from_site_id, to_site_id = @to_site_id, reference = sqlc.narg(reference), note = sqlc.narg(note),
    expected_arrival = sqlc.narg(expected_arrival), revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;

-- name: DeleteTransfer :exec
-- DeleteTransfer deletes a draft; its lines go with it (on delete cascade).
DELETE FROM inventory.transfers WHERE id = @id;

-- name: MarkTransferSent :exec
-- MarkTransferSent is a send's status (I39), or a send and receive's once
-- its receive is written (status received).
UPDATE inventory.transfers
SET status = @status, sent_at = @now::timestamptz, sent_by_user_id = sqlc.narg(sent_by_user_id),
    sent_by_display = sqlc.narg(sent_by_display), revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;

-- name: SetTransferStatus :exec
-- SetTransferStatus is a receive's or a cancel's status (I40, I44).
UPDATE inventory.transfers
SET status = @status, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;

-- name: CloseTransfer :exec
-- CloseTransfer is the transfer's end: received, or cancelled, with who
-- closed it and, for a difference closed, its reason and note (I42).
UPDATE inventory.transfers
SET status = @status, close_reason = sqlc.narg(close_reason), close_note = sqlc.narg(close_note),
    closed_at = @now::timestamptz, closed_by_user_id = sqlc.narg(closed_by_user_id),
    closed_by_display = sqlc.narg(closed_by_display), revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id;

-- name: TransferLines :many
-- TransferLines is a transfer's lines by line number.
SELECT * FROM inventory.transfer_lines WHERE transfer_id = @transfer_id ORDER BY line_no;

-- name: TransferLinesOf :many
-- TransferLinesOf is the lines of several transfers, for a page of them.
SELECT * FROM inventory.transfer_lines WHERE transfer_id = ANY(@transfer_ids::bigint[]) ORDER BY transfer_id, line_no;

-- name: DeleteTransferLines :exec
-- DeleteTransferLines removes a draft's lines before its edit writes them
-- again (I37).
DELETE FROM inventory.transfer_lines WHERE transfer_id = @transfer_id;

-- name: InsertTransferLines :exec
-- InsertTransferLines writes a draft's lines with the variant snapshots
-- (I36) and the lot each names, 0 for none (M70). An entry or a suggestion
-- of '' is none.
INSERT INTO inventory.transfer_lines (transfer_id, line_no, variant_id, sku, product_name, quantity, entered, suggestion, lot_id)
SELECT @transfer_id, u.line_no, u.variant_id, u.sku, u.product_name, u.quantity,
       nullif(u.entered, '')::jsonb, nullif(u.suggestion, '')::jsonb, nullif(u.lot_id, 0)
FROM (SELECT unnest(@line_nos::int[]) AS line_no, unnest(@variant_ids::int[]) AS variant_id, unnest(@skus::text[]) AS sku,
             unnest(@product_names::text[]) AS product_name, unnest(@quantities::numeric[]) AS quantity,
             unnest(@entereds::text[]) AS entered, unnest(@suggestions::text[]) AS suggestion,
             unnest(@lot_ids::bigint[]) AS lot_id) AS u;

-- name: SendTransferLines :exec
-- SendTransferLines is a send's lines (I39): resolved to units, snapshotted
-- again, with their source location and the lot sent (0 for none, M70), and
-- sent in full.
UPDATE inventory.transfer_lines l
SET sku = u.sku, product_name = u.product_name, unit = u.unit, quantity = u.quantity, entered = nullif(u.entered, '')::jsonb,
    from_location_id = u.from_location_id, lot_id = nullif(u.lot_id, 0), sent = u.quantity
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@skus::text[]) AS sku, unnest(@product_names::text[]) AS product_name,
             unnest(@units::text[]) AS unit, unnest(@quantities::numeric[]) AS quantity, unnest(@entereds::text[]) AS entered,
             unnest(@from_location_ids::bigint[]) AS from_location_id, unnest(@lot_ids::bigint[]) AS lot_id) AS u
WHERE l.id = u.id AND l.transfer_id = @transfer_id;

-- name: AddTransferLineTotals :exec
-- AddTransferLineTotals adds an action's quantities to its lines' running
-- totals (I38 step 6): received, missing and returned, each 0 for none. The
-- check keeps received + missing + returned within sent.
UPDATE inventory.transfer_lines l
SET received = l.received + u.received, missing = l.missing + u.missing, returned = l.returned + u.returned
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@received::numeric[]) AS received,
             unnest(@missing::numeric[]) AS missing, unnest(@returned::numeric[]) AS returned) AS u
WHERE l.id = u.id AND l.transfer_id = @transfer_id;

-- name: ActionByClientRequest :one
-- ActionByClientRequest is the replay check (I38 step 2), under the
-- transfer's lock: the action a client request id already wrote, with its
-- transfer, kind and hash.
SELECT id, transfer_id, kind, request_hash FROM inventory.transfer_actions WHERE client_request_id = @client_request_id;

-- name: NextTransferActionID :one
-- NextTransferActionID reserves an action's id before its movement is posted,
-- so the movement's source line names the action (I45) while the action is
-- written after it, at I38's step 6. It takes no lock.
SELECT nextval(pg_get_serial_sequence('inventory.transfer_actions', 'id'))::bigint AS id;

-- name: InsertTransferAction :exec
-- InsertTransferAction writes an action under the id NextTransferActionID
-- reserved.
INSERT INTO inventory.transfer_actions (id, transfer_id, kind, client_request_id, request_hash, movement_id, close_reason, close_note,
                                        posted_by_user_id, posted_by_display, posted_at)
OVERRIDING SYSTEM VALUE
VALUES (@id, @transfer_id, @kind, @client_request_id, @request_hash, sqlc.narg(movement_id), sqlc.narg(close_reason), sqlc.narg(close_note),
        sqlc.narg(posted_by_user_id), sqlc.narg(posted_by_display), @posted_at::timestamptz);

-- name: InsertTransferActionLines :exec
-- InsertTransferActionLines writes an action's lines (I38). A location of 0
-- is none (a missing line).
INSERT INTO inventory.transfer_action_lines (action_id, transfer_line_id, kind, quantity, location_id)
SELECT @action_id, u.transfer_line_id, u.kind, u.quantity, nullif(u.location_id, 0)
FROM (SELECT unnest(@transfer_line_ids::bigint[]) AS transfer_line_id, unnest(@kinds::text[]) AS kind,
             unnest(@quantities::numeric[]) AS quantity, unnest(@location_ids::bigint[]) AS location_id) AS u;

-- name: TransferActions :many
-- TransferActions is a transfer's actions oldest first.
SELECT * FROM inventory.transfer_actions WHERE transfer_id = @transfer_id ORDER BY posted_at, id;

-- name: TransferActionLines :many
-- TransferActionLines is every action line of a transfer, by action then
-- line.
SELECT al.*
FROM inventory.transfer_action_lines al
JOIN inventory.transfer_actions a ON a.id = al.action_id
WHERE a.transfer_id = @transfer_id
ORDER BY al.action_id, al.transfer_line_id, al.kind;

-- name: ListTransfers :many
-- ListTransfers is the transfers newest first (by created_at, id), filtered
-- by statuses, sites, a variant on a line, something recorded missing (of
-- that variant, when given), and q — a transfer's number or a line's SKU —
-- each NULL for any, after the cursor's (created_at, id); with each
-- transfer's line count and its in transit and missing summed.
SELECT t.*,
       (SELECT count(*) FROM inventory.transfer_lines c WHERE c.transfer_id = t.id)::int AS line_count,
       (SELECT coalesce(sum(c.sent - c.received - c.missing - c.returned), 0) FROM inventory.transfer_lines c
        WHERE c.transfer_id = t.id)::numeric AS in_transit,
       (SELECT coalesce(sum(c.missing), 0) FROM inventory.transfer_lines c WHERE c.transfer_id = t.id)::numeric AS missing
FROM inventory.transfers t
WHERE (sqlc.narg(statuses)::text[] IS NULL OR t.status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(from_site_id)::bigint IS NULL OR t.from_site_id = sqlc.narg(from_site_id)::bigint)
  AND (sqlc.narg(to_site_id)::bigint IS NULL OR t.to_site_id = sqlc.narg(to_site_id)::bigint)
  AND (sqlc.narg(variant_id)::int IS NULL OR EXISTS (
        SELECT 1 FROM inventory.transfer_lines v WHERE v.transfer_id = t.id AND v.variant_id = sqlc.narg(variant_id)::int))
  AND (NOT @with_missing::boolean OR EXISTS (
        SELECT 1 FROM inventory.transfer_lines m WHERE m.transfer_id = t.id AND m.missing > 0
          AND (sqlc.narg(variant_id)::int IS NULL OR m.variant_id = sqlc.narg(variant_id)::int)))
  AND (sqlc.narg(q)::text IS NULL
       OR t.number = sqlc.narg(q_number)::bigint
       OR EXISTS (SELECT 1 FROM inventory.transfer_lines s WHERE s.transfer_id = t.id AND lower(s.sku) = lower(sqlc.narg(q)::text)))
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (t.created_at, t.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
ORDER BY t.created_at DESC, t.id DESC
LIMIT @row_limit::int;

-- name: VariantInTransit :one
-- VariantInTransit is what of a variant is in transit on every transfer, and
-- on how many transfers, on the caller's transaction (the holder, I47).
SELECT coalesce(sum(sent - received - missing - returned), 0)::text AS quantity, count(DISTINCT transfer_id)::int AS transfers
FROM inventory.transfer_lines
WHERE variant_id = @variant_id AND sent > received + missing + returned;

-- name: IncomingOfVariants :many
-- IncomingOfVariants is each variant's in transit per to site (I46): the
-- incoming of the product's and the variant's stock and of I53's batch.
SELECT l.variant_id, t.to_site_id AS site_id, sum(l.sent - l.received - l.missing - l.returned)::text AS incoming
FROM inventory.transfer_lines l
JOIN inventory.transfers t ON t.id = l.transfer_id
WHERE l.variant_id = ANY(@variant_ids::int[]) AND l.sent > l.received + l.missing + l.returned
GROUP BY l.variant_id, t.to_site_id
ORDER BY l.variant_id, t.to_site_id;

-- name: InTransitOfVariants :many
-- InTransitOfVariants is each variant's in transit across every transfer,
-- for the stock totals (I46).
SELECT variant_id, sum(sent - received - missing - returned)::text AS in_transit
FROM inventory.transfer_lines
WHERE variant_id = ANY(@variant_ids::int[]) AND sent > received + missing + returned
GROUP BY variant_id
ORDER BY variant_id;

-- name: OpenTransferCounts :many
-- OpenTransferCounts is, per site of those given, the number of sent or
-- partly received transfers to it and from it (I46's InventorySite counts).
SELECT s.id AS site_id,
       (SELECT count(*) FROM inventory.transfers t WHERE t.to_site_id = s.id AND t.status IN ('sent', 'partly_received'))::int AS incoming,
       (SELECT count(*) FROM inventory.transfers t WHERE t.from_site_id = s.id AND t.status IN ('sent', 'partly_received'))::int AS outgoing
FROM inventory.sites s
WHERE s.id = ANY(@ids::bigint[])
ORDER BY s.id;

-- name: SiteHasOpenTransfers :one
-- SiteHasOpenTransfers reports whether a sent or partly received transfer
-- names the site, so archiving it is refused (site_has_open_transfers, I48).
SELECT EXISTS (
    SELECT 1 FROM inventory.transfers
    WHERE (from_site_id = @site_id::bigint OR to_site_id = @site_id::bigint) AND status IN ('sent', 'partly_received')
);

-- name: SiteHasTransfers :one
-- SiteHasTransfers reports whether any transfer names the site, so deleting
-- it is refused (site_has_transfers, I48).
SELECT EXISTS (
    SELECT 1 FROM inventory.transfers WHERE from_site_id = @site_id::bigint OR to_site_id = @site_id::bigint
);

-- name: SubtreeInTransit :one
-- SubtreeInTransit reports whether a line still in transit was sent from the
-- location or one below it, so archiving it is refused (location_in_transit,
-- I48): the cancel's way home.
SELECT EXISTS (
    SELECT 1
    FROM inventory.transfer_lines tl
    JOIN inventory.locations l ON l.id = tl.from_location_id
    WHERE tl.sent > tl.received + tl.missing + tl.returned
      AND (l.id = @location_id::bigint OR l.ancestors @> ARRAY[@location_id::bigint])
);

-- name: OverdueTransfers :many
-- OverdueTransfers is the transfers sent or partly received past their
-- expected arrival, or without one sent before the moment given, oldest
-- first (the dashboard's transferOverdue, I62).
SELECT t.id, t.number, t.from_site_id, t.to_site_id, t.expected_arrival, t.sent_at
FROM inventory.transfers t
WHERE t.status IN ('sent', 'partly_received')
  AND ((t.expected_arrival IS NOT NULL AND t.expected_arrival < @today::date)
       OR (t.expected_arrival IS NULL AND t.sent_at < @sent_before::timestamptz))
ORDER BY coalesce(t.expected_arrival::timestamptz, t.sent_at), t.id
LIMIT 20;

-- name: TransferRefsOfMovements :many
-- TransferRefsOfMovements is, per movement of those given that a transfer
-- posted, the transfer it names (I45): read through the action that posted
-- it, by the action's movement_id and the transfer's primary key.
SELECT a.movement_id::bigint AS movement_id, t.id AS transfer_id, t.number
FROM inventory.transfer_actions a
JOIN inventory.transfers t ON t.id = a.transfer_id
WHERE a.movement_id = ANY(@movement_ids::bigint[])
ORDER BY a.movement_id;
