-- Tariffs (energy consumption billing design D3, energy tariffs plan readings
-- 1–5): the header, its versions by date and each version's components.

-- name: InsertTariff :one
-- InsertTariff creates a tariff's header, always active; created_at and
-- updated_at are the caller's clock.
INSERT INTO energy.tariffs (kind, name, description, status, created_at, updated_at)
VALUES (@kind, @name, sqlc.narg(description), 'active', @now::timestamptz, @now::timestamptz)
RETURNING id, kind, name, description, status, created_at, updated_at;

-- name: UpdateTariffHeader :one
-- UpdateTariffHeader is the PUT's header write. The kind is never changed
-- (the handler refuses a different one) and the status is the archive's.
UPDATE energy.tariffs SET name = @name, description = sqlc.narg(description), updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, kind, name, description, status, created_at, updated_at;

-- name: GetTariff :one
SELECT id, kind, name, description, status, created_at, updated_at
FROM energy.tariffs
WHERE id = @id;

-- name: GetTariffForUpdate :one
-- GetTariffForUpdate locks the header for the PUT, the archive and the
-- delete, so two edits of one tariff replace its versions one after the
-- other and a delete judges usage nobody is changing.
SELECT id, kind, name, description, status, created_at, updated_at
FROM energy.tariffs
WHERE id = @id
FOR UPDATE;

-- name: ListTariffs :many
-- ListTariffs is GET /tariffs: every tariff, narrowed by kind and status when
-- given, active first, then by kind and name; each with its version count, the
-- version valid on @today (the civil day in Europe/Oslo the caller computes)
-- and how many supply periods (of any status) and metering points name it.
SELECT t.id, t.kind, t.name, t.description, t.status, t.created_at, t.updated_at,
       (SELECT count(*) FROM energy.tariff_versions v WHERE v.tariff_id = t.id)::int AS version_count,
       cur.valid_from AS current_valid_from,
       cur.valid_to AS current_valid_to,
       (SELECT count(*) FROM energy.supply_periods sp WHERE sp.tariff_id = t.id)::int AS supply_period_count,
       (SELECT count(*) FROM energy.metering_points mp WHERE mp.grid_tariff_id = t.id)::int AS metering_point_count
FROM energy.tariffs t
LEFT JOIN LATERAL (
    SELECT v.valid_from, v.valid_to FROM energy.tariff_versions v
    WHERE v.tariff_id = t.id AND daterange(v.valid_from, v.valid_to, '[)') @> @today::date
    LIMIT 1
) cur ON true
WHERE (sqlc.narg(kind)::text IS NULL OR t.kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
ORDER BY (t.status = 'active') DESC, t.kind, t.name, t.id;

-- name: TariffUsage :one
-- TariffUsage is how many supply periods (of any status: a cancelled one
-- still holds its foreign key) and metering points name the tariff; the
-- delete's in-use judgement and the detail's usage.
SELECT (SELECT count(*) FROM energy.supply_periods sp WHERE sp.tariff_id = @id)::int AS supply_period_count,
       (SELECT count(*) FROM energy.metering_points mp WHERE mp.grid_tariff_id = @id)::int AS metering_point_count;

-- name: ArchiveTariff :one
UPDATE energy.tariffs SET status = 'archived', updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, kind, name, description, status, created_at, updated_at;

-- name: DeleteTariff :execrows
-- DeleteTariff removes an unused tariff with its versions and components
-- (ON DELETE CASCADE); a tariff still named is refused by the foreign keys'
-- RESTRICT (SQLSTATE 23001), the backstop to the handler's usage check.
DELETE FROM energy.tariffs WHERE id = @id;

-- name: InsertTariffVersion :one
INSERT INTO energy.tariff_versions (tariff_id, valid_from, valid_to)
VALUES (@tariff_id, @valid_from::date, sqlc.narg(valid_to)::date)
RETURNING id, tariff_id, valid_from, valid_to;

-- name: DeleteTariffVersions :exec
-- DeleteTariffVersions is the PUT's replace: every version of the tariff, and
-- their components with them. Nothing outside the tariff points at a version
-- or a component.
DELETE FROM energy.tariff_versions WHERE tariff_id = @tariff_id;

-- name: ListTariffVersions :many
SELECT id, tariff_id, valid_from, valid_to
FROM energy.tariff_versions
WHERE tariff_id = @tariff_id
ORDER BY valid_from;

-- name: InsertTariffComponent :exec
INSERT INTO energy.tariff_components (
    version_id, position, kind, name, rate, rate_night, day_from, day_to, weekdays_only, tiers, rate_reduced, rate_business
) VALUES (
    @version_id, @position, @kind, @name, @rate, @rate_night, sqlc.narg(day_from)::smallint, sqlc.narg(day_to)::smallint,
    @weekdays_only, @tiers, @rate_reduced, @rate_business
);

-- name: ListTariffComponents :many
-- ListTariffComponents is every component of the tariff's versions, grouped
-- by version and in position order within it.
SELECT c.id, c.version_id, c.position, c.kind, c.name, c.rate, c.rate_night, c.day_from, c.day_to,
       c.weekdays_only, c.tiers, c.rate_reduced, c.rate_business
FROM energy.tariff_components c
JOIN energy.tariff_versions v ON v.id = c.version_id
WHERE v.tariff_id = @tariff_id
ORDER BY c.version_id, c.position;

-- name: ListTariffNamesByIDs :many
-- ListTariffNamesByIDs answers the tariffs a page of supply periods or
-- metering points names, so a response carries the name beside the id
-- without a join in every row query; the assignment check reads kind and
-- status from the same rows.
SELECT id, kind, name, status
FROM energy.tariffs
WHERE id = ANY(@ids::int[]);
