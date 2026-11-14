-- +goose Up
-- Invoices: the bank feed's read cursor and the bank's wait (invoices bank
-- feed plan, delivery C's review). read_through is the last day an account
-- was read successfully to the end of a window, so the next read starts ten
-- days before it however few payments the account receives; NULL until the
-- first window is read, and again whenever switching the feed on sets a start
-- day. rate_limited_until is when the bank, answering 429, said it may be
-- asked again: Fetch now never makes the account due before it. Both are
-- written by the sync's worker alone, the time a parameter, never now().
ALTER TABLE invoices.bank_feed_accounts
    ADD COLUMN read_through       date,
    ADD COLUMN rate_limited_until timestamptz;

-- +goose Down
ALTER TABLE invoices.bank_feed_accounts
    DROP COLUMN rate_limited_until,
    DROP COLUMN read_through;
