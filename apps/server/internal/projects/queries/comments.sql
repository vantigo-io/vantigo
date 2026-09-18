-- The comment queries (design §3.1, §4.1). A task's comments are its own
-- history — which is why a task writes nothing to the project timeline — so
-- they are read oldest first and paged the way the timeline is.
--
-- author_user_id is opaque here, exactly as assignee_user_id is on a task: no
-- query in this module joins identity's schema, so a comment's author is named
-- afterwards through contracts.UserDirectory, in one call for the whole page.
--
-- Every comment query is scoped by task_id as well as by id, because a comment
-- is addressed under the task in its path: a comment of another task must
-- answer the same bare 404 an unknown one does.

-- name: ListTaskComments :many
-- ListTaskComments is one page of a task's comments, oldest first, in the
-- order ix_task_comments_task_id_created_at is built for. The id breaks ties:
-- two comments can share an instant, and they must still come back in a stable
-- order, the first written first.
SELECT * FROM projects.task_comments
WHERE task_id = @task_id
ORDER BY created_at, id
LIMIT @page_size OFFSET @page_offset;

-- name: CountTaskComments :one
-- CountTaskComments is ListTaskComments' total, for the page envelope.
SELECT count(*) FROM projects.task_comments WHERE task_id = @task_id;

-- name: GetTaskComment :one
-- GetTaskComment is one comment of one task: the read that settles both
-- whether the comment is there and who wrote it, which is what decides who may
-- change or remove it.
SELECT * FROM projects.task_comments WHERE id = @id AND task_id = @task_id;

-- name: InsertTaskComment :one
-- InsertTaskComment writes one comment. created_at is supplied by the caller
-- from Deps.Clock(); edited_at stays null, which is what "nobody has rewritten
-- this" means on the wire.
INSERT INTO projects.task_comments (task_id, author_user_id, body, created_at)
VALUES (@task_id, @author_user_id, @body, @now::timestamptz)
RETURNING *;

-- name: UpdateTaskComment :one
-- UpdateTaskComment rewrites one comment and stamps when it happened, so a
-- reader can tell a comment that was changed from one that was not. The author
-- is never rewritten: only the author reaches this statement.
UPDATE projects.task_comments SET
    body = @body,
    edited_at = @now::timestamptz
WHERE id = @id AND task_id = @task_id
RETURNING *;

-- name: DeleteTaskComment :execrows
-- DeleteTaskComment removes one comment of one task. The row count is what
-- decides the 404: two deletes racing must not both answer 204.
DELETE FROM projects.task_comments WHERE id = @id AND task_id = @task_id;
