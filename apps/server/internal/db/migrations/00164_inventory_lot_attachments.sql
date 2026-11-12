-- +goose Up
-- A lot's files (manufacturing phase 2 M54): an EN 10204 3.1 certificate, a
-- supplier's analysis, a photo. The row points at its object in the
-- inventory-lots storage scope under lots/<uuid> — a key that names no lot
-- and is never derived from the file's name. uploaded_by_user_id is
-- identity's user, opaque here (module boundaries rule 4); the display is
-- snapshotted as the uploader was. Rows are deleted, never marked: a delete
-- inserts the object's ledger row in the same transaction.
CREATE TABLE inventory.lot_attachments (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    lot_id              bigint        NOT NULL REFERENCES inventory.lots (id),
    object_key          varchar(200)  NOT NULL,
    file_name           varchar(255)  NOT NULL,
    content_type        varchar(100)  NOT NULL,
    size_bytes          bigint        NOT NULL,
    uploaded_by_user_id uuid,
    uploaded_by_display varchar(200),
    created_at          timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_lot_attachments_object_key ON inventory.lot_attachments (object_key);
-- The lot page lists them newest first, and the cap counts by lot.
CREATE INDEX ix_lot_attachments_lot ON inventory.lot_attachments (lot_id, created_at DESC, id DESC);

-- The objects still to be removed from the store (M54): a delete removes the
-- row and inserts its object's key here in one transaction, and the
-- inventory.object-deletions worker finishes the object after the commit,
-- retried until the store confirms. Customers' customers.object_deletions
-- (00044), column for column.
CREATE TABLE inventory.object_deletions (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    object_key      varchar(200) NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz  NOT NULL,
    claimed_until   timestamptz,
    last_error      varchar(200),
    created_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_inventory_object_deletions_object_key ON inventory.object_deletions (object_key);
CREATE INDEX ix_inventory_object_deletions_next_attempt ON inventory.object_deletions (next_attempt_at);

-- +goose Down
DROP TABLE inventory.object_deletions;
DROP TABLE inventory.lot_attachments;
