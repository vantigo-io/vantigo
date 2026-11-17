-- +goose Up
-- Purchasing's invoice match (purchasing design §6, delivery D): which order
-- lines a supplier invoice of Accounting's bills (P36–P41, A9, A14, A15). A
-- supplier invoice and its lines are Accounting's, opaque here and read
-- through contracts.SupplierInvoices (module-boundaries rule 4); the match
-- is Purchasing's own.

-- A match (P37, A9): one line of a supplier invoice billing one order line,
-- keyed by the invoice's id and the line's position — Accounting writes a
-- draft's lines anew on every save, and Reverse and copy copies them in
-- order, so the position is what stays (A70). The quantity is in the order
-- line's own unit, always more than 0: a credit note's lowers the line's
-- invoiced quantity, an invoice's raises it. The invoice's number, date and
-- currency and the line's net, as last read from Accounting, are what the
-- differences and the received-not-invoiced list are judged by without
-- calling Accounting back; a posted invoice's never change again. A
-- provisional match (a draft's) is fixed when the invoice is posted and
-- deleted when it is reversed (P37); both count in order_lines.invoiced.
CREATE TABLE purchasing.invoice_matches (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    supplier_invoice_id bigint        NOT NULL,
    line_position       integer       NOT NULL,
    order_id            bigint        NOT NULL REFERENCES purchasing.orders (id) ON DELETE RESTRICT,
    order_line_id       bigint        NOT NULL REFERENCES purchasing.order_lines (id) ON DELETE RESTRICT,
    kind                varchar(12)   NOT NULL,
    quantity            numeric(12,3) NOT NULL,
    line_net            numeric(14,2) NOT NULL,
    invoice_number      varchar(60)   NOT NULL,
    invoice_date        date          NOT NULL,
    invoice_currency    char(3)       NOT NULL,
    state               varchar(12)   NOT NULL DEFAULT 'provisional',
    posted_at           timestamptz,
    matched_by_user_id  uuid,
    matched_by_display  varchar(200),
    created_at          timestamptz   NOT NULL,
    updated_at          timestamptz   NOT NULL,
    CONSTRAINT ck_invoice_matches_kind CHECK (kind IN ('invoice', 'credit_note')),
    CONSTRAINT ck_invoice_matches_state CHECK (state IN ('provisional', 'posted')),
    CONSTRAINT ck_invoice_matches_posted CHECK ((state = 'posted') = (posted_at IS NOT NULL)),
    CONSTRAINT ck_invoice_matches_position CHECK (line_position >= 1),
    CONSTRAINT ck_invoice_matches_quantity CHECK (quantity > 0)
);
CREATE UNIQUE INDEX ux_invoice_matches_line ON purchasing.invoice_matches (supplier_invoice_id, line_position);
CREATE INDEX ix_invoice_matches_order ON purchasing.invoice_matches (order_id);
CREATE INDEX ix_invoice_matches_order_line ON purchasing.invoice_matches (order_line_id);

-- What Purchasing has learnt of a supplier invoice from Accounting's events
-- (P5, A15): posted or reversed, and the copy a reversal made. Its row is
-- also the invoice's lock: every write of an invoice's matches — a match,
-- the event handlers, the posted invoice's worker — takes it FOR UPDATE
-- first, then the orders, so a match made while the invoice is being posted
-- cannot be left provisional, and a posted event delivered after its
-- reversal changes nothing.
CREATE TABLE purchasing.invoice_states (
    supplier_invoice_id bigint      PRIMARY KEY,
    state               varchar(10) NOT NULL DEFAULT 'open',
    copy_id             bigint,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT ck_invoice_states_state CHECK (state IN ('open', 'posted', 'reversed'))
);

-- The events the handlers took (A15): each by its id, so a replay changes
-- nothing.
CREATE TABLE purchasing.invoice_events (
    event_id            bigint      PRIMARY KEY,
    name                varchar(80) NOT NULL,
    supplier_invoice_id bigint      NOT NULL,
    handled_at          timestamptz NOT NULL
);

-- A posted invoice's follow-up (P39, A9; module-boundaries rule 23): the
-- posted handler reads no contract, so it queues the invoice here, and the
-- purchasing.invoice-posted worker reads the posted lines through
-- contracts.SupplierInvoices on the pool, writes each match's posted net,
-- releases a match whose line is gone, and sets the last prices.
CREATE TABLE purchasing.invoice_followups (
    supplier_invoice_id bigint       PRIMARY KEY,
    requested_at        timestamptz  NOT NULL,
    next_attempt_at     timestamptz  NOT NULL,
    attempts            integer      NOT NULL DEFAULT 0,
    last_error          varchar(500),
    completed_at        timestamptz
);
CREATE INDEX ix_invoice_followups_due ON purchasing.invoice_followups (next_attempt_at) WHERE completed_at IS NULL;

-- Register invoice (P37, A1, A11): the draft a clientRequestId made through
-- contracts.SupplierInvoices.CreateDraft, and the hash a replay is judged by.
CREATE TABLE purchasing.invoice_registrations (
    client_request_id   uuid        PRIMARY KEY,
    request_hash        char(64)    NOT NULL,
    order_id            bigint      NOT NULL REFERENCES purchasing.orders (id) ON DELETE RESTRICT,
    supplier_invoice_id bigint      NOT NULL,
    lines               integer     NOT NULL,
    lines_left          integer     NOT NULL,
    created_at          timestamptz NOT NULL
);

-- +goose Down
DROP TABLE purchasing.invoice_registrations;
DROP TABLE purchasing.invoice_followups;
DROP TABLE purchasing.invoice_events;
DROP TABLE purchasing.invoice_states;
DROP TABLE purchasing.invoice_matches;
