-- name: CustomerSupplyPeriodsForExport :many
-- CustomerSupplyPeriodsForExport is a private person's supply periods for
-- their export (customers GDPR design D2, contracts.CustomerPersonalData),
-- each with the metering point's address — for a residential customer that is
-- very likely their home, which is exactly why it belongs in the file. No index
-- is keyed by customer_id (RepointSupplyPeriodsCustomer's reasoning holds: an
-- export is rare and deliberate).
SELECT sp.id, sp.start, sp."end", sp.status,
       mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code
FROM energy.supply_periods sp
JOIN energy.metering_points mp ON mp.id = sp.metering_point_id
WHERE sp.customer_id = @customer_id::integer
ORDER BY sp.start, sp.id;
