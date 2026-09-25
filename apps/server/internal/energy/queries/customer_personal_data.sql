-- name: CustomerSupplyPeriodsForExport :many
-- CustomerSupplyPeriodsForExport is a private person's supply periods for
-- their export (customers GDPR design D2, contracts.CustomerPersonalData),
-- each with the metering point's address — for a residential customer that is
-- very likely their home, which is exactly why it belongs in the file. No index
-- is keyed by customer_id (RepointSupplyPeriodsCustomer's reasoning holds: an
-- export is rare and deliberate).
SELECT sp.id, sp.start, sp."end", sp.status,
       mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code, mp.price_area
FROM energy.supply_periods sp
JOIN energy.metering_points mp ON mp.id = sp.metering_point_id
WHERE sp.customer_id = @customer_id::integer
ORDER BY sp.start, sp.id;

-- name: SupplyPeriodConsumptionByMonth :many
-- SupplyPeriodConsumptionByMonth is one supply period's consumption for a
-- private person's export (customers GDPR design D2, widened by the
-- whole-branch review): a household's meter readings are the person's data.
-- Monthly sums of the current intervals inside the period's dates — the
-- containment ListConsumptionByCustomer uses — each month a calendar month in
-- the metering point's market zone, AggregateConsumptionByMeteringPoint's
-- double AT TIME ZONE idiom. Monthly rather than hourly keeps a file for years
-- of supply readable; the hourly series stays energy's own endpoint's.
SELECT to_char(date_trunc('month', c."start" AT TIME ZONE @time_zone::text), 'YYYY-MM')::text AS month,
       SUM(c.quantity_kwh)::numeric AS quantity_kwh
FROM energy.consumption_intervals c
JOIN energy.supply_periods p ON p.metering_point_id = c.metering_point_id
WHERE p.id = @supply_period_id::integer
  AND c.is_current
  AND c."start" >= p."start"
  AND (p."end" IS NULL OR c."end" <= p."end")
GROUP BY 1
ORDER BY 1;
