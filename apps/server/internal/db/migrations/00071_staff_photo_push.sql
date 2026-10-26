-- +goose Up
-- The photo pushed to the directory (Staff photo sync design S2 and §3.2,
-- docs/superpowers/specs/2026-10-25-staff-photo-sync-design.md; the plan,
-- docs/superpowers/plans/2026-10-25-staff-photo-sync.md, task 2): on a
-- provider link (provider = 'entra'), the stamp of the effective picture the
-- accounts worker last pushed to the directory — "managed:<source_stamp>"
-- for HR's photo as identity holds it, "own:<version>@<unix seconds>" for
-- the user's own avatar, or "none" once the photo was cleared or when there
-- was none to push — null until the first push. The worker compares it with
-- the effective avatar's stamp on every claim and pushes when they differ,
-- so a failed push, a late link or a changed own avatar is right by the next
-- claim. It is class C, as the rest of the link (retention.go's
-- columnClasses): it is about the account, not the person.
-- As 00042 and 00069, there is no CHECK constraint (amendment 19): the
-- worker writes it.
ALTER TABLE staff.identity_links
    ADD COLUMN photo_pushed_stamp varchar(80);

-- +goose Down
ALTER TABLE staff.identity_links
    DROP COLUMN photo_pushed_stamp;
