-- +goose Up
-- The calendar's first workspace setting: the channel invitations to people
-- outside the company are sent from (integrations and configuration tiers
-- design T6,
-- docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md;
-- delivery B, docs/superpowers/plans/2026-10-31-outbound-mail-slot.md).
--
-- One row, id 1, inserted below with no channel. invitation_channel is the id
-- of a Communications channel an Owner - or a holder of calendar:manage -
-- chose, or NULL for off: nothing is sent, nothing is marked, and the
-- appointment says invitations are off. It names a channel in another
-- module's schema, so by house style (module-boundaries rule 4) it holds no
-- foreign key: Communications answers through contracts.OutboundMail whether
-- the id is still a channel that can send, and a channel that goes away
-- leaves the setting as it was, shown as gone, until someone chooses again.
-- revision is the one a PUT's If-Match names.
CREATE TABLE calendar.settings (
    id                 smallint    PRIMARY KEY,
    invitation_channel uuid,
    updated_at         timestamptz NOT NULL,
    revision           integer     NOT NULL DEFAULT 1,
    CONSTRAINT ck_settings_single_row CHECK (id = 1)
);
INSERT INTO calendar.settings (id, updated_at) VALUES (1, now());

-- +goose Down
DROP TABLE calendar.settings;
