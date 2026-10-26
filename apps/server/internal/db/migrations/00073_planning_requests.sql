-- +goose Up
-- Planning delivery B (planning design §3.4, S16): the requests — a project's
-- need for a person not yet named — their competence requirements, and the
-- link from the allocation that fills a request back to it. No CHECK
-- constraints, as 00072 explains: the module's validation owns the rules
-- (the period, the hours a week, the status, the level) and reports them per
-- field. person-less by design: a request names a project (Projects', opaque)
-- and competences (Staff's, opaque), never a person.

-- A request (S16): a project, a title, a period of civil dates, a load in
-- hours a week only — a request has no person whose position a percentage
-- could be read against — a status open, filled or withdrawn, and the
-- planners' note. filled is true exactly while an allocation names the
-- request (below); withdrawn is final.
CREATE TABLE planning.requests (
    id                 bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    project_id         integer      NOT NULL,
    title              varchar(100) NOT NULL,
    from_date          date         NOT NULL,
    to_date            date         NOT NULL,
    hours_per_week     numeric(5,2) NOT NULL,
    status             varchar(10)  NOT NULL,
    note               varchar(500),
    revision           integer      NOT NULL DEFAULT 1,
    created_at         timestamptz  NOT NULL,
    created_by_user_id uuid,
    updated_at         timestamptz  NOT NULL,
    updated_by_user_id uuid
);
CREATE INDEX ix_requests_project_status ON planning.requests (project_id, status);
CREATE INDEX ix_requests_status_from ON planning.requests (status, from_date);

-- A requirement (S16): a competence the person who fills the request must
-- hold on its first day, with the least level on a competence that has one.
-- At most ten a request, judged by the module; each competence once.
CREATE TABLE planning.request_requirements (
    id            bigint   GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    request_id    bigint   NOT NULL REFERENCES planning.requests (id) ON DELETE CASCADE,
    competence_id bigint   NOT NULL,
    min_level     smallint
);
CREATE UNIQUE INDEX ux_request_requirements_request_competence
    ON planning.request_requirements (request_id, competence_id);

-- The fill (S16, §3.2): the allocation that fills a request names it. At most
-- one row fills a request, and the request is filled exactly while that row
-- exists — the module reopens the request when the row is deleted, in the
-- same transaction, which is why the key restricts a delete of the request
-- itself rather than cascading.
ALTER TABLE planning.allocations
    ADD COLUMN request_id bigint REFERENCES planning.requests (id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX ux_allocations_request ON planning.allocations (request_id) WHERE request_id IS NOT NULL;

-- +goose Down
DROP INDEX planning.ux_allocations_request;
ALTER TABLE planning.allocations DROP COLUMN request_id;
DROP TABLE planning.request_requirements;
DROP TABLE planning.requests;
