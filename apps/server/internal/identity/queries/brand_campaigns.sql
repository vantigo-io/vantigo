-- name: ListBrandCampaigns :many
SELECT id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled, revision,
       created_at, updated_at, updated_by_user_id
FROM identity.brand_campaigns
WHERE brand_id = @brand_id
ORDER BY starts_on, name, id;

-- name: ListEnabledBrandCampaigns :many
-- ListEnabledBrandCampaigns are the campaigns of one brand that can be in force.
SELECT id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled, revision,
       created_at, updated_at, updated_by_user_id
FROM identity.brand_campaigns
WHERE brand_id = @brand_id AND enabled
ORDER BY starts_on, id;

-- name: GetBrandCampaign :one
SELECT id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled, revision,
       created_at, updated_at, updated_by_user_id
FROM identity.brand_campaigns
WHERE id = @id AND brand_id = @brand_id;

-- name: InsertBrandCampaign :one
INSERT INTO identity.brand_campaigns (id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled,
                                      created_at, updated_at, updated_by_user_id)
VALUES (@id, @brand_id, @name::text, @starts_on::date, @ends_on::date, @yearly::boolean, @overlay::jsonb,
        @affects_email::boolean, @enabled::boolean, @now::timestamptz, @now::timestamptz, sqlc.narg('user_id'))
RETURNING id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled, revision,
          created_at, updated_at, updated_by_user_id;

-- name: UpdateBrandCampaign :one
UPDATE identity.brand_campaigns
SET name = @name::text, starts_on = @starts_on::date, ends_on = @ends_on::date, yearly = @yearly::boolean,
    overlay = @overlay::jsonb, affects_email = @affects_email::boolean, enabled = @enabled::boolean,
    revision = revision + 1, updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id AND brand_id = @brand_id
RETURNING id, brand_id, name, starts_on, ends_on, yearly, overlay, affects_email, enabled, revision,
          created_at, updated_at, updated_by_user_id;

-- name: DeleteBrandCampaign :execrows
DELETE FROM identity.brand_campaigns WHERE id = @id AND brand_id = @brand_id;

-- name: ListCampaignsNamingBrandLogo :many
-- ListCampaignsNamingBrandLogo are the enabled campaigns of published brands
-- whose overlay names the asset (B15): the anonymous asset route serves a
-- campaign's logo only while one of them is in force.
SELECT c.id, c.brand_id, c.name, c.starts_on, c.ends_on, c.yearly, c.overlay, c.affects_email, c.enabled, c.revision,
       c.created_at, c.updated_at, c.updated_by_user_id
FROM identity.brand_campaigns c
JOIN identity.brands b ON b.id = c.brand_id AND b.state = 'published'
WHERE c.enabled
  AND EXISTS (SELECT 1 FROM jsonb_each(c.overlay -> 'logos') AS l WHERE l.value ->> 'sha256' = @sha256::text);
