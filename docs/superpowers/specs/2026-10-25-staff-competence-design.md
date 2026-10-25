# Staff — competence and preferences (phase 2)

The second phase of the `staff` module. Research: `docs/superpowers/research/2026-10-11-staff-module.md`
("R", cited by section) and the discussion note
`docs/superpowers/research/2026-10-25-staff-competence.md` ("N"), whose §3 holds the
sixteen decisions the owner took on 2026-10-24 and 2026-10-25, which this design
implements and does not reopen. Revision 3: revision 1 was the design before review; the
code review and the build of delivery A (2026-10-25) amended it as §13 lists, and the
build of delivery B (2026-10-25) added §13's amendments 16–22; the sections above are
read with §13. Code references
are against `main` at `65a369c6`; `srv/` abbreviates `apps/server/internal/`, `mig/`
`srv/db/migrations/`, `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`, "the reference"
`docs/src/content/docs/en/reference/staff.md`, and phase 1's design
`docs/superpowers/specs/2026-10-11-staff-foundation-design.md` ("P1", its decisions
cited as P1 S*n*).

## 1. Purpose

Let the company answer "who can do this, and who wants to" from the register it
already keeps, and let a person and their manager keep that answer true together:

- HR keeps a flat **catalogue** of competences in categories — skills with a level,
  and certificates with a validity and legal weight, the Norwegian ones seeded;
- a **person's competences** are declared by the person on My page and **verified**
  by their manager, carry their **evidence** in the document archive, and a
  certificate is **renewed** as a new row so history keeps the old;
- a certificate that **expires** before the work does is mailed ahead and shown on
  the dashboard;
- the person writes **what they want** — interests, weighted preferences, an
  availability declaration — and the manager reads it, stamps it **seen**, and the
  two stamp the **review** they held together, so "last reviewed" is known and an
  overdue one is shown;
- HR describes what a position needs as a **competence profile**, assigned to people
  or defaulted by department, and reads the **gaps**; the **matrix** answers who holds
  what, at least which level, valid on a date;
- a **read contract** hands Staffing the competences valid on a date and, later, a
  fit for a proposed booking — never the wishes themselves.

The sequence stands (R §10.11): phase 1 → the Entra module (phase 1 done, phase 2 in
flight) → **this phase** → Staffing. Three deliveries, three pull requests, **one
migration each**, each opened against `main` after the previous landed (the number
assigned when the delivery's first task lands; `00065` is in flight for Energy):

- **A — The catalogue and the person's competences**: categories, competences and
  their seeds, a person's competences with evidence, declaration, verification,
  renewal and removal, the `staff-competence` worker with its mails and attention
  items, the events, the personal-data exports' and the erase's new sections, the
  contract's competence reads, the Competence tab on My page and the person page,
  the Competence settings, the docs.
- **B — The one-to-one**: interests, preferences, the availability declaration, the
  Seen stamps, the review stamp with its interval setting and overdue item, the My
  team cards, the tab's second half.
- **C — Profiles, gaps and the overview**: profiles and their items, assignment to
  people and departments, the gap list and its item, the Competence area with the
  matrix and the interests overview, the matching contract written into the
  reference.

## 2. Decisions

### The catalogue

**S1 — Two kinds in one flat catalogue (N3.1).** `staff.competence_categories` and
`staff.competences`. A competence has a `kind`: `skill` — a proficiency, a level when
`has_level`, never a validity — or `certificate` — issued by someone, with evidence, a
`validity_months` or none, a `reminder_lead_days`, the issuer the law names and the
regulation it rests on, asked one question only: valid on this date. No tree, no
framework (R §6.6). Five categories are seeded in order — `fag` (Trade / Fag),
`sertifikat` (Certificate / Sertifikat), `sprak` (Language / Språk), `verktoy` (Tool /
Verktøy), `domene` (Domain / Domene) — and the Norwegian certificates of R §4.11 are
seeded under `sertifikat` (§3.2). A **seeded** row's `key`, `kind`, `has_level` and
`validity_months` never change (`seed_immutable`, 409, as absence types); its names,
`reminder_lead_days`, `issuer_hint`, `sort_order` and `hidden` do. A **company** row is
HR's whole; its `key` and `kind` are fixed once a person holds it (`competence_in_use`,
409 on the field). A hidden or archived competence is offered to nobody for a new row
and keeps every row that names it, shown with the competence's name and *(archived)*.
Archiving is never refused for rows held (reading 12): history outlives the catalogue.

**S2 — One level scale (N3.2).** 0–4, fixed in code and named in both languages: 0
*kjenner til* / *aware*, 1 *grunnleggende* / *basic*, 2 *selvstendig* / *independent*,
3 *erfaren* / *experienced*, 4 *ekspert* / *expert*. A competence with `has_level`
requires a level on every row and a profile item may set a minimum; one without
refuses a level on the field. No CEFR scale, no per-installation labels (reading 1).

### The person's competences

**S3 — One current row per competence, declared or verified (N3.3).**
`staff.person_competences`: the competence, a `level` when it has one, `issued_on`,
`valid_from` (defaults to `issued_on`, else the day it was entered), `valid_to`, the
`issuer` and a `reference` (the certificate's number), the `evidence_document_id`, the
`source` (`self`, `manager`, `hr`), and the verification: `verified_at`,
`verified_by_user_id`, `verified_basis` (`evidence`, `observed`) and `verified_note` (≤ 200); there is one `level`, which verification may set (S4). A row is
**declared** until `verified_at` is set. A person holds at most **one current row** per
competence: a partial unique index on `(person_id, competence_id)` where
`superseded_by_id is null`; a second declaration of a held competence is
`competence_held` (409) and the client edits or renews instead. A **certificate counts
only verified**: the contract, the matrix's verified filter and the gap list treat a
declared certificate as not held (`declared_only`); a declared skill counts, with the
badge, wherever it is shown (N3.16).

**S4 — Verification is an act in the conversation (N3.3, N§2).** Who may verify person
P's row: P's managers today by the one manager rule (`managersOf`, P1 S12),
`staff:approve`, `staff:manage` — **never P** (`own_record`, 403), with none of the
absence decisions' sole-holder exception. `POST /people/{id}/competences/{cid}/verify`
takes the level the manager agrees to (which may lower or raise the person's), the
`basis` and the optional note, stamps the row and writes `competence.verified`. A
verified row's level, dates, issuer and reference are the verifier's, HR's and
`staff:personal-data`'s to change afterwards, not the person's: the person renews it
(S5) or asks. A manager who does not agree with a declaration **removes** it (S6) rather
than rejecting it; there is no `rejected` state (reading 3).

**S5 — Renewal supersedes, never edits (N3.4).** `POST …/competences/{cid}/renew`
writes a new row with the new `issued_on`, `valid_from`, `valid_to` (proposed as
`issued_on` + `validity_months` when the competence has one, editable), `issuer`,
`reference` and optional evidence, marks the old row `superseded_by_id` and leaves
its dates and evidence as they were; the new row is declared when the person renews
and verified at once when the verifier or HR does (`source` says who). Only the
current row is listed by default (`?history=true` adds the chain, newest first).
**Valid on a date D**: `valid_from ≤ D` and (`valid_to` is null or `D ≤ valid_to`),
on the current row; a superseded row is never valid, whatever its dates.

**S6 — Removal, and the one self-service upload (N§4).** `DELETE
…/competences/{cid}`: the person removes their **own declared** row; a verifier (S4)
or HR removes any current row; a superseded row is never removed on its own
(`superseded`, 409) — HR removes the chain by removing the current row with
`?chain=true`. Each removal is `competence.removed`, naming the competence and who,
in the person's history. Evidence: `POST …/competences/{cid}/evidence` (multipart,
one file, the documents' limits — `documents_validation.go`) is the **first write the
person makes into their own archive**: allowed to the row's person for their own
current row, and to HR as any document. It files the document under the seeded
`certificate` category (class D, not restricted), `employment_id` null, `uploaded_by`
the caller, and sets `evidence_document_id`; a second upload on a row that has one
replaces the reference and leaves the earlier document in the archive. The person
never deletes a document; the one exception is the row they declared themselves: its
removal by the same person also removes the evidence that person uploaded with it,
through the `object_deletions` ledger (reading 4). HR deletes evidence as any
document, which nulls the reference.

**S7 — Expiry: the `staff-competence` worker (N3.11).** A third daily worker, after
`staff-retention` and `staff-accounts`, run at the start of the business day in
`settings.time_zone`, over every **current** row with a `valid_to`:

- `valid_to − today ≤ reminder_lead_days` and `expiring_notified_at` null →
  **expiring**: a mail to the person and one to each of P's manager-deciders
  (`deciders.go` (a): managers whose link names an active user, at the address Staff
  holds), the row stamped;
- `valid_to < today` and `expired_notified_at` null → **expired**: the same mails, the
  row stamped;
- a renewal does not reset the old row's stamps (it is superseded) and the new row
  starts unstamped.

Mails follow `notifications.go`: after the write, outside every transaction, through
`smtpSend`, never failing the run, no address in any log line, in the recipient's
language. The person's mail names the competence and the date; the manager's names
the person too. `reminder_lead_days` defaults, for a seeded or company competence
given a validity and no lead, to **30** when `validity_months ≤ 12`, **60** when
`≤ 24`, **180** otherwise (R §4.11; reading 2), and is editable 0–365. The dashboard
carries `competence_expiring` and `competence_expired` items (§4) without waiting for
the worker: they are computed on read, as every attention item is.

### What the person wants

**S8 — Interests (N3.9, N3.15).** `staff.person_interests`: one row per
`(person_id, competence_id)`, written by the person alone as a whole list (`PUT
/people/{id}/interests`, at most 20), read by the person, their managers,
`staff:manage` and `staff:personal-data`. Its meaning is derived: a competence the
person holds (a current row, declared or verified) is *wants to use more*; one they do
not is *wants to learn*. The manager's **Seen** (`POST /people/{id}/wishes/seen`)
stamps `seen_at`, `seen_by_user_id` on every interest, preference and declaration row
that has none or changed since — the stamp is per row, so a wish added after the
one-to-one is unmistakably new.

**S9 — Preferences (N3.7, N3.8).** `staff.person_preferences`: one row per
`(person_id, kind)`, a `value` (jsonb, validated per kind in Go) and a `weight` 1–3
(*nice to have*, *matters*, *important*), written by the person alone as a whole list,
read as interests are. The kinds of this phase reference Staff's own entities or
nothing:

| `kind` | `value` | Rule |
|---|---|---|
| `weekdays` | `{"days":[1..7]}` | ISO weekdays, 1–7 distinct |
| `time_of_day` | `{"parts":["morning","day","evening","night"]}` | 1–4 distinct |
| `location` | `{"locationIds":[…]}` | 1–10 ids of `staff.locations`, not archived |
| `travel` | `{"extent":"none"\|"regional"\|"national"\|"international"}` | |
| `remote` | `{"share":"never"\|"some"\|"mostly"\|"only"}` | |
| `max_consecutive_days` | `{"days":1..14}` | |
| `max_hours_per_week` | `{"hours":1..60}` | |

No kind carries free text, and no row carries a note: a reason for "no night shifts"
is a health fact within a sentence (N§2). Customer and project wishes and *do not
schedule with* are Staffing's (N3.8). An unknown kind is a field error.

**S10 — The availability declaration (N3.10).** `staff.availability_declarations`:
`kind` (`available`, `unavailable`, `preferred`), a **recurring** row with `weekday`
1–7 and optional `from_date`/`to_date` bounds, or a **dated** row with `weekday` null
and `from_date` required, `to_date` optional (one day when null); `start_minute` and
`end_minute` 0–1440 both or neither (the whole day). Written by the person alone as
a whole list (at most 50), read as interests are. **Data only** in this phase:
nothing in Staff enforces or sums it; whether a declaration is hard or soft, and how
it meets absence and the contract's hours, is Staffing's spec's (R §6.3).

**S11 — The review stamp (N3.14, N3.13).** `staff.competence_reviews`: `person_id`,
`reviewed_on`, `reviewer_user_id`, `reviewer_person_id` (the reviewer's linked person,
when any), written by whoever may verify (S4), never the person, `reviewed_on` today
or earlier, never later. It records that the manager and the person went through the
tab together — no notes, no goals: development goals are the later HR module's
(N3.13). `settings.competence_review_interval_months` (default 12, 0 = off, ≤ 60)
makes a person **review-overdue** when they are in employment today and the latest
of (their last review, the start of their current unbroken employment run) is more
than the interval ago (reading 8); the item goes to their managers and to
`staff:manage`. The person's response carries `lastCompetenceReview` (date, reviewer's
display name) under the reviews group.

### Profiles, gaps and the overview (delivery C)

**S12 — Competence profiles (N3.9).** `staff.competence_profiles` (`key`, names,
`archived`) and `staff.competence_profile_items` (`competence_id`, `min_level` when
the competence has a level, `must_be_valid`, default true), HR's (`staff:manage`),
replaced whole with `If-Match`. A profile reaches a person two ways:
`staff.person_profiles` (HR assigns, a set replaced whole) and
`departments.competence_profile_id`, the default for everyone whose terms in force name
the department. A person's **profiles** are the union; the gap list says which way each
came.

**S13 — Gaps.** `GET /people/{id}/competence-gaps` answers, per profile and item, a
`status`: `met`; `declared_only` (a certificate declared, not verified; a skill
declared counts as met with `declared: true`); `below_level`; `expiring` (met, but
`valid_to` within the lead); `expired`; `missing`. Computed on read from the current
rows, never stored. The dashboard's `competence_gap` item names the person and the
count of items not met, to their managers and `staff:manage`; on the tab, the gaps
head the list so the one-to-one starts with them.

**S14 — The matrix and the interests overview.** `GET /competence-matrix` lists
**active** people (P1's directory rule) with their current rows, filtered by
`categoryId`, `competenceId` (repeatable), `minLevel`, `validOn` (a date; default
today) and `verifiedOnly`; a person is listed when they meet every competence given.
It is the Colleague read: names, levels, validity, the declared badge, nothing more
(N3.6, N3.16). `GET /competence-interests` groups interests by competence into *wants to
learn* and *wants more of*, with the people behind each, for `staff:manage` alone —
the gap-to-train view (R §6.7).

### The seam, the record and the rest

**S15 — The read contract `contracts.Workforce`, Staff the gatekeeper.** A new
interface in `srv/contracts/workforce.go`, Staff its only provider, Staffing its
intended consumer, both named in boundaries.md as rule 12 names Calendar's sources:

```go
type Workforce interface {
    // Competences is the catalogue: every competence not archived, hidden ones
    // included, with its category, kind, hasLevel and validityMonths.
    Competences(ctx context.Context) ([]CompetenceEntry, error)
    // HeldCompetences answers, for each person id, the competences they hold
    // valid on the date: key, level, verified, validTo. A declared certificate
    // is not held. A person the caller may not see is simply absent.
    HeldCompetences(ctx context.Context, on civil.Date, personIDs []int64) (map[int64][]HeldCompetence, error)
}
```

The provider applies the **Colleague rule** to the principal in `ctx` — `staff:view`,
and the person active today — and answers nothing for anyone else, never an error
(rule 12); a context without a request answers nothing. Delivered in A, implemented
over the same queries the matrix reads. The **preferences** are never a method: what
a person wants is scored by Staff into a **fit** for a proposed booking — a number and
the kinds that lowered it, no values — so a planner weighs wishes nobody but the
manager reads (N§4). Its shape, `PreferenceFit(ctx, personID, Booking) (Fit, error)`
with `Booking{From, To civil.Date; Weekdays; Parts; LocationID; HoursPerWeek;
ConsecutiveDays; Travel; Remote}`, is written into the reference by C as the matching
contract's Staff side (R §6.5), and added to the interface with Staffing's first
delivery, since an interface method without a consumer is dead code.

**S16 — Events and history.** New kinds in `staff.events`, each with its payload
classes: `competence.declared`, `competence.changed`, `competence.verified`,
`competence.renewed`, `competence.removed`, `competence.evidence_added`,
`interests.changed`, `preferences.changed`, `availability.changed`, `wishes.seen`,
`competence.reviewed`, `profiles.assigned` (C). The competence kinds carry the
competence's key and level, never the note; the wishes kinds carry which kinds
changed, not the values; `wishes.seen` carries the count. The person reads them all
in History: none is HR's note.

**S17 — Retention and exports (P1 S15).** Every new person row — competences,
interests, preferences, declarations, reviews, profile assignments — is **class D**:
erased with the record after `retentionGraceDays`; evidence is a `certificate`
document, class D already. The catalogue, the profiles and their items are about no
person and have no class. The **D erase** adds the kinds `competences`, `interests`,
`preferences`, `availability`, `reviews`, `profiles` to its `erased` event; the legal
hold blocks it as it blocks the rest. Both **personal-data exports** gain the sections
`competences` (with the evidence documents' details), `interests`, `preferences`,
`availability` and `competenceReviews`; HR's gains `profileAssignments`.

**S18 — No new permission (N3.12).** `staff:manage` keeps the catalogue, the profiles,
the settings and anyone's rows; `staff:approve` verifies and stamps reviews for anyone
and reads the Approver's groups; the self-service link gives the person My page; the
manager by the rule has their reports. The person response's capability block gains
`canVerifyCompetence` (S4's rule for the caller on this person) and
`canReadWishes`; `GET /meta` gains nothing — `managesAnyone` already opens My team.

**S19 — Imports and payroll unchanged.** The register's CSV import and export carry no
competence columns in this phase; the payroll export is untouched; a competence
import is a later entry.

## 3. Data model (schema `staff`)

Every table has `created_at`, `updated_at`; mutable rows have `revision` for
`If-Match` where a client edits them one at a time. Ids are `bigint identity (start
with 1001)`. The last column is the retention class (P1 S15).

### 3.1 `staff.competence_categories` (A)

| Column | Type | Notes | Class |
|---|---|---|---|
| `id` | bigint | | — |
| `key` | varchar(40) not null unique | `[a-z0-9-]`; the five seeds | — |
| `name_en`, `name_nb` | varchar(100) not null | | — |
| `sort_order` | smallint not null | | — |
| `seeded` | boolean not null | | — |
| `archived` | boolean not null default false | a seeded one never | — |
| `revision` | integer not null default 1 | | — |

### 3.2 `staff.competences` (A)

| Column | Type | Notes | Class |
|---|---|---|---|
| `id` | bigint | | — |
| `category_id` → competence_categories | not null | | — |
| `key` | varchar(60) not null unique | `[a-z0-9-]` | — |
| `name_en`, `name_nb` | varchar(100) not null | | — |
| `kind` | varchar(12) not null | `skill`, `certificate` | — |
| `has_level` | boolean not null | | — |
| `validity_months` | smallint | 1–240; certificates; null = none | — |
| `reminder_lead_days` | smallint | 0–365; null without validity | — |
| `issuer_hint` | varchar(200) | the issuer the law names | — |
| `basis` | varchar(200) | the regulation; seeds only | — |
| `sort_order` | smallint not null | | — |
| `seeded`, `hidden`, `archived` | boolean | hidden: seeds, by HR; archived: company rows | — |
| `revision` | integer | | — |

The seeds, under `sertifikat`, kind `certificate`, no level (R §4.11; validity in
months, lead by S7's rule):

| `key` | en / nb | Validity | Lead | Basis |
|---|---|---|---|---|
| `fagbrev` | Trade certificate / Fagbrev eller svennebrev | — | — | opplæringslova |
| `fse` | FSE training with first aid / FSE-opplæring med førstehjelp | 12 | 30 | FSE § 7 |
| `hms-kort` | HMS card / HMS-kort | 24 | 60 | FOR-2007-03-30-366 |
| `truck` | Forklift, certified training / Truckfører, sertifisert opplæring | — | — | fua. § 10-3 |
| `kran` | Crane, certified training / Kranfører, sertifisert opplæring | — | — | fua. § 10-3 |
| `masseforflytning` | Earth-moving machinery, certified training / Masseforflytningsmaskiner | — | — | fua. § 10-3 |
| `dokumentert-sikkerhetsopplaering` | Documented safety training / Dokumentert sikkerhetsopplæring | — | — | fua. §§ 10-1, 10-2 |
| `stillas-bruker` | Scaffolding user / Stillasbruker | — | — | fua. § 17-5 |
| `stillas-bygger` | Scaffolding erector / Stillasbygger | — | — | fua. §§ 17-2–17-4 |
| `forerkort-b` | Driving licence B / Førerkort klasse B | 180 | 180 | førerkortforskriften § 4-1 |
| `forerkort-c` | Driving licence C / Førerkort klasse C | 60 | 180 | førerkortforskriften § 4-1 |
| `forerkort-d` | Driving licence D / Førerkort klasse D | 60 | 180 | førerkortforskriften § 4-1 |
| `yrkessjaforkompetanse` | Professional driver competence (code 95) / Yrkessjåførkompetanse (kode 95) | 60 | 180 | yrkessjåførforskriften |
| `varme-arbeider` | Hot work / Varme arbeider | 60 | 180 | insurers' sikkerhetsforskrift |
| `arbeidsvarsling` | Work zone signage / Arbeidsvarsling | 60 | 180 | N301 (**UNCERTAIN**) |

The scaffolding heights (2–5, 5–9, over 9 m) are one `stillas-bygger` with the height
in the row's `reference`, not three seeds (reading 13). No skill is seeded: skills are
the company's vocabulary.

### 3.3 `staff.person_competences` (A)

| Column | Type | Notes | Class |
|---|---|---|---|
| `id` | bigint | | D |
| `person_id` → people | not null | | D |
| `competence_id` → competences | not null | | D |
| `level` | smallint | 0–4; required iff `has_level` | D |
| `issued_on` | date | | D |
| `valid_from` | date not null | S3's default | D |
| `valid_to` | date | ≥ `valid_from` | D |
| `issuer` | varchar(200) | | D |
| `reference` | varchar(100) | the certificate's number, the height | D |
| `evidence_document_id` → documents | on delete set null | D |
| `source` | varchar(10) not null | `self`, `manager`, `hr` | D |
| `verified_at`, `verified_by_user_id` | timestamptz, uuid | S4 | D |
| `verified_basis` | varchar(10) | `evidence`, `observed` | D |
| `verified_note` | varchar(200) | | D |
| `superseded_by_id` → person_competences | | S5 | D |
| `expiring_notified_at`, `expired_notified_at` | timestamptz | S7 | D |
| `seen_at`, `seen_by_user_id` | timestamptz, uuid | S8's stamp, **added by B** | D |
| `revision` | integer | | D |

Indexes: unique `(person_id, competence_id) where superseded_by_id is null`;
`(competence_id)`; `(valid_to) where valid_to is not null and superseded_by_id is null`
for the worker and the items.

### 3.4 `staff.person_interests` (B)

`id`, `person_id`, `competence_id`, `seen_at`, `seen_by_user_id`; unique `(person_id,
competence_id)`; class D.

### 3.5 `staff.person_preferences` (B)

`id`, `person_id`, `kind` varchar(30), `value` jsonb not null, `weight` smallint not
null (1–3), `seen_at`, `seen_by_user_id`; unique `(person_id, kind)`; class D.

### 3.6 `staff.availability_declarations` (B)

`id`, `person_id`, `kind` varchar(12), `weekday` smallint (1–7, null = dated),
`from_date`, `to_date`, `start_minute`, `end_minute` smallint, `seen_at`,
`seen_by_user_id`; check (`weekday is not null or from_date is not null`), (`to_date is
null or to_date >= from_date`), (`(start_minute is null) = (end_minute is null)`),
(`end_minute > start_minute`); index `(person_id)`; class D.

### 3.7 `staff.competence_reviews` (B)

`id`, `person_id`, `reviewed_on` date, `reviewer_user_id` uuid not null,
`reviewer_person_id` → people; index `(person_id, reviewed_on desc)`; class D.

### 3.8 Profiles (C)

`staff.competence_profiles` — `id`, `key` varchar(40) unique, `name_en`, `name_nb`,
`archived`, `revision`; no class. `staff.competence_profile_items` — `profile_id`,
`competence_id`, `min_level` smallint, `must_be_valid` boolean not null default true;
unique `(profile_id, competence_id)`; no class. `staff.person_profiles` — `person_id`,
`profile_id`; unique; class D. `staff.departments.competence_profile_id` →
competence_profiles, nullable; no class.

### 3.9 `staff.settings` (B)

`competence_review_interval_months smallint not null default 12` (0–60).

## 4. Rules

**Who may verify** (S4): `canVerifyCompetence(caller, P)` = `(caller manages P today
∨ caller.Approve ∨ caller.Manage) ∧ ¬(caller.Person = P)`. The same set stamps the
review (S11) and reads the Seen stamps.

**Who writes wishes** (S8–S10): the person alone, through their self-service link; HR
and the manager read. `staff:personal-data` reads. A PUT of a whole list is refused
with field errors per index (`interests[3].competenceId`), never partially applied.

**Who writes a competence row**: the person declares (`source = self`) and edits their
own **declared** row's level, dates, issuer and reference; a verifier adds
(`manager`) and edits any current row; HR (`hr`) likewise. A verified row is the
person's to read and renew only (`verified_row`, 409 on their edit).

**Attention items** (`attention.go`, shaped per caller as today):

| Kind | To whom | When |
|---|---|---|
| `competence_declared` | P's managers, `staff:approve`, `staff:manage` | a current declared row |
| `competence_expiring` | P's managers, `staff:manage` | current row, `valid_to − today ≤ lead` |
| `competence_expired` | the same | current row, `valid_to < today` |
| `competence_review_overdue` (B) | P's managers, `staff:manage` | S11's rule, interval > 0 |
| `competence_gap` (C) | P's managers, `staff:manage` | any item not `met` |

Only active people's rows raise items. The person sees their own expiring and
expired rows, declarations awaiting and gaps on the tab, not on the dashboard.

**The worker** (S7): one run per business day, idempotent through the two stamps,
skips superseded rows and erased people, logs per run the counts mailed and failed,
never an address. No *Run now*.

**Locking**: every write is one transaction on the module's pool; no contract call
and no mail inside one (`contractscalls.go`). The Seen stamp and the review are single
statements. The whole-list PUTs read the current set, diff it and write in one
transaction under the person's row lock (`FOR UPDATE` on `people`), as the next-of-kin
replacement does.

**Concurrency**: a competence row and a catalogue row take `If-Match` on their
`revision`; the lists (interests, preferences, declarations) take none — last write
wins, the person is their only writer.

**Hidden and archived**: a hidden or archived competence is offered to no new row or
interest (`competence_unavailable`, 400 on the field) and keeps every existing one.
A profile item naming an archived competence stays and is reported.

**The contract** (S15): `HeldCompetences` applies the Colleague rule per person and
omits those the caller may not see; `Competences` needs `staff:view` or
`staff:manage` and answers an empty list otherwise. Never called inside a transaction
(the consumer's rule, rule 12).

## 5. Permissions and visibility

Four new field groups, in `shaping.go`'s table:

| Group | Fields | Self | Colleague (`staff:view`) | Manager | Approver (`staff:approve`) | `staff:manage` | `staff:personal-data` |
|---|---|---|---|---|---|---|---|
| Competence | the competence, level, `validFrom`, `validTo`, `verified` (flag), `superseded` | R, declare, renew, remove own declared | R | R, add, verify, renew, remove | R, verify, remove | R/W all | R |
| Competence detail | `issuedOn`, issuer, reference, evidence document, `verifiedBy`, `verifiedAt`, basis, note, `source`, the notification stamps | R | — | R | R | R/W | R |
| Wishes (B) | interests, preferences, declarations, their Seen stamps | R/W | — | R, Seen | — | R, Seen | R |
| Reviews (B) | the reviews, `lastCompetenceReview` | R | — | R/W | R/W | R/W | R |
| Profiles and gaps (C) | the person's profiles, the gaps | R | — | R | R | R/W | R |

The Approver reads no wishes (N3.15). An absent group is absent from the response and
"changed" in an event payload, as today. The matrix (S14) is the Competence group for
every active person and nothing else; the interests overview is `staff:manage`'s.

## 6. API (`/api/v1/staff`, contract `openapi/staff.yaml`)

Every operation needs `staff:access`; the uniform 403 for anyone outside the audience;
404 for an unknown id or a person the caller may not see, as today.

**A — the catalogue** (`staff:view`, `staff:manage`, `staff:personal-data` read;
`staff:manage` writes): `GET/POST /competence-categories`, `PUT
/competence-categories/{id}`; `GET /competences` (`?includeHidden`, `?includeArchived`,
`?categoryId`), `POST /competences`, `PUT /competences/{id}` (`If-Match`).

**A — a person's competences**: `GET /people/{id}/competences` (`?history`), `POST
/people/{id}/competences` (declare or add), `PUT /people/{id}/competences/{cid}`
(`If-Match`), `DELETE …/{cid}` (`?chain`), `POST …/{cid}/verify`, `POST …/{cid}/renew`,
`POST …/{cid}/evidence` (multipart). My page calls them with the linked person's id,
as it calls the rest.

**B — wishes and reviews**: `GET/PUT /people/{id}/interests`, `GET/PUT
/people/{id}/preferences`, `GET/PUT /people/{id}/availability`, `POST
/people/{id}/wishes/seen`, `GET/POST /people/{id}/competence-reviews`; `PUT /settings`
gains `competenceReviewIntervalMonths`.

**C — profiles and the overview**: `GET/POST /competence-profiles`, `PUT
/competence-profiles/{id}` (items replaced whole, `If-Match`), `GET/PUT
/people/{id}/competence-profiles`, `GET /people/{id}/competence-gaps`, `GET
/competence-matrix`, `GET /competence-interests`; the structure's department PUT gains
`competenceProfileId`.

**Error codes**: `competence_held`, `competence_unavailable`, `competence_in_use`,
`verified_row`, `superseded`, `own_record` (reused), `seed_immutable` (reused),
`level_not_allowed` (a level, or a profile item's `minLevel`, on a competence without
one), `level_required`. A `mustBeValid` item on a competence with no validity is
accepted and always met.

## 7. Frontend (`@vantigo/staff-ui`, the host's `routes/staff`)

- **The Competence tab** (`-person-competence.tsx`), one component rendered on My page
  and on the person page with the capability block deciding the actions, phone first:
  *Competences* — current rows grouped by category, the declared badge, validity and
  days left, **Add competence** (a searchable picker over the visible catalogue, the
  level when the competence has one, issue and expiry dates with the proposed expiry,
  issuer, reference, the evidence file — the camera on a phone), **Renewed**, **Verify**
  (level, basis, note), **Remove**; *History* behind a toggle. **B** adds *What I
  want* — interests (a picker), preferences (one card per kind with its weight) and the
  availability declaration (recurring and dated rows), editable by the person,
  read-only with **Seen** for the manager — and *Reviewed together* with the date and
  **Reviewed today** for the verifier. **C** adds *What your profile asks* at the top
  with the gap statuses.
- **My page**: `Tabs.Tab value="competence"` after Absence (`my-page.tsx:119`).
- **The person page**: the same tab after Absence.
- **My team** (`-my-team.tsx`, B): cards *Awaiting your verification*, *Certificates
  expiring* and *Reviews overdue* over the caller's reports, each row opening the
  person's tab.
- **The Competence area** (C): a new route `/staff/competence` and sidebar entry for
  `staff:view` and `staff:manage` with **Matrix** (filters: category, competences,
  minimum level, valid on, verified only; people as rows) and, for `staff:manage`,
  **Interests** (by competence, who wants to learn and who wants more).
- **Settings** (`settings.tsx`): a **Competence** tab — categories, the catalogue with
  add, edit, hide and archive, the lead days per competence; **B** adds the review
  interval; **C** adds **Profiles** with their items and the departments' defaults.
- **The host**: the route and the tile's entry, the dashboard catalog's strings for the
  five items, the search's **Add competence** (opens My page's tab, as *Request
  absence* does).
- i18n in `en` and `nb` for every label, the level names and the kinds.

## 8. Platform, contracts and documentation changes

- `srv/contracts/workforce.go` (A): `Workforce`, `CompetenceEntry`, `HeldCompetence`,
  and the `Workforce` slot in `module.Deps` resolved for the API process; Staff's
  provider in `srv/staff/workforce.go`; `contractscalls.go` unchanged (Staff consumes
  nothing new).
- The worker runner (`srv/module/workers.go`, `cmd/vantigo`): `staff-competence`
  beside the two.
- **The reference** (`reference/staff.md`): a **Competence** section — the catalogue
  and seeds, the person's rows and their states, verification, renewal, evidence, the
  worker and its mails, interests, preferences and their kinds, the declaration, the
  review, profiles and gaps, the matrix, the contract and the matching contract's
  shape; the permissions table's `staff:approve` row; the field groups table; the
  settings table; the events list; the attention items; the exports' sections; the
  classes column by column; the error codes.
- **The user guide** (`user/staff.md`, `en` and `nb`): My page's **Competence** tab,
  the person page's, My team's cards, the **Competence** page, Settings' **Competence**
  and **Profiles**; one-to-one guidance in the manager's voice.
- **`boundaries.md`**: `contracts.Workforce` under rule 12's providers; Staff still
  depends on nobody but identity.
- **`ROADMAP.md`**: the Staff phase 2 section rewritten from "Later" to its deliveries;
  Staffing's entry names `contracts.Workforce` and the fit.
- `openapi/staff.yaml` and the generated types; the module's sqlc queries; the
  migrations as §3.

## 9. Edge cases

- **A person declares a competence HR hid meanwhile**: refused on the field; the rows
  already held stay and are shown.
- **The manager verifies at a lower level than declared**: the row's level becomes
  the manager's, the event carries both, the person reads it in History.
- **A certificate with no validity is "renewed"**: allowed — a new issue date and
  evidence, the old superseded.
- **`valid_to` before `valid_from`**: refused on `validTo`.
- **A renewal while the old row is expiring-notified**: the new row is unstamped; the
  item disappears because only current rows raise items.
- **The verifier is the person's manager and the person has `staff:approve`**: the
  person still never verifies their own (`own_record`).
- **The sole `staff:manage` holder is everyone's manager and their own**: their own row
  stays declared until another verifier exists; HR's own rows are a known limit of a
  one-person HR, stated in the guide.
- **A department default profile and the terms name no department**: only assigned
  profiles apply.
- **A profile item's competence is archived**: the item stays, the gap list reports
  the item with `archived: true`, HR edits the profile.
- **A hire not yet active** holds competences HR entered: visible to HR and the
  manager, absent from the matrix and the contract (the directory rule) until active;
  Staffing's question for booking ahead (open question 3).
- **Erase**: the D erase removes every new row and the evidence as documents, and
  reports the kinds; a legal hold blocks it. The catalogue is never touched.
- **An evidence document deleted by HR**: `evidence_document_id` nulls; the row stays
  verified if it was — the verification was the manager's act, the document a trace.
- **The worker and a mail server down**: the stamp is per row, written when the run
  handled the row, whatever the sends did, as the absence mails never fail a write; a
  failed send is logged and not retried (reading 5).
- **Preferences PUT with two rows of one kind**: refused on the second's index.

## 10. Testing

- **Go, per rule**: the unique current row; the validity on a date over the chain;
  verification's who (manager, approve, manage, never self); renewal superseding and
  the old row's stamps; removal with the evidence ledger; the lead-days default rule;
  the worker's idempotence and recipients (the fake SMTP, as absence's); the attention
  items per audience; the whole-list PUTs' validation per kind and index; the Seen
  stamp per row; the review-overdue rule over an employment run; the gap statuses;
  the matrix's filters and the active rule; the contract's gate under a request, under
  `AsUser`, and with none; the exports' sections; the D erase's kinds and the hold;
  the field groups per audience (the leaks test pattern, `absences_leaks_test.go`).
- **Frontend**: the tab in each capability state with snapshots; the picker; the
  declaration rows; My team's cards; the matrix's filters; the settings' catalogue.
- **Docs**: `docs:check` with the pages' `sources`; both languages page for page.
- **Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.

## 11. Readings on the record

For the owner's verdict; each is how the design read a decision or a gap.

1. **Level labels are fixed in code**, not an installation's setting: a profile's
   minimum level and the contract's levels must mean one thing across tenants.
2. **The lead-days default is 30 / 60 / 180** by validity ≤ 12, ≤ 24, longer months;
   R §4.11 named 30 for yearly and six months for five-year, this fills the gap between.
3. **A manager declines by removing**, with the event in the person's history, rather
   than a `rejected` state the person would have to clear.
4. **Removing one's own declared row removes the evidence one uploaded with it**, so a
   person who changes their mind leaves nothing behind they cannot reach; HR's
   uploads are HR's.
5. **The person is mailed on expiry as well as the manager** (N3.11, accepted as
   recommended), and a failed send is not retried: the item on the dashboard is the
   durable signal.
6. **An interest's meaning is derived from holding** the competence, not stored.
7. **Preference weights are 1–3** with the labels *nice to have*, *matters*,
   *important*; no kind carries free text.
8. **Review-overdue** measures from the later of the last review and the start of the
   current unbroken employment run, so a new hire is not overdue on day one.
9. **The contract applies the Colleague rule**, so a planner needs `staff:view` and
   sees active people only; a person starting next month is Staffing's question.
10. **HMS-kort's "only while employed"** is not modelled; an offboarding checklist
    (R §8 should) is where its destruction belongs.
11. **The matrix shows declared rows with the badge** and offers *verified only*
    (N3.16); the contract never counts a declared certificate.
12. **Archiving a competence is never refused for rows held**: history outlives the
    catalogue, unlike document categories, whose archive is refused while in use
    because a document must always have a category to be filed under.
13. **One scaffolding seed**, the height in the reference, rather than three.
14. **No *Run now* for the worker**: its items are computed on read, so nothing waits
    for it but the mails.
15. **The whole-list PUTs take no `If-Match`**: the person is the only writer.

## 12. Open questions

1. May the person attach a second evidence document to a **verified** row (a scan
   found later), or only through a renewal? This design allows it (S6: replaces the
   reference, keeps the earlier document).
2. Should languages get a **CEFR** scale later, as a second `level_scale` on the
   competence, and is `has_level` the right column to grow into that?
3. **Booking ahead**: should the contract answer, to `staff:manage`, people not yet
   active, for Staffing to plan a hire's first project? This design says no; Staffing's
   spec decides.
4. Should the expiry mail's **recipients** be a setting (person, manager, both), rather
   than fixed as both?
5. Does the review stamp want a **`kind`** now (`one_to_one`, `onboarding`) so the
   later medarbeidersamtale module inherits typed rows, or is one kind enough until
   it exists?

## 13. Amendments at revision 2 (decided while building delivery A, 2026-10-25)

Each entry names the section it amends; the section's text above stands otherwise.

1. **S1, §5, §6 — the visible catalogue is readable by anyone with `staff:access`.** The
   person declaring from My page needs the picker, and the catalogue holds no personal
   data: `GET /competence-categories` lists every category (archived included, so a row
   can still name its own), `GET /competences` the rows neither hidden nor archived;
   `includeHidden` and `includeArchived` take effect for `staff:manage` only and are
   silently false for anyone else. Writes stay `staff:manage`'s.
2. **S1 — a company competence's `key` and `kind` never change** (the change request has
   neither); `competence_in_use` fires on `hasLevel` changed on a competence a row names.
3. **S1 — a skill never carries a validity.** `validTo` on a skill row is `skill_no_validity`
   (400 on `validTo`) in declaration, change and renewal, and a skill row is never renewed
   (`skill_no_validity`, 409): a skill is edited.
4. **S4 — a manager's or HR's add, and their renewal, are verified at once** with basis
   `observed` and the caller as the verifier. A verifier (or HR) changing a verified row's
   level re-stamps `verified_at` and `verified_by`; the basis and the note stay.
5. **S5 — renewal of a hidden or archived competence is allowed**: a renewal keeps a held
   competence alive, it offers nothing new.
6. **S6 — removing a renewed current row without `chain` restores the row it superseded**
   as current (it undoes the renewal), recorded on `competence_removed` as `restored`; HR's
   `chain=true` removes the whole history. Row operations live at
   `/person-competences/{id}`; the list and the creation under `/people/{id}/competences`.
7. **S6, §5 — the evidence is downloaded through `GET /person-competences/{id}/evidence`**
   by everyone who reads the Competence detail group (Self, Manager, Approver, HR), since
   `GET /documents/{id}` is the Documents group's (Self and HR). Uploading stays the
   person's own current row and HR's; a manager or an approver uploads nothing, and the
   apps offer them no picker.
8. **S7 — the worker polls hourly** (`STAFF_WORKERS_POLL`) and stamps the row in its own
   transaction, with the row locked and re-judged, before mailing, so a mail may arrive
   just after midnight in the business zone; a changed `valid_to` clears both stamps so a
   corrected date is mailed again when due. `STAFF_COMPETENCE_ENABLED` (default true)
   switches the worker.
9. **S15 — `contracts.Workforce` is a sole-provider slot** on `module.Deps`, resolved by
   `Compose` and `Workers`; `HeldCompetences` is gated on `staff:view` alone and answers
   people active today; `Competences` on `staff:view` or `staff:manage`, hidden rows
   included, archived excluded.
10. **S16 — the event payloads** carry `competenceId` (the row) and `fields` with
    `competenceKey` and, per kind, `level`, `source`, `basis`, `validTo`, `chain`,
    `restored`, `documentId`; `competence_renewed` names the level too; the catalogue
    writes installation events `competence_created` and `competence_updated`.
11. **S17 — no new erase kind**: `person_competences` is a whole-D table deleted under kind
    `D`, as `next_of_kin` is; the evidence is erased as documents.
12. **S18 — `canVerifyCompetence` is audience-derived**: `(manager ∨ approver ∨ manage) ∧ ¬self`.
13. **§5's table — the Approver adds, edits, renews and removes** as a verifier does, with
    §4's rule; the table's "R, verify, remove" is read as the verifier's full set.
14. **§7 — Settings is a stack of cards**, so the catalogue is two cards after Document
    categories, not a tab.
15. **§4 — the lead-days default** applies when a validity is given and no lead; a change
    request leaving the lead out resets it to the default.

The following were decided while building delivery B (2026-10-25); the plan is
`docs/superpowers/plans/2026-10-25-staff-one-to-one.md`.

16. **S8, S18, §5 — the Seen set is the manager's and HR's, never the Approver's**: the
    table's "—" for the Approver on Wishes stands, so `POST /people/{id}/wishes/seen` is
    open to `(Manager ∨ Manage) ∧ ¬Self` alone; an Approver who is neither reads no
    wishes and stamps none. The capability block gains `canReadWishes` (S18) **and**
    `canStampWishesSeen`, so the apps carry no audience logic.
17. **S8, §3.3 — the Seen stamp reaches `person_competences`**: the two columns §3.3
    lists as B's are stamped on every **current** competence row with none, so a
    declaration the manager has read but not yet verified is acknowledged. The stamp is
    `seenAt` on each row (the Competence detail group for a competence row), and the
    Seen's response counts the rows stamped per table. The person's own change of their
    declared row, or their evidence upload, clears the row's stamp ("changed since");
    a verifier's or HR's edit or upload does not.
18. **§4 — a whole-list PUT is a diff, not a wipe**: a row equal to an existing one
    (an interest by `competenceId`; a preference by `kind`, `value` and `weight`; a
    declaration by every field) keeps its row and its Seen stamp; a new or changed row is
    inserted unstamped; a row no longer sent is deleted. "Changed since" (S8) is "not yet
    stamped". Catalogue availability — a hidden or archived competence, an archived
    location — is judged on new or changed rows only; an unchanged row is kept.
19. **§3.4–3.7 — no CHECK constraints** on the four tables, as 00042 and 00066 say: the
    module's validation owns §3.6's rules and reports them per field.
20. **§4, reading 15 — no person revision bump and no `If-Match` on the lists**: the
    person is their only writer, and the lists are not under the person's revision.
21. **S16 — the event payloads carry counts and kinds, never values**: `interests_changed`
    the count before and after; `preferences_changed` the kinds added, changed or removed;
    `availability_changed` the count before and after; `wishes_seen` the rows stamped;
    `competence_reviewed` the review's date. Every field is class D.
22. **§6 — no new refusal code**: a bad weekday, minute, date, weight or kind is a plain
    field error at its index (`preferences[1].kind`); `competence_unavailable` is reused on
    `interests[i].competenceId`; a review dated after today is a plain error on
    `reviewedOn`; the review reuses `own_record` for the person on their own.
