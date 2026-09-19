import { describe, expect, it } from "vitest";
import { jsonResponse, runQuery, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import {
  assignableRateUsersQueryOptions,
  createPersonRate,
  deletePersonRate,
  personRatesQueryOptions,
  updatePersonRate,
} from "./rates";

const USER = "22222222-2222-2222-2222-222222222222";

describe("personRatesQueryOptions", () => {
  it("lists every rate card when no user is given", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = personRatesQueryOptions();
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "rates", "all"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/rates");
  });

  it("reads one user's rates", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = personRatesQueryOptions(USER);
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "rates", USER]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe(`/api/v1/time/rates/users/${USER}`);
  });
});

describe("assignableRateUsersQueryOptions", () => {
  it("asks for the first users when nothing is typed", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = assignableRateUsersQueryOptions("");
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "rates", "assignable-users", ""]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/rates/assignable-users");
  });

  it("searches on the term, trimmed", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, [])));

    const options = assignableRateUsersQueryOptions("  Grace  ");
    await runQuery(options);

    expect(options.queryKey).toEqual(["time", "rates", "assignable-users", "Grace"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/rates/assignable-users?search=Grace");
  });
});

describe("rate writes", () => {
  it("creates a rate", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(201, {})));
    const input = { userId: USER, validFrom: "2026-01-01", billRate: 1200, costRate: 600, currency: "NOK" };
    await createPersonRate(input);
    expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/time/rates", body: input });
  });

  it("updates a rate", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, {})));
    const input = { validFrom: "2026-02-01", billRate: null, costRate: 650, currency: "NOK" };
    await updatePersonRate(7, input);
    expect(sent(fetchMock, "PUT")).toEqual({ url: "/api/v1/time/rates/7", body: input });
  });

  it("deletes a rate", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 204 })));
    await deletePersonRate(7);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/time/rates/7");
    expect(fetchMock.actualCalls[0]?.[1]?.method).toBe("DELETE");
  });
});
