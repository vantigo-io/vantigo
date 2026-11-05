-- +goose Up
-- Orders, the quotes and sales orders the business makes for its customers
-- (orders design §9, delivery A): the one settings row and the number
-- counters; the quotes and the orders, each with the shared header of O13 and
-- the origin of O27; the lines every document owns (O15), templates' too; an
-- order's change log (O26); the stored PDFs (O24); the mailings (O25); and the
-- record of the customers whose personal data the erase took out (O29, O54).
-- Subscriptions (S), warnings (A2), allocations (B), deliveries (C) and
-- shipping (D) add their tables in migrations of their own.
--
-- Every foreign identifier is opaque: a customer, its delivery address and its
-- contact are Customers', a variant and a packaging Products', a VAT code
-- Invoices', a project Projects' and a user identity's — each read through its
-- contract (docs/src/content/docs/en/contributing/module-boundaries.md rule 4),
-- so no key crosses a schema. The header snapshots the customer and the lines
-- snapshot the variant and the VAT code, so a document reads the same after
-- either is gone. The enumerations are CHECKed; the open sets — a line's kind,
-- an origin's channel, a change's actions — are not.
CREATE SCHEMA orders;

-- The settings (§9), one row, created by the first write. A series start can
-- only rise above the highest number used (O12); the module checks that.
CREATE TABLE orders.settings (
    id                         smallint      PRIMARY KEY,
    quote_series_start         bigint        NOT NULL DEFAULT 1,
    order_series_start         bigint        NOT NULL DEFAULT 1,
    quote_validity_days        integer       NOT NULL DEFAULT 30,
    default_payment_terms_days integer       NOT NULL DEFAULT 14,
    send_channel               uuid,
    confirmation_on_confirm    boolean       NOT NULL DEFAULT false,
    combine_by_default         boolean       NOT NULL DEFAULT false,
    mail_texts                 jsonb         NOT NULL DEFAULT '{}'::jsonb,
    revision                   integer       NOT NULL DEFAULT 1,
    updated_at                 timestamptz   NOT NULL,
    CONSTRAINT ck_settings_single_row CHECK (id = 1),
    CONSTRAINT ck_settings_quote_series_start CHECK (quote_series_start >= 1),
    CONSTRAINT ck_settings_order_series_start CHECK (order_series_start >= 1),
    CONSTRAINT ck_settings_quote_validity CHECK (quote_validity_days BETWEEN 1 AND 365),
    CONSTRAINT ck_settings_payment_terms CHECK (default_payment_terms_days BETWEEN 0 AND 365)
);

-- The display-number series (O12), one row per series: quote and order in A,
-- subscription (S) and delivery (C) later. A number is allocated by upsert
-- under the row's lock when the document is created; next_value is the next
-- number the series hands out.
CREATE TABLE orders.counters (
    counter_name varchar(20) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

-- A quote (O13, O20, O27). number is the series' display number, the same
-- across revisions. The customer snapshot follows the customer while the quote
-- is a draft and freezes when it is sent (O14).
CREATE TABLE orders.quotes (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number             bigint        NOT NULL,
    status             varchar(10)   NOT NULL DEFAULT 'draft',
    revision           integer       NOT NULL DEFAULT 1,
    valid_until        date,
    sent_at            timestamptz,
    accepted_at        timestamptz,
    accepted_into_kind varchar(12),
    accepted_into_id   bigint,
    declined_at        timestamptz,
    decline_reason     varchar(500),
    cancelled_at       timestamptz,
    template_id        bigint,
    -- The customer and its snapshot (O13).
    customer_id                  integer       NOT NULL,
    customer_number              bigint,
    customer_name                varchar(255)  NOT NULL DEFAULT '',
    customer_type                varchar(20),
    customer_organisation_number varchar(9),
    customer_foreign_id          varchar(60),
    customer_country             char(2),
    invoice_address_line1        varchar(255),
    invoice_address_line2        varchar(255),
    invoice_postal_code          varchar(20),
    invoice_city                 varchar(100),
    invoice_country              char(2),
    delivery_address_id          integer,
    delivery_name                varchar(255),
    delivery_address_line1       varchar(255),
    delivery_address_line2       varchar(255),
    delivery_postal_code         varchar(20),
    delivery_city                varchar(100),
    delivery_country             char(2),
    contact_id                   integer,
    contact_name                 varchar(255),
    contact_email                varchar(254),
    contact_phone                varchar(50),
    -- The customer's invoice e-mail, a send's default recipient when the
    -- document names no contact (O25).
    customer_invoice_email       varchar(254),
    your_reference               varchar(100)  NOT NULL DEFAULT '',
    -- Our side (O13).
    salesperson_id      uuid,
    salesperson_display varchar(200),
    project_id          integer,
    -- Money, text and dates (O13, O19, O22).
    currency            char(3)       NOT NULL,
    prices_include_vat  boolean       NOT NULL DEFAULT false,
    payment_terms_days  integer       NOT NULL,
    discount_visible    boolean       NOT NULL DEFAULT true,
    language            char(2)       NOT NULL DEFAULT 'nb',
    intro_text          varchar(4000) NOT NULL DEFAULT '',
    closing_text        varchar(4000) NOT NULL DEFAULT '',
    internal_note       varchar(4000) NOT NULL DEFAULT '',
    delivery_date       date,
    -- The totals of the lines that are not optional, kept for lists (O21).
    net_total           numeric(14,2) NOT NULL DEFAULT 0,
    vat_total           numeric(14,2) NOT NULL DEFAULT 0,
    gross_total         numeric(14,2) NOT NULL DEFAULT 0,
    -- The origin (O27).
    channel             varchar(20)   NOT NULL DEFAULT 'manual',
    source_module       varchar(30),
    source_kind         varchar(30),
    source_id           varchar(100),
    external_id         varchar(100),
    external_number     varchar(50),
    client_request_id   uuid,
    payment_reference   varchar(100),
    prepaid_amount      numeric(14,2),
    created_by_user_id  uuid,
    created_by_display  varchar(200),
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL,
    -- When an erase took the person out of the snapshot (O29, O54).
    anonymised_at       timestamptz,
    CONSTRAINT ck_quotes_status CHECK (status IN ('draft', 'sent', 'accepted', 'declined', 'expired', 'cancelled')),
    CONSTRAINT ck_quotes_revision CHECK (revision >= 1),
    CONSTRAINT ck_quotes_accepted_into CHECK (accepted_into_kind IS NULL OR accepted_into_kind IN ('order', 'subscription')),
    CONSTRAINT ck_quotes_customer_type CHECK (customer_type IS NULL OR customer_type IN ('business', 'person')),
    CONSTRAINT ck_quotes_language CHECK (language IN ('nb', 'en')),
    CONSTRAINT ck_quotes_payment_terms CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE UNIQUE INDEX ux_quotes_number ON orders.quotes (number);
CREATE INDEX ix_quotes_status_valid_until ON orders.quotes (status, valid_until);
CREATE INDEX ix_quotes_customer ON orders.quotes (customer_id);
CREATE INDEX ix_quotes_salesperson ON orders.quotes (salesperson_id);
CREATE UNIQUE INDEX ux_quotes_external ON orders.quotes (channel, external_id) WHERE external_id IS NOT NULL;
CREATE UNIQUE INDEX ux_quotes_client_request ON orders.quotes (client_request_id);

-- A sales order (O13, O18, O27). version is the optimistic-concurrency token of
-- O26 and the version a revised confirmation prints; delivery_status and
-- invoice_status are derived from the lines' buckets and stored for lists.
CREATE TABLE orders.orders (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    number             bigint        NOT NULL,
    status             varchar(10)   NOT NULL DEFAULT 'draft',
    on_hold            boolean       NOT NULL DEFAULT false,
    hold_reason        varchar(200),
    locked             boolean       NOT NULL DEFAULT false,
    version            integer       NOT NULL DEFAULT 1,
    delivery_status    varchar(10)   NOT NULL DEFAULT 'none',
    invoice_status     varchar(10)   NOT NULL DEFAULT 'none',
    confirmed_at       timestamptz,
    confirmed_by_user_id uuid,
    confirmed_by_display varchar(200),
    cancelled_at       timestamptz,
    -- The customer and its snapshot (O13).
    customer_id                  integer       NOT NULL,
    customer_number              bigint,
    customer_name                varchar(255)  NOT NULL DEFAULT '',
    customer_type                varchar(20),
    customer_organisation_number varchar(9),
    customer_foreign_id          varchar(60),
    customer_country             char(2),
    invoice_address_line1        varchar(255),
    invoice_address_line2        varchar(255),
    invoice_postal_code          varchar(20),
    invoice_city                 varchar(100),
    invoice_country              char(2),
    delivery_address_id          integer,
    delivery_name                varchar(255),
    delivery_address_line1       varchar(255),
    delivery_address_line2       varchar(255),
    delivery_postal_code         varchar(20),
    delivery_city                varchar(100),
    delivery_country             char(2),
    contact_id                   integer,
    contact_name                 varchar(255),
    contact_email                varchar(254),
    contact_phone                varchar(50),
    -- The customer's invoice e-mail, a send's default recipient when the
    -- document names no contact (O25).
    customer_invoice_email       varchar(254),
    your_reference               varchar(100)  NOT NULL DEFAULT '',
    -- Our side (O13).
    salesperson_id      uuid,
    salesperson_display varchar(200),
    project_id          integer,
    -- Money, text and dates (O13, O19, O22).
    currency            char(3)       NOT NULL,
    prices_include_vat  boolean       NOT NULL DEFAULT false,
    payment_terms_days  integer       NOT NULL,
    discount_visible    boolean       NOT NULL DEFAULT true,
    language            char(2)       NOT NULL DEFAULT 'nb',
    intro_text          varchar(4000) NOT NULL DEFAULT '',
    closing_text        varchar(4000) NOT NULL DEFAULT '',
    internal_note       varchar(4000) NOT NULL DEFAULT '',
    delivery_date       date,
    net_total           numeric(14,2) NOT NULL DEFAULT 0,
    vat_total           numeric(14,2) NOT NULL DEFAULT 0,
    gross_total         numeric(14,2) NOT NULL DEFAULT 0,
    -- The origin (O27).
    channel             varchar(20)   NOT NULL DEFAULT 'manual',
    source_module       varchar(30),
    source_kind         varchar(30),
    source_id           varchar(100),
    external_id         varchar(100),
    external_number     varchar(50),
    client_request_id   uuid,
    payment_reference   varchar(100),
    prepaid_amount      numeric(14,2),
    created_by_user_id  uuid,
    created_by_display  varchar(200),
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL,
    -- When an erase, or the retention sweep after it, took the person out of
    -- the snapshot (O29, O54).
    anonymised_at       timestamptz,
    CONSTRAINT ck_orders_status CHECK (status IN ('draft', 'confirmed', 'cancelled')),
    CONSTRAINT ck_orders_version CHECK (version >= 1),
    CONSTRAINT ck_orders_delivery_status CHECK (delivery_status IN ('none', 'partial', 'delivered')),
    CONSTRAINT ck_orders_invoice_status CHECK (invoice_status IN ('none', 'partial', 'invoiced')),
    CONSTRAINT ck_orders_hold_reason CHECK (on_hold OR hold_reason IS NULL),
    CONSTRAINT ck_orders_customer_type CHECK (customer_type IS NULL OR customer_type IN ('business', 'person')),
    CONSTRAINT ck_orders_language CHECK (language IN ('nb', 'en')),
    CONSTRAINT ck_orders_payment_terms CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE UNIQUE INDEX ux_orders_number ON orders.orders (number);
CREATE INDEX ix_orders_statuses ON orders.orders (status, delivery_status, invoice_status);
CREATE INDEX ix_orders_customer ON orders.orders (customer_id);
CREATE INDEX ix_orders_salesperson ON orders.orders (salesperson_id);
CREATE UNIQUE INDEX ux_orders_external ON orders.orders (channel, external_id) WHERE external_id IS NOT NULL;
CREATE UNIQUE INDEX ux_orders_client_request ON orders.orders (client_request_id);

-- A quote template (O23), edited under orders:settings. Its lines are
-- orders.lines with owner_kind template.
CREATE TABLE orders.templates (
    id               bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name             varchar(200)  NOT NULL,
    intro_text       varchar(4000) NOT NULL DEFAULT '',
    closing_text     varchar(4000) NOT NULL DEFAULT '',
    validity_days    integer,
    discount_visible boolean       NOT NULL DEFAULT true,
    archived         boolean       NOT NULL DEFAULT false,
    revision         integer       NOT NULL DEFAULT 1,
    created_at       timestamptz   NOT NULL,
    updated_at       timestamptz   NOT NULL,
    CONSTRAINT ck_templates_validity CHECK (validity_days IS NULL OR validity_days BETWEEN 1 AND 365)
);
CREATE UNIQUE INDEX ux_templates_name ON orders.templates (lower(name));

-- A line (O15), owned by a quote, an order or a template (S adds the
-- subscription). owner_id names the owner's row in its table; no foreign key
-- can, since it names one of three, so the module deletes a document's lines
-- with the document. kind is an open set (product, section and note in A).
-- A product line with no variant is a free-text line. The amounts are
-- Invoices' own (O17, internal/docmath); VAT is per document, never per line.
-- The buckets are an order line's; on a quote's or a template's they stay 0.
CREATE TABLE orders.lines (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_kind         varchar(12)   NOT NULL,
    owner_id           bigint        NOT NULL,
    position           integer       NOT NULL,
    kind               varchar(12)   NOT NULL,
    parent_line_id     bigint        REFERENCES orders.lines (id) ON DELETE RESTRICT,
    -- The catalog item and its snapshot.
    variant_id         integer,
    sku                varchar(64),
    name               varchar(255),
    unit               varchar(20)   NOT NULL DEFAULT '',
    description        varchar(2000) NOT NULL DEFAULT '',
    quantity           numeric(12,3),
    unit_price         numeric(18,8),
    discount_percent   numeric(5,2)  NOT NULL DEFAULT 0,
    packaging_id       integer,
    packaging_quantity numeric(12,3),
    -- Pricing (O16, O17).
    price_source       varchar(10),
    vat_code_id        integer,
    vat_code           varchar(20),
    vat_rate_percent   numeric(5,2),
    vat_category       varchar(2),
    line_gross         numeric(14,2) NOT NULL DEFAULT 0,
    line_allowance     numeric(14,2) NOT NULL DEFAULT 0,
    line_net           numeric(14,2) NOT NULL DEFAULT 0,
    -- Channel, invoicing and delivery.
    external_line_id   varchar(50),
    invoice_policy     varchar(10)   NOT NULL DEFAULT 'order',
    optional           boolean       NOT NULL DEFAULT false,
    qty_cancelled      numeric(12,3) NOT NULL DEFAULT 0,
    qty_delivered      numeric(12,3) NOT NULL DEFAULT 0,
    qty_invoiced       numeric(12,3) NOT NULL DEFAULT 0,
    delivery_method    varchar(10)   NOT NULL DEFAULT 'manual',
    CONSTRAINT ck_lines_owner_kind CHECK (owner_kind IN ('quote', 'order', 'template')),
    CONSTRAINT ck_lines_position CHECK (position >= 1),
    CONSTRAINT ck_lines_quantity CHECK (quantity IS NULL OR quantity > 0),
    CONSTRAINT ck_lines_unit_price CHECK (unit_price IS NULL OR unit_price >= 0),
    CONSTRAINT ck_lines_discount CHECK (discount_percent BETWEEN 0 AND 100),
    CONSTRAINT ck_lines_packaging_quantity CHECK (packaging_quantity IS NULL OR packaging_quantity > 0),
    CONSTRAINT ck_lines_price_source CHECK (price_source IS NULL OR price_source IN ('catalog', 'manual', 'channel', 'carrier')),
    CONSTRAINT ck_lines_invoice_policy CHECK (invoice_policy IN ('order', 'delivery')),
    CONSTRAINT ck_lines_optional CHECK (NOT optional OR owner_kind IN ('quote', 'template')),
    CONSTRAINT ck_lines_buckets CHECK (qty_cancelled >= 0 AND qty_delivered >= 0 AND qty_invoiced >= 0),
    CONSTRAINT ck_lines_delivery_method CHECK (delivery_method IN ('manual', 'delivery'))
);
CREATE UNIQUE INDEX ux_lines_owner_position ON orders.lines (owner_kind, owner_id, position);
CREATE INDEX ix_lines_variant ON orders.lines (variant_id) WHERE variant_id IS NOT NULL;
CREATE INDEX ix_lines_parent ON orders.lines (parent_line_id) WHERE parent_line_id IS NOT NULL;

-- An order's change log (O26): one row per save of a confirmed order and per
-- hold, release, lock, unlock, cancel remaining and delivery marking, with the
-- version it produced, the actor snapshotted (O29) and a typed list of actions.
CREATE TABLE orders.order_changes (
    id            bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_kind    varchar(12) NOT NULL DEFAULT 'order',
    owner_id      bigint      NOT NULL,
    version       integer     NOT NULL,
    actor_kind    varchar(20) NOT NULL,
    actor_user_id uuid,
    actor_display varchar(200),
    at            timestamptz NOT NULL,
    actions       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    CONSTRAINT ck_order_changes_owner_kind CHECK (owner_kind IN ('order'))
);
CREATE INDEX ix_order_changes_owner ON orders.order_changes (owner_kind, owner_id, version);

-- A stored PDF (O24): each sent quote revision and each sent or confirmed
-- order version, once, through the storage port. Immutable; retention is O54.
CREATE TABLE orders.documents (
    id          bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_kind  varchar(12)  NOT NULL,
    owner_id    bigint       NOT NULL,
    kind        varchar(16)  NOT NULL,
    version     integer      NOT NULL,
    storage_key varchar(300) NOT NULL,
    sha256      char(64)     NOT NULL,
    size_bytes  bigint       NOT NULL,
    created_at  timestamptz  NOT NULL,
    CONSTRAINT ck_documents_owner_kind CHECK (owner_kind IN ('quote', 'order')),
    CONSTRAINT ck_documents_kind CHECK (kind IN ('quote', 'confirmation'))
);
CREATE UNIQUE INDEX ux_documents_owner_version ON orders.documents (owner_kind, owner_id, kind, version);
CREATE UNIQUE INDEX ux_documents_storage_key ON orders.documents (storage_key);

-- A mailing (O25), as invoices.deliveries: a send from the dialog is written
-- after it is made; an automatic send is queued by the handler on its own
-- transaction, once per (document, version, trigger event), and sent by the
-- orders.mail worker with backoff.
CREATE TABLE orders.mailings (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_kind          varchar(12)  NOT NULL,
    owner_id            bigint       NOT NULL,
    version             integer      NOT NULL,
    document_id         bigint       REFERENCES orders.documents (id) ON DELETE RESTRICT,
    recipient           varchar(254) NOT NULL,
    cc                  varchar(1000) NOT NULL DEFAULT '',
    subject             varchar(300) NOT NULL DEFAULT '',
    channel_id          uuid,
    status              varchar(10)  NOT NULL,
    provider_message_id varchar(200),
    trigger_event_id    uuid,
    attempts            integer      NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    last_error          varchar(500),
    sent_at             timestamptz,
    sent_by_user_id     uuid,
    sent_by_display     varchar(200),
    created_at          timestamptz  NOT NULL,
    CONSTRAINT ck_mailings_owner_kind CHECK (owner_kind IN ('quote', 'order')),
    CONSTRAINT ck_mailings_status CHECK (status IN ('queued', 'sent', 'failed'))
);
CREATE INDEX ix_mailings_owner ON orders.mailings (owner_kind, owner_id);
CREATE INDEX ix_mailings_queued ON orders.mailings (next_attempt_at) WHERE status = 'queued';
CREATE UNIQUE INDEX ux_mailings_trigger ON orders.mailings (owner_kind, owner_id, version, trigger_event_id) WHERE trigger_event_id IS NOT NULL;

-- The customers whose personal data the erase took out (O29, O54): the erase
-- anonymises what it may at once and records the customer here; the
-- orders.retention worker anonymises the kept documents as their retention
-- runs out, and marks the row done when nothing is left to wait for.
CREATE TABLE orders.erasures (
    customer_id  integer     PRIMARY KEY,
    requested_at timestamptz NOT NULL,
    completed_at timestamptz
);
CREATE INDEX ix_erasures_open ON orders.erasures (requested_at) WHERE completed_at IS NULL;

-- +goose Down
DROP SCHEMA orders CASCADE;
