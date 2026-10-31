# Delivery Milestones Implementation Plan (PR A of Projects Phase 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add delivery milestones with derived task completion progress and task linking to the `projects` module, with full API CRUD, backend tests, and frontend UI components in `@vantigo/projects-ui`.

**Architecture:** Vertical slice inside `apps/server/internal/projects`, schema `projects`, contract `openapi/projects.yaml`, frontend package `@vantigo/projects-ui`. Follows existing module patterns (authorize → validate → db.WithTx → timeline event). One new migration `00096_projects_delivery_milestones.sql`. No new module, no new permissions.

**Tech Stack:** Go (pgx v5, sqlc, goose, oapi-codegen strict server), PostgreSQL, React 19, Mantine 9, TanStack Router + Query, Vitest, Bun.

**Spec:** `docs/superpowers/specs/2026-10-31-projects-delivery-milestones-timeline-design.md` (Delivery A).

## Global Constraints

- **Pattern files:** Backend `apps/server/internal/projects/{milestones.go,tasks.go,tasks_validation.go,authorize.go,responses.go,timeline.go,directory.go}`.
- **Boundaries:** `internal/projects/**` imports no other module schema; `tasks.delivery_milestone_id` references `projects.delivery_milestones`.
- **Toolchain:** `mise exec --` for go/sqlc/bun; `golangci-lint run` from `apps/server`; `bun run --cwd <dir> test`.
  - After `openapi/projects.yaml` changes: `cd apps/server && mise exec -- go generate ./...` and `mise exec -- bun run gen:client`.
  - Contract coverage: Every operation in `projects.yaml` must be exercised by a test (`main_test.go`'s `RequireCoverage`).
- **Authorization:** `canSee` reads delivery milestones; `canContribute` (member or manager) writes delivery milestones.
- **i18n:** Every user-facing UI string in English (`en`) and Norwegian Bokmål (`nb`).
- **Commits:** Conventional Commits (`feat(projects): ...`, `feat(projects-ui): ...`, `docs(projects): ...`).
- **Branch:** `feat/projects-delivery-milestones`.

## File Structure

```
apps/server/internal/db/migrations/00096_projects_delivery_milestones.sql
apps/server/internal/projects/
  delivery_milestones.go
  delivery_milestones_validation.go
  queries/delivery_milestones.sql
  sqlc.yaml
  delivery_milestones_test.go
openapi/projects.yaml
apps/projects/frontend/src/
  api/delivery-milestones.ts
  api/delivery-milestones.test.ts
  components/delivery-milestone-badge.tsx
  pages/-delivery-milestones-card.tsx
  pages/-delivery-milestone-modal.tsx
  i18n.ts
docs/src/content/docs/en/reference/projects.md
docs/src/content/docs/en/user/projects.md
docs/src/content/docs/nb/user/projects.md
```

---

## Tasks

- [ ] **Task 1: Database Migration `00096_projects_delivery_milestones.sql`**
  - Create table `projects.delivery_milestones` (id, project_id, name, description, target_date, status, position, completed_at, completed_by_user_id, revision, created_by_user_id, created_at, updated_at).
  - Add `delivery_milestone_id` to `projects.tasks` with foreign key and index.
  - Update `apps/server/internal/projects/sqlc.yaml` to include migration `00096`.

- [ ] **Task 2: OpenAPI Specification in `openapi/projects.yaml`**
  - Define paths:
    - `GET /api/v1/projects/{projectId}/delivery-milestones`
    - `POST /api/v1/projects/{projectId}/delivery-milestones`
    - `GET /api/v1/projects/delivery-milestones/{milestoneId}`
    - `PUT /api/v1/projects/delivery-milestones/{milestoneId}`
    - `DELETE /api/v1/projects/delivery-milestones/{milestoneId}`
    - `PUT /api/v1/projects/delivery-milestones/{milestoneId}/position`
    - `POST /api/v1/projects/delivery-milestones/{milestoneId}/status`
  - Define schemas: `DeliveryMilestoneResponse`, `DeliveryMilestoneListResponse`, `CreateDeliveryMilestoneRequest`, `UpdateDeliveryMilestoneRequest`, `MoveDeliveryMilestoneRequest`, `DeliveryMilestoneStatusRequest`.
  - Update `CreateTaskRequest`, `UpdateTaskRequest`, `TaskResponse`, `TaskTreeResponse` with `deliveryMilestoneId` and `deliveryMilestone` summary.
  - Run code generation: `go generate ./...` in `apps/server` and `bun run gen:client`.

- [ ] **Task 3: SQL Queries for Delivery Milestones**
  - In `apps/server/internal/projects/queries/delivery_milestones.sql`:
    - List project delivery milestones with task count aggregations (`COUNT(t.id)` total, `COUNT(t.id) FILTER (WHERE t.status = 'done')` completed, `SUM(t.estimate_hours)` total and completed).
    - Insert delivery milestone with next position.
    - Get single delivery milestone by ID with progress stats.
    - Update delivery milestone fields and revision.
    - Update delivery milestone position and renumber siblings.
    - Update delivery milestone status.
    - Delete delivery milestone and renumber remaining.
  - Run `mise exec -- sqlc generate` in `apps/server`.

- [ ] **Task 4: Go Backend Implementation**
  - Create `delivery_milestones.go` implementing strict server interface handlers.
  - Create `delivery_milestones_validation.go` with validation logic and concurrency guards.
  - Update task creation & update handlers in `tasks.go` and `tasks_validation.go` to validate and persist `delivery_milestone_id` (ensuring project match).
  - Emit timeline entries (`delivery-milestone-added`, `delivery-milestone-changed`, `delivery-milestone-removed`, `delivery-milestone-status-changed`).

- [ ] **Task 5: Backend Tests & Contract Coverage**
  - Create `delivery_milestones_test.go` covering CRUD, permissions, validation errors, concurrency conflict (409), reordering, status transitions, and task milestone unlinking.
  - Verify all OpenAPI operations are exercised to satisfy `RequireCoverage` in `main_test.go`.
  - Run `mise run server:test` and `mise run server:check`.

- [ ] **Task 6: Frontend API Client & State Hooks**
  - Create `apps/projects/frontend/src/api/delivery-milestones.ts` with React Query options and mutation hooks.
  - Add Vitest tests `delivery-milestones.test.ts`.

- [ ] **Task 7: Frontend UI Components**
  - Create `DeliveryMilestoneBadge` component with diamond indicator and progress tooltip.
  - Create `DeliveryMilestonesCard` on the project tasks tab / overview.
  - Create `DeliveryMilestoneModal` for creating and editing delivery milestones.
  - Add delivery milestone dropdown selector in the task form and task drawer.
  - Add i18n translations in `apps/projects/frontend/src/i18n.ts` for English and Norwegian Bokmål.

- [ ] **Task 8: Documentation & Quality Gates**
  - Update `docs/src/content/docs/en/reference/projects.md` with the new delivery milestones domain model, endpoints, and validation rules.
  - Update `docs/src/content/docs/en/user/projects.md` and `docs/src/content/docs/nb/user/projects.md`.
  - Update `ROADMAP.md` noting Delivery A completion.
  - Verify `mise run server:test`, `mise run server:check`, `mise run frontend:check`, and `mise run docs:check`.
