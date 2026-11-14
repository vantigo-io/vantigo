-- +goose Up
-- Accounting's SAF-T Financial (accounting design §6, delivery E: A24, A25).
--
-- saft_exports is every export asked for — who, when, the selection (a fiscal
-- year and the periods within it) — and the log of what came of it: the
-- state a lease worker moves (queued, running, done, failed with the error
-- kept), and per file written its name, object key, size and SHA-256. An
-- export is never deleted: it is the record that the books were handed over.
--
-- saft_imports is a SAF-T file uploaded to set the books up from another
-- system: the file itself is kept in the object store (its key and hash
-- here), the master data read from it as a preview (jsonb), and once applied
-- who applied it, when, and the opening voucher it made. At most one import
-- is ever applied: the import is the other way to Set up the books, and the
-- settings row is written by whichever comes first.
CREATE TABLE accounting.saft_exports (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    fiscal_year          integer      NOT NULL,
    from_period          smallint     NOT NULL,
    to_period            smallint     NOT NULL,
    period_start         date         NOT NULL,
    period_end           date         NOT NULL,
    state                varchar(10)  NOT NULL DEFAULT 'queued',
    error                varchar(1000),
    attempts             integer      NOT NULL DEFAULT 0,
    lease_id             uuid,
    lease_until          timestamptz,
    -- unmapped_accounts is how many accounts had no grouping for the year
    -- when the export was asked for: they export NA.
    unmapped_accounts    integer      NOT NULL DEFAULT 0,
    total_bytes          bigint,
    requested_at         timestamptz  NOT NULL,
    requested_by_user_id uuid         NOT NULL,
    started_at           timestamptz,
    finished_at          timestamptz,
    CONSTRAINT ck_saft_exports_state CHECK (state IN ('queued', 'running', 'done', 'failed')),
    CONSTRAINT ck_saft_exports_periods CHECK (from_period BETWEEN 1 AND 12 AND to_period BETWEEN from_period AND 12),
    CONSTRAINT ck_saft_exports_dates CHECK (period_end >= period_start),
    CONSTRAINT ck_saft_exports_year CHECK (fiscal_year BETWEEN 2000 AND 2100),
    CONSTRAINT ck_saft_exports_failed CHECK ((state = 'failed') = (error IS NOT NULL))
);
CREATE INDEX ix_saft_exports_queue ON accounting.saft_exports (id) WHERE state IN ('queued', 'running');

-- One row per file an export wrote: one, or several when the file would pass
-- the split threshold and the selection was split by period, each part a
-- valid file alone.
CREATE TABLE accounting.saft_export_files (
    export_id    bigint        NOT NULL REFERENCES accounting.saft_exports (id) ON DELETE RESTRICT,
    part         integer       NOT NULL,
    parts        integer       NOT NULL,
    file_name    varchar(200)  NOT NULL,
    object_key   varchar(300)  NOT NULL,
    size_bytes   bigint        NOT NULL,
    sha256       varchar(64)   NOT NULL,
    from_period  smallint      NOT NULL,
    to_period    smallint      NOT NULL,
    period_start date          NOT NULL,
    period_end   date          NOT NULL,
    PRIMARY KEY (export_id, part),
    CONSTRAINT ck_saft_export_files_part CHECK (part BETWEEN 1 AND parts)
);

CREATE TRIGGER tr_saft_exports_no_delete
    BEFORE DELETE ON accounting.saft_exports
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();
CREATE TRIGGER tr_saft_export_files_immutable
    BEFORE UPDATE OR DELETE ON accounting.saft_export_files
    FOR EACH ROW EXECUTE FUNCTION accounting.refuse_change();

CREATE TABLE accounting.saft_imports (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    state               varchar(10)   NOT NULL DEFAULT 'preview',
    file_name           varchar(255)  NOT NULL,
    object_key          varchar(300)  NOT NULL,
    size_bytes          bigint        NOT NULL,
    sha256              varchar(64)   NOT NULL,
    audit_file_version  varchar(10)   NOT NULL,
    selection_start     date          NOT NULL,
    selection_end       date          NOT NULL,
    -- preview is the master data read from the file: the header, the
    -- accounts with their balances, the tax table, the customers and the
    -- suppliers. The transactions are counted, never kept.
    preview             jsonb         NOT NULL,
    uploaded_at         timestamptz   NOT NULL,
    uploaded_by_user_id uuid          NOT NULL,
    applied_at          timestamptz,
    applied_by_user_id  uuid,
    opening_voucher_id  bigint        REFERENCES accounting.vouchers (id) ON DELETE RESTRICT,
    CONSTRAINT ck_saft_imports_state CHECK (state IN ('preview', 'applied')),
    CONSTRAINT ck_saft_imports_applied CHECK (
        (state = 'applied') = (applied_at IS NOT NULL) AND (state = 'applied') = (applied_by_user_id IS NOT NULL)),
    CONSTRAINT ck_saft_imports_selection CHECK (selection_end >= selection_start)
);
CREATE UNIQUE INDEX ux_saft_imports_applied ON accounting.saft_imports ((true)) WHERE state = 'applied';

-- +goose Down
DROP TABLE accounting.saft_imports;
DROP TABLE accounting.saft_export_files;
DROP TABLE accounting.saft_exports;
