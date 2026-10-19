-- Zones (calendar design D2, D5): the second of the two checks every zone
-- name this module takes is held to — an appointment's time_zone and a
-- window's timeZone — identity's workspace zone check restated, since no
-- module reads another's queries.

-- name: ResolveTimeZone :one
-- ResolveTimeZone asks Postgres what a zone name means, at a winter instant
-- and a summer one, for the caller to compare with Go's reading of it: the
-- two carry their own tzdata, and Postgres resolves
-- pg_timezone_abbrevs before pg_timezone_names, so 'CET' is a fixed +01:00 to
-- it and a zone with summer time to Go. A name Postgres does not know raises
-- 22023.
WITH probe AS (
    SELECT @name::text AS zone, @winter::timestamptz AS winter, @summer::timestamptz AS summer
)
SELECT
    EXTRACT(EPOCH FROM ((winter AT TIME ZONE zone) - (winter AT TIME ZONE 'UTC')))::bigint AS winter_offset,
    EXTRACT(EPOCH FROM ((summer AT TIME ZONE zone) - (summer AT TIME ZONE 'UTC')))::bigint AS summer_offset
FROM probe;
