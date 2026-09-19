-- name: CountAttachmentsForEntries :many
-- CountAttachmentsForEntries is how many receipts each of the given expenses
-- carries, for the attachmentCount every entry is rendered with — one query
-- for a whole page rather than one per row. An id with no receipts is simply
-- absent from the result, which the caller reads as zero.
SELECT entry_id, count(*)::bigint AS total
FROM expenses.attachments
WHERE entry_id = ANY(@entry_ids::bigint[])
GROUP BY entry_id;
