-- +goose Up
-- Inventory's placements (inventory phase 2 design, delivery B, I54–I61): per
-- variant and site, the primary location where it is put and taken, the
-- reorder point at or below which it is low, and the maximum a suggestion
-- fills up to. Kept apart from the balances (D §12.4): a placement is a
-- setting, never stock. A placement with nothing set is deleted, never
-- stored (I54), which the last check holds.
--
-- The SKU and name are the catalog's, snapshotted when the placement is
-- written and refreshed when a read of the low-stock list finds them changed:
-- they serve the list's order and search (I58), and the list shows the
-- catalog's current values. A site is deleted only when it never held stock,
-- so its placements go with it; a deleted location leaves the placement
-- without a primary location (I55), the application writing the
-- placement.cleared event first and the foreign key the backstop.
CREATE TABLE inventory.placements (
    variant_id          integer       NOT NULL,
    site_id             bigint        NOT NULL REFERENCES inventory.sites (id) ON DELETE CASCADE,
    primary_location_id bigint        REFERENCES inventory.locations (id) ON DELETE SET NULL,
    reorder_point       numeric(14,3),
    maximum             numeric(14,3),
    sku                 text          NOT NULL,
    product_name        text          NOT NULL,
    revision            integer       NOT NULL DEFAULT 1,
    updated_by_user_id  uuid,
    updated_by_display  varchar(200),
    updated_at          timestamptz   NOT NULL,
    PRIMARY KEY (variant_id, site_id),
    CONSTRAINT ck_placements_reorder_point CHECK (reorder_point IS NULL OR reorder_point >= 0),
    CONSTRAINT ck_placements_maximum CHECK (maximum IS NULL OR maximum > 0),
    CONSTRAINT ck_placements_maximum_reorder_point CHECK (maximum IS NULL OR reorder_point IS NULL OR maximum >= reorder_point),
    CONSTRAINT ck_placements_not_empty CHECK (num_nonnulls(primary_location_id, reorder_point, maximum) > 0)
);
-- The low-stock list's statement (I58) reads the placements with a reorder
-- point, per site; it orders by the joined site code, so this index serves
-- the per-site filter and the count, not the order.
CREATE INDEX ix_placements_site_sku ON inventory.placements (site_id, sku, variant_id)
    WHERE reorder_point IS NOT NULL;
-- Archiving or deleting a location clears the primaries that name it (I55).
CREATE INDEX ix_placements_primary_location ON inventory.placements (primary_location_id);

-- +goose Down
DROP TABLE inventory.placements;
