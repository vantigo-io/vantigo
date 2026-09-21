# Customers module

The Customers module is where every other module finds out who the work is for: a
customer's name, number, legal identity, contacts and activity history, served under
`/api/v1/customers/*` (`apps/server/internal/customers`, schema `customers`). It is the
hub Energy's supply periods, Projects' projects and Communications' conversations all
point back to, through `contracts.CustomerDirectory` — never by reading the `customers`
schema directly.

It is **not a sales CRM**. There is no pipeline, no deal stage, no lead scoring, no
email/calendar sync and no marketing automation, and none of that is planned inside
this module (see [What comes next](#what-comes-next) and
[ROADMAP.md](../ROADMAP.md#customers)). What it does hold — a timeline with generated
and manual entries, contacts with per-relationship roles, archival instead of delete —
is closer to what a light ERP customer card needs than to what a CRM offers, and it
stops well short of a CRM on purpose.

Customers depends on nobody but identity (`contracts.UserDirectory`, to name who wrote
a timeline entry) and its own Brønnøysundregisteret (Brreg) lookup. No other module is
a dependency of it.

## Domain model

- **Customer** — `id` (generated, never reused), `customerNumber` (assigned from an
  internal counter on create, unique, never changes), `name`, `status`
  (`active`/`disabled`/`archived`), `type` (`business`/`person`), an optional
  **legal identity**, and `revision` — the optimistic-concurrency token, see
  [Revision and concurrency](#revision-and-concurrency). `type` defaults to `business`
  on create; changing it later is its own operation
  (`PUT /customers/{id}/type`), not part of the general update, because it rarely
  makes sense for a customer with history and the UI confirms it separately.
- **Legal identity** — country, type, id, name and source, all five set together or
  none at all (never a partial identity). See [Legal identity and its
  validation](#legal-identity-and-its-validation).
- **Contact** — a person: first/last name, optional middle name, prefix, suffix,
  phone and email. A contact is not owned by any one customer.
- **Customer–contact association** — the many-to-many link, carrying `role` (free
  text, e.g. "Billing", "Decision maker") and a phone/email that **override** the
  contact's own for this relationship only. There is no primary-contact flag yet —
  ROADMAP phase 4.
- **Timeline entry** — one row per customer event, generated or manual, each with its
  own revision history. See [The timeline](#the-timeline).

`SafeCustomerResponse` — what `GET`/`POST`/`PUT` on a customer return — always carries
a `timelineSummary` (entry count, most recent `occurredOn`) even to a caller with no
`customers:timeline-view`: it is the "sanitized activity summary"
`customers:view`'s own description promises, not a timeline read. The full
`identity` block is only populated when the caller holds `customers:legal-identity-view`;
otherwise it is simply absent from the response, not empty.

## Statuses

`active`, `disabled` and `archived`, case-insensitively accepted and stored lower-case.

- **`disabled` is a label only.** It is accepted, stored, shown and — since this
  branch — filterable, but **nothing in this module or any other currently behaves
  differently because a customer is disabled.** This is deliberate (design decision
  D3): Projects already accepts a project against an archived customer on the
  reasoning that "a project can outlive the relationship that started it", so this
  branch does not invent a blocking rule for `disabled` that would contradict that
  standing decision on a status nobody can invoice against yet. Invoices, when it
  exists, is expected to give `disabled` its meaning — Business Central's "blocked
  for invoicing" is the model in mind.
- **Customers are archived, never deleted.** `DELETE /customers/{id}` sets
  `status: "archived"`; it is idempotent (archiving an already-archived customer
  writes nothing and emits no second event) and requires `customers:delete`.
- **Restoring** an archived customer is `PUT /customers/{id}` with `status: "active"`
  (or any other status) — there is no dedicated "restore" endpoint. It needs
  `customers:update`, the same permission any other edit does.
- Archived customers are still returned by every lookup that finds a customer by id —
  `GetCustomer`, the `CustomerDirectory`, the contact-association endpoints — so a
  reference held elsewhere (an energy supply period, a project) never breaks. The
  list endpoint hides them unless asked (see below).

## Customer type vs. legal identity type

A customer's `type` (`business`/`person`) and its legal identity's own `type` field
are two different things that are cross-checked against each other: a Brreg business
identity on a private-person customer, or a person identity on a business customer,
is never a consistent record, so every write that would create that mismatch is
refused with a 400 keyed `identity.type` (or bare `type` on the dedicated
legal-identity endpoint). Changing the customer type when it already carries an
identity of the old type removes the identity in the same request, recorded as an
ordinary `customer.updated` event alongside `customer.type_changed`.

## Legal identity and its validation

Every field of a legal identity — `country`, `type`, `id`, `name`, `source` — is
independently validated and every failure reported together (never short-circuited on
the first one), keyed `country`/`type`/`id`/`name`/`source` (or `identity.<field>`
when the identity is nested inside a create/update body rather than the dedicated
`.../legal-identity` endpoint).

| Field | Rule |
| --- | --- |
| `country` | Non-blank; must be an assigned **ISO 3166-1 alpha-2** code (249 codes, embedded in Go, no external dependency), case-insensitive, stored lower-case. User-assigned codes (`xk`, `aa`, …) and the unofficial `uk` alias for `gb` are deliberately not accepted. |
| `type` | Non-blank. Only `person`/`business` are conventional; any other non-blank value is accepted here (an inventory oddity carried over from the original implementation) — it is `identityTypeMismatch`, not this field's own validator, that enforces agreement with the customer's `type`. |
| `id` | Non-blank, at most 50 UTF-16 code units, trimmed and lower-cased — **except** when `country` is `no` and `type` is `business`, in which case it must be a Norwegian **organisasjonsnummer**: whitespace stripped, then exactly nine digits whose last is the mod-11 check digit (weights 3 2 7 6 5 4 3 2; a remainder that would produce check digit 10 is invalid outright). Stored as the nine digits. |
| `name` | Non-blank, at most 255 UTF-16 code units, trimmed, case preserved. |
| `source` | `brreg` or `manual`. |

**Norwegian person identities are deliberately not validated as a fødselsnummer.**
Every other country/type combination — including `no` + `person` — keeps the plain
non-blank/length rule above. Whether a Norwegian person's legal id should even be
stored at all, and under what GDPR handling, is [ROADMAP phase 6](../ROADMAP.md#customers)'s
call to make; this branch does not make that field look sanctioned by validating it.

**Validation applies to writes only.** A row already stored is never re-validated —
older data that predates a rule can sit there unchanged — and a `PUT` that omits
`identity` leaves whatever the customer already had untouched (the endpoint's own doc
comment says omitting it "removes" the identity; no test exercises that claim, and the
code — the port's ground truth — does not do it: use `DELETE .../legal-identity`
instead).

## Contacts and associations

A contact is a person record independent of any customer; the many-to-many
association is what links one to a customer, carrying `role` and an optional
phone/email that overrides the contact's own for that relationship. Deleting a
contact removes every association it has, each recorded as a `customer.contact_removed`
event against the customer it was attached to, all inside one transaction with a
row lock on the contact.

Ten operations in total: list/create/get/update/delete a contact, list a contact's
customers, list a customer's contacts, attach/update/detach an association. See the
[API](#api) table for exactly which permission gates which.

## The timeline

Every customer has a timeline: **generated** entries the module itself writes when
something happens, and **manual** entries a user writes by hand.

Generated event types: `customer.created`, `customer.updated`, `customer.type_changed`,
`customer.status_changed`, `customer.contact_attached`,
`customer.contact_relationship_updated`, `customer.contact_detached`,
`customer.contact_removed`. These are immutable — there is no edit or delete endpoint
for a generated entry.

Manual event types: `note`, `interaction.call`, `interaction.meeting`,
`interaction.email`, `registry.change`, `other`. A manual entry carries an
`occurredOn` (an ISO date, never in the future), an optional `occurredAt` (must share
`occurredOn`'s UTC calendar date), a note (≤ 10,000 characters) and an optional
`sourceUrl`. It can be edited and soft-deleted, and every edit or delete appends a new
**revision** — a full point-in-time snapshot — guarded by three overlapping
concurrency checks (a manual comparison before the write, the guarded `UPDATE`'s own
`WHERE`, and a unique index on entry+revision number as backstop), all answering the
same "Timeline revision conflict" problem. `registry.change` exists as a manual type
today; nothing produces it automatically yet — that is
[ROADMAP phase 3](../ROADMAP.md#customers)'s job.

### Authorship

Every timeline write made on behalf of a signed-in user — a manual entry's create,
update or delete, and the generated events a user's action causes — is stamped
`actorKind: "user"`, the user's id, and `actorDisplay` snapshotted from
`contracts.UserDirectory` at the moment of writing (`"Unknown user"` if the directory
no longer knows the id; renaming the person later never rewrites history, the same as
every other timeline column).

- The **entry** row keeps its *original* author, unchanged by later edits.
- Each **revision** row carries the actor of *that* revision — so if someone else
  edits or deletes an entry, the history shows their name on that step, not the
  original author's.
- A write made with no user principal (unreachable through any HTTP path today — every
  route sits behind authentication, and SCIM never reaches these operations) falls
  back to `unattributed`/`Unattributed` for a manual write or `system`/`System` for a
  generated one.
- Rows written before this branch (migration `00015_customers_timeline_actor.sql`)
  have no `actor_user_id` at all — nothing can be said about who wrote them, and they
  are left as they are, not backfilled.

## The list endpoint and search

`GET /api/v1/customers` supports paging (`page`, `pageSize` 1–100, default 25),
`status`, `type`, `includeArchived`, `search`, and `sortBy` (`id`, `name`,
`customerNumber`, `createdAt`, `updatedAt`, always tie-broken by `id`) with
`sortDirection` (`asc`/`desc`).

- Naming a `status` shows exactly that status — `status=archived` shows archived
  customers with no need for `includeArchived`. Leaving `status` off keeps the old
  default: archived hidden unless `includeArchived=true`.
- **`search` never reaches data the caller could not otherwise see.** It always
  matches the name and the customer number, case-insensitively, as a substring. It
  additionally matches the legal name and legal id **only when the caller holds
  `customers:legal-identity-view`**, and a linked contact's first/last name and
  email (canonical or association-specific) **only when the caller holds both**
  `customers:contacts-view` and `customers:associations-view`. A caller with neither
  gets exactly the name-and-number behaviour — search is deliberately not an oracle
  for data the response would otherwise withhold. The same permission that widens
  search also decides whether the response's `identity` block is populated, so one
  `hasPermission` call answers both questions for the identity branch.
- The customer number and legal id are additionally matched with all whitespace
  stripped from both the value and the search term, so `923 609 016` finds a legal id
  stored as `923609016`.

## Revision and concurrency

`customers.customers` carries `revision integer NOT NULL DEFAULT 1`. **Every write to
the row** — a general update, a type change, a legal-identity put or delete, an
archive — increments it by one. A request that turns out to write nothing is not a
write: like `PUT /customers/{id}`, a legal-identity `PUT` that resubmits the identity
already stored (equal in all five fields) issues no `UPDATE` at all, so neither
`revision` nor `updated_at` moves and no event is recorded.

- `SafeCustomerResponse` carries `revision` (optional in the schema, for corpus
  compatibility — see below).
- `PUT /customers/{id}` and `PUT /customers/{id}/type` accept an optional `revision`
  in the request body. When present and it disagrees with the row's current value,
  the answer is **409 "Customer revision conflict"** and nothing is written; the same
  comparison is repeated in the guarded `UPDATE`'s `WHERE` clause, so two writers who
  both read the same revision cannot both win the race even if the two 409 checks
  themselves race.
- **`revision` is optional, not required**, because the recorded exchange corpus
  (`openapi/testdata/exchanges/customers.jsonl`) is frozen — every contract change in
  this branch had to be additive. A request that omits it writes through
  unconditionally, exactly as before this branch existed; the frontend always sends
  it.
- The legal-identity sub-resource (`PUT`/`DELETE .../legal-identity`) and archive
  (`DELETE /customers/{id}`) stay **unconditional writes**, never revision-guarded:
  each replaces or clears one self-contained thing, and archive is idempotent by
  construction rather than by concurrency control. Unconditional means they never
  ask the caller for a revision — not that they write when there is nothing to
  write: each has its own no-op rule (an identical identity, an identity already
  absent, a customer already archived) under which nothing happens at all.
- Check order on `PUT /customers/{id}`: legal-identity-manage gate (if the body
  carries an identity) → name/status validation → the 404 lookup → the revision
  check → identity re-validation → the duplicate-identity check (last, immediately
  before the write). A stale revision wins over an invalid identity; validation wins
  over a missing customer; the permission gate wins over both.
- This follows the `revision` naming Time and Projects already use for their own
  optimistic-concurrency tokens, not the timeline's own `expectedRevision`.

## The duplicate-identity guard

Wherever a legal identity is written — `POST /customers`, `PUT /customers/{id}`,
`PUT /customers/{id}/legal-identity` — if **another** customer, of any status
(archived included, since the usual next move is to restore it, not create a
duplicate) already carries the same `(country, id)`, the write is refused with:

```
409 Conflict
{
  "title": "Duplicate legal identity",
  "code": "duplicate_legal_identity",
  "detail": "Another customer already has this legal identity.",
  "duplicates": [{ "id": …, "customerNumber": …, "name": …, "status": … }]
}
```

- The request may carry `allowDuplicateIdentity: true` to go ahead anyway — two
  departments of one company kept as separate customers is a legitimate case — which
  is exactly **why there is no unique index** on `(legal_country, legal_id)`. There is
  a plain btree one (`ix_customers_legal_identity`, migration 00016) so the check
  itself is an index lookup rather than a sequential scan.
- An identity that is **unchanged** by the request is never checked: a bare
  name/source/type edit, or resubmitting the same `(country, id)`, is never a
  conflict with itself. On create there is no such exemption — every create is a
  fresh identity by definition.
- **`duplicates` is only present when the caller holds `customers:view`.** None of the
  three write paths implies it — `POST /customers` needs `customers:create` plus
  `customers:legal-identity-manage`, and the legal-identity `PUT` needs
  `customers:legal-identity-manage` plus `customers:legal-identity-view` — so a caller
  who cannot read a customer at all still gets the title, code, detail and status
  (that the identity is taken is a fact about their own request), but the conflict
  names nobody. That is why `duplicates` is optional in the contract.
- The check runs inside the write's own transaction, before the write (and, on
  create, before the customer-number counter is advanced — a refused create burns no
  number).
- `duplicate_legal_identity` is, as of this branch, the module's **only** machine-readable
  error code. Every other refusal is a bare RFC 7807 problem or field-level validation
  text — the module has no broader code vocabulary, by design (see
  `apps/server/internal/customers/errors.go`).
- **Similar names** are a frontend-only hint, not a server feature: while a name is
  typed into the create form, the form calls the ordinary list endpoint with that name
  and shows up to three existing matches with a link. There is no dedicated endpoint
  for it.

## Brreg lookup

`GET /api/v1/customers/lookup/brreg` (`customers:lookup-view`) queries
Brønnøysundregisteret's open Enhetsregisteret API by `legalId` (exact) or `search`
(name, ≥ 2 characters after trimming; `legalId` wins if both are given). It returns,
for each of up to 10 matches, only **`legalId` and `legalName`** — nothing else Brreg
exposes (org form, addresses, NACE code, employee count, bankruptcy flags, …) is
fetched or stored. The lookup is **one-shot**: there is no refresh, no stored
provenance beyond `legal_source: "brreg"`, and editing a customer's name afterwards
does not re-touch the identity. A retried, bounded HTTP call (3 retries, exponential
backoff with jitter, ~4s per attempt) answers **502 "Lookup service unavailable"** on
any exhausted or unretryable failure, a non-2xx upstream status, or a 2xx response
whose body will not decode. [ROADMAP phase 3](../ROADMAP.md#customers) is where the
full record, provenance and a refresh land.

## Permissions

Thirteen keys, category-grouped, every one delegable. Only `view`, `create` and
`update` are non-sensitive.

| Key | Category | Meaning | Sensitive |
| --- | --- | --- | --- |
| `customers:view` | Customers | View customer names, identifiers, and a sanitized activity summary. | no |
| `customers:create` | Customers | Create customers without legal identity data. | no |
| `customers:update` | Customers | Update customer names and basic non-sensitive details. | no |
| `customers:delete` | Customers | Delete (archive) customers and their customer-owned records. | yes |
| `customers:legal-identity-view` | Legal identity | View customer legal identity and registry attribution; also widens list search to legal name/id. | yes |
| `customers:legal-identity-manage` | Legal identity | Add, replace, or remove customer legal identity data; gates the conditional check on a create/update body that carries an `identity`. | yes |
| `customers:contacts-view` | Contacts | View contact names and contact details. | yes |
| `customers:contacts-manage` | Contacts | Create, update, and delete contacts. | yes |
| `customers:associations-view` | Associations | View links between customers and contacts. | yes |
| `customers:associations-manage` | Associations | Create, update, and remove customer-contact links. | yes |
| `customers:timeline-view` | Timeline | View customer timeline entries, notes, provenance, and revisions. | yes |
| `customers:timeline-manage` | Timeline | Create, update, and delete customer timeline entries. | yes |
| `customers:lookup-view` | Lookup | Search the external business registry for legal identities. | yes |

Two permissions combine to widen list search beyond name/number:
`customers:legal-identity-view` alone unlocks legal name/id; **both**
`customers:contacts-view` and `customers:associations-view` together unlock
contact name/email — see [The list endpoint and search](#the-list-endpoint-and-search).

## `contracts.CustomerDirectory`

The one sanctioned way another module reads customer data — an in-process, read-only
port (`apps/server/internal/contracts/directory.go`), implemented by this module and
wired into every module's `Deps` by Compose before any module mounts:

```go
type CustomerDirectory interface {
    Customer(ctx, id int32) (*CustomerEntry, error)          // {ID, Name, Archived}
    Contact(ctx, id int32) (*ContactEntry, error)             // {ID, FirstName, LastName, Email}
    ContactsByEmail(ctx, email string) ([]ContactMatch, error) // {ContactID, CandidateCustomerIDs}
}
```

Three rules hold for every method:

- **A missing row is `(nil, nil)`, not an error.** A caller tells "does not exist"
  from "the lookup failed" only by checking `err`.
- **Archived customers still resolve.** A supply period or a past reference can hold
  a long-lived pointer to a customer that has since been archived, and must still be
  able to show its name — `Archived` tells the caller to decorate the reference, not
  that the lookup failed.
- **More than one match from `ContactsByEmail` is ambiguous, on purpose.** An email
  is not unique across customers; the directory returns every candidate and leaves
  resolving the ambiguity to the caller.

Today's consumers: Energy (naming the customer on a metering point/supply period),
Projects (resolving a project's `customerId`), and Communications (matching an
inbound email to a customer's contact). `ContactsByEmail` itself has no production
caller yet — it exists for Communications' future customer-suggestion feature. The
directory is deliberately narrow: no billing profile, no batch lookup (a caller
resolving many ids does one call per id today), nothing beyond `{ID, Name, Archived}`
for a customer. [ROADMAP phase 2](../ROADMAP.md#customers) is where it grows.

## The frontend

`@vantigo/customers-ui` (`apps/customers/frontend`), composed into the host SPA.

- **List** (`/customers`) — customer number column (replacing the old id column),
  status and type filters, sortable headers (number, name, created), all reflected in
  the URL (`status`, `type`, `sortBy`, `sortDirection`) and validated by the host
  route, plus the KPI row and a spotlight-openable create form.
- **Detail** (`/customers/:id`) — a host-composed page: this package owns the header
  (name, legal-identity badges, status/type badges, edit and change-type actions) and
  an overview tab (contacts card, timeline); other modules add their own tabs (Energy,
  Projects) the same way the host composes any module's tabs onto a customer.
  **Archive and Restore** are landing in this same branch, gated on the host's
  permission check rather than a local one: Archive (with a confirmation dialog)
  needs `customers:delete`, Restore — a `PUT` with `status: "active"` — needs
  `customers:update`; an archived customer shows a banner.
- **Form** (create/edit modal) — sends `revision` on every edit, so a stale write is
  caught by the backend's 409 rather than silently overwriting a concurrent change;
  a 409 revision conflict tells the user the customer changed underneath them and
  reloads it; a 409 duplicate identity shows who already holds it, with a link to
  each, and a "Create/Save anyway" action that resubmits with
  `allowDuplicateIdentity: true`; the similar-names hint queries the list endpoint
  live while a name is typed.
- **Timeline** — each entry shows its author, from the `actorDisplay` D1 added.

## API

Every operation is under `/api/v1/customers`, authenticated with the shared identity
session cookie. 30 operations in total, each exercised by the module's own
contract-validated test coverage gate — every operation in `openapi/customers.yaml`
must be exercised by at least one successful exchange, with no allow-list.

| Endpoint | Access |
| --- | --- |
| `GET /`, `GET /{id}` | `customers:view` |
| `POST /` | `customers:create` (plus `customers:legal-identity-manage` if the body carries an `identity`) |
| `PUT /{id}`, `PUT /{id}/type` | `customers:update` + `customers:view` (plus `legal-identity-manage` if the body carries an `identity`) |
| `DELETE /{id}` (archive) | `customers:delete` |
| `GET /{id}/legal-identity` | `customers:legal-identity-view` |
| `PUT /{id}/legal-identity` | `customers:legal-identity-manage` + `customers:legal-identity-view` |
| `DELETE /{id}/legal-identity` | `customers:legal-identity-manage` |
| `GET /{id}/contacts`, `GET /contacts/{id}/customers` | `customers:associations-view` + `customers:contacts-view` |
| `POST /{id}/contacts` (attach) | `customers:associations-manage` + `customers:contacts-view` |
| `PUT /{id}/contacts/{contactId}` | `customers:associations-manage` + `customers:contacts-view` |
| `DELETE /{id}/contacts/{contactId}` (detach) | `customers:associations-manage` |
| `GET /contacts`, `GET /contacts/{id}` | `customers:associations-view` + `customers:contacts-view` (list) / `customers:contacts-view` (get) |
| `POST /contacts`, `PUT /contacts/{id}` | `customers:contacts-manage` + `customers:contacts-view` |
| `DELETE /contacts/{id}` | `customers:associations-manage` + `customers:contacts-manage` |
| `GET /{id}/timeline`, `GET /{id}/timeline/{entryId}`, `GET /{id}/timeline/{entryId}/revisions` | `customers:timeline-view` |
| `POST /{id}/timeline`, `PUT /{id}/timeline/{entryId}` | `customers:timeline-manage` + `customers:timeline-view` |
| `DELETE /{id}/timeline/{entryId}` | `customers:timeline-manage` |
| `GET /lookup/brreg` | `customers:lookup-view` |
| `GET /stats`, `/stats/attention`, `/stats/summary`, `/stats/timeseries` | `customers:view` |

`/stats/attention` is a stub today — always an empty array, since no "attention items"
feature exists behind it yet ([ROADMAP phase 3](../ROADMAP.md#customers)).

## What comes next

This branch closed the foundation gaps: attribution, search reach, concurrency, the
duplicate guard, and validation. It added no new customer data. The known remaining
gaps — no address or contact info on the customer itself, Brreg returning only two
fields, `/stats/attention` permanently empty, `ContactsByEmail` unused in production,
no batch directory lookup, no CSV import/export, no merge — are exactly what
[ROADMAP.md's Customers section](../ROADMAP.md#customers) is built around, itself
drawn from
[`docs/superpowers/research/2026-09-21-customers-module-next.md`](superpowers/research/2026-09-21-customers-module-next.md),
which also compares this module against the Nordic ERP/accounting and international
CRM/PSA fields it was benchmarked against.

## Development

The backend is `apps/server/internal/customers`, with typed queries generated by sqlc
and the server interface generated by oapi-codegen from `openapi/customers.yaml`. The
UI lives in `apps/customers/frontend` (`@vantigo/customers-ui`) and is composed by the
host SPA in `apps/host/frontend`.

The Go package's tests run every HTTP exchange through a contract-validating client
and gate on operation coverage, the same pattern every other module here uses. The
user directory (`contracts.UserDirectory`) is the real one, composed; depguard forbids
this module from importing another module even in tests.

```bash
bun run --cwd apps/customers/frontend test
cd apps/server && go test ./internal/customers/...
```
