-- +goose Up
-- The index behind "ready to invoice", replaced by one the reads it was built
-- for can actually use.
--
-- 00012 shipped ix_entries_to_invoice as
-- (project_id, entry_date) WHERE status = 'approved' AND billable AND
-- invoiced_at IS NULL, and said so itself: "No query reads that predicate yet
-- … the project-side reads that page through it arrive next." They have
-- arrived — GET /entries?projectId=&toInvoice=true, and readyCount on
-- GET /projects/{projectId}/summary — and they cannot use it, for a reason
-- that is not a tuning detail but the module's own model:
--
--   * the predicate names `status`, which on a **travel claim's line** is the
--     default 'draft' nobody reads. Every read of this module judges a line by
--     its *unit* — COALESCE(claim.status, entry.status) — so an approved trip's
--     billable line is ready to invoice while its own column says otherwise.
--     An index over a column the rule does not consult cannot serve the rule.
--
-- Measured on a 206 000-row table over 2 000 projects (20 % of the rows lines
-- of 8 000 travel claims), one project holding 6 000 of them, after ANALYZE
-- and three warm-ups, EXPLAIN (ANALYZE, BUFFERS) on the toInvoice list:
--
--   as shipped   the planner does not choose it at all — it reads
--                ix_entries_project_id_entry_date, 6 100 candidate rows,
--                4 866 removed by filter, 226 heap blocks, 2.8 ms
--   dropped      byte for byte the same plan and the same buffers, 3.1 ms
--   replaced     1 357 candidate rows, 123 removed by filter, 149 heap
--                blocks, 1.5 ms
--
-- So the shipped index is dead weight — an index PostgreSQL maintains on every
-- insert, update and delete of an expense and never reads — while the same two
-- columns under the predicate the reads actually carry (billable, not yet
-- invoiced, whatever the status) cut the rows the heap has to visit by 4.5x.
-- It keeps the name: same purpose, correct predicate.
--
-- Dropping `status` from the predicate is what makes it usable rather than a
-- widening for its own sake: the status is judged through the claims join,
-- which no index on this table can carry, so it stays a filter either way.
--
-- What it costs to maintain: more tuples, fewer writes. The new predicate
-- admits every billable un-invoiced row in any status, roughly three times
-- what 00012's approved-only one held — but it also removes the predicate
-- *churn*. Under 00012 every approval and every unapproval moved a row into or
-- out of the index, which is a guaranteed non-HOT update; under this one only
-- a change to `billable` or `invoiced_at` does, and those happen once in a
-- line's life rather than on the commonest transition it makes.
DROP INDEX expenses.ix_entries_to_invoice;
CREATE INDEX ix_entries_to_invoice ON expenses.entries (project_id, entry_date)
    WHERE billable AND invoiced_at IS NULL;

-- +goose Down
-- Back to 00012's predicate exactly, so a rollback leaves the schema a 00012
-- installation would have.
DROP INDEX expenses.ix_entries_to_invoice;
CREATE INDEX ix_entries_to_invoice ON expenses.entries (project_id, entry_date)
    WHERE status = 'approved' AND billable AND invoiced_at IS NULL;
