-- What has been logged against projects, as contracts.ProjectActuals reports
-- it (design §4). One grouped read serves both the single-project and the
-- batch call: the folding by the caller's currency happens in Go over these
-- groups, so a project asked in NOK whose entries are priced in SEK ends up
-- with unpriced hours rather than a sum in no currency.
--
-- The grain is project, billing line, bucket (design §2 E2: rejected joins
-- draft; approved and invoiced are one bucket in the contract, but invoiced is
-- its own key here so Go can also report it as ActualsTotals.Invoiced, the part
-- of Approved already billed — customer 360 design D1), bill currency and cost
-- currency — the two currencies are separate columns, so an entry can be
-- billed in one and cost in another and each is folded on its own.
--
-- Hours are exact: hours is numeric(5,2), so hours * 100 is a whole number
-- and the bigint cast never rounds anything away. The amounts are the
-- unrounded numeric sums as text — rounding the grand total once is not the
-- same number as rounding every group and adding those, so the rounding is
-- Go's, after the groups are added.
--
-- bill_amount and priced_hours_hundredths are both qualified with billable,
-- the way ProjectBillingTotals restricts its whole query: only billable hours
-- can be priced, so only billable hours can be unpriced, and the project
-- summary and the economy view report the same figures. The rate chain never
-- prices a non-billable entry (rates.go's early return), so today the filter
-- changes nothing — it is here so that a row that somehow carries both would
-- be left out of the amount as well as out of the split, rather than showing
-- up as money belonging to hours that are counted in neither.
--
-- costed_hours_hundredths and cost_amount are not qualified: non-billable
-- work still costs the company, and leaving its cost out would make those
-- hours look uncosted.
--
-- The amounts are multiplied where they are summed (work types design D3):
-- each entry's hours × its base rate × its work type's multiplier, NULL (no
-- type) counting as 100 %. The multiplier is applied as COALESCE(pct, 100) ×
-- 0.01 — a multiplication, which numeric does exactly — rather than / 100, a
-- division PostgreSQL rounds to a scale that shrinks as the value grows; the
-- one rounding stays Go's, at the end. 333.33 × 1.5 h × 150 % is 749.9925 and
-- is published 749.99, where multiplying the rate first (499.995 → 500.00)
-- would bill 750.00.

-- name: ProjectActualGroups :many
SELECT project_id,
       billing_line_id,
       CASE
           WHEN status = 'approved' THEN 'approved'
           WHEN status = 'invoiced' THEN 'invoiced'
           WHEN status = 'submitted' THEN 'submitted'
           ELSE 'draft'
       END AS bucket,
       bill_currency,
       cost_currency,
       SUM(hours * 100)::bigint AS hours_hundredths,
       COALESCE(SUM(hours * 100) FILTER (WHERE billable), 0)::bigint AS billable_hours_hundredths,
       COALESCE(SUM(hours * 100) FILTER (WHERE billable AND bill_rate IS NOT NULL), 0)::bigint AS priced_hours_hundredths,
       COALESCE(SUM(hours * 100) FILTER (WHERE cost_rate IS NOT NULL), 0)::bigint AS costed_hours_hundredths,
       COALESCE(SUM(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01)) FILTER (WHERE billable), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate * (COALESCE(cost_multiplier_percent, 100) * 0.01)), 0)::text AS cost_amount,
       MAX(entry_date)::date AS last_entry_date
FROM time.entries
WHERE project_id = ANY(@project_ids::integer[])
GROUP BY project_id,
         billing_line_id,
         CASE
             WHEN status = 'approved' THEN 'approved'
             WHEN status = 'invoiced' THEN 'invoiced'
             WHEN status = 'submitted' THEN 'submitted'
             ELSE 'draft'
         END,
         bill_currency,
         cost_currency;

-- name: ProjectWorkTypeActualGroups :many
-- ProjectWorkTypeActualGroups is one project's logged work per work type
-- (work types design D4), all buckets together: the hours, and what they
-- bill and cost at the base rate times the multiplier each entry snapshotted,
-- grouped per bill and cost currency so Go folds the amounts on the currency
-- rule the buckets use. Entries without a type are not here — ordinary hours
-- are the absence of a type, not one of them. No name: projects owns the
-- type and names it (work types design D4, as ruled on the plan's review).
-- COALESCE on the id only tells sqlc what the WHERE already guarantees.
SELECT COALESCE(work_type_id, 0)::integer AS work_type_id,
       bill_currency,
       cost_currency,
       SUM(hours * 100)::bigint AS hours_hundredths,
       COALESCE(SUM(hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01)) FILTER (WHERE billable), 0)::text AS bill_amount,
       COALESCE(SUM(hours * cost_rate * (COALESCE(cost_multiplier_percent, 100) * 0.01)), 0)::text AS cost_amount
FROM time.entries
WHERE project_id = @project_id AND work_type_id IS NOT NULL
GROUP BY work_type_id, bill_currency, cost_currency
ORDER BY work_type_id;
