-- +goose Up
-- Energy's schema, single-tenant (every tenant_id from the .NET EF
-- configuration is dropped, along with the composite keys/indexes it was
-- part of; what survives is the rest of each key): metering points, the
-- meters installed at them, the supply periods that assign them to a
-- customer, and the (range-partitioned) consumption intervals recorded
-- against them. See docs/superpowers/specs/2026-09-12-energy-inventory.md
-- §3.
CREATE SCHEMA energy;

-- btree_gist supplies the "=" operator classes the supply_periods exclusion
-- constraint below needs for its scalar (metering_point_id) term: a GiST
-- exclusion index can mix "=" and "&&" terms in one index only when the
-- scalar type has a GiST-compatible "=" operator class, which core Postgres
-- does not ship for plain integers on its own (energy inventory §3.2).
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Address is owned in-table (street_address/postal_code/city/country_code),
-- mirroring the .NET EF owned entity (OwnsOne) rather than a separate
-- addresses table (energy inventory §2.1b/§3.1) — every metering point has
-- exactly one address and it is never shared, so a join table would only
-- add a lookup for no benefit. All four address columns are NOT NULL: the
-- contract requires address on create, and Address.ValidateCountry always
-- produces a 2-letter country code (defaulting to "NO"), never a null one.
CREATE TABLE energy.metering_points (
    id                              integer          GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    gsrn                            varchar(18)      NOT NULL,
    street_address                  varchar(200)     NOT NULL,
    postal_code                     varchar(16)      NOT NULL,
    city                            varchar(100)     NOT NULL,
    country_code                    varchar(2)       NOT NULL,
    price_area                      varchar(4)       NOT NULL,
    grid_area                       varchar(64),
    expected_annual_consumption_kwh numeric(14,3),
    latitude                        double precision,
    longitude                       double precision,
    -- No DB default: .NET's ConnectionStatus defaults to "New" in the
    -- domain constructor when unset, not at the database layer (energy
    -- inventory §2.1) — the Go port keeps that an application-level
    -- default, the same way it keeps CreatedAt/UpdatedAt application-
    -- stamped columns below rather than DB defaults/triggers.
    connection_status               varchar(20)      NOT NULL,
    created_at                      timestamptz      NOT NULL,
    updated_at                      timestamptz      NOT NULL
);
CREATE UNIQUE INDEX ux_metering_points_gsrn ON energy.metering_points (gsrn);

-- "Current"/active meter = the one row per metering point with
-- removed_at IS NULL; a partial unique index is the only thing enforcing
-- "at most one active meter" (energy inventory §2.2/§3) — there is no
-- in-app invariant object preventing a race outside this index. Any number
-- of removed (non-null removed_at) meters may exist per metering point.
CREATE TABLE energy.meters (
    id                 integer     GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    metering_point_id  integer     NOT NULL REFERENCES energy.metering_points (id) ON DELETE CASCADE,
    meter_number       varchar(64) NOT NULL,
    installed_at       timestamptz NOT NULL,
    removed_at         timestamptz
);
CREATE UNIQUE INDEX ux_meters_metering_point_id_active ON energy.meters (metering_point_id) WHERE removed_at IS NULL;
CREATE INDEX ix_meters_metering_point_id ON energy.meters (metering_point_id);

-- customer_id is deliberately NOT a foreign key: Energy resolves customers
-- through contracts.CustomerDirectory (internal/contracts), never by
-- reading the customers module's schema directly (Global Constraints, "one
-- schema per module"; energy inventory §1.2 — Energy trusts customerId as
-- an opaque id and only the two supply-period write endpoints ever validate
-- it, through the directory, not a DB constraint). A cross-schema FK would
-- also violate internal/db/schema_test.go's
-- TestNoModuleReferencesAnotherModulesSchema.
CREATE TABLE energy.supply_periods (
    id                 integer     GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    metering_point_id  integer     NOT NULL REFERENCES energy.metering_points (id) ON DELETE CASCADE,
    customer_id        integer     NOT NULL,
    start              timestamptz NOT NULL,
    "end"              timestamptz,
    status             varchar(20) NOT NULL
);
CREATE INDEX ix_supply_periods_metering_point_id ON energy.supply_periods (metering_point_id);

-- The GiST exclusion constraint that actually decides concurrent-create
-- races (energy inventory §3.2, §5) — the application's own overlap
-- pre-checks are a friendly advisory layer only. For a fixed
-- metering_point_id, no two non-Cancelled rows may have overlapping
-- [start, end) ranges, end IS NULL treated as +infinity. Quoted verbatim
-- from the dispatch correction to this task's brief (the brief's own
-- paraphrase does not parse: "end" is a reserved word and needs quoting,
-- and the infinity literal needs its explicit cast); the single-tenant port
-- drops only "tenant_id WITH =" from the .NET original and keeps
-- everything else, including the partial WHERE.
ALTER TABLE energy.supply_periods ADD CONSTRAINT supply_periods_no_overlap
    EXCLUDE USING gist (
        metering_point_id WITH =,
        tstzrange(start, COALESCE("end", 'infinity'::timestamptz), '[)') WITH &&
    ) WHERE (status <> 'Cancelled');

-- Range-partitioned on start (energy inventory §3.3): Postgres requires a
-- partitioned table's every unique/primary-key index to include the
-- partition key, which is why start is part of the primary key here
-- alongside id, not just id alone — a plain PRIMARY KEY (id) would be
-- rejected at creation. "end" is NOT NULL (unlike supply_periods.end):
-- every consumption interval has a concrete end, only supply periods are
-- ever open-ended. The self-referencing supersedes_id/supersedes_start FK
-- is the revision chain a manual entry with an identical (start, end) to an
-- existing current row creates (energy inventory §2.4); ON DELETE RESTRICT
-- mirrors .NET's DeleteBehavior.Restrict, so a superseded row can never be
-- deleted out from under the row that supersedes it.
CREATE TABLE energy.consumption_intervals (
    id                bigint        GENERATED BY DEFAULT AS IDENTITY,
    metering_point_id integer       NOT NULL REFERENCES energy.metering_points (id) ON DELETE CASCADE,
    start             timestamptz   NOT NULL,
    "end"             timestamptz   NOT NULL,
    quantity_kwh      numeric(14,3) NOT NULL,
    quality           varchar(20)   NOT NULL,
    source            varchar(20)   NOT NULL,
    received_at       timestamptz   NOT NULL,
    is_current        boolean       NOT NULL DEFAULT true,
    supersedes_id     bigint,
    supersedes_start  timestamptz,
    PRIMARY KEY (id, start),
    FOREIGN KEY (supersedes_id, supersedes_start) REFERENCES energy.consumption_intervals (id, start) ON DELETE RESTRICT
) PARTITION BY RANGE (start);
CREATE INDEX ix_consumption_intervals_metering_point_id ON energy.consumption_intervals (metering_point_id);
CREATE INDEX ix_consumption_intervals_supersedes ON energy.consumption_intervals (supersedes_id, supersedes_start);
-- The only DB-level protection consumption intervals get: a unique index on
-- the exact (metering_point_id, start, end) tuple among current rows, not
-- an overlap-guarding exclusion constraint (energy inventory §2.4 — this is
-- deliberate, not an oversight: .NET never had overlap protection here
-- either). start is part of the index only because the partition key must
-- be; two rows sharing one (metering_point_id, start, end) always share
-- start, so they are always in the same partition and this partial unique
-- index enforces true cross-partition uniqueness despite being declared on
-- a partitioned table.
CREATE UNIQUE INDEX ux_consumption_intervals_metering_point_id_start_end
    ON energy.consumption_intervals (metering_point_id, start, "end") WHERE is_current;

-- Monthly partitions, created on demand (energy inventory §3.3, quoted
-- verbatim from the .NET migration). Every manual-consumption write calls
-- this first so the target month's partition is guaranteed to exist before
-- the INSERT lands. Unlike the .NET original, this is not SECURITY DEFINER:
-- the Go port runs every query as one database role (Global Constraints;
-- no per-tenant/least-privilege runtime role exists here the way .NET's
-- RLS-era role did), so there is no privilege gap between "can INSERT" and
-- "can CREATE TABLE" for this function to bridge.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION energy.ensure_consumption_partition(partition_month date)
RETURNS void LANGUAGE plpgsql AS $function$
DECLARE
    partition_start date := date_trunc('month', partition_month)::date;
    partition_end date := (partition_start + interval '1 month')::date;
    partition_name text := format('consumption_intervals_%s', to_char(partition_start, 'YYYY_MM'));
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS energy.%I PARTITION OF energy.consumption_intervals FOR VALUES FROM (%L) TO (%L)',
        partition_name, partition_start, partition_end);
END;
$function$;
-- +goose StatementEnd

-- Pre-create partitions for [-12, +12] months around "now" (energy
-- inventory §3.3), same window the .NET initial migration seeds so a fresh
-- installation can write consumption for the current and nearby months
-- before any write-path call to ensure_consumption_partition ever runs.
-- +goose StatementBegin
DO $$
DECLARE
    i integer;
BEGIN
    FOR i IN -12..12 LOOP
        PERFORM energy.ensure_consumption_partition((date_trunc('month', now()) + (i || ' months')::interval)::date);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP SCHEMA energy CASCADE;
