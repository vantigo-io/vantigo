-- name: SetTimelineEntryFollowUpDone :one
-- SetTimelineEntryFollowUpDone is POST .../follow-up/done's guarded write
-- (follow-ups design D1). Two things about it are the design, not style.
--
-- There is NO expected_revision. A tick comes from a list — the Follow-ups
-- page, or an entry line in a feed loaded minutes ago — and must not lose a
-- race with somebody editing the note; the design says so outright. What takes
-- its place is `follow_up_done_at IS NULL` in the WHERE: two concurrent ticks
-- serialize on the row, and the second matches no row. The handler reads that
-- as "already done" and answers 200 with the entry, which is what idempotent
-- means here.
--
-- current_revision is nonetheless bumped, with `current_revision + 1` read
-- from the row rather than supplied: ticking a follow-up IS a change to the
-- entry, so a subsequent PUT holding the pre-tick revision must conflict. The
-- caller inserts the matching revision row from the returned row's own
-- current_revision, so the unique index on (entry id, revision number) stays
-- the backstop it already was.
--
-- provenance/state are in the WHERE as well as in the handler's own 409 check:
-- the handler answers the distinct "Timeline entry is immutable" problem, and
-- this repeats the condition so a mistake at the call site matches zero rows
-- instead of ticking a follow-up on a generated or deleted entry.
UPDATE customers.customers_timeline_entries
SET follow_up_done_at = @now::timestamptz,
    current_revision = current_revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id
  AND provenance = 'manual' AND state = 'active'
  AND follow_up_on IS NOT NULL AND follow_up_done_at IS NULL
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;

-- name: ClearTimelineEntryFollowUpDone :one
-- ClearTimelineEntryFollowUpDone is DELETE .../follow-up/done: the reopen, and
-- the exact mirror of the statement above, down to why it has no
-- expected_revision and why it still bumps current_revision. Its own guard is
-- `follow_up_done_at IS NOT NULL`, so reopening an already-open follow-up
-- matches no row and the handler answers 200 with the entry unchanged.
UPDATE customers.customers_timeline_entries
SET follow_up_done_at = NULL,
    current_revision = current_revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id
  AND provenance = 'manual' AND state = 'active'
  AND follow_up_on IS NOT NULL AND follow_up_done_at IS NOT NULL
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;

-- name: FollowUpAttentionCandidates :many
-- FollowUpAttentionCandidates is /stats/attention's follow-up half (follow-ups
-- design D2): every open follow-up on a non-archived customer, due today or
-- earlier, that is the caller's or nobody's. The overdue-vs-due-today split is
-- NOT made here — it is one comparison against the same `today` the caller
-- already holds, and keeping it in Go keeps this query's answer a set of facts
-- rather than a set of verdicts, the division RegistryAttentionCandidates
-- states for the same endpoint.
--
-- caller_id is nullable, and the NULL case is honest rather than defensive:
-- with no signed-in user the equality is NULL, so only unassigned follow-ups
-- come back. The router admits no unauthenticated caller here, so that case is
-- unreachable through HTTP — but "everyone's follow-ups" is the one answer a
-- caller-dependent list must never give by accident.
--
-- The ordering is the design's (due date, then entry id) so repeated calls
-- cannot reshuffle ties, even though the handler sorts the merged list itself.
SELECT e.id AS entry_id, e.customer_id, c.name AS customer_name, e.follow_up_on
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL AND e.follow_up_done_at IS NULL
  AND e.follow_up_on <= @today::date
  AND c.status <> 'archived'
  AND (e.follow_up_assignee_user_id IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(caller_id)::uuid)
ORDER BY e.follow_up_on, e.id;

-- name: CountCustomerFollowUps :one
-- CountCustomerFollowUps is the total GET /customers/follow-ups paginates over
-- (follow-ups design D3), the same filters ListCustomerFollowUps applies below.
-- sqlc has no query fragments, so this WHERE clause and that one must be kept
-- textually identical BY HAND — a drift between them would make
-- pagination.totalCount disagree with what the page actually shows, which is
-- the reason CountCustomers says the same thing about itself.
--
-- follow_up_state is the design's four values. 'overdue' is a subset of 'open',
-- which is why it repeats the done test rather than standing alone; 'all' means
-- every follow-up whatever its state. The archived rule rides on the same
-- parameter: archived customers' follow-ups are excluded UNLESS the caller
-- asked for done ones, because a done follow-up is a record of work finished
-- and an archived customer's finished work is still finished.
SELECT count(*)
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL
  AND (@follow_up_state::text = 'all'
       OR (@follow_up_state::text = 'done' AND e.follow_up_done_at IS NOT NULL)
       OR (@follow_up_state::text = 'open' AND e.follow_up_done_at IS NULL)
       OR (@follow_up_state::text = 'overdue' AND e.follow_up_done_at IS NULL AND e.follow_up_on < @today::date))
  AND (@follow_up_state::text = 'done' OR c.status <> 'archived')
  AND (NOT @assignee_none::bool OR e.follow_up_assignee_user_id IS NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(customer_id)::int IS NULL OR e.customer_id = sqlc.narg(customer_id)::int);

-- name: ListCustomerFollowUps :many
-- ListCustomerFollowUps is GET /customers/follow-ups' page (design D3). The
-- WHERE clause is CountCustomerFollowUps's, kept textually identical (see its
-- comment).
--
-- OFFSET paging, not a keyset cursor, and that is the module's own precedent
-- rather than a shortcut: GET /customers and GET /customers/contacts both
-- answer `page`/`pageSize` with a PaginationMetadata block, the frontend's
-- Pagination control needs a total page count to render at all, and the design
-- asks for `page`/`pageSize` by name. The timeline's own feed is the keyset
-- one, because an infinite scroll has no page numbers to show.
--
-- The note is cut to 200 UTF-16 units by the CALLER (truncateUTF16), not here:
-- Postgres' left() counts characters, Go's count is UTF-16 code units, and the
-- two disagree on every astral character — the same reason the manual entry's
-- own 500-character summary is truncated in Go.
SELECT e.id AS entry_id, e.customer_id, c.name AS customer_name,
       e.event_type, e.occurred_on, e.note,
       e.follow_up_on, e.follow_up_assignee_user_id, e.follow_up_done_at
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL
  AND (@follow_up_state::text = 'all'
       OR (@follow_up_state::text = 'done' AND e.follow_up_done_at IS NOT NULL)
       OR (@follow_up_state::text = 'open' AND e.follow_up_done_at IS NULL)
       OR (@follow_up_state::text = 'overdue' AND e.follow_up_done_at IS NULL AND e.follow_up_on < @today::date))
  AND (@follow_up_state::text = 'done' OR c.status <> 'archived')
  AND (NOT @assignee_none::bool OR e.follow_up_assignee_user_id IS NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(customer_id)::int IS NULL OR e.customer_id = sqlc.narg(customer_id)::int)
ORDER BY e.follow_up_on, e.id
LIMIT @page_size::int OFFSET @row_offset::int;
