-- +goose Up
-- Inventory's transfers between sites (inventory phase 2 design, delivery A,
-- I35–I53): a document of its own that sends stock out of one site, holds it
-- in transit on its lines, and receives it at another — in parts, closed with
-- a difference, or cancelled back to where it left. A transfer posts two kinds
-- of movement, transfer_out at the from site and transfer_in at the to site;
-- each stays in one site, so the movements table does not change (I65).
--
-- In transit is not a balance (I46): it is the running totals on a line,
-- sent less received, missing and returned, held non-negative by a check.
-- Every send, receive and cancel writes an action and its lines (I38), the
-- record of who did what and where each part went; the movement's
-- client_request_id is its action's, so one key claims both.

-- The display-number series (I36), the Orders shape: one row per series,
-- transfer in this phase, allocated by upsert under the row's lock when the
-- document is created; next_value is the next number the series hands out.
CREATE TABLE inventory.counters (
    counter_name varchar(20) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

-- A transfer (I36): a draft holds nothing (I37) and may be edited and deleted;
-- send, receive and cancel move it through its statuses. revision is checked
-- by If-Match on every write but receive (I41). Who created, sent and closed
-- it is snapshotted (D rule 19). close_reason and close_note are the
-- difference's, written by a closing receive (I42).
CREATE TABLE inventory.transfers (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number             bigint        NOT NULL,
    from_site_id       bigint        NOT NULL REFERENCES inventory.sites (id),
    to_site_id         bigint        NOT NULL REFERENCES inventory.sites (id),
    status             varchar(20)   NOT NULL,
    reference          varchar(100),
    note               varchar(2000),
    expected_arrival   date,
    close_reason       varchar(20),
    close_note         varchar(500),
    revision           integer       NOT NULL DEFAULT 1,
    created_by_user_id uuid,
    created_by_display varchar(200),
    created_at         timestamptz   NOT NULL,
    sent_at            timestamptz,
    sent_by_user_id    uuid,
    sent_by_display    varchar(200),
    closed_at          timestamptz,
    closed_by_user_id  uuid,
    closed_by_display  varchar(200),
    updated_at         timestamptz   NOT NULL,
    CONSTRAINT ck_transfers_sites CHECK (from_site_id <> to_site_id),
    CONSTRAINT ck_transfers_status CHECK (status IN ('draft', 'sent', 'partly_received', 'received', 'cancelled')),
    CONSTRAINT ck_transfers_close_reason CHECK (close_reason IS NULL OR close_reason IN ('lost', 'damaged', 'not_packed', 'other'))
);
CREATE UNIQUE INDEX ux_transfers_number ON inventory.transfers (number);
CREATE INDEX ix_transfers_created ON inventory.transfers (created_at DESC, id DESC);
CREATE INDEX ix_transfers_status_created ON inventory.transfers (status, created_at DESC);
CREATE INDEX ix_transfers_from_site_status ON inventory.transfers (from_site_id, status);
CREATE INDEX ix_transfers_to_site_status ON inventory.transfers (to_site_id, status);

-- A transfer line (I36, I46): a variant and a quantity, with the SKU and name
-- snapshotted when written and again at send; the unit and the source
-- location are set at send. entered keeps the packagings the quantity was
-- given in (D I25); suggestion is the low-stock list's rounding (B, I57),
-- stored and returned as given. The four running totals are what is read:
-- in transit is sent - received - missing - returned. Only a draft is ever
-- deleted, so its lines go with it.
CREATE TABLE inventory.transfer_lines (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    transfer_id      bigint        NOT NULL REFERENCES inventory.transfers (id) ON DELETE CASCADE,
    line_no          integer       NOT NULL,
    variant_id       integer       NOT NULL,
    sku              text          NOT NULL,
    product_name     text          NOT NULL,
    unit             text,
    quantity         numeric(14,3) NOT NULL CHECK (quantity > 0),
    entered          jsonb,
    suggestion       jsonb,
    from_location_id bigint        REFERENCES inventory.locations (id),
    sent             numeric(14,3) NOT NULL DEFAULT 0,
    received         numeric(14,3) NOT NULL DEFAULT 0,
    missing          numeric(14,3) NOT NULL DEFAULT 0,
    returned         numeric(14,3) NOT NULL DEFAULT 0,
    CONSTRAINT ck_transfer_lines_totals CHECK (received + missing + returned <= sent)
);
CREATE UNIQUE INDEX ux_transfer_lines_transfer_line ON inventory.transfer_lines (transfer_id, line_no);
CREATE INDEX ix_transfer_lines_variant_in_transit ON inventory.transfer_lines (variant_id)
    WHERE sent > received + missing + returned;
CREATE INDEX ix_transfer_lines_from_location_in_transit ON inventory.transfer_lines (from_location_id)
    WHERE sent > received + missing + returned;
-- The list's search by a line's SKU ignores case, as Products' lookups do.
CREATE INDEX ix_transfer_lines_sku ON inventory.transfer_lines (lower(sku));

-- An action (I38): one send, receive or cancel. client_request_id is the
-- action's — derived from the transfer for a send and a send-and-receive's
-- receive (I39, I43), the client's for a receive and a cancel (I40, I44) — and
-- request_hash tells a replay from a reuse. A
-- close-only receive posts no movement. The poster is snapshotted.
CREATE TABLE inventory.transfer_actions (
    id                bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    transfer_id       bigint       NOT NULL REFERENCES inventory.transfers (id),
    kind              varchar(10)  NOT NULL,
    client_request_id uuid         NOT NULL,
    request_hash      bytea        NOT NULL,
    movement_id       bigint       REFERENCES inventory.movements (id),
    close_reason      varchar(20),
    close_note        varchar(500),
    posted_by_user_id uuid,
    posted_by_display varchar(200),
    posted_at         timestamptz  NOT NULL,
    CONSTRAINT ck_transfer_actions_kind CHECK (kind IN ('send', 'receive', 'cancel'))
);
CREATE UNIQUE INDEX ux_transfer_actions_client_request ON inventory.transfer_actions (client_request_id);
CREATE INDEX ix_transfer_actions_transfer_posted ON inventory.transfer_actions (transfer_id, posted_at);
-- A movement's link back to its transfer (I45) is read through its action.
CREATE INDEX ix_transfer_actions_movement ON inventory.transfer_actions (movement_id) WHERE movement_id IS NOT NULL;

-- An action line (I38): what one action did to one transfer line — sent,
-- received, missing or returned — and the location: the source for sent and
-- returned, the destination for received, none for missing (I42).
CREATE TABLE inventory.transfer_action_lines (
    action_id        bigint        NOT NULL REFERENCES inventory.transfer_actions (id),
    transfer_line_id bigint        NOT NULL REFERENCES inventory.transfer_lines (id),
    kind             varchar(10)   NOT NULL,
    quantity         numeric(14,3) NOT NULL CHECK (quantity > 0),
    location_id      bigint        REFERENCES inventory.locations (id),
    PRIMARY KEY (action_id, transfer_line_id, kind),
    CONSTRAINT ck_transfer_action_lines_kind CHECK (kind IN ('sent', 'received', 'missing', 'returned'))
);
CREATE INDEX ix_transfer_action_lines_line ON inventory.transfer_action_lines (transfer_line_id);

-- +goose Down
DROP TABLE inventory.transfer_action_lines;
DROP TABLE inventory.transfer_actions;
DROP TABLE inventory.transfer_lines;
DROP TABLE inventory.transfers;
DROP TABLE inventory.counters;
