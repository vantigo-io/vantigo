-- The reads lane's own queries (design S19): what the views read beyond the
-- window of allocations.sql.

-- name: PersonConfirmedAllocationsFrom :many
-- PersonConfirmedAllocationsFrom is one person's confirmed allocations ending
-- on or after @from_date, by from date and id: My planning, the person's
-- confirmed rows from today on.
SELECT * FROM planning.allocations
WHERE person_id = @person_id AND status = 'confirmed' AND to_date >= @from_date::date
ORDER BY from_date, id;
