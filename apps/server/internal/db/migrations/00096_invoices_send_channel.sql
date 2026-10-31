-- +goose Up
-- The channel Invoices sends documents and reminder letters from (integrations
-- and configuration tiers design T4, T6,
-- docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md;
-- delivery B, docs/superpowers/plans/2026-10-31-outbound-mail-slot.md).
--
-- send_channel is the id of a Communications channel the Owner chose in Invoice
-- settings, or NULL for Disabled: nothing is sent, and a customer is told on
-- paper. It names a channel in another module's schema, so by house style
-- (module-boundaries rule 4) it holds no foreign key: Communications answers
-- through contracts.OutboundMail whether the id is still a channel that can
-- send, and a channel that goes away leaves the setting as it was, shown as
-- gone, until an Owner chooses again.
ALTER TABLE invoices.settings
    ADD COLUMN send_channel uuid;

-- +goose Down
ALTER TABLE invoices.settings
    DROP COLUMN send_channel;
