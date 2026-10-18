-- +goose Up
-- Staff's business time zone moves to the workspace (time zones design, D3):
-- 00052 added business_time_zone to identity's system settings, and Staff
-- reads it through contracts.BusinessCalendar from now on, so the column
-- 00042 gave staff.settings goes. No migration may read another module's
-- schema, so the value is not carried over; every installation runs the
-- default, Europe/Oslo, and one that did not is told here, in the migration
-- log, to set the workspace's zone once under Workspace administration.
-- +goose StatementBegin
DO $$
DECLARE
    zone text;
BEGIN
    SELECT time_zone INTO zone FROM staff.settings WHERE id = 1;
    IF zone IS NOT NULL AND zone <> 'Europe/Oslo' THEN
        RAISE WARNING 'Staff''s business time zone was %; the workspace''s is Europe/Oslo until set under Workspace administration', zone;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE staff.settings DROP COLUMN time_zone;

-- +goose Down
ALTER TABLE staff.settings
    ADD COLUMN time_zone varchar(64) NOT NULL DEFAULT 'Europe/Oslo';
