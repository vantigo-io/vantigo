-- +goose Up
-- Who owns the customer relationship, and how customers are classified
-- (owner and tags design D1, D2) — phase 4's first delivery.
--
-- owner_user_id is a SINGLE user of this installation, on the customer row
-- itself, which is the whole point: the owner shares the row's revision, so
-- setting it is an ordinary guarded write and a concurrent edit cannot lose
-- it. NULL is unowned.
--
-- There is deliberately NO foreign key to identity.users. Two reasons, and
-- both are load-bearing rather than stylistic: this module may not read
-- identity's schema at all (internal/db/schema_test.go bars it, depguard bars
-- the import) — contracts.UserDirectory is the only sanctioned seam — and a
-- FK would decide, in the database, what design D1 decides in prose: an owner
-- who is later disabled, or whose account is removed, KEEPS the customer.
-- Nothing is silently revoked; the UI shows the name with an "inactive" hint,
-- or "Unknown user" once the directory no longer knows the id at all (the
-- actorFor precedent, actor.go). ON DELETE SET NULL would have thrown that
-- record away.
ALTER TABLE customers.customers
    ADD COLUMN owner_user_id uuid;

-- The list's ownerId filter is an equality on this column, and "my customers"
-- is the one filter a sales user reaches for every day. Partial, because
-- unowned customers are found by IS NULL and never by an index lookup on a
-- value, so indexing them would be bytes spent on rows this index can never
-- serve.
--
-- The consequence is deliberate and worth stating: ownerId=none is a SCAN. A
-- partial index does not serve its own WHERE's complement, so "unassigned"
-- reads the table the same way an unfiltered list does — which is what it is,
-- a manager's occasional sweep rather than a daily filter, and it already
-- shares that plan with every other page of this list. The day unowned
-- customers are the majority of a large installation and that sweep is what
-- people run all day, the answer is a second partial index on
-- (owner_user_id IS NULL), not widening this one.
CREATE INDEX ix_customers_owner_user ON customers.customers (owner_user_id)
    WHERE owner_user_id IS NOT NULL;

-- The tag vocabulary (design D2), shaped after communications' own tags
-- (00006_communications_baseline.sql) with one deliberate difference: the
-- uniqueness is on lower(name), not on name. A tag is a vocabulary word, so
-- 'VIP' and 'vip' are the same word and a second one is a mistake, not a
-- variant — and an installation that accumulates both has a filter that
-- silently splits its customers in two. The id is a uuid rather than an
-- identity integer for the same reason communications' is: a tag is created
-- from a picker, mid-edit, and the client wants a stable id it can round-trip
-- without a second read.
--
-- color is one of Mantine's named colours or NULL, validated in Go
-- (validateTagColor, values.go) rather than by a CHECK: the set is a UI
-- palette, which changes with the UI and not with the data, and a CHECK would
-- turn a palette change into a migration.
CREATE TABLE customers.tags (
    id    uuid         PRIMARY KEY,
    name  varchar(100) NOT NULL,
    color varchar(20)
);
CREATE UNIQUE INDEX ux_customers_tags_name_lower ON customers.tags (lower(name));

-- Which customers carry which tag. CASCADE both ways is what makes the two
-- destructive operations honest: deleting a tag removes it from every
-- customer (design D2's DELETE /customers/tags/{tagId} is a 204 and nothing
-- else), and archiving is not deletion so a customer's links simply survive.
CREATE TABLE customers.customer_tags (
    customer_id integer NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    tag_id      uuid    NOT NULL REFERENCES customers.tags (id) ON DELETE CASCADE,
    PRIMARY KEY (customer_id, tag_id)
);

-- The primary key already serves "which tags does this customer have"; this
-- index serves the other two directions the module actually asks in: the
-- list's tagId filter (which customers carry this tag) and the tag list's
-- customerCount (design D3's delete confirmation).
CREATE INDEX ix_customer_tags_tag ON customers.customer_tags (tag_id);

-- +goose Down
DROP TABLE customers.customer_tags;
DROP TABLE customers.tags;
ALTER TABLE customers.customers DROP COLUMN owner_user_id;
