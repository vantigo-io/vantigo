-- +goose Up
-- What Communications knows of a Messenger participant's contact beyond the link
-- itself (Messenger design N20, N21; delivery D, contacts on Messenger). The
-- link is communications.participants.contact_id, set by an explicit act. Two
-- things sit beside it and must never be mistaken for it, so they live in a table
-- of their own and the participants' rows and every query that returns them stay
-- as they were:
--
--   * suggested_contact_id is the one contact the e-mail address the person
--     shared names (N3a). A suggestion: nothing is linked until an agent clicks.
--   * referral_contact_id is the contact whose Messenger link (N21) the person
--     used while already linked to another contact. The link is left alone and
--     the thread says so.
--
-- Both are references to Customers' contacts resolved through
-- contracts.CustomerDirectory, never foreign keys. Linking, unlinking or
-- changing the contact deletes the row.
CREATE TABLE communications.participant_contact_hints (
    participant_id        uuid        PRIMARY KEY REFERENCES communications.participants (id) ON DELETE CASCADE,
    suggested_contact_id  integer,
    referral_contact_id   integer,
    updated_at            timestamptz NOT NULL,
    CONSTRAINT ck_participant_contact_hints_any CHECK (suggested_contact_id IS NOT NULL OR referral_contact_id IS NOT NULL)
);

-- +goose Down
DROP TABLE communications.participant_contact_hints;
