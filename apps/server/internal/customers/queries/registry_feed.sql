-- name: EnsureRegistryFeedCursor :exec
-- EnsureRegistryFeedCursor plants the one cursor row the first time a cycle
-- runs (registry workers design D1), stamping the moment this installation
-- joined the feed. ON CONFLICT DO NOTHING rather than an upsert: started_at
-- is the feed's own starting point and must never move, or a replica that
-- started later would re-join the feed after the updates an earlier one had
-- already seen but not yet processed.
INSERT INTO customers.registry_feed_cursor (id, started_at)
VALUES (1, @started_at::timestamptz)
ON CONFLICT (id) DO NOTHING;

-- name: GetRegistryFeedCursor :one
-- GetRegistryFeedCursor is where to resume from. Read under the worker's
-- advisory lease and nowhere else, so it needs no lock of its own: the lease
-- is what makes one replica at a time the only reader and writer of this row.
SELECT id, next_update_id, started_at, last_polled_at, last_update_at, backfill_after_id
FROM customers.registry_feed_cursor
WHERE id = 1;

-- name: AdvanceRegistryFeedCursor :exec
-- AdvanceRegistryFeedCursor is the commit of one processed page (design D1):
-- run only after every matched customer on that page has had its hint written
-- and its refresh attempted, so a cycle that dies halfway re-reads the same
-- page rather than skipping it. next_update_id is the page's highest id PLUS
-- ONE, because oppdateringsid is inclusive.
UPDATE customers.registry_feed_cursor
SET next_update_id = @next_update_id,
    last_update_at = @last_update_at::timestamptz,
    last_polled_at = @last_polled_at::timestamptz
WHERE id = 1;

-- name: TouchRegistryFeedCursor :exec
-- TouchRegistryFeedCursor records that a cycle read the feed and found
-- nothing to process. It deliberately moves neither next_update_id nor
-- last_update_at: there was no entry, so there is no new position and no new
-- registry timestamp to claim — only the fact that this installation is still
-- asking, which is what tells an operator the worker is alive on an idle day.
UPDATE customers.registry_feed_cursor
SET last_polled_at = @last_polled_at::timestamptz
WHERE id = 1;

-- name: SetRegistryUpdatedHint :exec
-- SetRegistryUpdatedHint is "the feed said there is something newer" (design
-- D2), written on the record row BEFORE the refresh is attempted and in its
-- own statement, so that a refresh which then fails leaves hint > fetched_at
-- — the single definition of stale, and the only reason
-- StaleRegistryRecords below finds it again.
--
-- The hint never moves backwards: an older entry arriving after a newer one
-- (a page processed out of order, a backlog caught up in two cycles) must not
-- make a record look fresher than the registry said it was. A customer with
-- no record row has nowhere to keep a hint, and this UPDATE touching no row
-- is exactly right for that case — the refresh is still attempted, and the
-- backfill below is what retries it if that fails.
UPDATE customers.customer_registry_records
SET registry_updated_hint = @hint::timestamptz
WHERE customer_id = @customer_id
  AND (registry_updated_hint IS NULL OR registry_updated_hint < @hint::timestamptz);

-- name: CustomersByOrganisationNumbers :many
-- CustomersByOrganisationNumbers is one feed page intersected with this
-- installation (design D1): the unfiltered scan is matched LOCALLY, so the
-- whole register's churn costs one request and one indexed lookup rather than
-- a filtered request per chunk with no safe cursor.
--
-- The predicates are the same three registryOrganisationNumber applies in Go
-- (Norwegian business, non-archived), minus the organisation number's own
-- validity, which SQL cannot judge — the caller re-asks Go for that, so a
-- legacy legal_id like 'NO 923 609 016 MVA' is never refreshed for.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customers c
WHERE c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND c.legal_id = ANY(@legal_ids::text[])
ORDER BY c.id;

-- name: StaleRegistryRecords :many
-- StaleRegistryRecords is the sweep's first half (design D3): records the feed
-- said were out of date and whose refresh has not succeeded since. hint >
-- fetched_at IS the definition of stale — a successful refresh leaves
-- fetched_at at or past the hint, a failed one leaves the hint standing — so
-- this needs no separate retry ledger and no attempt counter: the row itself
-- remembers.
--
-- Oldest hint first, so a record that has been failing longest is asked about
-- before one that just changed. The organisation-number equality is the same
-- belt-and-braces RegistryAttentionCandidates applies: a row that outlived
-- the identity it was fetched for is nobody's to refresh.
SELECT r.customer_id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customer_registry_records r
JOIN customers.customers c ON c.id = r.customer_id
WHERE r.registry_updated_hint IS NOT NULL
  AND r.registry_updated_hint > r.fetched_at
  AND c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND r.organisation_number = c.legal_id
ORDER BY r.registry_updated_hint, r.customer_id
LIMIT @row_limit::int;

-- name: CustomersWithoutRegistryRecord :many
-- CustomersWithoutRegistryRecord is the sweep's second half — the backfill
-- (design D3): Norwegian business customers that have no record at all,
-- because they were created before delivery A existed or because their first
-- fetch failed.
--
-- @after_id is what keeps it from starving (the migration's own comment on
-- backfill_after_id): a customer whose organisation number the register does
-- not know never gets a record, so "the 25 lowest ids with no record" would be
-- the same 25 rows on every cycle and the 26th would never be reached. The
-- sweep walks past its last attempt instead and starts over at 0 when a batch
-- comes back short.
--
-- The nine-digit predicate is the other half of that: a legacy legal_id like
-- 'NO 923 609 016 MVA' can never be looked up, and leaving it in the candidate
-- set would spend one of the 25 places on it each pass. It is a shape check
-- only — the check digit is Go's (registryOrganisationNumber), which is also
-- why the worker re-asks rather than trusting this predicate.
--
-- A backfilled record goes through the ordinary first-fetch diff, so a
-- hand-typed name that differs from the registry's raises registryRenamed
-- exactly as a click would — this query is not a special path, it only finds
-- the customers nobody has clicked for.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customers c
LEFT JOIN customers.customer_registry_records r ON r.customer_id = c.id
WHERE r.customer_id IS NULL
  AND c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND c.legal_id ~ '^[0-9]{9}$'
  AND c.id > @after_id
ORDER BY c.id
LIMIT @row_limit::int;

-- name: SetRegistryBackfillPosition :exec
-- SetRegistryBackfillPosition stores where the backfill got to, so the next
-- cycle continues instead of re-reading the same page (design D3): the batch's
-- last id after a full batch, or 0 after a short one — a short batch means the
-- end of the installation, and the next pass starts from the front.
--
-- A cycle cancelled part-way through its batch writes nothing here at all: the
-- position means "everything up to here has been attempted this pass", and the
-- customers such a cycle never reached are next CYCLE's, not next pass's.
UPDATE customers.registry_feed_cursor
SET backfill_after_id = @after_id
WHERE id = 1;
