-- +goose Up
CREATE SCHEMA expenses;

-- Every foreign identifier here is opaque: users, projects and billing lines
-- live in other schemas (docs/module-boundaries.md rule 4). There are no CHECK
-- constraints anywhere in this schema — the house style is that the module's
-- own validation owns the domain rules and reports them per field.

-- The expense categories an outlay is booked on (design §3.3). Rows are
-- deactivated, never deleted, once an entry has used one; the unique index is
-- case-insensitive, so "Travel" and "travel" are the same category.
CREATE TABLE expenses.categories (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name       varchar(100) NOT NULL,
    active     boolean      NOT NULL DEFAULT true,
    position   integer      NOT NULL,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_categories_name_lower ON expenses.categories (lower(name));

-- The dated rate table (design §3.4, decision X8). The rate for a date is the
-- row of that kind with the greatest valid_from on or before it. currency is
-- NULL for the percentage kinds; source is the label a seeded row carries
-- ("State rate"), NULL for a row the company entered itself.
CREATE TABLE expenses.rates (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind       varchar(40)   NOT NULL,
    valid_from date          NOT NULL,
    value      numeric(10,2) NOT NULL,
    currency   char(3),
    source     varchar(100),
    created_at timestamptz   NOT NULL,
    updated_at timestamptz   NOT NULL
);
CREATE UNIQUE INDEX ux_rates_kind_valid_from ON expenses.rates (kind, valid_from);

-- The installation's expense settings (design §3.5): one row, id 1, written by
-- this migration so every read finds it.
CREATE TABLE expenses.settings (
    id                     smallint      PRIMARY KEY,
    locked_before          date,
    default_currency       char(3)       NOT NULL DEFAULT 'NOK',
    default_markup_percent numeric(6,2)  NOT NULL DEFAULT 0,
    receipt_required_over  numeric(12,2),
    updated_at             timestamptz   NOT NULL
);
-- "One row" is the table's rule, not a convention the queries keep: a unique
-- index on a constant expression admits exactly one row, whoever writes it — a
-- later migration, a support script, a restore. House style has no CHECK
-- constraints, and this says the same thing without one.
CREATE UNIQUE INDEX ux_settings_single_row ON expenses.settings ((true));

-- One money line (design §3.1): an outlay, a mileage line or — from delivery B
-- — a per diem day. claim_id is the travel claim it belongs to; delivery B adds
-- expenses.claims and the foreign key, so it stands here without one.
CREATE TABLE expenses.entries (
    id                         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id                    uuid          NOT NULL,
    created_by_user_id         uuid          NOT NULL,
    claim_id                   bigint,
    kind                       varchar(20)   NOT NULL,
    entry_date                 date          NOT NULL,
    description                varchar(500)  NOT NULL,
    category_id                integer       REFERENCES expenses.categories (id),
    supplier                   varchar(200),
    paid_by                    varchar(20),
    currency                   char(3)       NOT NULL,
    gross_amount               numeric(12,2) NOT NULL,
    vat_amount                 numeric(12,2),
    distance_km                numeric(8,1),
    from_place                 varchar(200),
    to_place                   varchar(200),
    passengers                 smallint      NOT NULL DEFAULT 0,
    rate                       numeric(10,2),
    passenger_rate             numeric(10,2),
    rate_overridden_by_user_id uuid,
    rate_table_value           numeric(10,2),
    passenger_rate_table_value numeric(10,2),
    project_id                 integer,
    billing_line_id            integer,
    billable                   boolean       NOT NULL DEFAULT false,
    markup_percent             numeric(6,2),
    bill_rate_per_km           numeric(10,2),
    bill_amount                numeric(12,2),
    status                     varchar(20)   NOT NULL DEFAULT 'draft',
    submitted_at               timestamptz,
    decided_at                 timestamptz,
    decided_by_user_id         uuid,
    rejection_reason           varchar(1000),
    reimbursed_at              timestamptz,
    reimbursed_by_user_id      uuid,
    reimbursement_reference    varchar(100),
    reimbursement_date         date,
    invoiced_at                timestamptz,
    invoiced_by_user_id        uuid,
    invoice_reference          varchar(100),
    revision                   integer       NOT NULL DEFAULT 1,
    created_at                 timestamptz   NOT NULL,
    updated_at                 timestamptz   NOT NULL
);
CREATE INDEX ix_entries_user_id_entry_date ON expenses.entries (user_id, entry_date);
CREATE INDEX ix_entries_project_id_entry_date ON expenses.entries (project_id, entry_date);
CREATE INDEX ix_entries_submitted ON expenses.entries (status) WHERE status = 'submitted';
CREATE INDEX ix_entries_claim_id ON expenses.entries (claim_id);
-- The two tracks after approval (design §2, decision X5). Both are partial on
-- the state the track is about, so each holds only the rows still waiting for
-- somebody to act and shrinks again as they are dealt with.
--
-- The payroll one serves every query of the reimbursement track — the list's
-- count and page, the export, and what one person is still owed — and it is
-- the only index the dashboard's "waiting to be reimbursed" figure has: that
-- one carries no user predicate at all and runs on every expenses:manage
-- dashboard load, so without it each paint is a sequential scan of the whole
-- table. user_id leads because four of the five group or order by it; the
-- fifth reads the index end to end, which is what it wants.
CREATE INDEX ix_entries_reimbursement_waiting ON expenses.entries (user_id, entry_date)
    WHERE status = 'approved' AND reimbursed_at IS NULL;
-- The invoicing one is the same shape for the other track, keyed on the
-- project because that is the side it belongs to: what a project still has to
-- put on an invoice. No query reads that predicate yet — this delivery only
-- marks one line at a time, by id — but the project-side reads that page
-- through it arrive next, and a partial index over the rows still waiting to
-- be invoiced costs almost nothing while it waits for them.
CREATE INDEX ix_entries_to_invoice ON expenses.entries (project_id, entry_date)
    WHERE status = 'approved' AND billable AND invoiced_at IS NULL;

-- A receipt (design §3.2, decision X11): the row owns the object key, the
-- bytes live in the object store, and deleting the entry takes its receipts
-- with it.
CREATE TABLE expenses.attachments (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_id           bigint       NOT NULL REFERENCES expenses.entries (id) ON DELETE CASCADE,
    object_key         text         NOT NULL,
    file_name          varchar(255) NOT NULL,
    content_type       varchar(100) NOT NULL,
    size_bytes         bigint       NOT NULL,
    uploaded_by_user_id uuid        NOT NULL,
    created_at         timestamptz  NOT NULL
);
CREATE INDEX ix_attachments_entry_id ON expenses.attachments (entry_id);

-- The seeded categories (design §3.3), in the order they are offered.
INSERT INTO expenses.categories (name, active, position, created_at, updated_at) VALUES
    ('Materials',          true, 1, now(), now()),
    ('Subcontractor',      true, 2, now(), now()),
    ('Equipment hire',     true, 3, now(), now()),
    ('Travel',             true, 4, now(), now()),
    ('Accommodation',      true, 5, now(), now()),
    ('Meals',              true, 6, now(), now()),
    ('Phone and internet', true, 7, now(), now()),
    ('Other',              true, 8, now(), now());

-- The seeded rates (design §3.4): the Norwegian state mileage rate and its
-- passenger supplement, verified against Skatteetaten's published rates on
-- 2026-09-19. They are ordinary rows an administrator may change or remove;
-- POST /rates/reset puts a removed one back from the same table the module
-- keeps in Go (seededRates in rates.go), which a test holds against these rows.
-- mileage_customer is deliberately unseeded: what a customer is charged per
-- kilometre is the company's own price. The per diem rates are delivery B's.
INSERT INTO expenses.rates (kind, valid_from, value, currency, source, created_at, updated_at) VALUES
    ('mileage',           DATE '2026-01-01', 5.30, 'NOK', 'State rate', now(), now()),
    ('mileage_passenger', DATE '2026-01-01', 1.00, 'NOK', 'State rate', now(), now());

-- The single settings row, with the defaults of design §3.5: no period lock,
-- NOK, no markup and no receipt threshold.
INSERT INTO expenses.settings (id, locked_before, default_currency, default_markup_percent, receipt_required_over, updated_at)
VALUES (1, NULL, 'NOK', 0, NULL, now());

-- +goose Down
DROP SCHEMA expenses CASCADE;
