-- name: InsertTimelineEntry :exec
-- InsertTimelineEntry records one generated project event. It is always
-- called inside the transaction of the change it accompanies, so a project
-- that exists always has the entry that created it. actor_user_id is
-- nullable for an event no signed-in user caused; actor_display never is, so
-- the timeline stays readable after the account behind it is gone.
INSERT INTO projects.timeline_entries (project_id, event_type, payload, actor_user_id, actor_display, occurred_at)
VALUES (@project_id, @event_type, @payload, @actor_user_id, @actor_display, @now::timestamptz);

-- name: ListTimelineEntries :many
-- ListTimelineEntries is one page of a project's timeline, newest first, in
-- the order ix_timeline_entries_project_id_occurred_at is built for. The id
-- breaks ties: several entries of one change share an instant (they are
-- written in one transaction from one clock reading), and they must still
-- come back in a stable order, newest written first.
SELECT * FROM projects.timeline_entries
WHERE project_id = @project_id
ORDER BY occurred_at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: CountTimelineEntries :one
-- CountTimelineEntries is ListTimelineEntries' total, for the page envelope.
SELECT count(*) FROM projects.timeline_entries WHERE project_id = @project_id;
