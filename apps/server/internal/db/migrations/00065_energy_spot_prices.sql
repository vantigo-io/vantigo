-- +goose Up
-- Day-ahead prices per price area (energy consumption billing design D2): the
-- price dimension a settlement (design D5) prices a spot contract by. One row
-- is one price interval, half-open [start, end), stored at whatever
-- resolution the source gives — hourly from the public feed today, a quarter
-- of an hour when the day-ahead market's 15-minute unit reaches it — exactly
-- as consumption_intervals are. nok_per_kwh is the figure a settlement uses;
-- eur_per_kwh and exchange_rate are kept for the record when the source gives
-- them (Nord Pool prices in EUR). source is Feed (the worker) or Import (a
-- file). A price replaced with another figure for the same interval is
-- overwritten, not revisioned: the settlement built on the old figure froze
-- it, and the next settlement carries the correction. Two prices of one area
-- never overlap, so a day fetched at one resolution over a day stored at
-- another is refused rather than doubled; btree_gist is on since 00005.
CREATE TABLE energy.spot_prices (
    id            bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    price_area    varchar(4)    NOT NULL,
    "start"       timestamptz   NOT NULL,
    "end"         timestamptz   NOT NULL,
    nok_per_kwh   numeric(12,6) NOT NULL,
    eur_per_kwh   numeric(12,6),
    exchange_rate numeric(12,6),
    source        varchar(20)   NOT NULL,
    received_at   timestamptz   NOT NULL,
    CONSTRAINT ck_spot_prices_interval CHECK ("end" > "start"),
    CONSTRAINT ck_spot_prices_source CHECK (source IN ('Feed', 'Import')),
    CONSTRAINT spot_prices_no_overlap EXCLUDE USING gist (
        price_area WITH =,
        tstzrange("start", "end", '[)') WITH &&
    )
);
CREATE UNIQUE INDEX ux_spot_prices_area_start ON energy.spot_prices (price_area, "start");

-- The feed worker's record of one (area, civil day in Europe/Oslo): ok once
-- the day's prices are stored, pending while tomorrow's are not yet
-- published, missing for a past day the feed has no file for, failed for
-- anything else; attempts counts every request, next_attempt_at is when the
-- planner may ask again, last_error the sentence of the last failure, never
-- the response's body.
CREATE TABLE energy.spot_price_fetches (
    price_area      varchar(4)   NOT NULL,
    day             date         NOT NULL,
    status          varchar(20)  NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0,
    last_error      varchar(500),
    attempted_at    timestamptz,
    fetched_at      timestamptz,
    next_attempt_at timestamptz,
    PRIMARY KEY (price_area, day),
    CONSTRAINT ck_spot_price_fetches_status CHECK (status IN ('ok', 'pending', 'missing', 'failed')),
    CONSTRAINT ck_spot_price_fetches_attempts CHECK (attempts >= 0)
);

-- +goose Down
DROP TABLE energy.spot_price_fetches;
DROP TABLE energy.spot_prices;
