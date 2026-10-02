-- name: DeliveriesOf :many
-- DeliveriesOf is every e-mail that handed a document over (D4), the first
-- first.
SELECT * FROM invoices.deliveries
WHERE invoice_id = @invoice_id
ORDER BY sent_at, id;

-- name: InsertDelivery :one
-- InsertDelivery logs one send (D4) — a plain insert. Blanking the recipient
-- of a customer erased meanwhile is tr_deliveries_parent's, which reads the
-- marker after its lock wait on the document; a check in this statement would
-- read a snapshot taken before that wait (D6).
INSERT INTO invoices.deliveries (
    invoice_id, recipient, subject, message_id, pdf_sha256, sent_at, sent_by_user_id
) VALUES (
    @invoice_id, @recipient, @subject, @message_id, @pdf_sha256, @sent_at::timestamptz, @sent_by_user_id
)
RETURNING *;

-- name: CustomerErased :one
-- CustomerErased is whether this module has anonymised a customer (D6): a
-- send to one is refused customer_anonymised (D4).
SELECT EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = @customer_id) AS erased;
