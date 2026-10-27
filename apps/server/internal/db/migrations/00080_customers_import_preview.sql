-- +goose Up
-- What a checked row would write (the check's preview): the customer's name
-- and type, its legal identity, its primary postal address and the fields
-- Brønnøysund filled in, as the check resolved the row — so the checked page
-- shows what a run will create and update before it is asked for. Written by
-- the check for a row it found would be created or updated, NULL on every
-- other row; cleared with the rest of a row's results (ClearImportRowResults)
-- and written NULL by the run, so a run's rows never show the check's.
ALTER TABLE customers.import_rows
    ADD COLUMN preview jsonb;

-- +goose Down
ALTER TABLE customers.import_rows
    DROP COLUMN preview;
