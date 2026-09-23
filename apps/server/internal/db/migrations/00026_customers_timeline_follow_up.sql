-- +goose Up
-- What happens next (follow-ups design D1) — phase 4's third delivery. A
-- manual timeline entry can carry a follow-up: a due date, optionally an
-- assignee, and a "done" stamp.
--
-- Three columns ON THE ENTRY, not a table of their own. A follow-up is never
-- shared, never plural and never outlives its entry: "call back on Friday"
-- belongs to the note that says why. On the entry it also shares the entry's
-- own revision, which is what lets the existing PUT set, replace and clear it
-- under the expectedRevision the caller already sends — a side table would
-- have needed a second concurrency story for one date.
--
-- follow_up_on is a date, not a timestamptz: a follow-up is due on a day, and
-- the design's overdue/due-today split is a UTC calendar comparison, not an
-- instant one. NULL means the entry carries no follow-up at all, which is the
-- overwhelming majority of entries and the reason both indexes below are
-- partial.
--
-- follow_up_assignee_user_id has deliberately NO foreign key to
-- identity.users, for migration 00024's own two reasons: this module may not
-- read identity's schema at all (internal/db/schema_test.go bars it, depguard
-- bars the import) — contracts.UserDirectory is the only sanctioned seam — and
-- design D1 rules that an assignee disabled or removed afterwards KEEPS the
-- follow-up, shown inactive. ON DELETE SET NULL would have quietly turned
-- somebody's task into nobody's. NULL means unassigned, which design D2 reads
-- as everyone's until somebody takes it.
--
-- follow_up_done_at is the stamp, not a boolean: "when" is strictly more than
-- "whether", the Follow-ups page shows it, and every other state change in
-- this module that can be undone is spelled the same way (deleted_at).
-- Clearing a follow-up clears this too — there is no done-ness without a
-- follow-up to be done.
ALTER TABLE customers.customers_timeline_entries
    ADD COLUMN follow_up_on              date,
    ADD COLUMN follow_up_assignee_user_id uuid,
    ADD COLUMN follow_up_done_at          timestamptz;

-- The revisions table mirrors the entry column-for-column (00003's own
-- comment), and that mirroring is not cosmetic here: a revision is a
-- point-in-time snapshot, so a revision that did not carry the follow-up would
-- answer "what did this entry look like then" with today's due date and
-- today's assignee. The design says history stays point-in-time; these three
-- columns are what makes that true.
ALTER TABLE customers.customers_timeline_entries_revisions
    ADD COLUMN follow_up_on              date,
    ADD COLUMN follow_up_assignee_user_id uuid,
    ADD COLUMN follow_up_done_at          timestamptz;

-- The open-follow-ups index: what /stats/attention asks (every open follow-up
-- due today or earlier) and what the Follow-ups page's default filters ask
-- (state=open or overdue, ordered by due date). Partial on all three
-- predicates, because that is exactly the set both readers want and it is a
-- tiny fraction of the table — an index over every timeline entry ever written
-- would be bytes spent on rows neither reader can ever return. id is the
-- second key column so the design's "dueOn ascending then entry id" ordering
-- is the index's own order and needs no sort.
--
-- state is in the predicate rather than the key for the same reason: a
-- soft-deleted entry's follow-up is not a follow-up any more, so those rows
-- should not be in the index at all. A DELETE of an entry therefore removes it
-- from every follow-up reader without touching a follow-up column, which is
-- why SetTimelineEntryDeleted needs no change.
CREATE INDEX ix_customers_timeline_entries_follow_up_open
    ON customers.customers_timeline_entries (follow_up_on, id)
    WHERE follow_up_on IS NOT NULL AND follow_up_done_at IS NULL AND state = 'active';

-- The assignee index: the Follow-ups page's assignee filter, which the index
-- above cannot serve because it does not carry the column, and which has to
-- work for state=done and state=all too — hence a predicate of only
-- "has a follow-up at all". Unassigned follow-ups are found by IS NULL, and a
-- b-tree index does store NULLs, so this one serves assignee=none as well as
-- assignee=<uuid>.
CREATE INDEX ix_customers_timeline_entries_follow_up_assignee
    ON customers.customers_timeline_entries (follow_up_assignee_user_id, follow_up_on, id)
    WHERE follow_up_on IS NOT NULL;

-- +goose Down
DROP INDEX customers.ix_customers_timeline_entries_follow_up_assignee;
DROP INDEX customers.ix_customers_timeline_entries_follow_up_open;
ALTER TABLE customers.customers_timeline_entries_revisions
    DROP COLUMN follow_up_done_at,
    DROP COLUMN follow_up_assignee_user_id,
    DROP COLUMN follow_up_on;
ALTER TABLE customers.customers_timeline_entries
    DROP COLUMN follow_up_done_at,
    DROP COLUMN follow_up_assignee_user_id,
    DROP COLUMN follow_up_on;
