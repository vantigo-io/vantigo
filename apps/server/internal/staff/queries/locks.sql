-- name: LockPerson :one
-- LockPerson takes one person's row for the rest of the transaction, the first
-- lock every mutation of a person's rows takes (design §4, Locking). It is FOR
-- NO KEY UPDATE rather than FOR UPDATE: it serialises every writer of the
-- person exactly as FOR UPDATE would, but leaves alone the KEY SHARE lock a
-- foreign-key check takes on the row — another person's terms naming this one
-- as manager, a department naming them as its head — so such a write never
-- waits on this person's lock while holding locks of its own. No row is an
-- unknown person. erased_at is read with the lock for the read-only guard
-- (erased.go): a person the retention run erased refuses every write but its
-- exceptions, judged on the row the lock holds.
SELECT id, erased_at FROM staff.people WHERE id = @id FOR NO KEY UPDATE;

-- name: AdvisoryXactLock :exec
-- AdvisoryXactLock takes one of the module's named transaction advisory locks
-- (locks.go): the manager graph, or one structure table's default. It is held
-- until the transaction ends.
SELECT pg_advisory_xact_lock(hashtext(@name::text));
