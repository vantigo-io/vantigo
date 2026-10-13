-- name: InsertQuickRequest :execrows
-- InsertQuickRequest keeps a quick invoice's requestId (D17, after review),
-- in the quick invoice's transaction right after its draft. A second request
-- with the id waits on the first's uncommitted key and, once it commits,
-- inserts nothing: 0 rows, so it answers the first's invoice instead.
INSERT INTO invoices.quick_requests (request_id, invoice_id, body_sha256, created_at)
VALUES (@request_id, @invoice_id, @body_sha256, @created_at)
ON CONFLICT (request_id) DO NOTHING;

-- name: QuickRequest :one
-- QuickRequest is a requestId as kept: the invoice it issued and the body's
-- hash.
SELECT * FROM invoices.quick_requests WHERE request_id = @request_id;
