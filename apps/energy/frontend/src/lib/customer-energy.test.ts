import { describe, expect, it } from "vitest";
import type { ConsumptionAggregate, CustomerMeteringPoint } from "../api/energy";
import { summarizeCustomerEnergy } from "./customer-energy";

const meter = (overrides: {
  id: number;
  expected?: number;
  periods?: CustomerMeteringPoint["supplyPeriods"];
}): CustomerMeteringPoint => ({
  meteringPoint: {
    id: overrides.id,
    gsrn: `70705750000000000${overrides.id}`,
    meterNumber: `M-${overrides.id}`,
    address: { streetAddress: "Main 1", postalCode: "0001", city: "Oslo", countryCode: "NO" },
    priceArea: "NO1",
    expectedAnnualConsumptionKwh: overrides.expected,
    connectionStatus: "Connected",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  },
  supplyPeriods: overrides.periods ?? [],
});

const aggregate = (quantityKwh: number): ConsumptionAggregate => ({
  meteringPointId: 1,
  bucketStart: "2026-01-01T00:00:00Z",
  bucketEnd: "2026-02-01T00:00:00Z",
  quantityKwh,
  intervalCount: 1,
  hasEstimated: false,
});

describe("summarizeCustomerEnergy", () => {
  it("sums expected consumption, actual aggregates and counts active periods", () => {
    const period = { id: 1, meteringPointId: 1, customerId: 7, start: "2026-01-01T00:00:00Z", end: null };
    const stats = summarizeCustomerEnergy(
      [
        meter({ id: 1, expected: 10000, periods: [{ ...period, status: "Active" }] }),
        meter({ id: 2, expected: 2500, periods: [{ ...period, id: 2, meteringPointId: 2, status: "Ended" }] }),
        meter({ id: 3 }),
      ],
      [aggregate(120.5), aggregate(79.5)],
    );
    expect(stats).toEqual({
      meteringPoints: 3,
      expectedAnnualKwh: 12500,
      lastYearKwh: 200,
      activeSupplyPeriods: 1,
    });
  });

  it("reports missing data as null instead of zero", () => {
    const stats = summarizeCustomerEnergy([meter({ id: 1 })], []);
    expect(stats.expectedAnnualKwh).toBeNull();
    expect(stats.lastYearKwh).toBeNull();
  });
});
