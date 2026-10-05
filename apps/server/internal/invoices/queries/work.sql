-- The work a document bills (invoices work design D2, D5, D8, D9): the line
-- sources held from the draft, invoiced by the issue and released by the
-- credit note that returns their line; the releases; the document's project;
-- and the timesheet rows. A source's kind and id are opaque — the rows are
-- other modules' (module-boundaries rule 4) — and a set is one statement:
-- parallel arrays unnested side by side (equal lengths, so they zip).

-- name: LineSourcesOf :many
-- LineSourcesOf is one document's line sources with their line's position, in
-- the lines' order and then by source.
SELECT s.*, l.position AS line_position
FROM invoices.line_sources s
JOIN invoices.lines l ON l.id = s.line_id
WHERE s.invoice_id = @invoice_id
ORDER BY l.position, s.source_kind, s.source_id;

-- name: InsertLineSources :exec
-- InsertLineSources writes a document's sources held, in ONE statement
-- ordered by (source_kind, source_id) (D2): two transactions inserting
-- overlapping holds wait on ux_line_sources_live in the same order, so one
-- fails with the unique violation and neither deadlocks. The arrays run in
-- parallel; an empty subkind is NULL (only an expense carries one).
INSERT INTO invoices.line_sources (
    line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
    project_id, quantity, amount, currency, source_date
)
SELECT u.line_id, @invoice_id::bigint, u.kind, u.id, u.revision, NULLIF(u.subkind, ''),
    u.project_id, u.quantity, u.amount, u.currency, u.source_date
FROM (
    SELECT unnest(@line_ids::bigint[]) AS line_id, unnest(@kinds::text[]) AS kind,
        unnest(@ids::bigint[]) AS id, unnest(@revisions::int[]) AS revision,
        unnest(@subkinds::text[]) AS subkind, unnest(@project_ids::int[]) AS project_id,
        unnest(@quantities::numeric[]) AS quantity, unnest(@amounts::numeric[]) AS amount,
        unnest(@currencies::text[]) AS currency, unnest(@dates::date[]) AS source_date
) AS u
ORDER BY u.kind, u.id;

-- name: DeleteHeldSourcesOf :many
-- DeleteHeldSourcesOf drops every hold of a draft — a customer change (D2) —
-- and answers what it released.
DELETE FROM invoices.line_sources
WHERE invoice_id = @invoice_id AND state = 'held'
RETURNING source_kind, source_id;

-- name: LiveSourcesElsewhere :many
-- LiveSourcesElsewhere is every held or invoiced row of the given sources
-- (kinds[i], ids[i]) on a document other than invoice_id, with that
-- document's number and status, the oldest first: what refuses a new hold
-- with source_held_elsewhere (D2).
SELECT s.source_kind, s.source_id, s.state, s.invoice_id, i.number, i.status
FROM invoices.line_sources s
JOIN invoices.invoices i ON i.id = s.invoice_id
WHERE s.state IN ('held', 'invoiced') AND s.invoice_id <> @invoice_id
  AND EXISTS (
    SELECT 1 FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@ids::bigint[]) AS id) AS u
    WHERE u.kind = s.source_kind AND u.id = s.source_id)
ORDER BY s.id;

-- name: LiveSourcesFor :many
-- LiveSourcesFor is every held or invoiced row of the given sources, with its
-- document's number and status: the uninvoiced view lists such work as held,
-- never selectable (D3).
SELECT s.source_kind, s.source_id, s.state, s.invoice_id, i.number, i.status
FROM invoices.line_sources s
JOIN invoices.invoices i ON i.id = s.invoice_id
WHERE s.state IN ('held', 'invoiced')
  AND EXISTS (
    SELECT 1 FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@ids::bigint[]) AS id) AS u
    WHERE u.kind = s.source_kind AND u.id = s.source_id)
ORDER BY s.source_kind, s.source_id;

-- name: HeldOnDrafts :many
-- HeldOnDrafts counts the held rows per draft, project and kind for the
-- given projects: the view's heldOnDrafts (D3).
SELECT s.invoice_id, s.project_id, s.source_kind, count(*)::int AS held
FROM invoices.line_sources s
WHERE s.state = 'held' AND s.project_id = ANY(@project_ids::int[])
GROUP BY s.invoice_id, s.project_id, s.source_kind
ORDER BY s.project_id, s.source_kind, s.invoice_id;

-- name: MarkSourcesInvoiced :execrows
-- MarkSourcesInvoiced moves a draft's holds to invoiced in the issue's own
-- step, while the document is still a draft — the one change the trigger
-- allows there (D2).
UPDATE invoices.line_sources SET state = 'invoiced'
WHERE invoice_id = @invoice_id AND state = 'held';

-- name: InvoicedSourcesOfLines :many
-- InvoicedSourcesOfLines is the invoiced rows of the given lines: what a
-- credit note that returns them in full releases (D8).
SELECT * FROM invoices.line_sources
WHERE line_id = ANY(@line_ids::bigint[]) AND state = 'invoiced'
ORDER BY source_kind, source_id;

-- name: ReleaseSources :many
-- ReleaseSources moves the given invoiced rows to released — the one change
-- the trigger allows under an issued document (D2, D8) — and answers them.
UPDATE invoices.line_sources SET state = 'released'
WHERE id = ANY(@ids::bigint[]) AND state = 'invoiced'
RETURNING *;

-- name: InsertLineReleases :exec
-- InsertLineReleases records a credit note's releases (D8): each credit line
-- (credit_line_ids[i]) and the original's source it released
-- (line_source_ids[i]), in one statement.
INSERT INTO invoices.line_releases (invoice_id, credit_line_id, line_source_id)
SELECT @invoice_id::bigint, u.credit_line_id, u.line_source_id
FROM (
    SELECT unnest(@credit_line_ids::bigint[]) AS credit_line_id, unnest(@line_source_ids::bigint[]) AS line_source_id
) AS u
ORDER BY u.line_source_id;

-- name: ReleasesOf :many
-- ReleasesOf is a credit note's releases with the sources they released.
SELECT r.id, r.credit_line_id, r.line_source_id, s.source_kind, s.source_id,
    s.invoice_id AS original_invoice_id, s.line_id AS original_line_id
FROM invoices.line_releases r
JOIN invoices.line_sources s ON s.id = r.line_source_id
WHERE r.invoice_id = @invoice_id
ORDER BY s.source_kind, s.source_id;

-- name: ReleasedHistoryOf :many
-- ReleasedHistoryOf is, for the given sources, each released row's original
-- invoice and the credit note that released it, newest first: the note the
-- wizard suggests when it pulls released work again (D8).
SELECT s.source_kind, s.source_id, s.invoice_id AS original_invoice_id, o.number AS original_number,
    r.invoice_id AS credit_note_id, c.number AS credit_note_number
FROM invoices.line_sources s
JOIN invoices.invoices o ON o.id = s.invoice_id
JOIN invoices.line_releases r ON r.line_source_id = s.id
JOIN invoices.invoices c ON c.id = r.invoice_id
WHERE s.state = 'released'
  AND EXISTS (
    SELECT 1 FROM (SELECT unnest(@kinds::text[]) AS kind, unnest(@ids::bigint[]) AS id) AS u
    WHERE u.kind = s.source_kind AND u.id = s.source_id)
ORDER BY c.issued_at DESC, r.id DESC;

-- name: SetDocumentProject :exec
-- SetDocumentProject writes the project a draft's work belongs to and its
-- code, derived by every save (D9): both, or NULL when the work spans two
-- projects or there is none.
UPDATE invoices.invoices
SET project_id = sqlc.narg(project_id), project_reference = sqlc.narg(project_reference)
WHERE id = @id;

-- name: TimesheetRowsOf :many
-- TimesheetRowsOf is a document's timesheet as printed (D5), in order.
SELECT * FROM invoices.timesheet_rows WHERE invoice_id = @invoice_id ORDER BY position;

-- name: InsertTimesheetRows :exec
-- InsertTimesheetRows writes a draft's timesheet rows in one statement (D5):
-- the arrays run in parallel; an empty work type is NULL. Never the entry's
-- note.
INSERT INTO invoices.timesheet_rows (
    invoice_id, position, source_id, person_label, entry_date, hours, work_type, description
)
SELECT @invoice_id::bigint, u.position, u.source_id, u.person_label, u.entry_date, u.hours,
    NULLIF(u.work_type, ''), u.description
FROM (
    SELECT unnest(@positions::int[]) AS position, unnest(@source_ids::bigint[]) AS source_id,
        unnest(@person_labels::text[]) AS person_label, unnest(@entry_dates::date[]) AS entry_date,
        unnest(@hours::numeric[]) AS hours, unnest(@work_types::text[]) AS work_type,
        unnest(@descriptions::text[]) AS description
) AS u
ORDER BY u.position;

-- name: DeleteTimesheetRows :exec
-- DeleteTimesheetRows clears a draft's timesheet: the flag turned off (D5).
DELETE FROM invoices.timesheet_rows WHERE invoice_id = @invoice_id;

-- name: PruneTimesheetRows :execrows
-- PruneTimesheetRows drops a draft's rows of hours it no longer holds (D5).
-- A nil kept arrives as NULL, and <> ALL(NULL) is NULL: COALESCE makes it
-- the empty set, so keeping nothing deletes every row.
DELETE FROM invoices.timesheet_rows
WHERE invoice_id = @invoice_id AND source_id <> ALL(COALESCE(@kept::bigint[], '{}'::bigint[]));
