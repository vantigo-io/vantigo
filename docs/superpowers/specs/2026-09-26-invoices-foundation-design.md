# Invoices — the sales document — design (Invoices phase 1A)

The first delivery of a new module. Research:
`docs/superpowers/research/2026-09-26-invoices-module.md`. Vantigo issues nothing today.
Four modules keep stamps and statuses for a module that does not exist. The B2B
e-invoicing duty is law from 2027-01-01; EHF is phase 2.

This delivery is the lawful document and nothing more:

- a seller record and one gap-free number series;
- a VAT-code table whose rates are dated periods;
- an invoice that is a draft until it is **issued** into an immutable, numbered document
  with a buyer snapshot, a seller snapshot and VAT per rate;
- a PDF rendered from that snapshot, stored once, and downloadable; a watermarked preview
  of a draft;
- full and partial credit notes in the same series;
- an invoice journal with a gap check;
- the two many-provider slots every module implements;
- one change to the customers contract;
- the frontend for those pages, and the docs.

A document issued in 1A is numbered, immutable, correctable by credit note, and can be
downloaded and handed over. That is lawful on its own. Payments, e-mail delivery, the CSV
export, the stats and the customer-page tab are phase 1B, listed at the end and not
designed here. Vantigo stays a sub-ledger: no general ledger, no posting.

`srv/` below means `apps/server/internal/`. Legal sources are Lovdata
(bokføringsforskriften, https://lovdata.no/dokument/SF/forskrift/2004-12-01-1558/KAPITTEL_5-1)
and anskaffelser.dev (EHF Billing 3.0 Norway,
https://anskaffelser.dev/postaward/g3/spec/current/billing-3.0/norway/).

## Decisions

### D1 — A new module `invoices`, requiring customers, with four permissions

`srv/invoices`, schema `invoices`, contract `openapi/invoices.yaml`, UI package
`apps/invoices/frontend` (`@vantigo/invoices-ui`), app "Invoices" / "Fakturaer". It is
added by the checklist in `docs/module-boundaries.md:268-318`: `businessModules`, the
OpenAPI `Modules` list, oapi-codegen config, depguard rules in `apps/server/.golangci.yml`,
`moduleSchemas` in `srv/db/schema_test.go`, the known-module set, the frontend
registry, `moduleKeys`, the i18n import and one admin catalog entry per permission key.

**Dependencies.**
- `MODULES`: `invoices requires customers`, a check beside the other four in
  `srv/config/config.go:1266-1277`. The buyer, its billing profile and its address come
  from `contracts.CustomerDirectory`.
- The object store (scope `invoices`) is a platform capability. The module fails closed
  without it at the operation (`docs/storage.md:41-45`), never at startup.
- Products, projects, time, expenses and energy are not read in 1A. Lines are typed by
  hand. Later phases add optional slots.
- `defaultModules` is every known module (`srv/config/config.go:1234-1236`). An
  installation with `MODULES` unset therefore gets Invoices on upgrade. The release notes
  say so.

**Time zone.** A fixed `Europe/Oslo` constant: this is Norwegian bookkeeping. There is no
setting. `time/tzdata` is already embedded (`apps/server/cmd/vantigo/main.go:33`).
"Today" is always the business day of `Deps.Clock()` in Oslo, computed in Go the way
expenses computes it (`srv/expenses/entries_validation.go:624-627`). Postgres
`CURRENT_DATE` is never used; it is UTC in the container.

**Permissions** (all delegable):

| Key | Sensitive | What it allows |
| --- | --- | --- |
| `invoices:access` | no | Use the app; read every invoice, credit note, PDF and the journal. Invoicing is a finance job, not per-customer. |
| `invoices:create` | no | Create, edit and delete drafts; preview a draft as PDF. |
| `invoices:issue` | yes | Issue a draft; create a credit-note draft. |
| `invoices:manage` | yes | The seller record, the series start, VAT codes and their rates. |

Every operation needs `invoices:access`, through the grammar
`permission:invoices:access+invoices:<x>` (`srv/contracts/permission.go:139-140`).
1B adds `invoices:payments` and puts sending under `invoices:issue`.

No built-in role gets any of these keys. The three built-in roles are seeded with no
permission keys at all (migration `00002`), and Owner holds the permission wildcard for
every module (`srv/identity/bootstrap.go:37-39`). That follows the sensitive-permission
practice (`projects:view-costs`, `docs/module-boundaries.md:222-224`). The catalog labels
go in `apps/host/frontend/src/catalogs/admin.ts`, en + nb.

**Creating a draft in the UI also needs `customers:view`.** `CustomerDirectory` has no
search (`srv/contracts/directory.go:151-174`). The buyer picker therefore calls the
customers module's HTTP list, which needs `customers:view`. The API itself takes a
customer id and does not check `customers:view`. The UI hides "New invoice" without it,
and the docs say so. No directory search is added in this phase.

### D2 — The seller record and the number series

Migration `00034_invoices_baseline.sql`. No column in the schema is named with a word
PostgreSQL reserves (`to`, `from`, `end`, `user`, `order`, …); a test pins it (Testing).

**`invoices.settings`** — one row, `id = 1` (a CHECK), inserted by the migration with
empty values:

| Column | Rule |
| --- | --- |
| `legal_name` | ≤ 200 |
| `organisation_number` | 9 digits, mod-11. The rule is the customers module's validator's, copied into this module; no cross-module import. |
| `vat_registered boolean` | Prints "MVA" after the number, and decides which VAT categories may be issued (D6). |
| `in_foretaksregisteret boolean` | Prints "Foretaksregisteret" (AS, ASA, NUF; § 5-1-2). |
| `address_line1`, `address_line2` | ≤ 200 each |
| `postal_code`, `city` | ≤ 20, ≤ 100 |
| `country` | ISO-2, default `NO` |
| `bank_account` | 11 digits, mod-11 |
| `iban`, `bic` | optional; IBAN mod-97, BIC 8 or 11 characters |
| `email` | optional, printed as the seller's contact. 1B uses it as Reply-To. |
| `default_payment_terms_days` | default 14, 0–365 |
| `default_currency` | `NOK`, and only `NOK` in phase 1: 400 on `defaultCurrency` "Only NOK in this phase" otherwise |
| `footer_text` | ≤ 500, printed on every document |
| `series_start bigint` | default 1, ≥ 1; see below |
| `updated_at`, `revision` | |

`GET /invoices/settings` and `PUT /invoices/settings` (full replace with `revision`;
`invoices:access+invoices:manage`). A stale revision is a 409 naming both revisions, the
codebase's rule (`docs/expenses.md:1200-1201`).

**The seller is complete** when `legal_name`, `organisation_number`, `address_line1`,
`postal_code`, `city` and `bank_account` are set. § 5-1-2 requires the name and the
organisation number. The address and bank account are a sensible gate, not a legal
requirement. Issuing is refused with 409 `seller_incomplete` until the seller is complete.

**`invoices.counters`** (`counter_name text PRIMARY KEY`, `next_value bigint NOT NULL`).
This is the counter-row primitive customers uses (`srv/db/migrations/00003_customers_baseline.sql:114-121`),
not a `SEQUENCE`, because a sequence burns values on rollback (research §2.6).
`next_value` holds the next number nobody has taken, as in projects
(`srv/projects/queries/counters.sql:1-16`). There is one row, `documents`. The allocation
is:

```sql
INSERT INTO invoices.counters (counter_name, next_value)
VALUES ('documents', @series_start + 1)
ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS allocated;
```

`@series_start` is read from the settings row the issue transaction holds `FOR SHARE`
(D6). The counter row's lock is held to commit. A rolled-back issue therefore rolls its
number back.

**One series for invoices and credit notes**, continuous across years, starting at
`series_start`. A draft has no number. One continuous series cannot produce "several sets
of identical numbers within one financial year", which is what Skatteetaten calls a
breach (research §2.2).

**"Something is issued" means "the counter row exists".** It is never a `count(*)`.

**The start locks at the first issue.** `PUT /invoices/settings` takes `FOR UPDATE` on the
settings row. It therefore waits behind an issue's `FOR SHARE`. It then checks for the
counter row. A changed `series_start` while the row exists is refused with 409
`series_locked`. Every other settings field stays editable after the first issue: the
documents keep their seller snapshot (D4).

`GET /invoices/meta` (`invoices:access`) answers what every page needs: the currency
(`NOK`), `defaultPaymentTermsDays`, `sellerComplete` and the missing fields by name,
`anythingIssued`, `seriesStart`, `storageAvailable`, the VAT codes in force today with
today's rate, and the caller's capabilities (`canCreate`, `canIssue`, `canManage`).

### D3 — VAT codes, with rates as dated periods

The table is split in two. A rate change (for example 25 → 26 % on 1 January) is then a
new period on the same code, not a new label that every open draft must be re-coded to.
The energy supply periods are the precedent for the exclusion
(`srv/db/migrations/00005_energy_baseline.sql:16,92`).

**`invoices.vat_codes`:**

| Column | Rule |
| --- | --- |
| `id` | |
| `code` | ≤ 10, the tenant's label, unique case-insensitively (`lower(code)`) |
| `name` | ≤ 100 |
| `saf_t_code` | ≤ 5, the SAF-T standard tax code |
| `ehf_category` | `S`, `Z`, `E`, `AE`, `G`, `O` or `K` (UNCL5305) |
| `exemption_reason` | ≤ 200; required unless `S` (a CHECK) |
| `active` | |
| `created_at`, `updated_at`, `revision` | |

**`invoices.vat_code_rates`:** `id`, `vat_code_id`, `rate_percent numeric(5,2)`,
`valid_from date`, `valid_to date` NULL (open-ended). No two periods of one code overlap:
`EXCLUDE USING gist (vat_code_id WITH =, daterange(valid_from, valid_to, '[]') WITH &&)`.
The migration repeats `CREATE EXTENSION IF NOT EXISTS btree_gist`. For category `S` the
rate is > 0 and ≤ 100. For every other category it is exactly 0. Either is a 400 on
`ratePercent`.

**Seed**, `ON CONFLICT DO NOTHING`, each with one open period from 2026-01-01:

| Code | Name | Rate | Category | SAF-T | Exemption reason |
| --- | --- | --- | --- | --- | --- |
| `3` | Utgående mva 25 % | 25.00 | S | 3 | — |
| `31` | Utgående mva 15 % | 15.00 | S | 31 | — |
| `32` | Utgående mva 11,11 % | 11.11 | S | 32 | — |
| `33` | Utgående mva 12 % | 12.00 | S | 33 | — |
| `5` | Fritatt innenlands 0 % | 0 | Z | 5 | Fritatt for merverdiavgift |
| `51` | Omvendt avgiftsplikt 0 % | 0 | AE | 51 | Omvendt avgiftsplikt – Merverdiavgift ikke beregnet |
| `52` | Utførsel 0 % | 0 | G | 52 | Utførsel av varer og tjenester |
| `6` | Utenfor mva-loven 0 % | 0 | **E** | 6 | Unntatt fra merverdiavgift (mval. kap. 3) |
| `7` | Ingen mva-behandling | 0 | **O** | 7 | Selger er ikke registrert i Merverdiavgiftsregisteret |

`6` is `E`, not `O`. anskaffelser.dev says E "shall be used for products and services
that is stated in §3-2 to §3-20" (unntatt), and O is for a seller outside the VAT act —
a seller that is not VAT-registered. A registered tenant selling an unntatt service with
`O` would state the wrong treatment, and fail EHF validation in phase 2. `7` is the `O`
code for a non-registered seller; D6 enforces which of the two a seller may use.

**Endpoints** (`invoices:access` to read, `invoices:access+invoices:manage` to write):

- `GET /invoices/vat-codes`: every code with its periods.
- `POST /invoices/vat-codes` `{code, name, safTCode, ehfCategory, exemptionReason,
  ratePercent, validFrom}`: a code and its first, open period.
- `PUT /invoices/vat-codes/{id}` `{code, name, safTCode, ehfCategory, exemptionReason,
  active, revision}`. A code is never deleted; it is deactivated. A code **in use** —
  referenced by any line, draft or issued — cannot change `ehfCategory` or `safTCode`:
  409 `vat_code_in_use`. Its name, label and reason stay editable; issued lines snapshot
  what they printed (D4).
- `POST /invoices/vat-codes/{id}/rates` `{ratePercent, validFrom}`: **the rate-change
  rule.** It closes the open period at `validFrom − 1 day` and opens a new one from
  `validFrom`. `validFrom` must be after the open period's `valid_from` (400 on
  `validFrom`). It must also be after the latest `issue_date` of any issued document:
  409 `rate_change_in_past`. No issued line ever falls into a period that changed after
  it was issued.
- `DELETE /invoices/vat-codes/{id}/rates/{rateId}`: removes a mistaken future period and
  reopens the previous one (`valid_to = NULL`). Only the latest period may go (409
  `rate_period_not_latest`), never the only one (409 `rate_period_last`), and only while
  no issued document's `issue_date` is on or after its `valid_from` (409
  `rate_period_in_use`).

**The rate a line carries is resolved at issue for the issue date**, from the period that
covers it, and snapshotted on the line (D6). A draft holds only the code.

`active = false` means the code is not offered for new lines. A draft that already holds
it is refused at issue (409 `vat_code_inactive`, naming the line). Credit notes are
exempt from both the active and the validity check (D8).

### D4 — The document: drafts, snapshots, and the tables

**`invoices.invoices`:**

| Column | Rule |
| --- | --- |
| `id` | |
| `kind` | `invoice` \| `credit_note` |
| `status` | `draft` \| `issued` |
| `number bigint` | NULL while `draft`, set at issue, unique. A CHECK: `(status = 'draft') = (number IS NULL)` |
| `customer_id` | opaque, NOT NULL |
| `credits_invoice_id` | the original, in-module FK; NOT NULL exactly when `kind = 'credit_note'` (a CHECK) |
| `issue_date date` | NULL until issued (D6) |
| `delivery_date date` | one day, or… |
| `delivery_from`, `delivery_to date` | …a period |
| `delivery_address_line1/2`, `delivery_postal_code`, `delivery_city`, `delivery_country` | optional place of delivery |
| `payment_terms_days` | 0–365; NULL on a credit note |
| `due_date date` | NULL until issued; `issue_date + payment_terms_days`, computed at issue; always NULL on a credit note |
| `currency char(3)` | `NOK` in phase 1 |
| `exchange_rate numeric(14,6)` | exactly 1 in phase 1 |
| `exchange_rate_date date` | set to `issue_date` at issue |
| `your_reference`, `our_reference`, `order_reference` | ≤ 100 each |
| `note` | ≤ 1000, printed |
| `internal_note` | ≤ 1000, never printed; immutable after issue |
| the buyer snapshot | below |
| the seller snapshot | below |
| `net_total`, `vat_total`, `gross_total`, `vat_total_nok numeric(14,2)` | |
| `pdf_object_key`, `pdf_sha256` | NULL until the PDF is stored (D7) |
| `issued_at`, `issued_by_user_id`, `created_by_user_id`, `created_at`, `updated_at`, `revision` | |

**Delivery** (§ 5-1-1 nr. 4, "tidspunkt og sted for levering"). A CHECK: either
`delivery_date` alone, or `delivery_from` and `delivery_to` together with
`delivery_from ≤ delivery_to`, or none of the three. None is allowed on a draft only;
the issue refuses it (D6). The API never invents a delivery date. The UI may prefill one.
The delivery address group is either wholly empty or has line 1, city and country. It is
printed as "Leveringssted" only when it differs from the buyer address. For services the
buyer address is the conventional place of delivery. **UNCERTAIN:** no Skatteetaten
statement was read on whether that convention is enough.

**The buyer snapshot**, written at issue, printed from, never re-read:

| Column | Source (`CustomerBillingProfile`) |
| --- | --- |
| `buyer_customer_number` | `CustomerNumber` |
| `buyer_type` | `Type` (`business` \| `person`) |
| `buyer_name` | `LegalName` when non-empty, else `Name` |
| `buyer_organisation_number` | `LegalID` when `LegalCountry = "NO"` and `Type = "business"`, else NULL |
| `buyer_foreign_id` | a business's foreign `LegalID`, prefixed with its `LegalCountry` (for example `SE556677889901`), printed under "VAT/Reg. no."; NULL otherwise. Never for a person. |
| `buyer_address_line1/2`, `buyer_postal_code`, `buyer_city`, `buyer_region`, `buyer_country` | `InvoiceAddress`, NULL when it is nil |
| `buyer_peppol_id` | `PeppolID` |
| `buyer_gln` | `GLN` (phase 2's EHF uses it) |
| `buyer_language` | `en` when `Language` is `en`, else `nb` (an empty `Language` is `nb`) |

There is no `buyer_email`. Nothing prints it. 1B's send reads the **current** profile's
invoice e-mail at send time, so an anonymised person correctly has none.

**The seller snapshot**, copied from the settings row at issue: `seller_legal_name`,
`seller_organisation_number`, `seller_vat_registered`, `seller_in_foretaksregisteret`,
`seller_address_line1/2`, `seller_postal_code`, `seller_city`, `seller_country`,
`seller_bank_account`, `seller_iban`, `seller_bic`, `seller_email`, `seller_footer_text`.

**`invoices.lines`:**

| Column | Rule |
| --- | --- |
| `id`, `invoice_id` (FK, `ON DELETE CASCADE`) | |
| `position` | 1…n, unique per document |
| `description` | 1–500 |
| `quantity numeric(12,3)` | > 0 |
| `unit` | ≤ 20, free text ("timer", "stk") |
| `unit_price numeric(14,4)` | ≥ 0 |
| `discount_percent numeric(5,2)` | 0–100 |
| `vat_code_id` | FK |
| `credits_line_id` | FK to `lines(id)`; on a credit note's line, the original line it credits; NULL on an invoice |
| `line_gross`, `line_allowance`, `line_net numeric(14,2)` | computed on every save (D5) |
| `vat_rate_percent`, `vat_category`, `saf_t_code`, `exemption_reason` | the issue snapshot; NULL on a draft |

**`invoices.vat_summaries`:** per issued document, per (`vat_category`, `rate_percent`):
`saf_t_code`, `exemption_reason`, `taxable_amount`, `vat_amount`, `vat_amount_nok`. It
has a row for each 0 % category too (§ 5-1-5: taxable and exempt sales totalled
separately).

**Drafts.**
- `POST /invoices` (`invoices:access+invoices:create`) creates an invoice draft. Body:
  `customerId`, `paymentTermsDays?`, `currency?`, `deliveryDate?` or
  `deliveryFrom?`/`deliveryTo?`, `deliveryAddress?`, `yourReference?`, `ourReference?`,
  `orderReference?`, `note?`, `internalNote?`, `lines: [{description, quantity, unit,
  unitPrice, discountPercent, vatCodeId}]`.
- `PUT /invoices/{id}` (same permission): a full replace with `revision`.
- `DELETE /invoices/{id}` (same permission).
- Only drafts are edited or deleted. An issued document answers 409 `invoice_issued`.

Prefills when the create omits a field, from a directory read made **before** the insert
(no lock is held): `yourReference` = `BuyerReference` (empty stays empty);
`paymentTermsDays` = `PaymentTermsDays`, else `settings.default_payment_terms_days`. The
UI prefills `ourReference` with the signed-in user's display name; the API takes what it
is given. `BuyerReference` is required by phase 2's EHF (PEPPOL-EN16931-R003), so the
editor nudges when it is empty.

The **customer gates** on creating and saving an invoice draft, in this order, from the
billing profile (after the contract change in D10):
1. `MergedInto` set: 409 `customer_merged`, carrying `mergedInto`.
2. `Status = "archived"` (which includes an anonymised customer, `docs/customers.md:109`):
   409 `customer_archived`.
3. `Status = "disabled"`: 409 `customer_blocked`. `disabled` finally means "blocked for
   invoicing".
4. A nil profile — which cannot happen, since customers are never deleted — is 409
   `customer_missing`.

The gates never apply to a credit-note draft, and never to reads (D8).

Validation of a save is a 400 on the field: `currency` other than `NOK` ("Only NOK in this
phase"); an unknown or inactive `vatCodeId`; more than 500 lines ("At most 500 lines"); the
money bounds in D5. A draft may have no lines; the issue refuses that.

Draft reads carry `warnings`, never refusals:
- `customer_currency_differs` when the profile's `Currency` is set and is not `NOK`;
- `issued_late` when delivery (the date, or `delivery_to`) is more than one month before
  today (D6);
- `credit_exceeds_invoice` / `credit_exceeds_line` on a credit-note draft (D8).

**Preview.** `GET /invoices/{id}/preview.pdf` (`invoices:access+invoices:create`) renders
a draft on demand. It is never stored. It carries the watermark "UTKAST — ikke et
salgsdokument" and no number. It uses the current settings (even incomplete), a fresh
directory read for an invoice draft's buyer, the copied snapshot for a credit-note draft,
and today as the would-be issue date. An issued document answers 409 `invoice_issued`.
Preview and download are two operations because access is declared per operation through
`x-vantigo-access` (`srv/openapi/lint.go:21-34`).

**The list.** `GET /invoices` (`invoices:access`): `status` (`draft`|`issued`), `kind`,
`customerId`, `search`, `from`/`to` on issue date, and `page`/`pageSize`, answering
`PaginatedResponseOfInvoicesInvoiceListItem` (`{data, pagination}`, `common.yaml`
`PaginationMetadata`). That is every module list's convention
(`openapi/expenses.yaml:1716-1727,1773-1790`), default page size 25, max 100
(`srv/expenses/entries.go:1469-1479`). There is no keyset cursor: the only keyset list in
the codebase is the customers timeline, and `number` is NULL on every draft. The order is
`number DESC NULLS FIRST, id DESC`: drafts first, newest first. `search` matches the
number exactly (digits) or `buyer_name` case-insensitively. A draft has no buyer snapshot,
so it is found through `customerId`, not `search`. Drafts show the customer's current name
through one `Customers(ids)` directory call per page.

**The response** of `GET /invoices/{id}` carries every column above in camelCase, the
lines, the VAT summaries, `warnings`, and:
- on an invoice: `creditedAmount` (the sum of its issued credit notes' gross),
  `uncreditedAmount`, and `creditNotes: [{id, number, issueDate, grossTotal, status}]`
  (credit-note drafts included, with `number` null);
- on a credit note: `credits: {id, number, issueDate}`.

### D5 — Money: the line, the VAT per rate, the bounds

**Input.** HTTP numbers are `number/double` in this codebase (for example
`openapi/expenses.yaml:800-803`), so every amount arrives as a float64. It is converted through `strconv.FormatFloat(f, 'f', -1, 64)`
and then `big.Rat.SetString`, never `SetFloat64`, which keeps the binary value (0.1 is not
0.1). A value with more decimals than its column is a 400 on the field: quantity 3,
unit price 4, discount 2.

**Arithmetic.** `math/big.Rat`; every rounding is to two decimals, half away from zero,
the codebase convention (`docs/expenses.md:112-118`).
- `line_gross = round(quantity × unit_price)`.
- `line_allowance = round(line_gross × discount_percent / 100)`.
- `line_net = line_gross − line_allowance`.
- Per (category, rate): `taxable_amount = Σ line_net`; `vat_amount =
  round(taxable_amount × rate / 100)`. **VAT is computed per rate on the sum of the
  lines' nets** (Peppol BR-CO-17), never per line. The PDF and a later EHF then agree to
  the øre. Skatteetaten accepts VAT per rate (research §2.5).
- `net_total = Σ line_net`; `vat_total = Σ vat_amount`; `gross_total = net_total +
  vat_total`.
- No øre rounding of the total: payment is electronic (research §2.5).
- `vat_amount_nok = round(vat_amount × exchange_rate)` per summary row;
  `vat_total_nok = Σ vat_amount_nok`. In phase 1 the rate is 1, so they are equal. The
  formula is in the model now so phase 2+ changes no layout.

The line is stored as gross minus allowance, not as one rounded product. EHF expresses a
line discount as an allowance, and `round(qty × price) − round(allowance)` can differ from
`round(qty × price × (1 − d))` by 1 øre, which PEPPOL-EN16931-R120 checks. Storing the
two parts makes the PDF and the EHF agree by construction. **UNCERTAIN:** R120's
tolerance was not confirmed.

**Bounds**, each a 400 before the database can overflow:
- `line_gross` ≤ 999 999 999.99, 400 on `lines[i].unitPrice` "The line amount is too
  large";
- `gross_total` ≤ 99 999 999 999.99, 400 on `lines` "The document total is too large";
- at most 500 lines.

**Currency in phase 1.** The API accepts only `currency = settings.default_currency`,
which is fixed to `NOK`: 400 on `currency` "Only NOK in this phase". `exchange_rate` is
exactly 1. The columns exist, and `exchange_rate_date` is set at issue. § 5-1-1 nr. 6
requires VAT stated in NOK, at the rate on the invoice date (research §2.5). A document
that accepted `EUR` today would print no NOK VAT and carry a rate chosen before its issue
date was known.

### D6 — Issue: the date, the refusals, one serialised transaction

`POST /invoices/{id}/issue` `{issueDate?}` (`invoices:access+invoices:issue`). The same
endpoint issues an invoice and a credit note.

**The issue date.** Lovdata § 5-1-3 third paragraph
(https://lovdata.no/forskrift/2004-12-01-1558/§5-1-3): "Salgsdokument som utstedes innen
de femten første virkedager i måneden, kan angi siste dato i foregående måned som
dokumentasjonsdato, forutsatt at varen eller tjenesten er levert på dette tidspunktet."
That is the only date other than the actual one the regulation allows. So exactly two
dates are allowed:
- **today** (Oslo). An omitted `issueDate` means today.
- **the last day of the previous month**, only while today's calendar day is ≤ 15 **and**
  the document's delivery (`delivery_date`, or `delivery_to`) is on or before that day.

"Virkedager" is not defined in the regulation. Fifteen working days always reach at least
the 17th of the month, even counting Saturdays as working days. "Calendar day ≤ 15" is
therefore always within the law and needs no holiday calendar. It is stricter than the
law, and `docs/invoices.md` says so.

On top of that, the date must not be before the latest `issue_date` of any issued
document. Numbers and dates are then both monotone. The law does not require this; it is
an extra guard. With it, the previous-month date is refused once any document is already
dated in the current month.

Any other date is 409 `issue_date_not_allowed`. The message names the allowed dates for
this document on this day.

**`issued_late`.** When delivery is more than one month before the issue date, the issue
succeeds and the response carries `warnings: ["issued_late"]`. § 5-2-2 says "senest en
måned etter levering" (https://lovdata.no/forskrift/2004-12-01-1558/§5-2-1); refusing
would leave the sale undocumented.

**Before the transaction** (no lock is held, `docs/module-boundaries.md:230-233`; no
contract call and no object-store call under a lock, `docs/expenses.md:1203-1205`):
1. Load the draft: 404 if absent. Not a draft: 409 `invoice_issued`.
2. No store configured (`Deps.Config.StorageProvider` empty, so the store would answer
   `storage.ErrNotConfigured`): 503 `storage_unavailable`. An issued number whose PDF can
   never be stored is not allowed to exist.
3. For an **invoice** only: read `BillingProfile(customer_id)`. Remember the
   `customer_id` that was read. A credit note reads no directory at all (D8).

**The transaction**, in this order. The counter row is the only thing that serialises two
issues, so every check that depends on other documents runs after it:
1. `SELECT … FROM invoices.invoices WHERE id = $1 FOR UPDATE`. Not a draft any more: 409
   `invoice_issued`.
2. For an invoice: the locked row's `customer_id` must equal the id read before the
   transaction. A merge may have re-pointed it in between. Otherwise 409
   `invoice_changed` ("The invoice changed; try again").
3. `SELECT … FROM invoices.settings WHERE id = 1 FOR SHARE`: the seller snapshot and
   `series_start`.
4. **Allocate** the number (D2).
5. Only now, the checks. Any refusal rolls the whole transaction back, the number with
   it:
   - `seller_incomplete` (D2);
   - `no_lines`;
   - `delivery_date_missing`: no delivery date or period;
   - `issue_date_not_allowed`: the rule above, with `max(issue_date)` read now, after the
     counter lock. The transaction runs READ COMMITTED, so this statement sees every
     issue committed before it;
   - for an **invoice** only:
     - the customer gates of D4 from the pre-transaction read (`customer_merged`,
       `customer_archived`, `customer_blocked`, `customer_missing`). A customer disabled
       after the draft was saved is refused here;
     - `buyer_incomplete`: the snapshot has neither a complete address (line 1, city and
       country; postal code too when the country is `NO`) nor an organisation number
       (§ 5-1-2);
     - `vat_code_inactive` and `vat_code_not_valid` (no rate period covers the issue
       date), each naming the line;
     - `vat_not_registered`: the seller is not VAT-registered and a line's category is
       not `O`. VAT charged without the right to charge it is still owed to the state
       (research §2.1, § 5-1-2);
     - `category_o_not_allowed`: the seller is VAT-registered and a line's category is
       `O`. Peppol also forbids mixing `O` with any other category on one document;
     - `reverse_charge_needs_org_number`: a line is `AE` and
       `buyer_organisation_number` is empty (§ 5-1-1);
     - `vat_codes_ambiguous`: two lines share a (category, rate) but carry different
       SAF-T codes, so one VAT summary row could not name its SAF-T code;
   - for a **credit note** only: lock the original with `SELECT … FOR UPDATE`, then
     `credit_exceeds_line` and `credit_exceeds_invoice` (D8).
6. Write, in this order: the line snapshots, the VAT summaries, and last the invoice row
   (`status = 'issued'`, `number`, `issue_date`, `due_date`, `exchange_rate_date`, both
   snapshots, the totals, `issued_at`, `issued_by_user_id`). The row goes last because
   the immutability trigger refuses line writes under an issued document (D9).

Every refusal above is a 409 with that code. The lock order is always the document, then
the settings row, then the counter, then the original. Nothing else takes these in
another order: `PUT /settings` takes only the settings row; the merge holder updates
invoice rows only; 1B's payments lock only the original. So there is no cycle.

**After the commit**, the PDF is stored by the store-once path (D7). A failure there is
logged at warn and does not fail the issue: the number is committed. The response then
carries `pdfStored: false`, and the next download stores it.

The response is 200 with the issued document and its `warnings`.

### D7 — The PDF: rendered from the snapshot, stored once, never replaced

**Library.** maroto v2 (pure Go; the runtime image is distroless static with no fonts,
research §8) added to `apps/server/go.mod`. It renders through gofpdf.

**What makes it lawful is store-once, not determinism.** A PDF that was never stored was
never downloaded, because every download goes through the stored object. So:
- The render reads only the document's own rows and snapshots. It never reads settings,
  the directory or the VAT tables.
- The **store-once path** (after the issue commits, and on a download that finds no
  hash): render; compute the SHA-256; key = `documents/<id>/<number>-<sha256>.pdf`; `Put`
  it unless `Exists` says it is there; then
  `UPDATE invoices.invoices SET pdf_object_key = $key, pdf_sha256 = $hash WHERE id = $id
  AND pdf_sha256 IS NULL`. No lock is held around the store calls
  (`docs/expenses.md:1203-1205`).
- Two racing downloads may each `Put`. The key names the bytes, so neither overwrites the
  other. The first `UPDATE` wins. The loser re-reads the row and streams the winner's
  object. Its own object is an orphan that nothing ever served. There is no sweeper, as
  for expenses receipts (`docs/storage.md:88-105`).
- Once `pdf_sha256` is set, the document is **never re-rendered**.
- The module never calls `Delete` on its scope.

**The key.** `pdf_object_key` holds the key relative to the scope,
`documents/<id>/<number>-<sha256>.pdf`. The store is `storage.NewScope(inner,
"invoices")`, so the physical key is `invoices/documents/…` (`docs/storage.md:72-92`).

**`GET /invoices/{id}/pdf`** (`invoices:access`). A draft answers 409 `invoice_draft`.
- No hash yet: the store-once path, then stream.
- Otherwise `Get` the object, read it whole (capped at 20 MB), and verify its SHA-256
  against `pdf_sha256`. A mismatch is 500, logged at error. It never falls back to
  re-rendering.
- `storage.ErrNotConfigured`: 503 `storage_unavailable`. Any other `Get` failure: 503
  `storage_unavailable`. `ErrNotExist` while a hash is set: 500, logged at error — the
  stored document is gone, and that is an operator problem, not something to paper over.
- `Content-Type: application/pdf`, `Content-Disposition: attachment;
  filename="faktura-<number>.pdf"` (`kreditnota-<number>.pdf`; in English
  `invoice-<number>.pdf`, `credit-note-<number>.pdf`, by `buyer_language`).

**Reproducible output**, a nice-to-have, not the legal proof. gofpdf writes `/ModDate` as
`time.Now()` unless it is set, and writes font objects in Go map order unless catalog
sorting is on; maroto exposes neither (`github.com/phpdave11/gofpdf` `fpdf.go`: `putinfo`
uses `timeOrNow(f.modDate)`; `SetCatalogSort`/`SetDefaultCatalogSort`; maroto's provider
calls only `SetCreationDate`). So:
- `init()` calls `gofpdf.SetDefaultCatalogSort(true)` and
  `gofpdf.SetDefaultModificationDate(<a fixed constant date>)`. Both are process-global,
  so they are constants, never per-document values.
- The creation date is `issued_at` (maroto `WithCreationDate`).
- Sequential mode, never `WithConcurrentMode`.
- A golden test pins the bytes in-process. The bytes may change with any upgrade of
  maroto, gofpdf or the font, or with a template edit; stored PDFs are unaffected.

**Font.** Noto Sans Regular and Bold, `go:embed`, under the SIL Open Font License, whose
text ships beside the files. It covers æøå and the en dash in the AE wording.

**Layout** (language per `buyer_language`, nb default, en; § 5-1-1a allows English):
- Seller block: legal name, address, "Org.nr. 999 999 999" followed by "MVA" when
  VAT-registered, "Foretaksregisteret" when set, e-mail when set.
- Buyer block: name, address, organisation number when set, or `buyer_foreign_id` under
  "VAT/Reg. no.".
- Meta: "Faktura" or "Kreditnota"; number; issue date; delivery date or period;
  "Leveringssted" when it differs from the buyer address; due date and terms (invoices
  only); "Deres ref.", "Vår ref.", order reference.
- A credit note prints "Kreditnota til faktura <number> av <issue date>".
- The line table: description, quantity, unit, unit price, discount %, VAT %, net.
- The VAT summary, one row per (category, rate), 0 % categories included; under it, each
  non-`S` category's exemption reason. The AE text "Omvendt avgiftsplikt –
  Merverdiavgift ikke beregnet" is printed verbatim in Norwegian whatever the document's
  language: it is the regulation's own wording.
- Totals: net, VAT, gross ("Å betale" on an invoice).
- The payment block, **invoices only**: bank account, IBAN/BIC when set, due date,
  "Vennligst oppgi fakturanummer ved betaling". A credit note has no due date and no
  payment block.
- The note, then `footer_text`.
- No logo in this phase.

### D8 — Correction is a credit note, in the same series

`POST /invoices/{id}/credit` (`invoices:access+invoices:issue`) creates a **draft credit
note** from an issued invoice (§ 5-2-7, https://lovdata.no/forskrift/2004-12-01-1558/§5-2-7:
"skal det også utstedes en kreditnota som reverserer opprinnelig salgsdokument").
Refusals: a draft original, 409 `invoice_draft`; a credit note as original, 409
`credit_note_not_creditable`; an original with `uncreditedAmount ≤ 0`, 409
`invoice_fully_credited`. Several credit-note drafts may exist at once; the caps are
decided at issue.

The draft copies from the original: `customer_id`, currency, exchange rate, the delivery
date or period and address, the references, and the **buyer snapshot**. It reads no
directory. For an anonymised customer a re-read would give "Anonymised person"; the
original's snapshot is what the correction must name. It copies every line with a
positive quantity (the kind says it reverses), its `vat_code_id`, and
`credits_line_id` pointing at the original line. `buyer_language` is the original's.

**What a credit draft may change** (`PUT /invoices/{id}`, `invoices:access+invoices:create`):
- remove lines;
- lower a quantity;
- lower a unit price (a price reduction, "prisavslag");
- edit a description, the note and the internal note.

It may not add a line, raise any amount, lower a discount, reference an original line
twice, or change a VAT code, the customer, the currency or the rate. Each is a 400 on the
field.

**The caps**, authoritative at issue, under the original's lock (D6 step 5). A draft save
only warns.
- Per original line: over the issued credit notes plus this one, the credited quantity
  and the credited `line_net` must each be at most the original line's. Otherwise 409
  `credit_exceeds_line`, naming the line. This implies a cap per rate: a line at 25 %
  cannot be credited twice while a 0 % line is never credited, which would reverse
  output VAT twice and misstate the VAT return per SAF-T code (research §3.2).
- The headline: this credit note's gross must be at most the original's
  `uncreditedAmount`. Otherwise 409 `credit_exceeds_invoice`.

The cap is common practice, not law (research §2.3).

**What a credit note skips, and why.** A credit note is the correction the law requires.
Gates meant for new invoices must not block it:
- every customer gate (merged, archived, disabled). An invoice to a customer who has
  since been archived, anonymised or disabled must still be correctable;
- the VAT active and validity checks. The rates are the original's snapshot, never
  re-looked up; a code that expired on 1 January still credits at the old rate;
- `vat_not_registered` and `category_o_not_allowed`. It reverses the original's
  treatment, whatever the seller's registration is now;
- `buyer_incomplete`. The snapshot was checked when the original was issued.

It keeps: the issue-date rule, `seller_incomplete`, `no_lines`, `delivery_date_missing`,
and both caps. Its seller snapshot is the settings at its own issue. Its VAT summaries
are computed on its own lines by D5. Its `due_date` and `payment_terms_days` stay NULL.
It is numbered from the same series. A credit note is never itself credited.

### D9 — Immutability is enforced in SQL too

The API answers 409 `invoice_issued` to every edit and delete of an issued document. That
is not enough on its own: a later code path (a phase-3 write-back, a repair, a bug) could
still update an issued row. Skatteetaten says a system that lets a user easily override
the rule breaches it "uavhengig av om muligheten rent faktisk benyttes" (research §2.2).

The migration adds `BEFORE UPDATE OR DELETE` triggers (plpgsql, inside
`-- +goose StatementBegin` / `-- +goose StatementEnd`):
- On `invoices.invoices`, when `OLD.status = 'issued'`: `DELETE` is refused. `UPDATE` is
  refused unless the only columns that differ are `customer_id` (the merge holder, D10)
  and `pdf_object_key`/`pdf_sha256` changing from NULL (set once). `revision` and
  `updated_at` do not move on an issued row.
- On `invoices.lines` and `invoices.vat_summaries` (`BEFORE INSERT OR UPDATE OR DELETE`):
  refused when the parent invoice exists with `status = 'issued'`. A missing parent is a
  cascade from deleting a draft, and is allowed; the parent's own trigger has already
  refused deleting an issued one.
- The refusal raises SQLSTATE `P0001` with the message `invoices: issued document is
  immutable`.

This is why D6 writes lines and summaries before the invoice row. `internal_note` is
immutable after issue in phase 1; a later `PATCH` may allow it as non-document state
(research §2.3).

### D10 — The customers contract change, and the two slots

**The contract change (a change to the customers module inside this PR).**
`CustomerEntry` carries `Archived` and `MergedInto` but no status, and
`CustomerBillingProfile`, the snapshot's source, carries `Archived` only
(`srv/contracts/directory.go:12-28,70-111`). The gates in D4 cannot be expressed. So:
- `CustomerBillingProfile` gains `Status string` (`active` | `disabled` | `archived`) and
  `MergedInto *int32`. One read then answers every gate and the snapshot.
- `srv/customers/directory.go` and its billing-profile query fill them;
  `srv/customers/directory_test.go` proves both, a disabled and a merged-away customer
  included.
- The fakes of `CustomerDirectory` in other modules' harnesses compile unchanged (the
  fields are new). The invoices fake models both.
- The contract comment at `srv/contracts/directory.go:132-137` says an archived customer
  "must still be able to … invoice it again". It becomes: archived customers still
  resolve, so a past invoice can be shown and credited; Invoices refuses a new invoice to
  an archived or disabled customer.
- `docs/module-boundaries.md` and `docs/customers.md` are updated (D13).
- The change is additive. Nothing is live, and the user allows breaking customers
  contract changes in any case.

**`CustomerReferences`** (`srv/contracts/references.go:25-49`). `RepointCustomer`
updates `customer_id` from `from` to `into` on every row, draft and issued, inside the
caller's transaction. It reports `invoices.invoices` with its count. Issued documents keep
their buyer snapshot untouched: the id is not printed, the snapshot is. It keeps the
holder's rules: `from == into` writes nothing and reports zero; it reads no contract; it
runs while the module is disabled, so its constructor needs only the pool. The trigger
allows exactly this column (D9).

**`CustomerPersonalData`** (`srv/contracts/personal_data.go:9-55`).
- `ExportCustomerData`: nil when nothing is held. Otherwise `{documents, drafts}`: every
  issued document and every draft for the customer, with number, kind, dates, lines,
  totals, the buyer snapshot, the references, the note and the internal note. The
  customers export already treats staff-written internal notes as data held about the
  person (`docs/customers.md:1519`).
- `EraseCustomerData`, inside the anonymisation transaction:
  - deletes the customer's **drafts**, invoice and credit-note drafts alike, reported as
    `invoices.drafts` with the count deleted. A draft is not a salgsdokument (research
    §2.6), so it has no retention basis and GDPR art. 17 applies;
  - keeps issued documents and their buyer snapshots, reported as `invoices.documents`
    with count 0. Bokføringsloven § 13 (https://lovdata.no/lov/2004-11-19-73/§13) keeps
    sales documentation for five years after the end of the financial year. This is the
    projects precedent, a module that kept everything listed at zero
    (`docs/customers.md:1606`).
  - `ErasedData` has no reason field (`{Kind, Count}`), and it does not grow one. The
    reason "kept under bokføringsloven § 13" is written in `docs/invoices.md` and in the
    anonymisation table in `docs/customers.md`.

### D11 — The journal

`GET /invoices/journal?from=YYYY-MM-DD&to=YYYY-MM-DD&page&pageSize` (`invoices:access`).
`from` and `to` are **issue dates**, both required, `from ≤ to` (400 otherwise). It pages
by the list convention (D4).

- `data`: the issued documents with an issue date in the range, in number order
  ascending. Each row: `number`, `kind`, `issueDate`, the delivery, `dueDate`,
  `buyerCustomerNumber`, `buyerName`, `buyerOrganisationNumber`, `currency`, `netTotal`,
  `vatTotal`, `grossTotal`, `vatSummaries: [{safTCode, category, ratePercent,
  taxableAmount, vatAmount}]`, and `creditsNumber` on a credit note.
- **Credit notes are signed negative** in every amount of the journal. They are stored
  positive (D8); a journal that summed them as positive would overstate revenue.
- `totals`: over the whole range, not the page, per (`safTCode`, `category`,
  `ratePercent`), plus net, VAT and gross, signed.
- `gaps`: over the whole range, the numbers in `[max(first − 1, series_start) … last]`
  that no issued document holds, where `first` and `last` are the lowest and highest
  numbers in the range. The first document of the range is thereby checked against the one
  before it. A number below `series_start` is never a gap. It must be empty; at most 1000
  are listed, with `gapsTruncated`.
- `seriesStart`, and `counterLast`: the counter's last allocated number (`next_value − 1`;
  null without a counter row). The page shows a warning when `counterLast` is not the
  highest issued number, which would mean a number was allocated without a document.

The journal is what proves complete registration (§ 5-1-3; Skatteetaten: it must be
visible that "det ikke er brudd i nummerserien", research §2.2). The CSV export is 1B.

### D12 — The frontend

App "Invoices", `@vantigo/invoices-ui`, en + nb throughout.
- **List**: status and kind chips, customer filter, search, issue-date range, paging;
  drafts first.
- **Invoice page, draft**: the buyer picker over the customers list (needs
  `customers:view`, D1); delivery date or period, required before Issue, with the optional
  place of delivery; references, with a nudge when "Deres ref." is empty; payment terms;
  the lines table with add, remove and reorder; a VAT code per line from the codes active
  today; live totals per rate by D5's rule (the server stays authoritative); the draft's
  warnings; Preview, Issue and Delete.
- **Issue dialog**: "This assigns the next number and cannot be undone." It offers today,
  and the last day of the previous month when D6 allows it. It shows `issued_late` after
  the issue.
- **Invoice page, issued**: the document header, lines, VAT summary, its credit notes,
  Download PDF, Credit (opens the new credit-note draft). A `pdfStored: false` issue shows
  that the PDF is stored on first download.
- **Credit note**: "Kreditnota til faktura N" linking the original; its draft editor
  offers only what D8 allows and shows the cap warnings.
- **Settings** (`invoices:manage`): the seller record with the completeness checklist;
  the series start, read-only once anything is issued; the VAT codes table with add,
  edit, deactivate and the rate periods (add a period, remove the latest future one).
- **Journal**: the date range, the gap check result in words ("No gaps between N and M"
  or the missing numbers), the per-code totals, paging.
- Host: `moduleApp(...)` in the registry, `moduleKeys`, the layout route, the i18n
  import, the admin catalog entries. No dashboard card and no customer tab in 1A.

### D13 — Docs

- `docs/invoices.md` (new): the law in one page — numbering, immutability, credit notes,
  VAT per rate, the issue-date rule and that "calendar day ≤ 15" is stricter than the law,
  delivery, NOK only; the model; the endpoints and every error code; the permissions and
  the `customers:view` note; the PDF and store-once; retention: five years after the end
  of the financial year (§ 13), nothing purged in phase 1, a purge is later work
  (**UNCERTAIN:** the 2027 wording of § 13 was not read, research §2.5); **the operator's
  backup of the object store is part of that retention** — the only driver is `fs`, with
  no WORM (research §7.9); why anonymisation erases drafts only; and a plain warning that
  phase 1 alone does not meet the B2G duty (EHF since 2019) nor the B2B duty from
  2027-01-01 (research §1.2). What phases 1B–5 add.
- `docs/module-boundaries.md`: the module, its required dependency, the two slots it
  implements, the contract change to `CustomerBillingProfile`.
- `docs/customers.md`: `disabled` means "blocked for invoicing" — it refuses creating,
  saving and issuing an invoice, and never blocks credit notes, PDFs or reads — replacing
  "a label only" (`docs/customers.md:94-102`); archived and merged-away customers are
  refused new invoices likewise; the anonymisation table gains the invoices row (drafts
  erased, issued documents and their buyer snapshots kept under § 13).
- `ROADMAP.md`: Invoices 1A done, 1B next.
- `docs/README.md` index; `CONTRIBUTING.md`'s module list if it has one; the release note
  that `MODULES` unset now includes Invoices; the Noto Sans licence in the third-party
  notices.

## Phase 1B (next branch)

Not designed here. A follow-up branch cut from `main` after 1A merges, not a stacked PR
(the project-economy lesson). It builds only on 1A's tables. The review's rulings that
concern it, so nothing is lost:

- **Payments**, `invoices.payments`, permission `invoices:payments` (no default role).
  Removal is a **soft removal**: `removed_at`, `removed_by_user_id`, `removal_reason`
  (≤ 200, required); the row stays, because payment registrations are kept five years
  (research §2.5); `paidAmount` sums only rows not removed; the list shows removed rows
  struck through. `paid_on` is on or after the issue date and not in the future (Oslo).
  Never on a credit note. A registration locks the invoice row `FOR UPDATE`; 1A's credit
  issue already locks the original after the counter, so both caps are evaluated under
  the same row lock. Payments are refused while the open amount is ≤ 0.
- **Derived states.** A credit note's state is `issued`. For an invoice the first match
  wins: `credited` (credited ≥ gross), `paid` (open ≤ 0), `overdue` (due < today, Oslo),
  `partiallyPaid` (paid > 0), `open`. A credit after payment is allowed and may make the
  open amount negative; the response carries `refundDue`; refunds are phase 4. One SQL
  function `invoices.document_state(…, today date)` with a Go mirror and a test pinning
  them together (the `expenses.owes_employee` precedent); `today` passed in from
  `Deps.Clock()` in Oslo; the list gains a `state` filter.
- **E-mail delivery**, `POST /invoices/{id}/send` under `invoices:issue`. The platform
  change is `mail.Outbound.ReplyTo` plus `msg.ReplyTo(...)` in `message()` with a test
  (`srv/mail/outbound.go:90-125`); `Deps.Config.Mail` and `Deps.SMTPSend` already exist.
  A mail driver other than `smtp` answers 503 `mail_unavailable`. From is `cfg.From` with
  `DisplayName` = the seller's legal name; Reply-To is `settings.email`. The recipient is
  the current profile's `InvoiceEmail` (or an override); none is 409 `no_invoice_email`.
  A failed send is 502 `mail_failed` and records nothing. `invoices.deliveries` with a
  column named `recipient`, never `to`. A missing PDF is stored once first. Warn loudly
  when the profile says `ehf` or the buyer is a public body. On anonymisation the delivery
  log is kept (the evidence of when the claim was sent) with `recipient` blanked for a
  person, reported as `invoices.deliveries`.
- **The CSV export**, `GET /invoices/export.csv`: the expenses byte format
  (`docs/expenses.md:1098-1110`), one row per (document × VAT summary row), fixed English
  columns `Number;Kind;Issue date;Delivery;Due;Customer number;Buyer;Buyer org no;Currency;SAF-T code;Rate;Base;VAT;Base NOK;VAT NOK;Credits number`,
  credit notes negative, over 5000 rows a 400 "Too many rows to export", never truncated.
- **Stats and the dashboard card**, `GET /invoices/stats/summary`, credit notes excluded
  from open and overdue.
- **The customer-page tab** "Invoices" (host registry, module-gated, `invoices:access`).
- **The customers + invoices integration test** composed for real.

## Out of scope

EHF/Peppol and KID (phase 2); pulling hours, expenses and milestones (phase 3); recurring
invoices, a-konto and prepayments; foreign currency beyond the modelled columns, and
exchange-rate sources; a Products picker on lines; reminders, late interest, payment
imports, overpayment, refunds and credit balances (phase 4); a logo on the PDF and
template branding; an approval step before issue; several legal entities; a per-year
series; printing and postal delivery; SAF-T or posting exports; e-invoice receiving; an
explicit due date on a draft (the terms decide it); a holiday calendar for working days;
a reason field on `contracts.ErasedData` (it would touch every implementer and the
`customer.anonymised` payload); a search method on `CustomerDirectory`; editing
`internal_note` after issue; a purge of documents older than the retention period; a
sweeper for orphaned PDF objects; WORM storage.

## Testing

Through `modtest` with a fake customer directory that models `Status` and `MergedInto`,
a fake object store with injectable failures, and a fixed clock in Oslo.

- **Settings.** Validation and both mod-11 checks; IBAN/BIC; `defaultCurrency` other than
  NOK refused; `seller_incomplete` for each missing field; `meta` names the missing
  fields. **`series_start` vs the counter**: settable while no counter row exists; 409
  `series_locked` once it does; the first issue allocates exactly `series_start` (e.g.
  1000) and the second `series_start + 1`; a `PUT /settings` racing the first issue
  either commits first (the issue uses the new start) or waits and is refused — the
  settings never show a start that was not used.
- **VAT codes.** The seed, including `6` as `E` and `7` as `O`; unique code
  case-insensitively; the reason required unless `S`; `S` rate > 0 and other rates 0; the
  in-use rule on category and SAF-T code; deactivation. **The rate-change rule**: a new
  period closes the old one at `validFrom − 1`; overlapping periods refused by the
  exclusion; `rate_change_in_past` when `validFrom` is on or before the last issue date;
  removing the latest future period reopens the previous; `rate_period_not_latest`,
  `rate_period_last`, `rate_period_in_use`; a draft issued after a change carries the new
  rate, one issued the day before carries the old.
- **Drafts.** Create with the prefills (buyer reference, terms from the profile, else the
  settings default); the customer gates in order (`customer_merged` with `mergedInto`,
  `customer_archived`, `customer_blocked`) on create and on save; full replace with
  `revision` and the stale 409; delete; `invoice_issued` on every edit and delete of an
  issued document; the delivery CHECK (date alone, period with from ≤ to, or none); the
  warnings `customer_currency_differs` and `issued_late`.
- **Money.** The float path (`0.1 × 3` is exactly `0.30`); too many decimals refused per
  field; the line and total bounds and the 500-line cap as 400s, never a 500; gross minus
  allowance per line; the per-rate rule against hand-computed cases, including one where
  per-line VAT rounding would differ by an øre; no øre rounding of the total; **NOK only**:
  `currency: "EUR"` refused on create and save, `exchange_rate` 1, `vat_total_nok =
  vat_total`, `exchange_rate_date = issue_date`.
- **The issue-date rule.** Today accepted; a date in the future, a date last week, and
  the 1st of the month refused with `issue_date_not_allowed`; **the exception**: on the
  15th the last day of the previous month is accepted when delivery is on or before it,
  refused when delivery is after it, refused on the 16th, refused once a document is
  already dated in the current month; the monotone guard; `issued_late` when delivery is
  more than one month before the issue date, with the issue still succeeding.
- **Issue refusals.** `no_lines`; `delivery_date_missing`; `buyer_incomplete` (a person
  with no address; a business with an organisation number and no address passes);
  **non-registered seller**: every line must be `O`, anything else `vat_not_registered`;
  a registered seller with an `O` line `category_o_not_allowed`;
  **`reverse_charge_needs_org_number`** for an `AE` line to a buyer without one (and
  passing with one); `vat_code_inactive`; `vat_code_not_valid`; `vat_codes_ambiguous`;
  the customer gates re-checked at issue (a customer disabled after the draft was saved);
  `invoice_changed` when a merge re-points the draft between the directory read and the
  transaction; every refusal rolls the number back — the next successful issue gets the
  number the refused one would have had.
- **The serialisation of the issue transaction.** Two racing issues get consecutive
  numbers; two racing issues with different chosen dates cannot produce a later number
  with an earlier date; two racing credit notes against one original cannot together
  exceed a line's cap; a failure injected after allocation leaves no gap; the lock order
  is document → settings → counter → original (no deadlock under a merge and a settings
  write running at the same time).
- **Snapshots.** Every buyer and seller snapshot column written at issue; `buyer_name`
  from `LegalName` else `Name`; the organisation number only for a Norwegian business;
  `buyer_foreign_id` with its country prefix; `buyer_language` (`en`, else `nb`); a later
  change to the customer or to the settings changes nothing on an issued document or its
  PDF; `due_date = issue_date + terms`.
- **Immutability, enforced in SQL too.** Direct SQL, bypassing the handlers, cannot
  update any column of an issued invoice other than `customer_id` and the NULL-to-set PDF
  columns; cannot set the PDF columns twice; cannot delete an issued invoice; cannot
  insert, update or delete its lines or VAT summaries; a draft's delete still cascades to
  its lines; the merge holder's update passes.
- **The PDF and the object-store failure modes.** No store configured: the issue is
  refused 503 `storage_unavailable` before any number is allocated (the counter row does
  not move); `Put` failing after the commit: the issue answers 200 with `pdfStored:
  false`, and the next download stores it once; two racing downloads of an unstored PDF
  store one hash and both stream the same bytes; a stored object whose bytes no longer
  match the hash answers 500 and is never re-rendered; a missing object with a hash set
  answers 500; a `Get` failure answers 503; the module never calls `Delete`; the key is
  scope-relative (`documents/<id>/<number>-<sha256>.pdf`). The draft preview has the
  watermark and no number, stores nothing, and refuses an issued document; the download
  refuses a draft (`invoice_draft`). The content: "MVA" and "Foretaksregisteret" per the
  flags, a VAT row per 0 % category with its exemption reason, the AE wording in
  Norwegian on an English document, no payment block on a credit note, "Kreditnota til
  faktura N". A golden test pins the bytes in-process (catalog sort and the fixed
  modification date set).
- **Credit notes.** The draft copies lines, codes, buyer snapshot and delivery with no
  directory read; for an archived, disabled, merged or anonymised customer it is created
  and issued; with an expired or inactive VAT code it is issued at the original's rate;
  what a credit draft may change and each 400 for what it may not; **the per-line cap**
  (the two-lines-at-two-rates case: the second credit of line A is refused
  `credit_exceeds_line` although the total fits); the headline cap; the cap only warns on
  save; same series as invoices; `invoice_fully_credited`; `credit_note_not_creditable`;
  the response links both ways; no due date.
- **The journal.** Number order; credit notes signed negative in rows and totals; totals
  over the whole range, not the page; `gaps` empty in the normal case; a gap planted by
  direct SQL (with the trigger disabled in the test) is reported, including at the range's
  first number against the one before; numbers below `series_start` never reported;
  `counterLast`; `from > to` refused.
- **The list: paging by this codebase's convention, not keyset.** `page`/`pageSize` with
  `{data, pagination}`, default 25, max 100; drafts first, then number descending, stable
  across pages; every filter; search by number and by buyer name.
- **Reserved column names.** A schema test lists every column of the `invoices` schema
  and fails on any name that `pg_get_keywords()` reports as reserved (`catcode = 'R'`).
- **The slots.** Re-point moves drafts and issued documents, leaves the snapshots, reports
  `invoices.invoices`, writes nothing for `from == into`, and works with the module
  disabled. Export: nil when nothing is held; documents and drafts with internal notes
  otherwise. Erase: drafts deleted and counted as `invoices.drafts`, issued documents kept
  and reported as `invoices.documents` at 0, run twice reports zeros.
- **The customers contract change.** `BillingProfile` returns `Status` for active,
  disabled and archived customers, and `MergedInto` for a merged-away one
  (`srv/customers/directory_test.go`).
- **Permissions** per operation, including preview needing `invoices:create` and the
  download needing only `invoices:access`; the `MODULES` check refusing invoices without
  customers.
- **Contract coverage.** `contracttest.RequireCoverage` over every operation (no frozen
  corpus file exists for this module; `docs/module-boundaries.md:286-291`), the PDF
  responses included.
- **Frontend.** The list and its paging; the draft editor with live per-rate totals
  matching the server on the øre case; the issue dialog's date offer (previous month only
  on days 1–15); the credit flow and its limits; settings with the locked series start
  and the rate periods; the journal's gap message; the admin catalog in both languages.
- **Docs** checked against the code: every endpoint, error code and permission in
  `docs/invoices.md` exists, and nothing the code has is missing from it.
