---
title: "Staff module"
description: "The workforce register: people, their field groups and who reads them, their history, employments with dated terms and the dates the a-melding derives from them, the § 14-6 check, managers and the org view, the sealed national identity number and bank account, the self-service link and My page, the document archive with its categories and retention classes, the personal-data exports, the register's import and export as CSV, the payroll export, the retention run and its deletion ledger, the erased state, the legal hold and the reassessment, the settings, the company's structure, the business time zone, the permissions and the lock rules."
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
[documents](#documents) and their [categories](#categories-and-classes), the
[personal-data exports](#the-personal-data-exports), the register's
[import and export](#the-registers-import-and-export), the
[payroll export](#the-payroll-export), [retention](#retention) and its
[deletion ledger](#the-deletion-ledger), the [erased state](#the-erased-state),
the [legal hold](#the-legal-hold) and the [reassessment](#the-reassessment), [how a refusal
looks](#refusals-and-revisions), the [locks](#locks) and the [endpoints](#endpoints).

## The schema

One migration, `00042_staff_foundation.sql`, creates the whole of this delivery's
schema at once: `people` (with every column the phase needs — the sealed identifiers,
the care-day inputs, the account flag, the legal hold and the erase stamps — so later
deliveries never add a column to it), `next_of_kin`, `person_roles`, `employments`,
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

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `staff:access` | Use Staff | The app at all: your own record, absence, documents and export. **Every operation requires it.** Your absence arrives with delivery B. | no |
| `staff:view` | View the staff directory | Everyone's work details, employments and terms without pay, the structure, the calendar with types hidden. The calendar arrives with B. | no |
| `staff:approve` | Decide absence | Decide anyone's absence, see types other than HR-only and open cases' dates, mark milestones. Registered now; grants nothing until absence arrives with B. | no |
| `staff:manage` | Manage staff | HR: people (not the sealed fields), employments and terms with pay, the structure, the settings, documents other than health and their categories, the account flag, imports and exports, the retention run by hand. The imports and exports arrive later in A2, the account flag with C. | yes |
| `staff:personal-data` | Reveal and erase personal data | Reads every person's every group; reveals and sets the national identity number and the bank account; health documents and notes, HR-only absence types, anyone's personal-data export, legal holds, erasure. It changes nothing else of a person but documents — the rest is `staff:manage`'s. Erasure is the [retention run](#retention)'s, on its schedule (run by hand by `staff:manage`); the milestones' notes and the HR-only types arrive with B. | yes |

All five are in the category "Staff" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform
403.

`GET /meta` answers what the caller may do as capabilities — `canViewDirectory`
(`staff:view`), `canEdit` (`staff:manage`), `canRevealIdentifiers`
(`staff:personal-data`), `canSeePay` (either of the last two) and `canSeeHealth`
(`staff:personal-data`: everyone's health documents) — so the app never re-derives a
rule. The capability absence adds (`canDecide`) arrives with it.

## Settings

One row, written by the migration. `GET /settings` and `PUT /settings` are
`staff:manage`'s. The contract exposes seven fields and a derived block; the others on
the row — the self-certification policy, the account days, the thresholds — reach the
contract with the deliveries that use them.

| Field | Default | Rule |
| --- | --- | --- |
| `timeZone` | `Europe/Oslo` | An IANA zone this server and its database read the same way (below). |
| `employeeNumberPrefix` | empty | Up to 8 letters, digits or hyphens. |
| `nextEmployeeNumber` | 1 | 1 or more. With the prefix, at most 16 characters. |
| `pensionProvider` | — | Up to 200 characters; left out, cleared. |
| `injuryInsurer` | — | Up to 200 characters; left out, cleared. |
| `retentionGraceDays` | 90 | 0 to 3650 days from the last employment's end to the [D erase](#what-is-due); left out, kept. |
| `healthRetentionDays` | 365 | 30 to 3650 days from the last employment's end to the [health erase](#what-is-due); left out, kept. |

`GET /settings` and the answer of `PUT /settings` also carry the read-only
[`retention` block](#the-settings-and-the-retention-block), derived from the deletion
ledger and the history: `pendingDeletions`, `failingDeletions`, `oldestPendingSince`,
`lastErasedAt`.

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
retention periods, which a body that leaves them out keeps. Every bad field is a 400
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
  force: the manager the terms name, else [the department's](#managers-and-the-org-view);
  absent when there is none. A caller holding `staff:view` alone, who sees only the
  people active today, is not shown a manager who is not active today either.

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

| Group | Fields | Self | Colleague (`staff:view`) | Manager | `staff:manage` | `staff:personal-data` |
| --- | --- | --- | --- | --- | --- | --- |
| Work | name, employee number, work email and phone, work country, language, roles; the person's place today — `manager`, `department`, `title`, `businessUnit`, `location` | ✓ | ✓ | ✓ | ✎ | ✓ |
| Employment dates | `status`; an employment's `startDate`, `endDate`, `fteChangedAt`; a terms row's `validFrom`, `validTo`, `employmentType`, `ftePercent` | ✓ | ✓ | ✓ | ✎ | ✓ |
| Employment | every other field of an employment and its terms but the pay | ✓ | — | ✓ | ✎ | ✓ |
| Pay | `remunerationType`, `payComponents`, `payMethod`, `paydayText`, `seniorityDate`, `payGrade`, `salaryChangedAt` | ✓ | — | — | ✎ | ✓ |
| Private | birth date, gender, mobile, private phone and email, home address, next of kin | ✓ | — | — | ✎ | ✓ |
| HR notes | `notes`; a terms row's `note` | — | — | — | ✎ | ✓ |
| National identity number | `nationalId { kind, onFile }` | ✓ | — | — | ✓ | ✎ reveal and set |
| Bank account | `bankAccount { kind, last4 }` | ✓ | — | — | ✓ | ✎ reveal and set |
| Link | `user { id, displayName, active }` | ✓ | — | — | ✓ | ✓ |
| Hold | `legalHold { held, reason, at }`, `erasedAt`, `reassessedAt` | — | — | — | ✓ | ✎ hold and reassess |

- **Self** is the user a person's self-service link names, whatever state the link is
  in. **Colleague** is `staff:view`, and only for a person **active today**: an
  unannounced hire, a leaver and someone with no employment stay out of the colleague
  directory, and since ids are sequential they answer the unknown id's 404.
  **Manager** is a caller whose self-service link names a manager of the person
  today: the manager the terms in force today name on one of the person's employments,
  or the department's when they name none (see [managers](#managers-and-the-org-view));
  concurrent employments may give two. It needs no permission. A caller holding several
  is every one of them at once and reads what any may. `staff:approve` is no audience
  yet (it decides absence, which arrives later).
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
| `erased` | No fields: `kinds`, `[{kind, count}]` — what one [erase](#what-an-erase-does) removed per kind (`D`, `health`, `A`); the actor is the installation's `system` for the worker, the caller for a run by hand; no payload class. |

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
to everyone, HR included; an `erased` event's `kinds` are read by the documents
group and are an empty payload to anyone else.

A **document's event** is shaped by one group for the whole event, not per field: the
**documents** group (the person, `staff:manage`, `staff:personal-data`), or — when its
classes hold E, a health document's — the **health** group (the person,
`staff:personal-data`). A manager reads that a document was added, changed or removed,
with its `documentId` and each field `"changed"`. A **health event** — any event whose
payload touches class E — is left out of the list, and out of its `totalCount`, for an
audience without the health group, HR without `staff:personal-data` included: they do
not learn that one happened.

The **hold's kinds** — `legal_hold_set`, `legal_hold_lifted`, `reassessed`,
`person_rehired` and `personal_data_exported` — are left out of the list, and out of its
`totalCount`, for an audience that is neither the person nor HR: a manager does not
learn that a hold was set or the person's data exported. The person themselves is
shown them — they have a right to know a hold is on them and that their data was
exported — as the kind, each field `"changed"`; `staff:manage` and
`staff:personal-data` read the values.

### Deleting a person

`DELETE /people/{id}` (`staff:manage`) is the hard delete of a person **created in
error**: one with no employment, no user link, no document, and named as nobody's
manager — neither a department's nor on anyone's terms — and not under a
[legal hold](#the-legal-hold). An [erased](#the-erased-state) person is
`person_erased` (409) and a held one `legal_hold` (409), judged before what names
them; anything else is `person_in_use` (409), whose
detail says what names the person — "has documents" among the reasons; the person
stays. The delete takes the person's row
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
  out; a named one must exist and not be archived (`structure_archived`, 409).
  `relationshipType` is `ordinary` (the default), `maritime`, `freelancer` or
  `pension_or_other`. The creation is an `employment_created` and a `terms_added`
  event.
- **The employment number** — the a-melding's `arbeidsforholdID` — is 1–40 letters,
  digits or hyphens, unique however it is cased, and never changes. Typed, it is kept
  as typed; left out, it is generated as the person's employee number, a hyphen and the
  first `n` from 1 that no employment has — so `E-7-1`, `E-7-2`, skipping past a number
  typed by hand — under the person's lock. A number another employment has is
  `employment_number_taken` (400), including when the unique index settles a race.
- **`PUT /employments/{id}`** (`staff:manage`, `If-Match`) replaces `startDate`,
  `relationshipType`, `businessUnitId` and `contractSignedAt` (left out, cleared). A new
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
the manager the terms in force name; when they name none, the manager of the terms'
department — unless that is the person themselves, or the department has none, when the
parent departments are climbed to the first whose manager is someone else; else nobody.
So the head of a department is managed by the head of the department above, never by
themselves, and concurrent employments may give two managers. The climb carries a
visited set, so a department graph that loops ends it.

Nobody manages themselves (`self_manager`, 409), and the graph never loops: a write
that names, clears or changes a manager or a department on a terms row — adding,
replacing or deleting one, a new start, a reopening — and a department write that
changes its manager or parent — and an end moved later — take the manager graph's lock after the person's row and
judge the graph they leave on the row's day and on **every later day the graph
changes** (each terms row's day, each start, each day after an end). A person who would
manage themselves through anyone is `manager_cycle` (409), naming the first such day:
a row of B's naming A from March is refused when A's row naming B takes effect in June.
Two such writes at once are judged one after the other, so exactly one of a pair that
would close a loop is written.

`GET /people/{id}/org` answers `{person, chain, reports}` as the work group names them
(`{id, displayName, employeeNumber}`): `chain` is the person's manager, that manager's
manager and so on up, following each one's earliest employment in force today and
stopping where nobody manages or where the walk would come back on itself; `reports`
are the people whose manager the person is today. Anyone who may see the person reads
it; to a caller holding `staff:view` alone a manager in the chain who is not active
today is left out, as they are from the directory.

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

## The personal-data exports

A person's data in one JSON file (design S16): `exportedAt`, the `person` — every field
of the record — their `nextOfKin`, `roles`, `employments` (each `{employment, terms}`,
the terms oldest first), the self-service `link`, the `documents`' metadata (the files
themselves are read through `GET /documents/{id}`) and every event of their history,
oldest first. Both exports read it all in **one** REPEATABLE READ snapshot, so its parts
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

HR's notes are in none of them. A column named `error` — what a failed-rows file adds
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
payroll already holds them. The leave and absence columns are delivery B's and will be
**appended to the right** of `Pay grade`, so a payroll system's mapping of these
columns keeps working.

**The order of work.** The parameters and the actor are judged first; everything the
file is built from — the people's numbers and names, the employments, their terms, the
business units and the departments — is read in **one REPEATABLE READ snapshot**; the
rows and the file are built in memory; then the export is recorded as the
installation's **`payroll_exported`** event, naming the exporter, with `month`,
`format`, `rows` and `sha256` — the hex SHA-256 of the very bytes answered, so the file
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
  care-day fields, HR's notes, the terms' notes, the next of kin, and documents of
  class D. Erased a grace period after the last employment ended.
- **E** — health: health documents (a restricted category) and the values of their
  events. Erased a longer period after the last employment ended, until delivery B
  gives health documents a sick case's schedule; the self-certification withdrawal is
  E on B's schedule, and no erase reaches it before B.
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
| `people` | E | `self_cert_withdrawn_until`, `self_cert_withdrawn_reason` |
| `people` | F | `legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`, `legal_hold_at` |
| `next_of_kin` | D | every column: `id`, `person_id`, `position`, `name`, `relationship`, `phone`, `note`, `created_at`, `updated_at` |
| `person_roles` | C | every column: `id`, `person_id`, `role`, `from_date`, `to_date`, `created_at`, `updated_at` |
| `employments` | C | every column: `id`, `person_id`, `employment_number`, `relationship_type`, `business_unit_id`, `start_date`, `end_date`, `end_reason`, `contract_signed_at`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | C | `id`, `employment_id`, `valid_from`, `employment_type`, `temporary_ground`, `expected_end_date`, `appointment_form`, `hired_out_to`, `fte_percent`, `working_hours_scheme`, `full_time_hours_per_week`, `occupation_code`, `title`, `employee_type`, `department_id`, `location_id`, `manager_person_id`, `cost_centre`, `division`, `working_time_text`, `break_minutes`, `special_hours_text`, `overtime_rule_text`, `probation_end_date`, `probation_extension_agreed`, `probation_extended_to`, `probation_extension_reason`, `notice_employee`, `notice_employer`, `notice_unit`, `tariff_reference`, `holiday_terms_text`, `competence_development_text`, `social_security_text`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | A | `remuneration_type`, `pay_components`, `pay_method`, `payday_text`, `seniority_date`, `pay_grade` |
| `employment_terms` | D | `note` |
| `identity_links` | C | every column: `id`, `person_id`, `provider`, `external_id`, `state`, `managed`, `blocked_reason`, `last_error`, `attempts`, `next_attempt_at`, `lease_id`, `lease_until`, `last_synced_at`, `last_spec_hash`, `created_at`, `updated_at` |
| `documents` | the category's | every column: `id`, `person_id`, `category_id`, `employment_id`, `object_key`, `file_name`, `content_type`, `size_bytes`, `valid_to`, `signed_at`, `uploaded_by_user_id`, `revision`, `created_at`, `updated_at` — a document is erased whole, by its [category's](#categories-and-classes) class |

Ids, foreign keys, revisions and timestamps are C: they say that a row existed and
when. An event's `payload_classes` are its fields' classes (`fieldClasses` in
`events.go`, held to the columns above); a document's event takes its category's.

### What is due

The run asks three questions, each over what remains, "today" being the settings'
time zone's civil day:

- **D** — a person not yet erased, not held, with **at least one employment and every
  one ended before today minus `retentionGraceDays`** (default 90). A person with no
  employment, or with one that has not ended, is never erased.
- **health** — a person, erased or not, not held, whose every employment ended before
  today minus `healthRetentionDays` (default 365), with a document in a restricted
  category still on file, or a value of class E in an event not yet replaced.
- **A** — per **employment**: ended in a year more than five years before this one
  (ended in 2020, due from 1 January 2026; ended in 2021, not before 2027), of a person
  not held, erased or not, with something of class A still there — a pay column set on
  one of its terms rows, a document of class A filed against it (or filed against no
  employment while it is the person's last one — the latest to start), or a pay field
  in one of its events not yet replaced.

### What an erase does

One person's erases run in **one transaction**, under the person's lock, each question
asked again on the row the lock holds — a hold set, an employment reopened or a rehire
since the cycle chose the person is seen there:

- **D** nulls every D column of the person and of their terms rows, deletes their next
  of kin, deletes their documents of class D, replaces every value of class D in their
  history's payloads, and sets `erased_at` — the person is then
  [read-only](#the-erased-state).
- **health** deletes their documents in restricted categories and replaces the values
  of their health documents' events.
- **A**, for each due employment, nulls its pay columns on every terms row, deletes its
  documents of class A, and replaces the values of class A in its events and every
  value of the deleted documents' events. A rehired person's old employment loses its
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
person, `{"kinds": [{"kind": "D" | "health" | "A", "count": n}]}`, its actor the
installation (`system`) when the worker ran it and the caller when
[`POST /retention/run`](#running-it-by-hand) did. `count` is the items the kind
removed: each column value nulled, each row deleted (a next of kin, a document) and
each event payload rewritten. A D erase is always reported, since it marks the
person, even when it found nothing to remove (count 0); an A count sums the person's
due employments. The event touches no class, so no later erase rewrites it; its kinds
are read by the documents group (the person, `staff:manage`, `staff:personal-data`)
and are an empty payload to anyone else.

The run is **idempotent**: a second run finds nothing due and writes nothing. That is
what makes "once per civil day" hold whatever the poll and the clock do.

### The deletion ledger

`object_deletions` holds the objects whose rows a delete or an erase removed and that
the store has not yet confirmed gone (`object_key`, `attempts`, `next_attempt_at`,
`claimed_until`, `last_error`, `created_at`). A pass takes the due rows —
`next_attempt_at` reached, not claimed or claimed by a pass whose window has run out —
a hundred at a time, the longest waiting first, and claims each by a conditional update
for five minutes; a row another replica won between the selection and the claim is
left to it. Each claimed row gets one attempt:

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
`STAFF_RETENTION_ENABLED=0`, in worker mode and in api mode with
`WORKERS_IN_PROCESS=1`. Each poll:

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
the kinds summed over the people erased, a kind no erase ran for absent. While another
holder has the lease — the worker, here or on another replica, or another run by hand —
it is **`retention_busy`** (409) and runs nothing.

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
employment ended (design S15; the attention item arrives later in A2).
`POST /people/{id}/reassessed` (`staff:personal-data`) records it: due when the person
is erased, not yet reassessed, every employment has ended, and today — the business
time zone's day — is on or after the last end plus twelve months (the 29th of February's
due day is the 28th). It records when and by whom (`reassessed_at`,
`reassessed_by_user_id`) as a `reassessed` event naming `reassessedAt`, and answers the
person. Anything else — too early, already reassessed, not erased, an employment that has
not ended — is `reassessment_not_due` (409). A rehire clears the reassessment with the
erasure.

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
| `structure_archived` | 409 | A write newly names an archived row, or makes one the default. |
| `structure_default` | 409 | Archiving the default, or the legal entity the default business unit sits under, or taking the flag off the default in place. |
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
| `seed_immutable` | 409 | Changing a seeded document category. |
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
  default lock first.
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
| `GET /people/{id}/personal-data`, `PUT /people/{id}/legal-hold`, `POST /people/{id}/reassessed` | `staff:personal-data` |
| `POST /people/similar-erased` | `staff:manage` |
| `POST /people/import`, `GET /people/export.csv`, `GET /people/import-template.csv` | `staff:manage`; an import file's sealed columns `staff:personal-data` too (`import_sealed_columns`) |
| `GET /payroll/export` | `staff:manage` |
| `POST /retention/run` | `staff:manage` |
| `GET /people/{id}/documents`, `GET /documents/{id}` | `staff:access`, then the person, `staff:manage` (not health documents) or `staff:personal-data` (otherwise 404) |
| `POST /people/{id}/documents`, `PUT /documents/{id}`, `DELETE /documents/{id}` | `staff:access`, then `staff:manage` or `staff:personal-data`, a health document `staff:personal-data` (judged in the module; otherwise 403) |
| `GET /document-categories` | `staff:view`, `staff:manage` or `staff:personal-data` (judged in the module; otherwise 403) |
| `POST /document-categories`, `PUT /document-categories/{id}` | `staff:manage` |
| `GET /people/{id}/employments`, `GET /employments/{id}`, `GET /employments/{id}/terms`, `GET /people/{id}/org` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `GET /employments/{id}/contract-check` | `staff:access`, then the person, a manager of theirs or HR (otherwise 404) |
| `POST /people/{id}/employments`, `PUT /employments/{id}`, `POST /employments/{id}/end`, `POST /employments/{id}/reopen`, `POST /employments/{id}/terms`, `PUT /terms/{id}`, `DELETE /terms/{id}` | `staff:manage` |

Every operation also requires `staff:access`. The request and response shapes are in
the contract, [`openapi/staff.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/staff.yaml),
and in the API reference generated from it.
