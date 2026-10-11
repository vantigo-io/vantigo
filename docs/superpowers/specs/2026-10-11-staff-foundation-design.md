# Staff — the workforce register, absence and the account flag (phase 1)

A new module, `staff`. Research: `docs/superpowers/research/2026-10-11-staff-module.md`
("R", cited by section); its §10 holds the nineteen decisions the owner took on
2026-10-11, which this design implements and does not reopen. Code references are
against `main` at `6941e58d`; `srv/` abbreviates `apps/server/internal/`, `mig/`
`srv/db/migrations/`, `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. Purpose

Give the company one record of every person who works for it, and let that record be
what everything else about the person hangs on:

- HR keeps the **person** — name, identity, contact, next of kin, bank — and their
  **employments**, each a Norwegian arbeidsforhold with the fields the a-melding and
  the written contract require, as dated rows that never overwrite history;
- the person's **absence** — holiday under the company's own scheme, sickness with the
  statutory follow-up timeline, leave in the statutory vocabulary — is requested,
  decided and shown on a calendar, and leaves the module in the shape payroll needs;
- a **flag** on the person decides whether and when they get a user account: created
  disabled ahead of the start, enabled on the day, disabled on the last day, never
  deleted. In this phase the account is Vantigo's own; the Entra module (R §10.10)
  fills the same slot next;
- a **document archive** per person, a **payroll export**, the employee's own page, and
  the retention rules the law puts on all of it.

The sequence decided (R §10.11): **this phase** (register, absence, the account flag
with the local path) → the Entra module → Staff phase 2 (competence and preferences)
→ Staffing. Three deliveries, three PRs:

- **A — The register**: the whole schema (so no later delivery of this phase adds a
  migration), people, employments and their dated terms, departments, locations and
  legal entities, documents, settings, permissions, import and export, the payroll
  CSV behind its port, the personal-data export, retention, stats, the app's People,
  Person and Settings pages, the docs.
- **B — Absence**: the public-holiday calendar moved to the platform, absence types,
  holiday accounts, requests and decisions, sick-leave cases with milestones, the
  calendar, the approvals inbox, the employee's own page.
- **C — Accounts**: identity's provisioning contract and the disabled-user path, the
  flag and its dates, identity links, the accounts worker, the Account tab.

Out of scope, named: payroll itself (a-melding, skattekort, feriepenger arithmetic,
inntektsmelding — R §1.3); competence, certificates and preferences (phase 2);
groups and the Entra provider (the Entra module); contract generation and e-signing;
onboarding and offboarding checklists; equipment; medarbeidersamtaler; NAV sykmelding
import; shift or project planning; a time-and-attendance reconciliation of Time's
hours with the absence plan; moving Time's person rate card (R §10.3); reading
Projects for anything.

## 2. Decisions

**S1 — Its own module, depending on nobody but identity.** `MODULES` may enable
`staff` alone. Nothing here imports another module, no SQL crosses a schema, and the
module reads identity only through the contracts (`UserDirectory`, and the new
`AccountProvisioning` of S14). It is the Expenses shape (boundaries.md:396-417).

**S2 — Three things, kept apart: person, employment, account.** A **person**
(`staff.people`) is a human the company has a relationship with, stable across
hires and across accounts, carrying a permanent **employee number**. An
**employment** (`staff.employments`) is one arbeidsforhold — the a-melding's unit,
with its own id, start, end and end reason — and its **terms**
(`staff.employment_terms`) are dated rows, a new row per change, the way Tripletex's
"Ny rad" and `time.person_rates` work (R §2.1, §7.4). An **account** is identity's
user, linked from the person (`staff.identity_links`), never the other way round.
A person may have several employments, sequential (a rehire, a move between legal
entities) or concurrent (two part-time positions in two units — Huma and Unimicro
model it, R §2.10); the register does not refuse overlap, it shows it.

**S3 — The employee number is permanent and short.** At most 16 characters (Entra's
`employeeId` bound, R §5.5), unique per installation, assigned by HR or taken from
the installation's own sequence (`settings.next_employee_number`), and **kept on
rehire**, so the external account the number correlates can be re-enabled rather
than re-created. It is the key every export and every provider sees; the national
identity number never is.

**S4 — The national identity number is stored sealed, found by hash.** The
fødselsnummer or d-nummer (11 digits, validated with both control digits; a d-nummer
has its first digit raised by 4) or a foreign identifier with its country is written
through `secrets.Box` under the purpose `staff.national-id`, with an HMAC of the
digits under a key derived for `staff.national-id-index` in a second column, unique,
so a duplicate is refused and a lookup is possible without opening anything. It is
revealed only by `GET /people/{id}/national-id` under `staff:personal-data`, each
reveal an event; list and detail responses carry `nationalIdLast4` and the kind. The
bank account is sealed the same way (`staff.bank-account`), shown as its last four
digits, revealed by the same permission; MOD11 is checked on a Norwegian account
number, IBAN by its check digits. Customers' D1 refusal (R §7.4) does not apply: the
number is what payroll, pension and the HMS-kort are keyed on (R §10.1).

**S5 — Employment terms carry the a-melding and the contract.** One row per change,
effective from a date, holding what Skatteetaten reports monthly and what aml. § 14-6
requires written (R §4.1, §4.7): department, location, legal entity, direct manager,
title, STYRK-08 yrkeskode, hours per week for a full position, stillingsprosent,
arbeidstidsordning, ansettelsesform (the five a-melding values), remuneration type,
the pay components as the contract states them (a list of named amounts, text to
the module, payroll-sourced — R §10.7), the working-time text and break length, the
probation end date, notice periods, the tariff reference and the temporary ground
and expected end when the employment is temporary. The dates the a-melding wants
derived — `fteChangedAt`, `salaryChangedAt` — are computed from the row chain, never
typed. `employmentType` (fast or midlertidig) and `ftePercent` are **required** on
every row: the law presumes the employee's claim when they are missing (R §4.1), so
the module refuses a row without them rather than defaulting.

**S6 — Structure is three small tables, not an org tree.** `departments` (code,
name, manager person, cost centre, optional parent), `locations` (name, address —
the arbeidssted) and `legal_entities` (name, organisation number, the
virksomhet/underenhet the a-melding and an Entra `companyName` want). The org view is
derived from the direct manager on the terms; a department's manager is the default
when the terms name none. Teams and groups wait for the Entra module (R §10.14).

**S7 — Absence is one table of dated rows under a vocabulary.** `absence_types`
seeds the statutory vocabulary (R §4.6) — ferie, egenmelding, sykmelding,
svangerskapskontroll, svangerskapspermisjon, omsorgspermisjon, fødselspermisjon,
foreldrepermisjon, ammefri, sykt barn, pleie av nærstående, utdanningspermisjon
(lovfestet, ikke lovfestet), militærtjeneste, offentlig verv, religiøs høytid,
velferdspermisjon, permittering, avspasering, helse (a dentist, a doctor), annen
lovfestet, annen ikke-lovfestet — each with a `kind` the rules key on, a paid flag,
whether it draws on the holiday account, whether it allows a percentage, the
a-melding leave description it maps to, whether it needs a decision or is a
notification, and who may see the type (everyone sees "absent"; the type is for the
manager, HR and the person — Huma's rule, R §2.5). A company adds its own types
above the seeds and may hide a seed; a seed is never deleted. An **absence**
(`absences`) is a person, a type, a first and last day, a percentage, a status and
the decision; a sick absence belongs to a **case**.

**S8 — Holiday is an account per person per year under a company scheme.** HR
configures the **schemes** (R §10.6): a name, the entitlement in days, the **day
basis** (`working_days` Monday–Friday, or `virkedager` Monday–Saturday, the law's
unit), the extra days from the year the person turns 60, the maximum agreed
carry-over, and which scheme is the installation's default. The statutory scheme is
seeded — 25 virkedager, +6 at 60, 12 carry-over by agreement — and a company adds
"five weeks" or anything else above it. A person's employment names its scheme (the
default when it names none). The **holiday account** for a year is derived, not
typed: entitlement (prorated to six virkedager, or the basis' equivalent, for a start
after 30 September), carried in (agreed, capped; forced, uncapped, with its reason),
taken (approved ferie days counted on the basis, public holidays excluded), remaining.
Feriepenger are payroll's: the account holds days, never money.

**S9 — Sickness is a case with a timeline.** A **sick case** (`sick_cases`) opens
with the first absent day of a sickness and gathers its egenmelding and sykmelding
rows; two sick absences fewer than sixteen calendar days apart belong to the same
case (ftrl. § 8-19, R §4.5). The case derives its milestones — arbeidsgiverperiode end
at day 16, oppfølgingsplan at 4 weeks, dialogmøte 1 at 7 weeks, NAV's aktivitetsplikt
at 8, dialogmøte 2 at 26, the 52-week end — and HR or the manager marks each done or
waived ("åpenbart unødvendig") with a date. The case carries a **degree** and
functional notes about tilrettelegging, **never a diagnosis**: there is no field for
one, and the health document category is behind `staff:personal-data` (R §4.5, §10.1).
Egenmelding is judged under the installation's **policy** (minimum: 3 days a case, 4
cases in 12 months, after two months of employment; extended: 8 and 24 days; or the
company's own numbers) and refused, with the reason, when the counters say the
person must bring a sykmelding; it is never allowed past the sixteenth day of a case.

**S10 — Who decides an absence.** A type that needs a decision (ferie, permisjon,
avspasering, the company's own) goes `requested → approved | rejected`, cancellable by
the person while requested and by the decider after. The deciders are the person's
**direct manager** on the terms in force (or the department's manager when the terms
name none), anyone with `staff:approve`, and HR (`staff:manage`). Nobody decides their
own request; a manager's own goes to their manager, and when that chain ends, to
`staff:approve` and HR. A type that is a notification (egenmelding, sykmelding, sykt
barn, helse) is `registered` when entered, by the person or by HR on their behalf,
and the manager is told, not asked. A decided absence in the past can be corrected by
HR only, as an event.

**S11 — Public holidays come from the platform.** The Norwegian calendar that lives
inside Invoices (`srv/invoices/reminderrules/months.go:81-115`, R §7.4) moves to
`internal/calendar` — `PublicHoliday(date) bool`, `NextBusinessDay`, `Easter` — added
to rule 1's allowed platform list; Invoices imports it from there, behaviour
unchanged, its purity test kept. Staff counts holiday and working days with it. A
region beyond Norway is a later column on the settings.

**S12 — Documents are the receipt pattern with categories and classes.**
`staff.documents` rows point at objects in the `staff` storage scope
(`storage.NewScope`, R §7.4): PDF, JPEG, PNG and HEIC, sniffed, 10 MiB, the object
written before the row's transaction and removed when it refuses, downloads
streamed through the API with `nosniff` and a disposition. A document has a
**category** (contract, attest, certificate, correspondence, health, other, and the
company's own), an optional `validTo`, an optional `signedAt`, and the **retention
class** its category implies (S16). The `health` category is readable only under
`staff:personal-data`; the person always sees their own documents except health ones
HR marks "not for the person" — which the regulation does not allow for long, so the
flag is an exception a reading notes.

**S13 — The account flag, in three settings.** On the person: `provisionAccount`
(true or false, HR's choice; false for the rare person who gets none) and
`provisionFrom` (a date; null means "the first day of the earliest employment that
has not ended", recomputed when that changes, so HR need not touch it). On the
installation: `createBeforeDays` (default 14; the account is created **disabled** that
many days before `provisionFrom`; 0 means on the day) and `deleteExternalAfterDays`
(default null, never). The leaver side needs no setting: the account is **disabled on
the day after the last employment's end date**, and re-enabled when a new employment
with the flag starts (R §10.8, §10.12, §10.13). A person with the flag off and an
account already linked keeps it until HR disables it explicitly; the flag governs
what the worker does, not what exists.

**S14 — A provisioning contract from identity, and a disabled user that waits.** A
new single-provider slot `Accounts` carries `contracts.AccountProvisioning`,
implemented by identity, consumed by staff through `Deps.Accounts`:

```go
type AccountSpec struct {
    Email, DisplayName, PreferredLanguage string // the work email; 'en' | 'nb'
    EmployeeNumber                         string // correlation, kept on the user
}
type AccountRef struct {
    UserID uuid.UUID
    State  string // "pending" | "active" | "disabled"
}
type AccountProvisioning interface {
    // Prepare creates a disabled user with no credentials, or finds the one
    // already holding the email or the employee number. Idempotent.
    Prepare(ctx context.Context, spec AccountSpec) (AccountRef, error)
    // Activate enables the user and, when it has no credentials yet, issues
    // and delivers an invitation that sets them. Idempotent.
    Activate(ctx context.Context, userID uuid.UUID) (AccountRef, error)
    // Deactivate disables the user and ends its sessions. Idempotent.
    Deactivate(ctx context.Context, userID uuid.UUID) (AccountRef, error)
    // Account answers the state of a user, or (nil, nil) for an unknown id.
    Account(ctx context.Context, userID uuid.UUID) (*AccountRef, error)
    // FindByEmail answers the user holding an email, or (nil, nil).
    FindByEmail(ctx context.Context, email string) (*AccountRef, error)
}
```

It is a **new cross-module write direction**, designed as boundaries.md:108-109
asks, not a holder: every method runs on identity's own pool, in identity's own
transactions, outside any of staff's — staff's worker calls them with no transaction
open — and mails after commit as `deliverInvitation` already does
(`srv/identity/invitations.go:125-135`). Identity gains for it: a user row allowed to
exist **disabled with no password and an unconfirmed email** (`pending`), an
`employee_number` column (varchar(16), unique when set) so `Prepare` is idempotent
and a later SCIM or OIDC arrival correlates, and an invitation acceptance that
**completes an existing pending user** instead of inserting a new one when the
invitation names one (`PostIdentityInvitationsAccept`, `srv/identity/invitations.go:241`).
`Prepare` grants the built-in `User` role only; RBAC roles stay the authorisation
admin's job in this phase. The `Users` slot stays read-only; the slot is a second
provider, so a future identity implementation could be swapped without touching
staff.

**S15 — The accounts worker, daily, in the business time zone.** `staff-accounts`
runs on an advisory lease (the customers anonymisation worker's loop, R §7.4) once
a day at the installation's zone's start of day and on demand (`POST /accounts/run`,
`staff:manage`). Per person with the flag it decides, from today in the zone: create
(disabled) when `today ≥ provisionFrom − createBeforeDays` and no link exists;
activate when `today ≥ provisionFrom`; deactivate when `today > lastEnd`; and with a
provider slot filled, the same four for each provider through its own contract
(defined with the Entra module; this phase defines the identity link's shape so no
migration is needed then). Each step writes the link's state and, on failure, the
error and a retry at the next cycle, with the person on the attention list after
three failures. The worker never writes under a lock and calls out of no transaction.

**S16 — Retention is per class, with an erase run.** Every field and document carries
one of six classes (R §4.9, §1.2 finding 9): **A** pay documentation (the payroll
export's lineage, five years after the year); **B** hours (Time's, not this module's);
**C** who was employed when (name, birth date, employee number, employments, terms
without pay, titles — kept, also for the sluttattest); **D** personnel administration
(contact, address, next of kin, photo, bank, national id, documents of category
contract, correspondence, other — erased at end plus `retentionGraceDays`, default 90);
**E** health (sick cases' notes, health documents, absence types of kind sick keep
their dates and degree but lose notes — erased when the case is closed and
`healthRetentionDays` have passed, default 365); **F** hold (`people.legal_hold` with a
reason and who set it stops every erase for the person). The `staff-retention` worker
runs daily on a lease: for every person whose last employment ended, past the grace,
not held, it blanks the D fields and deletes the D documents' objects (one person per
transaction, the receipt removal pattern), writes an `erased` event listing kinds and
counts, and schedules a **re-assessment** attention item twelve months after the end
(Datatilsynet's guidance). A person erased is read-only except for the hold flag and
a rehire, which opens a fresh D record under the same employee number. Identity's
user is never deleted by this module (R §10.13); `Deactivate` is the most it asks.

**S17 — The person's own data, exported.** `GET /me/export` and
`GET /people/{id}/personal-data` (`staff:personal-data`) stream one JSON document with
every field group, every absence, every case, every event and the documents' metadata
(the files follow on the archive's download endpoints), the shape of customers'
export (R §7.4). The module does not implement `CustomerPersonalData`: a person who is
also a customer is a later concern.

**S18 — Self-service is a short list of own fields, each change an event.** A person
with `staff:access` sees their own record, employments and terms (pay components
included — it is their contract), absence and documents, and edits their phones,
private email, address and next of kin directly. Bank account and national id are
HR's to change (R §9, a fraud boundary a reading notes); the person asks. Everything
else is read-only to them.

**S19 — Payroll export behind a port.** `staff.PayrollExporter` is an in-module
interface — `Export(ctx, month, rows) (contentType, []byte, error)` — with one
implementation, `csvExporter`, and `GET /payroll/export?month=YYYY-MM&format=csv`
(`staff:manage`). A row per employment active in the month: employee number, name,
the sealed identifiers **omitted** (payroll already holds them; a reading), the
employment id, start, end and end reason, the terms in force on the last day of the
month (yrkeskode, full-time hours, fte, `fteChangedAt`, arbeidstidsordning,
ansettelsesform, remuneration type, `salaryChangedAt`, legal entity, department,
cost centre), then one row per leave over fourteen days in the month with its
a-melding description and percentage, and the absence days per type. Semicolon, BOM,
CRLF, as Expenses' CSV (`srv/expenses/reimbursements.go:590-608`). An export is an
event with its hash; the rows it was built from are class A for five years, which
the retention run respects by blanking nothing the export named — in practice the
employment and terms rows, which are class C anyway.

**S20 — Import and export of the register.** `POST /people/import` (CSV, dry run
first, per-row plan create or update keyed on employee number, a failed-rows file —
customers' import, `srv/customers/import.go`), `GET /people/export.csv` and
`GET /people/import-template.csv`, under `staff:manage`; the sealed fields import but
never export.

**S21 — Attention and stats.** `GET /stats` (headcount, FTE sum, people on leave
today, open sick cases, absence rate for the trailing twelve months) and
`GET /stats/attention`, the host dashboard's items: absence awaiting the caller's
decision; a contract overdue (an employment started over seven days ago with no
document of category contract); a probation ending within a month; a temporary
employment past twelve months (notice due) or thirty-six (converts); a sick-case
milestone due or overdue for the caller's reports; holiday unplanned (fewer than the
scheme's main-period days approved by 1 June, or days remaining in December); a
provisioning failure; a retention re-assessment due; a headcount threshold crossed
since last seen (5: varslingsrutine and verneombud; 30: AMU; 50: ARP — R §4.10). Items
depend on who asks, as customers' follow-ups do.

**S22 — Events are the history.** `staff.events` (person, kind, actor snapshot,
instant, payload) records every mutation — created, field changed (with the field
names, never the sealed values), terms added, absence requested and decided, case
milestone done, document added or removed, account prepared/activated/deactivated,
export made, erased, hold set — and `GET /people/{id}/events` lists them. Terms and
absences are their own history; the events are the audit.

**S23 — Frontend, one app, mobile-first where the employee is.** `apps/staff/frontend`
(`@vantigo/staff-ui`), the tile "Staff" / "Ansatte". Sidebar: **My page**
(`staff:access`), **People**, **Absence** (calendar and the approvals inbox), **Settings**
(`staff:manage`). My page and the absence request are designed for a phone first
(Mantine's responsive primitives; the host is already responsive at the switcher
level), because the shift-trading decision (R §10.9) makes every employee a user.

**S24 — A read contract for the modules that follow.** `contracts.StaffDirectory`,
a new single-provider slot `Staff`, filled by this module: `PersonByUser(userID)`,
`EmploymentOn(userID, date)` (active, fte, department, manager user id, legal entity)
and `ManagerOf(userID, date)`. Nobody consumes it in this phase; it is defined and
covered so Projects' "assigning users who lack access" question, Time's approver
routing and Staffing can read it without a later contract design. Like every
directory it is read outside any caller transaction.

**S25 — Documentation is its own task in every delivery.** `en/reference/staff.md`
with `sources:` frontmatter, `en/user/staff.md` and `nb/user/staff.md` page for page,
`en/admin/staff.md` and `nb/admin/staff.md` for the settings, the retention run and the
accounts worker, module-boundaries.md (module list, schema list, `MODULES` default,
the new slots and the new write direction), ROADMAP.md (a Staff section with this
phase done and the next three named), `docs:check` green per commit.

## 3. Data model (schema `staff`, migration `00042_staff_baseline.sql`)

Every table has `created_at`, `updated_at`; mutable rows have `revision` for
`If-Match`. Ids are `bigint identity` unless noted. Person and user ids referenced
from other schemas are opaque; no cross-schema key.

### 3.1 `staff.people`

| Column | Type | Notes |
|---|---|---|
| `id` | bigint | |
| `employee_number` | varchar(16) not null unique | S3 |
| `given_name`, `family_name` | varchar(100) not null | |
| `display_name` | varchar(200) not null | defaults to given + family |
| `birth_date` | date | class C |
| `gender` | varchar(10) | `female`, `male`, `other`, `unspecified`; for ARP (R §4.12) |
| `national_id_kind` | varchar(20) | `fodselsnummer`, `d_nummer`, `foreign` |
| `national_id_sealed` | bytea | S4 |
| `national_id_hash` | bytea unique | S4 |
| `national_id_country` | char(2) | foreign ids |
| `national_id_last4` | char(4) | display |
| `work_email` | varchar(254) | required when `provision_account`; what the account is keyed on |
| `private_email`, `phone_mobile`, `phone_private` | varchar | D |
| `street`, `postal_code`, `city`, `country` | varchar | D |
| `preferred_language` | varchar(2) not null default `nb` | |
| `photo_object_key` | varchar | D; `staff` scope |
| `bank_account_sealed`, `bank_account_last4`, `bank_account_kind` | bytea, char(4), varchar(10) | D; `bban`, `iban` |
| `provision_account` | boolean not null default false | S13 |
| `provision_from` | date | S13; null = derived |
| `legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`, `legal_hold_at` | | F |
| `erased_at` | timestamptz | S16 |
| `notes` | varchar(2000) | HR's, D |

`staff.next_of_kin` — `person_id`, `name`, `relationship` (`spouse`, `partner`,
`parent`, `child`, `sibling`, `other`), `phone`, `note`; at most 3; class D.

### 3.2 `staff.employments`

| Column | Type | Notes |
|---|---|---|
| `id` | bigint | |
| `person_id` → people | | |
| `employment_number` | varchar(40) not null unique | the a-melding `arbeidsforholdID`; defaults to `<employee_number>-<n>` |
| `relationship_type` | varchar(20) not null | `ordinary`, `maritime`, `freelancer`, `pension_or_other` (R §4.7) |
| `start_date` | date not null | |
| `end_date` | date | |
| `end_reason` | varchar(40) | one of the six a-melding reasons; required with `end_date` |
| `holiday_scheme_id` → holiday_schemes | | null = default |
| `legal_entity_id` → legal_entities | not null | |
| `contract_signed_at` | date | set when a document of category `contract` with `signed_at` is attached, or typed |

Indexes `(person_id, start_date)`, `(end_date)` partial on not null.

### 3.3 `staff.employment_terms`

| Column | Type | Notes |
|---|---|---|
| `id` | bigint | |
| `employment_id` → employments | | |
| `valid_from` | date not null | unique with `employment_id` |
| `employment_type` | varchar(12) not null | `fast`, `midlertidig` (S5, required) |
| `temporary_ground` | varchar(30) | § 14-9 (2) `a`…`e`, `tariff`; required when midlertidig |
| `expected_end_date` | date | temporary |
| `appointment_form` | varchar(40) not null | the a-melding five: `fast`, `midlertidig`, `fast_utleid`, `midlertidig_utleid`, `midlertidig_tilkallingsvikar` |
| `fte_percent` | numeric(6,2) not null | 0 < x ≤ 100 (S5, required) |
| `full_time_hours_per_week` | numeric(5,2) not null default 37.5 | |
| `occupation_code` | char(7) | STYRK-08 |
| `title` | varchar(120) | |
| `working_hours_scheme` | varchar(40) not null default `not_shift` | the six values |
| `remuneration_type` | varchar(20) | `monthly`, `hourly`, `commission`, `fee`, `piecework` |
| `pay_components` | jsonb | `[{name, amount, currency, period}]`, text to the module (R §10.7) |
| `department_id` → departments | | |
| `location_id` → locations | | |
| `manager_person_id` → people | | direct manager; null = department's |
| `cost_centre` | varchar(40) | |
| `working_time_text`, `break_minutes` | varchar(500), smallint | § 14-6 j, k |
| `overtime_rule_text` | varchar(500) | § 14-6 m |
| `probation_end_date` | date | ≤ start + 6 months, ≤ half a temporary term |
| `notice_employee_days`, `notice_employer_days` | smallint | |
| `tariff_reference` | varchar(200) | |
| `seniority_date`, `pay_grade` | date, varchar(20) | lønnsansiennitet, lønnstrinn, optional |
| `note` | varchar(1000) | |

The row in force on a date is the one with the greatest `valid_from ≤ date`, never
later than the employment's end. `fteChangedAt` is the `valid_from` of the earliest
row in the latest run of equal `fte_percent`; `salaryChangedAt` the same over
`pay_components`.

### 3.4 Structure

`staff.departments` (`code` varchar(20) unique, `name`, `manager_person_id`,
`cost_centre`, `parent_id`, `archived_at`); `staff.locations` (`name`, address
columns, `archived_at`); `staff.legal_entities` (`name`, `org_number` char(9) MOD11
unique, `is_default`, `archived_at`). Archived rows stay referenced and leave the
pickers.

### 3.5 Absence

`staff.absence_types` — `key` varchar(40) unique (seeds use the statutory names),
`name_en`, `name_nb`, `kind` (`holiday`, `sick_self`, `sick_certified`, `child_sick`,
`leave`, `time_off`, `other`), `paid`, `draws_holiday`, `allows_percent`,
`needs_decision`, `amelding_leave` (the six descriptions, `permittering`, or null),
`seeded`, `hidden`, `sort`.

`staff.holiday_schemes` — `name`, `entitlement_days` smallint, `day_basis`
(`working_days`, `virkedager`), `extra_days_from_60` smallint, `max_carry_over_days`,
`is_default`, `seeded`.

`staff.holiday_adjustments` — `person_id`, `year` smallint, `days` numeric(5,1)
(signed), `reason` (`agreed_carry_over`, `forced_carry_over`, `correction`),
`note`, `by_user_id`; the only typed part of an account.

`staff.absences` — `person_id`, `type_id`, `first_day`, `last_day`, `percent`
numeric(5,2) default 100, `status` (`requested`, `approved`, `rejected`, `cancelled`,
`registered`), `note` varchar(500) (never health), `case_id` → sick_cases, `requested_by_user_id`,
`decided_by_user_id`, `decided_at`, `decision_note`, `calendar_only` boolean (a
notification type). Index `(person_id, first_day)`, `(status)` partial on requested.
Overlap of two approved or registered absences for one person on one day is refused
unless both allow percent and the percentages sum to at most 100.

`staff.sick_cases` — `person_id`, `first_day`, `closed_at`, `degree_percent`,
`accommodation_note` varchar(2000) (class E), `waived_reason`. `staff.sick_case_milestones` —
`case_id`, `kind` (`employer_period_end`, `follow_up_plan`, `dialogue_1`,
`nav_activity`, `dialogue_2`, `max_benefit`), `due_date` (derived, stored for
listing), `done_at`, `done_by_user_id`, `waived`.

### 3.6 Documents, links, events, settings

`staff.documents` — `person_id`, `category_id` → `document_categories` (`key`, names,
`retention_class`, `restricted` for health, `seeded`), `object_key`, `file_name`,
`content_type`, `size_bytes`, `valid_to`, `signed_at`, `not_for_person` boolean,
`uploaded_by_user_id`, `deleted_at`. Keys `people/<personId>/<uuid>`.

`staff.identity_links` — `person_id`, `provider` varchar(20) (`identity` now; the
Entra module adds its own value), `external_id` varchar(200), `state` (`pending`,
`active`, `disabled`, `failed`), `last_error`, `attempts`, `next_attempt_at`,
`last_synced_at`; unique `(person_id, provider)`, unique `(provider, external_id)`.

`staff.events` — `person_id`, `kind`, `actor_kind`, `actor_user_id`,
`actor_display`, `at`, `payload` jsonb. Append-only; class C except payloads the
erase blanks.

`staff.settings` — one row: `time_zone` (Europe/Oslo, validated as Expenses does,
`mig/00013_expenses_claims.sql:88-107`), `next_employee_number` bigint,
`employee_number_prefix` varchar(8), `self_cert_policy` (`minimum`, `extended`,
`custom`), `self_cert_days_per_case`, `self_cert_cases_per_year`,
`self_cert_days_per_year`, `self_cert_after_months` default 2,
`create_before_days` default 14, `delete_external_after_days` null,
`retention_grace_days` default 90, `health_retention_days` default 365,
`pension_provider`, `injury_insurer`, `headcount_thresholds_seen` jsonb.

## 4. Rules

**Days and the zone.** A date in this module is a calendar day in the installation's
business time zone; "today" for the workers is that day. Counting days on a basis
excludes public holidays from `internal/calendar` and, for `working_days`, Saturdays
and Sundays; for `virkedager`, Sundays only. A percentage absence counts its share of
each day.

**The holiday account** for person P, year Y: `entitlement` = scheme days, prorated to
six virkedager (or five working days) when P's earliest active employment starts after
30 September of Y, plus the extra days when P turns 60 or older in Y, plus carry-in:
agreed adjustments capped at the scheme's maximum, forced adjustments uncapped;
`taken` = approved ferie days in Y on the basis; `remaining` = entitlement − taken.
Requesting ferie beyond `remaining` is allowed with a warning (the law lets an
employer grant it; the account goes negative and shows it); HR may refuse by policy
later. A ferie request that would leave fewer than 18 virkedager (or 15 working days)
approved in 1 June–30 September when September ends is only a warning on attention.

**Egenmelding.** Allowed when: the person has been employed two months
(`self_cert_after_months`) on the first day; the case's self-certified days so far
plus the request stay within `self_cert_days_per_case`; the trailing 365 days hold
fewer than `self_cert_cases_per_year` cases with an egenmelding (minimum policy) or
fewer than `self_cert_days_per_year` self-certified days (extended); and the first day
is within the case's first sixteen calendar days. Otherwise the registration is
refused with `self_certification_exhausted` and the reason, and the person is told to
register a sykmelding instead.

**The case.** A sick absence whose first day is within sixteen calendar days after the
last day of an open or recently closed case joins that case; otherwise it opens one.
Milestones are dated from the case's `first_day`; the arbeidsgiverperiode end merges
across the sixteen-day rule. A case closes when its last absence ended and no new one
joined for sixteen days, or by HR. A closed case keeps its dates and degree; its notes
fall under class E.

**Terms.** A new terms row needs `valid_from ≥ start_date`, `≤ end_date`, and a
`valid_from` not already used; the previous row's values are the form's defaults.
`probation_end_date` is refused beyond six months from the employment start, or
beyond half the expected term when temporary. `temporary_ground` is required when
`employment_type = midlertidig`; `appointment_form` must agree with `employment_type`
(a `fast_*` form on a midlertidig row is refused). Ending an employment needs
`end_reason`; the end date may be in the future. A terms row dated after the end is
refused.

**Managers.** A person's manager on a date is the terms' `manager_person_id`, else
the department's manager; a person is never their own manager (refused). The manager's
*user* is the linked identity user of that person, if any; a manager without an
account decides nothing until they have one.

**Sealed fields.** Written only through `PUT /people/{id}/national-id` and
`/bank-account` (`staff:manage`), validated, sealed, hashed; read only through the
reveal endpoints (`staff:personal-data`), each reveal an event with the viewer; never
in lists, exports to CSV, logs or problem details.

**Accounts.** The worker's decision per person per cycle, in order: if `erased_at` →
nothing; if a link is `failed` with `next_attempt_at` in the future → skip; if no
employment is active or starts within `createBeforeDays` of `provisionFrom` → ensure
disabled when a link exists; else if `today ≥ provisionFrom` → `Prepare` if no link,
then `Activate`; else if `today ≥ provisionFrom − createBeforeDays` → `Prepare`. A
link's `external_id` is identity's user id. `work_email` must be set and unique
among people with the flag, else the person is on attention as `account_blocked` with
the reason and the worker skips them.

**Erase.** Class D fields → null or `[anonymised]` for names? No: names are class C and
stay. Blanked: contact, address, next of kin rows deleted, photo and D-documents'
objects deleted and rows marked, bank and national id sealed values and hashes
removed (the last4 too), notes. Terms' `pay_components` → null (class A lives in the
exports already made). Events keep kinds and dates; payloads with D content are
replaced by `{"erased": true}`. The run reports per kind.

**Locking.** Every mutation of a person's rows takes the person row `FOR UPDATE`
first, then absences, then documents, in that order; the workers take one person per
transaction. No call to a contract, the mail or the object store happens inside a
transaction, including the read-only snapshots the module may use for a multi-table
read (boundaries.md's rule as widened at `6941e58d`).

## 5. Permissions and visibility

| Key | Grants | Sensitive |
|---|---|---|
| `staff:access` | Use the Staff app: own record, own absence, own documents, own export (S18). **Required by every operation.** | no |
| `staff:view` | The register: everyone's non-sensitive profile, employments and terms without pay components, structure, the absence calendar with types hidden ("absent"). | no |
| `staff:approve` | Decide absence for anyone, see the types and the open cases' dates for anyone, mark milestones. | no |
| `staff:manage` | HR: create and edit people, employments and terms (pay included), structure, absence types and schemes, settings, documents other than health, the account flag and dates, run the workers, import and export, payroll export, correct past absence. | yes |
| `staff:personal-data` | Reveal national id and bank account, health documents and case notes, the personal-data export for anyone, the legal hold, see erasures. | yes |

**Being a manager** (by the terms, S10) grants without a permission: the reports'
profiles as `staff:view` shows them, their absence with types, their open cases'
milestones, and the decision on their requests. It does not grant pay components,
documents, sealed fields or the flag.

Shaping: a response carries only what the caller may see; a field the caller may not
see is absent, not null. Capabilities on `GET /meta` and on each person
(`canEdit`, `canDecide`, `canRevealIdentifiers`, `canManageAccount`,
`canSeeHealth`) drive the UI; the access layer answers the uniform 403.

Default roles: `User` gets `staff:access`; `Owner` gets all five. Delegable, all.

## 6. API (`/api/v1/staff`, contract `openapi/staff.yaml`)

| Area | Operations |
|---|---|
| Meta | `GET /meta` — capabilities, structure (departments, locations, legal entities), absence types visible to the caller, schemes, the zone, `accountsProvider` names |
| People | `GET /people` (q, departmentId, locationId, legalEntityId, active, paged) · `POST /people` · `GET|PUT /people/{id}` · `PUT /people/{id}/national-id` · `GET /people/{id}/national-id` · `PUT|GET /people/{id}/bank-account` · `GET|PUT /people/{id}/next-of-kin` · `PUT /people/{id}/photo` · `GET /people/{id}/events` · `GET /people/{id}/personal-data` · `PUT /people/{id}/legal-hold` · `GET /people/{id}/org` (manager chain and reports) |
| Employments | `GET|POST /people/{id}/employments` · `GET|PUT /employments/{id}` · `POST /employments/{id}/end` · `POST /employments/{id}/reopen` · `GET|POST /employments/{id}/terms` · `PUT|DELETE /terms/{id}` (the latest row only) · `GET /employments/{id}/contract-check` (the § 14-6 items present or missing) |
| Structure | `GET|POST /departments` · `PUT /departments/{id}` · `POST /departments/{id}/archive` · the same for `/locations` and `/legal-entities` |
| Documents | `GET /people/{id}/documents` · `POST /people/{id}/documents` (multipart) · `GET /documents/{id}` · `PUT /documents/{id}` (category, dates, flag) · `DELETE /documents/{id}` · `GET|POST /document-categories` · `PUT /document-categories/{id}` |
| Absence | `GET /absences` (personId, from, to, status, typeId, paged; shaped) · `POST /absences` · `GET|PUT|DELETE /absences/{id}` · `POST /absences/{id}/approve` · `/reject` (reason) · `/cancel` · `GET /approvals` (what the caller may decide, oldest first) · `GET /calendar?from&to&departmentId` (who is absent per day, types shaped) · `GET /people/{id}/holiday-account?year` · `POST /people/{id}/holiday-adjustments` · `GET /people/{id}/self-certification` (the counters and what is allowed today) |
| Cases | `GET /cases` (open, personId) · `GET|PUT /cases/{id}` · `POST /cases/{id}/milestones/{kind}/done` · `/waive` · `POST /cases/{id}/close` |
| Settings | `GET|PUT /settings` · `GET|POST /absence-types` · `PUT /absence-types/{id}` · `GET|POST /holiday-schemes` · `PUT /holiday-schemes/{id}` |
| Accounts | `GET /people/{id}/account` (links and states) · `PUT /people/{id}/account` (flag, from) · `POST /people/{id}/account/run` · `POST /accounts/run` |
| Data | `POST /people/import` (dry run) · `GET /people/export.csv` · `GET /people/import-template.csv` · `GET /payroll/export?month&format` · `POST /retention/run` |
| Me | `GET /me` · `PUT /me` (the S18 fields) · `GET /me/absences` · `POST /me/absences` · `GET /me/holiday-account` · `GET /me/documents` · `GET /me/export` |
| Dashboard | `GET /stats` · `GET /stats/attention` |

Refusals are RFC 9457 problem details with the module's codes: `national_id_invalid`,
`national_id_duplicate`, `employee_number_taken`, `terms_required_fields`
(`employmentType`, `ftePercent`), `temporary_ground_required`,
`appointment_form_mismatch`, `probation_too_long`, `terms_after_end`,
`end_reason_required`, `self_manager`, `absence_overlap`,
`self_certification_exhausted`, `self_certification_too_early`,
`decision_not_yours`, `own_request`, `holiday_scheme_in_use`, `seed_immutable`,
`document_type_unsupported`, `document_too_large`, `person_erased`, `legal_hold`,
`work_email_required`, `work_email_taken`, `accounts_unavailable`.

## 7. Frontend

A new app "Staff" / "Ansatte" (`apps/staff/frontend`, `@vantigo/staff-ui`).

- **My page** (`staff:access`): my profile with the editable fields, my employments
  and the terms in force (pay components shown), my holiday account for the year with
  the request button, my absences, my documents, "download my data". Mobile first:
  the request form is a sheet with the type, the days (a range picker), the
  percentage when allowed, a note; egenmelding shows what the policy allows today
  before the person submits.
- **People** (`staff:view`): the list with search and the structure filters, active
  by default; a row shows name, employee number, title, department, manager, status
  (active, starts on, ended on, on leave today). *New person* (`staff:manage`) is a
  two-step form: the person, then the first employment with its first terms, the
  required fields marked and the presumption warning explained.
- **Person page**: tabs **Profile** (fields by group; the sealed fields as last-four
  with a reveal button for those who may, each reveal confirmed), **Employments**
  (one card per employment with its terms as a timeline; *New terms* copies the
  current row; *End employment* asks the reason; the contract check as a list of
  the seventeen items with ticks), **Absence** (the holiday account, the list, the
  open case with its milestones and tick boxes), **Documents** (categories, upload,
  the health category only for those who may), **Account** (the flag, the dates with
  the derived default explained, the links' states, *Run now*), **History** (events).
- **Absence** (`staff:view`, decisions by right): the calendar — a month per
  department with people as rows and absences as bars, types only where the caller
  may see them — and the **Approvals** inbox listing what the caller may decide,
  oldest first, approve or reject with a reason inline.
- **Settings** (`staff:manage`): structure (departments with managers, locations,
  legal entities), absence types (seeds with their a-melding mapping shown read-only,
  own types editable), holiday schemes with the default, the self-certification
  policy as the three presets or custom numbers, the account settings, retention
  periods, the zone, pension provider and insurer, import and export, the payroll
  export for a month, *Run retention now*, *Run accounts now*.
- **Host**: the tile and routes; dashboard items from `/stats/attention`; the
  spotlight actions; the permission labels in both languages.

## 8. Platform and identity changes

1. **`internal/calendar`** (S11): the Norwegian public-holiday functions moved from
   `srv/invoices/reminderrules/months.go`, exported, with their tests; Invoices
   imports the package; rule 1's allowed list gains it; a purity test keeps it
   stdlib-only.
2. **Identity** (S14): migration `00043_identity_pending_users.sql` — `employee_number`
   varchar(16) unique nullable on `identity.users`; a check that allows a disabled user
   with a null `password_hash`; the invitation gains a nullable `user_id` for the user
   it completes. The `AccountProvisioning` implementation in `srv/identity/accounts.go`;
   `PostIdentityInvitationsAccept` completes the named pending user (sets the password,
   confirms the email, keeps the disabled flag as it is — activation is the worker's
   step, which precedes the invitation's delivery, so by the time the person clicks the
   account is enabled); `Deactivate` ends sessions as the owner's disable does
   (`srv/identity/users.go:456`). The session validation already refuses a disabled
   user; a pending user has no credentials to log in with. The owner's users list shows
   `pending` as a state.
3. **Slots** (S14, S24): `Accounts` and `Staff` as single-provider slots on
   `module.Module` and `Deps`, resolved by `Compose` after `Users`; `Deps.Accounts`
   is always set (identity is always mounted); `Deps.Staff` is nil when the module is
   off. `module.Workers` unchanged.
4. **Config**: `STAFF_ACCOUNTS_ENABLED` and `STAFF_RETENTION_ENABLED` (default on),
   `STAFF_WORKERS_POLL` (default 1 h; each worker acts once per business day and
   otherwise retries failures), `STAFF_IMPORT_MAX_ROWS` (default 5000). `MODULES`
   default gains `staff`. depguard rule set; `moduleSchemas`; the OpenAPI module list.
5. **Storage scope** `staff`; the operator page lists it.

## 9. Edge cases

- A person with two concurrent employments has one holiday account (the scheme of
  the employment that started first), one set of counters, one account flag.
- An employment ended with a future end date keeps the account active until the
  day after; ending it today disables tomorrow's run — or now with *Run now*.
- `provision_from` set before an employment start is honoured (an early account);
  set after it, the person works without an account until then.
- A rehire: a new employment on an erased person reopens a D record; the links and
  the employee number are reused; `Prepare` finds the existing user by employee
  number and the worker activates it.
- Two people with the same work email: the second is refused on the field.
- A manager who leaves: their reports' requests route to the department's manager,
  else to `staff:approve` and HR; attention lists "reports without a manager".
- Egenmelding registered for a day already covered by a sykmelding: refused as
  overlap; a sykmelding covering an egenmelding converts the earlier days' type and
  keeps the case.
- Holiday crossing a year boundary is two accounts' worth, split at New Year.
- A document uploaded for an erased person is refused (`person_erased`); a hold set
  on an already erased person is accepted (it stops a future rehire's erase).
- Public holidays falling in an approved ferie are not deducted; a company that
  treats inneklemte dager otherwise adds its own `time_off` type.
- The payroll export for a month that includes an employment's end reports the end
  and its reason in that month only.
- Importing a row whose employee number exists updates the person; the sealed
  fields update only when the column is present and non-empty.

## 10. Testing

Backend: shaping per caller (self, colleague, manager, approver, HR, personal-data,
outsider); the terms chain (in-force lookup, derived dates, refusals); national id
validation incl. d-nummer, duplicates by hash, reveal events; bank account MOD11 and
IBAN; the holiday account on both bases across a year boundary, proration, 60+,
carry-over caps; egenmelding counters under the three policies; the sixteen-day
case rule and milestones; decision routing incl. own request and a manager without an
account; overlap and percentages; documents (types, size, visibility, erase of
objects, the object store fake); the accounts worker against an identity fake and
the real identity in-process (prepare, activate with the mail fake, deactivate, rehire,
failure and retry); the retention run (classes, hold, re-assessment, idempotence);
the payroll CSV golden file; import dry run and rows; stats and attention per caller;
contract coverage for `StaffDirectory` and `AccountProvisioning`; the harness-wide
check that no contract, mail or storage call happens inside a transaction; the
cross-schema scan; config: `staff` alone.

Frontend: My page incl. the request sheet at phone width; People list and the
two-step creation with the required-field explanation; the person tabs incl. reveal
flow, terms timeline, contract check, case milestones, account flag with derived date;
the calendar's shaping; approvals; settings incl. seeds read-only; catalog parity;
dashboard items.

## 11. Readings on the record

For the owner's verdict; each is how the design read a decision or a gap.

1. **Terms are one row per change, not one table per concern.** Pay, structure and
   working time change together often enough that one dated row is simpler than
   several; the a-melding's derived dates come out of the one chain.
2. **`employmentType` and `ftePercent` are refused when missing**, not defaulted,
   because the law's presumption makes a silent default a liability (R §4.1).
3. **The holiday scheme's unit is the company's choice**, seeded as the law counts
   (virkedager), because HR configures entitlements (R §10.6) and the two bases
   differ by a day a week.
4. **A ferie request beyond the remaining days is a warning, not a refusal.**
5. **Egenmelding past the policy is refused**, with the reason, since registering it
   would be wrong for payroll; the person registers a sykmelding instead.
6. **Who decides**: direct manager by terms, the department's manager as fallback,
   `staff:approve` and HR always; nobody their own.
7. **A manager without an account decides nothing** until the account exists; the
   accounts worker is what gives most managers one.
8. **Bank account and national id are HR's to change**, not the employee's.
9. **The payroll CSV omits the sealed identifiers**; payroll holds them already.
10. **Retention defaults**: 90 days' grace for class D, 365 for class E after the
    case closes, a re-assessment at twelve months; the health period is
    **UNCERTAIN** in the research and set here as a conservative default HR edits.
11. **Health documents may be flagged "not for the person"** as an exception; the
    regulation's right of access makes this a short-lived flag, and attention shows it.
12. **The identity link's shape is provider-keyed from the start**, so the Entra
    module adds a provider value and no migration.
13. **`Prepare` grants only the `User` role**; RBAC roles stay the authorisation
    admin's in this phase.
14. **`provisionFrom` null derives from the earliest active employment**, and HR sets
    a date only to deviate.
15. **Disable the day after the end date**, so the last day is a working day with an
    account.
16. **The business time zone is this module's own setting**, defaulted like
    Expenses'; a platform-wide zone is a later consolidation (R §9 question 6 left it
    open).
17. **`StaffDirectory` is defined and covered with no consumer**, so the next modules
    read without a contract design.
18. **The public-holiday calendar moves in delivery B**, touching Invoices only by
    import path.
19. **Documents accept the receipt types only** (PDF, JPEG, PNG, HEIC); Office formats
    are a later sniffing question.
20. **Overlapping employments are shown, not refused.**
21. **An erased person is read-only except the hold and a rehire.**
22. **One schema migration for the phase** (`00042`) plus identity's `00043`.

## 12. Open questions

1. The health retention period (reading 10): keep 365 days after the case closes, or
   tie it to the refund claim's closure, which this module does not see?
2. Should the employee be allowed to **propose** a change to bank account or
   national id (a request HR confirms), rather than ask out of band?
3. The default `createBeforeDays`: 14, or 0 until the Entra module exists, since a
   disabled local account has little use before the start?
4. Should the Owner's existing users without a person be **adopted** into the
   register (a "create person from user" action), so the installation's current
   staff gets records without re-typing?
