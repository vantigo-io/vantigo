-- +goose Up
-- The calendar's invitations (calendar invitations design D1, D3, D5, D6): an
-- attendee is one of three kinds — a user, a customer's contact, or an address
-- typed in — and the two outside kinds carry what their invitation needs: the
-- RSVP token's hash, the link sealed so a resend carries the same one, and the
-- record of the last send. A contact is an opaque id into Customers, its name
-- and address read through the directory and never copied here. By house style
-- no CHECK holds the kinds to their columns: the module's validation does, and
-- reports them per field.
ALTER TABLE calendar.appointment_attendees
    ALTER COLUMN user_id DROP NOT NULL,
    ADD COLUMN kind                   varchar(8)   NOT NULL DEFAULT 'user', -- 'user' | 'contact' | 'email'
    ADD COLUMN contact_id             integer,                              -- opaque: Customers' contact
    ADD COLUMN email                  varchar(254),                         -- 'email' kind: lower-cased
    ADD COLUMN name                   varchar(200),                         -- 'email' kind: as typed, may be null
    ADD COLUMN rsvp_token_sha256      char(64),
    ADD COLUMN rsvp_link_ciphertext   text,                                 -- the link, sealed, so a resend carries the same one
    ADD COLUMN invited_at             timestamptz,                          -- the last successful send
    ADD COLUMN invited_sequence       integer,                              -- the sequence that send carried
    ADD COLUMN send_failed_at         timestamptz;                          -- the last failed send since invited_at, cleared by a success

-- One row per user, per contact and per address on an appointment, and one
-- attendee per RSVP token.
DROP INDEX calendar.ux_appointment_attendees;
CREATE UNIQUE INDEX ux_appointment_attendees_user    ON calendar.appointment_attendees (appointment_id, user_id)    WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX ux_appointment_attendees_contact ON calendar.appointment_attendees (appointment_id, contact_id) WHERE contact_id IS NOT NULL;
CREATE UNIQUE INDEX ux_appointment_attendees_email   ON calendar.appointment_attendees (appointment_id, email)      WHERE kind = 'email';
CREATE UNIQUE INDEX ux_appointment_attendees_rsvp    ON calendar.appointment_attendees (rsvp_token_sha256)          WHERE rsvp_token_sha256 IS NOT NULL;

-- A delete of an appointment someone outside has been sent an invitation for
-- becomes a cancellation (D6): the row stays, read-only and off every
-- calendar, until the calendar-cancellations worker erases it after its last
-- day.
ALTER TABLE calendar.appointments ADD COLUMN cancelled_at timestamptz;

-- +goose Down
-- The baseline holds only colleagues, so the rows of the two outside kinds are
-- deleted first: user_id is NOT NULL again, and they have none. A cancelled
-- appointment loses its cancelled_at and is a live one again.
DELETE FROM calendar.appointment_attendees WHERE kind <> 'user';

ALTER TABLE calendar.appointments DROP COLUMN cancelled_at;

DROP INDEX calendar.ux_appointment_attendees_rsvp;
DROP INDEX calendar.ux_appointment_attendees_email;
DROP INDEX calendar.ux_appointment_attendees_contact;
DROP INDEX calendar.ux_appointment_attendees_user;
CREATE UNIQUE INDEX ux_appointment_attendees ON calendar.appointment_attendees (appointment_id, user_id);

ALTER TABLE calendar.appointment_attendees
    DROP COLUMN send_failed_at,
    DROP COLUMN invited_sequence,
    DROP COLUMN invited_at,
    DROP COLUMN rsvp_link_ciphertext,
    DROP COLUMN rsvp_token_sha256,
    DROP COLUMN name,
    DROP COLUMN email,
    DROP COLUMN contact_id,
    DROP COLUMN kind,
    ALTER COLUMN user_id SET NOT NULL;
