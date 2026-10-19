-- +goose Up
CREATE SCHEMA calendar;

-- The calendar module's own rows (calendar design D2): the appointments people
-- schedule, the colleagues on them, and the exceptions of a recurring one.
-- Everything else on a person's calendar is another module's, projected at read
-- time through contracts.CalendarSource and never copied here. Every foreign
-- identifier is opaque — users, customers and projects live in other schemas
-- (docs/src/content/docs/en/contributing/module-boundaries.md rule 4) — and
-- there are no CHECK constraints: the module's validation owns the rules (one
-- pair of dates or instants set, the end after the start, the bounds) and
-- reports them per field.

-- One appointment, single or a series. A timed one is two UTC instants
-- (starts_at, ends_at exclusive), an all-day one two civil dates (start_date,
-- end_date inclusive); time_zone is the TZID it was made in, in which a
-- recurring one's rrule expands. visibility and transparency are iCalendar's
-- CLASS and TRANSP in this module's words; ical_uid and sequence exist from the
-- first row for the invitations and the feed; revision is If-Match's.
CREATE TABLE calendar.appointments (
    id               bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    owner_user_id    uuid         NOT NULL,
    title            varchar(200) NOT NULL,
    description      varchar(10000),
    location         varchar(200),
    all_day          boolean      NOT NULL,
    start_date       date,                 -- all-day: the first day
    end_date         date,                 -- all-day: the last day, inclusive
    starts_at        timestamptz,          -- timed: the instant it starts
    ends_at          timestamptz,          -- timed: the instant it ends, exclusive
    time_zone        varchar(64)  NOT NULL, -- the TZID the appointment was made in
    visibility       varchar(10)  NOT NULL, -- 'shared' | 'private'
    transparency     varchar(10)  NOT NULL, -- 'busy' | 'free'
    rrule            varchar(500),          -- RFC 5545 RRULE, no DTSTART, null when single
    series_last_date date,                  -- the last occurrence's day, null when unbounded or single
    customer_id      integer,               -- opaque: the customer it was made from
    project_id       integer,               -- opaque: the project it was made from
    ical_uid         varchar(255) NOT NULL,
    sequence         integer      NOT NULL DEFAULT 0,
    revision         integer      NOT NULL DEFAULT 1,
    created_at       timestamptz  NOT NULL,
    updated_at       timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_appointments_ical_uid ON calendar.appointments (ical_uid);
CREATE INDEX ix_appointments_owner_window ON calendar.appointments (owner_user_id, starts_at, start_date);

-- The colleagues on an appointment, the owner never among them: the owner's own
-- status is implicitly accepted and not stored.
CREATE TABLE calendar.appointment_attendees (
    id             bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    appointment_id bigint      NOT NULL REFERENCES calendar.appointments (id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL,
    status         varchar(12) NOT NULL, -- 'needs-action' | 'accepted' | 'declined' | 'tentative'
    responded_at   timestamptz,
    created_at     timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_appointment_attendees ON calendar.appointment_attendees (appointment_id, user_id);
CREATE INDEX ix_appointment_attendees_user ON calendar.appointment_attendees (user_id);

-- One occurrence of a series cancelled or moved, named by the day the series
-- put it on (calendar design D4). A move sets the pair the series uses.
CREATE TABLE calendar.appointment_exceptions (
    id              bigint      GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    appointment_id  bigint      NOT NULL REFERENCES calendar.appointments (id) ON DELETE CASCADE,
    occurrence_date date        NOT NULL, -- the occurrence's day in the appointment's zone, as the series would have made it
    cancelled       boolean     NOT NULL,
    start_date      date,                 -- a moved all-day occurrence
    end_date        date,
    starts_at       timestamptz,          -- a moved timed occurrence
    ends_at         timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE UNIQUE INDEX ux_appointment_exceptions ON calendar.appointment_exceptions (appointment_id, occurrence_date);

-- +goose Down
DROP SCHEMA calendar CASCADE;
