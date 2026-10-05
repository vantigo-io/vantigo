-- +goose Up
-- Invoices phase 3 (work becomes invoices; invoices work design D2, D5-D9):
-- the whole phase's schema at once — the line sources held from the draft,
-- the credit note's releases, the timesheet rows, the deduction line, the
-- document's project and timesheet flag, and the work settings — so no later
-- task of the phase adds a migration. Until a task gives them behaviour the
-- columns sit at their defaults. Every column the document gains is frozen at
-- issue by 00034's refuse_issued_document_change, which compares the whole
-- row. No column is named with a word PostgreSQL reserves; a schema test pins
-- it.

-- A line's id together with its document's, so a child table can name both
-- and never disagree with its line (invoices work design D2, D8).
ALTER TABLE invoices.lines
    ADD CONSTRAINT uq_lines_id_invoice UNIQUE (id, invoice_id),
    ADD COLUMN deducts_invoice_id bigint REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity
        CHECK (quantity > 0 OR (quantity < 0 AND deducts_invoice_id IS NOT NULL)),
    ADD CONSTRAINT ck_lines_deduction_no_discount
        CHECK (deducts_invoice_id IS NULL OR discount_percent = 0);
CREATE INDEX ix_lines_deducts ON invoices.lines (deducts_invoice_id) WHERE deducts_invoice_id IS NOT NULL;

-- The project all of a document's work belongs to (D9): derived by every
-- save, NULL when it spans two or holds none, frozen at issue with the rest.
ALTER TABLE invoices.invoices
    ADD COLUMN project_id        integer,
    ADD COLUMN project_reference varchar(30),
    ADD COLUMN timesheet         boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT ck_invoices_project CHECK ((project_id IS NULL) = (project_reference IS NULL));
CREATE INDEX ix_invoices_project ON invoices.invoices (project_id) WHERE project_id IS NOT NULL;

ALTER TABLE invoices.settings
    ADD COLUMN work_vat_code_hours      integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN work_vat_code_expenses   integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN work_vat_code_milestones integer     NOT NULL DEFAULT 1 REFERENCES invoices.vat_codes (id) ON DELETE RESTRICT,
    ADD COLUMN timesheet_default        boolean     NOT NULL DEFAULT false,
    ADD COLUMN timesheet_person_label   varchar(10) NOT NULL DEFAULT 'initials',
    ADD CONSTRAINT ck_settings_timesheet_person_label
        CHECK (timesheet_person_label IN ('initials', 'number', 'name'));

-- The work a line bills (D2): held from the draft, invoiced by the issue,
-- released by the credit note that returns the line. Opaque ids — the sources
-- are other modules' rows (rule 4). amount is the source's exact amount:
-- Time's carries up to eight decimals (plan reading 3).
CREATE TABLE invoices.line_sources (
    id              bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    line_id         bigint        NOT NULL,
    invoice_id      bigint        NOT NULL,
    source_kind     varchar(30)   NOT NULL,
    source_id       bigint        NOT NULL,
    source_revision integer       NOT NULL,
    source_subkind  varchar(20),
    project_id      integer       NOT NULL,
    quantity        numeric(12,3) NOT NULL,
    amount          numeric(22,8) NOT NULL,
    currency        char(3)       NOT NULL,
    state           varchar(10)   NOT NULL DEFAULT 'held',
    source_date     date          NOT NULL,
    CONSTRAINT fk_line_sources_line FOREIGN KEY (line_id, invoice_id)
        REFERENCES invoices.lines (id, invoice_id) ON DELETE CASCADE,
    CONSTRAINT ck_line_sources_kind
        CHECK (source_kind IN ('time.entry', 'expenses.entry', 'projects.milestone')),
    CONSTRAINT ck_line_sources_subkind
        CHECK ((source_kind = 'expenses.entry') = (source_subkind IS NOT NULL)),
    CONSTRAINT ck_line_sources_state CHECK (state IN ('held', 'invoiced', 'released')),
    CONSTRAINT ck_line_sources_quantity CHECK (quantity > 0)
);
-- The floor (D2): a source is held by one live draft or invoiced by one
-- unreleased issued line, whatever the interleaving.
CREATE UNIQUE INDEX ux_line_sources_live ON invoices.line_sources (source_kind, source_id)
    WHERE state IN ('held', 'invoiced');
CREATE INDEX ix_line_sources_invoice ON invoices.line_sources (invoice_id);
CREATE INDEX ix_line_sources_line ON invoices.line_sources (line_id);

-- A credit note's release of an original line's source (D8): written by the
-- credit note's issue, under refuse_issued_child_change through invoice_id.
CREATE TABLE invoices.line_releases (
    id             bigint GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id     bigint NOT NULL,
    credit_line_id bigint NOT NULL,
    line_source_id bigint NOT NULL REFERENCES invoices.line_sources (id) ON DELETE RESTRICT,
    CONSTRAINT fk_line_releases_line FOREIGN KEY (credit_line_id, invoice_id)
        REFERENCES invoices.lines (id, invoice_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX ux_line_releases_source ON invoices.line_releases (line_source_id);
CREATE INDEX ix_line_releases_invoice ON invoices.line_releases (invoice_id);

-- The timesheet as printed (D5): a snapshot, never the entry's note.
CREATE TABLE invoices.timesheet_rows (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id   bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    position     integer      NOT NULL,
    source_id    bigint       NOT NULL,
    person_label varchar(200) NOT NULL,
    entry_date   date         NOT NULL,
    hours        numeric(5,2) NOT NULL,
    work_type    varchar(100),
    description  varchar(200) NOT NULL,
    CONSTRAINT ck_timesheet_rows_position CHECK (position >= 1),
    CONSTRAINT ck_timesheet_rows_hours CHECK (hours > 0)
);
CREATE UNIQUE INDEX ux_timesheet_rows_invoice_position ON invoices.timesheet_rows (invoice_id, position);

-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_issued_line_source_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    parent_status text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT status INTO parent_status FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE;
        IF parent_status = 'issued' THEN
            RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
        END IF;
        IF NEW.state <> 'held' THEN
            RAISE EXCEPTION 'invoices: a line source is written held' USING ERRCODE = 'P0001';
        END IF;
        RETURN NEW;
    END IF;
    SELECT status INTO parent_status FROM invoices.invoices WHERE id = OLD.invoice_id FOR SHARE;
    IF TG_OP = 'DELETE' THEN
        IF parent_status = 'issued' THEN
            RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
        END IF;
        RETURN OLD;
    END IF;
    -- UPDATE: one transition per parent state, nothing else changed.
    IF (to_jsonb(NEW) - 'state') IS DISTINCT FROM (to_jsonb(OLD) - 'state') THEN
        RAISE EXCEPTION 'invoices: a line source changes only its state' USING ERRCODE = 'P0001';
    END IF;
    IF parent_status = 'issued' AND OLD.state = 'invoiced' AND NEW.state = 'released' THEN
        RETURN NEW;
    END IF;
    IF parent_status IS DISTINCT FROM 'issued' AND OLD.state = 'held' AND NEW.state = 'invoiced' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: issued document is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_line_sources_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.line_sources
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_line_source_change();
CREATE TRIGGER tr_line_releases_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.line_releases
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();
CREATE TRIGGER tr_timesheet_rows_immutable
    BEFORE INSERT OR UPDATE OR DELETE ON invoices.timesheet_rows
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_issued_child_change();

-- +goose Down
-- Everything 00040 added: the tables (their triggers and indexes go with
-- them), the line sources' trigger function, then the settings', the
-- document's and the lines' columns with their CHECKs, the old quantity
-- CHECK restored.
DROP TABLE invoices.timesheet_rows;
DROP TABLE invoices.line_releases;
DROP TABLE invoices.line_sources;
DROP FUNCTION invoices.refuse_issued_line_source_change();
ALTER TABLE invoices.settings
    DROP CONSTRAINT ck_settings_timesheet_person_label,
    DROP COLUMN timesheet_person_label,
    DROP COLUMN timesheet_default,
    DROP COLUMN work_vat_code_milestones,
    DROP COLUMN work_vat_code_expenses,
    DROP COLUMN work_vat_code_hours;
DROP INDEX invoices.ix_invoices_project;
ALTER TABLE invoices.invoices
    DROP CONSTRAINT ck_invoices_project,
    DROP COLUMN timesheet,
    DROP COLUMN project_reference,
    DROP COLUMN project_id;
-- Restoring the old CHECK fails while a deduction line exists, which is the
-- honest answer: a Down never deletes bookkeeping.
DROP INDEX invoices.ix_lines_deducts;
ALTER TABLE invoices.lines
    DROP CONSTRAINT ck_lines_deduction_no_discount,
    DROP CONSTRAINT ck_lines_quantity,
    ADD CONSTRAINT ck_lines_quantity CHECK (quantity > 0),
    DROP COLUMN deducts_invoice_id,
    DROP CONSTRAINT uq_lines_id_invoice;
