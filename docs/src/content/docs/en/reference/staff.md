---
title: "Staff module"
description: "The workforce register: the settings, the company's structure, the business time zone, the permissions and the lock rules."
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
which identity always provides, and sends its one mail through the platform's SMTP
seam. `MODULES=staff` alone is a valid installation. It provides no contract and fills
no slot; nothing in another module reads the register yet.

This page grows with the module. What is here today: the [permissions](#permissions),
the [settings](#settings), the [business time zone](#the-business-time-zone), the
[structure](#the-structure), [how a refusal looks](#refusals-and-revisions), the
[locks](#locks) and the [endpoints](#endpoints). People, the sealed identifiers, the
self-service link, employments and terms arrive with the rest of the delivery.

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
holds exactly one row.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `staff:access` | Use Staff | The app at all: your own record, absence, documents and export. **Every operation requires it.** | no |
| `staff:view` | View the staff directory | Everyone's work details, employments and terms without pay, the structure, the calendar with types hidden. | no |
| `staff:approve` | Decide absence | Decide anyone's absence, see types other than HR-only and open cases' dates, mark milestones. Registered now; grants nothing until absence arrives. | no |
| `staff:manage` | Manage staff | HR: people (not the sealed fields), employments and terms with pay, the structure, the settings, documents other than health, the account flag, imports and exports. | yes |
| `staff:personal-data` | Reveal and erase personal data | The national identity number and the bank account, health documents and notes, HR-only absence types, anyone's personal-data export, legal holds, erasure. | yes |

All five are in the category "Staff" and delegable. The Owner holds them through the
wildcard; anyone else needs a role that grants them. Each operation's rule is in the
contract's `x-vantigo-access`; a caller without it gets the access layer's uniform
403.

`GET /meta` answers what the caller may do as capabilities — `canViewDirectory`
(`staff:view`), `canEdit` (`staff:manage`), `canRevealIdentifiers`
(`staff:personal-data`) and `canSeePay` (either of the last two) — so the app never
re-derives a rule. The capabilities absence and the records add (`canDecide`,
`canSeeHealth`) arrive with them.

## Settings

One row, written by the migration. `GET /settings` and `PUT /settings` are
`staff:manage`'s. This delivery's contract exposes five fields; the others on the row —
the self-certification policy, the account and retention days, the thresholds — reach
the contract with the deliveries that use them.

| Field | Default | Rule |
| --- | --- | --- |
| `timeZone` | `Europe/Oslo` | An IANA zone this server and its database read the same way (below). |
| `employeeNumberPrefix` | empty | Up to 8 letters, digits or hyphens. |
| `nextEmployeeNumber` | 1 | 1 or more. With the prefix, at most 16 characters. |
| `pensionProvider` | — | Up to 200 characters; left out, cleared. |
| `injuryInsurer` | — | Up to 200 characters; left out, cleared. |

A generated employee number is the prefix and the next number, unpadded: `EMP-` and 7
make `EMP-7`. An employee number is at most 16 characters (Entra's `employeeId`
bound), so a prefix and a next number that together make a longer one are refused with
`employee_number_too_long`, named on both fields. A company that wants `EMP007` sets
the prefix to `EMP00` while it lasts.

`PUT /settings` is a full replace guarded by `If-Match` (below). Every bad field is a
400 naming it; the whole request is judged before anything is written.

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
needs `staff:view`; adding (`POST`), replacing (`PUT /{id}`) and archiving
(`POST /{id}/archive`) need `staff:manage`. `GET /meta` carries all four lists for a
caller holding `staff:view`, read in one snapshot.

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
(`structure_archived`). Each table's default is serialised by a transaction advisory
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

## Refusals and revisions

Every refusal is an RFC 9457 problem, `StaffProblem`: `title`, `status`, `detail`,
and — when a rule of the design refused — `code`, which is what a client keys off,
never the words. A 400 carries `errors`, the field errors keyed by the request's
camelCase field; a plain validation failure has no code, a named rule (such as
`org_number_invalid`) carries one beside its field errors. A request the server
cannot decode — malformed JSON, a missing required header — is a bare 400.

| Code | Status | When |
| --- | --- | --- |
| `org_number_invalid` | 400 | An organisation number that is not nine digits with a valid check digit. |
| `employee_number_too_long` | 400 | The prefix and the next number make a number longer than 16. |
| `structure_archived` | 409 | A write newly names an archived row, or makes one the default. |
| `structure_default` | 409 | Archiving the default, or taking the flag off it in place. |
| `department_cycle` | 409 | A parent that is the department itself or below it. |
| `revision_conflict` | 409 | The `If-Match` revision has moved on. |

An unknown id is a bare 404 with no body.

Every mutable row carries a `revision`, starting at 1 and moving on with each change.
A `PUT` requires `If-Match` carrying the revision the row was read at — bare (`3`) or
quoted (`"3"`). A revision that has moved on is `revision_conflict`, whose detail names
both revisions; an `If-Match` that holds no revision is a 400 on `If-Match`. The
revision is judged under the row's lock, so two writes at once queue and the second is
told the first won. Archiving needs no revision: it is idempotent.

## Locks

- **No call out of a transaction.** The user directory and the mail are reached before
  a transaction opens or after it commits, never inside one — neither a locked one nor
  a read-only snapshot, which holds a pool connection just the same. Every such call
  goes through one accessor in `contractscalls.go`, which refuses a call made inside
  one of the module's transactions; the tests record every call and fail on any made
  inside one.
- **A person's row first.** Every mutation of a person's rows takes the person's row
  first (`withPersonLock`), then their employments; nothing locks two people. The lock
  is `FOR NO KEY UPDATE`: it queues every writer of the person as `FOR UPDATE` would,
  but does not block the `KEY SHARE` lock a foreign-key check takes when another row
  names the person — another person's terms naming them as manager, a department
  naming them as its head — so such a write never waits on this person while holding
  locks of its own.
- **The manager graph.** A write that changes an edge of the manager graph — a
  department's manager or parent today, a terms row's manager or department with the
  employments — takes the transaction advisory lock `staff-manager-graph`, after any
  person's row, so the cycle checks across people are judged one at a time.
- **The defaults.** A write of a legal entity or a business unit takes its table's
  default lock first.

## Endpoints

All under `/api/v1/staff`.

| Operation | Access |
| --- | --- |
| `GET /meta` | `staff:access` |
| `GET /settings`, `PUT /settings` | `staff:manage` |
| `GET /legal-entities`, `/business-units`, `/departments`, `/locations` | `staff:view` |
| `POST` the same, `PUT /{id}`, `POST /{id}/archive` | `staff:manage` |

Every operation also requires `staff:access`. The request and response shapes are in
the contract, [`openapi/staff.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/staff.yaml),
and in the API reference generated from it.
