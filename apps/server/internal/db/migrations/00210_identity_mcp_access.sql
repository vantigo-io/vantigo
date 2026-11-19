-- +goose Up
-- What an integration's API keys may do at the MCP endpoint, /api/mcp (MCP
-- server design M1): 'off' refuses them there, 'read' lets an agent search,
-- describe and call read operations, 'read_write' lets it write too. Every
-- integration starts 'off': its keys were issued for REST, and an agent holding
-- one can be steered by the data it reads.
ALTER TABLE identity.integrations
    ADD COLUMN mcp_access text NOT NULL DEFAULT 'off'
        CHECK (mcp_access IN ('off', 'read', 'read_write'));

-- +goose Down
ALTER TABLE identity.integrations DROP COLUMN mcp_access;
