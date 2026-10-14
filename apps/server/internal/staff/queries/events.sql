-- The history (design S21, §3.6): one row per change, the actor snapshotted.

-- name: InsertEvent :exec
-- InsertEvent appends one event. person_id is NULL for an installation-level
-- event, such as a person deleted as created in error.
INSERT INTO staff.events (person_id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes)
VALUES (@person_id, @kind, @actor_kind, @actor_user_id, @actor_display, @at::timestamptz, @payload, @payload_classes::text[]);

-- name: CountPersonEvents :one
-- CountPersonEvents counts what ListPersonEvents pages through, health events
-- (those whose payload touches class E) — but the absence family's — and the
-- hold's kinds only when asked for, the withdrawal of egenmelding
-- (@care_kinds) to the readers of the care-day fields (@with_care), and an
-- absence event of a row shown as leave never to its leave-reader.
SELECT count(*) FROM staff.events
WHERE staff.events.person_id = @person_id
  AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)) OR kind = ANY(@absence_kinds::text[])
       OR (@with_care::boolean AND kind = ANY(@care_kinds::text[])))
  AND (@with_hold::boolean OR NOT (kind = ANY(@hold_kinds::text[])))
  AND NOT (@hide_leave_rows::boolean AND kind = ANY(@absence_kinds::text[]) AND NOT coalesce(
      CASE WHEN jsonb_typeof(payload->'typeIds') = 'array' AND jsonb_array_length(payload->'typeIds') > 0
           THEN payload->'typeIds' <@ to_jsonb(@plain_type_ids::bigint[]) END, false))
  AND NOT (@hide_leave_rows::boolean AND kind = ANY(@absence_kinds::text[])
           AND (payload->>'absenceId')::bigint IN (
               SELECT ab.id FROM staff.absences ab WHERE NOT (ab.type_id = ANY(@plain_type_ids::bigint[]))));

-- name: ListPersonEvents :many
-- ListPersonEvents is one page of a person's history, newest first; two
-- events of one instant keep the order they were written in, reversed. A
-- health event — one whose payload touches class E, such as a health
-- document's — is left out unless asked for: an audience without the health
-- group does not learn that one happened. The hold's kinds (@hold_kinds) —
-- the legal hold, the reassessment, the rehire, the export — are left out
-- the same way unless asked for (@with_hold). The absence family
-- (@absence_kinds) is listed whatever its classes (plan pre-flight I6): a
-- sick row's event is shaped field by field, each by its own group — but to
-- a reader of HR-only types as leave (@hide_leave_rows), an absence event
-- whose types are not all @plain_type_ids (the types shown as themselves) is
-- not listed at all, an event naming no types included (fail closed): its
-- count and its kinds would tell the HR-only notification from a request —
-- nor one whose row is of such a type now, recorded while it was a plain
-- one (whole-branch review).
-- The withdrawal of egenmelding and its restoring (@care_kinds), class E,
-- are listed to the readers of the care-day fields (@with_care) — the
-- person, staff:manage, staff:personal-data — and never to a manager (plan
-- pre-flight I6).
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes
FROM staff.events
WHERE staff.events.person_id = @person_id
  AND (@with_health::boolean OR NOT ('E' = ANY(payload_classes)) OR kind = ANY(@absence_kinds::text[])
       OR (@with_care::boolean AND kind = ANY(@care_kinds::text[])))
  AND (@with_hold::boolean OR NOT (kind = ANY(@hold_kinds::text[])))
  AND NOT (@hide_leave_rows::boolean AND kind = ANY(@absence_kinds::text[]) AND NOT coalesce(
      CASE WHEN jsonb_typeof(payload->'typeIds') = 'array' AND jsonb_array_length(payload->'typeIds') > 0
           THEN payload->'typeIds' <@ to_jsonb(@plain_type_ids::bigint[]) END, false))
  AND NOT (@hide_leave_rows::boolean AND kind = ANY(@absence_kinds::text[])
           AND (payload->>'absenceId')::bigint IN (
               SELECT ab.id FROM staff.absences ab WHERE NOT (ab.type_id = ANY(@plain_type_ids::bigint[]))))
ORDER BY at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: ListAllPersonEvents :many
-- ListAllPersonEvents is a person's whole history, oldest first, for the
-- personal-data exports (design S16): no page, health events included —
-- each export shapes what its reader may read.
SELECT id, kind, actor_kind, actor_user_id, actor_display, at, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id
ORDER BY at, id;
