-- name: ListBrandRules :many
-- ListBrandRules is the whole ordered rule list (brands design B8), every
-- brand's rules together: the order is the admin's.
SELECT id, brand_id, position, kind, target_ref
FROM identity.brand_rules
ORDER BY position, id;

-- name: ListLiveBrandRules :many
-- ListLiveBrandRules is the rules resolution reads: those pointing at a
-- published brand. A brand never published matches no rule until it is (B20),
-- and a rule never points at an archived one (B8).
SELECT r.id, r.brand_id, r.position, r.kind, r.target_ref
FROM identity.brand_rules r
JOIN identity.brands b ON b.id = r.brand_id AND b.state = 'published'
ORDER BY r.position, r.id;

-- name: LockBrandRulesRevision :one
-- LockBrandRulesRevision is the list's revision, locked for the transaction
-- that replaces it, so two admins never interleave half-edits.
SELECT revision FROM identity.brand_rules_revision WHERE singleton FOR UPDATE;

-- name: GetBrandRulesRevision :one
SELECT revision FROM identity.brand_rules_revision WHERE singleton;

-- name: BumpBrandRulesRevision :one
UPDATE identity.brand_rules_revision SET revision = revision + 1 WHERE singleton RETURNING revision;

-- name: DeleteAllBrandRules :exec
DELETE FROM identity.brand_rules;

-- name: DeleteBrandRulesOfBrand :execrows
DELETE FROM identity.brand_rules WHERE brand_id = @brand_id;

-- name: InsertBrandRule :exec
INSERT INTO identity.brand_rules (id, brand_id, position, kind, target_ref, created_at)
VALUES (@id, @brand_id, @position, @kind::text, @target_ref::text, @now::timestamptz);

-- name: CountBrandRulesByBrand :many
SELECT brand_id, count(*)::int AS rules FROM identity.brand_rules GROUP BY brand_id;

-- name: GetBrandResolutionFacts :one
-- GetBrandResolutionFacts is what resolution reads of a user outside the
-- rules: their kind (only a person is assigned a brand, B8), the address whose
-- domain a domain rule matches, and their own choice (B12).
SELECT kind, normalized_email, preferred_brand_id, display_name
FROM identity.users
WHERE id = @id;

-- name: ListUserActiveGroupIDs :many
-- ListUserActiveGroupIDs is the active access groups the user is effectively a
-- member of: forced in, or upstream-present with no override, as role mapping
-- counts membership.
SELECT g.id
FROM identity.access_group_memberships gm
JOIN identity.access_groups g ON g.id = gm.group_id AND g.is_active
WHERE gm.user_id = @user_id
  AND (gm.membership_override = 'force_member'
       OR (gm.membership_override IS NULL AND gm.is_upstream_present));

-- name: ListUserActiveGroupNames :many
SELECT g.id, g.display_name
FROM identity.access_group_memberships gm
JOIN identity.access_groups g ON g.id = gm.group_id AND g.is_active
WHERE gm.user_id = @user_id
  AND (gm.membership_override = 'force_member'
       OR (gm.membership_override IS NULL AND gm.is_upstream_present))
ORDER BY g.display_name COLLATE "C", g.id;

-- name: ListBrandRuleGroups :many
-- ListBrandRuleGroups is every access group, for the rule editor's picker and
-- for flagging a rule whose group is gone or inactive.
SELECT id, display_name, is_active FROM identity.access_groups ORDER BY display_name COLLATE "C", id;

-- name: SetUserPreferredBrand :exec
UPDATE identity.users SET preferred_brand_id = sqlc.narg('brand_id') WHERE id = @id;

-- name: InsertDraftBrand :one
-- InsertDraftBrand creates a brand that was never published: a draft only (B20).
INSERT INTO identity.brands (is_default, state, draft, name_key, created_at, updated_at, updated_by_user_id)
VALUES (false, 'draft', @draft::jsonb, @name_key::text, @now::timestamptz, @now::timestamptz, sqlc.narg('user_id'))
RETURNING id;

-- name: SetBrandState :exec
UPDATE identity.brands
SET state = @state::text, revision = revision + 1, updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id;

-- name: ClearDefaultBrand :exec
UPDATE identity.brands
SET is_default = false, revision = revision + 1, updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE is_default;

-- name: SetDefaultBrand :exec
UPDATE identity.brands
SET is_default = true, revision = revision + 1, updated_at = @now::timestamptz, updated_by_user_id = sqlc.narg('user_id')
WHERE id = @id;

-- name: DeleteBrand :exec
DELETE FROM identity.brands WHERE id = @id;

-- name: ClearUserPreferredBrandOf :execrows
-- ClearUserPreferredBrandOf drops the choice of everyone who chose a brand
-- that is being archived: an archived brand cannot be chosen (B21).
UPDATE identity.users SET preferred_brand_id = NULL WHERE preferred_brand_id = @brand_id;

-- name: NextBrandRulePosition :one
SELECT coalesce(max(position) + 1, 0)::int AS position FROM identity.brand_rules;

-- name: InsertBrandRuleIfAbsent :execrows
-- InsertBrandRuleIfAbsent is the development seed's: it adds a rule at the end
-- of the list unless the (kind, target) pair is already in it.
INSERT INTO identity.brand_rules (id, brand_id, position, kind, target_ref, created_at)
VALUES (@id, @brand_id, @position, @kind::text, @target_ref::text, @now::timestamptz)
ON CONFLICT (kind, target_ref) DO NOTHING;

-- name: GetBrandIDByNameKey :one
SELECT id FROM identity.brands WHERE name_key = @name_key::text;

-- name: BrandNameKeyTaken :one
SELECT EXISTS (SELECT 1 FROM identity.brands WHERE name_key = @name_key::text) AS taken;
