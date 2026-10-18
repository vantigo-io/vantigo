# Time zones — design

Vantigo has four answers today to "which day is it": Staff's business time zone
(`staff.settings.time_zone`), Expenses' own copy (`expenses.settings.time_zone`),
Invoices' and Projects' hard-coded `Europe/Oslo`, and the UTC calendar day Customers'
follow-ups, Time's entries and Projects' dashboard figures use. Nothing checks that
they agree, and no user can say where *they* are. A Norwegian installation never
notices: every answer is Oslo or two hours off Oslo at night. The calendar module
([`../research/2026-10-19-calendar-module.md`](../research/2026-10-19-calendar-module.md),
§6) would notice every evening, when a follow-up due "today" in UTC sits beside an
appointment at 23:30 Oslo on a different day.

This design makes it two zones with two owners, and leaves every stored row as it is:

- the **business time zone**, one per installation, the calendar every bookkeeping
  day is judged by — owned by the workspace, read by every module through a contract;
- the **personal time zone**, one per user, how an instant is shown to that person and
  what "09:00" means when they schedule something — owned by identity, following the
  business zone until the person chooses.

## Decisions

### D1 — Storage does not change: instants are UTC, dates are civil, the zone is a derivation

Every instant column in the schema is already `timestamptz`, which PostgreSQL stores
as UTC (`grep` finds no bare `timestamp` column in the migrations). An appointment's
start, an entry's `occurred_at`, a trip's `departure_at`, every `created_at` — all
UTC, and none of this design touches them.

Every **date** column — an invoice's `issue_date` and `due_date`, an absence's
`first_day`, a follow-up's `follow_up_on`, a time entry's `entry_date`, a task's
`due_date` — is a **civil date**: a fact of the company's calendar with no instant
behind it. "The invoice was issued on 1 October" is not "at some UTC moment"; it is
the day the books say. A civil date is not stored in any zone and is never converted,
so it needs no migration either.

The zone only ever enters at two moments, and both are computed, never stored:

1. **Deriving a day from an instant** — "today" from the server clock, "the day this
   trip departed" from `departure_at`, "the day this milestone became ready" from
   `ready_at`. This is the business zone's job (D2), and it is where the four answers
   disagree today.
2. **Showing an instant to a person**, or taking "09:00" from them. This is the
   personal zone's job (D5).

So a change of either zone later changes no existing row. A changed business zone
changes what "today" is from then on and which day *new* instants are bucketed into;
a day already derived and written as a date (Expenses' per-diem days, an invoice's
issue date) stays what it was, which Expenses already documents as the one thing a
zone change can do and the submit is the last place to notice. A changed personal
zone changes only what the person sees.

One exception is named rather than hidden: Time's `start_time`/`end_time` are
`time` without zone — a wall-clock time of day with no instant. D7 defines them as
wall-clock in the business zone and leaves them so.

### D2 — One business time zone, on the workspace, read through a contract

The business zone moves to identity's `system_settings` row — the one table that
already holds what is true of the installation as a whole (maintenance mode) and is
always migrated, since identity is always on — as `business_time_zone varchar(64)
NOT NULL`. Identity is the right owner because every module may depend on identity
and identity depends on none; Staff could not have served Invoices.

Modules read it through a new contract beside `Clock`, so no module reads identity's
table and no module keeps a copy:

```go
// contracts.BusinessCalendar is the installation's calendar: the zone every
// bookkeeping day is judged in, and the derivations every module made for
// itself until now. identity implements it over system_settings; Compose
// wires it into every module's Deps, always set, like UserDirectory.
type BusinessCalendar interface {
    // Zone is the business time zone. A stored name Go cannot load (written
    // by hand, or dropped by a Go release) answers UTC rather than failing
    // the request, as Staff's zoneOf does today; setting it again is the way back.
    Zone(ctx context.Context) (*time.Location, error)
    // Today is the business calendar day of now, as a date at midnight UTC —
    // the form every date column is compared at.
    Today(ctx context.Context, now time.Time) (time.Time, error)
    // Day is the business calendar day an instant falls on, in the same form.
    Day(ctx context.Context, t time.Time) (time.Time, error)
}
```

The arithmetic behind `Today` and `Day` — Expenses' `businessDay(t, loc)` and
Staff's `civilToday(now, zone)`, the same function twice — moves into the platform
package `internal/holidays` as `holidays.DayIn(t, loc)`, next to the public holidays,
so the package is "which days the company works, and which day an instant falls on".
It stays pure (its purity test allows `time` only).

The value is read per request, not cached across them: a `SELECT` of one row by
primary key, as Staff's `authorize.go` does today, so a change applies to the next
request. A module that already resolves the zone once per request (Staff, Expenses)
calls `Zone` once and keeps the `*time.Location` for the request.

SQL that buckets an instant into a day takes the zone **as a parameter** from the
same resolved name — Expenses' `(departure_at AT TIME ZONE @time_zone)::date` is the
pattern — never a literal and never `current_date`, so Go and the database cannot
disagree. Postgres is consulted about the name only at write time (D3).

### D3 — Setting it: the workspace's, validated twice, audited

`GET /api/v1/identity/owner/workspace` and `PUT /api/v1/identity/owner/workspace`
with `{ "businessTimeZone": "Europe/Oslo" }`, behind `policy:Owner`, the access the
owner's other operations declare. The PUT:

- refuses a name Go cannot load, and `Local` and `""` (they say nothing about the
  company's calendar), with a field error on `businessTimeZone`;
- asks the database what it means by the same name at a winter and a summer instant
  of the current year and refuses a disagreement — `CET` is a fixed +01:00 to
  Postgres and a zone with summer time to Go — worded as Staff's message is today
  ("use a city zone such as `Europe/Oslo`"). Staff's `zoneDisagreement` moves to
  identity unchanged;
- writes an authorization audit event, `workspace.business_time_zone_changed`, with
  `from` and `to`, since the day every module judges just moved;
- answers 200 with nothing written when the value is the one already held.

The host's **Workspace administration** gains a **Workspace** section with the one
field, and a sentence saying what it governs and that existing dates are untouched.
The setup flow (`/identity/bootstrap`) keeps the default `Europe/Oslo`; the first
administrator changes it there if the company is elsewhere.

**The migrations.** The cross-schema scan (`internal/db/schema_test.go`) refuses a
migration owned by one module that names another module's schema, and identity is
one of the schemas it guards, so there is no migration that reads Staff's value and
writes identity's. Three module-owned migrations instead, with no seed across
schemas:

- `00052_identity_workspace_time_zone.sql` adds `business_time_zone` to
  `system_settings` with default `Europe/Oslo`, and `time_zone` (nullable) to `users`
  (D5).
- `00053_staff_time_zone_to_workspace.sql` drops `staff.settings.time_zone`. Before it
  does, a `DO` block raises a `WARNING` naming the value when it is not `Europe/Oslo`
  — "staff's business time zone was %s; set the workspace's under Workspace
  administration" — so an operator reading the migration log is told. The Down
  restores the column with its default.
- `00054_expenses_time_zone_to_workspace.sql` does the same for
  `expenses.settings.time_zone`.

The upgrade note in the administration guide says it in one line: an installation
whose business zone was not `Europe/Oslo` sets it once under Workspace
administration after upgrading. Every installation today runs the default, so no
real installation has a value to lose; the warning is for the one that might.

Staff's `PUT /settings` and Expenses' `PUT /settings` lose `timeZone` from their
request bodies (a contract change: the API reference regenerates). Both `GET
/settings` keep answering `timeZone`, now the platform's, marked read-only in the
contract, so the two settings screens keep showing it with a hint linking to
Workspace administration rather than an input. Staff's `GET /meta` keeps answering
`timeZone` and `today` unchanged; it is the app's notion of today and nothing about
that moves.

### D4 — What the session answers

Every page already loads `GET /api/v1/identity/session`. Its user gains two fields,
so no screen needs a second call to know how to render an instant:

```json
"timeZone": "Europe/Oslo",          // the effective personal zone (D5)
"timeZoneSource": "business",       // "personal" when the user chose one
"businessTimeZone": "Europe/Oslo"
```

### D5 — The personal zone: on the user, null means "follow the business zone"

`identity.users.time_zone varchar(64) NULL` (migration `00052`, with D3's column). `NULL`
means **follow the business zone**, resolved at read time. It is not a copy taken when
the user is created: a copy goes stale the day the business zone changes, and a user
created by SCIM or by Staff's account provisioning has nobody to choose for them.
The effect is what was asked — a new user is on the company's zone — without a
second source of truth.

- `PUT /api/v1/identity/account/profile` and its PATCH accept `timeZone`: an IANA
  name Go can load, or `null` to follow the business zone again. The same two checks
  as D3 apply (Go loads it; Postgres agrees), because a personal zone is also handed
  to SQL wherever a query buckets by the viewer's day (none today; the calendar's
  range queries will).
- The account's **Personal settings** page, beside the preferred language: a select
  of IANA zones with "Same as the workspace (Europe/Oslo)" as the first and default
  choice.
- **SCIM**: the core user schema's `timezone` attribute, when the identity provider
  sends it, is written as the user's zone, and advertised in `/Schemas`; a value Go
  cannot load is ignored with the request otherwise applied, never refused, since an
  IdP's push must not fail on a cosmetic field. **UNCERTAIN** whether any IdP in use
  sends it; it costs one attribute mapping and may be left out of the first delivery.
- `contracts.UserDirectory` does **not** grow a zone method in this design. The
  calendar needs the viewer's zone (the session) and, later, an invitee's for an
  invite mail's text; the ICS itself carries UTC or a `TZID` and the client renders
  it. That method comes with that mail, with the same "mail them, do not hand the
  value over" shape `PermissionHolders` has for email.

### D6 — Which zone decides what

| Question | Zone | Why |
| --- | --- | --- |
| What day is it, for an invoice's issue date, a due date, a period lock, the holiday account, the a-melding, the payroll file, a follow-up's "overdue", a dashboard's "today" | **business** | A fact of the company's books; the same for every reader |
| Which day did this instant fall on (a trip's departure, a milestone's readiness, an entry's `occurred_at` against its `occurred_on`) | **business** | Same fact, derived from an instant |
| What time does this instant show as, in a list, a timeline, an audit log | **personal** | The viewer's |
| What instant is "Tuesday 09:00" when a person types it (an appointment, an `occurredAt`) | **personal** | The writer's; stored as UTC |
| Which wall-clock does a recurring appointment keep across the clock change | the **appointment's own `TZID`**, chosen as the creator's personal zone at creation | Decided in the calendar design, noted here so the two are not confused |
| Energy's consumption buckets | the price area's **market zone** | A different concept; untouched |

A personal zone never moves a bookkeeping day. An accountant working from Spain
issues invoices on the Oslo day, and sees the invoice's `issued_at` instant in
Spanish time.

### D7 — The modules that change, and what each change means

Each is its own delivery (below), independent of the others. None touches a stored
row; each changes a derivation from "the UTC day" or "Oslo" to the business day, and
the only user-visible effect is a day boundary moving from UTC midnight to the
business zone's, which is the correction.

- **Invoices** drops its `oslo` variable and `businessDay` for `Deps.Calendar`. D1 of
  the invoices foundation design ("Norwegian bookkeeping, so there is no setting")
  keeps its reason — bookkeeping has one calendar — and that calendar is now the
  installation's rather than Norway's, which for every Norwegian installation is the
  same day. Due-date arithmetic and reminder deadlines move no differently.
- **Projects** passes the zone into `invoiced.sql` as a parameter where it writes
  `AT TIME ZONE 'Europe/Oslo'` twice, and its dashboard `today` (`stats.go`, UTC
  midnight) becomes the business day.
- **Time** computes `today` on the business day, and the reference states what
  `start_time`/`end_time` are: **wall-clock in the business zone**, the time of day at
  work, which the calendar projects as such. No column changes.
- **Customers** replaces `civilDate`'s UTC day with the business day in the two places
  it is used: the rule that `occurredAt`'s date must equal `occurredOn`, and the
  follow-up's overdue/due-today split (the attention list, `state=overdue`). The
  reference's paragraph on "a Norwegian reader at 00:30 on Tuesday, two hours into a
  day the server still calls Monday" is rewritten to say the opposite: the day is the
  business day, so the two agree. An existing entry whose `occurredAt` and
  `occurredOn` were consistent in UTC and are not in Oslo (an entry at 23:30 UTC) is
  left as written; the rule is checked on writes only, as it is today, and a later
  `PUT` that re-sends both unchanged is not re-validated against the new day — the
  same division the follow-up's assignee already makes between validating a change
  and re-rendering stored state. The timeline's "· 10:00 UTC" suffix goes (D8).
- **Staff** and **Expenses** swap their own column for the contract and lose their
  `PUT` field (D3). Their derivations (`civilToday`, `businessDay`) are replaced by
  the contract's; their tests that set a zone through `PUT /settings` set it through
  the workspace endpoint, or through the test seam `Deps` offers.

### D8 — The frontend shows instants in the personal zone

`packages/frontend-shell`'s formatters (`i18n/format.ts`) format with `Intl` in the
**browser's** zone today. They take the zone from the session (D4) instead — one
`timeZone` option set on every `Intl.DateTimeFormat` the shell builds — so a person
who chose a zone sees it everywhere at once, and a person who chose none sees the
business zone rather than wherever their laptop happens to think it is. Date-only
values keep the existing `formatDateOnly` convention (`T00:00:00Z` with `timeZone:
"UTC"`): a civil date has no zone and must never shift. Date pickers that take an
instant (`occurredAt`, a trip's departure) build it in the personal zone
(`@mantine/dates` with dayjs's `utc` and `timezone` plugins, already in the catalog).
Customers' timeline drops the literal "UTC" after an entry's time.

### D9 — What a later change of either zone does

- **Business zone changed**: from the next request, "today" and every instant-to-day
  derivation use the new zone. Dates already written stay. Expenses' already-documented
  consequence (a per-diem day left outside its own trip) is unchanged and is still
  caught at submit. The audit event records it. The Workspace section says so in one
  sentence before the field.
- **Personal zone changed**: the person sees instants in the new zone from the next
  page load. Nothing else.
- **Neither** reaches into Energy's market zone, the holiday calendar (Norway's,
  unchanged) or any stored row.

## Deliveries

Each delivery is a commit series with its own documentation; A is the prerequisite
for the rest, and the calendar module depends on A and F.

**A — The workspace's business time zone.** `BusinessCalendar` in `contracts`,
`holidays.DayIn`, identity's implementation over `system_settings`, the owner
endpoints with validation and audit, the session's three fields, migrations `00052` to
`00054` (the two columns, the two drops), Staff's and Expenses' switch to the contract
and the read-only `timeZone` in their settings responses, the host's Workspace
section, and the two settings screens' hint. Docs: `admin/index.md`, `admin/staff.md`
(the business time zone moves out), `admin/authentication.md` or a new
`admin/workspace.md` (**UNCERTAIN** which; the audit event and the setting belong
with the owner's other procedures), `reference/staff.md` ("The business time zone"
section rewritten to point at the platform), `reference/expenses.md` ("The per-diem
day"), `contributing/platform-packages.md` (`DayIn` under "The holidays"),
`contributing/module-boundaries.md` (the contract in the list), `user/
workspace-administration.md`, `user/staff.md` and `user/expenses.md` — the user and
admin pages in both languages.

**B — Invoices on the contract.** Docs: `reference/invoices.md` ("The rules").

**C — Projects on the contract.** Docs: `reference/projects.md`.

**D — Time on the contract.** Docs: `reference/time.md`, `user/time.md` in both
languages (one sentence on what the start and end times mean).

**E — Customers on the contract.** Docs: `reference/customers.md` ("Follow-ups",
"The timeline"), `user/customers.md` in both languages.

**F — The personal time zone.** The nullable column (already in `00052` with A,
built after A), the profile endpoints, the
Personal settings control, the session's effective zone, the shell's formatters and
pickers, the SCIM attribute if kept. Docs: `admin/authentication.md` (the user
record's fields), `admin/sso-scim.md` (the attribute), `user/getting-started.md` (the
personal settings), in both languages.

B to E are a day each and may land in any order once A is in; F may run beside them.

## Out of scope

- A per-user zone for *bookkeeping* (a branch office in another zone keeping its own
  books): the business zone is one per installation, as the installation is one
  company.
- A per-module zone: the two that existed are the reason for this design.
- Changing Energy's market zones or the holiday calendar's region.
- The calendar module's own appointment zone (`TZID`) and recurrence: its design.
