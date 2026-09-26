-- name: GetSettings :one
-- GetSettings reads the installation's single settings row, the seller record
-- and the series start (invoices foundation design D2). The migration writes
-- it, so this always answers a row.
SELECT * FROM invoices.settings WHERE id = 1;
