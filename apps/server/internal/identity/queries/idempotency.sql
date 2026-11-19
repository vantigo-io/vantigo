-- name: LockIdempotencyPrincipal :exec
-- LockIdempotencyPrincipal serialises one principal's claims for the rest of
-- the transaction, so the quota and the claim are one decision (I21).
SELECT pg_advisory_xact_lock(hashtextextended('identity/idempotency:' || sqlc.arg(principal)::text, 0));

-- name: IdempotencyUsage :one
-- IdempotencyUsage is how many live keys the principal holds, across every way
-- in, and how many bytes their answers take, a claim still running reserving
-- the largest answer it could store: the quota at the claim (I21).
SELECT count(*)::int AS keys,
       coalesce(sum(CASE WHEN state = 'in_progress' THEN 262144 ELSE coalesce(octet_length(answer), 0) END), 0)::bigint AS bytes
FROM identity.idempotency_keys
WHERE principal_id = @principal_id AND expires_at > @now;

-- name: ClaimIdempotencyKey :one
-- ClaimIdempotencyKey is the claim (I27): it inserts the key in_progress under
-- the claimer's lease, or takes over an expired row, and answers the lease.
-- No row answered means the key is live.
INSERT INTO identity.idempotency_keys (
    principal_id, via, operation_id, key, fingerprint, state, lease_id, lease_until, customer_id, created_at, expires_at
) VALUES (
    @principal_id, @via, @operation_id, @key, @fingerprint, 'in_progress', @lease_id, @lease_until, sqlc.narg(customer_id), @now, @expires_at
)
ON CONFLICT (principal_id, via, operation_id, key) DO UPDATE SET
    fingerprint = EXCLUDED.fingerprint,
    state       = 'in_progress',
    lease_id    = EXCLUDED.lease_id,
    lease_until = EXCLUDED.lease_until,
    answer      = NULL,
    customer_id = EXCLUDED.customer_id,
    created_at  = EXCLUDED.created_at,
    expires_at  = EXCLUDED.expires_at
WHERE identity.idempotency_keys.expires_at <= @now
RETURNING lease_id;

-- name: GetIdempotencyKey :one
-- GetIdempotencyKey reads a live key (I5.2), and whether it was claimed before
-- the last customer anonymisation (I20).
SELECT k.fingerprint, k.state, k.lease_until, k.answer,
       (k.created_at < coalesce((SELECT e.erased_before FROM identity.idempotency_erasure e), '-infinity'::timestamptz))::boolean AS erased
FROM identity.idempotency_keys k
WHERE k.principal_id = @principal_id AND k.via = @via AND k.operation_id = @operation_id AND k.key = @key
  AND k.expires_at > @now;

-- name: CompleteIdempotencyKey :execrows
-- CompleteIdempotencyKey stores the sealed answer, while the claimer's lease
-- is the row's (I26).
UPDATE identity.idempotency_keys
SET state = 'done', answer = @answer
WHERE principal_id = @principal_id AND via = @via AND operation_id = @operation_id AND key = @key
  AND lease_id = @lease_id;

-- name: MarkIdempotencyKeyUnknown :execrows
-- MarkIdempotencyKeyUnknown records that the outcome cannot be told, keeping
-- no answer (I23).
UPDATE identity.idempotency_keys
SET state = 'unknown', answer = NULL
WHERE principal_id = @principal_id AND via = @via AND operation_id = @operation_id AND key = @key
  AND lease_id = @lease_id;

-- name: ReleaseIdempotencyKey :execrows
-- ReleaseIdempotencyKey deletes the claim of a request whose refusal is not
-- kept, so an identical retry runs again (I23).
DELETE FROM identity.idempotency_keys
WHERE principal_id = @principal_id AND via = @via AND operation_id = @operation_id AND key = @key
  AND lease_id = @lease_id;

-- name: DeleteExpiredIdempotencyKeys :execrows
-- DeleteExpiredIdempotencyKeys deletes a batch of expired keys (I21).
DELETE FROM identity.idempotency_keys
WHERE ctid IN (
    SELECT k.ctid FROM identity.idempotency_keys k
    WHERE k.expires_at <= @now
    LIMIT @batch_size
);

-- name: DeleteUserIdempotencyKeys :execrows
-- DeleteUserIdempotencyKeys deletes every key of a person being erased or
-- deleted (I20).
DELETE FROM identity.idempotency_keys WHERE principal_id = @principal_id;

-- name: DeleteCustomerIdempotencyKeys :execrows
-- DeleteCustomerIdempotencyKeys deletes the keys of agent runs scoped to a
-- customer being anonymised (I20).
DELETE FROM identity.idempotency_keys WHERE customer_id = @customer_id;

-- name: RaiseIdempotencyErasure :exec
-- RaiseIdempotencyErasure moves the erasure watermark to at, never back (I20).
INSERT INTO identity.idempotency_erasure (singleton, erased_before) VALUES (true, @at)
ON CONFLICT (singleton) DO UPDATE SET erased_before = greatest(identity.idempotency_erasure.erased_before, EXCLUDED.erased_before);
