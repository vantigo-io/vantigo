-- +goose Up
-- Invoices phase 6, delivery E-1 — send all (invoices phase 6 design I35,
-- I36; plan docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md, E1
-- and readings E2–E4). A send batch is one Send all: a selection on the
-- invoices list or every document of a billing run. Each document asked for
-- is one send job, which the invoices-send worker carries under a row lease
-- through the send's own cores — e-mail, EHF, eFaktura — or leaves for
-- printing. Neither table holds personal data: the recipient stays in
-- invoices.deliveries, and a job's detail is the module's own words, never a
-- provider's body. So an erasure has nothing to blank here.

-- A batch. billing_run_id names the run a batch of a run was made from, and
-- only then. finished_at is set when the last of its jobs ends.
CREATE TABLE invoices.send_batches (
    id                 bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    source             varchar(12) NOT NULL,
    billing_run_id     bigint      REFERENCES invoices.billing_runs (id) ON DELETE RESTRICT,
    requested_by       uuid        NOT NULL,
    created_at         timestamptz NOT NULL,
    finished_at        timestamptz,
    CONSTRAINT ck_send_batches_source CHECK (source IN ('selection', 'billing_run')),
    CONSTRAINT ck_send_batches_run CHECK ((billing_run_id IS NOT NULL) = (source = 'billing_run'))
);

CREATE INDEX ix_send_batches_run ON invoices.send_batches (billing_run_id, id) WHERE billing_run_id IS NOT NULL;

-- A job: one document of a batch. invoice_id carries no foreign key, as a
-- billing run item's documents do not: a draft asked for is skipped, and may
-- be deleted afterwards. channel is NULL until the worker chooses it (reading
-- E3). code is a skip's or a refusal's; detail the module's sentence for it.
-- attempts, lease_id, lease_until and started_at are the worker's: a claim
-- leases a queued job, or a sending one whose lease ran out, and started_at
-- is when the attempt in flight began — what a reclaimed e-mail job is judged
-- by (reading E4). transmission_id and delivery_id say what the job made.
CREATE TABLE invoices.send_jobs (
    id              bigint       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id        bigint       NOT NULL REFERENCES invoices.send_batches (id) ON DELETE RESTRICT,
    invoice_id      bigint       NOT NULL,
    channel         varchar(10),
    state           varchar(14)  NOT NULL DEFAULT 'queued',
    code            varchar(60),
    detail          varchar(500) NOT NULL DEFAULT '',
    attempts        integer      NOT NULL DEFAULT 0,
    lease_id        varchar(100),
    lease_until     timestamptz,
    started_at      timestamptz,
    transmission_id bigint,
    delivery_id     bigint,
    created_at      timestamptz  NOT NULL,
    finished_at     timestamptz,
    CONSTRAINT ck_send_jobs_channel CHECK (channel IN ('email', 'ehf', 'efaktura', 'print')),
    CONSTRAINT ck_send_jobs_state CHECK (state IN ('queued', 'sending', 'sent', 'handed_over', 'awaiting_print',
        'printed', 'skipped', 'failed', 'uncertain')),
    CONSTRAINT ck_send_jobs_code CHECK (state NOT IN ('skipped', 'failed') OR code IS NOT NULL),
    CONSTRAINT ck_send_jobs_finished CHECK ((finished_at IS NOT NULL) = (state NOT IN ('queued', 'sending'))),
    CONSTRAINT ck_send_jobs_attempts CHECK (attempts >= 0),
    CONSTRAINT ux_send_jobs_document UNIQUE (batch_id, invoice_id)
);

-- Two batches never send one document at once: a second enqueue skips it
-- send_in_progress.
CREATE UNIQUE INDEX ux_send_jobs_live ON invoices.send_jobs (invoice_id) WHERE state IN ('queued', 'sending');
CREATE INDEX ix_send_jobs_due ON invoices.send_jobs (lease_until, id) WHERE state IN ('queued', 'sending');
CREATE INDEX ix_send_jobs_batch ON invoices.send_jobs (batch_id, id);

-- A batch is never deleted, and only its finished_at is ever set, once.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_send_batch_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a send batch is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF (to_jsonb(OLD) - 'finished_at') = (to_jsonb(NEW) - 'finished_at') AND OLD.finished_at IS NULL THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a send batch is immutable' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_send_batches_immutable
    BEFORE UPDATE OR DELETE ON invoices.send_batches
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_send_batch_change();

-- A job is never deleted, its batch, document and creation never change, and
-- its state moves only as the worker moves it: a claim (queued or sending to
-- sending), a retry or a release (sending to queued), an outcome (sending to
-- an end state), and a printed job confirmed posted (awaiting_print to
-- printed). Every end state but awaiting_print is final.
-- +goose StatementBegin
CREATE FUNCTION invoices.refuse_send_job_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoices: a send job is never deleted' USING ERRCODE = 'P0001';
    END IF;
    IF NEW.id <> OLD.id OR NEW.batch_id <> OLD.batch_id OR NEW.invoice_id <> OLD.invoice_id
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'invoices: a send job keeps its batch and document' USING ERRCODE = 'P0001';
    END IF;
    IF (OLD.state = 'queued' AND NEW.state = 'sending')
       OR (OLD.state = 'sending' AND NEW.state IN ('sending', 'queued', 'sent', 'handed_over', 'awaiting_print',
           'skipped', 'failed', 'uncertain'))
       OR (OLD.state = 'awaiting_print' AND NEW.state = 'printed') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'invoices: a send job cannot move from % to %', OLD.state, NEW.state USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_send_jobs_immutable
    BEFORE UPDATE OR DELETE ON invoices.send_jobs
    FOR EACH ROW EXECUTE FUNCTION invoices.refuse_send_job_change();

-- +goose Down
DROP TABLE invoices.send_jobs;
DROP TABLE invoices.send_batches;
DROP FUNCTION invoices.refuse_send_job_change();
DROP FUNCTION invoices.refuse_send_batch_change();
