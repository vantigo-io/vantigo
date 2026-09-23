-- name: ListCustomerTags :many
-- ListCustomerTags is GET /customers/tags (owner and tags design D2, D3):
-- the whole vocabulary, name-ascending, each with how many customers carry
-- it. The count travels with the list rather than behind a second endpoint
-- because the one place it is needed is the Manage tags modal's delete
-- confirmation, which is already showing the list — and a tag vocabulary is
-- tens of rows, not thousands, so a correlated count per row costs nothing
-- worth a second round trip. id is the tie-break so two tags that differ only
-- in case (which the unique index forbids) or in trailing punctuation still
-- order deterministically.
SELECT t.id, t.name, t.color,
       (SELECT count(*) FROM customers.customer_tags ct WHERE ct.tag_id = t.id) AS customer_count
FROM customers.tags t
ORDER BY t.name, t.id;

-- name: InsertCustomerTag :one
-- InsertCustomerTag is POST /customers/tags. A unique violation on
-- ux_customers_tags_name_lower is the duplicate name (409 tag_exists), which
-- the handler recognises with db.IsUniqueViolation naming that constraint
-- exactly — never a bare "some unique violation", which would also swallow a
-- uuid collision the caller must hear about as a 500.
INSERT INTO customers.tags (id, name, color)
VALUES (@id, @name, @color)
RETURNING id, name, color;

-- name: UpdateCustomerTagRow :one
-- UpdateCustomerTagRow is PUT /customers/tags/{tagId}: a rename, a recolour,
-- or both. Named …Row rather than UpdateCustomerTag so the generated method
-- does not read as "update a customer's tag", which is what the set replace
-- below does. Renaming records nothing on the customers that carry the tag
-- (design D2: the tag is the vocabulary, not the customer), so there is no
-- timeline write anywhere near this statement.
--
-- The customerCount the 200 answers comes back from this same statement
-- (final fix wave M3). A rename must report the count the list would, and
-- re-reading the row afterwards had a window of its own: a tag deleted
-- between the UPDATE and that read answered 404 for a rename that had in fact
-- happened, or a 500 if the branch was forgotten. One statement has no window
-- and no second round trip. pgx.ErrNoRows means the tag does not exist.
UPDATE customers.tags
SET name = @name, color = @color
WHERE id = @id
RETURNING id, name, color,
          (SELECT count(*) FROM customers.customer_tags ct WHERE ct.tag_id = customers.tags.id) AS customer_count;

-- name: DeleteCustomerTag :execrows
-- DeleteCustomerTag is DELETE /customers/tags/{tagId}. The customer_tags
-- links go with it through the table's own ON DELETE CASCADE — there is no
-- second statement here, and that is the point of the cascade. The affected
-- row count is how the handler tells 204 from 404.
DELETE FROM customers.tags WHERE id = @id;

-- name: CustomerTagsByIDs :many
-- CustomerTagsByIDs resolves the ids PUT /customers/{id}/tags was given, in
-- one round trip, so an unknown one is a field error on tagIds rather than a
-- foreign-key violation surfacing as a 500. Duplicates in the input are
-- tolerated: = ANY() answers each tag once whatever the caller sent.
SELECT id, name, color FROM customers.tags WHERE id = ANY(@ids::uuid[]) ORDER BY name, id;

-- name: CustomerTagsForCustomers :many
-- CustomerTagsForCustomers is the tags on a whole page of customers in ONE
-- query (design D2), never one query per row: the list answers 25 customers
-- and a per-row read would be 25 round trips for data that is on the wire
-- either way. The single-customer reads (GET /customers/{id} and every
-- sub-resource PUT that answers SafeCustomerResponse) call it with a
-- one-element array rather than having a query of their own, so there is one
-- shape of tags-on-a-response and one place it can be wrong.
--
-- Ordered by customer, then tag name, so the chips come out in the same order
-- the vocabulary lists in and a caller never sees them shuffle between reads.
SELECT ct.customer_id, t.id, t.name, t.color
FROM customers.customer_tags ct
JOIN customers.tags t ON t.id = ct.tag_id
WHERE ct.customer_id = ANY(@customer_ids::int[])
ORDER BY ct.customer_id, t.name, t.id;

-- name: DeleteCustomerTagLinks :exec
-- DeleteCustomerTagLinks and InsertCustomerTagLinks are PUT
-- /customers/{id}/tags: the set is REPLACED, which is the natural write for a
-- multi-select, so it is a delete of everything followed by an insert of
-- what was asked for, both inside one transaction. A diff (delete the
-- removed, insert the added) would be the same two statements with more
-- arithmetic and the same outcome; replacing is what the endpoint means.
DELETE FROM customers.customer_tags WHERE customer_id = @customer_id;

-- name: InsertCustomerTagLinks :exec
-- unnest, not one INSERT per tag: the whole set is one statement, so a
-- transaction that dies halfway cannot leave a partial set. An empty array
-- inserts nothing, which is exactly what clearing a customer's tags means.
INSERT INTO customers.customer_tags (customer_id, tag_id)
SELECT @customer_id::int, unnest(@tag_ids::uuid[]);
