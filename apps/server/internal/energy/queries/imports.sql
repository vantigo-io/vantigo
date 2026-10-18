-- The imports of metering points from a file (energy import design D3, D6,
-- D7), the customers import's queries (internal/customers/queries/imports.sql)
-- in energy's schema, with the link columns and the attach beside them. The
-- rows themselves are written with pgx's CopyFrom at upload (imports.go), not
-- through a query here.

-- name: InsertImport :one
INSERT INTO energy.imports (
    state, file_name, size_bytes, encoding, separator, header, row_count,
    created_by_user_id, created_by_name, created_at, updated_at
) VALUES (
    'draft', $1, $2, $3, $4, $5, $6, $7, $8, $9, $9
)
RETURNING id;

-- name: GetImport :one
SELECT *
FROM energy.imports
WHERE id = $1;

-- name: ListImports :many
SELECT *
FROM energy.imports
ORDER BY updated_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountImports :one
SELECT count(*)
FROM energy.imports;

-- The mapping is saved on a draft or a checked import; a checked one goes
-- back to draft, and the caller clears the rows' results beside it. The
-- counts are reset with the state, so a draft never shows a stale check.
-- name: SetImportMapping :execrows
UPDATE energy.imports
SET mapping = $2, state = 'draft', processed = 0, created = 0, updated = 0, failed = 0, skipped = 0,
    switched = 0, pending = 0, attached = 0, confirmed_switches = 0, attach_start = NULL,
    requested_at = NULL, started_at = NULL, finished_at = NULL, error = NULL, stop_requested = false,
    updated_at = $3
WHERE id = $1 AND state IN ('draft', 'checked');

-- name: ClearImportRowResults :exec
UPDATE energy.import_rows
SET outcome = NULL, metering_point_id = NULL, gsrn = NULL, customer_id = NULL, link = NULL,
    switch_from = NULL, supply_start = NULL, attach_tried = false, errors = NULL
WHERE import_id = $1;

-- name: RequestImportCheck :execrows
UPDATE energy.imports
SET state = 'checking', requested_at = $2, started_at = NULL, finished_at = NULL, error = NULL,
    stop_requested = false, processed = 0, created = 0, updated = 0, failed = 0, skipped = 0,
    switched = 0, pending = 0, attached = 0, confirmed_switches = 0, updated_at = $2
WHERE id = $1 AND state IN ('draft', 'checked') AND mapping IS NOT NULL;

-- The check's counts stay until the worker starts (it writes its own as it
-- goes), so a run stopped before it started goes back to checked with them.
-- confirmed_switches is the number of switches the person confirmed (design
-- D4), which the handler compared with the check's before asking.
-- name: RequestImportRun :execrows
UPDATE energy.imports
SET state = 'importing', requested_at = $2, started_at = NULL, finished_at = NULL, error = NULL,
    stop_requested = false, processed = 0, confirmed_switches = $3, updated_at = $2
WHERE id = $1 AND state = 'checked' AND created + updated > 0 AND switched = $3;

-- The attach (design D6): a done import with pending rows goes to attaching,
-- with the date the person picked and the switches they confirmed.
-- name: RequestImportAttach :execrows
UPDATE energy.imports
SET state = 'attaching', requested_at = $2, started_at = NULL, error = NULL, stop_requested = false,
    attach_start = $3, confirmed_switches = $4, updated_at = $2
WHERE id = $1 AND state = 'done' AND pending > 0;

-- The pending rows an attach is about to try, each made untried again and its
-- switch_from read afresh from the point's active period now (design D6: the
-- point's customer is re-read at attach time, not trusted from the import).
-- name: PreparePendingRowsForAttach :exec
UPDATE energy.import_rows r
SET attach_tried = false, errors = NULL,
    switch_from = (
        SELECT p.customer_id FROM energy.supply_periods p
        WHERE p.metering_point_id = r.metering_point_id AND p.status = 'Active' AND p."end" IS NULL
          AND p.customer_id <> r.customer_id
        LIMIT 1
    )
WHERE r.import_id = $1 AND r.link = 'pending';

-- How many of an import's pending rows attaching would switch today: the
-- point's active period another customer's (design D6).
-- name: CountPendingSwitchesNow :one
SELECT count(*)
FROM energy.import_rows r
WHERE r.import_id = $1 AND r.link = 'pending'
  AND EXISTS (
      SELECT 1 FROM energy.supply_periods p
      WHERE p.metering_point_id = r.metering_point_id AND p.status = 'Active' AND p."end" IS NULL
        AND p.customer_id <> r.customer_id
  );

-- How many of an import's pending rows were pending switches when the run
-- wrote them (switch_from set): the import's own figure, beside the live one
-- above.
-- name: CountPendingSwitches :one
SELECT count(*)
FROM energy.import_rows
WHERE import_id = $1 AND link = 'pending' AND switch_from IS NOT NULL;

-- A switch the run or the attach made spends one of the switches the person
-- confirmed, in the switching row's own transaction (design D4).
-- name: SpendConfirmedSwitch :exec
UPDATE energy.imports
SET confirmed_switches = confirmed_switches - 1
WHERE id = $1 AND confirmed_switches > 0;

-- A stop on a run the worker has started: the worker reads the flag between
-- chunks and finishes.
-- name: RequestImportStop :execrows
UPDATE energy.imports
SET stop_requested = true, updated_at = $2
WHERE id = $1 AND state IN ('checking', 'importing', 'attaching') AND started_at IS NOT NULL;

-- A stop on a run the worker has not started yet: nothing ran, so the import
-- simply goes back to where it was asked from.
-- name: RevertUnstartedImport :execrows
UPDATE energy.imports
SET state = $2, requested_at = NULL, updated_at = $3
WHERE id = $1 AND state = $4 AND started_at IS NULL;

-- name: DeleteImport :execrows
DELETE FROM energy.imports
WHERE id = $1 AND state NOT IN ('checking', 'importing', 'attaching');

-- The worker's queue: the oldest import asked for, running or not yet started.
-- name: NextQueuedImport :one
SELECT *
FROM energy.imports
WHERE state IN ('checking', 'importing', 'attaching')
ORDER BY requested_at, id
LIMIT 1;

-- The worker taking up the import it read from the queue, only while the
-- import is still in the state it was read in: a stop or a delete in between
-- leaves nothing to take up, and the worker goes on to the next.
-- name: StartImport :execrows
UPDATE energy.imports
SET started_at = COALESCE(started_at, sqlc.arg(started_at)::timestamptz), error = NULL, updated_at = sqlc.arg(started_at)::timestamptz
WHERE id = sqlc.arg(id) AND state = sqlc.arg(state);

-- name: ImportStopRequested :one
SELECT stop_requested
FROM energy.imports
WHERE id = $1;

-- name: UpdateImportProgress :exec
UPDATE energy.imports
SET processed = $2, created = $3, updated = $4, failed = $5, skipped = $6, switched = $7, pending = $8,
    attached = $9, updated_at = $10
WHERE id = $1;

-- name: FinishImport :exec
UPDATE energy.imports
SET state = $2, processed = $3, created = $4, updated = $5, failed = $6, skipped = $7, switched = $8,
    pending = $9, attached = $10, finished_at = $11, stop_requested = false, error = NULL, updated_at = $12
WHERE id = $1;

-- name: FailImport :exec
UPDATE energy.imports
SET error = $2, updated_at = $3
WHERE id = $1;

-- name: ImportRowCounts :one
SELECT
    count(*) FILTER (WHERE outcome IS NOT NULL) AS processed,
    count(*) FILTER (WHERE outcome = 'created') AS created,
    count(*) FILTER (WHERE outcome = 'updated') AS updated,
    count(*) FILTER (WHERE outcome = 'failed')  AS failed,
    count(*) FILTER (WHERE outcome = 'skipped') AS skipped,
    count(*) FILTER (WHERE link = 'switched')   AS switched,
    count(*) FILTER (WHERE link = 'pending')    AS pending,
    count(*) FILTER (WHERE link = 'attached')   AS attached
FROM energy.import_rows
WHERE import_id = $1;

-- The next rows to run: the ones with no outcome, in file order.
-- name: ImportRowsChunk :many
SELECT row_no, cells
FROM energy.import_rows
WHERE import_id = $1 AND outcome IS NULL
ORDER BY row_no
LIMIT $2;

-- The next pending rows to attach: the ones this attach has not tried, in
-- file order.
-- name: PendingRowsChunk :many
SELECT row_no, metering_point_id, gsrn, customer_id
FROM energy.import_rows
WHERE import_id = $1 AND link = 'pending' AND NOT attach_tried
ORDER BY row_no
LIMIT $2;

-- The customers the attach's untried rows name, resolved once before the
-- first (design D4's rule, kept by the attach too).
-- name: PendingRowCustomerIDs :many
SELECT DISTINCT customer_id::integer AS customer_id
FROM energy.import_rows
WHERE import_id = $1 AND link = 'pending' AND NOT attach_tried AND customer_id IS NOT NULL
ORDER BY 1;

-- The pass before the first row (import_worker.go): every row's GSRN and
-- organisation number cells, in file order — the in-file duplicate GSRN and
-- the numbers to resolve. cells[0] is NULL in PostgreSQL, which is what an
-- unmapped column reads as; COALESCE makes it the blank it means.
-- name: ImportRowKeyCells :many
SELECT row_no,
       COALESCE((cells[@gsrn_column::int])::text, '')::text AS gsrn,
       COALESCE((cells[@legal_id_column::int])::text, '')::text AS legal_id
FROM energy.import_rows
WHERE import_id = $1
ORDER BY row_no;

-- name: WriteImportRowOutcome :exec
UPDATE energy.import_rows
SET outcome = $3, metering_point_id = $4, gsrn = $5, customer_id = $6, link = $7, switch_from = $8,
    supply_start = $9, errors = $10
WHERE import_id = $1 AND row_no = $2;

-- name: WriteImportRowAttach :exec
UPDATE energy.import_rows
SET link = $3, switch_from = $4, supply_start = $5, errors = $6, attach_tried = true
WHERE import_id = $1 AND row_no = $2;

-- A pending row the attach refused: still pending, its errors written, and
-- its switch_from as PreparePendingRowsForAttach left it, so the pending
-- switches still count it.
-- name: WriteImportRowAttachRefusal :exec
UPDATE energy.import_rows
SET errors = $3, attach_tried = true
WHERE import_id = $1 AND row_no = $2;

-- The preview's rows: the first few, in file order.
-- name: ImportSampleRows :many
SELECT row_no, cells
FROM energy.import_rows
WHERE import_id = $1
ORDER BY row_no
LIMIT $2;

-- The rows page: every row, or those of one outcome and/or one link.
-- name: ImportRowsPage :many
SELECT row_no, cells, outcome, metering_point_id, gsrn, customer_id, link, switch_from, supply_start, errors
FROM energy.import_rows
WHERE import_id = $1
  AND (sqlc.narg(outcome)::text IS NULL OR outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(link)::text IS NULL OR link = sqlc.narg(link)::text)
ORDER BY row_no
LIMIT $2 OFFSET $3;

-- name: CountImportRowsPage :one
SELECT count(*)
FROM energy.import_rows
WHERE import_id = $1
  AND (sqlc.narg(outcome)::text IS NULL OR outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(link)::text IS NULL OR link = sqlc.narg(link)::text);

-- name: FailedImportRows :many
SELECT row_no, cells, errors
FROM energy.import_rows
WHERE import_id = $1 AND outcome = 'failed'
ORDER BY row_no;

-- The suggestion's first tier (customers import wizard design D3): the
-- mapping last saved on an import with this very header.
-- name: LatestImportMappingForHeader :one
SELECT mapping
FROM energy.imports
WHERE header = $1 AND mapping IS NOT NULL
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- The sweep (design D6): every import not running whose last change is older
-- than the retention; the rows go by cascade.
-- name: SweepImports :execrows
DELETE FROM energy.imports
WHERE state NOT IN ('checking', 'importing', 'attaching') AND updated_at < $1;
