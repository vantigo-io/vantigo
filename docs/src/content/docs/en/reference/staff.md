---
title: "Staff module"
description: "The workforce register: people, their field groups and who reads them, their history, the sealed national identity number and bank account, the self-service link and My page, the settings, the company's structure, the business time zone, the permissions and the lock rules."
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
[structure](#the-structure), [people](#people) — the record, its
[field groups and audiences](#field-groups-and-audiences), the
[history](#the-history) and [deletion](#deleting-a-person) — the
[sealed identifiers](#the-sealed-identifiers), the
[self-service link](#the-self-service-link), [My page](#my-page), [how a refusal
looks](#refusals-and-revisions), the [locks](#locks) and the [endpoints](#endpoints).
Employments and terms arrive with the rest of the delivery.

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

**The directory**, `GET /people`, lists people by display name with the work group
only, paged (`page`, `pageSize` up to 100). `q` matches each of its words, however
cased, against the names, the employee number and the work email — never a private
field. `staff:manage` lists everyone; `staff:view` without it lists the people
**active today** — with an employment that has started and not ended, judged on the
business zone's day (the end day itself still counts). Anyone else is the uniform 403.
With employments' own endpoints, `staff:manage` gains a status filter.

### Field groups and audiences

Every field of a person belongs to one **field group**, held in one static table in
`shaping.go`; every response is built by asking that table, field by field, whether the
caller's audience reads the field's group. A group the audience may not read is absent
from the response — never null.

| Group | Fields | Self | Colleague (`staff:view`) | `staff:manage` | `staff:personal-data` |
| --- | --- | --- | --- | --- | --- |
| Work | name, employee number, work email and phone, work country, language, roles | ✓ | ✓ | ✎ | ✎ |
| Private | birth date, gender, mobile, private phone and email, home address, next of kin | ✓ | — | ✎ | ✎ |
| HR notes | `notes` | — | — | ✎ | ✎ |
| National identity number | `nationalId { kind, onFile }` | ✓ | — | ✓ | ✓ |
| Bank account | `bankAccount { kind, last4 }` | ✓ | — | ✓ | ✓ |
| Link | `user { id, displayName, active }` | ✓ | — | ✓ | ✓ |

- **Self** is the user a person's self-service link names, whatever state the link is
  in. **Colleague** is `staff:view`, and only for a person **active today**: an
  unannounced hire, a leaver and someone with no employment stay out of the colleague
  directory, and since ids are sequential they answer the unknown id's 404. A caller holding several is every one of them at
  once and reads what any may. `staff:approve` is no audience yet; the **manager**
  audience — the work group and the non-pay terms of their reports — arrives with
  employments.
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
`canRevealIdentifiers` (`staff:personal-data`) and `canSeePay` (the person themselves,
`staff:manage` or `staff:personal-data`).

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

`GET /people/{id}/events` lists a person's history, newest first, paged. It is read by
the person themselves, `staff:manage` and `staff:personal-data` (and, with employments,
their manager); to anyone else — a colleague included — it is the unknown id's 404.
Each payload is **shaped per field** by the same table: a field of a group the caller
may not read shows the string `"changed"` in place of its values. So a change to HR's
notes reads `{"notes": "changed"}` to the person and `{"notes": {"from": …, "to": …}}`
to HR. A sealed value is never in a payload. Each event also stores the retention
classes its payload touches (`payload_classes`), which the erase uses and nothing
else; who reads what is never decided by them.

### Deleting a person

`DELETE /people/{id}` (`staff:manage`) is the hard delete of a person **created in
error**: one with no employment, no user link, and named as nobody's manager — neither
a department's nor on anyone's terms. Anything else is `person_in_use` (409), whose
detail says what names the person; the person stays. The delete takes the person's row
and then the manager-graph lock before it judges, so no write can start naming the
person between the check and the delete, and a foreign key that refuses anyway is still
`person_in_use`. In one transaction it removes the next of kin, the roles and the
person's own history — the one exception to the history's append-only rule — and
records an installation-level `person_deleted` event carrying the employee number and
nothing else. It answers 204.

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
  HR's notes show `"changed"`.

## Refusals and revisions

Every refusal is an RFC 9457 problem, `StaffProblem`: `title`, `status`, `detail`,
and — when a rule of the design refused — `code`, which is what a client keys off,
never the words. A 400 carries `errors`, the field errors keyed by the request's
camelCase field; a plain validation failure has no code, a named rule (such as
`org_number_invalid`) carries one beside its field errors. A request the server
cannot decode — malformed JSON, a missing required header — is a bare 400. A list
element's field is named by its place: `nextOfKin[0].name`.

| Code | Status | When |
| --- | --- | --- |
| `org_number_invalid` | 400 | An organisation number that is not nine digits with a valid check digit. |
| `employee_number_too_long` | 400 | The prefix and the next number make a number longer than 16; a typed employee number longer than 16; a generation the skip carries past 16. |
| `employee_number_taken` | 400 | Another person has the employee number, however it is cased. |
| `work_email_taken` | 400 | Another person has the work email. |
| `person_in_use` | 409 | Deleting a person with an employment or a user link, or named as a manager. |
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
- **The sequence.** A create that generates an employee number holds the settings row
  until it commits, so two creators take numbers one after the other.
- **The delete.** `DELETE /people/{id}` takes the person's row, then the manager graph,
  before it judges whether anyone names the person as a manager.

## Endpoints

All under `/api/v1/staff`.

| Operation | Access |
| --- | --- |
| `GET /meta` | `staff:access` |
| `GET /settings`, `PUT /settings` | `staff:manage` |
| `GET /legal-entities`, `/business-units`, `/departments`, `/locations` | `staff:view` |
| `POST` the same, `PUT /{id}`, `POST /{id}/archive` | `staff:manage` |
| `GET /people` | `staff:view` or `staff:manage` (judged in the module; otherwise 403) |
| `GET /people/{id}`, `/next-of-kin`, `/roles`, `/events` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `POST /people`, `PUT /people/{id}`, `DELETE /people/{id}`, `PUT /people/{id}/next-of-kin`, `PUT /people/{id}/roles` | `staff:manage` |
| `GET /people/{id}/user` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `PUT /people/{id}/user`, `GET /assignable-users` | `staff:manage` |
| `PUT /people/{id}/national-id`, `POST /people/{id}/national-id/reveal`, `PUT /people/{id}/bank-account`, `POST /people/{id}/bank-account/reveal` | `staff:personal-data` |
| `GET /me`, `PUT /me`, `GET /me/events` | `staff:access`, then a self-service link (otherwise `no_person`) |

Every operation also requires `staff:access`. The request and response shapes are in
the contract, [`openapi/staff.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/staff.yaml),
and in the API reference generated from it.
