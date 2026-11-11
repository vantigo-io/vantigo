-- Bills of materials (manufacturing design M10–M16, M14a): a BoM, its
-- operations and its lines. Every structural write locks the variants through
-- ProductCatalog.LockVariants, then the BoM graph (LockBomGraph), then its own
-- rows.

-- name: LockBomGraph :exec
-- LockBomGraph serialises every structural BoM write (M14a): the
-- transaction-scoped advisory lock of class 19782 (0x4D46, "MF", the
-- manufacturing class key) and key 1, the BoM graph. It is taken after
-- LockVariants and before any of this module's rows.
SELECT pg_advisory_xact_lock(19782, 1);

-- name: ListBoms :many
-- ListBoms is a page of BoMs in one status (every status when status is
-- null), of one variant when variant_id is set, and — when q is set — whose
-- code or note contains q or whose variant is among q_variant_ids (the catalog's
-- matches for q, read before), newest change first, with their line and
-- operation counts.
SELECT b.*,
       (SELECT count(*) FROM manufacturing.bom_lines l WHERE l.bom_id = b.id)::int AS line_count,
       (SELECT count(*) FROM manufacturing.bom_operations o WHERE o.bom_id = b.id)::int AS operation_count
FROM manufacturing.boms b
WHERE (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
  AND (sqlc.narg(variant_id)::int IS NULL OR b.variant_id = sqlc.narg(variant_id)::int)
  AND (sqlc.narg(q)::text IS NULL
       OR b.code ILIKE '%' || sqlc.narg(q)::text || '%'
       OR b.note ILIKE '%' || sqlc.narg(q)::text || '%'
       OR b.variant_id = ANY(@q_variant_ids::int[]))
ORDER BY b.updated_at DESC, b.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: CountBoms :one
-- CountBoms is how many BoMs ListBoms' filters match.
SELECT count(*)::int FROM manufacturing.boms b
WHERE (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
  AND (sqlc.narg(variant_id)::int IS NULL OR b.variant_id = sqlc.narg(variant_id)::int)
  AND (sqlc.narg(q)::text IS NULL
       OR b.code ILIKE '%' || sqlc.narg(q)::text || '%'
       OR b.note ILIKE '%' || sqlc.narg(q)::text || '%'
       OR b.variant_id = ANY(@q_variant_ids::int[]));

-- name: GetBom :one
SELECT * FROM manufacturing.boms WHERE id = @id;

-- name: GetBomForUpdate :one
-- GetBomForUpdate takes the BoM's row FOR UPDATE, after LockBomGraph.
SELECT * FROM manufacturing.boms WHERE id = @id FOR UPDATE;

-- name: BomCodeTaken :one
-- BomCodeTaken reports whether another BoM has the code (bom_code_taken); id
-- 0 asks about a new one.
SELECT EXISTS (SELECT 1 FROM manufacturing.boms WHERE code = @code AND id <> @id);

-- name: InsertBom :one
INSERT INTO manufacturing.boms (variant_id, code, output_quantity, is_default, make_to_order, lead_time_days, note,
                                created_at, updated_at)
VALUES (@variant_id, sqlc.narg(code), @output_quantity, @is_default, @make_to_order, @lead_time_days, @note,
        @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: UpdateBom :one
-- UpdateBom replaces a BoM's own fields and moves its revision on; its
-- variant and its default are not an edit's.
UPDATE manufacturing.boms
SET code = sqlc.narg(code), output_quantity = @output_quantity, make_to_order = @make_to_order,
    lead_time_days = @lead_time_days, note = @note, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetBomStatus :one
-- SetBomStatus archives or reactivates a BoM, with whether it is its
-- variant's default after, and moves its revision on.
UPDATE manufacturing.boms
SET status = @status, is_default = @is_default, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: SetBomDefault :one
-- SetBomDefault makes a BoM its variant's default and moves its revision
-- on. UnsetVariantDefault runs first in the same transaction, so the partial
-- unique index holds; a race that trips it anyway is revision_conflict.
UPDATE manufacturing.boms
SET is_default = true, revision = revision + 1, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UnsetVariantDefault :many
-- UnsetVariantDefault unmarks the variant's default BoM, but for except_id,
-- moving its revision on, and answers the ids it unmarked.
UPDATE manufacturing.boms
SET is_default = false, revision = revision + 1, updated_at = @now::timestamptz
WHERE variant_id = @variant_id AND is_default AND id <> @except_id
RETURNING id;

-- name: OldestOtherActiveBom :one
-- OldestOtherActiveBom is the variant's oldest active BoM but except_id: the
-- one archiving the default promotes (M14a). No row means there is none.
SELECT * FROM manufacturing.boms
WHERE variant_id = @variant_id AND status = 'active' AND id <> @except_id
ORDER BY created_at, id
LIMIT 1;

-- name: VariantHasActiveDefault :one
-- VariantHasActiveDefault reports whether the variant has an active default
-- BoM other than except_id.
SELECT EXISTS (SELECT 1 FROM manufacturing.boms
               WHERE variant_id = @variant_id AND is_default AND status = 'active' AND id <> @except_id);

-- name: DefaultActiveBomsOfVariants :many
-- DefaultActiveBomsOfVariants is, per variant among variant_ids that has
-- one, its default active BoM: the cycle and depth walk (M14) and the
-- overview (M15) recurse through it.
SELECT id, variant_id, output_quantity FROM manufacturing.boms
WHERE variant_id = ANY(@variant_ids::int[]) AND is_default AND status = 'active'
ORDER BY variant_id;

-- name: VariantsWithActiveBom :many
-- VariantsWithActiveBom is which of variant_ids have at least one active
-- BoM, for the variant search's hasBom.
SELECT DISTINCT variant_id FROM manufacturing.boms
WHERE variant_id = ANY(@variant_ids::int[]) AND status = 'active'
ORDER BY variant_id;

-- name: OperationsOfBoms :many
-- OperationsOfBoms is the operations of the BoMs, by BoM and sequence, each
-- with its work center's code, name, site and rates.
SELECT o.*, w.code AS work_center_code, w.name AS work_center_name, w.site_id AS work_center_site_id,
       w.status AS work_center_status, w.capacity AS work_center_capacity,
       w.time_efficiency AS work_center_time_efficiency, w.setup_minutes AS work_center_setup_minutes,
       w.cleanup_minutes AS work_center_cleanup_minutes, w.machine_cost_per_hour AS work_center_machine_cost_per_hour,
       w.labour_cost_per_hour AS work_center_labour_cost_per_hour
FROM manufacturing.bom_operations o
JOIN manufacturing.work_centers w ON w.id = o.work_center_id
WHERE o.bom_id = ANY(@bom_ids::bigint[])
ORDER BY o.bom_id, o.sequence, o.id;

-- name: LinesOfBoms :many
-- LinesOfBoms is the lines of the BoMs, by BoM and sort order.
SELECT * FROM manufacturing.bom_lines
WHERE bom_id = ANY(@bom_ids::bigint[])
ORDER BY bom_id, sort_order, id;

-- name: DeleteBomOperationsExcept :exec
-- DeleteBomOperationsExcept removes the BoM's operations an edit no longer
-- keeps; a line that named one has its operation_id cleared by the key
-- (M13).
DELETE FROM manufacturing.bom_operations WHERE bom_id = @bom_id AND NOT (id = ANY(@keep_ids::bigint[]));

-- name: InsertBomOperation :one
INSERT INTO manufacturing.bom_operations (bom_id, sequence, name, work_center_id, setup_minutes, minutes_per_cycle, instructions)
VALUES (@bom_id, @sequence, @name, @work_center_id, sqlc.narg(setup_minutes), @minutes_per_cycle, @instructions)
RETURNING *;

-- name: UpdateBomOperation :one
-- UpdateBomOperation rewrites an operation the edit keeps; 0 rows is an id
-- that is not one of the BoM's.
UPDATE manufacturing.bom_operations
SET sequence = @sequence, name = @name, work_center_id = @work_center_id, setup_minutes = sqlc.narg(setup_minutes),
    minutes_per_cycle = @minutes_per_cycle, instructions = @instructions
WHERE id = @id AND bom_id = @bom_id
RETURNING *;

-- name: DeleteBomLines :exec
-- DeleteBomLines removes every line of the BoM: an edit replaces them.
DELETE FROM manufacturing.bom_lines WHERE bom_id = @bom_id;

-- name: InsertBomLine :one
INSERT INTO manufacturing.bom_lines (bom_id, variant_id, quantity, scrap_percent, operation_id, sort_order, note)
VALUES (@bom_id, @variant_id, @quantity, @scrap_percent, sqlc.narg(operation_id), @sort_order, @note)
RETURNING *;

-- name: CountActiveBomsNamingVariant :one
-- CountActiveBomsNamingVariant is how many active BoMs name the variant as
-- their product or on a line (M16): the holder's answer. An archived BoM
-- holds nothing.
SELECT count(*)::int FROM manufacturing.boms b
WHERE b.status = 'active'
  AND (b.variant_id = @variant_id
       OR EXISTS (SELECT 1 FROM manufacturing.bom_lines l WHERE l.bom_id = b.id AND l.variant_id = @variant_id));
