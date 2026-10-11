-- The retention run (design S15 as amended by the A2 pre-flight, plan Task 5):
-- three due queries over what remains, the erases' reads and writes, the
-- deletion ledger and the retention block of GET /settings. Each due query
-- takes an optional person (or employment) so the erase judges it again on
-- the row its lock holds — one predicate, asked twice — and none crosses the
-- schema. The columns an erase nulls are not named here: they are the
-- module's columnClasses (retention.go), and the statements that null them
-- are built from it.

-- name: DuePeopleD :many
-- DuePeopleD is the people whose personnel administration (class D) is due
-- for erasure: not erased yet, not held, with at least one employment and
-- every one ended before @cutoff (today minus retentionGraceDays) — a person
-- with no employment is never erased. Oldest last end first, so a backlog
-- drains in the order it built up.
SELECT p.id
FROM staff.people p
WHERE p.erased_at IS NULL
  AND NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
  AND NOT EXISTS (
      SELECT 1 FROM staff.employments e
      WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @cutoff::date))
ORDER BY (SELECT max(e.end_date) FROM staff.employments e WHERE e.person_id = p.id), p.id
LIMIT @row_limit;

-- name: DuePeopleHealth :many
-- DuePeopleHealth is the people whose health content is due (plan reading 3):
-- erased or not, not held, every employment ended before @cutoff (today
-- minus healthRetentionDays), with something of class E still there — a
-- document in a restricted category, or a value of class E in an event's
-- payload that the erase has not replaced by "changed" yet.
SELECT p.id
FROM staff.people p
WHERE NOT p.legal_hold
  AND (sqlc.narg(person_id)::bigint IS NULL OR p.id = sqlc.narg(person_id)::bigint)
  AND EXISTS (SELECT 1 FROM staff.employments e WHERE e.person_id = p.id)
  AND NOT EXISTS (
      SELECT 1 FROM staff.employments e
      WHERE e.person_id = p.id AND (e.end_date IS NULL OR e.end_date >= @cutoff::date))
  AND (
      EXISTS (
          SELECT 1 FROM staff.documents d
          JOIN staff.document_categories c ON c.id = d.category_id
          WHERE d.person_id = p.id AND c.retention_class = 'E')
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = p.id AND 'E' = ANY(ev.payload_classes)
            AND EXISTS (
                SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                WHERE f.value <> to_jsonb(@blanked::text))))
ORDER BY (SELECT max(e.end_date) FROM staff.employments e WHERE e.person_id = p.id), p.id
LIMIT @row_limit;

-- name: DueEmploymentsA :many
-- DueEmploymentsA is the employments whose pay documentation (class A) is
-- due (plan reading 4): ended in a year before @before_year — this year
-- less the five years pay documentation is kept — of a person not held — erased or not — with something of
-- class A still there: a pay column set on one of its terms rows
-- (@a_columns, the terms' class A columns), a document of class A filed
-- against it — or filed against no employment while it is the person's last
-- one — or a field of class A (@a_fields) in one of its events' payloads not
-- yet replaced by "changed". Oldest end first.
SELECT e.id, e.person_id
FROM staff.employments e
JOIN staff.people p ON p.id = e.person_id
WHERE NOT p.legal_hold
  AND e.end_date IS NOT NULL
  AND EXTRACT(YEAR FROM e.end_date)::int < @before_year::int
  AND (sqlc.narg(person_id)::bigint IS NULL OR e.person_id = sqlc.narg(person_id)::bigint)
  AND (
      EXISTS (
          SELECT 1 FROM staff.employment_terms t
          WHERE t.employment_id = e.id AND jsonb_strip_nulls(to_jsonb(t)) ?| @a_columns::text[])
      OR EXISTS (
          SELECT 1 FROM staff.documents d
          JOIN staff.document_categories c ON c.id = d.category_id
          WHERE d.person_id = e.person_id AND c.retention_class = 'A'
            AND (d.employment_id = e.id OR (d.employment_id IS NULL AND e.id = (
                SELECT l.id FROM staff.employments l WHERE l.person_id = e.person_id
                ORDER BY l.start_date DESC, l.id DESC LIMIT 1))))
      OR EXISTS (
          SELECT 1 FROM staff.events ev
          WHERE ev.person_id = e.person_id AND 'A' = ANY(ev.payload_classes)
            AND ev.payload ->> 'documentId' IS NULL
            AND ev.payload ->> 'employmentId' = e.id::text
            AND EXISTS (
                SELECT 1 FROM jsonb_each(ev.payload -> 'fields') f
                WHERE f.key = ANY(@a_fields::text[]) AND f.value <> to_jsonb(@blanked::text))))
ORDER BY e.end_date, e.id
LIMIT @row_limit;

-- name: LastEmploymentID :one
-- LastEmploymentID is a person's last employment — the latest to start — the
-- one a document filed against no employment belongs to for the A erase.
SELECT id FROM staff.employments
WHERE person_id = @person_id
ORDER BY start_date DESC, id DESC
LIMIT 1;

-- name: PersonDocumentsOfClass :many
-- PersonDocumentsOfClass is a person's documents in categories of one
-- retention class, the D and the health erases' rows to delete.
SELECT d.id, d.object_key
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.person_id = @person_id AND c.retention_class = @class::text
ORDER BY d.id;

-- name: EmploymentDocumentsA :many
-- EmploymentDocumentsA is the documents of class A the A erase of one
-- employment deletes: those filed against it, and those filed against no
-- employment when it is the person's last one (@is_last).
SELECT d.id, d.object_key
FROM staff.documents d
JOIN staff.document_categories c ON c.id = d.category_id
WHERE d.person_id = @person_id AND c.retention_class = 'A'
  AND (d.employment_id = @employment_id OR (d.employment_id IS NULL AND @is_last::boolean))
ORDER BY d.id;

-- name: PersonEventsOfClass :many
-- PersonEventsOfClass is a person's events whose payload touches one
-- retention class (payload_classes, written for the erase alone), oldest
-- first, for the erase to replace that class's values in.
SELECT id, payload, payload_classes
FROM staff.events
WHERE person_id = @person_id AND @class::text = ANY(payload_classes)
ORDER BY id;

-- name: SetEventPayload :exec
-- SetEventPayload rewrites one event's payload: the erase's only change to
-- the history, which keeps its kind, actor, instant and classes.
UPDATE staff.events SET payload = @payload WHERE id = @id;

-- name: MarkPersonErased :exec
-- MarkPersonErased sets erased_at, the D erase's last write: the record is
-- read-only from here (erased.go) until a rehire clears it.
UPDATE staff.people SET
    erased_at = @now::timestamptz,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: DueObjectDeletions :many
-- DueObjectDeletions is the ledger rows a pass may claim: due, and not
-- claimed — or claimed by a pass whose window has run out. A hundred at a
-- time, the longest waiting first.
SELECT object_key
FROM staff.object_deletions
WHERE next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
ORDER BY next_attempt_at, id
LIMIT @row_limit;

-- name: ClaimObjectDeletion :one
-- ClaimObjectDeletion claims one ledger row for @claimed_until by a
-- conditional update that asserts the candidate predicate again: of two
-- passes, on two replicas, that both selected the row, one updates it and the
-- other finds no row. It answers the attempts already failed.
UPDATE staff.object_deletions SET claimed_until = @claimed_until::timestamptz
WHERE object_key = @object_key
  AND next_attempt_at <= @now::timestamptz
  AND (claimed_until IS NULL OR claimed_until < @now::timestamptz)
RETURNING attempts;

-- name: RetentionLedgerState :one
-- RetentionLedgerState is the ledger as the retention block of GET /settings
-- counts it: the objects waiting, and those the store has refused at least
-- once.
SELECT
    count(*)::bigint AS pending,
    (count(*) FILTER (WHERE attempts >= 1))::bigint AS failing
FROM staff.object_deletions;

-- name: OldestPendingDeletion :one
-- OldestPendingDeletion is when the oldest object still in the ledger was
-- queued; no row when the ledger is empty.
SELECT created_at FROM staff.object_deletions ORDER BY created_at, id LIMIT 1;

-- name: LastErasedAt :one
-- LastErasedAt is the instant of the latest erased event; no row when the
-- run has never erased anything.
SELECT at FROM staff.events WHERE kind = 'erased' ORDER BY at DESC, id DESC LIMIT 1;
