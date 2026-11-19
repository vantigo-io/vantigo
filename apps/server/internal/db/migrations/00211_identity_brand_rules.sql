-- +goose Up
-- Brand assignment (brands design B8, B12): the ordered rules that say which
-- brand a person sees, the revision the whole list is replaced at, and a
-- person's own choice among the brands they match.
--
-- A rule is a kind (an e-mail domain, a Staff legal entity or an identity
-- access group) and a target in that kind's own terms. target_ref is text
-- because a legal entity's id is a bigint in another schema. A (kind, target)
-- pair appears once, which for a domain is also "at most one brand per domain"
-- (the sign-in lookup of B25 depends on it). The list is read and replaced
-- whole, ordered by position.
CREATE TABLE identity.brand_rules (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    brand_id   uuid        NOT NULL REFERENCES identity.brands (id) ON DELETE CASCADE,
    position   integer     NOT NULL CHECK (position >= 0),
    kind       text        NOT NULL CHECK (kind IN ('domain', 'legal_entity', 'access_group')),
    target_ref text        NOT NULL CHECK (char_length(target_ref) BETWEEN 1 AND 255),
    created_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX ux_brand_rules_target ON identity.brand_rules (kind, target_ref);
CREATE INDEX ix_brand_rules_position ON identity.brand_rules (position);
CREATE INDEX ix_brand_rules_brand ON identity.brand_rules (brand_id);

-- The list's revision (If-Match): one row, locked by whoever replaces the list,
-- so two admins never interleave half-edits.
CREATE TABLE identity.brand_rules_revision (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision  integer NOT NULL DEFAULT 1 CHECK (revision >= 1)
);
INSERT INTO identity.brand_rules_revision (singleton, revision) VALUES (true, 1);

-- The personal choice (B12), a preference like color_scheme: not cleared when
-- the user changes, and cleared (NULL) when the brand is deleted.
ALTER TABLE identity.users
    ADD COLUMN preferred_brand_id uuid REFERENCES identity.brands (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE identity.users DROP COLUMN preferred_brand_id;
DROP TABLE identity.brand_rules_revision;
DROP TABLE identity.brand_rules;
