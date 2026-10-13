-- +goose Up
-- An import of customers from a file (customers import wizard design D1): the
-- file parsed once at upload and kept as rows, the mapping of its columns to
-- customer fields, and the state the worker moves it through — draft,
-- checking, checked, importing, done. header is the file's header row as
-- written; mapping the JSON document PUT …/mapping saves (NULL until it is);
-- processed and the three counts are what the worker has done so far, the
-- final ones recounted from the rows at the end. error is a whole-run failure
-- the worker wrote before retrying next cycle. created_by_user_id is
-- identity's user, opaque here (module boundaries rule 4), nullable like a
-- timeline entry's actor_user_id, and created_by_name the display name the
-- upload resolved beside it — snapshotted as a timeline entry's actor is,
-- because the worker that writes the rows runs without a user directory
-- (worker mode composes none) and must still name the uploader on every
-- event. The bytes of the file are not kept: the rows are the file.
CREATE TABLE customers.imports (
    id                 integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    state              varchar(20)  NOT NULL,
    file_name          varchar(255) NOT NULL,
    size_bytes         bigint       NOT NULL,
    encoding           varchar(20)  NOT NULL,
    separator          varchar(1)   NOT NULL,
    header             text[]       NOT NULL,
    row_count          integer      NOT NULL,
    mapping            jsonb,
    processed          integer      NOT NULL DEFAULT 0,
    created            integer      NOT NULL DEFAULT 0,
    updated            integer      NOT NULL DEFAULT 0,
    failed             integer      NOT NULL DEFAULT 0,
    stop_requested     boolean      NOT NULL DEFAULT false,
    error              text,
    created_by_user_id uuid,
    created_by_name    varchar(200),
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL,
    requested_at       timestamptz,
    started_at         timestamptz,
    finished_at        timestamptz,
    CONSTRAINT ck_imports_state CHECK (state IN ('draft', 'checking', 'checked', 'importing', 'done')),
    CONSTRAINT ck_imports_counts CHECK (processed >= 0 AND created >= 0 AND updated >= 0 AND failed >= 0 AND row_count >= 0)
);
-- The worker takes the oldest queued import; the page lists the newest first.
CREATE INDEX ix_imports_queue ON customers.imports (requested_at) WHERE state IN ('checking', 'importing');
CREATE INDEX ix_imports_recent ON customers.imports (updated_at DESC, id DESC);

-- One data row of an import: its cells as the reader left them (the formula
-- guard taken off), and — once the worker has run it — what it did. outcome
-- is NULL for a row not yet run, so a run resumes at the first NULL; errors is
-- the row's [{column, message}] with column the file's 0-based column index,
-- since a mapped file may have two columns with the same header, and absent
-- for a problem with the row as a whole. customer_id is the customer the row
-- created or updated, so a finished import links each row to its customer.
-- The import's deletion takes the rows with it.
CREATE TABLE customers.import_rows (
    import_id   integer  NOT NULL REFERENCES customers.imports (id) ON DELETE CASCADE,
    row_no      integer  NOT NULL,
    cells       text[]   NOT NULL,
    outcome     varchar(10),
    customer_id integer,
    errors      jsonb,
    PRIMARY KEY (import_id, row_no),
    CONSTRAINT ck_import_rows_outcome CHECK (outcome IS NULL OR outcome IN ('created', 'updated', 'failed'))
);

-- +goose Down
DROP TABLE customers.import_rows;
DROP TABLE customers.imports;
