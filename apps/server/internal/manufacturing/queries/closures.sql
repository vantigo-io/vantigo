-- A work center's closures (manufacturing design M9): ranges of days it is
-- closed, at most 100 per work center. A write locks the work center's row
-- first, so the count is true when it is read.

-- name: ListClosures :many
SELECT * FROM manufacturing.work_center_closures
WHERE work_center_id = @work_center_id
ORDER BY from_date, id;

-- name: CountClosures :one
SELECT count(*)::int FROM manufacturing.work_center_closures WHERE work_center_id = @work_center_id;

-- name: InsertClosure :one
INSERT INTO manufacturing.work_center_closures (work_center_id, from_date, to_date, note)
VALUES (@work_center_id, @from_date, @to_date, @note)
RETURNING *;

-- name: DeleteClosure :execrows
-- DeleteClosure removes one closure of the work center; 0 rows is a closure
-- that is not the work center's.
DELETE FROM manufacturing.work_center_closures WHERE id = @id AND work_center_id = @work_center_id;
