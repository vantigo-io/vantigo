-- +goose Up
-- The work type an entry was logged as (work types design D3), snapshotted
-- the way its rates are: the project's work-type id, its name as it stood,
-- and its two multipliers — all four NULL when no type was picked, ordinary
-- hours being the absence of a type, not a row. Resolved at every save while
-- the entry is a draft or rejected, frozen from submitted on with the rates.
--
-- bill_rate and cost_rate stay the base rates the chain resolved: the
-- multiplier sits beside them and is applied where amounts are summed
-- (queries/actuals.sql), so nothing is rounded twice and the snapshot still
-- says what the rate was and what multiplied it. numeric(6,2) is the scale
-- projects stores the percentages in, so a snapshot never rounds one.
-- work_type_id is opaque — work types are projects' rows
-- (docs/module-boundaries.md rule 4) — and no CHECK ties the four together,
-- house style: the save writes all four or none.
ALTER TABLE time.entries
    ADD COLUMN work_type_id            integer,
    ADD COLUMN work_type_name          varchar(100),
    ADD COLUMN bill_multiplier_percent numeric(6,2),
    ADD COLUMN cost_multiplier_percent numeric(6,2);

-- +goose Down
ALTER TABLE time.entries
    DROP COLUMN work_type_id,
    DROP COLUMN work_type_name,
    DROP COLUMN bill_multiplier_percent,
    DROP COLUMN cost_multiplier_percent;
