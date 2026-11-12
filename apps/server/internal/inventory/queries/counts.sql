-- Counts (design I16–I20): a count of a site or of one location's subtree,
-- its lines frozen at opening, counted while it is counting, and posted as one
-- count movement or cancelled. Opening locks the site row FOR UPDATE; every
-- other write locks the count row FOR UPDATE first (§4).

-- name: OverlappingCount :one
-- OverlappingCount is a count still counting in the site whose scope overlaps
-- the scope given: the same location, one above it (its ancestors), or one
-- below it (I17's count_scope_overlaps).
SELECT c.id, t.full_code
FROM inventory.counts c
JOIN inventory.locations t ON t.id = c.scope_location_id
WHERE c.site_id = @site_id AND c.status = 'counting'
  AND (t.id = @scope_id OR t.id = ANY(@scope_ancestors::bigint[]) OR t.ancestors @> ARRAY[@scope_id::bigint])
ORDER BY c.id
LIMIT 1;

-- name: InsertCount :one
INSERT INTO inventory.counts (site_id, scope_location_id, status, note, opened_by_user_id, opened_by_display, opened_at)
VALUES (@site_id, @scope_location_id, 'counting', sqlc.narg(note), sqlc.narg(opened_by_user_id), sqlc.narg(opened_by_display),
        @opened_at::timestamptz)
RETURNING id;

-- name: FreezeCountLines :execrows
-- FreezeCountLines writes a line per variant and location in the count's
-- scope, expecting what it holds at this moment, summed over its lots (I17;
-- counts per lot are manufacturing phase 2 L2's M69).
INSERT INTO inventory.count_lines (count_id, variant_id, location_id, expected)
SELECT @count_id, b.variant_id, b.location_id, sum(b.on_hand)
FROM inventory.balances b
JOIN inventory.locations l ON l.id = b.location_id
WHERE l.id = @scope_id OR l.ancestors @> ARRAY[@scope_id::bigint]
GROUP BY b.location_id, b.variant_id
ORDER BY b.location_id, b.variant_id;

-- name: GetCount :one
SELECT * FROM inventory.counts WHERE id = @id;

-- name: LockCount :one
-- LockCount takes the count row FOR UPDATE: the first lock of every write to
-- a count once it is open (§4).
SELECT * FROM inventory.counts WHERE id = @id FOR UPDATE;

-- name: CountLines :many
-- CountLines is a count's lines with their location's full code and the
-- balance they stand on now (0 when there is none).
SELECT cl.id, cl.count_id, cl.variant_id, cl.location_id, cl.expected, cl.counted, cl.entered, cl.added,
       cl.counted_by_user_id, cl.counted_by_display, cl.counted_at, l.full_code,
       coalesce((SELECT sum(b.on_hand) FROM inventory.balances b
                 WHERE b.variant_id = cl.variant_id AND b.location_id = cl.location_id), 0)::numeric AS current
FROM inventory.count_lines cl
JOIN inventory.locations l ON l.id = cl.location_id
WHERE cl.count_id = @count_id
ORDER BY l.full_code, cl.variant_id;

-- name: CountLinePairs :many
-- CountLinePairs is the (variant, location) pairs a count has lines for.
SELECT variant_id, location_id FROM inventory.count_lines WHERE count_id = @count_id;

-- name: UpsertCountLines :exec
-- UpsertCountLines records counted quantities (I18): a pair with a line is
-- changed, a pair without one gets a line, expected 0 and added. Each change
-- records who counted and when.
INSERT INTO inventory.count_lines (count_id, variant_id, location_id, expected, counted, entered, added,
                                   counted_by_user_id, counted_by_display, counted_at)
SELECT @count_id, u.variant_id, u.location_id, 0, u.counted, nullif(u.entered, '')::jsonb, true,
       sqlc.narg(counted_by_user_id), sqlc.narg(counted_by_display), @counted_at::timestamptz
FROM (SELECT unnest(@variant_ids::int[]) AS variant_id, unnest(@location_ids::bigint[]) AS location_id,
             unnest(@counteds::numeric[]) AS counted, unnest(@entereds::text[]) AS entered) AS u
ON CONFLICT (count_id, variant_id, location_id) DO UPDATE
SET counted = excluded.counted, entered = excluded.entered, counted_by_user_id = excluded.counted_by_user_id,
    counted_by_display = excluded.counted_by_display, counted_at = excluded.counted_at;

-- name: TouchCount :exec
-- TouchCount moves a count's revision on after its lines changed.
UPDATE inventory.counts SET revision = revision + 1 WHERE id = @id;

-- name: PostCount :exec
-- PostCount marks a count posted, keeping the movement it wrote (none when
-- nothing differed) and how its uncounted lines were treated (I19).
UPDATE inventory.counts
SET status = 'posted', uncounted = @uncounted, movement_id = sqlc.narg(movement_id), posted_by_user_id = sqlc.narg(posted_by_user_id),
    posted_by_display = sqlc.narg(posted_by_display), posted_at = @posted_at::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: CancelCount :exec
UPDATE inventory.counts
SET status = 'cancelled', cancelled_by_user_id = sqlc.narg(cancelled_by_user_id), cancelled_by_display = sqlc.narg(cancelled_by_display),
    cancelled_at = @cancelled_at::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: ListCounts :many
-- ListCounts is the counts newest first, at most 200, filtered by status and
-- site (each NULL for any), each with its number of lines, of counted lines,
-- and — while it is counting — of lines whose balance moved since it opened.
SELECT c.*,
       (SELECT count(*) FROM inventory.count_lines cl WHERE cl.count_id = c.id)::int AS line_count,
       (SELECT count(*) FROM inventory.count_lines cl WHERE cl.count_id = c.id AND cl.counted IS NOT NULL)::int AS counted_lines,
       (CASE WHEN c.status = 'counting' THEN (
            SELECT count(*) FROM inventory.count_lines cl
            WHERE cl.count_id = c.id
              AND coalesce((SELECT sum(b.on_hand) FROM inventory.balances b
                            WHERE b.variant_id = cl.variant_id AND b.location_id = cl.location_id), 0) <> cl.expected)
        ELSE 0 END)::int AS moved_lines
FROM inventory.counts c
WHERE (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
  AND (sqlc.narg(site_id)::bigint IS NULL OR c.site_id = sqlc.narg(site_id)::bigint)
ORDER BY c.opened_at DESC, c.id DESC
LIMIT 200;

-- name: LongOpenCounts :many
-- LongOpenCounts is the counts still counting that were opened before the
-- moment given, oldest first, with their scope's full code: the dashboard's
-- countOpenLong.
SELECT c.id, c.site_id, c.opened_at, l.full_code
FROM inventory.counts c
JOIN inventory.locations l ON l.id = c.scope_location_id
WHERE c.status = 'counting' AND c.opened_at < @before::timestamptz
ORDER BY c.opened_at, c.id
LIMIT 20;
