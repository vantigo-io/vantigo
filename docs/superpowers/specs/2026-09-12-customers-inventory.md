# Customers module — behavioural inventory for the Go port

Input to the Go port of the .NET Customers module. Describes what the module *does* beyond
`apps/server/internal/openapi/specs/customers.yaml` (29 operations, all `x-vantigo-access: permission:...`)
so the port reproduces it. Tenancy is dropped; every tenancy mechanism is called out but not detailed.

Path abbreviations: `EP/` = `apps/customers/backend/Customers.Module/Endpoints/`, `DM/` = `…/Domain/`,
`DB/` = `…/Database/`, `AZ/` = `…/Authorization/`, `SV/` = `…/Services/`,
`TS/` = `apps/customers/backend/Customers.Module.Tests/`, `CT/` = `packages/contracts/Vantigo.Contracts/`,
`CFG/` = `packages/configuration/Vantigo.Configuration/`, `TN/` = `packages/tenancy/Vantigo.Tenancy.EntityFramework/`,
`SPEC` = `apps/server/internal/openapi/specs/customers.yaml`.

Access mix (29 ops, `x-vantigo-access`): every operation is `permission:<key>` (single key or `+`-joined pair —
the pair form means "require both permissions", mirroring two `.RequirePermission()` calls on the same route,
`EP/CustomersEndpoints.cs:37-40`). No op is `anonymous`, `session`, `scim`, or a bare `policy:`. There is **no
tenancy op** in this contract at all (unlike identity's `/session/tenant`, `/admin/tenants/*`); the only tenancy
surface is internal (row `tenant_id` columns, RLS, per-tenant counters — §3, §4).

**Route/contract parity**: all 29 `.NET` routes map 1:1 onto the 29 contract `operationId`s with matching method,
path, and access string (verified pair-by-pair below in §1). No orphan endpoint either direction.

## 1. Endpoints

Two top-level route groups are mounted under `/api/v{version:apiVersion}/customers` (`EP/VersionedBusinessEndpointExtensions.cs:10-21`,
version fixed at 1; `/api/v2/...` and other versions 404, confirmed by `TS/CustomersEndpointsTests.cs:511-519`):
`MapCustomersEndpoints` (bare group, tag "Customers") and `MapContactsEndpoints` (group `/contacts`, tag "Contacts"),
plus `MapLookupEndpoints` (`/lookup`). `CustomerStatsEndpoints` is mounted as a sub-call inside the Customers group
(`EP/CustomersEndpoints.cs:26`), not its own top-level group.

Every list/get/put/delete on a resource that doesn't exist returns bare **404 with no body** (ASP.NET's
`TypedResults.NotFound()` — ProducesProblem is not used for 404s anywhere in this module, only for 400/409/502).
Every 400 from field-level validation is `HttpValidationProblemDetails` (`TypedResults.ValidationProblem`, RFC7807
`errors: {field: [msgs]}`); every ad-hoc business-rule 400/409/502 is a plain `ProblemDetails` (`TypedResults.Problem`,
`title`/`detail`/`status`, **no machine-readable code field** — contrast identity's `{code,message}` bodies). This
is a systemic difference the porter must reproduce: Customers has no `error.code` strings at all; callers key off
HTTP status and `problem.title`/`detail` text.

### 1.1 Customers (`EP/CustomersEndpoints.cs`)

| Method & path | operationId | Permission(s) | Handler | Notes |
|---|---|---|---|---|
| GET `/` | getCustomers | `view` | `GetCustomersEndpoint.cs:23` | pagination, sort, search, archived filter |
| GET `/stats` | getCustomersStats | `view` | `GetCustomerStatsEndpoint.cs:20` | tenant-wide key figures |
| GET `/stats/summary` | getCustomersStatsSummary | `view` | `CustomerStatsEndpoints.cs:32` | dashboard summary |
| GET `/stats/timeseries` | getCustomersStatsTimeseries | `view` | `CustomerStatsEndpoints.cs:70` | daily buckets |
| GET `/stats/attention` | getCustomersStatsAttention | `view` | `CustomerStatsEndpoints.cs:111` | **hardcoded `[]`, unimplemented** |
| POST `/` | postCustomers | `create` (+`legal-identity-manage` iff `identity` supplied) | `CreateCustomerEndpoint.cs:24` | |
| GET `/{id}` | getCustomer | `view` | `GetCustomerEndpoint.cs:17` | |
| PUT `/{id}` | putCustomersById | `update`+`view` (+`legal-identity-manage` iff `identity` supplied) | `UpdateCustomerEndpoint.cs:23` | |
| DELETE `/{id}` | deleteCustomersById | `delete` | `DeleteCustomerEndpoint.cs:19` | archives, not deletes |
| GET `/{id}/legal-identity` | getCustomersByIdLegalIdentity | `legal-identity-view` | `LegalIdentityEndpoints.cs:13` | 200 or 204 |
| PUT `/{id}/legal-identity` | putCustomersByIdLegalIdentity | `legal-identity-manage`+`legal-identity-view` | `LegalIdentityEndpoints.cs:31` | full replace |
| DELETE `/{id}/legal-identity` | deleteCustomersByIdLegalIdentity | `legal-identity-manage` | `LegalIdentityEndpoints.cs:61` | idempotent |
| GET `/{id}/contacts` | getCustomersByIdContacts | `associations-view`+`contacts-view` | `GetCustomerContactsEndpoint.cs:15` | |
| POST `/{id}/contacts` | postCustomersByIdContacts | `associations-manage`+`contacts-view` | `AttachCustomerContactEndpoint.cs:18` | 409 if already attached |
| PUT `/{id}/contacts/{contactId}` | putCustomersByIdContactsByContactId | `associations-manage`+`contacts-view` | `UpdateCustomerContactEndpoint.cs:16` | |
| DELETE `/{id}/contacts/{contactId}` | deleteCustomersByIdContactsByContactId | `associations-manage` | `DetachCustomerContactEndpoint.cs:15` | keeps the contact |
| GET `/{id}/timeline` | getCustomersByIdTimeline | `timeline-view` | `TimelineEndpoints.List` (`:31`) | keyset pagination |
| POST `/{id}/timeline` | postCustomersByIdTimeline | `timeline-manage`+`timeline-view` | `TimelineEndpoints.Create` (`:144`) | manual entry only |
| GET `/{id}/timeline/{entryId}` | getCustomersByIdTimelineByEntryId | `timeline-view` | `TimelineEndpoints.Get` (`:169`) | |
| PUT `/{id}/timeline/{entryId}` | putCustomersByIdTimelineByEntryId | `timeline-manage`+`timeline-view` | `TimelineEndpoints.Update` (`:185`) | optimistic concurrency |
| DELETE `/{id}/timeline/{entryId}` | deleteCustomersByIdTimelineByEntryId | `timeline-manage` | `TimelineEndpoints.Delete` (`:235`) | soft delete, optimistic concurrency |
| GET `/{id}/timeline/{entryId}/revisions` | getCustomersByIdTimelineByEntryIdRevisions | `timeline-view` | `TimelineEndpoints.Revisions` (`:283`) | full history, no paging |

### 1.2 Contacts (`EP/ContactsEndpoints.cs`, group `/contacts`)

| Method & path | operationId | Permission(s) | Handler |
|---|---|---|---|
| GET `/` | getCustomersContacts | `contacts-view`+`associations-view` | `GetContactsEndpoint.cs:21` |
| POST `/` | postCustomersContacts | `contacts-manage`+`contacts-view` | `CreateContactEndpoint.cs:16` |
| GET `/{id}` | getContact | `contacts-view` | `GetContactEndpoint.cs:14` |
| PUT `/{id}` | putCustomersContactsById | `contacts-manage`+`contacts-view` | `UpdateContactEndpoint.cs:15` |
| GET `/{id}/customers` | getCustomersContactsByIdCustomers | `associations-view`+`contacts-view` | `GetContactCustomersEndpoint.cs:15` |
| DELETE `/{id}` | deleteCustomersContactsById | `contacts-manage`+`associations-manage` | `DeleteContactEndpoint.cs:14` |

### 1.3 Lookup (`EP/LookupEndpoints.cs`, group `/lookup`)

| Method & path | operationId | Permission | Handler |
|---|---|---|---|
| GET `/brreg` | getCustomersLookupBrreg | `lookup-view` | `BrregLookupEndpoint.cs:20` |

### 1.4 Validation/refusal ordering per endpoint (not obvious from the contract)

Order matters and is inconsistent across endpoints — a direct porting hazard:

- **CreateCustomer** (`CreateCustomerEndpoint.cs:24-82`): (1) if `identity` present and caller lacks
  `legal-identity-manage` → **403 immediately**, before any field validation (`:38-42`); (2) identity/name/status
  field validation, **all collected together** → one 400 `ValidationProblem` (`:44-81`). No existence check (create).
- **UpdateCustomer** (`UpdateCustomerEndpoint.cs:23-129`): (1) identity-permission 403 check (`:34-38`, same as
  create); (2) name/status validation → 400 (`:40-61`); (3) **customer lookup → 404** (`:63-69`); (4) *only after*
  the 404 check, identity value re-validation → 400 (`:72-86`). So an invalid name against a missing customer id
  returns 400 (validation wins), but an invalid identity against a missing id returns 404 (existence wins) — the
  two validations are on opposite sides of the existence check within the same handler.
- **AttachCustomerContact** (`AttachCustomerContactEndpoint.cs:18-68`): (1) connection field validation → 400
  (`:31-34`); (2) `SELECT … FOR UPDATE` locks + customer/contact existence → 404 (`:39-48`); (3) already-attached
  check → 409 (`:50-59`). Validation before existence.
- **UpdateCustomerContact** (`UpdateCustomerContactEndpoint.cs:16-53`): (1) association lookup → 404 (`:24-32`);
  (2) field validation → 400 (`:38-41`). Existence *before* validation — the opposite order from Attach.
- **CreateContact / UpdateContact**: validate first (400), then (for update) look up (404) — consistent with each
  other, but note Attach/Update-customer-contact are inconsistent with each other as above.
- **Timeline List** (`TimelineEndpoints.cs:31-142`): (1) `limit` range → 400; (2) filter shape (`provenance`,
  `eventType[i]`, dates) → 400; (3) **customer existence → 404**; (4) cursor decode → 400; (5) cursor-matches-filters
  → 400. A malformed cursor against a nonexistent customer id returns 404, not 400 (existence checked first).
- **Timeline Update/Delete** (`:185-281`): (1) body/limit validation → 400 (Update only); (2) entry lookup → 404;
  (3) `Provenance != Manual || State != Active || revision mismatch` → **409** "Timeline entry is immutable" (wrong
  provenance/state) or "Timeline revision conflict" (stale revision) — **the same title text is reused for two
  different root causes** in Delete (`:249-259`) but Update's message differs slightly in wording only.
- **DeleteContact** (`DeleteContactEndpoint.cs:14-43`): `SELECT … FOR UPDATE` lock, then 404, then cascades
  timeline "removed" events for every association before deleting.

## 2. Domain rules

### 2.1 Entities / aggregates

- **Customer** (`DM/Customers/Customer.cs`): `Id` (identity, per-tenant sequence start 1001), `CustomerNumber`
  (business-facing, gapless per tenant via `tenant_counters`), `Name` (`FriendlyName`), `Identity?` (`LegalIdentity`,
  optional), `Status` (`CustomerStatus`), `CreatedAt`, `UpdatedAt`. No soft/hard delete flag beyond `Status`.
- **Contact** (`DM/Contacts/Contact.cs`): `Id`, `FirstName`/`LastName` (`PersonName`, required), `MiddleName`/
  `Prefix`/`Suffix` (optional name parts), `Phone?`/`Email?`, `CreatedAt`. Exists independently of any customer.
- **CustomerContact** (`DM/Contacts/CustomerContact.cs`): join entity keyed by `(CustomerId, ContactId)` — **a
  contact can be attached to a given customer at most once** (enforced by app-level existence check + PK, not a
  DB unique constraint beyond the PK itself, `DB/Configurations/CustomerContactEntityTypeConfiguration.cs:15`).
  Carries its own optional `Role`, `Phone`, `Email` (connection-specific, independent of the contact's own).
- **CustomerTimelineEntry** / **CustomerTimelineEntryRevision** (`DM/Timeline/*.cs`): current-snapshot +
  full-history tables (§3, §4). `Provenance` (`manual`|`generated`), `State` (`active`|`deleted`|`voided` — `voided`
  is defined but never set anywhere in this codebase, dead state), `ActorKind` (`unattributed`|`system` — no
  `user`/authenticated-actor kind exists despite the field name; every manual entry is `unattributed`, every
  generated one is `system`; **the API never attributes an entry to the calling user**).

### 2.2 Value objects and their exact validation (message, no codes — see §1)

All value objects are `readonly record struct` wrapping a string, validated in a private static `Validate`, with a
`TryCreate(value, out result, out error)` and a throwing constructor (`DomainException`). None expose a machine
code; the message string *is* the contract. All except `CountryCode`/`LegalType`/`CustomerStatus` trim; several
lowercase.

| Type | File | Max len | Rule | Message (exact) |
|---|---|---|---|---|
| `FriendlyName` | `DM/Customers/Common/FriendlyName.cs` | 255 | non-blank, ≤255, trims (case kept) | "A friendly name cannot be null or empty" / "A friendly name cannot be longer than {max} characters, the given value was {n} characters" |
| `CustomerStatus` | `.../CustomerStatus.cs` | — | one of `active`/`disabled`/`archived`, case-insensitive, trims+lowercases | "A customer status must be one of 'active', 'disabled' or 'archived', but was '{v}'" |
| `CountryCode` | `.../CountryCode.cs` | none | only non-blank required (**no ISO-3166 shape check at all** — any non-blank string is a valid country) | "A country code cannot be null or empty" |
| `LegalType` | `.../LegalType.cs` | none | only non-blank required (**`Person`/`Business` are just conventional constants, not enforced** — any non-blank string passes) | "A legal type cannot be null or empty" |
| `LegalId` | `.../LegalId.cs` | 50 | non-blank, ≤50, trims+lowercases (**not format-checked**, e.g. no Norwegian org-number checksum) | analogous to FriendlyName |
| `LegalName` | `.../LegalName.cs` | 255 | non-blank, ≤255, trims (case kept) | analogous |
| `LegalSource` | `.../LegalSource.cs` | — | one of `brreg`/`manual`, case-insensitive | "A legal source must be one of 'brreg', 'manual', but was '{v}'" |
| `PersonName` | `DM/Contacts/Common/PersonName.cs` | 100 | non-blank, ≤100, trims | "A name cannot be null or empty" |
| `NamePart` (prefix/suffix) | `.../NamePart.cs` | 20 | non-blank, ≤20, trims | "A name part cannot be …" |
| `PhoneNumber` | `.../PhoneNumber.cs` | 30 | non-blank, ≤30, only digits/space/`+-().`, **must contain ≥1 digit** | "A phone number can only contain digits, spaces and the characters + - ( ) ." |
| `EmailAddress` | `.../EmailAddress.cs` | 255 | non-blank, ≤255, one `@` with index>0, a `.` after the `@` not at the end, no space; trims+**lowercases** | "An email address must have the shape 'name@domain.tld'" (deliberately not RFC 5322) |
| `ContactRole` | `.../ContactRole.cs` | 255 | non-blank, ≤255, trims (free text, e.g. "CEO") | analogous |
| `LegalIdentity` | `DM/Customers/ValueObjects/LegalIdentity.cs` | — | composes the five above via `TryCreate`; **all field errors reported together**, keyed `country`/`type`/`id`/`name`/`source` | |

`LegalIdentity` is an EF *complex property* (owned, not a separate table) embedded directly on `customers` as
`legal_country`/`legal_id`/`legal_name`/`legal_source`/`legal_type`, all nullable together (§3).

### 2.3 Manual timeline entry validation (`TimelineEndpoints.TryParseManual`, `:366-430`)

- `eventType`: must be one of exactly `registry.change`, `interaction.call`, `interaction.meeting`,
  `interaction.email`, `note`, `other` (`ManualTypes`, `:21-29`) — **generated event types like `customer.created`
  can never be produced through this endpoint**, only through the internal recorder (§2.4).
- `occurredOn`: strict `yyyy-MM-dd`, and cannot be in the future (compared against `DateTime.UtcNow`'s date).
- `note`: required non-blank, ≤10000 chars (trimmed on success).
- `occurredAt` (optional instant): converted to UTC; cannot be in the future; if present, **must fall on the same
  UTC calendar date as `occurredOn`** — mismatch is a distinct field error on `occurredAt`.
- `sourceUrl` (optional): must parse as an absolute `http`/`https` URL, ≤2048 chars.
- All errors are collected and returned together (unlike Update/Attach's early-return style elsewhere).

### 2.4 Timeline state machine

- A **manual** entry starts `State=Active`, `CurrentRevision=1`. `PUT` increments `CurrentRevision` and appends a
  full-snapshot row to `Revisions`; `DELETE` sets `State=Deleted`, stamps `DeletedAt`, increments the revision, and
  appends a final "delete" revision (action label derived purely from `State==Deleted` / `RevisionNumber==1` /
  else "update", `TimelineRevisionResponse.FromDomain`, `:655-677` — the *response* infers the verb, it is not
  stored).
- Only entries with `Provenance==Manual && State==Active` are editable/deletable; any other combination (generated,
  already deleted, or the dead `voided` state) is rejected as **409 "Timeline entry is immutable"** regardless of
  the supplied `expectedRevision` (checked in the same boolean as the revision mismatch, `:204-213`, `:249-258`).
- A **generated** entry (`SV/CustomerTimelineRecorder.cs`) is always `Provenance=Generated`, `CurrentRevision=1`,
  `ActorKind=System`, `ActorDisplay="System"`, and is written with exactly one revision at creation time — it is
  never mutated afterward; the "immutable" 409 above is the only way that invariant is enforced (there's no DB
  trigger or check constraint backing it).
- Event types the recorder emits: `customer.created`, `customer.updated` (only when name or identity actually
  changed — see below), `customer.status_changed` (only when status changed), `customer.contact_attached`,
  `customer.contact_relationship_updated` (only when role/phone/email actually changed), `customer.contact_detached`,
  `customer.contact_removed` (used when a contact itself is deleted, cascading to every association,
  `Endpoints/Contacts/DeleteContactEndpoint.cs:33-36`).
- **Change detection before recording**: `UpdateCustomerEndpoint.cs:93` compares `customer.Name != name ||
  customer.Identity != customerIdentity` — a request that resubmits the same name/identity produces **no** timeline
  event and does **not** bump `UpdatedAt` (verified by `TS/TimelineEndpointsTests.cs:266-284`,
  "SemanticallyUnchangedCustomerAndRelationshipUpdatesDoNotCreateEvents"). Same pattern for
  `UpdateCustomerContactEndpoint.cs:43-49`.
- `customer.updated`'s payload has **`PayloadVersion=2`** (`SV/CustomerTimelineRecorder.cs:70`) while every other
  generated event type uses version 1 — no other event type ever changed shape, so this is the only versioned
  payload in the module.

## 3. Persistence

Schema `customers` (`DB/Customers/CustomersDbContext.cs:32`). All five tables are declared with a composite PK
`(tenant_id, …)` and have `EnableTenantRls` applied in the initial migration, later hardened by
`MakeTenantRlsPolicyNullSafe` (`Migrations/20260825105902_TenantRlsPolicyNullSafe.cs`) — **`tenant_id` on every
table, the RLS policies, and the composite PKs/FKs that include it are all dropped** for the single-tenant port;
what survives is the rest of each PK/unique key.

| Table (EF config file) | Columns of note | Keys / indexes / FKs | Concurrency |
|---|---|---|---|
| `customers` (`CustomerEntityTypeConfiguration.cs`) | `id` int identity (start 1001, incr 1); `customer_number` bigint; `name` varchar(255); `status` varchar(20) default `active`; `legal_country` varchar(2), `legal_id` varchar(50), `legal_name` varchar(255), `legal_source` varchar(50), `legal_type` varchar(50) — the 5 legal-identity columns are an EF **complex/owned type**, all nullable together, no DB check that they're all-or-nothing (app-level only, via `LegalIdentity?`); `created_at`, `updated_at` | PK `(tenant_id, id)`; unique `ux_customers_tenant_customer_number` on `(tenant_id, customer_number)` — **drop tenant_id, keep `customer_number` unique alone** | none (no version column) |
| `contacts` (`ContactEntityTypeConfiguration.cs`) | `id` int identity(1001,1); `first_name`/`last_name` varchar(100) required; `middle_name` varchar(100), `prefix`/`suffix` varchar(20), `phone` varchar(30) ascii, `email` varchar(255) ascii; `created_at` (added later, `20260818173114_AddContactCreatedAt.cs`) | PK `(tenant_id, id)`; no other index | none |
| `customers_contacts` (`CustomerContactEntityTypeConfiguration.cs`) | `customer_id`, `contact_id`; `role` varchar(255) required; `phone`/`email` varchar ascii nullable | PK `(tenant_id, customer_id, contact_id)`; FK→customers and FK→contacts, both `Cascade`; index `ix_customers_contacts_tenant_contact_id` on `(tenant_id, contact_id)` — **drop tenant_id from the index, keep `contact_id`** | none |
| `customers_timeline_entries` (`CustomerTimelineEntryEntityTypeConfiguration.cs`) | `id` int identity(1001,1); `customer_id`; `provenance` varchar(20); `producer` varchar(100); `event_type` varchar(100); `occurred_on` date; `occurred_at` timestamptz nullable; `summary` varchar(500); `note` varchar(10000); `source_url` varchar(2048); `payload_json` **jsonb** nullable; `payload_version` int; `current_revision` int — **`IsConcurrencyToken()`** (`:36-41`); `state` varchar(20); `actor_kind` varchar(30); `actor_display` varchar(255); `created_at`/`updated_at`/`deleted_at` | PK `(tenant_id, id)`; FK→customers `Cascade`; index `ix_customers_timeline_entries_tenant_customer_id` on `(tenant_id, customer_id)` — **drop tenant_id, keep `customer_id`** | `current_revision` is the EF concurrency token (§4) |
| `customers_timeline_entries_revisions` (`CustomerTimelineEntryRevisionEntityTypeConfiguration.cs`) | mirrors the entry table plus `customer_timeline_entry_id`, `revision_number` | PK `(tenant_id, id)`; FK→entries `Cascade`; **unique** `ux_customers_timeline_entries_revisions_entry_revision` on `(tenant_id, customer_timeline_entry_id, revision_number)` — **drop tenant_id, keep `(entry_id, revision_number)` unique** | the unique index is the second (belt-and-suspenders) concurrency guard, §4 |
| `tenant_counters` (shared tenancy package, `TN/TenantRlsMigrationExtensions.cs:107-124`, raw SQL not EF-mapped) | `tenant_id` uuid, `counter_name` text, `next_value` bigint | PK `(tenant_id, counter_name)` — **drop tenant_id, keep `counter_name` as the sole key** (a single row per counter, e.g. `"customer-number"`) | upsert-based, not a DB sequence (§4) |

No exclusion/GiST constraints, no partial indexes, no CHECK constraints beyond what `HasMaxLength`/`IsUnicode`
imply as `varchar(n)` column types (i.e. length is enforced by column type, not an explicit CHECK). Comments
(`HasComment(...)`) are attached to most columns as Postgres `COMMENT ON COLUMN` — informational only, not
consulted by the app.

**Stale code comment**: `CustomerTimelineEntryEntityTypeConfiguration.cs:58-61` says "The feed index is created as
a PostgreSQL expression index in the migration" describing an index tuned for the
`OrderByDescending(OccurredOn).ThenByDescending(OccurredAt.HasValue).ThenByDescending(OccurredAt).ThenByDescending(Id)`
sort used by timeline listing (`TimelineEndpoints.cs:119-125`). **No such expression index exists in any migration**
(grepped all four) — only the plain `ix_customers_timeline_entries_tenant_customer_id` on `(tenant_id, customer_id)`
exists. The comment is aspirational/stale; the listing query has no supporting sort index today. Worth deciding
explicitly for the Go port rather than copying the comment's claim.

## 4. Concurrency

- **Timeline entries — optimistic, double-checked**: the client supplies `expectedRevision`; the handler first does
  a manual comparison against the loaded `CurrentRevision` (`TimelineEndpoints.cs:204-213` update, `:249-259`
  delete) and returns 409 immediately on mismatch *before* touching the database. If two requests both pass that
  manual check (raced), the underlying UPDATE still carries `current_revision` as an EF `IsConcurrencyToken()`
  WHERE-clause value, so the loser gets a `DbUpdateConcurrencyException` → also 409 (`:223-230`). Independently, the
  new revision row insert is protected by the **unique index** `ux_customers_timeline_entries_revisions_entry_revision`;
  a collision there raises Postgres `23505` on that exact constraint name, caught by `IsRevisionConflict`
  (`:432-435`) and also turned into the same 409. All three paths return the same problem shape
  (`title:"Timeline revision conflict"`), so callers cannot distinguish which layer caught the race — only that one did.
- **No Serializable transactions, no advisory locks** anywhere in this module (unlike identity's bootstrap/owner
  mutations). The module instead uses:
  - **Pessimistic row locks** via raw SQL `SELECT * FROM customers.contacts WHERE id = {id} FOR UPDATE`, used in
    `DeleteContactEndpoint.cs:22` and `AttachCustomerContactEndpoint.cs:42`, each wrapped in an explicit
    `BeginTransactionAsync`. Purpose (per the code comments): serialize a contact delete against a concurrent
    attach on the same contact, so a delete either observes the new association and emits a "removed" event for
    it, or completes first and makes the attach a clean 404 rather than an orphaned FK write.
  - `CreateCustomerEndpoint.cs:94-103` wraps the tenant-counter allocation + customer insert + `customer.created`
    timeline insert in one explicit transaction, so all three commit or none do.
  - No explicit isolation level is set anywhere; connections run at Postgres's default READ COMMITTED.
- **Customer number allocation** (`TN/TenantCounterService.cs`): a single upsert
  `INSERT … ON CONFLICT (tenant_id, counter_name) DO UPDATE SET next_value = next_value + 1 RETURNING next_value`
  against `tenant_counters`, executed on the ambient EF connection/transaction. This is what makes `CustomerNumber`
  gapless per tenant (verified `TS/TenantIsolationIntegrationTests.cs:66-78`: two tenants each start at 1
  independently). **Single-tenant port**: becomes one row keyed by `counter_name` alone (e.g. `"customer-number"`);
  the upsert pattern itself (not a bare Postgres `SEQUENCE`) should probably be kept since it's what the tests
  pin down as "gapless, gap-free even under a rolled-back caller" semantics.
- **Customer row itself has no concurrency token** — two concurrent `PUT /customers/{id}` last-writer-wins with no
  conflict detection (unlike timeline entries). This asymmetry is intentional-looking but undocumented; flag it as
  a design choice to preserve or fix in the port.
- **Archival is idempotent by construction**, not by concurrency control: `DeleteCustomerEndpoint.cs:31-38` only
  writes (and only emits a timeline event) `if (customer.Status != Archived)`; calling it twice concurrently could
  both read `Active` and both write `Archived` + both emit a `customer.status_changed` event (a race, not defended
  against — worth noting, not necessarily worth fixing).

## 5. External integration — Brreg lookup

- **Upstream**: Brønnøysundregisteret's open Enhetsregisteret API, base URL `https://data.brreg.no`
  (`CFG/BrregLookupOptions.cs:11`, overridable via config section `Brreg:BaseUrl`).
  - Search: `GET /enhetsregisteret/api/enheter?navn={urlencoded search}&size=10`.
  - Exact: `GET /enhetsregisteret/api/enheter?organisasjonsnummer={urlencoded legalId}&size=10`.
  - Response subset consumed: `_embedded.enheter[].{organisasjonsnummer, navn}` (`BrregLookupEndpoint.cs:109-117`);
    everything else in the real payload is ignored. A response with no `_embedded` key (e.g. zero matches) is
    treated as an empty list, not an error (`:43`, `result.Embedded?.Entities ?? []`).
- **Request validation** (`Validate`, `:69-85`): exactly one of `legalId` (any non-blank string, not format-checked)
  or `search` (≥2 chars after trim) must be given; otherwise 400 `ProblemDetails` "Invalid lookup query" (flat
  `title`/`detail`, no field-keyed errors — unlike most other 400s in this module).
- **HTTP client config** (`DB/CustomersDatabaseConfiguration.cs:41-58`): named client `"brreg"`;
  `client.Timeout = 20s`; wrapped in `.AddStandardResilienceHandler` (Polly, via `Microsoft.Extensions.Http.Resilience`)
  with: retry up to 3 attempts (exponential backoff + jitter is the library default, not overridden), per-attempt
  timeout 4s, total-request timeout 15s, circuit breaker sampling window 30s (failure-ratio/break-duration left at
  library defaults). Retries apply to 5xx/408/timeouts on GET, which is safe since lookups are idempotent.
- **Caching**: **none** — every request hits the upstream (through the resilience pipeline) or its retries; there
  is no response cache, in-memory or otherwise.
- **Error mapping**: `HttpRequestException` or `TaskCanceledException` (and only those, and only when the request
  wasn't locally cancelled) after the resilience pipeline exhausts retries → logged as a warning
  (`BrregLookupEndpoint.cs:58-60`) and returned as **502** `ProblemDetails` "Lookup service unavailable" (`:62-65`).
  Any other exception type is *not* caught here and would propagate to the host's generic exception handler.
- **Tests fake it** via `TS/StubBrregHandler.cs`, a custom `HttpMessageHandler` installed in place of the real
  `HttpClient` transport in `CustomersApiFactory`. Default canned responses: name search returns two fixed Equinor
  entities; org-number lookup echoes back `{organisasjonsnummer: <input>, navn: "STUB ENTITY AS"}`. Tests override
  `OnRequest` per-case to simulate: retryable 503s counted via `Interlocked.Increment` to prove the resilience
  pipeline retried exactly the expected number of times (`TS/LookupEndpointsTests.cs:24-39`), an empty
  `_embedded`-less body, and a thrown `HttpRequestException` to hit the 502 path. `Dispose()` resets `OnRequest` to
  null between tests in the same collection (`:18-22`) since the handler instance is shared per test-collection factory.

## 6. Cross-cutting

- **Rate limits**: none inside this module. (The host-level per-route rate limiting, if any, is out of scope here —
  no `RequireRateLimiting` call appears anywhere in the Customers endpoints.)
- **Audit**: none. There is no audit log comparable to identity's `authorization_audit_events`; the customer
  timeline (§2.4/§3) is the closest analogue but it is a **business-facing feature** (visible to `timeline-view`
  holders, not an admin-only audit trail) and only covers customer/contact mutations, not permission or
  authentication events.
- **Events published for other modules**: none via a message bus/outbox — this module publishes no domain events
  externally. What it *does* publish is a **cross-module C# contract**, `ICustomerDirectory`
  (`CT/ICustomerDirectory.cs`), registered as the `CustomerDirectory` implementation in
  `SV/ApplicationServiceCollectionExtensions.cs:16,20-107`, and consumed directly (in-process) by
  `apps/communications/backend/Communications.Module/Services/ConversationContactLinker.cs` and
  `apps/energy/backend/Energy.Module/Endpoints/SupplyPeriods/{CreateSupplyPeriodEndpoint,SwitchSupplyPeriodEndpoint}.cs`.
  This is the one interface the Go port must keep semantically equivalent for cross-module callers even though
  there's no HTTP contract for it:
  - `FindCustomerAsync(id) → CustomerDirectoryEntry?{Id, Name, Archived}` — **archived customers still resolve**
    (Communications/Energy keep historical customer references), flagged via `Archived: bool`.
  - `FindContactAsync(id) → ContactDirectoryEntry?{Id, FirstName, LastName, Email}`.
  - `FindByEmailAsync(normalizedEmail) → ContactEmailResolution?` — matches a contact's **canonical** email or any
    **customer-specific** (association-level) email, case-insensitive after trim; returns one `ContactEmailMatch`
    per matching contact id with the list of candidate customer ids; `IsAmbiguous` when >1 contact matches. Default
    interface method throws `NotSupportedException` for implementations (test doubles) that don't support it — the
    real Customers implementation always supports it. A customer-specific email match does **not** leak the email
    string itself into the result (verified `TS/CustomerDirectoryEmailResolutionTests.cs:53-54`).
- **Permission catalog contributor** (`AZ/CustomerPermissionCatalogContributor.cs:9-49`), module key prefix
  `customers`. `PermissionDescriptor` shape is `(Key, DisplayName, Description, Module, Category, Sensitive=false,
  Delegable=true)` (`CT/Authorization/PermissionDescriptor.cs:8-15`); the contributor never passes `Delegable`
  explicitly, so **every Customers permission is `Delegable: true`** (only `Sensitive` is ever overridden):

| Key | Display | Description | Category | Sensitive |
|---|---|---|---|---|
| `customers:view` | View customers | View customer names, identifiers, and a sanitized activity summary. | Customers | false |
| `customers:create` | Create customers | Create customers without legal identity data. | Customers | false |
| `customers:update` | Update customers | Update customer names and basic non-sensitive details. | Customers | false |
| `customers:delete` | Delete customers | Delete customers and their customer-owned records. | Customers | **true** |
| `customers:legal-identity-view` | View legal identities | View customer legal identity and registry attribution. | Legal identity | **true** |
| `customers:legal-identity-manage` | Manage legal identities | Add, replace, or remove customer legal identity data. | Legal identity | **true** |
| `customers:contacts-view` | View contacts | View contact names and contact details. | Contacts | **true** |
| `customers:contacts-manage` | Manage contacts | Create, update, and delete contacts. | Contacts | **true** |
| `customers:associations-view` | View customer associations | View links between customers and contacts. | Associations | **true** |
| `customers:associations-manage` | Manage customer associations | Create, update, and remove customer-contact links. | Associations | **true** |
| `customers:timeline-view` | View customer timeline | View customer timeline entries, notes, provenance, and revisions. | Timeline | **true** |
| `customers:timeline-manage` | Manage customer timeline | Create, update, and delete customer timeline entries. | Timeline | **true** |
| `customers:lookup-view` | Use registry lookup | Search the external business registry for legal identities. | Lookup | **true** |

  13 permissions total; only `view`/`create`/`update` are non-sensitive — everything touching legal identity,
  contacts, associations, timeline, or the external lookup is `Sensitive`. Runtime enforcement (catalog lookup,
  `Owner` short-circuit, disabled/locked check, direct + group-derived roles) is identity's
  `permission:<key>` policy machinery (see the identity inventory §1) — this module only *declares* the keys and
  calls `.RequirePermission(key)` per route.
- **`Business` view exposed twice**: the "safe" customer/contact DTOs (`SafeCustomerResponse`, `ContactResponse`,
  `CustomerContactResponse`) are the only shapes ever returned — there's no separate "full" vs "safe" endpoint;
  visibility of the legal-identity sub-object is gated per-response by re-checking `legal-identity-view` at
  serialization time (`GetCustomerEndpoint.cs:51-52`, `GetCustomersEndpoint.cs:37-38`, `UpdateCustomerEndpoint.cs:119-120`,
  `GetCustomerStatsEndpoint.cs:40-41`), not by a different route or a different permission model.

## 7. Tests

`Customers.Module.Tests` mixes true Customers-domain tests with generic host/identity infrastructure tests that
happen to live in this test project (they boot the same `WebApplicationFactory<Program>` and exercise
auth/OIDC/MFA/SPA/security-header/CSRF machinery that belongs to the identity/host inventory, not this one). Those
are **out of scope for the Customers port entirely** (not "tenancy-only" — they simply test unrelated modules) and
are marked "infra, drop" below rather than "tenancy-only".

| Test class | Count | Description | Disposition |
|---|---|---|---|
| `Domain/Contacts/Common/ContactValueObjectTests.cs` | 12 | `PersonName`/`PhoneNumber`/`EmailAddress`/`NamePart`/`ContactRole` value-object validation | **port** |
| `Domain/Customers/Common/CountryCodeTests.cs` | 5 | `CountryCode` validation/normalization | **port** |
| `Domain/Customers/Common/FriendlyNameTests.cs` | 6 | `FriendlyName` validation/normalization | **port** |
| `Domain/Customers/Common/LegalIdTests.cs` | 7 | `LegalId` validation/normalization | **port** |
| `Domain/Customers/Common/LegalNameTests.cs` | 7 | `LegalName` validation/normalization | **port** |
| `Domain/Customers/Common/LegalTypeTests.cs` | 5 | `LegalType` validation/normalization | **port** |
| `Domain/Customers/ValueObjects/LegalIdentityTests.cs` | 6 | `LegalIdentity` composite validation, multi-field errors | **port** |
| `Integration/CustomersEndpointsTests.cs` | 24 | Create/Update/Get/List customer: validation, projection parity, pagination, sort, search, versioning 404 | **port** |
| `Integration/ContactsEndpointsTests.cs` | 24 | Contact CRUD, search (name parts/phone/email), attach/detach/update associations, cascade delete | **port** |
| `Integration/TimelineEndpointsTests.cs` | 10 | Manual entry lifecycle (create/edit/soft-delete/revisions), keyset cursor, filters, generated events, immutability, no-op suppresses events | **port** |
| `Integration/CustomerArchivalTests.cs` | 4 | Archive-not-delete semantics, idempotency, listing filter, directory sees archived | **port** |
| `Integration/CustomerStatsAndStatusTests.cs` | 5 | `/stats` key figures incl. permission-gated identity figures; status default/transition/validation | **port** |
| `Integration/LookupEndpointsTests.cs` | 6 | Brreg search/exact lookup, empty result, invalid query, upstream failure → 502, retry-then-succeed | **port** |
| `Integration/CustomerDirectoryEmailResolutionTests.cs` | 5 | `ICustomerDirectory.FindByEmailAsync` canonical/association-email matching, ambiguity, no-leak, cancellation | **port** (as the directory-contract behavior; the specific interface shape is a Go-side design decision) |
| `Integration/CustomersPermissionIntegrationTests.cs` | 10 | Per-permission-key authorization matrix incl. safe-projection leakage checks, disabled-user 401 | **port** (permission *keys and gating*, not the identity policy plumbing itself) |
| `Integration/TenantIsolationIntegrationTests.cs` | 3 | Row stamping, cross-tenant query isolation, per-tenant counter independence | **tenancy-only, drop** |
| `Integration/LeastPrivilegeDatabaseRoleIntegrationTests.cs` | 8 | DB role privilege, RLS enforcement paths (superuser bypass, no-tenant-setting, session-scoped setting, pooled-connection bleed, unresolved-tenant fail-closed) | **tenancy-only, drop** |
| `Integration/DatabaseContextRegistrationTests.cs` | 1 | Two DbContexts share one `NpgsqlDataSource` | **infra, drop** (EF/ADO.NET wiring, no Go equivalent) |
| `Integration/DevelopmentSeedIntegrationTests.cs` | 3 | Dev-environment seed data (admin bootstrap + customer/contact/association fixtures), idempotency, convergence when re-seeded with a larger count | **mostly infra/host, drop** — but the *fixture data shapes* (legal ids `990000001+n`, contact emails, role strings) are a useful reference if the Go port wants an equivalent dev-seed |
| `Integration/AuthEndpointsTests.cs` | 25 | Identity login/2FA/session flows | **infra, drop** (identity module) |
| `Integration/AuthSecurityUnitTests.cs` | 2 | Invitation token opacity, password-policy by environment | **infra, drop** (identity module) |
| `Integration/Phase3MfaIntegrationTests.cs` | 4 | MFA enforcement | **infra, drop** (identity module) |
| `Integration/WorkforceOidcTests.cs` | 7 | OIDC option loading/validation | **infra, drop** (identity module) |
| `Integration/WorkforceOidcCompletionTests.cs` | 6 | OIDC callback completion | **infra, drop** (identity module) |
| `Integration/PublicOriginIntegrationTests.cs` | 3 | Public origin/base-path resolution | **infra, drop** (host) |
| `Integration/SecurityHeadersIntegrationTests.cs` | 5 | Browser security headers on every response | **infra, drop** (host) |
| `Integration/SpaFallbackIntegrationTests.cs` | 8 | SPA `index.html` templating/fallback | **infra, drop** (host) |

**The metric is .NET test methods: a `[Theory]` counts as one method regardless of its `InlineData` count.**
Every `Count` above is a method count on that basis. Stated explicitly because the three module inventories
previously mixed metrics (energy counted theory *cases*), which made their totals unaddable.

**Portable total: 136 methods** (corrected 2026-09-12 in Task 16, from 100). Domain, 7 classes = **48**:
12 + 5 + 6 + 7 + 7 + 5 + 6. Integration, 8 classes = **88**: 24 + 24 + 10 + 4 + 5 + 6 + 5 + 10. 48 + 88 = 136.

The previous "100" came from an addend list — `12 + 24 + 24 + 10 + 4 + 5 + 6 + 5 + 10` — that carried only
**one** of the seven "port" domain classes (`ContactValueObjectTests`' 12) and silently omitted the other six
(`CountryCode` 5, `FriendlyName` 6, `LegalId` 7, `LegalName` 7, `LegalType` 5, `LegalIdentity` 6 = **36**), even
though the same sentence correctly said the domain classes total 48. 100 + 36 = 136.

Cross-checked against the whole project: 211 methods exist in `Customers.Module.Tests`; **75 are dropped across
12 classes** (2 tenancy-only classes, 10 unrelated host/identity/infra classes); 211 − 75 = 136. The earlier
"11 classes, ~69 tests" understated both.

## 8. Oddities

1. **No machine-readable error codes anywhere in this module.** Every non-2xx response is either a bare-body 404,
   an RFC7807 `ValidationProblem` (field errors, no top-level code), or a `ProblemDetails` with only `title`/`detail`
   text (§1). This is a sharp contrast with identity's `{code, message}` bodies described in the identity inventory
   — a Go port that tries to reuse identity's error envelope for Customers will invent codes that never existed.
2. **`GetCustomers`'s doc comment lies about search scope.** `GetCustomersEndpoint.Request.Search`'s XML doc says
   "Case-insensitive free-text search matching the customer name, legal name and legal id"
   (`GetCustomersEndpoint.cs:185`), but the implementation only `ILIKE`s `customer.Name` (`:51-56`) — legal name and
   legal id are never searched. Confirmed by `TS/CustomersEndpointsTests.cs:472-495`
   ("`GetCustomers_Search_MatchesLegalNameAndLegalIdCaseInsensitively`"), whose own assertions are `Assert.Empty(...)`
   for both cases — the test name is also stale/misleading; it documents the *absence* of the claimed behavior.
3. **Stale "expression index" comment** on the timeline entry configuration (§3) claims a supporting index exists
   for the feed sort order; it doesn't, in any of the four migrations.
4. **Validation-vs-existence ordering is inconsistent across sibling endpoints** (§1.4): Attach validates fields
   before checking customer/contact existence; Update-association checks existence before validating fields;
   Update-customer checks name/status before existence but identity after existence. A faithful port has to encode
   each ordering explicitly per endpoint rather than adopt one canonical order.
5. **`GetCustomersStatsAttention` is a stub.** The handler is wired up, permissioned, and documented in the OpenAPI
   contract, but always returns an empty array (`CustomerStatsEndpoints.cs:111-112`) — there is no dashboard
   "attention items" feature behind it yet. Do not infer any behavior from the response schema beyond "empty array".
6. **`TimelineRevisionListResponse` is dead code.** It's declared (`TimelineEndpoints.cs:587-590`) but the
   `Revisions` handler actually returns an anonymous `new { data = revisions }` (`:303`) instead of constructing it
   — functionally identical once serialized, but a maintenance trap (the type looks load-bearing and isn't).
7. **`voided` timeline state is unreachable.** `TimelineState.Voided` is declared (`DM/Timeline/CustomerTimelineEntry.cs:51`)
   but nothing in the module ever sets it — only `Active`/`Deleted` are used. Decide whether the Go port needs this
   third state at all or should drop it as dead.
8. **`ActorKind` never reflects the calling user.** Despite looking like an attribution field, manual entries are
   always `Unattributed` and generated ones always `System` (§2.4) — there is no code path that stamps the
   authenticated principal's identity onto a timeline entry, even though the endpoint has `ClaimsPrincipal` in scope
   for other purposes (permission checks). If the Go port's product intent is "who did this", this is a gap to
   close, not preserve, but preserving current behavior means still not attributing to the caller.
9. **Country/legal-type value objects are unvalidated beyond non-blank.** `CountryCode` does not check ISO-3166
   shape and `LegalType` does not restrict to `person`/`business` even though those are the only two constants
   defined (`DM/Customers/Common/LegalType.cs:9-10`) — any non-blank string is accepted for both, e.g. `country:
   "Narnia"` or `type: "spaceship"` would both persist successfully. `LegalId` similarly performs no
   registry-specific checksum/format validation regardless of `source`.
10. **`AttachCustomerContact`'s row lock targets only the `contacts` row**, not the `customers` row or the
    association table, even though the race being defended against (§4) is specifically attach-vs-contact-delete.
    A concurrent attach vs. customer-archive is not defended by the same mechanism (and doesn't need to be, since
    archiving doesn't delete the customer row) — but a porter unfamiliar with the comment's reasoning might assume
    symmetrical locking is needed on both sides.
11. **JSON property casing is a framework default, not configured by this module.** All response DTOs use PascalCase
    C# properties (`Id`, `CustomerNumber`, …) and rely on ASP.NET Core Minimal API's `JsonSerializerDefaults.Web`
    (camelCase) applied at the host level — there's no `[JsonPropertyName]` on most properties (a few explicitly
    pin `eventType`, `:537,595,638`, presumably because it clashes with a reserved word or generic constraint
    elsewhere). The Go port has no such implicit default and must camelCase every field explicitly to match the
    contract's `camelCase` schema property names.
12. **Contract access strings use `+` for "requires both permissions" but never `,`/`|` for "either"** — every
    multi-permission op in `customers.yaml` is a hard AND, matching two `.RequirePermission()` calls chained on one
    route (ASP.NET's convention extension ANDs multiple requirements). There is no OR-permission concept anywhere
    in this module.
