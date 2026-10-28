-- +goose Up
-- The calendar-mailbox-sync worker's claim (calendar Outlook sync design O7):
-- a cycle claims each due person in a short transaction of its own, until
-- claimed_until, and gives its connection back before it calls out, so no
-- connection is held while the provider or the pool is asked. The claim is
-- what keeps two replicas from syncing one person at once; the PUT, the
-- appointment writes' nudge and the change notices move next_sync_at only,
-- and never take a claim away. A sync gives its claim back when it ends; a
-- replica that dies mid-sync leaves one that lapses.
ALTER TABLE calendar.mailbox_sync ADD COLUMN claimed_until timestamptz;

-- +goose Down
ALTER TABLE calendar.mailbox_sync DROP COLUMN claimed_until;
