-- Search (knowledge design K12, K16).

-- name: Search :many
-- Search finds the articles userID can read that are not trashed — and for
-- the Assistant none hidden from it — whose title or text match, in Norwegian
-- and in English at once. head is the query before its last word, last that
-- word, matched as a prefix too (empty when the query ends otherwise). The
-- excerpts are made for the hits only, with \x02 and \x03 around a match,
-- taken out of the text first. ts_headline reads markup as tags and drops
-- it, so <, > and & stand in as U+E000, U+E001 and U+E002 (taken out of the
-- text first too), which the server turns back.
WITH q AS (
    SELECT (
        CASE
            WHEN @last::text = '' THEN websearch_to_tsquery('norwegian', @head::text)
            WHEN @head::text = '' THEN websearch_to_tsquery('norwegian', @last::text) || to_tsquery('norwegian', @last::text || ':*')
            ELSE websearch_to_tsquery('norwegian', @head::text) && (websearch_to_tsquery('norwegian', @last::text) || to_tsquery('norwegian', @last::text || ':*'))
        END
    ) || (
        CASE
            WHEN @last::text = '' THEN websearch_to_tsquery('english', @head::text)
            WHEN @head::text = '' THEN websearch_to_tsquery('english', @last::text) || to_tsquery('english', @last::text || ':*')
            ELSE websearch_to_tsquery('english', @head::text) && (websearch_to_tsquery('english', @last::text) || to_tsquery('english', @last::text || ':*'))
        END
    ) AS query
), hits AS (
    SELECT a.id, a.title, a.icon, a.updated_at, a.content_text, ts_rank_cd(a.search, q.query) AS rank
    FROM knowledge.articles a
    CROSS JOIN q
    JOIN knowledge.article_access_default d ON d.article_id = a.id
    LEFT JOIN knowledge.article_access x ON x.article_id = a.id AND x.user_id = @user_id::uuid
    WHERE a.search @@ q.query
      AND a.trashed_at IS NULL
      AND coalesce(x.permission, d.permission) <> 'none'
      AND NOT (@assistant::boolean AND d.ai_hidden)
    ORDER BY rank DESC, a.updated_at DESC, a.id
    LIMIT @max_hits::int
)
SELECT h.id, h.title, h.icon, h.updated_at,
       ts_headline('norwegian', b.body, q.query, 'StartSel=' || chr(2) || ', StopSel=' || chr(3) || ', MaxWords=30, MinWords=12, ShortWord=2')::text AS headline_nb,
       ts_headline('english', b.body, q.query, 'StartSel=' || chr(2) || ', StopSel=' || chr(3) || ', MaxWords=30, MinWords=12, ShortWord=2')::text AS headline_en
FROM hits h
CROSS JOIN q
CROSS JOIN LATERAL (
    SELECT translate(
        translate(left(CASE WHEN h.content_text = '' THEN h.title ELSE h.content_text END, 200000),
                  chr(2) || chr(3) || chr(57344) || chr(57345) || chr(57346), ''),
        '<>&', chr(57344) || chr(57345) || chr(57346)) AS body
) b
ORDER BY h.rank DESC, h.updated_at DESC, h.id;
