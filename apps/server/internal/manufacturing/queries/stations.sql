-- Shared shop-floor stations, Manufacturing's side (manufacturing phase 2
-- M131, M137).

-- name: StationWorkCenterIDs :many
SELECT work_center_id FROM manufacturing.station_work_centers WHERE station_id = @station_id ORDER BY work_center_id;

-- name: DeleteStationWorkCenters :exec
DELETE FROM manufacturing.station_work_centers WHERE station_id = @station_id;

-- name: InsertStationWorkCenters :exec
INSERT INTO manufacturing.station_work_centers (station_id, work_center_id)
SELECT @station_id, unnest(@work_center_ids::bigint[])
ON CONFLICT DO NOTHING;

-- name: InsertStationWorkCenterChange :exec
INSERT INTO manufacturing.station_work_center_changes (station_id, before_ids, after_ids, actor_user_id, actor_display, changed_at)
VALUES (@station_id, @before_ids::bigint[], @after_ids::bigint[], @actor_user_id, @actor_display, @now);

-- name: CountWorkCenters :one
SELECT count(*)::integer FROM manufacturing.work_centers WHERE id = ANY(@ids::bigint[]);

-- name: StationServesWorkCenter :one
SELECT EXISTS (
    SELECT 1 FROM manufacturing.station_work_centers
    WHERE station_id = @station_id AND work_center_id = @work_center_id
)::boolean AS serves;

-- name: StationServesWorkOrder :one
-- StationServesWorkOrder reports whether a work order is at one of the
-- station's centers or — a group's work order (manufacturing phase 2 M119)
-- not yet started, so not yet fixed at a member — whether one of its group's
-- members is. With fixed_only the group does not count: an action on a
-- started work order is judged where it runs.
SELECT EXISTS (
    SELECT 1 FROM manufacturing.work_orders w
    WHERE w.id = @work_order_id
      AND (EXISTS (SELECT 1 FROM manufacturing.station_work_centers s
                   WHERE s.station_id = @station_id AND s.work_center_id = w.work_center_id)
           OR (NOT @fixed_only::boolean AND w.work_center_group_id IS NOT NULL AND w.status IN ('pending', 'ready') AND EXISTS (
                   SELECT 1 FROM manufacturing.work_center_group_members m
                   JOIN manufacturing.station_work_centers s ON s.work_center_id = m.work_center_id AND s.station_id = @station_id
                   WHERE m.group_id = w.work_center_group_id)))
)::boolean AS serves;

-- name: StationServesOrderOutput :one
-- StationServesOrderOutput reports whether an order's last work order that is
-- not cancelled — the one whose finish registers the production (M29) — is at
-- one of the station's centers.
SELECT EXISTS (
    SELECT 1 FROM (
        SELECT w.work_center_id FROM manufacturing.work_orders w
        WHERE w.order_id = @order_id AND w.status <> 'cancelled'
        ORDER BY w.sequence DESC, w.id DESC
        LIMIT 1
    ) last
    JOIN manufacturing.station_work_centers s ON s.work_center_id = last.work_center_id AND s.station_id = @station_id
)::boolean AS serves;
