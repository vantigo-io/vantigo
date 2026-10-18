-- +goose Up
-- A skipped row of an import: a row that repeats an earlier row's legal
-- identity with every mapped cell equal to that row's — a supplier's export
-- lists one customer per metering point, so a customer with seventeen points
-- is seventeen identical rows. The first of them is the customer; the rest are
-- skipped, not failed (the duplicate pass, import_worker.go). skipped is the
-- count beside created, updated and failed, recounted from the rows as they
-- are.
ALTER TABLE customers.imports
    ADD COLUMN skipped integer NOT NULL DEFAULT 0;
ALTER TABLE customers.imports
    DROP CONSTRAINT ck_imports_counts,
    ADD CONSTRAINT ck_imports_counts CHECK (processed >= 0 AND created >= 0 AND updated >= 0 AND failed >= 0 AND skipped >= 0 AND row_count >= 0);

ALTER TABLE customers.import_rows
    DROP CONSTRAINT ck_import_rows_outcome,
    ADD CONSTRAINT ck_import_rows_outcome CHECK (outcome IS NULL OR outcome IN ('created', 'updated', 'failed', 'skipped'));

-- +goose Down
-- A skipped row goes back to not run: the next check or run reads it again.
UPDATE customers.import_rows SET outcome = NULL, errors = NULL WHERE outcome = 'skipped';
ALTER TABLE customers.import_rows
    DROP CONSTRAINT ck_import_rows_outcome,
    ADD CONSTRAINT ck_import_rows_outcome CHECK (outcome IS NULL OR outcome IN ('created', 'updated', 'failed'));

ALTER TABLE customers.imports
    DROP CONSTRAINT ck_imports_counts,
    ADD CONSTRAINT ck_imports_counts CHECK (processed >= 0 AND created >= 0 AND updated >= 0 AND failed >= 0 AND row_count >= 0);
ALTER TABLE customers.imports
    DROP COLUMN skipped;
