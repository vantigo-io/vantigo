-- name: CalendarFollowUps :many
-- CalendarFollowUps is the customers.follow-up half of this module's calendar
-- source (calendar design D1): the manual, active follow-ups assigned to the
-- caller that fall due in the window, done ones included — the calendar strikes
-- them. A customer merged away or anonymised is no customer to follow up, so
-- its entries are left out, the way the follow-ups list leaves out what it
-- cannot link to. Every row is the caller's by the assignee predicate.
SELECT e.id, e.customer_id, c.name AS customer_name, e.event_type, e.summary,
       e.follow_up_on, e.follow_up_done_at
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_assignee_user_id = @user_id::uuid
  AND e.follow_up_on BETWEEN @from_day::date AND @to_day::date
  AND c.merged_into_customer_id IS NULL AND c.anonymised_at IS NULL
ORDER BY e.follow_up_on, e.id;

-- name: CalendarInteractions :many
-- CalendarInteractions is the customers.interaction half (calendar design
-- D1): the caller's own manual, active entries of the three interaction kinds
-- that occurred in the window — a call, a meeting, an email the caller wrote
-- down. occurred_at, when set, makes the item a point in time; the window is
-- judged on occurred_on, the day the entry says it happened on. The customer
-- predicate is CalendarFollowUps's.
SELECT e.id, e.customer_id, c.name AS customer_name, e.event_type, e.summary,
       e.occurred_on, e.occurred_at
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.actor_user_id = @user_id::uuid
  AND e.event_type IN ('interaction.call', 'interaction.meeting', 'interaction.email')
  AND e.occurred_on BETWEEN @from_day::date AND @to_day::date
  AND c.merged_into_customer_id IS NULL AND c.anonymised_at IS NULL
ORDER BY e.occurred_on, e.occurred_at NULLS FIRST, e.id;
