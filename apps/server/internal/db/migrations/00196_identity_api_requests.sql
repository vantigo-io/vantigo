-- +goose Up
-- The API request log and the expiry notices (API access design K23, K25,
-- K39, docs/superpowers/specs/2026-11-17-api-access-design.md).

-- One row per request an API key made that identity could attribute: the
-- key and its integration, the operation and how it was answered. It never
-- holds a body, a query string or a path parameter's value, which can carry
-- personal data; the operation id and the trace id find a request in the
-- server's own traces. reason is set for a refusal. There is no FK to
-- api_keys: a key's row is deleted 90 days after it expires, and its log
-- rows age out on their own after 30 days.
--
-- Partitioned by UTC day on at, as energy.consumption_intervals is by month
-- (00005), with a DEFAULT partition so a write never fails when no partition
-- has been made for its day: the identity.api-keys worker makes the next 7
-- days, moves what landed in DEFAULT into its day and drops the days older
-- than 30. With the worker switched off, every row lands in DEFAULT.
CREATE TABLE identity.api_requests (
    at             timestamptz  NOT NULL,
    key_id         uuid         NOT NULL,
    integration_id uuid         NOT NULL,
    method         varchar(10)  NOT NULL,
    operation_id   varchar(200) NOT NULL,
    status         smallint     NOT NULL,
    reason         varchar(20),
    duration_ms    integer      NOT NULL,
    client_ip      varchar(64),
    trace_id       varchar(64)
) PARTITION BY RANGE (at);
CREATE TABLE identity.api_requests_default PARTITION OF identity.api_requests DEFAULT;
CREATE INDEX ix_api_requests_key_at ON identity.api_requests (key_id, at DESC);
-- The Requests screen reads one integration's log.
CREATE INDEX ix_api_requests_integration_at ON identity.api_requests (integration_id, at DESC);

-- ensure_api_requests_partition makes day's partition when it is missing and
-- answers whether it did (K39). Postgres refuses to create a partition while
-- DEFAULT holds rows for it, so in the one transaction it locks the table,
-- moves that day's rows out of DEFAULT into a temporary table, creates the
-- partition and inserts them back. Taking the lock first keeps a write from
-- landing in DEFAULT between the move and the create; writes wait the few
-- milliseconds it takes and then go to the new partition.
-- +goose StatementBegin
CREATE FUNCTION identity.ensure_api_requests_partition(day date)
RETURNS boolean LANGUAGE plpgsql AS $function$
DECLARE
    lo timestamptz := day::timestamp AT TIME ZONE 'UTC';
    hi timestamptz := (day + 1)::timestamp AT TIME ZONE 'UTC';
    partition_name text := format('api_requests_%s', to_char(day, 'YYYYMMDD'));
BEGIN
    IF to_regclass(format('identity.%I', partition_name)) IS NOT NULL THEN
        RETURN false;
    END IF;
    LOCK TABLE identity.api_requests IN ACCESS EXCLUSIVE MODE;
    IF to_regclass(format('identity.%I', partition_name)) IS NOT NULL THEN
        RETURN false;
    END IF;
    CREATE TEMPORARY TABLE api_requests_drain (LIKE identity.api_requests) ON COMMIT DROP;
    WITH moved AS (
        DELETE FROM identity.api_requests_default WHERE at >= lo AND at < hi RETURNING *
    )
    INSERT INTO api_requests_drain SELECT * FROM moved;
    EXECUTE format(
        'CREATE TABLE identity.%I PARTITION OF identity.api_requests FOR VALUES FROM (%L) TO (%L)',
        partition_name, lo, hi);
    INSERT INTO identity.api_requests SELECT * FROM api_requests_drain;
    DROP TABLE api_requests_drain;
    RETURN true;
END;
$function$;
-- +goose StatementEnd

-- drop_api_requests_before drops every day's partition before day, deletes
-- what DEFAULT holds from before it, and answers how many partitions it
-- dropped (K23's 30 days).
-- +goose StatementBegin
CREATE FUNCTION identity.drop_api_requests_before(day date)
RETURNS integer LANGUAGE plpgsql AS $function$
DECLARE
    partition_name text;
    dropped integer := 0;
BEGIN
    FOR partition_name IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
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

-- Today and the 6 days after it, in UTC, so a fresh installation logs into
-- day partitions before the worker first runs.
-- +goose StatementBegin
DO $$
DECLARE
    i integer;
BEGIN
    FOR i IN 0..6 LOOP
        PERFORM identity.ensure_api_requests_partition((now() AT TIME ZONE 'UTC')::date + i);
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- The expiry notices sent (K25), one per key and kind, so none is sent twice:
-- 14_days and 3_days before the key expires, and today, the day it does.
CREATE TABLE identity.api_key_notices (
    key_id  uuid        NOT NULL REFERENCES identity.api_keys (id) ON DELETE CASCADE,
    kind    varchar(10) NOT NULL,
    sent_at timestamptz NOT NULL,
    PRIMARY KEY (key_id, kind),
    CONSTRAINT ck_api_key_notices_kind CHECK (kind IN ('14_days', '3_days', 'today'))
);

-- +goose Down
DROP TABLE identity.api_key_notices;
DROP FUNCTION identity.drop_api_requests_before(date);
DROP FUNCTION identity.ensure_api_requests_partition(date);
DROP TABLE identity.api_requests;
