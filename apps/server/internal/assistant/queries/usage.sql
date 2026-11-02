-- What a person used on a business day (S14, S9): counted as it happens.

-- name: CountConversation :exec
INSERT INTO assistant.usage_days (user_id, day, conversations)
VALUES (@user_id, @day::date, 1)
ON CONFLICT (user_id, day) DO UPDATE SET conversations = assistant.usage_days.conversations + 1;

-- name: CountRun :exec
INSERT INTO assistant.usage_days (user_id, day, runs)
VALUES (@user_id, @day::date, 1)
ON CONFLICT (user_id, day) DO UPDATE SET runs = assistant.usage_days.runs + 1;

-- name: CountTokens :exec
INSERT INTO assistant.usage_days (user_id, day, input_tokens, output_tokens)
VALUES (@user_id, @day::date, @input_tokens::bigint, @output_tokens::bigint)
ON CONFLICT (user_id, day) DO UPDATE SET
    input_tokens = assistant.usage_days.input_tokens + EXCLUDED.input_tokens,
    output_tokens = assistant.usage_days.output_tokens + EXCLUDED.output_tokens;

-- name: CountChange :exec
-- CountChange counts a confirmed change that was made (ok) or that failed.
INSERT INTO assistant.usage_days (user_id, day, confirmed, failed)
VALUES (@user_id, @day::date, CASE WHEN @ok::boolean THEN 1 ELSE 0 END, CASE WHEN @ok::boolean THEN 0 ELSE 1 END)
ON CONFLICT (user_id, day) DO UPDATE SET
    confirmed = assistant.usage_days.confirmed + EXCLUDED.confirmed,
    failed = assistant.usage_days.failed + EXCLUDED.failed;

-- name: TokensOnDay :one
SELECT COALESCE(SUM(input_tokens + output_tokens), 0)::bigint FROM assistant.usage_days
WHERE user_id = @user_id AND day = @day::date;

-- name: ListUsage :many
-- ListUsage is the Owner's count view: per person and day, never content.
SELECT * FROM assistant.usage_days
WHERE day BETWEEN @from_day::date AND @to_day::date
ORDER BY day DESC, user_id;

-- name: DeleteOldUsage :exec
DELETE FROM assistant.usage_days WHERE day < @before::date;
