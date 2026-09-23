-- +goose Up
-- Customer groups that carry defaults (customer groups design D1, D2) — phase
-- 4's fourth delivery, and the first thing in this module that decides a value
-- for a customer without storing it on the customer.
--
-- The vocabulary is the tag vocabulary's shape (00024), deliberately, down to
-- the unique index on lower(name): a group is a vocabulary word, so 'Retail'
-- and 'retail' are the same word and a second one is a mistake rather than a
-- variant — an installation holding both has a filter, and a default payment
-- term, that silently split its customers in two. Names are NFC-normalised in
-- Go (validateGroupName, values.go) before they are stored or compared, for
-- the same reason the comparison ignores case.
--
-- What it does NOT copy from tags: no colour and no description. A group is a
-- policy object — its name and its default are installation policy — not a
-- label, and nothing on it is decoration. created_at/updated_at are here and
-- not on customers.tags because this row IS edited over time in a way that
-- matters to every member. They say when the group was created and when it was
-- last edited, and no more: a rename moves updated_at as much as a new default
-- does, only the latest edit is kept, and the old value is gone. The module
-- keeps no history of a group's default anywhere, since the timeline it would
-- belong on is deliberately not written for vocabulary edits (design D2).
--
-- default_payment_terms_days is NULL for "this group decides nothing", and
-- CHECKed 0-365 — the billing profile's own rule (validatePaymentTermsDays,
-- billing_values.go), in the database as well as in Go because a value outside
-- it would be inherited by every member of the group. The range is a business
-- rule that has never changed and is not a UI palette (which is why tags'
-- colour has no CHECK), so a CHECK costs nothing a future migration will
-- regret.
CREATE TABLE customers.customer_groups (
    id                         uuid         PRIMARY KEY,
    name                       varchar(100) NOT NULL,
    default_payment_terms_days integer      CHECK (default_payment_terms_days BETWEEN 0 AND 365),
    created_at                 timestamptz  NOT NULL,
    updated_at                 timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_customers_groups_name_lower ON customers.customer_groups (lower(name));

-- The membership is the OWNER's shape, not the tags' (design D1): one nullable
-- column on the customer row, so it shares the row's revision and setting it
-- is an ordinary guarded write that a concurrent edit cannot lose. NULL is "in
-- no group", which is every customer until somebody says otherwise.
--
-- Unlike owner_user_id this column DOES have a foreign key, and the difference
-- is not inconsistency: owner_user_id points across a module boundary at
-- identity's users, which this module may not reference at all
-- (internal/db/schema_test.go bars the schema, depguard bars the import), while
-- customer_groups is this module's own table two lines up. ON DELETE RESTRICT
-- because design D2 rules that a group with members is not deleted: the
-- handler counts members and answers 409 group_in_use, and this is what makes
-- that true for a writer that raced the count. ON DELETE SET NULL would have
-- changed every member's effective payment term with no record on any customer
-- — the tags' cascade is right for a label and wrong for a default.
ALTER TABLE customers.customers
    ADD COLUMN group_id uuid REFERENCES customers.customer_groups (id) ON DELETE RESTRICT;

-- The list's groupId filter is an equality on this column, and the group
-- vocabulary list's customerCount is a count over it. Partial, and the
-- consequence is the owner column's own bet restated (00024): groupId=none is a
-- SCAN, because a partial index does not serve its own WHERE's complement.
-- "Customers in no group" is a tidying-up sweep, not a daily filter, and it
-- already shares that plan with every unfiltered page of this list. The day
-- ungrouped customers are the majority of a large installation and that sweep
-- is what people run all day, the answer is a second partial index on
-- (group_id IS NULL), not widening this one.
CREATE INDEX ix_customers_group ON customers.customers (group_id)
    WHERE group_id IS NOT NULL;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN group_id;
DROP TABLE customers.customer_groups;
