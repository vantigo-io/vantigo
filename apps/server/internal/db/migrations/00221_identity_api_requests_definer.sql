-- +goose Up
-- The API request log's partition upkeep under the least-privilege runtime
-- role (00220 is the same for Energy's consumption).
--
-- identity.ensure_api_requests_partition and identity.drop_api_requests_before
-- (00199), which the identity.api-keys worker calls, lock, create and drop
-- partitions of identity.api_requests. Without SECURITY DEFINER they ran as
-- the server's role, so under the runtime role the installation guide
-- prescribes (admin/installation.md, Database roles) every new day failed
-- with "permission denied for schema identity": requests piled into DEFAULT
-- and the 30 days' retention stopped once a day partition was due to go.
--
-- Both are now SECURITY DEFINER, running as their owner, the role that ran
-- the migrations and owns the table. Each pins search_path to pg_catalog with
-- the session's temporary schema last — the drain table is named pg_temp.
-- explicitly — and accepts only a day from 2000 to 2099. The most a caller
-- can do is make a bounded number of day partitions of the one table, or
-- drop the log's days before a date, which DELETE, already the runtime
-- role's, could empty as well. Partition names are derived from the day or
-- read from the catalog, never taken from the caller.
--
-- EXECUTE is revoked from PUBLIC, so a role that may read the identity schema
-- but not write it cannot drop the log through the function, and granted to
-- every role, other than the owner, that can already insert into and delete
-- from the table: the runtime role, when the installation made it before
-- migrating. A role made afterwards gets it with the guide's GRANT EXECUTE.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.ensure_api_requests_partition(day date)
RETURNS boolean LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    lo timestamptz;
    hi timestamptz;
    partition_name text;
BEGIN
    IF day IS NULL OR day < date '2000-01-01' OR day >= date '2100-01-01' THEN
        RAISE EXCEPTION 'identity.ensure_api_requests_partition: day % is outside 2000-2099', day
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    lo := day::timestamp AT TIME ZONE 'UTC';
    hi := (day + 1)::timestamp AT TIME ZONE 'UTC';
    partition_name := format('api_requests_%s', to_char(day, 'YYYYMMDD'));
    IF to_regclass(format('identity.%I', partition_name)) IS NOT NULL THEN
        RETURN false;
    END IF;
    PERFORM set_config('lock_timeout', '5s', true);
    LOCK TABLE identity.api_requests IN ACCESS EXCLUSIVE MODE;
    IF to_regclass(format('identity.%I', partition_name)) IS NOT NULL THEN
        RETURN false;
    END IF;
    CREATE TEMPORARY TABLE api_requests_drain (LIKE identity.api_requests) ON COMMIT DROP;
    WITH moved AS (
        DELETE FROM identity.api_requests_default WHERE at >= lo AND at < hi RETURNING *
    )
    INSERT INTO pg_temp.api_requests_drain SELECT * FROM moved;
    EXECUTE format(
        'CREATE TABLE identity.%I PARTITION OF identity.api_requests FOR VALUES FROM (%L) TO (%L)',
        partition_name, lo, hi);
    INSERT INTO identity.api_requests SELECT * FROM pg_temp.api_requests_drain;
    DROP TABLE pg_temp.api_requests_drain;
    RETURN true;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.drop_api_requests_before(day date)
RETURNS integer LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp
AS $function$
DECLARE
    partition_name text;
    dropped integer := 0;
BEGIN
    IF day IS NULL OR day < date '2000-01-01' OR day >= date '2100-01-01' THEN
        RAISE EXCEPTION 'identity.drop_api_requests_before: day % is outside 2000-2099', day
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    FOR partition_name IN
        SELECT c.relname
        FROM pg_catalog.pg_inherits i
        JOIN pg_catalog.pg_class c ON c.oid = i.inhrelid
        WHERE i.inhparent = 'identity.api_requests'::regclass
          AND c.relname ~ '^api_requests_[0-9]{8}$'
          AND to_date(substr(c.relname, 14), 'YYYYMMDD') < day
        ORDER BY c.relname
    LOOP
        EXECUTE format('DROP TABLE identity.%I', partition_name);
        dropped := dropped + 1;
    END LOOP;
    DELETE FROM identity.api_requests_default WHERE at < day::timestamp AT TIME ZONE 'UTC';
    RETURN dropped;
END;
$function$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION identity.ensure_api_requests_partition(date) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION identity.drop_api_requests_before(date) FROM PUBLIC;

-- +goose StatementBegin
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT rolname FROM pg_catalog.pg_roles
        WHERE rolname <> current_user AND rolname NOT LIKE 'pg\_%'
          AND has_table_privilege(oid, 'identity.api_requests', 'INSERT')
          AND has_table_privilege(oid, 'identity.api_requests', 'DELETE')
    LOOP
        EXECUTE format('GRANT EXECUTE ON FUNCTION identity.ensure_api_requests_partition(date) TO %I', r.rolname);
        EXECUTE format('GRANT EXECUTE ON FUNCTION identity.drop_api_requests_before(date) TO %I', r.rolname);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Back to invoker's rights and PUBLIC's EXECUTE; the argument checks stay in
-- the bodies, which refuse nothing a caller of 00199's did.
GRANT EXECUTE ON FUNCTION identity.ensure_api_requests_partition(date) TO PUBLIC;
GRANT EXECUTE ON FUNCTION identity.drop_api_requests_before(date) TO PUBLIC;
ALTER FUNCTION identity.ensure_api_requests_partition(date) SECURITY INVOKER RESET search_path;
ALTER FUNCTION identity.drop_api_requests_before(date) SECURITY INVOKER RESET search_path;
