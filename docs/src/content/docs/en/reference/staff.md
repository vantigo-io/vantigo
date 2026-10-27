---
title: "Staff module"
description: "The workforce register: people, their field groups and who reads them, the person's photo — HR's, made the linked user's picture in Vantigo and pushed to the directory, else the user's own avatar — the at-a-glance cards and the hours they read from Time, their history, employments with dated terms and the dates the a-melding derives from them, the § 14-6 check, managers and the org view, the sealed national identity number and bank account, the self-service link, the account flag, the account kind, the provider link with its directory profile and pass, groups — listed or derived by a rule, their owners, the traits a provider declares, the sync with the provider's directory and the licences they give — and My page, the document archive with its categories and retention classes, competence — the catalogue with the Norwegian certificates seeded, a person's competences declared, verified, renewed and removed with their evidence, what the person wants — their interests, preferences and availability declaration, stamped Seen by their manager — the competence review and its overdue item, competence profiles assigned or defaulted by department with the gap list and its item, the matrix and the interests overview, the competence worker and its mails, and the Workforce contract with the matching contract's Staff side and the caller's own directory account for the calendar's Outlook sync — the personal-data exports, the register's import and export as CSV, the payroll export, the retention run and its deletion ledger, the erased state, the legal hold and the reassessment, the dashboard's figures and attention items and the thresholds, the settings, the absence types with their a-melding mapping and reporting rules, the holiday schemes and their conversion, the care-day fields, absences requested and registered, overlap, the holiday account with its adjustments and carry-over, the absence events and their classes, sick-leave cases with the arbeidsgiverperiode and their follow-up milestones, egenmelding under the policy and its withdrawal, care days, the company's structure, the business time zone, the permissions and the lock rules."
sources:
  - apps/server/internal/staff
  - apps/staff/frontend
  - openapi/staff.yaml
  - apps/server/internal/openapi/specs/staff.yaml
  - apps/server/internal/db/migrations/00060_staff_account_kind.sql
  - apps/server/internal/db/migrations/00062_staff_groups.sql
  - apps/server/internal/db/migrations/00066_staff_competence.sql
  - apps/server/internal/db/migrations/00067_staff_one_to_one.sql
  - apps/server/internal/db/migrations/00068_staff_competence_profiles.sql
  - apps/server/internal/db/migrations/00069_staff_person_photo.sql
  - apps/server/internal/db/migrations/00071_staff_photo_push.sql
---
The Staff module is the company's register of the people who work for it: who they
are, how to reach them and their next of kin, their employments as Norwegian
arbeidsforhold with dated terms, the company's structure, a self-service link to their
user and the history of every change. It is a vertical-slice module inside the single
Vantigo binary (`apps/server/internal/staff`), owns the `staff` schema in the shared
PostgreSQL database, and serves `openapi/staff.yaml` under `/api/v1/staff`.

It depends on identity, and optionally on Time ([contracts consumed](#contracts-consumed)):
it reads users through `contracts.UserDirectory`, which identity always provides, sends
its one mail through the platform's SMTP seam, and keeps its documents' bytes and the
people's photos in the platform's object store under its own scope, `staff`.
`MODULES=staff` alone is a valid installation. It provides one contract,
`contracts.Workforce` — the [competences people hold](#the-workforce-contract), valid on
a date, the people in employment and their capacity over a window, and the caller's own
person — as the sole provider of that slot, and fills the many-provider calendar slot
with the caller's own absences ([on the calendar](#on-the-calendar)); nothing else of
the register is read by another module. It reads one many-provider
slot, `Deps.IdentityProvisioners` (`contracts.IdentityProvisioner`): the external
identity providers an enabled module offers, whose directory the
[accounts worker](#the-provider-link) writes for a person of a provider's
[account kind](#the-account-kind) — rule 13 of the
[module boundaries](/en/contributing/module-boundaries/). With none, nothing changes but
that the `entra` kind cannot be chosen.

This page grows with the module. What is here today: the [contracts it consumes](#contracts-consumed), the [permissions](#permissions),
the [settings](#settings), the [business time zone](#the-business-time-zone), the
[structure](#the-structure), [people](#people) — the record, the
[directory](#the-directory), the [field groups and audiences](#field-groups-and-audiences),
[the photo](#the-photo), [at a glance](#at-a-glance),
the [history](#the-history) and [deletion](#deleting-a-person) —
[employments](#employments) and their [terms](#terms), the
[derived dates](#the-derived-dates), the [§ 14-6 check](#the--14-6-check),
[managers and the org view](#managers-and-the-org-view), the
[sealed identifiers](#the-sealed-identifiers), the
[self-service link](#the-self-service-link), the [accounts](#accounts), [groups](#groups), [My page](#my-page), the
[documents](#documents) and their [categories](#categories-and-classes),
[competence](#competence) — the [catalogue](#the-catalogue),
[a person's competences](#a-persons-competences),
[what the person wants](#what-the-person-wants) — [interests](#interests),
[preferences](#preferences) and [the availability declaration](#the-availability-declaration) —
[the review](#the-review), [profiles and gaps](#profiles-and-gaps),
[the matrix](#the-matrix), [the interests overview](#the-interests-overview), the
[competence worker](#the-competence-worker) and the
[Workforce contract](#the-workforce-contract) —
[absence](#absence) — the [types](#absence-types), the [holiday schemes](#holiday-schemes) and the
[care-day fields](#the-care-day-fields) — the
[personal-data exports](#the-personal-data-exports), the register's
[import and export](#the-registers-import-and-export), the
[payroll export](#the-payroll-export), [retention](#retention) and its
[deletion ledger](#the-deletion-ledger), the [erased state](#the-erased-state),
the [legal hold](#the-legal-hold) and the [reassessment](#the-reassessment), the
dashboard's [figures and attention items](#stats-and-attention) and the
[thresholds](#the-thresholds), [how a refusal looks](#refusals-and-revisions), the [locks](#locks) and the [endpoints](#endpoints).

## Contracts consumed

Beside identity's `contracts.UserDirectory` and the many-provider
`contracts.IdentityProvisioner`, Staff reads one business module, optionally:
**`contracts.Timesheets`** (`Deps.Timesheets`), the hours each identity user logged and
had approved in a week, which Time provides as the slot's sole provider — for the hours
card [at a glance](#at-a-glance). It is **consumer-gated**, as
`contracts.ProjectActuals` is: Time performs no authorization, and Staff's own rule
decides who may see whose hours — the Hours group: the person, a manager of theirs today
by [the one manager rule](#managers-and-the-org-view), `staff:manage` and
`staff:personal-data` — before it asks. It asks through one accessor, `timesheetsWeek` in
`contractscalls.go`, with the request's context and never inside one of its
transactions, for the one user the person's self-service link names. With `time`
disabled the slot is nil: the card says time tracking is off, never zero, nothing fails,
and `MODULES=staff` alone stays a valid installation. Nothing else of Time is read, and
Time's own permissions, screens and rules are untouched — see the consumer-gated
contracts beside rule 12 in the [module boundaries](/en/contributing/module-boundaries/).

**The person's picture** travels through three contracts Staff already consumes (photo
sync design S4), one way — Staff → identity → the directory, never back:
`contracts.AccountProvisioning.SetManagedAvatar` is the write, HR's photo made the
linked user's managed avatar, or with nil its clear ([the photo](#the-photo));
`contracts.UserDirectory.Avatar` and `AvatarInfo` are the reads, the user's effective
avatar — the managed one while it stands, else their own — with its stamp, `AvatarInfo`
without the bytes for the person response and the push's comparison, `Avatar` with them
only to stream or push the picture;
and `contracts.IdentityProvisioner.SetPhoto` and `ClearPhoto` write the account's photo
in the provider's directory ([the directory's photo](#the-directorys-photo)). Each is
called through its own accessor in `contractscalls.go` (`accountsSetManagedAvatar`,
`usersAvatar`, `usersAvatarInfo`, `providerSetPhoto`, `providerClearPhoto`), with the request's or the
worker's context and never inside one of the module's transactions. With no module
providing accounts the write is skipped and logged; Staff never reads the directory's
photo.

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

Delivery C's Staff migration is `00051_staff_accounts.sql` (00050 is identity's, the
same delivery's: one migration per schema). It changes two things of 00042's and adds
three columns: `identity_links.disable_requested_at`, when *Disable now* asked for the
link's user to be disabled and identity has not yet confirmed it ([Run now and Disable
now](#run-now-and-disable-now)), `people.account_revision`, the [account flag's](#accounts) own revision, and
`identity_links.synced_employee_number`, the employee number the accounts worker last
wrote to identity for the link's user — written only after a `Prepare` or an `Update`
identity accepted, and reset with the worker's other columns whenever the link names
another user, so a person HR renumbered is still known by the number identity holds.
`identity_links.external_id` becomes nullable, so the accounts worker's
`preparing` link names no user until identity has created one; every reader of a link
takes one naming no user as a person linked to nobody — `GET /people/{id}/user` reads
`linked: false`, the person and the exports name no user, and no signed-in user is the
person through it. And `ux_people_employee_number_upper` is rebuilt on
`upper(employee_number COLLATE "C")`: `upper()` under the database's own collation is
that collation's (under a Turkish one `upper('i')` is `İ`), so the index — and the
queries that look a number up or skip past taken ones, which ask the same expression —
judges case the way identity's key does (`contracts.NormalizeEmployeeNumber`) on
Staff's alphabet of a–z, digits and hyphens, whatever the installation's locale. For
the absence settings the owner asked for (B's open questions 11–13), it also marks the
seed `ammefri` paid, seeds the reserved `absent_hours`, `absent_hr_only` and
`absent_hr_only_hours` beside `absent`, and
adds `settings.erase_pregnancy_health_types` and `settings.erase_sick_case_dates`
(booleans, false) — two more settings columns against 00042's comment, as B added
one. The Down deletes the links naming no user and restores both, drops the two
settings, deletes the three reserved types it seeded and makes `ammefri` unpaid
again — but refuses, naming them, while any absence names one of those types: it
cannot give such a row another type without widening who reads it. The Up refuses,
naming the key, when a company type already holds one of the keys it reserves.

The Entra phase's delivery B adds `00060_staff_account_kind.sql` (00059 is identity's,
the same delivery's). It adds `people.account_kind` — `local`, `external` or `entra`,
`local` by default ([the account kind](#the-account-kind)) — and four settings
columns: `default_account_kind` (`local`), `account_kind_seeded` (false until Staff has
seeded the kinds from identity's recorded mode, which a migration may not read),
`directory_fields` (the [directory profile's](#the-directory-profile) switches, a JSON
object seeded with the defaults) and `provider_status` (what the accounts worker last
saw of each provider, `{"<key>": {status, reason, checkedAt}}`, from which the
installation's provider items are judged). And it adds a provider link's columns to
`identity_links`: `external_upn` and `external_email` (the principal name and the
address the provider answered), `announced_at`, `pass_issued_at` and `reported_codes`
(`text[]`, empty by default) — [the provider link](#the-provider-link). A provider
link is a second row per person, `provider` the provider's key beside `identity`; the
existing `(person_id, provider)` and `(provider, external_id)` indexes already admit
it. Still no CHECK constraint: `contracts.ValidAccountKind` and the module's
validation own the kinds. The Down deletes every provider link — the external
accounts they name stay in the provider's directory — and drops the columns.

The Entra phase's delivery D adds `00062_staff_groups.sql` (00063 is the entra
module's, the same delivery's) and four tables, the [groups](#groups): `groups` (unique
`key`; `archived_by_user_id` beside `archived_at`), `group_members` and `group_owners`
(each keyed by group and person, indexed by person, deleted with their group), and
`group_traits`, a group's section per provider — the values, `enabled` and
`disabled_at`, the sync's `external_id`, `state`, `last_values_hash`,
`last_members_hash`, `last_error` and `last_error_field`, `synced_at`, `retired_at`, and
the claim's `attempts`, `next_attempt_at`, `lease_id` and `lease_until`, as a link
holds them (indexed by provider and `next_attempt_at`). Still no CHECK constraint: the
module validates the kinds and the states, and a provider judges its traits' values.
The Down drops the four tables; the groups a provider made in its directory stay there.

Staff phase 2's delivery A adds `00066_staff_competence.sql` and three tables, the
[competence](#competence): `competence_categories` (unique `key`; the five seeds;
`archived_at`), `competences` (unique `key`; indexed by category; `hidden` and
`archived_at`; the fifteen certificate seeds) and `person_competences` (indexed by
person, by competence, and by `valid_to` where the row is current and has one, for
the worker and the items; `evidence_document_id` references `documents` and is
cleared when the document is deleted; `superseded_by_id` names the row that renewed
it). At most one current row per person and competence is the index's
(`ux_person_competences_current`, partial on `superseded_by_id IS NULL`), and the
handler reports it as `competence_held`. Still no CHECK constraint: the module's
validation owns the kinds, the levels, the sources, the bases and the dates. The Down
drops the three tables.

Delivery B adds `00067_staff_one_to_one.sql`: [what the person wants](#what-the-person-wants)
and [the review](#the-review). Four tables — `person_interests` (unique per person and
competence, `ux_person_interests_person_competence`; indexed by competence),
`person_preferences` (unique per person and `kind`, `ux_person_preferences_person_kind`;
`value` is `jsonb`, `weight` a `smallint`), `availability_declarations` (indexed by
person; `weekday`, `from_date`, `to_date`, `start_minute` and `end_minute` all
nullable) and `competence_reviews` (indexed by person and `reviewed_on` descending, for
the latest review; `reviewer_user_id` is identity's user, opaque here, and
`reviewer_person_id` the reviewer's linked person, `ON DELETE SET NULL` so the delete of
a person created in error clears it and the review stays). Each of the three wish tables
carries the Seen stamp, `seen_at` and `seen_by_user_id`, and `person_competences` gains
the same two columns. `settings` gains `competence_review_interval_months` (`smallint`,
default 12). None of the new keys to the person cascades. Still no CHECK constraint: the
module's validation owns a preference's kind and value, the weight, the weekday, the
days and the minutes, and the interval, and reports each per field. The Down drops the
four tables, the settings column and the two stamp columns.

Delivery C adds `00068_staff_competence_profiles.sql`: [profiles and gaps](#profiles-and-gaps).
Three tables — `competence_profiles` (unique `key`, `ux_competence_profiles_key`;
`archived_at` and `revision`), `competence_profile_items` (unique per profile and
competence, `ux_competence_profile_items_profile_competence`; indexed by competence,
for `competence_in_use`; `min_level` a nullable `smallint`, `must_be_valid` true by
default) and `person_profiles` (unique per person and profile,
`ux_person_profiles_person_profile`; indexed by profile) — and one column,
`departments.competence_profile_id`, referencing `competence_profiles` and indexed
(`ix_departments_competence_profile`): the department's default profile. The profiles,
their items and the departments' column are about no person and have no retention
class; `person_profiles` is class D. None of the new keys cascades: the delete of a
person created in error deletes their assignments, and the erase deletes them. Still
no CHECK constraint: the module's validation owns the key, the items' bounds, one item
per competence, the levels and the archived profile, and reports each per field.
Nothing about a gap is stored — the gap list, its item and the matrix are computed on
read. The Down drops the departments' index and column, then the three tables.

The person-glance delivery adds `00069_staff_person_photo.sql`: [the photo](#the-photo).
Three nullable columns on `people` — `photo_object_key` (`varchar(200)`, the photo's key
in the object store under the `staff` scope, `people/<person id>/photo/<uuid>`),
`photo_content_type` (`varchar(40)`, `image/jpeg` or `image/png`) and `photo_updated_at`
(`timestamptz`, when it was set, the apps' cache key) — all class D. No table, no index,
no setting. Still no CHECK constraint: the module's validation owns the type, the size
and the dimensions, and reports them on `file`. Nothing about [a glance](#at-a-glance) is
stored. The Down drops the three columns; the objects they named stay in the store,
named by nothing.

The photo-sync delivery adds `00071_staff_photo_push.sql` (00070 is identity's, the same
delivery's): one nullable column on `identity_links`, `photo_pushed_stamp`
(`varchar(80)`), written on a provider link alone — the stamp of the picture the
accounts worker last pushed to the directory, `managed:<stamp>`, `own:<version>@<epoch>` or
`none`, null before the first push ([the directory's photo](#the-directorys-photo)). It
is class C with the rest of the link ([the classes](#the-classes-column-by-column)). No
index, no setting, no CHECK constraint: the worker writes it. The Down drops the column,
so the next claim of each link pushes its picture once more.

## Permissions

| Key | Name | Grants | Sensitive |
| --- | --- | --- | --- |
| `staff:access` | Use Staff | The app at all: your own record, absence, documents, competences and export; the [competence catalogue](#the-catalogue)'s visible rows and every [competence profile](#profiles-and-gaps). **Every operation requires it.** | no |
| `staff:view` | View the staff directory | Everyone's work details, employments and terms without pay, the structure — the departments' default competence profiles with it — the [calendar](#the-calendar) with types hidden, and the active people's [competences](#a-persons-competences) without their detail, in the [competence matrix](#the-matrix) too; the [Workforce contract](#the-workforce-contract)'s answers. | no |
| `staff:approve` | Decide absence | Decide anyone's absence, see types other than HR-only and open cases' dates, mark milestones. It [decides](#decisions) anyone's absence requests but an HR-only type's, is the [Approver audience](#field-groups-and-audiences), sees the [inbox](#the-approvals-inbox) and the [calendar](#the-calendar), and makes `GET /meta`'s `canDecide` true; it reads open [sick-leave cases](#sick-leave-cases) and marks their milestones; it [verifies](#verification) anyone's competences but its holder's own, and adds, changes, renews and removes them, and [records their competence reviews](#the-review); it reads no one's [wishes](#what-the-person-wants). | no |
| `staff:manage` | Manage staff | HR: people (not the sealed fields), employments and terms with pay, the structure, absence types and holiday schemes, the [competence catalogue](#the-catalogue) and the [competence profiles](#profiles-and-gaps), anyone's competences — their chains included — and the profiles assigned to them, the [matrix](#the-matrix) and the [interests overview](#the-interests-overview), the settings, documents other than health and their categories, the [account flag](#accounts), the [groups](#groups), imports and exports, the retention run by hand, the thresholds' acknowledgement. | yes |
| `staff:personal-data` | Reveal and erase personal data | Reads every person's every group; reveals and sets the national identity number and the bank account; health documents and notes, HR-only absence types, anyone's personal-data export, legal holds, erasure. It changes nothing else of a person but documents — the rest is `staff:manage`'s. Erasure is the [retention run](#retention)'s, on its schedule (run by hand by `staff:manage`); it alone writes the milestones' notes and [decides HR-only types](#decisions). | yes |

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
`staff:manage`, or `managesAnyone`: who may decide absence) and `canRunAccounts`
(`staff:manage`: running the accounts worker by hand and disabling an account now) — so
the app never re-derives a rule. For `staff:manage` alone it also answers the
[accounts'](#accounts) `defaultAccountKind`, `providers` and `provisioningRolesSet`.

## Settings

One row, written by the migration. `GET /settings` and `PUT /settings` are
`staff:manage`'s. The contract exposes twenty fields, a derived block and the
acknowledged thresholds.

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
| `selfCertOccurrencesPerYear` | 4 | 4 to 52 occurrences in twelve months; read under `custom` only; left out, kept. |
| `selfCertDaysPerYear` | 24 | 12 to 365 self-certified days in twelve months; read under `custom` only; left out, kept. |
| `selfCertAfterMonths` | 2 | 0 to 2 months of employment before egenmelding may be used; left out, kept. |
| `autoCarryOver` | false | Carry holiday left at a year's end into the next as forced, without HR's action; left out, kept. |
| `createBeforeDays` | 14 | 0 to 90 days before the account's effective date that the accounts worker prepares it, disabled ([accounts](#accounts)); left out, kept. |
| `deleteExternalAfterDays` | — | 1 to 3650 days after the last employment ended that a person's account in a provider's directory is deleted — soft-deleted there, once it is disabled and no legal hold is on the person ([the provider link](#the-provider-link)) — or `null` for never; left out, kept, and absent on the wire when never. The same days after a group was archived, or a provider turned off for it, the group is deleted in the directory; never renames it `… (archived)` and keeps it ([the sync](#the-sync)). |
| `defaultAccountKind` | `local`, seeded once from identity's recorded mode | The [account kind](#the-account-kind) a new person takes: `local`, `external` or a provider's key (`entra`), which is `provider_missing` on the field unless an enabled module offers that provider; left out, kept. People already registered keep theirs. While the kinds are [not yet seeded](#the-account-kind), a change of it is 503 `accounts_unavailable` and nothing is saved. |
| `directoryFields` | every switchable field on but `workAddress` and `phones` | `{"<key>": boolean}` over the [directory profile's](#the-directory-profile) switchable fields; a key left out keeps its value, a key that is not a switchable field is a field error on `directoryFields.<key>`. |
| `erasePregnancyHealthTypes` | false | Put the pregnancy and health-appointment rows on the health schedule ([what is due](#what-is-due)); left out, kept; `null` refused on the field (400), never read as false. |
| `eraseSickCaseDates` | false | Make a closed case's erase remove the case itself ([what an erase does](#what-an-erase-does)); left out, kept; `null` refused on the field (400). |
| `competenceReviewIntervalMonths` | 12 | 0 to 60 months after which a person in employment today is [review-overdue](#the-review) — counted from their latest competence review, or from the start of their current unbroken employment run when that is later; 0 turns the overdue item off; left out, kept. |

**Who changed an erase setting.** A `PUT /settings` that turns `erasePregnancyHealthTypes`
or `eraseSickCaseDates` on or off records the installation event `settings_updated` —
about no person, `{fields: {<name>: {from, to}}}`, the caller its actor, at the
request's instant — since each erases for good; a replace that changes neither records
nothing, and no other field of the settings is recorded.

**The self-certification policy.** A preset writes its own numbers, whatever the
request carries: `minimum` — the statutory one — 3 days an occurrence and 4
occurrences in twelve months; `extended` 8 days an occurrence and 24 days in
twelve months. The number a preset does not use keeps what it holds. `custom` takes the
three as sent, but never below the statutory minimum (ftrl. § 8-23: 3 days an
occurrence, 4 occurrences and 12 days in twelve months), which no agreement goes
below; the floors are judged whatever the policy. Egenmelding keys on the policy's
name: under `minimum` and `extended` it reads the preset's numbers, and the stored
counters are read under `custom` only ([egenmelding](#egenmelding)).

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
retention periods, the self-certification policy, the carry-over, the four account
settings, the two erase settings and the competence review interval, which a body that
leaves them out keeps. Every bad field is a 400
naming it (a period out of its bounds is a plain field error); the whole request is
judged before anything is written.

## The business time zone

Every date in this module — a start, an end, "today" — is a calendar day in the
**workspace's business time zone**: one setting for the installation, on identity's
system settings, read through `contracts.BusinessCalendar` (`Deps.Calendar`, always
set once composed, in worker mode too) and set by an Owner with
`PUT /api/v1/identity/owner/workspace`
([Workspace administration](/en/user/workspace-administration/#workspace-settings)).
Until the time zones design
([`docs/superpowers/specs/2026-10-19-time-zones-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-19-time-zones-design.md))
it was this module's own column, `staff.settings.time_zone`; migration `00053` drops
it, warning in the log when its value was not `Europe/Oslo`, and `PUT /settings` no
longer takes `timeZone`. `GET /settings` and `GET /meta` still answer it, as the
workspace's.

"Today" is computed once per request, in Go, as the zone's calendar day of the server
clock (`holidays.DayIn`), and handed to SQL as a date parameter; SQL's `current_date`
is never used, since it is the database session's day in the database's own zone.
`GET /meta` answers the zone and `today`, so the app's notion of today is the server's:
at 22:30 UTC on 30 September the Oslo day is already 1 October. The zone is read
before any transaction opens — the calendar's read takes a pool connection of its
own, which a transaction holding locks must not wait for — and kept for the request.

The checks a zone is put through when it changes (Go must load the name; the database
must read it the same way at a winter and a summer instant, which `CET` fails) are
identity's now, documented under
[Administration](/en/admin/authentication/#the-authority-mode). A stored name Go
cannot load is read as UTC by the calendar rather than failing every request; setting
the zone again is the way back.

## The structure

Four small tables, not an org tree (design S6):

- **Legal entities** — the juridisk enhet, with its organisation number.
- **Business units** — the virksomhet or underenhet under a legal entity, with an
  organisation number of its own. The a-melding reports per business unit, and an
  employment names one.
- **Departments** — a code, a name, an optional manager (a person), an optional
  parent department, a cost centre, a division and an optional default
  [competence profile](#profiles-and-gaps) (`competenceProfileId`). A department's
  manager is the default manager of the people whose terms name the department and no
  manager of their own; its default profile reaches every person whose place's terms in
  force today name it. The profile must exist and, newly named, not be archived — a
  plain field error on `competenceProfileId` (400) otherwise — while a department keeps
  its own default archived since, as it keeps an archived parent; that default reaches
  nobody while it stays archived. Left out of a write, the department has no default.
  The department's write records no event, as before.
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

A list row also carries **`onLeaveToday`** (People's "on leave today", design §7): true
when an approved or registered absence of whole or part days covers today, of any type —
the rows `GET /stats` counts in `onLeaveToday`, without the figure's test of an
employment in force today, read in the same request for the people
of the page alone. It is the same
to every reader of the list and names no type, so a colleague learns no more than the
calendar's "absent" bar shows them; a request does not count, nor an hour registration.

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
| Work | name, employee number, work email and phone, work country, language, roles; the person's place today — `manager`, `department`, `title`, `businessUnit`, `location`; `photoUpdatedAt` and [the photo](#the-photo) itself | ✓ | ✓ | ✓ | ✓ | ✎ | ✓ |
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
| Competence | a [competence row's](#a-persons-competences) `competence`, `level`, `validFrom`, `validTo`, `verified`, `superseded`, `expiring`, `expired`, `declaredAt`, `revision` | ✎ declare; change and remove their own declared rows; renew | ✓ | ✎ add, change, verify, renew, remove | ✎ add, change, verify, renew, remove | ✎ all, the chain too | ✓ |
| Competence detail | a row's `issuedOn`, `issuer`, `reference`, `evidence`, `source`, `verifiedAt`, `verifiedBy`, `verifiedBasis`, `verifiedNote`, `seenAt` | ✓ file evidence | — | ✓ | ✓ | ✎ file evidence | ✓ file evidence |
| Wishes | [what the person wants](#what-the-person-wants): the interests, the preferences, the availability declaration and each row's `seenAt` | ✎ the three lists | — | ✓ stamp Seen | — | ✓ stamp Seen | ✓ |
| Reviews | the [competence reviews](#the-review) and `lastCompetenceReview` | ✓ | — | ✎ record | ✎ record | ✎ record | ✓ |
| Profiles and gaps | the [competence profiles](#profiles-and-gaps) that reach the person, their gap list and the export's `profileAssignments` | ✓ | — | ✓ | ✓ | ✎ assign | ✓ |
| Contract | the contract card [at a glance](#at-a-glance): whether the employment in force is signed | — | — | — | — | ✓ | — |
| Hours | the hours card [at a glance](#at-a-glance): this week's hours logged and approved, read from Time, against the hours the contract expects | ✓ | — | ✓ | — | ✓ | ✓ |

- **Self** is the user a person's self-service link names, in whatever state the
  accounts worker left it (pending, disabled, failed …) — except a worker link the
  worker blocked as someone else's account: one whose user carries another employee
  number (`mismatch`) or whom Staff no longer manages (`account_not_managed`) gives
  that user nothing of the person, neither Self nor Manager; `GET /me` answers
  `no_person` and `GET /people/{id}` the unknown id's 404, as for a user linked to
  nobody. Nor is it the person's user anywhere else: a manager linked only so is no
  manager-decider (their reports' requests mail the holders by right, and a head of
  the structure whose only manager is linked so approves their own), and a holder by
  right linked so is greeted as identity names them, not as the person. HR's self-service link (`linked`) never carries either reason. **Colleague** is `staff:view`, and only for a person **active today**: an
  unannounced hire, a leaver and someone with no employment stay out of the colleague
  directory, and since ids are sequential they answer the unknown id's 404.
  **Manager** is a caller whose self-service link names a manager of the person
  today: the manager the terms in force today name on one of the person's employments,
  or the department's when they name none (see [managers](#managers-and-the-org-view));
  concurrent employments may give two. It needs no permission. A caller holding several
  is every one of them at once and reads what any may. **Approver** is `staff:approve`
  (delivery B, plan reading 18): every person's work and non-pay employment, and their
  absences with every type but an HR-only one (read as leave) and the detail — no
  history, no private fields, no pay, no documents, no hours: the Approver verifies
  competence and decides absence, and the Hours group is the person's, their
  manager's and HR's alone.
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
[reassessment](#the-reassessment) and the [complete export](#the-personal-data-exports)),
`canDecide` (the caller decides the person's absence), `canVerifyCompetence` (the
caller [verifies](#verification) the person's competences and records their
[reviews](#the-review): a manager of theirs today, `staff:approve` or `staff:manage`,
never the person), `canReadWishes` (the caller reads the Wishes group: the person, a
manager of theirs today, `staff:manage`, `staff:personal-data`),
`canStampWishesSeen` (the caller [stamps their wishes Seen](#what-the-person-wants): a
manager of theirs today or `staff:manage`, never the person, never `staff:approve`
alone) and `canAssignProfiles` (the caller [assigns the person's competence
profiles](#profiles-and-gaps): `staff:manage`), `canSetPhoto` (the caller [sets and
removes the person's photo](#the-photo): the person themselves, `staff:manage` or
`staff:personal-data`; false for an erased person), and who the caller is to the
person: `self` (the caller's self-service link names them) and `isManager` (the caller
manages them today by [the manager rule](#managers-and-the-org-view); false for oneself) —
which the page reads to offer a manager's writes on a sick-leave case, never guessing.

The **hold** group is about the record's retention rather than the person: whether a
legal hold stands, with its reason and when it was set, when the retention run erased
the person and when they were reassessed. It is HR's alone — `erasedAt` and
`reassessedAt` are absent until set, `legalHold` is `{held: false}` when there is none.

**The person page's Planning tab.** When the Planning module is on, the person page
gains a **Planning** tab — the person's [timeline](/en/reference/planning/#the-persons-timeline)
— for a `planning:view` holder and for the person themselves. The tab is the host's:
`@vantigo/staff-ui`'s `PersonPage` only opens a slot, `extraTabs`, which the host's
route fills from `@vantigo/planning-ui`, so neither package imports the other
([rule 7](/en/contributing/module-boundaries/)). Staff reads nothing of Planning and
answers nothing new for it.

### The photo

A person has at most one **photo** (`GET|PUT|DELETE /people/{id}/photo`, person-glance
design S1), and it is **HR's**: the register's own, set by `staff:manage` and
`staff:personal-data` alone. The person's own picture is their Vantigo avatar, which
identity keeps and the user sets through identity's `/api/v1/identity/account/avatar` —
from My page or **Settings › Profile** — never through Staff (photo sync design S2). The
one in front is **the effective picture**: HR's photo when set, else the linked user's
own avatar. HR's photo is made the linked user's **managed avatar** in identity, which
stands in front of their own avatar and keeps them from changing it while it stands
([the managed avatar](/en/admin/authentication/#the-managed-avatar)), and the accounts
worker pushes the effective picture on to the provider's directory
([the directory's photo](#the-directorys-photo)). Nothing flows back. HR's photo's bytes
are in the object store under the module's `staff` scope, at
`people/<person id>/photo/<uuid>` — a fresh key per upload, so a replaced photo is never
overwritten in place; the person's row holds the key, the type and when it was set
(`photo_object_key`, `photo_content_type`, `photo_updated_at`, class D).

- **Who sets it.** `PUT` sets the photo or replaces it whole, and `DELETE` removes it:
  `staff:manage` and `staff:personal-data` — `canSetPhoto` on the person's capabilities,
  false on an erased person. Anyone else who sees the person — the person themselves, a
  manager, a colleague, the Approver — gets the uniform 403, and anyone who does not the
  unknown id's 404. The caller is judged in a snapshot first, so a caller who may not
  see an erased person learns nothing from its 409, and again under the lock. An
  [erased](#the-erased-state) person is `person_erased` (409). Neither takes `If-Match`:
  the row's revision moves on, and the last write wins. `PUT` answers 200 with the
  person as the caller reads them; `DELETE` answers 204, and the 404 when there is no
  photo.
- **The file.** One multipart part, `file` — any other part is skipped — read no
  further than 5 MiB and a byte; the router caps the operation's body at 5 MiB and
  64 KiB of framing. Its own bytes decide what it is: the name and the type it was sent
  under are no evidence either way. It must sniff as a JPEG or a PNG (the documents'
  sniffing, `sniffDocumentType`), and `image.DecodeConfig` — which reads the header
  alone and never decompresses, so a decompression bomb costs nothing — must decode it
  as that same format, each side at most 4096 pixels (identity's rule for an avatar). It
  is stored and served as the type it sniffed as, and stored **without its metadata**:
  a JPEG's Exif and XMP (`APP1`) and `APP13` segments, and a PNG's `eXIf`, `tEXt`,
  `zTXt` and `iTXt` chunks, are cut out before the object is written — the pixels are
  never decoded or re-encoded — so a phone's location and camera details never reach
  the store. The upload carries the documents' rate limit (`StaffDocumentUpload`, 600
  an hour per client address), 429 past it. A file that breaks a rule is
  `photo_invalid` (400) on `file`, with the rule's words: *The photo must be a JPEG or
  PNG*, *A photo is at most 5 MiB*, *A photo is at most 4096 pixels on each side*. No
  `file`, an empty one, two `file` parts and a body that cannot be read as a multipart
  form are plain field errors on `file`, with no code.
- **The order.** The object is written to the store before the person's lock is taken —
  a store that cannot write it is `storage_unavailable` (503), with nothing recorded.
  Then, under the person's lock (`withPersonLock`, refusing an erased person), the caller
  is judged again, the row set and its revision moved on, the replaced photo's object
  queued in the [deletion ledger](#the-deletion-ledger) and `photo_set` recorded, in one
  transaction. Whatever refuses or fails after the write removes the new object again,
  in one attempt. A `DELETE` clears the three columns, queues the object and records
  `photo_removed` the same way. A replaced or removed photo's object is never removed
  inline: the ledger removes it once the transaction has committed.
- **The account's picture, at once** (`person_photo_sync.go`). Once a `PUT` or a
  `DELETE` has committed, and the person's self-service link names a user, Staff calls
  identity's `SetManagedAvatar` with the bytes as stored (their metadata already
  stripped), the source `staff` and the stamp — `photo_updated_at` in UTC as RFC 3339
  with nanoseconds — or, after a `DELETE`, clears it with nil. The actor is the HR user
  who made the request; the call is made on no connection, under the call timeout, and
  is not cut short by the request ending. It **never fails the request**: identity's
  `ErrAccountPrivileged` (an Owner's or a SystemAdmin's picture is theirs — reading 2,
  no item) and no module providing accounts are logged at Info, anything else at Warn,
  and the worker puts it right. A person with no linked user calls nothing. The write
  also moves the person's identity link's next attempt to now, so the next poll — within
  the hour by default — pushes the picture on to the directory rather than the next
  civil midnight.
- **The account's picture follows the link.** When HR changes or removes the person's
  [self-service link](#the-self-service-link), the old user's managed avatar is cleared
  and, when HR has a photo, the new user's set — at once, after the commit and outside
  the transaction, logged on failure and put right by the worker — and the identity
  link's next attempt moves to now (`NudgeAccountLink`; a link already due is left as
  it is).
- **The account's picture, reconciled** (reading 5). On every claim of a person's
  identity link — after the link's user is read and before the
  [provider column](#the-provider-link) — the [accounts worker](#the-accounts-worker)
  reads the user's effective avatar (`UserDirectory.Avatar`) and compares: HR's photo on
  the person whose stamp is not the managed stamp identity answers
  (`managed:<stamp>`) → the photo read from the store and `SetManagedAvatar` again, as
  `system:staff-accounts`; no photo on the person but a managed avatar Staff set →
  cleared. For an Owner or a SystemAdmin a managed avatar Staff set before they became
  one is cleared and none is set (reading 15). It reads the stamp alone
  (`UserDirectory.AvatarInfo`) and the photo's bytes only when it writes. It is skipped
  when identity reported no user this claim, and for an erased person, whose erase
  cleared it. A
  failure is logged and changes nothing on the link: the next claim tries again. So a
  person linked after HR set the photo, or an inline write that failed, is right by the
  next claim, and the provider column of the same claim pushes the picture on.
- **Reading it.** `GET /people/{id}/photo` streams **the effective picture** to everyone
  who reads the person's Work group — everyone who sees them — with its type, its
  length, `Cache-Control: private, max-age=300`, `X-Content-Type-Options: nosniff` and
  no `Content-Disposition`: it is shown in place. HR's photo when set, its object read
  after the row's snapshot; else the linked user's effective avatar, read through
  `UserDirectory.Avatar` after the snapshot — never for an erased person — and streamed
  as identity holds it — identity
  strips no metadata from an avatar the user uploads, though My page's downscale of a
  large picture (over 1 MiB or 1024 px) re-encodes it and drops its metadata. A person the caller may not see, one with
  neither picture, one whose object the store no longer has, and one whose avatar the
  directory cannot answer (logged), is the bare 404; a store that cannot be read is
  `storage_unavailable` (503).
- **The cache key and the source** (reading 7; photo sync reading 9). The person's
  response carries `photoUpdatedAt`, when the effective picture was set — HR's photo's
  `photo_updated_at`, else the avatar's `updatedAt` — and `photoSource`: `record` for
  HR's photo, `account` for the user's own avatar; both are the Work group's and absent
  when there is no picture, and the avatar's stamp and time (`AvatarInfo`, no bytes) are
  read only for an audience that reads the Work group, after any transaction, and never
  for an erased person. The directory's row (`StaffPersonListItem`) carries
  Staff's own `photoUpdatedAt` alone, with no source: the list reads no avatars. The
  apps fetch `GET /people/{id}/photo?v=<photoUpdatedAt>`, so a new picture is a new URL;
  the server ignores `v`.
- **The events** (reading 8). `photo_set` and `photo_removed` name the field `photo`
  with the content type alone — `{"fields": {"photo": {"from": "image/png", "to":
  "image/jpeg"}}}`, without `from` on a first photo; `photo_removed` carries `from` —
  never the bytes. The field is class D and the Work group's, so everyone who reads the
  person's [history](#the-history) reads the events whole. The managed avatar's own
  writes are identity's audit (`user.avatar-managed`, `user.avatar-unmanaged`), and the
  push is the provider link's `account_external_updated`.
- **The exports and the erase.** Both [personal-data exports](#the-personal-data-exports)
  carry `photo`, `{contentType, updatedAt}` — HR's photo alone, never the user's
  avatar — or null when there is none; the bytes are `GET /people/{id}/photo`'s. The
  [D erase](#what-an-erase-does) queues the photo's object in the ledger and nulls the
  three columns with the other D columns, and the
  [delete of a person created in error](#deleting-a-person) queues it likewise. The D
  erase also clears the linked user's managed avatar through identity, once its
  connection is returned, and an erased person's page and photo never fall back to the
  account's avatar (reading 21). The photo is in neither the register's
  [CSV import and export](#the-registers-import-and-export) nor the payroll export.

### At a glance

`GET /people/{id}/glance` (person-glance design S4, S5) answers, in one response, every
card of the Profile tab's **At a glance** row the caller may read. It is computed on
read — nothing about it is stored — with "today" the business zone's day and "this year"
its calendar year (reading 6). Whoever sees the person reads it; anyone else gets the
unknown id's 404. Each section is present when the caller reads its group and absent
otherwise — never zero, never null — and a section's own fields may be narrower still.
Each is read through the rule its own endpoint already has, so the glance never counts a
day differently from the tab it sums up.

| Section | Content | Audience (field group) |
| --- | --- | --- |
| `sickDays` | `selfCertified` (kind `sick_self`, egenmelding) and `certified` (`sick_certified`, sykmelding): this year's sick working days in approved and registered rows, each weighted as the payroll file weighs it | Absence type: the person, their manager, the Approver, HR |
| `selfCertification` | `occurrencesUsed` and `daysUsed`, with `occurrencesLimit` under the minimum policy, `daysLimit` under the extended one, both under a custom one — the [egenmelding standing](#egenmelding) as `GET /people/{id}/self-certification` reports it, over its twelve months | Absence detail: the person, their manager, the Approver, HR |
| `holiday` | `takenToDate`, `planned`, `remaining` and `overdrawn`, in working days, from this year's [holiday account](#the-holiday-account) | Absence detail |
| `childSick` | `days`: this year's registered `sykt_barn` working days; `entitlement` and `remaining`, the [care days](#care-days)' | Absence type; `entitlement` and `remaining` Care days alone (the person, HR), and only when the care-day fields are recorded |
| `employment` | `startDate`, `tenureMonths`, `ftePercent` and `title`; `probationEndsOn` | Employment dates, a colleague included; `probationEndsOn` Employment (the person, their manager, the Approver, HR) |
| `absence` | `now` and `next`, each `{firstDay, lastDay, shownAs, typeId?}`; `openCase`, `{firstDay, day}` | Absence, a colleague included; `openCase` Absence detail |
| `contract` | `signed`: `true`, `false`, or null with no employment in force | Contract: `staff:manage` alone |
| `hours` | `timeTracking`; `weekStart`, `logged`, `approved` and `expected` | Hours: the person, their manager, `staff:manage`, `staff:personal-data` |

- **The place.** The employment in force is the person's place today: of the employments
  in force, the earliest by start, then the lowest id — the rule a group's rule and a
  department's default profile judge a person on. Its terms row in force today gives
  `ftePercent`, `title` and the probation, and its `contractSignedAt` — the
  contract-signing stamp the [§ 14-6 check](#the--14-6-check) reports as
  `contractSigned` — gives `contract.signed`. With no employment in force they are
  absent and `signed` is null. `startDate` is the start of the current unbroken run of
  employments — one beginning the day after another ended continues it — and
  `tenureMonths` the whole months from it to today; `employment` is an empty object when
  nothing is in force.
- **The probation.** `probationEndsOn` is the extension's end when one is agreed, else
  the terms row's probation end, and is answered only while it is today or later.
- **Sick days.** The whole calendar year counts, days registered after today included
  (a sykmelding registered ahead). Each working day of an approved or registered row
  counts by its degree, less leave on the same day, and nothing under a holiday-kind
  row — the payroll file's per-day rule; a request, a refusal and a cancel count
  nothing.
- **Holiday.** `takenToDate` is the approved and registered holiday working days from
  1 January to today, and `planned` the approved, registered and requested ones from
  tomorrow to 31 December, both over the account's own rows and their shares.
  `remaining` and `overdrawn` are the account's for the year, as
  `GET /people/{id}/holiday-account` answers them — and the account already subtracts
  every approved and registered day whatever its date, so the three figures do not sum
  to the entitlement: a day planned and approved is both in `planned` and gone from
  `remaining`.
- **Sick child** (reading 4). `days` is every registered `sykt_barn` working day this
  year, the days before the four weeks' employment the right needs included (the care
  days' `used` and `ineligibleDays` together). A manager and the Approver read the count;
  the entitlement and what remains of it stay the Care-days group's, as
  `GET /people/{id}/care-days` is.
- **Absence now and next** (reading 9). `now` is the day row covering today — requested,
  approved or registered, among the rows the caller may see by the absence rule — a
  decided row before a request, then the earliest start, then the lowest id; `next` is
  the earliest such row starting after today, the lowest id on a tie. An hour row is
  neither. Each is shaped as `GET /absences` shapes the row for the caller: the type to
  its readers (`shownAs: type`, `typeId`), and `leave` for an HR-only type to all but
  the person and `staff:personal-data`, who are then shown approved and registered rows
  only. A **colleague** is never shown a row: `now` and `next` are the
  [coalesced spans](#absences) `GET /absences` gives them — the person's approved and
  registered day rows joined where they overlap or adjoin, `shownAs: absent` — so a
  sykmelding over ferie reads as one span, and never a row's own first or last day.
- **The open case.** `openCase` is the newest open [sick-leave case](#sick-leave-cases)
  whose first day is not after today; `day` counts its first day as day 1.
- **The contract.** `contract` is the Contract group's, `staff:manage`'s alone: not
  `staff:personal-data`'s without it, nor the person's or a manager's, though they read
  the § 14-6 check itself.
- **The hours** (S2; readings 3, 5, 10 and 11). `weekStart` is this business week's
  Monday. `logged` is every entry of the person's linked user dated in the seven days
  from it and not rejected — drafts, submitted, approved and invoiced — and `approved`
  the approved and invoiced ones, on every project, whatever the caller's project rights
  or Time permissions: the week's total, with no split by project and no entry. They are
  read from Time through [`contracts.Timesheets`](#contracts-consumed). A linked user
  with nothing in the week reads `logged: 0`, `approved: 0`; a person linked to no user
  has neither. `expected` is the full-time hours per week × the FTE / 100 of the terms
  in force today, summed over **every** employment in force today — two 50 % posts
  expect a full week — and rounded to a quarter of an hour: the contract's week, not
  reduced by absence or public holidays. It is absent with no terms in force. With `time` disabled the section is
  `{timeTracking: false}` and nothing else: off, never zero. So a manager reads their
  report's weekly total without `time:view-all`, through this page alone; Time's own
  screens are unchanged.
- **Two snapshots, Time between them.** The person, the audience and the link are read
  in a short snapshot of their own; Time is asked after it has closed, with the
  request's context, and only for a reader of the Hours group, with Time on and a link;
  then every other section is read in a second snapshot, where the audience is judged
  again, and a section the caller does not read is not read at all. A person gone, or no
  longer seen, between the two is the 404. A Time provider that fails fails the whole
  glance (500), never a card left out: absent must never also mean "could not be read"
  ([module boundaries](/en/contributing/module-boundaries/), rule 12).

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
| `photo_set` | `fields.photo`: the content type before (absent on a first photo) and after — never the bytes ([the photo](#the-photo)). |
| `photo_removed` | `fields.photo`: the content type as `from`. |
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
| `group_joined`, `group_left` | `groupId`, `groupName`, `groupKey` and `source` (`listed` or `derived`): the person joined or left a [group](#members-and-owners) — by the caller for a listed group's members or a group's delete, by the accounts worker's run for a derived one's. Class C; listed to every reader of the history, the payload read by the links group alone — the person, `staff:manage`, `staff:personal-data` — and empty to anyone else. |
| `competence_declared`, `competence_changed`, `competence_verified`, `competence_renewed`, `competence_removed`, `competence_evidence_added` | `competenceId` (the person's row) and `fields`: the competence's key and the row's fields before and after, never the verifier's note — see [the competence events](#the-competence-events). |
| `competence_created`, `competence_updated` | Installation-level (no person): `competenceId` (the catalogue's) and `fields`, each field set or changed. |
| `interests_changed`, `preferences_changed`, `availability_changed`, `wishes_seen`, `competence_reviewed` | `fields`: counts, kinds and a day, never a value — the interests or declarations before and after, the preference kinds touched, the rows stamped Seen, the review's day; class D, shaped by the Wishes and Reviews groups — see [the competence events](#the-competence-events). |
| `erased` | No fields: `kinds`, `[{kind, count}]` — what one [erase](#what-an-erase-does) removed per kind (`D`, `health`, `A`, `cases`, `childSick`, `withdrawal`, `pregnancyHealth`), read by `staff:manage` and `staff:personal-data`, the five health-side entries (`health`, `cases`, `childSick`, `withdrawal`, `pregnancyHealth`) by `staff:personal-data` alone; listed as the hold's kinds are; the actor is the installation's `system` for the worker, the caller for a run by hand; no payload class. |

Delivery B's absence and adjustment events carry their classes per field and are
shaped by the absence groups; they are described under
[The absence events](#the-absence-events).

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
error**: one with no employment, no user link (the accounts' `preparing` link, naming
no user, is deleted with them — `accounts_busy` (409) while the worker holds it), no
document, no absence and no holiday adjustment, and named as nobody's manager — neither a department's nor on anyone's
terms — and not under a [legal hold](#the-legal-hold). An absence or an adjustment is
the history of an employment, which the delete would take with the person. An [erased](#the-erased-state) person is
`person_erased` (409) and a held one `legal_hold` (409), judged before what names
them; anything else is `person_in_use` (409), whose
detail says what names the person — "has documents", "has absences" and "has holiday
adjustments" among the reasons; the person stays. The delete takes the person's row
and then the manager-graph lock before it judges, so no write can start naming the
person between the check and the delete, and a foreign key that refuses anyway is still
`person_in_use`. In one transaction it removes the next of kin, the roles, the person's
rows in [groups](#members-and-owners) — as a member and as an owner, with no event — and the
person's own history — the one exception to the history's append-only rule — queues
the object of the person's [photo](#the-photo), if any, in the
[deletion ledger](#the-deletion-ledger), and records an installation-level
`person_deleted` event carrying the employee number and nothing else. It answers 204.

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
  unlink a `user_unlinked` one. It answers the link. Once it has committed, the
  [picture follows the link](#the-photo): the old user's managed avatar is cleared and,
  when HR has a photo, the new user's set.
- **`GET /people/{id}/user`** answers `{linked, state, managed, user}` — `linked: false`
  and nothing else for a person linked to nobody. The link is the person's and HR's to
  read (`staff:manage`, `staff:personal-data`); anyone else gets the unknown id's 404.
- **`GET /assignable-users?q=`** (`staff:manage`) is the picker: the user directory's
  search over active users, at most twenty, `{id, displayName}`. A user already linked
  elsewhere is listed; linking them answers why not.

A linked person cannot be [deleted](#deleting-a-person) as created in error.

The link is one row with the [accounts](#accounts)' (plan reading 8), so the
self-service link keeps away from the accounts worker's. Before the transaction,
identity is asked (`Accounts.Account`) whether the user to link, or the user linked
now, is **Staff-managed**: a managed user is `link_managed` (409) — reached only through
the worker's employee-number lookup, never by tying a person to them by hand — and
identity unable to say is `accounts_unavailable` (503); nothing is written either way.
Under the person's lock, a link in any state but `linked` — the worker's `preparing`,
`pending`, `active`, `disabled`, `failed` or `blocked` — is `link_managed` too, for a
relink and an unlink alike, but for one the worker left `account_not_managed` (an
Owner stopped Staff managing the user, the final re-check's M-b): it gives the user no
Self, and HR repairs it — unlinks it, or sets it to a user, the same or another, which
makes it an ordinary self-service link (`linked`, not managed, the worker's columns
reset; a `user_linked` event) and gives the user the person's Self again. The
managed-user refusal still applies to that user, and a `mismatch` link stays refused. A link naming no user (the worker's `preparing` link) reads
`linked: false`.

The managed-link check is a check-then-act: identity is asked before the transaction,
so an Owner could adopt the user for Staff between the question and the write. The
second guard is the accounts worker's: it acts on a link only when identity's employee
number for the user is the person's, so a user adopted meanwhile is left alone
(`accountMismatch`). `link_managed` tells `staff:manage` whether a user is
Staff-managed — accepted: managing Staff's users is HR's role. With no module
providing accounts, every link and unlink naming a user answers `accounts_unavailable`
(503), since whether the user is managed cannot be judged.

## Accounts

Delivery C's accounts give a person a user in identity on the dates their employments
set (design S22–S27). This section is the flag and what reads it, the decision table,
and [the accounts worker](#the-accounts-worker) that acts on them. The Entra phase's
delivery B (Entra design D3–D8) makes the account's authority each person's own —
[the account kind](#the-account-kind), in place of the installation's authority mode —
and adds a second link, to the person's account in an external provider's directory,
which the same worker drives: [the provider link](#the-provider-link), the
[directory profile](#the-directory-profile) it writes and [the pass](#the-pass) it
issues.

**The flag** is three columns of `staff.people` (00042's): `provision_account`,
`provision_from` and `account_email_kind`, with its own revision,
`account_revision` (00051's). **No account write — HR's or the accounts worker's —
moves the person's `revision`**, which a manager, an Approver and a colleague read: a
flag turned on or off is no signal to anyone outside the links group, as a sealed
field's write is none. The one exception is a write of the person, not of the account:
the work email a provider made for a person who had none
([the provider link](#the-provider-link)), which moves the person's `revision` and
`updated_at` as any change of the work email does. It is the links group's, as the
self-service link is: the person, `staff:manage` and `staff:personal-data` read it; a
manager, an Approver, a colleague and anyone else get the person's 404.

- **`GET /people/{id}/account`** (`staff:access`, then the person themselves,
  `staff:manage` or `staff:personal-data`; otherwise the unknown id's 404) answers
  `{provisionAccount, provisionFrom, provisionFromEffective, accountEmailKind,
  address: {kind, present}, accountKind, link, providerLink, directoryProfile,
  groups, revision}` — `revision` the flag's own; `groups` is the person's
  [groups](#the-persons-groups).
  - `accountEmailKind` is `work` or `private`; a null column reads `work`.
    `address` says which of the person's addresses the account would be given and
    whether the person has it — never the address itself.
  - **The effective date** `provisionFromEffective` is the date the accounts worker
    acts on — the decision table's own `F` (`accountFrom`, below), one rule for both:
    `provisionFrom` when HR set one, else the first day of the person's earliest
    employment that has not ended by today (an employment ending today still counts);
    between employments — one ended, none current, a later one to come — the later
    start, unless the stored date falls after the ended employment's end (an early date
    for the later one, honoured); else absent.
  - `accountKind` is the person's [account kind](#the-account-kind) — `local`,
    `external` or `entra` — always present, read from Staff's own row. It carries the
    rule the installation's authority mode did: the previous `mode` field is gone.
  - `link` is the person's row in `staff.identity_links`, absent when there is none:
    `state`, `managed`, `reason` (`blocked_reason`, a code), `attempts`,
    `nextAttemptAt`, `lastSyncedAt`, `user` (named by the user directory) and, for a
    pending account whose link still names the user identity was asked about,
    `invitation {state, issued}` — `none`, `active`, `expired`, `revoked_by_owner` or
    `exhausted`, and the invitations delivered in the pending spell. identity is asked
    with no address to compare; the codes never carry an address or a driver's words.
  - `providerLink` is the person's [provider link](#the-provider-link), absent when
    there is none: `provider`, `state`, `reason`, `reportedCodes`, `externalUpn`,
    `hasPass`, `passIssuedAt`, `announcedAt`, `lastSyncedAt`, `nextAttemptAt` and
    `attempts` — read from Staff's row, the provider never asked.
  - `directoryProfile` is the [directory profile](#the-directory-profile) — every
    field the provider writes, in order, `{key, on, switchable, value?}` with the value
    it would carry today — present when the person's kind names a provider.
- **`PUT /people/{id}/account`** (`staff:manage`) replaces the three fields,
  `If-Match` on the flag's own revision (the `revision` `GET` answered, never the
  person's), under the person's lock. The body must name `provisionAccount` with a value —
  a body without it, or with it `null`, is a 400 on the field, since the decoder would
  read either as false; what it leaves out is
  cleared — `provisionFrom` to none, `accountEmailKind` to `work`. An erased person is
  `person_erased` (409), a stale revision `revision_conflict`; an unknown kind or an
  unknown property is a 400. A change moves the flag's revision — not the person's —
  and is one
  `account_flag_updated` event naming each changed field, before and after; a replace
  that changes nothing writes nothing. Turning the flag on without the chosen address is
  accepted — the worker waits, and HR is told. It answers the account as `GET` does.
  `accountKind` is **the one field the replace keeps when it is left out**; a change of
  it is judged as [the account kind](#the-account-kind) says — `provider_missing` (400
  on `accountKind`), `account_kind_locked` (409) and, while the kinds are not yet
  seeded, 503 `accounts_unavailable` — and named in the same event. A
  change of the kind alone writes the kind alone; a change of the kind and of the flag's
  own fields in one replace moves the flag's revision by two, one for each write.

**The address identity compares.** Whenever Staff asks identity whether its address
for a user differs from Staff's (`AccountRef.EmailDiffers`), it hands identity the
person's own stored address by their `accountEmailKind` — the accessor takes the
person, never an address a request carries. HR writes that address, though, so
`staff:manage` can still make one guess per run at identity's address for a user —
change the person's address, run, and read whether `email_differs` appears — each
guess a change recorded in the person's history (`person_updated`). This is
accepted (privacy review L1): HR's residual reach over Staff-managed users is the
owner's accepted default, and identity's address is never returned.

**The link's states.** `linked` is HR's self-service link; `preparing` (a link naming
no user yet — `external_id` is nullable since 00051), `pending`, `active`,
`disabled`, `failed` and `blocked` are the worker's. A `preparing` link does not make
a person "in use": [deleting](#deleting-a-person) the person deletes it with them, but
not while the worker holds it (`lease_until` in the future): `accounts_busy` (409).
The delete locks the link last, after the person and the manager graph, so it waits
for a worker's claim and judges the row the claim left: a lease set is
`accounts_busy`, a user named meanwhile `person_in_use`.

**The decision table** (`accounts_decide.go`) is what the accounts worker does next
for one person: a pure function of facts already read — the person (erased, the flag,
the stored `provisionFrom`, whether the chosen address is present, the employee
number, the employments, the [account kind](#the-account-kind)), the link, what
identity reports of the link's user (`AccountRef`), `createBeforeDays` and the civil
day in the business time zone — that reads no database or clock and calls nothing. It answers one
step: nothing, prepare, activate (with a fresh invitation on *Run now*), deactivate,
update, or a report written as the link's `blocked_reason`. With `T` today and `F` the
effective date — the stored `provisionFrom`, else the start of the earliest employment
not ended by today; between employments the later start, unless the stored date falls
after the earlier employment's end — the first that holds wins:

1. An erased person: nothing (a legal hold does not stop the table). The flag off:
   nothing is created, enabled or updated, and a link is left as it is — but **leaving
   disables anyway** (the owner's decision of 2026-10-18): once every employment has
   ended, none later (item 4's phase), a Staff-managed user on the worker's link — not
   HR's self-service link (`linked`) — who is not an Owner or a SystemAdmin, carries the
   person's employee number (or the one last synced) and is still enabled is
   deactivated, the day after the last end; anything else is nothing, with no report.
2. No employment at all: `no_employment`.
3. A link naming a user: a user identity no longer knows is `user_gone` (the link
   `failed`; never re-created) on the worker's link and nothing on HR's self-service
   link; a user Staff does not manage is left alone on HR's self-service link, which
   stays `linked` — but a user an Owner has since adopted is managed, so the worker acts
   on them through that link as through its own (plan reading 8, [the self-service link](#the-self-service-link));
   an Owner or a SystemAdmin (`AccountRef.Privileged`) is `account_privileged` (the
   link `blocked`); on the worker's link a user Staff no longer manages (an Owner
   unmanaged them) is `account_not_managed` and the link keeps its state; a user whose employee number
   is neither the person's nor the one the worker last wrote to identity for the link
   is `mismatch` (the link `blocked`) and nothing else, not even at the end. A user
   holding the number last written, after HR renumbered the person, is still theirs
   and is updated.
4. Every employment ended (the day after the last end): the user is deactivated
   whenever the person has left, whatever the directory says — a user the SCIM
   directory deactivated on every run, since `Deactivate` disables identity's own
   record whatever the directory says and, called again, changes nothing; the address
   need not be present. Otherwise nothing, and no report. Between employments, before
   the later one is due: the same, and before the later one's window nothing else
   either.
5. A link-only kind and a pending user without a credential (one made while the person
   was `local`): under `external`, `mode_changed` (the link kept `pending`) and nothing
   else; under `entra`, nothing — the provider column's announcement names that user
   as its `ReplaceUserID`, and the account's arrival completes them
   ([the provider link](#the-provider-link)).
6. `T < F − createBeforeDays` with no user yet: nothing. The address missing:
   `account_email_missing` — never for `entra`, whose provider makes the address. No
   user yet: prepare (for a link-only kind, find by the employee number).
7. The user's state — the same before `T ≥ F` (outside the gap between employments,
   where item 4 has answered) except that nothing is activated early:
   a user the directory deactivated, nothing; a pending user under a link-only kind,
   nothing, whoever disabled them; disabled by an Owner (or by no
   one recorded), `blocked_by_owner` while pending and `disabled_by_owner` otherwise
   (the link `blocked`); disabled by Staff, activate — for a link-only kind a
   re-enable only; a pending user's invitation:
   active, nothing; revoked by an Owner, `invitation_revoked`, and three unanswered,
   `invitation_unanswered`, unless HR runs the person now; otherwise activate. Under
   `entra`, an active user the link has not yet recorded active — the adopted user,
   who arrives enabled — is activated once, so identity gives them the provisioning
   roles.
8. The details: a pending user whose address in identity differs while it still follows
   the person's — no invitation naming the user delivered yet
   (`AccountRef.EmailFollows`; the owner's decision of 2026-10-18) — update (the
   `local` kind); any other user whose address differs — pending once the first
   invitation went out, or past pending — `email_differs` for an Owner to settle in
   identity (the `local` kind — under a link-only kind the provider owns the address),
   with an update when the person's other details changed; the details changed, or
   the person renumbered, update — before the window too.

**The settings** `createBeforeDays`, `deleteExternalAfterDays`, `defaultAccountKind`
and `directoryFields` are under [Settings](#settings). **Meta** answers `staff:manage`
`defaultAccountKind` — the settings' value, absent when no module provides accounts —
`providers` — every provider in the slot, `{key, name, status, reason?}`, its status
asked now (`Provider`, on no connection, before any snapshot), a provider that could
not be asked answered `failing` with the reason `unreachable` (logged), and a reason
outside a provider code's form left out (logged by field name); empty when no
enabled module offers one — and `provisioningRolesSet` — whether an Owner has named the
roles Staff may give in identity, without which a local account cannot be enabled,
absent when no module provides accounts or identity could not answer. The previous
`accountMode` is gone. Both exports carry `account`
(`provisionAccount`, `provisionFrom`, `accountEmailKind` and the link's `linkState`).

**The account events** are class C and listed only to the links group's readers — the
person, `staff:manage`, `staff:personal-data` — and never to a manager, an Approver or
a colleague, whose history and its count leave them out.
`account_flag_updated {fields: {provisionAccount, provisionFrom, accountEmailKind}}`
is HR's change of the flag; the worker's `account_prepared`, `account_activated`,
`account_deactivated`, `account_updated`, `account_blocked`, `account_failed`,
`account_mismatched` and the retention run's `identity_erased` carry `{step, state, code?, invitation?: {state, issued},
flagOffMeanwhile?}` — the step, the link's state after it, a code, a pending user's
invitation after an activation, and whether the flag was turned off while the step ran
— and nothing else: no address, no name, no user id beyond the link's own. They name
no field, so `payload_classes` is `{C}`, written as such; their payload is read whole
by the links group and empty to anyone else. The provider link's events are the same
class and readers: `account_announced` (the external account announced to
identity), `account_external_prepared`, `account_external_activated`,
`account_external_deactivated`, `account_external_updated`,
`account_external_deleted`, `account_external_blocked` and `account_external_failed`
(the first call of a run of failures, its code `accounts_unavailable`) — codes only,
never the principal name, the address or the provider's words. Each also carries
`codes`, the provider link's reported codes after the step, answered to the links group
when there are any. `pass_revealed` is one of them too: the viewer is its actor, and its
payload names the `provider` — answered to the links group alone — and never the pass.
`work_email_set_by_provider`, when the address the provider made is written to a person
who had none, is **not** among the account events: it is a change of the person, listed
to every reader of their history, its payload `{step: "provider", state}` read by the
links group alone and empty to anyone else — no address.
`account_flag_updated` names `accountKind` among its fields when HR changed it.

### The account kind

**Who holds a person's account** is the person's own setting,
`staff.people.account_kind` (Entra design D5), in place of the installation-wide
authority mode — `contracts.AccountKindLocal`, `AccountKindExternal`,
`AccountKindEntra`:

| Kind | The identity link (`provider = 'identity'`) | The provider link |
| --- | --- | --- |
| `local` | identity holds the credentials: `Prepare` creates, `Activate` enables, invites and gives the provisioning roles, `Deactivate`, `Update` | none |
| `external` | an identity provider owns the account: `Prepare` finds only, `Activate` re-enables what Staff disabled, `Deactivate`, `Update` of the language and the number; `accountNotFound` until an Owner adopts the user | none |
| `entra` | link-only as `external`, but the account is announced (`Expect`) after the provider's `Prepare`, so the user that arrives is adopted with no Owner; `Activate` gives the provisioning roles once, on the first activation of the adopted user | [the provider link](#the-provider-link), the provider keyed `entra` |

**The kind travels on the call.** Every `Prepare`, `Activate` and `Update` the worker
makes carries the person's kind (`AccountSpec.Kind`, `ActivateOpts.Kind`), so identity
creates, invites and gives roles by the person, never by the mode alone: a `local`
person in an installation whose single sign-on is on is still created and invited
([what Staff's provisioning does](/en/admin/authentication/#what-staffs-provisioning-does-in-identity)).

**A new person takes the default**, `settings.default_account_kind` — `POST /people`
and the import alike, read from the settings row as the person is inserted. **HR
changes a person's kind** on the Account tab, `accountKind` on
`PUT /people/{id}/account`, which moves the flag's revision and is recorded in
`account_flag_updated`:

- a provider's key — `entra` — is `provider_missing`, a field error on `accountKind`,
  unless an enabled module offers that provider (`Deps.IdentityProvisioners` holds its
  `Key()`); the same for `defaultAccountKind` on `PUT /settings`;
- a change **away from a provider's kind** (`entra`) is `account_kind_locked` (409)
  while the person's link under that provider names an account (`external_id` set) in
  any state but `deleted`, unless it is blocked `user_gone` — a `Prepare`'s answer is
  all it takes, so a link `blocked` or `failed` on an account the directory holds locks
  too: a person leaves the directory by *Disable now* or by the end of their employment,
  never by a change of kind. A link naming no account yet, one `deleted` or reported
  gone, or none, locks nothing. When such a change goes through and the link recorded an
  announcement while the identity link names no user yet, the announcement is withdrawn
  after the commit (`Unexpect`, the caller the actor, on no connection) and the link's
  `announced_at` cleared; a withdrawal that fails is logged, and the worker's deactivate
  of the link withdraws it too. A link the person keeps under a provider their kind no
  longer names is still worked, for a deactivate only ([the provider
  link](#the-provider-link));
- a change **from `local` to `entra`** while the person's user is Staff-managed,
  pending and holds no credential is carried by the next announcement as
  `ReplaceUserID`: the account's arrival completes that user rather than creating a
  second.

**The seed, once.** The migration writes `local` everywhere, since a migration may not
read identity's schema. While `settings.account_kind_seeded` is false, and a module
provides accounts, Staff seeds the kinds itself — at an API process's mount (on a
background context, bounded at thirty seconds) and at the start of every accounts
cycle, the worker's or *Run accounts now*'s: it asks identity's authority mode on no
connection (unavailable, or a mode mismatch in this process: skipped, logged as a
warning, tried the next time), then, under the settings row's lock, sets every
person's `account_kind` and `default_account_kind` from it — `local` from `local`,
`external` from `federated` — and `account_kind_seeded` true, and logs `staff: the
account kinds were seeded from the authority mode` with the `kind`. The settings'
revision and `updated_at` move only when the default changes (to `external`), so a
settings form read before a seed that left `local` as it was is no conflict. It never
runs again: a later change of the single sign-on settings changes no one's kind.

**Nothing is judged on a kind before the seed.** Until it has run, every person's
column reads `local`, its default, which is not the rule a federated installation had.
So while the kinds are unseeded — the seed tried and failed: no module provides
accounts, identity could not say its mode, or the write failed — the seed is tried
again by every request that would read or change a kind, and meanwhile:

- an accounts cycle runs no step but a deactivate, on both links: nothing is prepared,
  activated, updated or reported, no preparing link is written, and a link held keeps
  its state and reason, records no event and is claimed again at the next poll — logged
  once per cycle, `staff: the account kinds are not yet seeded; only deactivations run`;
- `POST /people/{id}/account/run` is 503 `accounts_unavailable`, its detail saying the
  kinds are not yet seeded, and nothing runs;
- `PUT /people/{id}/account` changing `accountKind`, and `PUT /settings` changing
  `defaultAccountKind`, are the same 503 and change nothing. Either sends the field to
  try the seed first; sending back the current value is no change, and saves.

The seed is tried at an API process's mount, at the start of every cycle, by
`POST /people/{id}/account/run`, and by either `PUT` that names the field.

### The accounts worker

`staff-accounts` (`accounts_worker.go`) runs every `STAFF_WORKERS_POLL` (default an
hour) unless `STAFF_ACCOUNTS_ENABLED=0`, as the system actor
`system:staff-accounts` in identity's audit and the installation in the history. With
no module providing accounts it does nothing. **It holds no cycle-wide lock and no
lease connection** (pre-flight B2): a cycle shares its work with any other — a second
worker process, a run beside the ticker — through the link rows alone.

1. **The seed, the mode and the providers.** While the kinds are unseeded, the cycle
   first [seeds them](#the-account-kind); a seed that fails holds every step but a
   deactivate for the whole cycle ([nothing is judged on a kind before the
   seed](#the-account-kind)). Then the mode is asked once, on no connection. Identity
   unable to answer fails the cycle, which the next poll runs again — unless the kinds
   are unseeded, when the deactivations still run. `ErrModeMismatch` (this process's OIDC or SCIM settings derive another mode
   than the API process recorded) holds every step but a deactivation: see below. Then
   each provider in the slot is asked its status once (`Provider`, on no connection,
   under the call timeout) — a provider that could not be asked is `failing`,
   `unreachable`, logged; a reason outside the form of a provider code (below) is
   dropped and logged by field name — and each provider kind held by a person whose flag is on and
   who is not erased, but offered by no enabled module, is `unconfigured`,
   `provider_missing`. Each is recorded on `settings.provider_status` (`{status,
   reason, checkedAt}` under the provider's key) when its status or reason changed,
   without moving the settings' revision.
2. **The preparing links.** For each person whose flag is on, who is not erased and has
   no link, the decision table is asked with no link; when it answers *prepare* the
   cycle takes the person's lock, reads the flag again and writes a `preparing` link —
   no user, managed — `INSERT … ON CONFLICT (person_id, provider) DO NOTHING`, so two
   cycles write one.
3. **The claims.** Up to fifty due links — `next_attempt_at` reached or never set, no
   lease or a lapsed one, of a person whose flag is on and who is not erased, or carrying
   a disable *Disable now* asked for ([below](#run-now-and-disable-now)), or — the flag
   off, not erased — a leaver's: the worker's link to a Staff-managed user, every
   employment ended before today in the settings zone and none later (claimed daily
   for as long as that holds, so a leaver an Owner re-enabled is disabled again by the
   next run, reading 32) — each
   claimed alone by a conditional `UPDATE … FOR UPDATE OF l SKIP LOCKED` that sets
   `lease_id` and `lease_until` (five minutes on) and commits at once. A link another
   cycle holds is skipped; a link another transaction has locked at that instant (HR's
   delete or relink of the person) is skipped too, which may end the cycle early — the
   next poll claims it. A `blocked` or `failed` link is claimed on its schedule like
   any other: the table never reads `blocked` as an input, so a link recovers on the
   next due run once what blocked it is gone — a user demoted and adopted again, the
   employee roles named.
4. **Per claim**: the person's facts — the flag, the addresses, the employee number,
   the name, the language, the employments, the settings' zone and `createBeforeDays` —
   in one read-only snapshot, closed; the link's user as identity reports them
   (`Account`, with the person's own chosen address); the table; then each step
   **called under a two-minute timeout with no transaction open and no connection
   held**. **The lease is renewed before each call, and before each read of the user
   after a call** — the first read rides on the claim's fresh lease — (`lease_until`
   five minutes on, conditionally on the claim still holding a live lease, with no
   connection kept), so a chain longer than one lease keeps its link; a renewal that
   finds the lease lapsed or taken stops the chain. **A user `Prepare`
   answers is recorded on the link at once**, in a short write under the person's lock
   on the claim's lease, before anything more is done with them: a user another
   person's link already names (`ux_identity_links_provider_external`) is `mismatch`,
   `blocked`, and the chain stops — nothing is activated or updated for someone else's
   user; recorded once (`account_mismatched`), not again each day the unchanged link is
   claimed. The same write reads the person again under the lock: a person erased, or
   whose flag was turned off, while `Prepare` ran has the user recorded and nothing more
   done with them. After each successful call the user is read again and the table asked again
   on the fresh answer, until it answers nothing or a report, or after **three calls**,
   or when a call left the user as they were (pre-flight I2: prepared and enabled on
   day one with `createBeforeDays` 0). After a successful `Prepare` or `Update` the
   employee number written is the link's `synced_employee_number`; after an `Update` the
   person's details hash is the link's `last_spec_hash` — SHA-256 over the address
   (only while it follows the person's, `AccountRef.EmailFollows`), the display name, the language and the employee
   number — so the next run asks for no second update.
   For a person of a provider's kind, **the provider column runs first**, under the
   same claim and lease ([the provider link](#the-provider-link)): at most three
   provider calls, then the identity chain's three. It runs even when the read of the
   link's user failed, so a leaver's external account is not left enabled because
   identity could not answer.
5. **The result**, under the person's lock and then the link `WHERE lease_id` is the
   claim's (the lock order: the person, then the link): `state`, `managed`,
   `external_id` (after `Prepare`), `blocked_reason`, `last_error`, `attempts`,
   `next_attempt_at`, `last_synced_at`, `last_spec_hash`, `synced_employee_number`, the
   events, the lease cleared. The flag is read again: one turned off meanwhile is
   recorded on the events (`flagOffMeanwhile`) and the next cycle claims the link no
   more (plan M9). **No worker write moves the person's `revision` or `updated_at`**,
   and the worker writes only `account_*` events — never `user_linked` or
   `user_unlinked`. The write names the user the claim knew
   (`external_id IS NOT DISTINCT FROM` it): a link claimed since by another cycle, or
   relinked by HR meanwhile — `PUT /people/{id}/user` ends a claim on the link it
   replaces — is not written, and the log says which. A link gone — the person deleted
   after the lease lapsed — has the user this claim's `Prepare` **created**
   (`AccountRef.Created`) deactivated, outside any transaction, and logged
   (pre-flight I4); a user `Prepare` only found, one an Owner adopted, is left as it is.
   `account_deactivated` is recorded when the link's state moved or the user changed:
   a leaver the SCIM directory keeps inactive is deactivated on every run, recorded
   once.

**The groups.** The same cycle [materialises the derived groups](#members-and-owners)
after the providers' statuses and before the preparing links, and, after the claims,
[syncs the groups](#the-sync) with each provider — up to 100 sections a cycle.

**The schedule.** A claim that ended — done, or answered with a refusal or a report —
is acted on again at **the next civil midnight** in the settings zone: once per civil
day, a 23- or 25-hour day of a daylight-saving change included. A call that could not
be made (identity unreachable, a timeout, an answer that is not one of the contract's)
is `last_error = accounts_unavailable`, `attempts` counted, retried at the next poll
while `attempts < 3`, then daily — **except a deactivation, retried at every poll for
as long as it fails**, so a leaver loses access as soon as identity answers; any answer
resets `attempts` and clears `last_error`. A failed read of the link's user is retried
at every poll too when the person's facts alone say a deactivation may be due — every
employment ended, or between employments before the later one is due — and the link is
not already `disabled`. A claim that spent its three calls with more
to do, a step held by the mode mismatch or by a provider that is not connected, or a
call whose follow-up read failed is claimed again at the next poll. The schedule is
the identity link's alone: the provider link is never claimed itself, so it is worked
whenever the person's identity link is.

**What a call's refusal records**, as the link's `blocked_reason` (codes only, never an
address or a driver's words):

| Refusal | `blocked_reason` | The link |
| --- | --- | --- |
| `ErrAccountPrivileged`, `ErrLastOwner` | `account_privileged` | `blocked` |
| `ErrBlockedByOwner` | `blocked_by_owner` | `blocked` |
| `ErrAccountNotFound` (the `external` kind: the identity provider has not created the user yet, or no Owner has adopted them) | `not_found` | `blocked` |
| `ErrAccountNotFound` on `Prepare` for a provider's kind (`entra`: the announced account has not arrived yet) | none recorded | its state kept; claimed again at the next poll |
| `ErrUserGone` | `user_gone` | `failed` |
| `ErrAccountNotManaged` | `account_not_managed` | its state kept |
| `ErrEmployeeRoleMissing` | `employee_role_missing` | `blocked` |
| `ErrEmployeeRoleUnsafe` | `employee_role_unsafe` | `blocked` |
| `ErrAccountEmailTaken` | `account_email_taken` | `blocked` |
| `ErrEmployeeNumberTaken` | `employee_number_taken` | `blocked` |
| `ErrModeMismatch` | `mode_mismatch` | its state kept |
| `ErrInvalidActor` (a bug of Staff's, logged) | `invalid_actor` | `failed` |

The table's own reports are written the same way, with the state it names. A user
`Prepare` answered that another person's link already names is `mismatch`, `blocked`.

**The mode mismatch.** Under `ErrModeMismatch` the cycle still runs every due
deactivation — `Deactivate` reads no mode, so a leaver loses access whatever the
configuration says — and holds every other step before any identity call: the links it
holds record `blocked_reason = mode_mismatch`, their state kept, and are claimed again
at the next poll, so the installation's item is derived from link rows (reading 21).
Identity no longer answers it, since every process derives the mode from the same
single sign-on row ([the authority mode](/en/admin/authentication/#the-authority-mode));
it stays one release, for a worker still on the binary before.

**A known remaining risk.** HR may relink a self-service link to a user an Owner has
adopted for Staff in the instant between a claim's lease renewal and its call: one call
of that claim can then reach the previous user. Every write the claim makes afterwards
is still refused — the result names the user the claim knew — and the next claim works
the new link.

**Calls out.** Every call to identity's `contracts.AccountProvisioning` goes through
one accessor in `contractscalls.go` and is refused inside any of the module's
transactions; with no module providing it (`Deps.Accounts` nil) each answers
"unavailable", which a read leaves out and a write answers `accounts_unavailable`
(503).

### Run now and Disable now

Three operations run the worker's code by hand (`accounts_run.go`), `staff:manage`'s,
the caller the actor identity audits (`contracts.Actor{UserID}`) and the account events
name. Each keeps the worker's disciplines: no cycle-wide lock — a link is claimed alone,
by a lease, in a statement that commits at once, so a run and the worker's ticker share
the work — every identity call on no connection under the two-minute timeout, the
result written under the person's lock on the claim's lease and only onto the user the
claim knew. Each runs on the request's values but not its cancellation
(`context.WithoutCancel`, review M3): a client or a proxy that gives up does not cut a
call in flight, record a false failure or leave a live lease — the work finishes, which
when identity or the mail server is slow can take minutes. A claim that writes no
result (its facts unreadable, or naming no user) releases its lease at once
(`ReleaseAccountLease`, review L3). **The rate limits** (review L2), per client address
as every platform policy: `POST /accounts/run` at most 30 an hour (`StaffAccountsRun`),
and a person's `/account/run` and `/account/disable` together at most 120 an hour
(`StaffAccountAction`, one counter for both); 429 past either.

- **`POST /accounts/run`** runs one cycle: the preparing links of the people now due,
  then the due links claimed one at a time, at most fifty, **none begun after sixty
  seconds** on the module's clock (a claim begun finishes under its calls' own bounds).
  The budget bounds the preparing links too: once it is spent no more is written, and
  the people left are prepared by the worker's next cycle.
  It answers `StaffAccountsRun {attempted, done, failed, remaining}`: the links claimed;
  those whose step identity answered — carried out, or refused with the reason the link
  now records; those whose call could not be made (`last_error`, retried by the
  worker); and the due links left, counted after the run whether or not another cycle
  holds them (`CountDueAccountLinks`). No provider, or a mode identity cannot say, is
  503 `accounts_unavailable` and nothing runs. A mode mismatch runs the deactivations
  alone, as the worker does.
- **`POST /people/{id}/account/run`** works one person. No module providing accounts
  is 503 `accounts_unavailable`, and so are account kinds that are not yet seeded (the
  seed is tried first; the detail says so) — nothing runs. Then, when their flag is on,
  the preparing link written if the table says they are due and they have none, then their
  link claimed **whatever its `next_attempt_at`** (`ClaimPersonAccountLink`, which waits
  out another cycle's claim statement rather than skipping it), and worked with
  `Fresh`: identity's `ActivateOpts.FreshInvitation` on the **first** `decide` of the
  claim only. For a person of a provider's kind, the provider's `SpecFlags.Fresh` — a
  new pass (*Run now (new pass)*) — reaches the provider **only on the `Activate` that
  enables a disabled account**: a joiner on their first day, a rehire, perhaps after the
  announcement in the same run. An account already enabled is never activated again by
  Run now, so `staff:manage` cannot mint a Temporary Access Pass over a working
  account; it is updated when its spec changed, else left as it is. A flag that is off
  runs nothing. For a person of a provider's kind
  the provider's status is asked first, as a cycle asks it. A provider's refusal is 409
  `provider_refused`, its code in the detail and on the provider link as its reason —
  but not `provider_unavailable`, nor a refusal of the announcement, which identity
  answers and the link records alone. It answers
  `StaffPersonAccountRun {invitationSent, account}`: `invitationSent` is whether the
  pending user's invitation became one that can be accepted during the run (none open
  before, one open after). Identity never re-sends an open invitation and, within ten
  minutes of the last invitation naming the user, treats a fresh one as an ordinary
  `Activate`, which sends nothing for a revoked or exhausted invitation: the run is
  **done** (200, `invitationSent: false`), the link keeping `invitation_revoked`. An
  erased person is 409 `person_erased`; a link with a live lease 409 `accounts_busy`;
  a call that could not be made — identity's or the provider's — 503
  `accounts_unavailable`, recorded on its link.
- **`POST /people/{id}/account/disable`** — *Disable now*, the owner's answer 2. Under
  the person's lock, the link locked last: a live lease is 409 `accounts_busy` and
  nothing is written; otherwise the flag is written off — `account_flag_updated`
  `{fields: {provisionAccount: {from: true, to: false}}}`, `account_revision` moved, the
  person's `revision` and `updated_at` not — and, when the link names a user,
  **`identity_links.disable_requested_at`** set (the first request's instant kept,
  review M1), and committed. Then the link is claimed
  (`ClaimPersonAccountLink`, the flag no longer on), the lease renewed, `Deactivate`
  called for its user with the caller as the actor, and the result written as the
  worker writes one (`account_deactivated`, the link `disabled`). **For a person of a
  provider's kind whose provider link names an account not `deleted`, both are
  disabled, the provider's first** (Entra design D7): `disable_requested_at` is set on
  the identity link even when it names no user yet; after the commit, under the
  identity link's claim, the provider's status is asked, then the provider's
  `Deactivate` is called with the caller as the actor for an account that is
  `enabled` (the provider link `disabled`, `account_external_deactivated`; an account
  disabled already is left as it is), then identity's as above. Identity's refusal or
  failure is answered first; otherwise a provider's refusal is 409 `provider_refused`,
  its code in the detail and on the provider link, and a provider call that could not
  be made 503 `accounts_unavailable` — the flag already off either way. A provider that
  The provider link is the person's under their kind, or one they still have under a
  provider their kind no longer names. **A disable the directory did not confirm is
  kept**: a provider `Deactivate` that failed — any error, `ErrProviderUnavailable`
  included — or was refused, a failed read of the account, a lease lost before the
  call, or no enabled module offering the provider while the link names an account not
  yet `disabled` or `deleted` keeps `disable_requested_at`, whatever identity answered,
  marks the claim failed and brings the link back at the next poll, where the worker
  finishes the disable. *Disable now* then answers — after identity's own refusal or
  failure, which is answered first — 409 `provider_refused` for a refusal, else 503
  `accounts_unavailable` whose detail says the external directory could not be reached
  and the accounts worker finishes the disable; the flag is off either way. With the
  flag off no
  cycle claims the link again — once the disable is done — so the user stays disabled
  until HR turns the flag on. **The disable asked for is the worker's to finish**:
  `disable_requested_at` is cleared only when the user is disabled — a `Deactivate`
  carried out, or identity's own record found disabled — or gone (`ErrUserGone`). A
  refusal (`account_not_managed`, `account_privileged`, the last Owner) keeps it, as the
  link's `blocked_reason` too, and is tried again on the daily schedule, so a user an
  Owner adopts again or demotes is disabled by the next try (re-check MEDIUM-1, final);
  a failure keeps it, retried at every poll, as a leaver's; `invalid_actor` keeps it.
  `ClaimAccountLink` also claims a link carrying it whatever the flag and the erasure,
  and the step (`finishRequestedDisable`, *Disable now*'s and the worker's), outside
  the decision table (whose "flag off: nothing" it overrides, a documented branch),
  reads the user first: gone, or disabled in identity's own record already (an Owner's
  disable meanwhile — a directory's deactivation is not that, and is still disabled in
  identity's record), clears the request with no call; a user carrying another employee
  number than the person's or the one last synced (`SameEmployeeNumber`, as `decide`
  judges) is someone else's account — nothing is called, the link is `blocked` `mismatch`
  (`account_mismatched`), *Disable now* answers 409 `account_mismatch`, and the request
  is kept, so the disable runs once HR has put the number right; otherwise it
  deactivates them. HR's turning the
  flag on again lets the request lapse — not for an erased person, whose disable always
  runs; `SetPersonLink` and `DeletePersonLink` clear it with the link, which is safe:
  both refuse a worker's or a Staff-managed link (`link_managed`) before they run, so
  the only marked link they replace is a self-service link to a user Staff does not
  manage. So a Disable now cut short — the client gone mid-call, identity down,
  the link claimed between the commit and the claim — is finished by the worker.
  A person with no link, or a link naming no user yet and no external account to
  disable, has the flag turned off alone (200). **Disable now acts only on accounts
  Staff manages**: HR's own self-service
  link (`linked`, not `managed` — a link the worker never acts on) has the flag turned
  off and nothing more — no `disable_requested_at`, no call — and answers 409
  `account_not_managed` (the Task 6 review); HR unlinks it, or an Owner disables the user
  in identity, and an Owner's adopting the user later disables nobody. An erased person
  may be disabled. Otherwise `ErrAccountNotManaged` is 409
  `account_not_managed` and `ErrAccountPrivileged` or `ErrLastOwner` 409
  `account_privileged` — the flag off, the reason on the link; `ErrUserGone` is done
  (200, the link `failed: user_gone`). A call that could not be made is **503
  `accounts_unavailable` with the flag already off**: the link's `last_error`
  `accounts_unavailable`, `attempts` counted, `disable_requested_at` kept, and
  `accountFailing` raised while the worker retries. A link claimed by a cycle in the
  instant between the commit and the claim is 409 `accounts_busy` with the flag off;
  the cycle holding it, or the next, deactivates the user from the marker.

### The provider link

A person of a provider's [kind](#the-account-kind) (`entra`) has a second row in
`staff.identity_links`, **the provider link** — and keeps it if HR later changes the
kind: `provider` the provider's key, beside the
identity link's `identity` (Entra design D7). It reuses the row's worker columns —
`external_id` (the directory's immutable id of the account), `state`,
`blocked_reason`, `last_spec_hash`, `attempts`, `next_attempt_at`, `last_synced_at` —
and has five of its own (00060's): `external_upn` and `external_email`, the principal
name and the address the provider answered (class D, as the work email); `announced_at`,
when Staff announced the account to identity; `pass_issued_at`, when the provider issued
a pass nobody has revealed yet — never the pass; and `reported_codes`, the facts the
provider reported and could not act on, codes only. 00071 adds a sixth,
`photo_pushed_stamp`, the stamp of the picture last pushed
([the directory's photo](#the-directorys-photo)). Its states are `preparing` (the
worker asked the provider to create or find the account, none named yet), `enabled`,
`disabled`, `deleted` (soft-deleted in the directory), `failed` and `blocked`.

**The identity link stays the claim.** The worker claims a person's identity link as
before — `ClaimAccountLink` now also claims, daily, the identity link of a leaver whose
provider link is not `deleted`, even one naming no user or of an erased person (not
under a legal hold, once `deleteExternalAfterDays` have passed since the last
employment ended), for the deactivation and the deletion below; an erased person's
managed identity link is kept keyless (`external_id` null, `disabled`) after
[the retention run erases their user](#the-worker) for as long as a non-`deleted`
provider link remains, and deleted once the provider link is `deleted` — and works both links under that one claim and its
lease: the provider link's result (`WriteProviderLinkResult`) is written only while the
identity link holds the claim's lease. For a person of the `entra` kind a cycle reads,
beside the person's facts, the provider link, the settings' `deleteExternalAfterDays`,
the person's legal hold, the [directory profile](#the-directory-profile) and the
manager's own provider link; the external account (`Account`, on no connection, by the
link's `external_id` — none answered is the account gone); and the provider's status,
once per cycle. **The provider column runs first, identity second**, so the
announcement precedes the account's arrival: at most three provider calls — the
announce among them — then the identity chain's three, each under the two-minute
timeout with no transaction open and no connection held, the lease renewed before each,
as [the accounts worker](#the-accounts-worker) does.

**The third column** (`accounts_decide_provider.go`, `decideProvider`) is a pure
function as `decide` is. With `T` today, `F` the effective date, `E` the last end and
`P` the provider link, the first row that holds wins — but **a call this claim's chain
has made already is not made again** (`providerDecision.Made`): what it left undone, a
leaver's step that failed again or an adoption nobody confirmed, waits for the link's
next claim rather than spending the chain's calls. An account *with its leaver's steps
undone* is a `disabled` one whose reported codes hold `leaver_step_failed:<step>`:

| # | When | The provider link `P` |
| --- | --- | --- |
| 1 | HR's *Disable now* (or the worker finishing it) | `Deactivate` an account that is `enabled`, or `disabled` with its leaver's steps undone; otherwise nothing |
| 2 | The person erased | once every employment has ended (`T > E`, none later), `deleteExternalAfterDays` have passed since `E`, with no legal hold and `P` `disabled`, `Delete` → `P` `deleted`; otherwise nothing |
| 3 | `P` names an account `Account` answers none for | `user_gone`, `P` `deleted` when the person is erased, else `failed` — never created again |
| 4 | No employment at all | `no_employment` with the flag on and a `P`; otherwise nothing |
| 5 | Every employment ended (`T > E`, none later), the flag on or off — or, the flag on, between employments before the later one is due | `Deactivate` an `enabled` account, whatever the provider's status; for a leaver, once `deleteExternalAfterDays` have passed since `E`, with no legal hold and the account `disabled`, `Delete` → `P` `deleted`; else a `disabled` account with its leaver's steps undone is deactivated again, once a day, the provider finishing the steps left and the code clearing when they succeed; otherwise nothing |
| 6 | The flag off | nothing |
| 7 | The provider not `connected` (`unconfigured` or `failing`) | `provider_unavailable` on a `P` there is, its state kept; with no `P`, nothing |
| 8 | `T < F − createBeforeDays` | nothing |
| 9 | No account — no `P`, a `P` `deleted`, or one no `Prepare` has answered | `Prepare` (the provider finds the account by the number or creates it disabled; one it found rather than created answers `ErrExternalUnconfirmed` until its adoption is confirmed) |
| 10 | The account not announced, and the identity link's user not arrived | announce: identity's `Expect` (below) |
| 11 | In the window (`F − createBeforeDays ≤ T < F`) | `Update` an `enabled` account whose external spec changed (`last_spec_hash`); otherwise nothing |
| 12 | Due (`T ≥ F`) | a `disabled` account: `Activate` — a joiner on their first day, a rehire — with a fresh pass when *Run now* asked for one, unless the worker left it enabled earlier today (an account an administrator disabled today is not re-enabled before tomorrow); an `enabled` one: `Update` when the spec changed, otherwise nothing — never an `Activate`, so *Run now* asks no new pass for an account already enabled |

**The identity link beside it** keeps [the decision table](#accounts) with the person's
kind: `Prepare` finds the user by the number — `ErrAccountNotFound` until the announced
account arrives, recorded as nothing and looked for again at the next poll — and,
since the adopted user arrives enabled, the first time the link sees them active it
calls `Activate` once, which gives them the provisioning roles; `Update` writes the
language and the number; a leaver's user is deactivated. While the provider is not
connected, or no enabled module offers it, the identity link runs nothing but a due
deactivation and is claimed again at the next poll. A provider no enabled module offers
is neither read nor called: a provider link there is records `provider_unavailable`
while the flag is on.

**A link the person's kind no longer names** — HR changed the kind while a link under
a provider stayed, one the [kind lock](#the-account-kind) let go — is still worked
(`personProviderKey`: the person's kind when it names a provider, else their first
link under one, those not `deleted` first). On it nothing runs but a deactivate — a
leaver's, or the one *Disable now* asked for — it never reports
`provider_unavailable`, and it never holds the identity link, which runs by the
person's own kind.

**The announce.** After a `Prepare` the provider answered, the chain calls identity's
`Expect` ([the announced account](/en/admin/authentication/#the-announced-account))
with the person's employee number, display name and language, the account's
`ExternalID` as `ObjectID` and its principal name as `UserPrincipalName`, `Issuer`
left empty — identity fills it from its own single sign-on row while OIDC is on, since
Staff knows no issuer — and `ReplaceUserID` the identity link's user when that user is
Staff-managed, pending and holds no credential (a person whose kind HR changed from
`local`). It counts as one of the three provider calls, records `announced_at` and
`account_announced`. `Expect`'s refusals are identity's codes, recorded on `P` as
identity's are on its link — `employee_number_taken` or `account_privileged`, `P`
`blocked`, `account_external_blocked` — and never answered as `provider_refused`; an
announcement identity judges invalid is a failure to retry. The user the provider's
SCIM push or the person's first sign-in then creates is Staff-managed and numbered on
arrival, and the identity link's next `Prepare` finds it by the number. A `Deactivate`
or `Delete` while the identity link names no user and the account was announced also
withdraws the announcement (`Unexpect`, one more call, `announced_at` cleared); a
withdrawal that fails is logged and left for the next try. An announcement no user has
fulfilled ninety days after `announced_at` raises `accountNotArrived`.

**The result** on `P`, written under the person's lock while the identity link still
holds the claim's lease: `external_id`, `state`, `external_upn`, `external_email`,
`blocked_reason`, `last_error`, `attempts`, `next_attempt_at`, `reported_codes` (the
provider's `Reported` facts, without repeats, and Staff's own `manager_not_in_entra`),
`pass_issued_at`, `announced_at`, `last_spec_hash`, `last_synced_at`,
`photo_pushed_stamp` (kept as it was by a claim that pushed nothing), and the
`account_external_*` events — a report or a refusal recorded once, when the link's
state or reason changed. `pass_issued_at` is set when the account comes back with a
pass and none was recorded, or after a fresh one was asked for (*Run now*); it is
cleared when the account comes back with none, on a `Deactivate` or a `Delete`, and
when the pass is shown or found gone. When the provider answers another account than
the link named, the link's `announced_at` and `pass_issued_at` start over for it. A provider's refusal is recorded as a code, never an address or the
directory's words:

| The provider's answer | `blocked_reason` | `P` | The item |
| --- | --- | --- | --- |
| `ErrExternalAmbiguous` — two accounts hold the number | `external_ambiguous` | `blocked` | `accountAmbiguousInEntra` |
| `ErrExternalAddressTaken` — the work email is another account's | `external_address_taken` | `blocked` | `accountAddressTakenInEntra` |
| `ErrExternalSyncedOnPremises` | `external_synced_on_premises` | `blocked` | `accountSyncedOnPremises` |
| `ErrExternalPrivileged` — the account holds a directory role | `external_privileged` | `blocked` | `accountPrivilegedInEntra` |
| `ErrExternalEnabled` — a delete of an account still enabled | `external_enabled` | its state kept | `accountFailing` |
| `ErrExternalUnconfirmed` — the provider found an account it did not create holding the number, and nobody has confirmed its adoption; it wrote nothing | `external_unconfirmed` | `blocked` | `accountUnconfirmedInEntra` |
| `ErrProviderUnavailable`, or the status not `connected` | `provider_unavailable` | its state kept | the installation's, from the status |
| `Account` answers none | `user_gone` | `failed` | `accountUserGone` |
| `ErrInvalidActor` (a bug of Staff's, logged) | `invalid_actor` | `failed` | `accountFailing` |

Anything else is a failure to retry, as with identity: `last_error`
`accounts_unavailable`, `attempts` counted, the first of a run recorded as
`account_external_failed`; a failed read of the account is one too. The provider
link's own `next_attempt_at` is written but claims nothing — the identity link's claim
brings it back — so **a provider `Deactivate` that did not happen, or a `Delete` that
could not be made** (as a failed read when a deactivation is due or asked for), sets
the identity link's next attempt to the next poll, not the next civil day: a leaver
loses access as soon as the directory answers — or, for an adoption nobody had
confirmed, within one poll of the confirmation. The other refusals are claimed again
on the link's daily schedule, or by *Run now*.

**What a provider answers is bounded** before it is stored or shown (review L2): a
reported code, and a status's reason (before `settings.provider_status` and
`GET /meta`), must have the form `^[a-z_]{1,40}(:[A-Za-z0-9_.-]{1,64})?$` — a
lower-case name, and for a fact naming a thing (a licence's SKU) one part after a
colon — or it is dropped; a principal name longer than 254 characters is not recorded;
and the address the provider made is recorded on the link, and given to the person, only
when it passes Staff's own email rule (an address of at most 254 characters, stored
lower-cased). What is dropped is logged by its field's name alone, never its value. A step that answers nothing more resets the reason, so
`provider_unavailable` lifts once the provider is connected again. The provider's
reported codes are `hire_date_not_written`, `privileged`, `pass_not_allowed`,
`pass_forbidden`, `licence_unavailable:<SKU part number>`, `manager_not_found`,
`photo_not_written`, `adoption_unconfirmed` and `leaver_step_failed:<step>`; Staff adds its own
`manager_not_in_entra` when the person's effective manager has no account in the
directory.

**The address the provider made.** When the account answers an address
(`ExternalAccount.Email`) that passes Staff's email rule and the person has no work
email, the address is written to
the person's `work_email`, after the link's result and in a transaction of its own
under their lock, with the event `work_email_set_by_provider` — the one write to a
person the accounts worker makes, which moves the person's `revision` and
`updated_at`. Nothing is written when the person has a work email by then, when another
person's work email is that address, or when the person is erased; a failure is
logged, and the write is tried again on a later call that answers the address.

**The external spec** (`accounts_spec.go`) is what every provider call carries
(`contracts.ExternalSpec`): the employee number, the given and family names, the
display name, `WorkEmail` — the work email only, never the private one; empty lets the
provider make the address — `WorkCountry` (the usage location), `PreferredLanguage`,
`HireDate` and `LeaveDate` — each of these three only while its row of the
[directory profile](#the-directory-profile) is on, with the profile's values —
`Fields`, the profile's switched-on attributes, `ManagerExternalID` — while the
`manager` row is on, the effective manager's own provider link's `external_id` while
that link is `enabled` or `disabled`; a person with a manager who has none gets an
empty one and `manager_not_in_entra` on their link — `Licences`, the union of their
groups' [licences](#the-licences), and `Flags.Fresh` on *Run now*. `last_spec_hash` is SHA-256 over the
spec as JSON, the flags left out, so a mover is one `Update` on the day the record
changes and nothing on the others.

### The directory profile

**Which fields a provider writes** on a person's external account is a fixed mapping
the installation switches field by field (Entra design D8): `settings.directory_fields`,
`directoryFields` on `PUT /settings` (`staff:manage`), its keys the switchable rows
below — a stored key that names no switchable row is ignored, and a row the column does
not name reads as its default. The person's Account tab reads it back as
`directoryProfile` on `GET /people/{id}/account`, present for a person of a provider's
kind — every row, in this order, `{key, on, switchable, value?}`, `on` always true for
a row that is not switchable, `value` absent when empty — to whoever reads the tab, the
person included: it is their own data as the directory would see it. The worker builds
the [external spec](#the-provider-link) from the same values, so what HR reads is what
the provider is sent. `ExternalSpec.Fields` carries the directory attributes of every
switchable row switched on, each with its value — empty to clear it in the directory
when the register holds nothing — and nothing for a row switched off, which the
provider leaves as it is in the directory. The dates, the language and the manager
travel in the spec's own fields instead, and only while their rows are on.

**The values** are read on the day: the place is the person's earliest employment in
force today, else the earliest to come (judged on its first day, so a joiner prepared
before it is sent the place they start in), else the one that ended last (judged on its
last day), and its terms in force on that day.

| Key | From the register | Sent as | Default |
| --- | --- | --- | --- |
| `givenName`, `familyName`, `displayName` | the names | `GivenName`, `FamilyName`, `DisplayName` | on, not switchable |
| `employeeId` | the employee number | `EmployeeNumber` | on, not switchable |
| `userPrincipalName` | the work email | `WorkEmail` (empty: the provider makes the address) | on, not switchable |
| `usageLocation` | the work country | `WorkCountry` | on, not switchable |
| `employeeHireDate` | the earliest start of an employment not ended before today, else the start of the one that ended last | `HireDate` | on |
| `employeeLeaveDateTime` | the latest end, when every employment has one; else none | `LeaveDate` | on |
| `employeeType` | the terms' employee type | `employeeType` | on |
| `jobTitle` | the terms' title | `jobTitle` | on |
| `department` | the terms' department's name | `department` | on |
| `companyName` | the legal entity of the employment's business unit | `companyName` | on |
| `employeeOrgData` | the cost centre and the division — the terms', else the department's | `costCenter` and `division` | on |
| `officeLocation` | the terms' location's name | `officeLocation` | on |
| `workAddress` | that location's street, postal code, city and country | `streetAddress`, `postalCode`, `city`, `country` | off |
| `phones` | the work phone and the mobile | `businessPhone`, `mobilePhone` | off |
| `preferredLanguage` | the language — shown as `nb-NO` or `en-GB` | `PreferredLanguage` (`nb` or `en`) | on |
| `manager` | the effective manager on the place's employment — the terms', else the department's, by the [manager rule](#managers-and-the-org-view) — shown by name | `ManagerExternalID`, the manager's own external account | on |

Private contact details are never mapped. What the directory attribute is called, and
which it writes on create only, is the provider's.

### The pass

**`POST /people/{id}/account/pass`** (`staff:manage`, `account_pass.go`) shows the
Temporary Access Pass the person's provider issued for their external account —
**once**. The provider keeps the pass sealed until then and answers it a single time
(`RevealPass`, on no connection, the caller the actor), so the answer is
`StaffAccountPass {pass, expiresAt}` with `Cache-Control: no-store`. Then, under the
person's lock and whatever happens to the response — the provider has spent the pass —
the link's `pass_issued_at` is cleared and the reveal is recorded as the event
`pass_revealed`, the viewer its actor, the provider in its payload, never the pass.
Staff never stores, logs or records the pass itself. A second call, a call after the
pass's lifetime and a call when none was issued are 409 `pass_gone` (the provider's
`ErrPassGone`), which also clears `pass_issued_at`; *Run now* on the person issues a
new one. No such person, or no provider link naming an account, is 404; no enabled
module offering the provider, or any other failure of the provider's (logged), 503
`accounts_unavailable`, nothing cleared. It is rate-limited with *Run now* and
*Disable now* (one counter, 120 an hour from one client address). A pass issued and not
shown for a day raises `passUnrevealed`.

### The directory's photo

The accounts worker pushes the person's [effective picture](#the-photo) — HR's photo as
identity holds it, else the linked user's own avatar — to their account in the
provider's directory (`accounts_worker_photo.go`; photo sync design §4, plan reading 10).
It is one direction: Staff writes the directory's photo and never reads it, so a photo
an administrator sets in the directory is replaced at the next push.

- **When.** On a claim of the person's identity link, at the end of the
  [provider column](#the-provider-link), only when its chain ended with nothing more to
  do (the third column answered nothing) on an account the provider answered
  `enabled` — and the provider is `connected`, the person's flag is on and they are not
  erased, no disable is asked for, the kinds are seeded, the link is the one the
  person's kind names, and the identity link names a user. The push spends the provider
  chain's three calls: with them spent, the link is claimed again at the next poll. The
  lease is renewed before the call; a lease found lapsed stops the push and leaves the
  link as it was.
- **The stamp.** The worker reads the linked user's effective avatar's stamp without its
  bytes (`UserDirectory.AvatarInfo`) — `managed:<stamp>` for HR's photo,
  `own:<version>@<epoch>` for the user's own — or `none` when there is none, and compares
  it with the link's `photo_pushed_stamp`. Equal: nothing. Different: the bytes are read
  (`UserDirectory.Avatar`, the stamp taken again from that read) and `SetPhoto` called
  with them, or `ClearPhoto` for `none`. An avatar the directory cannot read is logged and
  read again at the next claim. So a changed own avatar, a late link or a failed push is
  right by the next claim, and the same picture is never pushed twice. **Vantigo removes
  only what it wrote**: a link with no stamp yet and no picture in Vantigo records `none`
  without a call, so a photo the tenant already had stays until Vantigo writes one.
- **The result.** When the provider answered, the stamp is written to
  `photo_pushed_stamp` with the link's result, the account is read again (`Account`) so
  `reported_codes` are the directory's at once, and one `account_external_updated` event
  is recorded, `{step: "photo", state, code, codes}`, its code `photo_pushed` for a
  picture written, `photo_cleared` for one removed, `photo_refused` when the re-read
  account reports `photo_not_written`. A re-read that fails is logged: the push stands,
  the event says `photo_pushed` or `photo_cleared`, and the next claim reads the codes.
- **A refusal.** A picture, or a removal, the directory refuses — for Entra a 400, 403
  or 413 from Graph, a 404 on the photo's write (a user without a mailbox), or a picture
  the provider cannot decode or bring under its limit
  ([the Entra photo](/en/reference/entra/#setphoto-and-clearphoto)) — is answered as
  nothing wrong, and the account reports `photo_not_written`
  (`contracts.ReportedPhotoNotWritten`) until a push is taken or a clear runs: the
  link's `reported_codes` carry it and it raises `photoNotWritten` for `staff:manage`
  ([attention items](#attention-items)). The stamp is written all the same, so the same
  picture is not pushed again; another photo from HR, or another avatar from the user,
  changes the stamp and the next claim tries that one. The call's own refusals —
  `ErrExternalUnconfirmed` and the rest — are recorded as the
  [provider link](#the-provider-link) records them.
- **A failure.** Anything else is the link's failure, as any provider call's: `last_error`
  `accounts_unavailable`, `attempts` counted, the first of a run recorded as
  `account_external_failed` with the step `photo`, tried again at the next claim; three
  failed attempts raise `accountFailing`. When the push is the claim's only call, the
  failure the link carries from the last claim counts on rather than starting over.
- **No permission anywhere.** Nothing for HR to grant or press, and nothing new in the
  tenant: Entra's photo is written under the already consented `User.ReadWrite.All`.

## Groups

**Groups** are HR's named sets of people (Entra design D9, D10 and D14): listed — HR
names the members — or derived from the register by a rule, with owners, and kept by
each provider HR turns on for them in its own directory (a security or Microsoft 365
group in [Microsoft Entra ID](/en/reference/entra/#groups-in-the-tenant)). A group's
`licences` trait is how a person gets their licences in the directory: the
[licences](#the-licences) they are sent are the union of their groups'. Every group
operation is `staff:manage`'s (`staff:access+staff:manage`); `staff:view`, a manager and
anyone else get the uniform 403. Groups are **not audited** (plan reading 10), as the
structure's writes are not: a membership's change is an event on the person, and what
a provider did is on its own trail.

### The group

A group is a row of `staff.groups`:

- **`key`** — 1 to 60 lower-case letters, digits and hyphens, starting with a letter or
  a digit (`^[a-z0-9][a-z0-9-]{0,59}$`), unique, set on create and **never changed**: it
  is the reference a provider keeps the group by — Entra's mail nickname unless the
  group's `mailNickname` trait names another. A key another group holds is a field error
  on `key`. A `PUT` ignores a key left out or the group's own; any other is a field
  error on `key` ("cannot change").
- **`name`** — 1 to 120 characters; **`description`** — up to 500, cleared when a `PUT`
  leaves it out. A change of either makes every section of the group that is neither
  retired nor deleted due at once — a failed one included, since what HR changed may
  be what the provider refused — so the worker carries it at its next cycle.
- **`kind`** — `listed` or `derived`. A derived group needs a `rule`; a listed one
  refuses one (a field error on `rule`). A `PUT` may change the kind: a group made
  derived has its members written by the rule at the next materialisation, and one
  made listed keeps its members until HR sets them; a member who stays keeps the
  `source` and `since` they came in with.
- **`revision`** — 1 on create; `PUT /groups/{id}` needs `If-Match`
  ([revisions](#refusals-and-revisions)); `revision_conflict` when it has moved on.

`POST /groups` answers 201 with the group. A listed group starts empty; a derived one's
members are materialised by the accounts worker's next cycle. `GET /groups` lists every
group, archived ones included, by name, with `memberCount`, `ownerCount` and, for each
provider HR turned on for it, `{key, enabled, state}` — never the members and owners
themselves, which only one group's own answers carry (`GET /groups/{id}` and the writes
of one group: its `PUT`, its archive, its members and its owners), each
`{personId, displayName, employeeNumber}` by name and, for a member, `source` and
`since`. An unknown id is a bare 404.

### The rule

A derived group's `rule` is one predicate over the register (`StaffGroupRule`, plan
reading 4):

| `kind` | Names | A person is a member today when |
| --- | --- | --- |
| `employed` | nothing | they have an employment in force |
| `department` | `id`, a department; optionally `includeBelow` | the terms in force name the department — or, with `includeBelow`, it or any department beneath it by the parent chain, archived departments included |
| `businessUnit` | `id`, a business unit | the employment is in the unit |
| `location` | `id`, a location | the terms in force name the location |
| `employeeType` | `value`, an employee type | the terms in force carry that employee type |
| `reportsTo` | `id`, a person | their effective manager today on that employment, by the [manager rule](#managers-and-the-org-view) — the manager the terms in force name while in employment, else the department's, climbing the parent departments — is that person: one level, their reports and not their reports' reports |

`kind` must be one of the six (a field error on `rule.kind`). `id` is required for the
four kinds that name a row and must name one that exists — an archived department,
business unit or location is admitted, and so is any person — and is refused for the
other two; `value` is required for `employeeType` and must be one of the employee
types the terms know (`employee`, `contractor`, `consultant`, `intern`) and is refused
for the others; `includeBelow: true` is refused on any kind but `department`, and a
`false` is not stored. Each is a field error under its path (`rule.kind`, `rule.id`,
`rule.value`, `rule.includeBelow`), the reference checked inside the write's
transaction.

**In force** means on the business day — today in the settings'
[time zone](#the-business-time-zone) — over the people not erased: an employment that
started on or before today and has not ended before it (the end day still counts), and
its terms row in force today. A person is judged on **one employment**: of those in
force, the earliest by start date (then by id), the one the
[directory profile](#the-directory-profile) reads — so concurrent employments give one
department, one unit and one manager here. A person whose employment has no terms row
in force today is in an `employed` or `businessUnit` group and in no `department`,
`location`, `employeeType` or `reportsTo` one.

### Members and owners

A group's members are rows of `staff.group_members`: the person, `source` (`listed` or
`derived`) and `since`, the day they joined.

**The materialisation** (`materialiseGroups`, `groups_members.go`) is the
[accounts worker's](#the-accounts-worker) first step after the providers' statuses and
before the preparing links, skipped while the account kinds are unseeded. For each
derived group that is not archived, in its own transaction under the transaction
advisory lock `staff:group:<id>` and then the group's row, it evaluates the rule today
and writes the difference against the rows: a person who entered the set gets a row
with `source` `derived` and `since` today, and one who left it loses theirs. Each
change is an event on the person — `group_joined` or `group_left` — by the run's
actor. A group deleted, archived or made listed since the list was read is left. A
group whose evaluation fails is logged and skipped, and the cycle goes on with the
others. An archived group's members stay as they stood.

**A listed group's members** are set by `PUT /groups/{id}/members` with
`{personIds}`: exactly these people from today, the difference written under the same
lock, `source` `listed`, a duplicate counted once, each change an event on the person
with the caller as its actor. A person that does not exist, or one erased, is a field
error on `personIds`. A derived group's members are its rule's: the `PUT` is 409
`group_derived`; an archived group's, 409 `group_archived`. It answers the group with
its members and owners.

**A change of members** — the materialisation's or the `PUT`'s, anyone joining or
leaving — makes every section of the group that is neither retired nor deleted due at
once, in the same transaction, a failed one included, so the worker carries the members
at its next cycle whatever the section waits for.

**The events** `group_joined` and `group_left` carry `{groupId, groupName, groupKey,
source}`, of class C. They are listed to every reader of the person's
[history](#the-history) — unlike the [account events](#accounts), which only the
links group sees — but their payload is the links group's: the person,
`staff:manage` and `staff:personal-data`, who read the person's groups on the Account
tab, read the group and the source, and anyone else, a manager included, reads the
bare kind with an empty payload.

**The owners** (`staff.group_owners`) are the people a provider names as the group's
owners in its directory, beside the provider itself. `PUT /groups/{id}/owners` with
`{personIds}` replaces them, under the same rules for the people, records no event,
and makes every section of the group that is neither retired nor deleted due at once,
as a change of the name does; an archived group is 409 `group_archived`. It answers the
group with its members and owners. The provider is sent the owners' accounts under it
— the external ids of their [provider links](#the-provider-link) that are `enabled` —
so an owner without such an account owns the group in Staff alone. The owners are part
of the values' hash, so a change of owners, or an owner's account enabled since, is an
`EnsureGroup` at the next sync.

**Who is sent as a member**: a member's account under the provider, the external id of
their provider link while it is `enabled`. A member with no account there, one whose
account is disabled — a leaver — or deleted is not sent, and so leaves the external
group at its next sync while the Staff group keeps the row.

**A person leaving every group**: the retention run's erase and `DELETE /people/{id}`
delete the person's `group_members` and `group_owners` rows in their own transaction,
before the person's, and record no event — the person is going (plan reading 9). The
members' hash differs at the next sync, which takes them out in the directory.

### Archive and delete

**`POST /groups/{id}/archive`** sets `archivedAt` and who archived it, moves the
revision on, makes every section of the group that is neither retired nor deleted due
at once, and answers 200 with the group; archiving it again changes nothing — not the
stamp, the archiver or the revision. An archived group is read-only: `PUT`, the
members, the owners and the providers — turning one off included — are 409
`group_archived`. It is no longer materialised, its members stay as they stood, and
each provider's section is [retired](#the-sync) — deleted in the directory after the
settings' `deleteExternalAfterDays`, or renamed `… (archived)` and kept when that is
never. A person's lists leave archived groups out.

**`DELETE /groups/{id}`** answers 204 for a group no provider has reached or may be
reaching — no section that has synced, holds the external group's id, is turned on, or
is claimed by the worker under a live lease — archived or not, and deletes its
members, owners and sections with it, under the group's lock. Each member is recorded
leaving it first — a `group_left` event on the person by the caller, with the member's
`source` — so a person's history keeps no join without its end. Any other group is 409
`group_synced`: one a provider has made or found in its directory, one with a provider
turned on, and one whose section the worker holds under a live lease — its first sync
in flight, the external id not written yet — since a delete would take the sections
with it and orphan the provider's group. Archive it instead, or turn its providers off
first while none has made it. An unknown id is a bare 404. The group's page offers
**Delete** by the same rule but the lease, which it cannot see.

### Traits

A provider in the slot **declares the traits** a group may carry for it, and Staff
renders, stores and checks them from that declaration alone — no trait is named in
Staff's code. `GroupTraits(ctx)` answers `[]GroupTrait` — `Key`; `Type`, one of `bool`,
`string`, `choice` and `list`; `Label` and `Description` in `en` and `nb`; `Choices`
(`{Value, Label}`, for `choice` and `list`); `DocURL`, the vendor's page; `Default`;
`Required`; and `MaxLength` for a `string` (0 for unbounded). The provider reads its own
state to answer — never the network — and is called through `providerGroupTraits` in
`contractscalls.go`, on no connection, under the two-minute call timeout. Entra's six
traits are under [its reference](/en/reference/entra/#the-traits).

**`GET /groups/{id}/traits`** asks every provider in the slot for its status and its
declaration, each under the call timeout, and answers a section for each, in the
slot's order, whether HR turned it on for the group or not: `key`, `name`, `status` (as
the provider answers it), `enabled`, `traits` (the declaration,
`StaffGroupTraitDeclaration`), `values`, and from the row `state`, `externalId`,
`lastError`, `lastErrorField`, `syncedAt` and `retiredAt`. A provider that could not say
its status is listed `failing`; one whose declaration could not be read is listed
`failing` with no traits.

**`PUT /groups/{id}/traits/{provider}`** with `{enabled, values}` turns a provider on or
off for the group. An unknown group, or a provider the slot does not hold, is a bare
404; an archived group 409 `group_archived`.

- **`enabled: true`** checks the values against the declaration (plan reading 3). A
  key it does not declare, a value of the wrong type (a boolean for `bool`, a string
  for `string` and `choice`, an array of strings for `list`), a `choice` not among its
  choices, a `list` with a value outside them, or a `string` over `maxLength`
  characters is a field error under `values.<key>` whose code and message are both the
  bare code `trait_invalid`; a `required` trait with no value — absent, `""` or `[]`,
  while `false` is a value — is `trait_required` the same way, unless its key is
  refused already. A `null` value is left out, and an empty string is admitted for a
  `choice`. A provider whose declaration cannot be read cannot have its values
  checked: the request is 503 `provider_unavailable`, and nothing is written. Then the section's row
  (`staff.group_traits`, one per group and provider) is written `enabled`, `pending`,
  the values stored, `attempts` 0, and `disabled_at`, `last_values_hash`, `last_error`,
  `last_error_field` and `retired_at` cleared, due at once, and any claim the worker
  holds on it ended, so its result is not written over these: the worker sends the
  values at its next cycle. `external_id` and `last_members_hash` stay — the provider
  finds its group again.
- **`enabled: false`** on a section sets `enabled` false and `disabled_at` — now, or
  kept from the first time it was turned off — keeping the values and the state; the
  values sent are ignored. It is due at once, unless already retired or deleted, any
  claim on it ended, and the worker [retires](#the-sync) it. On a group with no section
  for the provider it writes nothing and answers 200.

It answers the section. What the declaration cannot say — a trait that cannot change
once the group exists, a trait that needs a person as owner, a value another trait
forbids — is the provider's to judge when the worker sends the values, and comes back
on the row as `lastError` and `lastErrorField`, shown beside the field.

### The sync

The accounts worker's cycle, after it has worked the links, **syncs the groups**
(`syncGroups`, `groups_sync.go`) — after, so a member's account enabled in this cycle is
in their groups in this cycle too. It claims up to 100 sections a cycle
(`groupsBatch`), one at a time (`ClaimGroupTrait`, `FOR UPDATE SKIP LOCKED`, the lease
five minutes as a link's), turned on or off: a section is due when its
`next_attempt_at` has come, no live lease holds it and it is neither `retired` nor
`deleted`. A section with no `next_attempt_at` is not due — the one a provider
refused, below. A section claimed twice in one cycle, its backoff shorter than the
cycle, is handed back for the next poll and ends the batch; a run by hand stops at its
budget. Every write of the result is `WHERE lease_id` is the claim's, so a section HR
changed meanwhile, or a group deleted, drops it. A claim reads the group, its owners'
and its members' accounts under the provider (sorted, de-duplicated), the settings and
the values, then decides:

1. **The provider not `connected`** in this cycle: nothing is written but the next
   poll; no attempt is counted.
2. **Retiring** — the group archived, or the section turned off, since the earlier of
   `archived_at` and `disabled_at`. When the settings' `deleteExternalAfterDays` is a
   number and that many civil days, in the business time zone, have passed since then,
   `DeleteGroup`, and the section reads `deleted` — whatever the provider made of it:
   Entra soft-deletes a Microsoft 365 group it made, empties and keeps a security group,
   and leaves one it did not make ([DeleteGroup](/en/reference/entra/#deletegroup)). When it is never, `EnsureGroup` once
   with the name followed by ` (archived)` — a name that already ends so is left — and
   the values, and the section reads `retired`, with `retired_at` (plan reading 5: the
   provider knows no archive, so one code path writes names); a section the provider
   never made a group for is retired with no call. A rename the provider refuses — a
   trait error or `group_not_manageable` — still leaves the section `retired`, the code
   kept on the row: an archived group takes no new values, and a `failed` section there
   would raise an item nobody could clear. A call that could not be made is counted and
   retried as below. Before the days have passed nothing is sent and the section waits
   for the next poll. A retired or deleted section is never claimed again; turning the
   provider on anew starts it over.
3. **Otherwise** `EnsureGroup` when the section has no `external_id` or the values' hash
   differs from `last_values_hash` — SHA-256 over the name, the description, the values
   and the owners' sorted external ids (`owners`) as JSON with sorted keys, so a change
   of owners reaches the provider through `EnsureGroup` too — then `SetMembers` when the
   members' hash differs from `last_members_hash` — SHA-256 over the civil day in the
   business time zone and the sorted, de-duplicated external ids, joined by `\n`. The day
   is in it so that every group's members are reconciled at least once a day, whatever
   Staff saw: a member added in the directory by hand, or a self-join on a public group,
   is undone by the next day's `SetMembers` (plan reading 14). At most two calls a
   claim, each through `contractscalls.go` on no transaction, under the two-minute
   timeout. A section with nothing changed is claimed and read at each poll and makes no
   call but the day's first `SetMembers`.

| The provider's answer | `state` | `last_error` | Next |
| --- | --- | --- | --- |
| Done | `synced`; `external_id` and both hashes written, `synced_at` when a call was made, `attempts` 0 | cleared | the next poll |
| Done, with `ExternalGroup.Pending` (Entra's team on a young group) | `synced` | cleared | the values' hash is not written, so the next cycle calls `EnsureGroup` again (plan reading 7) |
| `MembersResult.Failed` with *n* members | `synced` | `members_failed:<n>` | the members' hash is not written, so the next cycle tries them again; the ids are never stored, only counted in a debug log |
| `GroupTraitError` on a section with no `external_id` | `failed` | its code (`trait_required`, `trait_invalid`, `trait_immutable`, `trait_needs_owner`), `last_error_field` the trait; `attempts` 0 | not due again until HR saves the values, changes the group's name, description, owners or members, or archives it; the values' hash is not written |
| `GroupTraitError` on a section holding the `external_id` — the provider has the group | `failed` | as above | the values' hash is written, so `EnsureGroup` is not called again until the values change; but `SetMembers` still runs when the members' hash moved — a leaver never keeps the group's access while HR mends a trait (plan reading 14) — and the section is due at the next poll. A `SetMembers` that fails leaves the refusal as the row's word: a call that could not be made is counted and retried on the backoff, the provider unavailable at the next poll; members it could not change are tried again, with no `members_failed` written over the refusal |
| `ErrGroupNotManageable` — the provider may not write the group: Entra's [reasons](/en/reference/entra/#ensuregroup) | `failed` | `group_not_manageable`; `attempts` 0 | as a `GroupTraitError` on a section with no `external_id` |
| `ErrGroupUnconfirmed` — the provider found a group holding the key and adopted it, and waits for a holder of its permission to confirm it ([Entra's adopted groups](/en/reference/entra/#adopted-groups)) | `pending` | `group_unconfirmed`, `last_error_field` cleared | the next poll; no attempt counted: no failure and no item, and the cycle after the confirmation proceeds |
| `ErrProviderUnavailable` | kept | kept | the next poll; no attempt counted |
| Anything else — the call could not be made | kept; `failed` from the third failure in a row | `call_failed` | `attempts` counted, retried on the accounts worker's backoff (`backoff.go`) |

Codes only: `last_error` never carries the directory's words, and a member's external
id is never stored. What a provider's `GroupTraitError` names is bounded before it is
written: the code at 200 characters (`last_error`), the field at 40
(`last_error_field`). A `failed` section raises
[`groupSyncFailed`](#attention-items) until it is no longer failed.

### The licences

`ExternalSpec.Licences` ([the external spec](#the-provider-link)) is the union of the
`licences` values of the person's groups' sections under their provider that are on
and neither retired nor deleted, of groups not archived (`LicencesOfPerson`), sorted
and de-duplicated (Entra design D10); with none it is nil. The spec's hash covers
it, so a change of a group's licences, or of the person's membership, is an `Update`
at the person's next claim; on an enabled account the provider assigns the licences
missing and removes those it assigned itself that the list no longer names — every one
of them when the list is empty — while a licence an administrator assigned directly,
and one the directory assigns through its own group-based licensing, are left alone
([Entra's Update](/en/reference/entra/#update)).

### The person's groups

`GET /people/{id}/account` carries `groups` — the groups the person is in, archived
groups aside, by name, each `{id, name, key, source, since}` — to the tab's own
readers: the person, `staff:manage` and `staff:personal-data` (plan reading 8).
`GET /me/groups` answers the caller's own, by name and with no more than `{id, name}`
([My page](#my-page)).

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
- **`GET /me/groups`** is the [groups](#the-persons-groups) the person is in, archived
  groups aside, by name alone.
- **The Competence tab** has no `/me` path of its own: My page calls
  [a person's competence operations](#a-persons-competences) with the linked person's
  id, as Self — the person declares, changes and removes their own declared rows,
  renews their own and [files their evidence](#evidence), and never verifies one. The
  tab's top is **What your profile asks**: [the gap list](#profiles-and-gaps) of the
  profiles that reach the person, read through `/people/{id}/competence-gaps` with the
  same id; the person assigns none.
- **What the person wants** has no `/me` path either: My page reads and replaces
  [the interests, the preferences and the availability declaration](#what-the-person-wants)
  through `/people/{id}/interests`, `/preferences` and `/availability` with the linked
  person's id — the only caller who may replace them — and reads their
  [reviews](#the-review) through `/people/{id}/competence-reviews`, recording none.
  `GET /me` carries `lastCompetenceReview`, as the person reads the Reviews group.
- **The photo and the glance** have no `/me` path either: My page shows the person's
  [effective picture](#the-photo) through `/people/{id}/photo` with the linked person's
  id; its menu sets and removes the person's own Vantigo avatar through identity's
  `/api/v1/identity/account/avatar` — calling identity directly, the same origin (photo
  sync reading 11) — and says **Set by HR**, disabled, while HR's photo stands, which
  identity would refuse 409 `avatar_managed`. The person never sets Staff's photo. Its
  Profile tab opens on [at a glance](#at-a-glance), read through
  `/people/{id}/glance` with the same id — Self reads every section but the contract.
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
person never writes their own documents — but for the [evidence](#evidence) of their
own competence, filed through the competence row — and anyone else gets the uniform
403.

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
  (S15); `caseId` on another category, a case not the person's, or a case the
  retention run has erased or is due to erase (its close more than
  `healthRetentionDays` ago) is a 400, and a document moved out of a restricted
  category leaves its case.
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

## Competence

Competence is the first delivery of Staff's second phase (the
[design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-10-25-staff-competence-design.md),
delivery A): a flat [catalogue](#the-catalogue) of competences in categories — skills
with a level and certificates with a validity, the Norwegian certificates seeded — and
[a person's competences](#a-persons-competences), declared by the person on My page,
[verified](#verification) by their manager, [renewed](#renewal) as new rows and
[removed](#removal), carrying their [evidence](#evidence) in the person's archive,
mailed ahead of their end by the [competence worker](#the-competence-worker) and shown
on the dashboard as [attention items](#the-competence-items); and the read contract
[`contracts.Workforce`](#the-workforce-contract), through which another module learns
who holds what, valid on a date. Delivery B, the one-to-one, adds
[what the person wants](#what-the-person-wants) — their [interests](#interests),
[preferences](#preferences) and [availability declaration](#the-availability-declaration),
written by the person alone and stamped **Seen** by their manager or HR — and
[the review](#the-review), the record that the two went through the tab together,
with its interval setting and overdue item. Delivery C completes the phase:
[competence profiles](#profiles-and-gaps) — what a position needs, assigned to people
by HR or made a department's default — with the gap list at the top of the tab and
its dashboard item, [the matrix](#the-matrix) of who holds what valid on a day,
[the interests overview](#the-interests-overview) for HR, and the matching contract's
Staff side, written into [the Workforce contract](#the-workforce-contract). No
permission is new: `staff:manage` keeps the catalogue and the profiles,
`staff:approve` verifies and records reviews, and a manager needs none for their own
reports.

### The catalogue

`staff.competence_categories` and `staff.competences` are the catalogue (design S1,
S2). It is about no person: no retention class, never touched by an erase. Categories
are flat, and every competence is filed under one.

**Categories.** A category has a `key` (1–40 characters `[a-z0-9-]`, unique),
`names` `{en, nb}` (each 1–100 characters), `seeded`, `archivedAt`, `sort` and
`revision`. Five are seeded, in this order:

| Key | English | Norwegian |
| --- | --- | --- |
| `fag` | Trade | Fag |
| `sertifikat` | Certificate | Sertifikat |
| `sprak` | Language | Språk |
| `verktoy` | Tool | Verktøy |
| `domene` | Domain | Domene |

- **`GET /competence-categories`** lists every category in order, archived ones
  included, so a competence — and a person's row through it — can still name its own.
  Every caller with `staff:access` reads them.
- **`POST /competence-categories`** (`staff:manage`) adds a company category after the
  others; a key another category has is a 400 on `key`.
- **`PUT /competence-categories/{id}`** (`staff:manage`, `If-Match`) replaces a
  company category's `names` and sets `archived`. The key never changes. A seeded
  category never changes at all (`seed_immutable`, 409). Archiving a category that a
  competence not archived is filed under is `category_in_use` (409): archive the
  competences, or move them, first. A category's write records no event, as a
  document category's does not.

**Competences.** A competence has a `categoryId`, a `key` (1–60 characters
`[a-z0-9-]`, unique), `names` `{en, nb}` (each 1–100 characters), a `kind`,
`hasLevel`, `validityMonths`, `reminderLeadDays`, an `issuerHint` (at most 200
characters, shown to whoever adds the competence), a `basis` (the regulation it rests
on; the seeds' alone), `seeded`, `hidden`, `archivedAt`, `sort` and `revision`.

- **The kind** is `skill` — a proficiency, with a level when `hasLevel`, never a
  validity — or `certificate` — issued by someone, valid for `validityMonths` from its
  issue (1–240) or without end. A validity on a skill is a 400 on `validityMonths`, and
  a skill's row never carries a `validTo` (`skill_no_validity`, below).
- **The level** is one scale, fixed in code and named in both languages: 0 *aware*
  (*kjenner til*), 1 *basic* (*grunnleggende*), 2 *independent* (*selvstendig*), 3
  *experienced* (*erfaren*), 4 *expert* (*ekspert*). `hasLevel` decides whether every
  row of the competence carries one.
- **The reminder lead** is how many days before a row's end the
  [worker](#the-competence-worker) mails and the dashboard shows it as expiring: 0–365,
  and only with a validity (a 400 on `reminderLeadDays` otherwise). Left out beside a
  validity it is the default — **30** days for a validity of up to 12 months, **60** up
  to 24, **180** beyond — on a create and on every change alike. A competence without a
  validity has no lead, and its rows are never expiring.
- **`GET /competences`** lists the catalogue in its categories' order, then its own,
  optionally one category's (`categoryId`). Every caller with `staff:access` reads the
  competences that are neither hidden nor archived — the person declaring one on My
  page needs them. `includeHidden` and `includeArchived` add the others **for
  `staff:manage` alone**; for anyone else they are ignored.
- **`POST /competences`** (`staff:manage`) adds a company competence after the others,
  in a category that exists and is not archived (a 400 on `categoryId` otherwise); a
  key another competence has is a 400 on `key`. Its key and its kind are fixed from
  then on.
- **`PUT /competences/{id}`** (`staff:manage`, `If-Match`) replaces `categoryId`,
  `names`, `hasLevel`, `validityMonths`, `reminderLeadDays`, `issuerHint`, `hidden` and
  `archived`. The body is closed: a `key`, a `kind` or any other property the contract
  does not name is a 400 naming it, and nothing is written. A **seed** changes its
  names, its lead, its issuer hint and `hidden` alone; another category, level or
  validity, or `archived`, is `seed_immutable` (409) — a seed is hidden, never
  archived. A **company** competence moves to any category not archived, and its
  `hasLevel` is fixed once a person's row names it (`competence_in_use`, 409).
  Restoring an archived competence whose category is archived is a 400 on `archived`:
  restore the category first.
- **Hidden and archived.** A hidden or archived competence is offered for no new row
  (`competence_unavailable`, a 400 on `competenceId`) and keeps every row that names
  it: those rows are still read, changed, verified, renewed and removed, an archived
  one's with `competence.archived` true. Neither is refused for the rows held —
  history outlives the catalogue. Hiding is how HR stops offering a seed the company
  does not use; archiving retires a company competence.
- **History.** Each competence write is an installation event, `competence_created` or
  `competence_updated`, naming the competence (`competenceId`) and the fields set or
  changed. It is about no person and touches no retention class.

The seeds are the Norwegian certificates, under `sertifikat`, kind `certificate` and
without a level, the validity in months and the lead in days:

| Key | English | Norwegian | Validity | Lead | Basis |
| --- | --- | --- | --- | --- | --- |
| `fagbrev` | Trade certificate | Fagbrev eller svennebrev | — | — | opplæringslova |
| `fse` | FSE training with first aid | FSE-opplæring med førstehjelp | 12 | 30 | FSE § 7 |
| `hms-kort` | HMS card | HMS-kort | 24 | 60 | FOR-2007-03-30-366 |
| `truck` | Forklift, certified training | Truckfører, sertifisert opplæring | — | — | fua. § 10-3 |
| `kran` | Crane, certified training | Kranfører, sertifisert opplæring | — | — | fua. § 10-3 |
| `masseforflytning` | Earth-moving machinery, certified training | Masseforflytningsmaskiner, sertifisert opplæring | — | — | fua. § 10-3 |
| `dokumentert-sikkerhetsopplaering` | Documented safety training | Dokumentert sikkerhetsopplæring | — | — | fua. §§ 10-1, 10-2 |
| `stillas-bruker` | Scaffolding user | Stillasbruker | — | — | fua. § 17-5 |
| `stillas-bygger` | Scaffolding erector | Stillasbygger | — | — | fua. §§ 17-2–17-4 |
| `forerkort-b` | Driving licence B | Førerkort klasse B | 180 | 180 | førerkortforskriften § 4-1 |
| `forerkort-c` | Driving licence C | Førerkort klasse C | 60 | 180 | førerkortforskriften § 4-1 |
| `forerkort-d` | Driving licence D | Førerkort klasse D | 60 | 180 | førerkortforskriften § 4-1 |
| `yrkessjaforkompetanse` | Professional driver competence (code 95) | Yrkessjåførkompetanse (kode 95) | 60 | 180 | yrkessjåførforskriften |
| `varme-arbeider` | Hot work | Varme arbeider | 60 | 180 | insurers' sikkerhetsforskrift |
| `arbeidsvarsling` | Work zone signage | Arbeidsvarsling | 60 | 180 | Statens vegvesen N301 |

Their issuer hints name the issuer the law names: `fylkeskommunen` for the trade
certificate, `arbeidsgiver eller kursleverandør` for FSE, `Arbeidstilsynets
kortutsteder` for the HMS card, `Statens vegvesen` for the driving licences and code
95, and `Norsk brannvernforening` for hot work. The scaffolding heights (2–5, 5–9,
over 9 m) are one `stillas-bygger`, the height in the row's `reference`. No skill is
seeded: skills are the company's own vocabulary.

### A person's competences

`staff.person_competences` holds them (S3): the competence, a `level` when it has one,
`issuedOn`, `validFrom`, `validTo`, the `issuer` (at most 200 characters), a
`reference` (at most 100: the certificate's number, a scaffold's height), the evidence
document, the `source` — `self`, `manager` or `hr`: who entered the row — and the
verification: when, by whom, on what `basis` (`evidence` or `observed`) and a note of
at most 200 characters.

- **One current row per competence.** A row is **current** until a
  [renewal](#renewal) supersedes it, and a person holds at most one current row of a
  competence (the partial unique index `ux_person_competences_current`). Declaring or
  adding one the person holds is `competence_held` (409): change or renew that row
  instead.
- **Declared or verified.** A row is **declared** until it is
  [verified](#verification). A declared skill counts, with its badge wherever it is
  shown; a declared certificate is not held outside the person's tab — the
  [Workforce contract](#the-workforce-contract) leaves it out.
- **Valid on a day** D: the current row, its `validFrom` on or before D and its
  `validTo` absent or on or after D. A superseded row is valid on no day, whatever its
  dates.
- **Expiring and expired.** Every current row answers `expiring` — its `validTo` today
  or later and no more than its competence's `reminderLeadDays` away; false without a
  lead — and `expired` — its `validTo` before today. A superseded row is neither.
  Today is the [business time zone's](#the-business-time-zone).

**Who writes what.** The person is **Self** on their own rows whatever else they hold,
so HR and an Approver declare their own competences like anyone, and never verify
them:

| Who | Adds | Changes | Renews | Removes | Verifies |
| --- | --- | --- | --- | --- | --- |
| The person | declares: `source` `self`, declared | their own declared row | their own current row; the new row declared | their own declared row, with the evidence they uploaded | never: `own_record` (403) |
| A manager today, `staff:approve` | adds: `source` `manager`, verified at once | any current row | any current row; the new row verified at once | any current row | any current row |
| `staff:manage` | adds: `source` `hr`, verified at once | any current row | any current row; the new row verified at once | any current row, or the whole chain | any current row |

The person's change or removal of a **verified** row is `verified_row` (409): it is
theirs to read and renew, or to ask their manager or HR about. A manager's, an
Approver's or HR's entry is verified at once, on the basis `observed`, the caller its
verifier: they vouch for it by entering it (plan reading 5). `staff:personal-data`
without `staff:manage` reads every row with its detail and files
[evidence](#evidence), and writes no row (the uniform 403); so does a colleague with
`staff:view`, who reads the rows without their detail. A person the caller may not see
is the bare 404 an unknown id is, and a write on an [erased](#the-erased-state) person
is `person_erased` (409), judged under the person's lock.

**Reading.** `GET /people/{id}/competences` answers the current rows in the
catalogue's order; `history=true` answers every row the person has had, superseded
ones included, newest first. Two [field groups](#field-groups-and-audiences) shape each
row:

- **Competence** — `competence` (`{id, key, names, kind, hasLevel, categoryId,
  archived}`), `level`, `validFrom`, `validTo`, `verified`, `superseded`, `expiring`,
  `expired`, `declaredAt` (when the row was entered) and `revision`: everyone who sees
  the person — the person, a manager of theirs today, a colleague with `staff:view`
  while the person is active, the Approver, `staff:manage` and `staff:personal-data`.
- **Competence detail** — `issuedOn`, `issuer`, `reference`, `evidence`
  (`{documentId, fileName, contentType, sizeBytes}`), `source`, `verifiedAt`,
  `verifiedBy` (the verifier's display name, read from the user directory after the
  transaction; "Unknown user" for one it no longer knows), `verifiedBasis` and
  `verifiedNote`: all of them but the colleague. Absent, never null, for a colleague.

The worker's two notified stamps are in no response.

**Writing.**

- **`POST /people/{id}/competences`** (201) declares or adds: `competenceId`, `level`,
  `issuedOn`, `validFrom`, `validTo`, `issuer` and `reference`. The competence must be
  neither hidden nor archived — an unknown one likewise — (`competence_unavailable`); on
  a competence with a level, `level` is required (`level_required`), on one without it
  is refused (`level_not_allowed`), both 400 on `level`, and out of 0–4 a plain field
  error. `validFrom` left out is `issuedOn`, else today; a `validTo` before `validFrom`
  is a 400 on `validTo`, and so is any `validTo` on a skill (`skill_no_validity`): a
  skill is a proficiency, valid until no day. The server stores the `validTo` it is sent: the app proposes
  the issue date plus the competence's validity, and the person may change it. The
  refusals come in the order that tells a caller least: the person they may not see
  (404), the uniform 403, the fields (400), then the state of things (409). Recorded as
  `competence_declared`.
- **`PUT /person-competences/{id}`** (`If-Match`) replaces a current row's `level`,
  `issuedOn`, `validFrom`, `validTo`, `issuer` and `reference` by the same rules: a
  field left out is cleared, but `validFrom`, which keeps the row's own. A new
  `validTo` clears the worker's two stamps, so a corrected end is mailed again when it
  comes due. A manager, the Approver or HR changing a verified row's level verifies
  that level: `verifiedAt` and the verifier become theirs, the basis and the note kept.
  Recorded as `competence_changed` with the fields that changed; a change that changes
  nothing records nothing.
- A **superseded** row is written by nobody: a change, a verification, a renewal, a
  removal or evidence on it is `superseded` (409).

### Verification

`POST /person-competences/{id}/verify` (S4) takes the `level` agreed (on a competence
with a level, required — `level_required` — and on one without refused), the `basis` —
`evidence`, the verifier saw it, or `observed`, seen in the work — and an optional
`note` of at most 200 characters, about what the verification rests on.

- **Who.** A manager of the person today by the
  [one manager rule](#managers-and-the-org-view), `staff:approve` or `staff:manage` —
  **never the person**: the person verifying their own row is `own_record` (403, with
  its problem), judged before anything else, whatever they hold, with none of the
  absence decisions' sole-holder exception. Anyone else who sees the person gets the
  uniform 403. A one-person HR's own rows stay declared until another verifier exists.
- **What it stamps.** The level agreed becomes the row's, lower or higher than the
  one declared; `verifiedAt`, the verifier, the basis and the note are set. A verified
  row verified again moves the stamp.
- **Disagreeing** is removing: a manager who does not agree with a declaration
  [removes](#removal) it — there is no rejected state for the person to clear.
- **The event**, `competence_verified`, names the level before and after and the
  basis — never the note, which is the row's alone.

Each person carries `canVerifyCompetence` among its `capabilities`: this rule for the
caller on the person — `(a manager of theirs today ∨ staff:approve ∨ staff:manage) ∧
¬self` — derived from the audience, with no read of its own.

### Renewal

`POST /person-competences/{id}/renew` (S5; 201, the new row) writes a new current row
and supersedes the old one in the same transaction: the old row then names the new one
(`superseded_by_id`), keeping its dates and its evidence as they were. A certificate
is renewed, never its `validTo` edited, so history keeps the old.

- The body is `issuedOn`, `validFrom`, `validTo`, `issuer` and `reference`. The new
  row keeps the old row's level, carries no evidence until some is
  [filed](#evidence), and starts unstamped by the worker.
- `validTo` left out, with an `issuedOn`, is the issue date plus the competence's
  `validityMonths` when it has one; `validFrom` left out is the issue date, else
  today. A certificate without a validity is renewed the same way; a **skill** is not
  renewed (`skill_no_validity`, 409) — its level is changed instead.
- The person renews their own current row, declared or verified, and the new row is
  declared (`source` `self`); a manager, the Approver and HR renew any current row, and
  the new row is verified at once on the basis `observed`.
- The renewed row is no longer current: it raises no item and is never mailed again.
  The chain is read with `GET /people/{id}/competences?history=true`.
- Recorded as `competence_renewed`, naming the new row, with `validTo` from the old
  row's to the new one's and the level it carries.

### Removal

`DELETE /person-competences/{id}` (204) removes a current row, recorded as
`competence_removed`.

- **The person** removes their **own declared** row; a verified one is `verified_row`
  (409). The evidence **they uploaded** for it goes with it (reading 4): its document
  is deleted and its object queued in the [deletion ledger](#the-deletion-ledger) in
  the same transaction, with the document's own `document_removed` event, and removed
  once that has committed, as a [document's delete](#reading-changing-and-removing)
  is. Evidence someone else filed — HR's — stays in the archive.
- **A manager today, the Approver and HR** remove any current row. Its evidence stays
  in the archive, for HR to delete as any document.
- **Removing a renewal undoes it.** Removed without `chain`, a current row that renewed
  an earlier one makes that row current again, its dates, stamps and evidence as they
  were; the event names it (`restored`).
- **`chain=true`** (`staff:manage` alone; the uniform 403 otherwise) removes every row
  the person has had for the competence, the superseded ones with the current. A
  superseded row goes only so: removed on its own it is `superseded` (409).

### Evidence

`POST /person-competences/{id}/evidence` (S6; 200, the row) files a row's evidence in
the person's [archive](#documents) and names it on the row. It is the **one write the
person makes into their own archive**: allowed to the row's person on their own
current row, declared or verified, and to `staff:manage` and `staff:personal-data` as
any document. A manager or an Approver without either gets the uniform 403.

- **The file** is the multipart part `file`, alone, judged as
  [a document's](#filing-a-document) is: a PDF, JPEG, PNG or HEIC of at most 10 MiB
  whose own bytes decide its type — `document_type_unsupported` and
  `document_too_large`, 400 on `file`. The router caps the request at 10 MiB + 64 KiB,
  and a body over the cap is answered `document_too_large` (400) too.
- **Where it goes.** Under the seeded **`certificate`** category (class D, not
  restricted), with no employment and the caller as its uploader, recorded as the
  document's `document_added` and the row's `competence_evidence_added`.
- **The order.** A row the caller may not see is the 404, then the uniform 403, then
  the file (400). The object is written **before** the transaction — a store that
  cannot write is 503 `storage_unavailable`, with nothing recorded — and under the
  person's lock a superseded row (`superseded`) and a person with 200 documents
  (`document_count_exceeded`) are 409s. Whatever refuses or fails after the write
  removes the object again.
- **A second upload** names the new document on the row and leaves the earlier one in
  the archive.
- **A deleted document.** HR deleting the evidence as a document clears the row's
  reference; a verified row stays verified — the verification was the verifier's act,
  the document a trace.
- **Reading it.** `GET /person-competences/{id}/evidence` streams the file to everyone
  who reads the row's detail group — the person, a manager of theirs today, the
  Approver, `staff:manage` and `staff:personal-data` — though a manager and the Approver
  do not read the person's [documents](#documents) otherwise; it carries the headers a
  document's download does. A row without evidence, or one the caller does not read
  the detail of, is the bare 404, and so is evidence HR has since moved into a
  restricted category, to a reader without the health group; an object the store
  cannot read is 503 `storage_unavailable`. The person, HR and `staff:personal-data`
  also read it among the documents (`GET /documents/{id}`).
- **The rate limit** is the uploads' (`StaffDocumentUpload`, 600 an hour per client
  address), 429 past it.

### What the person wants

Delivery B's one-to-one (S8–S11, amendments 16–22) adds what the person wants — three
lists, each the person's own: their [interests](#interests) in competences, their
[preferences](#preferences) and their [availability declaration](#the-availability-declaration).
The manager reads them before the conversation and stamps them **Seen** in it; the two
then record the [review](#the-review).

- **Who reads.** The Wishes group's audiences: the person, a manager of theirs today by
  the [one manager rule](#managers-and-the-org-view), `staff:manage` and
  `staff:personal-data`. `GET /people/{id}/interests`, `/preferences` and
  `/availability` answer anyone else — a colleague, the Approver who is neither manager
  nor HR, a caller who does not see the person — the bare 404 an unknown id gets
  (amendment 16: the Approver reads no wishes). Each list is read in one snapshot.
- **Who writes.** The person alone, through their self-service link: `PUT
  /people/{id}/interests`, `/preferences` and `/availability` each replace one list
  whole, an empty list (`[]`) removing it; a body without the list, or with it null, is
  a field error on the list's name and removes nothing. Anyone else who sees the person — their manager,
  HR, the Approver, a colleague — gets the uniform 403; a caller who does not see them
  the bare 404. Who may is judged in a snapshot first, before the body's rules, so a
  caller who may not write learns nothing of them, and again under the person's lock
  with the caller's link read anew. An erased person is `person_erased` (409).
- **Never partly applied.** A list is judged whole and refused with a field error per
  row, named by its place in the list (`interests[3].competenceId`,
  `declarations[0].fromDate`); nothing is written while one stands. The three request
  schemas are closed at the top and on each row: a property a row does not name is a
  field error on it (`interests[0].level`, `preferences[0].note`). No refusal code is
  new (amendment 22) — every rule is a plain field error but `competence_unavailable`
  on an interest.
- **A replace is a diff, not a wipe** (amendment 18). A row sent equal to one on file —
  an interest by its competence, a preference by its kind, value and weight, a
  declaration row by every field — keeps its row and its Seen stamp; a new or changed
  row is written without a stamp; a row on file no longer sent is deleted. "Changed
  since the one-to-one" is simply "not yet stamped". What depends on the catalogue — a
  hidden or archived competence, an archived location — is judged on the new and
  changed rows alone, under the lock; a row on file is kept as it is.
- **No `If-Match` and no revision** (amendment 20). The person is the lists' only
  writer and the lists are not under the person's revision: a replace moves no
  revision, and the last write wins.
- **The event.** A replace that adds, changes or removes a row records one event —
  `interests_changed`, `preferences_changed` or `availability_changed` — carrying
  counts or kinds, never a value ([the competence events](#the-competence-events)); a
  replace that changes nothing records nothing.

**Seen.** `POST /people/{id}/wishes/seen` (no body) stamps, as the caller and now,
every row of the three lists that has no stamp **and every current competence row of
the person's that has none** (amendment 17), so a declaration the manager has read but
not yet verified is acknowledged too. A superseded competence row is left alone, and
the stamp moves no row's revision. The answer counts what was stamped —
`{seenAt, interests, preferences, declarations, competences}`, zeros everywhere when
everything had been seen — and a Seen that stamped anything records `wishes_seen`
with the total; one that stamped nothing records nothing.

- **Who.** The person's manager today or `staff:manage` — never the person, and never
  `staff:approve` alone (amendment 16). A caller who does not read the person's wishes
  (a colleague, the Approver, a stranger, an unknown id) gets the bare 404; one who
  reads them but does not stamp — the person, `staff:personal-data` — the uniform 403.
  An erased person is `person_erased` (409).
- **Where the stamp shows.** Each interest, preference and declaration row carries
  `seenAt`, absent until it is seen; a competence row carries it in the Competence
  detail group. The stamp's user (`seen_by_user_id`) is stored and not answered. A new
  or changed wish row starts without one. A competence row loses its stamp when the
  person changes their declared row or uploads its evidence — a change that changes
  nothing keeps it — and keeps it through a verifier's or HR's edit or upload; a
  renewal's new row starts without one.

Each person carries two [capabilities](#field-groups-and-audiences) for this, so the
apps carry no audience logic: `canReadWishes` (the Wishes group) and
`canStampWishesSeen` (`(manager ∨ staff:manage) ∧ ¬self`).

### Interests

`staff.person_interests` (S8): one row per person and competence, at most **20**
(`interests`, 400, past them). The body is `{interests: [{competenceId}]}`; a
competence named twice is refused on the later index (`interests[1].competenceId`).

- **Its meaning is derived**, never stored: each interest answers `held` — true when
  the person holds a current row of the competence, declared or verified, so they want
  to **use it more**; false when they do not, so they want to **learn it**. Declaring
  or removing the competence turns the meaning with no write to the interest.
- **A new interest names an offered competence**: one unknown, hidden or archived is
  `competence_unavailable` (400) on its `competenceId`, as a new competence row is
  ([the catalogue](#the-catalogue)). The competence is shared under the person's lock
  as a person's competence row shares it.
- **An interest already on file is kept** when its competence is later hidden or
  archived: sent again, it keeps its row and its stamp, and is answered with the
  competence's `archived` flag set.
- **The answer** — of the list and of a replace — is `{interests: [...]}`, each row
  `{competenceId, competence, held, seenAt}`, `competence` the summary a competence row
  carries (`id`, `key`, `names`, `kind`, `hasLevel`, `categoryId`, `archived`), in the
  catalogue's order: by category, then by competence.

### Preferences

`staff.person_preferences` (S9): one row per person and **kind**, at most **seven** —
one of each (`preferences`, 400, past them). Each row is `{kind, value, weight}`; a kind
sent twice is refused on the later index (`preferences[1].kind`), an unknown kind on
`preferences[i].kind`. The kinds reference Staff's own entities or nothing:

| `kind` | `value` | Rule |
| --- | --- | --- |
| `weekdays` | `{"days": [1..7]}` | ISO weekdays, 1 (Monday) to 7 (Sunday): 1 to 7 of them, each once. |
| `time_of_day` | `{"parts": [...]}` | 1 to 4 of `morning`, `day`, `evening`, `night`, each once. |
| `location` | `{"locationIds": [...]}` | 1 to 10 [locations](#the-structure), each once, existing and not archived on a new or changed row. |
| `travel` | `{"extent": "..."}` | `none`, `regional`, `national` or `international`. |
| `remote` | `{"share": "..."}` | `never`, `some`, `mostly` or `only`. |
| `max_consecutive_days` | `{"days": n}` | A whole number, 1 to 14. |
| `max_hours_per_week` | `{"hours": n}` | A whole number, 1 to 60. |

- **The value is decoded strictly** by its kind's shape: a key the kind does not take
  (`{"extent": "none", "reason": "…"}`), a value out of its range, a repeat and a
  fraction are each a field error on `preferences[i].value`. An unknown or archived
  location is a field error on the value too, judged on a new or changed preference
  alone: a location preference on file is kept when a location it names is later
  archived.
- **Stored canonical.** The value is re-encoded from the kind's own shape, the days and
  the locations in ascending order and the parts in the order morning, day, evening,
  night — so the same preference sent in another order is the same row and keeps its
  stamp. It is answered as stored.
- **The weight** is 1 (*nice to have*), 2 (*matters*) or 3 (*important*) — a field
  error on `preferences[i].weight` otherwise.
- **No free text.** No kind carries text and no row a note: a reason for "no night
  shifts" is a health fact in a sentence (S9). Customer and project wishes, and *do not
  schedule with*, are Planning's.
- **Kept by kind.** The same kind with the same canonical value and weight keeps its row
  and its stamp; another value or weight changes the row and clears its stamp.
- **The answer** is `{preferences: [...]}`, each row `{kind, value, weight, seenAt}`,
  by kind.

### The availability declaration

`staff.availability_declarations` (S10): at most **50** rows (`declarations`, 400, past
them), each a `kind` — `available`, `unavailable` or `preferred` — and its days and
hours:

- **Recurring**: a `weekday`, 1 (Monday) to 7 (Sunday), with optional `fromDate` and
  `toDate` bounding it.
- **Dated**: no weekday and a `fromDate`, required (`declarations[i].fromDate`
  otherwise), with an optional `toDate` — the one day when it is absent.
- `toDate` is on or after `fromDate` (`declarations[i].toDate`).
- **The hours**: `startMinute` and `endMinute`, minutes of the day from 0 to 1440, both
  or neither — the whole day without them — the end after the start; a missing one is
  refused on its own field, a bad end on `declarations[i].endMinute`.
- **A row twice** — equal to an earlier row in every field — is refused on its own index
  (`declarations[1]`).

A row is kept by every field: sent equal to one on file, it keeps its row and its
stamp; anything else is a new row. The answer is `{declarations: [...]}`, the recurring
rows by weekday first, then the dated ones by day, each by its start.

**Data only.** Nothing in Staff enforces, sums or checks the declaration against
absence or the contract's hours; whether a row is hard or soft, and how it meets the
rest, is Planning's to decide.

### The review

`staff.competence_reviews` (S11) records that the person and whoever may verify their
competences went through the Competence tab together: a day and the reviewer — no
notes, no goals (development goals are a later HR module's).

- **Recording one.** `POST /people/{id}/competence-reviews` takes an optional
  `{reviewedOn}` — the body may be left out — and answers 201 with
  `{id, reviewedOn, reviewer, recordedAt}`. `reviewedOn` left out is today in the
  [business time zone](#the-business-time-zone); an earlier day is accepted, a later
  one is a plain field error on `reviewedOn` (400). The request schema is closed: a
  property it does not take — a note, say — is a field error on it (400), since a
  review carries no notes and no goals (S11).
- **Who.** Whoever may [verify](#verification) the person's competences: a manager of
  theirs today, `staff:approve` or `staff:manage` — never the person, who gets
  `own_record` (403, with its problem). The refusals come in the order that tells a
  caller least: a caller outside the Reviews group — a colleague, a caller who does not
  see the person, an unknown id — gets the bare 404; one inside it who verifies nothing
  (`staff:personal-data`) the uniform 403; then the day (400); then, under the person's
  lock, an erased person (`person_erased`, 409).
- **The reviewer** is the caller, stored as their user and — when they have one — their
  linked person (`reviewer_person_id`), which the delete of that person as created in
  error clears; the review stays, the reviewed person's row. It is answered as
  `reviewer`, the name the user directory gives, read after the transaction ("Unknown
  user" when it no longer knows them).
- **Reading them.** `GET /people/{id}/competence-reviews` lists the person's reviews,
  newest first by `reviewedOn`, to the Reviews group: the person, a manager of theirs
  today, `staff:approve`, `staff:manage` and `staff:personal-data`; anyone else gets
  the bare 404.
- **`lastCompetenceReview`** — `{reviewedOn, reviewer}`, the latest by `reviewedOn` —
  is on the person (`GET /people/{id}`, `GET /me`) for the same audiences, and absent
  when the person was never reviewed or the caller does not read the group.
- **The event**, `competence_reviewed`, carries the day.

**Overdue.** `competenceReviewIntervalMonths` in the [settings](#settings) — 12 by
default, 0 to 60 — makes a person **review-overdue**, raising the
[attention item](#the-competence-items) `competenceReviewOverdue`:

- Only a person **in employment today** is judged, and never an erased one.
- **The due day** is the interval in months after the later of the person's latest
  review (`reviewed_on`) and the start of their **current unbroken employment run** —
  the employment in force today and every one it continues without a gap, an
  employment in force the day before the next began counting as one run with it (the
  care days' rule). So a new hire is due the interval after their start, never overdue
  on day one, and a review before a gap in employment counts for nothing.
- **Months are calendar months**: the same day of the month, or that month's last day
  when it has none (31 August plus six months is the last day of February).
- **Overdue** is the day after the due day: the item is raised while today is after it,
  and a review recorded clears it.
- **An interval of 0** raises no item for anyone.

### Profiles and gaps

`staff.competence_profiles` and `staff.competence_profile_items` are the profiles
catalogue (S12): a **competence profile** is what a position needs — a `key`, `names`
`{en, nb}` and a list of **items**, each a competence the person must hold, with an
optional minimum level and whether their row must be valid. Like the competence
catalogue it is about no person: no retention class, never touched by an erase.

- **`GET /competence-profiles`** lists every profile by key, archived ones included
  (with `archivedAt`) so Settings restores one, each with its `revision` and its items
  in the catalogue's order — `{competenceId, competence, minLevel, mustBeValid}`,
  `competence` the summary a competence row carries. Every caller with `staff:access`
  reads them: the catalogue holds no personal data (reading 36).
- **`POST /competence-profiles`** (`staff:manage`; 201) adds one: a `key` of 1–40
  characters `[a-z0-9-]`, unique (another profile's is a 400 on `key`), `names` (each
  1–100 characters) and `items`, required, an empty list allowed. The key is **fixed
  from then on** (reading 27).
- **`PUT /competence-profiles/{id}`** (`staff:manage`, `If-Match`) replaces `names`,
  `archived` and `items`, all three required. The body is closed: a `key` is a 400
  naming it. An unknown id is the bare 404, a revision that has moved on
  `revision_conflict` (409). The items are replaced whole as a diff: an item on a
  competence still named keeps its row, changed in place when its level or validity
  rule changed, and a competence no longer named loses its item.
- **The items** (reading 28). At most **50** (`items`, 400, past them). A competence
  named twice is refused on the later index (`items[1].competenceId`). An unknown,
  hidden or archived competence on a **new or changed** item is
  `competence_unavailable` (400) on `items[i].competenceId`, as on a new competence row;
  an item sent unchanged — the same competence, `minLevel` and `mustBeValid` — that
  names a competence hidden or archived since is kept, as an interest on file is, and
  reported on the gap list. `minLevel` is 0–4 (a plain field error otherwise) and only on
  a competence with a level: on one without it is `level_not_allowed` on
  `items[i].minLevel`. `mustBeValid` left out is true. Each item is closed: a property it
  does not take is a field error on it (`items[0].level`).
- **In use.** An item with a `minLevel` counts as the competence being in use: changing
  a company competence's `hasLevel` while one names it is `competence_in_use` (409), so
  no item's level is orphaned.
- **Archiving** is never refused, whoever the profile is assigned to and whichever
  department has it as its default: an archived profile **reaches nobody** until it is
  restored (below).
- **History.** Each write is an installation event, `competence_profile_created` or
  `competence_profile_updated`, naming the profile (`competenceProfileId`) and the fields
  set or changed ([the competence events](#the-competence-events)); a change that changes
  nothing moves the revision and records no event.

**How a profile reaches a person** (reading 25) — two ways, and the person's list says
which:

- **Assigned**: a `staff.person_profiles` row, written by `staff:manage` as a set.
- **Their department's default**: `departments.competence_profile_id` of the
  [department](#the-structure) the terms in force today of the person's **place** name —
  the earliest of their employments in force today, by start and then id, as a
  [group's rule](#the-rule) judges the place. A person with no employment in force today
  has only their assigned profiles; one whose place's terms name no department, or a
  department without a default, has no default.

`GET /people/{id}/competence-profiles` answers `{profiles: [...]}` by key, each
`{profile, assigned, department}`: `profile` the summary `{id, key, names, archived}`,
`assigned` whether it is assigned, and `department` (`{id, code, name}`) when it is the
department's default — one row when both. **An archived profile reaches nobody**
(reading 26): an assigned one is still listed, `archived` true, so HR sees the
assignment, but it is on no gap list and raises no item; a department whose default is
archived has no default while it stays so, and the profile is not listed through it.

**Assigning.** `PUT /people/{id}/competence-profiles` replaces the profiles assigned to
the person with `{profileIds: [...]}`: at most **20** (`profileIds`, 400), a profile
named twice refused on the later index (`profileIds[1]`), an empty list removing them
all.

- **Who.** `staff:manage` alone: anyone else who reads the person's profiles — the
  person, their manager, the Approver, `staff:personal-data` — gets the uniform 403,
  and a caller outside the group the bare 404. Who may is judged in a snapshot first,
  before the body's rules, and again under the person's lock; an erased person is
  `person_erased` (409).
- **What is judged.** A profile already assigned is kept as it is, archived since or
  not — never judged again; a new one must exist and not be archived, a plain field
  error on `profileIds[i]` otherwise (no code of its own).
- **No `If-Match`.** `staff:manage` is the only writer, and the list is not under the
  person's revision: a replace moves no revision, and the last write wins.
- **The answer** is the list as `GET` answers it, the department's default included —
  which is written on the department, never here.
- **The event.** A change of the set records `profiles_assigned` on the person, the
  caller its actor, with the assigned profiles' sorted **keys** before and after
  (reading 34) — catalogue words naming a position, never a value of the person's; a
  replace that changes nothing records nothing.

**The gap list.** `GET /people/{id}/competence-gaps` (S13) judges each item of every
unarchived profile that reaches the person on their **current row** of the item's
competence, today in the [business time zone](#the-business-time-zone), in this order
(reading 23):

1. No current row: `missing`.
2. A **declared certificate**: `declared_only`.
3. `minLevel` set and the row's level below it: `below_level`. A row without a level is
   never below.
4. `mustBeValid` and the row not valid today: `expired` when its `validTo` has passed,
   `missing` when its `validFrom` is not yet reached.
5. `mustBeValid` and its `validTo` within the competence's `reminderLeadDays` of today —
   the lead rule of a row's `expiring`: `expiring`.
6. Otherwise `met`.

With `mustBeValid` false the validity is not judged (reading 24): an expired or
not-yet-valid row is `met` — or `below_level`, or `declared_only` for a certificate —
and `expiring` is never answered; an item asking a valid row of a competence without a
validity is met whenever a row is held. A **declared skill** at the level is met, with
the flag (N3.16): every item carries `declared` (the person's row is unverified) and
`archived` (its competence is archived — the item stays on the profile, is reported and
counts, §9), beside `verified` and, from the row when there is one, `level` and
`validTo`. Each item is `{competenceId, competence, minLevel, mustBeValid, status,
declared, archived, verified, level, validTo}`, each profile `{profile, assigned,
department, items, notMet}`, and the list `{profiles, notMet}`. **Not met** is every
status but `met` and `expiring`: `notMet` counts the items not met per profile and
across every profile, an archived competence's included — the
[dashboard item's](#the-competence-items) count. Nothing about a gap is stored.

**Who reads.** Both GETs are the **Profiles and gaps**
[field group's](#field-groups-and-audiences) (reading 32): the person, a manager of
theirs today by the [one manager rule](#managers-and-the-org-view), `staff:approve`,
`staff:manage` and `staff:personal-data`. Anyone else — the colleague, a caller who does
not see the person, an unknown id — gets the bare 404. Each is read in one snapshot. The
person reads theirs at the top of their own Competence tab ([My page](#my-page)). Each
person carries `canAssignProfiles` (`staff:manage`) among its capabilities, so the apps
carry no audience logic.

### The matrix

`GET /competence-matrix` (S14, reading 29) answers who holds what, valid on a day. It is
`staff:view`'s and `staff:manage`'s, judged in the module; anyone else — the Approver, a
manager, the person — gets the uniform 403. It is the directory with competences: the
Competence group of every active person and nothing more.

- **The people** are every person active **today** and not erased — the colleague
  directory's rule, whatever day is asked — by name, then id.
- **The rows** are each person's current rows **valid on `validOn`** — a date, today in
  the business zone when left out. A row not valid on the day is absent, never shown as
  expired: the gap list says that. Each cell is
  `{competenceId, level, verified, validFrom, validTo}`.
- **The competences selected** are the `competenceId`s given (the parameter repeated),
  else `categoryId`'s, else all of them — hidden and archived ones included where a row
  names them. Given together, `categoryId` and the ids intersect: a competence outside
  the category has no rows, so nobody holds every one given.
- **`verifiedOnly`** drops the declared rows. Without it a declared certificate is
  shown, `verified` false for its badge: only the filter treats it as not held.
- **`minLevel`** (0–4) drops the rows of a competence with a level whose level is below
  it, or that carry none, and leaves a competence without a level untouched.
- **With `competenceId`s given** a person is listed only when, after those filters, they
  hold every one; otherwise every active person is listed, with no cells when they hold
  nothing selected.
- **The answer** is `{validOn, competences, people}`: the columns as competence
  summaries in the catalogue's order — the `competenceId`s given, or, when none is, the
  competences of the cells shown — and the rows as `{personId, name, competences}`.
- **Refused.** A `minLevel` outside 0–4 or an unknown `competenceId` is a plain field
  error on its name (400, *Invalid matrix filter*); a `validOn` that is not a date, like
  any parameter of the wrong type, is the bare 400 of a request that does not decode. An
  unknown `categoryId` answers no rows and no columns.

### The interests overview

`GET /competence-interests` (S14, reading 30) is the gap-to-train view: who wants to
learn or use more of what. It is `staff:manage`'s alone — never the Approver's, who
reads no wishes (amendment 16) — and anyone else gets the uniform 403.

- It groups every [interest](#interests) of a person **active today** and not erased by
  competence, in the catalogue's order, and lists only the competences somebody wants —
  hidden or archived ones included when an interest names them, since an interest on
  file is kept.
- Each entry is `{competence, wantsToLearn, wantsMore}`, the people as
  `{id, displayName, employeeNumber}` by name: under `wantsMore` when they hold a current
  row of the competence, declared or verified, and under `wantsToLearn` when they do
  not — the interest's `held`, derived as on the person's list.
- It names people and competences and nothing else of the wish: no Seen stamp, no
  preference, no declaration.

### The competence worker

`staff-competence` (S7) runs every `STAFF_WORKERS_POLL` (default `1h`, at least `1m`)
unless `STAFF_COMPETENCE_ENABLED=0` — in worker mode, and in api mode with
`WORKERS_IN_PROCESS=1`. Each cycle takes today in the business time zone and reads
every current row with a `validTo` of a person not erased and in employment today,
and judges it:

- `validTo` before today and not yet stamped expired: **expired**;
- otherwise `validTo` no more than its competence's `reminderLeadDays` after today and
  not yet stamped expiring: **expiring**. A competence without a lead never raises it.

Each row so judged is stamped — `expired_notified_at` or `expiring_notified_at` — in a
short transaction of its own, under the row's lock and judged again there, so two
replicas stamp it once; and it is stamped **before** any mail: the stamp is the run's,
not the send's (plan reading 4). Once every transaction is closed, the mails go one
after the other within 30 seconds for the cycle, through the platform's SMTP seam: to
the person and to each of their manager-deciders today — the managers by the one
manager rule whose link names an active user, as for [the absence
mails](#the-absence-mails) — at the work email Staff holds for each, else the private
one, in each one's language (`en` in English, any other in Norwegian). A send that
fails is logged with the recipient's person id and the address redacted, is never
retried and never fails the cycle: the [dashboard item](#the-competence-items),
computed on read, is the durable signal. With a mail driver other than `smtp` the rows
are stamped all the same and nothing is sent. A second cycle finds the stamps and
sends nothing; a renewal supersedes the row, and the new row starts unstamped. Each
cycle logs how many rows it stamped and how many mails it sent and failed — never an
address. Since a cycle runs every poll rather than at a set hour, a row is mailed at
the first poll after it comes due — often just after midnight in the business time
zone. A change of a row's `validTo` clears its stamps, so a corrected end is mailed
again when it comes due. There is no *Run now*: the items need nothing from the
worker.

The mails are plain text, name the competence in the recipient's language and the
last day it is valid, and nothing else of the row — no issuer, reference, level or
evidence:

- **Expiring, to the person**: the competence is valid until the day; when it is
  renewed, add the new one on My page — with a link to My page.
- **Expiring, to a manager**: the competence of the person, named, is valid until the
  day — with a link to the person's Competence tab.
- **Expired, to the person**: the competence was valid until the day and has expired;
  when it is renewed, add the new one — with the link to My page.
- **Expired, to a manager**: the person's competence was valid until the day and has
  expired — with the link to their Competence tab.

### The competence items

Five [attention items](#attention-items) — `competenceDeclared`,
`competenceExpiring`, `competenceExpired`, `competenceReviewOverdue` and
`competenceGap` — are computed on read in the dashboard's snapshot, never waiting for
the worker. Only people in employment today and not erased raise them, and never a
caller's own to the caller: the person sees their own rows and gaps on the tab. The
three row items are one per current row, `id` `<type>/<row id>`; the overdue review and
the gap are one per person, `id` `<type>/<person id>` — the overdue review dated the
day the review fell due ([the review](#the-review)), the gap dated today, since it is
computed on read and has no day of its own (reading 31).

**The gap item**, `competenceGap`, counts in `count` the items not met across the
person's unarchived [profiles](#profiles-and-gaps), assigned or their department's
default, by the gap list's rule; a person with none raises none. It goes to
`staff:manage` for everyone and to a manager today for their reports — never to
`staff:approve` alone. It is read — the assignments, the departments' defaults, then the
profiles, their items and the current rows of the people concerned, stopping as soon as
no profile reaches anyone — only for `staff:manage` or a caller who manages someone
today, so the Approver's and anyone else's dashboard reads nothing more.

Each has `entityId` the person and `title` the person's name; the host opens the
person's Competence tab. Their audiences and triggers are in the
[attention table](#attention-items).

### The Workforce contract

`contracts.Workforce` is Staff's register read by another module (S15, and the
planning design's S8): Staff is its sole provider (the `Workforce` slot; nil when Staff
is disabled), and [Planning](/en/reference/planning/#contracts-consumed) its consumer,
which reads it through `Deps.Workforce` and never Staff's tables. Staff is its own
gatekeeper ([rule 12](/en/contributing/module-boundaries/)): the principal is the
request's caller, or the user `contracts.AsUser` names; a context with neither answers
nothing.

**The Colleague rule, widened.** `HeldCompetences`, `People`, `Capacity` and
`PreferenceFits` need **`staff:view`** — `staff:manage` alone answers nothing — and answer the people **in
employment on today or on a later day of the window asked**: next month's starter is
answered, yesterday's leaver is not, whatever window is asked. For `HeldCompetences`
the window is its one date `on`: the people in employment on `on` when it is today or
later, and on today for a past `on`. This is wider than the directory's "active today"
([field groups and audiences](#field-groups-and-audiences)), so a planner sees who
starts next month; it never reaches a person who has left.

- **`Competences(ctx)`** is the catalogue: every competence not archived, hidden ones
  included, each with its id, key, names, its category's key, its kind, `HasLevel`,
  `ValidityMonths` and `Hidden` — for a principal holding `staff:view` or
  `staff:manage`; nothing, never an error, for anyone else.
- **`HeldCompetences(ctx, on, personIDs)`** answers, for each person id, the
  competences they hold valid on `on` (a calendar date at midnight UTC): the current
  row, `validFrom` on or before it and `validTo` absent or on or after it, as
  `{CompetenceID, Key, Kind, Level, Verified, ValidFrom, ValidTo}`, under the widened
  Colleague rule above. A person not in employment on the date judged, and one who
  holds nothing on `on`, is absent from the map.
- **`People(ctx, from, to)`** answers the people in employment on today or a later day
  of `from`..`to` (civil dates, inclusive), as the caller may see them: the register's
  Work group, never a private field — each `{ID, DisplayName, EmployeeNumber,
  DepartmentID, DepartmentName, UserID, Employed}`. The department is the person's
  place today, or on their first day in employment inside the window when that is
  later. `Employed` is the days inside the window the person is in employment, merged
  into non-overlapping spans. **`UserID`** is the user the person's
  [self-service link](#the-self-service-link) names in a state that gives that user
  Self, nil otherwise — for mailing the person through `UserDirectory.Notify`, and for
  nothing else.
- **`Capacity(ctx, from, to, personIDs)`** answers, per person, `Segments` — the hours a
  week in force over the window, `{From, To, HoursPerWeek}`, consecutive,
  non-overlapping and inside employment, `HoursPerWeek` the full position's hours × the
  FTE of the [terms](#terms) in force, summed over concurrent employments — and
  `Away`, whole-day spans: the approved and registered absences of whole or part days,
  coalesced and clipped to the window, with **no share, no hours, no type and no
  status** — the same as [the calendar](#the-calendar) shows a colleague. Never a terms
  row. A person the caller may not see is absent from the map.
- **The bound.** `People` and `Capacity` take a window from no earlier than the Monday
  thirteen weeks before today to no later than three years after today, in the business
  zone; outside it they answer `contracts.ErrWindowOutOfBounds`, so a past window never
  lists every leaver.
- **`Self(ctx)`** answers the caller's own person — the one whose self-service link
  names the caller in a state that gives them Self, under Staff's own Self rule, as
  [`GET /me`](#my-page) finds them — as a `PersonEntry` with `Employed` empty, or none
  (`nil, nil`). It needs `staff:access` and nothing more: the person's own record is
  theirs.
- **`DirectoryAccount(ctx)`** answers the caller's own account in an external directory,
  for the calendar's [Outlook sync](/en/reference/calendar/#outlook-sync) (its design
  O3): `{Provider, ExternalID, Enabled}` — the [provider link](#the-provider-link) of the
  person whose identity link names the principal (a request's caller, or the user
  `contracts.AsUser` names, so the calendar's worker asks with no request), with an
  external id and in state `enabled` or `disabled`; `Enabled` is the state. The person is
  found under the predicate `PersonForUser` applies — the identity link in any state the
  worker left it, a disabled one included, so a leaver's copies can still be cleaned up,
  but never one it blocked as someone else's account (`mismatch`, `account_not_managed`);
  a person with two provider links answers the oldest. It is the principal's own and
  nothing more: it needs no Staff key, never answers for another user, and answers
  `nil, nil` for a context with no principal, a user with no person, a person with no
  provider link, no external id, or a link `deleted`, `preparing`, `failed` or
  `blocked`. The calendar takes only an `Enabled` account whose `Provider` matches the
  mailbox provider's own key — a disabled one, a leaver's, is no account — and the
  provider checks again that it manages the account
  ([Microsoft Entra ID](/en/reference/entra/#the-account-check)).
- **Derived figures.** `HoursPerWeek` is a figure a colleague can nearly compute from
  the FTE they read and the scheme's default (planning reading 2); a day away is
  wholly away, since the share is a fact Staff keeps from colleagues (planning
  reading 1).
- **Held** means verified for a certificate: a declared certificate is not held. A
  declared skill is held, with `Verified` false. An expired certificate is absent on a
  date after its end, though the dashboard still shows it.
- The consumer calls with the request's context, never inside a transaction of its
  own. What a person **wants** is never read as such: it reaches Planning only as the
  fit Staff scores (`PreferenceFits`, below).

**The matching contract's Staff side** (S15, reading 36; planning design S17.6 and
reading 6). What a person wants reaches a planner as a **fit** for a proposed booking —
a number and the kinds that lowered it — never the wishes themselves, so Planning
weighs preferences nobody but the person's manager and HR read. The method joined the
interface with Planning's delivery B, which matches candidates to a request, as a
**batch** over every candidate at once:

```go
PreferenceFits(ctx context.Context, personIDs []int64, b Booking) (map[int64]Fit, error)

type Booking struct {
    From, To        time.Time // civil dates at midnight UTC, as every contract carries them
    Weekdays        []int     // ISO 1 (Monday) to 7 (Sunday)
    Parts           []string  // morning, day, evening, night
    LocationID      *int64
    HoursPerWeek    *float64
    ConsecutiveDays *int
    Travel          string    // none, regional, national, international
    Remote          string    // never, some, mostly, only
}

type Fit struct {
    Score   int      // 0 to 100
    Lowered []string // the preference kinds the booking contradicts, in the catalogue's order
}
```

- **The gate** is the Colleague rule widened, as for `People`: `staff:view`, and the
  person in employment on today or a later day of `From`..`To`; a person the caller may
  not see is absent from the map, and a period outside [the bound](#the-workforce-contract)
  answers `contracts.ErrWindowOutOfBounds`. Anyone else gets nothing, never an error.
- **The rule.** The score starts at 100; each of the person's
  [preferences](#preferences) the booking contradicts lowers it by its weight × 10 — 10,
  20 or 30 — floored at 0, and names its kind in `Lowered`, never its value. A person
  with no preference scores 100.
- **What contradicts**, per kind. `weekdays`: a weekday of the booking the person does
  not prefer. `time_of_day`: a part of the day the booking has that the person does not
  prefer. `location`: the booking's location not among the person's. `travel`: the
  booking's extent beyond the person's (`none` < `regional` < `national` <
  `international`). `remote`: the booking's share other than the person's.
  `max_consecutive_days` and `max_hours_per_week`: the booking's figure above the
  person's. **A facet of the booking left nil or empty contradicts nothing**, so a
  Planning request, which carries only its dates and its hours a week, is judged on
  `max_hours_per_week` alone — and on **this booking's hours alone**, never on the
  person's total: the total is Planning's figure (its capacity calculation), not
  Staff's. A stored value that does not decode contradicts nothing either; the fit
  never fails on a row.
- **The availability declaration is not scored**: whether a row is hard or soft, and
  how it meets the booking, is Planning's to enforce (S10).

### The exports and the erase

- Both [personal-data exports](#the-personal-data-exports) carry `competences`: every
  row the person has had, superseded ones included, newest first, with the detail —
  the evidence's details (the file is read through `GET /documents/{id}`), the
  verifier's name, the basis and the note.
- The [D erase](#what-an-erase-does) deletes the person's rows, renewal chains with
  them: every column is class D ([the classes](#the-classes-column-by-column)). The
  evidence is a `certificate` document, class D, deleted with the person's other D
  documents. No erase kind is new: both count under `D`. The catalogue is never
  touched.
- Both exports carry, too, `interests`, `preferences` and `availability` — every row of
  the three lists, with its Seen stamp — to an audience that reads the Wishes group, and
  `competenceReviews`, newest first with each reviewer's name, to one that reads the
  Reviews group; each is an empty list, never absent, to anyone else.
- The four tables of [what the person wants](#what-the-person-wants) and
  [the review](#the-review) — `person_interests`, `person_preferences`,
  `availability_declarations` and `competence_reviews` — are class D in every column,
  so the D erase deletes the person's rows whole, under kind `D`; the Seen stamp on a
  competence row is class D with the row.
- Both exports carry `profileAssignments` (reading 33): the
  [profiles](#profiles-and-gaps) that reach the person, assigned or their department's
  default, as `GET /people/{id}/competence-profiles` answers them, to an audience that
  reads the Profiles and gaps group — the person's own export included, since the tab
  shows them their profiles — and an empty list, never absent, to anyone else.
- `person_profiles` is class D in every column: the D erase deletes the person's
  assignments whole, under kind `D`. The profiles, their items and the departments'
  defaults are about no person and are never touched.
- `DELETE /people/{id}` refuses a person with a competence row (`person_in_use`, "has
  competences"); the delete of a person created in error deletes their interests,
  preferences, availability declaration, reviews and assigned profiles, whose keys to
  the person do not cascade, and clears the reviewer's person on any review they recorded
  of someone else. Assigned profiles do not make a person in use.
- The register's CSV import and export and the payroll export carry no competence and
  no wish.

### The competence events

A person's row's events name the row (`competenceId`) and carry, beside the competence's
key, the fields that changed, before and after:

| Kind | Payload |
| --- | --- |
| `competence_declared` | `competenceId`; `fields`: `competenceKey`, `source` and — on a competence with a level — `level`, each as `to`. Both the person's declaration and a manager's or HR's addition; `source` tells them apart. |
| `competence_changed` | `competenceId`; `fields`: `competenceKey` and each of `level`, `issuedOn`, `validFrom`, `validTo`, `issuer` and `reference` that changed, before and after. |
| `competence_verified` | `competenceId`; `fields`: `competenceKey`, `basis` and — on a competence with a level — `level`, from the row's to the level verified, both sides even when equal. Never the note. |
| `competence_renewed` | `competenceId`, the new row; `fields`: `competenceKey`, `validTo` from the old row's to the new one's (either side absent when empty) and — on a competence with a level — `level` as `to`. |
| `competence_removed` | `competenceId`, the row removed; `fields`: `competenceKey`, `chain` and — when the removal undid a renewal — `restored`, the row current again. |
| `competence_evidence_added` | `competenceId`; `fields`: `competenceKey` and `documentId`, beside the document's own `document_added`. |
| `competence_created`, `competence_updated` | Installation-level (no person): `competenceId`, the catalogue's competence, and `fields`, each field set or changed. |
| `interests_changed` | `{"fields": {"interests": {"from": n, "to": n}}}` — the count of interests before and after the replace. |
| `preferences_changed` | `{"fields": {"preferences": {"to": ["kind", …]}}}` — the kinds added, changed or removed, sorted; never a value or a weight. |
| `availability_changed` | `{"fields": {"declarations": {"from": n, "to": n}}}` — the count of rows before and after. |
| `wishes_seen` | `{"fields": {"seen": {"to": n}}}` — the rows the [Seen](#what-the-person-wants) stamped, the four lists together; the actor is the manager or HR who stamped. |
| `competence_reviewed` | `{"fields": {"reviewedOn": {"to": "YYYY-MM-DD"}}}` — the [review's](#the-review) day; the actor is the reviewer. |
| `profiles_assigned` | `{"fields": {"profiles": {"from": ["key", …], "to": ["key", …]}}}` — the keys of the profiles [assigned](#profiles-and-gaps) before and after, sorted, an empty side left out; the actor is HR. |
| `competence_profile_created`, `competence_profile_updated` | Installation-level (no person): `competenceProfileId`, the profile, and `fields` over `key`, `names`, `archived` and `items` — the items as `[{competenceId, minLevel, mustBeValid}]` in the catalogue's order — each field set or changed, before and after. |

The one-to-one's five kinds carry counts, kinds and a day, never a value (amendment
21): every field is class D, `interests`, `preferences`, `declarations` and `seen` in
the Wishes group and `reviewedOn` in the Reviews group, so an audience outside a group
would read `"changed"`; every reader of the history — the person, their manager, HR —
reads both. `profiles_assigned` carries the profiles' keys — catalogue words naming a
position, never a value of the person's (reading 34) — and its field `profiles` is
class D, in the Profiles and gaps group.

Each person event carries `classes`, its own map of each field's class — every one D,
the row's — which the D erase blanks it by. They are listed to everyone who reads the
person's [history](#the-history) and shaped by the two competence groups, field by
field; every reader of the history reads both.

### What comes next

The phase is built: A — the catalogue, a person's competences, the worker and the
Workforce contract; B — what the person wants and the review; C — profiles and gaps,
the matrix and the interests overview. Planning is the consumer: its delivery A reads
who holds what, the people in employment, their capacity and the caller's own person
through `contracts.Workforce`, and its delivery B the catalogue, for a request's
requirements, and `PreferenceFits`, the fit Staff scores for a booking
([the matching contract's Staff side](#the-workforce-contract)).

## Absence

Absence is delivery B: the [absence types](#absence-types) with
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
| `ammefri` | leave | hours | yes | — | — | yes | absent | `andreLovfestedePermisjoner` |
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
| `absent_hours` | other | hours | no | — | — | no | absent | — (reserved, always hidden) |
| `absent_hr_only` | other | days | no | — | — | no | hr_only | — (reserved, always hidden) |
| `absent_hr_only_hours` | other | hours | no | — | — | no | hr_only | — (reserved, always hidden) |

- **No percentage on the sick kinds.** A sykmelding's share of each day is its degree
  (`degree_percent`), not a percentage; ferie is whole days.
- **Visibility.** `absent`: a colleague sees "absent", the manager and HR the type.
  `hr_only` — religiøs høytid, svangerskapskontroll, svangerskapspermisjon and
  fødselspermisjon, which reveal special-category data: the manager sees "leave", the
  type is `staff:personal-data`'s and the person's own.
- **The reserved types** are what the erases write in place of a type: `absent` in
  place of a sick or child-sick type and of a plain pregnancy or health type counted in
  days, **`absent_hours`** in place of one counted in hours (helse), so an erased visit
  keeps its hours and counts no day; and, for an HR-only type (svangerskapskontroll,
  svangerskapspermisjon, fødselspermisjon), **`absent_hr_only`** (days) and
  **`absent_hr_only_hours`** (hours), HR-only themselves, so an erased HR-only row still
  reads *leave* to every reader who read it so and its events stay unlisted to them;
  only the person and `staff:personal-data` see it became a reserved type. All four are
  hidden, unpaid and never shown again (`seed_immutable`); `reserved` is true on each.
- **Pay.** `paid` is informational: no figure reads it — the payroll export and its
  totals count days and hours by kind, never by pay. A seed's pay is **HR's to change**
  (the owner's answer 7): a company that pays beyond the law marks the seed paid, and
  can take it back. `ammefri` is the exception: it is seeded **paid** —
  paid within aml. § 12-8 (2)'s limits — up to one hour a day in the child's first year on a working day of seven hours or more; nursing time beyond that is unpaid by law unless agreed — register it with a company type of your own — and is never set unpaid (`seed_immutable`, 409, the detail saying so). `svangerskapskontroll`, seeded paid, is held the same way (the owner's decision of 2026-10-18): aml. § 12-1 gives a pregnant employee paid time off for antenatal check-ups that cannot reasonably take place outside working hours, and the detail says so.
  The keys of the four reserved types are refused for a company type (400 on `key`).
  An erase switch turned on or off is recorded as the installation event
  `settings_updated`, naming the field `from` and `to`, the caller its actor.
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
  `key`, `kind` or `unit` is a 400 naming it. A **seed** changes its names, its order,
  whether it is hidden and whether it is paid, nothing else: anything more, `ammefri`
  unpaid, and showing or paying a reserved type, is `seed_immutable` (409). The full
  replace sends `paid` back like every other flag, so Settings' form sends what it read
  and what HR changed. A company type's visibility is **widened** — `hr_only` to
  `absent` — only while no absence names it: once one does, widening is
  `absence_type_in_use` (409), since it would show colleagues and managers, after the
  fact, a type people took as HR-only. **Narrowing** — `absent` to `hr_only` — is
  allowed but while an absence of the type that its own person approved
  (`self_approved`) has not ended by today in the settings zone: that is
  `absence_type_in_use` too (privacy review L3), since B's by-right self-approval is
  only ever of a type colleagues see, and an HR-only row its own person approved is
  the sole `staff:personal-data` holder's alone — narrowed under a running one, it would
  read as that. Once those rows have ended the narrowing is allowed: the
  `selfApproved` item, which lists only rows not ended, has gone with them. A company type that
  is HR-only neither allows a percentage nor draws on the holiday account (a 400 on
  `allowsPercent` or `drawsHoliday`; whole-branch review): a reader of it as leave would
  learn its kind from overlap's shares or the account's arithmetic. The type's row is
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
| `afterSeptemberVirkedager` | 6 (ferieloven § 5 nr. 3) to the entitlement, for a start after 30 September; left out, 6. |
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
no diagnosis is recorded. It is class D in the care-day group, where the design places
it; moving it to the health group and class E is an open question for the owner.

### Absences

`staff.absences` holds what a person is away for: a person, a type, a first and a last
day — or one day and its hours, for a type counted in hours — a percentage of each day,
a status and, for a sykmelding, its degree. A type that needs a decision starts
`requested`; a type that is a notification (egenmelding, sykmelding, sykt barn, helse,
svangerskapskontroll) starts `registered`. A request is then `approved` or `rejected` by a decider, and a
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
public holidays of the platform calendar (`internal/holidays`), whatever the share; an
hour row counts none — and, for a type that draws on the holiday account, `accounts`:
the [holiday account](#the-holiday-account) of each year the row touches after the
write, and `overdrawn` when taking every requested day would overdraw one. That is a
warning, never a refusal.

**Changing, removing, cancelling** (reading 16, pre-flight M8). `PUT /absences/{id}`
(`If-Match`) replaces the type, days, percent, hours, degree and note, judged as a new
row is with the row itself left out of the overlap. A change never swaps a type that
needs a decision for one that is a notification, or the other way round
(`absence_status_change`): a request stays a request and a registration a
registration — remove the row and enter the other. Nor does it swap an HR-only type for
any other, or the other way round (`absence_visibility_change`, 409; delivery C's
privacy review): the two are decided, mailed and shown by different people, so a
request of ferie turned into svangerskapspermisjon would leave its manager's inbox for
the `staff:personal-data` holders' — telling the manager what it became — and the other
way round arrive in a manager's inbox after those holders were mailed; the row is
removed and the other entered. A [correction](#decisions) never crosses it either. The
person changes their own row while it is `requested`; a `registered` row — a sick one
included — is `staff:manage`'s to change: ending a sykmelding early or fixing its days
is HR's job, the person asks. `DELETE /absences/{id}` removes a row nobody decided: a
request (the person or HR) or a registration (HR alone). Anything else is
`absence_not_requested`. `POST /absences/{id}/cancel` withdraws a request — the
person's or HR's — and keeps it as history (`cancelled`); an approved row is cancelled by
its decider ([decisions](#decisions)); anything else is `absence_not_cancellable`.
**Authority comes first, and one answer** (review C1): anyone who may not make the write —
for a change or a delete anyone but the person and HR, for a cancel anyone but the
person, HR and a decider of theirs, for a decision anyone but the person and a decider;
for an HR-only row HR means HR with `staff:personal-data` (review H2) — gets the 404 of a
row they cannot write, whatever its status, before any status is judged; so a write never tells a reader what the row does not.

**Reading them.** `GET /absences?personId&from&to&status&page&pageSize` lists, newest
first, the rows the caller may see: everyone's to `staff:manage`, `staff:approve` and
`staff:personal-data`; otherwise their own, their reports' today (the
[one manager rule](#managers-and-the-org-view)), and — with `staff:view` — the
days every person active today is away, as **spans** (below), never a request or a
refusal — and those never under a `status` filter: a colleague reads no status, so
asking for the registered rows cannot pick out who was sick. `GET /absences/{id}`
answers one row, the 404 of an unknown id to anyone who may not see it — a colleague
included, who is told no row ids. With spans among them the page is cut from all the
caller's rows and spans, newest first. `GET /me/absences` lists the caller's own. Each row is shaped by three
groups (design §5):

| Group | Fields | Self | Colleague | Manager | Approver | `staff:manage` | `staff:personal-data` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Absence | `id` | ✓ | — (a span) | ✓ | ✓ | ✓ | ✓ |
| Absence | `firstDay`, `lastDay` | ✓ | ✓ (a span) | ✓ | ✓ | ✓ | ✓ |
| Absence | `status`, `workingDays` | ✓ | — | ✓ | ✓ | ✓ | ✓ |
| Absence type | `typeId` (`shownAs: type`) | ✓ | — (`shownAs: absent`) | ✓; an `hr_only` type `shownAs: leave` | ✓; an `hr_only` type `shownAs: leave` | ✓; an `hr_only` type `shownAs: leave` | ✓ |
| Absence detail | `percent`, `hours`, `degreePercent`; `note` and `decisionNote` never on a row shown as leave | ✓ | — | ✓ | ✓ | ✓ | ✓ |

**A colleague cannot tell one absence from another.** Only the notification types —
the sick kinds among them — start `registered`, and only an hour type counts no
working day, so a colleague is shown neither the status nor the working days: a
sykmelding, a sick child and a holiday are the same dates, shown `absent`. Nor is a
colleague shown the rows themselves (whole-branch review): a person's `approved` and
`registered` day rows are coalesced into **spans** — a row that overlaps or adjoins the
span before it extends it — answered with no `id`, so a sykmelding over ferie, or a
row registered afterwards beside another, reads as one span. The spans are coalesced
over the person's whole absence and then **clipped to the window** asked for (`from`,
`to`; the calendar's range), so sliding the window across where one row ends and the
next begins shows only the clipped union, never a row's own first or last day
(privacy re-check).

**Hour rows** go only to the person, to `staff:personal-data` and, for a type that is
not HR-only, to the readers of the type (the manager, the Approver, HR): never to a
colleague, and an HR-only hour row — svangerskapskontroll — never to a reader of it as
leave, in a list, on the calendar or by id. `onLeaveToday` counts no hour row either.

**A row shown as leave looks like any other leave.** To the manager and HR without
`staff:personal-data`, a row of an HR-only type carries no `hours`, the working days of
its days, and `approved` in place of `registered` — and an HR-only hour row, such as
svangerskapskontroll, is not shown to them at all (above). It carries no `note` (review M1), and it is shown only while `approved`
or `registered`: a request of an HR-only type, a refusal and a cancel are
`staff:personal-data`'s ([decisions](#decisions)), so a list, a calendar and a read by
id leave them out to these readers. A status filter matches it by the status it is shown as — asking for
`registered` rows does not list it, asking for `approved` does — and none of its events
is listed to them (below): they read its dates already, and one registration event
against a request and its decision would tell a notification from a request.

**A row's revision goes only to whoever writes it** (reading 32). A
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
(plan Revision 3, M1; the payroll export and the sick-leave rate read that share). Two
sykmeldinger never share a day.

**What a writer may not read, overlap does not tell** (whole-branch review and its
privacy re-check; plan reading 35). A writer who reads HR-only types as leave — HR
without `staff:personal-data`, a manager deciding — judges an approved or registered
HR-only **day** row as one whole day of leave that shares nothing, its share unread,
and judges neither an HR-only request nor an HR-only **hour** row, which they never see
(reading 36) and which never blocks a day row in the rule anyway: a 24-hour probe beside
a svangerskapskontroll, ferie or a sykmelding over a check-up day, or a day row over a
pending svangerskapspermisjon is not refused for them. The person and
`staff:personal-data` judge every row as it is, so such a row is refused at the HR-only
request's approval when the two cannot stand together.

### The holiday account

`GET /people/{id}/holiday-account?year` — the person, a manager of theirs today, the
Approver (`staff:approve`), `staff:manage` and `staff:personal-data`; anyone else the 404 of an unknown person —
and `GET /me/holiday-account?year` answer one person's account for a year (this year
in the business time zone when left out; 2000–2100), in working days:

- **`entitlement`**: the scheme of the person's earliest employment active in the year
  (two concurrent employments, one account), [converted](#holiday-schemes) — its
  after-September days when that employment starts after 30 September of the year —
  plus the extra days converted when the person turns 60 or more in the year,
  un-prorated — after-September included: ferieloven § 5 nr. 2 sets the extra week
  no start condition, and nr. 3 limits only the feriefritid (checked on Lovdata,
  2026-10-14). No employment in the year, nothing. **Forskuddsferie** (holiday taken
  in advance of the year it is earned in, up to 12 virkedager by written agreement,
  ferieloven § 7 nr. 3) is out of scope: the account
  records no advance, and an advance agreed is HR's to keep as a correction.
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

**Who decides** (design S12, plan readings 1, 2 and 4; `deciders.go`). The
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
it. Its change,
delete and correction are the person's (as for any row) and HR's with
`staff:personal-data`; an approved one is also cancelled by its decider — any holder of
`staff:personal-data` — until its first day has passed; anyone else gets the uniform 404. So to every reader of the type as
leave, every HR-only row, requested-then-approved or registered, is one approved leave
they never saw asked for, refused or withdrawn: no note, no decision field, no history —
svangerskapskontroll reads as any other.

**The sole holder of `staff:personal-data` decides their own HR-only request** (the
owner's decision on B's open question 9, delivery C; `holders.go`, `deciders.go`). The
caller decides their own request of an HR-only type when they hold
`staff:personal-data` and the user directory's `PermissionHolders("staff:personal-data")`
names exactly one user — the caller. The **holders** are the users holding the key
through a role of their own, a role mapped to an active access group they are
effectively a member of, or the Owner role, which holds every key (a delegation grants
none); **active** is what a session needs: not disabled, not pending, and — while SCIM
is configured — not deactivated by the directory unless an Owner (a lockout is
ignored). So an Owner counts: with an Owner and one other holder, neither is sole. The
holders are read through `usersPermissionHolders` before any transaction and once per
request; a read that fails makes nobody sole, so the rule fails toward waiting
(`own_request`). It is judged before the decision's transaction takes the person's lock,
like the manager-deciders: a holder added in the same instant is not seen by that
write (accepted). The approval records `self_approved` and `absence_self_approved`,
listed and shaped as every HR-only row's events (reading 32 — a leave-reader is listed
none of them), sends no mail, and raises [`hrOnlySelfApproved`](#attention-items) to
`staff:personal-data` alone. The item is listed while the absence lasts — until its last
day has passed — and goes then; the `absence_self_approved` event stays in the person's
history for whoever reads the row's events (the person and `staff:personal-data`), so
the self-approval remains on record after the item is gone. The request is in the sole holder's own inbox and
`absenceAwaitingDecision` count (`@sole_personal_data` on `RequestedAbsences` and
`CountRequestedAbsences`; B's by-right self-approval, `@self_approves`, stays a type of
visibility `absent`), and `hrOnlyAwaitingDecision` leaves it out while they may decide
it. With a second active holder it is theirs to decide again, and the first holder's
own is `own_request`.

**An HR-only type is a target only for the person and `staff:personal-data`**
(whole-branch review): HR without that permission cannot register, change or correct
a row into one; the answer is the 400 an unknown type gets (`typeId`: "No absence type
has id …"), so the attempt tells nothing.

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
- **`POST /absences/{id}/cancel`** of an **approved** row is its decider's until its first
  day has passed, and `staff:manage`'s alone after that (a decided absence in the past is
  HR's, S12); the person never cancels their own decided row — `absence_not_cancellable`,
  or `own_request` (403) for a holder of `staff:approve` or `staff:manage` who is not the
  self-approver.
  Recorded as `absence_cancelled`.
- **`POST /absences/{id}/correct`** (`staff:manage`, `If-Match` as a change takes it) changes
  an `approved` or `registered` row's type, days, percent, hours, degree and note, judged
  as a new row is; a request, a rejected or a cancelled row is `absence_not_cancellable`
  (pre-flight M7), and a correction keeps the status, so never across the line between
  a request and a registration (`absence_status_change`); never HR's own row but the
  self-approver's. Recorded as `absence_corrected`, both types shaped by their own
  visibility. Nor does a correction cross the line between an HR-only type and any
  other (`absence_visibility_change`, 409; the re-check of delivery C's privacy
  review): the row would keep its decision — a sole holder's self-approval among it,
  which would then be listed to `staff:manage` as `selfApproved` and named on the row's
  later events — under the other audience, and the same row id would read as another
  kind of leave. HR cancels a decided row — or removes a registered one, which cannot
  be cancelled (`DELETE /absences/{id}`) — and enters the other anew; the refusal's
  detail says so.

### The absence mails

Sent after the write has committed, outside every transaction, through the module's one
SMTP accessor, one after the other under one budget of 30 seconds for all of a notice's
mails (what the budget leaves unsent is logged by count); skipped with a log line when
the mail driver is not `smtp`, and never failing the write (reading 26). Who is mailed
(`notifications.go`):

- **A request or a registration, to each manager-decider** at the address Staff holds —
  the person's work email, else their private one — in that person's language; never
  for an HR-only type (review H3).
- **A request whose person has no manager-decider with an active user** (the owner's
  answer 3, delivery C): every active holder of `staff:approve` or `staff:manage` instead
  — Owners by the wildcard — each once (by user id), never the person's own user, at the
  address identity holds (`Holder.Email`, the first Staff mail not to an address of its
  own register; reading 18). A holder identity holds no reachable address for (a SCIM or
  identity provider placeholder) is skipped with a log line naming the user's id. The
  language is that of the person the holder's user is linked to, else the user's own,
  else Norwegian. The mail links to the absence area. A registration falls back to no
  one. The inbox and `absenceAwaitingDecision` are unchanged.
- **A request of an HR-only type** to no manager and no holder of `staff:approve` or
  `staff:manage`, but to each other active holder of `staff:personal-data` — its only
  deciders (reading 20), Owners included — in a **neutral** mail: "An absence request
  awaits your decision" and a link to the absence area, with no person, type or dates.
  An HR-only registration mails nobody.
- **An approval, a rejection, a decider's cancel and a correction, to the person**; the
  self-approvals send none.

The holders are read through the directory before any send and never inside a
transaction; a read that fails mails no holder and is logged, and the request stands.
No log line carries an address: a failed send's error has the recipient's address
replaced, and every line names a person's or a user's id. A recipient without an address is skipped with a log
line naming the person's id. A mail names the type only when it is neither a sick kind
nor HR-only — an HR-only type is "leave", a sick or child-sick one "an absence" — and
never in a registration's notice to a manager, which always reads "an absence" (only
the notification types are registered, so a doctor's visit named would tell the rest
apart as sickness; whole-branch review) — and
never a degree, the hours, a note or a decision's reason (reading 15); it carries the
dates and a link to My page (a holder's, to the absence area).

### The approvals inbox

`GET /approvals?page&pageSize` lists the requests the caller decides, oldest first by
when they were made: every person's of a plain type to `staff:approve` and
`staff:manage`, a manager's reports' today, an HR-only type's to `staff:personal-data`
alone; never the caller's own but the self-approver's — B's by-right self-approver's
plain request, and the sole holder of `staff:personal-data`'s HR-only one. Each item is
`{absence, person}` — the person as the work group names them, the row shaped as
`GET /absences` shapes it for the caller.

### The calendar

`GET /calendar?from&to&departmentId` answers a range of at most 93 days: the public
holidays in it from the platform calendar (`internal/holidays`) and a row per person
with their requested, approved and registered absences touching it, shaped as
`GET /absences` shapes them. `staff:approve`, `staff:manage` and `staff:personal-data`
see everyone with an employment in the range; `staff:view` the people among them active
today, as the directory lists them, each bar a span of approved and registered days
with no row id and no hour row (as `GET /absences` answers a colleague); anyone else their reports today and themselves. `departmentId` keeps
the people whose terms place them in that department on the day judged — today, held
inside the range.

### On the calendar

When the Calendar module is on, Staff fills its `CalendarSource` slot
(`internal/staff/calendar_source.go`, with its own query) and projects one kind,
**`staff.absence`**: the caller's **own** absences — requested, approved or registered —
overlapping the window, found through the caller's [self-service
link](#the-self-service-link). It needs `staff:access` and a link; without either, it
answers nothing. Each is an all-day item over `first_day`..`last_day` (an hour type's is
one day), never done, titled with the type's name in the **person's own language** —
`staff.people.preferred_language`, `name_en` when it is `en` and `name_nb` otherwise, the
rule the module's own mail follows — not the language of whoever is looking. Its subtitle
is an hour type's hours (`3 h`) and, for a request not yet decided, the word `Requested`
or `Søkt om` in that same language, joined by ` · `; an approved or registered absence
carries no status word. An HR-only type is shown: the absence is the person's own. Every
item links to `/staff/me?tab=absence`. No colleague's absence is projected: a colleague's days away are
this calendar's, above, to the audience it chooses. See
[the kinds](/en/reference/calendar/#the-kinds).

### The absence events

An absence's write is one event — `absence_requested`, `absence_registered`,
`absence_updated` (each field's `from` and `to`), `absence_deleted` (the row's fields as
they were), `absence_cancelled`, or a [decision's](#decisions) `absence_approved`,
`absence_self_approved`, `absence_rejected` and `absence_corrected`, or a sykmelding's
`absence_split` of a row beneath it ([below](#a-sykmelding-over-other-rows); the part
it inserted named `splitAbsenceId`, the sykmelding `sickAbsenceId`) — naming the absence (`absenceId`); an adjustment's
names it (`adjustmentId`). Beside its `fields` each carries **`classes`**, each field's
retention class as the module wrote it (pre-flight B1), because one name means
different data on different rows:

| Field | Class |
| --- | --- |
| `firstDay`, `lastDay`, `percent`, `hours`, `status`, `selfApproved`; an adjustment's `year`, `days`, `reason`, `fromYear` | C |
| `note`, `decisionNote`; an adjustment's `note` | D |
| `type`, `degree` | E on a sick or child-sick row (either side of a change), else C |
| `demandedOn`, `postponedUnder` (a postponement's `absence_split`) | E |

`payload_classes` is their union. The names never enter the module's per-name class
table: the erase blanks such an event **by its own map** — a leaver's D erase blanks
the note and keeps the dates and the type, a sykmelding's type and degree included;
the health erase blanks a sick row's type and degree and leaves a ferie row's alone. A
field the map does not class, and every field of an absence event without a map, is
blanked by every erase: what cannot be placed is erased rather than kept. The module
writes a name it has not classed as E too (whole-branch review), so a field added
later is shaped by the health group until it is classed on purpose.

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
without `staff:personal-data` (reading 32): its count and its
kinds, one `absence_registered` against an `absence_requested` and its decision, would
tell svangerskapskontroll from the other HR-only leave. They read the row's dates on the
row itself. Nor is one listed whose row is of an HR-only type **now**, recorded while
it was a plain one (whole-branch review): a correction into such a type takes the
row's history from them. (Were one listed, its shaping would still be the row's — the neutral kind
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
  documents leaving it — unless HR closed it, or the retention run erased it (which
  closes it at its derived close, its last day + 17, keeping its dates and milestone
  kinds), or an absence still names it. An HR-closed case whose rows are all removed
  stays, empty, with its milestones and notes: the employer's follow-up record.
  A row changed or corrected into a type that is not sick leaves its case and its
  degree in the same write.
- **A merge** (plan Revision 3, M3): a row that fills the gap between two cases merges
  them into the older. The younger's documents move with its rows; each of its
  milestones' done day or waiver is carried onto the older's where that has neither;
  the milestones' notes and the accommodation note are appended after the older's, a
  blank line between. A note is the employer's aml. § 4-6 record and is never cut:
  when the two no longer fit in 2000 characters, the survivor keeps its own text and,
  where the pointer still fits, ends with "… continued in the case's history". Its estimate is kept where the older
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
case with its milestones. A case and its milestones are class **E** whole. The
Approver reads **open** cases only (§5): a closed case is no `staff:approve` holder's
unless they read it otherwise (the person, their manager, HR).

| | Self | Manager | Approver | `staff:manage` | `staff:personal-data` | Colleague |
| --- | --- | --- | --- | --- | --- | --- |
| The case, its dates and milestones | ✓ | ✎ | ✎ | ✎ | ✎ | — |
| The accommodation note | ✓ | ✎ | — | ✎ | ✎ | — |
| The estimate, the close | ✓ | ✓ | ✓ | ✎ | ✓ | — |
| A milestone's note, a waiver's reason | ✓ | — | — | — | ✎ (the note) | — |

A caller who may not write what a write asks for gets the 404 of a case they cannot
write, whatever its state (authority before status); a case they may not read is a 404
on every operation. `PUT /cases/{id}` and `PUT /cases/{id}/milestones/{kind}/note`
take `If-Match` (the case's and the milestone's revision). **The revisions go to the
health group alone** — the person and `staff:personal-data` — since a milestone's
moves with its note and a case's with its accommodation note (whole-branch review): a
writer of the case without that group, the manager or HR, is sent an opaque `etag` in
place of the case's revision and sends it back (a bare revision from them is
`revision_conflict`, naming none); the Approver, who writes neither note, is sent
neither. On `PUT /cases/{id}` a field
left out is kept and one sent is written, null clearing the note and returning the
estimate to the derived day (review L6); the estimate sent by anyone but
`staff:manage` — null included — is a 400. A milestone's note left out or empty clears
it.

### The arbeidsgiverperiode and the milestones

Each case has five milestones (`staff.sick_case_milestones`), dated when the case's
rows change — never touching a milestone's done day, its waiver or its note:

| Kind | Date |
| --- | --- |
| `employer_period_end` | The case's sixteenth sick day; none until the rows reach it. The count starts on a working day (NAV; whole-branch review): a row that begins on a Saturday, a Sunday or a public holiday — the case's first, or one after worked days — counts from its first working day. |
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

- **Ferie under a sykmelding** stays as it is (plan Revision 3, H1). Postponing holiday
  for sickness is the **employee's demand**, made with a legeerklæring (ferieloven § 9):
  for sickness that begins before the holiday, by the last working day before it
  (§ 9 first paragraph); for sickness during the holiday, as replacement days later
  (§ 9 second paragraph). Until HR records the demand, the approved ferie and a 100 %
  sykmelding stand side by side — the ferie still counted as taken, and in the payroll
  file and the sick-leave rate as holiday, not sickness — and HR's
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
  `holiday_postponement_too_late`; a row that is no registered sykmelding, a graded sykmelding — the employee is not wholly
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
- **The twelve months** — every window of twelve months (ftrl. § 8-23 "i løpet av 12
  måneder"; the day after the same date a year earlier to the day, 366 days across 29
  February — whole-branch review) that holds a day of the row, later occurrences
  counted too, so a backdated egenmelding is judged in each twelve months it lies in
  (review L10): under `minimum` at most four occurrences; under `extended` at most 24
  days; under `custom` both by the stored numbers. The limits **key on the policy's
  name**: under `minimum` and `extended` the preset's numbers are used whatever the
  settings row holds.
- **The case**: no day past its case's sixteenth sick day — after the
  arbeidsgiverperiode a sykmelding is needed.
- **Employment**: the first day at least `self_cert_after_months` after the start of
  the employment in force on it (`self_certification_too_early`).
- **The withdrawal**: no egenmelding with any day from the day HR decided to withdraw
  it until its last day (`self_certification_withdrawn`). Days before the decision stand
  (review L1), but every day of a row is judged: a row that begins before the decision
  and runs into the withdrawal is refused whole, and the person registers the days
  before it as a row of their own.

Past a limit is `self_certification_exhausted`; each refusal is a 409 whose detail says
why and tells the person to register a sykmelding.
`GET /people/{id}/self-certification` (the person, their manager, the Approver and HR)
and `GET /me/self-certification` answer the standing today: the policy and its
numbers, the occurrences and days used in the twelve months ending today, the day the
months of employment allow it from (`eligibleFrom`) and — to the readers of the
care-day fields alone — the withdrawal.

### The withdrawal

`PUT /people/{id}/self-certification/withdrawal {until, reason}` (`staff:manage`)
withdraws a person's right to egenmelding (ftrl. § 8-27) until a day after today and at
most a year ahead, with a reason of 1–500 characters; `{"until": null}` lifts it. It
applies from the day it is decided (`withdrawnFrom`), never to days before; re-issuing
a withdrawal still in force — to extend it, say — keeps that day. The write moves the
person's revision, as any change of the person does. The three
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
(ftrl. § 9-8) — counted no further than the entitlement when the care-day fields are
recorded (whole-branch review): a day used past it is no care day; the payroll file's
`Child sick days beyond employer` is capped the same way. When the only children who qualify are over twelve with extra days, NAV
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
(`selfCertification`), every [competence row](#a-persons-competences) they have had,
superseded ones included, newest first, with its detail (`competences`),
[what they want](#what-the-person-wants) — their `interests`, `preferences` and
`availability`, each row with its Seen stamp — their [competence reviews](#the-review),
newest first, each reviewer named (`competenceReviews`), the
[competence profiles](#profiles-and-gaps) that reach them, assigned or their
department's default (`profileAssignments`), their [photo](#the-photo)'s type and when
it was set (`photo`, `{contentType, updatedAt}` or null; the bytes are read through
`GET /people/{id}/photo`), and every
event of their history, oldest first. Each absence
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
(a choice of the absence plan): the register's file is a bulk copy that travels to spreadsheets, and the
fields are a family's circumstances — one of them, `chronicallyIllChildren`, borders on
health data (the plan's open question 8) — kept to the person's form, where HR types them one
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
(design S18). It is `staff:manage`'s **with `staff:personal-data`** (whole-branch
review; interim, open question 10 in the plan): its leave rows name HR-only leave by
its a-melding description, so HR without `staff:personal-data` — who reads such leave
only as *leave* — and anyone else get the uniform 403. The alternative left to the
owner is to leave HR-only chains out of the file for HR without that permission. The rows are the
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
| `Leave ID` | a leave row | The leave's a-melding `permisjonId`: a stable surrogate of its chain — `L-` and twelve hex digits, a keyed MAC of the lowest id among its rows under the installation's secret — so it stays as rows are added to the chain and is the same every month the leave is reported, and the file tells no id of the register (whole-branch review). |
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
nothing (Revision 3 M1). **A ferie day is a holiday day and counts no sickness**,
whatever sickness covers it — a sykmelding of any degree or an egenmelding: the ferie
stands until the employee demands to postpone it (ferieloven § 9), and no sykepenger
are paid on a holiday day. When HR records the employee's
[demand to postpone](#a-sykmelding-over-other-rows) the ferie is split, and the split
rows then decide the days — the sick days the sykmelding's, the rest holiday (Revision
3 H1). The sick-leave rate reads the days alike. An hour row of another kind than time
off counts in no total. **Concurrent employments**: a person with two employments in force
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
  carries a description; nor svangerskapspenger, which no type models;
- **never an hour row**: a leave counted in hours — `ammefri` among the seeds, though
  it carries the description `andreLovfestedePermisjoner` — is never a leave row; it
  counts in no total either.

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
can reach the month would cost less time and change no answer.

**The order of work.** The parameters and the actor are judged first; everything the
file is built from — the people's numbers and names, the employments, their terms, the
business units, the departments and every approved or registered absence — is read in **one REPEATABLE READ snapshot**; the
rows and the file are built in memory; then the export is recorded as the
installation's **`payroll_exported`** event, naming the exporter, with `month`,
`format`, `rows` (every row written, the leave rows included) and `sha256` — the hex SHA-256 of the very bytes answered, so the file
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
  documents (a restricted category) and the health fields of the events about them —
  and, with `erasePregnancyHealthTypes` on, a pregnancy or health-appointment row's
  type. Erased on [schedules of their own](#what-is-due) — a closed case's, a year's
  child-sick (or pregnancy and health) rows', a lapsed withdrawal's — whatever the
  employment, and what none of
  them owns a longer period after the last employment ended.
- **F** — the legal hold, which stops every erase and is erased by none.

### The classes, column by column

The module's `columnClasses` (`retention.go`) is the erase's one source: the
statements that null a class are built from it, a test walks the database's columns of
these tables and fails on one it does not know (or a key no column has), a second
holds every payload field to its column's class, and a third holds this table to it.

| Table | Class | Columns |
| --- | --- | --- |
| `people` | C | `id`, `employee_number`, `given_name`, `family_name`, `display_name`, `birth_date`, `gender`, `national_id_kind`, `work_country`, `preferred_language`, `provision_account`, `provision_from`, `account_email_kind`, `account_revision`, `account_kind`, `erased_at`, `reassessed_at`, `reassessed_by_user_id`, `revision`, `created_at`, `updated_at` |
| `people` | D | `national_id_sealed`, `national_id_mac`, `national_id_country`, `work_email`, `phone_work`, `phone_mobile`, `phone_private`, `private_email`, `street`, `postal_code`, `city`, `country`, `bank_account_sealed`, `bank_account_last4`, `bank_account_kind`, `children_under_12`, `sole_carer`, `chronically_ill_children`, `notes`, `photo_object_key`, `photo_content_type`, `photo_updated_at` |
| `people` | E | `self_cert_withdrawn_until`, `self_cert_withdrawn_reason`, `self_cert_withdrawn_from` |
| `people` | F | `legal_hold`, `legal_hold_reason`, `legal_hold_by_user_id`, `legal_hold_at` |
| `next_of_kin` | D | every column: `id`, `person_id`, `position`, `name`, `relationship`, `phone`, `note`, `created_at`, `updated_at` |
| `person_roles` | C | every column: `id`, `person_id`, `role`, `from_date`, `to_date`, `created_at`, `updated_at` |
| `employments` | C | every column: `id`, `person_id`, `employment_number`, `relationship_type`, `business_unit_id`, `start_date`, `end_date`, `end_reason`, `contract_signed_at`, `holiday_scheme_id`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | C | `id`, `employment_id`, `valid_from`, `employment_type`, `temporary_ground`, `expected_end_date`, `appointment_form`, `hired_out_to`, `fte_percent`, `working_hours_scheme`, `full_time_hours_per_week`, `occupation_code`, `title`, `employee_type`, `department_id`, `location_id`, `manager_person_id`, `cost_centre`, `division`, `working_time_text`, `break_minutes`, `special_hours_text`, `overtime_rule_text`, `probation_end_date`, `probation_extension_agreed`, `probation_extended_to`, `probation_extension_reason`, `notice_employee`, `notice_employer`, `notice_unit`, `tariff_reference`, `holiday_terms_text`, `competence_development_text`, `social_security_text`, `revision`, `created_at`, `updated_at` |
| `employment_terms` | A | `remuneration_type`, `pay_components`, `pay_method`, `payday_text`, `seniority_date`, `pay_grade` |
| `employment_terms` | D | `note` |
| `identity_links` | C | every column but a provider link's two addresses: `id`, `person_id`, `provider`, `external_id`, `state`, `managed`, `blocked_reason`, `last_error`, `attempts`, `next_attempt_at`, `lease_id`, `lease_until`, `last_synced_at`, `last_spec_hash`, `synced_employee_number`, `disable_requested_at`, `announced_at`, `pass_issued_at`, `reported_codes`, `photo_pushed_stamp`, `created_at`, `updated_at` |
| `identity_links` | D | `external_upn`, `external_email` — the principal name and the address a provider answered, addresses as the work email is |
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
| `groups` | C | every column: `id`, `key`, `name`, `description`, `kind`, `rule`, `archived_at`, `archived_by_user_id`, `revision`, `created_at`, `updated_at` |
| `group_members` | C | every column: `group_id`, `person_id`, `source`, `since` — a person's rows go with the person on an erase or a delete |
| `group_owners` | C | every column: `group_id`, `person_id` — a person's rows go with the person on an erase or a delete |
| `group_traits` | C | every column: `group_id`, `provider`, `values`, `enabled`, `disabled_at`, `external_id`, `state`, `last_values_hash`, `last_members_hash`, `last_error`, `last_error_field`, `synced_at`, `retired_at`, `attempts`, `next_attempt_at`, `lease_id`, `lease_until` |
| `person_competences` | D | every column: `id`, `person_id`, `competence_id`, `level`, `issued_on`, `valid_from`, `valid_to`, `issuer`, `reference`, `evidence_document_id`, `source`, `verified_at`, `verified_by_user_id`, `verified_basis`, `verified_note`, `superseded_by_id`, `expiring_notified_at`, `expired_notified_at`, `revision`, `created_at`, `updated_at`, `seen_at`, `seen_by_user_id` — the D erase deletes the rows; the evidence is a `certificate` document, erased by its category's class |
| `person_interests` | D | every column: `id`, `person_id`, `competence_id`, `seen_at`, `seen_by_user_id`, `created_at` — the D erase deletes the rows |
| `person_preferences` | D | every column: `id`, `person_id`, `kind`, `value`, `weight`, `seen_at`, `seen_by_user_id`, `created_at`, `updated_at` — the D erase deletes the rows |
| `availability_declarations` | D | every column: `id`, `person_id`, `kind`, `weekday`, `from_date`, `to_date`, `start_minute`, `end_minute`, `seen_at`, `seen_by_user_id`, `created_at` — the D erase deletes the rows |
| `competence_reviews` | D | every column: `id`, `person_id`, `reviewed_on`, `reviewer_user_id`, `reviewer_person_id`, `created_at` — the D erase deletes the rows |
| `person_profiles` | D | every column: `id`, `person_id`, `profile_id`, `created_at` — the D erase deletes the rows |

Ids, foreign keys, revisions and timestamps are C: they say that a row existed and
when. An event's `payload_classes` are its fields' classes (`fieldClasses` in
`events.go`, held to the columns above); a document's event takes its category's; an
absence, adjustment, case, withdrawal or competence event carries its own map of each
field's class ([the absence events](#the-absence-events),
[the competence events](#the-competence-events)), and the types beside its fields
(`typeIds`) carry theirs (`typeClasses`).

### What is due

The run asks seven questions, each over what remains, "today" being the settings'
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
  closed — by HR, or on its last day + 17 (the sixteen days a row may still join in
  over), whichever came first — before today minus `healthRetentionDays` (spec open question 1: 365 days, built as
  designed), with something of class E left: a value of the case's or its milestones'
  E columns, a row still of a sick type or with a degree, a document filed under it, or
  a health field of an event it owns — and, with `eraseSickCaseDates` on, the case
  itself: a case B's erase reached before is due again until it is removed.
- **childSick** — a person, not held, with a child-sick row whose year ended before
  today minus `healthRetentionDays` (plan reading 21).
- **pregnancyHealth** — only with `erasePregnancyHealthTypes` on (B's open question
  11; off by default, when these rows are class C, kept with the employment): a person,
  not held, whatever their employment, with a row of `svangerskapskontroll`,
  `svangerskapspermisjon`, `fodselspermisjon` or `helse` whose year ended before today
  minus `healthRetentionDays` — never before the payroll month that reported it has
  passed, which that bound guarantees — or with an event naming one of those types
  whose row no longer has one (deleted, or corrected to another type), recorded in a
  year that ended before then. **`svangerskapspermisjon` and `fodselspermisjon` are
  never due before a full year after their year ended** (today minus one year),
  whatever `healthRetentionDays` says or the person's own cutoff — an event naming one
  of them likewise by its year — so the holiday-pay year after the leave, and a payroll
  re-export of it, still read the leave (the coordinator's decision on review L5).
  Only the type is erased: an absence's `note` and `decision_note` are class D and go
  with the D erase.

  These four, and the orphaned events below, are **capped by the person's own health
  cutoff** (whole-branch review): when every employment of the person ended before
  today minus `healthRetentionDays`, what a case, a child-sick row or a withdrawal
  owns is due with the rest of their health data, whichever comes first.
- **withdrawal** — a person, not held, whose [withdrawal](#the-withdrawal) of
  egenmelding lapsed before today minus `healthRetentionDays`, or — none in force —
  with a withdrawal event recorded before then that still holds a value.
- **orphaned sick events** — reported as `cases` — a person, not held, whatever their
  employment, with an absence or case event that nothing owns any more (its row, its
  case and the sykmelding it names all gone: a case removed with its only row, a case
  merged away before the merge was recorded, or a case a row's event names that is gone
  though the row has since become a child-sick one — the child-sick schedule owns only
  events that never named a case), recorded more than `healthRetentionDays`
  and seventeen days ago — the latest its case could have closed, on its last day + 17,
  the same bound the case schedule reads — that still holds a health value.
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
  of kin and their [competence rows](#the-exports-and-the-erase) — the evidence among
  their documents — deletes their documents of class D, queues their
  [photo](#the-photo)'s object in the ledger (its three columns are class D and nulled
  with the rest) — and, once the cycle's connection is returned, clears the managed
  avatar of the user their self-service link names (`SetManagedAvatar` with nil, as
  `system:staff-retention`, a failure logged) — replaces every value of class D in their
  history's payloads, and sets `erased_at` — the person is then
  [read-only](#the-erased-state).
- **health** deletes their documents in restricted categories filed under no case and
  replaces the class E values of the events no other schedule owns.
- **cases**, for each due case, erases its class E columns and its milestones' (the
  case keeps its id and its days, and is marked closed at its derived close — its last
  day + 17 in the settings' zone — when HR had not closed it; a milestone its kind and
  date),
  writes the reserved `absent` type in place of its rows' sick type and nulls their
  degree, deletes the documents filed under it, and replaces the health fields — and
  the `typeIds` of class E — of the events it owns: its own, those of the cases it took
  in by a merge, its rows', and those of a ferie split for one of its rows.
  With `eraseSickCaseDates` on (B's open question 12), the case's erase then **removes
  the case**: its events — its own and those of the cases it took in — are blanked to
  their kind (no case, no milestone, no field; only their rows' reserved `typeIds`, by
  which they are listed), its rows' events no longer name it (`rowCaseId`), its
  milestones are deleted, its rows are unlinked (`case_id` null) keeping their days as
  `absent`, and the case row is deleted. Its documents are deleted and queued as
  before. The erased event's `cases` count keeps its meaning: the items removed.
- **childSick** writes `absent` in place of the due rows' type and replaces the health
  fields of their events.
- **pregnancyHealth** writes a reserved type on each due row by its type's visibility
  and unit — for an HR-only type (svangerskapskontroll, svangerskapspermisjon,
  fødselspermisjon) `absent_hr_only`, or `absent_hr_only_hours` on an hour row; for
  helse `absent_hours` — its hours kept, and replaces every type value of its events
  **whatever class they were recorded in** (B wrote them as C): the `type` field reads
  `"changed"` and each such id in `typeIds` becomes the reserved type chosen the same
  way. An event naming such a type whose row no longer has one goes by the year it was
  recorded in. An event whose payload cannot be read fails the person's erase — rolled
  back and logged, due again — rather than being passed over (review item 7), so a
  person whose event stayed naming the type is never silently due every cycle with
  nothing erased. The days, the status and the dates stay. An HR-only row keeps its
  `revision` and `updated_at` (review H1): HR without `staff:personal-data` holds an
  opaque `etag` of the revision, and only this erase rewrites an HR-only row, so a tag
  that moved would say what it was. A value already one of the four reserved types is
  erased already: no "not yet erased" check counts it and no erase rewrites it
  (review L2). An erased HR-only row therefore
  reads to a manager, an Approver and HR without `staff:personal-data` exactly as it
  did — *leave*, its events unlisted to them — and only the person and
  `staff:personal-data` see that it became a reserved type (the coordinator's
  decision). Its kind is recorded, with the other health kinds, in the `erased` event
  of class E (below), so no reader without `staff:personal-data` sees an entry about
  it. A payroll month re-exported after its rows were erased reports their days as
  other days (`Other days`, kind `other`), and a leave of more than fourteen days among
  them no longer as a leave row: the a-melding that reported them was sent long before
  (accepted by the coordinator).
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

When anything was erased, the transaction records an **`erased`** event for the
person, `{"kinds": [{"kind": "D" | "health" | "A" | "cases" | "childSick" | "withdrawal" | "pregnancyHealth", "count": n}]}` —
two when it erased both kinds of content: the health kinds (`health`, `cases`,
`childSick`, `withdrawal`, `pregnancyHealth`) in one of their own, of class E, and the
rest (`D`, `A`) in one of no class — its actor the
installation (`system`) when the worker ran it and the caller when
[`POST /retention/run`](#running-it-by-hand) did. `count` is the items the kind
removed: each column value nulled, each row deleted (a next of kin, a document) and
each event payload rewritten. A D erase is always reported, since it marks the
person, even when it found nothing to remove (count 0); an A count sums the person's
due employments. Neither event holds a value an erase replaces, so no later erase
rewrites it. The event of no class is listed as the [hold's kinds](#the-history) are —
not to a manager, to the person as its kind with an empty payload — and read by
`staff:manage` and `staff:personal-data`. The health kinds' event is listed, as every
event of class E outside the absence family, to the health group's readers alone —
the person and `staff:personal-data` — so HR without `staff:personal-data`, a manager
and an Approver are listed and counted nothing new after an erase of health content
alone: an entry they could not read would still say the person's health was on file
(the coordinator's follow-up to Task 5b).

A **D** erase also takes the person out of every [group](#members-and-owners): their
`group_members` and `group_owners` rows are deleted in the same transaction, before the
person's row is written, with no event; the next sync takes them out in the directory.

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
2. **An identity-erase pass** (`eraseAccounts`, once the lease is released, when a module
   provides accounts): reads `ListErasedAccountLinks` on the pool (up to 50 erased,
   unheld people who still have a Staff-managed `identity` link that either names a user
   or has no non-`deleted` provider link left) and for each person calls identity's
   `Erase` on no connection (actor `system:staff-retention` for the worker, the caller
   for `POST /retention/run`) and any unconsumed announcement's `Unexpect`, then under
   the person's lock (`withPersonLockAllowErased`) re-checks that the person is still
   erased and not held and the link's `external_id` has not changed: when `Erase`
   succeeded or answered `ErrUserGone` (or `external_id` was already empty), it deletes
   the `identity` link (`DeleteErasedIdentityLink`) — or, while a non-`deleted` provider
   link remains for the person, clears `external_id`, `blocked_reason`, `last_error` and
   `attempts` and leaves the `identity` link `disabled` (`ClearErasedIdentityLinkUser`)
   so the accounts worker still claims it to delete the external account once
   `deleteExternalAfterDays` has passed — and records `identity_erased`
   (`{step: "erase", state: "deleted"}`, with `code: "user_gone"` when the user was
   already gone); when `Erase` was refused (`ErrAccountEnabled`, `ErrAccountPrivileged`,
   `ErrLastOwner`, `ErrAccountNotManaged`, `ErrInvalidActor`) or failed, it sets
   `blocked_reason = erasure_incomplete` on the link (`state = blocked` on a refusal,
   `last_error = accounts_unavailable` and `attempts` incremented on a failure), raising
   `erasureIncomplete` on the dashboard and retrying on the next retention run.
3. **A ledger pass**, with no lease.

The worker starts without storage: in worker mode its object store is built from the
configuration, and an unconfigured one fails only the ledger's attempts.

### Running it by hand

`POST /retention/run` (`staff:manage`) runs all three steps now, by the same code with the
same connection disciplines, the caller as the `erased` and `identity_erased` events' actor. It answers 200
`{"erased": {"kinds": [{"kind", "count"}]}, "deletions": {"attempted", "succeeded"}}` —
the kinds summed over the people erased, a kind no erase ran for absent — the health
kinds (`health`, `cases`, `childSick`, `withdrawal`, `pregnancyHealth`) only to a
caller holding `staff:personal-data` (review M1), as in the history. Its ledger
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
ever erased) — to a caller without `staff:personal-data`, the latest erase of
something other than health content (the event of no class), so the last erase does
not move for them when only health content went (review M1).

**The two erase settings** (`erasePregnancyHealthTypes`, `eraseSickCaseDates`; the
owner's answer 7) are read by each cycle when it starts. Turned on, one applies from
the next cycle to **everything already due** — a year's rows long past, a case B's
erase reached years ago. Turned off, it **restores nothing**: what was erased stays
erased, and only what is not yet due is kept from then on.

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

**What happens in identity and the directory.** Once a person is erased (and not under
a legal hold), the retention run's [identity-erase pass](#the-worker) calls identity's
`Erase` for the disabled user Staff managed for them ([what Staff's provisioning does in
identity](/en/admin/authentication/#what-staffs-provisioning-does-in-identity)): the user
is deleted together with their sessions, credentials, invitations, SCIM mapping, any
unconsumed announcement for their employee number and the delegations they granted
(delegations granted to them keep their row with `delegate_user_id` cleared), the
`actor_snapshot` and `target_snapshot` of identity's audit rows naming them are
pseudonymised in place (`displayName: "Erased user"`, `email` and `employeeNumber`
empty), and the managed `identity` link is deleted (`identity_erased`) — or left keyless
and `disabled` while a non-`deleted` provider link still waits for
`deleteExternalAfterDays`, then deleted on the next retention run once the provider link
is `deleted`. A self-service link (`managed = false`) is left untouched. While `Erase`
is refused (the user is still enabled, is an Owner or a SystemAdmin, or is no longer
managed by Staff) or identity cannot be reached, the link records
`blocked_reason = erasure_incomplete` and raises [`erasureIncomplete`](#attention-items)
under the neutral title `"An erased person"`.

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
settings' [time zone](#the-business-time-zone). Delivery C's account items are read in
the same snapshot, from Staff's own rows.

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
| `onLeaveToday` | People with an employment in force today and an approved or registered absence of whole or part days (not an hour registration) covering today, of **any** kind — a figure without the sick ones would, beside the calendar's bars a colleague reads as "absent", tell them who is sick. A request counts nothing, so no HR-only request is counted. |
| `openSickCases` | Sick-leave cases not closed by HR whose last day is at most sixteen days ago. Sick figure. |
| `sickAbsenceRate` | The sick-leave rate over the twelve months ending today, in percent to two decimals (reading 7), as SSB and NAV count it: the working days lost — over every working day of every employment in force, the day's sick share, counted as in [the payroll file](#the-payroll-export) (by degree, beside leave the part left, a ferie day sick only under a 100 % sykmelding), times the `ftePercent` in force — over the working days agreed: the same days by the same percentage, less the share of each that was holiday or leave. Child sickness is not the employee's and counts nothing. Sick figure. |

### Attention items

`GET /stats/attention` is `staff:access`'s and shaped per caller: `staff:manage`'s
items, `staff:personal-data`'s, `staff:approve`'s and a manager's for the employments
they manage today (by the dashboard's manager rule below); a caller with none of these
gets `[]`, never a 403. A caller with none of `staff:manage`, `staff:personal-data` and
`staff:approve` is judged as a manager on the employments, their terms and the
departments alone, and gets `[]` at once when they manage nobody today; a manager's
read adds only their reports' names, the requests they decide, the open cases'
milestones and their reports' [competence rows](#the-competence-items) and
[profiles](#profiles-and-gaps). Every count and item shown to a caller without `staff:personal-data`
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
| `hrOnlyAwaitingDecision` | personal-data (never manage without it) | Requests of an HR-only type undecided for more than seven days — the caller's own included, so a holder whose own request another holder must decide sees it stall, unless the caller is the sole active holder of `staff:personal-data`, who decides their own ([decisions](#decisions)) and counts it under `absenceAwaitingDecision`. One item; `id` `hrOnlyAwaitingDecision`. | The oldest's seventh day. | `approvals`; `count` the requests. |
| `hrOnlySelfApproved` | personal-data (never manage without it) | An approved absence of an HR-only type its person approved as the sole active holder of `staff:personal-data` ([decisions](#decisions)) that has not ended — read by every holder of `staff:personal-data`, so a holder added later sees it while it lasts (reading 22). One per absence; `id` `hrOnlySelfApproved/<absence id>`; the title the person's name, never the type (`HROnlySelfApprovedAbsences`; `SelfApprovedAbsences` keeps leaving HR-only rows out for `selfApproved`). | When it was approved. | The person. |
| `sickMilestoneDue` | manage, approve; a manager today of the person | A [milestone](#the-arbeidsgiverperiode-and-the-milestones) due — the case open and lasted to within a week of the date, neither done nor waived (pre-flight I5) — dated within seven days or past. The end of the arbeidsgiverperiode is a date, never an action, and raises none; a three-day case raises none. One per milestone; `id` `sickMilestoneDue/<case id>/<kind>`. | The milestone's date. | The person. |
| `holidayUntaken` | manage | In January: a person with an employment in force today whose [holiday account](#the-holiday-account) for last year has days remaining and no carry-over from it recorded — a carry-in [automatic carry-over](#adjustments-and-carry-over) derives counts as recorded (pre-flight M6). One per person. | 1 January. | The person. |
| `competenceDeclared` | manage, approve: everyone's; a manager today: their reports' | A current [competence row](#a-persons-competences) not yet verified — the person's declaration, or a renewal they made — of a person in employment today and not erased; never the caller's own. One per row; `id` `competenceDeclared/<row id>`. | When the row was entered. | The person. |
| `competenceExpiring` | manage: everyone's; a manager today: their reports' (not approve) | A current row whose `validTo` is today or later and no more than its competence's `reminderLeadDays` away — none for a competence without a lead — of a person in employment today and not erased; never the caller's own; raised whatever the [worker](#the-competence-worker) has mailed. One per row; `id` `competenceExpiring/<row id>`. | The `validTo`. | The person. |
| `competenceExpired` | the same | A current row whose `validTo` has passed, on the same terms. Renewing the row supersedes it, and the item goes. One per row; `id` `competenceExpired/<row id>`. | The `validTo`. | The person. |
| `competenceReviewOverdue` | manage: everyone's; a manager today: their reports' (not approve) | A person in employment today and not erased whose [review](#the-review) is overdue: today is after the due day — `competenceReviewIntervalMonths` calendar months after the later of their latest review and the start of their current unbroken employment run. None while the interval is 0; never the caller's own. One per person; `id` `competenceReviewOverdue/<person id>`. A review recorded clears it. | The day the review fell due. | The person. |
| `competenceGap` | manage: everyone's; a manager today: their reports' (not approve) | A person in employment today and not erased with an item not met — every [gap status](#profiles-and-gaps) but `met` and `expiring`, an archived competence's item included — across their unarchived competence profiles, assigned or their department's default; never the caller's own. One per person; `id` `competenceGap/<person id>`. Meeting the items, or archiving the profile, clears it. | Today: a gap is computed on read and has no day of its own. | The person; `count` the items not met. |
| `mainPeriodShort` | manage | From 1 April to 30 September (reading 23): a person with an employment in force today that began before 1 June, with fewer approved ferie working days in 1 June – 30 September than the main period, converted, of the scheme the holiday account reads — the earliest employment's in the year (whole-branch review) — 18 virkedager are 15 days (ferieloven § 7 (1)). One per person. | 1 April. | The person. |

**The account items** (delivery C, `accounts_attention.go`; pre-flight I8) are
`staff:manage`'s alone — never `staff:personal-data` without it, a manager, an Approver
or a colleague — and are derived from Staff's own rows in the snapshot, never by asking
identity: the links as the [accounts worker](#the-accounts-worker) wrote them
(`AccountLinkAttention`: every link with a `blocked_reason`, a `last_error` or a
`disable_requested_at`, of a person not erased — or of an erased one whose link carries
`disable_requested_at` or `blocked_reason = erasure_incomplete`) and one person query (`AccountPersonAttention`). A person's item is listed
only while their flag is on and they are not erased — but for `accountFailing` from a
disable requested and `erasureIncomplete` from an identity erase that has not yet
completed, raised for an erased record too (review M2) under the neutral title
"An erased person" — with `id` `<type>/<person id>`, `entityId` the person, `title`
the person's display name and nothing more (no address, no reason beyond the type) and
`occurredAt` the link's `updated_at` (today for the two person-query items).
For a person whose flag is off, or an erased one (titled "An erased person"), a link
carrying `disable_requested_at` raises the answer the disable met —
`accountNotManaged`, `accountPrivileged` or `accountMismatch`, the user still enabled —
rather than `accountFailing`, and an erased person's link carrying `erasure_incomplete`
raises `erasureIncomplete` whatever the flag; a reason left from when the flag was on raises nothing
once the flag is off.

| `type` | Source | Trigger |
| --- | --- | --- |
| `accountNoEmployment` | the person query | The flag on and no employment at all. |
| `accountEmailMissing` | the person query | The flag on, an employment not ended by today, and the address the flag names (work or private) missing or empty. |
| `accountNotFound` | `blocked_reason = not_found` | The `external` kind: no managed user holds the employee number yet. |
| `accountNotManaged` | `account_not_managed` | The link's user is no longer Staff-managed; the link gives them no Self until HR links the user again by hand. |
| `accountBlockedByOwner` | `blocked_by_owner` | An Owner disabled the pending user. |
| `accountDisabledByOwner` | `disabled_by_owner` | An Owner disabled the user while employed. |
| `accountUserGone` | `user_gone` | The user no longer exists. |
| `accountPrivileged` | `account_privileged` | The user is an Owner or a SystemAdmin. |
| `accountMismatch` | `mismatch` | Identity's employee number is not the person's. |
| `accountModeChanged` | `mode_changed` | A pending user made for the `local` kind, the person's kind now `external`, which never invites. (Under `entra` the announcement completes that user instead, and nothing is reported.) |
| `accountEmailDiffers` | `email_differs` | The user's address in identity differs from the person's after the first invitation was delivered (pending or not): Staff no longer writes it, an Owner does. |
| `accountEmailTaken` | `account_email_taken` | Another user holds the address. |
| `accountNumberTaken` | `employee_number_taken` | An unmanaged user holds the employee number. |
| `accountFailing` | `last_error`, `disable_requested_at` | The flag on and not erased: `attempts` ≥ 3 with `last_error` set, or `blocked_reason` `invalid_actor` (Staff's own fault, folded in) or `external_enabled` (a provider link's delete refused for an account found enabled). Otherwise — the flag off, or erased — only from `disable_requested_at`: set with `last_error`, or set more than ten minutes ago (longer than a claim's lease, so a disable in hand raises nothing) — and from a flag-off leaver's link, which the worker deactivates whatever the flag (the worker's link to a Staff-managed user, the person not erased, every employment ended before today, none later): `attempts` ≥ 3 with `last_error` set, as with the flag on, cleared once the deactivation succeeds. A flag turned off after failures raises nothing otherwise (review L1). |
| `erasureIncomplete` | `erasure_incomplete` | An erased person's Staff-managed user in identity could not yet be erased — the user is still enabled, is an Owner or a SystemAdmin, is no longer managed by Staff, or identity could not be reached; titled `"An erased person"`. |
| `invitationUnanswered` | `invitation_unanswered` | Three invitations unanswered in the spell. |
| `invitationRevoked` | `invitation_revoked` | An Owner revoked the pending user's invitation. |
| `employeeRoleMissing` | `employee_role_missing` | Installation item: one, `id` the type, `entityId` `settings`, `count` the links, `occurredAt` the oldest such write. |
| `employeeRoleUnsafe` | `employee_role_unsafe` | Installation item, as above: a role named for Staff grants administration. Its own item rather than folded into `employeeRoleMissing`: the Owner's remedy differs. |
| `accountModeMismatch` | `mode_mismatch` | Installation item, as above. |

Every `blocked_reason` the worker writes maps to one item — a test walks the `reason*`
constants of `accounts_decide.go` and `accounts_worker.go` — and `no_employment` and
`account_email_missing` are judged by the person query rather than read off the link,
so the item stands while the condition does.

**The provider items** (delivery B, Entra design D7; `accounts_attention.go`) are
`staff:manage`'s alone, the same way, and read from the person's
[provider link](#the-provider-link) as the worker wrote it — `AccountLinkAttention`
answers its `provider`, `announced_at`, `pass_issued_at` and `reported_codes` too —
and from `settings.provider_status`, what the worker last saw of each provider, never
by asking the provider at read time. They are named for Entra, as the design names
them, though their codes are the slot's: a second provider would add its own. One per
person, as above, unless said:

| `type` | Source | Trigger |
| --- | --- | --- |
| `accountNotArrived` | `announced_at` | Staff announced the account more than ninety days ago and the identity link still names no user; `occurredAt` the announcement. |
| `accountAmbiguousInEntra` | `external_ambiguous` | Two accounts in the directory hold the person's employee number. |
| `accountAddressTakenInEntra` | `external_address_taken` | The person's work email is another account's in the directory. |
| `accountSyncedOnPremises` | `external_synced_on_premises` | The account is synchronised from an on-premises directory, whose attributes the provider cannot write. |
| `accountPrivilegedInEntra` | `external_privileged`, or the reported `privileged` | The account holds an administrative role in the directory; an administrator handles it by hand. |
| `accountUnconfirmedInEntra` | `external_unconfirmed` | The directory holds an account under the person's employee number the provider did not create, and nobody with `entra:manage` has confirmed its adoption; the dashboard's sentence links to the Microsoft Entra ID page (`/entra`), where it is confirmed. |
| `hireDateNotWritten` | the reported `hire_date_not_written` | The directory refused the hire date. |
| `licenceUnavailable` | the reported `licence_unavailable:<SKU>` | A licence could not be assigned, none being left — one per person whatever the licences; no title names the licence. |
| `passUnrevealed` | `pass_issued_at` | A pass issued a day ago or more that nobody has shown; `occurredAt` when it was issued. |
| `managerNotInEntra` | Staff's code `manager_not_in_entra`, or the reported `manager_not_found` | The person's manager has no account in the directory, or the directory could not find it, so the account carries no manager. |
| `photoNotWritten` | the reported `photo_not_written` | The directory refused the photo: the provider could not write the [picture](#the-directorys-photo) it was given, and the account keeps whatever photo it had. |
| `entraNotConnected` | the status `unconfigured` | Installation item: the provider has no connection yet — or, the reason `provider_missing`, no enabled module offers the provider a person's kind names. |
| `entraFailing` | the status `failing`, with any reason but `secret_expired` | Installation item: the provider's last check failed — a credential refused (`credential_invalid`), consent withdrawn — or Staff could not ask it (`unreachable`). |
| `secretExpiring` | the reason `secret_expiring` on a status neither `unconfigured` nor `failing` | Installation item: the newest valid client secret ends within thirty days. |
| `secretExpired` | the status `failing` with the reason `secret_expired` | Installation item: it has ended. |

The four installation items are one each, `id` the type, `entityId` `settings`,
`count` the people not erased of the provider's kind whose flag is on and `occurredAt`
when the worker recorded the status — raised only while someone has that kind. A
provider link's `provider_unavailable` raises nothing itself: the provider's state is
the installation's, judged on the recorded status alone. The person items of a
provider link — its reasons, its reported codes, `passUnrevealed` and
`accountNotArrived` — are raised only while the person's flag is on and they are not
erased. The reported `pass_not_allowed` raises none: the Account tab says it beside
the pass, and nothing on the dashboard would let HR act on it. On a provider link
`user_gone` raises `accountUserGone`, `invalid_actor` and `external_enabled`
`accountFailing`, and the announcement's refusals identity's own items
(`accountNumberTaken`, `accountPrivileged`), as on the identity link. A person whose two
links raise the same type gets one item. The walk over the reasons covers the
provider's too, so every code the worker records maps to an item.

**The group item** (delivery D, `accounts_attention.go`) is `staff:manage`'s alone too:
`groupSyncFailed`, one per [group section](#the-sync) whose state is `failed`
(`ListFailedGroupTraits`) — a trait the provider refused, a group the provider may not
write, or three calls in a row that could not be made — with `id`
`groupSyncFailed/<group id>/<provider>`, `entityId` the group's id (the host links it to
the group's page), `title` the group's name and `occurredAt` today, since the row keeps
no date of its failure; the code and the field stay on the group's page. It goes once
the section is no longer `failed`; a retired section keeps its code and raises none.

`id` is `<type>/<employment id>` for the employment items, `<type>/<row id>` for the
three competence row items, `<type>/<person id>` for the overdue competence review, the
competence gap and the other person items, `retentionFailing`, `otpThreshold` and `headcountThreshold/<key>`.
`title` is the person's display name — class C, so it survives the erase — with the
employment number after it, in brackets, on an employment item of a person who has
more than one; for the installation types it is an English sentence, and for the
two request counts the type's name. The host words every type from its own catalog, in
the reader's language, and links a person's item to their page — a competence item
to its Competence tab (`?tab=competence`) — the two request counts
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
| `person_in_use` | 409 | Deleting a person with an employment, a user link, documents, absences, holiday adjustments or competences, or named as a manager. |
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
| `photo_invalid` | 400 | A [photo](#the-photo) that is not a JPEG or PNG by its own bytes, or that `image.DecodeConfig` does not decode as the type it sniffed as; one over 5 MiB; one with a side over 4096 pixels — on `file`. |
| `category_restricted` | 403 | Filing, moving into or out of, or removing a health document without `staff:personal-data`. |
| `category_in_use` | 409 | Archiving a category documents are filed under, or a competence category a competence not archived is filed under. |
| `seed_immutable` | 409 | Changing a seeded document category or competence category; changing a seeded absence type beyond its names, order and `hidden`, or showing the reserved `absent`; changing a seeded competence beyond its names, `reminderLeadDays`, `issuerHint` and `hidden`, or archiving it. |
| `holiday_scheme_in_use` | 409 | Archiving the default holiday scheme, or one an employment names. |
| `absence_type_in_use` | 409 | Widening a company absence type's visibility from `hr_only` to `absent` once an absence names it, or narrowing it from `absent` to `hr_only` while a self-approved absence of it has not ended. |
| `percent_not_allowed` | 400 | A percentage on an absence of a type that allows none — a sick kind among them. |
| `hours_on_day_type` | 400 | Hours on an absence of a type counted in days; an hour type without hours or over more than one day. |
| `absence_note_on_sick` | 400 | A note on an egenmelding, a sykmelding or a sick child's absence. |
| `absence_overlap` | 409 | An absence that cannot stand beside the person's requested, approved or registered rows on one of its days. |
| `absence_not_requested` | 409 | Changing or deleting an absence that is not a request — a registered one by anyone but HR, a decided or cancelled one by anyone; approving or rejecting a row that is no longer a request. |
| `absence_not_cancellable` | 409 | Cancelling an absence that is neither a request nor an approved row its decider may still cancel (begun: HR alone); correcting one that is neither approved nor registered. |
| `own_request` | 403 | Deciding, cancelling or correcting one's own absence, but for the self-approver. |
| `absence_status_change` | 409 | Changing an absence's type between one that needs a decision and one that is a notification. |
| `absence_visibility_change` | 409 | Changing or correcting an absence's type between an HR-only one and any other (`PUT /absences/{id}`, `POST /absences/{id}/correct`); remove or cancel it and enter the other. |
| `carry_over_recorded` | 409 | A second carry-over for one person from one year. |
| `self_certification_exhausted` | 409 | An egenmelding past the policy's days of one occurrence, its twelve months' occurrences or days, or its case's sixteenth sick day. |
| `self_certification_too_early` | 409 | An egenmelding before the policy's months of employment. |
| `self_certification_withdrawn` | 409 | An egenmelding from the day HR decided to withdraw the right until its last day. |
| `holiday_postponement_not_applicable` | 409 | A demand to postpone holiday on a row that is no 100 % sykmelding, or one over no approved ferie. |
| `holiday_postponement_too_late` | 409 | A demand to postpone holiday for sickness that began before it, made after the last working day before the holiday (ferieloven § 9 first paragraph). |
| `competence_held` | 409 | Declaring or adding a competence the person holds a current row of: change or renew that row instead. |
| `competence_unavailable` | 400 | A new competence row naming a competence that is hidden, archived or unknown (on `competenceId`), a new interest naming one (on `interests[i].competenceId`), or a new or changed competence profile item naming one (on `items[i].competenceId`). |
| `competence_in_use` | 409 | Changing whether a company competence has a level once a person's row, or a competence profile's item with a minimum level, names it. |
| `verified_row` | 409 | The person changing or removing their own verified competence row: renew it instead. |
| `superseded` | 409 | Changing, verifying, renewing or removing a competence row a renewal has superseded, or filing its evidence; removed only with its chain. |
| `level_not_allowed` | 400 | A level on a competence row, or a verification, of a competence without one (on `level`); a competence profile item's `minLevel` on one (on `items[i].minLevel`). |
| `level_required` | 400 | No level on a competence row, or a verification, of a competence with one (on `level`). |
| `own_record` | 403 | The person verifying their own competence row, or recording a review of their own, whatever they hold. |
| `skill_no_validity` | 400, 409 | A `validTo` on a skill's competence row — declared, added or changed (400 on `validTo`); renewing a skill's row (409). |
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
| `group_synced` | 409 | `DELETE /groups/{id}` on a group a provider has made or found in its directory, has turned on, or is syncing under a live lease; archive it instead. |
| `group_derived` | 409 | `PUT /groups/{id}/members` on a derived group, whose members are its rule's. |
| `group_archived` | 409 | A change of an archived group: `PUT /groups/{id}`, its members, its owners or a provider's section. |
| `provider_unavailable` | 503 | `PUT /groups/{id}/traits/{provider}` turning on a provider that could not declare its traits: the values cannot be checked, and nothing is written. |
| `threshold_unknown` | 404 | `POST /thresholds/{key}/acknowledge` with a key other than `5`, `30`, `50` and `otp`. |
| `link_managed` | 409 | `PUT /people/{id}/user` naming, or unlinking, a Staff-managed user, or on a link in one of the accounts worker's states — but one blocked `account_not_managed`, which HR may unlink or relink. |
| `account_mismatch` | 409 | `POST /people/{id}/account/disable` on a link whose user carries another employee number: someone else's account, left alone. The flag is off. |
| `accounts_busy` | 409 | `DELETE /people/{id}` while the accounts worker holds the person's `preparing` link; `POST /people/{id}/account/run` and `/disable` while a live lease holds the person's link. |
| `account_not_managed` | 409 | `POST /people/{id}/account/disable` on HR's self-service link (nothing more is done) or on a user Staff does not manage. The flag is off. |
| `account_privileged` | 409 | `POST /people/{id}/account/disable` on an Owner, a SystemAdmin or the last active Owner. The flag is off. |
| `account_kind_locked` | 409 | `PUT /people/{id}/account` changing the kind away from a provider's (`entra`) while the person's link under it names an account the directory has not deleted and not reported gone (`user_gone`). |
| `provider_missing` | 400 | `accountKind` on `PUT /people/{id}/account`, or `defaultAccountKind` on `PUT /settings`, naming a kind that is neither `local` nor `external` nor a provider an enabled module offers (a field error). |
| `pass_gone` | 409 | `POST /people/{id}/account/pass` when the pass was shown already, has expired, or none was issued. |
| `provider_refused` | 409 | `POST /people/{id}/account/run` and `/disable` when the person's provider refused a step; the detail carries its code, never its words. |
| `accounts_unavailable` | 503 | `PUT /people/{id}/user` when identity cannot say whether the user is Staff-managed (nothing changed); `POST /accounts/run` and `/people/{id}/account/run` with no provider or identity unreachable, and `/people/{id}/account/run` when a call of identity's or of the person's provider could not be made; `POST /people/{id}/account/disable` when identity could not be reached, or the directory did not confirm the external account's disable — the flag already off; `POST /people/{id}/account/pass` when no enabled module offers the person's provider or it failed to answer; `/people/{id}/account/run`, and `PUT /people/{id}/account` or `PUT /settings` changing `accountKind` or `defaultAccountKind`, while the account kinds are not yet seeded (nothing changed). |

An unknown id is a bare 404 with no body.

[What the person wants](#what-the-person-wants) and [the review](#the-review) carry no
code of their own: a bad weekday, minute, day, weight, kind or value is a plain field
error at its index (`preferences[1].kind`, `declarations[0].endMinute`), a new interest
in a competence not offered reuses `competence_unavailable`, a review dated after today
is a plain field error on `reviewedOn`, and the person recording their own review
reuses `own_record`. Nor do [profiles and gaps](#profiles-and-gaps) and
[the matrix](#the-matrix): assigning an unknown or archived profile is a plain field
error on `profileIds[i]`, and a department's default naming one on
`competenceProfileId`; a profile assigned twice, or a competence named twice in a
profile, is one on the later index (`profileIds[1]`, `items[1].competenceId`); a
matrix filter that does not pass — a `minLevel` outside 0–4, an unknown
`competenceId` — is one on its name; and a profile's item reuses
`competence_unavailable` and `level_not_allowed`. A [photo](#the-photo) upload with no
`file`, an empty one, two of them or a body that is no multipart form is a plain field
error on `file`; only the file's own rules are `photo_invalid`.

Every mutable row carries a `revision`, starting at 1 and moving on with each change.
A `PUT` requires `If-Match` carrying the revision the row was read at — bare (`3`) or
quoted (`"3"`). A revision that has moved on is `revision_conflict`, whose detail names
both revisions; an `If-Match` that holds no revision is a 400 on `If-Match`. The
revision is judged under the row's lock, so two writes at once queue and the second is
told the first won. Archiving needs no revision: it is idempotent. A person's next of
kin and roles carry no revision of their own: their replace is guarded by the
person's, which it moves on when the list changes. The person's interests, preferences
and availability declaration take no `If-Match` and move no revision: the person is
their only writer, and the last write wins. A competence profile carries its own
revision, which `PUT /competence-profiles/{id}` takes as `If-Match`, its items replaced
under it; the profiles assigned to a person take none and move none — `staff:manage`
is their only writer.

## Locks

- **No call out of a transaction.** The user directory, Time's hours
  ([`contracts.Timesheets`](#contracts-consumed)), the mail and the object store
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
- **An absence's rows.** A new absence takes the person's row, then the absence type it
  names `FOR KEY SHARE` — a type's own change holds it `FOR UPDATE` and never a person,
  so the two queue; a change, removal, cancel or correction takes the person's row, the
  absence's row, then its type `FOR KEY SHARE`, and — for a change or a correction — the
  new type it names `FOR KEY SHARE` too; an adjustment, and its removal, takes the
  person's row, then the adjustment's (`FOR UPDATE`). A withdrawal of egenmelding, or
  its lifting, takes the person's row alone. Overlap, the carry-over's "at most one" and the account an answer
  carries are judged over rows read inside that lock, so two writes for one person are
  judged one after the other.
- **Sick-leave cases.** A sick row's write takes, after the person's row and their
  absences, the person's cases `FOR NO KEY UPDATE` and their milestones, then their
  documents (a merged or removed case's). A case's own write — its notes, a milestone,
  the close — takes the person's row, then the case; one on a case a merge removed
  meanwhile finds it gone (404). A demand to postpone holiday takes the person's row,
  the sykmelding, then each ferie row's type `FOR KEY SHARE`, and the ferie rows it
  splits are written — shortened, inserted or cancelled, each `UPDATE` taking its row —
  under the person's row; a sykmelding's carve of the egenmelding it covers is written
  the same way. A case's milestones are dated by an upsert on `(case_id, kind)` under the
  person's row and the case's, which never touches a milestone's done day, waiver or
  note. A change, removal,
  cancel or correction of an absence takes its type `FOR KEY SHARE` too, after the row
  (a review finding), so a change of the type's own waits. Every case write and every sick
  row of one person are judged one after the other; the case's dates are derived
  inside the lock from the rows read in it, and the egenmelding counters likewise.
- **A group's members.** The materialisation of a derived group, a listed group's
  `PUT /groups/{id}/members` and `DELETE /groups/{id}` take the transaction advisory
  lock `staff:group:<id>`, then the group's row, so two writes of one group's members
  are judged one after the other; none takes a person's row. Every other write of a
  group takes its row alone. A group's section is claimed by the accounts worker under its lease,
  as a link is.
- **A competence row.** A write of a person's competence row takes the person's row,
  then the competence it names `FOR SHARE` — a change of the competence holds its row
  `FOR NO KEY UPDATE` and never a person, so a hide, an archive or a change of its
  level and a new row naming it are judged one after the other — then the row
  (`FOR NO KEY UPDATE`): person → competence → row. A renewal supersedes the old row and
  writes the new under that one lock. The evidence then takes the seeded `certificate`
  category `FOR SHARE`, as a document's upload does; the person's removal of their own
  declared row then takes its evidence document's row. The
  [competence worker](#the-competence-worker) stamps each row in a transaction of its
  own holding that row alone, never a person.
- **What the person wants and the review.** A replace of the interests, the
  preferences or the availability declaration is one transaction under the person's
  row (`withPersonLock`): it reads the list on file, diffs it and writes it there,
  with no `If-Match` — the person is the only writer, and the lock queues two replaces
  of theirs. An interest's replace then takes each competence it newly names `FOR
  SHARE`, as a competence row does; a preference's replace reads the locations a new
  or changed row names. The [Seen](#what-the-person-wants) is one transaction under the
  person's row too — the four stamping statements, the three lists and the current
  competence rows, and its event — and so is a [review](#the-review)'s insert, the
  audience judged again in each under the lock. Nothing of the person's row changes:
  no revision moves.
- **The competence catalogue.** A category's change takes its row `FOR NO KEY UPDATE`,
  which a competence filed under it — added, or moved there — takes too, so an archive
  and a competence filed under the category are judged one after the other.
- **A competence profile.** A profile's change takes its row `FOR NO KEY UPDATE` and
  judges the `If-Match` revision on it; a new profile and a change then take each
  competence an item names `FOR SHARE`, as a competence row does, so a change of the
  competence's level and an item naming it are judged one after the other. An
  assignment takes the person's row (`withPersonLock`), then each profile it newly
  names `FOR SHARE`; a department's write takes the manager graph's lock, the
  department's row, then the default profile it names `FOR SHARE`. So an archive and a
  new assignment or default are judged one after the other: whichever comes second
  waits on the profile's row — an assignment after the archive finds the profile
  archived, an archive after the assignment leaves it assigned and reaching nobody. A
  profile's write takes no person and no department.
- **The photo.** A [photo](#the-photo)'s object is written to the store before the
  person's lock is taken. The row, its revision, the ledger's row for the object it
  replaces or removes and the event are then written in one transaction under the
  person's row (`withPersonLock`, refusing an erased person), the caller judged again
  in it; the store is never called inside it, and the ledger removes the old object
  after it has committed.
- **The glance.** [At a glance](#at-a-glance) takes no lock: Time's `WeekHours` is asked
  after the short snapshot that read the person, the audience and the link has closed
  and before the snapshot that reads the rest opens — through `timesheetsWeek` in
  `contractscalls.go`, which refuses a call made inside a transaction.
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
| `GET /people/{id}/photo` | `staff:access`, then the Work group — everyone who sees the person (otherwise, or with neither HR's photo nor the linked user's avatar, 404) |
| `PUT /people/{id}/photo`, `DELETE /people/{id}/photo` | `staff:access`, then `staff:manage` or `staff:personal-data` — never the person themselves, whose own picture is their Vantigo avatar (otherwise 403; a person the caller may not see 404); the `PUT` rate-limited (`StaffDocumentUpload`) |
| `GET /people/{id}/glance` | `staff:access`, then the [audience](#field-groups-and-audiences) — each section by its group, the contract `staff:manage`'s (otherwise 404) |
| `GET /people/{id}/account` | `staff:access`, then the person, `staff:manage` or `staff:personal-data` (otherwise 404) |
| `PUT /people/{id}/account`, `POST /people/{id}/account/run`, `POST /people/{id}/account/disable`, `POST /people/{id}/account/pass`, `POST /accounts/run` | `staff:manage` |
| `PUT /people/{id}/user`, `GET /assignable-users` | `staff:manage` |
| `PUT /people/{id}/national-id`, `POST /people/{id}/national-id/reveal`, `PUT /people/{id}/bank-account`, `POST /people/{id}/bank-account/reveal` | `staff:personal-data` |
| `GET /me`, `PUT /me`, `GET /me/events`, `GET /me/documents`, `GET /me/export`, `GET /me/groups` | `staff:access`, then a self-service link (otherwise `no_person`) |
| `GET /people/{id}/documents`, `GET /documents/{id}` | `staff:access`, then the person, `staff:manage` (not health documents) or `staff:personal-data` (otherwise 404) |
| `POST /people/{id}/documents`, `PUT /documents/{id}`, `DELETE /documents/{id}` | `staff:access`, then `staff:manage` or `staff:personal-data`, a health document `staff:personal-data` (judged in the module; otherwise 403) |
| `GET /document-categories` | `staff:view`, `staff:manage` or `staff:personal-data` (judged in the module; otherwise 403) |
| `GET /competence-categories`, `GET /competences` | `staff:access` (hidden and archived competences to `staff:manage` alone, when asked) |
| `POST /competence-categories`, `PUT /competence-categories/{id}`, `POST /competences`, `PUT /competences/{id}` | `staff:manage` |
| `GET /people/{id}/competences` | `staff:access`, then the [audience](#field-groups-and-audiences) — the detail to all but the colleague (otherwise 404) |
| `POST /people/{id}/competences` | `staff:access`, then the person (declares), a manager of theirs today, `staff:approve` or `staff:manage` (adds) (otherwise 403; a person the caller may not see 404) |
| `PUT /person-competences/{id}`, `DELETE /person-competences/{id}` | `staff:access`, then the person on their own declared row, a manager of theirs today, `staff:approve` or `staff:manage` (otherwise 403; `verified_row` to the person on a verified row); `chain` `staff:manage` |
| `POST /person-competences/{id}/renew` | `staff:access`, then the person on their own row, a manager of theirs today, `staff:approve` or `staff:manage` (otherwise 403) |
| `POST /person-competences/{id}/verify` | `staff:access`, then a manager of the person today, `staff:approve` or `staff:manage` — never the person (`own_record`; otherwise 403) |
| `POST /person-competences/{id}/evidence` | `staff:access`, then the person on their own row, `staff:manage` or `staff:personal-data` (otherwise 403) |
| `GET /person-competences/{id}/evidence` | `staff:access`, then a reader of the row's detail — the person, a manager of theirs today, `staff:approve`, `staff:manage`, `staff:personal-data` (otherwise, or without evidence, 404) |
| `GET /people/{id}/interests`, `GET /people/{id}/preferences`, `GET /people/{id}/availability` | `staff:access`, then the Wishes group — the person, a manager of theirs today, `staff:manage`, `staff:personal-data` (otherwise 404) |
| `PUT /people/{id}/interests`, `PUT /people/{id}/preferences`, `PUT /people/{id}/availability` | `staff:access`, then the person alone, through their self-service link (otherwise 403; a person the caller may not see 404) |
| `POST /people/{id}/wishes/seen` | `staff:access`, then a manager of the person today or `staff:manage`, never the person (otherwise 403; outside the Wishes group 404) |
| `GET /people/{id}/competence-reviews` | `staff:access`, then the Reviews group — the person, a manager of theirs today, `staff:approve`, `staff:manage`, `staff:personal-data` (otherwise 404) |
| `POST /people/{id}/competence-reviews` | `staff:access`, then a manager of the person today, `staff:approve` or `staff:manage` — never the person (`own_record`; otherwise 403; outside the Reviews group 404) |
| `GET /competence-profiles` | `staff:access` |
| `POST /competence-profiles`, `PUT /competence-profiles/{id}` | `staff:manage` |
| `GET /people/{id}/competence-profiles`, `GET /people/{id}/competence-gaps` | `staff:access`, then the Profiles and gaps group — the person, a manager of theirs today, `staff:approve`, `staff:manage`, `staff:personal-data` (otherwise 404) |
| `PUT /people/{id}/competence-profiles` | `staff:access`, then `staff:manage` (otherwise 403; outside the Profiles and gaps group 404) |
| `GET /competence-matrix` | `staff:view` or `staff:manage` (judged in the module; otherwise 403) |
| `GET /competence-interests` | `staff:manage` |
| `POST /document-categories`, `PUT /document-categories/{id}` | `staff:manage` |
| `GET /absence-types`, `GET /holiday-schemes` | `staff:access` (hidden types to `staff:manage` alone) |
| `POST /absence-types`, `PUT /absence-types/{id}`, `POST /holiday-schemes`, `PUT /holiday-schemes/{id}` | `staff:manage` |
| `GET /absences`, `GET /absences/{id}` | `staff:access`, then the rows the caller may see (otherwise left out, or 404) |
| `POST /absences` | `staff:access`, then the caller's own person or `staff:manage` (otherwise 403) |
| `PUT /absences/{id}`, `DELETE /absences/{id}` | `staff:access`, then the person or `staff:manage` — for an HR-only row with `staff:personal-data` (otherwise 404, whatever the status) |
| `POST /absences/{id}/cancel` | `staff:access`, then the person or `staff:manage` for a request, a [decider](#decisions) for an approved row (otherwise 404, whatever the status; `own_request` to the person on a decided row they may not cancel as decider) |
| `POST /absences/{id}/approve`, `/reject` | `staff:access`, then a [decider](#decisions) of the person — of an HR-only type `staff:personal-data` (otherwise 404; `own_request` to the person) |
| `GET /approvals` | `staff:access`; the requests the caller decides (otherwise an empty list) |
| `POST /absences/{id}/correct` | `staff:manage` (never one's own: `own_request`) |
| `GET /calendar` | `staff:access`, then the people and rows the caller may see |
| `GET /people/{id}/holiday-account` | `staff:access`, then the person, a manager of theirs, the Approver or HR (otherwise 404) |
| `GET /people/{id}/holiday-adjustments`, `POST /people/{id}/holiday-adjustments`, `DELETE /holiday-adjustments/{id}`, `POST /people/{id}/carry-over` | `staff:manage` |
| `GET /me/absences`, `POST /me/absences`, `GET /me/holiday-account`, `GET /me/self-certification`, `GET /me/care-days` | `staff:access`, then a self-service link (otherwise `no_person`) |
| `GET /cases`, `GET /cases/{id}` | `staff:access`, then the person, a manager of theirs, the Approver or HR (otherwise left out, or 404) |
| `PUT /cases/{id}` | `staff:access`, then a manager of the person or HR (otherwise 404); the estimate `staff:manage`'s alone (a 400 on `maxBenefitEstimate` otherwise) |
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
| `GET /stats/attention` | `staff:access`, shaped per caller: `staff:manage`, `staff:personal-data`, `staff:approve` and a manager for their reports (otherwise `[]`) |
| `POST /thresholds/{key}/acknowledge` | `staff:manage` |
| `GET /groups`, `POST /groups`, `GET /groups/{id}`, `PUT /groups/{id}`, `DELETE /groups/{id}`, `POST /groups/{id}/archive`, `PUT /groups/{id}/members`, `PUT /groups/{id}/owners`, `GET /groups/{id}/traits`, `PUT /groups/{id}/traits/{provider}` | `staff:manage` |
| `GET /people/{id}/employments`, `GET /employments/{id}`, `GET /employments/{id}/terms`, `GET /people/{id}/org` | `staff:access`, then the [audience](#field-groups-and-audiences) (otherwise 404) |
| `GET /employments/{id}/contract-check` | `staff:access`, then the person, a manager of theirs or HR (otherwise 404) |
| `POST /people/{id}/employments`, `PUT /employments/{id}`, `POST /employments/{id}/end`, `POST /employments/{id}/reopen`, `POST /employments/{id}/terms`, `PUT /terms/{id}`, `DELETE /terms/{id}` | `staff:manage` |

Every operation also requires `staff:access`. The request and response shapes are in
the contract, [`openapi/staff.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/staff.yaml),
and in the API reference generated from it.
