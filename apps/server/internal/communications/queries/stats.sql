-- Stats (Task 9, EP/CommunicationsStatsEndpoints.cs): getCommunicationsStatsSummary,
-- getCommunicationsStatsTimeseries, getCommunicationsStatsAttention. Mirrors
-- the customers/products modules' own stats query shape (SummaryCounts split
-- by table, one CreationBuckets-style query per timeseries metric) — the
-- completed broader template — with attention's own real query added on top
-- (unlike those two modules, whose /stats/attention is a parity stub).

-- name: CommunicationsConversationStatsSummaryCounts :one
-- CommunicationsStatsEndpoints.Summary's conversation-table counts (:41-47,
-- :52-57): open is the current TOTAL open count, not windowed — see
-- open_before_period's own comment on the Go side for why
-- open_before_period_delta is not a period delta like the other three.
SELECT
    count(*) FILTER (WHERE status = 'open') AS open,
    count(*) FILTER (WHERE status = 'open' AND created_at < @period_from::timestamptz) AS open_before_period,
    count(*) FILTER (WHERE created_at >= @period_from::timestamptz AND created_at < @period_to::timestamptz) AS new_conversations,
    count(*) FILTER (WHERE created_at >= @previous_from::timestamptz AND created_at < @period_from::timestamptz) AS previous_new_conversations,
    count(*) FILTER (WHERE status = 'closed' AND last_activity_at >= @period_from::timestamptz AND last_activity_at < @period_to::timestamptz) AS closed,
    count(*) FILTER (WHERE status = 'closed' AND last_activity_at >= @previous_from::timestamptz AND last_activity_at < @period_from::timestamptz) AS previous_closed
FROM communications.conversations;

-- name: CommunicationsMessageStatsSummaryCounts :one
-- CommunicationsStatsEndpoints.Summary's message-table counts (:48-51).
SELECT
    count(*) FILTER (WHERE occurred_at >= @period_from::timestamptz AND occurred_at < @period_to::timestamptz) AS messages,
    count(*) FILTER (WHERE occurred_at >= @previous_from::timestamptz AND occurred_at < @period_from::timestamptz) AS previous_messages
FROM communications.conversation_messages;

-- name: CommunicationsConversationCreationBuckets :many
-- The timeseries's newConversations metric (:93-98): one row per UTC
-- calendar day with at least one conversation created in [range_from, range_to).
SELECT (created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM communications.conversations
WHERE created_at >= @range_from::timestamptz AND created_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- name: CommunicationsMessageOccurredBuckets :many
-- The timeseries's messages metric (:104-109): one row per UTC calendar day
-- with at least one message occurring in [range_from, range_to).
SELECT (occurred_at AT TIME ZONE 'UTC')::date AS day, count(*) AS value
FROM communications.conversation_messages
WHERE occurred_at >= @range_from::timestamptz AND occurred_at < @range_to::timestamptz
GROUP BY day
ORDER BY day;

-- name: CommunicationsStatsAttentionItems :many
-- CommunicationsStatsEndpoints.Attention (:115-151): failed deliveries
-- (status IN ('failed','submission_failed'), UNBOUNDED in time) concatenated
-- with open conversations whose last_activity_at is older than @cutoff AND
-- whose newest message (by occurred_at) is inbound -- a condition this
-- outbound-only port's data can never satisfy today (communications
-- inventory §1.6), ported anyway so the second arm activates the moment an
-- inbound provider lands. The inner UNION ALL order does not matter; the
-- outer ORDER BY occurred_at ASC then LIMIT 100 is what the caller must
-- apply, and it is not a mistake to preserve: it is the hundred OLDEST
-- items, "reads like an accident but is what the code does" (inventory
-- §1.6, §19 item 19).
SELECT id, type, title, occurred_at, entity_id
FROM (
    SELECT d.id::text AS id,
           'failedDelivery'::text AS type,
           'Message delivery failed'::text AS title,
           d.created_at AS occurred_at,
           d.message_id::text AS entity_id
    FROM communications.message_deliveries d
    WHERE d.status IN ('failed', 'submission_failed')

    UNION ALL

    SELECT c.id::text AS id,
           'conversationNoReply'::text AS type,
           COALESCE(c.subject, 'Conversation awaiting reply') AS title,
           c.last_activity_at AS occurred_at,
           c.id::text AS entity_id
    FROM communications.conversations c
    WHERE c.status = 'open'
      AND c.last_activity_at < @cutoff::timestamptz
      AND (
          SELECT m.direction
          FROM communications.conversation_messages m
          WHERE m.conversation_id = c.id
          ORDER BY m.occurred_at DESC
          LIMIT 1
      ) = 'inbound'
) attention
ORDER BY occurred_at ASC
LIMIT 100;
