-- What a project's expenses cost and bill, as contracts.ProjectExpenses
-- reports it. One grouped read serves both the contract's batch call and the
-- module's own per-project summary: the folding into the contract's shape
-- happens in Go over these groups.
--
-- The grain is project, currency and the unit's status bucket. The currency
-- is on the line and nothing else — a claim's line may be in a currency
-- neither its claim nor its project is — so the answer is per currency and
-- the caller decides which of them is the project's. Two currencies are never
-- added: the sum would be a number in neither.
--
-- The bucket is the **unit's** status, which is why the claims join is here:
-- a claim's line takes its claim's status (COALESCE(c.status, e.status), the
-- form every other read of this module uses), because a trip is what somebody
-- approves while a line's own status column stays at the default nobody
-- reads. Approved carries the invoiced lines too — invoicing is a stamp here,
-- not a status — and draft carries the rejected ones, which are back with
-- their owner to fix, exactly as time's ProjectActualGroups buckets a
-- rejected entry.
--
-- The line is attributed to its **own** project_id. A claim's line always
-- carries its claim's project (the module's own invariant, kept by every door
-- that writes one), so the two never disagree; reading the line's column
-- keeps this query honest about what it is grouping and needs no second
-- COALESCE to say it.
--
-- Cost is the net — the gross less the VAT, which is NULL on mileage and per
-- diem and so contributes nothing — whoever paid: an outlay the company paid
-- costs the project exactly what one the employee is reimbursed for does.
-- Bill is the stored bill_amount of the billable lines, and a billable line
-- with no bill amount is counted as unpriced rather than summed as zero: a
-- missing price is not a price of nothing.
--
-- The amounts are the unrounded numeric sums as text — rounding every group
-- and adding those is a different number from rounding the sum once, and only
-- the second is the one an invoice would show — so the rounding is Go's,
-- after the groups have been added up in exact decimals.

-- name: ProjectExpenseGroups :many
SELECT e.project_id::integer AS project_id,
       e.currency,
       CASE COALESCE(c.status, e.status)
           WHEN 'approved' THEN 'approved'
           WHEN 'submitted' THEN 'submitted'
           ELSE 'draft'
       END AS bucket,
       count(*) AS line_count,
       COALESCE(SUM(e.gross_amount - COALESCE(e.vat_amount, 0)), 0)::text AS cost_amount,
       COALESCE(SUM(e.bill_amount) FILTER (WHERE e.billable), 0)::text AS bill_amount,
       count(*) FILTER (WHERE e.billable AND e.bill_amount IS NULL) AS unpriced_count,
       count(*) FILTER (WHERE COALESCE(c.status, e.status) = 'approved'
                          AND e.billable
                          AND e.bill_amount IS NOT NULL
                          AND e.invoiced_at IS NULL) AS ready_count,
       COALESCE(SUM(e.bill_amount) FILTER (WHERE COALESCE(c.status, e.status) = 'approved'
                                             AND e.billable
                                             AND e.bill_amount IS NOT NULL
                                             AND e.invoiced_at IS NULL), 0)::text AS ready_amount,
       count(*) FILTER (WHERE e.invoiced_at IS NOT NULL) AS invoiced_count,
       COALESCE(SUM(e.bill_amount) FILTER (WHERE e.invoiced_at IS NOT NULL), 0)::text AS invoiced_amount,
       MAX(e.entry_date)::date AS last_entry_date
FROM expenses.entries e
LEFT JOIN expenses.claims c ON c.id = e.claim_id
WHERE e.project_id = ANY(@project_ids::integer[])
GROUP BY e.project_id,
         e.currency,
         CASE COALESCE(c.status, e.status)
             WHEN 'approved' THEN 'approved'
             WHEN 'submitted' THEN 'submitted'
             ELSE 'draft'
         END;
