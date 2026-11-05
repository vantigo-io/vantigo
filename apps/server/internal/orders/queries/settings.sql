-- name: GetSettings :one
-- GetSettings reads the one settings row. No row means nothing was ever
-- saved, which the caller reads from pgx.ErrNoRows as the defaults.
SELECT * FROM orders.settings WHERE id = 1;

-- name: LockSettings :one
-- LockSettings reads the settings row under its lock, for a save that judges
-- the series starts against the counters (O12).
SELECT * FROM orders.settings WHERE id = 1 FOR UPDATE;

-- name: UpsertSettings :one
-- UpsertSettings writes the one settings row, creating it on the first save,
-- and bumps its revision.
INSERT INTO orders.settings (
    id, quote_series_start, order_series_start, quote_validity_days, default_payment_terms_days,
    send_channel, confirmation_on_confirm, combine_by_default, mail_texts, subscription_final_period_default,
    default_site_id, freight_variant_id, freight_markup_percent, shipping_notice_on_book, revision, updated_at
) VALUES (
    1, sqlc.arg(quote_series_start), sqlc.arg(order_series_start), sqlc.arg(quote_validity_days),
    sqlc.arg(default_payment_terms_days), sqlc.narg(send_channel), sqlc.arg(confirmation_on_confirm),
    sqlc.arg(combine_by_default), sqlc.arg(mail_texts), sqlc.arg(subscription_final_period_default), sqlc.narg(default_site_id),
    sqlc.narg(freight_variant_id), sqlc.arg(freight_markup_percent), sqlc.arg(shipping_notice_on_book), 1, sqlc.arg(updated_at)
)
ON CONFLICT (id) DO UPDATE SET
    quote_series_start         = EXCLUDED.quote_series_start,
    order_series_start         = EXCLUDED.order_series_start,
    quote_validity_days        = EXCLUDED.quote_validity_days,
    default_payment_terms_days = EXCLUDED.default_payment_terms_days,
    send_channel               = EXCLUDED.send_channel,
    confirmation_on_confirm    = EXCLUDED.confirmation_on_confirm,
    combine_by_default         = EXCLUDED.combine_by_default,
    mail_texts                 = EXCLUDED.mail_texts,
    subscription_final_period_default = EXCLUDED.subscription_final_period_default,
    default_site_id            = EXCLUDED.default_site_id,
    freight_variant_id         = EXCLUDED.freight_variant_id,
    freight_markup_percent     = EXCLUDED.freight_markup_percent,
    shipping_notice_on_book    = EXCLUDED.shipping_notice_on_book,
    revision                   = orders.settings.revision + 1,
    updated_at                 = EXCLUDED.updated_at
RETURNING *;
