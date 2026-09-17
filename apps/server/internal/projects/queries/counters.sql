-- name: NextCounterValue :one
-- NextCounterValue allocates the next value of a named counter, the same
-- gapless-per-counter upsert customers uses: the first call for a counter
-- starts it at 1000 — project codes count from KVEM1000, not KVEM1 — and
-- every later call increments it by one under the same upsert, so two
-- concurrent creates can never allocate the same value, even if one of them
-- later rolls back. The code suggestion reads next_value without allocating;
-- only a create advances it, whatever code that create actually used.
INSERT INTO projects.counters (counter_name, next_value)
VALUES (@counter_name, 1000)
ON CONFLICT (counter_name) DO UPDATE SET next_value = projects.counters.next_value + 1
RETURNING next_value;
