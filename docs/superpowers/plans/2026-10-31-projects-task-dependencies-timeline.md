# Implementation Plan: Projects Phase 4 — Delivery B (Lightweight Timeline & Task Dependencies)

## 1. Goal
Implement Delivery B of Projects Phase 4:
- Finish-to-Start (FS) task dependencies with cycle-free DAG validation.
- Schema, SQL queries, Go HTTP handlers, and contract tests for task dependencies.
- Downstream cascade reschedule option.
- Frontend API client, React Query options, and Vitest tests.
- Frontend Timeline view in the Tasks tab with task bars, dependency arrows, conflict indicators, and deep integration with delivery milestones.
- Documentation updates (English reference, English & Norwegian user docs, Roadmap).

## 2. Architecture & Design

### 2.1 Database Schema (Migration `00097_projects_task_dependencies.sql`)
```sql
CREATE TABLE projects.task_dependencies (
    id                  integer GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id          integer NOT NULL REFERENCES projects.projects (id) ON DELETE CASCADE,
    predecessor_task_id integer NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    successor_task_id   integer NOT NULL REFERENCES projects.tasks (id) ON DELETE CASCADE,
    created_by_user_id  uuid    NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_task_dependencies_pair UNIQUE (predecessor_task_id, successor_task_id),
    CONSTRAINT chk_task_dependencies_distinct CHECK (predecessor_task_id <> successor_task_id)
);
CREATE INDEX ix_task_dependencies_project_id ON projects.task_dependencies (project_id);
CREATE INDEX ix_task_dependencies_predecessor ON projects.task_dependencies (predecessor_task_id);
CREATE INDEX ix_task_dependencies_successor ON projects.task_dependencies (successor_task_id);
```

### 2.2 API Endpoints (`openapi/projects.yaml`)
- `GET /api/v1/projects/{id}/dependencies`: returns list of dependencies for the project.
- `POST /api/v1/projects/{id}/dependencies`: creates a dependency link `{ predecessorTaskId, successorTaskId }`.
  - Validates both tasks belong to `{id}`.
  - Validates DAG (no cycle $B \to \dots \to A$).
- `DELETE /api/v1/projects/dependencies/{dependencyId}`: deletes a dependency link.
- `POST /api/v1/projects/tasks/{taskId}/reschedule-downstream`: shifts downstream tasks by the date difference.

### 2.3 Cycle Detection (DAG)
Before inserting `(predecessor, successor)`:
- Run depth-limited graph traversal (BFS/DFS) on existing dependencies starting from `successor` to ensure `predecessor` is not reachable.
- If reachable, reject with HTTP 400 problem (`cyclic_dependency`).

### 2.4 Timeline View in Frontend (`apps/projects/frontend`)
- Expand `TaskView` with `"timeline"`.
- Create `-task-timeline-view.tsx`:
  - Canvas / SVG / HTML layout showing calendar timeline columns (days / weeks).
  - Rows for tasks with start and due dates.
  - Delivery milestone markers on target dates.
  - Dependency connector lines between predecessor end and successor start.
  - Conflict warning highlight when `predecessor.dueDate > successor.startDate`.
  - Drawer support and quick dependency adder.

---

## 3. Phased Execution Steps

### Step 1: Migration & Schema Test
- `00097_projects_task_dependencies.sql`
- `schema_projects_task_dependencies_test.go`
- Update `apps/server/internal/projects/sqlc.yaml`

### Step 2: OpenAPI Specification & Code Generation
- Add dependencies schemas and endpoints to `openapi/projects.yaml`
- Sync with `apps/server/internal/openapi/specs/projects.yaml`
- Run `cd apps/server && go generate ./...`
- Run `bun run gen:client`

### Step 3: SQL Queries & sqlc
- Create `internal/projects/queries/dependencies.sql`
- Run `sqlc generate -f internal/projects/sqlc.yaml`

### Step 4: Backend Implementation & Tests
- Implement `task_dependencies.go`, `task_dependencies_validation.go`
- Implement cycle detection algorithm
- Implement cascade rescheduling logic
- Add timeline event `task-dependency-added`, `task-dependency-removed`
- Create `task_dependencies_test.go` with full contract test coverage

### Step 5: Frontend API & State
- Create `apps/projects/frontend/src/api/dependencies.ts` and test
- Add React Query hooks

### Step 6: Frontend Timeline View & UI
- Build `-task-timeline-view.tsx` and test
- Add dependency management inside `TaskDrawer` (list dependencies, add, remove)
- Add timeline view toggle in `ProjectTasks`
- Add i18n keys for English and Norwegian

### Step 7: Documentation & Quality Checks
- Update `docs/src/content/docs/en/reference/projects.md`
- Update `docs/src/content/docs/en/user/projects.md` and `docs/src/content/docs/nb/user/projects.md`
- Update `ROADMAP.md`
- Verify `docs:check`, `server:test`, `frontend:check`
