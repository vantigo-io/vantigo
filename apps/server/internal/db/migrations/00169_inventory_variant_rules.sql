-- +goose Up
-- A variant's rules in Inventory (manufacturing phase 2 M100): so far one,
-- inspect_on_receipt — a receipt of the variant puts every lot it creates or
-- adds to under a quarantine hold of cause inspection from the receipt,
-- released on the lot page. Only a tracked variant may require it, judged by
-- the module under the catalog's lock. A variant with no row has no rule.
-- updated_by_user_id is identity's user, opaque here, with the display as it
-- was.
CREATE TABLE inventory.variant_rules (
    variant_id          integer      PRIMARY KEY,
    inspect_on_receipt  boolean      NOT NULL DEFAULT false,
    updated_by_user_id  uuid,
    updated_by_display  varchar(200),
    updated_at          timestamptz  NOT NULL,
    revision            integer      NOT NULL DEFAULT 1
);

-- +goose Down
DROP TABLE inventory.variant_rules;
