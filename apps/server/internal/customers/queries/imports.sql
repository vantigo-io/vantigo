-- The imports of customers from a file (customers import wizard design D1,
-- D5, D6). The rows themselves are written with pgx's CopyFrom at upload
-- (imports.go), not through a query here.

-- name: InsertImport :one
INSERT INTO customers.imports (
    state, file_name, size_bytes, encoding, separator, header, row_count,
    created_by_user_id, created_by_name, created_at, updated_at
) VALUES (
    'draft', $1, $2, $3, $4, $5, $6, $7, $8, $9, $9
)
RETURNING id;

-- name: GetImport :one
SELECT *
FROM customers.imports
WHERE id = $1;

-- name: ListImports :many
SELECT *
FROM customers.imports
ORDER BY updated_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: CountImports :one
SELECT count(*)
FROM customers.imports;

-- The mapping is saved on a draft or a checked import; a checked one goes
-- back to draft, and the caller clears the rows' results beside it. The
-- counts are reset with the state, so a draft never shows a stale check.
-- name: SetImportMapping :execrows
UPDATE customers.imports
SET mapping = $2, state = 'draft', processed = 0, created = 0, updated = 0, failed = 0,
    requested_at = NULL, started_at = NULL, finished_at = NULL, error = NULL, stop_requested = false,
    updated_at = $3
WHERE id = $1 AND state IN ('draft', 'checked');

-- name: ClearImportRowResults :exec
UPDATE customers.import_rows
SET outcome = NULL, customer_id = NULL, errors = NULL
WHERE import_id = $1;

-- name: RequestImportCheck :execrows
UPDATE customers.imports
SET state = 'checking', requested_at = $2, started_at = NULL, finished_at = NULL, error = NULL,
    stop_requested = false, processed = 0, created = 0, updated = 0, failed = 0, updated_at = $2
WHERE id = $1 AND state IN ('draft', 'checked') AND mapping IS NOT NULL;

-- The check's counts stay until the worker starts (it writes its own as it
-- goes), so a run stopped before it started goes back to checked with them.
-- name: RequestImportRun :execrows
UPDATE customers.imports
SET state = 'importing', requested_at = $2, started_at = NULL, finished_at = NULL, error = NULL,
    stop_requested = false, processed = 0, updated_at = $2
WHERE id = $1 AND state = 'checked' AND created + updated > 0;

-- A stop on a run the worker has started: the worker reads the flag between
-- chunks and finishes.
-- name: RequestImportStop :execrows
UPDATE customers.imports
SET stop_requested = true, updated_at = $2
WHERE id = $1 AND state IN ('checking', 'importing') AND started_at IS NOT NULL;

-- A stop on a run the worker has not started yet: nothing ran, so the import
-- simply goes back to where it was asked from.
-- name: RevertUnstartedImport :execrows
UPDATE customers.imports
SET state = $2, requested_at = NULL, updated_at = $3
WHERE id = $1 AND state = $4 AND started_at IS NULL;

-- name: DeleteImport :execrows
DELETE FROM customers.imports
WHERE id = $1 AND state NOT IN ('checking', 'importing');

-- The worker's queue: the oldest import asked for, running or not yet started.
-- name: NextQueuedImport :one
SELECT *
FROM customers.imports
WHERE state IN ('checking', 'importing')
ORDER BY requested_at, id
LIMIT 1;

-- name: StartImport :exec
UPDATE customers.imports
SET started_at = COALESCE(started_at, sqlc.arg(started_at)::timestamptz), error = NULL, updated_at = sqlc.arg(started_at)::timestamptz
WHERE id = $1;

-- name: ImportStopRequested :one
SELECT stop_requested
FROM customers.imports
WHERE id = $1;

-- name: UpdateImportProgress :exec
UPDATE customers.imports
SET processed = $2, created = $3, updated = $4, failed = $5, updated_at = $6
WHERE id = $1;

-- name: FinishImport :exec
UPDATE customers.imports
SET state = $2, processed = $3, created = $4, updated = $5, failed = $6,
    finished_at = $7, stop_requested = false, error = NULL, updated_at = $8
WHERE id = $1;

-- name: FailImport :exec
UPDATE customers.imports
SET error = $2, updated_at = $3
WHERE id = $1;

-- name: ImportRowCounts :one
SELECT
    count(*) FILTER (WHERE outcome IS NOT NULL)      AS processed,
    count(*) FILTER (WHERE outcome = 'created')      AS created,
    count(*) FILTER (WHERE outcome = 'updated')      AS updated,
    count(*) FILTER (WHERE outcome = 'failed')       AS failed
FROM customers.import_rows
WHERE import_id = $1;

-- The next rows to run: the ones with no outcome, in file order.
-- name: ImportRowsChunk :many
SELECT row_no, cells
FROM customers.import_rows
WHERE import_id = $1 AND outcome IS NULL
ORDER BY row_no
LIMIT $2;

-- The in-file duplicate pass (import.go's inFileDuplicates): every row's
-- identity cells alone, in file order. cells[0] is NULL in PostgreSQL, which
-- is what an unmapped column reads as; COALESCE makes it the blank it means.
-- name: ImportRowIdentityCells :many
SELECT row_no, COALESCE((cells[@id_column::int])::text, '')::text AS legal_id, COALESCE((cells[@country_column::int])::text, '')::text AS legal_country
FROM customers.import_rows
WHERE import_id = $1
ORDER BY row_no;

-- name: WriteImportRowOutcome :exec
UPDATE customers.import_rows
SET outcome = $3, customer_id = $4, errors = $5
WHERE import_id = $1 AND row_no = $2;

-- The preview's rows: the first few, in file order.
-- name: ImportSampleRows :many
SELECT row_no, cells
FROM customers.import_rows
WHERE import_id = $1
ORDER BY row_no
LIMIT $2;

-- The rows page: every row, or those of one outcome, with the customer's
-- number beside a row that has one.
-- name: ImportRowsPage :many
SELECT r.row_no, r.cells, r.outcome, r.customer_id, r.errors, c.customer_number
FROM customers.import_rows r
LEFT JOIN customers.customers c ON c.id = r.customer_id
WHERE r.import_id = $1 AND (sqlc.narg(outcome)::text IS NULL OR r.outcome = sqlc.narg(outcome)::text)
ORDER BY r.row_no
LIMIT $2 OFFSET $3;

-- name: CountImportRowsPage :one
SELECT count(*)
FROM customers.import_rows
WHERE import_id = $1 AND (sqlc.narg(outcome)::text IS NULL OR outcome = sqlc.narg(outcome)::text);

-- name: FailedImportRows :many
SELECT row_no, cells, errors
FROM customers.import_rows
WHERE import_id = $1 AND outcome = 'failed'
ORDER BY row_no;

-- The suggestion's first tier (design D3): the mapping last saved on an import
-- with this very header.
-- name: LatestImportMappingForHeader :one
SELECT mapping
FROM customers.imports
WHERE header = $1 AND mapping IS NOT NULL
ORDER BY updated_at DESC, id DESC
LIMIT 1;

-- The sweep (design D6): every import not running whose last change is older
-- than the retention; the rows go by cascade.
-- name: SweepImports :execrows
DELETE FROM customers.imports
WHERE state NOT IN ('checking', 'importing') AND updated_at < $1;
