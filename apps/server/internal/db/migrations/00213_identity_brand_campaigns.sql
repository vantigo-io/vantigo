-- +goose Up
-- Brand campaigns (brands design B22-B24): a dated overlay on one brand, such as
-- Pride or Christmas. A campaign has no draft: it is live on save while enabled.
--
-- starts_on and ends_on are calendar dates in the workspace's business time
-- zone, inclusive. A yearly campaign uses month and day only; the year of
-- starts_on is its first active year. The overlay holds inputs only (a primary,
-- a title, logos by slot): tokens are recomputed on the server at resolution
-- (B37, B38), never stored from a request.
--
-- An exclusion constraint cannot express the yearly case, so "at most one
-- campaign is active at a time" (B23) is checked by the handler under the
-- brand's row lock.
CREATE TABLE identity.brand_campaigns (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    brand_id           uuid        NOT NULL REFERENCES identity.brands (id) ON DELETE CASCADE,
    name               text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    starts_on          date        NOT NULL,
    ends_on            date        NOT NULL,
    yearly             boolean     NOT NULL DEFAULT false,
    overlay            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    affects_email      boolean     NOT NULL DEFAULT false,
    enabled            boolean     NOT NULL DEFAULT true,
    revision           integer     NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    updated_by_user_id uuid        REFERENCES identity.users (id) ON DELETE SET NULL
);

CREATE INDEX ix_brand_campaigns_brand ON identity.brand_campaigns (brand_id);

-- +goose Down
DROP TABLE identity.brand_campaigns;
