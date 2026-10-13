-- The holiday schemes (design S9, §3.5, delivery B) and the adjustments of
-- the holiday account they drive.

-- name: ListHolidaySchemes :many
-- ListHolidaySchemes is every scheme, archived ones included so an
-- employment can still name its own: the default first, then by name.
SELECT * FROM staff.holiday_schemes ORDER BY is_default DESC, name, id;

-- name: LockHolidayScheme :one
-- LockHolidayScheme holds one scheme FOR UPDATE for a change of its own: an
-- employment write naming it takes it FOR SHARE (ShareHolidayScheme), so an
-- archive and that write are judged one after the other (plan pre-flight
-- I9). It never locks a person.
SELECT * FROM staff.holiday_schemes WHERE id = @id FOR UPDATE;

-- name: LockDefaultHolidayScheme :one
-- LockDefaultHolidayScheme holds the default scheme for a move of the
-- default, after the scheme it moves to. The caller holds the default's
-- advisory lock, so the row it reads is the default still.
SELECT * FROM staff.holiday_schemes WHERE is_default FOR UPDATE;

-- name: ShareHolidayScheme :one
-- ShareHolidayScheme reads the scheme an employment is about to name and
-- holds it against an archive until the transaction ends. It is taken under
-- the person's lock, after their employment's (design §4 Locking, extended).
SELECT * FROM staff.holiday_schemes WHERE id = @id FOR SHARE;

-- name: HolidaySchemeNamed :one
-- HolidaySchemeNamed is whether any employment names the scheme. Asked under
-- the scheme's FOR UPDATE, which every employment write naming it waits for.
SELECT EXISTS (SELECT 1 FROM staff.employments WHERE holiday_scheme_id = @id::bigint) AS named;

-- name: ClearHolidaySchemeDefault :exec
-- ClearHolidaySchemeDefault takes the default away from every scheme but
-- keep, before keep is made it — the partial unique index admits one default
-- and is judged per statement.
UPDATE staff.holiday_schemes
SET is_default = false, revision = revision + 1, updated_at = @now::timestamptz
WHERE is_default AND id <> @keep;

-- name: InsertHolidayScheme :one
INSERT INTO staff.holiday_schemes (
    name, entitlement_virkedager, extra_from_60_virkedager, main_period_virkedager,
    after_september_virkedager, max_carry_over_virkedager, is_default, created_at, updated_at
) VALUES (
    @name, @entitlement_virkedager, @extra_from_60_virkedager, @main_period_virkedager,
    @after_september_virkedager, @max_carry_over_virkedager, @is_default, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateHolidayScheme :one
-- UpdateHolidayScheme replaces a scheme's figures and name, archives or
-- restores it (an archive keeps its first stamp), and makes it the default
-- when make_default — false leaves the flag as it is.
UPDATE staff.holiday_schemes SET
    name = @name,
    entitlement_virkedager = @entitlement_virkedager,
    extra_from_60_virkedager = @extra_from_60_virkedager,
    main_period_virkedager = @main_period_virkedager,
    after_september_virkedager = @after_september_virkedager,
    max_carry_over_virkedager = @max_carry_over_virkedager,
    is_default = is_default OR @make_default::boolean,
    archived_at = CASE WHEN @archived::boolean THEN coalesce(archived_at, @now::timestamptz) END,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ListHolidayAdjustments :many
-- ListHolidayAdjustments is a person's adjustments, newest year first.
SELECT * FROM staff.holiday_adjustments WHERE person_id = @person_id ORDER BY year DESC, id DESC;

-- name: GetHolidayAdjustment :one
SELECT * FROM staff.holiday_adjustments WHERE id = @id;

-- name: LockHolidayAdjustment :one
-- LockHolidayAdjustment takes one adjustment after its person's lock.
SELECT * FROM staff.holiday_adjustments WHERE id = @id FOR UPDATE;

-- name: CarryOverRecorded :one
-- CarryOverRecorded is whether a carry-over from @from_year is recorded for
-- the person: at most one is (plan pre-flight I8), asked under the person's
-- lock and held by ux_holiday_adjustments_carry_over.
SELECT EXISTS (
    SELECT 1 FROM staff.holiday_adjustments
    WHERE person_id = @person_id AND from_year = @from_year::smallint
      AND reason IN ('agreed_carry_over', 'forced_carry_over')
) AS recorded;

-- name: InsertHolidayAdjustment :one
INSERT INTO staff.holiday_adjustments (person_id, year, days, reason, from_year, note, by_user_id, created_at, updated_at)
VALUES (@person_id, @year, @days, @reason, @from_year, @note, @by_user_id, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: DeleteHolidayAdjustment :exec
DELETE FROM staff.holiday_adjustments WHERE id = @id;
