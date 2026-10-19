-- +goose Up
-- The calendar's feed addresses (calendar feed design D1, D2): at most one per
-- person per variant — full, their calendar as they see it, and busy, its
-- spans alone. The token is found by its hex SHA-256 and never stored; the
-- whole address is kept sealed under the secrets purpose calendar/feed-address
-- so the settings screen can show it again, and is opened for its owner's own
-- screen alone. A reset writes a new hash over the old, so the old address is
-- dead at once. user_id is an opaque id into identity, as everywhere in this
-- schema; by house style no CHECK holds the variant to its two values: the
-- module's own path parameter does.
CREATE TABLE calendar.feed_tokens (
    user_id            uuid        NOT NULL,
    variant            varchar(4)  NOT NULL,  -- 'full' | 'busy'
    token_sha256       char(64)    NOT NULL,  -- the token's hex SHA-256; the token itself is never stored
    address_ciphertext text        NOT NULL,  -- the whole address, sealed under calendar/feed-address
    created_at         timestamptz NOT NULL,
    last_used_at       timestamptz,           -- the last request that answered 200 or 304
    PRIMARY KEY (user_id, variant)
);
-- The feed's GET finds its row by the hash, and no two addresses share one.
CREATE UNIQUE INDEX ux_feed_tokens_hash ON calendar.feed_tokens (token_sha256);

-- The calendars a person subscribes to from elsewhere (D6, D7, D8): at most
-- ten each, a limit the module keeps. The worker's record of the last fetch
-- sits on the row — the validators sent back on the next, the body as last
-- fetched, when it last worked, when it last tried and why it failed — and the
-- body is parsed on read, never reconciled into the appointments, so a
-- subscription is never in the person's own feed.
CREATE TABLE calendar.subscriptions (
    id             bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    user_id        uuid          NOT NULL,
    name           varchar(100)  NOT NULL,
    url            varchar(2048) NOT NULL,   -- as typed, webcal(s) rewritten to https
    colour         varchar(10)   NOT NULL,   -- one of the palette's names (D9)
    busy_only      boolean       NOT NULL,
    etag           varchar(500),             -- the feed's last ETag, sent back as If-None-Match
    last_modified  varchar(100),             -- the feed's last Last-Modified, sent back as If-Modified-Since
    body           text,                     -- the last calendar fetched, at most 1 MiB; kept through a failed attempt
    fetched_at     timestamptz,              -- the last attempt that answered a calendar or a 304
    attempted_at   timestamptz,              -- the last attempt, whatever it answered
    next_fetch_at  timestamptz,              -- when the worker fetches next; null is now
    error          varchar(500),             -- why the last attempt failed; null after one that did not
    created_at     timestamptz   NOT NULL,
    updated_at     timestamptz   NOT NULL
);
-- A person's own list, and the worker's due list.
CREATE INDEX ix_subscriptions_user ON calendar.subscriptions (user_id);
CREATE INDEX ix_subscriptions_due  ON calendar.subscriptions (next_fetch_at);

-- +goose Down
-- Both tables go with their rows: every feed address stops answering, and
-- every subscription is forgotten — nothing of either was ever copied into
-- another table.
DROP TABLE calendar.subscriptions;
DROP TABLE calendar.feed_tokens;
