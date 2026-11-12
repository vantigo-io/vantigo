-- A variant's rules in Inventory (manufacturing phase 2 M100).

-- name: GetVariantRules :one
SELECT * FROM inventory.variant_rules WHERE variant_id = @variant_id;

-- name: GetVariantRulesForUpdate :one
SELECT * FROM inventory.variant_rules WHERE variant_id = @variant_id FOR UPDATE;

-- name: InsertVariantRules :one
-- InsertVariantRules writes a variant's first rules; a conflict is another
-- write that made them meanwhile, answered by no row.
INSERT INTO inventory.variant_rules (variant_id, inspect_on_receipt, updated_by_user_id, updated_by_display, updated_at)
VALUES (@variant_id, @inspect_on_receipt, sqlc.narg(updated_by_user_id), sqlc.narg(updated_by_display), @now)
ON CONFLICT (variant_id) DO NOTHING
RETURNING *;

-- name: UpdateVariantRules :one
UPDATE inventory.variant_rules
SET inspect_on_receipt = @inspect_on_receipt, updated_by_user_id = sqlc.narg(updated_by_user_id),
    updated_by_display = sqlc.narg(updated_by_display), updated_at = @now, revision = revision + 1
WHERE variant_id = @variant_id
RETURNING *;

-- name: InspectedVariants :many
-- InspectedVariants is the variants among those named whose receipts are
-- inspected.
SELECT variant_id FROM inventory.variant_rules
WHERE variant_id = ANY(@variant_ids::int[]) AND inspect_on_receipt
ORDER BY variant_id;

-- name: InspectionLotsOfMovement :many
-- InspectionLotsOfMovement is the lots a receipt put an inspection hold on,
-- released or not.
SELECT DISTINCT lot_id FROM inventory.lot_holds
WHERE source_module = 'inventory' AND source_kind = 'receipt' AND source_id = @source_id
ORDER BY lot_id;

-- name: CountLotsAwaitingInspection :one
-- CountLotsAwaitingInspection is the lots with an active inspection hold:
-- the inspection queue (M100).
SELECT count(DISTINCT lot_id)::int FROM inventory.lot_holds WHERE released_at IS NULL AND cause = 'inspection';
