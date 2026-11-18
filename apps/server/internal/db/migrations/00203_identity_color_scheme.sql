-- +goose Up
-- A person's colour scheme (dark theme design D-A): 'light' or 'dark', or NULL
-- to follow the computer. It is personal, like preferred_language and
-- time_zone, and is never set for anyone by an administrator.
ALTER TABLE identity.users
    ADD COLUMN color_scheme text CHECK (color_scheme IN ('light', 'dark'));

-- +goose Down
ALTER TABLE identity.users DROP COLUMN color_scheme;
