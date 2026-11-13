-- +goose Up
-- A kit line's child lines (manufacturing phase 2 M145): each component of a
-- kit is an order line of its own, under the kit line, so it reserves, is
-- delivered, picks lots and is cancelled like any goods line. per_kit is how
-- much of it one kit holds, copied from contracts.KitStructure when the kit
-- line is added; the child's quantity is the kit line's × per_kit. The kit
-- line carries the price; its children are priced 0 and not invoiced on their
-- own. A save writes a document's lines whole, so a removed kit line takes its
-- children with it.
ALTER TABLE orders.lines ADD COLUMN per_kit numeric(14, 3);
ALTER TABLE orders.lines DROP CONSTRAINT lines_parent_line_id_fkey;
ALTER TABLE orders.lines
    ADD CONSTRAINT lines_parent_line_id_fkey FOREIGN KEY (parent_line_id) REFERENCES orders.lines (id) ON DELETE CASCADE;
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_per_kit CHECK (per_kit IS NULL OR per_kit > 0);
ALTER TABLE orders.lines ADD CONSTRAINT ck_lines_kit_child CHECK ((parent_line_id IS NULL) = (per_kit IS NULL));

-- +goose Down
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_kit_child;
ALTER TABLE orders.lines DROP CONSTRAINT ck_lines_per_kit;
ALTER TABLE orders.lines DROP CONSTRAINT lines_parent_line_id_fkey;
ALTER TABLE orders.lines DROP COLUMN per_kit;
ALTER TABLE orders.lines
    ADD CONSTRAINT lines_parent_line_id_fkey FOREIGN KEY (parent_line_id) REFERENCES orders.lines (id) ON DELETE RESTRICT;
