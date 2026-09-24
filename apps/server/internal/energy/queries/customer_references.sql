-- name: RepointSupplyPeriodsCustomer :execrows
-- RepointSupplyPeriodsCustomer is energy's half of a customer merge (customers
-- merge design D1, contracts.CustomerReferenceHolder): every supply period of
-- the absorbed customer supplies the survivor, inside the customers module's
-- own transaction. supply_periods_no_overlap is scoped to metering_point_id,
-- so re-pointing the customer cannot violate it. No index is keyed by
-- customer_id, so this reads the table: a merge is a rare, deliberate act, and
-- an index every supply-period write pays for would be for this one statement.
UPDATE energy.supply_periods
SET customer_id = @into_customer_id::integer
WHERE customer_id = @from_customer_id::integer;
