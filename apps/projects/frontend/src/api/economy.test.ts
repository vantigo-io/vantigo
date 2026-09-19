import { describe, expect, it } from "vitest";
import type { EconomyPortfolioSearch } from "../lib/economy";
import { stubFetch } from "../test/fetch";
import { economyPortfolioQueryOptions, projectEconomyQueryOptions } from "./economy";
import { ApiValidationError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

const params = (overrides: Partial<EconomyPortfolioSearch> = {}): EconomyPortfolioSearch => ({
  page: 1,
  search: "",
  status: "active",
  overBudget: false,
  hasReady: false,
  sort: "budgetUsed",
  ...overrides,
});

const askedFor = (fetchMock: { mock: { calls: unknown[][] } }) =>
  new URL(String(fetchMock.mock.calls[0][0]), "http://localhost");

describe("projectEconomyQueryOptions", () => {
  it("reads one project's economy under the projects key", async () => {
    const economy = { timeTracking: true, budget: {}, lines: [], overBudget: false };
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, economy)));

    const options = projectEconomyQueryOptions(7);
    const result = await runQuery(options);

    expect(result).toEqual(economy);
    expect(options.queryKey).toEqual(["projects", "economy", "project", 7]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/7/economy", { signal: undefined });
  });
});

describe("economyPortfolioQueryOptions", () => {
  it("asks for the active projects, most-used first, one page at a time", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: {}, totals: {}, timeTracking: true })),
    );

    const options = economyPortfolioQueryOptions(params());
    await runQuery(options);

    const url = askedFor(fetchMock as never);
    expect(url.pathname).toBe("/api/v1/projects/economy");
    expect(Object.fromEntries(url.searchParams)).toEqual({
      page: "1",
      pageSize: "25",
      status: "active",
      sort: "budgetUsed",
    });
    expect(options.queryKey).toEqual(["projects", "economy", "portfolio", params()]);
  });

  it("carries every filter the toolbar can set", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: {}, totals: {}, timeTracking: true })),
    );

    await runQuery(
      economyPortfolioQueryOptions(
        params({
          page: 3,
          search: "  web  ",
          status: "all",
          customerId: 1001,
          overBudget: true,
          hasReady: true,
          sort: "readyAmount",
        }),
      ),
    );

    expect(Object.fromEntries(askedFor(fetchMock as never).searchParams)).toEqual({
      page: "3",
      pageSize: "25",
      search: "web",
      status: "all",
      customerId: "1001",
      overBudget: "true",
      hasReady: "true",
      sort: "readyAmount",
    });
  });

  // `false` and absent mean the same thing to the API, so a filter that is off
  // is left out rather than sent as a no-op the URL has to carry.
  it("leaves the two switches out while they are off, and an empty search too", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: {}, totals: {}, timeTracking: true })),
    );

    await runQuery(economyPortfolioQueryOptions(params({ search: "   " })));

    const asked = askedFor(fetchMock as never).searchParams;
    expect(asked.has("overBudget")).toBe(false);
    expect(asked.has("hasReady")).toBe(false);
    expect(asked.has("search")).toBe(false);
  });

  // Too many projects for one answer comes back as a 400 naming `status`, the
  // parameter the caller is asked to narrow.
  it("carries the 'too many projects' refusal back on the status field", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(400, {
          title: "Invalid project",
          errors: { status: ["More than 2000 projects match, which is more than one answer can carry."] },
        }),
      ),
    );

    const error = await runQuery(economyPortfolioQueryOptions(params())).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors.status).toBe(
      "More than 2000 projects match, which is more than one answer can carry.",
    );
  });
});
