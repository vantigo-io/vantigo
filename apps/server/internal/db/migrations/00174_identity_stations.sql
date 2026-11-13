-- +goose Up
-- Shared shop-floor stations (manufacturing phase 2 M129–M143): a registered
-- device that signs operators in with a PIN or a badge, never a sign-in
-- credential of its own.

-- A station (M129): a tablet on the floor, paired once with a one-time code
-- and holding a 256-bit token in its cookie for 90 days. Only hashes are
-- stored: the code's and the token's SHA-256. A revoked station has neither.
-- allowed_networks, when set, is the only addresses a station request may come
-- from. alerted_at is the last time its holders of identity:manage were mailed
-- about failed operator sign-ins (M133), at most once an hour.
CREATE TABLE identity.stations (
    id                     uuid PRIMARY KEY,
    name                   varchar(100) NOT NULL,
    token_hash             bytea,
    token_expires_at       timestamptz,
    paired_at              timestamptz,
    pairing_hash           bytea,
    pairing_expires_at     timestamptz,
    idle_minutes           integer      NOT NULL DEFAULT 5,
    require_pin_with_badge boolean      NOT NULL DEFAULT true,
    allowed_networks       cidr[],
    created_by             uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    created_at             timestamptz  NOT NULL,
    last_seen_at           timestamptz,
    last_seen_ip           text,
    alerted_at             timestamptz,
    revoked_at             timestamptz,
    revoked_by             uuid REFERENCES identity.users (id) ON DELETE SET NULL,
    CONSTRAINT ck_stations_name CHECK (btrim(name) <> ''),
    CONSTRAINT ck_stations_idle CHECK (idle_minutes BETWEEN 1 AND 60),
    CONSTRAINT ck_stations_token CHECK ((token_hash IS NULL) = (token_expires_at IS NULL)),
    CONSTRAINT ck_stations_pairing CHECK ((pairing_hash IS NULL) = (pairing_expires_at IS NULL)),
    CONSTRAINT ck_stations_networks CHECK (allowed_networks IS NULL OR cardinality(allowed_networks) BETWEEN 1 AND 50)
);
CREATE UNIQUE INDEX ux_stations_token_hash ON identity.stations (token_hash) WHERE token_hash IS NOT NULL;
CREATE UNIQUE INDEX ux_stations_pairing_hash ON identity.stations (pairing_hash) WHERE pairing_hash IS NOT NULL;
CREATE INDEX ix_stations_created_by ON identity.stations (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX ix_stations_revoked_by ON identity.stations (revoked_by) WHERE revoked_by IS NOT NULL;

-- A person's PIN (M132): argon2id over HMAC-SHA-256 of the six digits under a
-- key derived from APP_SECRET, so the table alone cannot be searched over its
-- million values. key_version is the version of that key the hash was made
-- under: a PIN of an older version cannot be verified and is set again.
CREATE TABLE identity.user_pins (
    user_id     uuid PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    pin_hash    text        NOT NULL,
    key_version integer     NOT NULL,
    set_at      timestamptz NOT NULL
);

-- A person's badge (M132): HMAC-SHA-256 of the code a card reader types,
-- under its own derived key; unique, so one card names one person. key_version
-- as the PIN's: a badge of an older version is enrolled again.
CREATE TABLE identity.user_badges (
    user_id     uuid PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    badge_hash  bytea       NOT NULL,
    key_version integer     NOT NULL,
    set_at      timestamptz NOT NULL,
    set_by      uuid REFERENCES identity.users (id) ON DELETE SET NULL
);
CREATE INDEX ix_user_badges_set_by ON identity.user_badges (set_by) WHERE set_by IS NOT NULL;
CREATE UNIQUE INDEX ux_user_badges_hash ON identity.user_badges (badge_hash);

-- The escalating backoff per (station, user) (M133): consecutive failures,
-- the first and the last of the run, and when the next attempt may be
-- verified. A success deletes the row, and so do a PIN or a badge set or
-- cleared and an administrator's reset; a run whose last failure is an hour
-- old starts again.
CREATE TABLE identity.operator_backoff (
    station_id       uuid        NOT NULL REFERENCES identity.stations (id) ON DELETE CASCADE,
    user_id          uuid        NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
    failures         integer     NOT NULL,
    first_failure_at timestamptz NOT NULL,
    last_failure_at  timestamptz NOT NULL,
    next_attempt_at  timestamptz,
    PRIMARY KEY (station_id, user_id)
);
CREATE INDEX ix_operator_backoff_user ON identity.operator_backoff (user_id);

-- An operator signed in at a station (M133): the token's SHA-256, ended by a
-- switch out, the idle timer, the 12-hour bound, a revocation, a replacing
-- sign-in, the person's PIN or badge changing, their sessions being signed
-- out, or an administrator. The rows are kept as the station's history when
-- the person is erased (M141): user_id is cleared and user_display keeps the
-- name the session had; an erased person's session has ended, so it has no
-- token.
CREATE TABLE identity.station_operator_sessions (
    id               uuid PRIMARY KEY,
    station_id       uuid        NOT NULL REFERENCES identity.stations (id) ON DELETE CASCADE,
    user_id          uuid        REFERENCES identity.users (id) ON DELETE SET NULL,
    user_display     text        NOT NULL,
    token_hash       bytea,
    started_at       timestamptz NOT NULL,
    last_activity_at timestamptz NOT NULL,
    ended_at         timestamptz,
    end_reason       text,
    CONSTRAINT ck_station_operator_sessions_end CHECK ((ended_at IS NULL) = (end_reason IS NULL)),
    CONSTRAINT ck_station_operator_sessions_token CHECK (token_hash IS NULL OR (ended_at IS NULL AND user_id IS NOT NULL)),
    CONSTRAINT ck_station_operator_sessions_reason CHECK (end_reason IS NULL OR end_reason IN
        ('switched_out', 'idle', 'expired', 'revoked', 'replaced', 'user_inactive',
         'credentials_changed', 'signed_out', 'ended_by_admin'))
);
CREATE UNIQUE INDEX ux_station_operator_sessions_token ON identity.station_operator_sessions (token_hash) WHERE token_hash IS NOT NULL;
CREATE INDEX ix_station_operator_sessions_active ON identity.station_operator_sessions (station_id) WHERE ended_at IS NULL;
CREATE INDEX ix_station_operator_sessions_user ON identity.station_operator_sessions (user_id);

-- +goose Down
DROP TABLE identity.station_operator_sessions;
DROP TABLE identity.operator_backoff;
DROP TABLE identity.user_badges;
DROP TABLE identity.user_pins;
DROP TABLE identity.stations;
