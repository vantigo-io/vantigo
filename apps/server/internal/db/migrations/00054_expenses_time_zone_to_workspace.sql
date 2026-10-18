-- +goose Up
-- Expenses' business time zone moves to the workspace (time zones design,
-- D3), as Staff's does in 00053: 00052 added business_time_zone to identity's
-- system settings, Expenses reads it through contracts.BusinessCalendar, and
-- the column 00013 gave expenses.settings goes. The value is not carried
-- over — no migration reads another module's schema — and an installation
-- that had changed it is told so in the migration log.
-- +goose StatementBegin
DO $$
DECLARE
    zone text;
BEGIN
    SELECT time_zone INTO zone FROM expenses.settings WHERE id = 1;
    IF zone IS NOT NULL AND zone <> 'Europe/Oslo' THEN
        RAISE WARNING 'Expenses'' business time zone was %; the workspace''s is Europe/Oslo until set under Workspace administration', zone;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE expenses.settings DROP COLUMN time_zone;

-- +goose Down
ALTER TABLE expenses.settings
    ADD COLUMN time_zone varchar(64) NOT NULL DEFAULT 'Europe/Oslo';
