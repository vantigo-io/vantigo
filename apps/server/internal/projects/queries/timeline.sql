-- name: InsertTimelineEntry :exec
-- InsertTimelineEntry records one generated project event. It is always
-- called inside the transaction of the change it accompanies, so a project
-- that exists always has the entry that created it. actor_user_id is
-- nullable for an event no signed-in user caused; actor_display never is, so
-- the timeline stays readable after the account behind it is gone.
INSERT INTO projects.timeline_entries (project_id, event_type, payload, actor_user_id, actor_display, occurred_at)
VALUES (@project_id, @event_type, @payload, @actor_user_id, @actor_display, @now::timestamptz);
