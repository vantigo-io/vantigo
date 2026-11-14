-- +goose Up
-- Accounting, sales post themselves (accounting design A12–A16 as amended by
-- R1, R3, R4, R5, R7, R9, R14; delivery B): the posting rules, the catch-up's
-- cursors, requests and leases, the facts that could not be posted, and two
-- columns the sales vouchers need. Nothing is seeded here: the default rules
-- are written by Set up the books, and for books set up before this
-- migration by the first use that needs them.

-- A posting rule (A14): which account a sales line's net is credited to —
-- later, with delivery C, which account an expense is debited to. A rule
-- matches on exactly one of a line's kind, a product's tax category, an
-- expense category or a standard tax code; the default rules (match
-- 'default') are seeded per tax code and are the last resort. version counts
-- the rule's changes of name or account (R15): each version is kept in
-- posting_rule_versions, a posting names the rule and the version that
-- placed it, and changing a rule changes future vouchers only. A rule is
-- archived (archived_at), never deleted, so what it placed keeps its
-- provenance; an archived rule matches nothing and frees its name and match.
CREATE TABLE accounting.posting_rules (
    id                  integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    name                varchar(100) NOT NULL,
    scope               varchar(10)  NOT NULL,
    match               varchar(20)  NOT NULL,
    line_kind           varchar(20),
    tax_category_id     integer,
    expense_category_id integer,
    tax_code            varchar(5),
    account_id          integer      NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    version             integer      NOT NULL DEFAULT 1,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    revision            integer      NOT NULL DEFAULT 1,
    archived_at         timestamptz,
    CONSTRAINT ck_posting_rules_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_posting_rules_scope CHECK (scope IN ('sales', 'expense')),
    CONSTRAINT ck_posting_rules_match CHECK (match IN ('line_kind', 'tax_category', 'expense_category', 'tax_code', 'default')),
    CONSTRAINT ck_posting_rules_one_match CHECK (
        (match = 'line_kind') = (line_kind IS NOT NULL)
        AND (match = 'tax_category') = (tax_category_id IS NOT NULL)
        AND (match = 'expense_category') = (expense_category_id IS NOT NULL)
        AND (match IN ('tax_code', 'default')) = (tax_code IS NOT NULL)),
    CONSTRAINT ck_posting_rules_scope_match CHECK (
        (match NOT IN ('line_kind', 'tax_category') OR scope = 'sales')
        AND (match <> 'expense_category' OR scope = 'expense')),
    CONSTRAINT ck_posting_rules_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX ux_posting_rules_name ON accounting.posting_rules (name) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX ux_posting_rules_match ON accounting.posting_rules (scope, match,
    coalesce(line_kind, ''), coalesce(tax_category_id, expense_category_id, 0), coalesce(tax_code, ''))
    WHERE archived_at IS NULL;

-- Every version a rule has had (R15): its name and account from valid_from
-- on. The trigger writes one when a rule is made and each time its version
-- moves, so a posting's (posting_rule_id, posting_rule_version) always names
-- a row here, whatever the rule has become since.
CREATE TABLE accounting.posting_rule_versions (
    rule_id    integer      NOT NULL REFERENCES accounting.posting_rules (id) ON DELETE RESTRICT,
    version    integer      NOT NULL,
    name       varchar(100) NOT NULL,
    account_id integer      NOT NULL REFERENCES accounting.accounts (id) ON DELETE RESTRICT,
    valid_from timestamptz  NOT NULL,
    PRIMARY KEY (rule_id, version)
);

-- +goose StatementBegin
CREATE FUNCTION accounting.record_posting_rule_version()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.version <> OLD.version THEN
        INSERT INTO accounting.posting_rule_versions (rule_id, version, name, account_id, valid_from)
        VALUES (NEW.id, NEW.version, NEW.name, NEW.account_id, NEW.updated_at);
    END IF;
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_posting_rules_version
    AFTER INSERT OR UPDATE ON accounting.posting_rules
    FOR EACH ROW EXECUTE FUNCTION accounting.record_posting_rule_version();

-- A rule is archived, never deleted (R15).
-- +goose StatementBegin
CREATE FUNCTION accounting.refuse_posting_rule_delete()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'accounting: a posting rule is archived, never deleted' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_posting_rules_no_delete
    BEFORE DELETE ON accounting.posting_rules
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_posting_rule_delete();

-- The catch-up's position in each stream (R1, R7): 'invoices.documents' by
-- document number, 'invoices.facts' by fact seq. A claim takes the stream's
-- lease (lease_id until lease_until) in one short transaction; the worker
-- reads the source with no transaction open and posts each fact in its own
-- transaction, which also moves position, naming its lease.
CREATE TABLE accounting.cursors (
    stream      varchar(40)  PRIMARY KEY,
    position    bigint       NOT NULL DEFAULT 0,
    lease_id    uuid,
    lease_until timestamptz,
    last_run_at timestamptz,
    last_error  varchar(500),
    updated_at  timestamptz  NOT NULL,
    CONSTRAINT ck_cursors_position CHECK (position >= 0),
    CONSTRAINT ck_cursors_lease CHECK ((lease_id IS NULL) = (lease_until IS NULL))
);

-- A wake-up for a stream (R7): an event handler upserts the row on its
-- delivery transaction and does nothing else; a claim deletes it.
CREATE TABLE accounting.catchup_requests (
    stream       varchar(40) PRIMARY KEY,
    requested_at timestamptz NOT NULL
);

-- A source fact that could not be posted (A15): an unknown tax code, an
-- inactive account, a rule the engine refused, a kind the module does not
-- know. The cursor moves past it; Retry reads the fact again and, when it
-- posts, records the voucher. One row per source fact. invoice_id is the
-- invoice it is about (a credit note's, the invoice it credits): a later
-- document or fact of that invoice waits behind an open row (R15), as one of
-- reason waiting_on whose waiting_on names the row it waits for.
CREATE TABLE accounting.unposted (
    id                bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    stream            varchar(40)   NOT NULL,
    position          bigint        NOT NULL,
    source_module     varchar(30)   NOT NULL,
    source_kind       varchar(40)   NOT NULL,
    source_id         bigint        NOT NULL,
    source_part       varchar(40)   NOT NULL DEFAULT '',
    fact_date         date,
    document_id       bigint,
    document_number   varchar(40),
    customer_id       integer,
    amount            numeric(14,2),
    reason            varchar(40)   NOT NULL,
    detail            varchar(500)  NOT NULL DEFAULT '',
    attempts          integer       NOT NULL DEFAULT 1,
    first_seen_at     timestamptz   NOT NULL,
    last_tried_at     timestamptz   NOT NULL,
    posted_voucher_id bigint        REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    posted_at         timestamptz,
    invoice_id        bigint,
    waiting_on        bigint        REFERENCES accounting.unposted (id) ON DELETE RESTRICT,
    CONSTRAINT ck_unposted_posted CHECK ((posted_voucher_id IS NULL) = (posted_at IS NULL)),
    CONSTRAINT ck_unposted_waiting CHECK ((reason = 'waiting_on') = (waiting_on IS NOT NULL))
);
CREATE UNIQUE INDEX ux_unposted_source ON accounting.unposted (source_module, source_kind, source_id, source_part);
CREATE INDEX ix_unposted_open ON accounting.unposted (fact_date) WHERE posted_at IS NULL;
CREATE INDEX ix_unposted_open_invoice ON accounting.unposted (invoice_id, id) WHERE posted_at IS NULL;

-- The document in Invoices a system voucher belongs to (the invoice a
-- payment, a credit note or a write-off is about), so a voucher links to it
-- and an invoice to its vouchers.
ALTER TABLE accounting.vouchers ADD COLUMN source_document_id bigint;
CREATE INDEX ix_vouchers_source_document ON accounting.vouchers (source_document_id)
    WHERE source_document_id IS NOT NULL;

-- A tax posting's specification line on the VAT return (A23, R4): tapPåKrav
-- for the VAT a konstatert loss takes back. NULL for an ordinary posting.
ALTER TABLE accounting.postings
    ADD COLUMN tax_specification varchar(30),
    ADD CONSTRAINT ck_postings_tax_specification
        CHECK (tax_specification IS NULL OR (tax_code_id IS NOT NULL AND tax_specification IN ('tapPåKrav')));

-- The posting rule and version that placed a revenue line (A14, R15), NULL
-- on every other posting: the version row keeps its name and account.
ALTER TABLE accounting.postings
    ADD COLUMN posting_rule_id      integer,
    ADD COLUMN posting_rule_version integer,
    ADD CONSTRAINT fk_postings_posting_rule_version FOREIGN KEY (posting_rule_id, posting_rule_version)
        REFERENCES accounting.posting_rule_versions (rule_id, version) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_postings_posting_rule
        CHECK ((posting_rule_id IS NULL) = (posting_rule_version IS NULL));

-- +goose Down
ALTER TABLE accounting.postings
    DROP CONSTRAINT ck_postings_posting_rule,
    DROP CONSTRAINT fk_postings_posting_rule_version,
    DROP COLUMN posting_rule_version,
    DROP COLUMN posting_rule_id;
ALTER TABLE accounting.postings
    DROP CONSTRAINT ck_postings_tax_specification,
    DROP COLUMN tax_specification;
DROP INDEX accounting.ix_vouchers_source_document;
ALTER TABLE accounting.vouchers DROP COLUMN source_document_id;
DROP TABLE accounting.unposted;
DROP TABLE accounting.catchup_requests;
DROP TABLE accounting.cursors;
DROP TABLE accounting.posting_rule_versions;
DROP TRIGGER tr_posting_rules_no_delete ON accounting.posting_rules;
DROP TRIGGER tr_posting_rules_version ON accounting.posting_rules;
DROP TABLE accounting.posting_rules;
DROP FUNCTION accounting.refuse_posting_rule_delete();
DROP FUNCTION accounting.record_posting_rule_version();
