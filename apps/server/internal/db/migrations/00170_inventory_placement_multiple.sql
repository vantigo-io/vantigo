-- +goose Up
-- A placement's multiple (manufacturing phase 2 M105): the quantity a
-- replenishment suggestion, and the low-stock list's, is rounded up to, before
-- the packaging rounding. Unset is no rounding. It is not one of the fields
-- that keep a placement stored: a placement with only a multiple is
-- placement_empty, as one with nothing is.
ALTER TABLE inventory.placements
    ADD COLUMN multiple numeric(14,3),
    ADD CONSTRAINT ck_placements_multiple CHECK (multiple IS NULL OR multiple > 0);

-- +goose Down
ALTER TABLE inventory.placements DROP COLUMN multiple;
