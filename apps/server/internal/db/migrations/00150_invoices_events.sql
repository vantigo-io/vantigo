-- +goose Up
-- The events of invoices that the virtual employees listen to (design V30):
-- invoices.invoice.overdue, published once for each invoice by a worker that
-- records here which invoices it announced, and invoices.payment.received,
-- published by a trigger on invoices.payments.

CREATE TABLE invoices.overdue_announced (
    invoice_id   bigint      PRIMARY KEY REFERENCES invoices.invoices (id) ON DELETE CASCADE,
    announced_on date        NOT NULL,
    announced_at timestamptz NOT NULL
);

-- Invoices that are already past due when this migration runs are not announced: the event is for the
-- invoice that becomes overdue, and a backlog of old ones would wake every follow-up at once.
INSERT INTO invoices.overdue_announced (invoice_id, announced_on, announced_at)
SELECT id, current_date, now() FROM invoices.invoices WHERE kind = 'invoice' AND status = 'issued' AND due_date < current_date;

-- Four write paths register a payment (a person, a bank line matched, a bank line
-- applied, a Vipps capture), and each holds the invoice FOR UPDATE; the event is
-- written by the insert itself, so it commits with the payment or not at all, and
-- the aggregate lock the outbox relies on is the invoice's. The payload is
-- contracts.PaymentReceived: ids, the amount and the date, no personal data.
-- +goose StatementBegin
CREATE FUNCTION invoices.publish_payment_received() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    inv invoices.invoices%ROWTYPE;
BEGIN
    SELECT * INTO inv FROM invoices.invoices WHERE id = NEW.invoice_id;
    INSERT INTO platform.events (name, version, module, aggregate, aggregate_id, payload, occurred_at)
    VALUES ('invoices.payment.received', 1, 'invoices', 'invoices.payment', NEW.id::text,
            jsonb_build_object(
                'paymentId', NEW.id, 'invoiceId', NEW.invoice_id, 'number', coalesce(inv.number, 0),
                'customerId', inv.customer_id, 'amount', to_char(NEW.amount, 'FM999999999990.00'), 'currency', NEW.currency,
                'paidOn', to_char(NEW.paid_on, 'YYYY-MM-DD'), 'source', NEW.source, 'registeredAt', NEW.registered_at),
            NEW.registered_at);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tr_payments_received AFTER INSERT ON invoices.payments
    FOR EACH ROW EXECUTE FUNCTION invoices.publish_payment_received();

-- +goose Down
DROP TRIGGER tr_payments_received ON invoices.payments;
DROP FUNCTION invoices.publish_payment_received();
DROP TABLE invoices.overdue_announced;
