-- +goose Up
-- Expenses' side of the books (accounting design A20 as amended by R2; plan
-- docs/superpowers/plans/2026-11-14-accounting-plan.md, delivery C): a log of
-- what a ledger posts from — an expense or a travel claim approved, its
-- approval undone, it reimbursed, its reimbursement undone — in one order, and
-- the events that make a reader prompt. Nothing here names a ledger;
-- contracts.ExpenseJournal reads it.
--
-- What one fact covers. The unit that moves through Expenses' flow is a
-- standalone expense or a whole travel claim, never one of a claim's lines
-- (unitOf in authorize.go): a claim is approved, unapproved and reimbursed as
-- one. So a fact is about an entry (subject 'entry', an expense with no claim)
-- or a claim (subject 'claim', every one of its lines): a line of a claim
-- appears only in its claim's facts, and every entry's cost is in exactly one
-- approved fact per approval and one unapproved fact per undoing.
--
-- lines is the subject's lines as they stood when the fact committed — kind,
-- date, category, who paid, currency, gross, VAT, project, the supplier's
-- free text and, for the supplier_invoice kind, its number and due date —
-- because an unapproved expense is a draft again and may be changed: the
-- fact keeps what was approved. No line carries a description, a place or a
-- name; the person is user_id, an opaque id.
--
-- seq comes from the counter row 'journal' in expenses.counters, taken by a
-- DEFERRABLE INITIALLY DEFERRED constraint trigger at commit and held for that
-- instant only, as Invoices' log does (00180): seqs become visible in the
-- order they were taken, so a reader paging by seq never passes a fact that
-- commits later with a lower one. undoes_seq is, on an unapproved or a
-- reimbursement_undone fact, the seq of the approved or reimbursed fact of
-- the same subject it undoes. business_date is the cost's day for an approval
-- and its undoing — the entry's date, or a claim's latest line date — and the
-- reimbursement's own date for a reimbursement and its undoing.
-- Append-only: refuse_journal_fact_change refuses every UPDATE and DELETE.
-- No approval or reimbursement may commit between the backfill's read and the
-- triggers' creation, or it would be in neither: the two tables are held in
-- SHARE ROW EXCLUSIVE — reads go on, writes wait — to the migration's commit.
LOCK TABLE expenses.entries, expenses.claims IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE expenses.counters (
    counter_name varchar(30) PRIMARY KEY,
    next_value   bigint      NOT NULL
);

CREATE TABLE expenses.journal_facts (
    seq                     bigint       PRIMARY KEY,
    occurrence              varchar(30)  NOT NULL,
    subject                 varchar(10)  NOT NULL,
    subject_id              bigint       NOT NULL,
    user_id                 uuid         NOT NULL,
    business_date           date         NOT NULL,
    undoes_seq              bigint,
    reimbursement_reference varchar(100),
    lines                   jsonb        NOT NULL,
    occurred_at             timestamptz  NOT NULL,
    created_at              timestamptz  NOT NULL,
    CONSTRAINT ck_journal_facts_seq CHECK (seq >= 1),
    CONSTRAINT ck_journal_facts_occurrence
        CHECK (occurrence IN ('approved', 'unapproved', 'reimbursed', 'reimbursement_undone')),
    CONSTRAINT ck_journal_facts_subject CHECK (subject IN ('entry', 'claim')),
    CONSTRAINT ck_journal_facts_lines CHECK (jsonb_typeof(lines) = 'array')
);
CREATE INDEX ix_journal_facts_subject ON expenses.journal_facts (subject, subject_id, seq);

-- +goose StatementBegin
CREATE FUNCTION expenses.refuse_journal_fact_change()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
    RAISE EXCEPTION 'expenses: a journal fact is never changed' USING ERRCODE = 'P0001';
END;
$function$;
-- +goose StatementEnd

CREATE TRIGGER tr_journal_facts_immutable
    BEFORE UPDATE OR DELETE ON expenses.journal_facts
    FOR EACH ROW EXECUTE FUNCTION expenses.refuse_journal_fact_change();

-- One line of a fact, as contracts.ExpenseJournal answers it.
-- +goose StatementBegin
CREATE FUNCTION expenses.journal_line(e expenses.entries)
RETURNS jsonb LANGUAGE sql STABLE AS $function$
    SELECT jsonb_build_object(
        'entryId', e.id, 'kind', e.kind, 'date', to_char(e.entry_date, 'YYYY-MM-DD'),
        'categoryId', e.category_id, 'paidBy', e.paid_by, 'currency', e.currency,
        'gross', to_char(e.gross_amount, 'FM999999999990.00'),
        'vat', CASE WHEN e.vat_amount IS NULL THEN NULL ELSE to_char(e.vat_amount, 'FM999999999990.00') END,
        'projectId', e.project_id, 'supplier', e.supplier,
        'supplierInvoiceNumber', e.supplier_invoice_number,
        'supplierDueDate', CASE WHEN e.supplier_due_date IS NULL THEN NULL ELSE to_char(e.supplier_due_date, 'YYYY-MM-DD') END)
$function$;
-- +goose StatementEnd

-- The lines of a subject now: an entry's own row, or a claim's lines by date.
-- +goose StatementBegin
CREATE FUNCTION expenses.journal_lines(subject text, subject_id bigint)
RETURNS jsonb LANGUAGE sql STABLE AS $function$
    SELECT coalesce(jsonb_agg(expenses.journal_line(e) ORDER BY e.entry_date, e.id), '[]'::jsonb)
    FROM expenses.entries e
    WHERE (subject = 'entry' AND e.id = subject_id) OR (subject = 'claim' AND e.claim_id = subject_id)
$function$;
-- +goose StatementEnd

-- The facts that exist when this migration runs, so books started later find
-- the history: every approved standalone expense and claim at its decision,
-- every reimbursed one at its reimbursement, in the order they happened, an
-- approval before a reimbursement at the same moment, then by subject and id,
-- numbered from 1. There is no history of an undoing to replay: an undone
-- approval left nothing behind. The counter row is made only when there is
-- something to number.
INSERT INTO expenses.journal_facts (seq, occurrence, subject, subject_id, user_id, business_date,
    reimbursement_reference, lines, occurred_at, created_at)
SELECT row_number() OVER (ORDER BY f.at, f.ord, f.subject, f.subject_id), f.occurrence, f.subject, f.subject_id,
    f.user_id, f.business_date, f.reference, expenses.journal_lines(f.subject, f.subject_id), f.at, now()
FROM (
    SELECT coalesce(decided_at, updated_at) AS at, 0 AS ord, 'approved' AS occurrence, 'entry' AS subject,
        id AS subject_id, user_id, entry_date AS business_date, NULL::varchar AS reference
    FROM expenses.entries WHERE claim_id IS NULL AND status = 'approved'
    UNION ALL
    SELECT reimbursed_at, 1, 'reimbursed', 'entry', id, user_id, coalesce(reimbursement_date, reimbursed_at::date),
        reimbursement_reference
    FROM expenses.entries WHERE claim_id IS NULL AND reimbursed_at IS NOT NULL
    UNION ALL
    SELECT coalesce(c.decided_at, c.updated_at), 0, 'approved', 'claim', c.id, c.user_id,
        coalesce((SELECT max(e.entry_date) FROM expenses.entries e WHERE e.claim_id = c.id), c.departure_at::date), NULL
    FROM expenses.claims c WHERE c.status = 'approved'
    UNION ALL
    SELECT c.reimbursed_at, 1, 'reimbursed', 'claim', c.id, c.user_id,
        coalesce(c.reimbursement_date, c.reimbursed_at::date), c.reimbursement_reference
    FROM expenses.claims c WHERE c.reimbursed_at IS NOT NULL
) AS f;

INSERT INTO expenses.counters (counter_name, next_value)
SELECT 'journal', max(seq) + 1 FROM expenses.journal_facts HAVING count(*) > 0;

-- The trigger behind every fact from now on, and the event it publishes.
-- TG_ARGV[0] is the subject ('entry' or 'claim'), TG_ARGV[1] the occurrence;
-- each trigger's WHEN lets through exactly the change that occurrence is. It
-- runs at commit; the lines are read then, as the transaction left them. The
-- event is published by the same statement, on the transaction that made the
-- fact and under the subject's row lock, which the change took: its payload
-- carries the seq, the subject's id, the day and, for an entry, its currency
-- and gross, and never a name (O3).
-- +goose StatementBegin
CREATE FUNCTION expenses.record_journal_fact()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
    subj       text := TG_ARGV[0];
    occ        text := TG_ARGV[1];
    next_seq   bigint;
    day        date;
    undoes     bigint;
    reference  varchar(100);
    line_set   jsonb;
    payload    jsonb;
BEGIN
    line_set := expenses.journal_lines(subj, NEW.id);
    IF occ IN ('approved', 'unapproved') THEN
        IF subj = 'entry' THEN
            day := NEW.entry_date;
        ELSE
            SELECT max(e.entry_date) INTO day FROM expenses.entries e WHERE e.claim_id = NEW.id;
            day := coalesce(day, NEW.departure_at::date);
        END IF;
    ELSIF occ = 'reimbursed' THEN
        day := coalesce(NEW.reimbursement_date, NEW.reimbursed_at::date);
        reference := NEW.reimbursement_reference;
    ELSE
        day := coalesce(OLD.reimbursement_date, OLD.reimbursed_at::date, now()::date);
        reference := OLD.reimbursement_reference;
    END IF;
    IF occ IN ('unapproved', 'reimbursement_undone') THEN
        SELECT max(f.seq) INTO undoes FROM expenses.journal_facts f
        WHERE f.subject = subj AND f.subject_id = NEW.id
          AND f.occurrence = CASE occ WHEN 'unapproved' THEN 'approved' ELSE 'reimbursed' END;
    END IF;
    INSERT INTO expenses.counters (counter_name, next_value)
    VALUES ('journal', 2)
    ON CONFLICT (counter_name) DO UPDATE SET next_value = expenses.counters.next_value + 1
    RETURNING next_value - 1 INTO next_seq;
    INSERT INTO expenses.journal_facts (seq, occurrence, subject, subject_id, user_id, business_date, undoes_seq,
        reimbursement_reference, lines, occurred_at, created_at)
    VALUES (next_seq, occ, subj, NEW.id, NEW.user_id, day, undoes, reference, line_set, now(), now());

    payload := jsonb_build_object('seq', next_seq, 'date', to_char(day, 'YYYY-MM-DD'));
    IF subj = 'entry' THEN
        payload := payload || jsonb_build_object('entryId', NEW.id, 'currency', NEW.currency,
            'grossAmount', to_char(NEW.gross_amount, 'FM999999999990.00'));
    ELSE
        payload := payload || jsonb_build_object('claimId', NEW.id, 'lines', jsonb_array_length(line_set));
    END IF;
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('expenses.' || subj || '.' || occ, 1, 'expenses', 'expenses.' || subj, NEW.id::text, payload, now());
    RETURN NULL;
END;
$function$;
-- +goose StatementEnd

-- A standalone expense's occurrences. A line of a claim (claim_id set) has no
-- status of its own and makes no fact: its claim's do. An INSERT of a row
-- already approved or reimbursed (a seed, an import) writes its facts too, so
-- nothing approved goes unlogged whatever wrote it.
CREATE CONSTRAINT TRIGGER tr_entries_journal_approved
    AFTER UPDATE ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND OLD.status <> 'approved' AND NEW.status = 'approved')
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'approved');
CREATE CONSTRAINT TRIGGER tr_entries_journal_unapproved
    AFTER UPDATE ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND OLD.status = 'approved' AND NEW.status <> 'approved')
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'unapproved');
CREATE CONSTRAINT TRIGGER tr_entries_journal_reimbursed
    AFTER UPDATE ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND OLD.reimbursed_at IS NULL AND NEW.reimbursed_at IS NOT NULL)
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'reimbursed');
CREATE CONSTRAINT TRIGGER tr_entries_journal_reimbursement_undone
    AFTER UPDATE ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND OLD.reimbursed_at IS NOT NULL AND NEW.reimbursed_at IS NULL)
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'reimbursement_undone');
CREATE CONSTRAINT TRIGGER tr_entries_journal_inserted_approved
    AFTER INSERT ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND NEW.status = 'approved')
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'approved');
CREATE CONSTRAINT TRIGGER tr_entries_journal_inserted_reimbursed
    AFTER INSERT ON expenses.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (NEW.claim_id IS NULL AND NEW.reimbursed_at IS NOT NULL)
    EXECUTE FUNCTION expenses.record_journal_fact('entry', 'reimbursed');

-- A travel claim's, the same four.
CREATE CONSTRAINT TRIGGER tr_claims_journal_approved
    AFTER UPDATE ON expenses.claims
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.status <> 'approved' AND NEW.status = 'approved')
    EXECUTE FUNCTION expenses.record_journal_fact('claim', 'approved');
CREATE CONSTRAINT TRIGGER tr_claims_journal_unapproved
    AFTER UPDATE ON expenses.claims
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.status = 'approved' AND NEW.status <> 'approved')
    EXECUTE FUNCTION expenses.record_journal_fact('claim', 'unapproved');
CREATE CONSTRAINT TRIGGER tr_claims_journal_reimbursed
    AFTER UPDATE ON expenses.claims
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.reimbursed_at IS NULL AND NEW.reimbursed_at IS NOT NULL)
    EXECUTE FUNCTION expenses.record_journal_fact('claim', 'reimbursed');
CREATE CONSTRAINT TRIGGER tr_claims_journal_reimbursement_undone
    AFTER UPDATE ON expenses.claims
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW WHEN (OLD.reimbursed_at IS NOT NULL AND NEW.reimbursed_at IS NULL)
    EXECUTE FUNCTION expenses.record_journal_fact('claim', 'reimbursement_undone');

-- +goose Down
-- The log is bookkeeping: the Down refuses while it holds a fact (or its
-- counter has numbered one), as Invoices' 00180 does. With none, it drops
-- what the Up made.
-- +goose StatementBegin
DO $down$
BEGIN
    IF EXISTS (SELECT 1 FROM expenses.journal_facts)
       OR EXISTS (SELECT 1 FROM expenses.counters WHERE counter_name = 'journal') THEN
        RAISE EXCEPTION 'expenses: the expense journal holds facts' USING ERRCODE = 'P0001';
    END IF;
END;
$down$;
-- +goose StatementEnd

DROP TRIGGER tr_claims_journal_reimbursement_undone ON expenses.claims;
DROP TRIGGER tr_claims_journal_reimbursed ON expenses.claims;
DROP TRIGGER tr_claims_journal_unapproved ON expenses.claims;
DROP TRIGGER tr_claims_journal_approved ON expenses.claims;
DROP TRIGGER tr_entries_journal_inserted_reimbursed ON expenses.entries;
DROP TRIGGER tr_entries_journal_inserted_approved ON expenses.entries;
DROP TRIGGER tr_entries_journal_reimbursement_undone ON expenses.entries;
DROP TRIGGER tr_entries_journal_reimbursed ON expenses.entries;
DROP TRIGGER tr_entries_journal_unapproved ON expenses.entries;
DROP TRIGGER tr_entries_journal_approved ON expenses.entries;
DROP FUNCTION expenses.record_journal_fact();
DROP TABLE expenses.journal_facts;
DROP FUNCTION expenses.journal_lines(text, bigint);
DROP FUNCTION expenses.journal_line(expenses.entries);
DROP FUNCTION expenses.refuse_journal_fact_change();
DROP TABLE expenses.counters;
