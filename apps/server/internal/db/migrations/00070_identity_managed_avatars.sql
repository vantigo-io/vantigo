-- +goose Up
-- The managed avatar (Staff photo sync design S1 and §3.1,
-- docs/superpowers/specs/2026-10-25-staff-photo-sync-design.md; the plan,
-- docs/superpowers/plans/2026-10-25-staff-photo-sync.md, task 2): HR's photo
-- in Staff as the user's picture, written through
-- contracts.AccountProvisioning.SetManagedAvatar. It is a second row beside
-- identity.profile_avatars, never an overwrite of it: the user's own avatar
-- is kept aside while the managed one stands, and shows again once Staff
-- clears it, with nobody re-uploading it. The effective avatar is this row
-- when one exists, else the own row.
--
-- One managed picture per user (the primary key), gone with the user (the
-- cascade). The bytes are as Staff stored them, metadata already stripped,
-- and the type has the CHECK profile_avatars has. source names who manages
-- the picture ('staff'); source_stamp is the caller's reconciliation key
-- (Staff's photo_updated_at in RFC 3339), which the contract's read answers
-- as "managed:<source_stamp>"; version is bumped on every write, as the own
-- avatar's is, for the image's cache key.
CREATE TABLE identity.managed_avatars (
    user_id      uuid PRIMARY KEY REFERENCES identity.users (id) ON DELETE CASCADE,
    data         bytea NOT NULL,
    content_type text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg')),
    source       text NOT NULL,
    source_stamp text NOT NULL,
    version      integer NOT NULL,
    updated_at   timestamptz NOT NULL
);

-- +goose Down
-- The managed pictures go with the table: every user's own avatar is their
-- picture again, and Staff's next write would need this version.
DROP TABLE identity.managed_avatars;
