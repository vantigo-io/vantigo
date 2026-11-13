-- +goose Up
-- Shared shop-floor stations, Manufacturing's side (manufacturing phase 2
-- M131, M137). Identity owns the station; Manufacturing knows only its id.

-- The work centers a station serves (M131), set on the work center's page: the
-- station's shop floor opens on them, and every station request naming a work
-- center or a work order is checked against them. A station with none reaches
-- none. No foreign key to identity.stations: one schema per module.
CREATE TABLE manufacturing.station_work_centers (
    station_id     uuid   NOT NULL,
    work_center_id bigint NOT NULL REFERENCES manufacturing.work_centers (id) ON DELETE CASCADE,
    PRIMARY KEY (station_id, work_center_id)
);
CREATE INDEX ix_station_work_centers_center ON manufacturing.station_work_centers (work_center_id);

-- Every change of a station's work centers, with who made it and the centers
-- before and after: a record, which like an order's event keeps its actor's
-- id and name after an erasure.
CREATE TABLE manufacturing.station_work_center_changes (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    station_id    uuid        NOT NULL,
    before_ids    bigint[]    NOT NULL,
    after_ids     bigint[]    NOT NULL,
    actor_user_id uuid,
    actor_display text        NOT NULL,
    changed_at    timestamptz NOT NULL
);
CREATE INDEX ix_station_work_center_changes_station ON manufacturing.station_work_center_changes (station_id, changed_at);

-- The device a time log, a production, a check result or a deviation was
-- recorded at (M137): the station, or null from a person's own browser.
ALTER TABLE manufacturing.time_logs ADD COLUMN station_id uuid;
ALTER TABLE manufacturing.productions ADD COLUMN station_id uuid;
ALTER TABLE manufacturing.check_results ADD COLUMN station_id uuid;
ALTER TABLE manufacturing.deviations ADD COLUMN station_id uuid;

-- +goose Down
ALTER TABLE manufacturing.deviations DROP COLUMN station_id;
ALTER TABLE manufacturing.check_results DROP COLUMN station_id;
ALTER TABLE manufacturing.productions DROP COLUMN station_id;
ALTER TABLE manufacturing.time_logs DROP COLUMN station_id;
DROP TABLE manufacturing.station_work_center_changes;
DROP TABLE manufacturing.station_work_centers;
