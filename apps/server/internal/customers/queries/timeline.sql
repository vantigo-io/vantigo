-- name: GetTimelineEntry :one
-- GetTimelineEntry fetches one entry by (customer, id) regardless of state
-- or provenance (TimelineEndpoints.cs:197-198 Update, :242-243 Delete): the
-- immutability check needs to see a generated or already-deleted entry to
-- answer its distinct 409, not a 404, so this is unfiltered on state.
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id
FROM customers.customers_timeline_entries
WHERE id = @id AND customer_id = @customer_id;

-- name: GetActiveTimelineEntry :one
-- GetActiveTimelineEntry is TimelineEndpoints.Get's lookup (:175-178): unlike
-- GetTimelineEntry above, a soft-deleted or generated-but-otherwise-fine
-- entry is state-filtered here too — Get only ever shows active entries;
-- CurrentRevision > 0 is not part of that filter, only State is.
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id
FROM customers.customers_timeline_entries
WHERE id = @id AND customer_id = @customer_id AND state = 'active';

-- name: ListTimelineEntries :many
-- ListTimelineEntries is TimelineEndpoints.List's feed query
-- (TimelineEndpoints.cs:76-125): active entries for one customer, in the
-- feed's newest-first order (occurredOn desc, entries carrying an instant
-- ordered before date-only ones, then that instant desc, then id desc as the
-- final tie-break). It fetches one row beyond the requested page size so the
-- handler can tell whether a next page exists without a second query.
-- has_cursor/cursor_has_occurred_at are two separate boolean flags (not one
-- nullable timestamp) because "the cursor's own entry had no occurredAt" is
-- itself part of the keyset predicate below, which a NULL parameter value
-- alone could not distinguish from "no cursor was given at all".
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id
FROM customers.customers_timeline_entries
WHERE customer_id = @customer_id::int
  AND state = 'active'
  AND (sqlc.narg(provenance)::text IS NULL OR provenance = sqlc.narg(provenance)::text)
  AND (@event_types::text[] IS NULL OR cardinality(@event_types::text[]) = 0 OR event_type = ANY (@event_types::text[]))
  AND (sqlc.narg(occurred_from)::date IS NULL OR occurred_on >= sqlc.narg(occurred_from)::date)
  AND (sqlc.narg(occurred_to)::date IS NULL OR occurred_on <= sqlc.narg(occurred_to)::date)
  AND (
    NOT @has_cursor::bool
    OR occurred_on < @cursor_occurred_on::date
    OR (
        occurred_on = @cursor_occurred_on::date
        AND (
            (@cursor_has_occurred_at::bool AND (
                occurred_at IS NULL
                OR occurred_at < @cursor_occurred_at::timestamptz
                OR (occurred_at = @cursor_occurred_at::timestamptz AND id < @cursor_id::int)
            ))
            OR (
                NOT @cursor_has_occurred_at::bool
                AND occurred_at IS NULL
                AND id < @cursor_id::int
            )
        )
    )
  )
ORDER BY occurred_on DESC, (occurred_at IS NOT NULL) DESC, occurred_at DESC, id DESC
LIMIT @take::int;

-- name: InsertManualTimelineEntry :one
-- InsertManualTimelineEntry is TimelineEndpoints.Create's write
-- (TimelineEndpoints.cs:144-167, CreateManualEntry :306-329): a manual entry
-- always starts at CurrentRevision 1, State active, with its first revision
-- row appended in the same statement — the same entry-then-revision CTE
-- shape as InsertGeneratedTimelineEvent (timeline_events.go), duplicated
-- here rather than shared because the two write entirely different constant
-- columns (provenance/producer) and take a client-supplied note/sourceUrl
-- that generated events never carry. actor_kind/actor_display/actor_user_id
-- are the caller's resolved actor (server.actorFor, customers foundation
-- design D1) — manualFallbackActor's 'unattributed'/'Unattributed'/NULL when
-- there is no user principal to attribute the write to.
WITH entry AS (
    INSERT INTO customers.customers_timeline_entries (
        customer_id, provenance, producer, event_type, occurred_on, occurred_at,
        summary, note, source_url, payload_version, current_revision, state, actor_kind, actor_display,
        actor_user_id, created_at, updated_at
    ) VALUES (
        @customer_id::int, 'manual', 'customers.api', @event_type::text, @occurred_on::date, @occurred_at,
        @summary::text, @note::text, @source_url, 1, 1, 'active', @actor_kind::text, @actor_display::text,
        @actor_user_id, @now::timestamptz, @now::timestamptz
    )
    RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
              source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
              created_at, updated_at, deleted_at, actor_user_id
), inserted_revision AS (
    INSERT INTO customers.customers_timeline_entries_revisions (
        customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
        occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
        state, actor_kind, actor_display, actor_user_id, created_at, updated_at
    )
    SELECT id, 1, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
           source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
           actor_user_id, created_at, updated_at
    FROM entry
)
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id
FROM entry;

-- name: UpdateManualTimelineEntry :one
-- UpdateManualTimelineEntry is TimelineEndpoints.Update's guarded write
-- (ApplyManual :331-341): the second of the three overlapping concurrency
-- guards customers inventory §4 describes. The caller has already compared
-- the loaded row's current_revision against the request's expectedRevision
-- itself (the first guard, entirely in Go, before this statement ever runs);
-- this statement repeats that same comparison as its own WHERE clause, so a
-- second writer that read the identical row concurrently and passed that
-- same Go-side check still cannot both win here — current_revision is the
-- optimistic-concurrency token, exactly as .NET's IsConcurrencyToken() WHERE
-- clause is. Zero rows affected (surfaced by :one as pgx.ErrNoRows) means a
-- concurrent writer committed first; the caller maps that to the same 409
-- "Timeline revision conflict" the Go-side pre-check answers, with .NET's
-- distinct DbUpdateConcurrencyException wording.
UPDATE customers.customers_timeline_entries
SET event_type = @event_type::text,
    occurred_on = @occurred_on::date,
    occurred_at = @occurred_at,
    note = @note::text,
    summary = @summary::text,
    source_url = @source_url,
    current_revision = @new_revision::int,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id AND current_revision = @expected_revision::int
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id;

-- name: SetTimelineEntryDeleted :one
-- SetTimelineEntryDeleted is TimelineEndpoints.Delete's guarded write
-- (:261-266): the soft-delete counterpart of UpdateManualTimelineEntry above,
-- guarded by the same current_revision token for the same reason.
UPDATE customers.customers_timeline_entries
SET state = 'deleted', deleted_at = @now::timestamptz, updated_at = @now::timestamptz, current_revision = @new_revision::int
WHERE id = @id AND customer_id = @customer_id AND current_revision = @expected_revision::int
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id;

-- name: InsertTimelineRevision :exec
-- InsertTimelineRevision is AddRevision (:343-364), called after either
-- guarded write above succeeds, from the row it just returned: a full
-- point-in-time snapshot at the new current_revision. Its uniqueness on
-- (customer_timeline_entry_id, revision_number) is the third, independent
-- concurrency guard customers inventory §4 describes — a collision here
-- means two writers both reached this insert claiming the same revision
-- number, which UpdateManualTimelineEntry/SetTimelineEntryDeleted's row-level
-- locking already makes unreachable in practice (whichever writer loses the
-- guarded UPDATE never gets here at all), but the constraint stands as the
-- belt-and-suspenders backstop .NET's own comment describes, not a chain
-- this code relies on for correctness.
--
-- actor_kind/actor_display/actor_user_id are the actor of *this* revision —
-- the caller resolved for the write that produced it — not copied from the
-- entry row (customers foundation design D1: an edit or delete by somebody
-- else than the entry's original author shows up in the history under their
-- own name, even though the entry row itself keeps its original author).
INSERT INTO customers.customers_timeline_entries_revisions (
    customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
    occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
    state, actor_kind, actor_display, actor_user_id, created_at, updated_at, deleted_at
) VALUES (
    @entry_id::int, @revision_number::int, @customer_id::int, @provenance::text, @producer::text, @event_type::text,
    @occurred_on::date, @occurred_at, @summary::text, @note, @source_url, @payload_json, @payload_version::int,
    @current_revision::int, @state::text, @actor_kind::text, @actor_display::text, @actor_user_id, @created_at::timestamptz,
    @updated_at::timestamptz, @deleted_at
);

-- name: ListTimelineRevisions :many
-- ListTimelineRevisions is TimelineEndpoints.Revisions' history query
-- (:283-304): every revision of one entry, oldest first, no paging (the
-- inventory notes this endpoint never paginates).
SELECT id, customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
       occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
       state, actor_kind, actor_display, created_at, updated_at, deleted_at, actor_user_id
FROM customers.customers_timeline_entries_revisions
WHERE customer_timeline_entry_id = @entry_id
ORDER BY revision_number;
