# Projects Phase 4 — Delivery milestones, timeline and templates

Phase 4 of the Projects module. Builds on the phased plan in
`docs/superpowers/specs/2026-09-18-project-management-plan.md` (decisions D7, D8, D10)
and the projects module reference (`docs/src/content/docs/en/reference/projects.md`).

## 1. Purpose

Give small contractors and consultancies the tools to plan, track, and execute work:
- **Delivery milestones** (execution tracking): track major project deliverables with
  derived completion progress from linked tasks, distinct from commercial billing milestones.
- **Lightweight timeline** (visual schedule): view tasks and delivery milestones on a
  time axis with drag-to-reschedule, zoom presets (day/week/month), and finish-to-start
  dependencies.
- **Project and task templates** (kickoff blueprints): instantiate standard project
  structures (tasks, subtasks, delivery milestones, checklists) with relative date offsets
  from a chosen start date.

Three deliveries, three pull requests, one migration each:

- **Delivery A — Delivery Milestones**: the `projects.delivery_milestones` entity,
  task association (`delivery_milestone_id`), derived completion metrics, CRUD endpoints,
  and project task list/card integration.
- **Delivery B — Lightweight Timeline and Dependencies**: task dependencies
  (`projects.task_dependencies`), cycle validation, the timeline view on the project
  page, date adjustment, and dependency arrow visualization.
- **Delivery C — Project and Task Templates**: template catalog and instantiation
  engine with relative date offsets ($T+0$, $T+14$ days) for project kickoff.

## 2. Decisions

### Delivery Milestones

**M1 — Delivery milestones are execution markers, not billing milestones (D7).**
A billing milestone is commercial: it defines an amount or a percentage of the fixed price,
moves through `planned → ready → invoiced`, and produces an invoice line. A **delivery
milestone** is execution-focused: a named marker with an optional target date, linked
tasks, and a progress percentage derived from whether those tasks are done. The two
entities serve different purposes and stay on separate tables (`projects.billing_milestones`
vs `projects.delivery_milestones`).

**M2 — Authorization follows work rights (`canContribute`), not financial rights.**
Because delivery milestones represent work execution rather than money, they do not
require `projects:view-financials` or manager financial control:
- Anyone who can see the project (`projects:access` + member/viewer/view-all) can read
  delivery milestones.
- Anyone who can contribute work (`manager` or `member`, or `projects:manage-all`)
  can create, edit, reorder, and delete delivery milestones, and link tasks to them.

**M3 — Tasks link to at most one delivery milestone.**
`projects.tasks` gains a nullable `delivery_milestone_id`. A task belongs to at most one
delivery milestone. When a milestone is deleted, linked tasks have their
`delivery_milestone_id` set to `NULL` (`ON DELETE SET NULL`), never deleted. Top-level
tasks and subtasks can belong to the milestone; subtasks inherit their parent's milestone
by default if not explicitly set.

**M4 — Derived progress metrics.**
Each delivery milestone reports:
- `totalTasks`: count of tasks linked to this milestone.
- `completedTasks`: count of linked tasks in `done` status.
- `progressPercent`: $\frac{\text{completedTasks}}{\text{totalTasks}} \times 100$ rounded
  to nearest integer (0 when `totalTasks = 0`).
- `estimatedHoursTotal` and `estimatedHoursCompleted`: aggregate of `estimate_hours` for
  linked tasks when estimates are present.
- `status`: `completed` when all linked tasks are `done` (and `totalTasks > 0`), otherwise
  `open`. An explicit completion flag allows marking a milestone completed even if it has
  no tasks.

**M5 — Position ordering within the project.**
Delivery milestones carry a 1-based `position` renumbered $1..n$ in one transaction
upon creation, deletion, or reordering (`PUT .../position`), following the same
pattern established by tasks and billing milestones.

### Timeline and Dependencies (Delivery B)

**M6 — Finish-to-Start (FS) dependencies only (D8).**
Predecessor must finish before successor starts (`task_a.due_date <= task_b.start_date`).
No complex lag/lead offsets or start-to-start / finish-to-finish variants in-house.

**M7 — Cycle-free DAG validation.**
Adding a dependency enforces a Directed Acyclic Graph (DAG) with depth limit and
immediate cycle rejection (400 `cyclic_dependency`).

**M8 — Opt-in downstream reschedule.**
Moving a predecessor task does not silently shift downstream tasks without confirmation.
The UI highlights dependency conflicts, and an opt-in toggle ("Shift dependent tasks")
cascades the date delta to direct and indirect successors.

### Templates (Delivery C)

**M9 — Relative day offsets from kickoff.**
Tasks, subtasks, and delivery milestones inside a template define `startDayOffset` and
`dueDayOffset` relative to project day 0 ($T+0$).

---

## 3. Delivery A — Delivery Milestones Specification

### 3.1 Database Schema (Migration `00096_projects_delivery_milestones.sql`)

```sql
CREATE TABLE projects.delivery_milestones (
    id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id          integer      NOT NULL REFERENCES projects.projects (id),
    name                varchar(200) NOT NULL,
    description         varchar(2000),
    target_date         date,
    status              varchar(20)  NOT NULL DEFAULT 'open',
    position            integer      NOT NULL,
    completed_at        timestamptz,
    completed_by_user_id uuid,
    revision            integer      NOT NULL DEFAULT 1,
    created_by_user_id  uuid         NOT NULL,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL
);
CREATE INDEX ix_delivery_milestones_project_id_position ON projects.delivery_milestones (project_id, position);

ALTER TABLE projects.tasks
    ADD COLUMN delivery_milestone_id integer REFERENCES projects.delivery_milestones (id) ON DELETE SET NULL;
CREATE INDEX ix_tasks_delivery_milestone_id ON projects.tasks (delivery_milestone_id);
```

### 3.2 Endpoints in `openapi/projects.yaml`

| Method | Path | Summary | Permission |
| --- | --- | --- | --- |
| `GET` | `/api/v1/projects/{projectId}/delivery-milestones` | List project delivery milestones with progress | `projects:access` + read |
| `POST` | `/api/v1/projects/{projectId}/delivery-milestones` | Create delivery milestone | Member or Manager |
| `GET` | `/api/v1/projects/delivery-milestones/{milestoneId}` | Get single delivery milestone | `projects:access` + read |
| `PUT` | `/api/v1/projects/delivery-milestones/{milestoneId}` | Replace delivery milestone (name, description, targetDate, revision) | Member or Manager |
| `DELETE` | `/api/v1/projects/delivery-milestones/{milestoneId}` | Delete milestone (unlinks tasks, renumbers remaining) | Member or Manager |
| `PUT` | `/api/v1/projects/delivery-milestones/{milestoneId}/position` | Reorder delivery milestone to 1..n | Member or Manager |
| `POST` | `/api/v1/projects/delivery-milestones/{milestoneId}/status` | Update milestone status (`open`, `completed`) | Member or Manager |

### 3.3 Task Updates

`CreateTaskRequest`, `UpdateTaskRequest`, `TaskResponse`, and `TaskTreeResponse` gain:
- `deliveryMilestoneId` (nullable integer).
- `TaskResponse` embeds `deliveryMilestone`: `{ id, name }` (optional) for efficient display on cards without secondary lookups.

### 3.4 Concurrency & Validation

- `name`: 1..200 characters, trimmed, required.
- `description`: max 2000 characters, optional.
- `target_date`: optional valid ISO date.
- `revision`: integer token checked on every update (`PUT`), answering 409 Conflict if mismatched.
- Changing `deliveryMilestoneId` on a task verifies that the milestone belongs to the **same project** (answering 400 `milestone_project_mismatch` if from another project).

### 3.5 Timeline Entries

Generated timeline entries on `projects.timeline_entries`:
- `delivery-milestone-added`
- `delivery-milestone-changed`
- `delivery-milestone-removed`
- `delivery-milestone-completed` / `delivery-milestone-reopened` (a status change; nothing
  when the status did not move)

`delivery-milestone-changed` carries the `fields` the edit moved and is not recorded when
none did. Applying a template records `delivery-milestone-added` for each milestone it
creates.

## 4. As delivered, and what is left for later

The three deliveries landed as one pull request (#202) rather than three. Where it
departs from the decisions above:

- **M4:** a milestone's `status` is set by hand (`POST .../status`); it is not derived from
  its tasks. The progress figures are derived; the status is the team's call.
- **M3:** a new subtask that names no delivery milestone takes its parent's. Later edits
  never re-inherit.
- **M7:** cycles are refused under the project's task-order lock. Template dependencies are
  checked for cycles and duplicates when the template is saved. *Follow-up (delivered):* the
  longest chain of linked tasks through a new link is capped at 100 links, decided under the
  same lock (400 `Dependency chain too long` on `successorTaskId`), and templates are held to
  the same cap.
- **M8 / Delivery B:** #202 shipped the timeline read-only; dates moved through the task form,
  and the downstream shift was the explicit **Reschedule downstream** action in the task
  drawer, bounded to ±3650 days and leaving tasks without dates alone. A follow-up delivered
  drag-to-reschedule and the Day / Week / Month zoom presets: dragging a bar's body moves both
  dates, an edge moves one (the due date never before the start), Alt+Left/Right moves a
  focused bar a day, and the drop saves through the ordinary task update. A move that changed
  the due date of a task with successors asks *Shift N dependent tasks …?* and calls the same
  reschedule-downstream endpoint only on **Shift dependent tasks** — M8's opt-in, as a prompt
  after the move rather than a toggle before it. The drawer's action stays.
- **Delivery C:** templates carry milestones, tasks, subtasks and dependencies. Templates are
  edited on their own screen (Projects → **Project templates**); writing one needs
  `projects:create`.

Follow-up, delivered after #202:

- **Checklists in templates.** Each template task and subtask carries an optional `checklist`
  (item texts in order, each 1–500 characters, at most 100 per task); apply creates them unticked
  in its transaction. The blueprint is JSON, so no migration.
- **Save a project as a template** (`POST /api/v1/projects/{id}/save-as-template`, needs
  `projects:create` and sight of the project). It snapshots milestones, top-level tasks with
  subtasks and checklists, milestone links and the dependencies between top-level tasks
  (dependencies touching a subtask are skipped), anchoring offsets on the project's start date
  (earlier dates clamp to 0) or else the earliest copied date. It records nothing on the timeline
  and refuses, with the template's field errors, a plan that breaks a template rule.
- The template revision conflict names the template; the catalog page lost its read-only branch,
  which the route's projects:create guard made unreachable.

Nothing from the original later-list is left; drag and zoom, checklists in templates, saving
a project as a template and the dependency chain cap are all delivered.
