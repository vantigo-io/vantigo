-- +goose Up
-- Tariffs (energy consumption billing design D3): what a settlement (design
-- D5) prices consumption by. A power tariff is the supplier's contract with
-- the customer, on the supply period; a grid tariff is the installation's
-- own, on the metering point, for the points it is the grid operator of. A
-- tariff is a header with versions by date, and a version is a list of
-- components, each of one kind. A price change is a new version from a day;
-- the old one stays for the periods it priced. A tariff in use is archived,
-- never deleted (the foreign keys below RESTRICT the delete), and an archived
-- tariff still prices the periods it covers.
CREATE TABLE energy.tariffs (
    id          integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind        varchar(10)  NOT NULL,
    name        varchar(120) NOT NULL,
    description varchar(500),
    status      varchar(10)  NOT NULL DEFAULT 'active',
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL,
    CONSTRAINT ck_tariffs_kind CHECK (kind IN ('power', 'grid')),
    CONSTRAINT ck_tariffs_status CHECK (status IN ('active', 'archived'))
);

-- A version applies on the civil days [valid_from, valid_to) in the point's
-- market time zone, valid_to NULL open-ended. Two versions of one tariff never
-- overlap; they may leave a gap, which prices nothing. btree_gist is on since
-- 00005.
CREATE TABLE energy.tariff_versions (
    id         integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    tariff_id  integer NOT NULL REFERENCES energy.tariffs (id) ON DELETE CASCADE,
    valid_from date    NOT NULL,
    valid_to   date,
    CONSTRAINT ck_tariff_versions_dates CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT tariff_versions_no_overlap EXCLUDE USING gist (
        tariff_id WITH =,
        daterange(valid_from, valid_to, '[)') WITH &&
    )
);
CREATE INDEX ix_tariff_versions_tariff_id ON energy.tariff_versions (tariff_id);

-- One component is one priced element of a version, in the order the lines
-- print. The columns a kind does not use are NULL; which kind uses which,
-- which kinds a power or a grid tariff may hold and the shape of tiers
-- ([{"fromKw": 0, "perMonth": 135}, …], ascending from 0) are validated in Go
-- before the write (tariffs_validate.go). The CHECKs keep only what a row can
-- state alone. Rates are NOK per the kind's unit: per kWh, per month, or per
-- year for enova_levy's rate_business.
CREATE TABLE energy.tariff_components (
    id            integer       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    version_id    integer       NOT NULL REFERENCES energy.tariff_versions (id) ON DELETE CASCADE,
    position      smallint      NOT NULL,
    kind          varchar(20)   NOT NULL,
    name          varchar(80)   NOT NULL,
    rate          numeric(12,6),
    rate_night    numeric(12,6),
    day_from      smallint,
    day_to        smallint,
    weekdays_only boolean       NOT NULL DEFAULT false,
    tiers         jsonb,
    rate_reduced  numeric(12,6),
    rate_business numeric(12,6),
    CONSTRAINT ux_tariff_components_position UNIQUE (version_id, position),
    CONSTRAINT ck_tariff_components_kind CHECK (kind IN (
        'spot_energy', 'fixed_energy', 'markup', 'monthly_fee',
        'grid_energy', 'grid_capacity', 'grid_fixed', 'electricity_tax', 'enova_levy'
    )),
    CONSTRAINT ck_tariff_components_position CHECK (position >= 0),
    CONSTRAINT ck_tariff_components_rates CHECK (
        (rate IS NULL OR rate >= 0) AND (rate_night IS NULL OR rate_night >= 0)
        AND (rate_reduced IS NULL OR rate_reduced >= 0) AND (rate_business IS NULL OR rate_business >= 0)
    ),
    CONSTRAINT ck_tariff_components_day CHECK (
        (day_from IS NULL AND day_to IS NULL)
        OR (day_from IS NOT NULL AND day_to IS NOT NULL AND day_from >= 0 AND day_from < day_to AND day_to <= 24)
    ),
    CONSTRAINT ck_tariff_components_tiers CHECK (tiers IS NULL OR jsonb_typeof(tiers) = 'array')
);

-- The billing facts (design D3, D4). A supply period's power tariff (NULL:
-- the settlement is blocked no_tariff) and its billing cadence; a metering
-- point's grid tariff (NULL: the grid company bills grid services itself),
-- whether it is supplied for household use (mvaf § 6-6-1, set by hand) and
-- its electricity-tax class. The VAT treatment is derived from household and
-- the point's address, never stored.
ALTER TABLE energy.supply_periods
    ADD COLUMN tariff_id integer REFERENCES energy.tariffs (id) ON DELETE RESTRICT,
    ADD COLUMN cadence varchar(10) NOT NULL DEFAULT 'monthly',
    ADD CONSTRAINT ck_supply_periods_cadence CHECK (cadence IN ('monthly', 'quarterly', 'yearly'));
CREATE INDEX ix_supply_periods_tariff_id ON energy.supply_periods (tariff_id) WHERE tariff_id IS NOT NULL;

ALTER TABLE energy.metering_points
    ADD COLUMN grid_tariff_id integer REFERENCES energy.tariffs (id) ON DELETE RESTRICT,
    ADD COLUMN household boolean NOT NULL DEFAULT false,
    ADD COLUMN electricity_tax_class varchar(10) NOT NULL DEFAULT 'ordinary',
    ADD CONSTRAINT ck_metering_points_electricity_tax_class CHECK (electricity_tax_class IN ('ordinary', 'reduced', 'exempt'));
CREATE INDEX ix_metering_points_grid_tariff_id ON energy.metering_points (grid_tariff_id) WHERE grid_tariff_id IS NOT NULL;

-- +goose Down
DROP INDEX energy.ix_metering_points_grid_tariff_id;
ALTER TABLE energy.metering_points
    DROP CONSTRAINT ck_metering_points_electricity_tax_class,
    DROP COLUMN electricity_tax_class,
    DROP COLUMN household,
    DROP COLUMN grid_tariff_id;
DROP INDEX energy.ix_supply_periods_tariff_id;
ALTER TABLE energy.supply_periods
    DROP CONSTRAINT ck_supply_periods_cadence,
    DROP COLUMN cadence,
    DROP COLUMN tariff_id;
DROP TABLE energy.tariff_components;
DROP TABLE energy.tariff_versions;
DROP TABLE energy.tariffs;
