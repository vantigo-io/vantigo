# Project Tasks Implementation Plan (PR A of Time tracking and tasks)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add tasks (with subtasks, checklists, comments), a project default bill rate and the `ProjectDirectory` methods Time needs to the projects module, with a Tasks tab, board and "My tasks" in the frontend.

**Architecture:** Everything lands inside the existing `projects` module (Go package `internal/projects`, schema `projects`, contract `openapi/projects.yaml`, package `@vantigo/projects-ui`) following the patterns that module already established (authorize → 404/403 → validate → transaction; financial shaping; contract-coverage gate). One new migration `00009_projects_tasks.sql`. No new module, no new permissions.

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-09-18-time-tracking-and-tasks-design.md` (delivery A: §2 D5–D7, §3.1, §4.1, §5, §7 Projects, §8 Projects app). The projects module's own guide is `docs/projects.md`.

## Global Constraints

- **Pattern files:** backend `apps/server/internal/projects/{people.go,lines.go,lines_validation.go,authorize.go,responses.go,values.go,errors.go,directory.go,harness_test.go,people_test.go,lines_test.go}` — the handler shape is: load project → `authorize()` → `!CanSee` → 404 (bare, identical to unknown id) → `!CanManage`-style check → validate → `db.WithTx`. Frontend `apps/projects/frontend/src/pages/{project-people.tsx,project-billing.tsx,-billing-line-form-modal.tsx}` and host `apps/host/frontend/src/routes/projects/*`.
- **Boundaries:** `internal/projects/**` imports no other module (tests included; fakes against `internal/contracts`); no SQL crosses schemas (`assignee_user_id`, `author_user_id` are opaque). Module frontend packages import neither each other nor the host.
- **Toolchain:** `mise exec --` for go/sqlc/golangci-lint/bun; `golangci-lint run` from `apps/server` only; `bun run --cwd <dir> test`; capture exit codes before pipes. Go tests need `TEST_DATABASE_URL` if the default port is busy (see the ledger's environment note). After `openapi/projects.yaml` changes: `cd apps/server && mise exec -- go generate ./...` and `mise exec -- bun run gen:client`; commit results. After host route changes: `mise exec -- bun run --cwd apps/host/frontend build` and commit `routeTree.gen.ts`. After exported Go signature changes: `mise exec -- go vet ./...`.
- **Contract coverage gate:** every new operation in `projects.yaml` must be exercised by a passing test (`main_test.go`'s `RequireCoverage`).
- **Exact values:** task status `todo | in-progress | done`; title ≤ 200, description ≤ 4000, checklist text ≤ 500, comment body ≤ 4000; `estimateHours > 0` when set; `dueDate >= startDate`; one level of subtasks (a parent must itself be top-level); `defaultBillRate > 0` when set and requires the project's `currency`; `defaultBillRate` lives inside `financials`.
- **Authorization:** see project ⇒ read tasks/checklists/comments; member or manager ⇒ create/edit/delete tasks and checklist items, add comments; a comment is edited/deleted by its author or a manager; outsiders 404 indistinguishable from unknown ids. No new permission keys.
- **Money/IDs:** float64/JSON numbers; task and comment IDs `int32`/`int64` per the schema; user IDs uuid.
- **i18n:** every user-facing string in `en` and `nb`; `translations:check` passes.
- **Commits:** Conventional Commits (`feat(projects): …`, `feat(projects-ui): …`, `feat(frontend): …`) each ending with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; never `--no-verify`.
- **Branch:** `feat/project-tasks` (based on `docs/project-management-plan`); never commit to `main`.

## File Structure

```
apps/server/internal/db/migrations/00009_projects_tasks.sql       tasks, checklist, comments, default_bill_rate
apps/server/internal/projects/
  tasks.go              task CRUD + position + my-tasks handlers
  tasks_validation.go   task/checklist/comment rules
  checklist.go          checklist item handlers
  comments.go           comment handlers
  responses.go          + taskResponse / taskTreeResponse / checklist & comment responses
  values.go             + task status enum, defaultBillRate rule
  directory.go          + Projects, ProjectByCode, BillingLines, Task, OpenTasksForUser, CanLogTime
  queries/tasks.sql, checklist.sql, comments.sql, directory.sql (+)
  sqlc.yaml             + 00009 migration in schema list
  *_test.go             tasks_test.go, checklist_test.go, comments_test.go, tasks_concurrency_test.go, directory_test.go (+)
apps/server/internal/contracts/projects.go                          + TaskEntry, new methods, ProjectEntry.Currency/DefaultBillRate
openapi/projects.yaml                                               + task/checklist/comment/my-tasks operations, defaultBillRate
apps/projects/frontend/src/
  api/tasks.ts (+test)     queryOptions + mutations for tasks/checklist/comments/my-tasks
  lib/tasks.ts             status → label key + colour; ordering helpers
  pages/project-tasks.tsx  Tasks tab (list + board toggle)
  pages/-task-drawer.tsx   task detail drawer (fields, checklist, comments)
  pages/-task-form-modal.tsx  create/quick-edit
  pages/my-tasks.tsx       cross-project list
  components/task-status-badge.tsx
  i18n.ts (+), index.ts (+), package.json exports (+)
apps/host/frontend/src/
  routes/projects/$projectId.tasks.tsx, routes/projects/my-tasks.tsx, -project-detail-layout.tsx (+ tab), apps.ts (+ sidebar entry), app-spotlight.tsx (+ Create task), catalogs/* (+ labels)
docs/projects.md (+ tasks section), ROADMAP.md (phase 2A note)
```

---

### Task 1: Migration, contract, directory methods

**Files:**
- Create: `apps/server/internal/db/migrations/00009_projects_tasks.sql`, `apps/server/internal/projects/queries/tasks.sql` (directory-related queries may live in `queries/directory.sql`)
- Modify: `apps/server/internal/contracts/projects.go`, `apps/server/internal/projects/directory.go`, `apps/server/internal/projects/sqlc.yaml`, `apps/server/internal/projects/values.go` (status enum), `apps/server/internal/projects/directory_test.go`
- Test: `directory_test.go`, `apps/server/internal/db` migration tests (existing up/down tests must pass)

**Interfaces — Produces:**

```go
// contracts/projects.go
type TaskEntry struct {
	ID, ProjectID  int32
	Title, Status  string
	AssigneeUserID *uuid.UUID
	DueDate        *time.Time
}
// ProjectEntry gains:
Currency        *string
DefaultBillRate *float64
// ProjectDirectory gains:
Projects(ctx context.Context, ids []int32) ([]ProjectEntry, error)             // unknown ids omitted
ProjectByCode(ctx context.Context, code string) (*ProjectEntry, error)         // case-insensitive (upper-cased input); (nil,nil) when missing
BillingLines(ctx context.Context, projectID int32) ([]BillingLineEntry, error) // active and inactive, ordered by code
Task(ctx context.Context, id int32) (*TaskEntry, error)                        // (nil,nil) when missing/deleted
OpenTasksForUser(ctx context.Context, userID uuid.UUID) ([]TaskEntry, error)   // status != done, ordered by due date NULLS LAST, project id, position
CanLogTime(ctx context.Context, projectID int32, userID uuid.UUID) (bool, error) // project active && role in {member, manager}
```

Migration (complete):

```sql
-- +goose Up
CREATE TABLE projects.tasks (
    id                 integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id         integer      NOT NULL REFERENCES projects.projects (id),
    parent_task_id     integer      REFERENCES projects.tasks (id) ON DELETE CASCADE,
    title              varchar(200) NOT NULL,
    description        varchar(4000),
    status             varchar(20)  NOT NULL DEFAULT 'todo',
    assignee_user_id   uuid,
    start_date         date,
    due_date           date,
    estimate_hours     numeric(8,2),
    position           integer      NOT NULL,
    completed_at       timestamptz,
    created_by_user_id uuid         NOT NULL,
    revision           integer      NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    updated_at         timestamptz  NOT NULL
);
CREATE INDEX ix_tasks_project_id_parent_task_id_position ON projects.tasks (project_id, parent_task_id, position);
CREATE INDEX ix_tasks_assignee_user_id_open ON projects.tasks (assignee_user_id) WHERE status <> 'done';

CREATE TABLE projects.task_checklist_items (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id    integer      NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    text       varchar(500) NOT NULL,
    done       boolean      NOT NULL DEFAULT false,
    position   integer      NOT NULL,
    created_at timestamptz  NOT NULL,
    updated_at timestamptz  NOT NULL
);
CREATE INDEX ix_task_checklist_items_task_id_position ON projects.task_checklist_items (task_id, position);

CREATE TABLE projects.task_comments (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id        integer       NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    author_user_id uuid          NOT NULL,
    body           varchar(4000) NOT NULL,
    created_at     timestamptz   NOT NULL,
    edited_at      timestamptz
);
CREATE INDEX ix_task_comments_task_id_created_at ON projects.task_comments (task_id, created_at, id);

-- The default bill rate is in the project's currency and is a financial field
-- (spec D8); Time reads it through the directory, never the schema.
ALTER TABLE projects.projects ADD COLUMN default_bill_rate numeric(12,2);

-- +goose Down
ALTER TABLE projects.projects DROP COLUMN default_bill_rate;
DROP TABLE projects.task_comments;
DROP TABLE projects.task_checklist_items;
DROP TABLE projects.tasks;
```

(Check `00007_customers_type.sql` for the exact goose header style of a non-baseline migration; the schema test's ownership check keys on the `projects` segment of the file name.)

- [ ] **Step 1: Failing tests** in `directory_test.go` (rows inserted with `h.Exec`, since the task API does not exist yet): `Projects` returns known ids and omits unknown; `ProjectByCode("kvem1000")` resolves `KVEM1000`, unknown → nil; `BillingLines` returns active and inactive ordered by code; `Task` resolves a row and returns nil for an unknown id; `OpenTasksForUser` excludes `done`, orders by due date with nulls last; `CanLogTime` true for member/manager on an active project, false for viewer, for `planned`/`completed`/`cancelled`/`on-hold`, and for an outsider; `ProjectEntry.Currency`/`DefaultBillRate` populated. Also an existing-consumer check: `TestDirectory_ProjectsForUser…` still passes.
- [ ] **Step 2: Verify failure** (`mise exec -- go test -count=1 ./internal/projects/... -run Directory`).
- [ ] **Step 3: Implement**: migration; `sqlc.yaml` schema list gains `../db/migrations/00009_projects_tasks.sql`; queries; `go generate`; directory methods (a `toProjectEntry(row)` helper removes the duplication noted in the last review); `values.go` gains `taskStatuses` and `validTaskStatus`.
- [ ] **Step 4: Run** `./internal/projects/... ./internal/db/... ./internal/module/... ./internal/contracts/...`, `go vet ./...`, `golangci-lint run`.
- [ ] **Step 5: Commit** `feat(projects): task tables, default bill rate and the directory methods Time needs`.

### Task 2: Task CRUD, position and my-tasks

**Files:** Create `tasks.go`, `tasks_validation.go`, `tasks_test.go`, `tasks_concurrency_test.go`; modify `openapi/projects.yaml`, `responses.go`, `values.go`, `queries/tasks.sql`, `harness_test.go` (helpers `createTask(t, c, projectID, overrides) taskJSON`, `getTasks(t, c, projectID)`).

**Contract shapes:**

```
TaskResponse { id int32, projectId int32, parentTaskId? int32, title, description?, status, assignee?: {userId uuid, displayName, active bool},
               startDate? date, dueDate? date, estimateHours? number, position int32, completedAt? date-time, revision int32,
               createdAt, updatedAt, checklist: {total int32, done int32}, commentCount int32,
               subtasks?: [TaskResponse]   // present only on the tree endpoint's top-level items }
TaskRequest  { title, description?, status?, assigneeUserId?, startDate?, dueDate?, estimateHours?, parentTaskId? }      // POST
TaskUpdateRequest = TaskRequest minus parentTaskId + revision int32                                                   // PUT (full replace, like projects)
TaskPositionRequest { parentTaskId? int32, position int32 }
MyTaskResponse = TaskResponse fields + { projectCode, projectName }
```

Operations (all `permission:projects:access`):
- `GET /projects/{id}/tasks` → `[TaskResponse]` (top-level with nested `subtasks`, ordered by position). Filter `status` optional, `assigneeUserId` optional.
- `POST /projects/{id}/tasks` → 201. Member/manager. Appends (position = max+1 among siblings). `parentTaskId` must be a top-level task of this project.
- `GET /projects/tasks/{taskId}` → 200 (single, with subtasks); `PUT /projects/tasks/{taskId}` → 200 (revision-guarded 409); `DELETE /projects/tasks/{taskId}` → 204 (cascades).
- `PUT /projects/tasks/{taskId}/position` → 200: move within siblings or re-parent (only to top-level, and a task with subtasks cannot become a subtask); renumber siblings 1..n in one transaction.
- `GET /projects/my-tasks` → `[MyTaskResponse]` (caller's open tasks on visible projects; ordered by due date nulls last, then project code, then position).

**Rules** (`tasks_validation.go`): title required/≤200 trimmed; description ≤4000; status enum; `dueDate ≥ startDate`; `estimateHours > 0`; assignee resolves via `s.deps.Users.User` and is active when set (existing assignment of a since-disabled user stays and renders `active:false`); `status → done` sets `completed_at` (`s.deps.Clock()`), leaving `done` clears it. The task's project is resolved from the path for `/projects/tasks/{taskId}` operations by loading the task first; an unknown task or a task on an invisible project answers the same bare 404.

- [ ] **Step 1: Failing tests**: create (201, appended position, assignee embedded), each validation rule (table), subtask creation and the two nesting refusals, get/update/delete (cascade: checklist+comments+subtasks gone, assert counts), 409 on stale revision, position moves (reorder among siblings; re-parent; refusals), `my-tasks` (assigned open tasks across two projects; excludes done and projects the caller cannot see; ordering), authorization matrix (viewer reads, cannot write 403; outsider 404 identical to unknown; member writes), concurrency: two simultaneous creates get distinct positions; two simultaneous position moves leave siblings numbered 1..n without gaps.
- [ ] **Step 2–4**: verify failure, implement, run (`taskset -c 0-3` once), vet, lint, `gen:client` no drift.
- [ ] **Step 5: Commit** `feat(projects): tasks with subtasks, ordering and a cross-project my-tasks list`.

### Task 3: Checklist items and comments

**Files:** Create `checklist.go`, `comments.go`, `checklist_test.go`, `comments_test.go`; modify `projects.yaml`, `queries/checklist.sql`, `queries/comments.sql`, `responses.go`, `tasks_validation.go`.

Operations:
- `GET /projects/tasks/{taskId}/checklist` → `[ChecklistItemResponse {id int64, text, done, position}]`; `POST` → 201 (appends); `PUT …/{itemId}` `{text?, done?, position?}` → 200 (position renumbers); `DELETE …/{itemId}` → 204. Read: sees project; write: member/manager.
- `GET /projects/tasks/{taskId}/comments` → `[CommentResponse {id int64, author {userId, displayName, active}, body, createdAt, editedAt?}]` oldest first, paged like the timeline; `POST` → 201 (member/manager); `PUT …/{commentId}` `{body}` → 200 (author only; sets `editedAt`); `DELETE …/{commentId}` → 204 (author or manager).
- Task responses' `checklist {total, done}` and `commentCount` come from aggregate queries in one round trip for a tree (no N+1).

- [ ] **Step 1: Failing tests**: checklist add/toggle/reorder/delete and the counts on the task; comment add/edit-by-author/edit-by-other 403/delete-by-manager/delete-by-other 403; viewer reads both, cannot write; outsider 404; text/body limits.
- [ ] **Step 2–4**, **Step 5: Commit** `feat(projects): checklist items and comments on tasks`.

### Task 4: Project default bill rate

**Files:** modify `projects.yaml` (`ProjectFinancials.defaultBillRate`, `ProjectCreateRequest`/`ProjectUpdateRequest.defaultBillRate`), `values.go`, `projects.go`, `responses.go`, `timeline.go` (`billing-changed` field name `defaultBillRate`), tests in `projects_test.go`/`projects_update_test.go`, `docs/projects.md`.

- [ ] **Step 1: Failing tests**: create/update with `defaultBillRate` → stored, returned inside `financials`, absent for members (raw JSON); `> 0` rule; requires `currency` (400 on `currency` when missing); change writes `billing-changed` with the field name only; directory `ProjectEntry.DefaultBillRate` reflects it.
- [ ] **Step 2–4**, **Step 5: Commit** `feat(projects): a default bill rate on the project`.

### Task 5: Frontend — API layer, Tasks tab, board, drawer, my tasks

**Files:** package files per File Structure; tests beside each. Mirror `project-people.tsx` (tab root owns `mt="md"`, own skeleton/alert, capabilities drive actions), `-billing-line-form-modal.tsx` (form modal pattern), `lib/{status,search,dates}.ts`, `components/field.tsx`.

- `api/tasks.ts`: `projectTasksQueryOptions(projectId, filters?)`, `taskQueryOptions(taskId)`, `myTasksQueryOptions()`, `checklistQueryOptions(taskId)`, `commentsQueryOptions(taskId, page)`, mutations `createTask`, `updateTask`, `deleteTask`, `moveTask`, `addChecklistItem`, `updateChecklistItem`, `deleteChecklistItem`, `addComment`, `updateComment`, `deleteComment`; query keys under `["projects", "tasks", …]` so the existing blanket invalidation still works; `ProjectTasks` type re-exports from the generated schema.
- `lib/tasks.ts`: status → i18n key + colour (`todo` gray, `in-progress` blue, `done` green); `isTaskStatus` guard.
- `pages/project-tasks.tsx` (`ProjectTasks({ projectId })`): `SegmentedControl` List | Board. **List**: grouped by status, top-level rows with expandable subtasks, columns title / assignee / due / estimate / checklist progress / comments; row click opens the drawer; "Add task" for members/managers (`capabilities.canManage || role member` — the project response has `capabilities.canManage` only; add `capabilities.canContribute` (member or manager) to `ProjectResponse` in Task 2 if not present, and use it). **Board**: three columns by status, cards, drag between columns updates status (use `@hello-pangea/dnd` only if already in the workspace; otherwise Mantine-native move buttons — check `package.json`; do not add a dependency without saying so).
- `pages/-task-drawer.tsx`: Mantine `Drawer` with title (inline edit), description, status, assignee (assignable-users search from `people.ts`), dates, estimate, subtasks list (add inline), checklist (add/toggle/delete), comments (list + composer; edit/delete own), delete task with confirm.
- `pages/my-tasks.tsx` (`MyTasksPage`, no props, reads the router): table of open tasks across projects with project code link, due date, status; quick status change.
- Spotlight quick action "Create task" is host work (Task 6).
- i18n en + nb for everything.

- [ ] **Step 1: Failing tests**: list renders grouped tasks with subtasks; board moves a card and PUTs the status; drawer edits title, toggles a checklist item, posts a comment; actions hidden for viewers; my-tasks lists across projects and links; error/empty states.
- [ ] **Step 2–4**: implement; `bun run --cwd apps/projects/frontend test|typecheck|lint`, `translations:check`, `i18n:test`, `bunx biome check .`.
- [ ] **Step 5: Commit** `feat(projects-ui): tasks tab with list and board, task drawer and my tasks`.

### Task 6: Host integration and docs

**Files:** `apps/host/frontend/src/routes/projects/$projectId.tasks.tsx` (renders `ProjectTasks`), `routes/projects/my-tasks.tsx` (renders `MyTasksPage`), `-project-detail-layout.tsx` (Tasks tab between Overview and People, gated on seeing the project), `apps.ts` (sidebar entry "My tasks" → `/projects/my-tasks`, `projects:access`), `app-spotlight.tsx` (quick action "Create task" → `/projects/my-tasks?create=true` opening a project picker + task form, or, simpler and acceptable: navigate to `/projects` — decide and say so), catalogs (`project.ts` tab label, navigation label, spotlight strings; en + nb), `routeTree.gen.ts` regenerated; tests (`project-detail-tabs.test.ts` gains the Tasks tab; route test for my-tasks; spotlight test). `docs/projects.md` gains a Tasks section (model, rules, authorization, the directory methods) and `ROADMAP.md`'s phase 2 gets "tasks (done)" marked.

- [ ] **Step 1–4**: tests first, implement, `bun run --cwd apps/host/frontend test|typecheck|lint|build`, `frontend:lint`, `frontend:typecheck`, translations, biome.
- [ ] **Step 5: Commit** `feat(frontend): tasks tab, my tasks and the create-task action`.

### Task 7: Final gates and PR

- [ ] `go generate ./...` and `bun run gen:client` no drift; `go vet`; `golangci-lint`; full Go suite pinned to four CPUs; `frontend:lint|typecheck|test|build`; `translations:check`; `biome check .`; `mise run smoke`.
- [ ] Normalise commit trailers if any subagent used a different model name; push; `gh pr create` against `main` (note in the body that it includes the plan-docs branch's two commits until #99 merges); body: summary, decisions, test evidence; ends with the attribution line. Watch checks with Monitor parsing `gh pr checks` default output; fix on red; never merge.
