-- +goose Up
-- Settlements (energy consumption billing design D5, energy settlements plan
-- reading 1): what a customer owes for a metering point over one billing
-- period of a supply period, priced by Energy from the intervals, the spot
-- prices and the tariffs, and handed to Invoices across
-- contracts.ConsumptionProvider. A draft is recomputed on every read and
-- written when a figure changed (revision bumped); an invoiced one is frozen
-- with the invoice's stamp (the invoiced-work holder, design D6); a released
-- one was returned by a credit note and is kept as history. A correction is
-- the difference between a period recomputed and what was invoiced for it.
CREATE TABLE energy.settlements (
    id                      bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    supply_period_id        integer       NOT NULL REFERENCES energy.supply_periods (id) ON DELETE RESTRICT,
    metering_point_id       integer       NOT NULL REFERENCES energy.metering_points (id) ON DELETE RESTRICT,
    -- Opaque, copied from the supply period; re-pointed by a customer merge.
    customer_id             integer       NOT NULL,
    kind                    varchar(12)   NOT NULL,
    corrects_id             bigint        REFERENCES energy.settlements (id) ON DELETE RESTRICT,
    period_start            timestamptz   NOT NULL,
    period_end              timestamptz   NOT NULL,
    state                   varchar(10)   NOT NULL DEFAULT 'draft',
    revision                integer       NOT NULL DEFAULT 1,
    kwh                     numeric(14,3) NOT NULL,
    estimated               boolean       NOT NULL DEFAULT false,
    interval_count          integer       NOT NULL,
    expected_interval_count integer       NOT NULL,
    blocked                 varchar(20),
    blocked_detail          varchar(200),
    amount                  numeric(14,2) NOT NULL,
    currency                char(3)       NOT NULL DEFAULT 'NOK',
    contract_name           varchar(120)  NOT NULL DEFAULT '',
    computed_at             timestamptz   NOT NULL,
    -- The last time a read answered this row: drafts nobody has touched for
    -- thirty days are deleted (plan reading 7).
    touched_at              timestamptz   NOT NULL,
    created_at              timestamptz   NOT NULL,
    updated_at              timestamptz   NOT NULL,
    invoiced_invoice_id     bigint,
    invoiced_number         bigint,
    invoiced_at             timestamptz,
    invoiced_by_user_id     uuid,
    invoice_date            date,
    released_at             timestamptz,
    CONSTRAINT ck_settlements_kind CHECK (kind IN ('regular', 'correction')),
    CONSTRAINT ck_settlements_corrects CHECK ((kind = 'correction') = (corrects_id IS NOT NULL)),
    CONSTRAINT ck_settlements_period CHECK (period_end > period_start),
    CONSTRAINT ck_settlements_state CHECK (state IN ('draft', 'invoiced', 'released')),
    CONSTRAINT ck_settlements_revision CHECK (revision >= 1),
    CONSTRAINT ck_settlements_counts CHECK (interval_count >= 0 AND expected_interval_count >= 0),
    CONSTRAINT ck_settlements_blocked CHECK (blocked IS NULL OR blocked IN (
        'period_open', 'no_tariff', 'no_consumption', 'incomplete', 'price_gap', 'price_preliminary'
    )),
    -- The stamp is whole on an invoiced row, absent on a draft, and kept as
    -- history on a released one, which also carries when it was released.
    CONSTRAINT ck_settlements_stamp CHECK (
        (state = 'draft' AND invoiced_invoice_id IS NULL AND invoiced_number IS NULL AND invoiced_at IS NULL
            AND invoiced_by_user_id IS NULL AND invoice_date IS NULL AND released_at IS NULL)
        OR (state = 'invoiced' AND invoiced_invoice_id IS NOT NULL AND invoiced_number IS NOT NULL
            AND invoiced_at IS NOT NULL AND invoice_date IS NOT NULL AND released_at IS NULL)
        OR (state = 'released' AND released_at IS NOT NULL)
    )
);

-- The floor (design D5): one live regular settlement per period of a supply
-- period, and one live correction per period at a time.
CREATE UNIQUE INDEX ux_settlements_live ON energy.settlements (supply_period_id, period_start, kind)
    WHERE state IN ('draft', 'invoiced');
CREATE INDEX ix_settlements_metering_point_id ON energy.settlements (metering_point_id, period_start);
CREATE INDEX ix_settlements_customer_id ON energy.settlements (customer_id);
CREATE INDEX ix_settlements_corrects_id ON energy.settlements (corrects_id) WHERE corrects_id IS NOT NULL;
CREATE INDEX ix_settlements_draft_touched ON energy.settlements (touched_at) WHERE state = 'draft';

-- A settlement's priced lines, in the order they print. band is day or night
-- for a grid_energy component with a night rate, month the calendar month a
-- grid_capacity line prices; both NULL otherwise. A correction's lines are
-- differences and may be negative.
CREATE TABLE energy.settlement_lines (
    settlement_id bigint        NOT NULL REFERENCES energy.settlements (id) ON DELETE CASCADE,
    position      smallint      NOT NULL,
    kind          varchar(20)   NOT NULL,
    text          varchar(120)  NOT NULL,
    band          varchar(10),
    month         date,
    quantity      numeric(14,3) NOT NULL,
    unit          varchar(10)   NOT NULL,
    unit_price    numeric(14,6) NOT NULL,
    amount        numeric(14,2) NOT NULL,
    vat_hint      varchar(30)   NOT NULL,
    PRIMARY KEY (settlement_id, position),
    CONSTRAINT ck_settlement_lines_kind CHECK (kind IN (
        'spot_energy', 'fixed_energy', 'markup', 'monthly_fee',
        'grid_energy', 'grid_capacity', 'grid_fixed', 'electricity_tax', 'enova_levy'
    )),
    CONSTRAINT ck_settlement_lines_band CHECK (band IS NULL OR band IN ('day', 'night')),
    CONSTRAINT ck_settlement_lines_unit CHECK (unit IN ('kWh', 'month', 'year', 'pcs')),
    CONSTRAINT ck_settlement_lines_vat_hint CHECK (vat_hint IN ('standard', 'exempt_household_north'))
);

-- +goose Down
DROP TABLE energy.settlement_lines;
DROP TABLE energy.settlements;
