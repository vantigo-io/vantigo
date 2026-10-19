-- name: CalendarTasks :many
-- CalendarTasks is this module's calendar source (calendar design D1): the
-- tasks assigned to the caller falling due in the window, whatever their
-- status — the calendar strikes the done ones, which is why ListMyOpenTasks,
-- open-only, does not serve. Visibility is projects.visible, the predicate the
-- list, the stats and "my tasks" use: a task on a project the caller holds no
-- role on and cannot see all of does not come back, however it is assigned.
SELECT t.id, t.project_id, t.title, t.status, t.due_date, p.code AS project_code
FROM projects.tasks t
JOIN projects.projects p ON p.id = t.project_id
WHERE t.assignee_user_id = sqlc.arg(user_id)::uuid
  AND t.due_date BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
  AND projects.visible(t.project_id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)
ORDER BY t.due_date, p.code, t.position, t.id;
