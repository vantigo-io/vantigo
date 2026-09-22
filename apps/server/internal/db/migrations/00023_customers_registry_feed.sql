-- +goose Up
-- Where this installation has read Brreg's update feed up to (registry
-- workers design D1): exactly one row, forever, which is why the primary key
-- is a constant rather than an identity — there is one feed and one position
-- in it, and a second row would silently mean two workers disagreeing about
-- what has been processed.
--
-- next_update_id is "the id to ask for next", already incremented past the
-- last entry processed: the registry's oppdateringsid parameter is INCLUSIVE
-- ("from and including"), so storing the last id itself would re-read that
-- entry on every cycle for the rest of time. NULL means the feed has never
-- been read, and the first request is made by started_at instead — the feed
-- is joined at the moment the worker first ran, never at the beginning of
-- time (the sweep, not the feed, is what covers the customers that existed
-- before then).
--
-- last_update_at is the dato of the last entry processed and last_polled_at
-- the last time a cycle read the feed at all: neither is read by any code
-- path, and both are here because the only report this delivery gives an
-- operator is a log line, and these two rows answer "is it running" and "how
-- far behind is it" from psql alone.
--
-- backfill_after_id is the sweep's OWN position, and it is not decoration
-- (design D3): the backfill takes the 25 lowest-id customers that have no
-- registry record, and a customer whose organisation number the register does
-- not know never gets one — so without a position, those 25 rows are the same
-- 25 rows every cycle, forever, and customer 26 onwards is never looked at. The
-- sweep therefore remembers the last id it attempted and continues past it,
-- starting over at 0 when a batch comes back short. A number nobody can resolve
-- then costs one request per full pass instead of one per cycle, and nobody
-- starves.
CREATE TABLE customers.registry_feed_cursor (
    id                smallint     PRIMARY KEY CHECK (id = 1),
    next_update_id    bigint,
    started_at        timestamptz  NOT NULL,
    last_polled_at    timestamptz,
    last_update_at    timestamptz,
    backfill_after_id integer      NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE customers.registry_feed_cursor;
