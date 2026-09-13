-- Task 13 (the attachment-cleanup worker, SV/AttachmentCleanupService.cs:11-85;
-- communications inventory §12.2, §12.3). Every query here belongs to
-- cleanup.go and to nothing else: the worker is the only reader or writer of
-- attachment_cleanup_records after the reservation protocol in objects.go /
-- attachments.sql has put a row there.
--
-- The batch size is written into the SQL rather than passed in, and that is
-- deliberate: .NET's `Take(100)` is a literal (`:48`) and this is the one
-- worker in the module with NOTHING configurable — no poll key, no batch key
-- (inventory §12.2, "also not configurable"). A parameter here would be an
-- invitation to add the environment variable that does not exist.

-- name: SelectClaimableCleanupRecordIDs :many
-- The candidate select (`:44-48`, inventory §12.2), ordered by created_at and
-- capped at .NET's literal 100. Ids only, exactly as .NET selects only
-- `item.Id` — the row itself is re-read after the claim wins, not before.
--
-- The three disjuncts are three different situations, and only the first is
-- the ordinary one:
--
--  1. pending  — an object retention or a released reservation handed over for
--     deletion, due now.
--  2. staged   — THE CRASH-RECOVERY PATH, and the entire reason the
--     reservation protocol exists (inventory §5.4 step 10, §12.3): a
--     reservation written before an object-store write whose owner never
--     committed the metadata that would have marked it 'owned'. Its
--     10-minute window has passed, so the object is abandoned and is
--     reclaimed here. Nothing else in the module ever reclaims it.
--  3. deleting — a cleanup lease that expired without an outcome, i.e. the
--     worker holding it died mid-delete.
--
-- 'owned' and 'completed' match no branch, which is what keeps a live
-- attachment's object and an already-deleted one out of every batch
-- (TS/Integration/ObjectLifecycleTests.cs:98-119).
SELECT id
FROM communications.attachment_cleanup_records
WHERE (status = 'pending' AND next_attempt_at <= @now)
   OR (status = 'staged' AND reservation_expires_at <= @now)
   OR (status = 'deleting' AND lease_until <= @now)
ORDER BY created_at
LIMIT 100;

-- name: ClaimCleanupRecord :execrows
-- The claim (`:53-58`): one conditional UPDATE re-asserting the same predicate
-- the candidate select used, so a record another worker won in between no
-- longer matches and this update reports 0 rows — "the conditional update is
-- the lock", the same idiom as the outbox (inventory §13.2).
--
-- There is NO advisory lock here and no SELECT ... FOR UPDATE. This worker is
-- deliberately unguarded by the installation-wide advisory lease that
-- retention takes: every replica runs every cycle of it (inventory §12.2,
-- §14), and the exclusion this UPDATE provides is the only exclusion there is.
--
-- attempts is NOT incremented here — unlike the outbox's claim, which does
-- increment. Cleanup counts an attempt only when a delete actually fails
-- (`:75`), which is what makes the first failure's backoff 2 s.
UPDATE communications.attachment_cleanup_records
SET status = 'deleting',
    lease_id = @lease_id,
    lease_until = @lease_until
WHERE id = @id
  AND ((status = 'pending' AND next_attempt_at <= @now)
    OR (status = 'staged' AND reservation_expires_at <= @now)
    OR (status = 'deleting' AND lease_until <= @now));

-- name: GetCleanupRecord :one
-- The re-read after a won claim (`:60`, `SingleAsync`): the worker takes the
-- row's storage_key and attempts from the database rather than from the
-- candidate list, so the values it acts on are the ones its own claim wrote
-- against.
SELECT id, message_id, storage_key, status, attempts, next_attempt_at, lease_id,
       lease_until, reservation_expires_at, last_error, created_at
FROM communications.attachment_cleanup_records
WHERE id = @id;

-- name: CompleteCleanupRecord :exec
-- The success outcome (`:68-70`): the object is gone, the record is
-- 'completed', and the lease is cleared. The row itself is kept forever —
-- queueObjectForDeletion reads it back (objects.sql's
-- CleanupRecordCompletedExists) to know a key has already been deleted and
-- must not be queued a second time.
--
-- Deliberately NOT conditional on the lease still being ours. .NET writes this
-- through the change tracker by primary key (`:82`'s single SaveChanges), with
-- no lease re-check anywhere — unlike the outbox, whose completion re-reads
-- and compares the lease first (inventory §13.3 step 10). Adding one here
-- would change which outcomes get recorded.
UPDATE communications.attachment_cleanup_records
SET status = 'completed',
    lease_id = NULL,
    lease_until = NULL
WHERE id = @id;

-- name: FailCleanupRecord :exec
-- The failure outcome (`:74-79`): straight back to 'pending' with one more
-- attempt, the backoff's next_attempt_at, the lease cleared, and last_error
-- set to a CONSTANT — the exception's own text is never stored (see
-- cleanup.go's cleanupFailureMessage).
--
-- **There is no terminal state.** The outbox has `max_attempts = 8` and gives
-- up; cleanup never does. A key that can never be deleted comes back here
-- forever, at the backoff's 1024 s ceiling (inventory §12.2: "a permanently
-- failing key retries forever"). There is no 'failed' status for this table
-- and no attempt ceiling to add.
--
-- attempts is passed in rather than written as `attempts + 1` so the stored
-- value and the backoff that was computed from it cannot disagree.
UPDATE communications.attachment_cleanup_records
SET status = 'pending',
    attempts = @attempts,
    last_error = @last_error,
    next_attempt_at = @next_attempt_at,
    lease_id = NULL,
    lease_until = NULL
WHERE id = @id;
