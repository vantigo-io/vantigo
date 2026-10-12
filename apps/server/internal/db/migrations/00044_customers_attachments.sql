-- +goose Up
-- A customer's attachments (customer attachments design D1, D2): a contract,
-- an NDA, the correspondence behind a timeline entry. The row points at its
-- object in the customers storage scope under attachments/<uuid> — a key that
-- names no customer, since a merge moves the row to the survivor and the
-- store has no rename (D1, D5). timeline_entry_id is NULL for a document of
-- the relationship itself and names a manual entry for a document of a
-- moment; the entry row is never hard-deleted, so SET NULL is a belt for a
-- buckle this module does not have. uploaded_by_user_id is identity's user,
-- opaque here (module boundaries rule 4), nullable like a timeline entry's
-- actor_user_id. Rows are deleted, never marked: a delete or an erase inserts
-- the object's ledger row in the same transaction (00043's staff.documents).
CREATE TABLE customers.customer_attachments (
    id                  integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    customer_id         integer      NOT NULL REFERENCES customers.customers (id),
    timeline_entry_id   integer      REFERENCES customers.customers_timeline_entries (id) ON DELETE SET NULL,
    object_key          varchar(200) NOT NULL,
    file_name           varchar(255) NOT NULL,
    content_type        varchar(100) NOT NULL,
    size_bytes          bigint       NOT NULL,
    uploaded_by_user_id uuid,
    created_at          timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_customer_attachments_object_key ON customers.customer_attachments (object_key);
-- The list is newest first, and the cap counts by customer.
CREATE INDEX ix_customer_attachments_customer ON customers.customer_attachments (customer_id, created_at DESC, id DESC);
CREATE INDEX ix_customer_attachments_entry ON customers.customer_attachments (timeline_entry_id)
    WHERE timeline_entry_id IS NOT NULL;

-- The objects still to be removed from the store (design D6): a delete or an
-- anonymisation removes the row and inserts its object's key here in one
-- transaction, and the ledger finishes the object after the commit, retried
-- until the store confirms, so an erase is never best-effort. Staff's
-- staff.object_deletions (00043), column for column.
CREATE TABLE customers.object_deletions (
    id              integer      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    object_key      varchar(200) NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz  NOT NULL,
    claimed_until   timestamptz,
    last_error      varchar(200),
    created_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_customers_object_deletions_object_key ON customers.object_deletions (object_key);
CREATE INDEX ix_customers_object_deletions_next_attempt ON customers.object_deletions (next_attempt_at);

-- +goose Down
DROP TABLE customers.object_deletions;
DROP TABLE customers.customer_attachments;
