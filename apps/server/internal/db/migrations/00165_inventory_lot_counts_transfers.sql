-- +goose Up
-- Counts and transfers with lots (manufacturing phase 2 M69, M70).
--
-- A count line is one balance: a variant on a location in a lot, NULL for
-- none. Opening a count freezes one line per balance row, so a tracked
-- variant has a line per lot; the key is NULLS NOT DISTINCT, as the
-- balances' own (00163), so an untracked variant still has one line per
-- location.
ALTER TABLE inventory.count_lines ADD COLUMN lot_id bigint REFERENCES inventory.lots (id);
DROP INDEX inventory.ux_count_lines_count_variant_location;
CREATE UNIQUE INDEX ux_count_lines_key ON inventory.count_lines (count_id, variant_id, location_id, lot_id) NULLS NOT DISTINCT;

-- A transfer line carries one lot (M70): two lots of a variant travel on two
-- lines, so the action lines and the line totals keep their shape. A draft
-- line may name it; the send requires it for a tracked variant, and the
-- receive and the cancel post the same lot.
ALTER TABLE inventory.transfer_lines ADD COLUMN lot_id bigint REFERENCES inventory.lots (id);
CREATE INDEX ix_transfer_lines_lot ON inventory.transfer_lines (lot_id) WHERE lot_id IS NOT NULL;

-- +goose Down
-- A down is for development: it fails while any count has two lines of one
-- (variant, location).
DROP INDEX inventory.ix_transfer_lines_lot;
ALTER TABLE inventory.transfer_lines DROP COLUMN lot_id;
DROP INDEX inventory.ux_count_lines_key;
CREATE UNIQUE INDEX ux_count_lines_count_variant_location ON inventory.count_lines (count_id, variant_id, location_id);
ALTER TABLE inventory.count_lines DROP COLUMN lot_id;
