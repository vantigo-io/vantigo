-- +goose Up
-- Create orders' requests (purchasing design A11, A58): one row per
-- clientRequestId that made or added to drafts, written in the same
-- transaction, so a replay answers the result as it was — whatever happened
-- to the drafts since — and the same id with another body is refused. The
-- result is kept as written: per draft its order, supplier and site ids,
-- whether the request created it, how many lines it touched and which of the
-- request's rows got no price; no supplier's or person's name, so the
-- supplier's erase and export have nothing to take here. The actor is the
-- caller's opaque user id.
CREATE TABLE purchasing.proposal_requests (
    client_request_id uuid        PRIMARY KEY,
    request_hash      char(64)    NOT NULL,
    orders            jsonb       NOT NULL DEFAULT '[]'::jsonb,
    actor_user_id     uuid,
    created_at        timestamptz NOT NULL
);

-- +goose Down
DROP TABLE purchasing.proposal_requests;
