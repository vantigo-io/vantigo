---
title: "Staff module"
description: "The workforce register: people, their field groups and who reads them, their history, employments with dated terms and the dates the a-melding derives from them, the § 14-6 check, managers and the org view, the sealed national identity number and bank account, the self-service link and My page, the document archive with its categories and retention classes, the personal-data exports, the register's import and export as CSV, the payroll export, the retention run and its deletion ledger, the erased state, the legal hold and the reassessment, the dashboard's figures and attention items and the thresholds, the settings, the absence types with their a-melding mapping and reporting rules, the holiday schemes and their conversion, the care-day fields, absences requested and registered, overlap, the holiday account with its adjustments and carry-over, the absence events and their classes, sick-leave cases with the arbeidsgiverperiode and their follow-up milestones, egenmelding under the policy and its withdrawal, care days, the company's structure, the business time zone, the permissions and the lock rules."
sources:
  - apps/server/internal/staff
  - apps/staff/frontend
  - openapi/staff.yaml
---
The Staff module is the company's register of the people who work for it: who they
are, how to reach them and their next of kin, their employments as Norwegian
arbeidsforhold with dated terms, the company's structure, a self-service link to their
user and the history of every change. It is a vertical-slice module inside the single
Vantigo binary (`apps/server/internal/staff`), owns the `staff` schema in the shared
PostgreSQL database, and serves `openapi/staff.yaml` under `/api/v1/staff`.

It depends on nobody but identity: it reads users through `contracts.UserDirectory`,
which identity always provides, sends its one mail through the platform's SMTP seam,
and keeps its documents' bytes in the platform's object store under its own scope,
`staff`. `MODULES=staff` alone is a valid installation. It provides no contract and fills
no slot; nothing in another module reads the register yet.

This page grows with the module. What is here today: the [permissions](#permissions),
the [settings](#settings), the [business time zone](#the-business-time-zone), the
[structure](#the-structure), [people](#people) — the record, the
[directory](#the-directory), the [field groups and audiences](#field-groups-and-audiences),
the [history](#the-history) and [deletion](#deleting-a-person) —
[employments](#employments) and their [terms](#terms), the
[derived dates](#the-derived-dates), the [§ 14-6 check](#the--14-6-check),
[managers and the org view](#managers-and-the-org-view), the
[sealed identifiers](#the-sealed-identifiers), the
[self-service link](#the-self-service-link), [My page](#my-page), the
[documents](#documents) and their [categories](#categories-and-classes),
[absence](#absence) — the [types](#absence-types), the [holiday schemes](#holiday-schemes) and the
[care-day fields](#the-care-day-fields) — the
[personal-data exports](#the-personal-data-exports), the register's
[import and export](#the-registers-import-and-export), the
[payroll export](#the-payroll-export), [retention](#retention) and its
[deletion ledger](#the-deletion-ledger), the [erased state](#the-erased-state),
the [legal hold](#the-legal-hold) and the [reassessment](#the-reassessment), the
dashboard's [figures and attention items](#stats-and-attention) and the
[thresholds](#the-thresholds), [how a refusal looks](#refusals-and-revisions), the [locks](#locks) and the [endpoints](#endpoints).

## The schema

One migration, `00042_staff_foundation.sql`, creates the whole of delivery A1's
schema at once, and a second, `00043_staff_records.sql`, adds A2's tables
(`documents`, `document_categories` and `object_deletions`, below). 00042's are
`people` (with every column the phase needs — the sealed identifiers, the care-day
inputs, the account flag, the legal hold and the erase stamps — so later deliveries
never add a column to it), `next_of_kin`, `person_roles`, `employments`,
`employment_terms`, the four structure tables, `identity_links`, `events` and
`settings`. No table has a CHECK constraint: the module's own validation owns the
domain rules and reports them per field. The exceptions to "the index is only a
backstop" are stated where they matter below: the employee number is unique however
it is cased (`ux_people_employee_number_upper`), a work email likewise where set, at
most one legal entity and one business unit is the default, and the settings table
holds exactly one row. The care-day inputs on a person and a terms row's pay components are
nullable with no default: NULL is "not recorded", and it is what the retention erase
writes.

Delivery A2's migration, `00043_staff_records.sql`, adds three tables and no column to
`people` or `settings`: `document_categories` (unique `key`; the six seeds),
`documents` (unique `object_key`; indexed by person and by category; a nullable
`employment_id`; no `deleted_at` — a document's row is deleted, never marked) and
`object_deletions`, the ledger of objects still to be removed from the store (unique
`object_key`; indexed by `next_attempt_at`). Every column of the tables a person's
erase reaches has a retention class, listed [under Retention](#the-classes-column-by-column).

Delivery B's migration is `00047_staff_absence.sql` (00046 is another delivery's;
goose applies versions as numbers, gaps and all). It adds `absence_types` (unique
`key`; the seeds), `holiday_schemes` (unique name however it is cased; at most one
default, `ux_holiday_schemes_default`; the statutory seed), `holiday_adjustments`
(indexed by person and year; at most one carry-over per person and year it comes
from), `sick_cases` (indexed by person and first day), `sick_case_milestones` (one
per kind per case) and `absences` (indexed by person and first day, by type, by case,
and the requested ones by a partial index); and four columns to tables 00042 and
00043 created: `employments.holiday_scheme_id` (with its index), `documents.case_id`
(with its index), `settings.auto_carry_over_since` — the one settings column B
adds after all, so the derived carry-over reaches no year before it was turned on —
and `people.self_cert_withdrawn_from`, the day HR withdrew the right to egenmelding,
from which it is refused.
Still no CHECK constraint.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `staff:access` | Use Staff | The app at all: your own record, absence, documents and export. **Every operation requires it.** Your absence arrives with delivery B. | no |
| `staff:view` | View the staff directory | Everyone's work details, employments and terms without pay, the structure, the [calendar](#the-calendar) with types hidden. | no |
| `staff:approve` | Decide absence | Decide anyone's absence, see types other than HR-only and open cases' dates, mark milestones. It [decides](#decisions) anyone's absence requests, is the [Approver audience](#field-groups-and-audiences), sees the [inbox](#the-approvals-inbox) and the [calendar](#the-calendar), and makes `GET /meta`'s `canDecide` true; the cases it opens arrive with B's later steps. | no |
| `staff:manage` | Manage staff | HR: people (not the sealed fields), employments and terms with pay, the structure, absence types and holiday schemes, the settings, documents other than health and their categories, the account flag, imports and exports, the retention run by hand, the thresholds' acknowledgement. The account flag arrives with C. | yes |
| `staff:personal-data` | Reveal and erase personal data | Reads every person's every group; reveals and sets the national identity number and the bank account; health documents and notes, HR-only absence types, anyone's personal-data export, legal holds, erasure. It changes nothing else of a person but documents — the rest is `staff:manage`'s. Erasure is the [retention run](#retention)'s, on its schedule (run by hand by `staff:manage`); the milestones' notes and the HR-only types arrive with B. | yes |

All five are in the category "Staff" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform
403.

`GET /meta` answers what the caller may do as capabilities — `canViewDirectory`
(`staff:view`), `canEdit` (`staff:manage`), `canRevealIdentifiers`
(`staff:personal-data`), `canSeePay` (either of the last two) and `canSeeHealth`
(`staff:personal-data`: everyone's health documents), `managesAnyone` (the caller's
linked person manages anyone today by the [one manager rule](#managers-and-the-org-view),
so a manager who has left manages nobody) and `canDecide` (`staff:approve`,
`staff:manage`, or `managesAnyone`: who may decide absence) — so the app never
re-derives a rule.

## Settings

One row, written by the migration. `GET /settings` and `PUT /settings` are
`staff:manage`'s. The contract exposes thirteen fields, a derived block and the
acknowledged thresholds; the account days on the row reach the contract with delivery
C.

| Field | Default | Rule |
| --- | --- | --- |
| `timeZone` | `Europe/Oslo` | An IANA zone this server and its database read the same way (below). |
| `employeeNumberPrefix` | empty | Up to 8 letters, digits or hyphens. |
| `nextEmployeeNumber` | 1 | 1 or more. With the prefix, at most 16 characters. |
| `pensionProvider` | — | Up to 200 characters; left out, cleared. |
| `injuryInsurer` | — | Up to 200 characters; left out, cleared. |
| `retentionGraceDays` | 90 | 0 to 3650 days from the last employment's end to the [D erase](#what-is-due); left out, kept. |
| `healthRetentionDays` | 365 | 30 to 3650 days from the last employment's end to the [health erase](#what-is-due); left out, kept. |
| `selfCertPolicy` | `minimum` | `minimum`, `extended` or `custom` (below); left out, kept. |
| `selfCertDaysPerOccurrence` | 3 | 3 to 16 calendar days one egenmelding may last; read under `custom` only; left out, kept. |
| `selfCertOccurrencesPerYear` | 4 | 4 to 52 occurrences in a rolling year; read under `custom` only; left out, kept. |
| `selfCertDaysPerYear` | 24 | 12 to 365 self-certified days in a rolling year; read under `custom` only; left out, kept. |
| `selfCertAfterMonths` | 2 | 0 to 2 months of employment before egenmelding may be used; left out, kept. |
| `autoCarryOver` | false | Carry holiday left at a year's end into the next as forced, without HR's action; left out, kept. |

**The self-certification policy.** A preset writes its own numbers, whatever the
request carries: `minimum` — the statutory one — 3 days an occurrence and 4
occurrences in a rolling year; `extended` 8 days an occurrence and 24 days in a
rolling year. The number a preset does not use keeps what it holds. `custom` takes the
three as sent, but never below the statutory minimum (ftrl. § 8-23: 3 days an
occurrence, 4 occurrences and 12 days in twelve months), which no agreement goes
below; the floors are judged whatever the policy. Egenmelding keys on the policy's
name: under `minimum` and `extended` it reads the preset's numbers, and the stored
counters are read under `custom` only — it is judged when absence arrives.

**Automatic carry-over.** Turning `autoCarryOver` on — off before, on now — records
today, in the business zone, as the read-only `autoCarryOverSince`; turning it off
keeps that day, and turning it on again moves it. The carry-over it derives reaches no
year before that day. `autoCarryOverSince` is absent until it was first turned on.

`GET /settings` and the answer of `PUT /settings` also carry the read-only
[`retention` block](#the-settings-and-the-retention-block), derived from the deletion
ledger and the history: `pendingDeletions`, `failingDeletions`, `oldestPendingSince`,
`lastErasedAt` — and the read-only `thresholdsAcknowledged`, when each
[threshold](#the-thresholds) was acknowledged (`{"5": at, "30": at, "50": at, "otp": at}`,
a threshold not acknowledged absent), which `PUT /settings` neither takes nor changes.

A generated employee number is the prefix and the next number, unpadded: `EMP-` and 7
make `EMP-7`. An employee number is at most 16 characters (Entra's `employeeId`
bound), so a prefix and a next number that together make a longer one are refused with
`employee_number_too_long`, named on both fields. A company that wants `EMP007` sets
the prefix to `EMP00` while it lasts.

Finding the first free number probes the numbers from the next one on against the
people table, so a generation reads the table — O(N) in the people, which at a
company's size is nothing and is accepted as such; an employment's `-n` is found the
same way among the employments.

`PUT /settings` is a full replace guarded by `If-Match` (below) — but for the two
retention periods, the self-certification policy and the carry-over, which a body
that leaves them out keeps. Every bad field is a 400
naming it (a period out of its bounds is a plain field error); the whole request is
judged before anything is written.

## The business time zone

Every date in this module — a start, an end, "today" — is a calendar day in the
installation's business time zone. "Today" is computed once per request, in Go, as the
zone's calendar day of the server clock, and handed to SQL as a date parameter; SQL's
`current_date` is never used, since it is the database session's day in the database's
own zone. `GET /meta` answers the zone and `today`, so the app's notion of today is the
server's: at 22:30 UTC on 30 September the Oslo day is already 1 October.

A zone is checked twice when it changes. Go must know the name (`Local` and an empty
name are refused: they say nothing about the company's calendar), and then the
database is asked what it means by the same name, at a winter and a summer instant of
the current year, and the two answers are compared. They can differ: PostgreSQL
resolves its abbreviations before its zone names, so `CET` is a fixed +01:00 to it and
a zone with summer time to Go. Such a name is refused on `timeZone` — use a city zone
such as `Europe/Oslo`. A row that somehow holds a name Go cannot load (written by hand)
is read as UTC rather than failing every request; setting the zone again is the way
back.

## The structure

Four small tables, not an org tree (design S6):

- **Legal entities** — the juridisk enhet, with its organisation number.
- **Business units** — the virksomhet or underenhet under a legal entity, with an
  organisation number of its own. The a-melding reports per business unit, and an
  employment names one.
- **Departments** — a code, a name, an optional manager (a person), an optional
  parent department, a cost centre and a division. A department's manager is the
  default manager of the people whose terms name the department and no manager of
  their own.
- **Locations** — the arbeidssted, with an address and a country (Norway when none is
  given).

Listing them (`GET /legal-entities`, `/business-units`, `/departments`, `/locations`)
needs `staff:view` or `staff:manage` — an HR role without the directory still lists
people and names their units and departments — and anyone else gets the uniform 403,
judged in the module as on `GET /people`; adding (`POST`), replacing (`PUT /{id}`) and
archiving (`POST /{id}/archive`) need `staff:manage`. `GET /meta` carries all four
lists for a caller holding either, read in one snapshot.

**Nothing is deleted.** A row no longer in use is archived: it stays on every row that
names it and leaves the pickers. Every list answers archived rows too, with
`archivedAt`, so a stored reference can still be named. Archiving an archived row
changes nothing. A write that newly names an archived row — a business unit put under
an archived legal entity, a department given an archived parent — is refused
`structure_archived` (409); a row that already named it before the write keeps it, so
a department under an archived parent can still be renamed.

**Exactly one default** legal entity and exactly one default business unit (the unit
an employment is put in when it names none). The first row created becomes the
default. A `POST` or `PUT` with `isDefault: true` makes that row the default and takes
the flag from the previous one, in one transaction. `isDefault: false` on the default
is refused `structure_default` (409) — move it by making another row the default —
and leaving `isDefault` out changes nothing about the flag. The default is never
archived (`structure_default`), and an archived row is never made the default
(`structure_archived`). Nor is the legal entity the default business unit sits under
archived (`structure_default` — make a unit under another legal entity the default
first), and a unit under an archived legal entity is never made the default
(`structure_archived`), so the unit an employment is put in by default never hangs
from an archived legal entity. Each table's default is serialised by a transaction advisory
lock of its own, and a partial unique index on `is_default` is the backstop.

**Organisation numbers** are nine digits with a valid MOD11 check digit (weights
3 2 7 6 5 4 3 2; a remainder that would make the check digit 10 is invalid); the
spaces a person types between the groups are taken out. A bad one is
`org_number_invalid` on `orgNumber` (400). A number is unique within its table; a taken
one is a field error on `orgNumber`. The number is optional.

**Department codes** are 1–20 letters, digits or hyphens, unique however they are
cased. A department's **parent** must exist, and must be neither the department itself
nor any department below it: that is `department_cycle` (409). The check walks the
parent chain upward from the proposed parent with a visited set, so even a graph that
already holds a cycle (written past the module) answers rather than looping. The
manager must be an existing person.

Names are 1–200 characters; a cost centre up to 40, a division up to 100; a street up
to 200, a postal code up to 16, a city up to 100; a country is an ISO 3166-1 alpha-2
code, stored upper-case. An optional text sent as only whitespace is refused — leave
the field out to clear it.

## People

A person (`staff.people`) is a human the company has a relationship with, stable
across hires and accounts. HR (`staff:manage`) creates them with `POST /people`,
replaces their editable fields with `PUT /people/{id}` and deletes one created in error
with `DELETE /people/{id}`; who reads a person, and which of their fields, is the
[audience](#field-groups-and-audiences).

**The employee number** is permanent and short: 1–16 letters, digits or hyphens,
unique however it is cased (`emp-7` and `EMP-7` are one number). A create either gives
one — kept as typed — or leaves it out, and the module generates the settings' prefix
and next number, unpadded, under the settings row's lock, so two people created at once
get two numbers. The sequence skips forward past any number a person already has
(however cased), so a number HR assigned by hand never collides with a generated one,
and moves the next number on past the one used. A number longer than 16 characters is
`employee_number_too_long` on `employeeNumber` (400), on both paths — a typed one, and
a generation the skip carries past 16. A number another person has is
`employee_number_taken` (400), including when two writes race and the unique index
settles it. The number never changes after the create: a `PUT` leaves it out or sends
it as it is.

**The display name** is derived once, at the create, from the given and the family
name, unless one is given; after that it is HR's to edit, and a `PUT` that leaves it
out keeps it. The person never edits it.

**The fields** a request may carry (`StaffPersonRequest`):

| Field | Group | Rule |
| --- | --- | --- |
| `employeeNumber` | Work | Above. |
| `givenName`, `familyName` | Work | Required, 1–100 characters. |
| `displayName` | Work | 1–200 characters. |
| `workEmail` | Work | An address alone (no display name), up to 254 characters, stored lower-cased; unique however cased — `work_email_taken` (400). |
| `phoneWork` | Work | Up to 32 characters. |
| `workCountry` | Work | ISO 3166-1 alpha-2, stored upper-case; `NO` when left out. |
| `preferredLanguage` | Work | `en` or `nb`; `nb` when left out. |
| `birthDate`, `gender` | Private | A date; `female`, `male`, `other` or `unspecified`. |
| `phoneMobile`, `phonePrivate` | Private | Up to 32 characters. |
| `privateEmail` | Private | As the work email, without the uniqueness. |
| `street`, `postalCode`, `city`, `country` | Private | The home address: 200, 16 and 100 characters, an alpha-2 country. |
| `notes` | HR notes | Up to 2000 characters. |

Text is trimmed; an optional field sent as only whitespace is refused — leave it out
to clear it. A `PUT` is a full replace: an optional field left out is cleared. A
replace that changes nothing writes nothing: the revision stays and no event is
recorded.

The national identity number, the bank account, the account flags and the self-service
link are **not** on the request — they are written through their own operations under
their own permissions ([the sealed identifiers](#the-sealed-identifiers),
[the self-service link](#the-self-service-link)). The schema is closed (`additionalProperties: false`), and the
module enforces it: a property it does not name is a 400 naming that property, and
nothing is written, so a client that sends `nationalId` learns it was not stored. The
check holds whatever the `Content-Type` (the body is read as JSON either way); a
multipart or octet-stream body, which the module never reads ahead of the handler, is
refused on such a request. The module refuses to mount if the contract loses the schema
or opens it.

**Next of kin** (`GET|PUT /people/{id}/next-of-kin`) are at most three, each a name
(1–200), a relationship (`spouse`, `partner`, `parent`, `child`, `sibling`, `other`),
an optional phone (32) and note (500), listed in the order HR gives them. **Roles**
(`GET|PUT /people/{id}/roles`) are `verneombud`, `amu_member` or `hms_responsible`,
each with optional `fromDate` and `toDate` (not before the from date), at most twenty.
Both are replaced whole by `staff:manage`; an empty list removes them. Both are guarded
by **the person's revision**: the `PUT` requires `If-Match` with it, a replace that
changes the list moves it on, and both the `GET` and the `PUT` answer the list with the
person's current revision. A field error names the element: `nextOfKin[1].name`,
`roles[0].toDate`.

### The directory

`GET /people` lists people by display name with the work group only, paged (`page`,
`pageSize` up to 100). `q` matches each of its words, however cased, against the
names, the employee number and the work email — never a private field. Every row, and
`GET /people/{id}`, carries the person's place today:

- **`status`** — where they stand on the business zone's day: `active` (an employment
  has started and not ended; the end day itself still counts), `upcoming` with the
  earliest later start as `date`, `ended` with the latest end as `date` (every
  employment ended before today), or `none` (no employment yet).
- **`manager`** and **`department`** — `{id, displayName, employeeNumber}` and `{id,
  code, name}` from the terms in force today on the person's earliest employment in
  force: the manager is the one who manages the person today by [the manager
  rule](#managers-and-the-org-view) — the one the terms name when they are in employment
  today, else the department's or one above it — so always someone active today;
  absent when there is none.

Who lists whom:

- **`staff:manage`** lists by `status`: `current` (the default — active, upcoming and
  no employment yet, so a person created in the first step of a two-step hire stays
  findable), `ended`, or `all`. Any other value is a 400.
- **`staff:view`** without it lists the people **active today** only, whatever
  `status` says (`status=ended` lists nobody).
- Anyone else is the uniform 403.

An [erased](#the-erased-state) person is listed to `staff:manage` as any leaver is —
under `ended` and `all`, their `status` `ended` — with `erasedAt` beside it, the hold
group's; there is no state of its own.

`unitId`, `departmentId` and `locationId` keep the people with an employment not ended
before today in that business unit whose terms name that department and that location —
the row in force today, or for an employment that starts later its first row, so HR
finds next month's starter in their department — every filter given, on the same
employment. "Today" is the business time
zone's calendar day, computed once per request from the clock and handed to the
database as a date, so the list turns over at midnight in Oslo, not in UTC, and does
so on the right hour on the days the clocks change.

### Field groups and audiences

Every field of a person belongs to one **field group**, held in one static table in
`shaping.go`; every response is built by asking that table, field by field, whether the
caller's audience reads the field's group. A group the audience may not read is absent
from the response — never null.

| Group | Fields | Self | Colleague (`staff:view`) | Manager | Approver (`staff:approve`) | `staff:manage` | `staff:personal-data` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Work | name, employee number, work email and phone, work country, language, roles; the person's place today — `manager`, `department`, `title`, `businessUnit`, `location` | ✓ | ✓ | ✓ | ✓ | ✎ | ✓ |
| Employment dates | `status`; an employment's `startDate`, `endDate`, `fteChangedAt`; a terms row's `validFrom`, `validTo`, `employmentType`, `ftePercent` | ✓ | ✓ | ✓ | ✓ | ✎ | ✓ |
| Employment | every other field of an employment and its terms but the pay | ✓ | — | ✓ | ✓ | ✎ | ✓ |
| Pay | `remunerationType`, `payComponents`, `payMethod`, `paydayText`, `seniorityDate`, `payGrade`, `salaryChangedAt` | ✓ | — | — | — | ✎ | ✓ |
| Private | birth date, gender, mobile, private phone and email, home address, next of kin | ✓ | — | — | — | ✎ | ✓ |
| Care days | `childrenUnder12`, `soleCarer`, `chronicallyIllChildren` ([the care-day fields](#the-care-day-fields)) | ✓ | — | — | — | ✎ | ✓ |
| HR notes | `notes`; a terms row's `note` | — | — | — | — | ✎ | ✓ |
| National identity number | `nationalId { kind, onFile }` | ✓ | — | — | — | ✓ | ✎ reveal and set |
| Bank account | `bankAccount { kind, last4 }` | ✓ | — | — | — | ✓ | ✎ reveal and set |
| Link | `user { id, displayName, active }` | ✓ | — | — | — | ✓ | ✓ |
| Hold | `legalHold { held, reason, at }`, `erasedAt`, `reassessedAt` | — | — | — | — | ✓ | ✎ hold and reassess |

- **Self** is the user a person's self-service link names, whatever state the link is
  in. **Colleague** is `staff:view`, and only for a person **active today**: an
  unannounced hire, a leaver and someone with no employment stay out of the colleague
  directory, and since ids are sequential they answer the unknown id's 404.
  **Manager** is a caller whose self-service link names a manager of the person
  today: the manager the terms in force today name on one of the person's employments,
  or the department's when they name none (see [managers](#managers-and-the-org-view));
  concurrent employments may give two. It needs no permission. A caller holding several
  is every one of them at once and reads what any may. **Approver** is `staff:approve`
  (delivery B, plan reading 18): every person's work and non-pay employment, and their
  absences with every type but an HR-only one (read as leave) and the detail — no
  history, no private fields, no pay, no documents.
- **The place today** — the manager, the department, the title and the location of the
  terms in force today, and their employment's business unit, each as an id and a
  name — is the work group's, everyone's who sees the person. A terms row's own
  `title`, `departmentId` and `locationId` stay the employment group's: a colleague
  reads today's title on the person and the list, never the history of titles.
- **`staff:personal-data` alone writes nothing but the sealed fields**: it reads every
  group and reveals and sets the national identity number and the bank account; a
  change to anything else is `staff:manage`'s (the [operations](#endpoints)).
- **The national identity number** is never answered as a digit: only its kind and
  whether one is on file, since the last digits with the birth date reconstruct the
  whole number. **The bank account** is its kind and last four digits.
- **The user** is read from the user directory after the person's transaction has
  ended; a user the directory no longer knows is "Unknown user", not active.
- **Anyone else** — signed in with `staff:access` but none of these — gets a 404 with
  an empty body, byte for byte what an unknown id gets, on the person and on every
  sub-resource. A sub-resource whose group the audience does not read answers the same
  404: a colleague reads a person's roles but not their next of kin.

Each person carries `capabilities`: `canEdit` and `canManageAccount` (`staff:manage`),
`canRevealIdentifiers` (`staff:personal-data`), `canSeePay` (the person themselves,
`staff:manage` or `staff:personal-data`), `canSeeHealth` (the person themselves or
`staff:personal-data`: the person's health documents) and `canHold`
(`staff:personal-data`: the [legal hold](#the-legal-hold), the
[reassessment](#the-reassessment) and the [complete export](#the-personal-data-exports)).

The **hold** group is about the record's retention rather than the person: whether a
legal hold stands, with its reason and when it was set, when the retention run erased
the person and when they were reassessed. It is HR's alone — `erasedAt` and
`reassessedAt` are absent until set, `legalHold` is `{held: false}` when there is none.

### The history

Every change to a person is an **event** (`staff.events`), written in the same
transaction as the change, attributed to the actor as the user directory named them
just before (`actor { kind, displayName, userId }`; a user it no longer knows is
"Unknown user"). The kinds today:

Delivery B's absence and adjustment events carry their classes per field and are
shaped by the absence groups; they are described under
[The absence events](#the-absence-events).

| Kind | Payload |
| --- | --- |
| `person_created` | `fields`: each field set, with its value as `to`. |
| `person_updated` | `fields`: each field that changed, `from` and `to` (either absent when the field was empty). |
| `next_of_kin_updated` | `fields.nextOfKin`: the list before and after. |
| `roles_updated` | `fields.roles`: the list before and after. |
| `person_deleted` | Installation-level (no person): `employeeNumber` only. |
| `national_id_set` | `fields.nationalId`: `{kind, country}` before and after — never a digit. |
| `national_id_revealed` | Empty; the actor is the viewer. |
| `bank_account_set` | `fields.bankAccount`: `{kind, last4}` before and after. |
| `bank_account_revealed` | Empty; the actor is the viewer. |
| `user_linked` | `fields.user`: the user linked before, if any, and now, each `{id, displayName}`. |
| `user_unlinked` | `fields.user`: the user that was linked (`{id, displayName}`). |
| `employment_created` | `employmentId`; `fields`: each field of the employment set. |
| `employment_updated` | `employmentId`; `fields`: each field that changed. |
| `employment_ended` | `employmentId`; `fields.endDate`, `fields.endReason`. |
| `employment_reopened` | `employmentId`; `fields.endDate`, `fields.endReason` from their values to nothing. |
| `terms_added` | `employmentId`, `termsId`; `fields`: each field of the new row. |
| `terms_corrected` | `employmentId`, `termsId`; `fields`: each field of the row that changed, before and after. |
| `terms_deleted` | `employmentId`, `termsId`; `fields`: each field of the deleted row, as `from`. |
| `document_added` | `documentId`; `fields`: `category` (the key), `fileName`, `validTo`, `signedAt` as `to`. |
| `document_updated` | `documentId`; `fields`: each of the four that changed, before and after. |
| `document_removed` | `documentId`; `fields`: the four as `from`. |
| `personal_data_exported` | Empty; the actor is the viewer of the [complete export](#the-personal-data-exports). |
| `legal_hold_set` | `fields.legalHold` (`false` to `true`) and `fields.legalHoldReason`, each that changed, before and after. |
| `legal_hold_lifted` | `fields.legalHold` (`true` to `false`), `fields.legalHoldReason` as `from`. |
| `reassessed` | `fields.reassessedAt` as `to`. |
| `person_rehired` | `fields.erasedAt` and, when set, `fields.reassessedAt`, each as `from`. |
| `people_imported` | Installation-level (no person): `rows`, `created`, `updated`, `failed` of a real [import](#the-registers-import-and-export); each row's own events name the people it wrote. |
| `people_exported` | Installation-level (no person): `rows`, the people the [export](#the-registers-import-and-export) wrote. |
| `payroll_exported` | Installation-level (no person): `month`, `format`, `rows` and `sha256` (hex) of the [payroll file](#the-payroll-export) answered; no personal data, so no payload class. |
| `threshold_acknowledged` | Installation-level (no person): `key`, the [threshold](#the-thresholds) acknowledged (`5`, `30`, `50` or `otp`); the actor is the caller; no payload class. |
| `erased` | No fields: `kinds`, `[{kind, count}]` — what one [erase](#what-an-erase-does) removed per kind (`D`, `health`, `A`), read by `staff:manage` and `staff:personal-data`, the `health` entry by `staff:personal-data` alone; listed as the hold's kinds are; the actor is the installation's `system` for the worker, the caller for a run by hand; no payload class. |

`GET /people/{id}/events` lists a person's history, newest first, paged. It is read by
the person themselves, a manager of theirs today, `staff:manage` and
`staff:personal-data`; to anyone else — a colleague included — it is the unknown id's
404. Each payload is **shaped per field** by the same table: a field of a group the
caller may not read shows the string `"changed"` in place of its values — except a
field of the **pay** or of **HR's notes**, which is left out altogether: the day a pay
field changed is itself a pay date (the `salaryChangedAt` a manager does not read), and
HR's notes are HR's alone. So a manager reads a birth-date change as `"changed"`, a
work-email change with its values, and a correction of the pay alone as a
`terms_corrected` event whose `fields` is empty: the event is still listed, without its
fields, and the app says its details are not visible to the reader. A change to HR's
notes alone is likewise listed to the person without its field, and HR reads it as
`{"notes": {"from": …, "to": …}}`. An employment's and
a terms row's `employmentId` and `termsId` are kept for everyone who reads the
history. A sealed value is never in a payload. Each event also stores the retention
classes its payload touches (`payload_classes`), which the erase uses; a document's
event takes its classes from the category it was filed under (before and after, for a
move). A value the [retention run](#what-an-erase-does) has erased reads `"changed"`
to everyone, HR included; an `erased` event's `kinds` are read by the hold group —
`staff:manage` and `staff:personal-data` — its `health` entry by the health group's HR
alone, `staff:personal-data`, so HR without it does not learn that there was health
content to erase; to anyone else it is an empty payload.

A **document's event** is shaped by one group for the whole event, not per field: the
**documents** group (the person, `staff:manage`, `staff:personal-data`), or — when its
classes hold E, a health document's — the **health** group (the person,
`staff:personal-data`). A manager reads that a document was added, changed or removed,
with its `documentId` and each field `"changed"`. A **health event** — any event whose
payload touches class E — is left out of the list, and out of its `totalCount`, for an
audience without the health group, HR without `staff:personal-data` included: they do
not learn that one happened.

The **hold's kinds** — `legal_hold_set`, `legal_hold_lifted`, `reassessed`,
`person_rehired`, `personal_data_exported` and `erased` — are left out of the list, and
out of its `totalCount`, for an audience that is neither the person nor HR: a manager
does not learn that a hold was set, the person's data exported or the record erased.
The person themselves is shown them — they have a right to know a hold is on them and
that their data was exported or erased — as the kind, each field `"changed"` (an
`erased` event's payload empty); `staff:manage` and `staff:personal-data` read the
values.

### Deleting a person

`DELETE /people/{id}` (`staff:manage`) is the hard delete of a person **created in
error**: one with no employment, no user link, no document, no absence and no holiday
adjustment, and named as nobody's manager — neither a department's nor on anyone's
terms — and not under a [legal hold](#the-legal-hold). An absence or an adjustment is
the history of an employment, which the delete would take with the person. An [erased](#the-erased-state) person is
`person_erased` (409) and a held one `legal_hold` (409), judged before what names
them; anything else is `person_in_use` (409), whose
detail says what names the person — "has documents", "has absences" and "has holiday
adjustments" among the reasons; the person stays. The delete takes the person's row
and then the manager-graph lock before it judges, so no write can start naming the
person between the check and the delete, and a foreign key that refuses anyway is still
`person_in_use`. In one transaction it removes the next of kin, the roles and the
person's own history — the one exception to the history's append-only rule — and
records an installation-level `person_deleted` event carrying the employee number and
nothing else. It answers 204.

## Employments

An employment (`staff.employments`) is one arbeidsforhold — the a-melding's unit — of
a person: its own number, a business unit, a start, and an end with its reason. A
person may hold several, one after the other (a rehire) or at once (two part-time
positions); the register does not refuse an overlap.

- **`POST /people/{id}/employments`** (`staff:manage`) creates one with its **first
  terms row** nested as `terms`, which takes effect on the start: `terms.validFrom`
  is left out or equals `startDate` — otherwise `terms_first_row_start` (409). The
  business unit is the [default one](#the-structure) when `businessUnitId` is left
  out; a named one must exist and not be archived (`structure_archived`, 409). So
  must its [holiday scheme](#holiday-schemes), `holidaySchemeId`; left out, the
  default's. `relationshipType` is `ordinary` (the default), `maritime`, `freelancer` or
  `pension_or_other`. The creation is an `employment_created` and a `terms_added`
  event.
- **The employment number** — the a-melding's `arbeidsforholdID` — is 1–40 letters,
  digits or hyphens, unique however it is cased, and never changes. Typed, it is kept
  as typed; left out, it is generated as the person's employee number, a hyphen and the
  first `n` from 1 that no employment has — so `E-7-1`, `E-7-2`, skipping past a number
  typed by hand — under the person's lock. A number another employment has is
  `employment_number_taken` (400), including when the unique index settles a race.
- **`PUT /employments/{id}`** (`staff:manage`, `If-Match`) replaces `startDate`,
  `relationshipType`, `businessUnitId`, `contractSignedAt` and `holidaySchemeId` (the
  last two left out, cleared — a scheme cleared is the default's). A new
  start moves the first terms row with it, and is `terms_before_start` (409) when a
  later row would then lie before it. Every row's probation is judged again against the
  new start: one ending more than six months (or half a temporary term) after it is
  `probation_too_long` (400) on `startDate`, one ending before it a field error. The change is an `employment_updated` event.
- **`POST /employments/{id}/end`** (`staff:manage`) takes `endDate` — the last day
  worked, on or after the start, and allowed in the future — and `endReason`, one of the
  a-melding's six: `employee_resigned`, `employer_terminated`, `contract_ended`,
  `wrongly_reported`, `internal_change`, `payroll_system_change`. Without the reason it
  is `end_reason_required` (400); a terms row dated after the end is `terms_after_end`
  (409). Ending an ended employment moves its end; a later end brings the terms after
  the old one back into force, so it is judged on the [manager graph](#managers-and-the-org-view)
  from the day after the old end. It is an `employment_ended` event.
- **`POST /employments/{id}/reopen`** (`staff:manage`) takes the end and its reason
  away, for an end recorded in error — an `employment_reopened` event; an employment
  with no end is answered as it is.
- **`GET /people/{id}/employments`** and **`GET /employments/{id}`** answer them as the
  [audience](#field-groups-and-audiences) reads them: a colleague reads the dates, a
  manager every field but `salaryChangedAt`.

The six end reasons, the six working-hours schemes and the five appointment forms map
to Skatteetaten's a-melding codes in `values.go`, pinned by a table test for the
[payroll export](#the-payroll-export):

| Scheme | a-melding | End reason | a-melding |
| --- | --- | --- | --- |
| `not_shift` | `ikkeSkift` | `employee_resigned` | `arbeidstakerHarSagtOppSelv` |
| `shift_365` | `skift365` | `employer_terminated` | `arbeidsgiverHarSagtOppArbeidstaker` |
| `offshore_336` | `offshore336` | `contract_ended` | `kontraktEngasjementEllerVikariatErUtloept` |
| `continuous_336` | `helkontinuerligSkiftOgAndreOrdninger336` | `wrongly_reported` | `arbeidsforholdetSkulleAldriVaertRapportert` |
| `round_the_clock_355` | `doegnkontinuerligSkiftOgTurnus355` | `internal_change` | `endringIOrganisasjonsstrukturEllerByttetJobbInternt` |
| `other_shift` | `andreSkift` | `payroll_system_change` | `byttetLoenssystemEllerRegnskapsfoerer` |

| Appointment form | a-melding |
| --- | --- |
| `fast` | `fast` |
| `midlertidig` | `midlertidig` |
| `fast_utleid` | `fastAnsattUtleid` |
| `midlertidig_utleid` | `midlertidigAnsattUtleid` |
| `midlertidig_tilkallingsvikar` | `midlertidigAnsattSomTilkallingsvikar` |

The hired-out and on-call forms have had codes of their own in the a-melding since
2025. `fast` and `midlertidig` are the a-melding's codes; the strings of the three
2025 forms (`fastAnsattUtleid`, `midlertidigAnsattUtleid`,
`midlertidigAnsattSomTilkallingsvikar`) are **unverified against the kodeliste** —
[Skatteetaten's guidance](https://www.skatteetaten.no/bedrift-og-organisasjon/arbeidsgiver/a-meldingen/veiledning/arbeidsforholdet/opplysninger-om-arbeidsforholdet/ansettelsesform) names the five forms in prose only — so check them
before wiring a payroll system to them. The remuneration type has none: Skatteetaten
says [not to report avlønningstype from 2019](https://www.skatteetaten.no/en/business-and-organisation/employer/the-a-melding/the-a-melding-guide/employment/information-on-employment/remuneration-type/), and the payroll export writes it as
stored.

### Terms

An employment's **terms** (`staff.employment_terms`) are dated rows, one per change,
each in force from its `validFrom` until the day before the next row's (`validTo`,
absent on the latest). They carry what the a-melding reports and what aml. § 14-6 asks
a contract to hold: the type, the ground and expected end of a temporary employment,
the appointment form, the percentage, the working-hours scheme and the hours of a full
position, the occupation code (STYRK-08, seven digits), the title, the department, the
location, the manager, the working time, the breaks, the special arrangement, the
overtime rule, the probation and its extension, the notice periods, the tariff, the
holiday terms, the competence development, the social security and pension, the hirer
when hired out — and the pay: the remuneration type, the pay components (at most
twenty `{name, amount, currency, period}`), the pay method and the payday, the
seniority date and the pay grade.

- **`POST /employments/{id}/terms`** (`staff:manage`) adds a row on `validFrom`
  (required). A field left out takes its value from the row in force before that day; a
  field sent as `null` is cleared. The temporary employment's ground and expected end are
  dropped when the row turns permanent and leaves them out; the appointment form follows
  the type when it is left out and the one before does not go with it; the hours of a
  full position follow a new scheme when they are left out. It is a `terms_added`
  event.
- **`PUT /terms/{id}`** (`staff:manage`, `If-Match` with the row's revision) replaces
  a row whole — a field left out is cleared. It replaces the latest row, or any row with
  `correction: true`, HR's correction of a past row; otherwise `terms_not_latest`
  (409). The first row stays dated the start (`terms_first_row_start`, 409; change the
  employment's start to move it). Either way it is a `terms_corrected` event with every
  changed field before and after.
- **`DELETE /terms/{id}`** (`staff:manage`) deletes the latest row only
  (`terms_not_latest` otherwise), so the one before is in force again — never the first
  (`terms_first_row_start`). It is a `terms_deleted` event.
- **`GET /employments/{id}/terms`** answers the chain, oldest first, as the audience
  reads it: the person and HR every field, a manager every field but the pay, a
  colleague `validFrom`, `validTo`, `employmentType` and `ftePercent` only; a row's
  `note` is HR's.

A row is refused when:

| Rule | Code | Status |
| --- | --- | --- |
| `employmentType` (`fast`, `midlertidig`) or `ftePercent` is missing after the defaults — the law presumes the employee's claim when they are, so the module never assumes one; `errors` names each | `terms_required_fields` | 400 |
| `midlertidig` without `temporaryGround` (§ 14-9 (2) `a`–`e`, or `tariff`) | `temporary_ground_required` | 400 |
| `appointmentForm` does not go with the type: `fast` and `fast_utleid` with `fast`; `midlertidig`, `midlertidig_utleid` and `midlertidig_tilkallingsvikar` with `midlertidig` | `appointment_form_mismatch` | 400 |
| the probation ends more than six months after the employment's start (the same day six months on, or that month's last day), or for a temporary employment with an expected end more than half the expected term after it; an extension (`probationExtendedTo`) without `probationExtensionAgreed`, without a reason, without a probation, not after it, or longer than the probation itself | `probation_too_long` | 400 |
| the hours of a full position break [the a-melding's rule](#the-hours-rule) | `full_time_hours_mismatch` | 400 |
| another row of the employment has the day | `terms_valid_from_taken` | 409 |
| the day is before the employment's start | `terms_before_start` | 409 |
| the day is after the employment's end | `terms_after_end` | 409 |
| the first row would not be dated the start | `terms_first_row_start` | 409 |
| the manager is the person | `self_manager` | 409 |
| the manager graph would loop | `manager_cycle` | 409 |
| a department or location newly named is archived | `structure_archived` | 409 |

`ftePercent` is more than 0 and at most 100 with at most two decimals; a pay amount 0
or more with at most two decimals; a currency three letters; a notice needs its unit
(`days`, `weeks`, `months`).

### The hours rule

`fullTimeHoursPerWeek` defaults to the scheme's own when it is left out, and is checked
against the a-melding's rule for the scheme:

| Scheme | Default | Allowed |
| --- | --- | --- |
| `not_shift` | 37.5 | any (more than 0, at most 168) |
| `shift_365` | 36.5 | exactly 36.5 |
| `offshore_336` | 33.6 | 28 to 33.6 |
| `continuous_336` | 33.6 | 28 to 33.6 |
| `round_the_clock_355` | 35.5 | exactly 35.5 |
| `other_shift` | 37.5 | any (more than 0, at most 168) |

### The derived dates

The a-melding wants two dates that are never typed; an employment answers them from
its terms, judged on today held inside the employment (its start before it has started,
its end once it has ended):

- **`fteChangedAt`** — the day the percentage in force took effect: the earliest row of
  the unbroken run of rows with that percentage, so a row that changed only the title
  leaves it where it was.
- **`salaryChangedAt`** — the same for the pay, judged on the pay components as a list
  sorted by name and amount and compared on those two alone: a reordered list, or a
  component whose currency or period alone changed, is the same pay; a renamed component
  or a new amount is a change. No components and an empty list are the same.

A single-row chain answers that row's `validFrom` for both. The row in force on a day
is the latest whose `validFrom` is on or before it, within the employment's days — a
row dated later, a change agreed ahead, is not yet in force.

### The § 14-6 check

`GET /employments/{id}/contract-check` answers the seventeen items aml. § 14-6 (1) a–q
asks a written contract to hold, each `{item, state, source}` — `present`, `missing` or
`not_applicable` — judged on the employment and its row in force today (its first row
before the start, its last at the end), and `contractSigned` from the employment's
`contractSignedAt`. It informs; it never blocks a write.

| Item | Source | Applies |
| --- | --- | --- |
| a parties | the person and the business unit | always (present) |
| b workplace | `locationId` | always |
| c title or description | `title` or `occupationCode` | always |
| d start | the employment's `startDate` | always |
| e duration and ground | `expectedEndDate` and `temporaryGround` | only when `midlertidig` |
| f probation | `probationEndDate` | only when a probation is set |
| g holiday | `holidayTermsText` | always |
| h notice | `noticeEmployee`, `noticeEmployer` and `noticeUnit` | always |
| i pay | `payComponents`, `payMethod` and `paydayText` — all three | always, for a reader of the pay |
| j working time | `workingTimeText` | always |
| k breaks | `breakMinutes` | always |
| l special arrangement | `specialHoursText` | not when the scheme is `not_shift` and the text is empty |
| m overtime | `overtimeRuleText` | always |
| n tariff | `tariffReference` | not when it is empty and the employment is not hired out |
| o hirer | `hiredOutTo` | only when the appointment form is `*_utleid` |
| p competence development | `competenceDevelopmentText` | always |
| q social security and pension | `socialSecurityText`, or the settings' `pensionProvider` | always |

Whether an item is present says something about the terms, so the check is read by
those who read the employment group — the person, a manager of theirs and HR; a
colleague gets the unknown id's 404. Item i says whether the pay is recorded, so a
reader who does not read the pay — a manager — gets the list **without item i**, not
marked not applicable: sixteen items.

## Managers and the org view

A person's **manager on a day** is, for each of their employments in force that day,
the manager the terms in force name when that manager is in employment that day; else
the manager of the terms' department — unless that is the person themselves, nobody, or
someone not in employment that day, when the parent departments are climbed to the
first whose manager is someone else in employment that day; else nobody. So the head
of a department is managed by the head of the department above, never by themselves,
and concurrent employments may give two managers. A manager who has left manages
nobody from the day after their last employment ends (design §9, "a manager who
leaves: their reports route to the department's manager"): the people who name them,
and the people of a department they still head, go to the department's manager, then
up the parents. The climb carries a visited set, so a department graph that loops ends
it. This one rule decides the Manager audience, the org view, the `manager` a person's
work group shows and the dashboard's [attention items](#attention-items).

Nobody manages themselves (`self_manager`, 409), and the graph never loops: a write
that names, clears or changes a manager or a department on a terms row — adding,
replacing or deleting one, a new start, a reopening — and a department write that
changes its manager or parent — and an end moved later — take the manager graph's lock after the person's row and
judge the graph they leave on the row's day and on **every later day the graph
changes** (each terms row's day, each start, each day after an end). A person who would
manage themselves through anyone is `manager_cycle` (409), naming the first such day:
a row of B's naming A from March is refused when A's row naming B takes effect in June.
Two such writes at once are judged one after the other, so exactly one of a pair that
would close a loop is written. The check judges the edges as written — the manager a
terms row names and each department's, whether or not they are in employment that
day — since a leaver's edge is still there to come back into force on a rehire.

`GET /people/{id}/org` answers `{person, chain, reports}` as the work group names them
(`{id, displayName, employeeNumber}`): `chain` is the person's manager, that manager's
manager and so on up, following each one's earliest employment in force today and
stopping where nobody manages or where the walk would come back on itself; `reports`
are the people whose manager the person is today. Anyone who may see the person reads
it; everyone in the chain and the reports is in employment today, by the rule, so a
caller holding `staff:view` alone, who sees only the people active today, reads it
whole.

The **Manager audience** is resolved from the same rule on today: a caller whose
self-service link names a manager of the person reads the person as the
[table](#field-groups-and-audiences) says — the work group, the employments and the
non-pay terms, the roles, the history shaped and the § 14-6 check; not the private
group, the next of kin, the pay, HR's notes or the link (`GET /people/{id}/user`
answers them the unknown id's 404).

## The sealed identifiers

The national identity number and the bank account are **sealed** (design S4): written
through `apps/server/internal/secrets` — AES-256-GCM under a key derived from
`APP_SECRET` for the purpose — and never stored, answered, logged or recorded in clear.
Both are written and read under `staff:personal-data` only: not `staff:manage` alone,
and never the person (a fraud boundary — changing where pay goes takes a second
permission).

| Purpose | Holds |
| --- | --- |
| `staff/national-id` | the national identity number, sealed |
| `staff/national-id-index` | its MAC, in the unique column `national_id_mac` |
| `staff/bank-account` | the bank account, sealed |

**`PUT /people/{id}/national-id`** takes `{kind, value, country}`:

- A **fødselsnummer** or **d-nummer** is eleven digits once spaces, hyphens and full
  stops are dropped. Both control digits must hold, and the first six digits must be a
  real date — a d-nummer's day raised by 40, the century read from the individual
  number, and Skatteetaten's synthetic ranges (the month plus 40 or plus 80) accepted.
  `country`, if given, is `NO`.
- A **foreign** id is 1–40 letters, digits or hyphens, upper-cased, with its `country`
  (ISO 3166-1 alpha-2, not `NO`).
- Anything else is `national_id_invalid` (400) on `value`. The message says what is
  wrong — "the second control digit does not hold" — and never repeats a digit; a body
  the server cannot decode is a bare 400 that repeats nothing either.
- A fødselsnummer or d-nummer encodes a birth date. When the person has a birth date
  and the two differ, the write is `national_id_birth_date_mismatch` (400): one of them
  is wrong, and HR corrects whichever it is. The rule holds the other way too: HR's
  `PUT /people/{id}` setting a birth date the number on file does not encode is
  `national_id_birth_date_mismatch` on `birthDate`, the number opened under the
  person's lock to judge it.
- The number is indexed by an HMAC-SHA256 under `staff/national-id-index` of
  `kind | country | value`, in a unique column, so the same number on a second person
  is `national_id_duplicate` (400) — found without opening anything, settled by the
  unique constraint when two writes race, and **naming nobody**. The same digits as
  another country's foreign id are another identifier and are accepted.
- In clear the person keeps only the kind and a foreign id's country; responses carry
  `nationalId { kind, onFile }`. The last digits are never shown, since with the birth
  date they reconstruct the whole number. `q` on `GET /people` cannot match it.

**`PUT /people/{id}/bank-account`** takes `{kind, value}`: a `bban` is eleven digits
once spaces and full stops are dropped, with a valid MOD11 control digit; an `iban`
has its spaces dropped, is upper-cased, and must hold its ISO 13616 check digits — a
Norwegian one being fifteen characters around an eleven-digit account whose own MOD11
holds. Anything else is `bank_account_invalid` (400). The kind and the last four digits
are kept in clear and answered as `bankAccount { kind, last4 }`.

A change of either is an event carrying the kind (and the bank account's last four
digits) before and after. The value already on file, however it is typed, is no
change: no event, and for the bank account no mail — the national id is compared by
its MAC, the bank account by opening the one on file. Neither write moves the person's revision: the revision
guards the editable fields, which these are not.

**The bank-change mail.** After a bank account is set and the change has committed,
the person is mailed at their private email, else their work email, in their
preferred language (`en` or `nb`), naming the account's last four digits and nothing
more. It goes through `mail.SendOutbound` (the `Deps.SMTPSend` seam), outside every
transaction, on a context the request's cancellation does not reach. It is skipped with
a log line when the mail driver is not `smtp` or the person has no email, and a failed
send is logged with the person's id and the address taken out of the error, however
the server cased it; the change stands either way.

**The reveals**, `POST /people/{id}/national-id/reveal` and
`POST /people/{id}/bank-account/reveal`, open the value and answer it once —
`{kind, country, value}` and `{kind, value}` — with `Cache-Control: no-store`. Each is
recorded, in the same transaction, as a `national_id_revealed` or
`bank_account_revealed` event whose actor is the viewer; a reveal that cannot be
recorded answers nothing. A person with nothing on file is a 404.

Changing `APP_SECRET` makes every sealed value unopenable and every stored MAC
unmatchable; see
[Platform packages → Rotating APP_SECRET](/en/contributing/platform-packages/#rotating-app_secret).

## The self-service link

A person is linked to the user they sign in as by HR (design S24 as amended): a row in
`staff.identity_links` with `provider = 'identity'`, `external_id` the user's id,
`state = 'linked'` and `managed = false`. The link makes the user the person's
**Self** audience — My page and the person's own record — and nothing else: it grants
no permission and never makes the user managed by Staff. Any active user may be
linked, Owners included.

- **`PUT /people/{id}/user`** (`staff:manage`) takes `{"userId": "<uuid>"}`, or
  `{"userId": null}` to unlink; a body without `userId` is a 400. The user is read from
  the user directory before the transaction opens: an id no user has is a field error
  on `userId`, a disabled user `user_inactive` (400). Under the person's lock, a user
  linked to another person is `user_linked_elsewhere` (400), and so is the unique
  index's refusal when two links race. Linking to the user already linked writes
  nothing; linking to another replaces the link. A change is a `user_linked` event, an
  unlink a `user_unlinked` one. It answers the link.
- **`GET /people/{id}/user`** answers `{linked, state, managed, user}` — `linked: false`
  and nothing else for a person linked to nobody. The link is the person's and HR's to
  read (`staff:manage`, `staff:personal-data`); anyone else gets the unknown id's 404.
- **`GET /assignable-users?q=`** (`staff:manage`) is the picker: the user directory's
  search over active users, at most twenty, `{id, displayName}`. A user already linked
  elsewhere is listed; linking them answers why not.

A linked person cannot be [deleted](#deleting-a-person) as created in error.

## My page

`/me` is the caller's own person, found through their self-service link and read as
**Self** — whatever else the caller holds, so it shows what the person sees of
themselves (`notes` never). A caller linked to nobody gets `no_person` (404) — a
problem, not the bare 404 of an unknown id, since there is nothing to hide from the
caller about themselves.

- **`GET /me`** answers the person as `GET /people/{id}` answers them to Self.
- **`PUT /me`** replaces what the person edits themselves (design S17): `phoneMobile`,
  `phonePrivate`, `privateEmail`, `street`, `postalCode`, `city`, `country` and
  `nextOfKin` (required, at most three), with `If-Match` carrying the person's
  revision. It is a full replace of exactly these: an optional field left out is
  cleared, and every other field stays. The schema, `StaffMeRequest`, is closed: a
  field of the work group, the birth date, the gender, HR's notes or a sealed field is
  a 400 naming it, and nothing is written. The values follow the same rules as HR's
  replace. A change is a `person_updated` and a `next_of_kin_updated` event with the
  person as actor; together they move the revision on once. The link is judged again
  under the person's lock, so a person HR has just unlinked gets `no_person`.
- **`GET /me/events`** is the person's own history, paged, shaped as Self reads it:
  HR's notes are left out.
- **`GET /me/documents`** is the person's own [documents](#documents), health
  documents included, newest first.
- **`GET /me/export`** is the person's own copy of their data — see
  [the personal-data exports](#the-personal-data-exports).

## Documents

A person's archive (design S14): each document is a row in `staff.documents` and an
object in the [object store](/en/admin/object-storage/) under the module's scope
`staff`, at the relative key `people/<personId>/<uuid>` — a fresh random name, never
derived from the file's name. A document has a [category](#categories-and-classes), an
optional `validTo` (the last day it is valid, a certificate's expiry) and `signedAt`,
the file's name, its type and size, and an `employmentId`: the employment covering
`signedAt` — else the upload day — when exactly one does, which HR may change and which
the class A erase goes by. Every document carries its category as
`{id, key, names, class, restricted}`.

**Who reads.** The person reads all of theirs; `staff:manage` reads every document but
**health documents** — those in a restricted category; `staff:personal-data` reads all.
To anyone else — a manager and a colleague included — a person's archive and each of
their documents are the 404 an unknown id is, and a health document is that bare 404 to
a reader without the health group. `GET /people/{id}/documents` lists the archive
newest first; `GET /documents/{id}` streams the bytes.

**Who writes.** Filing, changing and removing a document in a category that is not
restricted needs `staff:manage` or `staff:personal-data`; in a restricted one,
`staff:personal-data` (`category_restricted`, 403, to HR without it — HR's own health
document included, which they read as its person but do not change or remove). The
person never writes their own documents, and anyone else gets the uniform 403.

### Filing a document

`POST /people/{id}/documents` takes `multipart/form-data`: the file in the part
`file`, `categoryId`, and optional `validTo` and `signedAt` (`YYYY-MM-DD`).

- **The file.** A PDF, JPEG, PNG or HEIC of at most 10 MiB. Its own leading bytes
  decide its type (HEIC by its ISO base media brand, which the standard sniffer does
  not know); a declared type or a file extension that names one of the other three is
  refused — a PNG named `.pdf`, a PDF sent as `image/png` — while one the four do not
  claim (`application/octet-stream`, no type, no extension) is no evidence either way.
  A type that is none of the four or disagrees is `document_type_unsupported`, a file
  over 10 MiB `document_too_large`, both 400 on `file`; a missing or empty file is a
  plain field error. The router caps the request at 10 MiB + 64 KiB.
- **The name** is the client's without any path or control character, at most 255
  characters, and never part of the key.
- **At most 200 documents per person**, counted whatever the caller may read — so HR
  without `staff:personal-data` refused with `document_count_exceeded` (409) learns
  that the person has documents it cannot see. That is accepted: the cap protects the
  store, and the alternative, a count per reader, would let a person's archive grow
  without bound.
- **The order.** The object is written **before** the row's transaction; a store that
  cannot write is 503 `storage_unavailable` and nothing is recorded. Then, under the
  person's lock: the person is not erased (`person_erased`, 409 — the
  [read-only rule](#the-erased-state), judged at the lock), the count is under
  the cap, the category exists and is not archived (a field error on `categoryId`),
  the row is written and a `document_added` event recorded. Whatever refuses or fails
  after the write removes the object again, with one attempt on a context detached from
  the request's and bounded to 30 seconds, logged when the store refuses.
- **A signed contract** — a document of the seeded category `contract` with `signedAt`
  — sets `contractSignedAt` on the employment covering that day when exactly one does
  and it records none, with that employment's own `employment_updated` event; the
  employment's row is locked before the document is written. With two covering
  employments nothing is set — which one the contract is for is HR's to say — and a
  date already recorded is kept. Typing the date on the employment stays possible.
- **The rate limit**: 600 uploads an hour per client address (`StaffDocumentUpload`),
  429 past it.

### Reading, changing and removing

- **`GET /documents/{id}`** reads the row in a read-only snapshot, then the object
  outside it, and streams the bytes with the type they were sniffed as, `inline` (HEIC
  `attachment`, since only Safari renders one) with the name RFC 5987 encoded,
  `Cache-Control: private, no-store`, `X-Content-Type-Options: nosniff` and
  `Content-Security-Policy: default-src 'none'; sandbox` — the 404 and the 503 carry
  the same headers. An object the store cannot read is 503 `storage_unavailable`.
- **`PUT /documents/{id}`** files a document of a restricted category under one of the
  person's [sick-leave cases](#sick-leave-cases) with `caseId` (`staff:personal-data`;
  replaced only when named, null clearing it), so the case's own erase reaches it
  (S15); `caseId` on another category, or a case not the person's, is a 400, and a
  document moved out of a restricted category leaves its case.
- **`PUT /documents/{id}`** replaces `categoryId`, `validTo` and `signedAt` under
  `If-Match` — a date left out is cleared; the file never changes. `employmentId` is
  replaced only when the body names it: `null` clears it, and left out the document
  keeps the one the upload derived. The category must exist and not be archived; the
  employment must be one of the person's. Moving a document into or out of
  a restricted category is `staff:personal-data`'s (`category_restricted`). It records
  `document_updated`.
- **`DELETE /documents/{id}`** deletes the row and inserts the object's
  `object_deletions` row in one transaction under the person's lock, with a
  `document_removed` event; after the commit it makes one attempt to remove the object
  and clears the ledger row when the store confirms. A store that refuses leaves the
  ledger row for the retention worker to finish, with the attempt recorded on it —
  `attempts` 1, `last_error`, and `next_attempt_at` two seconds on (the ledger's
  backoff: doubling from two seconds, at most an hour) — so it counts as failing; the
  request still answers 204: the document is gone. Deleting by hand and the retention
  erase share this path: the same [ledger](#the-deletion-ledger) row, finished by the
  same attempt.

### Categories and classes

Every document is filed under a category, whose **retention class** decides when it is
erased ([Retention](#what-is-due)): **A** (pay and contract documentation: five years
after the year of the employment it is filed against — or, filed against none, of the
person's last employment), **D** (personnel administration: with the record, after
`retentionGraceDays`) or **E** (health: `healthRetentionDays` after the last
employment ended). A category of class E is **restricted** — its documents are health
documents — and only class E is. Six are seeded:

| Key | English | Norwegian | Class | Restricted |
| --- | --- | --- | --- | --- |
| `contract` | Contract | Arbeidsavtale | A | no |
| `attest` | Attest | Attest | A | no |
| `certificate` | Certificate | Sertifikat | D | no |
| `correspondence` | Correspondence | Korrespondanse | D | no |
| `health` | Health | Helse | E | yes |
| `other` | Other | Annet | D | no |

- **`GET /document-categories`** lists them in order, archived ones included, to
  `staff:view`, `staff:manage` and `staff:personal-data`; anyone else gets the uniform
  403.
- **`POST /document-categories`** (`staff:manage`) adds a company category after the
  others: a `key` of 1–40 characters `[a-z0-9-]`, unique; `names` `{en, nb}`, each
  1–100 characters; and a `class` of A, D or E, **fixed from then on** — `restricted`
  follows from it.
- **`PUT /document-categories/{id}`** (`staff:manage`, `If-Match`) replaces a company
  category's `names` and sets `archived`. A seeded category never changes
  (`seed_immutable`, 409). An archived category is offered for no new document;
  archiving one that documents are still filed under is `category_in_use` (409) —
  move them first — so the archive never takes from the picker a category a document
  still names.

## Absence

Absence is delivery B. What stands so far: the [absence types](#absence-types) with
their [a-melding mapping](#the-a-meldings-leave-descriptions), the
[holiday schemes](#holiday-schemes) and an employment's scheme, the
self-certification policy and automatic carry-over in the [settings](#settings), the
[care-day fields](#the-care-day-fields) on a person, [absences](#absences) requested
and registered with their [overlap](#overlap) rule, the
[holiday account](#the-holiday-account) with its
[adjustments and carry-over](#adjustments-and-carry-over), and
[their events](#the-absence-events), the [decisions](#decisions) with their
[mails](#the-absence-mails), the [approvals inbox](#the-approvals-inbox) and the
[calendar](#the-calendar), the [sick-leave cases](#sick-leave-cases) with the
[arbeidsgiverperiode and the milestones](#the-arbeidsgiverperiode-and-the-milestones),
[sickness over ferie and egenmelding](#a-sykmelding-over-other-rows),
[egenmelding](#egenmelding) with its [withdrawal](#the-withdrawal), and
[care days](#care-days).

### Absence types

`staff.absence_types` is the vocabulary an absence is entered in. Each type has a
`key` (1–40 of `a–z`, `0–9`, `_`, unique), names in English and Norwegian (1–100
characters each), a `kind` the rules key on, a `unit` (`days`, or `hours` for part of
one day), whether it is `paid`, whether it `drawsHoliday` from the holiday account,
whether it `allowsPercent`, whether it `needsDecision` (requested and decided) or is a
notification (registered when entered), its `visibility` and its `ameldingLeave`. A
type's `key`, `kind` and `unit` never change.

The seeds, every installation's, in their order:

| Key | Kind | Unit | Paid | Draws | % | Decision | Visibility | a-melding |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ferie` | holiday | days | yes | yes | — | yes | absent | — |
| `egenmelding` | sick_self | days | yes | — | — | no | absent | — |
| `sykmelding` | sick_certified | days | yes | — | — | no | absent | — |
| `svangerskapskontroll` | other | hours | yes | — | — | no | hr_only | — |
| `svangerskapspermisjon` | leave | days | no | — | — | yes | hr_only | `permisjonMedForeldrepenger` |
| `omsorgspermisjon` | leave | days | no | — | yes | yes | absent | `andreLovfestedePermisjoner` |
| `foreldrepermisjon` | leave | days | no | — | yes | yes | absent | `permisjonMedForeldrepenger` |
| `fodselspermisjon` | leave | days | no | — | — | yes | hr_only | `permisjonMedForeldrepenger` |
| `ammefri` | leave | hours | no | — | — | yes | absent | `andreLovfestedePermisjoner` |
| `sykt_barn` | child_sick | days | yes | — | — | no | absent | — |
| `pleie_naerstaende` | leave | days | no | — | yes | yes | absent | `andreLovfestedePermisjoner` |
| `utdanning_lovfestet` | leave | days | no | — | yes | yes | absent | `utdanningspermisjonLovfestet` |
| `utdanning_ikke_lovfestet` | leave | days | no | — | yes | yes | absent | `utdanningspermisjonIkkeLovfestet` |
| `militaertjeneste` | leave | days | no | — | — | yes | absent | `permisjonVedMilitaertjeneste` |
| `offentlig_verv` | leave | days | no | — | yes | yes | absent | `andreLovfestedePermisjoner` |
| `religios_hoytid` | leave | days | no | — | — | yes | hr_only | `andreLovfestedePermisjoner` |
| `velferdspermisjon` | leave | days | yes | — | — | yes | absent | `andreIkkeLovfestedePermisjoner` |
| `permittering` | leave | days | no | — | yes | yes | absent | `permittering` |
| `avspasering` | time_off | hours | yes | — | — | yes | absent | — |
| `helse` | other | hours | yes | — | — | no | absent | — |
| `annen_lovfestet` | leave | days | no | — | yes | yes | absent | `andreLovfestedePermisjoner` |
| `annen_ikke_lovfestet` | leave | days | no | — | yes | yes | absent | `andreIkkeLovfestedePermisjoner` |
| `absent` | other | days | no | — | — | no | absent | — (reserved, always hidden) |

- **No percentage on the sick kinds.** A sykmelding's share of each day is its degree
  (`degree_percent`), not a percentage; ferie is whole days.
- **Visibility.** `absent`: a colleague sees "absent", the manager and HR the type.
  `hr_only` — religiøs høytid, svangerskapskontroll, svangerskapspermisjon and
  fødselspermisjon, which reveal special-category data: the manager sees "leave", the
  type is `staff:personal-data`'s and the person's own.
- **The reserved `absent`** is what the health erase writes in place of a sick or
  child-sick type; it is hidden and is never shown again (`seed_immutable`).
- **Reading.** `GET /absence-types` answers every type that is not hidden to every
  caller — `hr_only` ones included: the type is public, who takes it is not — and the
  hidden ones too to `staff:manage`. `GET /meta` answers the same visible list as
  `absenceTypes`.
- **A company type** (`POST /absence-types`, `staff:manage`) has the kind `holiday`,
  `leave`, `time_off` or `other` — the sick kinds and `child_sick` are the seeds',
  since the case, egenmelding and the care days key on them. A percentage and drawing
  on the holiday account are for a type counted in days. Left out, `sort` places it
  after every type there is (1–1000 when given).
- **A change** (`PUT /absence-types/{id}`, `staff:manage`, `If-Match`) replaces the
  names, the flags, the visibility, the mapping, `hidden` and `sort`; a body naming
  `key`, `kind` or `unit` is a 400 naming it. A **seed** changes its names, its order
  and whether it is hidden, nothing else: anything more, and showing `absent`, is
  `seed_immutable` (409). A company type's visibility is **widened** — `hr_only` to
  `absent` — only while no absence names it: once one does, widening is
  `absence_type_in_use` (409), since it would show colleagues and managers, after the
  fact, a type people took as HR-only. Narrowing is always allowed. The type's row is
  held `FOR UPDATE`, which an absence's foreign key waits for, so no absence slips in
  between the question and the change.
- **History.** Each write is an installation event, `absence_type_created` or
  `absence_type_updated`, naming the type (`absenceTypeId`) and the fields that
  changed. It is about no person and touches no retention class.

### The a-melding's leave descriptions

`ameldingLeave` is the a-melding's `permisjonsOgPermitteringsBeskrivelse` a leave of
the type is reported as — the kodeverk `PermisjonsOgPermitteringsBeskrivelse`'s six
leave descriptions and permittering, in NAV's spellings, exactly:
`permisjonMedForeldrepenger`, `permisjonVedMilitaertjeneste`,
`utdanningspermisjonLovfestet`, `utdanningspermisjonIkkeLovfestet`,
`andreLovfestedePermisjoner`, `andreIkkeLovfestedePermisjoner` and `permittering`.
Any other value is a 400 on `ameldingLeave`. The codes `velferdspermisjon` and the
unsplit `utdanningspermisjon` expired on 31 December 2022 and are refused (the
a-melding answers them with MAGNET_EDAG-114B); a type is reported by its legal basis,
so the welfare-leave seed reports `andreIkkeLovfestedePermisjoner` — welfare leave is
agreed or tariff-based, not in the Working Environment Act — and a company whose
welfare leave is statutory uses `annen_lovfestet`. Omsorgspermisjon at a birth is one
of the other statutory leaves. The spellings rest on NAV's own code list; they are
checked against Skatteetaten's a-melding schema before the payroll export reports
leave.

What the a-melding asks to be reported as leave, which the payroll export's leave rows
follow when they arrive (Skatteetaten's a-melding guide, "Permisjon", as updated on
11 January 2024; in English,
[the a-melding guide, "Leave"](https://www.skatteetaten.no/en/business-and-organisation/employer/the-a-melding/the-a-melding-guide/employment/information-on-employment/leave/)):

- leave that lasts **more than fourteen days**, whole or partial — and several shorter
  leaves within twelve months that together pass fourteen days count together;
- **omsorgsdager** (a sick child, `sykt_barn`) are **never** reported as leave;
- **svangerskapspenger** are not reported as leave — no seed models them;
- every leave carries its `permisjonsprosent`, more than 0 and at most 100.

### Holiday schemes

`staff.holiday_schemes` holds the company's holiday rules in **virkedager** — Monday
to Saturday, as the Holiday Act counts — each answered with `converted`, the working
days of a five-day week it comes to, which is what the holiday account will deduct:

```
convert(v) = ⌊v / 6⌋ × 5 + min(v mod 6, 5)
```

25 → 21, 30 → 25, 31 → 26, 6 → 5, 18 → 15, 12 → 10: a Monday-to-Friday week costs five
days and an entitlement is never undercounted.

| Field | Rule |
| --- | --- |
| `name` | 1–100 characters, unique however it is cased. |
| `entitlementVirkedager` | 25–60 (the Holiday Act's 25 at least). |
| `extraFrom60Virkedager` | 6–30: the extra days from the year the person turns 60. |
| `mainPeriodVirkedager` | 18 (ferieloven § 7 (1)) to the entitlement; left out, 18. |
| `afterSeptemberVirkedager` | 6 (ferieloven § 5 (2)) to the entitlement, for a start after 30 September; left out, 6. |
| `maxCarryOverVirkedager` | 0 to 12 plus the entitlement's days above 25 (ferieloven § 7 (3)), the agreed carry-over's cap; left out, 12. |

The statutory scheme is seeded — "Statutory (ferieloven)", 25 / 6 / 18 / 6 / 12, the
default. `GET /holiday-schemes` (every caller) answers every scheme, archived ones
included, the default first; `GET /meta` answers them as `holidaySchemes`.
`POST /holiday-schemes` and `PUT /holiday-schemes/{id}` (`staff:manage`, `If-Match`
on the change) write them; each is an installation event, `holiday_scheme_created` or
`holiday_scheme_updated`, naming the scheme (`holidaySchemeId`) and the fields that
changed.

- **Exactly one default**, the scheme of an employment that names none.
  `isDefault: true` on a new or changed scheme moves it there; `false` on the default
  is `structure_default`, and an archived scheme is never made the default
  (`structure_archived`). The partial unique index `ux_holiday_schemes_default` holds
  "at most one", as `ux_business_units_default` does for the business units.
- **Archiving** (`archived: true`) is refused `holiday_scheme_in_use` for the default
  and for a scheme an employment names; `archived: false` restores it. An archived
  scheme stays on the employments that already follow it and leaves the picker.
- **An employment's scheme.** `POST /people/{id}/employments` and
  `PUT /employments/{id}` take `holidaySchemeId`; left out, the employment follows the
  default. A scheme that does not exist is a 400 on `holidaySchemeId`; an archived one
  is `structure_archived`, unless the employment already follows it. The employment's
  response carries it in the employment group — the person, their manager and HR; a
  colleague does not read it — and a change is an `employment_updated` event of
  class C.

### The care-day fields

Three fields on a person feed the [care-day counter](#care-days) (omsorgsdager,
ftrl. § 9-6). HR types them from what the employee declares; no child
is named.

| Field | Rule |
| --- | --- |
| `childrenUnder12` | 0–20: the children through the year they turn twelve **without** NAV's decision of extra care days. |
| `chronicallyIllChildren` | 0–20: the children **with** NAV's decision of extra care days for a chronic illness or disability, through the year they turn eighteen. |
| `soleCarer` | Whether the person cares for the children alone (aleneomsorg). |

The two counts together are the number of children § 9-6 counts, so no child is
counted twice. They are written on `POST /people` and `PUT /people/{id}`
(`staff:manage`) — left out, cleared, as every optional field of the replace — and
`PUT /me` refuses them as properties it does not take: the person declares, HR types.
They are their own field group, read by the person, `staff:manage` and
`staff:personal-data`, never by a manager or a colleague; they are class D, so the D
erase clears them, and a change is a `person_updated` event from which an audience
that does not read them has the three keys left out altogether, as the pay's and HR's
notes' are — a manager does not learn that they moved.

`chronicallyIllChildren` says that a child has a chronic illness or disability, which
is health data about the child (GDPR Art. 9). It is processed under Art. 9 (2) (b): the
employer's own obligations under ftrl. § 9-6 and § 9-8 — granting the extra care days
and claiming the refund — need the number, and only the number; no child is named and
no diagnosis is recorded. For now it stays where the design places it, class D in the
care-day group; whether it should move to the health group and class E is an open
question for the owner.

### Absences

`staff.absences` holds what a person is away for: a person, a type, a first and a last
day — or one day and its hours, for a type counted in hours — a percentage of each day,
a status and, for a sykmelding, its degree. A type that needs a decision starts
`requested`; a type that is a notification (egenmelding, sykmelding, sykt barn, helse)
starts `registered`. A request is then `approved` or `rejected` by a decider, and a
request or an approved row may end `cancelled` ([decisions](#decisions)).

**Who writes one.** `POST /me/absences` is the caller's own; `POST /absences` names
`personId` and is the caller's own person's, or — `staff:manage` — anyone's. Nobody
else may, a manager registering for a report included (the uniform 403): a
registration is the person's or HR's. `staff:personal-data` alone writes no absence
either: it is a grant to reveal and to read health data, not an HR write grant, so
every absence write is `staff:manage`'s (plan reading 31). Both requests refuse a
property they do not take. A caller linked to nobody gets `no_person` on
`/me`.

| Field | Rule |
| --- | --- |
| `typeId` | A type that is not hidden — never the reserved `absent`. |
| `firstDay`, `lastDay` | Civil dates; `lastDay` left out is `firstDay`; at most 366 days; inside one of the person's employments. |
| `percent` | 1–100, two decimals, only on a type that allows a percentage (`percent_not_allowed`); 100 when left out. Never on a sick kind, whose percentage is 100. |
| `hours` | 0.25–24 in quarter hours, only on a type counted in hours — which needs it, and one day (`hours_on_day_type` either way). |
| `degreePercent` | A sykmelding's degree, 1–100, two decimals, 100 when left out — its **share** of each day everywhere (the holiday account, the payroll totals); never on another type. |
| `note` | At most 500 characters; refused on egenmelding, sykmelding and sykt barn (`absence_note_on_sick`): what a sickness is about is health data. |

The write is judged under the person's lock: the type is read `FOR KEY SHARE` (so HR
widening an HR-only type waits for it, and then finds it in use), the employments, then
[overlap](#overlap). Its answer carries `workingDays` — Monday to Friday without the
public holidays of the platform calendar (`internal/calendar`), whatever the share; an
hour row counts none — and, for a type that draws on the holiday account, `accounts`:
the [holiday account](#the-holiday-account) of each year the row touches after the
write, and `overdrawn` when taking every requested day would overdraw one. That is a
warning, never a refusal.

**Changing, removing, cancelling** (reading 16, pre-flight M8). `PUT /absences/{id}`
(`If-Match`) replaces the type, days, percent, hours, degree and note, judged as a new
row is with the row itself left out of the overlap. A change never swaps a type that
needs a decision for one that is a notification, or the other way round
(`absence_status_change`): a request stays a request and a registration a
registration — remove the row and enter the other. The
person changes their own row while it is `requested`; a `registered` row — a sick one
included — is `staff:manage`'s to change: ending a sykmelding early or fixing its days
is HR's job, the person asks. `DELETE /absences/{id}` removes a row nobody decided: a
request (the person or HR) or a registration (HR alone). Anything else is
`absence_not_requested`. `POST /absences/{id}/cancel` withdraws a request — the
person's or HR's — and keeps it as history (`cancelled`); an approved row is cancelled by
its decider ([decisions](#decisions)); anything else is `absence_not_cancellable`.
**Authority comes first, and one answer** (review C1): anyone who is not the person, a
decider of theirs or HR — for an HR-only row, HR with `staff:personal-data` (review H2) —
gets the 404 of a row they cannot write, whatever its status, before any status is
judged; so a write never tells a reader what the row does not.

**Reading them.** `GET /absences?personId&from&to&status&page&pageSize` lists, newest
first, the rows the caller may see: everyone's to `staff:manage`, `staff:approve` and
`staff:personal-data`; otherwise their own, their reports' today (the
[one manager rule](#managers-and-the-org-view)), and — with `staff:view` — the
`approved` and `registered` rows of every person active today, never a request or a
refusal — and those never under a `status` filter: a colleague reads no status, so
asking for the registered rows cannot pick out who was sick. `GET /absences/{id}` answers one, the 404 of an unknown id to anyone who may
not see it. `GET /me/absences` lists the caller's own. Each row is shaped by three
groups (design §5):

| Group | Fields | Self | Colleague | Manager | Approver | `staff:manage` | `staff:personal-data` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Absence | `firstDay`, `lastDay` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Absence | `status`, `workingDays` | ✓ | — | ✓ | ✓ | ✓ | ✓ |
| Absence type | `typeId` (`shownAs: type`) | ✓ | — (`shownAs: absent`) | ✓; an `hr_only` type `shownAs: leave` | ✓; an `hr_only` type `shownAs: leave` | ✓; an `hr_only` type `shownAs: leave` | ✓ |
| Absence detail | `percent`, `hours`, `degreePercent`; `note` and `decisionNote` never on a row shown as leave | ✓ | — | ✓ | ✓ | ✓ | ✓ |

**A colleague cannot tell one absence from another.** Only the notification types —
the sick kinds among them — start `registered`, and only an hour type counts no
working day, so a colleague is shown neither the status nor the working days: a
sykmelding, a sick child, a doctor's visit and a holiday are the same dates, shown
`absent`.

**A row shown as leave looks like any other leave.** To the manager and HR without
`staff:personal-data`, a row of an HR-only type carries no `hours`, the working days of
its days whatever its unit, and `approved` in place of `registered` — so
svangerskapskontroll, an HR-only hour registration, is not told apart from the other
HR-only leave. It carries no `note` (review M1), and it is shown only while `approved`
or `registered`: a request of an HR-only type, a refusal and a cancel are
`staff:personal-data`'s ([decisions](#decisions)), so a list, a calendar and a read by
id leave them out to these readers. A status filter matches it by the status it is shown as — asking for
`registered` rows does not list it, asking for `approved` does — and none of its events
is listed to them (below): they read its dates already, and one registration event
against a request and its decision would tell a notification from a request.

**A row's revision goes only to whoever writes it** (Task 3 re-check, leak 1). A
registration stays at revision 1 while a decided request moves past it, so the number
would tell a notification from a request too. The person, and `staff:manage` with
`staff:personal-data`, read `revision`; `staff:manage` without `staff:personal-data` —
who writes every row but reads an HR-only one as leave — reads an opaque **`etag`** in
its place on every row alike: an HMAC of the row's id and revision under the
installation's secret (the secrets box, purpose `staff/absence-etag`), `a1-` and 24
characters, which changes with every write and says nothing of how many there were. It
sends the etag back, quoted, in `If-Match`; from such a caller a bare revision is
`revision_conflict`, so a write cannot probe how often a row was written, and the
conflict's detail names no revision. A manager, `staff:personal-data` alone and a
colleague are sent neither.

### Overlap

On every day of a new or changed day row, each of the person's `requested`, `approved`
and `registered` day rows that covers that day must be of a type that allows a
percentage, as the new one must, and the shares of all of them that day sum to at most
100 % — otherwise `absence_overlap` (409). A request blocks as an approved row does
(reading 17). Hour rows overlap only hour rows, on their one day, by hours summed to
at most 24; an hour row counts no day, so it neither blocks nor is blocked by a day row
(reading 27). A sykmelding stands outside the share rule over ferie, approved or
requested, which it leaves as it is, and over an egenmelding, which it trims
([a sykmelding over other rows](#a-sykmelding-over-other-rows)); an egenmelding or a
ferie on a day a sykmelding covers is refused as any overlap — a ferie request under a
sykmelding at its approval too. Beside a leave that allows a percentage a sykmelding of
**any degree** stands while the leave leaves part of the day (under 100 %): the doctor
grades the sick leave against the work that is left, so the day's sick share is
`degree × (100 − leave %) / 100` — 50 % sick beside 60 % leave is 20 % of the day
(Task 5 review M1; the payroll export and the sick-leave rate read that share). Two
sykmeldinger never share a day.

### The holiday account

`GET /people/{id}/holiday-account?year` — the person, a manager of theirs today,
`staff:manage` and `staff:personal-data`; anyone else the 404 of an unknown person —
and `GET /me/holiday-account?year` answer one person's account for a year (this year
in the business time zone when left out; 2000–2100), in working days:

- **`entitlement`**: the scheme of the person's earliest employment active in the year
  (two concurrent employments, one account), [converted](#holiday-schemes) — its
  after-September days when that employment starts after 30 September of the year —
  plus the extra days converted when the person turns 60 or more in the year,
  un-prorated. No employment in the year, nothing.
- **`carriedIn`**: the year's agreed carry-overs, capped at the scheme's maximum
  converted, and its forced carry-overs and corrections, uncapped.
- **`carriedInDerived`**: with automatic carry-over on, for a year whose 1 January is
  on or after the day it was turned on (`autoCarryOverSince`) — turned on
  12 September 2026, it first derives into 2027, never into a year already under way
  whose carry-in HR settled — when no carry-over from the year before is recorded,
  that year's remaining when above zero: a forced carry-in nobody typed. A carry-over
  recorded by hand takes its place.
- **`taken`**: the approved and registered days of the types that draw on the account,
  on working days **of that year** — a row across New Year counts in both — each times
  its share. **`pending`**: the requested ones, shown apart.
- **`remaining`**: `entitlement + carriedIn + carriedInDerived − taken`; **`overdrawn`**:
  `remaining − pending` is below zero.

### Adjustments and carry-over

`staff.holiday_adjustments` holds HR's changes to an account, in working days, signed,
one decimal. `GET /people/{id}/holiday-adjustments` lists a person's, newest year
first; `POST /people/{id}/holiday-adjustments` records one — `year` (2000–2100),
`days` (−366 to 366, not zero), `reason` (`agreed_carry_over`, `forced_carry_over` or
`correction`), `fromYear` (a carry-over's, the year just before `year`; never a correction's) and
`note` (at most 500, required with `forced_carry_over` and `correction`) — as
`holiday_adjusted`; `DELETE /holiday-adjustments/{id}` removes one as
`holiday_adjustment_deleted`. All three are `staff:manage`'s.

`POST /people/{id}/carry-over {fromYear, kind, days?, note?}` carries a year's untaken
days into the next: `agreed`, capped at the scheme's maximum (the scheme the next year's
account follows); or `forced`, uncapped, with its note. `days` defaults to `fromYear`'s
remaining rounded down to a tenth — capped, when agreed; an agreed `days` above the cap
is a 400, as is a year with nothing left. It is recorded as `carry_over_recorded`.

**At most one carry-over per person and year it comes from** (pre-flight I8), however
it is recorded: judged under the person's lock and held by the unique index
`ux_holiday_adjustments_carry_over`, whose violation answers the same
`carry_over_recorded` (409) in words of its own — it is reached only by a write past
the lock. Deleting it lets it be recorded again.

### Decisions

**Who decides** (design S12, plan Task 4, readings 1, 2 and 4; `deciders.go`). The
deciders of a person today are (a) the people the [one manager rule](#managers-and-the-org-view)
names for them — so a manager who has left decides nothing and the department climb
takes over — whose self-service link names an **active** user (the
**manager-deciders**), (b) every holder of `staff:approve` and (c) every holder of
`staff:manage`. Nobody decides their own: a manager's own request goes to their own
manager by the same rule, and anyone else's waits for another holder (`own_request`,
403). **Self-approval** is provisional (open question 2, pre-flight I7): a holder of
`staff:approve` or `staff:manage` who heads the structure today — their terms place them
in a department they manage with no manager above it — and for whom (a) is empty
approves their own; the row is `self_approved`, the event `absence_self_approved`, and
`staff:manage` sees the [`selfApproved`](#attention-items) item until it ends. A caller
who decides nothing of the person's — `staff:personal-data` alone on a plain type, a
colleague, a stranger — gets the 404 of a row they cannot write, whatever the row's
status (review C1): the code `decision_not_yours` is not answered, since it would say the
row is there in a status they may not act on. The person's capabilities carry
`canDecide` for the caller.

**HR-only types are decided by `staff:personal-data` alone** (review H4, interim option
(b), reading 33, open question 9): §5 gives those types to that permission. A request of
an HR-only type is in no manager's, Approver's or HR-without-`staff:personal-data`'s
inbox, mail, list or calendar; any holder of `staff:personal-data` but the person decides
it, and there is no self-approval for it — with no other holder it waits. Its writes —
change, delete, cancel, correct — are the person's (as for any row) and HR's with
`staff:personal-data`; anyone else gets the uniform 404. So to every reader of the type as
leave, every HR-only row, requested-then-approved or registered, is one approved leave
they never saw asked for, refused or withdrawn: no note, no decision field, no history —
svangerskapskontroll reads as any other.

The decider is judged before the person's lock — it reads the user directory, which no
transaction may — and the row under it, so the second of two deciders, or a decider
racing the person's cancel, finds the row no longer a request.

- **`POST /absences/{id}/approve`** `{note?}` and **`/reject`** `{note}` (a rejection
  needs its reason, at most 500 characters): a requested row only, else
  `absence_not_requested` (409). An approval judges [overlap](#overlap) again and
  answers the holiday accounts after it. Recorded as `absence_approved`,
  `absence_self_approved` or `absence_rejected`, with `decisionNote` (class D) and the
  decider as actor; the row keeps `decidedByUserId`, `decidedAt`, `decisionNote` and
  `selfApproved`, and answers `decisionNote` with the detail — never on a row shown as
  leave, which carries no decision field (reading 32).
- **`POST /absences/{id}/cancel`** of an **approved** row is its decider's while its first
  day lies ahead, and `staff:manage`'s alone once it has come (a decided absence in the
  past is HR's, S12); the person never cancels a decided row (`absence_not_cancellable`).
  Recorded as `absence_cancelled`.
- **`POST /absences/{id}/correct`** (`staff:manage`, `If-Match` as a change takes it) changes
  an `approved` or `registered` row's type, days, percent, hours, degree and note, judged
  as a new row is; a request, a rejected or a cancelled row is `absence_not_cancellable`
  (pre-flight M7), and a correction keeps the status, so never across the line between
  a request and a registration (`absence_status_change`); never HR's own row but the
  self-approver's. Recorded as `absence_corrected`, both types shaped by their own
  visibility.

### The absence mails

Sent after the write has committed, outside every transaction, through the module's one
SMTP accessor; skipped with a log line when the mail driver is not `smtp`, and never
failing the write (reading 26). Staff mails only addresses it holds: a person's work
email, else their private one, in that person's language — **on a request and a
registration to each manager-decider**, never for an HR-only type (review H3) (holders of `staff:approve` and `staff:manage`
by right get the inbox and the attention list, since the user directory names no
address: reading 4, open question 1), **on an approval, a rejection, a decider's cancel
and a correction to the person**. A recipient without an address is skipped with a log
line naming the person's id. A mail names the type only when it is neither a sick kind
nor HR-only — an HR-only type is "leave", a sick or child-sick one "an absence" — and
never a degree, the hours, a note or a decision's reason (reading 15); it carries the
dates and a link to My page.

### The approvals inbox

`GET /approvals?page&pageSize` lists the requests the caller decides, oldest first by
when they were made: every person's of a plain type to `staff:approve` and
`staff:manage`, a manager's reports' today, an HR-only type's to `staff:personal-data`
alone; never the caller's own but the self-approver's. Each item is
`{absence, person}` — the person as the work group names them, the row shaped as
`GET /absences` shapes it for the caller.

### The calendar

`GET /calendar?from&to&departmentId` answers a range of at most 93 days: the public
holidays in it from the platform calendar (`internal/calendar`) and a row per person
with their requested, approved and registered absences touching it, shaped as
`GET /absences` shapes them. `staff:approve`, `staff:manage` and `staff:personal-data`
see everyone with an employment in the range; `staff:view` the people among them active
today, as the directory lists them, each bar the dates alone and only for approved and
registered rows; anyone else their reports today and themselves. `departmentId` keeps
the people whose terms place them in that department on the day judged — today, held
inside the range.

### The absence events

An absence's write is one event — `absence_requested`, `absence_registered`,
`absence_updated` (each field's `from` and `to`), `absence_deleted` (the row's fields as
they were), `absence_cancelled`, or a [decision's](#decisions) `absence_approved`,
`absence_self_approved`, `absence_rejected` and `absence_corrected`, or a sykmelding's
`absence_split` of a row beneath it ([below](#a-sykmelding-over-other-rows); the part
it inserted named `splitAbsenceId`) — naming the absence (`absenceId`); an adjustment's
names it (`adjustmentId`). Beside its `fields` each carries **`classes`**, each field's
retention class as the module wrote it (pre-flight B1), because one name means
different data on different rows:

| Field | Class |
| --- | --- |
| `firstDay`, `lastDay`, `percent`, `hours`, `status`, `selfApproved`; an adjustment's `year`, `days`, `reason`, `fromYear` | C |
| `note`, `decisionNote`; an adjustment's `note` | D |
| `type`, `degree` | E on a sick or child-sick row (either side of a change), else C |

`payload_classes` is their union. The names never enter the module's per-name class
table: the erase blanks such an event **by its own map** — a leaver's D erase blanks
the note and keeps the dates and the type, a sykmelding's type and degree included;
the health erase blanks a sick row's type and degree and leaves a ferie row's alone. A
field the map does not class, and every field of an absence event without a map, is
blanked by every erase: what cannot be placed is erased rather than kept.

They are shaped by the three groups above, field by field — an adjustment's figures
by the detail group, so a manager reads them in the history as they read the account
itself, deliberately; its note HR's alone (left out for anyone else) — and **every type
value, before and after, by its own type's visibility** (pre-flight B2): a correction
away from an HR-only type shows a manager `"leave"` before and the new type after; a
deleted HR-only row's event shows `"leave"`; a type the module cannot find shows
`"leave"` too. Each absence event also names the row's type on either side
(`typeIds`), each with its class beside it (`typeClasses`, in the same order: E for a
sick or child-sick type, else C). **When one of them is HR-only, or the event names
none, the event is not listed at all to a reader of `"leave"`** — the manager and HR
without `staff:personal-data` (Task 3 re-check, leak 2; reading 32): its count and its
kinds, one `absence_registered` against an `absence_requested` and its decision, would
tell svangerskapskontroll from the other HR-only leave. They read the row's dates on the
row itself. (Were one listed, its shaping would still be the row's — the neutral kind
`absence_changed`, no `hours`, `registered` read as `approved`.) The erase treats
`typeIds` by `typeClasses` as it treats the fields by `classes`: the health erase
replaces a sick type's id with the reserved `absent` type's (leak 3), so neither the
history nor the complete personal-data file, which carries each event whole, names the
sick type once it is erased; an entry without a class is replaced by every erase. A sick row's event touches class E, yet the absence family is listed to
everyone who reads the person's history (pre-flight I6) — the manager and HR without
`staff:personal-data` read its dates and degree, as they read the row itself — while
any other event touching E stays the health group's.

The [sick-leave case's](#sick-leave-cases) events — `case_opened`, `case_updated`,
`case_closed`, `case_merged`, `milestone_done`, `milestone_waived`,
`milestone_note_updated` — name the case (`caseId`) and, for a milestone's, the
milestone (`milestone`); every field is class E, and each carries the case's types as
`typeIds` and `typeClasses`. They are listed to everyone who reads the person's
history, field by field: the days, the close, the estimate, a merge and the
milestones' facts to the case's readers, the accommodation note as the case's own, a
milestone's note and a waiver's reason to the person and `staff:personal-data` alone
(`"changed"` to anyone else) — but `milestone_note_updated`, which is about the note
alone, is listed only to the person and `staff:personal-data` (review L4). The [withdrawal's](#the-withdrawal) events are listed only
to the care-day fields' readers.

### Sick-leave cases

`staff.sick_cases` gathers a person's egenmelding and sykmelding rows (both
`registered`) into the sickness they belong to (design S10). It is **derived**: every
write of a sick row — a registration, HR's change, correction or removal of one —
derives the person's cases again from their sick rows, under the person's lock, and
stores the result (plan reading 13):

- **The join** (ftrl. § 8-19 third paragraph, pre-flight I1): a sick row joins a case
  when fewer than sixteen calendar days lie between the case's last day and the row's
  first — `first_day ≤ last_day + 16`. A case ending 1 January is joined by a row from
  17 January (2–16 January between, fifteen days); a row from 18 January opens a new
  case (`case_opened`).
- **Its days**: `first_day` the first row's, `last_day` the last row's, stored. A case
  keeps its id while its rows do; a case whose rows are all gone is removed, its
  documents leaving it.
- **A merge** (Task 5 review M3): a row that fills the gap between two cases merges
  them into the older. The younger's documents move with its rows; each of its
  milestones' done day or waiver is carried onto the older's where that has neither;
  the milestones' notes and the accommodation note are appended after the older's, a
  blank line between. A note is the employer's aml. § 4-6 record and is never cut:
  when the two no longer fit in 2000 characters, the survivor keeps its own text and
  ends with "… continued in the case's history". Its estimate is kept where the older
  has none. `case_merged` on the survivor names the case it took in (`mergedCaseId`)
  and carries that case's notes whole — `mergedNotes` by milestone, read by the person
  and `staff:personal-data`, and `mergedAccommodationNote`, read as the accommodation
  note is — all class E. A milestone write on the younger case once it is gone is the 404 of a case
  that is not there.
- **Open or closed**: a case is closed when HR closed it (`POST /cases/{id}/close`,
  `staff:manage`, `case_closed`) or when `last_day + 16` is before today — the join's own
  bound, so a case closes on the day a new row would no longer join it (reading 5). A
  row that joins a case HR closed opens it again.
- **The 52-week estimate** (`maxBenefitEstimate`) is `first_day + 364` until HR sets it
  (`PUT /cases/{id}`, `staff:manage`); null returns it to the derived day (M5). It is an
  estimate NAV may correct: sykepenger are paid for at most 248 days (52 weeks), and
  those days are counted **across cases** — a new sickness continues the count unless
  the person has been fit for work for 26 weeks in between (ftrl. § 8-12). The module
  derives the estimate from the one case; HR sets NAV's date when an earlier case
  shortens it.
- **The accommodation note** (`accommodationNote`, at most 2000 characters) is the
  manager's and HR's: functional notes about tilrettelegging (aml. § 4-6), never a
  diagnosis — there is no field for one.

`GET /cases?personId&status&page&pageSize` lists the cases the caller reads, newest
first — `status` `open` (the default), `closed` or `all` — and `GET /cases/{id}` one
case with its milestones. A case and its milestones are class **E** whole.

| | Self | Manager | Approver | `staff:manage` | `staff:personal-data` | Colleague |
| --- | --- | --- | --- | --- | --- | --- |
| The case, its dates and milestones | ✓ | ✎ | ✎ | ✎ | ✎ | — |
| The accommodation note | ✓ | ✎ | — | ✎ | ✎ | — |
| The estimate, the close | ✓ | ✓ | ✓ | ✎ | ✓ | — |
| A milestone's note, a waiver's reason | ✓ | — | — | — | ✎ (the note) | — |

A caller who may not write what a write asks for gets the 404 of a case they cannot
write, whatever its state (authority before status); a case they may not read is a 404
on every operation. `PUT /cases/{id}` and `PUT /cases/{id}/milestones/{kind}/note`
take `If-Match` (the case's and the milestone's revision). On `PUT /cases/{id}` a field
left out is kept and one sent is written, null clearing the note and returning the
estimate to the derived day (review L6); the estimate sent by anyone but
`staff:manage` — null included — is a 400. A milestone's note left out or empty clears
it.

### The arbeidsgiverperiode and the milestones

Each case has five milestones (`staff.sick_case_milestones`), dated when the case's
rows change — never touching a milestone's done day, its waiver or its note:

| Kind | Date |
| --- | --- |
| `employer_period_end` | The case's sixteenth sick day; none until the rows reach it. |
| `follow_up_plan` | `first_day + 28` — the follow-up plan (aml. § 4-6 (3)). |
| `dialogue_1` | `first_day + 49` — the first dialogue meeting. |
| `nav_activity` | `first_day + 56` — the activity requirement at eight weeks (ftrl. § 8-8 second paragraph); NAV's own follow-up is § 8-7 a. |
| `dialogue_2` | `first_day + 182` — the second dialogue meeting. |

**The arbeidsgiverperiode** (ftrl. § 8-19, NAV's R08-00 on § 8-19, Revision 2 Q7a)
ends on the day the case's sick days reach sixteen. Every day of its rows counts —
a graded day as one, whatever its degree — and so does every day of a gap between two
rows made **only** of Saturdays, Sundays and public holidays: sick on Friday and again
on Monday, the weekend counts. Egenmelding Monday–Friday and a sykmelding from the next
Monday end the period on the Tuesday of week 3. A gap holding a working day — a day the
person worked between two absences — is not bridged and none of its days count; the
module knows no work schedule, so another agreed day off between two rows is not
bridged either, and HR registers it when it should count (reading 30). The employer
pays the sixteen days only for a person who has worked for it four weeks before the
absence (ftrl. § 8-18); the module does not judge that condition.

A milestone is **due** (`due`) only while it is neither done nor waived (review L7),
its case is open and the case has lasted to within a week of the milestone's date —
`last_day ≥ date − 7` (pre-flight I5): a three-day case has none due. The person's
manager, the Approver and HR mark one done (`POST /cases/{id}/milestones/{kind}/done
{doneAt?}` — today when left out, never a later day nor one before the case's first
day; `milestone_done`) or waive it as plainly unnecessary with a reason of 1–500
characters (`/waive`, `milestone_waived`; the actor recorded); either clears the other.
`employer_period_end` follows from the sick days and is neither marked done nor waived
(400, review L8). The waiver's reason is never to be a diagnosis, yet it is free text
about a sickness: class E, read by the person and `staff:personal-data` alone, like a
milestone's note (review L5). `staff:personal-data` writes a milestone's note
(`PUT .../note`, `milestone_note_updated`).

### A sykmelding over other rows

Registered over the person's rows, a sykmelding stands over some of them:

- **Ferie under a sykmelding** stays as it is (Task 5 review H1). Postponing holiday
  for sickness is the **employee's demand**, made with a legeerklæring (ferieloven § 9):
  for sickness that begins before the holiday, by the last working day before it
  (§ 9 first paragraph); for sickness during the holiday, as replacement days later
  (§ 9 second paragraph). Until HR records the demand, the approved ferie and a 100 %
  sykmelding stand side by side — the ferie still counted as taken — and HR's
  attention list names the ferie ([`holidayAffectedBySickness`](#attention-items)). A
  sykmelding over a *requested* ferie leaves the request as it is; the overlap check
  refuses it at approval.
- **The demand.** `POST /absences/{id}/postpone-holiday {demandedOn?}`
  (`staff:manage`; `id` the sykmelding) records it — `demandedOn` the day the employee
  made it, today when left out, never later. Each approved ferie row the sykmelding lies
  over is split around it: the part before stays on the row, the part after becomes a
  new approved row with the same decision, and ferie it covers whole is cancelled. The
  days return to the account through its arithmetic alone — no adjustment is written.
  One `absence_split` event on each ferie row says so, carrying `demandedOn` and
  `postponedUnder` (`9 (1)` or `9 (2)`), class E. A demand is never dated before the
  sykmelding's first day (400: a demand follows the sickness). Today's § 9 sets no least
  number of sick days for either paragraph (checked against Lovdata, 2026-10-14), so a
  single wholly sick day may be postponed; § 9 (2)'s demand is to come "uten ugrunnet
  opphold" once the work is resumed, which HR judges. Sickness that began before a ferie's
  first day demanded after the last working day before it is
  `holiday_postponement_too_late`; a graded sykmelding — the employee is not wholly
  unable to work — or one over no approved ferie is
  `holiday_postponement_not_applicable`. The answer is the ferie rows as they stand.
- **A graded sykmelding over approved ferie** stands beside it, and no demand splits it
  (reading 25).
- **An egenmelding under a sykmelding** is trimmed to the days before (or after) it, and
  removed when wholly covered (`absence_split`); the case stays the same (reading 24).

### Egenmelding

An egenmelding (`sick_self`) is judged under the person's lock against the
self-certification policy in the [settings](#settings) (ftrl. § 8-23, § 8-24;
pre-flight I2):

- **An occurrence** is a maximal run of contiguous egenmelding days: rows that touch,
  or that only Saturdays, Sundays and public holidays lie between, are one — counted
  in **calendar days, the days off included** (R08-00 § 8-24: "også arbeidsfrie dager
  tas med"). Days a sykmelding covers join the egenmelding on either side too — one
  illness is one occurrence — and count no self-certified day (review M4): Monday to
  Wednesday cut by a Tuesday sykmelding is one occurrence of two days. Rows further
  apart are two occurrences, even within one case. One occurrence is at most the
  policy's days: 1+1+1+1 on four consecutive weekdays is refused on day four under the
  minimum's three, and egenmelding Friday and again Monday is day four.
- **The rolling year** — every 365 days that hold a day of the row, later occurrences
  counted too, so a backdated egenmelding is judged in each rolling year it lies in
  (review L10): under `minimum` at most four occurrences; under `extended` at most 24
  days; under `custom` both by the stored numbers. The limits **key on the policy's
  name**: under `minimum` and `extended` the preset's numbers are used whatever the
  settings row holds.
- **The case**: no day past its case's sixteenth sick day — after the
  arbeidsgiverperiode a sykmelding is needed.
- **Employment**: the first day at least `self_cert_after_months` after the start of
  the employment in force on it (`self_certification_too_early`).
- **The withdrawal**: not from the day HR decided to withdraw it until its last day
  (`self_certification_withdrawn`); days before the decision stand (review L1).

Past a limit is `self_certification_exhausted`; each refusal is a 409 whose detail says
why and tells the person to register a sykmelding.
`GET /people/{id}/self-certification` (the person, their manager, the Approver and HR)
and `GET /me/self-certification` answer the standing today: the policy and its
numbers, the occurrences and days used in the rolling year ending today, the day the
months of employment allow it from (`eligibleFrom`) and — to the readers of the
care-day fields alone — the withdrawal.

### The withdrawal

`PUT /people/{id}/self-certification/withdrawal {until, reason}` (`staff:manage`)
withdraws a person's right to egenmelding (ftrl. § 8-27) until a day after today and at
most a year ahead, with a reason of 1–500 characters; `{"until": null}` lifts it. It
applies from the day it is decided (`withdrawnFrom`), never to days before; re-issuing
a withdrawal still in force — to extend it, say — keeps that day. The three
columns are on the person (`self_cert_withdrawn_from`, `_until`, `_reason`, class E). The
events `self_cert_withdrawn` and `self_cert_restored` carry their fields with their
classes (E) and the marker `selfCertWithdrawal`, and are listed **only to the readers
of the care-day fields** — the person, `staff:manage` and `staff:personal-data` — never
to a manager or the Approver (pre-flight I6), who read the standing without it.

### Care days

`GET /people/{id}/care-days?year` and `GET /me/care-days?year` answer a person's care
days (omsorgsdager) for a calendar year (ftrl. § 9-6, Revision 2 Q7b), derived from
the [care-day fields](#the-care-day-fields). With `n = childrenUnder12 +
chronicallyIllChildren` (reading 29):

| | Days |
| --- | --- |
| `n` = 0 | 0 |
| one or two children | 10 (20 for a sole carer) |
| three or more | 15 (30 for a sole carer) |
| each chronically ill or disabled child, on top | 10 (20 for a sole carer) |

So two children under twelve and one chronically ill child are three, 15 + 10 = 25; a
sole carer of one chronically ill child has 20 + 20 = 40. `used` is the year's
registered child-sick (`sykt_barn`) rows' working days on which the person had the
right — four weeks into the unbroken employment the day lies in, an employment that
began the day after another ended continuing it (review L10); a day before it is no
care day and is counted apart (`ineligibleDays`). `employerPaid` the first ten,
which the employer bears itself, `beyondEmployer` the rest, which NAV refunds
(ftrl. § 9-8). When the only children who qualify are over twelve with extra days, NAV
refunds from the first day — HR and payroll apply that; the module does not model it.
Using more than the entitlement is shown (`exceeded`, `remaining` below zero), never
refused. The right needs four weeks' employment (§ 9-8): `eligible` is false until
`eligibleFrom`, four weeks after the start of the unbroken employment in force today.
The answer says what the fields hold, so it is read by the fields' own readers — the
person, `staff:manage`, `staff:personal-data` — and is the 404 of an unknown person to
anyone else, a manager and the Approver included (plan reading 34). Omsorgsdager are never reported to the
a-melding as leave.

## The personal-data exports

A person's data in one JSON file (design S16): `exportedAt`, the `person` — every field
of the record, the [care-day fields](#the-care-day-fields) included — their
`nextOfKin`, `roles`, `employments` (each `{employment, terms}`, the terms oldest
first), the self-service `link`, the `documents`' metadata (the files themselves are
read through `GET /documents/{id}`), every absence (`absences`, newest first, in any
status), the [holiday adjustments](#adjustments-and-carry-over) (`holidayAdjustments`),
every [sick-leave case](#sick-leave-cases) with its milestones (`cases`), this year's
[care days](#care-days) (`careDays`), the standing of
[egenmelding](#egenmelding) today with the [withdrawal](#the-withdrawal)
(`selfCertification`) and every event of their history, oldest first. Each absence
array is shaped as the file's reader reads it: the complete file whole, the person's
own copy as the person reads their own — both read every type as itself, a milestone's
note, the care-day fields and the withdrawal. Both exports read it all in **one** REPEATABLE READ snapshot, so its parts
agree with each other, build it in memory — one person's data is one response, and it is
never refused for size — and answer it as an attachment named
`staff-<employeeNumber>-personal-data.json`, `Content-Type: application/json`, with its
`Content-Length` and `Cache-Control: private, no-store`. The directory is asked only
after the snapshot: the linked user's name, and the viewer's for the audit.

- **`GET /people/{id}/personal-data`** (`staff:personal-data`) is the **complete file**,
  the module's answer to an innsyn request (GDPR art. 15): every field group, HR's
  `notes`, the hold fields, `nationalId` with its `country` and `value` and
  `bankAccount` with its `value` — the sealed values opened — the terms' `note` and
  pay, the health documents, and every event **whole**, its payload as stored. It is an
  **audited reveal**: once the file is read, a `personal_data_exported` event naming the
  viewer is recorded under the person's lock in a transaction of its own, and a file
  whose export could not be recorded is not answered. The event is not in the file it
  records. An erased person's file is what is left of them.
- **`GET /me/export`** (`staff:access`, the caller's linked person, else `no_person`)
  is the person's **own copy**: the same shape, without the sealed values —
  `nationalId` is `{kind, onFile}` and `bankAccount` `{kind, last4}`, as the person reads
  them on their record (reading 31: a stolen session should not get them) — without
  HR's `notes`, the terms' `note` and the hold fields (reading 35: the UI does not show
  them either), and with each event shaped as the person reads their history on My
  page. It records no event: it is the person's own.

## The register's import and export

The register travels as one CSV file (design S19): `GET /people/export.csv` writes it,
`GET /people/import-template.csv` starts one by hand and `POST /people/import` reads it
back. All three are `staff:manage`'s. The form is the Customers and Expenses files':
UTF-8 with a byte order mark, semicolon-separated, CRLF line ends, a header row, RFC
4180 quoting, ISO dates and the decimal comma. A cell beginning with `=`, `+`, `-`, `@`,
a tab or a carriage return is written with an apostrophe in front, so nothing a person
typed becomes a formula in a spreadsheet; the import takes that apostrophe off again.

### The columns

The header names are the contract's own field names, matched without regard to case.

| Group | Columns | Export | Template | Import |
| --- | --- | --- | --- | --- |
| Key | `employeeNumber` | yes | yes | required on every row |
| Work | `givenName`, `familyName`, `displayName`, `workEmail`, `phoneWork`, `workCountry`, `preferredLanguage` | yes | yes | yes |
| Private | `birthDate`, `gender`, `phoneMobile`, `phonePrivate`, `privateEmail`, `street`, `postalCode`, `city`, `country` | yes | yes | yes |
| Sealed | `nationalIdKind`, `nationalIdCountry`, `nationalId`, `bankAccountKind`, `bankAccount` | never | `staff:personal-data` only | `staff:personal-data` only |
| First employment | `startDate`, `businessUnit`, `employmentType`, `temporaryGround`, `expectedEndDate`, `ftePercent`, `appointmentForm`, `workingHoursScheme`, `fullTimeHoursPerWeek`, `occupationCode`, `title`, `department`, `location`, `managerEmployeeNumber` | no | yes | on a create only |
| Export-only | `status`, `currentTitle`, `currentDepartment`, `currentBusinessUnit`, `currentLocation`, `currentManagerEmployeeNumber`, `currentFtePercent`, `nationalIdOnFile` | yes | no | ignored |

HR's notes are in none of them, nor are the [care-day fields](#the-care-day-fields)
(plan Task 6): the register's file is a bulk copy that travels to spreadsheets, and the
fields are a family's circumstances — one of them, `chronicallyIllChildren`, borders on
health data (open question 8) — kept to the person's form, where HR types them one
person at a time. An import keeps what HR typed. A column named `error` — what a failed-rows file adds
— is ignored too, so a failed-rows file imports as it is.

### The export

`GET /people/export.csv` is every person, erased ones included, oldest first: the key,
the work and private groups, and where each stands today in the business time zone —
`status` (`active`, `upcoming`, `ended` or `none`), the title, the department's code,
the business unit's and the location's names and the manager's employee number of the
employment their place today is read from (as on their record), and
`currentFtePercent`, the percentages of every employment in force today added up. The
**sealed values are never in it** (reading 10): `nationalIdOnFile` says `yes` or `no`,
and nothing of the bank account is written, not its kind and not its last four digits.

It is read in one snapshot and answered as an attachment named
`staff-people-YYYY-MM-DD.csv` (the day in the business time zone), with
`Cache-Control: private, no-store`. More people than `STAFF_IMPORT_MAX_ROWS` (5000
unless the operator raised it; see
[the configuration reference](/en/admin/authentication/#configuration-reference)) is
the 400 `export_too_many`. Every export is recorded as the installation's
`people_exported` event naming the exporter and the number of rows (reading 11 — a
departure from Customers, whose export records nothing, because S21 asks for every
export to be an event); a file whose export could not be recorded is not answered.

### The template

`GET /people/import-template.csv` is the header row alone: the key, the person's
columns, the sealed ones only for a caller holding `staff:personal-data`, and the first
employment's — every column the caller's import can write, so a template filled in and
sent back is never refused for its header.

### The import

`POST /people/import` takes the file as the multipart part `file` and the query flag
`dryRun`, which defaults to `true`. The router allows the operation a body of 5 MiB and
64 KiB.

**The file and its header** are judged before any row, every refusal at once, each a
400 on `file`: more than 5 MiB is `import_too_large`; a comma-separated file is
`import_not_semicolon`; more data rows than `STAFF_IMPORT_MAX_ROWS` is
`import_too_many_rows`; a file that is not UTF-8 or not a CSV file, a part missing,
repeated or empty, a header with a column the table does not know (named, never
silently ignored), a repeated or nameless column, no `employeeNumber` column, or half of
a sealed pair (`nationalId` with `nationalIdKind`, `nationalIdCountry` only with them,
`bankAccount` with `bankAccountKind`) is a plain field error. A **sealed column** from a
caller without `staff:personal-data` refuses the whole file, `import_sealed_columns`,
rather than being skipped: a skipped column is a change the sender believes was made.
`dryRun` other than `true` or `false` is a 400 on `dryRun`.

**Each row**, in file order, is keyed on its `employeeNumber`, which every row needs:

- A number no person has — however it is cased — **creates** the person, the row's
  person columns as `POST /people` takes them (a blank display name derived, a blank
  work country `NO`, a blank language `nb`). When `startDate` is filled in, the row also
  creates the person's **first employment** with its first terms row, as
  `POST /people/{id}/employments` takes them, in the same transaction (reading 9):
  `businessUnit` by its name or its org number (the default unit when blank; a name two
  units share is refused), `department` by its code, `location` by its name and
  `managerEmployeeNumber` by the person who has it; `ftePercent` and
  `fullTimeHoursPerWeek` take either decimal mark. A filled first-employment cell without
  a `startDate` is the row's error. The manager must be in the register already or on
  an earlier row of the file.
- A number a person has **updates** that person's own columns, and only those the file
  carries — a blank cell clears an optional field, a column not in the file keeps its
  value — as `PUT /people/{id}` would, without a revision. An update **never touches
  employments**: the first-employment cells of such a row are not read. A row that
  changes nothing writes nothing, as a `PUT` that changes nothing.
- A filled **sealed cell** writes its value through the sealed paths — checked by its
  kind, sealed and, for a national identity number, MAC'd, with the
  `national_id_duplicate` and `national_id_birth_date_mismatch` rules — and a blank one
  changes nothing (spec §9). A bank account that changes mails the person, as
  `PUT /people/{id}/bank-account` does, **once that row has committed**, and never in a
  dry run. The mail is sent there and then, row by row and synchronously, so an
  onboarding import of N people with bank accounts sends N mails and takes as much
  longer as N sends do — a first account mails too, as by hand.
- An update row that changes **`birthDate` and `nationalId` together** is refused
  `national_id_birth_date_mismatch`: the person's columns are written first, and a
  birth date that changes is judged against the number on file, which still encodes the
  old one. Make the change in two steps, as by hand: a row with the new `nationalId` and
  `birthDate` blank (a birth date cleared is judged against nothing, and a number set on
  a person without one is judged against nothing), then a row with the new `birthDate`,
  which the new number now encodes.

Each row is **its own transaction** through the endpoints' own validation, statements
and events — `person_created`, `person_updated`, `employment_created`, `terms_added`,
`national_id_set`, `bank_account_set` — the importer as their actor. An update takes the
person's lock, and an [erased person](#the-erased-state) is read-only: their row is
refused `person_erased` when it would change a person column or a sealed value, and the
row an export wrote for them, unchanged, writes nothing and counts as `updated` like any
unchanged row — so an export taken after a retention run still imports back. A row that
fails writes nothing and does not stop the file. Two rows of one file with the same
employee number, however cased, the same work email or the same national identity
number (compared by its MAC, the refusal naming the earlier row and no digit) refuse
the second, in a dry run as in a real one.

A **dry run** is the real run rolled back row by row: the same statements, locks and
events, none kept — no person, no employee number, no event and no mail. It cannot see
an earlier row's effect on a later one, with one exception made for it: a
`managerEmployeeNumber` naming a person an earlier row of the file creates is taken as
found, and the row is judged without its manager.

The answer is `{dryRun, rows, created, updated, failed, errors}`: `updated` counts every
row that matched a person, changed or not, and each error is `{row, column, message}`,
`row` the data row counted from 1 under the header (a row whose every cell is blank is
skipped and counted not at all), `column` the header's own spelling, absent for a
problem with the row as a whole. **No message carries a digit of a national identity
number or a bank account**, and neither does a log line or an event. A real run is
recorded as the installation's `people_imported` event carrying the four counts — also
a real run cut short because its caller went away, with the counts so far: the rows it
ran stay committed, so the event is written on a context the request's end does not
cancel, bounded by a timeout of its own. On a run cut short, `rows` is the file's row
count while `created`, `updated` and `failed` count only the rows processed before the
cut, so they do not sum to `rows`. A dry run records nothing.

## The payroll export

`GET /payroll/export?month=YYYY-MM&format=csv` hands a month's employments to payroll
(design S18). It is `staff:manage`'s; anyone else gets the uniform 403. The rows are the
module's own; what file they become is a **payroll port**'s — `PayrollExporter`, an
interface inside the module with one implementation today, `csvExporter`, chosen by
`format` — so a payroll system that wants its own file is one implementation more and
not a change to the rows.

**The parameters.** `month` is required and is `YYYY-MM` with a month from `01` to
`12`; anything else — an empty `month` and a request without one included — is the 400
`payroll_month_invalid` on `month`. `format` defaults to
`csv`, the one there is; any other value, `CSV` and an empty one included, is the 400
`payroll_format_unsupported` on `format`. Neither refusal is an export, so neither is
recorded.

**The rows.** One row per employment **in the month** — started on or before its last
day, and open-ended or ended on or after its first day — erased people's included (who
was employed when is class C and outlives the erase, and payroll may need a past month
again). The rows are ordered by employee number, then employment number. Each row's
terms are those [in force](#the-derived-dates) on the **month's last day**, or on the
**end day** when the employment ends earlier in the month: the last day it still had
terms. A row dated after that day — a change agreed for the next month — is not yet in
force. An employment with no terms row in force that day keeps its row with every terms
column blank (validation dates the first row on the start, so this does not happen in
practice).

**The file** (`format=csv`) is the Expenses form: UTF-8 with a byte order mark,
semicolon-separated, CRLF line ends, RFC 4180 quoting, ISO dates and the decimal comma
(`37,50`), each cell through the same formula guard as [the register's
file](#the-registers-import-and-export) — so an employee number that starts with `-`
(which the number's pattern allows) is written `'-12`, and payroll will not strip the
apostrophe. The header is sentence-case English (reading 12), in this order:

| Column | Source |
| --- | --- |
| `Employee number` | The person's employee number. |
| `Name` | The given name and the family name, separated by a space (not the display name). |
| `Employment number` | The employment's number, the a-melding `arbeidsforholdID`. |
| `Business unit org number` | The org number of the employment's business unit (the virksomhet); blank when the unit has none. |
| `Start` | The employment's start date. |
| `End` | The end date — **only in the month the employment ends**; blank in every other month. |
| `End reason` | Skatteetaten's sluttårsak code for the end reason, in the end month only (below). |
| `Occupation code` | The STYRK-08 occupation code (yrkeskode), as stored. |
| `Full-time hours` | The hours per week of a full position. |
| `FTE percent` | The position percentage. |
| `FTE changed` | [`fteChangedAt`](#the-derived-dates), judged on the same day as the terms. |
| `Working hours scheme` | Skatteetaten's arbeidstidsordning code (below). |
| `Appointment form` | Skatteetaten's ansettelsesform code (below); the three 2025 forms' strings are unverified against the kodeliste — check before wiring a payroll system to them. |
| `Remuneration type` | As stored: `monthly`, `hourly`, `commission`, `fee` or `piecework`. Skatteetaten says [not to report avlønningstype from 2019](https://www.skatteetaten.no/en/business-and-organisation/employer/the-a-melding/the-a-melding-guide/employment/information-on-employment/remuneration-type/), so there is no code to translate it to. |
| `Salary changed` | [`salaryChangedAt`](#the-derived-dates), judged on the same day as the terms; blank when the terms in force have no pay components (none recorded, or erased). |
| `Department` | The department's **code**. |
| `Cost centre` | The terms' own cost centre, **else the department's**, else blank. |
| `Seniority date` | When set. |
| `Pay grade` | When set. |

The scheme, the end reason and the appointment form are written as the codes in
[the employments' table](#employments).

The **sealed identifiers are never read** (design reading 9): the national identity
number and the bank account are not in the file, nor anything derived from them —
payroll already holds them.

**The absence columns** (delivery B) are appended **to the right** of `Pay grade`, so a
payroll system's mapping of the first nineteen keeps working (plan reading 8). They are
fixed per kind, not one per type, and read every **approved or registered** absence; a
request, a refusal and a cancel count nothing.

| Column | On | Source |
| --- | --- | --- |
| `Leave ID` | a leave row | The leave's id: the lowest id among its rows, the a-melding `permisjonId` — it stays as rows are added to the chain, so it is the same every month the leave is reported. |
| `Leave start`, `Leave end` | a leave row | The leave's whole first and last day — before or after the month, a last day ahead when it is known. |
| `Leave description` | a leave row | The type's [a-melding description](#the-a-meldings-leave-descriptions). |
| `Leave percent` | a leave row | The leave's percent, always filled (> 0 and ≤ 100; 100 for a type without one). |
| `Holiday days` | the employment's row | Working days of ferie, by percent. |
| `Self-certified sick days` | the employment's row | Working days of egenmelding. |
| `Certified sick days` | the employment's row | Working days of sykmelding, by degree. |
| `Child sick days` | the employment's row | Working days of a child's sickness (care days), by percent. |
| `Child sick days beyond employer` | the main employment's row | Of the year's care days, summed from 1 January by percent — a day before the right's four weeks counting none (ftrl. § 9-8) — the part of this month's beyond the employer's ten: what NAV refunds. The person's, so on one row: the main employment, the highest full-time percentage in force, ties to the lowest employment number. |
| `Leave days` | the employment's row | Working days of leave, by percent. |
| `Time off hours` | the employment's row | The hours of time off (avspasering). |
| `Other days` | the employment's row | Working days of the other day types, by percent. |

A total counts the working days — Monday to Friday, not a public holiday — of the
employment within the month, each by the row's share; it is blank when nothing was
counted. A sick day **beside leave** counts degree × (100 − the leaves' percent) / 100,
the leaves summed and capped at a whole day, so sickness on a day of 100 % leave counts
nothing (Revision 3 M1). **A ferie day is a holiday day and counts no sickness** — no
sykepenger are paid on a holiday day, and an egenmelding cannot interrupt one — so a
graded sykmelding or an egenmelding over ferie counts as holiday alone; **a 100 %
sykmelding over approved ferie counts the day once, as sick**, and not as holiday,
until HR records the employee's [demand to postpone](#a-sykmelding-over-other-rows)
and the ferie is split (Revision 3 H1). An hour row of another kind than time off
counts in no total. **Concurrent employments**: a person with two employments in force
has the day totals on each — the absence is from both — and the days beyond the
employer's ten on the main one alone. An absence names no employment (a later
delivery's), so the file cannot tell which employment a leave is from: **its leave
rows repeat on every employment in force**, and payroll must check them by hand for
such a person.

**The leave rows.** After an employment's row comes one row per **leave** the
a-melding reports that touches the employment's days in the month, ordered by start: it
repeats the employee number, the name and the employment number and fills the leave's
five cells alone. A leave is a chain of approved or registered rows of one `leave`-kind
type with a description and one percent, each starting no later than the next working
day after the one before ended — a weekend or a public holiday between two rows does
not end it — so a leave extended by a second row is one leave, under the lowest id of
its rows. Skatteetaten's rules decide which are reported
([Permisjon](https://www.skatteetaten.no/bedrift-og-organisasjon/arbeidsgiver/a-meldingen/veiledning/arbeidsforholdet/opplysninger-om-arbeidsforholdet/permisjon/),
[Permittering](https://www.skatteetaten.no/bedrift-og-organisasjon/arbeidsgiver/a-meldingen/veiledning/arbeidsforholdet/opplysninger-om-arbeidsforholdet/permittering/)):

- a leave **longer than fourteen days**, whole or partial;
- a leave of fourteen days or fewer **when the person's leaves within the twelve months
  to the month's end together pass fourteen days** — each calendar day counted once,
  however many leaves lie on it;
- **permittering**, whatever its length;
- **never a child's sickness**: omsorgsdager are not leave, and no child-sick type
  carries a description; nor svangerskapspenger, which no type models.

**The descriptions' spellings** were checked on 2026-10-14: the a-melding's XSD types
`beskrivelse` as a plain string (`PermisjonsOgPermitteringsBeskrivelse`, NAV's
`a-arbeidsforhold` schema), so the values live in the kodeliste, and the seven the
types use — `permisjonMedForeldrepenger`, `permisjonVedMilitaertjeneste`,
`utdanningspermisjonLovfestet`, `utdanningspermisjonIkkeLovfestet`,
`andreLovfestedePermisjoner`, `andreIkkeLovfestedePermisjoner`, `permittering` — are
spelt the same in every NAV source that maps them (`fp-abakus`, `k9-abakus`,
`fp-oversikt`, `medlemskap-oppslag`). Skatteetaten's kodeliste file itself could not be
reached. **Before the export is wired to a payroll system**, compare the seven against
the kodeliste that system validates with.

The file reads every approved or registered absence of the installation, so a leave's
whole days are known however long ago it began; bounding that read to the leaves that
can reach the month is a later improvement, not a correctness matter.

**The order of work.** The parameters and the actor are judged first; everything the
file is built from — the people's numbers and names, the employments, their terms, the
business units, the departments and every approved or registered absence — is read in **one REPEATABLE READ snapshot**; the
rows and the file are built in memory; then the export is recorded as the
installation's **`payroll_exported`** event, naming the exporter, with `month`,
`format`, `rows` (the leave rows counted) and `sha256` — the hex SHA-256 of the very bytes answered, so the file
payroll received can be recognised later (reading 13). The event carries no personal
data, so it has no payload class, and it is kept as the export's lineage: its hash. A
file whose export could not be recorded is not answered. The file is served as
an attachment named `staff-payroll-YYYY-MM.csv`, with `Cache-Control: private,
no-store` and `X-Content-Type-Options: nosniff`.

## Retention

The register keeps what the law lets it keep, and the retention run erases the rest
(design S15). Every column of the tables the erase reaches has a **retention class**:

- **A** — pay and contract documentation: an employment's pay terms, its contract and
  attest documents and the pay in its events, kept five years after the year the
  employment ended.
- **C** — who was employed when: names, birth date, gender, employee number,
  employments, terms other than pay, roles, the link, the history's kinds and dates.
  Kept.
- **D** — personnel administration: contact and address, the sealed identifiers, the
  care-day fields, HR's notes, the terms' notes, the next of kin, absences' and
  holiday adjustments' notes, and documents of class D. Erased a grace period after
  the last employment ended.
- **E** — health: a sick-leave case's notes, estimate and milestones' facts, a sick
  row's type and degree, a child-sick row's type, the withdrawal of egenmelding, health
  documents (a restricted category) and the health fields of the events about them.
  Erased on [three schedules of their own](#what-is-due) — a closed case's, a year's
  child-sick rows', a lapsed withdrawal's — whatever the employment, and what none of
  them owns a longer period after the last employment ended.
- **F** — the legal hold, which stops every erase and is erased by none.

### The classes, column by column

The module's `columnClasses` (`retention.go`) is the erase's one source: the
statements that null a class are built from it, a test walks the database's columns of
these tables and fails on one it does not know (or a key no column has), a second
holds every payload field to its column's class, and a third holds this table to it.

| Table | Class | Columns |
| --- | --- | --- |
| `people` | C | `id`, `employee_number`, `given_name`, `family_name`, `display_name`, `birth_date`, `gender`, `national_id_kind`, `work_country`, `preferred_language`, `provision_account`, `provision_from`, `account_email_kind`, `erased_at`, `reassessed_at`, `reassessed_by_user_id`, `revision`, `created_at`, `updated_at` |
| `people` | D | `national_id_sealed`, `national_id_mac`, `national_id_country`, `work_email`, `phone_work`, `phone_mobile`, `phone_private`, `private_email`, `street`, `postal_code`, `city`, `country`, `bank_account_sealed`, `bank_account_last4`, `bank_account_kind`, `children_under_12`, `sole_carer`, `chronically_ill_children`, `notes` |
| `people` | E | `self_cert_withdrawn_until`, `self_cert_withdrawn_reason`, `self_cert_withdrawn_from` |
| `people` | F | `legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`, `legal_hold_at` |
| `next_of_kin` | D | every column: `id`, `person_id`, `position`, `name`, `relationship`, `phone`, `note`, `created_at`, `updated_at` |
| `person_roles` | C | every column: `id`, `person_id`, `role`, `from_date`, `to_date`, `created_at`, `updated_at` |
| `employments` | C | every column: `id`, `person_id`, `employment_number`, `relationship_type`, `business_unit_id`, `start_date`, `end_date`, `end_reason`, `contract_signed_at`, `holiday_scheme_id`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | C | `id`, `employment_id`, `valid_from`, `employment_type`, `temporary_ground`, `expected_end_date`, `appointment_form`, `hired_out_to`, `fte_percent`, `working_hours_scheme`, `full_time_hours_per_week`, `occupation_code`, `title`, `employee_type`, `department_id`, `location_id`, `manager_person_id`, `cost_centre`, `division`, `working_time_text`, `break_minutes`, `special_hours_text`, `overtime_rule_text`, `probation_end_date`, `probation_extension_agreed`, `probation_extended_to`, `probation_extension_reason`, `notice_employee`, `notice_employer`, `notice_unit`, `tariff_reference`, `holiday_terms_text`, `competence_development_text`, `social_security_text`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | A | `remuneration_type`, `pay_components`, `pay_method`, `payday_text`, `seniority_date`, `pay_grade` |
| `employment_terms` | D | `note` |
| `identity_links` | C | every column: `id`, `person_id`, `provider`, `external_id`, `state`, `managed`, `blocked_reason`, `last_error`, `attempts`, `next_attempt_at`, `lease_id`, `lease_until`, `last_synced_at`, `last_spec_hash`, `created_at`, `updated_at` |
| `documents` | the category's | every column: `id`, `person_id`, `category_id`, `employment_id`, `object_key`, `file_name`, `content_type`, `size_bytes`, `valid_to`, `signed_at`, `uploaded_by_user_id`, `case_id`, `revision`, `created_at`, `updated_at` — a document is erased whole, by its [category's](#categories-and-classes) class |
| `absences` | C | `id`, `person_id`, `type_id`, `first_day`, `last_day`, `hours`, `percent`, `status`, `case_id`, `requested_by_user_id`, `decided_by_user_id`, `decided_at`, `self_approved`, `revision`, `created_at`, `updated_at` — the type is C on the column; the health erase writes the reserved `absent` in place of a sick or child-sick one |
| `absences` | D | `note`, `decision_note` |
| `absences` | E | `degree_percent` |
| `holiday_adjustments` | C | `id`, `person_id`, `year`, `days`, `reason`, `from_year`, `by_user_id`, `created_at`, `updated_at` |
| `holiday_adjustments` | D | `note` |
| `sick_cases` | C | `id`, `person_id`, `first_day`, `last_day`, `closed_at`, `revision`, `created_at`, `updated_at` |
| `sick_cases` | E | `accommodation_note`, `max_benefit_estimate` |
| `sick_case_milestones` | C | `id`, `case_id`, `kind`, `due_date`, `revision`, `created_at`, `updated_at` |
| `sick_case_milestones` | E | `done_at`, `done_by_user_id`, `waived`, `waived_reason`, `waived_by_user_id`, `note` — `waived` cannot be null and is erased to `false` |
| `absence_types` | C | every column: `id`, `key`, `name_en`, `name_nb`, `kind`, `unit`, `paid`, `draws_holiday`, `allows_percent`, `needs_decision`, `visibility`, `amelding_leave`, `seeded`, `hidden`, `sort`, `revision`, `created_at`, `updated_at` |
| `holiday_schemes` | C | every column: `id`, `name`, `entitlement_virkedager`, `extra_from_60_virkedager`, `main_period_virkedager`, `after_september_virkedager`, `max_carry_over_virkedager`, `is_default`, `seeded`, `archived_at`, `revision`, `created_at`, `updated_at` |

Ids, foreign keys, revisions and timestamps are C: they say that a row existed and
when. An event's `payload_classes` are its fields' classes (`fieldClasses` in
`events.go`, held to the columns above); a document's event takes its category's; an
absence, adjustment, case or withdrawal event carries its own map of each field's
class ([the absence events](#the-absence-events)), and the types beside its fields
(`typeIds`) carry theirs (`typeClasses`).

### What is due

The run asks six questions, each over what remains, "today" being the settings'
time zone's civil day:

- **D** — a person not yet erased, not held, with **at least one employment and every
  one ended before today minus `retentionGraceDays`** (default 90). A person with no
  employment, or with one that has not ended, is never erased.
- **health** — a person, erased or not, not held, whose every employment ended before
  today minus `healthRetentionDays` (default 365), with a document in a restricted
  category filed under no case still on file, or a value of class E not yet replaced in
  an event no other schedule owns. The other schedules own the events of a row in a
  case or of a child-sick row, of a ferie split for a sykmelding in a case (the event
  names it, `sickAbsenceId`), of a case that exists or of a row in one (an absence
  event names its row's case, `rowCaseId`, so a row deleted from a case stays the
  case's), and of the withdrawal; what none
  owns stays here too.
- **cases** — a sick-leave case, of a person not held, **whatever their employment**,
  closed — by HR, or sixteen days after its last day, whichever came first — before
  today minus `healthRetentionDays` (spec open question 1: 365 days, built as
  designed), with something of class E left: a value of the case's or its milestones'
  E columns, a row still of a sick type or with a degree, a document filed under it, or
  a health field of an event it owns.
- **childSick** — a person, not held, with a child-sick row whose year ended before
  today minus `healthRetentionDays` (plan reading 21).
- **withdrawal** — a person, not held, whose [withdrawal](#the-withdrawal) of
  egenmelding lapsed before today minus `healthRetentionDays`, or — none in force —
  with a withdrawal event recorded before then that still holds a value.
- **orphaned sick events** — reported as `cases` — a person, not held, whatever their
  employment, with an absence or case event that nothing owns any more (its row, its
  case and the sykmelding it names all gone: a case removed with its only row, a case
  merged away before the merge was recorded), recorded more than `healthRetentionDays`
  and sixteen days ago — the latest its case could have closed — that still holds a
  health value.
- **A** — per **employment**: ended in a year more than five years before this one
  (ended in 2020, due from 1 January 2026; ended in 2021, not before 2027), of a person
  not held, erased or not, with something of class A still there — a pay column set on
  one of its terms rows, a document of class A filed against it (or filed against no
  employment while it is the person's last one — the latest to start), a pay field
  in one of its events not yet replaced, or — while it is the person's last one — a
  value not yet replaced in an event (`document_added`, `document_updated`,
  `document_removed`) of class A about a document that no longer exists.

### What an erase does

One person's erases run in **one transaction**, under the person's lock, each question
asked again on the row the lock holds — a hold set, an employment reopened or a rehire
since the cycle chose the person is seen there:

- **D** nulls every D column of the person and of their terms rows, deletes their next
  of kin, deletes their documents of class D, replaces every value of class D in their
  history's payloads, and sets `erased_at` — the person is then
  [read-only](#the-erased-state).
- **health** deletes their documents in restricted categories filed under no case and
  replaces the class E values of the events no other schedule owns.
- **cases**, for each due case, erases its class E columns and its milestones' (the
  case keeps its id, its days and when HR closed it; a milestone its kind and date),
  writes the reserved `absent` type in place of its rows' sick type and nulls their
  degree, deletes the documents filed under it, and replaces the health fields — and
  the `typeIds` of class E — of the events it owns: its own, those of the cases it took
  in by a merge, its rows', and those of a ferie split for one of its rows.
- **childSick** writes `absent` in place of the due rows' type and replaces the health
  fields of their events.
- **withdrawal** nulls `self_cert_withdrawn_from`, `_until` and `_reason` when the
  withdrawal on file lapsed before the cutoff, and replaces the values of the
  withdrawal's events recorded before it — each event by its own instant, so one
  recorded since waits for its own time.
- **orphaned sick events** have their health fields replaced, as a case's would be.
- **A**, for each due employment, nulls its pay columns on every terms row, deletes its
  documents of class A, and replaces the values of class A in its events and every
  value of the deleted documents' events. The erase of the person's **last**
  employment also replaces every value of the events of class A about a document that
  no longer exists — a contract or an attest deleted by hand before its erase, which
  no longer says which employment it was filed against, so its file name and dates do
  not outlive the pay documentation. A rehired person's old employment loses its
  pay while the current one keeps its own.

A value replaced in a payload keeps its field's name and reads `"changed"` — what a
reader who may not see the field was already shown, now shown to everyone, HR
included. The event keeps its kind, actor, instant and `payload_classes`. Nothing of
class C is touched: names, birth date, gender, employee number, employments, non-pay
terms, roles, the link, the history's kinds and dates.

A deleted document's row goes and its object's key is queued in the
[deletion ledger](#the-deletion-ledger) in the same transaction; the erase never calls
the object store.

When anything was erased, the transaction records one **`erased`** event for the
person, `{"kinds": [{"kind": "D" | "health" | "A" | "cases" | "childSick" | "withdrawal", "count": n}]}`, its actor the
installation (`system`) when the worker ran it and the caller when
[`POST /retention/run`](#running-it-by-hand) did. `count` is the items the kind
removed: each column value nulled, each row deleted (a next of kin, a document) and
each event payload rewritten. A D erase is always reported, since it marks the
person, even when it found nothing to remove (count 0); an A count sums the person's
due employments. The event touches no class, so no later erase rewrites it. It is
listed as the [hold's kinds](#the-history) are — not to a manager, to the person as its
kind with an empty payload — and its kinds are read by `staff:manage` and
`staff:personal-data`, the `health`, `cases`, `childSick` and `withdrawal` entries — each
says the person's health was on file — by `staff:personal-data` alone.

The run is **idempotent**: a second run finds nothing due and writes nothing. That is
what makes "once per civil day" hold whatever the poll and the clock do.

### The deletion ledger

`object_deletions` holds the objects whose rows a delete or an erase removed and that
the store has not yet confirmed gone (`object_key`, `attempts`, `next_attempt_at`,
`claimed_until`, `last_error`, `created_at`). A pass takes the due rows —
`next_attempt_at` reached, not claimed or claimed by a pass whose window has run out —
a hundred at a time, the longest waiting first, and claims each by a conditional update
for five minutes from the moment it is claimed, just before its attempt; a row another
replica won between the selection and the claim is left to it. Each claimed row gets one attempt:

- the store confirms — a key it no longer has counts as confirmed — and the row is
  deleted;
- the store refuses, and the row keeps: `attempts` one more, `last_error` a constant
  (`the object store refused the delete`; the store's own words go to the log, since
  they may name the key), `next_attempt_at` after the backoff — two seconds, doubling,
  at most an hour — and the claim given up. There is **no ceiling** on the attempts: a
  legal erase is never best-effort;
- no object store is configured, or it could not be built: the same, with `last_error`
  `storage_unavailable: …`, and the rows wait for storage.

No connection is held while the store is asked: every claim and every outcome is a
statement of its own on the pool. A hand [delete](#reading-changing-and-removing) makes
its one attempt through the same code.

### The worker

`staff-retention` runs every `STAFF_WORKERS_POLL` (default `1h`, at least `1m`) unless
`STAFF_RETENTION_ENABLED=0` — which stops every schedule, the cases' included: they
run in the same cycle, and there is no worker of their own (plan reading 5) — in worker
mode and in api mode with `WORKERS_IN_PROCESS=1`. Each poll:

1. **An erase cycle under the lease**: the session advisory lock
   `0x5354414646524554` (the ASCII `STAFFRET`), taken with `pg_try_advisory_lock` on one
   pooled connection. Another replica holding it means this one skips the cycle. The
   due queries and every person's transaction run **on that connection** — the person's
   lock included — never on the pool, so a cycle never waits for a second connection
   while holding the first. At most fifty people a cycle, each schedule's oldest end
   first; a backlog drains over cycles. One person failing is logged by id, rolled back,
   and due again next cycle. The lease is released on a context stripped of
   cancellation; an unlock that fails closes the connection.
2. **A ledger pass**, with no lease.

The worker starts without storage: in worker mode its object store is built from the
configuration, and an unconfigured one fails only the ledger's attempts.

### Running it by hand

`POST /retention/run` (`staff:manage`) runs both steps now, by the same code with the
same connection disciplines, the caller as the `erased` events' actor. It answers 200
`{"erased": {"kinds": [{"kind", "count"}]}, "deletions": {"attempted", "succeeded"}}` —
the kinds summed over the people erased, a kind no erase ran for absent. Its ledger
pass is bounded at **thirty seconds** in all, so a store that has stopped answering
does not hold the request: an attempt still under way then ends as a refusal, the pass
stops, `deletions` counts what it attempted, and the worker finishes the rest. While
another holder has the lease — the worker, here or on another replica, or another run
by hand — it is **`retention_busy`** (409) and runs nothing.

### The settings and the retention block

`retentionGraceDays` (0–3650, default 90) and `healthRetentionDays` (30–3650, default
365) are [settings](#settings). `GET /settings` also answers a read-only `retention`
block, derived from the ledger and the history — no column holds it (plan reading 8):
`pendingDeletions` (rows in the ledger), `failingDeletions` (rows the store has refused
at least once), `oldestPendingSince` (when the oldest row was queued; absent when the
ledger is empty) and `lastErasedAt` (the latest `erased` event; absent when nothing was
ever erased).

## The erased state

The retention run's [D erase](#what-an-erase-does) erases a former employee's personnel
administration (class D) and sets `erased_at`. What is left is **read-only** (design S15): every write
of the person answers `person_erased` (409) — the record (`PUT /people/{id}`,
`DELETE`), the next of kin, the roles, the national identity number and the bank
account, a link to a user, an employment and its terms (`PUT`, end, reopen, terms
added, replaced or deleted), a document (filed, changed, removed) and the person's own
`PUT /me`. The rule is judged once, where every write of a person starts: the person's
lock (`withPersonLock`) reads `erased_at` on the row it holds and refuses before the
write runs, so an erase cannot slip in between the check and the write, and no write
path can forget it.

The **exceptions**:

- **`PUT /people/{id}/legal-hold`** — the [legal hold](#the-legal-hold).
- **`POST /people/{id}/reassessed`** — the [reassessment](#the-reassessment).
- **`PUT /people/{id}/user` with `userId: null`** — the unlink, so the user can be linked
  to someone else. A link to a user is refused.
- **`POST /people/{id}/employments`** — a **rehire**: the employment is added and, in the
  same transaction, `erased_at` and the reassessment are cleared and a `person_rehired`
  event names what was cleared, before the employment's own events. The record is
  writable again under the same employee number; what the erase blanked stays blank
  until HR types it.

**Reads stay open**: the record, the history, the documents and their downloads (class
A documents live five years for claims), the employments, a reveal (which finds
nothing: the erase took the number), both exports. The retention run's own health and
A erases still reach an erased person: they take the person's lock allowing an erased
one, and the D erase's due question is what passes over a person already erased. An
export of an erased person writes their erased columns empty, and that row imports
back unchanged.

**The rehire warning.** The erase removes the national identity number and its index,
so a person who comes back cannot be recognised by it. `POST /people/similar-erased`
(`staff:manage`) takes `{givenName, familyName, birthDate}` — a body, not a query, so
the names stay out of access logs — and answers the erased people with the same names,
however cased, and the same birth date, most recently erased first, as
`[{id, employeeNumber, displayName, erasedAt}]` (`[]` when none). A missing or empty
name or birth date is a 400 naming it. The creation form uses it so that a second
record is a choice: rehire the erased one, or add someone new.

## The legal hold

`PUT /people/{id}/legal-hold` (`staff:personal-data`) takes `{held, reason}`. Setting it
(`held: true`) needs a `reason` of 1 to 500 characters, trimmed; it records the reason,
who set it and when (`legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`,
`legal_hold_at`, class F) as a `legal_hold_set` event naming `legalHold` and
`legalHoldReason` before and after. A new reason on a hold that stands keeps who set it and when; its
event names who changed the reason. Lifting it
(`held: false`, no `reason` — one sent is a 400) clears all four as a
`legal_hold_lifted` event. `held` is required. Writing what is there already records
nothing. The answer is the person.

While a person is held, no retention erase touches them, and `DELETE /people/{id}` is
`legal_hold` (409). The hold moves no revision: it is not among the editable fields the
person's revision guards.

## The reassessment

An erased person raises a reassessment item twelve months after their last
employment ended (design S15; [`reassessmentDue`](#attention-items)).
`POST /people/{id}/reassessed` (`staff:personal-data`) records it: due when the person
is erased, not yet reassessed, every employment has ended, and today — the business
time zone's day — is on or after the last end plus twelve months (the 29th of February's
due day is the 28th). It records when and by whom (`reassessed_at`,
`reassessed_by_user_id`) as a `reassessed` event naming `reassessedAt`, and answers the
person. Anything else — too early, already reassessed, not erased, an employment that has
not ended — is `reassessment_not_due` (409). A rehire clears the reassessment with the
erasure.

## Stats and attention

The dashboard's two reads (design S20). Both are read in one snapshot — the people,
every employment with its terms, the departments, and the manager graph built from
those same rows, each table read once — and judged in Go on today, the day in the
settings' [time zone](#the-business-time-zone). The account items arrive with
delivery C.

### The figures

`GET /stats` is `staff:view`'s or `staff:manage`'s, judged in the module; anyone else
gets the uniform 403 (the `GET /people` rule). The two **sick figures** are present for
a caller who also holds `staff:manage`, `staff:approve` or `staff:personal-data`, and
absent for anyone else — a holder of `staff:view` alone among them (plan reading 14, a
spec amendment; open question 3's interim default). An employment is **in force** on a
day when it started on or before it and has not ended before it (the end day is still
worked).

| Figure | Rule |
| --- | --- |
| `headcount` | The people with at least one employment in force today; each person once. |
| `fteSum` | The sum of `ftePercent` of the terms row in force today of every employment in force today, divided by 100, to two decimals — a person with two employments counts both. |
| `startingSoon` | Employments whose start is after today and no later than today + 30 days. |
| `endingSoon` | Employments in force today whose end is today or no later than today + 30 days. |
| `peopleWithoutEmployment` | People not erased with no employment at all. |
| `onLeaveToday` | People with an employment in force today and an approved or registered absence of whole days covering today, of **any** kind — a figure without the sick ones would, beside the calendar's bars a colleague reads as "absent", tell them who is sick. A request counts nothing, so no HR-only request is counted. |
| `openSickCases` | Sick-leave cases not closed by HR whose last day is at most sixteen days ago. Sick figure. |
| `sickAbsenceRate` | The sick-leave rate over the twelve months ending today, in percent to two decimals (reading 7), as SSB and NAV count it: the working days lost — over every working day of every employment in force, the day's sick share, counted as in [the payroll file](#the-payroll-export) (by degree, beside leave the part left, a ferie day sick only under a 100 % sykmelding), times the `ftePercent` in force — over the working days agreed: the same days by the same percentage, less the share of each that was holiday or leave. Child sickness is not the employee's and counts nothing. Sick figure. |

### Attention items

`GET /stats/attention` is `staff:access`'s and shaped per caller: `staff:manage`'s
items, `staff:personal-data`'s, `staff:approve`'s and a manager's for the employments
they manage today (by the dashboard's manager rule below); a caller with none of these
gets `[]`, never a 403. A caller with none of `staff:manage`, `staff:personal-data` and
`staff:approve` is judged as a manager on the employments, their terms and the
departments alone, and gets `[]` at once when they manage nobody today; a manager's
read adds only their reports' names, the requests they decide and the open cases'
milestones. Every count and item shown to a caller without `staff:personal-data`
leaves out the requests of an HR-only type, so none reveals one.

The **dashboard's manager rule** is the [manager rule](#managers-and-the-org-view) —
the one the Manager audience and the org view read, a manager who has left skipped
(design §9, "a manager who leaves: their reports route to the department's manager") —
judged per employment on its terms in force today: the manager the terms name when they are in employment today; else the manager
of the terms' department, else of the first department above it, whose manager is
someone else in employment today; else nobody. It decides both the probation item's
manager and whether a person has a manager at all, so the two items never disagree. A
manager of one of a person's two concurrent employments is the manager of that one
only. Each item is `{id, type, title, occurredAt, entityId, count?}`, the shape
every module's attention list shares; a date-only `occurredAt` is that day at midnight
UTC. The list is oldest `occurredAt` first, then by `type` and `id`, and an item two of
the caller's roles find is listed once.

| `type` | Audience | Trigger | `occurredAt` | `entityId`, `count` |
| --- | --- | --- | --- | --- |
| `contractOverdue` | manage | An employment in force today with neither `contractSignedAt` nor a signed document of the `contract` category filed for it (`employmentId` set, `signedAt` set), from seven days after the start — from the start when, judged on the terms in force on the start day, the person is hired out (`fast_utleid`, `midlertidig_utleid`) or the row is temporary with an `expectedEndDate` before the start plus a month less a day (aml. § 14-5). The employment's `endDate` is not read: an end typed later — an employment ended early — does not make the contract due at once. A signed contract document filed for no employment suppresses nothing. One per employment. | The day it fell overdue. | The person. |
| `probationEnding` | manage; the employment's manager today by the dashboard's manager rule | An employment in force today whose terms in force today end the probation — `probationExtendedTo`, else `probationEndDate` — today or within 30 days. One per employment. | The probation's end. | The person. |
| `temporaryNoticeDue` | manage | An employment in force today whose terms in force today are `midlertidig`, on any ground, that started twelve months ago or more: a month's written notice of the end is owed (aml. § 14-9). One per employment. | The start + 12 months. | The person. |
| `temporaryConverts` | manage | A run of the person's temporary employments on ground `a` or `b` — each judged on its terms in force on its last day so far (today, or its end), taken by start, each starting no later than the day after the run so far ended (a gap of a day breaks the run; an employment on another ground is no part of it), one of them in force today — that started 36 months ago or more (aml. § 14-9 (7)). One per person. | The run's start + 36 months. | The person. |
| `reportsWithoutManager` | manage | A person with an employment in force today that has no manager today by the dashboard's manager rule (design §9). Not listed is the person nobody can manage by the structure: one whose terms in force place them in a department they manage, with no department above it naming a manager other than them — their climb starts and ends in their own department. That holds whatever manager their terms name, so the head of a top-level department whose named manager has left is not listed either; a person who manages a top-level department while their terms place them in another department, or in none, is judged like anyone else. One per person. | That employment's start. | The person. |
| `reassessmentDue` | personal-data | An erased person not yet reassessed, every employment ended, the last end twelve months ago or more ([the reassessment](#the-reassessment)). | The due day. | The person. |
| `retentionFailing` | manage | Ledger rows the object store has refused at least once (`attempts` ≥ 1) that were queued more than 24 hours ago. One item. The settings' `failingDeletions` counts every refused row, however young. | When the oldest of them was queued. | `retention`; `count` the rows. |
| `otpThreshold` | manage | By the `ftePercent` in force today summed per person over their employments in force today: someone at 75 % or more, or the people at 20 % or more adding up to 200 % (two full-time equivalents) — OTP-loven § 2 judged without an owner flag (plan reading 17), so the title names the exception, "unless the only such person is the owner". Not shown once `otp` is [acknowledged](#the-thresholds). One item. | Today. | `thresholds`. |
| `selfApproved` | manage | An approved absence of a type of visibility `absent` its person approved themselves ([self-approval](#decisions), provisional) that has not ended — never an HR-only type's, which staff:manage without staff:personal-data must not be told of. One per absence; `id` `selfApproved/<absence id>`. | When it was approved. | The person. |
| `holidayAffectedBySickness` | manage | Approved ferie a 100 % sykmelding lies over, until four weeks after the ferie's last day — one whose sickness began before it only until the holiday begins, when the § 9 (1) deadline has passed: the employee may demand to postpone it ([ferieloven § 9](#a-sykmelding-over-other-rows)). One per ferie row; `id` `holidayAffectedBySickness/<absence id>`. | The ferie's first day. | The person. |
| `headcountThreshold` | manage | For each of 5, 30 and 50 the `headcount` has crossed — 5 or more, 30 or more, more than 50 — and that is not [acknowledged](#the-thresholds). | Today. | The threshold (`5`, `30`, `50`); `count` the headcount. |
| `absenceAwaitingDecision` | a decider: approve, manage, a manager today of the person; an HR-only type's requests personal-data alone | The requests the caller decides — the [approvals inbox](#the-approvals-inbox)'s count, never their own (but the self-approver's). A request of an HR-only type counts for `staff:personal-data` alone. One item; `id` `absenceAwaitingDecision`. | When the oldest was made. | `approvals`; `count` the requests. |
| `hrOnlyAwaitingDecision` | personal-data (never manage without it) | Requests of an HR-only type undecided for more than seven days — the caller's own included, so the only holder of `staff:personal-data`, whose own request nobody may decide ([decisions](#decisions)), sees it stall (open question 9). One item; `id` `hrOnlyAwaitingDecision`. | The oldest's seventh day. | `approvals`; `count` the requests. |
| `sickMilestoneDue` | manage, approve; a manager today of the person | A [milestone](#the-arbeidsgiverperiode-and-the-milestones) due — the case open and lasted to within a week of the date, neither done nor waived (pre-flight I5) — dated within seven days or past. The end of the arbeidsgiverperiode is a date, never an action, and raises none; a three-day case raises none. One per milestone; `id` `sickMilestoneDue/<case id>/<kind>`. | The milestone's date. | The person. |
| `holidayUntaken` | manage | In January: a person with an employment in force today whose [holiday account](#the-holiday-account) for last year has days remaining and no carry-over from it recorded — a carry-in [automatic carry-over](#adjustments-and-carry-over) derives counts as recorded (pre-flight M6). One per person. | 1 January. | The person. |
| `mainPeriodShort` | manage | From 1 April to 30 September (reading 23): a person with an employment in force today that began before 1 June, with fewer approved ferie working days in 1 June – 30 September than that employment's scheme's main period converted — 18 virkedager are 15 days (ferieloven § 7 (1)). One per person. | 1 April. | The person. |

`id` is `<type>/<employment id>` for the employment items, `<type>/<person id>` for
the person items, `retentionFailing`, `otpThreshold` and `headcountThreshold/<key>`.
`title` is the person's display name — class C, so it survives the erase — with the
employment number after it, in brackets, on an employment item of a person who has
more than one; for the three installation types it is an English sentence, and for the
two request counts the type's name. The host words every type from its own catalog, in
the reader's language, and links a person's item to their page, the two request counts
to the Absence page and the other three to Settings.

### The thresholds

Four thresholds bring an employer a duty, and each raises an item until HR
acknowledges it:

| Key | Crossed when | The duty |
| --- | --- | --- |
| `5` | `headcount` ≥ 5 | Written whistleblowing routines (aml. § 2 A-6) and a safety representative, verneombud (§ 6-1). |
| `30` | `headcount` ≥ 30 | A working environment committee, AMU (§ 7-1). |
| `50` | `headcount` > 50 | The activity and reporting duty on equality, ARP (likestillings- og diskrimineringsloven § 26), an employer's with more than fifty employees: at exactly 50 there is no item. |
| `otp` | The OTP test above | An occupational pension scheme (OTP-loven § 2), unless the only such person is the owner. |

`POST /thresholds/{key}/acknowledge` (`staff:manage`) acknowledges one, **once and
permanently** (plan reading 18): in one transaction under the settings row's lock — the
lock `PUT /settings` takes — it adds `key: at` to `settings.thresholds_acknowledged`
and records a `threshold_acknowledged` event of the installation naming the caller and
`{key}`. It answers `StaffThresholdsAcknowledged`, every acknowledgement so far. A
threshold already acknowledged answers the same 200 and changes nothing — no second
event, the first date kept. A threshold need not be crossed to be acknowledged, and an
acknowledged one is never shown again, whatever the headcount does. The settings'
revision is left alone: the acknowledgement is not among the fields `PUT /settings`
replaces. Any key but the four is 404 `threshold_unknown`, a problem with its code
rather than a bare 404 — the keys are no secret.

## Refusals and revisions

Every refusal is an RFC 9457 problem, `StaffProblem`: `title`, `status`, `detail`,
and — when a rule of the design refused — `code`, which is what a client keys off,
never the words. A 400 carries `errors`, the field errors keyed by the request's
camelCase field; a plain validation failure has no code, a named rule (such as
`org_number_invalid`) carries one beside its field errors. A request the server
cannot decode — malformed JSON, a missing required header — is a bare 400. A list
element's field is named by its place, a nested one by its path: `nextOfKin[0].name`,
`terms.ftePercent`, `payComponents[0].amount`.

| Code | Status | When |
| --- | --- | --- |
| `org_number_invalid` | 400 | An organisation number that is not nine digits with a valid check digit. |
| `employee_number_too_long` | 400 | The prefix and the next number make a number longer than 16; a typed employee number longer than 16; a generation the skip carries past 16. |
| `employee_number_taken` | 400 | Another person has the employee number, however it is cased. |
| `work_email_taken` | 400 | Another person has the work email. |
| `person_in_use` | 409 | Deleting a person with an employment, a user link or documents, or named as a manager. |
| `legal_hold` | 409 | Deleting a person under a legal hold. |
| `reassessment_not_due` | 409 | Recording a reassessment of a person who is not erased, already reassessed, or whose last employment ended less than twelve months ago or has not ended. |
| `structure_archived` | 409 | A write newly names an archived row — a holiday scheme among them — or makes one the default. |
| `structure_default` | 409 | Archiving the default, or the legal entity the default business unit sits under, or taking the flag off the default in place — a holiday scheme's too. |
| `department_cycle` | 409 | A parent that is the department itself or below it. |
| `revision_conflict` | 409 | The `If-Match` revision has moved on. |
| `national_id_invalid` | 400 | Not a valid national identity number of its kind; a foreign id without its country. |
| `national_id_birth_date_mismatch` | 400 | The number encodes another birth date than the person's. |
| `national_id_duplicate` | 400 | Another person has the number. Names nobody. |
| `bank_account_invalid` | 400 | Not a valid BBAN or IBAN. |
| `user_inactive` | 400 | The user to link is disabled. |
| `user_linked_elsewhere` | 400 | The user to link is linked to another person. |
| `no_person` | 404 | `/me` for a caller linked to no person. |
| `employment_number_taken` | 400 | Another employment has the employment number, however it is cased. |
| `end_reason_required` | 400 | Ending an employment without its reason. |
| `terms_required_fields` | 400 | A terms row without `employmentType` or `ftePercent`. |
| `temporary_ground_required` | 400 | A temporary row without its ground. |
| `appointment_form_mismatch` | 400 | An appointment form the employment type does not take. |
| `probation_too_long` | 400 | A probation past six months or half a temporary term — on a terms row, or on a new start — or an extension not agreed, without a reason or too long. |
| `full_time_hours_mismatch` | 400 | Hours of a full position the scheme does not allow. |
| `terms_valid_from_taken` | 409 | Another row of the employment has the day. |
| `terms_before_start` | 409 | A row before the employment's start, or a new start after a row. |
| `terms_after_end` | 409 | A row after the employment's end, or an end before a row. |
| `terms_first_row_start` | 409 | A first row not dated the start; deleting the first row. |
| `terms_not_latest` | 409 | Replacing a row that is not the latest without `correction`, or deleting one. |
| `self_manager` | 409 | A terms row naming the person as their own manager. |
| `manager_cycle` | 409 | A terms or department write that would make someone their own manager, through others. |
| `document_type_unsupported` | 400 | A file that is not a PDF, JPEG, PNG or HEIC, or whose bytes disagree with its declared type or extension. |
| `document_too_large` | 400 | A file over 10 MiB. |
| `document_count_exceeded` | 409 | Filing a document for a person who has 200. |
| `category_restricted` | 403 | Filing, moving into or out of, or removing a health document without `staff:personal-data`. |
| `category_in_use` | 409 | Archiving a category documents are filed under. |
| `seed_immutable` | 409 | Changing a seeded document category; changing a seeded absence type beyond its names, order and `hidden`, or showing the reserved `absent`. |
| `holiday_scheme_in_use` | 409 | Archiving the default holiday scheme, or one an employment names. |
| `absence_type_in_use` | 409 | Widening a company absence type's visibility from `hr_only` to `absent` once an absence names it. |
| `percent_not_allowed` | 400 | A percentage on an absence of a type that allows none — a sick kind among them. |
| `hours_on_day_type` | 400 | Hours on an absence of a type counted in days; an hour type without hours or over more than one day. |
| `absence_note_on_sick` | 400 | A note on an egenmelding, a sykmelding or a sick child's absence. |
| `absence_overlap` | 409 | An absence that cannot stand beside the person's requested, approved or registered rows on one of its days. |
| `absence_not_requested` | 409 | Changing or deleting an absence that is not a request — a registered one by anyone but HR, a decided or cancelled one by anyone. |
| `absence_not_cancellable` | 409 | Cancelling an absence that is neither a request nor an approved row its decider may still cancel (begun: HR alone); correcting one that is neither approved nor registered. |
| `decision_not_yours` | — | Not answered (review C1): a caller who decides nothing of the person's gets the 404 of a row they cannot write. |
| `own_request` | 403 | Deciding, cancelling or correcting one's own absence, but for the self-approver. |
| `absence_status_change` | 409 | Changing an absence's type between one that needs a decision and one that is a notification. |
| `carry_over_recorded` | 409 | A second carry-over for one person from one year. |
| `self_certification_exhausted` | 409 | An egenmelding past the policy's days of one occurrence, its rolling year's occurrences or days, or its case's sixteenth sick day. |
| `self_certification_too_early` | 409 | An egenmelding before the policy's months of employment. |
| `self_certification_withdrawn` | 409 | An egenmelding from the day HR decided to withdraw the right until its last day. |
| `holiday_postponement_not_applicable` | 409 | A demand to postpone holiday on a row that is no 100 % sykmelding, or one over no approved ferie. |
| `holiday_postponement_too_late` | 409 | A demand to postpone holiday for sickness that began before it, made after the last working day before the holiday (ferieloven § 9 first paragraph). |
| `person_erased` | 409 | Any write of a person the retention run has erased but the hold, the reassessment, an unlink and a rehire. |
| `storage_unavailable` | 503 | The object store could not be written to or read from. |
| `import_too_large` | 400 | An import file over 5 MiB. |
| `import_not_semicolon` | 400 | A comma-separated import file. |
| `import_too_many_rows` | 400 | An import file with more data rows than `STAFF_IMPORT_MAX_ROWS`. |
| `import_sealed_columns` | 400 | An import file carrying a sealed column from a caller without `staff:personal-data`. |
| `export_too_many` | 400 | An export of more people than `STAFF_IMPORT_MAX_ROWS`. |
| `payroll_month_invalid` | 400 | A payroll export's `month` that is not `YYYY-MM` with a month from `01` to `12`. |
| `payroll_format_unsupported` | 400 | A payroll export's `format` other than `csv`. |
| `retention_busy` | 409 | `POST /retention/run` while the retention lease is held — by the worker, here or on another replica, or another run by hand. Nothing ran. |
| `threshold_unknown` | 404 | `POST /thresholds/{key}/acknowledge` with a key other than `5`, `30`, `50` and `otp`. |

An unknown id is a bare 404 with no body.

Every mutable row carries a `revision`, starting at 1 and moving on with each change.
A `PUT` requires `If-Match` carrying the revision the row was read at — bare (`3`) or
quoted (`"3"`). A revision that has moved on is `revision_conflict`, whose detail names
both revisions; an `If-Match` that holds no revision is a 400 on `If-Match`. The
revision is judged under the row's lock, so two writes at once queue and the second is
told the first won. Archiving needs no revision: it is idempotent. A person's next of
kin and roles carry no revision of their own: their replace is guarded by the
person's, which it moves on when the list changes.

## Locks

- **No call out of a transaction.** The user directory, the mail and the object store
  are reached before a transaction opens or after it commits, never inside one —
  neither a locked one nor a read-only snapshot, which holds a pool connection just the
  same. Every such call goes through one accessor — in `contractscalls.go`, or
  `objects.go` for the store (`objectPut`, `objectGet`, `objectDelete`) — which refuses
  a call made inside one of the module's transactions; the tests record every call and
  fail on any made inside one.
- **A person's row first.** Every mutation of a person's rows takes the person's row
  first (`withPersonLock`), then their employments; nothing locks two people. The lock
  is `FOR NO KEY UPDATE`: it queues every writer of the person as `FOR UPDATE` would,
  but does not block the `KEY SHARE` lock a foreign-key check takes when another row
  names the person — another person's terms naming them as manager, a department
  naming them as its head — so such a write never waits on this person while holding
  locks of its own. The lock is also where the [read-only rule](#the-erased-state) is
  judged, on the row it holds.
- **The employment's row next.** A write of an employment or its terms takes the
  person's row, then the employment's (`FOR NO KEY UPDATE` too).
- **The document's row last.** A write of a document takes the person's row, then —
  for a signed contract that sets an employment's signing date — the employment's,
  then the document's: person → employment → document. The category is held `FOR
  SHARE` while a document is filed under it, so an archive (`FOR NO KEY UPDATE` on the
  category) and an upload into it are judged one after the other.
- **The manager graph.** A write that changes an edge of the manager graph — a terms
  row naming, clearing or changing a manager or a department, a moved start, a
  reopening, a department's manager or parent — takes the transaction advisory lock
  `staff-manager-graph`, after any person's and employment's row, and runs the
  [cycle check](#managers-and-the-org-view) over the graph it leaves before it
  commits, so the checks across people are judged one at a time. An end takes no
  graph lock — it only takes edges away — unless it moves an earlier end later.
- **The defaults.** A write of a legal entity or a business unit takes its table's
  default lock first. A move of the default holiday scheme takes
  `staff-holiday-schemes-default`, then the scheme it moves to and the default it
  moves from, each `FOR UPDATE`.
- **A holiday scheme's row.** An employment write naming a scheme takes the person's
  row, the employment's, then the scheme's `FOR SHARE`; a scheme's change or archive
  takes the scheme's row `FOR UPDATE` alone and never a person. So an archive and an
  employment write naming the scheme are judged one after the other: whichever comes
  second waits on the scheme's row and finds the scheme named
  (`holiday_scheme_in_use`) or archived (`structure_archived`). The two take the
  rows in one order, so they cannot deadlock.
- **An absence's rows.** A write of an absence or an adjustment takes the person's row,
  then the absence type it names `FOR KEY SHARE` — a type's own change holds it
  `FOR UPDATE` and never a person, so the two queue — then the absence's or the
  adjustment's row. Overlap, the carry-over's "at most one" and the account an answer
  carries are judged over rows read inside that lock, so two writes for one person are
  judged one after the other.
- **Sick-leave cases.** A sick row's write takes, after the person's row and their
  absences, the person's cases `FOR NO KEY UPDATE` and their milestones, then their
  documents (a merged or removed case's). A case's own write — its notes, a milestone,
  the close — takes the person's row, then the case; one on a case a merge removed
  meanwhile finds it gone (404). A demand to postpone holiday takes the person's row,
  the sykmelding, then each ferie row's type `FOR KEY SHARE`. A change, removal,
  cancel or correction of an absence takes its type `FOR KEY SHARE` too, after the row
  (Task 4 re-check), so a change of the type's own waits. Every case write and every sick
  row of one person are judged one after the other; the case's dates are derived
  inside the lock from the rows read in it, and the egenmelding counters likewise.
- **The sequence.** A create that generates an employee number holds the settings row
  until it commits, so two creators take numbers one after the other.
- **The delete.** `DELETE /people/{id}` takes the person's row, then the manager graph,
  before it judges whether anyone names the person as a manager.
- **The retention lease.** The erase cycle holds the session advisory lock
  `0x5354414646524554` on one pooled connection and runs every person's transaction on
  that connection (`withPersonLockOn`, the person's lock as above, marked as locked as
  any other); the ledger takes no lease and holds no connection while it asks the
  store ([the worker](#the-worker)).

## Endpoints

All under `/api/v1/staff`.

| Operation | Access |
| --- | --- |
| `GET /meta` | `staff:access` |
| `GET /settings`, `PUT /settings` | `staff:manage` |
| `GET /legal-entities`, `/business-units`, `/departments`, `/locations` | `staff:view` or `staff:manage` (judged in the module; otherwise 403) |
| `POST` the same, `PUT /{id}`, `POST /{id}/archive` | `staff:manage` |
| `GET /people` | `staff:view` or `staff:manage` (judged in the module; otherwise 403) |
| `GET /people/{id}`, `/next-of-kin`, `/roles`, `/events` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `POST /people`, `PUT /people/{id}`, `DELETE /people/{id}`, `PUT /people/{id}/next-of-kin`, `PUT /people/{id}/roles` | `staff:manage` |
| `GET /people/{id}/user` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `PUT /people/{id}/user`, `GET /assignable-users` | `staff:manage` |
| `PUT /people/{id}/national-id`, `POST /people/{id}/national-id/reveal`, `PUT /people/{id}/bank-account`, `POST /people/{id}/bank-account/reveal` | `staff:personal-data` |
| `GET /me`, `PUT /me`, `GET /me/events`, `GET /me/documents`, `GET /me/export` | `staff:access`, then a self-service link (otherwise `no_person`) |
| `GET /people/{id}/documents`, `GET /documents/{id}` | `staff:access`, then the person, `staff:manage` (not health documents) or `staff:personal-data` (otherwise 404) |
| `POST /people/{id}/documents`, `PUT /documents/{id}`, `DELETE /documents/{id}` | `staff:access`, then `staff:manage` or `staff:personal-data`, a health document `staff:personal-data` (judged in the module; otherwise 403) |
| `GET /document-categories` | `staff:view`, `staff:manage` or `staff:personal-data` (judged in the module; otherwise 403) |
| `POST /document-categories`, `PUT /document-categories/{id}` | `staff:manage` |
| `GET /absence-types`, `GET /holiday-schemes` | `staff:access` (hidden types to `staff:manage` alone) |
| `POST /absence-types`, `PUT /absence-types/{id}`, `POST /holiday-schemes`, `PUT /holiday-schemes/{id}` | `staff:manage` |
| `GET /absences`, `GET /absences/{id}` | `staff:access`, then the rows the caller may see (otherwise left out, or 404) |
| `POST /absences` | `staff:access`, then the caller's own person or `staff:manage` (otherwise 403) |
| `PUT /absences/{id}`, `DELETE /absences/{id}` | `staff:access`, then the person or `staff:manage` — for an HR-only row with `staff:personal-data` (otherwise 404, whatever the status) |
| `POST /absences/{id}/cancel` | `staff:access`, then the person or `staff:manage` for a request, a [decider](#decisions) for an approved row (otherwise 404, whatever the status; `own_request` to the person on a decided row they may not cancel as decider) |
| `POST /absences/{id}/approve`, `/reject`, `GET /approvals` | `staff:access`, then a [decider](#decisions) of the person — of an HR-only type `staff:personal-data` (otherwise 404; `own_request` to the person) |
| `POST /absences/{id}/correct` | `staff:manage` (never one's own: `own_request`) |
| `GET /calendar` | `staff:access`, then the people and rows the caller may see |
| `GET /people/{id}/holiday-account` | `staff:access`, then the person, a manager of theirs, the Approver or HR (otherwise 404) |
| `GET /people/{id}/holiday-adjustments`, `POST /people/{id}/holiday-adjustments`, `DELETE /holiday-adjustments/{id}`, `POST /people/{id}/carry-over` | `staff:manage` |
| `GET /me/absences`, `POST /me/absences`, `GET /me/holiday-account`, `GET /me/self-certification`, `GET /me/care-days` | `staff:access`, then a self-service link (otherwise `no_person`) |
| `GET /cases`, `GET /cases/{id}` | `staff:access`, then the person, a manager of theirs, the Approver or HR (otherwise left out, or 404) |
| `PUT /cases/{id}` | `staff:access`, then a manager of the person or HR — the estimate `staff:manage` (otherwise 404) |
| `POST /cases/{id}/milestones/{kind}/done`, `/waive` | `staff:access`, then a manager of the person, the Approver or HR (otherwise 404) |
| `PUT /cases/{id}/milestones/{kind}/note` | `staff:personal-data` |
| `POST /cases/{id}/close`, `PUT /people/{id}/self-certification/withdrawal`, `POST /absences/{id}/postpone-holiday` | `staff:manage` |
| `GET /people/{id}/self-certification` | `staff:access`, then the person, a manager of theirs, the Approver or HR — the withdrawal to the care-day fields' readers (otherwise 404) |
| `GET /people/{id}/care-days` | `staff:access`, then the person, `staff:manage` or `staff:personal-data` (otherwise 404) |
| `GET /people/{id}/personal-data`, `PUT /people/{id}/legal-hold`, `POST /people/{id}/reassessed` | `staff:personal-data` |
| `POST /people/similar-erased` | `staff:manage` |
| `POST /people/import`, `GET /people/export.csv`, `GET /people/import-template.csv` | `staff:manage`; an import file's sealed columns `staff:personal-data` too (`import_sealed_columns`) |
| `GET /payroll/export` | `staff:manage` |
| `POST /retention/run` | `staff:manage` |
| `GET /stats` | `staff:view` or `staff:manage` (judged in the module; otherwise 403) |
| `GET /stats/attention` | `staff:access`, shaped per caller: `staff:manage`, `staff:personal-data` and a manager for their reports (otherwise `[]`) |
| `POST /thresholds/{key}/acknowledge` | `staff:manage` |
| `GET /people/{id}/employments`, `GET /employments/{id}`, `GET /employments/{id}/terms`, `GET /people/{id}/org` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `GET /employments/{id}/contract-check` | `staff:access`, then the person, a manager of theirs or HR (otherwise 404) |
| `POST /people/{id}/employments`, `PUT /employments/{id}`, `POST /employments/{id}/end`, `POST /employments/{id}/reopen`, `POST /employments/{id}/terms`, `PUT /terms/{id}`, `DELETE /terms/{id}` | `staff:manage` |

Every operation also requires `staff:access`. The request and response shapes are in
the contract, [`openapi/staff.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/staff.yaml),
and in the API reference generated from it.
