-- +goose Up
-- The platform schema holds process-wide infrastructure owned by no module.
-- Each module's baseline migration creates that module's own schema.
CREATE SCHEMA platform;

-- Fixed-window rate-limit counters (internal/ratelimit). One row per
-- policy-and-client key, reset in place when a new window starts, so the
-- table grows with distinct clients rather than with traffic.
CREATE TABLE platform.rate_limit (
    key          text        PRIMARY KEY,
    window_start timestamptz NOT NULL,
    hits         integer     NOT NULL CHECK (hits > 0)
);

-- +goose Down
DROP TABLE platform.rate_limit;
DROP SCHEMA platform;
