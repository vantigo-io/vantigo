-- +goose Up
-- The announced link (Entra phase 2 design L1,
-- docs/superpowers/specs/2026-10-24-entra-link-existing-users-design.md;
-- delivery A, docs/superpowers/plans/2026-10-24-identity-expected-links.md):
-- an Owner's word that a user who already exists is the holder of a
-- directory account, so that the user's next arrival — their first OIDC
-- sign-in through the provider, or the provider's SCIM push — attaches to
-- them as a regular provisioning would, instead of being refused
-- oidc_email_conflict or DuplicateEmail. A link itself cannot be written by
-- hand: identity.oidc_links is keyed on the token's sub, which the directory
-- never returns, and a SCIM mapping is the provider's push.
--
-- One announcement per user (the primary key); a directory account announced
-- for at most one user under an issuer (the two unique indexes — a second
-- Owner naming the same account is refused, never silently the newest). The
-- issuer is the configured OIDC authority, normalised, at the moment of the
-- announcement: an authority change leaves the announcement unfulfillable,
-- as it leaves the links, and it blocks nothing until the Owner's next
-- announcement for the user replaces it. The object id is
-- stored as SCIM's externalId is normalised (a GUID in lower-case dashed
-- form) and the user principal name as emails are, so the arrival's keys
-- compare equal. The actor is always a user: only an Owner announces a link.
CREATE TABLE identity.expected_links (
    user_id              uuid         PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    issuer               varchar(200) NOT NULL,
    object_id            varchar(200),
    user_principal_name  varchar(254),
    created_at           timestamptz  NOT NULL,
    created_by_user_id   uuid         NOT NULL,
    CONSTRAINT expected_links_key_check CHECK (object_id IS NOT NULL OR user_principal_name IS NOT NULL)
);
CREATE UNIQUE INDEX ux_expected_links_object_id ON identity.expected_links (issuer, object_id) WHERE object_id IS NOT NULL;
CREATE UNIQUE INDEX ux_expected_links_upn ON identity.expected_links (issuer, user_principal_name) WHERE user_principal_name IS NOT NULL;

-- +goose Down
-- The announcements not yet fulfilled go with the table: the Owner announces
-- a user again, and until then an arrival is refused as before this version.
DROP TABLE identity.expected_links;
