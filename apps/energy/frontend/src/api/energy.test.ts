import { describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  assignSupplyPeriod,
  consumptionAggregateQueryOptions,
  createMeteringPoint,
  customerConsumptionAggregateQueryOptions,
  meteringPointsQueryOptions,
  metersQueryOptions,
  replaceMeter,
} from "./energy";

const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("energy api", () => {
  it("gets metering point consumption aggregates with resolution in the query key and URL", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(response([])));
    const options = consumptionAggregateQueryOptions(4, {
      from: "2026-01-01T00:00:00.000Z",
      to: "2026-02-01T00:00:00.000Z",
      resolution: "day",
    });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(options.queryKey).toEqual([
      "energy",
      "metering-points",
      4,
      "consumption",
      "aggregate",
      { from: "2026-01-01T00:00:00.000Z", to: "2026-02-01T00:00:00.000Z", resolution: "day" },
    ]);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/api/v1/energy/metering-points/4/consumption/aggregate?from=2026-01-01T00%3A00%3A00.000Z&to=2026-02-01T00%3A00%3A00.000Z&resolution=day",
    );
  });

  it("gets customer consumption aggregates with an optional metering point", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(response([])));
    const options = customerConsumptionAggregateQueryOptions(1001, {
      meteringPointId: 4,
      from: "2026-01-01",
      to: "2026-02-01",
      resolution: "month",
    });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(options.queryKey).toEqual([
      "energy",
      "customers",
      1001,
      "consumption",
      "aggregate",
      { meteringPointId: 4, from: "2026-01-01", to: "2026-02-01", resolution: "month" },
    ]);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/api/v1/energy/customers/1001/consumption/aggregate?meteringPointId=4&from=2026-01-01&to=2026-02-01&resolution=month",
    );
  });

  it("lists metering points using the energy endpoint and preserves query keys", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(response({ data: [], pagination: { totalPages: 1 } })));
    const options = meteringPointsQueryOptions({ page: 2, pageSize: 10, search: "123" });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(options.queryKey).toEqual(["energy", "metering-points", { page: 2, pageSize: 10, search: "123" }]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/energy/metering-points?page=2&pageSize=10&search=123", {
      signal: undefined,
      credentials: "include",
      headers: expect.any(Headers),
    });
  });

  it("creates a point and reports an overlapping supply period", async () => {
    const fetchMock = stubFetch(
      vi
        .fn()
        .mockResolvedValueOnce(response({ id: 1 }))
        .mockResolvedValueOnce(response({ detail: "This period overlaps an existing period." }, 409)),
    );
    await createMeteringPoint({
      gsrn: "123456789012345678",
      meterNumber: "M-1",
      address: { streetAddress: "Main 1", postalCode: "0001", city: "Oslo", countryCode: "NO" },
      priceArea: "NO1",
    });
    await expect(assignSupplyPeriod(1, { customerId: 7, start: "2026-01-01" })).rejects.toThrow(
      "This period overlaps an existing period.",
    );
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/v1/energy/metering-points/1/supply-periods");
  });

  it("lists and replaces meters through the metering point endpoints", async () => {
    const fetchMock = stubFetch(
      vi
        .fn()
        .mockResolvedValue(response([]))
        .mockResolvedValueOnce(response([{ id: 1 }])),
    );
    const options = metersQueryOptions(4);
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(options.queryKey).toEqual(["energy", "metering-points", 4, "meters"]);
    await replaceMeter(4, { meterNumber: "M-2", installedAt: "2026-08-11T00:00:00.000Z" });
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/v1/energy/metering-points/4/meters");
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      method: "POST",
      body: JSON.stringify({ meterNumber: "M-2", installedAt: "2026-08-11T00:00:00.000Z" }),
    });
  });
});
