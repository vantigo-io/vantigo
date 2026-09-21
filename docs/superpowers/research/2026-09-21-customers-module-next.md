# Customers module — what to build next

Research for a Customers roadmap (`ROADMAP.md` has no Customers section today). Three
inputs: an inventory of the module as it stands, a comparison with Nordic ERP/accounting/
CRM systems, and a comparison with international CRM and PSA tools. Uncertain items are
marked **UNCERTAIN**.

## 1. Where the module stands (2026-09-21)

**The customer record is a name and a legal identity — nothing else.**
`customers.customers` = `customer_number` (unique, gapless), `name`, `status`
(active/disabled/archived), `type` (business/person) and the all-or-nothing `legal_*`
block (country, id, name, source brreg|manual, type). There is **no address, email,
phone, website, payment terms, currency, language, invoice delivery, owner, tag, group,
parent, note field or attachment** on a customer. Everything reachable goes through a
contact.

What is solid:

- **Contacts** many-to-many with customers; per-relationship `role` (free text) and
  phone/email overrides. No primary-contact concept.
- **Timeline** with generated events (`customer.*`, immutable) and manual entries
  (`note`, `interaction.call|meeting|email`, `registry.change`, `other`), revision history,
  filters, keyset paging. This is already better than most Nordic accounting systems.
- **Archival instead of delete**, 13 delegable permission keys, legal identity gated and
  response-shaping, KPI stats, spotlight search.
- **Host composition**: Energy and Projects add tabs to the customer page.

Rough edges found (file references in the inventory, all verified against the code):

1. Manual timeline entries are always `actor_kind='unattributed'` — the timeline cannot
   say who logged a call (`queries/timeline.sql`). The 09-12 inventory spec already calls
   this "a gap to close".
2. List search is `ILIKE` on `name` only — not customer number, org number, legal name or
   contact email. No filter on status/type, sort only by id/name.
3. Archived customers are unreachable from the UI (`includeArchived` never sent), no
   un-archive action.
4. `status='disabled'` is stored but nothing behaves differently because of it.
5. No concurrency token on the customer row (last writer wins) while the timeline has three.
6. No duplicate check on create — two customers with the same `legal_id` are accepted; no
   merge.
7. Brreg lookup returns only org number + name, one-shot: no address, NACE, org form,
   bankruptcy flags, no re-sync. Editing the name afterwards silently drops the identity.
8. `legal_country` is not ISO-checked, org number has no mod-11 check, `legal_type` is free.
9. `/stats/attention` is a permanently empty endpoint; `registry.change` is an event type
   nobody produces.
10. `CustomerDirectory` exposes `{ID, Name, Archived}` only, has no batch lookup (projects
    list does one call per distinct customer), and `ContactsByEmail` has no production caller.
11. No other module writes to the customer timeline; time and expenses have no customer
    coupling at all (only via the project's `customer_id`).
12. No `docs/customers.md`; `docs/customers-authentication.md` is the platform identity doc,
    misnamed.

## 2. What comparable systems do

### Nordic ERP/accounting (Tripletex, PowerOffice Go, Visma eAccounting/Visma.net, Fiken, Fortnox, e-conomic, 24SevenOffice) and CRM (SuperOffice, Lime)

**Table stakes in Norway** — present in essentially every system:

- org number with registry lookup on create; company/person distinction
- **invoice delivery method** (EHF / email / eFaktura / paper …) with **automatic
  Peppol capability lookup** (Fiken, Tripletex, PowerOffice, Visma all switch to EHF on
  their own; only Fortnox asks the user)
- **invoice email separate from contact email** (Fortnox has nine such fields; PowerOffice
  has a separate channel per document type: invoice, reminder, inkassovarsel)
- **payment terms** (days), **currency**, **document language** (NO/EN)
- at least **invoice address + delivery address** (Tripletex 3, 24SO 4, Visma.net unlimited
  locations)
- customer groups/categories; fixed discount %; contact persons; inactive/archive
- CSV/Excel import; customer ledger (reskontro) view; dunning switchable per customer
- one record carrying both customer and supplier role (Tripletex, PowerOffice, Fiken, 24SO)

**Differentiators** — held by one or two systems:

- **duplicate detection and merge** — SuperOffice only; Tripletex states customers
  *cannot* be merged. Clearest open gap in the Norwegian field.
- **registry auto-refresh** — PowerOffice `KeepUpdated` (**UNCERTAIN** semantics), Lime
  add-on. Nobody else documents it.
- **GDPR anonymisation that survives the bookkeeping retention period** — Visma
  eAccounting, e-conomic, Lime. Missing in Tripletex, PowerOffice, Fiken.
- inline credit check stored on the customer (Fiken, Lime, Tripletex)
- customer-level "deres/vår referanse" defaults (Fortnox), per-customer department/project
  defaults (PowerOffice, Fortnox, Visma.net)
- custom fields (Visma.net, the CRMs — nobody else), parent/group company (Visma.net)
- notes/activity timeline/tasks — deep only in the CRMs; Tripletex has one `description`
  field. **Vantigo is already ahead of the accounting systems here.**

**Norwegian specifics that shape the data model:**

- **Peppol lookup**: ELMA's open datasets are dead (stopped updating 2024-09-23); the valid
  lookup is the Peppol way — SML DNS lookup of participant `0192:<orgnr>` → SMP → check
  Invoice/CreditNote BIS Billing 3.0 support.
- **B2G invoices must be EHF** (forskrift 2019-04-01-444 §4; payment may be withheld).
  **B2B e-invoicing mandate announced for 2027-01-01** (**UNCERTAIN**: dates from advisory
  firms' summaries, not a published regulation).
- Reminders cannot be sent as EHF or eFaktura → the dunning channel must be modelled
  separately from the invoice channel.
- eFaktura 2.0 addresses consumers by **mobile number and/or email** — a person customer
  needs a reliable mobile number.
- A separate VAT number field is unnecessary in Norway (buyer needs name + address *or* org
  number; "MVA" suffix is a seller obligation). It is needed for foreign customers.
- **Brreg open API** exposes much more than we take: `organisasjonsform`, `næringskode`,
  `antallAnsatte`, `forretningsadresse` and `postadresse`, `konkurs`, `underAvvikling`,
  `underTvangsavviklingEllerTvangsopplosning`, `registrertIMvaregisteret`, `hjemmeside`,
  `overordnetEnhet`, underenheter — plus **incremental update feeds**
  (`/api/oppdateringer/enheter`) to build a refresh on.
- Datatilsynet: **never store fødselsnummer** for ordinary customer administration.
  Bookkeeping retention forces keeping invoiced customers → the answer is scheduled
  *anonymisation*, not deletion.

### International CRM/PSA (HubSpot, Pipedrive, Salesforce, Zoho, Attio, Twenty; Odoo, ERPNext, Business Central; Productive, Scoro, Harvest, Teamleader)

- Every ERP/PSA customer card carries **financial defaults** that cascade to documents:
  currency, payment terms, tax treatment, language, billing address, default invoice
  recipients, e-invoice ID + buyer reference (Productive), price list.
- PSA tools put **rate cards at the client level** (Productive, Scoro) with project
  override. Harvest keeps rates on the project only.
- **Typed contact roles** on the contact↔company link with a primary flag (Salesforce
  `AccountContactRelation`: roles, `IsDirect`, active + dates; Teamleader decision-maker flag).
- **Single owner/account manager** is universal; account *teams* are Salesforce-only complexity.
- Segmentation: flat tags almost everywhere; ERPNext's **Customer Group carries defaults**
  (price list, credit limit); Business Central has customer templates.
- ERPs don't do lifecycle funnels; they do **Blocked** (BC: block Ship/Invoice/All) and
  enabled/disabled.
- **Customer 360** page in PSA: projects, budgets, invoices, people, time, rate cards,
  activity (Productive).
- Duplicate merge is treated as infrastructure (HubSpot, Pipedrive, Attio, BC).
- Attio's enrichment pattern: registry-sourced values are read-only with provenance — fits
  `legal_source='brreg'`.
- Multiple typed addresses: Odoo (invoice/delivery/follow-up/other), Productive (labelled
  emails/addresses with one marked as invoicing default — the lightest version).

**Traps for a light ERP/PSA**: full email/calendar sync, marketing automation and consent
centres, lead/health scoring, account teams with per-record access, territories and deep
hierarchies, third-party enrichment, a deals pipeline smuggled into Customers (it is its
own module), a customer portal before Invoices exists, credit limits before receivables exist.

## 3. Priorities

Guiding idea: Vantigo's customer is the hub that projects, time, expenses and soon invoices
hang off — not a sales funnel. The next module on the roadmap is Invoices, and today a
customer cannot be invoiced: there is nowhere to send it and no terms to put on it.

### P0 — Close the foundation gaps (small; before anything is built on top)

1. Attribute manual timeline entries to the calling user.
2. Search by customer number, org number, legal name and contact name/email; filter by
   status/type; sortable columns; show archived + un-archive.
3. Concurrency token on the customer row.
4. Duplicate guard on create (same `legal_id` → conflict with a link to the existing one;
   similar name → warning).
5. Validate org number (mod 11) and country (ISO 3166-1); decide `disabled` — either give it
   Business Central's meaning ("blocked": no new projects/invoices) or remove it.

### P1 — The invoice-ready customer (blocks Invoices; table stakes in Norway)

6. **Addresses**: typed (invoice/postal, delivery, visiting), filled from Brreg's
   `postadresse`/`forretningsadresse`.
7. **Customer-level contact info**: email, phone, website; **invoice email** and
   **reminder email** separate from it.
8. **Billing profile**: payment terms (days), currency, document language, invoice delivery
   method, reminder delivery method, Peppol participant ID / GLN, default buyer reference
   ("deres referanse") and whether a PO/reference is required (B2G).
9. **Peppol capability lookup** (SML/SMP) that sets delivery method to EHF automatically.
10. **`CustomerDirectory` grows**: a billing-profile read for Invoices, a batch `Customers(ids)`
    (fixes projects' N+1), drop or use `ContactsByEmail`.

### P2 — Brreg done properly (cheap, differentiating, fills two existing stubs)

11. Take the full Brreg record: org form, NACE, employees, addresses, MVA-registered,
    website, bankruptcy/dissolution flags, parent entity. Registry-sourced fields read-only
    with provenance; manual override explicit.
12. "Refresh from Brreg" button, then a scheduled refresh from the update feed. Changes are
    written as `registry.change` timeline events (the type already exists), and
    bankruptcy/dissolution/name change lands in `/stats/attention` (the endpoint already
    exists, empty).

### P3 — Light CRM: who owns the relationship and what happens next

13. **Owner / account manager** (single user) + "my customers" filter.
14. **Tags**, then **customer groups** that can carry defaults (payment terms, later the
    customer-group prices Products phase 4 already plans for).
15. **Typed contact roles + primary contact** (billing, project, decision maker) replacing
    free-text role — answers "who gets the invoice" and "who approves".
16. **Follow-ups**: a timeline entry can carry a follow-up date and assignee; due follow-ups
    feed `/stats/attention` and a "my follow-ups" view. No task engine beyond that.
17. **Attachments** on customer and timeline entries (contracts, NDAs) via the storage module.

### P4 — Customer 360 (the reason the customer is the hub)

18. Overview panel per customer: open projects, unbilled hours and expenses, invoiced
    revenue and outstanding (once Invoices exists), last activity. Host-composed from module
    contracts like the existing tabs.
19. Other modules write customer timeline events (project created/closed, invoice sent,
    supply period started) — rides on the domain-events outbox deferred until Orders.
20. **Customer default bill rate / rate card**, slotted into the existing chain
    (billing line → project → **customer** → person). Lives in Projects/Time but is keyed by customer.

### P5 — Data operations and compliance

21. CSV import (create + update, error-row re-run) and export — onboarding from
    Tripletex/Fiken/PowerOffice.
22. **Merge duplicates** (moves contacts, timeline, and re-points projects/energy/
    communications through a contract). Far cheaper before invoices reference customers;
    a differentiator in Norway.
23. **GDPR for person customers**: data export, scheduled anonymisation that leaves
    bookkeeping intact, never a fødselsnummer field.
24. `docs/customers.md` and a Customers section in `ROADMAP.md`; rename
    `docs/customers-authentication.md`.

### Later / on proven demand

Parent company (`parent_id`, seedable from Brreg `overordnetEnhet`) · customer-is-also-supplier
(when purchasing/supplier invoices arrive) · custom fields · credit check integration
(Proff/Creditsafe) · credit limit + credit hold (needs receivables) · per-customer dunning
settings (belongs with Invoices' dunning) · customer portal (after Invoices) · saved views.

### Not recommended

Sales pipeline/quotes inside Customers (own module if ever) · email/calendar sync ·
marketing automation and consent centre · lead/health scoring · account teams · territories.

## 4. Sources

Nordic: `apps.fortnox.se/apidocs` · `api.fiken.no/api/v2/docs/swagger.yaml` ·
`restdocs.e-conomic.com` · `eaccountingapi.vismaonline.com/openapi/v2.json` ·
`integration.visma.net/API-index` · `api.24sevenoffice.com` WSDL ·
`developer.tripletex.no` + `github.com/Tripletex/tripletex-api2` changelog ·
`developer.poweroffice.net` · `docs.superoffice.com` · `docs.lime-crm.com` · help centres
of the same. Norway: `docs.digdir.no/docs/ELMA/elma_open_data` ·
`lovdata.no/dokument/SF/forskrift/2019-04-01-444` ·
`data.brreg.no/enhetsregisteret/api/dokumentasjon` · `datatilsynet.no` · `bits.no/forbedret-efaktura`.

International: HubSpot Companies API and duplicate management · Pipedrive Organizations API ·
Salesforce Help (AccountContactRelation, Account Teams) · Zoho CRM help · `docs.attio.com` ·
`docs.twenty.com` · Odoo Contacts docs and `res_partner.py` · ERPNext Customer / Customer
Group / Credit Limit · Business Central customer card · Productive company pages and rate
cards · Harvest clients · Scoro price lists · Teamleader `companies.info`.

**UNCERTAIN**: Tripletex and PowerOffice v2 field lists are spec snapshots plus changelogs,
not the full live DTOs; Salesforce field details come from Help/secondary sources (developer
docs blocked fetches); Scoro/Teamleader client-card fields from search snippets; the 2027
B2B e-invoicing date.
