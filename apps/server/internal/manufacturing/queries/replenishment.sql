-- Replenishment (manufacturing phase 2 M107–M111): the job's runs, the
-- projection's reads of this module's own orders, the suggestions and the
-- rules of automatic creation.

-- name: InsertNightlyRun :exec
-- InsertNightlyRun queues the day's nightly run once, however many workers
-- see 02:00 pass (ux_replenishment_runs_nightly).
INSERT INTO manufacturing.replenishment_runs (kind, nightly_for, requested_at)
VALUES ('nightly', @nightly_for::date, @now::timestamptz)
ON CONFLICT (nightly_for) WHERE nightly_for IS NOT NULL DO NOTHING;

-- name: InsertManualRun :one
INSERT INTO manufacturing.replenishment_runs (kind, site_id, requested_at, requested_by_user_id, requested_by_display)
VALUES ('manual', sqlc.narg(site_id), @now::timestamptz, sqlc.narg(requested_by_user_id), sqlc.narg(requested_by_display))
RETURNING *;

-- name: LockRunRequests :exec
-- LockRunRequests serialises the on-demand requests (M109's one a minute):
-- a transaction's advisory lock on one constant key.
SELECT pg_advisory_xact_lock(@lock_key::bigint);

-- name: RecentManualRun :one
-- RecentManualRun is the newest run asked for by a person since the instant
-- given, of the same site (every site when site_id is null).
SELECT * FROM manufacturing.replenishment_runs
WHERE kind = 'manual' AND requested_at >= @since::timestamptz AND site_id IS NOT DISTINCT FROM sqlc.narg(site_id)::bigint
ORDER BY requested_at DESC, id DESC
LIMIT 1;

-- name: ClaimRun :one
-- ClaimRun takes the oldest pending run, or a running one whose lease has
-- run out, moving its lease on — one statement, no lock held after it.
UPDATE manufacturing.replenishment_runs r
SET status = 'running', started_at = coalesce(r.started_at, @now::timestamptz), lease_until = @lease_until::timestamptz,
    attempts = r.attempts + 1
WHERE r.id = (
    SELECT id FROM manufacturing.replenishment_runs
    WHERE status = 'pending' OR (status = 'running' AND lease_until < @now::timestamptz)
    ORDER BY requested_at, id
    LIMIT 1
    FOR UPDATE SKIP LOCKED)
RETURNING r.*;

-- name: RenewRunLease :execrows
-- RenewRunLease moves a claimed run's lease on, before each site. The claim
-- is the attempt: no row when another worker has claimed the run since.
UPDATE manufacturing.replenishment_runs
SET lease_until = @lease_until::timestamptz
WHERE id = @id AND status = 'running' AND attempts = @attempts;

-- name: FinishRun :execrows
-- FinishRun records a run's end, only by the claim that ran it (its
-- attempt): no row when another worker has claimed the run since.
UPDATE manufacturing.replenishment_runs
SET status = @status, finished_at = @now::timestamptz, lease_until = NULL, suggestions = @suggestions,
    orders_created = @orders_created, sales_counted = @sales_counted, error = sqlc.narg(error)
WHERE id = @id AND status = 'running' AND attempts = @attempts;

-- name: PruneRuns :exec
-- PruneRuns removes the runs finished before the instant given (M109's 90
-- days), with the suggestions they made that are no longer open.
DELETE FROM manufacturing.replenishment_runs r
WHERE r.finished_at < @before::timestamptz
  AND NOT EXISTS (SELECT 1 FROM manufacturing.suggestions s WHERE s.run_id = r.id AND s.status = 'open');

-- name: ListRuns :many
SELECT * FROM manufacturing.replenishment_runs ORDER BY requested_at DESC, id DESC LIMIT @row_limit::int;

-- name: GetRun :one
SELECT * FROM manufacturing.replenishment_runs WHERE id = @id;

-- name: SupplyAtSite :many
-- SupplyAtSite is, per variant, what the site's supply orders still make of
-- it (M107): the open ones and the drafts from a suggestion, quantity less
-- produced, at least 0 — make-to-order orders left out.
SELECT variant_id, sum(greatest(quantity - produced, 0))::text AS remaining
FROM manufacturing.orders
WHERE site_id = @site_id AND origin_kind <> 'sales_order'
  AND (status IN ('confirmed', 'in_progress') OR (status = 'draft' AND origin_kind = 'reorder'))
GROUP BY variant_id
ORDER BY variant_id;

-- name: ComponentDemandAtSite :many
-- ComponentDemandAtSite is, per component variant, what the site's open
-- orders still need of it and do not hold reserved (M107): required less
-- consumed, scrapped and reserved, at least 0, per line — and what its
-- drafts from a suggestion, counted as supply, will need: no component line
-- is copied before the confirm, so their BoM's lines, scaled to the order's
-- quantity with the scrap, rounded up to three decimals as the confirm
-- rounds them (requiredOf).
SELECT d.variant_id::int AS variant_id, sum(d.unreserved)::text AS unreserved
FROM (
    SELECT c.variant_id, greatest(c.required - c.consumed - c.scrapped - c.reserved, 0) AS unreserved
    FROM manufacturing.order_components c
    JOIN manufacturing.orders o ON o.id = c.order_id
    WHERE o.site_id = @site_id AND o.status IN ('confirmed', 'in_progress')
    UNION ALL
    SELECT l.variant_id, ceil(l.quantity * o.quantity / b.output_quantity * (1 + l.scrap_percent / 100) * 1000) / 1000
    FROM manufacturing.orders o
    JOIN manufacturing.boms b ON b.id = o.bom_id
    JOIN manufacturing.bom_lines l ON l.bom_id = o.bom_id
    WHERE o.site_id = @site_id AND o.status = 'draft' AND o.origin_kind = 'reorder'
) AS d
GROUP BY d.variant_id
ORDER BY d.variant_id;

-- name: MtoRemainingByLine :many
-- MtoRemainingByLine is, per sales order line a live make-to-order order
-- serves, what those orders still make for it (M107): quantity less
-- produced, at least 0. That output is left out of supply, so the line's
-- demand is counted less it; what they produced already is on hand and
-- stays the line's demand.
SELECT origin_line_id::bigint AS line_id, sum(greatest(quantity - produced, 0))::text AS remaining
FROM manufacturing.orders
WHERE origin_kind = 'sales_order' AND origin_line_id IS NOT NULL AND status IN ('draft', 'confirmed', 'in_progress')
GROUP BY origin_line_id
ORDER BY origin_line_id;

-- name: HasOpenReorder :one
-- HasOpenReorder is whether the variant has an order from a suggestion
-- still open or a draft at the site, read under LockVariants (M111).
SELECT EXISTS (
    SELECT 1 FROM manufacturing.orders
    WHERE variant_id = @variant_id AND site_id = @site_id AND origin_kind = 'reorder'
      AND status IN ('draft', 'confirmed', 'in_progress'))::bool;

-- name: MarkSuggestionReorderOpen :exec
UPDATE manufacturing.suggestions SET reorder_open = true WHERE id = @id;

-- name: OpenReorderVariantsAt :many
-- OpenReorderVariantsAt is the variants with an order from a suggestion
-- still open or a draft at the site (M111's automatic creation skips them).
SELECT DISTINCT variant_id
FROM manufacturing.orders
WHERE site_id = @site_id AND origin_kind = 'reorder' AND status IN ('draft', 'confirmed', 'in_progress')
ORDER BY variant_id;

-- name: LockSuggestionSite :exec
-- LockSuggestionSite serialises two runs replacing one site's suggestions:
-- a transaction's advisory lock on the site's key (suggestionSiteKey).
SELECT pg_advisory_xact_lock(@site_key::bigint);

-- name: SupersedeOpenSuggestions :exec
UPDATE manufacturing.suggestions SET status = 'superseded'
WHERE site_id = @site_id AND status = 'open';

-- name: SupersedeSuggestionsNotAt :exec
-- SupersedeSuggestionsNotAt supersedes the open suggestions of every site
-- but the active ones given: a site archived since its last run (M109).
UPDATE manufacturing.suggestions SET status = 'superseded'
WHERE status = 'open' AND NOT (site_id = ANY(@site_ids::bigint[]));

-- name: InsertSuggestion :one
INSERT INTO manufacturing.suggestions (run_id, site_id, variant_id, sku, product_name, kind, quantity, need_by, reason,
                                       bom_id, reorder_open, created_at)
VALUES (@run_id, @site_id, @variant_id, @sku, @product_name, @kind, @quantity, @need_by, @reason,
        sqlc.narg(bom_id), @reorder_open, @now::timestamptz)
RETURNING id;

-- name: ListOpenSuggestions :many
-- ListOpenSuggestions is the open suggestions, of one site or every one,
-- make before buy, then by SKU.
SELECT * FROM manufacturing.suggestions
WHERE status = 'open' AND (sqlc.narg(site_id)::bigint IS NULL OR site_id = sqlc.narg(site_id)::bigint)
ORDER BY site_id, kind = 'buy', sku, variant_id;

-- name: GetSuggestion :one
SELECT * FROM manufacturing.suggestions WHERE id = @id;

-- name: LockSuggestion :one
SELECT * FROM manufacturing.suggestions WHERE id = @id FOR UPDATE;

-- name: MarkSuggestionDone :exec
UPDATE manufacturing.suggestions
SET status = 'done', order_id = @order_id, acted_by_user_id = sqlc.narg(acted_by_user_id),
    acted_by_display = sqlc.narg(acted_by_display), acted_at = @now::timestamptz
WHERE id = @id;

-- name: DismissSuggestion :one
UPDATE manufacturing.suggestions
SET status = 'dismissed', acted_by_user_id = sqlc.narg(acted_by_user_id),
    acted_by_display = sqlc.narg(acted_by_display), acted_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: CountOpenMakeSuggestions :one
-- CountOpenMakeSuggestions is the open make suggestions of the active sites
-- given.
SELECT count(*)::int FROM manufacturing.suggestions
WHERE status = 'open' AND kind = 'make' AND site_id = ANY(@site_ids::bigint[]);

-- name: RulesAt :many
SELECT * FROM manufacturing.replenishment_rules WHERE site_id = @site_id AND auto_create ORDER BY variant_id;

-- name: RulesOfVariant :many
SELECT * FROM manufacturing.replenishment_rules WHERE variant_id = @variant_id ORDER BY site_id;

-- name: UpsertRule :exec
INSERT INTO manufacturing.replenishment_rules (variant_id, site_id, auto_create, auto_confirm, updated_by_user_id,
                                               updated_by_display, updated_at)
VALUES (@variant_id, @site_id, @auto_create, @auto_confirm, sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display),
        @now::timestamptz)
ON CONFLICT (variant_id, site_id) DO UPDATE
SET auto_create = excluded.auto_create, auto_confirm = excluded.auto_confirm,
    updated_by_user_id = excluded.updated_by_user_id, updated_by_display = excluded.updated_by_display,
    updated_at = excluded.updated_at;

-- name: DeleteRule :exec
DELETE FROM manufacturing.replenishment_rules WHERE variant_id = @variant_id AND site_id = @site_id;
