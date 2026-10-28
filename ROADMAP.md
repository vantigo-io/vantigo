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

### Phase 4 — Invoicing groundwork (align with Invoices) (done)

An `internal/contracts` interface (e.g. `contracts.ConsumptionProvider`) returning
billable consumption per customer/metering point/supply period, keeping the
privacy boundary enforced in one place. Price dimension: spot prices per price
area (NO1–NO5) and grid tariffs, decided together with the Invoices module. The
design for this phase and Invoices phase 5 is written
([design](docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md)) and
lands in five deliveries, the three of Energy first.

**Delivery A: spot prices (done)** — the price dimension. The day-ahead price of each
price area is stored as intervals of any length, hourly from the public feed today and
quarter-hourly when the 15-minute market unit reaches it, never two overlapping in one
area. The `energy-spot-prices` worker, off unless an installation opts into the
outbound call (`ENERGY_SPOT_PRICE_FEED_ENABLED`), fetches each area in use from seven
days back to tomorrow from a configured feed through the outbound address guard, under
an advisory lease, and records each day's fetch; **Import prices** reads a file of one
or several areas in a fixed layout, checks it as a dry run and writes every row or
nothing. The **Prices** page shows each area's coverage, the days missing and the last
fetch, and a day's or a month's prices as a chart, behind `energy:prices-view` and
`energy:prices-manage`. See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#spot-prices).

**Delivery A2: the spot-price service (done)** — the shared service as a third source.
In our hosted environments the private `vantigo-spotprices` service collects the
prices; with `ENERGY_SPOT_PRICE_SERVICE_URL` and its key set, the `energy-spot-prices`
worker copies them into the installation's own `energy.spot_prices` — the one store
every read and every later settlement uses — with their state (`final` or
`preliminary`) and provenance, never reading through. One source per installation:
the service or the public feed, never both. A cycle reads each followed area's live
window, fourteen days back to tomorrow, in one request, then walks its history
backwards in 35-day windows to what the service holds, a cycle a minute while it
copies; a fetched day at another resolution is replaced whole, an imported one never.
The client reaches the service at a private address through the outbound guard's one
exception, `netguard.DialContextAllowingPrivate` — RFC 1918 and unique-local only,
`http` only to a private address, the configured host only. The Prices page shows the
source, each area's resolution and the history copied. See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#the-service-sync)
and the
[design](docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md).

**Delivery A3: price history (done)** — the prices over time. A summary read,
`GET /spot-prices/summary`, answers an area's prices per civil hour, day, ISO week or
month in Europe/Oslo — the hour by its UTC hour, so the autumn's repeated hour stays
two — each bucket's time-weighted average (history is hourly before 2025-10-01 and
quarter-hourly after), its lowest and highest price, the minutes covered and whether
any price in it is preliminary, from one SQL statement over the installation's own
rows, its window bounded per bucket. The **Prices** page charts a day or a week
interval by interval and a month, a year or a range of months as averages on a
lowest–highest band — *Month* now shows daily averages, *Week* every interval —,
compares up to five areas, gives the first area's average, lowest and highest with
when, draws preliminary prices dashed under a badge, and keeps the view in the URL.
See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#prices-over-time-the-summary)
and the
[design](docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md).

**Delivery B: tariffs and the billing facts (done)** — what consumption will be priced
by beside the spot price, in migration `00076_energy_tariffs.sql`. The supplier's
power tariffs and the installation's own grid tariffs, each with versions over civil
days `[validFrom, validTo)` that never overlap, each version a list of components of
nine kinds — spot or fixed energy, markups and monthly fees; the grid's energy charge
with its night rate, capacity tiers, fixed charge, electricity tax and Enova levy —
validated in one body with every refusal keyed by its place. A tariff in use is
archived, not deleted (`409` "Tariff in use"), and an archived one still prices what
it is assigned to and can still be corrected. A supply period gains its power tariff
and its billing cadence (monthly, quarterly or yearly), on the create and the switch
and through a new terms `PUT`; a metering point gains its grid tariff, household use
and electricity-tax class, and the VAT treatment derived from household use and the
postal code (`exempt_household_north` for 8000–9999), shown on its page as a hint
Invoices will map. The import writes no tariff and the monthly cadence and keeps a
point's billing facts. The **Tariffs** page and the point's **Billing** card, behind
`energy:tariffs-view` and `energy:tariffs-manage`. Nothing prices yet: that is C's.
See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#tariffs)
and the [plan](docs/superpowers/plans/2026-10-27-energy-tariffs.md).

**Delivery C: settlements and the consumption contract (done)** — consumption priced,
in migration `00077_energy_settlements.sql`. Each supply period's billing periods —
the civil month, quarter or year of its cadence in the point's market zone, clipped to
the supply period — are priced once closed into **settlements**: the current intervals
(a missing, crossing or overlapping one blocks the period), the final spot prices
time-weighted over each interval (a preliminary price blocks it, so does a gap), and
the power and grid tariff versions valid on each civil day, as one line per version and
component — the grid's day and night bands, the capacity tier per month from the three
highest days, monthly fees prorated by counted days, the electricity tax by the
point's class, the Enova levy per kWh or per year — each line carrying the point's VAT
hint. A settlement that cannot be priced is answered with its blocker (`period_open`,
`no_tariff`, `no_consumption`, `incomplete`, `price_gap`, `price_preliminary`) and no
lines. Drafts are recomputed on every read and written with a new revision when a
figure changes, untouched drafts deleted after thirty days; an invoiced period changed
later, within twelve months, gets a correction of the differences. Invoices reads them
through `contracts.ConsumptionProvider` (`DueSupplies`, `Settle`, `Settlements`), the
fifteenth single-provider slot, optional, and stamps and releases them through
Energy's invoiced-work holder (`energy.settlement`, last in the lock order); a customer
merge re-points them. The metering point's **Settlements** card lists each period with
its state, kWh, amount, blocker, lines and invoice, behind `energy:supply-periods-view`
and `energy:consumption-view`. See
[`docs/src/content/docs/en/reference/energy.md`](docs/src/content/docs/en/reference/energy.md#settlements)
and the [plan](docs/superpowers/plans/2026-10-27-energy-settlements.md).

Energy phase 4 is done, and so is Invoices phase 5, which bills it: **D** invoices
from consumption (the wizard) and **E** billing runs.

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

Moved to [Planning](#planning), which owns every booking of a person's time; Projects keeps the demand side (phases, placeholders) once phase 4 lands.

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

### Phase 5 — Energy consumption billing (done)

Periodic invoices per supply point and period from Energy's figures, tariff and price
lines, batch issuing, the utilities' periodic-invoicing rules — with Energy phase 4's
consumption contract. The design is written
([design](docs/superpowers/specs/2026-10-24-energy-consumption-billing-design.md)); Energy
phase 4's deliveries A–C have landed.

**Delivery D: invoices from consumption (done)** — on `feat/invoices-energy`, migration
`00078_invoices_energy.sql`. A customer's settled consumption becomes an invoice draft
through the **From consumption** wizard: Energy's settlements read through
`contracts.ConsumptionProvider`, one line per settlement line in the buyer's language —
the component, its band or month, the GSRN, the address and the period — its VAT code
by the line's hint (the settings' two energy codes; the north's household exemption on
the seeded code `5N`, mval. § 6-6), a correction's lines as ±1 × the difference, a unit
price of up to eight decimals so the amount wins. A settlement is a line source
(`energy.settlement`, no project) held over its lines, judged fresh on a read, rebuilt
by a refresh, refused at issue when it moved to another customer, stamped by Energy's
holder last and released once credit notes have returned every one of its lines; a
consumer's yearly supply is refused. The PDF prints a block per metering point and, for
a person, the strømpris.no and Elklagenemnda footer; EHF carries the metering point on
each line. The uninvoiced panel gains a **Consumption** section, the settings the two
energy VAT codes. See
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md#invoicing-consumption)
and the [plan](docs/superpowers/plans/2026-10-27-invoices-energy.md).

**Delivery E: billing runs (done)** — on `feat/invoices-billing-runs`, migration
`00079_invoices_billing_runs.sql`. A **billing run** settles, drafts and — when asked —
issues one invoice per customer with consumption due through a date: every customer
Energy's `DueSupplies` names (its new `Every` read) or the ones named, one run at a time.
The `invoices-billing` worker carries the customers one at a time under a 10-minute
lease, through the **From consumption** wizard's and the issue's own code paths, as the
run's creator; a customer whose lines do not fit one document is split by metering
point at 500 lines. Each customer's item records what it made, or why it was skipped
(the wizard's refusal, the metering point named) or failed (the issue's refusal, or a
third error); an error is retried after 1 and 2 minutes. A run is cancelled with its
customers not yet reached; sending stays a step of its own. The **Billing runs** pages
make a run, list them and follow one; `module.Workers` resolves `Projects`, the three
`Billable*` reads and `Consumption` as Compose does. See
[`docs/src/content/docs/en/reference/invoices.md`](docs/src/content/docs/en/reference/invoices.md#billing-runs)
and the [plan](docs/superpowers/plans/2026-10-27-invoices-billing-runs.md).

**Invoices phase 5 is complete.** Left out on purpose (design D12): sending a run's
documents in one go, a run on a schedule, advance invoicing and a-konto for energy,
gjennomfakturering, strømstøtte and Norgespris, eFaktura and AvtaleGiro.

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

### Phase 1 — The calendar, invitations and the feed (done)

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

**B — Invitations, built** on `feat/calendar-invitations`
([the design](docs/superpowers/specs/2026-10-19-calendar-invitations-design.md);
[the plan](docs/superpowers/plans/2026-10-19-calendar-invitations.md);
[the reference](docs/src/content/docs/en/reference/calendar.md#invitations);
[the operator's page](docs/src/content/docs/en/admin/calendar.md)): three kinds of
attendee on one table — a colleague, a customer's contact named by its opaque id and
read through the customer directory each time it is mailed or shown, and a typed
address — at most 50 together, a contact only for a writer holding
`customers:contacts-view`; `GET /contacts` for the picker, through a new
`CustomerDirectory.ContactsForCustomer`; one invitation per outside attendee in the iMIP
layout — the text, the HTML and a `text/calendar; method=REQUEST` alternative, and the
same ICS as `invite.ics` — through the new `mail.CalendarPart`, with `UID` the row's
`ical_uid`, `SEQUENCE` the row's, a series' `RRULE`, an `EXDATE` per cancelled occurrence
and a `RECURRENCE-ID` event per moved one, in the organiser's language; sent after the
commit under a thirty-second budget, by the table of when it goes — a create, a change
of when or where, an attendee added or dropped, an occurrence changed, a cancellation,
the owner's **Resend** — with each send's outcome on the attendee; the answer through a
web link — a hashed token, the link sealed under `calendar/rsvp-link` so every mail
carries the same one — on two anonymous, rate-limited operations and the `/rsvp` page,
on the public host's list beside the pay page; a colleague, and the owner on an answer,
mailed through a new `contracts.UserDirectory.Notify`, so no address crosses into the
calendar; a delete of an appointment someone outside has been sent become a
cancellation — mailed, kept read-only and off every calendar — and erased after its last
day by the `calendar-cancellations` worker (`CALENDAR_CANCELLATIONS_ENABLED`,
`CALENDAR_WORKERS_POLL`). One migration, `00056_calendar_invitations.sql`.

Decisions taken while building it:

- **From and `ORGANIZER` are both `SMTP_FROM`**, under the organiser's name — the one
  arrangement the research found no client refuses — so the owner's address never
  leaves identity, and the native buttons' `REPLY` lands in a mailbox nobody reads; the
  mail says so and asks for the link. The inbound `METHOD:REPLY` route stays under
  *Later*, and the ICS is written so it can be added.
- **The link does not require a public host**: it is on `PUBLIC_BASE_URL` when set, and
  on `APP_URL` otherwise, where the page is sessionless all the same — the pay link's
  rule was not copied, since an invitee on the company's own network needs none.
- **Nothing is queued and nothing retries**: a failed send is on the attendee row and
  in the log, and the owner resends. Communications' outbox remains the module for mail
  that must arrive.
- **A colleague gets a plain notice and no ICS**, and their address is never in a file
  that leaves the company; the ICS lists the outside attendees only.
- **A contact's address is never copied and never on the appointment**: it is read at
  send time, so a corrected address is used by the next mail, and an anonymised contact
  is simply not mailed — the module registers no `CustomerPersonalData` holder, since it
  holds nothing of the contact's but an id.
- **A timed series is written in its own zone**: a single appointment's instants are UTC,
  which is exact, but a series' `DTSTART`, `EXDATE`s and `RECURRENCE-ID`s are wall-clock
  with `TZID` and a `VTIMEZONE` walked from Go's tzdata (one component per transition
  over the series' span, three years for an unbounded one), since a UTC `DTSTART` with an
  `RRULE` would move a 09:00 meeting by an hour across the clock change. The feed (C)
  reuses the writer.
- **The organiser's language is Norwegian for `nb` and English otherwise**, a preference
  of none included, for the mail and the answer page alike; the page names no company,
  since no contract answers the workspace's name.
- **A colleague is told of every change to their place on it**: invited (on a create or
  any `PUT` that adds them), changed (a change of when or where), cancelled (a
  cancellation, and a delete that removes the row), removed; one occurrence's change
  tells nobody.

*Unblocks:* meetings with customers booked from Vantigo, answered from any mail client.

**C — The feed and the subscriptions, built** on `feat/calendar-feed`
([the design](docs/superpowers/specs/2026-10-20-calendar-feed-design.md);
[the plan](docs/superpowers/plans/2026-10-20-calendar-feed.md);
[the reference](docs/src/content/docs/en/reference/calendar.md#the-feed);
[the operator's page](docs/src/content/docs/en/admin/calendar.md#the-feed-on-the-public-host)):
a calendar address per person in two variants, `full` and `busy`, behind a token of 32
random bytes stored only as its SHA-256 and the whole address sealed under
`calendar/feed-address`, created, reset and turned off from a **My calendar** screen;
`GET /feed/{token}.ics`, the module's one anonymous `GET`, on the public host's list,
rate limited to 120 per 10 minutes per client address — today −3 to +12 months in the
business zone, series as `RRULE` with their `EXDATE`s and moved occurrences, one
`VTIMEZONE` per zone, the projected items in the full variant with `CATEGORIES`, `URL`
and `✓ ` for a done one, an hour's `REFRESH-INTERVAL`, an `ETag` and 304, and a 1 MiB
bound that drops the farthest projected items first; the providers read for a person
with no session behind the request through `contracts.AsUser` and a new
`Access.UserHolds`, each under its own gate; calendars from elsewhere subscribed to — at
most ten, `webcal` taken as `https` — fetched once when added or when its address
changes, on **Refresh now**, and by the `calendar-subscriptions` worker (`CALENDAR_SUBSCRIPTIONS_ENABLED`,
`CALENDAR_SUBSCRIPTIONS_POLL`) through `netguard`, with no redirect, no proxy, at
most 1 MiB and 10,000 events, and 30 inline fetches per client address per 10 minutes,
the last good copy kept through a failure and the reason shown on the row;
parsed on read into the kind `calendar.subscription`, drawn in the colour the person
chose, busy-only when they ask, and never in their feed. One migration,
`00057_calendar_feed.sql`.

Decisions taken while building it:

- **The address is shown again, not once**: the design's "shown once" gave way to B's
  precedent — the whole address sealed, opened for its owner's screen alone — since a
  person sets up a phone today and a laptop next month, and a secret they must reset to
  see is one they paste into a note. The hash is still what a request is matched by, so
  a rotated `APP_SECRET` hides the addresses from the screen and breaks none.
- **One path for the three feed operations, and `.ics` inside the segment**: OpenAPI
  refuses two paths differing only in a parameter's name, so the create or reset and
  the turn-off take the variant in the segment the anonymous `GET` takes the token in;
  and the router matches a parameter to a whole segment, so the handler takes
  `<token>.ics` as one and requires the suffix.
- **The feed asks the providers through `AsUser` and `UserHolds`**: the gate stays the
  provider's and identity still answers it — the session query's person checks through
  one query, `UserMayAct`, not locked out, an Owner holding all, no MFA rule without a
  session — rather than a flag on the query the
  providers would honour, which would have shown a person their tasks after
  `projects:access` was taken from them.
- **The busy variant is spans alone**: busy appointments and the timed projected items,
  each titled *Busy* (*Opptatt* in `nb`); free appointments and all-day items are left
  out, since a day with a task on it is not a busy day.
- **Declined and cancelled appointments are left out** of both variants, and so are a
  colleague's appointments, the subscriptions and the public holidays.
- **No `ORGANIZER` and no `ATTENDEE`** in the feed: the copy is read-only, and an
  address in a file on a phone is one the module need not put there.
- **The `ETag` ignores `DTSTAMP`**: the hash of the calendar without its `DTSTAMP` lines,
  so an unchanged calendar keeps its tag from hour to hour; no `Last-Modified`, since a
  provider's items have no one time they changed at.
- **Subscriptions are at most ten, parsed on read, never in the feed**: nothing is
  reconciled into the module's tables, so two people subscribed to each other's feeds
  cannot copy each other back and forth, and a subscription's body never leaves the
  installation.
- **The worker wakes every five minutes, and each subscription is due a poll plus
  jitter**: `next_fetch_at` is the poll or, after a fetch that answered the calendar, the
  feed's own `REFRESH-INTERVAL` or `X-PUBLISHED-TTL` (capped at a day), whichever is
  longer, plus up to a tenth of the poll, so installations drift apart; a 304 or a
  failure waits the poll alone; no backoff, since a feed down for an hour costs two requests.
- **`TZID` is resolved in a chain**: Go's tzdata, the `VTIMEZONE`'s `X-LIC-LOCATION`, the
  CLDR table of Windows names Outlook writes, the `STANDARD` offset, and UTC last.

And after the review:

- **A token answers nothing once its person may not act**: the feed asks `UserHolds`
  for the token's person and `calendar:access` before it reads anything, and a person
  disabled, locked out, pending, deactivated by the directory under SCIM, or without the
  permission, gets the bare 404 an unknown token gets, with no `last_used_at` written.
  `UserHolds` now applies the session query's person checks through `UserMayAct`, which
  must agree with `GetSessionByTokenHash`: a token outlives nothing a session would not.
  The subscriptions worker asks the same, once per person per cycle, and puts a departed
  person's rows off by the poll rather than fetch for nobody.
- **The parse on read is bounded**: one budget of 200,000 steps per subscription per
  read shared by every event and rule, at most 10,000 `VEVENT`s read, a one-second
  deadline, and a bounded heap keeping the 5,000 items nearest the window's start; a read
  that reaches a bound answers what it has and logs it. At fetch, a calendar of more than
  10,000 events is refused — "the calendar has more than 10,000 events" — since it would
  be parsed on every load.
- **The busy variant's projected `UID`s are opaque**: a MAC under
  `calendar/feed-busy-uid` of the person's id and the item's kind and id, so a busy feed
  names neither the module nor the row and two people's cannot be joined; points, which
  are no span, are left out of it.
- **The three inline fetches are rate limited together**: a save, a change and
  **Refresh now** share `calendar-subscription-fetch`, 30 per client address per 10
  minutes, since each is an outbound request of up to twenty seconds made on a click.
- **The tenth subscription is taken under a per-person lock**, and **a fetch overtaken
  by a change of address records nothing**: the record matches the address it was
  fetched from, and a row deleted meanwhile is a 404.
- **The guarded transport keeps no idle connection**, and **a parse error is logged
  without the body's text**.

*Unblocks:* the Vantigo calendar on a phone, and a person's other calendars beside it.

### Phase 2 — Outlook sync (done)

Researched in
[`docs/superpowers/research/2026-10-27-calendar-outlook-sync.md`](docs/superpowers/research/2026-10-27-calendar-outlook-sync.md)
and designed in
[`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`](docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md):
a person who works in Outlook sees in it what Vantigo holds for them, and in time their
Outlook busy time in Vantigo and their edits in Outlook taken back. App-only, through the
[Entra](#entra) module's connection: an Owner's switch, one admin consent, and a toggle
per person. Four deliveries, each a pull request against `main` after the previous one
lands; the design decides A in full and B–D far enough that A need not be undone.

**A — The switch, the person's setting and the push, built** on
`feat/calendar-outlook-sync`
([the plan](docs/superpowers/plans/2026-10-27-calendar-outlook-sync.md);
[the reference](docs/src/content/docs/en/reference/calendar.md#outlook-sync);
[the mailbox calendar](docs/src/content/docs/en/reference/entra.md#the-mailbox-calendar);
[the operator's page](docs/src/content/docs/en/admin/entra.md#calendar-sync)): a new
provider-neutral contract, `contracts.MailboxCalendar`, in a new single-provider slot
filled by Entra and resolved by Compose and Workers — the sixth sanctioned cross-module
write, held to rule 13's lock discipline; Staff's `Workforce.DirectoryAccount`, the
caller's own account in an external directory; the Owner's **Calendar sync** switch on
the Entra page, which asks for `Calendars.ReadWrite` only while it is on and answers
`calendarSync` with its status; the person's **Outlook** card on **Calendar → My
calendar** — where (the main calendar or a separate *Vantigo* one), what (appointments
and the provider kinds), titles (full or *Busy*), *Sync now*, and why it cannot run in
their words; and the `calendar-mailbox-sync` worker (`CALENDAR_MAILBOX_SYNC_ENABLED`,
`CALENDAR_MAILBOX_SYNC_POLL`), which pushes each occurrence of their appointments and
the chosen items from 30 days back to 180 ahead, at most 1500, through Graph `$batch`,
nudged within a minute by every appointment write, reconciled daily against the
events carrying its marker, and cleaned up when the person, their rights, their account
or the switch go. Two migrations, `00081_calendar_mailbox_sync.sql` and
`00082_entra_calendar_sync.sql`.

Decisions taken while building it:

- **Nothing of a person's mailbox is guessed**: the mailbox is the provider link of the
  person Staff's identity link names, and Entra writes only to an account it made or
  whose adoption is confirmed, and not deleted.
- **Vantigo stays the authority**: what it writes is a copy, tagged with a marker,
  without attendees or reminders; an edit made in Outlook is put back until C.
- **Occurrences, not series**: each occurrence its own event, so a moved one in Outlook
  maps onto the calendar's own exception when C comes.
- **The reconcile adopts and never sweeps**: an event carrying a wanted key is adopted, a
  duplicate of a mapped key deleted, a mapped event no longer found forgotten — but an
  event carrying a key not wanted is left, since it is most often a copy that slid
  before the window and the past stays.
- **The link is the app's**, `APP_URL` and the base path, as the feed's and the notices'
  are — never the public host, which serves only its allowlist.
- **The clean-up follows what was made, and never forgets on a refusal**: the *Vantigo*
  calendar is deleted when it carries the installation's marker, else the marked copies
  in it; in the main calendar every marked copy is deleted, whatever its date. Only a
  mailbox gone, or an account no longer managed, is forgotten; a mailbox denied or a
  provider unusable keeps the clean-up owed, on the backoff.
- **A stale setting is 409 `revision_mismatch`**, the module's existing refusal, and an
  unavailable sync is 409 `mailbox_sync_unavailable` with the availability as its
  detail.
- **The token's role does not gate the sync**: a grant made with Exchange RBAC for
  Applications never shows in the token, so the provider is available whenever the
  switch is on and the connection connected, `calendarSync`'s `permission_missing` is
  advisory, and a mailbox outside the grant is that person's `mailbox_denied`. Cleanup
  needs a working credential, and `switch_off` is told the person whatever the state.
- **The markers name the installation**: `vantigo:<client id>` on the calendars the
  module makes and `vantigo:<client id>:<key>` on its events, so a calendar of the
  person's own, or another installation's copies in a shared tenant, are never adopted,
  deleted or found.
- **Bounded work**: three minutes per person, the first failed slice stops the sync, the
  1500 cap is hard (appointments trimmed farthest-first after the projected items), the
  `PUT` rate limited (`calendar-mailbox-sync-write`, 10 per 10 minutes), and errors
  logged by their codes alone.
- **The daily reconcile is derived**, not scheduled: a person's first sync on a business
  day later than their `last_synced_at`'s reads the mailbox's list first.
- **Graph's bare `ResourceNotFound` is not a mailbox gone**, so an event gone is never
  read as its mailbox gone.

*Unblocks:* a person's Vantigo appointments, absence and chosen items in Outlook, and
their busy time there for colleagues.

**B — Outlook's busy time in Vantigo, built** on `feat/calendar-outlook-busy`
([the plan](docs/superpowers/plans/2026-10-27-calendar-outlook-busy.md);
[the busy read](docs/src/content/docs/en/reference/calendar.md#the-busy-read);
[the busy time contract](docs/src/content/docs/en/reference/calendar.md#the-busy-time-contract);
[Entra's reads](docs/src/content/docs/en/reference/entra.md#the-reads)): after each push
that worked, the same worker reads the person's default calendar through Graph's
`calendarView/delta` — two new calls on `contracts.MailboxCalendar`, `Delta` and
`Marked`, refused under cleanup — over its own window, 30 days back to 210 ahead,
restarted about monthly; the calendar's own copies skipped by the map and the marker
read; `calendar.mailbox_busy` keeping each busy, tentative, out-of-office or
working-elsewhere event's times and `showAs` alone, never a subject, at most 5000 a
person, forgotten when the sync goes off or unavailable. A new kind `calendar.outlook`
on the person's calendar, labelled by `showAs`; a colleague's calendar shows their busy
and out-of-office time as untitled blocks; and the calendar's first provided contract,
`contracts.CalendarBusy` — a new single-provider slot, provider-gated by the calendar's
colleague rule — from which Planning's board shows each person's busy hours and days a
week. Migration `00083_calendar_mailbox_busy.sql`.

Decisions taken while building it:

- **Ours by the map and one read, not `transactionId`**: a delta answers no extended
  property, so a copy is known by the map, or by `Marked` for a single event the map
  lacks; `transactionId` would collide with A's copies re-made after a `Gone`.
- **A link is held to its mailbox**: Entra follows only a link of that mailbox's delta,
  so a stored link can point it nowhere else; an expired one restarts the delta once a
  sync.
- **Bounded reads**: 40 pages of 50 a call, each page read through a 16 MiB cap and
  written in its own short transaction; a read whose three minutes end between pages
  keeps its next link and is not a failure; the 5000 cap stops new events only; an event
  whose times do not read is skipped rather than holding the delta.
- **Shown, never subtracted**: Planning's `busyHours` and `busyDays` stand beside the
  load, absent when the calendar answers nothing, on each person's row and never a
  project's sum; a failing read is the board's 503. The timeline is unchanged.
- **The person's own Outlook titles are deferred** to C, which reads events whole
  anyway.

*Unblocks:* a person's Outlook meetings visible to the colleagues and planners who book
them in Vantigo.

**C — The write-back, built** on `feat/calendar-outlook-writeback`
([the plan](docs/superpowers/plans/2026-10-28-calendar-outlook-writeback.md);
[the write-back](docs/src/content/docs/en/reference/calendar.md#the-write-back);
[Entra's write-back reads](docs/src/content/docs/en/reference/entra.md#the-write-backs-reads)):
before each push the worker asks the provider for the copies' versions — two new calls
on `contracts.MailboxCalendar`, `Versions` (ids and change keys: the default calendar's
marked events, or every event of the *Vantigo* calendar's view) and `Events` (read whole
by id through the calendar's own path, in `$batch`es of twenty), both refused under
cleanup — and reads whole only the moved, the missing and the new, at most 100 a sync.
An owner's edit or move of a single appointment's copy becomes a `PUT` of the
appointment, an occurrence's move a `PUT …/occurrences/{date}`, and a delete the
module's own `DELETE` (a cancellation, with the CANCELs, once outsiders were invited) or
an occurrence cancelled — each through the screen's own code, on the person's behalf, at
most 20 a sync, with its `SEQUENCE`, invitations and notices. Vantigo wins a conflict;
an attendee's copy, a projected item's and what an occurrence cannot carry are put back,
and the person's **Outlook** card names the last change put back by its reason; a copy
the person turned into a meeting is released, never written again; an event made in the
*Vantigo* calendar becomes an appointment once, its map row written in the create's
transaction. Migration `00084_calendar_mailbox_writeback.sql`.

Decisions taken while building it:

- **Versions every sync, not the delta**: a delta answers no extended property, the
  *Vantigo* calendar has no v1.0 delta, and a removal cannot tell a delete from a move;
  a copy whose change key is unchanged is never read, and a moved one equal to what was
  written is Exchange's noise.
- **The screen's own paths**: the `PUT`, occurrence, `DELETE` and create handlers were
  split into internal forms the worker calls, so validation, revisions and mails are
  the same code.
- **An absence is a delete only when it can be**: a complete list, the row in the
  window, the directory's own 404 through the calendar's own path (`Events` answers it
  in `Gone`; an id it could not read infers nothing); and a reconcile after a
  write-back that did not complete forgets nothing.
- **A breaker on deletes**: more than ten of a person's own appointments deleted in
  Outlook in one sync are none of them taken — every copy put back, the card saying
  so (`many_deleted`); a delete of an appointment Vantigo changed since is put back as a
  conflict, and a copy whose last write is unknown is never taken.
- **A meeting is the person's**: one `Find` answers as a meeting is never adopted nor
  deleted, and the *Vantigo* calendar is not deleted whole while it holds one.
- **A failed read does not hide the push**: it is recorded `active` with `failed` on
  the backoff, as a failed busy read; that push may then write Vantigo's version over an
  unread edit.
- **The person's own Outlook titles are not shown**: only the copies and the *Vantigo*
  calendar are read whole, never the person's own events.

*Unblocks:* a person who works in Outlook, on a phone, changing their own appointments
there.

**D — Change notifications, built** on `feat/calendar-outlook-notifications`
([the plan](docs/superpowers/plans/2026-10-28-calendar-outlook-notifications.md);
[the calendar's side](docs/src/content/docs/en/reference/calendar.md#change-notifications);
[Entra's subscriptions and webhook](docs/src/content/docs/en/reference/entra.md#change-notifications)):
for an installation Microsoft can reach (`PUBLIC_BASE_URL`, and
`CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`, on by default), a change made in a person's
Outlook makes their sync due within about a minute instead of at the next poll. Three
new calls on `contracts.MailboxCalendar`: `Watch` (one Graph subscription per mailbox on
`/users/{id}/events`, renewed within three days of its expiry or when Microsoft asks to
reauthorize it), `Unwatch` (in every clean-up but a target change's) and `Notified` (the
mailboxes notified since a time, each cycle). Entra's anonymous webhook,
`POST /api/v1/entra/mailbox-notifications`, is on the public host's list; the Entra page
shows the state under *Calendar sync*. Migration `00085_entra_mailbox_subscriptions.sql`.

Decisions taken while building it:

- **A notification is a nudge**: it sets one time and is trusted for nothing else; the
  poll runs unchanged, so an unreachable address or a refused mailbox costs only latency.
- **The webhook reveals nothing**: anonymous, 600 a minute a client, answering the
  handshake only while a create is in flight, and 202 to any body that decodes — a wrong
  client state, an unknown subscription and a valid one alike. The client state is drawn
  per create and only its SHA-256 is kept, compared in constant time.
- **An unreachable address is not retried per person**: a create whose handshake fails
  holds every create off for an hour.
- **A clean-up owes the delete**: `Unwatch` skips the account check, so a subscription
  can go after the account has; a refusal keeps the clean-up owed.
- **Notified, not a callback**: the provider never calls the calendar; the worker asks
  each cycle, from a time held in memory.

*Unblocks:* a change made in Outlook arriving as fast as one made in Vantigo.

Phase 2 is complete. What is left is out of scope for now (the *Later* list below), and
checked against a real tenant: whether the subscription also sees the *Vantigo* calendar
and how fast Graph notifies.

### Later

Two-way sync with Google · booking pages · rooms and resources · reminders
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
**this phase → the Entra module → Staff phase 2 → [Planning](#planning).**

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

### Phase 2 — Competence and preferences (built)

What competence means for this company was discussed first, in
[`docs/superpowers/research/2026-10-25-staff-competence.md`](docs/superpowers/research/2026-10-25-staff-competence.md)
— the person on My page and the manager in the one-to-one, and the sixteen decisions
the owner took — and designed in
[`docs/superpowers/specs/2026-10-25-staff-competence-design.md`](docs/superpowers/specs/2026-10-25-staff-competence-design.md).
Three deliveries, three pull requests, one migration each, each opened against `main`
after the previous one lands.

**A — The catalogue and the person's competences, built** on
`claude/staff-phase2-competence-7c8b5e`
([the plan](docs/superpowers/plans/2026-10-25-staff-competence.md);
[the reference](docs/src/content/docs/en/reference/staff.md#competence)): a flat
catalogue of competence categories and competences — skills with a level on one
0–4 scale, certificates with a validity and a reminder lead — with the five categories
and fifteen Norwegian certificates seeded, hidden rather than archived; a person's
competences, declared by the person on My page from a phone, with the certificate
photographed as evidence into their archive under `certificate` (the first write a
person makes into their own archive), verified by their manager, `staff:approve` or
HR — never by themselves — renewed as new rows that keep the old as history, and
removed; the `staff-competence` worker, which mails the person and their managers once
when a certificate comes within its lead and once when it has expired; the three
attention items; the events, the exports' `competences` section and the D erase of the
rows; `contracts.Workforce`, the sole-provider slot through which [Planning](#planning) reads the
catalogue and who holds what, valid on a date, under the Colleague rule; and the
Competence tab on My page and the person page, the two catalogue cards in the Staff
settings and the search's **Add competence**. One migration, `00066_staff_competence.sql`.

Decisions taken while building it that change the design:

- **The visible catalogue is everyone's**: every `staff:access` holder reads the
  categories and the competences neither hidden nor archived, since the person
  declaring needs them; `includeHidden` and `includeArchived` take effect for
  `staff:manage` alone.
- **No new erase kind**: `person_competences` is class D whole and is deleted under
  the kind `D`, as the next of kin are; the evidence is erased as a `certificate`
  document.
- **Row operations live at `/person-competences/{id}`**, as documents and absences do,
  with the list and the creation under `/people/{id}/competences`; the evidence is
  downloaded from the row as well.
- **The worker stamps before it mails**, each row in a transaction of its own under the
  row's lock; a failed send is logged and never retried — the dashboard item is the
  durable signal — and a corrected end date is mailed again when due.
- **A manager's or HR's entry is verified at once**, basis `observed`: they vouch for
  it by entering it.
- **Settings has no tabs**: the catalogue is two cards, *Competence categories* and
  *Competences*, after *Document categories*.
- **Removing a renewed row undoes the renewal**, the earlier row current again; HR
  removes the whole history with `chain`. A skill carries no validity and is not
  renewed (`skill_no_validity`).
- **`contracts.Workforce` answers people active today**, whatever date is asked, and
  `HeldCompetences` needs `staff:view` alone.

**B — The one-to-one, built** on `claude/staff-phase-2-delivery-b-2ec898`
([the plan](docs/superpowers/plans/2026-10-25-staff-one-to-one.md);
[the reference](docs/src/content/docs/en/reference/staff.md#what-the-person-wants)):
what the person wants, written by them alone on My page and read by their manager and
HR, never by colleagues or the Approver — interests in competences, read as *wants to
learn* or *wants to use more* by whether they hold it; preferences of seven kinds
(weekdays, time of day, locations, travel, remote work, most consecutive days, most
hours a week), each weighted *nice to have*, *matters* or *important*, with no free
text; and an availability declaration of recurring and dated rows, data only until
[Planning](#planning) reads it — each list replaced whole and refused per row; the manager's or HR's
**Seen**, stamped per row on the three lists and on the person's current competence
rows, so what was added or changed since the one-to-one stands out; the competence
review the manager and the person record together — a day and the reviewer, no notes —
with `lastCompetenceReview` on the person, the review interval in the settings (12
months, 0 off) and the `competenceReviewOverdue` attention item, counted from the later
of the last review and the start of the current unbroken employment; the five events,
the exports' four sections and the D erase of the four tables; and on the apps the
tab's second half, *What I want*, the manager's view with **Seen** and **Reviewed
today**, My team's cards and the settings card.

Decisions taken while building it that change the design:

- **The Seen is the manager's and HR's, never the Approver's**: the Approver reads no
  wishes and stamps none; the person carries `canReadWishes` and `canStampWishesSeen`,
  so the apps hold no audience rule.
- **The Seen reaches the competence rows**: every current row without a stamp is
  stamped too, so a declaration read but not yet verified is acknowledged.
- **A whole-list replace is a diff, not a wipe**: an unchanged row keeps its row and
  its stamp, a new or changed one is written unstamped, so "changed since" is "not yet
  stamped"; a hidden or archived competence and an archived location are judged on new
  and changed rows alone.
- **No CHECK constraints** on the four tables: the module's validation owns the rules
  and reports them per field.
- **No `If-Match` and no revision on the lists**: the person is their only writer.
- **The events carry counts and kinds, never values**: the interests and declarations
  before and after, the preference kinds touched, the rows stamped, the review's day.
- **No new refusal code**: every rule is a field error at its index;
  `competence_unavailable` and `own_record` are reused.

One migration, `00067_staff_one_to_one.sql`.

**C — Profiles, gaps and the overview, built** on `claude/staff-phase2-delivery-c-396013`
([the plan](docs/superpowers/plans/2026-10-25-staff-competence-profiles.md);
[the reference](docs/src/content/docs/en/reference/staff.md#profiles-and-gaps)): competence
profiles — what a position needs, a named list of competences, each with an optional
minimum level and whether it must be valid — kept by HR in a catalogue every
`staff:access` holder reads, archived rather than deleted; a profile assigned to a person
by HR as a set, or made a department's default and so reaching everyone whose terms
place them there today; the gap list at the top of the Competence tab — **What your
profile asks** on My page, **What their profile asks** for the manager — judging each
item *met*, *declared only*, *below the level*, *expiring*, *expired* or *missing* on the
person's current row, with **Assign profiles** for HR; the `competenceGap` attention item,
one per person with the items not met, for their manager and HR, and My team's
**Profiles not met** card; the Competence page — the matrix of who holds what, valid on a
day, filtered by category, competences, minimum level and verified only, declared rows
badged, and, for HR, the interests overview of who wants to learn or use more of what;
Settings' **Competence profiles** card and the department form's **Default competence
profile**; the three events, the exports' `profileAssignments` and the D erase of the
assignments; and the matching contract's Staff side — `PreferenceFit`'s shape and rule,
a score and the kinds that lowered it — written into the reference's Workforce section.

Decisions taken while building it that change the design:

- **The gap statuses are judged in a fixed order** on the current row — missing,
  declared certificate, below the level, not valid (expired, or missing before its
  first day), expiring, met; a declared skill at the level is met with the `declared`
  flag, and an archived competence's item stays and counts. *Not met* is every status
  but met and expiring.
- **`mustBeValid: false` judges no validity**: an expired row is met, and `expiring` is
  never answered for such an item.
- **A profile reaches a person two ways, and the list says which**: `assigned`, and
  `department` — the department named by the terms in force today of the person's place,
  the earliest employment in force by start then id, as a group's rule judges it.
- **An archived profile reaches nobody**: it stays listed on the person, raises no gap
  and no item, and a department keeps it as its default without effect; assigning one
  anew, or making it a department's new default, is a plain field error.
- **A profile's key never changes**, its items are replaced whole under `If-Match`, and
  the catalogue writes `competence_profile_created` and `competence_profile_updated` as
  installation events.
- **A profile's items**: at most 50, each competence once; `competence_unavailable` on a
  new or changed item only, so an unchanged item on a competence archived since is
  kept; `minLevel` only on a competence with a level (`level_not_allowed`); an item's
  `minLevel` puts the competence in use for `competence_in_use`.
- **The matrix is the directory with competences**: `staff:view` or `staff:manage`,
  everyone active today, the rows valid on the day asked; a declared certificate is
  shown with its badge and dropped only by *verified only*; competences named together
  keep the people who hold every one.
- **The interests overview is `staff:manage`'s alone**, never the Approver's, over
  people active today, the bucket decided by whether they hold the competence.
- **The `competenceGap` item** is one per person, counted, dated today, for their
  manager and HR — never `staff:approve` alone, never one's own — and read only when
  the caller manages someone or holds `staff:manage`, so nobody else's dashboard pays
  for it.
- **A new field group, *Profiles and gaps***, read by the person, their manager, the
  Approver, HR and `staff:personal-data`, never a colleague; assigning is
  `staff:manage`'s, carried to the apps as `canAssignProfiles`.
- **`profileAssignments` is in both exports**, the person's own included, since the tab
  shows them their profiles.
- **`profiles_assigned` carries the profiles' keys**, sorted, before and after —
  catalogue words naming a position, not the person's — class D.
- **A department's default is a full-replace field**: left out, it is cleared; the
  department's write records no event, as before.
- **The profiles catalogue is everyone's to read**, archived ones included, and **no
  `PreferenceFit` method was added then**: its shape and rule went into the reference,
  and it joined `contracts.Workforce` as the batch `PreferenceFits` with
  [Planning](#planning)'s delivery B.

One migration, `00068_staff_competence_profiles.sql`.

*Unblocks:* "who can do this, and who wants to"; certificates that expire before the
work does.

**The person's photo and the at-a-glance cards, built** on `claude/staff-person-insights`
([the spec](docs/superpowers/specs/2026-10-25-staff-person-glance-design.md);
[the plan](docs/superpowers/plans/2026-10-25-staff-person-glance.md);
[the reference](docs/src/content/docs/en/reference/staff.md#at-a-glance)): a photo per
person in the page's header and on My page — the register's own, not identity's avatar —
set by the person, `staff:manage` or `staff:personal-data`, a JPEG or PNG judged by its
own bytes, its metadata stripped, replaced whole and its old object removed through the
deletion ledger, erased with the record; and **At a glance** at the top of the Profile
tab, one read (`GET /people/{id}/glance`) answering each card the caller may read — sick
days this year, the egenmelding standing, holiday taken, planned and left, sick-child
days, the position and tenure, absence now and next with an open case, the contract
check for HR, and this week's hours from Time against the contract. Decisions taken
while building it:

- **Staff reads Time optionally**, through a new consumer-gated contract,
  `contracts.Timesheets`: Time authorizes nothing, Staff's manager rule decides, and with
  `time` off the card says time tracking is off; `MODULES=staff` stays valid.
- **The cards count as the tabs do**: sick days by the payroll file's per-day rule over
  the whole calendar year, holiday from the account (planned includes requests), the
  egenmelding standing over its twelve months; a manager reads the sick-child count but
  never the entitlement.
- **A colleague reads only dates**: the position's start, tenure, FTE and title, and
  absence now and next as coalesced *absent* spans; the contract card is `staff:manage`'s
  alone, the hours card the person's, their manager's and HR's — never the Approver's.
- **Expected hours are the contract's week** over every employment in force, not reduced
  by absence.

One migration, `00069_staff_person_photo.sql`.

**The photo follows the account, built** on `claude/staff-photo-sync`
([the spec](docs/superpowers/specs/2026-10-25-staff-photo-sync-design.md);
[the plan](docs/superpowers/plans/2026-10-25-staff-photo-sync.md);
[the reference](docs/src/content/docs/en/reference/staff.md#the-photo)): one picture of a
person everywhere. HR's photo in Staff becomes the linked user's managed avatar in
identity — a second row in front of the user's own, which they cannot change while it
stands and which comes back when HR removes the photo — and the accounts worker pushes the
effective picture, HR's or else the user's own, to Microsoft Entra ID, converted there to
a JPEG under 4 MB; nothing flows back. The person's self-write of Staff's photo is gone:
My page changes their Vantigo avatar through identity, or says **Set by HR**. Decisions
taken while building it:

- **Reconciled by stamp, not by event**: identity answers which picture stands
  (`managed:<stamp>` or `own:<version>@<epoch>`), Staff writes identity at once and
  repairs it on every claim, and the push compares the stamp with the one last written,
  so a failed write, a late link or a changed avatar is right by the next poll.
- **A refusal is a fact, not a failure**: a picture the tenant will not take is reported
  `photo_not_written` (the item `photoNotWritten`) and not tried again until the picture
  changes; Vantigo never removes a tenant photo it did not write.
- **An Owner's or a SystemAdmin's picture is never managed**, and an erase clears the
  managed avatar with the account.
- **No new permission and no new Graph consent**: `User.ReadWrite.All` covers the photo.

Two migrations, `00070_identity_managed_avatars.sql` and `00071_staff_photo_push.sql`.

### Later

**The Entra module** has [a section of its own](#entra); its phases 1 and 2 are done; Staff phase 2 is
built.

**Planning** — one module owning every booking of a person's time, reading Projects and
Staff — has [a section of its own](#planning); its phase 1, allocations, requests and
assignments, is built. Shifts and their trades are that module's later phase.

## Planning

A new module, researched in
[`docs/superpowers/research/2026-10-26-planning-module.md`](docs/superpowers/research/2026-10-26-planning-module.md)
and designed in
[`docs/superpowers/specs/2026-10-26-planning-design.md`](docs/superpowers/specs/2026-10-26-planning-design.md):
booking the company's people on its projects and seeing the plan whole — Planning, in
Norwegian **Bemanning**. One booking table, the allocation — a person on a project over
a period, in hours a week or a share of their position, tentative or confirmed — read
against the capacity computed once from Staff's facts: the hours of the position, less
the days away and the public holidays. A project's need for someone not yet named is a
request, a placeholder with competence requirements, matched against who holds them,
has the hours free and whom nothing bars. An allocation that is also a contract with the
customer carries its terms — the notice, the minimum duration, the handover, the
non-compete — and the earliest day the person can start elsewhere, which none of the
products studied models. Like the rest of Vantigo it is **legally agnostic**: the
statutory and contractual limits are warnings an installation can make hard, never
built-in refusals, the non-compete the one exception the owner named. Shifts and
rostering are a later phase. It **depends on Projects and Staff** — the first module to
consume two business modules at once — reading the projects through
`contracts.ProjectDirectory` and the people through `contracts.Workforce`, and follows
[Staff](#staff)'s phase 2, built.

### Phase 1 — Allocations, requests and assignments

Three deliveries, three pull requests, one migration each, each opened against `main`
after the previous one lands.

**A — The plan, built** on `claude/staffing-module-plan-af8b31`
([the plan](docs/superpowers/plans/2026-10-26-planning-plan.md);
[the reference](docs/src/content/docs/en/reference/planning.md)): the `planning`
module and `@vantigo/planning-ui`, with `planning:access`, `planning:view` and
`planning:manage`, the last two sensitive, and the settings; allocations, tentative and
confirmed, in hours a week or percent, with a title and the planners' note, the status
defaulting by the project's, and the person mailed when a row of theirs is confirmed,
changed, deleted or set back to tentative; the one capacity calculation — capacity,
away, available, booked, tentative and free per day and per week, a day away wholly
away, and absence never shrinking a booking — and overbooking as a warning or, by the
setting, a refusal of a growing excess; the three reads Staff adds to
`contracts.Workforce` — `People`, `Capacity` and `Self` — with the Colleague rule
widened to the people in employment on today or a later day of the window, and the
window bounded; the listing Projects adds, `ProjectsByStatus`, consumer-gated, with the
projects' dates; the board, the project's plan, the person's timeline with its
`beyond_employment` and `certificate_expires` flags, the unbooked list and My planning;
the events, the four attention items and the dashboard's figures; the tile, the routes,
the dashboard card, the spotlight and the permission labels in the host; and the
Planning tab on Staff's person page, through a slot Staff's package opens. The readings
taken while building it are in the spec's §13. One migration,
`00072_planning_baseline.sql`.

*Unblocks:* "who is free in week 42", read against the hours each person really has;
the people nobody has booked, and the ones booked twice over.

**B — Requests and intake, built** on `claude/dreamy-ptolemy-9ff41d`
([the plan](docs/superpowers/plans/2026-10-26-planning-requests.md)): requests with
competence requirements, in hours a week, open until filled or withdrawn; their
candidates — competence through what Staff answers as held, availability through the
capacity calculation, and the fit Staff scores for the booking, `Workforce.PreferenceFits`
joining the contract as a batch — sorted fits first, then by fit, free hours and the
time on their current project; the fill that makes the allocation and names the request
on it, reopened when that row is deleted; the intake of the planned projects, each need
covered, partly covered or uncovered, with the key-person risk; the project's demand on
its plan; the requests' attention item and the open-requests figure; the competence
catalogue read for the request sheet and the board's filter; the Requests and Intake
pages; and the Planning tab on the project page, composed by the host in its own tab
list. The readings taken while building it are in the spec's §13. One migration,
`00073_planning_requests.sql`.

*Unblocks:* capacity at intake — whether the company can take the bid — and the project's
demand on the board before anyone is named.

**C — Assignments and wishes, built** on `claude/nifty-jemison-e121e1`
([the plan](docs/superpowers/plans/2026-10-26-planning-assignments.md)): an allocation's
terms — the notice and when it was given, the minimum duration, replacement approval,
the handover, the non-compete toward named customers and the contract's reference —
`planning:manage`'s to set and clear, with their own revision, set on a tentative row
and binding once it is confirmed; the earliest release and the handover start derived
on every allocation, a notice in months by Staff's month rule; the non-compete as the
one hard refusal — a confirmed row on a barred customer's project refused, a tentative
one warned, a candidate under one not listed — read through the customer ids the terms
store; the release and the customer's approval as a candidate's flags; the person's
wishes off a project or for something new, without a word of free text, at most five
open, seen and resolved by the planners on their Wishes page, mailed to every planner
but the wisher, a flag and the sort's second key in the matcher, and an erased person's
deleted; the `wishesUnseen` attention item; the wish events the planners' alone; the
terms section of the allocation sheet, the release and handover marks on the timeline,
the wish forms on My planning; and the calendar source, `planning.allocation`, the
person's own confirmed bookings on their calendar and in its feed when `calendar` is
composed. The readings taken while building it are in the spec's §13. One migration,
`00074_planning_assignments.sql`.

*Unblocks:* a consultant's release date known before the next bid, and a person heard
before they resign.

With C, phase 1 is complete: what follows is the *Later* list below.

### Later

- **Shifts and trading** — rostering, open shifts and trades every employee can see,
  the module's next phase.
- **The revenue forecast** — what the confirmed and tentative hours will bill.
- **The automatic request** — a request opened from a foreseeable drop in a project's
  staffing, rather than typed.
- **The booking contract Staff reads** for holiday approval, so a manager deciding
  absence sees the bookings it conflicts with.
- **The personal-data slot** — a many-provider slot through which Staff collects another
  module's rows about a person for its export and its erase.
- **A project manager booking on their own project** without `planning:manage`, if a
  small firm asks for it.
- **The weekly digest** — the unbooked and the ending, mailed rather than read.

## Entra

A new module, researched in
[`docs/superpowers/research/2026-10-19-entra-module.md`](docs/superpowers/research/2026-10-19-entra-module.md)
and designed in
[`docs/superpowers/specs/2026-10-19-entra-design.md`](docs/superpowers/specs/2026-10-19-entra-design.md):
Microsoft Entra ID as Vantigo's first external identity provider. The account Staff's
flag creates becomes a user in the company's tenant — created without licences ahead of
the start, enabled on the start day with a Temporary Access Pass, kept in step as the
person moves, disabled the day after the end with its sessions revoked and its licences
and groups stripped, deleted later or never — and the Vantigo sign-in follows through
SSO, correlated on the employee number. Groups are Staff's, listed or derived from the
register, and the module keeps them in the tenant with the traits an Owner sets. Like
Calendar and Staff it **depends on nobody but identity** for what it reads; Staff calls
it through a new many-provider slot, `contracts.IdentityProvisioner`, and identity's
accounts stay identity's. It follows [Staff](#staff)'s phase 1, done.

### Phase 1 — The tenant follows the register (done)

Five deliveries, five pull requests, one migration per schema each. A, B and C are
specified in full by the design; D and E are decided there and planned on their own.

**A — The settings move, built** on `feat/identity-sso-settings`
([the plan](docs/superpowers/plans/2026-10-19-identity-sso-settings.md);
[the operations page](docs/src/content/docs/en/admin/sso-scim.md)): the workforce OIDC
provider and the SCIM credential out of the environment into one row,
`identity.sso_settings`, which every process — the API and the workers alike — reads
through a 20-second cache, so an Owner's change is in effect on every replica without a
restart; the **Single sign-on** card under Workspace administration — the provider, the
callback to copy, the client secret sealed and never answered, the token file, the
domains, the display name, and the SCIM endpoint with *New token* (generated by the
server, shown once, only its SHA-256 kept), the overlap of at most 24 hours and *End
overlap* — over six Owner endpoints under `/owner/sso`, each write answering the whole
row with its new version and audited `sso-settings.changed` without the secret or the
digests; the confirmation an authority change needs while people are linked under the
old issuer (`oidc_links_exist`); the relying party rebuilt when the settings' version
moves; the import of `OIDC_*` and `SCIM_*`, once, at the first API start that finds the
row never written, audited `sso-settings.imported` with the variables' names, a warning
naming each one left over, and their refusal at startup in the next release; and the
authority mode derived from the row by every process, so it is the trail of changes
rather than a guard against a misconfigured worker. One migration,
`00058_identity_sso_settings.sql`.

*Unblocks:* an Owner who sets up single sign-on and rotates the SCIM token without a
deployment, and C's wizard, which embeds the card's two forms as its sign-in and
provisioning steps.

**B — The announced account and the slot, built** on `feat/staff-provisioner-slot`
([the plan](docs/superpowers/plans/2026-10-20-staff-provisioner-slot.md);
[the reference](docs/src/content/docs/en/reference/staff.md#the-provider-link)):
identity's SCIM **enterprise extension**, inbound — `employeeNumber`, the cost centre,
the organisation, the division, the department and the manager accepted on a create, a
replace and a patch, kept in the SCIM profile, answered back, advertised by `Schemas`
and `ResourceTypes` and filterable on `employeeNumber`, and never promoted to the
user's employee number; the **announced account**, `AccountProvisioning.Expect` and
`Unexpect` over `identity.expected_accounts`, which a SCIM create matches on the
employee number, then the principal name, then `externalId`, and an OIDC first sign-in
on `oid` under its issuer, adopting the user for Staff on arrival — managed, numbered,
not pending, audited `account.adopted-from-provider` — or completing the pending user a
change of kind left behind (`ReplaceUserID`), and the provisioning roles given once
(`users.provisioning_roles_at`); the **account kind** per person — `local`, `external`
or `entra` — in place of the installation-wide authority mode, seeded once from it,
with the installation's **Default account kind**, and carried to identity on every
call; `contracts.IdentityProvisioner`, the fifth many-provider slot, collected from
enabled modules, keyed by `Key()` and refusing a key claimed twice, which Staff writes
through under rule 13 — the provider link as the decision table's third column,
provider first and identity second under one claim, its states, codes, events and
thirteen attention items — with `modtest.FakeIdentityProvisioner` proving it before any
real provider exists; the **directory profile**, the fixed mapping of the register's
fields onto the directory's, switched per installation under Settings and shown on
the person's Account tab; and the Account tab's two cards, *Vantigo* and the
provider's, with *Show the pass* once and *Run now (new pass)*. Migrations
`00059_identity_expected_accounts.sql` (identity) and `00060_staff_account_kind.sql`
(staff).

*Unblocks:* C's provider, dropped into the slot with nothing in Staff or identity left
to build; HR choosing per person who holds the account.

**C — The module, built** on `feat/entra-module`
([the plan](docs/superpowers/plans/2026-10-20-entra-module.md);
[the reference](docs/src/content/docs/en/reference/entra.md);
[the operations page](docs/src/content/docs/en/admin/entra.md)): the `entra` module,
Microsoft Entra ID as the first provider in B's slot, under the key `entra`, which
`MODULES` refuses without `staff`. Its schema holds the one connection — the tenant and
the client, the account rules, what the last test found — the client secrets in the
plural, sealed under `entra/client-secret`, the newest valid one tried first and a
refused one last, revoked never deleted, with `secret_expiring` thirty days ahead and
`secret_expired` after; the accounts it found or made, by object id, with a Temporary
Access Pass sealed under `entra/pass` until it is shown once; and its own append-only
trail of events. One permission, `entra:manage` (sensitive, not delegable), for the
status and the account rules; the registration, the secrets and the connection test are
the Owner's, as the single sign-on settings are. The Graph client is guarded — two fixed
hosts, `netguard`, no proxy, no redirect, a token cached per secret until five minutes
before it ends, `Retry-After` honoured once, one 5xx retry, one fresh token on a 401, a
1 MiB cap, the body never stored — and `Provider` reads the row with no network call,
its reasons Staff's attention items. The lifecycle: found by employee id or created
disabled under an address made from the work email or a pattern over the folded names;
enabled on the first day with the hire date, the licences and the pass; kept in step;
disabled first, whatever the status, then the sessions revoked, the leave date written,
the direct licences stripped and the groups left as the connection says, a step that
fails reported `leaver_step_failed:<step>` and the disable made again the next day;
soft-deleted when Staff asks, a rehire after it a new user. A user the module found
rather than created is adopted untrusted — nothing written to it, `ErrExternalUnconfirmed`
and Staff's `accountUnconfirmedInEntra`, until a holder of `entra:manage` confirms it on
the Status card (the security review's finding, the plan's reading 8); a pass is
issued only on the call that enables an account; a tenant change clears the sealed
passes; the creates are never retried on a 5xx and their refusals logged without
Graph's words; every refusal a
sentinel or a reported code, and a streak of three refused sensitive actions read as
`role_missing`, the User Administrator role not assigned. The tile **Microsoft Entra
ID** carries the **Status** card and the six-step wizard with every portal path,
embedding A's two forms as its sign-in and provisioning steps. A fake Graph under the
module proves it, and a tagged test against a real tenant settles the research's three
uncertain facts. The pass is shown to HR on the Account tab; the manager's notice waits
for a later delivery. Migration `00061_entra_baseline.sql` (entra).

*Unblocks:* an Owner connecting the company's tenant without a deployment; HR's joiners,
movers and leavers reaching Microsoft 365 with no second place to type; D's groups,
whose traits the provider already declares.

**D — Groups and traits, built** on `feat/entra-groups`
([the plan](docs/superpowers/plans/2026-10-20-entra-groups.md);
[Staff's reference](docs/src/content/docs/en/reference/staff.md#groups);
[the module's](docs/src/content/docs/en/reference/entra.md#groups-in-the-tenant)):
**groups in Staff**, `staff:manage`'s — listed, HR naming the members, or derived by one
rule over the register (everyone employed, a department and optionally those beneath
it, a business unit, a location, an employee type, a manager's direct reports), judged
on the business day against the employments and terms in force — with a key that never
changes, owners, an archive that keeps the members as they stood, and a delete only
while no provider has the group, is turned on for it or is syncing it; the accounts worker **materialises** the derived
members each cycle as the difference, each change a `group_joined` or `group_left` on
the person, and a person erased or deleted leaves every group with them. A provider in
the slot **declares the traits** a group may carry — `GroupTraits` now takes a context
and reads the provider's own state, never the network (reading 1) — which Staff renders
as a form from the declaration alone, checks at the save (required, choices, types,
lengths) and stores; the provider's own rules come back from `EnsureGroup` as a
`GroupTraitError` beside the field, not retried until HR changes the group (reading 3).
The worker **syncs** each group a provider is on for, after the links, under a lease,
by the hash of its values and owners and the hash of its members' enabled accounts and
the day, the row's state and code on the group's card and `groupSyncFailed` on the
dashboard; a group archived or switched off is let go in the directory after
`deleteExternalAfterDays` or, when that is never, renamed `… (archived)` and kept —
Staff's rename through `EnsureGroup`, so one code path writes names (reading 5). The **licences** a person gets
are the union of their groups' licence traits, in the spec on every activation and
update, and the module now reconciles them on `Update` as on `Activate` — the missing
assigned, those it assigned itself and no longer named removed, an administrator's or a
group-assigned one untouched (readings 6 and 12). In the tenant: the six traits (`type`, immutable; `mailNickname`;
`visibility`; `description`; `team`; `licences`, from the SKUs cached on the connection
row); the group created with the module's **service principal** — read from its
token's `oid` (reading 2) — and the owners' accounts as owners, or a group holding the
nickname adopted untrusted, written to only once a holder of `entra:manage` confirms it
on the module page's **Groups** table — a dynamic, role-assignable, Exchange-kept or
another row's refused; the owners reconciled, never the service principal; a team made
by `PUT /team` once the group is fifteen minutes old and has a person owner, a younger
one answered `Pending` and asked again next cycle (reading 7); members added in twenties
and removed by `$ref` in `$batch`; a Microsoft 365 group soft-deleted, a security group
emptied and kept. The leaver's `managed` step now means the module's own groups, its
removals batched. The security review's four decisions (the plan's readings 11–14): an
adopted group is untrusted until `entra:manage` confirms it, and a group the module did
not make is never deleted; the module strips only the licences it assigned
(`entra.accounts.licences_assigned`); a security group, which Graph deletes for good, is
emptied of its people, renamed and kept rather than deleted; and a trait refusal no
longer freezes the members, every group's members reconciled at least once a day.
The apps: Staff's **Groups** page and the group page with a card per provider, the
Account tab's **Groups** card, My page's **Your groups**, and the module page's
**Groups** table. Migrations `00062_staff_groups.sql` (staff) and
`00063_entra_groups.sql` (entra).

*Unblocks:* HR keeping who is in which Microsoft 365 group, team and licence in the
register, with no second place to type; a second provider's groups, which need nothing
new in Staff.

**E — The erase and the leftovers, built** on `feat/identity-erase`
([the plan](docs/superpowers/plans/2026-10-21-identity-erase.md);
[Staff's reference](docs/src/content/docs/en/reference/staff.md#the-erased-state);
[Authentication](docs/src/content/docs/en/admin/authentication.md#the-audits-system-actor)):
`AccountProvisioning.Erase` (`contracts.SystemActorStaffRetention`,
`contracts.ErrAccountEnabled`), called by Staff's retention run after the erase lease is
released so a single-connection pool never deadlocks: the disabled Staff-managed user is
deleted with their sessions, credentials, invitations, SCIM mapping, any unconsumed
announcement for the employee number and the delegations they granted (delegations
granted to them keep their row with `delegate_user_id` cleared), the `actor_snapshot` and
`target_snapshot` of the audit rows naming them are pseudonymised in place
(`displayName: "Erased user"`, `email` and `employeeNumber` empty), and
`user.staff-erased` is recorded without a snapshot; in Staff, the managed `identity` link
is deleted (`identity_erased`) — or left keyless and `disabled` while a non-`deleted`
provider link still waits for `deleteExternalAfterDays`, which the accounts worker now
claims and soft-deletes even after the person is erased — and a refusal or failure
records `erasure_incomplete` on the link, raising `erasureIncomplete` under the neutral
title `"An erased person"` and retrying on the next retention run. A self-service link is
left untouched. No migration.

*Unblocks:* Staff as the authority for who exists in the company's Microsoft tenant;
the joiner, mover and leaver without a second place to type.

### Phase 2 — Connecting existing users (done)

Phase 1 made the tenant follow the register, but a company that turns single sign-on on
later, with a workspace full of users who already sign in with a password, got nothing
of that: their first provider sign-in is refused `oidc_email_conflict`, since an address
never links an identity to an existing account. Phase 2, specified in
[`docs/superpowers/specs/2026-10-24-entra-link-existing-users-design.md`](docs/superpowers/specs/2026-10-24-entra-link-existing-users-design.md),
gives an Owner the authorised exception, in two deliveries.

**A — The announced link (done)** on `feat/identity-expected-links`
([the plan](docs/superpowers/plans/2026-10-24-identity-expected-links.md);
[Authentication](docs/src/content/docs/en/admin/authentication.md#connect-an-existing-user-to-the-provider);
[SSO and SCIM](docs/src/content/docs/en/admin/sso-scim.md#the-announced-link)): an
Owner announces that an existing user holds a directory account —
`POST /api/v1/identity/owner/users/{id}/sso-link` with the user principal name, the
object id, or both, kept one per user in `identity.expected_links` — and the user's next
OIDC sign-in (matched on `oid` under the issuer, else on the token's principal name) or SCIM push (`userName` against the
principal name, then `externalId` against the object id) links or maps to that user,
without consulting the email claim, ahead of an announced account, audited
`account.linked-from-provider`; `DELETE …/sso-link` withdraws the announcement and
removes the user's OIDC links under the configured issuer; after an authority change
the Owner simply announces each account again under the new provider, and
announcements count for `oidc_links_exist`. Refusals in order:
`invalid_request`, 404, `sso_not_configured`, `account_pending`, `account_linked`,
`sso_link_taken`. `OwnerUserResponse.ssoLink` says `linked`, `expected` or `none`, and
the Users screen gains a **Single sign-on** column with *Connect…*, *Change the
connection…*, *Cancel the connection* and *Disconnect from single sign-on…*. Migration
`00064_identity_expected_links.sql`.

*Unblocks:* turning single sign-on on in a workspace that already has users, one
person at a time, with the Owner typing the principal name.

**B — The directory picker (done)** on `feat/entra-directory-picker`
([the plan](docs/superpowers/plans/2026-10-27-entra-directory-picker.md);
[Microsoft Entra ID](docs/src/content/docs/en/reference/entra.md#connecting-existing-users);
[the user guide](docs/src/content/docs/en/user/entra.md#connect-existing-users)): two
Owner reads in the Entra module under `policy:Owner+OwnerManagement` —
`GET /api/v1/entra/directory/users?q=`, a Graph `$search` over display name, mail and
principal name with `ConsistencyLevel: eventual`, at most 25 hits, each marked with who
already holds it (an announced link, a SCIM mapping, an account Staff announced), rate
limited `entra-directory` (60 a minute); and `GET /api/v1/entra/directory/matches`,
which proposes, for every user not yet connected (at most 1000, `truncated` beyond),
the one account whose mail or principal name equals their address or whose
`employeeId` equals their employee number, when every key that matched points to that
account, nobody else holds it, it is not a guest and no other user matched it — exact
matches only, a user with more than one account in play counted `ambiguous`, the read
bounded by two minutes and shared per caller while in flight — rate limited `entra-matches` (6 a minute). What it reads of
identity comes through a new identity-provided port, `contracts.SSOConnections`,
provider-gated for an Owner (`Unconnected`, `ErrSSONotConfigured` while OIDC is off;
`KeyHolders`); the addresses never leave the module. The connect dialog gains the
picker above the typed principal name, and the Users screen a *Find matches in the
directory…* review with **Connect selected**, which announces each chosen pair through
delivery A's endpoint; a row starts ticked only for an enabled account matched by its
principal name and a user who is not an Owner. Identity's announcement now also refuses
`sso_link_taken` when another user's SCIM mapping names either key. No migration.

*Unblocks:* connecting a whole workspace in one review, not user by user.

### Later

A certificate credential, and workload identity federation, for Graph · a leaver's
mailbox converted to shared and their OneDrive handed over (neither is in Graph) ·
hybrid tenants, with users synced from on-premises Active Directory · Lifecycle
Workflows switches per step · Google Workspace as the second provider · group-based
licensing as a trait · delegated authorisation.
