-- name: InsertMeteringPoint :one
-- InsertMeteringPoint creates a metering point row
-- (CreateMeteringPointEndpoint.cs:21-24). created_at and updated_at are the
-- same instant on creation, supplied by the caller from Deps.Clock().
INSERT INTO energy.metering_points (
    gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
    expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at,
    grid_tariff_id, household, electricity_tax_class
) VALUES (
    @gsrn, @street_address, @postal_code, @city, @country_code, @price_area, @grid_area,
    @expected_annual_consumption_kwh, @latitude, @longitude, @connection_status, @now::timestamptz, @now::timestamptz,
    sqlc.narg(grid_tariff_id)::int, @household, @electricity_tax_class
)
RETURNING id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
          expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at,
          grid_tariff_id, household, electricity_tax_class;

-- name: UpdateMeteringPoint :one
-- UpdateMeteringPoint applies PUT /{id}'s validated fields
-- (UpdateMeteringPointEndpoint.cs:20-29). updated_at is whatever the caller
-- computes it should be, from Deps.Clock(). The billing facts (energy
-- consumption billing design D3, D4) are replaced with the rest: the PUT is a
-- full replace.
UPDATE energy.metering_points
SET gsrn = @gsrn, street_address = @street_address, postal_code = @postal_code, city = @city,
    country_code = @country_code, price_area = @price_area, grid_area = @grid_area,
    expected_annual_consumption_kwh = @expected_annual_consumption_kwh, latitude = @latitude,
    longitude = @longitude, connection_status = @connection_status, updated_at = @updated_at::timestamptz,
    grid_tariff_id = sqlc.narg(grid_tariff_id)::int, household = @household, electricity_tax_class = @electricity_tax_class
WHERE id = @id
RETURNING id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
          expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at,
          grid_tariff_id, household, electricity_tax_class;

-- name: GetMeteringPointByID :one
-- GetMeteringPointByID is GetMeteringPointEndpoint's single lookup, and the
-- follow-up read Create/Update use to build their own response bodies
-- (there is no in-app tracked entity to read back from the way EF's
-- SaveChangesAsync leaves one).
SELECT id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
       expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at,
       grid_tariff_id, household, electricity_tax_class
FROM energy.metering_points
WHERE id = @id;

-- name: MeteringPointExists :one
SELECT EXISTS (SELECT 1 FROM energy.metering_points WHERE id = @id);

-- name: GsrnExists :one
-- GsrnExists is CreateMeteringPointEndpoint's duplicate-GSRN pre-check (:18).
SELECT EXISTS (SELECT 1 FROM energy.metering_points WHERE gsrn = @gsrn);

-- name: GsrnExistsExcludingID :one
-- GsrnExistsExcludingID is UpdateMeteringPointEndpoint's duplicate-GSRN
-- pre-check, excluding the row being updated (:18).
SELECT EXISTS (SELECT 1 FROM energy.metering_points WHERE id <> @id AND gsrn = @gsrn);

-- name: CountMeteringPoints :one
-- CountMeteringPoints is GetMeteringPointsEndpoint's total row count over
-- the same filter ListMeteringPoints applies (:21-27): an ILIKE match on
-- the GSRN, the city, the street address, or the *current* meter's number
-- only (removed_at IS NULL) — a removed meter's number is never searched.
-- without_customer_at is the Without customer filter (energy import design
-- D9): when set, only the points with no Active supply period covering that
-- instant (start at or before it, no end or an end after it).
SELECT count(*)
FROM energy.metering_points mp
LEFT JOIN energy.meters active ON active.metering_point_id = mp.id AND active.removed_at IS NULL
WHERE (sqlc.narg(search_pattern)::text IS NULL OR (
    mp.gsrn ILIKE sqlc.narg(search_pattern)::text OR
    mp.city ILIKE sqlc.narg(search_pattern)::text OR
    mp.street_address ILIKE sqlc.narg(search_pattern)::text OR
    active.meter_number ILIKE sqlc.narg(search_pattern)::text
))
AND (sqlc.narg(without_customer_at)::timestamptz IS NULL OR NOT EXISTS (
    SELECT 1 FROM energy.supply_periods p
    WHERE p.metering_point_id = mp.id AND p.status = 'Active'
      AND p.start <= sqlc.narg(without_customer_at)::timestamptz
      AND (p."end" IS NULL OR p."end" > sqlc.narg(without_customer_at)::timestamptz)
));

-- name: ListMeteringPoints :many
-- ListMeteringPoints is GetMeteringPointsEndpoint's one page, ordered by id
-- (:30), each row carrying its current meter's number (or NULL if the
-- metering point has none) the way MeteringPointResponse.FromDomain reads
-- it off the already-loaded Meters collection (Dtos/MeteringPointResponse.cs:20).
SELECT mp.id, mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code, mp.price_area, mp.grid_area,
       mp.expected_annual_consumption_kwh, mp.latitude, mp.longitude, mp.connection_status, mp.created_at, mp.updated_at,
       mp.grid_tariff_id, mp.household, mp.electricity_tax_class,
       active.meter_number AS active_meter_number
FROM energy.metering_points mp
LEFT JOIN energy.meters active ON active.metering_point_id = mp.id AND active.removed_at IS NULL
WHERE (sqlc.narg(search_pattern)::text IS NULL OR (
    mp.gsrn ILIKE sqlc.narg(search_pattern)::text OR
    mp.city ILIKE sqlc.narg(search_pattern)::text OR
    mp.street_address ILIKE sqlc.narg(search_pattern)::text OR
    active.meter_number ILIKE sqlc.narg(search_pattern)::text
))
AND (sqlc.narg(without_customer_at)::timestamptz IS NULL OR NOT EXISTS (
    SELECT 1 FROM energy.supply_periods p
    WHERE p.metering_point_id = mp.id AND p.status = 'Active'
      AND p.start <= sqlc.narg(without_customer_at)::timestamptz
      AND (p."end" IS NULL OR p."end" > sqlc.narg(without_customer_at)::timestamptz)
))
ORDER BY mp.id
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: MeteringPointByGsrnForUpdate :one
-- MeteringPointByGsrnForUpdate is the import's match (energy import design
-- D3): the point holding a GSRN, locked for the row's transaction so nothing
-- a colleague writes lands between the read and the row's write. FOR NO KEY
-- UPDATE, not FOR UPDATE: the import never changes the point's key, and the
-- weaker lock leaves the FOR KEY SHARE a supply period's, a meter's or a
-- reading's foreign key takes on the point free — so an endpoint that holds
-- the point's supply-period advisory lock and inserts a period does not wait
-- on a row that waits on that advisory lock. An update, a GSRN change or a
-- delete of the point still waits, as before.
SELECT id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
       expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at,
       grid_tariff_id, household, electricity_tax_class
FROM energy.metering_points
WHERE gsrn = @gsrn
FOR NO KEY UPDATE;
