# The invoice-ready customer — design

Second delivery of the Customers roadmap (`ROADMAP.md` → Customers → phase 2; research in
`docs/superpowers/research/2026-09-21-customers-module-next.md`, "P1"). Today a customer is
a name and a legal identity: there is nowhere to send an invoice and no terms to put on
it. This delivery gives the customer its own contact details, addresses and a billing
profile, and lets other modules read them.

**Split.** This spec is delivery **A**: the data, its API, the UI and the directory.
Delivery **B** (own spec, own PR) is the Peppol capability lookup (SML → SMP) that sets
the invoice delivery method to EHF by itself; it needs an outbound network client and its
own failure model, and nothing here waits for it — `peppolId` and `invoiceDelivery` are
plain fields a person can fill in meanwhile.

## Constraints

- **Frozen corpus**: every change to an existing schema in `openapi/customers.yaml` is
  additive and optional. New operations and new schemas are free.
- No cross-schema SQL; other modules read through `contracts.CustomerDirectory` only.
- The customer row's `revision` rules from the foundation design (D5) hold for every new
  write to the row.
- The timeline actor is resolved before any transaction opens (foundation D1).
- English and Norwegian catalogs both get every string, including the host's admin
  permission labels.

## Decisions

### D1 — Sub-resources, not a wider PUT

`PUT /customers/{id}` stays what it is (name, status, identity). The new data is written
through three sub-resources, the way legal identity and type already are:

| Operation | Access |
| --- | --- |
| `PUT /customers/{id}/contact-info` | `customers:update` + `customers:view` |
| `GET/POST /customers/{id}/addresses`, `PUT/DELETE /customers/{id}/addresses/{addressId}` | read: `customers:view`; write: `customers:update` + `customers:view` |
| `GET /customers/{id}/billing-profile` | `customers:view` |
| `PUT /customers/{id}/billing-profile` | `customers:billing-manage` + `customers:view` |

Why: a full-replace PUT over optional fields cannot tell "absent" from "clear" without
nullable-wrapper types, and old recorded PUT bodies must keep meaning what they meant. A
sub-resource PUT is a full replace of one self-contained thing: every field present or null.

Reading needs only `customers:view`: an address and an invoice email are what a customer
card is for, in every system compared. **Writing the billing profile is a new, sensitive
permission `customers:billing-manage`** — payment terms and delivery channel decide when
and how money arrives, and the person who may rename a customer is not thereby the person
who may give it 90 days' credit.

### D2 — Contact info lives on the customer row

`customers.customers` gains `email varchar(255)`, `phone varchar(30)`, `website
varchar(2048)`, all nullable. `SafeCustomerResponse` gains an optional `contactInfo:
{email, phone, website}` (always present on responses from this version on; each field
nullable). `POST /customers` accepts an optional `contactInfo` so a customer can be created
complete; `PUT /customers/{id}` does not (D1).

- `email`: trimmed, ≤ 255, must contain exactly one `@` with a non-empty local part and a
  domain containing a dot; stored as typed (no lower-casing of the local part); the same
  rule is used for the billing emails. `phone`: trimmed, ≤ 30, digits, spaces and
  `+ - ( )` only, at least 5 digits. `website`: absolute `http`/`https` URL ≤ 2048 (the
  timeline's `sourceUrl` rule).
- Blank strings are stored as NULL.
- The write bumps `revision`, accepts the optional request `revision` (409 when stale), is
  a no-op when nothing changed, and records `customer.contact_info_updated` with before
  and after in the payload.
- List search also matches the customer's own `email` and, compacted, `phone` — ungated,
  since the response shows them to anyone who can list.

### D3 — Addresses are a typed list

New table `customers.customer_addresses`: `id`, `customer_id` (FK, `ON DELETE CASCADE`),
`type`, `label varchar(100)`, `line1 varchar(255) NOT NULL`, `line2 varchar(255)`,
`postal_code varchar(20)`, `city varchar(100)`, `region varchar(100)`, `country
varchar(2) NOT NULL`, `is_primary boolean NOT NULL`, `created_at`, `updated_at`.

- `type` ∈ `postal`, `invoice`, `delivery`, `visiting` (the union of what Tripletex,
  Fortnox, 24SevenOffice and Visma keep). Any number of each; `label` tells two delivery
  addresses apart.
- **One primary per (customer, type)**, enforced by a partial unique index. The first
  address of a type is primary whatever the request says; setting `isPrimary: true` on
  another demotes the old one in the same transaction; deleting the primary promotes the
  oldest remaining of that type; `isPrimary: false` on the only/primary address is refused
  (400) — there is always a primary while any address of the type exists.
- `country`: the ISO 3166-1 alpha-2 list the legal identity uses. `postal_code` and `city`
  are required when `country = "no"` and the postal code must then be four digits; other
  countries: free text.
- At most 50 addresses per customer (400 beyond that).
- Address writes do **not** bump the customer's `revision` (they do not touch the row) and
  carry no revision of their own: an address is small, and last-writer-wins on one is
  acceptable. They record `customer.address_added` / `_updated` / `_removed`.
- **The invoice address** anyone needs is resolved as: primary `invoice` → primary
  `postal` → none. The directory (D5) and the UI both use that rule.

### D4 — The billing profile

Columns on `customers.customers`, all nullable, NULL meaning "not decided here — whoever
invoices uses its own default":

| Field | Rule |
| --- | --- |
| `invoiceEmail`, `reminderEmail` | email rule of D2. Separate because reminders cannot travel as EHF or eFaktura, so the reminder channel is its own thing |
| `paymentTermsDays` | integer 0–365 |
| `currency` | three-letter ISO 4217, upper-cased (projects' rule) |
| `language` | `nb` or `en` — the languages documents can be produced in |
| `invoiceDelivery` | `email`, `ehf`, `efaktura`, `paper` |
| `reminderDelivery` | `email`, `paper` |
| `peppolId` | `<4-digit scheme>:<identifier>`, e.g. `0192:923609016`; identifier 1–50 of `[A-Za-z0-9-]` ; for scheme `0192` the identifier must be a valid Norwegian organisation number |
| `gln` | 13 digits with a valid GS1 check digit |
| `buyerReference` | ≤ 100, the default "deres referanse" |

- No cross-field rules (e.g. "EHF needs a Peppol id", "eFaktura only for persons"): a
  profile is filled in over time, and whether a customer *can* be invoiced a given way is
  Invoices' question at send time. `GET …/billing-profile` instead returns a computed
  `warnings: string[]` of machine-readable codes the UI explains: `ehf_without_recipient`
  (delivery `ehf`, no `peppolId`, and no Norwegian business legal identity to derive one
  from), `email_without_address` (delivery `email`, no `invoiceEmail` and no contact-info
  email), `efaktura_for_business`, `no_invoice_address`.
- `GET` answers 200 with every field null for a customer that has none — a profile always
  exists conceptually.
- The write bumps `revision`, accepts the optional request `revision`, no-ops when
  unchanged, and records `customer.billing_profile_updated` (before/after).
- The billing profile is not part of `SafeCustomerResponse` — list pages do not need it and
  it keeps D1's permission story simple.

### D5 — The directory other modules read

`contracts.CustomerDirectory` gains:

- `Customers(ctx, ids []int32) ([]CustomerEntry, error)` — batch; a missing id is simply
  absent; archived customers resolve, as for `Customer`. Projects' list page uses it
  instead of one call per distinct customer.
- `BillingProfile(ctx, id int32) (*CustomerBillingProfile, error)` — `(nil, nil)` when the
  customer does not exist. Carries what an invoice needs in one call: id, customer number,
  name, type, archived, legal identity (`country`, `id`, `name`) when present, the resolved
  invoice address (D3's rule) when any, `InvoiceEmail` **already resolved** (billing
  `invoiceEmail` → contact-info `email` → empty), `ReminderEmail` (billing
  `reminderEmail` → the resolved invoice email), payment terms, currency, language,
  delivery methods, Peppol id **resolved** (explicit `peppolId` → `0192:<orgnr>` for a
  Norwegian business identity → empty), GLN, buyer reference. Resolution rules live here,
  once, not in every consumer.

`ContactsByEmail` stays (unused in production, still contract surface; removing it is not
this delivery's business).

### D6 — Frontend

On the customer's Overview tab, beside Contacts and the Timeline:

- **Contact & addresses** card: email / phone / website (mailto, tel, external link) with
  an edit modal; the address list grouped by type with primary badges, add/edit/delete
  modals (delete confirms), "Make primary". Shown to everyone with the page; editing
  behind a `canEdit` capability prop from the host (`customers:update`).
- **Billing** card: the profile read-only with the warnings explained in words, an edit
  modal behind `canManageBilling` (`customers:billing-manage`). Revision conflicts use the
  foundation's Reload pattern.
- The create form gains optional email and phone.
- The list gains no columns; search placeholder mentions email.

## Out of scope

Peppol lookup (delivery B); Brreg address prefill and refresh (roadmap phase 3); customer
groups carrying billing defaults (phase 4); dunning settings, credit limit, price lists
(Invoices / later); geocoding; address validation against Bring/Posten.

## Testing

As the foundation: handler tests through `modtest` for every rule, each proven able to
fail; concurrency tests for the primary-address invariant (two simultaneous "make
primary", and delete-primary racing add) and for the billing PUT's revision guard;
directory tests for each resolution rule; permission tests for `billing-manage`;
frontend tests beside each component; the corpus test stays green.
