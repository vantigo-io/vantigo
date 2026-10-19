# Roadmap

Planned evolution per module. Each phase notes why it exists and what it unblocks.
Other modules add their own sections as their roadmaps solidify.

## Platform

### Time zones (done)

Decided in
[`docs/superpowers/specs/2026-10-19-time-zones-design.md`](docs/superpowers/specs/2026-10-19-time-zones-design.md),
researched for the calendar module in
[`docs/superpowers/research/2026-10-19-calendar-module.md`](docs/superpowers/research/2026-10-19-calendar-module.md).
Two zones with two owners, and no stored row touched: the **business time zone**, one
per installation on identity's system settings, read by every module through
`contracts.BusinessCalendar` (`Deps.Calendar`, always set, in worker mode too) and set
by an Owner under Workspace administration, audited
`workspace.business_time_zone_changed`; and a **personal time zone** per user that
follows the business zone until the person chooses one, which the session answers.

**Delivery A (done)** — the contract and `holidays.DayIn`, identity's implementation
and owner endpoint, the session's `timeZone`/`timeZoneSource`/`businessTimeZone`,
migrations `00052`–`00054` (the two columns; Staff's and Expenses' own zone columns
dropped, with a warning in the migration log when either was not `Europe/Oslo`),
Staff and Expenses on the contract with `timeZone` answered read-only, the Workspace
card.

**Delivery B (done)** — Invoices off its hard-coded Oslo: every "today" and every day
derived from an instant — issue and due dates, states, the overdue list and reminders,
payments, bank files, Vipps' `paid_on`, a milestone's day, the customer receivables —
is the business day, the zone read through `Deps.Calendar` once per request or worker
claim and before any transaction; the contract's descriptions say so.

**Delivery C (done)** — Projects off its hard-coded Oslo and UTC days: the billable
milestones' `Until` buckets `ready_at` in the business zone, handed to the SQL as a
parameter, and the dashboard's "today", its attention items and a milestone's
`overdue` flag judge the business day; the contract's descriptions say so.

**Delivery D (done)** — Time off the UTC day: "today", the day the current week is read
from for the dashboard's hours this week, its unsubmitted-week items and the people
overview, is the business day; an entry's start and end times are documented as
wall-clock times in the business zone, with no column changed; the contract's
descriptions say so.

**Delivery E (done)** — Customers off the UTC day: a follow-up's overdue/due-today
split, on the attention list and behind `state=overdue`, the bound on an entry's
`occurredOn`, the earliest anonymisation day and the worker's due day, and the export
file's name judge the business day; an entry's `occurredAt` must fall on its
`occurredOn` in the business zone, checked on writes and, on a `PUT`, only when the
pair changes; the timeline shows an entry's time without the literal "UTC"; the
contract's descriptions say so.

**Delivery F (done)** — the personal zone: `timeZone` on the profile's `PUT` and
`PATCH`, held to the workspace zone's two checks, `null` to follow the workspace again
and kept when left out, answered by `GET /account`; a **Time zone** select under
Personal settings, "Same as the workspace (…)" first; the shell's formatters showing
every instant in the session's effective zone, with `useI18n().timeZone`,
`businessTimeZone` and helpers for "today in a zone" and the instant a typed time
means; Customers' "today" on the business day and its timeline form's time read in the
writer's zone; Time's "today" on the business day; the Inbox's times in the viewer's
zone. SCIM's `timezone` attribute, which the design left optional, is not mapped.

Nothing is left to land. The [calendar module](#calendar), which depends on A and F, has
started on them: its delivery A reads every window in the viewer's zone and stores the
zone each appointment was made in.

### Cross-module domain events (deferred until Orders)

Synchronous cross-module queries use in-process contracts from
`internal/contracts` (e.g. `contracts.CustomerDirectory`) and need nothing more.
For asynchronous "something happened" notifications the decided pattern is
**in-process domain events dispatched through a transactional outbox**: the
publishing module writes the event in the same transaction as its state change,
and a background worker dispatches to handlers with retries (the Communications
outbox worker already proves the pattern). **No message broker** — Postgres is
the queue, and every infrastructure piece multiplies per dedicated customer
deployment. Build the event bus together with its first real consumer, most
likely Orders (`OrderPlaced` → Communications sends confirmation, Warehouse
reserves stock). If a module is ever extracted, the outbox dispatcher targets a
transport instead of in-process handlers; event contracts stay unchanged.

## Customers

Customers is the hub Energy, Projects, Time, Expenses and Invoices all hang off — a
project has a `customerId`, a supply period has a customer, an invoice snapshots its
buyer from one — not a sales pipeline of its own. Research and priorities are in
[`docs/superpowers/research/2026-09-21-customers-module-next.md`](docs/superpowers/research/2026-09-21-customers-module-next.md),
comparing this module against the Nordic ERP/accounting field (Tripletex, PowerOffice,
Visma, Fiken, Fortnox, e-conomic) and international CRM/PSA tools (HubSpot, Pipedrive,
Attio, Odoo, Business Central, Productive).

### Phase 1 — Foundation (done)

Decided in
[`docs/superpowers/specs/2026-09-21-customers-foundation-design.md`](docs/superpowers/specs/2026-09-21-customers-foundation-design.md).
Closed the gaps that would otherwise be built upon, adding no new customer data: timeline
entries — manual ones, each of their revisions, and the generated events a user's action
causes — now name who wrote them, snapshotted from the user directory at write time; the customer row gained `revision`, an optimistic-concurrency
token an update or a type change may carry and get refused (409) against; a legal
identity another customer already has — archived included — is a 409 a caller can
overrule with `allowDuplicateIdentity: true`, since there is no unique index; legal
identities are now validated where a rule actually exists (ISO 3166-1 alpha-2 for
`country`, Norwegian organisasjonsnummer mod-11 for `no` + `business`, a Norwegian
person's id deliberately left unvalidated pending the GDPR question in phase 6); and
the customer list now searches by customer number and — permission-gated, so search is
never an oracle for data the response would withhold — legal name/id and contact
name/email, filters by status and type, sorts by more than id/name, and can show
archived customers with a restore path. `disabled` stays a label that blocks nothing
yet, on purpose: Projects already accepts a project against an archived customer, so
inventing a blocking rule for `disabled` ahead of Invoices would contradict that
standing decision. See [`docs/src/content/docs/en/reference/customers.md`](docs/src/content/docs/en/reference/customers.md).

*Unblocks:* every later phase below, and a customer list, duplicate guard and timeline
worth building the invoice-ready customer on top of.

### Phase 2 — The invoice-ready customer (align with Invoices) (done)

Decided in
[`docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md`](docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md),
split into two deliveries.

**Delivery A (done)** — the data, its API, the UI and the directory. Addresses
(postal, invoice, delivery, visiting — table stakes in every Nordic system
compared), any number of each, one primary per type. Customer-level contact info:
email, phone, website, on the customer itself. A billing profile behind its own
permission (`customers:billing-manage`, deliberately narrower than
`customers:update`): payment terms (days), currency, document language, invoice
delivery method, reminder delivery method, a Peppol participant id/GLN, and a
default buyer reference ("deres referanse") — with an **invoice email** and a
**reminder email** kept separate from the general contact address, since Norwegian
eFaktura/EHF and dunning correspondence route differently. `contracts.CustomerDirectory`
grew a `BillingProfile` read (every field resolved once, for Invoices) and a batch
`Customers(ids)` lookup — Projects' project list now makes one directory call per
page instead of one per distinct customer. See [`docs/src/content/docs/en/reference/customers.md`](docs/src/content/docs/en/reference/customers.md).

**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-21-customers-peppol-lookup-design.md`](docs/superpowers/specs/2026-09-21-customers-peppol-lookup-design.md):
a Peppol capability lookup (SML DNS → SMP → BIS Billing 3.0 support), asked on a
person's click (`POST .../peppol-lookup`) and remembered on its own table, off the
customer row — Phase 3 delivery B below later added a worker that asks again on a
schedule, through the same storage, never through this endpoint. It deliberately
does **not** set the invoice delivery method to EHF automatically the way every
Nordic competitor surveyed but Fortnox does — the
billing card offers **Use EHF** instead of switching silently, so nobody's billing
decision changes without a click. Delivery A did not wait for it: `peppolId` and
`invoiceDelivery` were, and remain, plain fields a person can fill in by hand, and
the billing profile's `ehf_without_recipient` warning already flagged a customer
set to `ehf` with no Peppol id to send to; two new warnings
(`ehf_recipient_not_registered`, `ehf_available`) now read the stored lookup
answer too. See [`docs/src/content/docs/en/reference/customers.md`](docs/src/content/docs/en/reference/customers.md#peppol-lookup).

*Unblocks:* Invoices can now read a resolved billing profile through the
directory — delivery A gave a customer somewhere to send an invoice and terms to
put on it; delivery B tells a person, with one click, whether EHF will actually
reach that customer.

### Phase 3 — Brreg in full (done)

Decided in
[`docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md`](docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md),
split into two deliveries.

**Delivery A (done)** — the fuller Brreg record: org form, NACE code, employee
count, addresses, MVA-registration, website, bankruptcy/dissolution flags, parent
entity, instead of the earlier `legalId`/`legalName` pair alone — kept beside the
customer (`customers.customer_registry_records`), never on it, so a fetch never
bumps `revision`. Fetched on a Brreg pick (create or a Brreg-sourced legal-identity
PUT) and re-read on a **Refresh** click (`POST .../registry-refresh`); a struck-off
entity (`SlettetEnhet`) and one removed from open data (410) are both real answers,
not failures, the second deleting the copy on file. What differs from the record on
file is written as a `registry.change` timeline event — the event type existed,
unused, since the port, and now has its first producer (`customers.brreg`) — and
bankruptcy/dissolution/deletion/a name change surface on `/stats/attention`, also no
longer a stub. Addresses from the record are offered to the address book, never
written to it. See [`docs/src/content/docs/en/reference/customers.md`](docs/src/content/docs/en/reference/customers.md#registry-record).

**Delivery B (done)** — a scheduled refresh from Brreg's incremental update feed
(`GET /enhetsregisteret/api/oppdateringer/enheter`, exact cursor on `oppdateringsid`, one unfiltered scan
matched against this installation's customers locally), driving the same
fetch-and-store delivery A built and filling `registry_updated_hint`, the column
delivery A's own migration already carried but left untouched. The same cycle sweeps:
records whose last refresh failed (`hint > fetched_at` is the whole retry mechanism)
and Norwegian business customers that never had a record at all, so an installation
that predates delivery A catches up on its own. Beside it, **scheduled re-checks of
Peppol registration**: the click (design D4 of the Peppol delivery) is no longer the
only thing that asks — an aged answer, and a customer already set to `ehf` that was
never checked, are asked again on a schedule, surfacing on the same
`ehf_available`/`ehf_recipient_not_registered` warnings a manual check raises, and
still never switching a customer's delivery method. Both workers elect one replica
per cycle through a Postgres advisory lease and are configured per installation
(`CUSTOMERS_REGISTRY_FEED_*`, `CUSTOMERS_PEPPOL_RECHECK_*`). See
[`docs/src/content/docs/en/reference/customers.md`](docs/src/content/docs/en/reference/customers.md#registry-workers).

*Delivered:* registry data worth relying on instead of a name and a number typed
once, and more behind the one endpoint (`/stats/attention`) and the one event type
(`registry.change`) this module already declared.

### Phase 4 — Light CRM (five deliveries done)

An owner/account manager (single user) and a "my customers" filter. Tags, then
customer groups that can carry defaults (payment terms, later the customer-group
prices Products phase 4 already plans for). Typed contact roles with a primary
contact (billing, project, decision maker), replacing today's free-text `role` —
answers "who gets the invoice" and "who approves", the way Business Central's and
Salesforce's contact-role models do. Follow-ups: a timeline entry that can carry a
follow-up date and assignee, feeding `/stats/attention` and a "my follow-ups" view —
no task engine beyond that. Attachments on a customer and its timeline entries
(contracts, NDAs), once the storage module has a model for it.

**Delivery A (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-owner-tags-design.md`](docs/superpowers/specs/2026-09-23-customers-owner-tags-design.md):
one owner per customer (`owner_user_id`, a single user of this installation, named
through `contracts.UserDirectory` and never stored as a foreign key — a disabled or
removed account keeps the customer), filterable on the list as `me` (resolved from
the session) or `none` (unassigned); a tag vocabulary whose name is unique
case-insensitively, so `VIP` and `vip` are the same word, with a customer's tags
replaced as a set rather than linked one at a time. Both ride on the existing
`customers:view`/`customers:update` split — no new permission key. Two generated
timeline events, `customer.owner_changed` and `customer.tags_changed`, each
recorded only when the value actually changed. See
[`docs/src/content/docs/en/reference/customers.md#owner-and-tags`](docs/src/content/docs/en/reference/customers.md#owner-and-tags).

**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md`](docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md):
the association's free-text `role` is a `title` and stays one (migration `00025`
renames the column; the wire answered `role` for one delivery longer, until
delivery C below removed the alias while nothing was live), and three typed
roles — `billing`,
`project`, `decision_maker` — live in `customers.customer_contact_roles` with
exactly one primary contact per role, on the addresses' own invariant: the first
holder is primary whatever the request said, `primary: true` demotes the
incumbent, clearing the only or primary holder's flag is refused, and losing a
role promotes the longest-standing remaining holder. The roles ride on the
association's four existing endpoints and its two existing permissions — no new
paths, no new key — and a promotion caused by somebody else's write is recorded
on the promoted contact with the user who caused it. See
[`docs/src/content/docs/en/reference/customers.md#contacts-and-associations`](docs/src/content/docs/en/reference/customers.md#contacts-and-associations).

**Delivery C (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-follow-ups-design.md`](docs/superpowers/specs/2026-09-23-customers-follow-ups-design.md):
a manual timeline entry carries a **follow-up** — a due date that may be in the
future and an optional assignee, on the entry itself (migration `00026`) and on
its revisions, so history stays point-in-time. Ticking it done and reopening it
are two paths that take no `expectedRevision`, because a tick comes from a list
and must not lose a race with an edit of the note; both are idempotent and each
real change is still a revision naming who ticked it. Due and overdue follow-ups
reach `/stats/attention` — the first items there that depend on who is asking,
reporting the caller's and unassigned ones only — and `GET /customers/follow-ups`
answers a **Follow-ups** page, defaulting to "my open ones". The timeline card
gains the section, the line and the tick, and a new `canManageTimeline`
capability prop stops a reader seeing controls that used to 403. The same
delivery removed the contact association's deprecated `role` alias while nothing
was live (`title` is the only name the contract has) and relaxed
`GET /customers/assignable-users` to `customers:view`. See
[`docs/src/content/docs/en/reference/customers.md#follow-ups`](docs/src/content/docs/en/reference/customers.md#follow-ups).

**Delivery D (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-groups-design.md`](docs/superpowers/specs/2026-09-23-customers-groups-design.md):
**customer groups that carry defaults** (migration `00027`). The vocabulary is
the tags' — unique on `lower(name)`, unpaged, with a member count — and the
membership is the owner's: one nullable column on the customer row, sharing its
revision, written only through `PUT /customers/{id}/group` and recorded as
`customer.group_changed` with the group names as they read at the time. A group's
`defaultPaymentTermsDays` is the **third resolution tier** for a customer's
payment term (own value, else the group's, else nothing), applied in the one
place resolution lives, and the billing profile answers `groupDefault` so a card
can say where an inherited term comes from. A group with members
is never deleted — 409 `group_in_use`, with the count, and `ON DELETE RESTRICT`
under it — because detaching them would change every member's effective payment
term with no record on any customer. No new permission key, and
`contracts.CustomerEntry.Group` is the seam Products phase 4's customer-group
prices will read. See [`docs/src/content/docs/en/reference/customers.md#groups`](docs/src/content/docs/en/reference/customers.md#groups).

**Delivery E (done)** — decided in
[`docs/superpowers/specs/2026-10-12-customers-attachments-design.md`](docs/superpowers/specs/2026-10-12-customers-attachments-design.md):
attachments on a customer and on its manual timeline entries — a contract, an NDA, the
correspondence behind an entry — PDF, JPEG, PNG or HEIC, 10 MiB, two hundred a customer,
Staff's bounds and sniffing, in the object store under the `customers` scope keyed
`attachments/<uuid>` so a merge moves the row and never the object. On the timeline's
two keys (`customers:timeline-view` reads, `customers:timeline-manage` writes), no new
permission; `customer.attachment_added` and `customer.attachment_removed` on the
timeline; moved by a merge as `customers.attachments`, named by the personal-data export
and deleted by the anonymisation through a deletion ledger the anonymisation worker
finishes, so an erase is never best-effort. An **Attachments** card on the customer page
and **Attach file…** on a manual entry. See
[`docs/src/content/docs/en/reference/customers.md#attachments`](docs/src/content/docs/en/reference/customers.md#attachments).

**Left for later on purpose** in this phase: the tag vocabulary is **unpaged**
(`GET /customers/tags` answers all of it, and both the
picker and the Manage tags modal want the whole list), which is a bet that a
vocabulary stays in the tens or low hundreds — paging it is an additive contract
change the day an installation proves otherwise; the same bet is made for the
role vocabulary, which is deliberately **three** values — a wider list
(technical, executive sponsor) is a value change rather than a migration, and
the free-text title carries everything else today. And follow-up
**reassignment**: an assignee who is disabled or removed keeps their follow-ups,
which then sit on nobody's attention list and behind nobody's page filter
(`GET /customers/follow-ups?assignee=<uuid>` is the only way to them), so this
phase's own leftover is a bucket for an inactive assignee's follow-ups, or a
reassign path — the day an installation has enough of them for that to be worth
a path rather than an edit of each entry. Groups have their own: no **bulk
move** (a group's members are moved one at a time, which is why the delete is
refused rather than cascading), no group-level prices until Products phase 4
reads the seam, no default beyond payment terms, and the vocabulary is unpaged
on the same bet the tags' is.

*Unblocks:* answering "who owns this relationship and what happens next" without
building a deals pipeline.

### Phase 5 — Customer 360 (three deliveries done)

An overview panel per customer — open projects, unbilled hours and expenses, invoiced
revenue and outstanding once Invoices exists, last activity — host-composed from
module contracts the same way the Energy and Projects tabs already are. Other
modules writing to the customer timeline (project created/closed, invoice sent, supply
period started), riding on the domain-events outbox deferred until Orders (see
[Platform](#platform)). A customer default bill rate, slotted into the chain Projects
and Time already resolve rates through (billing line → project → **customer** →
person).

**Delivery A (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-360-design.md`](docs/superpowers/specs/2026-09-23-customers-360-design.md):
the overview panel. Not host-composed from module endpoints after all — none takes a
customer id for hours or expenses — but **one endpoint on Customers**,
`GET /customers/{id}/overview`, composed in Go from `ProjectDirectory`,
`ProjectActuals` and `ProjectExpenses`, with a host-owned panel rendering it. Open
projects, unbilled work (approved less invoiced, through the new
`ActualsTotals.Invoiced`), expenses ready to invoice and last activity, each section
shaped by the projects module's keys and absent rather than refused; money per
currency and only for financial rights. See
[`docs/src/content/docs/en/reference/customers.md#customer-360`](docs/src/content/docs/en/reference/customers.md#customer-360).

**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md`](docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md):
the customer default bill rate, the billing profile's eleventh field, quoted in the
profile's own currency, and the customer step of Time's rate chain between the
project default and the person card — the person card's currency rule, nothing
converted. See [`docs/src/content/docs/en/reference/time.md#the-rate-chain`](docs/src/content/docs/en/reference/time.md#the-rate-chain).

**Delivery C (done)** — decided in
[`docs/superpowers/specs/2026-10-12-customers-receivables-design.md`](docs/superpowers/specs/2026-10-12-customers-receivables-design.md):
receivables on the overview, now that Invoices exists through its phase 4B. A new
single-provider contract, `contracts.CustomerReceivables`, implemented by Invoices
from its own state derivation on the Oslo day of its clock — what the customer was
invoiced this year and last (net of the credit notes issued in each year), what
stands outstanding and overdue at the open amounts, charges never included, and the
outstanding invoices on hold or at collection; the overview's `invoices` section,
shaped by `invoices:access` alone and zero rather than absent for a customer with no
documents; two tiles and a **See invoices** link on the panel, and two more dates in
`lastActivity`. No new permission key. See
[`docs/src/content/docs/en/reference/customers.md#customer-360`](docs/src/content/docs/en/reference/customers.md#customer-360).

**Still ahead in this phase:** other modules writing to the customer timeline, riding
on the outbox deferred until Orders.

*Unblocks:* the reason the customer page is meant to be the hub, not just a card.

### Phase 6 — Data operations and compliance (done)

CSV import (create and update, with error-row re-run) and export, for onboarding away
from Tripletex/Fiken/PowerOffice. Merging duplicate customers — moving contacts,
timeline entries, and re-pointing whatever other modules hold a customer id, through a
contract — far cheaper to build now, before invoices reference customers, and a
differentiator in the Norwegian field: of the systems compared only SuperOffice documents
a merge, and Tripletex states customers cannot be merged at all. GDPR handling for person customers: data export and scheduled
anonymisation that leaves bookkeeping retention intact — and, as phase 1 already
insists, never a fødselsnummer field.

**Delivery A (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-import-export-design.md`](docs/superpowers/specs/2026-09-24-customers-import-export-design.md):
CSV export of the list as the caller sees it and CSV import that creates and updates
through the endpoints' own write paths, with a dry run and a failed-rows file for the
re-run; one canonical format, no import key. Its import was replaced by delivery A2.

**Delivery A2 (done)** — decided in
[`docs/superpowers/specs/2026-10-13-customers-import-wizard-design.md`](docs/superpowers/specs/2026-10-13-customers-import-wizard-design.md):
importing customers from any file — its own headers, separator and encoding, up to
50 MB and 100 000 rows — with the columns mapped to fields on screen and a preview,
fixed values for every row, create, update or both by customer number or
organisation number, and the `customers-import` worker checking and importing it in
the background with progress, stop and resume; behind the new `customers:import`.
See
[`docs/src/content/docs/en/reference/customers.md#export-and-import-from-a-file`](docs/src/content/docs/en/reference/customers.md#export-and-import-from-a-file).

**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-merge-design.md`](docs/superpowers/specs/2026-09-24-customers-merge-design.md):
`POST /customers/{id}/merge` — one customer absorbs its duplicate in one transaction:
contacts (roles unioned), addresses, timeline, tags, and every other module's
references through `contracts.CustomerReferenceHolder` (projects, energy,
communications); the survivor keeps every field of its own and the duplicate is
archived with a marker; behind the new `customers:merge`. See
[`docs/src/content/docs/en/reference/customers.md#merging-duplicates`](docs/src/content/docs/en/reference/customers.md#merging-duplicates).

**Delivery C (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md`](docs/superpowers/specs/2026-09-24-customers-gdpr-design.md):
a Norwegian national identity number is refused as a person's legal id; a private
person's data is exported in one file; and an archived private person is anonymised on a
chosen day by a worker — the number, the dates and the shape of the history kept for
bookkeeping, the person taken out of the customer, its timeline and other modules
through `contracts.CustomerPersonalData` (communications, energy, projects); behind the
new `customers:personal-data`. See
[`docs/src/content/docs/en/reference/customers.md#personal-data-and-anonymisation`](docs/src/content/docs/en/reference/customers.md#personal-data-and-anonymisation).

Phase 6 is complete, and with it the Customers roadmap — this was its last delivery;
phase 5's delivery C landed after it, once Invoices existed, and phase 4's delivery E
once the storage port had a model to copy. One thing is still deferred, waiting on
another module rather than on Customers: the other modules' timeline writers (phase
5), which ride on the domain-events outbox deferred until Orders.

*Unblocks:* clean onboarding and offboarding, and a merge path that only gets more
expensive the longer it waits.

### Later

Parent company (`parentId`, seedable from Brreg's `overordnetEnhet`) · customer-is-also-supplier
(once purchasing and a supplier register arrive — a supplier invoice today names
its supplier in free text) · custom fields · credit check integration
(Proff/Creditsafe) · credit limit and credit hold (its prerequisite, receivables per
customer, landed with phase 5's delivery C) · whether a buyer is bokføringspliktig,
the fact the 2028 buyer org-number rule needs (bokføringsforskriften § 5-1-2 from
2028-01-01; scheduled under [Invoices](#invoices), which reads it from here) · a
customer portal (after Invoices) · saved list views. A customer's reminder policy —
the per-customer dunning setting once listed here — landed in Invoices phase 4B, on
the customer page's Invoices tab.

**Deliberately not planned inside Customers:** a sales pipeline or quotes (its own
module, if ever), email/calendar sync, marketing automation and a consent centre,
lead/health scoring, account teams, and territories. The Norwegian B2B e-invoicing
duty is now law (Lov 19. juni 2026 nr. 39: sending in electronic invoice format between
bokføringspliktige from 2027-01-01, the format regulation — expected to name EHF — due
by December 2026); it is scheduled against under [Invoices](#invoices), not here.

## Energy

### Phase 1 — Core metering model (done)

Metering points (målepunkt) keyed by the 18-digit GSRN with Nordic-compatible
attributes (price area, grid area, address, coordinates, expected annual
consumption), consumption as revisioned kWh intervals (corrections supersede,
never overwrite; arbitrary interval length supports PT1H today and PT15M later),
and supply periods linking customers to metering points over half-open time
ranges (overlap-proof via a Postgres exclusion constraint). Consumption access
is authorized through supply periods, so a customer never sees readings outside
their own ownership window.

*Unblocks:* registering meters, manual readings, and the privacy model required
by the Norwegian market (and GDPR) before any Elhub data flows.

### Phase 2 — Market operations (done)

Physical meters split from metering points (installation history + swap
endpoint, since AMS meters get replaced while the målepunkt persists),
server-side consumption aggregation (hour/day/month) bucketed in the metering
point's market timezone derived from its price area (NO/SE/DK → CET, FI → EET,
DST-correct), and atomic customer switch (move-in/move-out in one transaction
producing contiguous supply periods).

*Unblocks:* correct daily/monthly figures for charts and invoicing, meter swap
workflows, and leverandørbytte-style customer changes.

### Phase 3 — Customer context (done)

Customer detail page gets an Energy tab (host-composed, reusing the energy
module's meter listing) so day-to-day work happens in the customer's context:
consumption stats over the last twelve months, the customer's metering points
with attach/detach, all behind the deployment's enabled modules and the caller's
permissions (the tab row follows the same visibility rules as the sidebar).
Metering points are also searchable from the global spotlight by GSRN, meter
number, or address.

*Unblocks:* the composition pattern future modules (Invoices) reuse to extend
the customer page.

**Delivery: import from a file (done)** — moving to Vantigo from another system without
retyping it. **Metering points from a file**: a supplier's export uploaded, its columns
mapped (suggested from the export's own words), checked as a dry run and imported by
the `energy-import` worker; each point is linked to its customer by organisation
number through the customer directory, a customer switch is shown and must be
confirmed by count, and the points the file gave no start date are attached afterwards
or found again with the list's **Without customer** filter. **Consumption per metering
point**: **Import readings** on the point's page takes a file of hourly or
quarter-hourly values, reads local times in the market time zone with the DST nights
spelled out, refuses a file for another point by its GSRN, and writes every reading —
new, replacing the current one as history, or unchanged — in one transaction, or
nothing. Both read files through the shared `internal/fileimport`. See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#importing-metering-points).

*Delivered:* a supplier's metering points, their customers and their history in
Vantigo on day one, with the import behind its own sensitive permission
(`energy:import`) and every write still needing the permission it needs by hand.

### Phase 4 — Invoicing groundwork (align with Invoices)

An `internal/contracts` interface (e.g. `contracts.ConsumptionProvider`) returning
billable consumption per customer/metering point/supply period, keeping the
privacy boundary enforced in one place. Price dimension: spot prices per price
area (NO1–NO5) and grid tariffs, decided together with the Invoices module.

*Unblocks:* the Invoices module generating invoice lines from consumption
without touching `energy` tables.

### Phase 5 — Elhub integration (when API access is ready)

Sync worker following the Communications outbox pattern: ingest metered values
as `Source=Elhub` revisions (the supersede model already handles corrections),
derive supply-period events from market processes (leverandørbytte,
innflytting/utflytting), and reconcile master data (grid area, price area,
connection status) against Elhub with drift flagging. Adds
`SourceMessageId`/event audit columns.

*Unblocks:* automated consumption import and market-event-driven supply periods.

### Later

- **PT15M scale** — monthly partitioning of `consumption_intervals` when
  quarter-hourly Elhub data starts flowing; no schema changes required.
- **Data retention & GDPR** — retention policy for consumption tied to former
  customers; audit logging of back-office access to consumption data.
- **Customer self-service** — the supply-period authorization query is the
  foundation if end customers ever get a portal.

## Products

### Phase 1 — Catalog enrichment (done)

Table-stakes catalog fields identified from industry research (ERPNext, Odoo,
Business Central, Shopify, Medusa, Akeneo): plain-text description, a multi-level
category hierarchy (single category per product), GTIN barcodes with check-digit
validation and uniqueness, and logistics fields (weight and dimensions).

*Unblocks:* a usable catalog UI, category-based navigation and reporting, barcode
lookup, and shipping-cost estimation groundwork for Orders/Warehouse.

### Phase 2 — Variants (done)

Split the model into **Product** (shared identity: name, description, category,
type, status, tax category) and **ProductVariant** (sellable identity: SKU, barcode,
unit, standard cost, prices, weight/dimensions, and option values such as
`Color=Red`, stored as a JSONB map). Every product has at least one variant;
single-variant products keep an inline UX (flattened API responses).
Migration: each existing product became a product with one default variant;
`ProductPrice.ProductId` moved to `VariantId`.

*Unblocks:* selling size/colour assortments without SKU duplication. Order lines
in the future Orders service reference variants.

### Phase 3 — Tax categories (done)

Replaced the per-product `VatRate` value with a reference to a **TaxCategory**
(Standard/Reduced/Zero/Exempt) whose rates are configured centrally in the
Products module. Migration seeded categories from the distinct existing rates.

*Unblocks:* rate changes without touching every product, differentiated goods vs.
food vs. exempt handling, and correct tax snapshots for Orders (responses embed
the resolved rate for snapshotting).

### Phase 4 — Pricing depth (align with Orders)

Extend `ProductPrice` with price lists, customer-group prices (the group itself
is `contracts.CustomerEntry.Group`, delivered by Customers phase 4 delivery D)
and quantity breaks, with an explicit, documented precedence order.

*Unblocks:* B2B negotiated pricing and volume discounts; designed together with
the Orders service so order lines resolve prices the same way the catalog does.

### Phase 5 — Operational readiness (align with Warehouse)

Unit-of-measure conversions (base unit + conversion factor) and supplier records
per product/variant (vendor SKU, lead time, minimum order quantity).

*Unblocks:* purchasing and warehouse receiving; designed together with the
Warehouse service.

### Later

- **Bundles/kits** — explicit composition relationships between products.
- **Media/images** — needs a platform file-storage decision first.
- **Localization** — translated names/descriptions.
- **Typed custom attributes (PIM)** — schema-defined attributes per category.
- **Tags/collections** — the designated answer for cross-cutting, multi-assignment
  grouping (e.g. "Summer sale"), deliberately separate from the single-assignment
  category hierarchy.

## Projects

### Phase 1 — Projects, roles and billing lines (done)

A project per customer (or none, which means internal) with a unique, editable
code (`KVEM1000`) suggested from the customer and project names plus a running
number, a status the module never restricts (`planned`, `active`, `on-hold`,
`completed`, `cancelled` — only `active` means "open for work"), dates, budget
hours and the commercial rules invoicing will later read: billing type, fixed
price, budget amount and one currency per project. Projects are cancelled, never
deleted. Fixed roles per project (`manager`, `member`, `viewer`) sit under five
global permissions, with `projects:access` gating the app; an outsider gets 404
rather than 403, and financial fields are shaped out of the response rather than
forbidden. Billing lines pin a product variant to a pricing rule (`list`,
`fixed`, `discount`) under a short code, giving the trackable `KVEM1000-PM`.
Products is an **optional** dependency — without it the module is a full planning
tool and billing-line operations answer 409. Three contracts landed with it:
`contracts.UserDirectory` (identity), `contracts.ProductCatalog` (products) and
`contracts.ProjectDirectory` (projects), plus the platform's provider slots for
them.

*Unblocks:* Time tracking — a stable project identity, a code employees can quote,
per-project authorization, and a priced line to book hours against.

### Phase 2 — Time tracking and tasks (done)

Decided in `docs/superpowers/specs/2026-09-18-project-management-plan.md`
after surveying the Nordic ERPs, the international PSA tools and the dedicated
PM tools. Time is its own module (`time`) that requires Projects and reads
`contracts.ProjectDirectory`, never the `projects` schema; tasks live inside
Projects. A time entry references a project, optionally a billing line
(`KVEM1000-PM`) and optionally a task — the timesheet lists, under each project
code, the active lines and the caller's open tasks. Rates resolve through an
explicit chain (billing-line rule → project default → customer default → person default; cost
always from the person) and are snapshotted onto the entry; approval is a
state machine (`draft → submitted → approved → invoiced`) with period locking.

**Tasks.** Inside Projects, following the project's own roles and adding
no permission key: single assignee, the fixed statuses `todo`/`in-progress`/
`done`, one level of subtasks, checklist items, comments, dates and estimates;
a Tasks tab with list and board and a task drawer, plus `/projects/my-tasks`
across projects. This is where `member` and `viewer` stop being the same thing.
`ProjectDirectory` grew `Projects`, `ProjectByCode`, `BillingLines`, `Task`,
`OpenTasksForUser` and `CanLogTime` for Time to build on, and the project gained
`defaultBillRate`.

**Time tracking.** The `time` module and the `@vantigo/time-ui` app: entries with
hours or start/end times, the rate chain resolved at every save and frozen at
submission, effective-dated person rate cards, weekly submission from a grid,
batch approval and rejection by project managers and `time:approve` holders, a
period lock for `time:manage`, a people overview, the dashboard figures and a
Time tab on the project page. `invoiced` is terminal and waits for the module
that writes it. See [`docs/src/content/docs/en/reference/time.md`](docs/src/content/docs/en/reference/time.md).

*Unblocks:* hours that can be invoiced, the first real consumer of billing
lines, and a task list contractors and consultants will actually keep.

### Phase 3 — Budgets, billing milestones and costs (done)

Decided in `docs/superpowers/specs/2026-09-19-project-economy-design.md`, two
deliveries.

**Billing milestones and line budgets (done).** A project's invoice plan: a
billing milestone (name, optional planned date, a flat amount or a percent of
the fixed price) moving through `planned → ready → invoiced` with `cancelled`
off to the side, manual ordering, and a manual "mark as invoiced" step that
freezes the amount (a later Invoices module will set the same status). The
percent-of-price amount is computed exact-decimal, on read, so an open
milestone follows a later change to the fixed price; project guards refuse
clearing the currency or the fixed price while amounts still depend on them.
Line budgets: `budgetHours` (planning data) and `budgetAmount` (financial,
needs a currency) on a billing line, shown beside the line's pricing. Every
write that depends on the project's currency, fixed price or billing type
locks the project row first and decides under it — no separate ordering lock
turned out to be needed once that held. See
[`docs/src/content/docs/en/reference/projects.md`](docs/src/content/docs/en/reference/projects.md#billing-milestones-and-the-invoice-plan)
for the model, the status table and the guards.

*Unblocks:* fixed-price milestone invoicing recorded in Vantigo, and the
Economy tab's first half (`/projects/$projectId/economy`).

**Budget vs actual, portfolio and alerts (done).** A new optional contract
(`contracts.ProjectActuals`) through which Time supplies logged hours and
amounts, in three buckets (approved, submitted, draft), to Projects without
Projects ever reading the `time` schema; "budget used" resolved from one
basis per project (budget amount → fixed price → budget hours), on the exact
ratio, never the rounded percentage; the Economy tab's budget half (bars,
per-line table), now shown to everyone who sees the project rather than only
to its financial viewers; a project portfolio page (`/projects/economy`,
sidebar entry "Project economy"), filtered, sorted and capped at 2 000
projects; dashboard signals — a "ready to invoice" hint on the Projects card
and four attention types (a budget nearing or past 100 %, a ready or an
overdue milestone) linking to the Economy tab; `projects:view-costs`, a new
sensitive permission for cost and margin, granted to nobody by default. See
[`docs/src/content/docs/en/reference/projects.md`](docs/src/content/docs/en/reference/projects.md#project-economy) for the model, the
shaping rules and the dashboard signals, and
[`docs/src/content/docs/en/reference/time.md`](docs/src/content/docs/en/reference/time.md#what-time-reports-to-other-modules) for the
contract Time implements.

*Unblocks:* profitability and budget alerts, a project portfolio view.

**Work types and overtime multipliers (done).** Decided in
`docs/superpowers/specs/2026-09-25-project-work-types-design.md`. A project
defines its work types once — a name, a bill multiplier and a cost multiplier,
as percentages of the rate — and they apply to every billing line of it; a
person picks one when logging time. It is a project-level rule rather than a
multiplier per billing line on purpose: overtime is overtime whatever the
work, and a rule per line would recreate, one level down, the duplicate
"(overtime)" lines it replaces. Projects stores the percentages and computes
no money; Time multiplies whatever rate the chain resolved, keeps the base
rates and snapshots the multipliers beside them, freezes them on submit, and
multiplies where it sums, exactly; the actuals contract gains the work per
type (ids and figures; Projects names the rows), and the Economy tab shows
"Hours by work type". The Norwegian overtime case no longer needs a duplicate
billing line. See [`docs/src/content/docs/en/reference/projects.md`](docs/src/content/docs/en/reference/projects.md#work-types) and
[`docs/src/content/docs/en/reference/time.md`](docs/src/content/docs/en/reference/time.md#the-work-types-multiplier).

*Unblocks:* overtime billed and costed at its own rate on every project,
without a line per kind of work.

**Supplier invoices (done).** Decided in
`docs/superpowers/specs/2026-09-26-supplier-invoices-design.md`. Expenses
landed separately — see
[Expenses phase 3](#phase-3--expenses-on-the-project-page-done). The invoice a
supplier sends for work or goods on a project is a fourth kind in Expenses:
the supplier, the supplier's invoice number, the invoice date (the entry date)
and the due date, its PDF attached and required on submit, attested through
the module's own flow, priced and re-billed with the outlay's markup, and
company-paid — so it owes nobody, now that who is owed money is one SQL
function (`expenses.owes_employee`) and its Go mirror rather than fourteen
copies. It is recorded by whoever holds the project's financial rights, on a
completed project too, and visible to them as rows. The expenses contract
carries it as a per-currency sub-figure, and the Economy tab's Costs section
and the Expenses tab's cards show "Of which supplier invoices". Accounts
payable, a supplier register and inbound e-invoices stay out of scope. See
[`docs/src/content/docs/en/reference/expenses.md`](docs/src/content/docs/en/reference/expenses.md#the-supplier-invoice).

*Unblocks:* a project's non-hours cost that is what suppliers invoiced, not
only what somebody put on an expense.

Forecast / estimate-to-complete and original-vs-revised budgets (tracking a
budget's own history rather than only its current value) are candidates worth
deciding on next, not committed work yet.

### Phase 4 — Delivery milestones, timeline and templates

Delivery milestones as a lightweight entity (name, optional target date, %
complete derived from linked tasks); a lightweight timeline (bars, drag,
finish-to-start dependencies, opt-in cascade) — never a full Gantt in-house;
project and task templates with relative dates.

*Unblocks:* planning at kickoff; the "are we on track" view.

### Phase 5 — Capacity planning

Allocations (person × project × date range × hours-or-percent,
tentative/confirmed, placeholders) as a table of their own, compared with
assignments and actuals; availability and utilisation.

*Unblocks:* "who is free in week 42"; revenue forecasts from planned hours.

### Later

- **Full Gantt, WIP / earned value, multiple assignees, a customer portal** —
  only on proven demand; the plan explains why every comparable product shipped
  these last or bought them in.
- **A Norwegian construction layer** — NS 8405–8407 milestone rules,
  *innestående* (retention), kontrollskjema, Boligmappa: unserved in the SMB
  tier today and a vertical of its own on top of phases 2–3.
- **Milestone codes** — if delivery milestones ever get codes they use a shape
  decided with phase 4, never a bare `<project>-<code>` suffix, which billing
  lines own.
- **Configurable project roles** — the three roles are named capability sets in
  code, so a role editor can be added without a schema change. It needs a real
  demand for a fourth role first.
- **Project documents** — files on a project, once the object-storage scopes and a
  document model are settled (Communications' attachments are the working example).
- **Domain events** — `ProjectCancelled` and friends once the event bus exists; see
  [Platform](#cross-module-domain-events-deferred-until-orders). Until then every
  consumer reads `contracts.ProjectDirectory` synchronously, which is enough.
- **Assigning users who lack `projects:access`** — a manager can today only pick
  from the user directory, and a colleague without the permission would be assigned
  a role they cannot use. Whether to filter the picker, warn, or grant on
  assignment is a product decision, not a technical one.

## Expenses

### Phase 1 — Foundation: entries, approval, receipts, reimbursement and invoicing (done)

The `expenses` module and `@vantigo/expenses-ui`: outlays and mileage with receipts
(JPEG/PNG/HEIC/PDF, sniffed rather than trusted, served only through the API), a
`draft → submitted → approved`/`rejected` flow with an admin-configurable receipt
rule and a rate override an approver can make on a submitted mileage line, then two
independent tracks after approval — reimbursed per expense by `expenses:manage`,
with a payroll CSV, and invoiced per billable line by whoever holds financial
rights on its project — either, both or neither, in any order. Dated rates
(mileage, its passenger supplement, and a customer rate per kilometre) and
categories are admin-managed and effective-dated the same way Time's person rates
are. Unlike every other module here, Expenses **depends on nobody but identity**:
Projects is read only optionally, through `contracts.ProjectDirectory`, for the
project a line is booked on and the billing figures its side prices — with
`projects` disabled every project-shaped field is refused on its own field rather
than accepted and dropped. The period lock protects what was submitted and
approved; it deliberately does not reach reimbursing, pricing or invoicing, which
are bookkeeping done once a period has closed. See [`docs/src/content/docs/en/reference/expenses.md`](docs/src/content/docs/en/reference/expenses.md).

*Unblocks:* a company's non-hours costs recorded and paid back, and a customer's
project a step closer to fully costed with Time's hours already in.

### Phase 2 — Travel claims and per diem (done)

A **travel claim** is the unit a trip's outlays, mileage and per-diem days are
grouped, submitted, approved and paid under: it carries the status, the
decision, the reimbursement and the project, while each line keeps its own
amounts, receipts and invoicing. **Per diem** is a day of the trip per line,
priced at the rate in force on that day less the percentage of every meal
somebody else paid for, with the days a trip's own times imply suggested by the
server and ticked off by the traveller. The per-diem and meal rate kinds phase 1
reserved are seeded from the state's *Særavtale om dekning av utgifter til reise
og kost innenlands* (in force 2026-01-01 to 2027-12-31), verified against the
source rather than written from memory, and are the administrator's to change
from there. Because a claim stores two instants, the installation gained a
**business time zone** — the one calendar the period lock, the per-diem window
and the payroll file are judged by. In the app: the trip's own page, travel
claims as units in the approval queue and the payroll list (one selection, one
request, across both kinds), and the rate kinds and time zone in settings. See
[`docs/src/content/docs/en/reference/expenses.md`](docs/src/content/docs/en/reference/expenses.md).

*Unblocks:* a whole trip recorded, approved and paid as one, and per diem
priced by the agreement instead of by hand in a spreadsheet.

### Phase 3 — Expenses on the project page (done)

Expenses joins Time's hours on the Economy tab (`/projects/$projectId/economy`)
as the other half of a project's actual cost, through an optional contract of
its own — `contracts.ProjectExpenses`, the same shape Time already provides
`contracts.ProjectActuals` through, so Projects never reads the `expenses`
schema and either module can be left out of an installation. It answers **per
currency and converts nothing**: a line carries its own currency, which may be
neither its travel claim's nor its project's. On the Economy tab that is a
costs section (the three buckets with counts, what they cost, what they pass on
to the customer), expenses inside the margin, and billable lines in "ready to
invoice"; expenses stay out of `budgetUsed` and the per-line budgets, which
remain about work. Beside it, the project page's own **Expenses** tab
(`/projects/$projectId/expenses`) shows the same money from the other side —
the totals per currency, the expenses behind them that the caller may open, a
"ready to invoice" filter that is exactly the figure above it, and **Record a
cost** with the project fixed. The totals and the list are gated differently on
purpose — the aggregate is the project's money, the rows are a colleague's
receipts — and the tab says so rather than showing an empty table. See
[`docs/src/content/docs/en/reference/expenses.md`](docs/src/content/docs/en/reference/expenses.md#on-the-project-page) and
[`docs/src/content/docs/en/reference/projects.md`](docs/src/content/docs/en/reference/projects.md#the-optional-expenses-dependency).

*Unblocks:* a project fully costed — hours and money side by side — and one
list of everything waiting to go on an invoice.

**Next: invoicing.** `invoiced_at` is still set by hand, per line, by whoever
holds financial rights on the project; "ready to invoice" is the list an
Invoices module would build from, and that module owns the stamp when it
arrives — the same thing [`docs/src/content/docs/en/reference/time.md`](docs/src/content/docs/en/reference/time.md#what-invoicing-will-read)
has said of an hour since phase 2. Supplier costs are no longer a gap: a
supplier's invoice is its own kind — see [Projects](#projects).

## Invoices

A new module, researched in
[`docs/superpowers/research/2026-09-26-invoices-module.md`](docs/superpowers/research/2026-09-26-invoices-module.md).
Four roadmaps converge on it: Projects (milestones' `invoiced` status), Time (an hour's
`invoiced` column, "what invoicing will read"), Expenses (the manual invoiced stamp,
"next: invoicing") and Energy phase 4 (consumption billing). Customers has laid the
groundwork since its phase 2: the billing profile (invoice e-mail, payment terms,
currency, language, delivery preference, Peppol id, GLN, buyer reference) and the
Peppol capability lookup. Two facts shape the order below. Norwegian law makes the
sales document a strict thing — machine-assigned, gap-free numbers assigned at issue,
never edited or deleted, corrected only by a numbered credit note, VAT per rate in NOK,
kept five years — and the B2B e-invoicing duty is law from 2027-01-01, so EHF cannot be
a late phase. Vantigo stays a sub-ledger: it issues, sends, tracks and exports; it keeps
no general ledger.

### Phase 1A — The sales document (done)

Delivered on `feat/invoices-foundation`
([design](docs/superpowers/specs/2026-09-26-invoices-foundation-design.md),
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md)): the seller record and one gap-free number
series whose start locks at the first issue; VAT codes whose rates are dated periods,
seeded with the SAF-T output codes; drafts issued in one serialised transaction into an
immutable, numbered document with a buyer and a seller snapshot and VAT per rate,
immutability enforced by database triggers too; the issue-date rule with § 5-1-3's
previous-month exception; a PDF in Noto Sans rendered from the snapshot and stored once,
and a watermarked preview; full and partial credit notes in the same series with caps
per line and on the headline; an invoice journal with the gap check; both customer slots
(merge re-points, anonymisation erases drafts only); and `Status` and `MergedInto` on the
customers contract's billing profile, so `disabled` finally means "blocked for invoicing".

### Phase 1B — Payments, delivery and the export (done)

Delivered on `feat/invoices-payments-delivery`
([design](docs/superpowers/specs/2026-10-02-invoices-payments-delivery-design.md),
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md)), building only on 1A's tables: payment
registrations against an issued invoice, refused once nothing is open or over the open
amount, locked on the same row a credit note's issue locks, removable only with a reason
and never deleted; derived states (credited, paid, overdue, partially paid, open) from
one SQL function with a Go mirror, on every document and as a list filter, with the open
amount and a refund due; e-mail delivery of the stored PDF with the seller as Reply-To
(the one platform change, `mail.Outbound.ReplyTo`), plain-text cover mails in nb and en
whose payment paragraph follows the open amount, four warnings — a non-e-mail delivery
preference, and Norwegian businesses, red from 2027-01-01 — and an immutable delivery
log; the anonymisation blanking that log and the person's payment notes under a lock
and marking the customer erased, so a racing send cannot keep the address; the
accountant's CSV export; the stats summary and the dashboard card; the customer page's
Invoices tab; a new permission, `invoices:payments`; and the customers-plus-invoices
integration test.

*Unblocks:* receivables tracked in Vantigo, and everything below. Phases 1A and 1B do not
meet the B2G duty (EHF since 2019) nor the B2B duty from 2027-01-01 — that is phase 2.

### Phase 2 — EHF over Peppol, and KID (done)

Delivered on `feat/invoices-ehf-peppol-kid`
([design](docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md),
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md),
[`docs/src/content/docs/en/admin/e-invoicing.md`](docs/src/content/docs/en/admin/e-invoicing.md)):
a deterministic UBL 2.1 Invoice and CreditNote per Peppol BIS Billing 3.0 with the
Norwegian rules, rendered from the issued snapshot with the stored PDF embedded, checked
by a Go pre-check at send and by the official XSD and Schematron artefacts in CI; the
seller's Peppol id on the settings; a KID under the seller's bank agreement (MOD10 or
MOD11), computed at issue and stored with its algorithm, on the PDF, in the e-mail and
as the EHF's payment id — and no payment id without one; an access-point port with a
Storecove adapter, its key sealed in its own table; a send that re-checks the receiver
against the Peppol network and queues a transmission under the document's lock; two
workers — one submitting once per claim under an idempotency key and probing the
provider's evidence, one draining Storecove's event queue — with the receipt and the
delivered copy stored as the record, and an outcome the machine cannot know left
`unconfirmed` for a person to resolve; cancel, resolve and the EHF download; channel
precedence in the app (EHF first when the customer prefers it, or has a Peppol id and no
preference; e-mail second, neither refused for the other); and the transmissions in the
customer slots' export and erase.

**Still open in this phase:** a run of the tagged Storecove sandbox test
(`go test -tags storecove`, which needs a sandbox key) proving that the PDF embedded in
the submitted UBL survives Storecove's regeneration of it; and adopting Peppol BIS
Billing 3.0.21 — the artefacts are pinned at `v3.0.20` — the day OpenPEPPOL tags it.

*Unblocks:* B2G invoicing (mandatory since 2019) and the B2B duty from 2027-01-01; the KID
on every invoice is what phase 4's payment imports match on.

### Phase 3 — Work becomes invoices (done)

Delivered on `feat/invoices-work-to-invoices`
([design](docs/superpowers/specs/2026-10-05-invoices-work-to-invoices-design.md),
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md#invoicing-work)):
the **uninvoiced work** — approved, billable, priced hours, expenses ready to invoice
(outlays, mileage and re-billed supplier invoices, with their markup) and ready
billing milestones — read through three new billable contracts and shown per project
and kind on the customer's Invoices tab and on a new Invoicing tab on the project page,
with the reasons a row cannot be chosen (held by a draft or invoiced, a fixed-price
project's hours, a non-billable project, not NOK), the one-month deadline warning and
a supplier invoice billed twice; a **wizard** that makes an invoice draft of it, or
adds it to one, grouped per project, work type, person, day or itemised, written in the
buyer's language, each kind at its own VAT code from the settings; each line's
**sources** held by the draft, carried by every save, refreshed on demand and judged
fresh on read; the **write-back** — Time, Expenses and Projects each mark their rows
invoiced with the invoice's id and number inside the issue's own transaction, through
the third sanctioned cross-module write (`contracts.InvoicedWorkHolder`, module
boundaries rule 10) in one cross-module lock order, the customers merge reordered to
match; **release on credit** — a credit note that returns a line in full gives its
work back, uninvoiced again, and a re-pulled draft suggests the note naming what it
replaces; the document's **project** (`project_id` and its code), printed on the PDF,
carried as BT-11 in the EHF, a list column and filter and the export's last column;
**a-konto and the final settlement** — deduction lines at -1 taxed at the a-konto's
own rate, capped per invoice and VAT code, listed on the PDF and as BG-3 preceding
invoices in the EHF; an optional **timesheet** in the PDF, never with an entry's note,
naming people by initials unless the settings say otherwise; the "Invoiced by invoice
n" badges and the refused manual undo in the source apps; and every race of the issue
against the writers of its sources — a manual mark, a reimbursement, a milestone move,
a fixed-price edit, an unapprove, a merge — proved against the real modules on a pool
of two connections.

*Unblocks:* project invoicing end to end; Time's and Expenses' "next: invoicing".

### Phase 4 — Payments and reminders (done)

**4A and 4B — delivered** on `feat/invoices-payments-reminders`, the phase's first pull
request ([design](docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md),
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md#bank-files-and-the-exception-queue),
[`docs/src/content/docs/en/admin/payments.md`](docs/src/content/docs/en/admin/payments.md)):
**the ledger and bank imports** — a payment's `source` (manual or imported); OCR giro and
camt.054 (`.001.02` and `.001.08`) files parsed by two hand-written parsers in a leaf
package, checked against their control totals, stored once by their hash and imported
against the bank agreement's accounts; each line fingerprinted against duplicates across
files and formats and matched on KID — principal first, then the charges — or queued in
the **exception queue** with its reason and suggestions, where a person applies it across
invoices, dismisses it, confirms a duplicate, treats it as distinct, reopens it or handles
a reversal; the Payments screen with its files and the queue. **Overdue and reminders
under dated rules** — collection rates (the inkassosats and the late-interest rate) in a
dated, seeded table a release can extend; the reminder settings with the collection-law
regime (1988 or 2026) and its review; a customer's reminder policy; the delivery fact,
with manual deliveries; one pure rules engine — the 14-day rules, the fee cap, the B2B
standard compensation, late interest by the day — shared by the overdue list, the
preview, the run and the worker; charges kept apart from the principal, with charge
payments and waivers; reminder runs, the letters sent by e-mail by a leased worker or
printed in batches, confirmed posted or reprinted; holds and the hand-off to a
collection agency with its CSV export; the dashboard's attention items; and the
customers slots' export, erase and merge for every new kind. Every race on a new lock
is proved on a pool of two connections.

**4C — delivered** on `feat/invoices-payments-port`, the phase's second pull request
([design](docs/superpowers/specs/2026-10-06-invoices-payments-reminders-design.md) D13–D19,
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md#vipps-payments),
[`docs/src/content/docs/en/admin/payments.md`](docs/src/content/docs/en/admin/payments.md#the-public-host)),
on one migration, `00046_invoices_payments_port.sql`: **the payments port** —
`internal/payments`, a platform package with no SQL and no module import, so a later
Point of sale consumes the same adapters — with the **Vipps MobilePay ePayment**
adapter (token cache, system headers, idempotency keys, typed errors, the on-site QR
rendered locally), its fake `vippstest` in every suite and a tagged test against Vipps'
test environment; **`PUBLIC_BASE_URL`**, a second admitted host that serves only an
exact allowlist; the Vipps credentials sealed in their own table and the payment
settings, on-site payment turned on only with the kontantsalg acknowledgement; **pay
links** made at issue or on the send, printed on the PDF with a QR and in the e-mail
and the reminder letter, revocable; the anonymous **pay page** on the public host;
**payment attempts** and the `invoices-payments` worker, which reserves the amount
under the invoice's lock, captures outside it and registers the payment in a second
locked transaction, never releasing an outcome it cannot know (`capture_unknown`);
abandon, the refund watch and four attention types; **the quick invoice**
([reference](docs/src/content/docs/en/reference/invoices.md#the-quick-invoice)) —
created, issued and recorded handed over in one transaction, safe to send again by its
request id; **on-site payment**
([reference](docs/src/content/docs/en/reference/invoices.md#paid-on-site--kontantsalg))
with the day's Vipps payments and their reconciliation; the customers slots' export
and erase of pay links and attempts; and the screens — settings, the invoice page's
Vipps card, the Vipps tab, the quick invoice screen and the pay page. Every race on a
new lock is proved on a pool of two connections.

The quick invoice is **a credit sale when paid later** — by KID through the bank, or by
its pay link once the tradesperson has left. **Paid on site by Vipps it is kontantsalg**
(Vipps at delivery counts as cash), allowed without a kassasystem only as a
**kontantfaktura**, which the quick invoice enforces: the buyer named with an address
or an organisation number, one payment of the whole open amount per invoice in the
ordinary series, the kontantsalg notice acknowledged before the setting goes on, and
the day's list with its reconciliation. A sale over a counter is not that, and belongs
to the Point of sale module below.

**Vipps webhooks are deferred**: the integration polls. Pinning the webhook's HMAC step
needs a real signed webhook from Vipps' test sales unit — the published example's
signature does not reproduce (research R4 §7 item 37) — and no test keys or captured
fixture existed when the worker landed, so the raw-body router option, the receiver and
its registration were left out, and the public host's allowlist names no webhook path.

**Still open in this phase:** a run of the tagged Vipps test (`go test -tags vipps`,
which needs the test sales unit's keys); the webhook above; and a revisor's or
accountant's reading of the kontantsalg notice before on-site payment is turned on in
production.

*Unblocks:* receivables without a spreadsheet; Point of sale's payment adapters.

### Phase 5 — Energy consumption billing

Periodic invoices per supply point and period from Energy's figures, tariff and price
lines, batch issuing, the utilities' periodic-invoicing rules — with Energy phase 4's
consumption contract.

*Unblocks:* the energy business's core revenue.

### Later

Recurring invoices and retainers (a subscription may be invoiced up to a year ahead) ·
the 2028 buyer org-number rule (bokføringsforskriften § 5-1-2 from 2028-01-01: a
bokføringspliktig buyer's organisation number always stated — the customers directory
knows no such fact yet, and the issue still accepts a complete address instead) ·
utlegg outside the VAT base (merverdiavgiftsloven § 4-1 (2) a; phase 3 bills every
re-billed cost as a sale) · several EHF attachments — a separate timesheet file,
forwarded receipts · construction's § 8-1-2a progress invoicing and retention money ·
eFaktura and AvtaleGiro for consumers · a customer portal · a bank API for payments ·
several legal entities per tenant, each with its own series · a posting export in
SAF-T-friendly form to Tripletex, Fiken or PowerOffice.

Left out of phase 4 on purpose (design D20): customer credit balances, refunds as a flow
(and the payments port's `Refund` used by Invoices), setting an overpayment off against
the next invoice, rounding off small differences · an export of payments or charges for
the accountant · bank APIs and direct file delivery · camt.053 and the reconciliation of
movements that are not customers' payments · the Vipps Report API and settlement
reconciliation · MobilePay markets, other providers, cards, push messages, long-living
payments · the creditor's own betalingsoppfordring (inkassoloven § 10) · the § 19
regulation's egeninkasso fees · an agreed B2B interest rate · interest on fees · the
chapter 2 cost caps · letters as EHF or eFaktura, and SMS · a letter for charges alone ·
an agency API and an automatic hand-off · a group-level reminder policy · the B2B 60-day
term check · several KID lengths on one agreement · a holiday calendar · automatic webhook
re-registration · a "paid" stamp or receipt on the PDF · anything of a kassasystem (Point
of sale, below).

Follow-ups from phase 4's first pull request: the customers merge's `mergeKindNouns` for
the eight new invoices kinds (reminders, charge payments, charge waivers, manual
deliveries, holds, collection hand-offs, bank transactions, reminder policies) · the
overdue list's `charges=outstanding` filter covering credited invoices that still have
charges outstanding · the invoice number on `InvoicesReminder`, so a letter links by
number rather than by document id · `bankFileId` on `InvoicesPayment`, so an imported
payment links to its file · a bulk confirm-duplicate per bank file (a `duplicate` line
keeps the file's attention item alive until each is confirmed by hand).

## Point of sale

A new module, for a sale paid at the moment of delivery: a haircut, a counter sale, a
single job settled on the spot from a tablet. It is **its own module**, not an Invoices
feature, because a cash sale is a different legal document from an invoice: under
bokføringsforskriften a sale settled at delivery by cash, card or Vipps is **kontantsalg**
and must be registered in an approved cash register system (kassasystem) with its own
receipt numbering, a sales journal, X and Z reports, a receipt in the prescribed form
and a product declaration filed with Skatteetaten — separate records, retention and
audit rules from the invoice series. It consumes Products (the catalog and tax
categories), optionally Customers (a named buyer on a receipt) and the payments port of
Invoices phase 4 (`internal/payments`, delivered with its Vipps adapter); it produces
accounting output per VAT rate as Invoices' CSV does; it never issues invoices. An
invoice paid at delivery is still kontantsalg: a business selling over a counter needs
a kassasystem whatever document it issues, and Invoices' on-site payment covers only
the kontantfaktura case — an identified buyer named on a full invoice, for a business
that ordinarily sells on credit. A counter sale is also an order fulfilled and paid at once, so
this module is a candidate first consumer of the deferred Orders model and is sequenced
after the payments port and, where the timing allows, after Orders.

### Phase 1 — Research, three models side by side

Before any design, a research pass in the shape of the Storecove spike, with sources,
that settles which model is viable for which customer:

1. **The cash-register rules** for the register itself: the exemptions (small cash
   volumes, sporadic and mobile sales), what an approved kassasystem must do and
   declare, and what the receipt must say. Where the line runs between "an invoice paid
   at once by Vipps" and a cash sale is settled (Invoices research R4 §9): it is
   kontantsalg, outside a kassasystem only as a kontantfaktura.
2. **Vantigo as the register** with a card terminal attached: which terminal APIs work
   from a web app on a tablet in Norway (Adyen's terminal API, Viva Wallet, Stripe
   Terminal if available here, Nets/Verifone integrations), and what each costs in
   certification and in device handling (drawer, printer, offline).
3. **An external register, Vantigo as the books**: Zettle by PayPal and SumUp, whose
   apps are themselves approved registers and whose purchases APIs can feed Vantigo's
   accounting export, products and customers — the cheap path for a shop that already
   owns one, with no cash-register obligation on Vantigo's side.
4. **Vipps at the counter** through the payments port (`internal/payments`) in either model.

*Unblocks:* the decision between building a register and integrating one.

### Later

The register itself (basket, product grid, payment screen, shifts and cash counts,
receipts, X/Z reports, the product declaration) · gift cards and deposits · a
customer-facing display.

## Calendar

A new module, researched in
[`docs/superpowers/research/2026-10-19-calendar-module.md`](docs/superpowers/research/2026-10-19-calendar-module.md)
and designed in
[`docs/superpowers/specs/2026-10-19-calendar-design.md`](docs/superpowers/specs/2026-10-19-calendar-design.md):
one view of every time-bound thing the other modules know about one person — the
follow-ups, the absence, the tasks, the hours, the trips — beside the appointments they
schedule themselves, with colleagues on them. Like Expenses and Staff it **depends on
nobody but identity**: what it shows from the other modules it never copies, each
enabled module projecting its own rows at read time through a many-provider contract,
under its own permission. The one thing it owns is the appointment. It needs the
[time zones](#time-zones-done) design's deliveries A and F, both done.

### Phase 1 — The calendar, invitations and the feed (A done)

Three deliveries, A specified in full by the design, B and C decided there and planned
on their own.

**A — The calendar, built** on `feat/calendar-foundation`
([the plan](docs/superpowers/plans/2026-10-19-calendar-foundation.md);
[the reference](docs/src/content/docs/en/reference/calendar.md)): the `calendar` module
and `@vantigo/calendar-ui`; appointments, timed as two instants or all-day as two dates,
storing the zone they were made in, shared or private, busy or free, with `ical_uid` and
`sequence` from the first row; a recurrence subset of RFC 5545 (daily, weekly, monthly,
every *n*, until or *n* times) expanded in the appointment's own zone across the clock
change, with exceptions by the occurrence's day; colleagues on an appointment and their
answers, reset when the time changes; the `contracts.CalendarSource` many-provider slot,
collected from enabled modules only, with five providers — Customers' follow-ups and
interactions, Staff's own absence, Projects' tasks, Time's entries, Expenses' travel
claims — each its own gatekeeper, a failing one failing the read; `GET /items`, a
window of up to 93 days judged in the viewer's zone, with the public holidays; a
colleague's busy and free behind `calendar:view-colleagues`; the Month, Week, Day and
List views with the legend and filter, the appointment sheet and the colleague picker;
the dashboard's **Today and this week** agenda and **New appointment** on the customer
page and the project page. Two permissions, one migration,
`00055_calendar_baseline.sql`, and the contract `openapi/calendar.yaml`.

Decisions taken while building it:

- **FullCalendar 7 draws the grids**, proven in the shell before anything else was
  built. Version 7 has no global colour variables to map: the app takes the Breezy
  theme's layout and writes its palette in one stylesheet from Mantine's variables, so
  the grid follows the Vantigo theme and the colour scheme. The fallback — a month grid
  and an agenda of our own — was not needed.
- **A colleague's calendar has one audience rule, the permission**: every active
  colleague, with no manager relation, since the calendar has no org chart and does not
  borrow Staff's, which may be off. Their absences are not shown there (the design's
  open question, left for the owner to overrule).
- **The attention list keeps its dated items**: a follow-up or a task due is on the list
  and on the agenda, the one saying "this needs you", the other "when"; no module's
  `/stats/attention` changed.

*Unblocks:* one place a person sees their week, appointments with colleagues, and the
model B and C write from without a migration of the appointment.

**B — Invitations.** Attendees from outside the company — a customer's contact, read
through the customer directory at send time, or a typed address — each with an RSVP
token; the invitation in the iMIP layout (a `method=REQUEST` part and the `.ics`
attachment, `UID` the row's `ical_uid`, `SEQUENCE` the row's), sent from the
installation's `SMTP_FROM` with the owner's name, after the commit; the answer through a
web link on the public host, since no inbound mail route exists; an update resent with
the next `SEQUENCE`, and a delete become a cancellation kept until the series' last day.
A colleague is mailed through a new `contracts.UserDirectory.Notify`, so no address
crosses into the calendar; they answer in the app. Not built.

*Unblocks:* meetings with customers booked from Vantigo, answered from any mail client.

**C — The feed and the subscriptions.** A calendar address per person in two variants,
full and busy-only, behind a hashed, resettable token — the module's one anonymous
`GET`, on the public host's list — windowed, with series as `RRULE` and their exceptions,
one `VTIMEZONE` per zone, and the projected items in the full variant; calendars from
elsewhere subscribed to, polled by a `calendar-subscriptions` worker through `netguard`
and shown beside the person's own, never in their feed; a **My calendar** settings
screen for the addresses and the subscriptions. Not built.

*Unblocks:* the Vantigo calendar on a phone, and a person's other calendars beside it.

### Later

Two-way sync with Google and Microsoft · booking pages · rooms and resources · reminders
in the app and by mail · CalDAV · the inbound `METHOD:REPLY` route · moving a projected
item from the calendar (a write back into its module) · team calendars and delegation.

## Staff

A new module, researched in
[`docs/superpowers/research/2026-10-11-staff-module.md`](docs/superpowers/research/2026-10-11-staff-module.md)
and designed in
[`docs/superpowers/specs/2026-10-11-staff-foundation-design.md`](docs/superpowers/specs/2026-10-11-staff-foundation-design.md):
one record of every person who works for the company, which everything else about the
person hangs on — the employment the a-melding and the written contract describe, the
absence, the user account, the documents. Like Expenses it **depends on nobody but
identity**. Payroll itself (the a-melding, skattekort, feriepenger) stays out: Staff
keeps the fields payroll needs and exports them. The owner's sequence (research §10.11):
**this phase → the Entra module → Staff phase 2 → Staffing.**

### Phase 1 — The register, absence and the account flag (A1, A2, B and C done)

Four deliveries, four pull requests, one migration each.

**A1 — People and employments, built** on `feat/staff-foundation` (PR #135)
([`docs/src/content/docs/en/reference/staff.md`](docs/src/content/docs/en/reference/staff.md)):
the `staff` module and `@vantigo/staff-ui`; people with identity, contact, next of kin
and the sealed national identity number and bank account (encrypted, found by a keyed
MAC, revealed one at a time behind `staff:personal-data`); employments as Norwegian
arbeidsforhold with dated terms that never overwrite history, carrying the a-melding's
codes and the § 14-6 contract fields, and the seventeen-item contract check; the
company's structure (legal entities, business units, departments, locations); managers
from the terms or the department, with a cycle check serialised across people; the
self-service link from a person to an existing user, which gives My page and a
manager's reading of their own reports' records; five permissions with responses shaped per field group by audience; a
history of every change; the People, person, Settings and My page screens, the tile,
the dashboard card and the search's New person.

*Unblocks:* one register the rest of the phase hangs on — absence needs the employment
and the manager, the account flag needs the person and the link, the payroll export
needs the a-melding fields.

**A2 — Records, built** on `feat/staff-records` (PR #136)
([the reference](docs/src/content/docs/en/reference/staff.md)): documents per person in
the object store, under categories HR keeps, each with a retention class and the health
category behind `staff:personal-data`; the register's CSV import, checked before it
writes, and its CSV export; the month's payroll CSV behind an adapter port, so a
Tripletex or PowerOffice adapter plugs in later without touching the register; the
personal-data exports — the person's own copy and HR's, the latter audited; the legal
hold and the erased state, with the warning when an erased person is hired again; the `staff-retention` worker that erases what the
law no longer lets the company keep, and the ledger that removes their objects from the
store; the dashboard's figures, attention items and thresholds, one manager rule for
the dashboard, the Manager audience and the org view; and the Staff app's screens for
all of it.

**B — Absence, built** on `feat/staff-absence`
([the spec](docs/superpowers/specs/2026-10-11-staff-foundation-design.md), delivery B;
[the plan](docs/superpowers/plans/2026-10-13-staff-absence.md);
[the reference](docs/src/content/docs/en/reference/staff.md#absence)): the Norwegian
public-holiday calendar moved out of Invoices into the platform package
`internal/holidays`; absence types in the statutory vocabulary with their a-melding
leave descriptions ([the mapping](docs/src/content/docs/en/reference/staff.md#the-a-meldings-leave-descriptions)),
an HR-only visibility and company types beside the seeds; holiday schemes above the
statutory minimum, converted from virkedager, and the
[holiday account](docs/src/content/docs/en/reference/staff.md#the-holiday-account) per
person per year with adjustments, carry-over and the optional automatic carry-over;
requests and registrations, [decisions](docs/src/content/docs/en/reference/staff.md#decisions)
by the one manager rule and `staff:approve` (which starts granting here, with the
Approver audience), the narrowed self-approval, the absence mails to addresses Staff
holds, the approvals inbox and the
[calendar](docs/src/content/docs/en/reference/staff.md#the-calendar);
[sick-leave cases](docs/src/content/docs/en/reference/staff.md#sick-leave-cases) with
the arbeidsgiverperiode (weekends and public holidays between sick rows bridged) and
the follow-up milestones, [egenmelding](docs/src/content/docs/en/reference/staff.md#egenmelding)
under the installation's policy and its withdrawal, and
[care days](docs/src/content/docs/en/reference/staff.md#care-days) by ftrl. § 9-6; and
what absence adds to A2's records — the payroll export's leave rows and per-kind
totals, the health erase on the case's own schedule, the absences and cases in the
personal-data exports, the dashboard's absence figures and items, People's "on leave
today" — with My page's absence, the request sheet and My team, the Absence area's
calendar and approvals, and the person's Absence tab and the absence settings. One
migration, `00047_staff_absence.sql`.

Decisions taken while building it that change the design:

- **Holiday is postponed only on the employee's demand** (ferieloven § 9): a
  sykmelding stands beside approved ferie and splits it only when HR records the
  employee's demand, with the § 9 (1) deadline judged; spec S9 and its reading 29
  amended ([the reference](docs/src/content/docs/en/reference/staff.md#a-sykmelding-over-other-rows)).
- **HR-only types are decided by `staff:personal-data` alone** — decided by the owner
  (2026-10-14, open question 9 in the plan): no manager, Approver or HR without that
  permission sees such a request, its mail or its history, and nobody decides their
  own; the sole holder's own self-approval comes with C.
- **Colleagues read dates only**: a person's days away as coalesced spans with no row
  id — "absent", with no type, status, percentage, note or hour row, and none of the
  row's events — so a sykmelding over ferie is never seen.
- **The payroll export needs `staff:personal-data` as well as `staff:manage`**
  (decided by the owner, 2026-10-14, open question 10): its leave rows name HR-only
  leave by its description.
  Its Leave ID is a stable surrogate, and ferie counts as holiday under any sickness
  until a postponement is recorded.
- **The a-melding codes follow Skatteetaten's guide and NAV's kodeverk** — six leave
  descriptions plus `permittering`, the expired `velferdspermisjon` and unsplit
  `utdanningspermisjon` not used; leave reported over fourteen days, omsorgsdager
  never. The seven spellings are checked against the kodeliste the payroll system
  validates with before the payroll export is relied on
  ([the payroll export](docs/src/content/docs/en/reference/staff.md#the-payroll-export)).

**C — Accounts, built** on `feat/staff-accounts`
([the spec](docs/superpowers/specs/2026-10-11-staff-foundation-design.md), delivery C;
[the plan](docs/superpowers/plans/2026-10-17-staff-accounts.md);
[the reference](docs/src/content/docs/en/reference/staff.md#accounts)): identity's
**Staff-managed users** — pending until their invitation is accepted, disabled by an Owner
or by Staff, never deleted while managed — and the Owner's **adoption** of an existing user
for Staff under an employee number; the **employee roles** an Owner names, which never
grant administration; the account provisioning contract (`contracts.AccountProvisioning`,
the `Accounts` slot, resolved for the API and the workers alike) with an invitation that
completes a pending user and audit rows for a system actor; the **authority mode**, local
or federated, recorded by the API process; on the person, the **account flag** and its
dates, the managed link and the `staff-accounts` **worker** — the account prepared
`createBeforeDays` ahead of the start, enabled with an invitation on the first day,
disabled the day after the last employment ends — with *Run now*, *Disable now* and the
account attention items; the Account tab, the Accounts settings and identity's users,
invitations and roles pages. `contracts.UserDirectory` gains the active holders of a
permission, and with it the **sole active holder of `staff:personal-data`** approves their
own HR-only request (B's open question 9) and approval mails reach the holders of
`staff:approve` and `staff:manage` when no manager can decide (B's open question 1). Two
migrations, `00050_identity_accounts.sql` and `00051_staff_accounts.sql` (one per schema).

Decisions taken while building it that change the design:

- **The account's address follows the person's record only until the first invitation is
  delivered** (decided by the owner, 2026-10-18); afterwards an Owner changes it in
  identity, so HR cannot redirect an invitation.
- **Leaving disables a Staff-managed account whatever the flag**, and *Disable now* turns
  the flag off (decided by the owner); a leaver loses access whatever the identity
  provider or the worker's configuration says.
- **A permission's holders** come from direct roles, group-mapped roles and the Owner
  wildcard; delegations grant no key (decided by the owner). Holder mails are a fallback,
  sent only when no manager can decide.
- **The worker acts only on an account carrying the person's employee number**, records
  a prepared user on the link before acting on it, and never touches HR's own
  self-service links.
- **A change or correction never moves an absence across the HR-only line**: it is
  cancelled or removed and entered anew.
- **B's open questions 11–13 became settings** (decided by the owner): ammefri and
  svangerskapskontroll held paid by law, a seed's pay editable otherwise, and two erase
  switches — pregnancy and health-appointment rows (pregnancy leave kept at least a year
  after its year), and sick-leave cases' dates — off by default.
- **What identity keeps after Staff's erase** (the managed user, invitations, audit rows)
  is documented with the Owner's procedure to remove it; automating it is left for the
  Entra module.

*Unblocks:* a hire who exists before their first day and can sign in on it, a leaver
who cannot sign in the day after, and an employee who requests holiday from a phone.

### Later

**The Entra module.** Entra ID as the first external identity provider, in a module of
its own (`entra`) that fills an `IdentityProvisioner` slot Staff consumes, so identity
stays the owner of local accounts only. The account Staff's flag creates becomes an
Entra user — created without licences, enabled on the start date, disabled with its
licences, groups and sessions stripped at the end — and the Vantigo login follows
through SSO, correlated on the employee number. **Groups** are a Staff concept that
providers translate: Staff owns the group and its members, listed or derived from a
rule, and a provider declares the **traits** a group can carry for it, which Staff
stores and renders, while the provider creates and maintains the external group. The
OIDC and SCIM configuration moves from environment variables into the database in one
go — identity's SSO-and-SCIM settings table, read at request time — with the Graph
connection the module's own, both on one setup wizard that carries the portal steps.

*Unblocks:* Staff as the authority for who exists in the company's Microsoft tenant;
the joiner, mover and leaver without a second place to type.

**Staff phase 2 — Competence and preferences.** What competence must mean for this
company is discussed before the phase is specified; the starting proposal is flat
categories and competences with an optional level and expiry, a person's competences
with their evidence and verification, interests ("wants to work with"), requirements
on a project role, a task or a shift template, availability declarations and weighted
preferences — and the matching contract written down, implemented with Staffing.

*Unblocks:* "who can do this, and who wants to"; certificates that expire before the
work does.

**Staffing.** One module owning every booking of a person's time — allocations,
assignments with their notice periods and customer terms, and shifts — with staffing
requests, move wishes, matching and shift trades, and employee pages beside the
manager's. It reads Projects through `contracts.ProjectDirectory` and Staff through a
new read contract (the employment on a date, competences valid on a date, preferences
and wishes), the first module to consume two business modules at once; whether
Projects phase 5's allocations move into it or are read by it is its spec's first
question. The statutory limits are warnings an installation can mark as hard, never
built-in refusals.

*Unblocks:* capacity at intake, the unbooked list, and shifts every employee can see
and trade.
