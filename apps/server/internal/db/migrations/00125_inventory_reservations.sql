-- +goose Up
-- Inventory's reservations (orders design O45, inventory design §11 and
-- §12.6): stock another module has promised, per variant and site, held
-- against a line of that module's document. Available is on hand at the site
-- (summed over its locations) less what is reserved there; the balances gain
-- no reserved column. A reservation is written only through
-- contracts.StockLedger, on the caller's transaction (module-boundaries rule
-- 24), and is keyed by the source line and the site, so a second reserve of
-- the same line adds to the row instead of writing a second one.
--
-- The source is opaque text, as a movement's is: the module, the kind of its
-- document, the document's id and the line's id. No key crosses a schema.
CREATE TABLE inventory.reservations (
    id             bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    variant_id     integer       NOT NULL,
    site_id        bigint        NOT NULL REFERENCES inventory.sites (id),
    quantity       numeric(14,3) NOT NULL CHECK (quantity > 0),
    source_module  text          NOT NULL,
    source_kind    text          NOT NULL,
    source_id      text          NOT NULL,
    source_line_id text          NOT NULL,
    created_at     timestamptz   NOT NULL,
    updated_at     timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_reservations_source_line_site
    ON inventory.reservations (source_module, source_kind, source_id, source_line_id, site_id);
CREATE INDEX ix_reservations_variant_site ON inventory.reservations (variant_id, site_id);

-- A site that fulfils orders (orders design O45): a reservation and an issue
-- are refused at a site without it. A warehouse does by default, a store does
-- not; the flag is the site's own, changed by inventory:locations-manage.
ALTER TABLE inventory.sites ADD COLUMN fulfils_orders boolean NOT NULL DEFAULT false;
UPDATE inventory.sites SET fulfils_orders = (kind = 'warehouse');

-- +goose Down
ALTER TABLE inventory.sites DROP COLUMN fulfils_orders;
DROP TABLE inventory.reservations;
