import type { ConsumptionAggregate, CustomerMeteringPoint } from "../api/energy";

export const formatKwh = (value: number | null) =>
  value === null ? "—" : `${new Intl.NumberFormat("nb-NO", { maximumFractionDigits: 0 }).format(value)} kWh`;

export const summarizeCustomerEnergy = (meters: CustomerMeteringPoint[], aggregates: ConsumptionAggregate[]) => {
  const expected = meters
    .map((item) => item.meteringPoint.expectedAnnualConsumptionKwh)
    .filter((value): value is number => typeof value === "number");
  return {
    meteringPoints: meters.length,
    expectedAnnualKwh: expected.length > 0 ? expected.reduce((sum, value) => sum + value, 0) : null,
    lastYearKwh: aggregates.length > 0 ? aggregates.reduce((sum, aggregate) => sum + aggregate.quantityKwh, 0) : null,
    activeSupplyPeriods: meters.flatMap((item) => item.supplyPeriods).filter((period) => period.status === "Active")
      .length,
  };
};
