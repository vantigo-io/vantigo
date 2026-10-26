-- +goose Up
-- Spot prices from the shared spot-price service (spot-price service design
-- S2): a third source beside the public feed and the file, and what a row
-- records of where its figures came from. state is final or preliminary —
-- the service writes tomorrow as soon as the auction publishes it, and Nord
-- Pool's NOK figure stays preliminary until the day's exchange rate is final;
-- a settlement prices only from final rows. Every row stored before this
-- migration is final: the public feed publishes a day once, and a file is the
-- operator's say-so. upstream is the service's own source (nordpool_portal,
-- entsoe, hks) and is not CHECKed, since the service may add one; rate_source
-- is whose EUR/NOK rate gave the NOK figure; upstream_version is the
-- service's version of the interval. All three are null for a Feed or an
-- Import row.
ALTER TABLE energy.spot_prices DROP CONSTRAINT ck_spot_prices_source;
ALTER TABLE energy.spot_prices
    ADD CONSTRAINT ck_spot_prices_source CHECK (source IN ('Feed', 'Import', 'Service')),
    ADD COLUMN state varchar(12) NOT NULL DEFAULT 'final',
    ADD COLUMN upstream varchar(20),
    ADD COLUMN rate_source varchar(20),
    ADD COLUMN upstream_version integer,
    ADD CONSTRAINT ck_spot_prices_state CHECK (state IN ('final', 'preliminary'));

-- The service sync's walk back through an area's history (design S5): one
-- row per area, cursor_day the oldest civil day (Europe/Oslo) read so far —
-- null before the first window —, floor_day the oldest day the service held
-- for the area when last asked, days_skipped the days a window held
-- incompletely, without a NOK figure, or over an imported day at another
-- resolution (left for a file), and the backoff of a failed request. The
-- walk is complete when cursor_day reaches floor_day; a floor that moves
-- earlier resumes it. live_through is the last day of the most recent live
-- window read whole for the area (tomorrow at that read), null before the
-- first: when the worker was away longer than the live window reaches back,
-- the days after it are read forward as history before the walk goes on.
-- active is whether the service followed the area when /api/v1/areas last
-- answered: a row is kept, with what it copied, after the service stops
-- following its area.
CREATE TABLE energy.spot_price_history (
    price_area      varchar(4)   PRIMARY KEY,
    cursor_day      date,
    floor_day       date,
    live_through    date,
    active          boolean      NOT NULL DEFAULT true,
    days_skipped    integer      NOT NULL DEFAULT 0,
    attempts        integer      NOT NULL DEFAULT 0,
    last_error      varchar(500),
    next_attempt_at timestamptz,
    updated_at      timestamptz  NOT NULL,
    CONSTRAINT ck_spot_price_history_counts CHECK (days_skipped >= 0 AND attempts >= 0)
);

-- +goose Down
DROP TABLE energy.spot_price_history;
DELETE FROM energy.spot_prices WHERE source = 'Service';
ALTER TABLE energy.spot_prices
    DROP CONSTRAINT ck_spot_prices_state,
    DROP COLUMN upstream_version,
    DROP COLUMN rate_source,
    DROP COLUMN upstream,
    DROP COLUMN state,
    DROP CONSTRAINT ck_spot_prices_source;
ALTER TABLE energy.spot_prices
    ADD CONSTRAINT ck_spot_prices_source CHECK (source IN ('Feed', 'Import'));
