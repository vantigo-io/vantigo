-- name: OwnCalendarAbsences :many
-- OwnCalendarAbsences is this module's calendar source (calendar design D1):
-- one person's own absences that are, or may yet be, taken — requested,
-- approved or registered, never a rejected or cancelled one — overlapping the
-- window, each with its type, and the person's own language to name the type
-- in. The person is the caller's linked self, so an HR-only type is shown: it
-- is the person's own, which the Self audience reads in full. CalendarAbsences
-- (absence.sql) is the team calendar's bars: many people, and no names.
SELECT a.id, a.first_day, a.last_day, a.hours, a.status,
       t.unit, t.name_en, t.name_nb, p.preferred_language
FROM staff.absences a
JOIN staff.absence_types t ON t.id = a.type_id
JOIN staff.people p ON p.id = a.person_id
WHERE a.person_id = @person_id
  AND a.status IN ('requested', 'approved', 'registered')
  AND a.first_day <= @to_day::date AND a.last_day >= @from_day::date
ORDER BY a.first_day, a.id;
