-- What has been logged against projects, as contracts.ProjectActuals reports
-- it (design §4). One grouped read serves both the single-project and the
-- batch call: the folding by the caller's currency happens in Go over these
-- groups, so a project asked in NOK whose entries are priced in SEK ends up
-- with unpriced hours rather than a sum in no currency.
--
-- The grain is project, billing line, bucket (design §2 E2: approved and
-- invoiced are one bucket, rejected joins draft), bill currency and cost
-- currency — the two currencies are separate columns, so an entry can be
-- billed in one and cost in another and each is folded on its own.
--
-- Hours are exact: hours is numeric(5,2), so hours * 100 is a whole number
-- and the bigint cast never rounds anything away. The amounts are the
-- unrounded numeric sums as text — rounding the grand total once is not the
-- same number as rounding every group and adding those, so the rounding is
-- Go's, after the groups are added.

-- name: ProjectActualGroups :many
SELECT project_id,
       billing_line_id,
       CASE
           WHEN status IN ('approved', 'invoiced') THEN 'approved'
           WHEN status = 'submitted' THEN 'submitted'
           ELSE 'draft'
       END AS bucket,
       bill_currency,
       cost_currency,
       SUM(hours * 100)::bigint AS hours_hundredths,
       COALESCE(SUM(hours * 100) FILTER (WHERE billable), 0)::bigint AS billable_hours_hundredths,
       COALESCE(SUM(hours * 100) FILTER (WHERE bill_rate IS NOT NULL), 0)::bigint AS priced_hours_hundredths,
       COALESCE(SUM(hours * bill_rate), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate), 0)::text AS cost_amount,
       MAX(entry_date)::date AS last_entry_date
FROM time.entries
WHERE project_id = ANY(@project_ids::integer[])
GROUP BY project_id,
         billing_line_id,
         CASE
             WHEN status IN ('approved', 'invoiced') THEN 'approved'
             WHEN status = 'submitted' THEN 'submitted'
             ELSE 'draft'
         END,
         bill_currency,
         cost_currency;
