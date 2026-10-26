# Staff — the person's photo and the at-a-glance cards

A small addition to the `staff` module, decided with the owner on 2026-10-25 after
Staff phase 2 closed (PR #174). Code references are against `main` at `b217bd0e`;
`srv/` abbreviates `apps/server/internal/`, `st/` `srv/staff/`, "the reference"
`docs/src/content/docs/en/reference/staff.md`, `boundaries.md` the page
`docs/src/content/docs/en/contributing/module-boundaries.md`, and P1 the phase 1 design
`docs/superpowers/specs/2026-10-11-staff-foundation-design.md`.

A follow-up, [the photo follows the account](2026-10-25-staff-photo-sync-design.md), decided
the same day, makes HR's photo the user's picture in Vantigo and in Entra ID and gives the
person's own picture to identity's avatar; it supersedes this design's S1 where the person
uploads into Staff's slot.

## 1. Purpose

A manager opening a report's page should see who they are looking at and the handful
of facts that decide the next conversation, without touring the tabs: the person's
**photo** in the header, and an **At a glance** row of small cards at the top of the
Profile tab — sick days this year, the egenmelding standing, holiday taken, planned and
left, sick-child days, tenure and position, what is happening now and next in absence,
the contract check for HR, and the hours logged this week against the hours the
contract expects. Every card is shaped by the audience rules the module already has: a
card the caller may not read is absent, never zero.

## 2. Decisions (the owner's, 2026-10-25)

**S1 — The photo is Staff's.** P1 §1 deferred a photo with the reading "identity's
avatar is the one picture"; that reading is reopened. Identity's avatar is reachable by
the user and an Owner alone, and many employees never have a user, so the register
keeps its own: one photo per person, uploaded by HR (`staff:manage`,
`staff:personal-data`) or by the person on My page, replaced whole, removed by the
same. It is **class D**: erased with the record after the grace, through the deletion
ledger like a document's object. Everyone who sees the person sees it (the Work group).

**S2 — The hours card reads Time through a contract Time provides and Staff consumes
optionally.** `contracts.Timesheets` answers, per identity user and ISO week, the hours
logged and the hours approved. Staff maps the person to their user through
`identity_links`, gates the card by its own audience rule — the person, their manager
today by the one manager rule, `staff:manage`, `staff:personal-data` — and computes the
expected hours from the terms in force. With `time` disabled `Deps.Timesheets` is nil
and the card is absent, never zero (boundaries.md's nil-slot rule). Staff thereby reads
one business module **optionally**, as Expenses reads Projects; `MODULES=staff` alone
stays a valid installation. The contract is **consumer-gated** like
`contracts.ProjectActuals`: Time performs no authorization, Staff's rule decides, and
boundaries.md says so.

**S3 — A manager sees the count of sick-child days, not the entitlement.** The
`sykt_barn` rows are already the manager's to read (visibility `absent`); their count is
too. The entitlement, the remaining days and the employer split stay the Care-days
group's (the person and HR), as `GET /people/{id}/care-days` is today.

**S4 — The cards.** Sick days this year; the egenmelding standing; holiday taken so far,
planned ahead and remaining; sick-child days; tenure, FTE and probation; absence now
and next; the contract not signed (HR); hours this week against the contract. Not in
this delivery: competence standing (the tab's top already shows the gaps), the People
list's avatars (a follow-up).

**S5 — One read.** `GET /people/{id}/glance` answers every card the caller may read in
one response, so the page makes one call and the shaping lives in the module. The
sections and their audiences are §4's table; each section is a required key only when
the audience reads it, absent otherwise (as the person response drops a group).

## 3. Data model

### 3.1 `staff.people` (three columns, migration `00069_staff_person_photo.sql`)

| Column | Type | Notes | Class |
|---|---|---|---|
| `photo_object_key` | varchar(200) | `people/<person id>/photo/<uuid>` in the `staff` scope | D |
| `photo_content_type` | varchar(40) | `image/jpeg` or `image/png` | D |
| `photo_updated_at` | timestamptz | | D |

No CHECK (the module's validation owns the type). The Down drops the three columns.

### 3.2 `contracts.Timesheets` (`srv/contracts/timesheets.go`)

```go
// Timesheets is the hours a module logs per identity user and week, read by
// another module for a person's at-a-glance figure. Time is its sole provider;
// it is nil when time is disabled, which a consumer reads as "time tracking is
// off" — never as "nothing was logged". It performs no authorization (as
// ProjectActuals): the consumer has decided who may see whose hours.
type Timesheets interface {
    // WeekHours answers, for each user id, the hours of the ISO week starting on
    // weekStart (a Monday, a calendar date at midnight UTC): Logged is every
    // entry not rejected, Approved the approved and invoiced ones. A user with
    // no entry is absent from the map.
    WeekHours(ctx context.Context, weekStart time.Time, userIDs []uuid.UUID) (map[uuid.UUID]WeekHours, error)
}

type WeekHours struct{ Logged, Approved float64 }
```

A sole-provider slot `Deps.Timesheets`, resolved by `Compose` after Time's other
slots (as `Actuals` is), declared on `module.Module` as `Timesheets func(Deps)
contracts.Timesheets`; Time's provider reads its pool and nothing else
(`srv/time/timesheets.go`). Staff's accessor is one function in `st/contractscalls.go`,
called with the request's context, never inside a transaction.

## 4. Rules

**The photo.** `PUT /people/{id}/photo` (multipart, one part `file`): `image/jpeg` or
`image/png` by sniffing (`sniffDocumentType`), at most **5 MiB**, each side at most
**4096 px** judged by `image.DecodeConfig` as identity judges an avatar
(`srv/identity/account.go:495-540`); refused on `file` otherwise (`photo_invalid`,
400, with the reason). Who: the person themselves (through their self-service link),
`staff:manage`, `staff:personal-data` (`canSetPhoto` on the capability block); anyone
else who sees the person gets 403, anyone else 404. The object is put before the
transaction and the row updated under `withPersonLock(refuseErased)`; a replaced or
removed photo's old object is queued in the deletion ledger (`object_deletions`), never
removed inline. `DELETE /people/{id}/photo` by the same set; 204. `GET
/people/{id}/photo` streams the bytes with `Cache-Control: private, max-age=300`,
`nosniff`, no `Content-Disposition`, to everyone who reads the Work group; 404 when
none. The person response and the list item carry `photoUpdatedAt` (the Work group)
so the apps know to fetch and can bust the cache. Events `photo_set` and
`photo_removed` in the person's history, payload `{"fields": {"photo": {...}}}` with
the content type and nothing else, class D. Both personal-data exports gain `photo`:
`{contentType, updatedAt}` or null. The D erase deletes the object through the ledger
(`removeObject` + the columns nulled by the class), the delete of a person created in
error likewise. The photo is not in the register's CSV export or import.

**The glance** (`GET /people/{id}/glance`), computed on read in one snapshot, "today" and
"this year" in the business zone. Sections and their audiences:

| Section | Content | Audience (field group) |
|---|---|---|
| `sickDays` | this year's working days, degree-weighted, of kind `sick_self` and `sick_certified`, approved or registered rows, each as `selfCertified`, `certified` | Absence type: Self, Manager, Approver, HR |
| `selfCertification` | `occurrencesUsed`, `occurrencesLimit` (minimum policy) or `daysUsed`, `daysLimit` (extended) — the standing endpoint's figures | Absence detail: Self, Manager, Approver, HR |
| `holiday` | this year's account: `takenToDate` (approved or registered holiday working days on or before today), `planned` (approved or requested days after today), `remaining`, `overdrawn` | Absence detail |
| `childSick` | `days` this year (kind `child_sick`, registered rows, working days); and, for the Care-days group only, `entitlement`, `remaining` | Absence type; the two extra fields Care days (Self, HR) |
| `employment` | `startDate` (the current unbroken run's start), `tenureMonths`, `ftePercent`, `title`, `probationEndsOn` (when the probation, extended or not, has not ended) | Employment dates for the first three (Colleague too); `probationEndsOn` Employment (Self, Manager, Approver, HR) |
| `absence` | `openCase` `{firstDay, day: n}` when a sick-leave case is open; `now` the absence covering today and `next` the first approved or requested one after today, each `{firstDay, lastDay, shownAs, typeId?}` shaped as `GET /absences` shapes a row for the caller | Absence (the colleague reads `now` and `next` as `absent`, approved rows only); `openCase` Absence detail |
| `contract` | `signed: true|false|null` — the § 14-6 check's verdict for the employment in force (null when no employment is in force) | `staff:manage` |
| `hours` | `weekStart` (this business week's Monday), `logged`, `approved`, `expected` (full-time hours per week × FTE / 100 from the terms in force, rounded to a quarter), `timeTracking: true`; when Time is disabled the section is `{timeTracking: false}` so the app says "off", never shows zero; when the person has no linked user, `logged` and `approved` are absent | Hours (new group): Self, Manager, `staff:manage`, `staff:personal-data` |

A caller who reads none of the groups gets the bare 404, as any person they may not see.

**Shaping.** A new field group **Hours** (`groupHours`, "hours") read by Self, Manager and
HR — the Approver verifies competence and decides absence, not hours. `photoUpdatedAt`
is in the Work group; `photo` (the event field) in Work too. The capability block
gains `canSetPhoto`.

**Locking.** The photo write is one transaction under the person's lock after the
object is stored; the glance is a read snapshot. The contract call happens before the
read transaction opens, with the request's context.

## 5. API (`openapi/staff.yaml`)

`GET /people/{id}/photo` (200 image, 401, 403, 404), `PUT /people/{id}/photo`
(multipart; 200 `StaffPerson`, 400 problem `photo_invalid`, 401, 403, 404, 409
`person_erased`), `DELETE /people/{id}/photo` (204, 401, 403, 404, 409); `GET
/people/{id}/glance` (200 `StaffPersonGlance`, 401, 403, 404). `StaffPerson` and
`StaffPersonListItem` gain `photoUpdatedAt?`; `StaffPersonCapabilities` gains
`canSetPhoto`; `StaffPersonalData` gains `photo` (nullable object, required key).
`StaffEvent.kind` names `photo_set`, `photo_removed`. The new code `photo_invalid`.

## 6. Frontend (`@vantigo/staff-ui`)

- **The header** of the person page and My page: a round `Avatar` (the photo when
  `photoUpdatedAt` is set, the initials otherwise) beside the name; for `canSetPhoto` a
  menu on it — **Change photo** (a file input, JPEG or PNG, the camera on a phone),
  **Remove photo**.
- **At a glance**: a `SimpleGrid` of small cards at the top of the Profile tab, one per
  section the response carries, each a figure, a one-word label and a line under it
  (e.g. *12 sick days this year — 9 egenmelding, 3 sykmelding*; *Holiday: 14 taken,
  5 planned, 6 left*; *Hours this week: 31.5 of 37.5*, or *Time tracking is off*); a
  card links where its tab is (Absence, Employments, Documents for the contract).
- i18n `en` and `nb`.

## 7. Documentation

The reference: the schema paragraph, the Work group's `photoUpdatedAt`, the Hours
group, the capabilities, **The photo** and **At a glance** sections under People, the
endpoints, the refusal `photo_invalid`, the locks, the classes column
by column, the exports, the events, the Timesheets contract under a new **Contracts
consumed** paragraph; the user guide `en`+`nb` (the header's photo, At a glance, My
page's photo); `admin/installation.md` `en`+`nb` (00069); boundaries.md (Staff reads
Time optionally through `contracts.Timesheets`, consumer-gated; the slot list; Time
provides it); the Time reference's contracts section; `ROADMAP.md` (a line under Staff).

## 8. Readings on the record

1. **The photo is the register's, not identity's** (S1), reopening P1's reading.
2. **`contracts.Timesheets` is consumer-gated**, as `ProjectActuals` is, because the rule
   that decides who sees whose hours on a person page is Staff's manager rule, which
   Time cannot know; the boundaries page records the two consumer-gated contracts as a
   class of their own beside rule 12's provider-gated reads.
3. **A Staff manager sees their report's weekly total** without `time:view-all`, through
   Staff's page alone; Time's own screens are unchanged.
4. **Sick-child days are counted for the manager, the entitlement is not** (S3).
5. **Expected hours are the contract's**: full-time hours × FTE, not reduced by absence
   or holidays — the card compares against the agreed week, and the absence cards say
   why a week is short.
6. **"This year" is the calendar year in the business zone**; "this week" the ISO week
   containing today.
7. **`photoUpdatedAt` is the cache key**: the apps fetch `GET /people/{id}/photo?v=<updatedAt>`;
   the server ignores `v` and answers `Cache-Control: private, max-age=300` (the plan).
8. **The photo's event payload carries the content type only** (`{"fields": {"photo": {"to":
   "image/jpeg"}}}`, `photo_removed` with `from`), class D, in the Work group (the plan).
9. **The glance's `absence.now`/`next` reuse the absence row's shaping** — the type to its
   readers, `leave` for an HR-only type to all but Self and `staff:personal-data` — and a
   colleague gets the coalesced `absent` spans `GET /absences` gives them, never a row's own
   boundaries (the plan, amended by the server review).
10. **Hours are the user's across every project**, whatever the caller's project rights: the
    week's total, no per-project split, no entries (the plan).
11. **No new Time permission or endpoint**: Time's provider is a pool read, and Time's own
    screens and rules are untouched (the plan).
12. **Sick days count the whole calendar year**, registered days after today included, each
    working day weighted by its degree, reduced by leave on the same day and zero under a
    holiday-kind row — the payroll file's per-day rule.
13. **The holiday card's three figures do not sum to the entitlement**: `takenToDate` is the
    approved and registered days from 1 January to today, `planned` the approved, requested
    and registered days from tomorrow to 31 December, both over the account's rows and
    shares, and `remaining` the account's, which already subtracts every approved and
    registered day whatever its date.
14. **`childSick.days` is every registered child-sick working day this year**, the days
    before the four weeks' right included, while `entitlement` and `remaining` are the
    care-days endpoint's, for the Care-days group and only when the fields are recorded.
15. **`absence.now` is the day row covering today** — requested, approved or registered,
    seen through the caller's absence rule — a decided row before a request, then the
    earliest start, then the lowest id; `next` is the earliest start after today; an hour
    row is neither.
16. **`openCase` is the newest open case whose first day is not after today**, `day`
    counting the first day as day 1.
17. **`title`, `ftePercent`, the probation and `contract.signed` come from the person's
    place today** — the earliest employment in force by start, then id — the probation
    being the extension's end when agreed and shown only while it is today or later, and
    `signed` the employment's contract-signing stamp, null with no employment in force.
18. **`expected` sums over every employment in force today** (two 50 % posts expect a full
    week), each terms row's full-time hours × FTE / 100, rounded to a quarter of an hour.
19. **A linked user with no entries this week reads `logged: 0`, `approved: 0`**, and a
    person linked to no user has no `logged` or `approved`.
20. **The contract card is a field group of its own, Contract**, read by `staff:manage`
    alone — not by `staff:personal-data` without it.
21. **The glance is read in two snapshots with Time's hours read between them**, the
    audience judged in both, and a failing Time provider fails the whole glance rather than
    leaving the card out (amending §4's "one snapshot").
22. **The egenmelding card is the standing's twelve months**, as
    `GET /people/{id}/self-certification` reports it, not the calendar year.
23. **A replaced or removed photo's object is queued in the deletion ledger inside the
    row's transaction and never removed inline**; the delete of a person created in error
    and the D erase queue it the same way.
24. **Sniffing alone decides a photo's type, and `image.DecodeConfig` must agree**; a
    missing, empty, unreadable or doubled `file` part is a plain field error with no code,
    and `photo_invalid` is for the type, the size and the dimensions.
25. **A photo's metadata never reaches the store**: JPEG Exif/XMP (`APP1`) and `APP13`
    segments and PNG `eXIf`, `tEXt`, `zTXt` and `iTXt` chunks are cut out before it is
    stored, without decoding the pixels, so a phone's location is not kept.
26. **The photo upload carries the documents' rate limit** (`StaffDocumentUpload`, 429 past
    it).
27. **GET and PUT answer 503 `storage_unavailable` when the store fails**, and a GET whose
    object the store no longer has is the bare 404.
28. **The header shows the avatar beside the name**, the title and department under it, and
    for `canSetPhoto` a menu, the button named *Photo of …* (nb *Bilde av …*), with **Change
    photo** and **Remove photo** (nb **Bytt bilde**, **Fjern bilde**); a phone offers the
    camera or the library.
29. **The cards are Sick days, Egenmelding, Holiday, Sick child, Position, Absence, Contract
    and Hours this week** (nb *Sykedager*, *Egenmelding*, *Ferie*, *Sykt barn*, *Stilling*,
    *Fravær*, *Arbeidsavtale*, *Timer denne uken*), each but the hours card linking to the tab it sums up; an
    empty Absence card says *Nothing now or planned*, and the hours card *Time tracking is
    off* or *No user linked*.
