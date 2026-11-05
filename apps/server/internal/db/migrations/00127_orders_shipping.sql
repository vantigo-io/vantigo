-- +goose Up
-- Orders' shipping (orders design O49–O53, delivery D).
--
-- A carrier account (O51): one carrier's keys under Integrations — the
-- carrier (an adapter of internal/shipping SHIPPING_CARRIERS allows), its
-- customer numbers (Bring: the first for parcels, a second for cargo), the
-- credentials sealed by internal/secrets under orders' own purpose and never
-- answered, the carrier's test mode, an optional sender address that wins
-- over the site's, and a default service. An account is archived, never
-- deleted: a shipment keeps naming the account it was booked on.
CREATE TABLE orders.carrier_accounts (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    carrier               varchar(20)   NOT NULL,
    name                  varchar(100)  NOT NULL,
    customer_numbers      text[]        NOT NULL DEFAULT '{}',
    credentials           bytea         NOT NULL,
    test                  boolean       NOT NULL DEFAULT false,
    sender_name           varchar(200),
    sender_address_line1  varchar(200),
    sender_address_line2  varchar(200),
    sender_postal_code    varchar(20),
    sender_city           varchar(100),
    sender_country        varchar(2),
    sender_phone          varchar(50),
    sender_email          varchar(254),
    default_service       varchar(20),
    status                varchar(10)   NOT NULL DEFAULT 'active',
    verified_at           timestamptz,
    version               integer       NOT NULL DEFAULT 1,
    created_at            timestamptz   NOT NULL,
    updated_at            timestamptz   NOT NULL,
    CONSTRAINT ck_carrier_accounts_status CHECK (status IN ('active', 'archived')),
    CONSTRAINT ck_carrier_accounts_customer_numbers CHECK (cardinality(customer_numbers) BETWEEN 1 AND 5),
    CONSTRAINT ck_carrier_accounts_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX ux_carrier_accounts_name ON orders.carrier_accounts (lower(name)) WHERE status = 'active';

-- The default carrier account per Inventory site (O51); site_id is opaque
-- here as every foreign id is (module-boundaries rule 4).
CREATE TABLE orders.site_carrier_defaults (
    site_id     bigint      PRIMARY KEY,
    account_id  bigint      NOT NULL REFERENCES orders.carrier_accounts (id) ON DELETE CASCADE,
    updated_at  timestamptz NOT NULL
);

-- A shipment of a posted delivery (O50): the account and the service, its
-- value-added services, the recipient as the delivery address and contact
-- were when it was made, and — once booked — the carrier's consignment
-- number, tracking URL, where its label and waybill are fetched from
-- (document_refs, the carrier's opaque references) and booked cost, kept as
-- the business's cost (not
-- invoiced; an estimate unless the carrier says otherwise). idempotency_key
-- is stored before the carrier is called and reused by every retry;
-- book_attempted_at marks a call whose outcome may be unknown. A cancel of a
-- carrier with no cancel call voids the shipment locally (voided): its label
-- must not be used. exception is tracking's flag, set beside the status.
CREATE TABLE orders.shipments (
    id                    bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    delivery_id           bigint        NOT NULL REFERENCES orders.deliveries (id) ON DELETE RESTRICT,
    carrier_account_id    bigint        NOT NULL REFERENCES orders.carrier_accounts (id) ON DELETE RESTRICT,
    service               varchar(20)   NOT NULL,
    vas                   text[]        NOT NULL DEFAULT '{}',
    status                varchar(12)   NOT NULL DEFAULT 'draft',
    exception             boolean       NOT NULL DEFAULT false,
    consignment_number    varchar(64),
    tracking_url          varchar(500),
    document_refs         jsonb         NOT NULL DEFAULT '[]'::jsonb,
    booked_service        varchar(20),
    booked_cost           numeric(14,2),
    booked_cost_currency  varchar(3),
    booked_cost_estimate  boolean       NOT NULL DEFAULT true,
    planned_window_start  timestamptz,
    planned_window_end    timestamptz,
    idempotency_key       uuid          NOT NULL,
    book_attempted_at     timestamptz,
    recipient_name        varchar(255)  NOT NULL DEFAULT '',
    recipient_line1       varchar(255)  NOT NULL DEFAULT '',
    recipient_line2       varchar(255)  NOT NULL DEFAULT '',
    recipient_postal_code varchar(20)   NOT NULL DEFAULT '',
    recipient_city        varchar(100)  NOT NULL DEFAULT '',
    recipient_country     varchar(2)    NOT NULL DEFAULT 'NO',
    recipient_contact     varchar(200)  NOT NULL DEFAULT '',
    recipient_phone       varchar(50)   NOT NULL DEFAULT '',
    recipient_email       varchar(254)  NOT NULL DEFAULT '',
    pickup_point_id       varchar(50),
    booked_at             timestamptz,
    booked_by_user_id     uuid,
    booked_by_display     varchar(200),
    cancelled_at          timestamptz,
    voided                boolean       NOT NULL DEFAULT false,
    last_tracked_at       timestamptz,
    version               integer       NOT NULL DEFAULT 1,
    created_at            timestamptz   NOT NULL,
    updated_at            timestamptz   NOT NULL,
    CONSTRAINT ck_shipments_status CHECK (status IN ('draft', 'booked', 'in_transit', 'delivered', 'cancelled')),
    CONSTRAINT ck_shipments_booked CHECK (status IN ('draft', 'cancelled') OR (consignment_number IS NOT NULL AND booked_at IS NOT NULL)),
    CONSTRAINT ck_shipments_voided CHECK (NOT voided OR status = 'cancelled'),
    CONSTRAINT ck_shipments_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX ux_shipments_idempotency_key ON orders.shipments (idempotency_key);
CREATE INDEX ix_shipments_delivery ON orders.shipments (delivery_id);
CREATE INDEX ix_shipments_tracking ON orders.shipments (carrier_account_id, booked_at) WHERE status IN ('booked', 'in_transit');

-- A shipment's packages: weight in grams, dimensions in millimetres, an
-- optional content text and the carrier's package number once booked.
CREATE TABLE orders.shipment_packages (
    id              bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    shipment_id     bigint        NOT NULL REFERENCES orders.shipments (id) ON DELETE CASCADE,
    position        integer       NOT NULL,
    weight_g        integer       NOT NULL,
    length_mm       integer       NOT NULL DEFAULT 0,
    width_mm        integer       NOT NULL DEFAULT 0,
    height_mm       integer       NOT NULL DEFAULT 0,
    content         varchar(200)  NOT NULL DEFAULT '',
    package_number  varchar(64),
    CONSTRAINT ck_shipment_packages_weight CHECK (weight_g > 0),
    CONSTRAINT ck_shipment_packages_dimensions CHECK (length_mm >= 0 AND width_mm >= 0 AND height_mm >= 0),
    CONSTRAINT ck_shipment_packages_position CHECK (position >= 1)
);
CREATE UNIQUE INDEX ux_shipment_packages_position ON orders.shipment_packages (shipment_id, position);

-- A shipment's stored documents (O52): the carrier's label and waybill as it
-- issued them, under orders' storage scope.
CREATE TABLE orders.shipment_documents (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    shipment_id  bigint       NOT NULL REFERENCES orders.shipments (id) ON DELETE CASCADE,
    kind         varchar(10)  NOT NULL,
    storage_key  varchar(500) NOT NULL,
    format       varchar(5)   NOT NULL DEFAULT 'pdf',
    created_at   timestamptz  NOT NULL,
    CONSTRAINT ck_shipment_documents_kind CHECK (kind IN ('label', 'waybill')),
    CONSTRAINT ck_shipment_documents_format CHECK (format IN ('pdf', 'zpl'))
);
CREATE UNIQUE INDEX ux_shipment_documents_kind ON orders.shipment_documents (shipment_id, kind);

-- A shipment's tracking events as the carrier reported them (O50): its code,
-- text and place, when it happened, and the state the port folded it onto.
-- One row per event, however often it is polled.
CREATE TABLE orders.shipment_events (
    id            bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    shipment_id   bigint        NOT NULL REFERENCES orders.shipments (id) ON DELETE CASCADE,
    code          varchar(64)   NOT NULL,
    text          varchar(500)  NOT NULL DEFAULT '',
    place         varchar(200)  NOT NULL DEFAULT '',
    occurred_at   timestamptz   NOT NULL,
    folded_state  varchar(20)   NOT NULL
);
CREATE UNIQUE INDEX ux_shipment_events ON orders.shipment_events (shipment_id, occurred_at, code, md5(text));

-- The settings (O52, O53): the freight product a shipping line takes its
-- name, price and VAT from, the markup on a carrier's estimate, and whether
-- booking queues the "your order has shipped" e-mail.
ALTER TABLE orders.settings ADD COLUMN freight_variant_id integer;
ALTER TABLE orders.settings ADD COLUMN freight_markup_percent numeric(5,2) NOT NULL DEFAULT 0;
ALTER TABLE orders.settings ADD COLUMN shipping_notice_on_book boolean NOT NULL DEFAULT true;
ALTER TABLE orders.settings ADD CONSTRAINT ck_settings_freight_markup CHECK (freight_markup_percent BETWEEN 0 AND 500);

-- The shipping notice is a mailing of a shipment (O52), of a kind of its own.
ALTER TABLE orders.mailings ADD COLUMN kind varchar(20) NOT NULL DEFAULT 'document';
ALTER TABLE orders.mailings ADD CONSTRAINT ck_mailings_kind CHECK (kind IN ('document', 'shipping_notice'));
ALTER TABLE orders.mailings DROP CONSTRAINT ck_mailings_owner_kind;
ALTER TABLE orders.mailings ADD CONSTRAINT ck_mailings_owner_kind CHECK (owner_kind IN ('quote', 'order', 'shipment'));

-- +goose Down
DELETE FROM orders.mailings WHERE owner_kind = 'shipment';
ALTER TABLE orders.mailings DROP CONSTRAINT ck_mailings_owner_kind;
ALTER TABLE orders.mailings ADD CONSTRAINT ck_mailings_owner_kind CHECK (owner_kind IN ('quote', 'order'));
ALTER TABLE orders.mailings DROP CONSTRAINT ck_mailings_kind;
ALTER TABLE orders.mailings DROP COLUMN kind;
ALTER TABLE orders.settings DROP CONSTRAINT ck_settings_freight_markup;
ALTER TABLE orders.settings DROP COLUMN shipping_notice_on_book;
ALTER TABLE orders.settings DROP COLUMN freight_markup_percent;
ALTER TABLE orders.settings DROP COLUMN freight_variant_id;
DROP TABLE orders.shipment_events;
DROP TABLE orders.shipment_documents;
DROP TABLE orders.shipment_packages;
DROP TABLE orders.shipments;
DROP TABLE orders.site_carrier_defaults;
DROP TABLE orders.carrier_accounts;
