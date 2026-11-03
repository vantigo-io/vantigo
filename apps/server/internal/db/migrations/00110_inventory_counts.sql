-- +goose Up
-- Counts, and quantities entered in packagings (inventory design I16–I20,
-- I25, delivery B).
--
-- A count covers a site, or one location and its subtree (scope_location_id;
-- the root for the whole site). Opening it freezes the expected quantity of
-- every balance in scope as a line; lines take what was counted, and a
-- variant found where no line exists gets one, added. Posting writes one
-- count movement of the differences between counted and the balance at
-- posting, and keeps its id here; cancelling posts nothing. The opener,
-- poster, canceller and each counter are snapshotted as movements keep their
-- poster: a count is a record of the stock ledger.
CREATE TABLE inventory.counts (
    id                     bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    site_id                bigint        NOT NULL REFERENCES inventory.sites (id),
    scope_location_id      bigint        NOT NULL REFERENCES inventory.locations (id),
    status                 varchar(10)   NOT NULL,
    note                   varchar(1000),
    opened_by_user_id      uuid,
    opened_by_display      varchar(200),
    opened_at              timestamptz   NOT NULL,
    posted_by_user_id      uuid,
    posted_by_display      varchar(200),
    posted_at              timestamptz,
    cancelled_by_user_id   uuid,
    cancelled_by_display   varchar(200),
    cancelled_at           timestamptz,
    uncounted              varchar(4),
    movement_id            bigint        REFERENCES inventory.movements (id),
    revision               integer       NOT NULL DEFAULT 1
);
CREATE INDEX ix_counts_site_status ON inventory.counts (site_id, status);
CREATE INDEX ix_counts_status_opened ON inventory.counts (status, opened_at);

-- A count's line: one variant on one location, what was expected when the
-- count opened, and what was counted, in the variant's unit, with the
-- packagings it was entered in when it was.
CREATE TABLE inventory.count_lines (
    id                 bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    count_id           bigint        NOT NULL REFERENCES inventory.counts (id),
    variant_id         integer       NOT NULL,
    location_id        bigint        NOT NULL REFERENCES inventory.locations (id),
    expected           numeric(14,3) NOT NULL,
    counted            numeric(14,3),
    entered            jsonb,
    added              boolean       NOT NULL DEFAULT false,
    counted_by_user_id uuid,
    counted_by_display varchar(200),
    counted_at         timestamptz
);
CREATE UNIQUE INDEX ux_count_lines_count_variant_location ON inventory.count_lines (count_id, variant_id, location_id);
CREATE INDEX ix_count_lines_location ON inventory.count_lines (location_id);

-- A movement line entered in packagings keeps the entry as it was at
-- posting — names, counts and quantities per packaging, and the loose rest —
-- so history reads "3 Euro pallet + 2 Box" after the packagings change
-- (I25). A count movement names its count.
ALTER TABLE inventory.movement_lines ADD COLUMN entered jsonb;
ALTER TABLE inventory.movements ADD COLUMN count_id bigint REFERENCES inventory.counts (id);
CREATE INDEX ix_movements_count ON inventory.movements (count_id) WHERE count_id IS NOT NULL;

-- +goose Down
DROP INDEX inventory.ix_movements_count;
ALTER TABLE inventory.movements DROP COLUMN count_id;
ALTER TABLE inventory.movement_lines DROP COLUMN entered;
DROP TABLE inventory.count_lines;
DROP TABLE inventory.counts;
