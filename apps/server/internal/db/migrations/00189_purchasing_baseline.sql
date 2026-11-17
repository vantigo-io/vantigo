-- +goose Up
-- Purchasing, the buy side (purchasing design §9, delivery A): the one
-- settings row and the number counters; the supplier items (P11); the
-- purchase orders, their lines and their change log (P15–P20); the stored
-- PDFs and the mailings (P21); and the deletion ledger the supplier erase
-- writes the stored PDFs to (A8). Receipts (B) and invoice matches (D) add
-- their tables in migrations of their own.
--
-- Every foreign identifier is opaque: a supplier is Accounting's, a variant
-- and a packaging Products', a site Inventory's, a user identity's — each read
-- through its contract (docs/src/content/docs/en/contributing/module-boundaries.md
-- rule 4), so no key crosses a schema. An order snapshots the supplier and the
-- delivery address when it is sent, and a line the variant, so a document
-- reads the same after either is gone.
CREATE SCHEMA purchasing;

-- The settings (P9), one row, created by the first write. The series start
-- can only rise above the highest number used (P20); the module checks that.
-- delivery_addresses is a JSON array of {siteId, name, line1, line2,
-- postalCode, city, country}: a site's delivery address when it differs from
-- the company's.
CREATE TABLE purchasing.settings (
    id                      smallint      PRIMARY KEY,
    order_series_start      bigint        NOT NULL DEFAULT 1,
    default_site_id         bigint,
    send_channel            uuid,
    order_footer            varchar(4000) NOT NULL DEFAULT '',
    delivery_addresses      jsonb         NOT NULL DEFAULT '[]'::jsonb,
    price_tolerance_percent numeric(5,2)  NOT NULL DEFAULT 2,
    revision                integer       NOT NULL DEFAULT 1,
    updated_at              timestamptz   NOT NULL,
    CONSTRAINT ck_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_settings_order_series_start CHECK (order_series_start >= 1),
    CONSTRAINT ck_settings_price_tolerance CHECK (price_tolerance_percent BETWEEN 0 AND 100)
);

-- The number series (P20): the order series in A, the receipt series in B.
-- A number is allocated by upsert under the row's lock when the order is
-- sent; next_value is the next number the series hands out.
CREATE TABLE purchasing.counters (
    counter_name varchar(20) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

-- A supplier item (P11): what one supplier sells of one variant, at what
-- price per the purchase packaging (none: the variant's unit), in the
-- currency the price was set in. price_set_by says what set the price last —
-- a person, or a posted invoice (P39, delivery D). The first item of a
-- variant is its preferred one; at most one is.
CREATE TABLE purchasing.supplier_items (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_id          bigint        NOT NULL,
    variant_id           integer       NOT NULL,
    supplier_item_number varchar(60),
    supplier_item_name   varchar(255),
    price                numeric(18,4),
    currency             char(3)       NOT NULL,
    price_set_on         date,
    price_set_by         varchar(10),
    price_set_by_user_id uuid,
    packaging_id         integer,
    lead_time_days       integer,
    minimum_quantity     numeric(12,3),
    order_multiple       numeric(12,3),
    preferred            boolean       NOT NULL DEFAULT false,
    revision             integer       NOT NULL DEFAULT 1,
    created_at           timestamptz   NOT NULL,
    updated_at           timestamptz   NOT NULL,
    CONSTRAINT ck_supplier_items_price CHECK (price IS NULL OR price >= 0),
    CONSTRAINT ck_supplier_items_price_set_by CHECK (price_set_by IS NULL OR price_set_by IN ('person', 'invoice', 'import')),
    CONSTRAINT ck_supplier_items_lead_time CHECK (lead_time_days IS NULL OR lead_time_days BETWEEN 0 AND 3650),
    CONSTRAINT ck_supplier_items_minimum CHECK (minimum_quantity IS NULL OR minimum_quantity > 0),
    CONSTRAINT ck_supplier_items_multiple CHECK (order_multiple IS NULL OR order_multiple > 0)
);
CREATE UNIQUE INDEX ux_supplier_items_supplier_variant ON purchasing.supplier_items (supplier_id, variant_id);
CREATE UNIQUE INDEX ux_supplier_items_preferred ON purchasing.supplier_items (variant_id) WHERE preferred;
CREATE INDEX ix_supplier_items_variant ON purchasing.supplier_items (variant_id);

-- A purchase order (P15, P18). number is allocated when the order is sent
-- and is NULL while it is a draft. version counts the PDFs sent: 1 at the
-- first send, one more per Send again (P18). The supplier snapshot follows
-- the supplier while the order is a draft and freezes when it is sent.
-- send_client_request_id and send_request_hash make Send idempotent (A11).
CREATE TABLE purchasing.orders (
    id                          bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number                      bigint,
    status                      varchar(20)   NOT NULL DEFAULT 'draft',
    version                     integer       NOT NULL DEFAULT 0,
    -- The supplier and its snapshot (P15).
    supplier_id                 bigint        NOT NULL,
    supplier_number             integer,
    supplier_name               varchar(255)  NOT NULL DEFAULT '',
    supplier_organisation_number varchar(20),
    supplier_address_line       varchar(255),
    supplier_postal_code        varchar(20),
    supplier_city               varchar(100),
    supplier_country            char(2),
    supplier_email              varchar(254),
    supplier_contact            varchar(200)  NOT NULL DEFAULT '',
    -- Where it goes (P15, P9).
    site_id                     bigint        NOT NULL,
    delivery_name               varchar(255),
    delivery_address_line1      varchar(255),
    delivery_address_line2      varchar(255),
    delivery_postal_code        varchar(20),
    delivery_city               varchar(100),
    delivery_country            char(2),
    -- Money, text and dates (P15, P17).
    currency                    char(3)       NOT NULL,
    language                    char(2)       NOT NULL DEFAULT 'nb',
    order_date                  date          NOT NULL,
    expected_on                 date,
    buyer_id                    uuid,
    buyer_display               varchar(200),
    note                        varchar(4000) NOT NULL DEFAULT '',
    internal_note               varchar(4000) NOT NULL DEFAULT '',
    net_total                   numeric(14,2) NOT NULL DEFAULT 0,
    -- The supplier's confirmation (P19): a fact, not a state.
    supplier_reference          varchar(100),
    confirmed_on                date,
    confirmed_at                timestamptz,
    confirmed_by_user_id        uuid,
    confirmed_by_display        varchar(200),
    -- The lifecycle (P18).
    sent_at                     timestamptz,
    sent_by_user_id             uuid,
    sent_by_display             varchar(200),
    send_method                 varchar(10),
    send_client_request_id      uuid,
    send_request_hash           char(64),
    closed_at                   timestamptz,
    cancelled_at                timestamptz,
    copied_from_id              bigint,
    created_by_user_id          uuid,
    created_by_display          varchar(200),
    created_at                  timestamptz   NOT NULL,
    updated_at                  timestamptz   NOT NULL,
    -- When the supplier's erase blanked the snapshot (P7, A8).
    anonymised_at               timestamptz,
    CONSTRAINT ck_orders_status CHECK (status IN ('draft', 'sent', 'partially_received', 'received', 'closed', 'cancelled')),
    CONSTRAINT ck_orders_number CHECK ((status = 'draft') = (number IS NULL)),
    CONSTRAINT ck_orders_version CHECK (version >= 0),
    CONSTRAINT ck_orders_language CHECK (language IN ('nb', 'en')),
    CONSTRAINT ck_orders_send_method CHECK (send_method IS NULL OR send_method IN ('mail', 'manual'))
);
CREATE UNIQUE INDEX ux_orders_number ON purchasing.orders (number) WHERE number IS NOT NULL;
CREATE UNIQUE INDEX ux_orders_send_client_request ON purchasing.orders (send_client_request_id) WHERE send_client_request_id IS NOT NULL;
CREATE INDEX ix_orders_status ON purchasing.orders (status, expected_on);
CREATE INDEX ix_orders_supplier ON purchasing.orders (supplier_id);
CREATE INDEX ix_orders_buyer ON purchasing.orders (buyer_id);

-- A line (P16): a variant line or a text line. quantity, received and
-- invoiced are in the variant's own unit (a text line's in its own unit);
-- order_quantity is what was entered, in the purchase packaging (packaging_id,
-- NULL for the unit), and unit_price is per that packaging. received and
-- invoiced are written only by a receipt, its undo and the match's handlers
-- (§9). closed_at ends a line's remainder (A16).
CREATE TABLE purchasing.order_lines (
    id                   bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id             bigint        NOT NULL REFERENCES purchasing.orders (id) ON DELETE CASCADE,
    position             integer       NOT NULL,
    kind                 varchar(10)   NOT NULL,
    -- The variant and its snapshot.
    variant_id           integer,
    sku                  varchar(64),
    name                 varchar(255)  NOT NULL DEFAULT '',
    product_type         varchar(10),
    supplier_item_number varchar(60),
    description          varchar(2000) NOT NULL DEFAULT '',
    unit                 varchar(20)   NOT NULL DEFAULT '',
    packaging_id         integer,
    packaging_name       varchar(100),
    packaging_factor     numeric(12,3),
    order_quantity       numeric(12,3) NOT NULL,
    quantity             numeric(12,3) NOT NULL,
    unit_price           numeric(18,4) NOT NULL DEFAULT 0,
    discount_percent     numeric(5,2)  NOT NULL DEFAULT 0,
    line_net             numeric(14,2) NOT NULL DEFAULT 0,
    expected_on          date,
    confirmed_on         date,
    received             numeric(12,3) NOT NULL DEFAULT 0,
    invoiced             numeric(12,3) NOT NULL DEFAULT 0,
    closed_at            timestamptz,
    delivered_at         timestamptz,
    CONSTRAINT ck_order_lines_kind CHECK (kind IN ('variant', 'text')),
    CONSTRAINT ck_order_lines_variant CHECK ((kind = 'variant') = (variant_id IS NOT NULL)),
    CONSTRAINT ck_order_lines_product_type CHECK (product_type IS NULL OR product_type IN ('Goods', 'Service')),
    CONSTRAINT ck_order_lines_position CHECK (position >= 1),
    CONSTRAINT ck_order_lines_quantity CHECK (order_quantity > 0 AND quantity > 0),
    CONSTRAINT ck_order_lines_packaging_factor CHECK (packaging_factor IS NULL OR packaging_factor > 0),
    CONSTRAINT ck_order_lines_unit_price CHECK (unit_price >= 0),
    CONSTRAINT ck_order_lines_discount CHECK (discount_percent BETWEEN 0 AND 100),
    CONSTRAINT ck_order_lines_progress CHECK (received >= 0 AND invoiced >= 0)
);
CREATE UNIQUE INDEX ux_order_lines_position ON purchasing.order_lines (order_id, position);
CREATE INDEX ix_order_lines_variant ON purchasing.order_lines (variant_id) WHERE variant_id IS NOT NULL;

-- An order's change log (P18, A16): one row per change of a sent order and
-- per send, send again, mark as sent, confirmation, close line, close,
-- reopen and cancel, with the actor snapshotted and a typed list of actions.
CREATE TABLE purchasing.order_changes (
    id            bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id      bigint      NOT NULL REFERENCES purchasing.orders (id) ON DELETE CASCADE,
    actor_user_id uuid,
    actor_display varchar(200),
    at            timestamptz NOT NULL,
    actions       jsonb       NOT NULL DEFAULT '[]'::jsonb
);
CREATE INDEX ix_order_changes_order ON purchasing.order_changes (order_id, id);

-- A stored PDF (P21, A19): each version an order is sent at, and its
-- cancellation, once, through the storage port; stored after the commit,
-- keyed by its hash. erased_at is set when the supplier's erase wrote the
-- object to the deletion ledger (A8): the row stays, the object goes.
CREATE TABLE purchasing.documents (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id    bigint       NOT NULL REFERENCES purchasing.orders (id) ON DELETE RESTRICT,
    kind        varchar(16)  NOT NULL,
    version     integer      NOT NULL,
    storage_key varchar(300) NOT NULL,
    sha256      char(64)     NOT NULL,
    size_bytes  bigint       NOT NULL,
    created_at  timestamptz  NOT NULL,
    erased_at   timestamptz,
    CONSTRAINT ck_documents_kind CHECK (kind IN ('order', 'cancellation'))
);
CREATE UNIQUE INDEX ux_documents_order_version ON purchasing.documents (order_id, kind, version);
CREATE UNIQUE INDEX ux_documents_storage_key ON purchasing.documents (storage_key);

-- A mailing (P21, A19), as Orders' mailings: a person's send is mailed
-- synchronously after its commit and written with what happened; a failed
-- one is queued for the purchasing.mail worker's retries with backoff.
CREATE TABLE purchasing.mailings (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    order_id            bigint        NOT NULL REFERENCES purchasing.orders (id) ON DELETE RESTRICT,
    kind                varchar(16)   NOT NULL,
    version             integer       NOT NULL,
    document_id         bigint        REFERENCES purchasing.documents (id) ON DELETE RESTRICT,
    recipient           varchar(254)  NOT NULL,
    cc                  varchar(1000) NOT NULL DEFAULT '',
    subject             varchar(300)  NOT NULL DEFAULT '',
    message             varchar(4000) NOT NULL DEFAULT '',
    channel_id          uuid,
    status              varchar(10)   NOT NULL,
    provider_message_id varchar(200),
    attempts            integer       NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    last_error          varchar(500),
    sent_at             timestamptz,
    sent_by_user_id     uuid,
    sent_by_display     varchar(200),
    created_at          timestamptz   NOT NULL,
    CONSTRAINT ck_mailings_kind CHECK (kind IN ('order', 'cancellation')),
    CONSTRAINT ck_mailings_status CHECK (status IN ('queued', 'sent', 'failed'))
);
CREATE INDEX ix_mailings_order ON purchasing.mailings (order_id);
CREATE INDEX ix_mailings_queued ON purchasing.mailings (next_attempt_at) WHERE status = 'queued';

-- The deletion ledger (A8): a stored object the supplier's erase took out,
-- written inside Accounting's anonymisation transaction and deleted from the
-- object store by the purchasing.deletions worker, never inside it.
CREATE TABLE purchasing.deletions (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    storage_key  varchar(300) NOT NULL,
    requested_at timestamptz  NOT NULL,
    attempts     integer      NOT NULL DEFAULT 0,
    last_error   varchar(500),
    completed_at timestamptz
);
CREATE UNIQUE INDEX ux_deletions_storage_key ON purchasing.deletions (storage_key);
CREATE INDEX ix_deletions_open ON purchasing.deletions (requested_at) WHERE completed_at IS NULL;

-- +goose Down
DROP SCHEMA purchasing CASCADE;
