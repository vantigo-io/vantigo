-- +goose Up
-- A conversation with a virtual employee (virtual employees design V31): the
-- panel's person talks to an agent instead of the Assistant. agent_id is the
-- agents module's id of the agent, an opaque id with no foreign key (module
-- boundaries rule 4), null for the Assistant's own conversations. The run is
-- still the person's own, under their session; the agent narrows what it may
-- use and gives it its persona.
ALTER TABLE assistant.conversations ADD COLUMN agent_id uuid;

-- +goose Down
ALTER TABLE assistant.conversations DROP COLUMN agent_id;
