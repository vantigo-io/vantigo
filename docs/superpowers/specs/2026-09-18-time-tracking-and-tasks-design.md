# Time tracking and tasks — design

Phase 2 of `docs/superpowers/specs/2026-09-18-project-management-plan.md`.
Builds on the projects module (`docs/projects.md`,
`docs/superpowers/specs/2026-09-17-projects-design.md`).

## 1. Scope

Two deliveries, in order, each its own PR:

- **A — Tasks in Projects.** Tasks with one level of subtasks, checklist items
  and comments inside the `projects` module; a project default bill rate; the
  `contracts.ProjectDirectory` methods Time needs; a Tasks tab and board on the
  project page; "My tasks" across projects.
- **B — The `time` module.** Time entries with snapshotted rates, person rate
  cards, weekly submission, per-entry approval, a period lock; the Time app in
  the switcher (my week, day view, approvals, people, settings); a Time tab on
  the project page; a dashboard card.

Target users: small contractors and consultancies.

**Not in scope:** invoicing (Invoices multiplies hours by snapshotted rates
later, through a `TimeLedger` contract that does not exist yet); budgets vs
actual and billing milestones (phase 3); delivery milestones, timeline,
templates (phase 4); allocations (phase 5); customisable status labels;
multiple assignees; task attachments (waits for the documents decision);
overtime multipliers (phase 3 billing-line rules); pay/payroll export.

## 2. Decisions

**D1 — Duration is the truth; start and end times are optional.** An entry
always has `hours` (two decimals, > 0, ≤ 24). `startTime`/`endTime` are
optional; when both are given they must be on the same day, end after start,
and `hours` is derived from them (the client sends the derived value; the
server recomputes and rejects a mismatch). Breaks are not modelled.

**D2 — Submission is per week, approval per entry.** The owner submits a
week (all their draft entries in it) or a single entry. An approver approves
or rejects entries individually or in batch. Approvers are project managers
for entries on their projects, and anyone with `time:approve` for any entry.

**D3 — Rates resolve through an explicit chain and are snapshotted.** At every
save while an entry is `draft` or `rejected`: bill rate (only when billable) =
billing-line rule → project default bill rate → person bill rate effective on
the entry date → none; cost rate = person cost rate effective on the date →
none. Both are frozen from `submitted` on. `rateSource` records the winning
step (`line`, `project`, `person`, `none`). Rates are stored in the project's
currency for bill and the person card's currency for cost; no conversion.

**D4 — Time reads projects only through contracts.** `time` requires
`projects` at startup; it consumes `contracts.ProjectDirectory`,
`contracts.UserDirectory` and, optionally, `contracts.ProductCatalog` (for
`list`/`discount` line prices — absent means "no rate"). Time provides no
contract yet.

**D5 — Tasks live inside Projects and are deletable.** A task is a unit of
work on one project; a mis-created task is common, so tasks can be deleted.
A time entry that references a task stores a `taskTitle` snapshot so history
stays readable after deletion; the directory answers `(nil, nil)` for a
deleted task and the timesheet shows the snapshot.

**D6 — Single assignee, fixed status category, one level of subtasks.**
Status is `todo | in-progress | done` (customisable labels later, on top of
this category). A subtask's parent must be a top-level task. Checklist items
are text + done, not assignable and not reported. Comments are a task-scoped
table, not project timeline entries; tasks write no timeline events.

**D7 — Who may do what.** Anyone who sees a project sees its tasks and its
own entries' hours; members and managers create and edit tasks; viewers read.
A person logs time only on projects where they are member or manager and the
project is `active`. Only an entry's owner edits its content, and only in
`draft`/`rejected`.

**D8 — Financial shaping, extended.** Bill rate on an entry is visible to the
entry's owner and to callers who may see the project's financials (project
managers, `projects:view-financials`, `projects:manage-all`). Cost rate is
visible only to `time:manage` and `time:view-all`. Person rate cards are
`time:manage` only. The project's `defaultBillRate` sits inside the project's
`financials` object.

**D9 — Period lock.** One "locked before" date (`time.settings`). Entries
dated before it cannot be created, edited, submitted, approved or rejected by
anyone but `time:manage`, and never once `invoiced`.

**D10 — Delete is for drafts.** An entry can be deleted only by its owner and
only in `draft` or `rejected`. Everything else is a state transition.

## 3. Data model

### 3.1 Projects (delivery A)

```
projects.tasks(
  id               integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
  project_id       integer NOT NULL REFERENCES projects.projects(id),
  parent_task_id   integer REFERENCES projects.tasks(id) ON DELETE CASCADE,  -- one level: parent's parent must be NULL (Go-enforced)
  title            varchar(200) NOT NULL,
  description      varchar(4000),
  status           varchar(20) NOT NULL DEFAULT 'todo',    -- todo | in-progress | done
  assignee_user_id uuid,
  start_date       date,
  due_date         date,                                    -- >= start_date when both set
  estimate_hours   numeric(8,2),                            -- > 0 when set
  position         integer NOT NULL,                        -- ordering among siblings
  completed_at     timestamptz,                             -- set when status becomes done, cleared otherwise
  created_by_user_id uuid NOT NULL,
  revision         integer NOT NULL DEFAULT 1,
  created_at, updated_at timestamptz NOT NULL)
  -- indexes: (project_id, parent_task_id, position), (assignee_user_id) WHERE status <> 'done'

projects.task_checklist_items(
  id bigint identity PK, task_id integer NOT NULL REFERENCES projects.tasks(id) ON DELETE CASCADE,
  text varchar(500) NOT NULL, done boolean NOT NULL DEFAULT false, position integer NOT NULL,
  created_at, updated_at)

projects.task_comments(
  id bigint identity PK, task_id integer NOT NULL REFERENCES projects.tasks(id) ON DELETE CASCADE,
  author_user_id uuid NOT NULL, body varchar(4000) NOT NULL,
  created_at timestamptz NOT NULL, edited_at timestamptz)

ALTER TABLE projects.projects ADD COLUMN default_bill_rate numeric(12,2);  -- in the project's currency; requires currency
```

Migration `00009_projects_tasks.sql` (the 00008 baseline is merged and must not
change).

### 3.2 Time (delivery B), schema `time`

```
time.entries(
  id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id            uuid NOT NULL,
  project_id         integer NOT NULL,          -- opaque
  billing_line_id    integer,                   -- opaque, must belong to project_id and be active at save
  task_id            integer,                   -- opaque
  task_title         varchar(200),              -- snapshot at save
  entry_date         date NOT NULL,
  hours              numeric(5,2) NOT NULL,     -- > 0, <= 24
  start_time         time,
  end_time           time,                      -- both or neither; end > start; hours = end - start
  note               varchar(2000),
  billable           boolean NOT NULL,
  bill_rate          numeric(12,2),
  bill_currency      char(3),
  cost_rate          numeric(12,2),
  cost_currency      char(3),
  rate_source        varchar(20) NOT NULL,      -- line | project | person | none
  status             varchar(20) NOT NULL DEFAULT 'draft',  -- draft | submitted | approved | rejected | invoiced
  rejection_reason   varchar(1000),
  submitted_at       timestamptz,
  approved_by_user_id uuid,
  approved_at        timestamptz,
  invoiced_at        timestamptz,
  revision           integer NOT NULL DEFAULT 1,
  created_at, updated_at timestamptz NOT NULL)
  -- indexes: (user_id, entry_date), (project_id, entry_date), (status, entry_date)

time.person_rates(
  id integer identity PK, user_id uuid NOT NULL, valid_from date NOT NULL,
  bill_rate numeric(12,2), cost_rate numeric(12,2), currency char(3) NOT NULL,
  created_at, updated_at; UNIQUE (user_id, valid_from))

time.week_submissions(
  user_id uuid NOT NULL, week_start date NOT NULL,   -- Monday
  submitted_at timestamptz NOT NULL, PRIMARY KEY (user_id, week_start))

time.settings(key text PRIMARY KEY, value text NOT NULL)   -- 'locked_before' = YYYY-MM-DD
```

Migration `00010_time_baseline.sql`. Per-person-per-day total ≤ 24 h is
enforced in the create/update transaction (sum of the day's other entries).

## 4. Behaviour

### 4.1 Tasks (A)

- Create/update validate: title required ≤200; description ≤4000; status enum;
  `dueDate ≥ startDate`; `estimateHours > 0`; `assigneeUserId` must resolve via
  `UserDirectory` and be active when set; `parentTaskId` must be a top-level
  task of the same project.
- `status → done` sets `completed_at`; leaving `done` clears it. A parent's
  status is independent of its subtasks (no auto-rollup in this phase).
- Ordering: `PUT /tasks/{id}/position` with `{ parentTaskId?, position }`
  renumbers siblings in one transaction. New tasks append.
- Deleting a task cascades to its subtasks, checklist and comments. Deleting a
  project is still impossible (phase 1).
- Comments: author edits/deletes own; managers delete any.
- Authorization: see the project ⇒ read tasks; member/manager ⇒ write;
  outsiders 404 (as projects).
- `GET /projects/my-tasks`: the caller's open tasks across visible projects,
  with project code and name, ordered by due date then project code.
- `defaultBillRate` on the project: optional, > 0, requires `currency`; part
  of `financials`; editable by managers.

### 4.2 Time entries (B)

- Create/update (owner only, entry `draft`/`rejected`, date not before the
  lock): project must be `active` and the caller member/manager
  (`CanLogTime`); line must belong to the project and be active; task must
  belong to the project (title snapshotted); hours rule per D1; day total
  ≤ 24 h; billable defaults from the project's billing type when omitted and
  is forced false on `non-billable` projects; rates re-resolved per D3.
- Delete: owner, `draft`/`rejected` only.
- Submit week (`{ weekStart }`): all the owner's `draft` entries in that
  week → `submitted`, records `week_submissions`. Submit one entry likewise.
  A week with no draft entries submits as empty (allowed: "nothing to report").
- Approve/reject (batch of IDs): each entry must be `submitted`; the caller
  must be an approver for its project; reject requires a reason. Approve
  records approver and time.
- Unapprove (`time:manage` or the approver): `approved → draft`, not
  `invoiced`.
- Editing a `rejected` entry returns it to `draft` and clears the reason.
- List filters: `userId` (self unless `view-all`/approver of the project),
  `weekStart`, `projectId`, `status`; the week endpoint returns entries grouped
  by row (project, line, task) with the week's submission state.
- Approval queue: entries `submitted` on projects the caller may approve,
  grouped by person and week.
- People overview (`view-all`): per user, last four weeks: hours and
  submission/approval state.
- Stats (dashboard shapes as customers): `hoursThisWeek` for the caller,
  `awaitingMyApproval`; attention = weeks with unsubmitted entries older than
  a week (for the caller), and submitted entries waiting > 7 days (approvers).

### 4.3 Rate cards (B)

- `time.person_rates` CRUD, `time:manage` only; one row per (user,
  valid_from); the effective rate on a date is the latest `valid_from ≤ date`.
- Currency per row; the entry stores the rate and currency it resolved.

## 5. Contracts

`ProjectDirectory` gains (delivery A):

```go
Projects(ctx, ids []int32) ([]ProjectEntry, error)
ProjectByCode(ctx, code string) (*ProjectEntry, error)
BillingLines(ctx, projectID int32) ([]BillingLineEntry, error)            // active and inactive; consumer filters
Task(ctx, id int32) (*TaskEntry, error)                                   // nil for deleted
OpenTasksForUser(ctx, userID uuid.UUID) ([]TaskEntry, error)              // status != done, ordered by due date
CanLogTime(ctx, projectID int32, userID uuid.UUID) (bool, error)          // active && role in {member, manager}

type TaskEntry struct { ID, ProjectID int32; Title, Status string; AssigneeUserID *uuid.UUID; DueDate *time.Time }
// ProjectEntry gains Currency *string and DefaultBillRate *float64 (financial; only servers read it).
```

`time` consumes `ProjectDirectory` (required), `UserDirectory`, `ProductCatalog`
(optional). Config: `time requires projects`.

## 6. Permissions

Projects (A) adds none: tasks follow the project's roles.

Time (B):

| Key | Meaning |
|---|---|
| `time:access` | Use the Time app; log time on projects you're member/manager of; see your own entries |
| `time:approve` | Approve/reject anyone's submitted entries; see the approval queue for all projects |
| `time:view-all` | See everyone's entries and the people overview; cost rates |
| `time:manage` | Person rates, the period lock, unapprove, edit past the lock |

Project managers approve their own projects' entries through their role.

## 7. API

Projects (A), `/api/v1/projects`:

| Operation | Access |
|---|---|
| `GET /projects/{id}/tasks` (tree with checklists, comment counts) | sees project |
| `POST /projects/{id}/tasks`, `PUT /tasks/{taskId}`, `DELETE`, `PUT …/position` | member/manager |
| `GET/POST /tasks/{taskId}/checklist`, `PUT/DELETE …/{itemId}` | member/manager (GET: sees) |
| `GET/POST /tasks/{taskId}/comments`, `PUT/DELETE …/{commentId}` | sees / author, manager |
| `GET /projects/my-tasks` | `projects:access` |
| `PUT /projects/{id}` gains `defaultBillRate` | manager |

Time (B), `/api/v1/time`:

| Operation | Access |
|---|---|
| `GET /time/entries` (filters), `POST`, `PUT /{id}`, `DELETE /{id}` | access; owner rules |
| `GET /time/weeks/{weekStart}` (my rows), `POST /time/weeks/{weekStart}/submit` | access |
| `POST /time/entries/submit`, `/approve`, `/reject`, `/unapprove` (batch) | per §4.2 |
| `GET /time/approvals` | approve or manager |
| `GET /time/people` | view-all |
| `GET/POST /time/rates`, `PUT/DELETE /time/rates/{id}`, `GET /time/rates/users/{userId}` | manage |
| `GET/PUT /time/settings` | manage (GET: access, returns the lock date) |
| `GET /time/stats/summary`, `/timeseries`, `/attention` | access (dashboard shapes) |
| `GET /time/projects/{projectId}/summary` (hours per line/person/status) | sees project; amounts shaped per D8 |

Every entry response carries `capabilities` (`canEdit`, `canSubmit`,
`canApprove`) so the UI never re-derives rules.

## 8. Frontend

**Projects app (A):** Tasks tab on the project page (list grouped by status
with subtasks and checklist progress; board by status with drag between
columns; a task drawer with description, assignee, dates, estimate, checklist,
comments); "My tasks" sidebar entry at `/projects/my-tasks`. Task create from
the tab and from spotlight ("Create task" opens the project picker).

**Time app (B)** `@vantigo/time-ui`, switcher tile on `time:access`:

- `/time` **My week**: week navigator; grid rows "code › line › task", day
  columns, hours cells; add row from a picker (project → active line → my open
  task) or from my assigned tasks; per-cell status colour; day totals; "Submit
  week"; unsubmitted-changes banner.
- `/time/day/$date` **Day view**: list of entries with optional start/end,
  note; add/edit; mobile-first.
- `/time/approvals`: queue by person and week; approve/reject with reason,
  batch; hidden unless approver.
- `/time/people` (`view-all`); `/time/settings` (`manage`): rates table with
  effective dates, lock date.
- Host: project page **Time** tab (module on + `time:access` + sees project):
  hours per line and per person, status breakdown, amounts for financial
  viewers; dashboard card + metric; spotlight "Log time" (opens `/time`);
  permission labels; en + nb.

## 9. Testing

As for projects: contract-coverage gate per module against real Postgres;
authorization matrices (owner/member/viewer/manager/approver/outsider);
state-machine transitions incl. invalid ones; rate resolution table (line
fixed, list via fake catalog, discount, project default, person, none;
products off); 24 h cap and midnight rules; lock; concurrency (double submit,
double approve, racing day-cap inserts); directory methods; frontend tests for
the grid (add row, edit cell, submit, banner), approvals, task board drag and
drawer. Gates before each PR as before.

## 10. Delivery

- **PR A** `feat/project-tasks`: migration 00009, tasks/checklist/comments,
  default bill rate, directory methods, `projects.yaml`, projects-ui Tasks
  tab/board/drawer/my-tasks, host routes, docs and roadmap.
- **PR B** `feat/time-module` (based on A): migration 00010, `time` module,
  `openapi/time.yaml`, `@vantigo/time-ui`, host integration, docs.
