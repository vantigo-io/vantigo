-- Tags and tag links: getCommunicationsTags, postCommunicationsTags,
-- putCommunicationsConversationsByIdTagsByTagId,
-- deleteCommunicationsConversationsByIdTagsByTagId (communications inventory
-- §1.4; TagEndpoints.cs).

-- name: ListTags :many
-- ListTags: a bare array, ordered by Name ascending (inventory §1.4).
SELECT id, name, color FROM communications.tags ORDER BY name ASC;

-- name: InsertTag :one
-- CreateTag's insert; a unique violation on name -> 409 tag_exists, handled
-- by the caller via db.IsUniqueViolation.
INSERT INTO communications.tags (id, name, color) VALUES (@id, @name, @color)
RETURNING id, name, color;

-- name: ConversationAndTagExist :one
-- AddTag's combined existence check
-- (`!await db.Conversations.AnyAsync(...) || !await db.Tags.AnyAsync(...)`)
-- in one round trip. Each EXISTS names its own table explicitly (rather than
-- the bare "id" both tables share) so the two subqueries never look
-- ambiguous to a static analyzer.
SELECT
  EXISTS(SELECT 1 FROM communications.conversations con WHERE con.id = @conversation_id) AS conversation_exists,
  EXISTS(SELECT 1 FROM communications.tags tg WHERE tg.id = @tag_id) AS tag_exists;

-- name: InsertConversationTagIfAbsent :exec
-- AddTag's idempotent link insert
-- (`if (!await db.ConversationTags.AnyAsync(...)) { ...Add...; ...SaveChanges... }`,
-- inventory §1.4: "idempotent" — 204 whether or not the tag was already
-- linked). ON CONFLICT DO NOTHING targets conversation_tags' own primary
-- key (conversation_id, tag_id), so this needs no separate existence read.
INSERT INTO communications.conversation_tags (conversation_id, tag_id)
VALUES (@conversation_id, @tag_id)
ON CONFLICT DO NOTHING;

-- name: DeleteConversationTag :execrows
-- RemoveTag: NOT idempotent (inventory §1.4) — the caller checks the
-- affected-row count and answers 404 when it is zero, exactly mirroring
-- .NET's `db.ConversationTags.FindAsync(...) is null -> NotFound`.
DELETE FROM communications.conversation_tags WHERE conversation_id = @conversation_id AND tag_id = @tag_id;
