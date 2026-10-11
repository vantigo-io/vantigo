# Staff module — research

Research for a new **Staff** module: the workforce register that acts as the company's
HR system, holds each employee's competences and preferences, and later becomes the
source of truth that creates the person's user account — in Vantigo's own identity
module or in an external identity provider — so they can sign in. A future
**bemanning** (staffing) feature, where a manager plans projects or shifts and matches
people to them, must be able to build on the same data without a redesign. Five
studies feed this document: the Norwegian and Nordic systems an SMB would compare
Vantigo against (§2); the international HRIS baseline and how HR systems drive
identity providers (§3, §5); what Norwegian law requires an employee register to hold
(§4); how scheduling and resource-planning products model competence, availability
and preferences (§6); and the codebase's seams (§7). Every external source was read on
**2026-10-11** unless another date is given. Statutes and regulations were read on
Lovdata; Arbeidstilsynet, NAV, Skatteetaten and Datatilsynet pages are administrative
practice; vendor help centres, pricing pages and public API specifications are the
product facts; reseller and review sites are secondary and used only for prices.
Anything not confirmed from a primary source is marked **UNCERTAIN**. Code references
are `file:line` against `main` at `0ba2840e`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, and `boundaries.md` the
contributing page `docs/src/content/docs/en/contributing/module-boundaries.md`.

## 1. Why now, and what the research reshapes

### 1.1 What the module is for

Every module Vantigo has refers to people by an opaque identity user id and knows
nothing else about them: Projects holds roles and task assignees, Time holds an
effective-dated bill and cost rate per user, Expenses holds owners, approvers and a
payroll CSV that names an employee only by display name and user id, Customers holds
an account manager (§7.3). Nothing in the system knows an employee number, a
department, a manager, an employment start or end, a job title, or who is on leave.
Identity's user is an account, not a person: email, display name, language, password,
disabled flag, TOTP (`mig/00002_identity_baseline.sql:8-26`). The Staff module is the
missing **person** record — stable across employments and accounts — with everything
an employer must keep about them, and the one place other modules can ask "is this
person employed on this date, what can they do, and what do they prefer".

### 1.2 Nine findings that reshape the assumptions

1. **The accounting-first competitors have no HR; they all point to the same two
   partners.** Tripletex, PowerOffice Go, Fiken, Xledger, Unimicro and 24SevenOffice
   keep only the a-melding employee record and delegate HR to Huma, Simployer, 4human
   or Adminkit (§2.1–2.4, 2.8). The market Vantigo enters is therefore not "an
   accounting system with an employee tab" but Huma and Visma's Dottie HR — pure HR
   SaaS priced per employee per month — plus the a-melding record the payroll system
   needs. Being both in one product is the integration argument the README already makes.
2. **The table stakes are narrow and clear** (§2.9): the a-melding-shaped
   employment with date-effective change rows, department and manager, ferie as a
   balance with request → approval → calendar, egenmelding versus sykmelding,
   permisjon mapped to a-melding codes, a document archive per employee, employee
   self-service, and role-based access to sensitive fields. Everything else is a
   differentiator held by one or two products.
3. **Nobody has preferences.** None of the eleven Nordic HR products, none of the six
   international ones and none of the nine scheduling or resource tools model
   preferred days, locations, customers or project types as first-class employee
   data (§2.10, §6.4). Only Quinyx and Gat weigh working-time wishes inside their
   optimisers. Preferences are the one place Vantigo would be ahead rather than level.
4. **Competence with expiry is the differentiator the trades need, and the law
   gives it a catalogue.** Certificates with expiry and scheduled reminders exist
   only in Huma, Simployer and Dottie (§2.10). Norwegian regulation supplies the
   types, issuers and validity periods — FSE yearly, HMS-kort two years, YSK and
   varme arbeider five years, fagbrev and sertifisert sikkerhetsopplæring without
   expiry (§4.11) — so the catalogue can ship seeded rather than empty, the same way
   Expenses seeded the state's travel rates.
5. **The 2024 contract rules make the employment record large.** Arbeidsmiljøloven
   § 14-6 now lists seventeen mandatory contract items, the contract is due within
   seven days of the start, and an unstated stillingsprosent is presumed to be what
   the employee claims (§4.1). The a-melding adds its own enumerations — ansettelsesform
   in five values including tilkallingsvikar and utleid, six arbeidstidsordning
   values, six end reasons, STYRK-08 yrkeskode (§4.7). Both belong on the employment
   as first-class, versioned columns; payroll reads them, Staff owns them.
6. **HR drives the identity provider, never the other way round.** Every integration
   studied is one-way from the HR system: Personio and HiBob push into Entra and
   Okta, Rippling pushes into Entra through its API-driven inbound provisioning,
   Okta pulls BambooHR and Workday as a "profile source" that wins on status (§5.1,
   §5.6). The correlation key everywhere is the employee number (Entra `employeeId`,
   at most sixteen characters; Google `externalIds[organization]`; SCIM
   `employeeNumber`). Vantigo already receives inbound SCIM from Entra, so the
   design question is not "how to push" but "who is the authority per installation",
   and the answer must be one of the two (§5.5).
7. **Vantigo's SCIM server would break under an HR-sourced Entra tenant.** It stores
   only given and family name from the profile and answers 400 `invalidPath` to a
   PATCH on any enterprise-extension attribute (`srv/identity/scim_input.go:591-625`;
   §7.1). An Entra tenant that maps `employeeNumber`, `department` or `manager` —
   which HR-driven provisioning does by default — would see its pushes refused. The
   enterprise extension is a prerequisite for the staff module's correlation either way.
8. **Scheduling and project staffing agree on one competence model.** Flat skills in
   categories, an optional proficiency level (Runn's 0–4, Planday's seniority), an
   optional expiry that either warns (Quinyx) or blocks (Planday), and a requirement
   attached to a shift, position, role or placeholder (§6.1–6.3). The same
   `competence_requirement` serves Planday's shift skill, Simployer's kompetansekrav
   and Runn's placeholder — and Vantigo's project roles, which are the roadmap's
   Phase 5 placeholders (§6.5).
9. **Retention is per field group, not per record.** Datatilsynet's guidance makes
   the end of employment the ceiling for personnel data, while bokføringsloven keeps
   pay documentation five years and hour records three and a half, and "who was
   employed when" may be kept indefinitely (§4.9). Customers' anonymisation model
   (one date, one erase) does not fit; Staff needs retention classes and an
   end-of-employment erase run that leaves the bookkeeping lineage untouched.

### 1.3 What phase 1 cannot be

Payroll is out of scope and stays out: a-melding, skattekort, feriepenger arithmetic
and inntektsmelding are the payroll system's job (§4.7, §4.8). Staff holds the fields
payroll needs and exports them; it does not compute pay. Shift scheduling, auto-assign
and demand forecasting are out of scope (§6.6); Staff lays the competence, preference
and availability data the roadmap's Projects Phase 5 and a later bemanning feature
consume. E-signing of contracts needs a signing provider (Huma uses Scrive, Dottie
BankID) and is a later delivery.

## 2. What comparable Nordic systems do

Eleven products were read: the accounting systems an SMB already runs (Tripletex,
PowerOffice Go, Fiken, Xledger, Unimicro, 24SevenOffice), the HR SaaS they partner with
(Huma, Simployer, Visma's Dottie HR) and two mid-market HRIS (Sympa, CatalystOne).

### 2.1 Tripletex

No HR module; Huma is sold on the marketplace as "the only HR system with full
integration". The public API v2 is the richest accounting-side model and a good
checklist for the a-melding record: `Employee` (names, employeeNumber, dateOfBirth,
email as login, phones, nationalIdentityNumber, dnumber, internationalId, bank
account or IBAN/BIC, userType STANDARD/EXTENDED/NO_ACCESS, department,
employments[]), `NextOfKin` (name, phone, address, relationship SPOUSE/PARTNER/
PARENT/CHILD/SIBLING), `Employment` (start, end, end reason in six values, division,
lastSalaryChangeDate, isMainEmployer, isRemoveAccessAtEmploymentEnded) and
`EmploymentDetails` as date-effective rows ("Ny rad" per change: employmentType
ORDINARY/MARITIME/FREELANCE, employmentForm in the 2025 a-melding five values,
remunerationType, workingHoursScheme, occupationCode, percentageOfFullTimeEquivalent,
annual/monthly/hourly pay). Absence is hours on timesheet activities flagged Fravær,
with permisjon as a dated row on the employment reported to the a-melding
(`LeaveOfAbsence`). Ferieplan (calendar, approval inbox, balances via
`EmployeeVacationBalance`) is **Komplett-only** at 649 kr/month. Per-employee
document archive, no competence, no onboarding, no provisioning beyond its own login,
which `isRemoveAccessAtEmploymentEnded` revokes at the end date. Department has a
manager; the employee does not. Webhooks exist.

### 2.2 PowerOffice Go

Ansattkort with separate login and employee objects, arbeidsforhold copied or
imported from A02, and a real **Ferie og fravær** module (35 kr per user, requires
Timeregistrering): types Ferie, Syk med egenmelding, Syk med sykemelding, Permisjon,
Avspasering, Helse/tannlege; 25-day balance plus 6 over 60 with no automatic
carry-over; egenmelding rules as presets (minimum 4 × 3 days per 12 months, IA
24 days × 8, or custom), counted on workdays or calendar days; manager approval from
a mobile request into an inbox; absence posts straight into the timesheet. The
sick-leave report "follows no laws automatically": no oppfølgingsplan, no
arbeidsgiverperiode. The API exposes contact fields only — "employee salary details
and employment relationships are not exposed". Company-wide document centre, no
personnel file, no competence.

### 2.3 Visma: Visma.net Payroll, Visma Employee and Dottie HR

Visma Software's SMB HR is **Dottie HR** (vismasoftware.no; a calculator instead of a
price, resellers list 98 kr per employee per month falling to 55 by volume —
**UNCERTAIN**). It is the fullest Norwegian comparison: employee self-update, position
with direct manager and unit roles (HR, lønn, verneombud), calendar absence with
remaining-days calculation, **sykmelding fetched automatically from Altinn** with
NAV-rule checklists, arbeidsavtale templates built on Arbeidstilsynet's post-July-2024
model with merge fields and **BankID e-signing**, qualifications and competence
matrices with expiry independent of the attached document, onboarding checklists,
equipment inventory, org chart, medarbeidersamtaler, lønnsjustering, **automatic
GDPR deletion policies**, and a **Microsoft Entra ID integration with manager and
legal-entity sync, two-way since January 2026** (release notes). No Google Workspace,
no Slack provisioning, no public API found (**UNCERTAIN**).

### 2.4 Fiken, Xledger, Unimicro, 24SevenOffice

Fiken is payroll-only (219 kr base + 69 per user for Lønn og ansatte); its API has no
employee endpoints at all. Xledger has the employee register, absence with NAV and
Altinn automation for refunds, and leaves HR to Simployer, 4human and Huma. Unimicro's
employee under Lønn holds **multiple positions per employee** linked to accounting
dimensions, next of kin, documents, and an absence register with ferie carry-over,
calendar overlap view and GDPR flags on health data. 24SevenOffice announced an HR
module in beta in 2023 (documents, equipment, consents, competence, org diagram); the
current Finago site lists none (**UNCERTAIN** whether it survives).

### 2.5 Huma — the closest comparison

The partner every accounting system names. Masterdata with emergency contacts,
children, identification, bank via payroll sync, custom fields, Excel import and
export; **employment history with concurrent positions** (January 2026); teams,
locations, direct manager and an org chart derived from it. Absence with per-category
balances, manager approval, a shared calendar that pushes to Google Calendar and
Office 365 (colleagues see only "absent"), country public holidays, and a NAV
integration that creates the absence from the sykmelding and starts the follow-up
process with deadlines. Documents with sharing scopes; contracts from Schjødt-reviewed
templates with a builder; **e-signing through Scrive**. Competence (Premium): skills
and languages with levels beginner → expert, certifications, education, custom
categories, documentation requirements per competence with **expiry and reminders
at 90, 30 and 2 days** (Slack), gap analysis against an open profile, searchable by
skill — but no matching to projects. Onboarding and offboarding templates by
location and position; an equipment register with expiry notifications.
**Provisioning: one-way Huma → Entra ID in real time** (mail, names, phone, address,
employee id, job title, start), optional auto-create, manual pull from Entra;
deactivation in Huma does not deactivate in Entra. Google and Okta are SSO only;
Slack is notifications. Open API with OAuth2 client credentials and role-scoped keys;
**HMAC-signed webhooks** on profile, position, absence and organisation events.
Plans Free / Standard / Premium priced by company size; NOK figures only on partner
sites (**UNCERTAIN**). A free tier that includes masterdata, people list, org
structure, documents and the app is a notable go-to-market choice.

### 2.6 Simployer

The incumbent for sykefravær and compliance. Its **Sykefraværsoppfølging** (OAS) gives
the leader a timeline from day one — what to do, when, with deadlines and tasks — and
documents every activity per case. **Formell kompetanse** holds industry requirements
per role (sertifikater, kurs, helsekontroller) with deviation reports; the validity
period is computed from the expiry date with reminders at 3 months, 2 weeks, 1 day
before and 1 day after (support snippet — **UNCERTAIN** whether One or Classic).
Contract wizard with templates and an e-signing module. Entra user sync listed. A
Bemanningsplan shift module exists in the Classic suite. Pricing is quote-only.

### 2.7 Sympa and CatalystOne

Mid-market, read for their competence models only. Sympa assesses skills on a shared
**1–5 scale** org-wide with gap identification and coverage of certified skills per
site or shift. CatalystOne has HR Master Data with positions, contracts with
e-signature, onboarding with equipment tracking, a competence matrix with role-specific
assessment, and a CV module where the employee maintains skills, education and
certifications; Active Directory and Entra integration; absence to payroll.

### 2.8 Pricing picture

| Product | What the employee costs (NOK/month unless noted) |
|---|---|
| Tripletex | package 199–699; Lønn 65 per extra user; ferieplan only in Komplett 649 |
| PowerOffice Go | Lønn 195 + 35 per employee; Ferie og fravær 35 per user; Timeregistrering 100 + 50 per user |
| Fiken | 219 + 69 per user |
| Huma | Free / Standard / Premium by company size; partner sites 49–79 per employee (**UNCERTAIN**) |
| Dottie HR | resellers 98 → 55 per employee by volume (**UNCERTAIN**) |
| Simployer, Sympa, CatalystOne | quote only |
| BambooHR | USD 10 / 17 / 25 per employee, flat USD 250+ under 25 seats |
| Factorial | from USD 8 per user |
| Deel HR | from USD 5 per employee |
| Personio, HiBob, Rippling | quote; third-party USD 3–8 per employee (**UNCERTAIN**) |

### 2.9 What every one of them has

- An employee record with name, fødselsnummer or d-nummer (or international id),
  address, email and phone, and a bank account.
- An a-melding-shaped arbeidsforhold: start, end with reason, stillingsprosent,
  yrkeskode, ansettelsesform, arbeidstidsordning, avlønningstype — held by every
  payroll-capable system and pushed by every HR system.
- **Date-effective change rows** on the employment instead of overwrites (Tripletex,
  PowerOffice, Visma.net Payroll, Unimicro).
- Department as the cost dimension; a manager on the department or the employee.
- Ferie as a balance with employee request → manager approval → calendar.
- Egenmelding and sykmelding as distinct absence categories.
- Permisjon types that map to the a-melding leave descriptions.
- A mobile app or self-service for payslips, time, expenses and absence.
- Role-based access to sensitive employee data.

### 2.10 What only one or two have

- **NAV sykmelding auto-import** that opens the absence and starts the follow-up
  timeline — Huma, Dottie.
- **A sick-leave case timeline** with the statutory deadlines — Simployer, Dottie, Huma.
- **Certificates with expiry and reminders**, and required competence per role with
  gap reports — Huma, Simployer, Dottie; matrices in Sympa and CatalystOne.
- Skill levels on a scale — Huma (beginner → expert), Sympa (1–5), HiBob (named
  proficiency levels), Factorial (5-level competencies), Runn (0–4).
- Contract templates with merge fields and e-signing — Huma (Scrive), Dottie
  (BankID), Simployer, Sympa, CatalystOne, Personio (DocuSign).
- Equipment register tied to onboarding and offboarding — Huma, Dottie, CatalystOne.
- **Provisioning to Entra ID** — Huma (one-way), Dottie (two-way), Simployer One and
  CatalystOne (listed). Nobody provisions Google Workspace or Slack accounts.
- Public API with webhooks on employee, position and absence events — Huma (HMAC),
  Tripletex, Personio, HiBob, BambooHR, Factorial, Deel.
- Next of kin as a first-class entity — Tripletex, Huma.
- Multiple concurrent positions — Huma, Unimicro; the international six model one
  active employment at a time with history (§3.1).
- Automatic GDPR deletion policies — Dottie only.
- Calendar push of approved absence to Google and Microsoft 365 — Huma only.
- **Preferred working days, locations or hours — none of the eleven.**

## 3. The international baseline

### 3.1 Employee and employment models

Personio, HiBob, BambooHR, Rippling, Factorial and Deel all model a **person** with
**effective-dated employment history**: Personio v2 has Person → Employments ordered
by effective date with Position and org unit hanging off the employment; HiBob keeps
Work, Lifecycle and Payroll as historical tables where "only a single row can be
effective at any given time" and future-dated rows wait; BambooHR's `jobInfo`,
`employmentStatus` and `compensation` tables are rows per effective date, with
`onlyCurrent=false` returning the future; Factorial stores contract **versions** with
a history timeline. None clearly supports concurrent positions (**UNCERTAIN** for
each). Every one has custom fields, an org chart from the reports-to relation, time
off with approvals, documents with e-signature, onboarding and offboarding task flows
driven by attribute rules, and webhooks: Personio fires `employment.started` at 00:00
UTC on the hire date and `employment.terminated` at 23:59 UTC on the termination
date; BambooHR's `employee.updated` carries `changedFields`; Deel and Huma sign with
HMAC-SHA256.

### 3.2 Skills

| Product | Skills as a taxonomy with proficiency levels |
|---|---|
| HiBob | yes — Bob Skills library with categories, company-wide proficiency levels, an API with bulk create and `externalId` |
| Factorial | yes — competency groups with a 5-level framework and numeric scales, attached to job roles, rated in reviews |
| Deel | partial — skills per job level in the job architecture |
| Personio | no — custom attributes; a career framework is in development (**UNCERTAIN**) |
| BambooHR | no — "Trainings" that are one-time or **renewing** with a renewal interval and due dates model certificate expiry |
| Rippling | no taxonomy found; expiry tracking for licences and certifications marketed (**UNCERTAIN**) |

The pattern Vantigo should take is HiBob's for structure (categories, named levels,
stable external ids for import) and BambooHR's renewing training for expiry.

## 4. What Norwegian law requires of the register

Each item ends with its consequence for the data model. Short names: aml. =
arbeidsmiljøloven, ftrl. = folketrygdloven, fl. = ferieloven, bff. =
bokføringsforskriften, fua. = forskrift om utførelse av arbeid.

### 4.1 The written contract (aml. §§ 14-5, 14-6, 14-8, 15-6)

A written arbeidsavtale is required in every employment, within seven days of the
start for employments over a month and immediately for shorter ones and for hired-out
staff (§ 14-5). Its mandatory content after the 2024 amendment is seventeen items,
§ 14-6 (1) a–q: the parties; the workplace or a "varies" note; a title or description
of the work; the start date; expected duration **and the legal ground** when
temporary; prøvetid terms; holiday and holiday pay rights; notice periods for both
sides; pay at start with every allowance and non-wage benefit itemised, payment method
and payday; length and placement of daily and weekly working time, or a "varies" note
and the rules; break length; special working-time arrangements; rules for shift
changes and work beyond agreed hours, with pay; the tariff agreement; the innleier's
identity when hired out; the right to competence development; and social-security
benefits with the institutions receiving contributions (pension provider). Changes go
into the contract no later than the day they take effect (§ 14-8). If the contract is
silent on temporariness the employment is presumed permanent, and if silent on
stillingsomfang the employee's claim is presumed (§ 14-6 (3)–(4)). Prøvetid is at most
six months, at most half of a temporary term, extendable by absence only if agreed in
writing at hiring, and not repeatable in the same position (§ 15-6). The in-force date
of the 2024 rules was not re-verified (**UNCERTAIN**, widely reported as 2024-07-01).

→ An `employment_contract` version per change, effective-dated, with a column or
reference per item a–q; `employment_type` and `fte_percent` required and explicit —
a silent default to 100 % permanent is exactly the presumption the law warns about;
`probation_months ≤ 6`, derived "contract overdue" at start + 7 days.

### 4.2 Forms of employment and innleie (aml. §§ 14-9, 14-4a, 14-12)

Permanent is the main rule; temporary only on the grounds in § 14-9 (2) a–e (the old
general ground f is repealed); continuous temporary employment over three years on
grounds a or b converts to permanent; a temporary over one year is owed one month's
written notice of leaving. A part-timer who has regularly worked beyond the agreed
hours over the last twelve months has a right to a position matching actual working
time (§ 14-4a). Innleie from a bemanningsforetak is allowed only on grounds b–e (not
"arbeid av midlertidig karakter" any more), over three years gives a right to
permanent employment with the innleier, and is **banned for building work on
construction sites in Oslo, Viken and former Vestfold** since 2023-04-01 (forskrift
FOR-2013-01-11-33 § 4). Tilkallingsvikar has no own category; Skatteetaten's a-melding
gives it a value.

→ `appointment_form` aligned with the a-melding's five values; `temporary_ground`
required when temporary; derived warnings for the 12- and 36-month counters from the
person's employment chain; the § 14-4a check can compare Time's approved hours over a
trailing year with `fte_percent × full_time_hours`. If bemanning ever means hiring
staff out, every assignment needs innleier identity, legal ground, the innleier's
wage and working-time terms (likebehandling, § 14-12a) and the site's location and
category for the geographic ban — a separate design when that day comes.

### 4.3 Working time (aml. kap. 10)

The employer must keep "en oversikt som viser hvor mye den enkelte arbeidstaker har
arbeidet", available to Arbeidstilsynet and the tillitsvalgte (§ 10-7); Arbeidstilsynet
reads this as name, date, daily start and stop, and overtime start and stop. No
statutory retention period exists for the record itself; hour-based pay documentation
is kept three and a half years (bff. § 7-3). Overtime caps: 10 hours per 7 days,
25 per 4 weeks, 200 per 52 weeks (20 / 50 / 300 by written agreement with
tillitsvalgte); total working time at most 13 hours per 24 and 48 per 7 days
averaged over 8 weeks (§ 10-6); rest at least 11 hours per 24 and 35 per 7 days
(§ 10-8).

→ Time already holds the entries; Staff needs an employee-facing and inspector-facing
view and export, a per-employee overtime profile (statutory or tariff) and rolling
7-day / 4-week / 52-week windows that warn. These warnings are also the "mandatory
rules" a future matcher must never violate (§6.5).

### 4.4 Holiday (ferieloven)

Twenty-five **virkedager** per ferieår — Saturdays count, so four weeks and a day —
plus six for employees turning 60 (§ 5); five weeks and 12 % are tariff terms
(**UNCERTAIN** which agreements). The ferieår is the calendar year; feriepenger are
earned the year before at 10.2 % (§ 4, § 10). Eighteen virkedager fall in the main
period 1 June – 30 September; up to twelve virkedager may be carried over by written
agreement, and holiday not taken because of sickness or leave carries over
automatically (§ 7). Employment started after 30 September gives six virkedager that year.

→ A `holiday_account` per person per ferieår: entitlement under a scheme (25 or 30
virkedager, +6 at 60, prorated after 30 September), taken, remaining, agreed and
forced carry-over with reason, a main-period check. The HR module hands payroll the
days taken and the rate; payroll computes the base.

### 4.5 Sick leave (ftrl. kap. 8, aml. § 4-6)

Egenmelding needs two months of employment, covers up to three calendar days per case
and may be withdrawn after four self-certified absences in twelve months (§§ 8-23,
8-24, 8-27); the extended scheme of eight days per case and 24 per year is an
employer's choice. The arbeidsgiverperiode is sixteen calendar days, and absences less
than sixteen days apart fall into the same period (§ 8-19). Follow-up duties with
dates: oppfølgingsplan within four weeks and sent to the sykmelder, dialogmøte 1 within
seven weeks (aml. § 4-6), NAV's aktivitetsplikt at eight weeks, dialogmøte 2 at 26
weeks, sykepenger end at 52. The sykmelding arrives digitally on nav.no; the
inntektsmelding is due after the arbeidsgiverperiode, and NAV states Altinn cannot be
used for it from 2026-05-21. Datatilsynet: the employer may not demand a diagnosis;
only staff with a service need may see health data.

→ A **sick-leave case** per person with first absence date, derived milestones (+16
days, +4, +7, +8, +26, +52 weeks), per-milestone done date, a waiver flag, attachments
for the plan; an employer-level `self_cert_policy`; counters over trailing twelve
months; a degree in percent; **no diagnosis field**; health documents behind a
sensitive permission with an access log.

### 4.6 Leave vocabulary (aml. kap. 12, ftrl. kap. 9 and 14)

Svangerskapskontroll, svangerskapspermisjon (12 weeks before birth), omsorgspermisjon
(2 weeks for the other parent), fødselspermisjon (6 weeks), foreldrepermisjon (12
months plus 12 per parent, delvis by agreement within 3 years), ammefri, sykt barn
(omsorgsdager: 10 per year, 15 with three or more children, doubled for a sole carer,
+10 per chronically ill child; employer pays the first ten), pleie av nærstående,
utdanningspermisjon (statutory or not), militærtjeneste, offentlige verv, religiøse
høytider (2 days), and non-statutory velferdspermisjon and permittering. The
foreldrepenger quota arithmetic is NAV's (the 59 versus 61 weeks at 80 % figure is
**UNCERTAIN**).

→ An `absence_type` enum with these values, each with a paid flag, a percentage, and a
mapping to the six a-melding leave descriptions plus permittering; an omsorgsdager
counter derived from child count and flags, not from the children's identities.

### 4.7 The a-melding employment record (a-opplysningsloven; Skatteetaten's guide)

Reported monthly, from the first a-melding covering the start date until the end
month, even with no pay; a new hire or an end is reported by the Friday of the
following week (ftrl. § 25-1). Mandatory for an ordinary employment: a stable
`arbeidsforholdID`; start; end with one of six reasons (employee resigned, employer
terminated, contract or temporary employment ended, should never have been reported,
organisational change or internal position change, change of payroll system or
accountant); yrkeskode STYRK-08 (seven digits, by duties); hours per week for a full
position; stillingsprosent and the date it last changed; arbeidstidsordning in six
values (ikke skift; skift 36.5; offshore 33.6; helkontinuerlig 33.6; døgnkontinuerlig
and turnus 35.5; andre); the date of the last pay change; ansettelsesform in five
values (fast; midlertidig; fast og utleid; midlertidig og utleid; midlertidig
tilkallingsvikar); lønnsansiennitet and lønnstrinn only under the public and finance
tariffs; leave over fourteen days with id, dates, percentage and one of six
descriptions, or permittering; maritime extras.

→ These are first-class columns on `employment`, with `fte_changed_at` and
`salary_changed_at` maintained from the versioned changes rather than typed; an export
in the shape a payroll system imports.

### 4.8 Pension, insurance, tax card, registration

OTP-loven obliges a pension scheme once the firm has two people at ≥ 75 % or one
non-owner at ≥ 75 % or part-timers summing to two årsverk, established within six
months, minimum 2 % of pay up to 12 G, membership from age 13 and the first reported
pay. Yrkesskadeforsikring is mandatory for every employer. The skattekort fetch is
payroll's, keyed on the national id. → Company settings for pension provider (also
§ 14-6 q) and injury insurer; a derived OTP threshold warning; the national id stored
once, restricted, never a display id.

### 4.9 Personal data (GDPR, personopplysningsloven § 12, Datatilsynet, bokføring)

The lawful basis is the contract or a legal obligation, consent being weak in
employment; health and union membership are special categories. The fødselsnummer may
be processed when secure identification is needed — true for a-melding, skattekort and
pension — but not used as a display key or in URLs. Retention: Datatilsynet makes the
end of employment the ceiling for personnel-administration data, keeps "who was
employed when" for historical needs, allows a hold for legal claims, suggests
re-assessing a year after the end, and reads five years for warnings by analogy with
statsansatteloven; bokføringsloven keeps pay documentation five years and the "antall
arbeidede timer" documentation three years and six months (bff. § 7-3). The employee's
innsyn is answered within a month. The e-mail access regulation requires the mailbox
to be closed at termination and makes any innsyn a notified, reasoned event.

→ Retention **classes** per field group: (A) pay documentation 5 years, (B) hour
records 3.5 years, (C) name, dates and title indefinitely, (D) contact details, next of
kin, photo and competence evidence deleted at end plus a grace period, (E) health and
sick-case data deleted when the case and any refund claim are closed (**UNCERTAIN**
period), (F) a legal-hold flag. An end-of-employment erase run with a twelve-month
re-assessment reminder; a per-employee export; an offboarding item "mailbox closed".
Customers' one-date anonymisation (§7.4) is the worker pattern, not the policy.

### 4.10 Varsling, verneombud, AMU, internkontroll

Written varslingsrutiner at five or more employees (aml. § 2 A-6); a verneombud is
mandatory from five employees since 2024 and an AMU from thirty (§§ 6-1, 7-1);
internkontrollforskriften § 5 requires documented HMS organisation, risk assessment
and training; aml. § 3-5 requires the daglig leder's HMS training. → Role flags with
elected-from and -to dates (`is_verneombud`, `amu_member`), HMS course types in the
competence catalogue, and a headcount-driven compliance checklist (5, 30, 50; 20 on
request).

### 4.11 Certificates with legal weight

| Certificate | Basis | Issuer | Validity |
|---|---|---|---|
| Fagbrev / svennebrev | opplæringslova (**UNCERTAIN** §) | fylkeskommune | none |
| Relevant fagbrev for electrical work; faglig ansvarlig | FEK §§ 3, 6, 7 | fylkeskommune; DSB register | none; one faglig ansvarlig per foretak |
| FSE training incl. first aid | FSE § 7, DSB veiledning | employer or course provider | at most 12 months between sessions |
| Sertifisert sikkerhetsopplæring (truck, kran, masseforflytningsmaskiner) | fua. § 10-3 | certified provider | none in the regulation (**UNCERTAIN**; practice issues kompetansebevis) |
| Dokumentert sikkerhetsopplæring | fua. §§ 10-1, 10-2 | employer | none |
| Stillas 2–5 m, 5–9 m, over 9 m, bruker | fua. §§ 17-2 to 17-5 | employer or provider, hours documented | none |
| HMS-kort (bygg/anlegg, renhold, bilpleie, varebil transport from 2026) | FOR-2007-03-30-366 §§ 4, 6–8 | Arbeidstilsynet's issuer | 2 years and only while employed; destroyed at end |
| Førerkort | førerkortforskriften § 4-1 | Statens vegvesen | B 15 years; C and D classes 5 |
| Yrkessjåførkompetanse (kode 95) | yrkessjåførforskriften | Statens vegvesen | 5 years, 35 h etterutdanning |
| Varme arbeider | insurers' sikkerhetsforskrift | Norsk brannvernforening | 5 years |
| Arbeidsvarsling 1–3 | Statens vegvesen N301 | Statens vegvesen | 5 years (**UNCERTAIN**) |

→ One `certificate` shape — type, issuer, issued, valid-to or none, evidence
document, required-for links — with a seeded Norwegian catalogue, per-type reminder
lead times (30 days for yearly, 6 months for five-year certificates), and an
offboarding action for the HMS-kort.

### 4.12 Likestilling, pay transparency, sluttattest

The aktivitets- og redegjørelsesplikt applies to private employers over fifty
employees, and from twenty on request, with a lønnskartlegging by gender every two
years (ldl. §§ 26, 26a); the EU pay-transparency directive was not in the EEA
agreement as of 2025-11-20 (**UNCERTAIN** since). → Store gender and job category so
the anonymised report can be built from a payroll export; whether pay categories
belong in Staff is an open question. On termination the employee is entitled to a
sluttattest with name, birth date, what the work consisted of and the duration
(aml. § 15-15) → a generated document from the employment history.

## 5. HR as the source of truth for identity

### 5.1 How the HR products do it

Rippling makes the employee record the identity source: dynamic "Supergroups" built
from department, location, employment type and level assign apps, Slack channels and
Google Groups, so a mover re-evaluates automatically; Google Workspace is created a
configurable number of days before the start so the manager can invite the hire,
other apps on the start date; termination suspends rather than deletes and transfers
Drive and Calendar ownership; into Entra it pushes through **API-driven inbound
provisioning**, with Microsoft recommending the hire and termination dates be mapped
to `employeeHireDate` and `employeeLeaveDateTime`. Personio and HiBob push one-way
into Entra and Okta — Personio matches on UPN = email the first time, then writes its
employee id into Entra's `employeeId` and matches on that. Okta's model is the
opposite direction with the same authority rule: an HR app is a **profile source**
that owns status, so an admin suspension of an HR-sourced user is reverted by the next
import; Okta's Workday and BambooHR connectors have a pre-start interval and
time-zone-aware terminations, and rehires reactivate the existing user. Huma's and
Dottie's Entra connectors are the Nordic instances of the same pattern (§2.3, §2.5).

### 5.2 Microsoft Entra ID

Three ways in. **API-driven inbound provisioning**: `POST
/servicePrincipals/{sp}/synchronization/jobs/{job}/bulkUpload` with a SCIM
`BulkRequest` of at most fifty operations in the core and enterprise schemas; the
service — not the client — decides create, update, enable or disable by matching
`externalId` to `employeeId`, applies scoping and attribute mappings, maps
`active:false` to `accountEnabled=false`, never deletes (that is Lifecycle Workflows'
job) and does not de-duplicate the UPN for cloud targets; permission
`SynchronizationData-User.Upload.OwnedBy`; 2,000 calls per day on P1 or P2. **Direct
Graph**: `POST /users` requires `accountEnabled`, `displayName`, `mailNickname`,
`userPrincipalName` and a `passwordProfile` (`User.Create` least privilege); the
manager is set with `PUT /users/{id}/manager/$ref` (`User.ReadWrite.All`);
`employeeId` at most sixteen characters, `employeeHireDate`, `employeeType`,
`department`, `jobTitle`, `usageLocation` (required before a licence); writing
`employeeLeaveDateTime` needs `User-LifeCycleInfo.ReadWrite.All`. Microsoft's
passwordless onboarding pattern is a Temporary Access Pass emailed to the manager on
the hire date. **Lifecycle Workflows** (joiner, mover, leaver tasks at offsets from
the hire and leave dates) need **Entra ID Governance** or Entra Suite; API-driven and
HR-driven provisioning need P1 or P2, which Microsoft 365 Business Premium includes.
UPN characters are restricted, so Nordic names need diacritics normalised.

### 5.3 Google Workspace

Admin SDK Directory `users.insert` requires `primaryEmail`, given and family name and
a **password** (clear text 8–100 characters or a hash); `orgUnitPath`, `suspended`,
`changePasswordAtNextLogin` (ignored under a third-party IdP), `externalIds[]` with
type `organization` for the employee number, `organizations[]` (title, department,
cost centre), `relations[]` of type `manager` by email; 409 on a conflicting account,
202 when an unmanaged account is invited. Suspension keeps data and licence; deletion
is recoverable for twenty days. Authentication is a service account with domain-wide
delegation, authorised by a super admin for the `admin.directory.user` scope and
impersonating an admin. There is no "disabled" state other than suspended, so a
pre-start account is created suspended or on the day.

### 5.4 Okta

`POST /api/v1/users?activate=false` creates a STAGED user; activation sends the
token email; lifecycle endpoints activate, suspend, deactivate and reactivate; delete
only after deprovisioning; scope `okta.users.manage` from an API service app. Being an
Okta "profile source" requires a listing in Okta's integration network; **UNCERTAIN**
whether a custom SCIM app can be declared one.

### 5.5 The generic pattern, and Vantigo's position

1. **Person ≠ employment ≠ account.** The person is stable across hires; employments
   are effective-dated; each identity provider holds an account linked by the
   provider's object id. Keep an `identity_link` per provider on the person: provider,
   external object id, status, last pushed version.
2. **Correlate on the employee number**, email second. Keep it at most sixteen
   characters for Entra; decide whether a rehire reuses it (then the old account is
   re-enabled) or gets a new one (then a new account is created).
3. **Timing rules** are data, not code: create disabled N days before the start,
   enable on the start day, disable at the end of the leave day in the business time
   zone (Personio fires at 23:59, Entra recommends 21:00–23:00), delete X days later.
4. **Movers** flow as attribute updates — department, manager, title, cost centre —
   and group or licence membership is derived from attributes on the IdP side.
5. **One authority per installation for account existence and status.** Every
   integration studied is one-way. If Staff is authoritative and pushes to Entra,
   Entra's inbound SCIM into Vantigo must be treated as a mirror: correlate on
   `externalId` or `employeeNumber`, never create a second person, ignore `active`
   changes that echo our own push. If the IdP is authoritative, Staff links but does
   not push. Entra's own bulkUpload design — send the full record, let the service
   diff — is the right shape for an idempotent Staff → IdP connector.
6. **The SCIM enterprise extension** (RFC 7643 § 4.3: `employeeNumber`, `costCenter`,
   `organization`, `division`, `department`, `manager.value`) is what both directions
   speak. Vantigo's server must accept it inbound (§7.1), and Staff's outbound
   payloads should add `employeeType`, hire and leave datetimes and `usageLocation`.

Inside Vantigo the same split holds: Staff owns the person and the employment;
identity owns the account. Creating an account from Staff is a **new cross-module
write direction** — the boundaries page says one needs "a design of its own, not a
second holder-shaped interface" (boundaries.md:108-109) — and identity has the
in-process pieces to wrap: `issueInvitation` and `deliverInvitation`
(`srv/identity/invitations.go:105-135`), already called outside HTTP by the bootstrap
(`srv/identity/bootstrap.go:342-415`). Two constraints shape it: an invitation grants
only the `User` or `Owner` role (`mig/00002_identity_baseline.sql:174`), with RBAC
roles assigned afterwards through `PutIdentityAccessUsersByIdRoles`, and the user row
does not exist until the invitation is accepted, so Staff holds an invitation id or
correlates by email until then. A `contracts.UserProvisioning` port provided by
identity, pool-based and never under a lock, mailing after commit as
`deliverInvitation` already does, is the candidate shape; external providers (Entra,
Google, Okta) are adapters behind a Staff-side `IdentityProvider` port, the way
Invoices put Storecove behind an access-point port.

## 6. Competence, availability and preferences — what scheduling and PSA tools do

### 6.1 Competence in shift scheduling

Planday: a skill has a name, an optional **expiry date**, an optional **seniority**
level and an employee-group association; a shift or position carries a required skill;
"only employees who match the required Employee group and have the required Skill can
be assigned", and an expired skill **blocks** assignment until updated. Quinyx: skills
in a mandatory category, skill templates, linked to shifts; a mismatch is a
**warning** that names the missing skill; level and expiry not visible
(**UNCERTAIN**). tamigo: a missing skill filters which open shifts the employee sees.
Gat (Visma Enterprise's Norwegian scheduler, with Optur Plus, MinGat and GatGo) matches
available staff to the required competence when generating a turnus; its competence
structure is not public. Simployer Tid og Bemanning puts kompetansekrav on shifts and
checks them on shift exchange.

### 6.2 Competence in project resource planning

Runn: **skills with levels 0–4** per person and per placeholder, "Group Availability by
Skills", and "Find a Person" that filters by the placeholder's role, skills and levels
and warns on time off. Float, Resource Guru, Teamdeck and Milient (Moment's successor)
use flat tags or custom fields. Severa allocates to a **role** bound to a person's work
contract when the person is not yet known. Tripletex's Ressursplanlegger and
Kapasitetsplan allocate hours per day with "ledig tid = normaltid − allokert", no
skills, no tentative state. Certificates with issuer and evidence appear only in
staffing and HR tools (RecMan's courses and certificates, Simployer's formell
kompetanse).

### 6.3 Requirements, allocations and availability

Shift tools attach the requirement to a shift, position or shift type; PSA tools to a
placeholder or role allocation. The allocation entity is the same everywhere: person
or placeholder × project or phase × date range × hours per day, total hours or
percent, with **tentative versus confirmed** (Float, Resource Guru, Productive,
Teamdeck by tag, Runn by project state) — the roadmap's Phase 5 shape. Availability is
split in four layers in every product: (a) the contract or working pattern
(contracted hours, weekday windows, nominal hours), (b) absence, always hard,
(c) public holidays, (d) the employee's own declared availability or unavailability
per date or weekday window — **soft** in Planday ("even if an employee marks
themselves as unavailable, you can still schedule them"), **hard by default but
configurable** in Quinyx's Auto Assign, where absence always wins. Capacity is contract
hours minus absence minus holidays minus existing allocations, as Float, Severa and
Tripletex compute it.

### 6.4 Preferences and the rule engine

Preferences beyond availability are thin: Quinyx weighs "staff preferences for working
times" and fairness; Gat takes ønsker from MinGat; Planday chooses rotation versus
fixed distribution. Preferred location, customer or project type, "do not schedule
with", travel willingness and a personal maximum of consecutive days are **industry
ideas not observed as fields** anywhere. Working-time rules are the clearest pattern:
Quinyx's agreement templates carry maximum hours per day and week, minimum weekly
hours, **11 hours daily and 35 hours weekly rest**, maximum consecutive days and break
rules — all warnings in manual scheduling, with a separate compliance layer that
splits rules into overridable warnings and **hard stops**; its optimiser never violates
rules marked mandatory and weights the rest. Planday's rules only ever warn, except
skill and employee group, which block.

### 6.5 The minimal model that serves both

- `competence_category` (Fag, Sertifikat, Språk, Verktøy, Domene) → `competence`
  (name, `requires_expiry`, `level_scale` none or 0–4). Flat; no tree.
- `person_competence` (level, valid-from, valid-to, issuer, evidence document,
  verified by and when, note). Level labels for Norwegian use: 0 kjenner til,
  1 grunnleggende, 2 selvstendig, 3 erfaren, 4 ekspert. SFIA's seven levels are
  responsibility, not proficiency, and should not be copied.
- `person_interest` (competence, wants-to-work-with) — an industry idea, cheap, and
  the one thing that makes project staffing feel fair.
- `competence_requirement` (target: project role, task, shift template or position;
  competence; minimum level; must-be-valid-on-date; hard or soft). Vantigo's three
  project roles are the first target; a Phase 5 placeholder is an unstaffed role with
  requirements.
- Availability, reserved for later and named now: the employment's hours per week
  and working pattern, absence (Staff's, §4.4–4.6), a public-holiday region, and an
  `availability_declaration` (date or weekday window; available, unavailable or
  preferred; recurring; source employee or manager; locked).
- `person_preference` rows keyed by kind with a weight 1–3: preferred weekdays and
  time of day, locations, customers, project types, avoid-customers, do-not-schedule-
  with, travel willingness, maximum consecutive days, preferred maximum hours per
  week, remote. A deliberate superset of the market.
- The matching contract, documented now and implemented with bemanning: hard filters
  in order — employed on the date, no absence, required competences valid at the
  minimum level, the rules the installation marks mandatory (§ 10-8 rest, § 10-6
  caps) — then a weighted soft score over availability, preferences, hours balance
  and cost; warnings for soft breaches, a hard stop only for mandatory ones.

### 6.6 Out of scope by every product's first version

Skill trees and competency frameworks, demand forecasting, automatic shift generation
(Quinyx Auto Schedule, Gat Optur), payroll rule engines.

### 6.7 The staffing cases the owner named (2026-10-11), and the ones they imply

The owner's three cases describe **project staffing in a consultancy or
engineering firm**, not shift scheduling: people are placed on customer projects for
months, a project has phases with different needs, and a placement has contractual
terms of its own. That settles open question 1 for the data model (the shift-rule
layer in §6.4 stays reserved, not built) and adds one entity the market products
above do not have.

1. **Capacity at intake.** When a new project is being accepted, see whether the
   firm has people with the right competence free in each phase. This needs: a
   project in a **pipeline state** (bid, tender, likely) so planning does not
   pollute real data — Float and Productive call it Draft or Tentative at project
   level; **demand per phase** as placeholders (role, competence requirements at a
   minimum level, hours or percent, date range) — the Runn and Severa shape; and
   **capacity** = contract hours − absence − holidays − confirmed and tentative
   allocations, filtered by competence valid on those dates. The answer is a fit
   per phase (covered, partly covered, uncovered) with the candidates behind it.
   Several pipeline projects at once is the natural extension: "can we take A and
   B, or only one". Phases are the roadmap's Projects Phase 4 delivery milestones
   and timeline; allocations are Phase 5.
2. **The employee's timeline.** One forward view per person: allocations and
   assignments with their end dates, planned absence, holidays, certificates that
   expire inside a planned assignment, the notice period of the current assignment,
   and the end of employment when known. From it the system derives the **unbooked
   list**: anyone whose allocated share falls under a threshold within a horizon
   (for example under 50 % in the next six weeks), with the date they roll off.
   This is Runn's and Float's utilisation forecast turned into an alert. The same
   data gives the revenue forecast from planned hours the roadmap already names.
3. **Wishes, requests and matching with notice periods.** Two sides meet: a
   **move wish** on the person (wants off project A from a date, with reasons and
   preferences, visible to managers only) and a **staffing request** on a project
   (wants competence X at level Y from a date, open until filled). The matcher
   proposes moves. The constraint the owner names is the **assignment's notice
   period**: a placement on a customer project is a contract (an oppdragsavtale)
   with its own oppsigelsestid, typically one to three months, so the earliest
   release date is max(notice given + notice period, phase end) and a person cannot
   start elsewhere before it. That makes the **assignment** an entity distinct from
   the allocation: person × project × from and to × notice period × notice given on
   × earliest release × customer terms. Customer terms seen in consultant contracts
   and worth columns from the start: a minimum duration, whether the customer must
   approve a replacement, a required handover overlap, and a non-compete or
   karantene toward named competitors for a period after the assignment, which
   the matcher must honour as a hard filter.

Cases the three imply, and that the products reviewed or Norwegian practice
suggest adding:

- **Handover overlap.** A move is two allocations, not one: the leaver's taper and
  the joiner's ramp-up overlap for an agreed number of weeks, and the project's
  cost shows both. Suggest the overlap when proposing a move.
- **Foreseeable capacity drops.** Approved foreldrepermisjon, a reduced position, a
  long sick-leave case or an employment end date is a known hole in a project
  months ahead; the system should raise a staffing request automatically
  ("vikar needed on project P from date D with competence X").
- **Holiday against staffing.** Ferie approval should see the allocations it
  conflicts with (coverage per project per week), and intake should see planned
  holiday. Today neither exists; both read the same two tables.
- **Certificates against planned work.** An assignment that needs a certificate
  expiring before its end should create a renewal reminder dated before the start,
  not at the usual lead time; a lapsed certificate on an assignment is a warning
  on the timeline.
- **Gap-to-hire or gap-to-train.** Requests that stay open past a threshold, and
  competences the pipeline needs more of than the firm has, feed two decisions:
  recruit, or send someone with the interest flag on a course. The § 14-6 p right
  to competence development and the person's own interests (§6.5) make the
  training path a first-class suggestion, not an afterthought.
- **Rotation and fairness.** How long a person has been on one project, how often
  their wishes were granted, and who always gets the attractive assignments are
  the fairness signals Quinyx's and Gat's optimisers weigh; here they are a sort
  order on the match list and a flag on the timeline ("on project A for 34 months").
- **Key-person risk.** A project whose requirement is met by exactly one person,
  or a competence held by one employee, is a risk the intake view should show.
- **Over-allocation and part-time.** Allocations across projects summed against the
  contract's hours, with part-time, reduced hours and the § 14-4a trailing-year
  check from §4.2 in the same view.
- **External capacity.** Freelancers, subcontractors and innleid staff fill
  placeholders too, with their own cost and availability; the a-melding vocabulary
  already distinguishes them. Their assignments carry the customer terms above and,
  for innleie, the § 14-12 limits.
- **Access follows the assignment.** When provisioning exists, a confirmed
  assignment is what adds a person to the project's groups, mailboxes and tools,
  and the earliest release date is what removes them — the mover case in §5.5.
- **Notifications.** New request matching a wish, wish matching an open request,
  unbooked within the horizon, assignment ending in N weeks with no follow-on,
  notice period elapsed, certificate at risk — all to the manager, some to the
  person.

**Where this lives** is a design decision the spec must take. The entities split
three ways: the person's side (wishes, interests, preferences, availability) is
Staff's; the project's side (phases, requests, allocations) is Projects' Phase 5;
the assignment with customer terms, and the matcher that reads both, belong to
neither. The module-boundary rules (§7.2) suggest a third module — **Staffing** —
that owns assignments, requests, allocations and matching, reads Projects through
`contracts.ProjectDirectory` and Staff through a new read contract (employment on a
date, competences valid on a date, preferences and wishes), and is the first module
that consumes two business modules at once. Whether Phase 5's allocations move
there from Projects, or Staffing reads them, is the first question of that spec.

**Two decisions taken by the owner on 2026-10-11, after the above.** First, the
feature is **legally agnostic**: it supports every scenario in §6.7, including a
firm that hires its people out, and whether a given use is lawful is the using
company's responsibility. For the design that means the statutory limits in §4.2
and §4.3 (innleie grounds, the three-year counter, the geographic ban, overtime
caps, rest) are **warnings the installation can mark as hard**, never built-in
refusals — the Quinyx split in §6.4 — and the data those warnings need (innleier
identity, likebehandling terms, the work's location and category) is still modelled,
because a company that does hire out needs it for its contracts and the a-melding's
"utleid" forms. Second, **shifts are in scope**: employees see their own shifts and
can **trade** them with a colleague (a dentist appointment, a day off) under the
market's flow — swap or hand-over request, the colleague accepts, the manager
approves or an auto-approval rule passes (same required competence, no rest or
hours breach, same position), notifications both ways (Planday, Quinyx §6.1, §6.4).
A shift is a booking of a person's time at a specific start and stop on a date for a
position or location, distinct from an allocation (a share of a period on a
project) and an assignment (a contracted placement); all three are what the
employee timeline (§6.7 case 2) and the capacity calculation subtract from the
contract's hours. Two consequences: every shift worker needs an account, which
moves provisioning (§5) earlier than "later" in the phase order, and the employee
view must work on a phone. The § 10-7 working-time record then has two sources —
Time's entries for project hours and the shift plan for attendance — and reconciling
them (punches, deviations) is a time-and-attendance feature of its own, not Staff's
or Staffing's first version. Whether shifts and project staffing are one app or two
is still open; §9 question 1 lists the options.

## 7. Where the codebase stands

### 7.1 Identity, and the SCIM gap

`identity.users` holds account facts only (§1.1). Users are created by the bootstrap,
by an Owner with a password (`srv/identity/users.go:219`), by invitation
(`srv/identity/invitations.go:48`, accepted at `:241`), by OIDC just-in-time
provisioning with the `User` role and a synthetic `@sso.invalid` email when the
provider sends none (`srv/identity/oidc.go:659`), and by SCIM
(`srv/identity/scim_users.go:373`). `contracts.UserDirectory`
(`srv/contracts/users.go:12-50`) is read-only — id, display name, active — and
always present; nothing lets another module create, invite or read an email.
`identity.scim_user_mappings.source_profile` keeps only `givenName` and `familyName`
(`srv/identity/scim_users.go:36-40`); the advertised schema lists no enterprise
extension (`srv/identity/scim.go:477-520`), an extension block in a POST or PUT is
dropped silently, and a PATCH to one of its paths answers 400 `invalidPath`
(`srv/identity/scim_input.go:591-625`). `employeeNumber`, `department` and
`manager.value` are what finding 7 in §1.2 needs identity to keep. The admin UI for
users is host-owned (`apps/host/frontend/src/api/users.ts`).

### 7.2 Boundaries and the write direction

The rules that bind (boundaries.md): no module imports another (depguard,
`apps/server/.golangci.yml`, one rule set per module); one schema per module with no
cross-schema keys or joins (`srv/db/schema_test.go:28` and the scan at `:138-147`);
contracts are DTOs, with `pgx.Tx` the one exception; nine single-provider slots and
four many-provider slots in `module.Compose`; "no call that takes its own connection or
leaves the process while a transaction holds locks" (boundaries.md:164-169); a new
write direction needs its own design (boundaries.md:108-109). Expenses "depends on
nobody but identity" (boundaries.md:396-417) and is the scaffold to copy: Staff too
depends only on identity, with Projects optional later for requirements on roles.

### 7.3 Every place a person is referenced today

| Module | Column | Where |
|---|---|---|
| Projects | `project_roles(project_id, user_id, role)` manager/member/viewer | `mig/00008_projects_baseline.sql:34-41` |
| Projects | `tasks.assignee_user_id` | `mig/00009_projects_tasks.sql:14` |
| Projects | `timeline_entries.actor_user_id` + `actor_display` snapshot | `mig/00008:62-63` |
| Time | `entries.user_id`, `approved_by_user_id`; rates snapshotted per entry | `mig/00010_time_baseline.sql:8,19-27` |
| Time | **`person_rates(user_id, valid_from, bill_rate, cost_rate, currency)`** | `mig/00010:38-48`; `srv/time/rates.go:196-210` |
| Time | `week_submissions(user_id, week_start)` | `mig/00010:50-55` |
| Expenses | `entries.user_id`, approver, reimburser, invoicer; `claims.user_id` | `mig/00012:59-96`, `mig/00013:13-38` |
| Customers | `customers.owner_user_id`; follow-up assignee | `mig/00024:16-17`, `mig/00026:33-47` |
| Communications | `conversations.assigned_user_id`, authors, read states | `mig/00006:99-453` |
| Invoices | `issued_by_user_id` and ~20 audit columns | `mig/00034:165-166`, `mig/00041` |

All opaque uuids, no foreign keys, "Unknown user" when the directory no longer knows
the id. The payroll CSV (`srv/expenses/reimbursements.go:605-608`) has thirteen
columns and no employee number, bank account or cost centre. There is no line manager
anywhere: Time approves by project manager or `time:approve`, Expenses by
`expenses:approve`. Time's person rate card is HR data living in Time, managed under
`time:manage` (`srv/time/ratecards.go:179-330`); whether Staff eventually owns the
cost rate is a decision, not a necessity.

### 7.4 Patterns to reuse

- **GDPR**: `contracts.CustomerPersonalData` (`srv/contracts/personal_data.go:44-47`)
  with export outside any transaction and erase inside the owner's; the
  `customers-anonymisation` worker on an advisory lease, at most fifty per cycle, one
  transaction each, the row kept and fields blanked, read-only afterwards. The spec
  explicitly leaves "anonymising staff users" to identity
  (`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md:146`). The national id
  is **refused** for customers (D1); Staff must store it (§4.9), encrypted through
  `internal/secrets` as TOTP secrets are.
- **Effective-dated rows**: `time.person_rates` and `expenses.rates`, "greatest
  valid_from on or before the date", seeded rows resettable.
- **Point-in-time history**: customers' timeline entries mirrored by a revisions
  table (`mig/00003:53,84`; rationale `mig/00026:24-29`) with an actor snapshot
  (`srv/customers/actor.go:12-32`). Employment versions want the same shape.
- **CSV import and export**: `srv/customers/import.go:1064` with dry run, layout
  detection and per-row plans; `srv/customers/csvexport.go:104`. Huma's Excel
  template import is the market expectation.
- **Documents**: `internal/storage` `ObjectStore` with `NewScope` (scopes
  `communications`, `expenses`, `invoices`); the receipt pattern — metadata row,
  object key never derived from the file name, sniffed type, 10 MiB cap, object
  written before the transaction and removed if it refuses
  (`srv/expenses/attachments.go:240-416`). The roadmap's "Project documents" entry
  waits for "a document model"; an HR archive would be the first general one.
- **Workers and mail**: `worker.Worker` (`srv/worker/worker.go:26-41`), the
  anonymisation worker as the simplest lease loop, per-row leases in the invoices
  reminder worker for provisioning jobs, `mail.SendOutbound` with the `Deps.SMTPSend`
  seam, never under a lock. No event bus yet: an identity → Staff "user disabled"
  signal is a poll over `UserDirectory` or the first outbox consumer.
- **Settings**: Expenses' typed one-row table with `time_zone`
  (`mig/00013:88-107`) is the shape; Staff's self-certification policy, holiday
  scheme default, reminder lead times and provisioning rules live there.
- **The holiday calendar exists but is locked in Invoices**:
  `srv/invoices/reminderrules/months.go:81-115` (Norwegian public holidays, Easter,
  next business day). Depguard bars Staff from importing it; it moves to a platform
  package (e.g. `internal/calendar`, added to rule 1's list) before ferie needs it.

### 7.5 Scaffolding

Copy Expenses: `srv/staff/module.go`, `openapi/staff.yaml`, the oapi-codegen and
sqlc configs, `mig/00042_staff_baseline.sql` (00041 is the highest), the depguard rule
set and deny entries, `moduleSchemas`, `businessModules` in `cmd/vantigo/main.go`,
`Modules` in `srv/openapi/openapi.go:25`; `apps/staff/frontend` from
`apps/expenses/frontend`, the host tile in `apps.ts`, routes, catalogs, i18n; the
reference page with `sources:` frontmatter, user pages in en and nb, module-boundaries
and ROADMAP updates, `docs:check`. Permissions follow `<module>:<verb>`; the candidate
set is `staff:access` (see oneself), `staff:view` (the register), `staff:manage`
(employment and absence decisions), `staff:approve` (absence and competence
verification), and a sensitive `staff:personal-data` (national id, bank, health,
exports, erase).

## 8. The must-have list

What the research says a Norwegian SMB will expect on day one, and what is reserved
for Vantigo's advantage. "Must" is table stakes (§2.9) or law (§4); "should" is a
differentiator one or two competitors hold; "later" is confirmed demand without a
first-version need.

**Must — the register (phase 1).** A person with name, birth date, national id or
d-nummer (encrypted, sensitive permission), contact, address, next of kin (minimal),
gender for ARP, photo, bank account (masked), employee number (≤ 16 characters). One
or more employments, versioned and effective-dated, carrying the a-melding fields
(§4.7) and the § 14-6 contract items (§4.1) with explicit type and stillingsprosent,
probation, notice, tariff, pension provider, and the 2024 presumption warnings.
Departments with manager and cost centre; a direct manager on the employment; an org
view from it. A link to the identity user and a "no account" state. A document
archive per person with categories and a sensitive health category. Employee
self-service (see and propose changes to own data). CSV import and export. Retention
classes, an export-my-data endpoint, an end-of-employment erase run. A headcount
compliance checklist (5, 30, 50). A payroll-shaped export of the employment fields.

**Must — competence and preferences (phase 1 or 2, the owner's call).** The
catalogue and person competences with level, validity, issuer and evidence (§6.5),
seeded with the Norwegian certificate types (§4.11), a reminder worker with per-type
lead times, and preferences and interests as rows with weights. Requirements on
project roles come when Projects can consume them.

**Must — absence (phase 2).** Ferie accounts under a scheme with carry-over rules;
absence types from the statutory vocabulary with percent and paid flag; egenmelding
under an installation policy with the two-month and sixteen-day rules; sick-leave
cases with the milestone timeline; omsorgsdager counters; request → approval →
calendar; a-melding leave mapping in the payroll export; the holiday calendar moved to
the platform. Whether absence writes into Time (PowerOffice posts it into the
timesheet) or Time reads it through a contract is a design decision.

**Should.** Contract document generation from templates with merge fields
(signing later); sluttattest generation; onboarding and offboarding checklists with
the statutory items (HMS-kort destroyed, mailbox closed, a-melding end reason,
feriepenger note); an equipment register; NAV sykmelding import; webhooks on person,
employment and absence events; the SCIM enterprise extension inbound.

**Provisioning (its own phase).** `contracts.UserProvisioning` from identity; the
`IdentityProvider` port with Entra first (Graph `POST /users` with the enterprise
attributes, or bulkUpload), Google second, Okta on demand; identity links per
provider; timing rules in settings; one authority per installation; echo suppression
on inbound SCIM.

**Later.** E-signing (Scrive, BankID), medarbeidersamtaler and goals, lønnsjustering,
calendar push, and the bemanning feature itself — allocations and shifts, the
availability declaration, the matcher (§6.5) — which consumes Staff and Projects
Phase 5 rather than living in Staff.

## 9. Open questions for the owner

1. **What does bemanning mean here** — answered 2026-10-11: project staffing across
   customer projects **and** shifts with employee trading (§6.7), legally agnostic
   with statutory limits as configurable warnings. Still open: **one module or
   two.** (a) One `staffing` module owning every booking of a person's time —
   allocation, assignment, shift — plus requests, wishes, matching and trades, with
   one capacity calculation and one timeline, and an employee-facing page set
   gated by `staffing:access` beside the manager pages, the way Expenses serves
   submitters and approvers from one app. (b) Two modules, Resourcing (project
   allocations, assignments, requests, matching) and Shifts (rota, trades), each
   smaller, but with two capacity models that the timeline and the intake check
   must sum through a contract. (c) Allocations stay in Projects Phase 5 and shifts
   go into Staff, which leaves the assignment and the matcher without a home.
   Recommended: (a), with the frontend split by audience if the single app grows
   unwieldy; Vantigo's one-module-one-app convention (§7.5) would then need a
   first exception, which is a host decision rather than a backend one.
2. **Store the fødselsnummer?** Customers refuses it by design; Staff cannot work
   without it for payroll, HMS-kort and pension. Recommended: yes, encrypted, behind
   `staff:personal-data`, never displayed in full.
3. **Which payroll system is the export for** — Tripletex (richest API, could be
   pushed directly), PowerOffice Go (import only), Fiken (no API), or a generic CSV
   first? This decides whether phase 1 ships an adapter or a file.
4. **Should Time's person rate card move to Staff** as the employment's cost rate,
   with Time reading it through a contract, or stay where it is?
5. **Competence in phase 1 or phase 2?** The owner's brief puts competences and
   preferences in the module's core; the market puts absence first. Both are
   "must"; the order is a product choice.
6. **Which identity provider first** when provisioning comes — Entra (the OIDC
   provider already supported, the HR-inbound framework, every Nordic competitor's
   choice) is the evident answer; Google needs a password at creation and no HR
   framework exists.
7. **Holiday scheme default** for an installation: statutory 25 virkedager or the
   tariff 30? And is the business time zone shared with Expenses or moved to the
   platform?
8. **Pay data in Staff?** The § 14-6 contract requires pay components, the ARP needs
   pay categories, and payroll owns the numbers. Recommended: store the contract's
   pay components as the contract text requires, mark them payroll-sourced, and
   never compute from them.

## 10. Decisions taken by the owner on 2026-10-11

The open questions in §9 were answered the same day. The design spec starts from
these, not from §9.

1. **The fødselsnummer is stored.** It is required for nearly everything the
   register serves. Encrypted through `internal/secrets`, behind the sensitive
   `staff:personal-data` permission, never displayed in full, never a display key.
2. **Payroll export is a CSV first, behind an adapter port.** The export of the
   employment and absence fields (§4.7) is shaped so a Tripletex, PowerOffice or
   other adapter can be plugged in later without touching the register; the first
   adapter is the file.
3. **Time's person rate card stays in Time** for now; revisited once the employment
   record exists.
4. **Phase 1 is the register and absence; competence is phase 2.** What
   "competence" must mean for this company is discussed before that phase is
   specified; §6.5 is the starting proposal, not the decision.
5. **Entra ID is the first external identity provider**, with a setup that is
   deliberately simple and carries its instructions in the UI. Whether it is its own
   module is discussed in §10.9.
6. **Holiday and leave entitlements are configured by HR per company.** The
   statutory values (25 virkedager, +6 at 60, egenmelding 3 × 4) are the seeded
   minimum, and a company sets its own scheme and benefits above them — the same
   seeded-and-editable pattern as Expenses' rates.
7. **Pay components are stored as the contract requires them**, marked
   payroll-sourced, and never computed from.
8. **Provisioning is a flag on the employee, time-controlled.** `provision_account`
   (true or false, HR's decision; false for the rare cases that get no user) and
   `provision_from` (defaulting to the employment's start date, editable earlier or
   later). A person being hired exists in the register before the start with the
   date in the future and no account; a daily worker in the business time zone
   creates the account on the day. The same pair drives the leaver: the account is
   disabled on the employment's end date, and a rehire re-enables it. In an
   installation with Entra connected, "the account" is the Entra user, and the
   Vantigo login follows through SSO (just-in-time or inbound SCIM, correlated on
   the employee number, §5.5); without one, it is a local identity invitation sent
   on the day. Staff is the authority for existence and status in both cases.
9. **Staffing is one module**, owning every booking of a person's time
   (allocation, assignment, shift), requests, wishes, matching and trades, with
   employee-facing pages beside the manager pages (§9 option a). The person, the
   employment, absence and competence remain Staff's. If the single Staffing app
   grows unwieldy, the host may later split its tiles by audience.
10. **Entra ID is its own module** (`entra`), providing a many-provider
    `IdentityProvisioner` slot that Staff consumes; identity stays the owner of
    local accounts only, and must accept the SCIM enterprise extension inbound
    (§7.1). The setup is a wizard on the module's tile with the portal steps in the
    UI. Moving the OIDC and SCIM configuration from environment variables into the
    module is a later roadmap entry.
11. **The sequence**: Staff phase 1 (register, absence, the provisioning flag with
    the local-invitation path) → the Entra module → Staff phase 2 (competence) →
    Staffing.
12. **The account is created disabled ahead of the start.** Three settings replace
    the pair in item 8: `provision_account` (yes or no), `provision_from` (the day
    the account is enabled, default the employment start) and a company-level
    `create_before_days` (the account is created disabled that many days before;
    0 = on the day). Entra: created without licences, random never-shown password,
    `employeeHireDate` set so the tenant's own lifecycle workflows can run, first
    sign-in through a Temporary Access Pass or self-service reset explained in
    the wizard. Local identity: a new in-process path creates the user row disabled
    with no password at the lead date and delivers the invitation and enables on
    `provision_from`.
13. **At the end of employment the account is disabled, never deleted by
    default.** The disable step also strips licences and group memberships and
    revokes sessions, each a configurable step in the Entra module; a company
    setting `delete_external_after_days` defaults to never and respects the legal
    hold flag; identity never deletes a user Staff created (retention class C);
    rehire re-enables. Mailbox conversion and OneDrive transfer are a roadmap line.
14. **Groups are a Staff concept that providers translate.** The first Entra version
    adds the new user to groups. Staff owns `groups` (name, description, kind,
    members either listed or derived from a rule such as "everyone in department
    X"); a provider module declares the **traits** a group can carry for it (key,
    type, label, description, link to the vendor's documentation), Staff stores the
    trait values per group per provider and renders them dynamically in its UI, and
    the provider creates and maintains the external group — for Entra a security
    group or a Microsoft 365 group, optionally with a Team — and its membership.
    Membership is explicit (Staff decides), never Entra dynamic membership.
15. **Client secrets first, in the plural**: several secrets with their expiry
    dates, the newest valid one used, warnings ahead of expiry, rotation by adding
    then removing. Certificate and federated credential follow later.
16. **The owner provides an Entra test tenant**; what it needs is listed in §11.
17. **OIDC and SCIM configuration moves from environment variables to the
    database in one go** (nothing is in production). The boundary-respecting split:
    identity owns an SSO-and-SCIM settings table and the endpoints that edit it,
    read at request time through a short cache the way maintenance mode is; the
    Entra module owns the Graph connection; the host renders both on one Entra
    tile, so the operator sees one place and the backend keeps its boundaries.
    Google SSO settings live in the same identity table.
18. **The leave date is written to Entra** in the first version, with the extra
    permission.
19. **Field mapping is explicit and visible.** Every Staff field that a provider
    writes, and the attribute it lands in for Entra and (later) Google, is a fixed
    table shown in the Staff UI on the person's directory-profile section, with a
    per-installation toggle per field (private contact details off by default).
    The fields providers expect — company or legal entity, department, job title,
    employee type, office location, work address, business and mobile phone, cost
    centre, division, manager, preferred language, country — are all editable in
    Staff by HR, so the directory never needs a second place to type.

## 11. What the Entra test tenant needs

For the tagged integration test (the Storecove sandbox pattern: everything runs
against an `httptest` fake, and one tagged test runs against the real thing when
its environment variables are set). A **dedicated** tenant, never the company's
own: the test creates, updates, disables and deletes users and groups.

- A tenant (a Microsoft 365 trial or a pay-as-you-go tenant is enough; the
  Microsoft 365 Developer Program sandbox now requires a Visual Studio Enterprise
  subscription — **UNCERTAIN**, verify before relying on it). Entra ID Free covers
  user and group creation through Graph; P1 is only needed if the tenant's own
  provisioning or dynamic groups are tested; Teams creation needs a licence for
  members to use the Team but not to create the group.
- An **app registration** with application (not delegated) permissions, admin
  consented: `User.ReadWrite.All`, `User-LifeCycleInfo.ReadWrite.All`,
  `User.RevokeSessions.All`, `LicenseAssignment.ReadWrite.All`,
  `Group.ReadWrite.All` (create groups and manage members), `GroupMember.ReadWrite.All`,
  `Team.Create` and `TeamSettings.ReadWrite.All` if Teams are created,
  `Domain.Read.All` to list verified domains in the wizard, `Organization.Read.All`
  for the connection test. Whether `User.EnableDisableAccount.All` is additionally
  required for `accountEnabled` is to be verified in the spec.
- Two **client secrets** on that registration (to exercise rotation), with their
  expiry dates.
- The tenant id, the client id, and the **verified domain** test UPNs are created
  under (the `onmicrosoft.com` default domain is fine).
- For the SSO and SCIM side later: an enterprise application configured for OIDC
  sign-in with a redirect to a reachable Vantigo (a tunnel for local runs), and
  its provisioning configured against Vantigo's SCIM endpoint with a bearer token.
- Environment variables for the tagged test, named like the Storecove ones:
  `ENTRA_TEST_TENANT_ID`, `ENTRA_TEST_CLIENT_ID`, `ENTRA_TEST_CLIENT_SECRET`,
  `ENTRA_TEST_DOMAIN`. The spec's plan will add a contributing page that repeats
  this list with the portal paths.
