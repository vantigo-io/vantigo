-- +goose Up
-- Kits (manufacturing phase 2 M144): a bill of materials of kind kit lists
-- the goods sold together under one variant. It has lines and nothing else —
-- no operations, no by-products — it is never made (no manufacturing order,
-- never made to order: kit_not_manufactured), and kits nest at most three
-- levels. Orders copies a kit's components onto an order line's child lines
-- through contracts.KitStructure (M145). The kind is set when the BoM is
-- created and never changes.
ALTER TABLE manufacturing.boms ADD COLUMN kind text NOT NULL DEFAULT 'normal';
ALTER TABLE manufacturing.boms ADD CONSTRAINT ck_boms_kind CHECK (kind IN ('normal', 'kit'));
ALTER TABLE manufacturing.boms ADD CONSTRAINT ck_boms_kit_not_made_to_order CHECK (kind = 'normal' OR NOT make_to_order);

-- +goose Down
ALTER TABLE manufacturing.boms DROP CONSTRAINT ck_boms_kit_not_made_to_order;
ALTER TABLE manufacturing.boms DROP CONSTRAINT ck_boms_kind;
ALTER TABLE manufacturing.boms DROP COLUMN kind;
