-- name: NextCounterValue :one
-- NextCounterValue allocates the next value of a named counter
-- (TN/TenantCounterService.cs), gapless per counter: the first call for a
-- counter starts it at 1; every later call increments it by one under the
-- same upsert, so two concurrent callers can never allocate the same value,
-- even if one of them later rolls back.
INSERT INTO customers.counters (counter_name, next_value)
VALUES (@counter_name, 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = customers.counters.next_value + 1
RETURNING next_value;

-- name: InsertCustomer :one
-- InsertCustomer creates a customer row. created_at and updated_at are the
-- same instant on creation, supplied by the caller from Deps.Clock().
INSERT INTO customers.customers (
    customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
    created_at, updated_at, type
) VALUES (
    @customer_number, @name, @status, @legal_country, @legal_id, @legal_name, @legal_source, @legal_type,
    @now::timestamptz, @now::timestamptz, @type
)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type;

-- name: GetCustomer :one
-- GetCustomer fetches one customer by id.
SELECT id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
       created_at, updated_at, type
FROM customers.customers
WHERE id = @id;

-- name: UpdateCustomer :one
-- UpdateCustomer applies PUT /customers/{id}'s validated fields
-- (UpdateCustomerEndpoint.cs:88-109): name, status, and the legal identity
-- (all five columns together, or all five NULL). updated_at is whatever the
-- caller computes it should be — the row's own timestamp when nothing
-- changed, now when it did — never a database default.
UPDATE customers.customers
SET name = @name,
    status = @status,
    legal_country = @legal_country,
    legal_id = @legal_id,
    legal_name = @legal_name,
    legal_source = @legal_source,
    legal_type = @legal_type,
    updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type;

-- name: SetCustomerStatus :one
-- SetCustomerStatus is DeleteCustomerEndpoint's archive transition
-- (DeleteCustomerEndpoint.cs:31-38): status and updated_at only, called
-- once the handler has confirmed the row is not archived already.
UPDATE customers.customers
SET status = @status, updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type;

-- name: SetCustomerType :one
-- SetCustomerType is PUT /customers/{id}/type's write: the customer type
-- and, because a legal identity of the old type makes no sense on the new
-- one, the five legal columns the handler passes (all NULL when it clears
-- the identity, the row's own values otherwise). updated_at is the
-- caller's, as for UpdateCustomer.
UPDATE customers.customers
SET type = @type,
    legal_country = @legal_country,
    legal_id = @legal_id,
    legal_name = @legal_name,
    legal_source = @legal_source,
    legal_type = @legal_type,
    updated_at = @updated_at::timestamptz
WHERE id = @id
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type;

-- name: CountCustomers :one
-- CountCustomers is the total row count GetCustomers paginates over
-- (GetCustomersEndpoint.cs:58), the same filters ListCustomersByID/ByName
-- apply below: archived customers excluded unless requested, and search
-- ILIKE-matching the name only (inventory oddity #2: the legal name and
-- legal id are never searched, despite GetCustomers's own stale doc
-- comment claiming otherwise; TS/CustomersEndpointsTests.cs's
-- GetCustomers_Search_MatchesLegalNameAndLegalIdCaseInsensitively pins the
-- absence).
SELECT count(*)
FROM customers.customers
WHERE (@include_archived::bool OR status <> 'archived')
  AND (sqlc.narg(search)::text IS NULL OR name ILIKE sqlc.narg(search)::text);

-- name: ListCustomersByID :many
-- ListCustomersByID is GetCustomers's default sort (id, ascending unless
-- descending is requested), one page of rows with each row's timeline
-- summary inlined (GetCustomersEndpoint.cs:60-103, SafeCustomerProjection).
SELECT c.id, c.customer_number, c.name, c.status, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source,
       c.legal_type, c.created_at, c.updated_at,
       (SELECT count(*) FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS entry_count,
       (SELECT max(e.occurred_on)::date FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS latest_occurred_on
FROM customers.customers c
WHERE (@include_archived::bool OR c.status <> 'archived')
  AND (sqlc.narg(search)::text IS NULL OR c.name ILIKE sqlc.narg(search)::text)
ORDER BY
    CASE WHEN NOT @descending::bool THEN c.id END ASC,
    CASE WHEN @descending::bool THEN c.id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: ListCustomersByName :many
-- ListCustomersByName is GetCustomers's sortBy=name path: name first, id as
-- the tie-break (.NET's ThenBy(c => c.Id)), same filters and pagination as
-- ListCustomersByID. Exactly one of the two CASE pairs below is non-null
-- for every row in a given call (the sort direction is a query-wide
-- parameter, not a per-row one), so the other pair contributes nothing to
-- the ordering.
SELECT c.id, c.customer_number, c.name, c.status, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source,
       c.legal_type, c.created_at, c.updated_at,
       (SELECT count(*) FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS entry_count,
       (SELECT max(e.occurred_on)::date FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS latest_occurred_on
FROM customers.customers c
WHERE (@include_archived::bool OR c.status <> 'archived')
  AND (sqlc.narg(search)::text IS NULL OR c.name ILIKE sqlc.narg(search)::text)
ORDER BY
    CASE WHEN NOT @descending::bool THEN c.name END ASC,
    CASE WHEN @descending::bool THEN c.name END DESC,
    CASE WHEN NOT @descending::bool THEN c.id END ASC,
    CASE WHEN @descending::bool THEN c.id END DESC
LIMIT @page_size::int OFFSET @row_offset::int;

-- name: CustomerTimelineSummary :one
-- CustomerTimelineSummary is SafeCustomerProjection.TimelineSummaryAsync's
-- row for a single customer (SafeCustomerProjection.cs): how many active
-- timeline entries it has, and the most recent occurred_on among them.
-- Aggregates with no GROUP BY always answer one row, zero matches included
-- (entry_count 0, latest_occurred_on NULL), so :one is safe here.
SELECT
    count(*) AS entry_count,
    max(occurred_on)::date AS latest_occurred_on
FROM customers.customers_timeline_entries
WHERE customer_id = @customer_id AND state = 'active';

-- name: InsertGeneratedTimelineEvent :exec
-- InsertGeneratedTimelineEvent is CustomerTimelineRecorder.Add
-- (SV/CustomerTimelineRecorder.cs:127-160): every generated event is
-- written with its first, and since generated entries are never mutated
-- afterward (inventory §2.4), only revision, in one statement.
-- provenance/producer/actor_kind/actor_display/current_revision/state are
-- the recorder's fixed constants, never caller-supplied.
WITH entry AS (
    INSERT INTO customers.customers_timeline_entries (
        customer_id, provenance, producer, event_type, occurred_on, occurred_at,
        summary, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
        created_at, updated_at
    ) VALUES (
        @customer_id::int, 'generated', 'customers.api', @event_type::text, @occurred_on::date, @now::timestamptz,
        @summary::text, @payload_json::jsonb, @payload_version::int, 1, 'active', 'system', 'System',
        @now::timestamptz, @now::timestamptz
    )
    RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
              source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
              created_at, updated_at
)
INSERT INTO customers.customers_timeline_entries_revisions (
    customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
    occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
    state, actor_kind, actor_display, created_at, updated_at
)
SELECT id, 1, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at
FROM entry;

-- name: CustomerKeyFigures :one
-- CustomerKeyFigures is GetCustomerStatsEndpoint's tenant-wide counts
-- (GetCustomerStatsEndpoint.cs:29-38): archived customers excluded from
-- every figure.
SELECT
    count(*) FILTER (WHERE status <> 'archived') AS total_count,
    count(*) FILTER (WHERE status = 'active') AS active_count,
    count(*) FILTER (WHERE status <> 'archived' AND created_at >= @since::timestamptz) AS new_last_30_days_count
FROM customers.customers;

-- name: CustomerIdentityFigures :one
-- CustomerIdentityFigures is GetCustomerStatsEndpoint's identity-derived
-- counts (GetCustomerStatsEndpoint.cs:48-62), only ever queried when the
-- caller holds legal-identity-view; archived customers excluded, as for
-- CustomerKeyFigures. business_count and person_count count the customer
-- type column (00007_customers_type.sql), not legal_type, so a customer
-- without an identity is still counted as what it is. legal_country IS
-- NULL stands in for "Identity is null": the five legal_* columns are
-- written all-or-nothing by this module's own handlers (inventory §2.1's
-- owned-type invariant, app-level only — see the schema migration's
-- comment).
SELECT
    count(*) FILTER (WHERE type = 'business') AS business_count,
    count(*) FILTER (WHERE type = 'person') AS person_count,
    count(*) FILTER (WHERE legal_country IS NULL) AS missing_identity_count,
    count(DISTINCT legal_country) AS distinct_country_count
FROM customers.customers
WHERE status <> 'archived';

-- name: CustomerStatsSummaryCustomerCounts :one
-- CustomerStatsSummaryCustomerCounts is CustomerStatsEndpoints.Summary's
-- customer-side counts (CustomerStatsEndpoints.cs:44-51): unlike
-- CustomerKeyFigures, the dashboard summary counts every status, archived
-- included — .NET's db.Customers here carries no status filter.
-- previous_from..@period_from is the immediately preceding window of the
-- same length as [period_from, period_to).
SELECT
    count(*) FILTER (WHERE status = 'active') AS active,
    count(*) FILTER (WHERE status = 'active' AND created_at < @period_from::timestamptz) AS active_at_period_start,
    count(*) FILTER (WHERE created_at >= @period_from::timestamptz AND created_at < @period_to::timestamptz) AS new_customers,
    count(*) FILTER (WHERE created_at >= @previous_from::timestamptz AND created_at < @period_from::timestamptz) AS previous_new_customers
FROM customers.customers;

-- name: CustomerStatsSummaryContactCounts :one
-- CustomerStatsSummaryContactCounts is CustomerStatsEndpoints.Summary's
-- contact-side counts (CustomerStatsEndpoints.cs:52-55).
SELECT
    count(*) FILTER (WHERE created_at >= @period_from::timestamptz AND created_at < @period_to::timestamptz) AS new_contacts,
    count(*) FILTER (WHERE created_at >= @previous_from::timestamptz AND created_at < @period_from::timestamptz) AS previous_new_contacts
FROM customers.contacts;

-- name: CustomerCreationBuckets :many
-- CustomerCreationBuckets is the timeseries's newCustomers metric
-- (CustomerStatsEndpoints.cs:88-98): one row per UTC calendar day with at
-- least one customer created in [range_from, range_to).
SELECT (created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM customers.customers
WHERE created_at >= @range_from::timestamptz AND created_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- name: ContactCreationBuckets :many
-- ContactCreationBuckets is the timeseries's newContacts metric
-- (CustomerStatsEndpoints.cs:100-108).
SELECT (created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM customers.contacts
WHERE created_at >= @range_from::timestamptz AND created_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- name: DirectoryCustomer :one
-- DirectoryCustomer is contracts.CustomerDirectory.Customer's row
-- (SV/ApplicationServiceCollectionExtensions.cs:22-28): a customer of any
-- status, archived included, since a consumer holding a historical reference
-- must still be able to name it.
SELECT id, name, status = 'archived' AS archived
FROM customers.customers
WHERE id = @id;

-- name: DirectoryContact :one
-- DirectoryContact is contracts.CustomerDirectory.Contact's row
-- (SV/ApplicationServiceCollectionExtensions.cs:30-32).
SELECT id, first_name, last_name, email
FROM customers.contacts
WHERE id = @id;

-- name: DirectoryContactsByEmail :many
-- DirectoryContactsByEmail is contracts.CustomerDirectory.ContactsByEmail's
-- rows (SV/ApplicationServiceCollectionExtensions.cs:41-104): one row per
-- contact whose canonical email, or any of whose customer-specific
-- association emails, is the given address, together with the customers that
-- match makes candidates. A contact matched on its canonical email offers
-- every customer it is linked to; one matched only through an association
-- offers just the customers whose association carries that address. The email
-- itself never leaves this query, so no caller can learn an address it did not
-- already have.
--
-- .NET compared stored values that were canonical by construction; here both
-- sides are lower-cased and trimmed, so a row stored in another casing still
-- resolves. Neither column is indexed (the .NET schema had no index either),
-- so the functions cost nothing a bare comparison would have saved.
WITH matching AS (
    SELECT c.id,
           coalesce(lower(btrim(c.email)) = @email::text, false) AS canonical
    FROM customers.contacts c
    WHERE lower(btrim(c.email)) = @email::text
       OR EXISTS (
           SELECT 1
           FROM customers.customers_contacts cc
           WHERE cc.contact_id = c.id
             AND lower(btrim(cc.email)) = @email::text
       )
)
SELECT m.id AS contact_id,
       coalesce(
           array_agg(cc.customer_id ORDER BY cc.customer_id) FILTER (WHERE cc.customer_id IS NOT NULL),
           '{}'
       )::integer[] AS candidate_customer_ids
FROM matching m
LEFT JOIN customers.customers_contacts cc
       ON cc.contact_id = m.id
      AND (m.canonical OR lower(btrim(cc.email)) = @email::text)
GROUP BY m.id
ORDER BY m.id;
