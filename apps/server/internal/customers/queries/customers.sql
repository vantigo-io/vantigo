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
-- email/phone/website are the customer's own contact info (invoice-ready
-- customer design D2), NULL when PostCustomers received none.
INSERT INTO customers.customers (
    customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
    created_at, updated_at, type, email, phone, website
) VALUES (
    @customer_number, @name, @status, @legal_country, @legal_id, @legal_name, @legal_source, @legal_type,
    @now::timestamptz, @now::timestamptz, @type, @email, @phone, @website
)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website;

-- name: GetCustomer :one
-- GetCustomer fetches one customer by id.
SELECT id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
       created_at, updated_at, type, revision, email, phone, website, owner_user_id
FROM customers.customers
WHERE id = @id;

-- name: CustomersByLegalIdentity :many
-- CustomersByLegalIdentity is the duplicate-legal-identity conflict check
-- (customers foundation design D6): any customer — of any status, archived
-- included, since the right move for one is usually to restore it rather
-- than create a second — already holding (@country, @legal_id), other than
-- @exclude_id itself. Country and id are compared as stored, i.e. already
-- normalised by validateLegalIdentity (lower-cased country, stripped
-- Norwegian org number), so this is a plain equality, not another ILIKE.
-- Ordered by id and capped at 5: the conflict body only ever names a
-- handful of holders (CustomerConflictProblem.duplicates), never every one.
-- No unique index backs this — the design deliberately allows a race
-- between two simultaneous creates; allowDuplicateIdentity is how a caller
-- who knows better goes ahead anyway.
SELECT id, customer_number, name, status
FROM customers.customers
WHERE legal_country = @country::text AND legal_id = @legal_id::text AND id <> @exclude_id::int
ORDER BY id
LIMIT 5;

-- name: UpdateCustomer :one
-- UpdateCustomer applies PUT /customers/{id}'s validated fields
-- (UpdateCustomerEndpoint.cs:88-109): name, status, and the legal identity
-- (all five columns together, or all five NULL). updated_at is whatever the
-- caller computes it should be — the row's own timestamp when nothing
-- changed, now when it did — never a database default. revision always
-- advances by one on every execution of this statement (customers
-- foundation design D5): the caller (customers.go, legal_identity.go)
-- decides in Go whether to run it at all, exactly as it already decides
-- updated_at. sqlc.narg(expected_revision) is the optimistic-concurrency
-- guard PUT /customers/{id} supplies; the legal-identity writes leave it
-- NULL, an unconditional write that always succeeds while the row exists.
-- email/phone/website (invoice-ready customer design D2) are never in this
-- statement's SET list — PUT /customers/{id} does not touch contact info
-- (D1: it is its own sub-resource) — so RETURNING them here only reports
-- whatever the row already has, unchanged by this write.
UPDATE customers.customers
SET name = @name,
    status = @status,
    legal_country = @legal_country,
    legal_id = @legal_id,
    legal_name = @legal_name,
    legal_source = @legal_source,
    legal_type = @legal_type,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;

-- name: SetCustomerStatus :one
-- SetCustomerStatus is DeleteCustomerEndpoint's archive transition
-- (DeleteCustomerEndpoint.cs:31-38): status and updated_at only, called
-- once the handler has confirmed the row is not archived already. revision
-- advances by one, unconditionally (customers foundation design D5: archive
-- is idempotent by construction — the handler never calls this on an
-- already-archived row — so it needs no revision guard of its own).
UPDATE customers.customers
SET status = @status, updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website;

-- name: SetCustomerType :one
-- SetCustomerType is PUT /customers/{id}/type's write: the customer type
-- and, because a legal identity of the old type makes no sense on the new
-- one, the five legal columns the handler passes (all NULL when it clears
-- the identity, the row's own values otherwise). updated_at is the
-- caller's, as for UpdateCustomer. revision advances by one on every
-- execution, guarded the same way UpdateCustomer's is (customers
-- foundation design D5) — the handler skips calling this entirely when the
-- requested type is already the customer's own.
UPDATE customers.customers
SET type = @type,
    legal_country = @legal_country,
    legal_id = @legal_id,
    legal_name = @legal_name,
    legal_source = @legal_source,
    legal_type = @legal_type,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;

-- name: UpdateCustomerContactInfo :one
-- UpdateCustomerContactInfo is PUT /customers/{id}/contact-info's write
-- (invoice-ready customer design D1, D2): a full replace of the three
-- contact-info columns only — name, status and the legal identity are
-- untouched, since this sub-resource never writes them. Guarded and
-- revision-bumping exactly like UpdateCustomer/SetCustomerType above:
-- sqlc.narg(expected_revision) is PutCustomerContactInfoRequest's optional
-- revision, and the caller (contact_info.go) skips calling this entirely
-- when nothing about the contact info actually changed.
UPDATE customers.customers
SET email = @email,
    phone = @phone,
    website = @website,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;

-- name: UpdateCustomerOwner :one
-- UpdateCustomerOwner is PUT /customers/{id}/owner's write (owner and tags
-- design D1): the owner column only — name, status, type, the legal identity
-- and the contact info are untouched, since this sub-resource never writes
-- them. Guarded and revision-bumping exactly like UpdateCustomerContactInfo
-- above: sqlc.narg(expected_revision) is PutCustomerOwnerRequest's optional
-- revision, and the caller (owner.go) skips calling this entirely when the
-- owner did not actually change, so a resubmit of the current owner writes
-- nothing and bumps nothing (customers foundation design D5's no-op rule).
--
-- @owner_user_id is NULL to clear the owner, which is a real change like any
-- other: it bumps the revision and records customer.owner_changed.
UPDATE customers.customers
SET owner_user_id = @owner_user_id,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;

-- name: CustomerExists :one
-- CustomerExists is PUT /customers/{id}/tags's 404 check (owner and tags
-- design D2), and deliberately not LockCustomer (queries/addresses.sql): a
-- tag set-replace is off the customer row, so it takes no lock on it and no
-- revision — two concurrent replaces are last-wins, which is what replacing a
-- set means. pgx.ErrNoRows means the customer does not exist.
SELECT id FROM customers.customers WHERE id = @id;

-- name: GetCustomerBillingProfile :one
-- GetCustomerBillingProfile is GET /customers/{id}/billing-profile's read
-- (invoice-ready customer design D1, D4): the ten billing columns plus
-- everything billingWarnings (billing_profile.go) needs to compute its
-- warnings at read time — the customer's type and legal identity (the
-- ehf_without_recipient check) and its own contact-info email (the
-- email_without_address check) — in one round trip, without ever adding a
-- billing column to GetCustomer/ListCustomers's own SELECT list (the
-- controller ruling: the billing profile must never reach
-- SafeCustomerResponse).
SELECT id, revision, type, legal_country, legal_id, legal_name, legal_source, legal_type, email,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference
FROM customers.customers
WHERE id = @id;

-- name: UpdateCustomerBillingProfile :one
-- UpdateCustomerBillingProfile is PUT /customers/{id}/billing-profile's
-- write (invoice-ready customer design D1, D4): a full replace of the ten
-- billing columns only — name, status, type and the legal identity are
-- untouched, since this sub-resource never writes them. Guarded and
-- revision-bumping exactly like UpdateCustomerContactInfo above; RETURNING
-- mirrors GetCustomerBillingProfile's own column list, so the handler can
-- recompute warnings from the same row shape after the write.
UPDATE customers.customers
SET invoice_email = @invoice_email,
    reminder_email = @reminder_email,
    payment_terms_days = @payment_terms_days,
    currency = @currency,
    language = @language,
    invoice_delivery = @invoice_delivery,
    reminder_delivery = @reminder_delivery,
    peppol_id = @peppol_id,
    gln = @gln,
    buyer_reference = @buyer_reference,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, revision, type, legal_country, legal_id, legal_name, legal_source, legal_type, email,
          invoice_email, reminder_email, payment_terms_days, currency, language,
          invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference;

-- name: CountCustomers :one
-- CountCustomers is the total row count GetCustomers paginates over
-- (GetCustomersEndpoint.cs:58), the same filters ListCustomers applies
-- below. sqlc has no query fragments, so this WHERE clause and
-- ListCustomers's must be kept textually identical by hand — a drift
-- between them would make pagination.totalCount disagree with what the
-- page actually shows.
--
-- status/customer_type narrow to exactly that value when given; absent
-- status keeps today's rule (archived hidden unless include_archived).
-- search matches the name, the customer number (also against
-- search_compact, so "923 609 016" finds a legal id stored as
-- "923609016"), the customer's own email and, compacted with its
-- whitespace stripped the same way search_compact strips the caller's, its
-- own phone (invoice-ready customer design D2) — gated by search_phone
-- (final review fix M2), computed in Go from the compact term carrying at
-- least three ASCII digits (searchPhoneEligible, customers.go): a short
-- numeric term like "1" or a customer-number/legal-id fragment would
-- otherwise ILIKE-match nearly every phone number's compacted form by
-- accident, so the phone branch only activates once the term looks enough
-- like an actual phone-number fragment — and, only when the caller may see
-- that data (search_identity/search_contacts — customers foundation design
-- D4), the legal name/id and any linked contact's name/email: a caller
-- lacking those permissions gets exactly today's name-and-number behaviour,
-- never an oracle for data the response would withhold.
--
-- owner_none/owner_id are the two halves of design D1's ownerId filter,
-- because they are genuinely different questions: 'none' is "no owner at all"
-- (a NULL test, which no equality can express) and a uuid — including the one
-- 'me' resolved to in Go — is an equality. They are never both set: the Go
-- validation turns exactly one ownerId value into exactly one of them. tag_id
-- is design D2's single-tag filter: a customer matches when it carries that
-- tag, and multi-tag filtering is not built until someone asks.
SELECT count(*)
FROM customers.customers c
WHERE (
        (sqlc.narg(status)::text IS NOT NULL AND c.status = sqlc.narg(status)::text)
     OR (sqlc.narg(status)::text IS NULL AND (@include_archived::bool OR c.status <> 'archived'))
      )
  AND (sqlc.narg(customer_type)::text IS NULL OR c.type = sqlc.narg(customer_type)::text)
  AND (NOT @owner_none::bool OR c.owner_user_id IS NULL)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR c.owner_user_id = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
        SELECT 1
        FROM customers.customer_tags cft
        WHERE cft.customer_id = c.id
          AND cft.tag_id = sqlc.narg(tag_id)::uuid))
  AND (
        sqlc.narg(search)::text IS NULL
     OR c.name ILIKE sqlc.narg(search)::text
     OR c.customer_number::text ILIKE sqlc.narg(search_compact)::text
     OR c.email ILIKE sqlc.narg(search)::text
     OR (@search_phone::bool AND regexp_replace(c.phone, '\s', '', 'g') ILIKE sqlc.narg(search_compact)::text)
     OR (@search_identity::bool AND (
            c.legal_name ILIKE sqlc.narg(search)::text
         OR c.legal_id ILIKE sqlc.narg(search_compact)::text))
     OR (@search_contacts::bool AND EXISTS (
            SELECT 1
            FROM customers.customers_contacts cc
            JOIN customers.contacts ct ON ct.id = cc.contact_id
            WHERE cc.customer_id = c.id
              AND (ct.first_name ILIKE sqlc.narg(search)::text
                OR ct.last_name ILIKE sqlc.narg(search)::text
                OR (ct.first_name || ' ' || ct.last_name) ILIKE sqlc.narg(search)::text
                OR ct.email ILIKE sqlc.narg(search)::text
                OR cc.email ILIKE sqlc.narg(search)::text)))
      );

-- name: ListCustomers :many
-- ListCustomers is GetCustomers's one list query (customers foundation
-- design D4): where two near-identical queries stood before
-- (ListCustomersByID/ListCustomersByName, one per sortBy value), sortBy now
-- has five values, so the sort key becomes a query parameter instead —
-- one page of rows with each row's timeline summary inlined
-- (GetCustomersEndpoint.cs:60-103, SafeCustomerProjection). The WHERE
-- clause is CountCustomers's, kept textually identical (see its comment).
--
-- sort_by picks which CASE pair actually contributes a value to ORDER BY;
-- the other four contribute NULL to every row, so they change nothing
-- about the ordering (unlike a per-row expression, this is a query-wide
-- choice, made once). id, name, customer_number, created_at and updated_at
-- each need their own CASE pair — one CASE cannot mix a bigint, a text and
-- a timestamptz branch — and c.id is always the final tie-break, in
-- whatever direction @descending asks for, the same shape
-- ListCustomersByName's name-then-id ordering had.
SELECT c.id, c.customer_number, c.name, c.status, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source,
       c.legal_type, c.created_at, c.updated_at, c.revision, c.email, c.phone, c.website, c.owner_user_id,
       (SELECT count(*) FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS entry_count,
       (SELECT max(e.occurred_on)::date FROM customers.customers_timeline_entries e
         WHERE e.customer_id = c.id AND e.state = 'active') AS latest_occurred_on
FROM customers.customers c
WHERE (
        (sqlc.narg(status)::text IS NOT NULL AND c.status = sqlc.narg(status)::text)
     OR (sqlc.narg(status)::text IS NULL AND (@include_archived::bool OR c.status <> 'archived'))
      )
  AND (sqlc.narg(customer_type)::text IS NULL OR c.type = sqlc.narg(customer_type)::text)
  AND (NOT @owner_none::bool OR c.owner_user_id IS NULL)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR c.owner_user_id = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
        SELECT 1
        FROM customers.customer_tags cft
        WHERE cft.customer_id = c.id
          AND cft.tag_id = sqlc.narg(tag_id)::uuid))
  AND (
        sqlc.narg(search)::text IS NULL
     OR c.name ILIKE sqlc.narg(search)::text
     OR c.customer_number::text ILIKE sqlc.narg(search_compact)::text
     OR c.email ILIKE sqlc.narg(search)::text
     OR (@search_phone::bool AND regexp_replace(c.phone, '\s', '', 'g') ILIKE sqlc.narg(search_compact)::text)
     OR (@search_identity::bool AND (
            c.legal_name ILIKE sqlc.narg(search)::text
         OR c.legal_id ILIKE sqlc.narg(search_compact)::text))
     OR (@search_contacts::bool AND EXISTS (
            SELECT 1
            FROM customers.customers_contacts cc
            JOIN customers.contacts ct ON ct.id = cc.contact_id
            WHERE cc.customer_id = c.id
              AND (ct.first_name ILIKE sqlc.narg(search)::text
                OR ct.last_name ILIKE sqlc.narg(search)::text
                OR (ct.first_name || ' ' || ct.last_name) ILIKE sqlc.narg(search)::text
                OR ct.email ILIKE sqlc.narg(search)::text
                OR cc.email ILIKE sqlc.narg(search)::text)))
      )
ORDER BY
    CASE WHEN @sort_by::text = 'id' AND NOT @descending::bool THEN c.id END ASC,
    CASE WHEN @sort_by::text = 'id' AND @descending::bool THEN c.id END DESC,
    CASE WHEN @sort_by::text = 'name' AND NOT @descending::bool THEN c.name END ASC,
    CASE WHEN @sort_by::text = 'name' AND @descending::bool THEN c.name END DESC,
    CASE WHEN @sort_by::text = 'customerNumber' AND NOT @descending::bool THEN c.customer_number END ASC,
    CASE WHEN @sort_by::text = 'customerNumber' AND @descending::bool THEN c.customer_number END DESC,
    CASE WHEN @sort_by::text = 'createdAt' AND NOT @descending::bool THEN c.created_at END ASC,
    CASE WHEN @sort_by::text = 'createdAt' AND @descending::bool THEN c.created_at END DESC,
    CASE WHEN @sort_by::text = 'updatedAt' AND NOT @descending::bool THEN c.updated_at END ASC,
    CASE WHEN @sort_by::text = 'updatedAt' AND @descending::bool THEN c.updated_at END DESC,
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
-- provenance/current_revision/state are the recorder's fixed constants,
-- never caller-supplied; actor_kind/actor_display/actor_user_id are the
-- acting user the caller resolved (server.actorFor, customers foundation
-- design D1) — generatedFallbackActor's 'system'/'System'/NULL when there is
-- no user principal to attribute the event to. producer is the one column a
-- caller chooses: 'customers.api' for everything this module's own
-- endpoints do to a customer, 'customers.brreg' for what the registry says
-- (Brreg in full design D4, timeline_events.go's two constants).
WITH entry AS (
    INSERT INTO customers.customers_timeline_entries (
        customer_id, provenance, producer, event_type, occurred_on, occurred_at,
        summary, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
        actor_user_id, created_at, updated_at
    ) VALUES (
        @customer_id::int, 'generated', @producer::text, @event_type::text, @occurred_on::date, @now::timestamptz,
        @summary::text, @payload_json::jsonb, @payload_version::int, 1, 'active', @actor_kind::text, @actor_display::text,
        @actor_user_id, @now::timestamptz, @now::timestamptz
    )
    RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
              source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
              created_at, updated_at, actor_user_id
)
INSERT INTO customers.customers_timeline_entries_revisions (
    customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
    occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
    state, actor_kind, actor_display, actor_user_id, created_at, updated_at
)
SELECT id, 1, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       actor_user_id, created_at, updated_at
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

-- name: DirectoryCustomers :many
-- DirectoryCustomers is contracts.CustomerDirectory.Customers' rows: every
-- customer of any status, archived included, whose id is in @ids — the
-- batch twin of DirectoryCustomer above, for a caller naming a whole page of
-- customers in one round trip instead of one per row. A duplicate id in @ids
-- matches the same row more than once in the WHERE clause but the row itself
-- only exists once, so the result never repeats a customer; an id nobody has
-- is simply absent, not an error. Ordered by id, not by @ids' own order, so
-- two callers asking for the same set always see it the same way.
SELECT id, name, status = 'archived' AS archived
FROM customers.customers
WHERE id = ANY(@ids::int[])
ORDER BY id;

-- name: DirectoryBillingProfile :one
-- DirectoryBillingProfile is contracts.CustomerDirectory.BillingProfile's
-- customer row (invoice-ready customer design D5): identity (all five
-- columns, so identityFromRow's all-or-none invariant holds the same way it
-- does everywhere else this module reads it), contact email and the ten
-- billing columns GetCustomerBillingProfile itself selects, plus
-- customer_number and status — what resolveBillingProfile (directory.go)
-- needs to fill in every field of contracts.CustomerBillingProfile except
-- the resolved invoice address, which is DirectoryInvoiceAddress's own
-- query (queries/addresses.sql), a second round trip rather than a join:
-- at most one row either way, and a join would return no row at all for a
-- customer with no address, which pgx.ErrNoRows already means "no such
-- customer" for the :one shape this query needs.
SELECT id, customer_number, name, type, status = 'archived' AS archived,
       legal_country, legal_id, legal_name, legal_source, legal_type, email,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference
FROM customers.customers
WHERE id = @id;
