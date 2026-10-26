-- name: AdvisoryXactLock :exec
-- AdvisoryXactLock takes one of the module's named transaction advisory
-- locks (locks.go): a person's, or a project's. It is held until the
-- transaction ends.
SELECT pg_advisory_xact_lock(hashtext(@name::text));
