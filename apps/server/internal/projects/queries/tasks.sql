-- The task queries (design §3.1, §4.1). Three things run through them: a
-- task is always read with the two aggregates the contract embeds (its
-- checklist progress and its comment count), the sibling group — a project's
-- top-level tasks, or one task's subtasks — is the unit ordering is decided
-- in, and every write that decides a position takes the project's ordering
-- lock first (AcquireTaskOrderLock).
--
-- The aggregates are lateral subqueries rather than a join with a GROUP BY so
-- that one query answers a whole project's tree without multiplying rows:
-- a task with three checklist items and two comments is still one row.

-- name: GetTask :one
-- GetTask fetches one task by id, with none of its aggregates: it is the read
-- that resolves which project a /projects/tasks/{taskId} request is about, and
-- an unknown task and a task on an invisible project answer the same bare 404,
-- so the row is loaded first and discarded after (projects.go's GetProject
-- does the same for a project).
SELECT * FROM projects.tasks WHERE id = @id;

-- name: LockTask :one
-- LockTask is GetTask with the row held for the rest of the transaction: an
-- update decides its completed_at stamp from the status the row actually has,
-- and a move decides both sibling groups from the parent it actually has.
-- Deciding either from a read taken before the transaction would let another
-- writer move it in between.
SELECT * FROM projects.tasks WHERE id = @id FOR UPDATE;

-- name: GetTaskWithCounts :one
-- GetTaskWithCounts is one task in the shape the contract answers with: the
-- row plus its checklist progress and comment count. It is what an update or
-- a move renders its answer from, since neither touches those children but
-- both must report them.
SELECT sqlc.embed(t),
       coalesce(c.total, 0)::integer    AS checklist_total,
       coalesce(c.done, 0)::integer     AS checklist_done,
       coalesce(m.comments, 0)::integer AS comment_count
FROM projects.tasks t
LEFT JOIN LATERAL (
    SELECT count(*) AS total, count(*) FILTER (WHERE i.done) AS done
    FROM projects.task_checklist_items i WHERE i.task_id = t.id
) c ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS comments
    FROM projects.task_comments k WHERE k.task_id = t.id
) m ON true
WHERE t.id = @id;

-- name: ListProjectTasks :many
-- ListProjectTasks is a whole project's tasks, aggregates included, in one
-- query: the tree is assembled in Go from these rows, so a project with fifty
-- tasks costs one round trip and not one per parent.
--
-- The order is what the tree is built from: top-level tasks first, then
-- subtasks, each group by position. Both filters are optional, and an absent
-- one filters nothing.
SELECT sqlc.embed(t),
       coalesce(c.total, 0)::integer    AS checklist_total,
       coalesce(c.done, 0)::integer     AS checklist_done,
       coalesce(m.comments, 0)::integer AS comment_count
FROM projects.tasks t
LEFT JOIN LATERAL (
    SELECT count(*) AS total, count(*) FILTER (WHERE i.done) AS done
    FROM projects.task_checklist_items i WHERE i.task_id = t.id
) c ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS comments
    FROM projects.task_comments k WHERE k.task_id = t.id
) m ON true
WHERE t.project_id = @project_id
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status))
  AND (sqlc.narg(assignee_user_id)::uuid IS NULL OR t.assignee_user_id = sqlc.narg(assignee_user_id))
ORDER BY (t.parent_task_id IS NOT NULL), t.position, t.id;

-- name: ListTaskWithSubtasks :many
-- ListTaskWithSubtasks is one task and the subtasks under it, aggregates
-- included, in one query — the single-task read. The task itself sorts first
-- (its own id is not "not the id asked for"), so the caller never has to look
-- for it among its children.
SELECT sqlc.embed(t),
       coalesce(c.total, 0)::integer    AS checklist_total,
       coalesce(c.done, 0)::integer     AS checklist_done,
       coalesce(m.comments, 0)::integer AS comment_count
FROM projects.tasks t
LEFT JOIN LATERAL (
    SELECT count(*) AS total, count(*) FILTER (WHERE i.done) AS done
    FROM projects.task_checklist_items i WHERE i.task_id = t.id
) c ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS comments
    FROM projects.task_comments k WHERE k.task_id = t.id
) m ON true
WHERE t.id = @id OR t.parent_task_id = @id
ORDER BY (t.id <> @id), t.position, t.id;

-- name: ListMyOpenTasks :many
-- ListMyOpenTasks is design §4.1's "my tasks": every task assigned to the
-- caller that is not done, on a project the caller can see. Visibility is the
-- query's own predicate through projects.visible — the same function the
-- project list and every stats query use — rather than a filter applied to the
-- rows afterwards, so a task on a project the caller lost their role on simply
-- does not come back.
--
-- The order is the one the list is read in: what is due first, then by project
-- so one project's tasks stay together, then by the order inside it.
SELECT sqlc.embed(t),
       p.code AS project_code,
       p.name AS project_name,
       coalesce(c.total, 0)::integer    AS checklist_total,
       coalesce(c.done, 0)::integer     AS checklist_done,
       coalesce(m.comments, 0)::integer AS comment_count
FROM projects.tasks t
JOIN projects.projects p ON p.id = t.project_id
LEFT JOIN LATERAL (
    SELECT count(*) AS total, count(*) FILTER (WHERE i.done) AS done
    FROM projects.task_checklist_items i WHERE i.task_id = t.id
) c ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS comments
    FROM projects.task_comments k WHERE k.task_id = t.id
) m ON true
WHERE t.assignee_user_id = @user_id::uuid
  AND t.status <> 'done'
  AND projects.visible(p.id, @user_id, sqlc.arg(see_all)::boolean)
ORDER BY t.due_date NULLS LAST, p.code, t.position, t.id;

-- name: AcquireTaskOrderLock :exec
-- AcquireTaskOrderLock is the serialisation point of every write that decides
-- a position inside one project: a create appending after its siblings, and a
-- move renumbering them. A row lock cannot cover a create — the number two
-- concurrent creates race for is the gap after the last row, and a gap has no
-- row to lock — so the lock is taken on the project instead, for the rest of
-- the transaction. It is a two-argument advisory lock, which is a lock space
-- of its own: it can never collide with identity's single-argument ones.
SELECT pg_advisory_xact_lock(@lock_class::integer, @project_id::integer);

-- name: MaxSiblingPosition :one
-- MaxSiblingPosition is the number a create appends after, 0 for the first
-- task of a group. IS NOT DISTINCT FROM is what makes "the project's top-level
-- tasks" (parent_task_id IS NULL) one sibling group like any other rather than
-- a second query.
SELECT coalesce(max(position), 0)::integer FROM projects.tasks
WHERE project_id = @project_id
  AND parent_task_id IS NOT DISTINCT FROM sqlc.narg(parent_task_id)::integer;

-- name: SiblingTaskIDs :many
-- SiblingTaskIDs is one sibling group in its current order, every row held for
-- the rest of the transaction. The move reorders this list in Go and writes it
-- back with RenumberTasks, so what it renumbers is exactly what it read.
SELECT id FROM projects.tasks
WHERE project_id = @project_id
  AND parent_task_id IS NOT DISTINCT FROM sqlc.narg(parent_task_id)::integer
ORDER BY position, id
FOR UPDATE;

-- name: RenumberTasks :exec
-- RenumberTasks writes a sibling group's order back as 1..n in one statement:
-- ids is the group in its new order and WITH ORDINALITY is the number each one
-- takes. A row already carrying its number is left alone, so a move that only
-- reordered part of a group does not touch the rest of it.
UPDATE projects.tasks t
SET position = v.ord::integer, updated_at = @now::timestamptz
FROM unnest(@ids::integer[]) WITH ORDINALITY AS v(id, ord)
WHERE t.id = v.id AND t.position <> v.ord::integer;

-- name: InsertTask :one
-- InsertTask creates one task. created_at and updated_at are the same instant
-- on creation, supplied by the caller from Deps.Clock(); status and revision
-- are given explicitly since a body may carry a status, and position is the
-- number computed under the project's ordering lock.
INSERT INTO projects.tasks (
    project_id, parent_task_id, title, description, status, assignee_user_id,
    start_date, due_date, estimate_hours, position, completed_at,
    created_by_user_id, created_at, updated_at
) VALUES (
    @project_id, @parent_task_id, @title, @description, @status, @assignee_user_id,
    @start_date, @due_date, @estimate_hours, @position, @completed_at,
    @created_by_user_id, @now::timestamptz, @now::timestamptz
)
RETURNING *;

-- name: UpdateTask :one
-- UpdateTask applies one edit, guarded by the revision the caller read (design
-- §3). A revision that has moved on matches no row, which is the handler's
-- 409: the second writer never silently overwrites the first. Where the task
-- sits is not part of an update — position and parent_task_id are the move's,
-- so that an edit saved from an open drawer cannot undo somebody's reordering.
UPDATE projects.tasks SET
    title = @title,
    description = @description,
    status = @status,
    assignee_user_id = @assignee_user_id,
    start_date = @start_date,
    due_date = @due_date,
    estimate_hours = @estimate_hours,
    completed_at = @completed_at,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND revision = @revision
RETURNING *;

-- name: SetTaskParent :exec
-- SetTaskParent moves a task between sibling groups. It writes no position:
-- the number is RenumberTasks', which runs over the whole group afterwards, so
-- there is never a moment where two siblings both claim one number.
UPDATE projects.tasks SET
    parent_task_id = sqlc.narg(parent_task_id)::integer,
    updated_at = @now::timestamptz
WHERE id = @id;

-- name: CountSubtasks :one
-- CountSubtasks is D6's depth rule, asked of the task that is being moved: a
-- task with subtasks of its own cannot become a subtask, because that would
-- nest three levels deep.
SELECT count(*) FROM projects.tasks WHERE parent_task_id = @parent_task_id;

-- name: DeleteTask :execrows
-- DeleteTask removes one task. Its subtasks, checklist items and comments go
-- with it through the foreign keys' ON DELETE CASCADE (migration 00009), so
-- nothing is left pointing at a task that is gone. The row count is what
-- decides the 404: two deletes racing must not both answer 204.
DELETE FROM projects.tasks WHERE id = @id;
