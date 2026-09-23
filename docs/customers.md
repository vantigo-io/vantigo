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
- **Owner** — `owner_user_id` on `customers.customers` itself, naming one user of
  this installation or nobody; no foreign key to identity. See [Owner and
  tags](#owner-and-tags).
- **Tag** — a vocabulary word (`customers.tags`, unique case-insensitively) a
  customer's own set (`customers.customer_tags`) draws from; off the customer row,
  so a tag change carries no `revision`. See [Owner and tags](#owner-and-tags).
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
| `ehf_recipient_not_registered` | `invoiceDelivery` is `ehf` **and** the last [Peppol lookup](#peppol-lookup) on record for the participant this profile would look up now says `not_registered`, or `registered` without `canReceiveInvoice`. |
| `ehf_available` | Not a problem — an offer. The last Peppol lookup on record says `registered` with `canReceiveInvoice`, and `invoiceDelivery` is anything but `ehf` (unset counts as "anything but `ehf`"). |

Both of the last two read `peppolLookup` (below) rather than making a network
call of their own: `GET`/`PUT .../billing-profile` never touch the Peppol
network — only `POST .../peppol-lookup` does — so a lookup is never a side
effect of reading or saving the profile. (Nor is every lookup a person's click
any more: the registry workers' own schedule asks too — see
[Registry workers](#registry-workers) — but it goes through the same
lookup-and-store function, never through this endpoint or the profile.)

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

`GET`/`PUT` both also carry an optional `peppolLookup` — the last answer
`POST .../peppol-lookup` recorded, present only when it is still the answer
for the participant this profile would look up *now*. See [Peppol
lookup](#peppol-lookup).

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

## Owner and tags

Phase 4's first delivery, decided in
[`docs/superpowers/specs/2026-09-23-customers-owner-tags-design.md`](superpowers/specs/2026-09-23-customers-owner-tags-design.md).
Two questions a customer list has to be able to answer: *which of these are
mine?*, and *which of these are of this kind?*

### The owner

`customers.customers.owner_user_id` names **one user** of this installation — not
a team, not free text. The roadmap's "account manager" is one person accountable
for the relationship, and every system this module was benchmarked against models
it as exactly that (Salesforce's Account Owner, HubSpot's Company owner, Business
Central's Salesperson code). Because it is a column on the customer row, it shares
the row's `revision`: `PUT /customers/{id}/owner` is a revision-guarded
sub-resource write exactly like contact info's — same ordering, same guard, same
no-op rule, same 409 — so a concurrent edit cannot lose it, and a request that
names the owner the customer already has writes nothing at all.

The user is named through `contracts.UserDirectory` and nowhere else — the display
name is never stored on the customer. That has three consequences worth stating,
because each is a decision rather than a side effect:

- **There is no foreign key** to `identity.users`. This module may not read
  identity's schema, and a foreign key would decide in the database what the
  design decides in prose: an owner who is later disabled, or whose account is
  removed, **keeps the customer**. Nothing is revoked behind anyone's back.
- The owner is therefore reported with an `active` flag, and an id the directory
  no longer knows reads as `Unknown user` with `active: false` — the same answer
  a timeline entry's actor gets for a vanished account.
- To *be assigned*, on the other hand, a user must exist and be active: a field
  error on `ownerUserId` otherwise (`User <id> does not exist`, `User <id> is
  disabled and cannot own a customer`). `GET /customers/assignable-users` is the
  picker's own search — the directory's active users, at most twenty (`limit`,
  when given, must be between 1 and 20) — and it sits behind `customers:update`,
  because who a customer *could* be given to is only useful to whoever may give
  it.

The list filters on `ownerId`, which takes a user id, the literal `me`, or
`none`. `me` is resolved from the session, never from anything the request says
about who the caller is, which is why "My customers" in the UI needs nothing from
the host. `none` is the manager's "unassigned". Changing the owner records
`customer.owner_changed` on the timeline, with both names snapshotted at the time
of the change, and only when the owner actually changed — a no-op resubmit of the
owner the customer already has, a disabled account included, answers 200 with
nothing written and no directory lookup made for the candidate.

### Tags

Two tables (`customers.tags`, `customers.customer_tags`), shaped after
communications' own tags with one difference that matters: the uniqueness is on
`lower(name)`. A tag is a vocabulary word, so `VIP` and `vip` are the same word —
an installation holding both has a filter that silently splits its customers in
two. Case is still preserved as it was typed.

A colour is one of Mantine's named colours (`gray red pink grape violet indigo
blue cyan teal green lime yellow orange`) or null, validated by the server. That
looks like a layering violation and is not: the alternative is every consumer
sanitising whatever arrived, and a chip painted with a value no stylesheet knows
is an invisible chip.

A customer's tags are **replaced as a set** (`PUT /customers/{id}/tags`), which is
the natural write for a multi-select. There is no `revision` and none is accepted:
tags are off the customer row, so a tag change bumps nothing and two concurrent
replaces are last-wins, which is what replacing a set means. The handler reads the
customer's current set and diffs it against the request **before** opening a
transaction or resolving a timeline actor — a multi-select whose caller changed
their mind sends the set the customer already has, and that is a read, not a
write, so a request that changes nothing answers right there, with no actor
lookup and no directory call made for it. An unknown id is a field error on
`tagIds`. Deleting a tag removes it from every customer through the join table's
own cascade, and `GET /customers/tags` answers a `customerCount` per tag
(`CustomerTagSummary`) so the delete confirmation can say how many that is.
Renaming a tag records nothing on the customers carrying it — the tag is the
vocabulary, not the customer — while a set replace records `customer.tags_changed`
with what was added and what was removed, and only when the set moved.

Search does not match owner names or tag names: search stays what it is, the
customer's own fields. Everything here is readable with `customers:view` and
writable with `customers:update` — an owner is not sensitive data and tags are
classification, so a narrower key would be one more thing to configure for no
protection gained.

## The timeline

Every customer has a timeline: **generated** entries the module itself writes when
something happens, and **manual** entries a user writes by hand.

Generated event types: `customer.created`, `customer.updated`, `customer.type_changed`,
`customer.status_changed`, `customer.contact_attached`,
`customer.contact_relationship_updated`, `customer.contact_detached`,
`customer.contact_removed`, `customer.contact_info_updated`,
`customer.billing_profile_updated`, `customer.address_added`,
`customer.address_updated`, `customer.address_removed`, `customer.peppol_lookup`,
`customer.owner_changed`, `customer.tags_changed`.
These are immutable — there is no edit or delete endpoint for a generated entry.

- `customer.peppol_lookup` (see [Peppol lookup](#peppol-lookup)) is recorded only
  when a check's status or either capability changed from the stored answer —
  re-checking to the same answer is quiet — and its payload never carries the
  participant id.
- `customer.contact_info_updated` and `customer.billing_profile_updated` each carry
  a `before`/`after` snapshot of every field the sub-resource owns, plus a `changes`
  map of only the fields that actually moved — the same shape `customer.updated`
  already used for name/identity changes. Both are only ever recorded once the
  handler has confirmed something changed (the sub-resource's own no-op rule), so
  `changes` is never empty on either event.
- `customer.owner_changed` (`{customerId, before, after}`, each side `{userId,
  displayName}` or null) and `customer.tags_changed` (`{customerId, added,
  removed}`, each element `{tagId, name}`) are [owner and tags](#owner-and-tags)'
  own generated events. Both are recorded only when the value actually changed —
  renaming a tag records nothing, since the vocabulary changed and no customer's
  own set did — and both are already in `-customer-timeline.tsx`'s `typeKey`: a
  generated type missing from that map is unfilterable in the frontend's Event
  types filter, the one coupling between a new backend event and the UI.
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
same "Timeline revision conflict" problem. `registry.change` has existed as a manual
type since the port and stays one — a person can still write one by hand — but a
registry refresh now also produces it automatically, with `provenance: generated`
and `producer: customers.brreg` rather than through this manual-entry endpoint; see
[Registry record](#registry-record). The two are told apart by `provenance`, never
by `eventType`.

A generated `registry.change` payload **carries the registry's own values**, each
changed field's `from` and `to`, readable with `customers:timeline-view` alone — a
stated decision, not an oversight, and deliberately unlike `customer.peppol_lookup`,
which omits the participant id. The registry record is **open data** (NLOD 2.0)
about a public entity, published for anyone to read; the `customer.created` event
already carries the legal identity's own snapshot; and what the Peppol event leaves
out is a *derived* capability signal about a customer's invoicing setup, not a
public fact about a company.

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
`status`, `type`, `includeArchived`, `search`, `ownerId`, `tagId`, and `sortBy`
(`id`, `name`, `customerNumber`, `createdAt`, `updatedAt`, always tie-broken by
`id`) with `sortDirection` (`asc`/`desc`).

- Naming a `status` shows exactly that status — `status=archived` shows archived
  customers with no need for `includeArchived`. Leaving `status` off keeps the old
  default: archived hidden unless `includeArchived=true`.
- [`ownerId`](#the-owner) (a user id, `me` or `none`) and [`tagId`](#tags) (a tag
  id) narrow the list to one owner or one tag — anything else is a 400 worded
  `'ownerId' must be a user id, 'me' or 'none', but was '…'.` or `'tagId' must be
  a tag id, but was '…'.`. Both are applied to the count and the page from one
  `WHERE` clause kept by hand in step with each other, the same fragment `GetCustomers`
  and `CountCustomers` each carry (`queries/customers.sql`), so a filtered page
  and its total never disagree.
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
fetched or stored. The lookup itself is **one-shot**: there is no refresh, no stored provenance beyond
`legal_source: "brreg"`, and editing a customer's name afterwards does not re-touch
the identity. A retried, bounded HTTP call (3 retries, exponential backoff with
jitter, ~4s per attempt) answers **502 "Lookup service unavailable"** on any
exhausted or unretryable failure, a non-2xx upstream status, or a 2xx response whose
body will not decode. What happens to a picked company afterwards — the full record,
its provenance, and a refresh — is [Registry record](#registry-record), below.

## Registry record

Phase 3 delivery A ("Brreg in full",
[design](superpowers/specs/2026-09-22-customers-brreg-full-design.md)): the fuller
Enhetsregisteret record for a Norwegian business customer, kept beside the customer,
re-read on a click, its differences written to the timeline and the notable ones
surfaced on the dashboard. Delivery A does not touch the address book or the billing
profile — delivery B keeps it current on a schedule, see
[Registry workers](#registry-workers).

### What is kept

`customers.customer_registry_records` (migration `00021`), one row per customer,
`customer_id` the primary key, `ON DELETE CASCADE`: `organisation_number`, `name`
(both `NOT NULL`), `organisation_form_code`/`organisation_form`,
`industry_code`/`industry` (the registry's `naeringskode1` — only the first of up to
three codes an entity can carry is kept), `employees` (`NULL` when the registry has
not registered a headcount at all — distinct from a registered headcount of zero),
`vat_registered`/`bankrupt`/`under_liquidation`/`under_forced_liquidation` (all
`NOT NULL` booleans), `deleted_on`/`founded_on` (dates), `bankrupt_on`/
`liquidation_on` (dates, migration `00022`: the registry's `konkursdato` and
`underAvviklingDato`, nullable because it sends the flags with no date often
enough — stored and read back, but never diffed and never on the API record; they
exist so an attention item is dated the day the thing happened, below),
`website`/`email`/`phone`/`mobile`, `parent_organisation_number`,
`business_address`/`postal_address` (each
`jsonb`, shaped `{lines[], postalCode, city, municipality, countryCode}`, or SQL
`NULL` — never a JSON `null` — when the registry has none), and `fetched_at`. A
`registry_updated_hint` column already exists on the table but is untouched by
anything in delivery A: it is delivery B's, filled from Brreg's update feed and read
by nothing here.

**Why a table of its own, not columns on the customer.** The customer row is what
the user owns — its name, its status, the legal identity they asserted. The registry
row is a fact about the world with a timestamp, kept verbatim and never edited by
hand. Writing it must never bump `customers.revision` and conflict somebody's open
form, the same reasoning `customer_peppol_lookups` already follows. Where the
registry disagrees with the legal identity's own name, the difference is *reported*
(a `registry.change` timeline event, below) rather than written over it — the legal
identity keeps the name it was given at the time of the pick until a person accepts
the registry's.

### When it is fetched

Three triggers, all going through the same fetch-and-store
(`(*server).fetchAndStoreRegistryRecord` / `refreshRegistryRecord`,
`apps/server/internal/customers/registry.go`):

- **On create with a Brreg pick** — `identity.source = "brreg"`, country `no`,
  customer type `business` — the record is fetched *after* the create's own
  transaction has committed, and its error is logged and dropped, never returned to
  the caller: the customer already exists, and a user who just picked a company must
  not be told the create failed because the registry blinked. The Registry card
  shows "not fetched yet" with a Refresh action for that case.
- **On `PUT .../legal-identity` with `source: "brreg"`** — pointing a customer at a
  different entity makes the record on file the wrong company's, so the new one is
  read the same way, after commit, failure logged and dropped. A resubmit of the
  identity already stored is a no-op and never reaches the fetch (the same no-op
  rule that writes no row and no event for that PUT).
- **`POST /customers/{id}/registry-refresh`** — a person's click, or the
  `customers-registry-feed` worker calling `refreshRegistryRecord` directly
  ([Registry workers](#registry-workers), which is not a second HTTP hop). Unlike
  the two hooks above, a refresh reports every failure:
  a 502 on an unreachable registry, a 409 `no_registry_identity` when the customer
  has no Norwegian organisation number to look up — **any source**, so a manually
  typed, valid organisation number can be enriched here even though it was never
  fetched automatically.

Both hooks are bounded by **one attempt's worth of patience** (4 s,
`registryHookTimeout`), not by the whole `BRREG_TIMEOUT` the refresh endpoint
spends: the record is a bonus on a request that has already committed, so a
registry that has stopped answering must cost a create a moment rather than a
perceptible pause before its 201, and the Refresh button is the retry. They also
run on a context detached from the request's (`context.WithoutCancel`), so a
client that hangs up after the write cannot cancel the fetch that follows it.

**`PUT /customers/{id}` is the third write hook**, not just the two above: its body
carries an identity too (it is the edit modal's own Brreg picker path), so a pick
made there fetches the new company's record under exactly the same conditions.

### The four registry outcomes

The registry itself does not answer "found or not" — `brreg_entity.go` gives
`(*brregClient).entity` four outcomes, and a refresh answers with each of them.
("Nothing to look up at all" is not one of them: that 409 is decided from the
customer's own identity, before any fetch is made.)

| Outcome | Registry's answer | Stored | Reported |
| --- | --- | --- | --- |
| **found** | An ordinary live entity | Upserted in full | Diffed against the record on file (or the legal name, first fetch) |
| **deleted** | HTTP **200** with a reduced body, `respons_klasse: "SlettetEnhet"` — a live one never carries that field, so it is what tells the two apart, not the status code | Upserted: only `name`, `organisation_number` and `deleted_on` come from the response, everything else (industry, headcount, addresses…) is carried over from the record already on file — a struck-off company keeps the last picture anyone had of it rather than reporting every other field as "changed to nothing" in the same breath | Diffed the same way; `status: "deleted"` |
| **removed** | HTTP **410 Gone** — struck from *open data* entirely, not just the register | The stored row is **deleted** (`DeleteCustomerRegistryRecord`) — Brreg's own terms, a copy has no business outliving the thing it copied | One event, `{field: "removedFromOpenData", to: <date>}`, but **only when a row existed to remove** — a first refresh that lands straight on 410, or a second click after the row is already gone, changes nothing and writes nothing, so a disappearance is never reported twice |
| **unknown** | Plain **404**, empty body (also what a sub-entity's own organisation number answers) | Nothing | Nothing — `status: "unknown"`; the identity is what needs a person's look, not the record |

A registry answer is never treated as a failure. What is, and becomes the refresh's
**502** (all wrapped as `errBrregUnavailable`, worded as the Brreg lookup's own 502
above): every non-2xx status that is not a 404 or a 410 — a 429, a 406, a 403, a
5xx — an unexpected content type, a body past the 1 MiB cap, a body that will not
decode (including a malformed date), a body answering for a different organisation
number than the one asked for, and a transport error or timeout with every retry
exhausted. A 429 is deliberately in that list rather than retried: Brreg's rate
limit is not something one more immediate attempt fixes, which is also why the
refresh is throttled (below). Nothing is stored for any of them, and the record on
file, however old, is left standing.

Two of those are **terminal, not retried** — a body past the cap and a body that
could not be read to the end — for the same reason a 404 and a 410 are not: the
next three attempts would fetch the same unusable answer and spend the budget a
genuine outage needs.

A **deleted** entity whose body carries no `slettedato` at all is stored with
`deleted_on` set to the fetch date: the `respons_klasse` is the load-bearing fact,
and a deletion stored with no date would be reported as no change and raise no
attention item — the one outcome that must not happen. The date is the best anyone
here knows.

**Stored but never diffed:** `organisation_number`, `organisation_form_code`,
`industry` (the description beside the code, which is diffed), `founded_on`,
`bankrupt_on` and `liquidation_on`. Municipality changes are stored and displayed
(it is part of each address's `jsonb`) but deliberately left out of the one-line
address rendering, so they are never reported either. And a **404** leaves an
existing record exactly as it was: the identity is what needs a look, not the
record.

### The diff and the `registry.change` event

After a **found** or **deleted** fetch, the new record is compared with the one on
file (`diffRegistryRecords`, `registry_diff.go`). Compared, field by field: name,
organisation form, industry code, employees, VAT registration, the three status
flags (bankrupt, under liquidation, under forced liquidation), deletion date,
website, email, phone, mobile, parent organisation number, and both addresses — each
address as **one rendered line** ("Forusbeen 50, 4035 STAVANGER, NO": street lines,
then post code and city, then country), not field by field. **Municipality is
deliberately left out of that line** — it repeats the city on almost every Norwegian
address and is absent on every foreign one, so including it would read "…,
STAVANGER, STAVANGER, NO" on nearly every business address in the country.
`fetched_at` moving is never a change.

**The very first fetch for a customer** does not diff sixteen fields against zero
values — there is nothing to have changed *from*. It compares exactly two things,
in this order: the registry's name against the legal identity's own name (silent
when they agree, which they do for an ordinary Brreg pick — so a create's own
after-commit fetch normally writes no event at all), and the registry's deletion
date, if it has one (picking a company already struck from the register is exactly
what the person doing the picking needs told).

Whatever differed is written as **one** `registry.change` timeline event
(`recordRegistryChange`, `timeline_events.go`): `provenance: generated`,
`producer: customers.brreg` (the module's second producer beside `customers.api` —
a registry event describes the world changing, not this module's own API being
called), actor the user who clicked Refresh, or `system` when the
`customers-registry-feed` worker does the refreshing instead ([Registry
workers](#registry-workers)). The payload is `{changes: [{field, from, to}, …]}` — the
same shape the refresh's own HTTP response carries, so a card reading "3 changes"
and the timeline entry behind it never disagree; either side of `from`/`to` is
omitted when that side was empty. `registry.change` **stays a type a person can
also write by hand** through the ordinary manual-timeline endpoint (it has been in
`manualTimelineEventTypes` since the port); the two are told apart by `provenance`,
not by `eventType` — `?provenance=generated&eventType=registry.change` on the
timeline read finds only the automatic ones. A removal's event is a fixed-literal
summary ("Registry record removed from open data"), since there is only ever the
one change and reusing the field-name summary would read like an ordinary edit
rather than a disappearance.

### Refreshes of one customer queue

A refresh's network call happens first, outside any transaction (no out-of-process
call under a lock). Storing the answer then locks the **customer** row
(`LockCustomer`, `FOR NO KEY UPDATE` — the same lock every address write takes), not
just the registry-record row: on a first fetch there is no record row yet to lock,
so two refreshes racing each other would otherwise both see "nothing on file," both
diff against the legal name, and both write their own event. Locking the customer
instead makes a second refresh of the same customer wait for the first to finish, so
it diffs against what the first one actually stored.

### Attention items

`GET /stats/attention` — a stub since the port, always an empty array — now answers
for real (`attentionItemsFrom`, `stats.go`), computed from every non-archived
customer's stored registry record against its current row, **never from events**:
the list is idempotent by construction and needs no "dismiss" state, because it
clears itself the moment the underlying fact does.

Four types, **at most one per customer**, in this fixed precedence (a struck-off
company's single most useful sentence is that it is deleted, whatever else is also
true of it):

| Type | Raised when | Clears when |
| --- | --- | --- |
| `registryDeleted` | The stored record carries a deletion date, regardless of the other three flags | The customer is archived, a later refresh answers 410 and deletes the row, or the identity is changed away from that company |
| `registryBankrupt` | `bankrupt` is set (and the record carries no deletion date) | The customer is archived, a later refresh stores the flag as false, or the identity is changed away from that company |
| `registryLiquidation` | `underLiquidation` or `underForcedLiquidation` is set (and neither of the above applies) | The customer is archived, a later refresh stores both flags as false, or the identity is changed away from that company |
| `registryRenamed` | The record's `name` differs (trimmed, case-sensitive) from the legal identity's own name (and none of the above applies; a customer with no legal name at all — no identity, or one whose name was never set — never raises this one) | The legal identity's name is updated to match the registry's (the card's "Update legal name"), the customer is archived, or the identity is changed away from that company |

Each item's `id` is `"<type>/<customerId>"`, `title` is the **customer's own name**
(never the registry's), and `entityId` is the customer id — the host already links a
`customers` attention item to `/customers/{entityId}`, so no new wiring was needed
there. `occurredAt` is **the day the thing happened**, not the day we noticed
(projects' own rule): `deleted_on` for `registryDeleted`, `bankrupt_on` for
`registryBankrupt`, `liquidation_on` for `registryLiquidation`, each falling back to
`fetched_at` when the registry sent no date — and `fetched_at` for `registryRenamed`,
which has no date at all, since the registry says what an entity is called but never
since when. Dating everything by `fetched_at`, as this first did, re-floated a 2019
bankruptcy to the top of the list on every refresh. Items are ordered newest
`occurredAt` first, ties broken by `id`.

The query behind the list pre-filters in SQL — only rows with a deletion date, a
set flag or a name that differs from the legal name reach Go, and only rows whose
`organisation_number` still equals the customer's `legal_id` — but the precedence
between the four types stays Go's, and stays table-tested. The SQL is a pre-filter,
not the rule. The endpoint stays gated on plain
`customers:view` — an item never carries the organisation number or anything else
that `customers:legal-identity-view` would otherwise be needed to see, only the
customer's own name and the fact that it needs a look.

The dashboard's own four sentences (`apps/host/frontend/src/catalogs/dashboard.ts`):
"{{name}} is registered as bankrupt", "{{name}} is under liquidation", "{{name}} has
been deleted from the register", "{{name}} has changed its name in the register" —
in both English and Norwegian.

The record is deliberately **not** used to validate the billing profile (no
"registry says VAT-registered but billing profile doesn't reflect it" warning, say)
— that is Invoices' call, later.

### `GET /customers/{id}/registry-record`

Gated on plain `customers:view`, but **the whole record is withheld** — 204, no
body — for a caller without `customers:legal-identity-view`, exactly the way the
legal-identity endpoint's own "nothing to show" is: the record repeats the identity's
organisation number, so it is that permission's to show. 404 when the customer does
not exist; otherwise 204 for "never fetched" and 200 with the record — the **two
204s are deliberately indistinguishable**, since a caller who may not see the record
has no business learning whether one exists at all.

### `POST /customers/{id}/registry-refresh`

`customers:legal-identity-manage` + `customers:legal-identity-view` — the same pair
`PUT .../legal-identity` requires, because this operation hands back the whole
record and every `from`/`to`, which is exactly what the GET withholds without
`legal-identity-view`. Check order: 404 the customer → 409 `no_registry_identity`
when there is no Norwegian organisation number to look up → **the 60-second
throttle** → resolve the timeline actor (an out-of-process directory lookup, done
before the network call, and only once a write can happen) → the fetch, outside any
transaction → 502 on an unreachable registry → the transaction that stores the
answer and records what differed.

**One outbound call per customer per minute.** A click within
`registryRefreshMinInterval` (60 s) of the record's own `fetchedAt` answers the
stored record as it stands — `changes: []`, `status` the row's own `found` or
`deleted` — and makes no request at all: this endpoint is one GET per click on an
open API whose 429 is not retryable, and a registry record does not change twice a
minute. A removal deletes the row, so the click after a 410 always asks again; so
does a click on a record whose organisation number is no longer the customer's. 200 always
carries `status` (`found`/`deleted`/`removed`/`unknown`), `changes` (possibly empty),
and `record` when there is one to return (found and deleted only — a removal has
nothing left to show, an unknown organisation number was never anyone's record).

### The frontend

The **Registry** card (`-customer-registry-card.tsx`), on the Overview tab, shown
only when the caller has `customers:legal-identity-view` **and** the customer is a
business whose legal identity is a Norwegian business one (type `business`, country
`no`) — a customer with no such identity has nothing to look up: the record's
fields
(`-customer-registry-fields.tsx` — organisation form, industry code and
description together, employee count, VAT registration always shown (its "no" is a
fact in itself, unlike every other field, which is left out entirely rather than
shown as "not set" when the registry has nothing for it), founded-on, website/
email/phone/mobile as live links, the parent's organisation number as bare text (it
may belong to no customer here, and a dead link is worse than none), both
addresses), a "From Brønnøysundregistrene, fetched {date, time}" line, and status
badges in red — Bankrupt, Under liquidation, Under forced liquidation, Deleted (with
its date) — plus a same-session "Removed" badge after a 410 refresh. **Refresh**
(gated on `canManageIdentity`, i.e. `customers:legal-identity-manage`) shows the last
click's own result inline once — "N changes — see the timeline" (dismissible) — and,
for the other three outcomes, a note: unknown organisation number, no registry
identity to look up (the 409), or the registry unreachable (the 502); a `removed`
result adds the "Removed" badge above instead of a note. A found registry name that
disagrees with the legal identity's own opens
a yellow notice with **"Update legal name"** (`canManageIdentity` again): an ordinary
`PUT .../legal-identity` with the registry's name and everything else — country,
type, id, source — unchanged.

**The address offers.** The Contact & addresses card's address list
(`-customer-address-list.tsx`) reads the same registry record and shows
**"Use the registry's business address"** / **"Use the registry's postal address"**
as a subtle inline action — never an address written on its own. Each offer stands
only while the customer has **no address of that type** (once it has one, the
registry's is either already on file or a deliberate difference, and neither is an
invitation to add a second) and only for a caller who may edit addresses; both are
withheld while the address list is still loading or has failed, since an empty list
then says nothing about what the customer has. A click
prefills the ordinary add-address modal (`registryAddressValues`,
`lib/registry-address.ts`: the registry's free-form line array becomes the form's
two lines, first line to line 1, the rest joined into line 2) at type `visiting`
(business address) or `postal` (postal address); a second click through the
ordinary address endpoint is what actually saves it, applying delivery A's own
first-address-of-a-type-is-primary rule like any other add. **They stay offers, on
purpose**: an address on file may deliberately differ from the registry's (an
invoice address agreed with the customer over the phone, say), and a refresh must
never silently move where mail goes — only a person's own click does that, and only
for the one address they chose.

### Registry workers

Phase 3 delivery B ("Registry workers",
[design](superpowers/specs/2026-09-22-customers-registry-workers-design.md)): the
same fetch-and-store above, on a schedule instead of a click. Nothing new is shown
to a user beyond one line on the Registry card; what changes is that the record, the
timeline, the attention list and the billing warnings stop going stale.

Two workers, registered through the module's `Workers` field, so they run wherever
this deployment already runs workers: in `api` mode with `WORKERS_IN_PROCESS=1`, and
in `worker` mode — **never** in `server` mode. Each takes its own session-scoped
`pg_try_advisory_lock` (`"CUSTREG1"` and `"CUSTPEP1"` read as 64-bit values), so
several replicas are safe: one runs the cycle, the others log at debug level and skip
it.

**The lease keys are per *database*, not per tenant.** Postgres advisory locks share one
key space per database, and these two keys are fixed constants — so two tenants whose
deployments point at the *same* database would serialise their feed workers against each
other silently: whichever cycle starts first runs, the other logs "the lease is held by
another replica" at debug and skips, and a tenant could go a long time without a cycle.
The deployment model this repo documents gives each tenant its own database, where the
keys never meet. What that means for load on Brreg is the mirror image: N tenants are N
independent pollers, so the backfill alone is about N × 100 entity reads an hour while
they are catching up.

**`customers-registry-feed`** (`CUSTOMERS_REGISTRY_FEED_POLL`, default 15 minutes)
reads `GET /enhetsregisteret/api/oppdateringer/enheter` — Brreg's incremental update
feed, which says *which* entities changed, never what — from a stored cursor, and
re-reads each matched entity whole through the Refresh path above.

One cycle, in order:

1. **The sweep.** Up to **50** records whose `registry_updated_hint` is newer than
   their `fetched_at` (oldest hint first), then up to **25** non-archived Norwegian
   business customers with a valid organisation number and **no record at all**
   (lowest id first). The first half is the retry mechanism; the second is the
   backfill — customers created before delivery A, and picks whose fetch failed, get
   their record without anyone clicking, about a hundred an hour at the default poll,
   so a few thousand customers are caught up within a day or two. A backfilled record goes through the
   ordinary first-fetch diff, so a hand-typed name that differs from the registry's
   raises `registryRenamed` exactly as a click would. The backfill walks round-robin:
   it keeps its own position on the cursor row (`backfill_after_id`) and takes the
   next 25 customers after it, resetting to the front (0) whenever a batch comes back
   short or empty, and it only considers organisation numbers that are nine digits. A
   cycle cancelled part-way through a batch — a shutdown, say — writes no position at
   all: the customers it never reached are the next *cycle's*, not the next pass's.
   Both are there because a customer whose number the register does not know never
   gets a record — without a position, those 25 rows would be the same 25 rows on
   every cycle and the 26th customer would never be read at all. A sweep refresh that
   fails is logged and left for the next cycle, and a sweep never advances the *feed*
   cursor (`next_update_id`) — it keeps only its own backfill position on the same
   cursor row. The stale half has **no attempt counter**, unlike the backfill's
   position: `hint > fetched_at` is the whole ledger, so a record whose refresh the
   register can never satisfy — a number the feed reported and the entity endpoint
   answers 404 for, which stores nothing and so never moves `fetched_at` — keeps its
   place in every batch, at the head of it (oldest hint first). That is rare and it is
   by design: the alternative is a cycle quietly giving up on a change nobody ever
   picked up. It costs one request per cycle, and the record's own attention item is
   where a person sees that the identity is the problem.
2. **The feed**, in pages of 1000, at most **20** pages per cycle (so a week's
   backlog — about 21 000 entries — clears in two cycles), each response capped at
   4 MiB. With no stored cursor the first request is `?dato=<started_at>`: the feed
   is joined at the moment the worker first ran, never at the beginning of time, and
   what came before is the sweep's business. Afterwards it is
   `?oppdateringsid=<next_update_id>`.
3. **Per page**: the page's organisation numbers are matched against this
   installation's non-archived Norwegian business customers **locally** — the
   `organisasjonsnummer` filter is deliberately not used, because chunked filtered
   requests have no safe cursor, while the unfiltered scan's cursor is exact. Every
   `endringstype` counts (`Ny`, `Endring`, `Sletting`, `Fjernet` and the older
   `Ukjent` alike): the entity is re-read whole whatever the reason. Per matched
   customer, once even when the page names it several times: write
   `registry_updated_hint` (the newest of its entries, never moving backwards), then
   refresh. The cursor is written only after the whole page is handled, as the
   page's highest id **plus one** — `oppdateringsid` is inclusive.

A feed request that fails ends the cycle with the cursor untouched, so the next
cycle re-reads the same page. A *refresh* that fails does not: the hint records that
the register has something newer, `hint > fetched_at` is the definition of stale, and
the next cycle's sweep retries it.

**An abandoned cycle.** Five refreshes in a row answering "the registry could not be
reached" (`errBrregUnavailable` — a transport failure, a 5xx after the retries, a 429,
a body that will not parse) end the cycle on the spot, wherever it was, with one
`WARN registry unavailable, cycle abandoned`. The arithmetic is the reason: 75 sweep
refreshes each spending the full `BRREG_TIMEOUT` against a black hole is about
nineteen minutes of one cycle, under the lease, after which the 15-minute ticker fires
and the next cycle does it again. An abandoned cycle claims no ground — the backfill
position is not written (exactly as for a cancelled cycle) and a page abandoned
part-way does not advance the cursor, so it is simply read again next poll: a hint
never moves backwards and a refresh is idempotent, so re-reading costs requests and
nothing else. It is **not** a failed cycle: `RunCycle` returns no error and the Error
line below is not logged.

If Brreg ever issues an `oppdateringsid` past int32, the stored cursor holds it
(`next_update_id` is a `bigint`) but Brreg's own API rejects it as a parameter: the
feed request built from it answers 400 every cycle (the client refuses to retry a 400 —
its own request was wrong), and the cursor never moves again. The *sweep* keeps working, so records the feed already
reported are still caught up and the backfill still runs; what stops is noticing new
changes. There is no automatic recovery: an operator resets
`customers.registry_feed_cursor` by hand — clearing `next_update_id` re-joins the feed
by `started_at` (the whole history since this installation joined, replayed a page
budget at a time), and moving `started_at` forward with it joins nearer to today
instead. Every refresh is attributed to the system actor
(`System`) with `producer: customers.brreg`, and the four outcomes keep their
meaning — `Fjernet` in the feed becomes a 410 from the entity endpoint and the row is
deleted with one event; `Sletting` becomes a `SlettetEnhet` body; `unknown` stores
nothing. The 60-second click throttle (`registryRefreshMinInterval`, above) is the
HTTP handler's own and does not apply here: the worker only asks when the feed or the
sweep says there is a reason.

A refresh's HTTP response and a refresh's row on file can differ on one column only:
`registryUpdatedHint` is the feed worker's to write and the upsert never touches it,
so a click's own refresh carries the row's stored hint onto the record it returns
rather than answering with none — a successful refresh still leaves `fetchedAt` at or
past the hint, which is what actually clears the card's line.

The cursor lives on `customers.registry_feed_cursor` (migration `00023`), one row:
`next_update_id`, `started_at`, `last_update_at` (the `dato` of the last entry
processed), `last_polled_at`, and `backfill_after_id` — the sweep's own position,
described above. Nothing reads `last_polled_at` or `last_update_at`: they are there
because the only report this delivery gives an operator is a log line, and those two
columns answer "is it running" and "how far behind is it" from `psql` alone. A cycle's
outcome is one `INFO` line per page (entries seen, matched, refreshed, how many the
register did not know, how many failed, the new cursor) plus one `INFO` line for the
sweep (`registry sweep finished`: how many stale records and how many backfill
customers it **attempted**, then refreshed / unknown / failed, and the position it
started from). `refreshed` counts records actually stored: a number the register does
not know is an answer that stores nothing, and it is counted as `unknown`, never as
refreshed. The attempted counts are not the batch sizes: an abandoned cycle reached fewer (a
sweep cut short by a shutdown logs no summary at all).

**`customers-peppol-recheck`** (`CUSTOMERS_PEPPOL_RECHECK_POLL`, default 24 hours,
effective only with `PEPPOL_LOOKUP_ENABLED=1`) asks the Peppol network again, for up
to **100** customers a cycle:

- customers whose `invoice_delivery` is `ehf` and who have **no stored lookup at
  all** — the customer whose invoices are already going to Peppol is the one whose
  registration must not be assumed. These come first, so on an installation with
  more aged answers than the batch they are never the ones that do not fit, but they
  take at most **50** of the hundred: an afternoon's worth of customers switched to
  EHF must not be able to starve the aged half either;
- then non-archived customers with a stored lookup older than
  `CUSTOMERS_PEPPOL_RECHECK_AGE` (default 720h / 30 days), oldest first, **whose
  `participant_id` still equals the participant the billing profile resolves to
  today**, for whatever is left of the hundred (so at least 50). A lookup for a
  participant that changed is already stale by identity —
  [the billing profile](#billing-profile) drops it from every response and every
  warning — so it is not this worker's to refresh: **it is deleted instead**. Keeping
  it would keep it aged forever (nothing moves its `checked_at`) and hold one of the
  batch's places for good, and the customer's next lookup is a first one anyway for
  the participant it resolves to now.

The participant-equality check is Go's, not SQL's: the query behind the second set
selects by age alone, and the worker itself is what recognises a row whose participant
has moved on. A cycle therefore asks for a batch of up to 100 but can end up asking the
network about fewer.

Each is the handler's own lookup-and-store (`lookupAndStorePeppol`, shared by the
click and the worker so there is one ruling, not two): the answer is upserted, and
`customer.peppol_lookup` is recorded **only when it changed**, with the system actor.
A network failure is logged by kind and leaves `checked_at` alone, so that customer
is first in line next cycle. The cycle's own log line reports how many were checked,
how many answers changed, how many failed and how many stale-by-identity rows were
dropped. Nothing is ever switched on the billing profile: a
lapsed registration surfaces through the existing `ehf_recipient_not_registered` and
`ehf_available` warnings, and only a person changes `invoiceDelivery`.

**The card's one line.** `CustomerRegistryRecord` gains an optional
`registryUpdatedHint`, and the Registry card shows one line while it is newer than
`fetchedAt`: "The registry reported a change on {date}; this record is from {date}.",
with the existing **Refresh** in the card's header. That is the whole user-visible
surface of this delivery.

**Out of scope, on purpose:** `includeChanges` (the entity is re-read whole);
sub-entities (`underenheter`); an operator "run now" or worker-status endpoint;
rate limiting against Brreg beyond one request at a time; notifying anyone of what a
worker found (the attention list is the notification); a Peppol attention item (the
billing warning is where a lapsed registration belongs).

**Configuration**, all read once at startup by `internal/config`:

| Variable | Default | |
| --- | --- | --- |
| `CUSTOMERS_REGISTRY_FEED_ENABLED` | `1` | `0` → the feed worker is never handed to the runner, so no scheduled Brreg request is made |
| `CUSTOMERS_REGISTRY_FEED_POLL` | `15m` | how often a feed cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED` | `1` | the re-check worker; effective only with `PEPPOL_LOOKUP_ENABLED=1` |
| `CUSTOMERS_PEPPOL_RECHECK_POLL` | `24h` | how often a re-check cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_AGE` | `720h` | a stored lookup older than this is asked again |

**Turning the workers on for the first time on an installation that already has
customers is a one-off flood, and there is no way to dismiss it.** The backfill fetches
a record for every Norwegian business customer that has none, about a hundred an hour,
and each first fetch runs the ordinary first-fetch diff — so every legacy customer whose
registry state warrants one raises an attention item (a rename, a bankruptcy, a
liquidation, a struck-off company) and writes a `System` timeline entry saying what
differed. On an installation with a few thousand such customers that arrives over a day
or two, all of it at once from a user's point of view, and the dashboard's attention list
has no "dismiss": the items stand until somebody fixes the underlying legal name or
archives the customer. Plan the switch-on for a day when somebody can work through them,
or leave `CUSTOMERS_REGISTRY_FEED_ENABLED=0` until then.

`BRREG_BASE_URL` and `BRREG_TIMEOUT` are reused — a worker refresh is bounded by the
full `BRREG_TIMEOUT`, unlike the create hook's one attempt, because nobody is
waiting on it. Page size, page budget, the sweep batches, and the Peppol batch with
its EHF share are constants, not knobs: they bound one cycle's work against a public
register.

## Peppol lookup

`POST /api/v1/customers/{id}/peppol-lookup` (`customers:billing-manage` +
`customers:view` — the people who act on the answer) asks the Peppol network
itself whether this customer is a registered receiver of Peppol BIS Billing
3.0 (EHF), and remembers the answer. Decided in
[the design](superpowers/specs/2026-09-21-customers-peppol-lookup-design.md)
as delivery **B** of the invoice-ready customer (delivery A is
[above](#contact-info-addresses-and-the-billing-profile)); the design's own
"What the network looks like" section is the verified detail this section
only summarises.

**What is asked, and of whom.** The participant looked up is the billing
profile's explicit `peppolId` when set, else `derivedPeppolID` — `0192:` plus
the legal id, when the identity's country is `no`, the customer's own `type`
is `business`, and the id passes the Norwegian organisation-number check —
else there is nothing to look up: **200** with `status: "no_identifier"` and
nothing stored, no network call made.

**How the network is asked.** Discovery is NAPTR-only: the participant
identifier's value is lower-cased, SHA-256 hashed, base32-encoded (trailing
`=` stripped) and turned into a DNS name under the configured SML zone
(`<hash>.iso6523-actorid-upis.<zone>`). There are two outcomes at the DNS
step, and neither is ever collapsed into the other: **NXDOMAIN, or NOERROR with no NAPTR records, is the
definitive negative** — the SML publishes a name only for a registered
participant, so a name with no NAPTR records has no SMP behind it either,
and the two are never distinguished from each other; **SERVFAIL, REFUSED or
a timeout** is a technical failure, retryable, never reported as "not
registered". (A resolver that answers NODATA instead of forwarding the
authoritative NXDOMAIN would turn a registered participant into a silent
false negative — see "Configuration" below.)

A NAPTR record with flags `U`, service `Meta:SMP` and a regexp carrying the
SMP's base URL names the participant's Service Metadata Publisher. When the
name exists but none of its records carry `U` + `Meta:SMP`, the participant
is registered with no SMP service — able to receive nothing, and no SMP
request is made. Otherwise, one unauthenticated `GET` of that SMP's
`ServiceGroup` lists the document types the participant has registered,
compared as the **full identifier string, exactly** — never a prefix,
substring or the PINT wildcard — against the Peppol BIS Billing 3.0 invoice
and credit-note ids (`internal/peppol`, package doc comment and `smp.go` for
the two live edge cases — a reminder-only receiver and a participant with
order/response profiles only — that make anything looser wrong).

**The outbound guard.** The SMP base URL comes out of a DNS record a third
party published, so it is validated in full before any request is made: it
must parse, be `https`, name a host, carry no userinfo, use port 443 or
none, and carry neither a query (a bare `?` counts, even though it carries
nothing) nor a fragment. It is then fetched through `internal/netguard` —
the shared table of addresses the guarded outbound clients refuse to dial
(today `internal/mail`'s SMTP guard and this SMP client) — refusing
private, loopback, link-local (the `169.254.169.254` cloud metadata address
included), carrier-grade-NAT, unique-local, multicast, `0.0.0.0/8`,
`192.0.0.0/24`, `240.0.0.0/4`, `fec0::/10` (deprecated IPv6 site-local),
Teredo (`2001::/32`) and the local-use NAT64 range (`64:ff9b:1::/48`)
outright, plus the IPv6 forms that plainly embed one of those addresses at a
fixed offset (the well-known NAT64 prefix `64:ff9b::/96`, 6to4, the
deprecated IPv4-compatible `::/96`), classified by that embedded address.
The connection dials the very address that was checked, which is what
defeats DNS rebinding — trying every resolved address in order if the first
does not connect, since the guard already passed all of them; no proxy is
used, no redirect is ever followed, and the SMP's response body is capped at
1 MiB.

**Where the answer lives.** The result is stored in its own table,
`customers.customer_peppol_lookups` — one row per customer (participant id,
status, the two capabilities, SMP host, checked-at) — deliberately **not** a
column on `customers.customers`: recording an answer must never bump
`revision` and conflict somebody's open form. `GET`/`PUT
.../billing-profile` surface it as an optional `peppolLookup` object, but it
is **stale-dropped**: whenever the stored row's participant id no longer
equals the one that would be looked up now (the org number or an explicit
`peppolId` changed since), it is treated exactly as if no lookup had ever
been made — in the response and in the two warnings below alike.

**Withholding.** `participantId` is omitted from both the `POST` response
and the resolved `peppolLookup` whenever it was *derived* from the legal
identity and the caller lacks `customers:legal-identity-view` — the
organisation number is that permission's to show. An explicit `peppolId` is
already visible in the profile, so it is never withheld.

**Warnings.** Two new codes on `GET .../billing-profile`'s computed
`warnings`, read from the stored answer and never from a fresh network call:
`ehf_recipient_not_registered` (delivery is `ehf` and the last lookup says
`not_registered`, or `registered` without `canReceiveInvoice`) and
`ehf_available` — an offer, not a problem — (the last lookup says
`registered` with `canReceiveInvoice` and delivery is anything but `ehf`).
See [Billing profile](#billing-profile) for the full, ordered table.

**The timeline event.** A lookup records `customer.peppol_lookup` only when
the *status or either capability changed* from the stored answer, so
re-checking the same answer is quiet. Its payload deliberately omits the
participant id: `customers:timeline-view` does not imply
`customers:legal-identity-view`, and a derived id is that permission's to
show.

**Who asks.** A click is no longer the only thing that asks: the
`customers-peppol-recheck` worker re-asks for an aged answer and for a customer
already set to `ehf` that has never been checked ([Registry workers](#registry-workers)).
It records the same event under the same rule — only when the answer changed — with
the system actor, and it changes nothing on the billing profile.

**502 vs 503.** An upstream failure — the DNS query or the SMP request
itself could not complete — answers **502**, the same shape Brreg's own
lookup uses; nothing is stored, and the last good answer (if any) stands.
The feature switched off (`PEPPOL_LOOKUP_ENABLED=0`) answers **503**
instead, before any network call would have been attempted.

**Configuration**, all read once at startup by `internal/config`:

| Variable | Default | |
| --- | --- | --- |
| `PEPPOL_LOOKUP_ENABLED` | `1` | `0` → the operation answers 503 and the UI hides the action after the first 503, until the page is reloaded |
| `PEPPOL_SML_ZONE` | `participant.sml.prod.tech.peppol.org` | the test network is `participant.sml.test.tech.peppol.org` |
| `PEPPOL_DNS_SERVER` | *(empty → the server's own name servers, `/etc/resolv.conf`)* | `host:port` of a resolver to use instead |
| `PEPPOL_TIMEOUT` | `10s` | one lookup end to end (DNS and the SMP request together) |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED` / `_POLL` / `_AGE` | `1` / `24h` / `720h` | the background re-check worker, which asks the network again on a schedule — see [Registry workers](#registry-workers) |

A resolver that answers NODATA instead of NXDOMAIN for a name nobody
registered would produce silent false negatives — see "How the network is
asked" above. Point `PEPPOL_DNS_SERVER` at a plain recursive resolver.

**The frontend.** The Billing card's Peppol row gets a **Check EHF** action
(gated on `canManageBilling`, i.e. `customers:billing-manage`) that asks the
network again; the last answer is shown in words with its date and time ("Can receive
EHF invoices — checked 21 Sep 2026, 14:05", plus a dimmed "Looked up 0192:…"
line naming the identifier the answer is about when the caller may see it, "Not registered in Peppol", "Registered
in Peppol, but not for invoices"). The `ehf_available` warning renders as an
offer with its own **Use EHF** button — an ordinary billing-profile `PUT`
with `invoiceDelivery: "ehf"` and the current `revision` — never a silent
switch: unlike Tripletex and Fiken, nothing here changes `invoiceDelivery`
without a click, whether from this card or from the registry workers'
schedule. No lookup from this card ever runs automatically (not on create,
not on a Brreg pick — every lookup this button makes is a person's click, so
a network failure never blocks a save); the `customers-peppol-recheck` worker
asks independently, on its own schedule, and never on the strength of this
card being open (see [Registry workers](#registry-workers)). A 502 shows "The
Peppol network could not be reached. Try again."; a 503 makes the action
disappear, with a one-line note, until the page is reloaded, so the app does
not keep asking an installation that has the feature switched off.

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

No new permission key was added for [Registry record](#registry-record): reading
`GET .../registry-record` is withheld to 204 without `customers:legal-identity-view`
(the same rule the legal-identity GET's own "nothing to show" follows), and
`POST .../registry-refresh` needs `customers:legal-identity-manage` alongside
`customers:legal-identity-view` — the pair `PUT .../legal-identity` already
requires, since a refresh hands back the whole record and every `from`/`to`, which
is exactly what that view permission gates. The dashboard's
`GET /stats/attention` stays on plain `customers:view`: an item never carries the
organisation number, only the customer's own name.

No new permission key was added for [Owner and tags](#owner-and-tags) either:
an owner is not sensitive data — it is a name, not a legal identity or a billing
term — and tags are classification, so both ride on the same `customers:view`/
`customers:update` split every other non-sensitive field of the customer row
already uses, rather than a key of their own that every installation would have
to remember to grant.

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
  route, plus the KPI row and a spotlight-openable create form. [Owner and
  tags](#owner-and-tags) added an **Owner** column, the tag chips beside each row's
  name, and an **Owner** filter (mine/unassigned/all) and a **Tag** filter, both
  reflected in the URL (`ownerId`, `tagId`) the same way the others are — the Owner
  filter needs nothing new from the host, because `me` is resolved server-side from
  the session, never from anything the page sends. The Tag filter carries a **Manage
  tags** button, opening [the vocabulary editor](#tags), for a caller the host says
  may edit: the host passes one new prop, `canEdit`, read from `customers:update`,
  through its own `-customers-list.tsx` wrapper — this package still never fetches
  permissions itself.
- **Detail** (`/customers/:id`) — a host-composed page: this package owns the header
  (name, legal-identity badges, status/type badges, edit and change-type actions) and
  an overview tab (relationship card, contact & addresses card, billing card, contacts
  card, timeline); other modules add their own tabs (Energy, Projects) the same way
  the host composes any module's tabs onto a customer.
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
- **Relationship card** (owner and tags design D3, `-customer-relationship-card.tsx`)
  — where the owner and the tags actually live on the customer page: the owner's
  name (with an "inactive" badge when the directory says so) and an `OwnerPicker`
  (a searchable `Select` over `GET /assignable-users`, copied from projects' own
  assignee picker) below it, then the tag chips and a `MultiSelect` over the tag
  vocabulary that also offers *Create "x"* for a name no tag yet has — typing a new
  tag posts it and replaces the customer's set including it in two calls, since a
  set replace can only name ids that already exist. Both pickers sit behind the
  same `canEdit` prop the rest of the page uses. The two writes reload differently
  on purpose: the owner PUT answers the whole customer and carries a `revision`, so
  `syncCustomerRevision` runs before its invalidation and a 409 raises the same
  conflict-and-Reload alert the other row-editing modals use; the tags PUT carries
  no revision at all, so a save is a plain invalidation of `["customers"]` with
  nothing to sync and no conflict to handle.
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
  failing. Inside the Peppol ID row, `CustomerPeppolStatus`
  (`-customer-peppol-status.tsx`, [Peppol lookup](#peppol-lookup)) adds a
  **Check EHF** action gated the same way and the last answer in words with its
  date and time; the `ehf_available` offer is its own component,
  `CustomerEhfOffer`, rendered at the top of the card beside the warnings, with
  a **Use EHF** button that goes
  through the same revision-guarded `PUT` and Reload pattern as the edit
  modal, never a silent switch. A 503 (the feature disabled) hides the
  action until the page is reloaded, rather than asking again on every
  mount.
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
session cookie. 47 operations in total, each exercised by the module's own
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
| `PUT /{id}/owner` | `customers:update` + `customers:view` |
| `GET /assignable-users` | `customers:update` |
| `PUT /{id}/tags` | `customers:update` + `customers:view` |
| `GET /tags` | `customers:view` |
| `POST /tags` | `customers:update` |
| `PUT /tags/{tagId}`, `DELETE /tags/{tagId}` | `customers:update` |
| `POST /{id}/peppol-lookup` | `customers:billing-manage` + `customers:view` |
| `GET /{id}/registry-record` | `customers:view` (withheld to 204 without `customers:legal-identity-view`) |
| `POST /{id}/registry-refresh` | `customers:legal-identity-manage` + `customers:legal-identity-view` |
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

`/stats/attention` is no longer a stub: it answers the four [registry attention
items](#attention-items), computed live from the stored registry record against
each customer, not stored or cached.

## What comes next

The foundation branch closed the gaps that would otherwise be built upon:
attribution, search reach, concurrency, the duplicate guard, and validation. This
branch — the invoice-ready customer, delivery A — is what it was built to carry:
a customer now has its own contact info and typed addresses, and a billing profile
behind its own permission, and `contracts.CustomerDirectory` can answer a batch of
ids and resolve a billing profile in one call. A customer still cannot be invoiced
by this module alone, though — that is Invoices' job once it exists, reading
through the directory this branch built for it.

**Delivery B** — [the Peppol lookup](#peppol-lookup) — has since landed on top
of delivery A: SML DNS → SMP → BIS Billing 3.0 support, asked on a person's
click and remembered (Phase 3 delivery B, below, later added a schedule that
asks again on its own). It deliberately does **not** set a customer's
`invoiceDelivery` to `ehf` automatically the way every Nordic competitor
surveyed but Fortnox does — Tripletex and Fiken switch the delivery method
silently, and this module chose an offer (`ehf_available`, a **Use EHF**
button) over a silent write instead, so a customer's own billing decision
never changes without someone clicking to make it. Nothing waited for it
while it was outstanding: `peppolId` and `invoiceDelivery` were, and remain,
plain fields a person can fill in by hand, and the billing profile's
`ehf_without_recipient` warning already told them when EHF had no recipient
to send to.

**Phase 3 delivery A** — [Registry record](#registry-record) — has since landed on
top of delivery B: the fuller Enhetsregisteret record beside the customer, a
Refresh a person can click, `registry.change` producing its first automatic events,
and `/stats/attention` answering for real instead of an empty stub. Every fetch in
this delivery is still a person's action (a Brreg pick, a Brreg-sourced
legal-identity PUT, or a Refresh click) — nothing here reads the registry on a
schedule yet.

**Phase 3 delivery B** — [Registry workers](#registry-workers) — has since landed on
top of it: Brreg's incremental update feed driving the same fetch-and-store on a
cursor, filling the `registry_updated_hint` column delivery A's own migration
(`00021`) carried but left untouched, with a sweep that both retries a failed refresh
and backfills every Norwegian business customer that never had a record; and
scheduled Peppol re-checks on the same `ehf_available`/`ehf_recipient_not_registered`
warnings a manual check already raises. Registry data in this module is now
maintained rather than merely fetched once.

**Phase 4 delivery A** — [Owner and tags](#owner-and-tags) — has since landed: one
owner per customer, named through `contracts.UserDirectory` and never stored as a
foreign key, filterable as `me`/`none`/a user id; a case-insensitively unique tag
vocabulary a customer's set is replaced against; and the two generated events,
`customer.owner_changed` and `customer.tags_changed`, that record either. No
permission key was added. Still ahead in the phase: typed contact roles with a
primary contact, replacing today's free-text `role`; a follow-up date and assignee
on a timeline entry, feeding `/stats/attention` and a "my follow-ups" view; customer
groups that carry defaults; and attachments on a customer and its timeline entries,
once the storage module has a model for it.

Past that, the remaining gaps are exactly
what [ROADMAP.md's Customers section](../ROADMAP.md#customers) is built around —
`ContactsByEmail` still unused in production, no CSV import/export, no merge
(phase 6) — itself drawn from
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
