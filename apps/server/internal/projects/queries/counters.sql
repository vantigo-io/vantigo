-- name: NextCounterValue :one
-- NextCounterValue allocates the next value of a named counter, the same
-- gapless-per-counter upsert customers uses: the first call for a counter
-- allocates 1000 — project codes count from KVEM1000, not KVEM1 — and every
-- later call allocates one more, so two concurrent creates can never
-- allocate the same value, even if one of them later rolls back.
--
-- next_value always holds the next number nobody has taken yet: the first
-- call returns 1000 and leaves 1001 behind, the second returns 1001 and
-- leaves 1002. That is what lets the code suggestion read next_value
-- directly, without allocating, and treat an absent row as 1000; only a
-- create advances it, whatever code that create actually used.
INSERT INTO projects.counters (counter_name, next_value)
VALUES (@counter_name, 1001)
ON CONFLICT (counter_name) DO UPDATE SET next_value = projects.counters.next_value + 1
RETURNING (next_value - 1)::bigint AS allocated;
