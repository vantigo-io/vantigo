-- The economy read's own queries (design §5, delivery B). What a project
-- budgeted and what it billed already have queries of their own — GetProject
-- carries the project's budget, ListBillingLines its lines' budgets and
-- ListProjectMilestones its invoice plan — so this file holds only the figure
-- nothing else asks for. What was actually *logged* is never read here: it
-- lives in another module's schema and reaches Projects through
-- contracts.ProjectActuals alone.

-- name: ProjectTaskEstimateHours :one
-- ProjectTaskEstimateHours is the project's task estimates added up, a
-- secondary planning figure beside the budget (design §2 E3). Every task
-- counts: subtasks as well as top-level ones, and finished ones as well as
-- open ones, because the estimate is what the work was thought to take, not
-- what is left of it. There is no soft delete in this schema — a deleted task
-- is gone, its subtasks with it — so there is nothing to filter out.
--
-- A project none of whose tasks carries an estimate sums to SQL NULL rather
-- than to zero, which is the difference between "nobody estimated anything"
-- and "somebody estimated nothing"; the handler leaves the field out for the
-- first.
SELECT sum(estimate_hours)::numeric AS estimate_hours
FROM projects.tasks
WHERE project_id = @project_id;

-- The portfolio and the dashboard's economy signals (design §5, delivery B)
-- start from one of two predicates, and every query below says which.
--
-- **Financial rights on the project** is the module's existing rule
-- (authorize.go): projects:manage-all, or the manager role on this project,
-- or projects:view-financials on a project the caller can see —
-- projects.visible, the same function the list and every stats query filter
-- on. It is spelled out here rather than in Go because a portfolio has to
-- page and count over it, exactly as the list does with visibility: a
-- predicate applied to rows already fetched gives a total that lies. It
-- appears three times below, once per query that needs it, and the three
-- must be changed together — a caller who may see a project's money in one
-- of them and not in another would see a figure they cannot open the source
-- of.
--
-- **The manager role** is the narrower one: holding the role on this project,
-- and not projects:manage-all. It is what the budget and overdue-milestone
-- alerts are addressed to, because an alert is a request to act and an
-- administrator who can manage every project is not the person who acts on
-- each one — they would receive every project's alerts and read none of them.

-- name: PortfolioProjects :many
-- PortfolioProjects is every project the caller has financial rights on,
-- narrowed by the portfolio's own filters. The over-budget and has-ready
-- filters are not here: they are decided from what another module reports,
-- which no SQL of this module may read.
--
-- row_limit is the cap the handler asks one row past, so "more projects
-- matched than one answer may carry" is one read rather than a count and a
-- read that could disagree. The order is the list's own — by code — so a
-- caller sorting by code gets it straight from the index.
--
-- search is already ILIKE-escaped by the caller and arrives without its
-- wildcards, exactly as ListProjects takes it.
SELECT p.* FROM projects.projects p
WHERE (sqlc.arg(manage_all)::boolean
       OR EXISTS (SELECT 1 FROM projects.project_roles r
                  WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id) AND r.role = 'manager')
       OR (sqlc.arg(view_financials)::boolean
           AND projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)))
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status))
  AND (sqlc.narg(customer_id)::integer IS NULL OR p.customer_id = sqlc.narg(customer_id))
  AND (sqlc.arg(search)::text = '' OR p.code ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\'
                                   OR p.name ILIKE '%' || sqlc.arg(search) || '%' ESCAPE '\')
ORDER BY p.code, p.id
LIMIT sqlc.arg(row_limit);

-- name: ReadyMilestoneCount :one
-- ReadyMilestoneCount is the dashboard card's readyMilestones: how many
-- billing milestones are waiting to be invoiced on the projects whose money
-- the caller may see. A count and not an amount — the projects may be in
-- several currencies, and two currencies never add up. Financial rights,
-- because the existence of something ready to invoice is a financial fact.
--
-- It counts exactly what ReadyMilestonesForCaller below lists, cancelled and
-- completed projects excluded for the same reason: a card saying "4 ready to
-- invoice" over an attention list offering 2 is three numbers for one
-- question, and the one nobody can reach by clicking through is the one that
-- becomes a support ticket.
SELECT count(*) FROM projects.billing_milestones m
JOIN projects.projects p ON p.id = m.project_id
WHERE m.status = 'ready'
  AND p.status NOT IN ('cancelled', 'completed')
  AND (sqlc.arg(manage_all)::boolean
       OR EXISTS (SELECT 1 FROM projects.project_roles r
                  WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id) AND r.role = 'manager')
       OR (sqlc.arg(view_financials)::boolean
           AND projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)));

-- name: OpenMilestonesForProjects :many
-- OpenMilestonesForProjects is the portfolio's one milestone read: every open
-- milestone of a whole page's worth of projects, in one query rather than one
-- per project. Open is 'planned' and 'ready' — an invoiced or cancelled one is
-- neither the next thing to bill nor something ready to bill.
--
-- The order is the portfolio's "next milestone" rule, so the handler takes the
-- first row of each project's group rather than sorting again: earliest
-- planned date first, milestones nobody has dated after every dated one, and
-- then the plan's own manual order. The partial index over the two open
-- statuses (migration 00011) is what serves it.
SELECT * FROM projects.billing_milestones
WHERE project_id = ANY(@project_ids::integer[])
  AND status IN ('planned', 'ready')
ORDER BY project_id, (planned_date IS NULL), planned_date, position, id;

-- name: ReadyMilestonesForCaller :many
-- ReadyMilestonesForCaller is the dashboard's milestoneReady items: every
-- milestone waiting to be invoiced on a project the caller has financial
-- rights on. Unlike the two budget alerts this one is not addressed to
-- managers alone — whoever may see the money is who invoices — and unlike
-- them it is not limited to active projects: a milestone on a project that
-- has not started, or is on hold, is still money waiting to be billed. A
-- cancelled project raises nothing, and neither does a completed one: its
-- invoicing is finished, and anything still ready there is a bookkeeping
-- question rather than something to act on today.
--
-- occurred_at is when the milestone was marked ready, which is the day the
-- dashboard prints as "3 days ago". A row without that stamp — the API sets
-- it on every move to 'ready', so only hand-written data lacks it — falls
-- back to when it was last touched rather than dropping out of the list.
SELECT m.id, m.project_id, m.name,
       coalesce(m.ready_at, m.updated_at)::timestamptz AS occurred_at
FROM projects.billing_milestones m
JOIN projects.projects p ON p.id = m.project_id
WHERE m.status = 'ready'
  AND p.status NOT IN ('cancelled', 'completed')
  AND (sqlc.arg(manage_all)::boolean
       OR EXISTS (SELECT 1 FROM projects.project_roles r
                  WHERE r.project_id = p.id AND r.user_id = sqlc.arg(user_id) AND r.role = 'manager')
       OR (sqlc.arg(view_financials)::boolean
           AND projects.visible(p.id, sqlc.arg(user_id), sqlc.arg(see_all)::boolean)))
ORDER BY occurred_at, m.id;

-- name: OverdueMilestonesForManager :many
-- OverdueMilestonesForManager is the dashboard's milestoneOverdue items: a
-- milestone still planned whose day has passed, on an active project the
-- caller manages. 'ready' is deliberately not here — a ready milestone is
-- already somebody's milestoneReady item, and telling them twice about the
-- same milestone is noise. The date is a plain calendar date in UTC, as
-- everywhere else in this module.
SELECT m.id, m.project_id, m.name, m.planned_date
FROM projects.billing_milestones m
JOIN projects.projects p ON p.id = m.project_id
WHERE m.status = 'planned'
  AND m.planned_date IS NOT NULL
  AND m.planned_date < @today::date
  AND p.status = 'active'
  AND EXISTS (SELECT 1 FROM projects.project_roles r
              WHERE r.project_id = p.id AND r.user_id = @user_id AND r.role = 'manager')
ORDER BY m.planned_date, m.id;

-- name: ManagedActiveProjects :many
-- ManagedActiveProjects is the projects the two budget alerts are computed
-- over: the active ones the caller holds the manager role on. Their budgets
-- come from the row; what has been logged against them comes from the actuals
-- contract, in one call for the whole list, which is why row_limit exists at
-- all — it is that call's batch cap. The caller asks for one row past it and
-- says in the log when the extra row comes back, because unlike the portfolio
-- it keeps the capped set rather than refusing: an attention list totals
-- nothing and has no filter to narrow.
SELECT p.* FROM projects.projects p
WHERE p.status = 'active'
  AND EXISTS (SELECT 1 FROM projects.project_roles r
              WHERE r.project_id = p.id AND r.user_id = @user_id AND r.role = 'manager')
ORDER BY p.code, p.id
LIMIT sqlc.arg(row_limit);
