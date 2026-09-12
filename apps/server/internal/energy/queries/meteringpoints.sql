-- name: InsertMeteringPoint :one
-- InsertMeteringPoint creates a metering point row
-- (CreateMeteringPointEndpoint.cs:21-24). created_at and updated_at are the
-- same instant on creation, supplied by the caller from Deps.Clock().
INSERT INTO energy.metering_points (
    gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
    expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at
) VALUES (
    @gsrn, @street_address, @postal_code, @city, @country_code, @price_area, @grid_area,
    @expected_annual_consumption_kwh, @latitude, @longitude, @connection_status, @now::timestamptz, @now::timestamptz
)
RETURNING id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
          expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at;

-- name: UpdateMeteringPoint :one
-- UpdateMeteringPoint applies PUT /{id}'s validated fields
-- (UpdateMeteringPointEndpoint.cs:20-29). updated_at is whatever the caller
-- computes it should be, from Deps.Clock().
UPDATE energy.metering_points
SET gsrn = @gsrn, street_address = @street_address, postal_code = @postal_code, city = @city,
    country_code = @country_code, price_area = @price_area, grid_area = @grid_area,
    expected_annual_consumption_kwh = @expected_annual_consumption_kwh, latitude = @latitude,
    longitude = @longitude, connection_status = @connection_status, updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
          expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at;

-- name: GetMeteringPointByID :one
-- GetMeteringPointByID is GetMeteringPointEndpoint's single lookup, and the
-- follow-up read Create/Update use to build their own response bodies
-- (there is no in-app tracked entity to read back from the way EF's
-- SaveChangesAsync leaves one).
SELECT id, gsrn, street_address, postal_code, city, country_code, price_area, grid_area,
       expected_annual_consumption_kwh, latitude, longitude, connection_status, created_at, updated_at
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
SELECT count(*)
FROM energy.metering_points mp
LEFT JOIN energy.meters active ON active.metering_point_id = mp.id AND active.removed_at IS NULL
WHERE (sqlc.narg(search_pattern)::text IS NULL OR (
    mp.gsrn ILIKE sqlc.narg(search_pattern)::text OR
    mp.city ILIKE sqlc.narg(search_pattern)::text OR
    mp.street_address ILIKE sqlc.narg(search_pattern)::text OR
    active.meter_number ILIKE sqlc.narg(search_pattern)::text
));

-- name: ListMeteringPoints :many
-- ListMeteringPoints is GetMeteringPointsEndpoint's one page, ordered by id
-- (:30), each row carrying its current meter's number (or NULL if the
-- metering point has none) the way MeteringPointResponse.FromDomain reads
-- it off the already-loaded Meters collection (Dtos/MeteringPointResponse.cs:20).
SELECT mp.id, mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code, mp.price_area, mp.grid_area,
       mp.expected_annual_consumption_kwh, mp.latitude, mp.longitude, mp.connection_status, mp.created_at, mp.updated_at,
       active.meter_number AS active_meter_number
FROM energy.metering_points mp
LEFT JOIN energy.meters active ON active.metering_point_id = mp.id AND active.removed_at IS NULL
WHERE (sqlc.narg(search_pattern)::text IS NULL OR (
    mp.gsrn ILIKE sqlc.narg(search_pattern)::text OR
    mp.city ILIKE sqlc.narg(search_pattern)::text OR
    mp.street_address ILIKE sqlc.narg(search_pattern)::text OR
    active.meter_number ILIKE sqlc.narg(search_pattern)::text
))
ORDER BY mp.id
LIMIT @page_size::int OFFSET @row_offset::int;
