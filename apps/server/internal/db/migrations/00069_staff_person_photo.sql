-- +goose Up
-- Staff's photo of a person (staff person-glance design S1 and §3.1,
-- docs/superpowers/specs/2026-10-25-staff-person-glance-design.md; the plan,
-- docs/superpowers/plans/2026-10-25-staff-person-glance.md, task 2): one
-- picture per person, the register's own and not identity's avatar, uploaded
-- by HR or by the person, replaced whole and removed by the same. The bytes
-- live in the object store under the staff scope (people/<id>/photo/<uuid>);
-- the row holds the key, the type and when it was set, which the apps use as
-- the cache key. All three are class D: erased with the record, the object
-- through the deletion ledger. As 00042, 00066, 00067 and 00068, there is no
-- CHECK constraint (amendment 19): the module's validation owns the type (a
-- JPEG or a PNG), the size and the dimensions and reports them on the field.
ALTER TABLE staff.people
    ADD COLUMN photo_object_key   varchar(200),
    ADD COLUMN photo_content_type varchar(40),
    ADD COLUMN photo_updated_at   timestamptz;

-- +goose Down
ALTER TABLE staff.people
    DROP COLUMN photo_updated_at,
    DROP COLUMN photo_content_type,
    DROP COLUMN photo_object_key;
