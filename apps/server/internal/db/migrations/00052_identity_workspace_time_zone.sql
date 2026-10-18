-- +goose Up
-- The workspace's business time zone and the user's personal one (time zones
-- design, docs/superpowers/specs/2026-10-19-time-zones-design.md, D2 and D5).
--
-- business_time_zone is the one calendar every bookkeeping day in the
-- installation is judged in: an invoice's issue day, a period lock, a trip's
-- departure day, Staff's "today". It was Staff's and Expenses' own column
-- until now (00042, 00013), two copies nothing checked against each other,
-- and Invoices' and Projects' hard-coded Europe/Oslo; 00053 and 00054 drop
-- the two columns, and every module reads this one through
-- contracts.BusinessCalendar. It lives on identity's singleton row because
-- identity is the one schema always migrated and the one module always
-- mounted, which Staff could not have served Invoices from. An IANA name
-- (varchar(64) holds every one of them), defaulted to Europe/Oslo. PUT
-- /owner/workspace checks a new one against Go's tzdata and then asks this
-- database what it means by the same name — the UTC offset at a winter
-- instant and a summer one, compared with Go's — so a name the two read
-- differently ('CET' is an abbreviation to Postgres, a zone to Go) is refused
-- rather than stored and later disagreeing with itself.
--
-- users.time_zone is a person's own zone: how an instant is shown to them,
-- and what "09:00" means when they schedule something. NULL means "follow the
-- business time zone", resolved at read time — not a copy taken at creation,
-- which would go stale the day the business zone changed, and which a user
-- created by SCIM or by Staff's provisioning has nobody to choose for them.
-- Delivery F of the design gives it a setting; the column is added here so
-- one migration carries the design's two columns.
ALTER TABLE identity.system_settings
    ADD COLUMN business_time_zone varchar(64) NOT NULL DEFAULT 'Europe/Oslo';

ALTER TABLE identity.users
    ADD COLUMN time_zone varchar(64);

-- +goose Down
ALTER TABLE identity.users DROP COLUMN time_zone;
ALTER TABLE identity.system_settings DROP COLUMN business_time_zone;
