# Time module

The Time module is where the hours are: who worked, on which project and billing line,
for how long, what it should be billed at and what it cost. It is a vertical-slice
module inside the single Vantigo binary (`apps/server/internal/time`, package clause
`timetracking` so nothing in it shadows the standard library), owns the `time` schema
in the shared PostgreSQL database, and serves `openapi/time.yaml` under
`/api/v1/time`.

It sits on top of [Projects](projects.md): hours hang off a project, optionally a
billing line and a task, and `time` without `projects` is a startup error. It stops
short of invoicing. Time **snapshots** the rate that applied when the hours were
submitted and marks entries invoiced when somebody tells it they were; it issues no
invoice and holds no invoice line.

## Domain model

- **Time entry** (`time.entries`) — one person's hours on one day: `userId`,
  `projectId`, optional `billingLineId` and `taskId` with a `taskTitle` snapshot,
  `entryDate`, `hours`, optional `startTime`/`endTime`, a `note`, `billable`, the
  frozen `billRate`/`billCurrency` and `costRate`/`costCurrency` with the
  `rateSource` that produced them, a `status`, a `rejectionReason`, the
  `submittedAt`/`approvedAt`/`approvedBy`/`invoicedAt` stamps, and `revision` for
  concurrent edits. The id is a `bigint`.
- **Person rate card** (`time.person_rates`) — one row per person per `validFrom`
  date: `billRate`, `costRate` and the `currency` both are quoted in. Rates are
  effective-dated; the row with the latest `validFrom` on or before an entry's date
  is the one that applies. At most one row per person per date.
- **Week submission** (`time.week_submissions`) — one row per person per Monday,
  recording when they submitted that week.
- **Settings** (`time.settings`) — a key/value table holding one key today, the
  period lock date.

Every foreign identifier is opaque: users, projects, billing lines and tasks live in
other schemas and are read through contracts, never through SQL
(see [module boundaries](module-boundaries.md) rule 4).

### Hours, and the start/end rule

`hours` is greater than zero, at most 24, and has at most two decimals; the total for
one person on one day may not exceed 24 either. The day total is checked under a
PostgreSQL advisory lock keyed on `<userId>/<date>`, so two entries racing on the same
day cannot slip past 24 together.

`startTime` and `endTime` are both given or neither. They are clock times on the
entry's own day, `endTime` is strictly after `startTime` (an entry ending at midnight
ends at `23:59`; crossing midnight is refused), and **`hours` must equal the
difference to two decimals** — the request is refused on `hours` if it does not. Times
are optional because a day's work is often reconstructed, not clocked.

Hours are handled as integer hundredths end to end, so neither the cap nor the
start/end comparison depends on float rounding.

## The rate chain

Rates are resolved **at every save while the entry is `draft` or `rejected`**, and
**frozen from the moment it is submitted**. Changing a rate card never rewrites hours
that are already on their way to an invoice; re-editing a rejected entry re-resolves
them, because it is a draft again.

The bill rate, when the entry is billable:

1. **The billing line's rule.** `fixed` uses the line's amount. `list` and `discount`
   ask `contracts.ProductCatalog.ListPrice(variant, project currency, entry date)`
   for the variant the line is pinned to; `discount` then applies the line's
   percentage with **exact decimal arithmetic** — the price and the percentage are
   read as decimals, multiplied as rationals and rounded once, half away from zero,
   to cents. 101.10 less 15 % is 85.94, where float arithmetic gives 85.93.
2. **The project's `defaultBillRate`**, when the project has a currency to quote it
   in.
3. **The person's rate card** effective on the entry date — but **only when its
   currency is one the project bills in**: the project has no currency of its own, or
   it is the card's. A card quoted in SEK is no use to a project billed in NOK, and
   nothing here converts, so the chain ends at step 4 instead.
4. **None.** No rate is not an error; the entry is stored with `rateSource: "none"`
   and the hours simply have no amount.

Each step falls through when it has nothing to offer: a `list` line with products
disabled, or a variant with no price in the project's currency on that date, falls
through to the project default the same way a project without one falls through to
the person.

The cost rate is simpler: the person's rate card effective on the entry date, or none.
A non-billable entry gets no bill rate at all (`rateSource: "none"`), but still gets
its cost.

**Currency is never converted.** A bill rate from a line or the project carries the
**project's** currency; a bill rate from a rate card carries the **card's**, as does
every cost rate. Three consequences follow, and all three are the same rule:

- A project with **no currency** takes no line- or project-sourced bill rate at all.
- A rate card whose currency is **not the project's** gives no bill rate either — the
  chain ends at `none` rather than quoting an amount in a currency nobody bills in.
  (A project with no currency accepts the card's, because there is nothing to
  disagree with.) The **cost** rate is never held back this way: cost is the company's
  own number, in the card's currency, whatever the project bills in.
- Where two currencies would have to be added together — the project summary's billed
  amount — the hours are reported as *unpriced* instead of summed into a number in
  neither currency.

`rateSource` is one of `line`, `project`, `person` or `none`, and it is part of the
entry's response so the UI can say where an amount came from.

## The state machine

```text
        submit            approve           (invoicing)
draft ───────────► submitted ───────► approved ───────────► invoiced
  ▲                    │                  │
  │                    │ reject           │ unapprove
  │                    ▼                  │
  │                 rejected              │
  │       edit          │                 │
  └─────────────────────┴─────────────────┘
```

An edit takes a rejected entry back to `draft`; unapprove takes an approved one back
to `draft` too. `invoiced` has no way out.

- **draft** — the owner's to edit or delete. Content edits are owner-only.
- **submitted** — frozen rates, waiting for an approver. The owner can no longer edit
  it; a stale `PUT` gets 403 rather than a revision conflict.
- **approved** — an approver accepted it.
- **rejected** — an approver sent it back with a reason (required, at most 1000
  characters). Editing a rejected entry makes it a draft again and clears
  `submittedAt`, so it has to be submitted afresh.
- **invoiced** — terminal. Nothing moves an invoiced entry, unapprove included. This
  module never writes `status: "invoiced"` or `invoicedAt` itself — the column, the
  `WHERE status = 'approved'` guards and the "Entry N is invoiced" refusals all exist
  for the future Invoices module to use. Tests reach the state directly through the
  database.

**Unapprove** takes an approved entry back to a **fresh draft**: it clears
`submittedAt` as well as the approval, so the week reports unsubmitted changes and the
hours go round the loop again rather than silently re-entering the queue. It is for
approvers and for `time:manage`; the period lock holds it back for everyone but
`time:manage`, because an unapproved entry becomes a draft its owner could not edit.

Approve, reject and unapprove are **batch, all-or-nothing**: a request naming ten ids
either moves all ten or moves none and answers with a message per refused id. Every
batch's `ids` array is capped at 500 entries; a longer array is refused on `ids`
before any entry is read.

There is no owner-side "unsubmit". Once submitted, an entry is out of its owner's
hands until an approver acts on it: a mistaken submission is undone by an approver
rejecting it (which requires a reason), or by an approver or `time:manage` unapproving
it after it is approved. A person who submits by mistake has to ask for one of those,
which is a support burden worth knowing about.

## Weekly submission

A week starts on **Monday**, and `weekStart` must be one. `GET /time/weeks/{weekStart}`
returns the caller's own week as the grid renders it — one row per project, billing
line and task, seven day columns, per-day and week totals — plus `submittedAt` and
`hasUnsubmittedChanges` (the week was submitted and holds a draft again).

`POST /time/weeks/{weekStart}/submit` submits every draft in the caller's week and
records the submission. It leaves rejected entries alone: an edit turns them into
drafts first, which is the point of rejecting them. Submitting an empty week is
allowed, so "I worked nothing this week" is a thing a person can say.

## The period lock

`time:manage` sets one date, `lockedBefore`. Everybody can read it — the UI has to
grey out the days nobody can touch — and everybody except `time:manage` is held back
by it: no creating, editing, deleting, submitting, approving or unapproving an entry
dated before it. The lock date itself is open; only days strictly before it are
closed. It is how a period gets closed for invoicing without deleting anything.

The approval queue leaves out entries the lock would stop the caller from approving,
so the queue never offers an action that would be refused.

## Visibility and permissions

| Permission | Meaning |
| --- | --- |
| `time:access` | Use the Time app: log time on the projects you are a member or manager of, and see your own entries. Every operation requires it |
| `time:approve` | Approve and reject anyone's submitted entries, on every project |
| `time:view-all` | See everyone's entries and the people overview, and see cost rates (sensitive) |
| `time:manage` | Person rate cards, the period lock, unapprove, and editing past the lock (sensitive) |

Project managers approve their own projects' entries **through their role**, without
`time:approve` — `contracts.ProjectDirectory.Role` is what answers that, so nothing
has to be granted twice. Every entry carries a `capabilities` object (`canEdit`,
`canSubmit`, `canApprove`, `canUnapprove`) so a client never re-derives these rules.

What each caller sees:

- **The owner** sees their own entries, always, including their bill rate.
- **A project manager** sees the entries on the projects they manage.
- **`time:view-all`, `time:approve` and `time:manage`** see everyone's.
- **The bill rate** is visible to the owner and to whoever may see the project's
  financials — its manager, `projects:view-financials` on a project they can see, or
  `projects:manage-all`. **The cost rate** is for `time:view-all` and `time:manage`
  only.
- An entry on a project the caller may not see is a **bare 404**, byte-identical to
  the answer for an id that does not exist. "You may not see it" and "it is not
  there" must not be distinguishable.

The project hours summary is open to whoever may see the project and to the holders
of `time:view-all`, `time:approve` and `time:manage`, who see those hours entry by
entry anyway; everyone else gets the same bare 404. Its billing block has the one
answer every entry has: only whoever may see the project's financials gets it, so a
`time:view-all` holder who cannot see the project reads the hours and no money.

## Rate cards and the directory

`GET /time/rates` without a `userId`, and `GET /time/people`, are unpaginated: they
answer one row per person (every rate row, and every person with activity in up to 12
weeks) with no `page`/`pageSize`. Both are `time:manage`/`time:view-all` admin reads
sized by headcount rather than by activity history, which is fine for the small
installations Vantigo targets, but they are the only two lists in the module that do
not page.

`GET /time/rates/assignable-users?search=` is the **only** view of the user directory
the module has, and it is deliberately small: `time:manage` only, it matches display
names case-insensitively, and it answers **at most twenty** people. A rate card has to
be creatable for a new hire before their first hour, so the picker cannot be limited
to people who already have entries — but neither is it a user browser. A manager types
a name rather than scrolling a list.

## No directory call inside a locked transaction

Rule of the module, and it is enforced: **a transaction that holds a lock makes no
contract call.** Projects, users and product prices are resolved *before* the
transaction opens or *after* it commits, and role lookups are warmed into the caller's
cache first. A directory call is an in-process function today, but it is a seam that
may one day do I/O, and a slow one under a day lock or a `FOR UPDATE` would hold up
everybody else logging hours on the same day.

Structurally, every locked transaction goes through `withLockedTx`, which marks its
context; the test fakes record any call made with such a context, and the harness
fails any test in which one happened. The rule is the whole suite's, not one test's.

## API

Every operation is under `/api/v1/time`, authenticated with the shared identity
session cookie, and every one requires `time:access` on top of what the table says.

| Endpoint | Access |
| --- | --- |
| `GET /time/entries` (`userId`, `weekStart`, `projectId`, `status`, paging) | Own entries; a manager also sees their projects'; view-all/approve/manage see everyone's |
| `GET /time/entries/{id}` | The owner, the project's manager, or view-all/approve/manage; a bare 404 otherwise |
| `POST /time/entries`, `PUT /time/entries/{id}`, `DELETE /time/entries/{id}` | Create where you may log time; edit and delete your own, in `draft`/`rejected`, not past the lock |
| `POST /time/entries/submit` | Your own drafts |
| `POST /time/entries/approve`, `/reject`, `/unapprove` | Approver (project manager or `time:approve`); unapprove also `time:manage` |
| `GET /time/weeks/{weekStart}`, `POST /time/weeks/{weekStart}/submit` | Your own week |
| `GET /time/approvals` | Approver; 403 for a caller who approves nothing |
| `GET /time/people` | `time:view-all` |
| `GET/POST /time/rates`, `PUT/DELETE /time/rates/{id}`, `GET /time/rates/users/{userId}`, `GET /time/rates/assignable-users` | `time:manage` |
| `GET /time/settings` | Anyone in the app: it returns the lock date, which every page needs |
| `PUT /time/settings` | `time:manage` |
| `GET /time/stats/summary`, `/timeseries`, `/attention` | The caller's own figures, plus their approval queue's size |
| `GET /time/projects/{projectId}/summary` | Sees the project; amounts only for its financial viewers |

## The Time app

The UI is `@vantigo/time-ui` (`apps/time/frontend`), composed by the host SPA like
every other module package; module packages never import each other.

| Route | Page |
| --- | --- |
| `/time` | **My week**: the grid, a row per project ▸ line ▸ task, day columns, totals and "Submit week". `?week=YYYY-MM-DD` opens another week |
| `/time/day?date=YYYY-MM-DD` | **Day view**: one day's entries with times and notes; mobile-first |
| `/time/approvals` | The approval queue, by person and week, with batch approve and reject |
| `/time/people` | Everyone's hours week by week (`time:view-all`) |
| `/time/settings` | Rate cards and the period lock (`time:manage`) |
| `/projects/$projectId/time` | The **Time** tab on the project page: hours by status, line and person, and the billed amount for financial viewers |

The app is registered in `apps/host/frontend/src/apps.ts` and shows in the switcher
for anyone holding one of the four permissions, greying out with "Not enabled" when
the module is off. The sidebar offers **Approvals** to `time:approve`; a project
manager without it reaches the same queue from the dashboard's attention list or by
URL, so its *route guard* asks only for `time:access` (the nav item says so with
`guardPermissions`) and the backend refuses a caller who approves nothing.

Spotlight has a **Log time** quick action; the dashboard has a Time card (hours this
week, and how much waits for the caller's approval), the `hours` metric, and attention
items for an unsubmitted week and for a submission that has waited a week — those two
titles are written by the host, from the item's type and entity id, because the
server builds them from data and they would otherwise arrive in English.

## What invoicing will read

Time provides **no contract yet**; the module it is waiting for is invoicing, and the
seam is already the right shape for it:

- **Approved, billable entries with a bill rate** are the invoiceable set: `hours ×
  billRate` in `billCurrency`, grouped by project and by the **trackable code**
  `<project>-<line>` the billing line gives them.
- The rate is a **snapshot**, so an invoice built next month from last month's hours
  bills what was promised, not what the rate card says today.
- `unpricedHours` on the project summary is what an invoice cannot price: billable
  hours with no rate, or priced in a currency the project does not bill in.
- Setting `invoicedAt` makes an entry **terminal** — nothing edits, unapproves or
  re-approves it. That is what makes it safe for invoicing to own the stamp.
- The **period lock** is how a month gets closed before it is invoiced.

Until that module exists, no endpoint writes `invoicedAt`; the column and the status
are there so the state machine is complete rather than retrofitted.

## What Time reports to other modules

Time is the first module to *provide* a cross-module contract rather than only
consume one: it implements `contracts.ProjectActuals` (`internal/time/actuals.go`),
which is what has been logged against a project, for whoever compares it with what
was planned — Projects' economy view today, an invoice later. It performs no
authorization of its own: the caller has already decided who may see the project and
who may see amounts, and the answer hands back hours and money together for the
caller to shape.

- **The same three buckets everywhere.** `Approved` (approved and invoiced entries),
  `Submitted` and `Draft` (draft and rejected entries) — the split every surface in
  this module already shows.
- **`Total` is a fourth bucket, not a sum of the other three.** Each bucket's amount
  is rounded once, on its own; `Total`'s amount is the unrounded sum of everything
  in all three buckets, rounded once. Those two roundings can land a cent apart, so
  adding the three published bucket amounts is not reliably the same number as
  `Total`. `Total` is the figure a consumer should read whenever it wants "the
  project's amount" rather than one bucket's, and it should never be reached by
  adding the three. Its hours are simply the three buckets' hours, which do add up
  exactly either way.
- **The currency rule for bill and for cost, decided independently.** An entry's bill
  amount counts only when its `billCurrency` equals the currency the caller asked
  for; its cost amount counts only when its `costCurrency` does, on its own. A person
  carded in EUR working on a NOK project can have a row whose bill counts and whose
  cost does not, on the very same hours. With no currency asked for, no amount is
  reported at all.
- **Unpriced hours are billable hours without a usable bill rate** — no rate, or a
  rate in another currency — and nothing else: non-billable work is never unpriced,
  because it was never meant to carry a price. It is the same figure the project
  summary's `unpricedHours` reports (`GET /time/projects/{id}/summary`) **for the
  hours that summary covers**, and the two agree exactly when both of the following
  hold. The summary counts only `submitted`, `approved` and `invoiced` entries, so a
  billable draft or rejected entry with no rate is unpriced here and outside the
  summary entirely; and the summary *infers* a currency for a project that carries
  none, while the contract reports no amounts at all when asked without one and so
  calls every priced billable hour unpriced. A project with a currency and no
  unpriced draft is the region where the two are one number, and
  `TestActualsUnpricedHoursAgreeWithTheProjectSummary` pins both the agreement and
  the divergence.
- **Uncosted hours** are the same idea for cost, but across *every* bucket, billable
  or not: work nobody is billed for still costs the company, so a consumer showing a
  margin has to know how many of its hours it left out of that number.
- **No authorization, ever.** `Actuals`/`ActualsForProjects` answer whatever was
  logged; they never consult a role, a permission or the caller's identity, because
  the caller has already made that decision for its own surface.
- **The batch is capped at `contracts.MaxActualsRequests` (2 000)** projects per
  `ActualsForProjects` call, and naming one project twice in a batch is a hard error
  rather than a resolvable ambiguity — silently picking the first, the last, or
  merging the two would each be defensible, which is reason enough to refuse all of
  it instead.
- **It never calls back into `contracts.ProjectDirectory`.** A project's currency is
  a fact the module that owns projects has already decided, and asking for it while
  serving a request from that same module would be a cycle at request time — so the
  currency arrives *in* the request (`ActualsRequest.Currency`) instead of being
  looked up.

See [module boundaries](module-boundaries.md) for how this contract is resolved
without either module importing the other.

## Enabling and disabling

`MODULES` is a positive allowlist; unset enables every module the binary can mount.

```text
MODULES=customers,projects,time
```

Omitting `time` means the module contributes no route, no permission and no UI: its
paths answer the `/api` catch-all 404 and the switcher tile greys out. The `time`
schema is migrated regardless, so enabling it later needs no migration.

Two startup rules to know:

- `time` without `projects` fails configuration with **`time requires projects`** —
  hours hang off projects, which time reads through
  `contracts.ProjectDirectory`.
- `products` is optional. Without it, billing lines priced `list` or `discount` have
  no price to read and the rate chain falls through to the project default or the
  person's card.

Time contributes **no background worker** and **no rate-limited operation**.

## Development

The backend is `apps/server/internal/time`, with typed queries generated by sqlc and
the server interface generated by oapi-codegen from `openapi/time.yaml`. The UI lives
in `apps/time/frontend` (`@vantigo/time-ui`) and is composed by the host SPA in
`apps/host/frontend`.

The Go package's tests run every HTTP exchange through a contract-validating client
and gate on operation coverage: every operation in `openapi/time.yaml` must have been
exercised by at least one successful exchange, with no allow-list. Projects and
products are fakes in those tests — depguard forbids importing another module even in
tests — while the user directory is the real one, composed.

```bash
bun run --cwd apps/time/frontend test
cd apps/server && go test ./internal/time/
```
