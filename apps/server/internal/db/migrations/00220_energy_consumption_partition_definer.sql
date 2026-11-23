-- +goose Up
-- Energy's partition upkeep under the least-privilege runtime role.
--
-- The installation guide (admin/installation.md, Database roles) has the
-- server connect as a role that owns nothing and holds table DML only.
-- energy.ensure_consumption_partition, which every consumption write calls
-- first, runs CREATE TABLE ... PARTITION OF, and a function without
-- SECURITY DEFINER runs as its caller, so under that role every consumption
-- write failed with "permission denied for schema energy": CREATE TABLE ...
-- IF NOT EXISTS checks the schema's CREATE privilege before it looks for the
-- table, so a month the baseline made failed too. Only an installation
-- running the server as the database's owner worked. 00221 does the same for
-- identity's request log.
--
-- The function is now SECURITY DEFINER, so it runs as its owner, the role
-- that ran the migrations and owns energy.consumption_intervals (only a
-- table's owner may attach a partition to it). It pins search_path to
-- pg_catalog with the session's temporary schema last, names every object
-- schema-qualified, and accepts only a month from 1990 to 2099, so the most
-- a caller can do is make a bounded number of partitions of the one table.
-- The partition's name is derived from the month, never taken from the
-- caller. A month that has its partition returns before any DDL.
--
-- EXECUTE is revoked from PUBLIC and granted to every role, other than the
-- owner, that can already insert into the table: the runtime role, when the
-- installation made it before migrating, as deploy/compose's init script
-- does. A role made afterwards gets it with the guide's GRANT EXECUTE.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION energy.ensure_consumption_partition(partition_month date)
RETURNS void LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    partition_start date;
    partition_end date;
    partition_name text;
BEGIN
    IF partition_month IS NULL OR partition_month < date '1990-01-01' OR partition_month >= date '2100-01-01' THEN
        RAISE EXCEPTION 'energy.ensure_consumption_partition: month % is outside 1990-2099', partition_month
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    partition_start := date_trunc('month', partition_month)::date;
    partition_end := (partition_start + interval '1 month')::date;
    partition_name := format('consumption_intervals_%s', to_char(partition_start, 'YYYY_MM'));
    IF to_regclass(format('energy.%I', partition_name)) IS NOT NULL THEN
        RETURN;
    END IF;
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS energy.%I PARTITION OF energy.consumption_intervals FOR VALUES FROM (%L) TO (%L)',
        partition_name, partition_start, partition_end);
END;
$function$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION energy.ensure_consumption_partition(date) FROM PUBLIC;

-- +goose StatementBegin
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT rolname FROM pg_catalog.pg_roles
        WHERE rolname <> current_user AND rolname NOT LIKE 'pg\_%'
          AND has_table_privilege(oid, 'energy.consumption_intervals', 'INSERT')
    LOOP
        EXECUTE format('GRANT EXECUTE ON FUNCTION energy.ensure_consumption_partition(date) TO %I', r.rolname);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Back to invoker's rights and PUBLIC's EXECUTE; the argument check stays in
-- the body, which refuses nothing a caller of 00005's did.
GRANT EXECUTE ON FUNCTION energy.ensure_consumption_partition(date) TO PUBLIC;
ALTER FUNCTION energy.ensure_consumption_partition(date) SECURITY INVOKER RESET search_path;
