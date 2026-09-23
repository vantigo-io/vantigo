-- +goose Up
-- The free text was always a title, and the roles that answer "who gets the
-- invoice" are their own thing (typed contact roles design D1, D2) — phase 4's
-- second delivery.
--
-- The rename, not a new column beside the old one: every value already in this
-- column is a job title (the recorded corpus has CEO and CTO and nothing that
-- reads as a role), so there is nothing to migrate and nothing to interpret —
-- the data is correct under its new name. A new column would have meant
-- writing both for a release, a backfill, and a day where two columns disagree.
--
-- DROP NOT NULL, because a title is now genuinely optional: an association
-- whose contact is the billing contact and nothing else says what it needs to
-- say through customer_contact_roles below, and the alternative — the empty
-- string standing in for "none given" — is the thing this codebase spells NULL
-- everywhere else (00017's contact-info columns, 00019's billing profile).
-- The contract keeps answering `role` (the title, or "" when there is none),
-- so the frozen corpus stays true; see openapi/customers.yaml.
ALTER TABLE customers.customers_contacts RENAME COLUMN role TO title;
ALTER TABLE customers.customers_contacts ALTER COLUMN title DROP NOT NULL;

-- The typed roles. varchar(30) because the vocabulary is code-defined
-- (billing, project, decision_maker) and validated in Go, not by a CHECK: a
-- CHECK would turn the design's "a wider list is a value change for later"
-- into a migration, exactly as 00024 declined a CHECK on a tag's colour for
-- the same reason. Thirty characters leaves room for the executive-sponsor
-- kind of word the design parks without leaving room for prose.
--
-- The primary key is (customer_id, contact_id, role): a contact holds a role
-- for a customer at most once, and the pair leading the key is also the
-- "which roles does this association carry" lookup every read here makes.
--
-- The foreign key is the COMPOSITE one, to customers_contacts' own primary
-- key, rather than two separate keys to customers and contacts. That is what
-- makes a role row impossible without the association it describes, and it is
-- what makes detaching a contact — or deleting it, whose cascade removes the
-- association — take its roles with it in one statement. The handlers still
-- run the promotion the design requires before/after that cascade; the
-- cascade only guarantees no orphan survives it.
CREATE TABLE customers.customer_contact_roles (
    customer_id integer     NOT NULL,
    contact_id  integer     NOT NULL,
    role        varchar(30) NOT NULL,
    is_primary  boolean     NOT NULL,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (customer_id, contact_id, role),
    FOREIGN KEY (customer_id, contact_id)
        REFERENCES customers.customers_contacts (customer_id, contact_id) ON DELETE CASCADE
);

-- One primary per customer and role (design D2) — the invariant the handlers
-- keep under the customer row's lock, and the database's own last word should
-- they ever not. This is 00018's ux_customer_addresses_primary with (type)
-- swapped for (role) and deliberately nothing else changed: the same shape,
-- so the same reasoning about demote-before-promote applies unaltered.
CREATE UNIQUE INDEX ux_customer_contact_roles_primary
    ON customers.customer_contact_roles (customer_id, role) WHERE is_primary;

-- The primary key serves every customer-side read; this serves the other
-- direction the contact page asks in — "which roles does this contact hold,
-- at each of its customers" — which the key's leading customer_id cannot.
CREATE INDEX ix_customer_contact_roles_contact ON customers.customer_contact_roles (contact_id);

-- +goose Down
DROP TABLE customers.customer_contact_roles;
-- A title written as NULL under the new rule cannot go back under the old
-- NOT NULL, so rolling back spells it the way the old column had to: the
-- empty string. Nothing is lost that the old schema could have held.
UPDATE customers.customers_contacts SET title = '' WHERE title IS NULL;
ALTER TABLE customers.customers_contacts ALTER COLUMN title SET NOT NULL;
ALTER TABLE customers.customers_contacts RENAME COLUMN title TO role;
