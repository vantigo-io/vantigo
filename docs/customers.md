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
- **Contact info** *(on the customer itself, not to be confused with a Contact
  above)* — `email varchar(255)`, `phone varchar(30)`, `website varchar(2048)` on
  `customers.customers` itself, all nullable: what reaches the customer, not one of
  its contacts. See [Contact info, addresses and the billing
  profile](#contact-info-addresses-and-the-billing-profile).
- **Address** — a row in `customers.customer_addresses` (`id`, `customer_id` FK
  `ON DELETE CASCADE`, `type`, `label varchar(100)`, `line1 varchar(255) NOT NULL`,
  `line2 varchar(255)`, `postal_code varchar(20)`, `city varchar(100)`, `region
  varchar(100)`, `country varchar(2) NOT NULL`, `is_primary boolean NOT NULL`,
  `created_at`, `updated_at`) — any number per customer, typed `postal`/`invoice`/
  `delivery`/`visiting`, at most one primary per type. See [Contact info, addresses
  and the billing profile](#contact-info-addresses-and-the-billing-profile).
- **Billing profile** — ten nullable columns on `customers.customers` itself
  (`invoice_email`, `reminder_email`, `payment_terms_days`, `currency`, `language`,
  `invoice_delivery`, `reminder_delivery`, `peppol_id`, `gln`, `buyer_reference`):
  every column NULL means "not decided here — whoever invoices uses its own
  default". See [Contact info, addresses and the billing
  profile](#contact-info-addresses-and-the-billing-profile).
- **Timeline entry** — one row per customer event, generated or manual, each with its
  own revision history. See [The timeline](#the-timeline).

`SafeCustomerResponse` — what `GET`/`POST`/`PUT` on a customer return — always carries
a `timelineSummary` (entry count, most recent `occurredOn`) even to a caller with no
`customers:timeline-view`: it is the "sanitized activity summary"
`customers:view`'s own description promises, not a timeline read. The full
`identity` block is only populated when the caller holds `customers:legal-identity-view`;
otherwise it is simply absent from the response, not empty. Since the invoice-ready
customer branch, `SafeCustomerResponse` also always carries `contactInfo` (each of
`email`/`phone`/`website` independently nullable) — unlike `identity`, this is
never gated: it is shown to any caller with plain `customers:view`, the same
permission the endpoint already needs. The billing profile is **not** part of
`SafeCustomerResponse` — see [Billing profile](#billing-profile) for why.

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

## Contact info, addresses and the billing profile

The invoice-ready customer branch gave a customer its own contact details, a typed
list of addresses and a billing profile, and let other modules read all three
through the directory. None of it changed `PUT /customers/{id}` — name, status and
legal identity are still what that endpoint replaces. The new data is written
through three sub-resources instead, the way legal identity and type already were:

| Operation | Access |
| --- | --- |
| `PUT /customers/{id}/contact-info` | `customers:update` + `customers:view` |
| `GET /customers/{id}/addresses`, `POST /customers/{id}/addresses` | read: `customers:view`; create: `customers:update` + `customers:view` |
| `PUT /customers/{id}/addresses/{addressId}`, `DELETE /customers/{id}/addresses/{addressId}` | `customers:update` + `customers:view` |
| `GET /customers/{id}/billing-profile` | `customers:view` |
| `PUT /customers/{id}/billing-profile` | `customers:billing-manage` + `customers:view` |

**Why sub-resources, not a wider `PUT /customers/{id}`.** A full-replace PUT over
optional fields cannot tell "absent" from "clear" without nullable-wrapper types,
and `PUT /customers/{id}`'s own recorded exchange corpus
(`openapi/testdata/exchanges/customers.jsonl`) is frozen — every change to an
existing schema had to be additive, so an old recorded body could not suddenly be
asked to mean something new. A sub-resource PUT sidesteps that: it is a full
replace of one self-contained thing, every field present or null, and oapi-codegen's
generated `*string`/`*int32` fields already collapse "the key was missing" and
"the key was `null`" into the same nil pointer — none of the three validators below
ever has to tell the two apart.

Reading any of the three needs only `customers:view`, same as the rest of the
customer card: an address and an invoice email are what a customer card is for in
every system this module was benchmarked against. **Writing the billing profile
needs its own permission, `customers:billing-manage`**, deliberately narrower than
`customers:update` — payment terms and delivery channel decide when and how money
arrives, and the person who may rename a customer is not thereby the person who may
give it 90 days' credit.

### Contact info

`email`, `phone` and `website` live on `customers.customers` itself, one email/
phone/website reaching the customer, not one of its contacts. Each is
independently nullable; a blank or whitespace-only request value is stored as
NULL, not a validation error.

| Field | Rule |
| --- | --- |
| `email` | Trimmed, at most 255 UTF-16 code units, one and only one `@` with a non-empty local part and a domain containing a `.` that is not trailing, no embedded whitespace. Stored as typed — **not** lower-cased, unlike a Contact's own email. |
| `phone` | Trimmed, at most 30 UTF-16 code units, only digits, spaces and `+ - ( )`, and at least five digits. |
| `website` | Trimmed, at most 2048 UTF-16 code units, an absolute `http`/`https` URL (the same shape the timeline's `sourceUrl` field checks) — the same length limit as `sourceUrl`, checked independently since this module's own wording for it is its own, not a shared function. |

The email shape check (`hasValidEmailShape`) is now **one function shared with
Contacts**: a review fix folded the two modules' near-identical inline checks into
one, so a tab character or a trailing dot on the domain is rejected the same way
whichever endpoint validates it. Only the message text and the no-lower-casing
normalization stay specific to a customer's own contact info.

`PUT /customers/{id}/contact-info` is a full replace of all three fields: (1)
validate every field independently, every error reported together, keyed
`email`/`phone`/`website`; (2) 404 if the customer does not exist; (3) 409 if a
supplied `revision` disagrees with the row just read — checked *before* the no-op
comparison, so resubmitting the current contact info with a stale revision is
still a conflict; (4) a no-op (all three fields unchanged) writes nothing at all —
no revision bump, no `updated_at` move, no timeline event; (5) otherwise the
write, revision-guarded the same way `PUT /customers/{id}` is, and recorded as
`customer.contact_info_updated`. `POST /customers` accepts an optional nested
`contactInfo` (same validation, errors keyed `contactInfo.<field>`) so a customer
can be created complete in one call; `PUT /customers/{id}` does not carry it —
contact info is edited through its own sub-resource from then on.

### Addresses

`customers.customer_addresses`: any number of `postal`, `invoice`, `delivery` or
`visiting` addresses per customer (`label` tells two addresses of the same type
apart, e.g. two `delivery` addresses), at most 50 per customer (400 beyond that,
checked under the customer row's lock so two concurrent creates can never both
slip in as the 50th and 51st).

**One primary per (customer, type).** The invariant is kept in two layers:

1. **The application, under a lock.** Every address write locks the customer row
   `FOR NO KEY UPDATE` first (`queries/addresses.sql`'s `LockCustomer`), then does
   everything else — read, demote, promote, insert/update/delete — inside that
   same transaction. Address writes carry no revision of their own and never touch
   `customers.customers`' own columns, so the customer lock, not a revision guard,
   is what serializes two concurrent writers on the same customer's addresses.
   Within the lock, **demote happens before promote, and delete happens before
   promote**: setting a different address primary always demotes the type's
   current primary first (`demoteCurrentPrimary`), and deleting a primary address
   deletes the row before promoting the oldest remaining one of that type
   (`promoteOldestOfType`) — never the other order, since a transient state with
   two primaries of one type, even for a moment inside the same transaction, would
   violate the index below.
2. **The database, as the last word.** A partial unique index,
   `ux_customer_addresses_primary` on `(customer_id, type) WHERE is_primary`
   (migration `00018`), is what actually enforces the invariant if the
   application-layer ordering above were ever wrong — it is the backstop, not the
   primary mechanism.

The rest of the invariant's shape: the **first** address of a type is primary
whatever the request says. Setting `isPrimary: true` on a different address of the
same type demotes the old one in the same transaction. `isPrimary: false` on the
address that is currently the only or primary one of its type is **refused** (400,
field `isPrimary`) — there is always a primary while any address of the type
exists. A type change re-evaluates *both* types: leaving a type as its primary
promotes the oldest remaining address there (if any, since this one no longer
counts); landing in a new type is primary if it is the first address there, or if
`isPrimary` was requested (demoting that type's own current primary first).
**Deleting** the primary address is not refused the way a `PUT` clearing it would
be — it promotes the oldest remaining address of the same type instead, since a
delete of the last address of a type simply makes the invariant vacuous. A "make
primary" that demotes a different address, or a delete that promotes one, records
only **one** timeline event — for the address the request actually named — never
two.

`country` uses the same ISO 3166-1 alpha-2 list the legal identity does. A
Norwegian address (`country = "no"`) additionally needs a four-digit `postalCode`
and a non-blank `city`; every other country accepts `postalCode`/`city` as free
text. `label`/`line2`/`postalCode`/`city`/`region` are all optional — blank or
absent both mean "not set", never a validation error on their own.

**Address writes do not touch `customers.customers`' `revision` at all** — they
neither bump it nor accept one: an address is small, and last-writer-wins on one
is acceptable (the controller ruling). They record `customer.address_added`,
`customer.address_updated` or `customer.address_removed` instead.

**The invoice address** anyone needs — the directory's `BillingProfile` and the
billing profile's own `no_invoice_address` warning alike — is resolved as:
**primary `invoice`, else primary `postal`, else none.** Because at most one
primary per (customer, type) can ever exist, that resolution is a priority pick
between at most two candidate rows, never an arbitrary one among many.

### Billing profile

Ten nullable columns on `customers.customers` (migration `00019`), every one
meaning "not decided here — whoever invoices uses its own default" when NULL:

| Field | Rule |
| --- | --- |
| `invoiceEmail`, `reminderEmail` | Contact info's email rule. Kept separate from each other and from the customer's own contact-info email because reminders cannot travel as EHF or eFaktura — the reminder channel is its own decision. |
| `paymentTermsDays` | Integer, 0–365 inclusive. |
| `currency` | Three-letter ISO 4217 code, upper-cased (the shape only — not checked against the set of actually-assigned codes, the same rule Projects' own currency field uses). |
| `language` | `nb` or `en` — the languages a document can be produced in. |
| `invoiceDelivery` | One of `email`, `ehf`, `efaktura`, `paper`. |
| `reminderDelivery` | One of `email`, `paper` — a narrower set than `invoiceDelivery`'s, since a reminder can never travel as EHF or eFaktura. |
| `peppolId` | `<4-digit scheme>:<identifier>` (e.g. `0192:923609016`); identifier 1–50 characters of `[A-Za-z0-9-]`. For scheme `0192` (Norway's organisasjonsnummer scheme) the identifier must additionally be a valid Norwegian organisation number (the same mod-11 check the legal identity uses); every other scheme's identifier is accepted on shape alone. |
| `gln` | 13 digits (whitespace stripped first) with a valid GS1 mod-10 check digit. |
| `buyerReference` | At most 100 characters — the default "deres referanse". |

**There are deliberately no cross-field rules** — no "EHF needs a Peppol id", no
"eFaktura only makes sense for a business". A profile is filled in over time, field
by field, and whether a customer *can actually be invoiced* a given way is
Invoices' question to ask at send time, not this module's to gatekeep at save
time. What `GET .../billing-profile` gives instead is a computed, never-stored
`warnings: string[]`, recomputed at read time in this fixed order:

| Warning | Raised when |
| --- | --- |
| `ehf_without_recipient` | `invoiceDelivery` is `ehf`, there is no explicit `peppolId`, **and** `derivedPeppolID` — the one predicate this check and the directory's own `PeppolID` resolution rule (`BillingProfile`, further down) now share — finds nothing to derive: the identity's own `country` must be `no`, the **customer's own** `type` (not the identity's — a legacy row can hold `legal_type` `NULL`) must be `business`, and the identity's `id` must itself pass the Norwegian organisation-number check, so a malformed or pre-validation legacy `id` derives nothing and still raises the warning. |
| `email_without_address` | `invoiceDelivery` is `email`, there is no `invoiceEmail`, **and** no contact-info `email` either. |
| `efaktura_for_business` | `invoiceDelivery` is `efaktura` and the customer's `type` is `business`. |
| `no_invoice_address` | The customer has no primary `invoice` address and no primary `postal` address either — the same resolution rule addresses use throughout. |

The `ehf_without_recipient` check reads the customer's legal identity **even when
the caller lacks `customers:legal-identity-view`** — this sub-resource's own read
gate is only `customers:view`. That is a deliberate, narrow exception, not a leak:
the only thing that reaches the caller through the warning is whether an EHF
recipient *can or cannot be derived at all*, a fact the same caller can already
learn by holding `customers:billing-manage`, setting `invoiceDelivery` to `"ehf"`,
and reading the warning back — and `GET .../billing-profile` never exposes the
identity's own `country`/`id`/`name` fields to a caller who cannot see them
elsewhere. Nothing about the identity's actual content is disclosed, only a
boolean fact already inferable from the caller's own write access.

`GET` answers **200 with every field absent** (plus whatever warnings an empty
profile still raises — at least `no_invoice_address` if there is no invoice
address) for a customer that has none: a billing profile always exists
conceptually, unlike the legal identity's own GET, which answers 204. 404 only
when the customer itself does not exist.

`PUT /customers/{id}/billing-profile` needs `customers:billing-manage` **and**
`customers:view` — a caller who could not otherwise see the customer cannot manage
its billing either. It is revision-guarded and no-op'd exactly like the
contact-info PUT: a supplied stale `revision` is a 409 checked before the no-op
comparison; a request identical in all ten fields writes nothing — no revision
bump, no `updated_at` move, no `customer.billing_profile_updated` event.

The billing profile is **not** part of `SafeCustomerResponse` — list pages do not
need it, and keeping it off the general response keeps D1's permission story
simple (a caller with plain `customers:view` never has ten billing columns handed
to it through the list or GET-by-id endpoints; only the dedicated sub-resource
does, and that sub-resource's own GET needs nothing more than `customers:view`
either).

## The timeline

Every customer has a timeline: **generated** entries the module itself writes when
something happens, and **manual** entries a user writes by hand.

Generated event types: `customer.created`, `customer.updated`, `customer.type_changed`,
`customer.status_changed`, `customer.contact_attached`,
`customer.contact_relationship_updated`, `customer.contact_detached`,
`customer.contact_removed`, `customer.contact_info_updated`,
`customer.billing_profile_updated`, `customer.address_added`,
`customer.address_updated`, `customer.address_removed`. These are immutable — there
is no edit or delete endpoint for a generated entry.

- `customer.contact_info_updated` and `customer.billing_profile_updated` each carry
  a `before`/`after` snapshot of every field the sub-resource owns, plus a `changes`
  map of only the fields that actually moved — the same shape `customer.updated`
  already used for name/identity changes. Both are only ever recorded once the
  handler has confirmed something changed (the sub-resource's own no-op rule), so
  `changes` is never empty on either event.
- Each address event's payload always carries `addressId`, `type`, `label` and a
  one-line `display` rendering (e.g. "Storgata 1, 0155 Oslo, NO");
  `customer.address_updated` also carries `before`/`after`/`changes`, the same shape
  the other two updated-events use. **The stored `summary` column is truncated to
  its `varchar(500)` limit** (the same UTF-16 truncation the manual timeline entry's
  own summary already needed) — a fully-populated address (two 255-character lines
  plus label/postal code/city/country) can produce a one-line display well past 500
  characters on its own, and an untruncated insert would fail at the database with
  a "value too long" error on an otherwise valid request. The payload's own
  `display` field is **never** truncated — only the summary column is.

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
- **`search` never reaches data the caller could not otherwise see** — with one
  exception it deliberately does not gate. It always matches the name, the customer
  number, and, since the invoice-ready customer branch, the customer's own `email`
  and, its whitespace stripped, its own `phone` — all case-insensitively, as a
  substring. This is **ungated**: unlike the legal-identity and contact branches
  below, there is no permission check in front of the customer's own email/phone,
  because `SafeCustomerResponse`'s `contactInfo` is already shown to anyone who can
  list customers at all (`customers:view`) — search reaching what the response
  already reaches is not a new disclosure. It additionally matches the legal name
  and legal id **only when the caller holds `customers:legal-identity-view`**, and a
  linked contact's first/last name and email (canonical or association-specific)
  **only when the caller holds both** `customers:contacts-view` and
  `customers:associations-view`. A caller with neither gets name, number and its own
  contact info — search is deliberately not an oracle for data the response would
  otherwise withhold. The same permission that widens search also decides whether
  the response's `identity` block is populated, so one `hasPermission` call answers
  both questions for the identity branch.
- The customer number and legal id are additionally matched with all whitespace
  stripped from both the value and the search term, so `923 609 016` finds a legal id
  stored as `923609016`.

## Revision and concurrency

`customers.customers` carries `revision integer NOT NULL DEFAULT 1`. **Every write to
the row** — a general update, a type change, a legal-identity put or delete, an
archive, and, since the invoice-ready customer branch, a contact-info or a
billing-profile `PUT` — increments it by one; both new sub-resource writes live on
`customers.customers` itself (contact info and the ten billing columns), so both
bump `revision` the same way a name/status update does. A request that turns out to
write nothing is not a write: like `PUT /customers/{id}`, a legal-identity `PUT`
that resubmits the identity already stored (equal in all five fields), or a
contact-info/billing-profile `PUT` that resubmits exactly what is already stored,
issues no `UPDATE` at all, so neither `revision` nor `updated_at` moves and no event
is recorded.

**Addresses are the one exception.** `customers.customer_addresses` is its own
table, not a column on `customers.customers`, and an address write neither bumps
the customer's `revision` nor accepts one of its own — see [Contact info, addresses
and the billing profile](#contact-info-addresses-and-the-billing-profile). The
customer row's own lock, taken by every address write, is what serializes
concurrent address writers instead.

- `SafeCustomerResponse` carries `revision` (optional in the schema, for corpus
  compatibility — see below).
- `PUT /customers/{id}`, `PUT /customers/{id}/type`, `PUT /customers/{id}/contact-info`
  and `PUT /customers/{id}/billing-profile` all accept an optional `revision` in the
  request body. When present and it disagrees with the row's current value, the
  answer is **409 "Customer revision conflict"** and nothing is written; the same
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

Fourteen keys, category-grouped, every one delegable. Only `view`, `create` and
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
| `customers:billing-manage` | Billing | Set a customer's payment terms, invoice delivery and billing addresses for documents. | yes |

`customers:billing-manage` is the one key with no earlier counterpart: writing a
customer's billing profile is deliberately gated separately from
`customers:update`, on the reasoning [Contact info, addresses and the billing
profile](#contact-info-addresses-and-the-billing-profile) gives above. Note that
reading a billing profile (`GET .../billing-profile`) needs only `customers:view` —
`customers:billing-manage` gates the *write* alone, the same split `customers:update`
gets from the general customer PUT.

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
    Customer(ctx, id int32) (*CustomerEntry, error)             // {ID, Name, Archived}
    Customers(ctx, ids []int32) ([]CustomerEntry, error)         // batch; a missing id is simply absent
    Contact(ctx, id int32) (*ContactEntry, error)                // {ID, FirstName, LastName, Email}
    ContactsByEmail(ctx, email string) ([]ContactMatch, error)   // {ContactID, CandidateCustomerIDs}
    BillingProfile(ctx, id int32) (*CustomerBillingProfile, error) // resolved, see below
}
```

Rules that hold across every method here:

- **A missing row is `(nil, nil)`, not an error** — for `Customer`, `Contact` and
  `BillingProfile` alike. A caller tells "does not exist" from "the lookup failed"
  only by checking `err`. `Customers` is the batch exception: a missing id is
  simply absent from the result slice, never an entry in it and never an error
  either — the same rule, restated for a slice instead of a pointer.
- **Archived customers still resolve**, from `Customer`, `Customers` and
  `BillingProfile` alike. A supply period, a project or a past invoice can hold a
  long-lived pointer to a customer that has since been archived, and must still be
  able to show its name or invoice it again — `Archived` tells the caller to
  decorate the reference, not that the lookup failed.
- **More than one match from `ContactsByEmail` is ambiguous, on purpose.** An email
  is not unique across customers; the directory returns every candidate and leaves
  resolving the ambiguity to the caller.

### `Customers` — the batch lookup

`Customers(ctx, ids)` is `Customer`'s batch twin, for a caller naming a whole page
of customers rather than one per row: one round trip (`DirectoryCustomers`) instead
of one call per distinct id. A nil or empty `ids` is an empty, non-nil result with
no query made; a duplicate id in `ids` is tolerated, since the row itself only
exists once. Projects' project list is the first consumer: it gathers the page's
distinct customer ids once and calls `Customers` a single time, so a page of 25
projects for one customer costs one directory call, and a page spanning five
customers still costs exactly one — not 25 or five.

### `BillingProfile` — what an invoice needs, already resolved

`BillingProfile(ctx, id)` carries what an invoice needs in one call: id, customer
number, name, type, archived, the legal identity's `country`/`id`/`name` when
present, the resolved invoice address when any, and the billing fields below —
**every resolution rule lives here, once**, so no consumer ever re-derives an
invoice email, a reminder email or a Peppol id for itself.

| Field | Resolution rule |
| --- | --- |
| `InvoiceAddress` | The billing profile's resolved invoice address (D3's rule: primary `invoice`, else primary `postal`, else `nil`) — never a list, since a consumer only ever needs the one address to print. |
| `InvoiceEmail` | The billing profile's own `invoiceEmail`, else the customer's own contact-info `email`, else `""`. |
| `ReminderEmail` | The billing profile's own `reminderEmail`, else the `InvoiceEmail` just resolved above — reminders fall back to where an invoice would go, never straight to the contact-info email. |
| `PeppolID` | The billing profile's own explicit `peppolId`, else `derivedPeppolID(identity, customerType)`: `"0192:<legal id>"` when the identity's country is `"no"`, **the customer itself (not the identity) is of type `"business"`**, and the identity's `id` itself passes the Norwegian organisation-number check (a malformed or pre-validation legacy `id` derives nothing), else `""`. The same predicate backs `billingWarnings`'s `ehf_without_recipient` check above, so the two can never disagree about whether a recipient exists. |
| `PaymentTermsDays`, `Currency`, `Language`, `InvoiceDelivery`, `ReminderDelivery`, `GLN`, `BuyerReference` | The billing profile's own value, `nil`/`""` if never set — no further resolution. |

**Every consumer treats `""` (a string field) or `nil` (`PaymentTermsDays`,
`InvoiceAddress`) as "not decided — use your own default"**, never as an error or
an empty-but-meaningful value: a caller receiving one back has learned that
nothing was decided for that field, not that the lookup failed, and is free to
apply whatever default its own module would otherwise presume.

Today's consumers: Energy (naming the customer on a metering point/supply period),
Projects (resolving a project's `customerId`, and now naming a whole list page's
customers through `Customers` in one call), and Communications (matching an
inbound email to a customer's contact). `ContactsByEmail` still has **no production
caller** — it exists for Communications' future customer-suggestion feature.
`BillingProfile` is ready for Invoices to read once that module exists — no
consumer yet, the same "built ahead of its caller" position `ContactsByEmail` has
been in since the foundation.

## The frontend

`@vantigo/customers-ui` (`apps/customers/frontend`), composed into the host SPA.

- **List** (`/customers`) — customer number column (replacing the old id column),
  status and type filters, sortable headers (number, name, created), all reflected in
  the URL (`status`, `type`, `sortBy`, `sortDirection`) and validated by the host
  route, plus the KPI row and a spotlight-openable create form.
- **Detail** (`/customers/:id`) — a host-composed page: this package owns the header
  (name, legal-identity badges, status/type badges, edit and change-type actions) and
  an overview tab (contact & addresses card, billing card, contacts card, timeline);
  other modules add their own tabs (Energy, Projects) the same way the host composes
  any module's tabs onto a customer.
  **Archive and Restore** are gated on the host's permission check rather than a
  local one — the header takes `canArchive`/`canRestore` props and never fetches
  permissions itself: Archive needs `customers:delete` and goes through the shared
  confirmation dialog, Restore needs `customers:update` and is a plain `PUT` with
  `status: "active"` and the row's revision, straight through with no confirmation
  (it undoes nothing). Archive is hidden for a customer that is already archived,
  Restore for one that is not, and an archived customer shows a banner above the
  header. A `PUT` refused as a stale revision tells the user the customer changed
  and refetches it, rather than leaving a failure behind a button that would keep
  failing.
- **Contact & addresses card** (design D6) — email/phone/website (rendered as
  `mailto:`/`tel:`/an external link) with an edit modal, and the typed address list
  below it, grouped by type in the fixed order invoice/postal/delivery/visiting,
  primary badged, with add/edit/delete modals and a "Make primary" action (delete
  confirms through the shared confirmation dialog). The card itself is shown to
  everyone who can see the page; editing sits behind a `canEdit` capability prop the
  host passes down from its own read of `customers:update` — this package never
  fetches permissions itself, the same convention `canArchive`/`canRestore` follow
  on the header. Contact info rides on the customer row already fetched for the
  header, so the card reads it off that cache; addresses are their own sub-resource
  with their own query.
- **Billing card** (design D6) — the billing profile shown read-only, with each
  computed warning explained in words rather than shown as its raw code, and an
  edit modal behind a `canManageBilling` capability prop sourced the same way
  `canEdit` is, from the host's own read of `customers:billing-manage`. A revision
  conflict on save follows the same Reload pattern the contact-info modal and
  `CustomerFormModal` already use: the modal re-seeds itself and the revision the
  next save sends, rather than leaving a write behind a button that would keep
  failing.
- **Form** (create/edit modal) — sends `revision` on every edit, so a stale write is
  caught by the backend's 409 rather than silently overwriting a concurrent change;
  a 409 revision conflict tells the user the customer changed underneath them and
  offers Reload, which re-seeds the form *and the revision the next save sends*; a
  409 duplicate identity shows who already holds it, with a link to each — or, when
  the server withholds that list from a caller without `customers:view`, just says
  the identity is taken — and offers a "Create/Save anyway" action that resubmits
  with `allowDuplicateIdentity: true`; the similar-names hint queries the list
  endpoint live while a name is typed. The **create** form gained optional `email`
  and `phone` fields (design D2/D6) — `website` is not offered on create, only
  through the contact-info card afterwards — sent as a nested `contactInfo` only
  when at least one of the two is filled; validation errors on
  `contactInfo.<field>` are mapped back onto the form's own bare `email`/`phone`
  fields. The **list** gained no new columns, and its search placeholder now
  mentions email, matching the backend's now-ungated email/phone search.
- **Timeline** — each entry, and each revision in its history, shows its author. The
  label comes from D1's `actorKind`, not the snapshotted name: the server's sentinels
  for "nobody in particular" (`System`, `Unattributed`, `Unknown user`) are English
  literals, so they are mapped to catalogue keys instead of shown as stored
  (`src/lib/actor-label.ts`). A real person's name is never translated.

## API

Every operation is under `/api/v1/customers`, authenticated with the shared identity
session cookie. 37 operations in total, each exercised by the module's own
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
| `PUT /{id}/contact-info` | `customers:update` + `customers:view` |
| `GET /{id}/addresses` | `customers:view` |
| `POST /{id}/addresses` | `customers:update` + `customers:view` |
| `PUT /{id}/addresses/{addressId}`, `DELETE /{id}/addresses/{addressId}` | `customers:update` + `customers:view` |
| `GET /{id}/billing-profile` | `customers:view` |
| `PUT /{id}/billing-profile` | `customers:billing-manage` + `customers:view` |
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

The foundation branch closed the gaps that would otherwise be built upon:
attribution, search reach, concurrency, the duplicate guard, and validation. This
branch — the invoice-ready customer, delivery A — is what it was built to carry:
a customer now has its own contact info and typed addresses, and a billing profile
behind its own permission, and `contracts.CustomerDirectory` can answer a batch of
ids and resolve a billing profile in one call. A customer still cannot be invoiced
by this module alone, though — that is Invoices' job once it exists, reading
through the directory this branch built for it.

**Delivery B** (its own spec, own PR, already scoped in the design) is the one
piece deliberately left out here: a Peppol capability lookup (SML DNS → SMP → BIS
Billing 3.0 support) that sets a customer's `invoiceDelivery` to `ehf`
automatically, the way every Nordic competitor surveyed but Fortnox does. Nothing
in this branch waits for it — `peppolId` and `invoiceDelivery` are plain fields a
person can fill in by hand meanwhile, and the billing profile's
`ehf_without_recipient` warning already tells them when EHF has no recipient to
send to.

Past delivery B, the remaining gaps are exactly what
[ROADMAP.md's Customers section](../ROADMAP.md#customers) is built around —
Brreg returning only two fields (phase 3), `/stats/attention` permanently empty
(phase 3), `ContactsByEmail` still unused in production, no CSV import/export, no
merge (phase 6) — itself drawn from
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
