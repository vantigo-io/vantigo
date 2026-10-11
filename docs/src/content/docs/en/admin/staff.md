---
title: Staff
description: Enabling the Staff module, giving people access to it, linking people to their users, the personal-data permission and health documents, the document categories, answering an innsyn request, a legal hold, rehiring an erased person, importing and exporting the register, exporting a month for payroll, retention — the erase and the deletion ledger — the bank-change mail, and the settings — the business time zone, the employee number sequence and the retention periods.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
  - apps/server/internal/staff/document_categories.go
  - apps/server/internal/staff/personal_data.go
  - apps/server/internal/staff/legal_hold.go
  - apps/server/internal/staff/erased.go
  - apps/server/internal/staff/people_import.go
  - apps/server/internal/staff/people_export.go
  - apps/server/internal/staff/csvfile.go
  - apps/server/internal/staff/payroll.go
  - apps/server/internal/staff/retention.go
  - apps/server/internal/staff/retention_worker.go
  - apps/server/internal/staff/object_deletions.go
---

Staff is the company's register of the people who work for it: their records, their
employments and the company's structure. This page is the operator's side: turning
the module on, letting people in, and the settings. What the module does and the rules
it checks are in [the reference](/en/reference/staff/).

## Before you start

- Staff needs no other business module. `MODULES` unset enables it with every other
  module; an installation that lists `MODULES` adds `staff` to the list, alone if it
  likes (`MODULES=staff`).
- The migration creates the `staff` schema whether or not the module is enabled, so
  turning it on later needs no migration. If the database runs a least-privilege
  application role, it needs the `staff` schema granted like every other
  ([Installation](/en/admin/installation/)).
- People's documents are kept in the object store, so filing one needs
  `STORAGE_PROVIDER=fs` and a storage root
  ([Object storage](/en/admin/object-storage/)). Without it everything else works, and
  an upload or a download answers 503 `storage_unavailable`.

## Enable the module

1. Add `staff` to `MODULES` in `vantigo.env`, or leave `MODULES` unset.
2. Restart Vantigo (`docker compose up -d`).
3. Verify: `GET /api/v1/staff/meta` answers 401 without a session rather than the 404
   a disabled module's path answers.

A disabled module contributes no route and no permission; its data stays in the
database untouched.

## Give people access

Every Staff operation requires `staff:access`, and four permissions sit above it:

| Permission | For |
| --- | --- |
| `staff:access` | Everyone who should see their own record. |
| `staff:view` | Colleagues who should see the directory and the company's structure. |
| `staff:approve` | Whoever decides absence (it grants nothing until absence arrives). |
| `staff:manage` | HR: people, employments, documents other than health documents, the document categories, the structure and the settings. Sensitive. |
| `staff:personal-data` | Whoever may reveal national identity numbers and bank accounts, and read and file health documents. Sensitive. |

The Owner holds them all already. Everyone else needs a role that grants them: under
**Workspace admin → Roles & access**, create or edit a role and give it the
permissions in the "Staff" category
([Manage roles and access](/en/user/workspace-administration/#manage-roles-and-access)).
No built-in role holds any of them.

## Link people to their users

A person sees their own record — My page in the Staff app, `GET /api/v1/staff/me` —
only once HR has linked the person to the user they sign in as. The link makes that
user the person themselves to Staff and nothing else: it grants no permission, and it
never puts the user under Staff's management. Any active user can be linked, the Owner
included.

1. As a user with `staff:manage`, find the user: `GET /api/v1/staff/assignable-users?q=`
   with part of their name lists up to twenty active users.
2. Link them: `PUT /api/v1/staff/people/{id}/user` with `{"userId": "<the user's id>"}`.
   To unlink, send `{"userId": null}`.
3. Verify: the person signs in and `GET /api/v1/staff/me` answers their record rather
   than `no_person`. The person's history shows `user_linked`.

A user can be linked to one person only, and a person to one user; linking a person to
another user replaces the link. A disabled user cannot be linked.

## The personal-data permission

The national identity number and the bank account are sealed: stored encrypted, shown
to nobody — not to HR holding `staff:manage`, not to the person — except as the number's
kind and the account's last four digits. Only `staff:personal-data` writes them and
reveals them, and every reveal is recorded in the person's history with who looked.
Give `staff:personal-data` to as few people as payroll needs; it is a separate role from
HR's on purpose, so that changing where someone's pay goes takes a second pair of hands.

The values are sealed under keys derived from `APP_SECRET`. Rotating it makes them
unopenable and the national identity numbers' duplicate check blind — see
[Installation](/en/admin/installation/#the-settings-you-must-decide-on).

**Health documents** — a person's documents in a restricted category, `health` among
the seeds — are `staff:personal-data`'s too. HR holding only `staff:manage` does not see
them in a person's archive, cannot open, file, move or remove one, and does not see
that one was added in the person's history; the person themselves reads their own.
Give `staff:personal-data` to whoever handles sick notes and occupational-health
correspondence, and nobody else.

## Document categories

Every document is filed under a category, and the category decides when the document
is erased: its **retention class** — A for pay and contract documentation (five years
after the year the employment ended), D for personnel administration (with the rest of
the person's record), E for health. Six categories are there from the start: contract
and attest (A), certificate, correspondence and other (D), and health (E). A category
of class E is restricted: its documents are health documents.

To add one of your own, as a user with `staff:manage`:

1. `POST /api/v1/staff/document-categories` with a `key` (1–40 of `a–z`, `0–9` and
   hyphens), `names` in English and Norwegian and a `class` of `A`, `D` or `E`, for
   example `{"key": "courses", "names": {"en": "Courses", "nb": "Kurs"}, "class": "D"}`.
   Choose the class carefully: it never changes, because the erase reads it from every
   document filed under the category.
2. Verify: `GET /api/v1/staff/document-categories` lists it after the seeds.

To rename one, or stop offering it, send its `names` and `archived` to
`PUT /api/v1/staff/document-categories/{id}` with its revision in `If-Match`. An
archived category is offered for no new document, and only a category no document is
filed under can be archived. The six seeds never change.

## Answer an innsyn request

A person has the right to a copy of the personal data held about them (GDPR art. 15,
*innsyn*). Staff's complete answer is the HR-issued personal-data file:

1. As a user with `staff:personal-data`, `GET /api/v1/staff/people/{id}/personal-data`.
   The answer is a download, `staff-<employee number>-personal-data.json`: the person's
   record with every field group, the national identity number and the bank account in
   full, HR's notes, the next of kin, the roles, every employment with its terms (pay and
   notes included), the user link, the documents' list and the whole history.
2. Download each document the file lists through `GET /api/v1/staff/documents/{id}`; the
   file carries their metadata, not their bytes.
3. Verify: the person's history now starts with **Personal data exported**, naming you.
   Every such export is recorded, because the file opens the sealed values.

Hand the file over the way your company hands over personal data — not by ordinary
email. The person can also download their own copy from My page
(`GET /api/v1/staff/me/export`); it leaves out the sealed values, HR's notes and the
terms' notes, so it is not the complete answer, and it records nothing.

## Put a person under a legal hold

A legal hold keeps a person's record whole — no retention erase touches it, and the
person cannot be deleted — for as long as a dispute, a claim or an investigation needs
it.

1. As a user with `staff:personal-data`, `PUT /api/v1/staff/people/{id}/legal-hold` with
   `{"held": true, "reason": "…"}`. The reason is required, up to 500 characters; write
   what the hold is for and, if you can, until when.
2. Verify: the person's `legalHold` is `{"held": true, …}` with the reason and the time,
   and their history has **Legal hold set**.
3. When the matter is closed, lift it with `{"held": false}` (no reason). The reason and
   who set it are cleared; the history keeps both.

A held person who has been erased can still be held and released; nothing else of theirs
changes.

## Rehire an erased person

When the retention run has erased a former employee, their record is read-only: every
change answers `person_erased`, except a legal hold, a reassessment, unlinking their
user, and a rehire.

1. Before adding someone who may have worked here before, ask
   `POST /api/v1/staff/people/similar-erased` (`staff:manage`) with
   `{"givenName", "familyName", "birthDate"}`. Erased people with the same names and
   birth date are listed with their employee number and when they were erased. The
   national identity number cannot tell — the erase removed it.
2. If it is the same person, add the new employment to the erased record:
   `POST /api/v1/staff/people/{id}/employments`. The record opens again under the same
   employee number; its history gets **Rehired** before the new employment.
3. Type again what the erase removed — contact details, address, next of kin, the
   national identity number and the bank account (the last two with
   `staff:personal-data`).
4. If it is someone else, add them as a new person; the erased record stays as it is.

To link the person's user to a new record instead, unlink it from the erased one first
(`PUT /api/v1/staff/people/{id}/user` with `{"userId": null}`) — the one change of the
link an erased record allows.

## Import and export the register

The register travels as one semicolon-separated CSV file — the form a Norwegian Excel
opens and saves as CSV without an import dialog. All three steps need `staff:manage`;
the columns, the rules and the codes are in
[the reference](/en/reference/staff/#the-registers-import-and-export).

### Import a register

1. Download the template: `GET /api/v1/staff/people/import-template.csv`. It is the
   header row alone — the person's columns, the first employment's, and the sealed ones
   only if you hold `staff:personal-data`. Or start from an export.
2. Fill in one row per person. `employeeNumber` is required on every row: a number no
   person has adds the person, a number a person has changes them. For a new person,
   fill in `startDate` to add their first employment too — `businessUnit` by its name or
   org number (blank is the default unit), `department` by its code, `location` by its
   name, `managerEmployeeNumber` by the manager's employee number. A manager the file
   adds must come on an earlier row. A person already in the register only has their
   own columns changed — never an employment: change those in the app.
3. Save it as CSV UTF-8 with semicolons, and check it: `POST /api/v1/staff/people/import`
   with the file in the multipart part `file`. Without `dryRun=false` the import is a
   **dry run**: every row runs and is rolled back, and the answer says what each row
   would do — `created`, `updated`, `failed`, and each problem with its row and column.
4. Fix what failed and check again. When the dry run is clean, send the same file with
   `?dryRun=false`.
5. Verify: the answer's counts are what you expect, the people are in the list, and each
   person's history shows the import's changes under your name.

A row that fails writes nothing and does not stop the others; rows already imported stay
imported, so send a corrected file of the failed rows alone, or the whole file again —
rows that change nothing write nothing. An import cut short — the connection dropped
mid-file — keeps the rows it ran, and the installation's history still records it with
the counts so far: `rows` is the whole file, and the created, updated and failed rows
add up to fewer. The file is at most 5 MiB and
`STAFF_IMPORT_MAX_ROWS` rows (5000 unless you raised it,
[configuration reference](/en/admin/authentication/#configuration-reference)).

### The sealed columns

`nationalIdKind`, `nationalIdCountry`, `nationalId`, `bankAccountKind` and `bankAccount`
need `staff:personal-data` as well. A file carrying any of them from someone without it
is refused whole (`import_sealed_columns`) — remove the columns or ask for the
permission. A filled cell sets the value with the same checks as on the person's
record; a blank one leaves the value on file as it is. A bank account the import changes
mails the person, as a change by hand does ([The bank-change mail](#the-bank-change-mail)),
once their row is saved — never in a dry run. No answer, log line or history entry
carries a digit of either value.

The mail is sent row by row, as each row is saved, and the import waits for each send:
an onboarding import of a hundred people with bank accounts sends a hundred mails — a
first account mails too, as by hand — and takes correspondingly longer.

A row cannot change a person's `birthDate` and `nationalId` together: the import
writes the person's columns first, and checks the new birth date against the number on
file, which still encodes the old one — the row is refused
`national_id_birth_date_mismatch`. Make the change in two files, as you would by hand:
first the new `nationalId` with the `birthDate` cell blank, then the new `birthDate`.

### Export the register

`GET /api/v1/staff/people/export.csv` downloads `staff-people-<today>.csv`: every person
— erased ones too — with the work and private groups and where each stands today. The
national identity number and the bank account are never in it: `nationalIdOnFile` says
`yes` or `no`. Every export is recorded in the installation's history with your name and
the number of rows. The file holds personal data: keep it where you keep the register,
and delete it when you are done.

An export imports back unchanged: the columns after the private group are ignored, so
editing the person's columns in an export and importing it is a way to change many
people at once. An erased person's row comes back too and writes nothing; edit it, and
the row is refused `person_erased`.

## Export a month for payroll

The payroll file hands a month's employments to your payroll system. You need
`staff:manage`.

1. Ask for the month, as `YYYY-MM`:
   `GET /api/v1/staff/payroll/export?month=2026-09`. The Staff app's Settings page gains
   a month picker for it in a later release; until then, call the endpoint. `format`
   may be left out — `csv` is the one format there is.
2. Save the download, `staff-payroll-2026-09.csv`, and import it into payroll.

What the file holds: one row per employment that was in the month — started by its last
day and not ended before its first — erased people's included, with the terms in force
on the month's last day (or on the end day, for an employment that ended during the
month). Each row carries the employee and employment numbers, the name, the business
unit's org number, the start, the occupation code, the full-time hours, the position
percentage and when it last changed, the working hours scheme and the appointment form
as Skatteetaten's a-melding codes, the remuneration type, when the pay last changed,
the department's code, the cost centre (the terms' own, else the department's), and
the seniority date and pay grade when set. The end date and its reason are filled in
only in the month the employment ends. An employment with no terms on that day keeps
its row with the terms columns blank. The form is the Expenses payroll file's —
semicolons, a byte order mark and the decimal comma — so a Norwegian Excel opens it
as it is. The full column list and the codes are in
[the reference](/en/reference/staff/#the-payroll-export). The codes of the three
appointment forms added in 2025 (hired out, and on call) are not verified against
Skatteetaten's code list: check them with your payroll system before you rely on them.

The national identity number and the bank account are **not** in the file: payroll
already holds them, so the export never reads them.

Every export is recorded in the installation's history with your name, the month, the
format, the number of rows and the file's SHA-256 hash, so the file payroll received
can be recognised later — compute the hash of a file you hold
(`shasum -a 256 staff-payroll-2026-09.csv`) and compare. A month that is not
`YYYY-MM` is refused `payroll_month_invalid`, a format other than `csv`
`payroll_format_unsupported`; neither is recorded. The file holds everyone's pay terms:
keep it where you keep payroll's own records, and delete your copy when payroll has it.

## Retention

Staff keeps a former employee's record only as long as the law lets it, and erases the
rest on its own: the `staff-retention` worker looks every hour and erases what has
become due. What is due, and what each erase removes, is in
[the reference](/en/reference/staff/#retention); in short:

- **Personnel administration** — contact details and address, the national identity
  number and the bank account, the care-day fields, HR's notes and the terms' notes, the
  next of kin, and the documents in categories of class D (certificates,
  correspondence, other) — is erased **`retentionGraceDays`** (default 90) after the
  person's last employment ended. The person is then marked erased, and the record is
  read-only.
- **Health documents** — those in a restricted category — are deleted
  **`healthRetentionDays`** (default 365) after the last employment ended.
- **Pay** — an employment's pay terms, seniority date and pay grade, its contracts and
  attests, and the pay in its history — is erased **five years after the year the
  employment ended**, employment by employment: a person who has come back keeps the
  pay of their current employment while the old one's goes.

**What an erase leaves**: the names, the birth date, the gender, the employee number,
the employments and their terms other than pay, the roles, the link to a user, and the
history's kinds and dates. So the record still answers who worked here, when and in
what job; the history shows a value that was erased as *changed*, and an **Erased under
the retention rules** entry names what went, by kind and count. A person with no
employment, or one still employed, is never erased.

### Set the periods

1. As a user with `staff:manage`, read the settings: `GET /api/v1/staff/settings`.
2. `PUT /api/v1/staff/settings` with the revision you read in `If-Match` and
   `retentionGraceDays` (0 to 3650 days) and `healthRetentionDays` (30 to 3650). A body
   that leaves either out keeps it.
3. Verify: the answer carries the new periods. The next run judges by them; a period
   made shorter can make many people due at once, and the worker takes fifty a run.

### Turn the worker off, or change how often it looks

The worker runs unless `STAFF_RETENTION_ENABLED=0`, every `STAFF_WORKERS_POLL` (default
`1h`, at least `1m`) — in worker mode, and in api mode with `WORKERS_IN_PROCESS=1`
([Installation](/en/admin/installation/)). An erase finds nothing due a second time, so
an hourly look erases each day's due people once. Several replicas take turns through a
lock; one runs the erase at a time.

Turned off, nothing is erased on a schedule and the objects queued for deletion wait;
the run by hand below still works.

### Run it by hand

`POST /api/v1/staff/retention/run` (`staff:manage`) runs the worker's erase and a pass
over the deletion ledger now, with you as the erased entries' author. The answer says
what was erased per kind and how many objects were deleted from the store. While the
worker is running, it answers `retention_busy` (409); try again in a minute. The Staff
app's Settings page offers the same as *Run retention now*.

### The legal hold

A person under a [legal hold](#put-a-person-under-a-legal-hold) is passed over by every
erase, of every kind, for as long as the hold stands. Lift the hold when the matter is
closed, and the next run erases what has been due meanwhile.

### The deletion ledger

An erase deletes a document's record at once and queues its file for deletion from the
object store; so does deleting a document by hand, when the store does not confirm at
once. The worker asks the store again on every look, waiting longer after each refusal
(at most an hour), and never gives up — a legal erase is not best-effort.

`GET /api/v1/staff/settings` reports the ledger in its `retention` block:
`pendingDeletions` (files waiting), `failingDeletions` (files the store has refused at
least once), `oldestPendingSince` and `lastErasedAt` (the last erase). A
`failingDeletions` that stays above zero means the object store is refusing: check that
storage is configured and reachable ([Object storage](/en/admin/object-storage/)) and
the server log, which names the store's error. Once the store answers, the queued files
go at the next look.

### After an erase

An erased person who comes back is [rehired](#rehire-an-erased-person) on the same
record: what the erase removed stays empty until you type it again, and the rehired
employment's pay is untouched by the old one's erase.

## The bank-change mail

When a bank account is set or changed, the person is mailed — at their private email
when they have one, else at their work email, in their preferred language — naming the
account's last four digits and nothing more, so a change somebody else made does not go
unnoticed. The mail goes through the installation's own SMTP server after the change is
saved. Sending the account already on file again changes nothing and mails nobody.

- It needs `MAIL_DRIVER=smtp` and the `SMTP_*` settings
  ([Installation](/en/admin/installation/#email-and-observability)). With any other
  driver the mail is skipped, and the server logs
  `staff: bank-account change mail skipped: the mail driver is not smtp`.
- A person with neither email is not mailed, and the server logs
  `staff: bank-account change mail skipped: the person has no email`.
- A send that fails is logged as a warning naming the person's id; the change itself
  stays saved.

## The settings

The settings are `staff:manage`'s, read and replaced through `GET` and
`PUT /api/v1/staff/settings`, and changed on the Staff app's Settings page
([Change the settings](/en/user/staff/#change-the-settings)). A replace carries the revision it read in `If-Match`, so two people
changing the settings at once cannot overwrite each other unseen.

- **Business time zone** (`timeZone`, default `Europe/Oslo`). Every date in Staff —
  a start, an end, "today" — is a calendar day in this zone. Use a city zone such as
  `Europe/Oslo`: a name the server and the database read differently, such as `CET`,
  is refused.
- **Employee number prefix and next number** (`employeeNumberPrefix`, empty, and
  `nextEmployeeNumber`, 1). A person created without an employee number gets the
  prefix and the next number, unpadded: `EMP-` and 7 make `EMP-7`. The prefix is up
  to eight letters, digits or hyphens, and the two together may make at most 16
  characters.
- **Pension provider** and **injury insurer** (`pensionProvider`,
  `injuryInsurer`). The occupational pension (OTP) provider and the occupational
  injury insurer, which the employment contract check reads.
- **Retention periods** (`retentionGraceDays`, 90, from 0 to 3650, and
  `healthRetentionDays`, 365, from 30 to 3650): how many days after a person's last
  employment ended their personnel administration and their health documents are
  erased ([Retention](#retention)). Left out of a replace, each keeps its value.
- **Retention state** (`retention`, read-only): the deletion ledger's pending and
  failing files, the oldest pending, and the last erase ([The deletion
  ledger](#the-deletion-ledger)).

## What can go wrong

- **`timeZone` refused.** The name is unknown, or the server and the database
  disagree about it in winter or in summer. Choose the city zone the abbreviation
  stands for.
- **`employee_number_too_long`.** The prefix and the next number together exceed 16
  characters. Shorten the prefix, or lower the next number.
- **`revision_conflict`.** Somebody changed the settings since you read them. Read them
  again and repeat the change.
- **`user_inactive`.** The user you are linking is disabled. Enable them first, or link
  another.
- **`user_linked_elsewhere`.** The user is linked to another person. Unlink them there
  first.
- **`no_person`.** The person asking for My page is linked to nobody. Link them.
- **`national_id_duplicate`.** Another person already has this national identity
  number. The refusal does not say who; someone with `staff:personal-data` can compare
  by revealing.
- **No bank-change mail arrived.** Check the log for the skip line above, and that the
  person has an email.
- **`storage_unavailable`.** The object store could not be written to or read from:
  storage is not configured, or its root is not writable. Nothing was recorded; fix
  the storage and try again ([Object storage](/en/admin/object-storage/)).
- **`seed_immutable`.** One of the six seeded categories cannot be changed. Add a
  category of your own instead.
- **`category_in_use`.** Documents are still filed under the category you are
  archiving. Move them to another category first.
- **`document_count_exceeded`.** The person has 200 documents, the most one person may
  have — counted whatever you may see, so it can include health documents you cannot.
  Remove documents that are no longer needed.
- **`person_erased`.** The retention run has erased the person, and the record is
  read-only. Rehire them with a new employment, or leave it.
- **`legal_hold`.** The person you are deleting is under a legal hold. Lift it first, if
  the matter is closed.
- **`reassessment_not_due`.** The person is not erased, has been reassessed already, or
  their last employment ended less than twelve months ago.
- **`import_not_semicolon`.** The file is comma-separated. Save it again as CSV with
  semicolons — in a Norwegian Excel, *CSV UTF-8* does.
- **`import_too_large`** or **`import_too_many_rows`.** The file is over 5 MiB or has
  more rows than `STAFF_IMPORT_MAX_ROWS`. Import it in slices.
- **`import_sealed_columns`.** The file carries a national identity number or bank
  account column and you do not hold `staff:personal-data`. Remove the columns, or ask
  for the permission.
- **An unknown column.** The import names it: a misspelt header is refused rather than
  ignored. Correct it to one of the template's names.
- **`export_too_many`.** The register holds more people than `STAFF_IMPORT_MAX_ROWS`.
  Raise it, within its bounds, and restart.
- **`retention_busy`.** The retention worker is running, on this server or another.
  Wait a minute and run it again.
- **`failingDeletions` above zero.** The object store refuses to delete the queued
  files. Fix the storage; the worker retries without end.
- **A retention period refused.** `retentionGraceDays` is 0 to 3650 days,
  `healthRetentionDays` 30 to 3650.
- **Nobody is erased.** The worker is off (`STAFF_RETENTION_ENABLED=0`), the person
  is under a legal hold, still has an employment that has not ended, or has none at
  all; or the period has not run yet.
