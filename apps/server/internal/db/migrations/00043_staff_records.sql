-- +goose Up
-- Staff records (staff foundation design S14, S15, §3.6, delivery A2): the
-- document archive per person, its categories with their retention classes,
-- and the ledger of objects still to be removed from the object store. No
-- column is added to staff.people or staff.settings — 00042 carries every one
-- the phase needs. No CHECK constraint, as in 00042: the module's validation
-- owns the domain rules (a class of A, D or E; restricted exactly when the
-- class is E) and reports them per field.

-- A document's category (S14): the six seeds and the company's own. The
-- retention class says when a document of the category is erased (S15) and is
-- fixed when the category is created; restricted — a health category, readable
-- only by the person and staff:personal-data — is true exactly when the class
-- is E, which the module holds. A seeded category is never changed; one no
-- longer wanted is archived, so the documents filed under it keep their name.
CREATE TABLE staff.document_categories (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key             varchar(40)  NOT NULL,
    name_en         varchar(100) NOT NULL,
    name_nb         varchar(100) NOT NULL,
    retention_class char(1)      NOT NULL,
    restricted      boolean      NOT NULL DEFAULT false,
    seeded          boolean      NOT NULL DEFAULT false,
    archived_at     timestamptz,
    sort            integer      NOT NULL,
    revision        integer      NOT NULL DEFAULT 1,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_document_categories_key ON staff.document_categories (key);

INSERT INTO staff.document_categories (key, name_en, name_nb, retention_class, restricted, seeded, sort, created_at, updated_at)
VALUES
    ('contract',       'Contract',       'Arbeidsavtale',  'A', false, true, 1, now(), now()),
    ('attest',         'Attest',         'Attest',         'A', false, true, 2, now(), now()),
    ('certificate',    'Certificate',    'Sertifikat',     'D', false, true, 3, now(), now()),
    ('correspondence', 'Correspondence', 'Korrespondanse', 'D', false, true, 4, now(), now()),
    ('health',         'Health',         'Helse',          'E', true,  true, 5, now(), now()),
    ('other',          'Other',          'Annet',          'D', false, true, 6, now(), now());

-- A document in a person's archive (S14, §3.6): the row points at its object
-- in the staff storage scope under people/<personId>/<uuid>. Rows are deleted,
-- never marked (A2 pre-flight): a delete inserts the object's ledger row in the
-- same transaction. employment_id is the employment covering signed_at — else
-- the upload day — when exactly one does, and HR may set it; it is the A
-- erase's handle on the document. uploaded_by_user_id is identity's user,
-- opaque here (module-boundaries rule 4).
CREATE TABLE staff.documents (
    id                  bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    person_id           bigint       NOT NULL REFERENCES staff.people (id),
    category_id         bigint       NOT NULL REFERENCES staff.document_categories (id),
    employment_id       bigint       REFERENCES staff.employments (id),
    object_key          varchar(200) NOT NULL,
    file_name           varchar(255) NOT NULL,
    content_type        varchar(100) NOT NULL,
    size_bytes          bigint       NOT NULL,
    valid_to            date,
    signed_at           date,
    uploaded_by_user_id uuid         NOT NULL,
    revision            integer      NOT NULL DEFAULT 1,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_documents_object_key ON staff.documents (object_key);
CREATE INDEX ix_documents_person ON staff.documents (person_id);
CREATE INDEX ix_documents_category ON staff.documents (category_id);
-- The class A erase finds an employment's documents by it (S15).
CREATE INDEX ix_documents_employment ON staff.documents (employment_id);

-- The objects still to be removed from the store (S15, M7): a delete or an
-- erase removes the row and inserts its object's key here in one transaction,
-- and the ledger finishes the object after the commit, retried until the
-- store confirms, so a legal erase is never best-effort.
CREATE TABLE staff.object_deletions (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    object_key      varchar(200) NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at timestamptz  NOT NULL,
    claimed_until   timestamptz,
    last_error      varchar(200),
    created_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_object_deletions_object_key ON staff.object_deletions (object_key);
CREATE INDEX ix_object_deletions_next_attempt ON staff.object_deletions (next_attempt_at);

-- +goose Down
DROP TABLE staff.object_deletions;
DROP TABLE staff.documents;
DROP TABLE staff.document_categories;
