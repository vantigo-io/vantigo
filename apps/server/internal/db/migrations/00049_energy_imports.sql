-- +goose Up
-- An import of metering points from a file (energy import design D3, D6): the
-- customers import's record (migrations 00045 and 00048) in energy's schema,
-- the file parsed once at upload and kept as rows, the mapping of its columns
-- to metering-point fields, and the state the worker moves it through —
-- draft, checking, checked, importing, done, and attaching, the one
-- transition the customers import does not have: done ──attach──▶ attaching
-- ──(worker)──▶ done, which attaches the pending rows' points to their
-- customers from attach_start. Beside the customers import's counts, switched
-- (rows whose point switched customer), pending (rows matched to a customer
-- and not yet attached) and attached (pending rows attached since).
-- confirmed_switches is the number the person confirmed when asking for the
-- run or the attach (design D4): the worker never switches more points than
-- that. created_by_user_id is identity's user, opaque here (module
-- boundaries rule 4), and created_by_name the display name the upload
-- resolved beside it. The bytes of the file are not kept: the rows are the
-- file.
CREATE TABLE energy.imports (
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
    skipped            integer      NOT NULL DEFAULT 0,
    switched           integer      NOT NULL DEFAULT 0,
    pending            integer      NOT NULL DEFAULT 0,
    attached           integer      NOT NULL DEFAULT 0,
    confirmed_switches integer      NOT NULL DEFAULT 0,
    attach_start       timestamptz,
    stop_requested     boolean      NOT NULL DEFAULT false,
    error              text,
    created_by_user_id uuid,
    created_by_name    varchar(200),
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL,
    requested_at       timestamptz,
    started_at         timestamptz,
    finished_at        timestamptz,
    CONSTRAINT ck_imports_state CHECK (state IN ('draft', 'checking', 'checked', 'importing', 'done', 'attaching')),
    CONSTRAINT ck_imports_counts CHECK (processed >= 0 AND created >= 0 AND updated >= 0 AND failed >= 0 AND skipped >= 0
        AND switched >= 0 AND pending >= 0 AND attached >= 0 AND confirmed_switches >= 0 AND row_count >= 0)
);
-- The worker takes the oldest queued import; the page lists the newest first.
CREATE INDEX ix_imports_queue ON energy.imports (requested_at) WHERE state IN ('checking', 'importing', 'attaching');
CREATE INDEX ix_imports_recent ON energy.imports (updated_at DESC, id DESC);

-- One data row of an import: its cells as the reader left them, and — once
-- the worker has run it — what it did. outcome is NULL for a row not yet run,
-- so a run resumes at the first NULL; errors is the row's [{column, message}]
-- as in customers.import_rows. metering_point_id is the point the row created
-- or updated (none for a point a check would create: the dry run keeps no
-- id), gsrn the row's GSRN as read. customer_id is the customer the row's
-- organisation number resolved to — opaque, named through
-- contracts.CustomerDirectory — link what the row did about it (design D4),
-- switch_from the customer the point was supplied to before a switch, and
-- supply_start the date a period was or will be written from. attach_tried
-- marks a pending row the current attach has already tried, so an attach
-- interrupted resumes where it stood. The import's deletion takes the rows
-- with it.
CREATE TABLE energy.import_rows (
    import_id         integer     NOT NULL REFERENCES energy.imports (id) ON DELETE CASCADE,
    row_no            integer     NOT NULL,
    cells             text[]      NOT NULL,
    outcome           varchar(10),
    metering_point_id integer,
    gsrn              varchar(18),
    customer_id       integer,
    link              varchar(10),
    switch_from       integer,
    supply_start      timestamptz,
    attach_tried      boolean     NOT NULL DEFAULT false,
    errors            jsonb,
    PRIMARY KEY (import_id, row_no),
    CONSTRAINT ck_import_rows_outcome CHECK (outcome IS NULL OR outcome IN ('created', 'updated', 'failed', 'skipped')),
    CONSTRAINT ck_import_rows_link CHECK (link IS NULL OR link IN
        ('none', 'same', 'assigned', 'switched', 'pending', 'attached'))
);
-- The attach walks an import's pending rows.
CREATE INDEX ix_import_rows_pending ON energy.import_rows (import_id, row_no) WHERE link = 'pending';

-- +goose Down
DROP TABLE energy.import_rows;
DROP TABLE energy.imports;
