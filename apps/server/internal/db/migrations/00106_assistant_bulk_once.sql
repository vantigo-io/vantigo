-- +goose Up
-- A bulk item runs at most once (the Assistant design S17). The runner marks an
-- item in_flight, in a statement of its own that commits before the call goes
-- out; a replica that takes over a lapsed job moves every in_flight item to
-- unknown (the change may or may not have been made) and never calls it again.
-- Neither a connection nor a transaction is held across the call.
--
-- status: pending, in_flight (the call is out, or its replica died),
-- succeeded, failed, skipped, unknown. in_flight_at is when the call went out.
-- unknown counts the items marked unknown; processed = succeeded + failed +
-- skipped + unknown.
ALTER TABLE assistant.bulk_job_items DROP CONSTRAINT bulk_job_items_status_check;
ALTER TABLE assistant.bulk_job_items ADD CONSTRAINT bulk_job_items_status_check
    CHECK (status IN ('pending', 'in_flight', 'succeeded', 'failed', 'skipped', 'unknown'));
ALTER TABLE assistant.bulk_job_items ADD COLUMN in_flight_at timestamptz;
CREATE INDEX ix_bulk_job_items_unknown ON assistant.bulk_job_items (job_id, seq) WHERE status = 'unknown';

ALTER TABLE assistant.bulk_jobs ADD COLUMN unknown integer NOT NULL DEFAULT 0;

-- +goose Down
-- An item that may or may not have been made is, to the older code, a failure.
UPDATE assistant.bulk_jobs SET failed = failed + unknown, unknown = 0;
UPDATE assistant.bulk_job_items SET status = 'failed' WHERE status IN ('in_flight', 'unknown');
ALTER TABLE assistant.bulk_jobs DROP COLUMN unknown;
DROP INDEX assistant.ix_bulk_job_items_unknown;
ALTER TABLE assistant.bulk_job_items DROP COLUMN in_flight_at;
ALTER TABLE assistant.bulk_job_items DROP CONSTRAINT bulk_job_items_status_check;
ALTER TABLE assistant.bulk_job_items ADD CONSTRAINT bulk_job_items_status_check
    CHECK (status IN ('pending', 'succeeded', 'failed', 'skipped'));
