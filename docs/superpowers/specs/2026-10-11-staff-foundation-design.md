# Staff — the workforce register, absence and the account flag (phase 1)

A new module, `staff`. Research: `docs/superpowers/research/2026-10-11-staff-module.md`
("R", cited by section); its §10 holds the nineteen decisions the owner took on
2026-10-11, which this design implements and does not reopen. Revision 4 (A2 pre-flight), after the
critical review of 2026-10-11 (five blockers, fourteen major findings, thirty minor —
every one taken in its place), its re-check (seventeen edits, taken; its five open
questions answered as readings 30–34), and the A1 plan's pre-flight of the same day
(the amendments marked **(pre-flight)** below), and the A2 plan's pre-flight (marked
**(A2 pre-flight)**).
Code references are against `main` at `6941e58d`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. Purpose

Give the company one record of every person who works for it, and let that record be
what everything else about the person hangs on:

- HR keeps the **person** — name, identity, contact, next of kin, bank — and their
  **employments**, each a Norwegian arbeidsforhold with the fields the a-melding and
  the written contract require, as dated rows that never overwrite history;
- the person's **absence** — holiday under the company's own scheme, sickness with the
  statutory follow-up timeline, leave in the statutory vocabulary, care days for a
  sick child — is requested, decided, notified and shown on a calendar, and leaves the
  module in the shape payroll needs;
- a **flag** on the person decides whether and when they get a user account: created
  disabled ahead of the start, enabled on the day, disabled the day after the end,
  never deleted, under one **authority** per installation. In this phase the account
  is Vantigo's own where the installation has no identity provider, and a link to the
  existing one where it has; the Entra module (R §10.10) adds the external provider;
- a **document archive** per person, a **payroll export**, the employee's own page, and
  the retention rules the law puts on all of it.

The sequence decided (R §10.11): **this phase** (register, absence, the account flag
with the local path) → the Entra module → Staff phase 2 (competence and preferences)
→ Staffing. Four deliveries, four PRs, **one migration each** (each number assigned
when its delivery's first task lands; the invoices phase 4 PR 2 branch is in flight):
A1's creates `people` and `settings` with every §3 column they will ever need in this
phase (the retention and account columns included) plus the A1 tables,
`identity_links` among them; A2's adds `documents`, `document_categories` and
`object_deletions`; B's adds the absence tables, `employments.holiday_scheme_id` with
its key, and the absence rate's inputs; C's spans `identity` and `staff` in one file
(the precedent of `00024` and `00040`) for the columns of S24, the invitation's
`user_id` and `provisioning_roles`. A2's payroll export ships without
leave and absence columns, which B adds; A2's stats ship without the absence rate,
which B adds.

- **A1 — People and employments**: the module, people with the sealed identifiers,
  employments and their dated terms, structure (legal entities, business units,
  departments, locations), person roles, permissions, events, settings, the
  **self-service link** between a person and an existing user (`identity_links`,
  S24 — **pre-flight**), the People, Person and Settings pages, My page's profile,
  the docs.
- **A2 — Records**: documents and their categories, `object_deletions`, import and
  export, the payroll CSV behind its port, the personal-data export, the retention
  worker, stats and attention.
- **B — Absence**: the public-holiday calendar moved to the platform, absence types,
  holiday schemes and accounts (adding `holiday_scheme_id` and its key to
  `employments` — **pre-flight**), requests, decisions and notifications, sick-leave
  cases with milestones, care days, the calendar, the approvals inbox, My page's
  absence.
- **C — Accounts**: identity's provisioning contract, managed users, the employee
  role, the authority mode, the flag and its dates, the **managed** link, the
  accounts worker, adoption of existing users, the Account tab.

Out of scope, named: payroll itself (a-melding, skattekort, feriepenger arithmetic,
inntektsmelding — R §1.3); competence, certificates and preferences (phase 2);
groups and the Entra provider (the Entra module); contract generation and e-signing;
onboarding and offboarding checklists; equipment; medarbeidersamtaler; NAV sykmelding
import; shift or project planning; a time-and-attendance reconciliation of Time's
hours with the absence plan; moving Time's person rate card (R §10.3); reading
Projects for anything; a "propose a change" workflow for the employee (reading 23);
`contracts.StaffDirectory` (deferred to its first consumer, reading 17); a photo on
the person (identity's avatar is the one picture).

## 2. Decisions

### The register

**S1 — Its own module, depending on nobody but identity.** `MODULES` may enable
`staff` alone. Nothing here imports another module, no SQL crosses a schema, and the
module reads identity only through contracts (`UserDirectory`, and the new
`AccountProvisioning` of S14). It is the Expenses shape (boundaries.md:396-417).

**S2 — Three things, kept apart: person, employment, account.** A **person**
(`staff.people`) is a human the company has a relationship with, stable across
hires and across accounts, carrying a permanent **employee number**. An
**employment** (`staff.employments`) is one arbeidsforhold — the a-melding's unit,
with its own id, start, end and end reason — and its **terms**
(`staff.employment_terms`) are dated rows, a new row per change, the way Tripletex's
"Ny rad" and `time.person_rates` work (R §2.1, §7.4). An **account** is identity's
user, linked from the person (`staff.identity_links`), never the other way round.
A person may have several employments, sequential (a rehire, a move between units)
or concurrent (two part-time positions in two units — Huma and Unimicro model it,
R §2.10); the register does not refuse overlap, it shows it.

**S3 — The employee number is permanent and short.** At most 16 characters (Entra's
`employeeId` bound, R §5.5), unique per installation, assigned by HR or taken from
the installation's own sequence (`settings.employee_number_prefix` plus
`next_employee_number`, the prefix validated so the longest number fits), and
**kept on rehire**, so the account the number correlates is re-enabled rather than
re-created. It is the key every export and every provider sees; the national
identity number never is.

**S4 — The national identity number is stored sealed, found by MAC, shown to nobody.**
The fødselsnummer or d-nummer (11 digits, both control digits checked; a d-nummer's
first digit is raised by 4; the synthetic test ranges — month + 40 and month + 80 —
are accepted, reading 24) or a foreign identifier with its country is written through
`secrets.Box` under the purpose `staff/national-id`. Beside it, a MAC of the
domain-separated input (`kind | country | normalised id`) under
`Box.MAC("staff/national-id-index", …)` — a new platform method, HMAC-SHA256 under
the purpose's HKDF-derived key — in a unique column, so a duplicate is refused and a
lookup is possible without opening anything. **No digit of it is ever answered
outside `staff:personal-data`**: responses carry `nationalIdKind` and
`nationalIdOnFile`, nothing else, because the last four digits with the birth date
reconstruct the whole number (the seventh digit follows from the first control
digit). It is revealed only by `POST /people/{id}/national-id/reveal`
(`Cache-Control: no-store`), each reveal an event naming the viewer, and written only
by `PUT /people/{id}/national-id` under the same permission. The bank account is
sealed the same way (`staff/bank-account`); its last four digits are harmless and
shown; MOD11 is checked on a Norwegian account number, IBAN by its check digits; a
change mails the person. Customers' D1 refusal (R §7.4) does not apply: the number
is what payroll, pension and the HMS-kort are keyed on (R §10.1). An `APP_SECRET`
rotation breaks both the opening and the index; the operator page says so.

**S5 — Employment terms carry the a-melding and the contract.** One row per change,
effective from a date, holding what Skatteetaten reports monthly and what aml.
§ 14-6 requires written (R §4.1, §4.7): business unit, department, location, direct
manager, title, STYRK-08 yrkeskode, hours per week for a full position,
stillingsprosent, arbeidstidsordning, ansettelsesform (the five a-melding values),
remuneration type (optional; not among Skatteetaten's mandatory fields, reading 25),
the pay components as the contract states them (named amounts, text to the module,
payroll-sourced — R §10.7), payment method and payday, the working-time text and
break length, overtime and shift-change rules, the probation end with an agreed
extension, notice periods as a value and a unit, the tariff reference, the holiday
and holiday-pay reference, the competence-development reference, the
social-security and pension reference, the innleier when hired out, and the temporary
ground and expected end when the employment is temporary. The dates the a-melding
wants derived — `fteChangedAt`, `salaryChangedAt` — are computed from the row chain
(pay components compared as a sorted list of name and amount), never typed.
**(pre-flight)** `full_time_hours_per_week` is checked against the a-melding's rule
per arbeidstidsordning — exactly 36.5 for `shift_365`, exactly 35.5 for
`round_the_clock_355`, 28–33.6 for `offshore_336` and `continuous_336`, free for
`not_shift` and `other_shift` — refused `full_time_hours_mismatch`; the
`employment_number` (the arbeidsforholdID) is typed or generated as
`<employee_number>-<n>` under the person lock, immutable, unique
(`employment_number_taken`); the § 14-6 check answers each item as `present`,
`missing` or `not_applicable` (e only when temporary, f only when probation is agreed,
o only when hired out) with its source, and `contractSigned` apart from the items.
`employmentType` (fast or midlertidig) and `ftePercent` are **required** on every row:
the law presumes the employee's claim when they are missing (R §4.1), so the module
refuses a row without them rather than defaulting. The first row is dated the
employment's start; a later row's values default from the one before; HR may correct
a past row (an event records before and after), reading 11.

**S6 — Structure is four small tables, not an org tree.** `legal_entities` (the
juridisk enhet with its organisation number), `business_units` (the virksomhet or
underenhet with its own organisation number, under a legal entity — the a-melding
reports per virksomhet, R §4.7), `departments` (code, name, manager, cost centre,
division, optional parent — cycles refused) and `locations` (the arbeidssted, with
address and country). An employment names its business unit; the terms name the
department, location and manager. The org view is derived from the direct manager on
the terms; a department's manager is the default when the terms name none. Teams and
groups wait for the Entra module (R §10.14). **Person roles** (`person_roles`:
verneombud, AMU member, HMS responsible, with elected-from and -to) carry what
R §4.10 asks and feed the headcount checklist.

**S7 — Directory fields are the person's, in Staff.** What a provider will write (R
§10.19) is editable here from the start: work email, work phone, mobile, office
location, work country, division, cost centre, company (the business unit), job
title, employee type, manager, preferred language — so the Entra phase adds a mapping
table and a provider, not columns.

### Absence

**S8 — Absence is one table of dated rows under a vocabulary.** `absence_types`
seeds the statutory vocabulary (R §4.6) — ferie, egenmelding, sykmelding,
svangerskapskontroll, svangerskapspermisjon, omsorgspermisjon, fødselspermisjon,
foreldrepermisjon, ammefri, sykt barn, pleie av nærstående, utdanningspermisjon
(lovfestet, ikke lovfestet), militærtjeneste, offentlig verv, religiøs høytid,
velferdspermisjon, permittering, avspasering, helse (a dentist, a doctor), annen
lovfestet, annen ikke-lovfestet — each with a `kind` the rules key on, a paid flag,
whether it draws on the holiday account, whether it allows a percentage, its
**unit** (days, or hours for helse and avspasering — a dentist visit is two hours of
one day), the a-melding leave description it maps to, whether it needs a decision or
is a notification, and its **visibility**: `absent` (colleagues see "absent", the
manager and HR the type), or `hr_only` (the manager sees "leave" — seeded for
religiøs høytid, svangerskapskontroll, svangerskapspermisjon and fødselspermisjon,
which reveal special-category data). A company adds its own types above the seeds
and may hide a seed; a seed is never deleted. An **absence** (`absences`) is a
person, a type, a first and last day (or one day and hours), a percentage, a status
and the decision; a sick absence belongs to a **case** and carries the degree; a
sick absence has **no free-text note** (M8).

**S9 — Holiday is an account per person per year under a company scheme, counted in
working days.** HR configures the **schemes** (R §10.6): a name, the entitlement in
**virkedager** as the law counts (Monday–Saturday), the extra days from the year the
person turns 60, the main-period days, the days for a start after 30 September, and
the maximum agreed carry-over; one scheme is the default. The statutory scheme is
seeded — 25 virkedager, 6 extra at 60, 18 main-period, 6 after September, 12 carry-over
— and a company adds "five weeks" (30) or anything else above it. **Deduction is per
working day of the person's own week**, and the entitlement is converted the way
Norwegian payroll does: `weeks × 5 + min(rest, 5)` where `weeks = ⌊virkedager / 6⌋`
— 25 → 21, 30 → 25, 6 → 5 — so a Monday–Friday week costs five days of a 21-day
entitlement and never undercounts (M6). A person's working pattern in this phase is
the five-day week; a part-time pattern is a phase 2 column the deduction already
reads through one function (reading 4). A person's employment names its scheme (the
default when it names none). The **holiday account** for a year is derived:
entitlement (the after-September days when the earliest active employment starts
after 30 September; the 60+ days from the year of the birthday, un-prorated, reading
5), carried in (agreed, capped; forced, uncapped, with its reason), taken (approved
days of types that draw on the account, counted on working days, public holidays
excluded), pending (requested, shown apart), remaining. **Untaken days do not
vanish**: in January the attention list names every person with days left from the
year before and offers the carry-over in one action, agreed or forced with a
reason; HR may also set the installation to carry over automatically as forced
(`auto_carry_over`, default off). **Sickness during ferie returns the days** (fl. § 9 (1): helt
arbeidsufør, with a legeerklæring): a **100 % sykmelding** over approved ferie is
accepted and the ferie row is split around it, which alone returns the days to
`remaining` — no adjustment is written; a graded sykmelding over ferie is allowed too
and splits nothing. At year end, returned days still unused carry over under the
January action like any other. Feriepenger are payroll's:
the account holds days, never money.

**S10 — Sickness is a case with a timeline; egenmelding is counted per occurrence
in calendar days.** A **sick case** (`sick_cases`) opens with the first absent day of
a sickness and gathers its egenmelding and sykmelding rows; a sick absence that
starts with fewer than sixteen calendar days between it and the last day of a case joins it
(ftrl. § 8-19 (3), R §4.5). The **arbeidsgiverperiode** ends on the case's sixteenth
*summed* absence day across its rows, not on a calendar offset (M5); the other
milestones — oppfølgingsplan at 4 weeks, dialogmøte 1 at 7, NAV's aktivitetsplikt at
8, dialogmøte 2 at 26 — are dated from the case's first day, and the 52-week end is
an **estimate** NAV may correct, editable. HR or the manager marks each done or
waived ("åpenbart unødvendig", with a reason and the actor). The case carries
functional notes about tilrettelegging that **the manager reads and writes** — the
leader runs the follow-up (aml. § 4-6) — and **never a diagnosis**: there is no field
for one, and the health document category is behind `staff:personal-data`.
**Egenmelding** is one occurrence of up to the policy's calendar days (3 under the
statutory minimum, 8 under the extended scheme, or the company's own), allowed after
two months of employment, counted **per occurrence** against the yearly limit under
the minimum policy (4 occurrences in a rolling year) and **per day** under the
extended one (24 days), never past the sixteenth summed absence day of its case,
and refused with the reason when the counters say the person must bring a
sykmelding. HR may **withdraw** a person's right to egenmelding for a period
(ftrl. § 8-27), which refuses the type for them with `self_certification_withdrawn`.
Egenmelding is counted in calendar days whatever the holiday basis.

**S11 — Care days are a counter with a derived entitlement.** `child_sick` absences
count against the person's omsorgsdager for the calendar year: 10, 15 with more than
two children under twelve, doubled for a sole carer, plus 10 per chronically ill
child (ftrl. § 9-6, R §4.6) — derived from three fields on the person
(`children_under_12`, `sole_carer`, `chronically_ill_children`, class D, typed by HR
from what the employee declares; no child is named). The first ten days are the
employer's, the rest are reported to payroll with the export.

**S12 — Who decides, and who is told.** A type that needs a decision (ferie,
permisjon, avspasering, the company's own) goes `requested → approved | rejected`,
cancellable by the person while requested and by the decider after. The deciders are
the person's **direct manager** on the terms in force (or the department's manager
when the terms name none), anyone with `staff:approve`, and HR (`staff:manage`).
Nobody decides their own request; a manager's goes to their manager; when the chain
ends — the sole Owner, or a manager without a manager — and no other decider holds
an account, the person may approve their own with an event (`self_approved`). A type
that is a notification (egenmelding, sykmelding, sykt barn, helse) is `registered`
when entered, by the person or by HR on their behalf. **Notifications** go by the
installation's mail (`mail.SendOutbound` through the `Deps.SMTPSend` seam as
Invoices does, skipped with a log line when the mail driver is not SMTP — pre-flight;
after commit, never under a lock): to the deciders
on a request, to the person on a decision, to the manager on a registration, to the
person on a bank-account change, and to HR on a worker failure the attention list
also shows. A decided absence in the past can be corrected by HR only, as an event.

**S13 — Public holidays come from the platform.** The Norwegian calendar that lives
inside Invoices (`srv/invoices/reminderrules/months.go:81-115`, R §7.4) moves to
`internal/calendar` — `PublicHoliday(civil date at UTC midnight) bool`,
`NextBusinessDay`, `EasterSunday` — with the UTC-midnight civil-date convention
stated on the package, its own purity test (standard library only), a depguard rule
as a platform package, and rule 1's allowed list gaining it; Invoices imports it from
there, behaviour unchanged, and its reminderrules purity test admits the one import.
A region beyond Norway is a later column on the settings.

### Documents, retention, data

**S14 — Documents are the receipt pattern with categories and classes.**
`staff.documents` rows point at objects in the `staff` storage scope
(`storage.NewScope`, R §7.4): PDF, JPEG, PNG and HEIC, sniffed, 10 MiB, the object
written before the row's transaction and removed when it refuses, downloads
streamed through the API with `nosniff` and a disposition. A document has a
**category** (contract, attest, certificate, correspondence, health, other, and the
company's own), an optional `validTo`, an optional `signedAt`, and the **retention
class** its category implies (S15). The `health` category is readable only under
`staff:personal-data` and by the person it concerns; every other document is visible
to the person it concerns as well as to HR.

**S15 — Retention is per class on every column and category, with an erase run that
finishes.** Six classes (R §4.9, §1.2 finding 9): **A** pay and contract
documentation, five years after the year the employment ended — the terms'
pay-related columns, the payroll exports' lineage, documents of category contract
and attest (reading 10); **B** hours (Time's, not this module's); **C** who was
employed when — names, birth date, gender, employee number, employments, terms other
than pay, person roles, the events' kinds and dates, identity links; **D** personnel
administration — contact and address (private and work), next of kin, the care-day
fields, bank account, national id, notes, documents of category correspondence,
certificate and other, absence rows other than sick (dates and type kept as C, notes
dropped); **E** health — sick cases' notes and milestones' notes, health documents,
sick absences' degree; **F** hold (`people.legal_hold` with a reason and who set it
stops every erase for the person). The `staff-retention` worker runs daily: for every
person whose last employment ended more than `retentionGraceDays` ago (default 90),
not held, it blanks the D columns, deletes the D rows and queues the D objects; for
every **closed case** older than `healthRetentionDays` (default 365), whatever the
employment state, it blanks the E content and queues the health objects; for every
A item older than five years after its year, it blanks. Objects go through
`object_deletions` (object key, attempts, next attempt), retried every cycle until the
store confirms, so a legal erase is never best-effort (M7). It writes an `erased`
event per person listing kinds and counts, and a **re-assessment** attention item
is derived — due twelve months after the last employment's end for a person with
`erased_at` set and `reassessed_at` null — cleared by `reassessed_at` and
`reassessed_by`. **(A2 pre-flight)** The three schedules are three due queries, each
over what remains: **D** — `erased_at` null, every employment ended before today
minus the grace, not held; **health** — a *health document* is one in a **restricted**
category (restricted ⇔ class E), deleted `healthRetentionDays` after the last
employment's end until B attaches documents to a sick case (`documents.case_id`,
nullable, added by B) and moves the attached ones to the case's schedule, current
staff's health documents being kept until then; **A** — per employment whose end
year is more than five years past, nulling that employment's pay columns, deleting
its A documents and blanking the A fields of its events' payloads, so a rehired
person's old employment loses its pay terms while they work again. Document rows
are **deleted**, never marked: a delete and an erase both remove the row and insert
the object's ledger row in the same transaction, and the ledger finishes the object
after commit. A person erased is read-only except the hold, the reassessment, the
unlink of their user, the reads, and a rehire, which
opens a fresh D record under the same employee number; because the MAC is gone, the
creation form warns of an erased person with the same name and birth date so a
duplicate is a choice, not an accident. Identity's user is never deleted by this
module (R §10.13); `Deactivate` is the most it asks.

**S16 — The person's own data, exported.** `GET /me/export` (the person's own,
the sealed values omitted — reading 31) and `GET /people/{id}/personal-data`
(`staff:personal-data`) stream one JSON document with every field group, every
absence, every case, every event and the documents' metadata (the files follow on the
archive's download endpoints), the shape of customers' export (R §7.4) — built in
memory in one snapshot, never refused for size, and **an audited reveal**: an event
`personal_data_exported` naming the viewer (A2 pre-flight). `/me/export` carries the
person's own data without the sealed values (reading 31) and without HR's notes on
them, its events shaped as Self's; the HR-issued export is the complete answer to an
innsyn request (reading 35, for the owner's verdict). The module
does not implement `CustomerPersonalData`: a person who is also a customer is a later
concern.

**S17 — Self-service is a short list of own fields, each change an event.** A person
with `staff:access` sees their own record, employments and terms (pay components
included — it is their contract), absence, care days and documents, and edits their
private phone and email, address and next of kin directly. Bank account and national
id are changed only under `staff:personal-data` (a fraud boundary, reading 8); the
person asks HR.

**S18 — Payroll export behind a port.** `staff.PayrollExporter` is an in-module
interface — `Export(ctx, month, rows) (contentType, []byte, error)` — with one
implementation, `csvExporter`, and `GET /payroll/export?month=YYYY-MM&format=csv`
(`staff:manage`). A row per employment active in the month: employee number, name,
the sealed identifiers **omitted** (payroll already holds them, reading 9), the
employment number, business unit organisation number, start, end and end reason, the
terms in force on the last day of the month (yrkeskode, full-time hours, fte,
`fteChangedAt`, arbeidstidsordning, ansettelsesform, remuneration type,
`salaryChangedAt`, department, cost centre, seniority date and pay grade when set),
then one row per **leave whose total length exceeds fourteen days** and that touches
the month, with the absence id as its `permisjonId`, its whole first and last day, its
a-melding description and percentage, then the absence days per type and the care
days used beyond the employer's ten. Semicolon, BOM, CRLF, as Expenses' CSV
(`srv/expenses/reimbursements.go:584-594`, decimal comma included). An export is an event with its hash; the rows
it drew on are class A for five years.

**S19 — Import and export of the register.** `POST /people/import` (CSV, dry run
first, per-row plan create or update keyed on employee number, a failed-rows file —
customers' import, `srv/customers/import.go`), `GET /people/export.csv` and
`GET /people/import-template.csv`, under `staff:manage`; a file carrying the
national-id or bank-account columns is **refused unless the caller also holds
`staff:personal-data`** (customers' column-permission rule), and the sealed fields
never export.

**S20 — Attention and stats.** `GET /stats` (headcount, FTE sum, people on leave
today, open sick cases, absence rate for the trailing twelve months) and
`GET /stats/attention`, the host dashboard's items, shaped by who asks (customers'
follow-ups; item types in camelCase as every module's): absence awaiting the
caller's decision; a contract overdue (neither a typed `contract_signed_at` nor a
signed contract document on an employment seven days after its start — immediately
for one under a month or hired out, R §4.1); a probation ending within a month; a
temporary employment past twelve months (notice due, any ground) or whose chain of
temporary employments on grounds a or b for the person passes thirty-six (converts,
R §4.2);
a sick-case milestone due or overdue for the caller's reports; untaken holiday from
last year (January) and fewer than the main-period days approved by 1 June; reports
without a manager; a provisioning failure or a person blocked from an account, with
the reason; an invitation unanswered; a retention re-assessment due; the deletion
ledger failing (A2 pre-flight); the OTP threshold crossed (R §4.8 — judged without an
owner flag: one person at ≥ 75 %, two, or ≥ 20 % positions summing to two årsverk,
the item naming the owner exception); a headcount threshold crossed and not yet
acknowledged (5: varslingsrutine and verneombud; 30: AMU; 50: ARP — R §4.10),
acknowledged per threshold by `staff:manage`, once and permanently, as an event.

**S21 — Events are the history, shaped like the data, by field group.** `staff.events` (person,
nullable for installation events; kind; actor snapshot; instant; payload) records
every mutation — created, field changed (the field names, never a sealed value),
terms added or corrected, absence requested, decided and corrected, case milestone
done or waived, document added or removed, account prepared, activated,
deactivated, updated or refused, export made, erased, hold set, threshold
acknowledged — and `GET /people/{id}/events` lists them with each payload shaped by
the field groups the caller may see (§5): a D or E detail the caller may not see is
replaced by the field's name and "changed". **(pre-flight)** The shaping keys on a
static field → §5 group table applied per field, never on the retention classes,
which are not visibility groups (`work_email` is class D yet in the Work group;
`birth_date` is class C yet Private).

### Accounts

**S22 — The account flag, in three settings.** On the person: `provisionAccount`
(true or false, HR's choice; false for the rare person who gets none) and
`provisionFrom` (a date; null means "the first day of the earliest employment that
has not ended", recomputed when that changes). On the installation:
`createBeforeDays` (default 14; the account is created **disabled** that many days
before `provisionFrom`; 0 means on the day — reading 3) and `deleteExternalAfterDays`
(default null, never; **inert in this phase**, read by the Entra module). The leaver
side needs no setting: the account is **disabled on the day after the last
employment's end date**, and re-enabled when a new employment with the flag starts
(R §10.8, §10.12, §10.13). The person chooses, on the flag, which address the account
is keyed on — `work` (default) or `private` for the shift worker with no company
mailbox (M4). A person with the flag off and an account already linked keeps it until
HR disables it with `POST /people/{id}/account/disable`; the flag governs what the
worker does, not what exists.

**S23 — One authority per installation, reported by identity.** The contract's
`Mode` answers `local` when identity signs people in with its own credentials only,
and `federated` when workforce OIDC or SCIM is configured (today from the `OIDC_*` and
`SCIM_TOKEN` configuration; from the database once R §10.17 lands). In **local** mode
Staff is the authority: the worker creates, invites, enables and disables. In
**federated** mode the identity provider is the authority and the local path is
**link-only**: `Prepare` finds and links and never creates, no invitation is ever
sent, `Activate` does nothing but report, and only `Deactivate` on the day after the
end runs, because a leaver must lose access whatever the provider does — OIDC
just-in-time provisioning links by subject and never by email
(`srv/identity/oidc.go:553-572`), and SCIM refuses a duplicate email
(`srv/identity/scim_users.go:430-432`), so a locally created pending user would turn
every SSO sign-in into `oidc_account_locked`. The Entra module later fills the
provider slot and becomes the creator in federated mode; the local path's shape does
not change. The mode is shown on the Account tab and in `GET /meta`.

**S24 — Managed users, adopted explicitly, never Owners.** Identity gains
`managed_by` (`staff` or null), `employee_number` (varchar(16), unique when set),
`pending_since` and `disabled_by` (`owner` or `staff`) on `identity.users`. `Prepare`
creates **managed** users; an existing user is linked only when it is already marked
managed with the employee number — which an Owner does once, under `OwnerManagement`
(Owner plus MFA), through identity's own `POST /owner/users/{id}/manage {module:
"staff", employeeNumber}` (and `/unmanage`), the **adoption** that also brings the
installation's current staff into the register (open question 4, now in scope).
**(pre-flight)** The **self-service link** of A1 is a different thing: it ties a
person to any existing active user for the Self and Manager audiences only, Owners
included; it never makes the user managed, and the worker never acts on it.
`Prepare` by email alone finds nothing; the email is checked only for a collision
(`account_email_taken`). `Prepare` (when it links), `Activate`, `Deactivate`
and `Update` refuse an unmanaged user (`account_not_managed`) and any Owner or
SystemAdmin (`account_privileged` — the Owner handles Owners by hand); adoption
refuses Owners and SystemAdmins the same way; promoting a managed user to Owner
**unmanages** them, audited, and the link reports `account_privileged`; `Deactivate`
refuses the last active Owner as identity's own disable does
(`srv/identity/users.go:452-530`). **An Owner's disable wins**:
`Activate` never re-enables a user whose `disabled_by` is `owner`; it reports
`blocked_by_owner` to attention. Every contract method takes an **actor** (the HR
user who pressed *Run now*, or `system:staff-accounts`) that identity writes to its
audit (`srv/identity/audit.go:34-64`: a user actor as `actor_user_id`; a system actor
as a null `actor_user_id` with `details.actor = "system:staff-accounts"` and no
request trace, a new case beside the bootstrap's anonymous one). Identity's delete refuses a managed user
(`account_managed`); its users list shows `pending` and "managed by Staff"; its
`Resend` carries the invitation's `user_id`.

**S25 — The provisioning contract.** A new single-provider slot `Accounts` carries
`contracts.AccountProvisioning`, implemented by identity, consumed by staff through
`Deps.Accounts`, resolved by `Compose` **and by `module.Workers`** (B1: today
`module.Workers` resolves no single-provider slot, `srv/module/workers.go:20-42`, and
worker-mode Deps carry no `Users`, `apps/server/cmd/vantigo/main.go:261-283`; both gain the
slot, and identity's provider is buildable from worker-mode Deps):

```go
type Actor struct{ UserID *uuid.UUID; System string }   // one of the two
type AccountSpec struct {
    Email, DisplayName, PreferredLanguage string           // the chosen address; 'en' | 'nb'
    EmployeeNumber                         string           // correlation, kept on the user
    RoleIDs                                []uuid.UUID      // custom roles only (S26)
}
type AccountRef struct {
    UserID     uuid.UUID
    State      string // "pending" | "active" | "disabled"
    DisabledBy string // "" | "owner" | "staff"
    Managed    bool
}
type AccountProvisioning interface {
    Mode(ctx context.Context) (string, error)                       // "local" | "federated"
    // Prepare: local — create a managed, disabled, credential-less user, or find the
    // managed one with the employee number; federated — find only. Idempotent.
    Prepare(ctx context.Context, actor Actor, spec AccountSpec) (AccountRef, error)
    // Activate: enable; assign the roles; when the user has no credential (no
    // password, no passkey, no OIDC link) and no active invitation, issue and
    // deliver one. Local mode only. Idempotent.
    Activate(ctx context.Context, actor Actor, userID uuid.UUID) (AccountRef, error)
    // Deactivate: disable, end sessions, revoke pending invitations. Idempotent.
    Deactivate(ctx context.Context, actor Actor, userID uuid.UUID) (AccountRef, error)
    // Update: display name, email and language on a managed user (the mover);
    // federated mode: language and employee number only.
    Update(ctx context.Context, actor Actor, userID uuid.UUID, spec AccountSpec) (AccountRef, error)
    Account(ctx context.Context, userID uuid.UUID) (*AccountRef, error) // (nil, nil) if unknown
}
```

It is a **new cross-module write direction**, designed as boundaries.md:108-109
asks, not a holder: every method runs on identity's own pool, in identity's own
transactions, outside any of staff's — staff's worker holds no transaction and no
lease connection across a call — and mails after commit as `deliverInvitation`
already does (`srv/identity/invitations.go:125-135`). `FindByEmail` is not offered
(M4). Identity gains for it: the columns of S24; a `pending` state that is explicit
(`managed_by` set, `pending_since` set, no credential), not inferred from a null
password — OIDC and SCIM users have none either (M2); an invitation that names the
user it completes (`invitations.user_id`; it stores role `User`, which the
`identity.invitations.role` check allows, and accept assigns nothing beyond what
`Prepare` already gave — the built-in `User` role every account has), so
`PostIdentityInvitationsAccept`
(`srv/identity/invitations.go:241`) **completes** that user — sets the password,
confirms the email, clears `pending_since`, assigns no role itself (the roles were
assigned at activation), audits a distinct action — instead of inserting one and
refusing the email; an invitation is issued only when none is active, re-issued by
the next cycle after `INVITATION_LIFETIME` expires (default 168 h) at most three
times, then attention
(`invitation_unanswered`); a mail failure revokes the invitation as today and counts
as an attempt; the Owner's manual invite answers 409 `account_exists` for a pending
person, documented.

**S26 — The employee role is a custom role the Owner names.** The built-in `User`
role carries no permissions and cannot be edited
(`srv/identity/authorization.go:657`; `mig/00002_identity_baseline.sql:44-46`), so a
provisioned employee would see an empty dashboard (B2). **Identity holds the set of
roles Staff may assign** (`identity.provisioning_roles`, written only by an Owner under
`OwnerManagement` through `PUT /owner/provisioning-roles`, custom roles only —
typically one "Employee" role with `staff:access`, `time:access` and
`expenses:access`), because `staff:manage` must not be able to mint an account holding
any role it names (identity's `authorizeAssignment`, `srv/identity/authorization.go:279-314`,
is the gate HR would otherwise bypass). `Activate` assigns exactly that set and refuses
anything else (`role_not_assignable`); it runs only when the link is not yet active,
so an Owner's later removal of a role is not undone daily. Staff's Settings page
reads the set and links to identity's page to change it. With the set empty the
worker does not activate and attention says `employee_role_missing`; the admin page
explains the one-time step. Adopted users keep their roles.

**S27 — The accounts worker, daily, per-link claims, in the business time zone.**
`staff-accounts` runs every `STAFF_WORKERS_POLL` (default 1 h); a successful step
acts once per business day per link, a failed one retries at each poll up to three
times and then daily. A person due for `Prepare` with no link gets one inserted first
(`state = preparing`, `external_id` null) under the person lock, so every step has a
row to claim and a place for its attempts. It reads the due links on the pool,
**claims each with a lease id and a lease-until on the link row** (the invoices reminder worker's pattern,
`srv/invoices/reminder_worker.go:206-216`), releases the connection, calls identity
with no transaction open, and writes the result — never the customers anonymisation
loop, whose one lease connection held across a call is the pool starvation PR #124
fixed (`srv/customers/anonymisation_worker_test.go:647-651`). `POST /accounts/run`
and `POST /people/{id}/account/run` run a cycle on demand (`staff:manage`, the caller
as actor). Three failures put the person on attention. The decision per person per cycle is §4's table.

**S28 — Frontend, one app, mobile-first where the employee is.** `apps/staff/frontend`
(`@vantigo/staff-ui`), the tile "Staff" / "Ansatte". Sidebar: **My page**
(`staff:access`), **People**, **Absence** (calendar and the approvals inbox),
**Settings** (`staff:manage`). My page and the absence request are designed for a
phone first, because the shift-trading decision (R §10.9) makes every employee a user.

**S29 — Documentation is its own task in every delivery.** §8 lists every page
`docs:check` will enforce.

## 3. Data model (schema `staff`)

Every table has `created_at`, `updated_at`; mutable rows have `revision` for
`If-Match`. Ids are `bigint identity` unless noted. User ids referenced from other
schemas are opaque; no cross-schema key. Each column's retention class is in the last
column (S15).

### 3.1 `staff.people`

| Column | Type | Notes | Class |
|---|---|---|---|
| `id` | bigint | | C |
| `employee_number` | varchar(16) not null, unique on `upper()` | S3 | C |
| `given_name`, `family_name`, `display_name` | varchar | display defaults to given + family | C |
| `birth_date` | date | | C |
| `gender` | varchar(12) | `female`, `male`, `other`, `unspecified`; ARP (R §4.12) | C |
| `national_id_kind` | varchar(20) | `fodselsnummer`, `d_nummer`, `foreign` | C |
| `national_id_sealed`, `national_id_mac` | bytea, bytea unique | S4 | D |
| `national_id_country` | char(2) | foreign ids | D |
| `work_email` | varchar(254) | partial unique index where not null | D |
| `phone_work`, `phone_mobile`, `phone_private`, `private_email` | varchar | | D |
| `street`, `postal_code`, `city`, `country` | varchar | home address | D |
| `work_country` | char(2) default `NO` | the usage location | C |
| `preferred_language` | varchar(2) not null default `nb` | | C |
| `bank_account_sealed`, `bank_account_last4`, `bank_account_kind` | bytea, char(4), varchar(10) | `bban`, `iban` | D |
| `children_under_12`, `sole_carer`, `chronically_ill_children` | smallint, boolean, smallint | S11 | D |
| `self_cert_withdrawn_until`, `self_cert_withdrawn_reason` | date, varchar(500) | S10; blanked when the withdrawal lapses plus `healthRetentionDays` | E |
| `provision_account`, `provision_from`, `account_email_kind` | boolean, date, varchar(10) | S22 | C |
| `legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`, `legal_hold_at` | | F | — |
| `erased_at`, `reassessed_at`, `reassessed_by_user_id` | timestamptz, …, uuid | S15 | C |
| `notes` | varchar(2000) | HR's | D |

`staff.next_of_kin` — `person_id`, `name`, `relationship` (`spouse`, `partner`,
`parent`, `child`, `sibling`, `other`), `phone`, `note`; at most 3; class D.
`staff.person_roles` — `person_id`, `role` (`verneombud`, `amu_member`,
`hms_responsible`), `from_date`, `to_date`; class C.

### 3.2 `staff.employments`

| Column | Type | Notes |
|---|---|---|
| `id` | bigint | |
| `person_id` → people | | |
| `employment_number` | varchar(40) not null unique | the a-melding `arbeidsforholdID`; defaults to `<employee_number>-<n>` |
| `relationship_type` | varchar(20) not null | `ordinary`, `maritime`, `freelancer`, `pension_or_other` (R §4.7) |
| `business_unit_id` → business_units | not null | the virksomhet |
| `start_date` | date not null | |
| `end_date` | date | |
| `end_reason` | varchar(40) | one of the six a-melding reasons; required with `end_date` |
| `holiday_scheme_id` → holiday_schemes | | null = default; **added by delivery B** with its key (pre-flight) |
| `contract_signed_at` | date | typed, or set from a contract document's `signedAt` |

All class C. Indexes `(person_id, start_date)`, `(end_date)` partial on not null.

### 3.3 `staff.employment_terms`

| Column | Type | Notes | Class |
|---|---|---|---|
| `id`, `employment_id` → employments | | | C |
| `valid_from` | date not null | unique with `employment_id`; the first row equals `start_date` | C |
| `employment_type` | varchar(12) not null | `fast`, `midlertidig` (required) | C |
| `temporary_ground` | varchar(30) | § 14-9 (2) `a`…`e`, `tariff`; required when midlertidig | C |
| `expected_end_date` | date | temporary | C |
| `appointment_form` | varchar(40) not null | `fast`, `midlertidig`, `fast_utleid`, `midlertidig_utleid`, `midlertidig_tilkallingsvikar` | C |
| `hired_out_to` | varchar(200) | the innleier, § 14-6 o | C |
| `fte_percent` | numeric(6,2) not null | 0 < x ≤ 100 (required) | C |
| `working_hours_scheme` | varchar(40) not null default `not_shift` | the six values | C |
| `full_time_hours_per_week` | numeric(5,2) not null | defaults by scheme: 37.5; 36.5 shift; 33.6 offshore and continuous; 35.5 round-the-clock | C |
| `occupation_code` | char(7) | STYRK-08 | C |
| `title`, `employee_type` | varchar(120), varchar(20) | employee type for providers: `employee`, `contractor`, `consultant`, `intern` | C |
| `remuneration_type` | varchar(20) | `monthly`, `hourly`, `commission`, `fee`, `piecework`; optional | A |
| `pay_components` | jsonb | `[{name, amount, currency, period}]` (R §10.7) | A |
| `pay_method`, `payday_text` | varchar(40), varchar(100) | § 14-6 i | A |
| `department_id`, `location_id`, `manager_person_id`, `cost_centre`, `division` | | manager null = department's; cycles refused | C |
| `working_time_text`, `break_minutes`, `special_hours_text`, `overtime_rule_text` | | § 14-6 j, k, l, m | C |
| `probation_end_date`, `probation_extension_agreed`, `probation_extended_to`, `probation_extension_reason` | date, boolean, date, varchar(200) | § 15-6 (3)–(4) | C |
| `notice_employee`, `notice_employer`, `notice_unit` | smallint, smallint, varchar(6) | `days`, `weeks`, `months` | C |
| `tariff_reference`, `holiday_terms_text`, `competence_development_text`, `social_security_text` | varchar | § 14-6 n, g, p, q | C |
| `seniority_date`, `pay_grade` | date, varchar(20) | optional | A |
| `note` | varchar(1000) | | D |

Indexes `(employment_id, valid_from)`, `(manager_person_id)`, `(department_id)`.

### 3.4 Structure

`staff.legal_entities` (`name`, `org_number` char(9) MOD11 unique, `is_default`,
`archived_at`); `staff.business_units` (`legal_entity_id`, `name`, `org_number`
unique, `is_default`, `archived_at`); `staff.departments` (`code` varchar(20) unique,
`name`, `manager_person_id`, `cost_centre`, `division`, `parent_id`, `archived_at`);
`staff.locations` (`name`, address columns, `country`, `archived_at`). Archived rows
stay referenced and leave the pickers.

### 3.5 Absence

`staff.absence_types` — `key` varchar(40) unique, `name_en`, `name_nb`, `kind`
(`holiday`, `sick_self`, `sick_certified`, `child_sick`, `leave`, `time_off`, `other`),
`unit` (`days`, `hours`), `paid`, `draws_holiday`, `allows_percent`, `needs_decision`,
`visibility` (`absent`, `hr_only`), `amelding_leave` (the six descriptions,
`permittering`, or null), `seeded`, `hidden`, `sort`.

`staff.holiday_schemes` — `name`, `entitlement_virkedager` smallint,
`extra_from_60_virkedager` smallint, `main_period_virkedager` smallint default 18,
`after_september_virkedager` smallint default 6, `max_carry_over_virkedager` smallint,
`is_default`, `seeded`.

`staff.holiday_adjustments` — `person_id`, `year` smallint, `days` numeric(5,1)
(signed, working days), `reason` (`agreed_carry_over`, `forced_carry_over`,
`correction`), `note` (D), `by_user_id`; class C; listed and deleted by HR.

`staff.absences` — `person_id`, `type_id`, `first_day`, `last_day`, `hours`
numeric(4,2) (hour types; `first_day = last_day`), `percent` numeric(5,2) default 100,
`status` (`requested`, `approved`, `rejected`, `cancelled`, `registered`), `note`
varchar(500) (null and refused on sick kinds; class D), `case_id` → sick_cases,
`degree_percent` (sick kinds; class E), `requested_by_user_id`, `decided_by_user_id`,
`decided_at`, `decision_note` (D), `self_approved` boolean. Sick-kind rows (type,
dates, degree) and child-sick rows are class **E** whole, not C: that a person was sick
on a date is health data (R §4.9), so the erase replaces the type with `absent` and
keeps the days as C. Indexes `(person_id,
first_day)`, `(type_id)`, `(case_id)`, `(status)` partial on requested. Overlap of two
approved or registered absences for one person on one day is refused unless both
allow percent and the percentages sum to at most 100 — except a 100 % sykmelding over
approved ferie, which splits the ferie (S9), and a graded one over ferie, which is
allowed beside it.

`staff.sick_cases` — `person_id`, `first_day`, `closed_at`, `accommodation_note`
varchar(2000) (manager read-write), `max_benefit_estimate` date (editable). The case
and its milestones are class **E** whole; after the E erase the case keeps only its
id and that it existed between two dates.
`staff.sick_case_milestones` — `case_id`, `kind` (`employer_period_end`,
`follow_up_plan`, `dialogue_1`, `nav_activity`, `dialogue_2`), `due_date` (stored,
recomputed when the case's rows change), `done_at`, `done_by_user_id`, `waived`,
`waived_reason`, `waived_by_user_id`, `note` (E).

### 3.6 Documents, links, events, settings

`staff.documents` — `person_id`, `category_id` → `document_categories` (`key`, names,
`retention_class`, `restricted` ⇔ class E, `seeded`, `archived_at`, `sort`),
`object_key`, `file_name`, `content_type`, `size_bytes`, `valid_to`, `signed_at`,
`uploaded_by_user_id`, `revision` (no `deleted_at`: rows are deleted — A2
pre-flight). Keys `people/<personId>/<uuid>`. `staff.object_deletions` — `object_key`,
`attempts`, `next_attempt_at`, `claimed_until`, `last_error`, `created_at`. The
`retention` block of `GET /settings` (pending and failing deletions, the oldest
pending, the last erase) is derived from these and the events; no settings column
is added. Document events carry `{documentId, fields: {category, fileName, validTo,
signedAt}}` under a `documents` group (Self, manage, personal-data) or a `health`
group (Self, personal-data) chosen by the category, with their classes passed per
event from the category; a health document's events are left out of the listing for
an audience without the health group.

`staff.identity_links` — `person_id`, `provider` varchar(20) (`identity` now; the
Entra module adds its value), `external_id` varchar(200), `state` (`linked`,
`pending`, `active`, `disabled`, `failed`, `blocked`), `managed` boolean (what identity
last reported), `blocked_reason`, `last_error`, `attempts`, `next_attempt_at`,
`lease_id` uuid, `lease_until`, `last_synced_at`, `last_spec_hash` (for `Update`);
unique `(person_id, provider)`, unique `(provider, external_id)`, index
`(next_attempt_at)`. **(pre-flight)** The table lands in A1: a **self-service link**
(`state = linked`, written by HR through `PUT /people/{id}/user { userId | null }`
after reading the user from `Deps.Users` outside the transaction; an inactive user
is refused `user_inactive`, a user linked to another person
`user_linked_elsewhere`) establishes the Self and Manager audiences and nothing
else. C's worker acts only on a link whose user identity reports **managed**; an
unmanaged link is left alone and shown as "linked, not managed by Staff".

`staff.events` — `person_id` nullable, `kind`, `actor_kind`, `actor_user_id`,
`actor_display`, `at`, `payload` jsonb, `payload_classes` text[] (the retention
classes the payload touches, **for the erase only** — pre-flight). Index
`(person_id, at)`. Append-only.

`staff.settings` — one row: `time_zone` (Europe/Oslo, validated as Expenses does,
`mig/00013_expenses_claims.sql:88-107`), `employee_number_prefix` varchar(8),
`next_employee_number` bigint, `self_cert_policy` (`minimum`, `extended`, `custom`),
`self_cert_days_per_occurrence`, `self_cert_occurrences_per_year`,
`self_cert_days_per_year`, `self_cert_after_months` default 2, `auto_carry_over`
boolean, `create_before_days` default 14, `delete_external_after_days` null,
`retention_grace_days` default 90, `health_retention_days` default 365,
`pension_provider`, `injury_insurer`,
`thresholds_acknowledged` jsonb (`{"5": at, "30": at, "50": at, "otp": at}`).

## 4. Rules

**Days and the zone.** A date in this module is a calendar day in the installation's
business time zone; "today" for the workers is that day, and a worker that straddles
a daylight-saving change still acts once per civil day. Working days are
Monday–Friday excluding public holidays from `internal/calendar`; egenmelding and the
arbeidsgiverperiode count calendar days; a percentage absence counts its share of
each day; an hour absence counts no day.

**The holiday account** for person P, year Y, in working days: `entitlement` =
convert(scheme virkedager) — or convert(after-September virkedager) when P's earliest
active employment starts after 30 September of Y — plus convert(extra from 60) when P
turns 60 or older in Y; plus carry-in: agreed adjustments capped at
convert(max carry-over), forced and sick-during-holiday adjustments uncapped;
`taken` = approved days of `draws_holiday` types in Y on working days; `pending` =
requested such days; `remaining` = entitlement − taken. `convert(v) = ⌊v/6⌋ × 5 +
min(v mod 6, 5)`. Requesting beyond `remaining` is allowed with a warning (the
account goes negative and shows it). January's attention item lists last year's
remaining days per person; one action records the carry-over.

**Egenmelding.** One request is one occurrence: allowed when the person has been
employed `self_cert_after_months` on the first day; its calendar days are at most
`self_cert_days_per_occurrence`; under `minimum` the rolling 365 days hold fewer than
`self_cert_occurrences_per_year` earlier occurrences, under `extended` fewer than
`self_cert_days_per_year` earlier self-certified days (custom: both numbers as set);
no day lies past the case's sixteenth summed absence day; and the person's right is
not withdrawn. Otherwise `self_certification_exhausted`, `self_certification_too_early`
or `self_certification_withdrawn`, each with the reason, and the person is told to
register a sykmelding.

**The case.** A sick absence with fewer than sixteen calendar days between its first
day and the last day of an open or recently closed case joins that case; otherwise it opens
one. The arbeidsgiverperiode ends on the day the case's rows sum to sixteen absence
days (a 50 % day counts as one day); the other milestones are dated from
`first_day`; the 52-week estimate is `first_day + 364 days` until edited. A case
closes when its last absence ended and no new one joined for sixteen days, or by HR.
A closed case keeps its dates; its notes fall under class E.

**Terms.** A new row needs `valid_from` within the employment, not already used, and
the first row dated the start. `probation_end_date ≤ start + 6 months` and ≤ half the
expected term when temporary; `probation_extended_to` needs `probation_extension_agreed`
and a reason. `temporary_ground` required when midlertidig; `appointment_form` must
agree with `employment_type`. Ending an employment needs `end_reason`; the end may be
in the future. A row dated after the end is refused. HR's correction of a past row is
an event with before and after.

**Managers.** A person's manager on a date is the terms' `manager_person_id`, else
the department's manager **unless that is the person, in which case the parent
departments are climbed to the first whose manager is someone else, else none**
(pre-flight); self and cycles are refused (`self_manager`, `manager_cycle` — judged at
the row's `valid_from` and at every later change date, under a transaction advisory
lock on the manager graph taken after the person lock, since two people's rows are
never locked together; `department_cycle`); the org view walks with a visited set. The manager's *user* is the linked identity
user of that person, if any.

**Sealed fields.** Written only through `PUT /people/{id}/national-id` and
`/bank-account` (`staff:personal-data`), validated, sealed, MAC'd; read only through
the reveal endpoints (`staff:personal-data`) and the person's own export; never in
lists, CSV, logs, problem details or event payloads. The duplicate refusal names no
person.

**Accounts.** Per person with a link or the flag, per cycle, with `T` today, `F`
the effective `provisionFrom`, `S` the next employment start, `E` the last end, `L`
the link:

| State | Local mode | Federated mode |
|---|---|---|
| erased | nothing (a hold stops the erase only; every row below applies under a hold) | same |
| flag off, no L | nothing | nothing |
| flag off, L active | nothing (HR disables explicitly) | nothing |
| flag on, no employment at all | attention `no_employment` | same |
| flag on, `T < F − createBeforeDays` | nothing | nothing |
| flag on, `F − createBeforeDays ≤ T < F`, no L | `Prepare` → L pending | `Prepare` (find) → L pending or `not_found` attention |
| flag on, `T ≥ F`, `T ≤ E` or no E | `Prepare` if no L, then `Activate` (roles; invitation when no credential and none active) → L active; `blocked_by_owner` → L blocked | `Prepare` if no L; never `Activate`; L active when identity says active |
| flag on, `T > E`, no later employment | `Deactivate` → L disabled | `Deactivate` → L disabled |
| flag on, `T > E`, later employment with start `S` | as the row for `F = S` | same |
| L active, spec changed (`last_spec_hash`) | `Update` | `Update` of language and employee number only (the IdP owns name and email, which SCIM rewrites, `srv/identity/scim_users.go:282-291`) |
| rehire: L disabled, flag on, `T ≥ F` | `Activate` | re-enable only when `disabled_by = staff`, no invitation; otherwise attention `disabled_by_owner` |

An early `provisionFrom` (before the start) is honoured: the rows read `F`, not `S`.
`work_email` (or the private email when chosen) must be set, else attention
`account_email_missing`. An identity answer of "unknown user" on an active
link marks it `failed: user_gone` for attention; the worker never re-creates on its
own (reading 18).

**Erase.** The D columns → null, the D rows (next of kin, D documents' rows marked,
their objects queued) deleted, sealed values and MACs removed, notes blanked, events'
D payloads replaced by the field names; the E content per closed case on its own
schedule (sick rows' types to `absent`, cases and milestones to their dates, notes
blanked, the withdrawal after it lapses, sickness-related event payloads to the kind
alone); A columns after five years past the end year. Names, birth date, employee
number, employments, non-pay terms, absence dates and types stay (C). The run reports
per kind.

**Locking.** Every mutation of a person's rows takes the person row `FOR NO KEY UPDATE`
first, then employments, absences, documents, in that order (**A1 Task 2 review**: not
`FOR UPDATE`, which would block the `KEY SHARE` a foreign-key check on another row's
`manager_person_id` takes, so such a write — holding its own person and the
manager-graph lock — could deadlock against this one waiting on the graph lock); a
person `DELETE` takes the manager-graph lock before its named-as-nobody's-manager
check and maps a 23503 to `person_in_use`; the workers take one
person per transaction and claim links by row. No call to a contract, the mail or the
object store happens inside any transaction or read-only snapshot
(boundaries.md's rule as widened at `6941e58d`).

## 5. Permissions and visibility

| Key | Grants | Sensitive |
|---|---|---|
| `staff:access` | Use the Staff app: own record, own absence, own documents, own export (S17). **Required by every operation.** | no |
| `staff:view` | The directory: everyone's work fields (§5 matrix), employments and terms without pay, structure, the calendar with types hidden. | no |
| `staff:approve` | Decide absence for anyone, see types (not `hr_only`) and the open cases' dates for anyone, mark milestones. | no |
| `staff:manage` | HR: create and edit people (not the sealed fields), employments and terms (pay included), structure, absence types and schemes, settings, documents other than health, the account flag and dates, run the workers, import and export (not the sealed columns), payroll export, correct past absence, acknowledge thresholds. | yes |
| `staff:personal-data` | Reveal and write national id and bank account, health documents and case milestones' notes, `hr_only` absence types, the personal-data export for anyone, the legal hold, erasures and re-assessments, import files carrying sealed columns. | yes |

**Being a manager** (by the terms, S12) grants without a permission: the reports'
work fields, their absence with types other than `hr_only`, their open cases'
milestones and accommodation notes (read and write), and the decision on their
requests.

**Field group × audience** (✓ read; ✎ read and write; — absent from the response):

| Group | Self | Colleague (`staff:view`) | Manager | `staff:approve` | `staff:manage` | `staff:personal-data` |
|---|---|---|---|---|---|---|
| Work: name, employee number, title, department, unit, location, manager, work email and phone, language (**closing wave**: personal-data ✓, not ✎ — it writes only the sealed fields, as its grant says) | ✓ | ✓ | ✓ | ✓ | ✎ | ✓ |
| Employment and non-pay terms (**closing wave**: personal-data ✓) | ✓ | ✓ (dates, type, fte) | ✓ | ✓ | ✎ | ✓ |
| Pay terms, seniority, grade (**closing wave**: personal-data ✓) | ✓ | — | — | — | ✎ | ✓ |
| Private: mobile, private phone and email, home address, next of kin, birth date, gender (**closing wave**: personal-data ✓) | ✎ (birth date, gender ✓) | — | — | — | ✎ | ✓ |
| Care-day fields, self-cert withdrawal (**closing wave**: personal-data ✓) | ✓ | — | — | — | ✎ | ✓ |
| National id | kind, on file | — | — | — | kind, on file | ✎ reveal |
| Bank account | last four | — | — | — | last four | ✎ reveal |
| Absence: dates, status | ✓ | "absent" | ✓ | ✓ | ✎ | ✎ |
| Absence type | ✓ | — | ✓ except `hr_only` → "leave" | ✓ except `hr_only` | ✓ except `hr_only` | ✓ |
| Absence note (non-sick), degree (sick) | ✓ | — | ✓ | ✓ | ✎ | ✎ |
| Case milestones | ✓ | — | ✎ | ✎ | ✎ | ✎ |
| Accommodation note | ✓ | — | ✎ | — | ✎ | ✎ |
| Milestone notes, health documents | ✓ | — | — | — | — | ✎ |
| Other documents | ✓ | — | — | — | ✎ | ✎ |
| Account flag, links | ✓ | — | — | — | ✎ | ✎ |
| Events | ✓ shaped | — | ✓ shaped | — | ✓ shaped | ✓ |

Capabilities on `GET /meta` and per person (`canEdit`, `canDecide`,
`canRevealIdentifiers`, `canManageAccount`, `canSeeHealth`, `canSeePay`) drive the UI;
the access layer answers the uniform 403. Default: `Owner` gets all five; the
employee role (S26) is the Owner's to create and is the one that carries
`staff:access`.

## 6. API (`/api/v1/staff`, contract `openapi/staff.yaml`)

| Area | Operations |
|---|---|
| Meta | `GET /meta` — capabilities, structure, absence types visible to the caller, schemes, the zone, `accountMode`, whether identity's provisioning roles are set |
| People | `GET /people` (q, unitId, departmentId, locationId, status, paged) · `POST /people` · `GET|PUT /people/{id}` · `DELETE /people/{id}` (created in error: no employment, no absence, no link, named as nobody's manager) · `GET|PUT /people/{id}/user` (the self-service link, pre-flight) · `POST /people/similar-erased` (the rehire warning, A2 pre-flight) · `GET /assignable-users?q=` (the picker over `Deps.Users`) · `PUT /people/{id}/national-id` · `POST /people/{id}/national-id/reveal` · `PUT /people/{id}/bank-account` · `POST /people/{id}/bank-account/reveal` · `GET|PUT /people/{id}/next-of-kin` · `GET|PUT /people/{id}/roles` · `GET /people/{id}/events` · `GET /people/{id}/personal-data` · `PUT /people/{id}/legal-hold` · `POST /people/{id}/reassessed` · `GET /people/{id}/org` |
| Employments | `GET|POST /people/{id}/employments` · `GET|PUT /employments/{id}` · `POST /employments/{id}/end` · `/reopen` · `GET|POST /employments/{id}/terms` · `PUT /terms/{id}` (latest, or a correction with `correction: true`) · `DELETE /terms/{id}` (latest) · `GET /employments/{id}/contract-check` |
| Structure | `GET|POST`, `PUT /{id}`, `POST /{id}/archive` for `/legal-entities`, `/business-units`, `/departments`, `/locations` |
| Documents | `GET /people/{id}/documents` · `POST /people/{id}/documents` · `GET /documents/{id}` · `PUT /documents/{id}` · `DELETE /documents/{id}` · `GET|POST /document-categories` · `PUT /document-categories/{id}` |
| Absence | `GET /absences` · `POST /absences` · `GET|PUT|DELETE /absences/{id}` · `POST /absences/{id}/approve` · `/reject` · `/cancel` · `/correct` (HR) · `GET /approvals` · `GET /calendar?from&to&departmentId` · `GET /people/{id}/holiday-account?year` · `GET|POST /people/{id}/holiday-adjustments` · `DELETE /holiday-adjustments/{id}` · `POST /people/{id}/carry-over` · `GET /people/{id}/self-certification` · `PUT /people/{id}/self-certification/withdrawal` · `GET /people/{id}/care-days?year` |
| Cases | `GET /cases` · `GET|PUT /cases/{id}` · `POST /cases/{id}/milestones/{kind}/done` · `/waive` · `PUT /cases/{id}/milestones/{kind}/note` · `POST /cases/{id}/close` |
| Settings | `GET|PUT /settings` · `GET|POST /absence-types` · `PUT /absence-types/{id}` · `GET|POST /holiday-schemes` · `PUT /holiday-schemes/{id}` · `POST /thresholds/{key}/acknowledge` |
| Accounts | `GET /people/{id}/account` · `PUT /people/{id}/account` (flag, from, email kind) · `POST /people/{id}/account/run` · `/disable` · `POST /accounts/run` |
| Data | `POST /people/import` · `GET /people/export.csv` · `GET /people/import-template.csv` · `GET /payroll/export?month&format` · `POST /retention/run` |
| Me | `GET /me` · `PUT /me` · `GET /me/events` · `GET /me/absences` · `POST /me/absences` · `GET /me/holiday-account` · `GET /me/self-certification` · `GET /me/care-days` · `GET /me/documents` · `GET /me/export` |
| Dashboard | `GET /stats` · `GET /stats/attention` |

Refusal codes: `national_id_invalid`, `national_id_duplicate`, `bank_account_invalid`,
`org_number_invalid`, `employee_number_taken`, `employee_number_too_long`,
`terms_required_fields`, `temporary_ground_required`, `appointment_form_mismatch`,
`probation_too_long`, `terms_before_start`, `terms_after_end`,
`terms_valid_from_taken`, `terms_first_row_start`, `end_reason_required`,
`self_manager`, `manager_cycle`, `department_cycle`, `absence_overlap`,
`absence_note_on_sick`, `self_certification_exhausted`,
`self_certification_too_early`, `self_certification_withdrawn`, `decision_not_yours`,
`own_request`, `holiday_scheme_in_use`, `seed_immutable`, `document_type_unsupported`,
`document_too_large`, `document_count_exceeded`, `category_restricted`,
`person_erased`, `person_in_use`, `legal_hold`, `reassessment_not_due`,
`import_sealed_columns`, `import_too_large`, `import_too_many_rows`,
`import_not_semicolon`, `export_too_many`, `payroll_month_invalid`,
`payroll_format_unsupported`, `storage_unavailable`, `threshold_unknown`,
`retention_busy`, `work_email_taken`, `employment_number_taken`,
`full_time_hours_mismatch`, `user_inactive`, `user_linked_elsewhere`, `no_person`,
`structure_default`, `national_id_birth_date_mismatch`, `account_email_missing`,
`account_email_taken`, `account_not_managed`, `account_privileged`,
`blocked_by_owner`, `disabled_by_owner`, `role_not_assignable`,
`accounts_unavailable`, `employee_role_missing`, `terms_not_latest`,
`absence_not_requested`, `absence_not_cancellable`, `percent_not_allowed`,
`hours_on_day_type`, `structure_archived`, `category_in_use`.
`DELETE /absences/{id}` removes a row that was never decided (a draft the person
abandons, or HR's cleanup); `/cancel` withdraws a requested or approved one and keeps
it as history. `POST /retention/run` and `POST /accounts/run` are `staff:manage`.

## 7. Frontend

A new app "Staff" / "Ansatte" (`apps/staff/frontend`, `@vantigo/staff-ui`).

- **My page** (`staff:access`): my profile with the editable fields, my employments
  and the terms in force (pay components shown), my holiday account with pending and
  remaining and the request button, my care days, my self-certification standing, my
  absences, my documents, "download my data" (without the sealed numbers). Mobile first: the request form is a
  sheet with the type, the days (a range picker, or one day and hours for an hour
  type), the percentage when allowed, a note (hidden for sick types); egenmelding
  shows what the policy allows today before the person submits.
- **People** (`staff:view`): the list with search and the structure filters, active
  by default; a row shows name, employee number, title, department, manager, status
  (active, starts on, ended on, on leave today). *New person* (`staff:manage`) is a
  two-step form: the person, then the first employment with its first terms, the
  required fields marked and the presumption warning explained; an erased person
  with the same name and birth date is offered as a rehire.
- **Person page**: tabs **Profile** (fields by group per the matrix; the sealed fields
  as "on file" with a reveal button for those who may, each reveal confirmed),
  **Employments** (one card per employment with its terms as a timeline; *New terms*
  copies the current row; *Correct* on a past row; *End employment* asks the reason;
  the contract check as the seventeen items with ticks), **Absence** (the holiday
  account, carry-over, the list, the open case with its milestones, tick boxes and
  the accommodation note), **Documents**, **Account** (the mode, the flag, the dates
  with the derived default explained, the address choice, the links' states and
  reasons, *Run now*, *Disable now*; for an unlinked existing user, the Owner's
  adoption step explained with a link to identity's users page), **History** (events).
- **My team** (`staff:access`, shown when the caller manages anyone): the reports'
  work fields, their absence with the types the matrix allows, their open cases, and
  the **Approvals** inbox for what the caller may decide — a team lead on the Employee
  role needs no `staff:view` for their own reports.
- **Absence** (`staff:view`, decisions by right): the calendar — a month per
  department with people as rows and absences as bars, types only where the caller
  may see them — and the **Approvals** inbox listing what the caller may decide,
  oldest first, approve or reject with a reason inline.
- **Settings** (`staff:manage`): structure, absence types (seeds with their a-melding
  mapping and visibility shown read-only, own types editable), holiday schemes with
  the default and the conversion shown ("25 virkedager = 21 days on a five-day
  week"), the self-certification policy as presets or custom numbers, the account
  settings and the employee roles (read from identity, with a link to change them), retention periods, the zone, pension
  provider and insurer, thresholds, import and export, the payroll export for a
  month, *Run retention now*, *Run accounts now*.
- **Host**: the tile and routes; dashboard items from `/stats/attention`; the
  spotlight actions; the permission labels in both languages; identity's users page
  gains the `pending` state, "managed by Staff", and the adoption action.

## 8. Platform, identity and documentation changes

1. **`internal/calendar`** (S13), delivery B.
2. **`secrets.Box.MAC(purpose, data) ([]byte, error)`** (S4; the error is `keyFor`'s —
   pre-flight), delivery A1, HMAC-SHA256 under the HKDF key for the purpose, tests,
   the operator note on rotation.
3. **Identity** (S24–S26), delivery C: migration — `managed_by`, `employee_number`,
   `pending_since`, `disabled_by` on `identity.users`; `user_id` on
   `identity.invitations`; the provider in `srv/identity/accounts.go`; the accept
   branch that completes a named user; `Resend` carrying `user_id`; the Owner's disable
   and enable writing `disabled_by`; delete refusing managed users; the adoption
   endpoints under `OwnerManagement`; `identity.yaml` (the pending state, managed
   flag, adoption) and the host's `api/users.ts` and users page.
4. **Slots** (S25): `Accounts` on `module.Module` and `Deps`, resolved by `Compose` and
   by `module.Workers`; worker-mode Deps in `cmd/vantigo/main.go` gain what identity's
   provider needs; a composition test builds `staff-accounts` from `module.Workers`.
5. **Config**: `STAFF_ACCOUNTS_ENABLED`, `STAFF_RETENTION_ENABLED` (default on),
   `STAFF_WORKERS_POLL` (1 h), `STAFF_IMPORT_MAX_ROWS` (5000). `MODULES` default gains
   `staff`. depguard rule sets for `staff` and `internal/calendar`; `moduleSchemas`; the
   OpenAPI module list. Turning `staff` off strands pending users as pending; the
   operator page says so.
6. **Storage scope** `staff`.
7. **Documentation**, per delivery, en and nb where the rule asks:
   `en/reference/staff.md` (sources: `apps/server/internal/staff`,
   `apps/staff/frontend`, `openapi/staff.yaml`); `user/staff.md` en+nb;
   `admin/staff.md` en+nb (settings, the employee role step, the workers, retention,
   `STAFF_*`); `admin/installation.md` en+nb (`MODULES`, the schema list);
   `admin/object-storage.md` en+nb (the scope); `admin/authentication.md` and
   `admin/sso-scim.md` en+nb (managed users, the authority mode, adoption);
   `user/workspace-administration.md` en+nb (pending users, managed by Staff, the
   employee role); `user/getting-started.md` en+nb (accepting a Staff invitation);
   the section overviews `user/index.md`, `admin/index.md`, `reference/index.md`; a
   new `contributing/platform-packages.md` (English only) naming `internal/secrets`
   in its `sources` from A1 and `internal/calendar` from B, with the MAC and the
   rotation rule (the identifier checks live in `staff/ident`, a module package,
   ported from customers' and invoices' validators — pre-flight);
   `admin/installation.md` en+nb carrying the `APP_SECRET` rotation note for the
   national-id index; `reference/invoices.md` for
   the calendar import path (a `Docs-Impact` trailer where nothing documented changes);
   module-boundaries.md (module list, schema list, `MODULES` default, the `Accounts`
   slot and its resolution in both places, the new write direction, the platform
   packages); ROADMAP.md (a Staff section: this phase, then Entra, phase 2, Staffing).

## 9. Edge cases

- Two concurrent employments: one holiday account (the scheme of the earlier
  employment), one set of counters, one flag, one link.
- An employment ended with a future end date keeps the account until the day after;
  an end date in the past disables at the next cycle or with *Run now*; today's end
  disables tomorrow; *Disable now* disables at once.
- A rehire of an erased person: the warning at creation, the employee number reused,
  `Prepare` finds the managed user by employee number and the worker activates it
  (local) or re-enables it when Staff's own earlier run disabled it (federated).
- The Owner deletes a managed user: refused. The Owner revokes a pending invitation:
  no re-issue; attention `invitation_revoked` until HR runs the person again.
- A manager who leaves: their reports route to the department's manager, else to
  `staff:approve` and HR; attention lists "reports without a manager".
- Egenmelding registered for a day already covered by a sykmelding: refused as
  overlap; a sykmelding covering an egenmelding converts the earlier days' type and
  keeps the case.
- Holiday crossing a year boundary is two accounts' worth, split at New Year.
- A 100 % sykmelding over ferie: the ferie row is split, the days return through the
  account's own arithmetic, one event on the person.
- Public holidays in approved ferie are not deducted; inneklemte dager are a company
  `time_off` type.
- The payroll export for a month with an employment's end reports the end and its
  reason in that month only; a leave is reported in every month it touches.
- Importing a row whose employee number exists updates the person; sealed columns
  need the permission and a non-empty value to update.
- Federated mode with a person who has no SSO account yet: the link stays `not_found`
  on attention until the Owner adopts or the Entra module creates; the person works
  without an account meanwhile.

## 10. Testing

Backend: shaping per caller against the §5 matrix, field by field, including
events' payloads and the national-id "on file only" pin; the terms chain (in-force
lookup, derived dates with reordered components, first-row rule, corrections);
national id validation incl. d-nummer and the synthetic ranges, duplicates by MAC
without a named person, reveals as events; MAC domain separation (a foreign id never
collides with a fødselsnummer); bank account MOD11 and IBAN, the mail on change; the
holiday account: conversion table, a Monday–Friday week costing five of 21,
proration, 60+, carry-over caps, the January item, sickness during ferie splitting a
row; egenmelding occurrences and days under the three policies, two short occurrences
ten days apart allowed, the withdrawal; the sixteen-summed-days arbeidsgiverperiode
on an interrupted case, milestones, the estimate; care days from the three fields;
decision routing incl. own request, the sole Owner's self-approval, a manager
without an account, cycles; overlap and percentages; hour absences; notifications
through the mail fake; documents (types, size, visibility, the object store fake, the
deletion queue retrying a failing store); the accounts worker per row of the §4 table
in both modes against the real identity in-process and an identity fake: prepare,
activate with roles and the invitation, expiry and re-issue up to three, accept
completing a pending user, deactivate ending sessions and revoking invitations,
Owner refusals, `blocked_by_owner`, `user_gone`, update on a changed spec, the actor
in identity's audit; a pending user meeting OIDC just-in-time sign-in and SCIM
create in federated mode (link-only, no `oidc_account_locked`); worker-mode
composition of `staff-accounts` through `module.Workers`; pool-of-one runs of both
workers; zone and daylight-saving boundaries for "today"; the retention run per
class incl. E for current staff, hold, re-assessment, idempotence; the payroll CSV
golden file incl. a leave over fourteen days across a month boundary; import dry
run, sealed-column refusal; stats and attention per caller incl. thresholds;
contract coverage for `AccountProvisioning`; the harness-wide check that no
contract, mail or storage call happens inside a transaction or snapshot; the
cross-schema scan; config: `staff` alone; an end-to-end flow: flag → invitation →
accept → My page under the employee role.

Frontend: My page incl. the request sheet at phone width and the hour type; People
and the two-step creation with the required-field explanation and the rehire offer;
the person tabs incl. reveal flow, terms timeline and correction, contract check,
case milestones and the accommodation note, the account tab in both modes; the
calendar's shaping; approvals; settings incl. seeds read-only, the conversion hint,
the employee role picker; catalog parity; dashboard items; identity's users page
additions.

## 11. Readings on the record

For the owner's verdict; each is how the design read a decision or a gap.

1. **Terms are one row per change, not one table per concern**; the a-melding's
   derived dates come out of the one chain.
2. **`employmentType` and `ftePercent` are refused when missing**, not defaulted
   (R §4.1).
3. **`createBeforeDays` defaults to 14** even before the Entra module exists; a
   disabled local account has little use ahead of the start, but a consistent default
   across both modes is simpler to document than one that changes when Entra lands.
4. **Holiday is deducted on a five-day week** in this phase; the entitlement is
   converted from virkedager as payroll does (25 → 21); a part-time pattern is a
   phase 2 column behind the one deduction function.
5. **The 60+ extra days are not prorated** for a start after 30 September; the
   after-September days replace the base entitlement only — **UNCERTAIN** in law
   (critic open question 1), set as the simpler reading HR can correct by adjustment.
6. **Who decides**: direct manager, the department's manager as fallback,
   `staff:approve` and HR; nobody their own except when the chain ends.
7. **A manager without an account decides nothing** until the account exists.
8. **Bank account and national id are written only under `staff:personal-data`**,
   not by HR at large and never by the employee.
9. **The payroll CSV omits the sealed identifiers.**
10. **Contract and attest documents are class A (five years after the end year)**,
    with pay terms, because claims over pay and dismissal outlive the 90-day grace;
    whether the arbeidsavtale is bookkeeping documentation in law is **UNCERTAIN**
    (critic open question 2); the five-year reading is the conservative one.
11. **The first terms row is dated the start, and HR may correct a past row** as an
    event, so history has no gap and a typo is not a deletion.
12. **The identity link is provider-keyed from the start**; the Entra module adds a
    value, no migration.
13. **The employee role is a custom role the Owner names** (S26), since built-in
    roles cannot carry a module's permission; the alternative — identity letting a
    module declare default grants — is an identity design of its own, not taken.
14. **`provisionFrom` null derives from the earliest active employment.**
15. **Disable the day after the end date.**
16. **The business time zone is this module's own setting**, like Expenses'; a
    platform zone is a later consolidation.
17. **`StaffDirectory` is deferred** to its first consumer (the critic's reading);
    the research's wish for an early contract is noted, not taken.
18. **The worker never re-creates an account on its own** after identity reports a
    linked user gone; HR decides after the attention item.
19. **In federated mode the local path is link-only and never activates**; the
    provider is the authority for existence and status, Staff's `Deactivate` at the
    end is the one exception, as a leaver must lose access whatever the provider
    does.
20. **Owners are never managed by Staff** (`account_privileged`); an Owner who is
    also an employee has a person record with the flag off and, since the
    pre-flight, a self-service link so My page works for them.
21. **An Owner's disable wins over the worker**, for ever, until an Owner enables.
22. **One migration per delivery**, each number assigned when its delivery's first
    task lands (pre-flight), since other branches take numbers meanwhile.
23. **No "propose a change" workflow**; the employee edits the private fields
    directly and asks HR for the rest.
24. **The national-id validator accepts the synthetic test ranges** (month + 40,
    month + 80) so test data from Skatteetaten's tools is usable; the 2032 format
    change is a later validator update (critic open question 4).
25. **`remuneration_type` is optional** and not exported as mandatory; it is not in
    Skatteetaten's current mandatory list (R §4.7; critic open question 3).
26. **Special-category absence types are HR-only by seed** (religious holiday,
    pregnancy); the manager sees "leave".
27. **Sick absences carry no free text**; what the manager needs is the case's
    accommodation note, which the manager owns.
28. **The arbeidsgiverperiode is the sixteenth summed absence day**, and the
    52-week end is an estimate HR edits.
29. **A 100 % sykmelding over ferie returns the days** (fl. § 9) by splitting the
    ferie row; a graded one returns nothing.
30. **Moving an installation from local to federated** leaves pending, credential-less
    managed users pending; they would meet `oidc_account_locked` at an SSO sign-in.
    The worker reports every such link as `mode_changed` on attention, and the Entra
    module's adoption into the provider is where they are resolved; this phase does
    not migrate them.
31. **The person's own export omits the sealed values** (national id, bank account):
    they are the person's own, which they know, and a stolen session should not get
    them. This revises S16's earlier reading.
32. **An Owner who re-enables a leaver is re-disabled by the next run**, by design:
    the employment's end is the fact; an Owner who wants the account kept turns the
    person's flag off, after which the worker leaves the account alone.
33. **A change of the account's email mails both the old and the new address**, in
    local mode, so a takeover by password reset is noticed.
34. **A leave's "total length" for the a-melding is the chain** of contiguous rows of
    the same type (an extension row continues the leave), not the single row.
35. **(A2 pre-flight) `/me/export` omits HR's notes about the person**, as the UI
    does; GDPR art. 15 covers such notes, so the HR-issued personal-data export,
    which includes them, is the module's complete answer to an innsyn request. For
    the owner's verdict.

## 12. Open questions

1. The health retention period: 365 days after the case closes, or tied to the refund
   claim's closure, which this module does not see?
2. Should adoption also be offered from the Staff side — HR proposes, the Owner
   confirms in identity — or is the Owner's action in identity enough for a first
   version?
3. Should the authority mode be a per-installation **setting** that the Entra module
   later flips (critic open question 5), rather than derived from identity's
   configuration? Derived needs no setup and cannot disagree with the sign-in path;
   a setting would let a federated installation keep local invitations for people
   outside the IdP. This design derives it.
4. The `auto_carry_over` default: off (HR confirms every January) as designed, or on?
